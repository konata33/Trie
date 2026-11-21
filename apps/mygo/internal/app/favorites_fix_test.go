package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
)

// Letting go from the keys, the focus goes on to the next card that stays,
// past the ones leaving with it, else back to the one before.
func TestHandOnSkipsCardsLeaving(t *testing.T) {
	fs := &favState{cards: []favCard{{key: "a:1"}, {key: "k:x"}, {key: "a:2"}, {key: "k:y"}}}
	fs.handOn("a:1", "k:x")
	if fs.focus != "a:2" || fs.cursor != "a:2" {
		t.Fatalf("a writer let go with their keep: the focus goes to the next writer, not to their keep: %q", fs.focus)
	}
	fs.focus, fs.cursor = "", ""
	fs.handOn("k:y", "a:2")
	if fs.focus != "k:x" {
		t.Fatalf("a writer's last keep let go, their sheet with it: the focus goes back past the sheet: %q", fs.focus)
	}
	fs.focus = ""
	fs.handOn("k:gone")
	if fs.focus != "" {
		t.Fatalf("a card not shown moves no focus: %q", fs.focus)
	}
}

// The strip beside a block zoomed into is the map smaller; with so many
// blocks that one would stand under mapRailMin on a side, it grows taller
// rather than shrink them.
func TestRailRects(t *testing.T) {
	few := []mapBlock{{n: 4}, {n: 2}, {n: 1}}
	if _, h := railRects(few, 600); h != 600 {
		t.Fatalf("three blocks fit the window: %v tall", h)
	}
	var many []mapBlock
	for i := range 24 {
		many = append(many, mapBlock{n: 1 + i%5})
	}
	rects, h := railRects(many, 600)
	if h <= 600 {
		t.Fatalf("24 blocks in 600: the strip does not grow (%v)", h)
	}
	for i, r := range rects {
		if min(r.W, r.H) < mapRailMin || r.Y+r.H > h+0.01 {
			t.Fatalf("block %d is %v in a strip %v tall", i, r, h)
		}
	}
}

// Zoomed into a block, letting go of its last keep shows the map; 撤销
// brings the keep back and opens the block again.
func TestFavoritesUndoReopens(t *testing.T) {
	a, store := newExploreApp(t, nil)
	tt := exploreTester(a)
	toExplore(t, a, tt)
	for _, w := range []struct{ url, kind string }{{"https://gallery.example/a", "landing"}, {"https://gallery.example/c", "brand"}} {
		lib, err := store.Mutate(library.Keep{Source: library.SourceSnapshot{ID: "feed:gallery", Title: "Gallery", Host: "gallery.example", HomeURL: "https://gallery.example/"},
			Name: "Gallery", FeedURL: "https://gallery.example/feed", Article: library.ArticleSnapshot{ID: w.url, URL: w.url, Title: w.kind, Authors: []string{},
				Aliases: []string{}, BodyStatus: "not_extracted", Kinds: []string{w.kind}}})
		if err == nil {
			lib, err = store.Mutate(library.ToggleFavorite{ID: w.url})
		}
		must(t, err)
		a.accept(lib)
	}
	must(t, tt.Click("收藏"))
	settle(tt)
	must(t, tt.Click("品牌，收藏 1 件"))
	settle(tt)
	fs := a.favs()
	if fs.kind != "kind:brand" {
		t.Fatalf("into 品牌: %q", fs.kind)
	}
	tt.Key(0, ui.KeyJ)
	tt.Frame()
	tt.Key(0, ui.KeyS)
	settle(tt)
	if fs.kind != "" || a.toast.undo == nil {
		t.Fatalf("its last keep let go, the map shows, with 撤销: %q", fs.kind)
	}
	a.toast.undo()
	tt.Frame()
	settle(tt)
	if fs.kind != "kind:brand" {
		t.Fatalf("撤销 opens the block again: %q", fs.kind)
	}
}
