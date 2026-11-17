package app

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/theme"
)

// A subscribed author's page reads as their journal: everything they wrote,
// newest first, down the reading column, each entry its date, its title,
// its picture and how it opens, hairlines between. Beside it runs a rail of
// what they wrote, one tick an article: the page's scrollbar, the article
// in view an amber pill on it.

// listState is what an author's page keeps: its scroll, the list cursor
// (the entry J and K move, which Tab enters on) and the search.
type listState struct {
	scroll ui.ScrollState
	cursor string
	query  string
	search bool   // the search field is open
	focus  string // the entry J or K moved the cursor to, to focus
	rows   map[string]ui.Rect
	held   bool // the rail is held: its pointer scrubs
}

func (a *App) list(id string) *listState {
	l := a.lists[id]
	if l == nil {
		l = &listState{rows: map[string]ui.Rect{}}
		a.lists[id] = l
	}
	return l
}

// shown is what the page lists: the author's articles under the search.
func (a *App) shown(au *author, query string) []*library.Article {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return au.arts
	}
	var out []*library.Article
	for _, art := range au.arts {
		hay := []string{art.Title, au.src.DisplayName, au.src.Host}
		hay = append(hay, art.Authors...)
		for _, h := range hay {
			if strings.Contains(strings.ToLower(h), q) {
				out = append(out, art)
				break
			}
		}
	}
	return out
}

func (a *App) authorPage(c *ui.Context, id string, live bool) {
	au := a.writer(id)
	if au == nil {
		return
	}
	l := a.list(id)
	p := page{"author", id}
	items := a.shown(au, l.query)
	col := a.column()
	_, vh := c.Size()
	headH := headHeight(vh) + profileFoot
	sc := ui.Scroll(c.Key("scroll")).TrackScroll(&l.scroll).Grow(1).FillWidth().Scrollbars(false)
	view := sc.LayoutBounds()
	sc.Children(func() {
		ui.Column(c).FillWidth().MinHeight(view.H).Children(func() {
			// The band floats over the head's picture, at the column's edges.
			ui.Column(c.Key("band-float")).Absolute().Top(0).Left(0).Right(0).ZIndex(3).AlignItems(ui.Center).PassThrough().Children(func() {
				ui.Column(c).FillWidth().MaxWidth(col+80).Padding(0, 40).PassThrough().Children(func() {
					a.band(c, func() { a.backLink(c, backLabel(a, a.backTo())) }, func() {
						a.searchField(c, &l.query, &l.search, locales.AuthorSearchLabel(), locales.AuthorSearchPlaceholder())
					})
				})
			})
			a.authorHead(c, au, p, live)
			ui.Column(c).FillWidth().MaxWidth(col+80).AlignSelf(ui.Center).Padding(0, 40, 140, 40).Children(func() {
				cursor := l.cursor
				if !containsArticle(items, cursor) && len(items) > 0 {
					cursor = items[0].ID
				}
				// The entries reach 16 past the column, so that the keyboard's ground has room round the words.
				ui.Column(c.Key("entries")).Margin(0, -16).Role(ui.RoleList).Label(locales.AuthorArticles(au.src.DisplayName)).Children(func() {
					for i, art := range items {
						a.entry(c, l, p, art, i == 0, art.ID == cursor, live)
					}
				})
				if len(items) == 0 {
					if strings.TrimSpace(l.query) != "" {
						ui.Column(c).Margin(28, 0, 0, 0).Children(func() {
							ui.Text(c, locales.CommonNoMatchingArticles()).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Muted)
							if a.linkButton(c, "clear", locales.CommonClearSearch(), false, false).Margin(8, 0, 0, 0).Clicked() {
								l.query = ""
							}
						})
					} else if a.exPersonOf(id) == nil {
						// Someone only Explore has, with nothing of theirs read yet, has the page end at its head.
						a.emptyState(c, "mailbox", locales.AuthorEmptyTitle(), locales.AuthorEmptyText(), nil)
					}
				}
			})
		})
	})
	if live {
		// The rail stands for the scrollbar here; it waits under the head
		// until the head has scrolled away.
		a.scrolling = nil
		a.rail(c, l, items, view, max(0, headH+8-railTop-l.scroll.Y))
	}
}

func containsArticle(arts []*library.Article, id string) bool {
	for _, art := range arts {
		if art.ID == id {
			return true
		}
	}
	return false
}

// band is the 52 band every page starts with (ui/page PageBand): something
// on the left, controls on the right; 32 under it.
func (a *App) band(c *ui.Context, left, right func()) {
	ui.Row(c.Key("band")).MinHeight(52).FillWidth().Padding(14, 0, 6, 0).Margin(0, 0, 32, 0).Gap(8).AlignItems(ui.Center).ZIndex(3).Children(func() {
		left()
		ui.Box(c).Grow(1)
		right()
	})
}

// backLink is "‹ 首页": the way back; its glyph sits 8 out so the word keeps
// the page's left edge.
func (a *App) backLink(c *ui.Context, label string) {
	if a.backButton(c, label) {
		a.back(c)
	}
}

// backButton is backLink's look, reporting whether it was chosen: the way
// back out of something other than a page.
func (a *App) backButton(c *ui.Context, label string) bool {
	b := ui.ButtonBase(c.Key("back")).Label(label).Height(28).Margin(0, 0, 0, -8).Padding(0, 10, 0, 6).Gap(4).Radius(6).AlignItems(ui.Center).
		MaxWidth(220).FocusRing(false)
	color, bg := theme.Text2, ui.Transparent
	if b.Hovered() {
		color, bg = theme.Text, theme.Hover
	}
	if a.pressing(b) {
		bg = theme.Selected // pressed, a step up
	}
	b.Background(b.AnimateColor("bg", bg, a.ease(motion.Hover))).TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover)))
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	a.control(b)
	b.Children(func() {
		a.icon(c, "back", 16, false)
		ui.Text(c, label).FontSize(theme.Body).LineHeight(16.0 / 13).SingleLine().Ellipsis("…").Shrink(1)
	})
	return b.Clicked()
}

// searchField is the band's search, folded to its icon until it is wanted
// (ui/fields SearchField collapsed): 224 wide open, gliding over 250 ms.
// query is what is typed, search whether it was asked open.
func (a *App) searchField(c *ui.Context, query *string, search *bool, label, placeholder string) {
	open := *search || *query != ""
	box := ui.Row(c.Key("search")).Height(32).Padding(0, 8).Gap(8).Radius(6).AlignItems(ui.Center).ClipX().Shrink(0)
	w := float32(32)
	if open {
		w = 224
	}
	if a.kbd || a.still(c) {
		box.Width(box.Jump("w", w))
	} else {
		box.Width(box.AnimateTo("w", w, ui.Animation{Duration: motion.State, Ease: motion.Ease}))
	}
	ground, edge, color := ui.Transparent, ui.Transparent, theme.Muted
	if open {
		ground, edge = theme.Field, theme.Line
	} else if box.Hovered() {
		ground, color = theme.Hover, theme.Text2
	}
	box.Background(box.AnimateColor("bg", ground, a.ease(motion.Hover))).InsetShadow(0, 0, 0, 1, box.AnimateColor("edge", edge, a.ease(motion.Hover))).
		TextColor(color)
	a.control(box)
	box.Children(func() {
		if !open {
			b := ui.ButtonBase(c.Key("open")).Label(label).Size(16, 16).FocusRing(false)
			if b.Clicked() {
				*search = true
			}
			ink := theme.Muted
			if box.Hovered() {
				ink = theme.Text2
			}
			b.Children(func() { a.dotIcon(c, b, "search", 16, iconLook{ink: ink}) })
			a.tip(c, b, locales.CommonSearch()+" · ⌘F", false)
			return
		}
		a.icon(c, "search", 16, false)
		in := ui.TextInputBase(c.Key("query"), query).Label(label).Placeholder(placeholder).Grow(1).Height(32).FontSize(theme.Body).
			PlaceholderColor(theme.Muted).CaretColor(theme.Accent).TextColor(theme.Text).AutoFocus()
		if in.Focused() {
			box.FocusRing(true).FocusRingOffset(-2)
		}
		if in.OverlayShortcut(0, ui.KeyEscape) {
			*query, *search = "", false
		}
		if in.FocusLeft() && *query == "" {
			*search = false
		}
	})
}

// authorHead is the head of the journal, a profile: the author's own site's
// first screen as a banner across the content area (else their latest
// picture, else their face as a glow), half the window high and lit, only
// its foot sinking into the window's ground; their portrait standing on its
// foot, half in it; beside the portrait, below the banner, their name and
// their site; the subscription menu at the column's end. Come from the
// sidebar, the face flies out of its chip and the name travels with it;
// going back, they return.
func (a *App) authorHead(c *ui.Context, au *author, p page, live bool) {
	id := au.src.ID
	_, vh := c.Size()
	h, gh, w := headHeight(vh), groundHeight(vh), a.area.W
	col := min(a.column(), w-80)
	left := (w - col) / 2
	g := &favGroup{src: au.src, list: au.arts}
	ui.Box(c.Key("head")).FillWidth().Height(h + profileFoot).Children(func() {
		pic, site, waiting := a.shelfPicture(g, true)
		bright := float32(1)
		if pic != nil {
			bright = pic.lit
		}
		// The picture is drawn taller than the banner and cut to it; the face flying in is not.
		ground := ui.Box(c.Key("ground")).Absolute().Left(0).Right(0).Top(0).Height(h).ClipX().ClipY().IgnoreInput(true).AccessHidden(true)
		if a.crossing(c, live) {
			ground.Cache()
		}
		ground.Children(func() {
			// The foot, where the portrait and the name meet it, softened (a backdrop's blur) across the banner, so
			// that the site's own words there never crowd the name.
			var soft *ui.Bitmap
			if pic != nil && pic.soft != nil && w > 0 {
				soft = pic.pool(w, gh, posOf(site), left, h, 1.6*w, 200, 0.35, 1)
			}
			a.groundView(c, g, pic, site, waiting, gh, bright, a.picFade(c, pic), soft)
			// Under the floating band: dark enough for its grey words on a white site.
			ui.Box(c).Absolute().Left(0).Top(0).Right(0).Height(132).LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
				{Color: theme.Window.Alpha(0.9), At: 0}, {Color: theme.Window.Alpha(0.9), At: 52.0 / 132}, {Color: theme.Window.Alpha(0), At: 1}}})
			// The foot sinks into the window's ground: 0.9 by the name's first line, the ground itself at the edge.
			ui.Box(c).Absolute().Left(0).Right(0).Top(0).Height(h).LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
				{Color: theme.Window.Alpha(0), At: (h - 200) / h}, {Color: theme.Window.Alpha(0.6), At: (h - 96) / h},
				{Color: theme.Window.Alpha(0.9), At: (h - 40) / h}, {Color: theme.Window, At: 1}}})
		})
		// The portrait: the face in a ring of the window's ground, centred on the banner's foot.
		ui.Box(c.Key("portrait")).Absolute().Left(left-portraitRing).Top(h-portrait/2-portraitRing).
			Size(portrait+2*portraitRing, portrait+2*portraitRing).Radius(portrait/2 + portraitRing).Background(theme.Window).Center().AccessHidden(true).
			Children(func() { a.headFace(c, au, p, portrait, live) })
		// Beside it, its foot level with the portrait's: the name and, under it, the site.
		ui.Column(c.Key("words")).Absolute().Left(left + portrait + 20).Top(h - profileRise).Width(col - portrait - 20 - 40).Children(func() {
			name := authorName(c, au.src.DisplayName).Role(ui.RoleHeading).Level(1)
			a.end(name, p, "name:"+id)
			ui.Row(c).Margin(2, 0, 0, 0).FontSize(theme.Body).LineHeight(20.0 / 13).Children(func() { a.siteLink(c, au.src) })
		})
		// The menu at the column's end, level with the name; for someone not subscribed to, the pill that subscribes.
		if who := a.exPersonOf(id); who != nil {
			a.follow(c, who.entry, "head").Absolute().Right(w - left - col).Top(h - profileRise + 4)
			return
		}
		more := a.sourceMenu(c, "more", au.src, false).Absolute().Left(left + col - 24).Top(h - profileRise + 8).TextColor(theme.Muted)
		if more.Hovered() {
			more.Background(theme.Hover).TextColor(theme.Text)
		}
	})
}

// The profile at the head: the portrait 88 across in a ring of 4, centred
// on the banner's foot; the name and site beside it rise profileRise above
// the foot and end level with the portrait's; the head reaches profileFoot
// below the banner, the portrait's lower half and 24 more.
const (
	portrait, portraitRing = 88, 4
	profileRise            = 14
	profileFoot            = portrait/2 + portraitRing + 24
)

// headFace is the face at the head of an author's page, size across, with
// its flight: out of the sidebar chip coming in, back to it going back.
func (a *App) headFace(c *ui.Context, au *author, p page, size float32, live bool) ui.Element {
	id := au.src.ID
	if l := a.left; l != nil && l.page == p && l.move.chip == id {
		// The page left, built on for its exit, keeps the face's place
		// but not the face: the face flies to its chip as itself.
		return ui.Box(c.Key("face-gone")).Size(size, size).Shrink(0)
	}
	f := a.face(c, a.iconOf(au.src), "face", size)
	switch m := a.came; {
	case !live:
		// The page left for an article: the face goes with its page.
	case m != nil && m.to == p && m.depth == motion.Deep:
		// Come from the sidebar: the face flies out of its chip.
		t := m.depth.Crossing().Shared(faceMatch{id})
		t.ScaleContent = true
		f.Transition(a.tr(c, t))
	default:
		// Where the face is, for its chip to take it back from.
		f.Transition(ui.ElementTransition{Match: faceMatch{id}})
	}
	return f
}

// siteLink is the address of an author's site with the arrow of a way out
// of the app, which opens it: muted, both lighting up under the pointer, the
// address underlined.
func (a *App) siteLink(c *ui.Context, src *library.Source) {
	if src.Host == "" {
		return
	}
	if src.HomeURL == "" {
		ui.Text(c, src.Host).TextColor(theme.Muted)
		return
	}
	site := ui.ButtonBase(c.Key("site")).Label(src.Host).Margin(0, -4).Padding(0, 4).Gap(4).Radius(6).AlignItems(ui.Center).FocusRing(false)
	color, mark := theme.Muted, theme.Faint
	if site.Hovered() {
		color, mark = theme.Text, theme.Text2
	}
	if site.FocusVisible() {
		site.FocusRing(true)
	}
	if site.Clicked() {
		a.openExternal(c, src.HomeURL)
	}
	a.control(site)
	site.Children(func() {
		t := ui.Text(c, src.Host).TextColor(site.AnimateColor("fg", color, a.ease(motion.Hover)))
		if site.Hovered() {
			t.Underline().UnderlineOffset(3)
		}
		a.icon(c, "openArrow", 12, false).TextColor(site.AnimateColor("mark", mark, a.ease(motion.Hover)))
	})
	a.tip(c, site, locales.CommonOpenInBrowser(), false)
}

// face is a round author face: the site icon toned for the dark interface,
// or an empty disc, never a letter.
func (a *App) face(c *ui.Context, ic *siteIcon, key string, size float32) ui.Element {
	box := ui.Box(c.Key(key)).Size(size, size).Radius(size/2).Background(theme.Selected).ClipX().ClipY().Shrink(0).
		InsetShadow(0, 0, 0, 1, theme.LineSoft).AccessHidden(true)
	return box.Children(func() {
		if ic == nil {
			return
		}
		pad := float32(0)
		if ic.inset {
			pad = size * 0.12
		}
		ui.Image(c, ic.toned).Fit(ui.Cover).Absolute().Left(pad).Top(pad).Right(pad).Bottom(pad).Radius(size / 2)
	})
}

// entry is one article of the journal: the date it was written, with the
// moon of an article being read and the orbit of one kept; its title; its
// picture; and how it opens, three lines of what the feed gave. The pointer
// lays no ground over it; the keyboard's cursor takes the selected ground
// and a 1.5 amber line inside it.
func (a *App) entry(c *ui.Context, l *listState, p page, art *library.Article, first, cursor, live bool) {
	row := ui.Box(c.Key(art.ID)).FillWidth()
	pv := art.Preview
	var pic *favPic
	wants := false
	if row.InViewMargin(600, 0) || a.favs().pics["keep:"+art.ID] != nil {
		pic, wants = a.entryPic(p, art)
	} else {
		if who := a.exPersonOf(p.id); who != nil {
			_, wants = who.works[art.ID]
		}
		wants = wants || pv != nil && pv.HasImage
	}
	pictured := wants && !(pic != nil && pic.done && pic.bmp == nil)
	row.Children(func() {
		if !first {
			ui.Box(c).Absolute().Top(0).Left(16).Right(16).Height(1).Background(theme.LineSoft).IgnoreInput(true)
		}
		b := ui.ButtonBase(c.Key("open")).Label(titleOf(art.Title)).FillWidth().Padding(28, 16).Radius(10).
			FocusRing(false).TabStop(cursor).KeyShortcuts(0, ui.KeyEnter)
		r := b.LayoutBounds()
		if b.Clicked() && live {
			l.cursor = art.ID
			a.open(c, art.ID, r)
		}
		if l.focus == art.ID {
			l.focus = ""
			b.Focus().ScrollIntoView()
		} else if b.Focused() && a.kbd {
			l.cursor = art.ID
		}
		focus := b.FocusVisible() || b.Focused() && a.kbd
		if focus {
			b.Background(theme.Selected).InsetShadow(0, 0, 0, 1.5, theme.Focus)
		}
		l.rows[art.ID] = r
		a.layInto(p, art.ID, r)
		b.Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				ui.Row(c.Key("when")).Gap(8).AlignItems(ui.Center).FontSize(theme.Body).LineHeight(20.0 / 13).FontFeatures(theme.Numbers).Children(func() {
					if d := articleDate(art, false); d != "" {
						ui.Text(c, d).TextColor(theme.Text2).NoWrap()
					}
					a.progress(c, art)
					if art.Favorite {
						a.orbit(c, "kept", 12, true).AccessHidden(false).Label(locales.CommonFavorited())
					}
				})
				t := entryTitle(c, titleOf(art.Title)).Margin(8, 0, 0, 0)
				a.end(t, p, "title:"+art.ID)
				a.looks[art.ID] = titleLook{entryTitle, theme.Subtitle}
				if pictured {
					a.entryPicture(c, pic, row.Hovered())
				}
				if pv != nil && pv.Excerpt != nil && *pv.Excerpt != "" {
					ui.Text(c, *pv.Excerpt).Margin(12, 0, 0, 0).FontSize(theme.Item).LineHeight(1.8).TextColor(theme.Text2).MaxLines(3).Ellipsis("…")
				}
			})
		})
		// Resting on an entry reads its article and lays it out, unseen (warm.go):
		// only an article already in the library, so the pointer alone never
		// fetches a page: nothing is read before the click.
		hover := row.Hovered()
		if hover && !a.kbd && live {
			if a.hover != art.ID {
				a.hover, a.hoverAt = art.ID, c.Now()
				if art.BodyStatus != "not_extracted" {
					a.reading(art.ID)
				}
			}
		} else if a.hover == art.ID && !a.pinned {
			a.hover = ""
		}
		// The favourite, beside the date, shown on hover and on the keyboard's cursor, once the library has the article.
		if a.libArticle(art.ID) == nil {
			return
		}
		fav := a.favButton(c, art, 28, 14).Absolute().Right(10).Top(24).TabStop(false)
		fav.Opacity(fav.AnimateTo("show", map[bool]float32{true: 1, false: 0}[hover || focus || fav.Hovered()], a.ease(motion.Reveal)))
	})
}

// entryTitle is an entry's title: 20, two lines at most.
func entryTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).Font(reader.TitleFont(s)).FontSize(theme.Subtitle).LineHeight(1.4).FontWeight(600).TextColor(theme.Text).Balance().MaxLines(2).Ellipsis("…")
}

// entryPicture is an entry's picture across the column, 5:2, held a little
// above its middle; breathing on its way, fading in once there. It is as
// bright as the picture is (toneOf): a white one rests dimmer and comes up
// while the pointer is on its entry.
func (a *App) entryPicture(c *ui.Context, pic *favPic, lit bool) {
	box := ui.Box(c.Key("picture")).FillWidth().AspectRatio(2.5).Margin(16, 0, 0, 0).Radius(10).ClipX().ClipY().Background(theme.Surface).AccessHidden(true)
	if pic != nil && !pic.done {
		box.Opacity(1 - 0.45*breathe(box.Loop("breathe", 2*motion.Breathe, ui.Linear)))
	}
	box.Children(func() {
		if pic != nil && pic.bmp != nil {
			fade := a.picFade(c, pic)
			picImage(c, pic.bmp, false, fade)
			bright := pic.rest
			if lit {
				bright = pic.lit
			}
			if a.kbd {
				bright = box.Jump("bright", bright)
			} else {
				bright = box.AnimateTo("bright", bright, a.ease(motion.Lean))
			}
			if v := (1 - bright) * fade; v > 0.001 {
				ui.Box(c).Absolute().Fill().Background(theme.Darken).Opacity(v)
			}
		}
		ui.Box(c).Absolute().Fill().Radius(10).InsetShadow(0, 0, 0, 1, theme.LineSoft)
	})
}

// Where the rail sits: from under the head to above the window's foot, its
// ticks at its right edge; groups of ticks a little apart.
const (
	railTop, railBottom = 104, 48
	railWide, railGap   = 160, 14
	railRight           = 20
)

// rail is the page's scrollbar, down from its place by down while the head
// is in view: one tick an article, newest at the top, in
// groups of a month (of a year, when they span more than a year and a half)
// whose first tick is longer and named; the article in view an amber pill
// with its date, gliding as the page scrolls. The pointer lights the tick
// under it, the title beside it; a press goes there, a drag scrubs.
func (a *App) rail(c *ui.Context, l *listState, items []*library.Article, view ui.Rect, down float32) {
	n := len(items)
	if n < 2 || view.H <= 0 {
		return
	}
	days := make([]time.Time, n)
	dated := make([]bool, n)
	var newest, oldest time.Time
	for i, art := range items {
		if ms, ok := articleTime(art); ok {
			days[i], dated[i] = local(ms), true
			if newest.IsZero() || days[i].After(newest) {
				newest = days[i]
			}
			if oldest.IsZero() || days[i].Before(oldest) {
				oldest = days[i]
			}
		}
	}
	byYear := newest.Sub(oldest) > 548*24*time.Hour
	group := func(i int) int {
		if !dated[i] {
			return -1
		}
		if byYear {
			return days[i].Year()
		}
		return days[i].Year()*100 + int(days[i].Month())
	}
	groups := 1
	for i := 1; i < n; i++ {
		if group(i) != group(i-1) {
			groups++
		}
	}
	// ponytail: ticks never come closer than 3; an author of a few hundred articles runs past the foot.
	step := max(3, min(10, (view.H-railTop-railBottom-float32(groups-1)*railGap)/float32(n-1)))
	ys := make([]float32, n)
	starts := make([]bool, n)
	y := railTop + down
	for i := range items {
		if i > 0 && group(i) != group(i-1) {
			y += railGap
		}
		starts[i] = i == 0 || group(i) != group(i-1)
		ys[i], y = y, y+step
	}
	// The article in view: the last whose entry has come up to a third of the
	// view; at the foot of the page, the last one shown whole.
	cur := 0
	for i, art := range items {
		if r := l.rows[art.ID]; r.H > 0 && r.Y <= view.Y+view.H/3 {
			cur = i
		}
	}
	if s := &l.scroll; s.MaxY > 0 && s.Y >= s.MaxY-1 {
		for i, art := range items {
			if r := l.rows[art.ID]; r.H > 0 && r.Y+r.H <= view.Y+view.H {
				cur = i
			}
		}
	}
	strip := ui.Box(c.Key("rail")).Absolute().Top(0).Right(railRight).Width(railWide).Height(view.H).ZIndex(5).AccessHidden(true)
	px, py, over := strip.PointerPosition()
	hov := -1
	if over && px > railWide-48 && py > ys[0]-12 && py < ys[n-1]+12 {
		for i := range ys {
			if hov < 0 || math.Abs(float64(ys[i]-py)) < math.Abs(float64(ys[hov]-py)) {
				hov = i
			}
		}
	}
	// A press goes to the tick under it, gliding; held, it scrubs.
	goTo := func(i int, glide bool) {
		r, s := l.rows[items[i].ID], &l.scroll
		to := max(0, min(s.MaxY, r.Y-view.Y+s.Y-24))
		if glide && !a.still(c) {
			s.ScrollTo(0, to, ui.Smooth)
		} else {
			s.Y = to
		}
	}
	switch {
	case strip.Pressed() && hov >= 0:
		goTo(hov, !l.held)
		l.held = true
	case !strip.Pressed():
		l.held = false
	}
	pill := ys[cur]
	if a.kbd || a.still(c) || l.held {
		pill = strip.Jump("pill", pill)
	} else {
		pill = strip.AnimateTo("pill", pill, motion.Follow)
	}
	strip.Children(func() {
		ui.Box(c.Key("ticks")).Absolute().Fill().Draw(func(pt *ui.Painter, box ui.Rect) {
			hair := 1 / pt.Scale()
			snap := func(v float32) float32 { return float32(math.Round(float64(v*pt.Scale()))) / pt.Scale() }
			right := box.X + box.W
			for i, y := range ys {
				w, ink := float32(6), float32(0.3)
				if starts[i] {
					w, ink = 10, 0.45
				}
				if i == hov {
					w, ink = w+4, 0.9
				}
				pt.Fill(ui.Rect{X: right - w, Y: box.Y + snap(y), W: w, H: hair}, theme.RulerInk.Alpha(ink), 0)
			}
		})
		// Each group is named at its first tick, unless the pill or the name above is there.
		last := float32(-100)
		for i := range ys {
			if !starts[i] || !dated[i] || ys[i]-last < 18 || math.Abs(float64(ys[i]-pill)) < 16 {
				continue
			}
			last = ys[i]
			ui.Text(c.Key("g"+strconv.Itoa(i)), groupName(days[i], byYear)).Absolute().Right(18).Top(ys[i] - 8).FontSize(theme.Label).LineHeight(16.0 / 12).
				TextColor(theme.Muted).FontFeatures(theme.Numbers).NoWrap()
		}
		if day := articleDate(items[cur], false); day != "" {
			ui.Row(c.Key("pill")).Absolute().Right(0).Top(pill-10).Height(20).Padding(0, 8).Radius(10).Background(theme.Accent).AlignItems(ui.Center).
				Children(func() {
					ui.Text(c, day).FontSize(theme.Label).LineHeight(16.0 / 12).FontWeight(600).TextColor(theme.OnAccent).FontFeatures(theme.Numbers).NoWrap()
				})
		}
		if hov >= 0 && !l.held {
			// Up to 300 wide, past the rail's own width (a frame as wide, the tag in it hugging its words); over the
			// names, short of the ticks.
			ui.Row(c.Key("peek")).Absolute().Right(18).Top(ys[hov] - 14).Width(300).Height(28).Justify(ui.End).IgnoreInput(true).Children(func() {
				ui.Row(c).Height(28).Padding(0, 10).Gap(8).Radius(6).AlignItems(ui.Center).Shrink(1).MinWidth(0).
					Background(theme.Elevated).Border(1, theme.Line).Shadow(0, 0, 0, 1, theme.ShadowEdge).Shadow(0, 12, 32, 0, theme.ShadowFloat).
					Children(func() {
						if d := articleDate(items[hov], false); d != "" {
							ui.Text(c, d).FontSize(theme.Label).TextColor(theme.Muted).FontFeatures(theme.Numbers).NoWrap()
						}
						ui.Text(c, titleOf(items[hov].Title)).FontSize(theme.Body).TextColor(theme.Text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
					})
			})
		}
	})
}

// favButton keeps or lets go of an article (RowActions, the reader's fav):
// the orbit closes and lights amber.
func (a *App) favButton(c *ui.Context, art *library.Article, size, orbit float32) ui.Element {
	label := locales.CommonFavorite()
	if art.Favorite {
		label = locales.CommonRemoveFavorite()
	}
	b := ui.ButtonBase(c.Key("fav")).Label(label).Checked(art.Favorite).Role(ui.RoleToggleButton).KeyShortcuts(0, ui.KeyS).
		Size(size, size).Radius(6).Center().FocusRing(false)
	if b.Clicked() {
		a.toggleFav(art)
	}
	bg, fg := ui.Transparent, theme.Muted
	if b.Hovered() {
		bg, fg = theme.Selected, theme.Text
		if size == 32 {
			bg = theme.Hover
		}
	}
	b.Background(b.AnimateColor("bg", bg, a.ease(motion.Hover))).TextColor(fg)
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	b.Children(func() { a.orbitIn(c, "orbit", orbit, art.Favorite, b) })
	a.tip(c, b, label+" · S", false)
	return b
}

func (a *App) toggleFav(art *library.Article) {
	a.mutate(library.ToggleFavorite{ID: art.ID}, nil)
}

// moon is reading progress: a ring and what is left to
// read, lit on the left; an empty ring at the end.
func (a *App) moon(c *ui.Context, size, lit float32, label string) ui.Element {
	return drawMoon(ui.Box(c), size, lit, label)
}

// moonBack is a list's moon of the article whose reader was just left: it
// goes from where it was as the article was opened to where it was left,
// once its row is built.
type moonBack struct {
	id      string
	from    float32
	started bool
}

// moonOf is art's moon in a list, 10 across: going back from its reader by
// the pointer it wanes from where it was as the article was opened to where
// the reader left it, along the one curve, under less motion too, as what
// is left to read changes rather than moves; from the keys it is simply
// there.
func (a *App) moonOf(c *ui.Context, art *library.Article, label string) ui.Element {
	box, lit := ui.Box(c.Key("moon")), litOf(art)
	if t := a.moonBack; t != nil && t.id == art.ID && !a.warming {
		if !t.started {
			t.started = true
			box.Jump("lit", t.from)
		}
		lit = box.AnimateTo("lit", lit, ui.Animation{Duration: motion.MoonWane, Ease: motion.Ease, NonMotion: true})
		if box.Settled("lit") {
			a.moonBack = nil
		}
	}
	return drawMoon(box, 10, lit, label)
}

func drawMoon(box ui.Element, size, lit float32, label string) ui.Element {
	return box.Size(size, size).Shrink(0).Label(label).Draw(func(pt *ui.Painter, r ui.Rect) {
		cx, cy, rad := r.X+size/2, r.Y+size/2, size/2-1
		var ring ui.Path
		ring.Circle(cx, cy, rad)
		pt.StrokePath(&ring, 1, theme.MoonRing)
		if lit > 0.005 {
			pt.FillPath(moonPath(cx, cy, rad, lit), theme.Moon)
		}
	})
}

// moonPath is the moon's outline, waning: the left limb, and the
// terminator, an ellipse half rx wide, bulging right past half lit.
func moonPath(cx, cy, r, lit float32) *ui.Path {
	var p ui.Path
	const k = 0.5523
	p.MoveTo(cx, cy-r)
	// The limb: top to bottom round the left.
	p.CubeTo(cx-k*r, cy-r, cx-r, cy-k*r, cx-r, cy)
	p.CubeTo(cx-r, cy+k*r, cx-k*r, cy+r, cx, cy+r)
	if lit >= 0.995 {
		p.CubeTo(cx+k*r, cy+r, cx+r, cy+k*r, cx+r, cy)
		p.CubeTo(cx+r, cy-k*r, cx+k*r, cy-r, cx, cy-r)
		return p.Close()
	}
	rx := r * float32(math.Abs(float64(1-2*lit)))
	dir := float32(-1) // under half: the terminator bulges left, a crescent
	if lit > 0.5 {
		dir = 1
	}
	p.CubeTo(cx+dir*k*rx, cy+r, cx+dir*rx, cy+k*r, cx+dir*rx, cy)
	p.CubeTo(cx+dir*rx, cy-k*r, cx+dir*k*rx, cy-r, cx, cy-r)
	return p.Close()
}

// orbit is the favourite in the favorites icon's dots (DESIGN.md 收藏用轨道):
// a centre and an arc not closed; kept, the arc closes and it all turns
// amber, and the satellite comes round to the top 60 ms behind the arc's
// end, neither going past (460 ms; the colour over 200). Under less
// motion it simply changes, its colour fading.
func (a *App) orbit(c *ui.Context, key string, size float32, kept bool) ui.Element {
	return a.orbitIn(c, key, size, kept, ui.Element{})
}

// orbitIn is orbit in button b, whose pointer and presses light its dots.
func (a *App) orbitIn(c *ui.Context, key string, size float32, kept bool, b ui.Element) ui.Element {
	box := ui.Box(c.Key(key)).Size(size, size).Shrink(0).AccessHidden(true)
	target := float32(0)
	if kept {
		target = 1
	}
	var k, round float32
	if a.kbd || a.still(c) {
		k, round = box.Jump("k", target), box.Jump("round", target)
	} else {
		k = box.AnimateTo("k", target, ui.Animation{Duration: motion.Orbit, Ease: motion.Ease})
		round = box.AnimateTo("round", target, motion.Satellite)
	}
	color := theme.Muted
	if kept {
		color = theme.Star
	}
	dots := orbitDots(size, k, round, kept, box.AnimateColor("ink", color, a.ease(motion.Light)))
	light, _ := a.lightOf(c, b, box)
	return box.Draw(func(p *ui.Painter, r ui.Rect) { paintDots(p, r, dots, light, 0) })
}

// emptyState is ui/states EmptyState centred on the page: the pictogram, a
// short title, one sentence, and a way on when there is one.
func (a *App) emptyState(c *ui.Context, art, title, text string, action func()) {
	_, h := c.Size()
	ui.Column(c.Key("empty")).AlignItems(ui.Center).MaxWidth(360).AlignSelf(ui.Center).Margin(h*0.2, 0, 0, 0).Children(func() {
		if art != "" { // one line alone has no mark (没有匹配的收藏)
			a.pictogram(c, art).Margin(0, 0, 24, 0)
		}
		if title != "" {
			ui.Text(c, title).FontSize(theme.Item).LineHeight(22.0/15).FontWeight(600).TextColor(theme.Text).Margin(0, 0, 8, 0).TextAlign(ui.Center)
		}
		ui.Text(c, text).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Muted).TextAlign(ui.Center).Pretty()
		if action != nil {
			ui.Row(c).Margin(12, 0, 0, 0).Children(action)
		}
	})
}
