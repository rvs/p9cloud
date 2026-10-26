// p9cloud runs a lab's Plan 9 systems, QEMU guests and docker containers,
// as the lab file describes them.  The lab file is an ndb(6) database,
// read as 9front's libndb reads it: it is the systems' /lib/ndb/local
// too.  See ../../README.md.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"p9cloud/ndb"
)

const overview = `p9cloud runs the systems of a lab: QEMU guests (the sys= entries with
qemu=) and docker containers (container=), on the Linux host (the sys=
entry with bridge=).  The lab file, default p9.lab, is an ndb(6)
database, read as 9front reads it: it is the systems' /lib/ndb/local
too.  A system inherits what its entry lacks from the networks its ip
is on; ini.NAME attributes are its kernel variables.
`

const examples = `examples:
  p9cloud list                        the systems, running or not
  p9cloud show amd                    attributes, kernel line, QEMU command
  p9cloud show -k auth=10.99.0.1 amd  with a kernel variable changed
  p9cloud query sys amd ini.nvram     as ndb/ipquery in a guest
  p9cloud start -setup fs             fs maintenance boot, in the foreground
  p9cloud cpu amd                     drawterm session
  p9cloud cmd fs 'cat /dev/sysname'   one command at the serial console
  sudo p9cloud units -install         systemd units; then
  sudo systemctl start p9lab-amd      start, wait, stop as they do
`

type command struct {
	name, usage, help string
	flags             string // of: setup k t u n install
	min, max          int    // positional arguments; max -1: any
	extra             bool   // takes -- arguments
	timeout           float64
	db                bool // needs only the database: no host
	run               func(*ctx) error
}

var commands = []*command{
	{name: "list", usage: "list", run: cmdList,
		help: "list the systems: running or not, systemd unit"},
	{name: "show", usage: "show [-setup] [-k name=value]... sys [-- arg...]", run: cmdShow,
		flags: "setup k", min: 1, max: 1, extra: true,
		help: "show a system: attributes and where they come from, kernel line, command, unit"},
	{name: "query", usage: "query attr value [rattr...]", run: cmdQuery, min: 2, max: -1, db: true,
		help: "look up attributes as ndb/ipquery does; all of them without rattr"},
	{name: "start", usage: "start [-setup] [-k name=value]... sys [-- arg...]", run: cmdStart,
		flags: "setup k", min: 1, max: 1, extra: true,
		help: "run a system in the foreground, as its systemd unit does"},
	{name: "wait", usage: "wait [-t timeout] sys", run: cmdWait, flags: "t", min: 1, max: 1, timeout: 90,
		help: "wait until a system accepts connections on its ready= services"},
	{name: "stop", usage: "stop [-t timeout] sys", run: cmdStop, flags: "t", min: 1, max: 1, timeout: 90,
		help: "stop a system: fshalt at its console, then quit QEMU; or docker stop"},
	{name: "cpu", usage: "cpu [-u user] sys [command...]", run: cmdCpu, flags: "u", min: 1, max: -1,
		help: "drawterm into a cpu server, the host's terminal= as /mnt/term; or run command"},
	{name: "cmd", usage: "cmd [-t timeout] sys command...", run: cmdCmd, flags: "t", min: 2, max: -1, timeout: 60,
		help: "run an rc command at a QEMU system's idle serial console"},
	{name: "console", usage: "console sys", run: cmdConsole, min: 1, max: 1,
		help: "attach to a QEMU system's serial console (Ctrl-] detaches); a container's log"},
	{name: "net", usage: "net [-n]", run: cmdNet, flags: "n",
		help: "create the host's bridge and the systems' tap devices (root)"},
	{name: "serve", usage: "serve", run: cmdServe,
		help: "serve the host's httpdir= over http, on its ip and http= port"},
	{name: "provision", usage: "provision [-t timeout] nvram|user", run: cmdProvision,
		flags: "t", min: 1, max: 1, timeout: 40,
		help: "auth server: write its nvram, or add or rekey a user, with passwords from passwords="},
	{name: "units", usage: "units [-install]", run: cmdUnits, flags: "install",
		help: "print the systemd units for the systems; -install writes them (root)"},
	{name: "help", usage: "help [command]", max: 1,
		help: "help for a command"},
}

var flagHelp = map[string]string{
	"setup":   "  -setup        maintenance boot: setup.NAME replaces ini.NAME\n",
	"k":       "  -k name=value set a kernel variable; an empty value removes it\n",
	"t":       "  -t timeout    in seconds (default %g)\n",
	"u":       "  -u user       the 9front user (default: the host's user=, then $USER)\n",
	"n":       "  -n            print the commands only\n",
	"install": "  -install      write them to /etc/systemd/system and reload systemd\n",
}

// ctx is a command's invocation.
type ctx struct {
	e       *env
	cmd     *command
	lab     *Lab
	db      *ndb.DB
	args    []string
	extra   []string
	setup   bool
	k       kflag
	timeout float64
	user    string
	n       bool
	install bool
}

type kflag []ndb.Pair

func (k *kflag) String() string { return "" }

func (k *kflag) Set(s string) error {
	n, v, ok := strings.Cut(s, "=")
	if !ok || n == "" {
		return fmt.Errorf("%s: want name=value", s)
	}
	*k = append(*k, ndb.Pair{Attr: n, Val: v})
	return nil
}

// usageError is a command line p9cloud does not understand.
type usageError struct {
	msg string
	cmd *command
}

func (u *usageError) Error() string { return u.msg }

func lookup(name string) *command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return nil
}

func printUsage(w io.Writer, c *command) {
	if c == nil {
		fmt.Fprintf(w, "usage: p9cloud [-f lab] command [args]\n\n%s\ncommands:\n", overview)
		for _, c := range commands {
			fmt.Fprintf(w, "  %-10s %s\n", c.name, c.help)
		}
		fmt.Fprintf(w, "\n  -f lab     the lab file (default p9.lab)\n\n%s", examples)
		return
	}
	fmt.Fprintf(w, "usage: p9cloud [-f lab] %s\n\n%s\n", c.usage, c.help)
	if c.flags != "" {
		fmt.Fprintln(w)
	}
	for _, f := range strings.Fields(c.flags) {
		if f == "t" {
			fmt.Fprintf(w, flagHelp[f], c.timeout)
		} else {
			fmt.Fprint(w, flagHelp[f])
		}
	}
}

// p9cloud runs a command line and returns the exit status: 1 for an
// error, 2 for a command line it does not understand.
func p9cloud(e *env, args []string) int {
	err := parse(e, args)
	var u *usageError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &u):
		if u.msg != "" {
			fmt.Fprintf(e.stderr, "p9cloud: %s\n", u.msg)
		}
		printUsage(e.stderr, u.cmd)
		return 2
	}
	fmt.Fprintf(e.stderr, "p9cloud: %v\n", err)
	return 1
}

func parse(e *env, args []string) error {
	var extra []string
	dashdash := false
	for i, a := range args {
		if a == "--" {
			args, extra, dashdash = args[:i], args[i+1:], true
			break
		}
	}
	global := flag.NewFlagSet("p9cloud", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	labfile := global.String("f", "p9.lab", "")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(e.stdout, nil)
			return err
		}
		return &usageError{msg: err.Error()}
	}
	if global.NArg() == 0 {
		return &usageError{}
	}
	c := lookup(global.Arg(0))
	if c == nil {
		return &usageError{msg: global.Arg(0) + ": no such command"}
	}
	x := &ctx{e: e, cmd: c, extra: extra, timeout: c.timeout}
	fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for _, f := range strings.Fields(c.flags) {
		switch f {
		case "setup":
			fs.BoolVar(&x.setup, "setup", false, "")
		case "k":
			fs.Var(&x.k, "k", "")
		case "t":
			fs.Float64Var(&x.timeout, "t", c.timeout, "")
			fs.Float64Var(&x.timeout, "timeout", c.timeout, "")
		case "u":
			fs.StringVar(&x.user, "u", "", "")
			fs.StringVar(&x.user, "user", "", "")
		case "n":
			fs.BoolVar(&x.n, "n", false, "")
		case "install":
			fs.BoolVar(&x.install, "install", false, "")
		}
	}
	if err := fs.Parse(global.Args()[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(e.stdout, c)
			return err
		}
		return &usageError{msg: err.Error(), cmd: c}
	}
	x.args = fs.Args()
	if n := len(x.args); n < c.min || c.max >= 0 && n > c.max {
		return &usageError{cmd: c}
	}
	if dashdash && !c.extra {
		return &usageError{msg: c.name + " takes no -- arguments", cmd: c}
	}
	if c.name == "help" {
		if len(x.args) == 0 {
			printUsage(e.stdout, nil)
			return nil
		}
		h := lookup(x.args[0])
		if h == nil {
			return &usageError{msg: x.args[0] + ": no such command"}
		}
		printUsage(e.stdout, h)
		return nil
	}
	var warnings []string
	if c.db {
		db, err := ndb.Open(*labfile)
		if err != nil {
			return err
		}
		x.db, warnings = db, db.Warnings
	} else {
		lab, err := Load(*labfile)
		if lab != nil {
			x.lab, x.db, warnings = lab, lab.DB, lab.DB.Warnings
		}
		if err != nil {
			return err
		}
	}
	for _, w := range warnings {
		fmt.Fprintf(e.stderr, "p9cloud: warning: %s\n", w)
	}
	return c.run(x)
}

func main() { os.Exit(p9cloud(realEnv(), os.Args[1:])) }
