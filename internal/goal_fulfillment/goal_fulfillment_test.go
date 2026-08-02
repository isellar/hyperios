package goal_fulfillment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/module"
	"github.com/isellar/hyperios/internal/types"
)

func TestGoalFulfillment_SubmitGoal(t *testing.T) {
	client := &mockCompleter{response: `{}`}
	gf, err := New(client, nil, nil, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	goal, err := gf.SubmitGoal("install nginx")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}

	if goal.Description != "install nginx" {
		t.Errorf("expected description %q, got %q", "install nginx", goal.Description)
	}
	if goal.State != types.GoalStateRefining {
		t.Errorf("expected state %q, got %q", types.GoalStateRefining, goal.State)
	}
	if goal.ID == "" {
		t.Error("expected non-empty goal ID")
	}
}

func TestGoalFulfillment_GetGoal(t *testing.T) {
	client := &mockCompleter{response: `{}`}
	gf, err := New(client, nil, nil, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	submitted, err := gf.SubmitGoal("test goal")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}

	got, err := gf.GetGoal(submitted.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}

	if got.ID != submitted.ID {
		t.Errorf("expected ID %q, got %q", submitted.ID, got.ID)
	}
}

func TestGoalFulfillment_ListGoals(t *testing.T) {
	client := &mockCompleter{response: `{}`}
	gf, err := New(client, nil, nil, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := gf.SubmitGoal("goal 1"); err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}
	if _, err := gf.SubmitGoal("goal 2"); err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}

	all, err := gf.ListGoals("")
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 goals, got %d", len(all))
	}

	refining, err := gf.ListGoals(types.GoalStateRefining)
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	if len(refining) != 2 {
		t.Errorf("expected 2 refining goals, got %d", len(refining))
	}
}

func TestGoalFulfillment_UpdateGoalState(t *testing.T) {
	client := &mockCompleter{response: `{}`}
	gf, err := New(client, nil, nil, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	goal, err := gf.SubmitGoal("test")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}

	if err := gf.UpdateGoalState(goal.ID, types.GoalStateActive); err != nil {
		t.Fatalf("UpdateGoalState: %v", err)
	}

	got, _ := gf.GetGoal(goal.ID)
	if got.State != types.GoalStateActive {
		t.Errorf("expected state %q, got %q", types.GoalStateActive, got.State)
	}
}

func TestGoalFulfillment_ModuleInterface(t *testing.T) {
	client := &mockCompleter{response: `{}`}
	gf, err := New(client, nil, nil, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if gf.Name() != "goal_fulfillment" {
		t.Errorf("expected name %q, got %q", "goal_fulfillment", gf.Name())
	}

	health := gf.Health()
	if health.Status != "healthy" {
		t.Errorf("expected health status %q, got %q", "healthy", health.Status)
	}

	caps := gf.Capabilities()
	if len(caps) == 0 {
		t.Error("expected non-empty capabilities")
	}

	if err := gf.Tune(context.Background(), module.TuningChange{}); err != nil {
		t.Fatalf("Tune: %v", err)
	}
}

// TestGoalFulfillment_RefineGoal_ClarificationPersisted is a regression test
// for a real bug: when refinement needs clarification, GoalFulfillment.RefineGoal
// used to discard the goal entirely (return nil on any refiner error), so the
// clarification question was never persisted — the goal was just stuck in
// "refining" state forever with no visible reason why. It must now return the
// goal (with ClarificationQuestion/NeedsAttention set) alongside the error,
// AND persist it so GetGoal can retrieve the question later.
func TestGoalFulfillment_RefineGoal_ClarificationPersisted(t *testing.T) {
	resp := `{
		"intent": "do a thing",
		"context": "",
		"goals": [],
		"clarification_needed": true,
		"clarification_question": "Which thing specifically?"
	}`
	client := &mockCompleter{response: resp}
	gf, err := New(client, nil, nil, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	submitted, err := gf.SubmitGoal("do a thing")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}

	refined, refineErr := gf.RefineGoal(context.Background(), submitted)
	if refineErr == nil {
		t.Fatal("expected ClarificationNeededError")
	}
	var clarErr *ClarificationNeededError
	if !errors.As(refineErr, &clarErr) {
		t.Fatalf("expected ClarificationNeededError, got %T: %v", refineErr, refineErr)
	}

	// The goal returned alongside the error must carry the question.
	if refined == nil {
		t.Fatal("expected a non-nil goal alongside ClarificationNeededError")
	}
	if refined.ClarificationQuestion != "Which thing specifically?" {
		t.Errorf("expected ClarificationQuestion set on returned goal, got %q", refined.ClarificationQuestion)
	}
	if !refined.NeedsAttention {
		t.Error("expected NeedsAttention=true on returned goal")
	}

	// And it must be retrievable later via GetGoal — this is the actual bug:
	// previously the goal was never tracked, so GetGoal would fail or return
	// stale data with no question attached.
	stored, err := gf.GetGoal(submitted.ID)
	if err != nil {
		t.Fatalf("GetGoal after clarification: %v", err)
	}
	if stored.ClarificationQuestion != "Which thing specifically?" {
		t.Errorf("expected persisted goal to carry the clarification question, got %q", stored.ClarificationQuestion)
	}
	if !stored.NeedsAttention {
		t.Error("expected persisted goal to have NeedsAttention=true")
	}
	if stored.State != types.GoalStateRefining {
		t.Errorf("expected state %q while awaiting clarification, got %q", types.GoalStateRefining, stored.State)
	}
}

// TestGoalFulfillment_AnswerGoal verifies that answering a pending
// clarification folds the answer into the goal description and clears the
// needs-attention state.
func TestGoalFulfillment_AnswerGoal(t *testing.T) {
	clarifyResp := `{
		"intent": "notify me",
		"context": "",
		"goals": [],
		"clarification_needed": true,
		"clarification_question": "How should notifications appear?"
	}`
	client := &mockCompleter{response: clarifyResp}
	gf, err := New(client, nil, nil, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	submitted, err := gf.SubmitGoal("set up notifications")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}
	if _, err := gf.RefineGoal(context.Background(), submitted); err == nil {
		t.Fatal("expected clarification error on first refine")
	}

	answered, err := gf.AnswerGoal(submitted.ID, "Desktop notifications via notify-send")
	if err != nil {
		t.Fatalf("AnswerGoal: %v", err)
	}
	if answered.ClarificationQuestion != "" {
		t.Errorf("expected ClarificationQuestion cleared after answer, got %q", answered.ClarificationQuestion)
	}
	if answered.NeedsAttention {
		t.Error("expected NeedsAttention=false after answer")
	}
	if !strings.Contains(answered.Description, "Desktop notifications via notify-send") {
		t.Errorf("expected answer folded into description, got %q", answered.Description)
	}
}

// TestGoalFulfillment_AnswerGoal_NoQuestion verifies that answering a goal
// with no pending clarification question returns an error rather than
// silently mutating an unrelated goal's description.
func TestGoalFulfillment_AnswerGoal_NoQuestion(t *testing.T) {
	client := &mockCompleter{response: `{}`}
	gf, err := New(client, nil, nil, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	submitted, err := gf.SubmitGoal("a normal goal")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}

	if _, err := gf.AnswerGoal(submitted.ID, "some answer"); err == nil {
		t.Fatal("expected error answering a goal with no pending clarification question")
	}
}

// TestGoalFulfillment_AnswerGoal_UnknownID verifies AnswerGoal errors cleanly
// for a nonexistent goal ID rather than panicking.
func TestGoalFulfillment_AnswerGoal_UnknownID(t *testing.T) {
	client := &mockCompleter{response: `{}`}
	gf, err := New(client, nil, nil, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := gf.AnswerGoal("nonexistent-id", "some answer"); err == nil {
		t.Fatal("expected error for unknown goal ID")
	}
}

func TestGoalFulfillment_Report(t *testing.T) {
	client := &mockCompleter{response: `{}`}
	gf, err := New(client, nil, nil, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := gf.SubmitGoal("goal 1"); err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}

	report, err := gf.Report(context.Background(), 1*time.Hour)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	if report.ModuleName != "goal_fulfillment" {
		t.Errorf("expected module name %q, got %q", "goal_fulfillment", report.ModuleName)
	}

	total, ok := report.Metrics["total_goals"]
	if !ok {
		t.Fatal("expected total_goals metric")
	}
	if total.(int) != 1 {
		t.Errorf("expected 1 total goal, got %v", total)
	}
}
