# 9front-qemu: QEMU for p9cloud's VMs, on the lab's docker network.
FROM ubuntu:24.04@sha256:68ee22004a9109b44973313b02ff7b1de250f1de0f5ecb628d73f579aaff33ac
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
	qemu-system-x86 iproute2 && rm -rf /var/lib/apt/lists/*
COPY entrypoint /usr/local/bin/9front-qemu
ENTRYPOINT ["/usr/local/bin/9front-qemu"]
