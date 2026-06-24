package goal_fulfillment

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/module"
	"github.com/isellar/hyperios/internal/types"
)

var goalCounter atomic.Int64

type GoalFulfillment struct {
	refiner   *Refiner
	breakdown *Breakdown
	tracker   *Tracker
}

func New(client llm.Completer, memory MemoryProvider, processor ProcessorProvider, dataDir string) (*GoalFulfillment, error) {
	tracker, err := NewTracker(dataDir)
	if err != nil {
		return nil, fmt.Errorf("goal_fulfillment: create tracker: %w", err)
	}

	return &GoalFulfillment{
		refiner:   NewRefiner(client, memory, processor),
		breakdown: NewBreakdown(client),
		tracker:   tracker,
	}, nil
}

func (gf *GoalFulfillment) SubmitGoal(description string) (*types.Goal, error) {
	goal := &types.Goal{
		ID:          fmt.Sprintf("g-%d-%d", time.Now().UnixNano(), goalCounter.Add(1)),
		Description: description,
		State:       types.GoalStateRefining,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := gf.tracker.TrackGoal(goal); err != nil {
		return nil, fmt.Errorf("goal_fulfillment: track goal: %w", err)
	}

	return goal, nil
}

func (gf *GoalFulfillment) RefineGoal(ctx context.Context, goal *types.Goal) (*types.Goal, error) {
	refined, err := gf.refiner.RefineGoal(ctx, goal)
	if err != nil {
		return nil, fmt.Errorf("goal_fulfillment: refine: %w", err)
	}

	if err := gf.tracker.TrackGoal(refined); err != nil {
		return nil, fmt.Errorf("goal_fulfillment: track refined goal: %w", err)
	}

	return refined, nil
}

func (gf *GoalFulfillment) BreakdownGoal(ctx context.Context, goal *types.Goal) ([]*types.Goal, error) {
	subGoals, err := gf.breakdown.BreakdownGoal(ctx, goal)
	if err != nil {
		return nil, fmt.Errorf("goal_fulfillment: breakdown: %w", err)
	}

	if err := gf.tracker.TrackGoal(goal); err != nil {
		return nil, fmt.Errorf("goal_fulfillment: track parent goal: %w", err)
	}

	for _, sg := range subGoals {
		if err := gf.tracker.TrackGoal(sg); err != nil {
			return nil, fmt.Errorf("goal_fulfillment: track sub-goal %s: %w", sg.ID, err)
		}
	}

	return subGoals, nil
}

func (gf *GoalFulfillment) GetGoal(id string) (*types.Goal, error) {
	return gf.tracker.GetGoal(id)
}

func (gf *GoalFulfillment) ListGoals(state types.GoalState) ([]*types.Goal, error) {
	return gf.tracker.ListGoals(state)
}

func (gf *GoalFulfillment) UpdateGoalState(id string, state types.GoalState) error {
	return gf.tracker.UpdateGoalState(id, state)
}

func (gf *GoalFulfillment) Name() string {
	return "goal_fulfillment"
}

func (gf *GoalFulfillment) Report(ctx context.Context, window time.Duration) (module.ModuleReport, error) {
	allGoals, _ := gf.tracker.ListGoals("")
	activeGoals, _ := gf.tracker.ListGoals(types.GoalStateActive)
	refiningGoals, _ := gf.tracker.ListGoals(types.GoalStateRefining)
	doneGoals, _ := gf.tracker.ListGoals(types.GoalStateDone)
	blockedGoals, _ := gf.tracker.ListGoals(types.GoalStateBlocked)

	var issues []string
	for _, g := range blockedGoals {
		issues = append(issues, fmt.Sprintf("goal %s is blocked: %s", g.ID, g.Description))
	}

	return module.ModuleReport{
		ModuleName: "goal_fulfillment",
		Window:     window,
		Metrics: map[string]any{
			"total_goals":    len(allGoals),
			"active_goals":   len(activeGoals),
			"refining_goals": len(refiningGoals),
			"done_goals":     len(doneGoals),
			"blocked_goals":  len(blockedGoals),
		},
		Issues: issues,
	}, nil
}

func (gf *GoalFulfillment) Tune(ctx context.Context, change module.TuningChange) error {
	return nil
}

func (gf *GoalFulfillment) Health() module.ModuleHealth {
	return module.ModuleHealth{
		Status:    "healthy",
		Details:   "goal fulfillment module operational",
		Timestamp: time.Now(),
	}
}

func (gf *GoalFulfillment) Capabilities() []string {
	return []string{"read:file", "execute:shell"}
}

var _ module.Module = (*GoalFulfillment)(nil)
