package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry is one pipeline stage logged to the audit file.
type Entry struct {
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`
	Stage     string    `json:"stage"`
	Input     any       `json:"input"`
	Output    any       `json:"output"`
}

// Logger appends audit entries to a JSONL file.
type Logger struct {
	path string
}

// NewLogger returns a Logger that writes to path, creating it if needed.
func NewLogger(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("audit: create log dir: %w", err)
	}
	return &Logger{path: path}, nil
}

// Log appends a single entry to the audit file.
func (l *Logger) Log(sessionID, stage string, input, output any) error {
	entry := Entry{
		Timestamp: time.Now().UTC(),
		SessionID: sessionID,
		Stage:     stage,
		Input:     input,
		Output:    output,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("audit: marshal: %w", err)
	}

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit: open: %w", err)
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}
