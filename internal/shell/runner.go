package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/isellar/hyperios/internal/agents"
	"github.com/isellar/hyperios/internal/arbiter"
	"github.com/isellar/hyperios/internal/audit"
	"github.com/isellar/hyperios/internal/bus"
	"github.com/isellar/hyperios/internal/capability"
	cfg "github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/executor"
	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/manifest"
	"github.com/isellar/hyperios/internal/plan"
	"github.com/isellar/hyperios/internal/session"
	"github.com/isellar/hyperios/internal/types"
)

const maxReplans = 2

// RunnerConfig holds all dependencies the pipeline runner needs.
type RunnerConfig struct {
	APIKey        string
	AutonomyLevel int
	ExecutorType  string
	EventBus      *bus.Bus
	Registry      *capability.Registry
	Validator     *capability.CommandValidator
	Manifest      *manifest.Store
	SessionMgr    *session.Manager
	AuditLogger   *audit.Logger
	Config        *cfg.Config
	DataPathFn    func(string) string
	WorkspaceDir  string
}

// NewPipelineRunner returns a PipelineRunner closure suitable for use by the TUI.
// Each call to the returned function runs the full agent pipeline for one intent.
func NewPipelineRunner(rc RunnerConfig) PipelineRunner {
	return func(intent, _ string) error {
		sessionID := uuid.New().String()[:8]
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		client := llm.NewClient(rc.APIKey)
		ws := gatherWorkspaceContext(rc.WorkspaceDir)

		// Create plan document
		planPath := rc.DataPathFn(filepath.Join("plans", sessionID+".md"))
		planWriter, err := plan.NewWriter(planPath, sessionID, intent)
		if err != nil {
			return fmt.Errorf("create plan doc: %w", err)
		}

		// Save thin session index
		state := session.NewState(sessionID, intent, ws)
		state.PlanDocPath = planPath
		state.AutonomyLevel = rc.AutonomyLevel
		if err := rc.SessionMgr.Save(state); err != nil {
			// Non-fatal
			fmt.Fprintf(os.Stderr, "Warning: failed to save session: %v\n", err)
		}

		return runPipeline(ctx, pipelineArgs{
			sessionID:     sessionID,
			intent:        intent,
			ws:            ws,
			autonomyLevel: rc.AutonomyLevel,
			executorType:  rc.ExecutorType,
			client:        client,
			registry:      rc.Registry,
			validator:     rc.Validator,
			manifestStore: rc.Manifest,
			logger:        rc.AuditLogger,
			planWriter:    planWriter,
			sessionMgr:    rc.SessionMgr,
			state:         state,
			eventBus:      rc.EventBus,
			hypCfg:        rc.Config,
			dataPathFn:    rc.DataPathFn,
			attempt:       1,
		})
	}
}

type pipelineArgs struct {
	sessionID     string
	intent        string
	ws            types.WorkspaceContext
	autonomyLevel int
	executorType  string
	client        llm.Completer
	registry      *capability.Registry
	validator     *capability.CommandValidator
	manifestStore *manifest.Store
	logger        *audit.Logger
	planWriter    *plan.Writer
	sessionMgr    *session.Manager
	state         *session.State
	eventBus      *bus.Bus
	hypCfg        *cfg.Config
	dataPathFn    func(string) string
	attempt       int
}

func runPipeline(ctx context.Context, a pipelineArgs) error {
	pub := func(kind bus.EventKind, payload any) {
		if a.eventBus != nil {
			a.eventBus.Publish(bus.Event{
				Kind:      kind,
				SessionID: a.sessionID,
				Payload:   payload,
				Timestamp: time.Now(),
			})
		}
	}

	// ── Intent Agent ──────────────────────────────────────────────────────────
	_ = a.planWriter.WriteStageStart("intent")
	graph, err := agents.NewIntentAgent(a.client).Run(ctx, a.intent, a.ws)
	if err != nil {
		_ = a.planWriter.WriteStageFailed("intent", err)
		pub(bus.EventPlanFailed, err.Error())
		return err
	}
	_ = a.planWriter.WriteStageComplete("intent", marshalJSON(graph), "hyperi-intent")
	_ = a.logger.Log(a.sessionID, "intent", a.intent, graph)
	a.state.Goals = graph.Goals

	// ── Planner Agent ─────────────────────────────────────────────────────────
	_ = a.planWriter.WriteStageStart("plan")
	agentPlan, err := agents.NewPlannerAgent(a.client).Run(ctx, graph)
	if err != nil {
		_ = a.planWriter.WriteStageFailed("plan", err)
		pub(bus.EventPlanFailed, err.Error())
		return err
	}
	_ = a.planWriter.WriteStageComplete("plan", marshalJSON(agentPlan), "hyperi-plan")
	_ = a.logger.Log(a.sessionID, "planner", graph, agentPlan)
	a.state.Plan = agentPlan

	// Publish plan summary to TUI
	pub(bus.EventKind("plan:ready"), agentPlan)

	// ── Adversarial Agent ─────────────────────────────────────────────────────
	_ = a.planWriter.WriteStageStart("adversarial")
	report, err := agents.NewAdversarialAgent(a.client).Run(ctx, graph, agentPlan)
	if err != nil {
		_ = a.planWriter.WriteStageFailed("adversarial", err)
		pub(bus.EventPlanFailed, err.Error())
		return err
	}
	_ = a.planWriter.WriteStageComplete("adversarial", marshalJSON(report), "hyperi-risk")
	_ = a.logger.Log(a.sessionID, "adversarial", agentPlan, report)

	// ── Arbiter ───────────────────────────────────────────────────────────────
	_ = a.planWriter.WriteStageStart("arbiter")
	policyArbiter := arbiter.NewWithLevel(a.autonomyLevel)
	verdicts := policyArbiter.Decide(agentPlan, report)
	_ = a.logger.Log(a.sessionID, "arbiter", agentPlan, verdicts)

	for _, step := range agentPlan.Steps {
		v := findVerdict(verdicts, step.ID)
		if v != nil {
			_ = a.planWriter.WriteStepVerdict(step, v.Verdict, v.Reason)
		}
	}
	_ = a.planWriter.WriteStageComplete("arbiter", marshalJSON(verdicts), "hyperi-arbiter")

	// Publish verdicts to TUI for inline plan display
	pub(bus.EventKind("plan:verdicts"), &planVerdicts{
		Plan:     agentPlan,
		Verdicts: verdicts,
		Report:   report,
	})

	// ── Execute ───────────────────────────────────────────────────────────────
	if a.autonomyLevel == 0 {
		pub(bus.EventPlanCompleted, "plan presented as suggestion (autonomy level 0)")
		_ = a.planWriter.Finalize(plan.StatusCompleted)
		a.state.Status = "completed"
		_ = a.sessionMgr.Save(a.state)
		return nil
	}

	execCfg := executor.ExecutorConfig{
		Registry:     a.registry,
		Workspace:    a.ws.Cwd,
		ExecutorType: types.ExecutorType(a.executorType),
		Bus:          a.eventBus,
		SessionID:    a.sessionID,
	}
	execInstance := executor.New(execCfg)

	replanStepID := ""

	for _, step := range agentPlan.Steps {
		v := findVerdict(verdicts, step.ID)
		if v == nil {
			continue
		}

		if v.Verdict == "blocked" {
			_ = a.planWriter.WriteStepSkipped(step, "blocked by arbiter")
			continue
		}

		if v.Verdict == "modified" {
			// Publish approval request to TUI via event bus
			replyCh := make(chan bool, 1)
			timeout := a.hypCfg.ApprovalTimeoutForeground
			ap := &bus.ApprovalPayload{
				StepID:         step.ID,
				StepDesc:       step.Description,
				Command:        step.Command,
				Reason:         v.Reason,
				TimeoutSeconds: timeout,
				ReplyCh:        replyCh,
			}
			pub(bus.EventApprovalNeeded, ap)

			// Block waiting for reply
			select {
			case approved := <-replyCh:
				_ = a.planWriter.WriteStepApproval(step.ID, approved, "user decision")
				if !approved {
					_ = a.planWriter.Finalize(plan.StatusHalted)
					a.state.Status = "halted"
					_ = a.sessionMgr.Save(a.state)
					pub(bus.EventPlanFailed, fmt.Sprintf("user denied approval for step %s", step.ID))
					return fmt.Errorf("user denied approval for step %s", step.ID)
				}
			case <-time.After(time.Duration(timeout) * time.Second):
				_ = a.planWriter.WriteStepApproval(step.ID, false, "timed out")
				_ = a.planWriter.Finalize(plan.StatusHalted)
				a.state.Status = "halted"
				_ = a.sessionMgr.Save(a.state)
				pub(bus.EventPlanFailed, "approval-timeout")
				return fmt.Errorf("approval timed out for step %s", step.ID)
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		if vr := a.validator.Validate(step); !vr.Valid {
			_ = a.planWriter.WriteStepSkipped(step, vr.Reason)
			continue
		}

		_ = a.planWriter.WriteStepStart(step)
		result, execErr := execInstance.Execute(ctx, step)
		_ = a.planWriter.WriteStepResult(step, result)
		_ = a.logger.Log(a.sessionID, "execution", step, result)

		if execErr == executor.ErrStepSkipped {
			continue
		}
		if execErr == executor.ErrReplan {
			replanStepID = step.ID
			break
		}
		if execErr != nil {
			_ = a.planWriter.Finalize(plan.StatusHalted)
			a.state.Status = "halted"
			_ = a.sessionMgr.Save(a.state)
			pub(bus.EventPlanFailed, execErr.Error())
			return execErr
		}

		if result.Success {
			a.state.MarkCompleted(step.ID)
			_ = a.sessionMgr.Save(a.state)
			if a.manifestStore != nil {
				a.manifestStore.PostExecutionHook(step.Command)
				_ = a.manifestStore.Save()
			}
		}
	}

	if replanStepID != "" && a.attempt <= maxReplans {
		requiresConfirmation := a.attempt >= maxReplans
		_ = a.planWriter.WriteReplanHeader(a.attempt, replanStepID, a.attempt+1, requiresConfirmation)

		if requiresConfirmation {
			replyCh := make(chan bool, 1)
			ap := &bus.ApprovalPayload{
				StepID:         "replan",
				StepDesc:       fmt.Sprintf("Attempt %d of %d failed. Proceed with re-plan?", a.attempt+1, maxReplans+1),
				Reason:         "re-plan budget: user confirmation required",
				TimeoutSeconds: a.hypCfg.ApprovalTimeoutForeground,
				ReplyCh:        replyCh,
			}
			pub(bus.EventApprovalNeeded, ap)
			select {
			case approved := <-replyCh:
				if !approved {
					_ = a.planWriter.Finalize(plan.StatusHalted)
					a.state.Status = "halted"
					_ = a.sessionMgr.Save(a.state)
					return fmt.Errorf("user declined re-plan at attempt %d", a.attempt)
				}
			case <-time.After(time.Duration(a.hypCfg.ApprovalTimeoutForeground) * time.Second):
				return fmt.Errorf("re-plan confirmation timed out")
			}
		}

		a.attempt++
		return runPipeline(ctx, a)
	}

	_ = a.planWriter.Finalize(plan.StatusCompleted)
	a.state.Status = "completed"
	_ = a.sessionMgr.Save(a.state)
	pub(bus.EventPlanCompleted, "all steps completed")
	return nil
}

func findVerdict(verdicts []types.ArbiterVerdict, stepID string) *types.ArbiterVerdict {
	for i := range verdicts {
		if verdicts[i].StepID == stepID {
			return &verdicts[i]
		}
	}
	return nil
}

// planVerdicts is the payload for the plan:verdicts bus event.
type planVerdicts struct {
	Plan     *types.ActionPlan
	Verdicts []types.ArbiterVerdict
	Report   *types.RiskReport
}

func gatherWorkspaceContext(cwd string) types.WorkspaceContext {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return types.WorkspaceContext{
		Cwd:       cwd,
		GitBranch: runGit(cwd, "rev-parse", "--abbrev-ref", "HEAD"),
		GitLog:    runGit(cwd, "log", "--oneline", "-5"),
		GitStatus: runGit(cwd, "status", "--short"),
	}
}

func runGit(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "(unavailable)"
	}
	return strings.TrimSpace(string(out))
}

func marshalJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("{\"error\": %q}", err.Error())
	}
	return string(data)
}
