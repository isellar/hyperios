package goal_fulfillment

import (
	"context"

	"github.com/isellar/hyperios/internal/llm"
)

type mockCompleter struct {
	response string
	err      error
}

func (m *mockCompleter) Complete(ctx context.Context, system, user string) (string, error) {
	return m.response, m.err
}

func (m *mockCompleter) CompleteWithRetry(ctx context.Context, system, user string) (string, error) {
	return m.response, m.err
}

var _ llm.Completer = (*mockCompleter)(nil)

type mockMemory struct {
	store map[string]string
}

func newMockMemory() *mockMemory {
	return &mockMemory{store: make(map[string]string)}
}

func (m *mockMemory) GetContext(key string) (string, error) {
	return m.store[key], nil
}

func (m *mockMemory) StoreContext(key, value string) error {
	m.store[key] = value
	return nil
}

type mockProcessor struct {
	results map[string]string
}

func newMockProcessor() *mockProcessor {
	return &mockProcessor{results: make(map[string]string)}
}

func (m *mockProcessor) Lookup(query string) (string, error) {
	return m.results[query], nil
}
