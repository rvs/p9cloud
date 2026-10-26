#!/bin/sh
# make test: the commands on a scratch key directory, and speaksfor.
# The protocol is tested against 9front itself (see README).
set -u
B=${1:-./9front-auth}
D=$(mktemp -d)
trap 'kill $SRV 2>/dev/null; rm -rf "$D"' EXIT
SRV=
A="$B -d $D/k"
fails=0
ok()	{ if "$@" >$D/out 2>&1; then :; else echo "FAIL: $*"; cat $D/out; fails=$((fails+1)); fi; }
no()	{ if "$@" >$D/out 2>&1; then echo "FAIL (should fail): $*"; fails=$((fails+1)); fi; }
has()	{ grep -q "$1" $D/out || { echo "FAIL: no '$1' in output of last command"; cat $D/out; fails=$((fails+1)); }; }

no $A list;					has 'no domain'
ok $A init -r glenda test
no $A init -r glenda test;			has 'already has a domain'
ok env PASS=roman-pw-1 $A user roman
no env PASS=short $A user bob;			has 'at least 8'
ok sh -c "echo alice-pw-1 | $A user -l alice"
ok $A user -r cpu1
no env PASS=x-pw-12345 $A user 'bad name'
ok $A expire alice 20301231
ok $A disable alice
no $A disable glenda;				has 'host owner'
no $A remove glenda;				has 'host owner'
no $A expire roman 2030;			has 'bad date'
ok $A list
has 'glenda  *ok  *never  *dp9ik$'
has 'alice  *disabled 20301231 dp9ik p9sk1'
has 'cpu1  *ok  *never  *dp9ik$'
ok $A remove cpu1
ok $A list
grep -q cpu1 $D/out && { echo "FAIL: cpu1 not removed"; fails=$((fails+1)); }
ok $A speaksfor cpu2 '!sys' '*'
ok $A speaksfor glenda roman
ok $A speaksfor
has 'hostid=cpu2'
has 'uid=roman'
no $A nvram nobody
no $A nvram -l glenda;				has 'no p9sk1 key'
ok sh -c "$A nvram glenda > $D/nvram"
test $(wc -c < $D/nvram) = 512 || { echo "FAIL: nvram size"; fails=$((fails+1)); }
cmp -s $D/nvram $D/k/nvram || { echo "FAIL: nvram glenda differs from the server's"; fails=$((fails+1)); }
ok sh -c "$A export > $D/export"
ok sh -c "$B -d $D/k2 import < $D/export"
cmp -s $D/k/keys $D/k2/keys && cmp -s $D/k/nvram $D/k2/nvram || { echo "FAIL: export | import"; fails=$((fails+1)); }
ok $B -d $D/k3 import $D/k/nvram $D/k/keys $D/k/ndb
cmp -s $D/k/ndb $D/k3/ndb || { echo "FAIL: import ndb"; fails=$((fails+1)); }
head -c 100 $D/export > $D/short
no sh -c "$B -d $D/k4 import < $D/short"

# a server and its health check
port=$((20000 + $$ % 20000))
$A serve -a "tcp!127.0.0.1!$port" 2>$D/log & SRV=$!
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
	$A health "tcp!127.0.0.1!$port" 2>/dev/null && break
	sleep 0.25
done
ok $A health "tcp!127.0.0.1!$port"
kill $SRV; wait $SRV 2>/dev/null; SRV=
no $A health "tcp!127.0.0.1!$port"

printf 'hostid=glenda\n\tuid=!sys uid=!adm uid=*\n# comment\nhostid=cpu1 uid=bob\n\tuid=carol\nhostid=cpu1\n\tuid=*\nauth=x hostid=cpu2\n\tuid=!root uid=*\n' > $D/k/ndb
ok tests/speaksfor $D/k \
	glenda roman 1  glenda sys 0  glenda adm 0  glenda glenda 1 \
	cpu1 bob 1  cpu1 carol 1  cpu1 dave 0 \
	cpu2 x 1  cpu2 root 0  nobody roman 0  roman roman 1

[ $fails = 0 ] && echo PASS || { echo "$fails FAILED"; exit 1; }
