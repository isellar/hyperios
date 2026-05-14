# HyperiOS — Open Issues

Known bugs, stubs, missing tests, and limitations found during development and final review.
Severity: **Critical** → **High** → **Medium** → **Low** → **Post-v1**

---

## Critical

### Issue 1: Deadlock in `voice.Session.Start()`
**File:** `internal/voice/voice.go:94`  
**Severity:** Critical — calling `Start()` will permanently block the goroutine.

After acquiring `s.mu` at line 70 and setting `s.started = true`, the code immediately calls `s.mu.Lock()` again (line 94). Go's `sync.Mutex` is not reentrant — this deadlocks every caller of `Start()`. The comment says "Record start time for duration tracking" but `s.duration` is never actually set. The duplicate lock/unlock should be removed entirely.

**Fix:** Delete lines 94–96 (the second `s.mu.Lock()` / `s.mu.Unlock()` block inside `Start()`).

---

## High

### Issue 2: `parseCapabilityKey` splits on first `:` only — corrupts grants on round-trip
**File:** `internal/capability/registry.go` (around `capabilityKey`/`parseCapabilityKey`)  
**Severity:** High — runtime capability grants (e.g. `execute:shell:grep`) are corrupted after process restart.

`capabilityKey("execute:shell", "grep")` produces `"execute:shell:grep"`. `parseCapabilityKey("execute:shell:grep")` splits on the first `:` and returns `("execute", "shell:grep")`. When the grant is reloaded and checked as `{Type: "execute", Scope: "shell:grep"}` it will not match the allowlist pattern for `execute:shell`. All multi-segment capability types (`execute:shell`, `execute:git`, `execute:package`, etc.) are affected.

**Fix:** Use the last `:` as the separator, or use a delimiter that doesn't appear in capability type names (e.g. `|`).

### Issue 3: `vision:confirms` ReadyCondition silently always returns true
**File:** `internal/executor/local.go:285–291`  
**Severity:** High — plan steps with `vision:confirms` conditions never actually check screen state; they silently proceed as if confirmed.

The stub returns `true` with a TODO comment. This is a correctness hole: any plan depending on visual confirmation will proceed regardless of actual screen state. The audit trail and plan doc will show the step succeeded even if the condition was never verified.

**Fix (Phase 4+):** Implement via `display.Capturer.CaptureAndDelete()` + LLM vision API call with `rc.Target` as the confirmation prompt.

### Issue 4: Container executor ignores `step.Command[]`, uses legacy NL extraction
**File:** `internal/executor/container.go:87–106`  
**Severity:** High — the container executor was not updated alongside the local executor in Phase 1A. It still calls `extractCommand(desc, scope)` and `buildContainerShellArgs(desc, scope)`, ignoring the `command []string` field that the Planner now emits. Any plan step using `executor: container` will silently use the wrong command.

**Fix:** Update `runShellInContainer()` and `executeGitInContainer()` to use `step.Command[]` the same way `executeShell()` and `executeGit()` do in `local.go`.

### Issue 5: `buildContainerShellArgs` grep pattern hardcoded to `"TODO"`
**File:** `internal/executor/container.go:158`  
**Severity:** High — every grep run inside a container searches for the literal string "TODO".

```go
pattern := "TODO"
```

**Fix:** This function should be deleted and replaced with direct `step.Command[]` use (see Issue 4).

### Issue 6: `executePackage()` ignores `step.Command[]`, re-derives from scope
**File:** `internal/executor/local.go:346–383`  
**Severity:** High — package management commands are re-constructed from the capability scope, ignoring what the Planner actually emitted. Two specific breakages:

- `apt:update` scope → parsed as `action=install, pkg=update` → runs `apt-get -y install update` (wrong)
- `apt:upgrade` scope → same bug → runs `apt-get -y install upgrade` (wrong)

The Planner emits `["sudo", "apt-get", "update"]` in `Command`, but the executor throws this away and builds its own command from scope parsing.

**Fix:** When `step.Command` is non-empty, use it directly via `exec.Command(step.Command[0], step.Command[1:]...)`. Fall back to scope-derived command only when `Command` is empty.

### Issue 7: `execute:schedule` not mapped in `AllowlistConfig` struct
**File:** `internal/capability/registry.go` (AllowlistConfig struct)  
**Severity:** High — the `execute:schedule` section in `config/allowlist.yaml` is silently ignored because `AllowlistConfig` has no corresponding field. Any step using `execute:schedule` will fail the allowlist check even though the capability is listed as allowed.

**Fix:** Add `ExecuteSchedule []string \`yaml:"execute:schedule"\`` to `AllowlistConfig` and wire it into `LoadAllowlist`.

---

## Medium

### Issue 8: `display/atspi.go:Click()` always returns an error
**File:** `internal/display/atspi.go:134–146`  
**Severity:** Medium — any plan step using `atspi:click` always fails regardless of whether the element exists.

The method finds the element but then returns an error saying "not yet fully implemented." The object path needed to invoke `DoAction` is not returned by `FindElement`.

**Fix (Phase 4+):** `Element` struct needs an `ObjectPath` field populated by `FindElement`. Then `Click()` can call `org.a11y.atspi.Action.DoAction(0)` via gdbus.

### Issue 9: `searchAccessibleTree()` is substring match on raw D-Bus text, not a real AT-SPI walk
**File:** `internal/display/atspi.go:90–116`  
**Severity:** Medium — `FindElement` is unreliable. It matches if the element name appears anywhere in raw gdbus output text, producing false positives and missing correctly-present elements that don't appear in the root children listing.

**Fix (Phase 4+):** Implement a real recursive AT-SPI tree walk using the `org.a11y.atspi.Accessible.GetChildren` call per node.

### Issue 10: `resumeSession()` ignores parsed plan state, restarts pipeline from scratch
**File:** `cmd/hyperi/main.go:319–336`  
**Severity:** Medium — `hyperi session resume <id>` runs the full pipeline again (Intent → Planner → Adversarial → Arbiter → Execute) from scratch. The parsed `planState` variable at line 327 is read but never used to inform where to resume from. All the Phase 1B work for crash recovery (`NextPendingStep`, `NextPendingStage`) is implemented in `internal/plan/parser.go` but never called from `resumeSession`.

**Fix:** Wire `plan.ParsePlanDoc` result into the runner to skip completed stages/steps.

### Issue 11: Manifest prefix match can over-match without trailing slash
**File:** `internal/manifest/manifest.go:152`  

```go
strings.HasPrefix(path, k+"/") || strings.HasPrefix(path, k)
```

The second clause matches `/etcother/nginx.conf` against the key `/etc`. Should be `strings.HasPrefix(path, k+"/") || path == k`.

---

## Low

### Issue 12: Split channel ownership on approval reply channel
**File:** `internal/shell/model.go:683–687`  

The TUI `handleApprovalResponse` sends on `ap.replyCh` directly then calls `close(ap.replyCh)`. The `bus.ApprovalPayload.Respond()` method also sends and closes the same channel. Channel close ownership is split across two code paths; the `recover()` guard prevents a panic but the pattern is fragile. The model should call `ap.Respond(approved)` instead of manually sending and closing.

### Issue 13: `strings.Title` deprecated
**File:** `internal/plan/writer.go:379`  
`strings.Title` is deprecated since Go 1.18 (incorrect Unicode handling). Replace with `golang.org/x/text/cases.Title(language.English).String(stage)` or a simple manual map of stage names.

### Issue 14: Real process exit code not captured in plan doc
**File:** `internal/plan/writer.go:383`  
`exitCode()` returns `0` or `1` — always. The actual OS exit code from `ExecutionResult` is not captured. Distinguishing `exit 127` (command not found) from `exit 1` (general failure) in the plan doc is impossible.

**Fix:** Add `ExitCode int` to `types.ExecutionResult` and populate it in `local.go`; pass through to `WriteStepResult`.

### Issue 15: `handleIndex` in `ui/server.go` is dead code
**File:** `internal/ui/server.go:87–93`  
Defined but never registered as an HTTP handler. `Start()` registers `/` directly. Remove or register.

### Issue 16: `handleUserInput` in `ui/server.go` doesn't invoke the pipeline
**File:** `internal/ui/server.go:218–237`  
Web UI is post-v1 deferred, but the handler just broadcasts a "processing" status without calling the agent pipeline. Noted here for when the web UI phase is planned.

### Issue 17: `ui/capture.go` uses `sh -c` with pipe
**File:** `internal/ui/capture.go:32`  
Uses `exec.Command("sh", "-c", "grim -t png - | base64 -w 0")` — inconsistent with the project-wide "no shell interpolation" principle enforced by `CommandValidator`. Should use two separate exec calls or pipe via Go's `io.Pipe`.

### Issue 18: `godotenv.Load()` error silently ignored
**File:** `cmd/hyperi/main.go:33`  
A malformed `.env` file will not produce any diagnostic. Non-blocking in practice but worth logging.

### Issue 19: `registry.SaveGrants()` write error silently ignored
**File:** `internal/capability/registry.go` (around `SaveGrants`)  
`os.WriteFile` error is discarded. A failed write loses all runtime capability grants silently.

### Issue 20: Legacy NL extraction fallback still present in `executeShell()`
**File:** `internal/executor/local.go:307–343`  
The Phase 1A cleanup note says "to be removed" but it remains. Dead weight — the Planner validation at `planner.go:143–149` ensures `Command` is never empty for shell steps.

### Issue 21: `processRunning()` in `shell.go` is Linux-only with no build tag
**File:** `internal/shell/shell.go:176–179`  
Checks `/proc/<pid>` — Linux-specific. No `//go:build linux` tag. On macOS/Windows builds, stale foreground locks would never be detected.

---

## Missing Test Coverage

### Issue 22: Zero test coverage on Intent, Planner, Adversarial agents
**File:** `internal/agents/`  
All three LLM-calling agents have no test files. Mock LLM responses should be used to test JSON parsing, validation, and error handling paths.

### Issue 23: Zero test coverage on executor (local + container)
**File:** `internal/executor/`  
The most critical runtime path — dispatch, retry, ReadyCondition polling, on_failure policy — has no tests.

### Issue 24: Zero test coverage on config package
**File:** `internal/config/`  
`Load`, `Save`, `Defaults`, round-trip behaviour — untested.

### Issue 25: Zero test coverage on TUI shell and pipeline runner
**File:** `internal/shell/`  
TUI model and pipeline runner have no tests.

### Issue 26: `plan/writer.go` has no unit tests
**File:** `internal/plan/writer.go`  
Tasks.md explicitly listed writer unit tests as a Phase 1B deliverable. Not done.

### Issue 27: `capability/enforcer_test.go` missing struct-field assertion
**File:** `internal/capability/enforcer_test.go`  
`AsCapabilityNotGranted()` helper has no test coverage for the `Capability` field value.

---

## Known / Already Documented

### Issue 28: Voice push-to-talk hint not visible in TUI
*(Originally Issue 1 — preserved for continuity)*  
See original description above.

### Issue 29: `hyperi session start` allowlist path is relative
*(Originally Issue 2 — preserved for continuity)*  
`loadRegistry("")` uses `filepath.Join("config", "allowlist.yaml")` — relative to `os.Getwd()`. Running from any directory other than repo root silently drops all allowlist entries.

---

## Post-v1 (Not bugs, tracked in `post-v1.md`)

- Web UI (`internal/ui/`) — deferred; not wired to pipeline
- Multi-user sessions — deferred
- Automatic trust escalation — deferred
- Rollback / undo — deferred
- `ReadyCondition` as first-class step type — deferred
- Per-capability-domain autonomy levels — deferred
- `vision:confirms` full implementation (LLM vision API) — deferred
- AT-SPI `Click()` full implementation — deferred
- Real AT-SPI tree walk — deferred
- ISO build artifact signing and hosting — deferred
