# Cubestack 'jupyter-minimal' DevEnvironment image (CPU) — stock-native overlay.
#
# Thin overlay on the docker-stacks minimal-notebook image that enables ONLY ssh
# for the platform (see docs/design/devenv-images/decision.md): everything else
# stays identical to the stock image. The upstream layout is kept exactly — user
# 'jovyan' (uid 1000, primary group 'users' gid 100), $HOME=/home/jovyan, and the
# stock launch chain tini -> start.sh -> start-notebook.py. A DevEnvironment
# running this image configures that layout explicitly: spec.runtime.user=jovyan
# derives the workspace mount /home/jovyan, and securityContext.runAsGroup must
# be set to 100 (see images/README.md).
#
# Additions on top of stock:
#   - openssh-server, started only when the operator mounted the ssh Secret. No
#     key staging: the Secret entries are mounted (host key at
#     /etc/ssh/ssh_host_ed25519_key as a subPath file, platform authorized_keys at
#     /run/ssh/authorized_keys via a directory mount, outside $HOME) and sshd
#     reads them in place.
#   - Jupyter itself honors JUPYTER_TOKEN and NOTEBOOK_ARGS natively (token,
#     base_url, ...) — no overlay jupyter logic.
#
# Build context MUST be images/ so common/ is COPYable, and the platform must be named: the base
# is multi-arch, so a bare `docker build` resolves it to the *host* architecture and an arm64 host
# would publish an arm64-only image.
#   docker build --platform linux/amd64 -f jupyter/Dockerfile \
#     -t harbor.isuanova.com/suanova/jupyter-minimal:<tag> images/
#   (or: make -C images build)

# The upstream reference, for a build that reaches Docker Hub. Every published build
# substitutes the platform registry's mirrored copy of this same date, pinned by digest
# through BASE_ARGS in images/Makefile — which is where the base is bumped.
ARG JUPYTER_BASE=quay.io/jupyter/minimal-notebook:2026-09-07
FROM ${JUPYTER_BASE}

# docker-stacks ends with `USER jovyan`; step up for the overlay steps.
USER root

# Optional apt/pip mirror overrides for CN/offline builds; empty = upstream.
ARG APT_MIRROR=""
ARG PIP_INDEX_URL=""
RUN if [ -n "$APT_MIRROR" ]; then \
      sed -i -E "s#https?://(archive|ports)\.ubuntu\.com/(ubuntu|ubuntu-ports)#${APT_MIRROR}#g" \
        /etc/apt/sources.list /etc/apt/sources.list.d/*.sources 2>/dev/null || true; \
    fi

ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      openssh-server \
    && rm -rf /var/lib/apt/lists/*

# Create ~/.ssh (so a manual run without a workspace PVC still works and the
# account's own ssh-copy-id target exists; the PVC shadows it in-cluster), and
# drop the package-generated host keys: identity comes from the mounted Secret,
# and their absence is how the entrypoint detects that ssh is enabled.
# /run/sshd is the privilege separation directory a *root* sshd requires and a
# non-root one never consults - the apt package relies on the init system to
# create it, which a container has none of (see common/sshd/10-devenv.conf).
RUN mkdir -p /home/jovyan/.ssh \
    && mkdir -p -m 0700 /run/sshd \
    && chown 1000:100 /home/jovyan/.ssh \
    && chmod 700 /home/jovyan/.ssh \
    && rm -f /etc/ssh/ssh_host_*

COPY common/entrypoint.sh /usr/local/bin/cubestack-entrypoint.sh
# Shared sshd config (common/), with this family's ssh login account substituted
# for the @SSH_USER@ placeholder. It must match the stock container account
# (jovyan) that USER selects below.
ARG SSH_USER=jovyan
COPY common/sshd/10-devenv.conf /etc/ssh/sshd_config.d/10-devenv.conf
RUN chmod +x /usr/local/bin/cubestack-entrypoint.sh \
    && sed -i "s/@SSH_USER@/${SSH_USER}/" /etc/ssh/sshd_config.d/10-devenv.conf

ENV CUBESTACK_IMAGE=jupyter

EXPOSE 8888 2222

# Back to the stock account; HOME/workdir remain stock /home/jovyan.
USER 1000

# Our entrypoint runs first (starts sshd when the Secret is mounted), then hands
# off to the image CMD, which reproduces the stock launch chain exactly (the base
# image runs tini + ENTRYPOINT start.sh + CMD start-notebook.py).
ENTRYPOINT ["tini", "-g", "--", "/usr/local/bin/cubestack-entrypoint.sh"]
CMD ["/usr/local/bin/start.sh", "start-notebook.py"]
