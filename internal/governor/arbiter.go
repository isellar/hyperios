package governor

import (
	"fmt"
	"os"
	"sort"

	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/types"
	"gopkg.in/yaml.v3"
)

type PolicyArbiter struct {
	autonomyLevel int
	directives    []types.Directive
}

func NewArbiter() *PolicyArbiter {
	return &PolicyArbiter{autonomyLevel: config.AutonomyApproved}
}

func NewArbiterWithLevel(level int) *PolicyArbiter {
	return &PolicyArbiter{autonomyLevel: level}
}

type directivesFile struct {
	Directives []types.Directive `yaml:"directives"`
}

func (a *PolicyArbiter) LoadDirectives(immutablePath, mutablePath string) error {
	var all []types.Directive

	if immutablePath != "" {
		directives, err := loadDirectivesFile(immutablePath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("load immutable directives: %w", err)
		}
		for i := range directives {
			directives[i].Immutable = true
		}
		all = append(all, directives...)
	}

	if mutablePath != "" {
		directives, err := loadDirectivesFile(mutablePath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("load mutable directives: %w", err)
		}
		all = append(all, directives...)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].Priority < all[j].Priority
	})

	a.directives = all
	return nil
}

func loadDirectivesFile(path string) ([]types.Directive, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var df directivesFile
	if err := yaml.Unmarshal(data, &df); err != nil {
		return nil, err
	}
	return df.Directives, nil
}

type ReviewResult struct {
	Approved       bool
	Reason         string
	ViolatedDirectives []types.Directive
}

func (a *PolicyArbiter) ReviewGoal(goal *types.Goal) (*ReviewResult, error) {
	if goal == nil {
		return nil, fmt.Errorf("goal is nil")
	}

	var violated []types.Directive
	for _, d := range a.directives {
		if a.goalViolatesDirective(goal, d) {
			violated = append(violated, d)
		}
	}

	if len(violated) > 0 {
		return &ReviewResult{
			Approved:           false,
			Reason:             fmt.Sprintf("goal violates %d directive(s), highest priority: %s", len(violated), violated[0].Description),
			ViolatedDirectives: violated,
		}, nil
	}

	return &ReviewResult{
		Approved: true,
		Reason:   "goal complies with all directives",
	}, nil
}

func (a *PolicyArbiter) goalViolatesDirective(goal *types.Goal, d types.Directive) bool {
	return false
}

func (a *PolicyArbiter) Directives() []types.Directive {
	return a.directives
}

func (a *PolicyArbiter) Decide(plan *types.ActionPlan, report *types.RiskReport) []types.ArbiterVerdict {
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

func (a *PolicyArbiter) decideStep(step types.ActionStep, flagged bool, severity, description string) (verdict, reason string) {
	if flagged && severity == "block" {
		return "blocked", fmt.Sprintf("adversarial agent flagged as block: %s", description)
	}

	switch a.autonomyLevel {
	case config.AutonomyObserve:
		return "modified", "autonomy level 0: plan presented as suggestion only — user approval required for all steps"

	case config.AutonomyApproved:
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

	case config.AutonomyReversible:
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

	case config.AutonomyBounded:
		if flagged && severity == "high" {
			return "modified", fmt.Sprintf("high-severity risk — requires explicit user approval at level 3: %s", description)
		}
		if flagged {
			return "approved", fmt.Sprintf("%s-severity note (approved at level 3): %s", severity, description)
		}
		return "approved", "approved — adversarial review passed at autonomy level 3"

	case config.AutonomyTrusted:
		if flagged {
			return "approved", fmt.Sprintf("%s-severity note (approved at level 4): %s", severity, description)
		}
		return "approved", "approved — trusted autonomy level 4"

	default:
		if flagged && severity == "high" {
			return "modified", fmt.Sprintf("high-severity risk: %s", description)
		}
		return "approved", "no risks identified"
	}
}

func (a *PolicyArbiter) AutonomyLevel() int {
	return a.autonomyLevel
}
