# HyperiOS build and dev tasks

# ── Go ─────────────────────────────────────────────────────────────────────────

# Build for current platform (dev use)
build-local:
    go build -o hyperi ./cmd/hyperi

# Cross-compile for Linux targets (primary deployment targets)
build:
    mkdir -p dist
    GOOS=linux GOARCH=amd64 go build -o dist/hyperi-linux-amd64 ./cmd/hyperi
    GOOS=linux GOARCH=arm64 go build -o dist/hyperi-linux-arm64 ./cmd/hyperi

# Run all unit tests
test:
    go test -race ./...

# Run integration tests (requires ANTHROPIC_API_KEY)
test-integration:
    go test -tags integration -race ./...

# Run docker executor tests (requires Docker)
test-docker:
    go test -tags docker ./internal/executor/...

# Lint
lint:
    go vet ./...

# Format
fmt:
    gofmt -w .

# Tidy dependencies
tidy:
    go mod tidy

# ── Dev VM (Vagrant) ───────────────────────────────────────────────────────────

# Spin up dev VM (Ubuntu 24.04, headless sway, cloud-init provisioned)
dev:
    vagrant up

# SSH into dev VM
dev-ssh:
    vagrant ssh

# Re-run provisioner without recreating VM (use after 'just build')
dev-provision:
    vagrant provision

# Tear down dev VM cleanly
dev-destroy:
    vagrant destroy -f

# Build binary and sync to VM in one step
dev-deploy: build
    vagrant provision

# ── Distribution builds ────────────────────────────────────────────────────────

# Build QEMU disk image for testing (faster than ISO; uses cloud-init base)
# Requires: qemu-utils cloud-image-utils
build-image: build
    bash distro/build/build-image.sh

# Build distributable ISO via live-build (requires Linux build host)
# Requires: live-build debootstrap xorriso squashfs-tools
# Run 'just build' first to produce the binary, then this.
build-iso: build
    bash distro/build/build-iso.sh

# Install live-build prerequisites on the build host
install-build-deps:
    sudo apt-get install -y live-build debootstrap xorriso squashfs-tools qemu-utils cloud-image-utils

# ── Utilities ──────────────────────────────────────────────────────────────────

# Clean build artifacts
clean:
    rm -f hyperi
    rm -rf dist/
