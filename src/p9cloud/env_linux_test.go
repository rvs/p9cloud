package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

func TestRun(t *testing.T) {
	out, err := run(nil, "sh", "-c", "echo out; echo err >&2")
	if out != "out\n" || err != nil {
		t.Errorf("run = %q, %v", out, err)
	}
	out, err = run([]string{"X=1"}, "sh", "-c", "echo $X; echo oops >&2; exit 3")
	if out != "1\n" || err == nil || err.Error() != "exit status 3: oops" {
		t.Errorf("run = %q, %v", out, err)
	}
	if _, err := run(nil, "sh", "-c", "exit 1"); err == nil || err.Error() != "exit status 1" {
		t.Errorf("run = %v", err)
	}
	if _, err := run(nil, "/nonexistent"); err == nil {
		t.Error("ran /nonexistent")
	}
	if environ(nil) != nil {
		t.Error("environ(nil) is not nil: the command would get no environment")
	}
}

// TestExecv runs this test binary again to exec from it.
func TestExecv(t *testing.T) {
	if os.Getenv("P9CLOUD_EXECV") != "" {
		err := execv([]string{"PASS=secret"}, "sh", "-c", `echo "$PASS $P9CLOUD_EXECV"`)
		t.Fatalf("execv: %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestExecv$")
	cmd.Env = append(os.Environ(), "P9CLOUD_EXECV=kept")
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "secret kept\n" {
		t.Errorf("exec: %q, %v", out, err)
	}
	if err := execv(nil, "/nonexistent/x"); err == nil {
		t.Error("exec of /nonexistent/x")
	}
}

func openpty(t *testing.T) (*os.File, *os.File) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { m.Close() })
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Fatal(e)
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		t.Fatal(e)
	}
	s, err := os.OpenFile(filepath.Join("/dev/pts", strconv.Itoa(int(n))), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return m, s
}

func TestRawtty(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if restore, err := rawtty(int(f.Fd())); restore != nil || err != nil {
		t.Errorf("a file is a terminal: %v", err)
	}
	_, s := openpty(t)
	var before, raw, after syscall.Termios
	ioctl(int(s.Fd()), syscall.TCGETS, &before)
	if before.Lflag&syscall.ICANON == 0 {
		t.Fatal("a new pty is not canonical")
	}
	restore, err := rawtty(int(s.Fd()))
	if restore == nil || err != nil {
		t.Fatalf("rawtty: %v", err)
	}
	ioctl(int(s.Fd()), syscall.TCGETS, &raw)
	if raw.Lflag&(syscall.ICANON|syscall.ECHO|syscall.ISIG) != 0 || raw.Iflag&syscall.ICRNL != 0 ||
		raw.Oflag&syscall.OPOST != 0 || raw.Cflag&syscall.CSIZE != syscall.CS8 || raw.Cc[syscall.VMIN] != 1 {
		t.Errorf("not raw: %+v", raw)
	}
	restore()
	ioctl(int(s.Fd()), syscall.TCGETS, &after)
	if after != before {
		t.Errorf("not restored:\n%+v\n%+v", after, before)
	}
}

func TestRealEnv(t *testing.T) {
	e := realEnv()
	if e.run == nil || e.exec == nil || e.rawtty == nil || e.serve == nil || e.sleep == nil || e.now == nil ||
		e.self == "" || e.sysnet != "/sys/class/net" || e.typing == 0 {
		t.Errorf("realEnv %+v", e)
	}
}

// TestBinary builds p9cloud and runs it as a user would.
func TestBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds p9cloud")
	}
	bin := filepath.Join(t.TempDir(), "p9cloud")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	path := testLab(t)
	out, err := exec.Command(bin, "-f", path, "show", "amd").Output()
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "show-amd", string(out), filepath.Dir(path))
	cmd := exec.Command(bin, "-f", path, "nope")
	out, err = cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 || !strings.HasPrefix(string(out), "p9cloud: nope: no such command\nusage:") {
		t.Errorf("nope: %v %q", err, out)
	}
	cmd = exec.Command(bin, "-f", path, "show", "x")
	out, err = cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 || string(out) != "p9cloud: x: not one of: fs amd rv auth host\n" {
		t.Errorf("show x: %v %q", err, out)
	}
}
