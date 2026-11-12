package app

import (
	"math"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/motion"
	"trie/internal/theme"
)

// The reader's two-finger swipe back, read as Explore reads
// its rooms: the fingers moving right draw a round back mark in from the
// content area's left edge and hold the page a little; past swipeArm the
// mark arms, and letting go there, or flicking, goes back; anything shorter
// settles back. A vertical scroll, and a swipe that starts on something
// that can still scroll sideways that way (a code block, a wide table), are
// left alone. Under less motion nothing moves: the
// mark only fades in where it rests, and arms.

const (
	swipeArm   = 100                    // the pull, in DIPs, that arms the mark
	swipeFlick = 600                    // DIPs a second: the fingers flicked
	swipeLift  = 120 * time.Millisecond // this long without a move, the fingers have let go
)

// backSwipe is a reader's swipe: how far the fingers pulled, how fast over
// the last 100 ms, and what they ask for.
type backSwipe struct {
	x, speed float32
	last     time.Time
	axis     byte
	done     bool
	moves    []swipeMove
	pulling  bool      // the fingers hold the mark and the page; else they settle back
	shown    bool      // the mark shows, or settles back
	release  time.Time // the fingers count as lifted then: it goes back or settles
	ask      bool      // go back, in the next build
}

// swipeWheel takes a scroll over the reader, whose box starts at at in the
// window, and reports whether it is the swipe's.
func (a *App) swipeWheel(ev ui.InputEvent, rp *readerPage, at ui.Point) bool {
	if ev.Kind != ui.InputScroll {
		return false
	}
	s := &rp.swipe
	now := ev.Time
	if now.Sub(s.last) > scrollGap {
		s.x, s.axis, s.done, s.moves = 0, 0, false, s.moves[:0]
	}
	s.last = now
	if s.axis == 0 {
		s.axis = 'y'
		if abs32(ev.DX) > abs32(ev.DY) {
			s.axis = 'x'
		}
		aside := rp.r != nil && rp.r.ScrollsAside(at.X+ev.X, at.Y+ev.Y, ev.DX)
		if s.axis == 'y' || a.layered(rp) || aside {
			s.done = true
		} else {
			a.kbd = false // a swipe is the pointer's: the way back takes its crossing
		}
	}
	if s.done {
		return false
	}
	// Fingers moving right scroll the content left: DX below 0 pulls back.
	s.x -= ev.DX
	s.moves = append(s.moves, swipeMove{now, -ev.DX})
	s.moves = slices.DeleteFunc(s.moves, func(m swipeMove) bool { return now.Sub(m.at) >= 100*time.Millisecond })
	span := max(16*time.Millisecond, now.Sub(s.moves[0].at))
	var sum float32
	for _, m := range s.moves {
		sum += m.dx
	}
	s.speed = sum / float32(span.Seconds())
	s.pulling = true
	// Past the arm and still moving fast, the fingers are going back:
	// waiting for their momentum to die out would only hold it up.
	if s.x >= swipeArm && s.speed > swipeFlick {
		s.ask, s.done = true, true
		return true
	}
	s.release = now.Add(swipeLift)
	return true
}

// layered reports whether something is open over the reader, whose own
// scrolling the swipe leaves alone: a dialog, the sheet, the picture
// viewer or the reading options.
func (a *App) layered(rp *readerPage) bool {
	return rp.aa || a.viewer.open || a.layers.dialog != nil || a.layers.sheet
}

// swipeView makes sw, the box holding the reader's page, the swipe's: it
// takes the scrolls over it (live), decides what the fingers asked for as
// they let go, and holds the page as far as they pulled.
func (a *App) swipeView(c *ui.Context, sw ui.Element, rp *readerPage, live bool) {
	s := &rp.swipe
	if live {
		box := sw.LayoutBounds()
		at := ui.Point{X: box.X, Y: box.Y}
		sw.HandleInput(func(ev ui.InputEvent) bool { return a.swipeWheel(ev, rp, at) })
		now := c.Now()
		switch {
		case s.ask:
			s.ask = false
			a.swipeBack(c, rp)
		case s.release.IsZero():
		case now.Before(s.release):
			c.After(s.release.Sub(now))
		default:
			s.release = time.Time{}
			if s.x >= swipeArm || s.speed > swipeFlick && s.x > 24 {
				a.swipeBack(c, rp)
			} else {
				s.pulling = false
			}
		}
	}
	// The page goes up to 24 with the fingers, less and less the further
	// they pull.
	target := float32(0)
	if s.pulling && !a.still(c) {
		target = 24 * float32(1-math.Exp(-float64(max(0, s.x))/120))
	}
	var shift float32
	if s.pulling {
		shift = sw.Jump("shift", target)
	} else {
		shift = sw.AnimateTo("shift", target, ui.Animation{Duration: motion.SwipeSettle, Ease: motion.Ease})
	}
	if shift != 0 {
		sw.Translate(shift, 0)
	}
}

// swipeBack goes back as the fingers asked, holding the rest of the swipe
// from the page it lands on; with nowhere to go back to, it settles.
func (a *App) swipeBack(c *ui.Context, rp *readerPage) {
	if len(a.from) == 0 || a.next != nil {
		rp.swipe.pulling = false
		return
	}
	a.kbd = false
	a.back(c)
	a.held = c.Now()
}

// swipeMark is the round back mark the fingers draw in from the content
// area's left edge: 40 across, 16 in from the edge
// once it is all in, at the middle of the area's height, faded in and
// brought in by the pull; armed, it lights and steps 6 further in. Letting
// go short of the arm, it goes back out.
func (a *App) swipeMark(c *ui.Context, rp *readerPage) {
	s := &rp.swipe
	if !s.pulling && !s.shown {
		return
	}
	still := a.still(c)
	pull := float32(0)
	if s.pulling {
		pull = max(0, s.x)
	}
	t := min(1, pull/swipeArm)
	out := float32(-60) // where it waits, out of sight past the edge
	if still {
		out = 0
	}
	mark := ui.Box(c.Key("swipe-mark")).Absolute().Left(16).Top(a.area.H/2-20).Size(40, 40).Radius(20).ZIndex(5).Center().
		IgnoreInput(true).AccessHidden(true)
	settle := ui.Animation{Duration: motion.SwipeSettle, Ease: motion.Ease}
	var op, x float32
	if s.pulling {
		in := out + 54*(1-(1-t)*(1-t))
		if still {
			in = 0
		}
		op, x = mark.Jump("o", min(1, t*1.4)), mark.Jump("x", in)
	} else {
		fade := settle
		fade.NonMotion = true
		op, x = mark.AnimateTo("o", 0, fade), mark.AnimateTo("x", out, settle)
	}
	s.shown = s.pulling || !mark.Settled("o")
	armed := s.pulling && s.x >= swipeArm
	arm := ui.Animation{Duration: motion.SwipeSettle, Ease: motion.Ease}
	k := mark.AnimateTo("arm", map[bool]float32{true: 1, false: 0}[armed], arm)
	bg, edge, ink := theme.Elevated, theme.Line, theme.Text2
	if armed {
		bg, edge, ink = theme.SwipeArmed, theme.SwipeArmedEdge, theme.Text
	}
	if still {
		k = 0
	}
	mark.Translate(x+6*k, 0).Opacity(op).Background(mark.AnimateColor("bg", bg, arm)).
		Border(1, mark.AnimateColor("edge", edge, arm)).TextColor(mark.AnimateColor("ink", ink, arm)).
		Shadow(0, 0, 0, 1, theme.ShadowEdge).Shadow(0, 12, 32, 0, theme.ShadowFloat)
	mark.Children(func() { a.icon(c, "back", 20, false) })
}

// holdSwipe takes the momentum macOS keeps sending after the fingers of a
// swipe that went back lift, until it dies out (160 ms without a move), so
// that the page it lands on does not take it, as Explore would drag its
// rooms; the pointer's other input waits as long.
func (a *App) holdSwipe(c *ui.Context) {
	if a.held.IsZero() {
		return
	}
	left := scrollGap - c.Now().Sub(a.held)
	if left <= 0 {
		a.held = time.Time{}
		return
	}
	c.After(left)
	ui.Box(c.Key("swipe-hold")).Absolute().Left(0).Top(0).Right(0).Bottom(0).AccessHidden(true).
		HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputScroll || ev.Time.Sub(a.held) > scrollGap {
				return false
			}
			a.held = ev.Time
			return true
		})
}
