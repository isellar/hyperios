# Setup Notes — machine-setup

Date: 2026-05-14
Host: OMNIUSPRIME (Ubuntu 24.04, x86_64)

## Summary

Initial setup complete. All provisioning bugs fixed. Full pipeline wired and
tested through Phase 1C/Phase 2 of the implementation plan.

## Steps Completed

1. **Go 1.25.6** — already installed at `/usr/local/go` (no action needed).
2. **Repo cloned** — `git clone https://github.com/isellar/hyperios /opt/hyperios`.
3. **Provision script** — fixed and re-ran `distro/dev/provision.sh` successfully.
4. **Binary built** — `go build -buildvcs=false -o dist/hyperi-linux-amd64 ./cmd/hyperi`.
5. **Binary installed** — `/usr/local/bin/hyperi`.
6. **`hyperi --help`** — runs without error.
7. **`sudo systemctl status hyperi`** — service is `enabled; preset: enabled`.
8. **`systemd-analyze verify`** — zero warnings after service file fix.
9. **All unit tests pass** — `go test -race ./...`.

---

## Bugs Found and Fixed

### 1. `cmd/hyperi/main.go` missing — FIXED

Created `cmd/hyperi/main.go` as the full CLI entry point. Wires:
- TUI shell (Phase 2) as the default interface (`hyperi` with no args)
- `hyperi session start [intent]` — launches TUI, or headless with `--no-tui`
- `hyperi session list` — lists sessions
- `hyperi session resume <id>` — resumes halted/in-progress sessions
- `hyperi plans [--status ...]` — lists plan documents by status
- `hyperi config show/get/set` — runtime config management
- Full infrastructure bootstrap: event bus, capability registry, manifest store,
  inotify watcher, in-process scheduler (manifest rescan, session cleanup,
  audit log rotation), audit logger

### 2. `distro/dev/provision.sh` used `/vagrant/` paths — FIXED

Replaced hardcoded `/vagrant/` with a `$REPO` variable that auto-detects:
Vagrant synced folder → script location → explicit env var.

### 3. `distro/sudoers/` directory missing — FIXED

Created `distro/sudoers/hyperi` granting the `hyperi` service user NOPASSWD
sudo access to `apt-get`, `snap`, and `systemctl` operations.

### 4. `StartLimitIntervalSec` in `[Service]` section — FIXED

Moved to `[Unit]` where systemd expects it. Verified with `systemd-analyze verify`.

### 5. `ydotoold.service` non-fatal — FIXED

Made conditional in provision.sh (`systemctl cat ydotoold` check before enable/start).

---

## Additional Wiring Completed (Phase 1B/1C/2)

These items existed in the codebase but were not connected:

- **Scheduler `DefaultJobs()`**: now wired to real manifest store and session
  manager instances in `bootstrap()`. Three built-in jobs registered on startup:
  manifest rescan (6h), session cleanup (daily 3am), audit log rotation (weekly).

- **inotify watcher**: `manifest.NewWatcher` now called and started in `launchShell()`.
  Watches paths from `config.WatchPaths`; publishes `EventManifestUpdated` on changes.

- **TUI as primary interface**: `hyperi` (no args) launches the bubbletea shell.
  `HYPERI_INITIAL_INTENT` env var injects a pre-queued intent on startup (used by
  `hyperi session start "..."` to pre-fill the TUI input).

- **Container executor**: updated to use literal `Command[]` field (Phase 1A path),
  publish bus events on step start/complete/fail/skip, and apply `on_failure` policy.

- **Web UI pipeline**: `ui.Server.SetPipeline()` now wires `handleUserInput` to
  the real agent pipeline. Non-display intents route through the pipeline; display
  commands ("show X", "switch back") are still handled directly.

---

## Dev Testing

The `/var/lib/hyperi` directory is owned by the `hyperi` service user. When testing
as a regular user, set `HYPERI_DATA_DIR` to an accessible path:

```bash
export HYPERI_DATA_DIR=/tmp/hyperi-dev
export ANTHROPIC_API_KEY=sk-ant-...
hyperi                                    # launch TUI shell
hyperi session start "show disk usage"   # launch TUI with intent pre-filled
hyperi session start --no-tui "show disk usage"  # headless
hyperi config show
hyperi plans
```
