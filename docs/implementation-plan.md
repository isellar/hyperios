# HyperiOS Implementation Plan

## Overview

This document provides a detailed implementation plan for HyperiOS, organized by module with explicit instructions for existing code. The plan is split into phases to allow incremental development and testing.

---

## Module Structure

### Core Modules (5)

1. **Goal Fulfillment** - Refine, break down, track goals; interact with user
2. **Governor** - Safety, directive enforcement, tool authorization, execution
3. **Processor** - Prioritize, delegate, spawn autonomous agents
4. **Memory** - Store and recall context; long-term knowledge base
5. **Self-Improvement** - Analyze results, create improvement goals

### Supporting Modules (2)

6. **I/O Toolbox** - User interface components presented as agent toolbox
7. **Infrastructure** - Shared utilities, configuration, audit logging

---

## Phase 1: Foundation

### 1.1 Infrastructure Setup

**Objective:** Establish shared utilities and configuration system.

**Actions:**

1. **Refactor `internal/config/`**
   - Keep existing `config.go` and `config_test.go`
   - Add directive storage:
     - Create `config/directives-immutable.yaml` with hardcoded safety directives
     - Create `config/directives-mutable.yaml` for user-modifiable directives
   - Add configuration for:
     - Goal storage paths
     - Memory storage paths
     - Tool authorization persistence
     - Agent spawning limits

2. **Refactor `internal/types/`**
   - Keep existing `types.go`
   - Add new types:
     ```go
     type Goal struct {
         ID          string
         State       GoalState // Refining, Active, Done, Blocked, Cancelled
         Description string
         SubGoals    []string
         CreatedAt   time.Time
         UpdatedAt   time.Time
     }
     
     type GoalState string
     const (
         GoalStateRefining  GoalState = "refining"
         GoalStateActive    GoalState = "active"
         GoalStateDone      GoalState = "done"
         GoalStateBlocked   GoalState = "blocked"
         GoalStateCancelled GoalState = "cancelled"
     )
     
     type Directive struct {
         ID          string
         Priority    int
         Description string
         Immutable   bool
     }
     
     type ToolAuthorization struct {
         ToolID      string
         Scope       string // "always", "session", "request"
         ExpiresAt   time.Time
         AuthorizedBy string
     }
     ```

3. **Refactor `internal/audit/`**
   - Keep existing `audit.go` and `audit_test.go`
   - Extend to log:
     - Goal state transitions
     - Tool authorization requests
     - Agent spawning events
     - Self-improvement goal creation

4. **Refactor `internal/llm/`**
   - Keep existing `client.go` and `completer.go`
   - This becomes a shared utility used by all modules
   - Add support for:
     - Multiple model providers
     - Token tracking
     - Cost tracking

5. **Remove `internal/bus/`**
   - Delete `bus.go` and `bus_test.go`
   - Per vision doc: no event bus, use direct function calls
   - Update any code that references the event bus to use direct calls

6. **Refactor `internal/module/`**
   - Keep existing `module.go`
   - This defines the Module interface for all modules
   - Extend with:
     ```go
     type Module interface {
         Name() string
         Health() ModuleHealth
         // Add module-specific methods as needed
     }
     ```

**Existing Code Status:**
- `internal/config/` - Keep and extend
- `internal/types/` - Keep and extend
- `internal/audit/` - Keep and extend
- `internal/llm/` - Keep as shared utility
- `internal/bus/` - Delete
- `internal/module/` - Keep and extend

---

### 1.2 Goal Fulfillment Module

**Objective:** Implement goal refinement, breakdown, and tracking.

**Actions:**

1. **Refactor `internal/agents/intent.go`**
   - Keep existing intent parsing logic
   - Rename to `internal/goal_fulfillment/refiner.go`
   - Extend to:
     - Interact with user to clarify intent
     - Gather context from Memory module
     - Use Processor to look up information during refinement
     - Transition goal from Refining to Active when ready

2. **Refactor `internal/agents/planner.go`**
   - Keep existing planning logic
   - Rename to `internal/goal_fulfillment/breakdown.go`
   - Extend to:
     - Break goals into sub-goals recursively
     - Validate sub-goals are actionable
     - Store breakdown in goal state

3. **Move `internal/agents/adversarial.go`**
   - Move to `internal/governor/adversarial.go`
   - This is part of Governor, not Goal Fulfillment
   - Keep existing adversarial logic

4. **Refactor `internal/plan/`**
   - Keep `writer.go`, `parser.go`, and tests
   - Rename to `internal/goal_fulfillment/tracker.go`
   - Extend to:
     - Track goal state transitions
     - Persist goal state to disk
     - Query goal state for reporting

5. **Create `internal/goal_fulfillment/goal_fulfillment.go`**
   - Main module file
   - Implements Module interface
   - Coordinates refinement, breakdown, and tracking
   - Provides public API:
     ```go
     type GoalFulfillment struct {
         refiner   *Refiner
         breakdown *Breakdown
         tracker   *Tracker
     }
     
     func (gf *GoalFulfillment) SubmitGoal(description string) (*Goal, error)
     func (gf *GoalFulfillment) GetGoal(id string) (*Goal, error)
     func (gf *GoalFulfillment) ListGoals(state GoalState) ([]*Goal, error)
     func (gf *GoalFulfillment) UpdateGoalState(id string, state GoalState) error
     ```

**Existing Code Status:**
- `internal/agents/intent.go` - Refactor and move to Goal Fulfillment
- `internal/agents/planner.go` - Refactor and move to Goal Fulfillment
- `internal/agents/adversarial.go` - Move to Governor
- `internal/plan/` - Refactor and move to Goal Fulfillment

---

### 1.3 Governor Module

**Objective:** Implement safety checks, directive enforcement, tool authorization, and execution.

**Actions:**

1. **Refactor `internal/arbiter/arbiter.go`**
   - Keep existing arbiter logic
   - Move to `internal/governor/arbiter.go`
   - Extend to:
     - Load directives from config files
     - Enforce directive priorities
     - Check goal compliance with directives

2. **Refactor `internal/capability/`**
   - Keep all files: `registry.go`, `enforcer.go`, `validator.go`, `matcher.go` and tests
   - Move to `internal/governor/capability/`
   - Extend to:
     - Support tool authorization with scopes (always, session, request)
     - Persist authorizations to disk
     - Check authorization before execution

3. **Refactor `internal/executor/`**
   - Keep all files: `interface.go`, `executor.go`, `local.go`, `container.go`
   - Move to `internal/governor/executor/`
   - Extend to:
     - Check tool authorization before execution
     - Fire popup for user approval when needed
     - Log execution results to audit

4. **Move `internal/agents/adversarial.go`**
   - Move to `internal/governor/adversarial.go`
   - Keep existing adversarial logic
   - Extend to:
     - Analyze goals for safety risks
     - Provide risk assessment to arbiter

5. **Create `internal/governor/tool_authorization.go`**
   - New file for tool authorization logic
   - Implements:
     ```go
     type ToolAuthorization struct {
         registry *capability.Registry
     }
     
     func (ta *ToolAuthorization) RequestAuthorization(toolID string, scope string) error
     func (ta *ToolAuthorization) CheckAuthorization(toolID string) bool
     func (ta *ToolAuthorization) RevokeAuthorization(toolID string) error
     func (ta *ToolAuthorization) FirePopup(toolID string, scope string) (string, error)
     ```

6. **Create `internal/governor/governor.go`**
   - Main module file
   - Implements Module interface
   - Coordinates arbiter, capability, executor, adversarial
   - Provides public API:
     ```go
     type Governor struct {
         arbiter      *Arbiter
         capability   *capability.Registry
         executor     *executor.Executor
         adversarial  *Adversarial
         auth         *ToolAuthorization
     }
     
     func (g *Governor) ReviewGoal(goal *Goal) (*ReviewResult, error)
     func (g *Governor) AuthorizeTool(toolID string, scope string) error
     func (g *Governor) ExecuteGoal(goal *Goal) (*ExecutionResult, error)
     ```

**Existing Code Status:**
- `internal/arbiter/` - Refactor and move to Governor
- `internal/capability/` - Refactor and move to Governor
- `internal/executor/` - Refactor and move to Governor
- `internal/agents/adversarial.go` - Move to Governor

---

## Phase 2: Core Logic

### 2.1 Processor Module

**Objective:** Implement goal prioritization, delegation, and agent spawning.

**Actions:**

1. **Refactor `internal/router/router.go`**
   - Keep existing routing logic
   - Move to `internal/processor/prioritizer.go`
   - Extend to:
     - Prioritize goals based on directives, state, timeline
     - Resolve conflicts between goals
     - Queue goals for delegation

2. **Refactor `internal/cache/plan_cache.go`**
   - Keep existing cache logic
   - Move to `internal/processor/cache.go`
   - Extend to:
     - Cache goal plans for reuse
     - Invalidate cache when goals change

3. **Refactor `internal/router/generator.go`**
   - Move to `internal/self_improvement/generator.go`
   - This is part of Self-Improvement, not Processor

4. **Refactor `internal/router/stats.go`**
   - Move to `internal/self_improvement/stats.go`
   - This is part of Self-Improvement, not Processor

5. **Refactor `internal/router/templates.go`**
   - Move to `internal/self_improvement/templates.go`
   - This is part of Self-Improvement, not Processor

6. **Create `internal/processor/delegation.go`**
   - New file for delegation logic
   - Implements:
     ```go
     type Delegation struct {
         agentSpawner *AgentSpawner
     }
     
     func (d *Delegation) DelegateGoal(goal *Goal) (*Agent, error)
     func (d *Delegation) MonitorAgent(agent *Agent) (*AgentStatus, error)
     func (d *Delegation) CancelDelegation(goalID string) error
     ```

7. **Create `internal/processor/agent_spawner.go`**
   - New file for agent spawning
   - Implements:
     ```go
     type AgentSpawner struct {
         llmClient *llm.Client
         processor *Processor
     }
     
     func (as *AgentSpawner) SpawnAgent(goal *Goal) (*Agent, error)
     func (as *AgentSpawner) TerminateAgent(agentID string) error
     func (as *AgentSpawner) ListAgents() ([]*Agent, error)
     ```

8. **Create `internal/processor/processor.go`**
   - Main module file
   - Implements Module interface
   - Coordinates prioritization, delegation, agent spawning
   - Provides public API:
     ```go
     type Processor struct {
         prioritizer *Prioritizer
         delegation  *Delegation
         spawner     *AgentSpawner
         cache       *Cache
     }
     
     func (p *Processor) QueueGoal(goal *Goal) error
     func (p *Processor) GetNextGoal() (*Goal, error)
     func (p *Processor) DelegateGoal(goal *Goal) (*Agent, error)
     func (p *Processor) LookupInfo(query string) (string, error)
     ```

**Existing Code Status:**
- `internal/router/router.go` - Refactor and move to Processor
- `internal/cache/` - Refactor and move to Processor
- `internal/router/generator.go` - Move to Self-Improvement
- `internal/router/stats.go` - Move to Self-Improvement
- `internal/router/templates.go` - Move to Self-Improvement

---

### 2.2 Memory Module

**Objective:** Implement context storage, recall, and long-term knowledge base.

**Actions:**

1. **Refactor `internal/session/`**
   - Keep `state.go`, `manager.go` and tests
   - Move to `internal/memory/session.go`
   - Extend to:
     - Store session context as part of memory
     - Query session history

2. **Refactor `internal/manifest/`**
   - Keep `manifest.go`, `watcher_linux.go`, `watcher_stub.go` and tests
   - Move to `internal/memory/world_model.go`
   - Extend to:
     - Store system manifest as world model
     - Query system state

3. **Create `internal/memory/long_term.go`**
   - New file for long-term memory
   - Implements:
     ```go
     type LongTermMemory struct {
         storagePath string
     }
     
     func (ltm *LongTermMemory) Store(key string, value interface{}) error
     func (ltm *LongTermMemory) Recall(key string) (interface{}, error)
     func (ltm *LongTermMemory) Search(query string) ([]MemoryResult, error)
     func (ltm *LongTermMemory) Forget(key string) error
     ```

4. **Create `internal/memory/indexer.go`**
   - New file for memory indexing
   - Implements:
     ```go
     type Indexer struct {
         memory *LongTermMemory
     }
     
     func (i *Indexer) IndexContent(content string) error
     func (i *Indexer) SearchIndex(query string) ([]IndexResult, error)
     func (i *Indexer) RebuildIndex() error
     ```

5. **Create `internal/memory/memory.go`**
   - Main module file
   - Implements Module interface
   - Coordinates session, world model, long-term memory, indexer
   - Provides public API:
     ```go
     type Memory struct {
         session    *Session
         worldModel *WorldModel
         longTerm   *LongTermMemory
         indexer    *Indexer
     }
     
     func (m *Memory) StoreContext(key string, value interface{}) error
     func (m *Memory) RecallContext(key string) (interface{}, error)
     func (m *Memory) SearchContext(query string) ([]MemoryResult, error)
     func (m *Memory) GetWorldModel() (*WorldModel, error)
     ```

**Existing Code Status:**
- `internal/session/` - Refactor and move to Memory
- `internal/manifest/` - Refactor and move to Memory

---

### 2.3 Self-Improvement Module

**Objective:** Implement pattern detection, improvement goal generation, and analysis.

**Actions:**

1. **Refactor `internal/router/generator.go`**
   - Keep existing template generation logic
   - Move to `internal/self_improvement/generator.go`
   - Extend to:
     - Analyze execution results for patterns
     - Generate improvement goals
     - Submit goals to Goal Fulfillment via direct function call

2. **Refactor `internal/router/stats.go`**
   - Keep existing stats tracking
   - Move to `internal/self_improvement/stats.go`
   - Extend to:
     - Track goal success/failure rates
     - Track tool usage patterns
     - Track agent performance

3. **Refactor `internal/router/templates.go`**
   - Keep existing template logic
   - Move to `internal/self_improvement/templates.go`
   - Extend to:
     - Store successful goal patterns
     - Reuse patterns for similar goals

4. **Create `internal/self_improvement/analyzer.go`**
   - New file for analysis logic
   - Implements:
     ```go
     type Analyzer struct {
         stats *Stats
     }
     
     func (a *Analyzer) AnalyzeGoalResults(results []GoalResult) (*Analysis, error)
     func (a *Analyzer) IdentifyPatterns(analysis *Analysis) ([]Pattern, error)
     func (a *Analyzer) GenerateImprovementGoals(patterns []Pattern) ([]Goal, error)
     ```

5. **Create `internal/self_improvement/self_improvement.go`**
   - Main module file
   - Implements Module interface
   - Coordinates generator, stats, templates, analyzer
   - Provides public API:
     ```go
     type SelfImprovement struct {
         generator  *Generator
         stats      *Stats
         templates  *Templates
         analyzer   *Analyzer
         goalFulfillment *goal_fulfillment.GoalFulfillment
     }
     
     func (si *SelfImprovement) AnalyzeResults(results []GoalResult) error
     func (si *SelfImprovement) GenerateImprovementGoals() ([]Goal, error)
     func (si *SelfImprovement) SubmitImprovementGoal(goal *Goal) error
     ```

**Existing Code Status:**
- `internal/router/generator.go` - Refactor and move to Self-Improvement
- `internal/router/stats.go` - Refactor and move to Self-Improvement
- `internal/router/templates.go` - Refactor and move to Self-Improvement

---

## Phase 3: I/O Toolbox

### 3.1 I/O Toolbox Module

**Objective:** Implement user interface components as agent toolbox.

**Actions:**

1. **Refactor `internal/shell/`**
   - Keep `shell.go`, `model.go`, `runner.go`, `styles.go`
   - Move to `internal/io_toolbox/shell/`
   - Extend to:
     - Provide shell interface as toolbox for agents
     - Allow agents to spawn interactive shells
     - Allow agents to read shell output

2. **Refactor `internal/ui/`**
   - Keep `server.go`, `window.go`, `controller.go`, `capture.go`
   - Move to `internal/io_toolbox/ui/`
   - Extend to:
     - Provide UI components as toolbox for agents
     - Allow agents to create windows, capture screens, inject input

3. **Refactor `internal/voice/`**
   - Keep `voice.go` and `voice_test.go`
   - Move to `internal/io_toolbox/voice/`
   - Extend to:
     - Provide voice interface as toolbox for agents
     - Allow agents to use STT/TTS

4. **Refactor `internal/display/`**
   - Keep `sway.go`, `atspi.go`, `capture.go` and tests
   - Move to `internal/io_toolbox/display/`
   - Extend to:
     - Provide display management as toolbox for agents
     - Allow agents to manage windows, capture screens

5. **Refactor `internal/scheduler/`**
   - Keep `scheduler.go` and `scheduler_test.go`
   - Move to `internal/io_toolbox/scheduler/`
   - Extend to:
     - Provide scheduling as toolbox for agents
     - Allow agents to schedule tasks

6. **Create `internal/io_toolbox/io_toolbox.go`**
   - Main module file
   - Implements Module interface
   - Coordinates shell, ui, voice, display, scheduler
   - Provides public API:
     ```go
     type IOToolbox struct {
         shell     *shell.Shell
         ui        *ui.UI
         voice     *voice.Voice
         display   *display.Display
         scheduler *scheduler.Scheduler
     }
     
     func (io *IOToolbox) GetShell() (*shell.Shell, error)
     func (io *IOToolbox) GetUI() (*ui.UI, error)
     func (io *IOToolbox) GetVoice() (*voice.Voice, error)
     func (io *IOToolbox) GetDisplay() (*display.Display, error)
     func (io *IOToolbox) GetScheduler() (*scheduler.Scheduler, error)
     ```

**Existing Code Status:**
- `internal/shell/` - Refactor and move to I/O Toolbox
- `internal/ui/` - Refactor and move to I/O Toolbox
- `internal/voice/` - Refactor and move to I/O Toolbox
- `internal/display/` - Refactor and move to I/O Toolbox
- `internal/scheduler/` - Refactor and move to I/O Toolbox

---

## Phase 4: Integration

### 4.1 Main Application

**Objective:** Integrate all modules and create main application.

**Actions:**

1. **Refactor `cmd/hyperi/main.go`**
   - Keep existing main logic
   - Extend to:
     - Initialize all modules
     - Wire modules together with direct function calls
     - Start main application loop
     - Handle shutdown gracefully

2. **Create `cmd/hyperi/wiring.go`**
   - New file for module wiring
   - Implements:
     ```go
     func WireModules() (*Modules, error) {
         // Initialize infrastructure
         config := config.NewConfig()
         audit := audit.NewAudit(config.AuditPath)
         llmClient := llm.NewClient(config.LLMConfig)
         
         // Initialize modules
         goalFulfillment := goal_fulfillment.NewGoalFulfillment(config, llmClient)
         governor := governor.NewGovernor(config, llmClient)
         processor := processor.NewProcessor(config, llmClient)
         memory := memory.NewMemory(config)
         selfImprovement := self_improvement.NewSelfImprovement(config, llmClient, goalFulfillment)
         ioToolbox := io_toolbox.NewIOToolbox(config)
         
         // Wire modules together
         goalFulfillment.SetMemory(memory)
         goalFulfillment.SetProcessor(processor)
         
         processor.SetGovernor(governor)
         processor.SetGoalFulfillment(goalFulfillment)
         
         selfImprovement.SetGoalFulfillment(goalFulfillment)
         
         return &Modules{
             GoalFulfillment: goalFulfillment,
             Governor:         governor,
             Processor:        processor,
             Memory:           memory,
             SelfImprovement:  selfImprovement,
             IOToolbox:        ioToolbox,
         }, nil
     }
     ```

3. **Create `cmd/hyperi/app.go`**
   - New file for main application loop
   - Implements:
     ```go
     type App struct {
         modules *Modules
     }
     
     func (app *App) Run() error {
         // Start main loop
         // Handle user input
         // Process goals
         // Monitor agents
         // Handle shutdown
     }
     ```

**Existing Code Status:**
- `cmd/hyperi/main.go` - Refactor and extend

---

## Phase 5: Testing & Refinement

### 5.1 Integration Testing

**Objective:** Test all modules working together.

**Actions:**

1. **Create integration tests**
   - Test goal submission flow
   - Test goal refinement flow
   - Test goal breakdown flow
   - Test goal execution flow
   - Test self-improvement flow
   - Test tool authorization flow

2. **Create end-to-end tests**
   - Test complete goal lifecycle
   - Test multiple concurrent goals
   - Test goal conflicts
   - Test agent failures
   - Test system recovery

3. **Performance testing**
   - Test goal processing throughput
   - Test agent spawning performance
   - Test memory recall performance
   - Optimize bottlenecks

4. **Security testing**
   - Test directive enforcement
   - Test tool authorization
   - Test agent isolation
   - Test audit logging

---

## Summary of Existing Code Disposition

### Code to Keep and Refactor

| Package | New Location | Action |
|---------|--------------|--------|
| `internal/config/` | `internal/config/` | Keep and extend |
| `internal/types/` | `internal/types/` | Keep and extend |
| `internal/audit/` | `internal/audit/` | Keep and extend |
| `internal/llm/` | `internal/llm/` | Keep as shared utility |
| `internal/module/` | `internal/module/` | Keep and extend |
| `internal/agents/intent.go` | `internal/goal_fulfillment/refiner.go` | Refactor and move |
| `internal/agents/planner.go` | `internal/goal_fulfillment/breakdown.go` | Refactor and move |
| `internal/plan/` | `internal/goal_fulfillment/tracker.go` | Refactor and move |
| `internal/arbiter/` | `internal/governor/arbiter.go` | Refactor and move |
| `internal/capability/` | `internal/governor/capability/` | Refactor and move |
| `internal/executor/` | `internal/governor/executor/` | Refactor and move |
| `internal/agents/adversarial.go` | `internal/governor/adversarial.go` | Move |
| `internal/router/router.go` | `internal/processor/prioritizer.go` | Refactor and move |
| `internal/cache/` | `internal/processor/cache.go` | Refactor and move |
| `internal/session/` | `internal/memory/session.go` | Refactor and move |
| `internal/manifest/` | `internal/memory/world_model.go` | Refactor and move |
| `internal/router/generator.go` | `internal/self_improvement/generator.go` | Refactor and move |
| `internal/router/stats.go` | `internal/self_improvement/stats.go` | Refactor and move |
| `internal/router/templates.go` | `internal/self_improvement/templates.go` | Refactor and move |
| `internal/shell/` | `internal/io_toolbox/shell/` | Refactor and move |
| `internal/ui/` | `internal/io_toolbox/ui/` | Refactor and move |
| `internal/voice/` | `internal/io_toolbox/voice/` | Refactor and move |
| `internal/display/` | `internal/io_toolbox/display/` | Refactor and move |
| `internal/scheduler/` | `internal/io_toolbox/scheduler/` | Refactor and move |

### Code to Delete

| Package | Reason |
|---------|--------|
| `internal/bus/` | Per vision doc: no event bus, use direct function calls |

### Code to Create

| Package | Purpose |
|---------|---------|
| `internal/goal_fulfillment/goal_fulfillment.go` | Main module file |
| `internal/governor/tool_authorization.go` | Tool authorization logic |
| `internal/governor/governor.go` | Main module file |
| `internal/processor/delegation.go` | Delegation logic |
| `internal/processor/agent_spawner.go` | Agent spawning |
| `internal/processor/processor.go` | Main module file |
| `internal/memory/long_term.go` | Long-term memory |
| `internal/memory/indexer.go` | Memory indexing |
| `internal/memory/memory.go` | Main module file |
| `internal/self_improvement/analyzer.go` | Analysis logic |
| `internal/self_improvement/self_improvement.go` | Main module file |
| `internal/io_toolbox/io_toolbox.go` | Main module file |
| `cmd/hyperi/wiring.go` | Module wiring |
| `cmd/hyperi/app.go` | Main application loop |

---

## Key Design Decisions

1. **No Event Bus** - Use direct function calls between modules per vision doc
2. **I/O as Toolbox** - Present I/O components as agent toolbox, not hardcoded
3. **Executor in Governor** - Executor is part of Governor for tool authorization
4. **Directives in Config** - Two config files: immutable and mutable
5. **Self-Improvement Submits Goals** - Self-Improvement submits goals to Goal Fulfillment via direct function call
6. **Autonomous Agents** - Processor spawns autonomous agents that can use Processor to look up info
7. **Goal State Transitions** - Refining → Active when agents stop seeking info from user

---

## Next Steps

1. Review this implementation plan
2. Approve or request changes
3. Begin Phase 1 implementation
4. Iterate through phases with regular check-ins

---

## Implementation Status

*Updated after Phase 5.2 (End-to-End Tests and Refinement)*

### Core Modules

| Module | Package Path | Tests | Status |
|---|---|---|---|
| Goal Fulfillment | `internal/goal_fulfillment` | 33 | Complete |
| Governor | `internal/governor` | 84 | Complete |
| Processor | `internal/processor` | 22 | Complete |
| Memory | `internal/memory` | 27 | Complete |
| Self-Improvement | `internal/self_improvement` | 20 | Complete |

### Infrastructure & Integration

| Component | Package Path | Tests | Status |
|---|---|---|---|
| End-to-End / Wiring | `cmd/hyperi` | 7 | Complete |
| Integration Tests | `internal/integration` | 20 | Complete |
| Audit Logger | `internal/audit` | 8 | Complete |
| Config | `internal/config` | 5 | Complete |
| Capability Registry | `internal/governor/capability` | 38 | Complete |
| Session Manager | `internal/session` | 14 | Complete |
| Event Notifier | `internal/events` | 7 | Complete |
| Plan Parser/Writer | `internal/plan` | 18 | Complete |
| Scheduler | `internal/scheduler` | 7 | Complete |
| Router / Templates | `internal/router` | 22 | Complete |
| Manifest | `internal/manifest` | 11 | Complete |
| Cache | `internal/cache` | 6 | Complete |
| Voice (stub) | `internal/voice` | 5 | Complete |
| Display (stub) | `internal/display` | 3 | Complete |
| I/O Toolbox | `internal/io_toolbox` | 11 | Complete |

**Total tests: 452 (all passing)**

### Fixes Applied in Phase 5.2

| Bug | Location | Fix |
|---|---|---|
| `QueueGoal` never activated goals — `RunNext` always returned nil | `internal/processor/processor.go` | `QueueGoal` now transitions goal state `Refining → Active` before enqueuing, so `Prioritizer.Next()` can dequeue it |
| Nil `*llm.Client` passed as `llm.Completer` caused panic in `Analyzer.AnalyzeResults` | `internal/self_improvement/self_improvement.go`, `analyzer.go` | `NewSelfImprovement` now correctly stores a true nil interface when client pointer is nil; `AnalyzeResults` checks for nil Completer and returns an error rather than panicking |
