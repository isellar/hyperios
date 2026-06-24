package goal_fulfillment

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/types"
)

const breakdownSystem = `You are the Goal Breakdown Agent for HyperiOS, an intent-first AI Linux distribution.
Your job is to take a high-level goal and break it into concrete, actionable sub-goals.

Rules:
- Break goals into the minimal set of sub-goals needed.
- Each sub-goal must be specific and actionable.
- Define dependency relationships between sub-goals.
- If a goal is already atomic (cannot be broken down further), return it as-is with no sub-goals.
- Return ONLY valid JSON matching this schema — no markdown, no explanation:

{
  "parent_id": "<parent goal id>",
  "sub_goals": [
    {
      "id": "<sub-goal id>",
      "description": "<what this sub-goal achieves>",
      "depends_on": [],
      "is_atomic": true
    }
  ]
}`

type Breakdown struct {
	client llm.Completer
}

func NewBreakdown(client llm.Completer) *Breakdown {
	return &Breakdown{client: client}
}

type breakdownResponse struct {
	ParentID string       `json:"parent_id"`
	SubGoals []types.Goal `json:"sub_goals"`
}

func (b *Breakdown) BreakdownGoal(ctx context.Context, goal *types.Goal) ([]*types.Goal, error) {
	user := fmt.Sprintf(`Goal to break down:
ID: %s
Description: %q
Current state: %s`,
		goal.ID,
		goal.Description,
		goal.State,
	)

	raw, err := b.client.CompleteWithRetry(ctx, breakdownSystem, user)
	if err != nil {
		return nil, fmt.Errorf("breakdown: %w", err)
	}

	raw = extractJSON(raw)
	var resp breakdownResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("breakdown: parse response: %w\nraw: %s", err, raw)
	}

	if len(resp.SubGoals) == 0 {
		return []*types.Goal{goal}, nil
	}

	now := time.Now()
	var subGoals []*types.Goal
	var subGoalIDs []string
	for i := range resp.SubGoals {
		sg := resp.SubGoals[i]
		sg.CreatedAt = now
		sg.UpdatedAt = now
		if sg.State == "" {
			sg.State = types.GoalStateRefining
		}
		subGoals = append(subGoals, &sg)
		subGoalIDs = append(subGoalIDs, sg.ID)
	}

	goal.SubGoals = subGoalIDs
	goal.UpdatedAt = now

	return subGoals, nil
}

func (b *Breakdown) BreakdownRecursive(ctx context.Context, goal *types.Goal, maxDepth int) ([]*types.Goal, error) {
	if maxDepth <= 0 {
		return []*types.Goal{goal}, nil
	}

	subGoals, err := b.BreakdownGoal(ctx, goal)
	if err != nil {
		return nil, err
	}

	if len(subGoals) == 1 && subGoals[0].ID == goal.ID {
		return []*types.Goal{goal}, nil
	}

	var allGoals []*types.Goal
	for _, sg := range subGoals {
		allGoals = append(allGoals, sg)
		deeper, err := b.BreakdownRecursive(ctx, sg, maxDepth-1)
		if err != nil {
			return nil, err
		}
		for _, d := range deeper {
			if d.ID != sg.ID {
				allGoals = append(allGoals, d)
			}
		}
	}

	return allGoals, nil
}

func (b *Breakdown) ValidateSubGoals(goals []*types.Goal) error {
	seen := make(map[string]bool, len(goals))
	for _, g := range goals {
		if g.ID == "" {
			return fmt.Errorf("breakdown: sub-goal has empty ID")
		}
		if seen[g.ID] {
			return fmt.Errorf("breakdown: duplicate sub-goal ID %q", g.ID)
		}
		seen[g.ID] = true
		if g.Description == "" {
			return fmt.Errorf("breakdown: sub-goal %q has empty description", g.ID)
		}
		for _, dep := range g.DependsOn {
			if !seen[dep] {
				found := false
				for _, other := range goals {
					if other.ID == dep {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("breakdown: sub-goal %q depends on unknown goal %q", g.ID, dep)
				}
			}
		}
	}
	return nil
}
