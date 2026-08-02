package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/isellar/hyperios/internal/audit"
	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/goal_fulfillment"
	"github.com/isellar/hyperios/internal/io_toolbox"
	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/memory"
	"github.com/isellar/hyperios/internal/processor"
	"github.com/isellar/hyperios/internal/self_improvement"
)

// Modules holds all wired module instances.
//
// NOTE: this is the MVP wiring — there is intentionally no governor /
// capability-allowlist / policy-arbiter layer in this path. The spawned
// agent can call the shell tool directly with no further gating. Re-introduce
// a policy layer before running this against an untrusted or production
// environment.
type Modules struct {
	GoalFulfillment *goal_fulfillment.GoalFulfillment
	Processor       *processor.Processor
	Memory          *memory.Memory
	SelfImprovement *self_improvement.SelfImprovement
	IOToolbox       *io_toolbox.IOToolbox
	// ResultStore persists AgentResults (goal outcomes) to disk so they
	// survive a process restart — see processor.ResultStore.
	ResultStore *processor.ResultStore
}

// memoryAdapter bridges *memory.Memory to the goal_fulfillment.MemoryProvider
// interface.  goal_fulfillment expects StoreContext(key, value string) while
// memory.Memory.StoreContext accepts any value — the adapter casts to string.
type memoryAdapter struct {
	m *memory.Memory
}

func (a *memoryAdapter) GetContext(key string) (string, error) {
	return a.m.GetContext(key)
}

func (a *memoryAdapter) StoreContext(key, value string) error {
	return a.m.StoreContext(key, value)
}

// WireModules constructs and cross-wires all HyperiOS modules.
//
// Construction order:
//  1. Memory          — no external deps
//  2. IOToolbox       — no module deps
//  3. Processor       — constructed first, deps injected via Set* to break cycles
//  4. GoalFulfillment — needs Memory + Processor (via interfaces)
//  5. SelfImprovement — needs GoalFulfillment (injected via SetGoalFulfillment)
//
// After construction, bidirectional wiring is completed:
//   - Processor.SetToolbox(ioToolbox)
//   - Processor.SetGoalFulfillment(goalFulfillment)
//   - Processor.SetMemory(mem)
//   - SelfImprovement.SetGoalFulfillment(goalFulfillment)
//   - SelfImprovement.SetDirectiveStore(mem)
func WireModules(cfg *config.Config, llmClient llm.Completer, auditLog *audit.Logger) (*Modules, error) {
	// ── 1. Memory ─────────────────────────────────────────────────────────────
	mem := memory.NewMemory(cfg)

	// ── 2. IOToolbox ──────────────────────────────────────────────────────────
	toolbox := io_toolbox.NewIOToolbox(cfg)

	// ── 3. Processor (deps injected after goal_fulfillment is ready) ──────────
	proc := processor.NewProcessor(cfg, llmClient, auditLog)
	proc.SetToolbox(toolbox)
	if cfg.GoalTimeoutMinutes > 0 {
		proc.SetGoalTimeout(time.Duration(cfg.GoalTimeoutMinutes) * time.Minute)
	}
	if cfg.MaxToolIterations > 0 {
		proc.SetMaxToolIterations(cfg.MaxToolIterations)
	}

	// ── 4. GoalFulfillment ────────────────────────────────────────────────────
	goalStoragePath := cfg.GoalStoragePath
	if goalStoragePath == "" {
		goalStoragePath = filepath.Join(resolveDataDir(), "goals.json")
	}
	goalDataDir := filepath.Dir(goalStoragePath)
	if err := os.MkdirAll(goalDataDir, 0o750); err != nil {
		return nil, fmt.Errorf("wire: create goal storage dir: %w", err)
	}

	gf, err := goal_fulfillment.New(llmClient, &memoryAdapter{m: mem}, proc, goalDataDir)
	if err != nil {
		return nil, fmt.Errorf("wire: goal_fulfillment: %w", err)
	}

	// ── 4b. ResultStore ───────────────────────────────────────────────────────
	// Colocated with goals.json by default so a single data directory holds
	// both a goal's state and its most recent execution outcome.
	resultsPath := cfg.AgentResultsStoragePath
	if resultsPath == "" {
		resultsPath = filepath.Join(goalDataDir, "agent_results.json")
	}
	resultStore, err := processor.NewResultStore(resultsPath)
	if err != nil {
		return nil, fmt.Errorf("wire: result store: %w", err)
	}

	// ── 5. SelfImprovement ────────────────────────────────────────────────────
	si := self_improvement.NewSelfImprovement(cfg, llmClient, auditLog)

	// ── Bidirectional wiring ──────────────────────────────────────────────────
	proc.SetGoalFulfillment(gf)
	proc.SetMemory(mem)

	si.SetGoalFulfillment(gf)
	si.SetDirectiveStore(mem)

	// ── Self-modification ────────────────────────────────────────────────────
	// Enabled when: (a) explicitly confirmed via 'hyperi selfmodify enable',
	// OR (b) the source dir and binary path can be resolved at serve time
	// (which they always can on a normal install — source is the current
	// working directory if not set, binary is the running executable).
	// This makes self-modification on by default: the agent can improve its
	// own code without the user having to know the concept exists. The CLI
	// commands ('hyperi selfmodify disable/enable/rollback') remain available
	// for explicit control and rollback. 'disable' sets the config flag to
	// false which overrides the auto-enable.
	selfModifyEnabled := cfg.SelfModifyEnabled || !cfg.SelfModifyExplicitlyDisabled
	if selfModifyEnabled {
		mgr := buildSelfModifyManagerForServer(cfg)
		toolbox.EnableSelfModify(mgr)
	}

	return &Modules{
		GoalFulfillment: gf,
		Processor:       proc,
		Memory:          mem,
		SelfImprovement: si,
		IOToolbox:       toolbox,
		ResultStore:     resultStore,
	}, nil
}
