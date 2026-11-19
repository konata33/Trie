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
// room's front page, a door for each, standing on a work chosen for it;
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
// kind with no work yet has none. live is false for Explore built for its
// exit.
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
	w := (g.cw - (n-1)*16) / n
	ui.Row(c.Key("kinds")).Gap(16).Margin(0, 0, 56, 0).Role(ui.RoleNavigation).Label(locales.ExploreKinds()).Children(func() {
		for _, k := range kinds {
			a.kindDoor(c, k, w, w*5/4, live)
		}
	})
}

// doorCover is what a kind's door stands on: a work chosen for the kind,
// the same on every visit, held as that kind is looked at; lit, it moves
// on as one goes on looking.
type doorCover struct {
	src string // the work's picture
	// At rest the picture covers a box zoom times the door, grown about
	// fx, fy of it, and is held there at px, py (ObjectPosition).
	px, py, zoom, fx, fy float32
	// Lit, it is held at litX, litY and as close as litZoom.
	litX, litY, litZoom float32
	// At the head of the kind's page, the door opened, it is held at
	// headX, headY.
	headX, headY float32
}

// doorCovers are the works the doors stand on: dark ones, so that the five
// sit together under the front page and none glares.
var doorCovers = map[string]doorCover{
	// A landing page from its first screen, as it opens; lit, it scrolls
	// on down. Unicorn Studio, from Land-book.
	"landing": {src: "https://cdn.land-book.com/image/0x0:2880x4320/600x0/filters:format(jpeg)/website/100879/08621d6d6013a7df-screenshot-upload?signature=aa9cf3c7d5cdefd041bbb75ef52292fa68cd7e5a7f576165d492469eb2d00edb",
		px: 0.5, py: 0, zoom: 1, fx: 0.5, fy: 0.5, litX: 0.5, litY: 1, litZoom: 1, headX: 0.5, headY: 0.36},
	// A portfolio open at one work, the next ones beside it; lit, it leafs
	// on to the next. Carter Ogunsola, from Minimal Gallery.
	"portfolio": {src: "https://minimal.gallery/wp-content/uploads/2026/10/carterogunsola.com_.jpg",
		px: 0.5, py: 0.5, zoom: 1, fx: 0.5, fy: 0.5, litX: 0.62, litY: 0.5, litZoom: 1, headX: 0.5, headY: 0.5},
	// A frame of a piece in motion; lit, the camera moves in. Orchestrated,
	// from The FWA.
	"motion": {src: "https://thefwa.com/img/submissions/26538a45-6a0d-462a-a363-8ec91dc1f698/1-a143568e-af6a-494c-a7fb-7e0db8a5bf0f.webp?w=1200",
		px: 0.56, py: 0.5, zoom: 1, fx: 0.5, fy: 0.5, litX: 0.56, litY: 0.5, litZoom: 1.06, headX: 0.5, headY: 0.42},
	// One letter in many faces, seen close; lit, closer. The Bitcoin
	// Typefaces poster, from Fonts In Use.
	"type": {src: "https://assets.fontsinuse.com/static/use-media-items/308/307552/upto-700xauto/6ad61590/bitcoin-typefaces-2000x2824.png",
		px: 0.5, py: 0, zoom: 1.15, fx: 0.5, fy: 0.25, litX: 0.5, litY: 0, litZoom: 1.25, headX: 0.5, headY: 0.3},
	// A mark where it lives, on a label; lit, it leans in. Blæst, from
	// Abduzeedo.
	"brand": {src: "https://cms.abduzeedo.com/sites/default/files/originals/hero_blaest-www-kurppahosk-com.jpg",
		px: 0.5, py: 0.5, zoom: 1, fx: 0.5, fy: 0.5, litX: 0.5, litY: 0.5, litZoom: 1.03, headX: 0.5, headY: 0.12},
}

// doorNewest is how a door holds the kind's newest work when its own
// cannot be had: as the stream holds a picture.
var doorNewest = doorCover{px: 0.5, py: 0, zoom: 1, fx: 0.5, fy: 0.5, litX: 0.5, litY: 0, litZoom: 1.03, headX: 0.5, headY: 0.2}

// doorPic is the picture a kind's door stands on, and how it holds it: its
// own work, or the kind's newest where that cannot be had (offline on a
// first start, or gone from the web).
func (a *App) doorPic(k string) (doorCover, *exPic) {
	if cov, ok := doorCovers[k]; ok && a.opt.Explore != nil {
		svc, src := a.opt.Explore, cov.src
		p := a.picture("door:"+k, true, false, func() string { return svc.Picture(bg, src) })
		if p == nil || !p.done || p.bmp != nil {
			return cov, p
		}
	}
	if works := a.ex.kinds[k]; len(works) > 0 {
		return doorNewest, a.workPic(works[0])
	}
	return doorNewest, nil
}

func lerpf(a, b, t float32) float32 { return a + (b-a)*t }

// kindDoor is one kind's door: its work fills it, resting as bright as the
// picture allows (toneOf, as the home page's first screen does), its name
// at the foot on the words' band as the stream's names below sit on
// theirs. Pointed at or in focus, one number lights it, the name coming up
// from text-2, and moves the picture the way its kind is looked at.
// Opened, the picture and the name go on to the head of the kind's page
// (kindShare, openKind).
func (a *App) kindDoor(c *ui.Context, k string, w, h float32, live bool) {
	ex := a.ex
	cov, pic := a.doorPic(k)
	door := ui.Box(c.Key(k)).Size(w, h).Radius(10)
	a.intoView(door, 0)
	if b := door.LayoutBounds(); live && b.W > 0 {
		a.wall(k).doorAt = b
	}
	door.Children(func() {
		b := ui.ButtonBase(c.Key("open")).Label(kindName(k)).Absolute().Fill().Radius(10).ClipX().ClipY().FocusRing(false).Background(theme.Surface)
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
		if b.Hovered() || focus {
			on = 1
		}
		light := b.AnimateTo("light", on, a.ease(motion.Lean))
		look := light // under less motion the door lights and stays still
		if a.still(c) {
			look = 0
		}
		if b.Clicked() {
			a.openKind(c, k)
		}
		b.Children(func() {
			// The picture in a frame of its own, which opens into the head of the kind's page, the words' band going
			// with it: flying, it is drawn over the page, and its own words would show through where the name lands.
			frm := ui.Box(c.Key("pic")).Absolute().Fill().Radius(10).ClipX().ClipY().IgnoreInput(true).AccessHidden(true)
			fly := a.kindShare(c, frm, k, page{kind: "explore"}, live)
			frm.Children(func() {
				if pic != nil && pic.bmp != nil {
					z, x, y := lerpf(cov.zoom, cov.litZoom, look), lerpf(cov.px, cov.litX, look), lerpf(cov.py, cov.litY, look)
					// Closing back from the head, held at first as the head held it, coming to the door's own hold.
					kw := a.wall(k)
					kw.door = hold{w, h, x, y}
					if fly != nil {
						t := opening(frm, fly)
						x, y = between(pic, kw.head, kw.door, t)
						z = lerpf(1, z, t)
					}
					// In shares of the frame, so that the picture fills it at every size it flies through.
					ui.Image(c, pic.bmp).Fit(ui.Cover).ObjectPosition(x, y).Absolute().
						LeftPercent(-100 * (z - 1) * cov.fx).TopPercent(-100 * (z - 1) * cov.fy).WidthPercent(100 * z).HeightPercent(100 * z).Opacity(a.arrived(c, pic, motion.SwapIn))
					if v := 1 - lerpf(pic.rest, pic.lit, light); pic.rest > 0 && v > 0.001 {
						ui.Box(c).Absolute().Fill().Background(theme.Darken).Opacity(v)
					}
				}
				// The band is baked for the picture at rest; lit, the picture moves under it, which the blur hides.
				pw, ph := w*cov.zoom, h*cov.zoom
				bandAtFoot(c, bandKey{w: w, h: 130, top: h - 130, rise: 96, y: -(ph - h) * cov.fy, ph: ph, x: -(pw - w) * cov.fx, pw: pw, px: cov.px, py: cov.py}, pic, 10)
			})
			if pic == nil || !pic.done {
				a.breathing(c, theme.Hover)
			}
			ui.Box(c.Key("cap")).Absolute().Fill().IgnoreInput(true).ZIndex(1).Children(func() {
				name := ui.Text(c, kindName(k)).FontSize(theme.Item).LineHeight(20.0 / 15).FontWeight(600).TextColor(theme.Text2.Mix(theme.Text, light)).
					SingleLine().Absolute().Left(16).Right(16).Bottom(14)
				a.end(name, page{kind: "explore"}, "kind:"+k)
			})
			// The edge drawn over the picture, so that a dark work keeps its door's shape on the dark page.
			ui.Box(c).Absolute().Fill().Radius(10).InsetShadow(0, 0, 0, 1, theme.LineSoft).IgnoreInput(true).ZIndex(2)
		})
	})
}

// openKind goes into a kind's wall from its door: the door's picture opens
// into the head of the page (kindShare) and its name goes up to the head's,
// growing; the door takes the focus back when the keys come back.
func (a *App) openKind(c *ui.Context, k string) {
	a.ex.opener = "kind:" + k
	at := a.page
	m := a.goTo(c, page{"kind", k}, motion.Deep, append(a.from[:len(a.from):len(a.from)], at))
	if m != nil && !m.kbd && !a.still(c) {
		m.door = k
		a.fly("kind:"+k, kindName(k), at, m.to, doorName, kindTitle, float32(theme.Headline)/theme.Item, motion.Deep)
	}
}

// doorName is a kind's name on its door, lit; doorNameAtRest, at rest.
func doorName(c *ui.Context, s string) ui.Element { return doorWords(c, s).TextColor(theme.Text) }

func doorNameAtRest(c *ui.Context, s string) ui.Element {
	return doorWords(c, s).TextColor(theme.Text2)
}

func doorWords(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(theme.Item).LineHeight(20.0 / 15).FontWeight(600).SingleLine()
}

// kindTitle is a kind's name at the head of its page.
func kindTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(theme.Headline).LineHeight(40.0 / 32).FontWeight(650).LetterSpacing(-0.015 * theme.Headline).TextColor(theme.Text).SingleLine()
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
