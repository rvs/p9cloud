# 9front, as p9cloud boots it

p9cloud's additions to the 9front 11952 release, in the release's own
layout, and the recipe that builds kernels from them.

    ./build kernel FILE     # 9pc64cpuwait, in about 25 s with KVM
    ./build root DIR        # the release's files, for drawterm -r
    ./build [-i IMAGE] image ROOTFS.tar TAG PROVENANCE.json

The `root` stage of the `Dockerfile` pins the release: the image
`ghcr.io/rvs/9front-amd64` by digest, which serves its tree read-only
over 9P on port 564.  To build, a QEMU machine named `build` boots the
release's own kernel (`9pc64`, pinned by sha256) with its root served
by a container of the `build` stage, which adds `sys/` and makes
`build.rc` its `/cfg/build/termrc`.  That makes the kernel in a ramfs
and writes it, as a tar file, to a scratch disk; the machine then turns
itself off.  It reaches nothing but its root.  Kernels are not
bit-reproducible: they embed the time they were built.

`-i IMAGE` also selects the bootstrap for `root` and `kernel`. Without it,
the release digest and fixed kernel SHA above remain unchanged. An override
kernel must match that image's amd64 kernel manifest entry. Bootstrap kernel
compilation still uses host QEMU (KVM if accessible, otherwise TCG); this is
not the full source rebuild. Full-build labs should explicitly require KVM.

## Packaging a rebuilt root

`image` takes an **already verified** native uncompressed tar, containing
only `9front/` and its contents, an image tag, and caller provenance JSON.
It needs Python 3.11+ (stdlib only) and Docker. For example:

    ./build -i BOOTSTRAP_IMAGE image rootfs.tar 9front-amd64:next provenance.json

The caller must verify source identity, recipe completion and the complete
installed-output inventory before packaging. This helper does not claim
that an existing executable was rebuilt simply because it is present.
Source selection, namespace layering and full-build policy belong to the
caller, not this runtime packager.

Provenance requires `source_commit`, `source_tree` (full Git object IDs),
`recipe_sha256`, `build_log_sha256` (SHA-256 hex), and `bootstrap_image`
(exactly the selected `-i` reference, or the default digest). Other build
facts are retained under `provenance.json`'s `build` field. Do not include
passwords, keys or other credentials. The resolved bootstrap image ID and
input archive hash are recorded separately; the final manifest records
the normalized payload archive hash.

The archive must contain directory parents and nonempty `amd64/9pc64`,
`amd64/bin/rc` (executable), and `amd64/lib/libc.a`. Only ordinary file and
directory headers are accepted: no links, devices, sparse/PAX/GNU extension
headers, absolute/traversing paths, duplicates or incomplete end blocks.
Components `.git`, `secrets`, `work` and `bootstrap-bin` are rejected.
Limits are 250,000 entries, 16 GiB total file content, 4 GiB per file and
4,096 characters per path.

Native tar owners are not Linux identities. Each existing path gets its
seed manifest uid/gid; new paths get `sys/sys`, using the seed's numeric
identity mapping. Permission bits and timestamps come from the new archive;
privileged POSIX mode bits are stripped. All contents are hashed into a
new manifest; old QIDs and release-media offsets are not reused.

The old `/9front` and metadata are removed in an intermediate stage.
A **scratch** final image copies only the remaining Linux runtime, then
adds the normalized payload and fresh metadata: deleted old binaries do
not reappear, and old payload layers are not ancestors of the new image.
Runtime entrypoint, environment, healthcheck, ports and other runtime
settings are retained. Both the legacy seed (no agent interface) and the
current interface seed work. When present, agent-info is regenerated with
the new kernel hash, source revision and explicitly non-pristine provenance;
the existing dispatcher is unchanged. OCI revision labels name the source
commit. The command builds a local image only; it never pushes.

Focused tests (no Docker services or image builds):

    python3 -B -m unittest discover -s src/9front/tests -v

## 9pc64cpuwait

A cpu server with no root file system.  It boots, configures its
network and waits for rcpu clients.  The tree that a drawterm client
exports with `-r` is the root of every process its session runs.  Its
identity is the host owner key in its nvram.  The kernel configuration
is the stock `pc64`.  Only the boot file system differs:
`sys/src/9/pc64/pc64cpuwait.bootfs.proto` adds `aux/listen1`, `ns`,
`read`, `tlssrv` and three files.

- `boot/cpuwait.rc`, a bootrc method:
  `nobootprompt='cpuwait ether /net/ether0 IP MASK'`.
  - It runs ipconfig, sets `/dev/sysname` and publishes `auth=` in
    `/net/ndb`.
  - It posts the boot file system as `/srv/bootfs` and starts
    `aux/listen1 tcp!*!17019 /rc/bin/cpuwait.service`.
  - It gives the console a shell (`cpuwait#`) in the boot name space.
    There, `fshalt` only says `done halting`, since no file server runs.
  - It never returns, so bootrc never mounts a root.
- `boot/cpuwait.ns`, the boot name space's `/lib/namespace`: the boot
  file system stands in for the root until a client supplies one.
- `boot/cpuwait.service`, the stock rcpu service plus a one-shot rc
  function `mount`.
  - Every rcpu client script (drawterm's and 9front's `rcpu`) starts
    with `mount -nc /fd/0 /mnt/term`.
  - After that mount, the function binds `/mnt/term/root` in place of
    the boot file system, then `/bin`, `/rc` and `/usr/$user/tmp` from
    it.
  - It refuses a tree without `/$cputype/bin/rc`, such as drawterm's
    without `-r`.

In a session, nothing runs `ndb/cs`: dial by address.  When drawterm
disconnects, its processes lose their root.
