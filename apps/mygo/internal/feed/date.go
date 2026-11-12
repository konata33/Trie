package feed

import (
	"math"
	"regexp"
	"strconv"
	"time"

	"trie/internal/web"
)

// Feeds write dates in every shape (RFC 822 with or without a weekday, zone
// names, "2026-11-07 10:00:00 +0800"), and Parse reads them as
// Date.parse does. jsDate is V8's DateParser (src/date/dateparser*.{h,cc},
// builtins-date.cc ParseDateTimeString), ported line for line: the ES date time
// string format first, then the legacy rules for whatever is left.

const none = math.MaxInt32 // kNone

const (
	tagInvalid = -6 + iota
	tagUnknown
	tagSpace
	tagNumber
	tagSymbol
	tagEnd
)

// Keyword types; a keyword token's tag is its type, 0 to 4.
const (
	kwInvalid = iota
	kwMonth
	kwZone
	kwTimeSeparator
	kwAmPm
)

type token struct{ tag, length, value int }

func (t token) number(length int) bool {
	return t.tag == tagNumber && (length < 0 || t.length == length)
}
func (t token) symbol(c int) bool { return t.tag == tagSymbol && t.value == c }
func (t token) sign() bool        { return t.symbol('+') || t.symbol('-') }
func (t token) keyword() bool     { return t.tag >= 0 }
func (t token) z() bool           { return t.tag == kwZone && t.length == 1 && t.value == 0 }

var keywords = []struct {
	prefix [3]uint16
	kind   int
	value  int
}{
	{[3]uint16{'j', 'a', 'n'}, kwMonth, 1}, {[3]uint16{'f', 'e', 'b'}, kwMonth, 2}, {[3]uint16{'m', 'a', 'r'}, kwMonth, 3},
	{[3]uint16{'a', 'p', 'r'}, kwMonth, 4}, {[3]uint16{'m', 'a', 'y'}, kwMonth, 5}, {[3]uint16{'j', 'u', 'n'}, kwMonth, 6},
	{[3]uint16{'j', 'u', 'l'}, kwMonth, 7}, {[3]uint16{'a', 'u', 'g'}, kwMonth, 8}, {[3]uint16{'s', 'e', 'p'}, kwMonth, 9},
	{[3]uint16{'o', 'c', 't'}, kwMonth, 10}, {[3]uint16{'n', 'o', 'v'}, kwMonth, 11}, {[3]uint16{'d', 'e', 'c'}, kwMonth, 12},
	{[3]uint16{'a', 'm'}, kwAmPm, 0}, {[3]uint16{'p', 'm'}, kwAmPm, 12},
	{[3]uint16{'u', 't'}, kwZone, 0}, {[3]uint16{'u', 't', 'c'}, kwZone, 0}, {[3]uint16{'z'}, kwZone, 0}, {[3]uint16{'g', 'm', 't'}, kwZone, 0},
	{[3]uint16{'c', 'd', 't'}, kwZone, -5}, {[3]uint16{'c', 's', 't'}, kwZone, -6}, {[3]uint16{'e', 'd', 't'}, kwZone, -4},
	{[3]uint16{'e', 's', 't'}, kwZone, -5}, {[3]uint16{'m', 'd', 't'}, kwZone, -6}, {[3]uint16{'m', 's', 't'}, kwZone, -7},
	{[3]uint16{'p', 'd', 't'}, kwZone, -7}, {[3]uint16{'p', 's', 't'}, kwZone, -8},
	{[3]uint16{'t'}, kwTimeSeparator, 0},
}

// scanner is InputReader and DateStringTokenizer over UTF-16 code units. A
// NUL ends the input, as in V8.
type scanner struct {
	s     []uint16
	index int
	ch    uint16
	peek  token
}

func (r *scanner) step() {
	r.ch = 0
	if r.index < len(r.s) {
		r.ch = r.s[r.index]
	}
	r.index++
}

func (r *scanner) skip(c uint16) bool {
	if r.ch == c {
		r.step()
		return true
	}
	return false
}

func (r *scanner) digit() bool { return r.ch >= '0' && r.ch <= '9' }

// white is V8's IsWhiteSpace: JavaScript white space without the line terminators.
func white(c uint16) bool {
	return web.JSSpace(rune(c)) && c != '\n' && c != '\r' && c != 0x2028 && c != 0x2029
}

func (r *scanner) scan() token {
	start := r.index
	switch {
	case r.ch == 0:
		return token{tagEnd, 0, -1}
	case r.digit():
		n, digits := 0, 0
		for r.ch == '0' {
			r.step()
		}
		for ; r.digit(); r.step() {
			if digits < 9 {
				n = n*10 + int(r.ch-'0')
			}
			digits++
		}
		return token{tagNumber, r.index - start, n}
	case r.ch == ':' || r.ch == '-' || r.ch == '+' || r.ch == '.' || r.ch == ')':
		c := r.ch
		r.step()
		return token{tagSymbol, 1, int(c)}
	case r.ch >= 'A' && !white(r.ch):
		var prefix [3]uint16
		length := 0
		for ; r.ch >= 'A' && !white(r.ch); r.step() {
			if length < 3 {
				prefix[length] = r.ch | 0x20
			}
			length++
		}
		for _, k := range keywords {
			if k.prefix == prefix && (length <= 3 || k.kind == kwMonth) {
				return token{k.kind, length, k.value}
			}
		}
		return token{kwInvalid, length, 0}
	case web.JSSpace(rune(r.ch)):
		r.step()
		return token{tagSpace, r.index - start, -1}
	case r.ch == '(':
		for balance := 0; ; {
			if r.ch == ')' {
				balance--
			} else if r.ch == '(' {
				balance++
			}
			r.step()
			if balance <= 0 || r.ch == 0 {
				break
			}
		}
		return token{tagUnknown, 1, -1}
	}
	r.step()
	return token{tagUnknown, 1, -1}
}

func (r *scanner) next() token {
	t := r.peek
	r.peek = r.scan()
	return t
}

func (r *scanner) skipSymbol(c int) bool {
	if r.peek.symbol(c) {
		r.peek = r.scan()
		return true
	}
	return false
}

func between(x, lo, hi int) bool { return x >= lo && x <= hi }

type zone struct{ sign, hour, minute int }

func (z *zone) set(hours int) {
	z.sign = 1
	if hours < 0 {
		z.sign = -1
	}
	z.hour, z.minute = hours*z.sign, 0
}
func (z *zone) expecting(n int) bool { return z.hour != none && z.minute == none && between(n, 0, 59) }
func (z *zone) utc() bool            { return z.hour == 0 && z.minute == 0 }

type clock struct {
	comp       [4]int
	index      int
	hourOffset int
}

func (c *clock) empty() bool { return c.index == 0 }
func (c *clock) expecting(n int) bool {
	return c.index == 1 && between(n, 0, 59) || c.index == 2 && between(n, 0, 59) || c.index == 3 && between(n, 0, 999)
}
func (c *clock) add(n int) bool {
	if c.index < 4 {
		c.comp[c.index] = n
		c.index++
		return true
	}
	return false
}
func (c *clock) addFinal(n int) bool {
	if !c.add(n) {
		return false
	}
	for c.index < 4 {
		c.comp[c.index] = 0
		c.index++
	}
	return true
}

type calendar struct {
	comp       [3]int
	index      int
	namedMonth int
	iso        bool
}

func (d *calendar) add(n int) bool {
	if d.index < 3 {
		d.comp[d.index] = n
		d.index++
		return true
	}
	return false
}

func milliseconds(t token) int {
	n, length := t.value, t.length
	switch {
	case length == 1:
		n *= 100
	case length == 2:
		n *= 10
	case length > 3:
		length = min(length, 9)
		for ; length > 3; length-- {
			n /= 10
		}
	}
	return n
}

// es5 is ParseES5DateTime: the token the legacy parser goes on from, end
// of input when the string was in the ES format, or invalid.
func es5(r *scanner, day *calendar, clk *clock, tz *zone) token {
	invalid := token{tagInvalid, 0, -1}
	if r.peek.sign() {
		sign := r.next()
		if !r.peek.number(6) {
			return sign
		}
		year := r.next().value
		if sign.value == '-' && year == 0 {
			return sign
		}
		if sign.value == '-' {
			year = -year
		}
		day.add(year)
	} else if r.peek.number(4) {
		day.add(r.next().value)
	} else {
		return r.next()
	}
	if r.skipSymbol('-') {
		if !r.peek.number(2) || !between(r.peek.value, 1, 12) {
			return r.next()
		}
		day.add(r.next().value)
		if r.skipSymbol('-') {
			if !r.peek.number(2) || !between(r.peek.value, 1, 31) {
				return r.next()
			}
			day.add(r.next().value)
		}
	}
	if r.peek.tag != kwTimeSeparator {
		if r.peek.tag != tagEnd {
			return r.next()
		}
	} else {
		r.next()
		if !r.peek.number(2) || !between(r.peek.value, 0, 24) {
			return invalid
		}
		hour24 := r.peek.value == 24
		clk.add(r.next().value)
		if !r.skipSymbol(':') {
			return invalid
		}
		if !r.peek.number(2) || !between(r.peek.value, 0, 59) || hour24 && r.peek.value > 0 {
			return invalid
		}
		clk.add(r.next().value)
		if r.skipSymbol(':') {
			if !r.peek.number(2) || !between(r.peek.value, 0, 59) || hour24 && r.peek.value > 0 {
				return invalid
			}
			clk.add(r.next().value)
			if r.skipSymbol('.') {
				if !r.peek.number(-1) || hour24 && r.peek.value > 0 {
					return invalid
				}
				clk.add(milliseconds(r.next()))
			}
		}
		if r.peek.z() {
			r.next()
			tz.set(0)
		} else if r.peek.sign() {
			tz.sign = 1
			if r.next().value == '-' {
				tz.sign = -1
			}
			if r.peek.number(4) {
				hourmin := r.next().value
				if !between(hourmin/100, 0, 23) || !between(hourmin%100, 0, 59) {
					return invalid
				}
				tz.hour, tz.minute = hourmin/100, hourmin%100
			} else {
				if !r.peek.number(2) || !between(r.peek.value, 0, 23) {
					return invalid
				}
				tz.hour = r.next().value
				if !r.skipSymbol(':') {
					return invalid
				}
				if !r.peek.number(2) || !between(r.peek.value, 0, 59) {
					return invalid
				}
				tz.minute = r.next().value
			}
		}
		if r.peek.tag != tagEnd {
			return invalid
		}
	}
	// Date-only forms are UTC, date-time forms without an offset local time.
	if tz.hour == none && clk.empty() {
		tz.set(0)
	}
	day.iso = true
	return token{tagEnd, 0, -1}
}

// jsDate is Date.parse(s): milliseconds since the epoch, or false for NaN.
func jsDate(s string) (int64, bool) {
	r := &scanner{s: web.Units(s)}
	r.step()
	r.peek = r.scan()
	tz := zone{none, none, none}
	clk := clock{hourOffset: none}
	day := calendar{namedMonth: none}
	t := es5(r, &day, &clk, &tz)
	if t.tag == tagInvalid {
		return 0, false
	}
	read := day.index > 0
	for ; t.tag != tagEnd; t = r.next() {
		switch {
		case t.tag == tagNumber:
			read = true
			n := t.value
			if r.skipSymbol(':') {
				if r.skipSymbol(':') {
					if !clk.empty() {
						return 0, false
					}
					clk.add(n)
					clk.add(0)
				} else {
					if !clk.add(n) {
						return 0, false
					}
					if r.peek.symbol('.') {
						r.next()
					}
				}
			} else if r.skipSymbol('.') && clk.expecting(n) {
				clk.add(n)
				if !r.peek.number(-1) {
					return 0, false
				}
				ms := milliseconds(r.next())
				if ms < 0 {
					return 0, false
				}
				clk.addFinal(ms)
			} else if tz.expecting(n) {
				tz.minute = n
			} else if clk.expecting(n) {
				clk.addFinal(n)
				// Require end, white space, "Z", "+" or "-" immediately after finalizing time.
				if p := r.peek; p.tag != tagEnd && p.tag != tagSpace && !p.z() && !p.sign() {
					return 0, false
				}
			} else {
				if !day.add(n) {
					return 0, false
				}
				r.skipSymbol('-')
			}
		case t.keyword():
			switch {
			case t.tag == kwAmPm && !clk.empty():
				clk.hourOffset = t.value
			case t.tag == kwMonth:
				day.namedMonth = t.value
				r.skipSymbol('-')
			case t.tag == kwZone && read:
				tz.set(t.value)
			default:
				// Garbage words are illegal once a number has been read, and must be apart from the first number.
				if read || r.peek.tag == tagNumber {
					return 0, false
				}
			}
		case t.sign() && (tz.utc() || !clk.empty()):
			tz.sign = 1
			if t.value == '-' {
				tz.sign = -1
			}
			n, length := 0, 0
			if r.peek.tag == tagNumber {
				next := r.next()
				n, length = next.value, next.length
			}
			read = true
			switch {
			case r.peek.symbol(':'):
				tz.hour, tz.minute = n, none
			case length == 1 || length == 2:
				tz.hour, tz.minute = n, 0
			case length == 3 || length == 4:
				tz.hour, tz.minute = n/100, n%100
			default:
				return 0, false
			}
		case (t.sign() || t.symbol(')')) && read:
			return 0, false
		}
	}
	return day.write(&clk, &tz)
}

// write is DayComposer, TimeComposer and TimeZoneComposer::Write, then
// MakeDay, MakeTime, the local time conversion and TimeClip.
func (d *calendar) write(clk *clock, tz *zone) (int64, bool) {
	if d.index < 1 {
		return 0, false
	}
	for d.index < 3 {
		d.comp[d.index] = 1
		d.index++
	}
	year, month, day := 0, none, none
	if d.namedMonth == none {
		if d.iso || !between(d.comp[0], 1, 31) {
			year, month, day = d.comp[0], d.comp[1], d.comp[2]
		} else {
			month, day, year = d.comp[0], d.comp[1], d.comp[2]
		}
	} else {
		month = d.namedMonth
		if !between(d.comp[0], 1, 31) {
			year, day = d.comp[0], d.comp[1]
		} else {
			day, year = d.comp[0], d.comp[1]
		}
	}
	if !d.iso {
		if between(year, 0, 49) {
			year += 2000
		} else if between(year, 50, 99) {
			year += 1900
		}
	}
	if !between(month, 1, 12) || !between(day, 1, 31) {
		return 0, false
	}
	for clk.index < 4 {
		clk.comp[clk.index] = 0
		clk.index++
	}
	hour, minute, second, ms := clk.comp[0], clk.comp[1], clk.comp[2], clk.comp[3]
	if clk.hourOffset != none {
		if !between(hour, 0, 12) {
			return 0, false
		}
		hour = hour%12 + clk.hourOffset
	}
	if !between(hour, 0, 23) || !between(minute, 0, 59) || !between(second, 0, 59) || !between(ms, 0, 999) {
		if hour != 24 || minute != 0 || second != 0 || ms != 0 {
			return 0, false
		}
	}
	// MakeDay takes years within a million of 0; anything near that is out of range anyway.
	if !between(year, -1000000, 1000000) {
		return 0, false
	}
	t := time.Date(year, time.Month(month), day, hour, minute, second, ms*int(time.Millisecond), time.UTC).UnixMilli()
	if tz.sign == none {
		t = localToUTC(t)
	} else {
		if tz.hour == none {
			tz.hour = 0
		}
		if tz.minute == none {
			tz.minute = 0
		}
		// Unsigned 32-bit arithmetic, checked against Smi::kMaxValue (Node builds without pointer compression).
		total := uint32(tz.hour)*3600 + uint32(tz.minute)*60
		if total > math.MaxInt32 {
			return 0, false
		}
		t -= int64(tz.sign) * int64(total) * 1000
	}
	if t < -8.64e15 || t > 8.64e15 {
		return 0, false
	}
	return t, true
}

// localToUTC is DateCache::ToUTC for a wall clock time w: a time skipped or
// repeated at a transition is read with the offset in effect before it
// (ICU's UCAL_TZ_LOCAL_FORMER), as V8 reads it; Go's time.Date picks the
// later offset for a skipped time.
func localToUTC(w int64) int64 {
	offset := func(t int64) int64 {
		_, seconds := time.UnixMilli(t).In(time.Local).Zone()
		return int64(seconds) * 1000
	}
	const day = 86400000
	before, after := offset(w-day), offset(w+day)
	if offset(w-before) == before || offset(w-after) != after {
		return w - before
	}
	return w - after
}

var (
	yearOnly  = regexp.MustCompile(`^\d{4}$`)
	yearMonth = regexp.MustCompile(`^\d{4}-\d{2}$`)
	dateOnly  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	dateStart = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})`)
)

// feedDate is a feed's date as the store keeps
// it, nil when there is none or it is not a real date.
func feedDate(value string) *string {
	value = web.JSTrim(value)
	if value == "" {
		return nil
	}
	if yearOnly.MatchString(value) {
		if n, _ := strconv.Atoi(value); n > 0 {
			return &value
		}
		return nil
	}
	if yearMonth.MatchString(value) {
		if m, _ := strconv.Atoi(value[5:]); m >= 1 && m <= 12 {
			return &value
		}
		return nil
	}
	if c := dateStart.FindStringSubmatch(value); c != nil {
		y, _ := strconv.Atoi(c[1])
		m, _ := strconv.Atoi(c[2])
		d, _ := strconv.Atoi(c[3])
		if y <= 99 { // Date.UTC reads years 0 to 99 as 1900 to 1999.
			y += 1900
		}
		if m < 1 || m > 12 || d < 1 || d > time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day() {
			return nil
		}
	}
	ms, ok := jsDate(value)
	if !ok {
		return nil
	}
	if dateOnly.MatchString(value) {
		return &value
	}
	iso := web.ISOTime(ms)
	return &iso
}
