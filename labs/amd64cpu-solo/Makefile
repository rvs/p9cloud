# The lab's files that are not in git.  See README.md.
top = ../..
P9CLOUD = p9cloud

all: 9pc64cpuwait root secrets/cpu.nvram

9pc64cpuwait: $(top)/src/9front/Dockerfile $(top)/src/9front/build $(top)/src/9front/build.rc \
		$(wildcard $(top)/src/9front/sys/src/9/*/*)
	$(top)/src/9front/build kernel $@

# the release, with this file as its /lib/ndb/local
root:
	$(top)/src/9front/build root $@.new
	ln -sf ../../../p9.lab $@.new/lib/ndb/local
	mv $@.new $@

secrets/cpu.nvram:
	docker build -q -t 9front-auth $(top)/src/docker/auth
	$(P9CLOUD) provision nvram

.PHONY: all
