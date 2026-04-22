# HyperiOS — Agent Development Guide

## What Is HyperiOS

HyperiOS is a custom Linux distribution (based on Ubuntu 24.04 LTS) where the AI agent **is** the primary interface. The OS exists to serve user intent. Applications are infrastructure — installed, configured, surfaced, and hidden by the controlling agent as needed.

This is not an AI assistant running on top of an OS. The OS is built around the agent.

## Architecture

See `docs/plan.md` for the full high-level plan.

```
User Intent -> Intent Agent -> Planner Agent -> Adversarial Agent -> Policy Arbiter -> Executor -> Audit
```

**Design Principle:** Safety is architectural, not behavioral. Constraints live below the agent layer.

## Agent Taxonomy

| Agent | File | Responsibility |
|---|---|---|
| Intent Agent (IA) | `internal/agents/intent.go` | Converts NL to goal graph (no execution) |
| Planner Agent (PA) | `internal/agents/planner.go` | Proposes action sequences (no execution) |
| Adversarial Agent (AA) | `internal/agents/adversarial.go` | Actively finds risks in plans |
| Policy Arbiter | `internal/arbiter/arbiter.go` | Deterministic, non-LLM final authority |
| Executor | `internal/executor/` | Performs only arbiter-approved steps |

## Capability Types

| Type | Scope format | Example |
|---|---|---|
| `read:file` | `<path>` | `{workspace}/**` |
| `execute:shell` | `<command>` | `grep` |
| `execute:git` | `git:<op>` | `git:status` |
| `execute:package` | `<mgr>:<pkg>` | `apt:curl` |
| `execute:process` | `systemctl:<action>:<svc>` | `systemctl:start:nginx` |
| `execute:display` | `sway:<cmd>` | `sway:workspace 2` |
| `execute:config` | `<path>` | `/etc/nginx/nginx.conf` |
| `execute:network` | `nmcli:<action>` | `nmcli:device status` |
| `network:outbound` | `<host>` | `api.anthropic.com` |
| `ui:open` | `browser` or `terminal` | `browser` |

## Key Files

| File | Purpose |
|---|---|
| `cmd/hyperi/main.go` | CLI entry point, full pipeline orchestration |
| `internal/types/types.go` | All shared structs (no logic) |
| `internal/arbiter/arbiter.go` | Deterministic safety rules |
| `internal/capability/registry.go` | Capability storage and allowlist |
| `internal/executor/local.go` | Linux-native execution backend |
| `internal/session/state.go` | Session state |
| `internal/ui/server.go` | HTTP + WebSocket server |
| `config/allowlist.yaml` | Pre-approved capability allowlist |
| `distro/` | ISO build, systemd, sway, cloud-init |

## Implementation Status

| Phase | Item | Status |
|---|---|---|
| 0 | Base distro scaffolding | In progress |
| 1 | Agent pipeline (port from Uplink) | Done |
| 1 | Linux-native executor | Done (stub package/process/display/config) |
| 1 | Capability system (extended) | Done |
| 2 | TUI shell (bubbletea) | Stubbed |
| 3 | Voice interface (whisper/piper) | Stubbed |
| 4 | Display management (sway IPC) | Stubbed |
| 5 | ISO builder (live-build) | Stubbed |

## Testing

```bash
go test -race ./...                          # Unit tests
go test -tags integration -race ./...        # LLM integration tests (requires API key)
go test -tags docker ./internal/executor/... # Docker executor tests
```

## Build

```bash
go build -o hyperi ./cmd/hyperi    # Local build (Linux)
just build                          # Cross-compile: linux/amd64, linux/arm64
```

Target platforms: **Linux only** (amd64 primary, arm64 for ARM laptops/RPi).
This is a Linux distribution — Windows/macOS are not supported targets.

## Known Bugs

1. Glob matching in `registry.Check()` needs end-to-end testing on Linux paths
2. `ErrCapabilityNotGranted` sentinel: use `errors.As` not `errors.Is` at call sites
3. `session.Progress()` overcounts if `MarkCompleted()` called with same step ID twice
4. `executeNetwork()` is a stub — no real HTTP calls yet
5. `executeConfig()` is a stub — no file writing yet (Phase 3)
