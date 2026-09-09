# Cubestack 'jupyter' DevEnvironment base image (CPU).
#
# Thin overlay on the docker-stacks minimal-notebook image: keep the mature
# conda/JupyterLab stack and overlay the platform contract (see
# docs/design/devenv-images/decision.md):
#   - account 'user', uid/gid 1000, $HOME/workdir on /workspace (workspace PVC)
#   - JupyterLab on :8888 honoring JUPYTER_TOKEN and CUBESTACK_BASE_URL
#   - optional sshd on :22 when the /etc/cubestack/ssh Secret is mounted
#
# Build context MUST be images/ so common/ is COPYable:
#   docker build -f jupyter/Dockerfile -t cubestack/jupyter:smoke images/
#   (or: make -C images build)

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

# docker-stacks ships user 'jovyan' (uid 1000, primary group 'users' gid 100).
# Rename to the platform 'user' account and move the primary group to gid 1000
# so a runAsGroup=1000 process can write /opt/conda (upstream files are group
# 'users', gid 100). /home/user keeps the real home for non-durable runtime dirs;
# passwd HOME points at the workspace PVC so ssh login and notebook root land on
# /workspace.
RUN groupadd -g 1000 cubestack \
    && usermod -l user jovyan \
    && usermod -d /home/user -m user \
    && usermod -g 1000 user \
    && mkdir -p /workspace \
               /home/user/.cache \
               /home/user/.config \
               /home/user/.local/share/jupyter/runtime \
    && chown -R 1000:1000 /home/user /workspace \
    && usermod -d /workspace user \
    && chown -R 1000:1000 /opt/conda

# conda on PATH for interactive ssh logins (login shells read /etc/profile.d).
RUN printf 'export PATH="/opt/conda/bin:$PATH"\n' > /etc/profile.d/99-cubestack-conda.sh

COPY common/entrypoint.sh /usr/local/bin/cubestack-entrypoint.sh
COPY common/ssh-setup.sh /usr/local/bin/cubestack-ssh-setup.sh
COPY common/run-jupyter.sh /usr/local/bin/cubestack-run-jupyter.sh
COPY common/sshd/10-cubestack-nonroot.conf /etc/ssh/sshd_config.d/10-cubestack-nonroot.conf
RUN chmod +x /usr/local/bin/cubestack-entrypoint.sh \
             /usr/local/bin/cubestack-ssh-setup.sh \
             /usr/local/bin/cubestack-run-jupyter.sh

# $HOME/workdir on the PVC; ephemeral caches/runtime are redirected to the image
# (/home/user) so they never clobber or durably pollute the user's workspace.
# The SSH login account this image exposes when its sshd runs; the platform
# reads it to build the ssh://<user>@... endpoint (see images README, #169).
LABEL ai.cubestack.io/ssh-user="user"

ENV CUBESTACK_IMAGE=jupyter \
    HOME=/workspace \
    XDG_CACHE_HOME=/home/user/.cache \
    XDG_CONFIG_HOME=/home/user/.config \
    XDG_DATA_HOME=/home/user/.local/share \
    JUPYTER_RUNTIME_DIR=/home/user/.local/share/jupyter/runtime

WORKDIR /workspace
USER 1000:1000
EXPOSE 8888 22
ENTRYPOINT ["tini", "-g", "--", "/usr/local/bin/cubestack-entrypoint.sh"]
