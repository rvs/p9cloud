package main

import (
	"io"
	"net/http"
	"time"
)

// env is what p9cloud does to the world outside the lab file, so the
// tests can fake it.
type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	getenv         func(string) string
	// run runs a command with more environment and returns its standard
	// output; its standard error is p9cloud's.
	run func(extra []string, argv ...string) (string, error)
	// exec replaces p9cloud with a command.
	exec   func(extra []string, argv ...string) error
	umask  func(int) int
	sysnet string // /sys/class/net: the network devices
	self   string // p9cloud's binary
	sleep  func(time.Duration)
	now    func() time.Time
	typing time.Duration // between bytes typed at a console
	// rawtty puts the terminal in raw mode and returns what restores
	// it; nil if standard input is not a terminal.
	rawtty func() (func(), error)
	serve  func(addr string, h http.Handler) error
}
