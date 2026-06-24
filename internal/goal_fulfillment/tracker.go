package goal_fulfillment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

type Tracker struct {
	mu      sync.RWMutex
	goals   map[string]*types.Goal
	dataDir string
}

func NewTracker(dataDir string) (*Tracker, error) {
	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0o750); err != nil {
			return nil, fmt.Errorf("tracker: create dir: %w", err)
		}
	}

	t := &Tracker{
		goals:   make(map[string]*types.Goal),
		dataDir: dataDir,
	}

	if dataDir != "" {
		if err := t.loadFromDisk(); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("tracker: load: %w", err)
		}
	}

	return t, nil
}

func (t *Tracker) TrackGoal(goal *types.Goal) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if goal.ID == "" {
		return fmt.Errorf("tracker: goal ID is empty")
	}

	now := time.Now()
	if goal.CreatedAt.IsZero() {
		goal.CreatedAt = now
	}
	goal.UpdatedAt = now

	t.goals[goal.ID] = goal

	return t.persist()
}

func (t *Tracker) GetGoal(id string) (*types.Goal, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	goal, ok := t.goals[id]
	if !ok {
		return nil, fmt.Errorf("tracker: goal %q not found", id)
	}
	return goal, nil
}

func (t *Tracker) ListGoals(state types.GoalState) ([]*types.Goal, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var result []*types.Goal
	for _, g := range t.goals {
		if state == "" || g.State == state {
			result = append(result, g)
		}
	}
	return result, nil
}

func (t *Tracker) UpdateGoalState(id string, state types.GoalState) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	goal, ok := t.goals[id]
	if !ok {
		return fmt.Errorf("tracker: goal %q not found", id)
	}

	if !isValidTransition(goal.State, state) {
		return fmt.Errorf("tracker: invalid state transition from %q to %q for goal %q", goal.State, state, id)
	}

	goal.State = state
	goal.UpdatedAt = time.Now()

	return t.persist()
}

func (t *Tracker) DeleteGoal(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.goals[id]; !ok {
		return fmt.Errorf("tracker: goal %q not found", id)
	}

	delete(t.goals, id)
	return t.persist()
}

func isValidTransition(from, to types.GoalState) bool {
	validTransitions := map[types.GoalState][]types.GoalState{
		types.GoalStateRefining:  {types.GoalStateActive, types.GoalStateCancelled},
		types.GoalStateActive:    {types.GoalStateDone, types.GoalStateBlocked, types.GoalStateCancelled, types.GoalStateRefining},
		types.GoalStateBlocked:   {types.GoalStateActive, types.GoalStateCancelled},
		types.GoalStateDone:      {},
		types.GoalStateCancelled: {},
	}

	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

func (t *Tracker) persist() error {
	if t.dataDir == "" {
		return nil
	}

	path := filepath.Join(t.dataDir, "goals.json")

	data, err := json.MarshalIndent(t.goals, "", "  ")
	if err != nil {
		return fmt.Errorf("tracker: marshal: %w", err)
	}

	return os.WriteFile(path, data, 0o640)
}

func (t *Tracker) loadFromDisk() error {
	if t.dataDir == "" {
		return nil
	}

	path := filepath.Join(t.dataDir, "goals.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &t.goals)
}
