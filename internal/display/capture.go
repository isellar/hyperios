package display

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Capturer takes screenshots via grim (Wayland-native screenshot tool).
// Returns ErrNoCompositor when no Wayland session is available.
type Capturer struct {
	outputDir string // directory for captured images; defaults to /tmp
}

// NewCapturer creates a screen capturer.
func NewCapturer(outputDir string) *Capturer {
	if outputDir == "" {
		outputDir = os.TempDir()
	}
	return &Capturer{outputDir: outputDir}
}

// IsAvailable returns true if grim is installed and a Wayland session exists.
func (c *Capturer) IsAvailable() bool {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return false
	}
	_, err := exec.LookPath("grim")
	return err == nil
}

// CaptureResult holds the result of a screen capture.
type CaptureResult struct {
	FilePath  string    // path to the PNG file
	Base64    string    // base64-encoded PNG (for vision model API calls)
	Timestamp time.Time
}

// Capture takes a full-screen screenshot and returns the result.
// The screenshot file is written to the output directory.
func (c *Capturer) Capture() (*CaptureResult, error) {
	if !c.IsAvailable() {
		return nil, ErrNoCompositor
	}

	path := filepath.Join(c.outputDir, fmt.Sprintf("hyperi-screen-%d.png", time.Now().UnixNano()))

	cmd := exec.Command("grim", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("display: grim capture: %w\n%s", err, string(out))
	}

	// Read and base64-encode for vision model calls
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("display: read screenshot: %w", err)
	}

	return &CaptureResult{
		FilePath:  path,
		Base64:    base64.StdEncoding.EncodeToString(data),
		Timestamp: time.Now(),
	}, nil
}

// CaptureRegion captures a specific region of the screen.
// x, y are the top-left coordinates; w, h are width and height in pixels.
func (c *Capturer) CaptureRegion(x, y, w, h int) (*CaptureResult, error) {
	if !c.IsAvailable() {
		return nil, ErrNoCompositor
	}

	path := filepath.Join(c.outputDir, fmt.Sprintf("hyperi-region-%d.png", time.Now().UnixNano()))

	// grim -g "x,y wxh" path
	geometry := fmt.Sprintf("%d,%d %dx%d", x, y, w, h)
	cmd := exec.Command("grim", "-g", geometry, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("display: grim region: %w\n%s", err, string(out))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("display: read screenshot: %w", err)
	}

	return &CaptureResult{
		FilePath:  path,
		Base64:    base64.StdEncoding.EncodeToString(data),
		Timestamp: time.Now(),
	}, nil
}

// CaptureAndDelete takes a screenshot, encodes it, and deletes the file.
// Used when only the base64 content is needed (vision model calls).
func (c *Capturer) CaptureAndDelete() (string, error) {
	result, err := c.Capture()
	if err != nil {
		return "", err
	}
	_ = os.Remove(result.FilePath)
	return result.Base64, nil
}
