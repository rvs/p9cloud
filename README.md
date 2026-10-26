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

9front ignores attributes it does not know. p9cloud uses such attributes to
say how it runs each system: as a virtual machine (machine, memory, disks,
the network it plugs into, the kernel's boot variables) or as a container
(image, volumes). They are defined and documented as each piece arrives.

## Principles

- **One source of truth.** The lab file describes the whole lab, both for
  the host and for every system.
- **Plan 9 semantics, not a lookalike.** For any database, p9cloud's ndb
  reader must answer every query exactly as 9front's `ndb/ipquery` does. A
  difference is a bug.
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
