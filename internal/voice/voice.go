// Package voice implements the HyperiOS voice interface.
// Voice is an opt-in input path that feeds into the same agent pipeline
// as the text shell. It is NOT always-on unless the user explicitly enables it.
//
// Phase 3 implementation plan:
//   - STT (speech-to-text): local-first via whisper.cpp subprocess
//     Fallback: Anthropic/OpenAI API transcription
//   - TTS (text-to-speech): local via piper (https://github.com/rhasspy/piper)
//     Fallback: espeak-ng for minimal install
//   - Activation: push-to-talk keybind OR explicit "hyperi listen" shell command
//   - Always-on mode: optional, requires explicit user opt-in in config
//
// TODO(Phase 3): Implement STT/TTS pipeline.
package voice

import (
	"fmt"
	"os/exec"
)

// Config holds voice interface configuration.
type Config struct {
	// AlwaysOn enables continuous listening mode (off by default).
	AlwaysOn bool `yaml:"always_on"`

	// STTBackend selects the speech-to-text engine.
	// "whisper" (local, default) or "api" (cloud fallback).
	STTBackend string `yaml:"stt_backend"`

	// WhisperModel is the whisper.cpp model size.
	// "tiny", "base" (default), "small", "medium", "large"
	WhisperModel string `yaml:"whisper_model"`

	// TTSBackend selects the text-to-speech engine.
	// "piper" (local, default) or "espeak" (minimal fallback).
	TTSBackend string `yaml:"tts_backend"`

	// PiperVoice is the piper voice model to use.
	PiperVoice string `yaml:"piper_voice"`
}

// DefaultConfig returns the default voice configuration.
func DefaultConfig() Config {
	return Config{
		AlwaysOn:     false,
		STTBackend:   "whisper",
		WhisperModel: "base",
		TTSBackend:   "piper",
		PiperVoice:   "en_US-lessac-medium",
	}
}

// Interface is the voice I/O controller.
type Interface struct {
	cfg Config
}

// New creates a new voice Interface with the given configuration.
func New(cfg Config) *Interface {
	return &Interface{cfg: cfg}
}

// IsAvailable returns true if the required voice tools are installed.
func (v *Interface) IsAvailable() bool {
	switch v.cfg.STTBackend {
	case "whisper":
		_, err := exec.LookPath("whisper")
		return err == nil
	}
	return false
}

// Speak converts text to speech using the configured TTS backend.
// TODO(Phase 3): Implement piper/espeak integration.
func (v *Interface) Speak(text string) error {
	switch v.cfg.TTSBackend {
	case "piper":
		return fmt.Errorf("piper TTS not yet implemented (Phase 3)")
	case "espeak":
		cmd := exec.Command("espeak-ng", text)
		return cmd.Run()
	default:
		return fmt.Errorf("unknown TTS backend: %s", v.cfg.TTSBackend)
	}
}

// Listen records a single utterance and returns the transcribed text.
// Blocks until the user finishes speaking.
// TODO(Phase 3): Implement whisper.cpp integration.
func (v *Interface) Listen() (string, error) {
	return "", fmt.Errorf("voice input not yet implemented (Phase 3)")
}
