# images/ — DevEnvironment base images (CPU)

Container images that back the DevEnvironment `type`/image contract served by the `operator/`.
See `docs/design/devenv-images/decision.md` for the sourcing/composition decision and
`operator/internal/controller/devenvironment_controller.go` for the authoritative contract.

## Inventory

| Image | Base | Exposes | DevEnvironment type | Source |
|-------|------|---------|---------------------|--------|
| `cubestack/ssh-ubuntu-server` | `ubuntu:22.04` | ssh `22` | `ssh` | `ssh-ubuntu-server/Dockerfile` |
| `cubestack/jupyter` | `quay.io/jupyter/minimal-notebook:2026-09-07` | jupyter `8888`, ssh `22` | `jupyter` | `jupyter/Dockerfile` |

## The operator contract each image satisfies

- Runs as uid/gid **1000** (`runAsUser`/`runAsGroup`), single container, no command/args — the image
  ENTRYPOINT decides what runs (`common/entrypoint.sh`).
- Account literally named **`user`** (so `ssh://user@<gw>` maps), with passwd `$HOME` = workdir =
  **`/workspace`** (the workspace PVC).
- Readiness = TCP listening on the image's main port (8888 / 22).
- When the operator mounts the ssh Secret at `/etc/cubestack/ssh`
  (`ssh_host_ed25519_key`, `ssh_host_ed25519_key.pub`, `authorized_keys`, root-owned 0644):
  `common/ssh-setup.sh` copies it to a user-owned dir with tightened modes before sshd reads it.
- Jupyter honors the injected `JUPYTER_TOKEN` and a `CUBESTACK_BASE_URL` prefix (default `/`).

## Build & smoke

```bash
make -C images build     # both images, tagged cubestack/{ssh-ubuntu-server,jupyter}:smoke
make -C images smoke     # build + local Docker smoke (no cluster)
```

Per-image: `make -C images build-ssh` / `smoke-ssh`, `build-jupyter` / `smoke-jupyter`.

The smoke runs throwaway containers on `127.0.0.1` (ephemeral ports, fake ssh Secrets under
`mktemp -d`) and asserts:
- **ssh-ubuntu-server** — key-auth ssh login as `user`, uid 1000, `$HOME`/cwd `/workspace`, and the
  served host key equals the mounted Secret public key (host keys persist via the Secret, not the image).
- **jupyter** — token auth returns 200 and lab HTML on the `base_url` path; no token is rejected
  (401/403); the path without the `base_url` prefix is 404; the container runs as uid 1000.

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
  common/          shared runtime logic, COPYed into every image (single source)
    entrypoint.sh  mode selection + launch (CUBESTACK_IMAGE / CUBESTACK_TYPE)
    ssh-setup.sh   stage + tighten the /etc/cubestack/ssh Secret for non-root sshd
    run-jupyter.sh jupyter launcher (token / base_url / root_dir as CLI flags)
    sshd/          sshd_config.d drop-in for the non-root sshd
  ssh-ubuntu-server/Dockerfile
  jupyter/Dockerfile
  hack/smoke.sh    local acceptance smoke
```

**Build context is `images/`** for every Dockerfile — that is why ignore rules live in the single
`images/.dockerignore` (deny-by-default) and why shared files are `COPY common/...`.

## Trade-offs / notes

- **passwd `$HOME` = `/workspace`** (not `/home/user`): ssh sessions, `~`, and the notebook root land
  on the durable PVC. The real home `/home/user` only holds non-durable state (staged ssh keys,
  redirected XDG caches/runtime) so it never clobbers or durably pollutes the user's workspace.
- **Jupyter settings are CLI flags**, not a generated config file, because CLI options override the
  docker-stacks `/etc/jupyter/jupyter_server_config.py`, which hardcodes `root_dir=/home/jovyan`.
- The overlay **renames `jovyan` → `user`** and moves the primary group to gid 1000 (`/opt/conda` is
  re-`chown`ed `1000:1000`) so a `runAsGroup=1000` process can still write conda envs.
- A non-root sshd needs **`NET_BIND_SERVICE`** to bind `:22` (platform design Gap B); Docker grants it
  by default and the smoke adds `--cap-add` explicitly.
- An empty/root-owned PVC is storage-side (Gap A); the image cannot fix it. Readiness is TCP-only, so
  the image still reports ready; `CUBESTACK_WORKSPACE` exists as an escape hatch if a non-default
  `mountPath` is ever used (the operator does not inject it today).
