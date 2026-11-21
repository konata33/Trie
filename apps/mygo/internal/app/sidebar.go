package app

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/theme"
)

// The sidebar: the tabs, the authors, and
// the tools at the foot. It changes in place as the pages cross.

var magColors = [5]ui.Color{theme.Text2, theme.Mag1, theme.Mag2, theme.Mag3, theme.Mag4}

func (a *App) sidebar(c *ui.Context) {
	side := ui.Column(c.Key("side")).Width(232).Shrink(0).FillHeight().Background(theme.Sidebar).BorderWidth(0, 1, 0, 0).
		BorderColor(theme.LineSoft).Padding(46, 12, 12, 12).ClipX().Role(ui.RoleNavigation).Label(locales.SidebarRegion())
	side.Children(func() { a.sideParts(c) })
}

func (a *App) sideParts(c *ui.Context) {
	cur, tab := a.current(), a.tabOf()
	authorOn := ""
	if cur.kind == "author" {
		authorOn = cur.id
	}
	// A ground that does not show forgets where it was (slideGround).
	if authorOn == "" {
		a.unslide(sideMatch{"authors"})
	}
	bar := ui.Row(c.Key("tabs")).Gap(4).Role(ui.RoleNavigation).Label(locales.SidebarTabs())
	bar.Children(func() {
		cr := a.carry(c, bar, tab, authorOn == "")
		tabAt, iconAt := map[string]ui.Rect{}, map[string]ui.Rect{}
		for _, t := range tabs {
			tabAt[t.kind], iconAt[t.kind] = a.tabButton(c, t.kind, t.label(), t.icon, tab == t.kind, cr)
		}
		a.favTab = iconAt["favorites"]
		// The current tab's ground: from the tab the light left to the one it goes to, on its value.
		if authorOn != "" {
			return
		}
		box, to := bar.LayoutBounds(), tabAt[cr.to]
		if to.W <= 0 || box.W <= 0 {
			return // not laid out yet
		}
		r := to
		if from, ok := tabAt[cr.from]; ok && cr.grounded {
			r = ui.Rect{X: from.X + (to.X-from.X)*cr.p, Y: to.Y, W: from.W + (to.W-from.W)*cr.p, H: to.H}
		}
		ui.Box(c.Key("ground")).Absolute().ZIndex(-1).Radius(6).Background(theme.Selected).IgnoreInput(true).
			Left(r.X-box.X).Top(r.Y-box.Y).Size(r.W, r.H)
		// The amber itself on its way, over both tabs.
		if from, ok := iconAt[cr.from]; ok {
			fromDots, toDots := accentDots(tabIcon(cr.from), from), accentDots(tabIcon(cr.to), iconAt[cr.to])
			ui.Box(c.Key("flight")).Absolute().Left(0).Top(0).Right(0).Bottom(0).ZIndex(1).IgnoreInput(true).AccessHidden(true).
				Draw(func(p *ui.Painter, _ ui.Rect) { paintFlight(p, fromDots, toDots, cr.p) })
		}
	})
	list := ui.Scroll(c.Key("authors")).Grow(1).Shrink(1).MinHeight(0).Margin(24, 0, 0, 0).Scrollbars(false)
	list.Children(func() {
		col := ui.Column(c).Gap(rowGap)
		col.Children(func() {
			var at, over ui.Rect
			for _, row := range a.sideRows(c) {
				r, hovered := a.authorRow(c, row.au, authorOn == row.au.src.ID, row.p)
				if authorOn == row.au.src.ID {
					at = r
				} else if hovered {
					over = r
				}
			}
			// The pointer's ground glides between the rows; the current one has its own.
			a.pointerGround(c, col, over, &a.layers.side)
			if authorOn != "" {
				a.slideGround(c, col, sideMatch{"authors"}, at, true)
			}
		})
	})
	ui.Row(c.Key("foot")).AlignItems(ui.Center).Gap(4).Padding(12, 4, 0, 10).Children(func() {
		// Settings: the interface's language (the sheet), lit while open and until the sheet is back in it.
		going, _ := a.landing(c, "sheet")
		open := a.layers.sheet || going
		set := a.iconBtn(c, "settings", iconOpt{icon: "settings", label: locales.Settings(), tip: locales.Settings(), small: true, beside: true, active: open, lit: open}).
			KeyShortcuts(ui.Cmd, ui.KeyComma)
		if set.Clicked() {
			a.toggleSheet(set.Bounds())
		}
		ui.Box(c).Grow(1)
		add := a.iconBtn(c, "add-subscription", iconOpt{icon: "add", label: locales.SidebarFollowBlog(), tip: locales.SidebarFollowBlog(), small: true, beside: true,
			glow: a.landed(c, "dialog")})
		if add.Clicked() {
			a.openSubscribe(add.Bounds())
		}
		a.refreshButton(c)
	})
}

// partingRow is the row of an author just let go, kept a beat (120 ms) so
// their face can lift off it before it folds away; or, let go from its menu
// (dots), while it turns into dots reeled into its face (letGoRow) and then,
// closed up, while the notice offers 撤销, to come back in.
type partingRow struct {
	au           *author
	index        int
	start, until time.Time
	dots         bool

	h      float32       // the row's height as it set off
	strips []nameStrip   // its name in strips, once laid out
	name   ui.Rect       // where the name is
	last   time.Duration // when the last dot is in the face's point
}

type sideRow struct {
	au *author
	p  *partingRow // the author was just let go
}

// sideRows are the authors' rows: the authors, and those just let go where
// they were, for a beat, or as long as 撤销 is offered when they turn into
// dots; from the keys, or under less motion but for the dots' fade, they go
// at once.
func (a *App) sideRows(c *ui.Context) []sideRow {
	rows := make([]sideRow, 0, len(a.authors)+len(a.parting))
	for _, au := range a.authors {
		rows = append(rows, sideRow{au: au})
	}
	now := c.Now()
	var going []*partingRow
	for id, p := range a.parting {
		if p.start.IsZero() {
			p.start, p.dots, p.until = now, p.dots && !a.kbd, now.Add(120*time.Millisecond)
			if p.dots {
				p.until = now.Add(motion.UndoFor)
			}
		}
		if !p.dots && (a.kbd || a.still(c)) || !now.Before(p.until) || a.writer(id) != nil {
			delete(a.parting, id)
			delete(a.faceLeft, id)
			continue
		}
		c.After(p.until.Sub(now))
		going = append(going, p)
	}
	// Each was let go from the rows left after those let go before it: they
	// go back in the other way round.
	slices.SortFunc(going, func(x, y *partingRow) int { return y.start.Compare(x.start) })
	for _, p := range going {
		rows = slices.Insert(rows, min(p.index, len(rows)), sideRow{au: p.au, p: p})
	}
	return rows
}

// tabCarry is the light that is one thing going from tab to tab (DESIGN.md
// 选中的光是一份): the tab it left, the one it goes to, and p, how far it
// is, which the ground, the two labels and the amber dots all go by;
// grounded, whether the ground showed as it set off.
type tabCarry struct {
	from, to string
	p        float32
	grounded bool
}

// carry is the light's way to tab on Carry, set off as tab changes; at once
// from the keys or under less motion. grounded says whether the ground shows.
func (a *App) carry(c *ui.Context, bar ui.Element, tab string, grounded bool) tabCarry {
	s := ui.Local(bar, "carry", func() tabCarry { return tabCarry{to: tab} })
	if s.to != tab {
		s.from, s.to, s.grounded = s.to, tab, grounded
		bar.Jump("carry", 0)
	}
	if a.kbd || a.still(c) {
		s.p = bar.Jump("carry", 1)
	} else {
		s.p = bar.AnimateTo("carry", 1, motion.Carry)
	}
	if s.p >= 0.999 {
		s.from = ""
	}
	return *s
}

// tabIcon is the icon of tab kind.
func tabIcon(kind string) string {
	for _, t := range tabs {
		if t.kind == kind {
			return t.icon
		}
	}
	return ""
}

// tabButton is a sidebar tab: its icon, and its label while current. The
// label opens and comes into focus out of 1.6px of blur, inside its own
// tab, as the light comes (cr), and closes as it leaves; the icons' amber
// stays icon-fill while the light is on its way. It returns where the frame
// before laid the tab and its icon out. 收藏's tells of the captures put away
// into it (favIcon), and pressed then, opens them.
func (a *App) tabButton(c *ui.Context, kind, label, icon string, on bool, cr tabCarry) (tab, at ui.Rect) {
	b := ui.ButtonBase(c.Key("tab-"+kind)).Label(label).Height(32).Padding(0, 8).Radius(6).AlignItems(ui.Center).FocusRing(false)
	if on {
		b.Current(ui.CurrentPage)
	}
	var news *packItem
	var done bool
	if kind == "favorites" {
		news, done = a.tabNews(c, c.Now())
		switch {
		case done && b.Hovered():
			a.capture.toldAt = c.Now() // it holds while pointed at
		case news != nil && !done:
			a.capture.tabPct = news.percent(c, c.Now())
		}
	}
	switch {
	case !b.Clicked():
	case news != nil:
		a.showShelf(c)
	default:
		a.tab(c, kind)
	}
	hover := b.Hovered() && !on
	color, bg := theme.Icon, ui.Transparent
	switch {
	case on:
		color = theme.Text
	case hover:
		color, bg = theme.Text, theme.Hover
	}
	fg := b.AnimateColor("fg", color, a.ease(motion.Hover))
	b.Background(b.AnimateColor("bg", bg, a.ease(motion.TabGround))).TextColor(fg)
	if b.FocusVisible() {
		b.FocusRing(true).FocusRingOffset(-2)
	}
	// What 收藏's tells, its tip and VoiceOver read after its name.
	switch says := ""; {
	case news != nil:
		says = locales.CaptureTabTaking(news.Name, a.capture.tabPct)
		if done {
			says = locales.CaptureDone(news.Name)
		}
		b.Value(says)
		a.tip(c, b, label+" · "+says, true)
	case !on:
		a.tip(c, b, label, true)
	}
	open := float32(0)
	switch {
	case cr.from != "" && kind == cr.to:
		open = cr.p
	case cr.from != "" && kind == cr.from:
		open = 1 - cr.p
	case on:
		open = 1
	}
	b.Children(func() {
		lit, jump := on && cr.from == "", !a.still(c)
		if kind == "favorites" {
			a.landedLight(c, favLight, 6) // a capture just landed in it
			at = a.favIcon(c, b, news, done, lit, jump, fg).LayoutBounds()
		} else {
			at = a.dotIcon(c, b, icon, 16, iconLook{lit: lit, jump: jump, ink: fg}).LayoutBounds()
		}
		if open > 0.001 {
			w := textWidth(label, theme.Body)
			ui.Box(c).Width((w + 7) * open).Height(19.5).ClipX().Children(func() {
				t := ui.Text(c, label).Absolute().Top(0).Left(7).NoWrap().Opacity(open)
				if bl := 1.6 * (1 - open); bl > 0.05 {
					t.Blur(bl)
				}
			})
		}
	})
	return b.LayoutBounds(), at
}

// textWidth is how wide s is in the system face at size.
func textWidth(s string, size float32) float32 { return textWidthIn(s, theme.Sans, size) }

// textWidthIn is how wide s is in family at size.
func textWidthIn(s, family string, size float32) float32 {
	g := ui.Shape(s, ui.Font{Family: family, Size: size})
	if len(g) == 0 {
		return 0
	}
	last := g[len(g)-1]
	return last.X + last.Advance
}

// authorRow is an author in the sidebar (AuthorRow): their face and name,
// brighter the more recently they wrote; ··· and a right click open the
// subscription menu. It returns its box and whether the pointer is on it,
// for the ground that glides between the rows (pointerGround). p is set
// while the author is being let go.
func (a *App) authorRow(c *ui.Context, au *author, on bool, p *partingRow) (ui.Rect, bool) {
	id := au.src.ID
	row := ui.Row(c.Key("author:" + id)).Radius(6).AlignItems(ui.Start)
	// A row arriving opens its own height and comes into view in 250 ms; one
	// leaving folds away in 150, the rows below closing up after it. One let
	// go from its menu has turned into dots and closed up by then: it goes
	// at once.
	tr := ui.ElementTransition{Duration: motion.State, Ease: motion.Ease, ExitDuration: 150 * time.Millisecond, ExitEase: motion.Ease,
		Width: true, Enter: &ui.Motion{Collapse: true}, Exit: &ui.Motion{Collapse: true}}
	t, dots, gone := time.Duration(0), false, false
	if p != nil && p.dots {
		tr.Exit = nil
		t, dots, gone = a.letGoRow(c, row, p)
	} else {
		a.comeBack(c, row)
	}
	row.Transition(a.tr(c, tr))
	if p != nil {
		row.IgnoreInput(true)
	}
	if gone {
		return row.LayoutBounds(), false
	}
	hover := row.Hovered()
	if row.RightClicked() && a.layers.renaming != id {
		a.layers.menu = id
	}
	row.Children(func() {
		if a.layers.renaming == id && !dots {
			a.renameField(c, au)
			return
		}
		b := ui.ButtonBase(c.Key("open")).Label(au.src.DisplayName).Grow(1).Shrink(1).MinWidth(0).MinHeight(30).Padding(5, 4, 5, 10).Gap(10).
			AlignItems(ui.Start).FocusRing(false)
		if au.latest != "" {
			b.Description(locales.SidebarLatest(au.latest))
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
			if a.current() != (page{"author", id}) && !leaving && !dots {
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
			fg := b.AnimateColor("fg", color, a.ease(motion.NameLight))
			name := sideName(c, au.src.DisplayName).Grow(1).Shrink(1).MinWidth(0).TextColor(fg)
			if dots {
				a.dissolve(c, p, t, b, chip, name, au.src.DisplayName, fg)
			} else {
				a.end(name, page{}, "name:"+id)
			}
		})
		// A failed feed: the dimmed row and the refresh mark, beside where ··· appears.
		if failure != "" || pending && a.refresh.asked[id] {
			tip := locales.SidebarRefreshFailedTip()
			if pending {
				tip = ""
			}
			retry := a.iconBtn(c, "retry", iconOpt{icon: "refresh", label: locales.SidebarRefreshAgain(au.src.DisplayName), tip: tip, small: true, beside: true,
				lit: pending, turn: a.refresh.angle(c, a.still(c))}).Margin(3, 28, 0, 0).Description(failure)
			if pending {
				retry.Label(locales.SidebarRefreshingWriter(au.src.DisplayName))
			}
			if dots && p.strips != nil {
				// It fades as the name's strips would there.
				i := (retry.LayoutBounds().X - p.name.X) / stripWidth
				retry.Opacity(1 - p.drawn(t, int(max(0, i))))
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
	return row.LayoutBounds(), hover && !on
}

const (
	rowGap     = 2 // between the authors' rows
	stripWidth = 3 // a strip of a name turning into dots
)

// rowBack is how a row let go showed as it went, for it to come back from
// there (comeBack): its opacity, how open it was, and its full height.
type rowBack struct {
	going, coming bool
	o, open, h    float32
}

// letGoRow carries on the row of an author let go from its menu (DESIGN.md
// 取消订阅), and returns how long since it set off and whether it turns into
// dots (dissolve), after which the rows below close up over it; or whether
// it is gone, closed up, an empty row kept where it was for 撤销 to bring
// it back in (comeBack). Under less motion it fades where it is instead,
// and the rows below close up at once.
func (a *App) letGoRow(c *ui.Context, row ui.Element, p *partingRow) (t time.Duration, dots, gone bool) {
	d := motion.LetGo
	t = c.Now().Sub(p.start)
	if p.h == 0 {
		p.h = row.LayoutBounds().H
	}
	back := ui.Local(row, "back", func() rowBack { return rowBack{} })
	*back = rowBack{going: true, open: 1, h: p.h}
	row.AccessHidden(true)
	still := a.still(c)
	if still && t >= d.Fade || !still && p.strips != nil && t >= p.last+d.Wait+d.Closing() {
		back.o, back.open = 0, 0
		row.Height(0).Margin(0, 0, -rowGap, 0)
		return t, false, true
	}
	c.AnimationFrame()
	if still {
		back.o = 1 - motion.Ease(through(t, d.Fade))
		row.Opacity(back.o)
		return t, false, false
	}
	if cl := d.Closed(t - p.last - d.Wait); p.strips != nil && cl > 0 {
		back.open = 1 - cl
		row.Height(p.h*back.open).Margin(0, 0, -rowGap*cl, 0).ClipY()
	}
	return t, true, false
}

// comeBack brings back a row let go and followed again before it was gone
// (撤销): it fades in where it is, and opens back to its height if it had
// begun to close, the rows below making room.
func (a *App) comeBack(c *ui.Context, row ui.Element) {
	back := ui.Local(row, "back", func() rowBack { return rowBack{} })
	if back.going {
		back.going, back.coming = false, true
		row.Jump("in", back.o)
		row.Jump("open", back.open)
	}
	if !back.coming {
		return
	}
	in, open := row.AnimateTo("in", 1, a.ease(motion.State)), float32(1)
	if a.kbd || a.still(c) {
		row.Jump("open", 1)
	} else {
		open = row.AnimateTo("open", 1, ui.Animation{Duration: motion.State, Ease: motion.Ease})
	}
	row.Opacity(in)
	if open < 1 {
		row.Height(back.h*open).Margin(0, 0, -rowGap*(1-open), 0).ClipY()
	}
	back.coming = in < 1 || open < 1
}

type stripKey int

// dissolve turns an author's name into dots reeled into their face, t after
// they were let go (DESIGN.md 取消订阅). The face draws into its point. From
// its side, each strip of the name draws in toward its ink, a little soft,
// and fades as a dot grows there, in the name's ink; the dot is then reeled
// into the point, smaller and fainter on the way. Once the last is in, the
// point goes out. b, the row's button, its face and its name place it, as
// the frame before laid them out.
func (a *App) dissolve(c *ui.Context, p *partingRow, t time.Duration, b, face, name ui.Element, text string, ink ui.Color) {
	d := motion.LetGo
	bb, fb, nb := b.LayoutBounds(), face.LayoutBounds(), name.LayoutBounds()
	if p.strips == nil {
		// A name not laid out the frame before (it was being renamed) has
		// no strips: only the face goes.
		p.strips = []nameStrip{}
		if nb.W > 0 {
			p.strips = append(p.strips, nameStrips(text, nb.W, nb.H)...)
		}
		last := 0
		for i, s := range p.strips {
			if s.ink > 0 {
				last = i
			}
		}
		p.last = time.Duration(last)*d.Stagger + d.Grow + d.Reel
	}
	p.name = nb
	nx, ny := nb.X-bb.X, nb.Y-bb.Y
	fx, fy, fr := fb.X-bb.X+fb.W/2, fb.Y-bb.Y+fb.H/2, fb.W/2
	// The face shrinks into its point and lightens to its grey; the point
	// holds till the last dot is in, and goes out without swelling.
	point := inkDot{x: fx, y: fy}
	if sa := motion.Ease(through(t, d.Face)); sa < 1 {
		s := 1 - 0.8*sa
		face.TransformOrigin(0.5, 0.5).Scale(s, s)
		point.r, point.c, point.a = fr*s, theme.Mag2, sa*sa
	} else {
		face.Opacity(0)
		out := motion.Ease(through(t-p.last, d.Out))
		point.r, point.c, point.a = 0.2*fr+(0.2-0.2*fr)*out, theme.Mag2, 1-out
	}
	// What the wave has not reached is still the name, in one piece.
	cut := float32(-4)
	for i := range p.strips {
		if t < time.Duration(i)*d.Stagger {
			break
		}
		cut = float32(i+1) * stripWidth
	}
	name.ClipInset(-4, -4, -4, cut, 0)
	var dots []inkDot
	for i, s := range p.strips {
		e := p.drawn(t, i)
		if e <= 0 {
			break
		}
		x := float32(i) * stripWidth
		if e < 1 && s.ink > 0 {
			sc := 1 - 0.85*e
			sideName(c.Key(stripKey(i)), text).Absolute().Left(nx).Top(ny).Width(nb.W).Height(nb.H).TextColor(ink).
				ClipInset(-4, nb.W-x-stripWidth, -4, x, 0).TransformOrigin(s.cx/nb.W, s.cy/nb.H).Scale(sc, sc).
				Opacity(1 - unit((e-0.55)/0.45)).Blur(e)
		}
		q := motion.Ease(through(t-time.Duration(i)*d.Stagger-d.Grow, d.Reel))
		if s.dot && q < 1 {
			dots = append(dots, inkDot{x: nx + s.cx + (fx-nx-s.cx)*q, y: ny + s.cy + (fy-ny-s.cy)*q, r: 0.85 * e * (1 - 0.3*q), a: 1 - 0.6*q, c: ink})
		}
	}
	dots = append(dots, point)
	ui.Box(c.Key("dots")).Absolute().Left(0).Top(0).Right(0).Bottom(0).IgnoreInput(true).Draw(func(pt *ui.Painter, r ui.Rect) {
		for _, d := range dots {
			if d.r > 0 && d.a > 0 {
				pt.Fill(ui.Rect{X: r.X + d.x - d.r, Y: r.Y + d.y - d.r, W: 2 * d.r, H: 2 * d.r}, d.c.Alpha(d.a), d.r)
			}
		}
	})
}

// drawn is how far strip i of the name has drawn in toward its ink, t after
// the row set off: as far as its dot has grown.
func (p *partingRow) drawn(t time.Duration, i int) float32 {
	d := motion.LetGo
	return motion.Ease(through(t-time.Duration(i)*d.Stagger, d.Grow))
}

// share is how much of d has gone by t, from 0 to 1.
func through(t, d time.Duration) float32 { return unit(float32(t) / float32(d)) }

// nameStrip is a strip of a name turning into dots: where its ink centres,
// in the name's box, how much it holds, and whether a dot grows there.
type nameStrip struct {
	cx, cy, ink float32
	dot         bool
}

// nameStrips cuts s, as sideName sets it in a box w×h, into strips from its
// left, and finds the ink of each from the glyphs: their advances, and how
// high their letters reach (glyphInk). As in the study, a strip with less
// than 15% of the median strip's ink grows no dot.
func nameStrips(s string, w, h float32) []nameStrip {
	f := ui.Font{Family: theme.Sans, Size: theme.Body}
	type span struct {
		x, w float32
		r    rune
	}
	rs, line := []rune(s), []span{}
	for _, g := range ui.Shape(s, f) {
		if g.Cluster < len(rs) {
			line = append(line, span{g.X, g.Advance, rs[g.Cluster]})
		}
	}
	if n := len(line); n > 0 && line[n-1].x+line[n-1].w > w {
		// Cut short with an ellipsis, as it shows.
		ew, k, end := textWidth("…", theme.Body), 0, float32(0)
		for k < n && line[k].x+line[k].w <= w-ew {
			end, k = line[k].x+line[k].w, k+1
		}
		line = append(line[:k], span{end, ew, '…'})
	}
	if len(line) == 0 {
		return nil
	}
	m := f.Metrics()
	base := (h-m.Ascent-m.Descent)/2 + m.Ascent
	end := line[len(line)-1].x + line[len(line)-1].w
	out := make([]nameStrip, int(math.Ceil(float64(end/stripWidth))))
	masses := make([]float32, len(out))
	for i := range out {
		x0 := float32(i) * stripWidth
		x1 := x0 + stripWidth
		var ink, mx, my float32
		for _, g := range line {
			top, bottom := glyphInk(g.r)
			l, r := max(x0, g.x+0.1*g.w), min(x1, g.x+0.9*g.w)
			if top == bottom || r <= l {
				continue
			}
			k := (r - l) * (top - bottom)
			ink, mx, my = ink+k, mx+k*(l+r)/2, my+k*(base-f.Size*(top+bottom)/2)
		}
		if ink > 0 {
			out[i] = nameStrip{cx: mx / ink, cy: my / ink, ink: ink}
		}
		masses[i] = ink
	}
	slices.Sort(masses)
	median := masses[len(masses)/2]
	for i := range out {
		out[i].dot = out[i].ink > 0.15*median
	}
	return out
}

// glyphInk is how far a letter's ink reaches above its baseline and down to
// (below it, negative), in ems: near enough to find where a strip of it
// centres. A space has none.
func glyphInk(r rune) (top, bottom float32) {
	switch {
	case unicode.IsSpace(r):
		return 0, 0
	case r >= 0x2E80: // CJK and the like fill the square
		return 0.84, -0.08
	case strings.ContainsRune("gpqy", r):
		return 0.53, -0.21
	case r == 'j':
		return 0.72, -0.21
	case strings.ContainsRune("bdfhiklt", r), unicode.IsUpper(r), unicode.IsDigit(r):
		return 0.72, 0
	case unicode.IsLower(r):
		return 0.53, 0
	case strings.ContainsRune(".,…", r):
		return 0.12, -0.06
	case strings.ContainsRune("-–—~", r):
		return 0.34, 0.24
	case strings.ContainsRune("()[]{}/\\|", r):
		return 0.78, -0.2
	case strings.ContainsRune("'\"‘’“”`", r):
		return 0.72, 0.5
	}
	return 0.6, 0
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
		in := ui.TextInputBase(c.Key("rename"), &a.layers.name).Label(locales.SidebarRenameField()).MaxLength(40).Grow(1).Height(20).Margin(0, 0, 0, -4).Padding(0, 4).
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
// then slows evenly to rest there (900 ms a turn, 100° of braking). Under less motion it stays still, its accent lit.
func (a *App) refreshButton(c *ui.Context) {
	r := &a.refresh
	label := locales.CommonRefresh()
	if r.busy {
		label = locales.CommonRefreshing()
	}
	b := a.iconBtn(c, "refresh", iconOpt{icon: "refresh", label: label, tip: locales.CommonRefresh(), small: true, lit: r.busy, beside: true,
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
