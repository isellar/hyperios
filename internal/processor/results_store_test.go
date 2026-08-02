package processor

import (
	"path/filepath"
	"testing"
)

func TestResultStore_SaveAndGet(t *testing.T) {
	dir := t.TempDir()
	rs, err := NewResultStore(filepath.Join(dir, "agent_results.json"))
	if err != nil {
		t.Fatalf("NewResultStore: %v", err)
	}

	result := &AgentResult{GoalID: "g1", Success: false, Error: "permission denied"}
	if err := rs.Save(result); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok := rs.Get("g1")
	if !ok {
		t.Fatal("expected result to be found")
	}
	if got.Error != "permission denied" {
		t.Errorf("expected error %q, got %q", "permission denied", got.Error)
	}
}

func TestResultStore_Get_NotFound(t *testing.T) {
	rs, err := NewResultStore(filepath.Join(t.TempDir(), "agent_results.json"))
	if err != nil {
		t.Fatalf("NewResultStore: %v", err)
	}

	if _, ok := rs.Get("nonexistent"); ok {
		t.Error("expected not found for nonexistent goal ID")
	}
}

func TestResultStore_Save_NilOrEmptyGoalID(t *testing.T) {
	rs, err := NewResultStore(filepath.Join(t.TempDir(), "agent_results.json"))
	if err != nil {
		t.Fatalf("NewResultStore: %v", err)
	}

	if err := rs.Save(nil); err != nil {
		t.Errorf("Save(nil) should be a no-op, got error: %v", err)
	}
	if err := rs.Save(&AgentResult{GoalID: "", Success: true}); err != nil {
		t.Errorf("Save with empty GoalID should be a no-op, got error: %v", err)
	}
	if _, ok := rs.Get(""); ok {
		t.Error("expected empty-GoalID result not to be stored")
	}
}

func TestResultStore_Delete(t *testing.T) {
	rs, err := NewResultStore(filepath.Join(t.TempDir(), "agent_results.json"))
	if err != nil {
		t.Fatalf("NewResultStore: %v", err)
	}

	_ = rs.Save(&AgentResult{GoalID: "g1", Success: true})
	if _, ok := rs.Get("g1"); !ok {
		t.Fatal("expected g1 to be present before delete")
	}

	if err := rs.Delete("g1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := rs.Get("g1"); ok {
		t.Error("expected g1 to be gone after delete")
	}

	// Deleting a nonexistent goal ID should be a harmless no-op.
	if err := rs.Delete("never-existed"); err != nil {
		t.Errorf("Delete of nonexistent goal ID should not error, got: %v", err)
	}
}

// TestResultStore_SurvivesRestart is the core regression test for this
// feature: results saved by one ResultStore instance must be loadable by a
// fresh instance pointed at the same path, simulating a process restart.
func TestResultStore_SurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent_results.json")

	rs1, err := NewResultStore(path)
	if err != nil {
		t.Fatalf("NewResultStore (first): %v", err)
	}
	blocked := &AgentResult{
		GoalID:  "g-blocked-1",
		Success: false,
		Error:   "steam: command not found",
		Steps: []AgentStep{
			{Tool: "shell", Input: "steam --version", Output: "steam: command not found", IsError: true},
		},
	}
	if err := rs1.Save(blocked); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Simulate a restart: construct a brand new ResultStore against the same
	// file rather than reusing rs1.
	rs2, err := NewResultStore(path)
	if err != nil {
		t.Fatalf("NewResultStore (second, simulating restart): %v", err)
	}

	got, ok := rs2.Get("g-blocked-1")
	if !ok {
		t.Fatal("expected result to survive restart, but it was not found")
	}
	if got.Error != "steam: command not found" {
		t.Errorf("expected error to survive restart, got %q", got.Error)
	}
	if len(got.Steps) != 1 || got.Steps[0].Tool != "shell" {
		t.Errorf("expected steps to survive restart, got %+v", got.Steps)
	}
}

func TestResultStore_InMemoryOnly(t *testing.T) {
	// Empty path => in-memory only, no file I/O, no error.
	rs, err := NewResultStore("")
	if err != nil {
		t.Fatalf("NewResultStore(\"\"): %v", err)
	}
	if err := rs.Save(&AgentResult{GoalID: "g1", Success: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, ok := rs.Get("g1"); !ok {
		t.Error("expected in-memory save to be retrievable within the same instance")
	}
}

func TestResultStore_LoadFromMissingFile(t *testing.T) {
	// Path doesn't exist yet — should return an empty store, not an error.
	rs, err := NewResultStore(filepath.Join(t.TempDir(), "nested", "does-not-exist.json"))
	if err != nil {
		t.Fatalf("NewResultStore with missing file: %v", err)
	}
	if _, ok := rs.Get("anything"); ok {
		t.Error("expected empty store when file doesn't exist yet")
	}
}
