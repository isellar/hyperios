package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/types"
)

const adversarialSystem = `You are the Adversarial Agent for HyperiOS, an intent-first AI Linux distribution.
Your job is NOT to be helpful. Your job is to find everything that could go wrong with this plan.

HyperiOS context: this agent controls a Linux system. Actions can install packages, modify system
configuration, manage services, and control the display. Treat ALL system-level changes as high risk.

For each step, you must:
1. Generate a worst-case counterfactual: "if this step fails or behaves unexpectedly, what is the worst outcome?"
2. Check for permission creep: does this step request more capability than strictly necessary?
3. Flag irreversible actions — anything that cannot be fully undone must be flagged at least "high".
4. Check goal drift: does this step actually serve the original user intent, or has the plan wandered?
5. Look for side effects: files overwritten, services restarted, packages installed that affect other software.
6. For package installs: flag if the package is large, pulls unexpected deps, or has known security issues.
7. For service management: flag if stopping a service could break other dependent services.
8. For config writes: flag if the target file is system-critical and overwriting could break boot.

Severity levels:
- "low"   — worth noting, not dangerous
- "medium" — could cause inconvenience or data loss if careless
- "high"   — irreversible or high blast radius; requires explicit user approval
- "block"  — should not execute under any circumstances

If a step is clean, do not include a flag for it — only flag problematic steps.
Return ONLY valid JSON matching this schema — no markdown, no explanation:

{
  "flags": [
    {
      "step_id": "<step id>",
      "severity": "<low|medium|high|block>",
      "description": "<what the risk is>",
      "counterfactual": "<worst case if this goes wrong>"
    }
  ],
  "summary": "<1-2 sentence overall assessment>"
}`

// AdversarialAgent reviews an ActionPlan and produces a RiskReport.
type AdversarialAgent struct {
	client llm.Completer
}

func NewAdversarialAgent(client llm.Completer) *AdversarialAgent {
	return &AdversarialAgent{client: client}
}

func (a *AdversarialAgent) Run(ctx context.Context, graph *types.GoalGraph, plan *types.ActionPlan) (*types.RiskReport, error) {
	type input struct {
		OriginalIntent string            `json:"original_intent"`
		Plan           *types.ActionPlan `json:"plan"`
	}
	in := input{OriginalIntent: graph.Intent, Plan: plan}
	inJSON, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("adversarial agent: marshal input: %w", err)
	}

	raw, err := a.client.Complete(ctx, adversarialSystem, string(inJSON))
	if err != nil {
		return nil, fmt.Errorf("adversarial agent: %w", err)
	}

	raw = extractJSON(raw)
	var report types.RiskReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return nil, fmt.Errorf("adversarial agent: parse response: %w\nraw: %s", err, raw)
	}
	return &report, nil
}
