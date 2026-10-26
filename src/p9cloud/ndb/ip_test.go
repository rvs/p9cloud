package ndb

import "testing"

func v4(a, b, c, d byte) IP { return IP{10: 0xff, 11: 0xff, 12: a, 13: b, 14: c, 15: d} }

func TestParseIP(t *testing.T) {
	for _, c := range []struct {
		in   string
		want IP
		ok   bool
	}{
		{"10.99.0.1", v4(10, 99, 0, 1), true},
		{"255.255.255.0", v4(255, 255, 255, 0), true},
		{"10.1.2.3 and more", v4(10, 1, 2, 3), true}, // a delimiter may follow
		{"10.1.2", v4(10, 0, 1, 2), true},            // class A shorthand
		{"10.99", v4(10, 0, 0, 99), true},
		{"135.104.9", v4(135, 104, 0, 9), true}, // class B shorthand
		{"192.168.1", v4(192, 168, 1, 0), true}, // class C: as written
		{"0x0a.1.2.0x11", v4(10, 1, 2, 17), true},
		{"010.1.2.3", v4(8, 1, 2, 3), true}, // octal, as strtoul base 0
		{"10", v4(10, 0, 0, 0), true},
		{"::ffff:10.1.2.60", v4(10, 1, 2, 60), true},
		{"::1", IP{15: 1}, true},
		{"::", IP{}, true},
		{"fe80::1", IP{0: 0xfe, 1: 0x80, 15: 1}, true},
		{"2001:db8:1:2::5", IP{0x20, 0x01, 0x0d, 0xb8, 0, 1, 0, 2, 15: 5}, true},
		{"1:2:3:4:5:6:7:8", IP{0, 1, 0, 2, 0, 3, 0, 4, 0, 5, 0, 6, 0, 7, 0, 8}, true},
		{"1::2:3.4.5.6", IP{0, 1, 10: 0, 11: 2, 12: 3, 13: 4, 14: 5, 15: 6}, true},
		{"", IP{}, false},
		{"foo", IP{}, false},
		{"delete", IP{}, false}, // not de::
		{"1::2::3", IP{}, false},
		{"12345::", IP{}, false},
		{"10.1.2.x", IP{}, false},
		{"1:2:3:4:5:6:7:1.2.3.4", IP{}, false},
	} {
		got, ok := ParseIP(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseIP(%q) = %v, %v; want %v, %v", c.in, got[:], ok, c.want[:], c.ok)
		}
	}
}

func TestIPString(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"10.99.0.1", "10.99.0.1"},
		{"::ffff:1.2.3.4", "1.2.3.4"},
		{"::", "::"},
		{"::1", "::1"},
		{"1::", "1::"},
		{"2001:db8::", "2001:db8::"},
		{"2001:0db8:0:0:1:0:0:0", "2001:db8:0:0:1::"}, // the longer run
		{"1:0:0:2:0:0:3:4", "1::2:0:0:3:4"},           // the first of equal runs
		{"1:0:2:3:4:5:6:7", "1::2:3:4:5:6:7"},         // even a single zero
		{"1:2:3:4:5:6:7:8", "1:2:3:4:5:6:7:8"},
		{"fe80::ABCD", "fe80::abcd"},
	} {
		ip, ok := ParseIP(c.in)
		if !ok {
			t.Fatalf("ParseIP(%q) failed", c.in)
		}
		if got := ip.String(); got != c.want {
			t.Errorf("%q: String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseIPMask(t *testing.T) {
	for _, c := range []struct {
		in   string
		v4   bool
		want string // as %M
		ok   bool
	}{
		{"255.255.255.0", true, "/120", true},
		{"255.255.255.0", false, "/120", true},
		{"255.255.255.240", true, "/124", true},
		{"/24", true, "/120", true},
		{"/24", false, "/24", true},
		{"/120", true, "/120", true},
		{"/100", true, "/100", true},
		{"/0", true, "/96", true},
		{"/-5", true, "/96", true},
		{"/junk", true, "/96", true}, // atoi
		{"/200", false, "/128", true},
		{"/64", false, "/64", true},
		{"/90", true, "", false}, // fewer bits than a v4 mask has
		{"ffff:ffff:ffff:ffff::", false, "/64", true},
		{"ffff:ffff:ffff:ffff::", true, "", false},
		{"255.0.255.0", true, "ffff:ffff:ffff:ffff:ffff:ffff:ff00:ff00", true},
		{"junk", true, "", false},
		{"junk", false, "", false},
	} {
		m, ok := ParseIPMask(c.in, c.v4)
		if ok != c.ok || (ok && m.MaskString() != c.want) {
			t.Errorf("ParseIPMask(%q, %v) = %s, %v; want %s, %v", c.in, c.v4, m.MaskString(), ok, c.want, c.ok)
		}
	}
}

func TestMaskString(t *testing.T) {
	all := IP{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	if got := all.MaskString(); got != "/128" {
		t.Errorf("all bits: %q", got)
	}
	if got := (IP{}).MaskString(); got != "/0" {
		t.Errorf("no bits: %q", got)
	}
	if got := (IP{0: 0xff, 1: 0xfe}).MaskString(); got != "/15" {
		t.Errorf("/15: %q", got)
	}
	if got := (IP{0: 0xff, 1: 0x01}).MaskString(); got != "ff01::" {
		t.Errorf("not a prefix: %q", got)
	}
}

func TestDefMask(t *testing.T) {
	for _, c := range []struct{ ip, want string }{
		{"10.1.2.3", "/104"},
		{"127.0.0.1", "/104"},
		{"135.104.9.6", "/112"},
		{"192.168.7.9", "/120"},
		{"224.0.0.1", "/120"},
		{"2001:db8::1", "/128"},
		{"::1", "/128"},
		{"fe80::1", "/64"},
		{"ff02::1:ff00:1", "/104"},
		{"ff05::1", "/8"},
	} {
		ip, _ := ParseIP(c.ip)
		if got := DefMask(ip).MaskString(); got != c.want {
			t.Errorf("DefMask(%s) = %s, want %s", c.ip, got, c.want)
		}
	}
}

func TestPrefixLen(t *testing.T) {
	for _, c := range []struct {
		mask string
		v4   bool
		want int
	}{
		{"255.255.255.0", true, 120},
		{"/0", false, 0},
		{"/128", false, 128},
		{"255.0.255.0", true, 120}, // the last one bit, as libndb counts
	} {
		m, _ := ParseIPMask(c.mask, c.v4)
		if got := PrefixLen(m); got != c.want {
			t.Errorf("PrefixLen(%s) = %d, want %d", c.mask, got, c.want)
		}
	}
}

func TestIPAttr(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"10.1.2.3", "ip"},
		{"1.2.3", "ip"},
		{"::1", "ip"},
		{"fe80::1", "ip"},
		{"fs", "sys"},
		{"cafe", "sys"},
		{"dead.beef", "sys"},
		{"fs.p9lab", "dom"},
		{"a-b", "sys"},
		{"a_b.c", "sys"},
		{"123", "sys"},
	} {
		if got := IPAttr(c.in); got != c.want {
			t.Errorf("IPAttr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
