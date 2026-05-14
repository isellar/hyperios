# HyperiOS — Implementation Tasks

Concrete, sequenced tasks broken out from the build phases in `plan.md`. Each task is small enough to be a single PR or a focused work session. Tasks within a phase are ordered by dependency — work top to bottom within each phase.

Status values: `[ ]` pending, `[~]` in progress, `[x]` done

---

## Phase 0 — Base Distro (Foundation)

These tasks establish the dev environment and the on-device OS primitives. Most of the distro config files already exist — the work here is wiring them together and verifying they actually run.

### 0.1 — Verify and fix existing distro config files
- [ ] Review `distro/cloud-init/user-data.yaml` — confirm `hyperi` user creation, directory setup, package installs are correct for Ubuntu 24.04
- [ ] Review `distro/preseed/preseed.cfg` — confirm unattended install config is valid
- [ ] Review `distro/systemd/hyperi.service` — confirm `ExecStart` path, hardening flags, restart policy
- [ ] Review `distro/sway/config` — confirm kiosk mode, workspace 1 fullscreen, headless backend flag present
- [ ] Uncomment binary install line in `cloud-init/user-data.yaml` once a build artifact path is defined (placeholder for now)

### 0.2 — Create `hyperi` user and sudoers config
- [ ] Write `/etc/sudoers.d/hyperi` config defining:
  - Commands `hyperi` can run without password (e.g. `systemctl status *`)
  - Commands requiring PAM auth (e.g. `apt install *`)
  - Commands never permitted (e.g. `visudo`, `passwd`, `su`)
- [ ] Add `hyperi-admin` group definition to cloud-init provisioning
- [ ] Verify `hyperi` user has no shell login and no home directory outside `/var/lib/hyperi/`

### 0.3 — Vagrantfile
- [ ] Create `Vagrantfile` at repo root:
  - Box: `ubuntu/noble64` (Ubuntu 24.04 LTS)
  - 2GB RAM minimum
  - Synced folder: repo root → `/vagrant`
  - Provisioner: shell script that runs cloud-init equivalent (create user, install packages, configure sudoers, set up directories)
  - Port forward: `8080` → web UI (future use)
- [ ] Create `distro/dev/provision.sh` — idempotent provisioning script used by Vagrant
- [ ] Create `distro/dev/sway-headless.sh` — starts sway with `WLR_BACKENDS=headless` for CI/headless testing

### 0.4 — Justfile targets
- [ ] Add `just dev` — `vagrant up`
- [ ] Add `just dev-ssh` — `vagrant ssh`
- [ ] Add `just dev-destroy` — `vagrant destroy -f`
- [ ] Add `just dev-provision` — `vagrant provision` (re-run provisioner without recreating VM)
- [ ] Verify existing `just build` cross-compiles correctly for `linux/amd64` and `linux/arm64`

### 0.5 — Verify full stack boots in VM
- [ ] `just dev` — VM comes up without errors
- [ ] `vagrant ssh` — confirm `hyperi` user exists, directories created, sudoers in place
- [ ] Copy built `hyperi` binary to VM; confirm `systemctl start hyperi` starts without errors
- [ ] Confirm `journalctl -u hyperi` shows service output
- [ ] Confirm sway starts headless without errors via `distro/dev/sway-headless.sh`

### 0.6 — Document real machine testing path
- [ ] Write `distro/dev/real-machine.md` — step-by-step instructions for provisioning a real Ubuntu 24.04 machine without the ISO builder:
  1. Flash stock Ubuntu 24.04 Server ISO
  2. Boot and install (use preseed or manual)
  3. Run `distro/dev/provision.sh`
  4. Copy `hyperi` binary
  5. `systemctl enable --now hyperi`

---

## Phase 1A — Agent Core: Make the Pipeline Correct

Goal: `hyperi session start --execute` runs a real multi-step plan on the dev VM, executes correctly, handles step failure per policy, and produces a meaningful audit trail.

### 1A.1 — Fix inherited bugs
- [ ] **Bug 1:** `registry.Check()` glob matching — replace `filepath.Match` with a proper glob library (e.g. `github.com/gobwas/glob`) or implement recursive walk; verify `{workspace}/**` matches nested paths on Linux
- [ ] **Bug 2:** `ErrCapabilityNotGranted` sentinel — audit all call sites; replace `errors.Is` with `errors.As`; add test covering the struct fields
- [ ] **Bug 3:** `session.Progress()` overcount — add deduplication in `MarkCompleted()` before appending; add test for double-call with same step ID
- [ ] **Bug 4:** `executeNetwork()` stub — implement (see task 1A.6)
- [ ] **Bug 5:** `executeConfig()` stub — implement (see task 1A.7)

### 1A.2 — Add `Command []string` to `ActionStep`
- [ ] Add `Command []string` to `types.go` `ActionStep` struct
- [ ] Add `OnFailure string`, `MaxRetries int`, `RetryBackoffSeconds int` to `ActionStep`
- [ ] Add `ReadyCondition *ReadyCondition` to `ActionStep`; add `ReadyCondition` struct with all fields and condition type constants
- [ ] Update all existing tests that construct `ActionStep` literals
- [ ] Update `Stub.Present()` in `executor/executor.go` to display `Command` in plan output

### 1A.3 — Update Planner system prompt
- [ ] Update `internal/agents/planner.go` system prompt to require per step:
  - `command: []string` — literal executable command
  - `on_failure: halt|retry|replan|skip`
  - `max_retries` and `retry_backoff_seconds` when `on_failure == retry`
  - `ready_condition` — optional; include condition type, target, timeout, poll interval
- [ ] Add `extractJSON()` helper to planner (consistent with intent agent pattern)
- [ ] Add unit test: mock LLM response with `Command` field; confirm plan parses correctly
- [ ] Add unit test: mock LLM response missing `Command` field; confirm error is returned not a zero-value

### 1A.4 — Rewrite `executor/local.go` — remove NL extraction, use `Command []string`
- [ ] Remove all keyword extraction / NL parsing from all capability handlers
- [ ] `executeShell()` — `exec.Command(step.Command[0], step.Command[1:]...)`; no shell interpolation
- [ ] `executeGit()` — same pattern; validate `step.Command[0]` is `git`
- [ ] `executePackage()` — parse manager from `Capability.Scope`; build command from scope + `Command` args
- [ ] `executeProcess()` — `exec.Command("systemctl", step.Command...)` with action allowlist validation
- [ ] `executeDisplay()` — `exec.Command("swaymsg", step.Command...)`
- [ ] All handlers: capture stdout, stderr, exit code into `ExecutionResult`; set `Success = exitCode == 0`

### 1A.5 — Add `CommandValidator`
- [ ] Create `internal/capability/validator.go`
- [ ] `ValidateCommand(step ActionStep) ValidationResult` with `Valid bool`, `Error string`, `Reason string`
- [ ] Check 1 — structural: `Command` non-empty; `Command[0]` resolvable via `exec.LookPath`; no shell metacharacters (`|`, `;`, `&&`, `$()`, backticks, `>`, `<`) in any argument
- [ ] Check 2 — allowlist: `Command[0]` binary matches an entry in registry for declared capability type
- [ ] Check 3 — path consistency: for `execute:config`, `Command[0]` must match `Capability.Scope`; for `network:outbound`, host in `Command[1]` URL must match `Capability.Scope`
- [ ] Check 4 — manifest path check: **stub returning `Valid: true`** — wired in Phase 1C
- [ ] Wire `CommandValidator` into `cmd/hyperi/main.go` pipeline after Arbiter, before Executor
- [ ] Add unit tests covering: valid command, metacharacter injection attempt, binary not on allowlist, scope mismatch

### 1A.6 — Implement `executeNetwork()`
- [ ] `Command` convention: `["GET", "https://host/path"]` or `["POST", "https://host/path", "<body>"]`
- [ ] Use `net/http` standard library; set reasonable timeout (30s default)
- [ ] Return response body as `ExecutionResult.Output`; non-2xx → `Success = false`, status code in `Error`
- [ ] Validate that host in `Command[1]` URL matches `Capability.Scope` (allowlist check)
- [ ] Add unit test with mock HTTP server

### 1A.7 — Implement `executeConfig()`
- [ ] `Command` convention: `["<path>", "<content>"]`
- [ ] Validate `Command[0]` matches `Capability.Scope`
- [ ] `os.WriteFile(path, []byte(content), 0644)` — atomic write, no shell, no temp files
- [ ] OS permissions are the safety boundary — no special handling needed beyond letting the syscall fail naturally
- [ ] Add unit test: write to a temp path; verify content; verify error on unwritable path

### 1A.8 — Executor retry loop for `ReadyCondition`
- [ ] After executing a step, if `step.ReadyCondition != nil`, enter poll loop
- [ ] Implement condition checkers:
  - `exit:0` — re-run `Command` and check exit code (when `RetryCommand: true`)
  - `process:active` — `exec.Command("systemctl", "is-active", target)`
  - `file:exists` — `os.Stat(target)`
  - `output:contains` — re-run command, check stdout contains `Target` string
  - `http:ok` — HTTP GET to `Target` URL, check 2xx
  - `atspi:present` — **stub returning true** (Phase 4)
  - `vision:confirms` — **stub returning true** (Phase 4)
- [ ] Poll at `PollIntervalSeconds` (default 2s); fail step if `TimeoutSeconds` exceeded
- [ ] Add unit tests for `process:active` and `file:exists` condition types

### 1A.9 — `on_failure` executor enforcement
- [ ] After a step fails (non-zero exit or `ReadyCondition` timeout):
  - `halt` — stop pipeline; write failure to audit log; return error to caller
  - `retry` — re-execute up to `MaxRetries` times with `RetryBackoffSeconds` sleep between attempts; then `halt`
  - `replan` — return a sentinel error to the pipeline loop indicating re-plan is needed
  - `skip` — mark step as skipped in `ExecutionResult`; continue to next step
- [ ] Add integration test on dev VM: plan with `on_failure: retry`; confirm retries fire and backoff is respected

### 1A.10 — Verify end-to-end on dev VM
- [ ] Build binary; copy to VM
- [ ] Run `hyperi session start --execute` with a real multi-step plan (e.g. install a package, check a service status, read a file)
- [ ] Confirm: correct commands execute, step results captured, audit log written, `on_failure` policy respected
- [ ] Run with a deliberately failing step; confirm halt/retry/skip behavior

---

## Phase 1B — Agent Core: Make it Persistent and Recoverable

Goal: sessions survive crashes, re-plans work, autonomy levels change behavior, every task has a complete execution record.

### 1B.1 — `internal/plan/` package — writer
- [ ] Create `internal/plan/writer.go` — `PlanDoc` struct; file path; append-only writer
- [ ] `WriteHeader(session, intent)` — writes frontmatter: session ID, status, timestamps
- [ ] `WriteStageStart(stage string)` — appends `hyperi-meta` block with `status: in-progress`
- [ ] `WriteStageComplete(stage string, output string, fenceLabel string)` — updates meta to `completed`; appends fenced output block
- [ ] `WriteStageFailed(stage string, err error)` — updates meta to `failed`; appends error
- [ ] `WriteStepVerdict(step, verdict, reason)` — appends step heading + verdict prose
- [ ] `WriteStepApproval(step, granted bool, timestamp)` — appends approval line
- [ ] `WriteStepStart(step)` — opens `hyperi-meta` block with `status: in-progress`
- [ ] `WriteStepResult(step, result ExecutionResult)` — completes `hyperi-meta` block; appends `output` block
- [ ] `WriteReplanHeader(n int, triggerStepID string, attempt int, requiresConfirmation bool)` — appends `## Re-plan N` section header
- [ ] `UpdateStatus(status string)` — rewrites frontmatter `Status:` field
- [ ] Unit tests for each writer method — write to temp file, read back, verify structure

### 1B.2 — `internal/plan/` package — resume parser
- [ ] Create `internal/plan/parser.go`
- [ ] `ParsePlanDoc(path string) (*PlanState, error)` returning:
  - `Status string` — from frontmatter
  - `Attempt int` — from frontmatter
  - `Stages map[string]StageStatus` — each stage's `status` from `hyperi-meta` blocks
  - `Steps []StepState` — each step's `result`, `exit_code`, `on_failure` from `hyperi-meta` blocks
  - `PendingApproval *StepID` — step waiting for approval (result: `pending-approval`)
- [ ] Parser only reads `hyperi-meta` blocks; all other content is ignored
- [ ] `NextPendingStep(state *PlanState) (stepID string, isStage bool)` — returns first incomplete stage or step
- [ ] Unit tests: parse a complete plan doc; parse a partially executed plan doc; parse a plan doc with a failed stage; parse a plan doc with `in-progress` step

### 1B.3 — Wire pipeline stages to write plan doc
- [ ] Update `cmd/hyperi/main.go` `runSession()` to:
  - Create plan doc via `plan.Writer` before calling Intent Agent
  - Pass writer to each pipeline stage
- [ ] Update `internal/agents/intent.go` — accept writer; call `WriteStageStart`/`WriteStageComplete`/`WriteStageFailed`
- [ ] Update `internal/agents/planner.go` — same pattern
- [ ] Update `internal/agents/adversarial.go` — same pattern
- [ ] Update `internal/arbiter/arbiter.go` — write verdict per step via writer
- [ ] Update executor — write step start/result via writer in real time

### 1B.4 — LLM stage-level retry
- [ ] Update `internal/llm/client.go`:
  - Separate retry budgets: network/5xx (3 retries, exponential backoff 2s/4s/8s), rate limit 429 (5 retries, respect `Retry-After`), malformed JSON (2 retries)
  - Return typed errors: `NetworkError`, `RateLimitError`, `MalformedResponseError`
- [ ] Apply consistent `extractJSON()` across all three agents — strip markdown fences, extract first valid JSON object
- [ ] Malformed JSON retries count against `MalformedResponseError` budget
- [ ] Unit tests: mock LLM returning 429 then success; mock returning malformed JSON twice then success; mock returning malformed JSON three times → error

### 1B.5 — Crash recovery on startup
- [ ] On `hyperi` startup, scan `/var/lib/hyperi/plans/` for docs with `status: in-progress` or `status: halted`
- [ ] For each: call `ParsePlanDoc`; determine resume point via `NextPendingStep`
- [ ] If pending stage (incomplete LLM call): re-run that stage using prior stage outputs already in plan doc
- [ ] If pending step (execution): resume executor from that step
- [ ] If pending approval: re-publish `EventApprovalNeeded` (or surface via `hyperi plans` for user to action)
- [ ] Update plan doc `Updated` timestamp on resume
- [ ] Integration test on dev VM: kill process mid-execution; restart; confirm correct resume

### 1B.6 — Re-plan loop
- [ ] In `cmd/hyperi/main.go`: wrap execution in re-plan loop (max 3 attempts)
- [ ] When executor returns `replan` sentinel: increment attempt counter
- [ ] Attempt 2: automatic re-plan — call Planner with prior plan doc sections as context; append `## Re-plan 1` to plan doc
- [ ] Attempt 3: publish `EventApprovalNeeded` asking user to confirm third attempt; wait for reply with timeout
- [ ] After attempt 3 failure: set plan doc status to `failed`; surface to user
- [ ] Conditional Adversarial Agent: diff new plan steps against original; only re-run AA if new capability types or paths introduced
- [ ] Integration test: construct a plan where step 1 always fails with `on_failure: replan`; confirm re-plan fires; confirm plan doc has `## Re-plan 1` section

### 1B.7 — `internal/config/` package
- [ ] Create `internal/config/config.go` — `Config` struct with all fields
- [ ] Fields: `AutonomyLevel int`, `ApprovalTimeoutForeground int`, `ApprovalTimeoutBackground int`, `WatchPaths []string`
- [ ] `Load(path string) (*Config, error)` — reads `/var/lib/hyperi/config.json`; returns defaults if file absent
- [ ] `Save(path string, cfg *Config) error`
- [ ] Default values: `AutonomyLevel: 1`, `ApprovalTimeoutForeground: 300`, `ApprovalTimeoutBackground: 30`
- [ ] Add `hyperi config get <key>` and `hyperi config set <key> <value>` CLI subcommands
- [ ] Unit tests: load missing file returns defaults; load existing file returns correct values; save then load round-trips

### 1B.8 — Wire autonomy level into Arbiter
- [ ] `internal/arbiter/arbiter.go` — accept `autonomyLevel int` as parameter to `Evaluate()`
- [ ] Implement per-level verdict logic (see Graduated Autonomy Levels table in plan.md)
- [ ] Add `Autonomy int` field to `ArbiterVerdict`; record level at evaluation time
- [ ] Level 0 special case: all steps return `modified`; executor skips execution entirely; plan presented as suggestion
- [ ] Update `cmd/hyperi/main.go` to load config and pass autonomy level to arbiter
- [ ] Update `--autonomy` flag: override global default for this session; cap background sessions at global default
- [ ] Update existing arbiter tests; add new tests for each autonomy level

### 1B.9 — `hyperi plans` and `hyperi session resume`
- [ ] Add `hyperi plans` command: scan plan docs; display table of (id, intent summary, status, last updated, halt reason)
- [ ] Add status filter flag: `hyperi plans --status halted`
- [ ] Update `hyperi session resume <id>`: call `ParsePlanDoc`; determine resume point; re-enter pipeline correctly
  - Approval-halted: re-publish approval prompt; continue if approved
  - Execution-halted: re-enter re-plan loop with existing plan doc context
  - Stage-failed: re-run failed stage

### 1B.10 — Approval timeout
- [ ] `EventApprovalNeeded` carries `TimeoutSeconds int` set from config (foreground vs background)
- [ ] Reply channel closes after first response or timeout
- [ ] On timeout: call `writer.WriteStepApproval(step, false, now)` with `"timed out"` reason; set plan status `halted`; publish `EventPlanFailed` with reason `approval-timeout`
- [ ] Unit test: mock approval channel that never responds; confirm timeout fires and plan halts

### 1B.11 — Slim session JSON to thin index
- [ ] Remove execution state from `internal/session/state.go` (completed steps, plan — these are now in plan doc)
- [ ] Keep: `ID`, `Intent` (summary), `Status`, `CreatedAt`, `UpdatedAt`, `PlanDocPath`, `AutonomyLevel`, `AutonomyOverride`
- [ ] Update `session.Manager` save/load accordingly
- [ ] Update all call sites

### 1B.12 — Verify end-to-end on dev VM
- [ ] Kill hyperi mid-plan; confirm resume from correct step
- [ ] Trigger re-plan; confirm plan doc has history; confirm Planner receives prior context
- [ ] Set `autonomy_level: 0`; confirm nothing executes and plan is presented as suggestion
- [ ] Set `autonomy_level: 2`; confirm reversible steps execute without prompt
- [ ] Run `hyperi plans`; confirm halted plans appear; run `hyperi session resume`; confirm correct behavior

---

## Phase 1C — Agent Core: Make it Observable and Time-Aware

Goal: event bus proven; TUI has everything it needs; scheduled tasks work; manifest is live.

### 1C.1 — `internal/bus/` — event bus
- [ ] Create `internal/bus/bus.go` — `Bus` struct wrapping `chan Event`; buffered (size 256)
- [ ] `Publish(event Event)` — non-blocking send; drop and log if buffer full
- [ ] `Subscribe() <-chan Event` — returns read channel; bus fans out to all subscribers
- [ ] Define all `EventKind` constants (see Event Bus section in plan.md)
- [ ] `Event` struct: `Kind`, `SessionID`, `StepID`, `Payload any`, `Timestamp`
- [ ] `ApprovalEvent` — embed reply channel `chan bool` in Payload; `Respond(approved bool)` helper
- [ ] Audit log consumer: goroutine that reads all events and appends to JSONL audit log
- [ ] Unit tests: publish event; subscriber receives it; audit consumer writes to file

### 1C.2 — Wire event bus into pipeline
- [ ] Pass bus to executor; publish `EventStepStarted`, `EventStepCompleted`, `EventStepFailed` per step
- [ ] Publish `EventPlanCompleted` / `EventPlanFailed` at end of session
- [ ] Replace direct approval prompt in executor with `EventApprovalNeeded` publish + reply channel wait
- [ ] Pass bus to `cmd/hyperi/main.go` pipeline loop; publish session lifecycle events

### 1C.3 — `internal/scheduler/` — in-process cron
- [ ] Create `internal/scheduler/scheduler.go` wrapping `robfig/cron`
- [ ] `Register(name, cronExpr string, fn func()) error`
- [ ] `Start()` / `Stop()`
- [ ] Default schedules registered on startup:
  - Manifest re-scan: every 6 hours
  - Session cleanup (delete plans older than 30 days): daily at 3am
  - Audit log rotation: weekly
- [ ] Scheduled job publishes `EventScheduledFired` to bus on execution

### 1C.4 — `internal/manifest/` — system manifest
- [ ] Create `internal/manifest/scanner.go` — full filesystem scan of watch paths; enumerate systemd units via `systemctl list-units`; write `manifest.json`
- [ ] Create `internal/manifest/watcher.go` — inotify watcher; watch paths from config; handle `IN_MOVED_TO`, `IN_CREATE`, `IN_DELETE`, `IN_MODIFY`; on event, re-scan affected path and update manifest entry
- [ ] Create `internal/manifest/reconciler.go` — on startup, compare mtime of each watched path against manifest `last_scanned`; queue re-scan for any path modified since last scan
- [ ] Create `internal/manifest/reader.go` — `Lookup(path string) (*PathEntry, bool)`; `LookupService(name string) (*ServiceEntry, bool)`
- [ ] First-boot scan: triggered by `hyperi` service startup on a fresh install (no existing manifest)
- [ ] Post-execution hook: after each executor step, re-scan paths that appear in `step.Command` args
- [ ] Set `fs.inotify.max_user_watches=524288` in `distro/cloud-init/user-data.yaml`
- [ ] Unit tests: scan a temp directory structure; verify entries; update a file; verify manifest updates

### 1C.5 — Wire manifest into `CommandValidator`
- [ ] Replace manifest path check stub in `CommandValidator` with real lookup
- [ ] For each `Command` arg that is an absolute path: call `manifest.Lookup(path)`
- [ ] If entry found with `requires_pam: true`: return `ValidationError` with reason `"path requires PAM authentication"`
- [ ] If entry found with `sensitivity: critical`: escalate to arbiter context (attach `ManifestContext` to step)
- [ ] Unit test: mock manifest with a `requires_pam: true` path; confirm validator rejects

### 1C.6 — `execute:schedule` capability
- [ ] Implement `executeSchedule()` in `executor/local.go`
- [ ] `systemd` backend: write `.timer` and `.service` unit files to `/etc/systemd/system/` via `executeConfig()`; run `systemctl enable --now <name>.timer` via `executeProcess()`
- [ ] `cron` backend: register entry with in-process scheduler
- [ ] Scope format: `execute:schedule:systemd:<name>` and `execute:schedule:cron:<name>`
- [ ] `Command` convention for systemd: `["<name>", "<cron-expression>", "<intent>"]` — executor generates unit file content from these
- [ ] Add `execute:schedule` entries to `config/allowlist.yaml`
- [ ] Integration test on dev VM: create a systemd timer; verify it appears in `systemctl list-timers`

### 1C.7 — Verify end-to-end on dev VM
- [ ] Run a background session via scheduler; confirm result surfaces in foreground TUI via event bus
- [ ] Modify a file in `/etc`; confirm manifest updates within seconds
- [ ] Run `execute:schedule` step; confirm systemd timer created and enabled
- [ ] Confirm `CommandValidator` rejects a command targeting a `requires_pam: true` path without PAM token

---

## Phase 2 — Terminal Shell Interface

Dependencies: Phase 1C complete; event bus proven; plan doc format stable.

### 2.1 — Replace cobra CLI with persistent TUI skeleton
- [ ] Create `internal/shell/tui.go` replacing the current stdin loop stub
- [ ] Implement basic bubbletea model: input box at bottom; scrollable output above
- [ ] `hyperi` (no subcommand) starts the TUI shell; cobra subcommands (`plans`, `config`, `session`) remain for scripting use
- [ ] TUI subscribes to event bus on startup; renders received events in output area
- [ ] Startup check: if any plans are `halted` or `in-progress`, render notification banner before prompt

### 2.2 — Input → pipeline wiring
- [ ] User input in TUI submits intent to agent pipeline
- [ ] Pipeline runs as background goroutine; events stream to TUI via event bus
- [ ] TUI renders `EventStepStarted`, `EventStepCompleted`, `EventStepFailed` as inline progress
- [ ] TUI renders `EventPlanCompleted` / `EventPlanFailed` as summary block

### 2.3 — Inline plan display
- [ ] On plan generation: render plan table inline (step description, capability, verdict, risk flags)
- [ ] Modified verdicts render with approval prompt inline in TUI
- [ ] Approval response sent via event bus reply channel
- [ ] Approval timeout countdown visible in TUI

### 2.4 — Foreground/background session model
- [ ] Implement foreground lock: write PID to `/var/lib/hyperi/session.lock` on TUI start; release on exit
- [ ] If lock exists on startup: check if PID is still running; if yes, reject with message; if no (stale lock), remove and continue
- [ ] Background session events render in TUI when foreground session is idle

### 2.5 — Session context persistence
- [ ] TUI maintains session context across commands (same foreground session ID)
- [ ] Session context injected into each pipeline run as `WorkspaceContext`
- [ ] `clear` command resets session context

### 2.6 — Verify on dev VM
- [ ] Start TUI; type an intent; confirm plan displays inline; confirm approval prompt works
- [ ] Kill TUI mid-execution; restart; confirm halted plan notification appears
- [ ] Trigger a background scheduled session; confirm result appears in idle TUI

---

## Phases 3–5 — Not Yet Broken Down

These phases depend on Phases 0–2 being stable. Task breakdown should happen when Phase 2 is complete or nearly complete.

### Phase 3 — Voice Interface
*Complete.* STT-only push-to-talk via whisper.cpp + arecord. Toggle with Ctrl+Space.
Known issues: see docs/issues.md Issue 1 (hint not rendering in TUI), Issue on WSL2 (no mic access — works on real hardware only).

### Phase 4 — Display Management
*Complete.* Layered interaction model implemented: swaymsg IPC, AT-SPI queries, grim capture, vision stub.
All code compiles and tests pass. Integration tests require real hardware with sway running (WSL2 has no compositor).
Known: vision:confirms ReadyCondition is stubbed (returns true); full implementation requires LLM vision API call.
See docs/issues.md for open items.

### Phase 5 — ISO Builder
*Complete.*
- `distro/build/build-iso.sh` — full live-build pipeline; packages, overlay, hooks, GRUB config
- `distro/build/build-image.sh` — QEMU qcow2 image from Ubuntu cloud base + cloud-init
- `distro/build/hooks/` — three chroot hooks: system setup, whisper model download, whisper build
- `justfile` — `build-iso`, `build-image`, `install-build-deps` targets
- Both scripts require a Linux build host; WSL2 works for `build-image`, `build-iso` needs more RAM

Open: artifact signing and hosting not yet designed (post-v1).
