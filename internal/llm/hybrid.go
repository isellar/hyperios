package llm

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
)

// HybridCompleter tries a local Completer first and falls back to a remote
// one on failure. This is the mechanism behind HyperiOS's "avoid paid API
// calls when possible" behavior: local inference is free, so we prefer it,
// but a single flaky/unavailable local call shouldn't fail the whole goal —
// it silently upgrades to the configured remote provider instead.
//
// Both Local and Remote are optional individually (either may be nil), but at
// least one must be non-nil or every call returns an error.
type HybridCompleter struct {
	Local  Completer
	Remote Completer

	// LocalTools/RemoteTools are used by CompleteWithTools when the
	// respective Completer also implements ToolCompleter. Populated
	// automatically by NewHybridCompleter via type assertion.
	localTools  ToolCompleter
	remoteTools ToolCompleter

	// onFallback, if set, is called whenever a call falls back from local to
	// remote, with the error that triggered the fallback. Useful for
	// surfacing "this goal cost money" signals to the caller/UI.
	onFallback func(error)

	// lastUsedLocal tracks whether the most recent call was served locally,
	// for reporting/health purposes. 1 = local, 0 = remote/unknown.
	lastUsedLocal atomic.Bool
}

// NewHybridCompleter constructs a HybridCompleter. Pass nil for local or
// remote to disable that path entirely (e.g. local-only with no fallback, or
// remote-only which is equivalent to using remote directly).
func NewHybridCompleter(local, remote Completer, onFallback func(error)) *HybridCompleter {
	h := &HybridCompleter{Local: local, Remote: remote, onFallback: onFallback}
	if lt, ok := local.(ToolCompleter); ok {
		h.localTools = lt
	}
	if rt, ok := remote.(ToolCompleter); ok {
		h.remoteTools = rt
	}
	return h
}

var (
	_ Completer     = (*HybridCompleter)(nil)
	_ ToolCompleter = (*HybridCompleter)(nil)
)

// LastUsedLocal reports whether the most recently completed call was served
// by the local model (true) or fell back to remote (false). Intended for
// lightweight reporting (e.g. module.Report metrics), not for
// concurrency-safe per-call attribution.
func (h *HybridCompleter) LastUsedLocal() bool {
	return h.lastUsedLocal.Load()
}

// Complete tries Local first (if configured), falling back to Remote on any
// error. Returns an error only if both fail (or neither is configured).
func (h *HybridCompleter) Complete(ctx context.Context, system, user string) (string, error) {
	if h.Local != nil {
		out, err := h.Local.Complete(ctx, system, user)
		if err == nil {
			h.lastUsedLocal.Store(true)
			return out, nil
		}
		h.reportFallback(err)
	}
	if h.Remote == nil {
		return "", fmt.Errorf("hybrid completer: local failed and no remote fallback configured")
	}
	h.lastUsedLocal.Store(false)
	return h.Remote.Complete(ctx, system, user)
}

// CompleteWithRetry mirrors Complete but uses each side's own retry policy.
func (h *HybridCompleter) CompleteWithRetry(ctx context.Context, system, user string) (string, error) {
	if h.Local != nil {
		out, err := h.Local.CompleteWithRetry(ctx, system, user)
		if err == nil {
			h.lastUsedLocal.Store(true)
			return out, nil
		}
		h.reportFallback(err)
	}
	if h.Remote == nil {
		return "", fmt.Errorf("hybrid completer: local failed and no remote fallback configured")
	}
	h.lastUsedLocal.Store(false)
	return h.Remote.CompleteWithRetry(ctx, system, user)
}

// CompleteWithTools tries the local ToolCompleter first (if the local
// Completer supports tool-use), falling back to remote on error. If the
// local Completer doesn't implement ToolCompleter at all, this goes straight
// to remote.
func (h *HybridCompleter) CompleteWithTools(ctx context.Context, system string, messages []Message, tools []ToolDef) (*ToolResponse, error) {
	if h.localTools != nil {
		out, err := h.localTools.CompleteWithTools(ctx, system, messages, tools)
		if err == nil {
			h.lastUsedLocal.Store(true)
			return out, nil
		}
		h.reportFallback(err)
	}
	if h.remoteTools == nil {
		return nil, fmt.Errorf("hybrid completer: local tool-use failed/unavailable and no remote tool-use fallback configured")
	}
	h.lastUsedLocal.Store(false)
	return h.remoteTools.CompleteWithTools(ctx, system, messages, tools)
}

func (h *HybridCompleter) reportFallback(err error) {
	if h.onFallback != nil {
		h.onFallback(err)
		return
	}
	log.Printf("llm: local model call failed, falling back to remote provider: %v", err)
}
