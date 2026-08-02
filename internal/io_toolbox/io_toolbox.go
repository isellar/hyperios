package io_toolbox

import (
	"context"
	"fmt"
	"time"

	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/io_toolbox/tools"
	"github.com/isellar/hyperios/internal/module"
	"github.com/isellar/hyperios/internal/scheduler"
	"github.com/isellar/hyperios/internal/selfmodify"
)

// IOToolbox is a module that exposes a set of I/O tools to agents.
// It implements module.Module for integration into the self-tuning loop.
type IOToolbox struct {
	cfg      *config.Config
	registry *ToolRegistry
	sched    *scheduler.Scheduler
}

// NewIOToolbox creates an IOToolbox, registers the built-in tools, and returns it.
// If cfg is nil, config.Defaults() is used.
func NewIOToolbox(cfg *config.Config) *IOToolbox {
	if cfg == nil {
		cfg = config.Defaults()
	}

	sched := scheduler.New(nil)
	sched.Start()

	tb := &IOToolbox{
		cfg:      cfg,
		registry: NewToolRegistry(),
		sched:    sched,
	}

	tb.registry.Register(tools.NewShellTool())
	tb.registry.Register(tools.NewNotifyTool())
	tb.registry.Register(tools.NewScheduleTool(sched))

	return tb
}

// EnableSelfModify registers the "self_modify" tool backed by mgr, letting
// agents rebuild/verify/apply changes to HyperiOS's own source tree. Not
// called by NewIOToolbox — only wired when the user has explicitly opted in
// (see cmd/hyperi's 'hyperi selfmodify enable' and buildLLMClient/wiring.go).
// Calling this again with a new mgr replaces the previous registration.
func (tb *IOToolbox) EnableSelfModify(mgr *selfmodify.Manager) {
	tb.registry.Register(tools.NewSelfModifyTool(mgr))
}

// DisableSelfModify removes the "self_modify" tool from the registry, if
// present. Safe to call even if it was never registered.
func (tb *IOToolbox) DisableSelfModify() {
	tb.registry.Remove("self_modify")
}

// GetTool returns the named tool from the registry.
func (tb *IOToolbox) GetTool(name string) (Tool, bool) {
	return tb.registry.Get(name)
}

// DescribeTool returns the human-readable description for a registered tool,
// or ("", false) if no tool with that name is registered. Satisfies the
// processor.ToolCaller interface.
func (tb *IOToolbox) DescribeTool(name string) (string, bool) {
	t, ok := tb.registry.Get(name)
	if !ok {
		return "", false
	}
	return t.Description(), true
}

// ListTools returns the names of all registered tools.
func (tb *IOToolbox) ListTools() []string {
	return tb.registry.List()
}

// ExecuteTool runs the named tool with the given input.
// Returns an error if the tool does not exist.
func (tb *IOToolbox) ExecuteTool(name, input string) (string, error) {
	return tb.registry.Execute(name, input)
}

// ── module.Module implementation ──────────────────────────────────────────────

// Name returns the module identifier.
func (tb *IOToolbox) Name() string { return "io_toolbox" }

// Health returns the current health of the IOToolbox.
// The toolbox is healthy as long as at least one tool is registered.
func (tb *IOToolbox) Health() module.ModuleHealth {
	if len(tb.registry.List()) == 0 {
		return module.ModuleHealth{
			Status:    "unhealthy",
			Details:   "no tools registered",
			Timestamp: time.Now(),
		}
	}
	return module.ModuleHealth{
		Status:    "healthy",
		Details:   fmt.Sprintf("%d tool(s) registered", len(tb.registry.List())),
		Timestamp: time.Now(),
	}
}

// Capabilities returns the list of registered tool names as capability identifiers.
func (tb *IOToolbox) Capabilities() []string {
	return tb.registry.List()
}

// Report returns a ModuleReport summarising the registered tools.
func (tb *IOToolbox) Report(_ context.Context, window time.Duration) (module.ModuleReport, error) {
	names := tb.registry.List()
	toolMap := make(map[string]any, len(names))
	for _, n := range names {
		if t, ok := tb.registry.Get(n); ok {
			toolMap[n] = t.Description()
		}
	}
	return module.ModuleReport{
		ModuleName: tb.Name(),
		Window:     window,
		Metrics: map[string]any{
			"registered_tools": len(names),
			"tools":            toolMap,
		},
	}, nil
}

// Tune is a no-op for IOToolbox; it has no tunable parameters.
func (tb *IOToolbox) Tune(_ context.Context, _ module.TuningChange) error {
	return nil
}
