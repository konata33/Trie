package app

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"trie/internal/theme"
)

// Press asks the window to do what a named control does, as the pointer
// would, in its next frame: "author:<source ID>", "open:<article ID>",
// "back", "dialog", "close", "sheet", and on Explore "tab:explore", "door",
// "card:<entry ID>", "esc", "scroll:<y>"; in 收藏 "shelf:<source ID>" (into
// an author), "letgo:<source ID>" (their whole shelf), "undo"; in 摘录
// "clip:<clip ID>" (its card), "cite" (the card's article); on 首页
// "read:<article ID>" (a column of the people followed); on 收藏 (the
// authors or an author's keeps), 摘录 and 首页 "page:<y>" (the page's
// scroll). It is for running the window through its crossings from
// outside, to time them on the GPU (TRIE_PLAY); call it on the
// window's thread (Window.Update).
func (a *App) Press(name string) { a.pressed = append(a.pressed, name) }

// presses runs the presses asked for, with the frame's context.
func (a *App) presses(c *ui.Context) {
	for _, name := range a.pressed {
		a.kbd = false
		kind, id, _ := strings.Cut(name, ":")
		switch kind {
		case "author":
			a.openAuthor(c, id)
		case "open":
			if cur := a.current(); cur.kind == "author" {
				a.open(c, id, a.list(cur.id).rows[id])
			}
		case "rest":
			a.hoverAuthor, a.hoverAuthorAt, a.pinned = id, c.Now(), true
		case "hover":
			a.hover, a.hoverAt, a.pinned = id, c.Now(), true
			a.reading(id)
		case "back":
			a.back(c)
		case "dialog":
			a.openSubscribe(ui.Rect{X: 163, Y: 814, W: 24, H: 24})
		case "close":
			a.layers.dialog, a.layers.sheet = nil, false
		case "sheet":
			a.toggleSheet(ui.Rect{X: 22, Y: 817, W: 46, H: 18})
		case "tab":
			a.tab(c, id)
		case "door": // Explore's way across
			if a.page.kind == "explore" {
				a.enterRoom(c, 1-a.ex.room, false, 0)
			}
		case "card": // an Explore card opening its author's panel
			if e := a.ex.find(id); e != nil {
				a.showSheet(c, e, "site", ui.Rect{})
			}
		case "esc":
			if a.ex.open != nil {
				a.closeSheet(c)
			}
		case "scroll":
			if y, err := strconv.ParseFloat(id, 32); err == nil {
				a.ex.scroll.ScrollTo(0, float32(y), ui.Smooth)
			}
		case "shelf":
			a.enterShelf(c, id)
		case "letgo":
			_, groups := a.kept("")
			for _, g := range groups {
				if g.src.ID == id {
					a.letGoShelf(c, g)
				}
			}
		case "undo":
			if a.toast.undo != nil {
				a.toast.undo()
			}
		case "clip":
			a.showClip(id, a.clipsSt().boxes[id])
		case "cite":
			cs := a.clipsSt()
			for _, cl := range cs.order {
				if cl.ID == cs.open {
					a.openClip(c, cl)
					break
				}
			}
		case "read":
			if hs := a.homeSt(); a.page.kind == "home" {
				hs.cursor, hs.resumed = id, ""
				a.openAs(c, id, hs.columns[id], followTitle, theme.Subtitle)
			}
		case "page":
			if y, err := strconv.ParseFloat(id, 32); err == nil {
				st := &a.clipsSt().scroll
				switch {
				case a.page.kind == "favorites" && a.page.id != "":
					st = &a.favs().keeps
				case a.page.kind == "favorites":
					st = &a.favs().shelves
				case a.page.kind == "home":
					st = &a.homeSt().scroll
				}
				st.ScrollTo(0, float32(y), ui.Smooth)
			}
		}
	}
	a.pressed = a.pressed[:0]
	c.Invalidate()
}
