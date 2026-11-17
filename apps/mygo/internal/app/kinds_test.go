package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
)

// Under the design room's front page, a door for each kind the galleries
// shelve works under; a door opens the kind's wall of works, newest first,
// whose way back is 探索, under its tab; Esc goes back, the focus on the
// door. A work opened from the wall goes back to it.
func TestExploreKinds(t *testing.T) {
	a, _ := newExploreApp(t, nil)
	tt := exploreTester(a)
	toExplore(t, a, tt)
	ex := a.ex
	works := ex.kinds["landing"]
	if len(works) == 0 {
		t.Fatalf("Land-book shelves its works under landing: %v", len(ex.kinds))
	}
	for i := 1; i < len(works); i++ {
		if exTime(works[i].publishedAt) > exTime(works[i-1].publishedAt) {
			t.Fatal("a kind's works, newest first")
		}
	}
	if _, ok := tt.AccessByRole(ui.RoleNavigation, "按要做的东西找"); !ok {
		t.Fatal("the doors stand under the front page")
	}
	ex.scroll.Y = 500
	tt.Frame()
	rest(tt)
	must(t, tt.Click("落地页"))
	rest(tt)
	if a.page != (page{"kind", "landing"}) || a.tabOf() != "explore" || backLabel(a, a.backTo()) != "探索" {
		t.Fatalf("the door opens its wall, under 探索: %+v %q", a.page, a.tabOf())
	}
	if _, ok := tt.AccessByRole(ui.RoleHeading, "落地页"); !ok {
		t.Fatal("the wall names its kind")
	}
	// A work opened from the wall goes back to it.
	first := works[0]
	a.kbd = true
	ex.focus = "post:" + first.url
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	rest(tt)
	if a.page.kind != "reader" || backLabel(a, a.backTo()) != "落地页" {
		t.Fatalf("a work opens in the reader, its way back the wall: %+v", a.page)
	}
	tt.Key(0, ui.KeyEscape)
	rest(tt)
	if a.page != (page{"kind", "landing"}) {
		t.Fatalf("back to the wall: %+v", a.page)
	}
	// Esc leaves the wall for Explore, the focus on its door.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.page != (page{kind: "explore"}) || !tt.Focused("落地页") {
		t.Fatalf("Esc goes back to the door: %+v", a.page)
	}
}

// 收藏's first page is a map, one screen tall: a block for each kind kept
// under, a design under two in both, and the articles; the more a block
// holds, the larger. Choosing one zooms into it: its keeps are the cards,
// the rest of the map a strip, from which another is chosen or the map
// opened again; Esc opens it too.
func TestFavoritesMap(t *testing.T) {
	a, store := newExploreApp(t, nil)
	tt := exploreTester(a)
	toExplore(t, a, tt)
	keep := func(url, title string, kinds ...string) {
		t.Helper()
		lib, err := store.Mutate(library.Keep{Source: library.SourceSnapshot{ID: "feed:gallery", Title: "Gallery", Host: "gallery.example", HomeURL: "https://gallery.example/"},
			Name: "Gallery", FeedURL: "https://gallery.example/feed", Article: library.ArticleSnapshot{ID: url, URL: url, Title: title, Authors: []string{},
				Aliases: []string{}, BodyStatus: "not_extracted", Kinds: kinds}})
		if err == nil {
			lib, err = store.Mutate(library.ToggleFavorite{ID: url})
		}
		if err != nil {
			t.Fatal(err)
		}
		a.accept(lib)
	}
	keep("https://gallery.example/a", "Alpha", "landing", "motion")
	keep("https://gallery.example/b", "Beta", "landing")
	keep("https://gallery.example/c", "Gamma", "brand")
	writer := a.lib.Articles[0]
	if lib, err := store.Mutate(library.ToggleFavorite{ID: writer.ID}); err == nil {
		a.accept(lib)
	}
	paper := nameOf(a.keeperOf(&writer)) + "，收藏 1 篇"
	must(t, tt.Click("收藏"))
	settle(tt)
	blocks := map[string]ui.Rect{}
	for _, name := range []string{"落地页，收藏 2 件", "动效，收藏 1 件", "品牌，收藏 1 件", "文章，收藏 1 篇"} {
		r, ok := tt.Find(name)
		if !ok {
			t.Fatalf("the map has no %q", name)
		}
		if r.Y+r.H > 850 {
			t.Errorf("%q runs past the screen: %v", name, r)
		}
		blocks[name] = r
	}
	if l, m := blocks["落地页，收藏 2 件"], blocks["动效，收藏 1 件"]; l.W*l.H <= m.W*m.H {
		t.Errorf("落地页 holds two and is no larger than 动效 with one: %v %v", l, m)
	}
	for _, c := range a.favs().cards {
		if !strings.HasPrefix(c.key, "m:") {
			t.Errorf("the keys reach %q inside a block on the map", c.key)
		}
	}
	must(t, tt.Click("落地页，收藏 2 件"))
	settle(tt)
	cards := func() []string {
		var keys []string
		for _, c := range a.favs().cards {
			keys = append(keys, c.key)
		}
		return keys
	}
	if a.favs().kind != "kind:landing" || !slices.Equal(cards(), []string{"k:https://gallery.example/b", "k:https://gallery.example/a"}) {
		t.Fatalf("into 落地页: %q, cards %q", a.favs().kind, cards())
	}
	if _, ok := tt.Find("品牌，收藏 1 件"); !ok {
		t.Fatal("zoomed in, the rest of the map is gone")
	}
	must(t, tt.Click("品牌，收藏 1 件"))
	settle(tt)
	if a.favs().kind != "kind:brand" {
		t.Fatalf("the strip does not go to 品牌: %q", a.favs().kind)
	}
	must(t, tt.Click("品牌，收藏 1 件"))
	settle(tt)
	if a.favs().kind != "" {
		t.Fatal("the block zoomed into, chosen in the strip, does not open the map again")
	}
	must(t, tt.Click("文章，收藏 1 篇"))
	settle(tt)
	if _, ok := tt.Find(paper); !ok {
		t.Fatalf("the articles hold no %q", paper)
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if a.favs().kind != "" {
		t.Fatal("Esc does not open the map again")
	}
}

// The map's arithmetic: the blocks share the screen by what they hold, gap
// apart, none under half an even share; tiles cover a block; rows fill the
// width.
func TestMapLayout(t *testing.T) {
	r := ui.Rect{W: 1000, H: 600}
	blocks := []mapBlock{{n: 12}, {n: 6}, {n: 1}, {n: 3}}
	rects := mapRects(blocks, r, 4)
	var area float32
	for i, b := range rects {
		if b.X < 0 || b.Y < 0 || b.X+b.W > r.W+0.01 || b.Y+b.H > r.H+0.01 {
			t.Fatalf("block %d out of the map: %v", i, b)
		}
		for j := range i {
			o := rects[j]
			if b.X < o.X+o.W-0.01 && o.X < b.X+b.W-0.01 && b.Y < o.Y+o.H-0.01 && o.Y < b.Y+b.H-0.01 {
				t.Fatalf("blocks %d and %d overlap: %v %v", j, i, o, b)
			}
		}
		area += b.W * b.H
	}
	if share := rects[2].W * rects[2].H / area; share < 0.1 {
		t.Errorf("the block of one has %.2f of the map, not about half an even share", share)
	}
	if rects[0].W*rects[0].H <= rects[1].W*rects[1].H {
		t.Error("twelve is no larger than six")
	}
	for _, n := range []int{1, 2, 5, 9, 13} {
		tiles := mosaic(n, 400, 300, 2)
		if len(tiles) != n {
			t.Fatalf("%d tiles for %d", len(tiles), n)
		}
		var covered float32
		for _, c := range tiles {
			covered += (c.W + 2) * (c.H + 2)
		}
		if covered < 402*302*0.99 {
			t.Errorf("%d tiles leave the block uncovered: %.0f of %d", n, covered, 402*302)
		}
	}
	rows := mapRows([]float32{1.5, 1.5, 1.5, 1.5, 1.5}, 1000, 280, 8)
	if last := rows[2]; last.Y != rows[0].Y || last.X+last.W < 999 {
		t.Errorf("the first row does not fill the width: %v", rows[:3])
	}
}
