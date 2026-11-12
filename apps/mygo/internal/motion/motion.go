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

// How far a press shrinks what it presses (DESIGN.md 按下就有反应).
const (
	Pressed    = 0.97
	PressedTab = 0.96
)

// Special curves, named so no one writes their own.
var (
	// Flight is a long flight across the window, as an avatar into its
	// new sidebar row: it leaves gently, carries through, long damped tail.
	Flight = ui.CubicBezier(0.45, 0, 0.15, 1)
	// Swing settles back with damping, as the compass when Explore
	// becomes current.
	Swing = ui.CubicBezier(0.45, 0, 0.2, 1)
	// Brake is an exact quadratic ease-out: it starts at the speed
	// something already has and stops at zero, as a spinning icon.
	Brake = ui.CubicBezier(0.333, 0.667, 0.667, 1)
)

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

// Toast is a notice at the foot of the window: up from 8 below, coming into
// focus; it leaves 4 down, out of focus.
func Toast() ui.ElementTransition {
	return layer(ToastIn, ToastOut, ui.Motion{Blur: 2, Y: 8}, ui.Motion{Blur: 2, Y: 4})
}

// Shade is the shade behind a centred layer: a fade on exactly the layer's
// timing both ways, so the two read as one.
func Shade(in, out time.Duration) ui.ElementTransition {
	return layer(in, out, ui.Motion{}, ui.Motion{})
}

// Behind is how far the page behind a dialog goes out of focus while it
// shows (DESIGN.md 移焦的分寸: a mask).
const Behind = 2

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

	InkDraw, InkUndraw = 480 * time.Millisecond, 220 * time.Millisecond // 一笔圈出: the loop sweeping round the words, and back
	InkGone            = 200 * time.Millisecond                         // the loop drawing in as a button busies
	Roll               = 300 * time.Millisecond                         // a count's digits rolling
	Reveal             = 120 * time.Millisecond                         // a row's actions, a ··· trigger, appearing
	LinkHover          = 140 * time.Millisecond                         // an author's site link
	SignHover          = 150 * time.Millisecond                         // the sign-off's link
	ErrorOpen          = 180 * time.Millisecond                         // a field's error line opening its height

	TabGround          = 180 * time.Millisecond                         // a sidebar tab's hover ground
	TabOpen, TabWords  = 220 * time.Millisecond, 160 * time.Millisecond // a current tab's label opening, its words fading in
	NameLight          = 160 * time.Millisecond                         // an author's name brightening
	RowDim             = 220 * time.Millisecond                         // an author's row dimming while its feed is read; its face clearing
	SwingRise, SwingIn = 140 * time.Millisecond, 540 * time.Millisecond // the compass swinging out, and settled (26% of 540)
	Turn               = 900 * time.Millisecond                         // a turn of the refresh icon
	Flare              = 480 * time.Millisecond                         // a refreshed author's face flaring
	Glow               = 200 * time.Millisecond                         // the favourite's glow
	Orbit              = 460 * time.Millisecond                         // the favourite's arc closing, its satellite coming round

	Focus, FocusIn, FocusOut = 400 * time.Millisecond, 300 * time.Millisecond, 200 * time.Millisecond // 专注阅读: the sidebar sliding away, fading
	BarAway, BarFade         = 260 * time.Millisecond, 200 * time.Millisecond                         // the reader's bar sent away
	BarTitle, BarTitleRise   = 180 * time.Millisecond, 240 * time.Millisecond                         // the bar taking the title
	SelectionSettle          = 250 * time.Millisecond                                                 // a selection settling before its bar shows
	ClipBarIn                = 150 * time.Millisecond                                                 // the clip bar coming
	FeedbackIn, FeedbackFor  = 160 * time.Millisecond, 3 * time.Second                                // the reader's status line
	PositionSave             = 500 * time.Millisecond                                                 // a reading position saved after it stops
	Arrived                  = 200 * time.Millisecond                                                 // a body that kept the reader waiting this long arrives in focus
	ThumbFade, ThumbFocus    = 280 * time.Millisecond, 560 * time.Millisecond                         // a list's picture coming into focus
	WorkIn, WorkDim          = 360 * time.Millisecond, 280 * time.Millisecond                         // a work's recording fading in over its first screen; dimming as it pauses
	WorkMark, WorkMarkGrow   = 180 * time.Millisecond, 200 * time.Millisecond                         // the recording's play mark fading, and growing to size or pressed
	WorkTrail, WorkTrailGrow = 300 * time.Millisecond, 160 * time.Millisecond                         // the trail under it fading, and thickening under the pointer
	WorkShot                 = 160 * time.Millisecond                                                 // a work's shot under the pointer brightening, and pressed

	NoticeFor, UndoFor = 4 * time.Second, 8 * time.Second // a notice, and one offering 撤销

	// 收藏 and 摘录.
	Leave      = 150 * time.Millisecond  // a card leaving fades out of focus before the rest close up
	PictureIn  = 300 * time.Millisecond  // a picture coming in once read
	Lean       = 400 * time.Millisecond  // a picture leaning in, or coming up in light, under the pointer
	Fold       = 250 * time.Millisecond  // a deleted passage folding shut, the rest closing up
	Rise       = 300 * time.Millisecond  // a passage put back rising 8 into place
	Wash       = 1100 * time.Millisecond // what was copied lighting up and letting go
	Copied     = 2 * time.Second         // 已复制 holding before the button says 复制引文 again
	SettleWait = 150 * time.Millisecond  // a passage's card grown, its words settling in (over SwapIn)
	MarkWait   = 350 * time.Millisecond  // and its quote mark lighting (over SwapIn)

	RulerNap, RulerOut, RulerSpread = 900 * time.Millisecond, 400 * time.Millisecond, 140 * time.Millisecond // the scrollbar: asleep after, going, its light spreading

	Boil                       = time.Second / 12                                 // the pictograms' line boil, 12 fps
	LoadingTurn, LoadingBreath = 1100 * time.Millisecond, 1600 * time.Millisecond // the loading mark's turn; its breath under less motion

	// The reader.
	ReflowOut, ReflowIn = 100 * time.Millisecond, 180 * time.Millisecond // the text letting go of its old size or measure, the new coming in
	SwipeSettle         = 250 * time.Millisecond                         // the swipe's back mark arming, and the mark and the page settling back
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
