// Package memory provides the Memory module for HyperiOS.
// It combines three storage tiers:
//   - SessionMemory: ephemeral, in-process key/value store
//   - WorldModel:    agent's structured knowledge of system state
//   - LongTermMemory: disk-backed, JSON-file-per-entry store
//
// Memory implements both module.Module (for health/capability reporting) and
// the goal_fulfillment.MemoryProvider interface (GetContext/StoreContext).
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/module"
	"github.com/isellar/hyperios/internal/types"
)

// Memory is the top-level memory module for the HyperiOS agent.
type Memory struct {
	session    *SessionMemory
	worldModel *WorldModel
	longTerm   *LongTermMemory
}

// NewMemory constructs a Memory module from the supplied configuration.
// If cfg.MemoryStoragePath is empty the default path is used.
func NewMemory(cfg *config.Config) *Memory {
	storagePath := cfg.MemoryStoragePath
	if storagePath == "" {
		storagePath = config.Defaults().MemoryStoragePath
	}
	return &Memory{
		session:    newSessionMemory(),
		worldModel: newWorldModel(),
		longTerm:   newLongTermMemory(storagePath),
	}
}

// ---------------------------------------------------------------------------
// High-level context API
// ---------------------------------------------------------------------------

// StoreContext persists value in both session memory (fast path) and long-term
// storage (durable path).  The value is stored under key in both tiers.
func (m *Memory) StoreContext(key string, value interface{}) error {
	return m.StoreContextTagged(key, value, nil)
}

// StoreContextTagged is like StoreContext but also attaches tags to the
// long-term entry, so callers can later retrieve exactly this category of
// entry via LongTermMemory.SearchByTag (through SearchContext's underlying
// tier) without relying on substring matching. Used e.g. by the API server
// to tag goal-outcome entries as "goal_outcome"/"success"/"failure".
func (m *Memory) StoreContextTagged(key string, value interface{}, tags []string) error {
	if err := m.session.StoreSession(key, value); err != nil {
		return fmt.Errorf("memory: session store: %w", err)
	}
	if err := m.longTerm.Store(key, value, tags); err != nil {
		return fmt.Errorf("memory: long-term store: %w", err)
	}
	return nil
}

// RecallContext returns the value for key, checking session memory first.
// Falls back to long-term storage when the key is absent from the session.
// Returns (nil, false) when the key is not found in either tier.
func (m *Memory) RecallContext(key string) (interface{}, bool) {
	if v, ok := m.session.RecallSession(key); ok {
		return v, true
	}
	entry, err := m.longTerm.Recall(key)
	if err != nil {
		return nil, false
	}
	// Warm the session cache on a long-term hit
	_ = m.session.StoreSession(key, entry.Value)
	return entry.Value, true
}

// DefaultSearchLimit bounds how many entries SearchContext returns by
// default. Ranked search (BM25) makes a limit meaningful — the top N
// results are the most relevant ones — where the old unranked substring
// search had no principled way to truncate. Keeping this modest also
// protects callers (notably the agent's prompt builder) from dumping an
// unbounded number of entries into an LLM context window.
const DefaultSearchLimit = 5

// SearchContext searches long-term memory for entries matching query,
// ranked by relevance (see LongTermMemory.Search), and returns at most
// DefaultSearchLimit results. Directive-tagged entries are excluded — they
// have their own dedicated retrieval path (ListDirectives) and including
// them here would risk the same directive text appearing twice in a single
// agent prompt (once via the directives list, once via a generic search
// hit) for no benefit, since directives already apply to every goal
// unconditionally.
func (m *Memory) SearchContext(query string) ([]*MemoryEntry, error) {
	return m.SearchContextTopN(query, DefaultSearchLimit)
}

// SearchContextTopN is like SearchContext but allows the caller to override
// the result limit. limit <= 0 means unbounded.
func (m *Memory) SearchContextTopN(query string, limit int) ([]*MemoryEntry, error) {
	entries, err := m.longTerm.SearchTopN(query, 0)
	if err != nil {
		return nil, err
	}
	filtered := entries[:0:0]
	for _, e := range entries {
		if hasTag(e.Tags, directiveTag) {
			continue
		}
		filtered = append(filtered, e)
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

// hasTag reports whether tags contains t (case-insensitive).
func hasTag(tags []string, t string) bool {
	for _, tag := range tags {
		if strings.EqualFold(tag, t) {
			return true
		}
	}
	return false
}

// GetWorldModel returns the live WorldModel instance.
func (m *Memory) GetWorldModel() *WorldModel {
	return m.worldModel
}

// ---------------------------------------------------------------------------
// Directive storage
//
// Directives (types.Directive) are standing behavioral constraints that
// should apply to every future goal, not just the one that produced them —
// e.g. a lesson learned by SelfImprovement ("always check disk space before
// writing large files") or a user-authored rule. They persist in long-term
// memory under the "directive:<id>" key, tagged "directive" so
// ListDirectives retrieves exactly the directive set via SearchByTag rather
// than a fuzzy substring search that could pull in unrelated entries.
// ---------------------------------------------------------------------------

const directiveTag = "directive"

func directiveKey(id string) string {
	return "directive:" + id
}

// AddDirective persists d in long-term memory, keyed by d.ID. If a directive
// with the same ID already exists, it is overwritten — use this to update an
// existing directive's description/priority.
func (m *Memory) AddDirective(d types.Directive) error {
	if d.ID == "" {
		return fmt.Errorf("memory: directive ID must not be empty")
	}
	if err := m.longTerm.Store(directiveKey(d.ID), d, []string{directiveTag}); err != nil {
		return fmt.Errorf("memory: add directive %q: %w", d.ID, err)
	}
	return nil
}

// ListDirectives returns every stored directive, in no particular order.
// Malformed entries (which should not occur in normal operation, but could
// arise from hand-edited storage files) are skipped rather than failing the
// whole list.
func (m *Memory) ListDirectives() ([]types.Directive, error) {
	entries, err := m.longTerm.SearchByTag(directiveTag)
	if err != nil {
		return nil, fmt.Errorf("memory: list directives: %w", err)
	}

	directives := make([]types.Directive, 0, len(entries))
	for _, e := range entries {
		d, err := decodeDirective(e.Value)
		if err != nil {
			continue
		}
		directives = append(directives, d)
	}
	return directives, nil
}

// RemoveDirective deletes the directive with the given ID.
func (m *Memory) RemoveDirective(id string) error {
	if err := m.longTerm.Forget(directiveKey(id)); err != nil {
		return fmt.Errorf("memory: remove directive %q: %w", id, err)
	}
	return nil
}

// decodeDirective converts a long-term entry's Value back into a
// types.Directive. Values stored via AddDirective round-trip through
// JSON-on-disk, so after a Store+Recall cycle Value is a
// map[string]interface{}, not the original struct — this re-marshals and
// unmarshals to recover the typed value.
func decodeDirective(v interface{}) (types.Directive, error) {
	if d, ok := v.(types.Directive); ok {
		return d, nil // same-process fast path, no disk round-trip yet
	}

	raw, err := json.Marshal(v)
	if err != nil {
		return types.Directive{}, err
	}
	var d types.Directive
	if err := json.Unmarshal(raw, &d); err != nil {
		return types.Directive{}, err
	}
	return d, nil
}

// ---------------------------------------------------------------------------
// goal_fulfillment.MemoryProvider implementation
// The interface requires: GetContext(key string) (string, error)
//                         StoreContext(key, value string) error
// ---------------------------------------------------------------------------

// GetContext retrieves context for key as a string, satisfying MemoryProvider.
// If the stored value is not a string, it is formatted via fmt.Sprintf.
func (m *Memory) GetContext(key string) (string, error) {
	v, ok := m.RecallContext(key)
	if !ok {
		return "", fmt.Errorf("memory: key %q not found", key)
	}
	switch s := v.(type) {
	case string:
		return s, nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

// ---------------------------------------------------------------------------
// module.Module implementation
// ---------------------------------------------------------------------------

// Name returns the module identifier.
func (m *Memory) Name() string { return "memory" }

// Health reports the operational status of the memory module.
// A missing or unwritable long-term storage directory is reported as degraded.
func (m *Memory) Health() module.ModuleHealth {
	// Attempt a probe write to verify long-term storage is accessible.
	probeKey := "__health_probe__"
	err := m.longTerm.Store(probeKey, "ok", []string{"internal"})
	if err == nil {
		_ = m.longTerm.Forget(probeKey)
		return module.ModuleHealth{
			Status:    "healthy",
			Details:   "session, world-model, and long-term storage are operational",
			Timestamp: time.Now(),
		}
	}
	return module.ModuleHealth{
		Status:    "degraded",
		Details:   fmt.Sprintf("long-term storage unavailable: %v", err),
		Timestamp: time.Now(),
	}
}

// Report returns operational metrics for the memory module within window.
func (m *Memory) Report(_ context.Context, _ time.Duration) (module.ModuleReport, error) {
	snap := m.worldModel.Snapshot()
	entries, _ := m.longTerm.Search("") // empty query = all entries
	return module.ModuleReport{
		ModuleName: m.Name(),
		Metrics: map[string]any{
			"world_model_facts":     len(snap),
			"long_term_entry_count": len(entries),
		},
	}, nil
}

// Tune applies a TuningChange to the memory module.
// No tuning parameters are currently supported; unknown paths are silently ignored.
func (m *Memory) Tune(_ context.Context, change module.TuningChange) error {
	// Reserved for future tuning parameters (e.g. eviction policy, max entries).
	_ = change
	return nil
}

// Capabilities returns the list of OS capability types this module requires.
func (m *Memory) Capabilities() []string {
	return []string{
		"read:file",
		"execute:config",
	}
}
