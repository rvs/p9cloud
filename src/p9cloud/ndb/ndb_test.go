package ndb

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func pairs(s ...string) []Pair {
	var ps []Pair
	for _, x := range s {
		a, v, _ := strings.Cut(x, "=")
		ps = append(ps, Pair{a, v})
	}
	return ps
}

func TestParseLine(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []Pair
	}{
		{"sys=fs ip=10.0.1.10\n", pairs("sys=fs", "ip=10.0.1.10")},
		{"\tbootf=/386/9pc\n", pairs("bootf=/386/9pc")},
		{"flag other=x\n", pairs("flag", "other=x")},
		{"empty= next\n", pairs("empty", "next")},
		{"spaced =yes\n", pairs("spaced=yes")},
		{"q=\"a b#c\" r=1\n", pairs("q=a b#c", "r=1")},
		{"q=\"open quote\n", pairs("q=open quote")},
		{"q=\"x\"y\n", pairs("q=x", "y")},
		{"v=a#b\n", pairs("v=a#b")},
		{"note=#comment here\n", pairs("note")},
		{"a=1 # comment\n", pairs("a=1")},
		{"# comment\n", nil},
		{"   \n", nil},
		{"=noattr\n", pairs("=noattr")},
		{"crlf=1\r\n", pairs("crlf=1")},
		{"abcdefghijklmnopqrstuvwxyz0123456789=long\n", pairs("abcdefghijklmnopqrstuvwxyz01234=long")},
	} {
		if got := ParseLine(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParse(t *testing.T) {
	src := "# header\n" +
		"sys=a ip=1.2.3.4\n" +
		"# comment inside\n" +
		"\t   \n" +
		"\ttag=a\n" +
		"sys=b\n" +
		"\n" +
		"\tsys=orphan\n" +
		"sys=c\n" +
		"\tlast=dropped"
	es, warns := Parse("f", []byte(src))
	var got [][]Pair
	var lines []int
	for _, e := range es {
		got = append(got, e.Pairs())
		lines = append(lines, e.Line)
	}
	want := [][]Pair{pairs("sys=a", "ip=1.2.3.4", "tag=a"), pairs("sys=b"), pairs("sys=orphan"), pairs("sys=c")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entries %q, want %q", got, want)
	}
	if !reflect.DeepEqual(lines, []int{2, 6, 8, 9}) {
		t.Errorf("lines %v", lines)
	}
	if len(es[0].Lines) != 2 {
		t.Errorf("sys=a has %d lines, want 2", len(es[0].Lines))
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "f:10:") {
		t.Errorf("warnings %q, want one for f:10", warns)
	}
	if _, w := Parse("g", []byte("a=1\n")); w != nil {
		t.Errorf("warnings %q for a final newline", w)
	}
}

func TestEntry(t *testing.T) {
	es, _ := Parse("f", []byte("ipnet=p9lab ip=10.0.0.0\n\tdisk=a disk=b\n"))
	e := es[0]
	if v, ok := e.Find("disk"); !ok || v != "a" {
		t.Errorf("Find(disk) = %q, %v", v, ok)
	}
	if _, ok := e.Find("nope"); ok {
		t.Error("Find(nope) found")
	}
	if got := e.String(); got != "ipnet=p9lab" {
		t.Errorf("String() = %q", got)
	}
	es, _ = Parse("f", []byte("database\n\tfile=x\n"))
	if got := es[0].String(); got != "database" {
		t.Errorf("String() = %q", got)
	}
}

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, s := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"p9.lab": "database=\n\tfile=/net/ndb\n\tfile=/lib/ndb/local\n\tfile=/lib/ndb/common\n\tfile=/lib/ndb/missing\n\nsys=a\n",
		"common": "sys=b\n",
		"second": "database=\n\tfile=/lib/ndb/never\n\nsys=c\n",
		"late":   "database=\n\tfile=/lib/ndb/second\n\tfile=/lib/ndb/local\n\nsys=d\n",
		"never":  "sys=never\n",
	})
	names := func(db *DB) (s []string) {
		for _, e := range db.Entries {
			if v, ok := e.Find("sys"); ok {
				s = append(s, v)
			}
		}
		return
	}
	db, err := Open(filepath.Join(dir, "p9.lab"))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(db); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("p9.lab: %v, want a b: /lib/ndb/local is the lab itself", got)
	}
	if want := []string{filepath.Join(dir, "p9.lab"), filepath.Join(dir, "common")}; !reflect.DeepEqual(db.Files, want) {
		t.Errorf("files %v, want %v", db.Files, want)
	}
	// /lib/ndb/local named after another file moves the lab to the end;
	// only the first file's database= entry counts.
	db, err = Open(filepath.Join(dir, "late"))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(db); !reflect.DeepEqual(got, []string{"c", "d"}) {
		t.Errorf("late: %v, want c d", got)
	}
	if _, err := Open(filepath.Join(dir, "nonexistent")); err == nil {
		t.Error("Open of a missing file succeeded")
	}
}

func TestSearch(t *testing.T) {
	es, _ := Parse("f", []byte("sys=a ip=1\n\tsys=alias\nsys=b dom=x\nsys=a ip=2\n"))
	db := &DB{Entries: es}
	ms := db.Search("sys", "a")
	if len(ms) != 2 || ms[0].Entry != es[0] || ms[1].Entry != es[2] {
		t.Fatalf("Search(sys, a) = %+v", ms)
	}
	ms = db.Search("sys", "alias")
	if len(ms) != 1 || ms[0].Line != 1 || ms[0].Index != 0 {
		t.Errorf("Search(sys, alias) = %+v", ms)
	}
	if ms := db.Search("sys", "nope"); len(ms) != 0 {
		t.Errorf("Search(sys, nope) = %+v", ms)
	}
}

func TestGetValue(t *testing.T) {
	es, _ := Parse("f", []byte(
		"sys=a port=1\n\ttcp=x port=2\n"+
			"tcp=y\n"+
			"tcp=y port=3\n"))
	db := &DB{Entries: es}
	for _, c := range []struct {
		attr, val, rattr, want string
		ok                     bool
	}{
		{"tcp", "x", "port", "2", true}, // on the matching line first
		{"sys", "a", "port", "1", true},
		{"tcp", "y", "port", "3", true}, // the first entry that has one
		{"tcp", "z", "port", "", false},
		{"sys", "a", "nope", "", false},
	} {
		v, ok := db.GetValue(c.attr, c.val, c.rattr)
		if v != c.want || ok != c.ok {
			t.Errorf("GetValue(%s, %s, %s) = %q, %v; want %q, %v", c.attr, c.val, c.rattr, v, ok, c.want, c.ok)
		}
	}
}

func TestIPInfoSources(t *testing.T) {
	es, _ := Parse("f", []byte(
		"ipnet=lab ip=10.99.0.0 ipmask=255.255.255.0\n\tini.service=cpu fs=10.99.0.10\n"+
			"sys=amd ip=10.99.0.11\n\tini.nvram=x\n"))
	db := &DB{Entries: es}
	ts := db.IPInfo("sys", "amd", "ini.service", "ini.nvram", "ipmask", "sys")
	want := []string{"sys=amd sys=amd", "ini.nvram=x sys=amd", "ipmask=255.255.255.0 ipnet=lab", "ini.service=cpu ipnet=lab"}
	var got []string
	for _, x := range ts {
		got = append(got, x.Attr+"="+x.Val+" "+x.Entry.String())
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IPInfo = %q, want %q", got, want)
	}
	// a made-up class mask comes from no entry
	ts = db.IPInfo("ip", "172.16.0.1", "ipmask")
	if len(ts) != 1 || ts[0].Val != "/112" || ts[0].Entry != nil {
		t.Errorf("IPInfo(ip 172.16.0.1, ipmask) = %+v", ts)
	}
}

func TestAttrs(t *testing.T) {
	es, _ := Parse("f", []byte("database=\n\tfile=x\n\nsys=a ip=1\n\tz y\n"))
	db := &DB{Entries: es}
	if got := db.Attrs(); !reflect.DeepEqual(got, []string{"ip", "sys", "y", "z"}) {
		t.Errorf("Attrs() = %q", got)
	}
}

func TestFormat(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain", "plain"},
		{"", ""},
		{"a b", `"a b"`},
		{"x#y", `"x#y"`},
		{"tab\there", "\"tab\there\""},
	} {
		if got := FormatVal(c.in); got != c.want {
			t.Errorf("FormatVal(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	ts := []Tuple{{Pair: Pair{"a", "1"}}, {Pair: Pair{"q", "x y"}}, {Pair: Pair{"flag", ""}}}
	if got := Format(ts); got != `a=1 q="x y" flag= ` {
		t.Errorf("Format = %q", got)
	}
}
