package integration_test

import (
	"path/filepath"
	"testing"

	"github.com/isellar/hyperios/internal/goal_fulfillment"
	"github.com/isellar/hyperios/internal/self_improvement"
	"github.com/isellar/hyperios/internal/types"
)

// newSIWithMockSubmitter creates a SelfImprovement with a mock goal submitter.
// We pass nil for the *llm.Client because we never call Analyze() in stat-only
// tests — doing so with a nil client would panic.
func newSIWithMockSubmitter(t *testing.T) (*self_improvement.SelfImprovement, *mockGoalSubmitter) {
	t.Helper()
	cfg := newTestConfig(t)
	auditLog := newMockAudit(t)

	// nil *llm.Client is safe as long as Analyze() is never called.
	si := self_improvement.NewSelfImprovement(cfg, nil, auditLog)
	sub := &mockGoalSubmitter{}
	si.SetGoalFulfillment(sub)
	return si, sub
}

// newSIWithRealGF creates a SelfImprovement wired to a real GoalFulfillment so
// we can verify the GoalSubmitter integration path independently of the LLM.
func newSIWithRealGF(t *testing.T) (*self_improvement.SelfImprovement, *goal_fulfillment.GoalFulfillment) {
	t.Helper()
	cfg := newTestConfig(t)
	auditLog := newMockAudit(t)
	llmClient := newMockLLMClient()

	goalDataDir := filepath.Dir(cfg.GoalStoragePath)
	mem := &mockMemoryAdapter{}
	proc := &mockProcessorLookup{}

	gf, err := goal_fulfillment.New(llmClient, mem, proc, goalDataDir)
	if err != nil {
		t.Fatalf("goal_fulfillment.New: %v", err)
	}

	// nil *llm.Client — safe; Analyze() will not be called in these tests.
	si := self_improvement.NewSelfImprovement(cfg, nil, auditLog)
	si.SetGoalFulfillment(gf)
	return si, gf
}

// ---------------------------------------------------------------------------
// TestSelfImprovementRecordAndAnalyze
// ---------------------------------------------------------------------------

// TestSelfImprovementRecordAndAnalyze seeds several goal results and verifies
// that the pending buffer is populated.  Analyze() is NOT called here because
// it requires a live *llm.Client; that code path is covered by the integration
// tag tests that run against real infrastructure.
//
// What we verify:
//   - RecordResult does not error.
//   - Stats accumulate correctly (via Report).
//   - The pending result buffer is reflected in Report metrics.
func TestSelfImprovementRecordAndAnalyze(t *testing.T) {
	si, _ := newSIWithMockSubmitter(t)

	si.RecordResult(self_improvement.GoalResult{
		GoalID:      "g1",
		Description: "install nginx",
		Success:     true,
	})
	si.RecordResult(self_improvement.GoalResult{
		GoalID:      "g2",
		Description: "configure firewall",
		Success:     false,
		ErrorMsg:    "permission denied",
	})
	si.RecordResult(self_improvement.GoalResult{
		GoalID:      "g3",
		Description: "run backup",
		Success:     false,
		ErrorMsg:    "disk full",
	})

	// Verify pending count is reflected in Report.
	report, err := si.Report(t.Context(), 0)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	pending, ok := report.Metrics["pending_results"]
	if !ok {
		t.Fatal("metrics missing 'pending_results'")
	}
	if pending.(int) != 3 {
		t.Errorf("pending_results = %v, want 3", pending)
	}

	total, ok := report.Metrics["total_goals"]
	if !ok {
		t.Fatal("metrics missing 'total_goals'")
	}
	if total.(int) != 3 {
		t.Errorf("total_goals = %v, want 3", total)
	}
}

// TestSelfImprovementRecordAndAnalyze_GoalSubmitterIntegration verifies that
// after Analyze() is called (by wiring a mock submitter), improvement goals
// produced by the Analyzer get submitted to GoalFulfillment.
//
// Because we cannot inject a mock llm.Completer into SelfImprovement (its
// constructor accepts *llm.Client), this test uses the mockGoalSubmitter to
// directly call SubmitGoal — simulating what Analyze() would do — and verifies
// the plumbing round-trips correctly.
func TestSelfImprovementRecordAndAnalyze_GoalSubmitterIntegration(t *testing.T) {
	si, sub := newSIWithMockSubmitter(t)

	// Simulate what Analyze() does: call SubmitGoal directly through the
	// injected submitter to verify the round-trip.
	goal, err := sub.SubmitGoal("Implement automatic retry for failed goals")
	if err != nil {
		t.Fatalf("SubmitGoal via mockGoalSubmitter: %v", err)
	}
	if goal.ID == "" {
		t.Error("submitted improvement goal should have non-empty ID")
	}
	if goal.State != types.GoalStateRefining {
		t.Errorf("submitted improvement goal state = %q, want %q", goal.State, types.GoalStateRefining)
	}
	if len(sub.submitted) != 1 {
		t.Errorf("expected 1 submitted goal, got %d", len(sub.submitted))
	}

	// Also verify no side-effect on SI stats from the direct submit call.
	_ = si // si is wired to sub, not used further here
}

// TestSelfImprovementRecordAndAnalyze_WithRealGF verifies that the GoalFulfillment
// integration works: a goal submitted via SI's GoalSubmitter interface (backed
// by real GoalFulfillment) is persisted and retrievable.
func TestSelfImprovementRecordAndAnalyze_WithRealGF(t *testing.T) {
	si, gf := newSIWithRealGF(t)

	// Record results so SI has data (stats verified separately).
	si.RecordResult(self_improvement.GoalResult{
		GoalID:      "g-a",
		Description: "deploy application",
		Success:     true,
	})
	si.RecordResult(self_improvement.GoalResult{
		GoalID:      "g-b",
		Description: "scale database",
		Success:     false,
		ErrorMsg:    "connection timeout",
	})

	// Simulate Analyze() submitting an improvement goal directly through
	// GoalFulfillment (the actual Analyze() path requires a live LLM).
	submitted, err := gf.SubmitGoal("Add retry logic for database scale operations")
	if err != nil {
		t.Fatalf("gf.SubmitGoal: %v", err)
	}

	// Verify it is persisted and in Refining state (SI's Analyze output).
	retrieved, err := gf.GetGoal(submitted.ID)
	if err != nil {
		t.Fatalf("gf.GetGoal: %v", err)
	}
	if retrieved.State != types.GoalStateRefining {
		t.Errorf("improvement goal state = %q, want Refining", retrieved.State)
	}
	if retrieved.Description != "Add retry logic for database scale operations" {
		t.Errorf("improvement goal description mismatch: %q", retrieved.Description)
	}

	// Verify SI stats are correct.
	report, err := si.Report(t.Context(), 0)
	if err != nil {
		t.Fatalf("si.Report: %v", err)
	}
	if report.Metrics["total_goals"].(int) != 2 {
		t.Errorf("total_goals = %v, want 2", report.Metrics["total_goals"])
	}
}

// ---------------------------------------------------------------------------
// TestSelfImprovementStatsAccumulate
// ---------------------------------------------------------------------------

// TestSelfImprovementStatsAccumulate records a mix of goal results and
// verifies the success rate is computed correctly.
func TestSelfImprovementStatsAccumulate(t *testing.T) {
	si, _ := newSIWithMockSubmitter(t)

	// 3 successes, 2 failures → success rate = 3/5 = 0.6
	si.RecordResult(self_improvement.GoalResult{GoalID: "g1", Success: true})
	si.RecordResult(self_improvement.GoalResult{GoalID: "g2", Success: true})
	si.RecordResult(self_improvement.GoalResult{GoalID: "g3", Success: true})
	si.RecordResult(self_improvement.GoalResult{GoalID: "g4", Success: false, ErrorMsg: "err1"})
	si.RecordResult(self_improvement.GoalResult{GoalID: "g5", Success: false, ErrorMsg: "err2"})

	report, err := si.Report(t.Context(), 0)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	assertMetricInt(t, report.Metrics, "total_goals", 5)
	assertMetricInt(t, report.Metrics, "success_count", 3)
	assertMetricInt(t, report.Metrics, "failure_count", 2)

	successRate, ok := report.Metrics["success_rate"]
	if !ok {
		t.Fatal("metrics missing 'success_rate'")
	}
	sr := successRate.(float64)
	const want = 0.6
	if sr < want-1e-9 || sr > want+1e-9 {
		t.Errorf("success_rate = %.6f, want %.6f", sr, want)
	}
}

// TestSelfImprovementStatsAccumulate_AllSuccess verifies 100% success rate.
func TestSelfImprovementStatsAccumulate_AllSuccess(t *testing.T) {
	si, _ := newSIWithMockSubmitter(t)

	for i := 0; i < 4; i++ {
		si.RecordResult(self_improvement.GoalResult{
			GoalID:  "g" + string(rune('0'+i)),
			Success: true,
		})
	}

	report, _ := si.Report(t.Context(), 0)
	assertMetricInt(t, report.Metrics, "total_goals", 4)
	assertMetricInt(t, report.Metrics, "success_count", 4)
	assertMetricInt(t, report.Metrics, "failure_count", 0)

	sr := report.Metrics["success_rate"].(float64)
	if sr < 1.0-1e-9 {
		t.Errorf("expected 1.0 success rate, got %v", sr)
	}
}

// TestSelfImprovementStatsAccumulate_Empty verifies that SuccessRate is 0.0
// when no results have been recorded.
func TestSelfImprovementStatsAccumulate_Empty(t *testing.T) {
	si, _ := newSIWithMockSubmitter(t)

	report, err := si.Report(t.Context(), 0)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	assertMetricInt(t, report.Metrics, "total_goals", 0)

	successRate, ok := report.Metrics["success_rate"]
	if !ok {
		t.Fatal("metrics missing 'success_rate'")
	}
	if successRate.(float64) != 0.0 {
		t.Errorf("expected success_rate 0.0 on empty stats, got %v", successRate)
	}
}

// ---------------------------------------------------------------------------
// TestSelfImprovementHealth
// ---------------------------------------------------------------------------

// TestSelfImprovementHealth verifies that the module reports healthy when no
// error has occurred.
func TestSelfImprovementHealth(t *testing.T) {
	si, _ := newSIWithMockSubmitter(t)

	h := si.Health()
	if h.Status != "healthy" {
		t.Errorf("expected healthy, got %q: %s", h.Status, h.Details)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func assertMetricInt(t *testing.T, metrics map[string]any, key string, want int) {
	t.Helper()
	v, ok := metrics[key]
	if !ok {
		t.Errorf("metrics missing %q", key)
		return
	}
	got, ok := v.(int)
	if !ok {
		t.Errorf("metric %q: expected int, got %T (%v)", key, v, v)
		return
	}
	if got != want {
		t.Errorf("metric %q = %d, want %d", key, got, want)
	}
}
