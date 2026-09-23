# Cubestack 'ssh-maca-pytorch' DevEnvironment image — Metax MACA GPU base, ssh only.
#
# The ssh-only half of the Metax story, and the GPU counterpart of ssh-ubuntu22.04: the same
# platform layer as jupyter-maca-pytorch on the same vendor base, without JupyterLab. The split
# is not cosmetic, because the mode is baked into the image (CUBESTACK_IMAGE) — the operator
# injects no type, as entrypoint.sh notes — so no single image can be both. A `type: ssh`
# DevEnvironment pointed at the jupyter image would run JupyterLab beside sshd; pointed here,
# it runs sshd alone.
#
# The vendor base ships no launcher, no sshd, no non-root account and no ENTRYPOINT at all
# (config.User is unset, so it would run as root, and its CMD is a bare /bin/bash that exits
# immediately). So this adds the platform layer the self-authored CPU image defines — account
# ubuntu 1000:1000 with $HOME=/home/ubuntu, openssh-server, the shared entrypoint — on top of
# the vendor's own python/torch/MACA stack, which is inherited untouched. The account is not a
# preference: the shared sshd drop-in is a non-root configuration (PermitRootLogin no, Port 2222,
# UsePAM no), and a non-root sshd can only serve the uid it runs as.
#
# No key staging: the operator mounts two Secret entries and sshd reads them in place —
#   ssh_host_ed25519_key -> /etc/ssh/ssh_host_ed25519_key   (host identity, subPath file)
#   authorized_keys      -> /run/ssh/authorized_keys        (platform keys, directory mount)
#
# Build context MUST be images/ (not this directory) so common/ is COPYable.
#   docker build --platform linux/amd64 -f ssh-maca-pytorch/Dockerfile \
#     -t harbor.isuanova.com/suanova/ssh-maca-pytorch:<tag> images/
#   (or: make -C images build-ssh-maca)

# The platform registry's mirrored copy of the vendor package, which is what a bare `docker build`
# resolves. Every published build substitutes the same reference pinned by digest through BASE_ARGS
# in images/Makefile — which is where the base is bumped, and the pin is the only difference.
#
# The vendor publishes this package per architecture (`-amd64` is part of the tag), and the mirror
# is a single-arch manifest rather than an index, so this image is amd64-only — see MACA_PLATFORM
# in the Makefile.
ARG MACA_BASE=harbor.isuanova.com/mirrors/cr.metax-tech.com/public-library/maca-pytorch:3.9.0.12-torch2.4-py310-ubuntu22.04-amd64
FROM ${MACA_BASE}

# The vendor image runs as root and ships no non-root account; step up for the overlay steps.
USER root

# Optional apt mirror override for CN/offline builds; empty = upstream. No PIP_INDEX_URL, unlike
# the jupyter sibling: this image installs nothing from pip, and the base ships no pip anyway.
ARG APT_MIRROR=""
RUN if [ -n "$APT_MIRROR" ]; then \
      sed -i -E "s#https?://(archive|ports)\.ubuntu\.com/(ubuntu|ubuntu-ports)#${APT_MIRROR}#g" \
        /etc/apt/sources.list /etc/apt/sources.list.d/*.sources 2>/dev/null || true; \
    fi

ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates \
      openssh-server \
      tini \
    && rm -rf /var/lib/apt/lists/*

# Platform account: literal 'ubuntu', uid/gid 1000, home /home/ubuntu — the identity the
# self-authored CPU image ships, so one account names both and a DevEnvironment states it once
# (spec.runtime.user: ubuntu). The workspace PVC mounts at the home, so nothing durable is baked
# under it; ~/.ssh exists only so the account's own `ssh-copy-id` target exists when no PVC is
# attached.
RUN groupadd -g 1000 ubuntu \
    && useradd -u 1000 -g 1000 -s /bin/bash -d /home/ubuntu ubuntu \
    && mkdir -p /home/ubuntu/.ssh \
    && chown -R 1000:1000 /home/ubuntu \
    && chmod 700 /home/ubuntu/.ssh

# No host keys in the image: identity comes from the mounted Secret. Their absence is also what
# tells the entrypoint whether ssh is enabled — and in ssh mode an absent key is fatal, which is
# the right reading of "an ssh environment was asked for but nothing mounted the identity".
# /run/sshd is the privilege separation directory a *root* sshd requires and a non-root one never
# consults - the apt package relies on the init system to create it, which a container has none of
# (see common/sshd/10-devenv.conf).
RUN rm -f /etc/ssh/ssh_host_* \
    && mkdir -p -m 0700 /run/sshd

# The vendor base ships no ENTRYPOINT, so the platform supplies it. It is the same entrypoint as
# every other image; here the baked CUBESTACK_IMAGE=ssh below selects the branch that execs sshd
# in the foreground and never reaches "$@", which is why this image carries no CMD.
COPY common/entrypoint.sh /usr/local/bin/cubestack-entrypoint.sh
# Shared sshd config (common/), installed by the script that also owns its placeholders: this
# image's ssh login account (which must match USER below) and the session environment. The
# vendor's PATH, loader and linker paths and the MACA variables are named here so the ssh
# session carries the same toolchain the container runs with — sshd replaces a session's
# environment, so anything not named is simply absent. This image is defined by that: the
# MACA stack it exists to serve is reachable over ssh only because it is named (see the
# drop-in and the script).
ARG SSH_USER=ubuntu
COPY common/sshd/10-devenv.conf /etc/ssh/sshd_config.d/10-devenv.conf
COPY common/sshd/install-dropin.sh /tmp/install-dropin.sh
RUN chmod +x /usr/local/bin/cubestack-entrypoint.sh \
    && sh /tmp/install-dropin.sh "${SSH_USER}" \
         PATH LD_LIBRARY_PATH LIBRARY_PATH MACA_PATH MACA_CLANG_PATH TORCH_ALLOW_TF32_CUBLAS_OVERRIDE \
    && rm -f /tmp/install-dropin.sh

# The vendor's PATH, LD_LIBRARY_PATH and MACA_PATH are inherited from the base: they are what put
# the MACA stack on the account's search path, which a non-root login needs exactly as the
# vendor's root shell did.
ENV CUBESTACK_IMAGE=ssh \
    HOME=/home/ubuntu

WORKDIR /home/ubuntu
USER 1000:1000
# 2222 only: this image serves no jupyter, so it declares no 8888.
EXPOSE 2222
ENTRYPOINT ["tini", "-g", "--", "/usr/local/bin/cubestack-entrypoint.sh"]
