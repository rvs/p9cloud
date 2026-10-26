package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// conn is a connection to a QEMU system's serial console or monitor.
type conn struct {
	net.Conn
	e   *env
	buf []byte // received but not yet expected, without \r
}

func strerror(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno.Error()
	}
	return err.Error()
}

func (e *env) dialSerial(s *System) (*conn, error) {
	c, err := net.DialTimeout("unix", filepath.Join(s.State(), "serial.sock"), time.Second)
	if err != nil {
		return nil, fmt.Errorf("%s: serial console: %s; is it running?", s.Name, strerror(err))
	}
	return &conn{Conn: c, e: e}, nil
}

// typeText types at the console slowly enough for it.
func (c *conn) typeText(s string) error {
	for i := 0; i < len(s); i++ {
		if _, err := c.Write([]byte{s[i]}); err != nil {
			return err
		}
		c.e.sleep(c.e.typing)
	}
	return nil
}

// expectError is what expect saw instead.
type expectError struct {
	what string // timed out or no output
	data string
}

func (x *expectError) Error() string { return x.what + ": " + x.data }

// expect reads until match finds what it wants in what was received and
// not yet expected, \r removed; it returns the received text up to the
// match and the match's submatch indices into it.  It copies all it
// reads to echo.
func (c *conn) expect(match func([]byte) []int, timeout time.Duration, echo io.Writer) ([]byte, []int, error) {
	deadline := time.Now().Add(timeout)
	b := make([]byte, 65536)
	for {
		if m := match(c.buf); m != nil {
			data := c.buf[:m[1]]
			c.buf = c.buf[m[1]:]
			return data, m, nil
		}
		c.SetReadDeadline(deadline)
		n, err := c.Read(b)
		if echo != nil && n > 0 {
			echo.Write(b[:n])
		}
		c.buf = append(c.buf, bytes.ReplaceAll(b[:n], []byte("\r"), nil)...)
		if err != nil {
			if len(c.buf) == 0 {
				return nil, nil, &expectError{"no output", ""}
			}
			if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, io.EOF) {
				if m := match(c.buf); m != nil {
					continue
				}
				return nil, nil, &expectError{"timed out", string(c.buf)}
			}
			return nil, nil, err
		}
	}
}

// contains matches a string, case-insensitively with fold.
func contains(s string, fold bool) func([]byte) []int {
	want := []byte(s)
	return func(d []byte) []int {
		h := d
		if fold {
			h, want = bytes.ToLower(d), bytes.ToLower(want)
		}
		if i := bytes.Index(h, want); i >= 0 {
			return []int{i, i + len(want)}
		}
		return nil
	}
}
