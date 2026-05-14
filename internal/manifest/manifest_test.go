package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_SeedAndLookup(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "manifest.json"))
	s.SeedDefaults()

	// Exact match
	e := s.Lookup("/etc/nginx")
	if e == nil {
		t.Fatal("expected /etc/nginx entry after seeding")
	}
	if e.Sensitivity != SensitivityMedium {
		t.Errorf("expected medium sensitivity for /etc/nginx, got %q", e.Sensitivity)
	}

	// Critical entry
	e = s.Lookup("/etc/sudoers.d")
	if e == nil {
		t.Fatal("expected /etc/sudoers.d entry")
	}
	if !e.RequiresPAM {
		t.Error("expected requires_pam for /etc/sudoers.d")
	}
	if e.Sensitivity != SensitivityCritical {
		t.Errorf("expected critical sensitivity, got %q", e.Sensitivity)
	}
}

func TestStore_PrefixLookup(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "manifest.json"))
	s.UpsertPath(PathEntry{
		Path:        "/etc/nginx",
		Sensitivity: SensitivityMedium,
		RequiresPAM: false,
	})

	// Nested path should match parent
	e := s.Lookup("/etc/nginx/nginx.conf")
	if e == nil {
		t.Fatal("expected prefix match for /etc/nginx/nginx.conf")
	}
	if e.Path != "/etc/nginx" {
		t.Errorf("expected matched path /etc/nginx, got %q", e.Path)
	}
}

func TestStore_SaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	s1 := NewStore(path)
	s1.UpsertPath(PathEntry{
		Path:        "/etc/test",
		Sensitivity: SensitivityHigh,
		RequiresPAM: true,
		Description: "test entry",
	})
	s1.UpsertService(ServiceEntry{
		Name:          "test-svc",
		SafeToRestart: false,
		RestartImpact: "breaks things",
	})
	if err := s1.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2 := NewStore(path)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	e := s2.Lookup("/etc/test")
	if e == nil {
		t.Fatal("expected /etc/test after load")
	}
	if e.Sensitivity != SensitivityHigh {
		t.Errorf("expected high sensitivity, got %q", e.Sensitivity)
	}
	if e.Description != "test entry" {
		t.Errorf("expected 'test entry', got %q", e.Description)
	}

	svc := s2.LookupService("test-svc")
	if svc == nil {
		t.Fatal("expected test-svc service after load")
	}
	if svc.SafeToRestart {
		t.Error("expected safe_to_restart false")
	}
}

func TestStore_LoadMissing(t *testing.T) {
	s := NewStore("/nonexistent/path/manifest.json")
	err := s.Load()
	if err != nil {
		t.Errorf("expected nil error for missing file, got %v", err)
	}
}

func TestStore_PostExecutionHook(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "manifest.json"))

	// Create a real file so Stat succeeds
	testFile := filepath.Join(dir, "testfile.conf")
	_ = os.WriteFile(testFile, []byte("content"), 0o644)

	s.PostExecutionHook([]string{"write", testFile, "content"})

	e := s.Lookup(testFile)
	if e == nil {
		t.Fatalf("expected manifest entry for %s after post-execution hook", testFile)
	}
}
