// Package ui provides the web server and display management for HyperiOS.
// This file implements mouse/keyboard control via ydotool (Wayland-compatible
// input injection) and swaymsg for window focus.
package ui

import (
	"fmt"
	"os/exec"
	"strconv"
)

// AppController injects mouse clicks and keyboard input on Linux/Wayland.
// It uses ydotool for input injection (requires ydotoold service running).
type AppController struct{}

func NewAppController() *AppController {
	return &AppController{}
}

// Click injects a left mouse click at the given screen coordinates.
// Requires: ydotoold running as a service (part of HyperiOS base install).
func (c *AppController) Click(x, y int) error {
	// Move to position then click
	moveCmd := exec.Command("ydotool", "mousemove", "--absolute",
		"-x", strconv.Itoa(x),
		"-y", strconv.Itoa(y),
	)
	if err := moveCmd.Run(); err != nil {
		return fmt.Errorf("ydotool mousemove: %w", err)
	}

	clickCmd := exec.Command("ydotool", "click", "0xC0") // left button down+up
	if err := clickCmd.Run(); err != nil {
		return fmt.Errorf("ydotool click: %w", err)
	}
	return nil
}

// Type injects keyboard input as a sequence of characters.
func (c *AppController) Type(text string) error {
	cmd := exec.Command("ydotool", "type", "--", text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ydotool type: %w", err)
	}
	return nil
}
