# p9cloud

p9cloud instantiates *labs*: arbitrary topologies of Plan 9 systems,
described in a Plan 9 network database and run on Linux hosts as virtual
machines and containers. The repository holds the scripts that orchestrate
the systems and the recipes for the artifacts they boot: kernels, disk
images and container images.

## Aim

It starts small. One Linux host has a private network with a handful of
systems: a file server, an auth server, and CPU servers of different
architectures, each one a QEMU virtual machine or a docker container.

It grows into cloud infrastructure management. Systems run on many hosts
and on several networks, and topologies are built by tying arbitrary
systems together. Labs are still described the same way.

## Vocabulary

The terms are Plan 9's own, as used in ndb(6).

- **Service**: a Plan 9 service that answers on a network port. Examples are
  a file server (`9fs`, 564), an auth server (`ticket`, 567), a CPU server
  (`rcpu`, 17019), DNS and venti. Services use the names and ports in
  `/lib/ndb/common`, e.g. `tcp=9fs port=564`.
- **System**: a collection of services at one address, an ndb `sys=` entry.
  p9cloud starts, stops and watches systems as whole units. A system runs as
  a virtual machine or a container. To the rest of the lab it is only an
  address with services on it.
- **Network**: an `ipnet=` entry. It is an address range plus the attributes
  its systems share. These include which systems serve the network: `fs=`,
  `auth=`, `authdom=`, `cpu=`, `dns=` and `ntp=`.
- **Lab**: a set of networks and systems, described by one lab file.

## The lab file

All configuration comes from a single file, the *lab*, for example
`p9.lab`. A lab can be split over several files, which the top-level file
references. A lab is an ndb(6) database, and p9cloud reads it exactly as
9front's libndb reads `/lib/ndb/local`:

- **Syntax**: an entry starts in the first column and goes on in indented
  lines of `attr=value` pairs. Values can be quoted, and `#` starts a
  comment.
- **More files**: a `database=` entry lists the other files of the lab with
  `file=` attributes, in search order.
- **Inheritance**: when a system's entry lacks an attribute, the system
  takes it from the `ipnet=` entries that contain its address, most specific
  first. This is ndbipinfo(2), what `ndb/ipquery` prints. Settings shared by
  a network are written once, on the network.

The lab file is not only parsed like `/lib/ndb/local`; it is
`/lib/ndb/local`. p9cloud puts it, with the files it references, into the
file system that every system of the lab sees. The 9front systems use it to
resolve names and find their file and auth servers. The host uses the same
database to orchestrate them. There is no second configuration format, no
translation step, and nothing to keep in sync.

A classical ndb entry already describes a system:

```
ip=10.0.1.10 ether=0010dc724074 sys=fs
	dom=myfileserver.myuniversity.edu
	bootf=/386/9pc
```

The system gets its address from `ip=` and its network card from `ether=`.
`bootf=` is the kernel it boots, just as for a real machine booted from the
network. A small lab adds a network, and the network names the systems that
serve it:

```
database=
	file=/lib/ndb/local
	file=/lib/ndb/common

ipnet=mynet ip=10.0.1.0 ipmask=255.255.255.0
	authdom=myuniversity.edu
	auth=10.0.1.10
	fs=10.0.1.10

ip=10.0.1.10 ether=0010dc724074 sys=fs
	dom=myfileserver.myuniversity.edu
	bootf=/386/9pc

ip=10.0.1.20 ether=0010dc724075 sys=cpu
	dom=mycpu.myuniversity.edu
	bootf=/amd64/9pc64
```

## p9cloud attributes

ndb has no schema. libndb parses any `attr=value` pair, and each program
asks only for the attributes it needs. So a lab can mix two kinds of
attributes: classical ndb attributes, which 9front uses, and attributes
specific to p9cloud, which say how to run a system. Plan 9 programs reading
the database never ask for p9cloud's attributes and skip them; to those
programs a lab is an ordinary `/lib/ndb/local`.

A network and a system from a working lab:

```
ipnet=p9lab ip=10.99.0.0 ipmask=255.255.255.0
	fs=10.99.0.10 auth=10.99.0.10 ntp=10.99.0.10
	ini.console=0 ini.sysname=$sys ini.auth=$auth ini.fs=$fs ini.service=cpu

sys=amd ip=10.99.0.11 ether=525400990011 dom=amd.p9lab
	qemu=qemu-system-x86_64 accel=kvm cpumodel=host mem=2G smp=2
	bootf=images/9pc64 disk=secrets/amd.nvram virtio=modern tap=p9amd
	ini.nvram=/dev/sdF0/data
	ini.nobootprompt="tcp ether /net/ether0 $ip $ipmask"
	ready=rcpu
```

The classical attributes are:

- On the network: `ipnet`, `ip` and `ipmask`, plus the network's servers,
  `fs`, `auth` and `ntp`.
- On the system: `sys`, `ip`, `ether`, `dom` and `bootf`. `bootf` keeps its
  ndb meaning, the kernel the system boots. `ether` is the MAC address of
  the system's network card.

The rest are p9cloud's:

| Attribute | Meaning |
|---|---|
| `qemu=` | the system is a QEMU virtual machine, run by this emulator |
| `accel=`, `cpumodel=`, `mem=`, `smp=` | its accelerator, CPU model, memory and processors |
| `disk=` | its virtio disks, `sdF0`, `sdF1`, ... in order |
| `virtio=modern` | modern virtio devices only, without the legacy interface |
| `tap=` | the host tap device its network card plugs into |
| `ini.NAME=` | the plan9.ini(8) variable NAME on its kernel command line |
| `ready=` | the services (named by `tcp=` entries) that answer once it is up |

File names, such as `bootf=` and `disk=`, are paths on the host, relative
to the lab file.

### Inheritance from the network

ndb lookups are hierarchical and driven by IP address. As ndb(6) puts it, a
search for an attribute of a system starts at the system's own entry and
works its way up, bit by bit through its address, through the `ipnet=`
entries of the networks that contain it. The search for an attribute ends
when it is found.

amd's address, 10.99.0.11, lies in p9lab's 10.99.0.0/24. So the attributes
of the `ipnet=p9lab` stanza resolve for amd as if they were in amd's own
entry: `ini.console=0`, `ini.sysname=$sys`, `ini.auth=$auth`,
`ini.fs=$fs`, `ini.service=cpu`, and `fs=`, `auth=` and `ntp=` too. With the
two stanzas above as its database, a 9front system answers:

```
% ndb/ipquery sys amd ini.console ini.sysname ini.auth ini.fs ini.service ini.nvram ini.nobootprompt
ini.nvram=/dev/sdF0/data ini.nobootprompt="tcp ether /net/ether0 $ip $ipmask" ini.console=0 ini.sysname=$sys ini.auth=$auth ini.fs=$fs ini.service=cpu
```

The rules come from libndb's ndbipinfo(2):

- Every system on the network gets the network's attributes, one attribute
  at a time, and a system's own value wins. With `ini.service=terminal` in
  its entry, amd would boot as a terminal, and the other systems on p9lab
  would stay CPU servers.
- Networks nest. The most specific network (the longest mask) is searched
  first, and the networks around it fill in whatever is still missing.
- Inheritance is a property of this lookup, the one `ndb/ipquery` makes, and
  ndb/cs makes it for dial strings such as `tcp!$auth!ticket`. A plain match
  sees only amd's own entry: `ndb/query sys amd ini.service` finds nothing.
  p9cloud always uses the inheriting lookup to find a system's attributes.

### `$attr` in values

9front stores values verbatim and returns them that way; `ndb/ipquery`
answers `ini.sysname=$sys`. Expansion is p9cloud's own convention. In a
value, `$NAME` is replaced by attribute NAME as it resolves for the system
the value is used for. So the one `ini.sysname=$sys` on the network gives
every system its own name. amd boots with this kernel command line:

```
console=0 sysname=amd auth=10.99.0.10 fs=10.99.0.10 service=cpu nvram=/dev/sdF0/data nobootprompt='tcp ether /net/ether0 10.99.0.11 255.255.255.0'
```

## Principles

- **One source of truth.** The lab file describes the whole lab, both for
  the host and for every system.
- **Plan 9 semantics, not a lookalike.** For any database, p9cloud's ndb
  reader must answer every query exactly as 9front's `ndb/ipquery` does,
  inheritance included. A difference is a bug. p9cloud's attributes and its
  `$attr` expansion only add to ndb; they never change what a 9front
  program sees.
- **Topology is data.** Which system serves files, authentication or CPU,
  and to whom, is expressed with the ndb attributes 9front itself uses. To
  change the topology, edit the lab.
- **Systems are units.** They start in dependency order and count as up
  when their services answer. They stop cleanly, with file systems halted,
  never killed.
- **Reproducible artifacts.** Kernels and images are built by recipes in
  this repository from pinned sources.
- **Tight.** Only what the final implementation needs goes in. Experiments,
  their logs and their workarounds stay out.

## Status

So far only this README. Work proven earlier will be moved in piece by
piece, cleaned up first.
