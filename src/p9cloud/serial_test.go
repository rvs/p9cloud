package main

import (
	"errors"
	"io"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// pipeConn returns a conn whose other end is the returned net.Conn.
func pipeConn(t *testing.T) (*conn, net.Conn) {
	path := filepath.Join(t.TempDir(), "sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); s.Close() })
	return &conn{Conn: c, e: &newFake(t).env}, s
}

func TestExpect(t *testing.T) {
	c, s := pipeConn(t)
	s.Write([]byte("one\r\ntwo one"))
	var echo syncBuf
	for _, want := range []string{"one", "\ntwo", " one"} {
		data, m, err := c.expect(contains(strings.TrimSpace(want), false), time.Second, &echo)
		if err != nil || string(data) != want || m[1] != len(data) {
			t.Errorf("expect %q: %q %v %v", strings.TrimSpace(want), data, m, err)
		}
	}
	if echo.String() != "one\r\ntwo one" {
		t.Errorf("echo %q", echo.String())
	}
	// what was matched is gone
	if _, _, err := c.expect(contains("one", false), 50*time.Millisecond, nil); err == nil || err.Error() != "no output: " {
		t.Errorf("expected again: %v", err)
	}
	s.Write([]byte("Partial"))
	_, _, err := c.expect(contains("partial line", true), 50*time.Millisecond, nil)
	var xe *expectError
	if !errors.As(err, &xe) || xe.what != "timed out" || xe.data != "Partial" {
		t.Errorf("timeout: %v", err)
	}
	s.Write([]byte(" LINE\r\nDONE x\r\n"))
	data, m, err := c.expect(regexp.MustCompile(`(?m)^DONE([^\n]*)\n`).FindSubmatchIndex, time.Second, nil)
	if err != nil || string(data) != "Partial LINE\nDONE x\n" || string(data[m[2]:m[3]]) != " x" {
		t.Errorf("regexp: %q %v %v", data, m, err)
	}
	// hung up: what is there still matches
	s.Write([]byte("last"))
	s.Close()
	if data, _, err := c.expect(contains("last", false), time.Second, nil); err != nil || string(data) != "last" {
		t.Errorf("after hang up: %q %v", data, err)
	}
	if _, _, err := c.expect(contains("more", false), time.Second, nil); err == nil || err.Error() != "no output: " {
		t.Errorf("after hang up: %v", err)
	}
}

func TestTypeText(t *testing.T) {
	c, s := pipeConn(t)
	f := newFake(t)
	f.typing = time.Millisecond
	c.e = &f.env
	if err := c.typeText("abc\n"); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	s.SetReadDeadline(time.Now().Add(time.Second))
	if n, err := io.ReadFull(s, b); n != 4 || string(b) != "abc\n" {
		t.Errorf("got %q, %v", b[:n], err)
	}
	if f.slept != 4*time.Millisecond {
		t.Errorf("slept %v, want 4ms: a pause after each byte", f.slept)
	}
	s.Close()
	c.Close()
	if err := c.typeText("x"); err == nil {
		t.Error("typed at a closed connection")
	}
}
