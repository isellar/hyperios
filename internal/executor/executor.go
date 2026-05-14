package executor

import (
	"context"
	"fmt"
	"io"

	"github.com/isellar/hyperios/internal/bus"
	"github.com/isellar/hyperios/internal/capability"
	"github.com/isellar/hyperios/internal/types"
)

type ExecutorConfig struct {
	DryRun       bool
	Registry     *capability.Registry
	Workspace    string
	ExecutorType types.ExecutorType
	Image        string
	DockerHost   string
	// Bus is the session event bus. When set, the executor publishes
	// step lifecycle events. Optional — nil disables publishing.
	Bus       *bus.Bus
	SessionID string
}

func New(cfg ExecutorConfig) Executor {
	if cfg.DryRun {
		return &Stub{out: io.Discard}
	}
	if cfg.Registry == nil {
		cfg.Registry = capability.NewRegistry()
	}

	switch cfg.ExecutorType {
	case types.ExecutorContainer:
		return NewContainerWithBus(cfg.Registry, cfg.Workspace, cfg.Image, cfg.Bus, cfg.SessionID)
	default:
		return NewLocal(cfg.Registry, cfg.Workspace, cfg.Bus, cfg.SessionID)
	}
}

// Stub is a Phase 0 executor that prints what a human should do manually.
// It never touches the OS.
type Stub struct {
	out io.Writer
}

func NewStub(out io.Writer) *Stub {
	return &Stub{out: out}
}

func (s *Stub) Execute(ctx context.Context, step types.ActionStep) (*types.ExecutionResult, error) {
	return &types.ExecutionResult{
		StepID:  step.ID,
		Success: false,
		Error:   "dry-run mode",
	}, nil
}

func (s *Stub) Validate(step types.ActionStep) error {
	return nil
}

func (s *Stub) Name() string {
	return "stub"
}

// Present outputs the final proposed plan with arbiter verdicts to the user.
func (s *Stub) Present(plan *types.ActionPlan, verdicts []types.ArbiterVerdict, report *types.RiskReport) {
	// Index verdicts and flags by step ID
	verdictMap := map[string]types.ArbiterVerdict{}
	for _, v := range verdicts {
		verdictMap[v.StepID] = v
	}
	flagMap := map[string][]types.RiskFlag{}
	for _, f := range report.Flags {
		flagMap[f.StepID] = append(flagMap[f.StepID], f)
	}

	fmt.Fprintln(s.out, "\n-- Proposed Action Plan -----------------------------------------------")
	for i, step := range plan.Steps {
		v := verdictMap[step.ID]
		icon := verdictIcon(v.Verdict)
		fmt.Fprintf(s.out, "\n%s Step %d [%s] %s\n", icon, i+1, step.ID, step.Description)
		fmt.Fprintf(s.out, "   Capability : %s %s\n", step.Capability.Type, step.Capability.Scope)
		fmt.Fprintf(s.out, "   Reversible : %v\n", step.Reversible)
		fmt.Fprintf(s.out, "   Verdict    : %s -- %s\n", v.Verdict, v.Reason)

		if flags, ok := flagMap[step.ID]; ok {
			for _, f := range flags {
				fmt.Fprintf(s.out, "   [%s] %s\n", f.Severity, f.Description)
				fmt.Fprintf(s.out, "      Worst case: %s\n", f.Counterfactual)
			}
		}

		if v.Verdict == "approved" {
			fmt.Fprintf(s.out, "   -> Action required: %s\n", step.Description)
		} else if v.Verdict == "modified" {
			fmt.Fprintf(s.out, "   -> Needs your approval before executing: %s\n", step.Description)
		} else {
			fmt.Fprintf(s.out, "   X Blocked -- do not execute\n")
		}
	}

	fmt.Fprintln(s.out, "\n-- Adversarial Summary -----------------------------------------------")
	fmt.Fprintln(s.out, report.Summary)
	fmt.Fprintln(s.out, "----------------------------------------------------------------------")
}

func verdictIcon(v string) string {
	switch v {
	case "approved":
		return "+"
	case "modified":
		return "~"
	case "blocked":
		return "X"
	default:
		return "?"
	}
}
