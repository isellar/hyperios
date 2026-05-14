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

	// stepResults collects outcomes for the response synthesis stage.
	var stepResults []stepResult

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
			// Publish so the TUI shows why the step was skipped
			pub(bus.EventStepSkipped, vr.Reason)
			stepResults = append(stepResults, stepResult{step: step, skipped: true, reason: vr.Reason})
			continue
		}

		_ = a.planWriter.WriteStepStart(step)
		result, execErr := execInstance.Execute(ctx, step)
		_ = a.planWriter.WriteStepResult(step, result)
		_ = a.logger.Log(a.sessionID, "execution", step, result)

		if execErr == executor.ErrStepSkipped {
			stepResults = append(stepResults, stepResult{step: step, skipped: true, reason: result.Error})
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
			stepResults = append(stepResults, stepResult{step: step, result: result})
			a.state.MarkCompleted(step.ID)
			_ = a.sessionMgr.Save(a.state)
			if a.manifestStore != nil {
				a.manifestStore.PostExecutionHook(step.Command)
				_ = a.manifestStore.Save()
			}
		} else {
			stepResults = append(stepResults, stepResult{step: step, result: result, failed: true})
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

	// ── Response synthesis ────────────────────────────────────────────────────
	// Ask the LLM to summarise all step outputs into a plain-English answer
	// for the user, then publish it as a plan:response event for the TUI.
	responseText := synthesiseResponse(ctx, a.client, a.intent, stepResults)
	pub(bus.EventKind("plan:response"), responseText)
	_ = a.planWriter.WriteStageComplete("response", responseText, "hyperi-response")

	_ = a.planWriter.Finalize(plan.StatusCompleted)
	a.state.Status = "completed"
	_ = a.sessionMgr.Save(a.state)
	pub(bus.EventPlanCompleted, "all steps completed")
	return nil
}

// stepResult holds the outcome of a single executed step.
type stepResult struct {
	step    types.ActionStep
	result  *types.ExecutionResult
	skipped bool
	failed  bool
	reason  string // populated when skipped=true
}

// synthesiseResponse asks the LLM to turn raw step outputs into a concise,
// user-facing answer. Falls back to a plain-text summary if the LLM call fails.
func synthesiseResponse(ctx context.Context, client llm.Completer, intent string, results []stepResult) string {
	if len(results) == 0 {
		return "All steps were blocked or skipped — nothing was executed."
	}

	// Build a compact summary of outputs for the LLM
	var sb strings.Builder
	sb.WriteString("Original intent: ")
	sb.WriteString(intent)
	sb.WriteString("\n\nStep outputs:\n")

	anyOutput := false
	for _, r := range results {
		sb.WriteString(fmt.Sprintf("\nStep [%s]: %s\n", r.step.ID, r.step.Description))
		switch {
		case r.skipped:
			sb.WriteString("  Status: skipped — ")
			sb.WriteString(r.reason)
		case r.failed:
			sb.WriteString("  Status: failed — ")
			if r.result != nil {
				sb.WriteString(r.result.Error)
			}
		case r.result != nil && r.result.Output != "":
			sb.WriteString("  Output:\n")
			// Truncate very long outputs to keep the LLM prompt manageable
			out := r.result.Output
			if len(out) > 4000 {
				out = out[:4000] + "\n... (truncated)"
			}
			sb.WriteString(out)
			anyOutput = true
		default:
			sb.WriteString("  Status: completed (no output)")
		}
		sb.WriteString("\n")
	}

	// If there's literally nothing to summarise, skip the LLM call
	if !anyOutput {
		return buildFallbackResponse(results)
	}

	system := `You are the response formatter for HyperiOS, an AI-driven OS agent.
The agent has just executed a series of steps to fulfil a user's intent.
Your job is to write a concise, direct answer to the user based on the step outputs.

Rules:
- Answer the original intent directly and completely.
- Present data (numbers, tables, lists) clearly — use plain text formatting, not markdown headers.
- Do not narrate what the agent did ("I ran df -h..."). Just give the answer.
- If a step failed or was skipped, mention it briefly only if it affects the answer.
- Keep it short: 1–10 lines for simple questions, longer only if the data requires it.`

	response, err := client.CompleteWithRetry(ctx, system, sb.String())
	if err != nil {
		return buildFallbackResponse(results)
	}
	return strings.TrimSpace(response)
}

// buildFallbackResponse produces a minimal plain-text summary without an LLM call.
func buildFallbackResponse(results []stepResult) string {
	var sb strings.Builder
	for _, r := range results {
		switch {
		case r.skipped:
			sb.WriteString(fmt.Sprintf("[%s] skipped: %s\n", r.step.ID, r.reason))
		case r.failed && r.result != nil:
			sb.WriteString(fmt.Sprintf("[%s] failed: %s\n", r.step.ID, r.result.Error))
		case r.result != nil && r.result.Output != "":
			sb.WriteString(r.result.Output)
			if !strings.HasSuffix(r.result.Output, "\n") {
				sb.WriteString("\n")
			}
		default:
			sb.WriteString(fmt.Sprintf("[%s] completed\n", r.step.ID))
		}
	}
	return strings.TrimSpace(sb.String())
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
