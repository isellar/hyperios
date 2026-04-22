package executor

import (
	"context"

	"github.com/isellar/hyperios/internal/types"
)

type Executor interface {
	Execute(ctx context.Context, step types.ActionStep) (*types.ExecutionResult, error)
	Validate(step types.ActionStep) error
	Name() string
}
