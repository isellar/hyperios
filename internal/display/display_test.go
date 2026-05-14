package display

import (
	"os"
	"testing"
)

// All tests in this package run without a Wayland compositor.
// Tests that require sway/AT-SPI/grim are skipped when the compositor
// is unavailable. Full integration tests require real Linux hardware
// with sway running.

func TestSwayClient_IsAvailable_NoCompositor(t *testing.T) {
	// In WSL2 and headless environments, SWAYSOCK is not set
	// and IsAvailable should return false
	if os.Getenv("SWAYSOCK") != "" {
		t.Skip("SWAYSOCK is set — skipping no-compositor test")
	}
	c := NewSwayClient()
	if c.IsAvailable() {
		t.Error("expected IsAvailable=false when SWAYSOCK is not set")
	}
}

func TestSwayClient_Run_NoCompositor(t *testing.T) {
	if os.Getenv("SWAYSOCK") != "" {
		t.Skip("SWAYSOCK is set — skipping no-compositor test")
	}
	c := NewSwayClient()
	_, err := c.Run("workspace", "2")
	if err != ErrNoCompositor {
		t.Errorf("expected ErrNoCompositor, got %v", err)
	}
}

func TestSwayClient_FindNode_EmptyTree(t *testing.T) {
	root := &Node{
		Name:  "root",
		Nodes: []Node{},
	}
	result := findNode(root, "firefox", "")
	if result != nil {
		t.Error("expected nil for empty tree")
	}
}

func TestSwayClient_FindNode_Match(t *testing.T) {
	root := &Node{
		Name: "root",
		Type: "root",
		Nodes: []Node{
			{
				Name: "output",
				Type: "output",
				Nodes: []Node{
					{
						Name:  "workspace 1",
						Type:  "workspace",
						Nodes: []Node{},
					},
					{
						Name:  "workspace 2",
						Type:  "workspace",
						Nodes: []Node{
							{
								Name:  "Firefox",
								AppID: "firefox",
								Type:  "con",
							},
						},
					},
				},
			},
		},
	}

	// Find by app_id
	result := findNode(root, "firefox", "")
	if result == nil {
		t.Fatal("expected to find firefox node")
	}
	if result.AppID != "firefox" {
		t.Errorf("expected app_id firefox, got %q", result.AppID)
	}

	// Find by title substring
	result = findNode(root, "", "Firefox")
	if result == nil {
		t.Fatal("expected to find Firefox by title")
	}

	// No match
	result = findNode(root, "chromium", "")
	if result != nil {
		t.Error("expected nil for non-existent app")
	}
}

func TestCapturer_IsAvailable_NoWayland(t *testing.T) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		t.Skip("WAYLAND_DISPLAY is set — skipping no-Wayland test")
	}
	c := NewCapturer("")
	if c.IsAvailable() {
		t.Error("expected IsAvailable=false when WAYLAND_DISPLAY is not set")
	}
}

func TestCapturer_Capture_NoCompositor(t *testing.T) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		t.Skip("WAYLAND_DISPLAY is set — skipping no-compositor test")
	}
	c := NewCapturer("")
	_, err := c.Capture()
	if err != ErrNoCompositor {
		t.Errorf("expected ErrNoCompositor, got %v", err)
	}
}

func TestParseCriteria(t *testing.T) {
	tests := []struct {
		input   string
		appID   string
		title   string
	}{
		{"app_id=firefox", "firefox", ""},
		{"title=nginx", "", "nginx"},
		{"nginx status", "", "nginx status"},
		{"app_id=foot", "foot", ""},
	}
	for _, tt := range tests {
		appID, title := ParseCriteria(tt.input)
		if appID != tt.appID {
			t.Errorf("parseCriteria(%q) appID = %q, want %q", tt.input, appID, tt.appID)
		}
		if title != tt.title {
			t.Errorf("parseCriteria(%q) title = %q, want %q", tt.input, title, tt.title)
		}
	}
}
