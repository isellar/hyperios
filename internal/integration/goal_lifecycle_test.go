package integration_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/isellar/hyperios/internal/goal_fulfillment"
	"github.com/isellar/hyperios/internal/types"
)

// newGoalFulfillment wires up a GoalFulfillment using test helpers.
func newGoalFulfillment(t *testing.T) *goal_fulfillment.GoalFulfillment {
	t.Helper()
	cfg := newTestConfig(t)
	dataDir := filepath.Dir(cfg.GoalStoragePath)

	mem := &mockMemoryAdapter{}
	proc := &mockProcessorLookup{}
	llmClient := newMockLLMClient()

	gf, err := goal_fulfillment.New(llmClient, mem, proc, dataDir)
	if err != nil {
		t.Fatalf("newGoalFulfillment: %v", err)
	}
	return gf
}

// mockMemoryAdapter satisfies goal_fulfillment.MemoryProvider.
type mockMemoryAdapter struct {
	store map[string]string
}

func (m *mockMemoryAdapter) GetContext(key string) (string, error) {
	if m.store == nil {
		return "", nil
	}
	return m.store[key], nil
}

func (m *mockMemoryAdapter) StoreContext(key, value string) error {
	if m.store == nil {
		m.store = make(map[string]string)
	}
	m.store[key] = value
	return nil
}

// mockProcessorLookup satisfies goal_fulfillment.ProcessorProvider.
type mockProcessorLookup struct{}

func (m *mockProcessorLookup) Lookup(query string) (string, error) {
	return "", nil
}

// ---------------------------------------------------------------------------
// TestGoalSubmitAndTrack
// ---------------------------------------------------------------------------

// TestGoalSubmitAndTrack verifies that submitting a goal persists it with
// state Refining and makes it retrievable by ID.
func TestGoalSubmitAndTrack(t *testing.T) {
	gf := newGoalFulfillment(t)

	goal, err := gf.SubmitGoal("install and configure nginx")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}

	if goal.ID == "" {
		t.Fatal("expected non-empty goal ID")
	}
	if goal.State != types.GoalStateRefining {
		t.Errorf("expected state %q, got %q", types.GoalStateRefining, goal.State)
	}

	// Verify it is retrievable.
	retrieved, err := gf.GetGoal(goal.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if retrieved.ID != goal.ID {
		t.Errorf("retrieved goal ID %q != submitted %q", retrieved.ID, goal.ID)
	}
	if retrieved.State != types.GoalStateRefining {
		t.Errorf("retrieved goal state = %q, want %q", retrieved.State, types.GoalStateRefining)
	}
	if retrieved.Description != "install and configure nginx" {
		t.Errorf("retrieved description = %q", retrieved.Description)
	}
}

// ---------------------------------------------------------------------------
// TestGoalStateTransitions
// ---------------------------------------------------------------------------

// TestGoalStateTransitions verifies the full happy-path lifecycle:
// Refining → Active → Done.
func TestGoalStateTransitions(t *testing.T) {
	gf := newGoalFulfillment(t)

	// Submit (Refining).
	goal, err := gf.SubmitGoal("set up a cron job for backups")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}
	if goal.State != types.GoalStateRefining {
		t.Fatalf("after submit: expected %q, got %q", types.GoalStateRefining, goal.State)
	}

	// Refining → Active.
	if err := gf.UpdateGoalState(goal.ID, types.GoalStateActive); err != nil {
		t.Fatalf("UpdateGoalState(Active): %v", err)
	}
	retrieved, err := gf.GetGoal(goal.ID)
	if err != nil {
		t.Fatalf("GetGoal after Active: %v", err)
	}
	if retrieved.State != types.GoalStateActive {
		t.Errorf("after Active update: got %q, want %q", retrieved.State, types.GoalStateActive)
	}

	// Active → Done.
	if err := gf.UpdateGoalState(goal.ID, types.GoalStateDone); err != nil {
		t.Fatalf("UpdateGoalState(Done): %v", err)
	}
	retrieved, err = gf.GetGoal(goal.ID)
	if err != nil {
		t.Fatalf("GetGoal after Done: %v", err)
	}
	if retrieved.State != types.GoalStateDone {
		t.Errorf("after Done update: got %q, want %q", retrieved.State, types.GoalStateDone)
	}
}

// ---------------------------------------------------------------------------
// TestGoalBreakdown
// ---------------------------------------------------------------------------

// TestGoalBreakdown submits a goal, calls BreakdownGoal, and verifies that
// sub-goals are created and tracked.
func TestGoalBreakdown(t *testing.T) {
	gf := newGoalFulfillment(t)

	goal, err := gf.SubmitGoal("deploy a web application")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}

	subGoals, err := gf.BreakdownGoal(context.Background(), goal)
	if err != nil {
		t.Fatalf("BreakdownGoal: %v", err)
	}

	if len(subGoals) == 0 {
		t.Fatal("expected at least one sub-goal from breakdown")
	}

	// Verify sub-goals are persisted.
	for _, sg := range subGoals {
		retrieved, err := gf.GetGoal(sg.ID)
		if err != nil {
			t.Errorf("GetGoal(%q): %v", sg.ID, err)
			continue
		}
		if retrieved.Description == "" {
			t.Errorf("sub-goal %q has empty description", sg.ID)
		}
	}

	// The parent goal should reflect sub-goal IDs.
	parent, err := gf.GetGoal(goal.ID)
	if err != nil {
		t.Fatalf("GetGoal(parent): %v", err)
	}
	if len(parent.SubGoals) == 0 {
		t.Error("parent goal should have SubGoals populated after breakdown")
	}
}

// ---------------------------------------------------------------------------
// TestGoalBlockedAndCancelled
// ---------------------------------------------------------------------------

// TestGoalBlockedAndCancelled verifies that a goal can transition to Blocked
// and Cancelled states.
func TestGoalBlockedAndCancelled(t *testing.T) {
	t.Run("Blocked", func(t *testing.T) {
		gf := newGoalFulfillment(t)

		goal, err := gf.SubmitGoal("upgrade the kernel")
		if err != nil {
			t.Fatalf("SubmitGoal: %v", err)
		}

		// Refining → Active → Blocked
		if err := gf.UpdateGoalState(goal.ID, types.GoalStateActive); err != nil {
			t.Fatalf("UpdateGoalState(Active): %v", err)
		}
		if err := gf.UpdateGoalState(goal.ID, types.GoalStateBlocked); err != nil {
			t.Fatalf("UpdateGoalState(Blocked): %v", err)
		}

		retrieved, err := gf.GetGoal(goal.ID)
		if err != nil {
			t.Fatalf("GetGoal: %v", err)
		}
		if retrieved.State != types.GoalStateBlocked {
			t.Errorf("expected Blocked, got %q", retrieved.State)
		}
	})

	t.Run("Cancelled_from_Refining", func(t *testing.T) {
		gf := newGoalFulfillment(t)

		goal, err := gf.SubmitGoal("delete all log files")
		if err != nil {
			t.Fatalf("SubmitGoal: %v", err)
		}

		// Refining → Cancelled
		if err := gf.UpdateGoalState(goal.ID, types.GoalStateCancelled); err != nil {
			t.Fatalf("UpdateGoalState(Cancelled): %v", err)
		}

		retrieved, err := gf.GetGoal(goal.ID)
		if err != nil {
			t.Fatalf("GetGoal: %v", err)
		}
		if retrieved.State != types.GoalStateCancelled {
			t.Errorf("expected Cancelled, got %q", retrieved.State)
		}
	})

	t.Run("Cancelled_from_Active", func(t *testing.T) {
		gf := newGoalFulfillment(t)

		goal, err := gf.SubmitGoal("send an email notification")
		if err != nil {
			t.Fatalf("SubmitGoal: %v", err)
		}

		// Refining → Active → Cancelled
		if err := gf.UpdateGoalState(goal.ID, types.GoalStateActive); err != nil {
			t.Fatalf("UpdateGoalState(Active): %v", err)
		}
		if err := gf.UpdateGoalState(goal.ID, types.GoalStateCancelled); err != nil {
			t.Fatalf("UpdateGoalState(Cancelled): %v", err)
		}

		retrieved, err := gf.GetGoal(goal.ID)
		if err != nil {
			t.Fatalf("GetGoal: %v", err)
		}
		if retrieved.State != types.GoalStateCancelled {
			t.Errorf("expected Cancelled, got %q", retrieved.State)
		}
	})

	t.Run("InvalidTransition_Done_to_Active", func(t *testing.T) {
		gf := newGoalFulfillment(t)

		goal, err := gf.SubmitGoal("restart the service")
		if err != nil {
			t.Fatalf("SubmitGoal: %v", err)
		}

		// Bring to Done.
		if err := gf.UpdateGoalState(goal.ID, types.GoalStateActive); err != nil {
			t.Fatalf("UpdateGoalState(Active): %v", err)
		}
		if err := gf.UpdateGoalState(goal.ID, types.GoalStateDone); err != nil {
			t.Fatalf("UpdateGoalState(Done): %v", err)
		}

		// Done → Active is not a valid transition.
		err = gf.UpdateGoalState(goal.ID, types.GoalStateActive)
		if err == nil {
			t.Error("expected error for invalid transition Done → Active")
		}
	})
}
