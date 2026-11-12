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

// press shrinks what the pointer presses, from the press on (DESIGN.md 按下就有反应).
func (a *App) press(e ui.Element, scale float32) {
	p := ui.Pose{}
	if e.Pressed() && !a.kbd {
		p.Scale = scale
	}
	e.AnimatePose(p, ui.Animation{Duration: motion.Press, Ease: motion.Ease})
}

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
	focus := b.FocusVisible()
	if o.kind == ghost && hover {
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
	if !still {
		a.press(b, motion.Pressed)
	}
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
// the accent; turn rotates the icon, in degrees.
type iconOpt struct {
	icon, label, tip   string
	small, active, lit bool
	disabled, beside   bool
	turn               float32
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
	color, ground := theme.Text2, ui.Transparent
	switch {
	case o.active:
		color, ground = theme.Text, theme.Selected
	case hover:
		color, ground = theme.Text, theme.Hover
	}
	b.Background(b.AnimateColor("bg", ground, a.ease(motion.Hover))).TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover)))
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	if !o.disabled {
		a.press(b, motion.Pressed)
	}
	b.Children(func() {
		ic := a.icon(c, o.icon, 16, o.lit)
		if o.turn != 0 {
			ic.TransformOrigin(0.5, 0.5).Rotate(o.turn)
		}
	})
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

// switchControl is Switch: a 32×18 track, the thumb sliding over 250 ms.
func (a *App) switchControl(c *ui.Context, key any, label string, on *bool) ui.Element {
	b := ui.SwitchBase(c.Key(key), on).Label(label).Size(32, 18).Radius(9).Shrink(0).FocusRing(false)
	track := theme.SwitchTrack
	if b.Hovered() {
		track = theme.SwitchHover
	}
	thumb := theme.Text
	if *on {
		track, thumb = theme.Accent, theme.OnAccent
	}
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	b.Background(b.AnimateColor("track", track, a.ease(motion.Hover)))
	x := float32(0)
	if *on {
		x = 14
	}
	return b.Children(func() {
		t := ui.Box(c).Absolute().Top(2).Left(2).Size(14, 14).Radius(7).Background(b.AnimateColor("thumb", thumb, a.ease(motion.State)))
		if a.kbd || a.still(c) {
			t.Translate(t.Jump("x", x), 0)
		} else {
			t.Translate(t.AnimateTo("x", x, ui.Animation{Duration: motion.State, Ease: motion.Ease}), 0)
		}
	})
}

// segmented is Segmented: one choice of a few, the selected ground sliding
// between them on the shared spring.
func (a *App) segmented(c *ui.Context, key any, label string, options []string, chosen *int) ui.Element {
	frame := ui.Row(c.Key(key)).Height(32).Padding(2).Radius(6).InsetShadow(0, 0, 0, 1, theme.Line).FontSize(theme.Label).LineHeight(16.0 / 12).
		Role(ui.RoleRadioGroup).Label(label).Shrink(0)
	return frame.Children(func() {
		for i, o := range options {
			s := ui.ButtonBase(c.Key(o)).Role(ui.RoleRadio).Label(o).Checked(i == *chosen).Height(28).Padding(0, 12).Radius(6).Center().FocusRing(false)
			if s.Clicked() {
				*chosen = i
			}
			color := theme.Text2
			if i == *chosen || s.Hovered() {
				color = theme.Text
			}
			if s.FocusVisible() {
				s.FocusRing(true)
			}
			s.Children(func() {
				if i == *chosen {
					f := motion.Follow
					t := ui.ElementTransition{Animation: &f, Match: segmentMatch{key}}
					ui.Box(c).Absolute().Fill().Radius(6).Background(theme.Selected).IgnoreInput(true).Transition(a.tr(c, t))
				}
				ui.Text(c, o).TextColor(s.AnimateColor("fg", color, a.ease(motion.Hover)))
			})
		}
	})
}

type segmentMatch struct{ key any }

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
		a.count(c, int(*value), 16).MinWidth(24).Padding(0, 4).FontSize(theme.Body).LineHeight(16.0 / 13).TextAlign(ui.Center).Justify(ui.Center)
		plus := a.iconButton(c, "plus", "add", more, "", false, false).Size(28, 28)
		if plusOff {
			plus.Opacity(0.4)
		}
		if plus.Clicked() && *value < hi {
			*value++
		}
	})
}

// count is a number whose digits roll to their new value, each
// place a one-line window onto 0–9 that slides over 300 ms. lineH is the
// line's height.
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
