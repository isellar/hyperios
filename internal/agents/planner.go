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
- Shell commands
- Git operations
- Network management via nmcli
- Config file writes
- Outbound HTTP requests
- Scheduled tasks via systemd timers

CAPABILITY TYPES — use the most specific type for each step:
  read:file          scope: <absolute path or glob>      e.g. "/etc/nginx/nginx.conf"
  execute:shell      scope: <binary name>                e.g. "grep"
  execute:git        scope: git:<operation>              e.g. "git:status"
  execute:package    scope: <manager>:<package>          e.g. "apt:nginx", "flatpak:org.gnome.Gedit"
  execute:process    scope: systemctl:<action>:<service> e.g. "systemctl:restart:nginx"
  execute:display    scope: sway:<command>               e.g. "sway:workspace 2"
  execute:config     scope: <absolute path>              e.g. "/etc/nginx/nginx.conf"
  execute:network    scope: nmcli:<action>               e.g. "nmcli:device status"
  execute:schedule   scope: systemd:<name>               e.g. "systemd:backup-check"
  network:outbound   scope: <hostname>                   e.g. "api.anthropic.com"
  ui:open            scope: browser | terminal

COMMAND FIELD — every step must include a "command" array with the exact executable command:
  execute:shell:     ["grep", "-r", "pattern", "/path"]   ← read-only tools only; never use sudo here
  execute:git:       ["git", "status"]
  execute:package:   ["sudo", "apt-get", "-y", "install", "nginx"]
                     ["sudo", "apt-get", "update"]        ← apt-get update is execute:package, scope "apt:update"
                     ["sudo", "apt-get", "-y", "upgrade"] ← apt-get upgrade is execute:package, scope "apt:upgrade"
  execute:process:   ["sudo", "systemctl", "restart", "nginx"]
  execute:display:   ["swaymsg", "workspace 2"]
  execute:config:    ["/etc/nginx/nginx.conf", "<full file content here>"]
  execute:network:   ["nmcli", "device", "status"]
  execute:schedule:  ["backup-check", "0 8 * * 1", "check backup health"]
  network:outbound:  ["GET", "https://api.example.com/status"]
  read:file:         ["/etc/nginx/nginx.conf"]
  ui:open:           ["browser", "https://example.com"]  or  ["terminal"]

IMPORTANT: Never use sudo with execute:shell. Package operations (apt-get, dpkg --install) always use execute:package.
Local service health checks (curl localhost) use execute:shell with "curl", not network:outbound.

FAILURE POLICY — every step must include on_failure:
  "halt"   — stop the session immediately (use only for critical prerequisites where later steps cannot proceed)
  "retry"  — retry the step; include max_retries (int) and retry_backoff_seconds (int)
  "replan" — trigger a re-plan (use ONLY when a step fails in a way that requires a fundamentally different approach — e.g. a required service is down, a file doesn't exist that was expected to)
  "skip"   — continue to the next step (use for ALL probe/fallback/query steps; non-zero exit codes from queries like dpkg, which, snap are NORMAL and should be skip, not replan)

ON_FAILURE GUIDANCE — the most common mistake is using "replan" when "skip" is correct:
  - "dpkg -l somepackage" exits 1 if the package is not installed — this is information, use "skip"
  - "which somebinary" exits 1 if not found — this is information, use "skip"
  - "snap list somepackage" exits 1 if not installed — this is information, use "skip"
  - "google-chrome --version" failing because Chrome is not installed — use "skip"
  - Any fallback step in a chain of alternatives — always use "skip"
  - Only use "replan" if a step that was EXPECTED to succeed fails unexpectedly

PACKAGE AND VERSION QUERIES — use these exact patterns:
  To check if a deb package is installed:   ["dpkg-query", "-W", "-f=${Version}", "package-name"]
  To check if a binary exists:              ["which", "binary-name"]  (on_failure: "skip")
  To check snap packages:                   ["snap", "list", "package-name"]  (on_failure: "skip")
  To check Chrome version via dpkg:         ["dpkg-query", "-W", "-f=${Version}", "google-chrome-stable"]
  To check Chrome version via the binary:   ["google-chrome", "--version"]  — only use if "which google-chrome" succeeded first

RULES:
- Break goals into the minimal ordered steps needed to achieve them.
- command array must contain only literal strings — no shell interpolation, no variables.
- command[0] must be the exact binary name (resolvable by exec.LookPath on Linux).
- For execute:config, command[0] is the file path and command[1] is the complete file content.
- Scope is the narrowest possible target — never use wildcards unless necessary.
- Mark reversible:true only if the step can be fully undone (file reads, git reads, status queries).
- Package installs, service restarts, config writes are NOT reversible (reversible:false).
- Use "local" executor for all direct system operations.
- Use "container" executor for untrusted code, sandboxed analysis, resource-intensive tasks.
- For version/presence queries, prefer dpkg-query over dpkg -l: dpkg-query exits non-zero cleanly when a package is absent, and its output is easier to parse.

Return ONLY valid JSON — no markdown fences, no explanation, no text before or after:

{
  "name": "Install and start nginx web server",
  "executor": "local",
  "steps": [
    {
      "id": "s1",
      "description": "check if nginx is installed",
      "capability": { "type": "execute:shell", "scope": "dpkg" },
      "command": ["dpkg", "-l", "nginx"],
      "reversible": true,
      "depends_on": [],
      "on_failure": "skip"
    },
    {
      "id": "s2",
      "description": "install nginx",
      "capability": { "type": "execute:package", "scope": "apt:nginx" },
      "command": ["sudo", "apt-get", "-y", "install", "nginx"],
      "reversible": false,
      "depends_on": ["s1"],
      "on_failure": "halt"
    },
    {
      "id": "s3",
      "description": "start nginx service",
      "capability": { "type": "execute:process", "scope": "systemctl:start:nginx" },
      "command": ["sudo", "systemctl", "start", "nginx"],
      "reversible": false,
      "depends_on": ["s2"],
      "on_failure": "retry",
      "max_retries": 2,
      "retry_backoff_seconds": 3
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

	raw, err := a.client.CompleteWithRetry(ctx, plannerSystem, string(graphJSON))
	if err != nil {
		return nil, fmt.Errorf("planner agent: %w", err)
	}

	raw = extractJSON(raw)
	if raw == "" {
		return nil, fmt.Errorf("planner agent: empty response after JSON extraction")
	}

	var plan types.ActionPlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return nil, fmt.Errorf("planner agent: parse response: %w\nraw: %s", err, raw)
	}

	// Validate that every step has a non-empty Command — this is a hard requirement.
	for _, step := range plan.Steps {
		if len(step.Command) == 0 {
			return nil, fmt.Errorf("planner agent: step %q is missing required 'command' field", step.ID)
		}
		if step.OnFailure == "" {
			return nil, fmt.Errorf("planner agent: step %q is missing required 'on_failure' field", step.ID)
		}
	}

	return &plan, nil
}
