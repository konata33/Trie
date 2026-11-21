package app

import (
	"image"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/motion"
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
		t.Fatalf("One Page Love shelves its works under landing: %v", len(ex.kinds))
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

// Through a door Explore goes on, inert and unread, while it goes out of
// focus over the door's ground, and is let go once gone, rather than left
// to a copy that would lay its text out anew. On the wall, the pointer
// resting on the way back marks it, for Explore to be laid out unseen.
func TestDoorLeave(t *testing.T) {
	a, _ := newExploreApp(t, nil)
	tt := exploreTester(a)
	toExplore(t, a, tt)
	a.ex.scroll.Y = 500
	tt.Frame()
	rest(tt)
	must(t, tt.Click("落地页"))
	tt.Advance(50 * time.Millisecond)
	if l := a.left; l == nil || l.page != (page{kind: "explore"}) {
		t.Fatalf("Explore goes on as the door opens: %+v", a.left)
	}
	if _, ok := tt.AccessByRole(ui.RoleNavigation, "按要做的东西找"); ok {
		t.Fatal("the page left is not read out")
	}
	tt.Advance(motion.DoorLeave)
	if a.left != nil {
		t.Fatalf("Explore is let go once gone: %+v", a.left)
	}
	rest(tt)
	if a.page != (page{"kind", "landing"}) {
		t.Fatalf("on the wall: %+v", a.page)
	}
	kw := a.wall("landing")
	// The way back: the button named 探索 in the content area, not the sidebar's tab.
	var way ui.Rect
	for _, n := range tt.AccessAll() {
		if n.Role == ui.RoleButton && n.Name == "探索" && n.Bounds.X >= a.area.X {
			way = n.Bounds
		}
	}
	if way.W <= 0 {
		t.Fatal("no way back to 探索 on the wall")
	}
	tt.Move(way.X+way.W/2, way.Y+way.H/2)
	tt.Advance(50 * time.Millisecond)
	if kw.back.IsZero() {
		t.Fatal("resting on the way back marks it")
	}
	tt.Move(way.X+200, 400)
	tt.Frame()
	if !kw.back.IsZero() {
		t.Fatal("leaving it unmarks it")
	}
	// Scrolled down and left, the page opens at its top again through its door.
	kw.scroll.Y = 300
	tt.Frame()
	if kw.scroll.Y == 0 {
		t.Fatal("the wall scrolls")
	}
	a.Press("back")
	tt.Frame()
	rest(tt)
	must(t, tt.Click("落地页"))
	rest(tt)
	if a.page != (page{"kind", "landing"}) || kw.scroll.Y != 0 {
		t.Fatalf("through its door the page opens at its top: %+v at %v", a.page, kw.scroll.Y)
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

// A door stands on its kind's own drawing, shipped with the app. Pointed
// at, it lights and its moving part goes to where it is drawn; the
// pendulum swings once and comes to rest; the brand's mark lights once the
// stamp has pressed it. Back from a wall, a door that played does not play
// again. From the keys it is lit at once and nothing plays; under less
// motion the part stands where it is drawn and only the light changes.
func TestDoorDrawing(t *testing.T) {
	for _, k := range kindOrder {
		art := doorArts()[k]
		if art.still.svg == nil || art.moving.svg == nil || art.amber.svg == nil || art.faint.svg == nil {
			t.Errorf("%s's drawing is missing layers", k)
		}
		if _, ok := doorPoses[k]; !ok {
			t.Errorf("%s's drawing has no pose", k)
		}
	}
	a, _ := newExploreApp(t, nil)
	tt := exploreTester(a)
	toExplore(t, a, tt)
	a.ex.scroll.Y = 500
	tt.Frame()
	rest(tt)
	point := func(name string) {
		t.Helper()
		r, ok := tt.Find(name)
		if !ok {
			t.Fatalf("no door %s", name)
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
	}
	landing, mot := a.wall("landing"), a.wall("motion")
	if landing.look != 0 || landing.lit != 0 {
		t.Fatalf("a door at rest: %v %v", landing.look, landing.lit)
	}
	point("落地页")
	tt.Advance(motion.DoorAct + 100*time.Millisecond)
	if landing.look < 0.999 || landing.lit < 0.999 {
		t.Fatalf("pointed at, the door lights and its sun rises: %v %v", landing.look, landing.lit)
	}
	point("动效")
	tt.Advance(motion.DoorSwingFor / 5)
	if mot.acting.beat == 0 {
		t.Fatal("pointed at, the pendulum swings")
	}
	tt.Advance(motion.DoorSwingFor)
	if mot.acting.beat != 0 || mot.acting.runs != 1 {
		t.Fatalf("it swings once and comes to rest: %v, %d", mot.acting.beat, mot.acting.runs)
	}
	if landing.look > 0.001 {
		t.Fatalf("the pointer gone, the sun sets behind the hills again: %v", landing.look)
	}
	brand := a.wall("brand")
	point("品牌")
	tt.Advance(50 * time.Millisecond)
	if brand.acting.beat == 0 || brand.amber > 0.2 {
		t.Fatalf("pointed at, the stamp comes down before the mark lights: %v %v", brand.acting.beat, brand.amber)
	}
	tt.Advance(motion.DoorStampFor)
	if brand.acting.beat != 0 || brand.amber < 0.999 {
		t.Fatalf("the stamp lifted, the mark it pressed is lit: %v %v", brand.acting.beat, brand.amber)
	}
	must(t, tt.Click("动效"))
	rest(tt)
	runs := mot.acting.runs
	tt.Key(0, ui.KeyEscape)
	for range 30 {
		tt.Advance(30 * time.Millisecond)
		if brand.acting.beat != 0 {
			t.Fatalf("back from the wall, the stamp presses again: %v", brand.acting.beat)
		}
	}
	if brand.acting.runs != 1 || mot.acting.runs != runs {
		t.Fatalf("back from the wall, nothing plays again: %d, %d after %d", brand.acting.runs, mot.acting.runs, runs)
	}
	a.kbd = false
	// From the keys.
	tt.Move(10, 10)
	settle(tt)
	tt.SetWindowActive(true)
	a.ex.focus = "kind:portfolio"
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	tt.Advance(50 * time.Millisecond)
	if !tt.Focused("动效") || mot.look < 0.999 || mot.acting.runs != runs {
		t.Fatalf("in focus from the keys, the door is lit at once and its pendulum still: %v, %d", mot.look, mot.acting.runs)
	}
	// Under less motion.
	a.kbd = false
	a.Reduce = true
	tt.Move(10, 10)
	settle(tt)
	if landing.look != 1 || landing.lit != 0 {
		t.Fatalf("under less motion the drawing stands as drawn, unlit: %v %v", landing.look, landing.lit)
	}
}

// A kind's wall has the galleries take turns: the newest first, each
// gallery's works in its own order, spread by its share, and none three
// times running while another has works left.
func TestTakeTurns(t *testing.T) {
	big, b, c := &explore.Entry{ID: "big"}, &explore.Entry{ID: "b"}, &explore.Entry{ID: "c"}
	var works []exWork
	for i := range 8 {
		works = append(works, exWork{entry: big, index: i})
	}
	works = append(works, exWork{entry: b, index: 0}, exWork{entry: c, index: 0}, exWork{entry: b, index: 1}, exWork{entry: c, index: 1})
	got := takeTurns(works)
	if len(got) != len(works) || got[0] != works[0] {
		t.Fatalf("every work, the newest first: %v", got)
	}
	last := map[string]int{}
	for i, w := range got {
		if w.index < last[w.entry.ID] {
			t.Fatalf("a gallery's works keep their order: %v", got)
		}
		last[w.entry.ID] = w.index
		if i >= 2 && got[i-1].entry == w.entry && got[i-2].entry == w.entry && slices.ContainsFunc(got[i+1:], func(x exWork) bool { return x.entry != w.entry }) {
			t.Fatalf("%s three times running at %d while others wait: %v", w.entry.ID, i, got)
		}
	}
	if got[1].entry == big && got[2].entry == big {
		t.Errorf("the small galleries come in near the top: %v", got[:4])
	}
}

// A work's picture taller than twice its width keeps only its top, as no
// box holding it by its top shows more; one no taller stays as it is.
func TestTopOf(t *testing.T) {
	if b := topOf(image.NewRGBA(image.Rect(0, 0, 120, 1240))).Bounds(); b != image.Rect(0, 0, 120, 240) {
		t.Fatalf("a page captured whole keeps its top: %v", b)
	}
	short := image.Image(image.NewRGBA(image.Rect(0, 0, 120, 200)))
	if topOf(short) != short {
		t.Fatal("a picture no taller than twice its width stays whole")
	}
}
