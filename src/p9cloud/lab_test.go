package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"p9cloud/ndb"
)

func TestLoad(t *testing.T) {
	lab := loadLab(t)
	if got := names(lab.Systems); !reflect.DeepEqual(got, []string{"fs", "amd", "rv", "auth"}) {
		t.Errorf("systems %v", got)
	}
	if lab.Host.Name != "host" || lab.Prefix != "p9lab" {
		t.Errorf("host %s, prefix %s", lab.Host.Name, lab.Prefix)
	}
	if want := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()); lab.User != want {
		t.Errorf("user %s, want %s", lab.User, want)
	}
	for name, kind := range map[string]string{"fs": "qemu", "rv": "qemu", "auth": "container", "host": ""} {
		s, err := lab.System(name, "qemu", "container", "host")
		if err != nil || s.Kind != kind {
			t.Errorf("%s: %v, kind %q, want %q", name, err, s.Kind, kind)
		}
	}
	if !filepath.IsAbs(lab.Path) || lab.Dir != filepath.Dir(lab.Path) {
		t.Errorf("path %s, dir %s", lab.Path, lab.Dir)
	}
	if s, _ := lab.System("amd"); s.Entry.Line != 27 || s.Entry.File != lab.Path {
		t.Errorf("amd at %s:%d", s.Entry.File, s.Entry.Line)
	}
}

func TestLoadVariants(t *testing.T) {
	// no ipnet for the host: the prefix is lab
	lab := loadLab(t, "ipnet=p9lab ip=", "ipnet=x9lab ip=", "10.99.0.0", "10.98.0.0")
	if lab.Prefix != "lab" {
		t.Errorf("prefix %q, want lab", lab.Prefix)
	}
	// a system without ip= is still a system, without inheritance
	lab = loadLab(t, "sys=rv ip=10.99.0.12 ", "sys=rv ")
	rv, err := lab.System("rv")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := rv.Get("qemu"); !ok || v != "qemu-system-riscv64" {
		t.Errorf("rv qemu= %q", v)
	}
	if _, ok := rv.Get("ipnet"); ok {
		t.Error("rv without ip= inherited from the ipnet")
	}
	// repeated sys= entries are one system, merged
	lab = loadLab(t, "tcp=9fs", "sys=amd\n\tqemuarg=-s\n\ntcp=9fs")
	if got := names(lab.Systems); !reflect.DeepEqual(got, []string{"fs", "amd", "rv", "auth"}) {
		t.Errorf("systems %v", got)
	}
	// entries without qemu= or container= are not systems
	lab = loadLab(t, "tcp=9fs", "sys=printer ip=10.99.0.50\n\ntcp=9fs")
	if _, err := lab.System("printer"); err == nil || err.Error() != "printer: not one of: fs amd rv auth" {
		t.Errorf("printer: %v", err)
	}
	if _, err := lab.System("host"); err == nil {
		t.Error("host is a system without kinds host")
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "p9.lab")); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("missing lab: %v", err)
	}
	_, err := Load(testLab(t, "bridge=p9br ", ""))
	if err == nil || !strings.Contains(err.Error(), "p9.lab: no sys= entry with bridge= (the Linux host)") {
		t.Errorf("no host: %v", err)
	}
}

func kargs(t *testing.T, s *System, setup bool, over ...string) []Karg {
	t.Helper()
	var o []ndb.Pair
	for _, x := range over {
		n, v, _ := strings.Cut(x, "=")
		o = append(o, ndb.Pair{Attr: n, Val: v})
	}
	ks, err := s.Kargs(setup, o)
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

func TestKargs(t *testing.T) {
	lab := loadLab(t)
	amd, _ := lab.System("amd")
	want := []Karg{
		{"console", "0", "ipnet=p9lab"},
		{"sysname", "amd", "ipnet=p9lab"},
		{"auth", "10.99.0.10", "ipnet=p9lab"},
		{"fs", "10.99.0.10", "ipnet=p9lab"},
		{"service", "cpu", "ipnet=p9lab"},
		{"nvram", "/dev/sdF0/data", "sys=amd"},
		{"nobootprompt", "tcp ether /net/ether0 10.99.0.11 255.255.255.0", "sys=amd"},
	}
	if got := kargs(t, amd, false); !reflect.DeepEqual(got, want) {
		t.Errorf("amd:\n got %q\nwant %q", got, want)
	}
	// setup.NAME only for a setup boot
	if got := kargs(t, amd, true); !reflect.DeepEqual(got, want) {
		t.Errorf("amd --setup: %q", got)
	}
	fs, _ := lab.System("fs")
	got := kargs(t, fs, true, "nvram=", "service=x", "new=$dom")
	want = []Karg{
		{"console", "0", "ipnet=p9lab"},
		{"sysname", "fs", "ipnet=p9lab"},
		{"auth", "10.99.0.10", "ipnet=p9lab"},
		{"fs", "10.99.0.10", "ipnet=p9lab"},
		{"service", "x", "-k"},
		{"nobootprompt", "local!/dev/sdF0/fs", "sys=fs"},
		{"user", "glenda", "setup sys=fs"},
		{"new", "fs.p9lab", "-k"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fs --setup -k:\n got %q\nwant %q", got, want)
	}
	// the system's own value wins over its network's
	lab = loadLab(t, "\tini.nvram=/dev/sdF0/data\n\tini.nobootprompt=\"tcp", "\tini.nvram=/dev/sdF0/data ini.service=own\n\tini.nobootprompt=\"tcp")
	amd, _ = lab.System("amd")
	for _, k := range kargs(t, amd, false) {
		if k.Name == "service" && (k.Val != "own" || k.Where != "sys=amd") {
			t.Errorf("service %+v", k)
		}
	}
	// an entry's first value wins
	lab = loadLab(t, "\tini.nvram=/dev/sdF0/data\n\tini.nobootprompt=\"tcp", "\tini.nvram=/dev/sdF0/data ini.nvram=second\n\tini.nobootprompt=\"tcp")
	amd, _ = lab.System("amd")
	for _, k := range kargs(t, amd, false) {
		if k.Name == "nvram" && k.Val != "/dev/sdF0/data" {
			t.Errorf("nvram %+v, want the first", k)
		}
	}
	// a network's value wins over a containing network's
	lab = loadLab(t, "tcp=9fs", "ipnet=inner ip=10.99.0.8 ipmask=255.255.255.252\n\tini.service=inner\n\ntcp=9fs")
	amd, _ = lab.System("amd")
	fs, _ = lab.System("fs")
	for _, c := range []struct {
		s     *System
		where string
	}{{amd, "ipnet=inner"}, {fs, "ipnet=inner"}} {
		for _, k := range kargs(t, c.s, false) {
			if k.Name == "service" && k.Where != c.where {
				t.Errorf("%s: service %+v", c.s.Name, k)
			}
		}
	}
	rv, _ := lab.System("rv")
	for _, k := range kargs(t, rv, false) {
		if k.Name == "service" && k.Where != "ipnet=p9lab" {
			t.Errorf("rv (10.99.0.12 is outside inner): service %+v", k)
		}
	}
}

func TestExpand(t *testing.T) {
	lab := loadLab(t)
	amd, _ := lab.System("amd")
	for in, want := range map[string]string{
		"$sys":          "amd",
		"$ip/$ipmask":   "10.99.0.11/255.255.255.0",
		"x$ini.nvram,y": "x/dev/sdF0/data,y",
		"$$sys":         "$amd",
		"no dollar":     "no dollar",
		"trailing $":    "trailing $",
	} {
		if got, err := amd.Expand(in); err != nil || got != want {
			t.Errorf("Expand(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := amd.Expand("$nope"); err == nil || err.Error() != "sys=amd: $nope: no such attribute" {
		t.Errorf("Expand($nope): %v", err)
	}
	if _, err := amd.Kargs(false, []ndb.Pair{{Attr: "x", Val: "$nope"}}); err == nil {
		t.Error("Kargs expanded a missing attribute")
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain":              "plain",
		"local!/dev/sdF0/fs": "local!/dev/sdF0/fs",
		"":                   "''",
		"a b":                "'a b'",
		"it's":               "'it''s'",
		"x=y":                "'x=y'",
		"$v":                 "'$v'",
		"tab\t":              "'tab\t'",
		"#c":                 "'#c'",
		"a;b":                "'a;b'",
		"[x]":                "'[x]'",
	} {
		if got := rcquote(in); got != want {
			t.Errorf("rcquote(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"plain":          "plain",
		"-k":             "-k",
		"a=b,c:d/e.f@%+": "a=b,c:d/e.f@%+",
		"":               "''",
		"a b":            "'a b'",
		"it's":           `'it'"'"'s'`,
		"tcp!x":          "'tcp!x'",
		"ü":              "'ü'",
	} {
		if got := shquote(in); got != want {
			t.Errorf("shquote(%q) = %q, want %q", in, got, want)
		}
	}
	if got := shjoin([]string{"a", "b c"}); got != "a 'b c'" {
		t.Errorf("shjoin = %q", got)
	}
}

func TestQemu(t *testing.T) {
	lab := loadLab(t)
	rv, _ := lab.System("rv")
	argv, err := rv.Qemu(false, nil, []string{"-s"})
	if err != nil {
		t.Fatal(err)
	}
	st := filepath.Join(lab.Dir, "state/rv")
	want := []string{"qemu-system-riscv64", "-M", "virt", "-cpu", "rv64,svpbmt=true,zicbom=true",
		"-m", "2G", "-smp", "2", "-bios", filepath.Join(lab.Dir, "images/opensbi.bin"),
		"-kernel", filepath.Join(lab.Dir, "images/9rv64"),
		"-append", "console=0 sysname=rv auth=10.99.0.10 fs=10.99.0.10 service=cpu nvram=/dev/sdF0/data nobootprompt='tcp ether /net/ether0 10.99.0.12 255.255.255.0'",
		"-drive", "file=" + filepath.Join(lab.Dir, "secrets/rv.nvram") + ",if=none,id=sdF0,format=raw",
		"-device", "virtio-blk-pci,drive=sdF0,disable-legacy=on",
		"-netdev", "tap,id=lab,ifname=tap0,script=no,downscript=no",
		"-device", "virtio-net-pci,netdev=lab,mac=52:54:00:99:00:12,disable-legacy=on",
		"-display", "none",
		"-monitor", "unix:" + st + "/monitor.sock,server=on,wait=off",
		"-chardev", "socket,id=s,path=" + st + "/serial.sock,server=on,wait=off,logfile=" + st + "/console.log",
		"-serial", "chardev:s", "-s"}
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("rv:\n got %q\nwant %q", argv, want)
	}
	if f, _ := rv.Files(); !reflect.DeepEqual(f, []Mount{{filepath.Join(lab.Dir, "images/9rv64"), true},
		{filepath.Join(lab.Dir, "secrets/rv.nvram"), false}, {filepath.Join(lab.Dir, "images/opensbi.bin"), true}}) {
		t.Errorf("rv files %v", f)
	}

	// defaults, other accelerators, absolute paths, a qemu= of the
	// image's, qemuarg=, a state= of the host's, two modern disks
	lab = loadLab(t,
		"accel=tcg machine=virt mem=2G smp=2", "accel=hvf",
		"qemu=qemu-system-riscv64", "qemu=bin/qemu-system-riscv64",
		"\tvirtio=modern\n", "\tvirtio=modern disk=/abs/b.qcow2 qemuarg=-s qemuarg=-S\n",
		"bootf=images/9rv64", "bootf=/abs/9rv64",
		"state=state", "state=/run/lab")
	rv, _ = lab.System("rv")
	argv, err = rv.Qemu(false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(argv, " ")
	// qemu= is the image's program, not a file of the lab's
	if w := "bin/qemu-system-riscv64 -accel hvf -cpu rv64,svpbmt=true,zicbom=true -m 1G -smp 1 -bios " + lab.Dir + "/images/opensbi.bin -kernel /abs/9rv64 "; !strings.HasPrefix(got, w) {
		t.Errorf("rv: %q\ndoes not start %q", got, w)
	}
	for _, w := range []string{
		" -drive file=/abs/b.qcow2,if=none,id=sdF1,format=qcow2 -device virtio-blk-pci,drive=sdF1,disable-legacy=on -netdev tap,",
		" -display none -monitor unix:/run/lab/rv/monitor.sock,",
		" -serial chardev:s -s -S",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("rv: %q\nlacks %q", got, w)
		}
	}

	for _, c := range []struct{ old, new, err string }{
		{"ether=525400990011 ", "", "sys=amd: no ether= in $LAB"},
		{"ether=525400990011", "ether=52540099001", "sys=amd: ether=52540099001: want 12 hex digits"},
		{"ether=525400990011", "ether=5254009900zz", "sys=amd: ether=5254009900zz: want 12 hex digits"},
		{"bootf=images/9pc64 disk=secrets", "disk=secrets", "sys=amd: no bootf= in $LAB"},
		{"ini.nvram=/dev/sdF0/data\n\tini.nobootprompt", "ini.nvram=$nope\n\tini.nobootprompt", "sys=amd: $nope: no such attribute"},
	} {
		lab := loadLab(t, c.old, c.new)
		amd, _ := lab.System("amd")
		_, err := amd.Qemu(false, nil, nil)
		if want := strings.ReplaceAll(c.err, "$LAB", lab.Path); err == nil || err.Error() != want {
			t.Errorf("%s -> %s: %v, want %s", c.old, c.new, err, want)
		}
	}
	// upper case hex is fine
	lab = loadLab(t, "ether=525400990011", "ether=525400AbCdEf")
	amd, _ := lab.System("amd")
	if argv, err := amd.Qemu(false, nil, nil); err != nil || !strings.Contains(strings.Join(argv, " "), "mac=52:54:00:ab:cd:ef,") {
		t.Errorf("upper case ether: %q, %v", argv, err)
	}
}

func TestCommand(t *testing.T) {
	lab := loadLab(t)
	amd, _ := lab.System("amd")
	q, _ := amd.Qemu(false, nil, []string{"-s"})
	argv, err := amd.Command(false, nil, []string{"-s"})
	if err != nil {
		t.Fatal(err)
	}
	d := lab.Dir
	want := append([]string{"docker", "run", "--rm", "--name", "p9lab-amd", "--network", "p9br", "--ip", "10.99.0.11",
		"--cap-add", "NET_ADMIN", "--device", "/dev/net/tun", "--device", "/dev/kvm", "-e", "QEMU_USER=" + lab.User,
		"--mount", "type=bind,src=" + d + "/images/9pc64,dst=" + d + "/images/9pc64,ro",
		"--mount", "type=bind,src=" + d + "/secrets/amd.nvram,dst=" + d + "/secrets/amd.nvram",
		"--mount", "type=bind,src=" + d + "/state/amd,dst=" + d + "/state/amd",
		"9front-qemu"}, q...)
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("amd:\n got %q\nwant %q", argv, want)
	}

	// without kvm, an image of its own, firmware
	lab = loadLab(t, "bios=images/opensbi.bin", "image=my-qemu bios=images/opensbi.bin")
	rv, _ := lab.System("rv")
	argv, err = rv.Command(false, nil, nil)
	got := strings.Join(argv, " ")
	for _, w := range []string{
		" --device /dev/net/tun -e QEMU_USER=",
		" --mount type=bind,src=" + lab.Dir + "/images/opensbi.bin,dst=" + lab.Dir + "/images/opensbi.bin,ro --mount type=bind,src=" + lab.Dir + "/state/rv,",
		" my-qemu qemu-system-riscv64 -M virt ",
	} {
		if err != nil || !strings.Contains(got, w) {
			t.Errorf("rv: %q, %v\nlacks %q", got, err, w)
		}
	}
	if strings.Contains(got, "kvm") {
		t.Errorf("rv: %q has kvm", got)
	}

	lab = loadLab(t, "volume=p9lab-auth:/var/lib/9front-auth", "volume=p9lab-auth:/var/lib/9front-auth volume=/srv:/srv")
	auth, _ := lab.System("auth")
	argv, err = auth.Command(false, nil, []string{"-e", "X=1"})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"docker", "run", "--rm", "--name", "p9lab-auth", "--network", "p9br", "--ip", "10.99.0.2",
		"-v", "p9lab-auth:/var/lib/9front-auth", "-v", "/srv:/srv", "-e", "X=1", "9front-auth"}
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("auth:\n got %q\nwant %q", argv, want)
	}
	if f, err := auth.Files(); f != nil || err != nil {
		t.Errorf("container files %v, %v", f, err)
	}
	for _, c := range []struct {
		setup bool
		k     []ndb.Pair
	}{{true, nil}, {false, []ndb.Pair{{Attr: "a", Val: "b"}}}} {
		if _, err := auth.Command(c.setup, c.k, nil); err == nil || err.Error() != "auth is a container: no kernel command line" {
			t.Errorf("container --setup/-k: %v", err)
		}
	}
	if _, err := lab.Host.Command(false, nil, nil); err == nil || err.Error() != "sys=host has neither qemu= nor container=" {
		t.Errorf("host command: %v", err)
	}
	for _, c := range []struct{ old, new, sys, err string }{
		{"bridge=p9br ", "bridge= ", "amd", "sys=host: no bridge= in $LAB"},
		{"bootf=images/9pc64 disk=secrets", "disk=secrets", "amd", "sys=amd: no bootf= in $LAB"},
		{"sys=auth ip=10.99.0.2 ", "sys=auth ", "auth", "sys=auth: no ip= in $LAB"},
		{"image=9front-auth", "image=", "auth", "sys=auth: no image= in $LAB"},
	} {
		lab := loadLab(t, c.old, c.new)
		s, _ := lab.System(c.sys)
		_, err := s.Command(false, nil, nil)
		if want := strings.ReplaceAll(c.err, "$LAB", lab.Path); err == nil || err.Error() != want {
			t.Errorf("%s -> %s: %v, want %s", c.old, c.new, err, want)
		}
	}

	// container names: container=, else the prefix and the name
	lab = loadLab(t, "container=p9lab-auth", "container=keys")
	for name, want := range map[string]string{"amd": "p9lab-amd", "auth": "keys"} {
		if s, _ := lab.System(name); s.Container() != want {
			t.Errorf("%s: container %s, want %s", name, s.Container(), want)
		}
	}
	if s, _ := loadLab(t, "container=p9lab-auth", "container=").System("auth"); s.Container() != "p9lab-auth" {
		t.Errorf("container= empty: %s", s.Container())
	}
}

func TestNeeds(t *testing.T) {
	needs := func(lab *Lab, name string) []string {
		s, _ := lab.System(name)
		n, err := s.Needs()
		if err != nil {
			t.Fatal(err)
		}
		return names(n)
	}
	lab := loadLab(t)
	for name, want := range map[string][]string{"fs": nil, "amd": {"fs"}, "rv": {"fs"}, "auth": nil} {
		if got := needs(lab, name); !reflect.DeepEqual(got, want) {
			t.Errorf("%s needs %v, want %v", name, got, want)
		}
	}
	// the auth server in the container
	lab = loadLab(t, "auth=10.99.0.10 ntp", "auth=10.99.0.2 ntp")
	for name, want := range map[string][]string{"fs": {"auth"}, "amd": {"auth", "fs"}, "auth": nil} {
		if got := needs(lab, name); !reflect.DeepEqual(got, want) {
			t.Errorf("auth=10.99.0.2: %s needs %v, want %v", name, got, want)
		}
	}
	if s, _ := lab.AuthServer(); s.Name != "auth" {
		t.Errorf("auth server %s", s.Name)
	}
	if s, _ := loadLab(t).AuthServer(); s.Name != "fs" {
		t.Errorf("auth server %s", s.Name)
	}
	if _, err := loadLab(t, "auth=10.99.0.10 ntp", "auth=10.99.0.99 ntp").AuthServer(); err == nil ||
		!strings.Contains(err.Error(), "auth=10.99.0.99: not a qemu or container system in") {
		t.Errorf("no auth server: %v", err)
	}
	if got := names(lab.At("10.99.0.2")); !reflect.DeepEqual(got, []string{"auth"}) {
		t.Errorf("At(10.99.0.2) = %v", got)
	}
	if got := lab.At("10.99.0.1"); got != nil {
		t.Errorf("At(10.99.0.1) = %v: the host is not a system", names(got))
	}
}

func TestNvram(t *testing.T) {
	nvram := func(lab *Lab, name string) string {
		t.Helper()
		s, err := lab.System(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := s.Nvram()
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	lab := loadLab(t)
	// fs keeps its nvram in a partition, the container has no disks
	for name, want := range map[string]string{"fs": "", "amd": "secrets/amd.nvram", "rv": "secrets/rv.nvram", "auth": ""} {
		if got := nvram(lab, name); got != want {
			t.Errorf("%s nvram %q, want %q", name, got, want)
		}
	}
	// the disk the device names, inherited from an ipnet
	lab = loadLab(t, "\tini.nvram=/dev/sdF0/data\n", "",
		"ini.service=cpu", "ini.service=cpu ini.nvram=/dev/sdF1/data",
		"disk=secrets/rv.nvram", "disk=images/rv.disk disk=secrets/rv.nvram",
		"disk=secrets/amd.nvram", "disk=secrets/amd.nvram ini.cfg=/dev/sdF0/data")
	for name, want := range map[string]string{"fs": "", "amd": "", "rv": "secrets/rv.nvram"} {
		if got := nvram(lab, name); got != want {
			t.Errorf("inherited: %s nvram %q, want %q", name, got, want)
		}
	}
	s, _ := loadLab(t, "ini.nvram=/dev/sdF0/data", "ini.nvram=$nosuch").System("rv")
	if _, err := s.Nvram(); err == nil || !strings.Contains(err.Error(), "$nosuch: no such attribute") {
		t.Errorf("bad entry: %v", err)
	}
}

func TestPort(t *testing.T) {
	lab := loadLab(t)
	for s, want := range map[string]int{"rcpu": 17019, "9fs": 564, "564": 564} {
		if p, err := lab.Port(s); err != nil || p != want {
			t.Errorf("Port(%s) = %d, %v", s, p, err)
		}
	}
	for _, s := range []string{"nope", "", "-1"} {
		if _, err := lab.Port(s); err == nil || err.Error() != "tcp="+s+": no port= in "+lab.Path {
			t.Errorf("Port(%q): %v", s, err)
		}
	}
	if _, err := loadLab(t, "port=567", "port=x").Port("ticket"); err == nil {
		t.Error("port=x")
	}
}

func TestPasswords(t *testing.T) {
	lab := loadLab(t)
	f := filepath.Join(lab.Dir, "secrets/passwords.json")
	if _, err := lab.Password("roman", false); err == nil || err.Error() != f+": no password for roman" {
		t.Errorf("no file: %v", err)
	}
	host, err := lab.Password("glenda", true)
	if err != nil || len(host) != 24 {
		t.Fatalf("made up %q, %v", host, err)
	}
	roman, _ := lab.Password("roman", true)
	if roman == host || len(roman) != 24 {
		t.Errorf("roman %q, host %q", roman, host)
	}
	var pw map[string]string
	b, _ := os.ReadFile(f)
	if err := json.Unmarshal(b, &pw); err != nil || !reflect.DeepEqual(pw, map[string]string{"host": host, "roman": roman}) {
		t.Errorf("%s: %s, %v", f, b, err)
	}
	if st, _ := os.Stat(f); st.Mode().Perm() != 0o600 {
		t.Errorf("%s mode %v", f, st.Mode())
	}
	// a readable file stops being readable once it is written
	os.Chmod(f, 0o644)
	if _, err := lab.Password("alice", true); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(f); st.Mode().Perm() != 0o600 {
		t.Errorf("%s mode %v after a new password", f, st.Mode())
	}
	if st, _ := os.Stat(filepath.Dir(f)); st.Mode().Perm() != 0o700 {
		t.Errorf("secrets mode %v", st.Mode())
	}
	if p, _ := lab.Password("glenda", false); p != host {
		t.Errorf("glenda %q, want host's %q", p, host)
	}
	if p, _ := lab.Password("roman", true); p != roman {
		t.Error("roman's password was made up again")
	}
	// a user named in the file wins, even the host owner
	os.WriteFile(f, []byte(`{"glenda": "g", "host": "h"}`), 0o600)
	if p, _ := lab.Password("glenda", false); p != "g" {
		t.Errorf("glenda %q, want g", p)
	}
	os.WriteFile(f, []byte(`{`), 0o600)
	if _, err := lab.Password("glenda", false); err == nil || !strings.HasPrefix(err.Error(), f+": ") {
		t.Errorf("bad json: %v", err)
	}
	// passwords= elsewhere
	lab = loadLab(t, "passwords=secrets/passwords.json", "passwords=/nonexistent/pw.json")
	if _, err := lab.Password("roman", false); err == nil || err.Error() != "/nonexistent/pw.json: no password for roman" {
		t.Errorf("passwords=: %v", err)
	}
}

func TestAuthAttrs(t *testing.T) {
	lab := loadLab(t)
	if h := lab.HostID(); h != "glenda" {
		t.Errorf("hostid %s", h)
	}
	os.WriteFile(filepath.Join(lab.Dir, "auth"), []byte("hostid=bootes\n"), 0o644)
	if h := lab.HostID(); h != "bootes" {
		t.Errorf("hostid %s, want bootes", h)
	}
	os.Remove(filepath.Join(lab.Dir, "auth"))
	if h := lab.HostID(); h != "glenda" {
		t.Errorf("hostid %s without auth", h)
	}
	if d, err := lab.AuthDom(); err != nil || d != "p9lab" {
		t.Errorf("authdom %s, %v", d, err)
	}
	if _, err := loadLab(t, "authdom=p9lab ", "").AuthDom(); err == nil || !strings.HasSuffix(err.Error(), "p9.lab: no authdom=") {
		t.Errorf("no authdom: %v", err)
	}
}

func TestFile(t *testing.T) {
	lab := loadLab(t)
	for in, want := range map[string]string{
		"images/9pc64": filepath.Join(lab.Dir, "images/9pc64"),
		"/abs":         "/abs",
	} {
		if got := lab.File(in); got != want {
			t.Errorf("File(%s) = %s", in, got)
		}
	}
	for in, want := range map[string]string{
		"drawterm":     "drawterm",
		"bin/drawterm": filepath.Join(lab.Dir, "bin/drawterm"),
		"/abs/x":       "/abs/x",
	} {
		if got := lab.Prog(in); got != want {
			t.Errorf("Prog(%s) = %s", in, got)
		}
	}
}
