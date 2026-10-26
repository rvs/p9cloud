#include <u.h>
#include <libc.h>
#include <authsrv.h>
#include <libsec.h>
#include "dat.h"

char	*authdir = "/var/lib/9front-auth";
Nvrsafe	nvr;		/* our own key: decrypts the keys file */
User	*users;
int	nusers;
uchar	zeros[32];

char*
dbfile(char *name)
{
	return smprint("%s/%s", authdir, name);
}

/* the whole file (nil: standard input), nul terminated */
char*
readall(char *file, int *np)
{
	char *buf;
	int fd, n, m, sz;

	if(file == nil)
		fd = 0;
	else if((fd = open(file, OREAD)) < 0)
		return nil;
	sz = 8192;
	buf = malloc(sz+1);
	for(n = 0; (m = read(fd, buf+n, sz-n)) > 0; ){
		n += m;
		if(n == sz){
			sz *= 2;
			buf = realloc(buf, sz+1);
		}
	}
	if(fd != 0)
		close(fd);
	if(m < 0){
		free(buf);
		return nil;
	}
	buf[n] = 0;
	if(np != nil)
		*np = n;
	return buf;
}

/* replace file atomically */
int
writeall(char *file, void *buf, int n)
{
	char *tmp;
	int fd, r;

	tmp = smprint("%s.new", file);
	if((fd = create(tmp, OWRITE, 0600)) < 0){
		free(tmp);
		return -1;
	}
	r = write(fd, buf, n);
	close(fd);
	if(r != n || osrename(tmp, file) < 0){
		remove(tmp);
		free(tmp);
		return -1;
	}
	free(tmp);
	return 0;
}

static int
check(void *x, int len, uchar sum, char *what)
{
	if(nvcsum(x, len) == sum)
		return 0;
	werrstr("bad nvram %s checksum", what);
	return -1;
}

/* as readnvram(2) checks a non-file-server nvram */
int
unpacknvr(Nvrsafe *s, char *buf, int n)
{
	memset(s, 0, sizeof *s);
	if(n < sizeof *s){
		werrstr("short nvram");
		return -1;
	}
	memmove(s, buf, sizeof *s);
	if(check(s->authid, ANAMELEN, s->authidsum, "authid") < 0
	|| check(s->authdom, DOMLEN, s->authdomsum, "authdom") < 0
	|| check(s->machkey, DESKEYLEN, s->machsum, "des key") < 0
	|| check(s->aesmachkey, AESKEYLEN, s->aesmachsum, "aes key") < 0)
		return -1;
	s->authid[ANAMELEN-1] = 0;
	s->authdom[DOMLEN-1] = 0;
	if(s->authid[0] == 0){
		werrstr("no authid in nvram");
		return -1;
	}
	return 0;
}

int
readnvr(char *file, Nvrsafe *s)
{
	char *buf;
	int n, r;

	if((buf = readall(file, &n)) == nil)
		return -1;
	r = unpacknvr(s, buf, n);
	memset(buf, 0, n);
	free(buf);
	return r;
}

/* what wrkey(8) writes; des: include the p9sk1 key */
void
mknvr(Nvrsafe *s, char *authid, char *authdom, Authkey *k, int des)
{
	memset(s, 0, sizeof *s);
	strncpy(s->authid, authid, ANAMELEN-1);
	strncpy(s->authdom, authdom, DOMLEN-1);
	if(des)
		memmove(s->machkey, k->des, DESKEYLEN);
	memmove(s->aesmachkey, k->aes, AESKEYLEN);
	s->machsum = nvcsum(s->machkey, DESKEYLEN);
	s->configsum = nvcsum(s->config, CONFIGLEN);
	s->authidsum = nvcsum(s->authid, ANAMELEN);
	s->authdomsum = nvcsum(s->authdom, DOMLEN);
	s->aesmachsum = nvcsum(s->aesmachkey, AESKEYLEN);
}

int
userok(char *user)
{
	char buf[ANAMELEN+1];
	Rune r;
	int i, n;

	/* a string, or a keys file field of ANAMELEN bytes */
	memset(buf, 0, sizeof buf);
	strncpy(buf, user, ANAMELEN);
	if(buf[ANAMELEN-1] != 0 || buf[0] == 0)
		return -1;
	for(i = 0; buf[i]; i += n){
		n = chartorune(&r, buf+i);
		if(r == Runeerror || r < 0x20 || r == 0x7f || r == ' ' || r == '/')
			return -1;
	}
	return 0;
}

/*
 * load nvram and the keys file, as keyfs(4) does:
 * "AES KEYS" iv[16] {name[28] des[7] status warnings
 * expire[4] secret[32] aes[16]}*, AES-CBC from offset 8
 * with the nvram AES key.  (Not the pre-dp9ik DES format.)
 */
int
readkeys(void)
{
	uchar *buf, *p, *ep;
	int n, i, nu, len, off, bad;
	AESstate as;
	User *u;

	if(!osexists(dbfile("nvram"))){
		werrstr("no domain: run init or import");
		return -1;
	}
	if(readnvr(dbfile("nvram"), &nvr) < 0)
		return -1;
	if((buf = (uchar*)readall(dbfile("keys"), &n)) == nil)
		return -1;
	if(n >= 8+16 && memcmp(buf, "AES KEYS", 8) == 0){
		if(tsmemcmp(nvr.aesmachkey, zeros, AESKEYLEN) == 0){
			free(buf);
			werrstr("AES keys file but no AES key in nvram");
			return -1;
		}
		setupAESstate(&as, nvr.aesmachkey, AESKEYLEN, zeros);
		aesCBCdecrypt(buf+8, n-8, &as);
		len = KEYDBLEN + AESKEYLEN;
		off = 8+16;
	}else if(n > 0){
		free(buf);
		werrstr("keys file in the old DES format: convert it with auth/convkeys -a");
		return -1;
	}else{
		len = KEYDBLEN + AESKEYLEN;
		off = 8+16;
	}
	free(users);
	nu = n > off ? (n - off) / len : 0;
	users = mallocz((nu+1) * sizeof(User), 1);
	nusers = 0;
	bad = 0;
	for(i = 0, ep = buf+off; i < nu; i++, ep += len){
		if(userok((char*)ep) < 0){
			bad++;
			continue;
		}
		u = &users[nusers++];
		memmove(u->name, ep, ANAMELEN);
		p = ep + ANAMELEN;
		memmove(u->key.des, p, DESKEYLEN);
		p += DESKEYLEN;
		u->status = *p++;
		u->warnings = *p++;
		u->expire = p[0] | p[1]<<8 | p[2]<<16 | (ulong)p[3]<<24;
		p += 4;
		memmove(u->secret, p, SECRETLEN);
		u->secret[SECRETLEN-1] = 0;
		p += SECRETLEN;
		memmove(u->key.aes, p, AESKEYLEN);
		authpak_hash(&u->key, u->name);
	}
	memset(buf, 0, n);
	free(buf);
	if(nu > 0 && nusers == 0){
		werrstr("keys file does not decrypt with the nvram key");
		return -1;
	}
	if(bad)
		alog("keys: %d bad entries ignored", bad);
	return nusers;
}

/* always the AES format: 9front's since dp9ik */
int
writekeys(void)
{
	uchar *buf, *p;
	int i, n, len, off, r;
	AESstate as;
	User *u;

	if(tsmemcmp(nvr.aesmachkey, zeros, AESKEYLEN) == 0){
		werrstr("no AES key in nvram");
		return -1;
	}
	len = KEYDBLEN + AESKEYLEN;
	off = 8+16;
	n = off + nusers*len;
	buf = mallocz(n, 1);
	genrandom(buf, off);
	p = buf + off;
	for(i = 0; i < nusers; i++){
		u = &users[i];
		strncpy((char*)p, u->name, ANAMELEN);
		p += ANAMELEN;
		memmove(p, u->key.des, DESKEYLEN);
		p += DESKEYLEN;
		*p++ = u->status;
		*p++ = u->warnings;
		*p++ = u->expire;
		*p++ = u->expire >> 8;
		*p++ = u->expire >> 16;
		*p++ = u->expire >> 24;
		memmove(p, u->secret, SECRETLEN);
		p += SECRETLEN;
		memmove(p, u->key.aes, AESKEYLEN);
		p += AESKEYLEN;
	}
	memmove(buf, "AES KEYS", 8);
	setupAESstate(&as, nvr.aesmachkey, AESKEYLEN, zeros);
	aesCBCencrypt(buf+8, n-8, &as);
	r = writeall(dbfile("keys"), buf, n);
	memset(buf, 0, n);
	free(buf);
	return r;
}

User*
finduser(char *name)
{
	int i;

	for(i = 0; i < nusers; i++)
		if(strcmp(users[i].name, name) == 0)
			return &users[i];
	return nil;
}

User*
newuser(char *name)
{
	User *u;

	if(userok(name) < 0 || strlen(name) >= ANAMELEN){
		werrstr("bad user name %s", name);
		return nil;
	}
	users = realloc(users, (nusers+1) * sizeof(User));
	u = &users[nusers++];
	memset(u, 0, sizeof *u);
	strcpy(u->name, name);
	u->status = Sok;
	return u;
}

void
deluser(User *u)
{
	int i;

	i = u - users;
	memmove(&users[i], &users[i+1], (nusers-i-1) * sizeof(User));
	nusers--;
}

/* what reading /mnt/keys/user/{key,aeskey,pakhash} gives */
int
findkey(char *name, Authkey *k)
{
	User *u;

	memset(k, 0, sizeof *k);
	if((u = finduser(name)) == nil){
		werrstr("no such user");
		return 0;
	}
	if(u->status != Sok){
		werrstr("user disabled");
		return 0;
	}
	if(u->expire != 0 && u->expire < (ulong)time(0)){
		werrstr("user expired");
		return 0;
	}
	*k = u->key;
	return 1;
}

void
alog(char *fmt, ...)
{
	char buf[512];
	va_list arg;

	va_start(arg, fmt);
	vseprint(buf, buf+sizeof buf, fmt, arg);
	va_end(arg);
	fprint(2, "%s[%d]: %s\n", argv0, getpid(), buf);
}
