# HyperiOS — Implementation Tasks

Phases 0–5 are complete and running on real hardware (OMNIUSPRIME, Ubuntu 24.04).
This document now tracks open bugs and next work items only.

For the original phase-by-phase breakdown and decisions made during planning,
see `docs/critiques.md` and `docs/plan.md`.

---

## Done (all five phases)

| Phase | Status | Notes |
|---|---|---|
| 0 — Base distro | ✓ | Vagrantfile, cloud-init, preseed/autoinstall, sway, systemd, sudoers, provision scripts |
| 1A — Pipeline correctness | ✓ | Command[], CommandValidator, on_failure, ReadyCondition, all inherited bugs fixed |
| 1B — Persistence + recovery | ✓ | Plan docs, re-plan loop, crash recovery, autonomy levels, approval timeouts |
| 1C — Observable + time-aware | ✓ | Event bus, scheduler, manifest + inotify, execute:schedule |
| 2 — TUI shell | ✓ | bubbletea shell, pipeline runner, inline plan display, approval prompts, foreground lock |
| 3 — Voice input | ✓ | Push-to-talk STT via whisper.cpp + arecord; toggle Ctrl+Space |
| 4 — Display management | ✓ | swaymsg IPC, AT-SPI queries, grim capture, layered interaction model |
| 5 — ISO builder | ✓ | live-build pipeline, QEMU image builder, three chroot hooks |

Real-hardware fixes from machine-setup branch (merged):
- `cmd/hyperi/main.go` rewritten: proper bootstrap(), `--no-tui` flag, `HYPERI_INITIAL_INTENT` env var
- `provision.sh` auto-detects repo path (no longer hardcoded to `/vagrant/`)
- `distro/sudoers/hyperi` created
- `systemd.service` `StartLimitIntervalSec` moved to `[Unit]`
- Container executor updated to use `Command[]`, publishes bus events, applies `on_failure`
- Web UI `handleUserInput` wired to real pipeline

Bug fixes (post-review):
- Issue 1: voice deadlock in `Start()` — fixed
- Issue 2: grant round-trip corruption for multi-segment types — fixed
- Issue 6: `executePackage()` ignored `Command[]` — fixed
- Issue 7: `execute:schedule` missing from `AllowlistConfig` — fixed
- Issue 29: allowlist relative path — fixed by desktop (findRepoAllowlist)
- Issues 4/5: container executor NL extraction + hardcoded grep pattern — fixed by desktop

---

## Open — Active work

### Fix Issue 10: `resumeSession()` restarts pipeline from scratch
**Priority:** High — real-world friction on every halted session  
**File:** `cmd/hyperi/main.go`  
All the pieces exist (`ParsePlanDoc`, `NextPendingStep`, `NextPendingStage` in `internal/plan/parser.go`)
but `resumeSession` re-runs the full Intent→Planner→Adversarial→Arbiter→Execute pipeline
instead of picking up from the last completed step.

### Fix Issue 11: manifest prefix match over-matches
**Priority:** Medium  
**File:** `internal/manifest/manifest.go:152`  
`strings.HasPrefix(path, k)` matches `/etcother/foo` against key `/etc`. Fix: `|| path == k`.

### Fix Issue 19: `SaveGrants()` write error silently ignored
**Priority:** Low  
**File:** `internal/capability/registry.go`  
`os.WriteFile` error is discarded. Log it.

---

## Open — Bugs (from issues.md)

| # | File | Description | Priority |
|---|------|-------------|----------|
| 3 | `internal/executor/local.go` | `vision:confirms` always returns true (stub) | Post-v1 |
| 8 | `internal/display/atspi.go` | `Click()` always returns error | Post-v1 |
| 9 | `internal/display/atspi.go` | AT-SPI walk is substring match on raw text | Post-v1 |
| 12 | `internal/shell/model.go` | Split channel ownership on approval reply | Low |
| 13 | `internal/plan/writer.go` | `strings.Title` deprecated | Low |
| 14 | `internal/plan/writer.go` | Real exit code not captured in plan doc | Medium |
| 15 | `internal/ui/server.go` | `handleIndex` dead code | Low |
| 17 | `internal/ui/capture.go` | Uses `sh -c` with pipe (inconsistent with no-shell policy) | Low |
| 18 | `cmd/hyperi/main.go` | `godotenv.Load()` error silently ignored | Low |
| 20 | `internal/executor/local.go` | Legacy NL extraction fallback dead code | Low |
| 21 | `internal/shell/shell.go` | `processRunning()` Linux-only, no build tag | Low |
| 28 | `internal/shell/model.go` | Voice push-to-talk hint not rendering in TUI | Medium |

---

## Open — Missing test coverage

| Package | What's missing |
|---|---|
| `internal/agents/` | All three LLM agents — mock response parsing, validation, error handling |
| `internal/executor/` | dispatch, retry, ReadyCondition polling, on_failure policy |
| `internal/config/` | Load, Save, Defaults, round-trip |
| `internal/shell/` | TUI model, pipeline runner |
| `internal/plan/writer.go` | Writer unit tests |

---

## Post-v1 backlog

See `docs/post-v1.md` for full details on each deferred item:

- Web UI (remote access interface)
- Multi-user sessions
- Automatic trust accumulation
- Rollback / undo (hybrid Position A+B)
- `ReadyCondition` as first-class step type
- Per-capability-domain autonomy levels
- `vision:confirms` full implementation (LLM vision API)
- AT-SPI `Click()` full implementation + real tree walk
- ISO build artifact signing and hosting
