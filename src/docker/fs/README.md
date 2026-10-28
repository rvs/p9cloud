# 9front-fs

A 9front tree, served read-only over 9P2000 on port 564, and the
drawterm that attaches a cpuwait cpu server to it:

```sh
docker build -t 9front-fs src/docker/fs
docker run -d --init --name fs -p 127.0.0.1:9564:564 --read-only --cap-drop ALL \
	--cap-add SYS_CHROOT --cap-add DAC_OVERRIDE --cap-add NET_BIND_SERVICE 9front-fs
docker run --rm -it 9front-fs drawterm      # rcpu to a cpuwait server, -r /9front
```

Run the build from the repository root. Its parts:

- **The seed**, `ghcr.io/rvs/9front-amd64:latest` (`--build-arg
  SEED=`), supplies `/9front` and nothing else; this recipe does not
  build it. `COPY --parents` keeps the owner and mode of `/9front`
  itself. `passwd` and `group` name the tree's numeric owners (sys,
  glenda, ...) for u9fs, so they must match the seed's ids.
- **u9fs** is built from `unofficial-mirror/u9fs`, pinned by commit,
  with the patches in `u9fs/`, applied in order with no fuzz. They
  make it a read-only (unless `READ_ONLY=0`), plain-9P2000 server
  that chroots into the tree after loading the host's accounts, with
  bounded frames and strict path elements; each patch says what and
  why. `9p-healthcheck` (`healthcheck.c`) proves the server answers
  version, attach and stat.
- **drawterm** is the latest release of `rvs/drawterm` (`--build-arg
  DRAWTERM=` for another download URL): its `SHA256SUMS` names the
  tarballs, which must match it. The binaries go to
  `/usr/local/bin/drawterm.{linux-amd64,linux-arm64,macos-arm64}`,
  their docs and `SHA256SUMS` to `/usr/local/share/doc/drawterm`.
  Docker refetches `SHA256SUMS` on every build, so a new release
  invalidates the cache by itself.
- **`/boot`** holds a cpuwait cpu server: `9pc64cpuwait`, the kernel
  of `src/9front/build kernel`, and `cpu.nvram`, its nvram: host owner
  `glenda`, authdom `cpuwait`, password `glen||glenda`. That password is
  well known, so the VM is for a trusted machine only.

`cpu.nvram` is the 512 bytes `auth/wrkey` would write: 9front's
`Nvrsafe` (`authsrv.h`), then zeros. Each field has a checksum byte
after it, 9 plus the sum of the field's bytes, mod 256.

| Offset | Bytes | Field | Contents |
|---|---|---|---|
| 0 | 7+1 | `machkey` | zero: no DES (p9sk1) key; made without `-l` |
| 8 | 7+1 | `authkey` | zero, unused (checksum 0) |
| 16 | 14+1 | `config` | zero: no secstore key |
| 31 | 28+1 | `authid` | `glenda`: the host owner, the user id whose key this is |
| 60 | 48+1 | `authdom` | `cpuwait`: the authentication domain |
| 109 | 16+1 | `aesmachkey` | `ed8193c41caab394c4947068de802873`: glenda's dp9ik key |
| 126 | 386 | | zero |

The AES key is `passtokey(2)`'s: PBKDF2-HMAC-SHA1 of `glen||glenda`
with salt `Plan 9 key derivation`, 9001 rounds, 16 bytes:

```sh
python3 -c 'import hashlib; print(hashlib.pbkdf2_hmac("sha1", b"glen||glenda", b"Plan 9 key derivation", 9001, 16).hex())'
```

Booted with it (`nvram=/dev/sdF0/data`), the cpu server's factotum
holds a dp9ik key for `glenda@cpuwait`, and glenda is its host owner
(`/dev/hostowner`). That one user is all it knows. With no auth
server, drawterm makes the tickets from the same password, so only
glenda can log in.

To remake the nvram:

```sh
docker volume create nvram
docker run --rm -v nvram:/var/lib/9front-auth -e 'PASS=glen||glenda' 9front-auth init glenda cpuwait
docker run --rm -v nvram:/var/lib/9front-auth 9front-auth nvram glenda >src/docker/fs/cpu.nvram
docker volume rm nvram
```

- **`9front`**, the entrypoint, is everything else:

```
$ docker run --rm 9front-fs help
usage: 9front [command]
  serve               serve /9front over 9P2000 on tcp!*!564 (the default)
  drawterm [options]  rcpu to a cpuwait cpu server, exporting /9front as root
  kernel              write the amd64 kernel, 9pc64, to standard output
  agents.md           how to boot that kernel with QEMU on the host
  info --json         this image's metadata and settings
...
```

`serve` runs u9fs under socat, waits for the healthcheck to pass, then
prints the `agents.md` text as its readiness banner. `drawterm` finds the
cpu server (with no `CPU`: `host.docker.internal`, then the default
gateway, i.e. the Docker host), waits until it sends its first bytes
(qemu's hostfwd accepts before the guest listens), and execs drawterm
with `-h tcp!cpu!port -a AUTH -u DTUSER -r /9front` and your options.
`kernel` and `agents.md` compute the kernel's size and SHA-256 from
the tree; the boot script in `agents.md` checks the extracted kernel
against them.

To change a patch, apply the series to a checkout of the pinned commit,
commit there, and regenerate the series:

```sh
git am src/docker/fs/u9fs/*.patch     # in the u9fs checkout
git format-patch --no-signature --zero-commit -o .../src/docker/fs/u9fs d65923fd17e8
```

## Tested

Against a cpuwait VM launched as `9front/et/drawterm/cpuwait.sh` does
(QEMU with KVM, user-mode network, `hostfwd=tcp:0.0.0.0:17019`, nvram
for glenda in authdom `cpuwait`), with this container as its only
client on the default bridge network: no auth server, no `-v`, root
from the image's `/9front`. `drawterm -G -c ...` and an interactive
`drawterm -G` session (`cpu%`, then `exit`, status 0) ran on the VM
with the cpu server found as the default gateway, or as
`host.docker.internal` with `--add-host host.docker.internal:host-gateway`.
A wrong `PASS` ends in `cannot read authenticator`, status 1.
