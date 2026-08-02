package llm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type stubCompleter struct {
	text string
	err  error
	// calls counts invocations for assertions.
	calls int
}

func (s *stubCompleter) Complete(ctx context.Context, system, user string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.text, nil
}

func (s *stubCompleter) CompleteWithRetry(ctx context.Context, system, user string) (string, error) {
	return s.Complete(ctx, system, user)
}

// stubToolCompleter additionally implements ToolCompleter.
type stubToolCompleter struct {
	stubCompleter
	toolResp *ToolResponse
	toolErr  error
}

func (s *stubToolCompleter) CompleteWithTools(ctx context.Context, system string, messages []Message, tools []ToolDef) (*ToolResponse, error) {
	s.calls++
	if s.toolErr != nil {
		return nil, s.toolErr
	}
	return s.toolResp, nil
}

func TestHybridCompleter_LocalSucceeds(t *testing.T) {
	local := &stubCompleter{text: "local answer"}
	remote := &stubCompleter{text: "remote answer"}
	h := NewHybridCompleter(local, remote, nil)

	out, err := h.Complete(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "local answer" {
		t.Errorf("expected local answer, got %q", out)
	}
	if remote.calls != 0 {
		t.Errorf("remote should not have been called, got %d calls", remote.calls)
	}
	if !h.LastUsedLocal() {
		t.Error("expected LastUsedLocal() == true")
	}
}

func TestHybridCompleter_FallsBackOnLocalError(t *testing.T) {
	local := &stubCompleter{err: errors.New("local down")}
	remote := &stubCompleter{text: "remote answer"}

	var fallbackErr error
	h := NewHybridCompleter(local, remote, func(err error) { fallbackErr = err })

	out, err := h.Complete(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "remote answer" {
		t.Errorf("expected remote answer, got %q", out)
	}
	if fallbackErr == nil {
		t.Error("expected onFallback to be called with the local error")
	}
	if h.LastUsedLocal() {
		t.Error("expected LastUsedLocal() == false after fallback")
	}
}

func TestHybridCompleter_BothFail(t *testing.T) {
	local := &stubCompleter{err: errors.New("local down")}
	remote := &stubCompleter{err: errors.New("remote down")}
	h := NewHybridCompleter(local, remote, nil)

	_, err := h.Complete(context.Background(), "sys", "user")
	if err == nil {
		t.Fatal("expected error when both local and remote fail")
	}
}

func TestHybridCompleter_NoLocalConfigured(t *testing.T) {
	remote := &stubCompleter{text: "remote answer"}
	h := NewHybridCompleter(nil, remote, nil)

	out, err := h.Complete(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "remote answer" {
		t.Errorf("expected remote answer, got %q", out)
	}
}

func TestHybridCompleter_NoRemoteConfigured_LocalFails(t *testing.T) {
	local := &stubCompleter{err: errors.New("local down")}
	h := NewHybridCompleter(local, nil, nil)

	_, err := h.Complete(context.Background(), "sys", "user")
	if err == nil {
		t.Fatal("expected error when local fails and no remote fallback is configured")
	}
}

func TestHybridCompleter_CompleteWithTools_LocalSucceeds(t *testing.T) {
	local := &stubToolCompleter{toolResp: &ToolResponse{Text: "local tool answer", StopReason: "end_turn"}}
	remote := &stubToolCompleter{toolResp: &ToolResponse{Text: "remote tool answer", StopReason: "end_turn"}}
	h := NewHybridCompleter(local, remote, nil)

	resp, err := h.CompleteWithTools(context.Background(), "sys", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "local tool answer" {
		t.Errorf("expected local tool answer, got %q", resp.Text)
	}
	if remote.calls != 0 {
		t.Errorf("remote should not have been called, got %d calls", remote.calls)
	}
}

func TestHybridCompleter_CompleteWithTools_FallsBack(t *testing.T) {
	local := &stubToolCompleter{toolErr: errors.New("local tool-use failed")}
	remote := &stubToolCompleter{toolResp: &ToolResponse{Text: "remote tool answer", StopReason: "end_turn"}}
	h := NewHybridCompleter(local, remote, nil)

	resp, err := h.CompleteWithTools(context.Background(), "sys", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "remote tool answer" {
		t.Errorf("expected remote tool answer, got %q", resp.Text)
	}
}

func TestHybridCompleter_CompleteWithTools_LocalNotToolCapable(t *testing.T) {
	// local only implements Completer, not ToolCompleter.
	local := &stubCompleter{text: "local answer"}
	remote := &stubToolCompleter{toolResp: &ToolResponse{Text: "remote tool answer", StopReason: "end_turn"}}
	h := NewHybridCompleter(local, remote, nil)

	resp, err := h.CompleteWithTools(context.Background(), "sys", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "remote tool answer" {
		t.Errorf("expected remote tool answer (local has no tool support), got %q", resp.Text)
	}
}

func TestSimpleToolInput(t *testing.T) {
	raw := json.RawMessage(`{"input":"ls -la"}`)
	val, err := SimpleToolInput(raw, "input")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "ls -la" {
		t.Errorf("expected 'ls -la', got %q", val)
	}
}
