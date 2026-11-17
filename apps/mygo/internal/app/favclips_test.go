package app

import (
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/reading"
	"trie/internal/subscription"
)

// seedFavClips keeps a spread of real articles on a copy of the library,
// which has five keeps and two clips: writers and design galleries, with
// and without pictures, eight authors in all; and clips passages of the
// kept bodies, from six people, some with a note. Times are fixed, so the
// order is too: Innei kept and was quoted last.
func seedFavClips(t testing.TB, store *library.Store) {
	t.Helper()
	keep := func(id, at string) library.FavoriteTime { return library.FavoriteTime{ID: id, FavoritedAt: &at} }
	nng := "https://nngroup.com/articles/%s?utm_source=rss&utm_medium=feed&utm_campaign=rss-syndication"
	_, err := store.Mutate(library.SetFavorites{Items: []library.FavoriteTime{
		keep("https://mitchellh.com/writing/superlogical", "2026-11-09T06:00:00.000Z"),
		keep("https://thefwa.com/w/nebulofi", "2026-11-09T07:00:00.000Z"),
		keep("https://thefwa.com/w/orvae", "2026-11-09T07:10:00.000Z"),
		keep("https://smashingmagazine.com/2026/11/modern-css-might-make-your-front-end-more-complex", "2026-11-09T08:00:00.000Z"),
		keep(fmt.Sprintf(nng, "agent-harness"), "2026-11-09T09:00:00.000Z"),
		keep(fmt.Sprintf(nng, "usability-101-introduction-to-usability"), "2026-11-09T09:10:00.000Z"),
		keep("https://awwwards.com/sites/justin-jefferson", "2026-11-10T08:00:00.000Z"),
		keep("https://awwwards.com/sites/viv", "2026-11-10T08:05:00.000Z"),
		keep("https://antfu.me/posts/async-sync-in-between", "2026-11-10T09:00:00.000Z"),
		keep("https://antfu.me/posts/hello-tokyo", "2026-11-10T09:05:00.000Z"),
		keep("https://antfu.me/posts/categorize-deps", "2026-11-10T09:10:00.000Z"),
		keep("https://antfu.me/posts/ecosystem-sponsorship-forwarding", "2026-11-10T09:15:00.000Z"),
		keep("https://antfu.me/posts/pluggable-extensible-playful-devtools", "2026-11-10T09:20:00.000Z"),
		keep("https://innei.in/posts/tech/ai-native-ios-e2e-verification", "2026-11-10T10:00:00.000Z"),
		keep("https://innei.in/posts/tech/lobehub-antd-to-base-ui-stylex-migration", "2026-11-10T10:05:00.000Z"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	clip := func(id, art, text, note, at string) library.AddClip {
		return library.AddClip{Clip: library.ClipInput{ID: id, ArticleID: art, Text: text, Note: note, CreatedAt: at}}
	}
	for _, a := range []library.AddClip{
		clip("5eed0000-0000-4000-8000-000000000001", "https://velasx.com/am/6498", "我说不是，都是徒手敲的，毕竟这几个东西在做的时候，ChatGPT还在娘胎里。", "", "2026-11-09T05:00:00.000Z"),
		clip("5eed0000-0000-4000-8000-000000000002", fmt.Sprintf(nng, "agent-harness"), "An agent harness (also called an agentic harness, or simply a harness) is the software around an AI model that runs its execution loop: it carries out the model’s requests to use tools, returns the results, and controls what information the model receives and what it is allowed to do.",
			"harness 的定义，写说明时引用", "2026-11-10T03:00:00.000Z"),
		clip("5eed0000-0000-4000-8000-000000000003", "https://mitchellh.com/writing/superlogical", "I've started a new company: Superlogical.", "", "2026-11-10T04:00:00.000Z"),
		clip("5eed0000-0000-4000-8000-000000000004", "https://mitchellh.com/writing/superlogical", "But I never stopped building. Building software is my personal passion, and I continued to learn and build a little every day just for the love of it.",
			"", "2026-11-10T04:05:00.000Z"),
		clip("5eed0000-0000-4000-8000-000000000005", "https://antfu.me/posts/categorize-deps", "The main difference is that devDependencies are only needed during the build or development phase, while dependencies are required for the project to run.",
			"", "2026-11-10T05:00:00.000Z"),
		clip("5eed0000-0000-4000-8000-000000000006", "https://antfu.me/posts/hello-tokyo", "Seven years ago, my first visit to Japan - a trip to 大阪(Osaka), 京都(Kyoto), and 北海道(Hokkaido) - planted the seed of wanting to live in Japan one day.",
			"", "2026-11-10T05:10:00.000Z"),
		clip("5eed0000-0000-4000-8000-000000000007", "https://antfu.me/posts/async-sync-in-between", "This restriction forces developers to propagate the \"color\" throughout their codebase.",
			"函数着色：一处 async，处处 async", "2026-11-10T05:20:00.000Z"),
		clip("5eed0000-0000-4000-8000-000000000008", "https://innei.in/posts/tech/react-native-to-uikit-migration", "On the night of October 24, I decided to remove React Native entirely and replace the remaining screens with UIKit one by one.",
			"", "2026-11-10T06:00:00.000Z"),
		clip("5eed0000-0000-4000-8000-000000000009", "https://innei.in/notes/2026/10/24/2026-10-recap", "从上次总结也已经过去了半个多月之久，是时候又来说说最近发生的一些事情。", "", "2026-11-10T06:10:00.000Z"),
		clip("5eed0000-0000-4000-8000-000000000010", "https://innei.in/notes/2026/10/24/2026-10-recap", "期间还顺手把 Nyatabi 正在用的那套圆润线条图标库开源了出来", "图标库，线条和圆角可以借鉴", "2026-11-10T06:20:00.000Z"),
		clip("5eed0000-0000-4000-8000-000000000011", "https://innei.in/posts/tech/ai-native-ios-e2e-verification", "前沿模型和 agent 正在从\"实现需求\"往\"验收业务需求\"走。", "", "2026-11-10T06:30:00.000Z"),
		clip("5eed0000-0000-4000-8000-000000000012", "https://innei.in/posts/tech/ai-native-ios-e2e-verification", "这个 loop 的问题在人这一侧。中间的截图、日志、无障碍树和测试输出都被 agent 自己读完、消化掉了，人能看到的通常只有最后一句\"已验证通过\"。",
			"验收要让人看得见，不只是 agent 说通过", "2026-11-11T02:00:00.000Z"),
	} {
		if _, err := store.Mutate(a); err != nil {
			t.Fatal(err)
		}
	}
}

// newSeededApp is newTestApp on the seeded library.
func newSeededApp(t testing.TB) (*App, *library.Store) {
	t.Helper()
	a, store := newTestApp(t)
	seedFavClips(t, store)
	lib, err := store.Library()
	if err != nil {
		t.Fatal(err)
	}
	a.accept(lib)
	return a, store
}

// TestSeedFavClips writes the seeded library to SEED/library.sqlite, with
// the first screens and pictures earlier sessions kept, for both apps'
// screenshots: SEED=dir go test -run SeedFavClips.
func TestSeedFavClips(t *testing.T) {
	dir := os.Getenv("SEED")
	if dir == "" {
		t.Skip("SEED not set")
	}
	must(t, os.MkdirAll(dir, 0o755))
	copyFile(t, libraryCopy, filepath.Join(dir, "library.sqlite"))
	store, err := library.Open(filepath.Join(dir, "library.sqlite"), library.Snapshot{Version: 1, AsOf: "2026-11-09"})
	must(t, err)
	seedFavClips(t, store)
	must(t, store.Close())
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	must(t, err)
	defer src.Close()
	dst, err := os.Create(to)
	must(t, err)
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	must(t, dst.Close())
}

// seededFolder is the seeded library folder TestSeedFavClips writes, with
// first screens and pictures, which the screenshots run on.
const seededFolder = "/tmp/bs-favclips/seed"

// newShotApp is the app on a copy of the seeded folder, its first screens
// read by Explore, its pictures from the folder's thumbnails, offline.
func newShotApp(t *testing.T) *App { return newShotAppOn(t, seededFolder) }

// newShotAppOn is newShotApp on folder, with the Explore cache, icons and
// works it holds.
func newShotAppOn(t *testing.T, folder string) *App {
	t.Helper()
	dir := t.TempDir()
	copyFile(t, filepath.Join(folder, "library.sqlite"), filepath.Join(dir, "library.sqlite"))
	for _, name := range []string{"library.sqlite-wal", "explore-cache.json", "explore-icons.json"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err == nil {
			copyFile(t, filepath.Join(folder, name), filepath.Join(dir, name))
		}
	}
	for _, sub := range []string{"explore-shots", "thumbs", "explore-works"} {
		must(t, os.MkdirAll(filepath.Join(dir, sub), 0o755))
		files, _ := os.ReadDir(filepath.Join(folder, sub))
		for _, f := range files {
			copyFile(t, filepath.Join(folder, sub, f.Name()), filepath.Join(dir, sub, f.Name()))
			info, _ := os.Stat(filepath.Join(folder, sub, f.Name()))
			os.Chtimes(filepath.Join(dir, sub, f.Name()), info.ModTime(), info.ModTime())
		}
	}
	store, err := library.Open(filepath.Join(dir, "library.sqlite"), library.Snapshot{Version: 1, AsOf: "2026-11-09"})
	must(t, err)
	t.Cleanup(func() { store.Close() })
	reader := reading.New(store, offline, reading.Worker)
	var ex *explore.Service
	subs := subscription.New(store, offline, func(home string) *string { return ex.IconFor(home) },
		func(feedURL, home string) string { return ex.NameFor(&feedURL, home) })
	ex = explore.Open("../../resources/explore", dir, store, subs, reader, nil)
	a, err := New(Options{DataDir: dir, Store: store, Reading: reader, Subscriptions: subs, Explore: ex, Sync: true})
	must(t, err)
	return a
}

// TestFavClipsLook writes 收藏 and 摘录 as they show, the same states the
// previous implementation's captures show (.context/mygo/app-v1/favclips/tools/electron.mjs):
// LOOK=dir go test -run FavClipsLook.
func TestFavClipsLook(t *testing.T) {
	dir := os.Getenv("LOOK")
	if dir == "" {
		t.Skip("LOOK not set")
	}
	os.MkdirAll(dir, 0o755)
	a := newShotApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	shot := func(name string) {
		tt.Advance(2 * time.Second)
		savePNG(t, filepath.Join(dir, name+"-mygo.png"), tt.Image())
	}
	away := func() { tt.Move(1270, 840) }
	scroll := func(st *ui.ScrollState, y float32) {
		st.ScrollTo(0, y, ui.Animation{Duration: time.Nanosecond})
		tt.Frame()
	}
	settle(tt)
	must(t, tt.Click("收藏"))
	away()
	shot("fav-1-shelves")
	scroll(&a.favs().shelves, 700)
	away()
	shot("fav-2-down")
	scroll(&a.favs().shelves, 0)
	settle(tt)
	must(t, tt.Click(inneiShelf))
	away()
	shot("fav-3-author")
	must(t, tt.Click("摘录"))
	away()
	shot("clips-1")
	scroll(&a.clipsSt().scroll, 600)
	away()
	shot("clips-2-down")
	scroll(&a.clipsSt().scroll, 0)
	settle(tt)
	r, _ := tt.Find(leadText)
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	shot("clips-3-tools")
	must(t, tt.Click(leadText))
	away()
	shot("clips-4-card")
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	must(t, tt.Click("收藏"))
	settle(tt)
	tt.Key(0, ui.KeyEscape) // the tab came back to Innei
	settle(tt)
	must(t, tt.Click(antfuShelf))
	settle(tt)
	first := "「Pluggable, Extensible, and Playful DevTools」的收藏操作"
	m, _ := tt.Find(first)
	tt.Move(m.X+m.W/2, m.Y+m.H/2)
	tt.Advance(300 * time.Millisecond)
	must(t, tt.Click(first))
	tt.Advance(500 * time.Millisecond)
	must(t, tt.Click("取消收藏"))
	tt.Move(1270, 600)
	tt.Advance(time.Second)
	savePNG(t, filepath.Join(dir, "fav-4-letgo-mygo.png"), tt.Image())
	must(t, tt.Click("撤销"))
	tt.Advance(1200 * time.Millisecond)
	savePNG(t, filepath.Join(dir, "fav-5-undo-mygo.png"), tt.Image())
}

const (
	inneiShelf = "Innei，收藏 4 篇"
	antfuShelf = "Anthony Fu，收藏 5 篇"
	asyncID    = "https://antfu.me/posts/async-sync-in-between"
	ecoID      = "https://antfu.me/posts/ecosystem-sponsorship-forwarding"
	ecoTitle   = "Ecosystem Sponsorship Forwarding"
	leadClip   = "5eed0000-0000-4000-8000-000000000012"
	leadText   = "这个 loop 的问题在人这一侧。中间的截图、日志、无障碍树和测试输出都被 agent 自己读完、消化掉了，人能看到的通常只有最后一句\"已验证通过\"。"
)

// cardCopy is where the stage's 复制引文 is: the last of its foot, at the
// foot's right end (the clip bars over the page behind it have the same
// name).
func cardCopy(a *App) (x, y float32) {
	r := a.clips.cardBox
	return r.X + r.W - 40, r.Y + r.H - 16
}

// cardCite is where the stage's article title is: just after its 《 (the
// page behind it names the article too).
func cardCite(t *testing.T, tt *ui.Tester) (x, y float32) {
	r, ok := tt.Find("《")
	if !ok {
		t.Fatal("the card names no article")
	}
	return r.X + r.W + 24, r.Y + r.H/2
}

// toArticles zooms 收藏's map into the articles, where the writers are.
func toArticles(t *testing.T, a *App, tt *ui.Tester) {
	t.Helper()
	_, groups := a.kept("")
	for _, b := range mapBlocks(groups) {
		if b.key == articles {
			must(t, tt.Click(locales.FavoritesWriterCount(blockName(b), b.n)))
			settle(tt)
			return
		}
	}
	t.Fatal("the map has no articles")
}

func kept(a *App, id string) bool {
	art := a.article(id)
	return art != nil && art.Favorite
}

// 收藏: the authors, kept last first, each a picture; into an author, whose
// keeps are cards, and back; the tab remembers the author; an article
// opened from a card comes back to it, its title travelling home.
func TestFavoritesPages(t *testing.T) {
	a, _ := newSeededApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("收藏"))
	settle(tt)
	if a.page != (page{kind: "favorites"}) || !tt.HasText(lobeTitle) {
		t.Fatalf("the map: page %v, %q", a.page, tt.Texts())
	}
	toArticles(t, a, tt)
	if ri, _ := tt.Find(inneiShelf); ri.Y > 160 {
		t.Errorf("the author kept last does not stand first among the articles: %v", ri)
	}
	must(t, tt.Click(inneiShelf))
	tt.Advance(50 * time.Millisecond)
	if m := a.came; a.page != (page{"favorites", "innei"}) || m == nil || m.depth != motion.Deep || m.kbd {
		t.Fatalf("into Innei: page %v, crossing %+v", a.page, m)
	}
	settle(tt)
	for _, title := range []string{lobeTitle, "我的 AI Native iOS 开发验证工作流", "十月未央"} {
		if _, ok := tt.Find(title); !ok {
			t.Errorf("Innei's page has no card %q", title)
		}
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if a.page != (page{kind: "favorites"}) {
		t.Fatalf("Esc: page %v", a.page)
	}
	// The tab comes back to the author shown last.
	must(t, tt.Click(inneiShelf))
	settle(tt)
	must(t, tt.Click("摘录"))
	settle(tt)
	must(t, tt.Click("收藏"))
	settle(tt)
	if a.page != (page{"favorites", "innei"}) {
		t.Fatalf("back on 收藏: page %v", a.page)
	}
	// An article from a card, and back into it.
	must(t, tt.Click(lobeTitle))
	tt.Advance(50 * time.Millisecond)
	if a.page != (page{"reader", lobehub}) || a.lay == nil || len(a.flights) == 0 {
		t.Fatalf("opening: page %v, ground %v, flights %d", a.page, a.lay != nil, len(a.flights))
	}
	settle(tt)
	tt.ClickAt(460, 30) // ‹ 收藏
	tt.Advance(50 * time.Millisecond)
	if a.page != (page{"favorites", "innei"}) || a.layRow != lobehub || len(a.flights) == 0 {
		t.Fatalf("back: page %v, ground into %q, flights %d", a.page, a.layRow, len(a.flights))
	}
	tt.Advance(100 * time.Millisecond)
	if a.lay != nil && a.lay.To.W <= 0 {
		t.Error("the ground did not find its card")
	}
	settle(tt)
	if a.lay != nil || len(a.flights) != 0 || tt.FramePending() {
		t.Error("the crossing does not end")
	}
}

// Letting go asks nothing; the notice offers 撤销 (and ⌘Z), which puts the
// keeps back with the time they were kept. An author's whole shelf lifts
// their face onto the notice and back.
func TestFavoritesLetGoAndUndo(t *testing.T) {
	a, _ := newSeededApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("收藏"))
	settle(tt)
	toArticles(t, a, tt)
	must(t, tt.Click(antfuShelf))
	settle(tt)
	before := *a.article(ecoID).FavoritedAt
	must(t, tt.Click("「"+ecoTitle+"」的收藏操作"))
	tt.Frame()
	must(t, tt.Click("取消收藏"))
	settle(tt)
	if kept(a, ecoID) || !tt.HasText("已取消收藏「Ecosystem Sponsor…」") {
		t.Fatalf("let go: kept %v, notice %q", kept(a, ecoID), tt.Texts())
	}
	if _, ok := tt.Find(ecoTitle); ok {
		t.Error("its card is still there")
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	settle(tt)
	if !kept(a, ecoID) || *a.article(ecoID).FavoritedAt != before {
		t.Fatalf("⌘Z: kept %v at %v, was %v", kept(a, ecoID), a.article(ecoID).FavoritedAt, before)
	}
	if _, ok := tt.Find(ecoTitle); !ok {
		t.Error("its card did not come back")
	}
	// The whole shelf, from the shelves.
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	must(t, tt.Click("Anthony Fu 的收藏操作"))
	tt.Frame()
	must(t, tt.Click("取消收藏全部 5 篇"))
	tt.Advance(30 * time.Millisecond)
	if !a.tripping("notice") {
		t.Error("the face does not fly onto the notice")
	}
	settle(tt)
	if kept(a, asyncID) || !tt.HasText("已取消收藏 Anthony Fu 的 5 篇") {
		t.Fatalf("the shelf: kept %v, %q", kept(a, asyncID), tt.Texts())
	}
	must(t, tt.Click("撤销"))
	tt.Advance(30 * time.Millisecond)
	settle(tt)
	if !kept(a, asyncID) {
		t.Fatal("撤销 did not put the shelf back")
	}
	if _, ok := tt.Find(antfuShelf); !ok {
		t.Error("the shelf did not come back")
	}
}

// The keys: J and K walk the cards, Enter goes into an author (the focus
// on their first keep), ⌫ lets a keep go (the focus to the next), ⌘Z puts
// it back, Esc goes back to the author, ⌘⌫ lets their shelf go. Nothing
// moves or flies.
func TestFavoritesKeyboard(t *testing.T) {
	a, _ := newSeededApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	tt.Key(ui.Cmd, ui.Key2)
	settle(tt)
	// J walks the map's blocks to the articles; Enter zooms in, the focus on
	// the first writer; J walks on: Innei's sheet, its titles, then Anthony Fu's.
	_, groups := a.kept("")
	words := mapBlocks(groups)[len(mapBlocks(groups))-1]
	label := locales.FavoritesWriterCount(blockName(words), words.n)
	for range 8 {
		if tt.Focused(label) {
			break
		}
		tt.Key(0, ui.KeyJ)
		tt.Frame()
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Frame()
	if a.favs().kind != articles || !tt.Focused(inneiShelf) {
		t.Fatalf("Enter on the articles: zoomed into %q, cursor %q", a.favs().kind, a.favs().cursor)
	}
	for range 8 {
		if tt.Focused(antfuShelf) {
			break
		}
		tt.Key(0, ui.KeyJ)
		tt.Frame()
	}
	if !tt.Focused(antfuShelf) {
		t.Fatalf("J: the focus never reached Anthony Fu (cursor %q)", a.favs().cursor)
	}
	tt.Key(0, ui.KeyEnter)
	if a.page != (page{"favorites", "antfu"}) || a.left != nil {
		t.Fatalf("Enter: page %v, a crossing %v", a.page, a.left != nil)
	}
	tt.Frame()
	tt.Frame()
	first := "Pluggable, Extensible, and Playful DevTools"
	if !tt.Focused(first) {
		t.Fatalf("the focus is not on the first keep (cursor %q)", a.favs().cursor)
	}
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	tt.Frame()
	if kept(a, "https://antfu.me/posts/pluggable-extensible-playful-devtools") || a.tripping("notice") {
		t.Fatalf("⌫: still kept, or a face flies")
	}
	if !tt.Focused("Ecosystem Sponsorship Forwarding") {
		t.Errorf("the focus did not go on to the next keep (cursor %q)", a.favs().cursor)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if !kept(a, "https://antfu.me/posts/pluggable-extensible-playful-devtools") {
		t.Fatal("⌘Z did not put it back")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Frame()
	if a.page != (page{kind: "favorites"}) || !tt.Focused(antfuShelf) {
		t.Fatalf("Esc: page %v, focus on the author %v", a.page, tt.Focused(antfuShelf))
	}
	tt.Key(ui.Cmd, ui.KeyBackspace)
	tt.Frame()
	tt.Frame()
	if kept(a, asyncID) || len(a.trips) != 0 {
		t.Fatalf("⌘⌫: the shelf is still kept, or a face flies")
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if !kept(a, asyncID) {
		t.Fatal("⌘Z did not put the shelf back")
	}
}

// 摘录: the person quoted last on the opening spread; a passage opens its
// card, ← → turn it, Esc closes it; deleting asks nothing and the notice
// (or ⌘Z) puts the passage back; the card's title opens the article at
// the passage and comes back into it.
func TestClipsPage(t *testing.T) {
	pinNow(t)
	a, _ := newSeededApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("摘录"))
	settle(tt)
	lead, ok := tt.Find(leadText)
	if !ok || !tt.HasText("Innei") || !tt.HasText("《我的 AI Native iOS 开发验证工作流》") {
		t.Fatalf("the spread: %v %q", ok, tt.Texts())
	}
	// The card.
	must(t, tt.Click(leadText))
	settle(tt)
	cs := a.clips
	if cs.open != leadClip || !tt.HasText("摘于 11月11日") || !tt.HasText("1 / 14") {
		t.Fatalf("the card: open %q, %q", cs.open, tt.Texts())
	}
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if cs.open == leadClip || !tt.HasText("2 / 14") {
		t.Fatalf("→: still on %q", cs.open)
	}
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	if cs.open != leadClip {
		t.Fatalf("←: on %q", cs.open)
	}
	tt.ClickAt(cardCopy(a))
	tt.Frame()
	if got := tt.Clipboard(); !strings.Contains(got, "> "+leadText) || !strings.Contains(got, "想法：验收要让人看得见") {
		t.Errorf("复制引文 copied %q", got)
	}
	if !tt.HasText("已复制") {
		t.Error("the button does not say 已复制")
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if cs.open != "" {
		t.Fatal("Esc did not close the card")
	}
	// Deleting from the clip bar over the passage, and 撤销.
	tt.Move(lead.X+lead.W/2, lead.Y+lead.H/2)
	tt.Advance(300 * time.Millisecond)
	right, top := lead.X+lead.W, lead.Y
	tt.ClickAt(right-16, top-12) // 删除, at the passage's corner
	settle(tt)
	if slices.ContainsFunc(a.lib.Clips, func(cl library.Clip) bool { return cl.ID == leadClip }) || !tt.HasText("已删除摘录") {
		t.Fatalf("delete: %d clips, %q", len(a.lib.Clips), tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	settle(tt)
	if !slices.ContainsFunc(a.lib.Clips, func(cl library.Clip) bool { return cl.ID == leadClip }) {
		t.Fatal("⌘Z did not put the passage back")
	}
	// The card's title opens the article, and back lands on the passage.
	must(t, tt.Click(leadText))
	settle(tt)
	tt.ClickAt(cardCite(t, tt))
	tt.Advance(50 * time.Millisecond)
	ai := "https://innei.in/posts/tech/ai-native-ios-e2e-verification"
	if a.page != (page{"reader", ai}) || a.lay == nil || len(a.flights) == 0 || cs.open != "" {
		t.Fatalf("the article: page %v, ground %v, flights %d, card %q", a.page, a.lay != nil, len(a.flights), cs.open)
	}
	settle(tt)
	tt.ClickAt(460, 30) // ‹ 摘录
	tt.Advance(100 * time.Millisecond)
	if a.page != (page{kind: "clips"}) || a.lay == nil || a.lay.To.W <= 0 {
		t.Fatalf("back: page %v, ground into the passage %v", a.page, a.lay != nil && a.lay.To.W > 0)
	}
	settle(tt)
	if !tt.Focused(leadText) {
		t.Error("the passage did not take the focus back")
	}
}

// A passage takes the stage: its shared parts travel on one value while
// the stage's own parts wait until they are nearly there; the pointer
// resting in the dark lifts the card it came from; Esc sends it back on
// the way it came, quicker, and the passage takes the focus back. From the
// keys it comes and goes at once; under less motion it fades where it
// stands while the page goes dark at once.
func TestClipStage(t *testing.T) {
	a, _ := newSeededApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("摘录"))
	settle(tt)
	lead, _ := tt.Find(leadText)
	tt.Move(lead.X+lead.W/2, lead.Y+lead.H/2)
	tt.Advance(300 * time.Millisecond)
	tt.ClickAt(lead.X+lead.W/2, lead.Y+lead.H/2)
	tt.Advance(120 * time.Millisecond)
	cs := a.clips
	s := cs.stage
	if s == nil || !s.built || s.from.quote.W <= 0 || s.from.head.W <= 0 || s.from.card.W <= 0 {
		t.Fatalf("the stage should know where the passage came from: %+v", s)
	}
	q, own := s.values(tt.Now())
	if q <= 0.2 || q >= motion.StageLand || own != 0 {
		t.Errorf("120 ms in, the words should be on their way and the stage's own parts not yet in: q %.2f, own %.2f", q, own)
	}
	tt.Advance(time.Second)
	if q, own = s.values(tt.Now()); q != 1 || own != 1 || s.dark(q, own) != 1 {
		t.Fatalf("landed: q %.2f, own %.2f", q, own)
	}
	// The pointer resting in the dark lifts the card it came from.
	tt.Move(1110, 650)
	tt.Advance(300 * time.Millisecond)
	if !tt.HasText("1 / 14") || a.clips.cardBox.W <= 0 {
		t.Fatalf("the stage: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Advance(200 * time.Millisecond)
	if s.open || cs.stage != s {
		t.Fatal("Esc should send the passage back, on its way")
	}
	if q, _ = s.values(tt.Now()); q <= 0 || q >= 1 {
		t.Errorf("200 ms after Esc the words should be on their way back: q %.2f", q)
	}
	tt.Advance(400 * time.Millisecond)
	if cs.stage != nil || !tt.Focused(leadText) {
		t.Fatalf("back: stage %v, the passage focused %v", cs.stage != nil, tt.Focused(leadText))
	}
	// From the keys: at once both ways.
	a.kbd = true
	a.showClip(leadClip)
	tt.Frame()
	if s = cs.stage; s == nil || !s.keyed {
		t.Fatal("the keys should put the passage on the stage at once")
	}
	if q, own = s.values(tt.Now()); q != 1 || own != 1 {
		t.Errorf("from the keys: q %.2f, own %.2f", q, own)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Frame()
	if cs.stage != nil {
		t.Fatal("from the keys, Esc should take it back at once")
	}
	// Less motion: it fades in where it stands; the page is dark at once.
	a.Reduce = true
	a.kbd = false
	tt.ClickAt(lead.X+lead.W/2, lead.Y+lead.H/2)
	tt.Advance(60 * time.Millisecond)
	if s = cs.stage; s == nil || !s.still {
		t.Fatal("under less motion the passage should take the stage where it stands")
	}
	if q, own = s.values(tt.Now()); q != 1 || own <= 0 || own >= 1 || s.dark(q, own) != 1 {
		t.Errorf("less motion, 60 ms in: q %.2f, own %.2f, dark %.2f", q, own, s.dark(q, own))
	}
}

// Under less motion nothing travels or flies: the picture does not fold
// (it only fades with its page), no face flies onto the notice, a copy's
// wash only fades.
func TestFavClipsReducedMotion(t *testing.T) {
	a, _ := newSeededApp(t)
	tt := ui.NewTesterWith(a.View, 1280, 850, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	settle(tt)
	must(t, tt.Click("收藏"))
	settle(tt)
	toArticles(t, a, tt)
	must(t, tt.Click(lobeTitle)) // a title on Innei's sheet
	tt.Advance(50 * time.Millisecond)
	if a.page != (page{"reader", lobehub}) || len(a.flights) != 0 || a.lay == nil || !a.lay.Still {
		t.Fatalf("opening: page %v, flights %d, ground still %v", a.page, len(a.flights), a.lay != nil && a.lay.Still)
	}
	settle(tt)
	tt.ClickAt(460, 30)
	settle(tt)
	must(t, tt.Click("Innei 的收藏操作"))
	tt.Frame()
	must(t, tt.Click("取消收藏全部 4 篇"))
	tt.Advance(30 * time.Millisecond)
	if len(a.trips) != 0 || kept(a, lobehub) {
		t.Fatalf("let go: trips %d, kept %v", len(a.trips), kept(a, lobehub))
	}
	settle(tt)
	must(t, tt.Click("摘录"))
	settle(tt)
	must(t, tt.Click(leadText))
	settle(tt)
	tt.ClickAt(cardCopy(a))
	tt.Frame()
	if w, ok := a.clips.washes["card"]; !ok || !w.fade {
		t.Errorf("the wash: %+v %v (it only fades)", w, ok)
	}
}

// TestFavClipsFrames records 收藏's and 摘录's crossings frame by frame, a
// frame every 40 ms at half size, as GIFs and contact sheets:
// FAVFRAMES=dir go test -run FavClipsFrames.
func TestFavClipsFrames(t *testing.T) {
	dir := os.Getenv("FAVFRAMES")
	if dir == "" {
		t.Skip("FAVFRAMES not set")
	}
	os.MkdirAll(dir, 0o755)
	const step = 40 * time.Millisecond
	type phase struct {
		act  func(a *App, tt *ui.Tester)
		span time.Duration
	}
	click := func(s string) func(*App, *ui.Tester) {
		return func(a *App, tt *ui.Tester) { must(t, tt.Click(s)) }
	}
	at := func(x, y float32) func(*App, *ui.Tester) { return func(a *App, tt *ui.Tester) { tt.ClickAt(x, y) } }
	rest := func(tt *ui.Tester) { tt.Move(1270, 845); tt.Advance(2 * time.Second) }
	scenarios := []struct {
		name   string
		set    func(a *App, tt *ui.Tester)
		phases []phase
	}{
		{"fav-enter-back", func(a *App, tt *ui.Tester) { must(t, tt.Click("收藏")) },
			[]phase{{click(inneiShelf), 900 * time.Millisecond}, {at(285, 30), 760 * time.Millisecond}}},
		{"fav-letgo-undo", func(a *App, tt *ui.Tester) {
			must(t, tt.Click("收藏"))
			rest(tt)
			tt.Scroll(700, 500, 0, 760)
			rest(tt)
			must(t, tt.Click("Anthony Fu 的收藏操作"))
			tt.Advance(time.Second)
		}, []phase{{func(a *App, tt *ui.Tester) { must(t, tt.Click("取消收藏全部 5 篇")) }, 1100 * time.Millisecond},
			{click("撤销"), 1100 * time.Millisecond}}},
		{"clips-open-back", func(a *App, tt *ui.Tester) { must(t, tt.Click("摘录")) },
			[]phase{{click(leadText), 640 * time.Millisecond}, {func(a *App, tt *ui.Tester) { tt.ClickAt(cardCite(t, tt)) }, 960 * time.Millisecond},
				{at(460, 30), 760 * time.Millisecond}}},
		{"clips-copy-delete-undo", func(a *App, tt *ui.Tester) { must(t, tt.Click("摘录")) }, []phase{
			{func(a *App, tt *ui.Tester) {
				// The pointer comes onto the passage, its clip bar shows, and 复制引文 is pressed.
				r, _ := tt.Find(leadText)
				tt.Move(r.X+r.W/2, r.Y+r.H/2)
				tt.Advance(400 * time.Millisecond)
				tt.ClickAt(r.X+r.W-44, r.Y-12)
			}, 1200 * time.Millisecond},
			{func(a *App, tt *ui.Tester) { r, _ := tt.Find(leadText); tt.ClickAt(r.X+r.W-16, r.Y-12) }, 600 * time.Millisecond}, // 删除
			{func(a *App, tt *ui.Tester) { tt.Key(ui.Cmd, ui.KeyZ) }, 600 * time.Millisecond}}},
	}
	for _, s := range scenarios {
		if only := os.Getenv("ONLY"); only != "" && !strings.Contains(s.name, only) {
			continue
		}
		a := newShotApp(t)
		tt := ui.NewTester(a.View, 1280, 850)
		tt.SetScale(0.5)
		settle(tt)
		s.set(a, tt)
		rest(tt)
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

// Searching: 收藏 lists the keeps it finds as cards, with whose they are
// and how many; 摘录 says when nothing matches. With nothing kept or
// clipped, each page shows its pictogram, a short title and one line.
func TestFavClipsSearchAndEmpty(t *testing.T) {
	a, store := newSeededApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	tt.Key(ui.Cmd, ui.Key2)
	tt.Key(ui.Cmd, ui.KeyF)
	settle(tt)
	tt.Type("tokyo")
	settle(tt)
	if !tt.HasText("匹配 1 篇") || !tt.HasText("Hello Tokyo!") || !tt.HasText("Anthony Fu") || tt.HasText(lobeTitle) {
		t.Errorf("searching 收藏: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	tt.Key(ui.Cmd, ui.Key4)
	tt.Key(ui.Cmd, ui.KeyF)
	settle(tt)
	tt.Type("nothing like this")
	settle(tt)
	if !tt.HasText("没有找到「nothing like this」") {
		t.Errorf("searching 摘录: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	// Nothing kept, nothing clipped.
	lib, err := store.Library()
	must(t, err)
	off := library.SetFavorites{}
	for _, art := range lib.Articles {
		if art.Favorite {
			off.Items = append(off.Items, library.FavoriteTime{ID: art.ID})
		}
	}
	_, err = store.Mutate(off)
	must(t, err)
	for _, cl := range lib.Clips {
		_, err = store.Mutate(library.DeleteClip{ID: cl.ID})
		must(t, err)
	}
	lib, err = store.Library()
	must(t, err)
	a.accept(lib)
	settle(tt)
	if !tt.HasText("留住一句好文字") || !tt.HasText("摘下的段落连同出处，都收在这里。") {
		t.Errorf("摘录 empty: %q", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.Key2)
	settle(tt)
	if !tt.HasText("留一篇，慢慢读") || !tt.HasText("收藏的文章按作者收在这里。") {
		t.Errorf("收藏 empty: %q", tt.Texts())
	}
}

// TestClipStageFrames records a passage taking the stage on 摘录 and going
// back, a frame every 33 ms at 2x and at 1x, into CLIPSTAGE (a folder): the
// lead opening, a hold with the pointer in the dark, closing by a click in
// the dark and by Esc, turning to the next passage with the pointer and
// with →, closing from there, and a passage beside the lead opening and
// closing. Each run is a
// folder of frames and a contact sheet of them at 640 wide (SCALE=1 or 2
// records one scale): CLIPSTAGE=dir go test -run ClipStageFrames.
func TestClipStageFrames(t *testing.T) {
	dir := os.Getenv("CLIPSTAGE")
	if dir == "" {
		t.Skip("CLIPSTAGE not set")
	}
	pinNow(t)
	const step = 33 * time.Millisecond
	const side = "期间还顺手把 Nyatabi 正在用的那套圆润线条图标库开源了出来" // beside the lead
	type phase struct {
		name string
		act  func(tt *ui.Tester)
		span time.Duration
	}
	over := func(tt *ui.Tester, text string) (x, y float32) {
		r, ok := tt.Find(text)
		if !ok {
			t.Fatalf("no %q", text)
		}
		x, y = r.X+r.W/2, r.Y+r.H/2
		tt.Move(x, y)
		tt.Advance(300 * time.Millisecond)
		return x, y
	}
	open := func(text string) func(*ui.Tester) {
		return func(tt *ui.Tester) { tt.ClickAt(over(tt, text)) }
	}
	rest := func(tt *ui.Tester) { tt.Advance(time.Second) }
	phases := []phase{
		{"1-open", open(leadText), 700 * time.Millisecond},
		{"2-dark-hold", func(tt *ui.Tester) { tt.Move(1110, 650) }, 330 * time.Millisecond},
		{"3-close-click", func(tt *ui.Tester) { tt.ClickAt(1110, 650) }, 500 * time.Millisecond},
		{"4-open-again", open(leadText), 0},
		{"5-close-esc", func(tt *ui.Tester) { tt.Move(1110, 650); tt.Advance(300 * time.Millisecond); tt.Key(0, ui.KeyEscape) }, 500 * time.Millisecond},
		{"6-open-again", open(leadText), 0},
		{"7-turn-pointer", func(tt *ui.Tester) { tt.ClickAt(over(tt, locales.ClipsNext())) }, 330 * time.Millisecond},
		{"8-turn-key", func(tt *ui.Tester) { tt.Key(0, ui.KeyRight) }, 99 * time.Millisecond},
		{"9-close-turned", func(tt *ui.Tester) { tt.ClickAt(1110, 650) }, 500 * time.Millisecond},
		{"10-open-side", open(side), 700 * time.Millisecond},
		{"11-close-side", func(tt *ui.Tester) { tt.ClickAt(1110, 650) }, 500 * time.Millisecond},
	}
	for _, scale := range []float32{2, 1} {
		if only := os.Getenv("SCALE"); only != "" && only != fmt.Sprint(scale) {
			continue
		}
		a, _ := newSeededApp(t)
		tt := ui.NewTester(a.View, 1280, 850)
		tt.SetScale(scale)
		settle(tt)
		must(t, tt.Click("摘录"))
		tt.Move(1270, 845)
		settle(tt)
		for _, ph := range phases {
			ph.act(tt)
			if ph.span == 0 {
				rest(tt)
				continue
			}
			out := filepath.Join(dir, fmt.Sprintf("%gx", scale), ph.name)
			must(t, os.MkdirAll(out, 0o755))
			var sheet []*image.RGBA
			t0 := tt.Now()
			for d := time.Duration(0); d <= ph.span; d += step {
				tt.Advance(d - tt.Now().Sub(t0))
				m := tt.Image()
				savePNG(t, filepath.Join(out, fmt.Sprintf("%03dms.png", d/time.Millisecond)), m)
				for m.Bounds().Dx() > 640 {
					m = halve(m)
				}
				sheet = append(sheet, m)
			}
			writeSheet(t, filepath.Join(dir, fmt.Sprintf("%gx-%s.png", scale, ph.name)), sheet)
			rest(tt)
		}
		savePNG(t, filepath.Join(dir, fmt.Sprintf("%gx-end.png", scale)), tt.Image())
	}
}
