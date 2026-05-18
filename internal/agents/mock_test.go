package agents

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