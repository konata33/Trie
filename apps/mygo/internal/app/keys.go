package app

import (
	"github.com/egoist/mygo/ui"

	"trie/internal/library"
)

// shortcuts are the window's keys: what
// the keyboard does shows at once.
func (a *App) shortcuts(c *ui.Context) {
	// ⌘Z presses the notice's 撤销 (outside text fields, which keep their own undo).
	if a.toast.undo != nil && c.Shortcut(ui.Cmd, ui.KeyZ) {
		a.toast.undo()
	}
	// The card a passage opens in is modal: only its own keys.
	if a.page.kind == "clips" && a.clips != nil && a.clips.open != "" {
		a.clipsKeys(c)
		return
	}
	if c.Shortcut(ui.Cmd, ui.KeyF) {
		a.layers.sheet = false
		switch cur := a.current(); cur.kind {
		case "author", "favorites", "clips", "home":
			if a.page.kind == "reader" {
				a.back(c)
			}
			switch cur.kind {
			case "author":
				a.list(cur.id).search = true
			case "favorites":
				a.favs().search = true
			case "home":
				a.homeSt().search = true
			default:
				a.clipsSt().search = true
			}
		case "explore":
			// Explore has no search of its own: ⌘F goes to 首页's.
			a.tab(c, "home")
			a.homeSt().search = true
		}
	}
	// The capture card open takes its own keys; with the keys in the island, Esc
	// sends it away (收起).
	if a.capture.card != "" {
		return
	}
	if a.capture.focused && c.Shortcut(0, ui.KeyEscape) {
		a.hideIsland(c.Now())
		return
	}
	if a.layers.dialog != nil || a.layers.menu != "" {
		return
	}
	if c.Shortcut(ui.Cmd, ui.KeyComma) {
		a.toggleSheet(ui.Rect{})
		return
	}
	for i, k := range []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4} {
		if c.Shortcut(ui.Cmd, k) {
			a.tab(c, tabs[i].kind)
		}
	}
	switch a.page.kind {
	case "reader":
		art := a.article(a.page.id)
		switch {
		case c.Shortcut(0, ui.KeyEscape):
			if rp := a.readers[a.page.id]; rp != nil && (rp.aa || rp.more) {
				rp.aa = false // the options fold back into Aa
				return
			}
			a.back(c)
		case art == nil:
		case c.Shortcut(0, ui.KeyS) && a.libArticle(art.ID) != nil:
			a.toggleFav(art)
		case c.Shortcut(0, ui.KeyO):
			a.openExternal(c, art.URL)
		case c.Shortcut(ui.Cmd|ui.Shift, ui.KeyC):
			if rp := a.readers[art.ID]; rp != nil {
				a.copyLink(c, rp, art)
			}
		}
	case "author":
		a.listKeys(c, a.page.id)
	case "kind":
		if c.Shortcut(0, ui.KeyEscape) {
			a.back(c)
		}
	case "favorites":
		a.favKeys(c)
	case "clips":
		a.clipsKeys(c)
	case "home":
		a.homeKeys(c)
	}
}

// listKeys moves the list cursor (J, K and the arrows), opens its article
// (Enter on it, through its button), keeps it (S), and leaves (Esc).
func (a *App) listKeys(c *ui.Context, id string) {
	au := a.writer(id)
	if au == nil {
		return
	}
	l := a.list(id)
	items := a.shown(au, l.query)
	if c.Shortcut(0, ui.KeyEscape) {
		switch {
		case l.query != "":
			l.query = ""
		default:
			a.back(c)
		}
		return
	}
	if len(items) == 0 {
		return
	}
	at := 0
	for i, art := range items {
		if art.ID == l.cursor {
			at = i
		}
	}
	step := 0
	switch {
	case c.Shortcut(0, ui.KeyJ), c.Shortcut(0, ui.KeyDown):
		step = 1
	case c.Shortcut(0, ui.KeyK), c.Shortcut(0, ui.KeyUp):
		step = -1
	case c.Shortcut(0, ui.KeyS):
		a.toggleFav(items[at])
		return
	case c.Shortcut(0, ui.KeyEnter):
		if l.cursor == "" {
			l.cursor = items[0].ID
		}
		a.open(c, l.cursor, l.rows[l.cursor])
		return
	default:
		return
	}
	if l.cursor != "" {
		at = max(0, min(len(items)-1, at+step))
	}
	l.cursor = items[at].ID
	l.focus = l.cursor
}

var _ = library.ToggleFavorite{}
