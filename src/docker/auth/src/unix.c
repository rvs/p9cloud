/*
 * The host side: the system calls drawterm's libraries make
 * (sysopen etc., normally served by drawterm's kernel) and
 * the few Unix services 9front-auth needs.  Kept apart from
 * the Plan 9 headers, which redefine long, open, etc.
 */
#define _DEFAULT_SOURCE 1
#include <sys/types.h>
#include <sys/file.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <netinet/in.h>
#include <netdb.h>
#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <termios.h>
#include <time.h>
#include <unistd.h>

/* Plan 9 open modes */
enum
{
	Pread	= 0,
	Pwrite	= 1,
	Prdwr	= 2,
	Ptrunc	= 16,
	Pcexec	= 32,
	Pexcl	= 0x1000,
	Pdir	= 0x80000000,
};

extern void setoserr(char*);

static int
fail(void)
{
	setoserr(strerror(errno));
	return -1;
}

static int
omode(int mode)
{
	int m;

	switch(mode&3){
	case Pread:
		m = O_RDONLY;
		break;
	case Pwrite:
		m = O_WRONLY;
		break;
	default:
		m = O_RDWR;
		break;
	}
	if(mode & Ptrunc)
		m |= O_TRUNC;
	if(mode & Pcexec)
		m |= O_CLOEXEC;
	if(mode & Pexcl)
		m |= O_EXCL;
	return m;
}

int
sysopen(char *path, int mode)
{
	int fd;

	if((fd = open(path, omode(mode))) < 0)
		return fail();
	return fd;
}

int
syscreate(char *path, int mode, unsigned int perm)
{
	int fd;

	if(perm & Pdir){
		if(mkdir(path, perm & 0777) < 0 && errno != EEXIST)
			return fail();
		if((fd = open(path, O_RDONLY|O_DIRECTORY)) < 0)
			return fail();
		return fd;
	}
	if((fd = open(path, omode(mode)|O_CREAT|O_TRUNC, perm & 0777)) < 0)
		return fail();
	return fd;
}

int
sysread(int fd, void *buf, int n)
{
	ssize_t r;

	while((r = read(fd, buf, n)) < 0 && errno == EINTR)
		;
	if(r < 0)
		return fail();
	return r;
}

int
syswrite(int fd, void *buf, int n)
{
	ssize_t r;
	int m;

	for(m = 0; m < n; m += r)
		if((r = write(fd, (char*)buf+m, n-m)) < 0){
			if(errno == EINTR){
				r = 0;
				continue;
			}
			return fail();
		}
	return n;
}

int
sysclose(int fd)
{
	if(close(fd) < 0)
		return fail();
	return 0;
}

int
sysremove(char *path)
{
	if(remove(path) < 0)
		return fail();
	return 0;
}

int
sysdup(int ofd, int nfd)
{
	int fd;

	if(nfd < 0)
		fd = dup(ofd);
	else
		fd = dup2(ofd, nfd);
	if(fd < 0)
		return fail();
	return fd;
}

/* malloced, as Plan 9 getenv's result is */
char*
sysgetenv(char *name)
{
	char *v;

	if((v = getenv(name)) == NULL)
		return NULL;
	return strdup(v);
}

int
sysgetpid(void)
{
	return getpid();
}

int
systime(int *tp)
{
	int t;

	t = time(NULL);
	if(tp != NULL)
		*tp = t;
	return t;
}

void
osmsleep(int ms)
{
	usleep(ms*1000);
}

static void
die(int sig)
{
	(void)sig;
	_exit(0);
}

void
osinit(void)
{
	umask(077);
	signal(SIGPIPE, SIG_IGN);
	/* as process 1 in a container, there are no default actions */
	signal(SIGTERM, die);
	signal(SIGINT, die);
}

/* [tcp!]host!port or port into host (nil: any) and port */
static char*
splitaddr(char *addr, char **host, char **port)
{
	char *a, *p;

	a = strdup(addr);
	*port = a;
	*host = NULL;
	if(strncmp(a, "tcp!", 4) == 0)
		*port += 4;
	if((p = strrchr(*port, '!')) != NULL){
		*p = 0;
		*host = *port;
		*port = p+1;
	}
	if(*host != NULL && (**host == 0 || strcmp(*host, "*") == 0))
		*host = NULL;
	return a;
}

static int
announce1(char *host, char *port, int family)
{
	struct addrinfo hints, *res, *r;
	int fd, on, off, e;

	memset(&hints, 0, sizeof hints);
	hints.ai_socktype = SOCK_STREAM;
	hints.ai_flags = AI_PASSIVE;
	hints.ai_family = family;
	if((e = getaddrinfo(host, port, &hints, &res)) != 0){
		setoserr((char*)gai_strerror(e));
		return -1;
	}
	fd = -1;
	for(r = res; r != NULL; r = r->ai_next){
		if((fd = socket(r->ai_family, r->ai_socktype, r->ai_protocol)) < 0){
			fail();
			continue;
		}
		on = 1;
		setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &on, sizeof on);
		if(r->ai_family == AF_INET6 && host == NULL){
			off = 0;
			setsockopt(fd, IPPROTO_IPV6, IPV6_V6ONLY, &off, sizeof off);
		}
		if(bind(fd, r->ai_addr, r->ai_addrlen) == 0 && listen(fd, 64) == 0)
			break;
		fail();
		close(fd);
		fd = -1;
	}
	freeaddrinfo(res);
	return fd;
}

/*
 * addr is [tcp!]host!port or a port; host * means
 * every address: IPv6 and IPv4, or IPv4 if there is no IPv6.
 */
int
oslisten(char *addr)
{
	char *a, *host, *port;
	int fd;

	a = splitaddr(addr, &host, &port);
	if(host == NULL){
		if((fd = announce1(NULL, port, AF_INET6)) < 0)
			fd = announce1(NULL, port, AF_INET);
	}else
		fd = announce1(host, port, AF_UNSPEC);
	free(a);
	if(fd >= 0){
		signal(SIGCHLD, SIG_IGN);	/* no zombies */
		fcntl(fd, F_SETFD, FD_CLOEXEC);
	}
	return fd;
}

int
osdial(char *addr)
{
	struct addrinfo hints, *res, *r;
	char *a, *host, *port;
	int fd, e;

	a = splitaddr(addr, &host, &port);
	memset(&hints, 0, sizeof hints);
	hints.ai_socktype = SOCK_STREAM;
	if((e = getaddrinfo(host, port, &hints, &res)) != 0){
		free(a);
		setoserr((char*)gai_strerror(e));
		return -1;
	}
	free(a);
	fd = -1;
	for(r = res; r != NULL; r = r->ai_next){
		if((fd = socket(r->ai_family, r->ai_socktype, r->ai_protocol)) < 0){
			fail();
			continue;
		}
		if(connect(fd, r->ai_addr, r->ai_addrlen) == 0)
			break;
		fail();
		close(fd);
		fd = -1;
	}
	freeaddrinfo(res);
	return fd;
}

int
osaccept(int lfd, char *raddr, int n)
{
	struct sockaddr_storage ss;
	socklen_t len;
	char host[NI_MAXHOST];
	int fd;

	len = sizeof ss;
	while((fd = accept(lfd, (struct sockaddr*)&ss, &len)) < 0)
		if(errno != EINTR && errno != ECONNABORTED)
			return fail();
	if(getnameinfo((struct sockaddr*)&ss, len, host, sizeof host, NULL, 0, NI_NUMERICHOST) != 0)
		strcpy(host, "unknown");
	if(strncmp(host, "::ffff:", 7) == 0)
		memmove(host, host+7, strlen(host+7)+1);
	strncpy(raddr, host, n-1);
	raddr[n-1] = 0;
	return fd;
}

int
osfork(void)
{
	pid_t pid;

	if((pid = fork()) < 0)
		return fail();
	if(pid == 0)
		signal(SIGCHLD, SIG_DFL);
	return pid;
}

/* SIGALRM's default action ends the process */
void
osalarm(int sec)
{
	alarm(sec);
}

int
osrename(char *from, char *to)
{
	if(rename(from, to) < 0)
		return fail();
	return 0;
}

/* serializes changes to the key database */
int
oslock(char *path)
{
	int fd;

	if((fd = open(path, O_RDWR|O_CREAT|O_CLOEXEC, 0600)) < 0)
		return fail();
	while(flock(fd, LOCK_EX) < 0)
		if(errno != EINTR){
			fail();
			close(fd);
			return -1;
		}
	return fd;
}

void
osunlock(int fd)
{
	close(fd);
}

/* turn terminal echo on standard input on or off; 0 if not a terminal */
int
osecho(int on)
{
	struct termios t;

	if(!isatty(0) || tcgetattr(0, &t) < 0)
		return 0;
	if(on)
		t.c_lflag |= ECHO;
	else
		t.c_lflag &= ~ECHO;
	tcsetattr(0, TCSAFLUSH, &t);
	return 1;
}

int
osistty(int fd)
{
	return isatty(fd);
}

int
osexists(char *path)
{
	return access(path, F_OK) == 0;
}
