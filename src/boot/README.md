# boot, the QEMU launcher

`boot/qemu` boots a 9front checkout with binaries in it, such as
[rvs/9front](https://github.com/rvs/9front) with a release's
`ARCH.tar.gz` unpacked at its top, in QEMU.  The checkout's files are
the guest's root, over virtio 9P.  It is a Go module of its own,
rooted here, and builds into the checkout:

    cd src/boot
    go build -o /path/to/9front/boot/qemu
    go test ./...

The launcher takes the directory above its own as the checkout, so it
works from anywhere, also through a symlink.  `boot/cpu.nvram` in the
checkout is the guest's nvram.

    boot/qemu [-386 | -amd64 | -arm64 | -riscv64] [-cpuwait] [-nonet] [-smp n]
              [-vga | -kitty | -kitty-uart] [-emu] [-x [n]]

| Flag | Does |
|---|---|
| `-386` (default), `-amd64`, `-arm64`, `-riscv64` | the guest: `9pc`, `9pc64`, `9qemu`, `9rva23` |
| `-cpuwait` | the `cpuwait` kernel, amd64 and riscv64: no 9P root, rcpu on 127.0.0.1:17019 |
| `-nonet` | no network card |
| `-smp n` | processors |
| `-vga` | an x86 VESA display instead of none |
| `-kitty`, `-kitty-uart` | the guest's display in this Kitty terminal, over virtio-serial or the x86 COM2; the console goes to `.qemu.console.log` |
| `-emu`, `-x [n]` | with `-kitty`: mouse button emulation (on by default on macOS), HiDPI scale 0 to 8 |

`ROOT`, `KERNEL`, `MEM` (512M), `SMP`, `SERIAL` and `SERVICE` (terminal)
in the environment override the defaults.  The network card has the
fixed address 52:54:00:12:34:56, so that an ndb entry can name it.

It uses the host's hardware acceleration when it matches the guest:
KVM, HVF, WHPX or NVMM by host system and architecture, confirmed by
starting a paused machine with the same board, CPU and processors, and
otherwise TCG.  RISC-V stays on OpenSBI and TCG.

## Code

- `plan.go`: `plan`, from the arguments, environment, host and an
  accelerator probe to the QEMU command line, with no side effects.
- `accel.go`: the accelerator for each host and guest.
- `main.go`, `exec_*.go`: the I/O, writing the arm64 plan9.ini,
  probing, and exec'ing QEMU, or with `-kitty` running it and
  restoring the terminal.

`plan_test.go` pins the command lines and errors; `fuzz_test.go`
checks what must hold for any input.  `main_test.go` runs the launcher
as a program, with the test binary in the roles of both `boot/qemu`
and a fake `qemu-system-*`.
