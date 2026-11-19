//go:build !unix

package main

import (
	"os"
	"os/signal"
)

// execqemu runs QEMU and exits with its status: there is no exec.
func execqemu(qemu string, args []string) error {
	code, err := runqemu(qemu, args)
	if err != nil {
		return err
	}
	os.Exit(code)
	return nil
}

// forwardsignals keeps an interrupt from ending the launcher before
// QEMU, which sees it on the console as well.
func forwardsignals(p *os.Process) (stop func()) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	return func() { signal.Stop(c) }
}
