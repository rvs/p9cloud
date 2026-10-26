package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"p9cloud/ndb"
)

func TestShow(t *testing.T) {
	path := testLab(t)
	dir := filepath.Dir(path)
	for _, c := range []struct {
		golden string
		args   []string
	}{
		{"show-host", []string{"show", "host"}},
		{"show-fs", []string{"show", "fs"}},
		{"show-amd", []string{"show", "amd"}},
		{"show-rv", []string{"show", "rv"}},
		{"show-auth", []string{"show", "auth"}},
		{"show-setup-fs", []string{"show", "-setup", "fs"}},
		{"show-setup-fs", []string{"show", "--setup", "fs"}},
		{"show-k-amd", []string{"show", "-k", "nobootprompt=", "-k", "foo=a b", "-k", "x=it's", "-k", "auth=10.99.0.1", "amd", "--", "-s", "-d", "int"}},
	} {
		f := newFake(t)
		if st := f.p9cloud(append([]string{"-f", path}, c.args...)...); st != 0 {
			t.Errorf("%v: status %d: %s", c.args, st, f.err.String())
			continue
		}
		golden(t, c.golden, f.out.String(), dir)
	}
}

func TestDefaultLab(t *testing.T) {
	path := testLab(t)
	t.Chdir(filepath.Dir(path))
	f := newFake(t)
	if st := f.p9cloud("show", "amd"); st != 0 || !strings.HasPrefix(f.out.String(), "sys=amd: p9.lab:27, qemu\n") {
		t.Errorf("status %d: %s%s", st, f.out.String(), f.err.String())
	}
	t.Chdir(t.TempDir())
	f = newFake(t)
	if st := f.p9cloud("list"); st != 1 || !strings.Contains(f.err.String(), "p9.lab: no such file or directory") {
		t.Errorf("no p9.lab: status %d: %s", st, f.err.String())
	}
}

func TestShowElsewhere(t *testing.T) {
	other := filepath.Join(t.TempDir(), "more")
	os.WriteFile(other, []byte("sys=extra ip=10.99.0.20\n\tqemu=q bootf=k ether=525400990020\n"), 0o644)
	path := testLab(t, "\tfile=/lib/ndb/common\n", "\tfile=/lib/ndb/common\n\tfile="+other+"\n")
	f := newFake(t)
	if st := f.p9cloud("-f", path, "show", "extra"); st != 0 || !strings.HasPrefix(f.out.String(), "sys=extra: "+other+":1, qemu\n") {
		t.Errorf("status %d: %q %q", st, f.out.String(), f.err.String())
	}
}

func TestShowErrors(t *testing.T) {
	path := testLab(t)
	for _, c := range []struct {
		args []string
		err  string
	}{
		{[]string{"show", "nope"}, "p9cloud: nope: not one of: fs amd rv auth host\n"},
		{[]string{"show", "-k", "x=$nope", "amd"}, "p9cloud: sys=amd: $nope: no such attribute\n"},
		{[]string{"show", "-setup", "auth"}, "p9cloud: auth is a container: no kernel command line\n"},
	} {
		f := newFake(t)
		if st := f.p9cloud(append([]string{"-f", path}, c.args...)...); st != 1 || f.err.String() != c.err {
			t.Errorf("%v: status %d, %q; want %q", c.args, st, f.err.String(), c.err)
		}
	}
}

func TestUsage(t *testing.T) {
	path := testLab(t)
	for _, c := range []struct {
		args []string
		err  string // in the standard error
	}{
		{nil, "usage: p9cloud [-f lab] command"},
		{[]string{"nope"}, "p9cloud: nope: no such command"},
		{[]string{"-x"}, "usage: p9cloud [-f lab] command"},
		{[]string{"show"}, "usage: p9cloud [-f lab] show [-setup] [-k name=value]... sys [-- arg...]"},
		{[]string{"show", "amd", "fs"}, "usage: p9cloud [-f lab] show"},
		{[]string{"show", "-k", "novalue", "amd"}, "novalue: want name=value"},
		{[]string{"show", "-k", "=v", "amd"}, "=v: want name=value"},
		{[]string{"list", "--", "x"}, "p9cloud: list takes no -- arguments"},
		{[]string{"list", "x"}, "usage: p9cloud [-f lab] list"},
		{[]string{"query", "sys"}, "usage: p9cloud [-f lab] query attr value [rattr...]"},
		{[]string{"wait", "-t", "x", "amd"}, "invalid value"},
		{[]string{"cmd", "fs"}, "usage: p9cloud [-f lab] cmd [-t timeout] sys command..."},
		{[]string{"cpu"}, "usage: p9cloud [-f lab] cpu [-u user] [-r dir] sys [command...]"},
		{[]string{"cpu", "-r"}, "flag needs an argument: -r"},
		{[]string{"provision"}, "usage: p9cloud [-f lab] provision [-t timeout] nvram|user"},
		{[]string{"help", "nope"}, "p9cloud: nope: no such command"},
	} {
		f := newFake(t)
		if st := f.p9cloud(append([]string{"-f", path}, c.args...)...); st != 2 || !strings.Contains(f.err.String(), c.err) {
			t.Errorf("%v: status %d, %q; want 2, %q", c.args, st, f.err.String(), c.err)
		}
		if f.out.String() != "" {
			t.Errorf("%v: output %q", c.args, f.out.String())
		}
	}
	f := newFake(t)
	if st := f.p9cloud("help"); st != 0 || f.err.String() != "" {
		t.Errorf("help: status %d, %q", st, f.err.String())
	}
	for _, c := range []string{"list", "show", "query", "start", "wait", "stop", "cpu", "cmd", "console", "net", "serve", "provision", "help"} {
		if !regexp.MustCompile(`(?m)^  ` + c + ` `).MatchString(f.out.String()) {
			t.Errorf("help does not list %s:\n%s", c, f.out.String())
		}
	}
	f = newFake(t)
	if st := f.p9cloud("help", "wait"); st != 0 || !strings.HasPrefix(f.out.String(), "usage: p9cloud [-f lab] wait [-t timeout] sys\n") {
		t.Errorf("help wait: status %d, %q", st, f.out.String())
	}
	f = newFake(t)
	if st := f.p9cloud("show", "-h"); st != 0 || !strings.HasPrefix(f.out.String(), "usage: p9cloud [-f lab] show") {
		t.Errorf("show -h: status %d, %q %q", st, f.out.String(), f.err.String())
	}
}

func TestWarnings(t *testing.T) {
	path := testLab(t, "tcp=rcpu port=17019\n", "tcp=rcpu port=17019\nlast=line")
	f := newFake(t)
	if st := f.p9cloud("-f", path, "query", "tcp", "rcpu"); st != 0 {
		t.Fatalf("status %d: %s", st, f.err.String())
	}
	if want := "p9cloud: warning: " + path + ":51: no newline at the end: 9front ignores the line\n"; f.err.String() != want {
		t.Errorf("stderr %q, want %q", f.err.String(), want)
	}
}

func TestQuery(t *testing.T) {
	path := testLab(t)
	for _, c := range []struct {
		args []string
		out  string
	}{
		{[]string{"sys", "amd", "ini.nvram", "ini.service"}, "ini.nvram=/dev/sdF0/data ini.service=cpu \n"},
		{[]string{"sys", "amd", "ini.nobootprompt"}, "ini.nobootprompt=\"tcp ether /net/ether0 $ip $ipmask\" \n"},
		{[]string{"ip", "10.99.0.77", "ipmask", "fs"}, "ipmask=255.255.255.0 fs=10.99.0.10 \n"},
		{[]string{"sys", "amd", "@fs"}, "fs=10.99.0.10 \n"},
		{[]string{"sys", "nope", "ip"}, "\n"},
		{[]string{"tcp", "rcpu"}, "\n"}, // no ip=
		// as ndbipinfo: each entry with its networks', concatenated, then deduplicated
		{[]string{"ip", "10.99.0.2", "sys", "image"}, "sys=auth image=9front-auth \n"},
	} {
		f := newFake(t)
		if st := f.p9cloud(append([]string{"-f", path, "query"}, c.args...)...); st != 0 || f.out.String() != c.out {
			t.Errorf("query %v: status %d, %q; want %q", c.args, st, f.out.String(), c.out)
		}
	}
	// as ndbipinfo: each entry with its networks', concatenated, then deduplicated
	f := newFake(t)
	path2 := testLab(t, "sys=auth ip=10.99.0.2", "sys=auth ip=10.99.0.1")
	if st := f.p9cloud("-f", path2, "query", "ip", "10.99.0.1"); st != 0 || f.out.String() != "ip=10.99.0.1 dom=host.p9lab sys=host bridge=p9br state=state http=8082 httpdir=guest drawterm=drawterm terminal=terminal user=roman passwords=secrets/passwords.json ipnet=p9lab ipmask=255.255.255.0 fs=10.99.0.10 auth=10.99.0.10 ntp=10.99.0.10 ini.console=0 ini.sysname=$sys ini.auth=$auth ini.fs=$fs ini.service=cpu dom=auth.p9lab sys=auth container=p9lab-auth image=9front-auth volume=p9lab-auth:/var/lib/9front-auth ready=ticket \n" {
		t.Errorf("query ip 10.99.0.1: status %d, %q", st, f.out.String())
	}
	// a database without a host
	path = testLab(t, "bridge=p9br ", "")
	f = newFake(t)
	if st := f.p9cloud("-f", path, "query", "sys", "rv", "ini.nvram"); st != 0 || f.out.String() != "ini.nvram=/dev/sdF0/data \n" {
		t.Errorf("query without a host: %d %q %q", st, f.out.String(), f.err.String())
	}
}

func TestList(t *testing.T) {
	path := testLab(t)
	f := newFake(t)
	f.netdev(t, "p9br")
	f.reply["docker inspect -f {{.State.Running}} p9lab-fs"] = "true\n"
	f.reply["docker inspect -f {{.State.Running}} p9lab-amd"] = "false\n"
	f.reply["docker inspect -f {{.State.Running}} p9lab-auth"] = "true\n"
	if st := f.p9cloud("-f", path, "list"); st != 0 {
		t.Fatalf("status %d: %s", st, f.err.String())
	}
	want := `SYS   KIND       IP          RUNNING          CONTAINER
host  host       10.99.0.1   network p9br up
fs    qemu       10.99.0.10  yes              p9lab-fs
amd   qemu       10.99.0.11  no               p9lab-amd
rv    qemu       10.99.0.12  no               p9lab-rv
auth  container  10.99.0.2   yes              p9lab-auth

authdom p9lab, auth server 10.99.0.10 (fs)
`
	if got := f.out.String(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}

	// without the network or docker
	f = newFake(t)
	path = testLab(t, "auth=10.99.0.10 ntp", "auth=10.99.0.2 ntp")
	if st := f.p9cloud("-f", path, "list"); st != 0 {
		t.Fatalf("status %d: %s", st, f.err.String())
	}
	for _, w := range []string{
		"host  host       10.99.0.1   network p9br missing\n",
		"auth  container  10.99.0.2   no                    p9lab-auth\n",
		"\nauthdom p9lab, auth server 10.99.0.2 (auth)\n",
	} {
		if !strings.Contains(f.out.String(), w) {
			t.Errorf("got\n%s\nwithout\n%s", f.out.String(), w)
		}
	}
}

// startable makes a lab whose systems can start: the qemu systems'
// files and the lab's network exist.
func startable(t *testing.T, edits ...string) (*fake, string) {
	path := testLab(t, edits...)
	touch(t, filepath.Dir(path), "images/9pc64", "images/9rv64", "images/opensbi.bin",
		"images/fs-os.qcow2", "images/shared.qcow2", "secrets/amd.nvram", "secrets/rv.nvram")
	f := newFake(t)
	f.netdev(t, "p9br")
	return f, path
}

func TestStart(t *testing.T) {
	f, path := startable(t)
	dir := filepath.Dir(path)
	lab, _ := Load(path)
	amd, _ := lab.System("amd")
	touch(t, dir, "state/amd/console.log")
	os.WriteFile(filepath.Join(dir, "state/amd/console.log"), []byte("old boot"), 0o600)
	var mask int
	f.umask = func(m int) int { mask = m; return 0o022 }
	if st := f.p9cloud("-f", path, "start", "amd"); st != 0 {
		t.Fatalf("status %d: %s", st, f.err.String())
	}
	want, _ := amd.Command(false, nil, nil)
	if !reflect.DeepEqual(f.execed, want) || f.execEnv != nil {
		t.Errorf("exec %q %q\nwant %q", f.execed, f.execEnv, want)
	}
	if f.err.String() != shjoin(want)+"\n" {
		t.Errorf("stderr %q", f.err.String())
	}
	if !reflect.DeepEqual(f.ran, [][]string{
		{"docker", "inspect", "-f", "{{.State.Running}}", "p9lab-amd"},
		{"docker", "rm", "-f", "p9lab-amd"},
	}) {
		t.Errorf("ran %q", f.ran)
	}
	if mask != 0o077 {
		t.Errorf("umask %o", mask)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "state/amd/console.log")); err != nil || len(b) != 0 {
		t.Errorf("console.log %q, %v", b, err)
	}

	f, path = startable(t)
	if st := f.p9cloud("-f", path, "start", "-setup", "-k", "x=y", "fs", "--", "-s"); st != 0 {
		t.Fatalf("status %d: %s", st, f.err.String())
	}
	lab, _ = Load(path)
	fs, _ := lab.System("fs")
	want, _ = fs.Command(true, []ndb.Pair{{Attr: "x", Val: "y"}}, []string{"-s"})
	if !reflect.DeepEqual(f.execed, want) {
		t.Errorf("exec %q\nwant %q", f.execed, want)
	}
	if st, err := os.Stat(filepath.Join(filepath.Dir(path), "state/fs")); err != nil || !st.IsDir() {
		t.Errorf("state/fs: %v", err)
	}

	f, path = startable(t)
	if st := f.p9cloud("-f", path, "start", "auth", "--", "-e", "X=1"); st != 0 {
		t.Fatalf("status %d: %s", st, f.err.String())
	}
	if !reflect.DeepEqual(f.ran, [][]string{
		{"docker", "inspect", "-f", "{{.State.Running}}", "p9lab-auth"},
		{"docker", "rm", "-f", "p9lab-auth"},
	}) {
		t.Errorf("ran %q", f.ran)
	}
	if want := "docker run --rm --name p9lab-auth --network p9br --ip 10.99.0.2 -v p9lab-auth:/var/lib/9front-auth -e X=1 9front-auth"; strings.Join(f.execed, " ") != want {
		t.Errorf("exec %q", f.execed)
	}
}

func TestStartDetached(t *testing.T) {
	for _, name := range []string{"amd", "auth"} {
		t.Run(name, func(t *testing.T) {
			f, path := startable(t)
			if st := f.p9cloud("-f", path, "start", "-d", name); st != 0 {
				t.Fatalf("status %d: %s", st, f.err.String())
			}
			lab, _ := Load(path)
			s, _ := lab.System(name)
			command, _ := s.Command(false, nil, nil)
			want := append([]string{"docker", "run", "--detach"}, command[2:]...)
			if !reflect.DeepEqual(f.execed, want) {
				t.Errorf("exec %q\nwant %q", f.execed, want)
			}
		})
	}
}

func TestStartErrors(t *testing.T) {
	f, path := startable(t)
	f.reply["docker inspect -f {{.State.Running}} p9lab-amd"] = "true\n"
	if st := f.p9cloud("-f", path, "start", "amd"); st != 1 || f.err.String() != "p9cloud: amd is already running\n" {
		t.Errorf("running: %d %q", st, f.err.String())
	}
	f, path = startable(t)
	f.reply["docker inspect -f {{.State.Running}} p9lab-auth"] = "true\n"
	if st := f.p9cloud("-f", path, "start", "auth"); st != 1 || f.err.String() != "p9cloud: auth is already running\n" {
		t.Errorf("running: %d %q", st, f.err.String())
	}
	f, path = startable(t)
	os.Remove(filepath.Join(filepath.Dir(path), "images/opensbi.bin"))
	if st := f.p9cloud("-f", path, "start", "rv"); st != 1 || f.err.String() != "p9cloud: rv: "+filepath.Dir(path)+"/images/opensbi.bin: no such file\n" {
		t.Errorf("no file: %d %q", st, f.err.String())
	}
	for _, sys := range []string{"rv", "auth"} {
		f, path = startable(t)
		os.Remove(filepath.Join(f.sysnet, "p9br"))
		if st := f.p9cloud("-f", path, "start", sys); st != 1 || f.err.String() != "p9cloud: "+sys+": no network p9br: /usr/local/bin/p9cloud -f "+path+" net\n" {
			t.Errorf("no network: %d %q", st, f.err.String())
		}
	}
	f, path = startable(t)
	if st := f.p9cloud("-f", path, "start", "host"); st != 1 || f.err.String() != "p9cloud: host: not one of: fs amd rv auth\n" {
		t.Errorf("host: %d %q", st, f.err.String())
	}
	if f.execed != nil {
		t.Errorf("exec %q", f.execed)
	}
}

func listen(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

func closedPort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return p
}

func TestWait(t *testing.T) {
	p1, p2 := listen(t), listen(t)
	path := testLab(t, "sys=fs ip=10.99.0.10", "sys=fs ip=127.0.0.1", "ready=9fs ready=ticket", fmt.Sprintf("ready=%d ready=%d", p1, p2))
	f := newFake(t)
	if st := f.p9cloud("-f", path, "wait", "fs"); st != 0 || f.slept != 0 {
		t.Errorf("status %d, slept %v: %s", st, f.slept, f.err.String())
	}
	p := closedPort(t)
	path = testLab(t, "sys=fs ip=10.99.0.10", "sys=fs ip=127.0.0.1", "ready=9fs ready=ticket", fmt.Sprintf("ready=%d ready=%d", p1, p))
	f = newFake(t)
	if st := f.p9cloud("-f", path, "wait", "-t", "1.2", "fs"); st != 1 || f.err.String() != fmt.Sprintf("p9cloud: fs: nothing on 127.0.0.1:%d after 1.2s\n", p) {
		t.Errorf("status %d: %q", st, f.err.String())
	}
	// tried every half second: at 0, .5, 1 and 1.5, past the deadline
	if f.slept.Seconds() != 1.5 {
		t.Errorf("slept %v, want 1.5s", f.slept)
	}
	f = newFake(t)
	if st := f.p9cloud("-f", path, "wait", "--timeout", "0", "fs"); st != 1 || !strings.HasSuffix(f.err.String(), "after 0s\n") {
		t.Errorf("status %d: %q", st, f.err.String())
	}
}

func TestStop(t *testing.T) {
	path := testLab(t)
	dir := filepath.Dir(path)
	f := newFake(t)
	if st := f.p9cloud("-f", path, "stop", "amd"); st != 0 || f.out.String() != "amd is not running\n" {
		t.Errorf("not running: %d %q %q", st, f.out.String(), f.err.String())
	}

	f = newFake(t)
	stopStates(f, authStopStatus, "running\n", "exited\n")
	if st := f.p9cloud("-f", path, "stop", "auth"); st != 0 {
		t.Errorf("container: %d %q", st, f.err.String())
	}
	if len(f.ran) != 3 || strings.Join(f.ran[1], " ") != "docker stop p9lab-auth" ||
		strings.Join(f.ran[2], " ") != authStopStatus {
		t.Errorf("ran %q", f.ran)
	}

	mon := fakeMonitor(t, filepath.Join(dir, "state/amd/monitor.sock"))
	con := fakeConsole(t, filepath.Join(dir, "state/amd/serial.sock"), func(line string) string {
		if line == "fshalt" {
			return "syncing...\r\ndone halting\r\n"
		}
		return ""
	})
	f = newFake(t)
	stopStates(f, amdStopStatus, "running\n", "running\n", "running\n", "")
	if st := f.p9cloud("-f", path, "stop", "amd"); st != 0 {
		t.Fatalf("status %d: %s", st, f.err.String())
	}
	if len(f.ran) != 4 || strings.Join(f.ran[len(f.ran)-1], " ") != amdStopStatus {
		t.Errorf("ran %q", f.ran)
	}
	if con.String() != "fshalt\n" {
		t.Errorf("console got %q", con.String())
	}
	waitFor(t, "quit", func() bool { return mon.String() == "quit\n" })
	if !strings.Contains(f.out.String(), "done halting") {
		t.Errorf("stdout %q", f.out.String())
	}
	if f.slept != 2500*time.Millisecond {
		t.Errorf("slept %v, want 2.5s", f.slept)
	}
}

func TestStopUnclean(t *testing.T) {
	path := testLab(t)
	dir := filepath.Dir(path)
	mon := fakeMonitor(t, filepath.Join(dir, "state/amd/monitor.sock"))
	f := newFake(t)
	f.reply[amdStopStatus] = "running\n"
	if st := f.p9cloud("-f", path, "stop", "amd"); st != 1 || !strings.HasPrefix(f.err.String(), "p9cloud: amd: serial console: ") ||
		!strings.HasSuffix(f.err.String(), "; is it running?\n") {
		t.Errorf("no console: %d %q", st, f.err.String())
	}
	// done, but not done halting at the start of a line
	fakeConsole(t, filepath.Join(dir, "state/amd/serial.sock"), func(string) string { return "done flushing; not done halting\r\n" })
	f = newFake(t)
	f.reply[amdStopStatus] = "running\n"
	if st := f.p9cloud("-f", path, "stop", "-t", "0.2", "amd"); st != 1 || f.err.String() != "p9cloud: amd did not confirm a clean halt; NOT killing QEMU. Detach other console clients, check the guest.\n" {
		t.Errorf("unclean: %d %q", st, f.err.String())
	}
	if strings.Contains(mon.String(), "quit") || len(f.ran) != 1 {
		t.Errorf("quit QEMU without a clean halt: ran %q", f.ran)
	}
}

func writePasswords(t *testing.T, path string, pw map[string]string) {
	b, _ := json.Marshal(pw)
	touch(t, filepath.Dir(path), "secrets/passwords.json")
	os.WriteFile(filepath.Join(filepath.Dir(path), "secrets/passwords.json"), b, 0o600)
}

func TestCpu(t *testing.T) {
	path := testLab(t, "drawterm=drawterm", "drawterm=bin/drawterm")
	dir := filepath.Dir(path)
	writePasswords(t, path, map[string]string{"host": "hpw", "roman": "rpw"})
	f := newFake(t)
	if st := f.p9cloud("-f", path, "cpu", "amd"); st != 0 {
		t.Fatalf("status %d: %s", st, f.err.String())
	}
	want := []string{dir + "/bin/drawterm", "-G", "-h", "tcp!10.99.0.11!17019", "-a", "tcp!10.99.0.10!567", "-u", "roman", "-r", dir + "/terminal"}
	if !reflect.DeepEqual(f.execed, want) || !reflect.DeepEqual(f.execEnv, []string{"PASS=rpw"}) {
		t.Errorf("exec %q %q", f.execed, f.execEnv)
	}
	if st, err := os.Stat(dir + "/terminal"); err != nil || !st.IsDir() {
		t.Errorf("terminal: %v", err)
	}
	f = newFake(t)
	if st := f.p9cloud("-f", path, "cpu", "-u", "glenda", "rv", "ls", "-l", "/mnt/term"); st != 0 {
		t.Fatalf("status %d: %s", st, f.err.String())
	}
	if got := strings.Join(f.execed[1:], " "); got != "-G -h tcp!10.99.0.12!17019 -a tcp!10.99.0.10!567 -u glenda -r "+dir+"/terminal -c ls -l /mnt/term" ||
		!reflect.DeepEqual(f.execEnv, []string{"PASS=hpw"}) {
		t.Errorf("exec %q %q", f.execed, f.execEnv)
	}

	// no user=: $USER
	path = testLab(t, "user=roman ", "")
	writePasswords(t, path, map[string]string{"alice": "apw"})
	f = newFake(t)
	f.getenv = func(v string) string { return map[string]string{"USER": "alice"}[v] }
	if st := f.p9cloud("-f", path, "cpu", "--user=", "amd"); st != 0 || f.execed[0] != "drawterm" || f.execed[7] != "alice" {
		t.Errorf("status %d %q: %s", st, f.execed, f.err.String())
	}
	f = newFake(t)
	if st := f.p9cloud("-f", path, "cpu", "-u", "bob", "amd"); st != 1 || !strings.HasSuffix(f.err.String(), "passwords.json: no password for bob\n") {
		t.Errorf("no password: %d %q", st, f.err.String())
	}
	f = newFake(t)
	if st := f.p9cloud("-f", path, "cpu", "auth"); st != 1 || f.err.String() != "p9cloud: auth: not one of: fs amd rv\n" {
		t.Errorf("container: %d %q", st, f.err.String())
	}
}

func TestCpuRoot(t *testing.T) {
	for _, kind := range []string{"default", "empty", "relative", "absolute", "dot"} {
		t.Run(kind, func(t *testing.T) {
			path := testLab(t, "terminal=terminal", "terminal=bootstrap-root")
			dir := filepath.Dir(path)
			writePasswords(t, path, map[string]string{"host": "hpw"})
			// Relative roots belong to the lab, not the invocation's directory.
			t.Chdir(t.TempDir())
			args := []string{"-f", path, "cpu", "-u", "glenda"}
			root, wantRoot := "", filepath.Join(dir, "bootstrap-root")
			switch kind {
			case "relative":
				root, wantRoot = "check inputs", filepath.Join(dir, "check inputs")
			case "absolute":
				root = filepath.Join(t.TempDir(), "checks")
				wantRoot = root
			case "dot":
				root, wantRoot = ".", dir
			}
			if kind != "default" {
				args = append(args, "-r", root)
			}
			args = append(args, "amd", "/mnt/term/check", "kernel hash")
			f := newFake(t)
			if st := f.p9cloud(args...); st != 0 {
				t.Fatalf("status %d: %s", st, f.err.String())
			}
			want := []string{"drawterm", "-G", "-h", "tcp!10.99.0.11!17019",
				"-a", "tcp!10.99.0.10!567", "-u", "glenda", "-r", wantRoot,
				"-c", "/mnt/term/check kernel hash"}
			if !reflect.DeepEqual(f.execed, want) || !reflect.DeepEqual(f.execEnv, []string{"PASS=hpw"}) {
				t.Errorf("exec %q %q, want %q", f.execed, f.execEnv, want)
			}
			if st, err := os.Stat(wantRoot); err != nil || !st.IsDir() {
				t.Errorf("export root not created: %v", err)
			}
			if root != "" {
				if _, err := os.Stat(filepath.Join(dir, "bootstrap-root")); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("override created default export root: %v", err)
				}
			}
		})
	}
}

func TestCpuRootError(t *testing.T) {
	path := testLab(t)
	writePasswords(t, path, map[string]string{"roman": "rpw"})
	touch(t, filepath.Dir(path), "not-a-directory")
	f := newFake(t)
	if st := f.p9cloud("-f", path, "cpu", "-r", "not-a-directory", "amd"); st != 1 ||
		!strings.Contains(f.err.String(), "not-a-directory") || f.execed != nil {
		t.Fatalf("status %d, exec %q: %q", st, f.execed, f.err.String())
	}
}

func TestCpuRootHelp(t *testing.T) {
	f := newFake(t)
	if st := f.p9cloud("help", "cpu"); st != 0 ||
		!strings.Contains(f.out.String(), "cpu [-u user] [-r dir] sys [command...]") ||
		!strings.Contains(f.out.String(), "-r dir") ||
		!strings.Contains(f.out.String(), "relative to the lab file") {
		t.Fatalf("status %d: %q %q", st, f.out.String(), f.err.String())
	}
}

var markerRE = regexp.MustCompile(`; result=\$status; echo; echo (DONE[0-9a-f]{16}) \$result$`)

// shell answers a command typed at a fake console.
func shell(t *testing.T, out map[string]string, status map[string]string) func(string) string {
	return func(line string) string {
		m := markerRE.FindStringSubmatchIndex(line)
		if m == nil {
			t.Errorf("console got %q", line)
			return ""
		}
		cmd, marker := line[:m[0]], line[m[2]:m[3]]
		return out[cmd] + "\r\n" + marker + " " + status[cmd] + "\r\nfs# "
	}
}

func TestCmd(t *testing.T) {
	path := testLab(t)
	dir := filepath.Dir(path)
	con := fakeConsole(t, filepath.Join(dir, "state/fs/serial.sock"), shell(t,
		map[string]string{"cat /dev/sysname": "fs", "echo a b": "a b\r\n", "nope": "nope: not found\r\n"},
		map[string]string{"nope": "nope:file does not exist"}))
	f := newFake(t)
	f.typing = time.Millisecond
	if st := f.p9cloud("-f", path, "cmd", "fs", "cat /dev/sysname"); st != 0 || f.out.String() != "fs\n" {
		t.Errorf("cat: %d %q %q", st, f.out.String(), f.err.String())
	}
	if n := len(con.String()); f.slept != time.Duration(n)*time.Millisecond {
		t.Errorf("slept %v typing %d bytes", f.slept, n)
	}
	f = newFake(t)
	if st := f.p9cloud("-f", path, "cmd", "fs", "echo", "a", "b"); st != 0 || f.out.String() != "a b\n\n" {
		t.Errorf("echo: %d %q %q", st, f.out.String(), f.err.String())
	}
	f = newFake(t)
	if st := f.p9cloud("-f", path, "cmd", "fs", "nope"); st != 1 || f.out.String() != "nope: not found\n\n" || f.err.String() != "p9cloud: fs: status nope:file does not exist\n" {
		t.Errorf("nope: %d %q %q", st, f.out.String(), f.err.String())
	}
	if n := strings.Count(con.String(), "\n"); n != 3 {
		t.Errorf("console got %d lines: %q", n, con.String())
	}

	// a console that echoes but does not run commands
	fakeConsole(t, filepath.Join(dir, "state/amd/serial.sock"), func(string) string { return "" })
	f = newFake(t)
	if st := f.p9cloud("-f", path, "cmd", "-t", "0.2", "amd", "x"); st != 1 || !strings.HasPrefix(f.err.String(), "p9cloud: amd: timed out: x; result=$status") {
		t.Errorf("timeout: %d %q", st, f.err.String())
	}
	// a silent one
	rawSocket(t, filepath.Join(dir, "state/rv/serial.sock"), func(c net.Conn) { io.Copy(io.Discard, c) })
	f = newFake(t)
	if st := f.p9cloud("-f", path, "cmd", "-t", "0.2", "rv", "x"); st != 1 || f.err.String() != "p9cloud: rv: no output: \n" {
		t.Errorf("silent: %d %q", st, f.err.String())
	}
	f = newFake(t)
	if st := f.p9cloud("-f", path, "cmd", "auth", "x"); st != 1 || f.err.String() != "p9cloud: auth: not one of: fs amd rv\n" {
		t.Errorf("container: %d %q", st, f.err.String())
	}
}

func TestConsole(t *testing.T) {
	path := testLab(t)
	dir := filepath.Dir(path)
	con := fakeConsole(t, filepath.Join(dir, "state/fs/serial.sock"), func(line string) string { return "ok\r\n" })
	f := newFake(t)
	r, w := io.Pipe()
	f.stdin = r
	done := make(chan int)
	go func() { done <- f.p9cloud("-f", path, "console", "fs") }()
	io.WriteString(w, "hello\n")
	waitFor(t, "the echo", func() bool { return strings.Contains(f.out.String(), "hello\n\rok\r\n") })
	w.Close()
	if st := <-done; st != 0 || f.err.String() != "" {
		t.Errorf("status %d: %q", st, f.err.String())
	}

	// a terminal: raw, and Ctrl-] detaches
	f = newFake(t)
	r, w = io.Pipe()
	f.stdin = r
	f.rawtty = func() (func(), error) { f.raw = true; return func() { f.restored = true }, nil }
	go func() { done <- f.p9cloud("-f", path, "console", "fs") }()
	io.WriteString(w, "a\n")
	waitFor(t, "the echo", func() bool { return strings.Contains(f.out.String(), "a\n\rok") })
	io.WriteString(w, "b\x1dc")
	if st := <-done; st != 0 || !f.raw || !f.restored {
		t.Errorf("status %d, raw %v, restored %v", st, f.raw, f.restored)
	}
	waitFor(t, "b", func() bool { return strings.HasSuffix(con.String(), "a\nb") })
	time.Sleep(20 * time.Millisecond)
	if got := con.String(); got != "hello\na\nb" {
		t.Errorf("the guest got %q: Ctrl-] and what follows reached it", got)
	}
	if want := "fs console: Enter for a prompt, Ctrl-] detaches; one client at a time.\r\n\n"; f.err.String() != want {
		t.Errorf("stderr %q, want %q", f.err.String(), want)
	}
	w.Close()

	// the guest hangs up
	rawSocket(t, filepath.Join(dir, "state/rv/serial.sock"), func(c net.Conn) { io.WriteString(c, "bye\r\n") })
	f = newFake(t)
	r, w = io.Pipe()
	defer w.Close()
	f.stdin = r
	if st := f.p9cloud("-f", path, "console", "rv"); st != 0 || f.out.String() != "bye\r\n" {
		t.Errorf("hang up: %d %q %q", st, f.out.String(), f.err.String())
	}

	f = newFake(t)
	if st := f.p9cloud("-f", path, "console", "auth"); st != 0 || strings.Join(f.execed, " ") != "docker logs -f p9lab-auth" {
		t.Errorf("container: %d %q", st, f.execed)
	}
	f = newFake(t)
	if st := f.p9cloud("-f", path, "console", "amd"); st != 1 || !strings.HasSuffix(f.err.String(), "; is it running?\n") {
		t.Errorf("not running: %d %q", st, f.err.String())
	}
}

func TestNet(t *testing.T) {
	path := testLab(t)
	inspect := "docker network inspect -f {{range .IPAM.Config}}{{.Subnet}} {{.Gateway}}{{end}} p9br"
	create := "docker network create --internal --subnet 10.99.0.0/24 --gateway 10.99.0.1 -o com.docker.network.bridge.name=p9br p9br"
	f := newFake(t)
	f.fail[inspect] = errFake
	if st := f.p9cloud("-f", path, "net", "-n"); st != 0 || f.out.String() != create+"\n" || len(f.ran) != 1 {
		t.Errorf("net -n: %d %q %q, ran %q", st, f.out.String(), f.err.String(), f.ran)
	}
	f = newFake(t)
	f.fail[inspect] = errFake
	f.reply[create] = "8770c26b3612\n"
	if st := f.p9cloud("-f", path, "net"); st != 0 || f.out.String() != create+"\n" ||
		len(f.ran) != 2 || strings.Join(f.ran[1], " ") != create {
		t.Errorf("net: %d %q %q, ran %q", st, f.out.String(), f.err.String(), f.ran)
	}
	f = newFake(t)
	f.fail[inspect] = errFake
	f.fail[create] = errFake
	if st := f.p9cloud("-f", path, "net"); st != 1 || f.err.String() != "p9cloud: "+create+": exit status 1\n" {
		t.Errorf("failing: %d %q", st, f.err.String())
	}

	// there already
	f = newFake(t)
	f.reply[inspect] = "10.99.0.0/24 10.99.0.1\n"
	if st := f.p9cloud("-f", path, "net"); st != 0 || f.out.String() != "" || len(f.ran) != 1 {
		t.Errorf("there: %d %q %q, ran %q", st, f.out.String(), f.err.String(), f.ran)
	}
	f = newFake(t)
	f.reply[inspect] = "10.99.0.0/16 10.99.0.254\n"
	if st := f.p9cloud("-f", path, "net"); st != 1 || f.err.String() != "p9cloud: docker network p9br: 10.99.0.0/16 10.99.0.254; the lab wants 10.99.0.0/24 10.99.0.1\n" {
		t.Errorf("another: %d %q", st, f.err.String())
	}
	// a device of the name that docker does not know: the host's own
	f = newFake(t)
	f.netdev(t, "p9br")
	f.fail[inspect] = errFake
	if st := f.p9cloud("-f", path, "net", "-n"); st != 1 || f.err.String() != "p9cloud: p9br: a network device, but not a docker network\n" {
		t.Errorf("device: %d %q", st, f.err.String())
	}

	// the host's network: ipmask=, else ndbipinfo's default, the
	// address's class
	for _, c := range []struct{ old, new, out string }{
		{"sys=host ip=10.99.0.1 ", "sys=host ip=10.99.0.1 ipmask=255.255.0.0 ", "--subnet 10.99.0.0/16 --gateway 10.99.0.1 "},
		{"ipnet=p9lab ip=10.99.0.0", "ipnet=p9lab ip=10.98.0.0", "--subnet 10.0.0.0/8 --gateway 10.99.0.1 "},
	} {
		f = newFake(t)
		f.fail[inspect] = errFake
		if st := f.p9cloud("-f", testLab(t, c.old, c.new), "net", "-n"); st != 0 || !strings.Contains(f.out.String(), c.out) {
			t.Errorf("%s: %d %q %q", c.new, st, f.out.String(), f.err.String())
		}
	}
	for _, c := range []struct{ old, new, err string }{
		{"sys=host ip=10.99.0.1 ", "sys=host ip=nope ", "p9cloud: sys=host: ip=nope: not an IPv4 address\n"},
		{"sys=host ip=10.99.0.1 ", "sys=host ip=fd00::1 ", "p9cloud: sys=host: ip=fd00::1: not an IPv4 address\n"},
		{"sys=host ip=10.99.0.1 ", "sys=host ", "p9cloud: sys=host: no ip= in $LAB\n"},
		{"bridge=p9br ", "bridge= ", "p9cloud: sys=host: no bridge= in $LAB\n"},
		{"sys=host ip=10.99.0.1 ", "sys=host ip=10.99.0.1 ipmask=nope ", "p9cloud: sys=host: ipmask=nope: not an IPv4 mask\n"},
	} {
		f = newFake(t)
		path := testLab(t, c.old, c.new)
		if st := f.p9cloud("-f", path, "net"); st != 1 || f.err.String() != strings.ReplaceAll(c.err, "$LAB", path) {
			t.Errorf("%s: %d %q", c.new, st, f.err.String())
		}
	}
}

func TestServe(t *testing.T) {
	path := testLab(t)
	dir := filepath.Dir(path)
	touch(t, dir, "guest/configure-cpu.rc")
	os.WriteFile(filepath.Join(dir, "guest/configure-cpu.rc"), []byte("echo hi\n"), 0o644)
	f := newFake(t)
	if st := f.p9cloud("-f", path, "serve"); st != 0 || f.served != "10.99.0.1:8082" {
		t.Fatalf("status %d, served %q: %s", st, f.served, f.err.String())
	}
	if f.err.String() != "serving "+dir+"/guest on http://10.99.0.1:8082/\n" {
		t.Errorf("stderr %q", f.err.String())
	}
	srv := httptest.NewServer(f.handler)
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL + "/configure-cpu.rc")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "echo hi\n" {
		t.Errorf("got %q", b)
	}
	path = testLab(t, "http=8082 httpdir=guest", "http=9000")
	f = newFake(t)
	if st := f.p9cloud("-f", path, "serve"); st != 1 || f.err.String() != "p9cloud: sys=host: no httpdir= in "+path+"\n" {
		t.Errorf("no httpdir: %d %q", st, f.err.String())
	}
	path = testLab(t, "http=8082 ", "")
	f = newFake(t)
	if st := f.p9cloud("-f", path, "serve"); st != 0 || f.served != "10.99.0.1:8082" {
		t.Errorf("default port: %d %q", st, f.served)
	}
	path = testLab(t, "http=8082 ", "http=x ")
	f = newFake(t)
	if st := f.p9cloud("-f", path, "serve"); st != 1 || f.err.String() != "p9cloud: sys=host: http=x: not a port\n" {
		t.Errorf("bad port: %d %q", st, f.err.String())
	}
}

// prompter answers a command typed at a fake console with prompts,
// one after each line typed, and records the answers.
func prompter(command string, prompts []string, answers *recorder) func(string) string {
	n := -1
	return func(line string) string {
		if n < 0 {
			if line != command {
				return "rc: " + line + ": not found\r\nfs# "
			}
			n = 0
			return prompts[0]
		}
		answers.add(line)
		if n++; n < len(prompts) {
			return prompts[n]
		}
		return "fs# "
	}
}

func readPasswords(t *testing.T, path string) map[string]string {
	var pw map[string]string
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "secrets/passwords.json"))
	if err := json.Unmarshal(b, &pw); err != nil {
		t.Fatal(err)
	}
	return pw
}

func TestProvisionQemu(t *testing.T) {
	path := testLab(t)
	dir := filepath.Dir(path)
	answers := &recorder{}
	fakeConsole(t, filepath.Join(dir, "state/fs/serial.sock"), prompter("auth/wrkey",
		[]string{"authid: ", "authdom: ", "secstore key: ", "password: ", "confirm password: ", "enable legacy p9sk1[no]: "},
		answers))
	f := newFake(t)
	if st := f.p9cloud("-f", path, "provision", "nvram"); st != 0 || f.out.String() != "fs: auth/wrkey done\n" {
		t.Fatalf("status %d: %q %q", st, f.out.String(), f.err.String())
	}
	pw := readPasswords(t, path)["host"]
	if want := []string{"glenda", "p9lab", "", pw, pw, "no"}; !reflect.DeepEqual(answers.wait(t, 6), want) {
		t.Errorf("answers %q, want %q", answers.get(), want)
	}

	path = testLab(t)
	dir = filepath.Dir(path)
	writePasswords(t, path, map[string]string{"host": "hpw"})
	answers = &recorder{}
	fakeConsole(t, filepath.Join(dir, "state/fs/serial.sock"), prompter("auth/changeuser roman",
		[]string{"Password: ", "Confirm password: ", "assign new Inferno/POP secret? [y/n]: ",
			"Expiration date (YYYYMMDD or never)[never]: ", "Post id: ", "User's full name: ",
			"department #: ", "User's email address: ", "SPONSOR'S EMAIL ADDRESS: "}, // case does not matter
		answers))
	f = newFake(t)
	if st := f.p9cloud("-f", path, "provision", "roman"); st != 0 || f.out.String() != "fs: auth/changeuser roman done\n" {
		t.Fatalf("status %d: %q %q", st, f.out.String(), f.err.String())
	}
	pws := readPasswords(t, path)
	if want := []string{pws["roman"], pws["roman"], "n", "never", "", "roman", "", "", ""}; !reflect.DeepEqual(answers.wait(t, 9), want) || pws["host"] != "hpw" {
		t.Errorf("answers %q, want %q; passwords %q", answers.get(), want, pws)
	}

	// a console without the program
	path = testLab(t)
	fakeConsole(t, filepath.Join(filepath.Dir(path), "state/fs/serial.sock"), prompter("x", nil, &recorder{}))
	f = newFake(t)
	if st := f.p9cloud("-f", path, "provision", "-t", "0.2", "nvram"); st != 1 || !strings.HasPrefix(f.err.String(), `p9cloud: fs: no prompt "authid": timed out: auth/wrkey`) {
		t.Errorf("no program: %d %q", st, f.err.String())
	}
}

func TestProvisionContainer(t *testing.T) {
	path := testLab(t, "auth=10.99.0.10 ntp", "auth=10.99.0.2 ntp")
	run := "docker run --rm -e PASS -v p9lab-auth:/var/lib/9front-auth 9front-auth user roman"
	f := newFake(t)
	f.reply[run] = "roman: key written\n"
	if st := f.p9cloud("-f", path, "provision", "roman"); st != 0 || f.out.String() != "roman: key written\n" {
		t.Fatalf("status %d: %q %q", st, f.out.String(), f.err.String())
	}
	pw := readPasswords(t, path)["roman"]
	if len(f.ran) != 1 || strings.Join(f.ran[0], " ") != run || !reflect.DeepEqual(f.ranEnv[0], []string{"PASS=" + pw}) {
		t.Errorf("ran %q %q", f.ran, f.ranEnv)
	}
	f = newFake(t)
	f.fail[run] = errFake
	if st := f.p9cloud("-f", path, "provision", "roman"); st != 1 || f.err.String() != "p9cloud: "+run+": exit status 1\n" {
		t.Errorf("failing: %d %q", st, f.err.String())
	}
}

func TestProvisionContainerNvram(t *testing.T) {
	const (
		docker = "docker run --rm -v p9lab-auth:/var/lib/9front-auth 9front-auth "
		list   = docker + "list"
		init   = "docker run --rm -e PASS -v p9lab-auth:/var/lib/9front-auth 9front-auth init glenda p9lab"
		nvram  = docker + "nvram glenda"
	)
	noDomain := errors.New("exit status 1: 9front-auth: no domain in /var/lib/9front-auth: run init or import")
	key := strings.Repeat("k", 512)
	lab := func(edits ...string) (string, *fake) {
		path := testLab(t, append([]string{"auth=10.99.0.10 ntp", "auth=10.99.0.2 ntp"}, edits...)...)
		f := newFake(t)
		f.reply[nvram] = key
		return path, f
	}
	ran := func(f *fake) string {
		var s []string
		for _, r := range f.ran {
			s = append(s, strings.Join(r, " "))
		}
		return strings.Join(s, "\n")
	}
	written := func(path string, names ...string) {
		t.Helper()
		for _, n := range names {
			fi, err := os.Stat(filepath.Join(filepath.Dir(path), n))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(filepath.Join(filepath.Dir(path), n))
			if string(b) != key || fi.Mode().Perm() != 0o600 {
				t.Errorf("%s: %d bytes, mode %v", n, len(b), fi.Mode())
			}
		}
	}

	// a new domain, keyed with the host owner's password
	path, f := lab()
	f.fail[list] = noDomain
	f.reply[init] = "authdom p9lab, host owner glenda\n"
	if st := f.p9cloud("-f", path, "provision", "nvram"); st != 0 ||
		f.out.String() != "authdom p9lab, host owner glenda\namd: secrets/amd.nvram\nrv: secrets/rv.nvram\n" {
		t.Fatalf("status %d: %q %q", st, f.out.String(), f.err.String())
	}
	if got, want := ran(f), list+"\n"+init+"\n"+nvram; got != want {
		t.Errorf("ran\n%s\nwant\n%s", got, want)
	}
	if pw := readPasswords(t, path)["host"]; pw == "" || !reflect.DeepEqual(f.ranEnv[1], []string{"PASS=" + pw}) {
		t.Errorf("init env %q, password %q", f.ranEnv[1], pw)
	}
	written(path, "secrets/amd.nvram", "secrets/rv.nvram")

	// the domain is there: only the nvram files are written again
	path, f = lab("disk=secrets/rv.nvram", "disk=nv/rv.nvram")
	touch(t, filepath.Dir(path), "secrets/amd.nvram")
	os.Chmod(filepath.Join(filepath.Dir(path), "secrets/amd.nvram"), 0o644)
	f.reply[list] = "authdom p9lab, host owner glenda\nglenda                      ok       never    dp9ik\n"
	if st := f.p9cloud("-f", path, "provision", "nvram"); st != 0 || f.out.String() != "amd: secrets/amd.nvram\nrv: nv/rv.nvram\n" {
		t.Fatalf("status %d: %q %q", st, f.out.String(), f.err.String())
	}
	if got, want := ran(f), list+"\n"+nvram; got != want {
		t.Errorf("ran\n%s\nwant\n%s", got, want)
	}
	written(path, "secrets/amd.nvram", "nv/rv.nvram")

	// another domain
	for _, out := range []string{"authdom other, host owner glenda\n", "authdom p9lab, host owner bootes\n", "garbage\n"} {
		path, f = lab()
		f.reply[list] = out
		if st := f.p9cloud("-f", path, "provision", "nvram"); st != 1 || f.err.String() !=
			"p9cloud: auth: the keys say \""+strings.TrimSpace(out)+"\"; the lab wants authdom p9lab, host owner glenda\n" {
			t.Errorf("%q: %d %q", out, st, f.err.String())
		}
		if len(f.ran) != 1 {
			t.Errorf("ran %q", f.ran)
		}
	}

	// failures
	for _, c := range []struct {
		fail, reply map[string]string
		err         string
	}{
		{map[string]string{list: "exit status 125"}, nil, "p9cloud: " + list + ": exit status 125\n"},
		{map[string]string{list: noDomain.Error(), init: "exit status 1"}, nil, "p9cloud: " + init + ": exit status 1\n"},
		{map[string]string{nvram: "exit status 1"}, map[string]string{list: "authdom p9lab, host owner glenda\n"}, "p9cloud: " + nvram + ": exit status 1\n"},
		{nil, map[string]string{list: "authdom p9lab, host owner glenda\n", nvram: "short"}, "p9cloud: " + nvram + ": 5 bytes, want 512\n"},
	} {
		path, f = lab()
		for k, v := range c.fail {
			f.fail[k] = errors.New(v)
		}
		for k, v := range c.reply {
			f.reply[k] = v
		}
		if st := f.p9cloud("-f", path, "provision", "nvram"); st != 1 || f.err.String() != c.err {
			t.Errorf("%v: %d %q, want %q", c.fail, st, f.err.String(), c.err)
		}
	}
	path, f = lab()
	f.reply[list] = "authdom p9lab, host owner glenda\n"
	os.WriteFile(filepath.Join(filepath.Dir(path), "secrets"), nil, 0o644)
	if st := f.p9cloud("-f", path, "provision", "nvram"); st != 1 || !strings.Contains(f.err.String(), "secrets: not a directory") {
		t.Errorf("unwritable: %d %q", st, f.err.String())
	}
}

func TestRunErrors(t *testing.T) {
	// a command that fails reports its words and status
	path := testLab(t)
	f := newFake(t)
	f.reply[authStopStatus] = "running\n"
	f.fail["docker stop p9lab-auth"] = errFake
	if st := f.p9cloud("-f", path, "stop", "auth"); st != 1 || f.err.String() != "p9cloud: docker stop p9lab-auth: exit status 1\n" {
		t.Errorf("%d %q", st, f.err.String())
	}
}
