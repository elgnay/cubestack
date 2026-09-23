#!/usr/bin/env bash
# Cubestack jupyter launch chain for the stock-native CPU image.
#
# This is the image CMD. common/entrypoint.sh has already started sshd when the operator mounted
# the ssh host key, and hands off here (mode jupyter).
#
# The stock account keeps the stock chain: docker-stacks' start.sh execs the notebook as the account
# and with the home the image bakes (/home/jovyan), which is where the platform mounts the workspace
# for the identity this image advertises.
#
# uid 0 does not take that path. start.sh reads it as a startup mode and relocates root's home to
# /home/root whatever the environment declares, while the platform derives /root as a root
# environment's workspace mount (::resolveMountPath) and mounts the user's claim there — so the
# notebooks would land on the container filesystem with the PVC unused. Keeping start.sh out of the
# chain for uid 0 leaves root with the home the derivation expects, and with the one its passwd entry
# already gives an ssh session in the same container.
set -euo pipefail

if [ "$(id -u)" != 0 ]; then
  exec /usr/local/bin/start.sh start-notebook.py
fi

# The guard is what keeps a declared HOME authoritative: /home/jovyan is the value this image bakes
# and the base's WORKDIR, so anything else came from the spec (the controller passes spec.runtime.env
# through) and is where the claim was mounted. It is also the one home a root environment here cannot
# ask for, a declared /home/jovyan being indistinguishable from the image's default.
if [ "${HOME:-}" = /home/jovyan ]; then
  export HOME=/root
fi

# jupyter serves its working directory (ServerApp.root_dir defaults to os.getcwd()), so the home has
# to be entered and not only exported. This is the one thing start.sh did for root that is still
# wanted — it ends in `cd /home/root` — and start-notebook.py is what keeps the rest of the chain:
# NOTEBOOK_ARGS (the platform's flags: --allow-root, --ServerApp.base_url) and JUPYTER_TOKEN.
cd "$HOME"
exec /usr/local/bin/start-notebook.py
