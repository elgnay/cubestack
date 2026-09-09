#!/usr/bin/env bash
# Local Docker smoke for the Cubestack DevEnvironment base images.
#
# No cluster required: runs throwaway containers on 127.0.0.1 with ephemeral
# ports and fake /etc/cubestack/ssh Secrets, then asserts the operator contract:
#   ssh-ubuntu-server : key-auth ssh login as 'user' (uid 1000), $HOME/cwd on
#                       /workspace, served host key == mounted Secret public key
#   jupyter           : JupyterLab behind JUPYTER_TOKEN + CUBESTACK_BASE_URL, uid 1000
#
# Reads IMG_SSH / IMG_JUPYTER from the environment (the Makefile sets them).
# Usage: hack/smoke.sh [--ssh|--jupyter]    (default: both)
set -euo pipefail

cd "$(dirname "$0")/.." # images/ workspace root

IMG_SSH="${IMG_SSH:-cubestack/ssh-ubuntu-server:smoke}"
IMG_JUPYTER="${IMG_JUPYTER:-cubestack/jupyter:smoke}"
CONTAINER_TOOL="${CONTAINER_TOOL:-docker}"

run_ssh=1
run_jupyter=1
case "${1:-}" in
  --ssh) run_jupyter=0 ;;
  --jupyter) run_ssh=0 ;;
  -h | --help) sed -n '2,12p' "$0"; exit 0 ;;
  "") ;;
  *) echo "cubestack smoke: unknown option '$1'" >&2; exit 1 ;;
esac

tmp="$(mktemp -d "${TMPDIR:-/tmp}/cubestack-smoke.XXXXXX")"
ssh_cont=""
jup_cont=""
cleanup() {
  local code=$?
  [ -n "$ssh_cont" ] && "$CONTAINER_TOOL" rm -f "$ssh_cont" >/dev/null 2>&1 || true
  [ -n "$jup_cont" ] && "$CONTAINER_TOOL" rm -f "$jup_cont" >/dev/null 2>&1 || true
  rm -rf "$tmp"
  exit $code
}
trap cleanup EXIT

pass=0
fail=0
ok()  { echo "  PASS  $1"; pass=$((pass + 1)); }
bad() { echo "  FAIL  $1"; fail=$((fail + 1)); }

# check_contains <haystack> <needle> <label>
check_contains() {
  case "$1" in
    *"$2"*) ok "$3" ;;
    *) bad "$3 (missing: $2)" ;;
  esac
}

# wait_tcp <port> <seconds> <container-name>
wait_tcp() {
  local port=$1 secs=$2 name=$3 i
  for i in $(seq 1 "$secs"); do
    if nc -z 127.0.0.1 "$port" 2>/dev/null; then return 0; fi
    sleep 1
  done
  echo "  container $name did not open port $port within ${secs}s; last logs:"
  "$CONTAINER_TOOL" logs "$name" 2>&1 | tail -n 20
  return 1
}

# ---------------------------------------------------------------------------
# ssh-ubuntu-server
# ---------------------------------------------------------------------------
if [ "$run_ssh" = 1 ]; then
  echo "== smoke: $IMG_SSH (ssh-ubuntu-server) =="
  ssh_cont="cs-smoke-ssh-$$"
  mkdir -p "$tmp/ssh/client" "$tmp/ssh/host"

  ssh-keygen -q -t ed25519 -N "" -f "$tmp/ssh/client/id_ed25519"
  ssh-keygen -q -t ed25519 -N "" -f "$tmp/ssh/host/ssh_host_ed25519_key"
  cp "$tmp/ssh/client/id_ed25519.pub" "$tmp/ssh/host/authorized_keys"
  # Emulate an operator Secret: dir traversable, files world-readable (0644).
  chmod 755 "$tmp/ssh/host"
  chmod 644 "$tmp/ssh/host/ssh_host_ed25519_key" \
            "$tmp/ssh/host/ssh_host_ed25519_key.pub" \
            "$tmp/ssh/host/authorized_keys"
  chmod 700 "$tmp/ssh/client"
  chmod 600 "$tmp/ssh/client/id_ed25519"

  "$CONTAINER_TOOL" run -d --name "$ssh_cont" \
    --cap-add=NET_BIND_SERVICE \
    --user 1000:1000 \
    -p 127.0.0.1::22 \
    -v "$tmp/ssh/host:/etc/cubestack/ssh:ro" \
    "$IMG_SSH" >/dev/null
  ssh_port="$("$CONTAINER_TOOL" port "$ssh_cont" 22 | head -n1 | sed 's/^.*://')"
  wait_tcp "$ssh_port" 30 "$ssh_cont"

  out="$(ssh -i "$tmp/ssh/client/id_ed25519" \
    -p "$ssh_port" \
    -o BatchMode=yes -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null -o IdentitiesOnly=yes \
    user@127.0.0.1 \
    'printf "uid=%s home=%s pwd=%s\n" "$(id -u)" "$HOME" "$PWD"; cat /home/user/cubestack/ssh/ssh_host_ed25519_key.pub' \
    2>&1 || true)"

  check_contains "$out" "uid=1000" "ssh key-auth login as uid 1000"
  check_contains "$out" "home=/workspace" "ssh login HOME=/workspace"
  check_contains "$out" "pwd=/workspace" "ssh login cwd=/workspace"

  expected_pub="$(cat "$tmp/ssh/host/ssh_host_ed25519_key.pub")"
  check_contains "$out" "$expected_pub" "staged host key == mounted Secret key"

  served="$(ssh-keyscan -p "$ssh_port" 127.0.0.1 2>/dev/null | grep -v '^#' | head -n1 | awk '{print $NF}')"
  mounted_key="$(awk '{print $2}' "$tmp/ssh/host/ssh_host_ed25519_key.pub")"
  if [ "$served" = "$mounted_key" ]; then
    ok "served host key == mounted Secret public key"
  else
    bad "served host key differs from mounted Secret key"
  fi

  if ! case "$out" in *"uid=1000"*) true ;; *) false ;; esac; then
    echo "  container logs:"
    "$CONTAINER_TOOL" logs "$ssh_cont" 2>&1 | tail -n 20
  fi
  "$CONTAINER_TOOL" rm -f "$ssh_cont" >/dev/null 2>&1 || true
  ssh_cont=""
fi

# ---------------------------------------------------------------------------
# jupyter
# ---------------------------------------------------------------------------
if [ "$run_jupyter" = 1 ]; then
  echo "== smoke: $IMG_JUPYTER (jupyter) =="
  jup_cont="cs-smoke-jupyter-$$"
  base="/dev/ns/env"

  "$CONTAINER_TOOL" run -d --name "$jup_cont" \
    --user 1000:1000 \
    -p 127.0.0.1::8888 \
    -e JUPYTER_TOKEN=testtoken \
    -e CUBESTACK_BASE_URL="$base/" \
    "$IMG_JUPYTER" >/dev/null
  jup_port="$("$CONTAINER_TOOL" port "$jup_cont" 8888 | head -n1 | sed 's/^.*://')"

  printf "  waiting for JupyterLab"
  up=0
  for _ in $(seq 1 90); do
    if curl -fsS -o /dev/null "http://127.0.0.1:$jup_port$base/api/status?token=testtoken" 2>/dev/null; then
      up=1
      break
    fi
    printf "."
    sleep 1
  done
  echo
  if [ "$up" = 1 ]; then
    ok "jupyter /api/status with token -> 200"
  else
    bad "jupyter did not become reachable within 90s"
    "$CONTAINER_TOOL" logs "$jup_cont" 2>&1 | tail -n 30
  fi

  code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$jup_port$base/api/status")"
  case "$code" in
    401 | 403) ok "no token rejected (HTTP $code)" ;;
    *) bad "no-token request expected 401/403, got $code" ;;
  esac

  code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$jup_port/api/status?token=testtoken")"
  if [ "$code" = 404 ]; then
    ok "base_url prefix enforced (root path -> 404)"
  else
    bad "root path without base_url expected 404, got $code"
  fi

  html="$(curl -fsSL "http://127.0.0.1:$jup_port$base/?token=testtoken" 2>/dev/null || true)"
  check_contains "$html" "jupyter-config-data" "lab HTML served on base_url path"

  uid="$("$CONTAINER_TOOL" exec "$jup_cont" id -u 2>/dev/null || true)"
  if [ "$uid" = 1000 ]; then
    ok "container runs as uid 1000"
  else
    bad "expected uid 1000, got '$uid'"
  fi
  "$CONTAINER_TOOL" rm -f "$jup_cont" >/dev/null 2>&1 || true
  jup_cont=""
fi

echo
echo "smoke summary: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
