# HyperiOS build tasks

# Build for current platform
dev:
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

# Build ISO (requires live-build on Linux)
iso:
    bash distro/build/build-iso.sh

# Clean build artifacts
clean:
    rm -f hyperi dist/hyperi-*
