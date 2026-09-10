#!/usr/bin/env bash
# Cubestack image entrypoint.
#
# The DevEnvironment controller never sets command/args, so this script decides
# what a container runs. CUBESTACK_IMAGE is baked into each image at build time;
# an optional CUBESTACK_TYPE (future platform mode injection) wins when present.
#
# sshd reads the operator's Secret mounts directly — there is no key staging. The
# Secret is mounted with subPath, so its contents are frozen at container start
# (see images/README.md).
#
# Modes:
#   jupyter  start sshd only when the operator mounted the ssh Secret, then hand
#            off to the image CMD (the stock-derived jupyter overlay CMD is the
#            stock launch chain: start.sh start-notebook.py).
#   ssh      run sshd in the foreground.
set -euo pipefail

mode="${CUBESTACK_TYPE:-${CUBESTACK_IMAGE:-ssh}}"

# The operator mounts the ssh Secret's host key here. Images ship no host keys of
# their own, so the file's presence means ssh is enabled.
ssh_enabled() { [ -f /etc/ssh/ssh_host_ed25519_key ]; }

case "$mode" in
  jupyter)
    if ssh_enabled; then
      /usr/sbin/sshd -D -e -f /etc/ssh/sshd_config &
    fi
    # Hand off to the image CMD (the image decides its launch chain).
    exec "$@"
    ;;
  ssh)
    if ! ssh_enabled; then
      echo "cubestack: no host key at /etc/ssh/ssh_host_ed25519_key;" \
           "is the ssh Secret mounted?" >&2
      exit 1
    fi
    exec /usr/sbin/sshd -D -e -f /etc/ssh/sshd_config
    ;;
  *)
    echo "cubestack: unknown mode '$mode'" >&2
    exit 1
    ;;
esac
