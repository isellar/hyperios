package governor

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/governor/capability"
	"github.com/isellar/hyperios/internal/types"
)

func TestToolAuthorization_RequestAndCheck(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "auth.json")
	reg := capability.NewRegistry()
	ta := NewToolAuthorization(reg, storagePath)

	if ta.CheckAuthorization("tool1") {
		t.Error("expected tool1 to not be authorized initially")
	}

	if err := ta.RequestAuthorization("tool1", "always"); err != nil {
		t.Fatalf("RequestAuthorization: %v", err)
	}

	if !ta.CheckAuthorization("tool1") {
		t.Error("expected tool1 to be authorized after request")
	}
}

func TestToolAuthorization_SessionScope(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "auth.json")
	reg := capability.NewRegistry()
	ta := NewToolAuthorization(reg, storagePath)

	if err := ta.RequestAuthorization("tool1", "session"); err != nil {
		t.Fatalf("RequestAuthorization: %v", err)
	}

	if !ta.CheckAuthorization("tool1") {
		t.Error("expected tool1 to be authorized within session scope")
	}
}

func TestToolAuthorization_RequestScope(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "auth.json")
	reg := capability.NewRegistry()
	ta := NewToolAuthorization(reg, storagePath)

	if err := ta.RequestAuthorization("tool1", "request"); err != nil {
		t.Fatalf("RequestAuthorization: %v", err)
	}

	if !ta.CheckAuthorization("tool1") {
		t.Error("expected tool1 to be authorized within request scope")
	}
}

func TestToolAuthorization_InvalidScope(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "auth.json")
	reg := capability.NewRegistry()
	ta := NewToolAuthorization(reg, storagePath)

	err := ta.RequestAuthorization("tool1", "invalid")
	if err == nil {
		t.Error("expected error for invalid scope")
	}
}

func TestToolAuthorization_Revoke(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "auth.json")
	reg := capability.NewRegistry()
	ta := NewToolAuthorization(reg, storagePath)

	ta.RequestAuthorization("tool1", "always")
	if err := ta.RevokeAuthorization("tool1"); err != nil {
		t.Fatalf("RevokeAuthorization: %v", err)
	}

	if ta.CheckAuthorization("tool1") {
		t.Error("expected tool1 to not be authorized after revocation")
	}
}

func TestToolAuthorization_Persistence(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "auth.json")
	reg := capability.NewRegistry()

	ta1 := NewToolAuthorization(reg, storagePath)
	ta1.RequestAuthorization("tool1", "always")
	ta1.RequestAuthorization("tool2", "session")

	ta2 := NewToolAuthorization(reg, storagePath)
	if err := ta2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !ta2.CheckAuthorization("tool1") {
		t.Error("expected tool1 to persist after reload")
	}
	if !ta2.CheckAuthorization("tool2") {
		t.Error("expected tool2 to persist after reload")
	}
}

func TestToolAuthorization_ExpiredNotLoaded(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "auth.json")
	reg := capability.NewRegistry()

	ta1 := NewToolAuthorization(reg, storagePath)
	ta1.mu.Lock()
	ta1.auths["expired_tool"] = types.ToolAuthorization{
		ToolID:       "expired_tool",
		Scope:        "request",
		ExpiresAt:    time.Now().Add(-time.Hour),
		AuthorizedBy: "user",
	}
	ta1.mu.Unlock()
	ta1.save()

	ta2 := NewToolAuthorization(reg, storagePath)
	if err := ta2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if ta2.CheckAuthorization("expired_tool") {
		t.Error("expected expired authorization to not be loaded")
	}
}

func TestToolAuthorization_ListAuthorizations(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "auth.json")
	reg := capability.NewRegistry()
	ta := NewToolAuthorization(reg, storagePath)

	ta.RequestAuthorization("tool1", "always")
	ta.RequestAuthorization("tool2", "session")

	auths := ta.ListAuthorizations()
	if len(auths) != 2 {
		t.Errorf("expected 2 authorizations, got %d", len(auths))
	}
}

func TestToolAuthorization_FirePopup_NoHandler(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "auth.json")
	reg := capability.NewRegistry()
	ta := NewToolAuthorization(reg, storagePath)

	_, err := ta.FirePopup("tool1", "session")
	if err == nil {
		t.Error("expected error when no popup handler is registered")
	}
}

type mockPopupHandler struct {
	response string
	err      error
}

func (m *mockPopupHandler) ShowPopup(toolID, scope string) (string, error) {
	return m.response, m.err
}

func TestToolAuthorization_FirePopup_WithHandler(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "auth.json")
	reg := capability.NewRegistry()
	ta := NewToolAuthorization(reg, storagePath)

	handler := &mockPopupHandler{response: "approved"}
	ta.SetPopupHandler(handler)

	result, err := ta.FirePopup("tool1", "session")
	if err != nil {
		t.Fatalf("FirePopup: %v", err)
	}
	if result != "approved" {
		t.Errorf("expected 'approved', got %q", result)
	}
}

func TestToolAuthorization_LoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "nonexistent", "auth.json")
	reg := capability.NewRegistry()
	ta := NewToolAuthorization(reg, storagePath)

	err := ta.Load()
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
}

func TestToolAuthorization_EmptyStoragePath(t *testing.T) {
	reg := capability.NewRegistry()
	ta := NewToolAuthorization(reg, "")

	if err := ta.Load(); err != nil {
		t.Fatalf("Load with empty path: %v", err)
	}
	if err := ta.RequestAuthorization("tool1", "always"); err != nil {
		t.Fatalf("RequestAuthorization with empty path: %v", err)
	}
}

func TestToolAuthorization_RevokePersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "auth.json")
	reg := capability.NewRegistry()

	ta1 := NewToolAuthorization(reg, storagePath)
	ta1.RequestAuthorization("tool1", "always")
	ta1.RevokeAuthorization("tool1")

	ta2 := NewToolAuthorization(reg, storagePath)
	ta2.Load()

	if ta2.CheckAuthorization("tool1") {
		t.Error("expected revoked tool to not persist")
	}
}

func TestToolAuthorization_StorageFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permission bits not applicable on Windows")
	}
	dir := t.TempDir()
	storagePath := filepath.Join(dir, "auth.json")
	reg := capability.NewRegistry()
	ta := NewToolAuthorization(reg, storagePath)

	ta.RequestAuthorization("tool1", "always")

	info, err := os.Stat(storagePath)
	if err != nil {
		t.Fatalf("stat storage file: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected file permissions 0600, got %o", perm)
	}
}
