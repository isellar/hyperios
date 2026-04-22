package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/types"
)

const intentSystem = `You are the Intent Agent for HyperiOS, an intent-first AI Linux distribution.
Your sole job is to convert a user's natural language request into a structured goal graph.

HyperiOS context: you are running as the controlling agent of a custom Linux OS built on Ubuntu 24.04.
You can install packages, manage services, control the display/compositor, and configure the system.

Rules:
- Do not propose actions. Only represent what the user wants to achieve.
- Decompose compound intents into individual goals with dependency relationships.
- Capture any relevant workspace context in the "context" field.
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
  ]
}`

// IntentAgent converts natural language + workspace context into a GoalGraph.
type IntentAgent struct {
	client llm.Completer
}

func NewIntentAgent(client llm.Completer) *IntentAgent {
	return &IntentAgent{client: client}
}

func (a *IntentAgent) Run(ctx context.Context, request string, ws types.WorkspaceContext) (*types.GoalGraph, error) {
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

	raw, err := a.client.Complete(ctx, intentSystem, user)
	if err != nil {
		return nil, fmt.Errorf("intent agent: %w", err)
	}

	raw = extractJSON(raw)
	var graph types.GoalGraph
	if err := json.Unmarshal([]byte(raw), &graph); err != nil {
		return nil, fmt.Errorf("intent agent: parse response: %w\nraw: %s", err, raw)
	}
	return &graph, nil
}

func indent(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

// extractJSON pulls the first JSON object out of a response that may have
// surrounding markdown fences or prose.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	// Strip markdown code fences if present
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
	// Find outermost { }
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		s = s[start : end+1]
	}
	return strings.TrimSpace(s)
}
