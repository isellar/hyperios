// Package display implements HyperiOS display management.
//
// The layered interaction model:
//  1. CLI/API — always preferred; no screen dependency
//  2. AT-SPI  — for native GTK/Qt apps; semantic element access
//  3. Vision  — grim screenshot + LLM vision API; for Electron/browser
//  4. ydotool — last resort; raw coordinate injection
//
// This package implements layers 2–4. Layer 1 is handled by the existing
// execute:shell and execute:process capability handlers.
//
// All display operations require sway to be running with a valid SWAYSOCK.
// On WSL2 without a compositor, all operations return ErrNoCompositor.
package display

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrNoCompositor is returned when SWAYSOCK is not set or swaymsg is unavailable.
// This is the expected state in WSL2 and headless environments.
var ErrNoCompositor = fmt.Errorf("display: no Wayland compositor available (SWAYSOCK not set)")

// SwayClient wraps swaymsg IPC for window and workspace management.
// All methods are no-ops that return ErrNoCompositor when sway is not running.
type SwayClient struct {
	socket string // path to SWAYSOCK; empty if unavailable
}

// NewSwayClient creates a SwayClient. Returns a client regardless of whether
// sway is running — callers should check IsAvailable() before use.
func NewSwayClient() *SwayClient {
	return &SwayClient{
		socket: os.Getenv("SWAYSOCK"),
	}
}

// IsAvailable returns true if sway is running and swaymsg is in PATH.
func (c *SwayClient) IsAvailable() bool {
	if c.socket == "" {
		return false
	}
	if _, err := exec.LookPath("swaymsg"); err != nil {
		return false
	}
	// Verify the socket actually exists
	_, err := os.Stat(c.socket)
	return err == nil
}

// Run executes a swaymsg command and returns the raw JSON output.
// Returns ErrNoCompositor if sway is not available.
func (c *SwayClient) Run(args ...string) ([]byte, error) {
	if !c.IsAvailable() {
		return nil, ErrNoCompositor
	}
	cmd := exec.Command("swaymsg", args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("swaymsg %v: %w\n%s", args, err, string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("swaymsg %v: %w", args, err)
	}
	return out, nil
}

// ── Workspace management ──────────────────────────────────────────────────────

// FocusWorkspace switches to a numbered workspace.
func (c *SwayClient) FocusWorkspace(n int) error {
	_, err := c.Run("workspace", fmt.Sprintf("%d", n))
	return err
}

// FocusWorkspaceByName switches to a named workspace.
func (c *SwayClient) FocusWorkspaceByName(name string) error {
	_, err := c.Run("workspace", name)
	return err
}

// ── Window management ─────────────────────────────────────────────────────────

// LaunchApp launches an application in a given workspace.
// workspace is the sway workspace number (1 = HyperiOS shell, 2+ = apps).
func (c *SwayClient) LaunchApp(workspace int, command string) error {
	// Switch to target workspace then exec
	if err := c.FocusWorkspace(workspace); err != nil {
		return err
	}
	_, err := c.Run("exec", command)
	return err
}

// FocusWindow focuses the first window matching the given criteria.
// criteria is a sway criteria string, e.g. `app_id="firefox"` or `title="nginx"`.
func (c *SwayClient) FocusWindow(criteria string) error {
	_, err := c.Run(fmt.Sprintf("[%s]", criteria), "focus")
	return err
}

// CloseWindow closes the focused window or the window matching criteria.
func (c *SwayClient) CloseWindow(criteria string) error {
	if criteria != "" {
		_, err := c.Run(fmt.Sprintf("[%s]", criteria), "kill")
		return err
	}
	_, err := c.Run("kill")
	return err
}

// MoveWindowToWorkspace moves a window to a workspace.
func (c *SwayClient) MoveWindowToWorkspace(criteria string, workspace int) error {
	_, err := c.Run(fmt.Sprintf("[%s]", criteria), "move", "to", "workspace", fmt.Sprintf("%d", workspace))
	return err
}

// SetFullscreen enables or disables fullscreen for the focused/matched window.
func (c *SwayClient) SetFullscreen(criteria string, enable bool) error {
	action := "enable"
	if !enable {
		action = "disable"
	}
	if criteria != "" {
		_, err := c.Run(fmt.Sprintf("[%s]", criteria), "fullscreen", action)
		return err
	}
	_, err := c.Run("fullscreen", action)
	return err
}

// ── Tree queries ──────────────────────────────────────────────────────────────

// Node represents a node in the sway tree (workspace, container, or window).
type Node struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"` // "root", "output", "workspace", "con", "floating_con"
	AppID       string `json:"app_id"`
	WindowTitle string `json:"window_title,omitempty"`
	Focused     bool   `json:"focused"`
	Nodes       []Node `json:"nodes"`
	FloatNodes  []Node `json:"floating_nodes"`
}

// GetTree returns the full sway tree.
func (c *SwayClient) GetTree() (*Node, error) {
	out, err := c.Run("-t", "get_tree")
	if err != nil {
		return nil, err
	}
	var tree Node
	if err := json.Unmarshal(out, &tree); err != nil {
		return nil, fmt.Errorf("display: parse tree: %w", err)
	}
	return &tree, nil
}

// FindWindow returns the first Node with a matching app_id or title substring.
// Returns nil if not found.
func (c *SwayClient) FindWindow(appID, titleSubstr string) (*Node, error) {
	tree, err := c.GetTree()
	if err != nil {
		return nil, err
	}
	return findNode(tree, appID, titleSubstr), nil
}

// WindowExists returns true if a window matching app_id or title exists.
func (c *SwayClient) WindowExists(appID, titleSubstr string) (bool, error) {
	n, err := c.FindWindow(appID, titleSubstr)
	if err != nil {
		return false, err
	}
	return n != nil, nil
}

// WaitForWindow polls until a window matching criteria appears or timeout expires.
// Used as the implementation behind ReadyCondition type "atspi:present" for
// window-level checks.
func (c *SwayClient) WaitForWindow(appID, titleSubstr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		exists, err := c.WindowExists(appID, titleSubstr)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("display: window %q/%q did not appear within %v", appID, titleSubstr, timeout)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// ParseCriteria parses a sway criteria string like "app_id=firefox" or "title=nginx".
// Exported so the executor and tests can use it.
func ParseCriteria(s string) (appID, title string) {
	if strings.HasPrefix(s, "app_id=") {
		return strings.TrimPrefix(s, "app_id="), ""
	}
	if strings.HasPrefix(s, "title=") {
		return "", strings.TrimPrefix(s, "title=")
	}
	return "", s
}

// findNode recursively searches the sway tree for a node matching criteria.
func findNode(node *Node, appID, titleSubstr string) *Node {
	if node == nil {
		return nil
	}

	match := false
	if appID != "" && node.AppID == appID {
		match = true
	}
	if titleSubstr != "" && strings.Contains(node.Name, titleSubstr) {
		match = true
	}
	if match {
		return node
	}

	for i := range node.Nodes {
		if found := findNode(&node.Nodes[i], appID, titleSubstr); found != nil {
			return found
		}
	}
	for i := range node.FloatNodes {
		if found := findNode(&node.FloatNodes[i], appID, titleSubstr); found != nil {
			return found
		}
	}
	return nil
}
