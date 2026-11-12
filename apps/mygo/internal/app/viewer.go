package app

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/theme"
)

// The picture viewer (ui/overlays.tsx ImageViewer; ArticleReader.tsx
// openImage, WorkPage.tsx WorkShots): one picture large over the shade.
// Opened by the pointer, the picture travels out of its place on the page
// into the middle of the window and, closed, back into that place while it
// is in the window; it stays solid all the way, only the shade and the
// controls fade. With nowhere to travel it fades and scales like a dialog,
// and under less motion only fades; from the keyboard it is simply there
// or gone. Esc, a click beside the picture or 关闭 close it, and the focus
// goes back to what opened it, whose place stays empty while the picture
// is large. A work's shots show one at a time, named with their place in
// the set: 上一张 and 下一张 at the sides, and ← →, walk through them and
// round; by the pointer the picture shown lets go where it is while the
// next comes into focus, from the keys the next is simply there. A shot
// that did not load shows as a quiet tile saying so.

type viewer struct {
	open    bool
	pic     reader.Pic
	rp      *readerPage // whose page shows the picture
	label   string      // what the viewer is called: 图片预览, or 截图
	run     int         // tells this opening from the one before
	since   time.Time   // the frame it opened in
	travel  bool        // it flew out of its place
	instant bool        // the keys opened it
	until   time.Time   // closed: its copy goes until then, and its place stays empty

	// A work's shots: the one shown, from 1, 0 for a picture of the body;
	// its place in the strip as this build laid it out; a step asked for,
	// and the frame it was asked in, which shows it in the next one, so that
	// the picture it leaves goes as that frame said; the frame the last
	// step showed in.
	shot   int
	place  ui.Rect
	step   int
	stepAt time.Time
	swapAt time.Time
	// The shot a step by the pointer left, letting go where it showed for
	// SwapOut from swapAt; 0 for none.
	gone    int
	goneBox ui.Rect
}

// land shows a step asked for in a frame before this one, through a set of
// n shots, round at both ends.
func (v *viewer) land(now time.Time, n int) {
	if v.step != 0 && now.After(v.stepAt) && n > 0 {
		v.shot = ((v.shot-1+v.step)%n+n)%n + 1
		v.step, v.swapAt = 0, now
	}
}

// pictures has the reader show its pictures large, and leave empty the
// place of the one that is.
func (a *App) pictures(c *ui.Context, rp *readerPage, live bool) {
	r := rp.r
	r.OnPicture, r.Hide = nil, 0
	if live {
		now := c.Now()
		r.OnPicture = func(p reader.Pic) {
			a.viewer = viewer{open: true, pic: p, rp: rp, label: "图片预览", run: a.viewer.run + 1, since: now, instant: a.kbd}
		}
	}
	if v := &a.viewer; v.rp == rp && v.shot == 0 && (v.open || c.Now().Before(v.until)) {
		r.Hide = v.pic.ID
	}
}

// viewerView builds the picture viewer while it is open.
func (a *App) viewerView(c *ui.Context) {
	v := &a.viewer
	now := c.Now()
	if !v.open {
		if v.until.IsZero() {
			return
		}
		if now.Before(v.until) {
			c.After(v.until.Sub(now))
			return
		}
		*v = viewer{run: v.run}
		return
	}
	shots := 0
	if v.shot > 0 && v.rp != nil && v.rp.work != nil {
		shots = len(v.rp.work.Sections)
	}
	if v.rp == nil || a.page != (page{"reader", v.rp.id}) || v.shot > shots || v.shot == 0 && (v.pic.W <= 0 || v.pic.H <= 0) {
		*v = viewer{run: v.run} // its page went
		return
	}
	// Its place on the page, or in the strip, if it shows there: closed
	// now, the picture would fly back into it.
	twin := v.rp.r.HiddenBox()
	if v.shot > 0 {
		v.land(now, shots)
		v.pic, twin, v.place = a.shotPic(v.rp, v.shot-1), v.place, ui.Rect{}
	}
	p := v.pic
	w, h := c.Size()
	still := a.still(c)
	// At its own size within the window less 80 a side and 64 above and
	// below, or a tile 420 by 260 for a shot that did not load; the caption
	// 12 under it, with the place in the set 8 after the words, the two in
	// the middle.
	missing := p.W <= 0 || p.H <= 0
	pw, ph := min(420, w-160), float32(260)
	if !missing {
		fit := min(1, (w-160)/p.W, (h-128)/p.H)
		pw, ph = p.W*fit, p.H*fit
	}
	words, count := p.Caption, ""
	if shots > 1 {
		count = fmt.Sprintf("%d / %d", v.shot, shots)
	}
	room := float32(0)
	if words != "" || count != "" {
		said := words
		if count != "" {
			said += " " + count
		}
		_, line := c.MeasureText(0, ui.Span{Text: "图", Size: theme.Body})
		_, all := c.MeasureText(w-160, ui.Span{Text: said, Size: theme.Body})
		room = 12 + float32(max(1, math.Round(float64(all/max(1, line)))))*theme.Body*1.5
	}
	box := ui.Rect{X: (w - pw) / 2, Y: (h - ph - room) / 2, W: pw, H: ph}
	first := now.Equal(v.since)
	if first && twin.W <= 0 {
		twin = p.Box // the page has not left it empty yet
	}
	// Anywhere in the window (overlays.tsx onScreen), as a shot past the
	// strip's edge.
	back := !still && twin.W > 0 && twin.H > 0 && twin.Y < h && twin.Y+twin.H > 0 && twin.X < w && twin.X+twin.W > 0
	if first {
		v.travel = back && !v.instant
	}
	in, out := motion.DialogIn, motion.DialogOut
	if v.travel {
		in = motion.TravelIn
	}
	if back {
		out = motion.TravelOut
	}
	// The shade, the controls and the caption fade on the picture's timing.
	fade := ui.ElementTransition{Duration: in, Ease: motion.Ease, ExitDuration: out, ExitEase: motion.Ease, Colors: true,
		Enter: &ui.Motion{}, Exit: &ui.Motion{}}
	// The picture flies between its place and the middle as one move and
	// one scale (overlays.tsx fly), or comes into focus where it shows.
	flight := func(r ui.Rect) *ui.Motion {
		s := min(r.W/pw, r.H/ph)
		return &ui.Motion{X: r.X + r.W/2 - (box.X + pw/2), Y: r.Y + r.H/2 - (box.Y + ph/2), Scale: s, Opacity: 1}
	}
	focus := &ui.Motion{Blur: 2, Scale: 0.97}
	if still {
		focus = &ui.Motion{Blur: 2}
	}
	pt := ui.ElementTransition{Duration: in, Ease: motion.Ease, ExitDuration: out, ExitEase: motion.Ease, Colors: true, Enter: focus, Exit: focus}
	if v.travel {
		pt.Enter = flight(twin)
	}
	if !v.swapAt.IsZero() {
		// A step's picture settles in where it shows, out of a 2 DIP blur
		// (overlays.tsx settle).
		pt.Duration, pt.Enter = motion.SwapIn, &ui.Motion{Blur: 2}
	}
	if back {
		pt.Exit = flight(twin)
	}
	if first && v.instant {
		fade.Duration, pt.Duration = -1, -1
	}
	if v.swapAt.Equal(now) && a.kbd {
		pt.Duration = -1 // a step from the keys is simply there
	}
	closed, kbd := false, false
	ask := func(by int) {
		if v.step == 0 {
			v.stepAt = now
		}
		v.step += by
		// By the pointer the picture shown stays a moment, letting go where
		// it is (overlays.tsx step's copy).
		v.gone, v.goneBox = 0, box
		if !a.kbd {
			v.gone = v.shot
		}
	}
	// The picture of p in box, or a quiet tile for a shot that did not
	// load, as a dialog: 12 corners and the dialog's shadow.
	picture := func(key any, p reader.Pic, box ui.Rect) ui.Element {
		var e ui.Element
		if p.W <= 0 || p.H <= 0 {
			name := "图片没能载入"
			if p.Alt != "" {
				name += "：" + p.Alt
			}
			e = ui.Column(c.Key(key)).Role(ui.RoleImage).Label(name).Center().Gap(8).FontSize(theme.Body).LineHeight(1.5).TextColor(theme.Muted).
				Background(theme.Surface).InsetShadow(0, 0, 0, 1, theme.LineSoft).Children(func() {
				a.icon(c, "brokenImage", 20, false)
				ui.Text(c, "图片没能载入").AccessHidden(true)
			})
		} else {
			e = p.View(c.Key(key)).Unselectable().Label(p.Alt)
		}
		return e.Absolute().Left(box.X).Top(box.Y).Size(box.W, box.H).Radius(12).Shadow(0, 24, 64, 0, theme.ShadowDialog).TransformOrigin(0.5, 0.5)
	}
	ui.Overlay(c, func() {
		ui.Box(c.Key("viewer-shade")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(theme.Shade).IgnoreInput(true).
			AccessHidden(true).Transition(fade)
		// The whole window is the dialog, so the buttons at its edges are
		// inside it; a click on the bare stage closes.
		stage := ui.Box(c.Key("viewer")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Modal().Role(ui.RoleDialog).Label(v.label).
			ObserveInput(a.observe).Transition(fade)
		// Esc closes it before a tooltip of its buttons.
		if stage.Shortcut(0, ui.KeyEscape) || stage.OverlayShortcut(0, ui.KeyEscape) {
			closed, kbd = true, true
		}
		if stage.Clicked() {
			closed = true
		}
		if shots > 1 && stage.Shortcut(0, ui.KeyLeft) {
			ask(-1)
		}
		if shots > 1 && stage.Shortcut(0, ui.KeyRight) {
			ask(1)
		}
		stage.Children(func() {
			if shots > 1 {
				if a.roundButton(c, "prev", "back", "上一张", "上一张 · ←").Absolute().Left(24).Top(h/2 - 20).Clicked() {
					ask(-1)
				}
				if a.roundButton(c, "next", "forward", "下一张", "下一张 · →").Absolute().Right(24).Top(h/2 - 20).Clicked() {
					ask(1)
				}
			}
			b := a.roundButton(c, "close", "close", "关闭", "关闭 · Esc").Absolute().Top(24).Right(24)
			if b.Clicked() {
				closed = true
			}
		})
		if v.step != 0 {
			// A step shows in the next frame; the picture shown goes without
			// a copy of its own.
			pt.Exit = nil
			c.AnimationFrame()
		}
		img := picture(fmt.Sprint("viewer-pic:", v.run, ":", v.shot), p, box).Transition(pt)
		img.Clicked() // a click on the picture is not one beside it
		// The picture stepped from by the pointer lets go where it was, over
		// the next, as the old app's copy does.
		// ponytail: one at a time; a step within SwapOut of the last drops its copy at once.
		if g := v.gone; g > 0 && g <= shots && g != v.shot && v.step == 0 && now.Sub(v.swapAt) < motion.SwapOut {
			k := motion.Ease(float32(now.Sub(v.swapAt)) / float32(motion.SwapOut))
			picture(fmt.Sprint("viewer-pic:", v.run, ":", g), a.shotPic(v.rp, g-1), v.goneBox).Opacity(1 - k).IgnoreInput(true).
				AccessHidden(true).Transition(ui.ElementTransition{Duration: -1})
			c.AnimationFrame()
		}
		if words != "" || count != "" {
			line := ui.Row(c.Key("viewer-words")).Absolute().Left(80).Width(w - 160).Top(box.Y + ph + 12).Justify(ui.Center).AlignItems(ui.Baseline).
				Gap(8).FontSize(theme.Body).LineHeight(1.5).TextColor(theme.Text2).Transition(fade)
			line.Children(func() {
				if words != "" {
					ui.Text(c, words).TextAlign(ui.Center).Shrink(1).MinWidth(0)
				}
				if count != "" {
					ui.Text(c, count).FontSize(theme.Label).TextColor(theme.Muted).FontFeatures("tnum").Shrink(0)
				}
			})
			line.Clicked()
		}
	})
	if closed {
		v.open = false
		v.until = now.Add(out)
		if kbd || a.kbd {
			c.DisableAnimations() // from the keys it is simply gone
			v.until = time.Time{}
		}
	}
}

// roundButton is the viewer's: 40 across and round, on a faint ground of
// its own, so that it reads on the shade (overlays.tsx round).
func (a *App) roundButton(c *ui.Context, key, icon, label, tip string) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Label(label).Size(40, 40).Radius(20).Center().FocusRing(false)
	ground, ink := theme.Selected, theme.Text2
	if b.Hovered() {
		ground, ink = theme.ViewerHover, theme.Text
	}
	b.Background(b.AnimateColor("bg", ground, a.ease(motion.Hover))).TextColor(b.AnimateColor("fg", ink, a.ease(motion.Hover)))
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	a.press(b, motion.Pressed)
	b.Children(func() { a.icon(c, icon, 16, false) })
	a.tip(c, b, tip, false)
	return b
}
