#!/usr/bin/env bash
# Cubestack image entrypoint.
#
# The DevEnvironment controller never sets command/args, so this script decides
# what a container runs. CUBESTACK_IMAGE is baked into each image at build time;
# an optional CUBESTACK_TYPE (future platform mode injection) wins when present.
set -euo pipefail

mode="${CUBESTACK_TYPE:-${CUBESTACK_IMAGE:-ssh}}"

case "$mode" in
  jupyter)
    # Start sshd only when the operator mounted the ssh Secret (ssh.enabled).
    if [ -d /etc/cubestack/ssh ]; then
      /usr/local/bin/cubestack-ssh-setup.sh
      /usr/sbin/sshd -D -e -f /etc/ssh/sshd_config &
    fi
    exec /usr/local/bin/cubestack-run-jupyter.sh
    ;;
  ssh)
    /usr/local/bin/cubestack-ssh-setup.sh
    exec /usr/sbin/sshd -D -e -f /etc/ssh/sshd_config
    ;;
  *)
    echo "cubestack: unknown mode '$mode'" >&2
    exit 1
    ;;
esac
