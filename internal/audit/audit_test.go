package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewLogger_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "audit.jsonl")

	logger, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	if logger == nil {
		t.Fatal("expected logger to be non-nil")
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("expected parent directory to be created")
	}
}

func TestNewLogger_Unwritable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping on short mode")
	}
	// This test only makes sense on Linux where /dev/null is a character device
	// and cannot be used as a directory. On Windows we skip it.
	if os.Getenv("GOOS") == "windows" || isWindows() {
		t.Skip("skipping unwritable-path test on Windows")
	}
	_, err := NewLogger("/dev/null/invalid/path/audit.jsonl")
	if err == nil {
		t.Error("expected error for unwritable path")
	}
}

func isWindows() bool {
	return os.PathSeparator == '\\'
}

func TestLogger_Log(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}

	err = logger.Log("session-1", "intent", "input data", "output data")
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty file")
	}
}

func TestLogger_Log_ValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}

	logger.Log("session-1", "intent", map[string]string{"key": "value"}, nil)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Errorf("expected valid JSON: %v", err)
	}
	if entry.SessionID != "session-1" {
		t.Errorf("expected SessionID 'session-1', got %q", entry.SessionID)
	}
	if entry.Stage != "intent" {
		t.Errorf("expected Stage 'intent', got %q", entry.Stage)
	}
}

func TestLogger_Log_Appends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}

	logger.Log("session-1", "stage1", nil, nil)
	logger.Log("session-1", "stage2", nil, nil)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != 2 {
		t.Errorf("expected 2 lines, got %d", lines)
	}
}
