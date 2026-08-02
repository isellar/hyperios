# HyperiOS — Agent Development Guide

## What Is HyperiOS

HyperiOS is an intent-first agent server. A user (or any UI on top) submits a
goal over HTTP; the agent refines it, works it using an LLM-driven tool-use
loop, and reports back. It is fire-and-forget by default: goals are queued
and executed in the background immediately unless submitted as a draft.

## Architecture

```
Any UI (web / CLI / mobile)
        |
   HTTP API (internal/apiserver)
        |
[GoalFulfillment] -> [Processor / Agent tool-use loop] -> [IOToolbox: shell, notify, schedule]
        |                      |
   [Memory]           [SelfImprovement]
```

**Current state (MVP):** there is no policy/allowlist/capability-gating layer
in this path. The spawned agent can call the shell tool directly with no
further gating beyond the LLM's own judgment. A prior iteration had a
Governor/PolicyArbiter/capability-allowlist layer (`internal/governor`,
`internal/capability`) sitting between the processor and execution; that
layer was removed to focus on getting the core goal-fulfilment loop working
end-to-end first. Re-introduce a policy layer before running this against an
untrusted or production environment — see "Known limitations" below.

## Module Map

| Module | File | Responsibility |
|---|---|---|
| GoalFulfillment | `internal/goal_fulfillment/` | Submit/refine/breakdown/track goals; persists to disk |
| Processor | `internal/processor/` | Prioritizes goals, spawns the LLM agent, runs the tool-use loop |
| Memory | `internal/memory/` | Session + world-model + long-term (disk-backed) context store |
| SelfImprovement | `internal/self_improvement/` | Analyzes goal results, submits improvement goals |
| IOToolbox | `internal/io_toolbox/` | Registered tools the agent can call: `shell`, `notify`, `schedule` |
| API Server | `internal/apiserver/` | HTTP routes; owns the background goal-processing loop |

All five wired modules implement `internal/module.Module` (`Name`, `Report`,
`Tune`, `Health`, `Capabilities`) for uniform health/metrics reporting via
`GET /api/health` and `GET /api/reports`.

## Agent Execution Model

`processor.AgentSpawner` (`internal/processor/agent.go`) drives a bounded
tool-use loop against whatever `llm.Completer`/`llm.ToolCompleter` is wired
in (see below): the LLM is given the goal plus the registered IOToolbox
tools, and may call them repeatedly (up to `maxToolIterations`) before
producing a final plain-text summary. If no toolbox is wired, or the LLM
client doesn't support tool-use, it falls back to a single-shot narrative
response (no real execution) — see `runNarrative` in the same file.

## LLM Providers: Remote + Local (Ollama)

`internal/llm` supports three provider shapes, all behind the same
`Completer`/`ToolCompleter` interfaces so `processor`/`goal_fulfillment`/
`self_improvement` never need to know which one they're talking to:

- `*llm.Client` — Anthropic API or OpenCode Zen (Anthropic-messages-
  compatible gateway), selected by `cfg.LLMProvider`. Paid, per-token.
- `*llm.OllamaClient` (`internal/llm/ollama.go`) — talks to a local Ollama
  daemon's `/api/chat`. Free, but tool-calling reliability is lower on
  smaller models.
- `*llm.HybridCompleter` (`internal/llm/hybrid.go`) — wraps a local and a
  remote Completer; tries local first, falls back to remote on any error
  (network, model-not-found, tool-use failure). This is what's actually
  wired when `cfg.LocalModelEnabled` is true — see `buildLLMClient` in
  `cmd/hyperi/llm_setup.go`.

`internal/localmodel` handles the "which model fits this hardware" problem:
`DetectHardware()` reads GPU VRAM (via `nvidia-smi`, summed across all
detected GPUs — Ollama can tensor-split a model across multiple cards) and
system RAM (via `/proc/meminfo`), `PickModel()` picks the largest model from
a small curated `Catalog` (Qwen2.5-Instruct 3b/7b/14b/32b — all verified to
support Ollama's native tool-calling) that fits, and `Manager` wraps Ollama's
`/api/tags`, `/api/pull` (with streaming progress), and `/api/delete`
endpoints.

**Nothing about local-model use is automatic.** `cfg.LocalModelEnabled`
starts `false` and is only ever flipped by an explicit, confirmed
`hyperi models setup` run (see `cmd/hyperi/llm_setup.go`). `hyperi models
disable`/`enable` toggle it instantly with no re-download; `hyperi models
remove` also deletes the pulled model from Ollama.

### Context window (num_ctx) — do not trust Ollama's default

Ollama does **not** pick a safe context-window default: depending on
version/platform it can be as low as 2048-4096 tokens, and when a
conversation exceeds it, Ollama **silently drops the oldest messages**
(including the system prompt and early tool-call results) with no error
surfaced anywhere. For a multi-round tool-use agent loop this is exactly the
failure mode that would quietly corrupt longer-running goals partway
through. `localmodel.RecommendNumCtx(spec, availableVRAMMB)` computes an
explicit `num_ctx` from the model's approximate KV-cache-per-token cost
(`ModelSpec.KVCachePerKTokenMB`) and available VRAM; `hyperi models setup`
runs this and stores the result in `cfg.LocalModelNumCtx` (0 = "auto",
recomputed from currently-detected hardware at every `buildLLMClient` call —
relevant after adding/removing a GPU without re-running setup).
`OllamaClient` sends this as `options.num_ctx` on every request (see
`applyOptions` in `internal/llm/ollama.go`) — never rely on the daemon's own
default.

### Long-running goals

Two knobs bound how long a goal is allowed to run, both generous by default
specifically to accommodate slower local-model inference:
`cfg.GoalTimeoutMinutes` (default 30, wired via `Processor.SetGoalTimeout` →
a `context.WithTimeout` around the whole agent run in `RunNext`) and
`cfg.MaxToolIterations` (default 30, wired via
`Processor.SetMaxToolIterations` → `AgentSpawner.maxToolIterations`, bounding
tool-call round-trips within one run). Also note `OllamaClient` uses a
20-minute HTTP client timeout (`DefaultOllamaTimeout`) and a 30-minute
`keep_alive` (`DefaultKeepAlive`) to avoid model-reload latency between
back-to-back tool calls in the same goal.

## Key Files

| File | Purpose |
|---|---|
| `cmd/hyperi/main.go` | CLI entry point; `serve` is the default action |
| `cmd/hyperi/wiring.go` | Constructs and cross-wires all modules |
| `cmd/hyperi/llm_setup.go` | `buildLLMClient` + `hyperi models` subcommands |
| `internal/types/types.go` | Shared structs (Goal, Directive, etc.) |
| `internal/apiserver/server.go` | HTTP routes + background RunLoop |
| `internal/processor/agent.go` | Agent tool-use loop |
| `internal/llm/tools.go` | Anthropic tool-use (function calling) wrapper |
| `internal/llm/ollama.go` | Ollama `/api/chat` client (Completer + ToolCompleter) |
| `internal/llm/hybrid.go` | Local-first/remote-fallback Completer |
| `internal/localmodel/` | Hardware detection, model catalog/picker, Ollama pull/delete |
| `internal/io_toolbox/tools/` | Built-in tool implementations |
| `internal/config/config.go` | Runtime config (JSON, `~/.hyperi/config.json`) |

## API Routes

| Method | Path | Description |
|---|---|---|
| POST | `/api/goals` | Submit a goal; queued immediately unless `"draft": true` |
| GET | `/api/goals` | List goals, optional `?state=` filter |
| GET | `/api/goals/{id}` | Get a goal (+ result, if available) |
| POST | `/api/goals/{id}/run` | Queue a draft goal for execution |
| GET | `/api/goals/{id}/result` | Get the agent's result for a goal |
| GET | `/api/health` | Per-module health |
| GET | `/api/reports` | Per-module metrics report |
| GET | `/api/tools` | List registered IOToolbox tools |

## Testing

```bash
go test -race ./...                          # Unit + integration tests
```

## Build

```bash
go build -o hyperi ./cmd/hyperi    # Local build (Linux)
just build                          # Cross-compile: linux/amd64, linux/arm64
```

Target platforms: **Linux only** (amd64 primary, arm64 for ARM laptops/RPi).

## CI / Auto-push Policy

Every push and PR runs `.github/workflows/ci.yml` (`go vet`, `go build`,
`go test -race ./...`) as the required gate — do not merge/push changes that
fail it.

Locally, run `just hooks-install` once per clone to enable a `post-commit`
git hook (`scripts/hooks/post-commit`) that runs `go test -race ./...` after
every commit and pushes to the branch's upstream automatically **only if
tests pass**. If tests fail, nothing is pushed — fix and commit again.
Set `HYPERIOS_SKIP_AUTOPUSH=1` to skip auto-push for a single commit (e.g.
WIP commits you don't want published yet).

Net effect: any commit that passes tests locally is expected to end up on
origin without a separate manual push step.

## Known limitations (MVP)

1. No policy/allowlist/capability-gating layer — the agent's shell tool runs
   with no further restriction. Do not point this at an untrusted goal source
   or production system yet.
2. No directive-compliance checking (the previous Governor's directive check
   was itself a stub — `goalViolatesDirective` always returned `false`).
3. `internal/scheduler`, `internal/events`, and `internal/audit` remain wired
   but are lightly used in the MVP path — the `schedule` tool uses the
   scheduler; the event bus and full audit trail were built for the removed
   TUI pipeline and are not fully exercised here.
4. `docs/*.md` (vision, plan, critiques, tasks, issues) describe the earlier
   TUI-centric, five-phase architecture and are stale relative to this MVP.
   `distro/` provisioning scripts (sway, cloud-init, systemd unit comments)
   also reference the removed TUI/session CLI in places — not yet updated.
