package app

import (
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"trie/internal/motion"
	"trie/internal/theme"
)

// Controls: one look per kind of button, every state
// drawn. Keyboard focus shows at once, in the focus colour.

// pressing reports whether the pointer presses e, the keys aside: a button
// goes up a step while it does, nothing changing its shape (DESIGN.md 按下);
// an icon button's press rings out of its icon (dotIcon).
func (a *App) pressing(e ui.Element) bool { return e.Pressed() && !a.kbd }

// give shrinks a card or a passage the pointer presses a little, from the
// press on (DESIGN.md 按下就有反应).
func (a *App) give(e ui.Element, scale float32) {
	p := ui.Pose{}
	if e.Pressed() && !a.kbd {
		p.Scale = scale
	}
	e.AnimatePose(p, ui.Animation{Duration: motion.Press, Ease: motion.Ease})
}

// slideGround builds list's selected ground in parent, under the item at
// to (both as the frame before laid them out, in window coordinates): one
// piece sliding between the items on Carry, its length going from the one
// item's to the other's on the same value (DESIGN.md 选中的光是一份).
// vertical says which way the list runs. At once from the keys, under less
// motion, and where it first shows (unslide).
func (a *App) slideGround(c *ui.Context, parent ui.Element, list any, to ui.Rect, vertical bool) {
	box := parent.LayoutBounds()
	if to.W <= 0 || box.W <= 0 {
		return // not laid out yet
	}
	if a.slides == nil {
		a.slides = map[any]*slide{}
	}
	r := ui.Rect{X: to.X - box.X, Y: to.Y - box.Y, W: to.W, H: to.H}
	lo, hi := r.Y, r.Y+r.H
	if !vertical {
		lo, hi = r.X, r.X+r.W
	}
	_, shown := a.slides[list]
	a.slides[list] = &slide{}
	g := ui.Box(c.Key(list)).Absolute().ZIndex(-1).Radius(6).Background(theme.Selected).IgnoreInput(true)
	var a0, a1 float32
	if !shown || a.kbd || a.still(c) {
		a0, a1 = g.Jump("lo", lo), g.Jump("hi", hi)
	} else {
		a0, a1 = g.AnimateTo("lo", lo, motion.Carry), g.AnimateTo("hi", hi, motion.Carry)
	}
	if vertical {
		g.Left(r.X).Width(r.W).Top(a0).Height(max(0, a1-a0))
	} else {
		g.Top(r.Y).Height(r.H).Left(a0).Width(max(0, a1-a0))
	}
}

// unslide forgets list's ground while it does not show, so that it next
// shows where it is at once.
func (a *App) unslide(list any) { delete(a.slides, list) }

// slide marks a list's selected ground as showing (slideGround).
type slide struct{}

type btnKind uint8

const (
	secondary btnKind = iota
	primary
	ghost
	danger
)

type btnOpt struct {
	kind     btnKind
	small    bool
	inset    bool // in a panel: the loop stays inside the button
	loading  bool
	disabled bool
	key      any
	trail    string // an icon after the words, 4 from them (去看看's arrow)
	// words, when set, draws the words in place of label, in the colour
	// and weight given (摘录's 复制引文, whose wordings cross in place).
	words func(color ui.Color, weight int)
}

// button is 一笔圈出 (DESIGN.md Buttons): the words alone at rest; under
// the pointer one hand-drawn loop sweeps round them from the upper left,
// drawn at once by the keyboard's focus in the focus colour. Primary and
// danger keep theirs drawn.
func (a *App) button(c *ui.Context, label string, o btnOpt) ui.Element {
	key := o.key
	if key == nil {
		key = "btn:" + label
	}
	b := ui.ButtonBase(c.Key(key)).Label(label).Height(32).Padding(0, 12).Radius(6).AlignItems(ui.Center).Justify(ui.Center).
		FontSize(theme.Body).LineHeight(16.0 / 13).Shrink(0).FocusRing(false)
	color, weight := theme.Text, 400
	switch o.kind {
	case primary, danger:
		b.Padding(0, 16)
		weight = 600
	case ghost:
		b.Padding(0, 8)
		color = theme.Text2
	}
	if o.small {
		b.Height(24).Padding(0, 8).FontSize(theme.Label)
	}
	still := o.disabled || o.loading
	if o.disabled {
		b.Disabled(true).Opacity(0.4)
	}
	if o.loading {
		b.Busy(true)
	}
	hover := b.Hovered() && !still
	down := a.pressing(b) && !still
	focus := b.FocusVisible()
	if o.kind == ghost && (hover || down) {
		color = theme.Text
	}
	kept := o.kind == primary || o.kind == danger
	ink := theme.Text2
	switch {
	case focus:
		ink = theme.Focus
	case o.kind == primary:
		ink = theme.Accent
	case o.kind == danger:
		ink = theme.Danger
	case down:
		ink = theme.Text // pressed, the loop goes up a step
	}
	draw := float32(0)
	if kept && !o.loading || hover || focus {
		draw = 1
	}
	var anim ui.Animation
	switch {
	case focus || a.kbd || a.still(c) || kept:
		anim = ui.Animation{Duration: time.Nanosecond}
	case draw == 1:
		anim = ui.Animation{Duration: motion.InkDraw, Ease: motion.Ease}
	default:
		anim = ui.Animation{Duration: motion.InkUndraw, Ease: motion.Ease}
	}
	drawn := b.AnimateTo("ink", draw, anim)
	gone := b.AnimateTo("gone", map[bool]float32{true: 1, false: 0}[o.loading], ui.Animation{Duration: motion.InkGone, Ease: motion.Ease})
	inkColor := b.AnimateColor("inkColor", ink, a.ease(motion.Hover))
	textColor := b.AnimateColor("color", color, a.ease(motion.Hover))
	n := utf8.RuneCountInString(label)
	loop := inkLoops()[map[bool]int{true: 0, false: map[bool]int{true: 1, false: 2}[n <= 4]}[n <= 2]]
	return b.Children(func() {
		if drawn > 0.001 && gone < 0.999 {
			l := ui.Image(c, loop).Fit(ui.FillBox).Absolute().IgnoreInput(true).TextColor(inkColor)
			inX, inY := float32(-9), float32(-5)
			if o.small {
				inX, inY = -7, -4
			}
			if o.inset {
				inX, inY = 0, 0
			}
			l.Left(inX).Right(inX).Top(inY).Bottom(inY)
			if sweep := min(1, drawn*380/360); sweep < 1 {
				l.MaskConicGradient(ui.ConicGradient{Angle: -70, Stops: []ui.GradientStop{{Color: theme.Text, At: 0}, {Color: theme.Text, At: sweep}, {Color: ui.Transparent, At: sweep}}})
			}
			if gone > 0 {
				l.Opacity(1-gone).TransformOrigin(0.5, 0.5).Scale(1-0.6*gone, 1-0.6*gone)
			}
		}
		if o.words != nil {
			o.words(textColor, weight)
			return
		}
		t := ui.Text(c, label).TextColor(textColor).FontWeight(weight).NoWrap()
		if o.trail != "" {
			a.icon(c, o.trail, 16, false).Margin(0, 0, 0, 4).TextColor(textColor)
		}
		if o.loading {
			t.Opacity(0)
			ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().Children(func() {
				a.loading(c, map[bool]float32{true: 12, false: 16}[o.small])
			})
		}
	})
}

// iconOpt is an IconButton's: its icon, its name, its tooltip; small is
// 24 square; active shows the selected ground (its panel open); lit lights
// the accent; glow shows the hover ground for a moment, as a layer it opened
// lands back in it; turn rotates the icon, in degrees.
type iconOpt struct {
	icon, label, tip         string
	small, active, lit, glow bool
	disabled, beside         bool
	turn                     float32
}

// iconButton is ui/controls IconButton: 32 square (24 small), text-2 and the
// hover ground, the selected ground while active; tip is its tooltip.
func (a *App) iconButton(c *ui.Context, key any, icon, label, tip string, small, active bool) ui.Element {
	return a.iconBtn(c, key, iconOpt{icon: icon, label: label, tip: tip, small: small, active: active})
}

func (a *App) iconBtn(c *ui.Context, key any, o iconOpt) ui.Element {
	size := float32(32)
	if o.small {
		size = 24
	}
	b := ui.ButtonBase(c.Key(key)).Label(o.label).Size(size, size).Radius(6).Center().Shrink(0).FocusRing(false)
	if o.disabled {
		b.Disabled(true).Opacity(0.4)
	}
	hover := b.Hovered() && !o.disabled
	color, ground := theme.Icon, ui.Transparent
	switch {
	case o.active:
		color, ground = theme.Text, theme.Selected
	case hover || o.glow:
		color, ground = theme.Text, theme.Hover
	}
	fg := b.AnimateColor("fg", color, a.ease(motion.Hover))
	b.Background(b.AnimateColor("bg", ground, a.ease(motion.Hover))).TextColor(fg)
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	in := b
	if o.disabled {
		in = ui.Element{}
	}
	b.Children(func() { a.dotIcon(c, in, o.icon, 16, iconLook{lit: o.lit, turn: o.turn, ink: fg}) })
	if o.tip != "" && !o.disabled {
		a.tip(c, b, o.tip, o.beside)
	}
	return b
}

// tip is the app's tooltip: a label, not a panel, below the
// element, or beside it in the sidebar; the first waits 550 ms.
func (a *App) tip(c *ui.Context, e ui.Element, text string, beside bool) {
	ui.TooltipBase(c, e, func(t ui.Element) {
		if beside {
			t.AttachTo(e, ui.AnchorRight, ui.AnchorLeft).Margin(0, 0, 0, 6)
		} else {
			t.AttachTo(e, ui.AnchorBottom, ui.AnchorTop).Margin(6, 0, 0, 0)
		}
		t.AttachedOrigin().AttachEdgeMargin(8).Transition(a.tr(c, motion.Tip())).IgnoreInput(true).
			MaxWidth(300).Padding(4, 8).Radius(6).Background(theme.Elevated).FontSize(theme.Label).LineHeight(16.0/12).TextColor(theme.Text2).
			InsetShadow(0, 0, 0, 1, theme.Line).Shadow(0, 0, 0, 1, theme.ShadowEdge).Shadow(0, 4, 12, 0, theme.ShadowTip)
		t.Children(func() { ui.Text(c, text) })
	})
}

// linkButton is an underlined action in a line of text (重试, 清除搜索).
func (a *App) linkButton(c *ui.Context, key any, label string, strong, disabled bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Label(label).Radius(6).FocusRing(false)
	hover := b.Hovered() && !disabled
	color, line := theme.Text2, theme.Faint
	if strong {
		color = theme.Text
	}
	if hover {
		color, line = theme.Text, theme.Text2
	}
	if disabled {
		b.Disabled(true).Opacity(0.4)
	}
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	return b.Children(func() {
		ui.Text(c, label).TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover))).Underline().UnderlineOffset(3).
			DecorationColor(b.AnimateColor("line", line, a.ease(motion.Hover)))
	})
}

// kbd is a keycap (Kbd).
func kbd(c *ui.Context, key string) ui.Element {
	return ui.Text(c, key).MinWidth(20).Height(20).Padding(0, 4).Radius(6).Background(theme.Selected).TextAlign(ui.Center).
		FontSize(theme.Label).LineHeight(20.0/12).FontWeight(500).TextColor(theme.Text).InsetShadow(0, -1, 0, 0, theme.Line).NoWrap()
}

// segmented is Segmented: one choice of a few, the selected ground sliding
// between them on the shared spring.
func (a *App) segmented(c *ui.Context, key any, label string, options []string, chosen *int) ui.Element {
	frame := ui.Row(c.Key(key)).Height(32).Padding(2).Radius(6).InsetShadow(0, 0, 0, 1, theme.Line).FontSize(theme.Label).LineHeight(16.0 / 12).
		Role(ui.RoleRadioGroup).Label(label).Shrink(0)
	return frame.Children(func() {
		var at ui.Rect
		for i, o := range options {
			s := ui.ButtonBase(c.Key(o)).Role(ui.RoleRadio).Label(o).Checked(i == *chosen).Height(28).Padding(0, 12).Radius(6).Center().FocusRing(false)
			if s.Clicked() {
				*chosen = i
			}
			if i == *chosen {
				at = s.LayoutBounds()
			}
			color := theme.Text2
			if i == *chosen || s.Hovered() {
				color = theme.Text
			}
			if s.FocusVisible() {
				s.FocusRing(true)
			}
			s.Children(func() {
				ui.Text(c, o).TextColor(s.AnimateColor("fg", color, a.ease(motion.Hover)))
			})
		}
		a.slideGround(c, frame, segmentMatch{key}, at, false)
	})
}

type segmentMatch struct{ key any }

func sign(v float32) float32 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// stepper is Stepper: − value +, the digits rolling to the new value.
func (a *App) stepper(c *ui.Context, key any, label string, value *float32, lo, hi float32, less, more string) ui.Element {
	frame := ui.Row(c.Key(key)).Height(32).Padding(2).Radius(6).InsetShadow(0, 0, 0, 1, theme.Line).AlignItems(ui.Center).Role(ui.RoleGroup).Label(label).Shrink(0)
	return frame.Children(func() {
		minus := a.iconButton(c, "minus", "minus", less, "", false, false).Size(28, 28)
		if *value <= lo {
			minus.Opacity(0.4)
		}
		if minus.Clicked() && *value > lo {
			*value--
		}
		plusOff := *value >= hi
		// The size shows as a share of the default, 100%: one setting sets
		// Chinese at 17 and English at 20 (DESIGN.md Reading), so a size in
		// px would tell one language's alone.
		pct := int(*value/theme.ReadingSize*100 + 0.5)
		ui.Row(c).MinWidth(44).Padding(0, 4).Justify(ui.Center).AlignItems(ui.Center).FontSize(theme.Body).LineHeight(16.0 / 13).
			Label(strconv.Itoa(pct) + "%").Children(func() {
			a.count(c, pct, 16).AccessHidden(true)
			ui.Text(c, "%").FixedLineHeight(16).AccessHidden(true)
		})
		plus := a.iconButton(c, "plus", "add", more, "", false, false).Size(28, 28)
		if plusOff {
			plus.Opacity(0.4)
		}
		if plus.Clicked() && *value < hi {
			*value++
		}
	})
}

// count is a number whose digits roll to their new value, each place a
// one-line window onto 0–9 that slides over 300 ms along the one curve and
// stops at its place (DESIGN.md Continuity); lineH is the line's height.
func (a *App) count(c *ui.Context, n int, lineH float32) ui.Element {
	s := strconv.Itoa(n)
	row := ui.Row(c).Label(s).FontFeatures(theme.Numbers).Height(lineH)
	return row.Children(func() {
		for i := range len(s) {
			d := float32(s[i] - '0')
			// Keyed by place from the right, so 9 → 10 rolls the ones and adds the tens.
			ui.Box(c.Key(len(s) - i)).Height(lineH).ClipY().AccessHidden(true).Children(func() {
				col := ui.Column(c)
				var y float32
				if a.kbd || a.still(c) {
					y = col.Jump("y", -d)
				} else {
					y = col.AnimateTo("y", -d, ui.Animation{Duration: motion.Roll, Ease: motion.Ease})
				}
				col.Translate(0, y*lineH).Children(func() {
					for k := 0; k <= 9; k++ {
						ui.Text(c, strconv.Itoa(k)).Height(lineH).FixedLineHeight(lineH)
					}
				})
			})
		}
	})
}
