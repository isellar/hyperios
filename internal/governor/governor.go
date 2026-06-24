package governor

import (
	"context"
	"fmt"
	"time"

	"github.com/isellar/hyperios/internal/audit"
	"github.com/isellar/hyperios/internal/governor/capability"
	"github.com/isellar/hyperios/internal/governor/executor"
	"github.com/isellar/hyperios/internal/module"
	"github.com/isellar/hyperios/internal/types"
)

type Governor struct {
	arbiter     *PolicyArbiter
	registry    *capability.Registry
	enforcer    *capability.Enforcer
	validator   *capability.CommandValidator
	executor    executor.Executor
	adversarial *AdversarialAgent
	auth        *ToolAuthorization
	auditLog    *audit.Logger
	sessionID   string
}

type GovernorConfig struct {
	AutonomyLevel int
	Registry      *capability.Registry
	Validator     *capability.CommandValidator
	Exec          executor.Executor
	AuditLogger   *audit.Logger
	SessionID     string
	ToolAuthPath  string
}

func New(cfg GovernorConfig) *Governor {
	reg := cfg.Registry
	if reg == nil {
		reg = capability.NewRegistry()
	}

	return &Governor{
		arbiter:   NewArbiterWithLevel(cfg.AutonomyLevel),
		registry:  reg,
		enforcer:  capability.NewEnforcer(reg),
		validator: cfg.Validator,
		executor:  cfg.Exec,
		auth:      NewToolAuthorization(reg, cfg.ToolAuthPath),
		auditLog:  cfg.AuditLogger,
		sessionID: cfg.SessionID,
	}
}

func (g *Governor) SetAdversarial(agent *AdversarialAgent) {
	g.adversarial = agent
}

func (g *Governor) Arbiter() *PolicyArbiter {
	return g.arbiter
}

func (g *Governor) Registry() *capability.Registry {
	return g.registry
}

func (g *Governor) Enforcer() *capability.Enforcer {
	return g.enforcer
}

func (g *Governor) Validator() *capability.CommandValidator {
	return g.validator
}

func (g *Governor) Executor() executor.Executor {
	return g.executor
}

func (g *Governor) ToolAuth() *ToolAuthorization {
	return g.auth
}

func (g *Governor) ReviewGoal(goal *types.Goal) (*ReviewResult, error) {
	return g.arbiter.ReviewGoal(goal)
}

func (g *Governor) AuthorizeTool(toolID string, scope string) error {
	return g.auth.RequestAuthorization(toolID, scope)
}

type ExecutionResult struct {
	StepResults []types.ExecutionResult
	Verdicts    []types.ArbiterVerdict
	RiskReport  *types.RiskReport
}

func (g *Governor) ExecuteGoal(ctx context.Context, graph *types.GoalGraph, plan *types.ActionPlan) (*ExecutionResult, error) {
	if g.executor == nil {
		return nil, fmt.Errorf("no executor configured")
	}

	var report *types.RiskReport
	if g.adversarial != nil {
		var err error
		report, err = g.adversarial.Run(ctx, graph, plan)
		if err != nil {
			return nil, fmt.Errorf("adversarial analysis: %w", err)
		}
	} else {
		report = &types.RiskReport{}
	}

	verdicts := g.arbiter.Decide(plan, report)

	result := &ExecutionResult{
		Verdicts:   verdicts,
		RiskReport: report,
	}

	verdictMap := make(map[string]types.ArbiterVerdict)
	for _, v := range verdicts {
		verdictMap[v.StepID] = v
	}

	for _, step := range plan.Steps {
		v, ok := verdictMap[step.ID]
		if !ok {
			continue
		}

		if v.Verdict == "blocked" {
			result.StepResults = append(result.StepResults, types.ExecutionResult{
				StepID:  step.ID,
				Success: false,
				Error:   fmt.Sprintf("blocked: %s", v.Reason),
			})
			continue
		}

		if v.Verdict == "modified" {
			result.StepResults = append(result.StepResults, types.ExecutionResult{
				StepID:  step.ID,
				Success: false,
				Error:   fmt.Sprintf("requires approval: %s", v.Reason),
			})
			continue
		}

		if g.validator != nil {
			vr := g.validator.Validate(step)
			if !vr.Valid {
				result.StepResults = append(result.StepResults, types.ExecutionResult{
					StepID:  step.ID,
					Success: false,
					Error:   vr.Reason,
				})
				continue
			}
		}

		execResult, execErr := g.executor.Execute(ctx, step)
		if execResult != nil {
			result.StepResults = append(result.StepResults, *execResult)
		}

		if g.auditLog != nil {
			_ = g.auditLog.Log(g.sessionID, "execution", step, execResult)
		}

		if execErr != nil {
			return result, execErr
		}
	}

	return result, nil
}

func (g *Governor) Name() string {
	return "governor"
}

func (g *Governor) Report(ctx context.Context, window time.Duration) (module.ModuleReport, error) {
	return module.ModuleReport{
		ModuleName: g.Name(),
		Window:     window,
		Metrics: map[string]any{
			"autonomy_level": g.arbiter.AutonomyLevel(),
			"directives":     len(g.arbiter.Directives()),
			"capabilities":   len(g.registry.List()),
			"authorizations": len(g.auth.ListAuthorizations()),
		},
	}, nil
}

func (g *Governor) Tune(ctx context.Context, change module.TuningChange) error {
	switch change.Path {
	case "autonomy_level":
		level, ok := change.Value.(int)
		if !ok {
			return fmt.Errorf("autonomy_level must be int")
		}
		if level < 0 || level > 4 {
			return fmt.Errorf("autonomy_level must be 0-4, got %d", level)
		}
		g.arbiter = NewArbiterWithLevel(level)
		return nil
	default:
		return fmt.Errorf("unknown tuning path: %q", change.Path)
	}
}

func (g *Governor) Health() module.ModuleHealth {
	return module.ModuleHealth{
		Status:    "healthy",
		Details:   fmt.Sprintf("autonomy=%d, directives=%d", g.arbiter.AutonomyLevel(), len(g.arbiter.Directives())),
		Timestamp: time.Now(),
	}
}

func (g *Governor) Capabilities() []string {
	return []string{
		"read:file",
		"execute:shell",
		"execute:git",
		"execute:package",
		"execute:process",
		"execute:display",
		"execute:config",
		"execute:network",
		"execute:schedule",
		"network:outbound",
		"ui:open",
	}
}

var _ module.Module = (*Governor)(nil)
