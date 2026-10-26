/*
 * The ticket server: /sys/src/cmd/auth/authsrv.c of 9front,
 * with keys from the key directory (dat.h) instead of keyfs.
 * Speaks dp9ik (AuthPAK, AuthTreq), p9sk1 (AuthTreq, with -l)
 * and password changes (AuthPass); not the challenge/response
 * protocols of other systems.
 */
#include <u.h>
#include <libc.h>
#include <authsrv.h>
#include <libsec.h>
#include "dat.h"
#include "args.h"

typedef struct Keyslot Keyslot;
struct Keyslot
{
	Authkey	k;
	char	id[ANAMELEN];
};

static	char	raddr[128];
static	Keyslot	hkey, akey, ukey;
static	uchar	keyseed[SHA2_256dlen];
static	char	ticketform;
static	int	nodes = 1;

static	void	serve(void);
static	void	pak(Ticketreq*);
static	void	ticketrequest(Ticketreq*);
static	void	changepasswd(Ticketreq*);
static	void	replyerror(char*, ...);
static	void	initkeyseed(void);
static	void	mkkey(char*, Authkey*);
static	void	mkticket(Ticketreq*, Ticket*);
static	void	safecpy(char*, char*, int);

void
cmdserve(int argc, char **argv)
{
	char *addr;
	int lfd, fd;

	addr = "tcp!*!567";
	ARGBEGIN{
	case 'l':
		nodes = 0;
		break;
	case 'a':
		addr = EARGF(usage());
		break;
	default:
		usage();
	}ARGEND
	if(argc != 0)
		usage();

	if(readkeys() < 0)
		sysfatal("%s: %r", authdir);
	initkeyseed();
	if((lfd = oslisten(addr)) < 0)
		sysfatal("listen %s: %r", addr);
	alog("%s: authdom %s, host owner %s, %d users%s", addr,
		nvr.authdom, nvr.authid, nusers, nodes ? "" : ", p9sk1");
	for(;;){
		if((fd = osaccept(lfd, raddr, sizeof raddr)) < 0){
			alog("accept: %r");
			sleep(100);
			continue;
		}
		switch(osfork()){
		case -1:
			alog("fork: %r");
			break;
		case 0:
			close(lfd);
			dup(fd, 0);
			dup(fd, 1);
			close(fd);
			osalarm(10*60);
			/* see changes made since */
			if(readkeys() < 0){
				alog("%s: %r", authdir);
				exits("keys");
			}
			serve();
			exits(nil);
		}
		close(fd);
	}
}

static void
serve(void)
{
	char buf[TICKREQLEN];
	Ticketreq tr;
	int n;

	ticketform = nodes;
	for(;;){
		n = readn(0, buf, sizeof(buf));
		if(n <= 0 || convM2TR(buf, n, &tr) <= 0)
			return;
		switch(tr.type){
		case AuthTreq:
			ticketrequest(&tr);
			break;
		case AuthPass:
			changepasswd(&tr);
			break;
		case AuthPAK:
			pak(&tr);
			continue;
		default:
			alog("unsupported ticket request type %d from %s", tr.type, raddr);
			replyerror("unsupported request type %d", tr.type);
			return;
		}
		/* invalidate pak keys */
		akey.id[0] = 0;
		hkey.id[0] = 0;
		ukey.id[0] = 0;
	}
}

static void
pak1(char *u, Keyslot *k)
{
	uchar y[PAKYLEN];
	PAKpriv p;

	safecpy(k->id, u, sizeof(k->id));
	if(!findkey(k->id, &k->k) || tsmemcmp(k->k.aes, zeros, AESKEYLEN) == 0) {
		alog("pak-fail no AES key for id %s (%r)", k->id);
		/* make one up so caller doesn't know it was wrong */
		mkkey(k->id, &k->k);
		authpak_hash(&k->k, k->id);
	}
	authpak_new(&p, &k->k, y, 0);
	if(write(1, y, PAKYLEN) != PAKYLEN)
		exits(0);
	if(readn(0, y, PAKYLEN) != PAKYLEN)
		exits(0);
	if(authpak_finish(&p, &k->k, y))
		exits(0);
}

static void
pak(Ticketreq *tr)
{
	static uchar ok[1] = {AuthOK};

	if(write(1, ok, 1) != 1)
		exits(0);

	/* invalidate pak keys */
	akey.id[0] = 0;
	hkey.id[0] = 0;
	ukey.id[0] = 0;

	if(tr->hostid[0]) {
		if(tr->authid[0])
			pak1(tr->authid, &akey);
		pak1(tr->hostid, &hkey);
	} else if(tr->uid[0]) {
		pak1(tr->uid, &ukey);
	}

	ticketform = 1;
}

static int
getkey(char *u, Keyslot *k)
{
	/* empty user id is an error */
	if(*u == 0)
		exits(0);

	if(k == &hkey && strcmp(u, k->id) == 0)
		return 1;
	if(k == &akey && strcmp(u, k->id) == 0)
		return 1;
	if(k == &ukey && strcmp(u, k->id) == 0)
		return 1;

	if(ticketform != 0){
		alog("need DES key for %s, but DES is disabled", u);
		replyerror("DES is disabled");
		exits(0);
	}

	return findkey(u, &k->k);
}

static void
ticketrequest(Ticketreq *tr)
{
	char tbuf[2*MAXTICKETLEN+1];
	Ticket t;
	int n, ok;

	if(tr->uid[0] == 0)
		exits(0);
	ok = 1;
	if(!getkey(tr->authid, &akey)){
		/* make one up so caller doesn't know it was wrong */
		mkkey(tr->authid, &akey.k);
		alog("tr-fail authid %s", tr->authid);
		ok = 0;
	}
	if(!getkey(tr->hostid, &hkey)){
		/* make one up so caller doesn't know it was wrong */
		mkkey(tr->hostid, &hkey.k);
		alog("tr-fail hostid %s(%s)", tr->hostid, raddr);
		ok = 0;
	}
	mkticket(tr, &t);
	if(!speaksfor(tr->hostid, tr->uid)){
		mkkey(tr->authid, &akey.k);
		mkkey(tr->hostid, &hkey.k);
		alog("tr-fail %s@%s(%s) -> %s@%s no speaks for",
			tr->uid, tr->hostid, raddr, tr->uid, tr->authid);
		ok = 0;
	}
	n = 0;
	tbuf[n++] = AuthOK;
	t.num = AuthTc;
	n += convT2M(&t, tbuf+n, sizeof(tbuf)-n, &hkey.k);
	t.num = AuthTs;
	n += convT2M(&t, tbuf+n, sizeof(tbuf)-n, &akey.k);
	if(write(1, tbuf, n) != n)
		exits(0);

	if(ok)
		alog("tr-ok %s@%s(%s) -> %s@%s", tr->uid, tr->hostid, raddr, tr->uid, tr->authid);
}

/* change the user's key in the database: under the lock, on fresh data */
static int
setuserkey(char *user, Authkey *k, char *secret)
{
	User *u;
	int lk, r;

	if((lk = oslock(dbfile("lock"))) < 0)
		return 0;
	r = 0;
	if(readkeys() >= 0 && (u = finduser(user)) != nil){
		if(k != nil){
			memmove(u->key.des, k->des, DESKEYLEN);
			memmove(u->key.aes, k->aes, AESKEYLEN);
		}
		if(secret != nil)
			safecpy(u->secret, secret, SECRETLEN);
		r = writekeys() == 0;
	}
	osunlock(lk);
	return r;
}

static void
changepasswd(Ticketreq *tr)
{
	char tbuf[MAXTICKETLEN+1], prbuf[MAXPASSREQLEN], *err;
	Passwordreq pr;
	Authkey nkey;
	Ticket t;
	int n, m, hasdes;

	if(!getkey(tr->uid, &ukey)){
		/* make one up so caller doesn't know it was wrong */
		mkkey(tr->uid, &ukey.k);
		alog("cp-fail uid %s@%s", tr->uid, raddr);
	}
	hasdes = tsmemcmp(ukey.k.des, zeros, DESKEYLEN) != 0;

	/* send back a ticket with a new key */
	mkticket(tr, &t);
	t.num = AuthTp;
	n = 0;
	tbuf[n++] = AuthOK;
	n += convT2M(&t, tbuf+n, sizeof(tbuf)-n, &ukey.k);
	if(write(1, tbuf, n) != n)
		exits(0);

	/* loop trying passwords out */
	for(;;){
		for(n=0; (m = convM2PR(prbuf, n, &pr, &t)) <= 0; n += m){
			m = -m;
			if(m <= n || m > sizeof(prbuf))
				exits(0);
			m -= n;
			if(readn(0, prbuf+n, m) != m)
				exits(0);
		}
		if(pr.num != AuthPass){
			replyerror("protocol botch1: %s", raddr);
			exits(0);
		}
		passtokey(&nkey, pr.old);
		/* users without a p9sk1 key (authadm without -l) keep none */
		if(!hasdes)
			memset(nkey.des, 0, DESKEYLEN);
		if(tsmemcmp(ukey.k.des, nkey.des, DESKEYLEN) != 0){
			replyerror("protocol botch2: %s", raddr);
			continue;
		}
		if(tsmemcmp(ukey.k.aes, zeros, AESKEYLEN) != 0 && tsmemcmp(ukey.k.aes, nkey.aes, AESKEYLEN) != 0){
			replyerror("protocol botch3: %s", raddr);
			continue;
		}
		if(*pr.new){
			err = okpasswd(pr.new);
			if(err){
				replyerror("%s %s", err, raddr);
				continue;
			}
			passtokey(&nkey, pr.new);
			if(!hasdes)
				memset(nkey.des, 0, DESKEYLEN);
		}
		if(!setuserkey(tr->uid, *pr.new ? &nkey : nil, pr.changesecret ? pr.secret : nil)){
			replyerror("can't write key %s", raddr);
			continue;
		}
		memmove(ukey.k.des, nkey.des, DESKEYLEN);
		memmove(ukey.k.aes, nkey.aes, AESKEYLEN);
		break;
	}
	alog("cp-ok %s(%s)%s", tr->uid, raddr, *pr.new ? " new password" : "");

	prbuf[0] = AuthOK;
	if(write(1, prbuf, 1) != 1)
		exits(0);
}

char*
okpasswd(char *p)
{
	static char *trivial[] = {
		"login", "guest", "change me", "passwd", "no passwd", "anonymous", nil
	};
	char passwd[PASSWDLEN], back[PASSWDLEN];
	int i, n;

	strncpy(passwd, p, sizeof passwd - 1);
	passwd[sizeof passwd - 1] = '\0';
	n = strlen(passwd);
	while(n > 0 && passwd[n - 1] == ' ')
		n--;
	passwd[n] = '\0';
	for(i = 0; i < n; i++)
		back[i] = passwd[n - 1 - i];
	back[n] = '\0';
	if(n < 8)
		return "password must be at least 8 chars";
	for(i = 0; trivial[i]; i++)
		if(strcmp(passwd, trivial[i]) == 0
		|| strcmp(back, trivial[i]) == 0)
			return "trivial password";
	return nil;
}

static void
replyerror(char *fmt, ...)
{
	char buf[AERRLEN+1];
	va_list arg;

	memset(buf, 0, sizeof(buf));
	va_start(arg, fmt);
	vseprint(buf + 1, buf + sizeof(buf), fmt, arg);
	va_end(arg);
	buf[AERRLEN] = 0;
	buf[0] = AuthErr;
	write(1, buf, AERRLEN+1);
	alog("%s", buf+1);
}

static void
initkeyseed(void)
{
	char *file, *buf;
	int n;

	file = dbfile("keyseed");
	if((buf = readall(file, &n)) != nil && n == sizeof(keyseed)){
		memmove(keyseed, buf, n);
		free(buf);
		return;
	}
	free(buf);
	genrandom(keyseed, sizeof(keyseed));
	if(writeall(file, keyseed, sizeof(keyseed)) < 0)
		alog("initkeyseed: can't create %s: %r", file);
}

static void
mkkey(char *id, Authkey *a)
{
	uchar h[SHA2_256dlen];

	genrandom((uchar*)a, sizeof(Authkey));

	/*
	 * the DES key has to be constant for a user in each response,
	 * so we make one up pseudo randomly from a keyseed and user name.
	 */
	hmac_sha2_256((uchar*)id, strlen(id), keyseed, sizeof(keyseed), h, nil);
	memmove(a->des, h, DESKEYLEN);
	memset(h, 0, sizeof(h));
}

static void
mkticket(Ticketreq *tr, Ticket *t)
{
	memset(t, 0, sizeof(Ticket));
	memmove(t->chal, tr->chal, CHALLEN);
	safecpy(t->cuid, tr->uid, ANAMELEN);
	safecpy(t->suid, tr->uid, ANAMELEN);
	genrandom(t->key, NONCELEN);
	t->form = ticketform;
}

static void
safecpy(char *to, char *from, int len)
{
	strncpy(to, from, len);
	to[len-1] = 0;
}
