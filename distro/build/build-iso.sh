#!/usr/bin/env bash
# HyperiOS ISO Builder
# Produces a bootable Ubuntu 24.04-based ISO with HyperiOS pre-installed.
# For distribution — not for development (use 'just dev' for that).
#
# Prerequisites (Ubuntu build host):
#   sudo apt-get install -y live-build debootstrap xorriso squashfs-tools curl git cmake build-essential
#
# Usage:
#   ./distro/build/build-iso.sh
#   ARCH=arm64 ./distro/build/build-iso.sh
#   OUTPUT=./dist/my.iso ./distro/build/build-iso.sh
#
# The built binary must exist at dist/hyperi-linux-<arch> before running.
# Run 'just build' first.

set -euo pipefail

# ── Configuration ─────────────────────────────────────────────────────────────
ARCH="${ARCH:-amd64}"
OUTPUT="${OUTPUT:-./dist/hyperios-${ARCH}.iso}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BUILD_DIR="${REPO_ROOT}/build/iso-work-${ARCH}"
BINARY="${REPO_ROOT}/dist/hyperi-linux-${ARCH}"

echo "╔══════════════════════════════════════╗"
echo "║       HyperiOS ISO Builder           ║"
echo "╚══════════════════════════════════════╝"
echo ""
echo "  Architecture : $ARCH"
echo "  Output       : $OUTPUT"
echo "  Build dir    : $BUILD_DIR"
echo "  Repository   : $REPO_ROOT"
echo ""

# ── Prerequisites check ───────────────────────────────────────────────────────
echo "[0/6] Checking prerequisites..."

missing=()
for tool in live-build debootstrap xorriso mksquashfs curl git cmake; do
    if ! command -v "$tool" &>/dev/null; then
        missing+=("$tool")
    fi
done

if [ ${#missing[@]} -gt 0 ]; then
    echo "ERROR: Missing tools: ${missing[*]}"
    echo "Install with: sudo apt-get install -y live-build debootstrap xorriso squashfs-tools curl git cmake build-essential"
    exit 1
fi

if [ ! -f "$BINARY" ]; then
    echo "ERROR: Binary not found at $BINARY"
    echo "Run 'just build' first to cross-compile the hyperi binary."
    exit 1
fi

echo "  All prerequisites met."

# ── Setup build directory ─────────────────────────────────────────────────────
echo ""
echo "[1/6] Setting up build directory..."

rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"
mkdir -p "$(dirname "$OUTPUT")"
cd "$BUILD_DIR"

# ── Configure live-build ──────────────────────────────────────────────────────
echo ""
echo "[2/6] Configuring live-build..."

lb config \
    --architectures "$ARCH" \
    --distribution noble \
    --debian-installer live \
    --debian-installer-gui false \
    --bootloader grub \
    --memtest none \
    --apt-recommends false \
    --apt-options "--yes --no-install-recommends" \
    --mirror-bootstrap "http://archive.ubuntu.com/ubuntu/" \
    --mirror-chroot "http://archive.ubuntu.com/ubuntu/" \
    --mirror-binary "http://archive.ubuntu.com/ubuntu/" \
    --security true

# ── Package lists ─────────────────────────────────────────────────────────────
echo ""
echo "[3/6] Staging package lists..."

mkdir -p config/package-lists

cat > config/package-lists/hyperios-base.list.chroot << 'EOF'
# Base system
openssh-server
curl
wget
git
jq
vim
htop
tmux
unzip

# Network
network-manager

# Init and services
systemd
systemd-resolved
dbus

# Wayland / display
sway
foot
grim
ydotool
xdg-utils
wayland-utils

# Accessibility
at-spi2-core
dbus-x11

# Audio (voice input)
alsa-utils

# Filesystem / monitoring
inotify-tools
flatpak

# Build tools (for whisper.cpp compilation in hook)
build-essential
cmake
git

# Cloud-init for first-boot provisioning
cloud-init

# Utilities
gdbus-tools
EOF

# ── Filesystem overlay ────────────────────────────────────────────────────────
echo ""
echo "[4/6] Staging filesystem overlay..."

# Binary
mkdir -p config/includes.chroot/usr/local/bin
cp "$BINARY" config/includes.chroot/usr/local/bin/hyperi
chmod +x config/includes.chroot/usr/local/bin/hyperi

# sway config
mkdir -p config/includes.chroot/etc/sway
cp "${REPO_ROOT}/distro/sway/config" config/includes.chroot/etc/sway/config

# systemd service
mkdir -p config/includes.chroot/etc/systemd/system
cp "${REPO_ROOT}/distro/systemd/hyperi.service" config/includes.chroot/etc/systemd/system/

# sudoers
mkdir -p config/includes.chroot/etc/sudoers.d
cp "${REPO_ROOT}/distro/sudoers/hyperi" config/includes.chroot/etc/sudoers.d/hyperi

# cloud-init config
mkdir -p config/includes.chroot/etc/cloud/cloud.cfg.d
cp "${REPO_ROOT}/distro/cloud-init/user-data.yaml" \
    config/includes.chroot/etc/cloud/cloud.cfg.d/99-hyperios.cfg

# sysctl
mkdir -p config/includes.chroot/etc/sysctl.d
echo "fs.inotify.max_user_watches=524288" \
    > config/includes.chroot/etc/sysctl.d/90-hyperi.conf

# Placeholder directories (created with correct permissions in hook)
mkdir -p config/includes.chroot/var/lib/hyperi
mkdir -p config/includes.chroot/var/log/hyperi
mkdir -p config/includes.chroot/etc/hyperi

# ── Hooks ─────────────────────────────────────────────────────────────────────
echo ""
echo "[5/6] Staging live-build hooks..."

mkdir -p config/hooks/normal
cp "${REPO_ROOT}/distro/build/hooks/"*.hook.chroot config/hooks/normal/
chmod +x config/hooks/normal/*.hook.chroot

# ── Build ─────────────────────────────────────────────────────────────────────
echo ""
echo "[6/6] Building ISO (this will take 10–30 minutes)..."
echo "      Log: ${BUILD_DIR}/build.log"
echo ""

lb build 2>&1 | tee build.log

# ── Output ────────────────────────────────────────────────────────────────────
ISO_FILE=$(ls live-image-"${ARCH}".hybrid.iso 2>/dev/null || ls *.iso 2>/dev/null | head -1)

if [ -z "$ISO_FILE" ]; then
    echo ""
    echo "ERROR: ISO file not found after build. Check build.log for errors."
    exit 1
fi

cp "$ISO_FILE" "$OUTPUT"

echo ""
echo "╔══════════════════════════════════════╗"
echo "║          Build Complete              ║"
echo "╚══════════════════════════════════════╝"
echo ""
echo "  ISO: $OUTPUT"
echo "  Size: $(du -sh "$OUTPUT" | cut -f1)"
echo ""
echo "  Flash to USB:"
echo "    sudo dd if=$OUTPUT of=/dev/sdX bs=4M status=progress && sync"
echo ""
echo "  Test in QEMU:"
echo "    just build-image  # builds a QEMU disk image from the same config"
echo ""
