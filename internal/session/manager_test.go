package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

func TestManager_SaveAndLoad(t *testing.T) {
	mgr := NewManager(t.TempDir())

	state := NewState("test-id", "test intent", types.WorkspaceContext{Cwd: "/repo"})
	state.Plan = &types.ActionPlan{
		Steps: []types.ActionStep{{ID: "s1", Description: "do thing"}},
	}

	if err := mgr.Save(state); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := mgr.Load("test-id")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.ID != state.ID {
		t.Errorf("expected ID %q, got %q", state.ID, loaded.ID)
	}
	if loaded.Intent != state.Intent {
		t.Errorf("expected Intent %q, got %q", state.Intent, loaded.Intent)
	}
}

func TestManager_Load_NonExistent(t *testing.T) {
	mgr := NewManager(t.TempDir())

	_, err := mgr.Load("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

func TestManager_List(t *testing.T) {
	mgr := NewManager(t.TempDir())

	state1 := NewState("id1", "intent1", types.WorkspaceContext{Cwd: "/repo1"})
	state1.UpdatedAt = time.Now().Add(-time.Hour)
	state2 := NewState("id2", "intent2", types.WorkspaceContext{Cwd: "/repo2"})
	state2.UpdatedAt = time.Now()

	mgr.Save(state1)
	mgr.Save(state2)

	sessions, err := mgr.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].ID != "id2" {
		t.Errorf("expected most recent first, got %q", sessions[0].ID)
	}
}

func TestManager_List_EmptyDir(t *testing.T) {
	mgr := NewManager(t.TempDir())

	sessions, err := mgr.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestManager_List_SkipsCorrupt(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	state := NewState("good", "good intent", types.WorkspaceContext{})
	mgr.Save(state)

	badFile := filepath.Join(dir, "bad.json")
	os.WriteFile(badFile, []byte("invalid json{"), 0600)

	sessions, err := mgr.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}
}

func TestManager_Delete(t *testing.T) {
	mgr := NewManager(t.TempDir())

	state := NewState("test-id", "test intent", types.WorkspaceContext{})
	mgr.Save(state)

	if err := mgr.Delete("test-id"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if mgr.Exists("test-id") {
		t.Error("expected file to be deleted")
	}
}

func TestManager_Delete_NonExistent(t *testing.T) {
	mgr := NewManager(t.TempDir())

	err := mgr.Delete("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

func TestManager_Exists(t *testing.T) {
	mgr := NewManager(t.TempDir())

	state := NewState("test-id", "test intent", types.WorkspaceContext{})
	mgr.Save(state)

	if !mgr.Exists("test-id") {
		t.Error("expected exists to return true")
	}
	if mgr.Exists("nonexistent") {
		t.Error("expected exists to return false")
	}
}

func TestManager_Save_Idempotent(t *testing.T) {
	mgr := NewManager(t.TempDir())

	state := NewState("test-id", "intent1", types.WorkspaceContext{Cwd: "/repo1"})
	mgr.Save(state)

	state.Intent = "intent2"
	mgr.Save(state)

	loaded, _ := mgr.Load("test-id")
	if loaded.Intent != "intent2" {
		t.Errorf("expected intent2, got %q", loaded.Intent)
	}
}
