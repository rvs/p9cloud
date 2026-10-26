package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
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
	out, _ := x.e.run(nil, "docker", "inspect", "-f", "{{.State.Running}}", s.Container())
	return strings.TrimSpace(out) == "true"
}

func (x *ctx) exists(name string) bool {
	_, err := os.Stat(filepath.Join(x.e.sysnet, name))
	return err == nil
}

func (x *ctx) printf(format string, a ...any) { fmt.Fprintf(x.e.stdout, format, a...) }

func cmdList(x *ctx) error {
	lab := x.lab
	br := lab.Host.Or("bridge", "")
	up := "missing"
	if x.exists(br) {
		up = "up"
	}
	rows := [][]string{
		{"SYS", "KIND", "IP", "RUNNING", "CONTAINER"},
		{lab.Host.Name, "host", lab.Host.Or("ip", ""), "network " + br + " " + up, ""},
	}
	for _, s := range lab.Systems {
		r := "no"
		if x.running(s) {
			r = "yes"
		}
		rows = append(rows, []string{s.Name, s.Kind, s.Or("ip", ""), r, s.Container()})
	}
	w := make([]int, 4)
	for _, r := range rows {
		for i := range w {
			w[i] = max(w[i], len(r[i]))
		}
	}
	for _, r := range rows {
		var line string
		for i := range w {
			line += fmt.Sprintf("%-*s  ", w[i], r[i])
		}
		x.printf("%s\n", strings.TrimRight(line+r[4], " "))
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
		x.printf("command:\n\t%s\n", shjoin(argv))
		if needs != nil {
			x.printf("needs: %s\n", strings.Join(names(needs), " "))
		}
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
		if _, err := os.Stat(f.Path); err != nil {
			return fmt.Errorf("%s: %s: no such file", s.Name, f.Path)
		}
	}
	if br := x.lab.Host.Or("bridge", ""); !x.exists(br) {
		return fmt.Errorf("%s: no network %s: %s", s.Name, br, x.again("net"))
	}
	if s.Kind == "qemu" {
		x.e.umask(0o077)
		if err := os.MkdirAll(s.State(), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(s.State(), "console.log"), nil, 0o600); err != nil {
			return err
		}
	}
	// a container left over, stopped
	x.e.run(nil, "docker", "rm", "-f", s.Container())
	if x.detach {
		argv = append([]string{"docker", "run", "--detach"}, argv[2:]...)
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

// stopped checks Docker itself, not an inspect error's text: a successful
// empty listing confirms that an --rm container has disappeared.
func (x *ctx) stopped(s *System) (bool, error) {
	out, err := x.runx(nil, "docker", "ps", "--all", "--filter",
		"name=^/"+regexp.QuoteMeta(s.Container())+"$", "--format", "{{.State}}")
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(out) {
	case "", "created", "exited", "dead":
		return true, nil
	case "running", "paused", "restarting", "removing":
		return false, nil
	default:
		return false, fmt.Errorf("%s: unexpected container state %q", s.Name, strings.TrimSpace(out))
	}
}

func cmdStop(x *ctx) error {
	s, err := x.lab.System(x.args[0])
	if err != nil {
		return err
	}
	stopped, err := x.stopped(s)
	if err != nil {
		return err
	}
	if stopped {
		x.printf("%s is not running\n", s.Name)
		return nil
	}
	if s.Kind == "container" {
		if _, err := x.runx(nil, "docker", "stop", s.Container()); err != nil {
			return err
		}
	} else {
		monitor, err := x.haltQEMU(s)
		if err != nil || monitor == nil {
			return err
		}
		// HMP may discard queued input on disconnect. Keep the connection
		// open until Docker confirms quit, or the bounded wait fails.
		defer monitor.Close()
	}
	deadline := x.e.now().Add(seconds(x.timeout))
	for {
		stopped, err := x.stopped(s)
		if err != nil {
			return err
		}
		if stopped {
			return nil
		}
		left := deadline.Sub(x.e.now())
		if left <= 0 {
			return fmt.Errorf("%s: not confirmed stopped after %gs; NOT forcing it", s.Name, x.timeout)
		}
		x.e.sleep(min(500*time.Millisecond, left))
	}
}

// haltQEMU returns a monitor the caller must keep open through stop
// confirmation, or nil if the guest powered off by itself after fshalt.
func (x *ctx) haltQEMU(s *System) (net.Conn, error) {
	c, err := x.e.dialSerial(s)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if _, err := io.WriteString(c, "fshalt\n"); err != nil {
		return nil, err
	}
	if _, _, err := c.expect(contains("\ndone halting", false), seconds(x.timeout), x.e.stdout); err != nil {
		return nil, fmt.Errorf("%s did not confirm a clean halt; NOT killing QEMU. Detach other console clients, check the guest.", s.Name)
	}
	x.e.sleep(2 * time.Second)
	if stopped, err := x.stopped(s); err != nil || stopped {
		return nil, err
	}
	// Stock fshalt can power off QEMU between this check and monitor I/O.
	// A monitor error alone is never evidence that it has stopped.
	var m net.Conn
	monitorFailed := func(cause error) (net.Conn, error) {
		if m != nil {
			m.Close()
		}
		stopped, err := x.stopped(s)
		if err != nil || stopped {
			return nil, err
		}
		return nil, cause
	}
	m, err = net.DialTimeout("unix", filepath.Join(s.State(), "monitor.sock"), 2*time.Second)
	if err != nil {
		return monitorFailed(fmt.Errorf("%s: QEMU monitor: %w", s.Name, err))
	}

	if err := m.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return monitorFailed(fmt.Errorf("%s: QEMU monitor deadline: %w", s.Name, err))
	}
	monitor := &conn{Conn: m, e: x.e}
	if _, _, err := monitor.expect(contains("(qemu) ", false), 2*time.Second, nil); err != nil {
		return monitorFailed(fmt.Errorf("%s: QEMU monitor greeting: %w", s.Name, err))
	}
	if _, err := io.WriteString(m, "quit\n"); err != nil {
		return monitorFailed(fmt.Errorf("%s: QEMU monitor quit: %w", s.Name, err))
	}
	return m, nil
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
	term := x.root
	if term == "" {
		term = h.Or("terminal", "terminal")
	}
	term = lab.File(term)
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
		return x.e.exec(nil, "docker", "logs", "-f", s.Container())
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

// cmdNet creates the lab's docker network, internal: its systems reach
// each other and the host, which is its gateway, and nothing else.
func cmdNet(x *ctx) error {
	h := x.lab.Host
	br, err := h.Need("bridge")
	if err != nil {
		return err
	}
	ip, err := h.Need("ip")
	if err != nil {
		return err
	}
	addr, ok := ndb.ParseIP(ip)
	if !ok || !addr.IsV4() {
		return fmt.Errorf("sys=%s: ip=%s: not an IPv4 address", h.Name, ip)
	}
	// ndbipinfo's default ipmask= is the address's class mask
	mask, ok := ndb.ParseIPMask(h.Or("ipmask", ""), true)
	if !ok {
		return fmt.Errorf("sys=%s: ipmask=%s: not an IPv4 mask", h.Name, h.Or("ipmask", ""))
	}
	gw := netip.AddrFrom16(addr).Unmap()
	subnet := netip.PrefixFrom(gw, ndb.PrefixLen(mask)-96).Masked().String()
	want := subnet + " " + gw.String()
	out, err := x.e.run(nil, "docker", "network", "inspect", "-f", "{{range .IPAM.Config}}{{.Subnet}} {{.Gateway}}{{end}}", br)
	if err == nil {
		if got := strings.TrimSpace(out); got != want {
			return fmt.Errorf("docker network %s: %s; the lab wants %s", br, got, want)
		}
		return nil
	}
	if x.exists(br) {
		return fmt.Errorf("%s: a network device, but not a docker network", br)
	}
	argv := []string{"docker", "network", "create", "--internal", "--subnet", subnet, "--gateway", gw.String(),
		"-o", "com.docker.network.bridge.name=" + br, br}
	x.printf("%s\n", shjoin(argv))
	if x.n {
		return nil
	}
	_, err = x.runx(nil, argv...)
	return err
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
