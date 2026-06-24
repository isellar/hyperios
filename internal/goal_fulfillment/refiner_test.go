package goal_fulfillment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

func TestRefiner_RefineGoal_ActivatesGoal(t *testing.T) {
	resp := `{
		"intent": "install nginx",
		"context": "fresh install",
		"goals": [
			{"id": "g1", "description": "install nginx package", "depends_on": []}
		],
		"clarification_needed": false,
		"clarification_question": ""
	}`
	client := &mockCompleter{response: resp}
	r := NewRefiner(client, nil, nil)

	goal := &types.Goal{
		ID:          "g1",
		Description: "install nginx",
		State:       types.GoalStateRefining,
		CreatedAt:   time.Now(),
	}

	refined, err := r.RefineGoal(context.Background(), goal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if refined.State != types.GoalStateActive {
		t.Errorf("expected state %q, got %q", types.GoalStateActive, refined.State)
	}
	if refined.Description != "install nginx package" {
		t.Errorf("expected description %q, got %q", "install nginx package", refined.Description)
	}
}

func TestRefiner_RefineGoal_ClarificationNeeded(t *testing.T) {
	resp := `{
		"intent": "fix the thing",
		"context": "",
		"goals": [],
		"clarification_needed": true,
		"clarification_question": "What thing needs fixing?"
	}`
	client := &mockCompleter{response: resp}
	r := NewRefiner(client, nil, nil)

	goal := &types.Goal{
		ID:          "g1",
		Description: "fix the thing",
		State:       types.GoalStateRefining,
		CreatedAt:   time.Now(),
	}

	_, err := r.RefineGoal(context.Background(), goal)
	if err == nil {
		t.Fatal("expected ClarificationNeededError, got nil")
	}

	var clarErr *ClarificationNeededError
	if !errors.As(err, &clarErr) {
		t.Fatalf("expected ClarificationNeededError, got %T: %v", err, err)
	}
	if clarErr.Question != "What thing needs fixing?" {
		t.Errorf("expected question %q, got %q", "What thing needs fixing?", clarErr.Question)
	}
}

func TestRefiner_RefineGoal_WithMemoryAndProcessor(t *testing.T) {
	resp := `{
		"intent": "install nginx",
		"context": "with memory",
		"goals": [
			{"id": "g1", "description": "install nginx with context", "depends_on": []}
		],
		"clarification_needed": false,
		"clarification_question": ""
	}`
	client := &mockCompleter{response: resp}
	mem := newMockMemory()
	mem.store["install nginx"] = "user previously installed apache"
	proc := newMockProcessor()
	proc.results["install nginx"] = "nginx is available via apt"

	r := NewRefiner(client, mem, proc)

	goal := &types.Goal{
		ID:          "g1",
		Description: "install nginx",
		State:       types.GoalStateRefining,
		CreatedAt:   time.Now(),
	}

	refined, err := r.RefineGoal(context.Background(), goal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if refined.State != types.GoalStateActive {
		t.Errorf("expected state %q, got %q", types.GoalStateActive, refined.State)
	}
}

func TestRefiner_RefineFromIntent(t *testing.T) {
	resp := `{
		"intent": "install nginx",
		"context": "fresh ubuntu",
		"goals": [
			{"id": "g1", "description": "install nginx", "depends_on": []}
		],
		"clarification_needed": false,
		"clarification_question": ""
	}`
	client := &mockCompleter{response: resp}
	r := NewRefiner(client, nil, nil)

	graph, err := r.RefineFromIntent(context.Background(), "install nginx", types.WorkspaceContext{Cwd: "/home/user"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if graph.Intent != "install nginx" {
		t.Errorf("expected intent %q, got %q", "install nginx", graph.Intent)
	}
	if len(graph.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(graph.Goals))
	}
	if graph.Goals[0].State != types.GoalStateActive {
		t.Errorf("expected goal state %q, got %q", types.GoalStateActive, graph.Goals[0].State)
	}
}

func TestRefiner_LLMError(t *testing.T) {
	client := &mockCompleter{err: errors.New("llm error")}
	r := NewRefiner(client, nil, nil)

	goal := &types.Goal{
		ID:          "g1",
		Description: "test",
		State:       types.GoalStateRefining,
		CreatedAt:   time.Now(),
	}

	_, err := r.RefineGoal(context.Background(), goal)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRefiner_MalformedJSON(t *testing.T) {
	client := &mockCompleter{response: `{"intent": "broken`}
	r := NewRefiner(client, nil, nil)

	goal := &types.Goal{
		ID:          "g1",
		Description: "test",
		State:       types.GoalStateRefining,
		CreatedAt:   time.Now(),
	}

	_, err := r.RefineGoal(context.Background(), goal)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestRefiner_MarkdownFence(t *testing.T) {
	resp := "```json\n{\"intent\": \"test\", \"context\": \"\", \"goals\": [{\"id\": \"g1\", \"description\": \"test goal\", \"depends_on\": []}], \"clarification_needed\": false, \"clarification_question\": \"\"}\n```"
	client := &mockCompleter{response: resp}
	r := NewRefiner(client, nil, nil)

	goal := &types.Goal{
		ID:          "g1",
		Description: "test",
		State:       types.GoalStateRefining,
		CreatedAt:   time.Now(),
	}

	refined, err := r.RefineGoal(context.Background(), goal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if refined.State != types.GoalStateActive {
		t.Errorf("expected state %q, got %q", types.GoalStateActive, refined.State)
	}
}
