package web

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// entities are the named entities Decode knows.
var entities = map[string]string{"amp": "&", "lt": "<", "gt": ">", "quot": `"`, "apos": "'", "nbsp": " ", "hellip": "…", "mdash": "—", "ndash": "–",
	"lsquo": "‘", "rsquo": "’", "ldquo": "“", "rdquo": "”", "laquo": "«", "raquo": "»", "middot": "·", "times": "×", "copy": "©", "reg": "®", "trade": "™"}

// Decode is the entity decoder: the named entities above and numeric ones.
// Two numeric entities forming a surrogate pair join into one code point, as
// concatenation does in JavaScript; ponytail: a lone surrogate becomes
// U+FFFD, the most a Go string can hold.
func Decode(text string) string {
	if !strings.Contains(text, "&") {
		return text
	}
	var b strings.Builder
	high := rune(-1) // a numeric high surrogate waiting for its low half
	lone := func() {
		if high >= 0 {
			b.WriteRune(unicode.ReplacementChar)
			high = -1
		}
	}
	for i := 0; i < len(text); {
		name, code, n := entity(text[i:])
		switch {
		case n == 0:
			lone()
			j := strings.IndexByte(text[i+1:], '&')
			if j < 0 {
				j = len(text) - i - 1
			}
			b.WriteString(text[i : i+1+j])
			i += 1 + j
			continue
		case code < 0:
			lone()
			b.WriteString(name)
		case code >= 0xdc00 && code < 0xe000 && high >= 0:
			b.WriteRune(utf16.DecodeRune(high, code))
			high = -1
		case code >= 0xd800 && code < 0xdc00:
			lone()
			high = code
		default:
			lone()
			b.WriteRune(code)
		}
		i += n
	}
	lone()
	return b.String()
}

// entity reads /&(?:#(\d{1,7})|#x([\da-f]{1,6})|([a-z]{2,8}));/i at the start
// of s: a known name's text (code -1) or a code point in 1..0x10ffff, and its
// length; n is 0 when there is none to replace.
func entity(s string) (string, rune, int) {
	if len(s) < 3 || s[0] != '&' {
		return "", 0, 0
	}
	if s[1] == '#' {
		digits, base, limit, start := "0123456789", 10, 7, 2
		if s[2] == 'x' || s[2] == 'X' {
			digits, base, limit, start = "0123456789abcdefABCDEF", 16, 6, 3
		}
		j := start
		for j < len(s) && strings.IndexByte(digits, s[j]) >= 0 {
			j++
		}
		if j == start || j-start > limit || j >= len(s) || s[j] != ';' {
			return "", 0, 0
		}
		v, _ := strconv.ParseInt(s[start:j], base, 32)
		if v <= 0 || v > 0x10ffff {
			return "", 0, 0
		}
		return "", rune(v), j + 1
	}
	j := 1
	for j < len(s) && IsAlpha(rune(s[j])) {
		j++
	}
	if j-1 < 2 || j-1 > 8 || j >= len(s) || s[j] != ';' {
		return "", 0, 0
	}
	text, ok := entities[strings.ToLower(s[1:j])]
	if !ok {
		return "", 0, 0
	}
	return text, -1, j + 1
}
