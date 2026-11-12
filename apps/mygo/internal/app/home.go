package app

import (
	"fmt"
	"image"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/motion"
	"trie/internal/theme"
	"trie/internal/web"
)

// 首页: an overview that scrolls. Today's design
// example first, large; then what the aggregators rank highest, as a
// ranking; then the newest piece of each author followed, three across;
// then a few writers not followed yet, passing by slowly, one sentence
// each. Each kind has its own form, so none needs a heading. A search turns
// the page into one line per match.

// homeState is what 首页 keeps between frames.
type homeState struct {
	scroll   ui.ScrollState
	query    string
	search   bool
	cursor   string               // the article J and K are on
	focus    string               // what takes the focus in the next build: an article, a post's reader, "stranger:<entry ID>"
	seen     map[string]bool      // the authors followed as the page came, by source ID
	came     *move                // the move that brought the page, which seen is for
	leaving  map[string]time.Time // writers subscribed from the passing line, folding out of it, since when
	gone     map[string]bool      // and folded out, off the line while subscribed
	faces    map[string]*siteIcon // the catalog's icons, toned as the sidebar's
	street   float32              // how far the passing line has walked, in DIPs
	streetAt time.Time            // when it last walked
	follows  float32              // how tall the people followed were, the frame before
	passing  []string             // the writers passing by, as the build before drew them
	asked    map[string]bool      // writers whose subscription was asked for from the line
	shows    map[string]titleLook // how the page draws the titles it shows (rowLook)
	columns  map[string]ui.Rect   // where the people followed are laid out, by article: Press "read" opens one
	resumed  string               // the article last opened from 接着读: its title is that control's
	onRow    bool                 // the focus was on a column or a line, the build before

	// What the page draws from, kept until the library or the catalog changes.
	lib      [3]any             // the library they were read from: its revision, articles and sources
	all      []*library.Article // the subscribed articles, newest first
	subs     map[string]string  // which source subscribes to a catalog entry
	cat      [3]any             // the catalog and the day they were read from
	example  *homeItem
	trending []homeItem
	passers  []homeItem
}

func (a *App) homeSt() *homeState {
	if a.home == nil {
		a.home = &homeState{leaving: map[string]time.Time{}, gone: map[string]bool{}, faces: map[string]*siteIcon{},
			shows: map[string]titleLook{}, asked: map[string]bool{}, columns: map[string]ui.Rect{}}
	}
	return a.home
}

// Where 流行 comes from: the link aggregators and publications Explore reads, in the order their feeds rank them.
var trendingFrom = []string{"Hacker News", "Lobsters", "UX Collective", "Latent Space"}

var (
	galleryShelf = regexp.MustCompile(`画廊`)
	// Shared hosting gives every writer their own subdomain; anywhere else, one domain is one person.
	sharedHost = regexp.MustCompile(`(^|\.)(github\.io|substack\.com|medium\.com|wordpress\.com|blogspot\.com|netlify\.app|vercel\.app|pages\.dev|bearblog\.dev)$`)
	sentences  = regexp.MustCompile(`[。！？!?]|\.\s`)
	wholeEnd   = regexp.MustCompile(`[。！？!?.…」”"]$`)
)

// homeItem is a post from the catalog the page shows, with its writer and,
// passing by, one sentence of it.
type homeItem struct {
	entry *explore.Entry
	post  explore.Post
	index int    // a gallery's work: which
	said  string // a passing writer's sentence
}

// localMidnight is the start of t's day here.
func localMidnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// daysAgo is the whole days between the day it is dated and today, here;
// -1 for no date.
func daysAgo(v *string, now time.Time) int {
	if v == nil || *v == "" {
		return -1
	}
	ms, ok := web.JSDateParse(*v)
	if !ok {
		return -1
	}
	d := localMidnight(now).Sub(localMidnight(local(ms)))
	return max(0, int(math.Round(d.Hours()/24)))
}

// homeWhen writes a date as 首页 does: 今天, 昨天, 3 天前, then the date.
func homeWhen(v *string, now time.Time) string {
	switch d := daysAgo(v, now); {
	case d == 0:
		return "今天"
	case d == 1:
		return "昨天"
	case d > 1 && d < 7:
		return fmt.Sprintf("%d 天前", d)
	}
	return dateLabel(v)
}

// refreshedLabel is when the subscriptions were last read.
func refreshedLabel(at *string, now time.Time) string {
	if at == nil {
		return ""
	}
	ms, ok := web.JSDateParse(*at)
	if !ok {
		return ""
	}
	t := local(ms)
	days := int(math.Round(localMidnight(now).Sub(localMidnight(t)).Hours() / 24))
	hm := fmt.Sprintf("%d:%02d", t.Hour(), t.Minute())
	switch {
	case days <= 0:
		return "今天 " + hm + " 刷新"
	case days == 1:
		return "昨天 " + hm + " 刷新"
	}
	return fmt.Sprintf("%d月%d日刷新", t.Month(), t.Day())
}

// homeItems are the subscribed articles the search lets through, newest first.
func (a *App) homeItems(query string) []*library.Article {
	hs := a.homeSt()
	var first *library.Article
	if len(a.lib.Articles) > 0 {
		first = &a.lib.Articles[0]
	}
	if key := [3]any{a.lib.Revision, first, len(a.lib.Sources)}; hs.lib != key {
		hs.lib, hs.all, hs.subs = key, nil, map[string]string{}
		for i := range a.lib.Articles {
			if art := &a.lib.Articles[i]; slices.ContainsFunc(memberships(art), func(id string) bool { return a.writer(id) != nil }) {
				hs.all = append(hs.all, art)
			}
		}
		slices.SortStableFunc(hs.all, byDate)
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return hs.all
	}
	var out []*library.Article
	for _, art := range hs.all {
		if a.favMatches(art, q) {
			out = append(out, art)
		}
	}
	return out
}

// subscribedTo is subscribedSource, kept until the library changes.
func (a *App) subscribedTo(e *explore.Entry) string {
	hs := a.homeSt()
	a.homeItems("") // the library the answers hold for
	id, ok := hs.subs[e.ID]
	if !ok {
		id = a.subscribedSource(e)
		hs.subs[e.ID] = id
	}
	return id
}

// catalog reads today's example, the ranking and the writers passing by
// from Explore's entries, once a day and when the entries change.
func (a *App) catalog(entries []explore.Entry, now time.Time) {
	hs := a.homeSt()
	var first *explore.Entry
	if len(entries) > 0 {
		first = &entries[0]
	}
	y, m, d := now.Date()
	if key := [3]any{first, len(entries), y*10000 + int(m)*100 + d}; hs.cat != key {
		hs.cat = key
		hs.example, hs.trending, hs.passers = exampleOf(entries), trendingOf(entries), passersOf(entries, now)
	}
}

// followsOf is each followed author's newest piece, newest first.
func followsOf(items []*library.Article) []*library.Article {
	seen := map[string]bool{}
	var out []*library.Article
	for _, art := range items {
		if !seen[art.SourceID] {
			seen[art.SourceID] = true
			out = append(out, art)
		}
	}
	return out
}

// exampleOf is today's design example: the newest work any gallery featured.
func exampleOf(entries []explore.Entry) *homeItem {
	var best *homeItem
	for i := range entries {
		e := &entries[i]
		if !galleryShelf.MatchString(e.Section) {
			continue
		}
		for j, w := range e.Works {
			if best == nil || ptrStr(w.PublishedAt) > ptrStr(best.post.PublishedAt) {
				best = &homeItem{entry: e, post: w, index: j}
			}
		}
	}
	return best
}

func ptrStr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// trendingOf is what the aggregators rank highest: their first picks,
// taken in turn, five in all.
func trendingOf(entries []explore.Entry) []homeItem {
	queues := make([][]homeItem, len(trendingFrom))
	for k, name := range trendingFrom {
		for i := range entries {
			if e := &entries[i]; e.Section == name && len(e.Posts) > 0 {
				queues[k] = append(queues[k], homeItem{entry: e, post: e.Posts[0]})
			}
		}
	}
	var out []homeItem
	for i := 0; len(out) < 5; i++ {
		more := false
		for _, q := range queues {
			if i < len(q) {
				more = true
				if len(out) < 5 {
					out = append(out, q[i])
				}
			}
		}
		if !more {
			break
		}
	}
	return out
}

// seeded is FNV-1a over the text's UTF-16 units: the day's draw stays put
// all day.
func seeded(text string) uint32 {
	h := uint32(2166136261)
	for _, r := range text {
		if r >= 0x10000 {
			r = 0xD800 + (r-0x10000)>>10 // charCodeAt(0) of a code point past the BMP: its first half
		}
		h ^= uint32(r)
		h *= 16777619
	}
	return h
}

// personOfHost is one person per domain, but per subdomain on shared hosting.
func personOfHost(host string) string {
	h := strings.TrimPrefix(strings.ToLower(host), "www.")
	if sharedHost.MatchString(h) {
		return h
	}
	parts := strings.Split(h, ".")
	return strings.Join(parts[max(0, len(parts)-2):], ".")
}

// passersOf are the catalog's writers who wrote in the last two weeks and
// have a whole sentence to be met by, in the day's draw.
func passersOf(entries []explore.Entry, now time.Time) []homeItem {
	day := now.Format("Mon Jan 02 2006") // Date.toDateString
	var fresh []*explore.Entry
	for i := range entries {
		e := &entries[i]
		if e.Kind != "blog" || slices.Contains(trendingFrom, e.Section) || len(e.Posts) == 0 || e.Posts[0].Excerpt == "" {
			continue
		}
		if d := daysAgo(e.Posts[0].PublishedAt, now); d < 0 || d > 14 {
			continue
		}
		fresh = append(fresh, e)
	}
	slices.SortStableFunc(fresh, func(x, y *explore.Entry) int {
		sx, sy := seeded(day+x.ID), seeded(day+y.ID)
		switch {
		case sx < sy:
			return -1
		case sx > sy:
			return 1
		}
		return 0
	})
	var out []homeItem
	for _, e := range fresh {
		if said := sentenceOf(e); said != "" {
			out = append(out, homeItem{entry: e, post: e.Posts[0], said: said})
		}
	}
	return out
}

// strangersOf are a few writers passing by, not followed: one per person,
// ten at most.
func strangersOf(passers []homeItem, followed func(*explore.Entry) bool) []homeItem {
	people := map[string]bool{}
	var out []homeItem
	for _, it := range passers {
		if len(out) >= 10 || followed(it.entry) || people[personOfHost(it.entry.Host)] {
			continue
		}
		people[personOfHost(it.entry.Host)] = true
		out = append(out, it)
	}
	return out
}

// sentenceOf is one whole sentence from the opening of the latest post, to
// meet its writer by: not a line the writer repeats atop every post, not a
// fragment.
func sentenceOf(e *explore.Entry) string {
	split := func(text string) []string {
		text = strings.Join(strings.Fields(text), " ")
		var out []string
		start := 0
		for _, m := range sentences.FindAllStringIndex(text, -1) {
			out = append(out, strings.TrimSpace(text[start:m[1]]))
			start = m[1]
		}
		out = append(out, strings.TrimSpace(text[start:]))
		return slices.DeleteFunc(out, func(s string) bool { return s == "" })
	}
	elsewhere := map[string]bool{}
	for _, p := range e.Posts[1:] {
		for _, s := range split(p.Excerpt) {
			elsewhere[s] = true
		}
	}
	var cands []string
	for _, s := range split(e.Posts[0].Excerpt) {
		if !elsewhere[s] {
			cands = append(cands, s)
		}
	}
	one := ""
	for _, s := range cands {
		if n := utf8.RuneCountInString(s); wholeEnd.MatchString(s) && n >= 16 && n <= 110 {
			one = s
			break
		}
	}
	if one == "" {
		for _, s := range cands {
			if utf8.RuneCountInString(s) >= 24 {
				one = s
				break
			}
		}
	}
	if r := []rune(one); len(r) > 110 {
		one = string(r[:108]) + "…"
	}
	return one
}

// mark is a catalog site's icon, toned as the sidebar's faces, or, without
// one, a dark disc quieter than the words beside it.
func (a *App) mark(c *ui.Context, e *explore.Entry, size float32) ui.Element {
	hs := a.homeSt()
	ic, ok := hs.faces[e.ID]
	if !ok && e.Icon != nil {
		if img := dataImage(*e.Icon); img != nil && !img.Bounds().Empty() {
			ic = toneIcon(img)
		}
		hs.faces[e.ID] = ic
	}
	if ic == nil {
		return ui.Box(c).Size(size, size).Radius(size/2).Background(theme.Selected).InsetShadow(0, 0, 0, 1, theme.LineSoft).Shrink(0).AccessHidden(true)
	}
	return a.face(c, ic, "mark", size)
}

// homePage builds 首页.
func (a *App) homePage(c *ui.Context, live bool) {
	hs := a.homeSt()
	ex := a.ex
	if live && !ex.loaded && !ex.loading && ex.failed == "" && a.opt.Explore != nil {
		a.loadExplore()
	}
	now := c.Now()
	if live {
		clear(hs.shows)
		hs.onRow = false
	}
	searching := strings.TrimSpace(hs.query) != ""
	items := a.homeItems(hs.query)
	follows := followsOf(items)
	if live && (hs.seen == nil || hs.came != a.came) {
		// Whoever is followed as the page comes is not new; who joins while it shows comes into focus.
		hs.seen, hs.came = map[string]bool{}, a.came
		for _, art := range follows {
			hs.seen[art.SourceID] = true
		}
	}
	sc := ui.Scroll(c.Key("scroll")).TrackScroll(&hs.scroll).Grow(1).FillWidth().Scrollbars(false)
	view := sc.LayoutBounds()
	if live {
		a.scrolling = &hs.scroll
	}
	band := func() {
		a.band(c, func() {
			status := ""
			switch {
			case searching:
				status = fmt.Sprintf("匹配 %d 篇", len(items))
			case a.refresh.busy:
				status = "正在刷新"
			default:
				status = refreshedLabel(a.lastRefreshed(), now)
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).FontFeatures(theme.Numbers).
				Role(ui.RoleStatus).Children(func() {
				if a.refresh.busy && !searching {
					a.loading(c, 12)
				}
				ui.Text(c, status).NoWrap()
			})
		}, func() {
			if !searching {
				a.resume(c, follows, now, live) // Home's items are the follows
			}
			a.searchField(c, &hs.query, &hs.search, "搜索订阅的文章", "搜索标题、作者")
		})
	}
	sc.Children(func() {
		if searching {
			ui.Column(c).FillWidth().MaxWidth(720).MinHeight(view.H).AlignSelf(ui.Center).Padding(0, 40, 140, 40).Children(func() {
				ui.Text(c, "首页").Role(ui.RoleHeading).Level(1).Absolute().Size(1, 1).ClipX().ClipY().Opacity(0)
				band()
				if len(items) == 0 {
					// The band's 32 below it and the state's 20% above collapse into one, as the column's block margins do.
					ui.Column(c).Margin(-32, 0, 0, 0).Children(func() {
						a.emptyState(c, "", "", "没有匹配的文章", func() {
							if a.linkButton(c, "clear", "清除搜索", false, false).Clicked() {
								hs.query, hs.search = "", false
							}
						})
					})
					return
				}
				ui.Column(c.Key("lines")).Role(ui.RoleList).Children(func() {
					cursor := hs.cursor
					if !slices.ContainsFunc(items, func(x *library.Article) bool { return x.ID == cursor }) {
						cursor = items[0].ID
					}
					for _, art := range items {
						a.homeLine(c, art, art.ID == cursor, live)
					}
				})
			})
			return
		}
		var entries []explore.Entry
		if ex.loaded {
			entries = ex.entries
		}
		a.catalog(entries, now)
		example, trending := hs.example, hs.trending
		strangers := strangersOf(hs.passers, func(e *explore.Entry) bool { return a.followed(c, e, live) })
		if live {
			hs.passing = hs.passing[:0]
			for _, it := range strangers {
				hs.passing = append(hs.passing, it.entry.ID)
			}
		}
		ui.Column(c).FillWidth().MinHeight(view.H).Children(func() {
			ui.Text(c, "首页").Role(ui.RoleHeading).Level(1).Absolute().Size(1, 1).ClipX().ClipY().Opacity(0)
			ui.Column(c.Key("band-float")).Absolute().Top(0).Left(0).Right(0).ZIndex(3).AlignItems(ui.Center).PassThrough().Children(func() {
				frame(c).PassThrough().Children(band)
			})
			if example != nil {
				a.showcase(c, example, now, live)
			}
			g := frame(c)
			if len(strangers) == 0 {
				g.Padding(0, 32, 140, 32)
			}
			if example == nil {
				g.Padding(64, 32, map[bool]float32{true: 140, false: 0}[len(strangers) == 0], 32)
			}
			g.Children(func() {
				// Offline on a first start Explore has nothing yet; Home says so once instead of leaving gaps.
				if ex.failed != "" && !ex.loaded {
					ui.Text(c, "流行和设计例子暂时读不到，联网后会自己更新").FontSize(theme.Body).LineHeight(1.5).TextColor(theme.Muted).Margin(0, 0, 24, 0)
				}
				if len(trending) > 0 {
					a.trending(c, trending, now, live)
				}
				if len(follows) > 0 {
					a.followsView(c, follows, now, view, live)
				}
			})
			if len(strangers) > 0 {
				a.street(c, strangers, now, live)
			}
		})
	})
}

// homeTitle is an article's title as the page shows it, for the title to
// travel from and back to (openAs, rowLook). Opened from 接着读, the title
// is that control's; otherwise its row's.
func (a *App) homeTitle(t ui.Element, id string, look titleLook, resume bool) {
	hs := a.homeSt()
	if resume != (hs.resumed == id) {
		return
	}
	a.end(t, page{kind: "home"}, "title:"+id)
	hs.shows[id] = look
}

// opener is the button of one of Explore's posts: back from the reader by
// the keys, the focus comes back to it.
func (a *App) opener(b ui.Element, url string) {
	if hs := a.homeSt(); hs.focus == trialID(url) {
		hs.focus = ""
		b.Focus()
	}
}

// followed reports whether e is followed, and so off the passing line. A
// writer subscribed from the line folds out of it first; from the keys, or
// under less motion, they simply leave, and from the keys the focus moves on
// to the next writer in the line, so it never drops to the page.
func (a *App) followed(c *ui.Context, e *explore.Entry, live bool) bool {
	hs := a.homeSt()
	if a.subscribedTo(e) == "" {
		delete(hs.gone, e.ID)
		return false
	}
	if _, ok := hs.leaving[e.ID]; ok {
		return false
	}
	at := slices.Index(hs.passing, e.ID)
	if at < 0 || hs.gone[e.ID] || !live {
		return true
	}
	if !a.kbd && !a.still(c) {
		hs.leaving[e.ID] = c.Now()
		return false
	}
	hs.gone[e.ID] = true
	if a.kbd && len(hs.passing) > 1 {
		hs.focus = "stranger:" + hs.passing[(at+1)%len(hs.passing)]
	}
	return true
}

// openFrom opens post it from its button b on 首页, its title t travelling
// to the headline. The title was built before the press was known: it is
// given to the flight here.
func (a *App) openFrom(c *ui.Context, it *homeItem, b, t ui.Element, style func(*ui.Context, string) ui.Element, size float32) {
	a.openPost(c, it.entry, it.post, &trialFrom{title: t.LayoutBounds(), button: b.LayoutBounds(), style: style, size: size})
	a.end(t, page{kind: "home"}, "title:"+trialID(it.post.URL))
}

// lastRefreshed is the latest time any subscription was read.
func (a *App) lastRefreshed() *string {
	var at *string
	var best int64
	for _, au := range a.authors {
		if v := au.src.LastFetchedAt; v != nil {
			if ms, ok := web.JSDateParse(*v); ok && (at == nil || ms > best) {
				at, best = v, ms
			}
		}
	}
	return at
}

// resume is the article left half-read in the last thirty days: one quiet
// control beside the search, the moon showing how much is left.
func (a *App) resume(c *ui.Context, follows []*library.Article, now time.Time, live bool) {
	var art *library.Article
	for _, x := range follows {
		if inProgress(x) && x.ReadingPosition != nil {
			if ms, ok := web.JSDateParse(x.ReadingPosition.UpdatedAt); ok && now.Sub(local(ms)) < 30*24*time.Hour {
				art = x
				break
			}
		}
	}
	// Read to its end, or another left half-read: the title is its column's again.
	if hs := a.homeSt(); live && (art == nil || art.ID != hs.resumed) {
		hs.resumed = ""
	}
	if art == nil {
		return
	}
	pct := percentOf(art)
	b := ui.ButtonBase(c.Key("resume")).Label(fmt.Sprintf("接着读：%s，读到 %d%%", titleOf(art.Title), pct)).Height(32).MaxWidth(320).Padding(0, 12, 0, 10).
		Gap(8).Radius(6).AlignItems(ui.Center).FocusRing(false).TabStop(false).FontSize(theme.Label).LineHeight(16.0 / 12)
	ground, color := theme.Selected, theme.Text2
	if b.Hovered() {
		ground, color = theme.ResumeHover, theme.Text
	}
	b.Background(b.AnimateColor("bg", ground, a.ease(motion.Hover))).TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover)))
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	a.press(b, motion.Pressed)
	a.control(b)
	r := b.LayoutBounds()
	if b.Clicked() {
		a.homeSt().resumed = art.ID
		a.openAs(c, art.ID, r, resumeTitle, theme.Label)
	}
	b.Children(func() {
		a.moonOf(c, art, "")
		t := resumeTitle(c, titleOf(art.Title)).Shrink(1).MinWidth(0)
		a.homeTitle(t, art.ID, titleLook{resumeTitle, theme.Label}, true)
		ui.Text(c, fmt.Sprintf("%d%%", pct)).TextColor(theme.Muted).FontFeatures(theme.Numbers).NoWrap()
	})
	tip := nameOf(a.sourceOf(art)) + fmt.Sprintf(" · 读到 %d%%", pct)
	if m := art.Preview; m != nil && m.Minutes != nil && *m.Minutes > 0 {
		tip += fmt.Sprintf(" · 还剩约 %d 分钟", max(1, int(math.Round(float64(*m.Minutes)*(1-progressOf(art))))))
	}
	a.tip(c, b, tip, false)
}

func resumeTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(theme.Label).LineHeight(16.0 / 12).SingleLine().Ellipsis("…")
}

// showcase is today's design example: the work fills the page's width from
// the top of the window, the band floating on it; its name and source sit
// in the lower-left corner, only that corner darkened and softened, the
// work carrying on beside and behind them; its foot dissolves into the
// window.
func (a *App) showcase(c *ui.Context, it *homeItem, now time.Time, live bool) {
	_, vh := c.Size()
	h, w := clampf(vh-150, 480, 860), a.area.W
	svc, id, index := a.opt.Explore, it.entry.ID, it.index
	pic := a.favPicture(fmt.Sprintf("work:%s:%d", id, index), func() image.Image {
		if svc == nil {
			return nil
		}
		data, _ := svc.Work(bg, id, index)
		return dataImage(data)
	})
	ui.Box(c.Key("show")).FillWidth().Height(h).Role(ui.RoleRegion).Label("今天的设计例子").Children(func() {
		b := ui.ButtonBase(c.Key("open")).Label(it.post.Title).Absolute().Fill().ClipX().ClipY().FocusRing(false)
		a.opener(b, it.post.URL)
		hover := b.Hovered()
		if b.FocusVisible() {
			b.FocusRing(true).FocusRingOffset(-4)
		}
		var title ui.Element
		b.Children(func() {
			fade := a.picFade(c, pic)
			lean := float32(1)
			if hover && !a.still(c) {
				lean = 1.03
			}
			if a.kbd {
				lean = b.Jump("lean", lean)
			} else {
				lean = b.AnimateTo("lean", lean, ui.Animation{Duration: motion.Lean, Ease: motion.Ease})
			}
			ph := ui.Box(c.Key("picture")).Absolute().Fill().Background(theme.Surface).IgnoreInput(true).AccessHidden(true)
			ph.Children(func() {
				if pic != nil && pic.bmp != nil {
					ui.Box(c).Absolute().Fill().TransformOrigin(0.5, 0.5).Scale(lean, lean).Children(func() { picImage(c, pic.bmp, false, fade) })
				}
			})
			// The band's grey words need a little more than the title's 0.9 to read on a white work.
			ui.Box(c).Absolute().Left(0).Top(0).Right(0).Height(144).IgnoreInput(true).LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
				{Color: theme.Window.Alpha(0.94), At: 0}, {Color: theme.Window.Alpha(0.94), At: 56.0 / 144}, {Color: theme.Window.Alpha(0), At: 1}}})
			if pic != nil && pic.soft != nil && w > 0 {
				ui.Box(c.Key("soft")).Absolute().Fill().IgnoreInput(true).AccessHidden(true).Children(func() {
					picImage(c, pic.pool(w, h, posOf(false), 0, h, 0.6*w, 0.4*h, 0.45, 1), false, fade)
				})
			}
			if w > 0 {
				a.shade(c, w, h, 1.1*w, 0.72*h, [][2]float32{{0, 0.9}, {0.42, 0.82}, {1, 0}}).Absolute().Fill()
			}
			ui.Box(c).Absolute().Fill().IgnoreInput(true).LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
				{Color: theme.Window.Alpha(0), At: (h - 64) / h}, {Color: theme.Window, At: 1}}})
			ui.Column(c.Key("words")).Absolute().Left(0).Right(0).Bottom(64).AlignItems(ui.Center).PassThrough().Children(func() {
				frame(c).Gap(12).PassThrough().Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).FontSize(theme.Body).LineHeight(18.0 / 13).TextColor(theme.Text2).FontFeatures(theme.Numbers).Children(func() {
						if p := a.ex.pics["icon:"+it.entry.ID]; p != nil && p.bmp != nil {
							ui.Image(c, p.bmp).Fit(ui.Cover).Size(18, 18).Radius(6)
						}
						ui.Text(c, it.entry.Name)
						ui.Text(c, "·").AccessHidden(true)
						ui.Text(c, homeWhen(it.post.PublishedAt, now))
					})
					title = showTitle(c, it.post.Title)
					a.end(title, page{kind: "home"}, "title:"+trialID(it.post.URL))
				})
			})
		})
		if b.Clicked() && live {
			a.openFrom(c, it, b, title, showTitle, 40)
		}
	})
}

func showTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).MaxWidth(840).FontSize(40).LineHeight(1.15).FontWeight(650).LetterSpacing(-0.01 * 40).TextColor(theme.Text).Balance()
}

// trending is what the aggregators rank highest. The first is the lead and
// needs no number: its own picture, then its source, title and opening, as
// tall as the list beside it. The rest carry small numbers (2–5) hanging in
// the margin, quiet enough to read as an index.
func (a *App) trending(c *ui.Context, items []homeItem, now time.Time, live bool) {
	lead, rest := items[0], items[1:]
	pic := a.leadShot(lead.entry)
	ui.Grid(c.Key("trending")).ColumnTracks(ui.Fr(5), ui.Fr(7)).GapX(64).AlignItems(ui.Stretch).Role(ui.RoleRegion).Label("流行").Children(func() {
		b := ui.ButtonBase(c.Key("lead")).Label(lead.post.Title).Column().Gap(12).AlignItems(ui.Start).Padding(24, 0, 0, 0).Radius(6).
			BorderWidth(1, 0, 0, 0).BorderColor(theme.Line).FocusRing(false)
		a.opener(b, lead.post.URL)
		hover := b.Hovered()
		if b.FocusVisible() {
			b.FocusRing(true)
		}
		var title ui.Element
		b.Children(func() {
			if pic != nil && pic.bmp != nil {
				fade := a.picFade(c, pic)
				lean := float32(1)
				if hover && !a.still(c) {
					lean = 1.03
				}
				lean = b.AnimateTo("lean", lean, ui.Animation{Duration: motion.Lean, Ease: motion.Ease})
				ui.Box(c.Key("picture")).AlignSelf(ui.Stretch).Grow(1).Shrink(1).Basis(0).MinHeight(128).Margin(0, 0, 12, 0).Radius(10).ClipX().ClipY().
					Background(theme.Surface).InsetShadow(0, 0, 0, 1, theme.LineSoft).IgnoreInput(true).AccessHidden(true).Children(func() {
					ui.Box(c).Absolute().Fill().TransformOrigin(0.5, 0.5).Scale(lean, lean).Children(func() {
						img := ui.Image(c, pic.bmp).Fit(ui.Cover).Absolute().Fill()
						if fade < 1 {
							img.Opacity(fade)
						}
					})
				})
			}
			a.homeSource(c, lead, now)
			title = leadTitle(c, lead.post.Title)
			a.end(title, page{kind: "home"}, "title:"+trialID(lead.post.URL))
			if lead.post.Excerpt != "" {
				lines := 4
				if pic != nil && pic.done && pic.bmp == nil {
					lines = 9 // without a picture, more of the opening fills the height
				}
				color := theme.Text2
				if hover {
					color = theme.Text
				}
				ui.Text(c, lead.post.Excerpt).FontSize(theme.Item).LineHeight(1.7).MaxLines(lines).Ellipsis("…").TextColor(b.AnimateColor("ex", color, a.ease(motion.Hover)))
			}
		})
		if b.Clicked() && live {
			a.openFrom(c, &lead, b, title, leadTitle, theme.Headline)
		}
		ui.Column(c.Key("ranks")).BorderWidth(0, 0, 1, 0).BorderColor(theme.Line).Role(ui.RoleList).Children(func() {
			for i, it := range rest {
				ui.Box(c.Key(it.entry.ID)).BorderWidth(1, 0, 0, 0).BorderColor(theme.Line).Children(func() {
					r := ui.ButtonBase(c.Key("open")).Label(it.post.Title).Column().Gap(8).Padding(24, 0).Radius(6).AlignItems(ui.Stretch).FocusRing(false).FillWidth()
					a.opener(r, it.post.URL)
					if r.FocusVisible() {
						r.FocusRing(true)
					}
					var t ui.Element
					r.Children(func() {
						ui.Text(c, fmt.Sprint(i+2)).Absolute().Left(-32).Top(24).Width(16).TextAlign(ui.End).FontSize(theme.Body).LineHeight(25.0 / 13).FontWeight(500).
							TextColor(theme.Muted).FontFeatures(theme.Numbers).AccessHidden(true)
						color := theme.Mag2
						if r.Hovered() {
							color = theme.Text
						}
						t = rankTitle(c, it.post.Title).TextColor(r.AnimateColor("fg", color, a.ease(motion.Hover)))
						a.end(t, page{kind: "home"}, "title:"+trialID(it.post.URL))
						a.homeSource(c, it, now)
					})
					if r.Clicked() && live {
						a.openFrom(c, &it, r, t, rankTitle, 17)
					}
				})
			}
		})
	})
}

func leadTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(theme.Headline).LineHeight(1.25).FontWeight(650).LetterSpacing(-0.01 * 32).TextColor(theme.Text).Balance().MaxLines(4).Ellipsis("…")
}

func rankTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(17).LineHeight(1.45).FontWeight(600).TextColor(theme.Text).MaxLines(2).Ellipsis("…")
}

// leadShot is the lead's picture (loadExploreShot): a writer's latest post's own, a site's first screen.
func (a *App) leadShot(e *explore.Entry) *favPic {
	svc, id := a.opt.Explore, e.ID
	return a.favPicture("shot:"+id, func() image.Image {
		if svc == nil {
			return nil
		}
		data, _ := svc.Shot(bg, id)
		return dataImage(data)
	})
}

// homeSource is where a post comes from: the site's mark and name, its
// shelf when that is another name, and when.
func (a *App) homeSource(c *ui.Context, it homeItem, now time.Time) {
	ui.Row(c.Key("source")).Gap(6).AlignItems(ui.Center).MinWidth(0).MaxWidthPercent(100).ClipX().FontSize(theme.Label).LineHeight(16.0 / 12).
		TextColor(theme.Muted).FontFeatures(theme.Numbers).Children(func() {
		a.mark(c, it.entry, 16)
		ui.Text(c, it.entry.Name).FontWeight(500).TextColor(theme.Text2).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		if it.entry.Section != it.entry.Name {
			ui.Text(c, "·").AccessHidden(true)
			ui.Text(c, it.entry.Section).NoWrap()
		}
		ui.Text(c, "·").AccessHidden(true)
		ui.Text(c, homeWhen(it.post.PublishedAt, now)).NoWrap()
	})
}

// followsView is the people followed: one column each, in the same type and
// hairlines as the ranking above, no boxes. Someone who joins (followed from
// the passing line) comes into focus as the others slide over for them; the
// page keeps whoever is reading further down where they were.
func (a *App) followsView(c *ui.Context, follows []*library.Article, now time.Time, view ui.Rect, live bool) {
	hs := a.homeSt()
	p := page{kind: "home"}
	cursor := hs.cursor
	if !slices.ContainsFunc(follows, func(x *library.Article) bool { return x.ID == cursor }) {
		cursor = follows[0].ID
	}
	// The people sit in a box as tall as they were the frame before: grown,
	// the box grows a frame later, and in that frame the page scrolls on by
	// as much when they ended above the middle of the view, so whoever is
	// reading further down keeps their place on screen.
	box := ui.Box(c.Key("people")).FillWidth().Margin(64, 0, 0, 0)
	defer func() {
		if hs.follows > 0 {
			box.Height(hs.follows)
		}
	}()
	box.Children(func() {
		sec := ui.Grid(c.Key("follows")).ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1)).GapX(48).GapY(48).Role(ui.RoleRegion).Label("关注的人")
		if live {
			was, r := hs.follows, sec.LayoutBounds()
			if grown := r.H - was; was > 0 && (grown > 0.5 || grown < -0.5) && box.LayoutBounds().Y+was < view.Y+view.H/2 {
				hs.scroll.Y += grown
			}
			if r.H > 0 {
				hs.follows = r.H
			}
		}
		sec.Children(func() {
			for _, art := range follows {
				cell := ui.Box(c.Key(art.ID)).MinWidth(0).BorderWidth(1, 0, 0, 0).BorderColor(theme.Line)
				cell.Transition(a.tr(c, ui.ElementTransition{Animation: &motion.Follow, Position: true}))
				if live && !hs.seen[art.SourceID] {
					if !a.still(c) && !a.kbd {
						k := cell.Keyframes("fresh", ramp(motion.State, motion.TravelIn), ui.KeyframeOptions{})
						if k < 1 {
							cell.Opacity(k).Translate(0, 8*(1-k))
							if bl := 3 * (1 - k); bl > 0.05 {
								cell.Blur(bl)
							}
						} else {
							hs.seen[art.SourceID] = true
						}
					} else {
						hs.seen[art.SourceID] = true
					}
				}
				cell.Children(func() {
					b := ui.ButtonBase(c.Key("open")).Label(titleOf(art.Title)).Column().Gap(12).AlignItems(ui.Start).Padding(24, 0, 0, 0).Radius(6).
						FocusRing(false).TabStop(art.ID == cursor).KeyShortcuts(0, ui.KeyEnter).FillWidth()
					r := b.LayoutBounds()
					a.layInto(p, art.ID, r)
					if live {
						hs.columns[art.ID] = r
					}
					if b.Clicked() && live {
						hs.cursor, hs.resumed = art.ID, ""
						a.openAs(c, art.ID, r, followTitle, theme.Subtitle)
					}
					if live && hs.focus == art.ID {
						hs.focus = ""
						b.Focus().ScrollIntoView()
					}
					if b.Focused() {
						hs.onRow = true
						if a.kbd {
							hs.cursor = art.ID
						}
					}
					if b.FocusVisible() {
						b.FocusRing(true).FocusRingOffset(4)
					}
					hover := b.Hovered()
					b.Children(func() {
						src := a.sourceOf(art)
						ui.Row(c).Gap(8).AlignItems(ui.Center).MinWidth(0).MaxWidthPercent(100).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).
							FontFeatures(theme.Numbers).Children(func() {
							a.face(c, a.iconOf(src), "face", 24)
							ui.Text(c, nameOf(src)).FontSize(theme.Body).FontWeight(600).TextColor(theme.Text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
							d := art.PublishedAt
							if d == nil || *d == "" {
								d = art.ModifiedAt
							}
							ui.Text(c, homeWhen(d, now)).NoWrap().Shrink(0)
						})
						a.homeTitle(followTitle(c, titleOf(art.Title)), art.ID, titleLook{followTitle, theme.Subtitle}, false)
						if pv := art.Preview; pv != nil && pv.Excerpt != nil && *pv.Excerpt != "" {
							color := theme.Text2
							if hover {
								color = theme.Text
							}
							ui.Text(c, *pv.Excerpt).FontSize(theme.Item).LineHeight(1.7).MaxLines(3).Ellipsis("…").TextColor(b.AnimateColor("ex", color, a.ease(motion.Hover)))
						}
					})
				})
			}
		})
	})
}

func followTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(theme.Subtitle).LineHeight(1.4).FontWeight(600).TextColor(theme.Text).MaxLines(3).Ellipsis("…").Pretty()
}

// homeLine is a search's match, one line: the author's face and name, the
// title, how far it was read and when.
func (a *App) homeLine(c *ui.Context, art *library.Article, cursor, live bool) {
	hs := a.homeSt()
	p := page{kind: "home"}
	row := ui.Box(c.Key(art.ID)).FillWidth()
	row.Children(func() {
		b := ui.ButtonBase(c.Key("open")).Label(titleOf(art.Title)).Height(40).Margin(0, -12).Padding(0, 12).Gap(12).Radius(10).AlignItems(ui.Center).
			FocusRing(false).TabStop(cursor).KeyShortcuts(0, ui.KeyEnter)
		r := b.LayoutBounds()
		a.layInto(p, art.ID, r)
		if b.Clicked() && live {
			hs.cursor, hs.resumed = art.ID, ""
			a.openAs(c, art.ID, r, lineTitle, theme.Item)
		}
		if live && hs.focus == art.ID {
			hs.focus = ""
			b.Focus().ScrollIntoView()
		}
		if b.Focused() {
			hs.onRow = true
			if a.kbd {
				hs.cursor = art.ID
			}
		}
		if b.FocusVisible() {
			b.Background(theme.Selected).InsetShadow(0, 0, 0, 1.5, theme.Focus)
		}
		hover := b.Hovered()
		b.Children(func() {
			src := a.sourceOf(art)
			a.face(c, a.iconOf(src), "face", 16)
			ui.Text(c, nameOf(src)).Width(104).Shrink(0).FontSize(theme.Label).TextColor(theme.Muted).SingleLine().Ellipsis("…")
			ui.Row(c).Gap(6).Grow(1).Shrink(1).MinWidth(0).AlignItems(ui.Center).Children(func() {
				color := theme.Text2
				if hover {
					color = theme.Text
				}
				t := lineTitle(c, titleOf(art.Title)).Shrink(1).MinWidth(0).TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover)))
				a.homeTitle(t, art.ID, titleLook{lineTitle, theme.Item}, false)
				if art.Favorite {
					a.orbit(c, "badge", 11, true).AccessHidden(false).Label("已收藏")
				}
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).FontSize(theme.Label).TextColor(theme.Muted).FontFeatures(theme.Numbers).Children(func() {
				if inProgress(art) {
					a.moonOf(c, art, fmt.Sprintf("读到 %d%%", percentOf(art)))
				}
				d := art.PublishedAt
				if d == nil || *d == "" {
					d = art.ModifiedAt
				}
				ui.Text(c, dateLabel(d)).NoWrap()
			})
		})
	})
}

func lineTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(theme.Item).LineHeight(22.0 / 15).TextColor(theme.Text).SingleLine().Ellipsis("…")
}

// street is the writers not followed yet, passing by: one slow line crossing
// the sidebar's ground from right to left, out of the window's edge and back
// into it, about 25 a second, only while it is on screen; the pointer or the
// keyboard stops it. Under less motion it stands still, holds each writer
// once, and scrolls sideways by hand.
func (a *App) street(c *ui.Context, strangers []homeItem, now time.Time, live bool) {
	still := a.still(c)
	sec := ui.Column(c.Key("outside")).FillWidth().Margin(64, 0, 0, 0).Padding(64, 0).Background(theme.Sidebar).BorderWidth(1, 0, 0, 0).
		BorderColor(theme.LineSoft).ClipX().Role(ui.RoleRegion).Label("还没关注的博主")
	onScreen := sec.InView()
	sec.Children(func() {
		// The line fades into the ground over 96 at each end: the ground's own colour laid over it,
		// as a mask from transparent to #000 shows it, without a layer of its own every frame.
		ui.Box(c.Key("ends")).FillWidth().Children(func() {
			if still {
				ui.ScrollHorizontal(c.Key("street")).FillWidth().Scrollbars(false).Children(func() {
					for i := range strangers {
						a.stranger(c, &strangers[i], now, true, live)
					}
				})
			} else {
				a.passing(c, strangers, now, onScreen, live)
			}
			for _, end := range []struct {
				angle float32
				left  bool
			}{{90, true}, {270, false}} {
				e := ui.Box(c).Absolute().Top(0).Bottom(0).Width(96).IgnoreInput(true).AccessHidden(true).
					LinearGradient(ui.LinearGradient{Angle: end.angle, Stops: []ui.GradientStop{{Color: theme.Sidebar, At: 0}, {Color: theme.Sidebar.Alpha(0), At: 1}}})
				if end.left {
					e.Left(0)
				} else {
					e.Right(0)
				}
			}
		})
	})
}

// passing is the line walking: one copy of the writers and the same again,
// moved on by a copy's width every 160 s while it is on screen and neither
// the pointer nor the focus holds it.
func (a *App) passing(c *ui.Context, strangers []homeItem, now time.Time, onScreen, live bool) {
	hs := a.homeSt()
	st := ui.Box(c.Key("street")).FillWidth().ClipX()
	held := st.Hovered() || st.FocusWithin() || !onScreen
	// One copy's width: a writer is 400 wide, less while folding out.
	copyW := float32(0)
	for i := range strangers {
		copyW += a.strangerWidth(now, strangers[i].entry.ID)
	}
	// Away from the page, or off screen, it stood: it walks on from where it was.
	if dt := now.Sub(hs.streetAt); !held && copyW > 0 && dt > 0 && dt < 250*time.Millisecond {
		hs.street += float32(dt.Seconds()) * copyW / 160
		for hs.street >= copyW {
			hs.street -= copyW
		}
	}
	hs.streetAt = now
	if !held && live {
		c.AnimationFrame()
	}
	st.Children(func() {
		row := ui.Row(c.Key("passing")).Width(2 * copyW)
		if a.crossing(c, live) {
			// Laid out where it has walked to while the page crosses, rather than moved there: drawn into the
			// crossing's layer, what a move moves comes out scaled by a hair, its clips then cut by masks drawn on
			// the CPU every frame.
			row.Margin(0, 0, 0, -hs.street)
		} else {
			row.Translate(-hs.street, 0)
		}
		row.Children(func() {
			for i := range strangers {
				a.stranger(c, &strangers[i], now, true, live)
			}
			// The same people again, so the line never runs out; hidden from the keyboard and screen readers.
			ui.Row(c.Key("again")).IgnoreInput(true).AccessHidden(true).Children(func() {
				for i := range strangers {
					a.stranger(c, &strangers[i], now, false, false)
				}
			})
		})
	})
}

// strangerWidth is how wide a passing writer is now: 400, closing to nothing
// over 400 ms once subscribed.
func (a *App) strangerWidth(now time.Time, id string) float32 {
	at, ok := a.homeSt().leaving[id]
	if !ok {
		return 400
	}
	return 400 * (1 - motion.Ease(min(1, float32(now.Sub(at))/float32(motion.DialogIn))))
}

// stranger is a writer not followed yet, met first by name and then by one
// sentence of what they wrote this week, the post it is from under it. The
// one thing to do about them sits on their name line: Explore's own
// subscribe pill. Subscribing keeps them: the face on the pill flies to the
// sidebar, the pill says 已订阅, and the card folds out of the line.
func (a *App) stranger(c *ui.Context, it *homeItem, now time.Time, real, live bool) {
	hs := a.homeSt()
	id := it.entry.ID
	w := a.strangerWidth(c.Now(), id)
	card := ui.Box(c.Key(id)).Width(w).Shrink(0).AlignSelf(ui.Start).ClipX()
	if at, ok := hs.leaving[id]; ok {
		k := min(1, float32(c.Now().Sub(at))/float32(motion.DialogIn))
		card.Opacity(1 - motion.Ease(k))
		if bl := 3 * motion.Ease(k); bl > 0.05 {
			card.Blur(bl)
		}
		if k >= 1 && live {
			delete(hs.leaving, id)
			hs.gone[id] = true
		} else {
			c.AnimationFrame()
		}
	}
	card.Children(func() {
		ui.Box(c).Width(400).Padding(0, 32).Children(func() {
			b := ui.ButtonBase(c.Key("open")).Label(it.entry.Name + "：" + it.post.Title).Column().Gap(12).Radius(6).AlignItems(ui.Stretch).FocusRing(false).FillWidth()
			if real && live {
				a.opener(b, it.post.URL)
				if hs.focus == "stranger:"+id {
					hs.focus = ""
					b.Focus()
				}
			}
			if b.FocusVisible() {
				b.FocusRing(true).FocusRingOffset(-2)
			}
			hover := b.Hovered()
			b.Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).MinHeight(32).Padding(0, 112, 0, 0).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).Children(func() {
					a.mark(c, it.entry, 20)
					ui.RichText(c, ui.Span{Text: it.entry.Name, Weight: 500, Color: theme.Text2}, ui.Span{Text: " · " + homeWhen(it.post.PublishedAt, now)}).
						SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
				})
				color := theme.Text2
				if hover {
					color = theme.Text
				}
				ui.Text(c, it.said).FontSize(17).LineHeight(1.85).Pretty().TextColor(b.AnimateColor("said", color, a.ease(motion.Hover)))
				ui.Text(c, it.post.Title).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).SingleLine().Ellipsis("…")
			})
			if b.Clicked() && live {
				a.openPost(c, it.entry, it.post, nil) // no title to travel: the page just crosses
			}
			if real && it.entry.FeedURL != nil && *it.entry.FeedURL != "" {
				a.follow(c, it.entry, "home").Absolute().Top(0).Right(32)
				// A subscription asked for here that did not go through says why, as a notice.
				switch ex := a.ex; {
				case !live:
				case ex.busy == id:
					hs.asked[id] = true
				case hs.asked[id]:
					delete(hs.asked, id)
					if msg := ex.errors[id]; msg != "" {
						delete(ex.errors, id)
						a.fail(msg)
					}
				}
			}
		})
	})
}

// homeKeys are 首页's keys: J, K and the arrows walk
// the people followed (a search, its matches), the first press from outside
// them only bringing the focus to where it was; Enter opens and S keeps what
// it is on, the first of them before any; Esc clears the search.
func (a *App) homeKeys(c *ui.Context) {
	hs := a.homeSt()
	if c.Shortcut(0, ui.KeyEscape) {
		if hs.query != "" {
			hs.query, hs.search = "", false
		}
		return
	}
	items := a.homeItems(hs.query)
	if strings.TrimSpace(hs.query) == "" {
		items = followsOf(items)
	}
	if len(items) == 0 {
		return
	}
	at := max(0, slices.IndexFunc(items, func(x *library.Article) bool { return x.ID == hs.cursor }))
	step := 0
	switch {
	case c.Shortcut(0, ui.KeyJ), c.Shortcut(0, ui.KeyDown):
		step = 1
	case c.Shortcut(0, ui.KeyK), c.Shortcut(0, ui.KeyUp):
		step = -1
	case c.Shortcut(0, ui.KeyS):
		a.toggleFav(items[at])
		return
	case c.Shortcut(0, ui.KeyEnter):
		hs.cursor, hs.resumed = items[at].ID, ""
		a.openAs(c, items[at].ID, ui.Rect{}, followTitle, theme.Subtitle)
		return
	default:
		return
	}
	if hs.onRow {
		at = min(len(items)-1, max(0, at+step))
	}
	hs.cursor, hs.focus = items[at].ID, items[at].ID
}
