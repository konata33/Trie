package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/theme"
)

// A kind's page (PAGES.md 类别页): its door opened, the work the door stands
// on as wide as the wall, the kind's name over its foot; under it the wall
// of the kind's works, laid out as the kind is looked at, the galleries
// taking turns.

// kindWall is what a kind's page keeps: its scroll, its works as the wall
// lays them out, and how many it shows so far.
type kindWall struct {
	scroll ui.ScrollState
	shown  int
	endY   float32
	order  []exWork // the works as the wall lays them out (takeTurns)
	of     []exWork // the kind's works order was spread from
	run    uint64   // the crossing the door's picture flies in (kindShare)
	// The door and the head as last built, for the picture flying between them (between).
	door, head hold
	doorAt     ui.Rect   // where the door was last shown, in the window (doorGround)
	back       time.Time // when the pointer came onto the way back, zero when it is elsewhere (stage lays Explore out unseen)
}

// doorGround is the ground of a kind's page as its door opens into it
// (open) or closes back: the window's own colour, growing out of the door
// over the page left, or drawing back into it over Explore come back, along
// the travel spring the door's picture flies on. What lies under it is
// covered rather than dissolving, and nothing between the two pages shows.
func (a *App) doorGround(c *ui.Context, m *move, open bool) {
	d := a.wall(m.door).doorAt
	x := m.depth.Crossing()
	gone := c.Now().Sub(a.movedAt)
	if d.W <= 0 || m.kbd || a.still(c) || gone >= x.Travel {
		return
	}
	c.After(x.Travel - gone) // drawn back into the door, it goes as the door shows again
	g := ui.Box(c.Key("door-ground")).Absolute().ZIndex(1).Background(theme.Window).IgnoreInput(true).AccessHidden(true)
	// Closing, it covers Explore whole while the page's works go (motion.DoorLeave), then draws back.
	frames := []ui.Keyframe{{Value: 0}, {At: x.Travel, Value: 1, Ease: motion.TravelEase}}
	if !open {
		frames = []ui.Keyframe{{Value: 0}, {At: motion.DoorLeave, Value: 0}, {At: x.Travel, Value: 1, Ease: motion.TravelEase}}
	}
	t := g.Keyframes("t", frames, ui.KeyframeOptions{Run: m.run})
	if !open {
		t = 1 - t
	}
	r := a.area
	g.Left(lerpf(d.X-r.X, 0, t)).Top(lerpf(d.Y-r.Y, 0, t)).Width(lerpf(d.W, r.W, t)).Height(lerpf(d.H, r.H, t)).Radius(10 * (1 - t))
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

// hold is a box and where a picture covering it is held (ObjectPosition).
type hold struct{ w, h, x, y float32 }

// between is where a frame flying from box a to box b, t of the way, holds
// p: what it shows at its middle goes from what a showed to what b shows,
// rather than its hold sliding across boxes of other shapes.
func between(p *exPic, a, b hold, t float32) (x, y float32) {
	iw, ih := p.bmp.Size()
	fw, fh := float32(iw), float32(ih)
	if a.w <= 0 || b.w <= 0 || fw <= 0 || fh <= 0 {
		return lerpf(a.x, b.x, t), lerpf(a.y, b.y, t)
	}
	ax, ay := seen(fw, fh, a)
	bx, by := seen(fw, fh, b)
	return holdFor(fw, fh, lerpf(a.w, b.w, t), lerpf(a.h, b.h, t), lerpf(ax, bx, t), lerpf(ay, by, t))
}

// seen is the middle of what box o shows of a picture iw×ih covering it,
// in shares of the picture.
func seen(iw, ih float32, o hold) (cx, cy float32) {
	s := max(o.w/iw, o.h/ih)
	dw, dh := iw*s, ih*s
	return ((dw-o.w)*o.x + o.w/2) / dw, ((dh-o.h)*o.y + o.h/2) / dh
}

// holdFor is the hold of a box bw×bh covered by a picture iw×ih that shows
// cx, cy at its middle, as near as it can.
func holdFor(iw, ih, bw, bh, cx, cy float32) (x, y float32) {
	s := max(bw/iw, bh/ih)
	dw, dh := iw*s, ih*s
	x, y = 0.5, 0.5
	if dw > bw {
		x = min(1, max(0, (cx*dw-bw/2)/(dw-bw)))
	}
	if dh > bh {
		y = min(1, max(0, (cy*dh-bh/2)/(dh-bh)))
	}
	return x, y
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

// kindMatch is a kind's picture: the same on its door and at the head of
// its page.
type kindMatch struct{ kind string }

// kindShare gives e, the picture of kind k's door or of its page's head,
// shown on page p, the picture's identity. In a crossing between Explore
// and the kind's page the one coming flies out of the one going: the frame
// grows out of the door into the head, or closes back into it, the picture
// laid out anew at each size on the way; the one going stays with its page
// as it goes (doorLeave). Otherwise it says where it is, for the other to
// come from. It gives the crossing when e is the one coming, for its
// picture to go from the other's hold to its own (opening).
func (a *App) kindShare(c *ui.Context, e ui.Element, k string, p page, live bool) *move {
	if a.warming {
		return nil // laid out unseen, it is not where the picture is: the head flies to it as it shows
	}
	kw, m := a.wall(k), a.came
	door, head := page{kind: "explore"}, page{"kind", k}
	// Noted in the crossing's first frame, while the page left is still built, and kept for the whole flight.
	if m != nil && a.left != nil && a.left.move == m && (a.left.page == door && m.to == head || a.left.page == head && m.to == door) {
		kw.run = m.run
	}
	match := kindMatch{k}
	if m != nil && !m.kbd && m.run == kw.run && c.Now().Sub(a.movedAt) < crossingTime &&
		(live && m.to == p || !live && a.left != nil && a.left.page == p) {
		if !live && c.Now().After(a.movedAt) {
			return nil // the other took off from it in the crossing's first frame
		}
		e.Transition(a.tr(c, m.depth.Crossing().Shared(match)))
		if live && !a.still(c) {
			return m
		}
		return nil
	}
	e.Transition(ui.ElementTransition{Match: match})
	return nil
}

// opening is how far the frame flying in crossing m has come, along the
// travel spring that carries it.
func opening(e ui.Element, m *move) float32 {
	return e.Keyframes("open", []ui.Keyframe{{Value: 0}, {At: m.depth.Crossing().Travel, Value: 1, Ease: motion.TravelEase}}, ui.KeyframeOptions{Run: m.run})
}

// kindPage is a kind's page: the way back, the head, then the wall, the
// next batch coming as its end nears.
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
	batch := l.cols * 4
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
	w := min(1200, a.area.W) - 64
	tw := (w - float32(l.cols-1)*16) / float32(l.cols)
	th := tw * l.aspect
	// Come in through the door, the wall unfolds under the head, a row after the one above.
	m := a.came
	unfold := live && m != nil && m.to == (page{"kind", k}) && m.depth == motion.Deep && !m.kbd && !a.still(c)
	sc.Children(func() {
		frame(c).Padding(0, 32, 140, 32).Children(func() {
			a.band(c, func() {
				way := ui.Row(c.Key("way-back"))
				if on := way.Hovered(); live && on && kw.back.IsZero() {
					kw.back = c.Now()
				} else if live && !on {
					kw.back = time.Time{}
				}
				way.Children(func() { a.backLink(c, backLabel(a, back)) })
			}, func() {})
			a.kindHead(c, k, w, works, live, unfold)
			ui.Column(c.Key("wall")).Gap(16).Role(ui.RoleList).Label(kindName(k)).Children(func() {
				for row := 0; row*l.cols < n; row++ {
					r := ui.Row(c.Key(row)).Gap(16)
					if unfold && row < 4 {
						unfoldIn(c, r, m.run, motion.Unfold.After+time.Duration(row)*motion.Unfold.Row)
					}
					r.Children(func() {
						for col := range l.cols {
							if i := row*l.cols + col; i < n {
								cell := ui.Box(c.Key(kw.order[i].key())).Role(ui.RoleListItem)
								a.intoView(cell, 0)
								cell.Children(func() { a.kindTile(c, k, kw.order[i], l, tw, th) })
							}
						}
					})
				}
			})
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

// unfoldIn brings e into focus from motion.Unfold.Rise below, after delay into
// crossing run.
func unfoldIn(c *ui.Context, e ui.Element, run uint64, delay time.Duration) {
	u := motion.Unfold
	k := e.Keyframes("unfold", []ui.Keyframe{{Value: 0}, {At: delay, Value: 0}, {At: delay + u.In, Value: 1, Ease: motion.Ease}},
		ui.KeyframeOptions{Run: run, NonMotion: true})
	if k >= 1 {
		return
	}
	e.Opacity(k).Translate(0, u.Rise*(1-k))
	if b := motion.Defocus * (1 - k); b > 0.05 {
		e.Blur(b)
	}
}

// kindHead is the head of a kind's page: its door opened, the work the door
// stands on as wide as the wall and lit, the kind's name over its foot on
// the words' band, the galleries its works come from at the other end.
func (a *App) kindHead(c *ui.Context, k string, w float32, works []exWork, live, unfold bool) {
	cov, pic := a.doorPic(k)
	_, vh := c.Size()
	h := clampf(0.32*vh, 240, 320)
	p := page{"kind", k}
	ui.Box(c.Key("head")).Size(w, h).Margin(0, 0, 24, 0).Children(func() {
		frm := ui.Box(c.Key("pic")).Absolute().Fill().Radius(10).ClipX().ClipY().Background(theme.Surface).IgnoreInput(true).AccessHidden(true)
		fly := a.kindShare(c, frm, k, p, live)
		frm.Children(func() {
			if pic != nil && pic.bmp != nil {
				// Opening out of the door, held at first as the lit door held it, coming to the head's own hold.
				x, y, z := cov.headX, cov.headY, float32(1)
				kw := a.wall(k)
				kw.head = hold{w, h, x, y}
				if fly != nil {
					t := opening(frm, fly)
					x, y = between(pic, hold{kw.door.w, kw.door.h, cov.litX, cov.litY}, kw.head, t)
					z = lerpf(cov.litZoom, z, t)
				}
				ui.Image(c, pic.bmp).Fit(ui.Cover).ObjectPosition(x, y).Absolute().
					LeftPercent(-100 * (z - 1) * cov.fx).TopPercent(-100 * (z - 1) * cov.fy).WidthPercent(100 * z).HeightPercent(100 * z).Opacity(a.arrived(c, pic, motion.SwapIn))
				if v := 1 - pic.lit; pic.lit > 0 && v > 0.001 {
					ui.Box(c).Absolute().Fill().Background(theme.Darken).Opacity(v)
				}
			}
			// The words' band goes with the picture as it flies (kindDoor).
			bandAtFoot(c, bandKey{w: w, h: 152, top: h - 152, rise: 96, ph: h, pw: w, px: cov.headX, py: cov.headY}, pic, 10)
		})
		if pic == nil || !pic.done {
			a.breathing(c, theme.Hover)
		}
		ui.Box(c.Key("cap")).Absolute().Fill().IgnoreInput(true).ZIndex(1).Children(func() {
			name := kindTitle(c, kindName(k)).Role(ui.RoleHeading).Level(1).Absolute().Left(32).Bottom(28)
			a.end(name, p, "kind:"+k)
		})
		ui.Row(c.Key("from")).Absolute().Right(28).Bottom(36).Gap(8).ZIndex(2).Children(func() {
			for i, e := range kindSources(works) {
				f := a.exFace(c, e, 24, false, 11)
				a.tip(c, f, e.Name, false)
				if unfold {
					unfoldIn(c, f, a.came.run, motion.Unfold.After+time.Duration(i+1)*motion.Unfold.Row)
				}
			}
		})
		// The edge drawn over the picture, as on the door.
		ui.Box(c).Absolute().Fill().Radius(10).InsetShadow(0, 0, 0, 1, theme.LineSoft).IgnoreInput(true).ZIndex(3)
	})
}

// kindTile is a work on its kind's wall: its picture fills it, resting as
// bright as the picture allows and coming up under the pointer, held and
// moving as its kind is looked at (wallLook); the gallery's face at its
// foot, the title rising beside it as it lights. Opened, its ground spreads
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
		// The title on the words' band, both coming up with the light; the band baked for the picture at rest.
		capH := float32(56)
		ui.Box(c.Key("cap")).Absolute().Left(0).Right(0).Bottom(0).Height(capH).IgnoreInput(true).ZIndex(1).Opacity(light).Children(func() {
			bandOver(c, bandKey{w: tw, h: capH, rise: 32, y: -(th - capH), ph: th, pw: tw, px: l.px, py: l.py}, pic, 10)
			ui.Text(c, w.title).FontSize(theme.Body).LineHeight(20.0/13).FontWeight(600).TextColor(theme.Text).SingleLine().
				Absolute().Left(40).Right(12).Bottom(10).Translate(0, 4*(1-look))
		})
		if icon != nil && icon.bmp != nil {
			ui.Box(c).Absolute().Left(10).Bottom(10).Size(20, 20).Radius(10).Background(theme.Text).ZIndex(2).IgnoreInput(true).
				Shadow(0, 0, 0, 2, theme.ScrimLight).Children(func() { ui.Image(c, icon.bmp).Fit(ui.Cover).Fill().Radius(10) })
		}
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
