# 9front-qemu

QEMU for p9cloud's amd64 VMs. Build this prerequisite before starting
a lab or rebuilding a kernel in a VM:

```sh
sh src/docker/qemu/tests/test.sh
docker build -t 9front-qemu src/docker/qemu
```

Run those commands from the repository root. The base is pinned to the
same Ubuntu 24.04 digest as the proven recipe; apt packages follow that
release's repositories, so package versions are not pinned.

## Container contract

p9cloud supplies the QEMU command and its arguments, `QEMU_USER=uid:gid`
(numeric, both required), `NET_ADMIN`, and `/dev/net/tun`. For KVM it
also passes `/dev/kvm`. Kernels, disks and state directories are bind
mounted at their host paths; they must be accessible to `QEMU_USER`.

The entrypoint starts as container root, creates `tap0` owned by the
requested uid, and bridges it with `eth0` on `br0`. It removes `eth0`'s
addresses: the guest uses the container's lab address. It then execs
the command as the requested uid and primary gid, with supplementary
groups consisting of that gid and, when present, `/dev/kvm`'s numeric
group. QEMU's sockets and other files belong to the caller.

Use a normal Docker network, never host networking: all tap/bridge
changes belong inside the container. No sudo, privileged container, or
host interface setup is needed. QEMU uses `tap0` with `script=no` and
`downscript=no`; p9cloud already supplies these arguments.

This image includes `qemu-system-x86_64`, not other architectures.
It contains no guest kernel, disk, firmware override, or lab configuration.
Tests mock `ip`, `stat` and `setpriv`; they check validation, network
setup order, argument preservation and uid/gid/KVM group handoff without
changing interfaces or launching a VM.
