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

// GoalTransition records a goal state change.
type GoalTransition struct {
	GoalID    string `json:"goal_id"`
	FromState string `json:"from_state"`
	ToState   string `json:"to_state"`
	Reason    string `json:"reason,omitempty"`
}

// ToolAuthEvent records a tool authorization request or decision.
type ToolAuthEvent struct {
	ToolID       string `json:"tool_id"`
	Scope        string `json:"scope"`
	AuthorizedBy string `json:"authorized_by,omitempty"`
	Approved     bool   `json:"approved"`
	Reason       string `json:"reason,omitempty"`
}

// AgentSpawnEvent records the creation of a sub-agent.
type AgentSpawnEvent struct {
	AgentType  string `json:"agent_type"`
	ParentID   string `json:"parent_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
	SpawnCount int    `json:"spawn_count"`
}

// SelfImprovementGoal records a goal created by the self-improvement system.
type SelfImprovementGoal struct {
	GoalID      string `json:"goal_id"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Confidence  float64 `json:"confidence"`
}

// LogGoalTransition logs a goal state transition.
func (l *Logger) LogGoalTransition(sessionID string, t GoalTransition) error {
	return l.Log(sessionID, "goal:transition", t, nil)
}

// LogToolAuthorization logs a tool authorization event.
func (l *Logger) LogToolAuthorization(sessionID string, t ToolAuthEvent) error {
	return l.Log(sessionID, "tool:authorization", t, nil)
}

// LogAgentSpawn logs the creation of a sub-agent.
func (l *Logger) LogAgentSpawn(sessionID string, e AgentSpawnEvent) error {
	return l.Log(sessionID, "agent:spawn", e, nil)
}

// LogSelfImprovementGoal logs a self-improvement goal creation.
func (l *Logger) LogSelfImprovementGoal(sessionID string, g SelfImprovementGoal) error {
	return l.Log(sessionID, "selfimprovement:goal", g, nil)
}
