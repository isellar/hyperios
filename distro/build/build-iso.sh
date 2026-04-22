#!/usr/bin/env bash
# HyperiOS ISO builder
# Produces a bootable Ubuntu 24.04-based ISO with the HyperiOS agent pre-installed.
#
# Prerequisites (on Ubuntu build host):
#   sudo apt-get install -y live-build debootstrap xorriso squashfs-tools
#
# Usage:
#   ./distro/build/build-iso.sh [--arch amd64|arm64] [--output ./dist/hyperios.iso]
#
# TODO(Phase 5): Implement full live-build pipeline.

set -euo pipefail

ARCH="${ARCH:-amd64}"
OUTPUT="${OUTPUT:-./dist/hyperios-${ARCH}.iso}"
BUILD_DIR="./build/iso-work"

echo "HyperiOS ISO Builder"
echo "Architecture: $ARCH"
echo "Output: $OUTPUT"
echo ""

# Validate prerequisites
for tool in live-build debootstrap xorriso; do
    if ! command -v "$tool" &>/dev/null; then
        echo "ERROR: $tool is not installed. Run: sudo apt-get install live-build debootstrap xorriso squashfs-tools"
        exit 1
    fi
done

mkdir -p "$BUILD_DIR" "$(dirname "$OUTPUT")"

echo "[1/5] Configuring live-build..."
cd "$BUILD_DIR"
lb config \
    --architectures "$ARCH" \
    --distribution noble \
    --debian-installer live \
    --debian-installer-gui false \
    --bootloaders grub-pc \
    --memtest none \
    --apt-recommends false \
    --debootstrap-options "--include=ca-certificates" \
    --mirror-bootstrap "http://archive.ubuntu.com/ubuntu/" \
    --mirror-chroot "http://archive.ubuntu.com/ubuntu/" \
    --mirror-binary "http://archive.ubuntu.com/ubuntu/"

echo "[2/5] Staging package lists..."
mkdir -p config/package-lists
cat > config/package-lists/hyperios.list.chroot << 'EOF'
sway
foot
grim
ydotool
curl
git
jq
network-manager
flatpak
systemd-resolved
openssh-server
EOF

echo "[3/5] Staging HyperiOS overlay..."
mkdir -p config/includes.chroot/usr/local/bin
mkdir -p config/includes.chroot/etc/hyperi
mkdir -p config/includes.chroot/etc/sway
mkdir -p config/includes.chroot/var/lib/hyperi
mkdir -p config/includes.chroot/var/log/hyperi

# Copy sway config
cp "../../sway/config" config/includes.chroot/etc/sway/config

# Copy cloud-init user-data
mkdir -p config/includes.chroot/etc/cloud/cloud.cfg.d
cp "../../cloud-init/user-data.yaml" config/includes.chroot/etc/cloud/cloud.cfg.d/99-hyperios.cfg

# Copy systemd service
mkdir -p config/includes.chroot/etc/systemd/system
cp "../../systemd/hyperi.service" config/includes.chroot/etc/systemd/system/hyperi.service

echo "[4/5] Building ISO (this will take a while)..."
lb build 2>&1 | tee build.log

echo "[5/5] Copying ISO to output..."
cp live-image-"$ARCH".hybrid.iso "../../$OUTPUT"

echo ""
echo "Done. ISO written to: $OUTPUT"
echo "Flash with: sudo dd if=$OUTPUT of=/dev/sdX bs=4M status=progress && sync"
