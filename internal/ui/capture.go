// Package ui provides the web server and display management for HyperiOS.
// This file implements screen capture via grim (Wayland screenshot tool).
// On a running HyperiOS system this requires: grim, wl-clipboard, and a
// Wayland compositor (sway). The WAYLAND_DISPLAY env var must be set.
package ui

import (
	"fmt"
	"os/exec"
	"sync"
)

// ScreenCapture captures frames from the Wayland compositor using grim.
type ScreenCapture struct {
	mu            sync.RWMutex
	capturing     bool
	currentWindow string
}

func NewScreenCapture() *ScreenCapture {
	return &ScreenCapture{}
}

// Capture takes a screenshot using grim and returns a base64-encoded PNG.
// On HyperiOS this calls: grim -t png - | base64 -w 0
// TODO(Phase 4): Wire up wlr-screencopy protocol directly for lower latency.
func (sc *ScreenCapture) Capture() ([]byte, error) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	// grim writes PNG to stdout; we pipe through base64
	cmd := exec.Command("sh", "-c", "grim -t png - | base64 -w 0")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("grim capture: %w", err)
	}
	return output, nil
}

func (sc *ScreenCapture) IsCapturing() bool {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.capturing
}

func (sc *ScreenCapture) Stop() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.capturing = false
}

func (sc *ScreenCapture) Start() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.capturing = true
}

func (sc *ScreenCapture) SetWindow(windowID string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.currentWindow = windowID
}

func (sc *ScreenCapture) CurrentWindow() string {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.currentWindow
}
