package processor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ResultStore persists AgentResults to disk, keyed by goal ID, so that a
// goal's outcome — most importantly the failure reason for a blocked goal —
// survives a process restart. Before this existed, results only ever lived
// in an in-memory map (see apiserver.Server), which meant restarting hyperi
// silently erased the "why" behind every blocked goal: the web UI's
// "Blocked" status chip would open a goal with no explanation at all.
//
// Mirrors goal_fulfillment.Tracker's persistence approach (whole-map
// marshal-and-overwrite on every write) for consistency with the sibling
// goals.json store — this data is low-volume and low-frequency (one write
// per completed goal run), so a full rewrite is not a performance concern.
type ResultStore struct {
	mu      sync.RWMutex
	results map[string]*AgentResult
	path    string // empty means in-memory only, no persistence
}

// NewResultStore returns a ResultStore backed by the JSON file at path. If
// path is empty, the store is in-memory only (useful for tests). If the file
// doesn't exist yet, an empty store is returned rather than an error.
func NewResultStore(path string) (*ResultStore, error) {
	rs := &ResultStore{
		results: make(map[string]*AgentResult),
		path:    path,
	}

	if path == "" {
		return rs, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("results_store: create dir: %w", err)
	}

	if err := rs.loadFromDisk(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("results_store: load: %w", err)
	}

	return rs, nil
}

// Save records result for its GoalID and persists the whole store to disk.
// A no-op if result is nil or has an empty GoalID.
func (rs *ResultStore) Save(result *AgentResult) error {
	if result == nil || result.GoalID == "" {
		return nil
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.results[result.GoalID] = result
	return rs.persist()
}

// Get returns the most recent AgentResult for goalID, if any.
func (rs *ResultStore) Get(goalID string) (*AgentResult, bool) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	r, ok := rs.results[goalID]
	return r, ok
}

// Delete removes goalID's result, if present, and persists the change.
// Used when a goal itself is deleted, so results don't accumulate forever
// for goals the user has explicitly dismissed.
func (rs *ResultStore) Delete(goalID string) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if _, ok := rs.results[goalID]; !ok {
		return nil
	}
	delete(rs.results, goalID)
	return rs.persist()
}

// persist writes the whole result map to disk. Must be called with rs.mu held.
func (rs *ResultStore) persist() error {
	if rs.path == "" {
		return nil
	}

	data, err := json.MarshalIndent(rs.results, "", "  ")
	if err != nil {
		return fmt.Errorf("results_store: marshal: %w", err)
	}

	return os.WriteFile(rs.path, data, 0o640)
}

// loadFromDisk reads the result map from rs.path.
func (rs *ResultStore) loadFromDisk() error {
	data, err := os.ReadFile(rs.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &rs.results)
}
