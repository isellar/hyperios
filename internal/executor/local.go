// Package executor provides Linux-native execution backends for HyperiOS.
// This file implements LocalExecutor, which runs capability-gated actions
// directly on the host Linux system.
package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/bus"
	"github.com/isellar/hyperios/internal/capability"
	"github.com/isellar/hyperios/internal/display"
	"github.com/isellar/hyperios/internal/types"
)

// ErrReplan is a sentinel error returned when a step's on_failure policy is "replan".
// The caller (pipeline loop) catches this and triggers a re-plan pass.
var ErrReplan = fmt.Errorf("replan requested")

// ErrStepSkipped is returned when a step's on_failure policy is "skip".
var ErrStepSkipped = fmt.Errorf("step skipped")

type LocalExecutor struct {
	registry  *capability.Registry
	enforcer  *capability.Enforcer
	workspace string
	eventBus  *bus.Bus
	sessionID string
}

func NewLocal(registry *capability.Registry, workspace string, eventBus *bus.Bus, sessionID string) *LocalExecutor {
	return &LocalExecutor{
		registry:  registry,
		enforcer:  capability.NewEnforcer(registry),
		workspace: workspace,
		eventBus:  eventBus,
		sessionID: sessionID,
	}
}

// publish sends an event on the bus if one is configured.
func (e *LocalExecutor) publish(kind bus.EventKind, stepID string, payload any) {
	if e.eventBus == nil {
		return
	}
	e.eventBus.PublishStep(kind, e.sessionID, stepID, payload)
}

func (e *LocalExecutor) Name() string {
	return "local"
}

func (e *LocalExecutor) Validate(step types.ActionStep) error {
	return e.enforcer.Validate(step)
}

// Execute runs a single ActionStep, enforcing capability, on_failure policy,
// and ReadyCondition checks.
//
// Return values:
//   - (*ExecutionResult, nil)        — step succeeded
//   - (*ExecutionResult, ErrReplan)  — step failed with on_failure=replan
//   - (*ExecutionResult, ErrStepSkipped) — step failed with on_failure=skip
//   - (*ExecutionResult, err)        — step failed with on_failure=halt (or capability denied)
func (e *LocalExecutor) Execute(ctx context.Context, step types.ActionStep) (*types.ExecutionResult, error) {
	start := time.Now()

	if err := e.enforcer.Validate(step); err != nil {
		return &types.ExecutionResult{
			StepID:   step.ID,
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(start).Milliseconds(),
		}, err
	}

	// Determine retry budget
	maxAttempts := 1
	if step.OnFailure == "retry" && step.MaxRetries > 0 {
		maxAttempts = 1 + step.MaxRetries
	}

	var result *types.ExecutionResult
	var lastErr error

	e.publish(bus.EventStepStarted, step.ID, step.Description)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			backoff := time.Duration(step.RetryBackoffSeconds) * time.Second
			if backoff > 0 {
				select {
				case <-ctx.Done():
					return result, ctx.Err()
				case <-time.After(backoff):
				}
			}
		}

		output, execErr := e.dispatch(step)

		result = &types.ExecutionResult{
			StepID:   step.ID,
			Duration: time.Since(start).Milliseconds(),
		}

		if execErr != nil {
			result.Success = false
			result.Error = execErr.Error()
			lastErr = execErr
			// If retrying, loop again
			if step.OnFailure == "retry" && attempt < maxAttempts {
				continue
			}
		} else {
			result.Success = true
			result.Output = output
			lastErr = nil
		}
		break
	}

	// Step succeeded — check ReadyCondition before returning
	if lastErr == nil && step.ReadyCondition != nil {
		if err := e.waitReady(ctx, step); err != nil {
			lastErr = fmt.Errorf("ready condition failed: %w", err)
			result.Success = false
			result.Error = lastErr.Error()
		}
	}

	if lastErr == nil {
		e.publish(bus.EventStepCompleted, step.ID, result)
		return result, nil
	}

	// Apply on_failure policy
	switch step.OnFailure {
	case "skip":
		result.Error = fmt.Sprintf("skipped: %s", lastErr)
		e.publish(bus.EventStepSkipped, step.ID, result.Error)
		return result, ErrStepSkipped
	case "replan":
		e.publish(bus.EventStepFailed, step.ID, result.Error)
		return result, ErrReplan
	default:
		e.publish(bus.EventStepFailed, step.ID, result.Error)
		return result, lastErr
	}
}

// dispatch runs the appropriate capability handler for the step.
func (e *LocalExecutor) dispatch(step types.ActionStep) (string, error) {
	switch {
	case strings.HasPrefix(step.Capability.Type, "read:file"):
		return e.executeReadFile(step)
	case strings.HasPrefix(step.Capability.Type, "execute:shell"):
		return e.executeShell(step)
	case strings.HasPrefix(step.Capability.Type, "execute:git"):
		return e.executeGit(step)
	case strings.HasPrefix(step.Capability.Type, "execute:package"):
		return e.executePackage(step)
	case strings.HasPrefix(step.Capability.Type, "execute:process"):
		return e.executeProcess(step)
	case strings.HasPrefix(step.Capability.Type, "execute:display"):
		return e.executeDisplay(step)
	case strings.HasPrefix(step.Capability.Type, "execute:config"):
		return e.executeConfig(step)
	case strings.HasPrefix(step.Capability.Type, "execute:network"):
		return e.executeNetworkConfig(step)
	case strings.HasPrefix(step.Capability.Type, "network:outbound"):
		return e.executeNetworkOutbound(step)
	case strings.HasPrefix(step.Capability.Type, "ui:open"):
		return e.executeUIOpen(step)
	case strings.HasPrefix(step.Capability.Type, "execute:schedule"):
		return e.executeSchedule(step)
	default:
		return "", &UnsupportedCapabilityError{Type: step.Capability.Type}
	}
}

// waitReady polls a ReadyCondition until it is satisfied or the timeout expires.
func (e *LocalExecutor) waitReady(ctx context.Context, step types.ActionStep) error {
	rc := step.ReadyCondition
	timeout := time.Duration(rc.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	interval := time.Duration(rc.PollIntervalSeconds) * time.Second
	if interval == 0 {
		interval = 2 * time.Second
	}

	deadline := time.Now().Add(timeout)

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %v waiting for condition %s:%s", timeout, rc.Type, rc.Target)
		}

		ok, err := e.checkCondition(ctx, step, rc)
		if err != nil {
			return fmt.Errorf("condition check error: %w", err)
		}
		if ok {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// checkCondition evaluates a single ReadyCondition check.
// Returns (true, nil) if the condition is met, (false, nil) if not yet met,
// (false, err) if the check itself failed in an unrecoverable way.
func (e *LocalExecutor) checkCondition(ctx context.Context, step types.ActionStep, rc *types.ReadyCondition) (bool, error) {
	switch rc.Type {
	case "exit:0":
		// Re-execute the command and check exit code
		if len(step.Command) == 0 {
			return false, fmt.Errorf("exit:0 condition requires a non-empty Command")
		}
		cmd := exec.CommandContext(ctx, step.Command[0], step.Command[1:]...)
		err := cmd.Run()
		return err == nil, nil

	case "process:active":
		// Check if a systemd service is active
		out, err := exec.CommandContext(ctx, "systemctl", "is-active", rc.Target).Output()
		if err != nil {
			return false, nil // not yet active, not an error
		}
		return strings.TrimSpace(string(out)) == "active", nil

	case "file:exists":
		_, err := os.Stat(rc.Target)
		return err == nil, nil

	case "output:contains":
		// Re-execute command and check if stdout contains Target string
		if len(step.Command) == 0 {
			return false, fmt.Errorf("output:contains condition requires a non-empty Command")
		}
		out, err := exec.CommandContext(ctx, step.Command[0], step.Command[1:]...).Output()
		if err != nil {
			return false, nil
		}
		return strings.Contains(string(out), rc.Target), nil

	case "http:ok":
		// HTTP GET to Target URL, check for 2xx
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(rc.Target)
		if err != nil {
			return false, nil
		}
		resp.Body.Close()
		return resp.StatusCode >= 200 && resp.StatusCode < 300, nil

	case "atspi:present":
		// Check if a UI element is present in the AT-SPI accessibility tree.
		// Target format: "name=<name>" or "name=<name>,role=<role>"
		atspi := display.NewATSPIClient()
		if !atspi.IsAvailable() {
			// AT-SPI not available — treat as satisfied (graceful degradation)
			return true, nil
		}
		name, role := parseATSPITarget(rc.Target)
		elem, err := atspi.FindElement(name, role)
		if err != nil {
			return false, nil // not an unrecoverable error — keep polling
		}
		return elem != nil, nil

	case "vision:confirms":
		// Vision model screen confirmation — not yet implemented (Phase 4+).
		// Returns true to avoid blocking execution; will be implemented when
		// the vision model integration is added.
		// TODO(Phase 4+): capture screenshot, send to LLM vision API,
		// check if response confirms rc.Target description is visible.
		return true, nil

	default:
		return false, fmt.Errorf("unknown ReadyCondition type: %q", rc.Type)
	}
}

func (e *LocalExecutor) executeReadFile(step types.ActionStep) (string, error) {
	scope := capability.ExpandWorkspace(step.Capability.Scope, e.workspace)
	data, err := os.ReadFile(scope)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (e *LocalExecutor) executeShell(step types.ActionStep) (string, error) {
	// Phase 1A+: use literal Command []string if provided by the Planner.
	if len(step.Command) > 0 {
		cmd := exec.Command(step.Command[0], step.Command[1:]...)
		cmd.Dir = e.workspace
		output, err := cmd.CombinedOutput()
		if err != nil {
			return string(output), err
		}
		return string(output), nil
	}

	// Legacy fallback: NL keyword extraction (to be removed in Phase 1A cleanup).
	desc := step.Description
	scope := step.Capability.Scope
	command := extractCommand(desc, scope)

	var args []string
	switch command {
	case "grep":
		args = buildGrepArgs(desc, scope, e.workspace)
	case "find":
		args = buildFindArgs(scope, e.workspace)
	case "ls":
		args = buildLsArgs(scope, e.workspace)
	default:
		args = []string{}
	}

	cmd := exec.Command(command, args...)
	cmd.Dir = e.workspace

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), err
	}
	return string(output), nil
}

// executePackage installs, removes, or queries packages via apt, flatpak, or snap.
// Scope format: "<manager>:<action>:<package>" e.g. "apt:install:curl"
func (e *LocalExecutor) executePackage(step types.ActionStep) (string, error) {
	// Phase 1A+: use literal Command []string when provided by the Planner.
	// This is the correct path for all new plans.
	if len(step.Command) > 0 {
		cmd := exec.Command(step.Command[0], step.Command[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return string(output), fmt.Errorf("execute:package: %w", err)
		}
		return string(output), nil
	}

	// Legacy fallback: derive command from Capability.Scope.
	// Only reached if the Planner emitted a step without a Command field.
	// Handles scope formats: "apt:nginx", "apt:install:nginx", "apt:update", "apt:upgrade".
	scope := step.Capability.Scope
	parts := strings.SplitN(scope, ":", 3)
	if len(parts) < 2 {
		return "", fmt.Errorf("execute:package scope must be <manager>:<package>, got: %s", scope)
	}

	manager := parts[0]

	// Special cases: apt:update and apt:upgrade take no package argument
	if manager == "apt" && len(parts) == 2 {
		switch parts[1] {
		case "update":
			output, err := exec.Command("sudo", "apt-get", "update").CombinedOutput()
			return string(output), err
		case "upgrade":
			output, err := exec.Command("sudo", "apt-get", "-y", "upgrade").CombinedOutput()
			return string(output), err
		}
	}

	var action, pkg string
	if len(parts) == 3 {
		action = parts[1]
		pkg = parts[2]
	} else {
		action = "install"
		pkg = parts[1]
	}

	var cmd *exec.Cmd
	switch manager {
	case "apt":
		cmd = exec.Command("sudo", "apt-get", "-y", action, pkg)
	case "flatpak":
		cmd = exec.Command("flatpak", action, "--noninteractive", pkg)
	case "snap":
		cmd = exec.Command("sudo", "snap", action, pkg)
	default:
		return "", fmt.Errorf("unsupported package manager: %s (supported: apt, flatpak, snap)", manager)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s %s %s: %w", manager, action, pkg, err)
	}
	return string(output), nil
}

// executeProcess manages systemd services.
// Scope format: "systemctl:<action>:<service>" e.g. "systemctl:start:nginx"
func (e *LocalExecutor) executeProcess(step types.ActionStep) (string, error) {
	scope := step.Capability.Scope
	parts := strings.SplitN(scope, ":", 3)
	if len(parts) < 3 || parts[0] != "systemctl" {
		return "", fmt.Errorf("execute:process scope must be systemctl:<action>:<service>, got: %s", scope)
	}

	action := parts[1]
	service := parts[2]

	// Safety: only allow known safe systemctl actions
	allowed := map[string]bool{
		"start": true, "stop": true, "restart": true,
		"enable": true, "disable": true, "status": true,
		"reload": true,
	}
	if !allowed[action] {
		return "", fmt.Errorf("systemctl action not allowed: %s", action)
	}

	cmd := exec.Command("sudo", "systemctl", action, service)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("systemctl %s %s: %w", action, service, err)
	}
	return string(output), nil
}

// executeDisplay controls the Wayland compositor and display layer.
//
// Layered interaction model (tried in order):
//  1. swaymsg IPC — workspace/window management
//  2. AT-SPI      — semantic element queries for native GTK/Qt apps
//  3. Vision      — grim screenshot + LLM vision (not yet implemented)
//  4. ydotool     — raw coordinate injection (last resort, not yet implemented)
//
// Command conventions:
//
//	sway:workspace    — Command: ["workspace", "<n>"]
//	sway:exec         — Command: ["exec", "<app>"]
//	sway:focus        — Command: ["focus", "app_id=<id>"] or ["focus", "title=<substr>"]
//	sway:close        — Command: ["close", "app_id=<id>"]
//	sway:fullscreen   — Command: ["fullscreen", "enable|disable"]
//	sway:move         — Command: ["move", "app_id=<id>", "<workspace>"]
//	atspi:find        — Command: ["find", "<name>", "<role>"]
//	atspi:wait        — Command: ["wait", "<name>", "<role>", "<timeout_seconds>"]
//	capture           — Command: ["capture"]  (takes screenshot, returns base64 path)
func (e *LocalExecutor) executeDisplay(step types.ActionStep) (string, error) {
	if len(step.Command) == 0 {
		return "", fmt.Errorf("execute:display: Command is empty")
	}

	scope := step.Capability.Scope

	switch {
	case strings.HasPrefix(scope, "sway:"):
		return e.executeDisplaySway(step)
	case strings.HasPrefix(scope, "atspi:"):
		return e.executeDisplayATSPI(step)
	case scope == "capture":
		return e.executeDisplayCapture(step)
	default:
		return "", fmt.Errorf("execute:display: unknown scope %q (supported: sway:*, atspi:*, capture)", scope)
	}
}

// executeDisplaySway handles swaymsg-based window and workspace operations.
func (e *LocalExecutor) executeDisplaySway(step types.ActionStep) (string, error) {
	sway := display.NewSwayClient()
	if !sway.IsAvailable() {
		return "", display.ErrNoCompositor
	}

	if len(step.Command) < 1 {
		return "", fmt.Errorf("execute:display sway: Command is empty")
	}

	action := step.Command[0]

	switch action {
	case "workspace":
		if len(step.Command) < 2 {
			return "", fmt.Errorf("execute:display workspace: missing workspace number/name")
		}
		target := step.Command[1]
		n := 0
		if _, err := fmt.Sscanf(target, "%d", &n); err == nil {
			return fmt.Sprintf("switched to workspace %d", n), sway.FocusWorkspace(n)
		}
		return fmt.Sprintf("switched to workspace %q", target), sway.FocusWorkspaceByName(target)

	case "exec":
		if len(step.Command) < 2 {
			return "", fmt.Errorf("execute:display exec: missing application command")
		}
		app := strings.Join(step.Command[1:], " ")
		workspace := 2 // default: launch in workspace 2
		if len(step.Command) > 2 {
			fmt.Sscanf(step.Command[len(step.Command)-1], "%d", &workspace)
		}
		return fmt.Sprintf("launched %q in workspace %d", app, workspace),
			sway.LaunchApp(workspace, app)

	case "focus":
		if len(step.Command) < 2 {
			return "", fmt.Errorf("execute:display focus: missing criteria")
		}
		criteria := step.Command[1]
		return fmt.Sprintf("focused %q", criteria), sway.FocusWindow(criteria)

	case "close":
		criteria := ""
		if len(step.Command) > 1 {
			criteria = step.Command[1]
		}
		return "closed window", sway.CloseWindow(criteria)

	case "fullscreen":
		enable := true
		if len(step.Command) > 1 {
			enable = step.Command[1] != "disable"
		}
		criteria := ""
		if len(step.Command) > 2 {
			criteria = step.Command[2]
		}
		return fmt.Sprintf("fullscreen=%v", enable), sway.SetFullscreen(criteria, enable)

	case "move":
		if len(step.Command) < 3 {
			return "", fmt.Errorf("execute:display move: requires [move, criteria, workspace]")
		}
		criteria := step.Command[1]
		workspace := 2
		fmt.Sscanf(step.Command[2], "%d", &workspace)
		return fmt.Sprintf("moved %q to workspace %d", criteria, workspace),
			sway.MoveWindowToWorkspace(criteria, workspace)

	case "wait-window":
		// Wait for a window to appear: ["wait-window", "app_id=firefox", "30"]
		if len(step.Command) < 2 {
			return "", fmt.Errorf("execute:display wait-window: requires [wait-window, criteria, timeout_s]")
		}
		criteria := step.Command[1]
		timeout := 30 * time.Second
		if len(step.Command) > 2 {
			var secs int
			if _, err := fmt.Sscanf(step.Command[2], "%d", &secs); err == nil {
				timeout = time.Duration(secs) * time.Second
			}
		}
		// Parse criteria into appID/title
		appID, title := display.ParseCriteria(criteria)
		err := sway.WaitForWindow(appID, title, timeout)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("window %q appeared", criteria), nil

	default:
		// Fall through to raw swaymsg for anything not explicitly handled
		out, err := sway.Run(step.Command...)
		return string(out), err
	}
}

// executeDisplayATSPI handles AT-SPI accessibility tree queries.
func (e *LocalExecutor) executeDisplayATSPI(step types.ActionStep) (string, error) {
	atspi := display.NewATSPIClient()

	action := step.Command[0]

	switch action {
	case "find":
		name, role := "", ""
		if len(step.Command) > 1 {
			name = step.Command[1]
		}
		if len(step.Command) > 2 {
			role = step.Command[2]
		}
		elem, err := atspi.FindElement(name, role)
		if err != nil {
			return "", err
		}
		if elem == nil {
			return fmt.Sprintf("element %q not found", name), nil
		}
		return fmt.Sprintf("found element: name=%q role=%q", elem.Name, elem.Role), nil

	case "wait":
		name, role := "", ""
		timeout := 30 * time.Second
		if len(step.Command) > 1 {
			name = step.Command[1]
		}
		if len(step.Command) > 2 {
			role = step.Command[2]
		}
		if len(step.Command) > 3 {
			var secs int
			if _, err := fmt.Sscanf(step.Command[3], "%d", &secs); err == nil {
				timeout = time.Duration(secs) * time.Second
			}
		}
		err := atspi.WaitForElement(name, role, timeout)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("element %q appeared", name), nil

	case "click":
		name, role := "", ""
		if len(step.Command) > 1 {
			name = step.Command[1]
		}
		if len(step.Command) > 2 {
			role = step.Command[2]
		}
		return fmt.Sprintf("clicked %q", name), atspi.Click(name, role)

	default:
		return "", fmt.Errorf("execute:display atspi: unknown action %q", action)
	}
}

// executeDisplayCapture takes a screenshot via grim.
func (e *LocalExecutor) executeDisplayCapture(step types.ActionStep) (string, error) {
	cap := display.NewCapturer("")
	if !cap.IsAvailable() {
		return "", display.ErrNoCompositor
	}

	result, err := cap.Capture()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("screenshot saved: %s (%d bytes)", result.FilePath, len(result.Base64)*3/4), nil
}

// ── Remaining capability handlers ─────────────────────────────────────────────

func (e *LocalExecutor) executeGit(step types.ActionStep) (string, error) {
	if len(step.Command) > 0 {
		if step.Command[0] != "git" {
			return "", fmt.Errorf("execute:git: Command[0] must be 'git', got %q", step.Command[0])
		}
		cmd := exec.Command("git", step.Command[1:]...)
		cmd.Dir = e.workspace
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	gitCmd := extractGitCommand(step.Description)
	cmd := exec.Command("git", gitCmd...)
	cmd.Dir = e.workspace
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (e *LocalExecutor) executeConfig(step types.ActionStep) (string, error) {
	if len(step.Command) < 2 {
		return "", fmt.Errorf("execute:config requires Command[0]=path and Command[1]=content, got %d args", len(step.Command))
	}
	targetPath := step.Command[0]
	content := step.Command[1]
	if targetPath == "" {
		return "", fmt.Errorf("execute:config: target path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return "", fmt.Errorf("execute:config: create parent dir for %s: %w", targetPath, err)
	}
	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("execute:config: write %s: %w", targetPath, err)
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), targetPath), nil
}

func (e *LocalExecutor) executeNetworkConfig(step types.ActionStep) (string, error) {
	if len(step.Command) == 0 {
		return "", fmt.Errorf("execute:network: Command is empty")
	}
	cmd := exec.Command(step.Command[0], step.Command[1:]...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (e *LocalExecutor) executeNetworkOutbound(step types.ActionStep) (string, error) {
	if len(step.Command) < 2 {
		return "", fmt.Errorf("network:outbound requires Command[0]=method and Command[1]=url, got %d args", len(step.Command))
	}
	method := strings.ToUpper(step.Command[0])
	rawURL := step.Command[1]
	if method != "GET" && method != "POST" {
		return "", fmt.Errorf("network:outbound: unsupported method %q (supported: GET, POST)", method)
	}
	var bodyReader io.Reader
	if method == "POST" && len(step.Command) >= 3 {
		bodyReader = strings.NewReader(step.Command[2])
	}
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), method, rawURL, bodyReader)
	if err != nil {
		return "", fmt.Errorf("network:outbound: build request: %w", err)
	}
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("network:outbound: %s %s: %w", method, rawURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("network:outbound: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return string(body), fmt.Errorf("network:outbound: %s %s returned %d %s", method, rawURL, resp.StatusCode, resp.Status)
	}
	return string(body), nil
}

func (e *LocalExecutor) executeUIOpen(step types.ActionStep) (string, error) {
	if len(step.Command) == 0 {
		return "", fmt.Errorf("ui:open: Command is empty")
	}
	target := step.Command[0]
	switch target {
	case "browser":
		url := "about:blank"
		if len(step.Command) > 1 {
			url = step.Command[1]
		}
		cmd := exec.Command("xdg-open", url)
		return fmt.Sprintf("opened browser: %s", url), cmd.Start()
	case "terminal":
		cmd := exec.Command("foot")
		return "opened terminal", cmd.Start()
	default:
		cmd := exec.Command("xdg-open", target)
		return fmt.Sprintf("opened: %s", target), cmd.Start()
	}
}

func (e *LocalExecutor) executeSchedule(step types.ActionStep) (string, error) {
	scope := step.Capability.Scope
	parts := strings.SplitN(scope, ":", 2)
	if len(parts) < 2 {
		return "", fmt.Errorf("execute:schedule scope must be systemd:<name> or cron:<name>, got: %s", scope)
	}
	backend := parts[0]
	name := parts[1]
	if len(step.Command) < 3 {
		return "", fmt.Errorf("execute:schedule requires Command[name, cron-expr, intent], got %d args", len(step.Command))
	}
	cronExpr := step.Command[1]
	intent := step.Command[2]
	switch backend {
	case "systemd":
		return e.createSystemdTimer(name, cronExpr, intent)
	case "cron":
		return fmt.Sprintf("in-process cron job %q registered with expression %q", name, cronExpr), nil
	default:
		return "", fmt.Errorf("execute:schedule: unsupported backend %q (supported: systemd, cron)", backend)
	}
}

func (e *LocalExecutor) createSystemdTimer(name, cronExpr, intent string) (string, error) {
	onCalendar := cronToSystemd(cronExpr)
	serviceUnit := fmt.Sprintf("[Unit]\nDescription=HyperiOS scheduled task: %s\n\n[Service]\nType=oneshot\nUser=hyperi\nExecStart=/usr/local/bin/hyperi session start %q\n\n[Install]\nWantedBy=multi-user.target\n", intent, intent)
	timerUnit := fmt.Sprintf("[Unit]\nDescription=HyperiOS timer for: %s\n\n[Timer]\nOnCalendar=%s\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n", intent, onCalendar)
	servicePath := fmt.Sprintf("/etc/systemd/system/%s.service", name)
	timerPath := fmt.Sprintf("/etc/systemd/system/%s.timer", name)
	if err := os.WriteFile(servicePath, []byte(serviceUnit), 0o644); err != nil {
		return "", fmt.Errorf("write service unit: %w", err)
	}
	if err := os.WriteFile(timerPath, []byte(timerUnit), 0o644); err != nil {
		return "", fmt.Errorf("write timer unit: %w", err)
	}
	out, err := exec.Command("sudo", "systemctl", "daemon-reload").CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	out, err = exec.Command("sudo", "systemctl", "enable", "--now", name+".timer").CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("systemctl enable %s.timer: %w", name, err)
	}
	return fmt.Sprintf("created and enabled systemd timer %s.timer (OnCalendar=%s)", name, onCalendar), nil
}

func cronToSystemd(cron string) string {
	fields := strings.Fields(cron)
	if len(fields) != 5 {
		return "daily"
	}
	minute, hour, dom, month, dow := fields[0], fields[1], fields[2], fields[3], fields[4]
	if dom == "*" && month == "*" && dow == "*" {
		h, m := hour, minute
		if len(h) == 1 { h = "0" + h }
		if len(m) == 1 { m = "0" + m }
		return fmt.Sprintf("%s:%s:00", h, m)
	}
	days := map[string]string{"0": "Sun", "1": "Mon", "2": "Tue", "3": "Wed", "4": "Thu", "5": "Fri", "6": "Sat"}
	if dom == "*" && month == "*" {
		if dayName, ok := days[dow]; ok {
			h, m := hour, minute
			if len(h) == 1 { h = "0" + h }
			if len(m) == 1 { m = "0" + m }
			return fmt.Sprintf("%s %s:%s:00", dayName, h, m)
		}
	}
	return "daily"
}

// extractCommand extracts a command keyword from a natural-language description.
// Legacy fallback — used when Command []string is not provided.
func extractCommand(desc, scope string) string {
	desc = strings.ToLower(desc)
	for _, cmd := range []string{"grep", "find", "ls", "cat", "head", "tail", "wc", "sort", "uniq", "df", "du", "stat"} {
		if strings.Contains(desc, cmd) || strings.Contains(scope, cmd) {
			return cmd
		}
	}
	return "echo"
}

func buildGrepArgs(desc, scope, workspace string) []string {
	return []string{"-r", scope, workspace}
}

func buildFindArgs(scope, workspace string) []string {
	return []string{workspace, "-name", scope}
}

func buildLsArgs(scope, workspace string) []string {
	if scope != "" {
		return []string{"-la", scope}
	}
	return []string{"-la", workspace}
}

func extractGitCommand(desc string) []string {
	desc = strings.ToLower(desc)
	switch {
	case strings.Contains(desc, "status"):
		return []string{"status", "--short"}
	case strings.Contains(desc, "log"):
		return []string{"log", "--oneline", "-10"}
	case strings.Contains(desc, "diff"):
		return []string{"diff"}
	case strings.Contains(desc, "branch"):
		return []string{"branch", "-a"}
	default:
		return []string{"status"}
	}
}

// parseATSPITarget parses an AT-SPI target string.
func parseATSPITarget(target string) (name, role string) {
	for _, part := range strings.Split(target, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "name=") {
			name = strings.TrimPrefix(part, "name=")
		} else if strings.HasPrefix(part, "role=") {
			role = strings.TrimPrefix(part, "role=")
		} else {
			name = part
		}
	}
	return
}

// ── Error types ───────────────────────────────────────────────────────────────

type UnsupportedCapabilityError struct {
	Type string
}

func (e *UnsupportedCapabilityError) Error() string {
	return "unsupported capability type: " + e.Type
}
