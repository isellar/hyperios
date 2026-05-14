#!/usr/bin/env bash
# HyperiOS dev provisioner
# Idempotent — safe to run multiple times.
# Works in two modes:
#   Vagrant:      /vagrant is the synced repo root (set by Vagrant automatically)
#   Real machine: REPO env var or auto-detected from script location
#
# Mirrors what cloud-init/user-data.yaml does on a real install.

set -euo pipefail

# ── Detect repo root ───────────────────────────────────────────────────────────
# Priority: 1) explicit REPO env var, 2) /vagrant (Vagrant), 3) script location
if [ -z "${REPO:-}" ]; then
    if [ -d /vagrant ] && [ -f /vagrant/go.mod ]; then
        REPO=/vagrant
    else
        # Resolve from script location: distro/dev/provision.sh -> repo root
        SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
        REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"
    fi
fi
export REPO
echo "==> HyperiOS dev provisioner starting (repo: $REPO)"

# ── System update ──────────────────────────────────────────────────────────────
echo "==> Updating packages..."
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get upgrade -y -qq

# ── Install packages ───────────────────────────────────────────────────────────
echo "==> Installing packages..."
apt-get install -y -qq \
    sway \
    foot \
    grim \
    ydotool \
    curl \
    git \
    jq \
    network-manager \
    flatpak \
    systemd-resolved \
    inotify-tools \
    at-spi2-core \
    xdg-utils \
    openssh-server \
    vim

# ── inotify watch limit ────────────────────────────────────────────────────────
echo "==> Configuring inotify..."
if ! grep -q "max_user_watches" /etc/sysctl.d/90-hyperi.conf 2>/dev/null; then
    echo "fs.inotify.max_user_watches=524288" > /etc/sysctl.d/90-hyperi.conf
    sysctl --system -q
fi

# ── hyperi user and groups ─────────────────────────────────────────────────────
echo "==> Creating hyperi user..."
if ! id hyperi &>/dev/null; then
    useradd \
        --system \
        --shell /bin/bash \
        --home-dir /var/lib/hyperi \
        --create-home \
        hyperi
fi

# Create sway group if it doesn't exist (sway package may not create it)
if ! getent group sway &>/dev/null; then
    groupadd sway
fi

# Add hyperi to required groups
usermod -aG audio,video,render,input,sway hyperi 2>/dev/null || true
# seat group may not exist on all systems
usermod -aG seat hyperi 2>/dev/null || true

# ── Directories ────────────────────────────────────────────────────────────────
echo "==> Creating directories..."
mkdir -p /var/lib/hyperi/sessions
mkdir -p /var/lib/hyperi/plans
mkdir -p /var/log/hyperi
mkdir -p /etc/hyperi

chown -R hyperi:hyperi /var/lib/hyperi /var/log/hyperi
chmod 750 /var/lib/hyperi /var/log/hyperi

# ── sudoers ────────────────────────────────────────────────────────────────────
echo "==> Installing sudoers config..."
if [ -f "$REPO/distro/sudoers/hyperi" ]; then
    cp "$REPO/distro/sudoers/hyperi" /etc/sudoers.d/hyperi
    chmod 440 /etc/sudoers.d/hyperi
    # Validate before activating
    visudo -c -f /etc/sudoers.d/hyperi
    echo "    sudoers config installed"
else
    echo "    WARNING: $REPO/distro/sudoers/hyperi not found — skipping sudoers install"
fi

# ── sway config ────────────────────────────────────────────────────────────────
echo "==> Installing sway config..."
mkdir -p /etc/sway
cp "$REPO/distro/sway/config" /etc/sway/config

# ── systemd service ────────────────────────────────────────────────────────────
echo "==> Installing hyperi.service..."
cp "$REPO/distro/systemd/hyperi.service" /etc/systemd/system/hyperi.service
systemctl daemon-reload

# Enable but don't start — binary may not be present yet
systemctl enable hyperi || true

# ydotoold — only present on systems with the ydotool daemon unit
if systemctl cat ydotoold &>/dev/null; then
    systemctl enable ydotoold || true
    systemctl start ydotoold || true
else
    echo "    ydotoold.service not found on this system — skipping"
fi

# ── Environment file ───────────────────────────────────────────────────────────
if [ ! -f /etc/hyperi/environment ]; then
    echo "==> Creating environment file..."
    cat > /etc/hyperi/environment << 'EOF'
# HyperiOS environment configuration
# Set your Anthropic API key here:
# ANTHROPIC_API_KEY=sk-ant-...
HYPERI_DATA_DIR=/var/lib/hyperi
HYPERI_LOG_DIR=/var/log/hyperi
WAYLAND_DISPLAY=wayland-1
EOF
    chown root:hyperi /etc/hyperi/environment
    chmod 640 /etc/hyperi/environment
fi

# ── hyperi binary ──────────────────────────────────────────────────────────────
echo "==> Installing hyperi binary (if built)..."
if [ -f "$REPO/dist/hyperi-linux-amd64" ]; then
    cp "$REPO/dist/hyperi-linux-amd64" /usr/local/bin/hyperi
    chmod +x /usr/local/bin/hyperi
    echo "    hyperi binary installed: $(hyperi --version 2>/dev/null || echo 'version unknown')"
else
    echo "    No binary found at $REPO/dist/hyperi-linux-amd64"
    echo "    Run 'go build -buildvcs=false -o dist/hyperi-linux-amd64 ./cmd/hyperi' then re-run provision"
fi

echo ""
echo "==> HyperiOS dev provisioner complete."
echo ""
echo "    Next steps:"
echo "    1. Set ANTHROPIC_API_KEY in /etc/hyperi/environment"
echo "    2. Build: go build -buildvcs=false -o dist/hyperi-linux-amd64 ./cmd/hyperi"
echo "    3. Re-run: sudo bash distro/dev/provision.sh"
echo "    4. Run: sudo systemctl start hyperi"
echo "    5. Check logs: journalctl -u hyperi -f"
