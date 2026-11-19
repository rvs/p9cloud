package main

import (
	"reflect"
	"strings"
	"testing"
)

var fuzzhosts = []Host{
	{"linux", "amd64"}, {"linux", "arm64"}, {"linux", "386"}, {"linux", "riscv64"},
	{"darwin", "arm64"}, {"darwin", "amd64"}, {"netbsd", "amd64"}, {"windows", "amd64"},
	{"windows", "arm64"}, {"freebsd", "amd64"},
}

// FuzzPlan checks what must hold for any command line and environment:
// plan does not panic, gives the same answer twice, and either fails
// with a launcher message or produces a command line QEMU can parse.
func FuzzPlan(f *testing.F) {
	for _, a := range []string{"", "-amd64 -cpuwait", "-arm64 -kitty -x 3", "-386 -kitty-uart -emu",
		"-riscv64 -cpuwait -nonet", "-amd64 -vga -smp 4", "-x", "-x -x", "-smp", "-kitty -x 9 -emu"} {
		f.Add(a, "", "", "", byte(0), true)
	}
	f.Add("-arm64", "1.5G", "2", "/k/9.u", byte(4), true)
	f.Add("-arm64", "lots", "", "", byte(1), false)
	f.Fuzz(func(t *testing.T, args, mem, smp, kernel string, h byte, ok bool) {
		env := map[string]string{"MEM": mem, "SMP": smp, "KERNEL": kernel}
		in := func(probes *int) Input {
			return Input{
				Args: strings.Fields(args),
				Env:  func(k string) string { return env[k] },
				Top:  "/top",
				Host: fuzzhosts[int(h)%len(fuzzhosts)],
				Probe: func(string, []string) bool {
					*probes++
					return ok
				},
			}
		}
		var n1, n2 int
		p, err := plan(in(&n1))
		p2, err2 := plan(in(&n2))
		if !reflect.DeepEqual(p, p2) || (err == nil) != (err2 == nil) || n1 != n2 {
			t.Fatalf("plan is not deterministic: %v %v / %v %v", p, err, p2, err2)
		}
		if n1 > 1 {
			t.Errorf("%d probes", n1)
		}
		if err != nil {
			if m := err.Error(); m != usage && !strings.HasPrefix(m, "boot/qemu: ") {
				t.Errorf("error %q is not a launcher message", m)
			}
			return
		}
		switch p.Qemu {
		case "qemu-system-x86_64", "qemu-system-aarch64", "qemu-system-riscv64":
		default:
			t.Fatalf("qemu %q", p.Qemu)
		}
		a := p.Args
		if len(a) < 4 || a[0] != "-m" || a[2] != "-smp" {
			t.Fatalf("args start %q", a)
		}
		count := map[string]int{}
		for i, x := range a {
			if x == "" && !(mem == "" || smp == "" || kernel == "") {
				t.Errorf("empty argument %d in %q", i, a)
			}
			count[x]++
		}
		if count["-kernel"] != 1 || count["-accel"] != 1 || count["-no-reboot"] != 1 {
			t.Errorf("-kernel, -accel or -no-reboot not exactly once: %q", a)
		}
		if i := index(a, "-accel"); i+1 >= len(a) || !strings.Contains(" tcg kvm hvf nvmm whpx ", " "+a[i+1]+" ") {
			t.Errorf("bad -accel in %q", a)
		}
		if !strings.HasPrefix(p.Notice, "boot/qemu: ") || strings.Contains(p.Notice, "\n") {
			t.Errorf("notice %q", p.Notice)
		}
		arm := p.Qemu == "qemu-system-aarch64"
		if arm != (p.Ini != "") || arm != (p.IniPath == "/top/.qemu.ini") {
			t.Errorf("ini %q at %q for %s", p.Ini, p.IniPath, p.Qemu)
		}
		if arm && (!strings.HasSuffix(p.Ini, "\n") || !strings.Contains(p.Ini, "\n*maxmem=0x")) {
			t.Errorf("ini %q", p.Ini)
		}
	})
}
