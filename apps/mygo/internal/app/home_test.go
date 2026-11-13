package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/subscription"
	"trie/internal/web"
)

// pngData is a w×h picture of one colour as a data URL.
func pngData(w, h int, c color.Color) string {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

// homeEntries is a catalog for 首页: a gallery with today's work, two
// aggregators, and writers who wrote in the last two weeks (one twice under
// one domain, one too long ago, one with nothing whole to say).
func homeEntries(now time.Time) []explore.Entry {
	at := func(days int) *string {
		s := now.Add(-time.Duration(days) * 24 * time.Hour).UTC().Format(time.RFC3339)
		return &s
	}
	str := func(s string) *string { return &s }
	icon := pngData(8, 8, color.NRGBA{200, 120, 60, 255})
	out := []explore.Entry{{ID: "gallery", Name: "Godly", Kind: "site", Section: "网页画廊", HomeURL: "https://godly.website", Host: "godly.website", Icon: &icon,
		Works: []explore.Post{{Title: "昨天的作品", URL: "https://godly.website/w/old", PublishedAt: at(1)}, {Title: "今天的作品：Field Notes", URL: "https://godly.website/w/new", PublishedAt: at(0)}}}}
	// What an aggregator recommends sits on its shelf, in the order it ranks them.
	for _, sec := range []string{"Hacker News", "Lobsters"} {
		for i := range 3 {
			home := fmt.Sprintf("https://%s.example/%d", strings.ToLower(strings.ReplaceAll(sec, " ", "")), i+1)
			out = append(out, explore.Entry{ID: fmt.Sprintf("%s-%d", sec, i), Name: fmt.Sprintf("站点 %d", i), Kind: "blog", Section: sec, HomeURL: home,
				Posts: []explore.Post{{Title: fmt.Sprintf("%s 第 %d", sec, i+1), URL: home + "/post", PublishedAt: at(i), Excerpt: "开头的几句话。"}}})
		}
	}
	for i := range 12 {
		home := fmt.Sprintf("https://writer%d.dev", i)
		e := explore.Entry{ID: fmt.Sprintf("w%d", i), Name: fmt.Sprintf("写作者 %d", i), Kind: "blog", Section: "中文独立博客", HomeURL: home, Host: fmt.Sprintf("writer%d.dev", i),
			FeedURL: str(home + "/feed.xml"), Icon: &icon,
			Posts: []explore.Post{
				{Title: fmt.Sprintf("第 %d 位的新文章", i), URL: home + "/new", PublishedAt: at(i % 5), Excerpt: fmt.Sprintf("我是写作者 %d。"+
					"这周我做了第 %d 个小工具，把十年里拍下的照片按城市、街道和季节重新整理了一遍，顺手把重复的、拍糊的和只是为了记住停车位置的那些都清掉了，最后剩下的大概只有原来的三分之一。后面还有。", i, i)},
				{Title: "旧文章", URL: home + "/old", PublishedAt: at(30), Excerpt: fmt.Sprintf("我是写作者 %d。另一篇的开头。", i)}}}
		switch i {
		case 10:
			e.Posts[0].PublishedAt = at(20) // too long ago
		case 11:
			e.Posts[0].Excerpt = "短" // nothing whole to say
		}
		out = append(out, e)
	}
	twice := out[len(out)-12]
	twice.ID, twice.Host, twice.HomeURL = "w0b", "notes.writer0.dev", "https://notes.writer0.dev"
	twice.FeedURL = str("https://notes.writer0.dev/feed.xml")
	return append(out, twice)
}

// newHomeApp is the library copy with Explore offline on homeEntries, the
// pictures missing; subscribing reads a fake feed of the writer pressed.
func newHomeApp(t testing.TB, prefs ui.Preferences) (*App, *ui.Tester, *library.Store, func(*explore.Entry)) {
	t.Helper()
	var target *explore.Entry
	a, store := newExploreApp(t, func(store *library.Store) *subscription.Service {
		return subscription.New(store, func() fetch.Fetch { return feedFor(target.Name, target.HomeURL) }, nil, nil)
	})
	tt := ui.NewTesterWith(a.View, 1280, 850, ui.TesterOptions{Preferences: prefs})
	tt.SetScale(0.25)
	tt.Frame()
	ex := a.ex
	ex.entries = homeEntries(tt.Now())
	for i := range ex.entries {
		if e := &ex.entries[i]; e.Icon != nil {
			ex.pics["icon:"+e.ID] = decodePic(*e.Icon, false)
		}
	}
	ex.loaded, ex.loading = true, false
	settle(tt)
	return a, tt, store, func(e *explore.Entry) { target = e }
}

// unread puts the test library's half-read articles back at their start, for a page without 接着读.
func unread(t *testing.T, a *App, tt *ui.Tester) {
	t.Helper()
	for _, art := range a.lib.Articles {
		if p := art.ReadingPosition; p != nil {
			if err := a.opt.Store.SaveReadingPosition(library.ReadingPositionInput{ArticleID: art.ID, ContentVersion: p.ContentVersion}); err != nil {
				t.Fatal(err)
			}
		}
	}
	lib, err := a.opt.Store.Library()
	if err != nil {
		t.Fatal(err)
	}
	a.accept(lib)
	settle(tt)
}

// entryOf is the catalog's entry id.
func entryOf(a *App, id string) *explore.Entry {
	for i := range a.ex.entries {
		if a.ex.entries[i].ID == id {
			return &a.ex.entries[i]
		}
	}
	return nil
}

// The page: today's design example, then the ranking, then the people
// followed; nothing passes by.
func TestHomePage(t *testing.T) {
	a, tt, _, _ := newHomeApp(t, ui.Preferences{})
	unread(t, a, tt)
	if a.page.kind != "home" {
		t.Fatalf("starts on 首页: %v", a.page)
	}
	for _, name := range []string{"今天的设计例子", "流行", "订阅的作者"} {
		if _, ok := tt.AccessByRole(ui.RoleRegion, name); !ok {
			t.Errorf("no %s", name)
		}
	}
	if _, ok := tt.AccessByRole(ui.RoleRegion, "还没订阅的作者"); ok {
		t.Error("no one passes by")
	}
	entries := a.ex.entries
	if ex := exampleOf(entries); ex == nil || ex.post.Title != "今天的作品：Field Notes" || ex.index != 1 {
		t.Errorf("today's example is the newest work: %+v", ex)
	}
	var ranked []string
	for _, it := range trendingOf(entries) {
		ranked = append(ranked, it.post.Title)
	}
	if want := []string{"Hacker News 第 1", "Lobsters 第 1", "Hacker News 第 2", "Lobsters 第 2", "Hacker News 第 3"}; !slices.Equal(ranked, want) {
		t.Errorf("trending takes the first picks in turn: %v", ranked)
	}
	for _, n := range []string{"2", "3", "4", "5"} {
		if !tt.HasText(n) {
			t.Errorf("rank %s hangs in the margin", n)
		}
	}
	// The people followed are not on the first screen.
	follows := followsOf(a.homeItems(""))
	if len(follows) == 0 {
		t.Fatal("no one followed")
	}
	if r, ok := tt.Find(titleOf(follows[0].Title)); !ok || r.Y < 850 {
		t.Errorf("the people followed start below the first screen: %v %v", ok, r)
	}
}

// Opening: a post opens in the preview reader, its title travelling, and
// 首页 is the way back, to where the page was; a followed author's piece
// opens as an article and its title goes back to its column. From the keys
// the focus comes back to what opened it.
func TestHomeOpenAndBack(t *testing.T) {
	a, tt, _, _ := newHomeApp(t, ui.Preferences{})
	unread(t, a, tt)
	hs := a.homeSt()
	hs.scroll.Y = 300
	tt.Frame()
	must(t, tt.Click("Lobsters 第 1"))
	tt.Frame()
	if a.trials[a.page.id] == nil || a.lay == nil || len(a.flights) == 0 {
		t.Fatalf("opening a post: page %v, ground %v, %d flights", a.page, a.lay != nil, len(a.flights))
	}
	settle(tt)
	if l := backLabel(a, a.from[len(a.from)-1]); l != "首页" {
		t.Errorf("the way back says %q", l)
	}
	tt.ClickAt(460, 30) // ‹ 首页
	tt.Frame()
	if a.page.kind != "home" || a.came == nil || a.came.kbd {
		t.Fatalf("back to 首页, crossing: %v", a.page)
	}
	settle(tt)
	if hs.scroll.Y != 300 {
		t.Fatalf("back where it was: %v", hs.scroll.Y)
	}
	// The sidebar's 首页 goes back the same way.
	must(t, tt.Click("Lobsters 第 1"))
	settle(tt)
	must(t, tt.Click("首页"))
	tt.Frame()
	if a.page.kind != "home" || a.came == nil || a.came.kbd {
		t.Fatalf("the tab goes back, crossing: %v", a.page)
	}
	settle(tt)
	// A followed author's newest piece.
	follows := followsOf(a.homeItems(""))
	art := follows[0]
	hs.scroll.Y = 900
	tt.Frame()
	must(t, tt.Click(titleOf(art.Title)))
	tt.Advance(50 * time.Millisecond)
	if a.page != (page{"reader", art.ID}) || len(a.flights) == 0 || a.lay == nil {
		t.Fatalf("opening an article: %v, %d flights", a.page, len(a.flights))
	}
	settle(tt)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.page.kind != "home" {
		t.Fatalf("Esc goes back: %v", a.page)
	}
	settle(tt)
	// From the keys: Enter on a rank opens it at once; Esc comes back to it.
	hs.scroll.Y = 300
	hs.focus = a.postID("https://lobsters.example/1/post")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.trials[a.page.id] == nil || len(a.flights) != 0 {
		t.Fatalf("from the keys it is simply there: %v, %d flights", a.page, len(a.flights))
	}
	settle(tt)
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if a.page.kind != "home" || !tt.Focused("Lobsters 第 1") {
		t.Errorf("the focus comes back to the rank: %v", a.page)
	}
}

// The keys: J and K walk the people followed, Enter opens, S keeps; ⌘F
// opens the search (from 探索 too), whose matches are one line each; Esc
// clears it.
func TestHomeKeysAndSearch(t *testing.T) {
	a, tt, store, _ := newHomeApp(t, ui.Preferences{})
	unread(t, a, tt)
	hs := a.homeSt()
	follows := followsOf(a.homeItems(""))
	if len(follows) < 2 {
		t.Fatal("two people followed at least")
	}
	tt.Key(0, ui.KeyJ) // from outside the columns: the focus comes to the first
	tt.Frame()
	if hs.cursor != follows[0].ID || !tt.Focused(titleOf(follows[0].Title)) {
		t.Fatalf("J from outside: %q", hs.cursor)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if hs.cursor != follows[1].ID || !tt.Focused(titleOf(follows[1].Title)) {
		t.Fatalf("↓: %q", hs.cursor)
	}
	tt.Key(0, ui.KeyK)
	tt.Frame()
	if hs.cursor != follows[0].ID {
		t.Fatalf("K: %q", hs.cursor)
	}
	was := follows[0].Favorite
	tt.Key(0, ui.KeyS)
	tt.Frame()
	if lib, _ := store.Library(); slices.IndexFunc(lib.Articles, func(x library.Article) bool { return x.ID == follows[0].ID && x.Favorite != was }) < 0 {
		t.Error("S keeps it")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.page != (page{"reader", follows[0].ID}) {
		t.Fatalf("Enter opens: %v", a.page)
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if !tt.Focused(titleOf(follows[0].Title)) {
		t.Error("back by the keys, the focus is on the column again")
	}
	// ⌘F: the search, one line per match.
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	name := nameOf(a.sourceOf(follows[0]))
	tt.Type(name)
	settle(tt)
	items := a.homeItems(name)
	if !tt.HasText(fmt.Sprintf("匹配 %d 篇", len(items))) || tt.HasText("Lobsters 第 1") {
		t.Fatalf("searching %q: %d matches", name, len(items))
	}
	if _, ok := tt.AccessByRole(ui.RoleList, ""); !ok {
		t.Error("the matches are a list")
	}
	tt.Type("没有这个词")
	settle(tt)
	if !tt.HasText("没有匹配的文章") {
		t.Error("nothing matches")
	}
	must(t, tt.Click("清除搜索"))
	settle(tt)
	if hs.query != "" || !tt.HasText("Lobsters 第 1") {
		t.Error("cleared, the page is back")
	}
	// From 探索, ⌘F goes to 首页's search.
	must(t, tt.Click("探索"))
	settle(tt)
	tt.Key(ui.Cmd, ui.KeyF)
	settle(tt)
	if a.page.kind != "home" || !hs.search {
		t.Errorf("⌘F on 探索: %v %v", a.page, hs.search)
	}
	tt.Type("x")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if hs.query != "" {
		t.Errorf("Esc clears the search: %q", hs.query)
	}
}

// 接着读: the articles left half-read lately, the last left first, three at
// most, each with its moon and the words it was left at after an ellipsis;
// none, and the page goes from the first screen to the ranking. Opened,
// it is the article, its title travelling from the column.
func TestHomeResume(t *testing.T) {
	a, tt, _, _ := newHomeApp(t, ui.Preferences{})
	unread(t, a, tt)
	if _, ok := tt.AccessByRole(ui.RoleRegion, "接着读"); ok {
		t.Fatal("nothing half-read, no 接着读")
	}
	now := tt.Now()
	arts := a.homeItems("")
	if len(arts) < 5 {
		t.Fatalf("five articles at least: %d", len(arts))
	}
	left := func(art *library.Article, progress float64, ago time.Duration) {
		unit := "u-" + art.ID
		art.ReadingPosition = &library.ReadingPosition{ReadingPositionInput: library.ReadingPositionInput{ArticleID: art.ID, UnitID: &unit, Progress: progress},
			UpdatedAt: web.ISOTime(now.Add(-ago).UnixMilli())}
		a.homeSt().stopped[art.ID+"#"+unit] = "The words it was left at, " + art.ID
	}
	left(arts[0], 0.5, 3*time.Hour)
	left(arts[1], 0.3, time.Hour)
	left(arts[2], 0.6, 2*time.Hour)
	left(arts[3], 0.4, 4*time.Hour)
	left(arts[4], 0.97, 0) // read to its end
	got := a.resumable(now)
	if len(got) != 3 || got[0] != arts[1] || got[1] != arts[2] || got[2] != arts[0] {
		t.Fatalf("the last left first, three at most, nothing read to its end: %v", got)
	}
	left(arts[3], 0.4, 40*24*time.Hour)
	if got := a.resumable(now); len(got) != 3 || slices.Contains(got, arts[3]) {
		t.Fatal("a month and more ago is not 接着读")
	}
	settle(tt)
	if _, ok := tt.AccessByRole(ui.RoleRegion, "接着读"); !ok {
		t.Fatal("no 接着读")
	}
	if !tt.HasText("…The words it was left at, " + arts[1].ID) {
		t.Error("it opens with the words it was left at")
	}
	must(t, tt.Click(locales.HomeResume(titleOf(arts[1].Title), percentOf(arts[1]))))
	tt.Frame()
	if a.page != (page{"reader", arts[1].ID}) || a.lay == nil {
		t.Fatalf("opened, it is the article: %v", a.page)
	}
}

// The words an article was left at: its unit's, or the first after it with words.
func TestStoppedAt(t *testing.T) {
	doc := json.RawMessage(`{"units":[{"id":"a","kind":"paragraph","text":"First."},{"id":"b","kind":"image","text":""},` +
		`{"id":"c","kind":"caption","text":"Source: x"},{"id":"d","kind":"paragraph","text":"  After   the picture. "}]}`)
	for unit, want := range map[string]string{"a": "First.", "b": "After the picture.", "c": "After the picture.", "z": ""} {
		if got := stoppedAt(doc, unit); got != want {
			t.Errorf("%s: %q, want %q", unit, got, want)
		}
	}
}

// homeAt scrolls 首页 so that region starts 120 below the window's top, as
// the previous implementation's captures do.
func homeAt(t *testing.T, a *App, tt *ui.Tester, region string) {
	t.Helper()
	hs := a.homeSt()
	for range 6 {
		r, ok := tt.AccessByRole(ui.RoleRegion, region)
		if !ok {
			t.Fatalf("no %s", region)
		}
		if r.Bounds.H > 0 && r.Bounds.Y > 119 && r.Bounds.Y < 121 {
			return
		}
		if r.Bounds.H > 0 && r.Bounds.Y > 0 {
			hs.scroll.Y += r.Bounds.Y - 120
		} else {
			hs.scroll.Y += 400
		}
		tt.Frame()
	}
}

// TestHomeLook writes 首页 as it shows, in the states the previous
// implementation's captures show (.context/mygo/app-v1/home/tools/electron.mjs),
// on the folder those captures read: HOMELOOK=dir HOMEDATA=folder go test -run HomeLook.
func TestHomeLook(t *testing.T) {
	dir, data := os.Getenv("HOMELOOK"), os.Getenv("HOMEDATA")
	if dir == "" || data == "" {
		t.Skip("HOMELOOK and HOMEDATA not set")
	}
	os.MkdirAll(dir, 0o755)
	a := newShotAppOn(t, data)
	tt := ui.NewTester(a.View, 1280, 850)
	hs := a.homeSt()
	shot := func(name string) {
		tt.Move(1270, 840)
		tt.Advance(2 * time.Second)
		tt.Frame()
		savePNG(t, filepath.Join(dir, name+"-mygo.png"), tt.Image())
	}
	settle(tt)
	tt.Advance(3 * time.Second) // the pictures come in
	shot("home-1-first")
	homeAt(t, a, tt, "流行")
	shot("home-2-trending")
	homeAt(t, a, tt, "订阅的作者")
	shot("home-3-follows")
	hs.scroll.Y = 1e6
	tt.Frame()
	shot("home-4-end")
	hs.scroll.Y = 0
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	tt.Type("Base UI")
	shot("home-5-search")
	hs.query = "没有这个词"
	shot("home-6-nomatch")
}

// TestHomeFrames writes the motions of 首页 as frames and GIFs: opening a
// post and back, and opening a followed author's piece and back:
// HOMEFRAMES=dir HOMEDATA=folder go test -run HomeFrames.
func TestHomeFrames(t *testing.T) {
	dir, data := os.Getenv("HOMEFRAMES"), os.Getenv("HOMEDATA")
	if dir == "" || data == "" {
		t.Skip("HOMEFRAMES and HOMEDATA not set")
	}
	os.MkdirAll(dir, 0o755)
	const step = 40 * time.Millisecond
	type phase struct {
		act  func(a *App, tt *ui.Tester)
		span time.Duration
	}
	clickTitle := func(region string, pick func(a *App) string) func(*App, *ui.Tester) {
		return func(a *App, tt *ui.Tester) { must(t, tt.Click(pick(a))) }
	}
	back := func(a *App, tt *ui.Tester) { tt.ClickAt(460, 30) } // ‹ 首页
	scenarios := []struct {
		name   string
		set    func(a *App, tt *ui.Tester)
		phases []phase
	}{
		{"home-open-post-back", func(a *App, tt *ui.Tester) { homeAt(t, a, tt, "流行") }, []phase{
			{clickTitle("流行", func(a *App) string { return trendingOf(a.ex.entries)[1].post.Title }), 900 * time.Millisecond},
			{back, 800 * time.Millisecond}}},
		{"home-open-follow-back", func(a *App, tt *ui.Tester) { homeAt(t, a, tt, "订阅的作者") }, []phase{
			{clickTitle("订阅的作者", func(a *App) string { return titleOf(followsOf(a.homeItems(""))[0].Title) }), 900 * time.Millisecond},
			{back, 900 * time.Millisecond}}},
	}
	for _, s := range scenarios {
		if only := os.Getenv("ONLY"); only != "" && !strings.Contains(s.name, only) {
			continue
		}
		a := newShotAppOn(t, data)
		tt := ui.NewTester(a.View, 1280, 850)
		tt.SetScale(0.5)
		settle(tt)
		tt.Advance(3 * time.Second)
		s.set(a, tt)
		tt.Move(1270, 845)
		tt.Advance(time.Second)
		var frames []*image.RGBA
		for _, ph := range s.phases {
			ph.act(a, tt)
			t0 := tt.Now()
			for d := time.Duration(0); d <= ph.span; d += step {
				tt.Advance(d - tt.Now().Sub(t0))
				frames = append(frames, tt.Image())
			}
			tt.Advance(time.Second)
			frames = append(frames, tt.Image())
		}
		writeSheet(t, filepath.Join(dir, s.name+".png"), frames)
		writeGIF(t, filepath.Join(dir, s.name+".gif"), frames, step)
	}
}

// Offline, before Explore was ever read: the page says nothing of what is
// missing; the first screen stands on the newest piece of someone followed,
// on its writer's own site, and opens it; without even that the people
// followed start the page.
func TestHomeOffline(t *testing.T) {
	a, tt, _, _ := newHomeApp(t, ui.Preferences{})
	ex := a.ex
	ex.entries, ex.loaded, ex.failed = nil, false, "offline"
	settle(tt)
	if tt.HasText("流行和设计例子暂时读不到，联网后会自己更新") {
		t.Error("no line about what cannot be read")
	}
	if _, ok := tt.AccessByRole(ui.RoleRegion, "今天的设计例子"); ok {
		t.Error("no design example without the catalog")
	}
	follows := followsOf(a.homeItems(""))
	if r, ok := tt.Find(titleOf(follows[0].Title)); !ok || r.Y > 200 {
		t.Errorf("with nothing to stand on, the people followed start the page: %v %v", ok, r)
	}
	art := follows[0]
	site := image.NewNRGBA(image.Rect(0, 0, 64, 40))
	a.favs().pics["site:"+art.SourceID] = &favPic{done: true, bmp: ui.NewBitmap(site), rest: 0.84, lit: 1}
	settle(tt)
	show, ok := tt.AccessByRole(ui.RoleRegion, "最新一篇")
	if !ok {
		t.Fatal("the first screen stands on someone followed")
	}
	tt.ClickAt(show.Bounds.X+show.Bounds.W/2, show.Bounds.Y+show.Bounds.H/3)
	tt.Frame()
	if a.page != (page{"reader", art.ID}) {
		t.Fatalf("it opens their piece: %v", a.page)
	}
	settle(tt)
	// From the keys the focus comes back to the first screen, not the piece's column.
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if a.page.kind != "home" {
		t.Fatalf("back to 首页: %v", a.page)
	}
	if b, ok := tt.AccessByRole(ui.RoleButton, titleOf(art.Title)); !ok || !b.Focused || b.Bounds.Y > 100 {
		t.Errorf("the focus is back on the first screen: %v %v %v", ok, b.Focused, b.Bounds)
	}
}
