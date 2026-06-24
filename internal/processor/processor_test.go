package processor

import (
	"errors"
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/governor"
	"github.com/isellar/hyperios/internal/memory"
	"github.com/isellar/hyperios/internal/types"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

type mockGovernor struct {
	approved bool
	reason   string
	err      error
}

func (m *mockGovernor) ReviewGoal(goal *types.Goal) (*governor.ReviewResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &governor.ReviewResult{
		Approved: m.approved,
		Reason:   m.reason,
	}, nil
}

func (m *mockGovernor) CheckToolAuthorized(toolID string) bool {
	return m.approved
}

type mockGoalUpdater struct {
	updates []struct {
		id    string
		state types.GoalState
	}
	err error
}

func (m *mockGoalUpdater) UpdateGoalState(id string, state types.GoalState) error {
	m.updates = append(m.updates, struct {
		id    string
		state types.GoalState
	}{id, state})
	return m.err
}

type mockMemory struct {
	entries map[string]interface{}
}

func (m *mockMemory) RecallContext(key string) (interface{}, bool) {
	v, ok := m.entries[key]
	return v, ok
}

func (m *mockMemory) SearchContext(query string) ([]*memory.MemoryEntry, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func makeGoal(id string, state types.GoalState, createdAt time.Time) *types.Goal {
	return &types.Goal{
		ID:          id,
		State:       state,
		Description: "test goal " + id,
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
	}
}

// ---------------------------------------------------------------------------
// Prioritizer tests
// ---------------------------------------------------------------------------

func TestPrioritizer_EnqueueLen(t *testing.T) {
	p := NewPrioritizer()
	if p.Len() != 0 {
		t.Fatalf("expected empty queue, got %d", p.Len())
	}

	p.Enqueue(makeGoal("g1", types.GoalStateActive, time.Now()))
	p.Enqueue(makeGoal("g2", types.GoalStateActive, time.Now()))
	if p.Len() != 2 {
		t.Fatalf("expected 2 goals, got %d", p.Len())
	}
}

func TestPrioritizer_NilEnqueue(t *testing.T) {
	p := NewPrioritizer()
	p.Enqueue(nil) // should be a no-op
	if p.Len() != 0 {
		t.Fatalf("nil enqueue should be no-op, got len %d", p.Len())
	}
}

func TestPrioritizer_Next_ActiveOnly(t *testing.T) {
	p := NewPrioritizer()

	now := time.Now()
	p.Enqueue(makeGoal("g-blocked", types.GoalStateBlocked, now))
	p.Enqueue(makeGoal("g-active", types.GoalStateActive, now.Add(time.Second)))

	got, ok := p.Next()
	if !ok {
		t.Fatal("expected a goal, got none")
	}
	if got.ID != "g-active" {
		t.Fatalf("expected g-active, got %s", got.ID)
	}
	if p.Len() != 1 { // g-blocked remains
		t.Fatalf("expected 1 remaining, got %d", p.Len())
	}
}

func TestPrioritizer_Next_EmptyQueue(t *testing.T) {
	p := NewPrioritizer()
	got, ok := p.Next()
	if ok || got != nil {
		t.Fatal("expected (nil, false) from empty queue")
	}
}

func TestPrioritizer_Next_NoActiveGoals(t *testing.T) {
	p := NewPrioritizer()
	p.Enqueue(makeGoal("g1", types.GoalStateRefining, time.Now()))
	p.Enqueue(makeGoal("g2", types.GoalStateDone, time.Now()))

	got, ok := p.Next()
	if ok || got != nil {
		t.Fatal("expected (nil, false) when no Active goals")
	}
}

func TestPrioritizer_Next_EarliestCreatedAtFirst(t *testing.T) {
	p := NewPrioritizer()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	t2 := t0.Add(2 * time.Hour)

	// Enqueue out of order.
	p.Enqueue(makeGoal("g-middle", types.GoalStateActive, t1))
	p.Enqueue(makeGoal("g-latest", types.GoalStateActive, t2))
	p.Enqueue(makeGoal("g-earliest", types.GoalStateActive, t0))

	first, _ := p.Next()
	if first.ID != "g-earliest" {
		t.Fatalf("expected g-earliest first, got %s", first.ID)
	}

	second, _ := p.Next()
	if second.ID != "g-middle" {
		t.Fatalf("expected g-middle second, got %s", second.ID)
	}

	third, _ := p.Next()
	if third.ID != "g-latest" {
		t.Fatalf("expected g-latest third, got %s", third.ID)
	}

	if p.Len() != 0 {
		t.Fatalf("expected empty queue, got %d", p.Len())
	}
}

func TestPrioritizer_ActiveBeforeNonActive(t *testing.T) {
	p := NewPrioritizer()

	t0 := time.Now()
	// Non-Active goal created earlier than Active goal.
	p.Enqueue(makeGoal("g-refining-old", types.GoalStateRefining, t0))
	p.Enqueue(makeGoal("g-active-new", types.GoalStateActive, t0.Add(time.Hour)))

	got, ok := p.Next()
	if !ok {
		t.Fatal("expected a goal")
	}
	if got.ID != "g-active-new" {
		t.Fatalf("Active goal should be ahead of non-Active regardless of CreatedAt; got %s", got.ID)
	}
}

func TestPrioritizer_Remove(t *testing.T) {
	p := NewPrioritizer()
	p.Enqueue(makeGoal("g1", types.GoalStateActive, time.Now()))
	p.Enqueue(makeGoal("g2", types.GoalStateActive, time.Now()))

	p.Remove("g1")
	if p.Len() != 1 {
		t.Fatalf("expected 1 after remove, got %d", p.Len())
	}

	got, ok := p.Next()
	if !ok || got.ID != "g2" {
		t.Fatalf("expected g2 after removing g1, got %v", got)
	}
}

func TestPrioritizer_Remove_NonExistent(t *testing.T) {
	p := NewPrioritizer()
	p.Enqueue(makeGoal("g1", types.GoalStateActive, time.Now()))
	p.Remove("does-not-exist") // should be a no-op
	if p.Len() != 1 {
		t.Fatalf("remove of unknown ID should be no-op, got len %d", p.Len())
	}
}

// ---------------------------------------------------------------------------
// Processor.QueueGoal tests
// ---------------------------------------------------------------------------

func TestProcessor_QueueGoal_NilGoal(t *testing.T) {
	p := NewProcessor(nil, nil, nil)
	err := p.QueueGoal(nil)
	if err == nil {
		t.Fatal("expected error for nil goal")
	}
}

func TestProcessor_QueueGoal_Approved(t *testing.T) {
	p := NewProcessor(nil, nil, nil)
	p.SetGovernor(&mockGovernor{approved: true, reason: "all good"})

	goal := makeGoal("g1", types.GoalStateActive, time.Now())
	if err := p.QueueGoal(goal); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.prioritizer.Len() != 1 {
		t.Fatalf("expected 1 queued goal, got %d", p.prioritizer.Len())
	}
}

func TestProcessor_QueueGoal_Rejected(t *testing.T) {
	p := NewProcessor(nil, nil, nil)
	p.SetGovernor(&mockGovernor{approved: false, reason: "violates directive"})

	goal := makeGoal("g1", types.GoalStateActive, time.Now())
	err := p.QueueGoal(goal)
	if err == nil {
		t.Fatal("expected error when governor rejects goal")
	}
	if p.prioritizer.Len() != 0 {
		t.Fatalf("rejected goal should not be queued, got len %d", p.prioritizer.Len())
	}
}

func TestProcessor_QueueGoal_GovernorError(t *testing.T) {
	p := NewProcessor(nil, nil, nil)
	p.SetGovernor(&mockGovernor{err: errors.New("governor unavailable")})

	goal := makeGoal("g1", types.GoalStateActive, time.Now())
	err := p.QueueGoal(goal)
	if err == nil {
		t.Fatal("expected error when governor returns error")
	}
	if p.prioritizer.Len() != 0 {
		t.Fatalf("goal should not be queued on governor error, got len %d", p.prioritizer.Len())
	}
}

func TestProcessor_QueueGoal_NoGovernor(t *testing.T) {
	// When no governor is wired, QueueGoal should succeed (skip review).
	p := NewProcessor(nil, nil, nil)

	goal := makeGoal("g1", types.GoalStateActive, time.Now())
	if err := p.QueueGoal(goal); err != nil {
		t.Fatalf("expected success with no governor, got: %v", err)
	}
	if p.prioritizer.Len() != 1 {
		t.Fatalf("expected 1 queued goal, got %d", p.prioritizer.Len())
	}
}

func TestProcessor_QueueGoal_MultipleApproved(t *testing.T) {
	p := NewProcessor(nil, nil, nil)
	p.SetGovernor(&mockGovernor{approved: true})

	for i := 0; i < 5; i++ {
		goal := makeGoal(
			"g"+string(rune('0'+i)),
			types.GoalStateActive,
			time.Now().Add(time.Duration(i)*time.Second),
		)
		if err := p.QueueGoal(goal); err != nil {
			t.Fatalf("goal %d: unexpected error: %v", i, err)
		}
	}
	if p.prioritizer.Len() != 5 {
		t.Fatalf("expected 5 queued goals, got %d", p.prioritizer.Len())
	}
}

// ---------------------------------------------------------------------------
// Processor module interface tests
// ---------------------------------------------------------------------------

func TestProcessor_Name(t *testing.T) {
	p := NewProcessor(nil, nil, nil)
	if p.Name() != "processor" {
		t.Fatalf("unexpected name: %s", p.Name())
	}
}

func TestProcessor_Health_Degraded_NoGovernor(t *testing.T) {
	p := NewProcessor(nil, nil, nil)
	h := p.Health()
	if h.Status != "degraded" {
		t.Fatalf("expected degraded without governor, got %s", h.Status)
	}
}

func TestProcessor_Health_Degraded_NoGoalUpdater(t *testing.T) {
	p := NewProcessor(nil, nil, nil)
	p.SetGovernor(&mockGovernor{approved: true})
	// Still no GoalUpdater
	h := p.Health()
	if h.Status != "degraded" {
		t.Fatalf("expected degraded without goal updater, got %s", h.Status)
	}
}

func TestProcessor_Health_Healthy(t *testing.T) {
	p := NewProcessor(nil, nil, nil)
	p.SetGovernor(&mockGovernor{approved: true})
	p.SetGoalFulfillment(&mockGoalUpdater{})
	h := p.Health()
	if h.Status != "healthy" {
		t.Fatalf("expected healthy, got %s: %s", h.Status, h.Details)
	}
}

func TestProcessor_Capabilities(t *testing.T) {
	p := NewProcessor(nil, nil, nil)
	caps := p.Capabilities()
	if len(caps) == 0 {
		t.Fatal("expected non-empty capabilities")
	}
}

func TestProcessor_LookupInfo_NilMemory(t *testing.T) {
	p := NewProcessor(nil, nil, nil)
	result, err := p.LookupInfo("anything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Fatalf("expected empty result with no memory, got %q", result)
	}
}

func TestProcessor_LookupInfo_WithMemory(t *testing.T) {
	p := NewProcessor(nil, nil, nil)
	p.SetMemory(&mockMemory{entries: map[string]interface{}{
		"key1": "value1",
	}})
	// SearchContext returns nil in mock, so result is empty — that's fine.
	_, err := p.LookupInfo("key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
