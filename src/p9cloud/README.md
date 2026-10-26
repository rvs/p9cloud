# p9cloud, the command

`p9cloud` runs a lab's systems as its lab file describes them: QEMU
virtual machines and docker containers on one Linux host.  It is a Go
module of its own, rooted here.

    cd src/p9cloud
    go build                   # ./p9cloud
    go test ./...              # -short skips building the binary

## Commands

    p9cloud [-f lab] command [args]

The lab is `p9.lab` in the current directory unless `-f` names
another.  Flags come before arguments; `p9cloud help command` has
each command's.

| Command | Does |
|---|---|
| `list` | the systems: running or not, systemd unit |
| `show [-setup] [-k name=value]... sys [-- arg...]` | a system's attributes and where each comes from, its kernel line, its command and unit |
| `query attr value [rattr...]` | a lookup as `ndb/ipquery` does it; all attributes without `rattr` |
| `start [-setup] [-k name=value]... sys [-- arg...]` | runs a system in the foreground: QEMU, or `docker run` |
| `wait [-t timeout] sys` | waits until it answers on its `ready=` services |
| `stop [-t timeout] sys` | `fshalt` at its console, then quits QEMU; or `docker stop` |
| `cpu [-u user] sys [command...]` | a drawterm session with a cpu server, or one command |
| `cmd [-t timeout] sys command...` | one rc command at a QEMU system's serial console |
| `console sys` | the serial console (Ctrl-] detaches); a container's log |
| `net [-n]` | creates the host's bridge and the tap devices (root) |
| `serve` | serves the host's `httpdir=` over http |
| `provision [-t timeout] nvram\|user` | on the auth server: `auth/wrkey`, or adds or rekeys a user |
| `units [-install]` | the systemd units for the systems; `-install` writes them (root) |

`-setup` boots with `setup.NAME` in place of `ini.NAME`: a maintenance
boot.  `-k` sets a kernel variable; an empty value removes it.  Words
after `--` go to QEMU or `docker run`.

The systemd units run `p9cloud start`, `wait` and `stop`, as the lab
directory's owner and in it; a system's unit needs the units of the
systems its kernel names as `fs=` and `auth=`.

## Attributes

Besides those in ../../README.md:

| Entry | Attribute | Meaning |
|---|---|---|
| host | `bridge=` | the bridge the systems' taps plug into; it makes the entry the host |
| host | `state=` | directory of the systems' console and monitor sockets (`state`) |
| host | `http=`, `httpdir=` | port (8082) and directory for `serve` |
| host | `drawterm=`, `terminal=` | drawterm, and the directory it exports as `/mnt/term` |
| host | `user=`, `passwords=` | the 9front user for `cpu`; the JSON file of passwords (`secrets/passwords.json`) |
| QEMU | `machine=`, `bios=`, `bootf=` | QEMU machine, firmware, kernel |
| QEMU | `ether=` | the MAC address of its network card |
| QEMU | `virtio=transitional` | virtio devices with the legacy interface too |
| QEMU | `qemuarg=` | more QEMU arguments, one per attribute |
| QEMU | `setup.NAME=` | `ini.NAME` for a `-setup` boot |
| container | `container=`, `image=` | container name and image |
| container | `publish=`, `volume=` | services published on its `ip=`; `docker run -v` volumes |
| `tcp=NAME` | `port=` | a service's port, as in `/lib/ndb/common` |

Relative file names are relative to the lab file's directory.  The
auth server is the system at the host's `auth=`; its host owner is
`hostid=` in the file `auth` next to the lab file (`/lib/ndb/auth`),
else glenda, whose password is `host` in the passwords file.

## Layout

| File | Holds |
|---|---|
| `ndb/` | 9front's libndb in Go: ndb(6) parsing, `database=`, `ndbsearch`, `ndbgetvalue`, `ndbipinfo` |
| `lab.go` | the lab: systems, kernel variables, QEMU and docker commands, dependencies, ports, passwords |
| `main.go` | the command line |
| `commands.go`, `units.go` | the commands |
| `serial.go` | typing at a serial console and waiting for its answers |
| `env.go`, `env_linux.go` | what p9cloud does outside the lab file: processes, exec, tty, clock |

## Tests

The tests drive `p9cloud` through a fake `env`: the commands it would
run, exec or sleep are recorded, while consoles, QEMU monitors and
services are real sockets.  `testdata/lab` is a lab like the first one
that ran.  `testdata/golden` is what `show` and `units` print for it;
the launcher p9cloud replaces printed the same.

`ndb/testdata/ipquery.out` is what 9front's `ndb/ipquery` printed for
`ndb/testdata/queries`; `ndb/testdata/ipquery.rc` makes it again on a
9front system.  Every answer is checked byte for byte.

The tests were checked by mutation: each rule of the code was broken
in turn, and each time a test failed.
