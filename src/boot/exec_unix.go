//go:build unix

package main

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// execqemu replaces this process with QEMU.
func execqemu(qemu string, args []string) error {
	path, err := exec.LookPath(qemu)
	if err != nil {
		return err
	}
	return syscall.Exec(path, append([]string{qemu}, args...), os.Environ())
}

// forwardsignals passes the signals that would end the launcher on to
// QEMU instead, so that the launcher outlives it and can clean up.
func forwardsignals(p *os.Process) (stop func()) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		for s := range c {
			p.Signal(s)
		}
	}()
	return func() { signal.Stop(c); close(c) }
}
