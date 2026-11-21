// Package motion is Trie's motion in one place: every duration and
// curve the app animates with. docs/DESIGN.md Motion is the source of
// truth: change a value there first, then here. Nothing else in the app
// writes a duration, an easing or a spring.
package motion

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/theme"
)

// Ease is the one easing: Material 3's standard curve, a smooth
// deceleration.
var Ease = ui.CubicBezier(0.2, 0, 0, 1)

// The ladder (DESIGN.md Motion 时长): small things
// 150–200 ms, medium 250–300, large 400–450; a way out takes about 0.6 of
// the way in.
const (
	Hover = 150 * time.Millisecond // pointer hover: background and colour
	Press = 120 * time.Millisecond // the press's scale and its release
	State = 250 * time.Millisecond // a control changing state, a selection sliding

	TipIn, TipOut       = 200 * time.Millisecond, 120 * time.Millisecond
	MenuIn, MenuOut     = 150 * time.Millisecond, 100 * time.Millisecond // opened tens of times a day: a rung quicker
	PopIn, PopOut       = 250 * time.Millisecond, 150 * time.Millisecond // popovers and the shortcut sheet
	ToastIn, ToastOut   = 300 * time.Millisecond, 180 * time.Millisecond
	DialogIn, DialogOut = 400 * time.Millisecond, 240 * time.Millisecond
	TravelIn, TravelOut = 450 * time.Millisecond, 280 * time.Millisecond // the image viewer's picture
	SwapOut, SwapIn     = 150 * time.Millisecond, 250 * time.Millisecond // content swapping in place

	Breathe = 1400 * time.Millisecond // a placeholder breathing while its content loads
)

// How far a press shrinks a picture it presses (DESIGN.md 按下就有反应).
const Pressed = 0.97

// RiseIn brings up what comes in from below, a notice or a passage put
// back; Land closes a gap, the rest of a list moving up into the room of
// what left. Neither goes past its place (DESIGN.md 出现和补位).
var (
	RiseIn = ui.Animation{Spring: ui.SpringOptions{Duration: 400 * time.Millisecond}}
	Land   = ui.Animation{Spring: ui.SpringOptions{Duration: 450 * time.Millisecond}}
)

// Satellite is the favourite's satellite coming round to the top, 60 ms
// behind the arc's end, without going past it (DESIGN.md 收藏轨道).
var Satellite = ui.Animation{Spring: ui.SpringOptions{Duration: 500 * time.Millisecond}, Delay: 60 * time.Millisecond}

// Carry is the light that is one thing going from item to item: a sidebar
// tab's ground, words and amber dots, a list's selected ground, the amber
// line under a work's shot (DESIGN.md 带着走的弹簧: duration 0.5, bounce 0).
var Carry = Travel(500 * time.Millisecond)

// A press on an icon button sends a ring of light out from where it went
// down, RingSpeed px a second, gone over Ring (DESIGN.md 按下).
// RingFade is how its light falls off as it goes, e^(−t/RingFade).
const (
	Ring      = 900 * time.Millisecond
	RingFade  = 320 * time.Millisecond
	RingSpeed = 70
)

// Special curves, named so no one writes their own.
var (
	// Flight is a long flight across the window, as an avatar into its
	// new sidebar row: it leaves gently, carries through, long damped tail.
	Flight = ui.CubicBezier(0.45, 0, 0.15, 1)
	// Brake is an exact quadratic ease-out: it starts at the speed
	// something already has and stops at zero, as a spinning icon.
	Brake = ui.CubicBezier(0.333, 0.667, 0.667, 1)
)

// DrawIn is how far something on a flight (on Flight) has drawn in to its
// size at the end, k of the way: most of it early, so it is small before it
// passes what lies near where it lands, as the capture island going into
// 收藏's tab.
func DrawIn(k float32) float32 { return 1 - float32(math.Pow(float64(1-k), 1.6)) }

// CSS's ease-out and ease-in-out keywords, which the ground lights up and
// melts along.
var (
	cssEaseOut   = ui.CubicBezier(0, 0, 0.58, 1)
	cssEaseInOut = ui.CubicBezier(0.42, 0, 0.58, 1)
)

// travelRate is the travel spring's rate: a critically damped spring,
// 1 − (1 + 7.4t)·e^(−7.4t) over the travel's length, which gives the 25
// travelStops within 5e-5.
const travelRate = 7.4

// travelStops is the travel spring sampled over 24 equal steps, joined by
// straight lines.
var travelStops = [...]float32{0, 0.0388, 0.1274, 0.2367, 0.3494, 0.456, 0.5519, 0.6352, 0.7058, 0.7646, 0.813,
	0.8522, 0.8838, 0.909, 0.9291, 0.9449, 0.9573, 0.967, 0.9745, 0.9804, 0.985, 0.9885, 0.9912, 0.9933, 1}

// TravelEase is the travel spring as an easing, for what is timed
// along it rather than sprung, as the words' crossfade within their travel.
func TravelEase(t float32) float32 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	n := float32(len(travelStops) - 1)
	i := int(t * n)
	return travelStops[i] + (travelStops[i+1]-travelStops[i])*(t*n-float32(i))
}

// StageLand is how far a passage's words have travelled onto the stage
// when what only the stage has begins to come in (DESIGN.md 引文上台).
const StageLand = 0.92

// StageCross is how a passage's words as the list sets them and as the
// stage does cross while they lift off, q of the way, and as they land
// going back: the stage's come in over the first 15% of the way, the
// list's go under them from 10% to 25%, so that the two never thin out
// where they coincide.
func StageCross(q float32) (list, stage float32) {
	in := func(v float32) float32 { return Ease(min(1, max(0, v))) }
	return 1 - in((q-0.1)/0.15), in(q / 0.15)
}

// TravelReach is TravelEase turned round: how far along the travel's
// length it has come v of the way.
func TravelReach(v float32) float32 {
	if v <= 0 {
		return 0
	}
	n := len(travelStops) - 1
	for i := range n {
		if v < travelStops[i+1] {
			return (float32(i) + (v-travelStops[i])/(travelStops[i+1]-travelStops[i])) / float32(n)
		}
	}
	return 1
}

// settleRate is how a spring of a duration without bounce
// settles: within 0.001 of its target at the end, (1 + x)·e^(−x) = 0.001.
const settleRate = 9.2348

// Travel is how something goes a long way over d, a title, a face, a name
// or an article's ground: the travel spring, which does not bounce
// (DESIGN.md 走远路的弹簧). MyGo's spring Duration is the period of the undamped spring, as
// SwiftUI's, 2π/ω, so the same curve over d is a spring of d·2π/7.4; it
// gets within 0.6% of the way at d and settles after.
func Travel(d time.Duration) ui.Animation {
	return spring(d, travelRate)
}

// Follow is how an interruptible move goes, one that follows the pointer or
// the selection as it slides between items (DESIGN.md 跟手的弹簧: duration
// 0.3, bounce 0, which settles in 300 ms).
var Follow = spring(300*time.Millisecond, settleRate)

func spring(d time.Duration, rate float64) ui.Animation {
	return ui.Animation{Spring: ui.SpringOptions{Duration: time.Duration(float64(d) * 2 * math.Pi / rate)}}
}

// Card is a card's surface growing into its author's panel and landing back
// into the card: a spring of 0.4 s without bounce, the dialog's rung.
var Card = spring(DialogIn, settleRate)

// EaseOut is CSS's ease-out keyword, which the author panel's shade fades
// along.
var EaseOut = cssEaseOut

// Fade is how a value that moves nothing goes over d, as an opacity or a
// blur the view drives: along Ease, and on time under less motion too.
func Fade(d time.Duration) ui.Animation {
	return ui.Animation{Duration: d, Ease: Ease, NonMotion: true}
}

// Depth is which way the content area changes page.
type Depth uint8

const (
	Side Depth = iota // the sidebar's tabs: only the focus changes
	Deep              // into an article or an author
	Back              // out of one
)

// Defocus is how far a page crossing blurs, at most (DESIGN.md 移焦的分寸).
const Defocus = 3

// Crossing is how the content area changes page, 移焦: the page left lets
// go out of focus over Out, scaling to To; the next comes into focus over
// In, Wait after, from From; what both show travels for Travel. Only the
// content area takes part.
type Crossing struct {
	Out, In, Wait, Travel time.Duration
	To, From              float32
}

// Crossing returns the crossing of d. Going in, the old page falls back as
// the title and the ground set off, and the new one settles from a little
// closer once the ground has mostly become its page; going back reverses
// the depth, quicker.
func (d Depth) Crossing() Crossing {
	switch d {
	case Deep:
		return Crossing{Out: 240 * time.Millisecond, In: 420 * time.Millisecond, Wait: 220 * time.Millisecond,
			Travel: 600 * time.Millisecond, To: 0.985, From: 1.012}
	case Back:
		return Crossing{Out: 200 * time.Millisecond, In: 380 * time.Millisecond, Wait: 80 * time.Millisecond,
			Travel: 460 * time.Millisecond, To: 1.012, From: 0.985}
	}
	return Crossing{Out: 180 * time.Millisecond, In: 320 * time.Millisecond, Travel: 460 * time.Millisecond, To: 1, From: 1}
}

// Leave is the Transition of the page left, given in the last frame that
// builds it: a copy of it goes out of focus and fades.
func (x Crossing) Leave() ui.ElementTransition {
	return ui.ElementTransition{Duration: x.Out, Ease: Ease, Exit: &ui.Motion{Blur: Defocus, Scale: x.To}}
}

// Come is the Transition of the next page, from the frame it first shows:
// it waits unseen, then comes into focus.
func (x Crossing) Come() ui.ElementTransition {
	return ui.ElementTransition{Animation: &ui.Animation{Duration: x.In, Ease: Ease, Delay: x.Wait},
		Enter: &ui.Motion{Blur: Defocus, Scale: x.From}}
}

// Shared is the Transition of what both pages show (an article's title, an
// author's face and name), on each of them: on the page coming it travels
// from where the page going showed it, drawn over both. Give text
// ContentTransition{Morph: true} too, so that its characters grow by the
// ratio of the two type sizes rather than stretch.
func (x Crossing) Shared(match any) ui.ElementTransition {
	a := Travel(x.Travel)
	return ui.ElementTransition{Animation: &a, Match: match, Fly: true}
}

// Stage is the picture a work's first screen shows: once another is picked,
// it goes out of focus and back beneath the one coming, as a page left does
// (DESIGN.md 移焦的分寸), until it is covered. Its place and size change at
// once: the picture picked grows up out of the strip along TravelEase over
// TravelIn, which the page steps, so that it scrolls with the page.
func Stage() ui.ElementTransition {
	return ui.ElementTransition{Colors: true, ExitDuration: TravelIn, ExitEase: Ease,
		Exit: &ui.Motion{Opacity: 0.5, Blur: Defocus, Scale: 0.985}}
}

// StageStill is Stage under less motion: nothing flies, and the picture
// picked comes into focus where it shows, out of 2px of blur over the one
// shown (DESIGN.md 原处换内容), which stays until it is in.
func StageStill(picked bool) ui.ElementTransition {
	t := ui.ElementTransition{Colors: true, Duration: SwapIn, Ease: Ease, ExitDuration: SwapIn, Exit: &ui.Motion{Opacity: 1}}
	if picked {
		t.Enter = &ui.Motion{Blur: 2}
	}
	return t
}

// Layers come and go over In and Out along Ease.
func layer(in, out time.Duration, enter, exit ui.Motion) ui.ElementTransition {
	return ui.ElementTransition{Duration: in, Ease: Ease, ExitDuration: out, ExitEase: Ease, Enter: &enter, Exit: &exit}
}

var (
	// grown: opened from a button, a layer grows out of it, from 0.75 and
	// 3px of blur around the button's centre, and shrinks back into it
	// (put its TransformOrigin on the button, Origin).
	grown = ui.Motion{Blur: 3, Scale: 0.75}
	// away: a layer where it shows comes into focus from 0.97 and 2px.
	away = ui.Motion{Blur: 2, Scale: 0.97}
	// lift: lines of small text, which a blur only smudges, grow from 0.96
	// out of their trigger and leave by fading where they are.
	lift = ui.Motion{Scale: 0.96}
)

// Dialog is a dialog grown out of the button that opened it.
func Dialog() ui.ElementTransition { return layer(DialogIn, DialogOut, grown, grown) }

// Sheet is the shortcut sheet, grown out of its button.
func Sheet() ui.ElementTransition { return layer(PopIn, PopOut, grown, grown) }

// Popover is a popover beside its trigger, from AttachedOrigin.
func Popover() ui.ElementTransition { return layer(PopIn, PopOut, away, away) }

// Menu is a menu out of its trigger, from AttachedOrigin.
func Menu() ui.ElementTransition { return layer(MenuIn, MenuOut, lift, ui.Motion{}) }

// Tip is a tooltip, as a menu on the small rung.
func Tip() ui.ElementTransition { return layer(TipIn, TipOut, lift, ui.Motion{}) }

// Toast is a notice at the foot of the window: up from 12 below on RiseIn,
// coming into focus; it leaves 4 down, out of focus, over ToastOut.
func Toast() ui.ElementTransition {
	t := layer(ToastIn, ToastOut, ui.Motion{Blur: 2, Y: 12}, ui.Motion{Blur: 2, Y: 4})
	in := RiseIn
	t.Animation = &in
	return t
}

// Shade is the shade behind a centred layer: a fade on exactly the layer's
// timing both ways, so the two read as one.
func Shade(in, out time.Duration) ui.ElementTransition {
	return layer(in, out, ui.Motion{}, ui.Motion{})
}

// Behind is how far the page behind a dialog goes out of focus while it
// shows (DESIGN.md 移焦的分寸: a mask).
const Behind = 2

// Grow is how a centred layer opened by the pointer, the subscription
// dialog or the settings, comes out of what opened it and
// goes back into it as one thing (DESIGN.md 弹层), as a container
// transform: its surface, solid almost at once, goes from the opener's box
// to the layer's on a spring without bounce that settles over In, its
// corners going from 6 to 12, and the layer's words ride on it from the
// start, scaled whole to fit it, never stretched, coming into focus as it
// grows. Going back, surface and words go together over Out (Away).
type Grow struct{ In, Out time.Duration }

var (
	DialogGrow = Grow{In: DialogIn, Out: DialogOut}
	SheetGrow  = Grow{In: PopIn, Out: PopOut}
)

// Surface is how the surface goes: out to the layer, or back into the
// opener when the layer closes before it has come.
func (g Grow) Surface(back bool) ui.Animation {
	if back {
		return spring(g.Out, settleRate)
	}
	return spring(g.In, settleRate)
}

// Rise is how the surface goes up or down on its way out, a little ahead
// of how it goes across (Surface), so that its way bends into a shallow
// arc, some 40 off the straight line over 500, as a thing lifted and set
// down rather than slid.
func (g Grow) Rise() ui.Animation { return spring(g.In*4/5, settleRate) }

// Landed is how long an opener stays lit once the layer it opened begins
// to go back into it: from halfway through Out, as the layer reaches it,
// until LandHold past Out, then it dims on its usual timing.
func (g Grow) Landed(since time.Duration) bool { return since >= g.Out/2 && since <= g.Out+LandHold }

const LandHold = 150 * time.Millisecond

// WordsIn is the words' opacity as the layer opens, as Keyframes: in from
// 12% of In, once the surface is solid, to 50%, when it is some 95% grown.
func (g Grow) WordsIn() []ui.Keyframe {
	return []ui.Keyframe{{Value: 0}, {At: g.In * 3 / 25, Value: 0}, {At: g.In / 2, Value: 1, Ease: Ease}}
}

// Fit is the scale that sets a box of size w×h whole inside one of fw×fh,
// at most 1.
func Fit(w, h, fw, fh float32) float32 {
	if w <= 0 || h <= 0 {
		return 1
	}
	return min(1, fw/w, fh/h)
}

// Away is the Transition of a layer that grew, give in every frame that
// shows it with its TransformOrigin at its centre: going, it goes back into
// from as one piece, surface and words together, its centre to from's and
// shrinking to fit it, fading as it goes, over Out along Flight: a flight
// across the window, which sets off gently and settles into from slowly,
// faint by then. Of its changes only colours, which it has none of, would
// animate.
func (g Grow) Away(from, layer ui.Rect) ui.ElementTransition {
	m := ui.Motion{X: from.X + from.W/2 - layer.X - layer.W/2, Y: from.Y + from.H/2 - layer.Y - layer.H/2,
		Scale: max(0.01, min(0.9, Fit(layer.W, layer.H, from.W, from.H)))}
	return ui.ElementTransition{Duration: g.Out, Ease: Flight, Colors: true, Exit: &m}
}

// Glide is the pointer's ground in a menu going from item to item: a
// spring without bounce settled in 200 ms, quick enough to stay with the
// pointer (DESIGN.md 连续，不跳变).
var Glide = spring(200*time.Millisecond, settleRate)

// Origin returns the TransformOrigin, in fractions of box, at the centre of
// from: a layer grows out of the button that opened it and shrinks back
// into it. It may lie outside the box.
func Origin(box, from ui.Rect) (x, y float32) {
	if box.W <= 0 || box.H <= 0 || from.W <= 0 && from.H <= 0 {
		return 0.5, 0.5
	}
	return (from.X + from.W/2 - box.X) / box.W, (from.Y + from.H/2 - box.Y) / box.H
}

// Instant returns t showing every change at once, coming and going too:
// what the keyboard does is not animated (DESIGN.md Motion).
func Instant(t ui.ElementTransition) ui.ElementTransition {
	t.Duration, t.ExitDuration, t.Animation = -1, -1, nil
	return t
}

// Still returns t moving nothing: places, sizes and scales change at once
// and nothing flies, while fades and blurs keep their time (DESIGN.md 减弱动态
// 效果). MyGo does as much itself while the desktop asks for less motion;
// Still is for the app's own switch.
func Still(t ui.ElementTransition) ui.ElementTransition {
	t.Match, t.Fly = nil, false
	t.Enter, t.Exit = still(t.Enter), still(t.Exit)
	return t
}

func still(m *ui.Motion) *ui.Motion {
	if m == nil {
		return nil
	}
	return &ui.Motion{Opacity: m.Opacity, Blur: m.Blur}
}

// Ground is 底色先动's timing: the ground lights up
// over Light where it starts, travels over Travel, Wait after, and melts
// into the window over Melt, from meltEarly before it arrives.
type Ground struct{ Light, Wait, Travel, Melt time.Duration }

var (
	// Spread: the clicked row's ground lights up on the click and spreads
	// into the article's page.
	Spread = Ground{Light: 80 * time.Millisecond, Travel: 560 * time.Millisecond, Melt: 280 * time.Millisecond}
	// Gather: going back, the page rises under the article as it starts to
	// let go and shrinks into the row once the article has begun to go.
	Gather = Ground{Light: 140 * time.Millisecond, Wait: 60 * time.Millisecond, Travel: 420 * time.Millisecond, Melt: 220 * time.Millisecond}
)

const meltEarly = 40 * time.Millisecond

// Ground's look: the selected tint with 10px corners, and the page it
// spreads into, PaperMargin wider than the reading column on each side
// (DESIGN.md 底色先动).
const (
	GroundRadius = 10
	PaperMargin  = 24
)

// Opacity is the ground's opacity from the click on, as Keyframes.
func (g Ground) Opacity() []ui.Keyframe {
	melt := g.Wait + g.Travel - meltEarly
	return []ui.Keyframe{{Value: 0}, {At: g.Light, Value: 1, Ease: cssEaseOut}, {At: melt, Value: 1}, {At: melt + g.Melt, Value: 0, Ease: cssEaseInOut}}
}

// Move is how the ground's box travels: the travel spring, Wait after the
// click.
func (g Ground) Move() ui.Animation {
	a := Travel(g.Travel)
	a.Delay = g.Wait
	return a
}

// Paper returns the page the ground spreads into around the reading column
// col, from under the reader's toolbar to the foot of the content area:
// col's top and height.
func Paper(col ui.Rect) ui.Rect {
	return ui.Rect{X: col.X - PaperMargin, Y: col.Y, W: col.W + 2*PaperMargin, H: col.H}
}

// Lay is a ground on its way: it lights up at
// From, travels to To and melts. Leave To zero until it is known, as the
// row that a page going back returns to, which shows a frame later; with
// To still zero when it should travel, it stays where it lit up. Window
// DIPs, as LayoutBounds gives them.
type Lay struct {
	Ground
	From, To ui.Rect
	// Run tells this ground from the one before; Still puts it at To at
	// once, under less motion, while its light and melt keep their time.
	Run   uint64
	Still bool
	start time.Time
}

// View builds the ground, absolute in its parent, whose top-left corner is
// at origin in the window, and reports whether it still shows.
func (l *Lay) View(c *ui.Context, origin ui.Point) bool {
	now := c.Now()
	if l.start.IsZero() {
		l.start = now
	}
	t := now.Sub(l.start)
	if t > l.Wait+l.Travel-meltEarly+l.Melt {
		return false
	}
	to := l.To
	if to.W <= 0 {
		to = l.From
	}
	e := ui.Box(c.Key(l.Run)).Absolute().Radius(GroundRadius).Background(theme.Selected).IgnoreInput(true)
	a := l.Move()
	a.Delay = max(0, l.Wait-t)
	v := func(key string, from, to float32) float32 {
		if l.Still {
			return e.Jump(key, to)
		}
		if t == 0 {
			return e.Jump(key, from)
		}
		return e.AnimateTo(key, to, a)
	}
	x, y := v("x", l.From.X, to.X)-origin.X, v("y", l.From.Y, to.Y)-origin.Y
	e.Left(x).Top(y).Width(v("w", l.From.W, to.W)).Height(v("h", l.From.H, to.H))
	e.Opacity(e.Keyframes("o", l.Opacity(), ui.KeyframeOptions{Run: l.Run, NonMotion: true}))
	return true
}

// Words carries a text both pages show, an article's title or an author's
// name, from one page to the other: a drawing of each side, at its own size and with its own lines,
// rides the top-left corner of the text's box along the travel spring. The
// one going grows by Ratio, the type size coming over the type size going,
// and the one coming grows into place from 1/Ratio, so that words scale
// rather than stretch; the two cross early, while they nearly coincide:
// the one going is gone by 18% of the travel, the one coming shows from 6%
// to 26%. The page hides the text at both ends while it flies, as a view
// transition shows only its drawings.
type Words struct {
	// From and To are the text's boxes in the window (LayoutBounds); To
	// stays zero until the page coming is laid out, and the words wait
	// for it where they are.
	From, To ui.Rect
	Ratio    float32
	Travel   time.Duration
	start    time.Time
}

// View builds the words in flight, absolute in the window's overlay (call
// it in ui.Overlay), with old and new building the two drawings without
// placing them, and reports whether they still fly.
func (w *Words) View(c *ui.Context, key any, old, new func() ui.Element) bool {
	now := c.Now()
	if w.start.IsZero() {
		w.start = now
	}
	k := float32(now.Sub(w.start)) / float32(w.Travel)
	box := ui.Box(c.Key(key)).Absolute().IgnoreInput(true)
	target, to := float32(0), w.From
	if w.To.W > 0 {
		target, to = 1, w.To
	}
	p := box.AnimateTo("p", target, Travel(w.Travel))
	box.Left(w.From.X + (to.X-w.From.X)*p).Top(w.From.Y + (to.Y-w.From.Y)*p)
	box.Children(func() {
		if o := 1 - TravelEase(k/0.18); o > 0 {
			s := 1 + (w.Ratio-1)*p
			old().Absolute().Left(0).Top(0).Width(w.From.W).TransformOrigin(0, 0).Scale(s, s).Opacity(o)
		}
		if o := TravelEase((k - 0.06) / 0.2); o > 0 && target == 1 {
			s := 1/w.Ratio + (1-1/w.Ratio)*p
			new().Absolute().Left(0).Top(0).Width(w.To.W).TransformOrigin(0, 0).Scale(s, s).Opacity(o)
		}
	})
	if k < 0.26 {
		c.AnimationFrame() // the crossfade follows the clock
	}
	if target == 0 {
		return k < 0.5 // a page that never showed its end: it went with its page
	}
	return k < 0.26 || !box.Settled("p")
}

// The timings of particular controls and states.
const (
	TipWait = 550 * time.Millisecond // the first tooltip waits

	Roll      = 300 * time.Millisecond // a count's digits rolling
	Reveal    = 120 * time.Millisecond // a row's actions, a ··· trigger, appearing
	LinkHover = 140 * time.Millisecond // an author's site link
	SignHover = 150 * time.Millisecond // the sign-off's link
	ErrorOpen = 180 * time.Millisecond // a field's error line opening its height

	TabGround      = 180 * time.Millisecond                         // a sidebar tab's hover ground
	NameLight      = 160 * time.Millisecond                         // an author's name brightening
	RowDim         = 220 * time.Millisecond                         // an author's row dimming while its feed is read; its face clearing
	Turn           = 900 * time.Millisecond                         // a turn of the refresh icon
	Flare          = 480 * time.Millisecond                         // a refreshed author's face flaring
	Light          = 200 * time.Millisecond                         // an icon's accent lighting to amber and back; the favourite's orbit turning amber
	Orbit          = 460 * time.Millisecond                         // the favourite's arc closing, its satellite coming round
	Flow, FlowBack = 750 * time.Millisecond, 380 * time.Millisecond // copy's dots flowing into a check, and back

	BarAway, BarFade         = 260 * time.Millisecond, 200 * time.Millisecond // the reader's bar sent away
	SelectionSettle          = 250 * time.Millisecond                         // a selection settling before its bar shows
	ClipBarIn                = 150 * time.Millisecond                         // the clip bar coming
	FeedbackIn, FeedbackFor  = 160 * time.Millisecond, 3 * time.Second        // the reader's status line
	PositionSave             = 500 * time.Millisecond                         // a reading position saved after it stops
	Arrived                  = 200 * time.Millisecond                         // a body that kept the reader waiting this long arrives in focus
	WorkIn, WorkDim          = 360 * time.Millisecond, 280 * time.Millisecond // a work's recording fading in over its first screen; dimming as it pauses
	WorkMark, WorkMarkGrow   = 180 * time.Millisecond, 200 * time.Millisecond // the recording's play mark fading, and growing to size or pressed
	WorkTrail, WorkTrailGrow = 300 * time.Millisecond, 160 * time.Millisecond // the trail under it fading, and thickening under the pointer
	WorkShot                 = 160 * time.Millisecond                         // a work's shot under the pointer brightening, and pressed

	NoticeFor, UndoFor = 4 * time.Second, 8 * time.Second // a notice, and one offering 撤销

	// 收藏 and 摘录.
	Leave     = 150 * time.Millisecond  // a card leaving fades out of focus before the rest close up
	PictureIn = 300 * time.Millisecond  // a picture coming in once read
	Lean      = 400 * time.Millisecond  // a picture leaning in, or coming up in light, under the pointer
	Fold      = 250 * time.Millisecond  // a deleted passage folding shut, the rest closing up
	Wash      = 1100 * time.Millisecond // what was copied lighting up and letting go
	Copied    = 2 * time.Second         // 已复制 holding before the button says 复制引文 again

	// 引文上台: a passage's words travelling onto the stage on the travel
	// spring, and back in 0.6 of the time; what only the stage has coming in
	// where it stands once they are StageLand of the way there, and going.
	StageIn, StageOut           = 600 * time.Millisecond, 360 * time.Millisecond
	StagePartsIn, StagePartsOut = 180 * time.Millisecond, 160 * time.Millisecond
	SheetHold                   = 1200 * time.Millisecond // the clip card's sheet keeping its size after a turn, the dial still in hand

	RulerNap, RulerOut, RulerSpread = 900 * time.Millisecond, 400 * time.Millisecond, 140 * time.Millisecond // the scrollbar: asleep after, going, its light spreading

	Pictogram                  = time.Second / 30                                 // a moving pictogram's frame, 30 fps
	LoadingTurn, LoadingBreath = 1100 * time.Millisecond, 1600 * time.Millisecond // the loading mark's turn; its breath under less motion

	// The reader.
	ReflowOut, ReflowIn = 100 * time.Millisecond, 180 * time.Millisecond // the text letting go of its old size or measure, the new coming in
	MoonWane            = 420 * time.Millisecond                         // a list's moon going from where it was to where the reader left it
)

// Reflow is the reader's text going over to a new type size or measure:
// a copy of the old text lets go over ReflowOut
// while the new comes in over ReflowIn, in place, along the one curve; a
// fade, so less motion keeps it. instant shows the change in the next
// frame, as the keyboard has it. The element's moves and resizes are not
// animated.
func Reflow(instant bool) ui.ElementTransition {
	in, out := ReflowIn, ReflowOut
	if instant {
		in, out = time.Millisecond, time.Millisecond
	}
	return ui.ElementTransition{Duration: in, Ease: Ease, ExitDuration: out, Colors: true, Enter: &ui.Motion{}, Exit: &ui.Motion{}}
}

// Dissolve times a row that turns into dots reeled into its face, as a
// writer's sidebar row let go from its menu (DESIGN.md 取消订阅): the face
// draws into its point over Face; from the face's side the name turns into
// dots a strip at a time, each Stagger after the one before, drawing in
// over Grow as its dot grows; Grow after that, the dot is reeled into the
// point over Reel. Once the last dot is in, the point goes out over Out,
// and Wait later the rows below close up on Close, a spring without bounce
// (Closed). Under less motion the row fades over Fade instead and the rows
// close up at once.
type Dissolve struct{ Face, Stagger, Grow, Reel, Out, Wait, Fade, Close time.Duration }

// LetGo is a writer's row let go.
var LetGo = Dissolve{Face: 300 * time.Millisecond, Stagger: 12 * time.Millisecond, Grow: 180 * time.Millisecond,
	Reel: 320 * time.Millisecond, Out: 250 * time.Millisecond, Wait: 100 * time.Millisecond, Fade: 150 * time.Millisecond,
	Close: 500 * time.Millisecond}

// Closed returns how far the rows below have closed up, t after they set
// off: MyGo's spring of Duration Close without bounce, from rest,
// 1 − (1 + ωt)·e^(−ωt) with ω = 2π/Close (走远路的弹簧).
func (d Dissolve) Closed(t time.Duration) float32 {
	if t <= 0 {
		return 0
	}
	x := 2 * math.Pi * t.Seconds() / d.Close.Seconds()
	if x >= settleRate {
		return 1
	}
	return float32(1 - (1+x)*math.Exp(-x))
}

// Closing is how long the rows below take to close up, settled within
// 0.001 of the way.
func (d Dissolve) Closing() time.Duration {
	return time.Duration(settleRate / (2 * math.Pi) * float64(d.Close))
}

// Capture 拓本 (DESIGN.md Capture): a page's thumbnail flying from the
// button into the island over CaptureFly along Flight; its dots taking the first
// screen's colours over CaptureRecolour; once taken, the first screen rising out of
// them over CaptureRise and one light passing over it over CaptureSweep, then staying
// CaptureLinger, or that long after the island was last held, before it flies to
// 收藏. Over the dots not yet developed a slow light passes from the top left, once
// each CaptureWave; a pack that failed greys its dots over CaptureGrey and lets them
// fall on the travel spring over CaptureFall, each up to CaptureFallStagger after the
// first, and leaves the island CaptureFailedFor later. A card's line of where its
// page is stays bright CaptureLineHold after the page stops. How far a pack has
// come shows in hundredths that move on at most every CapturePercent; while the
// page opens they creep toward a tenth, nearing it over CaptureOpening, so they
// never stand still. 收藏's tab says 已拓下 of a page taken while the island was
// away for CaptureTold, or that long after the pointer was last on it.
const (
	CaptureFly                      = 600 * time.Millisecond
	CaptureRecolour                 = 400 * time.Millisecond
	CaptureRise                     = 500 * time.Millisecond
	CaptureSweep                    = 600 * time.Millisecond
	CaptureLinger                   = 5 * time.Second
	CaptureWave                     = 2400 * time.Millisecond
	CaptureGrey                     = 250 * time.Millisecond
	CaptureFall, CaptureFallStagger = 900 * time.Millisecond, 300 * time.Millisecond
	CaptureFailedFor                = 10 * time.Minute
	CaptureLineHold                 = 700 * time.Millisecond
	CapturePercent                  = 400 * time.Millisecond
	CaptureOpening                  = 4 * time.Second
	CaptureTold                     = 3 * time.Second
)

// FlightFor is how long a long flight takes over d px, as a face flies into
// the sidebar: 600 ms, longer by 0.35 ms a px, 900 at most.
func FlightFor(d float64) time.Duration {
	return time.Duration(min(900, 600+d*0.35)) * time.Millisecond
}

// Island is the capture island growing out of its point at the window's top
// edge and going back into it, a long way on a spring without bounce
// (走远路的弹簧).
var Island = Travel(450 * time.Millisecond)

// Develop is a thumbnail's dots following the pack's progress: the follow
// spring, kept under less motion, where only colours change by it.
var Develop = ui.Animation{Spring: Follow.Spring, NonMotion: true}

// Unfold is a kind's wall coming in under the band as its door opens into
// it (PAGES.md 类别页): its works come into focus from Rise below over In,
// the first After the crossing starts, each a Row later for each row
// further down and half a Row for each column further from the door.
var Unfold = struct {
	After, Row, In time.Duration
	Rise           float32
}{After: 80 * time.Millisecond, Row: 60 * time.Millisecond, In: 420 * time.Millisecond, Rise: 16}

// DoorLeave is how long the page a door opens or closes over takes to go:
// covered by the ground the door opens, or nearly gone from over Explore
// before that ground draws back into the door (PAGES.md 类别页).
const DoorLeave = 150 * time.Millisecond

// DoorDim is how the door's picture goes into the ground it spreads into
// (PAGES.md 类别页): opening, dark Dim in; closing back, the ground held
// whole Hold while the page goes, then showing the picture again from Show
// to Shown, as it nears the door. The shade behind the name goes over Shade
// as the name leaves the door, and comes back over the last Shade of the
// way as it lands.
var DoorDim = struct{ Dim, Hold, Show, Shown, Shade time.Duration }{Dim: 150 * time.Millisecond, Hold: 100 * time.Millisecond,
	Show: 150 * time.Millisecond, Shown: 350 * time.Millisecond, Shade: 150 * time.Millisecond}

// A door's drawing acting under the pointer (PAGES.md 探索, 门): its moving
// part goes to where it is drawn over DoorAct along Ease, and back over
// DoorActBack; the pendulum's swing (DoorSwing) and the stamp's press
// (DoorStamp) play once each, over DoorSwingFor and DoorStampFor.
const (
	DoorAct, DoorActBack       = 600 * time.Millisecond, 360 * time.Millisecond
	DoorSwingFor, DoorStampFor = 1600 * time.Millisecond, 760 * time.Millisecond
)

// DoorSwing is the motion door's pendulum k of the way through its swing,
// pushed from rest: its angle as a share of its widest, reached on the first
// swing out, each swing after smaller, at rest again at the end.
func DoorSwing(k float32) float32 {
	if k <= 0 || k >= 1 {
		return 0
	}
	return float32(math.Sin(3*math.Pi*float64(k)) * math.Pow(1-float64(k), 1.6) / doorSwingWidest)
}

// doorSwingWidest is sin(3πk)·(1−k)^1.6 at its widest, k ≈ 0.146.
const doorSwingWidest = 0.7622

// DoorStamp is how far down the brand door's stamp is, k of the way through
// its press: down along Ease over the first quarter, a moment on the sheet,
// lifted along Ease.
func DoorStamp(k float32) float32 {
	switch {
	case k <= 0 || k >= 1:
		return 0
	case k < 0.26:
		return Ease(k / 0.26)
	case k < 0.34:
		return 1
	}
	return 1 - Ease((k-0.34)/0.66)
}
