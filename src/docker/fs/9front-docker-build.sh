#!/bin/bash
# Build image 9front-ARCH:latest, this directory's Dockerfile with,
# for its seed, a tree rebuilt from a 9front git repository:
#
#	src/docker/fs/9front-docker-build.sh ARCH REPO
#
# rebuild.sh updates the seed's tree to REPO's REF (default HEAD) and
# builds ARCH's userland and its kernel there (all; userland only for
# power and power64, which have none), then commits the build's
# container as a temporary image, which the Dockerfile's SEED names:
# its /9front is the image's.  ARCH is one of rebuild.sh's objtypes
# (riscv64 needs a REPO that has it, as 9front's et branch).
#
# Environment: CONF (rebuild.sh's -c: the kernel), REF, UPDATE,
# EXCLUDE, PORT, MEM (rebuild.sh's).
set -euo pipefail
[ $# = 2 ] || { echo "usage: $0 ARCH REPO" >&2; exit 2; }
arch=$1 repo=$(realpath "$2")
here=$(cd "$(dirname "$0")" && pwd)
seed=9front-$arch-seed:$$
trap 'docker rmi "$seed" >/dev/null 2>&1 || :' EXIT
what=all
case $arch in power|power64) [ -n "${CONF:-}" ] || what=userland ;; esac
# the tar rebuild.sh writes is not needed: the committed tree is
SRC=$repo COMMIT=$seed "$here/rebuild.sh" -a "$arch" ${CONF:+-c "$CONF"} $what /dev/null
docker build -t "9front-$arch:latest" --build-arg "SEED=$seed" "$here"
