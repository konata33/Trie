// Command motion-spike shows Trie's three crossings (apps/desktop/
// DESIGN.md Motion: 移焦 between pages, layers grown out of their buttons,
// 底色先动 into an article) drawn by MyGo with the durations and curves of
// internal/motion. The shell is a stand-in at the Electron app's sizes,
// with the fixture's titles, authors and paragraphs.
//
//	go run ./cmd/motion-spike -fixture ../../.context/mygo/fixtures/articles.json
//	go run ./cmd/motion-spike -fixture … -frames ../../.context/mygo/motion-spike
//	MYGO_FRAME_STATS=all go run ./cmd/motion-spike -fixture … -autoplay
package main

import (
	"flag"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/theme"
)

type article struct {
	id, title, author string
	at                time.Time
	paras             []para
}

type para struct {
	text    string
	heading bool
}

type author struct {
	id, name string
	arts     []*article
}

// page is what the content area shows: a tab, an author's page or an
// article.
type page struct{ kind, id string }

var tabs = []struct{ kind, label string }{{"home", "首页"}, {"explore", "探索"}, {"favorites", "收藏"}, {"clips", "摘录"}}

// move is a change of page. The page left is built once more after it is
// asked for, so that it leaves as the change says (Crossing.Leave), and
// the next build shows the next page.
type move struct {
	to    page
	depth motion.Depth
	kbd   bool
	from  []page // where going back goes from the next page
	chip  string // the author whose sidebar chip takes the face back
}

// What both pages show, matched across them: an author's face (MyGo flies
// it), and the words that motion.Words carries.
type faceMatch struct{ id string }

type leaving struct {
	page page
	from []page // where its back button went
	move *move
	at   time.Time // the frame it was left in
}

type sideMatch struct{}

// flight is a text both pages show on its way: key names it on both
// pages, at and to are the pages it leaves and goes to (the sidebar is the
// zero page), and old and new draw it as each side does.
type flight struct {
	key      string
	text     string
	at, to   page
	old, new func(*ui.Context, string) ui.Element
	w        motion.Words
}

type app struct {
	arts    []*article
	authors []*author

	page page
	from []page // where going back goes, the nearest last
	next *move  // asked for in this build, shown in the next
	came *move  // how the page showing came
	// left is the page left, built on through the frame that shows the
	// next one: MyGo builds a frame again after a click, and a page's exit
	// is the one its last frame gave it, so the page left must still be
	// built, with its exit, in the frame the change shows in, and gone
	// from the next.
	left *leaving

	kbd    bool // the last input was a key: what it does shows at once
	reduce bool // the spike's own switch for less motion

	lay     *motion.Lay
	layRow  string // the article whose row the ground goes back to
	flights []*flight
	run     uint64
	chips   map[string]int  // a sidebar chip is built anew to take its face back
	chipFly map[string]bool // while the face flies back into it

	dialog, sheet, menu, pop bool
	opener                   ui.Rect // the button the dialog or the sheet grew out of
	layerIn, layerOut        time.Duration

	scroll map[string]*ui.ScrollState
	area   ui.Rect // the content area, as the frame before laid it out
	theme  *ui.Theme

	label string // -frames: the time drawn in the corner
	auto  string // -autoplay: the control pressed in the next build
	took  bool   // it was, and the build after it must follow
}

func newApp(arts []*article) *app {
	a := &app{arts: arts, page: page{kind: "home"}, theme: theme.UI(), chips: map[string]int{}, chipFly: map[string]bool{},
		scroll: map[string]*ui.ScrollState{}}
	by := map[string]*author{}
	for _, art := range arts {
		au := by[art.author]
		if au == nil {
			au = &author{id: art.author, name: art.author}
			by[art.author] = au
			a.authors = append(a.authors, au)
		}
		au.arts = append(au.arts, art)
	}
	return a
}

func (a *app) article(id string) *article {
	for _, art := range a.arts {
		if art.id == id {
			return art
		}
	}
	return nil
}

func (a *app) writer(id string) *author {
	for _, au := range a.authors {
		if au.id == id {
			return au
		}
	}
	return nil
}

// still reports whether motion should move nothing: the desktop asks for
// less motion, or the spike's switch does.
func (a *app) still(c *ui.Context) bool { return a.reduce || c.Preferences().ReduceMotion }

// tr returns t as the moment asks: at once after a key, without moving
// under less motion.
func (a *app) tr(c *ui.Context, t ui.ElementTransition) ui.ElementTransition {
	switch {
	case a.kbd:
		return motion.Instant(t)
	case a.still(c):
		return motion.Still(t)
	}
	return t
}

// observe keeps what the last input was: a key, or the pointer (motion.ts
// lastInputWasKeyboard).
func (a *app) observe(ev ui.InputEvent) {
	switch ev.Kind {
	case ui.InputKeyDown:
		a.kbd = true
	case ui.InputPointerDown:
		a.kbd = false
	}
}

// take reports whether -autoplay presses the control named so.
func (a *app) take(name string) bool {
	if a.auto == name {
		a.auto, a.took = "", true
		return true
	}
	return false
}

func (a *app) view(c *ui.Context) {
	c.SetTheme(a.theme)
	if l := a.left; l != nil && c.Now().After(l.at) {
		a.left = nil // a frame later: its copy goes as its exit says
	}
	if m := a.next; m != nil {
		a.left = &leaving{page: a.page, from: a.from, move: m, at: c.Now()}
		a.next, a.page, a.from, a.came = nil, m.to, m.from, m
		if m.kbd {
			a.left = nil // gone at once, without a copy
			c.DisableAnimations()
		}
		if m.chip != "" {
			a.chips[m.chip]++ // built anew, it appears, and the face flies into it
			a.chipFly[m.chip] = true
		}
	}
	if len(a.from) > 0 && (c.Shortcut(0, ui.KeyEscape) || a.take("back")) {
		a.back(c)
	}
	for i, k := range []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4} {
		if c.Shortcut(ui.Cmd, k) {
			a.tab(c, tabs[i].kind)
		}
	}
	if c.Shortcut(ui.Shift, ui.KeySlash) {
		a.openLayer(&a.sheet, ui.Rect{}, motion.PopIn, motion.PopOut)
	}
	root := ui.Row(c).Fill().Background(theme.Window).Font("system-ui, PingFang SC").FontSize(13).ObserveInput(a.observe)
	// The page behind a dialog goes out of focus while it shows.
	target, d := float32(0), a.layerOut
	if a.dialog {
		target, d = motion.Behind, a.layerIn
	}
	var behind float32
	if a.kbd || d == 0 {
		behind = root.Jump("behind", target)
	} else {
		behind = root.AnimateTo("behind", target, motion.Fade(d))
	}
	if behind > 0.01 {
		root.Blur(behind)
	}
	root.Children(func() {
		a.sidebar(c)
		a.stage(c)
	})
	a.layers(c)
	ui.Overlay(c, func() {
		live := a.flights[:0]
		for _, f := range a.flights {
			if f.w.From.W <= 0 {
				continue // its first end was not built: it goes with its page
			}
			oldStyle, newStyle, text := f.old, f.new, f.text
			if f.w.View(c, f.key, func() ui.Element { return oldStyle(c, text) }, func() ui.Element { return newStyle(c, text) }) {
				live = append(live, f)
			}
		}
		a.flights = live
		if a.label != "" {
			ui.Text(c, a.label).Absolute().Left(244).Bottom(8).FontSize(11).TextColor(theme.Faint).FontFeatures(theme.Numbers).IgnoreInput(true)
		}
	})
	if a.took {
		// A press without input: no frame follows it on its own, as one
		// follows a click.
		a.took = false
		c.Invalidate()
	}
}

// go asks for page to, the depth deciding how the content area crosses to
// it; the next build shows it.
func (a *app) goTo(c *ui.Context, to page, depth motion.Depth, from []page) *move {
	if a.next != nil || to == a.page {
		return nil
	}
	// A new crossing ends the words of the one before, as a view
	// transition skips the one it interrupts; a ground melts on.
	a.run, a.flights = a.run+1, a.flights[:0]
	a.next = &move{to: to, depth: depth, kbd: a.kbd, from: from}
	c.Invalidate()
	return a.next
}

func (a *app) tab(c *ui.Context, kind string) { a.goTo(c, page{kind: kind}, motion.Side, nil) }

// open goes into an article from its row: the row's ground spreads into
// the article's page and the title travels to the headline (底色先动).
func (a *app) open(c *ui.Context, art *article, row ui.Rect) {
	at := a.page
	m := a.goTo(c, page{"reader", art.id}, motion.Deep, append(a.from[:len(a.from):len(a.from)], at))
	if m == nil || m.kbd {
		return
	}
	still := a.still(c)
	if !still {
		a.fly("title:"+art.id, art.title, at, m.to, rowTitle, headline, 32.0/15, motion.Deep)
	}
	a.lay, a.layRow = &motion.Lay{Ground: motion.Spread, From: row, To: a.paper(), Run: a.run, Still: still}, ""
}

func (a *app) openAuthor(c *ui.Context, au *author) {
	at := a.page
	if at.kind == "reader" && len(a.from) > 0 {
		at = a.from[len(a.from)-1]
	}
	a.chipFly[au.id] = false // the chip stays where it is as the face leaves it
	m := a.goTo(c, page{"author", au.id}, motion.Deep, []page{at})
	if m != nil && !m.kbd && !a.still(c) {
		a.fly("name:"+au.id, au.name, page{}, m.to, sideName, authorName, 24.0/13, motion.Deep)
	}
}

// back goes to the page the content area came from, the way it came.
func (a *app) back(c *ui.Context) {
	if len(a.from) == 0 {
		return
	}
	left, to := a.page, a.from[len(a.from)-1]
	m := a.goTo(c, to, motion.Back, a.from[:len(a.from)-1:len(a.from)-1])
	if m == nil || m.kbd {
		return
	}
	still := a.still(c)
	switch left.kind {
	case "reader":
		art := a.article(left.id)
		if !still && a.lists(to, art) {
			a.fly("title:"+art.id, art.title, left, to, headline, rowTitle, 15.0/32, motion.Back)
		}
		// The page rises under the article and shrinks into its row,
		// once the row is laid out; without one, it melts where it is.
		a.lay, a.layRow = &motion.Lay{Ground: motion.Gather, From: a.paper(), Run: a.run, Still: still}, art.id
	case "author":
		if !still {
			m.chip = left.id
			au := a.writer(left.id)
			a.fly("name:"+au.id, au.name, left, page{}, authorName, sideName, 13.0/24, motion.Back)
		}
	}
}

func (a *app) fly(key, text string, at, to page, old, new func(*ui.Context, string) ui.Element, ratio float32, d motion.Depth) {
	for i, f := range a.flights {
		if f.key == key {
			a.flights = append(a.flights[:i], a.flights[i+1:]...)
			break
		}
	}
	a.flights = append(a.flights, &flight{key: key, text: text, at: at, to: to, old: old, new: new,
		w: motion.Words{Ratio: ratio, Travel: d.Crossing().Travel}})
}

// end is a text a flight may leave or reach, built on page where (the
// sidebar is the zero page): it gives the flight its box, and hides while
// the words fly, but for the sidebar's, which stays as they leave it.
func (a *app) end(e ui.Element, where page, key string) ui.Element {
	for _, f := range a.flights {
		if f.key != key {
			continue
		}
		switch where {
		case f.at:
			if f.w.From.W <= 0 {
				f.w.From = e.LayoutBounds()
			}
			if where != (page{}) {
				e.Opacity(0)
			}
		case f.to:
			if f.w.To.W <= 0 {
				f.w.To = e.LayoutBounds()
			}
			e.Opacity(0)
		}
	}
	return e
}

// lists reports whether page p shows a row for art.
func (a *app) lists(p page, art *article) bool {
	switch p.kind {
	case "home":
		for _, x := range a.latest() {
			if x == art {
				return true
			}
		}
	case "author":
		return p.id == art.author
	}
	return false
}

func (a *app) latest() []*article { return a.arts[:min(10, len(a.arts))] }

// paper is the page an article's ground spreads into: the reading column
// and 24 more on each side, from under the toolbar to the window's foot.
func (a *app) paper() ui.Rect {
	w := min(a.area.W, reader.Column+64) - 64
	return motion.Paper(ui.Rect{X: a.area.X + (a.area.W-w)/2, Y: a.area.Y + 52, W: w, H: a.area.H - 52})
}

func (a *app) openLayer(open *bool, from ui.Rect, in, out time.Duration) {
	*open, a.opener, a.layerIn, a.layerOut = true, from, in, out
}

// The texts both pages show, as each side draws them.
func rowTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(15).LineHeight(24.0 / 15).FontWeight(500).TextColor(theme.Text).MaxLines(2)
}

func headline(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(32).LineHeight(1.3).LetterSpacing(-0.012 * 32).FontWeight(600).TextColor(theme.Text).Balance()
}

func sideName(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(13).LineHeight(1.5).TextColor(theme.Text2).MaxLines(1)
}

func authorName(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(24).LineHeight(32.0 / 24).LetterSpacing(-0.24).FontWeight(600).TextColor(theme.Text)
}

// current is the page the sidebar marks: an article's is the list it was
// opened from.
func (a *app) current() page {
	if a.page.kind == "reader" && len(a.from) > 0 {
		return a.from[len(a.from)-1]
	}
	return a.page
}

func (a *app) sidebar(c *ui.Context) {
	cur := a.current()
	ui.Column(c).Width(232).FillHeight().Background(theme.Sidebar).BorderWidth(0, 1, 0, 0).BorderColor(theme.LineSoft).
		Padding(46, 12, 12, 12).Children(func() {
		ui.Row(c).Gap(4).Children(func() {
			for _, t := range tabs {
				b := ui.ButtonBase(c.Key("tab-"+t.kind)).Height(32).Padding(0, 8).Radius(6).AlignItems(ui.Center).Label(t.label)
				if b.Clicked() || a.take("tab:"+t.kind) {
					a.tab(c, t.kind)
				}
				on := cur.kind == t.kind
				if !on && b.Hovered() {
					b.Background(theme.Hover)
				}
				a.press(c, b, motion.PressedTab)
				b.Children(func() {
					if on {
						a.glide(c)
					}
					color := theme.Text2
					if on {
						color = theme.Text
					}
					ui.Text(c, t.label).TextColor(color)
				})
			}
		})
		ui.Column(c).Margin(24, 0, 0, 0).Gap(2).Grow(1).Children(func() {
			for _, au := range a.authors {
				a.authorRow(c, au, cur)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Switch(c, &a.reduce).Label("减弱动态效果")
			ui.Text(c, "减弱动态效果").TextColor(theme.Text2)
		})
	})
}

// glide is the current tab's or author's ground, one shape that slides
// between them (Sidebar's Glide).
func (a *app) glide(c *ui.Context) {
	f := motion.Follow
	ui.Box(c.Key("glide")).Absolute().Fill().Radius(6).Background(theme.Selected).IgnoreInput(true).
		Transition(a.tr(c, ui.ElementTransition{Animation: &f, Match: sideMatch{}}))
}

// press shrinks what the pointer presses, from the press on.
func (a *app) press(c *ui.Context, e ui.Element, scale float32) {
	p := ui.Pose{}
	if e.Pressed() {
		p.Scale = scale
	}
	e.AnimatePose(p, ui.Animation{Duration: motion.Press, Ease: motion.Ease})
}

func (a *app) authorRow(c *ui.Context, au *author, cur page) {
	b := ui.ButtonBase(c.Key("au-"+au.id)).FillWidth().Padding(5, 4, 5, 10).Gap(10).Radius(6).AlignItems(ui.Start).Label(au.name)
	if b.Clicked() || a.take("author:"+au.id) {
		a.openAuthor(c, au)
	}
	on := cur == page{"author", au.id}
	if !on && b.Hovered() {
		b.Background(theme.Hover)
	}
	b.Children(func() {
		if on {
			a.glide(c)
		}
		chip := ui.Box(c.Key("chip-"+au.id+"-"+strconv.Itoa(a.chips[au.id]))).Size(18, 18).Radius(6).Background(theme.Selected).
			Center().Margin(1, 0, 0, 0)
		chip.Children(func() { ui.Text(c, initial(au.name)).FontSize(12).FontWeight(600).TextColor(theme.Text2) })
		// One element has the face's Match at a time: the chip, but while
		// its author's page shows. It flies only as the face comes back.
		if a.page != (page{"author", au.id}) {
			t := ui.ElementTransition{Match: faceMatch{au.id}}
			if a.chipFly[au.id] {
				t = motion.Back.Crossing().Shared(faceMatch{au.id})
				t.ScaleContent = true
			}
			chip.Transition(a.tr(c, t))
		}
		name := sideName(c, au.name).Grow(1).Shrink(1)
		if on {
			name.TextColor(theme.Text)
		}
		a.end(name, page{}, "name:"+au.id)
	})
}

func initial(s string) string {
	r, _ := utf8.DecodeRuneInString(s)
	return strings.ToUpper(string(r))
}

// stage is the content area: the ground under the pages, and the page.
func (a *app) stage(c *ui.Context) {
	st := ui.Box(c.Key("stage")).Grow(1).FillHeight().ClipX().ClipY()
	a.area = st.LayoutBounds()
	st.Children(func() {
		if l := a.lay; l != nil && !l.View(c, ui.Point{X: a.area.X, Y: a.area.Y}) {
			a.lay = nil
		}
		// The page left, under the next, lets go as the change says.
		if l := a.left; l != nil {
			t := l.move.depth.Crossing().Leave()
			if a.still(c) {
				t = motion.Still(t)
			}
			ui.Column(c.Key(l.page.kind + ":" + l.page.id)).Absolute().Fill().ZIndex(1).
				Children(func() { a.pageView(c, l.page, l.from) }).Transition(t)
		}
		p := a.page
		pg := ui.Column(c.Key(p.kind + ":" + p.id)).Absolute().Fill().ZIndex(1)
		pg.Children(func() { a.pageView(c, p, a.from) })
		if m := a.came; m != nil {
			t := m.depth.Crossing().Come()
			if m.kbd {
				t = motion.Instant(t)
			} else if a.still(c) {
				t = motion.Still(t)
			}
			pg.Transition(t)
		}
	})
}

// pageView builds page p, whose back button goes to the last of from.
func (a *app) pageView(c *ui.Context, p page, from []page) {
	switch p.kind {
	case "reader":
		a.readerPage(c, a.article(p.id), from)
	case "author":
		a.authorPage(c, a.writer(p.id), from)
	case "home":
		a.band(c, func() { ui.Text(c, "刚刚更新").TextColor(theme.Muted) })
		a.column(c, p, func() {
			a.chapter(c, "最近", len(a.latest()))
			a.rows(c, p, a.latest(), true)
		})
	default:
		a.band(c, func() {})
		a.column(c, p, func() { a.tabPage(c, p.kind) })
	}
}

// band is the 52px band every page starts with (ui/page.tsx PageBand), with
// the layers' buttons on its right.
func (a *app) band(c *ui.Context, left func()) {
	ui.Row(c).Height(52).FillWidth().Padding(0, 16, 0, 20).Gap(4).AlignItems(ui.Center).Children(func() {
		left()
		ui.Box(c).Grow(1)
		a.tools(c)
	})
}

// column is a page's reading column (ui/page.tsx page.column: 720 at most,
// 40 inside), scrolling under its band.
func (a *app) column(c *ui.Context, p page, fn func()) {
	key := p.kind + ":" + p.id
	s := a.scroll[key]
	if s == nil {
		s = &ui.ScrollState{}
		a.scroll[key] = s
	}
	ui.Scroll(c.Key("scroll")).TrackScroll(s).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().MaxWidth(720).AlignSelf(ui.Center).Padding(0, 40, 140, 40).Children(fn)
	})
}

// chapter heads a run of rows, as the author page's years do.
func (a *app) chapter(c *ui.Context, title string, n int) {
	ui.Row(c).Gap(16).AlignItems(ui.Center).Margin(16, 0, 4, 0).Children(func() {
		ui.Text(c, title).FontSize(20).LineHeight(1.4).FontWeight(600).TextColor(theme.Text2)
		ui.Box(c).Grow(1).Height(1).Background(theme.LineSoft)
		ui.Text(c, strconv.Itoa(n)).FontSize(12).TextColor(theme.Muted).FontFeatures(theme.Numbers)
	})
}

// rows lists articles as the author page does (author.css .arow): a
// tear-off date, the title and the start of the article; 12 past the
// column on each side, as the row is the cursor's ground.
func (a *app) rows(c *ui.Context, p page, arts []*article, withAuthor bool) {
	ui.Column(c).Margin(0, -12, 0, -12).Children(func() {
		for i, art := range arts {
			if i > 0 {
				ui.Box(c).Height(1).Margin(0, 12, 0, 72).Background(theme.LineSoft)
			}
			a.row(c, p, art, withAuthor)
		}
	})
}

func (a *app) row(c *ui.Context, p page, art *article, withAuthor bool) {
	b := ui.ButtonBase(c.Key("row-"+art.id)).FillWidth().Padding(18, 12).Gap(14).Radius(10).AlignItems(ui.Start).Label(art.title)
	if b.Clicked() || a.take("open:"+art.id) {
		a.open(c, art, b.LayoutBounds())
	}
	if b.Hovered() {
		b.Background(theme.Hover)
	}
	// Going back, the ground shrinks into this row once it is laid out.
	if l := a.lay; l != nil && a.layRow == art.id && p == a.page && l.To.W <= 0 {
		l.To = b.LayoutBounds()
	}
	b.Children(func() {
		ui.Column(c).Width(34).AlignItems(ui.End).FontFeatures(theme.Numbers).Children(func() {
			ui.Text(c, strconv.Itoa(art.at.Day())).FontSize(20).LineHeight(1.2).TextColor(theme.Text)
			ui.Text(c, strconv.Itoa(int(art.at.Month()))+"月").FontSize(12).LineHeight(16.0 / 12).TextColor(theme.Muted)
		})
		ui.Box(c).Width(12)
		ui.Column(c).Grow(1).Shrink(1).Children(func() {
			a.end(rowTitle(c, art.title), p, "title:"+art.id)
			if len(art.paras) > 0 {
				ui.Text(c, art.paras[0].text).FontSize(13).LineHeight(21.0/13).TextColor(theme.Text2).MaxLines(2).Margin(4, 0, 0, 0)
			}
			if withAuthor {
				ui.Text(c, art.author).FontSize(12).LineHeight(1.5).TextColor(theme.Muted).Margin(6, 0, 0, 0)
			}
		})
	})
}

func (a *app) authorPage(c *ui.Context, au *author, from []page) {
	p := page{"author", au.id}
	a.band(c, func() { a.backButton(c, from) })
	a.column(c, p, func() {
		ui.Row(c).Gap(18).AlignItems(ui.Start).Children(func() {
			if l := a.left; l != nil && l.page == p && l.move.chip == au.id {
				// The page left, built on for its exit, keeps the face's place
				// but not the face: gone from it, the face flies to its chip
				// as a copy of itself, and the page's copy shows none.
				ui.Box(c.Key("face-gone")).Size(56, 56)
			} else {
				face := ui.Box(c.Key("face")).Size(56, 56).Radius(28).Background(theme.Selected).Center().
					Shadow(0, 0, 0, 1, theme.LineSoft).Shadow(0, 6, 18, 0, ui.RGBA(0, 0, 0, 0.35))
				face.Children(func() { ui.Text(c, initial(au.name)).FontSize(22).FontWeight(600).TextColor(theme.Text2) })
				if a.came != nil {
					t := a.came.depth.Crossing().Shared(faceMatch{au.id})
					t.ScaleContent = true
					face.Transition(a.tr(c, t))
				}
			}
			ui.Column(c).Grow(1).Shrink(1).Padding(2, 0, 0, 0).Children(func() {
				a.end(authorName(c, au.name), p, "name:"+au.id)
				newest := au.arts[0].at
				ui.Text(c, strconv.Itoa(len(au.arts))+" 篇 · 最近 "+strconv.Itoa(int(newest.Month()))+"月"+strconv.Itoa(newest.Day())+"日").
					FontSize(12).LineHeight(1.5).TextColor(theme.Muted).Margin(8, 0, 0, 0).FontFeatures(theme.Numbers)
			})
		})
		ui.Box(c).Height(32)
		a.chapter(c, strconv.Itoa(au.arts[0].at.Year()), len(au.arts))
		a.rows(c, p, au.arts, false)
	})
}

func (a *app) readerPage(c *ui.Context, art *article, from []page) {
	p := page{"reader", art.id}
	// The toolbar first, so that its back button is seen before the
	// headline is built; it paints over the article, on its own ground.
	ui.Row(c).Absolute().Top(0).Left(0).Right(0).Height(52).ZIndex(2).Justify(ui.Center).Children(func() {
		ui.Row(c).FillWidth().MaxWidth(reader.Column+16).Height(52).Padding(14, 0, 6, 0).Gap(4).AlignItems(ui.Center).
			Background(theme.Window).Children(func() {
			a.backButton(c, from)
			ui.Box(c).Grow(1)
			a.tools(c)
		})
	})
	key := "reader:" + art.id
	s := a.scroll[key]
	if s == nil {
		s = &ui.ScrollState{}
		a.scroll[key] = s
	}
	ui.Scroll(c.Key("scroll")).TrackScroll(s).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().MaxWidth(reader.Column+64).AlignSelf(ui.Center).Padding(80, 32, 180, 32).Children(func() {
			a.end(headline(c, art.title), p, "title:"+art.id)
			ui.Row(c).Margin(12, 0, 0, 0).Gap(8).FontSize(13).TextColor(theme.Muted).FontFeatures(theme.Numbers).Children(func() {
				ui.Text(c, art.author).TextColor(theme.Text2)
				ui.Text(c, "·")
				ui.Text(c, art.at.Format("2006年1月2日"))
			})
			ui.Column(c).Margin(36, 0, 0, 0).Gap(17).FontSize(17).LineHeight(1.85).TextColor(theme.Text).Children(func() {
				for _, pr := range art.paras {
					if pr.heading {
						ui.Text(c, pr.text).FontSize(21).LineHeight(1.45).FontWeight(600).Margin(12, 0, 0, 0)
					} else {
						ui.Text(c, pr.text)
					}
				}
			})
		})
	})
}

func (a *app) backButton(c *ui.Context, from []page) {
	label := "‹ 返回"
	if len(from) > 0 {
		to := from[len(from)-1]
		switch to.kind {
		case "author":
			label = "‹ " + to.id
		default:
			for _, t := range tabs {
				if t.kind == to.kind {
					label = "‹ " + t.label
				}
			}
		}
	}
	if a.button(c, label).Clicked() {
		a.back(c)
	}
}

// tabPage stands in for the other tabs' pages, each laid out its own way
// so that a crossing between them reads.
func (a *app) tabPage(c *ui.Context, kind string) {
	switch kind {
	case "explore":
		a.chapter(c, "设计", 6)
		ui.Row(c).Wrap().Gap(16).Margin(12, 0, 0, 0).Children(func() {
			for i, art := range a.arts[:min(6, len(a.arts))] {
				ui.Column(c.Key(i)).Width(304).Height(170).Padding(16).Radius(12).Background(theme.Surface).Justify(ui.End).Children(func() {
					ui.Text(c, art.author).FontSize(12).TextColor(theme.Muted)
					ui.Text(c, art.title).FontSize(15).FontWeight(500).MaxLines(2).Margin(4, 0, 0, 0)
				})
			}
		})
	case "favorites":
		a.chapter(c, "收藏的作者", len(a.authors))
		for _, au := range a.authors {
			ui.Row(c.Key(au.id)).Gap(16).AlignItems(ui.Center).Padding(16, 0).Children(func() {
				ui.Box(c).Size(40, 40).Radius(20).Background(theme.Selected)
				ui.Text(c, au.name).FontSize(20).FontWeight(600)
			})
		}
	case "clips":
		a.chapter(c, "摘录", 3)
		for i, art := range a.arts[:min(3, len(a.arts))] {
			if len(art.paras) == 0 {
				continue
			}
			ui.Column(c.Key(i)).Margin(24, 0, 0, 0).Padding(0, 0, 0, 16).BorderWidth(0, 0, 0, 2).BorderColor(theme.AccentSoft).Children(func() {
				ui.Text(c, art.paras[0].text).FontSize(17).LineHeight(1.7).MaxLines(4)
				ui.Text(c, art.author+" ·「"+art.title+"」").FontSize(12).TextColor(theme.Muted).MaxLines(1).Margin(8, 0, 0, 0)
			})
		}
	}
}

// button is a quiet text button of the bands, which a press shrinks.
func (a *app) button(c *ui.Context, label string) ui.Element {
	b := ui.ButtonBase(c.Key("btn-"+label)).Height(28).Padding(0, 10).Radius(6).AlignItems(ui.Center).Label(label)
	if b.Hovered() {
		b.Background(theme.Hover)
	}
	a.press(c, b, motion.Pressed)
	b.Children(func() { ui.Text(c, label).TextColor(theme.Text2) })
	return b
}

// tools are the layers' buttons: a popover, a menu, a dialog, the shortcut
// sheet and a notice.
func (a *app) tools(c *ui.Context) {
	aa := a.button(c, "Aa")
	if aa.Clicked() || a.take("pop") {
		a.pop = true
	}
	ui.PopoverBase(c, aa, &a.pop, func(panel ui.Element) {
		panel.AttachTo(aa, ui.AnchorBottomEnd, ui.AnchorTopEnd).Margin(8, 0, 0, 0).AttachedOrigin().
			Transition(a.tr(c, motion.Popover())).ObserveInput(a.observe).
			Width(240).Padding(16).Radius(12).Background(theme.Elevated).Border(1, theme.Line).
			Shadow(0, 0, 0, 1, ui.RGBA(0, 0, 0, 0.2)).Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.5)).Gap(12)
		ui.Text(c, "正文字号").FontSize(12).TextColor(theme.Muted)
		ui.Row(c).Gap(8).Children(func() {
			for _, s := range []string{"小", "中", "大"} {
				ui.Box(c.Key(s)).Size(64, 32).Radius(6).Background(theme.Field).Center().Children(func() { ui.Text(c, s) })
			}
		})
	})
	m := ui.MenuBase(c, &a.menu)
	m.Trigger.Height(28).Padding(0, 10).Radius(6).AlignItems(ui.Center).Label("更多").Children(func() {
		ui.Text(c, "···").TextColor(theme.Text2)
	})
	if a.take("menu") {
		a.menu = true
	}
	m.Popup(func(panel ui.Element) {
		panel.AttachTo(m.Trigger, ui.AnchorBottomEnd, ui.AnchorTopEnd).Margin(4, 0, 0, 0).AttachedOrigin().
			Transition(a.tr(c, motion.Menu())).ObserveInput(a.observe).
			Width(176).Padding(4).Radius(12).Background(theme.Elevated).Border(1, theme.Line).
			Shadow(0, 0, 0, 1, ui.RGBA(0, 0, 0, 0.2)).Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.5))
		for _, s := range []string{"在浏览器打开", "复制链接", "取消收藏"} {
			it := m.Item().Height(32).Padding(0, 8).Radius(6).AlignItems(ui.Center)
			if it.Highlighted() {
				it.Background(theme.Hover)
			}
			it.Children(func() { ui.Text(c, s) })
		}
	})
	if d := a.button(c, "对话框"); d.Clicked() || a.take("dialog") {
		a.openLayer(&a.dialog, d.Bounds(), motion.DialogIn, motion.DialogOut)
	}
	if d := a.button(c, "快捷键"); d.Clicked() || a.take("sheet") {
		a.openLayer(&a.sheet, d.Bounds(), motion.PopIn, motion.PopOut)
	}
	if a.button(c, "通知").Clicked() || a.take("toast") {
		title := "已取消收藏"
		if len(a.arts) > 0 {
			title += "「" + a.arts[0].title + "」"
		}
		c.AddToast(ui.Toast{Title: title, Action: "撤销", Timeout: 8 * time.Second})
	}
}

// layers are the dialog, the shortcut sheet and the notices.
func (a *app) layers(c *ui.Context) {
	a.centred(c, "dialog", &a.dialog, motion.Dialog(), func(panel ui.Element) {
		// DESIGN.md dialog: elevated, 12px corners, the dialog shadow, 24 inside (Dialog.tsx).
		panel.Width(480).Padding(24).Shadow(0, 0, 0, 1, theme.Line).Shadow(0, 24, 64, 0, ui.RGBA(0, 0, 0, 0.6))
	}, func() {
		ui.Text(c, "取消订阅这位作者？").FontSize(20).FontWeight(600).LineHeight(1.4)
		ui.Text(c, "已经存下的文章、收藏和摘录都会留在资料库里，之后可以重新订阅。").TextColor(theme.Text2).LineHeight(1.6).Margin(8, 0, 0, 0)
		ui.Row(c).Justify(ui.End).Gap(8).Margin(24, 0, 0, 0).Children(func() {
			if ui.Button(c, "取消").Clicked() || a.take("dialog-close") {
				a.dialog = false
			}
			if ui.PrimaryButton(c, "取消订阅").Clicked() {
				a.dialog = false
			}
		})
	})
	a.centred(c, "sheet", &a.sheet, motion.Sheet(), func(panel ui.Element) {
		// A floating panel, 16 inside (ShortcutSheet.tsx).
		panel.Width(360).Padding(16).InsetShadow(0, 0, 0, 1, theme.Line).
			Shadow(0, 0, 0, 1, ui.RGBA(0, 0, 0, 0.2)).Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.5))
	}, func() {
		ui.Text(c, "快捷键").FontSize(15).FontWeight(600).LineHeight(1.5)
		for _, k := range [][2]string{{"Tab / ⇧Tab", "在条目之间移动"}, {"回车", "打开文章或作者"}, {"Esc", "返回"}, {"⌘1 – ⌘4", "切换页签"}, {"?", "这张表"}} {
			ui.Row(c.Key(k[0])).Margin(12, 0, 0, 0).Children(func() {
				ui.Text(c, k[0]).Width(112).TextColor(theme.Text2).FontFeatures(theme.Numbers)
				ui.Text(c, k[1])
			})
		}
		if a.take("sheet-close") {
			a.sheet = false
		}
	})
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(0, 0, 24, 0).Justify(ui.End).AlignItems(ui.Center).Gap(8)
		for _, t := range toasts {
			tb := ui.ToastBase(c, t)
			tb.Root.Row().Gap(8).Padding(8, 8, 8, 16).MaxWidth(560).Radius(12).Background(theme.Elevated).AlignItems(ui.Center).
				Shadow(0, 0, 0, 1, theme.Line).Shadow(0, 0, 0, 2, ui.RGBA(0, 0, 0, 0.2)).Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.5)).
				Transition(a.tr(c, motion.Toast())).Children(func() {
				ui.Text(c, t.Title).MaxLines(1).Shrink(1)
				if t.Action != "" {
					tb.ActionButton().Height(28).Padding(0, 10).Radius(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, t.Action).TextColor(theme.Accent)
					})
				}
			})
		}
	})
}

// centred is a dialog over the shade: it grows out of the button that
// opened it and shrinks back into it, the shade fading on its timing. The
// backdrop carries the motion, so that the panel and its content go as
// one; the shade is its own layer under it, so that it only fades.
func (a *app) centred(c *ui.Context, key string, open *bool, t ui.ElementTransition, look func(ui.Element), content func()) {
	if *open {
		ui.Overlay(c, func() {
			ui.Box(c.Key("shade-" + key)).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(theme.Shade).IgnoreInput(true).
				Transition(a.tr(c, motion.Shade(a.layerIn, a.layerOut)))
		})
	}
	ui.DialogBase(c, open, func(back, panel ui.Element) {
		w, h := c.Size()
		back.TransformOrigin(motion.Origin(ui.Rect{W: w, H: h}, a.opener)).ObserveInput(a.observe).Transition(a.tr(c, t))
		panel.Radius(12).Background(theme.Elevated)
		look(panel)
		content()
	})
}

// load reads the fixture's articles, newest first: titles, authors and the
// first paragraphs of their bodies.
func load(path string) ([]*article, error) {
	src, err := reader.LoadFixture(path)
	if err != nil {
		return nil, err
	}
	var out []*article
	for _, s := range src {
		at, _ := time.Parse(time.RFC3339, s.PublishedAt)
		art := &article{id: s.ID, title: s.Title, author: s.SourceName, at: at}
		if ct, err := s.Content(); err == nil && ct.Document != nil {
			for _, u := range ct.Document.Units {
				if (u.Kind == "paragraph" || u.Kind == "heading") && strings.TrimSpace(u.Text) != "" && len(art.paras) < 40 {
					art.paras = append(art.paras, para{text: strings.TrimSpace(u.Text), heading: u.Kind == "heading"})
				}
			}
		}
		if art.title == "" {
			art.title = "未提取到标题"
		}
		out = append(out, art)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.After(out[j].at) })
	return out, nil
}

func main() {
	fixture := flag.String("fixture", "", "the articles.json the fixture exported")
	frames := flag.String("frames", "", "write each crossing's frames here, without a window, and quit")
	only := flag.String("only", "", "with -frames, only the crossings whose names start with this")
	autoplay := flag.Bool("autoplay", false, "play every crossing once in a window and quit; MYGO_FRAME_STATS=all logs each frame")
	flag.Parse()
	if *fixture == "" {
		log.Fatal("motion-spike: -fixture is required")
	}
	arts, err := load(*fixture)
	if err != nil {
		log.Fatal(err)
	}
	if *frames != "" {
		if err := writeFrames(arts, *frames, *only); err != nil {
			log.Fatal(err)
		}
		return
	}
	a := newApp(arts)
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title: "Trie 转场小样", Width: 1280, Height: 820, MinWidth: 900, MinHeight: 560,
			Content: ui.View(a.view),
		})
		if *autoplay {
			go play(a, win)
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
