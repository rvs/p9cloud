/*
 * Speaks-for relations, in the ndb file as in 9front's
 * /lib/ndb/auth:
 *
 *	hostid=glenda
 *		uid=!sys uid=!adm uid=*
 *
 * lets the host owner glenda get tickets for anyone but sys and
 * adm.  An entry is a line starting in column 0 and the indented
 * lines after it; as with ndbsearch in 9front's authsrv, the first
 * entry with hostid=speaker decides.
 */
#include <u.h>
#include <libc.h>
#include <authsrv.h>
#include "dat.h"

static int
entry(char **a, char **v, int na, char *speaker, char *user, int *ok)
{
	char notuser[ANAMELEN+2];
	int i;

	for(i = 0; i < na; i++)
		if(strcmp(a[i], "hostid") == 0 && strcmp(v[i], speaker) == 0)
			break;
	if(i == na)
		return 0;
	snprint(notuser, sizeof notuser, "!%s", user);
	*ok = 0;
	for(i = 0; i < na; i++){
		if(strcmp(a[i], "uid") != 0)
			continue;
		if(strcmp(v[i], notuser) == 0){
			*ok = 0;
			break;
		}
		if(*v[i] == '*' || strcmp(v[i], user) == 0)
			*ok = 1;
	}
	return 1;
}

int
speaksfor(char *speaker, char *user)
{
	char *buf, *line, *next, *p, *f[64], *a[256], *v[256];
	int i, n, na, ok;

	if(strcmp(speaker, user) == 0)
		return 1;
	if((buf = readall(dbfile("ndb"), nil)) == nil)
		return 0;
	ok = 0;
	na = 0;
	for(line = buf; line != nil; line = next){
		if((next = strchr(line, '\n')) != nil)
			*next++ = 0;
		if((p = strchr(line, '#')) != nil)
			*p = 0;
		/* an entry starts in column 0 */
		if(*line != ' ' && *line != '\t' && *line != 0){
			if(entry(a, v, na, speaker, user, &ok))
				goto out;
			na = 0;
		}
		n = tokenize(line, f, nelem(f));
		for(i = 0; i < n && na < nelem(a); i++){
			if((p = strchr(f[i], '=')) == nil)
				continue;
			*p++ = 0;
			a[na] = f[i];
			v[na++] = p;
		}
	}
	entry(a, v, na, speaker, user, &ok);
out:
	free(buf);
	return ok;
}


/* does line, the first of an entry, say attr=val? */
static int
hasattr(char *line, char *attr, char *val)
{
	char *buf, *p, *f[64];
	int i, n, r;

	buf = strdup(line);
	if((p = strchr(buf, '#')) != nil)
		*p = 0;
	r = 0;
	n = tokenize(buf, f, nelem(f));
	for(i = 0; i < n; i++){
		if((p = strchr(f[i], '=')) == nil)
			continue;
		*p++ = 0;
		if(strcmp(f[i], attr) == 0 && strcmp(p, val) == 0)
			r = 1;
	}
	free(buf);
	return r;
}

/* replace hostid's entry by one with these uids; none removes it */
int
setspeaksfor(char *hostid, char **uid, int nuid)
{
	char *buf, *line, *next, *s;
	int i, r, skip;
	Fmt f;

	for(i = 0; i < nuid; i++)
		if(*uid[i] == 0 || strpbrk(uid[i], " \t\n=#'") != nil){
			werrstr("bad uid %s", uid[i]);
			return -1;
		}
	if((buf = readall(dbfile("ndb"), nil)) == nil)
		buf = strdup("");
	fmtstrinit(&f);
	skip = 0;
	for(line = buf; *line != 0; line = next){
		if((next = strchr(line, '\n')) != nil)
			*next++ = 0;
		else
			next = line + strlen(line);
		if(*line != ' ' && *line != '\t')
			skip = hasattr(line, "hostid", hostid);
		if(!skip)
			fmtprint(&f, "%s\n", line);
	}
	free(buf);
	if(nuid > 0){
		fmtprint(&f, "hostid=%s\n\t", hostid);
		for(i = 0; i < nuid; i++)
			fmtprint(&f, "%suid=%s", i ? " " : "", uid[i]);
		fmtprint(&f, "\n");
	}
	s = fmtstrflush(&f);
	r = writeall(dbfile("ndb"), s, strlen(s));
	free(s);
	return r;
}
