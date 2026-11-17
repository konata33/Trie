package app

import (
	"cmp"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/motion"
	"trie/internal/theme"
)

// The pictograms (DESIGN.md Illustration): scenes of ink dots, 168 wide
// over a grid of 200 by 160 units, grey with one amber part, moving all
// the while they show and never twice the same. Each is the approved
// study's (.context/icon-redo/dots-scenes.mjs), its geometry and motion
// as there: every dot's speed and phase hashed from its index, value noise
// on top, and schedules of rounds and light passes that start over once
// run through.

// sceneDot is one dot of a scene, in the scene's units (y down): r its radius;
// z its depth, -1 far (smaller, darker) to 1 near (larger, brighter); g the
// light on it, 0 to 1; a its opacity; mix how far it has turned amber. A
// ghost is the faint layer's; amber, the one amber part.
type sceneDot struct {
	x, y, r, z, g, a, mix float64
	ghost, amber          bool
}

// A pictogramScene appends its dots t seconds after it first showed to ds,
// or its one still under less motion.
type pictogramScene func(t float64, still bool, ds []sceneDot) []sceneDot

var pictogramScenes = map[string]pictogramScene{
	"loading": loadingScene, "unreachable": unreachableScene, "broken": brokenScene, "picture": pictureScene,
	"clips": clipsScene, "favorites": favoritesScene, "mailbox": mailboxScene,
}

// pictogramFPS is how often a moving pictogram paints (motion.Pictogram):
// the dots drift slowly enough not to need the display's rate.
const pictogramFPS = float32(time.Second / motion.Pictogram)

// pictogram builds pictogram name, 168 wide. It paints itself again while
// it moves, without building the view, and only while it paints: out of
// view or with the window hidden, nothing asks for a frame.
func (a *App) pictogram(c *ui.Context, name string) ui.Element {
	draw := pictogramScenes[name]
	box := ui.Box(c).Width(168).AspectRatio(200.0 / 160).Shrink(0).AccessHidden(true)
	still := a.still(c)
	since := *ui.Local(box, "since", func() time.Time { return c.Now() })
	return box.Draw(func(p *ui.Painter, r ui.Rect) {
		buf := sceneBuffers.Get().(*[]sceneDot)
		*buf = draw(p.Now().Sub(since).Seconds(), still, (*buf)[:0])
		paintScene(p, r, *buf)
		sceneBuffers.Put(buf)
		if !still {
			p.AnimationFrameRate(pictogramFPS)
		}
	})
}

var sceneBuffers = sync.Pool{New: func() any { return new([]sceneDot) }}

// sceneUnit is a circle of radius 1 at the origin: Fill snaps its rectangle
// to whole pixels, so each dot is this one scaled and moved, which keeps
// its size and place between pixels.
var sceneUnit = ui.Rect{X: -1, Y: -1, W: 2, H: 2}

// paintScene paints ds over r, the far ones first. Grey runs from muted
// (far) through their middle (at rest) to text (near or lit).
func paintScene(p *ui.Painter, r ui.Rect, ds []sceneDot) {
	slices.SortStableFunc(ds, func(a, b sceneDot) int { return cmp.Compare(a.z, b.z) })
	k := float64(r.W) / 200
	ghost := theme.Muted.Mix(theme.Text, 0.7)
	for _, d := range ds {
		col := ghost
		switch {
		case d.amber:
			col = theme.Accent
		case !d.ghost:
			col = theme.Muted.Mix(theme.Text, float32(0.5*(1+clamp01(max(d.z, 0)+d.g)-clamp01(-d.z))))
		}
		if d.mix > 0 {
			col = col.Mix(theme.Accent, float32(d.mix))
		}
		col = col.Alpha(float32(clamp01(d.a)))
		rad := float32(max(0.3, d.r*(1+0.3*d.z)*k))
		at := ui.Scaling(rad, rad).Then(ui.Translation(r.X+float32(d.x*k), r.Y+float32(d.y*k)))
		p.Transform(at, 0, 0, func() { p.Fill(sceneUnit, col, 1) })
	}
}

// ── numbers

func picLerp(a, b, k float64) float64 { return a + (b-a)*k }

// picStep is smoothstep on 0..1.
func picStep(x float64) float64 {
	x = clamp01(x)
	return x * x * (3 - 2*x)
}

// picEase is the standard curve on 0..1.
func picEase(x float64) float64 { return float64(motion.Ease(float32(clamp01(x)))) }

// picHash is a constant for i under seed s, 0 to 1.
func picHash(i, s float64) float64 {
	h := math.Sin(i*127.1+s*311.7+0.5) * 43758.5453
	return h - math.Floor(h)
}

// picNoise is smooth value noise along x under seed s, -1 to 1.
func picNoise(x, s float64) float64 {
	i := math.Floor(x)
	f := x - i
	return picLerp(picHash(i, s), picHash(i+1, s), f*f*(3-2*f))*2 - 1
}

// picWrap brings t into a schedule from t0 lasting span, which starts
// over.
func picWrap(t, t0, span float64) float64 { return t0 + math.Mod(math.Mod(t-t0, span)+span, span) }

// ── lines

type picPt = [2]float64

func picLengths(P []picPt) []float64 {
	L := []float64{0}
	for i := 1; i < len(P); i++ {
		L = append(L, L[i-1]+math.Hypot(P[i][0]-P[i-1][0], P[i][1]-P[i-1][1]))
	}
	return L
}

// picAlong is the point s along P, whose running lengths are L.
func picAlong(P []picPt, L []float64, s float64) picPt {
	s = max(0, min(s, L[len(L)-1]))
	i := 1
	for i < len(L)-1 && L[i] < s {
		i++
	}
	f := (s - L[i-1]) / max(1e-9, L[i]-L[i-1])
	return picPt{picLerp(P[i-1][0], P[i][0], f), picLerp(P[i-1][1], P[i][1], f)}
}

// picEven sets dots evenly along P: a closed line gets n equal gaps all
// round; an open one starts and ends on a dot.
func picEven(P []picPt, pitch float64, closed bool) []picPt {
	if closed {
		P = append(slices.Clip(P), P[0])
	}
	L := picLengths(P)
	T := L[len(L)-1]
	n := max(1, int(math.Round(T/pitch)))
	m := n + 1
	if closed {
		m = n
	}
	o := make([]picPt, 0, m)
	for k := range m {
		o = append(o, picAlong(P, L, T*float64(k)/float64(n)))
	}
	return o
}

// picArc is n+1 points round (cx, cy) from angle a0 to a1.
func picArc(cx, cy, r, a0, a1 float64, n int) []picPt {
	o := make([]picPt, 0, n+1)
	for i := range n + 1 {
		a := a0 + (a1-a0)*float64(i)/float64(n)
		o = append(o, picPt{cx + r*math.Cos(a), cy + r*math.Sin(a)})
	}
	return o
}

// picRoundRect is the outline of a rounded rectangle, clockwise from the
// right end of its top edge to the left end, which it does not close.
func picRoundRect(x0, y0, x1, y1, r float64) []picPt {
	const h = math.Pi / 2
	return slices.Concat(picArc(x1-r, y0+r, r, -h, 0, 10), picArc(x1-r, y1-r, r, 0, h, 10),
		picArc(x0+r, y1-r, r, h, 2*h, 10), picArc(x0+r, y0+r, r, 2*h, 3*h, 10))
}

func picInRoundRect(x, y, x0, y0, x1, y1, r float64) bool {
	dx, dy := max(x0+r-x, 0, x-x1+r), max(y0+r-y, 0, y-y1+r)
	return x >= x0 && x <= x1 && y >= y0 && y <= y1 && math.Hypot(dx, dy) <= r
}

// sceneR is a dot's radius on a line, in units.
const sceneR = 1.25

// ── loading and unreachable: one page, a faint sheet with rows of dots
// for its text, in reading order.

type pageParts struct{ sheet, slots []picPt }

var loadingPage = sync.OnceValue(func() pageParts {
	var p pageParts
	p.sheet = picEven(picRoundRect(56, 16, 144, 144, 5), 5.4, true)
	for _, row := range [][2]float64{{32, 13}, {43, 12}, {54, 13}, {65, 7}, {84, 13}, {95, 13}, {106, 11}, {117, 13}, {128, 5}} {
		for j := range int(row[1]) {
			p.slots = append(p.slots, picPt{67.6 + float64(j)*5.4, row[0]})
		}
	}
	return p
})

func (p pageParts) sheetDots(ds []sceneDot) []sceneDot {
	for _, s := range p.sheet {
		ds = append(ds, sceneDot{x: s[0], y: s[1], r: 0.95, a: 0.2, ghost: true})
	}
	return ds
}

// sceneLoose is dot i adrift: a home in the box (x0, y0)–(x1, y1) under seed
// s, round which it wanders on its own noise, at its own speed and depth.
func sceneLoose(i, t, x0, y0, x1, y1, s float64) (x, y, z float64) {
	hx, hy, sp := picLerp(x0, x1, picHash(i, s)), picLerp(y0, y1, picHash(i, s+1)), 0.16+0.28*picHash(i, s+2)
	return hx + 9*picNoise(t*sp+i*7.3, s+3), hy + 7*picNoise(t*sp*0.87+i*3.1, s+4), -0.55 + 0.75*(0.5+0.5*picNoise(t*sp*0.6+i*1.9, s+5))
}

// A loadingRound: from t, the page assembles and holds until hold,
// then lets go and drifts until length.
type loadingRound struct{ t, hold, length float64 }

var loadingRounds = sync.OnceValue(func() []loadingRound {
	var rs []loadingRound
	T := -0.35
	for i := range 80 {
		fi := float64(i)
		hold := 2.95 + 0.4*picHash(fi, 3)
		l := hold + 2.15 + 0.6*picHash(fi, 5)
		rs = append(rs, loadingRound{T, hold, l})
		T += l
	}
	return rs
})

// loadingScene: each round the dots assemble in reading order (the amber
// opening dot first), hold, let go in no order (the opening dot last) and
// drift; every round's lengths and every dot's start and speed are its
// own. Held, each dot still stirs round its place. Still, the page whole.
func loadingScene(t float64, still bool, ds []sceneDot) []sceneDot {
	if still {
		t = 2.3
	}
	pg, rs := loadingPage(), loadingRounds()
	last := rs[len(rs)-1]
	tw := picWrap(t, rs[0].t, last.t+last.length-rs[0].t)
	ci := 0
	for ci < len(rs)-1 && tw >= rs[ci].t+rs[ci].length {
		ci++
	}
	c, seed := rs[ci], float64(ci*7)
	u, n, stir := tw-c.t, float64(len(pg.slots)), 1.0
	if still {
		stir = 0
	}
	ds = pg.sheetDots(ds)
	for i, slot := range pg.slots {
		fi := float64(i)
		a0, r0, size := 0.0, c.hold+0.8, 1.25
		if i > 0 {
			a0, r0, size = 0.1+fi/n+0.2*picHash(fi, seed+1), c.hold+0.75*picHash(fi, seed+4), 1
		}
		m, d := 0.6+0.25*picHash(fi, seed+2), 1.0+0.3*picHash(fi, seed+6)
		s := picEase((u-a0)/m) * (1 - picEase((u-r0)/d))
		lx, ly, lz := sceneLoose(fi, t, 40, 12, 160, 148, 40)
		hx := slot[0] + 0.3*stir*picNoise(t*0.4+fi*1.3, 44)
		hy := slot[1] + 0.25*stir*picNoise(t*0.35+fi*2.1, 45)
		ds = append(ds, sceneDot{x: picLerp(lx, hx, s), y: picLerp(ly, hy, s), z: picLerp(lz, 0, s), r: picLerp(0.85, size, s) * sceneR,
			a: picLerp(0.55, 1, s), amber: i == 0})
	}
	return ds
}

// unreachableScene: the same page stalled, its top rows formed, the next
// place held by one amber dot, the rest hovering below.
func unreachableScene(t float64, still bool, ds []sceneDot) []sceneDot {
	if still {
		t = 0
	}
	const stop = 13 + 12 + 6
	pg := loadingPage()
	ds = pg.sheetDots(ds)
	for _, s := range pg.slots[:stop] {
		ds = append(ds, sceneDot{x: s[0], y: s[1], r: sceneR, a: 1})
	}
	s := pg.slots[stop]
	ds = append(ds, sceneDot{x: s[0], y: s[1] + 0.3*picNoise(t*0.5, 52), r: sceneR * (1.22 + 0.07*picNoise(t*0.7, 51)), a: 1, amber: true})
	for i := stop + 1; i < len(pg.slots); i++ {
		x, y, z := sceneLoose(float64(i), t, 52, 72, 148, 146, 60)
		ds = append(ds, sceneDot{x: x, y: y, z: z, r: 0.85 * sceneR, a: 0.55})
	}
	return ds
}

// ── broken: an empty dotted frame (a view, its header line fainter); all
// it held lies on its floor as beads.

type brokenBead struct{ x, y, row float64 }

type brokenParts struct {
	frame, head []picPt
	beads       []brokenBead
}

var brokenDots = sync.OnceValue(func() brokenParts {
	p := brokenParts{frame: picEven(picRoundRect(36, 22, 164, 136, 6), 5.2, true), head: picEven([]picPt{{42, 37}, {158, 37}}, 5.2, false)}
	const br, dx, dy, cx = 2.4, 5.0, 4.3, 108.0
	fy := 136 - sceneR - br - 0.8
	for j, n := range []int{13, 10, 7, 4, 1} {
		for k := range n {
			kk, b := float64(k+20*j), brokenBead{row: float64(j)}
			b.x = cx + (float64(k)-float64(n-1)/2)*dx + (picHash(kk, 71)-0.5)*1.3
			b.y = fy - float64(j)*dy
			if j > 0 {
				b.y += (picHash(kk, 72) - 0.5) * 0.8
			}
			p.beads = append(p.beads, b)
		}
	}
	p.beads = append(p.beads, brokenBead{cx - 41, fy, 0}, brokenBead{cx - 49.5, fy, 0}, brokenBead{cx + 40, fy, 0}) // a few rolled away
	return p
})

func brokenScene(t float64, still bool, ds []sceneDot) []sceneDot {
	if still {
		t = 0
	}
	p := brokenDots()
	for _, f := range p.frame {
		ds = append(ds, sceneDot{x: f[0], y: f[1], r: sceneR, a: 1})
	}
	for _, h := range p.head {
		ds = append(ds, sceneDot{x: h[0], y: h[1], r: 0.95, a: 0.45})
	}
	for i, b := range p.beads {
		fi := float64(i)
		sp, m := 0.2+0.3*picHash(fi, 76), 1+0.6*b.row
		ds = append(ds, sceneDot{x: b.x + 0.35*m*picNoise(t*sp+fi*1.7, 74), y: b.y + 0.2*m*picNoise(t*sp*1.2+fi*2.3, 75), r: 2.4,
			z: (picHash(fi, 73) - 0.5) * 0.8, g: 0.07 * b.row, a: 1, amber: i == 16})
	}
	return ds
}

// ── picture: a halftone landscape (a hill, the sun in amber) developed
// up to a soft edge that breathes; past it, the even faint grid of an
// undeveloped plate.

type pictureDot struct{ x, y, ink, sun float64 }

var pictureDots = sync.OnceValue(func() []pictureDot {
	const sx, sy, sr = 58.0, 50.0, 15.0
	ridge := func(x float64) float64 {
		return 112 - 40*math.Exp(-math.Pow((x-122)/50, 2)) - 10*math.Exp(-math.Pow((x-26)/40, 2))
	}
	var o []pictureDot
	for j := range 26 {
		for i := range 34 {
			x, y := 11+float64(i)*5.4, 11+float64(j)*5.4
			if !picInRoundRect(x, y, 9, 9, 191, 151, 9) {
				continue
			}
			// The ink, and how much of it is sun; edges soft, so the screen does not stair-step.
			d := math.Hypot(x-sx, y-sy)
			sun, h := picStep((sr-d)/3+0.5), ridge(x)
			hill := picStep((y-h)/5 + 0.5)
			sky := 0.05 + 0.15*picStep((y-10)/100) + 0.22*math.Exp(-math.Pow(max(0, d-sr)/9, 2))
			land := clamp01(0.34 + 0.22*math.Exp(-max(0, y-h)/14) + 0.12*max(-1, min(1, (120-x)/70)) - 0.1*picStep((y-122)/30))
			o = append(o, pictureDot{x, y, picLerp(picLerp(sky, land, hill), 0.95-0.25*math.Pow(d/sr, 2), sun), sun})
		}
	}
	return o
})

func pictureScene(t float64, still bool, ds []sceneDot) []sceneDot {
	tt, e0 := t, 116+6*math.Sin(2*math.Pi*t/5.7)+3.5*math.Sin(2*math.Pi*t/3.3+1.3)+3*picNoise(t*0.4, 21)
	if still {
		tt, e0 = 0, 116
	}
	for _, s := range pictureDots() {
		e := e0 + 5*picNoise(s.y*0.05+tt*0.12, 22)
		w := picStep((e-(s.x-0.42*(s.y-80)))/18 + 0.5)
		ds = append(ds, sceneDot{x: s.x, y: s.y, r: picLerp(0.62, picLerp(0.45, 2.65, s.ink), w), a: picLerp(0.3, 1, w), g: 0.35 * s.ink * w, mix: s.sun * w})
	}
	return ds
}

// ── clips: a faint paragraph; one row lifted out of it toward you,
// brighter, larger, raised up and to the left (its near, left end most),
// its place keeping a faint trace like its shadow. Before it the 摘录
// icon's two amber quote marks, following a beat late.

const clipsLift = 3

type clipsParts struct {
	rows  [][]picPt
	quote [][3]float64 // a quote mark's tail: x, y (up) and how far along, 0 to 1
}

var clipsDots = sync.OnceValue(func() clipsParts {
	var p clipsParts
	for i, n := range []int{22, 21, 22, 20, 22, 21, 12} {
		var row []picPt
		for j := range n {
			row = append(row, picPt{62 + float64(j)*4.9, 40 + float64(i)*13.5})
		}
		p.rows = append(p.rows, row)
	}
	// The tail rises from the head's left and curls right, thinning as a pen lifts.
	qb := [4]picPt{{-2.2, 1.6}, {-3.2, 5.4}, {-1.4, 9.4}, {1.9, 11.2}}
	qp := make([]picPt, 81)
	for i := range qp {
		t := float64(i) / 80
		u := 1 - t
		for k := range 2 {
			qp[i][k] = u*u*u*qb[0][k] + 3*u*u*t*qb[1][k] + 3*u*t*t*qb[2][k] + t*t*t*qb[3][k]
		}
	}
	ql := picLengths(qp)
	qt := ql[len(ql)-1]
	qn := max(2, int(math.Round(qt/(4.3*0.78)))+1)
	for i := range qn {
		u := float64(i) / float64(qn-1)
		q := picAlong(qp, ql, qt*u)
		p.quote = append(p.quote, [3]float64{q[0], q[1], u})
	}
	return p
})

func clipsScene(t float64, still bool, ds []sceneDot) []sceneDot {
	p := clipsDots()
	drift := func(t float64) (x, y, lift float64) {
		if still {
			return 0, 0, 0
		}
		return 0.7 * picNoise(t*0.21, 31), 0.8*picNoise(t*0.27, 32) + 0.5*math.Sin(2*math.Pi*t/6.3), 0.45 * picNoise(t*0.17, 33)
	}
	for i, row := range p.rows {
		for _, q := range row {
			ds = append(ds, sceneDot{x: q[0], y: q[1], r: 1, a: 0.3, ghost: i == clipsLift})
		}
	}
	row := p.rows[clipsLift]
	fx, fy, ft := drift(t)
	for j, q := range row {
		u := float64(j) / float64(len(row)-1)
		ds = append(ds, sceneDot{x: q[0] + picLerp(-4.5, -2.2, u) + fx, y: q[1] + picLerp(-6.5, -3.4, u)*(1+0.25*ft) + fy,
			r: picLerp(1.75, 1.4, u), g: picLerp(0.85, 0.5, u), a: 1})
	}
	qx, qy, _ := drift(t - 0.3)
	const s = 1.4
	hy, hx := row[0][1]-11+qy, row[0][0]-19+qx
	for _, x := range [2]float64{hx - 9.2*s, hx} {
		ds = append(ds, sceneDot{x: x, y: hy, r: 1.5 * 2.05 * s, a: 1, amber: true})
		for _, q := range p.quote {
			ds = append(ds, sceneDot{x: x + q[0]*s, y: hy - q[1]*s, r: 1.5 * picLerp(1, 0.5, q[2]) * s, a: 1, amber: true})
		}
	}
	return ds
}

// ── favorites: an unprinted plate of the 收藏 page's blocks, each with
// its tab cut into its top-left corner. Crop marks at the plate's corners;
// in every block a faint even halftone screen waiting for ink, the ghost
// of its kind over it a little heavier (rows of text, a framed picture,
// an easing path, a glyph). Only the big block's tab is lit: in it the
// 文章 kind mark at its 16 px cut in dimmed amber, breathing a little, the
// count's place beside it empty. Now and then a soft light crosses
// everything and brings the ghosts up as it passes.

type favLine struct {
	x, y       float64
	notch, big bool // on the tab's two edges; of the big block
}

type favGhost struct {
	x, y, m float64 // m sizes it
	text    bool
}

type favoritesParts struct {
	lines  []favLine
	screen []picPt
	ghosts []favGhost
	crop   []picPt
	mark   [][3]float64 // x, y, r
}

var favoritesDots = sync.OnceValue(func() favoritesParts {
	var p favoritesParts
	const h = math.Pi / 2
	type block struct{ b, tab [4]float64 } // tab: its right edge and bottom
	blocks := []block{{[4]float64{20, 18, 104, 142}, [4]float64{66, 47}}, {[4]float64{112, 18, 180, 74}, [4]float64{127, 30}},
		{[4]float64{112, 82, 148, 142}, [4]float64{126, 94}}, {[4]float64{156, 82, 180, 142}, [4]float64{167, 93}}}
	for i, q := range blocks {
		x0, y0, x1, y1, tr, tb := q.b[0], q.b[1], q.b[2], q.b[3], q.tab[0], q.tab[1]
		rn, f, R := 2.5, 2.0, 5.0 // the notch's corners: convex, concave
		if i == 0 {
			rn, f = 4, 3.5
		}
		path := slices.Concat(picArc(x1-R, y0+R, R, -h, 0, 8), picArc(x1-R, y1-R, R, 0, h, 8), picArc(x0+R, y1-R, R, h, 2*h, 8),
			picArc(x0+rn, tb+rn, rn, 2*h, 3*h, 8), picArc(tr-f, tb-f, f, h, 0, 8), picArc(tr+rn, y0+rn, rn, 2*h, 3*h, 8))
		for _, d := range picEven(path, 5, true) {
			p.lines = append(p.lines, favLine{d[0], d[1], d[0] < tr+rn+0.5 && d[1] < tb+rn+0.5, i == 0})
		}
	}
	add := func(P []picPt, text bool, m float64) {
		pitch := 2.5
		if text {
			pitch = 2.9
		}
		for _, d := range picEven(P, pitch, false) {
			p.ghosts = append(p.ghosts, favGhost{d[0], d[1], m, text})
		}
	}
	// Rows of text under the tab, ragged, in two paragraphs.
	lens := []float64{70, 66, 70, 62, 38, 70, 68, 70, 64, 70, 66, 30}
	for i, y := range []float64{54, 60.5, 67, 73.5, 80, 93, 99.5, 106, 112.5, 119, 125.5, 132} {
		add([]picPt{{27, y}, {27 + lens[i], y}}, true, 1)
	}
	// A framed picture: the frame, a sun, a ridge.
	add(picRoundRect(119, 36, 173, 67, 2.5), false, 1)
	add(picArc(136, 47, 3.2, 0, 2*math.Pi, 10), false, 1.1)
	var ridge, ease, hook, bowl []picPt
	for i := range 23 {
		x := 121 + float64(i)*2.25
		ridge = append(ridge, picPt{x, 64 - 9*math.Exp(-math.Pow((x-156)/9, 2)) - 4*math.Exp(-math.Pow((x-128)/7, 2))})
	}
	add(ridge, false, 1)
	// An easing path, in and out, its start and end marked.
	for i := range 41 {
		t := float64(i) / 40
		u := 1 - t
		ease = append(ease, picPt{u*u*u*118 + 3*u*u*t*134 + 3*u*t*t*126 + t*t*t*142, u*u*u*134 + 3*u*u*t*134 + 3*u*t*t*102 + t*t*t*102})
	}
	add(ease, false, 1)
	add([]picPt{{116, 137}, {144, 137}}, false, 0.8)
	// A glyph: a lowercase a (hook, stem, bowl) on its baseline.
	glyph := func(P []picPt) []picPt {
		o := make([]picPt, len(P))
		for i, q := range P {
			o[i] = picPt{167 + q[0]*3.1, 118 - q[1]*3.1}
		}
		return o
	}
	hook = append(picArc(0.2, 1, 1.8, 161.5*math.Pi/180, 0, 12), picPt{2, 1}, picPt{2, -3})
	add(glyph(hook), false, 1)
	for i := range 21 {
		a := (70 + 220*float64(i)/20) * math.Pi / 180
		bowl = append(bowl, picPt{0.2 + 2*math.Cos(a), -1.75 + 1.25*math.Sin(a)})
	}
	add(glyph(bowl), false, 1)
	add(glyph([]picPt{{-3.2, -3}, {3.4, -3}}), false, 0.75)
	// The screen: a hex grid inside each block, clear of its outline, its tab and the ghost's own dots.
	const pitch = 3.6
	for j := 0; float64(j)*pitch*0.866 <= 160; j++ {
		for i := 0; float64(i)*pitch <= 200; i++ {
			x, y := 2+float64(i)*pitch, 2+float64(j)*pitch*0.866
			if j&1 == 1 {
				x += pitch / 2
			}
			k := slices.IndexFunc(blocks, func(q block) bool {
				return picInRoundRect(x, y, q.b[0]+3.4, q.b[1]+3.4, q.b[2]-3.4, q.b[3]-3.4, 2.5)
			})
			if k < 0 || x < blocks[k].tab[0]+3.2 && y < blocks[k].tab[1]+3.2 ||
				slices.ContainsFunc(p.ghosts, func(g favGhost) bool { return math.Hypot(g.x-x, g.y-y) < 1.9 }) {
				continue
			}
			p.screen = append(p.screen, picPt{x, y})
		}
	}
	// Crop marks: two short dotted ticks off each corner.
	for _, c := range [][4]float64{{20, 18, -1, -1}, {180, 18, 1, -1}, {20, 142, -1, 1}, {180, 142, 1, 1}} {
		x, y, sx, sy := c[0], c[1], c[2], c[3]
		p.crop = append(p.crop, picEven([]picPt{{x + sx*5, y}, {x + sx*13, y}}, 2.6, false)...)
		p.crop = append(p.crop, picEven([]picPt{{x, y + sy*5}, {x, y + sy*13}}, 2.6, false)...)
	}
	// The 文章 kind mark at its 16 px cut, the same dots as on a block's tab; here 16 px are 19 units, centred in the tab.
	const side, cx, cy = 19.0, 20 + 4.8 + 9.5, 18 + 14.5
	for _, d := range kindDots16["kind-articles"] {
		p.mark = append(p.mark, [3]float64{cx + (float64(d.x)-0.5)*side, cy + (float64(d.y)-0.5)*side, float64(d.r) * side})
	}
	return p
})

// A lightPass is light crossing a scene: from t, over d, at an angle.
type lightPass struct{ t, d, ang float64 }

type passList struct {
	passes []lightPass
	end    float64
}

var favoritesPasses = sync.OnceValue(func() passList {
	var l passList
	T := -1.2
	for i := range 40 {
		fi := float64(i)
		d := 2.6 + 0.9*picHash(fi, 81)
		l.passes = append(l.passes, lightPass{T, d, (35 + 18*(picHash(fi, 82)-0.5)) * math.Pi / 180})
		T += d + 0.7 + 0.9*picHash(fi, 83)
	}
	l.end = T
	return l
})

func favoritesScene(t float64, still bool, ds []sceneDot) []sceneDot {
	p := favoritesDots()
	// The light: one pass at a time, a soft band crossing at its angle.
	light := func(x, y float64) float64 { return 0 }
	if l := favoritesPasses(); !still {
		tw := picWrap(t, l.passes[0].t, l.end-l.passes[0].t)
		for _, q := range l.passes {
			if u := (tw - q.t) / q.d; u >= 0 && u <= 1 {
				c, s, at := math.Cos(q.ang), math.Sin(q.ang), picLerp(-150, 150, u)
				light = func(x, y float64) float64 { return math.Exp(-math.Pow(((x-100)*c+(y-80)*s-at)/20, 2)) }
				break
			}
		}
	}
	for _, d := range p.lines {
		L, lit := light(d.x, d.y), d.big && d.notch
		r, a, g := 1.15, picLerp(0.85, 1, L), 0.9*L
		switch {
		case lit:
			r, g = 1.25, g+0.55
		case d.notch:
			r, a = 0.95, picLerp(0.38, 0.7, L)
		}
		ds = append(ds, sceneDot{x: d.x, y: d.y, r: r * (1 + 0.22*L), a: a, g: g})
	}
	for _, d := range p.screen {
		L := light(d[0], d[1])
		ds = append(ds, sceneDot{x: d[0], y: d[1], r: 0.42 * (1 + 0.3*L), a: 0.2 + 0.18*L, ghost: true})
	}
	for _, d := range p.ghosts {
		L, a := light(d.x, d.y), 0.3
		if d.text {
			a = 0.22
		}
		ds = append(ds, sceneDot{x: d.x, y: d.y, r: 0.6 * d.m * (1 + 0.3*L), a: a + 0.3*L, ghost: true})
	}
	for _, d := range p.crop {
		ds = append(ds, sceneDot{x: d[0], y: d[1], r: 0.7, a: 0.55})
	}
	for i, d := range p.mark {
		L, b := light(d[0], d[1]), 0.0
		if !still {
			b = 0.08 * picNoise(t*0.45+float64(i)*1.7, 86)
		}
		ds = append(ds, sceneDot{x: d[0], y: d[1], r: d[2] * (1 + b + 0.15*L), a: picLerp(0.42, 0.62, L), amber: true})
	}
	return ds
}

// ── mailbox (an author with nothing yet): a time rail running down from
// now, the amber mark at its head, into the past; on it three empty slots,
// outlined cards with nothing in them, fainter the further back. Now and
// then a light leaves now and travels down the rail; each card catches it
// as it passes.

const mailboxX = 58

var mailboxSlots = [3]float64{52, 90, 128}

type mailboxParts struct {
	rail  []picPt
	cards [3][]picPt
}

var mailboxDots = sync.OnceValue(func() mailboxParts {
	var p mailboxParts
	for _, d := range picEven([]picPt{{mailboxX, 32}, {mailboxX, 154}}, 4.7, false) {
		if !slices.ContainsFunc(mailboxSlots[:], func(v float64) bool { return math.Abs(d[1]-v) <= 3.6 }) {
			p.rail = append(p.rail, d)
		}
	}
	for i, y := range mailboxSlots {
		p.cards[i] = picEven(picRoundRect(71, y-12, 152, y+12, 5), 4.8, true)
	}
	return p
})

var mailboxRuns = sync.OnceValue(func() passList {
	var l passList
	T := -2.0
	for i := range 40 {
		fi := float64(i)
		d := 3.8 + 1.0*picHash(fi, 91)
		l.passes = append(l.passes, lightPass{t: T, d: d})
		T += d + 0.6 + 0.8*picHash(fi, 92)
	}
	l.end = T
	return l
})

func mailboxScene(t float64, still bool, ds []sceneDot) []sceneDot {
	p := mailboxDots()
	yl, env := -99.0, 0.0
	if l := mailboxRuns(); !still {
		tw := picWrap(t, l.passes[0].t, l.end-l.passes[0].t)
		for _, q := range l.passes {
			if u := (tw - q.t) / q.d; u >= 0 && u <= 1 {
				yl, env = picLerp(24, 178, u), picStep(u/0.1)
				break
			}
		}
	}
	glow := func(y float64) float64 { return env * math.Exp(-math.Pow((y-yl)/11, 2)) }
	back := func(y float64) float64 { return picStep((y - 30) / 125) }
	for _, d := range p.rail {
		I, f := glow(d[1]), back(d[1])
		ds = append(ds, sceneDot{x: d[0], y: d[1], r: 1.15 * (1 - 0.22*f) * (1 + 0.25*I), a: picLerp(1-0.6*f, 1, 0.8*I), g: 0.9 * I})
	}
	for i, y := range mailboxSlots {
		I, f := 0.85*glow(y), back(y)
		ds = append(ds, sceneDot{x: mailboxX, y: y, r: 1.9 * (1 - 0.2*f) * (1 + 0.2*I), a: picLerp(1-0.5*f, 1, I), g: 0.9 * I})
		for _, d := range p.cards[i] {
			ds = append(ds, sceneDot{x: d[0], y: d[1], r: 1 - 0.15*f, a: picLerp(0.7-0.38*f, 1, 0.8*I), g: 0.8 * I})
		}
	}
	now := 1.0
	if !still {
		now += 0.05 * picNoise(t*0.35, 93)
	}
	return append(ds, sceneDot{x: mailboxX, y: 23, r: 3.3 * now, a: 1, amber: true})
}
