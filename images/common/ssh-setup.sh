#!/usr/bin/env bash
# Stage the operator-mounted ssh Secret for a non-root sshd.
#
# The Secret is mounted read-only at /etc/cubestack/ssh, root-owned, mode 0644
# (operator defaults). sshd refuses a world-readable private key and must be able
# to write its pid file, so this copies the files to a user-owned directory with
# tightened permissions before sshd starts. Host keys stay stable across restarts
# because they come from the Secret mount, not the image layer.
set -euo pipefail

src=/etc/cubestack/ssh
dst=/home/user/cubestack/ssh

mkdir -p "$dst"
chmod 700 "$dst"

if [ -f "$src/ssh_host_ed25519_key" ]; then
  # Operator-provided host key.
  cp -Lf "$src/ssh_host_ed25519_key" "$dst/ssh_host_ed25519_key"
  chmod 600 "$dst/ssh_host_ed25519_key"
  cp -Lf "$src/ssh_host_ed25519_key.pub" "$dst/ssh_host_ed25519_key.pub"
  chmod 644 "$dst/ssh_host_ed25519_key.pub"
else
  # No Secret mounted (manual/local run): ephemeral host key.
  ssh-keygen -q -t ed25519 -N "" -f "$dst/ssh_host_ed25519_key" -C "cubestack-$(hostname)"
  chmod 600 "$dst/ssh_host_ed25519_key"
fi

if [ -f "$src/authorized_keys" ]; then
  cp -Lf "$src/authorized_keys" "$dst/authorized_keys"
else
  # Empty authorized_keys: no password auth and no keys -> no logins.
  : > "$dst/authorized_keys"
fi
chmod 600 "$dst/authorized_keys"
