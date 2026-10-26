// Package ndb reads Plan 9 network databases, ndb(6), exactly as 9front's
// libndb does: the same syntax, the database= list of files, and the
// lookups of ndbsearch, ndbgetvalue and ndbipinfo, where a system
// inherits each attribute it lacks from the networks containing its
// address.  testdata/ipquery.out holds 9front's answers it is tested
// against.
package ndb

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// nattr is libndb's Ndbalen: attribute names are cut to nattr-1 bytes.
const nattr = 32

// Pair is one attr=value tuple; a valueless attribute has Val "".
type Pair struct{ Attr, Val string }

// Entry is one database entry, as lines of pairs.
type Entry struct {
	File  string // the file it is in
	Line  int    // the number of its first line
	Lines [][]Pair
}

// Pairs returns all the entry's pairs, in order.
func (e *Entry) Pairs() []Pair {
	var ps []Pair
	for _, l := range e.Lines {
		ps = append(ps, l...)
	}
	return ps
}

// Find returns the value of the entry's first attr.
func (e *Entry) Find(attr string) (string, bool) {
	for _, l := range e.Lines {
		for _, p := range l {
			if p.Attr == attr {
				return p.Val, true
			}
		}
	}
	return "", false
}

// String names the entry by its first pair, as in ipnet=p9lab.
func (e *Entry) String() string {
	p := e.Lines[0][0]
	if p.Val == "" {
		return p.Attr
	}
	return p.Attr + "=" + p.Val
}

func iswhite(c byte) bool { return c == ' ' || c == '\t' || c == '\r' }

// ParseLine returns the pairs on one line, as _ndbparseline: a value is
// quoted with " to hold white space or #, and # starts a comment.
func ParseLine(s string) []Pair {
	at := func(i int) byte {
		if i < len(s) {
			return s[i]
		}
		return '\n'
	}
	var ps []Pair
	for p := 0; at(p) != '#' && at(p) != '\n'; {
		for iswhite(at(p)) {
			p++
		}
		if at(p) == '#' || at(p) == '\n' {
			break
		}
		q := p
		for at(p) != '=' && !iswhite(at(p)) && at(p) != '\n' {
			p++
		}
		pair := Pair{Attr: s[q:p]}
		if len(pair.Attr) >= nattr {
			pair.Attr = pair.Attr[:nattr-1]
		}
		for iswhite(at(p)) {
			p++
		}
		if at(p) == '=' {
			p++
			switch at(p) {
			case '"':
				p++
				for q = p; at(p) != '\n' && at(p) != '"'; p++ {
				}
				pair.Val = s[q:p]
				if at(p) == '"' {
					p++
				}
			case '#':
			default:
				for q = p; !iswhite(at(p)) && at(p) != '\n'; p++ {
				}
				pair.Val = s[q:p]
			}
		}
		ps = append(ps, pair)
	}
	return ps
}

// Parse returns the entries in a file, as ndbparse: an entry starts on a
// line that has pairs and goes on until a line that starts with neither
// white space nor #.  A last line without a newline is ignored, by
// 9front too, and warned about.
func Parse(name string, data []byte) (entries []*Entry, warnings []string) {
	var cur *Entry
	for n, line := range strings.SplitAfter(string(data), "\n") {
		if !strings.HasSuffix(line, "\n") {
			if ParseLine(line) != nil {
				warnings = append(warnings, fmt.Sprintf("%s:%d: no newline at the end: 9front ignores the line", name, n+1))
			}
			break
		}
		if c := line[0]; cur != nil && !iswhite(c) && c != '#' {
			entries = append(entries, cur)
			cur = nil
		}
		ps := ParseLine(line)
		if ps == nil {
			continue
		}
		if cur == nil {
			cur = &Entry{File: name, Line: n + 1}
		}
		cur.Lines = append(cur.Lines, ps)
	}
	if cur != nil {
		entries = append(entries, cur)
	}
	return entries, warnings
}

// DB is a database: a file and the files its database= entry lists.
type DB struct {
	Path     string   // the first file
	Files    []string // the files, in search order
	Entries  []*Entry // their entries, in search order
	Warnings []string
}

// Open opens a database as ndbopen does.  The file is the database's
// /lib/ndb/local: in its database= entry, file=/lib/ndb/local is the file
// itself, file=/lib/ndb/NAME is NAME next to it, and files that do not
// exist, such as /net/ndb, are skipped.
func Open(path string) (*DB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	first, warns := Parse(path, data)
	type file struct {
		path    string
		entries []*Entry
	}
	files := []file{{path, first}}
	for _, e := range first {
		if !e.has(Pair{"database", ""}) {
			continue
		}
		for _, p := range e.Pairs() {
			if p.Attr != "file" {
				continue
			}
			f := p.Val
			if name, ok := strings.CutPrefix(f, "/lib/ndb/"); ok && name != "local" {
				f = filepath.Join(filepath.Dir(path), name)
			}
			if p.Val == "/lib/ndb/local" || samefile(f, path) {
				// the first file can be reordered in the list
				if len(files) > 1 && files[0].path == path {
					files = append(files[1:], files[0])
				}
				continue
			}
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			es, w := Parse(f, data)
			files = append(files, file{f, es})
			warns = append(warns, w...)
		}
		break
	}
	db := &DB{Path: path, Warnings: warns}
	for _, f := range files {
		db.Files = append(db.Files, f.path)
		db.Entries = append(db.Entries, f.entries...)
	}
	return db, nil
}

func samefile(a, b string) bool {
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	return err == nil && os.SameFile(x, y)
}

func (e *Entry) has(p Pair) bool {
	for _, l := range e.Lines {
		for _, q := range l {
			if q == p {
				return true
			}
		}
	}
	return false
}

// Match is an entry with attr=val, and where the pair is in it.
type Match struct {
	Entry       *Entry
	Line, Index int
}

// Search returns the entries with attr=val, as ndbsearch and ndbsnext.
func (db *DB) Search(attr, val string) []Match {
	var ms []Match
	for _, e := range db.Entries {
	entry:
		for li, l := range e.Lines {
			for ti, p := range l {
				if p.Attr == attr && p.Val == val {
					ms = append(ms, Match{e, li, ti})
					break entry
				}
			}
		}
	}
	return ms
}

// pairs returns the entry's pairs with the matching line first, starting
// at the matching pair, as ndbreorder leaves them.
func (m Match) pairs() []Tuple {
	l := m.Entry.Lines[m.Line]
	lines := append([][]Pair{append(l[m.Index:len(l):len(l)], l[:m.Index]...)}, m.Entry.Lines[m.Line+1:]...)
	lines = append(lines, m.Entry.Lines[:m.Line]...)
	var ts []Tuple
	for _, l := range lines {
		for _, p := range l {
			ts = append(ts, Tuple{p, m.Entry})
		}
	}
	return ts
}

// GetValue returns rattr of the first entry with attr=val that has one,
// as ndbgetvalue: on the matching line first.
func (db *DB) GetValue(attr, val, rattr string) (string, bool) {
	for _, m := range db.Search(attr, val) {
		for _, t := range m.pairs() {
			if t.Attr == rattr {
				return t.Val, true
			}
		}
	}
	return "", false
}

// Tuple is a pair found by IPInfo and the entry it is in, nil if libndb
// made it up (a class mask).
type Tuple struct {
	Pair
	Entry *Entry
}

type filter struct {
	attr         string
	addr, ignore bool
}

// IPInfo returns the rattrs of the entries with attr=val, as ndbipinfo:
// an attribute an entry lacks comes from the ipnet entries containing
// its ip, the longest prefix first.  An rattr of @attr returns attr's
// values as addresses.  For attr ip, an address without an entry still
// gets its networks' attributes.
func (db *DB) IPInfo(attr, val string, rattrs ...string) []Tuple {
	ms := db.Search(attr, val)
	for len(ms) > 0 {
		if _, ok := ms[0].Entry.Find("ip"); ok {
			break
		}
		ms = ms[1:]
	}
	var ts []Tuple
	if len(ms) == 0 {
		if attr != "ip" {
			return nil
		}
		ts = db.netinfo([]Tuple{{Pair: Pair{"ip", val}}}, rattrs)
	}
	for _, m := range ms {
		ts = append(ts, db.netinfo(m.pairs(), rattrs)...)
	}
	return dedup(ts)
}

func (db *DB) netinfo(t []Tuple, rattrs []string) []Tuple {
	var ip IP
	ok := false
	for _, x := range t {
		if x.Attr == "ip" {
			ip, ok = ParseIP(x.Val)
			break
		}
	}
	if !ok {
		return nil
	}
	f := make([]*filter, len(rattrs))
	for i, a := range rattrs {
		f[i] = &filter{attr: strings.TrimPrefix(a, "@"), addr: strings.HasPrefix(a, "@")}
		if len(f[i].attr) >= nattr {
			f[i].attr = f[i].attr[:nattr-1]
		}
	}
	out := db.filter(t, f)

	// look for a containing network: keep turning off the lowest bit
	// that is on and look for a network there with a shorter mask
	net := ip
	prefix, smallest, force := 127, 100, false
	if !ip.IsV4() {
		// in v6, the last 8 bytes have no structure
		prefix, smallest, force = 64, 2, true
		clear(net[8:])
	}
	for ; prefix >= smallest; prefix-- {
		if complete(f) {
			break
		}
		bit := byte(1 << (7 - prefix%8))
		if !force && net[prefix/8]&bit == 0 {
			continue
		}
		force = false
		net[prefix/8] &^= bit
		out = append(out, db.subnet(net, f, prefix)...)
	}
	for _, x := range f {
		if x.attr == "ipmask" {
			if !x.ignore {
				out = append(out, Tuple{Pair: Pair{"ipmask", DefMask(ip).MaskString()}})
			}
			break
		}
	}
	return out
}

func complete(f []*filter) bool {
	for _, x := range f {
		if !x.ignore {
			return false
		}
	}
	return true
}

// subnet returns the attributes still wanted from the ipnet entries for
// net with prefixes up to prefix, the longest first; the first entry for
// a prefix is the only one.
func (db *DB) subnet(net IP, f []*filter, prefix int) []Tuple {
	var at [129]*Entry
	for _, m := range db.Search("ip", net.String()) {
		e := m.Entry
		if _, ok := e.Find("ipnet"); !ok {
			continue
		}
		v, ok := e.Find("ipmask")
		var mask IP
		if ok {
			mask, ok = ParseIPMask(v, net.IsV4())
		}
		if !ok {
			mask = DefMask(net)
		}
		if n := PrefixLen(mask); n <= prefix && at[n] == nil {
			at[n] = e
		}
	}
	var out []Tuple
	for ; prefix >= 0; prefix-- {
		if e := at[prefix]; e != nil {
			var t []Tuple
			for _, p := range e.Pairs() {
				t = append(t, Tuple{p, e})
			}
			out = append(out, db.filter(t, f)...)
		}
	}
	return out
}

// filter keeps the wanted tuples, and stops wanting the attributes it
// found.
func (db *DB) filter(t []Tuple, f []*filter) []Tuple {
	var out []Tuple
	found := make([]bool, len(f))
	for _, x := range t {
		k := -1
		for i, w := range f {
			if !w.ignore && w.attr == x.Attr {
				k = i
				break
			}
		}
		if k < 0 {
			continue
		}
		found[k] = true
		if !f[k].addr {
			out = append(out, x)
			continue
		}
		for _, a := range db.ipaddrs(x.Val) {
			out = append(out, Tuple{Pair{x.Attr, a}, x.Entry})
		}
	}
	for i, ok := range found {
		if ok {
			f[i].ignore = true
		}
	}
	return out
}

// ipaddrs returns a name's addresses, as ndbgetipaddr.
func (db *DB) ipaddrs(name string) []string {
	attr := IPAttr(name)
	if attr == "ip" {
		return []string{name}
	}
	var ips []Tuple
	ms := db.Search(attr, name)
	for len(ms) > 0 {
		if _, ok := ms[0].Entry.Find("ip"); ok {
			break
		}
		ms = ms[1:]
	}
	for _, m := range ms {
		for _, p := range m.Entry.Pairs() {
			if p.Attr == "ip" {
				ips = append(ips, Tuple{Pair: p})
			}
		}
	}
	var s []string
	for _, t := range dedup(ips) {
		s = append(s, t.Val)
	}
	return s
}

// dedup drops repeated pairs, as ndbdedup.
func dedup(ts []Tuple) []Tuple {
	seen := map[Pair]bool{}
	var out []Tuple
	for _, t := range ts {
		if !seen[t.Pair] {
			seen[t.Pair] = true
			out = append(out, t)
		}
	}
	return out
}

// Attrs returns the attributes in the database but database and file,
// sorted.
func (db *DB) Attrs() []string {
	seen := map[string]bool{"database": true, "file": true}
	var as []string
	for _, e := range db.Entries {
		for _, p := range e.Pairs() {
			if !seen[p.Attr] {
				seen[p.Attr] = true
				as = append(as, p.Attr)
			}
		}
	}
	sort.Strings(as)
	return as
}

// FormatVal formats a value as %$ does: quoted if it holds white space
// or #.
func FormatVal(v string) string {
	if strings.ContainsAny(v, " \t\r#") {
		return `"` + v + `"`
	}
	return v
}

// Format formats tuples as ndb/ipquery prints them, without the newline.
func Format(ts []Tuple) string {
	var b strings.Builder
	for _, t := range ts {
		b.WriteString(t.Attr + "=" + FormatVal(t.Val) + " ")
	}
	return b.String()
}
