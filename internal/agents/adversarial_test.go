package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/isellar/hyperios/internal/types"
)

func TestAdversarialAgent_ValidJSON(t *testing.T) {
	resp := `{
		"flags": [
			{
				"step_id": "s1",
				"severity": "high",
				"description": "installs package",
				"counterfactual": "could break dependencies"
			}
		],
		"summary": "one risky step"
	}`
	client := &mockCompleter{response: resp}
	agent := NewAdversarialAgent(client)

	graph := &types.GoalGraph{Intent: "install stuff", Goals: []types.Goal{{ID: "g1", Description: "install", DependsOn: []string{}}}}
	plan := &types.ActionPlan{Steps: []types.ActionStep{{ID: "s1", Description: "install"}}}
	report, err := agent.Run(context.Background(), graph, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Flags) != 1 {
		t.Fatalf("Flags count = %d, want 1", len(report.Flags))
	}
	if report.Flags[0].Severity != "high" {
		t.Errorf("Flags[0].Severity = %q, want %q", report.Flags[0].Severity, "high")
	}
	if report.Flags[0].StepID != "s1" {
		t.Errorf("Flags[0].StepID = %q, want %q", report.Flags[0].StepID, "s1")
	}
	if report.Summary != "one risky step" {
		t.Errorf("Summary = %q, want %q", report.Summary, "one risky step")
	}
}

func TestAdversarialAgent_CleanPlan(t *testing.T) {
	resp := `{"flags": [], "summary": "all clear"}`
	client := &mockCompleter{response: resp}
	agent := NewAdversarialAgent(client)

	graph := &types.GoalGraph{Intent: "read only", Goals: []types.Goal{{ID: "g1", Description: "read", DependsOn: []string{}}}}
	plan := &types.ActionPlan{Steps: []types.ActionStep{{ID: "s1", Description: "ls"}}}
	report, err := agent.Run(context.Background(), graph, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Flags) != 0 {
		t.Errorf("Flags count = %d, want 0", len(report.Flags))
	}
}

func TestAdversarialAgent_MalformedJSON(t *testing.T) {
	resp := `{"flags": [`
	client := &mockCompleter{response: resp}
	agent := NewAdversarialAgent(client)

	graph := &types.GoalGraph{Intent: "test", Goals: []types.Goal{{ID: "g1", Description: "test", DependsOn: []string{}}}}
	plan := &types.ActionPlan{Steps: []types.ActionStep{{ID: "s1", Description: "x"}}}
	_, err := agent.Run(context.Background(), graph, plan)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestAdversarialAgent_LLMError(t *testing.T) {
	client := &mockCompleter{err: errors.New("llm error")}
	agent := NewAdversarialAgent(client)

	graph := &types.GoalGraph{Intent: "test", Goals: []types.Goal{{ID: "g1", Description: "test", DependsOn: []string{}}}}
	plan := &types.ActionPlan{Steps: []types.ActionStep{{ID: "s1", Description: "x"}}}
	_, err := agent.Run(context.Background(), graph, plan)
	if err == nil {
		t.Fatal("expected error from LLM, got nil")
	}
}