# syntax=docker/dockerfile:1
# 9front-fs: the seed's 9front tree, served over 9P by a patched u9fs,
# with the latest drawterm-kitty release and a `drawterm` command.
ARG SEED=ghcr.io/rvs/9front-amd64:latest
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

FROM ${SEED}
# the Linux drawterms are glibc binaries (libc, libm, libz only)
RUN apk add --no-cache gcompat zlib
COPY --from=u9fs /src/u9fs/u9fs /src/u9fs/9p-healthcheck /usr/local/bin/
COPY --from=drawterm /out/ /usr/local/
COPY 9front-entry cpuwait-drawterm cpuwait-findcpu /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/9front-entry"]
