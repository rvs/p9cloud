# root: the 9front 11952 amd64 release, served read-only over 9P on
# port 564.  build: root with p9cloud's additions to the kernel
# sources and the termrc of the machine named build.  See build.
ARG BOOTSTRAP_IMAGE=ghcr.io/rvs/9front-amd64@sha256:e50995696fd75749ef572c5a6b145c327436d80331ab02e8bef08fbd491b2822
FROM ${BOOTSTRAP_IMAGE} AS root

FROM root AS build
COPY sys /9front/sys/
COPY build.rc /9front/cfg/build/termrc
