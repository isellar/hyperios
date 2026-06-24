package goal_fulfillment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

func TestBreakdown_BreakdownGoal(t *testing.T) {
	resp := `{
		"parent_id": "g1",
		"sub_goals": [
			{"id": "g1-1", "description": "check if nginx is installed", "depends_on": [], "is_atomic": true},
			{"id": "g1-2", "description": "install nginx", "depends_on": ["g1-1"], "is_atomic": true}
		]
	}`
	client := &mockCompleter{response: resp}
	b := NewBreakdown(client)

	goal := &types.Goal{
		ID:          "g1",
		Description: "install and configure nginx",
		State:       types.GoalStateActive,
		CreatedAt:   time.Now(),
	}

	subGoals, err := b.BreakdownGoal(context.Background(), goal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(subGoals) != 2 {
		t.Fatalf("expected 2 sub-goals, got %d", len(subGoals))
	}
	if subGoals[0].ID != "g1-1" {
		t.Errorf("expected sub-goal ID %q, got %q", "g1-1", subGoals[0].ID)
	}
	if subGoals[1].ID != "g1-2" {
		t.Errorf("expected sub-goal ID %q, got %q", "g1-2", subGoals[1].ID)
	}
	if len(goal.SubGoals) != 2 {
		t.Errorf("expected parent to have 2 sub-goal IDs, got %d", len(goal.SubGoals))
	}
}

func TestBreakdown_BreakdownGoal_AtomicGoal(t *testing.T) {
	resp := `{
		"parent_id": "g1",
		"sub_goals": []
	}`
	client := &mockCompleter{response: resp}
	b := NewBreakdown(client)

	goal := &types.Goal{
		ID:          "g1",
		Description: "run ls",
		State:       types.GoalStateActive,
		CreatedAt:   time.Now(),
	}

	subGoals, err := b.BreakdownGoal(context.Background(), goal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(subGoals) != 1 {
		t.Fatalf("expected 1 goal (atomic), got %d", len(subGoals))
	}
	if subGoals[0].ID != goal.ID {
		t.Errorf("expected atomic goal to be same as input, got %q", subGoals[0].ID)
	}
}

func TestBreakdown_ValidateSubGoals(t *testing.T) {
	b := NewBreakdown(nil)

	goals := []*types.Goal{
		{ID: "g1", Description: "first"},
		{ID: "g2", Description: "second", DependsOn: []string{"g1"}},
	}

	if err := b.ValidateSubGoals(goals); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBreakdown_ValidateSubGoals_EmptyID(t *testing.T) {
	b := NewBreakdown(nil)

	goals := []*types.Goal{
		{ID: "", Description: "no id"},
	}

	if err := b.ValidateSubGoals(goals); err == nil {
		t.Fatal("expected error for empty ID, got nil")
	}
}

func TestBreakdown_ValidateSubGoals_DuplicateID(t *testing.T) {
	b := NewBreakdown(nil)

	goals := []*types.Goal{
		{ID: "g1", Description: "first"},
		{ID: "g1", Description: "duplicate"},
	}

	if err := b.ValidateSubGoals(goals); err == nil {
		t.Fatal("expected error for duplicate ID, got nil")
	}
}

func TestBreakdown_ValidateSubGoals_EmptyDescription(t *testing.T) {
	b := NewBreakdown(nil)

	goals := []*types.Goal{
		{ID: "g1", Description: ""},
	}

	if err := b.ValidateSubGoals(goals); err == nil {
		t.Fatal("expected error for empty description, got nil")
	}
}

func TestBreakdown_ValidateSubGoals_UnknownDependency(t *testing.T) {
	b := NewBreakdown(nil)

	goals := []*types.Goal{
		{ID: "g1", Description: "first", DependsOn: []string{"g99"}},
	}

	if err := b.ValidateSubGoals(goals); err == nil {
		t.Fatal("expected error for unknown dependency, got nil")
	}
}

func TestBreakdown_LLMError(t *testing.T) {
	client := &mockCompleter{err: errors.New("llm error")}
	b := NewBreakdown(client)

	goal := &types.Goal{
		ID:          "g1",
		Description: "test",
		State:       types.GoalStateActive,
		CreatedAt:   time.Now(),
	}

	_, err := b.BreakdownGoal(context.Background(), goal)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestBreakdown_Recursive(t *testing.T) {
	callCount := 0
	responses := []string{
		`{"parent_id": "g1", "sub_goals": [{"id": "g1-1", "description": "sub1", "depends_on": []}, {"id": "g1-2", "description": "sub2", "depends_on": ["g1-1"]}]}`,
		`{"parent_id": "g1-1", "sub_goals": []}`,
		`{"parent_id": "g1-2", "sub_goals": []}`,
	}
	client := &mockCompleterFunc{
		fn: func(ctx context.Context, system, user string) (string, error) {
			if callCount >= len(responses) {
				return `{"parent_id": "", "sub_goals": []}`, nil
			}
			resp := responses[callCount]
			callCount++
			return resp, nil
		},
	}
	b := NewBreakdown(client)

	goal := &types.Goal{
		ID:          "g1",
		Description: "complex task",
		State:       types.GoalStateActive,
		CreatedAt:   time.Now(),
	}

	subGoals, err := b.BreakdownRecursive(context.Background(), goal, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(subGoals) != 2 {
		t.Fatalf("expected 2 sub-goals, got %d", len(subGoals))
	}
}

type mockCompleterFunc struct {
	fn func(ctx context.Context, system, user string) (string, error)
}

func (m *mockCompleterFunc) Complete(ctx context.Context, system, user string) (string, error) {
	return m.fn(ctx, system, user)
}

func (m *mockCompleterFunc) CompleteWithRetry(ctx context.Context, system, user string) (string, error) {
	return m.fn(ctx, system, user)
}
