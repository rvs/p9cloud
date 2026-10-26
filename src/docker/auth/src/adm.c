/*
 * Maintaining the key directory: what auth/wrkey, changeuser,
 * enable, disable and keyfs do on a 9front auth server.
 */
#include <u.h>
#include <libc.h>
#include <authsrv.h>
#include <libsec.h>
#include "dat.h"
#include "args.h"

static	int	des;
static	int	lk = -1;

static void
lockdb(void)
{
	if(lk < 0 && (lk = oslock(dbfile("lock"))) < 0)
		sysfatal("lock %s: %r", authdir);
}

static void
load(void)
{
	if(!osexists(dbfile("nvram")))
		sysfatal("no domain in %s: run init or import", authdir);
	lockdb();
	if(readkeys() < 0)
		sysfatal("%s: %r", authdir);
}

static void
save(void)
{
	if(writekeys() < 0)
		sysfatal("write %s: %r", dbfile("keys"));
}

static char*
readline(char *prompt)
{
	static char buf[256];
	int n, r, echo;
	char c;

	if(osistty(0))
		fprint(2, "%s: ", prompt);
	echo = osecho(0);
	for(n = 0; (r = read(0, &c, 1)) == 1 && c != '\n'; )
		if(n < sizeof buf - 1)
			buf[n++] = c;
	buf[n] = 0;
	if(echo){
		osecho(1);
		fprint(2, "\n");
	}
	if(r <= 0 && n == 0)
		sysfatal("no password: set $PASS or use a terminal");
	return buf;
}

/* a new password: $PASS, else asked for twice */
static char*
password(char *user)
{
	char *p, *err, *prompt, first[256];

	if((p = getenv("PASS")) != nil && *p != 0 || !osistty(0)){
		/* $PASS, or one line of a pipe */
		if(p == nil || *p == 0)
			p = strdup(readline(smprint("%s password", user)));
		if((err = okpasswd(p)) != nil)
			sysfatal("%s", err);
		return p;
	}
	prompt = smprint("%s password", user);
	for(;;){
		strecpy(first, first+sizeof first, readline(prompt));
		if((err = okpasswd(first)) != nil){
			fprint(2, "%s\n", err);
			continue;
		}
		if(strcmp(first, readline("confirm password")) != 0){
			fprint(2, "mismatch\n");
			continue;
		}
		break;
	}
	free(prompt);
	return strdup(first);
}

/* for host owners: nobody logs in, cpu servers get the key from nvram */
static char*
randpass(void)
{
	uchar r[12];

	genrandom(r, sizeof r);
	return smprint("%.*H", sizeof r, r);
}

static void
mkauthkey(Authkey *k, char *user, char *pass)
{
	if(strlen(pass) >= PASSWDLEN)
		sysfatal("password longer than %d bytes", PASSWDLEN-1);
	passtokey(k, pass);
	if(!des)
		memset(k->des, 0, DESKEYLEN);
	authpak_hash(k, user);
}

static void
binout(void)
{
	if(osistty(1))
		sysfatal("binary output: redirect it to a file");
}

/* the 512 byte nvram partition, to file or standard output */
static void
writenvr(char *file, Nvrsafe *s)
{
	uchar buf[Nvramlen];

	memset(buf, 0, sizeof buf);
	memmove(buf, s, sizeof *s);
	if(file == nil){
		if(write(1, buf, sizeof buf) != sizeof buf)
			sysfatal("write: %r");
	}else if(writeall(file, buf, sizeof buf) < 0)
		sysfatal("write %s: %r", file);
	memset(buf, 0, sizeof buf);
}

/* 9front's /lib/ndb/auth: the host owner speaks for all but sys and adm */
static void
defndb(char *authid)
{
	static char *uids[] = { "!sys", "!adm", "*" };

	if(!osexists(dbfile("ndb")) && setspeaksfor(authid, uids, nelem(uids)) < 0)
		sysfatal("write ndb: %r");
}

static void
fresh(void)
{
	if(create(authdir, OREAD, DMDIR|0700) < 0 && !osexists(authdir))
		sysfatal("create %s: %r", authdir);
	lockdb();
	if(osexists(dbfile("nvram")) || osexists(dbfile("keys")))
		sysfatal("%s already has a domain", authdir);
}

void
cmdinit(int argc, char **argv)
{
	char *pass;
	int random;
	Authkey k;
	User *u;

	random = 0;
	ARGBEGIN{
	case 'l':
		des = 1;
		break;
	case 'r':
		random = 1;
		break;
	default:
		usage();
	}ARGEND
	if(argc != 2 || userok(argv[0]) < 0 || strlen(argv[0]) >= ANAMELEN
	|| *argv[1] == 0 || strlen(argv[1]) >= DOMLEN)
		usage();
	fresh();
	pass = random ? randpass() : password(argv[0]);
	mkauthkey(&k, argv[0], pass);
	mknvr(&nvr, argv[0], argv[1], &k, des);
	writenvr(dbfile("nvram"), &nvr);

	/* the host owner is a user of the domain, as on 9front */
	users = nil;
	nusers = 0;
	u = newuser(argv[0]);
	u->key = k;
	save();
	defndb(argv[0]);
	print("authdom %s, host owner %s\n", argv[1], argv[0]);
}

/*
 * a 9front auth server's nvram, /adm/keys and /lib/ndb/auth;
 * or, on standard input, export's: nvram (512 bytes), then keys
 */
void
cmdimport(int argc, char **argv)
{
	char *buf, *keys;
	int n, nkeys;

	ARGBEGIN{
	default:
		usage();
	}ARGEND
	if(argc != 0 && argc != 2 && argc != 3)
		usage();
	fresh();
	if(argc == 0){
		if((buf = readall(nil, &n)) == nil)
			sysfatal("read: %r");
		if(n < Nvramlen || unpacknvr(&nvr, buf, n) < 0)
			sysfatal("standard input: not nvram and keys: %r");
		keys = buf + Nvramlen;
		nkeys = n - Nvramlen;
	}else{
		if(readnvr(argv[0], &nvr) < 0)
			sysfatal("%s: %r", argv[0]);
		if((buf = readall(argv[1], &nkeys)) == nil)
			sysfatal("%s: %r", argv[1]);
		keys = buf;
	}
	if(writeall(dbfile("keys"), keys, nkeys) < 0)
		sysfatal("write keys: %r");
	free(buf);
	writenvr(dbfile("nvram"), &nvr);
	if(readkeys() < 0){
		remove(dbfile("keys"));
		remove(dbfile("nvram"));
		sysfatal("keys: %r");
	}
	if(argc == 3){
		if((buf = readall(argv[2], &n)) == nil)
			sysfatal("%s: %r", argv[2]);
		if(writeall(dbfile("ndb"), buf, n) < 0)
			sysfatal("write ndb: %r");
		free(buf);
	}
	defndb(nvr.authid);
	print("authdom %s, host owner %s, %d users\n", nvr.authdom, nvr.authid, nusers);
}

/*
 * the nvram, then the keys file: import reads it back; on 9front,
 * the first 512 bytes are the auth server's nvram, the rest /adm/keys
 */
void
cmdexport(int argc, char **argv)
{
	char *buf;
	int n;

	ARGBEGIN{
	default:
		usage();
	}ARGEND
	if(argc != 0)
		usage();
	binout();
	load();
	writenvr(nil, &nvr);
	if((buf = readall(dbfile("keys"), &n)) == nil)
		sysfatal("%r");
	if(write(1, buf, n) != n)
		sysfatal("write: %r");
}

/* what wrkey writes on a cpu server whose host owner is name */
void
cmdnvram(int argc, char **argv)
{
	Nvrsafe s;
	Authkey k;

	ARGBEGIN{
	case 'l':
		des = 1;
		break;
	default:
		usage();
	}ARGEND
	if(argc != 1)
		usage();
	binout();
	load();
	if(!findkey(argv[0], &k))
		sysfatal("%s: %r", argv[0]);
	if(des && tsmemcmp(k.des, zeros, DESKEYLEN) == 0)
		sysfatal("%s has no p9sk1 key", argv[0]);
	mknvr(&s, argv[0], nvr.authdom, &k, des);
	writenvr(nil, &s);
	memset(&s, 0, sizeof s);
	memset(&k, 0, sizeof k);
}

void
cmdspeaksfor(int argc, char **argv)
{
	char *buf;

	ARGBEGIN{
	default:
		usage();
	}ARGEND
	load();
	if(argc > 0){
		if(userok(argv[0]) < 0)
			sysfatal("bad hostid %s", argv[0]);
		if(setspeaksfor(argv[0], argv+1, argc-1) < 0)
			sysfatal("write ndb: %r");
	}
	if((buf = readall(dbfile("ndb"), nil)) != nil)
		print("%s", buf);
}

/* yyyymmdd, 00:00 UTC, in seconds since the epoch; never is 0 */
static ulong
parsedate(char *s)
{
	long d, y, m, era, yoe, doy, doe;
	char *e;

	if(strcmp(s, "never") == 0)
		return 0;
	d = strtol(s, &e, 10);
	if(*e != 0 || strlen(s) != 8)
		sysfatal("bad date %s, want yyyymmdd or never", s);
	y = d/10000;
	m = d/100%100;
	d %= 100;
	if(y < 1970 || y > 2105 || m < 1 || m > 12 || d < 1 || d > 31)
		sysfatal("bad date %s", s);
	/* days from civil, proleptic Gregorian */
	y -= m <= 2;
	era = y / 400;
	yoe = y - era*400;
	doy = (153*(m > 2 ? m-3 : m+9) + 2)/5 + d-1;
	doe = yoe*365 + yoe/4 - yoe/100 + doy;
	return (ulong)(era*146097 + doe - 719468) * 86400;
}

/* the inverse: civil from days */
static char*
fmtdate(ulong t)
{
	long z, era, doe, yoe, y, doy, mp, d, m;

	if(t == 0)
		return "never";
	z = t/86400 + 719468;
	era = z / 146097;
	doe = z - era*146097;
	yoe = (doe - doe/1460 + doe/36524 - doe/146096) / 365;
	y = yoe + era*400;
	doy = doe - (365*yoe + yoe/4 - yoe/100);
	mp = (5*doy + 2)/153;
	d = doy - (153*mp + 2)/5 + 1;
	m = mp < 10 ? mp+3 : mp-9;
	return smprint("%04ld%02ld%02ld", y + (m <= 2), m, d);
}

void
cmdlist(int argc, char **argv)
{
	User *u;

	ARGBEGIN{
	default:
		usage();
	}ARGEND
	if(argc != 0)
		usage();
	load();
	print("authdom %s, host owner %s\n", nvr.authdom, nvr.authid);
	for(u = users; u < users+nusers; u++)
		print("%-27s %-8s %-8s%s%s\n", u->name,
			u->status == Sok ? "ok" : "disabled", fmtdate(u->expire),
			tsmemcmp(u->key.aes, zeros, AESKEYLEN) != 0 ? " dp9ik" : "",
			tsmemcmp(u->key.des, zeros, DESKEYLEN) != 0 ? " p9sk1" : "");
}

void
cmduser(int argc, char **argv)
{
	char *pass;
	int random;
	Authkey k;
	User *u;

	random = 0;
	ARGBEGIN{
	case 'l':
		des = 1;
		break;
	case 'r':
		random = 1;
		break;
	default:
		usage();
	}ARGEND
	if(argc != 1)
		usage();
	load();
	if(finduser(argv[0]) == nil && userok(argv[0]) < 0)
		sysfatal("bad user name %s", argv[0]);
	pass = random ? randpass() : password(argv[0]);
	mkauthkey(&k, argv[0], pass);
	if((u = finduser(argv[0])) == nil){
		if((u = newuser(argv[0])) == nil)
			sysfatal("%r");
		print("new user %s\n", u->name);
	}else
		print("new password for %s\n", u->name);
	u->key = k;
	if(strcmp(u->name, nvr.authid) != 0){
		save();
		return;
	}
	/*
	 * the keys file is encrypted with the host owner's key:
	 * keep the nvram file and what nvram authid writes the same
	 */
	mknvr(&nvr, nvr.authid, nvr.authdom, &k, des);
	save();
	writenvr(dbfile("nvram"), &nvr);
	fprint(2, "9front-auth: new host owner key: give its cpu servers new nvram\n");
}

static User*
loaduser(int argc, char **argv)
{
	User *u;

	ARGBEGIN{
	default:
		usage();
	}ARGEND
	if(argc != 1)
		usage();
	load();
	if((u = finduser(argv[0])) == nil)
		sysfatal("no such user %s", argv[0]);
	return u;
}

void
cmdexpire(int argc, char **argv)
{
	User *u;

	if(argc != 3)
		usage();
	u = loaduser(2, argv);
	u->expire = parsedate(argv[2]);
	save();
}

void
cmdenable(int argc, char **argv)
{
	User *u;

	u = loaduser(argc, argv);
	u->status = Sok;
	u->warnings = 0;
	save();
}

void
cmddisable(int argc, char **argv)
{
	User *u;

	u = loaduser(argc, argv);
	if(strcmp(u->name, nvr.authid) == 0)
		sysfatal("%s is the host owner", u->name);
	u->status = Sdisabled;
	save();
}

void
cmdremove(int argc, char **argv)
{
	User *u;

	u = loaduser(argc, argv);
	if(strcmp(u->name, nvr.authid) == 0)
		sysfatal("%s is the host owner", u->name);
	deluser(u);
	save();
}

void
cmdhealth(int argc, char **argv)
{
	int fd;

	ARGBEGIN{
	default:
		usage();
	}ARGEND
	if(argc > 1)
		usage();
	if((fd = osdial(argc == 1 ? argv[0] : "tcp!127.0.0.1!567")) < 0)
		sysfatal("%r");
	close(fd);
}
