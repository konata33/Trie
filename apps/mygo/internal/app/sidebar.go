package app

import (
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/motion"
	"trie/internal/theme"
)

// The sidebar (Sidebar.tsx, styles.css .side): the tabs, the authors, and
// the tools at the foot. It changes in place as the pages cross.

var magColors = [5]ui.Color{theme.Text2, theme.Mag1, theme.Mag2, theme.Mag3, theme.Mag4}

func (a *App) sidebar(c *ui.Context) {
	side := ui.Column(c.Key("side")).Width(232).Shrink(0).FillHeight().Background(theme.Sidebar).BorderWidth(0, 1, 0, 0).
		BorderColor(theme.LineSoft).Padding(46, 12, 12, 12).ClipX().Role(ui.RoleNavigation).Label("侧栏")
	// 专注阅读: the sidebar slides out to the left as the content area
	// widens, in one motion (styles.css .reader-focused).
	shut := float32(0)
	if a.focused && a.page.kind == "reader" {
		shut = 1
	}
	var k float32
	if a.kbd {
		k = side.Jump("shut", shut)
	} else {
		k = side.AnimateTo("shut", shut, ui.Animation{Duration: motion.Focus, Ease: motion.Ease})
	}
	if k > 0 {
		side.Width(232 * (1 - k)).MinWidth(0)
		side.Children(func() {
			ui.Column(c).Width(232).FillHeight().Translate(-232*k*map[bool]float32{true: 0, false: 1}[a.still(c)], 0).
				Opacity(side.AnimateTo("fade", 1-shut, a.ease(map[bool]time.Duration{true: motion.FocusOut, false: motion.FocusIn}[shut == 1]))).Children(func() { a.sideParts(c) })
		})
		if k >= 0.999 {
			side.Invisible()
		}
		return
	}
	side.Children(func() { a.sideParts(c) })
}

func (a *App) sideParts(c *ui.Context) {
	cur, tab := a.current(), a.tabOf()
	authorOn := ""
	if cur.kind == "author" {
		authorOn = cur.id
	}
	ui.Row(c.Key("tabs")).Gap(4).Role(ui.RoleNavigation).Label("主导航").Children(func() {
		for _, t := range tabs {
			a.tabButton(c, t.kind, t.label, t.icon, tab == t.kind, tab == t.kind && authorOn == "")
		}
	})
	list := ui.Scroll(c.Key("authors")).Grow(1).Shrink(1).MinHeight(0).Margin(24, 0, 0, 0).Scrollbars(false)
	list.Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			for _, row := range a.sideRows(c) {
				a.authorRow(c, row.au, authorOn == row.au.src.ID, row.parting)
			}
		})
	})
	ui.Row(c.Key("foot")).AlignItems(ui.Center).Gap(4).Padding(12, 4, 0, 10).Children(func() {
		keys := ui.ButtonBase(c.Key("keys")).Label("快捷键 ?").KeyShortcuts(0, ui.KeySlash).FocusRing(false)
		if keys.Clicked() {
			a.toggleSheet(keys.Bounds())
		}
		color := theme.Muted
		if keys.Hovered() {
			color = theme.Text2
		}
		if keys.FocusVisible() {
			keys.FocusRing(true)
		}
		keys.Children(func() {
			ui.Text(c, "快捷键 ?").FontSize(theme.Label).LineHeight(18.0 / 12).TextColor(keys.AnimateColor("fg", color, a.ease(motion.Hover)))
		})
		ui.Box(c).Grow(1)
		add := a.iconBtn(c, "add-subscription", iconOpt{icon: "add", label: "添加订阅", tip: "添加订阅", small: true, beside: true})
		if add.Clicked() {
			a.openSubscribe(add.Bounds())
		}
		a.refreshButton(c)
	})
}

// partingRow is the row of an author just let go, kept a beat (120 ms) so
// their face can lift off it before it folds away.
type partingRow struct {
	au    *author
	index int
	until time.Time
}

type sideRow struct {
	au      *author
	parting bool
}

// sideRows are the authors' rows: the authors, and those just let go where
// they were, for a beat; from the keys, or under less motion, they go at once.
func (a *App) sideRows(c *ui.Context) []sideRow {
	rows := make([]sideRow, 0, len(a.authors)+len(a.parting))
	for _, au := range a.authors {
		rows = append(rows, sideRow{au: au})
	}
	now := c.Now()
	for id, p := range a.parting {
		if p.until.IsZero() {
			p.until = now.Add(120 * time.Millisecond)
		}
		if a.kbd || a.still(c) || !now.Before(p.until) || a.writer(id) != nil {
			delete(a.parting, id)
			delete(a.faceLeft, id)
			continue
		}
		c.After(p.until.Sub(now))
		at := min(p.index, len(rows))
		rows = append(rows[:at], append([]sideRow{{au: p.au, parting: true}}, rows[at:]...)...)
	}
	return rows
}

// tabButton is a sidebar tab: its icon, and its label while current; the
// current one's ground glides between the tabs (Glide).
func (a *App) tabButton(c *ui.Context, kind, label, icon string, on, ground bool) {
	b := ui.ButtonBase(c.Key("tab-"+kind)).Label(label).Height(32).Padding(0, 8).Radius(6).AlignItems(ui.Center).FocusRing(false)
	if on {
		b.Current(ui.CurrentPage)
	}
	if b.Clicked() {
		a.tab(c, kind)
	}
	hover := b.Hovered() && !on
	color, bg := theme.Text2, ui.Transparent
	switch {
	case on:
		color = theme.Text
	case hover:
		color, bg = theme.Text, theme.Hover
	}
	b.Background(b.AnimateColor("bg", bg, a.ease(motion.TabGround))).TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover)))
	if b.FocusVisible() {
		b.FocusRing(true).FocusRingOffset(-2)
	}
	a.press(b, motion.PressedTab)
	if !on {
		a.tip(c, b, label, true)
	}
	// Explore becoming current: the compass swings out a little and settles
	// back upright, damped (Sidebar.tsx), not from the keys, nor under less motion.
	var turn float32
	if kind == "explore" {
		if on && !a.kbd && !a.still(c) {
			turn = b.Keyframes("swing", []ui.Keyframe{{Value: 0}, {At: motion.SwingRise, Value: 12, Ease: motion.Ease},
				{At: motion.SwingIn, Value: 0, Ease: motion.Swing}}, ui.KeyframeOptions{Run: uint64(a.exploreRuns(on))})
		}
	}
	b.Children(func() {
		if ground {
			a.glide(c, "tabs")
		}
		ic := a.icon(c, icon, 16, on)
		if turn != 0 {
			ic.TransformOrigin(0.5, 0.5).Rotate(turn)
		}
		// The label opens as the tab becomes current: 220 ms, its words
		// fading in over 160 (styles.css .tab .tl).
		open := float32(0)
		if on {
			open = 1
		}
		var k float32
		if a.kbd {
			k = b.Jump("open", open)
		} else {
			k = b.AnimateTo("open", open, ui.Animation{Duration: motion.TabOpen, Ease: motion.Ease})
		}
		fade := b.AnimateTo("labelFade", open, a.ease(motion.TabWords))
		if k > 0.001 {
			w := textWidth(label, theme.Body)
			ui.Box(c).Width((w + 7) * k).Height(19.5).ClipX().Children(func() {
				ui.Text(c, label).Absolute().Top(0).Left(7 * k).NoWrap().Opacity(fade)
			})
		}
	})
}

// exploreRuns counts the times Explore became current, to replay its swing.
func (a *App) exploreRuns(on bool) int {
	if on && !a.refresh.exploreWas {
		a.refresh.exploreRun++
	}
	a.refresh.exploreWas = on
	return a.refresh.exploreRun
}

// glide is the current tab's or author's ground, one shape that slides
// between them (Glide): the shared spring, at once from the keys.
func (a *App) glide(c *ui.Context, list string) {
	f := motion.Follow
	ui.Box(c.Key("glide")).Absolute().Fill().Radius(6).Background(theme.Selected).IgnoreInput(true).
		Transition(a.tr(c, ui.ElementTransition{Animation: &f, Match: sideMatch{list}}))
}

// textWidth is how wide s is in the system face at size.
func textWidth(s string, size float32) float32 {
	g := ui.Shape(s, ui.Font{Family: theme.Sans, Size: size})
	if len(g) == 0 {
		return 0
	}
	last := g[len(g)-1]
	return last.X + last.Advance
}

// authorRow is an author in the sidebar (AuthorRow): their face and name,
// brighter the more recently they wrote; ··· and a right click open the
// subscription menu.
func (a *App) authorRow(c *ui.Context, au *author, on, parting bool) {
	id := au.src.ID
	row := ui.Row(c.Key("author:" + id)).Radius(6).AlignItems(ui.Start)
	// A row arriving opens its own height and comes into view in 250 ms; one
	// leaving folds away in 150, the rows below closing up after it.
	row.Transition(a.tr(c, ui.ElementTransition{Duration: motion.State, Ease: motion.Ease, ExitDuration: 150 * time.Millisecond, ExitEase: motion.Ease,
		Width: true, Enter: &ui.Motion{Collapse: true}, Exit: &ui.Motion{Collapse: true}}))
	if parting {
		row.IgnoreInput(true)
	}
	hover := row.Hovered()
	if !on && hover {
		row.Background(theme.Hover)
	}
	if row.RightClicked() && a.layers.renaming != id {
		a.layers.menu = id
	}
	row.Children(func() {
		if on {
			a.glide(c, "authors")
		}
		if a.layers.renaming == id {
			a.renameField(c, au)
			return
		}
		b := ui.ButtonBase(c.Key("open")).Label(au.src.DisplayName).Grow(1).Shrink(1).MinWidth(0).MinHeight(30).Padding(5, 4, 5, 10).Gap(10).
			AlignItems(ui.Start).FocusRing(false)
		if au.latest != "" {
			b.Description("最近一篇：" + au.latest)
		}
		if on {
			b.Current(ui.CurrentPage)
		}
		if b.Clicked() {
			a.openAuthor(c, id)
		}
		// Resting on an author lays their page out, unseen (warm.go).
		if b.Hovered() && !a.kbd && !on {
			if a.hoverAuthor != id {
				a.hoverAuthor, a.hoverAuthorAt = id, c.Now()
			}
		} else if a.hoverAuthor == id && !a.pinned {
			a.hoverAuthor = ""
		}
		if b.FocusVisible() {
			row.FocusRing(true).FocusRingOffset(-2)
		}
		pending, failure := a.refresh.pending[id], a.failure(au)
		if pending || failure != "" {
			b.Opacity(b.AnimateTo("dim", 0.45, a.ease(motion.RowDim)))
		} else {
			b.Opacity(b.AnimateTo("dim", 1, a.ease(motion.RowDim)))
		}
		lit := on || hover || b.FocusVisible()
		b.Children(func() {
			chip := a.chip(c, au, lit)
			// One element has the face's Match at a time: the chip, but while
			// its author's page, or an article of it, shows, and as that page
			// leaves for another (the face goes with it). It flies only as the
			// face comes back.
			leaving := a.left != nil && a.left.page == (page{"author", id}) && !a.chipFly[id]
			if a.current() != (page{"author", id}) && !leaving {
				t := ui.ElementTransition{Match: faceMatch{id}}
				if a.chipFly[id] {
					t = motion.Back.Crossing().Shared(faceMatch{id})
					t.ScaleContent = true
				}
				chip.Transition(a.tr(c, t))
			}
			color := magColors[magnitude(au.days, a.fresh[id])]
			if lit {
				color = theme.Text
			}
			name := sideName(c, au.src.DisplayName).Grow(1).Shrink(1).MinWidth(0).TextColor(b.AnimateColor("fg", color, a.ease(motion.NameLight)))
			a.end(name, page{}, "name:"+id)
		})
		// A failed feed: the dimmed row and the refresh mark, beside where ··· appears.
		if failure != "" || pending && a.refresh.asked[id] {
			tip := "刷新失败，点击重试"
			if pending {
				tip = ""
			}
			retry := a.iconBtn(c, "retry", iconOpt{icon: "refresh", label: "重新刷新 " + au.src.DisplayName, tip: tip, small: true, beside: true,
				lit: pending, turn: a.refresh.angle(c, a.still(c))}).Margin(3, 28, 0, 0).Description(failure)
			if pending {
				retry.Label("正在刷新 " + au.src.DisplayName)
			}
			if retry.Clicked() && !pending {
				a.refresh.ask(id)
				a.refreshFeeds(id)
			}
		}
		more := a.sourceMenu(c, "more", au.src, true).Absolute().Top(3).Right(4)
		show := hover || more.FocusVisible() || a.layers.menu == id
		mc, mb := theme.Muted, ui.Transparent
		if more.Hovered() {
			mc, mb = theme.Text, theme.Selected
		}
		more.Opacity(more.AnimateTo("show", map[bool]float32{true: 1, false: 0}[show], a.ease(motion.Reveal))).
			Background(mb).TextColor(mc)
	})
}

// chip is an author's face in the sidebar: their site's icon, toned for the
// dark interface and dimmed at rest, or their initial.
func (a *App) chip(c *ui.Context, au *author, lit bool) ui.Element {
	ic := a.iconOf(au.src)
	box := ui.Box(c.Key("chip-"+au.src.ID+"-"+strconv.Itoa(a.chips[au.src.ID]))).Size(18, 18).Radius(9).Background(theme.Selected).
		Margin(1, 0, 0, 0).Center().ClipX().ClipY().Shrink(0)
	// A face on its way here from Explore: the place stays hidden until it lands, then lights once.
	a.chipAt[au.src.ID] = box.LayoutBounds()
	if a.flyingTo("chip:"+au.src.ID) || a.faceLeft[au.src.ID] {
		box.Opacity(0)
	}
	return box.Children(func() {
		if ic == nil {
			ui.Text(c, initial(au.src.DisplayName)).FontSize(theme.Label).FontWeight(600).TextColor(theme.Text2).LineHeight(1)
			a.landedLight(c, "chip:"+au.src.ID, 9)
			return
		}
		pad := float32(0)
		if ic.inset {
			pad = 18 * 0.12
		}
		ui.Image(c, ic.toned).Fit(ui.Cover).Absolute().Left(pad).Top(pad).Right(pad).Bottom(pad).Radius(9)
		// At rest the face is dimmed (saturate(0.55) brightness(0.92)); it
		// clears over 220 ms as the row is pointed at or current.
		if dim := box.AnimateTo("dim", map[bool]float32{true: 0, false: 1}[lit], a.ease(motion.RowDim)); dim > 0.001 {
			ui.Image(c, ic.dim).Fit(ui.Cover).Absolute().Left(pad).Top(pad).Right(pad).Bottom(pad).Radius(9).Opacity(dim)
		}
		a.flare(c, au.src.ID)
		a.landedLight(c, "chip:"+au.src.ID, 9)
	})
}

// renameField renames an author in their row, as in Finder: Enter or
// leaving the field keeps the name, Esc keeps the old one.
func (a *App) renameField(c *ui.Context, au *author) {
	ui.Row(c).Grow(1).MinHeight(30).Padding(5, 4, 5, 10).Gap(10).AlignItems(ui.Start).Children(func() {
		a.chip(c, au, true)
		in := ui.TextInputBase(c.Key("rename"), &a.layers.name).Label("名称").MaxLength(40).Grow(1).Height(20).Margin(0, 0, 0, -4).Padding(0, 4).
			Radius(6).Background(theme.Field).LineHeight(20.0 / 13).AutoFocus().FocusRing(true).FocusRingOffset(0)
		switch {
		case in.Submitted():
			a.rename(au.src, a.layers.name)
		case in.OverlayShortcut(0, ui.KeyEscape):
			a.layers.renaming = ""
		case in.FocusLeft():
			a.rename(au.src, a.layers.name)
		}
	})
}

func (a *App) rename(src *library.Source, name string) {
	a.layers.renaming = ""
	if name = trimSpace(name); name != "" && name != src.DisplayName {
		a.mutate(library.RenameSource{ID: src.ID, Name: name}, nil)
	}
}

// refreshButton is the foot's refresh: its icon turns only while a refresh
// runs, and when it ends keeps its speed until a little short of upright,
// then slows evenly to rest there (useTurning: 900 ms a turn, 100° of
// braking). Under less motion it stays still, its accent lit.
func (a *App) refreshButton(c *ui.Context) {
	r := &a.refresh
	label := "刷新"
	if r.busy {
		label = "正在刷新"
	}
	b := a.iconBtn(c, "refresh", iconOpt{icon: "refresh", label: label, tip: "刷新", small: true, lit: r.busy, beside: true,
		turn: r.angle(c, a.still(c)), disabled: len(a.authors) == 0})
	b.Busy(r.busy)
	if b.Clicked() && !r.busy && len(a.authors) > 0 {
		a.refreshFeeds("")
	}
}

const (
	turnPeriod = motion.Turn
	brakeDeg   = 100.0
)

// angle is where the refresh icon is turned to now, asking for frames while it moves.
func (r *refreshState) angle(c *ui.Context, still bool) float32 {
	now := c.Now()
	if still {
		return 0
	}
	if r.busy {
		if r.spinAt.IsZero() {
			r.spinAt, r.stopAt = now.Add(-time.Duration(r.rest/360*float64(turnPeriod))), time.Time{}
		}
		c.AnimationFrame()
		return float32(float64(now.Sub(r.spinAt)) / float64(turnPeriod) * 360)
	}
	if r.spinAt.IsZero() {
		return 0
	}
	if r.stopAt.IsZero() {
		r.stopAt = now
		r.from = float64(now.Sub(r.spinAt)) / float64(turnPeriod) * 360
	}
	from := r.from
	rest := float64(int((from+brakeDeg)/360)+1) * 360
	if int(from+brakeDeg)%360 == 0 {
		rest = from + brakeDeg
	}
	coast := (rest - brakeDeg - from) / 360 * float64(turnPeriod)
	brake := 2 * brakeDeg / 360 * float64(turnPeriod)
	t := float64(now.Sub(r.stopAt))
	switch {
	case t < coast:
		c.AnimationFrame()
		return float32(from + t/float64(turnPeriod)*360)
	case t < coast+brake:
		c.AnimationFrame()
		k := (t - coast) / brake
		return float32(rest - brakeDeg + brakeDeg*float64(motion.Brake(float32(k))))
	}
	r.spinAt, r.rest = time.Time{}, 0
	return 0
}
