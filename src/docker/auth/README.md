# 9front-auth

The auth server of a 9front authentication domain as a Docker image:
one 226 kB static binary in a `scratch` image, running as an
unprivileged user, with the keys in a volume.

It replaces what a 9front auth server runs for its domain:

| 9front                                   | 9front-auth                        |
|------------------------------------------|------------------------------------|
| `authsrv` on tcp!*!567 (`service.auth`)  | `serve`                            |
| `auth/wrkey` (the auth server's nvram)   | `init`                             |
| `auth/changeuser`, `passwd`              | `user`, or `passwd` from 9front    |
| `auth/enable`, `auth/disable`            | `enable`, `disable`                |
| keyfs expiry (`auth/changeuser`)         | `expire`                           |
| `auth/keyfs` + `/adm/keys`               | the `keys` file                    |
| `/lib/ndb/auth` (speaks-for)             | `speaksfor`, the `ndb` file        |
| `auth/wrkey` on each cpu server          | `nvram`: write it here, copy there |

It speaks dp9ik (AuthPAK + AuthTreq), password changes (AuthPass) and,
with `-l`, p9sk1. Not supported: the challenge/response protocols for
other systems (APOP, CRAM, MS-CHAP, VNC), which keyfs's
`secret` field exists for, and secstore. The Plan 9 distributed lab
uses none of them.

## Commands

```
$ docker run --rm 9front-auth help
usage: 9front-auth [-d dir] command [args]
  serve     [-l] [-a addr]            answer ticket requests on addr (tcp!*!567)
  init      [-l] [-r] authid authdom  make a domain; authid owns this server
  import    [nvram keys [ndb]]        take over a domain (no files: export's output on stdin)
  export                              write the domain: nvram, then keys
  user      [-l] [-r] name            add a user or set its password (-r: random)
  expire    name yyyymmdd|never       set when a user's key expires
  disable   name                      refuse a user's tickets
  enable    name                      accept them again
  remove    name                      delete a user
  list                                show the domain and its users
  speaksfor [hostid [uid...]]         show or set whom a host owner may speak for
  nvram     [-l] hostowner            write the nvram of a cpu server
  health    [addr]                    exit 0 if the server answers on addr
dir holds the keys (/var/lib/9front-auth).  Passwords come from $PASS, else
standard input.
-l (legacy): p9sk1 (DES) too, for clients without dp9ik.  It takes serve -l and
keys made with -l; off by default, as its tickets let eavesdroppers guess passwords.
```

The default command is `serve`. Every command takes its arguments from
the command line; passwords come from `-e PASS`, a terminal (`-it`,
asked twice) or one line on standard input (`-i`). `export` and
`nvram` write binary data and refuse to write it to a terminal.

## Use

A new domain, `demo`, whose auth server is owned by `glenda`:

```sh
V="-v 9front-auth:/var/lib/9front-auth"
docker run --rm $V 9front-auth init -r glenda demo  # -r: nobody logs in as glenda
docker run --rm -it $V 9front-auth user roman        # asks for the password
docker run -d --name 9front-auth --restart unless-stopped \
	-p 567:567 $V 9front-auth
```

Cpu servers (and file servers) of the domain need nvram with the key of
their host owner. All of them can share the auth server's owner, as
9front usually does:

```sh
docker run --rm $V 9front-auth nvram glenda > cpu.nvram
```

or each can have its own, which the auth server must let speak for the
users who log in there (9front's default is everyone but `sys` and
`adm`):

```sh
docker run --rm $V 9front-auth user -r cpu1
docker run --rm $V 9front-auth speaksfor cpu1 '!sys' '!adm' '*'
docker run --rm $V 9front-auth nvram cpu1 > cpu1.nvram
```

The 512 bytes go where `auth/wrkey` writes: the nvram partition of the
machine's disk (`nvram=` in plan9.ini), e.g. a raw qemu drive. Its
`/lib/ndb/local` names the domain and the auth server, `authdom=demo
auth=<address of the container's host>`; with `auth=` on the kernel
command line during boot.

`passwd user@demo` from 9front works as usual. Changes made while the
server runs take effect on its next connection; there is no need to
restart it.

## The key directory

`/var/lib/9front-auth` (`-d dir` elsewhere) holds:

| file      | contents                                                    |
|-----------|-------------------------------------------------------------|
| `nvram`   | the auth server's own nvram, as `auth/wrkey` writes it      |
| `keys`    | the users: `/adm/keys`, encrypted with the nvram's AES key  |
| `ndb`     | speaks-for entries, as in `/lib/ndb/auth`                   |
| `keyseed` | random seed for the fake keys of unknown users              |
| `lock`    | serializes changes                                          |

`nvram`, `keys` and `ndb` are byte for byte what a 9front auth server
has, so a domain moves either way:

- from 9front: copy the auth server's nvram partition
  (`cat /dev/sdXX/nvram`), `/adm/keys` and `/lib/ndb/auth`, then
  `9front-auth import nvram keys ndb`;
- between key directories: `export | import`;
- to 9front: the first 512 bytes of `export` are the nvram
  (`dd ... of=/dev/sdXX/nvram`), the rest is `/adm/keys`.

Only the AES (dp9ik) key file format is read and written, the one
9front has used since 2016 whenever the nvram has an AES key.

The image runs as uid 65532. A named volume gets that owner from the
image; for a host directory, `chown 65532 dir` or `docker run --user`.
Back it up with `export`: it is the only way into the domain.

## In a p9cloud lab

A lab file runs the server as a system of its own; the first lab's
entry is

```
sys=auth ip=10.99.0.1 dom=auth.p9lab
	container=p9lab-auth image=9front-auth
	volume=p9lab-auth:/var/lib/9front-auth publish=ticket
	ready=ticket
```

`p9cloud start auth` runs it with the ticket port published on the
host's bridge address, and `p9cloud provision user` adds users to it.
The guests use it once the lab file's `auth=` names it.

That lab's domain `p9lab` lives on its fs VM (10.99.0.10). To move it
into the container:

```sh
# on fs, as glenda: cat /dev/sdF0/nvram, /adm/keys, /lib/ndb/auth
# into nvram, keys, ndb on the host, then
V="-v p9lab-auth:/var/lib/9front-auth"
docker run --rm $V -v "$PWD:/in:ro" 9front-auth import /in/nvram /in/keys /in/ndb
```

then set `auth=10.99.0.1` in the lab file, and turn off `authsrv` on
fs (its `cpurc` starts `aux/listen` with `service.auth`). The VMs'
nvram does not change: the domain, and every key in it, stay the same.

Tested: drawterm `-a tcp!10.99.0.1!567` to amd, rv and fs, as roman
and glenda, with the imported domain; with a new domain, a cpu
server booted from `nvram` output, `passwd`, `disable`, `expire`.

## Building

```sh
docker build -t 9front-auth src/docker/auth   # from the top of the repository
make && make test                             # here, on any Unix with a C compiler
```

The image builds `9front-auth` statically on musl, runs `make test`,
and keeps only the binary. Every build from scratch makes the same
binary, byte for byte.

`src/` is the program, `authsrv.c` a port of 9front's
`/sys/src/cmd/auth/authsrv.c`. `lib/` is the subset of drawterm's
libraries it links (libc, libsec, libmp, libauthsrv), unmodified, from
[drawterm](https://git.9front.org/plan9front/drawterm) 3aa7ce0c;
see `lib/LICENSE`.
