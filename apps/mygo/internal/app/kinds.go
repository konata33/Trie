package app

import (
	"slices"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/theme"
)

// What is being made (落地页, 作品集, 动效, 字体, 品牌): under the design
// room's front page, a door for each, standing on its newest work and
// turning through the next while pointed at; behind each, a wall of the
// kind's works from every gallery that shelves them, newest first. A
// gallery shelves its works by the catalog (explore.Shelf), never by a
// guess at one work.

var kindOrder = []string{"landing", "portfolio", "motion", "type", "brand"}

func kindName(k string) string {
	switch k {
	case "landing":
		return locales.KindLanding()
	case "portfolio":
		return locales.KindPortfolio()
	case "motion":
		return locales.KindMotion()
	case "type":
		return locales.KindType()
	case "brand":
		return locales.KindBrand()
	}
	return k
}

// kindSources are the galleries a kind's works come from, the one with the
// newest first.
func kindSources(works []exWork) []*explore.Entry {
	var out []*explore.Entry
	for _, w := range works {
		seen := false
		for _, e := range out {
			seen = seen || e == w.entry
		}
		if !seen {
			out = append(out, w.entry)
		}
	}
	return out
}

// kindShelf is the doors, in one row as wide as the stream under them; a
// kind with no work yet has none.
func (a *App) kindShelf(c *ui.Context, g exGeometry) {
	var kinds []string
	for _, k := range kindOrder {
		if len(a.ex.kinds[k]) > 0 {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 {
		return
	}
	n := float32(len(kinds))
	w := (g.cw - 32 - (n-1)*16) / n
	// A work shelved under several kinds stands on one door only: each door leads with the newest work no door before it shows.
	used := map[string]bool{}
	ui.Row(c.Key("kinds")).Gap(16).Margin(0, 0, 56, 0).Role(ui.RoleNavigation).Label(locales.ExploreKinds()).Children(func() {
		for _, k := range kinds {
			works := a.ex.kinds[k]
			lead := max(0, slices.IndexFunc(works, func(x exWork) bool { return !used[x.url] }))
			picks := append([]exWork{works[lead]}, slices.Delete(slices.Clone(works), lead, lead+1)...)
			picks = picks[:min(5, len(picks))]
			used[picks[0].url] = true
			a.kindDoor(c, k, picks, w, w*5/4)
		}
	})
}

// kindDoor is one kind's door: a poster of its newest work, the next ones
// turning over it while pointed at; at its foot, on the band, the kind's
// name and the faces of the galleries behind it.
func (a *App) kindDoor(c *ui.Context, k string, picks []exWork, w, h float32) {
	ex := a.ex
	door := ui.Box(c.Key(k)).Size(w, h).Radius(10)
	a.intoView(door, 0)
	hover := door.Hovered()
	turn := a.turning(c, "kind:"+k, picks, hover)
	door.Children(func() {
		b := ui.ButtonBase(c.Key("open")).Label(kindName(k)).Absolute().Fill().Radius(10).FocusRing(false)
		if ex.focus == "kind:"+k {
			ex.focus = ""
			b.Focus()
		}
		if b.FocusVisible() {
			b.FocusRing(true)
		}
		if !a.still(c) {
			s := float32(1)
			if b.Pressed() && !a.kbd {
				s = 0.99
			}
			p := door.AnimateTo("press", s, ui.Animation{Duration: motion.Press, Ease: motion.Ease})
			door.TransformOrigin(0.5, 0.5).Scale(p, p)
		}
		zoom := a.zoom(c, door, hover)
		var shown *exPic
		b.Children(func() { shown = a.turned(c, picks[0].entry, turn, zoom, w, h) })
		if b.Clicked() {
			a.openKind(c, k)
		}
		ui.Box(c.Key("cap")).Absolute().Fill().IgnoreInput(true).Children(func() {
			band(c, h-120, 120, 72, shown, 0, w, h, 10)
			ui.Row(c).Absolute().Left(16).Right(14).Bottom(14).Gap(8).AlignItems(ui.End).Children(func() {
				ui.Text(c, kindName(k)).FontSize(24).LineHeight(1.2).FontWeight(650).LetterSpacing(-0.01 * 24).TextColor(theme.Text).
					SingleLine().Grow(1).Shrink(1).MinWidth(0)
				a.kindFaces(c, kindSources(ex.kinds[k]), 18)
			})
		})
	})
}

// kindFaces are the galleries behind a kind, their faces overlapping, four
// at most, each ringed in the window's ground.
func (a *App) kindFaces(c *ui.Context, from []*explore.Entry, size float32) {
	ui.Row(c.Key("faces")).AccessHidden(true).Padding(0, 0, 3, 0).Children(func() {
		for i, e := range from[:min(4, len(from))] {
			ring := ui.Box(c.Key(e.ID)).Radius(size/2 + 2).Background(theme.Window).Padding(2).Shrink(0)
			if i > 0 {
				ring.Margin(0, 0, 0, -size/3)
			}
			ring.Children(func() { a.exFace(c, e, size, false, 9) })
		}
	})
}

// openKind goes into a kind's wall from its door; the door takes the focus
// back when the keys come back.
func (a *App) openKind(c *ui.Context, k string) {
	a.ex.opener = "kind:" + k
	a.goTo(c, page{"kind", k}, motion.Deep, append(a.from[:len(a.from):len(a.from)], a.page))
}

// kindWall is what a kind's page keeps: its scroll, and how many works it
// shows so far.
type kindWall struct {
	scroll ui.ScrollState
	shown  int
	endY   float32
}

// kindPage is a kind's wall: its name, the galleries behind it, then its
// works three across, newest first, a picture each, the title rising over
// it when pointed at; the next twelve come as the end nears.
func (a *App) kindPage(c *ui.Context, k string, live bool) {
	ex := a.ex
	if ex.walls == nil {
		ex.walls = map[string]*kindWall{}
	}
	kw := ex.walls[k]
	if kw == nil {
		kw = &kindWall{shown: 12}
		ex.walls[k] = kw
	}
	works := ex.kinds[k]
	n := min(kw.shown, len(works))
	sc := ui.Scroll(c.Key("scroll")).TrackScroll(&kw.scroll).Grow(1).FillWidth().Scrollbars(false)
	if live {
		a.scrolling = &kw.scroll
	}
	cw := (min(1200, a.area.W) - 64 - 2*16) / 3
	sc.Children(func() {
		frame(c).Padding(0, 32, 140, 32).Children(func() {
			a.band(c, func() { a.backLink(c, backLabel(a, a.backTo())) }, func() {})
			ui.Row(c.Key("head")).Margin(16, 0, 40, 0).Gap(16).AlignItems(ui.End).Children(func() {
				ui.Text(c, kindName(k)).FontSize(40).LineHeight(1.15).FontWeight(650).LetterSpacing(-0.02 * 40).TextColor(theme.Text).
					Role(ui.RoleHeading).Level(1).Grow(1)
				from := kindSources(works)
				ui.Row(c.Key("from")).Gap(8).Padding(0, 0, 6, 0).Children(func() {
					for _, e := range from {
						f := a.exFace(c, e, 24, false, 11)
						a.tip(c, f, e.Name, false)
					}
				})
			})
			ui.Column(c.Key("wall")).Gap(16).Role(ui.RoleList).Label(kindName(k)).Children(func() {
				for row := 0; row*3 < n; row++ {
					ui.Row(c.Key(row)).Gap(16).Children(func() {
						for col := range 3 {
							if i := row*3 + col; i < n {
								cell := ui.Box(c.Key(works[i].key())).Role(ui.RoleListItem)
								a.intoView(cell, 0)
								cell.Children(func() { a.frame(c, works[i], cw, cw*10/16) })
							}
						}
					})
				}
			})
			if n < len(works) {
				end := ui.Box(c.Key("end")).Height(1).FillWidth().AccessHidden(true)
				if live && !a.warming {
					if y := end.LayoutBounds().Y; end.InViewMargin(0, 0, 600, 0) && y != kw.endY {
						kw.endY = y
						kw.shown += 12
						c.Invalidate()
					}
				}
			}
		})
	})
}

// kindsOf is what a kept article is shelved under: what it was kept with
// from Explore; else what Explore shelves it under now; else, for a
// gallery's own feed subscribed to, the gallery's shelves its title is on.
// An article none of these places is a writer's, kept by them.
func (a *App) kindsOf(art *library.Article) []string {
	if len(art.Kinds) > 0 {
		return art.Kinds
	}
	if ks := a.ex.kindOf[art.URL]; len(ks) > 0 {
		return ks
	}
	src := a.source(art.SourceID)
	if src == nil {
		return nil
	}
	var out []string
	for _, e := range a.ex.sites {
		if len(e.Shelves) == 0 || !(src.FeedURL != nil && e.FeedURL != nil && *src.FeedURL == *e.FeedURL || exHost(src.HomeURL) == exHost(e.HomeURL)) {
			continue
		}
		for _, sh := range e.Shelves {
			if sh.Holds(art.Title) && !slices.Contains(out, sh.Kind) {
				out = append(out, sh.Kind)
			}
		}
	}
	return out
}

// favBoard is 收藏's first screen when designs are kept: one board of the
// kinds kept, edge to edge from the window's top, the kind kept most taking
// the left and the others sharing the right, a hairline of the window's
// ground between them; each block stands on its newest keep's picture, how
// many and its name in its lower-left corner. Its foot sinks into the
// window's ground, where the first writer kept from rises; alone, it fills
// the screen.
func (a *App) favBoard(c *ui.Context, p page, kinds []*favGroup, alone bool, stop string, live bool) {
	_, vh := c.Size()
	k := sceneFirst
	if alone {
		k = sceneOnly
	}
	h, w := sceneHeight(k, vh), a.area.W
	sorted := slices.Clone(kinds)
	slices.SortStableFunc(sorted, func(x, y *favGroup) int { return len(y.list) - len(x.list) })
	rects := boardRects(len(sorted), w, h, 4)
	// A keep shelved under several kinds stands on one block only, as far as there are keeps enough: the kinds with the
	// fewest pictured keeps choose first, each its newest keep no block has chosen.
	pictured := func(art *library.Article) bool { return art.Preview != nil && art.Preview.HasImage }
	order := slices.Clone(sorted)
	slices.SortStableFunc(order, func(x, y *favGroup) int {
		return len(slices.DeleteFunc(slices.Clone(x.list), func(a *library.Article) bool { return !pictured(a) })) -
			len(slices.DeleteFunc(slices.Clone(y.list), func(a *library.Article) bool { return !pictured(a) }))
	})
	used, cover := map[string]bool{}, map[*favGroup]*favGroup{}
	for _, g := range order {
		cover[g] = g
		if lead := slices.IndexFunc(g.list, func(art *library.Article) bool { return !used[art.ID] && pictured(art) }); lead >= 0 {
			used[g.list[lead].ID] = true
			cover[g] = &favGroup{src: g.src, kind: g.kind, list: append([]*library.Article{g.list[lead]}, slices.Delete(slices.Clone(g.list), lead, lead+1)...)}
		}
	}
	ui.Box(c.Key("board")).FillWidth().Height(h).Role(ui.RoleList).Label(locales.FavoritesKinds()).Children(func() {
		for i, g := range sorted {
			a.boardBlock(c, p, cover[g], rects[i], i == 0, rects[i].Y+rects[i].H >= h-0.5, stop, live)
		}
		// Under the floating band.
		ui.Box(c).Absolute().Left(0).Right(0).Top(0).Height(132).IgnoreInput(true).AccessHidden(true).LinearGradient(ui.LinearGradient{Angle: 180,
			Stops: []ui.GradientStop{{Color: theme.Window.Alpha(0.9), At: 0}, {Color: theme.Window.Alpha(0.9), At: 52.0 / 132}, {Color: theme.Window.Alpha(0), At: 1}}})
	})
}

// boardRects lays n blocks over a board w×h, gap apart: one fills it; two
// share it, the first wider; more give the first the left 56% and share
// the right, two stacked, a wide one over two, or two by two.
func boardRects(n int, w, h, gap float32) []ui.Rect {
	switch n {
	case 0:
		return nil
	case 1:
		return []ui.Rect{{W: w, H: h}}
	case 2:
		l := float32(int(w * 0.58))
		return []ui.Rect{{W: l, H: h}, {X: l + gap, W: w - l - gap, H: h}}
	}
	l := float32(int(w * 0.56))
	x, rw := l+gap, w-l-gap
	hh, hw := (h-gap)/2, (rw-gap)/2
	out := []ui.Rect{{W: l, H: h}}
	switch n {
	case 3:
		out = append(out, ui.Rect{X: x, W: rw, H: hh}, ui.Rect{X: x, Y: hh + gap, W: rw, H: hh})
	case 4:
		out = append(out, ui.Rect{X: x, W: rw, H: hh}, ui.Rect{X: x, Y: hh + gap, W: hw, H: hh}, ui.Rect{X: x + hw + gap, Y: hh + gap, W: hw, H: hh})
	default:
		out = append(out, ui.Rect{X: x, W: hw, H: hh}, ui.Rect{X: x + hw + gap, W: hw, H: hh},
			ui.Rect{X: x, Y: hh + gap, W: hw, H: hh}, ui.Rect{X: x + hw + gap, Y: hh + gap, W: hw, H: hh})
	}
	return out[:min(n, len(out))]
}

// boardBlock is one kind on the board: its newest keep's picture, as bright
// as the picture is and brighter when pointed at, drifting closer; how many
// and its name over a shade in the lower-left corner. It opens the kind's
// keeps, its picture folding into their page's head; ⌘⌫ and its menu let
// go of them all.
func (a *App) boardBlock(c *ui.Context, p page, g *favGroup, r ui.Rect, lead, foot bool, stop string, live bool) {
	fs := a.favs()
	key := "a:" + g.src.ID
	box := ui.Box(c.Key(key)).Absolute().Left(r.X).Top(r.Y).Size(r.W, r.H).ClipX().ClipY()
	a.share(c, box, p, favPicMatch{g.src.ID}, live)
	hover := box.Hovered()
	ctx := fs.ctx == key
	menu := ui.ContextMenuBase(c, box, &ctx)
	a.shelfMenu(c, menu, g, true)
	fs.ctx = keepOpen(fs.ctx, key, ctx)
	pic, _, waiting := a.shelfPicture(g, true)
	box.Children(func() {
		b := ui.ButtonBase(c.Key("enter")).Label(locales.FavoritesWriterCount(nameOf(g.src), len(g.list))).Absolute().Fill().FocusRing(false).
			KeyShortcuts(0, ui.KeyEnter).KeyShortcuts(ui.Cmd, ui.KeyBackspace)
		a.card(b, key, stop, live)
		if b.Clicked() && live {
			fs.cursor = key
			a.enterShelf(c, g.src.ID)
		}
		if b.FocusVisible() {
			b.FocusRing(true).FocusRingOffset(-3)
		}
		zoom := a.zoom(c, box, hover)
		b.Children(func() {
			ui.Box(c).Absolute().Fill().Background(theme.Surface)
			switch {
			case pic != nil && pic.bmp != nil:
				img := ui.Image(c, pic.bmp).Fit(ui.Cover).ObjectPosition(0.5, posOf(false)).Absolute()
				if fade := a.picFade(c, pic); fade < 1 {
					img.Opacity(fade)
				}
				grown(img, r.W, r.H, zoom)
				bright := pic.rest
				if hover || b.FocusVisible() {
					bright = pic.lit
				}
				if v := 1 - box.AnimateTo("bright", bright, a.ease(motion.Lean)); v > 0.001 {
					ui.Box(c).Absolute().Fill().Background(theme.Darken).Opacity(v)
				}
			case waiting && !a.still(c):
				a.breathing(c, theme.Hover)
			}
		})
		size := float32(24)
		if lead {
			size = 40
		}
		// The shade under the words; a block on the board's foot sinks there into the window's ground.
		end := theme.Window.Alpha(0.85)
		if foot {
			end = theme.Window
		}
		ui.Box(c.Key("shade")).Absolute().Left(0).Right(0).Bottom(0).Height(min(r.H, 200)).IgnoreInput(true).AccessHidden(true).
			LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{{Color: theme.Window.Alpha(0), At: 0}, {Color: end, At: 1}}})
		ui.Column(c.Key("words")).Absolute().Left(32).Right(24).Bottom(28).Gap(6).IgnoreInput(true).Children(func() {
			ui.Text(c, locales.FavoritesCount(len(g.list))).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2).FontFeatures(theme.Numbers)
			ui.Text(c, nameOf(g.src)).FontSize(size).LineHeight(1.15).FontWeight(650).LetterSpacing(-0.015 * size).TextColor(theme.Text).
				MaxLines(2).Balance().Role(ui.RoleHeading).Level(2)
		})
	})
}
