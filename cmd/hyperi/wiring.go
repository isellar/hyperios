package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/isellar/hyperios/internal/audit"
	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/goal_fulfillment"
	"github.com/isellar/hyperios/internal/governor"
	"github.com/isellar/hyperios/internal/governor/capability"
	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/memory"
	"github.com/isellar/hyperios/internal/processor"
	"github.com/isellar/hyperios/internal/self_improvement"
	"github.com/isellar/hyperios/internal/types"
)

// Modules holds all wired module instances.
type Modules struct {
	GoalFulfillment *goal_fulfillment.GoalFulfillment
	Governor        *governor.Governor
	Processor       *processor.Processor
	Memory          *memory.Memory
	SelfImprovement *self_improvement.SelfImprovement
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

// governorAdapter bridges *governor.Governor to the processor.GovernorReviewer
// interface, which requires both ReviewGoal and CheckToolAuthorized.
// Governor.ReviewGoal already exists; CheckToolAuthorized is delegated to
// Governor.ToolAuth().CheckAuthorization.
type governorAdapter struct {
	g *governor.Governor
}

func (a *governorAdapter) ReviewGoal(goal *types.Goal) (*governor.ReviewResult, error) {
	return a.g.ReviewGoal(goal)
}

func (a *governorAdapter) CheckToolAuthorized(toolID string) bool {
	return a.g.ToolAuth().CheckAuthorization(toolID)
}

// WireModules constructs and cross-wires all HyperiOS modules.
//
// Construction order:
//  1. Memory          — no external deps
//  2. Governor        — no module deps (uses capability registry)
//  3. Processor       — constructed first, deps injected via Set* to break cycles
//  4. GoalFulfillment — needs Memory + Processor (via interfaces)
//  5. SelfImprovement — needs GoalFulfillment (injected via SetGoalFulfillment)
//
// After construction, bidirectional wiring is completed:
//   - Processor.SetGovernor(governorAdapter)
//   - Processor.SetGoalFulfillment(goalFulfillment)
//   - Processor.SetMemory(mem)
//   - SelfImprovement.SetGoalFulfillment(goalFulfillment)
func WireModules(cfg *config.Config, llmClient *llm.Client, auditLog *audit.Logger) (*Modules, error) {
	// ── 1. Memory ─────────────────────────────────────────────────────────────
	mem := memory.NewMemory(cfg)

	// ── 2. Governor ───────────────────────────────────────────────────────────
	reg := capability.NewRegistry()
	cwd, _ := os.Getwd()
	reg.SetWorkspace(cwd)

	toolAuthPath := cfg.ToolAuthStoragePath
	if toolAuthPath == "" {
		toolAuthPath = filepath.Join(resolveDataDir(), "tool_auth.json")
	}

	gov := governor.New(governor.GovernorConfig{
		AutonomyLevel: cfg.AutonomyLevel,
		Registry:      reg,
		AuditLogger:   auditLog,
		SessionID:     "main",
		ToolAuthPath:  toolAuthPath,
	})

	// ── 3. Processor (deps injected after goal_fulfillment is ready) ──────────
	proc := processor.NewProcessor(cfg, llmClient, auditLog)

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

	// ── 5. SelfImprovement ────────────────────────────────────────────────────
	si := self_improvement.NewSelfImprovement(cfg, llmClient, auditLog)

	// ── Bidirectional wiring ──────────────────────────────────────────────────
	proc.SetGovernor(&governorAdapter{g: gov})
	proc.SetGoalFulfillment(gf)
	proc.SetMemory(mem)

	si.SetGoalFulfillment(gf)

	return &Modules{
		GoalFulfillment: gf,
		Governor:        gov,
		Processor:       proc,
		Memory:          mem,
		SelfImprovement: si,
	}, nil
}
