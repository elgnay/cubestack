#!/usr/bin/env bash
# Installs MetalLB into the helm-e2e kind cluster and gives it one address pool.
#
# Why this is here: the helm e2e makes a real DevEnvironment reachable over HTTP
# and SSH through the platform Gateway, and the address it reaches the Gateway on
# has to come from somewhere. kind ships no load balancer, so a Gateway dataplane
# Service of type LoadBalancer would sit EXTERNAL-IP <pending> forever and every
# published endpoint address would be built from an empty gateway IP. MetalLB
# supplies that address.
#
# The pool is derived from the kind docker network's own subnet, not hardcoded:
# docker picks the subnet (usually, but not always, 172.18.0.0/16), and a pool
# outside it is an address nothing routes. The speakers run hostNetwork on the
# node, so an address in the node's own subnet is answered by ARP from the node's
# interface — which the Linux CI runner shares, `kind` being a docker bridge on
# the runner's host. On Docker Desktop it does not: the bridge lives inside the
# VM, so the VIP is reachable from the cluster but not from a macOS shell. Run
# the reachability assertions on CI, or from a container/pod on the cluster.
#
# Idempotent: a settled controller skips the manifest install, and every step
# below it is a re-apply.
set -euo pipefail

# The version is not free to bump on its own: the controller and speaker images
# come from the shared mirrors project, and the only tag that project carries is
# the one below (the rewrite further down changes the repository, never the tag).
# v0.13.9 is what is mirrored there; a newer MetalLB would have to be mirrored
# into mirrors/quay.io/metallb/ before this could name it.
METALLB_VERSION="${METALLB_VERSION:-v0.13.9}"
# The native manifest is fetched, not vendored: it is 2.4k lines of CRDs and
# RBAC that only this test needs. Pinned by checksum for the same reason the CI
# workflow pins the kind binary — a tag alone is a mutable reference.
METALLB_MANIFEST_URL="${METALLB_MANIFEST_URL:-https://raw.githubusercontent.com/metallb/metallb/${METALLB_VERSION}/config/manifests/metallb-native.yaml}"
METALLB_MANIFEST_SHA256="${METALLB_MANIFEST_SHA256:-acf9490589d58d94df025228b58e4bf22190a3d8e50c53736a30b814302a9f77}"

NS="metallb-system"
KIND_NETWORK="${KIND_NETWORK:-kind}"

KIND_CLUSTER="${KIND_CLUSTER_HELM:-cubestack-helm-e2e}"
CTX="kind-${KIND_CLUSTER}"
KUBECTL="${KUBECTL:-kubectl}"
DOCKER="${DOCKER:-docker}"

cd "$(dirname "$0")/.." # operator/

# Skips the installation only, never the rest of the script: the pool and its
# advertisement are applied below either way. Exiting here instead — as this
# once did — means a run that stopped after the controller became Available but
# before the pool was applied leaves every later run reporting success with no
# IPAddressPool, and the Gateway's Service sitting EXTERNAL-IP <pending> for a
# reason nothing in the log names.
#
# Available is not sufficient on its own: it says a controller answers, not which
# one. A cluster reused across a METALLB_VERSION bump would otherwise keep the old
# controller, never apply the checksum-validated manifest for the new version, and
# report success — the same silent skip, one field over. The installed image's
# tag is the version, because the manifest carries METALLB_VERSION as exactly that
# tag (the rewrite further down changes only the repository, not the tag). Not
# matching means apply the manifest again, which is idempotent.
# Every request in this script that is not already bounded by its own --timeout carries
# a --request-timeout, because kubectl's default of 0 applies no client deadline: a
# server that accepts the connection and never answers leaves the command waiting, and
# nothing here can tell that apart from slowness.
METALLB_READY=""
CONTROLLER="$("${KUBECTL}" --context "${CTX}" --request-timeout=10s \
  get deployment -n "${NS}" controller \
  -o jsonpath='{.status.conditions[?(@.type=="Available")].status}{" "}{.spec.template.spec.containers[0].image}' 2>/dev/null || true)"
if [ "${CONTROLLER% *}" = "True" ] && [ "${CONTROLLER##*:}" = "${METALLB_VERSION}" ]; then
  METALLB_READY=1
  echo "metallb ${METALLB_VERSION} already Available in ${NS} on ${CTX} — reconciling the pool"
fi

# --- address pool ---
# METALLB_POOL overrides the derivation entirely (a kind network that is not a
# /16, or a host where .255.200-.255.250 is not free). MetalLB's own kind
# examples use exactly that sub-range of the default kind subnet.
if [ -n "${METALLB_POOL:-}" ]; then
  POOL_RANGE="${METALLB_POOL}"
else
  # Not `index .IPAM.Config 0`: the kind network is dual-stack, and which family
  # comes first is not stable. On the CI runner and on Docker Desktop index 0 is
  # the IPv6 ULA (fc00:f853:ccd:e793::/64) with the IPv4 subnet at index 1, so
  # indexing [0] reads an address family the pool cannot use. Take the first IPv4
  # subnet — that is the family both the pool and the ARP-based L2 advertisement
  # need.
  SUBNETS="$("${DOCKER}" network inspect "${KIND_NETWORK}" -f '{{range .IPAM.Config}}{{.Subnet}}{{"\n"}}{{end}}' 2>/dev/null || true)"
  SUBNET=""
  while IFS= read -r candidate; do
    case "${candidate}" in
      "" | *:*) continue ;; # an empty line, or an IPv6 subnet
      *) SUBNET="${candidate}"; break ;;
    esac
  done <<< "${SUBNETS}"
  [ -n "${SUBNET}" ] || { echo "no IPv4 subnet on the '${KIND_NETWORK}' docker network; set METALLB_POOL=<start>-<end>" >&2; exit 1; }
  case "${SUBNET}" in
    */16) ;;
    *) echo "kind network ${KIND_NETWORK}'s IPv4 subnet is ${SUBNET}, not a /16 — set METALLB_POOL=<start>-<end> inside it" >&2; exit 1 ;;
  esac
  IFS=. read -r o1 o2 _ <<< "${SUBNET%%/*}"
  POOL_RANGE="${o1}.${o2}.255.200-${o1}.${o2}.255.250"
fi
echo "MetalLB pool: ${POOL_RANGE} (kind network '${KIND_NETWORK}')"

# --- install ---
if [ -z "${METALLB_READY}" ]; then
  OUT="$(mktemp)"
  trap 'rm -f "${OUT}"' EXIT
  echo "fetching MetalLB ${METALLB_VERSION} native manifest"
  # Bounded for the same reason as the kubectl calls above: a remote that accepts the
  # connection and then stops sending would otherwise hold this step open until the job's
  # own timeout, and nothing here can tell that apart from a slow download. This is the
  # one network call in the script that is not a kubectl request, so it takes the
  # equivalent curl flags instead of a --request-timeout.
  curl --connect-timeout 15 --max-time 120 -fsSL "${METALLB_MANIFEST_URL}" -o "${OUT}"
  # sha256sum on the Linux CI runner, shasum on the macOS dev machines.
  if command -v sha256sum >/dev/null 2>&1; then
    DIGEST="$(sha256sum "${OUT}" | awk '{print $1}')"
  else
    DIGEST="$(shasum -a 256 "${OUT}" | awk '{print $1}')"
  fi
  [ "${DIGEST}" = "${METALLB_MANIFEST_SHA256}" ] \
    || { echo "MetalLB manifest checksum mismatch — ${METALLB_VERSION} was re-tagged, or the download is wrong" >&2; exit 1; }

  # The bundle's own image refs point at quay.io. Repoint them at the shared
  # mirrors project, which holds the same tags (hack/mirror-e2e-images.sh): the
  # runner reaches harbor.isuanova.com on the internal network, and quay.io is
  # both slow from it and one rate limit away from failing the job. The mirror
  # names a repo for its upstream reference, so the rewrite is a prefix —
  # quay.io/metallb/<image> becomes <registry>/quay.io/metallb/<image> — and the
  # tag, which is the version, is untouched. The checksum pin is what makes this
  # safe to do blind: the rewrite is applied to exactly the manifest those refs
  # were read from. Set METALLB_REGISTRY= (explicitly empty) to keep them.
  METALLB_REGISTRY="${METALLB_REGISTRY-harbor.isuanova.com/mirrors}"
  if [ -n "${METALLB_REGISTRY}" ]; then
    sed "s#quay\.io/metallb/#${METALLB_REGISTRY}/quay.io/metallb/#g" \
        "${OUT}" > "${OUT}.registry" && mv "${OUT}.registry" "${OUT}"
    # Fail loudly if the bundle stops carrying the refs this rewrote. Silently
    # falling back to quay.io would still pass on a runner that can reach it, so
    # the mirror would rot unnoticed until the day it mattered. Anchored on
    # `image:` because a rewritten ref legitimately contains the upstream path —
    # only a ref the sed left alone still reads `image: quay.io/...`.
    grep -qE '^[[:space:]]*image: quay\.io/metallb/' "${OUT}" \
      && { echo "MetalLB manifest still references quay.io after the registry rewrite — its image refs changed shape; update this script" >&2; exit 1; }
  fi

  # --server-side: the CRDs in this bundle (bgppeers, ipaddresspools, ...) carry
  # schemas too large for client-side apply's last-applied-configuration annotation.
  # 60s rather than the 10s the pool calls use: this is the largest payload in the
  # script, and a write that is merely slow is not a failure.
  echo "applying MetalLB to ${CTX}"
  "${KUBECTL}" --context "${CTX}" --request-timeout=60s apply --server-side -f "${OUT}"

  echo "waiting for metallb controller and speakers..."
  "${KUBECTL}" --context "${CTX}" rollout status deployment/controller -n "${NS}" --timeout=300s
  "${KUBECTL}" --context "${CTX}" rollout status daemonset/speaker -n "${NS}" --timeout=300s

  # The common case. Not the whole of it — see the probe below.
  "${KUBECTL}" --context "${CTX}" wait --for=condition=Ready pod -n "${NS}" \
    -l app=metallb,component=controller --timeout=120s
fi

# --- pool + advertisement ---
# Both objects, always. An IPAddressPool with no L2Advertisement is MetalLB's
# classic silent failure: the address is allocated and assigned to the Service's
# status, so `kubectl get svc` looks correct, but nothing ever advertises it and
# every connection to it times out.
POOL_AND_ADVERTISEMENT="$(cat <<YAML
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: cubestack-e2e
  namespace: ${NS}
spec:
  addresses:
    - ${POOL_RANGE}
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: cubestack-e2e
  namespace: ${NS}
spec:
  ipAddressPools:
    - cubestack-e2e
YAML
)"

# A Ready controller pod is not a webhook that answers. The webhook is reached
# through a Service, and until its endpoints are published and the node's rules for
# them are programmed, the ClusterIP is rejected — which reaches the API server as
# "connection refused", failing the apply of an object nothing is wrong with. That
# trails readiness by seconds and has no fixed length, so ask the webhook directly,
# until it answers. Server-side dry run is that question with nothing to create, so
# retrying it cannot leave a half-applied pool behind and the apply below stays a
# single call whose failure means what it says.
#
# Both calls carry a finite --request-timeout — the default is explained on the probe
# above — so each attempt can end, and the loop can therefore bound setup time. Without
# one, a stalled API server would hold it open for as long as the job is allowed to
# run, 15 attempts or not.
POOL_WEBHOOK_ERROR=""
for attempt in $(seq 1 15); do
  if POOL_WEBHOOK_ERROR="$(printf '%s\n' "${POOL_AND_ADVERTISEMENT}" |
      "${KUBECTL}" --context "${CTX}" --request-timeout=10s \
        apply --dry-run=server -f - 2>&1 >/dev/null)"; then
    # Cleared explicitly: a dry run that succeeds but writes a warning to stderr
    # would otherwise read as the failure below.
    POOL_WEBHOOK_ERROR=""
    break
  fi
  echo "MetalLB's webhook is not answering yet (attempt ${attempt}/15); retrying"
  sleep 2
done
[ -z "${POOL_WEBHOOK_ERROR}" ] || {
  echo "MetalLB's webhook did not admit an IPAddressPool after 15 attempts:" >&2
  echo "${POOL_WEBHOOK_ERROR}" >&2
  exit 1
}

printf '%s\n' "${POOL_AND_ADVERTISEMENT}" |
  "${KUBECTL}" --context "${CTX}" --request-timeout=10s apply -f -

echo "metallb installed in ${NS}; pool ${POOL_RANGE}"
