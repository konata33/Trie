package app

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/motion"
	"trie/internal/theme"
)

// The icons and the kind marks are ink dots (DESIGN.md Icons, 点会走，光会传).
// At rest a drawing is two SVG layers made from its dots (dots_gen.go),
// which ui.Icon draws in the colour of the text around it: its ink, and its
// accent in icon-fill or amber. While something lights it (the pointer, a
// press's ring) or moves its dots, it paints them itself, and asks for
// frames only while a ring still runs.

// dot is one ink dot of an icon or a kind mark (dots_gen.go), in the
// drawing's unit box: its centre, radius and alpha, and whether it is the
// accent.
type dot struct {
	x, y, r, a float32
	acc        bool
}

// dotCut returns the table that draws name at size (each size its own dots):
// a kind mark's 28 or 16; an icon's 16, scaled above it, or 12 below 14.
func dotCut(name string, size float32) (int, []dot) {
	if strings.HasPrefix(name, "kind-") {
		if size > 20 {
			return 28, kindDots28[name]
		}
		return 16, kindDots16[name]
	}
	if size < 14 {
		return 12, iconDots12[name]
	}
	return 16, iconDots16[name]
}

type dotKey struct {
	name string
	cut  int
}

// dotLayers are a drawing at rest: its ink, and its accent (nil without one).
type dotLayers struct{ ink, accent *ui.SVG }

var dotArt = sync.OnceValue(func() map[dotKey]dotLayers {
	m := map[dotKey]dotLayers{}
	for _, t := range []struct {
		cut  int
		dots map[string][]dot
	}{{12, iconDots12}, {16, iconDots16}, {28, kindDots28}, {16, kindDots16}} {
		for name, ds := range t.dots {
			m[dotKey{name, t.cut}] = dotLayers{ink: dotSVG(ds, false), accent: dotSVG(ds, true)}
		}
	}
	return m
})

// dotSVG draws the dots of ds that are (acc) or are not the accent, in
// currentColor at their alphas.
func dotSVG(ds []dot, acc bool) *ui.SVG {
	var b strings.Builder
	for _, d := range ds {
		if d.acc == acc {
			fmt.Fprintf(&b, `<circle cx="%.3f" cy="%.3f" r="%.3f" fill="currentColor" fill-opacity="%.3f"/>`, d.x*100, d.y*100, d.r*100, d.a)
		}
	}
	if b.Len() == 0 {
		return nil
	}
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="100" height="100">` + b.String() + `</svg>`))
}

// iconLook is how an icon shows beyond its name and size: lit; turned, in
// degrees; jump, its accent's colour changing at once (a tab's, as the
// light lands); ink, the colour of the words of the button it is in, which
// it is drawn in while that button's pointer or presses light it; copied,
// copy's dots flowed into a check.
type iconLook struct {
	lit, jump, copied bool
	turn              float32
	ink               ui.Color
}

// icon builds icon name at size, its accent lit amber when lit: the accent
// eases over 200 ms, at once after a key.
func (a *App) icon(c *ui.Context, name string, size float32, lit bool) ui.Element {
	return a.dotIcon(c, ui.Element{}, name, size, iconLook{lit: lit})
}

// dotIcon is icon name at size as l says, in button in (or none), whose
// pointer lights it and whose presses ring out of it.
func (a *App) dotIcon(c *ui.Context, in ui.Element, name string, size float32, l iconLook) ui.Element {
	cut, ds := dotCut(name, size)
	box := ui.Box(c).Size(size, size).Shrink(0)
	acc := a.accentOf(box, l.lit, l.jump)
	var flow float32
	if name == "copy" {
		flow = a.flow(c, box, l.copied)
	}
	light, lit := a.lightOf(c, in, box)
	if lit || flow > 0 {
		dots := inked(ds, l.ink, acc)
		if flow > 0 {
			dots = flowed(dots, flow)
		}
		return box.Draw(func(p *ui.Painter, r ui.Rect) { paintDots(p, r, dots, light, l.turn) })
	}
	art := dotArt()[dotKey{name, cut}]
	if l.turn != 0 {
		box.TransformOrigin(0.5, 0.5).Rotate(l.turn)
	}
	return box.Children(func() {
		if art.accent != nil {
			ui.Icon(c, art.accent).Absolute().Left(0).Top(0).Size(size, size).TextColor(acc)
		}
		ui.Icon(c, art.ink).Absolute().Left(0).Top(0).Size(size, size)
	})
}

// accentOf is the colour of e's accent: icon-fill, amber lit.
func (a *App) accentOf(e ui.Element, lit, jump bool) ui.Color {
	target, ease := theme.IconFill, a.ease(motion.Light)
	if lit {
		target = theme.Accent
	}
	if jump {
		ease = ui.Animation{Duration: time.Nanosecond}
	}
	return e.AnimateColor("accent", target, ease)
}

// dotLight is what lights a drawing's dots beyond their colours: the
// pointer, a lamp at (x, y) in the drawing's px as strong as h, and the
// rings of the presses on its button.
type dotLight struct {
	x, y, h float32
	rings   []dotRing
}

// dotRing is a press's ring of light: where it went down, in the drawing's
// px, and when.
type dotRing struct {
	x, y float32
	at   time.Time
}

// lightOf is the light on drawing e in button b: the pointer's lamp while it
// is over b, coming and going over 150 ms, and a ring from where each press
// went down. ok is false while nothing lights it, with no button, after a
// key or under less motion, and the drawing shows at rest.
func (a *App) lightOf(c *ui.Context, b, e ui.Element) (l dotLight, ok bool) {
	if !b.Valid() || a.still(c) {
		return dotLight{}, false
	}
	s := ui.Local(b, "dots", func() dotLight { return dotLight{} })
	px, py, over := b.PointerPosition()
	bb, eb := b.LayoutBounds(), e.LayoutBounds()
	x, y := px+bb.X-eb.X, py+bb.Y-eb.Y
	now := c.Now()
	if b.PressedInside() && !a.kbd && (len(s.rings) == 0 || !s.rings[len(s.rings)-1].at.Equal(now)) {
		s.rings = append(s.rings, dotRing{x, y, now})
	}
	s.rings = slices.DeleteFunc(s.rings, func(r dotRing) bool { return now.Sub(r.at) >= motion.Ring })
	on := over && !a.kbd
	if on {
		s.x, s.y = x, y
	}
	s.h = b.AnimateTo("dotLight", map[bool]float32{true: 1}[on], a.ease(motion.Hover))
	l = *s
	l.rings = slices.Clone(s.rings)
	return l, l.h > 0.001 || len(l.rings) > 0
}

// inkDot is a dot as it paints, in its drawing's unit box: its colour
// before its alpha, and whether it lights as the accent does.
type inkDot struct {
	x, y, r, a float32
	c          ui.Color
	acc        bool
}

func inked(ds []dot, ink, acc ui.Color) []inkDot {
	out := make([]inkDot, len(ds))
	for i, d := range ds {
		out[i] = inkDot{x: d.x, y: d.y, r: d.r, a: d.a, c: ink, acc: d.acc}
		if d.acc {
			out[i].c = acc
		}
	}
	return out
}

// The pointer's lamp: how far it reaches, e^(−d²/lampReach) over the
// distance d in px, about 7; how much nearer it draws a dot (px) and how
// much larger it makes it; and a ring's: how wide its light is (px), and
// how much larger it makes the dots it passes.
const (
	lampReach = 110
	lampLean  = 0.5
	lampGrow  = 0.15
	ringWidth = 2.6
	ringGrow  = 0.75
)

// paintDots paints ds in r, turned by turn degrees round its centre, lit
// by l: near the lamp a dot grows, brightens and leans toward it; as a ring
// passes, it grows and lights up, an accent in amber. It asks for the next
// frame while a ring still runs.
func paintDots(p *ui.Painter, r ui.Rect, ds []inkDot, l dotLight, turn float32) {
	now := p.Now()
	sin, cos := math.Sincos(float64(turn) * math.Pi / 180)
	moving := false
	for _, d := range ds {
		x, y := d.x-0.5, d.y-0.5
		if turn != 0 {
			x, y = x*float32(cos)-y*float32(sin), x*float32(sin)+y*float32(cos)
		}
		x, y = (0.5+x)*r.W, (0.5+y)*r.H // in the drawing's px
		rad := d.r * r.W
		var lamp, ring float32
		if l.h > 0 {
			dx, dy := l.x-x, l.y-y
			dd := float32(math.Hypot(float64(dx), float64(dy)))
			lamp = l.h * float32(math.Exp(-float64(dd*dd)/lampReach))
			x += dx / (dd + 1e-6) * lampLean * lamp
			y += dy / (dd + 1e-6) * lampLean * lamp
		}
		for _, q := range l.rings {
			s := now.Sub(q.at).Seconds()
			if s < 0 || s >= motion.Ring.Seconds() {
				continue
			}
			moving = true
			e := math.Hypot(float64(q.x-x), float64(q.y-y))
			w := (e - s*motion.RingSpeed) / ringWidth
			ring = max(ring, float32(math.Exp(-w*w)*math.Exp(-s/motion.RingFade.Seconds())))
		}
		rad *= 1 + lampGrow*lamp + ringGrow*ring
		c := d.c.Mix(theme.IconLit, max(0.4*lamp, ring))
		if d.acc {
			c = d.c.Mix(theme.Accent, ring)
		}
		p.Fill(ui.Rect{X: r.X + x - rad, Y: r.Y + y - rad, W: 2 * rad, H: 2 * rad}, c.Alpha(d.a), rad)
	}
	if moving {
		p.AnimationFrame()
	}
}

// flow is how far copy's dots have flowed into a check: one value, out over
// Flow once copied, back over FlowBack; at once after a key or under less
// motion, where the icon simply changes.
func (a *App) flow(c *ui.Context, e ui.Element, copied bool) float32 {
	switch {
	case a.kbd || a.still(c):
		return e.Jump("flow", map[bool]float32{true: 1}[copied])
	case copied:
		return e.AnimateTo("flow", 1, ui.Animation{Duration: motion.Flow, Ease: ui.Linear})
	}
	return e.AnimateTo("flow", 0, ui.Animation{Duration: motion.FlowBack, Ease: ui.Linear})
}

// flowed is ds, copy's dots, k of the way into a check (dots2.mjs
// morphA): each goes to its share of the check's line, a little after the
// one before, lifted a little on the way, smaller, and lit amber.
func flowed(ds []inkDot, k float32) []inkDot {
	n := float32(len(ds))
	out := make([]inkDot, len(ds))
	for i, d := range ds {
		e := motion.Ease(min(1, max(0, (k-float32(i)/n*0.35)/0.65)))
		tx, ty := alongCheck(float32(i) / max(1, n-1))
		lift := float32(math.Sin(math.Pi*float64(e))) * 2.2 / 24
		amber := d.c.Mix(theme.Accent, min(1, 1.2*e))
		if d.acc {
			amber = d.c.Mix(theme.Accent, e)
		}
		out[i] = inkDot{x: d.x + (tx-d.x)*e, y: d.y + (ty-d.y)*e - lift, r: d.r + (checkDot-d.r)*e, a: d.a + (1-d.a)*e, c: amber, acc: true}
	}
	return out
}

// checkDot is a dot's radius on the check copy flows into.
const checkDot = 0.9 * 1.5 / 24

// alongCheck is the point u of the way along checkLine.
func alongCheck(u float32) (x, y float32) {
	var lens [2]float32
	total := float32(0)
	for i := range lens {
		lens[i] = float32(math.Hypot(float64(checkLine[i+1][0]-checkLine[i][0]), float64(checkLine[i+1][1]-checkLine[i][1])))
		total += lens[i]
	}
	s := u * total
	for i, l := range lens {
		if s <= l || i == len(lens)-1 {
			f := min(1, s/l)
			return checkLine[i][0] + (checkLine[i+1][0]-checkLine[i][0])*f, checkLine[i][1] + (checkLine[i+1][1]-checkLine[i][1])*f
		}
		s -= l
	}
	return checkLine[2][0], checkLine[2][1]
}

// accentDots are the accent dots of icon name, 16 across, drawn in r, in
// window px.
func accentDots(name string, r ui.Rect) []inkDot {
	var out []inkDot
	for _, d := range iconDots16[name] {
		if d.acc {
			out = append(out, inkDot{x: r.X + d.x*r.W, y: r.Y + d.y*r.H, r: d.r * r.W, a: d.a})
		}
	}
	return out
}

// paintFlight paints the light going from one icon's accent dots to
// another's, k of the way (DESIGN.md 选中的光是一份): the leading dots leave
// first, each on a low arc, 5 px high and 6% of its way, a quarter larger
// midway; where the two have not as many, they part or join.
func paintFlight(p *ui.Painter, from, to []inkDot, k float32) {
	if len(from) == 0 || len(to) == 0 {
		return
	}
	dir := float32(1)
	if to[0].x < from[0].x {
		dir = -1
	}
	lead := func(ds []inkDot) []inkDot {
		ds = slices.Clone(ds)
		slices.SortStableFunc(ds, func(u, v inkDot) int { return int(sign(dir * (v.x - u.x))) })
		return ds
	}
	from, to = lead(from), lead(to)
	n := max(len(from), len(to))
	for j := range n {
		u, v := from[j*len(from)/n], to[j*len(to)/n]
		q := min(1, max(0, (k-0.12*float32(j)/float32(n))/0.88))
		arc := float32(math.Sin(math.Pi * float64(q)))
		x, y := u.x+(v.x-u.x)*q, u.y+(v.y-u.y)*q-arc*(5+0.06*float32(math.Abs(float64(v.x-u.x))))
		rad := (u.r + (v.r-u.r)*q) * (1 + 0.25*arc)
		p.Fill(ui.Rect{X: x - rad, Y: y - rad, W: 2 * rad, H: 2 * rad}, theme.Accent.Alpha(u.a+(v.a-u.a)*q), rad)
	}
}

// orbitDots are the favourite in dots, size across (DESIGN.md 收藏用轨道):
// a centre, and a ring of dots round it at the favorites icon's spacing in
// px, k of the way from an arc of seven tenths from the top, clockwise,
// to closed, the dots ahead of its end coming in as it nears them; and,
// once kept, the satellite at its end, round from 108° short of the top by
// round, the ring's dots giving way where it passes.
func orbitDots(size, k, round float32, kept bool, ink ui.Color) []inkDot {
	const ring = 8.4 / 24 // the ring's radius, as the icon's
	unit := max(1.5*size/24, 0.9) / size
	n := max(6, int(math.Round(2*math.Pi*ring*float64(size)/2.87)))
	step := float32(360) / float32(n)
	sweep := 360 * (0.7 + 0.3*k)
	at := func(deg, rad float32) (float32, float32) {
		s, c := math.Sincos(float64(deg) * math.Pi / 180)
		return 0.5 + rad*float32(c), 0.5 + rad*float32(s)
	}
	sat := min(1, k*4)
	if kept {
		sat = 1
	}
	sx, sy := at(-90-108*(1-round), ring)
	out := []inkDot{{x: 0.5, y: 0.5, r: 1.25 * unit, a: 1, c: ink, acc: kept}}
	for i := range n {
		off := float32(i) * step
		a := min(1, max(0, (sweep-off)/(0.6*step)+1))
		x, y := at(-90+off, ring)
		if sat > 0 {
			gap := float32(math.Hypot(float64(x-sx), float64(y-sy))) * 24
			a *= 1 - sat*(1-min(1, max(0, (gap-1.3)/1.4)))
		}
		if a > 0.01 {
			out = append(out, inkDot{x: x, y: y, r: unit, a: a, c: ink, acc: kept})
		}
	}
	if sat > 0.001 {
		out = append(out, inkDot{x: sx, y: sy, r: 1.8 * unit, a: sat, c: ink, acc: kept})
	}
	return out
}
