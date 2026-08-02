# HyperiOS

An intent-first agent server. Submit a goal. It figures out the rest.

HyperiOS is an AI agent server: submit a goal over HTTP, the agent refines
it, works it using a bounded tool-use loop (shell, notify, schedule), and
reports back. It is fire-and-forget by default — goals are queued and
executed in the background; poll the API to see progress and results.

## Status

MVP. The core module set (goal fulfilment, processor/agent loop, memory,
self-improvement, I/O toolbox) is wired and running behind an HTTP API. There
is currently no policy/allowlist/capability-gating layer in this path — the
agent can call the shell tool directly. See `AGENTS.md` for the module map
and known limitations.

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

Goals are submitted via the API, refined and tracked by `goal_fulfillment`,
queued and executed by `processor` (which spawns an LLM-driven agent that can
call tools in a loop), and results feed back into `self_improvement` for
periodic analysis.

## Building

```bash
go build -o hyperi ./cmd/hyperi
go test ./...
```

**Linux only.** This targets Linux; the agent's shell tool assumes a
Linux-like environment.

## Running

```bash
# Start the API server (also the default action with no subcommand)
./hyperi serve
./hyperi

# Configure the LLM provider/model/key
./hyperi config set llm_provider anthropic
./hyperi config set llm_api_key sk-ant-...
./hyperi config show
```

### API

```bash
# Submit a goal — queued and executed in the background immediately
curl -X POST localhost:8080/api/goals -d '{"description":"list files in /tmp"}'

# Submit a draft (not executed until explicitly run)
curl -X POST localhost:8080/api/goals -d '{"description":"...", "draft": true}'
curl -X POST localhost:8080/api/goals/<id>/run

# Poll status / result
curl localhost:8080/api/goals/<id>
curl localhost:8080/api/goals/<id>/result

# List goals, optionally filtered by state
curl localhost:8080/api/goals
curl 'localhost:8080/api/goals?state=active'

# Module health / metrics
curl localhost:8080/api/health
curl localhost:8080/api/reports

# List available tools
curl localhost:8080/api/tools
```

## Running locally without paid API calls

HyperiOS can run a local model via [Ollama](https://ollama.com/download) for
most goal execution, falling back to your configured remote provider
(Anthropic / OpenCode Zen) only when the local model fails or can't handle a
request. This is the "hybrid" mode — free by default, remote only as a
safety net.

```bash
# 1. Install and start Ollama (see https://ollama.com/download)

# 2. Detect your hardware and see what model would be picked (no changes made)
./hyperi models detect

# 3. Pull the recommended model and enable local mode (asks for confirmation)
./hyperi models setup

# Check current status
./hyperi models status

# Revert instantly — no re-download needed to turn it back on later
./hyperi models disable
./hyperi models enable

# Fully undo setup: delete the model from Ollama and clear config
./hyperi models remove
```

Model selection is a small curated list of Qwen2.5-Instruct sizes (3b/7b/14b/32b),
all verified to support Ollama's native tool-calling, picked automatically
based on detected GPU VRAM (multiple GPUs are summed — useful if you add a
second card later), falling back to system RAM if no GPU is found. Nothing
is downloaded and no config is changed until you explicitly confirm via
`hyperi models setup`.

### Expected local-model performance

Rough generation speed by model size on a single 16GB-class consumer GPU
(RTX 5080/4080-tier): 3b ~150+ tok/s, 7b ~90-120 tok/s, 14b ~50-70 tok/s,
32b needs 24GB+ VRAM to avoid CPU offload (much slower otherwise). Tool-use
*reliability* generally improves with model size — 14b is noticeably more
dependable at multi-step plans than 7b, which is why `hyperi models setup`
picks the largest model that comfortably fits your VRAM rather than the
fastest one.

For long-running, multi-step goals, two things matter more than raw
tokens/sec:

- **Context window (`num_ctx`)** — Ollama does not pick a safe default; on
  some versions it can default as low as 2-4k tokens and will *silently*
  drop the oldest messages (including the system prompt) once a multi-step
  tool-use conversation exceeds it, with no error. HyperiOS computes an
  explicit `num_ctx` from the model + detected VRAM during `hyperi models
  setup` (see `hyperi models status`) specifically to avoid this. Override
  with `hyperi config set local_model_num_ctx <tokens>` (or `auto` to
  recompute from current hardware at every server start — useful after
  adding a GPU).
- **Goal timeout / tool-call budget** — `goal_timeout_minutes` (default 30)
  and `max_tool_iterations` (default 30) bound how long a single goal is
  allowed to run and how many tool calls it may make. Raise these for goals
  you expect to genuinely take a long time:
  ```bash
  hyperi config set goal_timeout_minutes 120
  hyperi config set max_tool_iterations 100
  ```

## License

See LICENSE.
