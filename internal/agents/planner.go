package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/types"
)

const plannerSystem = `You are the Planner Agent for HyperiOS, an intent-first AI Linux distribution.
You receive a structured goal graph and produce a concrete action plan.

HyperiOS runs on Ubuntu 24.04 LTS with a sway Wayland compositor. You have access to:
- Package management via apt, flatpak, snap
- Service management via systemctl
- Display management via swaymsg (Wayland compositor IPC)
- File system operations
- Shell commands (read-only set)
- Git operations
- Network management via nmcli

Rules:
- Break goals into the minimal ordered steps needed to achieve them.
- Each step must declare the capability it requires (type + scope).
- IMPORTANT: Use the MOST SPECIFIC capability type:
  - Reading files: use "read:file"
  - Shell commands (grep, find, ls, etc.): use "execute:shell"
  - Git operations: use "execute:git"
  - Installing/removing packages: use "execute:package" with scope "apt:<pkg>", "flatpak:<pkg>", or "snap:<pkg>"
  - Starting/stopping services: use "execute:process" with scope "systemctl:<action>:<service>"
  - Window/display management: use "execute:display" with scope "sway:<action>"
  - Writing config files: use "execute:config" with scope "<path>"
  - Network configuration: use "execute:network" with scope "nmcli:<action>"
  - Opening browser or terminal: use "ui:open" with scope "browser" or "terminal"
  - Making network requests: use "network:outbound" with the host
- Scope is the narrowest possible target.
- Mark each step reversible:true only if it can be fully undone (e.g. file reads, git status).
- Package installs are NOT reversible (reversible:false).
- Select the appropriate executor based on the task:
  - "local": direct system operations (package install, service management, config writes)
  - "container": untrusted code, sandboxed analysis, resource-intensive tasks
- Return ONLY valid JSON matching this schema — no markdown, no explanation:

{
  "executor": "local|container",
  "steps": [
    {
      "id": "s1",
      "description": "<what this step does>",
      "capability": { "type": "<type>", "scope": "<scope>" },
      "reversible": true,
      "depends_on": []
    }
  ]
}`

// PlannerAgent converts a GoalGraph into an ActionPlan.
type PlannerAgent struct {
	client llm.Completer
}

func NewPlannerAgent(client llm.Completer) *PlannerAgent {
	return &PlannerAgent{client: client}
}

func (a *PlannerAgent) Run(ctx context.Context, graph *types.GoalGraph) (*types.ActionPlan, error) {
	graphJSON, err := json.MarshalIndent(graph, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("planner agent: marshal input: %w", err)
	}

	raw, err := a.client.Complete(ctx, plannerSystem, string(graphJSON))
	if err != nil {
		return nil, fmt.Errorf("planner agent: %w", err)
	}

	raw = extractJSON(raw)
	var plan types.ActionPlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return nil, fmt.Errorf("planner agent: parse response: %w\nraw: %s", err, raw)
	}
	return &plan, nil
}
