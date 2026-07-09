// Package config manages the HyperiOS global runtime configuration.
// Configuration is stored as JSON at /var/lib/hyperi/config.json (or
// ~/.hyperi/config.json in development). It is read at startup and updated
// by the 'hyperi config set' command.
//
// Autonomy level controls when the agent pauses and asks vs proceeds on its
// own judgment. It does not control what can execute — that is the job of
// the OS permissions layer and the capability allowlist. The autonomy level
// is the soft policy layer above those two hard layers.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Autonomy level constants.
const (
	// AutonomyObserve — all steps require explicit user approval; nothing executes automatically.
	AutonomyObserve = 0
	// AutonomyApproved — execute arbiter-approved steps; modified verdicts require approval.
	AutonomyApproved = 1
	// AutonomyReversible — reversible steps execute without prompt; irreversible require approval.
	AutonomyReversible = 2
	// AutonomyBounded — irreversible steps execute after adversarial review; only block → blocked.
	AutonomyBounded = 3
	// AutonomyTrusted — only block flags produce blocked; everything else approved without prompt.
	AutonomyTrusted = 4
)

// LLM provider identifiers for LLMProvider.
const (
	// ProviderAnthropic talks directly to api.anthropic.com using ANTHROPIC_API_KEY.
	ProviderAnthropic = "anthropic"
	// ProviderOpenCodeZen routes through OpenCode Zen (opencode.ai/zen), an
	// Anthropic-messages-compatible gateway to many models. Useful as a
	// fallback when the Anthropic account is out of quota/tokens.
	ProviderOpenCodeZen = "opencode-zen"
)

// Config is the global runtime configuration for HyperiOS.
type Config struct {
	// AutonomyLevel controls when the agent acts without asking.
	// Default: 1 (execute approved steps; modified verdicts require user approval).
	AutonomyLevel int `json:"autonomy_level"`

	// ApprovalTimeoutForeground is how long to wait for user approval in a
	// foreground (interactive) session before halting.
	ApprovalTimeoutForeground int `json:"approval_timeout_foreground_seconds"`

	// ApprovalTimeoutBackground is how long to wait for user approval in a
	// background (scheduled) session before halting.
	ApprovalTimeoutBackground int `json:"approval_timeout_background_seconds"`

	// WatchPaths are the filesystem paths the manifest watcher monitors.
	WatchPaths []string `json:"watch_paths,omitempty"`

	// AutonomyUpdatedAt is when the autonomy level was last changed.
	AutonomyUpdatedAt time.Time `json:"autonomy_updated_at,omitempty"`

	// AutonomyUpdatedBy is who changed the autonomy level.
	AutonomyUpdatedBy string `json:"autonomy_updated_by,omitempty"`

	// Voice configuration (Phase 3)
	// WhisperModelPath is the path to the whisper.cpp GGML model file.
	// Default: /var/lib/hyperi/models/ggml-tiny.en.bin
	WhisperModelPath string `json:"whisper_model_path,omitempty"`

	// WhisperCLIPath is the path to the whisper-cli binary.
	// Default: /usr/local/bin/whisper-cli
	WhisperCLIPath string `json:"whisper_cli_path,omitempty"`

	// VoiceEnabled controls whether push-to-talk is available in the TUI.
	VoiceEnabled bool `json:"voice_enabled"`

	// VoicePushToTalkKey is the key binding for push-to-talk recording.
	// Default: "ctrl+space"
	VoicePushToTalkKey string `json:"voice_push_to_talk_key,omitempty"`

	// GeneratorConfig controls template generation behavior.
	Generator GeneratorConfig `json:"generator,omitempty"`

	// DirectivesImmutablePath is the path to the immutable safety directives YAML file.
	// Default: /etc/hyperi/directives-immutable.yaml
	DirectivesImmutablePath string `json:"directives_immutable_path,omitempty"`

	// DirectivesMutablePath is the path to the user-modifiable directives YAML file.
	// Default: /var/lib/hyperi/directives-mutable.yaml
	DirectivesMutablePath string `json:"directives_mutable_path,omitempty"`

	// GoalStoragePath is the path to the goal state storage file.
	// Default: /var/lib/hyperi/goals.json
	GoalStoragePath string `json:"goal_storage_path,omitempty"`

	// MemoryStoragePath is the path to the agent memory storage directory.
	// Default: /var/lib/hyperi/memory
	MemoryStoragePath string `json:"memory_storage_path,omitempty"`

	// ToolAuthStoragePath is the path to the tool authorization persistence file.
	// Default: /var/lib/hyperi/tool_auth.json
	ToolAuthStoragePath string `json:"tool_auth_storage_path,omitempty"`

	// MaxAgentSpawnLimit is the maximum number of concurrent sub-agents allowed.
	// Default: 5
	MaxAgentSpawnLimit int `json:"max_agent_spawn_limit,omitempty"`

	// LLMProvider is the model provider to use. One of ProviderAnthropic
	// (default) or ProviderOpenCodeZen.
	// Default: "anthropic"
	LLMProvider string `json:"llm_provider,omitempty"`

	// LLMModel is the model identifier (e.g. "claude-sonnet-4-20250514").
	// Default: "" (uses provider default)
	LLMModel string `json:"llm_model,omitempty"`

	// LLMAPIKey overrides the provider's default env-var-sourced API key.
	// For "anthropic" this overrides ANTHROPIC_API_KEY; for "opencode-zen"
	// this is the Zen API key (falls back to OPENCODE_API_KEY env var).
	LLMAPIKey string `json:"llm_api_key,omitempty"`
}

// GeneratorConfig controls the self-improvement template generator.
type GeneratorConfig struct {
	// MinClusterSize is the minimum number of plans needed to form a cluster.
	// Default: 3
	MinClusterSize int `json:"min_cluster_size"`

	// MinSuccessRate is the minimum success rate for source plans.
	// Default: 0.8
	MinSuccessRate float64 `json:"min_success_rate"`

	// AutoApprove deploys generated templates without user review.
	// Default: false
	AutoApprove bool `json:"auto_approve"`
}

// Defaults returns a Config with safe default values for a fresh install.
func Defaults() *Config {
	return &Config{
		AutonomyLevel:             AutonomyApproved,
		ApprovalTimeoutForeground: 300, // 5 minutes
		ApprovalTimeoutBackground: 30,  // 30 seconds
		WatchPaths: []string{
			"/etc",
			"/var/lib/hyperi",
			"/opt",
		},
		// Voice defaults
		WhisperModelPath:   "/var/lib/hyperi/models/ggml-tiny.en.bin",
		WhisperCLIPath:     "/usr/local/bin/whisper-cli",
		VoiceEnabled:       false, // opt-in; user enables after verifying audio works
		VoicePushToTalkKey: "ctrl+space",
		// Generator defaults
		Generator: GeneratorConfig{
			MinClusterSize: 3,
			MinSuccessRate: 0.8,
			AutoApprove:    false,
		},
		// Directive paths
		DirectivesImmutablePath: "/etc/hyperi/directives-immutable.yaml",
		DirectivesMutablePath:   "/var/lib/hyperi/directives-mutable.yaml",
		// Storage paths
		GoalStoragePath:     "/var/lib/hyperi/goals.json",
		MemoryStoragePath:   "/var/lib/hyperi/memory",
		ToolAuthStoragePath: "/var/lib/hyperi/tool_auth.json",
		// Agent limits
		MaxAgentSpawnLimit: 5,
		// LLM provider
		LLMProvider: ProviderAnthropic,
		LLMModel:    "",
	}
}

// Load reads the config from path. If the file does not exist, returns Defaults().
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Defaults(), nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	cfg := Defaults()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	return cfg, nil
}

// Save writes the config to path atomically.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("config: create dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}

	// Write to temp file then rename for atomicity
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return fmt.Errorf("config: write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("config: rename: %w", err)
	}

	return nil
}

// AutonomyLevelText returns a human-readable description of an autonomy level.
func AutonomyLevelText(level int) string {
	switch level {
	case AutonomyObserve:
		return "observe only — nothing executes without approval"
	case AutonomyApproved:
		return "execute approved — modified verdicts require user approval"
	case AutonomyReversible:
		return "execute reversible — irreversible steps require approval"
	case AutonomyBounded:
		return "execute bounded — irreversible allowed after adversarial review"
	case AutonomyTrusted:
		return "trusted autonomy — only blocked verdicts halt execution"
	default:
		return fmt.Sprintf("unknown level %d", level)
	}
}
