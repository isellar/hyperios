package goal_fulfillment

import (
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

func TestTracker_TrackAndGetGoal(t *testing.T) {
	tracker, err := NewTracker("")
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	goal := &types.Goal{
		ID:          "g1",
		Description: "install nginx",
		State:       types.GoalStateRefining,
	}

	if err := tracker.TrackGoal(goal); err != nil {
		t.Fatalf("TrackGoal: %v", err)
	}

	got, err := tracker.GetGoal("g1")
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}

	if got.Description != "install nginx" {
		t.Errorf("expected description %q, got %q", "install nginx", got.Description)
	}
	if got.State != types.GoalStateRefining {
		t.Errorf("expected state %q, got %q", types.GoalStateRefining, got.State)
	}
}

func TestTracker_GetGoal_NotFound(t *testing.T) {
	tracker, err := NewTracker("")
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	_, err = tracker.GetGoal("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent goal, got nil")
	}
}

func TestTracker_TrackGoal_EmptyID(t *testing.T) {
	tracker, err := NewTracker("")
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	goal := &types.Goal{Description: "no id"}
	if err := tracker.TrackGoal(goal); err == nil {
		t.Fatal("expected error for empty ID, got nil")
	}
}

func TestTracker_ListGoals(t *testing.T) {
	tracker, err := NewTracker("")
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	now := time.Now()
	goals := []*types.Goal{
		{ID: "g1", Description: "refining goal", State: types.GoalStateRefining, CreatedAt: now},
		{ID: "g2", Description: "active goal", State: types.GoalStateActive, CreatedAt: now},
		{ID: "g3", Description: "done goal", State: types.GoalStateDone, CreatedAt: now},
		{ID: "g4", Description: "another active", State: types.GoalStateActive, CreatedAt: now},
	}

	for _, g := range goals {
		if err := tracker.TrackGoal(g); err != nil {
			t.Fatalf("TrackGoal: %v", err)
		}
	}

	all, err := tracker.ListGoals("")
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("expected 4 total goals, got %d", len(all))
	}

	active, err := tracker.ListGoals(types.GoalStateActive)
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("expected 2 active goals, got %d", len(active))
	}

	done, err := tracker.ListGoals(types.GoalStateDone)
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	if len(done) != 1 {
		t.Errorf("expected 1 done goal, got %d", len(done))
	}
}

func TestTracker_UpdateGoalState(t *testing.T) {
	tracker, err := NewTracker("")
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	goal := &types.Goal{
		ID:          "g1",
		Description: "test",
		State:       types.GoalStateRefining,
		CreatedAt:   time.Now(),
	}
	if err := tracker.TrackGoal(goal); err != nil {
		t.Fatalf("TrackGoal: %v", err)
	}

	if err := tracker.UpdateGoalState("g1", types.GoalStateActive); err != nil {
		t.Fatalf("UpdateGoalState: %v", err)
	}

	got, _ := tracker.GetGoal("g1")
	if got.State != types.GoalStateActive {
		t.Errorf("expected state %q, got %q", types.GoalStateActive, got.State)
	}
}

func TestTracker_UpdateGoalState_InvalidTransition(t *testing.T) {
	tracker, err := NewTracker("")
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	goal := &types.Goal{
		ID:          "g1",
		Description: "test",
		State:       types.GoalStateDone,
		CreatedAt:   time.Now(),
	}
	if err := tracker.TrackGoal(goal); err != nil {
		t.Fatalf("TrackGoal: %v", err)
	}

	if err := tracker.UpdateGoalState("g1", types.GoalStateActive); err == nil {
		t.Fatal("expected error for invalid transition from done to active, got nil")
	}
}

func TestTracker_UpdateGoalState_NotFound(t *testing.T) {
	tracker, err := NewTracker("")
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	if err := tracker.UpdateGoalState("nonexistent", types.GoalStateActive); err == nil {
		t.Fatal("expected error for nonexistent goal, got nil")
	}
}

func TestTracker_DeleteGoal(t *testing.T) {
	tracker, err := NewTracker("")
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	goal := &types.Goal{
		ID:          "g1",
		Description: "test",
		State:       types.GoalStateActive,
		CreatedAt:   time.Now(),
	}
	if err := tracker.TrackGoal(goal); err != nil {
		t.Fatalf("TrackGoal: %v", err)
	}

	if err := tracker.DeleteGoal("g1"); err != nil {
		t.Fatalf("DeleteGoal: %v", err)
	}

	_, err = tracker.GetGoal("g1")
	if err == nil {
		t.Fatal("expected error after delete, got nil")
	}
}

func TestTracker_DeleteGoal_NotFound(t *testing.T) {
	tracker, err := NewTracker("")
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	if err := tracker.DeleteGoal("nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent goal, got nil")
	}
}

func TestTracker_PersistAndReload(t *testing.T) {
	dir := t.TempDir()

	tracker1, err := NewTracker(dir)
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	goal := &types.Goal{
		ID:          "g1",
		Description: "persistent goal",
		State:       types.GoalStateActive,
	}
	if err := tracker1.TrackGoal(goal); err != nil {
		t.Fatalf("TrackGoal: %v", err)
	}

	tracker2, err := NewTracker(dir)
	if err != nil {
		t.Fatalf("NewTracker (reload): %v", err)
	}

	got, err := tracker2.GetGoal("g1")
	if err != nil {
		t.Fatalf("GetGoal after reload: %v", err)
	}
	if got.Description != "persistent goal" {
		t.Errorf("expected description %q, got %q", "persistent goal", got.Description)
	}
	if got.State != types.GoalStateActive {
		t.Errorf("expected state %q, got %q", types.GoalStateActive, got.State)
	}
}

func TestIsValidTransition(t *testing.T) {
	tests := []struct {
		from  types.GoalState
		to    types.GoalState
		valid bool
	}{
		{types.GoalStateRefining, types.GoalStateActive, true},
		{types.GoalStateRefining, types.GoalStateCancelled, true},
		{types.GoalStateRefining, types.GoalStateDone, false},
		{types.GoalStateActive, types.GoalStateDone, true},
		{types.GoalStateActive, types.GoalStateBlocked, true},
		{types.GoalStateActive, types.GoalStateCancelled, true},
		{types.GoalStateActive, types.GoalStateRefining, true},
		{types.GoalStateBlocked, types.GoalStateActive, true},
		{types.GoalStateBlocked, types.GoalStateCancelled, true},
		{types.GoalStateBlocked, types.GoalStateDone, false},
		{types.GoalStateDone, types.GoalStateActive, false},
		{types.GoalStateCancelled, types.GoalStateActive, false},
	}

	for _, tt := range tests {
		got := isValidTransition(tt.from, tt.to)
		if got != tt.valid {
			t.Errorf("isValidTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.valid)
		}
	}
}
