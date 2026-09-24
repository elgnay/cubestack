#!/bin/sh
# Fill in the shared sshd drop-in's per-image placeholders, and prove it was done.
#
#   install-dropin.sh <ssh-login-user> <variable>...
#
# Run at build time by every Dockerfile that copies common/sshd/10-devenv.conf. This is
# the only thing that knows the placeholders exist, so an image cannot ship the drop-in
# half-configured: a Dockerfile that copies the template without calling this fails the
# build, because the placeholders survive.
#
# @SSH_USER@  the login account (AllowUsers) — a non-root sshd can only serve the uid it
#             runs as, so this must match the image's USER.
# @SSH_ENV@   the pair list for the drop-in's single SetEnv, assembled here.
#
# The session environment is not the container's by default: sshd replaces it, and what
# it replaces it with is a PATH compiled into sshd itself. So anything an image's base
# published has to be named to survive, and only the image knows what that is — the
# self-authored CPU image needs little, the MACA base's toolchain (its compilers, MPI and
# UCX) needs several. The names are therefore the caller's, and the values are read out
# of THIS process's environment, which is the build shell's — the image's ENV by the time
# this runs, which is the base's plus whatever the overlay has set (both MACA images
# prepend /opt/conda/bin to PATH here, so a session reaches the interpreter that owns the
# vendor torch and not merely a login shell). What is read is what the container runs with.
#
# Named explicitly rather than harvested from `env`: a session should carry the base's
# toolchain settings, not its build-time debris (DEBIAN_FRONTEND, HOSTNAME, PWD) or this
# image's own CUBESTACK_IMAGE. A named variable that is unset or empty is a build
# failure rather than a variable that quietly drops out of every session.
set -eu

conf=/etc/ssh/sshd_config.d/10-devenv.conf
user=${1-}
[ -n "$user" ] || { echo "install-dropin: no ssh login user given" >&2; exit 1; }
shift

[ "$#" -gt 0 ] || { echo "install-dropin: no session variables named" >&2; exit 1; }

pairs=
has_path=0
for name in "$@"; do
  [ "$name" = PATH ] && has_path=1
  eval "value=\${$name-}"
  if [ -z "$value" ]; then
    echo "install-dropin: $name is unset or empty in the build environment" >&2
    exit 1
  fi
  case "$value" in
    *[[:space:]]*)
      echo "install-dropin: $name contains whitespace, which SetEnv cannot carry: $value" >&2
      exit 1
      ;;
  esac
  pairs="${pairs:+$pairs }$name=$value"
done

# Every session needs the image's PATH; a caller that forgot it would leave sshd's own
# compiled-in default in place, which is the failure this script exists to prevent.
[ "$has_path" = 1 ] || { echo "install-dropin: PATH must be named" >&2; exit 1; }

# & and \ are special to sed's replacement text, and the delimiter would end it early.
# No PATH contains them, but a silently mis-substituted value is exactly what the checks
# below are for, so they are escaped rather than assumed absent.
escape() { printf '%s' "$1" | sed -e 's/[\\&|]/\\&/g'; }

sed -i -e "s|@SSH_USER@|$(escape "$user")|" -e "s|@SSH_ENV@|$(escape "$pairs")|" "$conf"

for placeholder in '@SSH_USER@' '@SSH_ENV@'; do
  if grep -q -- "$placeholder" "$conf"; then
    echo "install-dropin: $placeholder left in $conf (template and script disagree?)" >&2
    exit 1
  fi
done

# sshd applies the first-obtained value and does not merge repeats, so a second SetEnv
# anywhere in this file would be silently ignored — the variables simply would not
# arrive. Cheap to check here; invisible at runtime.
if [ "$(grep -c '^SetEnv' "$conf")" != 1 ]; then
  echo "install-dropin: $conf must hold exactly one SetEnv line (sshd ignores later ones)" >&2
  exit 1
fi

echo "install-dropin: $conf -> AllowUsers $user; SetEnv $pairs"
