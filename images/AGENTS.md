# AGENTS.md

This file applies ONLY to the `images/` directory.

For platform-wide principles, see `../AGENTS.md`.

## Scope

Container images that back the DevEnvironment `type`/image contract served by the
`operator/` (see `operator/internal/controller/devenvironment_controller.go` and
`docs/design/devenv-images/decision.md`). Each image:

- runs as uid/gid **1000**, account literally named `user`, `$HOME`/workdir on `/workspace`
- never sets command/args (the controller provides none) — the image ENTRYPOINT decides what runs
- satisfies readiness = TCP listening on the image's main port (jupyter `8888`, ssh `22`)
- reads the operator-mounted ssh Secret from `/etc/cubestack/ssh`
  (`ssh_host_ed25519_key`, `.pub`, `authorized_keys`) when present

## Rules

- **Build context is `images/`** for every Dockerfile. Build with
  `make -C images build` (or `docker build -f <img>/Dockerfile ... images/`). The shared logic under
  `common/` is `COPY`ed into each image from this context.
- **Keep ignore rules in `images/.dockerignore`** (deny-by-default). Docker only honors the
  context-root ignore file; a per-subdir `.dockerignore` is inert and misleading.
- **Keep runtime logic in `common/`**, single source: entrypoint mode selection, ssh-key staging,
  sshd config, and the Jupyter launcher. Dockerfiles only assemble packages + the platform account.
- **Never commit secrets, private keys, or tokens.** Smoke-generated keys live only under `hack/` at
  runtime (`mktemp -d`) and are cleaned up.
- **English** code comments, commit messages, and docs.
- Reproducible base tags only; no floating tags. Mirror hooks are explicit build args
  (`APT_MIRROR`, `PIP_INDEX_URL`); nothing is baked that assumes a mirror.

## Build & smoke

```bash
make -C images build    # builds both images, tags cubestack/{ssh-ubuntu-server,jupyter}:smoke
make -C images smoke    # local Docker smoke (ssh key-auth login; jupyter token/base_url)
```

There is no cluster and no CI wiring yet; `make -C images smoke` is the acceptance gate. When this
workspace gains CI, it must add a build + smoke job like the other sub-projects.
