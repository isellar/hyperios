// Package self_improvement analyzes execution results, detects patterns, and
// creates improvement goals that feed back into the goal fulfillment pipeline.
package self_improvement

import (
	"sync"
)

// Stats tracks execution metrics for the self-improvement module.
type Stats struct {
	mu sync.RWMutex

	totalGoals    int
	successCount  int
	failureCount  int
	totalDuration int64 // ms

	// goalResults maps goalID → list of (success, errorMsg) pairs.
	goalResults map[string][]goalRecord

	// toolUsage maps toolID → (authorized, unauthorized) counts.
	toolUsage map[string]toolRecord
}

type goalRecord struct {
	description string
	success     bool
	errorMsg    string
}

type toolRecord struct {
	authorized   int
	unauthorized int
}

// NewStats returns an initialised Stats instance.
func NewStats() *Stats {
	return &Stats{
		goalResults: make(map[string][]goalRecord),
		toolUsage:   make(map[string]toolRecord),
	}
}

// RecordGoalResult records the outcome of a single goal execution.
func (s *Stats) RecordGoalResult(goalID string, success bool, durationMs int64, errorMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalGoals++
	s.totalDuration += durationMs

	if success {
		s.successCount++
	} else {
		s.failureCount++
	}

	s.goalResults[goalID] = append(s.goalResults[goalID], goalRecord{
		success:  success,
		errorMsg: errorMsg,
	})
}

// RecordGoalResultWithDescription records the outcome of a goal execution including
// the human-readable description (used by FailurePatterns).
func (s *Stats) RecordGoalResultWithDescription(goalID, description string, success bool, durationMs int64, errorMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalGoals++
	s.totalDuration += durationMs

	if success {
		s.successCount++
	} else {
		s.failureCount++
	}

	s.goalResults[goalID] = append(s.goalResults[goalID], goalRecord{
		description: description,
		success:     success,
		errorMsg:    errorMsg,
	})
}

// RecordToolUsage records a single tool invocation.
func (s *Stats) RecordToolUsage(toolID string, authorized bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := s.toolUsage[toolID]
	if authorized {
		r.authorized++
	} else {
		r.unauthorized++
	}
	s.toolUsage[toolID] = r
}

// SuccessRate returns the fraction of goals that succeeded (0.0 if none recorded).
func (s *Stats) SuccessRate() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.totalGoals == 0 {
		return 0.0
	}
	return float64(s.successCount) / float64(s.totalGoals)
}

// FailurePatterns returns the descriptions of goals that failed more than once.
// Descriptions are deduplicated; only goals with more than one failure record are included.
func (s *Stats) FailurePatterns() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[string]int) // description → failure count
	for _, records := range s.goalResults {
		failCount := 0
		desc := ""
		for _, r := range records {
			if !r.success {
				failCount++
				if r.description != "" {
					desc = r.description
				}
			}
		}
		if failCount > 1 && desc != "" {
			seen[desc] += failCount
		}
	}

	patterns := make([]string, 0, len(seen))
	for desc := range seen {
		patterns = append(patterns, desc)
	}
	return patterns
}

// StatsSummary is a point-in-time snapshot of the stats.
type StatsSummary struct {
	TotalGoals      int     `json:"total_goals"`
	SuccessCount    int     `json:"success_count"`
	FailureCount    int     `json:"failure_count"`
	SuccessRate     float64 `json:"success_rate"`
	AvgDurationMs   float64 `json:"avg_duration_ms"`
	TotalToolCalls  int     `json:"total_tool_calls"`
	UnauthorizedCalls int   `json:"unauthorized_calls"`
}

// Summary returns a StatsSummary snapshot.
func (s *Stats) Summary() StatsSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var avgDur float64
	if s.totalGoals > 0 {
		avgDur = float64(s.totalDuration) / float64(s.totalGoals)
	}

	totalToolCalls := 0
	unauthorizedCalls := 0
	for _, r := range s.toolUsage {
		totalToolCalls += r.authorized + r.unauthorized
		unauthorizedCalls += r.unauthorized
	}

	successRate := 0.0
	if s.totalGoals > 0 {
		successRate = float64(s.successCount) / float64(s.totalGoals)
	}

	return StatsSummary{
		TotalGoals:        s.totalGoals,
		SuccessCount:      s.successCount,
		FailureCount:      s.failureCount,
		SuccessRate:       successRate,
		AvgDurationMs:     avgDur,
		TotalToolCalls:    totalToolCalls,
		UnauthorizedCalls: unauthorizedCalls,
	}
}
