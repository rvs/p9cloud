/*
 * 9front-auth: the auth server of a 9front authentication
 * domain, for Unix.  One program, one command per operation.
 */
#include <u.h>
#include <libc.h>
#include <authsrv.h>
#include "dat.h"
#include "args.h"

typedef struct Cmd Cmd;
struct Cmd
{
	char	*name;
	void	(*f)(int, char**);
	char	*args;
	char	*help;
};

static Cmd cmds[] = {
	"serve",	cmdserve,	"[-l] [-a addr]",	"answer ticket requests on addr (tcp!*!567)",
	"init",		cmdinit,	"[-l] [-r] authid authdom",	"make a domain; authid owns this server",
	"import",	cmdimport,	"[nvram keys [ndb]]",	"take over a domain (no files: export's output on stdin)",
	"export",	cmdexport,	"",			"write the domain: nvram, then keys",
	"user",		cmduser,	"[-l] [-r] name",	"add a user or set its password (-r: random)",
	"expire",	cmdexpire,	"name yyyymmdd|never",	"set when a user's key expires",
	"disable",	cmddisable,	"name",			"refuse a user's tickets",
	"enable",	cmdenable,	"name",			"accept them again",
	"remove",	cmdremove,	"name",			"delete a user",
	"list",		cmdlist,	"",			"show the domain and its users",
	"speaksfor",	cmdspeaksfor,	"[hostid [uid...]]",	"show or set whom a host owner may speak for",
	"nvram",	cmdnvram,	"[-l] hostowner",	"write the nvram of a cpu server",
	"health",	cmdhealth,	"[addr]",		"exit 0 if the server answers on addr",
};

static void
help(int fd)
{
	Cmd *c;

	fprint(fd, "usage: 9front-auth [-d dir] command [args]\n");
	for(c = cmds; c < cmds+nelem(cmds); c++)
		fprint(fd, "  %-9s %-25s %s\n", c->name, c->args, c->help);
	fprint(fd, "dir holds the keys (%s).  Passwords come from $PASS, else\n"
		"standard input.\n"
		"-l (legacy): p9sk1 (DES) too, for clients without dp9ik.  It takes serve -l and\n"
		"keys made with -l; off by default, as its tickets let eavesdroppers guess passwords.\n",
		authdir);
}

void
usage(void)
{
	help(2);
	exits("usage");
}

void
main(int argc, char *argv[])
{
	Cmd *c;

	argv0 = "9front-auth";
	ARGBEGIN{
	case 'd':
		authdir = EARGF(usage());
		break;
	default:
		usage();
	}ARGEND
	if(argc < 1)
		usage();
	osinit();
	fmtinstall('H', encodefmt);
	for(c = cmds; c < cmds+nelem(cmds); c++)
		if(strcmp(c->name, argv[0]) == 0){
			/* argv[0] is skipped by the command's ARGBEGIN */
			c->f(argc, argv);
			exits(nil);
		}
	if(strcmp(argv[0], "help") == 0){
		help(1);
		exits(nil);
	}
	fprint(2, "9front-auth: unknown command %s\n", argv[0]);
	usage();
}
