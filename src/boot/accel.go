package main

// hwaccel returns the QEMU accelerator that could run guest on host
// natively, or "" when there is none and QEMU must emulate.
func hwaccel(host Host, guest string) string {
	if host.Arch != guest && !(host.Arch == "amd64" && guest == "386") {
		return ""
	}
	switch host.OS {
	case "linux":
		// KVM loads a raw RISC-V kernel at the start of RAM, leaving
		// no room below it for the boot stack; OpenSBI does.
		if guest != "riscv64" {
			return "kvm"
		}
	case "darwin":
		if host.Arch == "amd64" || host.Arch == "arm64" {
			return "hvf"
		}
	case "netbsd":
		if host.Arch == "386" || host.Arch == "amd64" {
			return "nvmm"
		}
	case "windows":
		if host.Arch == "386" || host.Arch == "amd64" || host.Arch == "arm64" {
			return "whpx"
		}
	}
	return ""
}

// osname spells GOOS the way uname -s does.
func osname(goos string) string {
	switch goos {
	case "linux":
		return "Linux"
	case "darwin":
		return "Darwin"
	case "netbsd":
		return "NetBSD"
	case "freebsd":
		return "FreeBSD"
	case "openbsd":
		return "OpenBSD"
	case "windows":
		return "Windows"
	}
	return goos
}
