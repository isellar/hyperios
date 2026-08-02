package processor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/audit"
	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/module"
	"github.com/isellar/hyperios/internal/types"
)

// GoalUpdater is the narrow interface the Processor uses from GoalFulfillment.
type GoalUpdater interface {
	UpdateGoalState(id string, state types.GoalState) error
}

// DefaultGoalTimeout bounds how long a single goal's agent run (the whole
// tool-use loop, not one LLM call) is allowed to take before being cancelled.
// Generous by design: HyperiOS is meant to let long-running goals take their
// time rather than fail fast, especially on a local model that trades speed
// for zero API cost. Override with Processor.SetGoalTimeout.
const DefaultGoalTimeout = 30 * time.Minute

// Processor prioritises goals, delegates to autonomous agents, and fans
// execution results back to the goal-fulfilment system.
// It implements module.Module.
type Processor struct {
	cfg         *config.Config
	prioritizer *Prioritizer
	spawner     *AgentSpawner
	goalUpdater GoalUpdater
	memory      MemoryQuerier
	audit       *audit.Logger
	sessionID   string
	goalTimeout time.Duration
}

// NewProcessor constructs a Processor. Dependencies (GoalFulfillment, Memory)
// are injected via the Set* methods so that callers can wire them after
// construction without circular-import issues.
func NewProcessor(cfg *config.Config, llmClient llm.Completer, auditLog *audit.Logger) *Processor {
	if cfg == nil {
		cfg = config.Defaults()
	}
	return &Processor{
		cfg:         cfg,
		prioritizer: NewPrioritizer(),
		spawner:     NewAgentSpawner(llmClient),
		audit:       auditLog,
		goalTimeout: DefaultGoalTimeout,
	}
}

// SetToolbox injects a ToolCaller implementation used by spawned agents to
// invoke I/O tools (shell, notify, schedule) via LLM tool-use.
func (p *Processor) SetToolbox(tb ToolCaller) {
	p.spawner.SetToolbox(tb)
}

// SetGoalTimeout overrides how long a single goal's agent run may take
// before being cancelled. d <= 0 is ignored (keeps the current value); pass
// a large value (or wire a very large one from config) for goals that need
// to run for a long time.
func (p *Processor) SetGoalTimeout(d time.Duration) {
	if d > 0 {
		p.goalTimeout = d
	}
}

// SetMaxToolIterations overrides how many tool-call round-trips a single
// agent run may perform. See AgentSpawner.SetMaxToolIterations.
func (p *Processor) SetMaxToolIterations(n int) {
	p.spawner.SetMaxToolIterations(n)
}

// SetGoalFulfillment injects a GoalUpdater implementation.
func (p *Processor) SetGoalFulfillment(gf GoalUpdater) {
	p.goalUpdater = gf
}

// SetMemory injects a MemoryQuerier implementation.
func (p *Processor) SetMemory(m MemoryQuerier) {
	p.memory = m
}

// SetSessionID sets the audit session identifier.
func (p *Processor) SetSessionID(id string) {
	p.sessionID = id
}

// QueueGoal enqueues a goal for execution.
//
// As part of enqueueing, the goal's state is transitioned to GoalStateActive
// so that the Prioritizer will return it on the next RunNext call.  If a
// GoalUpdater is wired the transition is persisted there too; otherwise only
// the in-memory Goal struct is updated (which is sufficient because the
// Prioritizer holds a pointer to the same struct).
func (p *Processor) QueueGoal(goal *types.Goal) error {
	if goal == nil {
		return fmt.Errorf("processor: goal must not be nil")
	}

	// Transition to Active so the Prioritizer can dequeue this goal.
	// This is the canonical Refining → Active transition: the goal has been
	// reviewed, approved, and is now ready for execution.
	//
	// If a GoalUpdater is wired (it holds a pointer to the same *types.Goal)
	// we delegate the state update so it can persist and validate the
	// transition.  When no GoalUpdater is wired we mutate the struct directly;
	// the Prioritizer holds the same pointer so it will see GoalStateActive.
	if goal.State != types.GoalStateActive {
		if p.goalUpdater != nil {
			// UpdateGoalState mutates goal.State via the shared pointer.
			_ = p.goalUpdater.UpdateGoalState(goal.ID, types.GoalStateActive)
		} else {
			goal.State = types.GoalStateActive
		}
	}

	p.prioritizer.Enqueue(goal)

	if p.audit != nil {
		_ = p.audit.LogAgentSpawn(p.sessionID, audit.AgentSpawnEvent{
			AgentType:  "processor-queue",
			Reason:     fmt.Sprintf("goal %s enqueued", goal.ID),
			SpawnCount: p.prioritizer.Len(),
		})
	}

	return nil
}

// RunNext dequeues the highest-priority Active goal, spawns an agent, updates
// goal state via GoalFulfillment, and returns the AgentResult.
// Returns (nil, nil) when the queue is empty.
func (p *Processor) RunNext() (*AgentResult, error) {
	goal, ok := p.prioritizer.Next()
	if !ok {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.goalTimeout)
	defer cancel()

	var directives []types.Directive
	if p.memory != nil {
		if d, dErr := p.memory.ListDirectives(); dErr == nil {
			directives = d
		}
		// A directive-lookup failure is non-fatal — the agent still runs,
		// just without the extra constraints for this one goal.
	}

	agent, err := p.spawner.Spawn(ctx, goal, directives, p.memory)
	if err != nil {
		// Spawn itself only returns an error for programming mistakes (nil goal);
		// LLM errors are captured inside the Agent. Propagate true errors.
		return nil, fmt.Errorf("processor: spawn agent for goal %s: %w", goal.ID, err)
	}

	if p.audit != nil {
		_ = p.audit.LogAgentSpawn(p.sessionID, audit.AgentSpawnEvent{
			AgentType:  "autonomous",
			ParentID:   goal.ID,
			Reason:     fmt.Sprintf("executing goal: %s", goal.Description),
			SpawnCount: 1,
		})
	}

	agent.Result.GoalID = goal.ID

	// Fan result back to GoalFulfillment.
	if p.goalUpdater != nil {
		newState := types.GoalStateDone
		if !agent.Result.Success {
			newState = types.GoalStateBlocked
		}
		if updateErr := p.goalUpdater.UpdateGoalState(goal.ID, newState); updateErr != nil {
			// Log but don't fail the overall result.
			_ = updateErr
		}
	}

	return agent.Result, nil
}

// RunLoop continuously drains the goal queue in the background, calling
// RunNext whenever a goal is available and sleeping for pollInterval when the
// queue is empty. It blocks until ctx is cancelled. Intended to be started
// once as a goroutine at process startup so that goals queued via QueueGoal
// (e.g. from an HTTP handler) are executed fire-and-forget, without the
// caller waiting for RunNext to complete.
//
// onResult, if non-nil, is called with the AgentResult after each goal run
// (including failed runs). It must not block for long, since it runs inline
// in the loop.
func (p *Processor) RunLoop(ctx context.Context, pollInterval time.Duration, onResult func(*AgentResult)) {
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		result, err := p.RunNext()
		if err != nil || result == nil {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}

		if onResult != nil {
			onResult(result)
		}
	}
}

// LookupInfo queries memory for context related to query.
// Used by agents during goal refinement.
// Returns a formatted string of all matching memory entries.
func (p *Processor) LookupInfo(query string) (string, error) {
	if p.memory == nil {
		return "", nil
	}

	entries, err := p.memory.SearchContext(query)
	if err != nil {
		return "", fmt.Errorf("processor: memory search: %w", err)
	}

	if len(entries) == 0 {
		return "", nil
	}

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("[%s] %v\n", e.Key, e.Value))
	}
	return strings.TrimSpace(sb.String()), nil
}

// Lookup satisfies the goal_fulfillment.ProcessorProvider interface.
func (p *Processor) Lookup(query string) (string, error) {
	return p.LookupInfo(query)
}

// ActiveModelInfo describes which LLM backend is configured and, if
// determinable at runtime, which one actually served the most recent call.
// Intended for status/reporting surfaces (e.g. the web UI status bar), not
// for per-call attribution.
type ActiveModelInfo struct {
	// Provider/Model describe the configured remote fallback (or primary,
	// if local is disabled). Provider defaults to "anthropic" when unset.
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`

	// LocalEnabled/LocalModel describe the configured local (Ollama) model,
	// if any. LocalModel is empty when LocalEnabled is false.
	LocalEnabled bool   `json:"local_enabled"`
	LocalModel   string `json:"local_model,omitempty"`

	// LastUsedLocal reports whether the most recent LLM call was served by
	// the local model rather than falling back to remote. Only meaningful
	// when LocalEnabled is true and at least one call has been made; it is
	// always false otherwise.
	LastUsedLocal bool `json:"last_used_local"`
}

// ModelInfo returns the current LLM backend configuration plus, when the
// wired llm.Completer is a *llm.HybridCompleter, the last-used-local signal.
func (p *Processor) ModelInfo() ActiveModelInfo {
	provider := p.cfg.LLMProvider
	if provider == "" {
		provider = config.ProviderAnthropic
	}

	info := ActiveModelInfo{
		Provider:     provider,
		Model:        p.cfg.LLMModel,
		LocalEnabled: p.cfg.LocalModelEnabled,
		LocalModel:   p.cfg.LocalModelName,
	}

	if hc, ok := p.spawner.llmClient.(*llm.HybridCompleter); ok {
		info.LastUsedLocal = hc.LastUsedLocal()
	}

	return info
}

// QueuedGoals returns the number of goals currently waiting in the
// prioritizer queue.
func (p *Processor) QueuedGoals() int {
	return p.prioritizer.Len()
}

// ---------------------------------------------------------------------------
// module.Module implementation
// ---------------------------------------------------------------------------

// Name returns the module identifier.
func (p *Processor) Name() string { return "processor" }

// Health reports the current operational status of the Processor.
func (p *Processor) Health() module.ModuleHealth {
	status := "healthy"
	details := fmt.Sprintf("queued_goals=%d", p.prioritizer.Len())

	if p.goalUpdater == nil {
		status = "degraded"
		details += "; goal_fulfillment not wired"
	}

	return module.ModuleHealth{
		Status:    status,
		Details:   details,
		Timestamp: time.Now(),
	}
}

// Report returns operational metrics for the processor within window.
func (p *Processor) Report(_ context.Context, window time.Duration) (module.ModuleReport, error) {
	return module.ModuleReport{
		ModuleName: p.Name(),
		Window:     window,
		Metrics: map[string]any{
			"queued_goals": p.prioritizer.Len(),
		},
	}, nil
}

// Tune applies a TuningChange. No parameters are supported yet.
func (p *Processor) Tune(_ context.Context, change module.TuningChange) error {
	_ = change
	return nil
}

// Capabilities returns the OS capability types required by this module.
func (p *Processor) Capabilities() []string {
	return []string{
		"read:file",
		"execute:shell",
		"network:outbound",
	}
}

// Compile-time assertion.
var _ module.Module = (*Processor)(nil)
