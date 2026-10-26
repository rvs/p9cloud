/* speaksfor against ndb files: speaksfor ndb [speaker user expect]... */
#include <u.h>
#include <libc.h>
#include <authsrv.h>
#include "../src/dat.h"

void
usage(void)
{
	exits("usage");
}

void
main(int argc, char **argv)
{
	int i, r, fail;

	if(argc < 2 || (argc-2)%3 != 0)
		usage();
	authdir = argv[1];
	fail = 0;
	for(i = 2; i < argc; i += 3){
		r = speaksfor(argv[i], argv[i+1]);
		if(r != atoi(argv[i+2])){
			print("FAIL speaksfor(%s, %s) = %d\n", argv[i], argv[i+1], r);
			fail = 1;
		}
	}
	exits(fail ? "fail" : nil);
}
