#!/usr/bin/env bash
# Cubestack jupyter launch chain for the MACA image.
#
# This is the image CMD. common/entrypoint.sh has already started sshd when the operator mounted
# the ssh host key, and hands off here (mode jupyter). The vendor base ships no launcher, so this
# is the platform's — and it has to do the two things docker-stacks' start.sh does for the CPU
# jupyter image, because the controller injects the same two variables for both:
#
#   JUPYTER_TOKEN  the operator injects it (a secretKeyRef to <env>-jupyter-token) for every
#                  jupyter-type environment, and jupyter-server reads that variable itself.
#
#   NOTEBOOK_ARGS  the platform's flag channel. The controller puts
#                  `--ServerApp.base_url=<webPath>` in it, and jupyter does NOT read the
#                  variable — a launcher has to expand it. Word-splitting is the point of the
#                  unquoted expansion below, which is why shellcheck is silenced for that line:
#                  quoting it would pass the whole string as one flag.
#
# --ServerApp.root_dir is the workspace: $HOME is the account's home, where the platform mounts
# the PVC, so a user's notebooks land on durable storage rather than in the image.
set -euo pipefail

# shellcheck disable=SC2086
exec jupyter lab \
  --ip=0.0.0.0 \
  --port=8888 \
  --no-browser \
  --ServerApp.allow_remote_access=True \
  --ServerApp.root_dir="$HOME" \
  ${NOTEBOOK_ARGS:-}
