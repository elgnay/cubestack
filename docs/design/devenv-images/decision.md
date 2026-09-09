# DevEnvironment base images: sourcing / composition / delivery decision

## 1. Context and goal

Platform DevEnvironments are driven by the operator's `DevEnvironment` CRD, which lets a user pick a
container image (`spec.image`) combined with semantic axes such as `type` (jupyter / ssh / vscode),
GPU vendor, storage, and an SSH toggle. This module delivers that **base-image set**: images that
satisfy the image contract already fixed in the operator, are published to a container image
registry, and can be bundled for offline installation.

In scope: the four image products `base-cuda` (NVIDIA), `base-maca` (Metax), `jupyter`,
`ssh·ubuntu-server`, plus offline packaging / container image registry publishing. **Out of scope**: code-server,
`DevEnvironmentTemplate`, GPU driver / RDMA bring-up (Installer).

Whether images are pulled from public registries or self-built, how they are composed, and where the
Dockerfiles live are all undecided — this document settles them.

---

## 2. Operator image-contract cross-check

The images' only mandatory spec is what the operator has already merged
(`operator/internal/controller/*`, `operator/api/v1alpha1/*`, `operator/config/samples`). Each
assumption is checked below. "🚩" marks gaps the **controller / platform side must close before the
images are final** — they cannot be solved inside the images themselves.

| # | Operator assumption | Code evidence | Requirement on the image | Status |
|---|---|---|---|---|
| 1 | Brand marker | `devenvironment_controller.go::brandMatch`: image name (lowercased) must **contain** `base-cuda` / `base-maca` | Image registry path must include the `base-cuda` / `base-maca` segment | ✅ consistent with the brand gate |
| 2 | Type → port | `::mainContainerPort`: jupyter 8888 / ssh 22 / vscode 8080; readiness probe = TCP on the main port | The server must listen on the type's port and accept TCP | ✅ |
| 3 | Non-root default | `::desiredSecurityContext`: `runAsUser=runAsGroup=1000`, `runAsNonRoot=true` (only lifted when the user explicitly sets `0`) | Image must run as the **resolved uid/gid** — `image.cubestack.io/{uid,gid}` labels, platform default 1000/1000 — non-root, incl. writing `$HOME` (stock images ship uid 1000) | ✅ (but see #6, #9) |
| 4 | `$HOME` / working dir = `mountPath` | comment in `devenvironment_types.go` + sample: PVC mounted at `mountPath` (default `/workspace`); the image's home/workdir should land on it | Image `USER`/`HOME`/workdir must land on the workspace PVC mount. The mount path is the image's declared home (label; default `/workspace`) — docker-stacks images declare `/home/jovyan` and keep it; `mountPath` overrides it | ⚠️ see "Gap A" + §2 contract |
| 5 | SSH key mount | `assets.go:63`: Secret data keys `ssh_host_ed25519_key`(+`.pub`), `authorized_keys`; `devenvironment_controller.go:709` mounts read-only at `/etc/cubestack/ssh`, default mode 0644; host key is **never rotated** (persistence provided by the Secret) | Entrypoint must **copy host key / authorized_keys to a user-writable dir and tighten to 0600** before starting sshd (sshd refuses a world-readable 0644 private key, and a root-owned RO mount cannot be chmod'ed in place) | ✅ handled by entrypoint script |
| 6 | sshd listening on :22 as uid 1000 | ssh Service 22→22 in the controller; `desiredSecurityContext` grants no capabilities | Binding a port <1024 as non-root needs `NET_BIND_SERVICE`; a Restricted PSA would drop it | 🚩 **Gap B** |
| 7 | `JUPYTER_TOKEN` | `jupyterTokenEnv="JUPYTER_TOKEN"` (:144), injected only for the jupyter type, from `<env>-auth` Secret `data[token]` | The jupyter server must authenticate with this env value | ✅ (jupyter-server reads `JUPYTER_TOKEN` natively, see §3.B4) |
| 8 | `base_url` = `/dev/<ns>/<env>/` | HTTPRoute forwards the prefix **unchanged** (no URLRewrite filter); the controller never injects the prefix into the container — yet the route design states "container serves under that base_url" | Jupyter must serve under that prefix via `ServerApp.base_url`, but nothing hands the prefix to the container | 🚩 **Gap C** |
| 9 | Runtime-mode inference | Single-container pod; `type` and `image` are independent axes; the controller injects no `type`/mode env | The same image must decide by itself whether to run jupyter or sshd (e.g. inferred from whether `JUPYTER_TOKEN` is injected / ssh keys are mounted) | 🚩 **convention to be defined** (implicit inference is workable; explicit is recommended) |
| 10 | Multi-service in one container | `sshExposed` adds a 22 Service and mounts keys for jupyter/vscode types, sharing the main container | jupyter type + `ssh.enabled` ⇒ the same process group must run jupyter *and* sshd | ✅ handled by entrypoint script |
| 11 | GPU extended resource | `::gpuResource`: nvidia `nvidia.com/gpu` / metax `metax-tech.com/gpu` | The image is device-agnostic; `nvidia-smi`/`mx-smi` come from driver injection | ✅ see §3 |
| 12 | SSH login user | `sshEndpointUser="user"` (:137) is the platform default → endpoint `ssh://user@<gw>`; first-party images **declare** their login account via the `image.cubestack.io/ssh-user` label — self-authored images ship `user`, the docker-stacks-derived jupyter ships `jovyan`; operator resolution precedence spec → label → default is issue #169 | The image must contain the login account it declares (default `user`, uid 1000); for jupyter that is the stock `jovyan` account | ✅ |

### Image-declared runtime metadata (`image.cubestack.io/*`)

Images may declare, via labels, the runtime facts the platform otherwise assumes.
Labels are read **only for builtin-registry images** (issue #169) and fall back to
platform defaults when absent. One precedence applies to every knob:
**spec field > image label > platform default** — labels carry per-image facts so a
curated/relayed image "just works" with nothing set; spec fields remain the
per-environment override. Of the four knobs, only `ssh-user` needs a new spec
field; uid/gid/home already have homes in the DevEnvironment CRD.

The platform defaults encode the **self-authored layout** (account `user`, uid/gid 1000,
`/workspace`) — the contract our own platform-layer images ship by construction. Images we do **not**
author are not forced into that layout: **stock-derived images** (the docker-stacks jupyter overlay)
keep the upstream account/home (`jovyan` uid 1000 gid 100, `/home/jovyan`) exactly as the stock image
ships them, and declare that via the labels. The labels are therefore *per-image declarations of the
image's native layout*, not conformance requirements.

| Knob | Image label | Platform default | Spec override (CRD) |
|---|---|---|---|
| SSH login account | `image.cubestack.io/ssh-user` | `user` | `spec.ssh.userName` (new, #169) |
| Container uid | `image.cubestack.io/uid` | `1000` | `spec.runtime.securityContext.runAsUser` |
| Container gid | `image.cubestack.io/gid` | `1000` | `spec.runtime.securityContext.runAsGroup` |
| Durable home / mount | `image.cubestack.io/home` | `/workspace` | `spec.storage.mountPath` |

For inspectable (builtin) images the controller validates tuple coherence: the
declared `ssh-user`'s passwd uid must equal the resolved uid — a non-root sshd can
only serve an account whose uid equals the process uid.

The workspace PVC mounts at the image's **declared home** — `spec.storage.mountPath` when the user
sets it, otherwise the `image.cubestack.io/home` label, otherwise the `/workspace` platform default —
so `/home/jovyan` for the jupyter overlay, `/workspace` for self-authored images. The `home` label is
what lets a stock-derived image keep its native `$HOME` while still getting a persistent workspace on
the PVC. ⚠️ The CRD currently defaults `mountPath=/workspace`, which would override the label for
omitted specs; making an unset `mountPath` defer to the label is part of #169.

### Gap A — the home mount writable by uid 1000

The pod sets no `fsGroup`; the workspace PVC uses `cephfs-ephemeral` (RWX, `assets.go:85`). The
container runs as uid 1000 with `$HOME` on the mount, so the **RWX StorageClass's mount behavior must
make the mount path writable by 1000** (cephfs owner/mode) — regardless of whether that path is
`/workspace` or `/home/jovyan`. The image cannot chown itself (non-root). This belongs to
workspace-storage work for verification; the image only commits to pointing `$HOME` at the mount point.

### Gap B — capability for non-root sshd binding :22

The container security context grants no capabilities. To let sshd listen on 22 as uid 1000, pick one
(and follow through):

1. The controller appends `capabilities.add: [NET_BIND_SERVICE]` to the main container's
   securityContext (when SSH is exposed), and the namespace must not enforce a Restricted PSA that
   drops that cap;
2. Stay unprivileged: sshd listens on a high port (e.g. 2222), and Gateway/Service DNATs back to 22 —
   but the ssh Service is currently 22→22 (targetPort 22), so this needs a matching controller change.

**Recommendation: option 1** (smallest change, clearest contract), scheduled as follow-up controller
work; the image entrypoint is developed assuming "uid 1000 can bind 22", and if the PS policy
tightens, Gap B is revisited together.

### Gap C — injecting `base_url` into Jupyter

The HTTPRoute (`::desiredHTTPRoute`) forwards the `/dev/<ns>/<env>/` prefix unchanged to the 8888
backend (no rewrite), so jupyter must serve that prefix with `ServerApp.base_url` set, or relative
asset URLs and 404s break. Today the controller injects only `JUPYTER_TOKEN`; the prefix has no
source. Two options:

1. The controller injects a prefix env for jupyter containers (e.g. `CUBESTACK_BASE_URL=/dev/<ns>/<env>/`),
   and the image entrypoint appends `--ServerApp.base_url=$CUBESTACK_BASE_URL`;
2. The Gateway adds a URLRewrite that strips the prefix, and jupyter serves at `/` as usual.

**Recommendation: option 1** (consistent with the route design's "container serves under that
base_url" semantics, no per-environment gateway rewriting); the Gateway must still pass websockets
through. This is a controller change and is listed as "to close".

### Gap D — explicit runtime mode (optional)

The §9 implicit inference (a `JUPYTER_TOKEN` present → start jupyter; ssh keys mounted with no token
→ start sshd) suffices for the current jupyter/ssh types but is fragile and leaves no hook for vscode.
Recommended: the controller also injects `CUBESTACK_TYPE=<jupyter|ssh|vscode>`, and the image
entrypoint reads it first, falling back to implicit inference when absent. **Optional** — can be done
together with Gaps B/C.

---

## 3. Upstream base survey findings (2026-09-08)

### 3.A NVIDIA CUDA (`nvidia/cuda` / `nvcr.io/nvidia/cuda`)

- Exact patch tags exist and are multi-arch (amd64/arm64): e.g. `11.8.0-{base,runtime,devel}-ubuntu22.04`,
  `12.4.1-…`, `12.6.3-…`. **No `latest` tag** — full patch tags must be pinned.
- Variants: `base` = cudart; `runtime` = base + math libraries + NCCL; `devel` = runtime + headers +
  nvcc. Sizes (ubuntu22.04, compressed layers): runtime ~1.2–1.5 GB, devel ~3.2–3.7 GB.
- `nvidia-smi` is **not baked in**: NVIDIA Container Toolkit injects it from the host driver at
  container start. So the "can run `nvidia-smi`" acceptance is really a check of **driver + toolkit
  injection**, not of image contents.
- Driver requirement: the host driver must be ≥ the image's minimum CUDA version (e.g. CUDA 12.4 ≥
  550.54.14; drivers are backward compatible).

### 3.B Jupyter docker-stacks

- **Official images are only published to Quay since 2023-10** (`quay.io/jupyter/*`); Docker Hub
  `jupyter/*` is stale. Chain: `docker-stacks-foundation → base-notebook → minimal-notebook →
  scipy-notebook → {pytorch, tensorflow, …}-notebook`. CUDA tags exist (`pytorch-notebook:cuda12-` /
  `cuda13-`).
- Default user `jovyan` (uid 1000, group users gid 100), `$HOME=/home/jovyan`.
- **Arbitrary UID is not cleanly supported**: `start.sh` remaps user/UID/GID (`NB_USER/UID/GID`) only
  when started as root; run as a non-root uid and it just execs, leaving `/home/jovyan` and
  `/opt/conda` unwritable for uids other than 1000. In K8s either use `runAsUser: 1000` or mount a
  writable volume at `/home/jovyan`.
- Token: docker-stacks' `start-notebook.py` does not read `JUPYTER_TOKEN`, but **jupyter-server
  itself** does (`IdentityProvider.token`) → setting the env suffices for auth; more explicit is
  `--IdentityProvider.token` / `NOTEBOOK_ARGS`.
- `base_url`: pass `--ServerApp.base_url=/prefix` through; under JupyterHub integration it is derived
  from `JUPYTERHUB_SERVICE_PREFIX`.

### 3.C Metax MACA

- **No anonymous public registry**: the official `cr.metax-tech.com` requires commercial authorization
  or an offline package (`metax-gpu-k8s-package.<ver>.tar.gz`). Image tags look like
  `cr.metax-tech.com/library/maca-c500:<ver>-<os>-<arch>` or the newer `<product>-maca:…`;
  `cloud/{gpu-device,container-runtime,metax-operator}` hold the driver/runtime components.
- `mx-smi` ships with / is injected from the **Metax driver userspace**, not bundled in a bare MACA
  base.
- Architecture mirrors NVIDIA: `gpu-device` = Device Plugin (advertising `metax-tech.com/gpu`),
  `container-runtime` = a private runtime that injects MACA on demand (app images need not bake the
  MACA stack).
- **The CUDA ecosystem is not drop-in**: software must be rebuilt against MACA via `cu-bridge`, and
  PyTorch must be a customized `torch…+metax` build (conflicting cupy/flashinfer wheels removed). →
  affects the software selection inside base-maca (see §6).

### 3.D Offline delivery notes

- `docker pull` (on an amd64 host) + `docker save` produces a **single-arch** tarball; the
  multi-arch index is lost. Preserving multi-arch offline needs `skopeo copy --all` or per-node
  `--platform` pulls.
- Pin patch tags and record digests (floating minor tags drift).

---

## 4. Composition model (recommended)

Two families of images share a common "platform layer" so that service capability (entrypoint, ssh
key handling) is written once. They differ in the base and the layout they carry:

```
  common scripts (images/common): entrypoint (mode select) + ssh key / authorized_keys copy & tighten
                                   │ shared by both families
        ┌──────────────────────────┴───────────────────────────┐
        │                                                      │
 self-authored platform layer                     docker-stacks thin overlay
 (ubuntu22.04) — ships the self-authored           (jupyter CPU) on quay.io/jupyter/
 layout: user 'user' uid/gid 1000,                 minimal-notebook — keeps stock layout
 $HOME on the workspace mount                      native: jovyan uid 1000 gid 100,
 (default /workspace); python + jupyterlab         $HOME=/home/jovyan (declared via labels);
 (for jupyter type) + openssh-server               + openssh-server / ssh key handling ONLY
   │                                               so ssh.enabled works; otherwise == stock
   ├── CPU: ssh-ubuntu-server                      (jupyter type on a GPU-vendor image is the
   └── GPU: base-cuda (+nvidia runtime)            self-authored jupyterlab, not this overlay)
           base-maca (+Metax runtime)
```

Key points:

- **What is shared is the entrypoint and the ssh key / `authorized_keys` handling** (`images/common`),
  reused by both families — GPU and CPU images, and the docker-stacks overlay, do not duplicate it.
- **Account / `HOME` follow the image family** (see §2 label contract): the **self-authored** platform
  layer (`ssh-ubuntu-server`, `base-cuda`, `base-maca`) ships the platform default `user` uid/gid 1000
  with `HOME` on the workspace mount (`/workspace`) — there is no upstream UX to preserve, so
  uniformity is free. The **stock-derived jupyter** image is **not** conformed: it keeps `jovyan`
  (uid 1000, gid 100) and `/home/jovyan` exactly as docker-stacks ships them, so users familiar with
  the stock image see stock behavior; it adds sshd only for `ssh.enabled`. Both declare their layout
  via `image.cubestack.io/*` labels (issue #169).
- **`base-cuda`/`base-maca` reuse the same self-authored platform layer**: a jupyter-type environment
  can pick a GPU-vendor image, while an ssh-type environment on a GPU-vendor image runs only sshd (mode
  chosen by the entrypoint).

---

## 5. Registry organization and tag scheme (recommended)

Current anchors: `config/samples/ai_v1alpha1_devenvironment.yaml` and controller unit tests already
pin `harbor.local/ai-images/base-cuda:11.8-pytorch2.2` / `harbor.local/ai-images/base-maca:1.0`
(the offline in-cluster registry host form).

- **Project name**: `ai-images` (reuse the existing sample/tests — zero churn). On the platform
  container image registry the host is `harbor.isuanova.com` (CI already publishes operator/portal
  under a `suanova` project on the same domain); offline installs surface as `harbor.local`. → same
  registry org, two hosts; the offline host swap is handled by packaging/rewriting.
- **Tags**: content-locked and reproducible. Examples:
  - `base-cuda:<cuda>-py<python>[-torch<torch>]`, e.g. `12.4.1-py3.11-torch2.4.0`; or reuse today's
    semantics `11.8-py3.10-torch2.2.2`. **Recommend promoting the sample's `11.8` to the full patch
    `11.8.0` and eventually recording digests** in the packaging manifest.
  - `base-maca:<maca-version>-py<python>[-torch<ver>+metax]`
  - `jupyter:<python>-lab<labver>`, `ssh-ubuntu-server:<os>-<date>`
- The brand marker only requires the path to contain `base-cuda`/`base-maca` (§2 #1), orthogonal to
  the project/tag choices above.

---

## 6. Image inventory and composition

### 6.1 `base-cuda` (NVIDIA)

- **Base**: pull `nvidia/cuda:<cuda>-runtime-ubuntu22.04` directly (**runtime**, not devel — see below).
- **Overlay**: the platform layer (§4): python + jupyter/sshd entrypoint + `user`(1000)/`HOME=/workspace`.
- **Bake PyTorch or not**: to decide. Today's sample implies yes (`…-pytorch2.2`); baking requires
  pulling CUDA torch wheels at build time and a larger image. **Recommendation**: bake a default,
  commonly used `torch+cu` stack into the GPU image for "out of the box" use, version pinned in the tag.
- **runtime vs devel**: a runtime base covers "run torch"; if target users must `nvcc`-compile inside
  the container, devel is needed (+~2.3 GB) or a separate `-devel` variant. **Recommendation: runtime
  by default**, compile needs handled as build-time wheels, with a devel variant if required — pending
  product confirmation.
- **Acceptance mapping**: `nvidia-smi` visible = driver+toolkit injection check (not image content);
  non-root 1000, `$HOME` on `/workspace`, 8888/22 ready — smoke-driven by the controller pod spec.

### 6.2 `base-maca` (Metax)

- **Gate**: Metax has no anonymous public registry. Commercial access to the offline package/account
  (`cr.metax-tech.com`) is required first, plus confirmation that `container-runtime` injection works
  for non-root uid-1000 containers. **Until this gate passes, the Metax image cannot be self-built.**
- **Base/overlay**: stack the platform layer on the Metax MACA runtime (from the commercial package's
  images or a base unpacked from it); if `container-runtime` injection is available, the image need
  not bake the MACA stack (mirroring NVIDIA's injection model).
- **Software stack**: PyTorch must be the `torch+metax` build; stock CUDA wheels are not directly
  usable (cu-bridge recompile).
- **Acceptance mapping**: `mx-smi` visible, `metax-tech.com/gpu` request succeeds, 1000/`/workspace`/
  ports ready.
- **Decision**: direction = self-built (no public base to choose); blocker = obtaining the MACA package
  and confirming the injection model — an explicit first task; do not assume it can be pulled directly.

### 6.3 `jupyter` and `ssh·ubuntu-server`

- **`jupyter` (CPU)**:
  - Option A: **thin-overlay `quay.io/jupyter/minimal-notebook`**, **stock-native**: keep the image
    exactly as docker-stacks ships it — `jovyan` (uid 1000, gid 100), `$HOME=/home/jovyan`, stock
    launcher/entrypoint — and add **only** openssh-server + the shared ssh key handling so that
    `ssh.enabled` works. The workspace PVC mounts at `/home/jovyan` (its native home, declared via the
    `image.cubestack.io/home` label). Users familiar with the stock image get stock behavior; `base_url`
    / token need no image change (see below).
  - Option B: **self-build** (ubuntu22.04 + conda/pip + jupyterlab + custom entrypoint) — full control
    of uid/HOME/size, but you own the dependency manifest.
  - **Decision**: CPU `jupyter` via Option A **thin-overlay, stock + ssh only** (recommended in §4).
    No renaming to `user`, no gid-1000 move, no `/workspace` redirect, no XDG re-homing: everything
    except the added sshd stays identical to the stock image, because there is no platform reason to
    change it — the label contract (§2) lets the platform adapt to the image rather than the image to
    the platform. Option B (self-build) stays a fallback if a product later needs the ecosystem
    trimmed; the GPU variant remains base-cuda's self-authored platform layer.
- **`ssh·ubuntu-server` (CPU)**: self-build (ubuntu22.04 + openssh-server + entrypoint that
  copies/tightens keys) — simplest, smallest attack surface; self-authored (no upstream stock UX to
  preserve) so it ships the platform default account `user` uid/gid 1000, `$HOME` on `/workspace`.
- **Acceptance mapping**: jupyter 8888 + token (`JUPYTER_TOKEN` env, no-token rejected) + base_url path
  reachable (depends on Gap C closing); ssh 22 + authorized_keys login + host keys persistent across
  restarts (provided by the Secret).

---

## 7. Where the Dockerfiles live (recommended)

"This monorepo has no images workspace yet" is one of the items this decision must settle.

- **Recommendation**: add a top-level **`images/`** workspace to this monorepo, alongside `operator/`
  and `web/`. It holds the shared platform layer, per-image Dockerfiles, a `Makefile`/`hack` build
  script, and an offline export script; CI (reusing the existing GitHub Actions image publishing
  pattern) pushes to a container image registry per §5 tags.
- Rationale: the operator contract and the e2e suite evolve in the same repo; layered builds (§4) need
  same-repo references to the shared base; consistent with the existing operator/web two-stack layout.
- Alternative: a separate Installer/image repo — not recommended (splits contract evolution from builds).
- **Timing**: after this decision is reviewed, create the `images/` skeleton together with the build
  work. This decision phase creates **no directory**.

---

## 8. Platform-side changes to close (not image-side)

| Item | Owner | Recommendation | Blocks |
|---|---|---|---|
| A workspace writability check (uid 1000 writing the home mount: `/workspace` self-authored, `/home/jovyan` jupyter) | workspace storage (cephfs-ephemeral) | Confirm the RWX SC makes the mount path writable by 1000 | smoke of all images |
| B non-root sshd binding :22 | controller + PSA policy | `capabilities.add:[NET_BIND_SERVICE]` on the main container (when SSH is exposed) | base image ssh acceptance |
| C injecting jupyter `base_url` | controller | Inject `CUBESTACK_BASE_URL=/dev/<ns>/<env>/`; image sets `ServerApp.base_url` from it | jupyter image acceptance, e2e |
| D mode env (optional) | controller | Inject `CUBESTACK_TYPE`, entrypoint reads it first | vscode hook |

---

## 9. Decision summary and items to confirm

| Item | Conclusion | Status |
|---|---|---|
| Two-family model: self-authored platform layer (`user` 1000, `HOME` on `/workspace`) + docker-stacks thin overlay (`jovyan`/`/home/jovyan`, stock-native, ssh added) — shared entrypoint + label-declared layout | §4 | ✅ recommended here |
| GPU image = self-authored platform + runtime layer (layered reuse) | §4 | ✅ recommended here |
| Project `ai-images`, host `harbor.isuanova.com` (online) / `harbor.local` (offline) | §5 | ✅ recommended (follows today) |
| Dockerfiles live in monorepo `images/` | §7 | ✅ recommended (lands with the build work) |
| base-cuda base = runtime (not devel) + whether to bake torch | §6.1 | ⚠️ **pending product** (CUDA version 11.8 vs 12.x, torch version, devel variant?) |
| base-maca self-build + commercial gate | §6.2 | ⚠️ **to confirm**: Metax package channel / injection model / target software versions |
| jupyter CPU = stock-native thin-overlay on Quay minimal-notebook + ssh only | §6.3 | ✅ decided (shipped in images work) |
| ssh·ubuntu-server self-build, platform-default `user`/`/workspace` | §6.3 | ✅ recommended here |
| Gaps A/B/C/D closure | §8 | ⚠️ **to schedule into follow-up controller work** (B/C precede image acceptance) |

After review: promote the "✅ recommended" items to "decided", backfill the "to confirm" items, and
post a summary of this document so downstream image-build work can proceed.
