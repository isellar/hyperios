package tools

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
)

// NotifyTool sends a desktop notification to the user.
// On Linux it uses notify-send. On other platforms it logs to stderr.
type NotifyTool struct{}

// NewNotifyTool creates a NotifyTool.
func NewNotifyTool() *NotifyTool {
	return &NotifyTool{}
}

// Name returns "notify".
func (t *NotifyTool) Name() string { return "notify" }

// Description returns a description of the notify tool.
func (t *NotifyTool) Description() string {
	return "Send a desktop notification to the user"
}

// Execute sends message as a desktop notification.
// On Linux it invokes notify-send. On other platforms it logs the message
// and returns a note that native notifications are not available.
func (t *NotifyTool) Execute(message string) (string, error) {
	if runtime.GOOS == "linux" {
		out, err := exec.Command("notify-send", "HyperiOS", message).CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("notify: notify-send failed: %w", err)
		}
		return "notification sent", nil
	}

	// Non-Linux fallback: log the notification instead of crashing.
	log.Printf("[notify] %s", message)
	return fmt.Sprintf("notification logged (notify-send not available on %s)", runtime.GOOS), nil
}
