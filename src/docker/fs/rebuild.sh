#!/bin/bash
# Rebuild a userland, a kernel, or both, for any of the tree's
# objtypes, on the amd64 cpuwait kernel:
#
#	src/docker/fs/rebuild.sh [-a OBJTYPE] userland [FILE]
#	src/docker/fs/rebuild.sh [-c CONF] kernel [FILE]
#	src/docker/fs/rebuild.sh [-a OBJTYPE] [-c CONF] all [FILE]
#
# boots boot/9pc64cpuwait with boot/cpu.nvram in qemu, then runs the
# 9front-fs image's drawterm against it, with the container's /9front
# as the session's root, for one command:
#
#	userland: rc /sys/src/build.rc OBJTYPE (default amd64)
#	kernel:   rc /sys/src/9/kernel.rc CONF (default pc64cpuwait)
#	all:      both, in one session; CONF defaults to OBJTYPE's
#	          kernel below
#
# The builds are in place.  userland and all write FILE (default
# ./9front.tar, or ./9front.OBJTYPE.tar) as a tar file with the Plan 9
# owners: for amd64 the whole /9front, else 9front/OBJTYPE and
# 9front/acme/bin/OBJTYPE, which hold all a build for OBJTYPE makes,
# its kernel too.  kernel copies 9CONF to FILE (default ./9CONF),
# and 9CONF.u, s9CONF or 9CONF.gz, if the mkfile makes them, beside
# it.  The release has only amd64's userland, which other kernels need
# to link, so other kernels build with all only.
#
# OBJTYPE is one of mkfile.proto's CPUS; the cross-compilers are amd64
# programs, so everything builds on the amd64 VM.  mk release does not
# fail when a directory's mk does, so the build fails on any mk error
# in its output.  The output and the VM's console are in a temporary
# directory, which is kept (and named) if the build fails.
#
# Environment: IMAGE (9front-fs, built from here first), PORT (17019,
# forwarded from all of this host's addresses so the container can
# reach it), PASS (glen||glenda, cpu.nvram's password), MEM (1G).
set -euo pipefail
usage() { echo "usage: $0 [-a OBJTYPE] [-c CONF] userland|kernel|all [FILE]" >&2; exit 2; }
objtype=amd64 conf=
while getopts 'a:c:' opt; do
	case $opt in
	a) objtype=$OPTARG ;;
	c) conf=$OPTARG ;;
	*) usage ;;
	esac
done
shift $((OPTIND - 1))
# each objtype's kernel for all: a cpuwait server where qemu can boot
# one (arm64: qemu-system-aarch64 -M virt), else a stock one; power's
# kernels do not build in this release
case $objtype in
amd64)   k=pc64cpuwait ;;
386)     k=pccpuwait ;;
arm64)   k=qemucpuwait ;;
arm)     k=pi2 ;;
mips)    k=indy ;;
spim)    k=mt7688 ;;
power|power64) k= ;;
*) echo "rebuild: no objtype $objtype" >&2; usage ;;
esac
[[ $conf =~ ^[a-z0-9]*$ ]] || usage
what=${1:-}
case $what in
userland) cmd="rc /sys/src/build.rc $objtype" conf= ;;
kernel)   conf=${conf:-pc64cpuwait} cmd="rc /sys/src/9/kernel.rc $conf" ;;
all)      conf=${conf:-$k}
	[ -n "$conf" ] || { echo "rebuild: no kernel for $objtype: use -c or userland" >&2; exit 2; }
	cmd="rc /sys/src/build.rc $objtype && rc /sys/src/9/kernel.rc $conf" ;;
*) usage ;;
esac
if [ "$what" = kernel ]; then def=9$conf
elif [ $objtype = amd64 ]; then paths=9front def=9front.tar
else paths="9front/$objtype 9front/acme/bin/$objtype" def=9front.$objtype.tar
fi
here=$(cd "$(dirname "$0")" && pwd)
out=$(realpath -m "${2:-$def}")
mkdir -p "$(dirname "$out")"
image=${IMAGE:-9front-fs}
port=${PORT:-17019}
tmp=$(mktemp -d)
ctr=rebuild-$what-$$

die() { echo "rebuild: $*" >&2; exit 1; }
cleanup() {
	[ -f "$tmp/pid" ] && kill "$(cat "$tmp/pid")" 2>/dev/null
	docker rm -f "$ctr" >/dev/null 2>&1
	if [ -f "$tmp/ok" ]; then rm -rf "$tmp"; else echo "rebuild: see $tmp" >&2; fi
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

docker build -q -t "$image" "$here" >/dev/null

accel="-accel tcg"
[ -w /dev/kvm ] && accel="-accel kvm -cpu host"
qemu-system-x86_64 $accel -m "${MEM:-1G}" -smp "$(nproc)" \
	-kernel "$here/boot/9pc64cpuwait" \
	-append "console=0 sysname=cpu service=cpu nvram=/dev/sdF0/data nobootprompt='cpuwait -g 10.0.2.2 ether /net/ether0 10.0.2.15 255.255.255.0'" \
	-drive "file=$here/boot/cpu.nvram,if=none,id=nvram,format=raw,readonly=on" \
	-device virtio-blk-pci,drive=nvram,disable-legacy=on \
	-netdev "user,id=net,restrict=on,hostfwd=tcp:0.0.0.0:$port-:17019" \
	-device virtio-net-pci,netdev=net,disable-legacy=on \
	-serial "file:$tmp/console" -monitor none -display none \
	-daemonize -pidfile "$tmp/pid" 2>"$tmp/qemu" || { cat "$tmp/qemu" >&2; die "qemu failed"; }

# The container idles, drawterm runs in it, and the results come out
# with its tar.  drawterm waits for the cpu server to answer (WAIT,
# 60 s); as sys, the files it makes in /9front are owned by sys, like
# the rest of the tree.
docker run -d --name "$ctr" --user 110000:110000 --entrypoint sleep "$image" infinity >/dev/null
docker exec -e "PORT=$port" -e "PASS=${PASS:-glen||glenda}" "$ctr" \
	9front drawterm -G -c "$cmd" </dev/null 2>&1 | tee "$tmp/log" || die "$what build failed"
! tr -d '\r' <"$tmp/log" | grep -E '^mk: .*(exit status|know how|no recipe)|^bad include' >&2 || die "mk failed"
if [ "$what" = kernel ]; then
	# 9CONF, and 9CONF.u, s9CONF or 9CONF.gz, in the kernel's /$objtype
	docker exec -w /9front "$ctr" sh -c "ls -d */9$conf */9$conf.* */s9$conf 2>/dev/null" >"$tmp/k" || :
	grep -qx "[^/]*/9$conf" "$tmp/k" || die "no kernel 9$conf"
	while read -r f; do
		b=${f#*/} dest=$(dirname "$out")/${f#*/}
		[ "$b" = "9$conf" ] && dest=$out
		docker exec -w /9front "$ctr" cat "$f" >"$dest" || die "cannot copy $f"
		sha256sum "$dest"
	done <"$tmp/k"
else
	docker exec -u 0 "$ctr" tar -C / -cf - $paths >"$out" || die "cannot copy $paths"
	ls -l "$out"
fi
touch "$tmp/ok"
