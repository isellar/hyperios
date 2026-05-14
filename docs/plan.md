# HyperiOS — High-Level Plan

## Concept

HyperiOS is a Linux distribution where **the agent is the primary interface**. The OS exists to serve intent. Applications are infrastructure — installed, configured, surfaced, and hidden by the controlling agent as needed. The user talks to HyperiOS; HyperiOS manages everything beneath.

This is not an AI assistant running on top of an OS. The OS is built around the agent.

---

## Architecture

```
User (text terminal / voice)
        |
[Hyperi Shell — TUI]         [Web UI — future, remote access only]
  bubbletea, always-on             browser via WebSocket
        |                                  |
        +──────────────┬────────────────────+
                       |
                  [Event Bus]
                  chan Event — buffered; all producers write here;
                  TUI + web UI consume; audit log always writes
                       |
              [Agent Pipeline]
  Intent Agent -> Planner Agent -> Adversarial Agent -> Policy Arbiter -> CommandValidator
                       |
        [Capability-Gated Executor]
          |- execute:shell       — literal command via Command []string
          |- execute:package     — apt / flatpak / snap
          |- execute:process     — systemctl
          |- execute:display     — swaymsg + AT-SPI + vision (layered)
          |- execute:config      — config file writes
          |- execute:network     — nmcli
          |- execute:schedule    — systemd timer or in-process cron
          |- read:file           — filesystem reads
          `- execute:git         — git operations
                       |
        [Ubuntu 24.04 LTS Server — minimal base]
          + sway compositor (kiosk mode, Phase 4)
          + Hyperi systemd service (boot -> agent ready)
          + system manifest (/var/lib/hyperi/manifest.json)
          + audit trail (/var/log/hyperi/audit.jsonl)
```

---

## Design Principles

**Safety is architectural, not behavioral.**
Constraints live below the agent layer. No LLM is trusted to self-govern. A deterministic, non-LLM Policy Arbiter has the final say over what executes.

**The OS is the security boundary.**
Linux user permissions, file ownership, and PAM are not supplementary to the capability system — they are a proven, kernel-enforced security layer that the agent operates within. The `hyperi` process runs as a dedicated system user with restricted privileges. Actions requiring elevated access (package installation, service management, config writes outside the agent's home) require either a real user authentication event (PAM/sudo with human password entry) or an explicit runtime capability grant. The agent cannot escalate its own privileges. What a `chmod 750` denies, no amount of LLM reasoning can override.

**Modularity first.**
Every layer is swappable. The compositor (sway) can be replaced with a more capable one. The STT/TTS backend can be swapped. The executor backends are pluggable. The agent pipeline is interface-driven.

**The OS serves intent.**
Applications are not installed for users to manage. They are infrastructure that the agent installs, configures, and surfaces as needed to fulfill a command, then returns to the background.

---

## Build Phases

### Phase 0: Base Distro (Foundation)
- Ubuntu 24.04 LTS Server minimal as base
- `cloud-init` for automated first-boot provisioning
- `preseed` for bare metal unattended install
- Custom GRUB, minimal boot, no desktop environment
- Hyperi Go binary as a `systemd` service, starts on boot
- Serial/TTY console fallback for headless debugging
- `hyperi` system user created with restricted permissions; `hyperi-admin` group defined
- `/etc/sudoers.d/hyperi` configured: allowlisted commands without password, elevated commands require PAM auth
- `Vagrantfile` — dev VM; Ubuntu 24.04, provisions via cloud-init, headless sway, repo synced
- `just dev` / `just dev-ssh` / `just dev-destroy` — VM lifecycle targets in justfile
- Real machine testing path documented: stock Ubuntu 24.04 Server ISO + cloud-init + built binary; no ISO builder required

### Phase 1A: Agent Core — Make the Pipeline Correct
Goal: the binary runs correctly end-to-end on the dev VM. Commands are literal, validated, and auditable. Steps fail gracefully per policy. Testable against real Linux operations.

- Port the agent pipeline as-is: Intent -> Planner -> Adversarial -> Arbiter
- Replace Windows executor with Linux-native executor
- Add `Command []string` to `ActionStep`; remove NL keyword extraction from executor
- Add `on_failure`, `MaxRetries`, `RetryBackoffSeconds` to `ActionStep`
- Add `ReadyCondition` to `ActionStep`; executor poll loop for long-running steps
- Add `CommandValidator` — structural check + allowlist membership; manifest path check stubbed until Phase 1C
- Fix all five known inherited bugs (glob matching, `errors.As`, `Progress()` overcount, `executeNetwork()` stub, `executeConfig()` stub)
- `executeConfig()` — implement fully: `Command ["<path>", "<content>"]`; `os.WriteFile`; OS permissions are the safety boundary
- `executeNetwork()` — implement fully: `Command ["GET|POST", "<url>", "<optional-body>"]`; `net/http`; response body in `ExecutionResult.Output`
- New Linux capability types fully implemented (not stubbed): `execute:package`, `execute:process`, `execute:config`, `execute:network`
- Update Planner system prompt: emit `Command []string`, `on_failure`, `ReadyCondition` per step
- Audit trail writes to `/var/log/hyperi/`
- Sessions stored at `/var/lib/hyperi/sessions/`

**Exit criteria:** `hyperi session start --execute` runs a real multi-step plan on the dev VM, executes correctly, handles step failure per `on_failure` policy, and produces a meaningful audit trail.

### Phase 1B: Agent Core — Make it Persistent and Recoverable
Goal: sessions survive crashes, re-plans work, autonomy levels change behavior, every task has a complete execution record.

- `internal/plan/` — plan doc writer; all pipeline stage writers; resume parser with explicit parsing contract (see Plan Document section)
- Session JSON slimmed to thin index; plan doc is the execution record
- Re-plan loop in `cmd/hyperi/main.go`: 3 attempts max; conditional Adversarial Agent re-run
- Crash recovery: on startup, scan for `in-progress` plan docs; resume from last completed step without LLM call
- `internal/config/` — global config (`autonomy_level`, watch paths); `hyperi config` CLI subcommand
- Autonomy level wired into Arbiter; `ArbiterVerdict` records level at evaluation time
- Plan docs at `/var/lib/hyperi/plans/`
- LLM pipeline-stage failure handling: stage-level retry with exponential backoff; `hyperi-meta` stage status blocks; resume from failed stage without re-running completed stages
- Approval timeout: configurable foreground (5m) and background (30s) timeouts; `halt` on expiry; plan doc records timeout
- `hyperi plans` subcommand: list plans by status
- `hyperi session resume <id>`: wired to re-enter pipeline correctly from halted state

**Exit criteria:** kill `hyperi` mid-execution, restart, confirm it resumes from the correct step. Run a plan that triggers a re-plan. Change autonomy level and confirm Arbiter behavior changes.

### Phase 1C: Agent Core — Make it Observable and Time-Aware
Goal: event bus exists and is proven; TUI (Phase 2) has everything it needs to build against; scheduled tasks work; manifest is live.

- Event bus (`internal/bus/`): buffered `chan Event`, all event types, audit log consumer
- In-process scheduler (`robfig/cron`): manifest re-scan cadence, session cleanup, audit log rotation
- System manifest (`internal/manifest/`): first-boot scan, inotify watcher with `IN_MOVED_TO` handling, post-execution hook, startup reconciliation scan
- `CommandValidator` manifest path check wired (was stubbed in 1A)
- `execute:schedule` capability type: `systemd` and `cron` backends
- `/proc/sys/fs/inotify/max_user_watches` set in distro config

**Exit criteria:** a background session fires via scheduler and its result surfaces in the foreground TUI without user prompting. The manifest reflects a real filesystem change within seconds. `execute:schedule` creates a working systemd timer.

### Phase 2: Terminal Shell Interface
- Hyperi Shell: persistent TUI (charmbracelet/bubbletea)
- Replaces cobra CLI — always running, not invoked per-command
- Text input -> agent pipeline -> streamed output
- TUI consumes event bus: renders step progress, plan completion, alerts, and background session results without user prompting
- Inline plan display with approval prompts for `modified` verdicts (delivered via event bus `EventApprovalNeeded`; first reply wins)
- Session context persists across commands
- Foreground/background session model: TUI holds foreground lock; scheduled tasks run as background sessions, results buffered to event bus
- User-directed scheduled tasks via `execute:schedule:systemd` (create systemd timer + service)
- Web UI (`ui/server.go`, `ui/frontend/`) is carry-over scaffolding — not active Phase 2 development

### Phase 3: Voice Interface
- Opt-in, not always-on (user activates or configures)
- STT: local-first via whisper.cpp, fallback to API
- TTS: local via piper, fallback via espeak-ng
- Voice is just another input path into the same agent pipeline and event bus

### Phase 4: Display Management
- sway compositor (Wayland, i3-compatible, scriptable via swaymsg IPC)
- Executor gains full `execute:display` capability
- Layered interaction model: CLI/API → AT-SPI → vision model → ydotool (last resort)
- AT-SPI integration for native GTK/Qt apps
- Vision model integration: grim screenshot → LLM vision API for Electron/browser content
- All display steps require `ReadyCondition` (`atspi:present` or `vision:confirms`)
- Hyperi Shell runs fullscreen in workspace 1; apps summoned into workspace 2+

### Phase 5: ISO Builder
- live-build pipeline
- Produces bootable `.iso` installable on bare metal laptops — for distribution, not development
- Includes: base OS + sway + Hyperi binary + default config
- Post-install setup runs via cloud-init on first boot
- `just build-iso` target; separate from `just build-image` (QEMU disk image for heavier pre-ISO testing)
- The Vagrantfile and cloud-init provisioning from Phase 0 serve as the reference spec for what the ISO must produce

---

## Agent Taxonomy

| Agent | File | Responsibility |
|---|---|---|
| Intent Agent (IA) | `internal/agents/intent.go` | Converts natural language to goal graph (no execution) |
| Planner Agent (PA) | `internal/agents/planner.go` | Proposes action sequences (no execution) |
| Adversarial Agent (AA) | `internal/agents/adversarial.go` | Actively tries to break plans before execution |
| Policy Arbiter | `internal/arbiter/arbiter.go` | Deterministic, rule-based final authority (non-LLM) |
| Executor | `internal/executor/` | Performs only approved steps |

### Plan Validation Flow
```
User Intent
    │
Intent Agent ─────────────────────────────► Plan Doc (intent + goal graph)
    │
Planner Agent ────────────────────────────► Plan Doc (action steps + on_failure policy)
    │
Adversarial Agent ────────────────────────► Plan Doc (risk report)
    │
Policy Arbiter ───────────────────────────► Plan Doc (verdicts inline with steps)
    │
CommandValidator (deterministic, no LLM)
    │
Executor ─────────────────────────────────► Plan Doc (step results, exact output, timestamps)
    │
    ├── Step success → next step
    ├── Step failure: on_failure=retry → retry loop → Plan Doc
    ├── Step failure: on_failure=replan → Re-plan pass → append to Plan Doc
    ├── Step failure: on_failure=skip → mark skipped → Plan Doc → next step
    └── Step failure: on_failure=halt → halt → Plan Doc → surface to user
```

The plan document is written to disk at each stage and updated in real time during execution. Pipeline stages are sequential per task — no concurrent writes. A crash at any point is recoverable: restart reads the plan doc, identifies the last completed step, and resumes without an LLM call.

The `CommandValidator` runs deterministic pre-checks (structural validity, allowlist membership, manifest path lookup) after the Arbiter approves a step and before the Executor touches the OS. It is not an LLM — it is a cheap, synchronous gate that catches structural problems the Arbiter is not designed to handle.

---

## Session Model

HyperiOS is single-user for v1. There is one agent pipeline, one event bus, and one active foreground session at a time. Concurrency is handled by distinguishing session modes, not by blocking all concurrency.

**Session modes:**

| Mode | Trigger | TUI interaction | Event bus |
|---|---|---|---|
| Foreground | User input in TUI | Inline — renders plan, prompts, output | Publishes all events; TUI consumes immediately |
| Background | Scheduler, systemd timer | None — no TUI interaction | Publishes all events with `background:true`; TUI buffers and renders when idle |

**Foreground lock:**
A lock file at `/var/lib/hyperi/session.lock` records the active foreground session PID. If a second foreground attempt is made while one is active (e.g. user opens a second terminal and runs hyperi), it is rejected with a clear message pointing to the active session. Scheduled and system-initiated sessions never attempt to acquire the foreground lock — they always run as background sessions.

**Background session delivery:**
Background session events are buffered on the event bus. The TUI renders them when it is idle (no active foreground session step in progress) or when the foreground session ends. If the TUI is not running at all (user logged out), events are written to the audit log and lost from the display perspective — acceptable for v1.

**Approval prompts with multiple interfaces:**
`EventApprovalNeeded` is published to the event bus. Any active interface (TUI, or web UI in a future phase) can render the prompt and respond. The first response closes the reply channel; subsequent responses from any interface are discarded. The pipeline pauses waiting on the reply channel until a response arrives or the approval times out.

**Approval timeouts (configurable in `config.json`):**
- Foreground session: 5 minutes (default) — user is present
- Background session: 30 seconds (default) — no human present; fail fast

On timeout: `halt` — step does not execute; plan doc records `Approval: timed out`; session status set to `halted`; `EventPlanFailed` published with reason `approval-timeout`. Silently skipping a step the Arbiter flagged is not acceptable.

**Halted plan discovery:**
On TUI startup, if any plans have status `halted` or `in-progress`, the shell surfaces a notification before the main prompt. `hyperi plans` lists all plans by status. `hyperi session resume <id>` re-presents the pending approval prompt for approval-halted plans, or re-enters the re-plan loop for execution-halted plans.

**Multi-user (post-v1):**
The OS permission model (`hyperi` user, per-user home directories, PAM) is structured to make per-OS-user sessions a feasible future extension. Each OS user would have their own session state, capability grants, and autonomy level stored under their home directory. This is not designed now but not designed out — nothing in the v1 session model prevents it.

**Interfaces:**
- **TUI (v1)** — primary on-device interface; works headless, over SSH, without a display server; always present
- **Web UI (future)** — remote access interface; browser-based; connects to the same event bus via WebSocket; not v1 scope

---

## Plan Document

The plan document is the single source of truth for a task. It is a markdown file written to disk before execution begins and updated in real time as each pipeline stage completes. It survives crashes, process restarts, and re-plans. The full history of a task — intent, risk assessment, arbiter verdicts, execution results, and any re-plans — lives in one file.

**Location:** `/var/lib/hyperi/plans/<session-id>.md`

**Session index:** `/var/lib/hyperi/sessions/<session-id>.json` — thin metadata only (id, intent summary, status, timestamps, plan doc path). All substantive content is in the plan doc.

**Who writes what:**

| Stage | Writes to plan doc |
|---|---|
| Intent Agent | Goal graph — structured goals, dependencies, context |
| Planner | Action steps — command, capability, `on_failure` policy, `ReadyCondition` |
| Adversarial Agent | Risk report — per-step risk flags, severity, counterfactuals |
| Arbiter | Verdict inline with each step — approved / modified / blocked + reason |
| Executor | Step results — exact command run, stdout, stderr, exit code, duration, timestamp |

**Step failure policy (`on_failure`):**
Specified by the Planner per step. The executor enforces it — failure handling is not hardcoded.

| Value | Behavior |
|---|---|
| `halt` | Stop immediately; surface full failure context to user |
| `retry` | Re-execute up to `max_retries` times with `retry_backoff_seconds`; then `halt` |
| `replan` | Trigger re-plan pass with full failure context; counts against re-plan budget |
| `skip` | Mark skipped, continue with remaining steps; for non-critical steps |

**Re-plan budget:** 3 total attempts (2 re-plans) per task.
- Attempt 1: original plan — automatic
- Attempt 2: first re-plan — automatic; Planner receives full prior execution context from plan doc
- Attempt 3: second re-plan — requires user confirmation (`EventApprovalNeeded`) before proceeding
- After attempt 3 fails: session halts; full plan doc surfaced to user

Re-plans append a `## Re-plan N` section to the same plan doc. The full prior history is preserved above it. A single plan doc covers the entire task regardless of how many attempts it required.

**Adversarial Agent on re-plans:**
- Re-plan introduces no new capability types and no new paths → skip; proceed directly to Arbiter
- Re-plan introduces new capability types or new paths → run Adversarial Agent on new steps only
- Decision is deterministic (diff new steps against prior plan) — no LLM call needed to make it

**LLM pipeline-stage failure handling:**
Each pipeline stage (Intent, Planner, Adversarial) writes a `hyperi-meta` block with `status: in-progress` when it starts and updates to `completed` or `failed` when it finishes. If an LLM call fails:

| Error type | Retry policy |
|---|---|
| Network error / HTTP 5xx | Up to 3 retries; exponential backoff (2s, 4s, 8s) |
| Rate limit (HTTP 429) | Up to 5 retries; respect `Retry-After` header |
| Malformed JSON response | Up to 2 retries; if still malformed, halt and surface to user |
| All other errors | Halt immediately; surface full error context |

On resume after a stage failure, the parser finds the first stage with `status: in-progress` or `status: failed` and re-runs from that stage — completed stages are not re-run. This means a failed Planner re-runs only the Planner, reusing the already-written goal graph.

**Resume after crash:**
On restart, scan plan docs with `status: in-progress`. For each:
1. Check each pipeline stage's `hyperi-meta` status block in order — find the first non-completed stage
2. If a stage is incomplete, re-run from that stage (reuses prior stage outputs)
3. If all stages are complete, find the last completed execution step via `hyperi-meta result:` fields
4. Resume from next pending step — no redundant LLM calls
5. Update `status` and `Updated` frontmatter in plan doc

**Parsing contract:**
The plan doc is both human-readable and machine-parseable. Machine-readable fields are isolated in `hyperi-meta` fenced blocks. LLM output and command stdout/stderr are in separate fenced blocks that the parser never reads. This prevents false positives from command output or LLM prose containing field-like strings.

- `hyperi-meta` — structured key-value fields; only block the resume parser reads
- `hyperi-intent` / `hyperi-plan` / `hyperi-risk` — LLM stage output; human-readable, not parsed for fields
- `output` — raw command stdout/stderr; never parsed

**Plan doc structure:**
````markdown
# Task: <intent summary>
Session: <session-id>
Status: in-progress | completed | failed | halted
Attempt: 1
Created: <timestamp>
Updated: <timestamp>

## Intent

```hyperi-meta
stage: intent
status: completed
started: <timestamp>
completed: <timestamp>
```

```hyperi-intent
(goal graph — goals, dependencies, context)
```

## Plan

```hyperi-meta
stage: plan
status: completed
started: <timestamp>
completed: <timestamp>
```

```hyperi-plan
(action steps — command, capability, on_failure, ready_condition)
```

## Risk Report

```hyperi-meta
stage: adversarial
status: completed
started: <timestamp>
completed: <timestamp>
```

```hyperi-risk
(per-step risk flags, severity, counterfactuals)
```

## Execution

### Step 1: <description>
Verdict: approved

```hyperi-meta
result: success
exit_code: 0
started: <timestamp>
duration_ms: 142
```

```output
(exact stdout)
```

### Step 2: <description>
Verdict: modified — requires user approval
Approval: granted at <timestamp>

```hyperi-meta
result: failure
exit_code: 1
started: <timestamp>
duration_ms: 3201
on_failure: replan
```

```output
E: Unable to fetch some archives, try apt-get update or --fix-missing
```

## Re-plan 1
Triggered: Step 2 failure
Attempt: 2 of 3
User confirmation: not required

### Plan (Re-plan 1)
(revised action steps)

### Risk Report (Re-plan 1)
Skipped — no new capability types or paths introduced

### Execution (Re-plan 1)

### Step 1: ...
````

---

## OS Security Model

The agent runs as a dedicated `hyperi` system user. Linux DAC (discretionary access control) is the outermost and most trusted security boundary — enforced by the kernel, not by any code in this repo.

**User/group layout:**
- `hyperi` — the agent's OS identity; owns `/var/lib/hyperi/`, `/var/log/hyperi/`; no sudo by default
- `hyperi-admin` — elevated group; membership grants sudo for specific allowlisted commands via `/etc/sudoers.d/hyperi`
- Human user (e.g. `user`) — owns the interactive session; can authenticate via PAM to approve elevated actions

**What this buys us:**
- File system boundaries are kernel-enforced: the agent cannot read `/root/`, `/etc/shadow`, or user home directories it doesn't own unless permissions are explicitly set
- Package installation, service management, and config writes outside `/var/lib/hyperi/` all require `sudo` — which requires either PAM authentication (human enters password) or an explicit sudoers rule
- Actions that require human authentication create a natural, OS-enforced confirmation step that no capability grant can bypass
- The audit trail at `/var/log/hyperi/audit.jsonl` is owned by `hyperi` and append-only; the `hyperi` user cannot delete or truncate it

**Interaction with the capability system and autonomy levels:**
Linux permissions are the outermost precondition layer. A step can be blocked at any of four layers, evaluated in order:

1. **OS permissions** — kernel denies the syscall; PAM requires human authentication; no code in this repo can override this
2. **Capability allowlist** — capability type + scope not on the pre-approved list in `config/allowlist.yaml`
3. **Arbiter** — on the list, but this specific plan or context is rejected based on risk flags
4. **Autonomy level** — arbiter verdict is `modified` but current autonomy level auto-approves it (or auto-blocks at level 0)

The sudoers file defines exactly which elevated commands `hyperi` can run without a password (e.g. `systemctl status *`), which require a PAM-authenticated password (e.g. `apt install`), and which are never permitted (e.g. `visudo`, `passwd`). PAM requirements operate at layer 1 — they are not part of the capability system and cannot be bypassed by capability grants or autonomy level.

---

## Capability System

Not role-based — scoped capabilities with TTL, revocability, and audit trail.

**Three-layer defense:**
1. **OS permissions layer** — kernel-enforced; `hyperi` user's filesystem and syscall access; PAM for human-authenticated escalation
2. **Allowlist layer** (`config/allowlist.yaml`) — binary pre-approval; only listed commands can ever run
3. **Arbiter layer** — context-aware judgment on each specific plan at runtime

**Capability types for HyperiOS:**
- `read:file:<path>` — filesystem reads
- `execute:shell:<cmd>` — shell commands (binary name only; full command in `ActionStep.Command`)
- `execute:git:<op>` — git operations (status, log, diff, branch)
- `execute:package:<manager>:<pkg>` — apt/flatpak/snap install/remove
- `execute:process:systemctl:<action>:<service>` — service management
- `execute:display:sway:<cmd>` — compositor window control
- `execute:config:<path>` — write config files
- `execute:network:nmcli:<action>` — network configuration
- `execute:schedule:<backend>:<name>` — create/remove scheduled tasks (systemd timer or in-process cron)
- `network:outbound:<host>` — outbound HTTP calls
- `ui:open:<target>` — open browser or terminal

---

## ActionStep Data Model

`ActionStep` is the fundamental unit of execution. Every agent plan is a sequence of `ActionStep` values. The Arbiter, CommandValidator, and Executor all operate on this struct.

**Current + planned fields:**
```go
type ActionStep struct {
    ID          string     // unique within plan
    Description string     // human-readable intent (for display and audit)
    Capability  Capability // type + scope — used by allowlist and arbiter
    Command     []string   // literal command to execute, e.g. ["grep", "-r", "foo", "/etc"]
    Reversible  bool       // whether this step can be undone
    DependsOn   []string   // step IDs that must complete before this runs

    // Failure policy — specified by the Planner, enforced by the Executor.
    // The Planner sets these when generating the plan based on the nature of
    // the step. The executor does not make failure handling decisions.
    OnFailure           string // "halt" | "retry" | "replan" | "skip"
    MaxRetries          int    // only used when OnFailure == "retry"
    RetryBackoffSeconds int    // seconds between retries; 0 = no backoff

    // ReadyCondition — optional. If set, the executor polls this condition after
    // running the command and before marking the step complete. This is the
    // mechanism for handling long-running steps, service startup waits, UI
    // readiness checks, and any step whose effect is not instantaneous.
    ReadyCondition *ReadyCondition
}

type ReadyCondition struct {
    Type            string // "exit:0" | "process:active" | "file:exists" |
                           // "output:contains" | "http:ok" | "atspi:present" |
                           // "vision:confirms"
    Target          string // service name, file path, URL, text to match, etc.
    TimeoutSeconds  int    // fail the step if condition not met within this window
    PollIntervalSeconds int // how often to re-check (default 2s)
    RetryCommand    bool   // if true, re-execute Command on each poll; if false,
                           // only check condition (useful for idempotent commands)
}
```

**Why `ReadyCondition` is a field, not a separate step type (v1 decision):**
Keeping it as a field on `ActionStep` couples the wait to the step that needs it, which is simpler for the Planner to emit and the Executor to handle. A separate wait step type is architecturally purer (the Adversarial Agent could reason about it independently, it would appear in the audit trail as a discrete event) but adds Planner complexity that isn't justified yet. This is explicitly a v1 decision — promote to a first-class step type in a later phase if the Adversarial Agent needs to reason about wait conditions independently.

**`ReadyCondition` types and their uses:**

| Type | Use case | Implementation |
|---|---|---|
| `exit:0` | Command must succeed (default for most steps) | Check `ExitCode` |
| `process:active` | Service must be running after restart | `systemctl is-active` poll |
| `file:exists` | Wait for a file to be created | `os.Stat` poll |
| `output:contains` | Command output must include a string | Capture stdout, substring match |
| `http:ok` | Wait for a service to respond on a port | HTTP GET poll |
| `atspi:present` | Wait for a UI element to appear (Phase 4) | AT-SPI query |
| `vision:confirms` | LLM vision confirms screen state (Phase 4) | grim + vision API call |

The last two condition types only apply to `execute:display` steps in Phase 4. They are defined now so the data model doesn't need to change later.

---

## System Manifest

The system manifest is a machine-readable description of the filesystem and service topology. It is used by the Arbiter and the `CommandValidator` to make deterministic, context-aware decisions about what a command will affect — without relying on the LLM to infer consequences.

**Purpose:**
- Gives the Arbiter structured metadata about paths and services (sensitivity, ownership, what is affected, whether PAM is required)
- Enables the `CommandValidator` to attach `ManifestContext` to any step that touches a known path
- Feeds the Adversarial Agent as additional context for risk assessment
- Is machine-readable, not just LLM context — the Arbiter runs deterministic rules against it

**Schema (`/var/lib/hyperi/manifest.json`):**
```yaml
paths:
  /etc/nginx/:
    owner: www-data
    description: "nginx web server configuration"
    affects: [web-serving]
    requires_pam: false
    requires_capability: execute:config
    sensitivity: medium   # low | medium | high | critical

  /home/user/:
    owner: user
    description: "human user home directory"
    requires_pam: true
    sensitivity: high

  /var/lib/hyperi/:
    owner: hyperi
    description: "agent state and session data"
    requires_pam: false
    sensitivity: medium

  /etc/sudoers.d/:
    owner: root
    description: "sudo policy — modification grants privilege escalation"
    requires_pam: true
    sensitivity: critical

services:
  nginx:
    depends_on: [network]
    affects: [web-serving]
    safe_to_restart: true
    restart_impact: "brief downtime on port 80/443"

  hyperi:
    depends_on: [network]
    affects: [agent-pipeline]
    safe_to_restart: true
    restart_impact: "active session lost"
```

**How it is generated and kept current:**

The manifest is auto-generated — it reflects the actual system state rather than being manually authored. This ensures it stays honest as the system evolves.

| Trigger | Mechanism | Scope |
|---|---|---|
| First boot | Full filesystem + service scan | All watched paths; all systemd units |
| Post-execution | Hook in executor after each step | Paths touched by the step's `Command` args |
| Async background | inotify watcher on sensitive paths | `/etc`, `/var`, `/opt`, user homes |
| Service startup | mtime-based reconciliation scan | Paths modified since last manifest update |

**inotify implementation notes:**
- Watch directories recursively; handle `IN_MOVED_TO` (atomic rename pattern used by apt, editors, etc.) not just `IN_MODIFY`
- Set `/proc/sys/fs/inotify/max_user_watches` to a safe value (e.g. 524288) in Phase 0 distro config
- inotify events are queued; if hyperi is down when a change happens the event is lost — the startup reconciliation scan (mtime diff) is the recovery path for this case
- The post-execution hook is the highest-priority trigger: the agent knows exactly which paths it touched, so it re-scans those immediately before the next step executes

**Tradeoffs documented:**
- inotify does not catch changes made from within Docker containers or other namespaces — out of scope for v1
- The manifest describes sensitivity and ownership but does not enforce it — enforcement remains with Linux DAC and the capability system
- Auto-generation means a first-boot scan has a one-time cost proportional to filesystem size; watch path list should be configurable to limit scope

---

## Graduated Autonomy Levels

Autonomy level controls **when the agent pauses and asks** vs **proceeds on its own judgment**. It does not control what can execute at all — that is the job of the OS permissions layer and the capability allowlist. Those two layers are enforced regardless of autonomy level.

**Storage:** Global default in `/var/lib/hyperi/config.json`. Overridable per foreground session at start time via `--autonomy <level>` flag. Background (scheduled) sessions are capped at the global default — they cannot override upward.

**Default on fresh install: Level 1.** The `config/allowlist.yaml` ships conservatively (read-oriented commands, no writes, no package installs). At level 1 the system is functional without being dangerous. The allowlist and default level are a paired safety instrument.

**Trust escalation: explicit grant only.** The system never increases autonomy level automatically. Automatic trust accumulation is a post-v1 concept.

| Level | Name | Arbiter prompt behavior |
|---|---|---|
| 0 | Observe only | All steps → `modified`; plan presented as suggestion; nothing executes |
| 1 | Execute approved | `block` → blocked; `high` severity or irreversible → `modified` (prompt); else → approved |
| 2 | Execute reversible | Reversible steps → approved without prompt; irreversible → `modified` |
| 3 | Execute bounded irreversible | Irreversible → approved after adversarial review; only `block` → blocked |
| 4 | Trusted autonomy | Only `block` flags produce blocked; everything else approved without prompt |

**Hard floors (not affected by autonomy level):**
- `block` verdict is always a hard block
- OS permissions and PAM requirements are always enforced
- Capabilities not on the allowlist can never run

**Audit trail:** `ArbiterVerdict` records the autonomy level at which it was evaluated. This is written to the plan doc and the JSONL audit log — if something was approved at level 3 that would have been flagged at level 1, that is visible in the audit trail.

**Level 4 note:** Available but only set by explicit user action. Appropriate for headless background maintenance. Requires thorough testing before any wider use.

---

## Rollback & Recovery

**v1 stance: explicit scope limitation.** Automated rollback is not implemented in v1. This is a deliberate decision, not an oversight.

**Why this is acceptable for v1:**
- The plan doc records every executed command with exact arguments and timestamps — it is a de facto undo reference. If manual reversal is needed, the user has the exact commands.
- OS permissions and PAM already gate the most dangerous irreversible actions (package installs, service writes, config writes outside `/var/lib/hyperi/`) behind human authentication — the hardest cases are already protected.
- Irreversible steps at autonomy levels 0–2 always require explicit user approval before executing. The user approves with full knowledge of what can't be undone.
- On partial plan failure, the plan doc's `## Execution` section shows exactly which steps completed — the user has full context for manual recovery.

**Known limitation:** Background sessions at autonomy level 3+ can execute irreversible steps without a human present. This is accepted for v1 with the documented expectation that level 3+ should only be used for tasks the user has thoroughly reviewed. It is not a silent risk — it is a documented tradeoff.

**What `reversible: bool` on `ActionStep` does in v1:**
- Drives Arbiter approval requirements: irreversible steps receive `modified` verdict at levels 1–2, requiring user approval
- Informs the Adversarial Agent's risk assessment
- Is recorded in the plan doc and audit trail

**Post-v1 path:** Planner-generated rollback steps (Position A) and executor-maintained undo stack (Position B) are both documented in `post-v1.md`, with a hybrid noted as the likely correct long-term approach. Neither is implemented until v1 has been run against real scenarios and the failure modes are understood.

---

## Event Bus

The event bus is the internal message channel that decouples producers (executor, scheduler, system monitors) from consumers (TUI shell, web UI, audit log). It is what makes the persistent shell feel like an OS rather than a REPL — the shell can receive messages it didn't ask for.

**Why it exists:**
Without an event bus, the TUI can only display responses to user input. There is no mechanism for the agent to proactively surface information — a completed background task, a condition-triggered alert, a scheduled check-in. The event bus is the solution to all three.

**Architecture:**
```
Producers                          Event Bus              Consumers
─────────────────────              ──────────             ─────────────────────
Executor (step results)    ──────> chan Event  ──────>    TUI render loop
Scheduler (task fired)     ──────>            ──────>    WebSocket push (web UI)
System monitor (alert)     ──────>            ──────>    Audit log (always)
Agent pipeline (status)    ──────>
```

A buffered `chan Event` in Go. The TUI's bubbletea `Update` loop reads from it via a `tea.Cmd` that wraps the channel. The WebSocket server has a goroutine that also reads from it and broadcasts to connected clients. The audit logger writes every event regardless of other consumers.

**Event types:**
```go
type EventKind string

const (
    EventStepStarted    EventKind = "step:started"
    EventStepCompleted  EventKind = "step:completed"
    EventStepFailed     EventKind = "step:failed"
    EventPlanCompleted  EventKind = "plan:completed"
    EventPlanFailed     EventKind = "plan:failed"
    EventScheduledFired EventKind = "scheduled:fired"
    EventAlertTriggered EventKind = "alert:triggered"
    EventApprovalNeeded EventKind = "approval:needed"   // arbiter "modified" verdict
    EventManifestUpdated EventKind = "manifest:updated"
)

type Event struct {
    Kind      EventKind
    SessionID string
    StepID    string    // empty for plan-level events
    Payload   any       // step result, alert message, approval request, etc.
    Timestamp time.Time
}
```

**Approval prompts flow through the event bus:**
When the Arbiter returns a `"modified"` verdict, an `EventApprovalNeeded` event is published. The TUI renders an inline approval prompt. The user's response (y/n) is sent back through a separate reply channel on the same `Event`. This keeps the approval flow decoupled from the pipeline — the pipeline pauses waiting on the reply channel, the TUI handles the interaction independently.

**Connections to other systems:**
- Critique 4 (TUI vs web UI): both are consumers of the same event bus — shared state without shared code
- Scheduled tasks: the scheduler publishes `EventScheduledFired` when a timer fires; the pipeline picks it up like any other intent
- `ReadyCondition` polling: each poll result can optionally publish a `EventStepStarted`/status update so the TUI shows live progress on long-running steps

---

## Scheduler

The scheduler is responsible for time-based and recurring agent actions. It is the implementation layer behind the `execute:schedule` capability.

**Two backends, different scopes:**

| Backend | Tool | Survives restart | Use case |
|---|---|---|---|
| OS-level | systemd timer | Yes | User-directed recurring tasks, health checks, anything that must persist across reboots |
| In-process | `robfig/cron` | No (unless persisted) | Agent-internal cadence: manifest re-scan, session cleanup, audit log rotation |

**`execute:schedule` capability scope format:**
- `execute:schedule:systemd:<name>` — create/enable/disable a systemd timer unit
- `execute:schedule:cron:<name>` — register an in-process cron entry

**How systemd scheduling works:**
Creating a scheduled task via `execute:schedule:systemd:<name>` is a two-step plan the agent generates:
1. `execute:config` — write `.timer` + `.service` unit files to `/etc/systemd/system/`
2. `execute:process` — `systemctl enable --now <name>.timer`

The `execute:schedule` capability is the user-facing abstraction. The executor decomposes it into those two underlying steps internally, so the user says "remind me every Sunday to check backups" and the agent handles the systemd plumbing.

**Scheduled session re-entry:**
When a systemd timer fires, it starts a new `hyperi session start` process with a pre-set intent (stored in the `.service` unit's `ExecStart`). This re-enters the full agent pipeline with that intent. The result is published to the event bus, which delivers it to any active TUI or web UI session.

**Check-in scenarios handled by the scheduler:**

| Scenario | Trigger | Mechanism | Delivery |
|---|---|---|---|
| Morning summary | systemd timer (daily) | New session with fixed intent | Event bus → TUI on next interaction |
| Condition alert ("disk > 90%") | In-process monitor + threshold check | `EventAlertTriggered` on event bus | TUI interrupt or WebSocket push |
| Long-running task completion | Executor step result | `EventPlanCompleted` on event bus | TUI interrupt (user may have walked away) |
| Scheduled health check | systemd timer | New session, result to event bus | TUI on next interaction or WebSocket |

---

## Display Architecture

**Compositor: sway (Phase 4 default)**
- Chosen for: Wayland-native, i3-compatible IPC, scriptable via `swaymsg`
- Kiosk configuration: Hyperi Shell fullscreen in workspace 1
- Agent summons apps into workspace 2+
- Modular: swap for wlroots custom compositor in later phases

**Screen capture: grim**
- Wayland-native screenshot tool
- Output fed back into the agent pipeline as `ReadyCondition` context and vision model input

**Input injection: ydotool**
- Wayland-compatible mouse/keyboard injection
- Requires `ydotoold` daemon running as a service
- Last resort only — see layered interaction model below

### Layered Interaction Model

The agent does not default to raw screen automation. It prefers the most reliable, deterministic interaction path available for a given app and falls back progressively:

```
1. CLI / API          — preferred; deterministic, auditable, no screen dependency
2. AT-SPI             — for native Linux GUI apps (GTK, Qt); semantic element access
3. Vision model       — for Electron apps, browser content, anything AT-SPI can't reach
4. ydotool (raw)      — last resort only; coordinate-based, layout-dependent
```

**Layer 1 — CLI/API:**
The executor checks whether the target app has a CLI or IPC interface before attempting any GUI automation. Most Linux apps do (`nmcli`, `git`, `systemctl`, app-specific sockets). This is always preferred.

**Layer 2 — AT-SPI (Accessibility Tree):**
Linux's accessibility API. GTK and Qt apps expose a semantic tree of UI elements — labels, buttons, text fields, their states. The agent can query "find button labeled Save" and get back its position and state without a screenshot. Fast, deterministic, no API call. Limitation: Electron apps have partial/broken AT-SPI support; web content in browsers is not exposed.

**Layer 3 — Vision model:**
When AT-SPI is unavailable or incomplete, the agent takes a `grim` screenshot, encodes it, and sends it to the LLM vision API with a structured prompt ("identify the element labeled X and return its approximate screen coordinates"). This covers Electron apps and browser content. Costs: extra API latency per interaction, requires network, screenshot may contain sensitive content.

**Layer 4 — ydotool:**
Raw coordinate injection. Used only when no other layer is viable. The agent must have confirmed screen state via vision or AT-SPI before issuing coordinate-based input — it never clicks at a hardcoded position without a prior observation step.

**Feedback loop — `ReadyCondition` for display steps:**
Every `execute:display` step that launches or interacts with a UI element must have a `ReadyCondition`. The executor does not proceed to the next step until the condition is met (or times out). Condition types used in display context: `atspi:present`, `vision:confirms`, or `output:contains` for CLI-accessible state.

**Tradeoffs documented:**
- AT-SPI requires apps to be compiled with accessibility support enabled — most distro packages are, but not all
- Vision model interaction requires network access to Anthropic API — display automation degrades gracefully to AT-SPI-only when offline
- Layer 3 and 4 are Phase 4+ scope; Layer 1 and 2 foundations should be established first

---

## Repo Structure

```
hyperios/
|- cmd/
|   `- hyperi/main.go           # Entry point (cobra CLI -> agent pipeline)
|- internal/
|   |- agents/                   # Intent, Planner, Adversarial agents
|   |- arbiter/                  # Deterministic Policy Arbiter (autonomy-level aware)
|   |- audit/                    # JSONL audit logger
|   |- bus/                      # Event bus (chan Event, producers, consumers)
|   |- capability/               # Registry, Enforcer, Matcher, CommandValidator
|   |- config/                   # Global config (autonomy level, watch paths)
|   |- executor/
|   |   |- interface.go          # Executor interface
|   |   |- executor.go           # Factory + Stub (dry-run)
|   |   |- local.go              # Linux-native executor (with ReadyCondition poll loop)
|   |   `- container.go          # Docker container executor
|   |- llm/                      # Anthropic SDK wrapper + Completer interface
|   |- manifest/                 # System manifest: scanner, inotify watcher, reader
|   |- plan/                     # Plan doc writer, resume parser, re-plan loop
|   |- scheduler/                # execute:schedule backend (systemd + robfig/cron)
|   |- session/                  # Session state + persistence
|   |- shell/                    # TUI shell (Phase 2)
|   |- types/                    # Shared data structures
|   |- ui/
|   |   |- server.go             # HTTP + WebSocket server (event bus consumer)
|   |   |- capture.go            # grim screen capture
|   |   |- controller.go         # ydotool input injection
|   |   |- atspi.go              # AT-SPI accessibility tree queries (Phase 4)
|   |   `- window.go             # swaymsg window management
|   `- voice/                    # STT/TTS pipeline (Phase 3)
|- Vagrantfile                    # Dev VM: Ubuntu 24.04, headless sway, cloud-init provisioned
|- distro/
|   |- cloud-init/               # First-boot provisioning (used by Vagrant + real installs)
|   |- dev/                      # Dev environment scripts (headless sway launcher, provision helper)
|   |- preseed/                  # Bare metal unattended install
|   |- systemd/                  # hyperi.service unit
|   |- sway/                     # Compositor config (kiosk mode)
|   `- build/
|       |- build-iso.sh          # Phase 5: distributable ISO (live-build)
|       `- build-image.sh        # Phase 3-4: QEMU disk image for pre-ISO testing
|- config/
|   `- allowlist.yaml            # Capability allowlist (extended for Linux)
|- var/lib/hyperi/               # Runtime data (on-device only, not in repo)
|   `- manifest.json             # Auto-generated system manifest (paths + services)
|- ui/frontend/                  # React SPA — carry-over scaffolding, not active v1 development
|- docs/
|   `- plan.md                   # This document
|- go.mod
|- justfile
`- AGENTS.md
```

---

## What Carries Over from Uplink

| Package | Status |
|---|---|
| `internal/agents/` | Carried over; system prompts updated for HyperiOS context |
| `internal/arbiter/` | Carried over as-is |
| `internal/audit/` | Carried over; paths updated to /var/log/hyperi/ |
| `internal/capability/` | Carried over; extended with 5 new Linux capability types |
| `internal/llm/` | Carried over as-is |
| `internal/session/` | Carried over; paths updated to /var/lib/hyperi/ |
| `internal/types/` | Carried over as-is |
| `internal/executor/container.go` | Carried over |
| `internal/executor/local.go` | Rewritten for Linux |
| `internal/ui/server.go` | Carried over; Win32 deps removed |
| `internal/ui/capture.go` | Rewritten: grim |
| `internal/ui/controller.go` | Rewritten: ydotool |
| `internal/ui/window.go` | Rewritten: swaymsg |
| `windows/` | Dropped entirely |

---

## Known Bugs (Inherited from Uplink — all scheduled for Phase 1A)

1. `registry.Check()` glob patterns — `filepath.Match` does not support `**`; fix with proper glob library or recursive walk
2. `ErrCapabilityNotGranted` sentinel — use `errors.As` not `errors.Is` at all call sites
3. `session.Progress()` overcounts — deduplicate on write or check before append in `MarkCompleted()`
4. `executeNetwork()` stub — implement: `Command ["GET|POST", "<url>", "<body>"]`; `net/http`; see Blocker 4 in critiques.md
5. `executeConfig()` stub — implement: `Command ["<path>", "<content>"]`; `os.WriteFile`; see Blocker 4 in critiques.md

---

## Stack

- **Language:** Go 1.25.6
- **CLI:** spf13/cobra
- **LLM:** Anthropic SDK (claude-sonnet-4-6)
- **TUI Shell (Phase 2):** charmbracelet/bubbletea
- **In-process scheduler:** robfig/cron
- **Compositor:** sway (Wayland)
- **Screen capture:** grim
- **Accessibility:** AT-SPI (Phase 4)
- **Input injection:** ydotool (Phase 4, last resort)
- **Package management:** apt, flatpak, snap
- **Service management:** systemd (incl. systemd timers for scheduled tasks)
- **Network:** nmcli
- **Voice STT (Phase 3):** whisper.cpp
- **Voice TTS (Phase 3):** piper

## Running

```bash
go build -o hyperi ./cmd/hyperi    # Build binary
go test ./...                       # Unit tests
go test -tags integration ./...     # Integration tests (requires API key)
go vet ./...                        # Lint
just build                          # Cross-compile for Linux amd64/arm64
```

**Dev environment (VM):**
```bash
just dev              # Spin up Vagrant VM (Ubuntu 24.04, headless sway, cloud-init provisioned)
just dev-ssh          # SSH into dev VM
just dev-destroy      # Tear down and recreate clean
```

**Real machine testing (no ISO builder required):**
1. Flash stock Ubuntu 24.04 Server ISO to USB and install
2. Copy `cloud-init/user-data.yaml` and run `sudo cloud-init init`
3. Copy built `hyperi` binary to `/usr/local/bin/hyperi`
4. `sudo systemctl enable --now hyperi`

**Distribution builds:**
```bash
just build-image      # QEMU disk image (Phase 3-4 pre-ISO testing)
just build-iso        # Distributable ISO via live-build (Phase 5)
```

**Target platforms:** Linux amd64 (primary), Linux arm64 (Raspberry Pi / ARM laptops)
**Not supported:** Windows, macOS (this is a distro, not a cross-platform app)

---

## Hardware-Aware Local Model Integration

### Motivation

HyperiOS should maximize use of available host hardware. If a user installs the OS on a machine with capable GPUs, significant RAM, or ample disk, the system should automatically detect that, recommend appropriate local models, and offer to provision them — reducing cloud API costs and remote agent calls. A machine with two RTX 4090s should not be routing all inference to Anthropic's API.

---

### Architecture

```
First Boot / hyperi setup
        |
[Hardware Probe] internal/hardware/probe.go
  - /proc/cpuinfo     → CPU cores, architecture
  - /proc/meminfo     → system RAM
  - lspci / nvidia-smi / rocm-smi → GPU vendor, VRAM
  - df /              → disk free space
        |
[Model Selector] internal/hardware/selector.go
  Deterministic logic (no LLM):
  - Given hardware profile, select best-fit model config
  - Embedded model catalog (VRAM requirements, RAM, disk)
        |
[Setup Wizard] (TUI — existing bubbletea shell)
  - "We detected 2× RTX 4090 (48 GB VRAM combined).
     We recommend installing llama3:70b via Ollama.
     This will use ~42 GB VRAM and reduce cloud API costs.
     Install? [y/n]"
        |
[Provisioner] → agent pipeline handles install via existing capability system
  execute:package → install Ollama
  execute:process → systemctl enable ollama
  execute:shell   → ollama pull <model>
  (all gated through Arbiter + CommandValidator as normal)
        |
[Provider Router] internal/llm/router.go
  Routes Complete() calls:
  - local Ollama if running + model loaded
  - fallback to Anthropic if local unavailable
  Config: preferred_provider, fallback_provider
        |
[Config update] /var/lib/hyperi/config.json
  + preferred_provider: "ollama" | "anthropic"
  + local_model: "llama3:70b"
  + ollama_host: "http://localhost:11434"
  + model_fallback: true
```

---

### New Packages

| Package | Purpose |
|---|---|
| `internal/hardware/probe.go` | Reads `/proc/cpuinfo`, `/proc/meminfo`, runs `lspci`, `nvidia-smi`, `rocm-smi` — returns a `HardwareProfile` struct |
| `internal/hardware/selector.go` | Embedded model catalog + deterministic selection logic; takes `HardwareProfile`, returns ranked `[]ModelRecommendation` |
| `internal/hardware/catalog.go` | Static model catalog: model name, provider, VRAM req, RAM req, disk req, Ollama tag, performance tier |
| `internal/llm/router.go` | `Router` implements `Completer`; wraps Ollama + Anthropic clients; handles fallback logic |
| `internal/llm/ollama.go` | Ollama `Completer` implementation (HTTP to `localhost:11434/api/chat`) |

---

### Config Additions (`internal/config/config.go`)

```go
// Add to Config struct:
PreferredProvider  string // "anthropic" | "ollama" | "auto"
LocalModel         string // e.g. "llama3:70b", "mistral:7b"
OllamaHost         string // default: "http://localhost:11434"
ModelFallback      bool   // fall back to Anthropic if local fails
HardwareProbed     bool   // whether probe has run on this install
```

---

### Model Catalog (Embedded in Binary)

The selector maps hardware tiers to recommended models:

| Tier | Criteria | Recommended Model | Notes |
|---|---|---|---|
| Flagship GPU | ≥24 GB VRAM (single or combined) | llama3.1:70b or qwen2.5:72b | Full-quality local inference |
| Mid GPU | 8–24 GB VRAM | llama3.2:8b or mistral:7b-instruct | Good local quality |
| Low GPU / CPU | <8 GB VRAM or no GPU | mistral:7b-instruct-q4 | Acceptable for simple tasks |
| RAM-only fallback | ≥32 GB RAM, no GPU | qwen2.5:7b-q4 | Last resort before Anthropic |
| Underpowered | <16 GB RAM | (none) | Recommend Anthropic only |

Multi-GPU: sum VRAM across cards (Ollama supports tensor parallelism ≥ v0.1.29). Disk space is checked before recommendation — models range 4–40 GB.

---

### First-Boot Flow

The probe and wizard run once: when `HardwareProbed == false` in config (fresh install). The existing TUI shell startup (`shell.go`) checks this flag and initiates the wizard before the first prompt. After setup, `HardwareProbed = true` and the wizard is skipped on all subsequent starts.

The actual installation is routed through the existing agent pipeline — the wizard generates an intent (`"install Ollama and pull model llama3.1:70b"`) and it runs through Intent → Planner → Adversarial → Arbiter → Executor normally. The install is audited, arbiter-gated, and capability-checked like any other action.

---

### Allowlist Additions (`config/allowlist.yaml`)

```yaml
execute:shell:
  - nvidia-smi
  - rocm-smi
  - lspci
  - ollama

execute:package:
  - apt:ollama

network:outbound:
  - ollama.ai
  - registry.ollama.ai

execute:process:
  - systemctl:start:ollama
  - systemctl:enable:ollama
  - systemctl:stop:ollama
```

---

### What This Does NOT Change

- The `Completer` interface is unchanged — agents call `client.Complete()` exactly as today
- The Arbiter, CommandValidator, and capability system are unchanged
- Anthropic remains the default until a local model is confirmed healthy
- The audit trail, plan docs, and session system are unaffected

---

### Build Phases

#### Phase 1D — Hardware Probe + Model Catalog
- `internal/hardware/probe.go` + `selector.go` + `catalog.go`
- Surfaces recommendations to the user on first run; no install yet
- Adds `HardwareProbed` and hardware fields to `Config`
- Tests: probe against `/proc` fixtures, selector unit tests against hardware profiles

#### Phase 1E — Local Model Provider
- `internal/llm/ollama.go` + `internal/llm/router.go`
- Config fields for `preferred_provider`, `local_model`, `ollama_host`, `model_fallback`
- Router wired into shell startup in place of direct `llm.NewClient()`
- Tests: mock Ollama server, router fallback behavior

#### Phase 1F — First-Boot Setup Wizard
- TUI wizard in `internal/shell/` triggered by `!cfg.HardwareProbed`
- Uses agent pipeline for the actual install (no new executor code needed)
- Updates config and sets `HardwareProbed = true` on completion
- Post-install latency/quality check before switching away from Anthropic

---

### Open Questions

1. **Ollama vs direct llama.cpp:** Ollama is preferred for v1 — single binary, REST API, manages model files, matches the "agent installs infrastructure" philosophy. Direct llama.cpp gives more quantization control but adds complexity.
2. **Multi-GPU VRAM aggregation:** Sum VRAM across cards for tensor-parallel models; note Ollama ≥ 0.1.29 requirement in catalog constraints.
3. **Model quality gate:** After install, run a benchmark prompt and measure latency before switching away from Anthropic. A simple echo test with latency threshold is sufficient for v1.
4. **Cost tracking (post-v1):** Track Anthropic API spend over time and use it to drive "nudge to local model" recommendations. Natural Phase 2 addition once the router exists.
