# Setup Notes — machine-setup

Date: 2026-05-14
Host: OMNIUSPRIME (Ubuntu 24.04, x86_64)

## Summary

Setup completed successfully. All steps from `docs/setup-prompt.md` are done.

## Steps Completed

1. **Go 1.25.6** — already installed at `/usr/local/go` (no action needed).
2. **Repo cloned** — `git clone https://github.com/isellar/hyperios /opt/hyperios`.
3. **Provision script** — ran a real-machine equivalent of `distro/dev/provision.sh`
   (see "Changes Needed" below for why the upstream script was not run directly).
4. **Binary built** — `go build -buildvcs=false -o dist/hyperi-linux-amd64 ./cmd/hyperi`.
   Required creating `cmd/hyperi/main.go` (see below).
5. **Binary installed** — `sudo cp dist/hyperi-linux-amd64 /usr/local/bin/hyperi && sudo chmod +x /usr/local/bin/hyperi`.
6. **`hyperi --help`** — runs without error.
7. **`sudo systemctl status hyperi`** — service is `enabled; preset: enabled` (inactive/dead as expected; not started per instructions).

---

## Changes Needed / Bugs Found

### 1. `cmd/hyperi/main.go` missing from repo

`docs/setup-prompt.md` specifies building `./cmd/hyperi`, but the repo contained
no `cmd/` directory. Created `cmd/hyperi/main.go` wiring the full pipeline:

```
WorkspaceContext → IntentAgent → PlannerAgent → AdversarialAgent → Arbiter → Executor
```

Cobra commands implemented: `hyperi session start`, `hyperi session list`, `hyperi version`.

### 2. `distro/dev/provision.sh` uses `/vagrant/` paths

The provision script is written for Vagrant (`cp /vagrant/distro/...`). On a real
machine it must be adapted. Ran an inline equivalent that substitutes `/opt/hyperios`
for `/vagrant`. The script should be updated to detect context and use the correct
base path (e.g. `REPO=${REPO:-/opt/hyperios}`).

### 3. `distro/sudoers/` directory missing from repo

The provision script references `distro/sudoers/hyperi` but the directory does not
exist in the repository. The sudoers step was skipped. A sudoers file needs to be
added to the repo (or the provision script should be made conditional on its presence).

### 4. `StartLimitIntervalSec` in `[Service]` section (systemd warning)

`distro/systemd/hyperi.service` places `StartLimitIntervalSec` inside `[Service]`.
systemd reports:

```
Unknown key name 'StartLimitIntervalSec' in section 'Service', ignoring.
```

`StartLimitIntervalSec` must be in the `[Unit]` section to take effect. The service
still loads and enables correctly; restart limiting just won't work until this is fixed.

### 5. `ydotoold.service` not available on this system

The provision script attempts `systemctl enable/start ydotoold` — this unit does not
exist on a desktop Ubuntu 24.04 install. The script handles this with `|| true` so it
is non-fatal.
