package app

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"

	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/web"
)

// Dates, as the pages write them: in local time, in CLDR's forms of
// dateLocale, on the user's clock.

// dateLocale is the language dates are written in, the interface's
// (i18n.Locale): "zh-Hans" or "en".
func dateLocale() string {
	if strings.HasPrefix(i18n.Locale(), "zh") {
		return "zh-Hans"
	}
	return "en"
}

// hourCycle is the user's clock, 12 or 24, or 0 for the locale's own
// (Region.HourCycle). App.View sets it each frame.
var hourCycle int

// dateNow is the moment dates are written at; tests pin it.
var dateNow = time.Now

func local(ms int64) time.Time { return time.UnixMilli(ms).In(time.Local) }

// How much of a date a value names.
const (
	noDate    = iota // none, or none that reads
	yearOnly         // "2025"
	monthOnly        // "2025-11"
	dayOnly          // "2025-11-04"
	withTime         // a moment
)

// dated reads a date. A year, a month or a day written alone is as
// written, from midnight here; anything else is the moment JavaScript
// reads, here.
func dated(v *string) (time.Time, int) {
	if v == nil || *v == "" {
		return time.Time{}, noDate
	}
	s := *v
	for p, layout := range [...]string{yearOnly: "2006", monthOnly: "2006-01", dayOnly: "2006-01-02"} {
		if len(s) == len(layout) {
			if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
				return t, p
			}
		}
	}
	if ms, ok := web.JSDateParse(s); ok {
		return local(ms), withTime
	}
	return time.Time{}, noDate
}

func cldr(t time.Time, skeleton string) string {
	return i18n.FormatDate(dateLocale(), t, i18n.DateOptions{Skeleton: skeleton, HourCycle: hourCycle})
}

// daysSince is the whole days from t's day to now's, here; 0 for later.
func daysSince(t, now time.Time) int {
	mid := func(t time.Time) time.Time { y, m, d := t.Date(); return time.Date(y, m, d, 0, 0, 0, 0, t.Location()) }
	return max(0, int(math.Round(mid(now).Sub(mid(t)).Hours()/24)))
}

// daysAgo is daysSince for a written date; -1 for no date.
func daysAgo(v *string, now time.Time) int {
	t, p := dated(v)
	if p == noDate {
		return -1
	}
	return daysSince(t, now)
}

// absolute writes a date: 11月4日 this year, 2025年11月4日 in another;
// 11月 (2025年11月) for a month alone, 2025年 for a year; "" for none.
func absolute(t time.Time, p int) string {
	other := t.Year() != dateNow().Year()
	switch {
	case p == noDate:
		return ""
	case p == yearOnly:
		return cldr(t, "y")
	case p == monthOnly && other:
		return cldr(t, "yMMM")
	case p == monthOnly:
		return cldr(t, "MMM")
	case other:
		return cldr(t, "yMMMd")
	}
	return cldr(t, "MMMd")
}

// dateText is a date in the absolute form: 11月4日, 2025年11月4日.
func dateText(v *string) string { return absolute(dated(v)) }

// recentText is a date in Home's recent lists: 今天, 昨天, else the absolute form.
func recentText(v *string) string { return recent(dated(v)) }

func recent(t time.Time, p int) string {
	if p >= dayOnly {
		switch daysSince(t, dateNow()) {
		case 0:
			return locales.DateToday()
		case 1:
			return locales.DateYesterday()
		}
	}
	return absolute(t, p)
}

// updatedText is a modified date: 更新于 11月4日.
func updatedText(v *string) string {
	if d := dateText(v); d != "" {
		return locales.DateUpdated(d)
	}
	return ""
}

// articleDate is an article's date: its published one, else 更新于 its
// modified one, else nothing. home writes Home's 今天 and 昨天.
func articleDate(art *library.Article, home bool) string {
	if t, p := dated(art.PublishedAt); p != noDate {
		if home {
			return recent(t, p)
		}
		return absolute(t, p)
	}
	t, p := dated(art.ModifiedAt)
	if home && p >= dayOnly {
		switch daysSince(t, dateNow()) {
		case 0:
			return locales.DateUpdatedToday()
		case 1:
			return locales.DateUpdatedYesterday()
		}
	}
	return updatedText(art.ModifiedAt)
}

// refreshText is when the subscriptions were last read: 今天 09:37 刷新,
// 昨天 21:05 刷新, then 11月4日刷新; "" for never.
func refreshText(v *string) string {
	t, p := dated(v)
	if p == noDate {
		return ""
	}
	hm := cldr(t, "jm")
	switch daysSince(t, dateNow()) {
	case 0:
		return locales.DateRefreshedToday(hm)
	case 1:
		return locales.DateRefreshedYesterday(hm)
	}
	return locales.DateRefreshed(absolute(t, dayOnly))
}

// groupName names a group of the author page's time rail: its month
// (11月), or its year (2025) when the rail goes by years.
func groupName(t time.Time, byYear bool) string {
	if byYear {
		return strconv.Itoa(t.Year())
	}
	return cldr(t, "MMM")
}
