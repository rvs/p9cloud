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
| `list` | the systems: running or not, their containers |
| `show [-setup] [-k name=value]... sys [-- arg...]` | a system's attributes and where each comes from, its kernel line, its command |
| `query attr value [rattr...]` | a lookup as `ndb/ipquery` does it; all attributes without `rattr` |
| `start [-d] [-setup] [-k name=value]... sys [-- arg...]` | runs a system's container; `-d` detaches, otherwise foreground |
| `wait [-t timeout] sys` | waits until it answers on its `ready=` services |
| `stop [-t timeout] sys` | `fshalt` at its console, then quits QEMU; or `docker stop` |
| `cpu [-u user] [-r dir] sys [command...]` | a drawterm session with a cpu server, or one command |
| `cmd [-t timeout] sys command...` | one rc command at a QEMU system's serial console |
| `console sys` | the serial console (Ctrl-] detaches); a container's log |
| `net [-n]` | creates the lab's docker network |
| `serve` | serves the host's `httpdir=` over http |
| `provision [-t timeout] nvram\|user` | on the auth server: `auth/wrkey`, or adds or rekeys a user; for an auth container, `nvram` makes its domain, unless its keys hold it, and writes the VMs' nvram disks |

`-setup` boots with `setup.NAME` in place of `ini.NAME`: a maintenance
boot.  `-k` sets a kernel variable; an empty value removes it.  Words
after `--` go to QEMU or `docker run`.

Every system runs in a docker container of its own, `container=` or
the host's ipnet name, a dash and the system's name, at its `ip=` on
the lab's docker network.  `net` makes that network: named after the
host's `bridge=`, which is the name of its Linux bridge too, with the
host's `ip=` and `ipmask=` its gateway and subnet.  It is internal:
the systems reach each other and the host, and nothing else.  None of
it needs root, only docker.

`start -d` means Docker accepted the start, not that the guest is ready.
Follow it with `wait sys`. This allows unattended recipes to use the same
lifecycle commands without inventing their own VM launchers.

`stop` succeeds only after Docker confirms the container stopped or is
absent (including normal `--rm` removal). After a clean halt, a VM may
power off itself: Docker is checked before contacting its monitor and
again on monitor errors. Those errors are failures unless Docker confirms
the VM stopped; Docker failures never count as a stop.
For a VM, `-t` bounds the clean-halt
confirmation and, separately, the status-polling wait after `quit`.
The monitor connection stays open through that wait so QEMU can consume
the queued command before a disconnect.
Containers are also checked after `docker stop`. A timeout never forces
QEMU down. Stop CPU servers before their auth server yourself; `stop`
does not stop dependencies.

`cpu -r dir` overrides the host's `terminal=` export for this invocation.
Relative paths are relative to the lab file, not the current directory;
absolute paths are used unchanged. The directory is created if absent.
Without `-r` (or with an empty value), the host's `terminal=` remains the
default. For example, `cpu -r check-input amd /mnt/term/check` exports
only the check-input directory, not a bootstrap tree.

A VM's container is of the image `image=`, by default `9front-qemu`,
which is not in this repository yet.  p9cloud runs it with
`NET_ADMIN`, `/dev/net/tun`, `/dev/kvm` for `accel=kvm`, and
`QEMU_USER` set to the uid:gid of p9cloud's user; the VM's kernel,
disks, firmware and state directory are mounted at their own paths.
The image's entrypoint makes a tap `tap0` owned by `QEMU_USER`,
bridges it with `eth0` and takes `eth0`'s address away, which is the
VM's, then runs its command, QEMU, as `QEMU_USER` and in the kvm
group.  So the files QEMU makes, its sockets, are the user's.

## Attributes

Besides those in ../../README.md:

| Entry | Attribute | Meaning |
|---|---|---|
| host | `bridge=` | the lab's docker network and its Linux bridge; it makes the entry the host |
| host | `state=` | directory of the systems' console and monitor sockets (`state`) |
| host | `http=`, `httpdir=` | port (8082) and directory for `serve` |
| host | `drawterm=`, `terminal=` | drawterm, and the directory it exports as `/mnt/term` |
| host | `user=`, `passwords=` | the 9front user for `cpu`; the JSON file of passwords (`secrets/passwords.json`) |
| QEMU | `machine=`, `bios=`, `bootf=` | QEMU machine, firmware, kernel |
| QEMU | `qemu=` | the emulator, a program in its image |
| QEMU | `image=` | the image it runs in (`9front-qemu`) |
| QEMU | `ether=` | the MAC address of its network card |
| QEMU | `virtio=transitional` | virtio devices with the legacy interface too |
| QEMU | `qemuarg=` | more QEMU arguments, one per attribute |
| QEMU | `setup.NAME=` | `ini.NAME` for a `-setup` boot |
| any | `container=` | its container's name |
| container | `image=`, `volume=` | its image; `docker run -v` volumes |
| `tcp=NAME` | `port=` | a service's port, as in `/lib/ndb/common` |

Relative file names are relative to the lab file's directory.  The
auth server is the system at the host's `auth=`; its host owner is
`hostid=` in the file `auth` next to the lab file (`/lib/ndb/auth`),
else glenda, whose password is `host` in the passwords file.  A VM's
nvram disk is the `disk=` that its `ini.nvram=/dev/sdFn/data` names:
the nth.

## Layout

| File | Holds |
|---|---|
| `ndb/` | 9front's libndb in Go: ndb(6) parsing, `database=`, `ndbsearch`, `ndbgetvalue`, `ndbipinfo` |
| `lab.go` | the lab: systems, kernel variables, QEMU and docker commands, dependencies, ports, passwords |
| `main.go` | the command line |
| `commands.go` | the commands |
| `serial.go` | typing at a serial console and waiting for its answers |
| `env.go`, `env_linux.go` | what p9cloud does outside the lab file: processes, exec, tty, clock |

## Tests

The tests drive `p9cloud` through a fake `env`: the commands it would
run, exec or sleep are recorded, while consoles, QEMU monitors and
services are real sockets.  `testdata/lab` is a lab like the first one
that ran.  `testdata/golden` is what `show` prints for it;
the launcher p9cloud replaces printed the same.

`ndb/testdata/ipquery.out` is what 9front's `ndb/ipquery` printed for
`ndb/testdata/queries`; `ndb/testdata/ipquery.rc` makes it again on a
9front system.  Every answer is checked byte for byte.

The tests were checked by mutation: each rule of the code was broken
in turn, and each time a test failed.
