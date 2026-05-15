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

// resumePrefix is the intent prefix used to signal a session resume.
const resumePrefix = "__resume__:"

// NewPipelineRunner returns a PipelineRunner closure suitable for use by the TUI.
// Each call to the returned function runs the full agent pipeline for one intent.
//
// If the intent starts with "__resume__:<sessionID>", the runner opens the
// existing plan doc for that session and resumes from the last completed step
// instead of running the full pipeline from scratch.
func NewPipelineRunner(rc RunnerConfig) PipelineRunner {
	return func(intent, _ string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		client := llm.NewClient(rc.APIKey)

		// ── Resume path ───────────────────────────────────────────────────────
		if strings.HasPrefix(intent, resumePrefix) {
			sessionID := strings.TrimPrefix(intent, resumePrefix)
			return resumeFromPlanDoc(ctx, sessionID, rc, client)
		}

		// ── Normal path ───────────────────────────────────────────────────────
		sessionID := uuid.New().String()[:8]
		ws := gatherWorkspaceContext(rc.WorkspaceDir)

		planPath := rc.DataPathFn(filepath.Join("plans", sessionID+".md"))
		planWriter, err := plan.NewWriter(planPath, sessionID, intent)
		if err != nil {
			return fmt.Errorf("create plan doc: %w", err)
		}

		state := session.NewState(sessionID, intent, ws)
		state.PlanDocPath = planPath
		state.AutonomyLevel = rc.AutonomyLevel
		if err := rc.SessionMgr.Save(state); err != nil {
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

// resumeFromPlanDoc loads an existing plan doc and re-enters the pipeline
// starting from the first incomplete stage or step.
func resumeFromPlanDoc(ctx context.Context, sessionID string, rc RunnerConfig, client llm.Completer) error {
	dataDir := rc.DataPathFn("")
	planPath := filepath.Join(dataDir, "plans", sessionID+".md")

	planState, err := plan.ParsePlanDoc(planPath)
	if err != nil {
		return fmt.Errorf("resume: parse plan doc for %s: %w", sessionID, err)
	}

	if planState.Status == plan.StatusCompleted {
		return fmt.Errorf("session %s is already completed", sessionID)
	}

	// Load session state to get the original intent
	sessState, err := rc.SessionMgr.Load(sessionID)
	if err != nil {
		return fmt.Errorf("resume: load session state for %s: %w", sessionID, err)
	}

	// Open plan doc for appending (not creating fresh)
	planWriter, err := plan.OpenWriter(planPath, sessionID, sessState.Intent, planState.Attempt)
	if err != nil {
		return fmt.Errorf("resume: open plan doc: %w", err)
	}

	ws := gatherWorkspaceContext(rc.WorkspaceDir)

	pub := func(kind bus.EventKind, payload any) {
		if rc.EventBus != nil {
			rc.EventBus.Publish(bus.Event{
				Kind:      kind,
				SessionID: sessionID,
				Payload:   payload,
				Timestamp: time.Now(),
			})
		}
	}

	pub(bus.EventKind("session:resuming"), fmt.Sprintf("Resuming session %s from last checkpoint", sessionID))

	// Determine the first incomplete stage
	nextStage := planState.NextPendingStage()

	// If all stages are complete, we were interrupted during execution —
	// we need the plan to know which steps to skip. Re-run the pipeline
	// with skip logic based on the plan state.
	if nextStage == "" {
		// All LLM stages done — only execution is incomplete.
		// We re-run the full pipeline but pass the planState so execution
		// skips already-completed steps.
		return runPipeline(ctx, pipelineArgs{
			sessionID:     sessionID,
			intent:        sessState.Intent,
			ws:            ws,
			autonomyLevel: sessState.AutonomyLevel,
			executorType:  "local",
			client:        client,
			registry:      rc.Registry,
			validator:     rc.Validator,
			manifestStore: rc.Manifest,
			logger:        rc.AuditLogger,
			planWriter:    planWriter,
			sessionMgr:    rc.SessionMgr,
			state:         sessState,
			eventBus:      rc.EventBus,
			hypCfg:        rc.Config,
			dataPathFn:    rc.DataPathFn,
			attempt:       planState.Attempt,
			resumeState:   planState,
		})
	}

	// One or more LLM stages didn't complete — re-run from the first
	// incomplete stage. For simplicity in v1, re-run from the beginning
	// (stages are fast and idempotent when appending to the plan doc).
	pub(bus.EventKind("session:resuming"), fmt.Sprintf("Re-running from stage: %s", nextStage))

	return runPipeline(ctx, pipelineArgs{
		sessionID:     sessionID,
		intent:        sessState.Intent,
		ws:            ws,
		autonomyLevel: sessState.AutonomyLevel,
		executorType:  "local",
		client:        client,
		registry:      rc.Registry,
		validator:     rc.Validator,
		manifestStore: rc.Manifest,
		logger:        rc.AuditLogger,
		planWriter:    planWriter,
		sessionMgr:    rc.SessionMgr,
		state:         sessState,
		eventBus:      rc.EventBus,
		hypCfg:        rc.Config,
		dataPathFn:    rc.DataPathFn,
		attempt:       planState.Attempt,
		resumeState:   planState,
	})
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
	// resumeState is non-nil when resuming a halted session.
	// Pipeline stages and execution steps already marked completed in
	// resumeState are skipped without re-running.
	resumeState   *plan.PlanState
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

	// stageComplete returns true if we're resuming and this stage already finished.
	stageComplete := func(stage string) bool {
		return a.resumeState != nil && a.resumeState.IsStageComplete(stage)
	}
	// stepComplete returns true if we're resuming and this step already finished.
	stepComplete := func(stepID string) bool {
		if a.resumeState == nil {
			return false
		}
		ss, ok := a.resumeState.Steps[stepID]
		return ok && (ss.Status == "completed" || ss.Status == "skipped")
	}

	// ── Intent Agent ──────────────────────────────────────────────────────────
	var graph *types.GoalGraph
	if stageComplete("intent") {
		pub(bus.EventKind("stage:skipped"), "intent (already completed)")
		// We need the graph to feed the planner — re-run intent agent even on
		// resume if we can't read the prior output. Intent is cheap and idempotent.
		graph = &types.GoalGraph{Goals: a.state.Goals}
	} else {
		_ = a.planWriter.WriteStageStart("intent")
		var err error
		graph, err = agents.NewIntentAgent(a.client).Run(ctx, a.intent, a.ws)
		if err != nil {
			_ = a.planWriter.WriteStageFailed("intent", err)
			pub(bus.EventPlanFailed, err.Error())
			return err
		}
		_ = a.planWriter.WriteStageComplete("intent", marshalJSON(graph), "hyperi-intent")
		_ = a.logger.Log(a.sessionID, "intent", a.intent, graph)
		a.state.Goals = graph.Goals
	}

	// ── Planner Agent ─────────────────────────────────────────────────────────
	var agentPlan *types.ActionPlan
	if stageComplete("plan") && a.state.Plan != nil {
		pub(bus.EventKind("stage:skipped"), "plan (already completed)")
		agentPlan = a.state.Plan
	} else {
		_ = a.planWriter.WriteStageStart("plan")
		var err error
		agentPlan, err = agents.NewPlannerAgent(a.client).Run(ctx, graph)
		if err != nil {
			_ = a.planWriter.WriteStageFailed("plan", err)
			pub(bus.EventPlanFailed, err.Error())
			return err
		}
		_ = a.planWriter.WriteStageComplete("plan", marshalJSON(agentPlan), "hyperi-plan")
		_ = a.logger.Log(a.sessionID, "planner", graph, agentPlan)
		a.state.Plan = agentPlan
	}

	pub(bus.EventKind("plan:ready"), agentPlan)

	// ── Adversarial Agent ─────────────────────────────────────────────────────
	var report *types.RiskReport
	if stageComplete("adversarial") {
		pub(bus.EventKind("stage:skipped"), "adversarial (already completed)")
		report = &types.RiskReport{} // empty report — risk was already assessed
	} else {
		_ = a.planWriter.WriteStageStart("adversarial")
		var err error
		report, err = agents.NewAdversarialAgent(a.client).Run(ctx, graph, agentPlan)
		if err != nil {
			_ = a.planWriter.WriteStageFailed("adversarial", err)
			pub(bus.EventPlanFailed, err.Error())
			return err
		}
		_ = a.planWriter.WriteStageComplete("adversarial", marshalJSON(report), "hyperi-risk")
		_ = a.logger.Log(a.sessionID, "adversarial", agentPlan, report)
	}

	// ── Arbiter ───────────────────────────────────────────────────────────────
	var verdicts []types.ArbiterVerdict
	if stageComplete("arbiter") {
		pub(bus.EventKind("stage:skipped"), "arbiter (already completed)")
		// Re-run arbiter deterministically — it has no LLM cost and its output
		// is needed to know which steps require approval.
		policyArbiter := arbiter.NewWithLevel(a.autonomyLevel)
		verdicts = policyArbiter.Decide(agentPlan, report)
	} else {
		_ = a.planWriter.WriteStageStart("arbiter")
		policyArbiter := arbiter.NewWithLevel(a.autonomyLevel)
		verdicts = policyArbiter.Decide(agentPlan, report)
		_ = a.logger.Log(a.sessionID, "arbiter", agentPlan, verdicts)
		for _, step := range agentPlan.Steps {
			v := findVerdict(verdicts, step.ID)
			if v != nil {
				_ = a.planWriter.WriteStepVerdict(step, v.Verdict, v.Reason)
			}
		}
		_ = a.planWriter.WriteStageComplete("arbiter", marshalJSON(verdicts), "hyperi-arbiter")
	}

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
		// On resume: skip steps that already completed or were skipped.
		if stepComplete(step.ID) {
			pub(bus.EventStepSkipped, fmt.Sprintf("%s (already completed)", step.ID))
			continue
		}

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

	// Build a compact summary of outcomes for the LLM — always, even if all steps
	// were skipped (e.g. "Chrome not found" is a valid answer to "what version of Chrome?")
	var sb strings.Builder
	sb.WriteString("Original intent: ")
	sb.WriteString(intent)
	sb.WriteString("\n\nStep outcomes:\n")

	for _, r := range results {
		sb.WriteString(fmt.Sprintf("\nStep [%s]: %s\n", r.step.ID, r.step.Description))
		switch {
		case r.skipped:
			// Strip internal validator boilerplate ("step %q: binary %q not found...") down
			// to just the actionable part so the LLM doesn't echo implementation details.
			reason := r.reason
			if idx := strings.Index(reason, "not found in PATH"); idx != -1 {
				reason = fmt.Sprintf("%q is not installed on this system", r.step.Command[0])
			} else if idx := strings.Index(reason, "is not on the execute:shell allowlist"); idx != -1 {
				reason = fmt.Sprintf("%q is not permitted by the capability allowlist", r.step.Command[0])
			} else if strings.HasPrefix(reason, "skipped: exit status") {
				reason = "command returned non-zero (package/binary not found)"
			}
			sb.WriteString(fmt.Sprintf("  Status: skipped — %s\n", reason))
		case r.failed:
			msg := ""
			if r.result != nil {
				msg = r.result.Error
			}
			sb.WriteString(fmt.Sprintf("  Status: failed — %s\n", msg))
		case r.result != nil && r.result.Output != "":
			out := r.result.Output
			if len(out) > 4000 {
				out = out[:4000] + "\n... (truncated)"
			}
			sb.WriteString(fmt.Sprintf("  Output:\n%s\n", out))
		default:
			sb.WriteString("  Status: completed (no output)\n")
		}
	}

	system := `You are the response formatter for HyperiOS, an AI-driven OS agent.
The agent has just executed a series of steps to fulfil a user's intent.
Your job is to write a concise, direct answer to the user based on the step outcomes.

Rules:
- Answer the original intent directly and completely — even if all steps were skipped or failed.
- If nothing was found (e.g. a package is not installed), say so plainly: "Chrome is not installed on this system."
- Present data (numbers, tables, lists) clearly — use plain text, not markdown headers or bullet symbols.
- Do not narrate what the agent did ("I ran dpkg..."). Just give the answer.
- Do not expose internal error messages or validator details to the user.
- Keep it short: 1–6 lines for simple questions, longer only if the data requires it.`

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
