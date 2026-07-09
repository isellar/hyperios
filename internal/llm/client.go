package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const Model = anthropic.ModelClaudeSonnet4_6

// DefaultZenModel is the OpenCode Zen model ID used when no override is set.
// See https://opencode.ai/zen/v1/models for the full catalog.
const DefaultZenModel = "claude-sonnet-5"

// ZenBaseURL is the OpenCode Zen API endpoint. Zen exposes an
// Anthropic-messages-compatible API at this base URL, authenticated the same
// way as Anthropic's own API (X-Api-Key header), which is what
// option.WithAPIKey sends. See https://opencode.ai/docs/zen/
const ZenBaseURL = "https://opencode.ai/zen"

// ProviderAnthropic and ProviderOpenCodeZen are the supported provider names
// for NewClientWithConfig / NewClientForProvider.
const (
	ProviderAnthropic   = "anthropic"
	ProviderOpenCodeZen = "opencode-zen"
)

// Error types for stage-level retry decisions.

// NetworkError wraps transient network failures and HTTP 5xx responses.
type NetworkError struct{ Cause error }

func (e *NetworkError) Error() string { return fmt.Sprintf("network error: %v", e.Cause) }
func (e *NetworkError) Unwrap() error { return e.Cause }

// RateLimitError wraps HTTP 429 responses.
type RateLimitError struct {
	RetryAfter time.Duration
	Cause      error
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limited (retry after %v): %v", e.RetryAfter, e.Cause)
	}
	return fmt.Sprintf("rate limited: %v", e.Cause)
}
func (e *RateLimitError) Unwrap() error { return e.Cause }

// MalformedResponseError wraps responses where the LLM returned unparseable output.
type MalformedResponseError struct{ Raw string }

func (e *MalformedResponseError) Error() string {
	preview := e.Raw
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}
	return fmt.Sprintf("malformed LLM response (not parseable JSON): %q", preview)
}

// Client wraps the Anthropic SDK for simple prompt→JSON calls.
type Client struct {
	inner    *anthropic.Client
	provider string
	model    string

	mu          sync.Mutex
	totalInput  int64
	totalOutput int64
	totalCost   float64
}

// TokenUsage returns the cumulative token usage for this client.
type TokenUsage struct {
	InputTokens   int64
	OutputTokens  int64
	TotalTokens   int64
	EstimatedCost float64
}

// NewClient creates an Anthropic client. API key is read from ANTHROPIC_API_KEY
// env var by the SDK automatically; pass an explicit key to override.
func NewClient(apiKey string) *Client {
	return NewClientWithConfig(apiKey, ProviderAnthropic, "")
}

// NewClientWithConfig creates a client with explicit provider and model settings.
// Supported providers:
//   - "anthropic" (default): talks directly to api.anthropic.com.
//   - "opencode-zen": routes through OpenCode Zen (opencode.ai/zen), an
//     Anthropic-messages-compatible gateway to many models. Useful as a
//     fallback when the Anthropic account is out of quota/tokens.
//
// Model overrides the default model if non-empty; for "opencode-zen" it
// defaults to DefaultZenModel instead of Model.
func NewClientWithConfig(apiKey, provider, model string) *Client {
	if provider == "" {
		provider = ProviderAnthropic
	}

	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if provider == ProviderOpenCodeZen {
		opts = append(opts, option.WithBaseURL(ZenBaseURL))
		if model == "" {
			model = DefaultZenModel
		}
	}

	c := anthropic.NewClient(opts...)
	return &Client{
		inner:    &c,
		provider: provider,
		model:    model,
	}
}

// NewClientForProvider builds a Client for the named provider, applying
// sane env-var fallbacks when apiKey is empty:
//   - "anthropic":    ANTHROPIC_API_KEY
//   - "opencode-zen": OPENCODE_API_KEY
//
// An unknown/empty provider is treated as "anthropic".
func NewClientForProvider(provider, apiKey, model string) *Client {
	if apiKey == "" {
		switch provider {
		case ProviderOpenCodeZen:
			apiKey = os.Getenv("OPENCODE_API_KEY")
		default:
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
	}
	return NewClientWithConfig(apiKey, provider, model)
}

// Usage returns the cumulative token usage and estimated cost.
func (c *Client) Usage() TokenUsage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return TokenUsage{
		InputTokens:   c.totalInput,
		OutputTokens:  c.totalOutput,
		TotalTokens:   c.totalInput + c.totalOutput,
		EstimatedCost: c.totalCost,
	}
}

// ResetUsage clears the cumulative token and cost counters.
func (c *Client) ResetUsage() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.totalInput = 0
	c.totalOutput = 0
	c.totalCost = 0
}

func (c *Client) trackUsage(inputTokens, outputTokens int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.totalInput += inputTokens
	c.totalOutput += outputTokens
	c.totalCost += estimateCost(c.model, inputTokens, outputTokens)
}

func (c *Client) resolveModel() string {
	if c.model != "" {
		return c.model
	}
	return Model
}

// estimateCost returns a rough USD cost estimate based on model pricing tiers.
func estimateCost(model string, input, output int64) float64 {
	inputPricePerM := 3.0
	outputPricePerM := 15.0
	return (float64(input)/1_000_000)*inputPricePerM + (float64(output)/1_000_000)*outputPricePerM
}

// Complete sends a system + user prompt and returns the raw text response.
//
// Retry policy (caller is responsible for acting on error types):
//   - NetworkError / HTTP 5xx: caller should retry up to 3 times with exponential backoff
//   - RateLimitError: caller should retry up to 5 times, respecting RetryAfter
//   - MalformedResponseError: caller should retry up to 2 times; if still malformed, halt
func (c *Client) Complete(ctx context.Context, system, user string) (string, error) {
	msg, err := c.inner.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.resolveModel()),
		MaxTokens: 16384,
		System: []anthropic.TextBlockParam{
			{Text: system},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	})
	if err != nil {
		return "", classifyError(err)
	}
	if len(msg.Content) == 0 {
		return "", &NetworkError{Cause: fmt.Errorf("empty response from model")}
	}

	c.trackUsage(msg.Usage.InputTokens, msg.Usage.OutputTokens)

	// Detect truncation — if the model hit max_tokens, the output is likely
	// incomplete JSON that will fail to parse. Surface a clear error so the
	// caller can retry or the user sees what happened.
	if msg.StopReason == anthropic.StopReasonMaxTokens {
		return "", &MalformedResponseError{
			Raw: "response truncated at max_tokens — plan may be too large; consider fewer steps",
		}
	}

	var sb strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String(), nil
}

// CompleteWithRetry wraps Complete with the full stage-level retry policy.
// It is the preferred entry point for all pipeline stage LLM calls.
func (c *Client) CompleteWithRetry(ctx context.Context, system, user string) (string, error) {
	const (
		maxNetworkRetries   = 3
		maxRateLimitRetries = 5
		maxMalformedRetries = 2
	)

	networkAttempts := 0
	rateLimitAttempts := 0
	malformedAttempts := 0

	for {
		raw, err := c.Complete(ctx, system, user)
		if err == nil {
			return raw, nil
		}

		var netErr *NetworkError
		var rlErr *RateLimitError
		var malErr *MalformedResponseError

		switch {
		case errors.As(err, &rlErr):
			rateLimitAttempts++
			if rateLimitAttempts > maxRateLimitRetries {
				return "", fmt.Errorf("rate limit exceeded after %d retries: %w", maxRateLimitRetries, err)
			}
			wait := rlErr.RetryAfter
			if wait == 0 {
				wait = time.Duration(rateLimitAttempts*2) * time.Second
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(wait):
			}

		case errors.As(err, &netErr):
			networkAttempts++
			if networkAttempts > maxNetworkRetries {
				return "", fmt.Errorf("network error after %d retries: %w", maxNetworkRetries, err)
			}
			wait := time.Duration(1<<uint(networkAttempts)) * time.Second // 2s, 4s, 8s
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(wait):
			}

		case errors.As(err, &malErr):
			malformedAttempts++
			if malformedAttempts > maxMalformedRetries {
				return "", fmt.Errorf("malformed response after %d retries (prompt engineering issue): %w", maxMalformedRetries, err)
			}
			// No backoff for malformed — retry immediately

		default:
			return "", err
		}
	}
}

// classifyError maps SDK errors to typed LLM errors for retry decisions.
func classifyError(err error) error {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Rate limit
	if strings.Contains(errStr, "429") || strings.Contains(errStr, "rate_limit") {
		return &RateLimitError{Cause: err}
	}

	// HTTP 5xx / network
	for _, code := range []string{"500", "502", "503", "504"} {
		if strings.Contains(errStr, code) {
			return &NetworkError{Cause: err}
		}
	}
	if strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "network") {
		return &NetworkError{Cause: err}
	}

	// Check for http error type with status code
	var httpErr interface{ StatusCode() int }
	if errors.As(err, &httpErr) {
		code := httpErr.StatusCode()
		if code == http.StatusTooManyRequests {
			return &RateLimitError{Cause: err}
		}
		if code >= 500 {
			return &NetworkError{Cause: err}
		}
	}

	return fmt.Errorf("llm call failed: %w", err)
}
