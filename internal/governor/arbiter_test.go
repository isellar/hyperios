package governor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/types"
)

func TestDecide_BlockSeverity(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{{ID: "s1", Reversible: true}},
	}
	report := &types.RiskReport{
		Flags: []types.RiskFlag{{StepID: "s1", Severity: "block", Description: "deletes data"}},
	}
	verdicts := NewArbiter().Decide(plan, report)

	if len(verdicts) != 1 {
		t.Fatalf("expected 1 verdict, got %d", len(verdicts))
	}
	if verdicts[0].Verdict != "blocked" {
		t.Errorf("expected 'blocked', got %q", verdicts[0].Verdict)
	}
}

func TestDecide_HighSeverity(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{{ID: "s1", Reversible: true}},
	}
	report := &types.RiskReport{
		Flags: []types.RiskFlag{{StepID: "s1", Severity: "high", Description: "modifies system"}},
	}
	verdicts := NewArbiter().Decide(plan, report)

	if len(verdicts) != 1 {
		t.Fatalf("expected 1 verdict, got %d", len(verdicts))
	}
	if verdicts[0].Verdict != "modified" {
		t.Errorf("expected 'modified', got %q", verdicts[0].Verdict)
	}
}

func TestDecide_IrreversibleNoFlag(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{{ID: "s1", Reversible: false}},
	}
	report := &types.RiskReport{Flags: nil}
	verdicts := NewArbiter().Decide(plan, report)

	if len(verdicts) != 1 {
		t.Fatalf("expected 1 verdict, got %d", len(verdicts))
	}
	if verdicts[0].Verdict != "modified" {
		t.Errorf("expected 'modified' for irreversible step, got %q", verdicts[0].Verdict)
	}
}

func TestDecide_ReversibleNoFlag(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{{ID: "s1", Reversible: true}},
	}
	report := &types.RiskReport{Flags: nil}
	verdicts := NewArbiter().Decide(plan, report)

	if len(verdicts) != 1 {
		t.Fatalf("expected 1 verdict, got %d", len(verdicts))
	}
	if verdicts[0].Verdict != "approved" {
		t.Errorf("expected 'approved', got %q", verdicts[0].Verdict)
	}
}

func TestDecide_MediumSeverity(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{{ID: "s1", Reversible: true}},
	}
	report := &types.RiskReport{
		Flags: []types.RiskFlag{{StepID: "s1", Severity: "medium", Description: "minor change"}},
	}
	verdicts := NewArbiter().Decide(plan, report)

	if verdicts[0].Verdict != "approved" {
		t.Errorf("expected 'approved', got %q", verdicts[0].Verdict)
	}
}

func TestDecide_LowSeverity(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{{ID: "s1", Reversible: true}},
	}
	report := &types.RiskReport{
		Flags: []types.RiskFlag{{StepID: "s1", Severity: "low", Description: "trivial"}},
	}
	verdicts := NewArbiter().Decide(plan, report)

	if verdicts[0].Verdict != "approved" {
		t.Errorf("expected 'approved', got %q", verdicts[0].Verdict)
	}
}

func TestDecide_MultipleFlags_HighestWins(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{{ID: "s1", Reversible: true}},
	}
	report := &types.RiskReport{
		Flags: []types.RiskFlag{
			{StepID: "s1", Severity: "low", Description: "minor"},
			{StepID: "s1", Severity: "block", Description: "dangerous"},
		},
	}
	verdicts := NewArbiter().Decide(plan, report)

	if verdicts[0].Verdict != "blocked" {
		t.Errorf("expected 'blocked' (highest severity), got %q", verdicts[0].Verdict)
	}
}

func TestDecide_FlagForNonexistentStep(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{{ID: "s1", Reversible: true}},
	}
	report := &types.RiskReport{
		Flags: []types.RiskFlag{{StepID: "nonexistent", Severity: "block", Description: "bad"}},
	}
	verdicts := NewArbiter().Decide(plan, report)

	if verdicts[0].Verdict != "approved" {
		t.Errorf("expected 'approved' (flag ignored), got %q", verdicts[0].Verdict)
	}
}

func TestDecide_EmptyPlanEmptyReport(t *testing.T) {
	plan := &types.ActionPlan{Steps: nil}
	report := &types.RiskReport{Flags: nil}
	verdicts := NewArbiter().Decide(plan, report)

	if len(verdicts) != 0 {
		t.Errorf("expected 0 verdicts, got %d", len(verdicts))
	}
}

func TestDecide_OneBlockedOneClean(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1", Reversible: true},
			{ID: "s2", Reversible: true},
		},
	}
	report := &types.RiskReport{
		Flags: []types.RiskFlag{{StepID: "s1", Severity: "block", Description: "dangerous"}},
	}
	verdicts := NewArbiter().Decide(plan, report)

	if verdicts[0].Verdict != "blocked" {
		t.Errorf("s1: expected 'blocked', got %q", verdicts[0].Verdict)
	}
	if verdicts[1].Verdict != "approved" {
		t.Errorf("s2: expected 'approved', got %q", verdicts[1].Verdict)
	}
}

func TestDecide_AutonomyLevel0_AllModified(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1", Reversible: true},
			{ID: "s2", Reversible: false},
		},
	}
	report := &types.RiskReport{Flags: nil}
	verdicts := NewArbiterWithLevel(config.AutonomyObserve).Decide(plan, report)

	for _, v := range verdicts {
		if v.Verdict != "modified" {
			t.Errorf("level 0: expected all steps modified, got %q for %s", v.Verdict, v.StepID)
		}
	}
}

func TestDecide_AutonomyLevel2_ReversibleAutoApproved(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1", Reversible: true},
			{ID: "s2", Reversible: false},
		},
	}
	report := &types.RiskReport{Flags: nil}
	verdicts := NewArbiterWithLevel(config.AutonomyReversible).Decide(plan, report)

	if verdicts[0].Verdict != "approved" {
		t.Errorf("level 2: reversible step should be approved, got %q", verdicts[0].Verdict)
	}
	if verdicts[1].Verdict != "modified" {
		t.Errorf("level 2: irreversible step should be modified, got %q", verdicts[1].Verdict)
	}
}

func TestDecide_AutonomyLevel4_BlockOnlyBlocked(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1", Reversible: false},
			{ID: "s2", Reversible: false},
		},
	}
	report := &types.RiskReport{
		Flags: []types.RiskFlag{
			{StepID: "s1", Severity: "high", Description: "risky"},
			{StepID: "s2", Severity: "block", Description: "dangerous"},
		},
	}
	verdicts := NewArbiterWithLevel(config.AutonomyTrusted).Decide(plan, report)

	if verdicts[0].Verdict != "approved" {
		t.Errorf("level 4: high severity should still be approved, got %q", verdicts[0].Verdict)
	}
	if verdicts[1].Verdict != "blocked" {
		t.Errorf("level 4: block severity should still be blocked, got %q", verdicts[1].Verdict)
	}
}

func TestLoadDirectives(t *testing.T) {
	dir := t.TempDir()

	immutableContent := `directives:
  - id: "safety-no-harm"
    priority: 1
    description: "Do not harm the user"
    immutable: true
  - id: "safety-no-delete"
    priority: 2
    description: "Do not delete user data"
    immutable: true
`
	mutableContent := `directives:
  - id: "pref-concise"
    priority: 10
    description: "Be concise"
    immutable: false
`
	immutablePath := filepath.Join(dir, "immutable.yaml")
	mutablePath := filepath.Join(dir, "mutable.yaml")
	os.WriteFile(immutablePath, []byte(immutableContent), 0644)
	os.WriteFile(mutablePath, []byte(mutableContent), 0644)

	a := NewArbiter()
	if err := a.LoadDirectives(immutablePath, mutablePath); err != nil {
		t.Fatalf("LoadDirectives: %v", err)
	}

	directives := a.Directives()
	if len(directives) != 3 {
		t.Fatalf("expected 3 directives, got %d", len(directives))
	}

	if directives[0].Priority != 1 {
		t.Errorf("expected first directive priority 1, got %d", directives[0].Priority)
	}
	if directives[0].ID != "safety-no-harm" {
		t.Errorf("expected first directive id 'safety-no-harm', got %q", directives[0].ID)
	}
}

func TestLoadDirectives_MissingFiles(t *testing.T) {
	a := NewArbiter()
	err := a.LoadDirectives("/nonexistent/immutable.yaml", "/nonexistent/mutable.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing files, got: %v", err)
	}
	if len(a.Directives()) != 0 {
		t.Errorf("expected 0 directives, got %d", len(a.Directives()))
	}
}

func TestReviewGoal_NoDirectives(t *testing.T) {
	a := NewArbiter()
	goal := &types.Goal{ID: "g1", Description: "install nginx"}
	result, err := a.ReviewGoal(goal)
	if err != nil {
		t.Fatalf("ReviewGoal: %v", err)
	}
	if !result.Approved {
		t.Errorf("expected approved with no directives, got: %s", result.Reason)
	}
}

func TestReviewGoal_NilGoal(t *testing.T) {
	a := NewArbiter()
	_, err := a.ReviewGoal(nil)
	if err == nil {
		t.Error("expected error for nil goal")
	}
}
