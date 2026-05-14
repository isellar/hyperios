package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const Model = anthropic.ModelClaudeSonnet4_6

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
	inner *anthropic.Client
}

// NewClient creates an Anthropic client. API key is read from ANTHROPIC_API_KEY
// env var by the SDK automatically; pass an explicit key to override.
func NewClient(apiKey string) *Client {
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	c := anthropic.NewClient(opts...)
	return &Client{inner: &c}
}

// Complete sends a system + user prompt and returns the raw text response.
//
// Retry policy (caller is responsible for acting on error types):
//   - NetworkError / HTTP 5xx: caller should retry up to 3 times with exponential backoff
//   - RateLimitError: caller should retry up to 5 times, respecting RetryAfter
//   - MalformedResponseError: caller should retry up to 2 times; if still malformed, halt
func (c *Client) Complete(ctx context.Context, system, user string) (string, error) {
	msg, err := c.inner.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     Model,
		MaxTokens: 4096,
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
		maxNetworkRetries  = 3
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
