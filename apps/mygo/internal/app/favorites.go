package app

import (
	"cmp"
	"encoding/base64"
	"fmt"
	"image"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/net/html"

	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/theme"
	"trie/internal/web"
)

// 收藏: kept by author. Each author is
// a whole picture, their own site's first screen, laid edge to edge down
// the page, their name and latest keeps written into its lower-left corner.
// Opening an author, that picture folds up into the head of their page and
// their keeps follow, two to a row, each its own picture with its words in
// it, or its own first page. Letting go asks nothing: the notice offers 撤销.

// favState is what 收藏 keeps between frames.
type favState struct {
	shelves, keeps ui.ScrollState // the authors' page; an author's, from its top on each visit
	query          string
	search         bool
	place          string // the author whose page was open last: the tab goes back to it
	lastQuery      string
	lastCols       int
	pics           map[string]*favPic
	openings       map[string]*favOpening
	warmed         map[string]bool
	warming        map[string]bool
	menu, ctx      string    // the ··· menu and the right-click menu open, by card
	cursor         string    // the card the keyboard is on: "a:<author>" or "k:<article>"
	focused        string    // the card that had the focus in the last build
	focus          string    // the card to take the focus in the next build
	cards          []favCard // the cards the last build showed, in order
	leftAt         time.Time // when a card last left: the rest close up once it has faded
	pending        string    // an author to let go of once the page has folded back to them
	pendingKbd     bool
	crossing       uint64                // the run of the last crossing between the shelves and an author
	shades         map[string]*ui.Bitmap // the shades baked (shade), by box and gradient
}

type favCard struct {
	key string
	box ui.Rect
}

func (a *App) favs() *favState {
	if a.fav == nil {
		a.fav = &favState{pics: map[string]*favPic{}, openings: map[string]*favOpening{}, warmed: map[string]bool{}, warming: map[string]bool{},
			shades: map[string]*ui.Bitmap{}}
	}
	return a.fav
}

// favGroup is an author's keeps, or a kind's (kind set, src standing for
// the kind), newest kept first.
type favGroup struct {
	src  *library.Source
	list []*library.Article
	kind string
}

// kindSource is a kind as 收藏 shows it: a group named for it, with no site.
func kindSource(k string) *library.Source {
	return &library.Source{SourceSnapshot: library.SourceSnapshot{ID: "kind:" + k, Title: kindName(k)}, DisplayName: kindName(k)}
}

// keeperOf is whose a keep is as 收藏 shows it, every source counting: the
// first of its sources the library knows.
func (a *App) keeperOf(art *library.Article) *library.Source {
	for _, id := range memberships(art) {
		if s := a.source(id); s != nil {
			return s
		}
	}
	return a.source(art.SourceID)
}

// keptAt is when an article was kept, or when it is dated for one kept
// before that was recorded.
func keptAt(art *library.Article) int64 {
	if art.FavoritedAt != nil {
		if t, ok := web.JSDateParse(*art.FavoritedAt); ok {
			return t
		}
	}
	t, _ := articleTime(art)
	return t
}

// favMatches reports whether the title, the names and sites of its
// sources, or its authors hold what is typed.
func (a *App) favMatches(art *library.Article, q string) bool {
	if q == "" {
		return true
	}
	hay := []string{art.Title}
	for _, id := range memberships(art) {
		s := a.source(id)
		hay = append(hay, nameOf(s))
		if s != nil {
			hay = append(hay, s.Host)
		}
	}
	for _, h := range append(hay, art.Authors...) {
		if strings.Contains(strings.ToLower(h), q) {
			return true
		}
	}
	return false
}

// kept is what 收藏 shows for query: the keeps it lets through, newest kept
// first, and their groups: the kinds the designs among them are shelved
// under, then the authors of the rest, each in the order of their newest
// keep.
func (a *App) kept(query string) (shown []*library.Article, groups []*favGroup) {
	q := strings.ToLower(strings.TrimSpace(query))
	for i := range a.lib.Articles {
		if art := &a.lib.Articles[i]; art.Favorite && a.favMatches(art, q) {
			shown = append(shown, art)
		}
	}
	slices.SortStableFunc(shown, byDate)
	slices.SortStableFunc(shown, func(x, y *library.Article) int { return cmp.Compare(keptAt(y), keptAt(x)) })
	by := map[string]*favGroup{}
	var kinds []*favGroup
	for _, art := range shown {
		if ks := a.kindsOf(art); len(ks) > 0 {
			for _, k := range ks {
				g := by["kind:"+k]
				if g == nil {
					g = &favGroup{src: kindSource(k), kind: k}
					by["kind:"+k] = g
					kinds = append(kinds, g)
				}
				g.list = append(g.list, art)
			}
			continue
		}
		s := a.keeperOf(art)
		if s == nil {
			continue
		}
		g := by[s.ID]
		if g == nil {
			g = &favGroup{src: s}
			by[s.ID] = g
			groups = append(groups, g)
		}
		g.list = append(g.list, art)
	}
	return shown, append(kinds, groups...)
}

func (a *App) anyKept() bool {
	return slices.ContainsFunc(a.lib.Articles, func(art library.Article) bool { return art.Favorite })
}

// What both sides of an author's crossing show: their picture, folding from
// the shelf into the head of their page, the line above their name and
// their name.
type (
	favPicMatch  struct{ id string }
	favByMatch   struct{ id string }
	favNameMatch struct{ id string }
)

// The heights of an author's picture: the first
// leaves the next one's top in view, one alone fills the screen, the others
// are shorter, and the head of their page is the same picture folded.
type sceneKind uint8

const (
	sceneFirst sceneKind = iota
	sceneOnly
	sceneRest
	sceneHead
)

func clampf(v, lo, hi float32) float32 { return max(lo, min(hi, v)) }

func sceneHeight(k sceneKind, vh float32) float32 {
	switch k {
	case sceneFirst:
		return clampf(vh-150, 480, 860)
	case sceneOnly:
		return clampf(vh-96, 480, 900)
	case sceneRest:
		return clampf(0.64*vh, 400, 620)
	}
	return clampf(0.5*vh, 340, 480)
}

// groundHeight is the one height every author's picture is drawn at from
// the top, however tall the place showing it: folding only cuts it.
func groundHeight(vh float32) float32 { return clampf(vh-96, 480, 900) }

// favoritesPage builds 收藏: the authors (id ""), or an author's keeps.
func (a *App) favoritesPage(c *ui.Context, id string, live bool) {
	fs := a.favs()
	p := page{"favorites", id}
	// What the designs kept are shelved under is Explore's to say.
	if ex := a.ex; live && !ex.loaded && !ex.loading && ex.failed == "" && a.opt.Explore != nil {
		a.loadExplore()
	}
	searching := strings.TrimSpace(fs.query) != ""
	shown, groups := a.kept(fs.query)
	var cur *favGroup
	for _, g := range groups {
		if g.src.ID == id {
			cur = g
		}
	}
	anyKept := a.anyKept()
	cols := 1
	if min(a.area.W, 1200)-64 >= 704 {
		cols = 2
	}
	// Moves that happen a hundred times a day stay still: the keyboard's,
	// the search filtering, the window resizing.
	quiet := a.kbd || fs.lastQuery != fs.query || fs.lastCols != cols
	stop := ""
	if live {
		fs.place = id
		fs.lastQuery, fs.lastCols = fs.query, cols
		// An author with nothing kept any more sends you back to the authors.
		if id != "" && cur == nil && !searching {
			a.goTo(c, page{kind: "favorites"}, motion.Side, nil).noMotion()
		}
		stop = fs.tabStop()
		fs.cards, fs.focused = fs.cards[:0], ""
		if fs.pending != "" && id == "" {
			a.letGoPending(c, groups)
		}
	}
	found := shown
	if searching && id != "" {
		found = nil
		if cur != nil {
			found = cur.list
		}
	}
	st := &fs.shelves
	if id != "" {
		st = &fs.keeps
	}
	pictured := anyKept && !searching && (cur != nil || id == "" && len(groups) > 0)
	sc := ui.Scroll(c.Key("scroll")).TrackScroll(st).Grow(1).FillWidth().Scrollbars(false)
	view := sc.LayoutBounds()
	var drawn []*library.Article
	sc.Children(func() {
		ui.Column(c).FillWidth().MinHeight(view.H).Children(func() {
			if cur == nil {
				ui.Text(c, locales.TabFavorites()).Role(ui.RoleHeading).Level(1).Absolute().Size(1, 1).ClipX().ClipY().Opacity(0)
			}
			band := func() {
				a.band(c, func() {
					if id != "" {
						a.backLink(c, locales.TabFavorites())
					}
					if searching {
						ui.Text(c, locales.FavoritesMatches(len(found))).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).
							FontFeatures(theme.Numbers).NoWrap().Role(ui.RoleStatus)
					}
				}, func() {
					if anyKept || searching {
						a.searchField(c, &fs.query, &fs.search, locales.FavoritesSearch(), locales.FavoritesSearchPlaceholder())
					}
				})
			}
			if pictured {
				// Pictures float the band; a page of words keeps it in its place.
				ui.Column(c.Key("band-float")).Absolute().Top(0).Left(0).Right(0).ZIndex(3).AlignItems(ui.Center).PassThrough().Children(func() {
					frame(c).PassThrough().Children(band)
				})
			}
			switch {
			case !anyKept:
				frame(c).Padding(0, 32, 140, 32).Children(func() {
					band()
					a.emptyState(c, "favorites", locales.FavoritesEmptyTitle(), locales.FavoritesEmptyText(), nil)
				})
			case searching:
				frame(c).Padding(0, 32, 140, 32).Children(func() {
					band()
					if len(found) == 0 {
						a.emptyState(c, "", "", locales.FavoritesNoMatches(), nil)
						return
					}
					drawn = found
					a.tiles(c, p, locales.FavoritesSearchResults(), found, true, cols, quiet, stop, live)
				})
			case cur != nil:
				a.scene(c, p, cur, sceneHead, quiet, stop, live)
				frame(c).Padding(0, 32, 140, 32).Children(func() {
					list := slices.Clone(cur.list)
					// Their own page reads as their archive: newest first by when they wrote it.
					slices.SortStableFunc(list, func(x, y *library.Article) int {
						tx, _ := articleTime(x)
						ty, _ := articleTime(y)
						return cmp.Compare(ty, tx)
					})
					drawn = list
					a.tiles(c, p, locales.FavoritesFrom(nameOf(cur.src)), list, cur.kind != "", cols, quiet, stop, live)
				})
			default:
				// The designs kept, by kind, are the first screen's board; the writers kept from follow, one picture each.
				var kinds, people []*favGroup
				for _, g := range groups {
					if g.kind != "" {
						kinds = append(kinds, g)
					} else {
						people = append(people, g)
					}
				}
				if len(kinds) > 0 {
					a.favBoard(c, p, kinds, len(people) == 0, stop, live)
				}
				ui.Column(c.Key("shelves")).FillWidth().Padding(0, 0, 96, 0).Role(ui.RoleList).Label(locales.FavoritesWriters()).Children(func() {
					for i, g := range people {
						k := sceneRest
						if i == 0 && len(kinds) == 0 {
							k = sceneFirst
							if len(people) == 1 {
								k = sceneOnly
							}
						}
						a.scene(c, p, g, k, quiet, stop, live)
					}
				})
			}
		})
	})
	if live {
		a.scrolling = st
		a.warmKeeps(drawn)
	}
}

// frame is the page's frame (ui/page grid): 1200 at most, 32 at the sides.
// Pictures run past it to the window's edges; words keep to it.
func frame(c *ui.Context) ui.Element {
	return ui.Column(c).FillWidth().MaxWidth(1200).AlignSelf(ui.Center).Padding(0, 32)
}

// tabStop is the page's one tab stop: the keep in hand if it is here, else
// its author, else the first card.
func (fs *favState) tabStop() string {
	if len(fs.cards) == 0 {
		return ""
	}
	has := func(key string) bool {
		return slices.ContainsFunc(fs.cards, func(c favCard) bool { return c.key == key })
	}
	if has(fs.cursor) {
		return fs.cursor
	}
	return fs.cards[0].key
}

// card notes a card as built, and gives it the focus asked for.
func (a *App) card(b ui.Element, key, stop string, live bool) {
	fs := a.favs()
	b.TabStop(key == stop || stop == "" && len(fs.cards) == 0)
	if !live {
		return
	}
	fs.cards = append(fs.cards, favCard{key, b.LayoutBounds()})
	// Into an author from the keys, the focus goes to their first card.
	if fs.focus == key || fs.focus == favFirst && len(fs.cards) == 1 {
		fs.cursor = key
		fs.focus = ""
		b.Focus().ScrollIntoView()
	}
	if b.Focused() {
		fs.focused = key
		if a.kbd {
			fs.cursor = key
		}
	}
}

// listMove is how a card or an author moves as others come and go: the
// rest close up on Land, past their place once and back (弹一下),
// once what left has faded (150 ms, out of focus); one coming back rises 4
// out of a 2 px blur. Quiet changes do none of it.
func (a *App) listMove(c *ui.Context, quiet bool) ui.ElementTransition {
	f := motion.Land
	if now := c.Now(); now.Sub(a.favs().leftAt) < motion.Leave+motion.State {
		f.Delay = motion.Leave
	}
	t := ui.ElementTransition{Animation: &f, Position: true, ExitDuration: motion.Leave, ExitEase: motion.Ease,
		Enter: &ui.Motion{Y: 4, Blur: 2}, Exit: &ui.Motion{Blur: 2}}
	if quiet {
		return motion.Instant(t)
	}
	return a.tr(c, t)
}

// scene is an author's picture (Shelf, ShelfHead): their own site edge to
// edge, their name and latest keeps written into its lower-left corner; at
// the head of their page the same picture, folded shorter.
func (a *App) scene(c *ui.Context, p page, g *favGroup, kind sceneKind, quiet bool, stop string, live bool) {
	fs := a.favs()
	id := g.src.ID
	key := "a:" + id
	_, vh := c.Size()
	h, gh, w := sceneHeight(kind, vh), groundHeight(vh), a.area.W
	head := kind == sceneHead
	outer := ui.Box(c.Key(key)).FillWidth().Height(h)
	if !head {
		outer.Transition(a.listMove(c, quiet))
	}
	outer.Children(func() {
		sc := ui.Box(c.Key("scene")).Absolute().Fill().ClipX().ClipY()
		a.share(c, sc, p, favPicMatch{id}, live)
		hover := sc.Hovered()
		near := sc.InViewMargin(600, 0)
		pic, site, waiting := a.shelfPicture(g, near)
		bright := float32(1)
		if pic != nil {
			bright = pic.rest
			if hover || head {
				bright = pic.lit
			}
		}
		bright = sc.AnimateTo("bright", bright, a.ease(motion.Lean))
		ctx := fs.ctx == key
		menu := ui.ContextMenuBase(c, sc, &ctx)
		a.shelfMenu(c, menu, g, true)
		fs.ctx = keepOpen(fs.ctx, key, ctx)
		// Drawn at the scene's own height and cut there, as a snapshot of it is: while the frame folds, what lies past
		// it is the other side, fading.
		sc.Children(func() {
			own := ui.Box(c.Key("own")).Absolute().Left(0).Right(0).Top(0).Height(h).ClipX().ClipY().PassThrough()
			if a.crossing(c, live) {
				own.Cache()
			}
			own.Children(func() {
				a.sceneLayers(c, p, g, kind, h, gh, w, pic, site, waiting, bright, hover || sc.FocusWithin(), near || head, stop, live)
			})
		})
	})
}

// sceneLayers are what an author's picture holds, from the bottom: the
// picture, the shades under the band and over the foot, the pool of shade
// behind the words, the whole picture as a way in, and the words.
func (a *App) sceneLayers(c *ui.Context, p page, g *favGroup, kind sceneKind, h, gh, w float32, pic *favPic, site, waiting bool, bright float32, shown, near bool, stop string, live bool) {
	id, key, head := g.src.ID, "a:"+g.src.ID, kind == sceneHead
	if !near {
		// Far from the view the picture is not drawn, only its words and its way in: a page drawn whole into a layer as
		// it crosses (its blur) then paints only the pictures near the view.
		a.sceneWay(c, p, g, h, head, key, id, shown, stop, live)
		return
	}
	fade := a.picFade(c, pic)
	// The pool of shade behind the words, sized from where the words sit:
	// they reach about 560 into the frame and 330 up from the foot. Under
	// the words the picture is softened (a backdrop's blur(18px)), wholly to
	// 0.62 of the pool and not at all past 0.92.
	rx, ry, inner, outer := (w-1200)*0.9+1240, float32(620), float32(0.62), float32(0.92)
	if head {
		rx, ry, inner, outer = (w-1200)*0.9+1000, 360, 0.46, 0.9
	}
	var soft *ui.Bitmap
	if pic != nil && pic.soft != nil {
		soft = pic.pool(w, gh, posOf(site), 0, h, rx, ry, inner, outer)
	}
	a.groundView(c, g, pic, site, waiting, gh, bright, fade, soft)
	if kind == sceneRest {
		// A picture that follows another comes in out of the window's ground.
		ui.Box(c).Absolute().Left(0).Top(0).Right(0).Height(144).IgnoreInput(true).LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
			{Color: theme.Window, At: 0}, {Color: theme.Window.Alpha(0.55), At: 64.0 / 144}, {Color: theme.Window.Alpha(0), At: 1}}})
	} else {
		// Under the floating band: dark enough for its grey words on a white site.
		ui.Box(c).Absolute().Left(0).Top(0).Right(0).Height(132).IgnoreInput(true).LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
			{Color: theme.Window.Alpha(0.9), At: 0}, {Color: theme.Window.Alpha(0.9), At: 52.0 / 132}, {Color: theme.Window.Alpha(0), At: 1}}})
	}
	// Darkened from the lower-left corner: at least 0.9 under the words, gone past them.
	a.shade(c, w, h, rx, ry, [][2]float32{{0, 0.92}, {0.58, 0.9}, {0.8, 0.6}, {1, 0}}).Absolute().Left(0).Top(0).Width(w).Height(h)
	// Everything in the picture keeps its place from the top, at the scene's own height, so that while
	// its frame folds into the other side's the picture and its words hold still and are only cut.
	ui.Box(c).Absolute().Left(0).Right(0).Top(0).Height(h).IgnoreInput(true).LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
		{Color: theme.Window.Alpha(0), At: (h - 144) / h}, {Color: theme.Window.Alpha(0.55), At: (h - 64) / h}, {Color: theme.Window, At: 1}}})
	a.sceneWay(c, p, g, h, head, key, id, shown, stop, live)
}

// sceneWay is the way into an author, the whole picture, and their words over it.
func (a *App) sceneWay(c *ui.Context, p page, g *favGroup, h float32, head bool, key, id string, shown bool, stop string, live bool) {
	fs := a.favs()
	ring := false
	if !head {
		// The whole picture opens the author.
		enter := ui.ButtonBase(c.Key("enter")).Label(locales.FavoritesWriterCount(nameOf(g.src), len(g.list))).Absolute().Fill().FocusRing(false).
			KeyShortcuts(0, ui.KeyEnter).KeyShortcuts(ui.Cmd, ui.KeyBackspace)
		a.card(enter, key, stop, live)
		if enter.Clicked() && live {
			fs.cursor = key
			a.enterShelf(c, id)
		}
		ring = enter.FocusVisible()
	}
	a.sceneWords(c, p, g, h, head, ring, shown, live)
}

// share gives e, shown on page p, the identity match has on the other page
// of 收藏: coming from it, it travels there; otherwise it says where it is,
// for the other to come from.
func (a *App) share(c *ui.Context, e ui.Element, p page, match any, live bool) {
	fs, m := a.favs(), a.came
	// The move came from the other page of 收藏: noted in its first frame,
	// while the page left is still built, and kept for the whole flight.
	if m != nil && a.left != nil && a.left.move == m && a.left.page.kind == "favorites" {
		fs.crossing = m.run
	}
	if m != nil && !m.kbd && m.run == fs.crossing && m.to.kind == "favorites" && (m.depth == motion.Deep || m.depth == motion.Back) &&
		(live && m.to == p || !live && a.left != nil && a.left.page == p) {
		// Both sides fly, so that one fades into the other as their frame folds. Flying, they are scaled with the
		// pages they leave and reach: drawn into a bitmap of their own while they fly, their clips (the frame
		// folding, the face's circle) cut there unscaled, rather than as masks drawn on the CPU every frame.
		e.Transition(a.tr(c, m.depth.Crossing().Shared(match)))
		if c.Now().Sub(a.movedAt) < crossingTime {
			e.Cache()
		}
		return
	}
	e.Transition(ui.ElementTransition{Match: match})
}

// crossing reports whether the page built takes part in a crossing: the
// page left, built for its exit, or the page coming into an author or an
// article or back out of one, while it comes. The crossing blurs both pages
// and has what they share fly over them, which draws them anew every frame:
// a picture or a card holds still in them, drawn once into a bitmap of its
// own (Cache) rather than layer by layer every frame. A page the sidebar's
// tabs bring draws as it is: its first frame would draw those bitmaps too.
func (a *App) crossing(c *ui.Context, live bool) bool {
	m := a.came
	return m != nil && !m.kbd && (!live || m.depth != motion.Side && c.Now().Sub(a.movedAt) < crossingTime)
}

// keepOpen is which of the page's menus is open after one built as key.
func keepOpen(cur, key string, open bool) string {
	switch {
	case open:
		return key
	case cur == key:
		return ""
	}
	return cur
}

// sceneWords are an author's words in their picture: face, site and count,
// their name with the menu, and on the shelf the latest three keeps. The
// keyboard's ring goes round them rather than the picture.
func (a *App) sceneWords(c *ui.Context, p page, g *favGroup, h float32, head, ring, shown, live bool) {
	id := g.src.ID
	bottom := float32(64)
	if head {
		bottom = 48
	}
	ui.Column(c.Key("words")).Absolute().Left(0).Right(0).Top(0).Height(h - bottom).Justify(ui.End).AlignItems(ui.Center).PassThrough().Children(func() {
		frame(c).AlignItems(ui.Start).PassThrough().Children(func() {
			mark := ui.Column(c.Key("mark")).Gap(8).AlignItems(ui.Start).MaxWidthPercent(100).Radius(8).PassThrough()
			if ring {
				mark.FocusRing(true).FocusRingOffset(12)
			}
			mark.Children(func() {
				by := ui.Row(c.Key("by")).Gap(8).AlignItems(ui.Center).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2).FontFeatures(theme.Numbers)
				a.share(c, by, p, favByMatch{id}, live)
				by.Children(func() {
					if g.kind == "" {
						f := a.face(c, a.iconOf(g.src), "face", 20)
						if live {
							a.shelfFaces[id] = f.LayoutBounds()
						}
						if a.tripping("shelf:" + id) {
							f.Opacity(0)
						}
					}
					if host := g.src.Host; host != "" {
						if head && g.src.HomeURL != "" {
							site := ui.ButtonBase(c.Key("site")).Label(host).Margin(0, -4).Padding(0, 4).Radius(6).FocusRing(false)
							color := theme.Text2
							if site.Hovered() {
								color = theme.Text
							}
							if site.FocusVisible() {
								site.FocusRing(true)
							}
							if site.Clicked() {
								a.openExternal(c, g.src.HomeURL)
							}
							a.control(site)
							site.Children(func() {
								t := ui.Text(c, host).TextColor(site.AnimateColor("fg", color, a.ease(motion.Hover)))
								if site.Hovered() {
									t.Underline().UnderlineOffset(3)
								}
							})
							a.tip(c, site, locales.CommonOpenInBrowserTip(), false)
						} else {
							ui.Text(c, host)
						}
						ui.Text(c, "·").AccessHidden(true)
					}
					ui.Text(c, locales.FavoritesCount(len(g.list)))
				})
				ui.Row(c.Key("name-row")).Gap(8).AlignItems(ui.Center).MaxWidthPercent(100).Children(func() {
					level := 2
					if head {
						level = 1
					}
					name := ui.Text(c.Key("name"), nameOf(g.src)).FontSize(40).LineHeight(1.15).FontWeight(650).LetterSpacing(-0.015 * 40).
						TextColor(theme.Text).Balance().Shrink(1).MinWidth(0).Role(ui.RoleHeading).Level(level)
					a.share(c, name, p, favNameMatch{id}, live)
					a.moreMenu(c, "shelf:"+id, locales.FavoritesWriterActions(nameOf(g.src)), head || shown, func(m ui.MenuParts) { a.shelfMenu(c, m, g, false) }).Margin(0, 0, 0, 0)
				})
				if head {
					return
				}
				ui.Column(c.Key("titles")).Width(480).MaxWidthPercent(100).Margin(8, 0, 0, 0).Role(ui.RoleList).Label(locales.FavoritesRecentFrom(nameOf(g.src))).Children(func() {
					for i, art := range g.list[:min(3, len(g.list))] {
						a.shelfTitle(c, p, art, i == 0, live)
					}
				})
			})
		})
	})
}

// shelfTitle is one of an author's latest keeps written into their picture:
// the newest brightest; it opens its article.
func (a *App) shelfTitle(c *ui.Context, p page, art *library.Article, first, live bool) {
	ui.Box(c.Key(art.ID)).FillWidth().BorderWidth(1, 0, 0, 0).BorderColor(theme.TitleRule).Children(func() {
		b := ui.ButtonBase(c.Key("open")).Label(titleOf(art.Title)).FillWidth().Padding(8, 0).Gap(8).Radius(6).AlignItems(ui.Center).
			FocusRing(false).TabStop(false)
		r := b.LayoutBounds()
		if b.Clicked() && live {
			a.favs().cursor = "k:" + art.ID
			a.openAs(c, art.ID, r, shelfTitleText, theme.Item)
		}
		a.layInto(p, art.ID, r)
		color := theme.Text2
		if first || b.Hovered() {
			color = theme.Text
		}
		if b.FocusVisible() {
			b.FocusRing(true)
		}
		b.Children(func() {
			t := shelfTitleText(c, titleOf(art.Title)).TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover))).Grow(1).Shrink(1).MinWidth(0)
			a.end(t, p, "title:"+art.ID)
			a.looks[art.ID] = titleLook{shelfTitleText, theme.Item}
			a.progress(c, art)
		})
	})
}

func shelfTitleText(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).Font(reader.TitleFont(s)).FontSize(theme.Item).LineHeight(22.0 / 15).FontWeight(500).TextColor(theme.Text).SingleLine().Ellipsis("…")
}

// progress is the moon, only while an article is part-read.
func (a *App) progress(c *ui.Context, art *library.Article) {
	if inProgress(art) {
		ui.Box(c).Padding(0, 0, 0, 2).Shrink(0).Children(func() {
			a.moonOf(c, art, locales.ReaderProgress(percentOf(art)))
		})
	}
}

// moreMenu is ···: a dark glass disc that reads on
// any picture, shown while what it belongs to is pointed at or has the
// focus, or while its menu is open; build fills its menu.
func (a *App) moreMenu(c *ui.Context, key, label string, shown bool, build func(ui.MenuParts)) ui.Element {
	fs := a.favs()
	open := fs.menu == key
	m := ui.MenuBase(c.Key("more:"+key), &open)
	t := m.Trigger.Label(label).Size(32, 32).Radius(16).Center().Shrink(0).FocusRing(false).TabStop(false)
	color, ground := theme.Text2, theme.MoreGround
	if t.Hovered() {
		color, ground = theme.Text, theme.MoreGroundHover
	}
	t.Background(t.AnimateColor("bg", ground, a.ease(motion.Hover))).TextColor(t.AnimateColor("fg", color, a.ease(motion.Hover))).
		InsetShadow(0, 0, 0, 1, theme.Line)
	if t.FocusVisible() {
		t.FocusRing(true)
	}
	// A menu opens on the press, so the ··· squares up while it is open
	// rather than while pressed: round to 10 and back on RiseIn (弹一下).
	a.press(t, motion.SquashedDot)
	sq := float32(0)
	if open {
		sq = 1
	}
	if a.kbd || a.still(c) {
		sq = t.Jump("square", sq)
	} else {
		sq = t.AnimateTo("square", sq, motion.RiseIn)
	}
	t.Radius(16 - (16-motion.SquaredUp)*sq)
	a.control(t)
	show := float32(0)
	if shown || open || t.FocusVisible() {
		show = 1
	}
	t.Opacity(t.AnimateTo("show", show, a.ease(motion.Hover)))
	t.Children(func() { a.pressIcon(c, "more", 16, false, open) }) // a menu opens on the press
	build(m)
	fs.menu = keepOpen(fs.menu, key, open)
	return t
}

// shelfMenu is an author's menu: their site, and letting go of all they
// have kept; at is a right click's.
func (a *App) shelfMenu(c *ui.Context, m ui.MenuParts, g *favGroup, pointer bool) {
	m.Popup(func(panel ui.Element) {
		a.menuPanel(c, panel)
		if !pointer {
			panel.AttachTo(m.Trigger, ui.AnchorBottomLeft, ui.AnchorTopLeft).Margin(8, 0, 0, 0)
		}
		if g.src.HomeURL != "" {
			if a.menuItem(c, m.Item(), locales.CommonOpenInBrowser(), false, "openArrow").Clicked() {
				a.openExternal(c, g.src.HomeURL)
			}
			ui.Box(c).Height(1).Margin(4, 8).Background(theme.Line).AccessHidden(true)
		}
		label := locales.CommonRemoveFavorite()
		if len(g.list) > 1 {
			label = locales.FavoritesRemoveAll(len(g.list))
		}
		// No confirmation: the notice that follows offers 撤销.
		if a.menuItem(c, m.Item(), label, true, "").Clicked() {
			a.letGoShelf(c, g)
		}
	})
}

// keepMenu is a keep's menu: the original, and letting it go.
func (a *App) keepMenu(c *ui.Context, m ui.MenuParts, p page, art *library.Article, pointer bool) {
	m.Popup(func(panel ui.Element) {
		a.menuPanel(c, panel)
		if !pointer {
			panel.AttachTo(m.Trigger, ui.AnchorBottomRight, ui.AnchorTopRight).Margin(8, 0, 0, 0)
		}
		if a.menuItem(c, m.Item(), locales.ReaderOpenOriginal(), false, "openArrow").Clicked() {
			a.openExternal(c, art.URL)
		}
		ui.Box(c).Height(1).Margin(4, 8).Background(theme.Line).AccessHidden(true)
		if a.menuItem(c, m.Item(), locales.CommonRemoveFavorite(), true, "").Clicked() {
			a.letGo(c, p, art)
		}
	})
}

// shade is CSS's radial-gradient(ellipse rx ry at 0% 100%, the window's
// colour at each stop's alpha) over a box w×h, drawn once for each box as a
// small picture stretched over it (the gradient is smooth enough not to
// show its pixels) rather than shaded on the GPU every frame.
// ponytail: kept for the life of the app; a few per window size.
func (a *App) shade(c *ui.Context, w, h, rx, ry float32, stops [][2]float32) ui.Element {
	fs := a.favs()
	key := fmt.Sprint(w, h, rx, ry, stops)
	b := fs.shades[key]
	if b == nil {
		const pw, ph = 192, 144
		m := image.NewNRGBA(image.Rect(0, 0, pw, ph))
		win := theme.Window
		for j := range ph {
			for i := range pw {
				dx, dy := (float32(i)+0.5)/pw*w/rx, (float32(ph-j)-0.5)/ph*h/ry
				d := float32(math.Sqrt(float64(dx*dx + dy*dy)))
				alpha := stops[len(stops)-1][1]
				for k := 1; k < len(stops); k++ {
					if d <= stops[k][0] {
						t := (d - stops[k-1][0]) / (stops[k][0] - stops[k-1][0])
						alpha = stops[k-1][1] + (stops[k][1]-stops[k-1][1])*max(0, t)
						break
					}
				}
				o := m.PixOffset(i, j)
				m.Pix[o], m.Pix[o+1], m.Pix[o+2], m.Pix[o+3] = win.R, win.G, win.B, uint8(alpha*255+0.5)
			}
		}
		b = ui.NewBitmap(m)
		fs.shades[key] = b
	}
	return ui.Image(c, b).Fit(ui.FillBox).IgnoreInput(true).AccessHidden(true)
}

// --- Pictures.

// favPic is a picture 收藏 shows, on its way, there, or none to be had,
// with how bright it rests and comes up to (toneOf).
type favPic struct {
	done      bool
	bmp       *ui.Bitmap
	soft      *image.NRGBA              // blurred once, for the soft pool under the words (softOf)
	pools     map[[9]float32]*ui.Bitmap // and cut to the pool, for each place it is drawn at (pool)
	rest, lit float32
	shown     time.Time // when it first showed: it fades in once
}

// favPicture asks for a picture once: load gives it, nil for none.
func (a *App) favPicture(key string, load func() image.Image) *favPic {
	fs := a.favs()
	if p := fs.pics[key]; p != nil || a.warming {
		return p
	}
	p := &favPic{rest: 0.84, lit: 1}
	fs.pics[key] = p
	a.do(func() func() {
		got := favPic{done: true, rest: 0.84, lit: 1}
		if img := load(); img != nil && !img.Bounds().Empty() {
			got.bmp, got.soft = ui.NewBitmap(img), softOf(img)
			got.pools = map[[9]float32]*ui.Bitmap{}
			got.rest, got.lit = toneOf(img)
		}
		return func() { *p = got }
	})
	return p
}

// sitePic is an author's own site's first screen (loadSourceShot); for
// someone only Explore has, what Explore shows of them.
func (a *App) sitePic(src *library.Source) *favPic {
	svc, id := a.opt.Explore, src.ID
	return a.favPicture("site:"+id, func() image.Image {
		if svc == nil {
			return nil
		}
		shot := svc.SourceShot
		if eid, ok := strings.CutPrefix(id, "explore:"); ok {
			id, shot = eid, svc.Shot
		}
		data, err := shot(bg, id)
		if err != nil {
			return nil
		}
		return dataImage(data)
	})
}

// keepPic is a keep's own picture, 1200 wide.
func (a *App) keepPic(id string) *favPic {
	thumbs := a.opt.Thumbs
	return a.favPicture("keep:"+id, func() image.Image {
		if thumbs != nil {
			return nil // the tests' thumbnails are bitmaps, without the picture to measure
		}
		img, _ := a.thumbImage(id, 0, 1200, true)
		return img
	})
}

// dataImage decodes a data: URL's picture.
func dataImage(data string) image.Image {
	_, b64, ok := strings.Cut(data, ";base64,")
	if !ok || !strings.HasPrefix(data, "data:image/") {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil
	}
	return decodeAny(raw)
}

// toneOf is how light a picture is, so that a white
// site rests dimmer than a dark one: the brightness it rests at and comes
// up to, 0.84 → 1 for a dark site, down to 0.58 → 0.74 for a white one.
func toneOf(img image.Image) (rest, lit float32) {
	small := image.NewNRGBA(image.Rect(0, 0, 24, 16))
	xdraw.ApproxBiLinear.Scale(small, small.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	var sum float64
	for i := 0; i < len(small.Pix); i += 4 {
		sum += 0.2126*float64(small.Pix[i]) + 0.7152*float64(small.Pix[i+1]) + 0.0722*float64(small.Pix[i+2])
	}
	light := float32(min(1, max(0, (sum/float64(24*16)/255-0.3)/0.4)))
	rest = 0.84 - 0.26*light
	return rest, rest + 0.16 - 0.1*light
}

// softOf is img as the pool under an author's words softens it (CSS's
// blur(18px) over a picture about a thousand wide), blurred once as it
// loads rather than on the GPU every frame: shrunk to 96 across, a Gaussian
// of 1.5 of those pixels, and drawn back up, whose stretch blurs the rest.
func softOf(img image.Image) *image.NRGBA {
	b := img.Bounds()
	w, h := 96, max(1, b.Dy()*96/max(1, b.Dx()))
	small := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(small, small.Bounds(), img, b, xdraw.Src, nil)
	return gaussian(small, 1.5)
}

// pool is p's softened copy cut to a pool of shade: the ellipse rx across
// and ry up from (cx, cy) in a box w×h that the picture covers, held at
// posY; wholly soft out to inner of the way, and gone by outer, as CSS's
// mask-image: radial-gradient(ellipse …, #000 inner, transparent outer).
// The cut is baked into the copy for each place it is drawn at, so that it
// is drawn as a picture rather than masked on the GPU every frame.
// ponytail: the places a picture was drawn at stay kept; a few per window size.
func (p *favPic) pool(w, h, posY, cx, cy, rx, ry, inner, outer float32) *ui.Bitmap {
	key := [9]float32{w, h, posY, cx, cy, rx, ry, inner, outer}
	if b := p.pools[key]; b != nil {
		return b
	}
	src := p.soft
	iw, ih := float32(src.Bounds().Dx()), float32(src.Bounds().Dy())
	scale := max(w/iw, h/ih)
	ox, oy := (w-iw*scale)*0.5, (h-ih*scale)*posY
	out := image.NewNRGBA(src.Bounds())
	copy(out.Pix, src.Pix)
	for j := range int(ih) {
		for i := range int(iw) {
			dx, dy := (ox+(float32(i)+0.5)*scale-cx)/rx, (oy+(float32(j)+0.5)*scale-cy)/ry
			d := float32(math.Sqrt(float64(dx*dx + dy*dy)))
			o := out.PixOffset(i, j)
			out.Pix[o+3] = uint8(float32(out.Pix[o+3]) * clampf((outer-d)/(outer-inner), 0, 1))
		}
	}
	b := ui.NewBitmap(out)
	p.pools[key] = b
	return b
}

// gaussian blurs m by a Gaussian of sigma pixels, its edges held (as a
// backdrop's blur does, without darkening them).
func gaussian(m *image.NRGBA, sigma float64) *image.NRGBA {
	r := int(math.Ceil(3 * sigma))
	k := make([]float64, 2*r+1)
	var sum float64
	for i := range k {
		d := float64(i - r)
		k[i] = math.Exp(-d * d / (2 * sigma * sigma))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	w, h := m.Bounds().Dx(), m.Bounds().Dy()
	pass := func(src *image.NRGBA, dx, dy int) *image.NRGBA {
		out := image.NewNRGBA(src.Bounds())
		for y := range h {
			for x := range w {
				var acc [4]float64
				for i, kv := range k {
					sx, sy := min(w-1, max(0, x+(i-r)*dx)), min(h-1, max(0, y+(i-r)*dy))
					o := src.PixOffset(sx, sy)
					for ch := range acc {
						acc[ch] += kv * float64(src.Pix[o+ch])
					}
				}
				o := out.PixOffset(x, y)
				for ch := range acc {
					out.Pix[o+ch] = uint8(min(255, max(0, acc[ch]+0.5)))
				}
			}
		}
		return out
	}
	return pass(pass(m, 1, 0), 0, 1)
}

// shelfPicture is what an author stands on: their own site's first screen;
// failing that, the picture of their latest keep that has one; failing
// that, their face. Nothing is asked for until the place is near the view.
func (a *App) shelfPicture(g *favGroup, near bool) (pic *favPic, site, waiting bool) {
	fs := a.favs()
	// A kind has no site: it stands on its newest keep's picture.
	switch {
	case g.kind != "":
	case !near && fs.pics["site:"+g.src.ID] == nil:
		return nil, false, true
	default:
		if sp := a.sitePic(g.src); sp == nil || !sp.done {
			return nil, true, true
		} else if sp.bmp != nil {
			return sp, true, false
		}
	}
	for _, art := range g.list {
		if art.Preview != nil && art.Preview.HasImage {
			kp := a.keepPic(art.ID)
			switch {
			case kp == nil || !kp.done:
				return nil, false, true
			case kp.bmp != nil:
				return kp, false, false
			}
			break
		}
	}
	return nil, false, false
}

// picFade is how far a picture has come in: over 300 ms the first time it
// shows, at once after a key.
func (a *App) picFade(c *ui.Context, p *favPic) float32 {
	if p == nil || p.bmp == nil {
		return 0
	}
	now := c.Now()
	if p.shown.IsZero() {
		p.shown = now
		if a.kbd {
			p.shown = now.Add(-motion.PictureIn)
		}
	}
	k := float32(now.Sub(p.shown)) / float32(motion.PictureIn)
	if k >= 1 {
		return 1
	}
	c.AnimationFrame()
	return motion.Ease(k)
}

// groundView is what an author stands on (Ground): their picture, dimmer at
// rest the lighter it is; with none, their face blown up and out of focus
// as a glow on the right; on its way, the surface breathing.
func (a *App) groundView(c *ui.Context, g *favGroup, pic *favPic, site, waiting bool, gh, bright, fade float32, soft *ui.Bitmap) {
	box := ui.Box(c.Key("ground")).Absolute().Left(0).Top(0).Right(0).Height(gh).Background(theme.Surface).IgnoreInput(true).AccessHidden(true)
	if pic == nil && !waiting {
		box.Children(func() {
			a.face(c, a.iconOf(g.src), "glow", 360).Absolute().RightPercent(8).Top(64).Blur(72).Opacity(0.55)
		})
		return
	}
	if waiting {
		box.Opacity(1 - 0.45*breathe(box.Loop("breathe", 2*motion.Breathe, ui.Linear)))
		return
	}
	box.Children(func() {
		picImage(c, pic.bmp, site, fade)
		if soft != nil {
			picImage(c, soft, site, fade)
		}
		// Dimmed to its brightness, the softened copy with it, as a brightness() filter under a backdrop's blur.
		if v := (1 - bright) * fade; v > 0.001 {
			ui.Box(c).Absolute().Fill().Background(theme.Darken).Opacity(v)
		}
	})
}

// posOf is where a picture is held as it covers its box: a site's first
// screen from its top, so its name and opening stay in view; a keep's
// picture a little above its middle.
func posOf(site bool) float32 {
	if site {
		return 0
	}
	return 0.3
}

// picImage draws bmp over its parent as the ground does, fade of the way in.
func picImage(c *ui.Context, bmp *ui.Bitmap, site bool, fade float32) {
	img := ui.Image(c, bmp).Fit(ui.Cover).ObjectPosition(0.5, posOf(site)).Absolute().Fill()
	if fade < 1 {
		img.Opacity(fade)
	}
}

// --- An author's keeps.

// tiles are keeps, two to a row (one in a narrow window); a single keep
// takes the whole row.
func (a *App) tiles(c *ui.Context, p page, label string, list []*library.Article, byline bool, cols int, quiet bool, stop string, live bool) {
	tracks := []ui.Track{ui.Fr(1)}
	if cols == 2 {
		tracks = append(tracks, ui.Fr(1))
	}
	ui.Grid(c.Key("tiles")).ColumnTracks(tracks...).Gap(24).Margin(8, 0, 0, 0).Role(ui.RoleList).Label(label).Children(func() {
		for _, art := range list {
			a.tile(c, p, art, byline, len(list) == 1 && cols > 1, cols, quiet, stop, live)
		}
	})
}

// tile is a keep (Tile): its own picture with its title written into the
// lower-left corner, or, without one, its own first page: the title and
// its opening paragraphs, fading out at the foot. Every keep is the same
// shape, so a row never jumps when one leaves.
func (a *App) tile(c *ui.Context, p page, art *library.Article, byline, wide bool, cols int, quiet bool, stop string, live bool) {
	fs := a.favs()
	key := "k:" + art.ID
	cell := ui.Box(c.Key(key)).FillWidth()
	if wide {
		cell.ColumnSpan(cols)
	}
	cell.Transition(a.listMove(c, quiet))
	hover := cell.Hovered()
	wants := art.Preview != nil && art.Preview.HasImage
	var pic *favPic
	if wants && (cell.InViewMargin(600, 0) || fs.pics["keep:"+art.ID] != nil) {
		pic = a.keepPic(art.ID)
	}
	pictured := wants && !(pic != nil && pic.done && pic.bmp == nil)
	warming := fs.warming[art.ID]
	var words []string
	if !pictured {
		if o := a.opening(art.ID, cell.InViewMargin(600, 0) && !warming); o != nil && len(o.paras) > 0 {
			words = o.paras
		} else if art.Preview != nil && art.Preview.Excerpt != nil && *art.Preview.Excerpt != "" {
			words = []string{*art.Preview.Excerpt}
		}
	}
	ratio := float32(1.5)
	if wide {
		ratio = 21.0 / 9
	}
	cell.Children(func() {
		b := ui.ButtonBase(c.Key("open")).Label(titleOf(art.Title)).FillWidth().AspectRatio(ratio).Radius(12).ClipX().ClipY().
			Background(theme.Surface).FocusRing(false).KeyShortcuts(0, ui.KeyEnter).KeyShortcuts(0, ui.KeyS).KeyShortcuts(0, ui.KeyDelete)
		if a.crossing(c, live) {
			b.Cache()
		}
		a.card(b, key, stop, live)
		r := b.LayoutBounds()
		if b.Clicked() && live {
			fs.cursor = key
			look := a.looks[art.ID]
			if look.style == nil {
				look = titleLook{tileTitle(wide), theme.Subtitle}
			}
			a.openAs(c, art.ID, r, look.style, look.size)
		}
		a.layInto(p, art.ID, r)
		if b.FocusVisible() {
			b.FocusRing(true).FocusRingOffset(4)
		}
		a.give(b, 0.99)
		var paper ui.Color
		if !pictured {
			// A page: the paper's edge and ground brighten under the pointer.
			ground, edge := theme.Surface, theme.LineSoft
			if hover {
				ground, edge = theme.PaperHover, theme.Line
			}
			paper = b.AnimateColor("bg", ground, a.ease(motion.Hover))
			b.Background(paper).InsetShadow(0, 0, 0, 1, b.AnimateColor("edge", edge, a.ease(motion.Hover)))
		}
		if warming {
			// Opacity only, so it keeps breathing under less motion.
			b.Opacity(1 - 0.45*breathe(b.Loop("breathe", 2*motion.Breathe, ui.Linear)))
		}
		when := func(muted bool) {
			color := theme.Text2
			if muted {
				color = theme.Muted
			}
			ui.Row(c.Key("when")).Gap(8).AlignItems(ui.Center).MinWidth(0).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(color).
				FontFeatures(theme.Numbers).Children(func() {
				d := articleDate(art, false)
				if byline {
					au := a.keeperOf(art)
					a.face(c, a.iconOf(au), "face", 16)
					ui.Text(c, nameOf(au)).FontWeight(500).TextColor(theme.Text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
					if d != "" {
						ui.Text(c, "·").AccessHidden(true)
					}
				}
				if d != "" {
					ui.Text(c, d).NoWrap()
				}
				a.progress(c, art)
			})
		}
		b.Children(func() {
			if pictured {
				w, h := r.W, r.H
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
				ph := ui.Box(c.Key("picture")).Absolute().Fill().IgnoreInput(true).AccessHidden(true)
				if pic == nil || !pic.done {
					ph.Opacity(1 - 0.45*breathe(ph.Loop("breathe", 2*motion.Breathe, ui.Linear)))
				}
				ph.Children(func() {
					if pic != nil && pic.bmp != nil {
						picImage(c, pic.bmp, false, fade)
						if lean != 1 {
							ph.TransformOrigin(0.5, 0.5).Scale(lean, lean)
						}
					}
				})
				// Softened behind its words (a backdrop's blur(12px)), from the lower-left corner.
				if pic != nil && pic.soft != nil && w > 0 {
					ui.Box(c.Key("soft")).Absolute().Fill().IgnoreInput(true).AccessHidden(true).Children(func() {
						picImage(c, pic.pool(w, h, posOf(false), 0, h, 0.72*w, 0.5*h, 0.4, 1), false, fade)
					})
				}
				if w > 0 {
					a.shade(c, w, h, 1.2*w, 0.82*h, [][2]float32{{0, 0.9}, {0.42, 0.78}, {1, 0}}).Absolute().Fill()
				}
				ui.Column(c.Key("words")).Absolute().Left(0).Right(0).Bottom(0).MaxWidth(560).Gap(8).Padding(24).Children(func() {
					when(false)
					style := tileTitle(wide)
					t := style(c, titleOf(art.Title))
					a.end(t, p, "title:"+art.ID)
					a.looks[art.ID] = titleLook{style, map[bool]float32{true: 32, false: 20}[wide]}
				})
			} else {
				pad := float32(32)
				if wide {
					pad = 48
				}
				// The words are cut 24 above the foot and fade out over the 88 above that into the paper: a band clear
				// of the rounded corners (their curves and edges reach 19 in) and the paper's colour laid over them,
				// rather than a mask of the words, which is drawn on the CPU again wherever the page moves.
				ui.Column(c.Key("page")).Absolute().Left(0).Right(0).Top(20).Bottom(24).Gap(8).Padding(pad-20, pad, pad, pad).ClipY().IgnoreInput(true).Children(func() {
					when(true)
					style := paperTitle(wide)
					t := style(c, titleOf(art.Title)).Margin(4, 0, 8, 0).Shrink(0)
					a.end(t, p, "title:"+art.ID)
					a.looks[art.ID] = titleLook{style, map[bool]float32{true: 32, false: 24}[wide]}
					for i, said := range words {
						e := ui.Text(c.Key(i), said).FontSize(theme.Item).LineHeight(1.8).TextColor(theme.Text2).Pretty().Shrink(0)
						if wide {
							e.MaxWidth(660)
						}
					}
				})
				ui.Box(c).Absolute().Left(1).Right(1).Bottom(24).Height(88).IgnoreInput(true).LinearGradient(ui.LinearGradient{Angle: 180,
					Stops: []ui.GradientStop{{Color: paper.Alpha(0), At: 0}, {Color: paper, At: 1}}})
			}
		})
		// The inner hairline over the picture, so a dark page never melts into the ground.
		edge := theme.LineSoft
		if hover {
			edge = theme.EdgeHover
		}
		ui.Box(c.Key("edge")).Absolute().Fill().Radius(12).IgnoreInput(true).AccessHidden(true).InsetShadow(0, 0, 0, 1, b.AnimateColor("hairline", edge, a.ease(motion.Hover)))
		ctx := fs.ctx == key
		menu := ui.ContextMenuBase(c, b, &ctx)
		a.keepMenu(c, menu, p, art, true)
		fs.ctx = keepOpen(fs.ctx, key, ctx)
		a.moreMenu(c, key, locales.FavoritesArticleActions(titleOf(art.Title)), hover || cell.FocusWithin(), func(m ui.MenuParts) { a.keepMenu(c, m, p, art, false) }).
			Absolute().Top(12).Right(12).ZIndex(1)
	})
}

// tileTitle is a pictured keep's title: 20, or 32 across the whole row.
func tileTitle(wide bool) func(*ui.Context, string) ui.Element {
	return func(c *ui.Context, s string) ui.Element {
		t := ui.Text(c, s).Font(reader.TitleFont(s)).FontSize(theme.Subtitle).LineHeight(1.4).FontWeight(600).TextColor(theme.Text).Balance().MaxLines(3).Ellipsis("…")
		if wide {
			t.FontSize(theme.Headline).LineHeight(1.3).LetterSpacing(-0.012 * 32)
		}
		return t
	}
}

// paperTitle is a page's title: 24, or 32 across the whole row.
func paperTitle(wide bool) func(*ui.Context, string) ui.Element {
	return func(c *ui.Context, s string) ui.Element {
		t := ui.Text(c, s).FontSize(theme.Title).LineHeight(1.3).FontWeight(650).LetterSpacing(-0.01 * 24).TextColor(theme.Text).Balance().MaxLines(3).Ellipsis("…")
		if wide {
			t.FontSize(theme.Headline).LetterSpacing(-0.012 * 32).MaxWidth(760)
		}
		return t
	}
}

// --- A keep's first page.

// favOpening is a keep's opening paragraphs, about a page's worth, as the
// article is kept.
type favOpening struct {
	done  bool
	paras []string
}

// opening asks for a keep's opening once, when ask says it is wanted.
func (a *App) opening(id string, ask bool) *favOpening {
	fs := a.favs()
	if o := fs.openings[id]; o != nil || !ask || a.warming || a.opt.Reading == nil {
		return o
	}
	o := &favOpening{}
	fs.openings[id] = o
	svc := a.opt.Reading
	a.do(func() func() {
		content, err := svc.Read(bg, id, false, "")
		var paras []string
		if err == nil {
			paras = openingOf(content.HTML)
		}
		return func() { o.done, o.paras = true, paras }
	})
	return o
}

// openingOf is the text of the article's
// paragraphs, list items and quotes that hold no paragraph, in order, until
// a little past 720 characters. Read as data: nothing in it runs or loads.
func openingOf(htmlText string) []string {
	doc, err := html.Parse(strings.NewReader(htmlText))
	if err != nil {
		return nil
	}
	var out []string
	length := 0
	var walk func(n *html.Node) bool
	holdsP := func(n *html.Node) bool {
		var found bool
		var look func(*html.Node)
		look = func(n *html.Node) {
			for k := n.FirstChild; k != nil && !found; k = k.NextSibling {
				if k.Type == html.ElementNode && k.Data == "p" {
					found = true
				}
				look(k)
			}
		}
		look(n)
		return found
	}
	walk = func(n *html.Node) bool {
		if n.Type == html.ElementNode && (n.Data == "p" || (n.Data == "li" || n.Data == "blockquote") && !holdsP(n)) {
			if text := strings.Join(strings.Fields(textOf(n)), " "); text != "" {
				out = append(out, text)
				if length += utf8.RuneCountInString(text); length > 720 {
					return false
				}
			}
		}
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			if !walk(k) {
				return false
			}
		}
		return true
	}
	walk(doc)
	return out
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var add func(*html.Node)
	add = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			add(k)
		}
	}
	add(n)
	return b.String()
}

// warmKeeps reads ahead, one batch at a time, the keeps drawn whose feed
// gave only a stub, so that their first page shows how they open.
func (a *App) warmKeeps(drawn []*library.Article) {
	fs := a.favs()
	if len(fs.warming) > 0 || a.warming || a.opt.Reading == nil {
		return
	}
	var cold []string
	for _, art := range drawn {
		pv := art.Preview
		if (pv == nil || (pv.Excerpt == nil || *pv.Excerpt == "") && !pv.HasImage) && !fs.warmed[art.ID] && !slices.Contains(cold, art.ID) {
			cold = append(cold, art.ID)
		}
		if len(cold) == 6 {
			break
		}
	}
	if len(cold) == 0 {
		return
	}
	for _, id := range cold {
		fs.warmed[id], fs.warming[id] = true, true
	}
	svc, store := a.opt.Reading, a.opt.Store
	a.do(func() func() {
		for _, id := range cold {
			svc.Read(bg, id, false, "")
		}
		lib, err := store.Library()
		return func() {
			clear(fs.warming)
			for _, id := range cold {
				delete(fs.openings, id) // read again, now that it is kept
			}
			if err == nil {
				a.accept(lib)
			}
		}
	})
}

// --- Going in and out, letting go.

// favFirst asks the focus for the first card the page builds.
const favFirst = "\x00first"

// enterShelf goes into an author: their picture folds up into the head of
// their page, their face and name with it; from the keys, the focus goes
// to their first keep.
func (a *App) enterShelf(c *ui.Context, id string) {
	fs := a.favs()
	fs.keeps = ui.ScrollState{}
	if a.goTo(c, page{"favorites", id}, motion.Deep, []page{{kind: "favorites"}}) != nil && a.kbd {
		fs.focus = favFirst
	}
}

// letGo lets go of one keep; an author's last, on their own page, goes as
// their shelf does.
func (a *App) letGo(c *ui.Context, p page, art *library.Article) {
	fs := a.favs()
	if p.id != "" && strings.TrimSpace(fs.query) == "" {
		_, groups := a.kept("")
		for _, g := range groups {
			if g.src.ID == p.id && len(g.list) == 1 {
				a.letGoShelf(c, g)
				return
			}
		}
	}
	if a.kbd {
		fs.handOn("k:" + art.ID)
	}
	a.unkeep(c, []*library.Article{art}, nil)
}

// letGoShelf lets go of an author's whole shelf. From their own page, the
// page folds back into the shelf first, so that you see them leave it.
func (a *App) letGoShelf(c *ui.Context, g *favGroup) {
	fs := a.favs()
	if a.page == (page{"favorites", g.src.ID}) {
		fs.pending, fs.pendingKbd = g.src.ID, a.kbd
		a.back(c)
		return
	}
	if a.kbd {
		fs.handOn("a:" + g.src.ID)
	}
	a.unkeep(c, g.list, g.src)
}

// letGoPending lets go of the author asked for once the page has folded
// back to them.
func (a *App) letGoPending(c *ui.Context, groups []*favGroup) {
	fs := a.favs()
	if a.next != nil {
		return
	}
	if wait := motion.Back.Crossing().Travel - c.Now().Sub(a.movedAt); wait > 0 && !fs.pendingKbd && !a.still(c) {
		c.After(wait)
		return
	}
	id := fs.pending
	fs.pending = ""
	for _, g := range groups {
		if g.src.ID == id {
			if fs.pendingKbd {
				fs.handOn("a:" + id)
			}
			a.unkeep(c, g.list, g.src)
		}
	}
}

// handOn says where the keyboard goes once card key has left: the next card,
// else the one before.
func (fs *favState) handOn(key string) {
	at := slices.IndexFunc(fs.cards, func(c favCard) bool { return c.key == key })
	switch {
	case at < 0:
	case at+1 < len(fs.cards):
		fs.focus, fs.cursor = fs.cards[at+1].key, fs.cards[at+1].key
	case at > 0:
		fs.focus, fs.cursor = fs.cards[at-1].key, fs.cards[at-1].key
	}
}

// unkeep lets go of keeps at once (letGo); the notice that follows
// offers 撤销, which puts each back with the time it was kept, so in its old
// place. Letting go of a shelf (whose), the author's face lifts off the page
// onto the notice and goes back on 撤销; from the keyboard, or under less
// motion, nothing flies.
func (a *App) unkeep(c *ui.Context, list []*library.Article, whose *library.Source) {
	if len(list) == 0 {
		return
	}
	fs := a.favs()
	src := a.keeperOf(list[0])
	ic := a.iconOf(src)
	text := locales.FavoritesRemovedFrom(nameOf(src), len(list))
	if len(list) == 1 {
		t := []rune(titleOf(list[0].Title))
		if len(t) > 18 {
			t = append(t[:17], '…')
		}
		text = locales.FavoritesRemoved(string(t))
	}
	now := web.Now()
	off := library.SetFavorites{}
	back := library.SetFavorites{}
	for _, art := range list {
		off.Items = append(off.Items, library.FavoriteTime{ID: art.ID})
		at := art.FavoritedAt
		if at == nil {
			at = &now
		}
		back.Items = append(back.Items, library.FavoriteTime{ID: art.ID, FavoritedAt: at})
	}
	var from ui.Rect
	moving := whose != nil && ic != nil && !a.still(c) && !a.kbd
	if moving {
		from = a.shelfFaces[whose.ID]
		moving = from.W > 0
	}
	id := ""
	if whose != nil {
		id = whose.ID
	}
	a.mutate(off, func() {
		fs.leftAt = time.Now()
		it := toastItem{text: text, face: ic, undo: back}
		if moving {
			a.trip(ic.toned, from, "notice")
			it.onUndo = func() {
				if r := a.noticeFace; r.W > 0 {
					a.trip(ic.toned, r, "shelf:"+id)
				}
			}
		}
		a.toast.add(it)
	})
}

// favKeys are 收藏's keys: J and K, ← and →, the
// next and last card; ↓ and ↑ the nearest card in the row below or above;
// ⌫, Delete or S let go of a keep, ⌘⌫ of an author's whole shelf; Esc
// leaves an author.
func (a *App) favKeys(c *ui.Context) {
	fs := a.favs()
	if c.Shortcut(0, ui.KeyEscape) {
		switch {
		case fs.query != "":
			fs.query, fs.search = "", false
		case a.page.id != "":
			// Back on the shelves, the focus goes to the author left.
			fs.focus, fs.cursor = "a:"+a.page.id, "a:"+a.page.id
			a.back(c)
		}
		return
	}
	cards := fs.cards
	if len(cards) == 0 {
		return
	}
	at := slices.IndexFunc(cards, func(c favCard) bool { return c.key == fs.focused })
	from := at
	if from < 0 {
		from = slices.IndexFunc(cards, func(c favCard) bool { return c.key == fs.cursor })
	}
	to := -1
	switch {
	case c.Shortcut(0, ui.KeyJ):
		to = from + 1
	case c.Shortcut(0, ui.KeyK):
		to = max(0, from-1)
	case at >= 0 && c.Shortcut(0, ui.KeyRight):
		to = at + 1
	case at >= 0 && c.Shortcut(0, ui.KeyLeft):
		to = at - 1
	case at >= 0 && c.Shortcut(0, ui.KeyDown):
		to = nearestCard(cards, at, true)
	case at >= 0 && c.Shortcut(0, ui.KeyUp):
		to = nearestCard(cards, at, false)
	case at >= 0 && strings.HasPrefix(cards[at].key, "k:") && (c.Shortcut(0, ui.KeyS) || c.Shortcut(0, ui.KeyDelete) || c.Shortcut(0, ui.KeyBackspace)):
		if art := a.article(strings.TrimPrefix(cards[at].key, "k:")); art != nil {
			a.letGo(c, a.page, art)
		}
		return
	case at >= 0 && strings.HasPrefix(cards[at].key, "a:") && c.Shortcut(ui.Cmd, ui.KeyBackspace):
		_, groups := a.kept("")
		for _, g := range groups {
			if "a:"+g.src.ID == cards[at].key {
				a.letGoShelf(c, g)
			}
		}
		return
	default:
		return
	}
	if to >= 0 && to < len(cards) {
		fs.focus, fs.cursor = cards[to].key, cards[to].key
	}
}

// nearestCard is the card nearest the middle of card at in the row below
// (or above): the cards whose top is the first under its foot.
func nearestCard(cards []favCard, at int, down bool) int {
	a := cards[at].box
	x := a.X + a.W/2
	edge, best := float32(0), -1
	found := false
	for _, c := range cards {
		r := c.box
		if down && r.Y >= a.Y+a.H-1 && (!found || r.Y < edge) {
			edge, found = r.Y, true
		}
		if !down && r.Y+r.H <= a.Y+1 && (!found || r.Y+r.H > edge) {
			edge, found = r.Y+r.H, true
		}
	}
	if !found {
		return -1
	}
	dist := float32(0)
	for i, c := range cards {
		r := c.box
		y := r.Y
		if !down {
			y = r.Y + r.H
		}
		if y-edge > 2 || edge-y > 2 {
			continue
		}
		d := r.X + r.W/2 - x
		if d < 0 {
			d = -d
		}
		if best < 0 || d < dist {
			best, dist = i, d
		}
	}
	return best
}
