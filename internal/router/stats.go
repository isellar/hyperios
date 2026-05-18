package router

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Tier constants for intent execution levels.
const (
	TierNovel     = 0
	TierCached    = 1
	TierTemplated = 2
	TierTrusted   = 3
)

// Promotion thresholds.
const (
	PromoteToCached    = 1
	PromoteToTemplated = 3
	PromoteToTrusted   = 50
)

// IntentStats tracks execution statistics for promotion/demotion decisions.
type IntentStats struct {
	Intent       string        `json:"intent"`
	Tier         int           `json:"tier"`
	Count        int           `json:"count"`
	SuccessCount int           `json:"success_count"`
	Failures     []string      `json:"failures"`
	LastUsed     time.Time     `json:"last_used"`
	AvgDuration  time.Duration `json:"avg_duration_ms"`
	totalDuration time.Duration `json:"-"`
}

// StatsManager manages intent statistics across sessions.
type StatsManager struct {
	mu    sync.RWMutex
	stats map[string]*IntentStats
	path  string
}

// NewStatsManager creates a StatsManager, loading existing stats from disk.
func NewStatsManager(path string) *StatsManager {
	sm := &StatsManager{
		stats: make(map[string]*IntentStats),
		path:  path,
	}
	_ = sm.load()
	return sm
}

// RecordExecution records a single intent execution.
func (sm *StatsManager) RecordExecution(intent string, duration time.Duration, success bool, failureReason string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok := sm.stats[intent]
	if !ok {
		s = &IntentStats{
			Intent: intent,
			Tier:   TierNovel,
		}
		sm.stats[intent] = s
	}

	s.Count++
	s.LastUsed = time.Now()
	s.totalDuration += duration
	s.AvgDuration = s.totalDuration / time.Duration(s.Count)

	if success {
		s.SuccessCount++
		s.evaluatePromotion()
	} else {
		s.Failures = append(s.Failures, failureReason)
		if len(s.Failures) > 10 {
			s.Failures = s.Failures[len(s.Failures)-10:]
		}
		s.demote()
	}

	_ = sm.save()
}

// Get returns stats for an intent, or nil if not tracked.
func (sm *StatsManager) Get(intent string) *IntentStats {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.stats[intent]
}

// Tier returns the current execution tier for an intent.
func (sm *StatsManager) Tier(intent string) int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if s, ok := sm.stats[intent]; ok {
		return s.Tier
	}
	return TierNovel
}

func (sm *StatsManager) load() error {
	if sm.path == "" {
		return nil
	}

	data, err := os.ReadFile(sm.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("load stats: %w", err)
	}

	var stats map[string]*IntentStats
	if err := json.Unmarshal(data, &stats); err != nil {
		return fmt.Errorf("parse stats: %w", err)
	}

	sm.stats = stats
	return nil
}

func (sm *StatsManager) save() error {
	if sm.path == "" {
		return nil
	}

	dir := filepath.Dir(sm.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create stats dir: %w", err)
	}

	data, err := json.MarshalIndent(sm.stats, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}

	if err := os.WriteFile(sm.path, data, 0644); err != nil {
		return fmt.Errorf("write stats: %w", err)
	}

	return nil
}

func (s *IntentStats) evaluatePromotion() {
	switch s.Tier {
	case TierNovel:
		if s.SuccessCount >= PromoteToCached {
			s.Tier = TierCached
		}
	case TierCached:
		if s.SuccessCount >= PromoteToTemplated {
			s.Tier = TierTemplated
		}
	case TierTemplated:
		if s.SuccessCount >= PromoteToTrusted {
			s.Tier = TierTrusted
		}
	}
}

func (s *IntentStats) demote() {
	if s.Tier > TierNovel {
		s.Tier--
	}
}
