# 9front-fs

A 9front tree, served read-only over 9P2000 on port 564, and the
drawterm that attaches a cpuwait cpu server to it:

```sh
docker build -t 9front-fs src/docker/fs
docker run -d --name fs -p 127.0.0.1:9564:564 --read-only --cap-drop ALL \
	--cap-add SYS_CHROOT --cap-add DAC_OVERRIDE --cap-add NET_BIND_SERVICE 9front-fs
docker run --rm -it 9front-fs drawterm      # rcpu to a cpuwait server, -r /9front
```

Run the build from the repository root. Its parts:

- **The seed**, `ghcr.io/rvs/9front-amd64:latest` (`--build-arg
  SEED=`), is the base image. It supplies `/9front`, the Linux
  `passwd`/`group` that name the tree's owners, socat, Python, and the
  agent interface (`serve-9front`: `agents.md`, `kernel`,
  `info --json`). This recipe does not build it.
- **u9fs** is built from `unofficial-mirror/u9fs`, pinned by commit,
  with the patches in `u9fs/`, applied in order with no fuzz. They
  make it a read-only (unless `READ_ONLY=0`), plain-9P2000 server
  that chroots into the tree after loading the host's accounts, with
  bounded frames and strict path elements; each patch says what and
  why. `9p-healthcheck` (`healthcheck.c`) proves the server answers
  version, attach and stat. Both replace the seed's.
- **drawterm** is the latest release of `rvs/drawterm` (`--build-arg
  DRAWTERM=` for another download URL): its `SHA256SUMS` names the
  tarballs, which must match it. The binaries go to
  `/usr/local/bin/drawterm.{linux-amd64,linux-arm64,macos-arm64}`,
  their docs and `SHA256SUMS` to `/usr/local/share/doc/drawterm`.
  Docker refetches `SHA256SUMS` on every build, so a new release
  invalidates the cache by itself.

`9front-entry` is the entrypoint: `drawterm [options]` runs
`cpuwait-drawterm`, anything else goes to the seed's `serve-9front`.
`cpuwait-drawterm` documents its environment (`CPU`, `PORT`, `AUTH`,
`DTUSER`, `ROOT`, `WAIT`, `PASS`); `cpuwait-findcpu` waits for the
cpu server's first bytes on the Docker host.

To change a patch, apply the series to a checkout of the pinned commit,
commit there, and regenerate the series:

```sh
git am src/docker/fs/u9fs/*.patch     # in the u9fs checkout
git format-patch --no-signature --zero-commit -o .../src/docker/fs/u9fs d65923fd17e8
```
