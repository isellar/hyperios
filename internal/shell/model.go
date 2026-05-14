package shell

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/isellar/hyperios/internal/bus"
	"github.com/isellar/hyperios/internal/types"
	"github.com/isellar/hyperios/internal/voice"
)

// ── Message types ─────────────────────────────────────────────────────────────

// busEventMsg wraps a bus.Event for delivery to the bubbletea Update loop.
type busEventMsg struct{ event bus.Event }

// pipelineStartMsg signals that a pipeline run has started.
type pipelineStartMsg struct{ intent string }

// pipelineDoneMsg signals that a pipeline run has finished.
type pipelineDoneMsg struct{ err error }

// approvalRequestMsg carries a pending approval prompt.
type approvalRequestMsg struct {
	stepID   string
	stepDesc string
	command  []string
	reason   string
	timeout  time.Duration
	replyCh  chan bool
}

// approvalTimeoutMsg fires when an approval prompt times out.
type approvalTimeoutMsg struct{ stepID string }

// tickMsg drives the approval countdown timer.
type tickMsg time.Time

// voiceStartMsg signals push-to-talk recording has begun.
type voiceStartMsg struct{}

// voiceStopMsg signals the key was released and transcription should run.
type voiceStopMsg struct{ session *voice.Session }

// voiceReadyMsg carries a newly created session back to the model after Start().
type voiceReadyMsg struct{ session *voice.Session }

// voiceResultMsg carries the finished transcript (or error) from whisper.
type voiceResultMsg struct {
	transcript string
	err        error
}

// outputLine is a rendered line in the output area.
type outputLine struct {
	text    string
	style   lipgloss.Style
	isBlank bool
}

// ── Model ─────────────────────────────────────────────────────────────────────

// Model is the bubbletea model for the HyperiOS TUI shell.
type Model struct {
	// Layout
	width  int
	height int

	// Input
	input   textinput.Model
	history []string
	histIdx int

	// Output
	viewport viewport.Model
	lines    []outputLine

	// Pipeline state
	running   bool
	sessionID string
	runCount  int

	// Approval prompt state
	approval    *approvalRequestMsg
	approvalEnd time.Time

	// Event bus
	eventBus *bus.Bus
	busCh    <-chan bus.Event

	// Pipeline runner — called when user submits input
	runner PipelineRunner

	// Notifications
	notifications []string

	// Context for the foreground session (workspace dir, etc.)
	workspaceDir string

	// Voice (push-to-talk)
	voiceEnabled   bool
	voiceModelPath string
	voiceCLIPath   string
	voiceRecording bool
	voiceSession   *voice.Session
	voicePTTKey    string // e.g. "ctrl+space"
}

// PipelineRunner is the function the TUI calls when the user submits an intent.
// It runs in a goroutine and sends events to the bus; the TUI receives them
// via the bus subscription.
type PipelineRunner func(intent string, sessionID string) error

// VoiceConfig holds push-to-talk configuration for the TUI.
type VoiceConfig struct {
	Enabled   bool
	ModelPath string
	CLIPath   string
	PTTKey    string // e.g. "ctrl+space"
}

// New creates a new TUI Model.
func New(eventBus *bus.Bus, runner PipelineRunner, notifications []string, workspaceDir string, vc VoiceConfig) Model {
	// Text input
	ti := textinput.New()
	ti.Placeholder = "what do you want to do?"
	ti.Focus()
	ti.CharLimit = 512
	ti.Width = 80

	// Viewport for scrollable output
	vp := viewport.New(80, 20)
	vp.SetContent("")

	pttKey := vc.PTTKey
	if pttKey == "" {
		pttKey = "ctrl+space"
	}

	m := Model{
		input:          ti,
		viewport:       vp,
		eventBus:       eventBus,
		runner:         runner,
		notifications:  notifications,
		workspaceDir:   workspaceDir,
		histIdx:        -1,
		voiceEnabled:   vc.Enabled && voice.IsAvailable(vc.ModelPath, vc.CLIPath),
		voiceModelPath: vc.ModelPath,
		voiceCLIPath:   vc.CLIPath,
		voicePTTKey:    pttKey,
	}

	// Subscribe to event bus
	if eventBus != nil {
		m.busCh = eventBus.Subscribe()
	}

	return m
}

// initialIntentMsg is sent on Init when HYPERI_INITIAL_INTENT is set.
type initialIntentMsg struct{ intent string }

// ── Init ──────────────────────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textinput.Blink,
	}
	if m.busCh != nil {
		cmds = append(cmds, waitForBusEvent(m.busCh))
	}
	// If an initial intent was passed via environment (from `hyperi session start "..."`),
	// inject it as the first command after the TUI is ready.
	if intent := os.Getenv("HYPERI_INITIAL_INTENT"); intent != "" {
		os.Unsetenv("HYPERI_INITIAL_INTENT")
		cmds = append(cmds, func() tea.Msg {
			return initialIntentMsg{intent: intent}
		})
	}
	return tea.Batch(cmds...)
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = m.outputHeight()
		m.input.Width = msg.Width - len(promptStr()) - 1
		m.refreshViewport()

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "ctrl+space":
			// Push-to-talk toggle: first press starts recording, second stops + transcribes
			if m.voiceEnabled && !m.running && m.approval == nil {
				if !m.voiceRecording {
					// Start — voiceRecording set true when voiceReadyMsg arrives
					return m, m.startVoiceRecording()
				} else {
					// Stop — clear recording state immediately for responsive UI
					m.voiceRecording = false
					return m, m.stopVoiceRecording()
				}
			}

		case "enter":
			if m.approval != nil {
				// In approval mode — 'enter' with empty input means deny
				m.handleApprovalResponse(false)
				return m, nil
			}
			return m, m.handleInput()

		case "y", "Y":
			if m.approval != nil {
				m.handleApprovalResponse(true)
				return m, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)

		case "n", "N":
			if m.approval != nil {
				m.handleApprovalResponse(false)
				return m, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)

		case "up":
			if m.approval == nil && len(m.history) > 0 {
				if m.histIdx < len(m.history)-1 {
					m.histIdx++
				}
				m.input.SetValue(m.history[len(m.history)-1-m.histIdx])
				m.input.CursorEnd()
			}

		case "down":
			if m.approval == nil {
				if m.histIdx > 0 {
					m.histIdx--
					m.input.SetValue(m.history[len(m.history)-1-m.histIdx])
					m.input.CursorEnd()
				} else if m.histIdx == 0 {
					m.histIdx = -1
					m.input.SetValue("")
				}
			}

		default:
			if m.approval == nil {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				cmds = append(cmds, cmd)
			}
		}

	case busEventMsg:
		m.handleBusEvent(msg.event)
		// Keep listening
		if m.busCh != nil {
			cmds = append(cmds, waitForBusEvent(m.busCh))
		}

	case approvalRequestMsg:
		m.approval = &msg
		m.approvalEnd = time.Now().Add(msg.timeout)
		m.appendLine(outputLine{
			text:  fmt.Sprintf("  ~ Step [%s] requires approval", msg.stepID),
			style: styleApprovalPrompt,
		})
		m.appendLine(outputLine{
			text:  fmt.Sprintf("    %s", msg.reason),
			style: styleOutput,
		})
		m.appendLine(outputLine{
			text:  fmt.Sprintf("    Command: %s", strings.Join(msg.command, " ")),
			style: styleCommandOutput,
		})
		m.appendLine(outputLine{
			text:  fmt.Sprintf("    Approve? [y/n] (timeout %v)", msg.timeout.Round(time.Second)),
			style: styleApprovalHint,
		})
		m.refreshViewport()
		// Start countdown tick
		cmds = append(cmds, tickEvery(time.Second))

	case approvalTimeoutMsg:
		if m.approval != nil && m.approval.stepID == msg.stepID {
			m.appendLine(outputLine{
				text:  "    Approval timed out — halting",
				style: styleError,
			})
			m.handleApprovalResponse(false)
			m.refreshViewport()
		}

	case tickMsg:
		if m.approval != nil {
			remaining := time.Until(m.approvalEnd).Round(time.Second)
			if remaining <= 0 {
				return m, func() tea.Msg {
					return approvalTimeoutMsg{stepID: m.approval.stepID}
				}
			}
			cmds = append(cmds, tickEvery(time.Second))
		}

	case voiceReadyMsg:
		// Session started successfully — store the pointer and show indicator
		m.voiceSession = msg.session
		m.voiceRecording = true
		m.appendLine(outputLine{text: "  🎤 Recording... (press Ctrl+Space again to stop)", style: styleSystem})
		m.refreshViewport()

	case voiceStartMsg:
		// Legacy — kept for completeness; voiceReadyMsg is now the primary path
		m.voiceRecording = true

	case voiceStopMsg:
		m.voiceRecording = false
		m.appendLine(outputLine{text: "  ⏳ Transcribing...", style: styleSystem})
		m.refreshViewport()
		// Run transcription in background
		sess := msg.session
		return m, func() tea.Msg {
			transcript, err := sess.Transcribe(context.Background())
			return voiceResultMsg{transcript: transcript, err: err}
		}

	case voiceResultMsg:
		if msg.err != nil {
			m.appendLine(outputLine{
				text:  fmt.Sprintf("  Voice error: %v", msg.err),
				style: styleError,
			})
		} else if msg.transcript != "" {
			m.input.SetValue(msg.transcript)
			m.input.CursorEnd()
			m.appendLine(outputLine{
				text:  fmt.Sprintf("  ✓ Heard: %q", msg.transcript),
				style: styleSystem,
			})
		} else {
			m.appendLine(outputLine{text: "  (no speech detected)", style: styleGray})
		}
		m.refreshViewport()

	case initialIntentMsg:
		// Pre-queued intent from `hyperi session start "..."` — run it immediately.
		if !m.running && msg.intent != "" {
			m.input.SetValue(msg.intent)
			return m, m.handleInput()
		}

	case pipelineStartMsg:
		m.running = true
		m.runCount++
		m.appendBlank()
		m.appendLine(outputLine{
			text:  fmt.Sprintf("Running: %s", msg.intent),
			style: styleSystem,
		})
		m.refreshViewport()

	case pipelineDoneMsg:
		m.running = false
		if msg.err != nil {
			m.appendLine(outputLine{
				text:  fmt.Sprintf("Error: %v", msg.err),
				style: styleError,
			})
		}
		m.appendBlank()
		m.refreshViewport()
		m.input.Focus()
	}

	// Scroll viewport with mouse/keyboard
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	// Header
	b.WriteString(styleHeader.Render("HyperiOS"))
	b.WriteString(styleDivider.Render(" — " + m.workspaceDir))
	b.WriteString("\n")
	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)))
	b.WriteString("\n")

	// Notification banners
	for _, n := range m.notifications {
		b.WriteString(styleBanner.Render(n))
		b.WriteString("\n")
	}

	// Output viewport
	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	// Divider above input
	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)))
	b.WriteString("\n")

	// Input line or approval prompt
	if m.approval != nil {
		remaining := time.Until(m.approvalEnd).Round(time.Second)
		prompt := styleApprovalPrompt.Render(fmt.Sprintf("approve [y/n] (%v) > ", remaining))
		b.WriteString(prompt)
	} else if m.voiceRecording {
		b.WriteString(styleApprovalPrompt.Render("🎤 Recording... press Ctrl+Space again to stop"))
	} else if m.running {
		b.WriteString(styleSystem.Render(promptStr() + " (running...)"))
	} else {
		b.WriteString(stylePrompt.Render(promptStr()))
		b.WriteString(m.input.View())
		if m.voiceEnabled {
			b.WriteString(styleGray.Render("  [Ctrl+Space: voice]"))
		}
	}

	return b.String()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func promptStr() string {
	return "hyperi> "
}

func (m *Model) outputHeight() int {
	// Total height minus: header (2) + divider (1) + notifications + divider (1) + input (1)
	reserved := 5 + len(m.notifications)
	h := m.height - reserved
	if h < 5 {
		h = 5
	}
	return h
}

// startVoiceRecording creates a session, starts arecord, and returns a
// voiceReadyMsg so the model can store the session pointer.
func (m *Model) startVoiceRecording() tea.Cmd {
	modelPath := m.voiceModelPath
	cliPath := m.voiceCLIPath
	return func() tea.Msg {
		sess, err := voice.NewSession(modelPath, cliPath)
		if err != nil {
			return voiceResultMsg{err: err}
		}
		if err := sess.Start(); err != nil {
			return voiceResultMsg{err: err}
		}
		return voiceReadyMsg{session: sess}
	}
}

// stopVoiceRecording stops the active recording session and triggers transcription.
func (m *Model) stopVoiceRecording() tea.Cmd {
	sess := m.voiceSession
	m.voiceSession = nil
	if sess == nil {
		return nil
	}
	_ = sess.Stop()
	return func() tea.Msg {
		return voiceStopMsg{session: sess}
	}
}

func (m *Model) handleInput() tea.Cmd {
	val := strings.TrimSpace(m.input.Value())
	if val == "" {
		return nil
	}

	m.input.SetValue("")
	m.histIdx = -1

	// Built-in commands
	switch strings.ToLower(val) {
	case "exit", "quit":
		return tea.Quit
	case "clear":
		m.lines = nil
		m.refreshViewport()
		return nil
	case "help":
		m.appendHelp()
		m.refreshViewport()
		return nil
	}

	// Add to history
	if len(m.history) == 0 || m.history[len(m.history)-1] != val {
		m.history = append(m.history, val)
		if len(m.history) > 100 {
			m.history = m.history[1:]
		}
	}

	// Echo input
	m.appendLine(outputLine{
		text:  fmt.Sprintf("%s%s", promptStr(), val),
		style: stylePrompt,
	})
	m.refreshViewport()

	// Run pipeline in background
	intent := val
	runner := m.runner
	sessionID := m.sessionID

	return tea.Batch(
		func() tea.Msg { return pipelineStartMsg{intent: intent} },
		func() tea.Msg {
			err := runner(intent, sessionID)
			return pipelineDoneMsg{err: err}
		},
	)
}

func (m *Model) handleBusEvent(e bus.Event) {
	switch e.Kind {
	case bus.EventStepStarted:
		m.appendLine(outputLine{
			text:  fmt.Sprintf("  → %s", e.StepID),
			style: styleStepStarted,
		})

	case bus.EventStepCompleted:
		m.appendLine(outputLine{
			text:  fmt.Sprintf("  ✓ %s", e.StepID),
			style: styleStepOk,
		})
		// Show command output inline
		if result, ok := e.Payload.(*types.ExecutionResult); ok && result != nil {
			out := strings.TrimSpace(result.Output)
			if out != "" {
				for _, line := range strings.Split(out, "\n") {
					m.appendLine(outputLine{
						text:  "    " + line,
						style: styleCommandOutput,
					})
				}
			}
		}

	case bus.EventStepFailed:
		m.appendLine(outputLine{
			text:  fmt.Sprintf("  ✗ %s failed", e.StepID),
			style: styleStepFail,
		})
		if msg, ok := e.Payload.(string); ok && msg != "" {
			m.appendLine(outputLine{
				text:  "    " + msg,
				style: styleError,
			})
		}

	case bus.EventStepSkipped:
		m.appendLine(outputLine{
			text:  fmt.Sprintf("  - %s (skipped)", e.StepID),
			style: styleStepSkip,
		})

	case bus.EventPlanCompleted:
		m.appendLine(outputLine{
			text:  "✓ Plan completed",
			style: styleStepOk,
		})

	case bus.EventPlanFailed:
		m.appendLine(outputLine{
			text:  "✗ Plan failed",
			style: styleStepFail,
		})
		if msg, ok := e.Payload.(string); ok && msg != "" {
			m.appendLine(outputLine{text: "  " + msg, style: styleError})
		}

	case bus.EventApprovalNeeded:
		if ap, ok := e.Payload.(*bus.ApprovalPayload); ok {
			timeout := time.Duration(ap.TimeoutSeconds) * time.Second
			if timeout == 0 {
				timeout = 5 * time.Minute
			}
			m.approval = &approvalRequestMsg{
				stepID:   ap.StepID,
				stepDesc: ap.StepDesc,
				command:  ap.Command,
				reason:   ap.Reason,
				timeout:  timeout,
				replyCh:  ap.ReplyCh,
			}
			m.approvalEnd = time.Now().Add(timeout)
		}

	case bus.EventKind("plan:verdicts"):
		if pv, ok := e.Payload.(*planVerdicts); ok && pv != nil {
			m.appendBlank()
			m.appendLine(outputLine{text: "── Plan ─────────────────────────────────", style: stylePlanHeading})
			verdictMap := map[string]types.ArbiterVerdict{}
			for _, v := range pv.Verdicts {
				verdictMap[v.StepID] = v
			}
			for i, step := range pv.Plan.Steps {
				v := verdictMap[step.ID]
				icon := verdictIcon(v.Verdict)
				style := verdictStyle(v.Verdict)
				m.appendLine(outputLine{
					text:  fmt.Sprintf("  %s %d. [%s] %s", icon, i+1, step.ID, step.Description),
					style: style,
				})
				m.appendLine(outputLine{
					text:  fmt.Sprintf("       %s %s", step.Capability.Type, step.Capability.Scope),
					style: styleGray,
				})
			}
			if pv.Report.Summary != "" {
				m.appendLine(outputLine{text: "── Risk ─────────────────────────────────", style: stylePlanHeading})
				m.appendLine(outputLine{text: "  " + pv.Report.Summary, style: styleOutput})
			}
			m.appendBlank()
		}

	case bus.EventScheduledFired:
		if name, ok := e.Payload.(string); ok {
			m.appendLine(outputLine{
				text:  fmt.Sprintf("  ⏰ Scheduled task fired: %s", name),
				style: styleSystem,
			})
		}

	case bus.EventManifestUpdated:
		// Silent — don't clutter output with manifest updates

	case bus.EventAlertTriggered:
		if msg, ok := e.Payload.(string); ok {
			m.appendLine(outputLine{
				text:  fmt.Sprintf("  ⚠  Alert: %s", msg),
				style: styleModified,
			})
		}
	}

	m.refreshViewport()
}

func (m *Model) handleApprovalResponse(approved bool) {
	if m.approval == nil {
		return
	}

	ap := m.approval
	m.approval = nil

	if approved {
		m.appendLine(outputLine{text: "    → Approved", style: styleStepOk})
	} else {
		m.appendLine(outputLine{text: "    → Denied", style: styleStepFail})
	}
	m.refreshViewport()
	m.input.Focus()

	// Send reply (non-blocking — channel may already be closed)
	go func() {
		defer func() { recover() }()
		ap.replyCh <- approved
		close(ap.replyCh)
	}()
}

func (m *Model) appendLine(line outputLine) {
	m.lines = append(m.lines, line)
	// Keep last 2000 lines to bound memory
	if len(m.lines) > 2000 {
		m.lines = m.lines[len(m.lines)-2000:]
	}
}

func (m *Model) appendBlank() {
	m.lines = append(m.lines, outputLine{isBlank: true})
}

func (m *Model) appendHelp() {
	m.appendBlank()
	m.appendLine(outputLine{text: "HyperiOS Shell — built-in commands:", style: stylePlanHeading})
	m.appendLine(outputLine{text: "  clear           clear the output area", style: styleOutput})
	m.appendLine(outputLine{text: "  help            show this help", style: styleOutput})
	m.appendLine(outputLine{text: "  exit / quit     exit the shell", style: styleOutput})
	m.appendLine(outputLine{text: "  ↑ / ↓           navigate command history", style: styleOutput})
	m.appendLine(outputLine{text: "  Ctrl+C          exit", style: styleOutput})
	m.appendBlank()
	m.appendLine(outputLine{text: "Anything else is sent to the agent pipeline as an intent.", style: styleSystem})
	m.appendBlank()
}

func (m *Model) refreshViewport() {
	var sb strings.Builder
	for _, line := range m.lines {
		if line.isBlank {
			sb.WriteString("\n")
		} else {
			sb.WriteString(line.style.Render(line.text))
			sb.WriteString("\n")
		}
	}
	content := sb.String()
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

// ── Commands ──────────────────────────────────────────────────────────────────

// waitForBusEvent returns a tea.Cmd that blocks until the next bus event.
func waitForBusEvent(ch <-chan bus.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return nil
		}
		return busEventMsg{event: e}
	}
}

// tickEvery returns a tea.Cmd that fires a tickMsg after d.
func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}
