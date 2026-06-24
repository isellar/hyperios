// Package processor prioritises goals, delegates to autonomous agents, and
// coordinates execution results back to the goal-fulfilment system.
package processor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/memory"
	"github.com/isellar/hyperios/internal/types"
)

// AgentState is the lifecycle state of a spawned agent.
type AgentState string

const (
	AgentStateRunning   AgentState = "running"
	AgentStateCompleted AgentState = "completed"
	AgentStateFailed    AgentState = "failed"
)

// AgentResult holds the outcome of an agent run.
type AgentResult struct {
	Success bool
	Output  string
	Error   string
}

// Agent represents a spawned autonomous agent.
type Agent struct {
	ID        string
	GoalID    string
	State     AgentState
	Result    *AgentResult
	StartedAt time.Time
}

// MemoryQuerier is a narrow interface that lets the agent query memory without
// creating a direct dependency on *memory.Memory (which would risk circular
// imports if memory ever imports processor).
type MemoryQuerier interface {
	RecallContext(key string) (interface{}, bool)
	SearchContext(query string) ([]*memory.MemoryEntry, error)
}

// AgentSpawner creates and runs autonomous agents backed by an LLM.
type AgentSpawner struct {
	llmClient llm.Completer
}

// NewAgentSpawner returns an AgentSpawner that uses llmClient for completions.
func NewAgentSpawner(llmClient llm.Completer) *AgentSpawner {
	return &AgentSpawner{llmClient: llmClient}
}

const agentSystem = `You are an autonomous execution agent for HyperiOS.
You receive a goal and a set of directives that constrain your behaviour.
Your task is to reason about how to fulfil the goal, taking the directives
into account, and return a structured plain-text execution summary.

Return ONLY a short paragraph (≤200 words) describing what you would do to
achieve the goal. Do not include JSON, markdown headers, or code blocks.`

// Spawn creates a new Agent, calls the LLM with the goal + directive + memory
// context, and returns the resulting Agent with its result populated.
func (s *AgentSpawner) Spawn(
	ctx context.Context,
	goal *types.Goal,
	directives []types.Directive,
	mem MemoryQuerier,
) (*Agent, error) {
	if goal == nil {
		return nil, fmt.Errorf("agent spawner: goal must not be nil")
	}

	agent := &Agent{
		ID:        fmt.Sprintf("agent-%s-%d", goal.ID, time.Now().UnixNano()),
		GoalID:    goal.ID,
		State:     AgentStateRunning,
		StartedAt: time.Now(),
	}

	user := buildAgentPrompt(goal, directives, mem)

	output, err := s.llmClient.CompleteWithRetry(ctx, agentSystem, user)
	if err != nil {
		agent.State = AgentStateFailed
		agent.Result = &AgentResult{
			Success: false,
			Error:   fmt.Sprintf("llm error: %v", err),
		}
		return agent, nil // return agent (not error) so caller can fan result to goal system
	}

	agent.State = AgentStateCompleted
	agent.Result = &AgentResult{
		Success: true,
		Output:  strings.TrimSpace(output),
	}
	return agent, nil
}

// buildAgentPrompt assembles the user-turn prompt for the LLM agent.
func buildAgentPrompt(
	goal *types.Goal,
	directives []types.Directive,
	mem MemoryQuerier,
) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Goal ID: %s\n", goal.ID))
	sb.WriteString(fmt.Sprintf("Goal description: %s\n", goal.Description))
	sb.WriteString(fmt.Sprintf("Goal state: %s\n\n", goal.State))

	if len(directives) > 0 {
		sb.WriteString("Directives (must be respected):\n")
		for _, d := range directives {
			immutable := ""
			if d.Immutable {
				immutable = " [immutable]"
			}
			sb.WriteString(fmt.Sprintf("  - [priority %d%s] %s\n", d.Priority, immutable, d.Description))
		}
		sb.WriteString("\n")
	}

	if mem != nil {
		// Try to recall context keyed on the goal description.
		if v, ok := mem.RecallContext(goal.Description); ok {
			sb.WriteString(fmt.Sprintf("Recalled memory context:\n%v\n\n", v))
		}

		// Search for related memory entries.
		entries, err := mem.SearchContext(goal.Description)
		if err == nil && len(entries) > 0 {
			sb.WriteString("Related memory entries:\n")
			for _, e := range entries {
				sb.WriteString(fmt.Sprintf("  - [%s] %v\n", e.Key, e.Value))
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString("Based on the goal and constraints above, describe how you would fulfil this goal.")
	return sb.String()
}
