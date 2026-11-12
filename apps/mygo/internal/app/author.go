package app

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/motion"
	"trie/internal/theme"
)

// An author's page (AuthorPage.tsx, author.css): the page band, a card for
// the person, then their chronology by year, each article a row with a
// tear-off date.

// listState is what an author's page keeps: its scroll, the list cursor
// (the row J and K move, which Tab enters on) and the search.
type listState struct {
	scroll ui.ScrollState
	cursor string
	query  string
	search bool   // the search field is open
	focus  string // the row J or K moved the cursor to, to focus
	rows   map[string]ui.Rect
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
	sc := ui.Scroll(c.Key("scroll")).TrackScroll(&l.scroll).Grow(1).FillWidth().Scrollbars(false)
	view := sc.LayoutBounds()
	sc.Children(func() {
		ui.Column(c).FillWidth().MaxWidth(720).MinHeight(view.H).AlignSelf(ui.Center).Padding(0, 40, 140, 40).Children(func() {
			a.band(c, func() { a.backLink(c, "首页") }, func() { a.searchField(c, &l.query, &l.search, "搜索这位作者的文章", "搜索标题") })
			a.authorCard(c, au, p, live)
			cursor := l.cursor
			if !containsArticle(items, cursor) && len(items) > 0 {
				cursor = items[0].ID
			}
			ui.Column(c).Margin(48, 0, 0, 0).Gap(48).Children(func() {
				for _, y := range years(items) {
					ui.Column(c.Key("year:" + y.year)).Role(ui.RoleGroup).Label(y.year + " 年").Children(func() {
						ui.Row(c).Gap(16).AlignItems(ui.Center).Margin(0, 0, 4, 0).Children(func() {
							ui.Text(c, y.year).FontSize(theme.Subtitle).LineHeight(28.0 / 20).FontWeight(600).TextColor(theme.Text2).Role(ui.RoleHeading).Level(2)
							ui.Box(c).Grow(1).Height(1).Background(theme.LineSoft)
							ui.Text(c, strconv.Itoa(len(y.rows))+" 篇").FontSize(theme.Label).TextColor(theme.Muted)
						})
						ui.Column(c).Margin(0, -12, 0, -12).Children(func() {
							for i, art := range y.rows {
								a.articleRow(c, l, p, art, i > 0, art.ID == cursor, view, live)
							}
						})
					})
				}
			})
			if len(items) == 0 {
				if strings.TrimSpace(l.query) != "" {
					ui.Column(c).Margin(32, 0, 0, 0).Children(func() {
						ui.Text(c, "没有匹配的文章").FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Muted)
						if a.linkButton(c, "clear", "清除搜索", false, false).Margin(8, 0, 0, 0).Clicked() {
							l.query = ""
						}
					})
				} else {
					a.emptyState(c, "mailbox", "还没有收到文章", "这位作者发了新文章，会出现在这里", nil)
				}
			}
		})
	})
	if live {
		a.scrolling = &l.scroll
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

type year struct {
	year string
	rows []*library.Article
}

// years groups articles, newest first, by the year they are dated.
func years(arts []*library.Article) []year {
	var out []year
	for _, art := range arts {
		y := "未注明"
		if t, ok := articleTime(art); ok {
			y = strconv.Itoa(local(t).Year())
		}
		if n := len(out); n > 0 && out[n-1].year == y {
			out[n-1].rows = append(out[n-1].rows, art)
		} else {
			out = append(out, year{y, []*library.Article{art}})
		}
	}
	return out
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
	b := ui.ButtonBase(c.Key("back")).Label(label).Height(28).Margin(0, 0, 0, -8).Padding(0, 10, 0, 6).Gap(4).Radius(6).AlignItems(ui.Center).
		MaxWidth(220).FocusRing(false)
	if b.Clicked() {
		a.back(c)
	}
	color, bg := theme.Text2, ui.Transparent
	if b.Hovered() {
		color, bg = theme.Text, theme.Hover
	}
	b.Background(b.AnimateColor("bg", bg, a.ease(motion.Hover))).TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover)))
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	a.press(b, motion.Pressed)
	a.control(b)
	b.Children(func() {
		a.icon(c, "back", 16, false)
		ui.Text(c, label).FontSize(theme.Body).LineHeight(16.0 / 13).SingleLine().Ellipsis("…").Shrink(1)
	})
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
			b.Children(func() { a.icon(c, "search", 16, false) })
			a.tip(c, b, "搜索 · ⌘F", false)
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

// authorCard is the head of an author's page: their face, their name with
// the subscription menu, their own words, and the facts.
func (a *App) authorCard(c *ui.Context, au *author, p page, live bool) {
	id := au.src.ID
	ui.Row(c.Key("card")).Gap(18).AlignItems(ui.Start).Children(func() {
		if l := a.left; l != nil && l.page == p && l.move.chip == id {
			// The page left, built on for its exit, keeps the face's place
			// but not the face: the face flies to its chip as itself.
			ui.Box(c.Key("face-gone")).Size(56, 56).Shrink(0)
		} else {
			f := a.face(c, a.iconOf(au.src), "face", 56).Shadow(0, 0, 0, 1, theme.LineSoft).Shadow(0, 6, 18, 0, theme.ShadowTip)
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
		}
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Padding(2, 0, 0, 0).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				name := authorName(c, au.src.DisplayName).Role(ui.RoleHeading).Level(1).Shrink(1)
				a.end(name, p, "name:"+id)
				more := a.sourceMenu(c, "more", au.src, false).TextColor(theme.Faint)
				if more.Hovered() {
					more.Background(theme.Hover).TextColor(theme.Text2)
				}
			})
			if intro := a.introOf(au.src); intro != "" {
				ui.Text(c, intro).Margin(6, 0, 0, 0).MaxWidth(468).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2).Pretty()
			}
			ui.Row(c).Margin(8, 0, 0, 0).Wrap().AlignItems(ui.Center).FontSize(theme.Label).LineHeight(18.0 / 12).TextColor(theme.Muted).Children(func() {
				site := ui.ButtonBase(c.Key("site")).Label(au.src.Host).Radius(6).FocusRing(false)
				color, line := theme.Text2, theme.Line
				if site.Hovered() {
					color, line = theme.Text, theme.Faint
				}
				if site.Clicked() {
					a.openExternal(c, au.src.HomeURL)
				}
				if site.FocusVisible() {
					site.FocusRing(true)
				}
				site.Children(func() {
					ui.Text(c, au.src.Host).TextColor(site.AnimateColor("fg", color, a.ease(motion.LinkHover))).Underline().UnderlineOffset(3).
						DecorationColor(site.AnimateColor("line", line, a.ease(motion.LinkHover)))
				})
				a.tip(c, site, "在浏览器中打开", false)
				sep(c)
				ui.Text(c, "收录")
				a.count(c, len(au.arts), 18).Margin(0, 3.3)
				ui.Text(c, "篇")
				if au.days >= 0 {
					sep(c)
					ui.Text(c, ago(au.time)+"更新")
				}
			})
		})
	})
}

// introOf is an author's introduction (AuthorPage.tsx intro): their own
// words from the feed first; when it says nothing useful, the line Explore
// has about their site, looked up once.
func (a *App) introOf(src *library.Source) string {
	if v := src.Description.Value; v != nil && strings.TrimSpace(*v) != "" {
		return *v
	}
	if a.opt.Explore == nil || a.warming {
		return a.intros[src.ID]
	}
	if intro, ok := a.intros[src.ID]; ok {
		return intro
	}
	a.intros[src.ID] = ""
	svc, id, s := a.opt.Explore, src.ID, *src
	a.do(func() func() {
		intro := svc.Intro(s)
		return func() { a.intros[id] = intro }
	})
	return a.intros[src.ID]
}

// sep is the facts' quiet dot, 6 either side.
func sep(c *ui.Context) {
	ui.Text(c, "·").Margin(0, 6).Opacity(0.6)
}

// face is a round author face: the site icon toned for the dark interface,
// or an empty disc, never a letter (HomeList Face).
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

// articleRow is a row of the chronology (author.css .arow): the date, the
// moon of an article being read, the title and what the list can say of
// it, its picture. The pointer lays no ground over it; the keyboard's
// cursor takes the selected ground and a 1.5 amber line inside it.
func (a *App) articleRow(c *ui.Context, l *listState, p page, art *library.Article, rule, cursor bool, view ui.Rect, live bool) {
	row := ui.Box(c.Key(art.ID)).Radius(10).FillWidth()
	pv := art.Preview
	film := pv != nil && pv.Gallery >= 3 && pv.Images >= 5
	thumb := pv != nil && pv.HasImage && !film && a.thumbShows(art.ID, 0)
	row.Children(func() {
		if rule {
			ui.Box(c).Absolute().Top(0).Left(72).Right(12).Height(1).Background(theme.LineSoft).IgnoreInput(true)
		}
		// The row's grid keeps an empty column for its actions: 8 and 8 at its end.
		b := ui.ButtonBase(c.Key("open")).Label(titleOf(art.Title)).FillWidth().Margin(0, 16, 0, 0).Padding(18, 12).Gap(14).Radius(10).AlignItems(ui.Start).
			FocusRing(false).TabStop(cursor).KeyShortcuts(0, ui.KeyEnter)
		if b.Clicked() {
			l.cursor = art.ID
			a.open(c, art.ID, b.LayoutBounds())
		}
		if l.focus == art.ID {
			l.focus = ""
			b.Focus().ScrollIntoView()
		} else if b.Focused() && a.kbd {
			l.cursor = art.ID
		}
		focus := b.FocusVisible() || b.Focused() && a.kbd
		if focus {
			row.Background(theme.Selected).InsetShadow(0, 0, 0, 1.5, theme.Focus)
		}
		r := b.LayoutBounds()
		l.rows[art.ID] = r
		a.layInto(p, art.ID, r)
		near := r.W == 0 || r.Y < view.Y+view.H+400 && r.Y+r.H > view.Y-400
		b.Children(func() {
			ui.Column(c).Width(34).AlignItems(ui.End).Shrink(0).FontFeatures(theme.Numbers).Children(func() {
				if t, ok := articleTime(art); ok {
					d := local(t)
					ui.Text(c, strconv.Itoa(d.Day())).FontSize(theme.Subtitle).LineHeight(24.0 / 20).TextColor(theme.Text)
					ui.Text(c, strconv.Itoa(int(d.Month()))+"月").FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted)
				}
			})
			ui.Box(c).Width(12).Padding(5, 0, 0, 0).Shrink(0).Children(func() {
				if inProgress(art) {
					a.moonOf(c, art, fmt.Sprintf("读到 %d%%", percentOf(art)))
				}
			})
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Children(func() {
				ui.Row(c).Gap(7).AlignItems(ui.Center).MinWidth(0).Children(func() {
					t := rowTitle(c, titleOf(art.Title)).Shrink(1).MinWidth(0)
					a.end(t, p, "title:"+art.ID)
					if art.Favorite {
						a.orbit(c, "badge", 11, true).AccessHidden(false).Label("已收藏")
					}
				})
				if pv != nil && pv.Excerpt != nil && *pv.Excerpt != "" {
					ui.Text(c, *pv.Excerpt).Margin(4, 0, 0, 0).FontSize(theme.Body).LineHeight(21.0 / 13).TextColor(theme.Text2).MaxLines(2).Ellipsis("…")
				}
				if film {
					ui.Row(c).Margin(12, 0, 0, 0).Gap(6).MaxWidth(420).Children(func() {
						for i := range 3 {
							a.thumbView(c, art.ID, i, near).Grow(1).Basis(0).AspectRatio(1.5)
						}
					})
				}
				var facts []string
				if pv != nil && pv.Minutes != nil && *pv.Minutes > 0 {
					facts = append(facts, fmt.Sprintf("约 %d 分钟", *pv.Minutes))
				}
				if pv != nil && pv.CodeBlocks > 0 {
					facts = append(facts, fmt.Sprintf("%d 段代码", pv.CodeBlocks))
				}
				if pv != nil && pv.Images > 1 {
					facts = append(facts, fmt.Sprintf("%d 张图", pv.Images))
				}
				if inProgress(art) {
					facts = append(facts, fmt.Sprintf("读到 %d%%", percentOf(art)))
				}
				if len(facts) > 0 {
					ui.Row(c).Margin(1, 0, 0, 0).FontSize(theme.Label).LineHeight(18.0 / 12).TextColor(theme.Muted).Children(func() {
						for i, f := range facts {
							if i > 0 {
								sep(c)
							}
							ui.Text(c, f).NoWrap()
						}
					})
				}
			})
			if thumb {
				a.thumbView(c, art.ID, 0, near).Size(120, 80).Margin(0, 0, 0, 4)
			}
		})
		// Resting on a row reads its article and lays it out, unseen (warm.go):
		// only an article already in the library, so the pointer alone never
		// fetches a page, as in Electron, where nothing is read before the click.
		if row.Hovered() && !a.kbd && live {
			if a.hover != art.ID {
				a.hover, a.hoverAt = art.ID, c.Now()
				if art.BodyStatus != "not_extracted" {
					a.reading(art.ID)
				}
			}
		} else if a.hover == art.ID && !a.pinned {
			a.hover = ""
		}
		// The favourite action, shown on hover and on the keyboard's cursor.
		show := row.Hovered() || focus
		right := float32(8)
		if thumb {
			right = 148
		}
		fav := a.favButton(c, art, 28, 14).Absolute().Right(right).Bottom(12).TabStop(false)
		fav.Opacity(fav.AnimateTo("show", map[bool]float32{true: 1, false: 0}[show || fav.Hovered()], a.ease(motion.Reveal)))
	})
}

// favButton keeps or lets go of an article (RowActions, the reader's fav):
// the orbit closes and lights amber.
func (a *App) favButton(c *ui.Context, art *library.Article, size, orbit float32) ui.Element {
	label := "收藏"
	if art.Favorite {
		label = "取消收藏"
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
	a.press(b, 0.94)
	b.Children(func() { a.orbit(c, "orbit", orbit, art.Favorite) })
	a.tip(c, b, label+" · S", false)
	return b
}

func (a *App) toggleFav(art *library.Article) {
	a.mutate(library.ToggleFavorite{ID: art.ID}, nil)
}

// moon is reading progress (HomeList Moon): a ring and what is left to
// read, lit on the left; an empty ring at the end.
func (a *App) moon(c *ui.Context, size, lit float32, label string) ui.Element {
	return drawMoon(ui.Box(c), size, lit, label)
}

// moonBack is a list's moon of the article whose reader was just left: it
// goes from where it was as the article was opened to where it was left
// (main.tsx returning, HomeList tweenMoon), once its row is built.
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

// moonPath is HomeList's moonPath, waning: the left limb, and the
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

// orbit is the favourite (HomeList Orbit): a centre and an arc not closed;
// kept, the arc closes, the satellite comes round to the top and it all
// lights amber (460 ms; its glow over 200).
func (a *App) orbit(c *ui.Context, key string, size float32, kept bool) ui.Element {
	box := ui.Box(c.Key(key)).Size(size, size).Shrink(0).AccessHidden(true)
	target := float32(0)
	if kept {
		target = 1
	}
	var k, glow float32
	if a.kbd || a.still(c) && false {
		k, glow = box.Jump("k", target), box.Jump("glow", target)
	} else {
		k = box.AnimateTo("k", target, ui.Animation{Duration: motion.Orbit, Ease: motion.Ease})
		glow = box.AnimateTo("glow", target, a.ease(motion.Glow))
	}
	if a.still(c) {
		k = box.Jump("k", target) // the satellite does not travel under less motion
	}
	color := theme.Muted
	if kept {
		color = theme.Star
	}
	ink := box.AnimateColor("ink", color, a.ease(motion.Glow))
	return box.Draw(func(pt *ui.Painter, r ui.Rect) {
		s := size / 20 // the drawing's units: -10 to 10
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		if glow > 0.001 {
			var g ui.Path
			g.Circle(cx, cy, 8*s)
			pt.FillPath(&g, theme.Star.Alpha(0.16*glow))
		}
		arc := 0.7 + 0.3*k
		pt.StrokePath(arcPath(cx, cy, 6.5*s, -90, 360*arc), 1.15*s, ink)
		var dot ui.Path
		dot.Circle(cx, cy, 1.7*s)
		pt.FillPath(&dot, ink)
		// The satellite rides the arc's end, from 108° short of the top.
		if sat := min(1, k*4); sat > 0.001 || kept {
			ang := (-90 - 108*(1-k)) * math.Pi / 180
			var sp ui.Path
			sp.Circle(cx+6.5*s*float32(math.Cos(float64(ang))), cy+6.5*s*float32(math.Sin(float64(ang))), 1.5*s)
			pt.FillPath(&sp, ink.Alpha(sat))
		}
	})
}

// arcPath is an arc of a circle from start, in degrees clockwise from the
// right, sweeping clockwise, in cubic pieces of at most 90°.
func arcPath(cx, cy, r, start, sweep float32) *ui.Path {
	var p ui.Path
	a0 := float64(start) * math.Pi / 180
	p.MoveTo(cx+r*float32(math.Cos(a0)), cy+r*float32(math.Sin(a0)))
	left := float64(sweep) * math.Pi / 180
	for left > 1e-4 {
		step := math.Min(left, math.Pi/2)
		a1 := a0 + step
		h := 4.0 / 3 * math.Tan(step/4)
		x0, y0 := math.Cos(a0), math.Sin(a0)
		x1, y1 := math.Cos(a1), math.Sin(a1)
		p.CubeTo(cx+r*float32(x0-h*y0), cy+r*float32(y0+h*x0), cx+r*float32(x1+h*y1), cy+r*float32(y1-h*x1), cx+r*float32(x1), cy+r*float32(y1))
		a0, left = a1, left-step
	}
	return &p
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
