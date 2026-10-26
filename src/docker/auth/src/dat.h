/*
 * The key directory of a 9front auth server, kept in files
 * instead of behind keyfs(4) and nvram:
 *
 *	nvram	the auth server's own key, Nvrsafe format (wrkey(8))
 *	keys	the users, keyfs(4) /adm/keys format, encrypted
 *		with the nvram key
 *	ndb	speaks-for relations, /lib/ndb/auth format
 *	keyseed	authsrv's seed for made-up keys
 *	lock	serializes writers
 *
 * nvram, keys and ndb are byte compatible with a 9front auth
 * server's, so they can be copied in either direction.
 */
typedef struct User User;

struct User
{
	char	name[ANAMELEN];
	Authkey	key;
	char	secret[SECRETLEN];
	ulong	expire;		/* 0 == never */
	uchar	status;
	uchar	warnings;
};

enum
{
	Sok,
	Sdisabled,

	Nvramlen = 512,
};

extern	char	*authdir;
extern	Nvrsafe	nvr;
extern	User	*users;
extern	int	nusers;
extern	uchar	zeros[32];

/* main.c */
void	usage(void);

/* authsrv.c */
void	cmdserve(int, char**);
char*	okpasswd(char*);

/* adm.c */
void	cmdinit(int, char**);
void	cmdimport(int, char**);
void	cmdexport(int, char**);
void	cmdnvram(int, char**);
void	cmdspeaksfor(int, char**);
void	cmdlist(int, char**);
void	cmduser(int, char**);
void	cmdexpire(int, char**);
void	cmdenable(int, char**);
void	cmddisable(int, char**);
void	cmdremove(int, char**);
void	cmdhealth(int, char**);

/* keys.c */
char*	dbfile(char*);
char*	readall(char*, int*);
int	writeall(char*, void*, int);
int	unpacknvr(Nvrsafe*, char*, int);
int	readnvr(char*, Nvrsafe*);
void	mknvr(Nvrsafe*, char*, char*, Authkey*, int);
int	readkeys(void);
int	writekeys(void);
int	userok(char*);
User*	finduser(char*);
User*	newuser(char*);
void	deluser(User*);
int	findkey(char*, Authkey*);
void	alog(char*, ...);

/* ndb.c */
int	speaksfor(char*, char*);
int	setspeaksfor(char*, char**, int);

/* unix.c: the host system */
void	osinit(void);
int	oslisten(char*);
int	osaccept(int, char*, int);
int	osdial(char*);
int	osfork(void);
void	osalarm(int);
int	osrename(char*, char*);
int	oslock(char*);
void	osunlock(int);
int	osecho(int);
int	osistty(int);
int	osexists(char*);
