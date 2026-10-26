package ndb

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIPQuery checks IPInfo against 9front: testdata/ipquery.out is what
// ndb/ipquery printed for each line of testdata/queries on a 9front
// system (testdata/ipquery.rc).
func TestIPQuery(t *testing.T) {
	out, err := os.ReadFile("testdata/ipquery.out")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	lines := strings.Split(string(out), "\n")
	for i := 0; i+1 < len(lines); i += 2 {
		q, ok := strings.CutPrefix(lines[i], "# ")
		if !ok {
			t.Fatalf("ipquery.out:%d: %q is not a query", i+1, lines[i])
		}
		want[q] = lines[i+1]
	}
	f, err := os.Open("testdata/queries")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dbs := map[string]*DB{}
	n := 0
	for sc := bufio.NewScanner(f); sc.Scan(); {
		q := sc.Text()
		if q == "" || q[0] == '#' {
			continue
		}
		n++
		w := strings.Fields(q)
		db := dbs[w[0]]
		if db == nil {
			if db, err = Open(filepath.Join("testdata", w[0], "local")); err != nil {
				t.Fatal(err)
			}
			dbs[w[0]] = db
		}
		exp, ok := want[strings.Join(w, " ")]
		if !ok {
			t.Errorf("%s: not in ipquery.out; run ipquery.rc on 9front", q)
			continue
		}
		if got := Format(db.IPInfo(w[1], w[2], w[3:]...)); got != exp {
			t.Errorf("%s:\n got %q\nwant %q", q, got, exp)
		}
	}
	if n != len(want) {
		t.Errorf("%d queries, %d answers", n, len(want))
	}
}
