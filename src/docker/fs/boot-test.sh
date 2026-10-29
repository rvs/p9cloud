#!/bin/bash
# Boot a cpuwait kernel in qemu and test it over drawterm:
#
#	src/docker/fs/boot-test.sh [-a OBJTYPE] KERNEL [TAR]
#
# KERNEL is a cpuwait kernel made by rebuild.sh, for one of the ports
# qemu boots as is, by OBJTYPE (default from KERNEL's name):
#
#	amd64    9pc64cpuwait   qemu-system-x86_64 (KVM if it can)
#	386      9pccpuwait     qemu-system-x86_64 (KVM if it can)
#	arm64    9qemucpuwait   qemu-system-aarch64 -M virt, its .u
#	                        (uImage) beside it, the DTB at RAM's start
#	riscv64  9rv64cpuwait   qemu-system-riscv64 -M virt, OpenSBI
#
# all with boot/cpu.nvram.  The session's root is a 9front-fs
# container's /9front, with TAR (a userland tar made by rebuild.sh,
# needed unless the image has OBJTYPE's userland, as 9front-fs has
# amd64's and 9front-docker-build.sh's images their own) unpacked in it, after, with SRC or REF set, its
# sources are updated as rebuild.sh updates them (UPDATE=host only).
# The tests, each its own drawterm session:
#
#	login:   glenda, with PASS, gets OBJTYPE's shell
#	native:  ls and rc, built there with OBJTYPE's compiler from the
#	         tree's sources, are identical to /OBJTYPE/bin's (so TAR
#	         must be built from the same sources)
#	status:  a failing command fails drawterm
#	pass:    another password is refused
#
# The VM's console is in a temporary directory, kept (and named) if a
# test fails.
#
# Environment: IMAGE (default 9front-fs, built from here first), PORT (17029),
# PASS (glen||glenda), MEM (1G), SMP (all CPUs with KVM, else 2), SRC,
# REF and EXCLUDE (as rebuild.sh's).
set -euo pipefail
usage() { echo "usage: $0 [-a OBJTYPE] KERNEL [TAR]" >&2; exit 2; }
objtype=
while getopts 'a:' opt; do
	case $opt in
	a) objtype=$OPTARG ;;
	*) usage ;;
	esac
done
shift $((OPTIND - 1))
[ $# -ge 1 ] && [ $# -le 2 ] || usage
kernel=$(realpath "$1") tar=${2:+$(realpath "$2")}
[ -f "$kernel" ] || usage
[ -z "$tar" ] || [ -f "$tar" ] || { echo "boot-test: no TAR $tar" >&2; exit 1; }
if [ -z "$objtype" ]; then
	case $(basename "$kernel") in
	9pc64*) objtype=amd64 ;;
	9pc*)   objtype=386 ;;
	9qemu*) objtype=arm64 ;;
	9rv64*) objtype=riscv64 ;;
	*) echo "boot-test: $kernel: which objtype? -a" >&2; usage ;;
	esac
fi
here=$(cd "$(dirname "$0")" && pwd)
image=${IMAGE:-9front-fs}
port=${PORT:-17029}
pass=${PASS:-glen||glenda}
tmp=$(mktemp -d)
ctr=boot-test-$$

die() { echo "boot-test: $*" >&2; exit 1; }
cleanup() {
	[ -f "$tmp/pid" ] && kill "$(cat "$tmp/pid")" 2>/dev/null
	docker rm -f "$ctr" >/dev/null 2>&1
	if [ -f "$tmp/ok" ] || [ ! -f "$tmp/console" ]; then rm -rf "$tmp"; else echo "boot-test: see $tmp" >&2; fi
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

kvm=
[ -w /dev/kvm ] && kvm=1
smp=${SMP:-2}
case $objtype in
amd64|386)
	qemu=(qemu-system-x86_64 -kernel "$kernel")
	if [ -n "$kvm" ]; then qemu+=(-accel kvm -cpu host); smp=${SMP:-$(nproc)}; fi ;;
arm64)
	# 9front's arm64 kernel reads its bootargs, memory and cpus from
	# the DTB at 0x40000000, where qemu doesn't put it for -kernel
	[ "${kernel%.u}" = "$kernel" ] && kernel=$kernel.u
	[ -f "$kernel" ] || die "no uImage $kernel"
	qemu=(qemu-system-aarch64 -M virt-2.12,gic-version=3 -cpu cortex-a72 -kernel "$kernel") ;;
riscv64)
	# the generic kernel needs Svpbmt, Zicbom and Sstc
	qemu=(qemu-system-riscv64 -M virt -bios default -cpu rv64,svpbmt=true,zicbom=true,sstc=true -kernel "$kernel") ;;
*) die "no qemu target for $objtype: amd64, 386, arm64 or riscv64" ;;
esac
qemu+=(-m "${MEM:-1G}" -smp "$smp"
	-append "console=0 sysname=cpu service=cpu nvram=/dev/sdF0/data nobootprompt='cpuwait -g 10.0.2.2 ether /net/ether0 10.0.2.15 255.255.255.0'"
	-drive "file=$here/boot/cpu.nvram,if=none,id=nvram,format=raw,readonly=on"
	-device virtio-blk-pci,drive=nvram,disable-legacy=on
	-netdev "user,id=net,restrict=on,hostfwd=tcp:0.0.0.0:$port-:17019"
	-device virtio-net-pci,netdev=net,disable-legacy=on
	-monitor none -display none)
if [ $objtype = arm64 ]; then
	"${qemu[@]}" -machine dumpdtb="$tmp/virt.dtb" >"$tmp/qemu" 2>&1 || { cat "$tmp/qemu" >&2; die "cannot dump the DTB"; }
	qemu+=(-device "loader,file=$tmp/virt.dtb,addr=0x40000000,force-raw=on")
fi

# another IMAGE is taken as it is
[ -n "${IMAGE:-}" ] || docker build -q -t "$image" "$here" >/dev/null
docker run -d --name "$ctr" --user 110000:110000 --entrypoint sleep "$image" infinity >/dev/null
if [ -n "${SRC:-}${REF:-}" ]; then
	src=$(realpath -m "${SRC:-$here/../../../../9front}")
	want=$(git -C "$src" rev-parse --verify -q "${REF:-HEAD}^{commit}") || die "REF ${REF:-HEAD}: no commit in $src"
	seed=$(docker exec "$ctr" cat /9front/dist/9front/.git/refs/heads/front)
	python3 "$here/sysupdate.py" "$ctr" "$src" "$seed" "$want" ${EXCLUDE-et .github AGENTS.md} || die "update failed"
fi
if [ -n "$tar" ]; then
	docker exec -i -u 0 "$ctr" tar -C / -xpf - <"$tar" || die "cannot unpack $tar"
fi
docker exec "$ctr" test -x /9front/$objtype/bin/rc || die "no /$objtype/bin/rc in the tree: TAR"
# not in /tmp: drawterm covers the root's /tmp with its own
docker exec -i "$ctr" sh -c 'cat >/9front/sys/lib/boot-test.rc' <<'EOF'
rfork e
fn fail { echo boot-test: FAIL $* >[1=2]; exit fail }
u=`{cat /dev/user}
~ $cputype $1 || fail cputype $cputype, not $1
~ $u glenda || fail user $u
echo boot-test: ok login $u $cputype `{cat /dev/sysname}
objtype=$cputype
O=`{sed -n 's/^O=//p' /$objtype/mkfile}
cd /sys/src/cmd
mk $O.ls >/dev/null || fail native: mk $O.ls
cmp $O.ls /$objtype/bin/ls || fail native: ls differs
rm -f $O.ls ls.$O
cd rc
mk $O.out >/dev/null || fail native: mk rc
cmp $O.out /$objtype/bin/rc || fail native: rc differs
mk clean >/dev/null
echo boot-test: ok native ls rc
EOF

# ready when cpuwait's listener says 'listen started' on the console
t0=$SECONDS
"${qemu[@]}" -serial "file:$tmp/console" -daemonize -pidfile "$tmp/pid" 2>"$tmp/qemu" ||
	{ cat "$tmp/qemu" >&2; die "qemu failed"; }
until grep -qa 'listen started' "$tmp/console"; do
	[ $((SECONDS - t0)) -lt 300 ] || { tail -20 "$tmp/console" >&2; die "no listener in 300 s"; }
	sleep 1
done
echo "boot-test: $objtype $(basename "$kernel"): listening in $((SECONDS - t0)) s"

dt() { docker exec -e "PORT=$port" -e "PASS=$1" "$ctr" 9front drawterm -G -c "$2" </dev/null 2>&1 | tr -d '\r'; }
dt "$pass" "rc /sys/lib/boot-test.rc $objtype" | tee "$tmp/log" || die "login or native failed"
grep -q '^boot-test: ok native' "$tmp/log" || die "login or native failed"
if dt "$pass" 'echo boot-test: in; exit fail' >"$tmp/log"; then die "status: a failing command succeeded"; fi
# failed for the command, not for want of a session
grep -q '^boot-test: in' "$tmp/log" || { cat "$tmp/log" >&2; die "status: no session"; }
echo "boot-test: ok status"
if dt "not-$pass" 'echo in' >"$tmp/log"; then die "pass: another password got in"; fi
grep -q 'cannot read authenticator' "$tmp/log" || { cat "$tmp/log" >&2; die "pass: not refused"; }
echo "boot-test: ok pass"
touch "$tmp/ok"
