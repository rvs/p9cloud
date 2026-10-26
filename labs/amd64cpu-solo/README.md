# amd64cpu-solo

One amd64 cpu server, a QEMU VM, with no root file system of its own:
it waits for drawterm, and the tree drawterm exports (`-r`) is the root
of the session (see ../../src/9front).  A container of `9front-auth` is
the auth server of its domain, `amd64solo`.

| System | Address | What |
|---|---|---|
| host | 10.99.1.1 | the gateway of docker network `amd64solo` |
| auth | 10.99.1.2 | container `amd64solo-auth`; keys in volume `amd64solo-auth` |
| cpu | 10.99.1.10 | QEMU with KVM, 512 MB, in container `amd64solo-cpu`; kernel `9pc64cpuwait`, nvram `secrets/cpu.nvram` |

Besides `p9cloud` (../../src/p9cloud), it takes docker, with the
`9front-qemu` image, KVM, and 9front's drawterm in `$PATH`.  No root.

    cd labs/amd64cpu-solo
    make                          # the files git does not hold, below
    p9cloud net                   # the docker network
    p9cloud start auth            # in the foreground: one terminal each
    p9cloud start cpu
    p9cloud wait cpu              # about 10 s
    p9cloud cpu cpu               # drawterm, as glenda, with root/ as /
    p9cloud cpu cpu 'cat /dev/sysname'
    p9cloud console cpu           # the boot name space
    p9cloud stop cpu
    p9cloud stop auth

`make` makes:

| File | What |
|---|---|
| `9pc64cpuwait` | the kernel: `src/9front/build kernel` |
| `root/` | the 9front 11952 release, 510 MB: `src/9front/build root`; its `/lib/ndb/local` is a link to `p9.lab` |
| `secrets/` | the domain's passwords (`host` is glenda's) and the VM's nvram: `p9cloud provision nvram`, after building the `9front-auth` image from ../../src/docker/auth |

Any amd64 9front tree can be the root:

    drawterm -h 'tcp!10.99.1.10!17019' -a 'tcp!10.99.1.2!567' -u glenda -r DIR

glenda's password is `host` in `secrets/passwords.json`.
