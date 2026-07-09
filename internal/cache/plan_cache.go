package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

// Guard is a pre-flight check that must pass before a cached plan can execute.
type Guard struct {
	Check       func() bool `json:"-"`
	Description string      `json:"description"`
}

// CachedPlan stores a successful intent→plan mapping with metadata.
type CachedPlan struct {
	Intent      string            `json:"intent"`
	Plan        *types.ActionPlan `json:"plan"`
	Hits        int               `json:"hits"`
	LastUsed    time.Time         `json:"last_used"`
	SuccessRate float64           `json:"success_rate"`
	TotalExecs  int               `json:"total_execs"`
	Guards      []Guard           `json:"-"`
	// guardDescs is unexported and therefore never marshaled by encoding/json;
	// it exists purely as an in-memory cache of Guard.Description strings.
	guardDescs []string
}

// PlanCache stores and retrieves cached intent→plan mappings.
type PlanCache struct {
	mu      sync.RWMutex
	entries map[string]*CachedPlan
	path    string
	maxSize int
	ttl     time.Duration
}

// Config holds PlanCache configuration.
type Config struct {
	Path    string
	MaxSize int
	TTL     time.Duration
}

// New creates a PlanCache, loading existing entries from disk if available.
func New(cfg Config) *PlanCache {
	pc := &PlanCache{
		entries: make(map[string]*CachedPlan),
		path:    cfg.Path,
		maxSize: cfg.MaxSize,
		ttl:     cfg.TTL,
	}
	if pc.maxSize == 0 {
		pc.maxSize = 1000
	}
	if pc.ttl == 0 {
		pc.ttl = 7 * 24 * time.Hour
	}
	_ = pc.load()
	return pc
}

// Get retrieves a cached plan for an exact intent match.
// Returns nil, false if not found or expired.
func (pc *PlanCache) Get(intent string) (*CachedPlan, bool) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	entry, ok := pc.entries[intent]
	if !ok {
		return nil, false
	}

	if time.Since(entry.LastUsed) > pc.ttl {
		return nil, false
	}

	return entry, true
}

// Store adds or updates a cached plan.
func (pc *PlanCache) Store(intent string, plan *types.ActionPlan, guards []Guard) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if len(pc.entries) >= pc.maxSize {
		pc.evictLRU()
	}

	entry, exists := pc.entries[intent]
	if exists {
		entry.Plan = plan
		entry.LastUsed = time.Now()
		entry.Guards = guards
		entry.guardDescs = make([]string, len(guards))
		for i, g := range guards {
			entry.guardDescs[i] = g.Description
		}
	} else {
		guardDescs := make([]string, len(guards))
		for i, g := range guards {
			guardDescs[i] = g.Description
		}
		pc.entries[intent] = &CachedPlan{
			Intent:     intent,
			Plan:       plan,
			Hits:       0,
			LastUsed:   time.Now(),
			Guards:     guards,
			guardDescs: guardDescs,
		}
	}

	_ = pc.save()
}

// RecordSuccess increments hit count and updates success rate.
func (pc *PlanCache) RecordSuccess(intent string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	entry, ok := pc.entries[intent]
	if !ok {
		return
	}

	entry.Hits++
	entry.TotalExecs++
	entry.SuccessRate = float64(entry.TotalExecs-entry.failures()) / float64(entry.TotalExecs)
	entry.LastUsed = time.Now()
	_ = pc.save()
}

// RecordFailure increments total executions and updates success rate.
func (pc *PlanCache) RecordFailure(intent string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	entry, ok := pc.entries[intent]
	if !ok {
		return
	}

	entry.TotalExecs++
	entry.SuccessRate = float64(entry.TotalExecs-entry.failures()-1) / float64(entry.TotalExecs)
	entry.LastUsed = time.Now()
	_ = pc.save()
}

// Remove deletes a cached entry.
func (pc *PlanCache) Remove(intent string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	delete(pc.entries, intent)
	_ = pc.save()
}

// All returns a copy of all cached entries.
func (pc *PlanCache) All() map[string]*CachedPlan {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	result := make(map[string]*CachedPlan, len(pc.entries))
	for k, v := range pc.entries {
		result[k] = v
	}
	return result
}

// GuardCheck runs all guards for a cached plan. Returns true if all pass.
func (c *CachedPlan) GuardCheck() bool {
	for _, g := range c.Guards {
		if !g.Check() {
			return false
		}
	}
	return true
}

// HitCount returns the number of times this plan has been reused.
func (c *CachedPlan) HitCount() int {
	return c.Hits
}

func (c *CachedPlan) failures() int {
	return c.TotalExecs - c.Hits
}

func (pc *PlanCache) evictLRU() {
	var oldest string
	var oldestTime time.Time
	first := true
	for k, v := range pc.entries {
		if first || v.LastUsed.Before(oldestTime) {
			oldest = k
			oldestTime = v.LastUsed
			first = false
		}
	}
	if oldest != "" {
		delete(pc.entries, oldest)
	}
}

func (pc *PlanCache) load() error {
	if pc.path == "" {
		return nil
	}

	data, err := os.ReadFile(pc.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("load plan cache: %w", err)
	}

	var entries map[string]*CachedPlan
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse plan cache: %w", err)
	}

	pc.entries = entries
	return nil
}

func (pc *PlanCache) save() error {
	if pc.path == "" {
		return nil
	}

	dir := filepath.Dir(pc.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	data, err := json.MarshalIndent(pc.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal plan cache: %w", err)
	}

	if err := os.WriteFile(pc.path, data, 0644); err != nil {
		return fmt.Errorf("write plan cache: %w", err)
	}

	return nil
}
