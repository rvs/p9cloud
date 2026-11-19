package main

// These tests run the launcher as a program.  The test binary is copied
// to TOP/boot/qemu, where it runs main, and to BIN/qemu-system-*, where
// it plays QEMU: it logs how it was run to $FAKE_LOG and does what the
// other FAKE_ variables say.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

var exe = map[bool]string{true: ".exe"}[runtime.GOOS == "windows"]

func TestMain(m *testing.M) {
	switch name := strings.TrimSuffix(filepath.Base(os.Args[0]), exe); {
	case name == "qemu":
		main()
	case strings.HasPrefix(name, "qemu-system-"):
		fakeqemu()
	default:
		os.Exit(m.Run())
	}
}

// record is one run of the fake QEMU.
type record struct {
	Argv   []string
	Cwd    string
	Pid    int
	Stdin  string // probes only
	Signal string // set if it got the signal it waited for
}

func isprobe(args []string) bool {
	for _, a := range args {
		if a == "-qmp" {
			return true
		}
	}
	return false
}

func fakeqemu() {
	r := record{Argv: os.Args, Pid: os.Getpid()}
	r.Cwd, _ = os.Getwd()
	code := 0
	if isprobe(os.Args) {
		b, _ := io.ReadAll(os.Stdin)
		r.Stdin = string(b)
		switch os.Getenv("FAKE_PROBE") {
		case "ok":
		case "hang":
			logrecord(r)
			time.Sleep(time.Hour)
		default:
			code = 1
		}
	} else {
		os.Stdout.WriteString(os.Getenv("FAKE_PRINT"))
		if os.Getenv("FAKE_WAITSIG") != "" {
			r.Signal = waitsignal()
			code = 42
		}
		if s := os.Getenv("FAKE_EXIT"); s != "" {
			code, _ = strconv.Atoi(s)
		}
	}
	logrecord(r)
	os.Exit(code)
}

func logrecord(r record) {
	f, err := os.OpenFile(os.Getenv("FAKE_LOG"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil {
		panic(err)
	}
	json.NewEncoder(f).Encode(r)
	f.Close()
}

// world is a checkout with the launcher in it and a fake QEMU on PATH.
type world struct {
	t       *testing.T
	top     string
	bin     string
	log     string
	launch  string
	env     []string
	signals func(*exec.Cmd) // set by signal tests
}

func newworld(t *testing.T) *world {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir()) // /var is /private/var on macOS
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, top: filepath.Join(tmp, "top"), bin: filepath.Join(tmp, "bin"), log: filepath.Join(tmp, "log")}
	w.launch = filepath.Join(w.top, "boot", "qemu"+exe)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyfile(t, self, w.launch)
	for _, q := range []string{"x86_64", "aarch64", "riscv64"} {
		copyfile(t, self, filepath.Join(w.bin, "qemu-system-"+q+exe))
	}
	for _, kv := range os.Environ() {
		switch strings.ToUpper(kv[:strings.IndexByte(kv+"=", '=')]) {
		case "PATH", "ROOT", "KERNEL", "MEM", "SMP", "SERIAL", "SERVICE":
			continue
		}
		if !strings.HasPrefix(kv, "FAKE_") {
			w.env = append(w.env, kv)
		}
	}
	w.env = append(w.env, "PATH="+w.bin, "FAKE_LOG="+w.log)
	return w
}

func copyfile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(to), 0777)
	}
	if err == nil {
		err = os.WriteFile(to, b, 0777)
	}
	if err != nil {
		t.Fatal(err)
	}
}

type result struct {
	stdout, stderr string
	code           int
	runs           []record
}

// probes and boots split the fake QEMU's runs by kind.
func (r *result) probes() (p []record) {
	for _, x := range r.runs {
		if isprobe(x.Argv) {
			p = append(p, x)
		}
	}
	return p
}

func (r *result) boots() (b []record) {
	for _, x := range r.runs {
		if !isprobe(x.Argv) {
			b = append(b, x)
		}
	}
	return b
}

// run runs the launcher (or path, if not "") from dir with env added.
func (w *world) run(path, dir string, env []string, args ...string) *result {
	w.t.Helper()
	if path == "" {
		path = w.launch
	}
	os.Remove(w.log)
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, w.env...), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		w.t.Fatal(err)
	}
	if w.signals != nil {
		w.signals(cmd)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		w.t.Fatalf("launcher %q did not exit", args)
	}
	r := &result{stdout: stdout.String(), stderr: stderr.String()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.code = ee.ExitCode()
	} else if err != nil {
		w.t.Fatal(err)
	}
	if f, err := os.Open(w.log); err == nil {
		s := bufio.NewScanner(f)
		for s.Scan() {
			var x record
			if err := json.Unmarshal(s.Bytes(), &x); err != nil {
				w.t.Fatal(err)
			}
			r.runs = append(r.runs, x)
		}
		f.Close()
	}
	return r
}

// want is what plan says the launcher should run on this host.
func (w *world) want(env map[string]string, probe bool, args ...string) *Plan {
	w.t.Helper()
	p, err := plan(Input{
		Args:  args,
		Env:   func(k string) string { return env[k] },
		Top:   w.top,
		Host:  Host{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Probe: func(string, []string) bool { return probe },
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

func kv(m map[string]string) (r []string) {
	for k, v := range m {
		r = append(r, k+"="+v)
	}
	return r
}

func TestRunsPlan(t *testing.T) {
	w := newworld(t)
	for _, probe := range []string{"ok", "fail"} {
		for _, args := range [][]string{{"-amd64"}, {"-386", "-nonet", "-smp", "2"}, {"-riscv64", "-cpuwait"}, {"-amd64", "-kitty"}} {
			r := w.run("", t.TempDir(), []string{"FAKE_PROBE=" + probe, "FAKE_PRINT=console"}, args...)
			p := w.want(nil, probe == "ok", args...)
			if r.code != 0 {
				t.Fatalf("%q: exit %d, stderr %q", args, r.code, r.stderr)
			}
			b := r.boots()
			if len(b) != 1 {
				t.Fatalf("%q: %d boots", args, len(b))
			}
			if got := strings.TrimSuffix(filepath.Base(b[0].Argv[0]), exe); got != p.Qemu {
				t.Errorf("%q: ran %q, want %q", args, got, p.Qemu)
			}
			if !reflect.DeepEqual(b[0].Argv[1:], p.Args) {
				t.Errorf("%q: args\n got %q\nwant %q", args, b[0].Argv[1:], p.Args)
			}
			if r.stderr != p.Notice+"\n" {
				t.Errorf("%q: stderr %q, want %q", args, r.stderr, p.Notice+"\n")
			}
			wantout := "console"
			if p.Kitty {
				wantout += kittyrestore
			}
			if r.stdout != wantout {
				t.Errorf("%q: stdout %q, want %q", args, r.stdout, wantout)
			}
		}
	}
}

func TestProbeRuns(t *testing.T) {
	w := newworld(t)
	host := Host{OS: runtime.GOOS, Arch: runtime.GOARCH}
	guest := map[string]string{"amd64": "-amd64", "386": "-386", "arm64": "-arm64"}[host.Arch]
	if guest == "" || hwaccel(host, guest[1:]) == "" {
		t.Skipf("no accelerator to probe for on %v", host)
	}
	r := w.run("", w.top, []string{"FAKE_PROBE=ok"}, guest)
	p := r.probes()
	if len(p) != 1 {
		t.Fatalf("%d probes", len(p))
	}
	if want := "{\"execute\":\"qmp_capabilities\"}\n{\"execute\":\"quit\"}\n"; p[0].Stdin != want {
		t.Errorf("probe stdin %q, want %q", p[0].Stdin, want)
	}
	if !strings.Contains(r.stderr, "hardware acceleration") {
		t.Errorf("stderr %q", r.stderr)
	}
}

// A probe that hangs must not hang the launcher.
func TestProbeTimeout(t *testing.T) {
	w := newworld(t)
	t.Setenv("FAKE_LOG", w.log)
	t.Setenv("FAKE_PROBE", "hang")
	defer func(d time.Duration) { probetimeout = d }(probetimeout)
	probetimeout = 200 * time.Millisecond
	start := time.Now()
	ok := probe(filepath.Join(w.bin, "qemu-system-x86_64"+exe), []string{"-qmp", "stdio"})
	if ok {
		t.Error("a hung probe succeeded")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("probe took %v", d)
	}
}

func TestProbeResult(t *testing.T) {
	w := newworld(t)
	t.Setenv("FAKE_LOG", w.log)
	for _, tt := range []struct {
		fake string
		want bool
	}{{"ok", true}, {"fail", false}} {
		t.Setenv("FAKE_PROBE", tt.fake)
		if got := probe(filepath.Join(w.bin, "qemu-system-x86_64"+exe), []string{"-qmp", "stdio"}); got != tt.want {
			t.Errorf("probe with FAKE_PROBE=%s = %v", tt.fake, got)
		}
	}
	if probe(filepath.Join(w.bin, "nonexistent"), nil) {
		t.Error("probe of a missing QEMU succeeded")
	}
}

func TestExitStatus(t *testing.T) {
	w := newworld(t)
	for _, args := range [][]string{{"-amd64"}, {"-amd64", "-kitty"}} {
		r := w.run("", w.top, []string{"FAKE_EXIT=7", "FAKE_PRINT=x"}, args...)
		if r.code != 7 {
			t.Errorf("%q: exit %d, want 7", args, r.code)
		}
		// The terminal is restored however QEMU exits.
		if kitty := len(args) > 1; kitty != strings.HasSuffix(r.stdout, kittyrestore) {
			t.Errorf("%q: stdout %q", args, r.stdout)
		}
	}
}

// QEMU runs at the top of the checkout wherever the launcher is run
// from, so that relative paths in the environment are from there.
func TestRunsAtTop(t *testing.T) {
	w := newworld(t)
	env := map[string]string{"KERNEL": "amd64/k", "ROOT": "."}
	r := w.run("", t.TempDir(), kv(env), "-amd64")
	b := r.boots()
	if r.code != 0 || len(b) != 1 {
		t.Fatalf("exit %d, %d boots, stderr %q", r.code, len(b), r.stderr)
	}
	if b[0].Cwd != w.top {
		t.Errorf("QEMU ran in %q, want %q", b[0].Cwd, w.top)
	}
	if p := w.want(env, false, "-amd64"); !reflect.DeepEqual(b[0].Argv[1:], p.Args) {
		t.Errorf("args\n got %q\nwant %q", b[0].Argv[1:], p.Args)
	}
}

func TestWritesIni(t *testing.T) {
	w := newworld(t)
	root := t.TempDir()
	env := map[string]string{"ROOT": root}
	r := w.run("", w.top, kv(env), "-arm64")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	b, err := os.ReadFile(filepath.Join(root, ".qemu.ini"))
	if err != nil {
		t.Fatal(err)
	}
	if p := w.want(env, false, "-arm64"); string(b) != p.Ini {
		t.Errorf("ini %q, want %q", b, p.Ini)
	}

	// QEMU is not started without its plan9.ini.
	r = w.run("", w.top, []string{"ROOT=" + filepath.Join(root, "missing")}, "-arm64")
	if r.code != 1 || len(r.runs) != 0 || !strings.HasPrefix(r.stderr, "boot/qemu: ") {
		t.Errorf("exit %d, %d runs, stderr %q", r.code, len(r.runs), r.stderr)
	}
}

func TestErrorsExit(t *testing.T) {
	w := newworld(t)
	for _, tt := range []struct {
		args   []string
		path   string // PATH, if not the fake QEMU's
		stderr string // prefix
	}{
		{args: []string{"-h"}, stderr: wantusage + "\n"},
		{args: []string{"-arm64", "-vga"}, stderr: "boot/qemu: -vga is for x86 kernels\n"},
		{args: []string{"-amd64"}, path: t.TempDir(), stderr: "boot/qemu: software emulation"},
	} {
		var env []string
		if tt.path != "" {
			env = []string{"PATH=" + tt.path}
		}
		r := w.run("", w.top, env, tt.args...)
		if r.code != 1 || !strings.HasPrefix(r.stderr, tt.stderr) {
			t.Errorf("%q: exit %d, stderr %q, want prefix %q", tt.args, r.code, r.stderr, tt.stderr)
		}
		if tt.path != "" && !strings.Contains(r.stderr, "\nboot/qemu: ") {
			t.Errorf("%q: missing QEMU not reported: %q", tt.args, r.stderr)
		}
	}
}
