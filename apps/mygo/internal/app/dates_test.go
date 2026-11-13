package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"

	"trie/internal/library"
)

// pinNow writes dates as on 2026-11-13, the fixtures' year, until t ends.
func pinNow(t *testing.T) {
	t.Cleanup(func() { dateNow = time.Now })
	dateNow = func() time.Time { return time.Date(2026, 11, 13, 10, 0, 0, 0, time.Local) }
}

func TestDates(t *testing.T) {
	t.Cleanup(func() {
		i18n.SetLocaleFunc(func() string { return "zh-Hans" })
		hourCycle, dateNow = 0, time.Now
	})
	at := func(y int, m time.Month, d, h, min int) *string {
		s := time.Date(y, m, d, h, min, 0, 0, time.Local).Format(time.RFC3339)
		return &s
	}
	str := func(s string) *string { return &s }
	art := func(pub, mod *string) *library.Article { return &library.Article{PublishedAt: pub, ModifiedAt: mod} }
	newYear := time.Date(2027, 1, 1, 0, 30, 0, 0, time.Local)
	for _, tc := range []struct {
		locale string
		hc     int
		now    time.Time // zero: 2026-11-13 10:00
		got    func() string
		want   string
	}{
		// Home's recent form: two named days, then the absolute form.
		{got: func() string { return recentText(str("2026-11-13")) }, want: "今天"},
		{got: func() string { return recentText(at(2026, 11, 13, 23, 59)) }, want: "今天"},
		{got: func() string { return recentText(at(2026, 11, 14, 8, 0)) }, want: "今天"}, // ahead of the clock
		{got: func() string { return recentText(at(2026, 11, 12, 0, 1)) }, want: "昨天"},
		{got: func() string { return recentText(str("2026-11-11")) }, want: "11月11日"},
		{got: func() string { return recentText(str("2026-11-07")) }, want: "11月7日"},
		{got: func() string { return recentText(str("2026-11")) }, want: "11月"},
		{got: func() string { return recentText(nil) }, want: ""},
		// The absolute form.
		{got: func() string { return dateText(str("2026-11-13")) }, want: "11月13日"},
		{got: func() string { return dateText(str("2026-03-01")) }, want: "3月1日"},
		{got: func() string { return dateText(at(2026, 11, 4, 19, 8)) }, want: "11月4日"},
		{got: func() string { return dateText(str("2025-11-04")) }, want: "2025年11月4日"},
		{got: func() string { return dateText(at(2025, 11, 4, 10, 0)) }, want: "2025年11月4日"},
		{got: func() string { return dateText(str("2026-11")) }, want: "11月"},
		{got: func() string { return dateText(str("2025-11")) }, want: "2025年11月"},
		{got: func() string { return dateText(str("2025")) }, want: "2025年"},
		{got: func() string { return dateText(str("2026")) }, want: "2026年"},
		{got: func() string { return dateText(nil) }, want: ""},
		{got: func() string { return dateText(str("")) }, want: ""},
		{got: func() string { return dateText(str("someday")) }, want: ""},
		{got: func() string { return dateText(str("2026-13-01")) }, want: ""},
		// An article: published, else 更新于 modified, else nothing.
		{got: func() string { return articleDate(art(str("2026-11-01"), str("2026-11-04")), false) }, want: "11月1日"},
		{got: func() string { return articleDate(art(nil, str("2026-11-04")), false) }, want: "更新于 11月4日"},
		{got: func() string { return articleDate(art(str("someday"), str("2025-11-04")), false) }, want: "更新于 2025年11月4日"},
		{got: func() string { return articleDate(art(nil, str("2026-11-13")), true) }, want: "更新于今天"},
		{got: func() string { return articleDate(art(nil, str("2026-11-13")), false) }, want: "更新于 11月13日"},
		{got: func() string { return articleDate(art(str("2026-11-12"), nil), true) }, want: "昨天"},
		{got: func() string { return articleDate(art(nil, nil), true) }, want: ""},
		{got: func() string { return updatedText(nil) }, want: ""},
		// The hover: date and time, the date alone for a date.
		// The refresh line, on the user's clock, across a new year.
		{got: func() string { return refreshText(at(2026, 11, 13, 9, 37)) }, want: "今天 09:37 刷新"},
		{hc: 12, got: func() string { return refreshText(at(2026, 11, 13, 9, 37)) }, want: "今天 上午9:37 刷新"},
		{hc: 24, got: func() string { return refreshText(at(2026, 11, 12, 21, 5)) }, want: "昨天 21:05 刷新"},
		{got: func() string { return refreshText(at(2026, 11, 4, 8, 0)) }, want: "11月4日刷新"},
		{now: newYear, got: func() string { return refreshText(at(2026, 12, 31, 23, 50)) }, want: "昨天 23:50 刷新"},
		{now: newYear, got: func() string { return refreshText(at(2026, 12, 30, 12, 0)) }, want: "2026年12月30日刷新"},
		{now: newYear, got: func() string { return dateText(str("2026-12-30")) }, want: "2026年12月30日"},
		{got: func() string { return refreshText(nil) }, want: ""},
		// The time rail's groups.
		{got: func() string { return groupName(time.Date(2025, 11, 4, 0, 0, 0, 0, time.Local), false) }, want: "11月"},
		{got: func() string { return groupName(time.Date(2025, 11, 4, 0, 0, 0, 0, time.Local), true) }, want: "2025"},
		// English.
		{locale: "en", got: func() string { return recentText(str("2026-11-13")) }, want: "Today"},
		{locale: "en", got: func() string { return recentText(str("2026-11-12")) }, want: "Yesterday"},
		{locale: "en", got: func() string { return dateText(str("2026-11-04")) }, want: "Nov 4"},
		{locale: "en", got: func() string { return dateText(str("2025-11-04")) }, want: "Nov 4, 2025"},
		{locale: "en", got: func() string { return dateText(str("2026-11")) }, want: "Nov"},
		{locale: "en", got: func() string { return dateText(str("2025")) }, want: "2025"},
		{locale: "en", got: func() string { return articleDate(art(nil, str("2026-11-04")), false) }, want: "Updated Nov 4"},
		{locale: "en", got: func() string { return articleDate(art(nil, str("2026-11-13")), true) }, want: "Updated today"},
		{locale: "en", got: func() string { return refreshText(at(2026, 11, 13, 9, 37)) }, want: "Refreshed today at 9:37 AM"},
		{locale: "en", got: func() string { return refreshText(at(2026, 11, 12, 21, 5)) }, want: "Refreshed yesterday at 9:05 PM"},
		{locale: "en", got: func() string { return refreshText(at(2026, 11, 4, 8, 0)) }, want: "Refreshed Nov 4"},
		{locale: "en", now: newYear, got: func() string { return refreshText(at(2026, 12, 30, 12, 0)) }, want: "Refreshed Dec 30, 2026"},
		{locale: "en", got: func() string { return groupName(time.Date(2025, 11, 4, 0, 0, 0, 0, time.Local), false) }, want: "Nov"},
	} {
		locale := "zh-Hans"
		if tc.locale != "" {
			locale = tc.locale
		}
		i18n.SetLocaleFunc(func() string { return locale })
		hourCycle = tc.hc
		now := tc.now
		if now.IsZero() {
			now = time.Date(2026, 11, 13, 10, 0, 0, 0, time.Local)
		}
		dateNow = func() time.Time { return now }
		if got := tc.got(); got != tc.want {
			t.Errorf("%s, hc %d, on %s: got %q, want %q", locale, tc.hc, now.Format("2006-01-02"), got, tc.want)
		}
	}
	if d := daysAgo(nil, dateNow()); d != -1 {
		t.Errorf("daysAgo(nil) = %d, want -1", d)
	}
}
