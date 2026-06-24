package governor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectiveStore_LoadAndAll(t *testing.T) {
	dir := t.TempDir()

	immutableYAML := `directives:
  - id: "safety-no-harm"
    priority: 1
    description: "Do not harm the user"
    immutable: true
  - id: "safety-no-delete"
    priority: 2
    description: "Do not delete user data without explicit permission"
    immutable: true
`
	mutableYAML := `directives:
  - id: "pref-concise"
    priority: 10
    description: "Be concise in communication"
    immutable: false
`

	immutablePath := filepath.Join(dir, "immutable.yaml")
	mutablePath := filepath.Join(dir, "mutable.yaml")
	if err := os.WriteFile(immutablePath, []byte(immutableYAML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mutablePath, []byte(mutableYAML), 0644); err != nil {
		t.Fatal(err)
	}

	var s DirectiveStore
	if err := s.LoadDirectives(immutablePath, mutablePath); err != nil {
		t.Fatalf("LoadDirectives: %v", err)
	}

	all := s.AllDirectives()
	if len(all) != 3 {
		t.Fatalf("expected 3 directives, got %d", len(all))
	}

	// Sorted by priority ascending.
	if all[0].Priority != 1 {
		t.Errorf("first directive priority = %d, want 1", all[0].Priority)
	}
	if all[1].Priority != 2 {
		t.Errorf("second directive priority = %d, want 2", all[1].Priority)
	}
	if all[2].Priority != 10 {
		t.Errorf("third directive priority = %d, want 10", all[2].Priority)
	}
}

func TestDirectiveStore_ImmutableFlagForced(t *testing.T) {
	dir := t.TempDir()

	// Even if immutable:false is written in the immutable file, the store
	// must override it to true.
	immutableYAML := `directives:
  - id: "safety-test"
    priority: 1
    description: "Test directive"
    immutable: false
`
	path := filepath.Join(dir, "immutable.yaml")
	if err := os.WriteFile(path, []byte(immutableYAML), 0644); err != nil {
		t.Fatal(err)
	}

	var s DirectiveStore
	if err := s.LoadDirectives(path, ""); err != nil {
		t.Fatalf("LoadDirectives: %v", err)
	}

	all := s.AllDirectives()
	if len(all) != 1 {
		t.Fatalf("expected 1 directive, got %d", len(all))
	}
	if !all[0].Immutable {
		t.Error("expected immutable flag to be forced true for directive loaded from immutable file")
	}
}

func TestDirectiveStore_MissingFiles(t *testing.T) {
	var s DirectiveStore
	err := s.LoadDirectives("/nonexistent/immutable.yaml", "/nonexistent/mutable.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing files, got: %v", err)
	}
	if len(s.AllDirectives()) != 0 {
		t.Errorf("expected 0 directives with missing files, got %d", len(s.AllDirectives()))
	}
}

func TestDirectiveStore_EmptyPaths(t *testing.T) {
	var s DirectiveStore
	if err := s.LoadDirectives("", ""); err != nil {
		t.Fatalf("LoadDirectives with empty paths: %v", err)
	}
	if len(s.AllDirectives()) != 0 {
		t.Errorf("expected 0 directives, got %d", len(s.AllDirectives()))
	}
}

func TestDirectiveStore_CheckCompliance_Approved(t *testing.T) {
	dir := t.TempDir()
	immutableYAML := `directives:
  - id: "safety-no-harm"
    priority: 1
    description: "Do not harm the user"
    immutable: true
  - id: "safety-no-delete"
    priority: 2
    description: "Do not delete user data without explicit permission"
    immutable: true
`
	path := filepath.Join(dir, "immutable.yaml")
	if err := os.WriteFile(path, []byte(immutableYAML), 0644); err != nil {
		t.Fatal(err)
	}

	var s DirectiveStore
	if err := s.LoadDirectives(path, ""); err != nil {
		t.Fatal(err)
	}

	compliant, violated := s.CheckCompliance("install nginx on the server")
	if !compliant {
		t.Errorf("expected compliant for safe description, got violated: %v", violated)
	}
	if len(violated) != 0 {
		t.Errorf("expected no violations, got: %v", violated)
	}
}

func TestDirectiveStore_CheckCompliance_ViolatesNoDelete(t *testing.T) {
	dir := t.TempDir()
	immutableYAML := `directives:
  - id: "safety-no-delete"
    priority: 2
    description: "Do not delete user data without explicit permission"
    immutable: true
`
	path := filepath.Join(dir, "immutable.yaml")
	if err := os.WriteFile(path, []byte(immutableYAML), 0644); err != nil {
		t.Fatal(err)
	}

	var s DirectiveStore
	if err := s.LoadDirectives(path, ""); err != nil {
		t.Fatal(err)
	}

	compliant, violated := s.CheckCompliance("delete all user files from the home directory")
	if compliant {
		t.Error("expected non-compliant for description containing 'delete'")
	}
	if len(violated) == 0 {
		t.Error("expected at least one violated directive")
	}
}

func TestDirectiveStore_CheckCompliance_ViolatesNoHarm(t *testing.T) {
	dir := t.TempDir()
	immutableYAML := `directives:
  - id: "safety-no-harm"
    priority: 1
    description: "Do not harm the user"
    immutable: true
`
	path := filepath.Join(dir, "immutable.yaml")
	if err := os.WriteFile(path, []byte(immutableYAML), 0644); err != nil {
		t.Fatal(err)
	}

	var s DirectiveStore
	if err := s.LoadDirectives(path, ""); err != nil {
		t.Fatal(err)
	}

	compliant, violated := s.CheckCompliance("this will harm the system")
	if compliant {
		t.Error("expected non-compliant for description containing 'harm'")
	}
	if len(violated) == 0 {
		t.Error("expected at least one violated directive")
	}
}

func TestDirectiveStore_CheckCompliance_MultipleViolations(t *testing.T) {
	dir := t.TempDir()
	immutableYAML := `directives:
  - id: "safety-no-harm"
    priority: 1
    description: "Do not harm the user"
    immutable: true
  - id: "safety-no-delete"
    priority: 2
    description: "Do not delete user data without explicit permission"
    immutable: true
`
	path := filepath.Join(dir, "immutable.yaml")
	if err := os.WriteFile(path, []byte(immutableYAML), 0644); err != nil {
		t.Fatal(err)
	}

	var s DirectiveStore
	if err := s.LoadDirectives(path, ""); err != nil {
		t.Fatal(err)
	}

	// Both "harm" and "delete" keywords present.
	compliant, violated := s.CheckCompliance("harm and delete all user data")
	if compliant {
		t.Error("expected non-compliant when multiple directives are violated")
	}
	if len(violated) < 2 {
		t.Errorf("expected at least 2 violations, got %d: %v", len(violated), violated)
	}
}

func TestDirectiveStore_CheckCompliance_MutableDirectives_NotViolated(t *testing.T) {
	dir := t.TempDir()
	// Mutable directives (preferences) have no keyword checks — they never
	// show as violated through keyword matching alone.
	mutableYAML := `directives:
  - id: "pref-concise"
    priority: 10
    description: "Be concise in communication"
    immutable: false
`
	path := filepath.Join(dir, "mutable.yaml")
	if err := os.WriteFile(path, []byte(mutableYAML), 0644); err != nil {
		t.Fatal(err)
	}

	var s DirectiveStore
	if err := s.LoadDirectives("", path); err != nil {
		t.Fatal(err)
	}

	compliant, violated := s.CheckCompliance("write a very long and verbose explanation of everything")
	if !compliant {
		t.Errorf("mutable preference should not trigger keyword violation, got: %v", violated)
	}
}

func TestDirectiveStore_AllDirectives_ReturnsCopy(t *testing.T) {
	dir := t.TempDir()
	yaml := `directives:
  - id: "safety-no-harm"
    priority: 1
    description: "Do not harm the user"
    immutable: true
`
	path := filepath.Join(dir, "immutable.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	var s DirectiveStore
	if err := s.LoadDirectives(path, ""); err != nil {
		t.Fatal(err)
	}

	all1 := s.AllDirectives()
	all1[0].ID = "mutated"

	all2 := s.AllDirectives()
	if all2[0].ID == "mutated" {
		t.Error("AllDirectives should return a copy, not a reference to internal state")
	}
}

func TestDirectiveStore_MalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("not: [valid: yaml: at: all"), 0644); err != nil {
		t.Fatal(err)
	}

	var s DirectiveStore
	err := s.LoadDirectives(path, "")
	if err == nil {
		t.Error("expected error for malformed YAML file")
	}
}
