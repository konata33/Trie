package app

import (
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

// 收藏 with designs kept: the kinds they are shelved under make the first
// screen's board, the kind kept most taking the left; a block opens the
// kind's keeps, each with its author; what is not a design stays with its
// writer.
func TestFavoritesBoard(t *testing.T) {
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
	must(t, tt.Click("收藏"))
	settle(tt)
	if _, ok := tt.AccessByRole(ui.RoleList, "收藏的设计"); !ok {
		t.Fatal("the designs kept make a board")
	}
	land, ok := tt.Find("落地页，收藏 2 篇")
	brand, ok2 := tt.Find("品牌，收藏 1 篇")
	if !ok || !ok2 || land.W <= brand.W || land.X > brand.X {
		t.Fatalf("the kind kept most takes the left: %v %v", land, brand)
	}
	if _, ok := tt.Find(nameOf(a.keeperOf(&writer)) + "，收藏 1 篇"); !ok {
		t.Fatal("a writer's keep stays with its writer")
	}
	must(t, tt.Click("落地页，收藏 2 篇"))
	settle(tt)
	if a.page != (page{"favorites", "kind:landing"}) || !tt.HasText("Alpha") || !tt.HasText("Beta") || tt.HasText("Gamma") {
		t.Fatalf("the block opens the kind's keeps: %+v", a.page)
	}
}

// The board's blocks cover it without overlapping, the first the largest.
func TestBoardRects(t *testing.T) {
	for n := 1; n <= 5; n++ {
		rs := boardRects(n, 1000, 600, 4)
		if len(rs) != n {
			t.Fatalf("%d blocks: %v", n, rs)
		}
		for i, r := range rs {
			if r.X < 0 || r.Y < 0 || r.X+r.W > 1000.5 || r.Y+r.H > 600.5 || r.W*r.H > rs[0].W*rs[0].H {
				t.Errorf("%d blocks, #%d: %v", n, i, r)
			}
			for _, q := range rs[:i] {
				if r.X < q.X+q.W && q.X < r.X+r.W && r.Y < q.Y+q.H && q.Y < r.Y+r.H {
					t.Errorf("%d blocks overlap: %v %v", n, q, r)
				}
			}
		}
	}
}
