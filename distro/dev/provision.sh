#!/usr/bin/env bash
# HyperiOS dev VM provisioner
# Idempotent — safe to run multiple times via 'vagrant provision'
# Mirrors what cloud-init/user-data.yaml does on a real install.

set -euo pipefail

echo "==> HyperiOS dev provisioner starting"

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
cp /vagrant/distro/sudoers/hyperi /etc/sudoers.d/hyperi
chmod 440 /etc/sudoers.d/hyperi
# Validate
visudo -c -f /etc/sudoers.d/hyperi

# ── sway config ────────────────────────────────────────────────────────────────
echo "==> Installing sway config..."
mkdir -p /etc/sway
cp /vagrant/distro/sway/config /etc/sway/config

# ── systemd service ────────────────────────────────────────────────────────────
echo "==> Installing hyperi.service..."
cp /vagrant/distro/systemd/hyperi.service /etc/systemd/system/hyperi.service
systemctl daemon-reload

# Enable but don't start — binary may not be present yet
systemctl enable hyperi || true

# ydotoold
systemctl enable ydotoold || true
systemctl start ydotoold || true

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
if [ -f /vagrant/dist/hyperi-linux-amd64 ]; then
    cp /vagrant/dist/hyperi-linux-amd64 /usr/local/bin/hyperi
    chmod +x /usr/local/bin/hyperi
    echo "    hyperi binary installed: $(hyperi --version 2>/dev/null || echo 'version unknown')"
else
    echo "    No binary found at /vagrant/dist/hyperi-linux-amd64"
    echo "    Run 'just build' on host then 'vagrant provision' to install"
fi

echo ""
echo "==> HyperiOS dev provisioner complete."
echo ""
echo "    Next steps:"
echo "    1. Set ANTHROPIC_API_KEY in /etc/hyperi/environment"
echo "    2. Run: just build  (on host), then: vagrant provision"
echo "    3. Run: sudo systemctl start hyperi"
echo "    4. Check logs: journalctl -u hyperi -f"
