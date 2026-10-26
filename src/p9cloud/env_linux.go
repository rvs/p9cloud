package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func realEnv() *env {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	return &env{
		stdin:  os.Stdin,
		stdout: os.Stdout,
		stderr: os.Stderr,
		getenv: os.Getenv,
		run:    run,
		exec:   execv,
		umask:  syscall.Umask,
		sysnet: "/sys/class/net",
		self:   self,
		sleep:  time.Sleep,
		now:    time.Now,
		typing: 20 * time.Millisecond,
		rawtty: func() (func(), error) { return rawtty(int(os.Stdin.Fd())) },
		serve:  http.ListenAndServe,
	}
}

func environ(extra []string) []string {
	if extra == nil {
		return nil
	}
	return append(os.Environ(), extra...)
}

// run runs a command and returns its output; a failure's error has
// what it printed on its standard error.
func run(extra []string, argv ...string) (string, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = environ(extra)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if msg := strings.TrimSpace(stderr.String()); err != nil && msg != "" {
		err = fmt.Errorf("%v: %s", err, msg)
	}
	return string(out), err
}

func execv(extra []string, argv ...string) error {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	env := environ(extra)
	if env == nil {
		env = os.Environ()
	}
	return syscall.Exec(path, argv, env)
}

func ioctl(fd int, req uint, t *syscall.Termios) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(req), uintptr(unsafe.Pointer(t))); e != 0 {
		return e
	}
	return nil
}

// rawtty puts a terminal in raw mode, as cfmakeraw, and returns what
// restores it; nil if fd is not a terminal.
func rawtty(fd int) (func(), error) {
	var old syscall.Termios
	if ioctl(fd, syscall.TCGETS, &old) != nil {
		return nil, nil
	}
	t := old
	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	t.Oflag &^= syscall.OPOST
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cflag &^= syscall.CSIZE | syscall.PARENB
	t.Cflag |= syscall.CS8
	t.Cc[syscall.VMIN], t.Cc[syscall.VTIME] = 1, 0
	if err := ioctl(fd, syscall.TCSETS, &t); err != nil {
		return nil, err
	}
	return func() { ioctl(fd, syscall.TCSETS, &old) }, nil
}
