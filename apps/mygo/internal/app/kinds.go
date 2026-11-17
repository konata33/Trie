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
	ui.Row(c.Key("kinds")).Gap(16).Margin(0, 0, 56, 0).Role(ui.RoleNavigation).Label(locales.ExploreKinds()).Children(func() {
		for _, k := range kinds {
			a.kindDoor(c, k, w, w*5/4)
		}
	})
}

// kindDoor is one kind's door: a poster of our own, the same on every
// visit whatever the galleries showed last: the kind's subject drawn large
// in the icons' hand and cut by the door's edges, the guide line its makers
// watch running across it; its lower part sinks into the plate, its name
// over it. Pointed at, the plate comes up, the lines brighten and the fill
// lights.
func (a *App) kindDoor(c *ui.Context, k string, w, h float32) {
	ex := a.ex
	door := ui.Box(c.Key(k)).Size(w, h).Radius(10)
	a.intoView(door, 0)
	door.Children(func() {
		b := ui.ButtonBase(c.Key("open")).Label(kindName(k)).Absolute().Fill().Radius(10).ClipX().ClipY().FocusRing(false)
		if ex.focus == "kind:"+k {
			ex.focus = ""
			b.Focus()
		}
		focus := b.FocusVisible()
		if focus {
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
		lit := b.Hovered() || focus
		ground, edge, ink, fill := theme.Surface, theme.LineSoft, theme.Muted, theme.IconFill
		if lit {
			ground, edge, ink, fill = theme.Elevated, theme.Line, theme.Text2, theme.Accent
		}
		ease := a.ease(motion.Hover)
		ground = b.AnimateColor("bg", ground, ease)
		b.Background(ground).InsetShadow(0, 0, 0, 1, b.AnimateColor("edge", edge, ease))
		if b.Clicked() {
			a.openKind(c, k)
		}
		art := doorArt()[k]
		b.Children(func() {
			ui.Icon(c, art.accent).Absolute().Fill().AccessHidden(true).TextColor(b.AnimateColor("fill", fill, a.ease(motion.State)))
			ui.Icon(c, art.ink).Absolute().Fill().AccessHidden(true).TextColor(b.AnimateColor("ink", ink, ease))
			// Whatever runs under the name sinks into the plate first.
			ui.Box(c.Key("sink")).Absolute().Left(0).Right(0).Bottom(0).Height(h * 0.5).IgnoreInput(true).LinearGradient(ui.LinearGradient{Angle: 180,
				Stops: []ui.GradientStop{{Color: ground.Alpha(0), At: 0}, {Color: ground.Alpha(0.94), At: 0.45}, {Color: ground, At: 1}}})
			ui.Text(c, kindName(k)).FontSize(theme.Title).LineHeight(1.2).FontWeight(650).LetterSpacing(-0.01 * theme.Title).TextColor(theme.Text).
				MaxLines(2).Absolute().Left(18).Right(16).Bottom(18)
		})
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
		if len(e.Shelves) == 0 || !sameSite(src, e) {
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

// gallery reports whether src is a design gallery's, whose keeps are designs
// even where it shelves none of them by kind.
func (a *App) gallery(src *library.Source) bool {
	return slices.ContainsFunc(a.ex.sites, func(e *explore.Entry) bool { return len(e.Works) > 0 && sameSite(src, e) })
}

// sameSite reports whether a library source is the site an Explore entry is:
// the same feed, or the same host.
func sameSite(src *library.Source, e *explore.Entry) bool {
	return src.FeedURL != nil && e.FeedURL != nil && *src.FeedURL == *e.FeedURL || src.HomeURL != "" && exHost(src.HomeURL) == exHost(e.HomeURL)
}
