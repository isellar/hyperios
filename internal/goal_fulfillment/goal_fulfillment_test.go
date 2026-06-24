package goal_fulfillment

import (
	"context"
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
