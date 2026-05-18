# Fast-Path Intent Processing — Design Plan

## Problem

Every user intent — even "what time is it?" or "close this window" — currently passes through three LLM calls (Intent → Planner → Adversarial) before any execution. This adds 5-13 seconds of latency for operations that should take milliseconds.

For an agentic OS where users will make frequent mundane requests (resize windows, open apps, check status), this is unacceptable.

## Solution: Tiered Execution Model

Inspired by JIT compiler tiered compilation, voice assistant intent routing, and Home Assistant's deterministic automation engine.

### Tiers

| Tier | Name | Trigger | Pipeline Skipped | Latency Target |
|------|------|---------|------------------|----------------|
| 0 | **Novel** | Unknown intent | None | 5-13s (unchanged) |
| 1 | **Cached** | Exact intent match seen before | Planner + Adversarial | 2-4s |
| 2 | **Templated** | Pattern match in template registry | Intent + Planner + Adversarial | <1s |
| 3 | **Trusted** | High-frequency, proven-safe, autonomy 4 | All LLM stages | <100ms |

**Policy Arbiter always runs at every tier** — safety is non-negotiable.

### Promotion/Demotion Rules

- **Promote 0→1**: After 1 successful execution, cache the intent→plan mapping
- **Promote 1→2**: After 3 successful executions, extract a template with parameterized slots
- **Promote 2→3**: After 50 successful executions at autonomy level 4
- **Demote**: Any failure drops one tier; system change (OS update, package removed) triggers guard failure → demote

### Guard Conditions

Before executing a cached/templated plan, verify assumptions:
- Required binaries exist
- Network connectivity (if needed)
- Service/package state matches expectations
- If any guard fails → deoptimize to full pipeline

## Architecture

```
User Intent
    │
    ▼
┌─────────────────────────┐
│    IntentRouter          │  ← New: internal/router/router.go
│  1. Exact cache lookup   │
│  2. Template pattern match│
│  3. Fallback to pipeline │
└──────┬──────────────────┘
       │
   ┌───┴───┐
   │Match? │
   └───┬───┘
  Yes  │   No
   │   │
   ▼   ▼
┌──────────────┐  ┌─────────────────────┐
│ PlanCache    │  │ Full Pipeline       │
│ + SlotFill   │  │ (Intent→Planner→    │
│ + Guards     │  │  Adversarial→Arbiter)│
└──────┬───────┘  └──────────┬──────────┘
       │                     │
       ▼                     ▼
┌──────────────────────────────────────────┐
│        Policy Arbiter (always runs)      │
└──────────────────┬───────────────────────┘
                   ▼
┌──────────────────────────────────────────┐
│        Executor                          │
│  + Update PlanCache on success           │
│  + Update IntentStats for promotion      │
└──────────────────────────────────────────┘
```

## Components

### 1. IntentRouter (`internal/router/router.go`)

Entry point that replaces direct pipeline invocation. Routes intents based on:

1. **Exact match** against cached intent→plan mappings
2. **Pattern match** against template registry (regex with named capture groups)
3. **Fallback** to full pipeline for novel intents

```go
type Router struct {
    cache    *PlanCache
    registry *TemplateRegistry
    stats    *IntentStats
    fallback PipelineRunner
}

func (r *Router) Route(ctx context.Context, intent string) error {
    // Tier 2: Template match
    if tmpl, slots := r.registry.Match(intent); tmpl != nil {
        plan := tmpl.Fill(slots)
        return r.executeWithArbiter(plan, intent)
    }
    // Tier 1: Exact cache match
    if cached, ok := r.cache.Get(intent); ok {
        if cached.GuardCheck() {
            return r.executeWithArbiter(cached.Plan, intent)
        }
        r.cache.Remove(intent) // guard failed, remove stale entry
    }
    // Tier 0: Full pipeline
    return r.fallback(ctx, intent)
}
```

### 2. PlanCache (`internal/cache/plan_cache.go`)

Stores successful intent→plan mappings with metadata:

```go
type CachedPlan struct {
    Intent      string
    Plan        *types.ActionPlan
    Hits        int
    LastUsed    time.Time
    SuccessRate float64
    Guards      []Guard
}

type Guard struct {
    Check       func() bool
    Description string
}
```

Persistence: JSON file at `~/.local/share/hyperi/cache/plans.json`

### 3. TemplateRegistry (`internal/router/templates.go`)

Pre-defined templates for common operations with slot filling:

```yaml
# config/templates.yaml
templates:
  install_package:
    patterns:
      - "install (?P<package>.+)"
      - "get (?P<package>.+)"
      - "set up (?P<package>.+)"
    plan:
      steps:
        - id: check
          description: "Check if {package} is installed"
          capability: {type: "execute:shell", scope: "dpkg-query"}
          command: ["dpkg-query", "-W", "-f=${Version}", "{package}"]
          on_failure: skip
        - id: install
          description: "Install {package}"
          capability: {type: "execute:package", scope: "apt:{package}"}
          command: ["sudo", "apt-get", "-y", "install", "{package}"]
          on_failure: halt
          depends_on: [check]

  open_url:
    patterns:
      - "open (?P<url>https?://\\S+)"
      - "go to (?P<url>https?://\\S+)"
      - "visit (?P<url>https?://\\S+)"
    plan:
      steps:
        - id: open
          description: "Open {url} in browser"
          capability: {type: "ui:open", scope: "browser"}
          command: ["xdg-open", "{url}"]
```

### 4. IntentStats (`internal/router/stats.go`)

Tracks execution statistics for promotion/demotion decisions:

```go
type IntentStats struct {
    Intent       string
    Tier         int       // current tier (0-3)
    Count        int       // total executions
    SuccessCount int       // successful executions
    Failures     []string  // recent failure reasons
    LastUsed     time.Time
    AvgDuration  time.Duration
}
```

### 5. TemplateGenerator (`internal/router/generator.go`) — Phase 5

Extracts templates from successful plans:
- After N successful executions of similar intents
- Generalizes concrete values to slots
- Proposes template for user approval
- Adds to registry on approval

## Implementation Phases

### Phase 1: Intent Router + Exact-Match Cache — ✓ COMPLETE
- `internal/router/router.go` — Router with fallback to existing pipeline
- `internal/cache/plan_cache.go` — In-memory cache with JSON persistence
- `internal/router/stats.go` — Intent statistics tracking
- Integrate into `internal/shell/runner.go` — wrap `NewPipelineRunner` with router
- Cache successful plans after execution
- **Result**: Repeated identical intents skip Planner + Adversarial

### Phase 2: Template Registry — ✓ COMPLETE
- `config/templates.yaml` — 12 initial template definitions (package management, service control, URL opening, disk/memory/process checks, system update)
- `internal/router/templates.go` — Template matching with regex slot filling
- **Result**: Common pattern-matched intents skip all LLM stages

### Phase 3: Promotion/Demotion — ✓ COMPLETE
- Automatic tier promotion based on stats thresholds (1→cached, 3→templated, 50→trusted)
- Guard conditions for cached plans (binary existence checks)
- Demotion on failure
- **Result**: System automatically optimizes frequently-used intents

### Phase 4: Semantic Similarity — NOT STARTED
- Embedding-based intent matching
- Fuzzy matching for "install nginx" ≈ "set up nginx web server"
- Semantic cache with similarity threshold
- **Result**: Near-duplicate intents get fast-path treatment
- **Status**: Deferred — templates + exact cache cover 80%+ of repeated intents. Semantic similarity provides modest value for the complexity. Revisit if user feedback shows frequent false negatives.

### Phase 5: Self-Improvement (Template Generation) — ✓ COMPLETE
- **Phase 5A**: Single-slot template generation — `internal/router/generator.go` extracts templates from clustered plans
- **Phase 5B**: Multi-slot template generation — handles templates with up to 3 variable positions
- **Phase 5C**: Self-tuning + lifecycle management — Generator implements `Report`/`Tune`/`Health` (first Module interface implementation), auto-adjusts parameters, promotes/demotes/retires templates
- **CLI**: `hyperi templates generate/pending/approve/reject/stats/tune/retire/promote`
- **Result**: System learns new patterns without manual template definition, self-tunes for optimal performance

## Integration Points

### runner.go Changes

The `NewPipelineRunner` function becomes the fallback. A new `NewRoutedRunner` wraps it:

```go
func NewRoutedRunner(rc RunnerConfig) PipelineRunner {
    fallback := NewPipelineRunner(rc)
    router := router.NewRouter(router.Config{
        CachePath:    rc.DataPathFn("cache/plans.json"),
        TemplatePath: "config/templates.yaml",
        StatsPath:    rc.DataPathFn("cache/stats.json"),
        Fallback:     fallback,
    })

    return func(intent, _ string) error {
        return router.Route(context.Background(), intent)
    }
}
```

### Executor Changes

After successful execution, the executor (or runner) notifies the cache:

```go
// In runPipeline after successful execution:
if result.Success {
    router.Cache().RecordSuccess(intent, agentPlan)
    router.Stats().RecordExecution(intent, result.Duration)
}
```

## Self-Improvement Workflow

The system improves itself through:

1. **Observation**: Every successful execution is recorded
2. **Pattern Detection**: Similar intents with similar plans are grouped
3. **Template Extraction**: Concrete values generalized to slots
4. **Validation**: New templates tested against historical intents
5. **Deployment**: Approved templates added to registry
6. **Monitoring**: Template success rate tracked; poor templates removed

This creates a feedback loop where the system becomes faster over time without sacrificing safety — the Policy Arbiter remains the final authority at every tier.
