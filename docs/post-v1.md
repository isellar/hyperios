# HyperiOS — Post-v1 Backlog

Decisions and features explicitly deferred from v1. Each item records *why* it was deferred, what was decided in its place for v1, and what would need to be true before picking it up.

Items are grouped by theme, not priority. Priority should be set when planning the next phase.

---

## Interfaces

### Web UI — Remote Access Interface
**Deferred from:** Critique 4  
**v1 decision:** TUI is the primary and only interface. SSH into the TUI is the workaround for remote access.  
**What it is:** A browser-based interface for accessing a running hyperi session from another device. Purpose is remote management only — not a richer local display.  
**Why deferred:** The existing `ui/server.go` and `ui/frontend/` are carry-over scaffolding from a prior iteration of the project. Building it properly requires the TUI and event bus to be stable first so the web UI can consume the same event stream.  
**Prerequisites before picking up:**
- TUI (Phase 2) complete and stable
- Event bus (`internal/bus/`) implemented and proven
- Security model for remote access defined (auth, TLS, network binding)

**Open decision when this phase is planned:**  
Reconsider React vs a lighter alternative (HTMX or Go template server). The use case is a terminal emulator with richer rendering — React's infrastructure overhead may not be justified. Don't let the existing `.gitkeep` placeholder make the decision by inertia.

---

### Multi-User Sessions
**Deferred from:** Critique 4, Session Model  
**v1 decision:** Single-user. One foreground session at a time. Background sessions run under the same user.  
**What it is:** Each OS user gets their own session state, capability grants, autonomy level, and plan doc history stored under their home directory. Multiple users can interact with the agent independently.  
**Why deferred:** Single-user is simpler and sufficient for v1 (personal laptop / single-operator server). The complexity of concurrent sessions, per-user capability isolation, and per-user audit trails is not justified until the single-user model is proven.  
**Why it's feasible later:** The OS permission model (`hyperi` user, per-user home directories, PAM) is structured to support this. Nothing in the v1 session model prevents it — it's additive, not a redesign.  
**Prerequisites before picking up:**
- v1 session model proven stable
- Clear use case established (shared server? family device? team deployment?)
- Per-user config, capability grants, and autonomy levels designed

---

## Agent Pipeline

### `ReadyCondition` as a First-Class Step Type
**Deferred from:** ActionStep Data Model, Critique 3  
**v1 decision:** `ReadyCondition` is an optional field on `ActionStep`. The wait is coupled to the step that triggers it.  
**What it is:** A separate `WaitStep` type in the plan that appears as a discrete node in the dependency graph. The Adversarial Agent can reason about it independently. It appears in the plan doc and audit trail as its own step with its own verdict.  
**Why deferred:** Keeping it as a field is simpler for the Planner to emit and the executor to handle. The architectural benefit (independent Adversarial Agent reasoning on wait conditions) doesn't justify the added Planner prompt complexity for v1.  
**When to promote:** If the Adversarial Agent needs to reason about wait conditions independently — e.g. "what happens if this service never becomes active?" as a distinct risk from the step that restarts it. Watch for cases where `ReadyCondition` timeouts are causing unexpected halts that a smarter risk assessment could have predicted.

---

### Automatic Trust Accumulation (Autonomy Level Escalation)
**Deferred from:** Critique 5  
**v1 decision:** Autonomy level is set by explicit user action only. The system never increases it automatically.  
**What it is:** The agent earns higher autonomy levels over time based on a track record of successful, safe executions. Criteria might include: N successful executions of a capability type without failures, no adversarial flags in the last M sessions, no user overrides of modified verdicts in a domain.  
**Why deferred:** Designing automatic trust escalation requires a track record to base it on and clear, auditable criteria for what counts as "earned." Doing it without those inputs risks creating a system that escalates trust based on superficial signals (e.g. running `ls` 500 times successfully).  
**Prerequisites before picking up:**
- Sufficient execution history to analyze (months of real use)
- Clear criteria defined for each level transition
- Audit trail analysis tooling to verify criteria are met
- Explicit user consent mechanism before any automatic escalation takes effect

---

### Per-Capability-Domain Autonomy Levels
**Deferred from:** Critique 5  
**v1 decision:** Global autonomy level with per-session override. One level applies to all capability types.  
**What it is:** Different autonomy levels for different capability domains. Shell commands might be level 3 (trusted), package management at level 1 (always ask), config writes at level 2.  
**Why deferred:** The capability allowlist already provides per-capability-type control at the binary yes/no level. Adding per-domain autonomy levels on top is a significant complexity increase for users to configure and reason about. Not justified for v1.  
**When to reconsider:** If users consistently find the global level too coarse — e.g. they want the agent to freely run read-only shell commands but always ask before touching packages. Watch for explicit user requests for this granularity.

---

## Display & Automation

### Vision Model Screen Understanding (Display Layer 3)
**Deferred from:** Critique 3, Phase 4  
**v1 decision:** Layers 1 (CLI/API) and 2 (AT-SPI) are Phase 4 scope. Vision model is a later capability within Phase 4.  
**What it is:** Taking a `grim` screenshot, encoding it, and sending it to the LLM vision API to understand screen state — identify elements, read content, confirm conditions. Covers Electron apps and browser content that AT-SPI cannot reach.  
**Why deferred within Phase 4:** Vision model interaction requires network access and adds API latency per interaction. Establish CLI and AT-SPI foundations first; add vision as a fallback only for apps those layers cannot handle.  
**Dependencies:** Phase 4 AT-SPI integration complete; `ReadyCondition` with `vision:confirms` type implemented; grim capture pipeline working.

### Raw Input Injection via ydotool (Display Layer 4)
**Deferred from:** Critique 3, Phase 4  
**v1 decision:** ydotool is last-resort only, after CLI/API, AT-SPI, and vision model have all failed or been exhausted.  
**What it is:** Raw mouse/keyboard coordinate injection via ydotool. Fragile by nature — depends on exact screen layout, no semantic understanding.  
**Why deferred:** Should almost never be used if the layered interaction model is working correctly. Only implement once the higher layers are proven insufficient for a real use case.

### wlroots Custom Compositor
**Deferred from:** Display Architecture  
**v1 decision:** sway is the Phase 4 compositor. Chosen for Wayland-native IPC and i3-compatible scriptability.  
**What it is:** A custom compositor built directly on wlroots (the library sway itself uses), giving full control over window management, input handling, and protocol extensions.  
**Why deferred:** sway covers Phase 4 requirements well. A custom compositor is only worth the investment if sway's IPC model proves too constraining — e.g. if the agent needs protocol-level access to input events, custom window decorations, or compositor-side rendering.

---

## Infrastructure

### Docker/Namespace-Aware Manifest Updates
**Deferred from:** System Manifest  
**v1 decision:** inotify watcher does not catch changes made from within Docker containers or other Linux namespaces.  
**What it is:** Manifest update hooks that are aware of container boundaries — watching for filesystem changes made by Docker containers, flatpak sandboxes, or other namespaced processes.  
**Why deferred:** Out of scope for v1 where the primary execution environment is the host system. The gap is documented and accepted.  
**Prerequisites:** Clear use case for container-aware manifest tracking; likely tied to the container executor (`executor/container.go`) being actively used.

### Automatic Trust Accumulation Infrastructure
*(See Agent Pipeline section above — same item, cross-referenced here for infrastructure implications)*  
The audit trail and plan doc formats are designed to support retrospective analysis. When this is revisited, the raw data will be available.

---

## Security

### Web UI Authentication & TLS
**Deferred from:** Critique 4  
**v1 decision:** Web UI itself is deferred. When it arrives, auth and TLS must be designed before the first line of web UI code is written — not added later.  
**What it needs:** At minimum: a shared secret or token for local network access; TLS for any remote access; consider whether to use the OS user's session (PAM) as the authentication mechanism.  
**Hard requirement:** The web UI must not be accessible on the network without authentication. `http.ListenAndServe` on `0.0.0.0` with no auth is not acceptable for a remote access interface.

---

## Rollback & Undo

### Position A — Planner-Generated Rollback Steps
**Deferred from:** Critique 6  
**v1 decision:** Position C — explicit scope limitation; user is the rollback mechanism; plan doc is the undo reference.  
**What it is:** The Planner emits a `RollbackStep` paired with each reversible `ActionStep` when generating the plan. Rollback steps sit unused in the plan doc unless triggered. On partial failure, the executor runs rollback steps for completed reversible steps in reverse order. The Arbiter and Adversarial Agent review rollback steps at plan time — before anything executes — so the user can see what reversal would look like before approving the forward plan.  
**Why deferred:** Requires the Planner to reliably generate correct rollback commands for every reversible capability type. For some this is trivial (`apt install nginx` → `apt remove nginx`). For others — particularly config writes — it is not, because the previous state must be known at plan time. Prompt engineering challenge that needs real-scenario testing before it can be trusted.  
**Capability types where this works well:** `execute:package` (logical inverse is well-defined), `execute:process` (start/stop are inverses of each other), `execute:display` (window state is easily restored).

### Position B — Executor-Maintained Undo Stack
**Deferred from:** Critique 6  
**v1 decision:** Position C — explicit scope limitation; user is the rollback mechanism; plan doc is the undo reference.  
**What it is:** The executor captures actual system state before making changes — reads the current config file content before overwriting it, checks whether a package was already installed before installing it. On failure or explicit user request ("undo last action"), the executor walks the undo stack in reverse, restoring captured state.  
**Why deferred:** Requires correct undo implementation for every capability type. A buggy undo that claims to have reversed something but didn't is worse than no undo. Needs thorough testing across real failure scenarios before it can be trusted.  
**Capability types where this works well:** `execute:config` (capture file content before write), `read:file` (no-op, nothing to undo), any step that modifies a file where the previous content is the correct rollback.

### Hybrid Rollback (Likely Correct Long-Term Approach)
**What it is:** A and B are complementary — each is better suited to different capability types. A hybrid uses whichever source has better information:
- **Planner-generated rollback** for capability types where the logical inverse is well-defined at plan time: `execute:package`, `execute:process`, `execute:git`
- **Executor-captured state** for capability types where the actual prior state is what matters: `execute:config` (previous file content), `execute:network` (previous network config snapshot)

The plan doc already records every executed command — the hybrid approach adds either a `rollback_command` field on `ActionStep` (from the Planner) or a `prior_state` capture in the execution record (from the executor), depending on capability type. Both end up in the plan doc and are available for automated or manual reversal.

**Prerequisites before any rollback work:**
- v1 running against real scenarios long enough to understand which failure modes actually occur
- Clear inventory of which capability types need which rollback approach
- Test suite covering rollback correctness for each capability type before shipping

---

## Known Bugs (Inherited, Not Yet Fixed)

These are carry-overs from the prior "Uplink" codebase. They are not post-v1 features — they are bugs that need fixing in Phase 1. Listed here as a reminder that they exist and shouldn't be forgotten during planning.

1. `registry.Check()` glob matching — `filepath.Match` does not support `**`; `{workspace}/**` won't match nested paths. Fix: use a proper glob library or recursive walk.
2. `ErrCapabilityNotGranted` sentinel — call sites use `errors.Is`; should use `errors.As` because it is a struct pointer type with a `Capability` field.
3. `session.Progress()` overcounts — `MarkCompleted()` appends unconditionally; calling it twice with the same step ID inflates `len(Completed)`. Fix: deduplicate on write or check before append.
4. `executeNetworkOutbound()` stub — always returns success without making HTTP calls.
5. `executeConfig()` stub — returns placeholder without writing files.
