#!/usr/bin/env bash
# Mirrors everything the helm e2e pulls from an external registry into the
# platform registry, so `make helm-e2e-setup` / `helm-e2e-install` pull from
# harbor.isuanova.com instead of quay.io and Docker Hub. The runner reaches
# harbor on the internal network, and Docker Hub's anonymous pull limits are a
# CI failure this keeps clear of.
#
# Run by hand when a pinned version moves — CI only ever pulls. Pushing needs
# credentials the e2e itself does not: the project is public-read, so a node
# pulls anonymously, but this script needs `docker login harbor.isuanova.com`
# (crane reads the same config.json) or `crane auth login`.
#
# The versions are read out of the files that pin them rather than restated
# here: the same reason PERMISSION_INIT_IMAGE is read out of the controller's
# source, so a bump cannot leave this mirroring a tag nothing asks for.
#
# The upstream side goes through HTTPS_PROXY if one is set; add the registry to
# NO_PROXY so harbor is still reached directly. If your network does not need a
# proxy, set nothing.
set -euo pipefail

CRANE="${CRANE:-crane}"
command -v "${CRANE}" >/dev/null 2>&1 \
  || { echo "crane not found; install with 'go install github.com/google/go-containerregistry/cmd/crane@latest'" >&2; exit 1; }

cd "$(dirname "$0")/.." # operator/

METALLB_VERSION="$(sed -n 's/^METALLB_VERSION="\${METALLB_VERSION:-\(.*\)}"$/\1/p' hack/install-metallb.sh)"
ENVOY_GATEWAY_VERSION="$(sed -n 's/^ENVOY_GATEWAY_VERSION ?= \(.*\)$/\1/p' Makefile)"
ENVOY_GATEWAY_IMAGE="$(sed -n 's/^ENVOY_GATEWAY_IMAGE ?= \(.*\)$/\1/p' Makefile | sed -E 's/:[^:]*$//')"
ENVOY_PROXY_IMAGE="$(sed -n 's/^ENVOY_PROXY_IMAGE ?= \(.*\)$/\1/p' Makefile)"
ENVOY_GATEWAY_CHART="$(sed -n 's/^ENVOY_GATEWAY_CHART ?= oci:\/\/\(.*\)$/\1/p' Makefile)"
for v in "${METALLB_VERSION}" "${ENVOY_GATEWAY_VERSION}" "${ENVOY_GATEWAY_IMAGE}" \
         "${ENVOY_PROXY_IMAGE}" "${ENVOY_GATEWAY_CHART}"; do
  [ -n "${v}" ] || { echo "could not read a pinned version out of hack/install-metallb.sh or Makefile — one of their assignments changed shape" >&2; exit 1; }
done

# The MetalLB refs are the two repositories hack/install-metallb.sh rewrites the
# upstream manifest to, and they have to match it for the node to find them.
METALLB_REGISTRY="${METALLB_REGISTRY:-harbor.isuanova.com/suanova}"

# upstream -> platform. Each upstream tag is the one the e2e actually resolves:
# MetalLB's from the sha256-pinned native manifest, Envoy Gateway's from
# ENVOY_GATEWAY_VERSION, the proxy's from the compatibility matrix published
# beside that release (see the Makefile).
MIRRORS=(
  "quay.io/metallb/controller:${METALLB_VERSION}=${METALLB_REGISTRY}/metallb-controller:${METALLB_VERSION}"
  "quay.io/metallb/speaker:${METALLB_VERSION}=${METALLB_REGISTRY}/metallb-speaker:${METALLB_VERSION}"
  "docker.io/envoyproxy/gateway:${ENVOY_GATEWAY_VERSION}=${ENVOY_GATEWAY_IMAGE}:${ENVOY_GATEWAY_VERSION}"
  "docker.io/envoyproxy/envoy:${ENVOY_PROXY_IMAGE##*:}=${ENVOY_PROXY_IMAGE}"
  "docker.io/envoyproxy/gateway-helm:${ENVOY_GATEWAY_VERSION}=${ENVOY_GATEWAY_CHART}:${ENVOY_GATEWAY_VERSION}"
)

# Multi-arch throughout: a copied index costs a node only its own platform's
# manifest and layers, and keeps the e2e runnable on an arm64 laptop as well as
# on the amd64 runner.
ok=0
failed=0
for pair in "${MIRRORS[@]}"; do
  src="${pair%%=*}"
  dst="${pair#*=}"
  echo "=== ${src}"
  echo " -> ${dst}"
  if "${CRANE}" copy "${src}" "${dst}"; then ok=$((ok + 1)); else echo "!! FAILED: ${src}" >&2; failed=$((failed + 1)); fi
done

echo
echo "=== verifying the mirrored tags read back ==="
missing=0
for pair in "${MIRRORS[@]}"; do
  dst="${pair#*=}"
  if "${CRANE}" manifest "${dst}" >/dev/null 2>&1; then
    echo "  OK    ${dst}"
  else
    echo "  MISSING  ${dst}" >&2
    missing=$((missing + 1))
  fi
done

echo
echo "mirrored: ${ok}, failed: ${failed}, missing: ${missing}"
[ "${failed}" -eq 0 ] && [ "${missing}" -eq 0 ]
