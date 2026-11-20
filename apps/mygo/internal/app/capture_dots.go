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

// The capture picture (DESIGN.md Capture 显影): a page as a grid of round
// dots, 3px a cell in the island's 72 × 45 thumbnail, each the colour of its
// cell of the page. How far the pack has come develops it: the dots come in
// layer by layer, mostly from the top, grow, and take on their colours. The
// button's 4 × 4 of dots is the same picture, small.

// packCols and packRows are the picture's cells, the island thumbnail's 72 × 45
// in 3px cells; it keeps them at every size.
const packCols, packRows = 24, 15

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

// dotGrid is a picture's colours, a cell each.
type dotGrid [packCols * packRows]ui.Color

// gridOf is img's colours as the 16:10 window onto its top shows them, each
// cell's the average of 8 × 8 points over it; nil for no picture.
func gridOf(img image.Image) *dotGrid {
	if img == nil {
		return nil
	}
	src := topWindow(img.Bounds())
	if src.Empty() {
		return nil
	}
	var g dotGrid
	const n = 8
	for i := range g {
		col, row := i%packCols, i/packCols
		var r, gr, b float64
		for sy := range n {
			for sx := range n {
				x := src.Min.X + (col*n+sx)*src.Dx()/(packCols*n)
				y := src.Min.Y + (row*n+sy)*src.Dy()/(packRows*n)
				cr, cg, cb, _ := img.At(x, y).RGBA()
				r, gr, b = r+float64(cr), gr+float64(cg), b+float64(cb)
			}
		}
		k := float64(n*n) * 257
		g[i] = ui.Color{R: uint8(r / k), G: uint8(gr / k), B: uint8(b / k), A: 255}
	}
	return &g
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
	// from is the picture the app has of the page (nil: neutral grey), to
	// the first screen once taken; recolour is how far the dots have gone
	// from the one's colours to the other's.
	from, to *dotGrid
	recolour float32
	p        float32 // how far it has developed, 0 to 1
	shown    float32 // the dots' opacity: they fade as the first screen rises out of them
	// failed is when the pack failed and its dots let go; zero while it
	// has not.
	failed time.Time
	still  bool // less motion: the dots do not grow or fall, their colours fade
}

// The picture's timing within p: a dot comes in over dotIn of it, at a
// moment of its own, its row's share dotRows and the rest its own.
const (
	dotIn   = 0.14
	dotRows = 0.62
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

// dotAt is when dot i comes in, as p: mostly from the top, a little at
// random, so the picture fills in layer by layer rather than as a line.
func dotAt(i int) float32 {
	row := float32(i/packCols) / (packRows - 1)
	return (1 - dotIn) * (dotRows*row + (1-dotRows)*hash01(uint32(i), 1))
}

// paintDeveloping paints d in r, its cells r.W/packCols across, centred on
// device pixels so the dots stay round and crisp. Where none has come yet, a
// faint dot breathes, each on its own 2–4 s and from its own moment. It asks
// for frames while anything moves.
func paintDeveloping(p *ui.Painter, r ui.Rect, d developing) {
	s := p.Scale()
	snap := func(v float32) float32 { return float32(math.Round(float64(v*s))) / s }
	cell := r.W / packCols
	ox, oy := snap(r.X), snap(r.Y)
	now := p.Now()
	secs := float64(now.UnixNano()%int64(time.Hour)) / 1e9
	var fell time.Duration
	if !d.failed.IsZero() {
		fell = max(time.Nanosecond, now.Sub(d.failed))
	}
	// A failed pack's dots fall, each to the top of those below it in its
	// column, so that they settle in a heap on the frame's floor.
	var rest [packCols * packRows]int
	if fell > 0 {
		for col := range packCols {
			k := 0
			for row := packRows - 1; row >= 0; row-- {
				i := row*packCols + col
				if d.p > dotAt(i) {
					rest[i] = packRows - 1 - k
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
	for i := range packCols * packRows {
		col, row := i%packCols, i/packCols
		x := ox + (float32(col)+0.5)*cell
		y := oy + (float32(row)+0.5)*cell
		at := dotAt(i)
		in := motion.Ease(unit((d.p - at) / dotIn))
		grow := unit((d.p - at) / (1 - at))
		// Not come yet: a faint dot, breathing.
		if faint := (1 - in) * d.shown; faint > 0.01 {
			breath := float32(0.5)
			if !d.still {
				period := (motion.CaptureBreathMin + time.Duration(hash01(uint32(i), 2)*float32(motion.CaptureBreathMax-motion.CaptureBreathMin))).Seconds()
				breath = 0.5 - 0.5*float32(math.Cos(2*math.Pi*(secs/period+float64(hash01(uint32(i), 3)))))
				moving = true
			}
			if fell > 0 {
				faint *= 1 - desat
			}
			rad := 0.22 * cell
			p.Fill(ui.Rect{X: x - rad, Y: y - rad, W: 2 * rad, H: 2 * rad}, theme.CaptureFaint.Alpha(faint*(0.35+0.65*breath)), rad)
		}
		if in <= 0.001 {
			continue
		}
		c := theme.CaptureDot
		if d.from != nil {
			c = d.from[i]
		}
		if d.to != nil {
			c = c.Mix(d.to[i], d.recolour)
		}
		grey := grayOf(c)
		// The colour comes slowly: grey of its own lightness at first, its own once grown.
		c = grey.Mix(c, (0.2+0.8*grow)*(1-desat))
		rad := cell * (0.35*in + 0.15*grow)
		alpha := in
		if d.still {
			rad = 0.42 * cell
		}
		if fell > 0 {
			delay := time.Duration(hash01(uint32(i), 4) * float32(motion.CaptureFallStagger))
			f := motion.TravelEase(float32(fell-delay) / float32(motion.CaptureFall))
			if d.still {
				f = 0
			}
			y += float32(rest[i]-row) * cell * f
			rad = rad + (0.36*cell-rad)*f
			alpha *= 1 - 0.45*desat
		}
		// In the frame's rounded corners a dot keeps inside the curve, round.
		if room := cornerRoom(x-ox, y-oy, r.W, r.H, 2*cell); room < rad {
			if room < 0.3*cell {
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
