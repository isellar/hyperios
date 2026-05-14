package llm

import (
	"context"
)

// Completer defines the interface for LLM completion.
// This allows for mocking in tests.
type Completer interface {
	Complete(ctx context.Context, system, user string) (string, error)
	// CompleteWithRetry wraps Complete with the full stage-level retry policy.
	// Preferred entry point for all pipeline stage LLM calls.
	CompleteWithRetry(ctx context.Context, system, user string) (string, error)
}

// Client implements the Completer interface.
var _ Completer = (*Client)(nil)
