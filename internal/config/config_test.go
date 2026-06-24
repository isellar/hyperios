package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()

	if cfg.AutonomyLevel != AutonomyApproved {
		t.Errorf("expected AutonomyLevel %d, got %d", AutonomyApproved, cfg.AutonomyLevel)
	}

	if cfg.ApprovalTimeoutForeground != 300 {
		t.Errorf("expected ApprovalTimeoutForeground 300, got %d", cfg.ApprovalTimeoutForeground)
	}

	if cfg.ApprovalTimeoutBackground != 30 {
		t.Errorf("expected ApprovalTimeoutBackground 30, got %d", cfg.ApprovalTimeoutBackground)
	}

	expectedPaths := []string{"/etc", "/var/lib/hyperi", "/opt"}
	if len(cfg.WatchPaths) != len(expectedPaths) {
		t.Fatalf("expected %d watch paths, got %d", len(expectedPaths), len(cfg.WatchPaths))
	}
	for i, p := range expectedPaths {
		if cfg.WatchPaths[i] != p {
			t.Errorf("watch path[%d]: expected %q, got %q", i, p, cfg.WatchPaths[i])
		}
	}
}

func TestLoad_NonExistentFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if cfg.AutonomyLevel != AutonomyApproved {
		t.Errorf("expected default AutonomyLevel, got %d", cfg.AutonomyLevel)
	}
}

func TestLoad_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	os.WriteFile(path, []byte(`{
		"autonomy_level": 3,
		"approval_timeout_foreground_seconds": 600,
		"approval_timeout_background_seconds": 60,
		"watch_paths": ["/tmp"]
	}`), 0644)

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if loaded.AutonomyLevel != AutonomyBounded {
		t.Errorf("expected AutonomyLevel %d, got %d", AutonomyBounded, loaded.AutonomyLevel)
	}
	if loaded.ApprovalTimeoutForeground != 600 {
		t.Errorf("expected ApprovalTimeoutForeground 600, got %d", loaded.ApprovalTimeoutForeground)
	}
	if loaded.ApprovalTimeoutBackground != 60 {
		t.Errorf("expected ApprovalTimeoutBackground 60, got %d", loaded.ApprovalTimeoutBackground)
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{invalid json`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	original := &Config{
		AutonomyLevel:             AutonomyTrusted,
		ApprovalTimeoutForeground: 500,
		ApprovalTimeoutBackground: 45,
		WatchPaths:                []string{"/home", "/var"},
		WhisperModelPath:          "/custom/model.bin",
		WhisperCLIPath:            "/custom/cli",
		VoiceEnabled:              true,
		VoicePushToTalkKey:        "alt+space",
	}

	if err := Save(path, original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.AutonomyLevel != original.AutonomyLevel {
		t.Errorf("AutonomyLevel: expected %d, got %d", original.AutonomyLevel, loaded.AutonomyLevel)
	}
	if loaded.ApprovalTimeoutForeground != original.ApprovalTimeoutForeground {
		t.Errorf("ApprovalTimeoutForeground: expected %d, got %d", original.ApprovalTimeoutForeground, loaded.ApprovalTimeoutForeground)
	}
	if loaded.ApprovalTimeoutBackground != original.ApprovalTimeoutBackground {
		t.Errorf("ApprovalTimeoutBackground: expected %d, got %d", original.ApprovalTimeoutBackground, loaded.ApprovalTimeoutBackground)
	}
	if len(loaded.WatchPaths) != len(original.WatchPaths) {
		t.Errorf("WatchPaths length: expected %d, got %d", len(original.WatchPaths), len(loaded.WatchPaths))
	}
	for i, p := range original.WatchPaths {
		if loaded.WatchPaths[i] != p {
			t.Errorf("WatchPaths[%d]: expected %q, got %q", i, p, loaded.WatchPaths[i])
		}
	}
	if loaded.WhisperModelPath != original.WhisperModelPath {
		t.Errorf("WhisperModelPath: expected %q, got %q", original.WhisperModelPath, loaded.WhisperModelPath)
	}
	if loaded.WhisperCLIPath != original.WhisperCLIPath {
		t.Errorf("WhisperCLIPath: expected %q, got %q", original.WhisperCLIPath, loaded.WhisperCLIPath)
	}
	if loaded.VoiceEnabled != original.VoiceEnabled {
		t.Errorf("VoiceEnabled: expected %v, got %v", original.VoiceEnabled, loaded.VoiceEnabled)
	}
	if loaded.VoicePushToTalkKey != original.VoicePushToTalkKey {
		t.Errorf("VoicePushToTalkKey: expected %q, got %q", original.VoicePushToTalkKey, loaded.VoicePushToTalkKey)
	}
}

func TestSave_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "config.json")

	err := Save(path, Defaults())
	if err != nil {
		t.Fatalf("Save failed to create parent directories: %v", err)
	}

	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		t.Error("parent directory was not created")
	}
}

func TestAutonomyLevelText(t *testing.T) {
	tests := []struct {
		level    int
		expected string
	}{
		{AutonomyObserve, "observe only — nothing executes without approval"},
		{AutonomyApproved, "execute approved — modified verdicts require user approval"},
		{AutonomyReversible, "execute reversible — irreversible steps require approval"},
		{AutonomyBounded, "execute bounded — irreversible allowed after adversarial review"},
		{AutonomyTrusted, "trusted autonomy — only blocked verdicts halt execution"},
		{-1, "unknown level -1"},
		{99, "unknown level 99"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.expected, func(t *testing.T) {
			got := AutonomyLevelText(tt.level)
			if got != tt.expected {
				t.Errorf("AutonomyLevelText(%d) = %q, want %q", tt.level, got, tt.expected)
			}
		})
	}
}

func TestDefaults_NewFields(t *testing.T) {
	cfg := Defaults()

	if cfg.DirectivesImmutablePath != "/etc/hyperi/directives-immutable.yaml" {
		t.Errorf("expected DirectivesImmutablePath '/etc/hyperi/directives-immutable.yaml', got %q", cfg.DirectivesImmutablePath)
	}
	if cfg.DirectivesMutablePath != "/var/lib/hyperi/directives-mutable.yaml" {
		t.Errorf("expected DirectivesMutablePath '/var/lib/hyperi/directives-mutable.yaml', got %q", cfg.DirectivesMutablePath)
	}
	if cfg.GoalStoragePath != "/var/lib/hyperi/goals.json" {
		t.Errorf("expected GoalStoragePath '/var/lib/hyperi/goals.json', got %q", cfg.GoalStoragePath)
	}
	if cfg.MemoryStoragePath != "/var/lib/hyperi/memory" {
		t.Errorf("expected MemoryStoragePath '/var/lib/hyperi/memory', got %q", cfg.MemoryStoragePath)
	}
	if cfg.ToolAuthStoragePath != "/var/lib/hyperi/tool_auth.json" {
		t.Errorf("expected ToolAuthStoragePath '/var/lib/hyperi/tool_auth.json', got %q", cfg.ToolAuthStoragePath)
	}
	if cfg.MaxAgentSpawnLimit != 5 {
		t.Errorf("expected MaxAgentSpawnLimit 5, got %d", cfg.MaxAgentSpawnLimit)
	}
	if cfg.LLMProvider != "anthropic" {
		t.Errorf("expected LLMProvider 'anthropic', got %q", cfg.LLMProvider)
	}
}

func TestSaveLoad_RoundTrip_NewFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	original := Defaults()
	original.DirectivesImmutablePath = "/custom/immutable.yaml"
	original.DirectivesMutablePath = "/custom/mutable.yaml"
	original.GoalStoragePath = "/custom/goals.json"
	original.MemoryStoragePath = "/custom/memory"
	original.ToolAuthStoragePath = "/custom/tool_auth.json"
	original.MaxAgentSpawnLimit = 10
	original.LLMProvider = "openai"
	original.LLMModel = "gpt-4"

	if err := Save(path, original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.DirectivesImmutablePath != original.DirectivesImmutablePath {
		t.Errorf("DirectivesImmutablePath: expected %q, got %q", original.DirectivesImmutablePath, loaded.DirectivesImmutablePath)
	}
	if loaded.DirectivesMutablePath != original.DirectivesMutablePath {
		t.Errorf("DirectivesMutablePath: expected %q, got %q", original.DirectivesMutablePath, loaded.DirectivesMutablePath)
	}
	if loaded.GoalStoragePath != original.GoalStoragePath {
		t.Errorf("GoalStoragePath: expected %q, got %q", original.GoalStoragePath, loaded.GoalStoragePath)
	}
	if loaded.MemoryStoragePath != original.MemoryStoragePath {
		t.Errorf("MemoryStoragePath: expected %q, got %q", original.MemoryStoragePath, loaded.MemoryStoragePath)
	}
	if loaded.ToolAuthStoragePath != original.ToolAuthStoragePath {
		t.Errorf("ToolAuthStoragePath: expected %q, got %q", original.ToolAuthStoragePath, loaded.ToolAuthStoragePath)
	}
	if loaded.MaxAgentSpawnLimit != original.MaxAgentSpawnLimit {
		t.Errorf("MaxAgentSpawnLimit: expected %d, got %d", original.MaxAgentSpawnLimit, loaded.MaxAgentSpawnLimit)
	}
	if loaded.LLMProvider != original.LLMProvider {
		t.Errorf("LLMProvider: expected %q, got %q", original.LLMProvider, loaded.LLMProvider)
	}
	if loaded.LLMModel != original.LLMModel {
		t.Errorf("LLMModel: expected %q, got %q", original.LLMModel, loaded.LLMModel)
	}
}