package llm

import (
	"encoding/json"
	"testing"
)

func TestToOllamaMessages_TextOnly(t *testing.T) {
	msg := UserMessage(TextPart("hello"))
	out := toOllamaMessages(msg)
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	if out[0].Role != "user" || out[0].Content != "hello" {
		t.Errorf("unexpected message: %+v", out[0])
	}
}

func TestToOllamaMessages_AssistantToolUse(t *testing.T) {
	input := json.RawMessage(`{"input":"ls"}`)
	msg := AssistantMessage(
		TextPart("let me check"),
		ContentPart{Type: "tool_use", ToolUseID: "call_0", ToolName: "shell", ToolInput: input},
	)
	out := toOllamaMessages(msg)
	if len(out) != 1 {
		t.Fatalf("expected 1 message (text+tool_use combine into one assistant turn), got %d", len(out))
	}
	if out[0].Role != "assistant" {
		t.Errorf("expected assistant role, got %q", out[0].Role)
	}
	if out[0].Content != "let me check" {
		t.Errorf("expected combined text 'let me check', got %q", out[0].Content)
	}
	if len(out[0].ToolCalls) != 1 || out[0].ToolCalls[0].Function.Name != "shell" {
		t.Errorf("expected one shell tool call, got %+v", out[0].ToolCalls)
	}
}

func TestToOllamaMessages_MultipleToolResults(t *testing.T) {
	// Simulates the agent's batched tool-execution turn: a single Message
	// carrying multiple tool_result parts must become multiple separate
	// role:"tool" messages, since Ollama has no concept of a multi-result
	// content block.
	msg := UserMessage(
		ToolResultPart("call_0", "output-0", false),
		ToolResultPart("call_1", "output-1 error", true),
	)
	out := toOllamaMessages(msg)
	if len(out) != 2 {
		t.Fatalf("expected 2 separate tool messages, got %d: %+v", len(out), out)
	}
	if out[0].Role != "tool" || out[0].Content != "output-0" {
		t.Errorf("unexpected first tool message: %+v", out[0])
	}
	if out[1].Role != "tool" || out[1].Content != "output-1 error" {
		t.Errorf("unexpected second tool message: %+v", out[1])
	}
}

func TestToOllamaMessages_Empty(t *testing.T) {
	msg := UserMessage()
	out := toOllamaMessages(msg)
	if len(out) != 1 {
		t.Fatalf("expected 1 placeholder message for empty content, got %d", len(out))
	}
	if out[0].Content != "" {
		t.Errorf("expected empty content, got %q", out[0].Content)
	}
}
