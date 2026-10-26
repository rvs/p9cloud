# syntax=docker/dockerfile:1
# 9front-auth: the auth server of a 9front authentication domain.
# One static binary in an empty image; the keys live in the volume.
FROM alpine:3.22@sha256:822614ea121e1d40a33655081af10ef85f0aa2ab9e61f649ceaf0c651822433c AS build
RUN apk add --no-cache gcc make musl-dev
WORKDIR /src
COPY Makefile ./
COPY lib lib
COPY src src
COPY tests tests
RUN make -j"$(nproc)" LDFLAGS=-static && strip 9front-auth && make test && mkdir -p keys

FROM scratch
COPY --from=build /src/9front-auth /9front-auth
COPY --from=build --chown=65532:65532 /src/keys /var/lib/9front-auth
USER 65532:65532
VOLUME /var/lib/9front-auth
EXPOSE 567
HEALTHCHECK --interval=30s --timeout=5s CMD ["/9front-auth", "health"]
ENTRYPOINT ["/9front-auth"]
CMD ["serve"]
