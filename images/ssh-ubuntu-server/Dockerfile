# Cubestack 'ssh-ubuntu-server' DevEnvironment base image.
#
# Minimal image exposing sshd on :22 for the platform 'ssh' DevEnvironment type.
# The operator mounts the ssh Secret read-only at /etc/cubestack/ssh (host keys +
# authorized_keys) and runs the container as uid/gid 1000 with $HOME=/workspace
# (the workspace PVC). The entrypoint stages those keys and starts sshd.
#
# Build context MUST be images/ (not this directory) so common/ is COPYable:
#   docker build -f ssh-ubuntu-server/Dockerfile -t cubestack/ssh-ubuntu-server:smoke images/
#   (or: make -C images build)

ARG UBUNTU_VERSION=22.04
FROM ubuntu:${UBUNTU_VERSION}

# Optional apt mirror override (e.g. a Tsinghua/Aliyun mirror in CN); empty = upstream.
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

# Platform account: literal 'user', uid/gid 1000. passwd HOME points at the
# workspace PVC so ssh login and $HOME land on /workspace. /home/user holds only
# non-durable runtime state (staged ssh keys, caches).
RUN groupadd -g 1000 cubestack \
    && mkdir -p /workspace \
               /home/user/cubestack/ssh \
               /home/user/.cache /home/user/.config /home/user/.local/share \
    && useradd -u 1000 -g 1000 -s /bin/bash -d /workspace user \
    && chown -R 1000:1000 /workspace /home/user \
    && chmod 700 /home/user/cubestack/ssh

COPY common/entrypoint.sh /usr/local/bin/cubestack-entrypoint.sh
COPY common/ssh-setup.sh /usr/local/bin/cubestack-ssh-setup.sh
COPY common/sshd/10-cubestack-nonroot.conf /etc/ssh/sshd_config.d/10-cubestack-nonroot.conf
RUN chmod +x /usr/local/bin/cubestack-entrypoint.sh \
             /usr/local/bin/cubestack-ssh-setup.sh

ENV CUBESTACK_IMAGE=ssh \
    HOME=/workspace

WORKDIR /workspace
USER 1000:1000
EXPOSE 22
ENTRYPOINT ["tini", "-g", "--", "/usr/local/bin/cubestack-entrypoint.sh"]
