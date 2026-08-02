package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isellar/hyperios/internal/audit"
	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/memory"
	"github.com/isellar/hyperios/internal/types"
)

// ---------------------------------------------------------------------------
// newTestConfig
// ---------------------------------------------------------------------------

// newTestConfig creates a *config.Config that uses temp directories for all
// storage so tests are fully isolated from each other and from the real system.
func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	tmp := t.TempDir()

	cfg := config.Defaults()
	cfg.GoalStoragePath = filepath.Join(tmp, "goals", "goals.json")
	cfg.MemoryStoragePath = filepath.Join(tmp, "memory")
	cfg.ToolAuthStoragePath = filepath.Join(tmp, "tool_auth.json")
	// Use trusted autonomy so tests don't hang waiting for approvals.
	cfg.AutonomyLevel = config.AutonomyTrusted
	return cfg
}

// ---------------------------------------------------------------------------
// mockLLMClient
// ---------------------------------------------------------------------------

// mockLLMClient satisfies llm.Completer.  It returns canned JSON appropriate
// for the system prompt it receives.
type mockLLMClient struct {
	// override completely overrides the response for every call if non-empty.
	override string
}

func newMockLLMClient() *mockLLMClient {
	return &mockLLMClient{}
}

// Complete delegates to CompleteWithRetry — identical behaviour for mocks.
func (m *mockLLMClient) Complete(ctx context.Context, system, user string) (string, error) {
	return m.CompleteWithRetry(ctx, system, user)
}

// CompleteWithRetry returns canned JSON based on which agent is calling.
// It inspects the system prompt to decide which schema to return.
func (m *mockLLMClient) CompleteWithRetry(ctx context.Context, system, user string) (string, error) {
	if m.override != "" {
		return m.override, nil
	}

	switch {
	// Breakdown agent: expects {"parent_id":…,"sub_goals":[…]}
	case strings.Contains(system, "Goal Breakdown Agent"):
		return m.breakdownResponse(user), nil

	// Refiner agent: expects {"intent":…,"goals":[…],…}
	case strings.Contains(system, "Goal Refiner"):
		return m.refinerResponse(user), nil

	// Analyzer (self-improvement): expects {"patterns":…,"improvement_goals":[…]}
	case strings.Contains(system, "AI system analyst"):
		return m.analyzerResponse(), nil

	// Autonomous execution agent: plain-text paragraph
	default:
		return "I would fulfil this goal by following the necessary steps systematically.", nil
	}
}

// breakdownResponse returns a minimal valid breakdown JSON.
func (m *mockLLMClient) breakdownResponse(user string) string {
	// Extract parent ID from the user prompt ("ID: <id>").
	parentID := "parent-goal"
	for _, line := range strings.Split(user, "\n") {
		if strings.HasPrefix(line, "ID: ") {
			parentID = strings.TrimPrefix(line, "ID: ")
			break
		}
	}

	resp := map[string]interface{}{
		"parent_id": parentID,
		"sub_goals": []map[string]interface{}{
			{
				"id":          fmt.Sprintf("%s-sub-1", parentID),
				"description": "First sub-goal step",
				"depends_on":  []string{},
				"is_atomic":   true,
			},
			{
				"id":          fmt.Sprintf("%s-sub-2", parentID),
				"description": "Second sub-goal step",
				"depends_on":  []string{fmt.Sprintf("%s-sub-1", parentID)},
				"is_atomic":   true,
			},
		},
	}
	data, _ := json.Marshal(resp)
	return string(data)
}

// refinerResponse returns a minimal valid refiner JSON.
func (m *mockLLMClient) refinerResponse(user string) string {
	resp := map[string]interface{}{
		"intent":                 "refined intent",
		"context":                "test context",
		"clarification_needed":   false,
		"clarification_question": "",
		"goals": []map[string]interface{}{
			{
				"id":          "refined-g1",
				"description": "Refined goal description",
				"depends_on":  []string{},
			},
		},
	}
	data, _ := json.Marshal(resp)
	return string(data)
}

// analyzerResponse returns a minimal valid analyzer JSON with one improvement goal.
func (m *mockLLMClient) analyzerResponse() string {
	resp := map[string]interface{}{
		"patterns":          []string{"recurring failure pattern detected"},
		"suggestions":       []string{"add retry logic"},
		"improvement_goals": []string{"Implement automatic retry for failed goals"},
	}
	data, _ := json.Marshal(resp)
	return string(data)
}

// Compile-time assertion that mockLLMClient satisfies llm.Completer.
var _ llm.Completer = (*mockLLMClient)(nil)

// ---------------------------------------------------------------------------
// newMockAudit
// ---------------------------------------------------------------------------

// newMockAudit creates an *audit.Logger writing to a temp file.
func newMockAudit(t *testing.T) *audit.Logger {
	t.Helper()
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "audit.jsonl")
	logger, err := audit.NewLogger(logPath)
	if err != nil {
		t.Fatalf("newMockAudit: %v", err)
	}
	return logger
}

// ---------------------------------------------------------------------------
// mockMemoryQuerier — satisfies processor.MemoryQuerier for processor tests.
// ---------------------------------------------------------------------------

type mockMemoryQuerier struct {
	entries    map[string]string
	directives []types.Directive
}

func newMockMemoryQuerier() *mockMemoryQuerier {
	return &mockMemoryQuerier{entries: make(map[string]string)}
}

func (m *mockMemoryQuerier) RecallContext(key string) (interface{}, bool) {
	v, ok := m.entries[key]
	return v, ok
}

func (m *mockMemoryQuerier) SearchContext(query string) ([]*memory.MemoryEntry, error) {
	var results []*memory.MemoryEntry
	q := strings.ToLower(query)
	for k, v := range m.entries {
		if strings.Contains(strings.ToLower(k), q) || strings.Contains(strings.ToLower(v), q) {
			results = append(results, &memory.MemoryEntry{Key: k, Value: v})
		}
	}
	return results, nil
}

func (m *mockMemoryQuerier) ListDirectives() ([]types.Directive, error) {
	return m.directives, nil
}

// ---------------------------------------------------------------------------
// mockGoalUpdater — satisfies processor.GoalUpdater for processor tests.
// ---------------------------------------------------------------------------

type mockGoalUpdater struct {
	updates []goalUpdate
}

type goalUpdate struct {
	id    string
	state types.GoalState
}

func (m *mockGoalUpdater) UpdateGoalState(id string, state types.GoalState) error {
	m.updates = append(m.updates, goalUpdate{id: id, state: state})
	return nil
}

// ---------------------------------------------------------------------------
// mockGoalSubmitter — satisfies self_improvement.GoalSubmitter.
// ---------------------------------------------------------------------------

type mockGoalSubmitter struct {
	submitted []*types.Goal
}

func (m *mockGoalSubmitter) SubmitGoal(description string) (*types.Goal, error) {
	g := &types.Goal{
		ID:          fmt.Sprintf("mock-g-%d", len(m.submitted)+1),
		Description: description,
		State:       types.GoalStateRefining,
	}
	m.submitted = append(m.submitted, g)
	return g, nil
}
