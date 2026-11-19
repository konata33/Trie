package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/theme"
)

// The content area and its crossings (DESIGN.md Motion 移焦): the page
// left lets go out of focus while the next comes into focus, and what both
// show travels from one to the other. Sideways for the
// sidebar's tabs, deep into an article or an author, back out of one. From
// the keyboard it just changes; under less motion nothing moves or flies.

// move is a change of page, asked for in one build and shown in the next.
type move struct {
	to    page
	depth motion.Depth
	kbd   bool
	from  []page // where going back goes from the next page
	chip  string // the author whose sidebar chip takes the face back
	run   uint64 // tells this crossing from the one before
	door  string // the kind whose door opens into its page, or closes back (doorGround)
}

// leaving is the page left, built on through the frame that shows the
// next one, so that it leaves as its last frame's exit says.
type leaving struct {
	page page
	from []page
	move *move
	at   time.Time
}

// What both pages show, matched across them: an author's face (MyGo flies
// it), and the words motion.Words carries.
type faceMatch struct{ id string }

type sideMatch struct{ list string }

// flight is a text both pages show on its way: key names it on both pages,
// at and to are the pages it leaves and goes to (the sidebar is the zero
// page), and old and new draw it as each side does.
type flight struct {
	key      string
	text     string
	at, to   page
	old, new func(*ui.Context, string) ui.Element
	w        motion.Words
}

// goTo asks for page to, the depth deciding how the content area crosses
// to it; the next build shows it.
func (a *App) goTo(c *ui.Context, to page, depth motion.Depth, from []page) *move {
	if a.next != nil || to == a.page {
		return nil
	}
	// A new crossing ends the words of the one before, as a view
	// transition skips the one it interrupts; a ground melts on.
	a.run, a.flights = a.run+1, a.flights[:0]
	a.next = &move{to: to, depth: depth, kbd: a.kbd, from: from, run: a.run}
	a.layers.closeAll()
	c.Invalidate()
	return a.next
}

// current is the list the page belongs to: an article's is the list it
// was opened from.
func (a *App) current() page {
	if a.page.kind == "reader" && len(a.from) > 0 {
		return a.from[len(a.from)-1]
	}
	return a.page
}

// tabOf is the sidebar tab the page is under: an author's page or a kind's
// is the tab's it was opened from, Home's from the sidebar.
func (a *App) tabOf() string {
	switch p := a.current(); p.kind {
	case "author", "reader", "kind":
		if len(a.from) > 0 && a.from[0].kind != "author" && a.from[0].kind != "reader" {
			return a.from[0].kind
		}
		return "home"
	default:
		return p.kind
	}
}

// backTo is the page going back goes to: Home's, from the sidebar's.
func (a *App) backTo() page {
	if n := len(a.from); n > 0 {
		return a.from[n-1]
	}
	return page{kind: "home"}
}

// tab goes to a sidebar tab; on it already, an open article closes at once.
// 收藏 comes back to the author it showed last.
func (a *App) tab(c *ui.Context, kind string) {
	cur := a.current()
	if cur.kind == kind {
		switch {
		case a.page.kind == "reader" && kind == "home" && a.trials[a.page.id] != nil:
			a.back(c) // a post of Explore's opened from 首页 goes back the way its own back does (closeTrial)
		case a.page.kind == "reader":
			a.goTo(c, cur, motion.Back, a.from[:len(a.from)-1]).noMotion()
		}
		return
	}
	if kind == "favorites" && a.fav != nil && a.fav.place != "" {
		a.goTo(c, page{"favorites", a.fav.place}, motion.Side, []page{{kind: "favorites"}})
		return
	}
	a.goTo(c, page{kind: kind}, motion.Side, nil)
}

// noMotion makes a move show at once.
func (m *move) noMotion() {
	if m != nil {
		m.kbd = true
	}
}

// openAuthor goes to an author's page from the sidebar: their face and
// name travel from the row to the head of the page.
func (a *App) openAuthor(c *ui.Context, id string) {
	if cur := a.current(); cur == (page{"author", id}) {
		if a.page.kind == "reader" {
			a.goTo(c, cur, motion.Back, a.from[:len(a.from)-1]).noMotion()
		}
		return
	}
	au := a.writer(id)
	if au == nil {
		return
	}
	a.chipFly[id] = false // the chip stays where it is as the face leaves it
	a.hoverAuthor = ""
	m := a.goTo(c, page{"author", id}, motion.Deep, []page{{kind: "home"}})
	if m != nil && !m.kbd && !a.still(c) {
		a.fly("name:"+id, au.src.DisplayName, page{}, m.to, sideName, authorName, float32(theme.Headline)/theme.Body, motion.Deep)
	}
}

// open goes into an article from its card on an author's page: the card's
// ground spreads into the article's page and the title travels to the
// headline as the card last drew it (底色先动).
func (a *App) open(c *ui.Context, id string, card ui.Rect) {
	look, ok := a.looks[id]
	if !ok {
		look = titleLook{tileTitle(false), theme.Subtitle}
	}
	a.openAs(c, id, card, look.style, look.size)
}

// openAs opens an article from row, whose title is drawn as style draws it
// at size: a list's row, a card in 收藏, a passage's card in 摘录.
func (a *App) openAs(c *ui.Context, id string, row ui.Rect, style func(*ui.Context, string) ui.Element, size float32) {
	// A post an unsubscribed person's page lists is kept in the library as it opens.
	if a.article(id) == nil {
		if t := a.listedPost(id); t != nil {
			a.keep(&t.entry, t.post)
		}
	}
	art := a.article(id)
	if art == nil {
		return
	}
	at := a.page
	m := a.goTo(c, page{"reader", id}, motion.Deep, append(a.from[:len(a.from):len(a.from)], at))
	if m == nil {
		return
	}
	a.reading(id) // starts reading it, if it is not ready
	a.hover, a.pinned = "", false
	if m.kbd {
		return
	}
	still := a.still(c)
	if !still {
		a.fly("title:"+id, titleOf(art.Title), at, m.to, style, a.headline, theme.Headline/size, motion.Deep)
	}
	a.lay, a.layRow = &motion.Lay{Ground: motion.Spread, From: row, To: a.paper(), Run: a.run, Still: still}, ""
}

// layInto gives the ground going back from an article the row r of
// article id on page p, once that row is laid out: it shrinks into it.
func (a *App) layInto(p page, id string, r ui.Rect) {
	if g := a.lay; g != nil && a.layRow == id && p == a.page && g.To.W <= 0 && r.W > 0 {
		g.To = r
	}
}

// back goes to the page the content area came from, the way it came.
func (a *App) back(c *ui.Context) {
	if len(a.from) == 0 {
		return
	}
	left, to := a.page, a.from[len(a.from)-1]
	m := a.goTo(c, to, motion.Back, a.from[:len(a.from)-1:len(a.from)-1])
	switch {
	case m == nil || !m.kbd:
	case to.kind == "home":
		a.homeSt().focus = left.id // the article's row, or the post's opener
	case to.kind == "explore" && (left.kind == "author" || left.kind == "kind"):
		a.ex.focus = a.ex.opener
	case (to.kind == "explore" || to.kind == "kind") && left.kind == "reader":
		if art := a.article(left.id); art != nil {
			a.ex.focus = "post:" + art.URL
		}
	}
	if m == nil || m.kbd {
		return
	}
	still := a.still(c)
	switch left.kind {
	case "reader":
		if to.kind == "kind" {
			// Back to a kind's wall, the page gathers into the work's tile (kindTile).
			a.lay, a.layRow = &motion.Lay{Ground: motion.Gather, From: a.paper(), Run: a.run, Still: still}, left.id
			break
		}
		if to.kind == "explore" || a.trials[left.id] != nil {
			// Back to Explore, or to an Explore post's row on 首页, it just crosses; from the keys, the focus goes back to the post.
			break
		}
		art := a.article(left.id)
		if look, ok := a.rowLook(to, left.id); ok && art != nil && !still {
			a.fly("title:"+art.ID, titleOf(art.Title), left, to, a.headline, look.style, look.size/theme.Headline, motion.Back)
		}
		// The page rises under the article and shrinks into its row, once
		// the row is laid out; without one, it melts where it is.
		a.lay, a.layRow = &motion.Lay{Ground: motion.Gather, From: a.paper(), Run: a.run, Still: still}, left.id
	case "kind":
		// Back to Explore the name goes home to its door, as the head closes back into it (kindShare).
		if !still && to.kind == "explore" {
			m.door = left.id
			a.fly("kind:"+left.id, kindName(left.id), left, to, kindTitle, doorNameAtRest, float32(theme.Item)/theme.Headline, motion.Back)
		}
	case "author":
		// Back to the sidebar the name goes home to its row; elsewhere the page just crosses.
		if !still && to.kind == "home" && a.exPersonOf(left.id) == nil {
			if au := a.writer(left.id); au != nil {
				m.chip = left.id
				a.fly("name:"+left.id, au.src.DisplayName, left, page{}, authorName, sideName, float32(theme.Body)/theme.Headline, motion.Back)
			}
		}
	}
}

// titleLook is how a row draws an article's title, at what size.
type titleLook struct {
	style func(*ui.Context, string) ui.Element
	size  float32
}

// rowLook is how page p draws article id's title, when it shows it: a card
// on an author's page or 收藏 as its last build drew it. 摘录 shows
// passages, not titles: the title goes with its page there.
func (a *App) rowLook(p page, id string) (titleLook, bool) {
	switch p.kind {
	case "author":
		look, ok := a.looks[id]
		return look, ok && a.showsRow(p, id)
	case "favorites":
		look, ok := a.looks[id]
		art := a.article(id)
		return look, ok && art != nil && art.Favorite
	case "home":
		look, ok := a.homeSt().shows[id]
		return look, ok
	}
	return titleLook{}, false
}

// showsRow reports whether page p shows a card for article id.
func (a *App) showsRow(p page, id string) bool {
	if p.kind != "author" {
		return false
	}
	if au := a.writer(p.id); au != nil {
		for _, art := range au.arts {
			if art.ID == id {
				return true
			}
		}
	}
	return false
}

func (a *App) fly(key, text string, at, to page, old, new func(*ui.Context, string) ui.Element, ratio float32, d motion.Depth) *flight {
	for i, f := range a.flights {
		if f.key == key {
			a.flights = append(a.flights[:i], a.flights[i+1:]...)
			break
		}
	}
	f := &flight{key: key, text: text, at: at, to: to, old: old, new: new, w: motion.Words{Ratio: ratio, Travel: d.Crossing().Travel}}
	a.flights = append(a.flights, f)
	return f
}

// end is a text a flight may leave or reach, built on page where (the
// sidebar is the zero page): it gives the flight its box, and hides while
// the words fly, but for the sidebar's, which stays as they leave it.
func (a *App) end(e ui.Element, where page, key string) ui.Element {
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

// paper is the page an article's ground spreads into: the reading column
// and 24 more on each side, from under the toolbar to the window's foot.
func (a *App) paper() ui.Rect {
	w := a.column()
	col := min(a.area.W, w+80) - 80
	return motion.Paper(ui.Rect{X: a.area.X + (a.area.W-col)/2, Y: a.area.Y + 52, W: col, H: a.area.H - 52})
}

// column is the reading column's width.
func (a *App) column() float32 {
	if a.wide {
		return theme.ReaderWide
	}
	return theme.ReaderColumn
}

// The texts both pages show, as each side draws them.

// headline is the reader's (reader.Headline), at the width the reader
// gives it.
func (a *App) headline(c *ui.Context, s string) ui.Element {
	return reader.Headline(c, s, min(a.area.W-80, a.column())).TextColor(theme.Text)
}

func sideName(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2).SingleLine().Ellipsis("…")
}

// authorName is the name at the head of an author's page, 32, its lines 40
// high as Chrome sets them (reader.Lines), in the room the column leaves
// between the portrait and the menu.
func authorName(c *ui.Context, s string) ui.Element {
	return reader.Lines(c, s, theme.ReaderColumn-portrait-20-40, ui.Font{Family: theme.Sans, Size: theme.Headline, Weight: 650, LineHeight: 40.0 / 32}, -0.012*theme.Headline, false).
		TextColor(theme.Text)
}

func titleOf(s string) string {
	if s == "" {
		return locales.CommonUntitled()
	}
	return s
}

// stage is the content area: the ground under the pages, and the page.
func (a *App) stage(c *ui.Context) {
	st := ui.Box(c.Key("stage")).Grow(1).Shrink(1).MinWidth(0).FillHeight().ClipX().ClipY()
	a.area = st.LayoutBounds()
	st.Children(func() {
		if l := a.lay; l != nil && !l.View(c, ui.Point{X: a.area.X, Y: a.area.Y}) {
			a.lay = nil
		}
		// The page left, under the next, lets go as the change says; through a door it is built on as it goes
		// (doorLeave), a kind's page closing back into its door going over Explore, which its ground uncovers
		// (doorGround).
		if l := a.left; l != nil {
			lp := ui.Column(c.Key(l.page.kind + ":" + l.page.id)).Absolute().Fill()
			if l.move.door != "" {
				a.doorLeave(lp, l.move)
			}
			lp.Children(func() { a.pageView(c, l.page, l.from, false) })
			if l.move.door == "" {
				t := l.move.depth.Crossing().Leave()
				if a.still(c) {
					t = motion.Still(t)
				}
				lp.ZIndex(1).Transition(t)
			}
		}
		// The article under the pointer, laid out unseen where its page will
		// be; not while a crossing plays, nor while its page is leaving.
		if id := a.hover; id != "" && a.page.kind == "author" && a.next == nil && a.left == nil {
			if rp := a.readers[id]; rp != nil && rp.r != nil {
				if wait := max(hoverWarm-c.Now().Sub(a.hoverAt), crossingTime-c.Now().Sub(a.movedAt)); wait > 0 {
					c.After(wait)
				} else {
					from := append(a.from[:len(a.from):len(a.from)], a.page)
					ui.Column(c.Key("reader:" + id)).Absolute().Fill().ZIndex(1).Invisible().AccessHidden(true).
						Children(func() { a.unseen(func() { a.pageView(c, page{"reader", id}, from, false) }) })
				}
			}
		}
		// And the author under the pointer in the sidebar, likewise.
		if id := a.hoverAuthor; id != "" && a.next == nil && a.left == nil && a.current() != (page{"author", id}) && a.writer(id) != nil {
			if wait := max(hoverWarm-c.Now().Sub(a.hoverAuthorAt), crossingTime-c.Now().Sub(a.movedAt)); wait > 0 {
				c.After(wait)
			} else {
				ui.Column(c.Key("author:" + id)).Absolute().Fill().ZIndex(1).Invisible().AccessHidden(true).
					Children(func() { a.unseen(func() { a.pageView(c, page{"author", id}, nil, false) }) })
			}
		}
		// And Explore under a kind's way back to it, the pointer on it.
		if p := a.page; p.kind == "kind" && a.next == nil && a.left == nil && a.backTo() == (page{kind: "explore"}) {
			if at := a.wall(p.id).back; !at.IsZero() {
				if wait := max(hoverWarm-c.Now().Sub(at), crossingTime-c.Now().Sub(a.movedAt)); wait > 0 {
					c.After(wait)
				} else {
					ui.Column(c.Key("explore:")).Absolute().Fill().ZIndex(1).Invisible().AccessHidden(true).
						Children(func() { a.unseen(func() { a.pageView(c, page{kind: "explore"}, nil, false) }) })
				}
			}
		}
		p := a.page
		m := a.came
		door := m != nil && m.door != ""
		if door && m.depth == motion.Deep {
			a.doorGround(c, m, true)
		}
		pg := ui.Column(c.Key(p.kind + ":" + p.id)).Absolute().Fill().ZIndex(1)
		pg.Children(func() { a.pageView(c, p, a.from, true) })
		// Through a door the page comes in on the ground the door opens, or Explore lies under the ground closing.
		if m != nil && !m.kbd && !(door && !a.still(c)) {
			a.comeIn(c, pg, m)
		}
		if door && m.depth == motion.Back {
			a.doorGround(c, m, false)
		}
		a.scrollbar(c)
	})
}

// pageView builds page p, whose back button goes to the last of from; live
// is false for the page left, built on for its exit.
func (a *App) pageView(c *ui.Context, p page, from []page, live bool) {
	switch p.kind {
	case "reader":
		a.readerPage(c, p.id, from, live)
	case "author":
		a.authorPage(c, p.id, live)
	case "explore":
		a.explorePage(c, live)
	case "kind":
		a.kindPage(c, p.id, live)
	case "favorites":
		a.favoritesPage(c, p.id, live)
	case "clips":
		a.clipsPage(c, live)
	case "home":
		a.homePage(c, live)
	}
}

// comeIn is the next page coming into focus (Crossing.Come: it waits unseen,
// then comes into focus from a little closer), timed from the frame it first
// shows. It is played on the page's own values rather than as its entering
// transition, as an article laid out unseen beforehand (warm.go) is not new
// when it shows. Under less motion it only fades and focuses.
func (a *App) comeIn(c *ui.Context, pg ui.Element, m *move) {
	x := m.depth.Crossing()
	opt := ui.KeyframeOptions{Run: m.run, NonMotion: true}
	frames := []ui.Keyframe{{Value: 0}, {At: x.Wait + x.In, Value: 1, Ease: motion.Ease}}
	if x.Wait > 0 {
		frames = []ui.Keyframe{{Value: 0}, {At: x.Wait, Value: 0}, {At: x.Wait + x.In, Value: 1, Ease: motion.Ease}}
	}
	k := pg.Keyframes("come", frames, opt)
	if k >= 1 {
		return
	}
	pg.Opacity(k)
	if blur := motion.Defocus * (1 - k); blur > 0.01 {
		pg.Blur(blur)
	}
	if s := x.From + (1-x.From)*k; !a.still(c) && s != 1 {
		pg.TransformOrigin(0.5, 0.5).Scale(s, s)
	}
}
