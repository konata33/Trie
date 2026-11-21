package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/fetch"
	"trie/internal/reading"
)

const (
	lobehub   = "https://innei.in/posts/tech/lobehub-antd-to-base-ui-stylex-migration"
	lobeTitle = "十个月，给 LobeHub 换掉 Ant Design：向 Base UI 与 StyleX 的渐进式迁移"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// The sidebar changes the content area's page: an author's page, 探索,
// and back to 首页.
func TestPagesChange(t *testing.T) {
	pinNow(t)
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	if tt.HasText("2026") {
		t.Fatal("Home shows a list; it waits for its design and stays empty")
	}
	must(t, tt.Click("Innei"))
	tt.Advance(100 * time.Millisecond)
	if a.page != (page{"author", "innei"}) || len(a.flights) == 0 {
		t.Errorf("into an author: page %v, %d flights (the name travels)", a.page, len(a.flights))
	}
	settle(tt)
	_, named := tt.AccessByRole(ui.RoleHeading, "Innei")
	if !named || !tt.HasText("11月4日") || len(a.flights) != 0 || a.left != nil {
		t.Errorf("Innei's page: %v %v, %d flights, left %v", named, tt.HasText("11月4日"), len(a.flights), a.left)
	}
	if acc, ok := tt.AccessByRole(ui.RoleButton, "Innei"); !ok || acc.Current != ui.CurrentPage {
		t.Errorf("the sidebar row is not current: %+v", acc)
	}
	must(t, tt.Click("探索"))
	settle(tt)
	if _, named := tt.AccessByRole(ui.RoleHeading, "Innei"); a.page.kind != "explore" || named {
		t.Errorf("Explore: page %v, author still showing %v", a.page, named)
	}
	must(t, tt.Click("首页"))
	settle(tt)
	if a.page.kind != "home" {
		t.Errorf("Home: page %v", a.page)
	}
}

// An article opens from its row, its ground spreading and its title
// travelling, and goes back to the row the same way.
func TestOpenArticleAndBack(t *testing.T) {
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("Innei"))
	settle(tt)
	must(t, tt.Click(lobeTitle))
	tt.Advance(50 * time.Millisecond)
	if a.page != (page{"reader", lobehub}) || a.lay == nil || len(a.flights) == 0 {
		t.Fatalf("opening: page %v, ground %v, %d flights", a.page, a.lay != nil, len(a.flights))
	}
	settle(tt)
	if !tt.HasText("起点是一轮性能优化") || a.lay != nil || len(a.flights) != 0 {
		t.Errorf("the reader: body %v, ground %v, flights %d", tt.HasText("起点是一轮性能优化"), a.lay != nil, len(a.flights))
	}
	tt.ClickAt(460, 30) // the bar's back button, ‹ Innei
	tt.Advance(50 * time.Millisecond)
	if a.page != (page{"author", "innei"}) || a.lay == nil || a.layRow != lobehub {
		t.Errorf("going back: page %v, ground %v into %q", a.page, a.lay != nil, a.layRow)
	}
	tt.Advance(100 * time.Millisecond)
	if a.lay != nil && a.lay.To.W <= 0 {
		t.Error("the ground did not find its row")
	}
	settle(tt)
	if _, named := tt.AccessByRole(ui.RoleHeading, "Innei"); !named || a.lay != nil || len(a.flights) != 0 {
		t.Errorf("back on the author's page: %v, ground %v, flights %d", named, a.lay != nil, len(a.flights))
	}
	if tt.FramePending() {
		t.Error("the window keeps asking for frames once the crossing is over")
	}
}

// The reader keeps its place: in the library half a second after it stops,
// and back where it was on returning, in this session and the next.
func TestReadingPosition(t *testing.T) {
	a, store := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("Innei"))
	settle(tt)
	must(t, tt.Click(lobeTitle))
	settle(tt)
	rp := a.readers[lobehub]
	tt.Scroll(700, 500, 0, 3000)
	tt.Advance(time.Second)
	y := rp.r.Scroll.Y
	if y < 1000 {
		t.Fatalf("scrolled to %v", y)
	}
	lib, err := store.Library()
	must(t, err)
	var saved float64
	for _, art := range lib.Articles {
		if art.ID == lobehub && art.ReadingPosition != nil {
			saved = art.ReadingPosition.ScrollTop
		}
	}
	if saved < 1000 || saved > float64(y)+1 {
		t.Errorf("the library keeps %v, the reader is at %v", saved, y)
	}
	if !inProgress(a.article(lobehub)) {
		t.Error("the list does not see the article being read")
	}
	must(t, tt.Click("Innei"))
	settle(tt)
	if !tt.HasText("读到 " + itoa(percentOf(a.article(lobehub))) + "%") {
		t.Errorf("the row shows no progress: %q", tt.Texts())
	}
	must(t, tt.Click(lobeTitle))
	settle(tt)
	if got := a.readers[lobehub].r.Scroll.Y; got < y-2 || got > y+2 {
		t.Errorf("back in the article at %v, left at %v", got, y)
	}
	// A new session reads the place from the library.
	b, err := New(Options{DataDir: a.opt.DataDir, Store: store, Reading: a.opt.Reading, Sync: true, Thumbs: a.opt.Thumbs})
	must(t, err)
	t2 := ui.NewTester(b.View, 1280, 850)
	settle(t2)
	must(t, t2.Click("Innei"))
	settle(t2)
	must(t, t2.Click(lobeTitle))
	settle(t2)
	settle(t2)
	if got := b.readers[lobehub].r.Scroll.Y; got < float32(saved)-40 || got > float32(saved)+40 {
		t.Errorf("a new session opens at %v, the library keeps %v", got, saved)
	}
}

func itoa(n int) string { return string(rune('0'+n/10%10)) + string(rune('0'+n%10)) }

// What the keyboard does shows at once: J moves the list's cursor, Enter
// opens without a crossing, Esc goes back, ⌘2 changes tab, ? the sheet.
func TestKeyboard(t *testing.T) {
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("Innei"))
	settle(tt)
	tt.Key(0, ui.KeyJ)
	tt.Key(0, ui.KeyJ)
	au := a.writer("innei")
	if l := a.list("innei"); l.cursor != au.arts[1].ID {
		t.Errorf("after J J the cursor is on %q, want the second row %q", l.cursor, au.arts[1].ID)
	}
	if !tt.Focused(titleOf(au.arts[1].Title)) {
		t.Error("the cursor's row does not have the focus")
	}
	tt.Key(0, ui.KeyEnter)
	if a.page != (page{"reader", au.arts[1].ID}) || a.left != nil || len(a.flights) != 0 || a.lay != nil {
		t.Errorf("Enter: page %v, left %v, flights %d, ground %v: it should simply be there", a.page, a.left, len(a.flights), a.lay != nil)
	}
	tt.Key(0, ui.KeyEscape)
	if a.page != (page{"author", "innei"}) || a.left != nil || a.lay != nil {
		t.Errorf("Esc: page %v, left %v, ground %v", a.page, a.left, a.lay != nil)
	}
	tt.Key(ui.Cmd, ui.Key2)
	if a.page.kind != "favorites" {
		t.Errorf("⌘2: page %v", a.page)
	}
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if !a.layers.sheet || !tt.HasText("界面语言") {
		t.Error("⌘, did not open the settings panel")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.layers.sheet {
		t.Error("Esc did not close the settings panel")
	}
}

// Under less motion nothing travels or flies: the title stays, the ground
// lights and melts where it is, the pages only fade.
func TestReducedMotion(t *testing.T) {
	a, _ := newTestApp(t)
	tt := ui.NewTesterWith(a.View, 1280, 850, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	settle(tt)
	must(t, tt.Click("Innei"))
	tt.Advance(50 * time.Millisecond)
	if len(a.flights) != 0 {
		t.Errorf("the name flies under less motion: %d flights", len(a.flights))
	}
	settle(tt)
	must(t, tt.Click(lobeTitle))
	tt.Advance(50 * time.Millisecond)
	if len(a.flights) != 0 || a.lay == nil || !a.lay.Still {
		t.Errorf("opening under less motion: %d flights, ground %+v", len(a.flights), a.lay)
	}
	settle(tt)
	if !tt.HasText("起点是一轮性能优化") {
		t.Error("the article did not open")
	}
}

// A picture the read did not keep shows its pictogram and 重试, which reads
// the article again.
func TestPictureRetry(t *testing.T) {
	a, store := newTestApp(t)
	// The library copy keeps every picture its articles show; one is put
	// back as the read left it, not saved.
	const id = "https://antfu.me/posts/categorize-deps"
	kept, err := store.Content(id)
	must(t, err)
	kept.Document = nil
	// Kept with the current picture rules, so opening it reads nothing.
	current := 3
	kept.ImageCacheVersion = &current
	kept.HTML = strings.Replace(kept.HTML, "<p><span>图片暂未载入：Screenshot of the extension PNPM Catalog Lens</span></p>",
		`<p><img src="https://antfu.me/images/pnpm-catalog-lens.png" alt="Screenshot" width="800" height="400"></p>`, 1)
	must(t, store.SaveContent(*kept))
	var asked atomic.Int32
	a.opt.Reading = reading.New(store, func() fetch.Fetch {
		return func(ctx context.Context, u string, scope func(string) bool, n int) (*fetch.Response, error) {
			asked.Add(1)
			return offline()(ctx, u, scope, n)
		}
	}, reading.Worker)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("Anthony Fu"))
	settle(tt)
	// The article's entry is past the window's foot, under the head: bring it up.
	tt.Scroll(700, 500, 0, 400)
	settle(tt)
	must(t, tt.Click("Categorize Your Dependencies"))
	settle(tt)
	if _, ok := tt.Find("重试"); !ok {
		t.Fatalf("no 重试 under the missing picture: %q", tt.Texts())
	}
	if n := asked.Load(); n != 0 {
		t.Fatalf("opening a kept article read the network %d times", n)
	}
	r, _ := tt.Find("重试")
	for r.Y > 800 || r.Y < 60 {
		tt.Scroll(700, 400, 0, r.Y-400)
		tt.Advance(time.Second)
		r, _ = tt.Find("重试")
	}
	must(t, tt.Click("重试"))
	settle(tt)
	if asked.Load() == 0 {
		t.Error("重试 did not read the article again")
	}
	if !tt.HasText("没能更新，仍显示已保存的版本。") {
		t.Error("a read that failed does not say so")
	}
}

// Selecting words shows the bar over them; 摘录 keeps them in the library and
// marks them; selected again, the bar takes them back.
func TestClip(t *testing.T) {
	a, store := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("Innei"))
	settle(tt)
	must(t, tt.Click(lobeTitle))
	settle(tt)
	before := len(a.lib.Clips)
	// The first paragraph's first line, from its start to its middle.
	tt.Press(440, 290)
	tt.Move(700, 290)
	tt.Release(700, 290)
	tt.Advance(400 * time.Millisecond)
	acc, ok := tt.AccessByRole(ui.RoleToolbar, "选中文字的工具条")
	if !ok {
		t.Fatalf("no clip bar over the selection: %q", tt.Texts())
	}
	bar := acc.Bounds
	if bar.Y+bar.H > 290 || bar.Y < 200 {
		t.Errorf("the bar is at %v, not over the selection at 290", bar)
	}
	tt.ClickAt(bar.X+bar.W/2, bar.Y+bar.H/2)
	settle(tt)
	lib, err := store.Library()
	must(t, err)
	if len(lib.Clips) != before+1 || len(a.lib.Clips) != before+1 {
		t.Fatalf("clips %d → %d (app %d)", before, len(lib.Clips), len(a.lib.Clips))
	}
	if _, ok := tt.AccessByRole(ui.RoleToolbar, "选中文字的工具条"); ok {
		t.Error("the bar stays once the clip is kept")
	}
	// Selecting inside the clip offers to take it back.
	tt.Press(460, 290)
	tt.Move(600, 290)
	tt.Release(600, 290)
	tt.Advance(400 * time.Millisecond)
	acc, ok = tt.AccessByRole(ui.RoleToolbar, "摘录工具条")
	if !ok {
		t.Fatalf("no 取消摘录 inside the clip: %q", tt.Texts())
	}
	tt.ClickAt(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	settle(tt)
	if len(a.lib.Clips) != before || !tt.HasText("已删除摘录") {
		t.Errorf("taking the clip back: %d clips, notice %v", len(a.lib.Clips), tt.HasText("已删除摘录"))
	}
}

// Resting the pointer touches no site, nothing being read before the click: a row is read ahead only
// when its article is already in the library, and a page laid out unseen asks for no pictures.
func TestRestingFetchesNothing(t *testing.T) {
	a, store := newTestApp(t)
	var fetched, pictures atomic.Int32
	a.opt.Reading = reading.New(store, func() fetch.Fetch {
		return func(context.Context, string, func(string) bool, int) (*fetch.Response, error) {
			fetched.Add(1)
			return nil, errOffline
		}
	}, reading.Worker)
	a.opt.Thumbs = func(string, int) (*ui.Bitmap, error) {
		pictures.Add(1)
		return nil, errOffline
	}
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	rest := func(text string) {
		r, ok := tt.Find(text)
		if !ok || r.W == 0 || r.Y < 60 || r.Y > 800 {
			t.Fatalf("%q is not on screen (%+v)", text, r)
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Advance(400 * time.Millisecond)
		tt.Frame()
	}
	// The first row on screen of an author's page whose body is, or is not, in the library.
	onScreen := func(source string, cached bool) (id, title string) {
		for _, art := range a.lib.Articles {
			r, ok := tt.Find(art.Title)
			if art.SourceID == source && ok && r.W > 0 && r.Y >= 60 && r.Y <= 800 && (art.BodyStatus != "not_extracted") == cached {
				return art.ID, art.Title
			}
		}
		t.Fatalf("no row of %s on screen (cached %v)", source, cached)
		return "", ""
	}
	// Smashing Magazine has nothing in the library; Anthony Fu's newest posts are.
	rest("Smashing Magazine")
	if n := pictures.Load(); n != 0 {
		t.Errorf("resting on an author in the sidebar asked for %d pictures", n)
	}
	must(t, tt.Click("Smashing Magazine"))
	settle(tt)
	fresh, title := onScreen("feed:3fbb92094059402d", false)
	rest(title)
	if a.hover != fresh {
		t.Fatalf("the pointer did not rest on %q (hover %q)", title, a.hover)
	}
	if a.readers[fresh] != nil || fetched.Load() != 0 {
		t.Errorf("resting on a row whose article is not in the library read it (fetched %d pages)", fetched.Load())
	}
	must(t, tt.Click("Anthony Fu"))
	settle(tt)
	cached, title := onScreen("antfu", true)
	rest(title)
	if a.readers[cached] == nil {
		t.Error("resting on a row whose article is in the library did not read it ahead")
	}
	if n := fetched.Load(); n != 0 {
		t.Errorf("resting on rows fetched %d pages", n)
	}
}

// An article ends on the host of its page, a button that opens it in the browser.
func TestArticleEndsOnItsHost(t *testing.T) {
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("Anthony Fu"))
	settle(tt)
	tt.Scroll(700, 500, 0, 400)
	settle(tt)
	must(t, tt.Click("Categorize Your Dependencies"))
	settle(tt)
	for range 30 {
		if _, ok := tt.AccessByRole(ui.RoleButton, "antfu.me"); ok {
			return
		}
		tt.Scroll(700, 400, 0, 3000)
		tt.Advance(500 * time.Millisecond)
	}
	t.Fatalf("no antfu.me at the article's end: %q", tt.Texts())
}
