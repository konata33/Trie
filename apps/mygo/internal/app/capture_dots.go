package app

import (
	"image"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
	xdraw "golang.org/x/image/draw"

	"trie/internal/motion"
	"trie/internal/theme"
)

// The capture picture (DESIGN.md Capture 显影): a page as a fine screen of
// round dots, each the colour of its cell of the page, there from the first
// frame: small, dim and grey while the page is being taken, a slow light
// passing over them. How far the pack has come develops it: the dots grow and
// take on their colours, mostly from the top, until they touch and the page
// reads whole; its first screen then comes into focus over them. The button's
// 4 × 4 of dots is the same picture, small.

// The picture's cells: 2 px apart in the island's 72 × 45 thumbnail (and as
// it flies), 5 px on the 440 × 275 card, so it reads as the page at both sizes
// and as dots only up close.
const (
	thumbCols, thumbRows = 36, 22
	cardCols, cardRows   = 88, 55
)

// The capture icon: a 4 × 4 halftone, its dots growing from the top row,
// small and dim, to the bottom one, full and amber (DESIGN.md Icons). The dots
// are 3.5px apart, so that at 2× each sits on whole pixels and all draw alike.
func init() {
	for _, cut := range []struct {
		m    map[string][]dot
		n    int
		from float32
		step float32
		full float32
	}{{iconDots16, 4, 2.5 / 16, 3.5 / 16, 0.0625}, {iconDots12, 3, 2.5 / 12, 3.5 / 12, 0.0833}} {
		var ds []dot
		for row := range cut.n {
			k := float32(row) / float32(cut.n-1)
			for col := range cut.n {
				ds = append(ds, dot{x: cut.from + float32(col)*cut.step, y: cut.from + float32(row)*cut.step,
					r: cut.full * (0.5 + 0.5*k), a: 0.6 + 0.4*k, acc: row == cut.n-1})
			}
		}
		cut.m["capture"] = ds
	}
}

// captureDots are the button's dots as the page's pack stands: dev is how far it
// has developed, below 0 for none; lit, taken. While it develops, each row in
// turn grows to full and lights to text, the amber row brightening from
// icon-fill to amber all the way; taken, the sixteen are one size, the bottom
// row amber. ink is the button's colour.
func captureDots(dev float32, lit bool, ink ui.Color) []inkDot {
	ds := iconDots16["capture"]
	out := make([]inkDot, len(ds))
	for i, d := range ds {
		f := float32(0)
		switch {
		case lit:
			f = 1
		case dev >= 0:
			f = motion.Ease(unit(dev*4 - float32(i/4)))
		}
		c := ink.Mix(theme.Text, f)
		if d.acc {
			c = theme.IconFill.Mix(theme.Accent, map[bool]float32{true: 1, false: max(0, dev)}[lit])
		}
		out[i] = inkDot{x: d.x, y: d.y, r: d.r + (0.0625-d.r)*f, a: d.a + (1-d.a)*f, c: c}
	}
	return out
}

// dotGrid is a picture's colours, a cell each, cols × rows.
type dotGrid struct {
	cols, rows int
	c          []ui.Color
}

// dotPic is a picture's colours at the thumbnail's cells and at the card's.
type dotPic struct{ thumb, card *dotGrid }

// at is the grid for the card's size, else the thumbnail's; nil for none.
func (d *dotPic) at(card bool) *dotGrid {
	switch {
	case d == nil:
		return nil
	case card:
		return d.card
	}
	return d.thumb
}

// gridOf is img's colours as the 16:10 window onto its top shows them, at both
// sizes; nil for no picture.
func gridOf(img image.Image) *dotPic {
	if img == nil {
		return nil
	}
	src := topWindow(img.Bounds())
	if src.Empty() {
		return nil
	}
	return &dotPic{thumb: sampleGrid(img, src, thumbCols, thumbRows), card: sampleGrid(img, src, cardCols, cardRows)}
}

// sampleGrid is img's colours in cols × rows square cells across the window src,
// centred in its height as the dots are, each cell's the average of 4 × 4
// points over it.
func sampleGrid(img image.Image, src image.Rectangle, cols, rows int) *dotGrid {
	g := &dotGrid{cols: cols, rows: rows, c: make([]ui.Color, cols*rows)}
	const n = 4
	cell := float64(src.Dx()) / float64(cols)
	top := float64(src.Min.Y) + (float64(src.Dy())-cell*float64(rows))/2
	b := img.Bounds()
	for i := range g.c {
		col, row := i%cols, i/cols
		var r, gr, bl float64
		for sy := range n {
			for sx := range n {
				x := min(b.Max.X-1, src.Min.X+int((float64(col)+(float64(sx)+0.5)/n)*cell))
				y := min(b.Max.Y-1, max(b.Min.Y, int(top+(float64(row)+(float64(sy)+0.5)/n)*cell)))
				cr, cg, cb, _ := img.At(x, y).RGBA()
				r, gr, bl = r+float64(cr), gr+float64(cg), bl+float64(cb)
			}
		}
		k := float64(n*n) * 257
		g.c[i] = ui.Color{R: uint8(r / k), G: uint8(gr / k), B: uint8(bl / k), A: 255}
	}
	return g
}

// topWindow is the 16:10 window onto the top of a picture of bounds b: its
// whole width, or the middle of a wide one.
func topWindow(b image.Rectangle) image.Rectangle {
	w, h := b.Dx(), b.Dy()
	cw, ch := w, w*10/16
	if ch > h {
		cw, ch = h*16/10, h
	}
	x := b.Min.X + (w-cw)/2
	return image.Rect(x, b.Min.Y, x+cw, b.Min.Y+ch)
}

// shotOf is img's 16:10 window at width w, for the island and the card.
func shotOf(img image.Image, w int) *ui.Bitmap {
	if top := screenTop(img, w); top != nil {
		return ui.NewBitmap(top)
	}
	return nil
}

// screenTop is the 16:10 window onto img's top, w wide; nil for no picture.
func screenTop(img image.Image, w int) image.Image {
	if img == nil {
		return nil
	}
	src := topWindow(img.Bounds())
	if src.Empty() {
		return nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, w*10/16))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, src, xdraw.Src, nil)
	return dst
}

// developing is how the picture shows in a frame.
type developing struct {
	// from is the picture the app has of the page (nil: the ground's colour), to
	// the first screen once taken; baseIn is how far the dots have gone from
	// the ground to from's colours as it came, recolour from from's to to's.
	from, to         *dotPic
	ground           ui.Color
	baseIn, recolour float32
	p                float32 // how far it has developed, 0 to 1
	shown            float32 // the dots' opacity
	// failed is when the pack failed and its dots let go; zero while it
	// has not.
	failed time.Time
	still  bool // less motion: the dots do not grow, wave or fall; their colours fade
}

// The picture's timing within p: a dot develops over dotWin of it, from a
// moment of its own, its row's share dotRows and the rest its own; so the
// page comes in as a soft wave going down rather than dot by dot.
const (
	dotWin  = 0.32
	dotRows = 0.7
)

// A dot as a share of the cells' pitch, still to come and developed: small,
// dim, a grey of its own lightness keeping a little of its colour (a link's
// amber, a picture's hue) and drawn toward the ground; then large enough to
// touch its neighbours, in its own colour.
const (
	latentR, fullR = 0.36, 0.62
	latentA        = 0.85
	latentInk      = 0.7
	latentHue      = 0.35
)

// hash01 is a steady value from 0 to 1 for dot i, one per salt.
func hash01(i, salt uint32) float32 {
	x := i*0x9E3779B1 ^ salt*0x85EBCA77
	x ^= x >> 15
	x *= 0x2C1B3C6D
	x ^= x >> 12
	x *= 0x297A2D39
	x ^= x >> 15
	return float32(x>>8) / (1 << 24)
}

// dotAt is when dot i of a cols × rows picture starts to develop, as p:
// mostly from the top, a little at random.
func dotAt(i, cols, rows int) float32 {
	row := float32(i/cols) / float32(max(1, rows-1))
	return (1 - dotWin) * (dotRows*row + (1-dotRows)*hash01(uint32(i), 1))
}

// paintDeveloping paints d in r: the card's cells when r is card-sized, else
// the thumbnail's, their pitch r.W across, centred on device pixels so the
// dots stay round and crisp. Every dot is there from the start; those still to
// develop wait small and dim, a slow light passing over them from the top left
// (CaptureWave). It asks for frames while anything moves.
func paintDeveloping(p *ui.Painter, r ui.Rect, d developing) {
	card := r.W > 2*thumbW
	from, to := d.from.at(card), d.to.at(card)
	cols, rows, corner := thumbCols, thumbRows, 6*r.W/thumbW
	if card {
		cols, rows, corner = cardCols, cardRows, 12
	}
	pitch := r.W / float32(cols)
	s := p.Scale()
	snap := func(v float32) float32 { return float32(math.Round(float64(v*s))) / s }
	ox, oy := snap(r.X), snap(r.Y+(r.H-float32(rows)*pitch)/2)
	now := p.Now()
	wave := float64(now.UnixNano()%int64(time.Hour)) / float64(motion.CaptureWave)
	n := cols * rows
	ease := make([]float32, n)
	for i := range n {
		ease[i] = motion.Ease(unit((d.p - dotAt(i, cols, rows)) / dotWin))
	}
	var fell time.Duration
	if !d.failed.IsZero() {
		fell = max(time.Nanosecond, now.Sub(d.failed))
	}
	// A failed pack's developed dots fall, each to the top of those below it in
	// its column, so that they settle in a heap on the frame's floor; the rest go out.
	var rest []int
	if fell > 0 {
		rest = make([]int, n)
		for col := range cols {
			k := 0
			for row := rows - 1; row >= 0; row-- {
				if i := row*cols + col; ease[i] >= 0.5 {
					rest[i] = rows - 1 - k
					k++
				}
			}
		}
	}
	desat := float32(0)
	if fell > 0 {
		desat = motion.Ease(unit(float32(fell) / float32(motion.CaptureGrey)))
	}
	moving := fell > 0 && fell < motion.CaptureFall+motion.CaptureFallStagger
	for i := range n {
		col, row := i%cols, i/cols
		x := ox + (float32(col)+0.5)*pitch
		y := oy + (float32(row)+0.5)*pitch
		e := ease[i]
		full := d.ground
		if from != nil {
			full = full.Mix(from.c[i], d.baseIn)
		}
		if to != nil {
			full = full.Mix(to.c[i], d.recolour)
		}
		c := theme.Window.Mix(grayOf(full).Mix(full, latentHue*(1-desat)), latentInk).Mix(full, e*(1-desat))
		rad := pitch * (latentR + (fullR-latentR)*e)
		alpha := latentA + (1-latentA)*e
		switch {
		case d.still:
			rad = 0.5 * pitch
		case e < 1 && fell == 0:
			// The light passing over what is still to come, a narrow band: brighter
			// and a little larger where it is; the rest waits as it is.
			ph := 0.55*float64(col)/float64(cols) + 0.45*float64(row)/float64(rows) + 0.08*float64(hash01(uint32(i), 3))
			b := float32(math.Pow(0.5-0.5*math.Cos(2*math.Pi*(wave-ph)), 4))
			alpha = min(1, alpha*(1+0.35*(1-e)*b))
			rad *= 1 + 0.25*(1-e)*b
			moving = true
		}
		if fell > 0 {
			if e < 0.5 {
				alpha *= 1 - desat
			} else if !d.still {
				delay := time.Duration(hash01(uint32(i), 4) * float32(motion.CaptureFallStagger))
				f := motion.TravelEase(float32(fell-delay) / float32(motion.CaptureFall))
				y += float32(rest[i]-row) * pitch * f
				rad += (0.5*pitch - rad) * f
			}
			alpha *= 1 - 0.45*desat
		}
		if alpha < 0.01 {
			continue
		}
		// In the frame's rounded corners a dot keeps inside the curve, round.
		if room := cornerRoom(x-r.X, y-r.Y, r.W, r.H, corner); room < rad {
			if room < 0.3*pitch {
				continue
			}
			rad = room
		}
		p.Fill(ui.Rect{X: x - rad, Y: y - rad, W: 2 * rad, H: 2 * rad}, c.Alpha(alpha*d.shown), rad)
	}
	if moving {
		p.AnimationFrame()
	}
}

// cornerRoom is how large a dot at (x, y) in a w × h frame with corners of
// radius k may be and stay inside them; large away from the corners.
func cornerRoom(x, y, w, h, k float32) float32 {
	cx, cy := min(max(x, k), w-k), min(max(y, k), h-k)
	if cx == x || cy == y {
		return w
	}
	return k - float32(math.Hypot(float64(x-cx), float64(y-cy)))
}

// grayOf is c in grey of its own lightness.
func grayOf(c ui.Color) ui.Color {
	l := uint8(0.2126*float32(c.R) + 0.7152*float32(c.G) + 0.0722*float32(c.B) + 0.5)
	return ui.Color{R: l, G: l, B: l, A: c.A}
}

// paintSweep paints the one light passing over a page, k of the way along
// angle: 125 from its top left to its bottom right, as a page just taken;
// 305 back from the bottom right, from the copy button, as a page copied.
func paintSweep(p *ui.Painter, r ui.Rect, k, radius, angle float32) {
	if k <= 0 || k >= 1 {
		return
	}
	// It comes in and goes out at the corners, brightest midway.
	at := motion.Ease(k)
	light := theme.CaptureSweep.Alpha(float32(math.Sin(math.Pi * float64(k))))
	p.FillGradient(r, ui.LinearGradient{Angle: angle, Stops: []ui.GradientStop{
		{Color: ui.Transparent, At: max(0, at-0.24)}, {Color: light, At: at}, {Color: ui.Transparent, At: min(1, at+0.24)}}}, radius)
}

// The folder icon, for 在访达中显示: a folder's outline with its tab, and one
// amber dot inside, the pack, found (DESIGN.md Icons). Dots fall on every
// corner and evenly between, at the set's spacing for each cut.
func init() {
	outline := [][2]float32{{0.125, 0.8125}, {0.125, 0.1875}, {0.3333, 0.1875}, {0.4583, 0.3125}, {0.875, 0.3125}, {0.875, 0.8125}, {0.125, 0.8125}}
	for _, cut := range []struct {
		m       map[string][]dot
		step, r float32
	}{{iconDots16, 0.2083, 0.0625}, {iconDots12, 0.2778, 0.0833}} {
		var ds []dot
		for i := 0; i+1 < len(outline); i++ {
			p, q := outline[i], outline[i+1]
			n := max(1, int(math.Round(math.Hypot(float64(q[0]-p[0]), float64(q[1]-p[1]))/float64(cut.step))))
			for j := range n { // the segment's end is the next one's start
				u := float32(j) / float32(n)
				ds = append(ds, dot{x: p[0] + (q[0]-p[0])*u, y: p[1] + (q[1]-p[1])*u, r: cut.r, a: 1})
			}
		}
		cut.m["folder"] = append(ds, dot{x: 0.5, y: 0.5625, r: cut.r * 1.15, a: 1, acc: true})
	}
}
