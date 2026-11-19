//go:build unix

package main

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// waitsignal is the fake QEMU waiting to be told to quit.  It creates
// $FAKE_LOG.ready once it is listening.
func waitsignal() string {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGHUP)
	os.WriteFile(os.Getenv("FAKE_LOG")+".ready", nil, 0666)
	select {
	case s := <-c:
		return s.String()
	case <-time.After(20 * time.Second):
		return ""
	}
}

// Without -kitty the launcher becomes QEMU, so that signals and
// expect(1) reach QEMU directly.
func TestExecs(t *testing.T) {
	w := newworld(t)
	var pid int
	w.signals = func(cmd *exec.Cmd) { pid = cmd.Process.Pid }
	r := w.run("", w.top, nil, "-amd64")
	if b := r.boots(); len(b) != 1 || b[0].Pid != pid {
		t.Errorf("QEMU is not the launcher's process %d: %+v", pid, b)
	}
}

// With -kitty the launcher waits for QEMU to restore the terminal,
// so it hands on the signals that would end it.
func TestKittyForwardsSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		w := newworld(t)
		w.signals = func(cmd *exec.Cmd) {
			for i := 0; ; i++ {
				if _, err := os.Stat(w.log + ".ready"); err == nil {
					break
				}
				if i == 2000 {
					t.Fatal("fake QEMU never got ready")
				}
				time.Sleep(10 * time.Millisecond)
			}
			cmd.Process.Signal(sig)
		}
		r := w.run("", w.top, []string{"FAKE_WAITSIG=1"}, "-amd64", "-kitty")
		b := r.boots()
		if len(b) != 1 || b[0].Signal != sig.String() {
			t.Fatalf("%v: QEMU runs %+v", sig, b)
		}
		if r.code != 42 || r.stdout != kittyrestore {
			t.Errorf("%v: exit %d, stdout %q", sig, r.code, r.stdout)
		}
	}
}

// A symlink to boot/qemu, say in ~/bin, still finds its checkout.
func TestSymlink(t *testing.T) {
	w := newworld(t)
	link := filepath.Join(t.TempDir(), "qemu") // the name picks the role in TestMain
	if err := os.Symlink(w.launch, link); err != nil {
		t.Fatal(err)
	}
	r := w.run(link, t.TempDir(), nil, "-amd64")
	b := r.boots()
	if r.code != 0 || len(b) != 1 {
		t.Fatalf("exit %d, %d boots, stderr %q", r.code, len(b), r.stderr)
	}
	if b[0].Cwd != w.top {
		t.Errorf("QEMU ran in %q, want %q", b[0].Cwd, w.top)
	}
}
