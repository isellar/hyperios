#!/usr/bin/env bash
# Start sway with headless backend for dev/CI environments without a display.
# Useful for testing swaymsg IPC, execute:display steps, and service startup
# in the Vagrant VM or any headless Linux environment.
#
# Usage:
#   ./distro/dev/sway-headless.sh
#   ./distro/dev/sway-headless.sh &   # background
#
# After starting, SWAYSOCK is printed — export it to use swaymsg:
#   export SWAYSOCK=$(./distro/dev/sway-headless.sh --print-socket)

set -euo pipefail

export WLR_BACKENDS=headless
export WLR_RENDERER=pixman
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/tmp/hyperi-xdg-runtime}"

mkdir -p "$XDG_RUNTIME_DIR"
chmod 700 "$XDG_RUNTIME_DIR"

echo "Starting sway (headless backend)..."
echo "XDG_RUNTIME_DIR=$XDG_RUNTIME_DIR"

exec sway -c /etc/sway/config "$@"
