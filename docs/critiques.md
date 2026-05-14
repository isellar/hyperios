# HyperiOS — Architecture Critiques & Open Questions

Generated during initial plan review. Each critique is a design decision that needs resolution before or during task breakdown. Work through these one by one, then update `plan.md` accordingly.

---

## Critique 1: The Agent Pipeline is Stateless — No Feedback Loop

**Current state:**
Every `session start` invokes Intent → Planner → Adversarial → Arbiter → Execute once and exits. There is no feedback loop. If a step fails mid-execution, the agent doesn't know. If the user interrupts mid-plan, there is no mechanism to resume intelligently. `session.ToGoalGraph()` exists for resume but isn't wired to re-enter the pipeline in a meaningful way.

**Why it matters:**
This is the difference between a CLI tool and an actual agent operating system. A real agent loop must:
- Know what executed and what didn't
- Adapt when steps fail (re-plan, not crash)
- Handle partial completion and resume correctly
- Survive crashes and unexpected shutdowns without losing state

**Decisions:**

**The plan document is the source of truth.**
Plans are written to disk immediately and updated in real time as the pipeline progresses. The plan document — not in-memory session state — is the authoritative record of a task. A crash is just an interruption; on restart the executor reads the plan doc and knows exactly where things stand without any LLM call.

Plan documents live at `/var/lib/hyperi/plans/<session-id>.md`. The session JSON at `/var/lib/hyperi/sessions/<session-id>.json` is a thin index: session ID, intent summary, status, timestamps, and a pointer to the plan doc. All substantive content is in the plan doc.

**All pipeline stages write to the plan doc.**
Pipeline stages are sequential for a given task — no concurrent writes. Each stage appends its output to the plan doc in order:
1. **Intent Agent** — writes the goal graph (structured, human-readable)
2. **Planner** — writes the full action plan with all steps
3. **Adversarial Agent** — writes the risk report
4. **Arbiter** — writes its verdict inline with each step
5. **Executor** — annotates each step with exact execution results as they complete

The plan doc is append-only during execution. Steps are never edited — only annotated. The full history of what was planned, what was risked, what was approved, and what actually happened is preserved in one file.

**`on_failure` is specified per step in the plan.**
The Planner decides failure handling when it generates the plan — not the executor. Each `ActionStep` has an `on_failure` field:

| Value | Behavior |
|---|---|
| `halt` | Stop the session immediately; surface full failure context to user |
| `retry` | Re-execute the same command up to `max_retries` times with optional backoff; then `halt` |
| `replan` | Trigger a re-plan pass with the failure context; counts against re-plan budget |
| `skip` | Mark step as skipped, continue with remaining steps; used for non-critical steps |

`retry` requires `max_retries` (int) and `retry_backoff_seconds` (int) alongside it in the step definition. The Planner sets these based on the nature of the step — a network call might retry 3 times with 5s backoff; a package install with a known flaky mirror might retry once.

**Re-plan budget: 3 total attempts (2 re-plans).**
- Attempt 1: original plan
- Attempt 2: first re-plan — automatic on `replan` failure policy; Planner receives full failure context from plan doc
- Attempt 3: second re-plan — requires user confirmation via `EventApprovalNeeded` before proceeding
- After 3 attempts: session halts; full plan doc presented to user

**Re-plans append to the same plan doc.**
A re-plan does not create a new file. It appends a `## Re-plan N` section to the existing plan doc, preserving the full history above it. The Planner is given the prior plan sections and execution results as context when generating the re-plan. This gives a clean, single-file history of the entire task regardless of how many attempts it took.

**Adversarial Agent on re-plans — conditional.**
Re-running the full Adversarial Agent on every re-plan is expensive. Policy:
- Re-plan introduces **no new capability types and no new paths** → skip Adversarial Agent, proceed directly to Arbiter
- Re-plan introduces **new capability types or new filesystem paths** → run Adversarial Agent on the new steps only

This is evaluated deterministically by diffing the new plan's steps against the original — no LLM needed for the decision itself.

**Plan document structure:**
```markdown
# Task: <intent summary>
Session: <session-id>
Status: in-progress | completed | failed | halted
Created: <timestamp>
Updated: <timestamp>

## Intent
<goal graph output from Intent Agent>

## Plan
<action steps from Planner, with on_failure, ready_condition, etc.>

## Risk Report
<adversarial agent output>

## Execution

### Step 1: <description>
Verdict: approved
Started: <timestamp>
Command: grep -r "password" /etc
Result: success
Duration: 142ms
Output:
  <exact stdout>

### Step 2: <description>
Verdict: modified (requires approval)
Approval: granted by user at <timestamp>
Started: <timestamp>
Command: apt install nginx
Result: failure
Exit code: 1
Duration: 3201ms
Error:
  E: Unable to fetch some archives, maybe run apt-get update or try with --fix-missing?
on_failure: replan

## Re-plan 1
Triggered by: Step 2 failure
User confirmation: not required (attempt 2 of 3)

### Revised Plan
<new action steps>

### Risk Report (Re-plan 1)
skipped — no new capability types or paths introduced

### Execution (Re-plan 1)

### Step 1: <description>
...
```

**Resume behavior:**
On restart after a crash, the executor scans plan docs with `status: in-progress`. For each:
1. Parse completed steps from the `## Execution` section (machine-readable: `Result: success|failure|skipped`)
2. Identify the last completed step and the next pending step
3. Resume execution from the next pending step — no LLM call required for resume unless a re-plan is needed
4. Update `status` and `Updated` in the plan doc frontmatter

**What changes in code:**
- `types.go` — add `OnFailure`, `MaxRetries`, `RetryBackoffSeconds` to `ActionStep`
- `internal/plan/` — new package; `PlanDoc` struct; writer methods for each pipeline stage; resume parser
- `internal/session/state.go` — slim down to thin index; remove execution state that moves to plan doc
- `internal/agents/planner.go` — updated system prompt: must specify `on_failure` per step; re-plan prompt variant that receives prior plan context
- `internal/agents/adversarial.go` — add targeted re-plan variant that only evaluates new steps
- `internal/executor/local.go` — write step results to plan doc in real time; implement retry loop; trigger re-plan via event bus
- `cmd/hyperi/main.go` — re-plan loop with attempt counter; resume logic on startup

**Status:** [x] Resolved

---

## Critique 2: `execute:shell` is Broken by Design — Command Extraction from NL

**Current state:**
`local.go`'s shell executor does keyword extraction from the step's natural-language description string to determine what command to run. The actual command executed is not known at plan time, not validated by the arbiter, and not auditable in a meaningful way.

**Why it matters:**
This is both a correctness and security issue. The Planner writes "use grep to find exposed secrets in /etc" and the executor extracts `grep` and constructs something. The command the arbiter approved is not the command that runs. The audit trail is meaningless for these steps.

**Decision: Option A — Add `Command []string` to `ActionStep`**

The Planner LLM emits the literal command as a string slice (e.g. `["grep", "-r", "password", "/etc"]`). The Arbiter validates against the literal command. The executor runs it directly via `exec.Command(args[0], args[1:]...)` — no shell interpolation, no NL parsing.

*Tradeoff accepted:* The Planner system prompt becomes more constrained — it must emit valid, executable commands in addition to natural-language descriptions. JSON schema enforcement on the LLM output matters more. Malformed `Command` fields need to be caught before reaching the executor.

**Pre-validation layer — `internal/capability/validator.go`**

A `CommandValidator` runs deterministic checks *before* the Arbiter sees the step. This is cheap, has no LLM cost, and catches structural problems early. It lives in `internal/capability/` alongside the registry and enforcer — it is part of the capability layer, not a peer to it.

Checks performed in order:
1. **Structural** — `Command` is non-empty; `args[0]` is an absolute path or resolvable binary; no shell metacharacters (`|`, `;`, `&&`, `$()`, backticks, redirects) in any argument
2. **Allowlist membership** — `args[0]` (the binary) matches an entry in `config/allowlist.yaml` for the declared capability type
3. **Path check** — any argument that looks like a filesystem path is validated against the system manifest (see System Manifest section in plan.md); if the path has `requires_pam: true` and no PAM token is present in session context, return a structured `ValidationError` with reason

*Tradeoff:* The structural check (no shell metacharacters) is a heuristic. A sufficiently crafted argument could bypass it. This is a known limitation — it reduces the attack surface but is not a complete sandbox. The OS user permissions and `exec.Command` (no shell) are the real defense; the validator is defense-in-depth.

**`execute:shell` scope format**

The `Capability.Scope` field for shell steps becomes the binary name only (e.g. `grep`). The full command is in `Command []string`. This means the allowlist entry `execute:shell: [grep, find, ls]` still works as-is — the validator checks `args[0]` against that list. The Arbiter receives the full `Command` slice as context for its verdict.

**System Manifest integration**

Any `Command` argument that is a filesystem path is looked up in the system manifest before reaching the Arbiter. If the manifest entry for that path has fields like `requires_pam`, `sensitivity`, or `affects`, those are attached to the step as `ManifestContext` and passed to the Arbiter. The Arbiter can then make a deterministic verdict based on structured path metadata rather than inferring consequences from a natural-language description.

**What changes in code:**
- `types.go` — add `Command []string` to `ActionStep`
- `internal/capability/validator.go` — new file; `CommandValidator` struct with `Validate(step) ValidationResult`
- `internal/agents/planner.go` — system prompt updated to require `command` field in every step
- `internal/arbiter/arbiter.go` — receives and logs `Command`; can use path manifest context for hard blocks
- `internal/executor/local.go` — all capability handlers use `step.Command` directly; NL keyword extraction removed
- `internal/executor/container.go` — same
- `cmd/hyperi/main.go` — validator runs between Arbiter and Executor in the pipeline

**Status:** [x] Resolved

---

## Critique 3: Phase 4 Display Management — The Agent is Blind

**Current state:**
The plan has `execute:display` for launching/arranging apps and `grim` for screenshots, but there is no feedback loop between them. The agent can open a browser but cannot know what loaded, whether a window is ready, or what to interact with next. `ydotool` injects raw mouse coordinates with no semantic understanding of screen state.

**Why it matters:**
Without screen understanding, Phase 4 is brittle scripting. The agent issues "click at (400, 300)" with no knowledge of what's there. Any dynamic layout, loading state, or unexpected dialog breaks it completely.

**Decision: Layered interaction model with mandatory `ReadyCondition` on all display steps.**

The agent does not default to raw screen automation. It uses the most reliable path available and falls back progressively:

1. **CLI/API** — preferred always; deterministic, no screen dependency
2. **AT-SPI** — for native Linux GUI apps (GTK/Qt); semantic element access without screenshots
3. **Vision model** — grim screenshot → LLM vision API; covers Electron/browser content that AT-SPI can't reach
4. **ydotool** — last resort only; never used without a prior observation step confirming screen state

Every `execute:display` step must have a `ReadyCondition` (`atspi:present` or `vision:confirms`). The executor does not advance until the condition is met or times out.

**Tradeoffs accepted:**
- Vision model interaction requires network and adds API latency per interaction; display automation degrades gracefully to AT-SPI-only when offline
- AT-SPI requires apps compiled with accessibility support — most distro packages qualify, but not all
- Layers 3 and 4 are Phase 4 scope; Layers 1 and 2 foundations established first
- The agent will frequently prefer CLI paths even for "GUI" tasks — this is intentional

**Cross-reference:** `ReadyCondition` on `ActionStep` (see ActionStep Data Model section) is the mechanism that makes this work for display steps and also for long-running steps generally.

**Status:** [x] Resolved

---

## Critique 4: Two Competing Interfaces — TUI vs Web UI, No Reconciliation

**Current state:**
Phase 2 is a bubbletea TUI shell. `ui/server.go` is a WebSocket server for a React SPA. These are different interaction paradigms, neither is complete, and the plan doesn't specify when one is used vs the other, whether they share session state, or which is primary. The web UI code is carry-over from the previous iteration of this project (which was primarily web-based) and is not active v1 development.

**Why it matters:**
Session management, streaming output, approval prompts, and plan display all need to know where to render. Building both in parallel without a clear ownership model will produce two half-finished interfaces that don't interoperate.

**Decisions:**

**TUI is the primary interface for v1.** Works without a display server, over SSH, headless, from first boot. The on-device interface for all of Phase 2. The web UI is not v1 scope.

**Web UI is deferred to a future phase — remote access only.** When it arrives, its purpose is remote management: access a running hyperi session from a browser on another device. It is not a richer local display. The existing `ui/server.go` and `ui/frontend/` are scaffolding from a prior iteration; treat them as placeholders until the web UI phase is explicitly planned.

**Web UI tech note (deferred decision):** When the web UI phase arrives, reconsider React vs a lighter alternative (HTMX or Go template server). The use case is essentially a terminal emulator with richer rendering — React's overhead may not be justified. Noted here so the decision isn't made by inertia.

**Both interfaces share state via the event bus.** The event bus (`internal/bus/`) is the source of truth for session state from the perspective of both consumers. Neither interface holds its own session state — they both read from the same event stream. This means the web UI, when it exists, is naturally consistent with the TUI without any additional synchronization.

**Approval prompts — first reply wins.** `EventApprovalNeeded` is published to the bus. Whichever interface renders it first and receives a user response closes the reply channel. Subsequent responses from any interface are discarded (no-op). This is safe because the reply channel is a `chan bool` that is closed after the first write; subsequent sends to a closed channel are caught and ignored.

**Single-user for v1.** One foreground session at a time. Concurrency is handled via a session mode distinction, not a lock:

- **Foreground session** — started by user input in the TUI; renders inline; holds the interactive prompt
- **Background session** — started by the scheduler or a systemd timer; no TUI interaction; publishes all events to the bus with a `background: true` flag; results are buffered and surface in the TUI when it is idle or the foreground session ends

A lock file at `/var/lib/hyperi/session.lock` records the active foreground session PID. If a second foreground attempt is made while one is running (e.g. user opens a second terminal), it is rejected with a clear message. Scheduled/system-initiated sessions always run as background sessions and never attempt to acquire the foreground lock.

**Multi-user is explicitly post-v1.** The OS permission model (`hyperi` user, per-user home directories, PAM) is structured such that scoping sessions to individual OS users is a feasible future extension — each user would have their own session state, capability grants, and autonomy level. This is not designed now but not designed out.

**Tradeoffs accepted:**
- Background session events are buffered on the bus; if the TUI is not running when a background session completes (e.g. user is logged out), those events are written to the audit log but not displayed until next login — acceptable for v1
- Web UI deferral means remote access is not possible in v1; SSH into the TUI is the workaround
- React tech choice left open until web UI phase is actually planned

**Status:** [x] Resolved

---

## Critique 5: Graduated Autonomy Levels Defined but Not Implemented

**Current state:**
The 5-level autonomy table exists in `plan.md` but there is no field on `Session`, no input to the Arbiter, and no code that changes executor behavior based on level. All sessions currently run at the same effective level (nothing executes without `--execute` flag).

**Why it matters:**
The autonomy model is a core UX and safety primitive. Without it, the system has no way to express "I trust this agent to act on X without asking, but not Y." The capability system and arbiter are doing all the work but have no concept of trust level.

**Decision: Option C — global default with per-session override.**

A global default level stored in `/var/lib/hyperi/config.json`. Any foreground session can override it downward or upward at start time. The session JSON records the level it ran at. Background (scheduled) sessions are capped at the global default — they cannot override upward, preventing privilege escalation through the scheduler.

**What autonomy level actually controls:**
Autonomy level controls *when the agent pauses and asks* vs *proceeds on its own judgment*. It does not control what can execute at all — that is the job of the OS permissions layer and the capability allowlist. The autonomy level is the soft layer above those two hard layers.

Revised behavior table:

| Level | Name | Arbiter prompt behavior |
|---|---|---|
| 0 | Observe only | All steps → `modified`; nothing executes without explicit user approval |
| 1 | Execute approved | Current arbiter logic; `block` → blocked, `high`/irreversible → modified, else → approved |
| 2 | Execute reversible | Reversible steps → approved without prompt; irreversible → modified |
| 3 | Execute bounded irreversible | Irreversible steps → approved after adversarial review; only `block` flags → blocked |
| 4 | Trusted autonomy | Only `block` flags produce `blocked`; everything else → approved without prompt |

`block` verdict is always a hard block regardless of autonomy level. OS permissions and PAM requirements are always enforced regardless of autonomy level. These are below the autonomy layer.

**Default on fresh install: Level 1.**
The fresh install allowlist (`config/allowlist.yaml`) is conservative by design — read-oriented shell commands, `systemctl status` only, no config writes, no package installs. At level 1, this means the agent is functional out of the box without being dangerous. The allowlist and the default autonomy level are a paired safety instrument — the allowlist defines what can ever run; level 1 defines that modified verdicts still require user approval.

**Trust escalation: explicit grant only (v1).**
The system never increases autonomy level automatically. The user sets it explicitly. Automatic trust accumulation based on successful execution history is a post-v1 concept — designing it requires a track record to base it on and clear criteria for what counts as "earned." Noted as a future direction, not designed now.

**Level 4 is available but gated.**
Level 4 (no prompts for anything arbiter-approves) is in the system. It is only set by explicit user action. It is appropriate for headless background maintenance contexts where human-in-the-loop is not practical. Requires thorough testing before use in any multi-user or production context.

**Arbiter changes:**
- Arbiter receives `autonomyLevel int` as input alongside the plan
- Verdict logic branches on level as described in the table above
- `ArbiterVerdict` gains an `Autonomy int` field — the level at which the verdict was evaluated, recorded in the plan doc and audit trail
- Level 0 special case: the executor does not run at all; the plan is presented as a suggestion only

**`config.json` schema (relevant fields):**
```json
{
  "autonomy_level": 1,
  "autonomy_updated_at": "<timestamp>",
  "autonomy_updated_by": "user"
}
```

**Session JSON additions:**
```json
{
  "autonomy_level": 1,
  "autonomy_override": false
}
```
`autonomy_override: true` when the session was started with an explicit level different from the global default. Background sessions always have `autonomy_override: false` and are capped at global default.

**What changes in code:**
- `internal/config/` — new package; `Config` struct; read/write `/var/lib/hyperi/config.json`; `hyperi config set autonomy-level <n>` CLI command
- `internal/arbiter/arbiter.go` — accept `autonomyLevel int`; branch verdict logic per level
- `internal/types/types.go` — add `Autonomy int` to `ArbiterVerdict`
- `internal/session/state.go` — add `AutonomyLevel int`, `AutonomyOverride bool` to session
- `cmd/hyperi/main.go` — load config on startup; pass autonomy level to arbiter; enforce background session cap; `--autonomy` flag for per-session override
- `config/allowlist.yaml` — remains the hard capability boundary; documented as paired safety instrument with default level 1

**Status:** [x] Resolved

---

## Critique 6: No Recovery, Rollback, or Undo Semantics

**Current state:**
`ActionStep` has a `reversible: bool` field and the Arbiter uses it to flag steps for user approval, but there is no rollback mechanism. Partial execution leaves the system in an unknown state. There is no `RollbackStep`, no undo log, and no recovery path.

**Why it matters:**
For an OS-level agent running `apt install`, `systemctl restart`, and config file writes, partial failure is not theoretical. An interrupted plan that has already restarted a service or written a config file needs a defined recovery behavior.

**Decision: Position C for v1 — explicit scope limitation with plan doc as undo reference.**

Two prior decisions significantly reduce the severity of this problem:
- The plan doc already records every executed command with exact arguments and timestamps — it is a de facto undo log even without automated rollback
- OS permissions and PAM already gate the most dangerous irreversible actions (package installs, service writes, config writes outside `/var/lib/hyperi/`) behind human authentication

**v1 rollback model:**
- Irreversible steps at autonomy levels 0–2 always require user approval before executing — the user is the rollback mechanism for these
- The plan doc records exactly what ran; if manual reversal is needed, the user has the exact commands
- On partial plan failure, the plan doc's `## Execution` section shows precisely which steps completed and which didn't — the user has full context for manual recovery
- Background sessions at autonomy level 3+ can run irreversible steps without a human present; this is a documented limitation — level 3+ background sessions should only be used for tasks the user has thoroughly reviewed

**Known gap:** The "user is the rollback mechanism" breaks down for unattended background sessions at higher autonomy levels. This is acceptable for v1 given that level 3+ requires explicit user configuration and the OS permissions floor still applies. Documented and tracked.

**Post-v1 path:** See `post-v1.md` — Position A (Planner-generated rollback steps) and Position B (executor-maintained undo stack) are both documented as future options, with a hybrid noted as the likely correct approach. A handles capability types where the logical inverse is well-defined (packages, services). B handles capability types where the actual prior state must be captured (config file writes). Neither is implemented until the v1 model has been run against real scenarios and the gaps are understood.

**No code changes required for v1** beyond what is already planned:
- `reversible: bool` on `ActionStep` remains and continues to drive Arbiter approval requirements
- Plan doc execution records provide the undo reference
- Audit trail provides the secondary record

**Status:** [x] Resolved

---

## Critique 7: ISO Build is Phase 5, But Integration Testing Has No Path

**Current state:**
`cloud-init`, `preseed`, `systemd` unit, and sway config are all written. The ISO build is Phase 5. But the distro configs reference a binary path that doesn't exist yet, and there is no way to boot the full stack even in a VM for development testing. There is a gap between "code compiles" and "system actually works as a distro."

**Why it matters:**
Integration bugs (sway not starting, hyperi.service failing, cloud-init not provisioning correctly) will only be caught when building the ISO in Phase 5 — far too late. Without a local dev environment that approximates the real distro, every phase is developed and tested in isolation.

**Decision: Vagrantfile as the dev environment, built in Phase 0. Real machine testing via cloud-init on stock Ubuntu ISO before Phase 5.**

**Why Vagrant and not Docker:**
Docker eliminates itself — no systemd (PID 1 is not init), no Wayland, no sway. You can test the Go binary in isolation but not the distro stack. A Vagrant-managed VM runs real Ubuntu 24.04 with real init, real systemd, and real sway. It provisions automatically from the same `cloud-init` config as a real install — the VM is always a known, reproducible state.

**Why Vagrant and not manual QEMU:**
A manually provisioned VM drifts. The moment it diverges from what `cloud-init` would do on a real install, the testing value degrades. Vagrant destroys and recreates cleanly on demand — `vagrant destroy && vagrant up` always gives a fresh, correctly provisioned environment.

**Headless sway for Phases 0–3:**
sway runs with its headless backend (`WLR_BACKENDS=headless`) in the VM. No display output, but:
- `hyperi.service` starts and the agent pipeline runs — verifiable via `journalctl -u hyperi`
- `swaymsg` IPC works — verifiable by querying the window tree
- All capability types through Phase 3 are fully exercisable
- The TUI shell works over `vagrant ssh` — fully functional, not headless
- Plan docs and audit logs are accessible from the host via Vagrant's shared folder

Phase 4 display management requires a real display or VNC — added to the VM config at that point.

**Real machine testing path (before Phase 5):**
The `cloud-init/user-data.yaml` and `preseed/preseed.cfg` are already written. To test on real hardware:
1. Flash a stock Ubuntu 24.04 Server ISO to USB
2. Boot, use preseed for unattended install, or manually provision
3. Copy `cloud-init/user-data.yaml` to the machine and run `cloud-init init`
4. Copy the built `hyperi` binary to `/usr/local/bin/hyperi`
5. Enable `hyperi.service`

This is documented as a Phase 0 step — real hardware testing does not require waiting for Phase 5 ISO builder. The ISO builder is for distribution, not for development verification.

**`build-iso.sh` split into three targets:**

| Target | Tool | Available | Purpose |
|---|---|---|---|
| `just dev` | Vagrant | Phase 0 | Spin up dev VM; provisions via cloud-init |
| `just dev-ssh` | Vagrant | Phase 0 | SSH into dev VM |
| `just dev-destroy` | Vagrant | Phase 0 | Tear down and recreate clean |
| `just build-image` | QEMU + cloud-init | Phase 3–4 | Bootable QEMU disk image; heavier testing without Vagrant |
| `just build-iso` | live-build | Phase 5 | Distributable `.iso` for bare metal |

**`Vagrantfile` spec:**
- Box: `ubuntu/noble64` (Ubuntu 24.04 LTS)
- Provisioner: shell — copies `cloud-init/user-data.yaml`, installs packages, creates `hyperi` user, configures sudoers, starts sway headless
- Shared folder: repo root → `/vagrant` in VM; `hyperi` binary synced automatically on `vagrant provision`
- Port forward: `2222` → VM SSH (Vagrant default); `8080` → web UI port for future use
- Memory: 2GB minimum; 4GB recommended for Phase 4 display testing

**What changes in repo:**
- `Vagrantfile` — added to repo root
- `distro/dev/` — dev environment scripts (headless sway launcher, provision helper)
- `justfile` — `dev`, `dev-ssh`, `dev-destroy`, `build-image`, `build-iso` targets split out
- `distro/build/build-iso.sh` — scoped to ISO-only; build-image becomes a separate script

**Status:** [x] Resolved

---

## Design Decision: Retry/Wait, Scheduling, and the Event Bus

**Resolved — incorporated into plan.md.**

These three concerns were identified together as facets of the same missing concept: the agent needs a relationship with time, not just a single-shot execute-and-exit model.

**Retry/wait (`ReadyCondition`):**
- Added as an optional field on `ActionStep` — not a separate step type (v1 decision, documented as promotable later)
- The executor polls the condition after running the command, before marking the step complete
- Condition types: `exit:0`, `process:active`, `file:exists`, `output:contains`, `http:ok`, `atspi:present`, `vision:confirms`
- Applies to all long-running steps, not just display — service restarts, package installs, any step whose effect is not instantaneous
- Tradeoff: keeping it as a field rather than a step type means the Adversarial Agent cannot independently reason about wait conditions; accepted for v1 complexity reasons

**Scheduling (`execute:schedule`):**
- New capability type: `execute:schedule:systemd:<name>` and `execute:schedule:cron:<name>`
- Added because users will directly request it ("remind me every Sunday...")
- Two backends: systemd timers (persistent, survives restart) for user-directed tasks; `robfig/cron` in-process for agent-internal cadence
- `execute:schedule:systemd` decomposes internally into `execute:config` + `execute:process` — the capability is a user-facing abstraction over those two steps
- Scheduled sessions re-enter the full agent pipeline with a stored intent

**Event bus (`internal/bus/`):**
- Buffered `chan Event` in Go; decouples producers (executor, scheduler, monitors) from consumers (TUI, web UI, audit log)
- Solves the persistent shell problem: the TUI can receive unsolicited messages (step completion, alerts, approval requests) without user input
- Approval prompts for `modified` verdicts flow through the bus as `EventApprovalNeeded`; the pipeline pauses on a reply channel while the TUI handles the interaction
- Both TUI and web UI are consumers of the same bus — this is also the answer to Critique 4 (shared state without shared code)
- All events are written to the audit log regardless of other consumers

**Cross-references:** Event bus directly informs Critique 4 (TUI vs web UI). `ReadyCondition` directly informs Critique 3 (display management feedback loop).

---

## Design Decision: Linux OS Permissions as a Security Layer

**Resolved — incorporated into plan.md.**

The agent runs as a dedicated `hyperi` system user. Linux DAC (file permissions, ownership) and PAM (authentication) are a proven, kernel-enforced security boundary that sits *below* the capability system. This is not supplementary — it is the outermost and most trusted layer.

Key implications:
- Actions requiring elevated privilege (apt, systemctl write ops, config outside `/var/lib/hyperi/`) must go through `sudo`, which can require a real human PAM authentication event
- The agent cannot escalate its own privileges regardless of what the LLM decides
- This naturally enforces a subset of the Graduated Autonomy levels at the OS level: some things are physically gated on human auth, not just policy
- The capability system and Arbiter operate *within* what the OS permits — they add context-aware judgment on top of what Linux already enforces

Cross-references: this partially informs **Critique 5** (autonomy levels — OS permissions handle the hard floor) and **Critique 6** (rollback — some actions requiring irreversibility are already gated by PAM).

---

## What's Already Well-Handled

These are explicitly called out as correct — don't change them during the revision pass:

- `types.go` is logic-free; all shared structs with no behavior
- Capability scoping is per-action, not role-based — the right model
- The three-layer safety stack (OS permissions → Arbiter → Capability Registry) is sound
- Audit trail to JSONL is simple, append-only, and correct
- Adversarial Agent as a first-class pipeline stage is underused in the industry — keep it
- Phase sequencing (agent core before UX) is correct

---

## Resolution Status (Pass 1)

| # | Critique | Status |
|---|---|---|
| 1 | Agent pipeline is stateless — no feedback loop | [x] Resolved |
| 2 | `execute:shell` broken by design — NL command extraction | [x] Resolved |
| 3 | Phase 4 display management — agent is blind | [x] Resolved |
| 4 | Two competing interfaces — TUI vs web UI | [x] Resolved |
| 5 | Graduated autonomy levels defined but not implemented | [x] Resolved |
| 6 | No recovery, rollback, or undo semantics | [x] Resolved |
| 7 | ISO build is Phase 5 — no integration test path | [x] Resolved |

---

# Second Critique Pass — V1 Blockers Only

Focused review after the first pass. Only issues that should block v1 shipping.

---

## Blocker 1: Phase 1 is a Single Enormous Phase — Will Never Ship as Written

**Problem:**
Phase 1 contained: agent pipeline port, executor rewrite, `Command []string`, `on_failure`, `ReadyCondition`, `CommandValidator`, full plan doc system, re-plan loop, crash recovery, config package, autonomy level wiring, six new capability types, system manifest with inotify, event bus, and in-process scheduler. No intermediate state was shippable or testable.

**Decision: Split into Phase 1A / 1B / 1C with explicit exit criteria.**

| Phase | Goal | Exit criteria |
|---|---|---|
| 1A | Make the pipeline correct | Multi-step plan runs end-to-end on dev VM; steps fail per policy; audit trail meaningful |
| 1B | Make it persistent and recoverable | Crash + resume works; re-plan works; autonomy level changes Arbiter behavior |
| 1C | Make it observable and time-aware | Event bus proven; background session surfaces in TUI; manifest live; `execute:schedule` works |

**Key scoping decision:** `CommandValidator` manifest path check is stubbed in 1A, wired in 1C when the manifest exists. 1A ships with allowlist-only validation — safe because the allowlist is the hard boundary; the manifest adds context, not enforcement.

See Phase 1A / 1B / 1C in plan.md for full details.

**Status:** [x] Resolved

---

## Blocker 2: Plan Doc Format Is Not a Parsing Spec

**Problem:**
The plan doc is both human-readable markdown and machine-parseable by the resume parser. The resume parser needs to reliably identify `Result: success|failure|skipped` from step sections, but there's nothing preventing LLM-generated output or command stdout from containing that exact string in prose. The parsing contract is ambiguous — a naive line scanner would produce false positives.

**Decision: Machine-readable fields use a fenced metadata block per step. LLM and command output is always in a separate fenced code block.**

Each execution step section has two clearly delimited regions:
1. A ```` ```hyperi-meta ```` fenced block containing structured key-value fields the parser reads
2. A ```` ```output ```` fenced block containing raw command stdout/stderr — never parsed for machine fields

```markdown
### Step 2: install nginx

Verdict: modified — requires user approval
Approval: granted at 2026-05-06T03:12:44Z

\`\`\`hyperi-meta
result: failure
exit_code: 1
started: 2026-05-06T03:12:45Z
duration_ms: 3201
on_failure: replan
\`\`\`

\`\`\`output
E: Unable to fetch some archives, try apt-get update or --fix-missing
\`\`\`
```

**Parsing contract:**
- The resume parser scans for `### Step` headings to find step sections
- Within each step section, it looks for a ```` ```hyperi-meta ```` block and reads only the key-value pairs inside it
- Everything outside `hyperi-meta` blocks is prose — never parsed for machine fields
- `result:` is the primary resume field: `success | failure | skipped | halted | pending`
- A step with no `hyperi-meta` block is `pending` — not yet executed
- The parser never reads ```` ```output ```` blocks; command output cannot produce false positives

**LLM output isolation:**
Pipeline stage outputs (goal graph, risk report, re-plan sections) are also fenced:
- Intent Agent output: ```` ```hyperi-intent ````
- Planner output: ```` ```hyperi-plan ````
- Adversarial Agent output: ```` ```hyperi-risk ````
- Arbiter verdicts: inline in step section prose (human-readable), but verdict for resume purposes is in `hyperi-meta` block

**Why this works:**
- The `hyperi-meta` fence label is unlikely to appear in LLM prose or command output
- If it somehow does appear in command output, it's inside an `output` block which is never parsed
- The format remains readable to a human skimming the markdown file
- The parser is a simple line-by-line scanner looking for known fence labels — no regex magic, no fragile heuristics

**What changes in code:**
- `internal/plan/` — writer emits `hyperi-meta` blocks for all machine fields; `output` blocks for all command output
- `internal/plan/` — resume parser scans for `hyperi-meta` blocks only; ignores all other content
- Plan doc structure example in `plan.md` updated to reflect this format

**Status:** [x] Resolved

---

## Blocker 3: Approval Timeout Is Unspecified

**Problem:**
The approval flow says "the pipeline pauses waiting on the reply channel until a response arrives or the approval times out." No timeout value is defined, and the behavior on timeout (fail the step? skip? halt the session?) is unspecified. A background session that fires a `modified` verdict at 3am with no one watching would block indefinitely.

**Decision: Configurable timeouts with `halt` on expiry. Halted plans are discoverable and resumable.**

**Timeout values (configurable in `config.json`, these are defaults):**
- Foreground session approval timeout: **5 minutes** — user is present; long enough to not be annoying
- Background session approval timeout: **30 seconds** — no human present; fail fast

**Behavior on expiry:** `halt` — the step does not execute. Plan doc records `Approval: timed out at <timestamp>`. Session status set to `halted`. `EventPlanFailed` published to event bus with reason `approval-timeout`. This is intentionally conservative — silently skipping a step the Arbiter flagged as requiring approval is worse than halting.

**`config.json` additions:**
```json
{
  "approval_timeout_foreground_seconds": 300,
  "approval_timeout_background_seconds": 30
}
```

**Halted plan discovery and resumption:**
A plan halted on an approval timeout needs to be findable and actionable — otherwise it silently accumulates in `/var/lib/hyperi/plans/`.

- **On TUI startup:** if any plans have status `halted` or `in-progress`, surface them prominently before the main prompt — e.g. `2 plans need attention (halted). Run 'hyperi plans' to review.`
- **`hyperi plans` command:** lists all plans by status (in-progress, halted, completed, failed) with intent summary, last updated timestamp, and halt reason
- **`hyperi session resume <id>`:** for approval-halted plans, re-presents the pending approval prompt and continues if approved; for execution-halted plans, re-enters the re-plan loop with full failure context already in the plan doc — no re-running the pipeline from scratch

Resume behavior is Phase 1B scope — it is part of the crash recovery and re-plan loop work.

**What changes in code:**
- `internal/config/` — add `ApprovalTimeoutForeground`, `ApprovalTimeoutBackground` fields
- `internal/bus/` — `EventApprovalNeeded` carries the applicable timeout; reply channel closes on first response or timeout
- `internal/plan/` — record `Approval: timed out` in plan doc on expiry; set status `halted`
- `cmd/hyperi/main.go` — `plans` subcommand (list by status); `session resume` wired to re-enter pipeline correctly
- `internal/shell/` — startup check for halted/in-progress plans; surface notification before main prompt

**Status:** [x] Resolved

---

## Blocker 4: `executeConfig()` and `executeNetwork()` Stubs Have No Resolution Path in the Plan

**Problem:**
Both are listed as known bugs but Phase 1A lists "fix the five known inherited bugs" without calling them out explicitly as capability-blocking. `executeConfig()` blocks `execute:schedule:systemd` (which writes unit files) and any config-writing task. `executeNetwork()` blocks `network:outbound`. Both need explicit scheduling in 1A.

**Decision: Both are explicit Phase 1A deliverables with defined calling conventions.**

**`executeConfig()` implementation:**
- `Command []string` convention: `["<path>", "<content>"]` — two elements, path and literal file content
- Executor calls `os.WriteFile(path, []byte(content), 0644)` — atomic, no shell, no temp files
- OS permissions are the safety boundary — if `hyperi` user doesn't own the path, the write fails at the syscall level; no special handling needed in the executor
- `Capability.Scope` is the path (for allowlist matching); `Command[0]` is also the path (for execution); they must match — `CommandValidator` checks this

**`executeNetwork()` implementation:**
- `Command []string` convention: `["GET", "https://host/path"]` or `["POST", "https://host/path", "<body>"]`
- Executor uses `net/http` standard library; no external dependencies
- Response body returned as `ExecutionResult.Output`; non-2xx status codes are failures
- `Capability.Scope` is the host (for allowlist matching); `CommandValidator` checks that `Command[1]`'s host matches the declared scope

**Phase 1A additions (explicit):**
- `executeConfig()` — implement per calling convention above; remove stub
- `executeNetwork()` — implement per calling convention above; remove stub
- Both calling conventions documented in Planner system prompt so LLM emits correct `Command` shape

**Status:** [x] Resolved

---

## Blocker 5: System Manifest Complexity Rivals the Agent Pipeline

**Problem:**
The manifest (first-boot scan, inotify watcher, `IN_MOVED_TO` handling, watch limit config, startup reconciliation, post-execution hooks, manifest reader used by Arbiter and CommandValidator) is Phase 1 scope but is genuinely complex. If it's not working correctly, the CommandValidator's path checks are broken and the Arbiter lacks context. The question is whether 1A/1B can ship without it.

**Decision:** Manifest is Phase 1C scope. 1A and 1B ship with allowlist-only validation. The manifest adds context and deterministic path checks — it does not provide the hard enforcement boundary (that's OS permissions + allowlist). This is already captured in the 1A/1B/1C split above.

**Status:** [x] Resolved (subsumed by Blocker 1 split)

---

## Blocker 6: No Defined Error Contract for LLM Pipeline-Stage Failures

**Problem:**
The Planner, Intent Agent, and Adversarial Agent all write to the plan doc. What happens when an LLM call fails (network error, rate limit, malformed JSON)? The plan doc would be partially written. The resume parser would see an `in-progress` document with an incomplete pipeline section. The re-plan loop handles step-level failures but not stage-level failures. No retry policy or partial-state behavior is defined for pipeline stage failures.

**Decision: Stage-level retry with exponential backoff; explicit stage status in plan doc; failed stages surface to user with full context.**

**Stage status tracking:**
Each pipeline stage writes a `hyperi-meta` block to the plan doc when it starts and updates it when it completes or fails:

```markdown
## Intent

\`\`\`hyperi-meta
stage: intent
status: completed
started: 2026-05-06T03:10:01Z
completed: 2026-05-06T03:10:03Z
\`\`\`

\`\`\`hyperi-intent
(goal graph output)
\`\`\`
```

If the stage fails mid-write, the `hyperi-meta` block has `status: in-progress` with no `completed` timestamp. On resume, the parser sees this and knows the stage needs to be retried — not the step execution, but the LLM call itself.

**Stage-level retry policy:**
- Network error or HTTP 5xx: retry up to **3 times** with exponential backoff (2s, 4s, 8s)
- Rate limit (HTTP 429): retry up to **5 times** with backoff respecting `Retry-After` header if present
- Malformed JSON response (LLM hallucination): retry up to **2 times** — if the LLM returns unparseable output twice, that is a prompt engineering problem, not a transient error; halt and surface to user
- All other errors: halt immediately; surface to user with full error context

**Partial plan doc state on stage failure:**
The plan doc always records what was attempted. A failed stage leaves its `hyperi-meta` block with `status: failed` and an `error:` field. The plan doc status is set to `halted`. The user can inspect the plan doc to see exactly which stage failed and why.

**Resume after stage failure:**
The resume parser checks each stage's `hyperi-meta` status in order. The first stage with `status: in-progress` or `status: failed` is the resume point. The pipeline re-runs from that stage — completed stages are not re-run. This means:
- Intent Agent completed, Planner failed → resume runs Planner only, reusing the existing goal graph
- Planner completed, Adversarial Agent failed → resume runs Adversarial Agent only, reusing the existing plan
- Stage retry exhausted → status `halted`; user must explicitly resume or abandon

**Malformed JSON handling:**
The LLM occasionally returns output with markdown fences, prose before/after the JSON, or structurally invalid JSON. The existing `extractJSON()` helper in `agents/intent.go` handles some of this. This helper needs to be applied consistently across all three agents and its failure mode needs to count against the malformed-JSON retry budget, not the network retry budget.

**What changes in code:**
- `internal/plan/` — stage writer emits `hyperi-meta` with `status: in-progress` on start, updates to `completed` or `failed` on finish
- `internal/llm/client.go` — retry logic with backoff; separate retry budgets for network vs rate-limit vs malformed-JSON errors
- `internal/agents/` — all three agents use consistent `extractJSON()` with failure counted against retry budget
- `internal/plan/` — resume parser checks stage status blocks; resumes from first non-completed stage
- `cmd/hyperi/main.go` — stage-level retry loop wraps each pipeline stage call

**Status:** [x] Resolved

---

## Second Pass Resolution Status

| # | Blocker | Status |
|---|---|---|
| 1 | Phase 1 too large — will never ship | [x] Resolved — split into 1A/1B/1C |
| 2 | Plan doc format not a parsing spec | [x] Resolved — `hyperi-meta` fenced blocks |
| 3 | Approval timeout unspecified | [x] Resolved — 5m/30s configurable; halt on expiry; halted plan discovery |
| 4 | `executeConfig()` / `executeNetwork()` stubs unscheduled | [x] Resolved — explicit Phase 1A with calling conventions |
| 5 | System manifest complexity in Phase 1 | [x] Resolved — subsumed by 1A/1B/1C split; manifest is 1C |
| 6 | No LLM stage failure handling | [x] Resolved — stage retry policy; stage status in plan doc; resume from failed stage |
