package arbiter

import (
	"fmt"

	"github.com/isellar/hyperios/internal/types"
)

// PolicyArbiter is the deterministic, rule-based final authority.
// It has no LLM — it applies fixed rules to produce verdicts.
type PolicyArbiter struct{}

func New() *PolicyArbiter {
	return &PolicyArbiter{}
}

// Decide produces one ArbiterVerdict per ActionStep.
//
// Rules (in priority order):
//  1. Any step with a "block" severity flag → blocked.
//  2. Any step with a "high" severity flag  → modified (requires explicit user approval).
//  3. Any irreversible step with no flag    → modified (precautionary).
//  4. Everything else                       → approved.
func (a *PolicyArbiter) Decide(plan *types.ActionPlan, report *types.RiskReport) []types.ArbiterVerdict {
	// Index flags by step ID, keeping highest severity per step.
	type flagSummary struct {
		severity    string
		description string
	}
	flags := map[string]flagSummary{}
	severityRank := map[string]int{"low": 1, "medium": 2, "high": 3, "block": 4}

	for _, f := range report.Flags {
		existing, ok := flags[f.StepID]
		if !ok || severityRank[f.Severity] > severityRank[existing.severity] {
			flags[f.StepID] = flagSummary{severity: f.Severity, description: f.Description}
		}
	}

	verdicts := make([]types.ArbiterVerdict, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		flag, flagged := flags[step.ID]
		var verdict, reason string

		switch {
		case flagged && flag.severity == "block":
			verdict = "blocked"
			reason = fmt.Sprintf("adversarial agent flagged as block: %s", flag.description)
		case flagged && flag.severity == "high":
			verdict = "modified"
			reason = fmt.Sprintf("high-severity risk — requires explicit user approval: %s", flag.description)
		case !step.Reversible && !flagged:
			verdict = "modified"
			reason = "irreversible action with no adversarial flag — precautionary manual approval required"
		default:
			verdict = "approved"
			if flagged {
				reason = fmt.Sprintf("%s-severity note: %s", flag.severity, flag.description)
			} else {
				reason = "no risks identified"
			}
		}

		verdicts = append(verdicts, types.ArbiterVerdict{
			StepID:  step.ID,
			Verdict: verdict,
			Reason:  reason,
		})
	}
	return verdicts
}
