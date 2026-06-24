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

func TestLogger_LogGoalTransition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}

	gt := GoalTransition{
		GoalID:    "goal-1",
		FromState: "refining",
		ToState:   "active",
		Reason:    "user confirmed",
	}
	if err := logger.LogGoalTransition("session-1", gt); err != nil {
		t.Fatalf("LogGoalTransition failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if entry.Stage != "goal:transition" {
		t.Errorf("expected stage 'goal:transition', got %q", entry.Stage)
	}
}

func TestLogger_LogToolAuthorization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}

	ta := ToolAuthEvent{
		ToolID:       "execute:shell",
		Scope:        "session",
		AuthorizedBy: "user",
		Approved:     true,
	}
	if err := logger.LogToolAuthorization("session-1", ta); err != nil {
		t.Fatalf("LogToolAuthorization failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if entry.Stage != "tool:authorization" {
		t.Errorf("expected stage 'tool:authorization', got %q", entry.Stage)
	}
}

func TestLogger_LogAgentSpawn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}

	as := AgentSpawnEvent{
		AgentType:  "planner",
		ParentID:   "agent-0",
		Reason:     "complex task",
		SpawnCount: 1,
	}
	if err := logger.LogAgentSpawn("session-1", as); err != nil {
		t.Fatalf("LogAgentSpawn failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if entry.Stage != "agent:spawn" {
		t.Errorf("expected stage 'agent:spawn', got %q", entry.Stage)
	}
}

func TestLogger_LogSelfImprovementGoal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}

	sg := SelfImprovementGoal{
		GoalID:      "si-goal-1",
		Description: "optimize cache",
		Source:      "template-generator",
		Confidence:  0.85,
	}
	if err := logger.LogSelfImprovementGoal("session-1", sg); err != nil {
		t.Fatalf("LogSelfImprovementGoal failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if entry.Stage != "selfimprovement:goal" {
		t.Errorf("expected stage 'selfimprovement:goal', got %q", entry.Stage)
	}
}
