#!/bin/sh
# Mock every command that could change networking or credentials.
set -eu
dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
mkdir "$tmp/bin"
export CALLS="$tmp/calls"
cat > "$tmp/bin/ip" <<'EOF'
#!/bin/sh
printf 'ip' >> "$CALLS"
printf ' <%s>' "$@" >> "$CALLS"
printf '\n' >> "$CALLS"
case "$*" in 'link show eth0') exit "${ETH0_STATUS:-0}" ;; esac
EOF
cat > "$tmp/bin/stat" <<'EOF'
#!/bin/sh
test "$#" -eq 3 && test "$1" = -c && test "$2" = %g && test "$3" = /dev/kvm || exit 1
echo 4242
EOF
cat > "$tmp/bin/setpriv" <<'EOF'
#!/bin/sh
printf 'setpriv' >> "$CALLS"
printf ' <%s>' "$@" >> "$CALLS"
printf '\n' >> "$CALLS"
exit 23
EOF
chmod +x "$tmp/bin/ip" "$tmp/bin/stat" "$tmp/bin/setpriv"
PATH="$tmp/bin:$PATH"
export PATH
fail() { echo "FAIL: $*" >&2; exit 1; }

reject() {
	: > "$CALLS"
	status=0
	sh "$dir/entrypoint" "$@" > "$tmp/out" 2> "$tmp/err" || status=$?
	test "$status" -eq 1 || fail "expected rejection: ${QEMU_USER-unset}"
	test ! -s "$CALLS" || fail 'invalid input reached network/credential commands'
	test ! -s "$tmp/out" || fail 'unexpected standard output'
}
unset QEMU_USER
reject qemu-system-x86_64
grep -q 'QEMU_USER must be numeric uid:gid' "$tmp/err"
for user in '' 1000 : :1000 1000: 1000:1000:1 1000::1 alice:1000 1000:users \
	-1:1000 1000:-1 +1:2 '1:2 ' ' 1:2' '1:2/3' '1:2;id'; do
	QEMU_USER=$user
	export QEMU_USER
	reject qemu-system-x86_64
	grep -q 'QEMU_USER must be numeric uid:gid' "$tmp/err" || fail 'missing diagnostic'
done
QEMU_USER=1001:1002
export QEMU_USER
reject
grep -q '^usage:' "$tmp/err" || fail 'missing command diagnostic'

groups=1002
if [ -c /dev/kvm ]; then groups=1002,4242; fi
for ETH0_STATUS in 0 1; do
	export ETH0_STATUS
	: > "$CALLS"
	status=0
	sh "$dir/entrypoint" qemu-system-x86_64 -append 'a b' '' '*' \
		> "$tmp/out" 2> "$tmp/err" || status=$?
	test "$status" -eq 23 || fail 'command exit status not preserved'
	test ! -s "$tmp/out" && test ! -s "$tmp/err" || fail 'unexpected output'
	{
		echo 'ip <tuntap> <add> <dev> <tap0> <mode> <tap> <user> <1001>'
		echo 'ip <link> <add> <br0> <type> <bridge>'
		echo 'ip <link> <set> <tap0> <master> <br0>'
		echo 'ip <link> <show> <eth0>'
		if [ "$ETH0_STATUS" -eq 0 ]; then
			echo 'ip <addr> <flush> <dev> <eth0>'
			echo 'ip <link> <set> <eth0> <master> <br0>'
		fi
		echo 'ip <link> <set> <tap0> <up>'
		echo 'ip <link> <set> <br0> <up>'
		printf 'setpriv <--reuid> <1001> <--regid> <1002> <--groups> <%s> <-->' "$groups"
		printf ' <qemu-system-x86_64> <-append> <a b> <> <*>\n'
	} > "$tmp/want"
	diff -u "$tmp/want" "$CALLS" || fail "contract (eth0 status $ETH0_STATUS)"
done
echo 'qemu entrypoint tests passed'
