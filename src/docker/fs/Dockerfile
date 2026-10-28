# syntax=docker/dockerfile:1
# 9front-fs: the seed's /9front, served over 9P by a patched u9fs, with
# the latest drawterm-kitty release.  Only /9front comes from the seed.
ARG SEED=ghcr.io/rvs/9front-amd64:latest@sha256:131b06bdc301a87c1e861e548a79e08f81ce52afe3297b6258f08a63dab194d2
ARG ALPINE=alpine:3.22@sha256:822614ea121e1d40a33655081af10ef85f0aa2ab9e61f649ceaf0c651822433c

FROM ${ALPINE} AS u9fs
RUN apk add --no-cache build-base
ADD https://github.com/unofficial-mirror/u9fs.git#d65923fd17e8b158350d3ccd6a4e32b89b15014a /src/u9fs
COPY u9fs/*.patch healthcheck.c /src/
WORKDIR /src/u9fs
RUN for p in ../*.patch; do patch -p1 -F0 <"$p" || exit; done \
 && make CFLAGS="-O2 -I. -D_GNU_SOURCE" && strip u9fs \
 && cc -O2 -Wall -Wextra -Werror -o 9p-healthcheck ../healthcheck.c

# SHA256SUMS names the release's tarballs; ADD refetches it every build.
FROM ${ALPINE} AS drawterm
ARG DRAWTERM=https://github.com/rvs/drawterm/releases/latest/download
ADD --chmod=644 ${DRAWTERM}/SHA256SUMS /src/
WORKDIR /src
RUN for t in $(awk '{print $2}' SHA256SUMS); do wget -q "$DRAWTERM/$t" || exit; done \
 && sha256sum -c SHA256SUMS && mkdir -p /out/bin /out/share/doc/drawterm \
 && for t in *.tar.gz; do d=${t%.tar.gz}; tar xozf "$t" \
	&& mv "$d/drawterm" "/out/bin/drawterm.${d#drawterm-kitty-*-}" || exit; done \
 && mv "$d"/* SHA256SUMS /out/share/doc/drawterm/

FROM ${SEED} AS seed

FROM ${ALPINE}
# the Linux drawterms are glibc binaries (libc, libm, libz only)
RUN apk add --no-cache python3 socat gcompat zlib
# the Linux names of the tree's Plan 9 owners, which u9fs serves
COPY passwd group /etc/
# --parents: /9front keeps its own owner and mode, not just its contents
COPY --parents --from=seed /9front /
# the seed's riscv64 skeleton (untracked, for 9front's et branch) is
# 755/644; group-writable like the rest, or what builds there is too.
# glenda's profile binds $home/bin/$cputype: one for each port, as amd64's
RUN chmod -R g+w /9front/riscv64 \
 && for a in 386 arm arm64 mips power power64 spim riscv64; do \
	install -d -o 100002 -g 100002 -m 775 /9front/usr/glenda/bin/$a || exit; done
# the cpuwait kernel's sources, kernel.rc, which builds any kernel,
# build.rc, which rebuilds a userland, and sysupdate.rc, which updates the
# sources from a host repository, owned by sys like their neighbours
COPY --chown=110000:110000 --chmod=664 boot/cpuwait.rc boot/cpuwait.service boot/cpuwait.ns /9front/sys/src/9/boot/
COPY --chown=110000:110000 --chmod=664 boot/pc64cpuwait.bootfs.proto /9front/sys/src/9/pc64/
COPY --chown=110000:110000 --chmod=664 boot/kernel.rc /9front/sys/src/9/
COPY --chown=110000:110000 --chmod=664 boot/build.rc boot/sysupdate.rc /9front/sys/src/
# the cpuwait cpu server: kernel, and nvram for glenda in authdom cpuwait
COPY boot/9pc64cpuwait boot/cpu.nvram /boot/
COPY --from=u9fs /src/u9fs/u9fs /src/u9fs/9p-healthcheck /usr/local/bin/
COPY --from=u9fs /src/u9fs/LICENSE /usr/share/licenses/u9fs/
COPY --from=drawterm /out/ /usr/local/
COPY 9front /usr/local/bin/
EXPOSE 564
HEALTHCHECK --interval=60s --timeout=5s --start-period=5s CMD ["/usr/local/bin/9p-healthcheck"]
ENTRYPOINT ["/usr/local/bin/9front"]
