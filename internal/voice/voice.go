// Package voice implements push-to-talk STT (speech-to-text) for HyperiOS.
//
// Architecture:
//   - Recorder — captures audio via arecord subprocess to a temp WAV file
//   - Transcriber — transcribes the WAV via whisper-cli subprocess
//   - Session — combines record + transcribe into a single push-to-talk operation
//
// Both recorder and transcriber use subprocess calls, avoiding CGO entirely.
// This keeps the build simple and allows the whisper.cpp version to be updated
// independently of the Go binary.
//
// Push-to-talk flow:
//  1. TUI key down (Ctrl+Space) → Session.Start()   → arecord begins
//  2. TUI key up   (Ctrl+Space) → Session.Stop()    → arecord stops
//  3.                             Session.Transcribe() → whisper-cli runs
//  4. Transcript string returned → injected into TUI input field
package voice

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Session manages a single push-to-talk voice input session.
// Create one per key-hold event; it is not reusable.
type Session struct {
	modelPath  string
	cliPath    string
	wavPath    string

	mu       sync.Mutex
	cmd      *exec.Cmd
	started  bool
	stopped  bool
	duration time.Duration
}

// NewSession creates a voice session. modelPath is the whisper GGML model file;
// cliPath is the whisper-cli binary. Both must exist.
func NewSession(modelPath, cliPath string) (*Session, error) {
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("voice: model not found at %s — run 'hyperi config set whisper-model-path <path>'", modelPath)
	}
	if _, err := exec.LookPath(cliPath); err != nil {
		if _, err2 := os.Stat(cliPath); err2 != nil {
			return nil, fmt.Errorf("voice: whisper-cli not found at %s — install whisper.cpp", cliPath)
		}
	}

	// Temp WAV file for this session
	wavPath := filepath.Join(os.TempDir(), fmt.Sprintf("hyperi-voice-%d.wav", time.Now().UnixNano()))

	return &Session{
		modelPath: modelPath,
		cliPath:   cliPath,
		wavPath:   wavPath,
	}, nil
}

// Start begins audio recording. Returns immediately; recording runs in background.
// Must be paired with Stop(). Safe to call only once.
func (s *Session) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("voice: session already started")
	}

	// arecord: ALSA command-line recorder
	// -f S16_LE: 16-bit signed little-endian (required by whisper)
	// -r 16000: 16kHz sample rate (required by whisper)
	// -c 1: mono
	// -t wav: WAV format
	s.cmd = exec.Command("arecord",
		"-f", "S16_LE",
		"-r", "16000",
		"-c", "1",
		"-t", "wav",
		s.wavPath,
	)

	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("voice: start arecord: %w", err)
	}

	s.started = true
	return nil
}

// Stop ends audio recording. Blocks until arecord exits cleanly.
// Must be called after Start(). Safe to call only once.
func (s *Session) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return fmt.Errorf("voice: session not started")
	}
	if s.stopped {
		return nil
	}

	s.stopped = true

	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}

	// Send SIGTERM to arecord — it flushes and closes the WAV file cleanly
	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		// If signal fails (process already gone), try Kill
		_ = s.cmd.Process.Kill()
	}

	// Wait for arecord to exit and flush the WAV header
	_ = s.cmd.Wait()
	return nil
}

// Transcribe runs whisper-cli on the recorded WAV and returns the transcript.
// Must be called after Stop(). Blocks until transcription is complete.
// The WAV temp file is deleted after transcription regardless of outcome.
func (s *Session) Transcribe(ctx context.Context) (string, error) {
	defer os.Remove(s.wavPath)

	s.mu.Lock()
	stopped := s.stopped
	wavPath := s.wavPath
	modelPath := s.modelPath
	cliPath := s.cliPath
	s.mu.Unlock()

	if !stopped {
		return "", fmt.Errorf("voice: must call Stop() before Transcribe()")
	}

	// Verify the WAV was actually written
	info, err := os.Stat(wavPath)
	if err != nil {
		return "", fmt.Errorf("voice: WAV file not found (recording may have failed): %w", err)
	}
	if info.Size() < 1024 {
		return "", fmt.Errorf("voice: recording too short (%.0f bytes) — hold the key longer", float64(info.Size()))
	}

	// Run whisper-cli
	// --no-prints: suppress progress output
	// --no-timestamps: clean transcript without [00:00:00.000] markers
	// -nt: same as --no-timestamps (short form)
	cmd := exec.CommandContext(ctx, cliPath,
		"-m", modelPath,
		"-f", wavPath,
		"--no-prints",
		"--no-timestamps",
	)

	out, err := cmd.Output()
	if err != nil {
		// Include stderr if available for better error messages
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("voice: whisper-cli failed: %w\n%s", err, string(exitErr.Stderr))
		}
		return "", fmt.Errorf("voice: whisper-cli: %w", err)
	}

	transcript := cleanTranscript(string(out))
	return transcript, nil
}

// IsAvailable returns true if the voice dependencies are present on this system.
// Used to determine whether to show the push-to-talk hint in the TUI.
func IsAvailable(modelPath, cliPath string) bool {
	if _, err := os.Stat(modelPath); err != nil {
		return false
	}
	if _, err := exec.LookPath("arecord"); err != nil {
		return false
	}
	if _, err := os.Stat(cliPath); err != nil {
		if _, err := exec.LookPath(cliPath); err != nil {
			return false
		}
	}
	return true
}

// cleanTranscript normalises whitespace and removes common whisper artifacts.
func cleanTranscript(raw string) string {
	s := strings.TrimSpace(raw)

	// Remove timestamp lines that sneak through despite --no-timestamps
	lines := strings.Split(s, "\n")
	var kept []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip lines that look like "[00:00:00.000 --> 00:00:05.000]"
		if strings.HasPrefix(line, "[") && strings.Contains(line, "-->") {
			continue
		}
		// Strip leading timestamp if present: "[00:00:00.000]   text"
		if strings.HasPrefix(line, "[") {
			if idx := strings.Index(line, "]"); idx >= 0 {
				line = strings.TrimSpace(line[idx+1:])
			}
		}
		if line != "" {
			kept = append(kept, line)
		}
	}

	return strings.Join(kept, " ")
}
