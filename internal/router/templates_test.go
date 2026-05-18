package router

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestTemplateRegistry_Match(t *testing.T) {
	yaml := `
templates:
  install_package:
    name: install_package
    patterns:
      - "install (?P<package>[a-zA-Z0-9_.-]+)"
    plan:
      name: install_package
      steps:
        - id: install
          description: "Install {package}"
          capability:
            type: "execute:package"
            scope: "apt:{package}"
          command: ["sudo", "apt-get", "-y", "install", "{package}"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "templates.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tr, err := NewTemplateRegistry(path)
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	tmpl, slots := tr.Match("install nginx")
	if tmpl == nil {
		t.Fatal("expected template match")
	}

	if slots["package"] != "nginx" {
		t.Errorf("expected package=nginx, got %s", slots["package"])
	}
}

func TestTemplateRegistry_NoMatch(t *testing.T) {
	yaml := `
templates:
  install_package:
    name: install_package
    patterns:
      - "install (?P<package>[a-zA-Z0-9_.-]+)"
    plan:
      name: install_package
      steps:
        - id: install
          description: "Install {package}"
          command: ["sudo", "apt-get", "-y", "install", "{package}"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "templates.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tr, err := NewTemplateRegistry(path)
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	tmpl, _ := tr.Match("remove nginx")
	if tmpl != nil {
		t.Error("expected no match")
	}
}

func TestTemplateRegistry_Fill(t *testing.T) {
	yaml := `
templates:
  install_package:
    name: install_package
    patterns:
      - "install (?P<package>[a-zA-Z0-9_.-]+)"
    plan:
      name: install_package
      steps:
        - id: install
          description: "Install {package}"
          capability:
            type: "execute:package"
            scope: "apt:{package}"
          command: ["sudo", "apt-get", "-y", "install", "{package}"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "templates.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tr, err := NewTemplateRegistry(path)
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	tmpl, slots := tr.Match("install nginx")
	if tmpl == nil {
		t.Fatal("expected template match")
	}

	plan := tr.Fill(tmpl, slots)

	if plan.Steps[0].Description != "Install nginx" {
		t.Errorf("expected 'Install nginx', got %s", plan.Steps[0].Description)
	}

	if plan.Steps[0].Command[4] != "nginx" {
		t.Errorf("expected command arg 'nginx', got %s", plan.Steps[0].Command[4])
	}

	if plan.Steps[0].Capability.Scope != "apt:nginx" {
		t.Errorf("expected scope 'apt:nginx', got %s", plan.Steps[0].Capability.Scope)
	}
}

func TestTemplateRegistry_CaseInsensitive(t *testing.T) {
	yaml := `
templates:
  install_package:
    name: install_package
    patterns:
      - "install (?P<package>[a-zA-Z0-9_.-]+)"
    plan:
      name: install_package
      steps:
        - id: install
          description: "Install {package}"
          command: ["sudo", "apt-get", "-y", "install", "{package}"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "templates.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tr, err := NewTemplateRegistry(path)
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	tmpl, _ := tr.Match("INSTALL nginx")
	if tmpl == nil {
		t.Error("expected case-insensitive match")
	}
}

func TestTemplateRegistry_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "templates.yaml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tr, err := NewTemplateRegistry(path)
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	if len(tr.templates) != 0 {
		t.Errorf("expected 0 templates, got %d", len(tr.templates))
	}
}

func TestTemplateRegistry_NonExistentFile(t *testing.T) {
	tr, err := NewTemplateRegistry("/nonexistent/path/templates.yaml")
	if err != nil {
		t.Fatalf("expected no error for non-existent file, got: %v", err)
	}

	if len(tr.templates) != 0 {
		t.Errorf("expected 0 templates, got %d", len(tr.templates))
	}
}

func TestStatsManager_RecordExecution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.json")

	sm := NewStatsManager(path)

	sm.RecordExecution("test intent", 100*time.Millisecond, true, "")
	sm.RecordExecution("test intent", 200*time.Millisecond, true, "")

	stats := sm.Get("test intent")
	if stats == nil {
		t.Fatal("expected stats for intent")
	}

	if stats.Count != 2 {
		t.Errorf("expected count 2, got %d", stats.Count)
	}

	if stats.SuccessCount != 2 {
		t.Errorf("expected success count 2, got %d", stats.SuccessCount)
	}
}

func TestStatsManager_Promotion(t *testing.T) {
	sm := NewStatsManager("")

	sm.RecordExecution("test intent", 100*time.Millisecond, true, "")

	if sm.Tier("test intent") != TierCached {
		t.Errorf("expected tier %d (cached), got %d", TierCached, sm.Tier("test intent"))
	}

	sm.RecordExecution("test intent", 100*time.Millisecond, true, "")
	sm.RecordExecution("test intent", 100*time.Millisecond, true, "")

	if sm.Tier("test intent") != TierTemplated {
		t.Errorf("expected tier %d (templated), got %d", TierTemplated, sm.Tier("test intent"))
	}
}

func TestStatsManager_Demotion(t *testing.T) {
	sm := NewStatsManager("")

	sm.RecordExecution("test intent", 100*time.Millisecond, true, "")
	sm.RecordExecution("test intent", 100*time.Millisecond, true, "")
	sm.RecordExecution("test intent", 100*time.Millisecond, true, "")

	if sm.Tier("test intent") != TierTemplated {
		t.Errorf("expected tier %d (templated), got %d", TierTemplated, sm.Tier("test intent"))
	}

	sm.RecordExecution("test intent", 100*time.Millisecond, false, "test failure")

	if sm.Tier("test intent") != TierCached {
		t.Errorf("expected tier %d (cached) after demotion, got %d", TierCached, sm.Tier("test intent"))
	}
}

func TestStatsManager_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.json")

	sm1 := NewStatsManager(path)
	sm1.RecordExecution("test intent", 100*time.Millisecond, true, "")

	sm2 := NewStatsManager(path)

	stats := sm2.Get("test intent")
	if stats == nil {
		t.Fatal("expected stats after reload")
	}

	if stats.Count != 1 {
		t.Errorf("expected count 1 after reload, got %d", stats.Count)
	}
}

func TestStatsManager_AvgDuration(t *testing.T) {
	sm := NewStatsManager("")

	sm.RecordExecution("test intent", 100*time.Millisecond, true, "")
	sm.RecordExecution("test intent", 200*time.Millisecond, true, "")

	stats := sm.Get("test intent")
	if stats == nil {
		t.Fatal("expected stats for intent")
	}

	expected := 150 * time.Millisecond
	if stats.AvgDuration != expected {
		t.Errorf("expected avg duration %v, got %v", expected, stats.AvgDuration)
	}
}

func TestFillTemplate(t *testing.T) {
	slots := map[string]string{
		"package": "nginx",
		"service": "nginx",
	}

	result := fillTemplate("Install {package} and restart {service}", slots)
	expected := "Install nginx and restart nginx"

	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestExtractSlots(t *testing.T) {
	re := compilePattern("install (?P<package>[a-zA-Z0-9_.-]+)")
	match := re.FindStringSubmatch("install nginx")

	slots := extractSlots(re, match)

	if slots["package"] != "nginx" {
		t.Errorf("expected package=nginx, got %s", slots["package"])
	}
}

func compilePattern(pattern string) *regexp.Regexp {
	re, _ := regexp.Compile("(?i)" + pattern)
	return re
}
