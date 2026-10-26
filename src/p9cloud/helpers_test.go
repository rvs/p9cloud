package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf is a bytes.Buffer safe to write from one goroutine and read
// from another.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fake is an env that records what p9cloud would do to the host.
type fake struct {
	env
	out, err syncBuf
	ran      [][]string        // commands run
	ranEnv   [][]string        // and their added environment
	reply    map[string]string // a command's output, by its words
	fail     map[string]error  // a command's error
	execed   []string          // the command p9cloud replaced itself with
	execEnv  []string
	slept    time.Duration
	served   string // the address serve listened on
	handler  http.Handler
	raw      bool // whether the terminal was in raw mode
	restored bool
}

var errFake = errors.New("exit status 1")

func newFake(t *testing.T) *fake {
	f := &fake{reply: map[string]string{}, fail: map[string]error{}}
	f.stdin = strings.NewReader("")
	f.stdout, f.stderr = &f.out, &f.err
	f.run = func(extra []string, argv ...string) (string, error) {
		f.ran = append(f.ran, argv)
		f.ranEnv = append(f.ranEnv, extra)
		k := strings.Join(argv, " ")
		return f.reply[k], f.fail[k]
	}
	f.exec = func(extra []string, argv ...string) error {
		f.execed, f.execEnv = argv, extra
		return nil
	}
	f.umask = func(int) int { return 0o022 }
	f.sysnet = t.TempDir()
	f.self = "/usr/local/bin/p9cloud"
	f.sleep = func(d time.Duration) { f.slept += d }
	start := time.Now()
	f.now = func() time.Time { return start.Add(f.slept) }
	f.rawtty = func() (func(), error) { return nil, nil }
	f.serve = func(addr string, h http.Handler) error {
		f.served, f.handler = addr, h
		return nil
	}
	f.getenv = func(string) string { return "" }
	return f
}

// p9cloud runs the command line and returns its exit status.
func (f *fake) p9cloud(args ...string) int { return p9cloud(&f.env, args) }

// netdev pretends the host has these network devices.
func (f *fake) netdev(t *testing.T, names ...string) {
	for _, n := range names {
		if err := os.Mkdir(filepath.Join(f.sysnet, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// testLab copies testdata/lab to a directory of its own, applying edits
// (old, new, old, new...) to p9.lab, and returns the lab file.
func testLab(t *testing.T, edits ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"p9.lab", "auth"} {
		b, err := os.ReadFile(filepath.Join("testdata/lab", name))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		for i := 0; name == "p9.lab" && i+1 < len(edits); i += 2 {
			if !strings.Contains(s, edits[i]) {
				t.Fatalf("p9.lab has no %q", edits[i])
			}
			s = strings.ReplaceAll(s, edits[i], edits[i+1])
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "p9.lab")
}

// touch creates the files under dir, with their directories.
func touch(t *testing.T, dir string, names ...string) {
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func loadLab(t *testing.T, edits ...string) *Lab {
	t.Helper()
	lab, err := Load(testLab(t, edits...))
	if err != nil {
		t.Fatal(err)
	}
	return lab
}

// golden compares got with testdata/golden/name, where $LAB is the lab's
// directory and $QEMU_USER is the uid:gid QEMU runs as.
func golden(t *testing.T, name, got, labdir string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata/golden", name))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer("$LAB", labdir,
		"$QEMU_USER", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())).Replace(string(b))
	if got != want {
		gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
		for i := 0; i < max(len(gl), len(wl)); i++ {
			var g, w string
			if i < len(gl) {
				g = gl[i]
			}
			if i < len(wl) {
				w = wl[i]
			}
			if g != w {
				t.Errorf("%s:%d:\n got %q\nwant %q", name, i+1, g, w)
				return
			}
		}
	}
}

// fakeConsole serves a serial console on path.  It echoes what it is
// sent, as a 9front console does, and answers each line with reply's
// result.  It returns everything it was sent.
func fakeConsole(t *testing.T, path string, reply func(line string) string) *syncBuf {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	got := &syncBuf{}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				var line []byte
				for {
					b, err := r.ReadByte()
					if err != nil {
						return
					}
					got.Write([]byte{b})
					c.Write([]byte{b})
					if b != '\n' {
						line = append(line, b)
						continue
					}
					c.Write([]byte("\r"))
					io.WriteString(c, reply(string(line)))
					line = nil
				}
			}()
		}
	}()
	return got
}

// fakeMonitor serves a QEMU monitor on path and returns what each
// connection sent, once it closes.
func fakeMonitor(t *testing.T, path string) *syncBuf {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	got := &syncBuf{}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				io.WriteString(c, "QEMU 9.2.0 monitor - type 'help' for more information\r\n(qemu) ")
				b, _ := io.ReadAll(c)
				got.Write(b)
			}()
		}
	}()
	return got
}

// rawSocket serves path with handler, which may return to hang up.
func rawSocket(t *testing.T, path string, handler func(net.Conn)) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				handler(c)
			}()
		}
	}()
}

// recorder records lines from another goroutine.
type recorder struct {
	mu sync.Mutex
	a  []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.a = append(r.a, s)
}

func (r *recorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.a...)
}

// wait waits for n lines and returns them.
func (r *recorder) wait(t *testing.T, n int) []string {
	t.Helper()
	waitFor(t, fmt.Sprintf("%d lines", n), func() bool { return len(r.get()) >= n })
	return r.get()
}

// waitFor waits up to a second for cond.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
