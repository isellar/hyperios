Clone and set up the HyperiOS repository, build the binary, and provision the system.

Repo: https://github.com/isellar/hyperios

Follow `distro/dev/real-machine.md` for the full install steps. After setup:

1. Install Go 1.25.6 to /usr/local/go
2. Clone repo to /opt/hyperios
3. Run `sudo bash distro/dev/provision.sh` from repo root
4. Build: `go build -buildvcs=false -o dist/hyperi-linux-amd64 ./cmd/hyperi`
5. Install: `sudo cp dist/hyperi-linux-amd64 /usr/local/bin/hyperi && sudo chmod +x /usr/local/bin/hyperi`
6. Verify: `hyperi --help` runs without error
7. Verify: `sudo systemctl status hyperi` shows service is enabled

Note any errors or changes needed in `setup-notes.md` at the repo root.
Create branch `machine-setup`, commit `setup-notes.md` and any modified files, push to origin.

Do not start the hyperi service. Do not set any API keys.
