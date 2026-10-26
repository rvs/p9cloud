package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"p9cloud/ndb"
)

func seconds(t float64) time.Duration { return time.Duration(t * float64(time.Second)) }

// runx runs a command; an error says which.
func (x *ctx) runx(extra []string, argv ...string) (string, error) {
	out, err := x.e.run(extra, argv...)
	if err != nil {
		return out, fmt.Errorf("%s: %v", shjoin(argv), err)
	}
	return out, nil
}

// again is how to run p9cloud again with another command.
func (x *ctx) again(args string) string {
	return shquote(x.e.self) + " -f " + shquote(x.lab.Path) + " " + args
}

func (x *ctx) running(s *System) bool {
	switch s.Kind {
	case "qemu":
		c, err := net.DialTimeout("unix", filepath.Join(s.State(), "monitor.sock"), 2*time.Second)
		if err != nil {
			return false
		}
		c.Close()
		return true
	case "container":
		c, err := s.Need("container")
		if err != nil {
			return false
		}
		out, _ := x.e.run(nil, "docker", "inspect", "-f", "{{.State.Running}}", c)
		return strings.TrimSpace(out) == "true"
	}
	return false
}

func (x *ctx) exists(name string) bool {
	_, err := os.Stat(filepath.Join(x.e.sysnet, name))
	return err == nil
}

func (x *ctx) printf(format string, a ...any) { fmt.Fprintf(x.e.stdout, format, a...) }

func cmdList(x *ctx) error {
	lab := x.lab
	units := []string{lab.Prefix + "-network.service"}
	for _, s := range lab.Systems {
		units = append(units, s.Unit())
	}
	out, _ := x.e.run(nil, append([]string{"systemctl", "is-active"}, units...)...)
	active := strings.Fields(out)
	if len(active) != len(units) {
		active = make([]string, len(units))
		for i := range active {
			active[i] = "?"
		}
	}
	br := lab.Host.Or("bridge", "")
	up := "missing"
	if x.exists(br) {
		up = "up"
	}
	rows := [][]string{
		{"SYS", "KIND", "IP", "RUNNING", "UNIT"},
		{lab.Host.Name, "host", lab.Host.Or("ip", ""), "bridge " + br + " " + up, units[0] + " " + active[0]},
	}
	for i, s := range lab.Systems {
		r := "no"
		if x.running(s) {
			r = "yes"
		}
		rows = append(rows, []string{s.Name, s.Kind, s.Or("ip", ""), r, units[i+1] + " " + active[i+1]})
	}
	w := make([]int, 4)
	for _, r := range rows {
		for i := range w {
			w[i] = max(w[i], len(r[i]))
		}
	}
	for _, r := range rows {
		for i := range w {
			x.printf("%-*s  ", w[i], r[i])
		}
		x.printf("%s\n", r[4])
	}
	dom, err := lab.AuthDom()
	if err != nil {
		return err
	}
	auth := lab.Host.Or("auth", "")
	x.printf("\nauthdom %s, auth server %s", dom, auth)
	for _, s := range lab.At(auth) {
		x.printf(" (%s)", s.Name)
	}
	x.printf("\n")
	return nil
}

// column prints lines of a left column and a right one.
func (x *ctx) column(left, right []string) {
	w := 0
	for _, l := range left {
		w = max(w, len(l))
	}
	for i, l := range left {
		x.printf("%s\n", strings.TrimRight(fmt.Sprintf("\t%-*s%s", w+2, l, right[i]), " "))
	}
}

func cmdShow(x *ctx) error {
	lab := x.lab
	s, err := lab.System(x.args[0], "qemu", "container", "host")
	if err != nil {
		return err
	}
	file := s.Entry.File
	if r, err := filepath.Rel(lab.Dir, file); err == nil && !strings.HasPrefix(r, "..") {
		file = r
	}
	kind := ""
	if s.Kind != "" {
		kind = ", " + s.Kind
	}
	x.printf("sys=%s: %s:%d%s\n", s.Name, file, s.Entry.Line, kind)
	var left, right []string
	for _, t := range s.Info {
		left = append(left, t.Attr+"="+ndb.FormatVal(t.Val))
		switch t.Entry {
		case s.Entry:
			right = append(right, "")
		case nil:
			right = append(right, "from default")
		default:
			right = append(right, "from "+t.Entry.String())
		}
	}
	x.column(left, right)
	if s.Kind == "qemu" {
		ks, err := s.Kargs(x.setup, x.k)
		if err != nil {
			return err
		}
		left, right = nil, nil
		for _, k := range ks {
			left = append(left, k.Name+"="+rcquote(k.Val))
			right = append(right, k.Where)
		}
		x.printf("kernel:\n")
		x.column(left, right)
	}
	if s.Kind != "" {
		argv, err := s.Command(x.setup, x.k, x.extra)
		if err != nil {
			return err
		}
		needs, err := s.Needs()
		if err != nil {
			return err
		}
		x.printf("command:\n\t%s\nunit: %s", shjoin(argv), s.Unit())
		for i, n := range needs {
			if i == 0 {
				x.printf(", needs")
			}
			x.printf(" %s", n.Unit())
		}
		x.printf("\n")
	}
	return nil
}

func cmdQuery(x *ctx) error {
	rattrs := x.args[2:]
	if len(rattrs) == 0 {
		rattrs = x.db.Attrs()
	}
	x.printf("%s\n", ndb.Format(x.db.IPInfo(x.args[0], x.args[1], rattrs...)))
	return nil
}

func cmdStart(x *ctx) error {
	s, err := x.lab.System(x.args[0])
	if err != nil {
		return err
	}
	argv, err := s.Command(x.setup, x.k, x.extra)
	if err != nil {
		return err
	}
	if x.running(s) {
		return fmt.Errorf("%s is already running", s.Name)
	}
	files, err := s.Files()
	if err != nil {
		return err
	}
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			return fmt.Errorf("%s: %s: no such file", s.Name, f)
		}
	}
	if s.Kind == "qemu" {
		if tap, _ := s.Get("tap"); tap != "" && !x.exists(tap) {
			return fmt.Errorf("%s: no tap %s: sudo %s", s.Name, tap, x.again("net"))
		}
		x.e.umask(0o077)
		if err := os.MkdirAll(s.State(), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(s.State(), "console.log"), nil, 0o600); err != nil {
			return err
		}
	} else {
		x.e.run(nil, "docker", "rm", "-f", s.Or("container", ""))
	}
	fmt.Fprintln(x.e.stderr, shjoin(argv))
	return x.e.exec(nil, argv...)
}

func cmdWait(x *ctx) error {
	s, err := x.lab.System(x.args[0])
	if err != nil {
		return err
	}
	ip, err := s.Need("ip")
	if err != nil {
		return err
	}
	var ports []int
	for _, r := range s.All("ready") {
		p, err := x.lab.Port(r)
		if err != nil {
			return err
		}
		ports = append(ports, p)
	}
	deadline := x.e.now().Add(seconds(x.timeout))
	for _, p := range ports {
		addr := net.JoinHostPort(ip, strconv.Itoa(p))
		for {
			c, err := net.DialTimeout("tcp", addr, time.Second)
			if err == nil {
				c.Close()
				break
			}
			if x.e.now().After(deadline) {
				return fmt.Errorf("%s: nothing on %s after %ss", s.Name, addr, strconv.FormatFloat(x.timeout, 'g', -1, 64))
			}
			x.e.sleep(500 * time.Millisecond)
		}
	}
	return nil
}

func cmdStop(x *ctx) error {
	s, err := x.lab.System(x.args[0])
	if err != nil {
		return err
	}
	if !x.running(s) {
		x.printf("%s is not running\n", s.Name)
		return nil
	}
	if s.Kind == "container" {
		c, err := s.Need("container")
		if err != nil {
			return err
		}
		_, err = x.runx(nil, "docker", "stop", c)
		return err
	}
	c, err := x.e.dialSerial(s)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := io.WriteString(c, "fshalt\n"); err != nil {
		return err
	}
	if _, _, err := c.expect(contains("\ndone halting", false), seconds(x.timeout), x.e.stdout); err != nil {
		return fmt.Errorf("%s did not confirm a clean halt; NOT killing QEMU. Detach other console clients, check the guest.", s.Name)
	}
	x.e.sleep(2 * time.Second)
	m, err := net.DialTimeout("unix", filepath.Join(s.State(), "monitor.sock"), 2*time.Second)
	if err != nil {
		return nil
	}
	defer m.Close()
	m.SetDeadline(time.Now().Add(2 * time.Second))
	m.Read(make([]byte, 8192))
	io.WriteString(m, "quit\n")
	x.e.sleep(200 * time.Millisecond)
	return nil
}

func cmdCpu(x *ctx) error {
	lab := x.lab
	s, err := lab.System(x.args[0], "qemu")
	if err != nil {
		return err
	}
	h := lab.Host
	user := x.user
	if user == "" {
		user = h.Or("user", "")
	}
	if user == "" {
		user = x.e.getenv("USER")
	}
	ip, err := s.Need("ip")
	if err != nil {
		return err
	}
	auth, err := s.Need("auth")
	if err != nil {
		return err
	}
	rcpu, err := lab.Port("rcpu")
	if err != nil {
		return err
	}
	ticket, err := lab.Port("ticket")
	if err != nil {
		return err
	}
	pw, err := lab.Password(user, false)
	if err != nil {
		return err
	}
	term := lab.File(h.Or("terminal", "terminal"))
	if err := os.MkdirAll(term, 0o755); err != nil {
		return err
	}
	argv := []string{lab.Prog(h.Or("drawterm", "drawterm")), "-G",
		"-h", fmt.Sprintf("tcp!%s!%d", ip, rcpu),
		"-a", fmt.Sprintf("tcp!%s!%d", auth, ticket),
		"-u", user, "-r", term}
	if len(x.args) > 1 {
		argv = append(argv, "-c", strings.Join(x.args[1:], " "))
	}
	return x.e.exec([]string{"PASS=" + pw}, argv...)
}

func cmdCmd(x *ctx) error {
	s, err := x.lab.System(x.args[0], "qemu")
	if err != nil {
		return err
	}
	b := make([]byte, 8)
	rand.Read(b)
	marker := "DONE" + hex.EncodeToString(b)
	c, err := x.e.dialSerial(s)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.typeText(strings.Join(x.args[1:], " ") + "; result=$status; echo; echo " + marker + " $result\n"); err != nil {
		return err
	}
	re := regexp.MustCompile(`(?m)^` + marker + `([^\n]*)\n`)
	data, m, err := c.expect(re.FindSubmatchIndex, seconds(x.timeout), nil)
	if err != nil {
		return fmt.Errorf("%s: %v", s.Name, err)
	}
	out := data[:m[0]]
	if _, rest, ok := strings.Cut(string(out), "\n"); ok {
		out = []byte(rest)
	}
	x.e.stdout.Write(out)
	if status := strings.TrimSpace(string(data[m[2]:m[3]])); status != "" {
		return fmt.Errorf("%s: status %s", s.Name, status)
	}
	return nil
}

func cmdConsole(x *ctx) error {
	s, err := x.lab.System(x.args[0])
	if err != nil {
		return err
	}
	if s.Kind == "container" {
		c, err := s.Need("container")
		if err != nil {
			return err
		}
		return x.e.exec(nil, "docker", "logs", "-f", c)
	}
	c, err := x.e.dialSerial(s)
	if err != nil {
		return err
	}
	defer c.Close()
	restore, err := x.e.rawtty()
	if err != nil {
		return err
	}
	if restore != nil {
		fmt.Fprintf(x.e.stderr, "%s console: Enter for a prompt, Ctrl-] detaches; one client at a time.\r\n", s.Name)
		defer func() {
			restore()
			fmt.Fprintln(x.e.stderr)
		}()
	}
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(x.e.stdout, c)
		done <- struct{}{}
	}()
	go func() {
		b := make([]byte, 1024)
		for {
			n, err := x.e.stdin.Read(b)
			if restore != nil {
				if i := strings.IndexByte(string(b[:n]), 0x1d); i >= 0 {
					c.Write(b[:i])
					break
				}
			}
			if n > 0 {
				if _, err := c.Write(b[:n]); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	<-done
	return nil
}

func cmdNet(x *ctx) error {
	lab := x.lab
	h := lab.Host
	br, err := h.Need("bridge")
	if err != nil {
		return err
	}
	ip, err := h.Need("ip")
	if err != nil {
		return err
	}
	addr, ok := ndb.ParseIP(ip)
	if !ok {
		return fmt.Errorf("sys=%s: ip=%s: not an address", h.Name, ip)
	}
	mask, ok := ndb.ParseIPMask(h.Or("ipmask", ""), true)
	if !ok {
		mask = ndb.DefMask(addr)
	}
	cidr := fmt.Sprintf("%s/%d", ip, ndb.PrefixLen(mask)-96)
	owner, _, err := lab.Owner()
	if err != nil {
		return err
	}
	var cmds [][]string
	if !x.exists(br) {
		cmds = append(cmds, []string{"ip", "link", "add", br, "type", "bridge"})
	}
	if out, _ := x.e.run(nil, "ip", "-o", "-4", "addr", "show", "dev", br); !strings.Contains(out, " "+cidr+" ") {
		cmds = append(cmds, []string{"ip", "addr", "add", cidr, "dev", br})
	}
	cmds = append(cmds, []string{"ip", "link", "set", br, "up"})
	for _, s := range lab.Systems {
		tap, _ := s.Get("tap")
		if s.Kind != "qemu" || tap == "" {
			continue
		}
		if !x.exists(tap) {
			cmds = append(cmds, []string{"ip", "tuntap", "add", "dev", tap, "mode", "tap", "user", owner})
		}
		cmds = append(cmds, []string{"ip", "link", "set", tap, "master", br}, []string{"ip", "link", "set", tap, "up"})
	}
	if !x.n && x.e.euid != 0 {
		return fmt.Errorf("net needs root: sudo %s", x.again("net"))
	}
	for _, c := range cmds {
		x.printf("%s\n", shjoin(c))
		if x.n {
			continue
		}
		out, err := x.runx(nil, c...)
		x.printf("%s", out)
		if err != nil {
			return err
		}
	}
	return nil
}

func cmdServe(x *ctx) error {
	h := x.lab.Host
	dir, err := h.Need("httpdir")
	if err != nil {
		return err
	}
	ip, err := h.Need("ip")
	if err != nil {
		return err
	}
	port := h.Or("http", "8082")
	if _, ok := number(port); !ok {
		return fmt.Errorf("sys=%s: http=%s: not a port", h.Name, port)
	}
	dir = x.lab.File(dir)
	addr := net.JoinHostPort(ip, port)
	fmt.Fprintf(x.e.stderr, "serving %s on http://%s/\n", dir, addr)
	return x.e.serve(addr, http.FileServer(http.Dir(dir)))
}

// authCmd runs the auth container's image on its volumes, with $PASS
// passed on if pass is set.
func authCmd(s *System, image string, pass bool, args ...string) []string {
	argv := []string{"docker", "run", "--rm"}
	if pass {
		argv = append(argv, "-e", "PASS")
	}
	for _, v := range s.All("volume") {
		argv = append(argv, "-v", v)
	}
	return append(append(argv, image), args...)
}

// provisionDomain makes the auth container's domain, unless its keys
// hold it already, and writes the nvram of each VM that keeps it on a
// disk of its own.
func (x *ctx) provisionDomain(s *System, image string) error {
	lab := x.lab
	dom, err := lab.AuthDom()
	if err != nil {
		return err
	}
	id := lab.HostID()
	want := fmt.Sprintf("authdom %s, host owner %s", dom, id)
	out, err := x.runx(nil, authCmd(s, image, false, "list")...)
	switch {
	case err != nil && strings.Contains(err.Error(), ": no domain in "):
		pw, err := lab.Password(id, true)
		if err != nil {
			return err
		}
		out, err := x.runx([]string{"PASS=" + pw}, authCmd(s, image, true, "init", id, dom)...)
		x.printf("%s", out)
		if err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if got, _, _ := strings.Cut(out, "\n"); got != want {
			return fmt.Errorf("%s: the keys say %q; the lab wants %s", s.Name, got, want)
		}
	}
	argv := authCmd(s, image, false, "nvram", id)
	key, err := x.runx(nil, argv...)
	if err != nil {
		return err
	}
	if len(key) != 512 {
		return fmt.Errorf("%s: %d bytes, want 512", shjoin(argv), len(key))
	}
	for _, v := range lab.Systems {
		f, err := v.Nvram()
		if err != nil {
			return err
		}
		if f == "" {
			continue
		}
		p := lab.File(f)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(key), 0o600); err != nil {
			return err
		}
		if err := os.Chmod(p, 0o600); err != nil {
			return err
		}
		x.printf("%s: %s\n", v.Name, f)
	}
	return nil
}

func cmdProvision(x *ctx) error {
	lab := x.lab
	what := x.args[0]
	s, err := lab.AuthServer()
	if err != nil {
		return err
	}
	if s.Kind == "container" {
		image, err := s.Need("image")
		if err != nil {
			return err
		}
		if what == "nvram" {
			return x.provisionDomain(s, image)
		}
		pw, err := lab.Password(what, true)
		if err != nil {
			return err
		}
		out, err := x.runx([]string{"PASS=" + pw}, authCmd(s, image, true, "user", what)...)
		x.printf("%s", out)
		return err
	}
	type step struct{ prompt, answer string }
	var steps []step
	var command string
	if what == "nvram" {
		pw, err := lab.Password(lab.HostID(), true)
		if err != nil {
			return err
		}
		dom, err := lab.AuthDom()
		if err != nil {
			return err
		}
		steps = []step{{"authid", lab.HostID()}, {"authdom", dom}, {"secstore key", ""},
			{"password", pw}, {"confirm password", pw}, {"enable legacy p9sk1", "no"}}
		command = "auth/wrkey"
	} else {
		pw, err := lab.Password(what, true)
		if err != nil {
			return err
		}
		steps = []step{{"Password", pw}, {"Confirm password", pw}, {"assign new Inferno/POP secret", "n"},
			{"Expiration date", "never"}, {"Post id", ""}, {"User's full name", what},
			{"Department #", ""}, {"User's email address", ""}, {"Sponsor's email address", ""}}
		command = "auth/changeuser " + what
	}
	c, err := x.e.dialSerial(s)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.typeText(command + "\n"); err != nil {
		return err
	}
	for _, st := range steps {
		if _, _, err := c.expect(contains(st.prompt, true), seconds(x.timeout), nil); err != nil {
			var xe *expectError
			if errors.As(err, &xe) {
				return fmt.Errorf("%s: no prompt %q: %v", s.Name, st.prompt, err)
			}
			return err
		}
		if err := c.typeText(st.answer + "\n"); err != nil {
			return err
		}
	}
	x.e.sleep(2 * time.Second)
	x.printf("%s: %s done\n", s.Name, command)
	return nil
}
