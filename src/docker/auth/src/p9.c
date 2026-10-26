/*
 * What drawterm's libc, libsec and libauthsrv expect from
 * drawterm's kernel, for programs that run without one.
 */
#include <u.h>
#include <libc.h>

char	*argv0;
static	char	errbuf[ERRMAX];

void
setoserr(char *s)
{
	utfecpy(errbuf, errbuf+sizeof errbuf, s);
}

void
werrstr(char *fmt, ...)
{
	va_list arg;

	va_start(arg, fmt);
	vsnprint(errbuf, sizeof errbuf, fmt, arg);
	va_end(arg);
}

int
errstr(char *buf, uint n)
{
	char tmp[ERRMAX];

	if(n > ERRMAX)
		n = ERRMAX;
	utfecpy(tmp, tmp+n, buf);
	utfecpy(buf, buf+n, errbuf);
	utfecpy(errbuf, errbuf+n, tmp);
	return utflen(buf);
}

int
rerrstr(char *buf, uint n)
{
	utfecpy(buf, buf+n, errbuf);
	return utflen(buf);
}

int
__errfmt(Fmt *f)
{
	return fmtstrcpy(f, errbuf);
}

int
print(char *fmt, ...)
{
	va_list arg;
	int n;

	va_start(arg, fmt);
	n = vfprint(1, fmt, arg);
	va_end(arg);
	return n;
}

/* single threaded */
void
qlock(QLock *q)
{
	USED(q);
}

void
qunlock(QLock *q)
{
	USED(q);
}

void
setmalloctag(void *v, uintptr pc)
{
	USED(v, pc);
}
