#!/usr/bin/env bash
# HyperiOS WSL2 provisioner — run as root inside WSL2
# Usage: wsl -d Ubuntu-24.04 -u root -- bash /mnt/c/Users/ian/Code/hyperiOS/distro/dev/provision-wsl.sh
#
# Mirrors provision.sh but adapted for WSL2:
#   - Runs as root directly (no sudo needed)
#   - Repo available at REPO_PATH
#   - Skips VirtualBox guest additions, ydotoold, sway compositor (no display server)
#   - systemd not available in WSL2 by default — services managed manually

set -euo pipefail

REPO_PATH="/mnt/c/Users/ian/Code/hyperiOS"

echo "==> HyperiOS WSL2 provisioner starting"

# ── System update ──────────────────────────────────────────────────────────────
echo "==> Updating packages..."
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get upgrade -y -qq

# ── Install packages ───────────────────────────────────────────────────────────
echo "==> Installing packages..."
# Note: sway, foot, grim, ydotool excluded — no Wayland display server in WSL2
# These will be installed on real hardware / VM with display
apt-get install -y -qq \
    curl \
    wget \
    git \
    jq \
    network-manager \
    flatpak \
    inotify-tools \
    at-spi2-core \
    openssh-client \
    vim \
    htop \
    tmux \
    unzip \
    build-essential \
    cmake \
    alsa-utils \
    xdg-utils \
    golang-go

# ── inotify watch limit ────────────────────────────────────────────────────────
echo "==> Configuring inotify..."
echo "fs.inotify.max_user_watches=524288" > /etc/sysctl.d/90-hyperi.conf
# sysctl --system doesn't work in WSL2 without systemd; set directly
sysctl -w fs.inotify.max_user_watches=524288 2>/dev/null || true

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

# ── Directories ────────────────────────────────────────────────────────────────
echo "==> Creating directories..."
mkdir -p /var/lib/hyperi/sessions
mkdir -p /var/lib/hyperi/plans
mkdir -p /var/log/hyperi
mkdir -p /etc/hyperi

chown -R hyperi:hyperi /var/lib/hyperi /var/log/hyperi
# 770: hyperi user + hyperi group (dev users added to hyperi group) can read/write
chmod 770 /var/lib/hyperi /var/lib/hyperi/sessions /var/lib/hyperi/plans /var/log/hyperi

# ── sudoers ────────────────────────────────────────────────────────────────────
echo "==> Installing sudoers config..."
cp "$REPO_PATH/distro/sudoers/hyperi" /etc/sudoers.d/hyperi
chmod 440 /etc/sudoers.d/hyperi
visudo -c -f /etc/sudoers.d/hyperi

# ── Environment file ───────────────────────────────────────────────────────────
if [ ! -f /etc/hyperi/environment ]; then
    echo "==> Creating environment file..."
    cat > /etc/hyperi/environment << 'EOF'
# HyperiOS environment configuration
# Set your Anthropic API key here:
# ANTHROPIC_API_KEY=sk-ant-...
HYPERI_DATA_DIR=/var/lib/hyperi
HYPERI_LOG_DIR=/var/log/hyperi
EOF
    chown root:hyperi /etc/hyperi/environment
    chmod 640 /etc/hyperi/environment
fi

# ── hyperi binary ──────────────────────────────────────────────────────────────
echo "==> Installing hyperi binary (if built)..."
if [ -f "$REPO_PATH/dist/hyperi-linux-amd64" ]; then
    cp "$REPO_PATH/dist/hyperi-linux-amd64" /usr/local/bin/hyperi
    chmod +x /usr/local/bin/hyperi
    echo "    hyperi binary installed"
else
    echo "    No binary at $REPO_PATH/dist/hyperi-linux-amd64"
    echo "    Run 'just build' on Windows host first"
fi

# ── WSL2 convenience: add repo to PATH for isellar user ───────────────────────
PROFILE="/home/isellar/.bashrc"
if ! grep -q "HYPERI_DATA_DIR" "$PROFILE" 2>/dev/null; then
    echo "" >> "$PROFILE"
    echo "# HyperiOS dev environment" >> "$PROFILE"
    echo "export HYPERI_DATA_DIR=/var/lib/hyperi" >> "$PROFILE"
    echo "export HYPERI_LOG_DIR=/var/log/hyperi" >> "$PROFILE"
    echo "export REPO=/mnt/c/Users/ian/Code/hyperiOS" >> "$PROFILE"
fi

echo ""
echo "==> HyperiOS WSL2 provisioner complete."
echo ""
echo "    Next steps:"
echo "    1. Add your API key: echo 'ANTHROPIC_API_KEY=sk-ant-...' >> /etc/hyperi/environment"
echo "    2. Build binary: cd /mnt/c/Users/ian/Code/hyperiOS && just build"
echo "    3. Run: /usr/local/bin/hyperi --help"
