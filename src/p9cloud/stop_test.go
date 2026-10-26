package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const amdStopStatus = "docker ps --all --filter name=^/p9lab-amd$ --format {{.State}}"
const authStopStatus = "docker ps --all --filter name=^/p9lab-auth$ --format {{.State}}"

func TestStopHoldsMonitorUntilConfirmed(t *testing.T) {
	path := testLab(t)
	dir := filepath.Dir(path)
	fakeConsole(t, filepath.Join(dir, "state/amd/serial.sock"), func(string) string {
		return "done halting\r\n"
	})
	connected := make(chan net.Conn, 1)
	done := make(chan struct{})
	defer close(done)
	rawSocket(t, filepath.Join(dir, "state/amd/monitor.sock"), func(c net.Conn) {
		io.WriteString(c, "QEMU monitor\r\n(qemu) ")
		line, err := bufio.NewReader(c).ReadString('\n')
		if err != nil || line != "quit\n" {
			return
		}
		connected <- c
		<-done
	})
	f := newFake(t)
	run := f.run
	polls := 0
	var peer net.Conn
	f.run = func(extra []string, argv ...string) (string, error) {
		if strings.Join(argv, " ") == amdStopStatus {
			polls++
			f.reply[amdStopStatus] = "running\n"
			if polls == 3 {
				select {
				case peer = <-connected:
				case <-time.After(time.Second):
					return "", errors.New("monitor did not receive quit")
				}
				peer.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
				_, err := peer.Read(make([]byte, 1))
				if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
					return "", errors.New("monitor closed before Docker confirmed stop")
				}
				f.reply[amdStopStatus] = ""
			}
		}
		return run(extra, argv...)
	}
	if st := f.p9cloud("-f", path, "stop", "amd"); st != 0 {
		t.Fatalf("status %d: %s", st, f.err.String())
	}
	peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("monitor not closed after stop confirmation: %v", err)
	}
}

// stopStates answers consecutive status polls, repeating the last state.
func stopStates(f *fake, command string, states ...string) {
	run := f.run
	f.run = func(extra []string, argv ...string) (string, error) {
		if strings.Join(argv, " ") == command {
			f.reply[command] = states[0]
			if len(states) > 1 {
				states = states[1:]
			}
		}
		return run(extra, argv...)
	}
}

func TestStopStatusErrors(t *testing.T) {
	for _, tc := range []struct {
		name, out, want string
		err             error
	}{
		{"daemon", "", "daemon unavailable", errors.New("daemon unavailable")},
		{"partial error", "exited\n", "daemon unavailable", errors.New("daemon unavailable")},
		{"invalid", "unexpected\n", "unexpected container state", nil},
		{"multiple", "running\nexited\n", "unexpected container state", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, path := newFake(t), testLab(t)
			f.reply[amdStopStatus], f.fail[amdStopStatus] = tc.out, tc.err
			if st := f.p9cloud("-f", path, "stop", "amd"); st != 1 || !strings.Contains(f.err.String(), tc.want) {
				t.Fatalf("status %d: %q", st, f.err.String())
			}
			if len(f.ran) != 1 || strings.Contains(f.out.String(), "not running") {
				t.Fatalf("unconfirmed stop: ran %q, out %q", f.ran, f.out.String())
			}
		})
	}
}

func TestStopAlreadyStopped(t *testing.T) {
	for _, state := range []string{"", "created\n", "exited\n", "dead\n"} {
		t.Run(state, func(t *testing.T) {
			f, path := newFake(t), testLab(t)
			f.reply[amdStopStatus] = state
			if st := f.p9cloud("-f", path, "stop", "amd"); st != 0 || f.out.String() != "amd is not running\n" {
				t.Fatalf("status %d: %q %q", st, f.out.String(), f.err.String())
			}
			if len(f.ran) != 1 {
				t.Fatalf("ran %q", f.ran)
			}
		})
	}
}

func TestStopGuestSelfExit(t *testing.T) {
	for _, state := range []string{"", "exited\n", "dead\n", "daemon error"} {
		t.Run(state, func(t *testing.T) {
			path := testLab(t)
			dir := filepath.Dir(path)
			con := fakeConsole(t, filepath.Join(dir, "state/amd/serial.sock"), func(string) string {
				return "done halting\r\n"
			})
			attempted := make(chan struct{}, 1)
			rawSocket(t, filepath.Join(dir, "state/amd/monitor.sock"), func(net.Conn) {
				attempted <- struct{}{}
			})
			f := newFake(t)
			stopStates(f, amdStopStatus, "running\n", state)
			run := f.run
			f.run = func(extra []string, argv ...string) (string, error) {
				out, err := run(extra, argv...)
				if out == "daemon error" {
					return "", errors.New("daemon unavailable")
				}
				return out, err
			}
			st := f.p9cloud("-f", path, "stop", "amd")
			if state == "daemon error" {
				if st != 1 || !strings.Contains(f.err.String(), "daemon unavailable") {
					t.Fatalf("status %d: %q", st, f.err.String())
				}
			} else if st != 0 {
				t.Fatalf("status %d: %q", st, f.err.String())
			}
			if len(f.ran) != 2 || con.String() != "fshalt\n" || f.slept != 2*time.Second {
				t.Fatalf("ran %q, console %q, slept %v", f.ran, con.String(), f.slept)
			}
			select {
			case <-attempted:
				t.Fatal("contacted monitor after guest self-exit or failed status check")
			default:
			}
		})
	}
}

func TestStopMonitorSelfExitRace(t *testing.T) {
	for _, kind := range []string{"dial", "read", "write"} {
		for _, state := range []string{"", "exited\n", "dead\n", "daemon error"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				done := make(chan struct{})
				defer close(done)
				path := testLab(t)
				dir := filepath.Dir(path)
				fakeConsole(t, filepath.Join(dir, "state/amd/serial.sock"), func(string) string {
					return "done halting\r\n"
				})
				if kind != "dial" {
					rawSocket(t, filepath.Join(dir, "state/amd/monitor.sock"), func(c net.Conn) {
						if kind == "write" {
							c.(*net.UnixConn).CloseRead()
							io.WriteString(c, "QEMU monitor\r\n(qemu) ")
							<-done
						}
					})
				}
				f := newFake(t)
				stopStates(f, amdStopStatus, "running\n", "running\n", state)
				run := f.run
				f.run = func(extra []string, argv ...string) (string, error) {
					out, err := run(extra, argv...)
					if out == "daemon error" {
						return "", errors.New("daemon unavailable")
					}
					return out, err
				}
				st := f.p9cloud("-f", path, "stop", "amd")
				if state == "daemon error" {
					if st != 1 || !strings.Contains(f.err.String(), "daemon unavailable") {
						t.Fatalf("status %d: %q", st, f.err.String())
					}
				} else if st != 0 {
					t.Fatalf("status %d: %q", st, f.err.String())
				}
				if len(f.ran) != 3 {
					t.Fatalf("did not recheck status after monitor failure: %q", f.ran)
				}
			})
		}
	}
}

func TestStopMonitorErrors(t *testing.T) {
	for _, kind := range []string{"dial", "closed", "partial", "silent", "write"} {
		t.Run(kind, func(t *testing.T) {
			done := make(chan struct{})
			defer close(done)
			path := testLab(t)
			dir := filepath.Dir(path)
			fakeConsole(t, filepath.Join(dir, "state/amd/serial.sock"), func(string) string {
				return "done halting\r\n"
			})
			if kind != "dial" {
				rawSocket(t, filepath.Join(dir, "state/amd/monitor.sock"), func(c net.Conn) {
					switch kind {
					case "partial":
						io.WriteString(c, "QEMU monitor\r\n(qe")
					case "silent":
						io.Copy(io.Discard, c)
					case "write":
						c.(*net.UnixConn).CloseRead()
						io.WriteString(c, "QEMU monitor\r\n(qemu) ")
						<-done
					}
				})
			}
			f := newFake(t)
			f.reply[amdStopStatus] = "running\n"
			want := "QEMU monitor greeting:"
			if kind == "dial" {
				want = "QEMU monitor:"
			} else if kind == "write" {
				want = "QEMU monitor quit:"
			}
			if st := f.p9cloud("-f", path, "stop", "-t", "1", "amd"); st != 1 || !strings.Contains(f.err.String(), want) {
				t.Fatalf("status %d: %q", st, f.err.String())
			}
			if len(f.ran) != 3 {
				t.Fatalf("did not recheck status after monitor failure: %q", f.ran)
			}
		})
	}
}

func TestStopZeroTimeout(t *testing.T) {
	f, path := newFake(t), testLab(t)
	f.reply[authStopStatus] = "running\n"
	if st := f.p9cloud("-f", path, "stop", "-t", "0", "auth"); st != 1 ||
		!strings.Contains(f.err.String(), "not confirmed stopped after 0s") {
		t.Fatalf("status %d: %q", st, f.err.String())
	}
	if f.slept != 0 || len(f.ran) != 3 {
		t.Fatalf("slept %v, ran %q", f.slept, f.ran)
	}
}

func TestStopExactContainerName(t *testing.T) {
	f := newFake(t)
	path := testLab(t, "container=p9lab-auth", "container=p9lab.auth")
	if st := f.p9cloud("-f", path, "stop", "auth"); st != 0 {
		t.Fatalf("status %d: %q", st, f.err.String())
	}
	if len(f.ran) != 1 || strings.Join(f.ran[0], " ") != `docker ps --all --filter name=^/p9lab\.auth$ --format {{.State}}` {
		t.Fatalf("not an exact container-name filter: %q", f.ran)
	}
}

func TestStopConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, state, want string
		err               error
	}{
		{"auto removed", "", "", nil},
		{"exited", "exited\n", "", nil},
		{"running", "running\n", "not confirmed stopped after", nil},
		{"paused", "paused\n", "not confirmed stopped after", nil},
		{"restarting", "restarting\n", "not confirmed stopped after", nil},
		{"removing", "removing\n", "not confirmed stopped after", nil},
		{"daemon", "", "daemon unavailable", errors.New("daemon unavailable")},
		{"invalid", "unknown\n", "unexpected container state", nil},
	} {
		for _, name := range []string{"amd", "auth"} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				path := testLab(t)
				f := newFake(t)
				status := authStopStatus
				baseSleep := time.Duration(0)
				var mon *syncBuf
				if name == "amd" {
					status = amdStopStatus
					baseSleep = 2 * time.Second
					dir := filepath.Dir(path)
					fakeConsole(t, filepath.Join(dir, "state/amd/serial.sock"), func(string) string {
						return "done halting\r\n"
					})
					mon = fakeMonitor(t, filepath.Join(dir, "state/amd/monitor.sock"))
				}
				run := f.run
				polls := 0
				beforeQuit := 1
				if name == "amd" {
					beforeQuit = 2
				}
				f.run = func(extra []string, argv ...string) (string, error) {
					if strings.Join(argv, " ") == status {
						polls++
						f.reply[status] = "running\n"
						if polls > beforeQuit {
							f.reply[status], f.fail[status] = tc.state, tc.err
						}
					}
					return run(extra, argv...)
				}
				st := f.p9cloud("-f", path, "stop", "-t", "1.2", name)
				if (st == 0) != (tc.want == "") || !strings.Contains(f.err.String(), tc.want) {
					t.Fatalf("status %d: %q", st, f.err.String())
				}
				if polls < 2 {
					t.Fatalf("did not confirm status: %q", f.ran)
				}
				if tc.want == "not confirmed stopped after" && f.slept != baseSleep+1200*time.Millisecond {
					t.Errorf("slept %v, want %v", f.slept, baseSleep+1200*time.Millisecond)
				}
				if mon != nil {
					waitFor(t, "quit", func() bool { return mon.String() == "quit\n" })
				}
				for _, argv := range f.ran {
					cmd := strings.Join(argv, " ")
					if strings.Contains(cmd, "docker wait") || strings.Contains(cmd, "docker kill") || strings.Contains(cmd, "docker rm") {
						t.Errorf("unexpected stop mechanism: %s", cmd)
					}
				}
			})
		}
	}
}
