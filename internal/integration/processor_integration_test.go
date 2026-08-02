package integration_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/goal_fulfillment"
	"github.com/isellar/hyperios/internal/processor"
	"github.com/isellar/hyperios/internal/types"
)

// newTestProcessor constructs a Processor with a mock LLM client and audit
// logger, ready for dependency injection in each test.
func newTestProcessor(t *testing.T) *processor.Processor {
	t.Helper()
	cfg := newTestConfig(t)
	auditLog := newMockAudit(t)
	llmClient := newMockLLMClient()
	return processor.NewProcessor(cfg, llmClient, auditLog)
}

// activeGoal creates a goal in Active state suitable for queuing.
func activeGoal(id, description string) *types.Goal {
	now := time.Now()
	return &types.Goal{
		ID:          id,
		Description: description,
		State:       types.GoalStateActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// ---------------------------------------------------------------------------
// TestProcessorQueueAndRun
// ---------------------------------------------------------------------------

// TestProcessorQueueAndRun wires Processor with a mock GoalUpdater, queues a
// goal, runs it, and verifies the result is recorded.
func TestProcessorQueueAndRun(t *testing.T) {
	proc := newTestProcessor(t)

	updater := &mockGoalUpdater{}
	proc.SetGoalFulfillment(updater)

	goal := activeGoal("g-run-1", "write a hello world program")

	if err := proc.QueueGoal(goal); err != nil {
		t.Fatalf("QueueGoal: %v", err)
	}

	result, err := proc.RunNext()
	if err != nil {
		t.Fatalf("RunNext: %v", err)
	}
	if result == nil {
		t.Fatal("expected a non-nil AgentResult")
	}

	// The mock LLM always succeeds, so the agent should succeed.
	if !result.Success {
		t.Errorf("expected Success=true, got false; error=%q", result.Error)
	}

	// GoalFulfillment should have been called to mark the goal Done.
	if len(updater.updates) == 0 {
		t.Fatal("expected UpdateGoalState to be called")
	}
	last := updater.updates[len(updater.updates)-1]
	if last.id != goal.ID {
		t.Errorf("updated goal ID = %q, want %q", last.id, goal.ID)
	}
	if last.state != types.GoalStateDone {
		t.Errorf("updated state = %q, want %q", last.state, types.GoalStateDone)
	}
}

// ---------------------------------------------------------------------------
// TestProcessorMemoryLookup
// ---------------------------------------------------------------------------

// TestProcessorMemoryLookup wires a mock memory querier and verifies that
// LookupInfo returns results from memory that match the query.
func TestProcessorMemoryLookup(t *testing.T) {
	proc := newTestProcessor(t)

	mem := newMockMemoryQuerier()
	mem.entries["nginx config"] = "listen on port 80"
	mem.entries["nginx ssl"] = "ssl certificate path"
	proc.SetMemory(mem)

	result, err := proc.LookupInfo("nginx")
	if err != nil {
		t.Fatalf("LookupInfo: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result for 'nginx' query")
	}
}

// TestProcessorMemoryLookup_EmptyQuery verifies that an empty memory module
// returns an empty string without error.
func TestProcessorMemoryLookup_NoMemory(t *testing.T) {
	proc := newTestProcessor(t)
	// No memory wired.

	result, err := proc.LookupInfo("anything")
	if err != nil {
		t.Fatalf("unexpected error with no memory: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty result with no memory, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// TestProcessorFullWiring
// ---------------------------------------------------------------------------

// TestProcessorFullWiring connects Processor <-> GoalFulfillment in a
// realistic configuration (all real implementations, mock LLM).
func TestProcessorFullWiring(t *testing.T) {
	cfg := newTestConfig(t)
	auditLog := newMockAudit(t)
	llmClient := newMockLLMClient()

	// Real GoalFulfillment.
	goalDataDir := filepath.Dir(cfg.GoalStoragePath)
	mem := &mockMemoryAdapter{}
	proc := processor.NewProcessor(cfg, llmClient, auditLog)

	gf, err := goal_fulfillment.New(llmClient, mem, proc, goalDataDir)
	if err != nil {
		t.Fatalf("goal_fulfillment.New: %v", err)
	}

	// Wire bidirectionally.
	proc.SetGoalFulfillment(gf)
	proc.SetMemory(newMockMemoryQuerier())

	// Submit and queue a goal.
	submitted, err := gf.SubmitGoal("run unit tests for the codebase")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}

	// Transition to Active so the prioritizer will pick it up.
	if err := gf.UpdateGoalState(submitted.ID, types.GoalStateActive); err != nil {
		t.Fatalf("UpdateGoalState(Active): %v", err)
	}

	retrieved, err := gf.GetGoal(submitted.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}

	if err := proc.QueueGoal(retrieved); err != nil {
		t.Fatalf("QueueGoal: %v", err)
	}

	result, err := proc.RunNext()
	if err != nil {
		t.Fatalf("RunNext: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Success {
		t.Errorf("expected Success, got failure: %s", result.Error)
	}

	// The goal should now be Done (updated by the processor via GoalFulfillment).
	final, err := gf.GetGoal(submitted.ID)
	if err != nil {
		t.Fatalf("GetGoal(final): %v", err)
	}
	if final.State != types.GoalStateDone {
		t.Errorf("expected final state Done, got %q", final.State)
	}
}
