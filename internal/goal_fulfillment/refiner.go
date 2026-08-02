package goal_fulfillment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/types"
)

const refinerSystem = `You are the Goal Refiner for HyperiOS, an intent-first AI Linux distribution.
Your job is to take a user's raw intent and refine it into a clear, actionable goal.

Rules:
- Clarify ambiguous goals by asking targeted questions.
- Ensure the goal is specific, measurable, and achievable.
- Decompose compound intents into individual goals with dependency relationships.
- Capture relevant workspace context.
- Return ONLY valid JSON matching this schema — no markdown, no explanation:

{
  "intent": "<original user request>",
  "context": "<summary of relevant workspace context>",
  "goals": [
    {
      "id": "g1",
      "description": "<what needs to be achieved>",
      "depends_on": []
    }
  ],
  "clarification_needed": false,
  "clarification_question": ""
}`

type MemoryProvider interface {
	GetContext(key string) (string, error)
	StoreContext(key, value string) error
}

type ProcessorProvider interface {
	Lookup(query string) (string, error)
}

type Refiner struct {
	client    llm.Completer
	memory    MemoryProvider
	processor ProcessorProvider
}

func NewRefiner(client llm.Completer, memory MemoryProvider, processor ProcessorProvider) *Refiner {
	return &Refiner{
		client:    client,
		memory:    memory,
		processor: processor,
	}
}

type refinementResponse struct {
	Intent                string       `json:"intent"`
	Context               string       `json:"context"`
	Goals                 []types.Goal `json:"goals"`
	ClarificationNeeded   bool         `json:"clarification_needed"`
	ClarificationQuestion string       `json:"clarification_question"`
}

func (r *Refiner) RefineGoal(ctx context.Context, goal *types.Goal) (*types.Goal, error) {
	memoryContext := ""
	if r.memory != nil {
		if mc, err := r.memory.GetContext(goal.Description); err == nil {
			memoryContext = mc
		}
	}

	processorContext := ""
	if r.processor != nil {
		if pc, err := r.processor.Lookup(goal.Description); err == nil {
			processorContext = pc
		}
	}

	user := fmt.Sprintf(`Goal to refine: %q

Memory context:
%s

Processor context:
%s`,
		goal.Description,
		memoryContext,
		processorContext,
	)

	raw, err := r.client.CompleteWithRetry(ctx, refinerSystem, user)
	if err != nil {
		return nil, fmt.Errorf("refiner: %w", err)
	}

	raw = extractJSON(raw)
	var resp refinementResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("refiner: parse response: %w\nraw: %s", err, raw)
	}

	if resp.ClarificationNeeded {
		goal.State = types.GoalStateRefining
		goal.ClarificationQuestion = resp.ClarificationQuestion
		goal.NeedsAttention = true
		goal.UpdatedAt = time.Now()
		return goal, &ClarificationNeededError{Question: resp.ClarificationQuestion}
	}

	if len(resp.Goals) > 0 {
		refined := resp.Goals[0]
		// The LLM is asked to invent an "id" per the schema example (which
		// literally shows "id": "g1"), but that ID must never replace the
		// real tracked goal ID — doing so orphans the original goal (stuck
		// forever in its prior state) and creates an unrelated new one,
		// which is especially bad right after answering a clarification
		// question: the goal the user just answered would appear to vanish.
		refined.ID = goal.ID
		refined.State = types.GoalStateActive
		refined.CreatedAt = goal.CreatedAt
		refined.UpdatedAt = time.Now()
		refined.ClarificationQuestion = ""
		refined.NeedsAttention = false
		return &refined, nil
	}

	goal.State = types.GoalStateActive
	goal.ClarificationQuestion = ""
	goal.NeedsAttention = false
	goal.UpdatedAt = time.Now()
	return goal, nil
}

func (r *Refiner) RefineFromIntent(ctx context.Context, request string, ws types.WorkspaceContext) (*types.GoalGraph, error) {
	user := fmt.Sprintf(`User request: %q

Workspace context:
- Directory: %s
- Git branch: %s
- Recent commits:
%s
- Git status:
%s`,
		request,
		ws.Cwd,
		ws.GitBranch,
		indent(ws.GitLog),
		indent(ws.GitStatus),
	)

	raw, err := r.client.CompleteWithRetry(ctx, refinerSystem, user)
	if err != nil {
		return nil, fmt.Errorf("refiner: %w", err)
	}

	raw = extractJSON(raw)
	var resp refinementResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("refiner: parse response: %w\nraw: %s", err, raw)
	}

	now := time.Now()
	graph := &types.GoalGraph{
		Intent:  resp.Intent,
		Context: resp.Context,
	}
	for _, g := range resp.Goals {
		g.State = types.GoalStateActive
		g.CreatedAt = now
		g.UpdatedAt = now
		graph.Goals = append(graph.Goals, g)
	}

	return graph, nil
}

type ClarificationNeededError struct {
	Question string
}

func (e *ClarificationNeededError) Error() string {
	return fmt.Sprintf("clarification needed: %s", e.Question)
}

func indent(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, "```json"); idx >= 0 {
		s = s[idx+7:]
		if end := strings.Index(s, "```"); end >= 0 {
			s = s[:end]
		}
	} else if idx := strings.Index(s, "```"); idx >= 0 {
		s = s[idx+3:]
		if end := strings.Index(s, "```"); end >= 0 {
			s = s[:end]
		}
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		s = s[start : end+1]
	}
	return strings.TrimSpace(s)
}
