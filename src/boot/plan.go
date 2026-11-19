package main

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const usage = "usage: boot/qemu [-386 | -amd64 | -arm64 | -riscv64] [-cpuwait] [-nonet] [-smp n] [-vga | -kitty | -kitty-uart] [-emu] [-x [n]]"

// Host is the machine running QEMU, in GOOS/GOARCH terms.
type Host struct{ OS, Arch string }

// Input is everything plan depends on, so that tests can supply it.
type Input struct {
	Args []string
	Env  func(string) string // "" for unset
	Top  string              // top of the checkout
	Host Host
	// Probe reports whether qemu starts a paused machine with args.
	Probe func(qemu string, args []string) bool
}

// Plan is how to run QEMU.
type Plan struct {
	Qemu string
	Args []string
	// Ini, if not empty, is the plan9.ini to write to IniPath first.
	IniPath, Ini string
	// Kitty: the terminal's modes need restoring once QEMU exits.
	Kitty  bool
	Notice string
}

// options holds the parsed command line.
type options struct {
	arch      string
	cpuwait   bool
	net       bool
	smp       string
	vga       bool
	kitty     bool
	kittyuart bool
	emu       bool
	scale     int
}

func fail(format string, a ...any) error {
	return fmt.Errorf("boot/qemu: "+format, a...)
}

func parse(args []string, smp string) (*options, error) {
	o := &options{arch: "386", net: true, smp: smp}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-386", a == "-amd64", a == "-arm64", a == "-riscv64":
			o.arch = a[1:]
		case a == "-cpuwait":
			o.cpuwait = true
		case a == "-nonet":
			o.net = false
		case a == "-smp":
			if i+1 >= len(args) {
				return nil, errors.New(usage)
			}
			i++
			o.smp = args[i]
		case a == "-vga":
			o.vga = true
		case a == "-kitty":
			o.kitty, o.kittyuart = true, false
		case a == "-kitty-uart":
			o.kitty, o.kittyuart = true, true
		case a == "-emu":
			o.emu = true
		case len(a) == 3 && strings.HasPrefix(a, "-x") && a[2] >= '0' && a[2] <= '8':
			o.scale = int(a[2] - '0')
		case a == "-x":
			// The scale is optional: the next word is one unless it is a flag.
			o.scale = 1
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				n := args[i]
				if len(n) != 1 || n[0] < '0' || n[0] > '8' {
					return nil, fail("-x takes an integer from 0 to 8")
				}
				o.scale = int(n[0] - '0')
			}
		default:
			return nil, errors.New(usage)
		}
	}
	if o.emu && !o.kitty {
		return nil, fail("-emu needs -kitty or -kitty-uart")
	}
	if o.scale != 0 && !o.kitty {
		return nil, fail("-x needs -kitty or -kitty-uart")
	}
	return o, nil
}

func x86(arch string) bool { return arch == "386" || arch == "amd64" }

// membytes converts a QEMU -m size (default unit M) to bytes.
func membytes(s string) (uint64, error) {
	unit := float64(1 << 20)
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'K', 'k':
			unit = 1 << 10
		case 'M', 'm':
			unit = 1 << 20
		case 'G', 'g':
			unit = 1 << 30
		case 'T', 't':
			unit = 1 << 40
		default:
			n++
		}
		s = s[:n-1]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || math.IsInf(f, 0) {
		return 0, errors.New("bad size")
	}
	return uint64(math.Round(f * unit)), nil
}

// env returns the variable k, or def when it is unset or empty.
func env(in Input, k, def string) string {
	if v := in.Env(k); v != "" {
		return v
	}
	return def
}

func plan(in Input) (*Plan, error) {
	root := env(in, "ROOT", in.Top)
	mem := env(in, "MEM", "512M")
	service := env(in, "SERVICE", "terminal")
	o, err := parse(in.Args, env(in, "SMP", "1"))
	if err != nil {
		return nil, err
	}

	base := map[string]string{"386": "9pc", "amd64": "9pc64", "arm64": "9qemu", "riscv64": "9rva23"}[o.arch]
	bootargs := []string{"console=0", "user=glenda", "service=" + service, "nobootprompt=virtio9p"}
	forward := ""
	if o.cpuwait {
		if o.arch != "amd64" && o.arch != "riscv64" {
			return nil, fail("cpuwait is available for amd64 and riscv64")
		}
		if !o.net {
			return nil, fail("cpuwait needs networking")
		}
		base += "cpuwait"
		bootargs = []string{"console=0", "user=glenda", "service=cpu", "nobootprompt=cpuwait"}
		forward = ",hostfwd=tcp:127.0.0.1:17019-:17019"
	}
	bootargs = append(bootargs, "nvram=/dev/sdF0/data")

	serial := env(in, "SERIAL", "stdio")
	if o.kitty {
		if o.kittyuart && !x86(o.arch) {
			return nil, fail("-kitty-uart needs an x86 COM2; use -kitty on ARM64 or RISC-V")
		}
		if o.vga || o.cpuwait {
			return nil, fail("-kitty needs a terminal kernel without -vga")
		}
		serial = env(in, "SERIAL", "file:"+root+"/.qemu.console.log")
		if o.kittyuart {
			bootargs = append(bootargs, "kittyuart=1", "monitor=kitty")
		} else {
			bootargs = append(bootargs, "kittyuart=virtserialport1", "monitor=kitty")
		}
		if o.scale != 0 {
			bootargs = append(bootargs, fmt.Sprintf("kittyscale=%d", o.scale))
		}
		if o.emu || in.Host.OS == "darwin" {
			bootargs = append(bootargs, "kittyemu=1")
		}
	}
	kernel := env(in, "KERNEL", root+"/"+o.arch+"/"+base)
	gfx := []string{"-display", "none"}
	if o.vga {
		if !x86(o.arch) {
			return nil, fail("-vga is for x86 kernels")
		}
		gfx = []string{"-vga", "std"}
		bootargs = append(bootargs, "monitor=vesa", "vgasize=1920x1080x32", "mouseport=ps2intellimouse")
	}

	p := &Plan{Kitty: o.kitty}
	args := []string{"-m", mem, "-smp", o.smp}
	args = append(args, gfx...)
	args = append(args, "-monitor", "none", "-serial", serial, "-no-reboot")
	// Public test credentials; not for production.
	// authid=glenda authdom=front; dp9ik password='glen||glenda'.
	// No secstore or p9sk1 key.
	args = append(args,
		"-drive", "file="+in.Top+"/boot/cpu.nvram,if=none,id=nvram,format=raw,readonly=on",
		"-device", "virtio-blk-pci,drive=nvram,disable-legacy=on")
	if o.kitty {
		if o.kittyuart {
			args = append(args, "-serial", "stdio")
		} else {
			// Keep the x86 COM2 backend in place for legacy UART users.
			if x86(o.arch) {
				args = append(args, "-serial", "null")
			}
			args = append(args,
				"-chardev", "stdio,id=kitty,signal=off",
				"-device", "virtio-serial-pci,id=kittyserial,disable-legacy=on,max_ports=2",
				"-device", "virtserialport,bus=kittyserial.0,chardev=kitty,nr=1,name=org.9front.kitty")
		}
	}

	var board, cpu, bios []string
	switch o.arch {
	case "386", "amd64":
		p.Qemu = "qemu-system-x86_64"
		args = append(args, "-kernel", kernel, "-append", strings.Join(bootargs, " "))
	case "arm64":
		// QEMU ignores -append for Plan 9 uImages: load a plan9.ini instead.
		p.Qemu = "qemu-system-aarch64"
		size, err := membytes(mem)
		if err != nil {
			return nil, fail("MEM must be a size such as 512M, not %s", mem)
		}
		p.IniPath = root + "/.qemu.ini"
		p.Ini = strings.Join(bootargs, "\n") +
			fmt.Sprintf("\n*maxmem=%#x\n*ncpu=%s\n", 0x40000000+size, o.smp)
		board = []string{"-M", "virt,gic-version=3,highmem=off"}
		cpu = []string{"-cpu", "cortex-a72"}
		args = append(args, "-kernel", strings.TrimSuffix(kernel, ".u")+".u",
			"-device", "loader,file="+p.IniPath+",addr=0x40010000,force-raw=on")
	case "riscv64":
		p.Qemu = "qemu-system-riscv64"
		board = []string{"-M", "virt"}
		bios = []string{"-bios", "default"}
		cpu = []string{"-cpu", "rv64,svpbmt=true,zicbom=true,sstc=true"}
		args = append(args, "-kernel", kernel, "-append", strings.Join(bootargs, " "))
	}

	accel := "tcg"
	if hw := hwaccel(in.Host, o.arch); hw != "" {
		hwcpu := []string{"-cpu", "host"}
		// Older QEMU supports -cpu host on x86 only with KVM and HVF.
		if x86(o.arch) && (hw == "whpx" || hw == "nvmm") {
			hwcpu = nil
		}
		probe := cat(board, hwcpu, bios, []string{"-accel", hw,
			"-m", "64M", "-smp", o.smp, "-S", "-nodefaults", "-display", "none",
			"-monitor", "none", "-serial", "none", "-qmp", "stdio"})
		if in.Probe(p.Qemu, probe) {
			accel, cpu = hw, hwcpu
		}
	}
	args = append(args, "-accel", accel)
	args = cat(args, board, cpu, bios)

	if !o.cpuwait {
		args = append(args,
			"-fsdev", "local,id=root,path="+root+",security_model=none",
			"-device", "virtio-9p-pci,fsdev=root,mount_tag=root,disable-legacy=on")
	}
	if o.net {
		args = append(args,
			"-device", "virtio-net-pci,netdev=net0,mac=52:54:00:12:34:56,disable-legacy=on,romfile=",
			"-netdev", "user,id=net0"+forward)
	} else {
		args = append(args, "-nic", "none")
	}
	p.Args = args

	who := fmt.Sprintf("(%s/%s, guest %s)", osname(in.Host.OS), in.Host.Arch, o.arch)
	if accel == "tcg" {
		p.Notice = "boot/qemu: software emulation: TCG " + who
	} else {
		p.Notice = "boot/qemu: hardware acceleration: " + accel + " " + who
	}
	return p, nil
}

func cat(parts ...[]string) []string {
	var r []string
	for _, p := range parts {
		r = append(r, p...)
	}
	return r
}
