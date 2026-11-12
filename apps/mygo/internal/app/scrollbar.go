package app

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/motion"
	"trie/internal/theme"
)

// The app's scrollbar (DESIGN.md Scrollbar): not there
// while you look; scrolling brings up a scale at the content area's right
// edge standing for the whole page, divided as a ruler is, with a needle
// where you are and a lamp on it that lights the marks near it and finer
// divisions between them. Pointed at, it comes up a step and takes the
// pointer: a press glides there, the needle drags.
const (
	rulerTop, rulerBottom = 48, 10
	rulerWide, rulerEdge  = 24, 6
)

var (
	markLength = [4]float32{11, 8, 6, 4}
	markDim    = [4]float32{0.34, 0.27, 0.22, 0.17}
	markLit    = [4]float32{0.82, 0.72, 0.64, 0.56}
)

type rulerState struct {
	lastY    float32
	wokeAt   time.Time // the last scroll
	upAt     time.Time // when it came up
	downAt   time.Time // when it began to go
	up       bool
	held     bool
	grabY    float32 // where a drag began, and the scroll then
	grabTop  float32
	scroller *ui.ScrollState
}

func rank(k, n int) int {
	switch {
	case k%(n/2) == 0:
		return 0
	case k%(n/4) == 0:
		return 1
	case k%(n/8) == 0:
		return 2
	}
	return 3
}

// lampLight is the lamp's light at distance d, a cosine to nothing at reach.
func lampLight(d, reach float32) float32 {
	if d >= reach {
		return 0
	}
	return float32((1 + math.Cos(math.Pi*float64(d/reach))) / 2)
}

// scrollbar draws the live page's scale over the content area's right edge.
func (a *App) scrollbar(c *ui.Context) {
	s := a.scrolling
	r := &a.ruler
	if s == nil || s.MaxY <= 1 || a.area.H <= 0 {
		r.scroller = s
		return
	}
	now := c.Now()
	if r.scroller != s {
		r.scroller, r.lastY = s, s.Y
	}
	if s.Y != r.lastY {
		r.lastY, r.wokeAt = s.Y, now
	}
	view := a.area.H
	span := view - rulerTop - rulerBottom
	strip := ui.Box(c.Key("ruler")).Absolute().Top(0).Right(0).Width(rulerWide).Height(view).ZIndex(6).AccessHidden(true)
	_, hy, over := strip.PointerPosition()
	hoverY := ui.Point{Y: hy}
	near := over || r.held
	awake := !r.wokeAt.IsZero() && now.Sub(r.wokeAt) < motion.RulerNap
	up := awake || near
	if up != r.up {
		r.up = up
		if up {
			r.upAt = now
		} else {
			r.downAt = now
		}
	}
	if awake {
		c.After(motion.RulerNap - now.Sub(r.wokeAt))
	}
	at := s.Y / s.MaxY
	y := rulerTop + span*at
	// A press on the scale glides there; the needle drags.
	if strip.Pressed() {
		if !r.held {
			r.held, r.grabY, r.grabTop = true, hoverY.Y, s.Y
			if math.Abs(float64(hoverY.Y-y)) > 12 {
				to := max(0, min(1, (hoverY.Y-rulerTop)/span)) * s.MaxY
				if a.still(c) {
					s.Y = to
				} else {
					s.ScrollTo(0, to, ui.Smooth)
				}
				r.held = false
			}
		} else {
			s.Y = max(0, min(s.MaxY, r.grabTop+(hoverY.Y-r.grabY)*s.MaxY/span))
		}
	} else if r.held {
		r.held, r.wokeAt = false, now
	}
	reach, lift := float32(52), float32(1)
	if near {
		reach, lift = 68, 1.35
	}
	if r.held {
		reach = 88
	}
	n := 16
	if span/32 >= 16 {
		n = 32
	}
	// The light spreads from the needle as it comes up (the far ends up to
	// 140 ms later) and draws back into it as it goes.
	fade := func(d float32) float32 {
		delay := min(motion.RulerSpread, time.Duration(d*0.3)*time.Millisecond)
		if r.up {
			t := now.Sub(r.upAt) - delay
			return max(0, min(1, float32(t)/float32(motion.Hover)))
		}
		if r.downAt.IsZero() {
			return 0
		}
		t := now.Sub(r.downAt) - max(0, motion.RulerSpread-time.Duration(d*0.3)*time.Millisecond)
		return 1 - max(0, min(1, float32(t)/float32(motion.RulerOut)))
	}
	moving := r.up && now.Sub(r.upAt) < motion.Hover+motion.RulerSpread || !r.up && !r.downAt.IsZero() && now.Sub(r.downAt) < motion.RulerOut+2*motion.RulerSpread
	if moving {
		c.AnimationFrame()
	}
	if !r.up && !moving {
		return
	}
	pointing := over && !r.held && math.Abs(float64(hoverY.Y-y)) > 12 && hoverY.Y >= rulerTop && hoverY.Y <= rulerTop+span
	strip.Draw(func(pt *ui.Painter, box ui.Rect) {
		hair := 1 / pt.Scale()
		snap := func(v float32) float32 { return float32(math.Round(float64(v*pt.Scale()))) / pt.Scale() }
		right := box.X + box.W - rulerEdge
		for k := 0; k <= n; k++ {
			at := rulerTop + span*float32(k)/float32(n)
			d := float32(math.Abs(float64(at - y)))
			rk := rank(k, n)
			white := markDim[rk]*lift + (markLit[rk]-markDim[rk]*lift)*lampLight(d, reach)
			o := fade(d)
			pt.Fill(ui.Rect{X: right - markLength[rk], Y: box.Y + snap(at), W: markLength[rk], H: hair}, theme.RulerInk.Alpha(white*o), 0)
			// The loupe: quarter divisions, only where the lamp falls.
			for j := 1; k < n && j < 4; j++ {
				sub := rulerTop + span*(float32(k)+float32(j)/4)/float32(n)
				if light := lampLight(float32(math.Abs(float64(sub-y))), reach); light > 0.03 {
					w := float32(2)
					if j == 2 {
						w = 3
					}
					pt.Fill(ui.Rect{X: right - w, Y: box.Y + snap(sub), W: w, H: hair}, theme.RulerInk.Alpha(0.5*light*o), 0)
				}
			}
		}
		if pointing {
			pt.FillGradient(ui.Rect{X: right + 3 - 20, Y: box.Y + snap(hoverY.Y), W: 20, H: hair}, needleTaper(theme.RulerInk.Alpha(0.3*fade(0))), 0)
		}
		needle, w := theme.Needle, float32(20)
		if near {
			needle, w = theme.NeedleNear, 22
		}
		if r.held {
			needle = theme.RulerInk
		}
		pt.FillGradient(ui.Rect{X: right + 3 - w, Y: box.Y + snap(y), W: w, H: snap(1)}, needleTaper(needle.Alpha(float32(needle.A)/255*fade(0))), 0)
	})
}

// needleTaper fades a line in from its left end over 6 (mask-image).
func needleTaper(c ui.Color) ui.LinearGradient {
	return ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{{Color: c.Alpha(0), At: 0}, {Color: c, At: 0.3}, {Color: c, At: 1}}}
}
