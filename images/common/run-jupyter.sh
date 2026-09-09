#!/usr/bin/env bash
# Launch JupyterLab for the platform 'jupyter' DevEnvironment type.
#
# Settings are passed as CLI flags rather than written to a config file: CLI
# options override the docker-stacks /etc/jupyter server config, which hardcodes
# root_dir=/home/jovyan. JUPYTER_TOKEN is required (the operator injects it from
# a Secret); base_url defaults to "/" and is overridable via CUBESTACK_BASE_URL.
set -euo pipefail

token="${JUPYTER_TOKEN:-}"
workspace="${CUBESTACK_WORKSPACE:-/workspace}"

if [ -z "$token" ]; then
  echo "cubestack: JUPYTER_TOKEN is required; refusing unauthenticated start" >&2
  exit 1
fi

args=(
  lab
  --ip=0.0.0.0
  --port=8888
  --no-browser
  --IdentityProvider.token="$token"
  --ServerApp.root_dir="$workspace"
  --ServerApp.trust_xheaders=True
  --ServerApp.allow_remote_access=True
)
if [ -n "${CUBESTACK_BASE_URL:-}" ]; then
  args+=(--ServerApp.base_url="$CUBESTACK_BASE_URL")
fi

cd "$workspace"
exec jupyter "${args[@]}"
