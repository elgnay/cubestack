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
# Idempotent: exits early when the controller is already Available.
set -euo pipefail

METALLB_VERSION="${METALLB_VERSION:-v0.16.0}"
# The native manifest is fetched, not vendored: it is 2.4k lines of CRDs and
# RBAC that only this test needs. Pinned by checksum for the same reason the CI
# workflow pins the kind binary — a tag alone is a mutable reference.
METALLB_MANIFEST_URL="${METALLB_MANIFEST_URL:-https://raw.githubusercontent.com/metallb/metallb/${METALLB_VERSION}/config/manifests/metallb-native.yaml}"
METALLB_MANIFEST_SHA256="${METALLB_MANIFEST_SHA256:-b0b9be2802f10aa32d45308b4457d06cde0c70544712c8d0cf5511657ffd2b69}"

NS="metallb-system"
KIND_NETWORK="${KIND_NETWORK:-kind}"

KIND_CLUSTER="${KIND_CLUSTER_HELM:-cubestack-helm-e2e}"
CTX="kind-${KIND_CLUSTER}"
KUBECTL="${KUBECTL:-kubectl}"
DOCKER="${DOCKER:-docker}"

cd "$(dirname "$0")/.." # operator/

if [ "$("${KUBECTL}" --context "${CTX}" get deployment -n "${NS}" controller \
  -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || true)" = "True" ]; then
  echo "metallb controller already Available in ${NS} on ${CTX} — skipping"
  exit 0
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
OUT="$(mktemp)"
trap 'rm -f "${OUT}"' EXIT
echo "fetching MetalLB ${METALLB_VERSION} native manifest"
curl -fsSL "${METALLB_MANIFEST_URL}" -o "${OUT}"
# sha256sum on the Linux CI runner, shasum on the macOS dev machines.
if command -v sha256sum >/dev/null 2>&1; then
  DIGEST="$(sha256sum "${OUT}" | awk '{print $1}')"
else
  DIGEST="$(shasum -a 256 "${OUT}" | awk '{print $1}')"
fi
[ "${DIGEST}" = "${METALLB_MANIFEST_SHA256}" ] \
  || { echo "MetalLB manifest checksum mismatch — ${METALLB_VERSION} was re-tagged, or the download is wrong" >&2; exit 1; }

# --server-side: the CRDs in this bundle (bgppeers, ipaddresspools, ...) carry
# schemas too large for client-side apply's last-applied-configuration annotation.
echo "applying MetalLB to ${CTX}"
"${KUBECTL}" --context "${CTX}" apply --server-side -f "${OUT}"

echo "waiting for metallb controller and speakers..."
"${KUBECTL}" --context "${CTX}" rollout status deployment/controller -n "${NS}" --timeout=300s
"${KUBECTL}" --context "${CTX}" rollout status daemonset/speaker -n "${NS}" --timeout=300s

# The webhook has to answer before an IPAddressPool can be admitted; the rollout
# above covers the pods, not the Service behind the webhook.
"${KUBECTL}" --context "${CTX}" wait --for=condition=Ready pod -n "${NS}" \
  -l app=metallb,component=controller --timeout=120s

# --- pool + advertisement ---
# Both objects, always. An IPAddressPool with no L2Advertisement is MetalLB's
# classic silent failure: the address is allocated and assigned to the Service's
# status, so `kubectl get svc` looks correct, but nothing ever advertises it and
# every connection to it times out.
"${KUBECTL}" --context "${CTX}" apply -f - <<YAML
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

echo "metallb installed in ${NS}; pool ${POOL_RANGE}"
