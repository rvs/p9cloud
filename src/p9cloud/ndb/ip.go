package ndb

import "fmt"

// IP is an address as 9front's libip keeps it: 16 bytes, with IPv4
// addresses in the v4-in-v6 form.
type IP [16]byte

var (
	v4prefix = IP{10: 0xff, 11: 0xff}
	allbits  = IP{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
)

// IsV4 reports whether ip is an IPv4 address.
func (ip IP) IsV4() bool { return [12]byte(ip[:12]) == [12]byte(v4prefix[:12]) }

// at is s[i] or, past its end, the NUL that ends a C string.
func at(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func isdigit(c byte) bool { return '0' <= c && c <= '9' }
func isxdigit(c byte) bool {
	return isdigit(c) || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
func isalpha(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

// strtoul is Plan 9's, where an unsigned long has 32 bits.  It parses s
// from i and returns the value and where it stopped; i if there are no
// digits.
func strtoul(s string, i, base int) (uint32, int) {
	p := i
	for c := at(s, p); c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == '\v'; c = at(s, p) {
		p++
	}
	neg := false
	if c := at(s, p); c == '-' || c == '+' {
		neg = c == '-'
		p++
	}
	if base == 0 {
		base = 10
		if at(s, p) == '0' {
			base = 8
			if c := at(s, p+1); c == 'x' || c == 'X' {
				base = 16
			}
		}
	}
	if base == 16 && at(s, p) == '0' {
		if c := at(s, p+1); (c == 'x' || c == 'X') && isxdigit(at(s, p+2)) {
			p += 2
		}
	}
	var n uint32
	ovfl, ndig := false, 0
	m := ^uint32(0) / uint32(base)
	for ; ; p, ndig = p+1, ndig+1 {
		c, v := at(s, p), base
		switch {
		case isdigit(c):
			v = int(c - '0')
		case 'a' <= c && c <= 'z':
			v = int(c-'a') + 10
		case 'A' <= c && c <= 'Z':
			v = int(c-'A') + 10
		}
		if v >= base {
			break
		}
		if n > m {
			ovfl = true
		}
		nn := n*uint32(base) + uint32(v)
		if nn < n {
			ovfl = true
		}
		n = nn
	}
	if ndig == 0 {
		p = i
	}
	switch {
	case ovfl:
		return ^uint32(0), p
	case neg:
		return -n, p
	}
	return n, p
}

// atoi is Plan 9's: strtol in base 10.
func atoi(s string) int {
	i := 0
	for c := at(s, i); c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == '\v'; c = at(s, i) {
		i++
	}
	neg := false
	if c := at(s, i); c == '-' || c == '+' {
		neg = c == '-'
		i++
	}
	n := 0
	for ; isdigit(at(s, i)); i++ {
		if n = n*10 + int(at(s, i)-'0'); n > 1<<31 {
			n = 1 << 31
		}
	}
	if neg {
		return -n
	}
	return min(n, 1<<31-1)
}

// v4parseip parses an IPv4 address, perhaps classful shorthand such as
// 10.1.2 for 10.0.1.2, from s[p:] into to.
func v4parseip(to []byte, s string, p int) int {
	i := 0
	for ; i < 4 && at(s, p) != 0; i++ {
		var x uint32
		x, p = strtoul(s, p, 0)
		to[i] = byte(x)
		if at(s, p) == '.' {
			p++
		}
	}
	switch to[0] >> 6 {
	case 0, 1: // class A: one byte of network
		if i == 3 {
			to[3], to[2], to[1] = to[2], to[1], 0
		} else if i == 2 {
			to[3], to[1] = to[1], 0
		}
	case 2: // class B: two bytes of network
		if i == 3 {
			to[3], to[2] = to[2], 0
		}
	}
	return p
}

func ipcharok(c byte) bool { return c == '.' || c == ':' || isxdigit(c) }

func delimchar(c byte) bool {
	return c == 0 || !(c == '.' || c == ':' || isdigit(c) || isalpha(c))
}

// ParseIP parses an address as libip's parseip does.  Like parseip it
// allows other characters after the address, after a delimiter.
func ParseIP(s string) (IP, bool) {
	var to IP
	ellipsis, v4 := 0, true
	p, i := 0, 0
	for ; i < len(to) && ipcharok(at(s, p)); i += 2 {
		op := p
		var x uint32
		x, p = strtoul(s, p, 16)
		if at(s, p) == '.' || at(s, p) == 0 && i == 0 { // ends with v4
			if i > len(to)-4 {
				return IP{}, false
			}
			p = v4parseip(to[i:i+4], s, op)
			i += 4
			break
		}
		// v6: at most 4 hex digits, then a colon or a delimiter
		if x != uint32(uint16(x)) || at(s, p) != ':' && !delimchar(at(s, p)) {
			return IP{}, false
		}
		to[i], to[i+1] = byte(x>>8), byte(x)
		if at(s, p) == ':' {
			v4 = false
			if p++; at(s, p) == ':' { // :: is elided zeros
				if ellipsis != 0 {
					return IP{}, false
				}
				ellipsis = i + 2
				p++
			}
		} else if p == op {
			break
		}
	}
	if p == 0 || !delimchar(at(s, p)) {
		return IP{}, false
	}
	if i < len(to) {
		copy(to[ellipsis+len(to)-i:], to[ellipsis:i])
		clear(to[ellipsis : ellipsis+len(to)-i])
	}
	if v4 {
		to[10], to[11] = 0xff, 0xff
	}
	return to, true
}

// ParseIPMask parses a mask as libip's parseipmask does: /N is N prefix
// bits (N+96 for a v4 mask of at most 32), anything else an address.
func ParseIPMask(s string, v4 bool) (IP, bool) {
	var to IP
	if at(s, 0) == '/' {
		i := atoi(s[1:])
		if i < 0 {
			i = 0
		}
		if i <= 32 && v4 {
			i += 96
		}
		i = min(i, 128)
		w, k := i, 0
		for ; i >= 8; i -= 8 {
			to[k] = 0xff
			k++
		}
		if i > 0 {
			to[k] = ^byte(1<<(8-i) - 1)
		}
		if w < 96 {
			return to, !v4
		}
		return to, true
	}
	to, ok := ParseIP(s)
	if [12]byte(to[:12]) == [12]byte(v4prefix[:12]) {
		copy(to[:12], allbits[:12])
	} else if v4 && [12]byte(to[:12]) != [12]byte(allbits[:12]) {
		ok = false
	}
	return to, ok
}

// String formats ip as %I does.
func (ip IP) String() string {
	if ip.IsV4() {
		return fmt.Sprintf("%d.%d.%d.%d", ip[12], ip[13], ip[14], ip[15])
	}
	// the longest run of zero shorts, the first of equal ones
	eln, eli := -1, -1
	for i := 0; i < 16; i += 2 {
		j := i
		for ; j < 16 && ip[j] == 0 && ip[j+1] == 0; j += 2 {
		}
		if j > i && j-i > eln {
			eli, eln = i, j-i
		}
	}
	s := ""
	for i := 0; i < 16; i += 2 {
		if i == eli {
			s += "::"
			if i += eln; i >= 16 {
				break
			}
		} else if i != 0 {
			s += ":"
		}
		s += fmt.Sprintf("%x", uint16(ip[i])<<8|uint16(ip[i+1]))
	}
	return s
}

// MaskString formats a mask as %M does: /N if it is a prefix.
func (m IP) MaskString() string {
	i := 0
	for i < 16 && m[i] == 0xff {
		i++
	}
	n := 8 * i
	if i < 16 {
		b := 0
		for b < 8 && m[i]<<b&0x80 != 0 {
			b++
		}
		if m[i]<<b != 0 {
			return m.String()
		}
		for j := i + 1; j < 16; j++ {
			if m[j] != 0 {
				return m.String()
			}
		}
		n += b
	}
	return fmt.Sprintf("/%d", n)
}

var classmask = [4]IP{
	{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
}

var v6masks = []struct {
	prefix IP
	n      int
	mask   IP
}{
	{IP{15: 1}, 16, allbits}, // loopback
	{IP{0xfe, 0x80}, 8, IP{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},                                                    // link-local
	{IP{0xff, 0x02, 11: 0x01, 12: 0xff}, 13, IP{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}, // solicited-node
	{IP{0xff}, 1, IP{0xff}}, // multicast
}

// DefMask is libip's defmask: an IPv4 address's class mask; for IPv6 all
// bits but for link-local, solicited-node and multicast addresses.
func DefMask(ip IP) IP {
	if ip.IsV4() {
		return classmask[ip[12]>>6]
	}
	for _, m := range v6masks {
		if [16]byte(ip) == [16]byte(m.prefix) || m.n < 16 && string(ip[:m.n]) == string(m.prefix[:m.n]) {
			return m.mask
		}
	}
	return allbits
}

// PrefixLen is libndb's prefixlen: the position of the mask's last one
// bit.
func PrefixLen(m IP) int {
	for y := 15; y >= 0; y-- {
		for i := 8; i > 0; i-- {
			if m[y]&(1<<(8-i)) != 0 {
				return y*8 + i
			}
		}
	}
	return 0
}

// IPAttr is libndb's ipattr: the attribute a name is probably a value
// of: ip, dom or sys.
func IPAttr(s string) string {
	dot, alpha, colon, hex := false, false, false, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case isdigit(c):
		case isxdigit(c):
			hex = true
		case isalpha(c) || c == '-':
			alpha = true
		case c == '.':
			dot = true
		case c == ':':
			colon = true
		default:
			return "sys"
		}
	}
	switch {
	case alpha && dot:
		return "dom"
	case alpha:
		return "sys"
	case colon, dot && !hex:
		return "ip"
	}
	return "sys"
}
