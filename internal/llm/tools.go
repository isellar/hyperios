package llm

import (
	"context"
	"encoding/json"
	"fmt"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// Role identifies the speaker of a Message in a tool-use conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ContentPart is a single block within a Message. Exactly one of the
// type-specific fields is meaningful, selected by Type:
//   - "text"        — Text
//   - "tool_use"     — ToolUseID, ToolName, ToolInput
//   - "tool_result"  — ToolUseID, Text (result content), IsError
type ContentPart struct {
	Type      string
	Text      string
	ToolUseID string
	ToolName  string
	ToolInput json.RawMessage
	IsError   bool
}

// TextPart returns a plain-text ContentPart.
func TextPart(text string) ContentPart {
	return ContentPart{Type: "text", Text: text}
}

// ToolResultPart returns a tool_result ContentPart responding to toolUseID.
func ToolResultPart(toolUseID, content string, isError bool) ContentPart {
	return ContentPart{Type: "tool_result", ToolUseID: toolUseID, Text: content, IsError: isError}
}

// Message is one turn in a tool-use conversation.
type Message struct {
	Role    Role
	Content []ContentPart
}

// UserMessage builds a user-role Message from content parts.
func UserMessage(parts ...ContentPart) Message {
	return Message{Role: RoleUser, Content: parts}
}

// AssistantMessage builds an assistant-role Message from content parts.
func AssistantMessage(parts ...ContentPart) Message {
	return Message{Role: RoleAssistant, Content: parts}
}

// ToolDef describes a single callable tool exposed to the model.
// InputSchema follows the JSON Schema "properties"/"required" shape expected
// by the Anthropic tool-use API.
type ToolDef struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
}

// ToolCall is a single tool invocation requested by the model.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResponse is the result of one CompleteWithTools round-trip.
type ToolResponse struct {
	// Text is the concatenation of all text blocks in the model's response.
	Text string
	// ToolCalls holds any tool_use blocks the model produced. Empty when the
	// model finished its turn (StopReason == "end_turn").
	ToolCalls []ToolCall
	// StopReason is the raw stop reason from the API ("end_turn", "tool_use",
	// "max_tokens", etc).
	StopReason string
	// Assistant is the assistant-role Message representing this response,
	// ready to be appended to the conversation history for the next round.
	Assistant Message
}

// ToolCompleter is implemented by LLM clients that support tool-use
// (function calling). *Client implements this interface.
type ToolCompleter interface {
	CompleteWithTools(ctx context.Context, system string, messages []Message, tools []ToolDef) (*ToolResponse, error)
}

var _ ToolCompleter = (*Client)(nil)

// CompleteWithTools sends a system prompt, conversation history, and tool
// definitions to the model and returns the structured response. Unlike
// Complete/CompleteWithRetry, this method does not currently apply the
// stage-level retry policy — callers running a tool-use loop are expected to
// handle their own iteration/backoff logic.
func (c *Client) CompleteWithTools(ctx context.Context, system string, messages []Message, tools []ToolDef) (*ToolResponse, error) {
	apiMessages := make([]anthropic.MessageParam, 0, len(messages))
	for _, m := range messages {
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.Content))
		for _, part := range m.Content {
			switch part.Type {
			case "text":
				blocks = append(blocks, anthropic.NewTextBlock(part.Text))
			case "tool_use":
				var input any
				if len(part.ToolInput) > 0 {
					_ = json.Unmarshal(part.ToolInput, &input)
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(part.ToolUseID, input, part.ToolName))
			case "tool_result":
				blocks = append(blocks, anthropic.NewToolResultBlock(part.ToolUseID, part.Text, part.IsError))
			}
		}
		switch m.Role {
		case RoleAssistant:
			apiMessages = append(apiMessages, anthropic.NewAssistantMessage(blocks...))
		default:
			apiMessages = append(apiMessages, anthropic.NewUserMessage(blocks...))
		}
	}

	apiTools := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		props := t.Properties
		if props == nil {
			props = map[string]any{}
		}
		apiTools = append(apiTools, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: props,
					Required:   t.Required,
				},
			},
		})
	}

	msg, err := c.inner.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.resolveModel()),
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: system},
		},
		Messages: apiMessages,
		Tools:    apiTools,
	})
	if err != nil {
		return nil, classifyError(err)
	}

	c.trackUsage(msg.Usage.InputTokens, msg.Usage.OutputTokens)

	resp := &ToolResponse{StopReason: string(msg.StopReason)}
	var assistantParts []ContentPart

	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			resp.Text += block.Text
			assistantParts = append(assistantParts, TextPart(block.Text))
		case "tool_use":
			tu := block.AsToolUse()
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:    tu.ID,
				Name:  tu.Name,
				Input: tu.Input,
			})
			assistantParts = append(assistantParts, ContentPart{
				Type:      "tool_use",
				ToolUseID: tu.ID,
				ToolName:  tu.Name,
				ToolInput: tu.Input,
			})
		}
	}
	resp.Assistant = AssistantMessage(assistantParts...)

	if msg.StopReason == anthropic.StopReasonMaxTokens && len(resp.ToolCalls) == 0 {
		return nil, &MalformedResponseError{Raw: "response truncated at max_tokens"}
	}

	return resp, nil
}

// SimpleToolInput extracts a single string field (conventionally "input")
// from a tool call's raw JSON input. Used by callers whose tools accept a
// single free-form string argument.
func SimpleToolInput(raw json.RawMessage, field string) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("llm: parse tool input: %w", err)
	}
	v, ok := m[field]
	if !ok {
		return "", nil
	}
	s, _ := v.(string)
	return s, nil
}
