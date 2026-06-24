package processor

import (
	"sort"
	"sync"

	"github.com/isellar/hyperios/internal/types"
)

// Prioritizer is a thread-safe priority queue for Goal objects.
// Goals in state Active are prioritised; within equal priority they are ordered
// by CreatedAt ascending (earliest first).
type Prioritizer struct {
	mu    sync.Mutex
	goals []*types.Goal
}

// NewPrioritizer returns an empty Prioritizer.
func NewPrioritizer() *Prioritizer {
	return &Prioritizer{}
}

// Enqueue adds goal to the priority queue and re-sorts.
func (p *Prioritizer) Enqueue(goal *types.Goal) {
	if goal == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.goals = append(p.goals, goal)
	p.sort()
}

// Next pops the highest-priority Active goal from the queue.
// Only goals whose State is GoalStateActive are eligible.
// Returns (nil, false) when no Active goal exists.
func (p *Prioritizer) Next() (*types.Goal, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, g := range p.goals {
		if g.State == types.GoalStateActive {
			p.goals = append(p.goals[:i], p.goals[i+1:]...)
			return g, true
		}
	}
	return nil, false
}

// Remove deletes the goal identified by goalID from the queue.
// No-op if the goal is not present.
func (p *Prioritizer) Remove(goalID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, g := range p.goals {
		if g.ID == goalID {
			p.goals = append(p.goals[:i], p.goals[i+1:]...)
			return
		}
	}
}

// Len returns the number of goals currently in the queue.
func (p *Prioritizer) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.goals)
}

// sort orders goals: Active goals first, then by CreatedAt ascending.
// Must be called with p.mu held.
func (p *Prioritizer) sort() {
	sort.SliceStable(p.goals, func(i, j int) bool {
		gi, gj := p.goals[i], p.goals[j]

		// Active goals are sorted ahead of non-Active ones.
		iActive := gi.State == types.GoalStateActive
		jActive := gj.State == types.GoalStateActive
		if iActive != jActive {
			return iActive
		}

		// Tiebreaker: earliest CreatedAt first.
		return gi.CreatedAt.Before(gj.CreatedAt)
	})
}
