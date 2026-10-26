package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"p9cloud/ndb"
)

// Lab is a lab file and the systems it describes.
type Lab struct {
	DB      *ndb.DB
	Path    string    // the lab file, absolute
	Dir     string    // its directory: relative file names are in it
	Host    *System   // the Linux host: the sys= entry with bridge=
	Systems []*System // the sys= entries with qemu= or container=
	Prefix  string    // the host's ipnet name: a VM's container is PREFIX-NAME
	User    string    // uid:gid, which QEMU runs as in its container
}

// System is a sys= entry with what it inherits from the networks its ip
// is on.
type System struct {
	Name  string
	Lab   *Lab
	Info  []ndb.Tuple // ndbipinfo's answer for all the attributes
	Entry *ndb.Entry  // its own entry
	Kind  string      // qemu, container or ""
}

// Load reads a lab file.
func Load(path string) (*Lab, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := ndb.Open(abs)
	if err != nil {
		return nil, err
	}
	lab := &Lab{DB: db, Path: abs, Dir: filepath.Dir(abs),
		User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())}
	attrs := db.Attrs()
	seen := map[string]bool{}
	for _, e := range db.Entries {
		name, ok := e.Find("sys")
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		s := lab.system(name, attrs)
		if _, ok := s.Entry.Find("bridge"); ok && lab.Host == nil {
			lab.Host = s
		} else if s.Kind != "" {
			lab.Systems = append(lab.Systems, s)
		}
	}
	if lab.Host == nil {
		return nil, fmt.Errorf("%s: no sys= entry with bridge= (the Linux host)", abs)
	}
	lab.Prefix = lab.Host.Or("ipnet", "")
	if lab.Prefix == "" {
		lab.Prefix = "lab"
	}
	return lab, nil
}

func (lab *Lab) system(name string, attrs []string) *System {
	s := &System{Name: name, Lab: lab, Info: lab.DB.IPInfo("sys", name, attrs...)}
	if len(s.Info) == 0 {
		// without an ip, the entry alone
		e := lab.DB.Search("sys", name)[0].Entry
		for _, p := range e.Pairs() {
			s.Info = append(s.Info, ndb.Tuple{Pair: p, Entry: e})
		}
	}
	s.Entry = s.Info[0].Entry
	if _, ok := s.Get("container"); ok {
		s.Kind = "container"
	} else if _, ok := s.Get("qemu"); ok {
		s.Kind = "qemu"
	}
	return s
}

// System returns the system called name, of one of kinds (default qemu
// and container); kind host is the host.
func (lab *Lab) System(name string, kinds ...string) (*System, error) {
	if kinds == nil {
		kinds = []string{"qemu", "container"}
	}
	has := func(k string) bool {
		for _, x := range kinds {
			if x == k {
				return true
			}
		}
		return false
	}
	if name == lab.Host.Name && has("host") {
		return lab.Host, nil
	}
	var all []string
	for _, s := range lab.Systems {
		if has(s.Kind) {
			if s.Name == name {
				return s, nil
			}
			all = append(all, s.Name)
		}
	}
	if has("host") {
		all = append(all, lab.Host.Name)
	}
	return nil, fmt.Errorf("%s: not one of: %s", name, strings.Join(all, " "))
}

// At returns the systems with the address.
func (lab *Lab) At(ip string) []*System {
	var ss []*System
	for _, s := range lab.Systems {
		if v, _ := s.Get("ip"); v == ip {
			ss = append(ss, s)
		}
	}
	return ss
}

// Port returns a service's tcp port: a number, or tcp=NAME port=N.
func (lab *Lab) Port(service string) (int, error) {
	if p, ok := number(service); ok {
		return p, nil
	}
	if v, ok := lab.DB.GetValue("tcp", service, "port"); ok {
		if p, ok := number(v); ok {
			return p, nil
		}
	}
	return 0, fmt.Errorf("tcp=%s: no port= in %s", service, lab.Path)
}

func number(s string) (int, bool) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// File names a file of the lab's: relative names are in its directory.
func (lab *Lab) File(f string) string {
	if filepath.IsAbs(f) {
		return f
	}
	return filepath.Join(lab.Dir, f)
}

// Prog names a program: a name with a / is a file of the lab's, one
// without is looked for in $PATH.
func (lab *Lab) Prog(f string) string {
	if !strings.Contains(f, "/") {
		return f
	}
	return lab.File(f)
}

// AuthServer is the system at the host's auth= address.
func (lab *Lab) AuthServer() (*System, error) {
	ip, err := lab.Host.Need("auth")
	if err != nil {
		return nil, err
	}
	ss := lab.At(ip)
	if ss == nil {
		return nil, fmt.Errorf("auth=%s: not a qemu or container system in %s", ip, lab.Path)
	}
	return ss[0], nil
}

func (lab *Lab) passwords() (string, map[string]string, error) {
	f := lab.File(lab.Host.Or("passwords", "secrets/passwords.json"))
	pw := map[string]string{}
	b, err := os.ReadFile(f)
	if errors.Is(err, fs.ErrNotExist) {
		return f, pw, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &pw)
	}
	if err != nil {
		return f, nil, fmt.Errorf("%s: %v", f, err)
	}
	return f, pw, nil
}

// Password returns a 9front user's password from the host's passwords=
// file, where the auth server's host owner is "host".  With create, a
// missing one is made up and stored.
func (lab *Lab) Password(user string, create bool) (string, error) {
	f, pw, err := lab.passwords()
	if err != nil {
		return "", err
	}
	key := user
	if _, ok := pw[user]; !ok && user == lab.HostID() {
		key = "host"
	}
	if p, ok := pw[key]; ok {
		return p, nil
	}
	if !create {
		return "", fmt.Errorf("%s: no password for %s", f, user)
	}
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	p := make([]byte, 24)
	for i := range p {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		p[i] = chars[n.Int64()]
	}
	pw[key] = string(p)
	b, _ := json.Marshal(pw)
	if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(f, append(b, '\n'), 0o600); err != nil {
		return "", err
	}
	return pw[key], os.Chmod(f, 0o600)
}

// HostID is the auth server's host owner: hostid= in the auth file next
// to the lab file, as /lib/ndb/auth, or glenda.
func (lab *Lab) HostID() string {
	if b, err := os.ReadFile(filepath.Join(lab.Dir, "auth")); err == nil {
		es, _ := ndb.Parse("auth", b)
		for _, e := range es {
			if v, ok := e.Find("hostid"); ok && v != "" {
				return v
			}
		}
	}
	return "glenda"
}

// AuthDom is the first authdom= in the lab.
func (lab *Lab) AuthDom() (string, error) {
	for _, e := range lab.DB.Entries {
		if v, ok := e.Find("authdom"); ok && v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("%s: no authdom=", lab.Path)
}

// All returns the values of attr.
func (s *System) All(attr string) []string {
	var vs []string
	for _, t := range s.Info {
		if t.Attr == attr {
			vs = append(vs, t.Val)
		}
	}
	return vs
}

// Get returns the first value of attr.
func (s *System) Get(attr string) (string, bool) {
	for _, t := range s.Info {
		if t.Attr == attr {
			return t.Val, true
		}
	}
	return "", false
}

// Or returns the first value of attr, or def if there is none.
func (s *System) Or(attr, def string) string {
	if v, ok := s.Get(attr); ok {
		return v
	}
	return def
}

// Need returns the first value of attr, which must not be empty.
func (s *System) Need(attr string) (string, error) {
	if v, _ := s.Get(attr); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("sys=%s: no %s= in %s", s.Name, attr, s.Lab.Path)
}

var dollar = regexp.MustCompile(`\$([A-Za-z0-9_.-]+)`)

// Expand replaces each $attr in v with the system's value of attr.
func (s *System) Expand(v string) (string, error) {
	var err error
	out := dollar.ReplaceAllStringFunc(v, func(m string) string {
		x, ok := s.Get(m[1:])
		if !ok && err == nil {
			err = fmt.Errorf("sys=%s: %s: no such attribute", s.Name, m)
		}
		return x
	})
	return out, err
}

// Container is the name of the system's docker container: container=,
// else the lab's prefix and the system's name.
func (s *System) Container() string {
	if c, _ := s.Get("container"); c != "" {
		return c
	}
	return s.Lab.Prefix + "-" + s.Name
}

// State is the directory of its sockets and console log.
func (s *System) State() string {
	return filepath.Join(s.Lab.File(s.Lab.Host.Or("state", "state")), s.Name)
}

// Karg is a kernel variable, and where it was set.
type Karg struct{ Name, Val, Where string }

func where(e *ndb.Entry) string {
	if e == nil {
		return ""
	}
	return e.String()
}

// Kargs returns the kernel variables: the ini.NAME attributes, those of
// the most general networks first; for a setup boot setup.NAME replaces
// ini.NAME; then the overrides.  An empty value removes a variable.
func (s *System) Kargs(setup bool, override []ndb.Pair) ([]Karg, error) {
	var order []*ndb.Entry
	rank := map[*ndb.Entry]int{}
	for _, t := range s.Info {
		if _, ok := rank[t.Entry]; !ok {
			rank[t.Entry] = len(order)
			order = append(order, t.Entry)
		}
	}
	var ks []Karg
	seen := map[string]bool{}
	for r := len(order) - 1; r >= 0; r-- {
		for _, t := range s.Info {
			if t.Entry != order[r] {
				continue
			}
			if n, ok := strings.CutPrefix(t.Attr, "ini."); ok && !seen[n] {
				seen[n] = true
				ks = append(ks, Karg{n, t.Val, where(t.Entry)})
			}
		}
	}
	var more []Karg
	if setup {
		for _, t := range s.Info {
			if n, ok := strings.CutPrefix(t.Attr, "setup."); ok {
				more = append(more, Karg{n, t.Val, "setup " + where(t.Entry)})
			}
		}
	}
	for _, p := range override {
		more = append(more, Karg{p.Attr, p.Val, "-k"})
	}
	for _, m := range more {
		found := false
		for i := range ks {
			if ks[i].Name == m.Name {
				ks[i], found = m, true
				break
			}
		}
		if !found {
			ks = append(ks, m)
		}
	}
	var out []Karg
	for _, k := range ks {
		if k.Val == "" {
			continue
		}
		v, err := s.Expand(k.Val)
		if err != nil {
			return nil, err
		}
		out = append(out, Karg{k.Name, v, k.Where})
	}
	return out, nil
}

// Cmdline is the kernel command line, as 9front's plan9.ini.
func (s *System) Cmdline(setup bool, override []ndb.Pair) (string, error) {
	ks, err := s.Kargs(setup, override)
	if err != nil {
		return "", err
	}
	var w []string
	for _, k := range ks {
		w = append(w, k.Name+"="+rcquote(k.Val))
	}
	return strings.Join(w, " "), nil
}

func (s *System) path(attr string) (string, error) {
	v, err := s.Need(attr)
	return s.Lab.File(v), err
}

var hex12 = regexp.MustCompile(`^[0-9a-f]{12}$`)

// Qemu returns the QEMU command line, as its container runs it: the
// network card is on the container's tap0.
func (s *System) Qemu(setup bool, override []ndb.Pair, extra []string) ([]string, error) {
	qemu, err := s.Need("qemu")
	if err != nil {
		return nil, err
	}
	modern := s.Or("virtio", "modern") == "modern"
	legacy := ""
	if modern {
		legacy = ",disable-legacy=on"
	}
	a := []string{qemu}
	switch accel := s.Or("accel", "tcg"); accel {
	case "kvm":
		a = append(a, "-enable-kvm")
	case "tcg":
	default:
		a = append(a, "-accel", accel)
	}
	if v, _ := s.Get("machine"); v != "" {
		a = append(a, "-M", v)
	}
	if v, _ := s.Get("cpumodel"); v != "" {
		a = append(a, "-cpu", v)
	}
	a = append(a, "-m", s.Or("mem", "1G"), "-smp", s.Or("smp", "1"))
	if v, _ := s.Get("bios"); v != "" {
		a = append(a, "-bios", s.Lab.File(v))
	}
	bootf, err := s.path("bootf")
	if err != nil {
		return nil, err
	}
	cmdline, err := s.Cmdline(setup, override)
	if err != nil {
		return nil, err
	}
	a = append(a, "-kernel", bootf, "-append", cmdline)
	for i, d := range s.All("disk") {
		f := s.Lab.File(d)
		format := "raw"
		if filepath.Ext(f) == ".qcow2" {
			format = "qcow2"
		}
		if modern {
			a = append(a, "-drive", fmt.Sprintf("file=%s,if=none,id=sdF%d,format=%s", f, i, format),
				"-device", fmt.Sprintf("virtio-blk-pci,drive=sdF%d%s", i, legacy))
		} else {
			a = append(a, "-drive", fmt.Sprintf("file=%s,if=virtio,format=%s", f, format))
		}
	}
	ether, err := s.Need("ether")
	if err != nil {
		return nil, err
	}
	mac := strings.ToLower(ether)
	if !hex12.MatchString(mac) {
		return nil, fmt.Errorf("sys=%s: ether=%s: want 12 hex digits", s.Name, ether)
	}
	var b []string
	for i := 0; i < 12; i += 2 {
		b = append(b, mac[i:i+2])
	}
	a = append(a, "-netdev", "tap,id=lab,ifname=tap0,script=no,downscript=no",
		"-device", "virtio-net-pci,netdev=lab,mac="+strings.Join(b, ":")+legacy)
	st := s.State()
	a = append(a, "-display", "none",
		"-monitor", "unix:"+st+"/monitor.sock,server=on,wait=off",
		"-chardev", "socket,id=s,path="+st+"/serial.sock,server=on,wait=off,logfile="+st+"/console.log",
		"-serial", "chardev:s")
	a = append(a, s.All("qemuarg")...)
	return append(a, extra...), nil
}

// docker is how docker run starts the system's container: on the
// host's bridge= network, at the system's address.
func (s *System) docker() ([]string, error) {
	br, err := s.Lab.Host.Need("bridge")
	if err != nil {
		return nil, err
	}
	ip, err := s.Need("ip")
	if err != nil {
		return nil, err
	}
	return []string{"docker", "run", "--rm", "--name", s.Container(), "--network", br, "--ip", ip}, nil
}

// Command returns the command that runs the system: docker run of its
// image= with its volume= volumes; for a VM, of the QEMU image (image=,
// 9front-qemu), which runs QEMU with the files it needs mounted at the
// same paths.
func (s *System) Command(setup bool, override []ndb.Pair, extra []string) ([]string, error) {
	switch s.Kind {
	case "qemu":
		q, err := s.Qemu(setup, override, extra)
		if err != nil {
			return nil, err
		}
		a, err := s.docker()
		if err != nil {
			return nil, err
		}
		a = append(a, "--cap-add", "NET_ADMIN", "--device", "/dev/net/tun")
		if s.Or("accel", "tcg") == "kvm" {
			a = append(a, "--device", "/dev/kvm")
		}
		a = append(a, "-e", "QEMU_USER="+s.Lab.User)
		files, _ := s.Files()
		for _, m := range append(files, Mount{s.State(), false}) {
			a = append(a, "--mount", m.String())
		}
		return append(append(a, s.Or("image", "9front-qemu")), q...), nil
	case "container":
		if setup || override != nil {
			return nil, fmt.Errorf("%s is a container: no kernel command line", s.Name)
		}
		a, err := s.docker()
		if err != nil {
			return nil, err
		}
		for _, v := range s.All("volume") {
			a = append(a, "-v", v)
		}
		image, err := s.Need("image")
		if err != nil {
			return nil, err
		}
		return append(append(a, extra...), image), nil
	}
	return nil, fmt.Errorf("sys=%s has neither qemu= nor container=", s.Name)
}

// Mount is a file of the host's that a VM's container sees at the same
// path.
type Mount struct {
	Path     string
	ReadOnly bool
}

func (m Mount) String() string {
	s := "type=bind,src=" + m.Path + ",dst=" + m.Path
	if m.ReadOnly {
		s += ",ro"
	}
	return s
}

// Files returns the files a QEMU system needs: kernel, disks, firmware.
func (s *System) Files() ([]Mount, error) {
	if s.Kind != "qemu" {
		return nil, nil
	}
	bootf, err := s.path("bootf")
	if err != nil {
		return nil, err
	}
	fs := []Mount{{bootf, true}}
	for _, d := range s.All("disk") {
		fs = append(fs, Mount{s.Lab.File(d), false})
	}
	if v, _ := s.Get("bios"); v != "" {
		fs = append(fs, Mount{s.Lab.File(v), true})
	}
	return fs, nil
}

var nvramDisk = regexp.MustCompile(`^/dev/sdF([0-9]+)/data$`)

// Nvram is the disk= file a VM keeps its nvram in, when it takes a
// whole disk: ini.nvram=/dev/sdFn/data names the system's nth disk.
func (s *System) Nvram() (string, error) {
	ks, err := s.Kargs(false, nil)
	if err != nil {
		return "", err
	}
	disks := s.All("disk")
	for _, k := range ks {
		if k.Name != "nvram" {
			continue
		}
		if m := nvramDisk.FindStringSubmatch(k.Val); m != nil {
			if n, _ := strconv.Atoi(m[1]); n < len(disks) {
				return disks[n], nil
			}
		}
	}
	return "", nil
}

func names(ss []*System) []string {
	var n []string
	for _, s := range ss {
		n = append(n, s.Name)
	}
	return n
}

// Needs returns the systems a QEMU system boots from: its fs= and auth=.
func (s *System) Needs() ([]*System, error) {
	if s.Kind != "qemu" {
		return nil, nil
	}
	ks, err := s.Kargs(false, nil)
	if err != nil {
		return nil, err
	}
	var out []*System
	for _, k := range ks {
		if k.Name != "fs" && k.Name != "auth" {
			continue
		}
	next:
		for _, x := range s.Lab.At(k.Val) {
			if x == s {
				continue
			}
			for _, y := range out {
				if y == x {
					continue next
				}
			}
			out = append(out, x)
		}
	}
	return out, nil
}

var rcspecial = regexp.MustCompile(`[\s'#;&|^$=` + "`" + `(){}<>\[\]*?]`)

// rcquote quotes a value for rc and plan9.ini.
func rcquote(v string) string {
	if v != "" && !rcspecial.MatchString(v) {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

var shsafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shquote quotes a word for sh, as Python's shlex.quote.
func shquote(v string) string {
	if shsafe.MatchString(v) {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", `'"'"'`) + "'"
}

func shjoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = shquote(a)
	}
	return strings.Join(q, " ")
}
