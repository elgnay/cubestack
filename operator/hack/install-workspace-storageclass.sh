#!/usr/bin/env bash
# Creates the workspace StorageClass the DevEnvironment controller hardcodes:
# `cephfs-ephemeral`, backed by the local-path provisioner kind already ships.
#
# A StorageClass object on its own is NOT enough here, and the way it fails is
# worth stating because it is silent. The controller's workspace claim always
# requests ReadWriteMany (design §7.2, so the volume can follow the pod across
# nodes), and local-path-provisioner refuses that access mode outright:
#
#   unless the class is in shared-filesystem mode,
#   provisionFor() rejects any access mode other than ReadWriteOnce/ReadWriteOncePod
#   with ProvisioningFinished — terminal, so the claim sits Pending forever with
#   no retry, and the environment never leaves WaitingForPod.
#
# kind v0.32.0 ships local-path-provisioner v0.0.34 (images/local-path-provisioner
# /Makefile at that tag), so this is the behaviour in play.
#
# Shared-filesystem mode is the provisioner's own escape hatch: given
# `sharedFileSystemPath` instead of `nodePathMap`, it skips both the access-mode
# and the node check, provisions with the claim's own access modes, and drops the
# node affinity (the comment at that branch: "If the same filesystem is mounted
# across all nodes, we don't need affinity"). The volume source stays hostPath.
# On a single-node cluster that is exactly right.
#
# The mode is a property of the PROVISIONER's config, not of the StorageClass —
# `sharedFileSystemPath` is a key of the provisioner's config.json and must not
# be confused with StorageClass.spec.parameters, where it does nothing.
#
# The `standard` entry below is not optional either. Once `storageClassConfigs`
# is non-empty the provisioner stops falling back to the top-level nodePathMap
# and looks every request up in that map — an unknown class fails with
# "BUG: Got request for unexpected storage class". So adding only
# `cephfs-ephemeral` would break kind's default class for everything else in the
# cluster.
#
# Idempotent: re-running patches the same config and reapplies the same class.
set -euo pipefail

KIND_CLUSTER="${KIND_CLUSTER_HELM:-cubestack-helm-e2e}"
CTX="kind-${KIND_CLUSTER}"
KUBECTL="${KUBECTL:-kubectl}"
STORAGE_CLASS="${WORKSPACE_STORAGE_CLASS:-cephfs-ephemeral}"
PROVISIONER_NS="local-path-storage"

kubectl_e2e() { "${KUBECTL}" --context "${CTX}" "$@"; }

if [ "$(kubectl_e2e get deployment local-path-provisioner -n "${PROVISIONER_NS}" \
  -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || true)" != "True" ]; then
  echo "local-path-provisioner is not Available in ${PROVISIONER_NS} on ${CTX}; is this a kind cluster?" >&2
  exit 1
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/cubestack-storageclass.XXXXXX")"
trap 'rm -rf "${tmp}"' EXIT

# The provisioner's config, with the shipped nodePathMap kept verbatim for the
# `standard` class and a shared-filesystem entry added alongside it.
cat >"${tmp}/config.json" <<'JSON'
{
  "nodePathMap": [
    {
      "node": "DEFAULT_PATH_FOR_NON_LISTED_NODES",
      "paths": ["/var/local-path-provisioner"]
    }
  ],
  "storageClassConfigs": {
    "standard": {
      "nodePathMap": [
        {
          "node": "DEFAULT_PATH_FOR_NON_LISTED_NODES",
          "paths": ["/var/local-path-provisioner"]
        }
      ]
    },
    "__STORAGE_CLASS__": {
      "sharedFileSystemPath": "/var/local-path-provisioner/__STORAGE_CLASS__"
    }
  }
}
JSON
sed -i.bak "s/__STORAGE_CLASS__/${STORAGE_CLASS}/g" "${tmp}/config.json" && rm -f "${tmp}/config.json.bak"

# A merge patch of just the one data key: the ConfigMap also carries the setup,
# teardown and helperPod.yaml scripts, which kind put there and this has no
# business rewriting.
python3 - "${tmp}/config.json" "${tmp}/patch.json" <<'PY'
import json, sys

config, out = sys.argv[1], sys.argv[2]
with open(config) as fh:
    body = fh.read()
with open(out, "w") as fh:
    json.dump({"data": {"config.json": body}}, fh)
PY

echo "configuring the local-path provisioner: ${STORAGE_CLASS} as a shared filesystem"
kubectl_e2e patch configmap local-path-config -n "${PROVISIONER_NS}" \
  --type merge --patch-file "${tmp}/patch.json"

# The provisioner re-reads its config on a 30s ticker (ConfigFileCheckInterval).
# The workspace claim is created moments after the chart install, well inside
# that window, and a claim provisioned against the old config fails terminally —
# so wait for the new config rather than racing it.
echo "restarting the provisioner to pick the config up now rather than within 30s"
kubectl_e2e rollout restart deployment/local-path-provisioner -n "${PROVISIONER_NS}"
kubectl_e2e rollout status deployment/local-path-provisioner -n "${PROVISIONER_NS}" --timeout=120s

cat <<YAML | kubectl_e2e apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ${STORAGE_CLASS}
provisioner: rancher.io/local-path
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
YAML

echo "StorageClass ${STORAGE_CLASS} ready"
