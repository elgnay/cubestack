# CubeStack Operator

The CubeStack operator manages the `ai.cubestack.io` resources: `ModelVersion`,
`InferenceRuntimeProfile`, `InferenceService` and `DevEnvironment`. It ships as a
Helm chart (see [helm/cubestack-controller-manager-chart](helm/cubestack-controller-manager-chart/README.md));
the `make helm-e2e-*` targets below are the quickest way to install it locally
and verify it end-to-end on a dedicated kind cluster.

## Prerequisites

- [docker](https://docs.docker.com/engine/install/) (daemon running)
- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation)
- [helm](https://helm.sh/docs/intro/install/) v3
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [go](https://go.dev/dl/) (for `make` targets that download tools / build images)

## Install and verify on kind (make)

```bash
# From the repository root (or `make -C operator ...` inside operator/)

make helm-e2e-install    # create the kind cluster (if absent), build and load the
                         # manager + echo images, helm-install the operator, wait for rollout
make helm-e2e-crd-check  # assert CRDs / VAPs / RBAC are installed and schema validation rejects
                         # invalid resources
make helm-e2e-verify     # apply the dummy assets (test/e2e/assets) and assert the full
                         # gpu-less happy path: InferenceService reaches Ready=True with all
                         # conditions True, rendered overrides appear in the pod logs, the
                         # HostPath volume is mounted and the endpoint has ready backends
```

Everything runs on a dedicated kind cluster named `cubestack-helm-e2e`
(independent from the scaffold `test-e2e` cluster `cubestack-test-e2e`).
The targets are idempotent and safe to re-run; `helm-e2e-verify` re-applies the
dummy assets and re-asserts them.

The manager image defaults to `harbor.isuanova.com/suanova/cubestack-controller-manager:latest`
and is deployed with `imagePullPolicy: IfNotPresent` (baked into the chart
template), so the image loaded into kind wins over the registry even for a
`:latest` tag. Override the image with `make helm-e2e-install IMG=<registry>/<repo>:<tag>`.

### What the install provisions

The kind cluster setup (`helm-e2e-setup`, a dependency of `helm-e2e-install`)
installs the platform prerequisites the operator needs to run and reconcile
before the chart is helm-installed:

- the Gateway API CRDs (the DevEnvironment controller watches Gateway,
  HTTPRoute, TCPRoute, UDPRoute and ListenerSet; the manager registers those
  watches at startup for the kinds the cluster serves), and
- the upstream [LeaderWorkerSet](https://github.com/kubernetes-sigs/lws)
  controller at the version pinned in `go.mod` (LWS workloads do not
  materialize pods without it). The controller install applies the pinned lws
  module's `config/default`, which provides its own
  `leaderworkerset.x-k8s.io` / `disaggregatedset.x-k8s.io` CRDs.

The chart installs the `ai.cubestack.io` CRDs (ModelVersion,
InferenceRuntimeProfile, InferenceService, DevEnvironment — synced from
`config/crd/bases` at build time) together with the VAPs, RBAC and Deployment
for the controller manager; it does not ship the lws CRDs.

In a non-kind cluster you must provide both prerequisites before installing the
chart, and the Envoy Gateway v1.9.1 CRDs as well: the `ClientTrafficPolicy` that
ships with the Gateway — from the chart, and from the kustomize base's
`config/gateway/` — is one of that controller's resources.

## Requirements on the namespaces that host DevEnvironments

The operator neither creates nor labels namespaces: an environment lands in whatever
namespace its CR was created in. Pod Security Admission has no per-container exemption, so
the namespace's enforce level decides whether the workload can exist there at all. Measured
against a 1.36 API server, the strictest level each spec still runs under is:

| spec | strictest level it runs under | because |
|------|-------------------------------|---------|
| plain | `restricted` | the environment is a non-root container with no capabilities and a runtime-default seccomp profile |
| + `storage` | `baseline` | the workspace claim is chowned by a root init container holding `CHOWN`, `FOWNER` and `FSETID`, which `restricted` rejects three ways over |
| + `runtime.securityContext.runAsUser: 0` | `baseline` | `restricted` requires a non-root user |
| + `network.rdmaEnabled` | `privileged` | registering a memory region adds `IPC_LOCK`, and a RoCE fabric adds `hostNetwork`; `baseline` refuses both |

`privileged` in that last row names the Pod Security Standard level, not
`securityContext.privileged` on the container: the operator never sets that, and an RDMA
environment's container is an ordinary non-root one. What forces the level is the
namespace's policy, which has no per-container exemption — `baseline` disallows a declared
`IPC_LOCK` whatever uid the container runs as, and disallows `hostNetwork`. The device
itself is handed over by the device plugin through the device cgroup, not by either.

`IPC_LOCK` is there because registering an RDMA memory region has to pin pages beyond the
default `RLIMIT_MEMLOCK`. A node configured to lift that limit for containers would not need
it, but a node's runtime configuration is not something the platform can assume.

So a plain environment runs in a namespace labelled
`pod-security.kubernetes.io/enforce=restricted`, and anything with storage needs that label
relaxed to `baseline`. The DevEnvironment e2e labels its namespace `baseline`
(`hack/verify-devenv.sh`), which suits the common case.

A namespace enforcing `restricted` cannot host an environment that asks for storage: the
StatefulSet is created but its pod is refused at admission, so the environment never reaches
`Running` and the reason is only in the StatefulSet's events, not in the DevEnvironment's
status. A namespace with no enforce label inherits the API server's cluster-wide default,
which the operator cannot read — label the namespace explicitly rather than rely on it.

## Uninstall and cleanup

```bash
make helm-e2e-uninstall  # helm uninstall cubestack -n cubestack-system
make helm-e2e-cleanup    # delete the kind cluster (cubestack-helm-e2e)
```

`helm uninstall` removes the operator (Deployment, RBAC, VAPs, metrics Service)
but intentionally keeps the CRDs — deleting a CRD would orphan its resources.
Use `kubectl delete crd <name>` explicitly if you want them gone.

## Installing via Helm (production-like)

See [helm/cubestack-controller-manager-chart/README.md](helm/cubestack-controller-manager-chart/README.md) for
chart values, CRD lifecycle semantics and the prerequisite install commands.
