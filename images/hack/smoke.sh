#!/usr/bin/env bash
# Local Docker smoke for the Cubestack DevEnvironment base images.
#
# No cluster required: runs throwaway containers on 127.0.0.1 with ephemeral
# ports and fake ssh Secrets, then asserts the operator contract:
#   ssh-ubuntu22.04   : key-auth ssh login as 'ubuntu' (uid/gid 1000, home
#                       /home/ubuntu), served host key == mounted Secret public key
#   jupyter-minimal   : stock-native overlay (user 'jovyan', uid 1000 gid 100,
#                       /home/jovyan) — JupyterLab behind JUPYTER_TOKEN +
#                       NOTEBOOK_ARGS base_url, plus sshd-as-'jovyan' when the ssh
#                       Secret is mounted; both services in the same container
#
# sshd listens on the unprivileged :2222 (so no NET_BIND_SERVICE is needed); the
# platform's Service publishes it as 22. The smoke talks to 2222 directly.
#
# The operator mounts the Secret keys with subPath; docker has no subPath, so the
# smoke reproduces it with per-file bind mounts:
#   ssh_host_ed25519_key -> /etc/ssh/ssh_host_ed25519_key
#   authorized_keys      -> $HOME/.ssh/authorized_keys2
#
# Reads IMG_SSH / IMG_JUPYTER from the environment (the Makefile sets them).
# Usage: hack/smoke.sh [--ssh|--jupyter]    (default: both)
set -euo pipefail

cd "$(dirname "$0")/.." # images/ workspace root

# Fallback tags mirror the Makefile default: TAG is the short commit SHA.
TAG="${TAG:-$(git rev-parse --short HEAD 2>/dev/null || echo latest)}"
IMG_SSH="${IMG_SSH:-harbor.isuanova.com/suanova/ssh-ubuntu22.04:$TAG}"
IMG_JUPYTER="${IMG_JUPYTER:-harbor.isuanova.com/suanova/jupyter-minimal:$TAG}"
CONTAINER_TOOL="${CONTAINER_TOOL:-docker}"

run_ssh=1
run_jupyter=1
case "${1:-}" in
  --ssh) run_jupyter=0 ;;
  --jupyter) run_ssh=0 ;;
  -h | --help) sed -n '2,20p' "$0"; exit 0 ;;
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

# make_secret <parent> — generate a throwaway host keypair + authorized_keys that
# emulates an operator Secret.
#
# Kubernetes projects Secret files root-owned 0644, and a non-root sshd accepts
# them because OpenSSH only enforces private-key permissions on files owned by the
# uid doing the reading. Docker cannot reproduce that ownership (Docker Desktop
# reports a bind-mounted file as owned by the calling user), so the smoke mounts
# the private key 0600 — the mode sshd demands when the owner check does apply.
make_secret() {
  local base=$1
  mkdir -p "$base/client" "$base/host"
  ssh-keygen -q -t ed25519 -N "" -f "$base/client/id_ed25519"
  ssh-keygen -q -t ed25519 -N "" -f "$base/host/ssh_host_ed25519_key"
  cp "$base/client/id_ed25519.pub" "$base/host/authorized_keys"
  chmod 600 "$base/host/ssh_host_ed25519_key" "$base/host/authorized_keys"
  chmod 644 "$base/host/ssh_host_ed25519_key.pub"
  chmod 700 "$base/client"
  chmod 600 "$base/client/id_ed25519"
}

# served_key <port> — the ed25519 host key sshd serves, or empty if it is not
# serving yet. A bare TCP connect is not sufficient: docker's port proxy accepts
# the connection even when nothing listens inside the container.
served_key() {
  ssh-keyscan -t ed25519 -p "$1" 127.0.0.1 2>/dev/null | awk '!/^#/ && NF {print $NF; exit}'
}

# wait_ssh <port> <seconds> <container-name>
wait_ssh() {
  local port=$1 secs=$2 name=$3 i
  for i in $(seq 1 "$secs"); do
    if [ -n "$(served_key "$port")" ]; then return 0; fi
    sleep 1
  done
  echo "  container $name did not serve an ssh host key on port $port within ${secs}s; last logs:"
  "$CONTAINER_TOOL" logs "$name" 2>&1 | tail -n 20
  return 1
}

# check_served_host_key <port> <secret-host-pub> <container> <label>
check_served_host_key() {
  local port=$1 pub=$2 name=$3 label=$4 served
  served="$(served_key "$port")"
  if [ -n "$served" ] && [ "$served" = "$(awk '{print $2}' "$pub")" ]; then
    ok "$label"
  else
    bad "$label"
    "$CONTAINER_TOOL" logs "$name" 2>&1 | tail -n 20
  fi
}

# ---------------------------------------------------------------------------
# ssh-ubuntu22.04
# ---------------------------------------------------------------------------
if [ "$run_ssh" = 1 ]; then
  echo "== smoke: $IMG_SSH (ssh-ubuntu22.04) =="
  ssh_cont="cs-smoke-ssh-$$"
  make_secret "$tmp/ssh"

  "$CONTAINER_TOOL" run -d --name "$ssh_cont" \
    --user 1000:1000 \
    -p 127.0.0.1::2222 \
    -v "$tmp/ssh/host/ssh_host_ed25519_key:/etc/ssh/ssh_host_ed25519_key:ro" \
    -v "$tmp/ssh/host/authorized_keys:/home/ubuntu/.ssh/authorized_keys2:ro" \
    "$IMG_SSH" >/dev/null
  ssh_port="$("$CONTAINER_TOOL" port "$ssh_cont" 2222 | head -n1 | sed 's/^.*://')"
  wait_ssh "$ssh_port" 30 "$ssh_cont"

  out="$(ssh -i "$tmp/ssh/client/id_ed25519" \
    -p "$ssh_port" \
    -o BatchMode=yes -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null -o IdentitiesOnly=yes \
    ubuntu@127.0.0.1 \
    'printf "uid=%s gid=%s home=%s pwd=%s\n" "$(id -u)" "$(id -gn)" "$HOME" "$PWD"' \
    2>&1 || true)"

  check_contains "$out" "uid=1000" "ssh key-auth login as uid 1000"
  check_contains "$out" "gid=ubuntu" "ssh login primary group 'ubuntu'"
  check_contains "$out" "home=/home/ubuntu" "ssh login HOME=/home/ubuntu"
  check_contains "$out" "pwd=/home/ubuntu" "ssh login cwd=/home/ubuntu"

  check_served_host_key "$ssh_port" "$tmp/ssh/host/ssh_host_ed25519_key.pub" \
    "$ssh_cont" "served host key == mounted Secret public key"

  if ! case "$out" in *"uid=1000"*) true ;; *) false ;; esac; then
    echo "  container logs:"
    "$CONTAINER_TOOL" logs "$ssh_cont" 2>&1 | tail -n 20
    echo "  mounted files as the container sees them:"
    "$CONTAINER_TOOL" exec --user 0 "$ssh_cont" ls -ln \
      /etc/ssh/ssh_host_ed25519_key /home/ubuntu/.ssh/authorized_keys2 2>&1 || true
  fi
  "$CONTAINER_TOOL" rm -f "$ssh_cont" >/dev/null 2>&1 || true
  ssh_cont=""
fi

# ---------------------------------------------------------------------------
# jupyter-minimal (stock-native overlay: jupyter server + optional sshd)
# ---------------------------------------------------------------------------
if [ "$run_jupyter" = 1 ]; then
  echo "== smoke: $IMG_JUPYTER (jupyter-minimal) =="
  jup_cont="cs-smoke-jupyter-$$"
  base="/dev/ns/env"
  make_secret "$tmp/jupssh"

  # Native identity (uid 1000, gid 'users' 100) and pure-stock knobs: token via
  # JUPYTER_TOKEN, URL prefix via NOTEBOOK_ARGS. The ssh Secret is mounted so the
  # overlay's sshd starts next to jupyter.
  "$CONTAINER_TOOL" run -d --name "$jup_cont" \
    --user 1000:100 \
    -p 127.0.0.1::8888 \
    -p 127.0.0.1::2222 \
    -e JUPYTER_TOKEN=testtoken \
    -e NOTEBOOK_ARGS="--ServerApp.base_url=$base/" \
    -v "$tmp/jupssh/host/ssh_host_ed25519_key:/etc/ssh/ssh_host_ed25519_key:ro" \
    -v "$tmp/jupssh/host/authorized_keys:/home/jovyan/.ssh/authorized_keys2:ro" \
    "$IMG_JUPYTER" >/dev/null
  jup_port="$("$CONTAINER_TOOL" port "$jup_cont" 8888 | head -n1 | sed 's/^.*://')"
  jup_ssh_port="$("$CONTAINER_TOOL" port "$jup_cont" 2222 | head -n1 | sed 's/^.*://')"

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

  id_out="$("$CONTAINER_TOOL" exec "$jup_cont" sh -c 'printf "%s %s" "$(id -u)" "$(id -g)"' 2>/dev/null || true)"
  if [ "$id_out" = "1000 100" ]; then
    ok "container runs as native uid 1000 gid 100"
  else
    bad "expected '1000 100', got '$id_out'"
  fi

  # sshd on the same container (ssh Secret mounted -> ssh.enabled).
  wait_ssh "$jup_ssh_port" 30 "$jup_cont"

  out="$(ssh -i "$tmp/jupssh/client/id_ed25519" \
    -p "$jup_ssh_port" \
    -o BatchMode=yes -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null -o IdentitiesOnly=yes \
    jovyan@127.0.0.1 \
    'printf "uid=%s home=%s pwd=%s\n" "$(id -u)" "$HOME" "$PWD"' \
    2>&1 || true)"
  check_contains "$out" "uid=1000" "jupyter ssh key-auth login as uid 1000"
  check_contains "$out" "home=/home/jovyan" "jupyter ssh login HOME=/home/jovyan"

  check_served_host_key "$jup_ssh_port" "$tmp/jupssh/host/ssh_host_ed25519_key.pub" \
    "$jup_cont" "jupyter served host key == mounted Secret public key"

  if ! case "$out" in *"uid=1000"*) true ;; *) false ;; esac; then
    echo "  container logs:"
    "$CONTAINER_TOOL" logs "$jup_cont" 2>&1 | tail -n 20
    echo "  mounted files as the container sees them:"
    "$CONTAINER_TOOL" exec --user 0 "$jup_cont" ls -ln \
      /etc/ssh/ssh_host_ed25519_key /home/jovyan/.ssh/authorized_keys2 2>&1 || true
  fi
  "$CONTAINER_TOOL" rm -f "$jup_cont" >/dev/null 2>&1 || true
  jup_cont=""
fi

echo
echo "smoke summary: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
