package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/theme"
)

// A kind's page (PAGES.md 类别页): the wall of the kind's works under the
// band, laid out as the kind is looked at, the galleries taking turns, and
// nothing written over it; its door opens into its ground.

// kindWall is what a kind's page keeps: its scroll, its works as the wall
// lays them out, and how many it shows so far.
type kindWall struct {
	scroll ui.ScrollState
	shown  int
	endY   float32
	order  []exWork // the works as the wall lays them out (takeTurns)
	of     []exWork // the kind's works order was spread from
	doorAt ui.Rect  // where the door was last shown, in the window (doorGround)
	art    ui.Rect  // where its drawing lay on it (doorArtBox)
	// How far the door was lit, its amber lit and its drawing acted, as it was last shown live: the ground
	// opening out of it starts as it was.
	lit, amber, look float32
	acting           doorActing
	// When the pointer came onto the door, and onto the way back from the page; zero when it is elsewhere (stage
	// lays the page coming out unseen).
	held, back time.Time
	heldY      float32 // where Explore was scrolled to as the pointer rested on the door
	// How far the door had opened by its last frame (doorGround): the ground grown, its picture gone into the
	// window's colour, the shade under its name; and the crossing it opened in, which the wall unfolds in. Gone
	// back before it has opened, the ground draws back from there and the wall goes on from where it was.
	reach, veil, shade float32
	run                uint64
}

// doorGround is the door spreading into the ground of its kind's page
// (open), or the ground drawing back into the door (closing over Explore
// come back): one box, along the travel spring, from the door to the whole
// content area. The door's drawing and its name stay where the door was,
// as the door was pressed, and go into the window's colour as it opens
// (DoorDim), so that what lies under it is covered rather than
// dissolving and nothing between the two pages shows; closing, they show
// again, at rest, as the box nears the door, which it lands on exactly.
func (a *App) doorGround(c *ui.Context, m *move, open bool) {
	kw := a.wall(m.door)
	d := kw.doorAt
	x := m.depth.Crossing()
	gone := c.Now().Sub(a.movedAt)
	if d.W <= 0 || m.kbd || a.still(c) || gone >= x.Travel {
		if open {
			kw.reach, kw.veil, kw.shade = 1, 1, 0
		}
		return
	}
	c.After(x.Travel - gone) // drawn back into the door, it goes as the door shows again
	dim := motion.DoorDim
	g := ui.Box(c.Key("door-ground")).Absolute().ZIndex(1).ClipX().ClipY().Background(theme.Window).IgnoreInput(true).AccessHidden(true)
	opt := ui.KeyframeOptions{Run: m.run}
	// Closing, it covers Explore whole while the page's works go (motion.DoorLeave), drawing back as they are nearly gone.
	frames := []ui.Keyframe{{Value: 0}, {At: x.Travel, Value: 1, Ease: motion.TravelEase}}
	veil := []ui.Keyframe{{Value: 0}, {At: dim.Dim, Value: 1, Ease: motion.Ease}}
	shade := []ui.Keyframe{{Value: 1}, {At: dim.Shade, Value: 0, Ease: motion.Ease}}
	if !open {
		frames = []ui.Keyframe{{Value: 0}, {At: dim.Hold, Value: 0}, {At: x.Travel, Value: 1, Ease: motion.TravelEase}}
		veil = []ui.Keyframe{{Value: kw.veil}, {At: dim.Show, Value: kw.veil}, {At: dim.Shown, Value: 0, Ease: motion.Ease}}
		shade = []ui.Keyframe{{Value: kw.shade}, {At: x.Travel - dim.Shade, Value: kw.shade}, {At: x.Travel, Value: 1, Ease: motion.Ease}}
	}
	t := g.Keyframes("t", frames, opt)
	if !open {
		t = kw.reach * (1 - t) // from as far as it had opened
	}
	r := a.area
	w, h := lerpf(d.W, r.W, t), lerpf(d.H, r.H, t)
	g.Left(lerpf(d.X-r.X, 0, t)).Top(lerpf(d.Y-r.Y, 0, t)).Width(w).Height(h).Radius(10 * (1 - t))
	v, sh := g.Keyframes("veil", veil, opt), g.Keyframes("shade", shade, opt)
	if open {
		kw.reach, kw.veil, kw.shade = t, v, sh
	}
	// Opening, as the door was when it was pressed; closing, as it rests, the pointer away on the way back.
	var l doorLit
	if open {
		l = doorLit{kw.lit, kw.amber, kw.look, kw.acting.beat}
	}
	g.Children(func() {
		if v < 1 {
			ui.Box(c).Absolute().Left(t*(d.X-r.X)).Top(t*(d.Y-r.Y)).Size(d.W, d.H).Children(func() {
				ui.Box(c).Absolute().Left(kw.art.X).Top(kw.art.Y).Size(kw.art.W, kw.art.H).Children(func() {
					a.doorDrawing(c, m.door, kw.art.W, kw.art.H, l)
				})
				if sh > 0.001 {
					doorCaption(c, kindName(m.door), l.light).Opacity(sh)
				}
			})
		}
		if v > 0.001 && v < 1 {
			ui.Box(c).Absolute().Fill().Background(theme.Window).Opacity(v)
		}
	})
}

// doorLeave is the page left as a door opens (Explore, under the door's
// ground) or closes back (the kind's page, over it): built on while it goes
// out of focus and fades over motion.DoorLeave, inert, rather than left to
// a copy, which would lay all its text out anew in the crossing's first
// frames.
func (a *App) doorLeave(e ui.Element, m *move) {
	z := 1
	if m.depth == motion.Back {
		z = 2
	}
	k := e.Keyframes("leave", []ui.Keyframe{{Value: 0}, {At: motion.DoorLeave, Value: 1, Ease: motion.Ease}}, ui.KeyframeOptions{Run: m.run})
	e.ZIndex(z).IgnoreInput(true).AccessHidden(true).Opacity(1 - k)
	if b := motion.Defocus * k; b > 0.01 {
		e.Blur(b)
	}
	if s := lerpf(1, m.depth.Crossing().To, k); s != 1 {
		e.TransformOrigin(0.5, 0.5).Scale(s, s)
	}
}

// wall is kind k's page's state.
func (a *App) wall(k string) *kindWall {
	ex := a.ex
	if ex.walls == nil {
		ex.walls = map[string]*kindWall{}
	}
	kw := ex.walls[k]
	if kw == nil {
		kw = &kindWall{}
		ex.walls[k] = kw
	}
	return kw
}

// wallLook is how a kind's wall lays its works out and looks at them.
type wallLook struct {
	cols   int
	aspect float32 // a tile's height over its width
	px, py float32 // where a tile holds its picture
	scroll bool    // lit, a picture taller than its tile scrolls on down
	lean   float32 // else lit, how close it comes
}

var wallLooks = map[string]wallLook{
	// Landing pages stand as pages, four across, from their first screen;
	// lit, a page scrolls on down.
	"landing": {cols: 4, aspect: 4.0 / 3, px: 0.5, py: 0, scroll: true, lean: 1.03},
	// Portfolios lie as screens, three across.
	"portfolio": {cols: 3, aspect: 10.0 / 16, px: 0.5, py: 0, lean: 1.03},
	// Motion as frames, three across; lit, the camera moves in.
	"motion": {cols: 3, aspect: 3.0 / 4, px: 0.5, py: 0.5, lean: 1.06},
	// Type as printed pieces, four across; lit, seen closer.
	"type": {cols: 4, aspect: 5.0 / 4, px: 0.5, py: 0.5, lean: 1.08},
	// Brands as their marks, four across.
	"brand": {cols: 4, aspect: 1, px: 0.5, py: 0.5, lean: 1.03},
}

// wallScroll is how far down a lit page scrolls, at most, in its tile's
// heights.
const wallScroll = 0.3

// kindPage is a kind's page: the way back, then the wall, the next batch
// coming as its end nears.
func (a *App) kindPage(c *ui.Context, k string, live bool) {
	ex := a.ex
	kw := a.wall(k)
	works := ex.kinds[k]
	if len(kw.of) != len(works) || len(works) > 0 && &kw.of[0] != &works[0] {
		kw.order, kw.of = takeTurns(works), works
	}
	l, ok := wallLooks[k]
	if !ok {
		l = wallLooks["portfolio"]
	}
	w := min(1200, a.area.W) - 64
	cols := l.cols
	batch := cols * 4
	if kw.shown == 0 {
		kw.shown = batch
	}
	n := min(kw.shown, len(kw.order))
	// Built for its exit, the page keeps the way back it had.
	back := a.backTo()
	if l := a.left; !live && l != nil && l.page == (page{"kind", k}) && len(l.from) > 0 {
		back = l.from[len(l.from)-1]
	}
	sc := ui.Scroll(c.Key("scroll")).TrackScroll(&kw.scroll).Grow(1).FillWidth().Scrollbars(false)
	if live {
		a.scrolling = &kw.scroll
	}
	tw := (w - float32(cols-1)*16) / float32(cols)
	th := tw * l.aspect
	// Come in through the door, the wall unfolds under the band from the door's side: a row later for each row
	// down, half a row for each column away from the door. Gone back through it before the wall is in, the wall
	// goes on from where it was as it leaves; come in any other way, it is there.
	run := uint64(0)
	switch m, l := a.came, a.left; {
	case live && m != nil && m.to == (page{"kind", k}) && m.door == k && m.depth == motion.Deep:
		run = m.run
	case live:
		kw.run = 0
	case l != nil && l.page == (page{"kind", k}) && l.move.door == k:
		run = kw.run
	}
	doorCol := float32(0)
	if d := kw.doorAt; d.W > 0 {
		left := a.area.X + (a.area.W-w)/2
		doorCol = min(float32(cols-1), max(0, (d.X+d.W/2-left)/(tw+16)-0.5))
	}
	sc.Children(func() {
		frame(c).Padding(0, 32, 140, 32).Children(func() {
			a.band(c, func() {
				way := ui.Row(c.Key("way-back"))
				if on := way.Hovered(); live && on && kw.back.IsZero() {
					kw.back = c.Now()
				} else if live && !on {
					kw.back = time.Time{}
				}
				// Come in through the door, the way back comes in with the wall, not before it over the door's ground.
				if run != 0 {
					if in := unfolding(way, run, motion.Unfold.After); in < 1 {
						way.Opacity(in)
					}
				}
				way.Children(func() { a.backLink(c, backLabel(a, back)) })
				// The page's name, for a screen reader only: on the screen the works say what kind they are.
				ui.Box(c.Key("name")).Size(0, 0).Role(ui.RoleHeading).Level(1).Label(kindName(k))
			}, func() {})
			ui.Column(c.Key("wall")).Gap(16).Role(ui.RoleList).Label(kindName(k)).Children(func() { a.wallRows(c, k, kw, l, n, cols, tw, th, run, doorCol) })
			if n < len(kw.order) {
				end := ui.Box(c.Key("end")).Height(1).FillWidth().AccessHidden(true)
				if live && !a.warming {
					if y := end.LayoutBounds().Y; end.InViewMargin(0, 0, 600, 0) && y != kw.endY {
						kw.endY = y
						kw.shown += batch
						c.Invalidate()
					}
				}
			}
		})
	})
}

// wallRows is the first n of the wall's works, cols across, each tw×th;
// come in through the door in crossing run, they unfold from its side,
// doorCol. A work whose picture cannot be had is left off the wall, as a site
// whose first screen cannot be taken is left out of Explore's stream; unless
// none can be (offline, none kept), when the tiles wait for them.
func (a *App) wallRows(c *ui.Context, k string, kw *kindWall, l wallLook, n, cols int, tw, th float32, run uint64, doorCol float32) {
	works, seen := make([]exWork, 0, n), false
	for _, w := range kw.order[:n] {
		p := a.workPic(w)
		if p != nil && p.done && p.bmp == nil {
			continue
		}
		works, seen = append(works, w), seen || p != nil && p.bmp != nil
	}
	if !seen {
		works = kw.order[:n]
	}
	n = len(works)
	for row := 0; row*cols < n; row++ {
		ui.Row(c.Key(row)).Gap(16).Children(func() {
			for col := range cols {
				if i := row*cols + col; i < n {
					cell := ui.Box(c.Key(works[i].key())).Role(ui.RoleListItem)
					in := float32(1)
					if run != 0 && row < 4 {
						away := float32(row) + 0.5*max(float32(col)-doorCol, doorCol-float32(col))
						in = unfolding(cell, run, motion.Unfold.After+time.Duration(away*float32(motion.Unfold.Row)))
					}
					a.cellIn(cell, in)
					cell.Children(func() { a.kindTile(c, k, works[i], l, tw, th) })
				}
			}
		})
	}
}

// unfolding is how far e has come in, come in through the door: from 0
// until delay into crossing run, to 1 over motion.Unfold.In.
func unfolding(e ui.Element, run uint64, delay time.Duration) float32 {
	u := motion.Unfold
	return e.Keyframes("unfold", []ui.Keyframe{{Value: 0}, {At: delay, Value: 0}, {At: delay + u.In, Value: 1, Ease: motion.Ease}},
		ui.KeyframeOptions{Run: run, NonMotion: true})
}

// cellIn is a work on the wall coming into focus as it scrolls in
// (intoView) and as it comes in through the door, in of the way, rising from
// Unfold.Rise below: one opacity and one blur for both, on the work itself,
// so that no blur is drawn inside another's layer (a blur in a layer that
// stops showing jumps to the window's top for a few frames).
func (a *App) cellIn(e ui.Element, in float32) {
	seen := a.seenOf(e, 0)
	if seen >= 1 && in >= 1 {
		return
	}
	opacity, blur := seenLook(seen)
	e.Opacity(opacity * in)
	if b := max(blur, motion.Defocus*(1-in)); b > 0.05 {
		e.Blur(b)
	}
	if in < 1 {
		e.Translate(0, motion.Unfold.Rise*(1-in))
	}
}

// kindTile is a work on its kind's wall: its picture fills it, resting as
// bright as the picture allows and coming up under the pointer, held and
// moving as its kind is looked at (wallLook); the gallery's face and the
// title rise at its foot as it lights. Opened, its ground spreads
// into the work's page, and gathers back into it on the way back.
func (a *App) kindTile(c *ui.Context, k string, w exWork, l wallLook, tw, th float32) {
	pic := a.workPic(w)
	b := ui.ButtonBase(c.Key(w.key())).Label(locales.ExploreWorkCard(w.title, w.entry.Name)).Size(tw, th).Radius(10).ClipX().ClipY().
		Background(theme.Surface).FocusRing(false)
	a.reach(c, b, w.entry.ID, w.url)
	if a.lay != nil && a.layRow != "" {
		a.layInto(page{"kind", k}, a.postID(w.url), b.LayoutBounds())
	}
	focus := b.FocusVisible()
	if focus {
		b.FocusRing(true).FocusRingOffset(-2)
	}
	if !a.still(c) {
		s := float32(1)
		if b.Pressed() && !a.kbd {
			s = 0.99
		}
		p := b.AnimateTo("press", s, ui.Animation{Duration: motion.Press, Ease: motion.Ease})
		b.TransformOrigin(0.5, 0.5).Scale(p, p)
	}
	on := float32(0)
	if b.Hovered() || focus {
		on = 1
	}
	light := b.AnimateTo("light", on, a.ease(motion.Lean))
	look := light
	if a.still(c) {
		look = 0
	}
	if b.Clicked() {
		a.openPost(c, w.entry, w.post(), &trialFrom{button: b.LayoutBounds()})
	}
	icon := a.ex.pics["icon:"+w.entry.ID]
	b.Children(func() {
		if pic != nil && pic.bmp != nil {
			py, z := l.py, lerpf(1, l.lean, look)
			if d := overhang(pic, tw, th); l.scroll && d > 1 {
				py, z = lerpf(l.py, min(1, wallScroll*th/d), look), 1
			}
			img := ui.Image(c, pic.bmp).Fit(ui.Cover).ObjectPosition(l.px, py).Absolute().Opacity(a.arrived(c, pic, motion.SwapIn))
			grown(img, tw, th, z)
			// Half as dim as a picture alone rests (toneOf): a wall of white pages stays a wall of pages, and the one
			// under the pointer comes up whole.
			if v := 1 - lerpf(lerpf(pic.rest, 1, 0.5), 1, light); pic.rest > 0 && v > 0.001 {
				ui.Box(c).Absolute().Fill().Background(theme.Darken).Opacity(v).IgnoreInput(true)
			}
		}
		if pic == nil || !pic.done {
			a.breathing(c, theme.Hover)
		}
		// At rest the wall is the works alone: a kind with one gallery would repeat its face on every tile. Lit, the
		// gallery's face and the title come up together on the words' band, baked for the picture at rest.
		capH := float32(56)
		ui.Box(c.Key("cap")).Absolute().Left(0).Right(0).Bottom(0).Height(capH).IgnoreInput(true).ZIndex(1).Opacity(light).Children(func() {
			bandOver(c, bandKey{w: tw, h: capH, rise: 32, y: -(th - capH), ph: th, pw: tw, px: l.px, py: l.py}, pic, 10)
			left := float32(12)
			if icon != nil && icon.bmp != nil {
				left = 40
				ui.Box(c).Absolute().Left(10).Bottom(10).Size(20, 20).Radius(10).Background(theme.Text).Translate(0, 4*(1-look)).
					Children(func() { ui.Image(c, icon.bmp).Fit(ui.Cover).Fill().Radius(10) })
			}
			ui.Text(c, w.title).FontSize(theme.Body).LineHeight(20.0/13).FontWeight(600).TextColor(theme.Text).SingleLine().
				Absolute().Left(left).Right(12).Bottom(10).Translate(0, 4*(1-look))
		})
		ui.Box(c).Absolute().Fill().Radius(10).InsetShadow(0, 0, 0, 1, theme.LineSoft).IgnoreInput(true).ZIndex(3)
	})
}

// overhang is how much taller than a tile tw×th a picture covering it is.
func overhang(p *exPic, tw, th float32) float32 {
	iw, ih := p.bmp.Size()
	if iw <= 0 || ih <= 0 {
		return 0
	}
	s := max(tw/float32(iw), th/float32(ih))
	return float32(ih)*s - th
}

// takeTurns is a kind's works as its wall lays them out: the newest first,
// then each gallery's over the wall by its share of them, newest first
// within it, none three times running while another has works left.
func takeTurns(works []exWork) []exWork {
	if len(works) < 3 {
		return works
	}
	var gals [][]exWork
	at := map[string]int{}
	for _, w := range works {
		i, ok := at[w.entry.ID]
		if !ok {
			i = len(gals)
			at[w.entry.ID] = i
			gals = append(gals, nil)
		}
		gals[i] = append(gals[i], w)
	}
	next := make([]int, len(gals))
	out := make([]exWork, 0, len(works))
	take := func(g int) {
		out = append(out, gals[g][next[g]])
		next[g]++
	}
	left := func(g int) bool { return next[g] < len(gals[g]) }
	// ran reports whether gallery g has the last two places.
	ran := func(g int) bool {
		n, id := len(out), gals[g][0].entry.ID
		return n >= 2 && out[n-1].entry.ID == id && out[n-2].entry.ID == id
	}
	take(at[works[0].entry.ID])
	for len(out) < len(works) {
		others := 0
		for g := range gals {
			if left(g) {
				others++
			}
		}
		best, key := -1, 0.0
		for g := range gals {
			if !left(g) || ran(g) && others > 1 {
				continue
			}
			kg := (float64(next[g]) + 0.5) / float64(len(gals[g]))
			if best < 0 || kg < key || kg == key && exTime(gals[g][next[g]].publishedAt) > exTime(gals[best][next[best]].publishedAt) {
				best, key = g, kg
			}
		}
		take(best)
	}
	return out
}
