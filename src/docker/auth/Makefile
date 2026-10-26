# 9front-auth: src/ plus the parts of drawterm's libraries it uses (lib/).
# make LDFLAGS=-static for a static binary (as the Dockerfile does, on musl).
CC=cc
CFLAGS=-O2 -g -Wall -Wno-missing-braces -Wno-parentheses -Wno-unused-result \
	-Wno-main -Wno-unused-but-set-variable -Wno-stringop-truncation
P9CFLAGS=-Ilib -Ilib/include -DPTHREAD -D_THREAD_SAFE
LDFLAGS=

SRC=src/main.c src/authsrv.c src/adm.c src/keys.c src/ndb.c src/p9.c
LIB=$(wildcard lib/libc/*.c lib/libsec/*.c lib/libmp/*.c lib/libauthsrv/*.c lib/posix-port/*.c)
OBJ=$(SRC:.c=.o) src/unix.o $(LIB:.c=.o)

9front-auth: $(OBJ)
	$(CC) $(LDFLAGS) -o $@ $(OBJ) -pthread

# the host side, without the Plan 9 headers
src/unix.o: src/unix.c
	$(CC) $(CFLAGS) -c -o $@ src/unix.c

src/%.o: src/%.c src/dat.h
	$(CC) $(CFLAGS) $(P9CFLAGS) -c -o $@ $<

lib/%.o: lib/%.c
	$(CC) $(CFLAGS) -w $(P9CFLAGS) -c -o $@ $<

tests/speaksfor: tests/speaksfor.o src/ndb.o src/keys.o src/p9.o src/unix.o $(LIB:.c=.o)
	$(CC) -o $@ $^ -pthread

tests/%.o: tests/%.c src/dat.h
	$(CC) $(CFLAGS) $(P9CFLAGS) -c -o $@ $<

test: 9front-auth tests/speaksfor
	tests/test.sh

clean:
	rm -f 9front-auth tests/speaksfor tests/*.o $(OBJ)
