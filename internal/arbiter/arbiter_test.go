package arbiter

import (
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
	verdicts := New().Decide(plan, report)

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
	verdicts := New().Decide(plan, report)

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
	verdicts := New().Decide(plan, report)

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
	verdicts := New().Decide(plan, report)

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
	verdicts := New().Decide(plan, report)

	if len(verdicts) != 1 {
		t.Fatalf("expected 1 verdict, got %d", len(verdicts))
	}
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
	verdicts := New().Decide(plan, report)

	if len(verdicts) != 1 {
		t.Fatalf("expected 1 verdict, got %d", len(verdicts))
	}
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
	verdicts := New().Decide(plan, report)

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
	verdicts := New().Decide(plan, report)

	if len(verdicts) != 1 {
		t.Fatalf("expected 1 verdict, got %d", len(verdicts))
	}
	if verdicts[0].Verdict != "approved" {
		t.Errorf("expected 'approved' (flag ignored), got %q", verdicts[0].Verdict)
	}
}

func TestDecide_EmptyPlanEmptyReport(t *testing.T) {
	plan := &types.ActionPlan{Steps: nil}
	report := &types.RiskReport{Flags: nil}
	verdicts := New().Decide(plan, report)

	if len(verdicts) != 0 {
		t.Errorf("expected 0 verdicts, got %d", len(verdicts))
	}
}

func TestDecide_EmptyPlanWithReport(t *testing.T) {
	plan := &types.ActionPlan{Steps: nil}
	report := &types.RiskReport{
		Flags: []types.RiskFlag{{StepID: "s1", Severity: "block", Description: "bad"}},
	}
	verdicts := New().Decide(plan, report)

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
	verdicts := New().Decide(plan, report)

	if len(verdicts) != 2 {
		t.Fatalf("expected 2 verdicts, got %d", len(verdicts))
	}
	if verdicts[0].Verdict != "blocked" {
		t.Errorf("s1: expected 'blocked', got %q", verdicts[0].Verdict)
	}
	if verdicts[1].Verdict != "approved" {
		t.Errorf("s2: expected 'approved', got %q", verdicts[1].Verdict)
	}
}

func TestDecide_StepAppearsMultipleTimesInFlags(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{{ID: "s1", Reversible: true}},
	}
	report := &types.RiskReport{
		Flags: []types.RiskFlag{
			{StepID: "s1", Severity: "low", Description: "minor"},
			{StepID: "s1", Severity: "high", Description: "major"},
		},
	}
	verdicts := New().Decide(plan, report)

	if verdicts[0].Verdict != "modified" {
		t.Errorf("expected 'modified' (high > low), got %q", verdicts[0].Verdict)
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
	verdicts := NewWithLevel(config.AutonomyObserve).Decide(plan, report)

	for _, v := range verdicts {
		if v.Verdict != "modified" {
			t.Errorf("level 0: expected all steps modified, got %q for %s", v.Verdict, v.StepID)
		}
		if v.Autonomy != config.AutonomyObserve {
			t.Errorf("expected autonomy level recorded as 0, got %d", v.Autonomy)
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
	verdicts := NewWithLevel(config.AutonomyReversible).Decide(plan, report)

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
	verdicts := NewWithLevel(config.AutonomyTrusted).Decide(plan, report)

	if verdicts[0].Verdict != "approved" {
		t.Errorf("level 4: high severity should still be approved, got %q", verdicts[0].Verdict)
	}
	if verdicts[1].Verdict != "blocked" {
		t.Errorf("level 4: block severity should still be blocked, got %q", verdicts[1].Verdict)
	}
}

func TestDecide_ArbiterRecordsAutonomyLevel(t *testing.T) {
	plan := &types.ActionPlan{
		Steps: []types.ActionStep{{ID: "s1", Reversible: true}},
	}
	report := &types.RiskReport{Flags: nil}
	verdicts := NewWithLevel(3).Decide(plan, report)

	if verdicts[0].Autonomy != 3 {
		t.Errorf("expected autonomy level 3 recorded in verdict, got %d", verdicts[0].Autonomy)
	}
}

func TestDecide_TableDriven(t *testing.T) {
	tests := []struct {
		name            string
		plan            *types.ActionPlan
		report          *types.RiskReport
		expectedVerdict string
	}{
		{
			name:            "block severity",
			plan:            &types.ActionPlan{Steps: []types.ActionStep{{ID: "s1", Reversible: true}}},
			report:          &types.RiskReport{Flags: []types.RiskFlag{{StepID: "s1", Severity: "block"}}},
			expectedVerdict: "blocked",
		},
		{
			name:            "high severity",
			plan:            &types.ActionPlan{Steps: []types.ActionStep{{ID: "s1", Reversible: true}}},
			report:          &types.RiskReport{Flags: []types.RiskFlag{{StepID: "s1", Severity: "high"}}},
			expectedVerdict: "modified",
		},
		{
			name:            "medium severity",
			plan:            &types.ActionPlan{Steps: []types.ActionStep{{ID: "s1", Reversible: true}}},
			report:          &types.RiskReport{Flags: []types.RiskFlag{{StepID: "s1", Severity: "medium"}}},
			expectedVerdict: "approved",
		},
		{
			name:            "low severity",
			plan:            &types.ActionPlan{Steps: []types.ActionStep{{ID: "s1", Reversible: true}}},
			report:          &types.RiskReport{Flags: []types.RiskFlag{{StepID: "s1", Severity: "low"}}},
			expectedVerdict: "approved",
		},
		{
			name:            "irreversible no flag",
			plan:            &types.ActionPlan{Steps: []types.ActionStep{{ID: "s1", Reversible: false}}},
			report:          &types.RiskReport{Flags: nil},
			expectedVerdict: "modified",
		},
		{
			name:            "reversible no flag",
			plan:            &types.ActionPlan{Steps: []types.ActionStep{{ID: "s1", Reversible: true}}},
			report:          &types.RiskReport{Flags: nil},
			expectedVerdict: "approved",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdicts := New().Decide(tt.plan, tt.report)
			if len(verdicts) != 1 {
				t.Fatalf("expected 1 verdict, got %d", len(verdicts))
			}
			if verdicts[0].Verdict != tt.expectedVerdict {
				t.Errorf("expected %q, got %q", tt.expectedVerdict, verdicts[0].Verdict)
			}
		})
	}
}
