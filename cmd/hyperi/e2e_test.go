package main

import (
	"context"
	"strings"
	"testing"

	"github.com/isellar/hyperios/internal/audit"
	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/module"
	"github.com/isellar/hyperios/internal/processor"
)

// ── Mock LLM client ───────────────────────────────────────────────────────────

// mockCompleter satisfies llm.Completer without making real API calls.
// It returns a fixed JSON response that the agent parser accepts.
type mockCompleter struct {
	response string
	err      error
}

func (m *mockCompleter) Complete(_ context.Context, _, _ string) (string, error) {
	return m.response, m.err
}

func (m *mockCompleter) CompleteWithRetry(_ context.Context, _, _ string) (string, error) {
	return m.response, m.err
}

var _ llm.Completer = (*mockCompleter)(nil)

// ── Test helpers ──────────────────────────────────────────────────────────────

// testConfig returns a config with temp-dir storage paths so tests are
// hermetic and do not touch ~/.hyperi or /var/lib/hyperi.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.GoalStoragePath = dir + "/goals.json"
	cfg.MemoryStoragePath = dir + "/memory"
	cfg.ToolAuthStoragePath = dir + "/tool_auth.json"
	return cfg
}

// testAuditLogger returns an audit.Logger that writes to a temp directory.
func testAuditLogger(t *testing.T) *audit.Logger {
	t.Helper()
	dir := t.TempDir()
	log, err := audit.NewLogger(dir + "/audit.jsonl")
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}
	return log
}

// testWire constructs fully-wired Modules for tests. It sets HYPERI_DATA_DIR
// to a temp directory so all path resolution stays hermetic.
func testWire(t *testing.T) *Modules {
	t.Helper()
	cfg := testConfig(t)
	t.Setenv("HYPERI_DATA_DIR", t.TempDir())

	// Use a real *llm.Client with no API key — no calls will be made in the
	// health/capability tests.
	llmClient := llm.NewClient("")
	auditLog := testAuditLogger(t)

	mods, err := WireModules(cfg, llmClient, auditLog)
	if err != nil {
		t.Fatalf("WireModules: %v", err)
	}
	return mods
}

// allModules returns the named-module cases shared by the health/report/
// capabilities/names tests below.
func allModules(mods *Modules) []struct {
	name string
	mod  module.Module
} {
	return []struct {
		name string
		mod  module.Module
	}{
		{"goal_fulfillment", mods.GoalFulfillment},
		{"processor", mods.Processor},
		{"memory", mods.Memory},
		{"self_improvement", mods.SelfImprovement},
		{"io_toolbox", mods.IOToolbox},
	}
}

// ── TestWireModules ───────────────────────────────────────────────────────────

// TestWireModules verifies that WireModules returns a fully populated Modules
// struct (no nil fields) when given a valid config and a stub LLM client.
func TestWireModules(t *testing.T) {
	cfg := testConfig(t)
	t.Setenv("HYPERI_DATA_DIR", t.TempDir())

	llmClient := llm.NewClient("")
	auditLog := testAuditLogger(t)

	mods, err := WireModules(cfg, llmClient, auditLog)
	if err != nil {
		t.Fatalf("WireModules returned error: %v", err)
	}
	if mods == nil {
		t.Fatal("WireModules returned nil Modules")
	}
	if mods.GoalFulfillment == nil {
		t.Error("GoalFulfillment is nil after wiring")
	}
	if mods.Processor == nil {
		t.Error("Processor is nil after wiring")
	}
	if mods.Memory == nil {
		t.Error("Memory is nil after wiring")
	}
	if mods.SelfImprovement == nil {
		t.Error("SelfImprovement is nil after wiring")
	}
	if mods.IOToolbox == nil {
		t.Error("IOToolbox is nil after wiring")
	}
	if mods.ResultStore == nil {
		t.Error("ResultStore is nil after wiring")
	}
}

// ── TestModuleHealthAll ───────────────────────────────────────────────────────

// TestModuleHealthAll verifies all wired modules report "healthy" status.
func TestModuleHealthAll(t *testing.T) {
	mods := testWire(t)

	for _, tc := range allModules(mods) {
		h := tc.mod.Health()
		if h.Status != "healthy" {
			t.Errorf("module %q: expected health %q, got %q (details: %s)",
				tc.name, "healthy", h.Status, h.Details)
		}
		if h.Timestamp.IsZero() {
			t.Errorf("module %q: Health().Timestamp is zero", tc.name)
		}
	}
}

// ── TestFullGoalPipeline ──────────────────────────────────────────────────────

// TestFullGoalPipeline exercises the full goal path using a mock LLM client:
//  1. GoalFulfillment.SubmitGoal  — creates a tracked Goal
//  2. Processor.QueueGoal         — enqueue (no policy layer in the MVP path)
//  3. Processor.RunNext           — dequeue + agent spawn via mock LLM -> AgentResult
//
// The processor is constructed independently with a mock LLM client so no
// real API calls occur.  GoalFulfillment is still the wired real instance so
// state tracking is exercised end-to-end.
func TestFullGoalPipeline(t *testing.T) {
	cfg := testConfig(t)
	t.Setenv("HYPERI_DATA_DIR", t.TempDir())

	auditLog := testAuditLogger(t)

	// Wire the full module set (real client, no API key — not called here).
	mods, err := WireModules(cfg, llm.NewClient(""), auditLog)
	if err != nil {
		t.Fatalf("WireModules: %v", err)
	}

	// Build a standalone Processor backed by the mock LLM so that RunNext
	// does not attempt real HTTP calls.  Wire it with the real GoalFulfillment
	// so goal-state updates exercise real tracker logic.
	mock := &mockCompleter{response: `{"output":"stub result","success":true}`}
	proc := processor.NewProcessor(cfg, mock, auditLog)
	proc.SetGoalFulfillment(mods.GoalFulfillment)

	// 1. Submit a goal.
	goal, err := mods.GoalFulfillment.SubmitGoal("list files in /tmp")
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}
	if goal == nil {
		t.Fatal("SubmitGoal returned nil goal")
	}
	if goal.ID == "" {
		t.Error("SubmitGoal: goal ID is empty")
	}

	// 2. Queue the goal.
	if err := proc.QueueGoal(goal); err != nil {
		t.Fatalf("QueueGoal: %v", err)
	}

	// 3. Run the next queued goal.
	result, err := proc.RunNext()
	if err != nil {
		t.Fatalf("RunNext: %v", err)
	}
	if result == nil {
		t.Fatal("RunNext returned nil result")
	}
}

// ── TestWireModules_DefaultsOnMissingConfig ────────────────────────────────

// TestWireModules_DefaultsOnMissingConfig verifies that config.Load returns
// safe defaults (no error) when the config file does not exist.
func TestWireModules_DefaultsOnMissingConfig(t *testing.T) {
	cfg, err := config.Load("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("config.Load with missing file should return defaults, got error: %v", err)
	}
	if cfg == nil {
		t.Fatal("config.Load returned nil config for missing file")
	}
	if cfg.AutonomyLevel != config.AutonomyApproved {
		t.Errorf("expected default autonomy level %d, got %d",
			config.AutonomyApproved, cfg.AutonomyLevel)
	}
	if cfg.MaxAgentSpawnLimit == 0 {
		t.Error("expected non-zero MaxAgentSpawnLimit in defaults")
	}
}

// ── TestModuleCapabilities ────────────────────────────────────────────────────

// TestModuleCapabilities verifies all wired modules return non-empty
// Capabilities slices with no empty strings.
func TestModuleCapabilities(t *testing.T) {
	mods := testWire(t)

	for _, tc := range allModules(mods) {
		caps := tc.mod.Capabilities()
		if len(caps) == 0 {
			t.Errorf("module %q returned empty Capabilities()", tc.name)
		}
		for _, c := range caps {
			if strings.TrimSpace(c) == "" {
				t.Errorf("module %q: Capabilities() contains an empty string", tc.name)
			}
		}
	}
}

// ── TestModuleNames ───────────────────────────────────────────────────────────

// TestModuleNames verifies the Name() method of each module returns its
// expected string identifier.
func TestModuleNames(t *testing.T) {
	mods := testWire(t)

	for _, tc := range allModules(mods) {
		if got := tc.mod.Name(); got != tc.name {
			t.Errorf("expected module name %q, got %q", tc.name, got)
		}
	}
}
