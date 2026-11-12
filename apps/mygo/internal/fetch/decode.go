package fetch

import (
	"bytes"
	"regexp"
	"strings"
	"unicode/utf8"

	"trie/internal/web"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/unicode"
)

var charset = regexp.MustCompile(`charset[` + web.Space + `]*=[` + web.Space + `]*["']?([\w-]+)`)

// Charset is the charset a Content-Type names, read as reader-service.ts
// reads it (/charset\s*=\s*["']?([\w-]+)/i); "" when it names none. Labels
// are case-insensitive, so the lowered spelling is returned.
func Charset(contentType string) string {
	if m := charset.FindStringSubmatch(strings.Map(web.LowerASCII, contentType)); m != nil {
		return m[1]
	}
	return ""
}

// Decode is new TextDecoder(label).decode(body), the WHATWG Encoding
// Standard's decoder with replacement characters; "" is UTF-8. A label
// TextDecoder refuses (unknown, or one of the "replacement" labels) reads as
// body.toString('utf8'), which keeps a byte order mark.
func Decode(body []byte, label string) string {
	if label == "" {
		label = "utf-8"
	}
	e, err := htmlindex.Get(label)
	if err != nil || e == encoding.Replacement {
		return utf8String(body)
	}
	// TextDecoder drops a byte order mark of its own encoding, and only that.
	name, _ := htmlindex.Name(e)
	switch name {
	case "utf-8":
		return utf8String(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")))
	case "utf-16le":
		body = bytes.TrimPrefix(body, []byte("\xff\xfe"))
	case "utf-16be":
		body = bytes.TrimPrefix(body, []byte("\xfe\xff"))
	}
	if name == "iso-8859-8-i" { // the same table, a logical-order label
		e = charmap.ISO8859_8
	}
	if c, ok := e.(*charmap.Charmap); ok {
		// The WHATWG indexes give C1 bytes no table entry of their own: they read as U+0080 to U+009F,
		// which x/text leaves out of some tables (windows-1252 0x81, iso-8859-2 0x96).
		out := make([]rune, len(body))
		for i, b := range body {
			if out[i] = c.DecodeByte(b); out[i] == utf8.RuneError && b >= 0x80 && b <= 0x9f {
				out[i] = rune(b)
			}
		}
		return string(out)
	}
	// ponytail: Node decodes the multi-byte encodings with ICU, which reads some invalid or user-defined
	// sequences differently from x/text's WHATWG decoders (PUA, C1 controls, U+FFFD); real text reads the same.
	out, err := e.NewDecoder().Bytes(body)
	if err != nil {
		return utf8String(body)
	}
	return string(out)
}

// utf8String replaces each maximal ill-formed subsequence with U+FFFD, as V8
// and TextDecoder do (Go's own conversion replaces byte by byte).
func utf8String(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	out, _ := unicode.UTF8.NewDecoder().Bytes(b)
	return string(out)
}
