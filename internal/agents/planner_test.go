package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/isellar/hyperios/internal/types"
)

func TestPlannerAgent_ValidJSON(t *testing.T) {
	resp := `{
		"executor": "local",
		"steps": [
			{
				"id": "s1",
				"description": "check nginx",
				"capability": {"type": "execute:shell", "scope": "dpkg"},
				"command": ["dpkg", "-l", "nginx"],
				"reversible": true,
				"depends_on": [],
				"on_failure": "skip"
			},
			{
				"id": "s2",
				"description": "install nginx",
				"capability": {"type": "execute:package", "scope": "apt:nginx"},
				"command": ["sudo", "apt-get", "-y", "install", "nginx"],
				"reversible": false,
				"depends_on": ["s1"],
				"on_failure": "halt"
			}
		]
	}`
	client := &mockCompleter{response: resp}
	agent := NewPlannerAgent(client)

	graph := &types.GoalGraph{Intent: "install nginx", Goals: []types.Goal{{ID: "g1", Description: "install", DependsOn: []string{}}}}
	plan, err := agent.Run(context.Background(), graph)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Steps) != 2 {
		t.Fatalf("Steps count = %d, want 2", len(plan.Steps))
	}
	if plan.Steps[0].Command[0] != "dpkg" {
		t.Errorf("Step[0].Command[0] = %q, want %q", plan.Steps[0].Command[0], "dpkg")
	}
	if plan.Steps[1].OnFailure != "halt" {
		t.Errorf("Step[1].OnFailure = %q, want %q", plan.Steps[1].OnFailure, "halt")
	}
}

func TestPlannerAgent_StepMissingCommand(t *testing.T) {
	resp := `{
		"executor": "local",
		"steps": [
			{
				"id": "s1",
				"description": "broken step",
				"capability": {"type": "execute:shell", "scope": "dpkg"},
				"reversible": true,
				"depends_on": [],
				"on_failure": "skip"
			}
		]
	}`
	client := &mockCompleter{response: resp}
	agent := NewPlannerAgent(client)

	graph := &types.GoalGraph{Intent: "test", Goals: []types.Goal{{ID: "g1", Description: "test", DependsOn: []string{}}}}
	_, err := agent.Run(context.Background(), graph)
	if err == nil {
		t.Fatal("expected error for missing command field, got nil")
	}
}

func TestPlannerAgent_StepMissingOnFailure(t *testing.T) {
	resp := `{
		"executor": "local",
		"steps": [
			{
				"id": "s1",
				"description": "another broken step",
				"capability": {"type": "execute:shell", "scope": "dpkg"},
				"command": ["dpkg", "-l", "nginx"],
				"reversible": true,
				"depends_on": []
			}
		]
	}`
	client := &mockCompleter{response: resp}
	agent := NewPlannerAgent(client)

	graph := &types.GoalGraph{Intent: "test", Goals: []types.Goal{{ID: "g1", Description: "test", DependsOn: []string{}}}}
	_, err := agent.Run(context.Background(), graph)
	if err == nil {
		t.Fatal("expected error for missing on_failure field, got nil")
	}
}

func TestPlannerAgent_MarkdownAroundJSON(t *testing.T) {
	resp := "```json\n{\"executor\": \"local\", \"steps\": [{\"id\": \"s1\", \"description\": \"x\", \"capability\": {\"type\": \"execute:shell\", \"scope\": \"x\"}, \"command\": [\"true\"], \"reversible\": true, \"depends_on\": [], \"on_failure\": \"skip\"}]}\n```"
	client := &mockCompleter{response: resp}
	agent := NewPlannerAgent(client)

	graph := &types.GoalGraph{Intent: "test", Goals: []types.Goal{{ID: "g1", Description: "test", DependsOn: []string{}}}}
	plan, err := agent.Run(context.Background(), graph)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Steps) != 1 {
		t.Fatalf("Steps count = %d, want 1", len(plan.Steps))
	}
}

func TestPlannerAgent_EmptyAfterExtraction(t *testing.T) {
	client := &mockCompleter{response: "no json here"}
	agent := NewPlannerAgent(client)

	graph := &types.GoalGraph{Intent: "test", Goals: []types.Goal{{ID: "g1", Description: "test", DependsOn: []string{}}}}
	_, err := agent.Run(context.Background(), graph)
	if err == nil {
		t.Fatal("expected error for empty response after extraction, got nil")
	}
}

func TestPlannerAgent_LLMError(t *testing.T) {
	client := &mockCompleter{err: errors.New("llm error")}
	agent := NewPlannerAgent(client)

	graph := &types.GoalGraph{Intent: "test", Goals: []types.Goal{{ID: "g1", Description: "test", DependsOn: []string{}}}}
	_, err := agent.Run(context.Background(), graph)
	if err == nil {
		t.Fatal("expected error from LLM, got nil")
	}
}