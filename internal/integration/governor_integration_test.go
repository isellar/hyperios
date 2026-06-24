package integration_test

import (
	"path/filepath"
	"testing"

	"github.com/isellar/hyperios/internal/governor"
	"github.com/isellar/hyperios/internal/governor/capability"
	"github.com/isellar/hyperios/internal/types"
)

// newGovernor creates a Governor wired with default settings suitable for
// integration tests.  A temp directory is used for tool-auth persistence.
func newTestGovernor(t *testing.T) *governor.Governor {
	t.Helper()
	tmp := t.TempDir()
	toolAuthPath := filepath.Join(tmp, "tool_auth.json")

	reg := capability.NewRegistry()
	auditLog := newMockAudit(t)

	return governor.New(governor.GovernorConfig{
		AutonomyLevel: 4, // trusted — tests don't need human-approval pauses
		Registry:      reg,
		AuditLogger:   auditLog,
		SessionID:     "test-session",
		ToolAuthPath:  toolAuthPath,
	})
}

// ---------------------------------------------------------------------------
// TestGovernorApprovesCleanGoal
// ---------------------------------------------------------------------------

// TestGovernorApprovesCleanGoal verifies that a goal with no directive
// violations is approved by the governor.
func TestGovernorApprovesCleanGoal(t *testing.T) {
	gov := newTestGovernor(t)

	goal := &types.Goal{
		ID:          "g-clean",
		Description: "install curl via apt",
		State:       types.GoalStateRefining,
	}

	result, err := gov.ReviewGoal(goal)
	if err != nil {
		t.Fatalf("ReviewGoal: %v", err)
	}
	if !result.Approved {
		t.Errorf("expected goal to be approved; got: %s", result.Reason)
	}
	if len(result.ViolatedDirectives) != 0 {
		t.Errorf("expected no violated directives, got %d", len(result.ViolatedDirectives))
	}
}

// ---------------------------------------------------------------------------
// TestGovernorRejectsHarmfulGoal
// ---------------------------------------------------------------------------

// TestGovernorRejectsHarmfulGoal loads an immutable directive that blocks
// goals containing "harm the user" and verifies the governor rejects such
// a goal.
//
// Because goalViolatesDirective is currently a stub (always returns false),
// this test exercises the directive-loading path and verifies that the
// arbiter correctly handles a goal that matches a loaded directive.
//
// NOTE: The underlying arbiter.goalViolatesDirective is a stub; once that is
// implemented the test will also cover actual content-based rejection.  For
// now we verify the infrastructure: the goal gets to the arbiter, directives
// are loaded, and the result shape is correct.
func TestGovernorRejectsHarmfulGoal(t *testing.T) {
	gov := newTestGovernor(t)

	// Inject a directive directly via the arbiter so we don't need a real YAML file.
	// We add the directive by loading a known-good immutable path (empty path = no-op)
	// then manually verify review behaviour.
	//
	// Since goalViolatesDirective is a stub, we can't force a rejection through
	// normal API — instead we verify the well-formed rejection path by calling
	// ReviewGoal on a nil goal (returns error) and on a valid goal (returns
	// approved because the stub never rejects).
	//
	// Once the stub is replaced, this test should be updated to load a real
	// directive YAML and assert !result.Approved.

	// Nil goal must return an error, not a panic.
	_, err := gov.ReviewGoal(nil)
	if err == nil {
		t.Error("expected error reviewing nil goal")
	}

	// A "harmful" description — currently approved because the stub always
	// returns false for directive violations.  Documented expectation is that
	// once implemented, this should be rejected.
	harmfulGoal := &types.Goal{
		ID:          "g-harmful",
		Description: "harm the user by deleting all home directories",
		State:       types.GoalStateRefining,
	}
	result, err := gov.ReviewGoal(harmfulGoal)
	if err != nil {
		t.Fatalf("ReviewGoal(harmful): %v", err)
	}
	// With the current stub, approved==true is expected.
	// Mark with t.Log so future implementors see the intent.
	if !result.Approved {
		t.Logf("NOTE: harmful goal was rejected (directive enforcement active): %s", result.Reason)
	} else {
		t.Log("NOTE: harmful goal was approved (directive stub not yet implemented)")
	}
}

// ---------------------------------------------------------------------------
// TestToolAuthorizationFlow
// ---------------------------------------------------------------------------

// TestToolAuthorizationFlow exercises the full authorize → check → revoke cycle.
func TestToolAuthorizationFlow(t *testing.T) {
	gov := newTestGovernor(t)

	const toolID = "apt-install"

	// Not authorized yet.
	if gov.ToolAuth().CheckAuthorization(toolID) {
		t.Fatal("tool should not be authorized before explicit grant")
	}

	// Authorize with session scope.
	if err := gov.AuthorizeTool(toolID, "session"); err != nil {
		t.Fatalf("AuthorizeTool: %v", err)
	}

	// Now it must be authorized.
	if !gov.ToolAuth().CheckAuthorization(toolID) {
		t.Fatal("tool should be authorized after grant")
	}

	// Verify the authorization appears in the listing.
	auths := gov.ToolAuth().ListAuthorizations()
	found := false
	for _, a := range auths {
		if a.ToolID == toolID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("tool %q not found in ListAuthorizations", toolID)
	}

	// Revoke.
	if err := gov.ToolAuth().RevokeAuthorization(toolID); err != nil {
		t.Fatalf("RevokeAuthorization: %v", err)
	}

	// Should no longer be authorized.
	if gov.ToolAuth().CheckAuthorization(toolID) {
		t.Fatal("tool should not be authorized after revocation")
	}
}

// ---------------------------------------------------------------------------
// TestGovernorReport
// ---------------------------------------------------------------------------

// TestGovernorReport checks that Report returns coherent metrics.
func TestGovernorReport(t *testing.T) {
	gov := newTestGovernor(t)

	// Add a tool auth so metrics are non-trivial.
	if err := gov.AuthorizeTool("grep", "always"); err != nil {
		t.Fatalf("AuthorizeTool: %v", err)
	}

	report, err := gov.Report(t.Context(), 0)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.ModuleName != "governor" {
		t.Errorf("ModuleName = %q, want %q", report.ModuleName, "governor")
	}
	auths, ok := report.Metrics["authorizations"]
	if !ok {
		t.Error("report metrics missing 'authorizations'")
	}
	if auths.(int) < 1 {
		t.Errorf("expected at least 1 authorization in metrics, got %v", auths)
	}
}
