package agents

import (
	"context"

	"github.com/isellar/hyperios/internal/governor"
	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/types"
)

// AdversarialAgent reviews an ActionPlan and produces a RiskReport.
// Delegates to governor.AdversarialAgent.
type AdversarialAgent struct {
	inner *governor.AdversarialAgent
}

func NewAdversarialAgent(client llm.Completer) *AdversarialAgent {
	return &AdversarialAgent{inner: governor.NewAdversarialAgent(client)}
}

func (a *AdversarialAgent) Run(ctx context.Context, graph *types.GoalGraph, plan *types.ActionPlan) (*types.RiskReport, error) {
	return a.inner.Run(ctx, graph, plan)
}
