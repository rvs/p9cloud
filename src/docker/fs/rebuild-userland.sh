#!/bin/sh
# Rebuild the userland on the cpuwait kernel:
#
#	src/docker/fs/rebuild-userland.sh [FILE]
#
# boots boot/9pc64cpuwait with boot/cpu.nvram in qemu, then runs the
# 9front-fs image's drawterm against it, with the image's /9front as
# the session's root, for one command: rc /sys/src/build.rc.  The
# rebuilt tree, the container's /9front, is written to FILE (default
# ./9front.tar) as a tar file, with its Plan 9 owners.
#
# mk release does not fail when a directory's mk does, so the build
# fails on any mk error in its output.  The output and the VM's
# console are in a temporary directory, which is kept (and named) if
# the build fails.
#
# Environment: IMAGE (9front-fs, built from here first), PORT (17019,
# forwarded from all of this host's addresses so the container can
# reach it), PASS (glen||glenda, cpu.nvram's password).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=$(realpath -m "${1:-9front.tar}")
image=${IMAGE:-9front-fs}
port=${PORT:-17019}
tmp=$(mktemp -d)
ctr=rebuild-userland-$$

die() { echo "rebuild-userland: $*" >&2; exit 1; }
cleanup() {
	[ -f "$tmp/pid" ] && kill "$(cat "$tmp/pid")" 2>/dev/null
	docker rm -f "$ctr" >/dev/null 2>&1
	if [ -f "$tmp/ok" ]; then rm -rf "$tmp"; else echo "rebuild-userland: see $tmp" >&2; fi
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

docker build -q -t "$image" "$here" >/dev/null

accel="-accel tcg"
[ -w /dev/kvm ] && accel="-accel kvm -cpu host"
qemu-system-x86_64 $accel -m 1G -smp "$(nproc)" \
	-kernel "$here/boot/9pc64cpuwait" \
	-append "console=0 sysname=cpu service=cpu nvram=/dev/sdF0/data nobootprompt='cpuwait -g 10.0.2.2 ether /net/ether0 10.0.2.15 255.255.255.0'" \
	-drive "file=$here/boot/cpu.nvram,if=none,id=nvram,format=raw,readonly=on" \
	-device virtio-blk-pci,drive=nvram,disable-legacy=on \
	-netdev "user,id=net,restrict=on,hostfwd=tcp:0.0.0.0:$port-:17019" \
	-device virtio-net-pci,netdev=net,disable-legacy=on \
	-serial "file:$tmp/console" -monitor none -display none \
	-daemonize -pidfile "$tmp/pid" 2>"$tmp/qemu" || { cat "$tmp/qemu" >&2; die "qemu failed"; }

# drawterm waits for the cpu server to answer (WAIT, 60 s); as sys, the
# files it makes in /9front are owned by sys, like the rest of the tree
docker run --name "$ctr" --user 110000:110000 -e "PORT=$port" -e "PASS=${PASS:-glen||glenda}" "$image" \
	drawterm -G -c 'rc /sys/src/build.rc' </dev/null 2>&1 | tee "$tmp/log"
tr -d '\r' <"$tmp/log" | grep -E '^mk: .*(exit status|know how)' >&2 && die "mk failed"
[ "$(docker inspect -f '{{.State.ExitCode}}' "$ctr")" = 0 ] || die "build.rc failed"
docker cp -q "$ctr:/9front" - >"$out" || die "cannot copy /9front"
touch "$tmp/ok"
ls -l "$out"
