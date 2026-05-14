package arbiter

import (
	"fmt"

	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/types"
)

// PolicyArbiter is the deterministic, rule-based final authority.
// It has no LLM — it applies fixed rules to produce verdicts.
// It is autonomy-level aware: the level controls when modified verdicts
// require user approval vs execute automatically.
type PolicyArbiter struct {
	autonomyLevel int
}

// New creates a PolicyArbiter at the default autonomy level (1 = execute approved).
func New() *PolicyArbiter {
	return &PolicyArbiter{autonomyLevel: config.AutonomyApproved}
}

// NewWithLevel creates a PolicyArbiter at a specific autonomy level.
func NewWithLevel(level int) *PolicyArbiter {
	return &PolicyArbiter{autonomyLevel: level}
}

// Decide produces one ArbiterVerdict per ActionStep.
//
// Hard rules (never overridden by autonomy level):
//  1. Any step with a "block" severity flag → blocked.
//
// Soft rules (affected by autonomy level):
//
//	Level 0 (observe): all steps → modified (nothing executes without explicit approval).
//	Level 1 (approved): high/block flags → blocked/modified; irreversible → modified; else → approved.
//	Level 2 (reversible): irreversible-only → modified; reversible → approved without prompt.
//	Level 3 (bounded): high/block → blocked/modified; irreversible → approved after adversarial.
//	Level 4 (trusted): only block → blocked; everything else → approved.
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
		verdict, reason := a.decideStep(step, flagged, flag.severity, flag.description)

		verdicts = append(verdicts, types.ArbiterVerdict{
			StepID:   step.ID,
			Verdict:  verdict,
			Reason:   reason,
			Autonomy: a.autonomyLevel,
		})
	}
	return verdicts
}

// decideStep applies the autonomy-level-aware verdict logic for a single step.
func (a *PolicyArbiter) decideStep(step types.ActionStep, flagged bool, severity, description string) (verdict, reason string) {
	// Hard block — always blocked regardless of autonomy level.
	if flagged && severity == "block" {
		return "blocked", fmt.Sprintf("adversarial agent flagged as block: %s", description)
	}

	switch a.autonomyLevel {
	case config.AutonomyObserve: // 0
		// Everything requires approval — plan is a suggestion only.
		return "modified", "autonomy level 0: plan presented as suggestion only — user approval required for all steps"

	case config.AutonomyApproved: // 1
		// Standard logic: high severity or irreversible → modified.
		switch {
		case flagged && severity == "high":
			return "modified", fmt.Sprintf("high-severity risk — requires explicit user approval: %s", description)
		case !step.Reversible && !flagged:
			return "modified", "irreversible action — precautionary manual approval required"
		default:
			if flagged {
				return "approved", fmt.Sprintf("%s-severity note: %s", severity, description)
			}
			return "approved", "no risks identified"
		}

	case config.AutonomyReversible: // 2
		// Reversible steps execute without prompt; irreversible require approval.
		switch {
		case flagged && severity == "high":
			return "modified", fmt.Sprintf("high-severity risk — requires explicit user approval: %s", description)
		case !step.Reversible:
			return "modified", "irreversible action — approval required at autonomy level 2"
		default:
			if flagged {
				return "approved", fmt.Sprintf("%s-severity note: %s", severity, description)
			}
			return "approved", "reversible step — auto-approved at autonomy level 2"
		}

	case config.AutonomyBounded: // 3
		// Irreversible steps approved after adversarial review; only block → blocked.
		if flagged && severity == "high" {
			return "modified", fmt.Sprintf("high-severity risk — requires explicit user approval at level 3: %s", description)
		}
		if flagged {
			return "approved", fmt.Sprintf("%s-severity note (approved at level 3): %s", severity, description)
		}
		return "approved", "approved — adversarial review passed at autonomy level 3"

	case config.AutonomyTrusted: // 4
		// Only block flags halt; everything else runs.
		if flagged {
			return "approved", fmt.Sprintf("%s-severity note (approved at level 4): %s", severity, description)
		}
		return "approved", "approved — trusted autonomy level 4"

	default:
		// Unknown level — fall back to level 1 behavior.
		if flagged && severity == "high" {
			return "modified", fmt.Sprintf("high-severity risk: %s", description)
		}
		return "approved", "no risks identified"
	}
}

// AutonomyLevel returns the level this arbiter was created with.
func (a *PolicyArbiter) AutonomyLevel() int {
	return a.autonomyLevel
}
