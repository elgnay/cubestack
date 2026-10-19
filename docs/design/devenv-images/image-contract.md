# DevEnvironment image contract

What a container image must do to back a `DevEnvironment`, and what the operator promises it in
return. The authoritative behaviour is the controller
(`operator/internal/controller/devenvironment_controller.go`); this document is the contract the
first-party images under `images/` satisfy and any bring-your-own image has to satisfy to be
configured the same way.

## Mode

The runtime mode is a property of the **environment**, not of the image.

| `spec.type` | Controller injects | Image must |
|---|---|---|
| `jupyter` | `CUBSTACK_TYPE=jupyter` | serve JupyterLab on `8888`, honouring `JUPYTER_TOKEN` and `NOTEBOOK_ARGS`; also run sshd on `2222` **iff** the host key is mounted |
| `ssh` | `CUBSTACK_TYPE=ssh` | run sshd on `2222` only: no notebook process, nothing listening on `8888`; exit non-zero when the host key is missing |
| `vscode` | nothing | be a stock code-server image; no first-party image serves it |

- `CUBSTACK_TYPE` is **controller-owned**: a value the spec declares is dropped, because it would
  contradict the ports, probes and routes the controller derives from `spec.type`. It participates
  in the StatefulSet template hash, so upgrading the operator rolls every jupyter/ssh environment
  once to deliver it.
- An image run **outside** the platform falls back to the `CUBESTACK_IMAGE` baked into it
  (`jupyter` for every first-party image). An unknown value must exit non-zero rather than guess.
- Readiness is **TCP listening on the type's main port** — `8888` for jupyter, `2222` for ssh. An
  `ssh` environment is never probed on `8888`, which is why its image must not start a notebook at
  all, even one that ships with the base.

## Account and home

Every first-party image ships the same layout, and it is the platform default:

| | Value |
|---|---|
| account | `ubuntu` |
| uid / gid | `1000` / `1000` |
| `$HOME` / workdir | `/home/ubuntu` |

- The ssh login account is `spec.runtime.user`, else the platform default; an environment names
  `ubuntu` and needs no `runAsGroup` override.
- The workspace claim mounts at `spec.storage.mountPath`, else the home the runtime identity
  implies — `/home/ubuntu` here. The controller states that path as `HOME`, so a launcher must serve
  the home it is given rather than one the image bakes. An ssh session starts in the image's
  workdir, which is `/home/ubuntu` on all three; a non-default `mountPath` moves `HOME` but is not
  guaranteed to move that working directory.
- The account must be able to **write** `$HOME`; the controller repairs the claim's ownership with
  an init container, but the image must not make the home unwritable itself (and must not `useradd`
  over an account its base already ships).
- **Root** (`spec.runtime.securityContext.runAsUser: 0`) is supported: the entrypoint admits `root`
  only when the container itself runs as uid 0, and the controller adds `--allow-root` to
  `NOTEBOOK_ARGS`. A non-root sshd cannot setuid to root and must not admit it.

## Mounts

Everything ssh-related is **mounted, never baked, copied, staged or re-permissioned** in the image.
Images ship no host keys and no authorized keys.

| Path | Kind | Source |
|---|---|---|
| `/etc/ssh/ssh_host_ed25519_key` | `subPath` file | the controller's `<env>-ssh-host-key` Secret; its presence gates ssh |
| `/run/ssh/authorized_keys` | whole-Secret directory mount, `items`-renamed | `spec.ssh.authorizedKeysSecret`, else the controller's `<env>-ssh-client-key` |

- Both paths are absolute and outside `$HOME`, so they do not depend on the account and survive the
  workspace claim being mounted over the home.
- The host key must be **OpenSSH format**; sshd rejects PKCS#8 Ed25519.
- The operator renders the Secret mode from the reading uid — `0644` for a non-root sshd (which is
  not the file's owner and so is never checked, but also cannot read `0600`), `0600` for a root sshd
  (which is the owner and rejects a world-readable key).
- The authorized-keys mount is an ordinary Secret mount, so a rotation reaches a running container;
  the host key is a `subPath` and is frozen at container start.
- A **root sshd needs `/run/sshd`** (privilege separation directory). Every first-party Dockerfile
  creates it; an image that does not cannot serve a root environment.

## Ports

| Mode | Container port | Service |
|---|---|---|
| jupyter | `8888` | HTTP through the Gateway |
| ssh | `2222` | `port: 22` → `targetPort: 2222` |

sshd binds `2222` rather than `:22`, so no capability is required. Images `EXPOSE` both ports; the
controller probes only the one `spec.type` selects.

## Platform

| Image | Base | Platforms |
|---|---|---|
| `minimal` | `ubuntu:22.04` | `linux/amd64`, `linux/arm64` (multi-arch index) |
| `cube-base` | NVIDIA NGC PyTorch (`nvcr.io/nvidia/pytorch:<release>-py3`, mirrored) | `linux/amd64` |
| `maca-base` | Metax MACA (`public-library/maca-pytorch:…`, mirrored) | `linux/amd64` |

The vendor bases publish one architecture only, so their images do too. A registry must receive the
platform the nodes run; verify with `docker buildx imagetools inspect --raw <ref>` rather than
assuming the build host's architecture.

## Notes for bring-your-own images

The controller cannot see inside an image, so a spec's `runtime.user`, `securityContext` and
`storage.mountPath` are **statements about the image's native layout**, not requirements the image
can be validated against. An image that does not read `CUBSTACK_TYPE` still runs with `spec.type`
driving its port and probe — so an image intended for `ssh` must not start a notebook, and one
intended for `jupyter` must serve the type's port. Bring-your-own docker-stacks images keep their
own account (`jovyan`, gid `100`, `/home/jovyan`) and are selected with those values in the spec.
