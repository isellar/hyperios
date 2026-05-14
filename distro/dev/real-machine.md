# HyperiOS — Real Machine Testing Guide

How to provision a real machine for testing without the Phase 5 ISO builder.
This works with any bare metal laptop or desktop running Ubuntu 24.04 Server.

---

## Prerequisites

- A machine to install on (anything x86_64 with 2GB+ RAM)
- A USB drive (4GB+)
- The built `hyperi` binary (`just build` on your dev machine)
- Your Anthropic API key

---

## Step 1 — Install Ubuntu 24.04 Server

Download the Ubuntu 24.04 LTS Server ISO:
```
https://releases.ubuntu.com/24.04/ubuntu-24.04-live-server-amd64.iso
```

Flash to USB (replace `/dev/sdX` with your USB device):
```bash
sudo dd if=ubuntu-24.04-live-server-amd64.iso of=/dev/sdX bs=4M status=progress && sync
```

Boot from USB. During install:
- Choose **Ubuntu Server (minimized)** if offered
- Set hostname: `hyperios`
- Create a user (e.g. `admin`) — this is the human operator account
- Enable OpenSSH server
- Do not install any additional snaps

---

## Step 2 — Run the provisioner

After first boot, copy the repo's provisioning script and run it:

```bash
# On the new machine, clone the repo or copy the distro/dev directory
git clone https://github.com/isellar/hyperios /opt/hyperios

# Run provisioner (creates hyperi user, installs packages, sets up directories)
sudo bash /opt/hyperios/distro/dev/provision.sh
```

Alternatively, copy just the necessary files:
```bash
# From your dev machine
scp distro/dev/provision.sh admin@<machine-ip>:/tmp/
scp distro/sudoers/hyperi admin@<machine-ip>:/tmp/
scp distro/sway/config admin@<machine-ip>:/tmp/
scp distro/systemd/hyperi.service admin@<machine-ip>:/tmp/

# On the target machine
sudo bash /tmp/provision.sh
```

---

## Step 3 — Copy the binary

Build on your dev machine:
```bash
just build
```

Copy to the target:
```bash
scp dist/hyperi-linux-amd64 admin@<machine-ip>:/tmp/hyperi
ssh admin@<machine-ip> 'sudo mv /tmp/hyperi /usr/local/bin/hyperi && sudo chmod +x /usr/local/bin/hyperi'
```

---

## Step 4 — Set your API key

```bash
ssh admin@<machine-ip>
sudo nano /etc/hyperi/environment
```

Add your key:
```
ANTHROPIC_API_KEY=sk-ant-...
```

Save and exit. File is owned `root:hyperi` with `640` permissions — only the `hyperi` service user can read it.

---

## Step 5 — Start and verify

```bash
# Start the service
sudo systemctl start hyperi

# Watch the logs
sudo journalctl -u hyperi -f

# Check status
sudo systemctl status hyperi
```

---

## Step 6 — SSH as the hyperi user (optional, for debugging)

The `hyperi` user has a shell but no password. Add your SSH key:
```bash
sudo mkdir -p /var/lib/hyperi/.ssh
sudo cp ~/.ssh/authorized_keys /var/lib/hyperi/.ssh/
sudo chown -R hyperi:hyperi /var/lib/hyperi/.ssh
sudo chmod 700 /var/lib/hyperi/.ssh
sudo chmod 600 /var/lib/hyperi/.ssh/authorized_keys

# Then SSH in:
ssh -i ~/.ssh/your_key hyperi@<machine-ip>
```

---

## Iterating

After rebuilding the binary on your dev machine:
```bash
scp dist/hyperi-linux-amd64 admin@<machine-ip>:/tmp/hyperi
ssh admin@<machine-ip> 'sudo mv /tmp/hyperi /usr/local/bin/hyperi && sudo systemctl restart hyperi'
```

Check logs immediately after:
```bash
ssh admin@<machine-ip> 'sudo journalctl -u hyperi -n 50'
```

---

## Notes

- sway is installed but not started by default — it is not required for Phases 0–2
- The `hyperi.service` runs headlessly; the TUI is accessible via SSH
- Phase 4 (display management) requires starting sway: `sudo -u hyperi WLR_BACKENDS=headless sway &`
- The service restarts automatically on failure (up to 3 times per 60 seconds)
