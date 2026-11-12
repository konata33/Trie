package web

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// URL is a URL parsed as `new URL` parses it in Node (the WHATWG URL
// standard). The store resolves image addresses and matches article links
// with it; net/url differs on hosts, default ports, percent-encoding,
// backslashes and whitespace, and would match or keep different articles.
type URL struct {
	Scheme, Username, Password string
	host                       *string // serialized; nil when null
	port                       int     // -1 when null
	path                       []string
	opaque                     bool // path is the single string opath
	opath                      string
	query, fragment            *string
}

var errURL = errors.New("Invalid URL")

var defaultPorts = map[string]int{"ftp": 21, "file": -1, "http": 80, "https": 443, "ws": 80, "wss": 443}

func (u *URL) special() bool { _, ok := defaultPorts[u.Scheme]; return ok }

func (u *URL) Hostname() string {
	if u.host == nil {
		return ""
	}
	return *u.host
}

func (u *URL) Pathname() string {
	if u.opaque {
		return u.opath
	}
	var b strings.Builder
	for _, s := range u.path {
		b.WriteByte('/')
		b.WriteString(s)
	}
	return b.String()
}

func (u *URL) Href() string {
	var b strings.Builder
	b.WriteString(u.Scheme + ":")
	if u.host != nil {
		b.WriteString("//")
		if u.Username != "" || u.Password != "" {
			b.WriteString(u.Username)
			if u.Password != "" {
				b.WriteString(":" + u.Password)
			}
			b.WriteByte('@')
		}
		b.WriteString(*u.host)
		if u.port >= 0 {
			b.WriteString(":" + strconv.Itoa(u.port))
		}
	} else if !u.opaque && len(u.path) > 1 && u.path[0] == "" {
		b.WriteString("/.")
	}
	b.WriteString(u.Pathname())
	if u.query != nil {
		b.WriteString("?" + *u.query)
	}
	if u.fragment != nil {
		b.WriteString("#" + *u.fragment)
	}
	return b.String()
}

func (u *URL) shorten() {
	if u.Scheme == "file" && len(u.path) == 1 && normalizedDrive(u.path[0]) {
		return
	}
	if len(u.path) > 0 {
		u.path = u.path[:len(u.path)-1]
	}
}

func ptr(s string) *string { return &s }

const (
	sSchemeStart = iota
	sScheme
	sNoScheme
	sSpecialRelativeOrAuthority
	sPathOrAuthority
	sRelative
	sRelativeSlash
	sSpecialAuthoritySlashes
	sSpecialAuthorityIgnoreSlashes
	sAuthority
	sHost
	sPort
	sFile
	sFileSlash
	sFileHost
	sPathStart
	sPath
	sOpaquePath
	sQuery
	sFragment
)

// ParseURL is the basic URL parser, without state overrides; base may be nil.
func ParseURL(input string, base *URL) (*URL, error) {
	input = strings.TrimFunc(input, func(r rune) bool { return r <= ' ' })
	in := []rune(strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, input))
	u := &URL{port: -1}
	var buf []rune
	atSign, brackets, password := false, false, false
	state := sSchemeStart
	at := func(i int) rune {
		if i >= 0 && i < len(in) {
			return in[i]
		}
		return -1
	}
	copyBase := func(withPath bool) {
		u.Username, u.Password, u.host, u.port = base.Username, base.Password, base.host, base.port
		if withPath {
			u.path = slices.Clone(base.path)
			if base.query != nil {
				u.query = ptr(*base.query)
			}
		}
	}
	for p := 0; ; p++ {
		c := at(p)
		special := u.special()
		ends := c == -1 || c == '/' || c == '?' || c == '#' || special && c == '\\'
		switch state {
		case sSchemeStart:
			if IsAlpha(c) {
				buf = append(buf, LowerASCII(c))
				state = sScheme
			} else {
				state, p = sNoScheme, p-1
			}
		case sScheme:
			switch {
			case IsAlpha(c) || IsDigit(c) || c == '+' || c == '-' || c == '.':
				buf = append(buf, LowerASCII(c))
			case c == ':':
				u.Scheme, buf = string(buf), buf[:0]
				switch {
				case u.Scheme == "file":
					state = sFile
				case u.special() && base != nil && base.Scheme == u.Scheme:
					state = sSpecialRelativeOrAuthority
				case u.special():
					state = sSpecialAuthoritySlashes
				case at(p+1) == '/':
					state, p = sPathOrAuthority, p+1
				default:
					u.opaque, state = true, sOpaquePath
				}
			default:
				buf, state, p = buf[:0], sNoScheme, -1
			}
		case sNoScheme:
			switch {
			case base == nil || base.opaque && c != '#':
				return nil, errURL
			case base.opaque:
				u.Scheme, u.opaque, u.opath, u.fragment = base.Scheme, true, base.opath, ptr("")
				if base.query != nil {
					u.query = ptr(*base.query)
				}
				state = sFragment
			case base.Scheme != "file":
				state, p = sRelative, p-1
			default:
				state, p = sFile, p-1
			}
		case sSpecialRelativeOrAuthority:
			if c == '/' && at(p+1) == '/' {
				state, p = sSpecialAuthorityIgnoreSlashes, p+1
			} else {
				state, p = sRelative, p-1
			}
		case sPathOrAuthority:
			if c == '/' {
				state = sAuthority
			} else {
				state, p = sPath, p-1
			}
		case sRelative:
			u.Scheme = base.Scheme
			if c == '/' || u.special() && c == '\\' {
				state = sRelativeSlash
				break
			}
			copyBase(true)
			switch {
			case c == '?':
				u.query, state = ptr(""), sQuery
			case c == '#':
				u.fragment, state = ptr(""), sFragment
			case c != -1:
				u.query = nil
				u.shorten()
				state, p = sPath, p-1
			}
		case sRelativeSlash:
			switch {
			case special && (c == '/' || c == '\\'):
				state = sSpecialAuthorityIgnoreSlashes
			case c == '/':
				state = sAuthority
			default:
				copyBase(false)
				state, p = sPath, p-1
			}
		case sSpecialAuthoritySlashes:
			if c == '/' && at(p+1) == '/' {
				state, p = sSpecialAuthorityIgnoreSlashes, p+1
			} else {
				state, p = sSpecialAuthorityIgnoreSlashes, p-1
			}
		case sSpecialAuthorityIgnoreSlashes:
			if c != '/' && c != '\\' {
				state, p = sAuthority, p-1
			}
		case sAuthority:
			switch {
			case c == '@':
				if atSign {
					buf = append([]rune("%40"), buf...)
				}
				atSign = true
				for _, r := range buf {
					if r == ':' && !password {
						password = true
						continue
					}
					if password {
						u.Password += percentEncode(r, userinfoSet)
					} else {
						u.Username += percentEncode(r, userinfoSet)
					}
				}
				buf = buf[:0]
			case ends:
				if atSign && len(buf) == 0 {
					return nil, errURL
				}
				p -= len(buf) + 1
				buf, state = buf[:0], sHost
			default:
				buf = append(buf, c)
			}
		case sHost:
			switch {
			case c == ':' && !brackets:
				if len(buf) == 0 {
					return nil, errURL
				}
				h, err := parseHost(string(buf), !special)
				if err != nil {
					return nil, err
				}
				u.host, buf, state = &h, buf[:0], sPort
			case ends:
				p--
				if special && len(buf) == 0 {
					return nil, errURL
				}
				h, err := parseHost(string(buf), !special)
				if err != nil {
					return nil, err
				}
				u.host, buf, state = &h, buf[:0], sPathStart
			default:
				if c == '[' {
					brackets = true
				} else if c == ']' {
					brackets = false
				}
				buf = append(buf, c)
			}
		case sPort:
			switch {
			case IsDigit(c):
				buf = append(buf, c)
			case ends:
				if len(buf) > 0 {
					port := 0
					for _, d := range buf {
						if port = port*10 + int(d-'0'); port > 65535 {
							return nil, errURL
						}
					}
					if defaultPorts[u.Scheme] == port && special {
						port = -1
					}
					u.port, buf = port, buf[:0]
				}
				state, p = sPathStart, p-1
			default:
				return nil, errURL
			}
		case sFile:
			u.Scheme, u.host = "file", ptr("")
			switch {
			case c == '/' || c == '\\':
				state = sFileSlash
			case base != nil && base.Scheme == "file":
				u.host, u.path = base.host, slices.Clone(base.path)
				if base.query != nil {
					u.query = ptr(*base.query)
				}
				switch {
				case c == '?':
					u.query, state = ptr(""), sQuery
				case c == '#':
					u.fragment, state = ptr(""), sFragment
				case c != -1:
					u.query = nil
					if !startsWithDrive(in[p:]) {
						u.shorten()
					} else {
						u.path = nil
					}
					state, p = sPath, p-1
				}
			default:
				state, p = sPath, p-1
			}
		case sFileSlash:
			if c == '/' || c == '\\' {
				state = sFileHost
				break
			}
			if base != nil && base.Scheme == "file" {
				u.host = base.host
				if !startsWithDrive(in[p:]) && len(base.path) > 0 && normalizedDrive(base.path[0]) {
					u.path = append(u.path, base.path[0])
				}
			}
			state, p = sPath, p-1
		case sFileHost:
			if c == -1 || c == '/' || c == '\\' || c == '?' || c == '#' {
				p--
				switch {
				case isDrive(buf):
					state = sPath // the buffer becomes the first path segment
				case len(buf) == 0:
					u.host, state = ptr(""), sPathStart
				default:
					h, err := parseHost(string(buf), false)
					if err != nil {
						return nil, err
					}
					if h == "localhost" {
						h = ""
					}
					u.host, buf, state = &h, buf[:0], sPathStart
				}
			} else {
				buf = append(buf, c)
			}
		case sPathStart:
			switch {
			case special:
				state = sPath
				if c != '/' && c != '\\' {
					p--
				}
			case c == '?':
				u.query, state = ptr(""), sQuery
			case c == '#':
				u.fragment, state = ptr(""), sFragment
			case c != -1:
				state = sPath
				if c != '/' {
					p--
				}
			}
		case sPath:
			if !ends {
				buf = append(buf, []rune(percentEncode(c, pathSet))...)
				break
			}
			slash := c == '/' || special && c == '\\'
			seg := string(buf)
			switch {
			case doubleDot(seg):
				u.shorten()
				if !slash {
					u.path = append(u.path, "")
				}
			case singleDot(seg):
				if !slash {
					u.path = append(u.path, "")
				}
			default:
				if u.Scheme == "file" && len(u.path) == 0 && isDrive(buf) {
					seg = string(buf[0]) + ":"
				}
				u.path = append(u.path, seg)
			}
			buf = buf[:0]
			if c == '?' {
				u.query, state = ptr(""), sQuery
			} else if c == '#' {
				u.fragment, state = ptr(""), sFragment
			}
		case sOpaquePath:
			switch {
			case c == '?':
				u.query, state = ptr(""), sQuery
			case c == '#':
				u.fragment, state = ptr(""), sFragment
			case c == ' ':
				if next := at(p + 1); next == '?' || next == '#' {
					u.opath += "%20"
				} else {
					u.opath += " "
				}
			case c != -1:
				u.opath += percentEncode(c, c0Set)
			}
		case sQuery:
			if c == '#' || c == -1 {
				set := querySet
				if special {
					set = specialQuerySet
				}
				var b strings.Builder
				for _, r := range buf {
					b.WriteString(percentEncode(r, set))
				}
				*u.query += b.String()
				buf = buf[:0]
				if c == '#' {
					u.fragment, state = ptr(""), sFragment
				}
			} else {
				buf = append(buf, c)
			}
		case sFragment:
			if c != -1 {
				*u.fragment += percentEncode(c, fragmentSet)
			}
		}
		if p >= len(in) {
			return u, nil
		}
	}
}

// ResolveURL is new URL(raw, base).
func ResolveURL(raw, base string) (*URL, error) {
	b, err := ParseURL(base, nil)
	if err != nil {
		return nil, err
	}
	return ParseURL(raw, b)
}

func IsAlpha(c rune) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func IsDigit(c rune) bool { return c >= '0' && c <= '9' }
func isHex(c rune) bool   { return IsDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
func LowerASCII(c rune) rune {
	if c >= 'A' && c <= 'Z' {
		return c + 32
	}
	return c
}

func isDrive(b []rune) bool { return len(b) == 2 && IsAlpha(b[0]) && (b[1] == ':' || b[1] == '|') }
func normalizedDrive(s string) bool {
	return len(s) == 2 && IsAlpha(rune(s[0])) && s[1] == ':'
}
func startsWithDrive(r []rune) bool {
	return len(r) >= 2 && isDrive(r[:2]) && (len(r) == 2 || strings.ContainsRune(`/\?#`, r[2]))
}

func singleDot(s string) bool { return s == "." || strings.EqualFold(s, "%2e") }
func doubleDot(s string) bool {
	switch strings.ToLower(s) {
	case "..", ".%2e", "%2e.", "%2e%2e":
		return true
	}
	return false
}

func c0Set(c rune) bool       { return c < 0x20 || c > 0x7e }
func fragmentSet(c rune) bool { return c0Set(c) || strings.ContainsRune(" \"<>`", c) }
func querySet(c rune) bool    { return c0Set(c) || strings.ContainsRune(" \"#<>", c) }
func specialQuerySet(c rune) bool {
	return querySet(c) || c == '\''
}
func pathSet(c rune) bool { return querySet(c) || strings.ContainsRune("?^`{}", c) }
func userinfoSet(c rune) bool {
	return pathSet(c) || strings.ContainsRune("/:;=@[\\]^|", c)
}

func percentEncode(c rune, set func(rune) bool) string {
	if !set(c) {
		return string(c)
	}
	var b strings.Builder
	for _, x := range []byte(string(c)) {
		b.WriteString("%" + strings.ToUpper(strconv.FormatUint(uint64(x)>>4, 16)+strconv.FormatUint(uint64(x)&15, 16)))
	}
	return b.String()
}

func forbiddenHost(r rune) bool {
	return r == 0 || strings.ContainsRune("\t\n\r #/:<>?@[\\]^|", r)
}

func forbiddenDomain(r rune) bool { return forbiddenHost(r) || r <= 0x1f || r == '%' || r == 0x7f }

// lookup is UTS #46 ToASCII with the URL standard's flags.
var lookup = idna.New(idna.MapForLookup(), idna.StrictDomainName(false), idna.CheckHyphens(false),
	idna.CheckJoiners(true), idna.BidiRule(), idna.Transitional(false), idna.VerifyDNSLength(false))

func parseHost(input string, opaque bool) (string, error) {
	if strings.HasPrefix(input, "[") {
		if !strings.HasSuffix(input, "]") {
			return "", errURL
		}
		a, err := parseIPv6([]rune(input[1 : len(input)-1]))
		if err != nil {
			return "", err
		}
		return "[" + ipv6String(a) + "]", nil
	}
	if opaque {
		var b strings.Builder
		for _, r := range input {
			if forbiddenHost(r) {
				return "", errURL
			}
			b.WriteString(percentEncode(r, c0Set))
		}
		return b.String(), nil
	}
	domain := strings.ToValidUTF8(percentDecode(input), "�")
	ascii := strings.ToLower(domain)
	if !isASCIIText(domain) || hasPunycode(domain) {
		var err error
		if ascii, err = lookup.ToASCII(domain); err != nil {
			return "", errURL
		}
	}
	if ascii == "" || strings.IndexFunc(ascii, forbiddenDomain) >= 0 {
		return "", errURL
	}
	if endsInNumber(ascii) {
		return parseIPv4(ascii)
	}
	return ascii, nil
}

func isASCIIText(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func hasPunycode(s string) bool {
	for _, label := range strings.Split(s, ".") {
		if len(label) >= 4 && strings.EqualFold(label[:4], "xn--") {
			return true
		}
	}
	return false
}

func percentDecode(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(rune(s[i+1])) && isHex(rune(s[i+2])) {
			v, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
			b = append(b, byte(v))
			i += 2
		} else {
			b = append(b, s[i])
		}
	}
	return string(b)
}

func endsInNumber(s string) bool {
	parts := strings.Split(s, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) == 1 {
			return false
		}
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last != "" && strings.Trim(last, "0123456789") == "" {
		return true
	}
	_, ok := ipv4Number(last)
	return ok
}

// ipv4Number saturates at 1<<40, far above any value an address allows.
func ipv4Number(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	radix := uint64(10)
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		radix, s = 16, s[2:]
	} else if len(s) >= 2 && s[0] == '0' {
		radix, s = 8, s[1:]
	}
	var v uint64
	for _, c := range s {
		d, err := strconv.ParseUint(string(c), int(radix), 8)
		if err != nil {
			return 0, false
		}
		v = min(v*radix+d, 1<<40)
	}
	return v, true
}

func parseIPv4(s string) (string, error) {
	parts := strings.Split(s, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return "", errURL
	}
	var numbers []uint64
	for _, part := range parts {
		n, ok := ipv4Number(part)
		if !ok {
			return "", errURL
		}
		numbers = append(numbers, n)
	}
	last := len(numbers) - 1
	for _, n := range numbers[:last] {
		if n > 255 {
			return "", errURL
		}
	}
	if numbers[last] >= 1<<(8*(5-len(numbers))) {
		return "", errURL
	}
	ip := numbers[last]
	for i, n := range numbers[:last] {
		ip += n << (8 * (3 - i))
	}
	return strconv.FormatUint(ip>>24, 10) + "." + strconv.FormatUint(ip>>16&255, 10) + "." + strconv.FormatUint(ip>>8&255, 10) + "." + strconv.FormatUint(ip&255, 10), nil
}

func parseIPv6(in []rune) ([8]uint16, error) {
	var a [8]uint16
	piece, compress, p := 0, -1, 0
	at := func(i int) rune {
		if i < len(in) {
			return in[i]
		}
		return -1
	}
	if at(p) == ':' {
		if at(p+1) != ':' {
			return a, errURL
		}
		p += 2
		piece++
		compress = piece
	}
	for at(p) != -1 {
		if piece == 8 {
			return a, errURL
		}
		if at(p) == ':' {
			if compress != -1 {
				return a, errURL
			}
			p++
			piece++
			compress = piece
			continue
		}
		value, length := 0, 0
		for length < 4 && isHex(at(p)) {
			d, _ := strconv.ParseUint(string(at(p)), 16, 8)
			value = value*16 + int(d)
			p++
			length++
		}
		if at(p) == '.' {
			if length == 0 || piece > 6 {
				return a, errURL
			}
			p -= length
			seen := 0
			for at(p) != -1 {
				part := -1
				if seen > 0 {
					if at(p) != '.' || seen >= 4 {
						return a, errURL
					}
					p++
				}
				if !IsDigit(at(p)) {
					return a, errURL
				}
				for IsDigit(at(p)) {
					n := int(at(p) - '0')
					switch {
					case part == -1:
						part = n
					case part == 0:
						return a, errURL
					default:
						part = part*10 + n
					}
					if part > 255 {
						return a, errURL
					}
					p++
				}
				a[piece] = a[piece]<<8 | uint16(part)
				if seen++; seen == 2 || seen == 4 {
					piece++
				}
			}
			if seen != 4 {
				return a, errURL
			}
			break
		} else if at(p) == ':' {
			if p++; at(p) == -1 {
				return a, errURL
			}
		} else if at(p) != -1 {
			return a, errURL
		}
		a[piece] = uint16(value)
		piece++
	}
	if compress != -1 {
		swaps := piece - compress
		for piece = 7; piece != 0 && swaps > 0; piece, swaps = piece-1, swaps-1 {
			a[piece], a[compress+swaps-1] = a[compress+swaps-1], a[piece]
		}
	} else if piece != 8 {
		return a, errURL
	}
	return a, nil
}

func ipv6String(a [8]uint16) string {
	compress, best := -1, 1
	for i := 0; i < 8; {
		j := i
		for j < 8 && a[j] == 0 {
			j++
		}
		if j-i > best {
			compress, best = i, j-i
		}
		i = max(j, i+1)
	}
	var b strings.Builder
	ignore := false
	for i, v := range a {
		if ignore && v == 0 {
			continue
		}
		ignore = false
		if i == compress {
			if i == 0 {
				b.WriteString("::")
			} else {
				b.WriteString(":")
			}
			ignore = true
			continue
		}
		b.WriteString(strconv.FormatUint(uint64(v), 16))
		if i != 7 {
			b.WriteByte(':')
		}
	}
	return b.String()
}

// URLKey is importFeed's key(): the address with no www., fragment or
// trailing slash, so two spellings of one article meet.
func URLKey(value string) (string, error) {
	u, err := ParseURL(value, nil)
	if err != nil {
		return "", err
	}
	if !u.opaque {
		// The hostname setter: an empty host is refused for special URLs and
		// for others with credentials or a port.
		if h := u.Hostname(); strings.HasPrefix(h, "www.") {
			rest := h[4:]
			if rest != "" || !u.special() && u.Username == "" && u.Password == "" && u.port < 0 {
				if parsed, err := parseHost(rest, !u.special()); err == nil {
					u.host = &parsed
				}
			}
		}
		// The pathname setter reparses a serialized path, which only splits it.
		path := strings.TrimRight(u.Pathname(), "/")
		if path == "" {
			path = "/"
		}
		u.path = strings.Split(path[1:], "/")
	}
	u.fragment = nil
	return u.Href(), nil
}
