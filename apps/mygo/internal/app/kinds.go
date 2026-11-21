package app

import (
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/theme"
)

// What is being made (落地页, 作品集, 动效, 字体, 品牌): under the design
// room's front page, a door for each, a drawing of its own on it (doors.go);
// behind each, a wall of the kind's works from every gallery that shelves
// them, newest first. A gallery shelves its works by the catalog
// (explore.Shelf), never by a guess at one work.

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

// kindShelf is the doors, in one row as wide as the stream under them, an
// index on the page rather than cards: no ground or edge of their own, a
// hairline above and below the row and one between its places; a kind with
// no work yet has none. live is false for Explore built for its exit.
func (a *App) kindShelf(c *ui.Context, g exGeometry, live bool) {
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
	w := g.cw / n
	h := w * 6 / 7
	rule := func(x, y, rw, rh float32) {
		ui.Box(c).Absolute().Left(x).Top(y).Width(rw).Height(rh).Background(theme.Line).IgnoreInput(true).AccessHidden(true)
	}
	ui.Box(c.Key("kinds")).Width(g.cw).Height(h).Margin(0, 0, 56, 0).Role(ui.RoleNavigation).Label(locales.ExploreKinds()).Children(func() {
		rule(0, 0, g.cw, 1)
		rule(0, h-1, g.cw, 1)
		for i := 1; i < len(kinds); i++ {
			rule(float32(i)*w, h*0.2, 1, h*0.6)
		}
		ui.Row(c.Key("row")).Absolute().Left(0).Top(0).Children(func() {
			for _, k := range kinds {
				a.kindDoor(c, k, w, h, live)
			}
		})
	})
}

func lerpf(a, b, t float32) float32 { return a + (b-a)*t }

// kindDoor is one kind's door: its drawing standing on the row's line, its
// name under it, on the window itself. Pointed at, or in focus
// from the keys, one number lights it and the drawing's moving part acts
// (doorAct). Opened, it spreads into the ground of the kind's page
// (doorGround).
func (a *App) kindDoor(c *ui.Context, k string, w, h float32, live bool) {
	ex := a.ex
	door := ui.Box(c.Key(k)).Size(w, h).Radius(10)
	a.intoView(door, 0)
	kw := a.wall(k)
	art := doorArtBox(k, w, h)
	// Laid out unseen on the way back, the door is where it will show: the ground closing goes to it.
	if b := door.LayoutBounds(); (live || a.warming) && b.W > 0 {
		kw.doorAt, kw.art = b, art
	}
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
		on := float32(0)
		// The keyboard's door lights only while the keys are in use: the pointer lights its own.
		if b.Hovered() || focus && a.kbd {
			on = 1
		}
		// Resting on it, the kind's page is laid out unseen (stage), for its first frame not to stall.
		if live && b.Hovered() {
			if kw.held.IsZero() {
				kw.held = c.Now()
			}
		} else if live {
			kw.held = time.Time{}
		}
		light := b.AnimateTo("light", on, a.ease(motion.Lean))
		act, beat := a.doorAct(c, b, k, kw, on, live)
		amber := a.doorAmber(c, b, k, kw, on, light)
		if live {
			kw.lit, kw.look, kw.amber = light, act, amber
		}
		if b.Clicked() {
			a.openKind(c, k)
		}
		b.Children(func() {
			ui.Box(c.Key("pic")).Absolute().Fill().Radius(10).ClipX().ClipY().IgnoreInput(true).AccessHidden(true).Children(func() {
				ui.Box(c).Absolute().Left(art.X).Top(art.Y).Size(art.W, art.H).Children(func() {
					a.doorDrawing(c, k, art.W, art.H, doorLit{light, amber, act, beat})
				})
			})
			doorCaption(c, kindName(k), light).ZIndex(1)
		})
	})
}

// openKind goes into a kind's wall from its door, which spreads into its
// ground (doorGround); the door takes the focus back when the keys come back.
func (a *App) openKind(c *ui.Context, k string) {
	a.ex.opener = "kind:" + k
	at := a.page
	m := a.goTo(c, page{"kind", k}, motion.Deep, append(a.from[:len(a.from):len(a.from)], at))
	if m == nil {
		return
	}
	// Through its door the page opens at its top, with its first batch; from the keys or under less motion it is
	// there at once.
	kw := a.wall(k)
	kw.scroll.Y, kw.shown, kw.endY = 0, 0, 0
	kw.reach, kw.veil, kw.shade, kw.run = 1, 1, 0, 0
	if !m.kbd && !a.still(c) {
		m.door = k
		kw.reach, kw.veil, kw.shade, kw.run = 0, 0, 1, m.run
	}
}

// doorCaption is a kind's name on its door: one line, centred under the
// drawing, text-2 coming up to text as the door lights.
func doorCaption(c *ui.Context, s string, light float32) ui.Element {
	return ui.Text(c, s).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2.Mix(theme.Text, light)).
		SingleLine().Ellipsis("…").TextAlign(ui.Center).Absolute().Left(12).Right(12).Bottom(18)
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
