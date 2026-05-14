// Package shell implements the HyperiOS persistent terminal shell (TUI).
// Built on charmbracelet/bubbletea. The TUI is the primary on-device interface:
// it works headless, over SSH, without a display server.
//
// Architecture:
//   - Model (model.go) — bubbletea model; handles all rendering and user input
//   - Runner (runner.go) — pipeline runner; executes agent pipeline per intent
//   - Styles (styles.go) — lipgloss styles
//   - Shell (shell.go) — entry point; foreground lock; wires everything together
package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/isellar/hyperios/internal/audit"
	"github.com/isellar/hyperios/internal/bus"
	"github.com/isellar/hyperios/internal/capability"
	cfg "github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/manifest"
	"github.com/isellar/hyperios/internal/plan"
	"github.com/isellar/hyperios/internal/session"
)

// Shell is the HyperiOS TUI entry point.
type Shell struct {
	config     *cfg.Config
	eventBus   *bus.Bus
	registry   *capability.Registry
	validator  *capability.CommandValidator
	manifestSt *manifest.Store
	sessionMgr *session.Manager
	logger     *audit.Logger
	apiKey     string
	dataPathFn func(string) string
	logPathFn  func(string) string
	workDir    string
}

// Config holds Shell dependencies.
type Config struct {
	APIKey        string
	HypConfig     *cfg.Config
	EventBus      *bus.Bus
	Registry      *capability.Registry
	Validator     *capability.CommandValidator
	ManifestStore *manifest.Store
	SessionMgr    *session.Manager
	AuditLogger   *audit.Logger
	DataPathFn    func(string) string
	LogPathFn     func(string) string
	WorkDir       string
}

// NewShell creates a Shell from a Config.
func NewShell(c Config) *Shell {
	return &Shell{
		config:     c.HypConfig,
		eventBus:   c.EventBus,
		registry:   c.Registry,
		validator:  c.Validator,
		manifestSt: c.ManifestStore,
		sessionMgr: c.SessionMgr,
		logger:     c.AuditLogger,
		apiKey:     c.APIKey,
		dataPathFn: c.DataPathFn,
		logPathFn:  c.LogPathFn,
		workDir:    c.WorkDir,
	}
}

// Run starts the TUI shell. It acquires the foreground lock, builds the model,
// and runs the bubbletea event loop. Blocks until the user exits.
func (s *Shell) Run() error {
	// Acquire foreground lock
	lockPath := s.dataPathFn("session.lock")
	if err := acquireLock(lockPath); err != nil {
		return err
	}
	defer releaseLock(lockPath)

	// Collect startup notifications
	notifications := s.startupNotifications()

	// Subscribe audit consumer to event bus
	if s.eventBus != nil {
		auditCh := s.eventBus.Subscribe()
		go bus.DrainToAudit(auditCh, s.logger.Log)
	}

	// Build pipeline runner
	runner := NewPipelineRunner(RunnerConfig{
		APIKey:        s.apiKey,
		AutonomyLevel: s.config.AutonomyLevel,
		ExecutorType:  "local",
		EventBus:      s.eventBus,
		Registry:      s.registry,
		Validator:     s.validator,
		Manifest:      s.manifestSt,
		SessionMgr:    s.sessionMgr,
		AuditLogger:   s.logger,
		Config:        s.config,
		DataPathFn:    s.dataPathFn,
		WorkspaceDir:  s.workDir,
	})

	// Build model
	vc := VoiceConfig{
		Enabled:   s.config.VoiceEnabled,
		ModelPath: s.config.WhisperModelPath,
		CLIPath:   s.config.WhisperCLIPath,
		PTTKey:    s.config.VoicePushToTalkKey,
	}
	model := New(s.eventBus, runner, notifications, s.workDir, vc)

	// Run bubbletea
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}

// ── Foreground lock ───────────────────────────────────────────────────────────

func acquireLock(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("shell: create lock dir: %w", err)
	}

	// Check if lock exists
	data, err := os.ReadFile(path)
	if err == nil {
		// Lock file exists — check if the PID is still running
		pidStr := strings.TrimSpace(string(data))
		if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
			if processRunning(pid) {
				return fmt.Errorf(
					"hyperi shell is already running (PID %d)\n"+
						"Run 'hyperi session list' to see active sessions.\n"+
						"If the process is no longer running, delete %s",
					pid, path,
				)
			}
		}
		// Stale lock — remove it
		_ = os.Remove(path)
	}

	// Write our PID
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o640)
}

func releaseLock(path string) {
	// Only remove if it's our lock
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if strings.TrimSpace(string(data)) == strconv.Itoa(os.Getpid()) {
		_ = os.Remove(path)
	}
}

func processRunning(pid int) bool {
	// On Linux: check /proc/<pid>
	_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	return err == nil
}

// ── Startup notifications ─────────────────────────────────────────────────────

func (s *Shell) startupNotifications() []string {
	var notes []string

	plansDir := s.dataPathFn("plans")
	entries, err := os.ReadDir(plansDir)
	if err != nil {
		return notes
	}

	halted, inProgress := 0, 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(plansDir, entry.Name())
		state, err := plan.ParsePlanDoc(path)
		if err != nil {
			continue
		}
		// Only flag recent ones (last 7 days)
		info, _ := entry.Info()
		if info != nil && time.Since(info.ModTime()) > 7*24*time.Hour {
			continue
		}
		switch state.Status {
		case plan.StatusHalted:
			halted++
		case plan.StatusInProgress:
			inProgress++
		}
	}

	if halted > 0 || inProgress > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d plan(s) need attention (%d halted, %d in-progress) — run 'hyperi plans' to review",
			halted+inProgress, halted, inProgress,
		))
	}

	return notes
}
