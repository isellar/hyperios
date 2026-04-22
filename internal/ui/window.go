// Package ui provides the web server and display management for HyperiOS.
// This file implements window enumeration and focus via swaymsg IPC.
package ui

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// WindowManager lists and focuses Wayland windows via swaymsg.
type WindowManager struct{}

func NewWindowManager() *WindowManager {
	return &WindowManager{}
}

// swayNode is used to parse the sway tree JSON for window enumeration.
type swayNode struct {
	ID      int        `json:"id"`
	Name    string     `json:"name"`
	AppID   string     `json:"app_id"`
	Nodes   []swayNode `json:"nodes"`
	Focused bool       `json:"focused"`
	Type    string     `json:"type"`
}

// ListWindows returns all visible application windows from the sway tree.
func (wm *WindowManager) ListWindows() ([]WindowInfo, error) {
	out, err := exec.Command("swaymsg", "-t", "get_tree").Output()
	if err != nil {
		return nil, fmt.Errorf("swaymsg get_tree: %w", err)
	}

	var root swayNode
	if err := json.Unmarshal(out, &root); err != nil {
		return nil, fmt.Errorf("parse sway tree: %w", err)
	}

	var windows []WindowInfo
	collectWindows(&root, &windows)
	return windows, nil
}

func collectWindows(node *swayNode, out *[]WindowInfo) {
	if node.Type == "con" && node.AppID != "" {
		*out = append(*out, WindowInfo{
			ID:    fmt.Sprintf("%d", node.ID),
			Title: node.Name,
			App:   node.AppID,
		})
	}
	for i := range node.Nodes {
		collectWindows(&node.Nodes[i], out)
	}
}

// FocusWindow focuses the window with the given sway node ID via swaymsg.
func (wm *WindowManager) FocusWindow(windowID string) error {
	cmd := exec.Command("swaymsg", fmt.Sprintf("[con_id=%s] focus", windowID))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("swaymsg focus %s: %w", windowID, err)
	}
	return nil
}
