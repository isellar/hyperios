package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultOllamaBaseURL is the default local Ollama daemon address.
const DefaultOllamaBaseURL = "http://localhost:11434"

// DefaultOllamaTimeout is the HTTP client timeout applied when no explicit
// timeout is configured. It is deliberately generous: a long tool-use loop on
// a modest local GPU (10+ tool-call round-trips, each involving a real shell
// command) can legitimately take several minutes, and a client-side timeout
// firing mid-call is indistinguishable from a hang to the caller.
const DefaultOllamaTimeout = 20 * time.Minute

// DefaultKeepAlive controls how long Ollama keeps the model loaded in
// VRAM/RAM after the last request, to avoid expensive reload latency between
// consecutive tool-call round-trips in the same goal (and between
// back-to-back goals). "30m" is longer than Ollama's own default ("5m") since
// HyperiOS goals are bursty (several calls in quick succession, then idle)
// rather than steadily interactive.
const DefaultKeepAlive = "30m"

// OllamaClient talks to a local Ollama daemon's /api/chat endpoint. It
// implements both Completer and ToolCompleter, so it is a drop-in
// replacement for *Client (Anthropic) anywhere a goal is processed.
//
// Ollama is free — there's no per-token billing — so OllamaClient does not
// track cost the way *Client does.
type OllamaClient struct {
	baseURL   string
	model     string
	numCtx    int    // 0 = let Ollama pick its own default (not recommended; see RecommendNumCtx)
	keepAlive string // e.g. "30m"; empty = Ollama's own default (5m)
	http      *http.Client
}

// NewOllamaClient returns a client targeting baseURL (e.g.
// "http://localhost:11434") using the given model name (e.g. "qwen2.5:7b").
// If baseURL is empty, DefaultOllamaBaseURL is used. Uses DefaultOllamaTimeout
// and DefaultKeepAlive; use NewOllamaClientWithOptions to override numCtx or
// either of those.
func NewOllamaClient(baseURL, model string) *OllamaClient {
	return NewOllamaClientWithOptions(baseURL, model, OllamaOptions{})
}

// OllamaOptions configures optional per-client tuning for NewOllamaClientWithOptions.
type OllamaOptions struct {
	// NumCtx sets the context window size (Ollama's num_ctx option) sent
	// with every request. 0 leaves it unset, which means Ollama's own
	// (version/platform-dependent, sometimes quite small) default applies —
	// see localmodel.RecommendNumCtx for computing a safe explicit value
	// instead of relying on the daemon default.
	NumCtx int
	// KeepAlive sets how long Ollama keeps the model loaded after the last
	// request (e.g. "30m", "-1" for forever, "0" to unload immediately).
	// Empty uses DefaultKeepAlive.
	KeepAlive string
	// Timeout overrides the HTTP client timeout. Zero uses DefaultOllamaTimeout.
	Timeout time.Duration
}

// NewOllamaClientWithOptions is like NewOllamaClient but allows overriding
// context window size, keep-alive duration, and HTTP timeout.
func NewOllamaClientWithOptions(baseURL, model string, opts OllamaOptions) *OllamaClient {
	if baseURL == "" {
		baseURL = DefaultOllamaBaseURL
	}
	keepAlive := opts.KeepAlive
	if keepAlive == "" {
		keepAlive = DefaultKeepAlive
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultOllamaTimeout
	}
	return &OllamaClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		model:     model,
		numCtx:    opts.NumCtx,
		keepAlive: keepAlive,
		http:      &http.Client{Timeout: timeout},
	}
}

var (
	_ Completer     = (*OllamaClient)(nil)
	_ ToolCompleter = (*OllamaClient)(nil)
)

// ── wire types (Ollama /api/chat) ─────────────────────────────────────────────

type ollamaChatMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolName  string           `json:"tool_name,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	Function ollamaToolCallFn `json:"function"`
}

type ollamaToolCallFn struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ollamaTool struct {
	Type     string         `json:"type"`
	Function ollamaFunction `json:"function"`
}

type ollamaFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  ollamaJSONSpec `json:"parameters"`
}

type ollamaJSONSpec struct {
	Type       string   `json:"type"`
	Properties any      `json:"properties,omitempty"`
	Required   []string `json:"required,omitempty"`
}

type ollamaChatRequest struct {
	Model     string              `json:"model"`
	Messages  []ollamaChatMessage `json:"messages"`
	Tools     []ollamaTool        `json:"tools,omitempty"`
	Stream    bool                `json:"stream"`
	KeepAlive string              `json:"keep_alive,omitempty"`
	Options   *ollamaOptions      `json:"options,omitempty"`
}

type ollamaOptions struct {
	NumCtx int `json:"num_ctx,omitempty"`
}

type ollamaChatResponse struct {
	Model   string            `json:"model"`
	Message ollamaChatMessage `json:"message"`
	Done    bool              `json:"done"`
	Error   string            `json:"error,omitempty"`
}

// ── Completer ──────────────────────────────────────────────────────────────────

// Complete sends a system + user prompt and returns the raw text response.
func (o *OllamaClient) Complete(ctx context.Context, system, user string) (string, error) {
	req := ollamaChatRequest{
		Model: o.model,
		Messages: []ollamaChatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream: false,
	}
	o.applyOptions(&req)

	resp, err := o.doChat(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Message.Content, nil
}

// CompleteWithRetry applies the same NetworkError/RateLimitError/
// MalformedResponseError retry policy as *Client.CompleteWithRetry. Rate
// limiting doesn't really apply to a local daemon, but a busy/loading model
// can return transient errors that are worth a short retry.
func (o *OllamaClient) CompleteWithRetry(ctx context.Context, system, user string) (string, error) {
	const maxRetries = 3
	attempts := 0
	for {
		raw, err := o.Complete(ctx, system, user)
		if err == nil {
			return raw, nil
		}
		var netErr *NetworkError
		if !errors.As(err, &netErr) {
			return "", err
		}
		attempts++
		if attempts > maxRetries {
			return "", fmt.Errorf("ollama: network error after %d retries: %w", maxRetries, err)
		}
		wait := time.Duration(attempts) * time.Second
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
	}
}

// ── ToolCompleter ──────────────────────────────────────────────────────────────

// CompleteWithTools sends a system prompt, conversation history, and tool
// definitions to the model and returns the structured response.
func (o *OllamaClient) CompleteWithTools(ctx context.Context, system string, messages []Message, tools []ToolDef) (*ToolResponse, error) {
	req := ollamaChatRequest{
		Model:  o.model,
		Stream: false,
	}
	o.applyOptions(&req)
	req.Messages = append(req.Messages, ollamaChatMessage{Role: "system", Content: system})

	for _, m := range messages {
		req.Messages = append(req.Messages, toOllamaMessages(m)...)
	}

	for _, t := range tools {
		props := t.Properties
		if props == nil {
			props = map[string]any{}
		}
		req.Tools = append(req.Tools, ollamaTool{
			Type: "function",
			Function: ollamaFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters: ollamaJSONSpec{
					Type:       "object",
					Properties: props,
					Required:   t.Required,
				},
			},
		})
	}

	resp, err := o.doChat(ctx, req)
	if err != nil {
		return nil, err
	}

	out := &ToolResponse{Text: resp.Message.Content}
	if resp.Message.Content != "" {
		out.Assistant = AssistantMessage(TextPart(resp.Message.Content))
	}

	if len(resp.Message.ToolCalls) > 0 {
		out.StopReason = "tool_use"
		var parts []ContentPart
		if resp.Message.Content != "" {
			parts = append(parts, TextPart(resp.Message.Content))
		}
		for i, tc := range resp.Message.ToolCalls {
			// Ollama does not assign IDs to tool calls the way Anthropic does;
			// synthesize a stable per-response ID so ToolResultPart round-trips
			// correctly within a single CompleteWithTools call.
			id := fmt.Sprintf("call_%d", i)
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:    id,
				Name:  tc.Function.Name,
				Input: tc.Function.Arguments,
			})
			parts = append(parts, ContentPart{
				Type:      "tool_use",
				ToolUseID: id,
				ToolName:  tc.Function.Name,
				ToolInput: tc.Function.Arguments,
			})
		}
		out.Assistant = AssistantMessage(parts...)
	} else {
		out.StopReason = "end_turn"
	}

	return out, nil
}

// toOllamaMessages converts a provider-agnostic Message into one or more
// Ollama wire messages. Ollama represents each tool result as its own
// role:"tool" message (with tool_name identifying which call it answers)
// rather than a typed "tool_result" content block embedded alongside other
// content — so a single Message carrying N tool_result parts (as produced by
// the agent's batched tool-execution turn) expands into N separate "tool"
// messages here. Any plain text and tool_use parts in the same Message are
// combined into one leading user/assistant message, in original order,
// before the tool-result messages.
func toOllamaMessages(m Message) []ollamaChatMessage {
	role := "user"
	if m.Role == RoleAssistant {
		role = "assistant"
	}

	var out []ollamaChatMessage
	var textParts []string
	var toolCalls []ollamaToolCall

	flushLead := func() {
		if len(textParts) == 0 && len(toolCalls) == 0 {
			return
		}
		out = append(out, ollamaChatMessage{
			Role:      role,
			Content:   strings.Join(textParts, "\n"),
			ToolCalls: toolCalls,
		})
		textParts = nil
		toolCalls = nil
	}

	for _, part := range m.Content {
		switch part.Type {
		case "text":
			textParts = append(textParts, part.Text)
		case "tool_use":
			toolCalls = append(toolCalls, ollamaToolCall{
				Function: ollamaToolCallFn{
					Name:      part.ToolName,
					Arguments: part.ToolInput,
				},
			})
		case "tool_result":
			flushLead()
			out = append(out, ollamaChatMessage{
				Role:     "tool",
				Content:  part.Text,
				ToolName: part.ToolName,
			})
		}
	}
	flushLead()

	if len(out) == 0 {
		// Preserve empty messages rather than dropping them silently.
		return []ollamaChatMessage{{Role: role}}
	}
	return out
}

// applyOptions sets keep_alive and options.num_ctx on req from the client's
// configuration, so every request path (Complete and CompleteWithTools) gets
// the same explicit context-window/keep-alive behavior rather than trusting
// Ollama's own (often too-small) defaults.
func (o *OllamaClient) applyOptions(req *ollamaChatRequest) {
	req.KeepAlive = o.keepAlive
	if o.numCtx > 0 {
		req.Options = &ollamaOptions{NumCtx: o.numCtx}
	}
}

// doChat performs a single non-streaming /api/chat request.
func (o *OllamaClient) doChat(ctx context.Context, req ollamaChatRequest) (*ollamaChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.http.Do(httpReq)
	if err != nil {
		return nil, &NetworkError{Cause: fmt.Errorf("ollama: request failed (is the daemon running at %s?): %w", o.baseURL, err)}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &NetworkError{Cause: fmt.Errorf("ollama: read response: %w", err)}
	}

	if resp.StatusCode >= 500 {
		return nil, &NetworkError{Cause: fmt.Errorf("ollama: server error %d: %s", resp.StatusCode, string(data))}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: unexpected status %d: %s", resp.StatusCode, string(data))
	}

	var chatResp ollamaChatResponse
	if err := json.Unmarshal(data, &chatResp); err != nil {
		return nil, &MalformedResponseError{Raw: string(data)}
	}
	if chatResp.Error != "" {
		return nil, fmt.Errorf("ollama: %s", chatResp.Error)
	}

	return &chatResp, nil
}

// Ping checks whether the Ollama daemon is reachable at baseURL.
func Ping(ctx context.Context, baseURL string) error {
	if baseURL == "" {
		baseURL = DefaultOllamaBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/tags", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama not reachable at %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama at %s returned status %d", baseURL, resp.StatusCode)
	}
	return nil
}
