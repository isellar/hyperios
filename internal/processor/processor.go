package processor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/audit"
	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/governor"
	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/module"
	"github.com/isellar/hyperios/internal/types"
)

// GovernorReviewer is the narrow interface the Processor uses from the Governor.
// Using an interface here prevents a hard dependency on *governor.Governor and
// makes the Processor trivially testable with mocks.
type GovernorReviewer interface {
	ReviewGoal(goal *types.Goal) (*governor.ReviewResult, error)
	CheckToolAuthorized(toolID string) bool
}

// GoalUpdater is the narrow interface the Processor uses from GoalFulfillment.
type GoalUpdater interface {
	UpdateGoalState(id string, state types.GoalState) error
}

// Processor prioritises goals, delegates to autonomous agents, and fans
// execution results back to the goal-fulfilment system.
// It implements module.Module.
type Processor struct {
	cfg            *config.Config
	prioritizer    *Prioritizer
	spawner        *AgentSpawner
	governorRev    GovernorReviewer
	goalUpdater    GoalUpdater
	memory         MemoryQuerier
	audit          *audit.Logger
	sessionID      string
}

// NewProcessor constructs a Processor. Dependencies (Governor, GoalFulfillment,
// Memory) are injected via the Set* methods so that callers can wire them after
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
	}
}

// SetGovernor injects a GovernorReviewer implementation.
func (p *Processor) SetGovernor(g GovernorReviewer) {
	p.governorRev = g
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

// QueueGoal subjects the goal to a governor review, then enqueues it if
// approved. Returns an error if the goal is rejected.
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

	if p.governorRev != nil {
		result, err := p.governorRev.ReviewGoal(goal)
		if err != nil {
			return fmt.Errorf("processor: governor review: %w", err)
		}
		if !result.Approved {
			return fmt.Errorf("processor: goal %q rejected by governor: %s", goal.ID, result.Reason)
		}
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

	agent, err := p.spawner.Spawn(context.Background(), goal, nil, p.memory)
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

// ---------------------------------------------------------------------------
// module.Module implementation
// ---------------------------------------------------------------------------

// Name returns the module identifier.
func (p *Processor) Name() string { return "processor" }

// Health reports the current operational status of the Processor.
func (p *Processor) Health() module.ModuleHealth {
	status := "healthy"
	details := fmt.Sprintf("queued_goals=%d", p.prioritizer.Len())

	if p.governorRev == nil {
		status = "degraded"
		details += "; governor not wired"
	}
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
