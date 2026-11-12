package web

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
)

// store.ts runs on JavaScript strings, numbers and dates. The helpers here
// reproduce the parts of their semantics the store relies on, so the Go store
// accepts, rejects and computes what the Electron store does.

// JSLen is a string's JavaScript length, in UTF-16 code units.
func JSLen(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// JSSpace is JavaScript's \s, the set String.prototype.trim removes.
func JSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// space is JSSpace as the body of a regexp class.
const Space = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

func JSTrim(s string) string { return strings.TrimFunc(s, JSSpace) }

// Units and FromUnits convert between Go strings and UTF-16, where
// JavaScript indexes strings. A lone surrogate, which a slice can leave and a
// Go string cannot hold, becomes U+FFFD.
func Units(s string) []uint16 { return utf16.Encode([]rune(s)) }

func FromUnits(u []uint16) string { return string(utf16.Decode(u)) }

// CodePoints decodes UTF-16 as JavaScript's /u regexps and toLowerCase see it:
// a lone surrogate stays a code point of its own, with no letter, space or
// punctuation properties.
func CodePoints(u []uint16) []rune {
	out := make([]rune, 0, len(u))
	for i := 0; i < len(u); i++ {
		r := rune(u[i])
		if utf16.IsSurrogate(r) && r < 0xdc00 && i+1 < len(u) && u[i+1] >= 0xdc00 && u[i+1] < 0xe000 {
			r = utf16.DecodeRune(r, rune(u[i+1]))
			i++
		}
		out = append(out, r)
	}
	return out
}

func EncodeUnits(rs []rune) []uint16 {
	out := make([]uint16, 0, len(rs))
	for _, r := range rs {
		if r >= 0x10000 {
			a, b := utf16.EncodeRune(r)
			out = append(out, uint16(a), uint16(b))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

// JSLower is String.prototype.toLowerCase over code points: simple mappings,
// İ to i̇, and Σ to ς at the end of a word (Final_Sigma).
func JSLower(rs []rune) []rune {
	out := make([]rune, 0, len(rs))
	for i, r := range rs {
		switch {
		case r == 0x130:
			out = append(out, 'i', 0x307)
		case r == 0x3a3 && finalSigma(rs, i):
			out = append(out, 0x3c2)
		default:
			out = append(out, unicode.ToLower(r))
		}
	}
	return out
}

func finalSigma(rs []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		if !caseIgnorable(rs[j]) {
			before = cased(rs[j])
			break
		}
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(rs); j++ {
		if !caseIgnorable(rs[j]) {
			return !cased(rs[j])
		}
	}
	return true
}

func cased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) || unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

func caseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', '^', '`', 0xa8, 0xad, 0xaf, 0xb4, 0xb7, 0xb8, 0x387, 0x55f, 0x5f4, 0x2018, 0x2019, 0x2024, 0x2027, 0xfe13, 0xfe52, 0xfe55, 0xff07, 0xff0e, 0xff1a:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// JSString is String(value) for a decoded JSON value.
func JSString(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(v)
	case string:
		return v
	case json.Number:
		f, _ := NumberValue(v)
		return jsNumberString(f)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			if item != nil {
				parts[i] = JSString(item)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func jsNumberString(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	b, _ := json.Marshal(f) // encoding/json formats floats as JavaScript does
	return string(b)
}

// NumberValue reads a JSON number as JSON.parse does: out-of-range values
// are infinite, not errors.
func NumberValue(n json.Number) (float64, bool) {
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil && !isRange(err) {
		return 0, false
	}
	return f, true
}

func isRange(err error) bool {
	e, ok := err.(*strconv.NumError)
	return ok && e.Err == strconv.ErrRange
}

// JSNumber is Number(value) for a string: StringToNumber.
func JSNumber(s string) float64 {
	s = JSTrim(s)
	if s == "" {
		return 0
	}
	if len(s) > 2 && s[0] == '0' {
		radix := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[s[1]]
		if radix != 0 {
			v := 0.0
			for _, c := range s[2:] {
				d, err := strconv.ParseInt(string(c), radix, 8)
				if err != nil {
					return math.NaN()
				}
				v = v*float64(radix) + float64(d)
			}
			return v
		}
	}
	body := strings.TrimLeft(s, "+-")
	if len(s)-len(body) > 1 {
		return math.NaN()
	}
	if body == "Infinity" {
		if s[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	if !decimalLiteral(body) {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !isRange(err) {
		return math.NaN()
	}
	return f
}

// decimalLiteral is StrUnsignedDecimalLiteral without Infinity: digits with an
// optional point and exponent, as JavaScript writes them.
func decimalLiteral(s string) bool {
	i, digits := 0, 0
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		digits++
	}
	if i < len(s) && s[i] == '.' {
		for i++; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			digits++
		}
	}
	if digits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exp := i
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		}
		if i == exp {
			return false
		}
	}
	return i == len(s)
}

// JSParseInt is parseInt(value, 10); ok is false for NaN.
func JSParseInt(s string) (float64, bool) {
	s = strings.TrimLeftFunc(s, JSSpace)
	sign := 1.0
	if s != "" && (s[0] == '+' || s[0] == '-') {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	f, _ := strconv.ParseFloat(s[:end], 64)
	return sign * f, true
}

// maxTime is the largest distance from the epoch a JavaScript Date holds.
const maxTime = 8.64e15

// JSDateParse is Date.parse for the ES date time string format, read as V8
// reads it: date-only forms are UTC, date-time forms without an offset are
// local time, any number of fraction digits, lowercase t and z.
// ponytail: V8 falls back to a lenient legacy parser for other text (RFC 2822
// and the like); here that text is invalid. The library only stores times in
// this format.
func JSDateParse(s string) (int64, bool) {
	i := 0
	fixed := func(n int) (int, bool) {
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j-i != n {
			return 0, false
		}
		v, _ := strconv.Atoi(s[i:j])
		i = j
		return v, true
	}
	next := func(c byte) bool {
		if i < len(s) && s[i] == c {
			i++
			return true
		}
		return false
	}
	var year int
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		negative := s[i] == '-'
		i++
		y, ok := fixed(6)
		if !ok || negative && y == 0 {
			return 0, false
		}
		if negative {
			y = -y
		}
		year = y
	} else if y, ok := fixed(4); ok {
		year = y
	} else {
		return 0, false
	}
	month, day := 1, 1
	if next('-') {
		m, ok := fixed(2)
		if !ok || m < 1 || m > 12 {
			return 0, false
		}
		month = m
		if next('-') {
			d, ok := fixed(2)
			if !ok || d < 1 || d > 31 {
				return 0, false
			}
			day = d
		}
	}
	hour, minute, second, milli, offset := 0, 0, 0, 0, 0
	timed, zoned := false, false
	if next('T') || next('t') {
		timed = true
		h, ok := fixed(2)
		if !ok || h > 24 || !next(':') {
			return 0, false
		}
		m, ok := fixed(2)
		if !ok || m > 59 || h == 24 && m > 0 {
			return 0, false
		}
		hour, minute = h, m
		if next(':') {
			sec, ok := fixed(2)
			if !ok || sec > 59 || h == 24 && sec > 0 {
				return 0, false
			}
			second = sec
			if next('.') {
				j := i
				for i < len(s) && s[i] >= '0' && s[i] <= '9' {
					i++
				}
				fraction := s[j:i]
				if fraction == "" || h == 24 && strings.Trim(fraction, "0") != "" {
					return 0, false
				}
				milli, _ = strconv.Atoi((fraction + "00")[:3])
			}
		}
		if next('Z') || next('z') {
			zoned = true
		} else if i < len(s) && (s[i] == '+' || s[i] == '-') {
			sign := 1
			if s[i] == '-' {
				sign = -1
			}
			i++
			zoned = true
			var hh, mm int
			if v, ok := fixed(4); ok {
				hh, mm = v/100, v%100
			} else {
				if hh, ok = fixed(2); !ok || !next(':') {
					return 0, false
				}
				if mm, ok = fixed(2); !ok {
					return 0, false
				}
			}
			if hh > 23 || mm > 59 {
				return 0, false
			}
			offset = sign * (hh*60 + mm)
		}
	}
	if i != len(s) {
		return 0, false
	}
	loc := time.UTC
	if timed && !zoned {
		loc = time.Local
	}
	ms := time.Date(year, time.Month(month), day, hour, minute, second, milli*int(time.Millisecond), loc).UnixMilli() - int64(offset)*60000
	if math.Abs(float64(ms)) > maxTime {
		return 0, false
	}
	return ms, true
}

// ISOTime is Date.prototype.toISOString.
func ISOTime(ms int64) string {
	t := time.UnixMilli(ms).UTC()
	var year string
	switch {
	case t.Year() < 0:
		year = "-" + pad(-t.Year(), 6)
	case t.Year() > 9999:
		year = "+" + pad(t.Year(), 6)
	default:
		year = pad(t.Year(), 4)
	}
	return year + t.Format("-01-02T15:04:05.000Z")
}

func pad(n, width int) string {
	s := strconv.Itoa(n)
	return strings.Repeat("0", max(0, width-len(s))) + s
}

// Now is new Date().toISOString().
func Now() string { return ISOTime(time.Now().UnixMilli()) }
