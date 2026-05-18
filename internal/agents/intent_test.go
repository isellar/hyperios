package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/isellar/hyperios/internal/types"
)

func TestIntentAgent_ValidJSON(t *testing.T) {
	resp := `{
		"intent": "install nginx",
		"context": "fresh ubuntu install",
		"goals": [
			{"id": "g1", "description": "install nginx package", "depends_on": []}
		]
	}`
	client := &mockCompleter{response: resp}
	agent := NewIntentAgent(client)

	graph, err := agent.Run(context.Background(), "install nginx", types.WorkspaceContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if graph.Intent != "install nginx" {
		t.Errorf("Intent = %q, want %q", graph.Intent, "install nginx")
	}
	if len(graph.Goals) != 1 {
		t.Fatalf("Goals count = %d, want 1", len(graph.Goals))
	}
	if graph.Goals[0].ID != "g1" {
		t.Errorf("Goal[0].ID = %q, want %q", graph.Goals[0].ID, "g1")
	}
	if graph.Goals[0].Description != "install nginx package" {
		t.Errorf("Goal[0].Description = %q, want %q", graph.Goals[0].Description, "install nginx package")
	}
}

func TestIntentAgent_MarkdownFence(t *testing.T) {
	resp := "```json\n{\"intent\": \"test\", \"context\": \"\", \"goals\": []}\n```"
	client := &mockCompleter{response: resp}
	agent := NewIntentAgent(client)

	graph, err := agent.Run(context.Background(), "test", types.WorkspaceContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if graph.Intent != "test" {
		t.Errorf("Intent = %q, want %q", graph.Intent, "test")
	}
}

func TestIntentAgent_ProseAroundJSON(t *testing.T) {
	resp := "Here is the plan:\n{\"intent\": \"prose test\", \"context\": \"\", \"goals\": []}\nLet me know if you need anything."
	client := &mockCompleter{response: resp}
	agent := NewIntentAgent(client)

	graph, err := agent.Run(context.Background(), "prose test", types.WorkspaceContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if graph.Intent != "prose test" {
		t.Errorf("Intent = %q, want %q", graph.Intent, "prose test")
	}
}

func TestIntentAgent_MalformedJSON(t *testing.T) {
	resp := `{"intent": "broken`
	client := &mockCompleter{response: resp}
	agent := NewIntentAgent(client)

	_, err := agent.Run(context.Background(), "broken", types.WorkspaceContext{})
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestIntentAgent_EmptyResponse(t *testing.T) {
	client := &mockCompleter{response: ""}
	agent := NewIntentAgent(client)

	_, err := agent.Run(context.Background(), "empty", types.WorkspaceContext{})
	if err == nil {
		t.Fatal("expected error for empty response, got nil")
	}
}

func TestIntentAgent_LLMError(t *testing.T) {
	client := &mockCompleter{err: errors.New("llm error")}
	agent := NewIntentAgent(client)

	_, err := agent.Run(context.Background(), "fail", types.WorkspaceContext{})
	if err == nil {
		t.Fatal("expected error from LLM, got nil")
	}
}