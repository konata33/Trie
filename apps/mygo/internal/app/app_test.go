package app

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/reading"
)

// The library the tests open: a copy of the real one's copy, in a folder of
// their own (.context/mygo/library, which the data-layer checks use too).
const libraryCopy = "../../../../.context/mygo/library/library.sqlite"

var errOffline = errors.New("offline")

// offline is a fetch that never reaches the network: the tests read what
// the library keeps.
func offline() fetch.Fetch {
	return func(context.Context, string, func(string) bool, int) (*fetch.Response, error) { return nil, errOffline }
}

// newTestApp opens a fresh copy of the library and the app on it, running
// its jobs at once.
func newTestApp(t testing.TB) (*App, *library.Store) {
	t.Helper()
	src, err := os.Open(libraryCopy)
	if err != nil {
		t.Skip("no library copy: ", err)
	}
	defer src.Close()
	dir := t.TempDir()
	dst, err := os.Create(filepath.Join(dir, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	dst.Close()
	store, err := library.Open(filepath.Join(dir, "library.sqlite"), library.Snapshot{Version: 1, AsOf: "2026-11-09"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	a, err := New(Options{DataDir: dir, Store: store, Reading: reading.New(store, offline, reading.Worker), Sync: true,
		Thumbs: func(string, int) (*ui.Bitmap, error) { return nil, errOffline }})
	if err != nil {
		t.Fatal(err)
	}
	return a, store
}

// settle runs the clock on until nothing moves.
func settle(tt *ui.Tester) {
	tt.Frame()
	tt.Advance(2 * time.Second)
}

func savePNG(t testing.TB, path string, m image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, m); err != nil {
		t.Fatal(err)
	}
}

// TestShots writes the window's pages to SHOTS: SHOTS=dir go test -run Shots.
func TestShots(t *testing.T) {
	dir := os.Getenv("SHOTS")
	if dir == "" {
		t.Skip("SHOTS not set")
	}
	os.MkdirAll(dir, 0o755)
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	tt.SetScale(2)
	settle(tt)
	savePNG(t, filepath.Join(dir, "side.png"), tt.Image())
	if err := tt.Click("Innei"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	savePNG(t, filepath.Join(dir, "author.png"), tt.Image())
	if err := tt.Click("十个月，给 LobeHub 换掉 Ant Design：向 Base UI 与 StyleX 的渐进式迁移"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	savePNG(t, filepath.Join(dir, "reader.png"), tt.Image())
}

// TestMeasure prints where the pages put their texts, to set beside the
// recorded reference (.context/mygo/app-v1/electron/shots/geometry.json):
// MEASURE=1 go test -run Measure -v.
func TestMeasure(t *testing.T) {
	if os.Getenv("MEASURE") == "" {
		t.Skip("MEASURE not set")
	}
	pinNow(t)
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	show := func(names ...string) {
		for _, n := range names {
			r, ok := tt.Find(n)
			t.Logf("%-40q %v x%.1f y%.1f w%.1f h%.1f", n, ok, r.X, r.Y, r.W, r.H)
		}
	}
	show("首页", "收藏", "Fonts In Use", "设置")
	tt.Click("Innei")
	settle(tt)
	show("Innei", "Migrating from React Native to UIKit in 87 Hours: Hollow Out the Data Layer, Rebuild It in UIKit", "11月4日")
	tt.Click("十个月，给 LobeHub 换掉 Ant Design：向 Base UI 与 StyleX 的渐进式迁移")
	settle(tt)
	show("十个月，给 LobeHub 换掉 Ant Design：向 Base UI 与 StyleX 的渐进式迁移", "11月4日", "约 17 分钟", "起点是一轮性能优化", "让新旧组件在同一个包里并存")
}

// TestAuthorPage: an author's page says nothing its entries do not show (no
// counts, reading times or picture counts), dates its entries with month and
// day, and its ··· opens its own menu, not the sidebar row's as well.
func TestAuthorPage(t *testing.T) {
	pinNow(t)
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("Innei"))
	settle(tt)
	noise := regexp.MustCompile(`^收录|\d+ 篇$|约 \d+ 分钟|\d+ 张图|\d+ 段代码|前更新$`)
	for _, text := range tt.Texts() {
		if noise.MatchString(text) {
			t.Errorf("the page says %q", text)
		}
	}
	if !tt.HasText("11月4日") {
		t.Errorf("no entry dated 11月4日: %q", tt.Texts())
	}
	menus := func() (n int) {
		for _, text := range tt.Texts() {
			if text == "重命名" {
				n++
			}
		}
		return n
	}
	// One menu open, the sidebar row's, to count against.
	side, ok := tt.AccessByRole(ui.RoleMenuButton, "Innei 的订阅操作")
	if !ok {
		t.Fatal("no ··· on the sidebar row")
	}
	tt.ClickAt(side.Bounds.X+side.Bounds.W/2, side.Bounds.Y+side.Bounds.H/2)
	settle(tt)
	one := menus()
	if a.layers.menu != "innei" || one == 0 {
		t.Fatalf("the sidebar's ··· opened %q", a.layers.menu)
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	name, ok := tt.AccessByRole(ui.RoleHeading, "Innei")
	if !ok {
		t.Fatal("no name at the head of the page")
	}
	// The head's ··· sits at the column's end, 24 across, level with the name's first line; the name starts 20
	// right of the portrait, which starts at the column's left.
	col := min(a.column(), a.area.W-80)
	tt.ClickAt(name.Bounds.X-portrait-20+col-12, name.Bounds.Y+20)
	settle(tt)
	if a.layers.menu != headMenu+"innei" {
		t.Fatalf("the head's ··· opened %q", a.layers.menu)
	}
	if n := menus(); n != one {
		t.Errorf("the head's ··· showed %d 重命名, one menu shows %d", n, one)
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	// The rail: a press on a tick further down goes to its entry.
	l := a.list("innei")
	// It waits 8 under the head: the banner, half the window high, and the portrait's foot under it.
	x, y := a.area.X+a.area.W-railRight-4, sceneHeight(sceneHead, 850)+profileFoot+8+24
	tt.Press(x, y)
	tt.Frame()
	tt.Release(x, y)
	settle(tt)
	if l.scroll.Y <= 0 {
		t.Errorf("a press on the rail did not go down the page: scrolled to %v", l.scroll.Y)
	}
}

// TestPairs writes the reader at the scroll positions the previous
// implementation's captures were taken at (.context/mygo/app-v1/electron/pairs.mjs): PAIRS=dir go test -run Pairs.
func TestPairs(t *testing.T) {
	dir := os.Getenv("PAIRS")
	if dir == "" {
		t.Skip("PAIRS not set")
	}
	cases := []struct {
		name, author, id string
		at               []float32
	}{
		{"lobehub", "innei", "https://innei.in/posts/tech/lobehub-antd-to-base-ui-stylex-migration", []float32{0, 4000, 6700}},
		{"async", "antfu", "https://antfu.me/posts/async-sync-in-between", []float32{0, 1500, 3000}},
		{"rain", "innei", "https://innei.in/notes/220", []float32{0, 2200, 6000}},
	}
	for _, k := range cases {
		a, _ := newTestApp(t)
		tt := ui.NewTester(a.View, 1280, 850)
		tt.SetScale(2)
		settle(tt)
		a.Press("author:" + k.author)
		settle(tt)
		a.Press("open:" + k.id)
		settle(tt)
		for _, y := range k.at {
			a.readers[k.id].r.Scroll.Y = y
			tt.Frame()
			settle(tt)
			savePNG(t, filepath.Join(dir, fmt.Sprintf("%s-%d-mygo.png", k.name, int(y))), tt.Image())
		}
	}
}
