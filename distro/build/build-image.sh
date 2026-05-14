#!/usr/bin/env bash
# HyperiOS QEMU disk image builder
# Produces a bootable qcow2 image for testing without burning an ISO.
# Faster than live-build; uses cloud-init + debootstrap directly.
#
# Prerequisites:
#   sudo apt-get install -y qemu-utils cloud-image-utils debootstrap
#
# Usage:
#   ./distro/build/build-image.sh
#   ARCH=amd64 OUTPUT=./dist/hyperios.qcow2 ./distro/build/build-image.sh
#
# The image can be run with:
#   qemu-system-x86_64 \
#     -machine type=q35,accel=kvm \
#     -cpu host -smp 2 -m 2G \
#     -drive file=./dist/hyperios-amd64.qcow2,format=qcow2 \
#     -nographic -serial mon:stdio
#
# Note: This uses the Ubuntu 24.04 cloud image as a base and applies
# the HyperiOS provisioner on first boot via cloud-init.
# It does NOT produce a distributable install ISO — use build-iso.sh for that.

set -euo pipefail

ARCH="${ARCH:-amd64}"
OUTPUT="${OUTPUT:-./dist/hyperios-${ARCH}.qcow2}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BINARY="${REPO_ROOT}/dist/hyperi-linux-${ARCH}"
WORK_DIR="${REPO_ROOT}/build/image-work-${ARCH}"

# Ubuntu 24.04 cloud image base
BASE_URL="https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-${ARCH}.img"
BASE_IMG="${WORK_DIR}/noble-base.img"

echo "╔══════════════════════════════════════╗"
echo "║     HyperiOS QEMU Image Builder      ║"
echo "╚══════════════════════════════════════╝"
echo ""
echo "  Architecture : $ARCH"
echo "  Output       : $OUTPUT"
echo ""

# ── Prerequisites ─────────────────────────────────────────────────────────────
missing=()
for tool in qemu-img cloud-localds wget; do
    if ! command -v "$tool" &>/dev/null; then
        missing+=("$tool")
    fi
done
if [ ${#missing[@]} -gt 0 ]; then
    echo "ERROR: Missing tools: ${missing[*]}"
    echo "Install: sudo apt-get install -y qemu-utils cloud-image-utils"
    exit 1
fi

if [ ! -f "$BINARY" ]; then
    echo "ERROR: Binary not found at $BINARY"
    echo "Run 'just build' first."
    exit 1
fi

mkdir -p "$WORK_DIR" "$(dirname "$OUTPUT")"

# ── Download base image ───────────────────────────────────────────────────────
echo "[1/4] Fetching Ubuntu 24.04 cloud base image..."
if [ ! -f "$BASE_IMG" ]; then
    wget -q --show-progress -O "$BASE_IMG" "$BASE_URL"
else
    echo "  Using cached base image."
fi

# ── Resize and copy ───────────────────────────────────────────────────────────
echo "[2/4] Creating disk image (20GB)..."
cp "$BASE_IMG" "${WORK_DIR}/hyperios.img"
qemu-img resize "${WORK_DIR}/hyperios.img" 20G

# ── Build cloud-init seed ─────────────────────────────────────────────────────
echo "[3/4] Building cloud-init seed..."

SEED_DIR="${WORK_DIR}/seed"
mkdir -p "$SEED_DIR"

# user-data: run our provisioner + install the binary
cat > "${SEED_DIR}/user-data" << USERDATA
#cloud-config
$(cat "${REPO_ROOT}/distro/cloud-init/user-data.yaml" | tail -n +2)

# Inject the hyperi binary from the seed drive
runcmd:
  - cp /var/lib/cloud/instance/scripts/hyperi /usr/local/bin/hyperi
  - chmod +x /usr/local/bin/hyperi
  - systemctl restart hyperi || true
USERDATA

# meta-data
cat > "${SEED_DIR}/meta-data" << 'METADATA'
instance-id: hyperios-dev-001
local-hostname: hyperios
METADATA

# Copy binary into seed (accessible via cloud-init)
mkdir -p "${SEED_DIR}/scripts"
cp "$BINARY" "${SEED_DIR}/scripts/hyperi"

# Build seed ISO
cloud-localds "${WORK_DIR}/seed.iso" \
    "${SEED_DIR}/user-data" \
    "${SEED_DIR}/meta-data"

# ── Convert to qcow2 ─────────────────────────────────────────────────────────
echo "[4/4] Converting to qcow2..."
qemu-img convert -O qcow2 "${WORK_DIR}/hyperios.img" "$OUTPUT"
cp "${WORK_DIR}/seed.iso" "$(dirname "$OUTPUT")/hyperios-seed-${ARCH}.iso"

echo ""
echo "╔══════════════════════════════════════╗"
echo "║          Build Complete              ║"
echo "╚══════════════════════════════════════╝"
echo ""
echo "  Image : $OUTPUT"
echo "  Seed  : $(dirname "$OUTPUT")/hyperios-seed-${ARCH}.iso"
echo "  Size  : $(du -sh "$OUTPUT" | cut -f1)"
echo ""
echo "  Run with:"
echo "    qemu-system-x86_64 \\"
echo "      -machine type=q35,accel=kvm \\"
echo "      -cpu host -smp 2 -m 2G \\"
echo "      -drive file=$OUTPUT,format=qcow2 \\"
echo "      -drive file=$(dirname "$OUTPUT")/hyperios-seed-${ARCH}.iso,format=raw \\"
echo "      -nographic -serial mon:stdio"
echo ""
