package app

import (
	"fmt"
	"image"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/theme"
	"trie/internal/web"
)

// 首页: today, an overview that scrolls and ends. Today's design example
// first, large; then what was left half-read (接着读); then what the
// aggregators rank highest, as a ranking; then the newest piece of each
// author followed, three across. Each kind has its own form, so none needs
// a heading. A search turns the page into one line per match.

// homeState is what 首页 keeps between frames.
type homeState struct {
	scroll   ui.ScrollState
	query    string
	search   bool
	cursor   string               // the article J and K are on
	focus    string               // what takes the focus in the next build: an article, a post's reader
	seen     map[string]bool      // the authors followed as the page came, by source ID
	came     *move                // the move that brought the page, which seen is for
	faces    map[string]*siteIcon // the catalog's icons, toned as the sidebar's
	follows  float32              // how tall the people followed were, the frame before
	shows    map[string]titleLook // how the page draws the titles it shows (rowLook)
	columns  map[string]ui.Rect   // where the people followed are laid out, by article: Press "read" opens one
	resumed  string               // the article last opened from 接着读: its title is that column's
	resuming map[string]bool      // 接着读's articles, true for those whose title is its own (not also a column below)
	stopped  map[string]string    // the words articles were left at, by article and unit (stopped)
	shown    string               // and from the first screen, when it shows a followed article
	onRow    bool                 // the focus was on a column or a line, the build before

	// What the page draws from, kept until the library or the catalog changes.
	lib      [3]any             // the library they were read from: its revision, articles and sources
	all      []*library.Article // the subscribed articles, newest first
	subs     map[string]string  // which source subscribes to a catalog entry
	cat      [3]any             // the catalog and the day they were read from
	example  *homeItem
	trending []homeItem
}

func (a *App) homeSt() *homeState {
	if a.home == nil {
		a.home = &homeState{faces: map[string]*siteIcon{}, shows: map[string]titleLook{}, columns: map[string]ui.Rect{},
			resuming: map[string]bool{}, stopped: map[string]string{}}
	}
	return a.home
}

// Where 流行 comes from: the link aggregators and publications Explore reads, in the order their feeds rank them.
var trendingFrom = []string{"Hacker News", "Lobsters", "UX Collective", "Latent Space"}

var (
	galleryShelf = regexp.MustCompile(`画廊`)
)

// homeItem is a post from the catalog the page shows, with its writer.
type homeItem struct {
	entry *explore.Entry
	post  explore.Post
	index int // a gallery's work: which
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

// catalog reads today's example and the ranking from Explore's entries,
// once a day and when the entries change.
func (a *App) catalog(entries []explore.Entry, now time.Time) {
	hs := a.homeSt()
	var first *explore.Entry
	if len(entries) > 0 {
		first = &entries[0]
	}
	y, m, d := now.Date()
	if key := [3]any{first, len(entries), y*10000 + int(m)*100 + d}; hs.cat != key {
		hs.cat = key
		hs.example, hs.trending = exampleOf(entries), trendingOf(entries)
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

// mark is a catalog site's icon, toned as the sidebar's faces, or, without
// one, its first letter on a quiet disc, as the sidebar shows a source
// without an icon.
func (a *App) mark(c *ui.Context, e *explore.Entry, size float32) ui.Element {
	return a.avatar(c, e.Name, a.markIcon(e), size)
}

// markIcon is a catalog site's icon, toned once; nil for none.
func (a *App) markIcon(e *explore.Entry) *siteIcon {
	hs := a.homeSt()
	ic, ok := hs.faces[e.ID]
	if !ok && e.Icon != nil {
		if img := dataImage(*e.Icon); img != nil && !img.Bounds().Empty() {
			ic = toneIcon(img)
		}
		hs.faces[e.ID] = ic
	}
	return ic
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
				status = locales.HomeMatches(len(items))
			case a.refresh.busy:
				status = locales.CommonRefreshing()
			default:
				status = refreshText(a.lastRefreshed())
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).FontFeatures(theme.Numbers).
				Role(ui.RoleStatus).Children(func() {
				if a.refresh.busy && !searching {
					a.loading(c, 12)
				}
				ui.Text(c, status).NoWrap()
			})
		}, func() {
			a.searchField(c, &hs.query, &hs.search, locales.HomeSearchLabel(), locales.HomeSearchPlaceholder())
		})
	}
	sc.Children(func() {
		if searching {
			ui.Column(c).FillWidth().MaxWidth(720).MinHeight(view.H).AlignSelf(ui.Center).Padding(0, 40, 140, 40).Children(func() {
				ui.Text(c, locales.TabHome()).Role(ui.RoleHeading).Level(1).Absolute().Size(1, 1).ClipX().ClipY().Opacity(0)
				band()
				if len(items) == 0 {
					// The band's 32 below it and the state's 20% above collapse into one, as the column's block margins do.
					ui.Column(c).Margin(-32, 0, 0, 0).Children(func() {
						a.emptyState(c, "", "", locales.CommonNoMatchingArticles(), func() {
							if a.linkButton(c, "clear", locales.CommonClearSearch(), false, false).Clicked() {
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
		resume := a.resumable(now)
		// An article both half-read and its author's newest: its title travels from 接着读 once opened from there.
		clear(hs.resuming)
		for _, art := range resume {
			hs.resuming[art.ID] = !slices.Contains(follows, art)
		}
		ui.Column(c).FillWidth().MinHeight(view.H).Children(func() {
			ui.Text(c, locales.TabHome()).Role(ui.RoleHeading).Level(1).Absolute().Size(1, 1).ClipX().ClipY().Opacity(0)
			ui.Column(c.Key("band-float")).Absolute().Top(0).Left(0).Right(0).ZIndex(3).AlignItems(ui.Center).PassThrough().Children(func() {
				frame(c).PassThrough().Children(band)
			})
			// Without the catalog (offline, or not read yet) the page stands on what is followed, and says nothing of what is missing.
			shown := true
			switch {
			case example != nil:
				a.showcase(c, example, live)
			case ex.loaded || ex.failed != "" || a.opt.Explore == nil:
				shown = a.ownShow(c, follows, live)
			default:
				// The catalog on its way (read from disk, a moment): its place is kept, so the example does not push the page down.
				_, vh := c.Size()
				ph := ui.Box(c.Key("show")).FillWidth().Height(clampf(vh-150, 480, 860)).Background(theme.Surface).AccessHidden(true)
				if !a.still(c) {
					ph.Opacity(1 - 0.45*breathe(ph.Loop("breathe", 2*motion.Breathe, ui.Linear)))
				}
			}
			g := frame(c).Padding(0, 32, 140, 32)
			if !shown {
				g.Padding(64, 32, 140, 32)
			}
			g.Children(func() {
				if len(resume) > 0 {
					a.resumeView(c, resume, live)
				}
				if len(trending) > 0 {
					a.trending(c, trending, live)
				}
				if len(follows) > 0 {
					a.followsView(c, follows, view, live)
				}
			})
		})
	})
}

// homeTitle is an article's title as the page shows it, for the title to
// travel from and back to (openAs, rowLook). Opened from 接着读 ("resume")
// or the first screen ("show"), the title is that control's; otherwise its
// row's ("").
func (a *App) homeTitle(t ui.Element, id string, look titleLook, from string) {
	hs := a.homeSt()
	owner := ""
	switch {
	case id == hs.resumed, hs.resuming[id] && id != hs.shown:
		owner = "resume"
	case id == hs.shown:
		owner = "show"
	}
	if from != owner {
		return
	}
	a.end(t, page{kind: "home"}, "title:"+id)
	hs.shows[id] = look
}

// opener is the button of one of Explore's posts: back from the reader by
// the keys, the focus comes back to it.
func (a *App) opener(b ui.Element, url string) {
	if hs := a.homeSt(); hs.focus == a.postID(url) {
		hs.focus = ""
		b.Focus()
	}
}

// openFrom opens post it from its button b on 首页, its title t travelling
// to the headline. The title was built before the press was known: it is
// given to the flight here.
func (a *App) openFrom(c *ui.Context, it *homeItem, b, t ui.Element, style func(*ui.Context, string) ui.Element, size float32) {
	a.openPost(c, it.entry, it.post, &trialFrom{title: t.LayoutBounds(), button: b.LayoutBounds(), style: style, size: size})
	a.end(t, page{kind: "home"}, "title:"+a.postID(it.post.URL))
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

// showcase is today's design example (show).
func (a *App) showcase(c *ui.Context, it *homeItem, live bool) {
	// By the picture's address, as Explore keys it (workKey).
	svc, src := a.opt.Explore, it.post.Image
	pic := a.favPicture("work:"+src, func() image.Image {
		if svc == nil || src == "" {
			return nil
		}
		return dataImage(svc.Picture(bg, src))
	})
	a.show(c, pic, false, locales.HomeTodaysExample(), it.post.Title, live, func(b ui.Element) { a.opener(b, it.post.URL) }, func() ui.Element {
		a.showSource(c, func() {
			if p := a.ex.pics["icon:"+it.entry.ID]; p != nil && p.bmp != nil {
				ui.Image(c, p.bmp).Fit(ui.Cover).Size(18, 18).Radius(6)
			}
		}, it.entry.Name, recentText(it.post.PublishedAt))
		title := showTitle(c, it.post.Title)
		a.end(title, page{kind: "home"}, "title:"+a.postID(it.post.URL))
		return title
	}, func(b, title ui.Element) { a.openFrom(c, it, b, title, showTitle, 40) })
}

// ownShow is the first screen without Explore's catalog (offline, or before
// it is first read): the newest piece of someone followed that has a
// picture, else the newest one on its writer's own first screen, laid out
// as the design example is. False when there is nothing to stand it on.
func (a *App) ownShow(c *ui.Context, follows []*library.Article, live bool) bool {
	hs := a.homeSt()
	var art *library.Article
	var pic *favPic
	site := false
	for _, x := range follows[:min(6, len(follows))] {
		if x.Preview == nil || !x.Preview.HasImage {
			continue
		}
		p := a.keepPic(x.ID)
		if p == nil || !p.done {
			// Still on its way: the screen waits for it rather than changing its mind once it comes.
			art, pic = x, p
			break
		}
		if p.bmp != nil {
			art, pic = x, p
			break
		}
	}
	if art == nil && len(follows) > 0 {
		art, site = follows[0], true
		pic = a.sitePic(a.sourceOf(art))
		if pic != nil && pic.done && pic.bmp == nil {
			return false
		}
	}
	if art == nil {
		return false
	}
	p := page{kind: "home"}
	title := titleOf(art.Title)
	a.show(c, pic, site, locales.HomeLatest(), title, live, func(b ui.Element) {
		if hs.shown == art.ID {
			a.layInto(p, art.ID, b.LayoutBounds())
			// Back from the keys, the focus comes back here, ahead of the piece's own column further down.
			if live && hs.focus == art.ID {
				hs.focus = ""
				b.Focus()
			}
		}
	}, func() ui.Element {
		src := a.sourceOf(art)
		a.showSource(c, func() { a.avatar(c, nameOf(src), a.iconOf(src), 18) }, nameOf(src), articleDate(art, true))
		t := showTitle(c, title)
		a.homeTitle(t, art.ID, titleLook{showTitle, 40}, "show")
		return t
	}, func(b, t ui.Element) {
		hs.cursor, hs.resumed, hs.shown = art.ID, "", art.ID
		a.openAs(c, art.ID, b.LayoutBounds(), showTitle, 40)
		a.homeTitle(t, art.ID, titleLook{showTitle, 40}, "show") // built before the press was known
	})
	return true
}

// showSource is the line over the first screen's title: a mark, a name, when.
func (a *App) showSource(c *ui.Context, mark func(), name, when string) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).FontSize(theme.Body).LineHeight(18.0 / 13).TextColor(theme.Text2).FontFeatures(theme.Numbers).Children(func() {
		mark()
		ui.Text(c, name)
		if when != "" {
			ui.Text(c, "·").AccessHidden(true)
			ui.Text(c, when)
		}
	})
}

// show is the first screen: a picture filling the page's width from the top
// of the window, the band floating on it, resting as bright as the picture
// allows (toneOf, as 收藏's do) and coming up under the pointer; the words
// sit in the lower-left corner, only that corner darkened and softened, the
// picture carrying on beside and behind them; its foot dissolves into the
// window. words builds the words and gives the title; open opens it.
func (a *App) show(c *ui.Context, pic *favPic, site bool, region, label string, live bool, each func(b ui.Element), words func() ui.Element, open func(b, title ui.Element)) {
	_, vh := c.Size()
	h, w := clampf(vh-150, 480, 860), a.area.W
	ui.Box(c.Key("show")).FillWidth().Height(h).Role(ui.RoleRegion).Label(region).Children(func() {
		b := ui.ButtonBase(c.Key("open")).Label(label).Absolute().Fill().ClipX().ClipY().FocusRing(false)
		each(b)
		hover := b.Hovered()
		if b.FocusVisible() {
			b.FocusRing(true).FocusRingOffset(-4)
		}
		var title ui.Element
		b.Children(func() {
			fade := a.picFade(c, pic)
			lean, bright := float32(1), float32(1)
			if pic != nil && pic.bmp != nil {
				bright = pic.rest
				if hover || b.FocusVisible() {
					bright = pic.lit
				}
			}
			if hover && !a.still(c) {
				lean = 1.03
			}
			if a.kbd {
				lean, bright = b.Jump("lean", lean), b.Jump("bright", bright)
			} else {
				lean = b.AnimateTo("lean", lean, ui.Animation{Duration: motion.Lean, Ease: motion.Ease})
				bright = b.AnimateTo("bright", bright, a.ease(motion.Lean))
			}
			ph := ui.Box(c.Key("picture")).Absolute().Fill().Background(theme.Surface).IgnoreInput(true).AccessHidden(true)
			if (pic == nil || !pic.done) && !a.still(c) {
				ph.Opacity(1 - 0.45*breathe(ph.Loop("breathe", 2*motion.Breathe, ui.Linear)))
			}
			ph.Children(func() {
				if pic == nil || pic.bmp == nil {
					return
				}
				ui.Box(c).Absolute().Fill().TransformOrigin(0.5, 0.5).Scale(lean, lean).Children(func() { picImage(c, pic.bmp, site, fade) })
				if pic.soft != nil && w > 0 {
					ui.Box(c.Key("soft")).Absolute().Fill().Children(func() {
						picImage(c, pic.pool(w, h, posOf(site), 0, h, 0.6*w, 0.4*h, 0.45, 1), site, fade)
					})
				}
				// Dimmed to its brightness, as 收藏's pictures are: a white site never glares out of the dark window.
				if v := (1 - bright) * fade; v > 0.001 {
					ui.Box(c).Absolute().Fill().Background(theme.Darken).Opacity(v)
				}
			})
			// The band's grey words need a little more than the title's 0.9 to read on a white picture.
			ui.Box(c).Absolute().Left(0).Top(0).Right(0).Height(144).IgnoreInput(true).LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
				{Color: theme.Window.Alpha(0.94), At: 0}, {Color: theme.Window.Alpha(0.94), At: 56.0 / 144}, {Color: theme.Window.Alpha(0), At: 1}}})
			if w > 0 {
				a.shade(c, w, h, 1.1*w, 0.72*h, [][2]float32{{0, 0.9}, {0.42, 0.82}, {1, 0}}).Absolute().Fill()
			}
			ui.Box(c).Absolute().Fill().IgnoreInput(true).LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
				{Color: theme.Window.Alpha(0), At: (h - 64) / h}, {Color: theme.Window, At: 1}}})
			ui.Column(c.Key("words")).Absolute().Left(0).Right(0).Bottom(64).AlignItems(ui.Center).PassThrough().Children(func() {
				frame(c).Gap(12).PassThrough().Children(func() { title = words() })
			})
		})
		if b.Clicked() && live {
			open(b, title)
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
func (a *App) trending(c *ui.Context, items []homeItem, live bool) {
	lead, rest := items[0], items[1:]
	pic := a.leadShot(lead.entry)
	ui.Grid(c.Key("trending")).ColumnTracks(ui.Fr(5), ui.Fr(7)).GapX(64).AlignItems(ui.Stretch).Role(ui.RoleRegion).Label(locales.HomePopular()).Children(func() {
		b := ui.ButtonBase(c.Key("lead")).Label(lead.post.Title).Column().Gap(12).AlignItems(ui.Start).Padding(24, 0, 0, 0).Radius(6).
			BorderWidth(1, 0, 0, 0).BorderColor(theme.Line).FocusRing(false)
		a.opener(b, lead.post.URL)
		hover := b.Hovered()
		if b.FocusVisible() {
			b.FocusRing(true)
		}
		var title ui.Element
		b.Children(func() {
			if pic != nil {
				fade := a.picFade(c, pic)
				lean := float32(1)
				if hover && !a.still(c) {
					lean = 1.03
				}
				lean = b.AnimateTo("lean", lean, ui.Animation{Duration: motion.Lean, Ease: motion.Ease})
				slot := ui.Box(c.Key("picture")).AlignSelf(ui.Stretch).Grow(1).Shrink(1).Basis(0).MinHeight(128).Margin(0, 0, 12, 0).Radius(10).ClipX().ClipY().
					Background(theme.Surface).InsetShadow(0, 0, 0, 1, theme.LineSoft).IgnoreInput(true).AccessHidden(true)
				slot.Children(func() {
					ui.Box(c).Absolute().Fill().TransformOrigin(0.5, 0.5).Scale(lean, lean).Children(func() {
						switch {
						case pic.bmp != nil:
							img := ui.Image(c, pic.bmp).Fit(ui.Cover).Absolute().Fill()
							if fade < 1 {
								img.Opacity(fade)
							}
						case pic.done:
							// A post without a picture of its own stands as Explore's writer covers do without one: their
							// quiet grey glow, the site's mark small in the corner.
							if r := slot.LayoutBounds(); r.W > 0 && r.H > 0 {
								ui.Image(c, a.ex.aura(grey, inkCover, r.W, r.H)).Fit(ui.FillBox).Absolute().Fill()
							}
							a.mark(c, lead.entry, 32).Absolute().Left(20).Bottom(20)
						}
					})
				})
			}
			a.homeSource(c, lead)
			title = leadTitle(c, lead.post.Title)
			a.end(title, page{kind: "home"}, "title:"+a.postID(lead.post.URL))
			if lead.post.Excerpt != "" {
				lines := 4
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
						a.end(t, page{kind: "home"}, "title:"+a.postID(it.post.URL))
						a.homeSource(c, it)
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
	return ui.Text(c, s).Font(reader.TitleFont(s)).FontSize(theme.Headline).LineHeight(1.25).FontWeight(650).LetterSpacing(-0.01 * 32).TextColor(theme.Text).Balance().MaxLines(4).Ellipsis("…")
}

func rankTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).Font(reader.TitleFont(s)).FontSize(17).LineHeight(1.45).FontWeight(600).TextColor(theme.Text).MaxLines(2).Ellipsis("…")
}

// leadShot is the lead's picture (loadExploreShot): a writer's latest post's own, a site's first screen.
func (a *App) leadShot(e *explore.Entry) *favPic {
	svc, id := a.opt.Explore, e.ID
	return a.favPicture(shotKey(e), func() image.Image {
		if svc == nil {
			return nil
		}
		data, _ := svc.Shot(bg, id)
		return dataImage(data)
	})
}

// homeSource is where a post comes from: the site's mark and name, its
// shelf when that is another name, and when.
func (a *App) homeSource(c *ui.Context, it homeItem) {
	ui.Row(c.Key("source")).Gap(6).AlignItems(ui.Center).MinWidth(0).MaxWidthPercent(100).ClipX().FontSize(theme.Label).LineHeight(16.0 / 12).
		TextColor(theme.Muted).FontFeatures(theme.Numbers).Children(func() {
		a.mark(c, it.entry, 16)
		ui.Text(c, it.entry.Name).FontWeight(500).TextColor(theme.Text2).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		if it.entry.Section != it.entry.Name {
			ui.Text(c, "·").AccessHidden(true)
			ui.Text(c, it.entry.Section).NoWrap()
		}
		if when := recentText(it.post.PublishedAt); when != "" {
			ui.Text(c, "·").AccessHidden(true)
			ui.Text(c, when).NoWrap()
		}
	})
}

// followsView is the people followed: one column each, in the same type and
// hairlines as the ranking above, no boxes. Someone who joins comes into
// focus as the others slide over for them; the page keeps whoever is
// reading further down where they were.
func (a *App) followsView(c *ui.Context, follows []*library.Article, view ui.Rect, live bool) {
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
		sec := ui.Grid(c.Key("follows")).ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1)).GapX(48).GapY(48).Role(ui.RoleRegion).Label(locales.HomeFollowing())
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
						hs.cursor, hs.resumed, hs.shown = art.ID, "", ""
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
							a.avatar(c, nameOf(src), a.iconOf(src), 24)
							ui.Text(c, nameOf(src)).FontSize(theme.Body).FontWeight(600).TextColor(theme.Text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
							if d := articleDate(art, true); d != "" {
								ui.Text(c, d).NoWrap().Shrink(0)
							}
						})
						a.homeTitle(followTitle(c, titleOf(art.Title)), art.ID, titleLook{followTitle, theme.Subtitle}, "")
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
	return ui.Text(c, s).Font(reader.TitleFont(s)).FontSize(theme.Subtitle).LineHeight(1.4).FontWeight(600).TextColor(theme.Text).MaxLines(3).Ellipsis("…").Pretty()
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
			hs.cursor, hs.resumed, hs.shown = art.ID, "", ""
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
				a.homeTitle(t, art.ID, titleLook{lineTitle, theme.Item}, "")
				if art.Favorite {
					a.orbit(c, "badge", 11, true).AccessHidden(false).Label(locales.CommonFavorited())
				}
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).FontSize(theme.Label).TextColor(theme.Muted).FontFeatures(theme.Numbers).Children(func() {
				if inProgress(art) {
					a.moonOf(c, art, locales.CommonPercentRead(percentOf(art)))
				}
				if d := articleDate(art, false); d != "" {
					ui.Text(c, d).NoWrap()
				}
			})
		})
	})
}

func lineTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).Font(reader.TitleFont(s)).FontSize(theme.Item).LineHeight(22.0 / 15).TextColor(theme.Text).SingleLine().Ellipsis("…")
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
		hs.cursor, hs.resumed, hs.shown = items[at].ID, "", ""
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
