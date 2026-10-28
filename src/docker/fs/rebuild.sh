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
# With SRC or REF set, the session first updates the tree's sources to
# commit REF (default HEAD) of the git checkout SRC (default the 9front
# beside p9cloud), as sysupdate would: rc /sys/src/sysupdate.rc, with
# SRC's .git mounted read-only at /9front/dist/host/.git, its refs
# replaced by one branch, front, at REF.  REF must descend from the
# seed's commit, /9front/dist/9front/.git/refs/heads/front.  Only
# commits count, not SRC's uncommitted changes.  An updated kernel
# needs its updated userland, so kernel takes no SRC or REF: use all.
#
# Environment: IMAGE (9front-fs, built from here first), PORT (17019,
# forwarded from all of this host's addresses so the container can
# reach it), PASS (glen||glenda, cpu.nvram's password), MEM (1G),
# SRC and REF (above).
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
	# kept if the VM ran and the build failed
	if [ -f "$tmp/ok" ] || [ ! -f "$tmp/pid" ]; then rm -rf "$tmp"; else echo "rebuild: see $tmp" >&2; fi
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

docker build -q -t "$image" "$here" >/dev/null

mounts=()
if [ -n "${SRC:-}${REF:-}" ]; then
	[ "$what" != kernel ] || die "an updated kernel needs its updated userland: use all"
	src=$(realpath -m "${SRC:-$here/../../../../9front}")
	gitdir=$(git -C "$src" rev-parse --path-format=absolute --git-common-dir) || die "SRC $src: no git checkout"
	[ "$(git -C "$src" rev-parse --show-object-format)" = sha1 ] || die "SRC $src: 9front's git reads only sha1 repositories"
	want=$(git -C "$src" rev-parse --verify -q "${REF:-HEAD}^{commit}") || die "REF ${REF:-HEAD}: no commit in $src"
	seed=$(docker run --rm --entrypoint cat "$image" /9front/dist/9front/.git/refs/heads/front)
	git -C "$src" merge-base --is-ancestor "$seed" "$want" || die "${REF:-HEAD} does not descend from the seed's $seed"
	echo "rebuild: $seed..$want, $(git -C "$src" rev-list --count "$seed..$want") commits" >&2
	# 9front's git reads loose refs only (not packed-refs) and the
	# remote's front: give it one branch, front, at the commit
	mkdir -p "$tmp/remote/refs/heads"
	echo "$want" >"$tmp/remote/refs/heads/front"
	echo 'ref: refs/heads/front' >"$tmp/remote/HEAD"
	chmod -R a+rX "$tmp/remote"
	mounts=(-v "$gitdir:/9front/dist/host/.git:ro"
		-v "$tmp/remote/refs:/9front/dist/host/.git/refs:ro"
		-v "$tmp/remote/HEAD:/9front/dist/host/.git/HEAD:ro")
	cmd="rc /sys/src/sysupdate.rc && $cmd"
fi

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
docker run -d --name "$ctr" --user 110000:110000 "${mounts[@]}" --entrypoint sleep "$image" infinity >/dev/null
docker exec -e "PORT=$port" -e "PASS=${PASS:-glen||glenda}" "$ctr" \
	9front drawterm -G -c "$cmd" </dev/null 2>&1 | tee "$tmp/log" || die "$what build failed"
! tr -d '\r' <"$tmp/log" | grep -E '^mk: .*(exit status|know how|no recipe)|^bad include|^(merge|cp|pull) failed' >&2 ||
	die "$what build failed"
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
	# not SRC's .git, mounted at dist/host
	docker exec -u 0 "$ctr" tar -C / --exclude=9front/dist/host -cf - $paths >"$out" || die "cannot copy $paths"
	ls -l "$out"
fi
touch "$tmp/ok"
