# images/ — DevEnvironment base images

Container images that back the DevEnvironment `type`/image contract served by the `operator/`.
See `docs/design/devenv-images/decision.md` for the sourcing/composition decision and
`operator/internal/controller/devenvironment_controller.go` for the authoritative contract.

## Two image families

Images are **not** all conformed to a single layout. The decision doc splits them into two families:

- **Self-authored** images ship the platform default: account `ubuntu` (uid/gid **1000**), home and
  workdir **`/home/ubuntu`** (where the workspace PVC mounts).
- **Stock-derived** images keep their upstream native layout **unchanged**; the overlay enables only
  ssh. The platform reads each image's declared native facts and adapts to it — not the reverse.

| Image | Family | Base | Account (uid:gid) | Home / workspace | Exposes | ssh login |
|-------|--------|------|-------------------|------------------|---------|-----------|
| `harbor.isuanova.com/suanova/ssh-ubuntu22.04` | self-authored | `ubuntu:22.04` | `ubuntu` 1000:1000 | `/home/ubuntu` | ssh `2222` | `ubuntu` |
| `harbor.isuanova.com/suanova/jupyter-minimal` | stock-derived | `quay.io/jupyter/minimal-notebook:2026-09-07` | `jovyan` 1000:100 | `/home/jovyan` | jupyter `8888`, ssh `2222` | `jovyan` |

The `jupyter-minimal` overlay adds **only** `openssh-server` on top of the stock image: same account,
home, conda stack, launcher (`tini → start.sh → start-notebook.py`), and jupyter settings.

## Runtime facts the operator reads

Each image may declare its layout as labels under `image.cubestack.io/*`. The stock-derived overlay
declares all of them because its layout is upstream's; the self-authored image declares `user` and
`home` (its home is not the platform mount default `/workspace`):

- `image.cubestack.io/user` — the container account (the one its sshd serves).
- `image.cubestack.io/uid` / `gid` / `home` — run-as identity and where the workspace PVC mounts.

Honoring `home` for the PVC mountPath is operator work still tracked in #169; until it lands the
operator mounts the PVC at `/workspace` while these images live at their declared home.

## Runtime behavior common to both images

- Single container, **no command/args** — the image ENTRYPOINT decides what runs
  (`common/entrypoint.sh`): mode `ssh` runs sshd; mode `jupyter` starts sshd alongside jupyter when the
  host key is mounted, then hands off to the image CMD (the stock launch chain for jupyter).
- Readiness = TCP listening on the image's main port (8888 / 2222).
- sshd listens on the **unprivileged `2222`**, never `:22`: a non-root process cannot bind a privileged
  port without `CAP_NET_BIND_SERVICE`, so no image needs that capability granted. The platform's
  Service carries `port: 22` → `targetPort: 2222`, which is invisible to users — the ssh endpoint is
  published through the Gateway's TCP listener pool, not on 22 either way.
- ssh is enabled by the presence of the mounted host key file — images ship no host keys of their own,
  and there is no key staging (see the mount contract below).
- sshd runs as the container account (uid 1000): a non-root sshd can only serve the uid it runs as,
  so the only login account is the image's own (coherent with the `user` label).
- Jupyter is stock-native: the overlay adds no jupyter logic. `JUPYTER_TOKEN` (token) and
  `NOTEBOOK_ARGS` (extra flags, e.g. `--ServerApp.base_url=…`) are honored by the upstream launcher.

## ssh Secret mount contract

The operator mints one Secret per DevEnvironment holding the ssh material and mounts **two of its keys
as files** with `subPath` — nothing is copied, staged, or re-permissioned in the container:

| Secret key | Mounted at | Used by |
|------------|-----------|---------|
| `ssh_host_ed25519_key` | `/etc/ssh/ssh_host_ed25519_key` | sshd host identity; its presence gates ssh |
| `authorized_keys` | `$HOME/.ssh/authorized_keys2` | platform keys that may log in |

`$HOME` is the image's declared `home` (`/home/ubuntu`, `/home/jovyan`) and the workspace PVC mounts
there. sshd reads both files in place via the drop-in's `HostKey` and
`AuthorizedKeysFile %h/.ssh/authorized_keys2 %h/.ssh/authorized_keys` — the second path is the user's
own file, so `ssh-copy-id` and similar tools keep working alongside the platform keys. No `.pub` and
no host-key-per-algorithm files are needed: sshd derives the public half from the private key.

That second path is a **deliberate, bounded trade-off**: the account that can write it is the one sshd
serves (a non-root sshd can serve no other) and `AllowUsers` fixes the login account, so a key left
there yields a login as the uid that already owns the workspace — not a new privilege. `StrictModes yes`
would not close it either, since it accepts a key file owned by the account doing the reading; it is
`no` here because the workspace PVC mounted at `%h` may not carry modes sshd demands. Restricting logins
to operator-issued keys only would mean dropping this path (and `ssh-copy-id` with it) — a product
decision, not a tightening the drop-in can make on its own.

The operator must ensure the Secret always exists and carries `ssh_host_ed25519_key`; images have no
fallback identity and fail fast without it (`ssh` mode exits; `jupyter` simply starts without sshd).
A host key that is mounted but unusable is **not** that case: `jupyter` mode runs `sshd -t` before
backgrounding sshd and exits if it fails, rather than serving a ready notebook with a dead ssh endpoint.

### Requirements on the operator

The operator side of this contract is not implemented yet — it still mounts the Secret as a directory
at `/etc/cubestack/ssh`. The changes below are tracked in **#173**.

- **Restart the workload when the Secret changes.** Kubernetes does not propagate Secret updates to
  `subPath` mounts — the container keeps the bytes it started with
  ([Secret docs](https://kubernetes.io/docs/concepts/configuration/secret/)). Rotated keys are
  therefore inert until the pod is recreated.
- **Keep the files readable by the container uid.** The default Secret `defaultMode` `0644` is
  correct: the files are root-owned, and OpenSSH only enforces its private-key permission check on
  files owned by the uid reading them, so a uid-1000 sshd accepts a root-owned `0644` host key.
  Tightening `defaultMode` to `0600`/`0400` makes the key unreadable to that uid and sshd exits with
  *no hostkeys available*.
- **Mount the PVC at the image's declared `home`** (#169), so the platform keys land in the user's
  home. It must also provide `~/.ssh`: the image bakes the directory, but the PVC shadows it, and the
  subPath mount needs the parent to exist.
- **Publish the container's `2222`** as the Service's ssh port (`port: 22`, `targetPort: 2222`) and
  point the readiness probe at `2222` — the probe targets the container, not the Service.

## Build & smoke

```bash
make -C images build     # both images, tagged $(REGISTRY)/$(PROJECT)/<image>:$(TAG) (see Publish)
make -C images smoke     # build + local Docker smoke (no cluster)
```

Per-image: `make -C images build-ssh` / `smoke-ssh`, `build-jupyter` / `smoke-jupyter`.

The smoke runs throwaway containers on `127.0.0.1` (ephemeral ports, fake ssh Secrets under
`mktemp -d`) and asserts:
- **ssh-ubuntu22.04** — key-auth ssh login as `ubuntu`, uid 1000, group `ubuntu`, `$HOME`/cwd
  `/home/ubuntu`, and the served host key equals the mounted Secret public key (host keys persist via
  the Secret, not the image).
- Docker has no `subPath`, so the smoke reproduces the mount contract with per-file bind mounts; see
  the fidelity note under Trade-offs.
- **jupyter-minimal** — one container running both services at native identity (uid 1000, gid 100):
  token auth returns 200 and lab HTML on the `NOTEBOOK_ARGS` `base_url` path; no token is rejected;
  the path without the prefix is 404; plus key-auth ssh login as `jovyan` (`$HOME=/home/jovyan`) with
  the served host key equal to the mounted Secret public key.

### Publish

Each image has **one name**: `make build` tags it at the reference it is published under,
`$(REGISTRY)/$(PROJECT)/<image>:$(TAG)`, defaulting to `harbor.isuanova.com/suanova/...` and the short
commit SHA (decision doc §5). `push` adds `:latest` to that same reference rather than introducing a
second name:

```bash
docker login harbor.isuanova.com           # once — the Makefile never authenticates
make -C images push                        # build, then publish both images
make -C images push-ssh      TAG=20260910
make -C images push-jupyter  TAG=2026-09-07
```

`push` depends on the build, then **adds the moving `:latest` to the built image** and pushes both
references. `:latest` is the one tag `make build` never produces, so finding it locally means it came
from a publish. The smoke is the acceptance gate but not a prerequisite of `push` — run
`make -C images smoke` first.

`TAG` defaults to the short commit SHA (`git rev-parse --short HEAD`), so a bare `make build` yields a
traceable, non-floating reference. It names the **last commit, not the working tree** — commit before
publishing, or the tag will not describe the built content — and it is resolved per make invocation, so
a commit landing between `make build` and `make push` makes the two disagree; let `make push` do both, or
pass an explicit TAG. §5 gives the release schemes per image — `ssh-ubuntu22.04:<date>` and
`jupyter-minimal:<base-date>` — the two families version on different axes, hence the per-target form.

A deployment tracking `:latest` follows the newest publish while a pinned one keeps its SHA/release
tag; publishing an older commit therefore moves `:latest` backwards, which is expected for a moving
tag but worth knowing before rebuilding a previous release. `REGISTRY` / `PROJECT` relocate the whole
destination.

### Overrides / mirror builds (CN or offline)

```bash
APT_MIRROR=https://mirrors.tuna.tsinghua.edu.cn/ubuntu-ports \
PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple \
make -C images build
```

`IMG_SSH` / `IMG_JUPYTER` override the output tags; `CONTAINER_TOOL` overrides `docker` (e.g. `podman`).

## Layout

```
images/
  common/                  runtime config shared by both images (single source)
    entrypoint.sh          mode selection + optional sshd, then hand-off to the image CMD
    sshd/10-nonroot.conf   sshd_config.d drop-in; AllowUsers is @SSH_USER@
  ssh-ubuntu-server/Dockerfile
  jupyter/Dockerfile
  hack/smoke.sh            local acceptance smoke
```

The sshd_config drop-in is **shared**: it holds the mount contract's paths (`HostKey`,
`AuthorizedKeysFile`, the latter `%h`-relative so it follows each image's home) and the login account as
an `@SSH_USER@` placeholder, which each Dockerfile substitutes from its `ARG SSH_USER` (`ubuntu` /
`jovyan`). A missed substitution is not a parse error — `AllowUsers` is a valid keyword and the pattern
simply matches no account — so it fails *closed*: sshd starts but denies every login, and the smoke
fails on its login assertion. **Build context is `images/`** for every Dockerfile
— that is why ignore rules live in the single `images/.dockerignore` (deny-by-default) and why shared
files are `COPY common/...`.

## Trade-offs / notes

- The `jupyter-minimal` overlay is intentionally thin: identical stock layout, sshd only. Tokens and the
  URL prefix flow through stock env (`JUPYTER_TOKEN`, `NOTEBOOK_ARGS`); `base_url` is not yet injected by
  the operator (Gap C / #169). Its labels declare the native `gid 100` / `/home/jovyan`.
- sshd binds the unprivileged `2222`, so the images need **no capability at all** — design Gap B
  (`NET_BIND_SERVICE`) is closed by port choice rather than by granting a privilege. Two things follow:
  the operator must set the Service's `targetPort` to `2222` (#173), and a local smoke cannot validate
  the port privilege anyway — Docker writes `ip_unprivileged_port_start=0` into every container netns,
  so a container there can bind `:22` with no capability, while a pod's own netns defaults to `1024`.
- The jupyter container runs at its native gid 100; until #169 lands the operator defaults
  `runAsGroup` to 1000, so in-cluster correctness ships with #169. Self-authored images stay on the
  platform uid/gid defaults (1000:1000).
- The workspace PVC mounts at the image's declared home (`/home/ubuntu` self-authored; `/home/jovyan`
  jupyter), where the notebook root already lives by default. An empty/root-owned PVC is
  storage-side (Gap A); the image cannot fix it, and readiness is TCP-only.
- **Smoke fidelity for the host-key mode.** Kubernetes projects Secret files root-owned `0644`, which a
  uid-1000 sshd accepts (the owner check applies only to a file owned by the uid doing the reading).
  Docker does not reproduce that faithfully: Docker Desktop reports a bind mount as root-owned `0600`
  yet lets any container uid read it, while a rootful Linux daemon keeps the host uid and modes — there
  the mounted `0600` key really is unreadable to uid 1000 and sshd exits with *no hostkeys available*.
  `hack/smoke.sh` therefore mounts the private key `0600` **and** asserts up front that the container
  uid can read it (`check_mount_readable`), so that environment mismatch is reported as itself rather
  than as a 30-second ssh timeout. The cluster path (`0644`, root-owned) was verified separately by
  hand; only the ownership the engine presents and enforces differs, not the image.
