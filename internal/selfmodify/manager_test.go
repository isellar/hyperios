package selfmodify

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFixtureModule creates a minimal, self-contained Go module at dir with
// a cmd/hyperi package (so `go build ./cmd/hyperi` succeeds/fails
// predictably) and lets the test control whether build/vet/test pass.
func writeFixtureModule(t *testing.T, dir string, brokenBuild, brokenVet, brokenTest bool) {
	t.Helper()

	mustWrite := func(path, content string) {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o640); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}

	mustWrite("go.mod", "module fixture\n\ngo 1.21\n")

	mainBody := `package main

func main() {}
`
	if brokenBuild {
		mainBody = `package main

func main() { this is not valid go }
`
	}
	mustWrite("cmd/hyperi/main.go", mainBody)

	// go vet flags Printf-style format errors; construct one deliberately
	// when brokenVet is set. Import fmt unconditionally so the "clean" file
	// also compiles without an unused-import error either way.
	vetBody := `package pkg

import "fmt"

func helper() {
	fmt.Println("ok")
}
`
	if brokenVet {
		vetBody = `package pkg

import "fmt"

func helper() {
	fmt.Printf("%d", "not a number")
}
`
	}
	mustWrite("internal/pkg/helper.go", vetBody)

	testBody := `package pkg

import "testing"

func TestOK(t *testing.T) {}
`
	if brokenTest {
		testBody = `package pkg

import "testing"

func TestFails(t *testing.T) {
	t.Fatal("deliberately failing test")
}
`
	}
	mustWrite("internal/pkg/helper_test.go", testBody)
}

func newTestManager(t *testing.T, brokenBuild, brokenVet, brokenTest bool) (*Manager, string) {
	t.Helper()
	sourceDir := t.TempDir()
	writeFixtureModule(t, sourceDir, brokenBuild, brokenVet, brokenTest)

	binDir := t.TempDir()
	binaryPath := filepath.Join(binDir, "hyperi")
	// Seed a fake "currently installed" binary so backup/rollback have
	// something to work with.
	if err := os.WriteFile(binaryPath, []byte("original-binary-contents"), 0o755); err != nil {
		t.Fatalf("seed binary: %v", err)
	}

	mgr := NewManager(sourceDir, binaryPath, Options{
		BuildTimeout: 30 * time.Second,
		ReExecDelay:  time.Hour, // long enough that tests never actually trigger re-exec
	})
	return mgr, binaryPath
}

func TestVerify_AllPass(t *testing.T) {
	mgr, _ := newTestManager(t, false, false, false)

	result, err := mgr.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: unexpected error: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected Verify to pass, got failing steps: %+v", result.Steps)
	}
	if len(result.Steps) != 3 {
		t.Fatalf("expected 3 steps (build, vet, test), got %d", len(result.Steps))
	}
	for _, s := range result.Steps {
		if !s.Passed {
			t.Errorf("step %s unexpectedly failed: %s", s.Name, s.Output)
		}
	}
}

func TestVerify_BuildFails(t *testing.T) {
	mgr, _ := newTestManager(t, true, false, false)

	result, err := mgr.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: unexpected Go-level error: %v", err)
	}
	if result.Passed {
		t.Fatal("expected Verify to fail on broken build")
	}
	if len(result.Steps) != 1 || result.Steps[0].Name != "build" {
		t.Fatalf("expected to stop at the build step, got: %+v", result.Steps)
	}
}

func TestVerify_VetFails(t *testing.T) {
	mgr, _ := newTestManager(t, false, true, false)

	result, err := mgr.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: unexpected error: %v", err)
	}
	if result.Passed {
		t.Fatal("expected Verify to fail on broken vet")
	}
	if len(result.Steps) != 2 {
		t.Fatalf("expected to stop at the vet step (2 steps: build, vet), got %d: %+v", len(result.Steps), result.Steps)
	}
	if result.Steps[1].Name != "vet" || result.Steps[1].Passed {
		t.Errorf("expected vet step to be present and failing, got: %+v", result.Steps[1])
	}
}

func TestVerify_TestFails(t *testing.T) {
	mgr, _ := newTestManager(t, false, false, true)

	result, err := mgr.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: unexpected error: %v", err)
	}
	if result.Passed {
		t.Fatal("expected Verify to fail on broken test")
	}
	if len(result.Steps) != 3 {
		t.Fatalf("expected all 3 steps to run (test is last), got %d", len(result.Steps))
	}
	if result.Steps[2].Name != "test" || result.Steps[2].Passed {
		t.Errorf("expected test step to be present and failing, got: %+v", result.Steps[2])
	}
}

func TestVerifyResult_Summary(t *testing.T) {
	mgr, _ := newTestManager(t, true, false, false)
	result, _ := mgr.Verify(context.Background())

	summary := result.Summary()
	if summary == "" {
		t.Fatal("Summary() returned empty string")
	}
	if !containsAll(summary, "FAIL", "build") {
		t.Errorf("expected summary to mention FAIL and build step, got:\n%s", summary)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// ---------------------------------------------------------------------------
// Apply / Rollback
// ---------------------------------------------------------------------------

func TestApply_Success(t *testing.T) {
	mgr, binaryPath := newTestManager(t, false, false, false)
	// ReExecEnabled is false by default, so Apply should not attempt
	// re-exec — it returns a "restart manually" message instead.

	msg, err := mgr.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if msg == "" {
		t.Fatal("Apply: expected a non-empty result message")
	}

	// Default (no re-exec) path should mention manual restart.
	if !contains(msg, "restart") {
		t.Errorf("expected message to mention restart, got: %s", msg)
	}

	// The binary at binaryPath should have been replaced (no longer the
	// original placeholder contents).
	data, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read binary after Apply: %v", err)
	}
	if string(data) == "original-binary-contents" {
		t.Error("expected binary to be replaced after successful Apply")
	}

	// A backup of the original should exist.
	backups, err := mgr.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup after Apply, got %d", len(backups))
	}
}

// TestApply_ReExecEnabled verifies that when ReExecEnabled=true (i.e. a
// server-process Manager, not a CLI Manager), Apply() reports that it will
// restart (not "restart manually"). The actual re-exec is guarded behind a
// 1-hour delay so the test process is never actually replaced.
func TestApply_ReExecEnabled(t *testing.T) {
	sourceDir := t.TempDir()
	writeFixtureModule(t, sourceDir, false, false, false)
	binDir := t.TempDir()
	binaryPath := filepath.Join(binDir, "hyperi")
	_ = os.WriteFile(binaryPath, []byte("original"), 0o755)

	mgr := NewManager(sourceDir, binaryPath, Options{
		BuildTimeout:  30 * time.Second,
		ReExecDelay:   time.Hour, // ensures re-exec goroutine never fires during test
		ReExecEnabled: true,
	})

	msg, err := mgr.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply (ReExecEnabled): unexpected error: %v", err)
	}
	// Should mention automatic restart / restarting-in time, not "manually".
	if contains(msg, "manually") {
		t.Errorf("expected auto-restart message, got: %s", msg)
	}
	if !contains(msg, "Restarting") {
		t.Errorf("expected 'Restarting' in message, got: %s", msg)
	}
}

func TestApply_RefusedOnFailingVerify(t *testing.T) {
	mgr, binaryPath := newTestManager(t, true, false, false)

	originalData, _ := os.ReadFile(binaryPath)

	msg, err := mgr.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply: unexpected Go-level error: %v", err)
	}
	if !contains(msg, "refused") && !contains(msg, "failed") {
		t.Errorf("expected refusal message, got: %s", msg)
	}

	// Binary must be untouched.
	data, _ := os.ReadFile(binaryPath)
	if string(data) != string(originalData) {
		t.Error("expected binary to remain unchanged after a refused Apply")
	}

	// No backup should have been created since we never got to that step.
	backups, _ := mgr.ListBackups()
	if len(backups) != 0 {
		t.Errorf("expected 0 backups after refused Apply, got %d", len(backups))
	}
}

func TestRollback_NoBackups(t *testing.T) {
	mgr, _ := newTestManager(t, false, false, false)

	_, err := mgr.Rollback()
	if err == nil {
		t.Fatal("expected error rolling back with no backups")
	}
}

func TestRollback_RestoresPreviousBinary(t *testing.T) {
	mgr, binaryPath := newTestManager(t, false, false, false)

	// Apply once to create a backup of the "original-binary-contents" placeholder.
	if _, err := mgr.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	appliedData, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read applied binary: %v", err)
	}

	msg, err := mgr.Rollback()
	if err != nil {
		t.Fatalf("Rollback: unexpected error: %v", err)
	}
	if msg == "" {
		t.Fatal("Rollback: expected non-empty message")
	}

	restoredData, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read restored binary: %v", err)
	}
	if string(restoredData) != "original-binary-contents" {
		t.Error("expected Rollback to restore the pre-Apply binary contents")
	}
	if string(restoredData) == string(appliedData) {
		t.Error("restored binary should differ from the applied one")
	}
}

func TestPruneBackups_KeepsOnlyMostRecent(t *testing.T) {
	mgr, _ := newTestManager(t, false, false, false)

	// Apply repeatedly; each Apply creates one backup of whatever was
	// currently installed. maxBackups caps how many are retained.
	for i := 0; i < maxBackups+3; i++ {
		if _, err := mgr.Apply(context.Background()); err != nil {
			t.Fatalf("Apply iteration %d: %v", i, err)
		}
		time.Sleep(time.Millisecond) // ensure distinct timestamps
	}

	backups, err := mgr.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(backups) > maxBackups {
		t.Errorf("expected at most %d backups after pruning, got %d", maxBackups, len(backups))
	}
}

func TestGetStatus(t *testing.T) {
	mgr, binaryPath := newTestManager(t, false, false, false)
	_, _ = mgr.Verify(context.Background())

	status := mgr.GetStatus()
	if status.BinaryPath != binaryPath {
		t.Errorf("Status.BinaryPath = %q, want %q", status.BinaryPath, binaryPath)
	}
	if status.LastVerify == nil {
		t.Error("expected LastVerify to be populated after calling Verify")
	}
}
