package main

import (
	"reflect"
	"strings"
	"testing"
)

// Fixed pieces of every command line, so that each case spells out only
// what it is about.
var (
	nvram = []string{
		"-drive", "file=/top/boot/cpu.nvram,if=none,id=nvram,format=raw,readonly=on",
		"-device", "virtio-blk-pci,drive=nvram,disable-legacy=on",
	}
	fsroot = []string{
		"-fsdev", "local,id=root,path=/top,security_model=none",
		"-device", "virtio-9p-pci,fsdev=root,mount_tag=root,disable-legacy=on",
	}
	net = []string{
		"-device", "virtio-net-pci,netdev=net0,mac=52:54:00:12:34:56,disable-legacy=on,romfile=",
		"-netdev", "user,id=net0",
	}
	kittyvirtio = []string{
		"-chardev", "stdio,id=kitty,signal=off",
		"-device", "virtio-serial-pci,id=kittyserial,disable-legacy=on,max_ports=2",
		"-device", "virtserialport,bus=kittyserial.0,chardev=kitty,nr=1,name=org.9front.kitty",
	}
)

const (
	terminal = "console=0 user=glenda service=terminal nobootprompt=virtio9p nvram=/dev/sdF0/data"
	cpuwait  = "console=0 user=glenda service=cpu nobootprompt=cpuwait nvram=/dev/sdF0/data"
)

func head(serial string) []string {
	return []string{"-m", "512M", "-smp", "1", "-display", "none", "-monitor", "none", "-serial", serial, "-no-reboot"}
}

func w(s string) []string { return strings.Fields(s) }

var linuxamd64 = Host{OS: "linux", Arch: "amd64"}

type probecall struct {
	qemu string
	args []string
}

// fixture runs plan with a fake environment and records the
// acceleration probes it asks for.
type fixture struct {
	args   []string
	env    map[string]string
	host   Host
	probe  bool // result of every probe
	probes []probecall
}

func (f *fixture) run() (*Plan, error) {
	host := f.host
	if host == (Host{}) {
		host = linuxamd64
	}
	return plan(Input{
		Args: f.args,
		Env:  func(k string) string { return f.env[k] },
		Top:  "/top",
		Host: host,
		Probe: func(qemu string, args []string) bool {
			f.probes = append(f.probes, probecall{qemu, args})
			return f.probe
		},
	})
}

func TestCommandLines(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		env    map[string]string
		host   Host
		probe  bool
		qemu   string
		want   []string
		notice string
	}{
		{
			name: "386 by default, software emulation",
			host: Host{OS: "linux", Arch: "riscv64"},
			qemu: "qemu-system-x86_64",
			want: cat(head("stdio"), nvram,
				[]string{"-kernel", "/top/386/9pc", "-append", terminal},
				w("-accel tcg"), fsroot, net),
			notice: "boot/qemu: software emulation: TCG (Linux/riscv64, guest 386)",
		},
		{
			name:  "386 on an amd64 host uses KVM",
			args:  w("-386"),
			probe: true,
			qemu:  "qemu-system-x86_64",
			want: cat(head("stdio"), nvram,
				[]string{"-kernel", "/top/386/9pc", "-append", terminal},
				w("-accel kvm -cpu host"), fsroot, net),
			notice: "boot/qemu: hardware acceleration: kvm (Linux/amd64, guest 386)",
		},
		{
			name: "amd64 falls back to TCG when the probe fails",
			args: w("-amd64"),
			qemu: "qemu-system-x86_64",
			want: cat(head("stdio"), nvram,
				[]string{"-kernel", "/top/amd64/9pc64", "-append", terminal},
				w("-accel tcg"), fsroot, net),
			notice: "boot/qemu: software emulation: TCG (Linux/amd64, guest amd64)",
		},
		{
			name:  "amd64 cpuwait forwards rcpu and has no 9P root",
			args:  w("-amd64 -cpuwait"),
			probe: true,
			qemu:  "qemu-system-x86_64",
			want: cat(head("stdio"), nvram,
				[]string{"-kernel", "/top/amd64/9pc64cpuwait", "-append", cpuwait},
				w("-accel kvm -cpu host"),
				w("-device virtio-net-pci,netdev=net0,mac=52:54:00:12:34:56,disable-legacy=on,romfile="),
				w("-netdev user,id=net0,hostfwd=tcp:127.0.0.1:17019-:17019")),
			notice: "boot/qemu: hardware acceleration: kvm (Linux/amd64, guest amd64)",
		},
		{
			name: "riscv64 stays on OpenSBI and TCG on a riscv64 host",
			args: w("-riscv64"),
			host: Host{OS: "linux", Arch: "riscv64"},
			qemu: "qemu-system-riscv64",
			want: cat(head("stdio"), nvram,
				[]string{"-kernel", "/top/riscv64/9rva23", "-append", terminal},
				w("-accel tcg -M virt -cpu rv64,svpbmt=true,zicbom=true,sstc=true -bios default"),
				fsroot, net),
			notice: "boot/qemu: software emulation: TCG (Linux/riscv64, guest riscv64)",
		},
		{
			name: "riscv64 cpuwait",
			args: w("-riscv64 -cpuwait"),
			qemu: "qemu-system-riscv64",
			want: cat(head("stdio"), nvram,
				[]string{"-kernel", "/top/riscv64/9rva23cpuwait", "-append", cpuwait},
				w("-accel tcg -M virt -cpu rv64,svpbmt=true,zicbom=true,sstc=true -bios default"),
				w("-device virtio-net-pci,netdev=net0,mac=52:54:00:12:34:56,disable-legacy=on,romfile="),
				w("-netdev user,id=net0,hostfwd=tcp:127.0.0.1:17019-:17019")),
			notice: "boot/qemu: software emulation: TCG (Linux/amd64, guest riscv64)",
		},
		{
			name: "arm64 boots a uImage with plan9.ini in memory",
			args: w("-arm64"),
			qemu: "qemu-system-aarch64",
			want: cat(head("stdio"), nvram,
				w("-kernel /top/arm64/9qemu.u -device loader,file=/top/.qemu.ini,addr=0x40010000,force-raw=on"),
				w("-accel tcg -M virt,gic-version=3,highmem=off -cpu cortex-a72"),
				fsroot, net),
			notice: "boot/qemu: software emulation: TCG (Linux/amd64, guest arm64)",
		},
		{
			name:  "arm64 on macOS uses HVF and the host CPU",
			args:  w("-arm64"),
			host:  Host{OS: "darwin", Arch: "arm64"},
			probe: true,
			qemu:  "qemu-system-aarch64",
			want: cat(head("stdio"), nvram,
				w("-kernel /top/arm64/9qemu.u -device loader,file=/top/.qemu.ini,addr=0x40010000,force-raw=on"),
				w("-accel hvf -M virt,gic-version=3,highmem=off -cpu host"),
				fsroot, net),
			notice: "boot/qemu: hardware acceleration: hvf (Darwin/arm64, guest arm64)",
		},
		{
			name:  "WHPX gets no -cpu host on x86",
			args:  w("-amd64"),
			host:  Host{OS: "windows", Arch: "amd64"},
			probe: true,
			qemu:  "qemu-system-x86_64",
			want: cat(head("stdio"), nvram,
				[]string{"-kernel", "/top/amd64/9pc64", "-append", terminal},
				w("-accel whpx"), fsroot, net),
			notice: "boot/qemu: hardware acceleration: whpx (Windows/amd64, guest amd64)",
		},
		{
			name:  "NVMM on NetBSD",
			args:  w("-386"),
			host:  Host{OS: "netbsd", Arch: "amd64"},
			probe: true,
			qemu:  "qemu-system-x86_64",
			want: cat(head("stdio"), nvram,
				[]string{"-kernel", "/top/386/9pc", "-append", terminal},
				w("-accel nvmm"), fsroot, net),
			notice: "boot/qemu: hardware acceleration: nvmm (NetBSD/amd64, guest 386)",
		},
		{
			name: "-nonet and -smp",
			args: w("-amd64 -nonet -smp 4"),
			qemu: "qemu-system-x86_64",
			want: cat(w("-m 512M -smp 4 -display none -monitor none -serial stdio -no-reboot"), nvram,
				[]string{"-kernel", "/top/amd64/9pc64", "-append", terminal},
				w("-accel tcg"), fsroot, w("-nic none")),
			notice: "boot/qemu: software emulation: TCG (Linux/amd64, guest amd64)",
		},
		{
			name: "-vga",
			args: w("-amd64 -vga"),
			qemu: "qemu-system-x86_64",
			want: cat(w("-m 512M -smp 1 -vga std -monitor none -serial stdio -no-reboot"), nvram,
				[]string{"-kernel", "/top/amd64/9pc64", "-append", terminal + " monitor=vesa vgasize=1920x1080x32 mouseport=ps2intellimouse"},
				w("-accel tcg"), fsroot, net),
			notice: "boot/qemu: software emulation: TCG (Linux/amd64, guest amd64)",
		},
		{
			name: "environment overrides",
			args: w("-amd64"),
			env: map[string]string{"ROOT": "/r", "KERNEL": "/k/9", "MEM": "2G", "SMP": "2",
				"SERIAL": "null", "SERVICE": "cpu"},
			qemu: "qemu-system-x86_64",
			want: cat(w("-m 2G -smp 2 -display none -monitor none -serial null -no-reboot"), nvram,
				[]string{"-kernel", "/k/9", "-append", "console=0 user=glenda service=cpu nobootprompt=virtio9p nvram=/dev/sdF0/data"},
				w("-accel tcg -fsdev local,id=root,path=/r,security_model=none"),
				w("-device virtio-9p-pci,fsdev=root,mount_tag=root,disable-legacy=on"), net),
			notice: "boot/qemu: software emulation: TCG (Linux/amd64, guest amd64)",
		},
		{
			name: "-kitty on x86 parks COM2 and uses a virtio port",
			args: w("-amd64 -kitty"),
			qemu: "qemu-system-x86_64",
			want: cat(head("file:/top/.qemu.console.log"), nvram,
				w("-serial null"), kittyvirtio,
				[]string{"-kernel", "/top/amd64/9pc64", "-append", terminal + " kittyuart=virtserialport1 monitor=kitty"},
				w("-accel tcg"), fsroot, net),
			notice: "boot/qemu: software emulation: TCG (Linux/amd64, guest amd64)",
		},
		{
			name: "-kitty-uart, scale and emulation",
			args: w("-386 -kitty-uart -x 2 -emu"),
			env:  map[string]string{"SERIAL": "file:/log"},
			qemu: "qemu-system-x86_64",
			want: cat(head("file:/log"), nvram, w("-serial stdio"),
				[]string{"-kernel", "/top/386/9pc", "-append", terminal + " kittyuart=1 monitor=kitty kittyscale=2 kittyemu=1"},
				w("-accel tcg"), fsroot, net),
			notice: "boot/qemu: software emulation: TCG (Linux/amd64, guest 386)",
		},
		{
			name: "-kitty on riscv64 has no COM2 to park",
			args: w("-riscv64 -kitty -x3"),
			qemu: "qemu-system-riscv64",
			want: cat(head("file:/top/.qemu.console.log"), nvram, kittyvirtio,
				[]string{"-kernel", "/top/riscv64/9rva23", "-append", terminal + " kittyuart=virtserialport1 monitor=kitty kittyscale=3"},
				w("-accel tcg -M virt -cpu rv64,svpbmt=true,zicbom=true,sstc=true -bios default"),
				fsroot, net),
			notice: "boot/qemu: software emulation: TCG (Linux/amd64, guest riscv64)",
		},
		{
			name: "-x alone means scale 1; macOS turns on emulation",
			args: w("-amd64 -kitty -x"),
			host: Host{OS: "darwin", Arch: "arm64"},
			qemu: "qemu-system-x86_64",
			want: cat(head("file:/top/.qemu.console.log"), nvram,
				w("-serial null"), kittyvirtio,
				[]string{"-kernel", "/top/amd64/9pc64", "-append", terminal + " kittyuart=virtserialport1 monitor=kitty kittyscale=1 kittyemu=1"},
				w("-accel tcg"), fsroot, net),
			notice: "boot/qemu: software emulation: TCG (Darwin/arm64, guest amd64)",
		},
		{
			name: "-x 0 is no scale",
			args: w("-amd64 -kitty -x 0"),
			qemu: "qemu-system-x86_64",
			want: cat(head("file:/top/.qemu.console.log"), nvram,
				w("-serial null"), kittyvirtio,
				[]string{"-kernel", "/top/amd64/9pc64", "-append", terminal + " kittyuart=virtserialport1 monitor=kitty"},
				w("-accel tcg"), fsroot, net),
			notice: "boot/qemu: software emulation: TCG (Linux/amd64, guest amd64)",
		},
		{
			name: "the last architecture wins; -kitty after -kitty-uart",
			args: w("-arm64 -kitty-uart -amd64 -kitty"),
			qemu: "qemu-system-x86_64",
			want: cat(head("file:/top/.qemu.console.log"), nvram,
				w("-serial null"), kittyvirtio,
				[]string{"-kernel", "/top/amd64/9pc64", "-append", terminal + " kittyuart=virtserialport1 monitor=kitty"},
				w("-accel tcg"), fsroot, net),
			notice: "boot/qemu: software emulation: TCG (Linux/amd64, guest amd64)",
		},
		{
			name: "-x before another flag means scale 1",
			args: w("-amd64 -kitty -x -emu"),
			qemu: "qemu-system-x86_64",
			want: cat(head("file:/top/.qemu.console.log"), nvram,
				w("-serial null"), kittyvirtio,
				[]string{"-kernel", "/top/amd64/9pc64", "-append", terminal + " kittyuart=virtserialport1 monitor=kitty kittyscale=1 kittyemu=1"},
				w("-accel tcg"), fsroot, net),
			notice: "boot/qemu: software emulation: TCG (Linux/amd64, guest amd64)",
		},
		{
			name: "-smp takes the next word whatever it is; QEMU checks it",
			args: w("-amd64 -smp -1"),
			qemu: "qemu-system-x86_64",
			want: cat(w("-m 512M -smp -1 -display none -monitor none -serial stdio -no-reboot"), nvram,
				[]string{"-kernel", "/top/amd64/9pc64", "-append", terminal},
				w("-accel tcg"), fsroot, net),
			notice: "boot/qemu: software emulation: TCG (Linux/amd64, guest amd64)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fixture{args: tt.args, env: tt.env, host: tt.host, probe: tt.probe}
			p, err := f.run()
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if p.Qemu != tt.qemu {
				t.Errorf("qemu = %q, want %q", p.Qemu, tt.qemu)
			}
			if !reflect.DeepEqual(p.Args, tt.want) {
				t.Errorf("args:\n got %q\nwant %q", p.Args, tt.want)
			}
			if p.Notice != tt.notice {
				t.Errorf("notice = %q, want %q", p.Notice, tt.notice)
			}
			wantkitty := strings.Contains(strings.Join(tt.args, " "), "-kitty")
			if p.Kitty != wantkitty {
				t.Errorf("kitty = %v, want %v", p.Kitty, wantkitty)
			}
		})
	}
}

func TestProbe(t *testing.T) {
	tests := []struct {
		name string
		args []string
		host Host
		want []probecall // nil: no probe at all
	}{
		{
			name: "x86 on KVM",
			args: w("-amd64 -smp 2"),
			want: []probecall{{"qemu-system-x86_64",
				w("-cpu host -accel kvm -m 64M -smp 2 -S -nodefaults -display none -monitor none -serial none -qmp stdio")}},
		},
		{
			name: "arm64 probes the same board",
			args: w("-arm64"),
			host: Host{OS: "linux", Arch: "arm64"},
			want: []probecall{{"qemu-system-aarch64",
				w("-M virt,gic-version=3,highmem=off -cpu host -accel kvm -m 64M -smp 1 -S -nodefaults -display none -monitor none -serial none -qmp stdio")}},
		},
		{
			name: "WHPX probes without -cpu host",
			args: w("-amd64"),
			host: Host{OS: "windows", Arch: "amd64"},
			want: []probecall{{"qemu-system-x86_64",
				w("-accel whpx -m 64M -smp 1 -S -nodefaults -display none -monitor none -serial none -qmp stdio")}},
		},
		{name: "riscv64 is never accelerated", args: w("-riscv64"), host: Host{OS: "linux", Arch: "riscv64"}},
		{name: "foreign guest", args: w("-arm64")},
		{name: "amd64 guest on a 386 host", args: w("-amd64"), host: Host{OS: "linux", Arch: "386"}},
		{name: "no accelerator on FreeBSD", args: w("-amd64"), host: Host{OS: "freebsd", Arch: "amd64"}},
		{name: "HVF is not for 386 hosts", args: w("-386"), host: Host{OS: "darwin", Arch: "386"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fixture{args: tt.args, host: tt.host}
			if _, err := f.run(); err != nil {
				t.Fatalf("plan: %v", err)
			}
			if !reflect.DeepEqual(f.probes, tt.want) {
				t.Errorf("probes:\n got %q\nwant %q", f.probes, tt.want)
			}
		})
	}
}

func TestArm64Ini(t *testing.T) {
	f := &fixture{
		args: w("-arm64 -smp 2 -kitty -x 2"),
		env:  map[string]string{"MEM": "1G", "KERNEL": "/k/9qemu.u"},
	}
	p, err := f.run()
	if err != nil {
		t.Fatal(err)
	}
	if p.IniPath != "/top/.qemu.ini" {
		t.Errorf("ini path = %q", p.IniPath)
	}
	want := strings.Join(w(terminal+" kittyuart=virtserialport1 monitor=kitty kittyscale=2"), "\n") +
		"\n*maxmem=0x80000000\n*ncpu=2\n"
	if p.Ini != want {
		t.Errorf("ini:\n%s\nwant:\n%s", p.Ini, want)
	}
	if i := index(p.Args, "-kernel"); i < 0 || p.Args[i+1] != "/k/9qemu.u" {
		t.Errorf("kernel not /k/9qemu.u: %q", p.Args)
	}
	if index(p.Args, "-append") >= 0 {
		t.Errorf("arm64 must not use -append: %q", p.Args)
	}

	// Other guests take the command line from -append.
	f = &fixture{args: w("-amd64")}
	if p, _ = f.run(); p.Ini != "" || p.IniPath != "" {
		t.Errorf("amd64 wrote an ini: %q %q", p.IniPath, p.Ini)
	}
}

func index(a []string, s string) int {
	for i, x := range a {
		if x == s {
			return i
		}
	}
	return -1
}

func TestMemBytes(t *testing.T) {
	tests := []struct {
		in   string
		want uint64
		bad  bool
	}{
		{in: "512M", want: 512 << 20},
		{in: "512m", want: 512 << 20},
		{in: "512", want: 512 << 20},
		{in: "64k", want: 64 << 10},
		{in: "2G", want: 2 << 30},
		{in: "1.5G", want: 3 << 29},
		{in: "1T", want: 1 << 40},
		{in: "", bad: true},
		{in: "G", bad: true},
		{in: "lots", bad: true},
		{in: "-1G", bad: true},
	}
	for _, tt := range tests {
		got, err := membytes(tt.in)
		if tt.bad {
			if err == nil {
				t.Errorf("membytes(%q) = %d, want an error", tt.in, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("membytes(%q) = %d, %v; want %d", tt.in, got, err, tt.want)
		}
	}
}

const wantusage = "usage: boot/qemu [-386 | -amd64 | -arm64 | -riscv64] [-cpuwait] [-nonet] [-smp n] [-vga | -kitty | -kitty-uart] [-emu] [-x [n]]"

func TestErrors(t *testing.T) {
	tests := []struct {
		args []string
		env  map[string]string
		want string
	}{
		{w("-bogus"), nil, wantusage},
		{w("-x9"), nil, wantusage},
		{w("-x12"), nil, wantusage},
		{w("-smp"), nil, wantusage},
		{w("-kitty -x 9"), nil, "boot/qemu: -x takes an integer from 0 to 8"},
		{w("-kitty -x two"), nil, "boot/qemu: -x takes an integer from 0 to 8"},
		{w("-emu"), nil, "boot/qemu: -emu needs -kitty or -kitty-uart"},
		{w("-x 2"), nil, "boot/qemu: -x needs -kitty or -kitty-uart"},
		{w("-386 -cpuwait"), nil, "boot/qemu: cpuwait is available for amd64 and riscv64"},
		{w("-arm64 -cpuwait"), nil, "boot/qemu: cpuwait is available for amd64 and riscv64"},
		{w("-amd64 -cpuwait -nonet"), nil, "boot/qemu: cpuwait needs networking"},
		{w("-arm64 -kitty-uart"), nil, "boot/qemu: -kitty-uart needs an x86 COM2; use -kitty on ARM64 or RISC-V"},
		{w("-riscv64 -kitty-uart"), nil, "boot/qemu: -kitty-uart needs an x86 COM2; use -kitty on ARM64 or RISC-V"},
		{w("-amd64 -vga -kitty"), nil, "boot/qemu: -kitty needs a terminal kernel without -vga"},
		{w("-amd64 -cpuwait -kitty"), nil, "boot/qemu: -kitty needs a terminal kernel without -vga"},
		{w("-arm64 -vga"), nil, "boot/qemu: -vga is for x86 kernels"},
		{w("-arm64"), map[string]string{"MEM": "lots"}, "boot/qemu: MEM must be a size such as 512M, not lots"},

		// With several problems, the first check reports.
		{w("-emu -bogus"), nil, wantusage},
		{w("-x 2 -emu"), nil, "boot/qemu: -emu needs -kitty or -kitty-uart"},
		{w("-386 -cpuwait -nonet"), nil, "boot/qemu: cpuwait is available for amd64 and riscv64"},
		{w("-arm64 -cpuwait -kitty-uart -vga"), nil, "boot/qemu: cpuwait is available for amd64 and riscv64"},
		{w("-arm64 -kitty-uart -vga"), nil, "boot/qemu: -kitty-uart needs an x86 COM2; use -kitty on ARM64 or RISC-V"},
		{w("-arm64 -vga"), map[string]string{"MEM": "lots"}, "boot/qemu: -vga is for x86 kernels"},
	}
	for _, tt := range tests {
		f := &fixture{args: tt.args, env: tt.env}
		_, err := f.run()
		if err == nil || err.Error() != tt.want {
			t.Errorf("%q: err = %v, want %q", tt.args, err, tt.want)
		}
	}
}

func TestOsname(t *testing.T) {
	for goos, want := range map[string]string{"linux": "Linux", "darwin": "Darwin", "netbsd": "NetBSD",
		"freebsd": "FreeBSD", "openbsd": "OpenBSD", "windows": "Windows", "plan9": "plan9"} {
		if got := osname(goos); got != want {
			t.Errorf("osname(%q) = %q, want %q", goos, got, want)
		}
	}
}
