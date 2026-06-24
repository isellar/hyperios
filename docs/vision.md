# HyperiOS — Long-Range Vision

*A working document. Iterate freely. Precision matters more than completeness here.*

---

## The Core Thesis

HyperiOS is not an AI assistant bolted onto a conventional OS. It is an operating system where the agent is the primary interface and all other software is infrastructure. The user's relationship with the computer changes: they express goals, not instructions. The system reasons about those goals, refines them with the user, breaks them down into actionable pieces, validates them against safety directives, prioritizes and delegates to agents, executes them, and learns from the outcome — all within a safety architecture that is structural, not behavioral.

The long-range bet is that this model — goal-driven, agent-native, architecturally safe — is the right foundation for what personal computing becomes when AI is capable enough to act, not just advise.

**Two key distinctions:**
- **Goals vs. Tools:** The user defines goals (what to achieve). The agent owns tools (how to achieve it). The agent can build, restructure, and optimize its own tools, memory, and internal systems — but cannot modify the safety architecture or the user's data.
- **Directives vs. Goals:** Directives are hierarchical rules that constrain behavior (safety > convenience). Goals are what the user wants to achieve. The agent pursues goals within directive constraints. Directives have no states; goals have a lifecycle (Refining, Active, Done, Blocked, Cancelled).

---

## Module Map

The system decomposes into five first-class modules. The current implementation conflates some of these; separating them cleanly is the architectural direction.

```
┌───────────────────────────────────────────────────────────────────────────────┐
│                              HyperiOS                                         │
│                                                                               │
│  ┌──────────────────────────────────────────────┐   ┌──────────────────────┐ │
│  │               GOAL FULFILLMENT                │   │                      │ │
│  │     (Refinement + Breakdown + User Inter.)    │──►│                      │ │
│  └──────────────────────────────────────────────┘   │        MEMORY        │ │
│                         │                           │                      │ │
│                         ▼                           │   (Context + Store   │ │
│  ┌──────────────────────────────────────────────┐   │    + Recall)         │ │
│  │                  GOVERNOR                    │   │                      │ │
│  │  (Adversarial + Arbiter + Directives +       │──►│                      │ │
│  │          Executor + Tool Authorization)      │   │                      │ │
│  └──────────────────────────────────────────────┘   │                      │ │
│                         │                           │                      │ │
│                         ▼                           │                      │ │
│  ┌──────────────────────────────────────────────┐   │                      │ │
│  │                 PROCESSOR                    │   │                      │ │
│  │         (Prioritization + Delegation)        │   │                      │ │
│  └──────────────────────────────────────────────┘   │                      │ │
│                         │                           │                      │ │
│                         ▼                           │                      │ │
│  ┌──────────────────────────────────────────────┐   │                      │ │
│  │             SELF-IMPROVEMENT                 │   │                      │ │
│  │   (Analysis + Pattern Detection + Improve.)  │──►│                      │ │
│  └──────────────────────────────────────────────┘   └──────────────────────┘ │
│                                                                               │
└───────────────────────────────────────────────────────────────────────────────┘
```

### 1. Goal Fulfillment

*Interacts with the user to refine, break down, and track goals.*

**Current state:** Intent Agent converts NL to GoalGraph. Planner breaks goals into steps. Session-scoped, not persistent.

**Scope:**
- Goal refinement: interact with user to clarify intent, gather context, resolve ambiguities
- Goal breakdown: decompose goals into sub-goals that can be delegated to agents
- Goal tracking: maintain goal state (Refining, Active, Done, Blocked, Cancelled)
- User interaction: the only module that directly communicates with the user about goals
- Goal completion verification: test and verify that goals are actually completed before marking Done

**Key insight:** Goal Fulfillment is the user-facing interface to the goal system. It translates user intent into structured goals, breaks them down into actionable pieces, and reports results back to the user. It does not execute anything — it orchestrates the goal lifecycle.

**Goal states:**
- **Refining:** Goal is being clarified with the user. Context is being gathered. Goal agents are actively seeking more information. Not yet ready for execution.
- **Active:** Goal has been refined and broken down. Goal agents are executing. No longer actively seeking more information from the user.
- **Done:** Goal has been completed and verified. No further action needed.
- **Blocked:** Goal cannot proceed due to a dependency, missing information, or user constraint. Requires user input or resolution.
- **Cancelled:** Goal has been explicitly cancelled by the user or abandoned due to changing priorities.

**Goal refinement:**
During the Refining phase, goal agents can use the Processor to look up information. For example, a goal agent might say "the first step is to do a web search to find more info on the problem" and then continue from there when the results come back. This allows the agent to gather context autonomously before asking the user for clarification.

**Goal transition from Refining to Active:**
A goal transitions from Refining to Active once the goal agents are no longer actively seeking more information from the user. During Refining, goal agents can use the Processor to look up information (e.g., "do a web search to find more info on the problem"). Once they have enough context and are ready to execute, the goal moves to Active.

**Goal breakdown:**
Goals are broken down continuously into sub-goals. A goal like "organize my photographs" might break down into:
- "Scan photo library structure"
- "Identify user's photo organization preferences"
- "Propose organization scheme"
- "Implement folder structure"
- "Create indexing system"
- "Set up maintenance workflow"

Each sub-goal can be further broken down until it reaches a granularity that can be delegated to a single agent for execution. The breakdown is LLM-driven, not hardcoded — the agent decides how to decompose based on context.

**Directives vs Goals:**
Goal Fulfillment operates under two types of constraints:
- **Goals:** What the user wants to achieve (e.g., "organize my photographs"). These have states and are tracked through the lifecycle.
- **Directives:** How the system should behave (e.g., "do not harm the user," "be concise in communication"). These are hierarchical rules that constrain behavior but are not goals to be achieved. They do not have states — they are always active.

Directives are hierarchical: "do not harm the user" takes precedence over "be witty and tell jokes." The agent must respect directive priorities when pursuing goals.

### 2. Governor

*The system's internal check on its own plans. Enforces safety, directive compliance, and tool authorization.*

**Current state:** Adversarial Agent (LLM, finds risks) + Policy Arbiter (deterministic, final authority) + Executor (capability-gated execution). This is the most architecturally distinct module — it exists to actively distrust the rest of the system.

**Scope:**
- Adversarial Agent: actively seeks failure modes, not helpfulness
- Policy Arbiter: deterministic, non-LLM, autonomy-level-aware, final word
- CommandValidator: structural + allowlist + scope + manifest pre-checks
- Capability system: allowlist, runtime grants, TTL, revocation
- Directive enforcement: ensures goals and plans comply with system directives
- **Executor**: capability-gated execution on the OS (moved from separate module)
- **Tool authorization**: manages which tools/capabilities are authorized for execution, with user approval for sensitive operations

**Key insight:** The Governor is the module that makes the rest of the system extensible safely. As Goal Fulfillment and Processor become more capable, the Governor's job gets harder and more important. The Governor must be independently upgradeable — it should be possible to make the Governor smarter without touching anything else.

**Long-range direction:** The Adversarial Agent eventually reasons about *sequences* of steps and accumulated effects, not just individual steps in isolation. A single step that installs curl is low-risk; a plan that installs curl, creates a cron job, and opens an outbound connection is a different risk profile than its parts suggest. The Governor needs to reason about the plan as a whole, not step-by-step.

**Directive enforcement:**
The Governor ensures that all goals and plans comply with directives. When a goal is proposed, the Governor checks:
- Does this goal violate any directives?
- Does the plan to achieve this goal violate any directives?
- Are directive priorities being respected (e.g., safety over convenience)?

Directive violations result in the goal being blocked or modified before it reaches the Processor.

**Directives storage:**
Directives are stored in two config files:
- **Immutable directives** (`config/directives-immutable.yaml`): Highest priority, cannot be changed by the agent (e.g., "do not harm the user")
- **Mutable directives** (`config/directives-mutable.yaml`): Can be modified through the goal system (e.g., "be concise in communication")

Both files are loaded into all agent instructions, even if not all directives are relevant to all tasks. They are considered important enough to always be considered.

**Tool authorization:**
When a goal requires tools or capabilities that need user approval, the Governor fires a popup allowing the user to select an authorization option:
- **Always**: Authorize this tool/capability for all future goals
- **This session only**: Authorize for the current session
- **This request only**: Authorize for this specific goal execution
- **No**: Deny this request
- **Never**: Deny and prevent future requests for this tool/capability

Once authorized, the goal agent has explicit access to those tools. If the goal agent tries to use unauthorized tools, it fails and loops back to Goal Fulfillment for re-evaluation.

**Executor integration:**
The Executor is now part of the Governor module. When a goal agent needs to execute actions, it requests tool authorization from the Governor. The Governor checks:
- Does this tool/capability require user approval?
- Is it already authorized (always, this session, this request)?
- Does it violate any directives?

If authorization is needed, the Governor fires a popup for user approval. Once authorized, the goal agent has explicit access to those tools. The Executor then performs the capability-gated execution on the OS.

If the goal agent doesn't have the right tools authorized, it fails and loops back to Goal Fulfillment for re-evaluation. This ensures that goal agents only use authorized tools and capabilities.

### 3. Processor

*Decides what to work on and delegates to agents.*

**Current state:** Not implemented. Goals are session-scoped and executed in order.

**Scope:**
- Prioritization: decide which goals to work on based on directives, goal state, timeline, dependencies
- Delegation: spawn autonomous agents to execute sub-goals
- Agent coordination: manage concurrent agents, resolve conflicts, handle dependencies
- Result reporting: agents report results to Goal Fulfillment, Memory, and Self-Improvement

**Key insight:** The Processor is the decision-making layer. It receives refined and broken-down goals from Goal Fulfillment, checks them with the Governor, then decides what to execute based on:
- Directive priorities (safety > convenience)
- Goal priorities (user-set or agent-inferred)
- Timeline (deadlines, dependencies)
- Resource availability (agents, system resources)
- Conflict resolution (when goals conflict, which takes precedence?)

Prioritization is LLM-driven where possible — the agent decides how to sequence and delegate based on context, not hardcoded rules.

**Delegation model:**
For v1, the Processor spawns simple autonomous agents that are equivalent to the current chat window but autonomous. There is no workflow or handoffs — it's just spawning agents with instructions for completing a broken-down goal. This allows the goal agents to use the Processor to look up information as well. A goal agent can say that the first step is to do a web search to find more info on the problem it is trying to solve, and then continue from there when the results come back.

Each goal agent:
- Receives the sub-goal with context
- Plans how to achieve it (using the existing Planner Agent logic)
- Requests tool authorization from the Governor before execution
- Executes the plan using authorized tools (via the Executor, which is part of Governor)
- Can use the Processor to look up additional information during execution
- Reports results back to Goal Fulfillment, Memory, and Self-Improvement

**Tool authorization flow:**
When a goal agent needs to use tools or capabilities, it requests authorization from the Governor. The Governor checks:
- Does this tool/capability require user approval?
- Is it already authorized (always, this session, this request)?
- Does it violate any directives?

If authorization is needed, the Governor fires a popup for user approval. Once authorized, the goal agent has explicit access to those tools. If the goal agent tries to use unauthorized tools, it fails and loops back to Goal Fulfillment for re-evaluation.

**Result reporting:**
Agents report all results to three systems via direct function calls:
1. **Memory** — stores execution results for future reference
2. **Self-Improvement** — analyzes results for patterns and improvement opportunities
3. **Goal Fulfillment** — updates goal state and decides next steps

Goal Fulfillment receives the results and decides what needs to be done next: report to the user, refine and resubmit to the Processor, or cancel sub-goals.

**Execution monitoring:**
The Processor monitors execution and can:
- Re-prioritize if a higher-priority goal arrives
- Re-delegate if an agent fails
- Pause execution if a goal becomes blocked

**Goals cannot be broken down mid-execution.** If a delegated agent cannot complete the goal as written, it should end execution and return results so they can be reevaluated by the Goal Fulfillment system. In this way, smaller sub-goals may be cancelled or reworded to achieve necessary results without changing the overall user-given goal. The process for achieving a user goal is always fluid and available to be changed depending on execution results.

### 4. Memory

*Stores and recalls context. The system's long-term knowledge base.*

**Current state:** System manifest (paths + services), session state, plan doc as execution record, audit log. All are read-at-need, not a unified context layer.

**Scope:**
- Short-term context: current session, active plan, recent events
- Long-term memory: execution history, learned preferences, per-task outcomes, user patterns
- World model: system manifest, service topology, installed packages, filesystem sensitivity map
- Storage and recall: determine how things are stored and recalled when queried

**Key insight:** Memory is currently a passive store that modules query. The long-range vision is a memory layer that actively maintains relevance — forgetting irrelevant history, surfacing relevant patterns, and pre-loading context before modules need it.

**What's missing now:** Long-term memory, semantic indexing of past executions, preference inference. The audit log is the raw material; the memory module is what processes it into usable knowledge.

**Memory responsibilities:**
- Store everything that isn't actively processing (goal state, execution results, user preferences, system observations)
- Determine storage format (structured, semantic, graph-based) based on what needs to be recalled
- Provide context to other modules when queried
- Actively maintain relevance: forget irrelevant history, surface relevant patterns

**v1 approach:** Memory is a passive store. Goal Fulfillment, Governor, and Self-Improvement agent instructions should include where to query and how. Eventually we may want a marshalling system that injects relevant context before an agent gets it, but not necessary for v1. There are decent examples of AI memory systems already out there that we could take inspiration from if not use outright to start. This feels like the most independent and straightforward part for v1.

### 5. Self-Improvement

*Analyzes results and creates improvement goals. The system's capacity to get better at its own job.*

**Current state:** Not implemented. The audit log and plan docs are the raw material. Nothing processes them.

**Scope:**
- Retrospective analysis: what worked, what failed, what triggered re-plans
- Pattern detection: identify recurring problems, successful strategies, inefficient workflows
- Improvement goal generation: create new goals to improve the system (e.g., "optimize photo indexing strategy," "reduce re-plan failures for package installation")
- Tool creation: identify opportunities to build new tools, scripts, or utilities that improve efficiency

**Key insight:** Self-Improvement is the module that distinguishes HyperiOS from a static agent framework. It is what allows the system to compound capability over time. However, it must operate within the same Governor constraints as everything else — the Self-Improvement module cannot grant itself new capabilities, modify the Arbiter's rules, or bypass the allowlist.

**The self-improvement loop:**
1. Execution produces results (success, failure, partial completion)
2. Self-Improvement analyzes results and identifies patterns
3. Self-Improvement creates improvement goals (e.g., "reduce package installation failures")
4. Improvement goals are submitted to Goal Fulfillment via direct function call, just like a user submitting a request
5. Improvement goals go through the normal goal lifecycle: Refinement → Governor → Prioritization → Execution
6. Results feed back into Self-Improvement, creating a continuous improvement cycle

**Key constraint:** Self-Improvement can create goals, but those goals must go through the Governor and Prioritization like any other goal. The agent cannot bypass safety checks by creating "improvement goals" that violate directives.

**Automatic learning:**
The system should automatically learn from execution history without requiring the user to tell it that an agent struggled with something but eventually found a good way to go about it. Self-Improvement should analyze conversation history and execution traces to identify:
- What approaches worked well
- What approaches failed or were inefficient
- Patterns that can be encoded as tools, templates, or improved strategies

This is crucial for the MVP. Eventually Self-Improvement will want to ingest more than just agent histories (system logs, user feedback, goal outcomes), but agent histories are a good place to start.

### 6. Orchestration (implied, currently in main.go)

*How modules are wired together.*

**Current state:** `cmd/hyperi/main.go` — a 1000-line file that orchestrates the full pipeline. This is fine for v1 but becomes a bottleneck as modules evolve independently.

**v1 approach:** Direct function calls between modules. No event bus. Systems hit each other directly. This keeps the architecture simple and avoids unnecessary complexity.

**Long-range direction:** Each module exposes a clean interface. Orchestration becomes thin configuration: "when Goal Fulfillment produces a refined goal, send it to Governor; when Governor approves, hand to Processor; when Processor delegates, execute; publish all results to Memory, Self-Improvement, and Goal Fulfillment via direct function calls."

---

## The Self-Improvement Architecture

This is the most important long-range architectural question. How does the system improve itself without breaking itself?

### The Boundary Principle

Modules have well-defined interfaces. Self-improvement that stays inside a module's interface cannot break adjacent modules. This is already partially true: the Planner Agent communicates with the rest of the system only through `ActionPlan`. Improving the Planner (better prompts, better context, different model) cannot affect the Governor or the Executor as long as `ActionPlan` remains valid.

Each module boundary is a safety fence for self-improvement.

### What Can Be Improved

| Module | Improvable Surface | Cannot Change |
|---|---|---|
| Goal Fulfillment | Refinement strategies, breakdown heuristics, user interaction patterns | Goal states (Refining/Active/Done/Blocked/Cancelled), directive hierarchy |
| Governor (Adversarial) | Adversarial system prompt, risk taxonomy | Arbiter rules, allowlist, autonomy level thresholds, directive enforcement |
| Governor (Arbiter) | **Cannot be improved by the agent** — only by explicit human edit | Everything — it is the trust anchor |
| Governor (Executor) | Execution strategies, retry policies, failure handling | Capability system, audit trail format |
| Processor | Prioritization strategies, delegation patterns, agent coordination | Directive priorities, safety constraints |
| Memory | Storage strategies, indexing, relevance scoring, recall patterns | The audit log format (append-only, tamper-evident) |
| Self-Improvement | Improvement strategies, analysis heuristics, goal generation patterns | Cannot grant itself new capabilities or modify the Arbiter |

**How improvement works:**
Self-Improvement receives raw data from all non-Governor agents and analyzes it. When it identifies an opportunity for improvement, it creates a goal and submits it to Goal Fulfillment via direct function call, just like a user submitting a request. The improvement goal goes through the normal goal lifecycle (Refinement → Governor → Processor → Execution). The improvement goal can result in arbitrary code changes (except to Governor systems), but those changes must pass through the Governor for safety review.

### The Self-Improvement Loop

Self-Improvement receives raw data directly from all non-Governor agents (Goal Fulfillment, Processor, Memory). There is no intermediate LLM processing to create reports — Self-Improvement gets the raw conversation history, execution traces, and goal outcomes directly.

Self-Improvement analyzes this data and identifies patterns. When it wants to make a change (optimize a strategy, create a new tool, restructure memory), it creates a goal that goes through the normal goal lifecycle:

1. Self-Improvement creates a goal (e.g., "reduce package installation failures by implementing dependency pre-checks")
2. Goal Fulfillment refines the goal
3. Governor reviews for safety and directive compliance
4. Processor executes the goal
5. Results flow back to Memory, Self-Improvement, and Goal Fulfillment

**Key constraint:** Self-Improvement can propose goals that result in arbitrary code changes (except to Governor systems), but those goals must go through the Governor like any other goal. The agent cannot bypass safety checks.

**Automatic learning:**
The system should automatically learn from execution history without requiring the user to tell it that an agent struggled with something but eventually found a good way to go about it. Self-Improvement analyzes raw conversation history and execution traces to identify:
- What approaches worked well
- What approaches failed or were inefficient
- Patterns that can be encoded as tools, templates, or improved strategies

Eventually Self-Improvement will want to ingest more than just agent histories (system logs, user feedback, goal outcomes), but agent histories are a good place to start.

---

## Module APIs (Draft)

Each module should eventually expose a stable, versioned Go interface. This is the direction, not the current state.

```go
// Every module implements this.
type Module interface {
    // Name returns the module's canonical name for routing and audit.
    Name() string
    // Health returns current operational status.
    Health() ModuleHealth
}

// v1: Modules communicate via direct function calls.
// No event bus. Systems hit each other directly.
```

**Why direct function calls for v1:** The event bus adds complexity that isn't justified for the initial implementation. Direct function calls are simpler to reason about, easier to debug, and sufficient for the current scale. If the system grows to require decoupled communication, an event bus can be introduced later without changing the module interfaces.

### Goal Manager (Part of Goal Fulfillment)

The goal-driven architecture requires a persistent goal store managed by the Goal Fulfillment module.

The Goal Manager is responsible for:
- Maintaining the persistent goal store (`/var/lib/hyperi/goals/`)
- Tracking goal lifecycle (Refining, Active, Done, Blocked, Cancelled)
- Coordinating with the scheduler to trigger goal-related background work
- Surfacing goal status to the user via the TUI

The Goal Manager interacts with other modules:
- It reads from the **Memory** module to gather goal-relevant information
- It writes to the **Governor** for safety review before goals reach Prioritization
- It writes to the **Processor** module to delegate refined goals
- It reads from the **Self-Improvement** module to receive improvement goals (via direct function call, just like a user submitting a request)

The Goal Manager is what transforms HyperiOS from a reactive task-runner into a proactive goal-driven system.

### Directives Store (Part of Governor)

Directives are hierarchical rules that constrain system behavior. They are stored and managed by the Governor module.

The Directives Store is responsible for:
- Maintaining the directive hierarchy (safety > convenience, etc.)
- Enforcing directive compliance during goal review
- Preventing goals that violate directive priorities

Directives are stored in two config files:
- **Immutable directives** (`config/directives-immutable.yaml`): Highest priority, cannot be changed by the agent (e.g., "do not harm the user")
- **Mutable directives** (`config/directives-mutable.yaml`): Can be modified through the goal system (e.g., "be concise in communication")

Both files are loaded into all agent instructions, even if not all directives are relevant to all tasks. They are considered important enough to always be considered.

---

## The Full Long-Range Module Taxonomy

Revised from your initial framing:

| Module | Role | Agentic? | Current Status |
|---|---|---|---|
| Goal Fulfillment | Refine, break down, track goals; interact with user | Yes — LLM-driven | Partial (Intent Agent, Planner) |
| Governor | Adversarial review + deterministic arbitration + directive enforcement + executor + tool authorization | Partially (AA is LLM) | Done (Phase 1A) |
| Processor | Prioritize, delegate sub-goals; coordinate agents | Yes — LLM-driven | Not started |
| Memory | Store and recall context; long-term knowledge base | No — it's a store+index | Partial (manifest, session) |
| Self-Improvement | Analyze results; create improvement goals; compound capability | Yes — LLM-driven | Not started |
| Orchestration | Wiring — direct function calls, session lifecycle | No — it's plumbing | In main.go (Phase 1) |

### Module Mapping from Current Architecture

The existing components map to the new module structure as follows:

| Current Component | New Module | Notes |
|---|---|---|
| Intent Agent | Goal Fulfillment | Converts NL to GoalGraph; part of goal refinement |
| Planner Agent | Goal Fulfillment | Breaks goals into steps; part of goal breakdown |
| Adversarial Agent | Governor | Finds risks in plans; part of adversarial review |
| Policy Arbiter | Governor | Deterministic final authority; part of governance |
| Executor | Governor | Capability-gated execution; moved from separate module to Governor for tool authorization |
| CommandValidator | Governor | Structural + allowlist checks; part of governance |
| Capability System | Governor | Allowlist and runtime grants; part of governance |

The functionality isn't changing much — just the organization and nomenclature. The Intent Agent and Planner both operate within Goal Fulfillment (planning/breaking down goals). The Adversarial Agent, Arbiter, Executor, and Capability System all operate within Governor (safety, directive enforcement, and tool authorization).

### Module Interaction Flow

```
User
  │
  ▼
Goal Fulfillment (refine + break down)
  │
  ▼
Governor (safety + directive check + tool authorization)
  │
  ▼
Processor (prioritize + delegate)
  │
  ├──► Agents execute sub-goals (can use Processor to look up info)
  │    (request tool authorization from Governor as needed)
  │
  ▼
Results ──┬──► Memory (store for future reference)
          ├──► Self-Improvement (analyze raw data for patterns)
          └──► Goal Fulfillment (update goal state, inform user)
```

The flow is continuous: results feed back into Memory, Self-Improvement, and Goal Fulfillment via direct function calls, creating a learning loop. Self-Improvement can create new goals that re-enter the flow at Goal Fulfillment.

**Communication pattern:** All inter-module communication uses direct function calls. No event bus. Systems hit each other directly.

**Tool authorization flow:** When a goal agent needs to use tools or capabilities, it requests authorization from the Governor. The Governor checks:
- Does this tool/capability require user approval?
- Is it already authorized (always, this session, this request)?
- Does it violate any directives?

If authorization is needed, the Governor fires a popup for user approval with options:
- **Always**: Authorize this tool/capability for all future goals
- **This session only**: Authorize for the current session
- **This request only**: Authorize for this specific goal execution
- **No**: Deny this request
- **Never**: Deny and prevent future requests for this tool/capability

Once authorized, the goal agent has explicit access to those tools. If the goal agent tries to use unauthorized tools, it fails and loops back to Goal Fulfillment for re-evaluation.

### MVP Requirements

All five modules are required for the minimum viable system:

- **Goal Fulfillment** — Without this, there's no way to interact with the user or track goals
- **Governor** — Without this, there's no safety check; the system cannot be trusted
- **Processor** — Without this, goals cannot be executed
- **Memory** — Without this, the system cannot learn or accumulate context
- **Self-Improvement** — Without this, the system cannot automatically improve; the user would have to manually tell it what worked and what didn't

Self-Improvement is crucial for the MVP. The system should automatically learn from execution history without requiring the user to intervene. If an agent struggled with something but eventually found a good way to go about it, Self-Improvement should identify this pattern and ensure the good approach is used first time next time.

---

## Modularization Path from Here

The current codebase is not poorly structured — the packages are mostly right. The gap is that package boundaries are enforced by Go's type system but not by clean interface contracts. The internal packages know too much about each other.

**Iteration 1 (near-term, v0.2-v0.3):** Define explicit Go interfaces for each module. The implementations stay the same. This makes the seams visible without requiring a rewrite.

**Iteration 2 (medium-term, v0.4-v0.5):** Refine inter-module communication patterns. Reduce direct method calls between packages where appropriate. Each module becomes a clear interface with well-defined inputs/outputs.

**Iteration 3 (longer-term, v1.x):** Introduce the Module interface with Report + Tune. The Self-Improvement module can now talk to any other module through a stable API. This is when self-improvement becomes real.

**Iteration 4 (post-v1):** Modules become independently deployable. The Self-Improvement module could run on a separate process or device. The Governor could be a separate verified binary. This is the distributed-system direction and is not needed for v1.

---

## Open Questions

These are the questions that will most shape the long-range architecture. They do not have answers yet — they need real usage data.

**1. What is the right memory model?**
The audit log and plan docs are append-only raw history. Long-range memory requires something indexed and queryable. Options: vector embedding of plan docs (semantic search over past actions), structured key-value store for facts, graph model for relationships between actions and outcomes. The right answer depends on what kinds of questions the system needs to ask about its own history.

**2. How fast should the Self-Improvement module act?**
A weekly improvement cycle is conservative and safe. A continuous improvement cycle (after every session) is riskier but compounds faster. The right cadence is probably adaptive — faster improvement on high-failure areas, slower improvement on stable areas.

**3. What is the trust model for the Self-Improvement module itself?**
The Self-Improvement module has a privileged view of the system's internals. It can read all reports, propose changes to all other modules. This makes it a high-value attack target and a high-risk component to get wrong. Should it run with the same autonomy level as user-directed sessions, or should it have its own, more conservative autonomy level?

**4. How does the system handle conflicting goals across sessions?**
A background session scheduled to "keep nginx running" and a foreground session that removes nginx for troubleshooting are in conflict. The Prioritization module must resolve this based on directives, timeline, and goal priorities. How does it decide which instruction is most correct?

**5. Where does the modular boundary sit for the Governor?**
The Adversarial Agent is an LLM and can be improved. The Arbiter is deterministic and should not be LLM-modified. But they are currently co-located in the Governor. As the system matures, should they be separate modules? The Arbiter is a trust anchor — perhaps it warrants being a signed binary that the rest of the system cannot modify even in principle.

**6. What does "improving the Planner" actually mean in practice?**
Prompt tuning is the obvious answer, but prompts are fragile and hard to version. Better long-term approaches might include: fine-tuning on the project's own plan docs (learning from its own successful plans), structured retrieval of relevant prior plans as context, or a library of capability-specific plan templates that get selected rather than generated from scratch.

**7. How should goals be prioritized?**
When the agent has multiple active goals, how does it decide what to work on? Should there be explicit user-set priorities, or should the agent infer priority from context (urgency, dependencies, idle time)? How does it balance short-term user requests against long-term goal maintenance? The Prioritization module needs enough context to resolve sequencing needs.

**8. How does the agent know when a goal is "done"?**
Some goals have clear completion criteria ("install nginx"). Others are open-ended ("keep the system secure"). How does the agent distinguish between a goal that is complete and one that is ongoing? The agent should test and verify completion before informing the user. How does it decide when to mark a goal as Done vs. keep it Active for ongoing maintenance?

**9. How should the agent handle goal conflicts with user data?**
If a goal is "organize photographs" but the user has a specific folder structure they prefer, how does the agent balance its own optimization against the user's existing patterns? The Goal Fulfillment module should honor user requests and leave existing user state unchanged unless specifically requested. If the user specifies a technical approach that seems suboptimal, the agent can inquire further, but should honor the request if that is truly what the user wants.

**10. How does the agent learn from the user?**
The system internals are intended to be invisible to the user. Only explicit feedback is needed to start. How does the agent capture explicit feedback ("don't do that," "I prefer this approach") and incorporate it into goal refinement and memory? Does it ask for feedback or wait for the user to provide it?

**11. How do tools and learnings get incorporated into the system?**
Every move the agent makes should be reflected on and analyzed for self-improvement and memory saving opportunities. When the agent discovers a useful pattern, how does it encode that as a tool, script, or template? Are these tools scoped to goals or shared across the system? How does the agent decide when a one-off solution should become a first-class tool?

**12. How does the Memory system determine what to store and recall?**
Basically anything that isn't actively processing needs to be in the memory system. How does the Memory system determine storage format (structured, semantic, graph-based)? How does it decide what's relevant when queried? Does it forget old context? How does it balance comprehensiveness against performance?

---

## What This Is Not

Clarifying the boundaries:

- **Not a general-purpose AI agent platform.** It is a Linux distribution. The OS context (sway, systemd, apt, inotify) is not incidental — it is the point. Generic agent frameworks (LangChain, etc.) are not the model.

- **Not a user-configures-everything system.** The goal is a system that improves itself. The user should have to configure less over time, not more.

- **Not a task-runner.** It is a goal-driven system. The user defines goals ("organize my photos," "keep this system secure"), not tasks ("run this script," "install this package"). The agent decomposes goals into tasks, executes them, monitors outcomes, and adapts. The user never needs to specify the implementation.

- **Not a directive-free system.** The agent operates under hierarchical directives that constrain behavior. "Do not harm the user" takes precedence over "be witty and tell jokes." Directives are user-defined and cannot be modified by the agent. They are the structural guarantee that the agent stays within bounds.

- **Not a jailbreak.** The Governor and the OS security model are not obstacles to be overcome — they are features. Self-improvement that defeats safety constraints is not improvement.

- **Not a cloud agent.** Local-first. The Anthropic API is the current LLM backend, but the system should be capable of running with a local model for everything except the most demanding reasoning tasks. This is a direction, not a current requirement.

- **Not a black box.** While the system internals are intended to be invisible to the user during normal operation, the user can ask the agent for information about goals, progress, and decisions. The system is transparent when requested, but does not burden the user with details by default.

- **Not event-driven (for v1).** Modules communicate via direct function calls, not an event bus. This keeps the architecture simple and avoids unnecessary complexity. If the system grows to require decoupled communication, an event bus can be introduced later.

---

## Goals vs. Tools: The Ownership Boundary

The most important architectural boundary in HyperiOS is between what the user owns and what the agent owns. This is not just a permissions model — it is a philosophical division that determines how the system evolves.

### The Principle

**The user defines goals. The agent owns everything else.**

Goals are the user's domain: what they want accomplished, what constraints matter, what success looks like. Goals come from outside the agent — through intent, through scheduled tasks, through system events. The agent does not invent goals.

Tools are the agent's domain: how it accomplishes goals, what capabilities it has, how it stores and retrieves knowledge, what internal systems it builds. The agent owns its toolbox completely. It can create, modify, retire, and restructure its own tools without user intervention — as long as it stays within the Governor's safety boundaries.

This is a stronger claim than "the agent can use tools." It says the agent *owns* its tools — they are its infrastructure, not the user's. The agent is an entity with its own workspace, its own memory, its own internal systems. The user defines what needs to happen; the agent decides how and with what.

### Goals Are the Primary Driver

Goals are not just inputs to be decomposed and executed. They are the **persistent objectives** that drive all agent behavior. The agent maintains a portfolio of active goals and works toward them continuously, not just when the user is actively prompting.

**Goals are numerous and concurrent.** A user might have active goals like:
- "Keep this system secure and up to date"
- "Organize my photographs so I can find them easily"
- "Monitor disk usage and alert me before it becomes a problem"
- "Learn my coding patterns and suggest improvements"

These goals coexist. Some are short-term (finish this task now). Some are long-term (maintain this system indefinitely). Some are reactive (respond to this event). Some are proactive (improve this over time).

**Goals are long-lived and adaptive.** A goal like "organize photographs" is not a one-shot task. It's an ongoing objective that requires:
1. **Context gathering** — understanding the user's photo library structure, usage patterns, what "organized" means to them
2. **Planning** — proposing an organization scheme, folder structure, tagging strategy
3. **Implementation** — moving files, creating indexes, building tools
4. **Monitoring** — checking if the organization is working, if the user can find photos
5. **Improvement** — adjusting the scheme based on feedback, learning from usage patterns
6. **Maintenance** — handling new photos as they arrive, keeping the system current

The agent handles this entire lifecycle autonomously. The user doesn't specify the folder structure or the tagging taxonomy — they specify the goal ("organize my photos so I can find them"), and the agent figures out what works for this user based on observation, experimentation, and feedback.

**Goals drive proactive behavior.** The agent doesn't wait for the user to ask. If a goal is "keep the system secure," the agent checks for updates, runs security scans, and reports findings — even when the user isn't asking. If a goal is "organize photographs," the agent might notice a new batch of unorganized photos and process them without being prompted.

**Goals are adaptive.** As the agent learns, it adjusts its approach. If the initial photo organization scheme isn't working (the user keeps overriding folder choices, or search patterns don't match the structure), the agent recognizes this and proposes a different approach. The goal remains constant; the strategy evolves.

### The Goal Lifecycle

Every goal goes through a lifecycle, managed by the Goal Fulfillment module:

**Goal states:**
- **Refining** — Goal is being clarified with the user. Context is being gathered. Not yet ready for execution.
- **Active** — Goal has been refined and broken down. Currently being worked on by agents via Prioritization.
- **Done** — Goal has been completed and verified. No further action needed.
- **Blocked** — Goal cannot proceed due to a dependency, missing information, or user constraint. Requires user input or resolution.
- **Cancelled** — Goal has been explicitly cancelled by the user or abandoned due to changing priorities.

**Goal flow through the system:**

```
User defines goal
  │
  ▼
Goal Fulfillment (Refining)
  - Clarify intent with user
  - Gather context from Memory
  - Break down into sub-goals
  │
  ▼
Governor
  - Check directive compliance
  - Adversarial review
  - Arbiter approval
  │
  ▼
Prioritization (Active)
  - Decide when to work on it
  - Delegate sub-goals to agents
  - Monitor execution
  │
  ▼
Execution
  - Agents execute sub-goals
  - Results flow to Memory, Self-Improvement, Goal Fulfillment
  │
  ▼
Goal Fulfillment
  - Verify completion
  - Update goal state (Done/Blocked/Cancelled)
  - Inform user
```

The agent handles the entire lifecycle autonomously. The user doesn't specify the folder structure or the tagging taxonomy — they specify the goal ("organize my photos so I can find them"), and the agent figures out what works for this user based on observation, experimentation, and feedback.

**Goals drive proactive behavior.** The agent doesn't wait for the user to ask. If a goal is "keep the system secure," the agent checks for updates, runs security scans, and reports findings — even when the user isn't asking. If a goal is "organize photographs," the agent might notice a new batch of unorganized photos and process them without being prompted.

**Goals are adaptive.** As the agent learns, it adjusts its approach. If the initial photo organization scheme isn't working (the user keeps overriding folder choices, or search patterns don't match the structure), the agent recognizes this and proposes a different approach. The goal remains constant; the strategy evolves.

### Architectural Implications

The current architecture treats goals as session-scoped: the user says something, the Intent Agent decomposes it into a GoalGraph, the Planner turns it into steps, the Executor runs them, the session ends. Goals don't persist beyond the session.

Goal-driven autonomy requires a persistent goal model managed by the Goal Fulfillment module:

**Goal store.** A new persistent data structure at `/var/lib/hyperi/goals/` that holds active goals across sessions. Each goal has:
- ID, description, state (Refining, Active, Done, Blocked, Cancelled)
- Context gathered so far (observations, constraints, user preferences)
- Current strategy (the agent's current approach to achieving this goal)
- Sub-goals (broken-down pieces delegated to Prioritization)
- Progress history (what has been tried, what worked, what didn't)
- Last activity timestamp
- Priority (user-set or agent-inferred)

**Goal Fulfillment module.** Responsible for:
- Interacting with the user to refine goals
- Breaking down goals into sub-goals
- Tracking goal state transitions
- Verifying goal completion
- Reporting results to the user

**Goal-aware planning.** The Planner (part of Prioritization) needs access to active goals when generating plans. A plan for "organize photographs" should reference the goal's accumulated context, not start from scratch. The goal's strategy and history inform the plan.

**Goal-aware scheduling.** The scheduler's background jobs should include goal maintenance: periodic context gathering, monitoring checks, improvement cycles. Goals drive what the scheduler does during idle time.

**Goal-aware memory.** The Memory module should prioritize goal-relevant information. When the agent is working on "organize photographs," the context should include the goal's history, the user's photo-related patterns, and the current organization state — not just generic system context.

### What This Means in Practice

**The agent can build new tools.** If the agent encounters a recurring pattern that doesn't map cleanly to existing capabilities, it should be able to create a new one. A `execute:database` capability for a system that needs it. A specialized script that becomes a first-class tool. A new ReadyCondition type for a domain-specific check.

**The agent can restructure its own memory.** The Context module is agent-owned infrastructure. If the current indexing strategy isn't working, the agent can rebuild it. If it discovers that semantic search over plan docs is more useful than structured key-value lookup, it can restructure. The memory system exists to serve the agent's ability to accomplish goals — the agent should optimize it.

**The agent can build internal utilities.** Anything the agent does repeatedly should become a tool. A pattern of "check disk usage before installing a large package" becomes a pre-flight check tool. A debugging workflow becomes a diagnostic tool. The agent's internal processes are its own to optimize.

**The agent can retire unused tools.** A capability that hasn't been used in months gets deprioritized. A memory index that produces only noise gets archived. The agent prunes its own toolbox based on what actually helps accomplish goals.

**The agent can hardcode what it learns.** When the agent discovers through reflection that a particular approach always works for a class of problems, it can encode that as a template, a cached plan, or a first-class tool — rather than re-deriving it every time. This is the difference between an agent that merely responds and one that accumulates capability.

### The Three Ownership Domains

```
┌─────────────────────────────────────────────────────────────┐
│                                                             │
│  USER DOMAIN                   AGENT DOMAIN                 │
│  ───────────                   ────────────                 │
│  Goals                         Tools (capabilities,         │
│  Intent                          scripts, templates)        │
│  Constraints                   Internal systems (memory,    │
│  Autonomy level                  indexing, caching)         │
│  Approval of changes           Learned patterns             │
│  The Arbiter's rules           Execution strategies         │
│                                Reflection output            │
│                                                             │
│              SHARED / AMBIGUOUS                             │
│              ────────────────────                           │
│              Project code (user's work, agent maintains)    │
│              System configs (user's system, agent manages)  │
│              Package state (shared infrastructure)          │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

The boundary is enforced architecturally, not by convention:

- **User domain** is governed by OS permissions, PAM, the Arbiter, and the capability allowlist. The agent cannot escalate its own autonomy, modify the Arbiter, or bypass the allowlist. These are structural constraints below the agent layer.
- **Agent domain** is governed by the Governor's safety review. The agent can do anything within its own domain as long as the Governor approves it. At low autonomy levels, the user sees and approves changes to agent infrastructure. At high autonomy levels, the agent self-governs within its domain.
- **Shared domain** requires negotiation. The agent manages project code and system configs *on behalf of the user* — but the user owns the data. The agent can suggest changes, but the user decides what the codebase looks like.

### Why This Matters

Without this boundary, the agent is a tool the user operates. With it, the agent is an entity that operates tools on the user's behalf. The difference compounds over time:

- **Without:** The user configures the agent's capabilities, manages its memory, defines its workflows. The agent responds but doesn't accumulate.
- **With:** The agent builds its own capabilities, manages its own memory, defines its own workflows. The user sets goals and constraints. The agent gets better at its job without the user having to maintain it.

The system that improves itself without requiring the user to configure it — that's the bet. The user should have to configure *less* over time, not more. The agent's growing toolbox and refined memory are what make that possible.

### How This Maps to Current Architecture

| Component | Current state | What "agent ownership" means |
|---|---|---|
| Capability types | Static allowlist.yaml, hardcoded in code | Agent can propose new capability types via Self-Improvement; Governor reviews; user approves at low autonomy |
| Templates | Template generator creates from cached plans | Already agent-owned. Generator clusters, extracts, validates. User approves before deployment. |
| Memory | Session state, plan docs, audit log — passive store | Agent should actively manage: index, forget, restructure. Memory module is agent infrastructure. |
| System prompt | Hardcoded in agent source | Agent should be able to tune its own prompts through the Module interface (Report + Tune) |
| Execution strategies | Hardcoded retry/backoff, on_failure policies | Agent can learn better strategies from execution history and propose tuning changes |
| Internal utilities | None — everything is inline in executor | Agent can extract patterns into reusable tools/scripts that become part of its toolbox |
| Goals | Session-scoped, not persistent | Persistent goal store managed by Goal Fulfillment; goals survive across sessions |
| Directives | Implicit in Arbiter rules | Explicit directive hierarchy managed by Governor; user-defined, agent cannot modify |

### The Self-Improvement Connection

The Goals vs. Tools boundary is what makes the Self-Improvement module meaningful. The Self-Improvement module doesn't optimize the user's goals — it optimizes the agent's ability to accomplish them. It operates entirely within the agent domain:

- Reports on tool effectiveness (which capabilities are used, which fail, which are redundant)
- Proposes tool changes (new capabilities, retired ones, restructured memory)
- Tunes internal systems (prompt fragments, context window size, re-plan strategy)
- Builds new infrastructure (diagnostic tools, monitoring utilities, workflow templates)
- Creates improvement goals that flow through the normal goal lifecycle

The Self-Improvement module is the agent's internal R&D department. It works for the agent, not the user — but its output ultimately serves the user by making the agent better at accomplishing goals.

**The continuous improvement loop:**
1. Execution produces results (success, failure, partial completion)
2. Results flow to Memory (storage), Self-Improvement (analysis), and Goal Fulfillment (state update)
3. Self-Improvement identifies patterns and creates improvement goals
4. Improvement goals enter the normal flow: Refinement → Governor → Prioritization → Execution
5. Results feed back into the loop, creating continuous improvement

Every move the agent makes should be reflected on and analyzed for self-improvement and memory saving opportunities. Tools, scripts, and learnings are incorporated into the system so that it feels adaptive and intelligent.

### What the Agent Should Not Own

Clarity on the negative boundary matters as much as the positive one:

- **The user's data.** Files, configs, personal information. The agent manages these *on the user's behalf* — it does not own them. It cannot delete user files to free space for its own use, cannot restructure user code without understanding the user's intent, cannot decide that user data is "inefficient" and reorganize it.
- **The safety architecture.** The Arbiter, the allowlist, OS permissions, PAM. These are the user's structural guarantee that the agent stays within bounds. The agent cannot modify them — only the user can, through explicit action.
- **The agent's own identity.** The agent cannot decide to become a different kind of agent. It cannot rewrite its core objectives or change what "success" means. Goals come from the user; the agent optimizes the path, not the destination.

---

## First Principles Check

Every architectural decision in this vision should be traceable to one of these:

1. **Safety is architectural, not behavioral.** The Arbiter cannot be LLM-modified. The OS permission model is below the agent layer. Directives are enforced by the Governor, not by agent behavior.

2. **Modularity enables safe self-improvement.** Interfaces are the fences. Improvement that respects module boundaries cannot break adjacent modules.

3. **The audit trail is the ground truth.** Every action, every observation, every improvement is recorded. The system cannot lie about what it did.

4. **The user is the ultimate authority.** Autonomy level is set by explicit human action. Automatic trust escalation requires explicit human consent. The Self-Improvement module proposes; it does not decide. Directives are user-defined and cannot be modified by the agent.

5. **Local-first, not cloud-dependent.** The system should degrade gracefully without network access, not fail silently.

6. **The user defines goals. The agent owns tools.** This is the fundamental ownership boundary. The agent's toolbox, memory, and internal systems are its own to build, restructure, and optimize — within the Governor's safety constraints. The user should have to configure less over time, not more.

7. **Directives constrain, goals drive.** Directives are hierarchical rules that constrain behavior (safety > convenience). Goals are what the user wants to achieve. The agent pursues goals within directive constraints. Directives have no states; goals have a lifecycle.
