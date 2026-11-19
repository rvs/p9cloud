// Command qemu boots 9front in QEMU from a checkout with binaries in it.
//
// It is installed as the checkout's boot/qemu and treats the directory
// above its own as the top of the checkout:
//
//	go build -o /path/to/9front/boot/qemu
//
// See plan for the options.  ROOT, KERNEL, MEM (e.g. 512M), SMP, SERIAL
// and SERVICE in the environment override the defaults.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// probetimeout bounds an accelerator probe.
var probetimeout = 10 * time.Second

// The modes a Kitty guest may have set in the terminal, undone.
const kittyrestore = "\033[<u\033[?2048l\033[?2004l\033[?1004l\033[?1016l\033[?1006l\033[?1003l\033[?7h\033[?25h\033[?1049l"

func main() {
	top, err := checkout()
	if err != nil {
		fatal(err)
	}
	p, err := plan(Input{
		Args:  os.Args[1:],
		Env:   os.Getenv,
		Top:   top,
		Host:  Host{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Probe: probe,
	})
	if err != nil {
		fatal(err)
	}
	if p.Ini != "" {
		if err := os.WriteFile(p.IniPath, []byte(p.Ini), 0666); err != nil {
			fatal(err)
		}
	}
	fmt.Fprintln(os.Stderr, p.Notice)
	if !p.Kitty {
		fatal(execqemu(p.Qemu, p.Args))
	}
	code, err := runqemu(p.Qemu, p.Args)
	os.Stdout.WriteString(kittyrestore)
	if err != nil {
		fatal(err)
	}
	os.Exit(code)
}

func fatal(err error) {
	msg := err.Error()
	if !strings.HasPrefix(msg, "boot/qemu: ") && !strings.HasPrefix(msg, "usage: ") {
		msg = "boot/qemu: " + msg
	}
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

// checkout finds the top of the checkout from the executable's path,
// boot/qemu, and makes it the current directory so that relative ROOT
// and KERNEL settings are relative to it.
func checkout() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	top := filepath.Dir(filepath.Dir(exe))
	if err := os.Chdir(top); err != nil {
		return "", err
	}
	return top, nil
}

// probe initializes a paused machine and quits it over QMP: compiled
// support and device permissions alone do not prove an accelerator works.
// A probe that does not finish in probetimeout counts as a failure.
func probe(qemu string, args []string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), probetimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, qemu, args...)
	cmd.Stdin = strings.NewReader("{\"execute\":\"qmp_capabilities\"}\n{\"execute\":\"quit\"}\n")
	cmd.WaitDelay = time.Second
	return cmd.Run() == nil
}

// runqemu runs QEMU on this terminal and returns its exit status.
func runqemu(qemu string, args []string) (int, error) {
	cmd := exec.Command(qemu, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	stop := forwardsignals(cmd.Process)
	err := cmd.Wait()
	stop()
	if ee, ok := err.(*exec.ExitError); ok {
		if code := ee.ExitCode(); code >= 0 {
			return code, nil
		}
		return 1, nil
	}
	return 0, err
}
