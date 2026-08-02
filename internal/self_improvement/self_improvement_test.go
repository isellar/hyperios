package self_improvement

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/types"
)

// ── Mock helpers ─────────────────────────────────────────────────────────────

// mockCompleter satisfies llm.Completer for testing.
type mockCompleter struct {
	response string
	err      error
	// capture last call arguments for inspection
	lastSystem string
	lastUser   string
}

func (m *mockCompleter) Complete(_ context.Context, system, user string) (string, error) {
	m.lastSystem = system
	m.lastUser = user
	return m.response, m.err
}

func (m *mockCompleter) CompleteWithRetry(_ context.Context, system, user string) (string, error) {
	m.lastSystem = system
	m.lastUser = user
	return m.response, m.err
}

var _ llm.Completer = (*mockCompleter)(nil)

// mockGoalSubmitter satisfies GoalSubmitter for testing.
type mockGoalSubmitter struct {
	submitted []string
	err       error
}

func (m *mockGoalSubmitter) SubmitGoal(description string) (*types.Goal, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.submitted = append(m.submitted, description)
	return &types.Goal{
		ID:          "g-test-" + description,
		Description: description,
		State:       types.GoalStateRefining,
	}, nil
}

// mockDirectiveStore satisfies DirectiveStore for testing.
type mockDirectiveStore struct {
	added []types.Directive
	err   error
}

func (m *mockDirectiveStore) AddDirective(d types.Directive) error {
	if m.err != nil {
		return m.err
	}
	m.added = append(m.added, d)
	return nil
}

// ── Stats tests ───────────────────────────────────────────────────────────────

func TestStats_RecordGoalResult_Success(t *testing.T) {
	s := NewStats()
	s.RecordGoalResult("g1", true, 100, "")
	s.RecordGoalResult("g2", true, 200, "")

	summary := s.Summary()
	if summary.TotalGoals != 2 {
		t.Errorf("TotalGoals: got %d, want 2", summary.TotalGoals)
	}
	if summary.SuccessCount != 2 {
		t.Errorf("SuccessCount: got %d, want 2", summary.SuccessCount)
	}
	if summary.FailureCount != 0 {
		t.Errorf("FailureCount: got %d, want 0", summary.FailureCount)
	}
}

func TestStats_RecordGoalResult_Failure(t *testing.T) {
	s := NewStats()
	s.RecordGoalResult("g1", false, 50, "timeout")
	s.RecordGoalResult("g2", true, 150, "")

	summary := s.Summary()
	if summary.TotalGoals != 2 {
		t.Errorf("TotalGoals: got %d, want 2", summary.TotalGoals)
	}
	if summary.FailureCount != 1 {
		t.Errorf("FailureCount: got %d, want 1", summary.FailureCount)
	}
	if summary.SuccessCount != 1 {
		t.Errorf("SuccessCount: got %d, want 1", summary.SuccessCount)
	}
}

func TestStats_SuccessRate_Empty(t *testing.T) {
	s := NewStats()
	if got := s.SuccessRate(); got != 0.0 {
		t.Errorf("SuccessRate() on empty stats: got %f, want 0.0", got)
	}
}

func TestStats_SuccessRate(t *testing.T) {
	s := NewStats()
	s.RecordGoalResult("g1", true, 0, "")
	s.RecordGoalResult("g2", true, 0, "")
	s.RecordGoalResult("g3", false, 0, "err")

	want := 2.0 / 3.0
	if got := s.SuccessRate(); got != want {
		t.Errorf("SuccessRate(): got %f, want %f", got, want)
	}
}

func TestStats_AvgDurationMs(t *testing.T) {
	s := NewStats()
	s.RecordGoalResult("g1", true, 100, "")
	s.RecordGoalResult("g2", true, 300, "")

	summary := s.Summary()
	if summary.AvgDurationMs != 200.0 {
		t.Errorf("AvgDurationMs: got %f, want 200.0", summary.AvgDurationMs)
	}
}

func TestStats_RecordToolUsage(t *testing.T) {
	s := NewStats()
	s.RecordToolUsage("grep", true)
	s.RecordToolUsage("grep", true)
	s.RecordToolUsage("rm", false)

	summary := s.Summary()
	if summary.TotalToolCalls != 3 {
		t.Errorf("TotalToolCalls: got %d, want 3", summary.TotalToolCalls)
	}
	if summary.UnauthorizedCalls != 1 {
		t.Errorf("UnauthorizedCalls: got %d, want 1", summary.UnauthorizedCalls)
	}
}

func TestStats_FailurePatterns_None(t *testing.T) {
	s := NewStats()
	// Single failure — should NOT appear (threshold is > 1).
	s.RecordGoalResultWithDescription("g1", "install nginx", false, 0, "permission denied")

	patterns := s.FailurePatterns()
	if len(patterns) != 0 {
		t.Errorf("FailurePatterns(): expected empty, got %v", patterns)
	}
}

func TestStats_FailurePatterns_Detected(t *testing.T) {
	s := NewStats()
	// Two failures on same goal ID with description.
	s.RecordGoalResultWithDescription("g1", "install nginx", false, 0, "permission denied")
	s.RecordGoalResultWithDescription("g1", "install nginx", false, 0, "network error")

	patterns := s.FailurePatterns()
	if len(patterns) != 1 {
		t.Errorf("FailurePatterns(): expected 1 pattern, got %d: %v", len(patterns), patterns)
	}
	if patterns[0] != "install nginx" {
		t.Errorf("FailurePatterns()[0]: got %q, want %q", patterns[0], "install nginx")
	}
}

func TestStats_FailurePatterns_MultipleGoals(t *testing.T) {
	s := NewStats()
	s.RecordGoalResultWithDescription("g1", "install nginx", false, 0, "err1")
	s.RecordGoalResultWithDescription("g1", "install nginx", false, 0, "err2")
	s.RecordGoalResultWithDescription("g2", "update config", false, 0, "err3")
	// g2 only failed once — should not appear.

	patterns := s.FailurePatterns()
	if len(patterns) != 1 {
		t.Errorf("FailurePatterns(): expected 1 pattern, got %d: %v", len(patterns), patterns)
	}
}

// ── SelfImprovement.RecordResult tests ───────────────────────────────────────

func TestSelfImprovement_RecordResult(t *testing.T) {
	mc := &mockCompleter{}
	si := newTestSelfImprovement(mc)

	si.RecordResult(GoalResult{GoalID: "g1", Description: "do X", Success: true})
	si.RecordResult(GoalResult{GoalID: "g2", Description: "do Y", Success: false, ErrorMsg: "failed"})

	si.mu.Lock()
	pendingCount := len(si.pending)
	si.mu.Unlock()

	if pendingCount != 2 {
		t.Errorf("pending: got %d, want 2", pendingCount)
	}

	summary := si.stats.Summary()
	if summary.TotalGoals != 2 {
		t.Errorf("stats.TotalGoals: got %d, want 2", summary.TotalGoals)
	}
}

// ── SelfImprovement.Analyze tests ────────────────────────────────────────────

func TestSelfImprovement_Analyze_NoGoalFulfillment(t *testing.T) {
	mc := &mockCompleter{}
	si := newTestSelfImprovement(mc)
	si.RecordResult(GoalResult{GoalID: "g1", Description: "do X", Success: false})

	err := si.Analyze()
	if err == nil {
		t.Error("Analyze(): expected error when no GoalSubmitter configured")
	}
}

func TestSelfImprovement_Analyze_NoPendingResults(t *testing.T) {
	mc := &mockCompleter{}
	si := newTestSelfImprovement(mc)
	gs := &mockGoalSubmitter{}
	si.SetGoalFulfillment(gs)

	err := si.Analyze()
	if err != nil {
		t.Errorf("Analyze() with no pending results: unexpected error: %v", err)
	}
	if len(gs.submitted) != 0 {
		t.Errorf("expected no goals submitted, got %d", len(gs.submitted))
	}
}

func TestSelfImprovement_Analyze_SubmitsImprovementGoals(t *testing.T) {
	analysisResp := Analysis{
		Patterns:         []string{"frequent permission errors"},
		Suggestions:      []string{"pre-check permissions before executing"},
		ImprovementGoals: []string{"add permission pre-check step", "improve error messages"},
	}
	respJSON, _ := json.Marshal(analysisResp)

	mc := &mockCompleter{response: string(respJSON)}
	si := newTestSelfImprovement(mc)
	gs := &mockGoalSubmitter{}
	si.SetGoalFulfillment(gs)

	si.RecordResult(GoalResult{GoalID: "g1", Description: "install package", Success: false, ErrorMsg: "permission denied"})
	si.RecordResult(GoalResult{GoalID: "g2", Description: "write config", Success: false, ErrorMsg: "permission denied"})

	if err := si.Analyze(); err != nil {
		t.Fatalf("Analyze() unexpected error: %v", err)
	}

	if len(gs.submitted) != 2 {
		t.Errorf("submitted goals: got %d, want 2; %v", len(gs.submitted), gs.submitted)
	}
	if gs.submitted[0] != "add permission pre-check step" {
		t.Errorf("submitted[0]: got %q", gs.submitted[0])
	}
	if gs.submitted[1] != "improve error messages" {
		t.Errorf("submitted[1]: got %q", gs.submitted[1])
	}
}

// TestSelfImprovement_Analyze_PersistsDirectives verifies that
// analysis.Directives are persisted via DirectiveStore.AddDirective.
func TestSelfImprovement_Analyze_PersistsDirectives(t *testing.T) {
	analysisResp := Analysis{
		Patterns: []string{"agent repeatedly ran out of disk space mid-write"},
		Directives: []DirectiveSuggestion{
			{Description: "always check available disk space before writing files larger than 100MB", Priority: 7},
		},
	}
	respJSON, _ := json.Marshal(analysisResp)

	mc := &mockCompleter{response: string(respJSON)}
	si := newTestSelfImprovement(mc)
	gs := &mockGoalSubmitter{}
	ds := &mockDirectiveStore{}
	si.SetGoalFulfillment(gs)
	si.SetDirectiveStore(ds)

	si.RecordResult(GoalResult{GoalID: "g1", Description: "write large file", Success: false, ErrorMsg: "no space left on device"})

	if err := si.Analyze(); err != nil {
		t.Fatalf("Analyze() unexpected error: %v", err)
	}

	if len(ds.added) != 1 {
		t.Fatalf("expected 1 directive persisted, got %d", len(ds.added))
	}
	if ds.added[0].Description != analysisResp.Directives[0].Description {
		t.Errorf("directive description mismatch: got %q", ds.added[0].Description)
	}
	if ds.added[0].Priority != 7 {
		t.Errorf("directive priority mismatch: got %d, want 7", ds.added[0].Priority)
	}
	if ds.added[0].Immutable {
		t.Error("learned directives should not be Immutable")
	}
	if ds.added[0].ID == "" {
		t.Error("expected a non-empty derived directive ID")
	}
	// No improvement goals in this analysis — none should be submitted.
	if len(gs.submitted) != 0 {
		t.Errorf("expected 0 goals submitted, got %d", len(gs.submitted))
	}
}

// TestSelfImprovement_Analyze_BothGoalsAndDirectives verifies both kinds of
// output can be produced and persisted from a single analysis cycle.
func TestSelfImprovement_Analyze_BothGoalsAndDirectives(t *testing.T) {
	analysisResp := Analysis{
		ImprovementGoals: []string{"install missing 'jq' dependency"},
		Directives:       []DirectiveSuggestion{{Description: "prefer apt over manual builds", Priority: 3}},
	}
	respJSON, _ := json.Marshal(analysisResp)

	mc := &mockCompleter{response: string(respJSON)}
	si := newTestSelfImprovement(mc)
	gs := &mockGoalSubmitter{}
	ds := &mockDirectiveStore{}
	si.SetGoalFulfillment(gs)
	si.SetDirectiveStore(ds)

	si.RecordResult(GoalResult{GoalID: "g1", Description: "task", Success: false})

	if err := si.Analyze(); err != nil {
		t.Fatalf("Analyze() unexpected error: %v", err)
	}

	if len(gs.submitted) != 1 {
		t.Errorf("expected 1 goal submitted, got %d", len(gs.submitted))
	}
	if len(ds.added) != 1 {
		t.Errorf("expected 1 directive persisted, got %d", len(ds.added))
	}
}

// TestSelfImprovement_Analyze_NeitherGoalsNorDirectives verifies that an
// analysis with both arrays empty is a legitimate, error-free outcome (not
// every analysis cycle needs to produce something).
func TestSelfImprovement_Analyze_NeitherGoalsNorDirectives(t *testing.T) {
	analysisResp := Analysis{Patterns: []string{}, Suggestions: []string{}}
	respJSON, _ := json.Marshal(analysisResp)

	mc := &mockCompleter{response: string(respJSON)}
	si := newTestSelfImprovement(mc)
	gs := &mockGoalSubmitter{}
	ds := &mockDirectiveStore{}
	si.SetGoalFulfillment(gs)
	si.SetDirectiveStore(ds)

	si.RecordResult(GoalResult{GoalID: "g1", Description: "task", Success: true})

	if err := si.Analyze(); err != nil {
		t.Fatalf("Analyze() unexpected error: %v", err)
	}
	if len(gs.submitted) != 0 || len(ds.added) != 0 {
		t.Errorf("expected no goals/directives, got %d goals, %d directives", len(gs.submitted), len(ds.added))
	}
}

// TestSelfImprovement_Analyze_NoDirectiveStore verifies Analyze still
// succeeds (for the goals path) when no DirectiveStore is wired — directive
// persistence is silently skipped rather than treated as an error.
func TestSelfImprovement_Analyze_NoDirectiveStore(t *testing.T) {
	analysisResp := Analysis{
		Directives: []DirectiveSuggestion{{Description: "some rule", Priority: 1}},
	}
	respJSON, _ := json.Marshal(analysisResp)

	mc := &mockCompleter{response: string(respJSON)}
	si := newTestSelfImprovement(mc)
	gs := &mockGoalSubmitter{}
	si.SetGoalFulfillment(gs)
	// No SetDirectiveStore call.

	si.RecordResult(GoalResult{GoalID: "g1", Description: "task", Success: false})

	if err := si.Analyze(); err != nil {
		t.Fatalf("Analyze() unexpected error with no DirectiveStore: %v", err)
	}
}

// TestSelfImprovement_Analyze_DirectiveStoreError verifies a
// directive-persistence failure is collected as a partial error (mirroring
// the existing GoalSubmitter error-collection behavior) rather than
// silently swallowed or fatal to the whole cycle.
func TestSelfImprovement_Analyze_DirectiveStoreError(t *testing.T) {
	analysisResp := Analysis{
		Directives: []DirectiveSuggestion{{Description: "some rule", Priority: 1}},
	}
	respJSON, _ := json.Marshal(analysisResp)

	mc := &mockCompleter{response: string(respJSON)}
	si := newTestSelfImprovement(mc)
	gs := &mockGoalSubmitter{}
	ds := &mockDirectiveStore{err: errTest("disk full")}
	si.SetGoalFulfillment(gs)
	si.SetDirectiveStore(ds)

	si.RecordResult(GoalResult{GoalID: "g1", Description: "task", Success: false})

	err := si.Analyze()
	if err == nil {
		t.Error("Analyze(): expected error when DirectiveStore fails")
	}
}

func TestSelfImprovement_Analyze_PendingClearedAfterAnalysis(t *testing.T) {
	analysisResp := Analysis{ImprovementGoals: []string{"do better"}}
	respJSON, _ := json.Marshal(analysisResp)

	mc := &mockCompleter{response: string(respJSON)}
	si := newTestSelfImprovement(mc)
	gs := &mockGoalSubmitter{}
	si.SetGoalFulfillment(gs)

	si.RecordResult(GoalResult{GoalID: "g1", Description: "task", Success: false})

	if err := si.Analyze(); err != nil {
		t.Fatalf("Analyze() unexpected error: %v", err)
	}

	si.mu.Lock()
	pendingAfter := len(si.pending)
	si.mu.Unlock()

	if pendingAfter != 0 {
		t.Errorf("pending after Analyze(): got %d, want 0", pendingAfter)
	}
}

func TestSelfImprovement_Analyze_LLMError(t *testing.T) {
	mc := &mockCompleter{err: &llm.NetworkError{Cause: errTest("connection refused")}}
	si := newTestSelfImprovement(mc)
	gs := &mockGoalSubmitter{}
	si.SetGoalFulfillment(gs)

	si.RecordResult(GoalResult{GoalID: "g1", Description: "task", Success: false})

	err := si.Analyze()
	if err == nil {
		t.Error("Analyze(): expected error on LLM failure")
	}

	// Health should report degraded after an error.
	h := si.Health()
	if h.Status != "degraded" {
		t.Errorf("Health().Status: got %q, want %q", h.Status, "degraded")
	}
}

func TestSelfImprovement_Analyze_GoalSubmitterError(t *testing.T) {
	analysisResp := Analysis{ImprovementGoals: []string{"goal 1", "goal 2"}}
	respJSON, _ := json.Marshal(analysisResp)

	mc := &mockCompleter{response: string(respJSON)}
	si := newTestSelfImprovement(mc)
	gs := &mockGoalSubmitter{err: errTest("storage full")}
	si.SetGoalFulfillment(gs)

	si.RecordResult(GoalResult{GoalID: "g1", Description: "task", Success: false})

	err := si.Analyze()
	if err == nil {
		t.Error("Analyze(): expected error when GoalSubmitter fails")
	}
}

// ── Module interface tests ────────────────────────────────────────────────────

func TestSelfImprovement_Name(t *testing.T) {
	mc := &mockCompleter{}
	si := newTestSelfImprovement(mc)
	if si.Name() != "self_improvement" {
		t.Errorf("Name(): got %q, want %q", si.Name(), "self_improvement")
	}
}

func TestSelfImprovement_Health_Healthy(t *testing.T) {
	mc := &mockCompleter{}
	si := newTestSelfImprovement(mc)
	h := si.Health()
	if h.Status != "healthy" {
		t.Errorf("Health().Status: got %q, want %q", h.Status, "healthy")
	}
}

func TestSelfImprovement_Capabilities(t *testing.T) {
	mc := &mockCompleter{}
	si := newTestSelfImprovement(mc)
	caps := si.Capabilities()
	if len(caps) == 0 {
		t.Error("Capabilities(): expected at least one capability")
	}
}

func TestSelfImprovement_Report(t *testing.T) {
	mc := &mockCompleter{}
	si := newTestSelfImprovement(mc)
	si.RecordResult(GoalResult{GoalID: "g1", Description: "task", Success: true})

	report, err := si.Report(context.Background(), 0)
	if err != nil {
		t.Fatalf("Report() unexpected error: %v", err)
	}
	if report.ModuleName != "self_improvement" {
		t.Errorf("ModuleName: got %q, want %q", report.ModuleName, "self_improvement")
	}
	if report.Metrics["total_goals"].(int) != 1 {
		t.Errorf("Metrics[total_goals]: got %v, want 1", report.Metrics["total_goals"])
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// newTestSelfImprovement builds a SelfImprovement with nil audit (no file I/O in tests).
func newTestSelfImprovement(mc llm.Completer) *SelfImprovement {
	stats := NewStats()
	return &SelfImprovement{
		analyzer:  NewAnalyzer(mc, stats),
		stats:     stats,
		audit:     nil, // no file I/O in unit tests
		sessionID: "test-session",
		cfg:       nil,
	}
}

// errTest is a simple error value for testing.
type errTest string

func (e errTest) Error() string { return string(e) }
