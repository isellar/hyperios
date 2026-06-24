package governor

import (
	"context"
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/governor/capability"
	"github.com/isellar/hyperios/internal/module"
	"github.com/isellar/hyperios/internal/types"
)

func TestGovernor_Name(t *testing.T) {
	g := New(GovernorConfig{})
	if g.Name() != "governor" {
		t.Errorf("expected name 'governor', got %q", g.Name())
	}
}

func TestGovernor_Health(t *testing.T) {
	g := New(GovernorConfig{AutonomyLevel: 1})
	h := g.Health()
	if h.Status != "healthy" {
		t.Errorf("expected healthy, got %q", h.Status)
	}
}

func TestGovernor_Capabilities(t *testing.T) {
	g := New(GovernorConfig{})
	caps := g.Capabilities()
	if len(caps) == 0 {
		t.Error("expected non-empty capabilities list")
	}
}

func TestGovernor_Report(t *testing.T) {
	reg := capability.NewRegistry()
	g := New(GovernorConfig{Registry: reg, AutonomyLevel: 2})
	report, err := g.Report(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.ModuleName != "governor" {
		t.Errorf("expected module name 'governor', got %q", report.ModuleName)
	}
	if report.Metrics["autonomy_level"] != 2 {
		t.Errorf("expected autonomy_level 2, got %v", report.Metrics["autonomy_level"])
	}
}

func TestGovernor_Tune(t *testing.T) {
	g := New(GovernorConfig{AutonomyLevel: 1})

	err := g.Tune(context.Background(), module.TuningChange{
		Module: "governor",
		Path:   "autonomy_level",
		Value:  3,
	})
	if err != nil {
		t.Fatalf("Tune: %v", err)
	}
	if g.Arbiter().AutonomyLevel() != 3 {
		t.Errorf("expected autonomy level 3 after tune, got %d", g.Arbiter().AutonomyLevel())
	}
}

func TestGovernor_Tune_InvalidLevel(t *testing.T) {
	g := New(GovernorConfig{AutonomyLevel: 1})

	err := g.Tune(context.Background(), module.TuningChange{
		Module: "governor",
		Path:   "autonomy_level",
		Value:  5,
	})
	if err == nil {
		t.Error("expected error for invalid autonomy level")
	}
}

func TestGovernor_Tune_UnknownPath(t *testing.T) {
	g := New(GovernorConfig{})

	err := g.Tune(context.Background(), module.TuningChange{
		Module: "governor",
		Path:   "unknown",
		Value:  nil,
	})
	if err == nil {
		t.Error("expected error for unknown tuning path")
	}
}

func TestGovernor_ReviewGoal(t *testing.T) {
	g := New(GovernorConfig{})
	goal := &types.Goal{ID: "g1", Description: "install nginx"}
	result, err := g.ReviewGoal(goal)
	if err != nil {
		t.Fatalf("ReviewGoal: %v", err)
	}
	if !result.Approved {
		t.Errorf("expected approved, got: %s", result.Reason)
	}
}

func TestGovernor_AuthorizeTool(t *testing.T) {
	g := New(GovernorConfig{})
	if err := g.AuthorizeTool("tool1", "always"); err != nil {
		t.Fatalf("AuthorizeTool: %v", err)
	}
	if !g.ToolAuth().CheckAuthorization("tool1") {
		t.Error("expected tool1 to be authorized")
	}
}

func TestGovernor_ExecuteGoal_NoExecutor(t *testing.T) {
	g := New(GovernorConfig{})
	graph := &types.GoalGraph{Intent: "test"}
	plan := &types.ActionPlan{Steps: []types.ActionStep{{ID: "s1"}}}

	_, err := g.ExecuteGoal(context.Background(), graph, plan)
	if err == nil {
		t.Error("expected error when no executor is configured")
	}
}

func TestGovernor_Accessors(t *testing.T) {
	reg := capability.NewRegistry()
	g := New(GovernorConfig{Registry: reg, AutonomyLevel: 2})

	if g.Arbiter() == nil {
		t.Error("Arbiter() returned nil")
	}
	if g.Registry() == nil {
		t.Error("Registry() returned nil")
	}
	if g.Enforcer() == nil {
		t.Error("Enforcer() returned nil")
	}
	if g.ToolAuth() == nil {
		t.Error("ToolAuth() returned nil")
	}
}

func TestGovernor_ModuleInterface(t *testing.T) {
	var m module.Module = New(GovernorConfig{})
	if m.Name() != "governor" {
		t.Errorf("Module.Name() = %q, want 'governor'", m.Name())
	}
}
