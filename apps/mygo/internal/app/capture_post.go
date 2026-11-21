package app

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/theme"
)

// 收进收藏 (DESIGN.md Capture 收进收藏): the island goes home into 收藏's tab, and
// the tab tells from then on of what it took in. The island travels along a
// low arc, drawing in toward its own 收藏 icon, which comes onto the tab's; the
// tab's orbit then fills amber, clockwise from its satellite, as far as the
// page being taken has come, the figures under the tab, and closes once the
// page is taken, saying 已拓下. Only a page that could not be taken asks for
// something (重试): it grows out of the tab as a note.

// captureHoming is the island on its way home: what it held, where it and its
// thumbnails were in the window, and its status's place in it (markAt), its
// 收藏 icon after that.
type captureHoming struct {
	items  []*packItem
	from   ui.Rect
	slots  []ui.Rect
	mark   float32
	start  time.Time
	length time.Duration
	landed bool // near enough that the tab has taken over
}

// The way home, on Flight: the island fades from homeFade to homeGone of the
// way, and the tab takes over from homeLand.
const (
	homeFade = 0.55
	homeGone = 0.95
	homeLand = 0.85
)

// putAway sends the island home to 收藏 holding items, from where it is; from
// the keys or under less motion it is there at once.
func (a *App) putAway(c *ui.Context, items []*packItem, now time.Time) {
	h := &a.capture
	if a.kbd || a.still(c) || h.islandRect.W <= 0 {
		a.homed(c, items, now)
		return
	}
	hm := &captureHoming{items: items, from: h.islandRect, mark: h.markAt, start: now}
	for _, it := range items {
		hm.slots = append(hm.slots, h.slots[it.ID])
	}
	h.homing = hm
}

// homed is the island come home: the tab lights and takes over, and VoiceOver
// hears what came in.
func (a *App) homed(c *ui.Context, items []*packItem, now time.Time) {
	a.lights[favLight] = now
	c.AnimationFrame() // the sidebar, built before, lights in the next
	said := locales.CaptureMovedOne()
	if n := len(items); n > 1 {
		said = locales.CaptureMovedMany(n)
	}
	for _, it := range items {
		if it.state == packTaking {
			said += " · " + locales.CaptureTakingAt(it.pct)
			break
		}
	}
	a.said = append(a.said, said)
}

// captureHoming draws the island on its way home: one whole, along a low arc,
// on Flight, as long as the way is (FlightFor), drawing in toward its 收藏 icon,
// which comes onto the tab's; over the last stretch it fades into it.
func (a *App) captureHoming(c *ui.Context) {
	h := &a.capture
	hm := h.homing
	if hm == nil {
		return
	}
	tab := a.favTab
	if tab.W <= 0 {
		c.AnimationFrame() // the sidebar is not laid out yet
		return
	}
	now := c.Now()
	// The island's 收藏 icon, in it and in the window, and the tab's.
	ix, iy := hm.mark+islandStatusW+islandHideW/2, hm.from.H/2
	x0, y0 := float64(hm.from.X+ix), float64(hm.from.Y+iy)
	x1, y1 := float64(tab.X+tab.W/2), float64(tab.Y+tab.H/2)
	if hm.length == 0 {
		hm.length = motion.FlightFor(math.Hypot(x1-x0, y1-y0))
	}
	t := float32(now.Sub(hm.start)) / float32(hm.length)
	k := motion.Flight(min(1, t))
	if !hm.landed && k >= homeLand {
		hm.landed = true
		a.homed(c, hm.items, now)
	}
	if t >= 1 {
		h.homing = nil
		return
	}
	c.AnimationFrame()
	kk := float64(k)
	mx, my := (x0+x1)/2, math.Max(y0, y1)+math.Min(56, math.Hypot(x1-x0, y1-y0)*0.12)
	x := float32((1-kk)*(1-kk)*x0 + 2*(1-kk)*kk*mx + kk*kk*x1)
	y := float32((1-kk)*(1-kk)*y0 + 2*(1-kk)*kk*my + kk*kk*y1)
	// It draws in to the width of the tab's icon.
	s := lerpf(1, tab.W/hm.from.W, motion.DrawIn(k))
	box := ui.Box(c.Key("capture-home")).Absolute().Left(x-ix).Top(y-iy).Size(hm.from.W, hm.from.H).
		TransformOrigin(ix/hm.from.W, 0.5).Scale(s, s).Opacity(1-unit((k-homeFade)/(homeGone-homeFade))).Radius(12).
		Background(theme.Elevated).InsetShadow(0, 0, 0, 1, theme.Line).Shadow(0, 0, 0, 1, theme.ShadowEdge).Shadow(0, 12, 32, 0, theme.ShadowFloat).
		IgnoreInput(true).AccessHidden(true)
	box.Children(func() {
		var taking *packItem
		lit := true
		for i, it := range hm.items {
			lit = lit && it.state == packTaken
			if it.state == packTaking && taking == nil {
				taking = it
			}
			if i >= len(hm.slots) || hm.slots[i].W <= 0 {
				continue // stacked behind, out of sight
			}
			r := hm.slots[i]
			ui.Box(c.Key("thumb-"+it.ID)).Absolute().Left(r.X-hm.from.X).Top(r.Y-hm.from.Y).Size(r.W, r.H).Radius(6).ClipX().ClipY().
				Background(theme.Window).Children(func() { thumbFace(c, it, now, false) })
		}
		// Its right end as it was: the figures of the page being taken, else the mark.
		if taking != nil {
			ui.Row(c.Key("figures")).Absolute().Left(hm.mark).Top(0).Size(islandStatusW, hm.from.H).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				ui.Text(c, percentText(taking.percent(c, now))).FontSize(theme.Body).FontFeatures(theme.Numbers).TextColor(theme.Text).NoWrap()
			})
		} else {
			dots := captureDots(-1, lit, theme.Icon)
			ui.Box(c.Key("mark")).Absolute().Left(hm.mark+(islandStatusW-16)/2).Top(iy-8).Size(16, 16).
				Draw(func(p *ui.Painter, r ui.Rect) { paintDots(p, r, dots, dotLight{}, 0) })
		}
		ui.Box(c.Key("home")).Absolute().Left(ix-8).Top(iy-8).Size(16, 16).TextColor(theme.Text).Children(func() {
			a.dotIcon(c, ui.Element{}, "favorites", 16, iconLook{ink: theme.Text})
		})
	})
}

// tabNews is what 收藏's tab tells of the captures put away into it: the page
// taken while the island was away, for CaptureTold once it was or the pointer
// was last on the tab (done); else the page being taken there; nil while the
// island is still on its way, with nothing to tell, or while 收藏's captures
// show, which tell it themselves.
func (a *App) tabNews(c *ui.Context, now time.Time) (it *packItem, done bool) {
	h := &a.capture
	if h.homing != nil && !h.homing.landed {
		return nil, false
	}
	quiet := a.onShelf()
	if h.told != nil && !h.told.gone {
		if h.toldAt.IsZero() {
			h.toldAt = now
		}
		if until := h.toldAt.Add(motion.CaptureTold); now.Before(until) {
			c.After(until.Sub(now))
			if quiet {
				return nil, false
			}
			return h.told, true
		}
	}
	h.told = nil
	if !h.away || quiet {
		return nil, false
	}
	for _, it := range h.items {
		if it.state == packTaking {
			return it, false
		}
	}
	return nil, false
}

// tabSaysW is the room of the figures or 已拓下 under 收藏's tab, centred
// under its icon.
const tabSaysW = 64

// favIcon is 收藏's icon in its tab b, lit and inked as the tab's, telling of
// it (tabNews): its orbit fills amber clockwise from the satellite as far as
// the page being taken has come, and closes and lights whole once it is
// taken, the figures or 已拓下 coming out of it under the tab into focus.
func (a *App) favIcon(c *ui.Context, b ui.Element, it *packItem, done, lit, jump bool, ink ui.Color) ui.Element {
	h := &a.capture
	still := a.still(c)
	on := map[bool]float32{true: 1}
	glow := b.AnimateTo("glow", on[it != nil], a.ease(motion.SwapIn))
	switch {
	case done:
		h.tabFill = 1
	case it != nil:
		h.tabFill = float32(h.tabPct) / 100
	case glow < 0.01:
		h.tabFill = 0 // gone back to rest: the next fills from the satellite
	}
	var fill float32
	if a.kbd || it == nil && glow < 0.01 {
		fill = b.Jump("fill", h.tabFill)
	} else {
		fill = b.AnimateTo("fill", h.tabFill, motion.Develop)
	}
	kept := b.AnimateTo("kept", on[done], a.ease(motion.Light))
	pct := b.AnimateTo("saysPct", on[it != nil && !done], a.ease(motion.SwapIn))
	told := b.AnimateTo("saysDone", on[done], a.ease(motion.SwapIn))

	var icon ui.Element
	if glow < 0.002 {
		icon = a.dotIcon(c, b, "favorites", 16, iconLook{lit: lit, jump: jump, ink: ink})
	} else {
		icon = ui.Box(c.Key("orbit")).Size(16, 16).Shrink(0)
		acc := a.accentOf(icon, lit, jump)
		light, _ := a.lightOf(c, b, icon)
		dots := orbitFilled(inked(iconDots16["favorites"], ink, acc), fill, glow, kept, !still)
		icon.Draw(func(p *ui.Painter, r ui.Rect) { paintDots(p, r, dots, light, 0) })
	}
	// Under the tab: the tab's 8 px of padding and half the icon in, a little
	// below it, dropping from the icon as they come.
	for _, s := range []struct {
		key, text string
		o         float32
	}{{"pct", percentText(h.tabPct), pct}, {"done", locales.CaptureTakenNote(), told}} {
		if s.o < 0.01 {
			continue
		}
		top := float32(33)
		if !still {
			top -= 3 * (1 - s.o)
		}
		ui.Row(c.Key("says-" + s.key)).Absolute().Left(16 - tabSaysW/2).Top(top).Width(tabSaysW).Justify(ui.Center).
			Opacity(s.o).IgnoreInput(true).AccessHidden(true).Children(func() {
			t := ui.Text(c, s.text).FontSize(theme.Label).LineHeight(1.25).FontFeatures(theme.Numbers).TextColor(theme.Muted).NoWrap()
			if !still && s.o < 1 {
				t.Blur(1.5 * (1 - s.o))
			}
		})
	}
	return icon
}

// orbitFilled is 收藏's icon's dots, ds, its orbit lit amber clockwise from the
// satellite as far as fill, by glow, the dot being lit swelling a little when
// swell; its centre and satellite lit amber as kept, so that, closed, it is
// the orbit of something kept.
func orbitFilled(ds []inkDot, fill, glow, kept float32, swell bool) []inkDot {
	const start, step = -45, 360.0 / 14 // the satellite's place, and the spacing of the orbit's dots
	for i := range ds {
		d := &ds[i]
		dx, dy := float64(d.x-0.5), float64(d.y-0.5)
		if d.acc || math.Hypot(dx, dy) < 0.2 {
			d.c = d.c.Mix(theme.Accent, kept*glow)
			continue
		}
		deg := float32(math.Mod(math.Atan2(dy, dx)*180/math.Pi-start+720, 360))
		f := unit((fill*360-deg)/step + 0.5)
		d.c = d.c.Mix(theme.Accent, f*glow)
		if swell {
			d.r *= 1 + 0.35*glow*float32(math.Sin(math.Pi*float64(f)))
		}
	}
	return ds
}

// thumbFace is it's picture, filling the box it is built in: its first screen
// once taken, else its dots as they stand.
func thumbFace(c *ui.Context, it *packItem, now time.Time, still bool) {
	if it.state == packTaken && it.shot != nil {
		ui.Image(c, it.shot).Absolute().Fill().Fit(ui.Cover)
		return
	}
	d := devAt(it, it.shown(now), now, still)
	ui.Box(c).Absolute().Fill().Draw(func(p *ui.Painter, r ui.Rect) { paintDeveloping(p, r, d) })
}

// capturePost is the note of a page that could not be taken while the island
// was away: it grows out of 收藏's tab, says so and offers 重试, holds, and
// folds back into the tab.
type capturePost struct {
	it   *packItem
	next *packItem // one that failed as it folded: it grows once this is in
	// start is when it began to grow; at when it came to rest, held when the
	// pointer was last on it, and folding when it began to fold into the tab.
	start, at, held, folding time.Time
}

// The note's measure: its height and padding, its thumbnail, how wide its
// words may run, its gap under 收藏's tab, and 重试's room at its right.
const (
	postH, postPad = 46, 8
	postThumbW     = 48
	postThumbH     = 30
	postTextMax    = 200
	postUnder      = 6
	postRetryW     = 28
)

// postFailed is the note growing out of 收藏's tab for it, which could not be
// taken while the island was away: one showing becomes it where it is, one
// folding lets it grow once it is in. VoiceOver hears of each.
func (a *App) postFailed(it *packItem, now time.Time) {
	h := &a.capture
	a.said = append(a.said, locales.CaptureDidFail(it.Name))
	switch p := h.post; {
	case p == nil:
		h.post = &capturePost{it: it, start: now}
	case p.folding.IsZero():
		p.it, p.held = it, time.Time{}
		if !p.at.IsZero() {
			p.at = now // at rest, it holds afresh
		}
	default:
		p.next = it
	}
}

// foldPost sends the note into 收藏's tab now.
func (h *captureState) foldPost(now time.Time) {
	if p := h.post; p != nil && p.folding.IsZero() {
		p.folding = now
	}
}

// capturePost draws the note: growing out of 收藏's tab, at rest under it, or
// folding back into it. It holds two buttons side by side: the page, which
// opens 收藏's captures, and 重试.
func (a *App) capturePost(c *ui.Context) {
	h := &a.capture
	p := h.post
	if p == nil {
		return
	}
	tab := a.favTab
	if tab.W <= 0 {
		c.AnimationFrame() // the sidebar is not laid out yet
		return
	}
	now := c.Now()
	still := a.still(c)
	it := p.it
	// Its page deleted, or taken again from its card in 收藏, it has nothing left to say.
	live := !it.gone && it.state == packFailed
	if !live {
		h.foldPost(now)
	}
	first, second := locales.CaptureNotTakenShort(), it.Name
	textW := min(postTextMax, max(textWidth(first, theme.Body), textWidth(second, theme.Label)))
	textX := float32(postPad + postThumbW + 10)
	openW := textX + textW + 10
	note := ui.Rect{X: tab.X - 8, Y: tab.Y + tab.H + 8 + postUnder, W: openW + postRetryW + postPad, H: postH}

	g := float32(1) // grown out of the tab
	if t := now.Sub(p.start); !a.kbd && t < motion.PopIn {
		g = motion.Ease(float32(t) / float32(motion.PopIn))
		c.AnimationFrame()
	}
	if p.at.IsZero() && g >= 1 {
		p.at = now
	}
	// It holds NoticeFor, and that long after the pointer was last on it; then it folds.
	if !p.at.IsZero() && p.folding.IsZero() {
		until := p.at
		if p.held.After(until) {
			until = p.held
		}
		if until = until.Add(motion.NoticeFor); now.Before(until) {
			c.After(until.Sub(now))
		} else {
			p.folding = now
		}
	}
	var f float32 // folded back into the tab
	if !p.folding.IsZero() {
		f = 1
		if !a.kbd {
			f = unit(float32(now.Sub(p.folding)) / float32(motion.PopOut))
		}
		if f >= 1 {
			h.post = nil
			if n := p.next; n != nil && !n.gone && n.state == packFailed {
				h.post = &capturePost{it: n, start: now}
			}
			a.lights[favLight] = now
			c.AnimationFrame()
			return
		}
		c.AnimationFrame()
	}
	resting := g >= 1 && f == 0 && live

	box := ui.Box(c.Key("capture-post")).Absolute().Left(note.X).Top(note.Y).Height(note.H).Radius(12).ClipX().ClipY().
		Background(theme.Elevated).InsetShadow(0, 0, 0, 1, theme.Line).Shadow(0, 0, 0, 1, theme.ShadowEdge).Shadow(0, 12, 32, 0, theme.ShadowFloat).
		Role(ui.RoleGroup).Label(first + " · " + second)
	// At rest, a note changing into another goes to its new width on the follow spring.
	if resting && !a.kbd {
		box.Width(box.AnimateTo("w", note.W, motion.Follow))
	} else {
		box.Width(box.Jump("w", note.W))
	}
	// Growing out of the tab and folding back into it, one whole, toward the tab's icon.
	if s := g * (1 - f); s < 1 {
		ox := (tab.X + tab.W/2 - note.X) / note.W
		oy := (tab.Y + tab.H/2 - note.Y) / note.H
		sc := 0.2 + 0.8*motion.Ease(s)
		if still {
			sc = 1
		}
		box.TransformOrigin(ox, oy).Scale(sc, sc).Opacity(min(1, 2*s) * (1 - f*f))
	}
	if box.Hovered() {
		p.held = now
	}
	box.Children(func() {
		// The page: pressed, the note folds into the tab as 收藏's captures open,
		// its card there with 重试 too.
		open := ui.ButtonBase(c.Key("open")).Absolute().Left(0).Top(0).Size(openW, postH).Radius(12, 0, 0, 12).FocusRing(false).
			Label(first + " · " + second)
		if !resting {
			open.IgnoreInput(true)
		}
		if open.FocusVisible() {
			open.FocusRing(true).FocusRingOffset(-2)
		}
		open.Children(func() {
			ui.Box(c.Key("thumb")).Absolute().Left(postPad).Top((postH-postThumbH)/2).Size(postThumbW, postThumbH).Radius(4).ClipX().ClipY().
				Background(theme.Window).IgnoreInput(true).AccessHidden(true).Children(func() { thumbFace(c, it, now, still) })
			ui.Column(c.Key("words")).Absolute().Left(textX).Top((postH - 34) / 2).Width(textW).IgnoreInput(true).AccessHidden(true).Children(func() {
				ui.Text(c, first).FontSize(theme.Body).LineHeight(18.0 / 13).TextColor(theme.Text).SingleLine().Ellipsis("…")
				ui.Text(c, second).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).SingleLine().Ellipsis("…")
			})
		})
		if open.Clicked() && resting {
			h.foldPost(now)
			a.showShelf(c)
		}
		rb := a.cardTool(c, "retry", "refresh", locales.CommonTryAgain(), locales.CommonTryAgain(), true, false, false)
		rb.Absolute().Right(postPad - 2).Top((postH - 32) / 2)
		if !resting {
			rb.IgnoreInput(true)
		}
		if rb.Clicked() && resting {
			h.foldPost(now)
			a.retryPack(it, now)
		}
	})
	a.control(box)
}
