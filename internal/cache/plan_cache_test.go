package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

func TestPlanCache_StoreAndGet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plans.json")

	pc := New(Config{Path: path})

	plan := &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1", Description: "test step", Command: []string{"echo", "hello"}},
		},
	}

	pc.Store("test intent", plan, nil)

	got, ok := pc.Get("test intent")
	if !ok {
		t.Fatal("expected to find cached plan")
	}

	if got.Plan.Steps[0].ID != "s1" {
		t.Errorf("expected step ID s1, got %s", got.Plan.Steps[0].ID)
	}
}

func TestPlanCache_Miss(t *testing.T) {
	pc := New(Config{})

	_, ok := pc.Get("nonexistent")
	if ok {
		t.Error("expected cache miss")
	}
}

func TestPlanCache_RecordSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plans.json")

	pc := New(Config{Path: path})

	plan := &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1", Description: "test step", Command: []string{"echo", "hello"}},
		},
	}

	pc.Store("test intent", plan, nil)
	pc.RecordSuccess("test intent")
	pc.RecordSuccess("test intent")

	got, ok := pc.Get("test intent")
	if !ok {
		t.Fatal("expected to find cached plan")
	}

	if got.Hits != 2 {
		t.Errorf("expected 2 hits, got %d", got.Hits)
	}
	if got.TotalExecs != 2 {
		t.Errorf("expected 2 total execs, got %d", got.TotalExecs)
	}
}

func TestPlanCache_RecordFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plans.json")

	pc := New(Config{Path: path})

	plan := &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1", Description: "test step", Command: []string{"echo", "hello"}},
		},
	}

	pc.Store("test intent", plan, nil)
	pc.RecordSuccess("test intent")
	pc.RecordFailure("test intent")

	got, ok := pc.Get("test intent")
	if !ok {
		t.Fatal("expected to find cached plan")
	}

	if got.TotalExecs != 2 {
		t.Errorf("expected 2 total execs, got %d", got.TotalExecs)
	}
	if got.Hits != 1 {
		t.Errorf("expected 1 hit, got %d", got.Hits)
	}
}

func TestPlanCache_Remove(t *testing.T) {
	pc := New(Config{})

	plan := &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1", Description: "test step", Command: []string{"echo", "hello"}},
		},
	}

	pc.Store("test intent", plan, nil)
	pc.Remove("test intent")

	_, ok := pc.Get("test intent")
	if ok {
		t.Error("expected cache miss after remove")
	}
}

func TestPlanCache_GuardCheck(t *testing.T) {
	guardCalled := false
	guards := []Guard{
		{
			Check:       func() bool { guardCalled = true; return true },
			Description: "test guard",
		},
	}

	cached := &CachedPlan{
		Intent: "test",
		Plan:   &types.ActionPlan{},
		Guards: guards,
	}

	if !cached.GuardCheck() {
		t.Error("expected guard check to pass")
	}
	if !guardCalled {
		t.Error("expected guard to be called")
	}
}

func TestPlanCache_GuardCheck_Fail(t *testing.T) {
	guards := []Guard{
		{
			Check:       func() bool { return false },
			Description: "failing guard",
		},
	}

	cached := &CachedPlan{
		Intent: "test",
		Plan:   &types.ActionPlan{},
		Guards: guards,
	}

	if cached.GuardCheck() {
		t.Error("expected guard check to fail")
	}
}

func TestPlanCache_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plans.json")

	pc1 := New(Config{Path: path})

	plan := &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1", Description: "test step", Command: []string{"echo", "hello"}},
		},
	}

	pc1.Store("test intent", plan, nil)
	pc1.RecordSuccess("test intent")

	pc2 := New(Config{Path: path})

	got, ok := pc2.Get("test intent")
	if !ok {
		t.Fatal("expected to find cached plan after reload")
	}

	if got.Hits != 1 {
		t.Errorf("expected 1 hit after reload, got %d", got.Hits)
	}
}

func TestPlanCache_TTL(t *testing.T) {
	pc := New(Config{
		TTL: 1 * time.Millisecond,
	})

	plan := &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1", Description: "test step", Command: []string{"echo", "hello"}},
		},
	}

	pc.Store("test intent", plan, nil)

	time.Sleep(10 * time.Millisecond)

	_, ok := pc.Get("test intent")
	if ok {
		t.Error("expected cache miss after TTL expiry")
	}
}

func TestPlanCache_MaxSize(t *testing.T) {
	pc := New(Config{MaxSize: 2})

	for i := 0; i < 5; i++ {
		plan := &types.ActionPlan{
			Steps: []types.ActionStep{
				{ID: "s1", Description: "test step", Command: []string{"echo", "hello"}},
			},
		}
		pc.Store("intent", plan, nil)
	}

	count := 0
	for _, intent := range []string{"intent"} {
		if _, ok := pc.Get(intent); ok {
			count++
		}
	}

	if len(pc.entries) > 2 {
		t.Errorf("expected at most 2 entries, got %d", len(pc.entries))
	}
}

func TestPlanCache_EmptyPath(t *testing.T) {
	pc := New(Config{Path: ""})

	plan := &types.ActionPlan{
		Steps: []types.ActionStep{
			{ID: "s1", Description: "test step", Command: []string{"echo", "hello"}},
		},
	}

	pc.Store("test intent", plan, nil)

	got, ok := pc.Get("test intent")
	if !ok {
		t.Fatal("expected to find cached plan")
	}

	if got.Plan.Steps[0].ID != "s1" {
		t.Errorf("expected step ID s1, got %s", got.Plan.Steps[0].ID)
	}
}

func TestPlanCache_LoadNonExistentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	pc := New(Config{Path: path})

	if len(pc.entries) != 0 {
		t.Errorf("expected empty cache, got %d entries", len(pc.entries))
	}
}

func TestPlanCache_LoadInvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plans.json")

	if err := os.WriteFile(path, []byte("invalid json"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	pc := New(Config{Path: path})

	if len(pc.entries) != 0 {
		t.Errorf("expected empty cache after invalid file, got %d entries", len(pc.entries))
	}
}
