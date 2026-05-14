package session

import (
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

func TestNewState(t *testing.T) {
	ctx := types.WorkspaceContext{Cwd: "/repo", GitBranch: "main"}
	state := NewState("test-id", "test intent", ctx)

	if state.ID != "test-id" {
		t.Errorf("expected ID 'test-id', got %q", state.ID)
	}
	if state.Intent != "test intent" {
		t.Errorf("expected Intent 'test intent', got %q", state.Intent)
	}
	if state.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if state.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
	if !state.CreatedAt.Equal(state.UpdatedAt) {
		t.Error("expected CreatedAt == UpdatedAt at construction")
	}
}

func TestState_MarkCompleted(t *testing.T) {
	state := NewState("test-id", "test intent", types.WorkspaceContext{})
	initialUpdatedAt := state.UpdatedAt

	time.Sleep(time.Millisecond)
	state.MarkCompleted("s1")

	if len(state.Completed) != 1 {
		t.Errorf("expected 1 completed step, got %d", len(state.Completed))
	}
	if state.Completed[0] != "s1" {
		t.Errorf("expected 's1', got %q", state.Completed[0])
	}
	if !state.UpdatedAt.After(initialUpdatedAt) {
		t.Error("expected UpdatedAt to be updated")
	}
}

func TestState_IsCompleted(t *testing.T) {
	state := NewState("test-id", "test intent", types.WorkspaceContext{})
	state.MarkCompleted("s1")

	if !state.IsCompleted("s1") {
		t.Error("expected s1 to be completed")
	}
	if state.IsCompleted("s2") {
		t.Error("expected s2 to not be completed")
	}
}

func TestState_RemainingSteps(t *testing.T) {
	state := NewState("test-id", "test intent", types.WorkspaceContext{})
	state.Plan = &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1"},
			{ID: "s2"},
			{ID: "s3"},
		},
	}
	state.MarkCompleted("s1")

	remaining := state.RemainingSteps()
	if len(remaining) != 2 {
		t.Errorf("expected 2 remaining steps, got %d", len(remaining))
	}
}

func TestState_RemainingSteps_NilPlan(t *testing.T) {
	state := NewState("test-id", "test intent", types.WorkspaceContext{})
	state.Plan = nil

	remaining := state.RemainingSteps()
	if len(remaining) != 0 {
		t.Errorf("expected 0 remaining steps, got %d", len(remaining))
	}
}

func TestState_Progress(t *testing.T) {
	state := NewState("test-id", "test intent", types.WorkspaceContext{})
	state.Plan = &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1"},
			{ID: "s2"},
			{ID: "s3"},
		},
	}
	state.MarkCompleted("s1")
	state.MarkCompleted("s2")

	completed, total := state.Progress()
	if total != 3 {
		t.Errorf("expected total 3, got %d", total)
	}
	if completed != 2 {
		t.Errorf("expected completed 2, got %d", completed)
	}
}

func TestState_Progress_NilPlan(t *testing.T) {
	state := NewState("test-id", "test intent", types.WorkspaceContext{})
	state.Plan = nil

	completed, total := state.Progress()
	if total != 0 {
		t.Errorf("expected total 0, got %d", total)
	}
	if completed != 0 {
		t.Errorf("expected completed 0, got %d", completed)
	}
}

func TestState_MarkCompleted_Idempotent(t *testing.T) {
	state := NewState("test-id", "test intent", types.WorkspaceContext{})
	state.Plan = &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1"},
			{ID: "s2"},
		},
	}

	// Call MarkCompleted twice with the same ID
	state.MarkCompleted("s1")
	state.MarkCompleted("s1")

	// Completed slice must not grow beyond 1 entry
	if len(state.Completed) != 1 {
		t.Errorf("expected 1 completed entry after double-mark, got %d", len(state.Completed))
	}

	// Progress must not overcount
	completed, total := state.Progress()
	if completed > total {
		t.Errorf("Progress() overcount: completed=%d > total=%d", completed, total)
	}
	if completed != 1 {
		t.Errorf("expected completed=1, got %d", completed)
	}
}

func TestState_ToGoalGraph(t *testing.T) {
	ctx := types.WorkspaceContext{Cwd: "/repo", GitBranch: "main"}
	state := NewState("test-id", "my intent", ctx)
	state.Goals = []types.Goal{{ID: "g1", Description: "goal 1"}}

	gg := state.ToGoalGraph()
	if gg.Intent != "my intent" {
		t.Errorf("expected intent 'my intent', got %q", gg.Intent)
	}
	if len(gg.Goals) != 1 {
		t.Errorf("expected 1 goal, got %d", len(gg.Goals))
	}
}
