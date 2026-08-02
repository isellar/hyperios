package self_improvement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/isellar/hyperios/internal/audit"
	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/module"
	"github.com/isellar/hyperios/internal/types"
)

// GoalSubmitter is an interface for submitting improvement goals back into the
// goal-fulfillment pipeline. Defined here to avoid a circular import.
type GoalSubmitter interface {
	SubmitGoal(description string) (*types.Goal, error)
}

// DirectiveStore is the narrow interface SelfImprovement uses to persist
// standing directives it derives from analysis. *memory.Memory implements
// this; defined here (rather than importing internal/memory directly) to
// avoid a circular import and to keep SelfImprovement's dependency surface
// minimal and mockable.
type DirectiveStore interface {
	AddDirective(d types.Directive) error
}

// directiveIDFromDescription derives a stable ID for a learned directive from
// its description text, so that the analyzer re-suggesting the same rule
// across multiple Analyze() cycles overwrites the existing entry (via
// Memory.AddDirective's overwrite-on-same-ID semantics) instead of
// accumulating duplicate directives with different random IDs.
func directiveIDFromDescription(description string) string {
	sum := sha256.Sum256([]byte(description))
	return "learned-" + hex.EncodeToString(sum[:])[:12]
}

// SelfImprovement analyses accumulated execution results and generates
// improvement goals that are re-submitted as user-level goals.
//
// It implements module.Module so it can participate in the self-tuning loop.
type SelfImprovement struct {
	mu sync.Mutex

	cfg      *config.Config
	analyzer *Analyzer
	stats    *Stats
	audit    *audit.Logger

	goalFulfillment GoalSubmitter
	directiveStore  DirectiveStore

	// Buffered results waiting to be analysed.
	pending []GoalResult

	// Session ID for audit entries.
	sessionID string

	// lastError holds the most recent Analyze() error for Health() reporting.
	lastError error
}

// NewSelfImprovement creates a SelfImprovement instance.
// Call SetGoalFulfillment before calling Analyze.
//
// A nil llmClient is accepted: Analyze() will return an error rather than
// panic when no LLM is configured.
func NewSelfImprovement(cfg *config.Config, llmClient llm.Completer, auditLogger *audit.Logger) *SelfImprovement {
	stats := NewStats()
	return &SelfImprovement{
		cfg:       cfg,
		analyzer:  NewAnalyzer(llmClient, stats),
		stats:     stats,
		audit:     auditLogger,
		sessionID: fmt.Sprintf("si-%d", time.Now().UnixNano()),
	}
}

// SetGoalFulfillment wires the GoalSubmitter dependency (breaks the circular
// import that would arise from depending on goal_fulfillment directly).
func (si *SelfImprovement) SetGoalFulfillment(gf GoalSubmitter) {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.goalFulfillment = gf
}

// SetDirectiveStore wires the DirectiveStore dependency used to persist
// standing directives derived from analysis. Optional: if unset, Analyze
// still submits improvement goals but silently skips directive persistence.
func (si *SelfImprovement) SetDirectiveStore(ds DirectiveStore) {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.directiveStore = ds
}

// RecordResult stores a goal result in the pending buffer and updates stats.
func (si *SelfImprovement) RecordResult(result GoalResult) {
	si.mu.Lock()
	defer si.mu.Unlock()

	si.pending = append(si.pending, result)
	si.stats.RecordGoalResultWithDescription(
		result.GoalID,
		result.Description,
		result.Success,
		0, // duration not tracked at this layer
		result.ErrorMsg,
	)
}

// Analyze runs the full analysis cycle:
//  1. Drains the pending result buffer.
//  2. Calls the LLM analyzer.
//  3. Submits each one-off improvement goal to GoalFulfillment, AND/OR
//     persists each standing directive to the DirectiveStore — whichever
//     the analyzer determined applies (see analyzerSystem's prompt: it may
//     return goals only, directives only, both, or neither).
//  4. Logs each submitted goal / persisted directive via the audit logger.
//
// Returns an error if analysis fails or no GoalSubmitter is configured.
// Partial errors (individual goal-submission or directive-persistence
// failures) are collected and returned as a combined error so that a single
// failure does not abort the whole cycle.
func (si *SelfImprovement) Analyze() error {
	si.mu.Lock()
	results := si.pending
	si.pending = nil
	gf := si.goalFulfillment
	ds := si.directiveStore
	si.mu.Unlock()

	if gf == nil {
		err := fmt.Errorf("self_improvement: no GoalSubmitter configured; call SetGoalFulfillment first")
		si.mu.Lock()
		si.lastError = err
		si.mu.Unlock()
		return err
	}

	if len(results) == 0 {
		return nil
	}

	analysis, err := si.analyzer.AnalyzeResults(results)
	if err != nil {
		si.mu.Lock()
		si.lastError = err
		si.mu.Unlock()
		return fmt.Errorf("self_improvement: analyze: %w", err)
	}

	var errs []error

	// One-off improvement goals — re-submitted as regular goals, run once.
	for _, goalDesc := range analysis.ImprovementGoals {
		if goalDesc == "" {
			continue
		}

		goal, submitErr := gf.SubmitGoal(goalDesc)
		if submitErr != nil {
			errs = append(errs, fmt.Errorf("submit improvement goal %q: %w", goalDesc, submitErr))
			continue
		}

		if si.audit != nil {
			_ = si.audit.LogSelfImprovementGoal(si.sessionID, audit.SelfImprovementGoal{
				GoalID:      goal.ID,
				Description: goalDesc,
				Source:      "self_improvement:analyze",
				Confidence:  si.stats.SuccessRate(),
			})
		}
	}

	// Code changes — submitted as goals for the agent to carry out using
	// the shell + self_modify tool loop. The agent edits source files, runs
	// self_modify verify, and applies if it passes. No human involvement.
	// Prefixed with "[CODE CHANGE]" so the agent (and logs) can distinguish
	// these from ordinary environment-level improvement goals.
	for _, changeDesc := range analysis.CodeChanges {
		if changeDesc == "" {
			continue
		}

		goalDesc := "[CODE CHANGE] " + changeDesc
		goal, submitErr := gf.SubmitGoal(goalDesc)
		if submitErr != nil {
			errs = append(errs, fmt.Errorf("submit code change goal %q: %w", changeDesc, submitErr))
			continue
		}

		if si.audit != nil {
			_ = si.audit.LogSelfImprovementGoal(si.sessionID, audit.SelfImprovementGoal{
				GoalID:      goal.ID,
				Description: goalDesc,
				Source:      "self_improvement:analyze:code_change",
				Confidence:  si.stats.SuccessRate(),
			})
		}
	}

	// Standing directives — persisted so every future goal's prompt includes
	// them (see Processor.RunNext -> AgentSpawner). Not mutually exclusive
	// with the above; the analyzer may return any combination.
	if ds != nil {
		for _, sugg := range analysis.Directives {
			if sugg.Description == "" {
				continue
			}

			d := types.Directive{
				ID:          directiveIDFromDescription(sugg.Description),
				Priority:    sugg.Priority,
				Description: sugg.Description,
				Immutable:   false, // learned directives are always user-revisable
			}
			if addErr := ds.AddDirective(d); addErr != nil {
				errs = append(errs, fmt.Errorf("persist directive %q: %w", sugg.Description, addErr))
				continue
			}

			if si.audit != nil {
				_ = si.audit.LogSelfImprovementGoal(si.sessionID, audit.SelfImprovementGoal{
					GoalID:      d.ID,
					Description: sugg.Description,
					Source:      "self_improvement:analyze:directive",
					Confidence:  si.stats.SuccessRate(),
				})
			}
		}
	}

	si.mu.Lock()
	if len(errs) > 0 {
		si.lastError = combineErrors(errs)
	} else {
		si.lastError = nil
	}
	si.mu.Unlock()

	if len(errs) > 0 {
		return combineErrors(errs)
	}
	return nil
}

// combineErrors joins multiple errors into one.
func combineErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	msg := "self_improvement errors:"
	for _, e := range errs {
		msg += "\n  - " + e.Error()
	}
	return fmt.Errorf("%s", msg)
}

// ── module.Module implementation ─────────────────────────────────────────────

// Name returns the module identifier.
func (si *SelfImprovement) Name() string { return "self_improvement" }

// Report returns a ModuleReport with the current stats summary as metrics.
func (si *SelfImprovement) Report(_ context.Context, window time.Duration) (module.ModuleReport, error) {
	summary := si.stats.Summary()
	patterns := si.stats.FailurePatterns()

	var issues []string
	for _, p := range patterns {
		issues = append(issues, fmt.Sprintf("recurring failure: %s", p))
	}

	return module.ModuleReport{
		ModuleName: "self_improvement",
		Window:     window,
		Metrics: map[string]any{
			"total_goals":        summary.TotalGoals,
			"success_count":      summary.SuccessCount,
			"failure_count":      summary.FailureCount,
			"success_rate":       summary.SuccessRate,
			"avg_duration_ms":    summary.AvgDurationMs,
			"total_tool_calls":   summary.TotalToolCalls,
			"unauthorized_calls": summary.UnauthorizedCalls,
			"pending_results":    len(si.pending),
		},
		Issues: issues,
	}, nil
}

// Tune accepts tuning changes. Reserved for future parameter adjustment.
func (si *SelfImprovement) Tune(_ context.Context, change module.TuningChange) error {
	if change.Module != "self_improvement" {
		return fmt.Errorf("self_improvement: wrong module: %s", change.Module)
	}
	// No tunable parameters in v1.
	return fmt.Errorf("self_improvement: unknown tuning path: %s", change.Path)
}

// Health returns the current health of the module.
func (si *SelfImprovement) Health() module.ModuleHealth {
	si.mu.Lock()
	lastErr := si.lastError
	si.mu.Unlock()

	if lastErr != nil {
		return module.ModuleHealth{
			Status:    "degraded",
			Details:   lastErr.Error(),
			Timestamp: time.Now(),
		}
	}
	return module.ModuleHealth{
		Status:    "healthy",
		Details:   "self-improvement module operational",
		Timestamp: time.Now(),
	}
}

// Capabilities returns the capability types this module requires.
func (si *SelfImprovement) Capabilities() []string {
	return []string{"network:outbound"} // LLM calls
}

// Compile-time assertion that SelfImprovement satisfies module.Module.
var _ module.Module = (*SelfImprovement)(nil)
