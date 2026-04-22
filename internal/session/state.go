package session

import (
	"fmt"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

type State struct {
	ID        string                 `json:"id"`
	Intent    string                 `json:"intent"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
	Goals     []types.Goal           `json:"goals"`
	Plan      *types.ActionPlan      `json:"plan,omitempty"`
	Completed []string               `json:"completed"`
	Context   types.WorkspaceContext `json:"context"`
}

func NewState(id, intent string, ctx types.WorkspaceContext) *State {
	now := time.Now()
	return &State{
		ID:        id,
		Intent:    intent,
		CreatedAt: now,
		UpdatedAt: now,
		Goals:     []types.Goal{},
		Completed: []string{},
		Context:   ctx,
	}
}

func (s *State) ToGoalGraph() *types.GoalGraph {
	return &types.GoalGraph{
		Intent:  s.Intent,
		Context: fmt.Sprintf("Directory: %s, Git branch: %s", s.Context.Cwd, s.Context.GitBranch),
		Goals:   s.Goals,
	}
}

func (s *State) MarkCompleted(stepID string) {
	s.Completed = append(s.Completed, stepID)
	s.UpdatedAt = time.Now()
}

func (s *State) IsCompleted(stepID string) bool {
	for _, id := range s.Completed {
		if id == stepID {
			return true
		}
	}
	return false
}

func (s *State) RemainingSteps() []types.ActionStep {
	if s.Plan == nil {
		return []types.ActionStep{}
	}
	var remaining []types.ActionStep
	for _, step := range s.Plan.Steps {
		if !s.IsCompleted(step.ID) {
			remaining = append(remaining, step)
		}
	}
	return remaining
}

func (s *State) Progress() (completed, total int) {
	if s.Plan == nil {
		return 0, 0
	}
	total = len(s.Plan.Steps)
	completed = len(s.Completed)
	return
}
