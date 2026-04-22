# HyperiOS — High-Level Plan

## Concept

HyperiOS is a Linux distribution where **the agent is the primary interface**. The OS exists to serve intent. Applications are infrastructure — installed, configured, surfaced, and hidden by the controlling agent as needed. The user talks to HyperiOS; HyperiOS manages everything beneath.

This is not an AI assistant running on top of an OS. The OS is built around the agent.

---

## Architecture

```
User (text terminal / voice)
        |
[Hyperi Shell]  — persistent TUI, optional voice activation
        |
[Agent Pipeline]
  Intent Agent -> Planner Agent -> Adversarial Agent -> Policy Arbiter
        |
[Capability-Gated Executor]
  |- execute:shell       — read-only shell commands
  |- execute:package     — apt / flatpak / snap (NEW)
  |- execute:process     — systemctl start/stop/enable (NEW)
  |- execute:display     — swaymsg window control (NEW)
  |- execute:config      — config file templating (NEW)
  |- execute:network     — nmcli / firewall (NEW)
  |- read:file           — filesystem reads
  `- execute:git         — git operations
        |
[Ubuntu 24.04 LTS Server — minimal base]
  + sway compositor (kiosk mode, scriptable via swaymsg)
  + Hyperi systemd service (boot -> agent ready)
  + audit trail (/var/log/hyperi/audit.jsonl)
```

---

## Design Principles

**Safety is architectural, not behavioral.**
Constraints live below the agent layer. No LLM is trusted to self-govern. A deterministic, non-LLM Policy Arbiter has the final say over what executes.

**Modularity first.**
Every layer is swappable. The compositor (sway) can be replaced with a more capable one. The STT/TTS backend can be swapped. The executor backends are pluggable. The agent pipeline is interface-driven.

**The OS serves intent.**
Applications are not installed for users to manage. They are infrastructure that the agent installs, configures, and surfaces as needed to fulfill a command, then returns to the background.

---

## Build Phases

### Phase 0: Base Distro (Foundation)
- Ubuntu 24.04 LTS Server minimal as base
- `cloud-init` for automated first-boot provisioning
- `preseed` for bare metal unattended install
- Custom GRUB, minimal boot, no desktop environment
- Hyperi Go binary as a `systemd` service, starts on boot
- Serial/TTY console fallback for headless debugging

### Phase 1: Agent Core (Ported from Uplink)
- Port the agent pipeline as-is: Intent -> Planner -> Adversarial -> Arbiter
- Replace Windows executor with Linux-native executor
- New capability types: execute:package, execute:process, execute:display, execute:config, execute:network
- Audit trail writes to `/var/log/hyperi/`
- Sessions stored at `/var/lib/hyperi/sessions/`

### Phase 2: Terminal Shell Interface
- Hyperi Shell: persistent TUI (charmbracelet/bubbletea)
- Replaces cobra CLI — always running, not invoked per-command
- Text input -> agent pipeline -> streamed output
- Inline plan display with approval prompts for "modified" verdicts
- Session context persists across commands

### Phase 3: Voice Interface
- Opt-in, not always-on (user activates or configures)
- STT: local-first via whisper.cpp, fallback to API
- TTS: local via piper, fallback via espeak-ng
- Voice is just another input path into the same agent pipeline

### Phase 4: Display Management
- sway compositor (Wayland, i3-compatible, scriptable via swaymsg IPC)
- Executor gains execute:display capability
- Agent can: launch app into named workspace, show/hide windows, take screenshots
- Hyperi Shell runs fullscreen in workspace 1; apps summoned into workspace 2+
- Screen capture via grim (Wayland screenshot tool)

### Phase 5: ISO Builder
- live-build pipeline
- Produces bootable `.iso` installable on bare metal laptops
- Includes: base OS + sway + Hyperi binary + default config
- Post-install setup runs via cloud-init on first boot

---

## Agent Taxonomy

| Agent | File | Responsibility |
|---|---|---|
| Intent Agent (IA) | `internal/agents/intent.go` | Converts natural language to goal graph (no execution) |
| Planner Agent (PA) | `internal/agents/planner.go` | Proposes action sequences (no execution) |
| Adversarial Agent (AA) | `internal/agents/adversarial.go` | Actively tries to break plans before execution |
| Policy Arbiter | `internal/arbiter/arbiter.go` | Deterministic, rule-based final authority (non-LLM) |
| Executor | `internal/executor/` | Performs only approved steps |

### Plan Validation Flow
```
User Intent -> Intent Agent -> Planner Agent -> Adversarial Agent -> Policy Arbiter -> Executor -> Audit
```

---

## Capability System

Not role-based — scoped capabilities with TTL, revocability, and audit trail.

**Two-layer defense:**
1. **Allowlist layer** (`config/allowlist.yaml`) — binary pre-approval; listed commands can ever run
2. **Arbiter layer** — context-aware judgment on each specific plan

**Capability types for HyperiOS:**
- `read:file:<path>` — filesystem reads
- `execute:shell:<cmd>` — read-only shell commands (grep, find, ls, etc.)
- `execute:git:<op>` — git operations (status, log, diff, branch)
- `execute:package:<manager>:<pkg>` — apt/flatpak/snap install/remove
- `execute:process:systemctl:<action>:<service>` — service management
- `execute:display:sway:<cmd>` — compositor window control
- `execute:config:<path>` — write config files
- `execute:network:nmcli:<action>` — network configuration
- `network:outbound:<host>` — outbound HTTP calls
- `ui:open:<target>` — open browser or terminal

---

## Graduated Autonomy Levels

| Level | Name | Behavior |
|---|---|---|
| 0 | Observe only | Suggest, never execute (default) |
| 1 | Execute approved | Execute arbiter-approved steps |
| 2 | Execute reversible | Pre-approved reversible actions without prompt |
| 3 | Execute bounded irreversible | Requires adversarial + user approval |
| 4 | Trusted autonomy | Earned, domain-specific, scoped |

---

## Display Architecture

**Compositor: sway (Phase 4 default)**
- Chosen for: Wayland-native, i3-compatible IPC, scriptable via `swaymsg`
- Kiosk configuration: Hyperi Shell fullscreen in workspace 1
- Agent summons apps into workspace 2+
- Modular: swap for wlroots custom compositor in later phases

**Screen capture: grim**
- Wayland-native screenshot tool
- Called via subprocess; output piped through base64 for web UI

**Input injection: ydotool**
- Wayland-compatible mouse/keyboard injection
- Requires `ydotoold` daemon running as a service

---

## Repo Structure

```
hyperios/
|- cmd/
|   `- hyperi/main.go           # Entry point (cobra CLI -> agent pipeline)
|- internal/
|   |- agents/                   # Intent, Planner, Adversarial agents
|   |- arbiter/                  # Deterministic Policy Arbiter
|   |- audit/                    # JSONL audit logger
|   |- capability/               # Registry, Enforcer, Matcher
|   |- executor/
|   |   |- interface.go          # Executor interface
|   |   |- executor.go           # Factory + Stub (dry-run)
|   |   |- local.go              # Linux-native executor
|   |   `- container.go          # Docker container executor
|   |- llm/                      # Anthropic SDK wrapper + Completer interface
|   |- session/                  # Session state + persistence
|   |- shell/                    # TUI shell (Phase 2)
|   |- types/                    # Shared data structures
|   |- ui/
|   |   |- server.go             # HTTP + WebSocket server
|   |   |- capture.go            # grim screen capture
|   |   |- controller.go         # ydotool input injection
|   |   `- window.go             # swaymsg window management
|   `- voice/                    # STT/TTS pipeline (Phase 3)
|- distro/
|   |- cloud-init/               # First-boot provisioning
|   |- preseed/                  # Bare metal unattended install
|   |- systemd/                  # hyperi.service unit
|   |- sway/                     # Compositor config (kiosk mode)
|   `- build/                    # ISO build scripts
|- config/
|   `- allowlist.yaml            # Capability allowlist (extended for Linux)
|- ui/frontend/                  # React SPA
|- docs/
|   `- plan.md                   # This document
|- go.mod
|- justfile
`- AGENTS.md
```

---

## What Carries Over from Uplink

| Package | Status |
|---|---|
| `internal/agents/` | Carried over; system prompts updated for HyperiOS context |
| `internal/arbiter/` | Carried over as-is |
| `internal/audit/` | Carried over; paths updated to /var/log/hyperi/ |
| `internal/capability/` | Carried over; extended with 5 new Linux capability types |
| `internal/llm/` | Carried over as-is |
| `internal/session/` | Carried over; paths updated to /var/lib/hyperi/ |
| `internal/types/` | Carried over as-is |
| `internal/executor/container.go` | Carried over |
| `internal/executor/local.go` | Rewritten for Linux |
| `internal/ui/server.go` | Carried over; Win32 deps removed |
| `internal/ui/capture.go` | Rewritten: grim |
| `internal/ui/controller.go` | Rewritten: ydotool |
| `internal/ui/window.go` | Rewritten: swaymsg |
| `windows/` | Dropped entirely |

---

## Known Bugs (Inherited from Uplink, to be fixed)

1. `registry.Check()` glob patterns: partially fixed in uplink but needs verification on Linux paths
2. `ErrCapabilityNotGranted` sentinel: use `errors.As` instead of `errors.Is` at call sites
3. `session.Progress()` overcounts if `MarkCompleted()` called twice with same ID
4. `executeNetwork()` is a stub — returns success without making HTTP calls
5. `executeConfig()` is a stub — returns placeholder without writing files (Phase 3)

---

## Stack

- **Language:** Go 1.25.6
- **CLI:** spf13/cobra
- **LLM:** Anthropic SDK (claude-sonnet-4-6)
- **Compositor:** sway (Wayland)
- **Screen capture:** grim
- **Input injection:** ydotool
- **Package management:** apt, flatpak, snap
- **Service management:** systemd
- **Network:** nmcli
- **Voice STT (Phase 3):** whisper.cpp
- **Voice TTS (Phase 3):** piper
- **TUI Shell (Phase 2):** charmbracelet/bubbletea

## Running

```bash
go build -o hyperi ./cmd/hyperi    # Build
go test ./...                       # Run unit tests
go vet ./...                        # Lint
just build                          # Cross-compile for Linux amd64/arm64
```

**Target platforms:** Linux amd64 (primary), Linux arm64 (Raspberry Pi / ARM laptops)
**Not supported:** Windows, macOS (this is a distro, not a cross-platform app)
