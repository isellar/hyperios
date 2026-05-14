# HyperiOS — Long-Range Vision

*A working document. Iterate freely. Precision matters more than completeness here.*

---

## The Core Thesis

HyperiOS is not an AI assistant bolted onto a conventional OS. It is an operating system where the agent is the primary interface and all other software is infrastructure. The user's relationship with the computer changes: they express intent, not instructions. The system reasons about that intent, proposes a path, validates it, executes it, and learns from the outcome — all within a safety architecture that is structural, not behavioral.

The long-range bet is that this model — intent-first, agent-native, architecturally safe — is the right foundation for what personal computing becomes when AI is capable enough to act, not just advise.

---

## Module Map

The system decomposes into six first-class modules. The current implementation conflates some of these; separating them cleanly is the architectural direction.

```
┌─────────────────────────────────────────────────────────────────┐
│                        HyperiOS                                 │
│                                                                 │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐   │
│  │  INPUT/  │   │ CONTEXT  │   │PLANNING/ │   │GOVERNOR  │   │
│  │ OUTPUT / │◄──►          │◄──►THINKING/ │◄──►(Adversary│   │
│  │  ENV     │   │(Memory & │   │EXECUTION │   │+ Arbiter)│   │
│  └──────────┘   │Marshalling│  └──────────┘   └──────────┘   │
│                 └──────────┘                                   │
│                      ▲                ▲                         │
│                      │                │                         │
│                 ┌──────────┐   ┌──────────┐                   │
│                 │IMPROVEMENT│  │(all modules│                  │
│                 │          │   │ expose     │                  │
│                 │          │   │ stable API)│                  │
│                 └──────────┘   └──────────┘                   │
└─────────────────────────────────────────────────────────────────┘
```

### 1. Input / Output / Environment (I/O/E)

*What the system perceives and how it communicates back.*

**Current state:** Text input via TUI (Phase 2 stub), voice stub (Phase 3), display management stub (Phase 4).

**Scope:**
- All input channels: text (TUI, web), voice (STT), vision (grim + vision model), system events (inotify, systemd, scheduler)
- All output channels: text rendering (TUI, web), voice (TTS), display management (swaymsg, AT-SPI, ydotool)
- Environment sensing: filesystem state, process state, network state, screen state

**Key insight:** I/O/E is the most hardware-coupled module and the most likely to change across deployment contexts (headless server vs. desktop vs. embedded). It should expose a stable sensor/actuator interface that the rest of the system programs against, not concrete implementations.

**Missing from current taxonomy you identified:** The *sensor* side — not just UI rendering but the OS-level observation pipeline (inotify, `/proc`, sway events, service state changes). The system needs to perceive the world proactively, not only in response to user input.

### 2. Context (Memory & Marshalling)

*What the system knows and how it makes that knowledge available.*

**Current state:** System manifest (paths + services), session state, plan doc as execution record, audit log. All are read-at-need, not a unified context layer.

**Scope:**
- Short-term context: current session, active plan, recent events
- Long-term memory: execution history, learned preferences, per-task outcomes, user patterns
- World model: system manifest, service topology, installed packages, filesystem sensitivity map
- Marshalling: assembling the right subset of context for each agent and module — not flooding every LLM call with everything, but not starving them either

**Key insight:** Context is currently a passive store that agents query. The long-range vision is a context layer that actively maintains relevance — forgetting irrelevant history, surfacing relevant patterns, and pre-loading context before agents need it.

**What's missing now:** Long-term memory, semantic indexing of past executions, preference inference. The audit log is the raw material; the context module is what processes it into usable knowledge.

### 3. Planning / Thinking / Execution

*How the system reasons about what to do and then does it.*

**Current state:** Intent Agent → Planner Agent → Executor. Well-implemented, literal commands, no shell interpolation, retry/replan/skip/halt policies.

**Scope:**
- Intent parsing: NL → structured goal graph
- Planning: goal graph → action sequence with failure policies
- Execution: action sequence → real system changes with audit trail
- The re-plan loop: failure-aware iteration

**Key insight:** Planning and execution are already well-scoped. The boundary to sharpen is between thinking (no side effects — reasoning happens here) and execution (side effects — deterministic, auditable, capability-gated). The Planner never executes. The Executor never reasons.

**Long-range direction:** The planner eventually needs a richer model of the world — not just "what steps accomplish this goal" but "what are the side effects, what is the expected duration, what is the failure probability, what would I do if each step fails." This is where the context module feeds in: a planner with history makes better plans.

### 4. Governor (Adversary + Arbiter)

*The system's internal check on its own plans.*

**Current state:** Adversarial Agent (LLM, finds risks) + Policy Arbiter (deterministic, final authority). This is the most architecturally distinct module — it exists to actively distrust the rest of the system.

**Scope:**
- Adversarial Agent: actively seeks failure modes, not helpfulness
- Policy Arbiter: deterministic, non-LLM, autonomy-level-aware, final word
- CommandValidator: structural + allowlist + scope + manifest pre-checks
- Capability system: allowlist, runtime grants, TTL, revocation

**Key insight:** The Governor is the module that makes the rest of the system extensible safely. As Planning/Thinking/Execution becomes more capable, the Governor's job gets harder and more important. The Governor must be independently upgradeable — it should be possible to make the Governor smarter without touching anything else.

**Long-range direction:** The Adversarial Agent eventually reasons about *sequences* of steps and accumulated effects, not just individual steps in isolation. A single step that installs curl is low-risk; a plan that installs curl, creates a cron job, and opens an outbound connection is a different risk profile than its parts suggest. The Governor needs to reason about the plan as a whole, not step-by-step.

### 5. Improvement

*The system's capacity to get better at its own job.*

**Current state:** Not implemented. The audit log and plan docs are the raw material. Nothing processes them.

**Scope:**
- Retrospective analysis: what worked, what failed, what triggered re-plans
- Module-specific improvement: each module (Planner, Governor, Context, I/O/E) exposes a report interface — structured data about what happened — and a configuration/prompt interface — what can be tuned
- Proactive improvement: runs on a schedule, not just after failures
- Improvement of the Improvement module itself: the recursion is deliberate and bounded

**Key insight:** Improvement is the module that distinguishes HyperiOS from a static agent framework. It is what allows the system to compound capability over time. However, it must operate within the same Governor constraints as everything else — the Improvement module cannot grant itself new capabilities, modify the Arbiter's rules, or bypass the allowlist.

**The self-improvement loop (more on this below).**

### 6. Orchestration (implied, currently in main.go)

*How modules are wired together.*

**Current state:** `cmd/hyperi/main.go` — a 1000-line file that orchestrates the full pipeline. This is fine for v1 but becomes a bottleneck as modules evolve independently.

**Long-range direction:** Each module exposes a clean interface. Orchestration becomes thin configuration: "when I/O/E receives text input, hand it to Planning; when Planning produces a plan, send it to Governor; when Governor approves, hand to Executor; publish all events to the bus."

---

## The Self-Improvement Architecture

This is the most important long-range architectural question. How does the system improve itself without breaking itself?

### The Boundary Principle

Modules have well-defined interfaces. Self-improvement that stays inside a module's interface cannot break adjacent modules. This is already partially true: the Planner Agent communicates with the rest of the system only through `ActionPlan`. Improving the Planner (better prompts, better context, different model) cannot affect the Governor or the Executor as long as `ActionPlan` remains valid.

Each module boundary is a safety fence for self-improvement.

### What Can Be Improved

| Module | Improvable Surface | Cannot Change |
|---|---|---|
| Planning | Planner system prompt, context selection, re-plan strategy | `ActionPlan` schema, `ActionStep` fields, `Command[]string` contract |
| Governor (Adversarial) | Adversarial system prompt, risk taxonomy | Arbiter rules, allowlist, autonomy level thresholds |
| Governor (Arbiter) | **Cannot be improved by the agent** — only by explicit human edit | Everything — it is the trust anchor |
| Context | What gets included in LLM context, memory indexing, relevance scoring | The audit log format (append-only, tamper-evident) |
| I/O/E | UI rendering, TTS/STT model selection, display interaction strategy | The event bus interface, sensor/actuator contract |
| Improvement | Improvement strategies, analysis heuristics, scheduling | Cannot grant itself new capabilities or modify the Arbiter |

### The Report + Tune Interface

Every module exposes two things to the Improvement module:
1. **Report:** Structured data about its recent performance. Not raw logs — processed observations. Examples: "Planner produced plans that triggered re-plan in 30% of sessions this week." "Adversarial Agent flagged 12 steps as high-risk; 11 of them were later approved by the user." "CommandValidator rejected 3 structurally invalid commands."
2. **Tune:** A set of configuration handles the Improvement module can adjust. Examples: Planner system prompt fragments, context window size, re-plan budget, Adversarial Agent persona strength.

The Improvement module cannot write arbitrary code, cannot modify Go source, cannot change compiled behavior. It can adjust the parameters within which the system operates.

*Post-v1 extension:* If the system gains the ability to modify its own source code (a significant trust escalation), that capability must be Governor-gated, require explicit human approval per change, and be separately audited. It is architecturally feasible but not a v1 or v2 concern.

### Improvement Trigger Modes

The Improvement module runs in two modes:

| Mode | Trigger | Scope | Governor Gate? |
|---|---|---|---|
| Reactive | Module failure or degraded performance | Specific module that failed | Yes — proposed change goes through Governor |
| Proactive | Scheduled (e.g. weekly) | All modules | Yes — proposed changes go through Governor |

Both modes produce proposed tuning changes, not automatic changes. The Governor reviews them. At low autonomy levels, the user approves them. At high autonomy levels, they apply automatically — but this is a deliberate trust decision the user makes.

### The Recursion Is Real

The Improvement module can propose improvements to itself. This is not exotic — it is just a module like any other, with a Report interface and a Tune interface. The constraint is the same: any proposed change to the Improvement module goes through the Governor, and the Governor's rules cannot be changed by the Improvement module.

The recursion terminates at the Arbiter, which is the only component that is never self-modified.

---

## Module APIs (Draft)

Each module should eventually expose a stable, versioned Go interface. This is the direction, not the current state.

```go
// Every module implements this.
type Module interface {
    // Name returns the module's canonical name for routing and audit.
    Name() string
    // Report returns structured performance data for the Improvement module.
    Report(ctx context.Context, window time.Duration) (ModuleReport, error)
    // Tune applies a proposed configuration change (already Governor-approved).
    Tune(ctx context.Context, change TuningChange) error
    // Health returns current operational status.
    Health() ModuleHealth
}

// The event bus is the primary inter-module communication channel.
// Direct method calls between modules should be the exception, not the rule.
```

**Why event-bus-first:** Direct method calls create tight coupling and make it hard for the Improvement module to observe what's happening between modules. Event bus messages are inherently observable, auditable, and decoupled. Modules that communicate only through the event bus can be upgraded independently.

---

## The Obvious Missing Module: Observation

You asked what was missing from the module taxonomy. The answer is a distinct **Observation** module — separating it from I/O/E.

The current I/O/E module mixes two different concerns:
- **Interaction** (receiving user input, producing user output)
- **Observation** (monitoring system state proactively, without user prompting)

These have very different trust and cadence profiles. User interaction is synchronous and human-paced. System observation is asynchronous, continuous, and potentially high-frequency. Keeping them in one module creates pressure to design I/O/E around the slower, user-paced concern.

A dedicated Observation module:
- Maintains a live model of system state (filesystem, services, processes, network, screen)
- Detects conditions the user cares about (disk usage, service failures, security events)
- Publishes observations to the event bus
- Feeds the Context module with current world state
- Is the data source for the Improvement module's proactive analysis

This is partially implemented in the manifest watcher and scheduler, but it is not yet a coherent module with its own interface.

---

## The Full Long-Range Module Taxonomy

Revised from your initial framing:

| Module | Role | Agentic? | Current Status |
|---|---|---|---|
| I/O (Interaction) | User input/output (TUI, web, voice) | No — it's a channel | Phase 2-3 stubbed |
| Observation | Proactive system sensing (filesystem, services, processes) | No — it's a sensor | Partial (manifest watcher) |
| Context | Memory, world model, context assembly for agents | No — it's a store+index | Partial (manifest, session) |
| Planning | Intent → goal → action sequence | Yes — LLM-driven | Done (Phase 1A) |
| Governor | Adversarial review + deterministic arbitration | Partially (AA is LLM) | Done (Phase 1A) |
| Execution | Capability-gated action, audit trail | No — it's a runtime | Done (Phase 1A) |
| Improvement | Retrospective + proactive module tuning | Yes — LLM-driven | Not started |
| Orchestration | Wiring — event bus, session lifecycle | No — it's plumbing | In main.go (Phase 1) |

---

## Modularization Path from Here

The current codebase is not poorly structured — the packages are mostly right. The gap is that package boundaries are enforced by Go's type system but not by clean interface contracts. The internal packages know too much about each other.

**Iteration 1 (near-term, v0.2-v0.3):** Define explicit Go interfaces for each module. The implementations stay the same. This makes the seams visible without requiring a rewrite.

**Iteration 2 (medium-term, v0.4-v0.5):** Move inter-module communication to the event bus. Reduce direct method calls between packages. Each module becomes a subscriber/publisher, not a direct caller.

**Iteration 3 (longer-term, v1.x):** Introduce the Module interface with Report + Tune. The Improvement module can now talk to any other module through a stable API. This is when self-improvement becomes real.

**Iteration 4 (post-v1):** Modules become independently deployable. The Improvement module could run on a separate process or device. The Governor could be a separate verified binary. This is the distributed-system direction and is not needed for v1.

---

## Open Questions

These are the questions that will most shape the long-range architecture. They do not have answers yet — they need real usage data.

**1. What is the right memory model?**
The audit log and plan docs are append-only raw history. Long-range memory requires something indexed and queryable. Options: vector embedding of plan docs (semantic search over past actions), structured key-value store for facts, graph model for relationships between actions and outcomes. The right answer depends on what kinds of questions the system needs to ask about its own history.

**2. How fast should the Improvement module act?**
A weekly improvement cycle is conservative and safe. A continuous improvement cycle (after every session) is riskier but compounds faster. The right cadence is probably adaptive — faster improvement on high-failure areas, slower improvement on stable areas.

**3. What is the trust model for the Improvement module itself?**
The Improvement module has a privileged view of the system's internals. It can read all reports, propose changes to all other modules. This makes it a high-value attack target and a high-risk component to get wrong. Should it run with the same autonomy level as user-directed sessions, or should it have its own, more conservative autonomy level?

**4. How does the system handle conflicting goals across sessions?**
A background session scheduled to "keep nginx running" and a foreground session that removes nginx for troubleshooting are in conflict. The current model (foreground session wins, background sessions are background) handles the mechanics, but the semantic conflict is not resolved. This is a Context + Orchestration problem.

**5. Where does the modular boundary sit for the Governor?**
The Adversarial Agent is an LLM and can be improved. The Arbiter is deterministic and should not be LLM-modified. But they are currently co-located in the Governor. As the system matures, should they be separate modules? The Arbiter is a trust anchor — perhaps it warrants being a signed binary that the rest of the system cannot modify even in principle.

**6. What does "improving the Planner" actually mean in practice?**
Prompt tuning is the obvious answer, but prompts are fragile and hard to version. Better long-term approaches might include: fine-tuning on the project's own plan docs (learning from its own successful plans), structured retrieval of relevant prior plans as context, or a library of capability-specific plan templates that get selected rather than generated from scratch.

---

## What This Is Not

Clarifying the boundaries:

- **Not a general-purpose AI agent platform.** It is a Linux distribution. The OS context (sway, systemd, apt, inotify) is not incidental — it is the point. Generic agent frameworks (LangChain, etc.) are not the model.

- **Not a user-configures-everything system.** The goal is a system that improves itself. The user should have to configure less over time, not more.

- **Not a jailbreak.** The Governor and the OS security model are not obstacles to be overcome — they are features. Self-improvement that defeats safety constraints is not improvement.

- **Not a cloud agent.** Local-first. The Anthropic API is the current LLM backend, but the system should be capable of running with a local model for everything except the most demanding reasoning tasks. This is a direction, not a current requirement.

---

## First Principles Check

Every architectural decision in this vision should be traceable to one of these:

1. **Safety is architectural, not behavioral.** The Arbiter cannot be LLM-modified. The OS permission model is below the agent layer.

2. **Modularity enables safe self-improvement.** Interfaces are the fences. Improvement that respects module boundaries cannot break adjacent modules.

3. **The audit trail is the ground truth.** Every action, every observation, every improvement is recorded. The system cannot lie about what it did.

4. **The user is the ultimate authority.** Autonomy level is set by explicit human action. Automatic trust escalation requires explicit human consent. The Improvement module proposes; it does not decide.

5. **Local-first, not cloud-dependent.** The system should degrade gracefully without network access, not fail silently.
