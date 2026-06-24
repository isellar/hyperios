# HyperiOS — Open Questions for v1

*Questions that must be answered before v1 ships. Organized by priority and category.*

---

## Impact Analysis: Do These Questions Change Core Ideas?

**Short answer:** Most are implementation details that need ironing out. A few could challenge core architectural decisions.

### Questions That Could Challenge Core Architecture

These questions, if answered a certain way, might require rethinking fundamental design decisions in `vision.md`:

**1. Concurrency & Resource Management (Question #2)**
- **Core principle at stake:** "Not event-driven (for v1)" — modules communicate via direct function calls
- **Potential challenge:** If concurrent agents create coordination problems that direct calls can't solve, you might need an event bus or message queue earlier than planned
- **Example scenario:** Two agents both try to modify `/etc/nginx/nginx.conf` simultaneously. Direct function calls don't provide a natural coordination mechanism. You might need:
  - Resource locking (adds complexity to direct calls)
  - Serialization of agents (reduces concurrency benefits)
  - Event-based coordination (challenges "not event-driven" principle)
- **Likelihood of core change:** Medium. Probably solvable with resource locks, but could reveal that direct calls don't scale.

**2. Self-Improvement Safety (Question #12, failure modes)**
- **Core principle at stake:** Self-Improvement is required for MVP and must be safe
- **Potential challenge:** If the self-improvement loop can't be made safe enough for v1 (e.g., can't prevent infinite improvement loops, can't validate that improvements actually improve), you might need to defer it to v2
- **Example scenario:** Self-Improvement creates a goal to "optimize photo indexing," which creates another goal to "optimize the optimizer," which creates another goal... How do you detect and prevent meta-improvement loops?
- **Likelihood of core change:** Low-Medium. The Governor should catch this, but needs careful design.

**3. Multi-user Support (Question #13)**
- **Core principle at stake:** Single-user assumption simplifies architecture significantly
- **Potential challenge:** If v1 needs multi-user (e.g., for a team use case), you need:
  - Goal namespaces (per-user vs. shared)
  - Permission model (who can see/modify whose goals?)
  - Conflict resolution (user A's goal vs. user B's goal)
  - Audit isolation (whose action was this?)
- **Likelihood of core change:** High if multi-user is required. Low if single-user for v1 (recommended).

**4. Autonomy Levels (Question #4)**
- **Core principle at stake:** Governor enforces safety, autonomy level controls what needs approval
- **Potential challenge:** If you need per-goal autonomy (different goals have different trust levels), the Governor's design becomes more complex. Currently envisioned as system-wide.
- **Example scenario:** User trusts the agent to "organize photos" at high autonomy but wants "modify system configs" at low autonomy. How does the Governor track per-goal autonomy?
- **Likelihood of core change:** Medium. Per-goal autonomy is more flexible but more complex.

### Questions That Are Implementation Details

These are important but don't challenge core architectural decisions:

**User Interaction Model (#1)** — Affects UI layer, not core pipeline. The goal lifecycle works the same whether input comes from TUI, voice, or API.

**Error Handling & Recovery (#3)** — Important for robustness, but the core architecture (Goal Fulfillment → Governor → Processor → Execution) remains the same. You're adding policies, not changing structure.

**Data Model & Storage (#5)** — SQLite vs. YAML vs. JSON is a choice, not an architectural decision. The goal store schema might evolve, but the concept of persistent goals is sound.

**Security Model Details (#6)** — "Safety is architectural" principle stands. You're choosing *which* OS mechanisms (AppArmor, namespaces, etc.), not *whether* to use them.

**Observability & Debugging (#7)** — Important for operations, but doesn't change how modules interact. You're adding instrumentation, not restructuring.

**Performance & Cost (#8)** — Affects implementation choices (caching, local vs. cloud models), but the goal-driven architecture is performance-agnostic.

**Deployment & Installation (#9)** — Purely operational. ISO vs. container vs. VM doesn't change the agent architecture.

**Testing Strategy (#10)** — Important for validation, but doesn't change what you're building.

**Scheduler (#11)** — Affects the Processor module's implementation, but the concept of "prioritize and delegate" is sound regardless of scheduling algorithm.

**Failure Modes (#12)** — Need mitigation strategies, but the core architecture should handle failures gracefully with proper error handling.

**Success Metrics (#14)** — Defines "done" for v1, but doesn't change what v1 is.

**Integration Boundaries (#15)** — Scope decision, not architectural. Which tools to support first doesn't change the capability system.

### Gray Area Questions

These might affect architecture but probably don't change core ideas:

**User Interaction Model — Interruption/Cancellation**
- If users need to interrupt multi-step goals mid-execution, you might need:
  - Checkpointing (save partial progress)
  - Rollback (undo partial work)
  - Pause/resume (hold agent state)
- This affects the Executor and Processor, but the goal lifecycle can accommodate it (add "Paused" state?).

**Security Model — Threat Model**
- If the threat model includes "compromised LLM" (adversarial prompts that try to bypass safety), you might need:
  - Input validation on LLM outputs
  - Sandboxing of LLM-generated code
  - Multiple layers of validation
- This affects Governor implementation but not the "safety is architectural" principle.

**Scheduler — Proactive Behavior**
- If the system needs to be highly proactive (constantly working on background goals), you might need:
  - Resource reservation for background work
  - Priority inversion handling
  - Dynamic scaling of agent concurrency
- This affects Processor and resource management, but the goal-driven model is sound.

### Summary

**Core architecture is sound.** The five-module design (Goal Fulfillment, Governor, Processor, Memory, Self-Improvement) and the goal lifecycle are robust. Most questions are about *how* to implement, not *what* to implement.

**Watch for:** Concurrency coordination, self-improvement safety, and multi-user requirements. These are the most likely to require architectural adjustments.

**Recommendation:** Assume single-user for v1, implement resource locking for concurrency, and design self-improvement with explicit loop detection. Revisit if these prove insufficient.

---

## Critical Gaps

### 1. User Interaction Model

How does the user actually interact with HyperiOS day-to-day?

**Why this matters:** The vision describes a goal-driven system but doesn't specify the user's moment-to-moment experience. This is the biggest UX gap — every other module depends on how the user provides input and receives output.

**Core questions:**

**Primary interface:**
- What is the primary interface? TUI (bubbletea)? Voice (whisper/piper)? Both? Web UI?
- If TUI, what does the main screen look like? Chat-style? Dashboard? Split view?
- If voice, how do you handle ambiguity? ("organize my photos" vs. "organize my folders" — does it ask for clarification or guess?)
- Can the user switch between interfaces mid-conversation?
- Is there a fallback interface if the primary fails? (e.g., web UI if TUI crashes)

**Goal refinement conversations:**
- What does a goal refinement conversation look like in practice? Need concrete examples:
  - User: "Organize my photos"
  - Agent: "I see you have 15,000 photos in ~/Pictures. Do you want them organized by date, location, event, or something else?"
  - User: "By date, but keep the ones from trips together"
  - Agent: "Got it. I'll organize by date, with trip folders for photos taken within 3 days of each other at locations >50 miles from home. Sound right?"
  - *How many turns is acceptable? When does the agent stop asking and start doing?*
- How does the agent know when it has enough context to proceed?
- What happens if the user gives contradictory requirements? ("Organize by date but also by location")
- Can the user say "just do it and I'll tell you if it's wrong"?

**Goal status updates:**
- How are goal status updates surfaced without being intrusive?
  - Real-time notifications? (Annoying for long-running goals)
  - Periodic summaries? (User might miss important updates)
  - On-demand status checks? (User has to remember to ask)
  - Smart notifications? (Only notify on completion, failure, or when input needed)
- What's the format? ("Goal 'organize photos' is 40% complete" vs. "I've organized 6,000 of 15,000 photos so far")
- How does the user see the full history of what the agent did?

**Interruption and cancellation:**
- How does the user interrupt ongoing multi-step work?
  - Voice command? ("Stop what you're doing")
  - Keyboard shortcut? (Ctrl+C in TUI?)
  - GUI button? (Cancel button in dashboard?)
- What happens to partial work when a goal is cancelled?
  - Rollback everything? (Safe but wasteful)
  - Keep what's done? (Efficient but might leave inconsistent state)
  - Ask the user? (Intrusive but flexible)
- Can the user pause a goal and resume later?
- Can the user modify a goal mid-execution? ("Actually, organize by location instead")

**Feedback during execution:**
- How does the user provide feedback during execution?
  - "Don't do that" — how does the agent interpret this? (Stop current step? Stop entire goal? Avoid this approach in future?)
  - "Try this instead" — does the agent incorporate this into the current plan or start over?
  - "That looks good" — does this accelerate autonomy or just acknowledge?
- Can the user give feedback on sub-goals without affecting the parent goal?
- How does the agent distinguish between "I don't like this approach" and "I don't want this goal"?

**Clarification and assumptions:**
- What happens when the agent needs clarification?
  - Pause and wait for user input? (Blocks progress)
  - Continue with best guess and ask later? (Might do wrong work)
  - Fork: continue with assumption, but also ask user? (Complex)
- How does the agent decide what's worth asking about vs. what it can assume?
- What if the user doesn't respond to a clarification request? (Timeout? Abandon goal? Proceed with default?)

**Tool authorization UX:**
- What's the UX for tool authorization popups? (Always/Session/Request/No/Never)
  - Modal dialog that blocks everything? (Intrusive)
  - Non-blocking notification? (User might miss it)
  - Batch approvals? ("Approve all apt installs for this goal")
- How does the user review and revoke past authorizations?
- What happens if the user is away and a goal needs authorization? (Pause? Skip? Fail?)

**Design considerations:**
- The vision says "system internals are intended to be invisible to the user during normal operation" — but what does "normal operation" mean?
  - Does the user ever see the Goal Fulfillment / Governor / Processor distinction?
  - Or is it all just "the agent" from the user's perspective?
- How does the user inspect what the agent is doing when they want to?
  - "What are you working on right now?"
  - "Why did you decide to do X?"
  - "Show me everything you did in the last hour"
- Consider: different interaction modes for different contexts
  - Foreground mode: user actively watching, high interaction
  - Background mode: agent working autonomously, low interaction
  - Monitoring mode: user passively observing, medium interaction

**Concrete scenarios to design for:**
1. User gives a vague goal ("make my system better") — how does refinement work?
2. User gives a specific goal ("install nginx and configure it as a reverse proxy for port 8080") — does refinement still happen?
3. User gives a goal, agent starts, user realizes they meant something different — how does correction work?
4. Agent is working on a long goal (hours), user wants to check progress — what do they see?
5. Agent encounters an unexpected problem — how does it communicate this to the user?
6. User wants to see what the agent learned from a completed goal — how do they access this?

**Dependencies:** Affects all modules (Goal Fulfillment, Processor, Governor all need to communicate with user)

**Potential approaches:**
- **Chat-first:** Start with TUI chat interface (like current implementation), add voice later
- **Dashboard-first:** Build a dashboard showing active goals, status, history, with chat for input
- **Voice-first:** Prioritize voice interaction, use TUI as fallback
- **Hybrid:** Chat for complex goals, voice for simple commands, dashboard for monitoring

**Recommendation:** Start with TUI chat for v1 (simplest, already partially built), but design the interaction protocol so voice and dashboard can be added without changing core modules.

---

### 2. Concurrency & Resource Management

How does the system handle multiple concurrent agents and resource constraints?

**Why this matters:** The vision describes concurrent goals and sub-goals, but doesn't specify how multiple agents coordinate. This is the most likely question to challenge the "direct function calls, not event-driven" principle.

**Core questions:**

**Agent concurrency:**
- How many agents can run concurrently? 
  - Hard limit (e.g., max 5 agents)? 
  - Dynamic based on resources (e.g., as long as CPU < 80%)?
  - User-configurable?
- What types of concurrency are supported?
  - Multiple sub-goals of the same parent goal? (Parallel execution)
  - Multiple independent goals? (Background + foreground)
  - Multiple improvement goals? (Self-improvement running alongside user goals)
- Can a single goal have multiple agents working on it? (Parallel sub-goals)
- What's the overhead per agent? (Memory, CPU, LLM API calls)

**Resource limits:**
- What are the resource limits per agent?
  - CPU: percentage of cores? priority level?
  - Memory: hard limit (e.g., 512MB per agent)? soft limit with OOM killer?
  - Network bandwidth: rate limiting? connection limits?
  - Disk I/O: priority? bandwidth?
  - LLM API calls: rate limiting? token budget per agent?
- How do you prevent resource exhaustion when multiple agents are active?
  - Global resource pool that agents draw from?
  - Per-agent quotas?
  - Dynamic allocation based on priority?
- What happens when an agent exceeds its resource limit?
  - Throttle? (Slow down but continue)
  - Pause? (Hold until resources available)
  - Kill? (Terminate and report failure)

**Resource conflicts:**
- What happens when two agents try to modify the same resource?
  - Same file (e.g., both editing `/etc/nginx/nginx.conf`)
  - Same service (e.g., both restarting nginx)
  - Same package (e.g., both trying to install different versions)
  - Same port (e.g., both trying to bind to port 8080)
- What locking or coordination mechanisms exist?
  - File locks (flock)?
  - Resource locks in the Governor?
  - Serialization of conflicting agents?
  - Optimistic concurrency with conflict detection?
- How do you detect conflicts?
  - Static analysis of plans? (Check if two plans touch same resource)
  - Runtime detection? (Catch lock contention)
  - Post-hoc detection? (Notice inconsistent state)
- How do you resolve conflicts?
  - Priority-based (higher priority goal wins)?
  - First-come-first-served?
  - User intervention?
  - Automatic rollback of lower-priority agent?

**Agent lifecycle:**
- Can agents be paused/resumed, or only started/stopped?
  - If paused, where is state stored? (Memory? Disk?)
  - How long can an agent be paused? (Indefinitely? Timeout?)
  - What happens if an agent is paused while holding a lock?
- Can agents be migrated? (Moved to different process/machine?)
- What's the graceful shutdown procedure for an agent?
  - Finish current step?
  - Rollback partial work?
  - Save state for resumption?

**Coordination patterns:**
- How do agents coordinate when they have dependencies?
  - Sub-goal B depends on sub-goal A completing
  - Agent X needs result from Agent Y
  - Multiple agents need to synchronize at a checkpoint
- Do agents communicate directly or through the Processor?
- What's the coordination protocol?
  - Shared state (database)?
  - Message passing?
  - Locks and semaphores?

**Design considerations:**
- The Processor module handles "agent coordination" but the mechanism is undefined
- Need a clear model for conflict resolution
- Consider: optimistic concurrency vs. pessimistic locking vs. agent serialization
- **Key trade-off:** Simplicity (serialize all agents) vs. Performance (allow concurrency with coordination)
- **Risk:** If you allow too much concurrency without proper coordination, you get race conditions and inconsistent state
- **Risk:** If you serialize everything, you lose the benefits of concurrent goals

**Concrete scenarios to design for:**
1. User has goal "keep system secure" (background, runs daily) and goal "install new app" (foreground, runs now) — both need to run apt. How do they coordinate?
2. Goal "organize photos" spawns 5 sub-goals to process different folders in parallel. One sub-goal fails. What happens to the others?
3. Two goals both want to modify nginx config. Goal A wants to add a reverse proxy, Goal B wants to enable SSL. How do they coordinate?
4. Self-Improvement creates an improvement goal while user goals are running. Does it compete for resources or run at lower priority?
5. Agent A is installing a package (takes 5 minutes), Agent B needs that package to proceed. Does B wait, fail, or do something else?

**Dependencies:** Affects Processor (coordination), Governor (resource authorization), Executor (resource management)

**Potential approaches:**
- **Serialize everything:** Only one agent runs at a time. Simple, safe, but slow.
- **Resource locking:** Agents acquire locks before modifying resources. Prevents conflicts but adds complexity.
- **Optimistic concurrency:** Agents run freely, conflicts detected and resolved after the fact. Fast but risky.
- **Priority-based scheduling:** Higher priority goals get resources first, lower priority goals wait or get throttled.
- **Namespace isolation:** Each agent gets its own namespace (container, chroot) to prevent conflicts. Safe but heavyweight.

**Recommendation:** Start with resource locking for v1. Agents declare what resources they'll modify in their plan, Governor checks for conflicts, Processor serializes conflicting agents. This is simpler than optimistic concurrency and safer than serialization.

---

### 3. Error Handling & Recovery

What happens when things go wrong?

**Why this matters:** The vision describes the happy path (goal → refinement → execution → completion) but doesn't specify what happens when steps fail. Real systems fail constantly — LLM APIs timeout, packages fail to install, disk fills up, network drops. Without clear error handling, the system will be fragile.

**Core questions:**

**Partial completion:**
- What happens when execution fails mid-way through a plan?
  - Example: Plan has 10 steps, step 6 fails. What happens to steps 1-5?
  - Rollback everything? (Safe but wasteful)
  - Keep what's done and retry from step 6? (Efficient but might leave inconsistent state)
  - Ask the agent to re-plan from current state? (Flexible but complex)
- Are there rollback strategies for partial completion?
  - Automatic rollback (undo each step in reverse order)?
  - Manual rollback (user decides what to keep)?
  - Intelligent rollback (agent decides what's safe to undo)?
- How do you handle steps that can't be rolled back?
  - Sent an email (can't unsend)
  - Made an API call (can't undo external side effects)
  - Deleted a file (might not have backup)

**Persistence and recovery:**
- What state persists across system reboots?
  - Goals (active, blocked, refining)?
  - Agent state (what step they were on)?
  - Memory (learned patterns, context)?
  - Audit log (always)?
  - Tool authorizations (always/session/request)?
- How does the system recover after a crash?
  - Resume interrupted goals from last checkpoint?
  - Mark interrupted goals as "Blocked" and ask user what to do?
  - Abandon interrupted goals and start fresh?
- What's the checkpoint strategy?
  - Checkpoint after every step? (Safe but slow)
  - Checkpoint at key milestones? (Faster but might lose work)
  - No checkpoints, just re-plan from current state? (Flexible but complex)

**LLM API failures:**
- How does the system handle LLM API failures?
  - Retry with exponential backoff?
  - Fallback to local model?
  - Fail gracefully and ask user to retry later?
  - Continue with cached/simplified responses?
- What if the LLM returns garbage or refuses to respond?
  - Retry with different prompt?
  - Ask user for clarification?
  - Mark goal as blocked?
- What if the LLM is slow? (10+ seconds per response)
  - Timeout and retry?
  - Show "thinking..." to user?
  - Queue requests and process asynchronously?

**Governor disagreements:**
- What happens if the Governor and Goal Fulfillment disagree on a plan?
  - Governor rejects plan → Goal Fulfillment re-plans?
  - Governor rejects plan → Goal marked as blocked?
  - Governor rejects plan → User asked to override?
- What if the Governor rejects every plan for a goal?
  - Infinite re-plan loop?
  - Give up after N attempts?
  - Escalate to user?
- What if the Adversarial Agent finds risks that the Planner can't mitigate?
  - Accept the risk (with user approval)?
  - Abandon the goal?
  - Find a different approach?

**Infinite loops and runaway goals:**
- How does the system detect and recover from infinite goal loops?
  - Goal A creates sub-goal B, which creates sub-goal A?
  - Agent keeps retrying the same failing step?
  - Self-Improvement creates improvement goals that create more improvement goals?
- What's the circuit breaker?
  - Max retries per step?
  - Max sub-goals per goal?
  - Max depth of goal hierarchy?
  - Timeout per goal?
- How does the system detect that a goal is making no progress?
  - No state change in N minutes?
  - Same step retried N times?
  - Resource usage without output?

**Resource exhaustion:**
- What happens when disk space runs out?
  - Detect before it happens and warn user?
  - Pause all agents and ask user to free space?
  - Automatically clean up temp files?
  - Fail gracefully with clear error message?
- What happens when memory (RAM) runs out?
  - OOM killer terminates agents?
  - Swap to disk (slow but continues)?
  - Pause low-priority agents?
- What happens when network is unavailable?
  - Queue network-dependent goals?
  - Continue with non-network goals?
  - Fail and retry when network returns?

**Design considerations:**
- The vision mentions "audit trail is ground truth" but doesn't specify how it's used for recovery
- Need clear policies for: retry, rollback, escalate-to-user, abandon
- Consider: checkpointing for long-running goals
- **Key principle:** Fail gracefully, not silently. The user should always know what happened and what state the system is in.
- **Key trade-off:** Safety (rollback everything) vs. Efficiency (keep partial work)

**Concrete scenarios to design for:**
1. Agent is installing packages (step 3 of 10), network drops. What happens?
2. Agent is reorganizing files (step 5 of 20), system crashes. On reboot, what state is the goal in?
3. Agent tries to install a package that doesn't exist. Does it retry, skip, or fail the whole goal?
4. Governor rejects a plan 3 times. What happens on the 4th attempt?
5. Self-Improvement creates an improvement goal that fails. Does it try again or give up?
6. Agent is 90% done with a long goal, disk fills up. What happens to the 90%?

**Dependencies:** Affects all modules (Goal Fulfillment needs to track state, Governor needs to validate recovery plans, Processor needs to handle agent failures, Executor needs to support rollback)

**Potential approaches:**
- **Checkpoint and resume:** Save state after each step, resume from last checkpoint on failure
- **Transactional execution:** Each plan is a transaction, either fully completes or fully rolls back
- **Best-effort with reporting:** Do what you can, report what failed, let user decide
- **Re-plan on failure:** When a step fails, agent re-plans from current state (not from beginning)

**Recommendation:** Use re-plan on failure for v1. When a step fails, the agent gets the current state and the failure reason, and creates a new plan. This is more flexible than checkpoint/resume and simpler than transactions. Add checkpointing in v2 for long-running goals.

---

### 4. Autonomy Levels

What are the specific autonomy levels and what do they control?

**Why this matters:** The vision mentions autonomy levels multiple times but never defines them. This is a core safety mechanism — it determines what the agent can do without asking. Without clear levels, the Governor can't enforce appropriate constraints.

**Core questions:**

**Level definitions:**
- What are the exact autonomy levels?
  - How many levels? (3? 5? 10?)
  - What are they called? (Low/Medium/High? Supervised/Standard/Autonomous? Manual/Assisted/Automatic?)
  - What does each level mean in plain language?
- What capabilities are available at each level?
  - Level 1: Agent can only read files, cannot modify anything
  - Level 2: Agent can modify files in user's home directory, cannot touch system
  - Level 3: Agent can install packages, modify system configs (with approval)
  - Level 4: Agent can install packages, modify system configs (without approval)
  - Level 5: Agent can do anything except modify its own safety constraints
  - *These are examples — what's the actual breakdown?*

**Approval requirements:**
- What requires user approval at each level?
  - File modifications?
  - Package installations?
  - Service restarts?
  - Network connections?
  - System config changes?
  - Tool authorizations?
- How is approval requested?
  - Synchronous popup (blocks until user responds)?
  - Asynchronous notification (agent waits, user responds when convenient)?
  - Batch approval (approve multiple actions at once)?
- What happens if the user doesn't respond?
  - Timeout and fail?
  - Timeout and proceed with default (approve/deny)?
  - Wait indefinitely?

**Escalation and de-escalation:**
- How does autonomy escalation work?
  - User explicitly raises level? ("I trust you, do what you need to do")
  - Agent requests escalation? ("I need higher autonomy to complete this goal efficiently")
  - Automatic escalation after N successful goals? (Requires explicit consent per vision)
- How does de-escalation work?
  - User lowers level? ("Be more careful from now on")
  - Automatic de-escalation on failure? ("You messed up, I'm reducing your autonomy")
  - Temporary de-escalation for specific goals? ("Be careful with this one")
- Can autonomy level be different per goal?
  - System-wide level (all goals have same autonomy)?
  - Per-goal level (user sets autonomy for each goal)?
  - Per-capability level (different autonomy for different types of actions)?

**Default and initial state:**
- What's the default autonomy level for a fresh install?
  - Conservative (Level 1 or 2)?
  - Moderate (Level 3)?
  - Aggressive (Level 4 or 5)?
- How does the user change the autonomy level?
  - Command in TUI?
  - Config file?
  - Voice command?
  - GUI settings?
- Is the autonomy level visible to the user at all times?
  - Displayed in TUI status bar?
  - Shown in dashboard?
  - Only visible when asked?

**Enforcement:**
- How is autonomy level stored and enforced?
  - Config file? (Easy to modify)
  - Database? (Harder to modify)
  - Signed by user? (Tamper-evident)
- Who enforces the autonomy level?
  - Governor checks before execution?
  - Executor checks at runtime?
  - Both?
- Can the agent bypass autonomy constraints?
  - No (architectural constraint)?
  - Yes, with user approval?
  - Yes, in emergency situations? (e.g., system about to crash)

**Design considerations:**
- The vision says "automatic trust escalation requires explicit human consent" — how is this implemented?
- Need a clear matrix: autonomy level → allowed capabilities → approval requirements
- Consider: per-goal autonomy vs. system-wide autonomy
- **Key principle:** Autonomy level should be easy for users to understand and control
- **Key trade-off:** Safety (low autonomy, lots of approvals) vs. Efficiency (high autonomy, fewer interruptions)

**Concrete scenarios to design for:**
1. Fresh install, user gives first goal. What's the default autonomy? What approvals are needed?
2. User has been using system for months, all goals successful. Should autonomy increase automatically?
3. Agent at Level 3 encounters a goal that requires Level 4 capabilities. What happens?
4. User sets autonomy to Level 5, then agent makes a mistake. Should autonomy automatically decrease?
5. User wants high autonomy for "organize photos" but low autonomy for "modify system configs". Is this possible?
6. Agent needs to install a package to complete a goal. At what autonomy levels does it need approval?

**Dependencies:** Affects Governor (enforcement), Executor (capability checks), Goal Fulfillment (user communication about autonomy)

**Potential approaches:**
- **3 levels:** Low (read-only), Medium (modify with approval), High (modify freely)
- **5 levels:** Add "Supervised" (agent proposes, user approves every step) and "Autonomous" (agent does whatever, reports after)
- **Capability-based:** No levels, just per-capability approval settings
- **Goal-based:** Each goal has its own autonomy level set by user

**Recommendation:** Start with 3 levels for v1 (Low, Medium, High), system-wide setting, user can change anytime. Add per-goal autonomy in v2 if needed.

---

## Important Gaps

### 5. Data Model & Storage

How is persistent data stored and managed?

**Why this matters:** The vision describes persistent goals, long-term memory, and audit logs, but doesn't specify storage format or schema. This affects performance, reliability, and extensibility.

**Core questions:**

**Goal store:**
- What format is the goal store?
  - SQLite? (Relational, ACID, good for queries, single file)
  - YAML/JSON files? (Human-readable, easy to edit, no transactions)
  - Hybrid? (SQLite for metadata, files for large context)
  - Custom binary format? (Fast but opaque)
- What's the schema for goals?
  ```
  Goal {
    id: UUID
    parent_id: UUID (for sub-goals)
    description: string
    state: enum (Refining, Active, Done, Blocked, Cancelled)
    priority: int
    created_at: timestamp
    updated_at: timestamp
    context: JSON (observations, constraints, preferences)
    strategy: text (current approach)
    sub_goals: [UUID]
    progress_history: [ProgressEntry]
    last_activity: timestamp
    autonomy_level: int (optional, overrides system default)
  }
  ```
  - Is this the right schema? What's missing?
  - How do you query goals efficiently? ("Show me all active goals", "Find goals related to photos")
- How do you handle goal relationships?
  - Parent-child (sub-goals)?
  - Dependencies (goal B depends on goal A)?
  - Conflicts (goal A and goal B are mutually exclusive)?
  - Related goals (goal A and goal B are similar)?

**Memory module:**
- How is the Memory module implemented?
  - Vector database? (Semantic search over past executions)
  - Key-value store? (Fast lookup of facts)
  - Graph database? (Relationships between actions and outcomes)
  - Relational database? (Structured queries)
  - All of the above? (Different stores for different types of memory)
- What types of memory exist?
  - Episodic memory (specific events: "On 2024-01-15, I installed nginx")
  - Semantic memory (general knowledge: "nginx is a web server")
  - Procedural memory (how to do things: "To install nginx, run apt install nginx")
  - Working memory (current context for active goals)
- How do you decide what to remember vs. forget?
  - Time-based decay? (Forget old memories)
  - Relevance-based? (Forget memories that aren't queried often)
  - Importance-based? (Never forget critical memories)
  - User-controlled? (User can pin or delete memories)

**Audit log:**
- How is the audit log stored?
  - Append-only file? (Simple, tamper-evident, hard to query)
  - Database? (Queryable, but harder to make tamper-evident)
  - Both? (File for integrity, database for queries)
- What's the audit log format?
  ```
  AuditEntry {
    id: UUID
    timestamp: timestamp
    actor: string (which agent/module)
    action: string (what was done)
    target: string (what was affected)
    result: enum (success, failure, partial)
    details: JSON (additional context)
    goal_id: UUID (which goal this relates to)
    signature: hash (tamper-evident)
  }
  ```
- How do you query the audit log?
  - "Show me everything the agent did today"
  - "Show me all actions related to nginx"
  - "Show me all failures in the last week"
- How do you ensure audit log integrity?
  - Cryptographic signatures?
  - Append-only filesystem?
  - Regular backups?

**Schema evolution:**
- How do you handle schema evolution as the system grows?
  - Migration scripts?
  - Version field in each record?
  - Backward compatibility guarantees?
- What happens when you upgrade HyperiOS and the schema changes?
  - Automatic migration on first run?
  - Manual migration tool?
  - Export/import to new format?

**Backup and restore:**
- What's the backup strategy?
  - Automatic daily backups?
  - User-initiated backups?
  - Incremental vs. full backups?
- What needs to be backed up?
  - Goal store?
  - Memory?
  - Audit log?
  - Tool authorizations?
  - Directives?
- How does restore work?
  - Full system restore?
  - Selective restore (just goals, just memory)?
  - Point-in-time recovery?

**Disk layout:**
- Where is data stored on disk?
  ```
  /var/lib/hyperi/
    goals/           # Goal store
    memory/          # Memory module data
    audit/           # Audit log
    tools/           # Agent-created tools
    templates/       # Cached plan templates
    config/          # Runtime config (autonomy level, etc.)
  /etc/hyperi/
    directives-immutable.yaml
    directives-mutable.yaml
    allowlist.yaml
  ```
  - Is this the right layout?
  - What about user-specific data? (If multi-user in future)
  - What about temporary data? (Agent scratch space)

**Design considerations:**
- The vision mentions `/var/lib/hyperi/goals/` but doesn't specify format
- Need to balance: simplicity for v1 vs. extensibility for future
- Consider: SQLite for structured data, filesystem for large artifacts
- **Key principle:** Data should be durable, queryable, and inspectable by users
- **Key trade-off:** Simplicity (single SQLite file) vs. Flexibility (multiple specialized stores)

**Concrete scenarios to design for:**
1. User wants to see all active goals and their progress. How is this queried?
2. Agent needs to recall "how did I organize photos last time?" How is this retrieved?
3. User wants to audit what the agent did yesterday. How is this queried?
4. System crashes during goal execution. How is state recovered?
5. User wants to backup their HyperiOS state and restore on a new machine. How does this work?
6. Schema changes in v2. How are v1 goals migrated?

**Dependencies:** Affects all modules (all need to read/write persistent data)

**Potential approaches:**
- **SQLite only:** Single database for everything (goals, memory, audit). Simple, ACID, good queries.
- **Files only:** YAML/JSON files for everything. Human-readable, easy to edit, no transactions.
- **Hybrid:** SQLite for goals and audit, vector DB for memory, files for large artifacts.
- **Embedded DB:** Use an embedded database like Badger (Go key-value) or BBolt.

**Recommendation:** Use SQLite for v1. It's simple, ACID-compliant, good for queries, and a single file is easy to backup. Add specialized stores (vector DB for memory) in v2 if needed.

---

### 6. Security Model Details

How is security actually implemented at the OS level?

**Why this matters:** The vision says "safety is architectural, not behavioral" but doesn't specify the OS-level mechanisms. Without concrete security implementation, the Governor's constraints are just suggestions.

**Core questions:**

**OS-level security mechanisms:**
- What OS-level security mechanisms are used?
  - AppArmor? (Mandatory access control, profile-based)
  - SELinux? (Mandatory access control, label-based, more complex)
  - Namespaces? (Process isolation, resource limits)
  - Seccomp? (System call filtering)
  - Capabilities? (Fine-grained privileges, e.g., CAP_NET_BIND_SERVICE)
  - Cgroups? (Resource limits: CPU, memory, I/O)
  - User namespaces? (Run as unprivileged user)
- How are these configured?
  - Static profiles (predefined for HyperiOS)?
  - Dynamic profiles (generated based on goal)?
  - User-configurable?
- How do you prevent the agent from modifying its own security constraints?
  - Read-only security profiles?
  - Separate process for security management?
  - Kernel-level enforcement?

**Threat model:**
- What's the threat model? What are you defending against?
  - Malicious goals? (User gives goal that tries to bypass safety)
  - Compromised LLM? (LLM returns adversarial prompts that try to escape sandbox)
  - User error? (User accidentally gives dangerous goal)
  - External attacks? (Network-based attacks on running services)
  - Insider threats? (Malicious user with physical access)
- For each threat, what's the mitigation?
  - Malicious goals: Governor + Arbiter + directives
  - Compromised LLM: Input validation + sandboxing + multiple validation layers
  - User error: Clarification requests + confirmation for dangerous actions
  - External attacks: Firewall + network isolation + minimal attack surface
  - Insider threats: Full disk encryption + secure boot + audit log

**Secrets management:**
- How are secrets managed?
  - API keys (Anthropic, other services)
  - Passwords (database, services)
  - Tokens (OAuth, JWT)
  - SSH keys
- Where are secrets stored?
  - Environment variables? (Easy but visible in process list)
  - Encrypted file? (Secure but complex)
  - Secret manager (HashiCorp Vault, etc.)? (Secure but heavyweight)
  - Kernel keyring? (Linux-native, secure)
- How does the agent access secrets?
  - Injected at runtime?
  - Fetched from secret manager?
  - Read from encrypted file?
- Can the agent create new secrets?
  - Generate SSH keys?
  - Create API tokens?
  - Store passwords?

**Network security:**
- What are the network security boundaries?
  - Can agents make arbitrary outbound connections?
  - Can agents listen on arbitrary ports?
  - Can agents modify firewall rules?
- How is network access controlled?
  - Allowlist of allowed hosts? (api.anthropic.com, etc.)
  - Firewall rules? (iptables, nftables)
  - Network namespaces? (Isolate agent network)
- How do you handle network-dependent goals when offline?
  - Queue for later?
  - Fail gracefully?
  - Use cached data?

**Process isolation:**
- What user does the agent run as?
  - Root? (Full access, dangerous)
  - Dedicated `hyperi` user? (Limited access, safer)
  - Multiple users? (Different agents as different users)
- How do you prevent privilege escalation?
  - No setuid binaries?
  - No sudo access for agent user?
  - Capability dropping after startup?
- How do agents interact with system services?
  - systemctl (requires root or specific capabilities)?
  - D-Bus (requires specific permissions)?
  - Direct process management?

**File system security:**
- What files can the agent read/write?
  - User's home directory? (Read/write with user approval?)
  - System configs (/etc)? (Read always, write with approval?)
  - HyperiOS data (/var/lib/hyperi)? (Read/write always?)
  - Other users' data? (Never?)
- How is this enforced?
  - AppArmor/SELinux profiles?
  - File permissions?
  - Capability system checks?

**Design considerations:**
- The vision says "safety is architectural, not behavioral" — need concrete OS-level mechanisms
- Consider: defense in depth (multiple layers of security)
- Need to document what the agent CAN do vs. what it CANNOT do at the OS level
- **Key principle:** Security should be enforced by the OS, not by agent behavior
- **Key trade-off:** Security (restrictive) vs. Functionality (permissive)

**Concrete scenarios to design for:**
1. Agent tries to install a package that requires root. How is this handled securely?
2. Agent tries to modify /etc/passwd. How is this prevented?
3. LLM returns a prompt that tries to execute `rm -rf /`. How is this caught?
4. Agent needs to bind to port 80 (requires root). How is this done safely?
5. Agent tries to make outbound connection to malicious host. How is this prevented?
6. User wants to give agent access to sensitive file. How is this done securely?

**Dependencies:** Affects Governor (enforcement), Executor (capability checks), all agents (what they can do)

**Potential approaches:**
- **AppArmor profiles:** Define what each agent can access. Linux-native, well-tested.
- **Namespaces + seccomp:** Isolate agents in containers with minimal syscalls. Strong isolation.
- **Capability-based:** Use Linux capabilities for fine-grained privileges. Flexible but complex.
- **Hybrid:** AppArmor for file access, namespaces for process isolation, capabilities for specific privileges.

**Recommendation:** Use AppArmor for v1 (simpler, well-documented, Ubuntu-native). Add namespaces for agent isolation in v2. Document threat model clearly.

---

### 7. Observability & Debugging

How do you understand what the system is doing and debug problems?

**Why this matters:** Complex systems fail in complex ways. Without good observability, you can't debug failures, understand behavior, or validate that the system is working correctly. This is especially important for a system that's supposed to be "invisible" to users — when something goes wrong, you need to see inside.

**Core questions:**

**Debugging failed goals:**
- How do you debug a failed goal?
  - What information is available?
    - The goal description and context
    - The plan that was generated
    - Each step that was executed (success/failure)
    - Error messages and stack traces
    - Agent reasoning (why it chose this approach)
    - Governor review (why it approved/rejected)
  - How is this information presented?
    - Timeline view (step-by-step execution)?
    - Graph view (goal hierarchy and dependencies)?
    - Log view (raw audit log filtered for this goal)?
  - Can you replay a failed goal? (Re-execute with same inputs)
  - Can you step through a goal manually? (Execute one step at a time)

**Logging strategy:**
- What's the logging strategy?
  - Structured logs? (JSON with consistent fields)
  - Log levels? (DEBUG, INFO, WARN, ERROR, FATAL)
  - Log rotation? (Daily? Size-based? Retention policy?)
  - Log aggregation? (Central log file? Per-module logs?)
- What gets logged?
  - Goal lifecycle events (created, refined, executed, completed, failed)
  - Plan generation (what plan was created, why)
  - Step execution (what was executed, result, duration)
  - Governor decisions (approved/rejected, why)
  - LLM interactions (prompts, responses, token usage)
  - Resource usage (CPU, memory, network per agent)
  - Errors and exceptions (with full context)
- Where are logs stored?
  - /var/log/hyperi/?
  - Journalctl (systemd journal)?
  - Both?

**Metrics and monitoring:**
- What metrics are collected?
  - Goal success rate (overall, by type, by complexity)
  - Execution time (per goal, per step, per agent)
  - Resource usage (CPU, memory, network, disk)
  - LLM API usage (tokens, cost, latency)
  - Error rates (by type, by module)
  - Autonomy level distribution
  - Self-improvement effectiveness (improvement goals created, success rate)
- How are metrics exposed?
  - Prometheus endpoint?
  - Dashboard in TUI?
  - CLI commands? (`hyperi stats`)
- What alerts are configured?
  - High error rate?
  - Resource exhaustion?
  - Goal stuck (no progress for N minutes)?
  - Cost threshold exceeded?

**User-facing observability:**
- How does the user inspect what the agent did and why?
  - "What are you working on?" → List active goals with progress
  - "Why did you do X?" → Show reasoning and context for action X
  - "Show me everything you did today" → Filtered audit log
  - "What did you learn from goal Y?" → Show memory entries created
- What's the user-facing view vs. developer-facing view?
  - User: High-level status, simple explanations
  - Developer: Full logs, metrics, traces, debugging tools
- Can the user export logs/reports?
  - For sharing with support?
  - For personal record-keeping?

**Audit log querying:**
- What's the audit log format? Is it queryable?
  - SQL queries (if stored in database)?
  - Full-text search?
  - Filtered views (by goal, by time, by action type)?
- What queries are commonly needed?
  - "Show me all actions in the last hour"
  - "Show me all failed actions"
  - "Show me all actions that modified system configs"
  - "Show me the full history of goal X"
- Can the user add notes/comments to audit entries?
  - "This was wrong, don't do it again"
  - "This worked well"

**Distributed tracing:**
- How do you trace a goal through the system?
  - From user input to final result
  - Across multiple agents and modules
  - Including LLM calls, Governor reviews, execution steps
- What's the trace format?
  - OpenTelemetry?
  - Custom format?
- Can you visualize traces?
  - Timeline view?
  - Graph view (module interactions)?

**Developer tools:**
- What tools exist for developers to debug the system?
  - REPL for querying state?
  - Debug mode (verbose logging, step-by-step execution)?
  - Mock LLM (for testing without API calls)?
  - Replay tool (re-execute from audit log)?
  - Profiler (performance analysis)?

**Design considerations:**
- The vision says "the system cannot lie about what it did" — but how do you make that useful?
- Need both user-facing observability (simple) and developer-facing observability (detailed)
- Consider: distributed tracing for multi-agent goals
- **Key principle:** Observability should be built-in, not bolted-on
- **Key trade-off:** Verbosity (lots of data) vs. Performance (overhead of logging)

**Concrete scenarios to design for:**
1. User's goal failed. They want to know why. What do they see?
2. Developer is debugging a performance issue. What tools do they use?
3. User wants to understand why the agent chose a particular approach. How do they find out?
4. System is running slowly. How do you identify the bottleneck?
5. User wants to report a bug. What information do they provide?
6. Developer wants to validate that a new feature works correctly. How do they test it?

**Dependencies:** Affects all modules (all need to emit logs, metrics, traces)

**Potential approaches:**
- **Structured logging + CLI:** JSON logs, CLI commands to query. Simple, effective.
- **Full observability stack:** Prometheus + Grafana + Jaeger. Powerful but complex.
- **Built-in dashboard:** TUI dashboard showing real-time status. User-friendly but limited.
- **Hybrid:** Structured logs for developers, TUI dashboard for users, export for advanced analysis.

**Recommendation:** Use structured logging (JSON) for v1, add TUI dashboard for user-facing status. Add metrics and tracing in v2.

---

### 8. Performance & Cost

What are the performance expectations and cost constraints?

**Why this matters:** LLM APIs are expensive and slow. Without clear performance targets and cost controls, the system could be unusable (too slow) or unaffordable (too expensive). This is especially important for a system that's supposed to run continuously and autonomously.

**Core questions:**

**Response time expectations:**
- What are expected response times for different operations?
  - Goal refinement conversation: < 5 seconds per response?
  - Simple goal execution (single step): < 30 seconds?
  - Complex multi-step goal: minutes? hours? days?
  - Background goal (low priority): can run indefinitely?
  - Status query ("what are you working on?"): < 1 second?
  - Memory recall ("what did you learn about X?"): < 5 seconds?
- What's acceptable latency for different contexts?
  - Foreground (user waiting): < 5 seconds
  - Background (user not waiting): can be slow
  - Real-time (voice interaction): < 1 second
- How do you handle slow operations?
  - Show progress indicator?
  - Run asynchronously and notify when done?
  - Break into smaller steps?

**LLM API cost:**
- What's the LLM API cost budget?
  - Per day? (e.g., $10/day)
  - Per goal? (e.g., $1/goal for simple, $10/goal for complex)
  - Per month? (e.g., $300/month)
  - User-configurable?
- How do you track costs?
  - Per goal?
  - Per agent?
  - Per module (Goal Fulfillment, Governor, etc.)?
  - Per time period?
- How do you prevent runaway costs?
  - Token limits per goal?
  - Cost estimation before execution?
  - User approval for expensive goals?
  - Automatic pause when budget exceeded?
- What happens when budget is exceeded?
  - Pause all goals?
  - Pause low-priority goals?
  - Ask user to increase budget?
  - Switch to cheaper model?

**Caching strategy:**
- What's the caching strategy?
  - Cache LLM responses? (Same prompt → same response)
  - Cache plans? (Similar goal → similar plan)
  - Cache tool results? (Same command → same output)
  - Cache memory queries? (Same question → same answer)
- How long are things cached?
  - LLM responses: 1 hour? 1 day? Forever?
  - Plans: Until goal completes?
  - Tool results: Depends on command (ls vs. date)?
- How do you invalidate cache?
  - Time-based?
  - Event-based (file changed)?
  - Manual?

**Local vs. cloud models:**
- When do you use local models vs. cloud APIs?
  - Cloud for complex reasoning (planning, refinement)?
  - Local for simple tasks (classification, extraction)?
  - Cloud for everything (simpler but expensive)?
  - Local for everything (cheaper but lower quality)?
- What local models are supported?
  - Llama 2/3?
  - Mistral?
  - Custom fine-tuned models?
- What are the hardware requirements for local models?
  - RAM: 16GB? 32GB? 64GB?
  - GPU: Required? Optional? Which models?
  - CPU: How many cores?
- How do you decide which model to use?
  - User preference?
  - Automatic based on task complexity?
  - Cost-based (use local when possible, cloud when needed)?

**Optimization strategies:**
- How do you optimize for performance?
  - Parallelize independent sub-goals?
  - Prefetch context before it's needed?
  - Batch LLM calls?
  - Use streaming responses?
- How do you optimize for cost?
  - Use smaller models for simple tasks?
  - Cache aggressively?
  - Batch similar requests?
  - Use prompt compression?

**Hardware requirements:**
- What are the minimum hardware requirements?
  - CPU: 2 cores? 4 cores? 8 cores?
  - RAM: 8GB? 16GB? 32GB?
  - Disk: 50GB? 100GB? 500GB?
  - Network: Required? Optional?
  - GPU: Required? Optional?
- What are the recommended specs for good performance?
- Can HyperiOS run on a Raspberry Pi? (ARM support mentioned in AGENTS.md)
- Can HyperiOS run in a VM with limited resources?

**Design considerations:**
- The vision says "local-first, not cloud-dependent" — need concrete criteria for when to use each
- Consider: cost estimation before goal execution, user approval for expensive goals
- Need to balance: quality (cloud models) vs. cost/speed (local models)
- **Key principle:** System should degrade gracefully without network, not fail
- **Key trade-off:** Quality (expensive cloud models) vs. Cost (cheap local models)

**Concrete scenarios to design for:**
1. User gives a simple goal ("list files in directory"). How fast should it complete? How much should it cost?
2. User gives a complex goal ("organize my 50,000 photos"). How long can it take? How much can it cost?
3. Network is unavailable. What can the system still do?
4. User's API budget is $10/day. System has used $9 by noon. What happens?
5. Agent needs to choose between local model (fast, cheap, lower quality) and cloud model (slow, expensive, high quality). How does it decide?
6. User wants to run HyperiOS on a laptop with 16GB RAM and no GPU. What's possible?

**Dependencies:** Affects all modules (all use LLM, all have performance characteristics)

**Potential approaches:**
- **Cloud-only:** Use cloud APIs for everything. Simple, high quality, expensive.
- **Local-only:** Use local models for everything. Cheap, fast, lower quality.
- **Hybrid:** Cloud for complex tasks, local for simple tasks. Balanced but complex.
- **User-configurable:** Let user choose model and budget. Flexible but requires user expertise.

**Recommendation:** Use hybrid approach for v1. Cloud for planning and refinement (quality matters), local for execution and simple tasks (cost matters). User sets budget, system optimizes within it.

---

## Missing Practical Details

### 9. Deployment & Installation

How do users get HyperiOS running?

**Why this matters:** The best architecture is useless if users can't install and run the system. HyperiOS is a Linux distribution, which means it needs ISO builds, installation processes, and update mechanisms. This is especially challenging because HyperiOS is not just an app — it's an entire OS.

**Core questions:**

**Hardware requirements:**
- What are the minimum hardware requirements?
  - CPU: x86_64 only? ARM support? (AGENTS.md mentions ARM)
  - RAM: Minimum for basic operation? Minimum for local models?
  - Disk: How much space for base install? For goals/memory/audit?
  - Network: Required for installation? For operation?
- What are the recommended specs for good performance?
- What hardware is explicitly unsupported?

**Installation process:**
- What's the ISO installation process?
  - Live USB with installer (like Ubuntu)?
  - Network boot (PXE)?
  - VM image (OVA, VMDK, QCOW2)?
  - Container image (Docker, Podman)?
- What does the installer look like?
  - Graphical installer (Calamares, Ubiquity)?
  - Text-based installer (Debian installer, Subiquity)?
  - Automated installer (cloud-init, autoinstall)?
- What choices does the user make during installation?
  - Disk partitioning?
  - Timezone, locale, keyboard?
  - Username and password?
  - Autonomy level?
  - API key configuration?
- Can HyperiOS dual-boot with another OS?
- Can HyperiOS be installed alongside existing data?

**First-run experience:**
- What happens on first boot?
  - Welcome screen?
  - Initial setup wizard?
  - Jump straight to TUI?
- How does the user configure the system initially?
  - Set autonomy level?
  - Configure API keys?
  - Set up directives?
  - Import existing data?
- What's the first goal the user should try?
  - Guided tutorial?
  - Example goals?
  - Just let them figure it out?

**Updates and upgrades:**
- How do updates work?
  - apt (standard Ubuntu packages)?
  - Custom HyperiOS package repository?
  - Atomic updates (like Fedora Silverblue)?
  - Full re-install?
- How do you handle breaking changes?
  - Schema migrations?
  - Configuration updates?
  - Backward compatibility guarantees?
- How do you handle security updates?
  - Automatic updates?
  - User-approved updates?
  - Critical updates auto-applied, others manual?
- Can the agent update itself?
  - Self-Improvement creates update goals?
  - User must approve all updates?
  - Automatic for minor, manual for major?

**Testing and development:**
- Can HyperiOS run in a VM for testing?
  - Official VM images?
  - Vagrant boxes?
  - Cloud images (AWS, GCP, Azure)?
- Can HyperiOS run in a container?
  - Docker image for development?
  - Limitations of container vs. full install?
- How do developers test changes?
  - Local development environment?
  - CI/CD pipeline with VMs?
  - Staging environment?

**Migration:**
- How do you migrate from an existing Linux install to HyperiOS?
  - Import user data?
  - Import configurations?
  - Import installed packages?
- How do you migrate between HyperiOS versions?
  - In-place upgrade?
  - Fresh install + data migration?
- How do you migrate away from HyperiOS?
  - Export data in standard formats?
  - Documentation for manual migration?

**Design considerations:**
- The vision mentions "ISO builder (live-build)" but doesn't specify user experience
- Consider: multiple deployment targets (bare metal, VM, cloud, container)
- Need a clear onboarding flow for new users
- **Key principle:** Installation should be as easy as installing Ubuntu
- **Key trade-off:** Flexibility (many installation options) vs. Simplicity (one recommended path)

**Concrete scenarios to design for:**
1. User downloads HyperiOS ISO and installs on laptop. What's the process?
2. User wants to try HyperiOS without installing. What are the options?
3. User has been running v1.0, v1.1 is released. How do they update?
4. Developer wants to test changes locally. How do they set up dev environment?
5. User wants to migrate from Ubuntu to HyperiOS, keeping their data. How?
6. User wants to run HyperiOS on a Raspberry Pi. Is this possible?

**Dependencies:** Affects distro/ directory, all modules (need to work in installed environment)

**Potential approaches:**
- **Ubuntu-based ISO:** Use live-build to create Ubuntu-based ISO with HyperiOS pre-installed. Familiar, well-documented.
- **Custom distro:** Build from scratch with minimal dependencies. Lightweight but complex.
- **Container-first:** Docker image for development, ISO for production. Fast iteration but limited.
- **Cloud-first:** AMI/GCE image for cloud, ISO for bare metal. Modern but requires cloud account.

**Recommendation:** Use Ubuntu-based ISO for v1 (leverage existing tooling, familiar to users). Provide VM images for testing. Add container support in v2 for development.

---

### 10. Testing Strategy

How do you validate that the system works correctly and safely?

**Why this matters:** HyperiOS is a safety-critical system — it can modify system configs, install packages, and execute arbitrary commands. Without rigorous testing, bugs could lead to security vulnerabilities, data loss, or system instability. The self-improvement loop adds another layer of complexity — how do you test that improvements actually improve?

**Core questions:**

**Safety validation:**
- How do you validate that safety constraints actually work?
  - Governor correctly rejects dangerous plans?
  - Arbiter enforces autonomy levels?
  - Directives are respected?
  - Capability system prevents unauthorized actions?
- What's the testing approach for safety?
  - Unit tests for each safety component?
  - Integration tests for safety pipeline?
  - Adversarial testing (try to bypass safety)?
  - Formal verification (mathematically prove safety properties)?
- How do you test edge cases?
  - Plans that are safe individually but dangerous in sequence?
  - Plans that exploit race conditions?
  - Plans that use social engineering (LLM prompts that try to bypass safety)?

**End-to-end testing:**
- What's the end-to-end testing approach?
  - Simulated goals in sandbox environment?
  - Real goals on test machines?
  - User acceptance testing?
- What test scenarios are required?
  - Simple goal (single step, no dependencies)?
  - Complex goal (multiple steps, dependencies)?
  - Failing goal (step fails, recovery needed)?
  - Concurrent goals (multiple agents, resource conflicts)?
  - Long-running goal (hours/days)?
  - Self-improvement goal (system modifies itself)?
- How do you test without breaking real systems?
  - VMs for each test run?
  - Containers for isolation?
  - Mock OS layer?

**Acceptance criteria:**
- What does "v1 done" look like?
  - Feature complete (all modules implemented)?
  - Stable (no crashes, no data loss)?
  - Secure (passes safety tests)?
  - Performant (meets response time targets)?
  - Usable (users can accomplish goals)?
- What are the acceptance criteria for each module?
  - Goal Fulfillment: Can refine and track goals?
  - Governor: Can reject dangerous plans?
  - Processor: Can delegate and coordinate agents?
  - Memory: Can store and recall context?
  - Self-Improvement: Can create improvement goals?
- Who validates that criteria are met?
  - Automated tests?
  - Manual testing?
  - User feedback?

**Self-improvement testing:**
- How do you test the Self-Improvement loop without risking system stability?
  - Run in sandbox with no real effects?
  - Limit to safe improvements (prompt tuning, not code changes)?
  - Require user approval for all improvements?
  - Gradual rollout (test on subset of goals)?
- How do you validate that improvements actually improve?
  - A/B testing (old vs. new approach)?
  - Metrics comparison (before vs. after)?
  - User feedback?
- What happens if an improvement makes things worse?
  - Automatic rollback?
  - User notification?
  - Metrics alerting?

**Adversarial testing:**
- How do you test adversarial scenarios?
  - Malicious goals (goals that try to bypass safety)?
  - Edge cases (unusual inputs, resource exhaustion)?
  - Race conditions (concurrent modifications)?
  - LLM failures (garbage outputs, refusals)?
- Who performs adversarial testing?
  - Internal security team?
  - External penetration testers?
  - Bug bounty program?
  - Automated fuzzing?

**Regression testing:**
- What's the regression testing strategy?
  - Automated test suite run on every commit?
  - Nightly full test suite?
  - Manual testing before releases?
- How do you prevent regressions in safety-critical code?
  - Code review requirements?
  - Mandatory test coverage?
  - Formal verification?
- How do you handle test failures?
  - Block release until fixed?
  - Triage and prioritize?
  - Known issues list?

**Design considerations:**
- The vision mentions unit tests and integration tests, but not system-level validation
- Consider: formal verification for critical safety properties
- Need both automated tests and manual validation for v1
- **Key principle:** Safety must be tested, not just assumed
- **Key trade-off:** Test coverage (comprehensive) vs. Development speed (fast iteration)

**Concrete scenarios to design for:**
1. Developer changes Governor logic. How do they validate it still blocks dangerous plans?
2. New feature added. What tests are required before release?
3. User reports a bug. How do you reproduce and test the fix?
4. Self-Improvement creates an improvement. How do you validate it's safe and effective?
5. Security researcher finds a way to bypass Governor. How do you test the fix?
6. Preparing for v1.0 release. What's the final validation process?

**Dependencies:** Affects all modules (all need tests), CI/CD pipeline

**Potential approaches:**
- **Test pyramid:** Many unit tests, fewer integration tests, even fewer E2E tests. Fast, comprehensive.
- **Safety-first:** Extensive safety tests, minimal feature tests. Secure but slow.
- **User-driven:** Rely on user feedback and bug reports. Fast but risky.
- **Hybrid:** Automated tests for safety and core features, user feedback for UX.

**Recommendation:** Use hybrid approach for v1. Automated tests for safety (Governor, Arbiter, directives) and core features (goal lifecycle). Manual testing for UX. Add formal verification for critical safety properties in v2.

---

### 11. Scheduler

How does background work get triggered and managed?

**Why this matters:** The vision describes proactive behavior (agent works on goals without being prompted) but doesn't specify how this is triggered. Without a scheduler, the system is purely reactive — it only works when the user is actively interacting.

**Core questions:**

**Triggering mechanisms:**
- What triggers background work?
  - Time-based? (Run goal maintenance every day at 2am)
  - Event-based? (Run when new photos are added)
  - Goal-driven? (Active goals run continuously until done)
  - Resource-based? (Run when system is idle)
  - User-requested? (User says "work on this in the background")
- Can multiple triggers be combined?
  - "Run every day at 2am, but only if system is idle"
  - "Run when new photos are added, but not more than once per hour"

**Scheduler implementation:**
- How does the scheduler interact with the Processor?
  - Scheduler triggers goals, Processor executes them?
  - Scheduler is part of Processor?
  - Scheduler is separate module?
- What's the scheduling algorithm?
  - Priority queue (highest priority first)?
  - Round-robin (fair sharing)?
  - Resource-aware (schedule based on available resources)?
  - Deadline-based (schedule to meet deadlines)?
- How is the schedule persisted?
  - Database?
  - Config file?
  - systemd timers?
- How is the schedule managed?
  - User configures schedule?
  - Agent configures schedule (via Self-Improvement)?
  - Goals define their own schedule?

**Background vs. foreground:**
- How do you prevent background work from interfering with foreground work?
  - Resource limits for background work (max 50% CPU)?
  - Priority levels (foreground always higher than background)?
  - Pause background when foreground is active?
  - Separate resource pools?
- What happens when background work needs user input?
  - Pause and notify user?
  - Skip and retry later?
  - Fail and report?
- Can the user see what background work is running?
  - Dashboard showing background goals?
  - Notifications when background work completes?
  - Ability to pause/cancel background work?

**Conflict resolution:**
- What happens when scheduled work conflicts with user requests?
  - User request always wins?
  - Priority-based (some scheduled work is higher priority)?
  - User decides?
- What happens when two scheduled goals conflict?
  - Priority-based?
  - First-come-first-served?
  - Serialize?

**Persistence and recovery:**
- How is scheduled work persisted across reboots?
  - systemd timers (survive reboots)?
  - Database (restored on boot)?
  - Config file (static schedule)?
- What happens if scheduled work is missed?
  - Run immediately on next boot?
  - Skip and wait for next scheduled time?
  - Ask user?

**Design considerations:**
- The vision mentions "scheduler" but doesn't define it
- Consider: systemd timers, cron, or custom scheduler
- Need to balance: proactive behavior vs. resource usage
- **Key principle:** Background work should never interfere with user's current activity
- **Key trade-off:** Proactivity (more background work) vs. Resource usage (less background work)

**Concrete scenarios to design for:**
1. Goal "keep system secure" should run daily. How is this scheduled?
2. Goal "organize photos" should run when new photos are added. How is this triggered?
3. User is actively working, background goal wants to run. What happens?
4. System reboots, scheduled goal was supposed to run 2 hours ago. What happens?
5. Self-Improvement wants to schedule an improvement goal. How does it do this?
6. User wants to pause all background work for a week. How?

**Dependencies:** Affects Processor (execution), Goal Fulfillment (goal lifecycle), resource management

**Potential approaches:**
- **systemd timers:** Use systemd for scheduling. Linux-native, survives reboots, well-tested.
- **Custom scheduler:** Build scheduler into HyperiOS. Flexible but complex.
- **Goal-driven:** Goals define their own schedule, Processor manages execution. Simple but limited.
- **Hybrid:** systemd for time-based, inotify for event-based, Processor for coordination.

**Recommendation:** Use hybrid approach for v1. systemd timers for time-based scheduling, inotify for event-based triggers, Processor for coordination and resource management.

---

### 12. Failure Modes

What are the known failure modes and how does the system handle them?

**Why this matters:** Complex systems fail in complex ways. Without a catalog of failure modes and mitigation strategies, the system will fail unpredictably and be hard to debug. This is especially important for a system that's supposed to run autonomously.

**Core questions:**

**Infinite loops:**
- What happens when a goal enters an infinite loop?
  - Goal A creates sub-goal B, which creates sub-goal A?
  - Agent keeps retrying the same failing step?
  - Self-Improvement creates improvement goals that create more improvement goals?
- How do you detect infinite loops?
  - Max retries per step?
  - Max depth of goal hierarchy?
  - Cycle detection in goal graph?
  - Timeout per goal?
- What's the mitigation?
  - Circuit breaker (stop after N failures)?
  - User notification?
  - Automatic cancellation?

**Governor/Goal Fulfillment disagreements:**
- What happens when the Governor and Goal Fulfillment disagree repeatedly?
  - Governor rejects every plan for a goal?
  - Goal Fulfillment keeps re-planning, Governor keeps rejecting?
  - Infinite re-plan loop?
- How do you detect this?
  - Max re-plan attempts?
  - Timeout?
  - Pattern detection (same rejection reason)?
- What's the mitigation?
  - Mark goal as blocked?
  - Escalate to user?
  - Abandon goal?

**Resource exhaustion:**
- What happens when disk space runs out mid-execution?
  - Detect before it happens (check available space)?
  - Fail gracefully with clear error?
  - Automatically clean up temp files?
  - Pause and ask user to free space?
- What happens when memory (RAM) runs out?
  - OOM killer terminates agents?
  - Swap to disk (slow but continues)?
  - Pause low-priority agents?
- What happens when CPU is saturated?
  - Throttle agents?
  - Pause low-priority agents?
  - Queue new work?

**Network failures:**
- What happens when network becomes unavailable during a goal?
  - Queue network-dependent steps?
  - Fail and retry when network returns?
  - Skip network steps and continue?
  - Mark goal as blocked?
- What happens when LLM API is unavailable?
  - Retry with exponential backoff?
  - Fallback to local model?
  - Fail gracefully?
- What happens when network is slow?
  - Timeout and retry?
  - Use cached data?
  - Degrade gracefully?

**LLM failures:**
- What happens when an LLM returns garbage or refuses to respond?
  - Retry with different prompt?
  - Try different model?
  - Ask user for clarification?
  - Mark goal as blocked?
- What happens when LLM returns adversarial output?
  - Input validation on LLM outputs?
  - Governor catches it?
  - Multiple validation layers?
- What happens when LLM is slow (10+ seconds)?
  - Timeout and retry?
  - Show "thinking..." to user?
  - Queue and process asynchronously?

**Agent crashes:**
- What happens when a goal agent crashes?
  - Restart agent from last checkpoint?
  - Mark goal as failed?
  - Create new agent to continue work?
- How do you detect agent crashes?
  - Heartbeat mechanism?
  - Process monitoring?
  - Timeout?
- What state is preserved after crash?
  - Goal state (always)?
  - Agent state (if checkpointed)?
  - Partial work (depends on rollback policy)?

**System shutdown:**
- What happens when the system is shut down during goal execution?
  - Graceful shutdown (finish current step)?
  - Checkpoint and resume on next boot?
  - Mark goal as interrupted?
- How do you handle unexpected shutdown (power loss, crash)?
  - Recover from last checkpoint?
  - Detect inconsistent state and repair?
  - Ask user what to do?
- What happens on next boot?
  - Resume interrupted goals?
  - Notify user of interrupted goals?
  - Start fresh?

**Design considerations:**
- Need a failure mode catalog with mitigation strategies
- Consider: circuit breakers, timeouts, resource quotas
- Need clear policies for: retry, escalate, abandon
- **Key principle:** Fail gracefully, not silently. User should always know what happened.
- **Key trade-off:** Resilience (retry everything) vs. Safety (fail fast on unknown errors)

**Concrete scenarios to design for:**
1. Agent is installing packages, network drops. What happens?
2. Agent is reorganizing files, system crashes. On reboot, what state?
3. Governor rejects plan 10 times. What happens?
4. Self-Improvement creates infinite loop of improvement goals. How detected?
5. Disk fills up during long goal. What happens to partial work?
6. Agent crashes mid-execution. How is goal recovered?
7. System shut down during goal. What happens on next boot?
8. LLM API returns garbage. How does system handle it?

**Dependencies:** Affects all modules (all need failure handling)

**Potential approaches:**
- **Circuit breakers:** Stop after N failures, notify user.
- **Timeouts:** Fail after N minutes, mark as blocked.
- **Resource quotas:** Prevent resource exhaustion.
- **Checkpointing:** Save state periodically, resume from checkpoint.
- **Hybrid:** Use different strategies for different failure modes.

**Recommendation:** Use hybrid approach. Circuit breakers for infinite loops, timeouts for stuck goals, resource quotas for exhaustion, checkpointing for long-running goals. Document failure mode catalog with specific mitigations.

---

## Scope Questions

### 13. Multi-user Support

Is HyperiOS single-user or multi-user for v1?

**Why this matters:** Multi-user support fundamentally changes the architecture. It affects goal storage, permissions, audit logs, resource management, and security. If v1 needs multi-user, the scope increases significantly.

**Core questions:**

**v1 scope:**
- Is v1 single-user only?
  - Recommended: Yes, single-user for v1
  - Rationale: Simpler architecture, faster development, clearer security model
  - Risk: Limits use cases (no team/family scenarios)
- If single-user, is the architecture extensible to multi-user?
  - Can you add multi-user in v2 without major refactoring?
  - What design decisions now make multi-user easier later?

**Permission model (if multi-user):**
- If multi-user, what's the permission model?
  - Each user has own goals? (Isolated)
  - Shared goals? (Collaborative)
  - Both? (Some goals private, some shared)
- How do users authenticate?
  - Linux PAM (standard login)?
  - Custom authentication?
  - Biometric?
- What can one user see about another?
  - Goal descriptions?
  - Goal progress?
  - Audit log entries?
  - Memory/context?

**Conflict resolution:**
- How do you handle conflicting goals from different users?
  - User A wants to install package X, User B wants to remove it
  - User A wants to organize photos one way, User B wants another way
  - Priority-based? (Admin user wins?)
  - Negotiation? (Agent mediates between users?)
  - Isolation? (Each user's goals only affect their own data?)

**Autonomy levels:**
- How is autonomy level set per user?
  - Each user sets their own?
  - Admin sets for all users?
  - Per-user with admin override?
- Can one user's goal affect another user's data?
  - Never? (Strict isolation)
  - With permission? (User A approves access to User B's data)
  - Admin override? (Admin can grant cross-user access)

**Audit isolation:**
- Can one user see another user's audit log entries?
  - Never? (Privacy)
  - Admin only? (Oversight)
  - Always? (Transparency)
- How do you attribute actions to users?
  - Goal owner is responsible for agent actions?
  - Separate audit trail per user?

**Design considerations:**
- Single-user is simpler and probably right for v1
- If multi-user, need to think about: namespaces, permissions, audit isolation
- Consider: single-user v1, multi-user v2
- **Key principle:** Start simple, add complexity only when needed
- **Key trade-off:** Scope (multi-user is more useful) vs. Complexity (single-user is easier to build and secure)

**Concrete scenarios to design for:**
1. Single user on laptop. Simple case. How does this work?
2. Two users sharing a desktop. Each has own goals. How isolated are they?
3. Family of four using one HyperiOS machine. Parents want oversight of kids' goals. How?
4. Team of developers using HyperiOS. Shared project goals. How do they collaborate?
5. User A's goal accidentally affects User B's data. How is this prevented/handled?

**Dependencies:** Affects data model (goal store, memory, audit), security model, Governor (permissions)

**Potential approaches:**
- **Single-user only:** One user per HyperiOS instance. Simple, clear, limited.
- **Multi-user with isolation:** Each user has isolated namespace. Moderate complexity.
- **Multi-user with collaboration:** Users can share goals and data. High complexity.
- **Phased:** Single-user v1, isolated multi-user v2, collaborative multi-user v3.

**Recommendation:** Single-user for v1. Design data model with user_id field (even if always the same user) to make multi-user easier in v2.

---

### 14. Success Metrics

How do you know if v1 is successful?

**Why this matters:** Without clear success criteria, you can't know when v1 is "done" or whether it's working. This affects prioritization, testing, and release decisions.

**Core questions:**

**Use cases:**
- What use cases must v1 handle well?
  - System maintenance (keep system secure, up to date)?
  - File organization (organize photos, documents)?
  - Development assistance (set up dev environment, manage dependencies)?
  - Learning and exploration (research topics, summarize information)?
  - Automation (schedule tasks, monitor services)?
- Which use cases are "nice to have" vs. "must have"?
- What use cases are explicitly out of scope for v1?

**Key performance indicators:**
- What are the key performance indicators?
  - Goal success rate (percentage of goals completed successfully)?
  - User satisfaction (surveys, feedback)?
  - Time to completion (how long do goals take)?
  - Cost per goal (LLM API costs)?
  - System stability (uptime, crash rate)?
  - Safety incidents (goals that bypass safety, data loss)?
- What are the target values for each KPI?
  - Goal success rate: >80%? >90%? >95%?
  - User satisfaction: >4/5 stars?
  - Safety incidents: Zero?

**Self-improvement metrics:**
- How do you measure if the self-improvement loop is working?
  - Number of improvement goals created?
  - Success rate of improvement goals?
  - Measurable improvement in KPIs after improvements?
  - User perception ("does the system seem to get better?")
- What's the expected improvement rate?
  - 1% per week? 5% per month?
  - Diminishing returns over time?

**Minimum viable experience:**
- What's the minimum viable user experience?
  - User can give a goal and see it completed?
  - User can track progress of active goals?
  - User can cancel or modify goals?
  - User can see what the agent learned?
- What's the "wow moment" that convinces users this is valuable?
  - First successful complex goal?
  - Agent proactively does something useful?
  - System improves itself noticeably?

**Failure criteria:**
- What would make v1 a failure?
  - Safety incidents (agent bypasses safety, causes damage)?
  - Low goal success rate (<50%)?
  - Poor user experience (confusing, slow, unreliable)?
  - High cost (>$50/day for typical usage)?
  - No self-improvement (system doesn't get better)?
- What's the "pull the plug" criteria?
  - Fundamental architecture flaw?
  - Cannot achieve acceptable safety?
  - Cannot achieve acceptable performance?

**Validation process:**
- How do you validate success?
  - Internal testing (team uses system for real work)?
  - Pilot users (small group of external users)?
  - Public beta (open to anyone)?
- How do you collect feedback?
  - Surveys?
  - Interviews?
  - Usage analytics?
  - Bug reports?
- How do you iterate based on feedback?
  - Weekly releases?
  - Monthly releases?
  - Continuous deployment?

**Design considerations:**
- Need concrete, measurable success criteria
- Consider: pilot users, feedback loops, iteration cycles
- Need to define "done" for v1
- **Key principle:** Success should be measurable, not subjective
- **Key trade-off:** Ambition (high targets) vs. Realism (achievable targets)

**Concrete scenarios to design for:**
1. v1 is released. How do you know if it's successful after 1 month?
2. Goal success rate is 60%. Is this acceptable? What do you do?
3. Users report system is slow. How do you measure and improve?
4. Self-improvement creates 10 improvements, 8 succeed. Is this good?
5. Safety incident occurs (agent modifies system config without approval). What do you do?
6. User says "this is amazing, it saved me hours." How do you capture and replicate this?

**Dependencies:** Affects all modules (all contribute to success metrics), testing strategy, release process

**Potential approaches:**
- **Quantitative only:** Focus on measurable KPIs (success rate, cost, performance).
- **Qualitative only:** Focus on user feedback and satisfaction.
- **Hybrid:** Combine quantitative KPIs with qualitative feedback.
- **Phased:** Different metrics for alpha, beta, v1.0.

**Recommendation:** Use hybrid approach. Define quantitative KPIs (goal success rate >80%, zero safety incidents, cost <$20/day) and qualitative targets (users report system is useful and gets better over time). Validate with pilot users before v1.0 release.

---

### 15. Integration Boundaries

What's in scope for v1 and what's explicitly out?

**Why this matters:** HyperiOS can theoretically integrate with any Linux tool or service. Without clear boundaries, scope creep will delay v1 indefinitely. Need to decide what's in scope for v1 and what's explicitly deferred.

**Core questions:**

**In-scope tools and services:**
- Which Linux tools and services are in-scope for v1?
  - Package management: apt (install, remove, update packages)?
  - Service management: systemctl (start, stop, enable services)?
  - File system: basic file operations (read, write, move, delete)?
  - Process management: start/stop processes, monitor status?
  - Display management: sway (window manager, workspaces)?
  - Version control: git (clone, commit, push, pull)?
  - Network: basic network tools (ping, curl, wget)?
  - Shell: execute shell commands?
- Which are explicitly out-of-scope for v1?
  - Container orchestration: Docker, Kubernetes?
  - Cloud services: AWS, GCP, Azure?
  - Database management: PostgreSQL, MySQL, MongoDB?
  - Complex networking: VPN, firewall configuration?
  - Hardware management: GPU drivers, printer setup?
  - Multimedia: video editing, audio processing?

**Capability prioritization:**
- How do you prioritize which integrations to build first?
  - User demand (most requested features)?
  - Use case coverage (enables most use cases)?
  - Complexity (easiest to implement)?
  - Safety (lowest risk)?
- What's the process for adding new capabilities?
  - User requests capability?
  - Self-Improvement identifies need?
  - Developer adds based on roadmap?
- How do you validate new capabilities?
  - Safety review (Governor can handle it)?
  - Testing (works correctly)?
  - Documentation (users understand it)?

**Capability system:**
- How does the capability system work?
  - Static allowlist (predefined capabilities)?
  - Dynamic capabilities (agent can request new ones)?
  - User-approved capabilities (user grants permission)?
- What's the granularity of capabilities?
  - Coarse-grained (execute:shell = any shell command)?
  - Fine-grained (execute:shell:apt:install = only apt install)?
  - Context-aware (execute:shell in user's home directory)?
- How do you handle capabilities that don't exist yet?
  - Agent requests new capability?
  - Self-Improvement creates new capability?
  - User manually adds to allowlist?

**Third-party integrations:**
- Should HyperiOS integrate with third-party services?
  - GitHub (for git operations)?
  - Slack/Discord (for notifications)?
  - Email (for sending reports)?
  - Calendar (for scheduling)?
- If yes, how are these secured?
  - OAuth?
  - API keys?
  - Sandboxed execution?
- Who maintains these integrations?
  - HyperiOS team?
  - Community plugins?
  - Third-party developers?

**Extensibility:**
- How extensible is the capability system?
  - Can users add custom capabilities?
  - Can developers create plugins?
  - Can the agent create new capabilities?
- What's the API for adding capabilities?
  - Well-documented interface?
  - Plugin system?
  - Configuration only?

**Design considerations:**
- Need a clear "v1 capability list"
- Consider: start with core system management, expand from there
- Need to balance: usefulness vs. complexity
- **Key principle:** Start with core Linux operations, add integrations based on user demand
- **Key trade-off:** Scope (more capabilities = more useful) vs. Quality (fewer capabilities = better tested)

**Concrete scenarios to design for:**
1. User wants to install nginx. Is this in scope for v1?
2. User wants to deploy to Kubernetes. Is this in scope for v1?
3. User wants to send email notifications. Is this in scope for v1?
4. Agent identifies need for new capability (e.g., database management). How is this added?
5. User wants to add custom capability for their specific workflow. How?
6. Community wants to create plugin for Slack integration. How is this supported?

**Dependencies:** Affects Executor (capability implementation), Governor (capability authorization), allowlist.yaml

**Potential approaches:**
- **Minimal:** Only core Linux operations (apt, systemctl, file ops, shell). Safe, limited.
- **Moderate:** Core operations + common tools (git, network, display). Balanced.
- **Maximal:** Everything possible. Useful but complex and risky.
- **Phased:** Minimal for v1.0, moderate for v1.1, maximal for v2.0.

**Recommendation:** Moderate scope for v1. Include core Linux operations (apt, systemctl, file ops, shell, process management) plus git and basic network tools. Exclude containers, cloud services, databases, and complex networking. Add based on user feedback in v1.1+.

---

## Structural Issues in vision.md

### Repetition

The tool authorization flow is described three times:
- Lines 134-152 (Governor section)
- Lines 186-192 (Processor section)
- Lines 437-449 (Module Interaction Flow)

**Recommendation:** Define once, reference elsewhere.

### Over-specification for v1

The Self-Improvement architecture is detailed extensively (lines 234-264, 278-323, 727-746) but the document admits it's "not started."

**Recommendation:** Move detailed Self-Improvement design to a separate doc. Keep v1 vision focused on what's needed to ship.

### Under-specification for v1

The Processor module is "not started" but is required for MVP. The delegation model is vague.

**Recommendation:** Add concrete details on:
- How agents are spawned
- What context they receive
- How they report results
- How conflicts are resolved

---

## Recommendation: Add v1 Specification Section

The vision document is strong on *what* and *why*, but v1 needs more *how*. Add a **"v1 Specification"** section to `vision.md` that concretely defines:

1. **User interaction flows** — with concrete examples of real conversations
2. **Autonomy level definitions** — exact levels and what they control
3. **Data storage formats** — schemas for goals, memory, audit log
4. **Error handling policies** — what happens when things go wrong
5. **Performance targets** — response times, resource limits, cost budgets
6. **Success criteria** — measurable goals for v1
7. **Explicit v1 scope** — what's in, what's out

This will make the vision actionable and testable.

---

## Next Steps

### Immediate Actions (This Week)

1. **Review this document** — Read through all questions, identify which are most critical for your current development phase
2. **Prioritize questions** — Rank questions by:
   - Impact on v1 timeline (blocking vs. non-blocking)
   - Architectural significance (core-changing vs. implementation detail)
   - Dependencies (questions that unblock other questions)
3. **Assign owners** — For each critical question, assign someone responsible for driving resolution
4. **Schedule design sessions** — Block time to work through critical questions with stakeholders

### Short-term Actions (This Month)

5. **Resolve critical gaps** — Work through Questions #1-4 (User Interaction, Concurrency, Error Handling, Autonomy Levels)
   - These are blocking for v1 implementation
   - Create design docs for each with concrete decisions
6. **Update vision.md** — Add "v1 Specification" section with concrete answers to critical questions
7. **Create separate design docs** — For complex topics that need detailed specification:
   - `docs/design/user-interaction.md` — Detailed UX flows and examples
   - `docs/design/data-model.md` — Schema definitions and storage architecture
   - `docs/design/security-model.md` — Threat model and OS-level security implementation
   - `docs/design/autonomy-levels.md` — Level definitions and capability matrix

### Medium-term Actions (Next 2-3 Months)

8. **Resolve important gaps** — Work through Questions #5-8 (Data Model, Security, Observability, Performance)
   - These affect implementation but don't block initial development
   - Can be refined as you build and learn
9. **Build prototypes** — For questions with multiple potential approaches, build quick prototypes to validate
   - Example: Try SQLite vs. YAML for goal store, see which is easier to work with
   - Example: Implement basic resource locking, test with concurrent agents
10. **Validate with users** — Once you have working prototypes, test with real users
    - Do the interaction flows work?
    - Are autonomy levels intuitive?
    - Is the system useful for real goals?

### Long-term Actions (Next 6 Months)

11. **Resolve scope questions** — Finalize decisions on multi-user, success metrics, integration boundaries
    - These affect v1 scope and release criteria
    - Can be decided later without blocking development
12. **Address structural issues** — Clean up vision.md (remove repetition, move over-specified content to separate docs)
13. **Define v1 release criteria** — Based on success metrics, define what "v1 done" looks like
14. **Plan v2** — Based on v1 learnings, start planning v2 features (multi-user, advanced integrations, etc.)

### Decision Framework

When resolving questions, use this framework:

**For architectural questions:**
1. What are the options? (List 2-4 approaches)
2. What are the trade-offs? (Pros/cons of each)
3. What's the simplest option that could work? (Start simple, add complexity only if needed)
4. What's reversible? (Prefer decisions that can be changed later)
5. What's the risk if we're wrong? (Prefer low-risk options for v1)

**For scope questions:**
1. Is this required for v1 to be useful? (If no, defer to v2)
2. Is this a differentiator? (If yes, prioritize)
3. What's the implementation cost? (If high, defer unless critical)
4. What's the testing cost? (If high, defer unless critical)

**For design questions:**
1. What would a user expect? (Prefer intuitive over clever)
2. What's the simplest UX? (Prefer simple over powerful)
3. What's consistent with the vision? (Prefer aligned over expedient)
4. What's testable? (Prefer measurable over subjective)

### Tracking Progress

Use this checklist to track resolution of open questions:

**Critical Gaps:**
- [ ] #1 User Interaction Model — Resolved?
- [ ] #2 Concurrency & Resource Management — Resolved?
- [ ] #3 Error Handling & Recovery — Resolved?
- [ ] #4 Autonomy Levels — Resolved?

**Important Gaps:**
- [ ] #5 Data Model & Storage — Resolved?
- [ ] #6 Security Model Details — Resolved?
- [ ] #7 Observability & Debugging — Resolved?
- [ ] #8 Performance & Cost — Resolved?

**Missing Practical Details:**
- [ ] #9 Deployment & Installation — Resolved?
- [ ] #10 Testing Strategy — Resolved?
- [ ] #11 Scheduler — Resolved?
- [ ] #12 Failure Modes — Resolved?

**Scope Questions:**
- [ ] #13 Multi-user Support — Resolved?
- [ ] #14 Success Metrics — Resolved?
- [ ] #15 Integration Boundaries — Resolved?

**Structural Issues:**
- [ ] Remove repetition in vision.md
- [ ] Move over-specified content to separate docs
- [ ] Add detail to under-specified sections

---

## Summary

### Key Takeaways

1. **Core architecture is sound.** The five-module design (Goal Fulfillment, Governor, Processor, Memory, Self-Improvement) and goal lifecycle are robust. Most questions are about implementation, not architecture.

2. **Four questions could challenge core decisions:**
   - Concurrency coordination (might need event bus instead of direct calls)
   - Self-improvement safety (might need to defer to v2 if can't be made safe)
   - Multi-user support (fundamentally changes architecture if required for v1)
   - Autonomy levels (per-goal autonomy is more complex than system-wide)

3. **Most questions are implementation details.** Data format, deployment process, testing strategy, etc. are important but don't change the core vision.

4. **v1 needs more "how" and less "what."** The vision document is strong on philosophy and architecture but light on concrete specifications. Add a "v1 Specification" section with:
   - User interaction flows (with examples)
   - Autonomy level definitions
   - Data storage formats
   - Error handling policies
   - Performance targets
   - Success criteria
   - Explicit scope

5. **Start simple, add complexity.** For each question, prefer the simplest option that could work. You can always add complexity later, but removing complexity is hard.

### Recommended v1 Defaults

Based on the analysis, here are recommended defaults for v1:

- **User Interaction:** TUI chat interface (simplest, already partially built)
- **Concurrency:** Resource locking (agents declare resources, Processor serializes conflicts)
- **Error Handling:** Re-plan on failure (agent gets current state and failure reason, creates new plan)
- **Autonomy Levels:** 3 levels (Low, Medium, High), system-wide setting
- **Data Storage:** SQLite (simple, ACID, good queries, single file)
- **Security:** AppArmor profiles (Linux-native, well-tested, Ubuntu-friendly)
- **Observability:** Structured logging (JSON) + TUI dashboard for user-facing status
- **Performance:** Hybrid (cloud for planning/refinement, local for execution/simple tasks)
- **Deployment:** Ubuntu-based ISO (leverage existing tooling)
- **Testing:** Automated tests for safety, manual testing for UX
- **Scheduler:** Hybrid (systemd timers for time-based, inotify for event-based)
- **Multi-user:** Single-user only (design with user_id field for future extensibility)
- **Integrations:** Moderate scope (core Linux ops + git + basic network tools)

### Final Thoughts

This document contains 15 categories of open questions with hundreds of specific sub-questions. It's comprehensive, but don't let it paralyze you. 

**The vision is strong.** The architectural decisions in `vision.md` are sound and well-reasoned. These questions are about filling in the details, not rethinking the foundation.

**Prioritize ruthlessly.** Focus on the critical gaps first (Questions #1-4). These are blocking for v1 implementation. The other questions can be resolved as you build and learn.

**Build and iterate.** Don't try to answer every question before writing code. Build prototypes, test with users, and refine your answers based on real-world feedback.

**Document decisions.** As you resolve questions, update this document and create detailed design docs. Future you (and your team) will thank you.

**Ship v1.** Perfect is the enemy of done. Define clear success criteria, meet them, and ship. You can iterate in v1.1, v1.2, and v2.0.

---

*This document is a living artifact. Update as questions are resolved. Last updated: 2026-06-24.*
