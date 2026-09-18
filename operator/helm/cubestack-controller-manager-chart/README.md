# cubestack-controller-manager-chart

The CubeStack operator chart: installs the `ai.cubestack.io` CRDs
(ModelVersion, InferenceRuntimeProfile, InferenceService, DevEnvironment),
their L1 validating admission policies (VAPs) and bindings, and the controller
manager. InferenceService workloads are LeaderWorkerSets, but the chart
installs only the `ai.cubestack.io` CRDs — the `leaderworkerset.x-k8s.io` /
`disaggregatedset.x-k8s.io` CRDs come with the upstream LeaderWorkerSet
controller prerequisite below.

## Prerequisites

- Kubernetes **>= 1.30** (ValidatingAdmissionPolicy support).
- **gateway-api CRDs** installed in the cluster. The manager watches
  Gateway/HTTPRoute/TCPRoute/UDPRoute at startup and fails to boot without these
  CRDs.
  The chart does not install them. Example for a kind cluster:

  ```bash
  GW_VER="$(awk '$1=="sigs.k8s.io/gateway-api" {print $2}' operator/go.mod)"
  kubectl apply -f "$(go env GOMODCACHE)/sigs.k8s.io/gateway-api@${GW_VER}/config/crd/standard"
  ```

- **Envoy Gateway >= v1.9.1**, which provides the `EnvoyProxy` CRD *and* the
  controller that programs the Gateway. The chart ships the platform Gateway
  together with the `EnvoyProxy` that tells Envoy Gateway to run the proxy
  behind a `NodePort` Service (see `gateway.envoyProxy.serviceType`), so the CRDs
  must be in place before the chart can be installed: without them
  `helm install` fails with `no matches for kind "EnvoyProxy"`. v1.9.1 is the
  version ListenerSet reconciliation is verified on — an older one accepts the
  Gateway but never programs the ListenerSets, so no L4 listener ever appears.
  The chart's Gateway names the `eg` GatewayClass that this install creates
  (change it with `gateway.className` if you installed a differently named
  class):

  ```bash
  helm install eg oci://docker.io/envoyproxy/gateway-helm --version v1.9.1 \
    -n envoy-gateway-system --create-namespace
  ```

- The **upstream LeaderWorkerSet controller** running in the cluster, which
  provides the `leaderworkerset.x-k8s.io` / `disaggregatedset.x-k8s.io` CRDs
  as well as the controller: the manifest below is the pinned lws module's
  `config/default` and includes its CRDs. Without the controller,
  LeaderWorkerSet workloads never materialize pods, so InferenceServices can
  never reach `Ready=True`. Install it from the pinned lws version in
  `operator/go.mod`, e.g. via the operator's `make -C operator helm-e2e-setup`
  (provisions a kind cluster) or the upstream lws release manifests:

  ```bash
  LWS_VER="$(awk '$1=="sigs.k8s.io/lws" {print $2}' operator/go.mod)"
  kubectl apply --server-side -f \
    "$(go env GOMODCACHE)/sigs.k8s.io/lws@${LWS_VER}/config/default"
  ```

  No cert-manager is required: LWS v0.10.0 manages its own webhook
  certificates.

## Install

From a fresh checkout, run `make -C operator helm-crds-sync` first: the chart
directory's `crds/` is not committed — it is populated from
`operator/config/crd/bases` at package/install time (`helm-crds-sync` is a
prereq of `helm-package` and `helm-e2e-install`, so packaged charts and the
`helm-e2e-*` flow already contain the CRDs).

```bash
helm install cubestack ./helm/cubestack-controller-manager-chart -n cubestack-system --create-namespace
```

### Install from the OCI registry

The chart is published to the team's Harbor registry as an OCI artifact. The
prerequisites above still apply — gateway-api CRDs and the upstream
LeaderWorkerSet controller must already be installed (the chart installs the
`ai.cubestack.io` CRDs only):

```bash
helm install cubestack oci://harbor.isuanova.com/suanova/cubestack-controller-manager-chart \
  --version 0.1.0 -n cubestack-system --create-namespace
```

### Image overrides

The default image is `harbor.isuanova.com/suanova/cubestack-controller-manager:latest`
(the team's registry). Override repository and tag with `--set`:

```bash
helm install cubestack ./helm/cubestack-controller-manager-chart -n cubestack-system \
  --create-namespace \
  --set image.repository=myregistry.example.com/cubestack \
  --set image.tag=v1.2.3
```

The Deployment template bakes `imagePullPolicy: IfNotPresent` into the
manager container (fixed in the template — it is not a values knob). Local
kind testing therefore works with the `:latest` default: the built image is
kind-loaded into the cluster, and `IfNotPresent` makes the loaded image win
over the registry instead of the kubelet's `Always` default for `latest`
tags triggering a remote pull.

### Upgrading a cluster that already has the Gateway

Before the chart shipped it, every cluster created `cubestack-gateway` — and
usually the `EnvoyProxy` it references — by hand. Helm refuses to take over
resources it does not own (the upgrade stops with `invalid ownership metadata`),
so adopt them once:

```bash
kubectl -n cubestack-system label --overwrite \
  gateway/cubestack-gateway envoyproxy/cubestack-gateway-proxy \
  app.kubernetes.io/managed-by=Helm
kubectl -n cubestack-system annotate --overwrite \
  gateway/cubestack-gateway envoyproxy/cubestack-gateway-proxy \
  meta.helm.sh/release-name=cubestack meta.helm.sh/release-namespace=cubestack-system
```

(Helm 4 does the same thing with `helm upgrade --take-ownership`.) Adoption
makes the objects release-owned: later hand edits to them are reverted by the
next upgrade, and `helm uninstall` deletes them. Diff before upgrading, since
the chart's version of these objects replaces whatever was there — including
any `externalTrafficPolicy`, logging or listener the hand-made ones carried:

```bash
helm template cubestack ./helm/cubestack-controller-manager-chart -n cubestack-system \
  | kubectl diff -f -
```

### Gateway configuration (route publishing)

`spec.route.publish: true` on an InferenceService publishes its HTTPRoute to
the platform Gateway. **The chart creates that Gateway**: `cubestack-gateway`,
with one HTTP listener on :80 that routes from any namespace may attach to and
the `allowedListeners` opt-in the per-environment ListenerSets need (see the L4
section) — plus the `EnvoyProxy` it references, which is where the proxy
Service's type comes from. Both objects land in the **release namespace**, and
the manager is told that same namespace, so the release can be installed
anywhere: there is no namespace to keep in step by hand.

`gateway.name` is the one value two readers have to agree on, so the chart
feeds it to both: it names the Gateway object, the `EnvoyProxy` beside it
(`<name>-proxy`, which the Gateway references) and the `--gateway-name` the
manager receives. The DevEnvironment controller attaches its ListenerSets to
that same Gateway, so renaming moves the objects and both lookups together.

The values below do three different jobs, which is worth knowing when one of
them seems to have no effect:

- **Manager flags** — `name`, `domain`, `dataplaneNamespace`. An empty value
  omits its flag entirely, keeping the manager's own default. The namespace is
  the exception among them: it always renders, because this chart always
  creates the Gateway it points at.
- **A Gateway API field** — `className`, written into the Gateway object.
- **Envoy Gateway's own configuration** — `envoyProxy.*`, written into the
  `EnvoyProxy` CR that the Gateway references. The manager never reads these,
  and they do nothing on a cluster served by a different Gateway
  implementation.

| Key | Manager flag | Default | Notes |
|---|---|---|---|
| `gateway.name` | `--gateway-name` | `cubestack-gateway` | Names the Gateway and the `EnvoyProxy` (`<name>-proxy`) the chart creates, **and** the flag — so the object the operator publishes through is always the one the chart made. Empty = flag omitted; publishing is disabled (`RouteReady=False`, `GatewayNotConfigured`) while the objects still take the conventional name. |
| `gateway.domain` | `--gateway-domain` | `""` | Empty = flag omitted. **Set this to enable publishing** — the public hostname of a published service is `<modelName>.<domain>`. |
| `gateway.dataplaneNamespace` | `--gateway-dataplane-namespace` | `envoy-gateway-system` | Names the namespace the Gateway's dataplane pods run in. **Not** a publishing switch. Two things read it: environment pods admit ingress from that Gateway, and the controller looks up the dataplane Service there to learn which port each listener is reachable on. Empty = flag omitted: environments stay default-deny inbound, and endpoint addresses fall back to assuming the listener port is the reachable one — true of a LoadBalancer or ClusterIP dataplane, not of a NodePort one. |
| `gateway.className` | *(no flag)* | `eg` | The GatewayClass the Gateway asks to be served by. Not fed to the manager — only the cluster's Gateway controller reads it — so it must name a class that controller has established. |
| `gateway.envoyProxy.serviceType` | *(no flag)* | `NodePort` | How Envoy Gateway types the Service running the proxy fleet for this Gateway. **Envoy Gateway's own setting**, written into the `EnvoyProxy` CR it defines — not a Gateway API field, not passed to the manager, and meaningless on a cluster served by another Gateway implementation. With `NodePort` each listener is also exposed on a nodePort, and the controller reads that Service back to publish the port a listener is actually reachable on (`status.endpoints[].address`, keeping the pool port in `listenerPort`) — the setting that makes a cluster with no load-balancer controller work. `LoadBalancer` (Envoy Gateway's own default) instead needs a load balancer to give the Gateway an address, and without one nothing is published; `ClusterIP` is never reachable from outside the cluster. |

`dataplaneNamespace` and `envoyProxy.serviceType` are the keys here the
**DevEnvironment** controller is affected by: the namespace is where its
NetworkPolicy allowance points and where it finds the dataplane Service, and
the Service type is what decides whether the port published for a listener is
the listener's own port or the nodePort it was renumbered onto. The dataplane
namespace is not the Gateway's own namespace — Envoy Gateway runs the proxy
pods in a namespace of its own, separate from the one holding the `Gateway`
object. `name` reaches that controller too, as the Gateway its ListenerSets
attach to; `className` is read only by the cluster, and `domain` configures the
InferenceService publishing path only.

The defaults are the platform convention — `cubestack-gateway`, served by the
`eg` class — so a standard install only needs the domain:

```bash
helm install cubestack ./helm/cubestack-controller-manager-chart -n cubestack-system \
  --create-namespace --set gateway.domain=example.com
```

Pass `--set` again on `helm upgrade` (or use a `--values` file) — the flags
are rendered by the chart, so an upgrade never resets them. When upgrading a
release that was created by an older chart, drop `--reuse-values` (or pass
the `gateway.*` keys explicitly): reused values are the release's stored
values and do not pick up these new chart defaults.

Renaming the Gateway — `--set gateway.name=...`, or moving the release to
another namespace — makes Helm delete the objects under the old name and create
them under the new one. The manager picks the new name up in the same upgrade,
but the old Gateway is gone before that: every environment loses its published
address until the new one is admitted by its GatewayClass and Envoy Gateway
programs it.

The kustomize deployment (`make deploy`) carries the same `--gateway-name` /
`--gateway-namespace` args in `operator/config/manager/manager.yaml`;
`--gateway-domain` and `--gateway-dataplane-namespace` are left to your overlay
there, so a kustomize install keeps environment pods default-deny inbound. It
creates the same Gateway and EnvoyProxy — `config/gateway/` is part of the
kustomize base — with the dataplane Service type spelled in
`operator/config/gateway/envoyproxy.yaml` instead of a value. There the names
and the GatewayClass are literals under `operator/config/`, not values.

### L4 port pool (DevEnvironment exposure)

Each DevEnvironment that exposes `ssh` or a `spec.ports[]` entry of type `tcp`
or `udp` takes one port from a cluster-wide pool. The manager learns the range
through two flags, fed by the `l4PortRange.*` values — these always render:

| Key | Manager flag | Default |
|---|---|---|
| `l4PortRange.start` | `--l4-port-range-start` | `20000` |
| `l4PortRange.end` | `--l4-port-range-end` | `20999` |

A port is allocated to the lowest free number in the range and stays with the
environment across restarts. `tcp` and `udp` draw on the same numbering — one
number is held by one protocol, so a udp port never shares a number with a tcp
one. Each allocated port becomes a listener the
environment's own `ListenerSet` declares on the platform Gateway. **Nothing has
to pre-publish the range**: Envoy Gateway adds the port of every accepted
listener to the proxy Service it manages for the Gateway, and where that
Service is a `NodePort` it also assigns the nodePort. The controller reads the
dataplane Service back (see `gateway.dataplaneNamespace`) and publishes the port
it is actually reachable on in `status.endpoints[].address`, keeping the pool
port in `listenerPort`. Widen the range as the number of environments grows —
the pool, not the Service, is what runs out.

Two things have to be in place for a listener to take effect, once per cluster:

- The Gateway must admit the ListenerSets. **The chart's Gateway does**:
  `spec.allowedListeners` is set to `from: All`, because the API default
  (`from: None`) denies every ListenerSet, which comes back `Accepted=False` /
  `NotAllowed` — surfaced on the environment as `RouteReady=False` /
  `ListenerNotAccepted`, which names the reason rather than hanging. Who may
  publish is settled by RBAC, not by that selector — anyone able to create a
  ListenerSet in their own namespace can contribute a listener — so a Gateway
  you create yourself with a namespace selector instead is an equally valid
  setup.
- The `ListenerSet` CRD (`gateway.networking.k8s.io/v1`) must be installed, and
  the Envoy Gateway version must reconcile ListenerSets (v1.9.1 or newer — see
  the prerequisites). The controller probes for each Gateway API kind and only
  watches the ones the cluster serves, so a cluster without it still runs and
  still publishes HTTP; its L4 environments report `RouteReady=False` /
  `GatewayAPINotInstalled`. An Envoy Gateway that is too old leaves the object
  accepted by the API server but unprogrammed, so no listener appears and the
  environment never reaches `RouteReady=True`.

```bash
helm install cubestack ./helm/cubestack-controller-manager-chart -n cubestack-system \
  --create-namespace --set l4PortRange.start=20000 --set l4PortRange.end=29999
```

The kustomize deployment (`make deploy`) carries the same two args in
`operator/config/manager/manager.yaml`.

## Uninstall

```bash
helm uninstall cubestack -n cubestack-system
```

Helm uninstall removes the release's objects (Deployment, RBAC, VAPs, the
Gateway and its EnvoyProxy, ...) but **not the CRDs** — CRDs are cluster-scoped
and intentionally left in place so custom resources survive a reinstall. Delete
the chart's `ai.cubestack.io` CRDs explicitly if you want them gone (all custom
resources must be removed first):

```bash
kubectl delete crd modelversions.ai.cubestack.io inferenceruntimeprofiles.ai.cubestack.io \
  inferenceservices.ai.cubestack.io devenvironments.ai.cubestack.io
```

Uninstalling therefore takes the platform Gateway down with it: every
environment loses its published address until a Gateway exists again, and the
per-environment ListenerSets wait for one to attach to.

The `leaderworkerset.x-k8s.io` / `disaggregatedset.x-k8s.io` CRDs belong to the
LeaderWorkerSet controller prerequisite (see above) rather than the chart;
delete them only when removing that prerequisite too:

```bash
kubectl delete crd leaderworkersets.leaderworkerset.x-k8s.io \
  disaggregatedsets.disaggregatedset.x-k8s.io \
  disaggregatedsetrolescalers.disaggregatedset.x-k8s.io
```

## Publishing to Harbor (maintainers)

CI (`.github/workflows/ci-operator-chart.yml`) pushes the chart to
`oci://harbor.isuanova.com/suanova` automatically on `main` when chart-relevant
paths change. To publish manually, from `operator/`:

```bash
make helm-package   # regenerates chart resources from config/, then packages
helm registry login harbor.isuanova.com -u <CI_BOT_NAME> -p <CI_BOT_PASSWORD>
helm push helm/cubestack-controller-manager-chart/cubestack-controller-manager-chart-0.1.0.tgz oci://harbor.isuanova.com/suanova
```

The OCI version tag comes from the Chart.yaml `version` — CI derives the
pushed tgz name from it, so a bump needs no workflow edit. Bump chart versions
in one commit: update the Chart.yaml `version` **and** the version literals in
this README (the OCI install `--version` above and the manual push path in
this section). Re-pushing the same version overwrites the existing tag.

## Generated content — do not hand-edit

`templates/` and `vap.yaml` are generated from the kustomize sources in
`operator/config/` by `operator/hack/update-helm-resources.sh`:

- VAPs: `operator/config/vap/*.yaml` (concatenated with `---` separators)
- RBAC / Deployment / Service / Role: `kustomize build operator/config/default`
  with namespace and image rewritten to Helm values
- Gateway / EnvoyProxy: the same kustomize build (`operator/config/gateway/`),
  with the object names rewritten to `gateway.name`, the Gateway's
  `gatewayClassName` to `gateway.className`, and the EnvoyProxy's
  `envoyService.type` to `gateway.envoyProxy.serviceType`

The `crds/` directory is NOT stored in the repo — it is populated at package
or install time by copying `operator/config/crd/bases` into the chart
(`make -C operator helm-crds-sync`, a prereq of `helm-package` and
`helm-e2e-install`), so it always matches `config/` by construction. The chart
installs only the `ai.cubestack.io` CRDs; the `leaderworkerset.x-k8s.io` /
`disaggregatedset.x-k8s.io` CRDs come with the LeaderWorkerSet controller
prerequisite — see above.

To change chart content, edit the sources under `operator/config/` and run
`make -C operator helm-resources-update`, then commit the regenerated chart.
CI (`make -C operator helm-resources-check`) fails when the committed chart is
out of sync with the sources.
