// Package executor provides Linux-native execution backends for HyperiOS.
// This file implements LocalExecutor, which runs capability-gated actions
// directly on the host Linux system.
package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/capability"
	"github.com/isellar/hyperios/internal/types"
)

type LocalExecutor struct {
	registry  *capability.Registry
	enforcer  *capability.Enforcer
	workspace string
}

func NewLocal(registry *capability.Registry, workspace string) *LocalExecutor {
	return &LocalExecutor{
		registry:  registry,
		enforcer:  capability.NewEnforcer(registry),
		workspace: workspace,
	}
}

func (e *LocalExecutor) Name() string {
	return "local"
}

func (e *LocalExecutor) Validate(step types.ActionStep) error {
	return e.enforcer.Validate(step)
}

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

	var output string
	var execErr error

	switch {
	case strings.HasPrefix(step.Capability.Type, "read:file"):
		output, execErr = e.executeReadFile(step)
	case strings.HasPrefix(step.Capability.Type, "execute:shell"):
		output, execErr = e.executeShell(step)
	case strings.HasPrefix(step.Capability.Type, "execute:git"):
		output, execErr = e.executeGit(step)
	case strings.HasPrefix(step.Capability.Type, "execute:package"):
		output, execErr = e.executePackage(step)
	case strings.HasPrefix(step.Capability.Type, "execute:process"):
		output, execErr = e.executeProcess(step)
	case strings.HasPrefix(step.Capability.Type, "execute:display"):
		output, execErr = e.executeDisplay(step)
	case strings.HasPrefix(step.Capability.Type, "execute:config"):
		output, execErr = e.executeConfig(step)
	case strings.HasPrefix(step.Capability.Type, "execute:network"):
		output, execErr = e.executeNetworkConfig(step)
	case strings.HasPrefix(step.Capability.Type, "network:outbound"):
		output, execErr = e.executeNetworkOutbound(step)
	case strings.HasPrefix(step.Capability.Type, "ui:open"):
		output, execErr = e.executeUIOpen(step)
	default:
		execErr = &UnsupportedCapabilityError{Type: step.Capability.Type}
	}

	result := &types.ExecutionResult{
		StepID:   step.ID,
		Duration: time.Since(start).Milliseconds(),
	}

	if execErr != nil {
		result.Success = false
		result.Error = execErr.Error()
	} else {
		result.Success = true
		result.Output = output
	}

	return result, nil
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
	scope := step.Capability.Scope
	parts := strings.SplitN(scope, ":", 3)
	if len(parts) < 2 {
		return "", fmt.Errorf("execute:package scope must be <manager>:<package> or <manager>:<action>:<package>, got: %s", scope)
	}

	manager := parts[0]
	// Determine action and package
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

// executeDisplay controls the Wayland compositor (sway) via swaymsg IPC.
// Scope format: "sway:<command>" e.g. "sway:workspace 2"
func (e *LocalExecutor) executeDisplay(step types.ActionStep) (string, error) {
	scope := step.Capability.Scope
	if !strings.HasPrefix(scope, "sway:") {
		return "", fmt.Errorf("execute:display scope must be sway:<command>, got: %s", scope)
	}

	swayCmd := strings.TrimPrefix(scope, "sway:")

	cmd := exec.Command("swaymsg", swayCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("swaymsg %q: %w", swayCmd, err)
	}
	return string(output), nil
}

// executeConfig writes configuration files from the step description.
// Scope is the target file path. The actual content comes from step metadata
// (future: template engine). For now, this is a stub that signals intent.
func (e *LocalExecutor) executeConfig(step types.ActionStep) (string, error) {
	targetPath := step.Capability.Scope
	if targetPath == "" {
		return "", fmt.Errorf("execute:config requires a file path in scope")
	}
	// TODO(Phase 3): Implement template engine; write config content to targetPath.
	// For now, return a placeholder indicating what would be written.
	return fmt.Sprintf("[config stub] Would write config to: %s\nDescription: %s", targetPath, step.Description), nil
}

// executeNetworkConfig manages network settings via nmcli.
// Scope format: "nmcli:<action>" e.g. "nmcli:connect:MyWifi"
func (e *LocalExecutor) executeNetworkConfig(step types.ActionStep) (string, error) {
	scope := step.Capability.Scope
	if !strings.HasPrefix(scope, "nmcli:") {
		return "", fmt.Errorf("execute:network scope must be nmcli:<action>, got: %s", scope)
	}

	nmcliArgs := strings.Fields(strings.TrimPrefix(scope, "nmcli:"))
	if len(nmcliArgs) == 0 {
		return "", fmt.Errorf("execute:network: empty nmcli command")
	}

	cmd := exec.Command("nmcli", nmcliArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("nmcli %v: %w", nmcliArgs, err)
	}
	return string(output), nil
}

func (e *LocalExecutor) executeNetworkOutbound(step types.ActionStep) (string, error) {
	// TODO(Phase 3): Implement real HTTP calls.
	scope := step.Capability.Scope
	if scope == "*" {
		return "Network access allowed (wildcard)", nil
	}
	return "Network access allowed to: " + scope, nil
}

// executeUIOpen opens a browser or terminal via xdg-open / the compositor.
func (e *LocalExecutor) executeUIOpen(step types.ActionStep) (string, error) {
	scope := step.Capability.Scope

	switch scope {
	case "browser":
		url := extractURL(step.Description)
		if url == "" {
			url = "https://google.com"
		}
		cmd := exec.Command("xdg-open", url)
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("open browser: %w", err)
		}
		return "Opened browser: " + url, nil
	case "terminal":
		// Try common terminals in order of preference
		for _, term := range []string{"foot", "alacritty", "wezterm", "kitty", "xterm"} {
			if path, err := exec.LookPath(term); err == nil {
				cmd := exec.Command(path)
				if err := cmd.Start(); err == nil {
					return "Opened terminal: " + term, nil
				}
			}
		}
		return "", fmt.Errorf("no supported terminal found (tried: foot, alacritty, wezterm, kitty, xterm)")
	default:
		return "", fmt.Errorf("unsupported ui:open scope: %s", scope)
	}
}

func (e *LocalExecutor) executeGit(step types.ActionStep) (string, error) {
	gitCmd := extractGitCommand(step.Description)
	cmd := exec.Command("git", gitCmd...)
	cmd.Dir = e.workspace
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), err
	}
	return string(output), nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func buildGrepArgs(desc, scope, workspace string) []string {
	pattern := "TODO"
	if strings.Contains(strings.ToLower(desc), "fixme") {
		pattern = "FIXME"
	}
	path := workspace
	if scope != "" && !strings.HasPrefix(scope, "{") {
		path = strings.Split(scope, "**")[0]
		if path == "" {
			path = workspace
		}
	}
	return []string{"-rn", "--", pattern, path}
}

func buildFindArgs(scope, workspace string) []string {
	path := workspace
	if scope != "" && !strings.HasPrefix(scope, "{") {
		path = scope
	}
	return []string{path, "-name", "*.go"}
}

func buildLsArgs(scope, workspace string) []string {
	path := workspace
	if scope != "" && !strings.HasPrefix(scope, "{") {
		path = scope
	}
	return []string{"-la", path}
}

func extractCommand(description, scope string) string {
	desc := strings.ToLower(description)
	commandMap := map[string]string{
		"list":    "ls",
		"search":  "grep",
		"count":   "wc",
		"read":    "cat",
		"show":    "cat",
		"display": "cat",
	}
	commands := []string{
		"grep", "find", "cat", "head", "tail", "ls", "pwd", "wc", "sort", "uniq",
		"list", "search", "count", "read", "show", "display",
	}
	for _, cmd := range commands {
		if strings.Contains(desc, cmd+" ") || strings.Contains(desc, cmd+".") || strings.Contains(desc, cmd+"ing") {
			if actual, ok := commandMap[cmd]; ok {
				return actual
			}
			return cmd
		}
	}
	return scope
}

func extractGitCommand(description string) []string {
	desc := strings.ToLower(description)
	if strings.Contains(desc, "status") {
		return []string{"status"}
	}
	if strings.Contains(desc, "log") && !strings.Contains(desc, "catalog") {
		return []string{"log", "--oneline", "-10"}
	}
	if strings.Contains(desc, "diff") {
		return []string{"diff", "--stat"}
	}
	if strings.Contains(desc, "branch") {
		return []string{"branch", "-a"}
	}
	return []string{"status"}
}

func extractURL(description string) string {
	desc := strings.ToLower(description)
	for _, scheme := range []string{"https://", "http://"} {
		if idx := strings.Index(desc, scheme); idx >= 0 {
			end := strings.Index(desc[idx:], " ")
			if end == -1 {
				end = len(desc)
			} else {
				end += idx
			}
			return desc[idx:end]
		}
	}
	return ""
}

// ── Error types ───────────────────────────────────────────────────────────────

type UnsupportedCapabilityError struct {
	Type string
}

func (e *UnsupportedCapabilityError) Error() string {
	return "unsupported capability type: " + e.Type
}
