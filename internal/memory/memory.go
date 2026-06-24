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
	"fmt"
	"time"

	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/module"
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
	if err := m.session.StoreSession(key, value); err != nil {
		return fmt.Errorf("memory: session store: %w", err)
	}
	if err := m.longTerm.Store(key, value, nil); err != nil {
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

// SearchContext searches long-term memory for entries matching query.
func (m *Memory) SearchContext(query string) ([]*MemoryEntry, error) {
	return m.longTerm.Search(query)
}

// GetWorldModel returns the live WorldModel instance.
func (m *Memory) GetWorldModel() *WorldModel {
	return m.worldModel
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
