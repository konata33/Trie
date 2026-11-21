package app

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/reading"
	"trie/internal/theme"
)

// packStep is what the stand-in packer does next: hear progress, or end.
type packStep struct {
	done  float64
	first image.Image
	end   bool
	err   error
}

// fakePacker stands in for TakePack: it says which page it started on and
// does what the test sends it, step by step; a pack it ends well is a folder
// with a first screen.
type fakePacker struct {
	started chan string
	steps   chan packStep
}

func newFakePacker() *fakePacker {
	return &fakePacker{started: make(chan string, 8), steps: make(chan packStep)}
}

func (f *fakePacker) take(ctx context.Context, link, dir string, progress explore.PackProgress) (*explore.Pack, error) {
	f.started <- link
	for {
		select {
		case s := <-f.steps:
			if !s.end {
				progress(s.done, s.first)
				continue
			}
			if s.err != nil {
				return nil, s.err
			}
			if err := os.MkdirAll(filepath.Join(dir, "screens"), 0o755); err != nil {
				return nil, err
			}
			out, err := os.Create(filepath.Join(dir, "screens", "00.jpg"))
			if err != nil {
				return nil, err
			}
			jpeg.Encode(out, stripes(160, 100), nil)
			out.Close()
			// The rest of what the pack says it holds: three screens, five pictures, two fonts.
			for _, name := range strings.Fields("screens/01.jpg screens/02.jpg files/images/0 files/images/1 files/images/2 files/images/3 files/images/4 files/fonts/0 files/fonts/1") {
				file := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
					return nil, err
				}
				if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
					return nil, err
				}
			}
			progress(1, nil)
			return &explore.Pack{URL: link, Screens: make([]explore.PackScreen, 3), Images: 5, Fonts: 2}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// stripes is a picture of coloured bands, for the dots to take colours from.
func stripes(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bands := []color.RGBA{{220, 90, 60, 255}, {40, 120, 200, 255}, {240, 210, 120, 255}, {30, 30, 30, 255}}
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, bands[(x*len(bands))/w])
		}
	}
	return img
}

// captureTester is an app on an empty library in dir, its jobs run off the
// window's thread and their results waiting in posts, the packer f.
func captureTester(t *testing.T, dir string, f *fakePacker) (*App, *ui.Tester, chan func()) {
	t.Helper()
	store, err := library.Open(filepath.Join(t.TempDir(), "library.sqlite"), library.Snapshot{Version: 1, AsOf: "2026-11-09"})
	must(t, err)
	t.Cleanup(func() { store.Close() })
	posts := make(chan func(), 64)
	a, err := New(Options{DataDir: dir, Store: store, Reading: reading.New(store, offline, reading.Worker),
		Post: func(fn func()) { posts <- fn }, Thumbs: func(string, int) (*ui.Bitmap, error) { return nil, errOffline }})
	must(t, err)
	a.capture.take = f.take
	a.capture.reveal = func(string) {}
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	return a, tt, posts
}

// await runs the jobs' results until ok, building a frame after each.
func await(t *testing.T, tt *ui.Tester, posts chan func(), ok func() bool) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for !ok() {
		select {
		case fn := <-posts:
			fn()
			tt.Frame()
		case <-deadline:
			t.Fatal("waited too long")
		}
	}
	tt.Advance(100 * time.Millisecond)
}

func started(t *testing.T, f *fakePacker) string {
	t.Helper()
	select {
	case link := <-f.started:
		return link
	case <-time.After(30 * time.Second):
		t.Fatal("nothing started")
	}
	return ""
}

func clickRole(t *testing.T, tt *ui.Tester, role ui.Role, name string) {
	t.Helper()
	acc, ok := tt.AccessByRole(role, name)
	if !ok {
		t.Fatalf("no %q", name)
	}
	tt.ClickAt(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	tt.Frame()
}

func clickAccess(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	acc, ok := tt.Access(name)
	if !ok {
		t.Fatalf("no %q", name)
	}
	tt.ClickAt(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	tt.Frame()
}

// One page is taken at a time, the others waiting their turn; progress moves
// its thumbnail; taken, it is kept in packs.json, its button lit, and a new
// session finds it again.
func TestCaptureQueueAndProgress(t *testing.T) {
	dir := t.TempDir()
	f := newFakePacker()
	a, tt, posts := captureTester(t, dir, f)
	one := a.takePage("p1", "https://one.example/", "One", nil, ui.Rect{}, tt.Now())
	two := a.takePage("p2", "https://two.example/", "Two", nil, ui.Rect{}, tt.Now())
	settle(tt)
	if got := started(t, f); got != "https://one.example/" {
		t.Fatalf("started %q first", got)
	}
	if one.state != packTaking || two.state != packWaiting {
		t.Fatalf("states %v %v", one.state, two.state)
	}
	if _, ok := tt.AccessByRole(ui.RoleGroup, "拓本"); !ok {
		t.Fatal("no island")
	}
	for _, name := range []string{"One，正在拓", "Two，排队中"} {
		if _, ok := tt.Access(name); !ok {
			t.Errorf("no thumbnail %q", name)
		}
	}
	f.steps <- packStep{done: 0.4, first: stripes(320, 200)}
	await(t, tt, posts, func() bool { return one.progress == 0.4 })
	if one.first == nil || one.shot == nil {
		t.Error("the first screen did not come")
	}
	// At 0.4 the button's top row has grown full, the third not yet.
	if dev, lit := captureDots(0.4, false, ui.Color{}), captureDots(-1, true, ui.Color{}); dev[0].r != lit[0].r || dev[8].r >= lit[8].r {
		t.Errorf("developing at 0.4: top %v, third %v; lit %v", dev[0].r, dev[8].r, lit[8].r)
	}
	f.steps <- packStep{end: true}
	await(t, tt, posts, func() bool { return one.state == packTaken })
	if got := started(t, f); got != "https://two.example/" {
		t.Fatalf("then started %q", got)
	}
	if one.Screens != 3 || one.Images != 5 || one.Fonts != 2 {
		t.Errorf("counts %d %d %d", one.Screens, one.Images, one.Fonts)
	}
	if _, err := os.Stat(filepath.Join(dir, "packs.json")); err != nil {
		t.Fatal("packs.json not written: ", err)
	}
	if a.packOf("p1") != one {
		t.Error("the page's button does not find its pack")
	}
	settle(tt)
	if _, ok := tt.Access("One，已拓下"); !ok {
		t.Error("the taken pack is not ready in the island")
	}

	// A new session: the taken pack is in 收藏, not in the island; the one being taken is gone.
	b, tt2, posts2 := captureTester(t, dir, newFakePacker())
	if len(b.capture.items) != 1 || b.capture.items[0].ID != one.ID || b.capture.items[0].state != packTaken {
		t.Fatalf("reloaded %v", b.capture.items)
	}
	await(t, tt2, posts2, func() bool { return b.capture.items[0].shot != nil })
	settle(tt2)
	if _, ok := tt2.AccessByRole(ui.RoleGroup, "拓本"); ok || b.capture.up {
		t.Error("the island came up for a pack taken in an earlier session")
	}
	if got := b.shelved(""); len(got) != 1 || got[0].ID != one.ID {
		t.Errorf("收藏 holds %v, want the reloaded pack", got)
	}
	a.StopPacks()
}

// Pressed, a thumbnail grows into its card; 复制 copies the folder's place and
// uses the pack, and once its card is gone the island, with nothing left,
// goes back into its point, the pack into 收藏. There its place opens the card
// again, which goes back into it and says 已复制 there.
func TestCaptureCopyUses(t *testing.T) {
	dir := t.TempDir()
	f := newFakePacker()
	a, tt, posts := captureTester(t, dir, f)
	it := a.takePage("p1", "https://one.example/", "One", nil, ui.Rect{}, tt.Now())
	started(t, f)
	f.steps <- packStep{end: true}
	await(t, tt, posts, func() bool { return it.state == packTaken })
	settle(tt)
	clickAccess(t, tt, "One，已拓下")
	settle(tt)
	if a.capture.card != it.ID {
		t.Fatal("the card did not open")
	}
	// The card shows what it holds rather than writes it; screen readers hear it.
	if _, ok := tt.Access("3 屏 · 5 张图 · 2 种字体"); !ok {
		t.Error("the card's page does not tell what it holds")
	}
	if err := tt.Click("复制"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// A brief: the task first, then the page, its address and the folder.
	brief := tt.Clipboard()
	if !strings.HasPrefix(brief, "任务：") || !strings.Contains(brief, "https://one.example/") ||
		!strings.Contains(brief, "`"+filepath.Join(dir, "packs", it.ID)+"`") {
		t.Errorf("copied %q, want the brief with the page and its folder", brief)
	}
	if !it.Used {
		t.Error("copying did not use the pack")
	}
	// Once the light has passed, the card goes back into its thumbnail, which
	// stays while the island says 已复制.
	tt.Advance(motion.CaptureSweep)
	tt.Frame()
	tt.Advance(motion.DialogOut + 100*time.Millisecond)
	tt.Frame()
	if a.capture.card != "" {
		t.Error("copied, the card stayed open")
	}
	if _, ok := a.capture.slots[it.ID]; !ok || !a.capture.up {
		t.Error("the island left before it said 已复制")
	}
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("已复制") {
		t.Error("the island does not say 已复制")
	}
	settle(tt)
	settle(tt)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleGroup, "拓本"); ok || a.capture.up {
		t.Error("the island stayed with nothing in it")
	}
	if !it.filed || len(a.shelved("")) != 1 {
		t.Fatal("the copied pack did not go into 收藏")
	}
	clickAccess(t, tt, "收藏")
	settle(tt)
	clickAccess(t, tt, "拓本，1 页")
	settle(tt)
	clickAccess(t, tt, "One")
	settle(tt)
	if a.capture.card != it.ID || a.capture.home != homeShelf {
		t.Fatalf("card %q from %q, want %q from 收藏", a.capture.card, a.capture.home, it.ID)
	}
	if err := tt.Click("复制"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(motion.CaptureSweep)
	tt.Frame()
	tt.Advance(motion.DialogOut + 100*time.Millisecond)
	tt.Frame()
	if a.capture.card != "" || a.capture.up {
		t.Error("copied from 收藏, the card stayed or the island came up")
	}
	if !tt.HasText("已复制") {
		t.Error("its place in 收藏 does not say 已复制")
	}
	tt.Advance(motion.Copied)
	settle(tt)
	if tt.HasText("已复制") {
		t.Error("已复制 stayed")
	}
	// Searching 收藏 finds it by its address.
	a.favs().query = "one.example"
	settle(tt)
	if _, ok := tt.Access("One"); !ok {
		t.Error("searching by its address does not find the pack")
	}
}

// Taken, a pack stays in the island while it is pointed at, then flies into
// 收藏's tab and the island goes back into its point; 收进收藏 sends the island
// away, and what is taken while it is away goes straight into 收藏.
func TestCaptureLingerAndHide(t *testing.T) {
	dir := t.TempDir()
	f := newFakePacker()
	a, tt, posts := captureTester(t, dir, f)
	one := a.takePage("p1", "https://one.example/", "One", nil, ui.Rect{}, tt.Now())
	started(t, f)
	f.steps <- packStep{end: true}
	await(t, tt, posts, func() bool { return one.state == packTaken })
	settle(tt)
	// Pointed at, it stays past CaptureLinger.
	acc, ok := tt.Access("One，已拓下")
	if !ok {
		t.Fatal("not in the island")
	}
	tt.Move(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	for range 4 {
		settle(tt)
	}
	if one.filed {
		t.Fatal("it left the island while pointed at")
	}
	tt.Move(600, 600)
	tt.Frame()
	for i := 0; !one.filed && i < 80; i++ {
		tt.Advance(100 * time.Millisecond)
	}
	if !one.filed || len(a.capture.files) != 1 {
		t.Fatalf("filed %v, %d flying, want it on its way into 收藏", one.filed, len(a.capture.files))
	}
	settle(tt)
	settle(tt)
	tt.Frame()
	if len(a.capture.files) != 0 || a.capture.up {
		t.Error("it did not land, or the island stayed")
	}

	// 收进收藏 while one is being taken: the island itself sets off for 收藏's tab
	// and goes into it, which then tells how far the page has come, under its
	// icon, and nothing lies over the sidebar; it goes on there. Pressed, the tab
	// opens 收藏's captures.
	two := a.takePage("p2", "https://two.example/", "Two", nil, ui.Rect{}, tt.Now())
	started(t, f)
	settle(tt)
	acc, ok = tt.Access("Two，正在拓")
	if !ok {
		t.Fatal("the second is not in the island")
	}
	tt.Move(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	settle(tt)
	clickAccess(t, tt, "收进收藏")
	tt.Move(600, 600)
	tt.Frame()
	hm := a.capture.homing
	if hm == nil || a.capture.up || !a.capture.away {
		t.Fatal("the island did not set off for 收藏")
	}
	tt.Advance(150 * time.Millisecond)
	if hm.landed {
		t.Fatal("the tab took over at once: want the island on its way")
	}
	for range 12 {
		tt.Advance(100 * time.Millisecond)
	}
	if a.capture.homing != nil || a.capture.post != nil {
		t.Fatalf("on its way %v, note %v: want it home in the tab, no note", a.capture.homing != nil, a.capture.post != nil)
	}
	if a.capture.tabPct != two.pct || a.capture.tabFill == 0 || !tt.HasText(percentText(two.pct)) {
		t.Fatalf("the tab does not tell how far it has come (%d%%, its %d%%)", two.pct, a.capture.tabPct)
	}
	clickAccess(t, tt, "收藏")
	tt.Move(600, 600) // off the tab, which holds what it says while pointed at
	settle(tt)
	settle(tt)
	if a.page != (page{kind: "favorites"}) || a.favs().kind != captures {
		t.Fatalf("the tab went to %+v, block %q: want 收藏's captures", a.page, a.favs().kind)
	}
	if _, ok := tt.Access("Two"); !ok || !tt.HasText(percentText(two.pct)) {
		t.Errorf("the page being taken is not in 收藏's captures with how far it has come (%s)", percentText(two.pct))
	}
	if a.capture.tabFill != 0 {
		t.Error("the tab still tells it over 收藏's captures, which tell it themselves")
	}
	// Taken out of sight: into 收藏, whose tab closes its orbit and says so a while.
	clickAccess(t, tt, "首页")
	tt.Move(600, 600)
	settle(tt)
	if a.capture.tabFill == 0 {
		t.Fatal("away from 收藏's captures, the tab does not tell it again")
	}
	f.steps <- packStep{end: true}
	await(t, tt, posts, func() bool { return two.state == packTaken })
	settle(tt)
	if !two.filed || a.capture.up {
		t.Errorf("taken while away: filed %v, island up %v", two.filed, a.capture.up)
	}
	if a.capture.told != two || a.capture.tabFill != 1 || !tt.HasText("已拓下") || a.capture.post != nil {
		t.Error("the tab does not say it was taken out of sight, or a note came")
	}
	if a.capture.away {
		t.Error("away stayed with nothing being taken")
	}
	for range 40 {
		tt.Advance(100 * time.Millisecond)
	}
	if a.capture.told != nil || a.capture.tabFill != 0 || tt.HasText("已拓下") {
		t.Errorf("the tab did not go back to rest: told %v, orbit %v, says %v", a.capture.told != nil, a.capture.tabFill, tt.HasText("已拓下"))
	}
	// The next capture brings the island back.
	a.takePage("p3", "https://three.example/", "Three", nil, ui.Rect{}, tt.Now())
	started(t, f)
	settle(tt)
	if !a.capture.up {
		t.Error("the island did not come back for the next capture")
	}
	a.StopPacks()
}

// Past five, the island keeps the page being taken and the one just taken in
// sight, and stacks behind the earliest taken first, then the last waiting.
func TestIslandFront(t *testing.T) {
	pack := func(name string, st packState) *packItem { return &packItem{Name: name, state: st} }
	names := func(its []*packItem) string {
		var s []string
		for _, it := range its {
			s = append(s, it.Name)
		}
		return strings.Join(s, " ")
	}
	// As islandItems lays them out: taken and failed, waiting, being taken; newest first.
	taken := []*packItem{pack("t6", packTaken), pack("t5", packFailed), pack("t4", packTaken), pack("t3", packTaken),
		pack("t2", packTaken), pack("t1", packTaken), pack("k", packTaking)}
	if got := names(islandFront(taken)); got != "t6 t5 t4 t3 k" {
		t.Errorf("six taken and one being taken show %q, want the newest taken and the one being taken", got)
	}
	waiting := []*packItem{pack("t1", packTaken), pack("w6", packWaiting), pack("w5", packWaiting), pack("w4", packWaiting),
		pack("w3", packWaiting), pack("w2", packWaiting), pack("w1", packWaiting), pack("k", packTaking)}
	if got := names(islandFront(waiting)); got != "t1 w3 w2 w1 k" {
		t.Errorf("one taken, six waiting show %q, want the one just taken and the next to be taken", got)
	}
}

// The island's figures: from 0% at once, creeping on while the page opens and
// nothing is told, never back, in steps no closer than CapturePercent, 99% at
// most until it is taken; then 100%, which gives way to the lit mark.
func TestCaptureFigures(t *testing.T) {
	f := newFakePacker()
	a, tt, posts := captureTester(t, t.TempDir(), f)
	it := a.takePage("p1", "https://one.example/", "One", nil, ui.Rect{}, tt.Now())
	started(t, f)
	tt.Frame()
	if !tt.HasText("0%") {
		t.Fatal("no figures as it starts")
	}
	last, steps, lastAt := 0, 0, tt.Now()
	for range 60 { // 6 s of the page opening, nothing told
		tt.Advance(100 * time.Millisecond)
		switch {
		case a.capture.pct < last:
			t.Fatalf("went back from %d%% to %d%%", last, a.capture.pct)
		case a.capture.pct > last:
			if gap := tt.Now().Sub(lastAt); steps > 0 && gap < motion.CapturePercent {
				t.Fatalf("moved on after %v", gap)
			}
			last, lastAt, steps = a.capture.pct, tt.Now(), steps+1
		}
	}
	if last < 5 || last >= 10 {
		t.Fatalf("after 6 s of opening, %d%%: want it creeping toward a tenth", last)
	}
	go func() { f.steps <- packStep{done: 1} }()
	await(t, tt, posts, func() bool { return it.progress == 1 })
	settle(tt)
	if !tt.HasText("99%") || tt.HasText("100%") {
		t.Fatal("told it is all there but not yet taken: want 99%")
	}
	go func() { f.steps <- packStep{end: true} }()
	await(t, tt, posts, func() bool { return it.state == packTaken })
	tt.Frame()
	if !tt.HasText("100%") {
		t.Fatal("no 100% as it is taken")
	}
	for range 20 {
		tt.Advance(100 * time.Millisecond)
	}
	if tt.HasText("100%") {
		t.Error("100% stayed past the first screen rising")
	}
}

// A pack that fails while the island is away says so in the note under 收藏's
// tab, whose 重试 takes it again, the island coming back for it.
func TestCaptureFailedAway(t *testing.T) {
	f := newFakePacker()
	a, tt, posts := captureTester(t, t.TempDir(), f)
	it := a.takePage("p1", "https://one.example/", "One", nil, ui.Rect{}, tt.Now())
	started(t, f)
	settle(tt)
	a.capture.away = true
	go func() { f.steps <- packStep{end: true, err: errors.New("explore: the page did not load")} }()
	await(t, tt, posts, func() bool { return it.state == packFailed })
	settle(tt)
	if p := a.capture.post; p == nil || p.it != it || !tt.HasText("没拓成") {
		t.Fatal("no note that it failed out of sight")
	}
	was := a.page
	if err := tt.Click("重试"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if it.state != packTaking || a.capture.away || a.capture.post != nil {
		t.Fatalf("重试: state %v, away %v, note left %v", it.state, a.capture.away, a.capture.post != nil)
	}
	if a.page != was {
		t.Errorf("重试 also opened %+v: want only the page taken again", a.page)
	}
	// It fails again, and is deleted in 收藏 while the note says so: the note
	// goes into the tab, its 重试 gone with it.
	a.capture.away = true
	go func() { f.steps <- packStep{end: true, err: errors.New("explore: the page did not load")} }()
	await(t, tt, posts, func() bool { return it.state == packFailed })
	settle(tt)
	if a.capture.post == nil {
		t.Fatal("no note the second time")
	}
	a.dropPack(it)
	settle(tt)
	if a.capture.post != nil || tt.HasText("没拓成") {
		t.Error("the note stayed for a page deleted")
	}
	// Two failing at once: VoiceOver hears of both, the note shows the later.
	one, two := &packItem{Name: "One", state: packFailed}, &packItem{Name: "Two", state: packFailed}
	a.said = nil
	a.postFailed(one, tt.Now())
	a.postFailed(two, tt.Now())
	if len(a.said) != 2 || a.capture.post.it != two {
		t.Errorf("said %q, note of %q: want both said and the note of Two", a.said, a.capture.post.it.Name)
	}
	a.StopPacks()
}

// From the keys (one Esc in the island, though the thumbnail the keys are on
// shows its tip) or under less motion the island does not travel home: it is
// gone at once, and 收藏's tab tells how far the page has come straight away.
func TestCapturePutAwayAtOnce(t *testing.T) {
	for _, still := range []bool{false, true} {
		f := newFakePacker()
		a, tt, _ := captureTester(t, t.TempDir(), f)
		if still {
			tt = ui.NewTesterWith(a.View, 1280, 850, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
			settle(tt)
		}
		it := a.takePage("p1", "https://one.example/", "One", nil, ui.Rect{}, tt.Now())
		started(t, f)
		settle(tt)
		if still {
			acc, ok := tt.Access("One，正在拓")
			if !ok {
				t.Fatal("not in the island")
			}
			tt.Move(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
			settle(tt)
			clickAccess(t, tt, "收进收藏")
			tt.Move(600, 600)
		} else {
			for i := 0; !tt.Focused("One，正在拓") && i < 80; i++ {
				tt.Key(0, ui.KeyTab)
			}
			if !tt.Focused("One，正在拓") {
				t.Fatal("the keys did not reach the island")
			}
			settle(tt)
			if !tt.HasText("One，正在拓") {
				t.Fatal("the thumbnail the keys are on shows no tip")
			}
			tt.Key(0, ui.KeyEscape) // one: the tip does not take it
		}
		tt.Frame()
		if !a.capture.away || a.capture.homing != nil || a.capture.up {
			t.Fatalf("still %v: away %v, travelling %v, island up %v: want it home at once", still, a.capture.away, a.capture.homing != nil, a.capture.up)
		}
		settle(tt)
		if a.capture.tabFill == 0 || !tt.HasText(percentText(it.pct)) {
			t.Errorf("still %v: the tab does not tell how far it has come", still)
		}
		a.StopPacks()
	}
}

// Under less motion a pack let go of fades where it was rather than fly, and
// 收藏's tab still lights.
func TestCaptureStill(t *testing.T) {
	f := newFakePacker()
	a, _, posts := captureTester(t, t.TempDir(), f)
	tt := ui.NewTesterWith(a.View, 1280, 850, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	settle(tt)
	it := a.takePage("p1", "https://one.example/", "One", nil, ui.Rect{}, tt.Now())
	started(t, f)
	f.steps <- packStep{end: true}
	await(t, tt, posts, func() bool { return it.state == packTaken })
	settle(tt)
	for i := 0; !it.filed && i < 80; i++ {
		tt.Advance(100 * time.Millisecond)
	}
	if !it.filed || len(a.capture.files) != 1 || !a.capture.files[0].still {
		t.Fatalf("filed %v, %d going, want one fading", it.filed, len(a.capture.files))
	}
	tt.Advance(motion.PopIn + 50*time.Millisecond)
	tt.Frame()
	if len(a.capture.files) != 0 {
		t.Error("it did not fade")
	}
	if _, ok := a.lights[favLight]; !ok {
		t.Error("收藏's tab did not light")
	}
	settle(tt)
	if a.capture.up {
		t.Error("the island stayed")
	}
}

// A pack that fails lets its dots fall; its card says what happened and
// takes it again.
func TestCaptureFailure(t *testing.T) {
	dir := t.TempDir()
	f := newFakePacker()
	a, tt, posts := captureTester(t, dir, f)
	it := a.takePage("p1", "https://one.example/", "One", nil, ui.Rect{}, tt.Now())
	started(t, f)
	f.steps <- packStep{end: true, err: errors.New("explore: the page did not load")}
	await(t, tt, posts, func() bool { return it.state == packFailed })
	settle(tt)
	clickAccess(t, tt, "One，没拓成")
	settle(tt)
	if !tt.HasText("这一页没能打开。检查网络后再试。") {
		t.Errorf("the card does not say what happened: %q", tt.Texts())
	}
	if err := tt.Click("重试"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// It left no folder, so it keeps its name.
	if got := started(t, f); got != "https://one.example/" || it.state != packTaking || it.ID != "one" {
		t.Fatalf("retried %q, state %v, folder %q", got, it.state, it.ID)
	}
	if _, err := os.Stat(filepath.Join(dir, "packs.json")); err == nil {
		t.Error("a failed pack was kept")
	}
	a.StopPacks()
	// Failed ones leave after ten minutes.
	it.state, it.failedAt = packFailed, tt.Now()
	settle(tt)
	tt.Advance(11 * time.Minute)
	settle(tt)
	if len(a.capture.items) != 0 {
		t.Error("a failed pack stayed past ten minutes")
	}
}

// Quitting abandons the pack being taken and those waiting, leaving no
// folder; a half folder a crash left goes as the next session opens.
func TestCaptureStop(t *testing.T) {
	dir := t.TempDir()
	f := newFakePacker()
	a, tt, posts := captureTester(t, dir, f)
	one := a.takePage("p1", "https://one.example/", "One", nil, ui.Rect{}, tt.Now())
	a.takePage("p2", "https://two.example/", "Two", nil, ui.Rect{}, tt.Now())
	started(t, f)
	a.StopPacks()
	await(t, tt, posts, func() bool { return one.cancel == nil })
	if len(a.capture.items) != 0 {
		t.Errorf("left %d packs", len(a.capture.items))
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "packs")); len(entries) != 0 {
		t.Errorf("left %d folders", len(entries))
	}
	select {
	case link := <-f.started:
		t.Errorf("started %q after the quit", link)
	default:
	}
	must(t, os.MkdirAll(filepath.Join(dir, "packs", "x-20261118-120000.part"), 0o755))
	captureTester(t, dir, newFakePacker())
	if _, err := os.Stat(filepath.Join(dir, "packs", "x-20261118-120000.part")); err == nil {
		t.Error("a half folder stayed")
	}
}

// Without a library folder, as in some tests, packs go to the temporary
// folder and nothing is kept.
func TestCaptureNoDataDir(t *testing.T) {
	f := newFakePacker()
	a, tt, posts := captureTester(t, "", f)
	it := a.takePage("p1", "https://one.example/", "One", nil, ui.Rect{}, tt.Now())
	started(t, f)
	f.steps <- packStep{end: true}
	await(t, tt, posts, func() bool { return it.state == packTaken })
	t.Cleanup(func() { os.RemoveAll(filepath.Join(a.capture.dir, it.ID)) })
	if a.capture.keep {
		t.Error("kept a list with no library folder")
	}
}

// The reader's button takes the page and, lit once it is taken, opens the
// pack's card rather than take it again.
func TestCaptureButton(t *testing.T) {
	a, tt, _ := openLobehub(t)
	f := newFakePacker()
	a.capture.take = f.take
	posts := make(chan func(), 64)
	a.opt.Post, a.opt.Sync = func(fn func()) { posts <- fn }, false
	clickRole(t, tt, ui.RoleButton, "拓本")
	if got := started(t, f); got == "" {
		t.Fatal("nothing taken")
	}
	it := a.packOf(lobehub)
	if it == nil || it.Name == "" {
		t.Fatalf("pack %+v", it)
	}
	f.steps <- packStep{end: true}
	await(t, tt, posts, func() bool { return it.state == packTaken })
	settle(tt)
	clickRole(t, tt, ui.RoleButton, "拓本")
	settle(tt)
	if a.capture.card != it.ID || a.capture.home != homeButton {
		t.Error("the lit button did not open the card")
	}
	select {
	case link := <-f.started:
		t.Errorf("took %q again", link)
	default:
	}
}

// TestCaptureShots writes the capture at 2×, cropped to where it shows, into
// CAPTURE_SHOTS: the button idle, developing and lit; the island developing,
// taken, pointed at, failed and under less motion; the card; the frames of a
// pack being taken. CAPTURE_SHOTS=dir go test -run CaptureShots ./internal/app
func TestCaptureShots(t *testing.T) {
	out := os.Getenv("CAPTURE_SHOTS")
	if out == "" {
		t.Skip("CAPTURE_SHOTS not set")
	}
	must(t, os.MkdirAll(out, 0o755))
	load := func(name string) image.Image {
		data, err := os.ReadFile(filepath.Join("../../../../.context/handoff-pack", name))
		if err != nil {
			t.Skip("no screenshots: ", err)
		}
		return decodeAny(data)
	}
	tegra, codrops, obys := load("tegra-00.jpg"), load("codrops-03.jpg"), load("obys-05.jpg")
	add := func(a *App, name string, st packState, p float32, base, first image.Image) *packItem {
		it := &packItem{ID: name, Name: name, Page: name, URL: "https://" + name + ".example/", state: st, progress: p,
			base: gridOf(base), first: gridOf(first), shot: shotOf(first, 2*cardW), Screens: 14, Images: 38, Fonts: 3}
		a.capture.items = append([]*packItem{it}, a.capture.items...)
		return it
	}
	crop := func(tt *ui.Tester, r ui.Rect) image.Image {
		img := tt.Image()
		return img.SubImage(image.Rect(int(r.X*2), int(r.Y*2), int((r.X+r.W)*2), int((r.Y+r.H)*2)))
	}
	islandArea := func(a *App, w float32) ui.Rect {
		return ui.Rect{X: a.area.X + a.area.W/2 - w/2, Y: 0, W: w, H: 72}
	}
	must(t, os.MkdirAll(filepath.Join(out, "zoom"), 0o755))
	save := func(name string, m image.Image) {
		savePNG(t, filepath.Join(out, name+".png"), m)
		// Four times larger, pixel for pixel, to see the dots' edges.
		if b := m.Bounds(); b.Dx() <= 500 && !strings.Contains(name, "/") {
			big := image.NewRGBA(image.Rect(0, 0, b.Dx()*4, b.Dy()*4))
			for y := range big.Bounds().Dy() {
				for x := range big.Bounds().Dx() {
					big.Set(x, y, m.At(b.Min.X+x/4, b.Min.Y+y/4))
				}
			}
			savePNG(t, filepath.Join(out, "zoom", name+".png"), big)
		}
	}
	var posts chan func()
	shots := func(prefs ui.Preferences) (*App, *ui.Tester) {
		a, _, p := captureTester(t, t.TempDir(), newFakePacker())
		posts = p
		tt := ui.NewTesterWith(a.View, 1280, 850, ui.TesterOptions{Preferences: prefs})
		tt.SetScale(2)
		settle(tt)
		return a, tt
	}

	// The island, one pack developing: before its first screen, from the app's picture; after, from the page.
	for _, c := range []struct {
		name  string
		p     float32
		base  image.Image
		first image.Image
	}{{"island-p15", 0.15, tegra, nil}, {"island-p15-grey", 0.15, nil, nil}, {"island-p50", 0.5, tegra, tegra}, {"island-p85", 0.85, tegra, tegra}} {
		a, tt := shots(ui.Preferences{})
		add(a, "tegra", packTaking, c.p, c.base, c.first)
		settle(tt)
		save(c.name, crop(tt, islandArea(a, 200)))
		// Its card while it is being taken: the same picture, developing.
		clickAccess(t, tt, "tegra，正在拓")
		settle(tt)
		if d, ok := tt.Access("tegra"); ok {
			save("card-"+strings.TrimPrefix(c.name, "island-"), crop(tt, ui.Rect{X: d.Bounds.X - 40, Y: d.Bounds.Y - 40, W: d.Bounds.W + 80, H: d.Bounds.H + 80}))
		}
	}
	// Taken: the first screen up.
	a, tt := shots(ui.Preferences{})
	it := add(a, "tegra", packTaking, 0.9, tegra, tegra)
	// CAPTURE_FONT, a font file, stands in for the face the page's largest words are set in.
	if f := os.Getenv("CAPTURE_FONT"); f != "" {
		data, err := os.ReadFile(f)
		must(t, err)
		must(t, ui.RegisterFont(data, "trie-pack-shots"))
		it.Name, it.font, it.TitleWeight = "Tegra", "trie-pack-shots", 600
	}
	// CAPTURE_PACK, a pack's folder, gives the card its page to scroll through.
	if pk := os.Getenv("CAPTURE_PACK"); pk != "" {
		a.capture.dir, it.ID = filepath.Dir(pk), filepath.Base(pk)
	}
	settle(tt)
	it.state, it.progress = packTaken, 1
	settle(tt)
	tt.Advance(time.Second)
	save("island-taken", crop(tt, islandArea(a, 200)))
	// Its card.
	clickAccess(t, tt, it.Name+"，已拓下")
	settle(tt)
	// Its page read: the screens, then the specimen's pictures.
	await(t, tt, posts, func() bool { return it.pages != nil && it.pages.read })
	settle(tt)
	area := func() ui.Rect {
		d, _ := tt.Access(it.Name)
		return ui.Rect{X: d.Bounds.X - 40, Y: d.Bounds.Y - 40, W: d.Bounds.W + 80, H: d.Bounds.H + 80}
	}
	tt.Move(5, 400)
	settle(tt)
	save("card", crop(tt, area()))
	save("card-window", tt.Image())
	// Pointed at: the other tools come in, and the line that tells where the page is.
	cp, _ := tt.Access("复制")
	tt.Move(cp.Bounds.X-160, cp.Bounds.Y-80)
	settle(tt)
	save("card-hover", crop(tt, area()))
	tt.Move(cp.Bounds.X+cp.Bounds.W/2, cp.Bounds.Y+cp.Bounds.H/2)
	settle(tt)
	save("card-hover-copy", crop(tt, area()))
	// Scrolled down the page, and past its foot to the specimen.
	tt.Move(cp.Bounds.X-160, cp.Bounds.Y-80)
	for _, sc := range []struct {
		name string
		y    float32
	}{{"card-scrolled", 620}, {"card-specimen", 1e9}} {
		a.capture.scroll.Y = sc.y
		tt.Frame()
		tt.Advance(100 * time.Millisecond)
		save(sc.name+"-moving", crop(tt, area()))
		settle(tt)
		save(sc.name, crop(tt, area()))
	}
	a.capture.scroll.Y = 0
	settle(tt)
	// Copied: the light from the button, the card back into its thumbnail, the
	// island saying so, then going.
	cardAt := area()
	var went time.Duration
	tt.ClickAt(cp.Bounds.X+cp.Bounds.W/2, cp.Bounds.Y+cp.Bounds.H/2)
	for _, f := range []struct {
		name  string
		after time.Duration
		at    func() ui.Rect
	}{
		{"copy-1-light", 300 * time.Millisecond, func() ui.Rect { return cardAt }},
		{"copy-2-back", 720 * time.Millisecond, func() ui.Rect { return ui.Rect{W: 1280, H: 850} }},
		{"copy-3-said", 1200 * time.Millisecond, func() ui.Rect { return islandArea(a, 240) }},
		{"copy-4-going", 3000 * time.Millisecond, func() ui.Rect { return islandArea(a, 240) }},
		{"copy-5-gone", 3600 * time.Millisecond, func() ui.Rect { return islandArea(a, 240) }},
	} {
		// Frame by frame, as the window draws them, so each step starts when the one before ends.
		for ; went < f.after; went += 20 * time.Millisecond {
			tt.Advance(20 * time.Millisecond)
		}
		save(f.name, crop(tt, f.at()))
	}

	// The frames of a pack being taken: developing, then taken.
	a, tt = shots(ui.Preferences{})
	it = add(a, "tegra", packTaking, 0, tegra, nil)
	settle(tt)
	frame := 0
	snap := func() {
		save("frames/"+string(rune('a'+frame/10))+string(rune('0'+frame%10)), crop(tt, islandArea(a, 120)))
		frame++
	}
	must(t, os.MkdirAll(filepath.Join(out, "frames"), 0o755))
	for _, p := range []float32{0.1, 0.25, 0.4, 0.55, 0.7, 0.85, 1} {
		it.progress = p
		if p == 0.4 {
			it.first = gridOf(tegra)
			it.shot = shotOf(tegra, 2*cardW)
		}
		tt.Frame()
		for range 3 {
			tt.Advance(120 * time.Millisecond)
			snap()
		}
	}
	it.state = packTaken
	tt.Frame()
	for range 14 {
		tt.Advance(100 * time.Millisecond)
		snap()
	}

	// Three, the pointer on the middle one; and six, the earlier stacking behind the fifth.
	a, tt = shots(ui.Preferences{})
	add(a, "obys", packTaken, 1, obys, obys)
	add(a, "codrops", packTaking, 0.6, codrops, codrops)
	add(a, "tegra", packWaiting, 0, tegra, nil)
	settle(tt)
	tt.Advance(time.Second)
	save("island-three", crop(tt, islandArea(a, 320)))
	acc, _ := tt.Access("codrops，正在拓")
	tt.Move(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	settle(tt)
	save("island-three-hover", crop(tt, islandArea(a, 320)))
	tt.Move(600, 600)
	for _, n := range []string{"a1", "a2", "a3"} {
		add(a, n, packWaiting, 0, nil, nil)
	}
	settle(tt)
	save("island-six", crop(tt, islandArea(a, 480)))

	// A pack that failed, its dots fallen, and its card.
	a, tt = shots(ui.Preferences{})
	it = add(a, "tegra", packTaking, 0.45, tegra, tegra)
	settle(tt)
	it.state, it.failed = packFailed, packFailure(errors.New("explore: the page did not load"))
	tt.Advance(150 * time.Millisecond)
	save("island-failing", crop(tt, islandArea(a, 200)))
	settle(tt)
	save("island-failed", crop(tt, islandArea(a, 200)))
	clickAccess(t, tt, "tegra，没拓成")
	settle(tt)
	if d, ok := tt.Access("tegra"); ok {
		save("card-failed", crop(tt, ui.Rect{X: d.Bounds.X - 40, Y: d.Bounds.Y - 40, W: d.Bounds.W + 80, H: d.Bounds.H + 80}))
	}

	// An article with no picture: its proof, waiting and half developed, in the island and on its card.
	proof := proofPic(proofPage{title: "Categorize Your Dependencies", source: "Anthony Fu", date: "10月2日", mark: ui.Hex("#C8C8C8"),
		blocks: reader.Prepare(&reader.Content{Status: "ready", HTML: proofHTML}).Sketch(32)})
	for _, p := range []float32{0.06, 0.5} {
		a, tt := shots(ui.Preferences{})
		it := add(a, "proof", packTaking, p, nil, nil)
		it.base, it.basing = proof, true
		settle(tt)
		name := fmt.Sprintf("proof-p%02d", int(p*100))
		save("island-"+name, crop(tt, islandArea(a, 200)))
		clickAccess(t, tt, "proof，正在拓")
		settle(tt)
		if d, ok := tt.Access("proof"); ok {
			save("card-"+name, crop(tt, ui.Rect{X: d.Bounds.X - 40, Y: d.Bounds.Y - 40, W: d.Bounds.W + 80, H: d.Bounds.H + 80}))
		}
	}

	// Less motion: developing, and taken.
	a, tt = shots(ui.Preferences{ReduceMotion: true})
	it = add(a, "tegra", packTaking, 0.5, tegra, tegra)
	settle(tt)
	save("still-p50", crop(tt, islandArea(a, 200)))
	it.state, it.progress = packTaken, 1
	settle(tt)
	save("still-taken", crop(tt, islandArea(a, 200)))

	// 收进收藏 on the island pointed at; let go of, the thumbnail flies into 收藏's tab.
	a, tt = shots(ui.Preferences{})
	it = add(a, "tegra", packTaken, 1, tegra, tegra)
	settle(tt)
	if acc, ok := tt.Access("tegra，已拓下"); ok {
		tt.Move(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	}
	settle(tt)
	save("island-hide", crop(tt, islandArea(a, 200)))
	tt.Move(600, 600)
	tt.Frame()
	for i := 0; !it.filed && i < 80; i++ {
		tt.Advance(100 * time.Millisecond)
	}
	for i := range 5 {
		save("file-"+string(rune('1'+i)), crop(tt, ui.Rect{W: 1280, H: 240}))
		tt.Advance(150 * time.Millisecond)
	}
	settle(tt)
	save("file-landed", crop(tt, ui.Rect{W: 1280, H: 240}))

	// In 收藏: the block of pages captured, zoomed into, pointed at, and copied from.
	a, tt = shots(ui.Preferences{})
	for _, p := range []struct {
		name string
		img  image.Image
	}{{"obys", obys}, {"codrops", codrops}, {"tegra", tegra}} {
		it := add(a, p.name, packTaken, 1, p.img, p.img)
		it.filed = true
		screens := filepath.Join(a.capture.dir, it.ID, "screens")
		must(t, os.MkdirAll(screens, 0o755))
		out, err := os.Create(filepath.Join(screens, "00.jpg"))
		must(t, err)
		must(t, jpeg.Encode(out, p.img, &jpeg.Options{Quality: 90}))
		out.Close()
	}
	clickAccess(t, tt, "收藏")
	await(t, tt, posts, func() bool {
		for _, id := range []string{"obys", "codrops", "tegra"} {
			if p := a.favs().pics["pack:"+packKey(a.capture.find(id))]; p == nil || !p.done {
				return false
			}
		}
		return true
	})
	settle(tt)
	save("favorites-map", tt.Image())
	clickAccess(t, tt, "拓本，3 页")
	settle(tt)
	settle(tt)
	save("favorites-captures", tt.Image())
	if acc, ok := tt.Access("codrops"); ok {
		tt.Move(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	}
	settle(tt)
	save("favorites-captures-hover", tt.Image())
	clickAccess(t, tt, "codrops")
	settle(tt)
	if err := tt.Click("复制"); err != nil {
		t.Fatal(err)
	}
	for range 60 {
		tt.Advance(20 * time.Millisecond)
	}
	tt.Move(5, 600)
	tt.Advance(100 * time.Millisecond)
	save("favorites-copied", tt.Image())

	// The loop out of sight: the island pointed at, put away into 收藏 (its way
	// into the tab frame by frame, the tab's orbit filling), the tab pointed at
	// and further on, 收藏's captures it opens with the page still being taken,
	// then the tab saying a page was taken, and the note of one that failed.
	a, tt = shots(ui.Preferences{})
	add(a, "codrops", packTaken, 1, codrops, codrops)
	it = add(a, "tegra", packTaking, 0.42, tegra, tegra)
	settle(tt)
	if acc, ok := tt.Access("tegra，正在拓"); ok {
		tt.Move(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	}
	settle(tt)
	save("loop-1-pointed", crop(tt, islandArea(a, 320)))
	clickAccess(t, tt, "收进收藏")
	tt.Move(900, 600)
	must(t, os.MkdirAll(filepath.Join(out, "away"), 0o755))
	for i := range 24 {
		tt.Advance(40 * time.Millisecond)
		save(fmt.Sprintf("away/%02d", i), crop(tt, ui.Rect{W: 1280, H: 260}))
	}
	settle(tt)
	tabArea := ui.Rect{W: 320, H: 150}
	save("loop-2-tab", crop(tt, tabArea))
	save("loop-2-tab-window", tt.Image())
	tab := a.favTab
	tt.Move(tab.X+tab.W/2, tab.Y+tab.H/2)
	settle(tt)
	save("loop-3-tab-pointed", crop(tt, ui.Rect{W: 640, H: 150}))
	tt.Move(900, 600)
	it.progress = 0.8
	settle(tt)
	save("loop-3-tab-80", crop(tt, tabArea))
	clickAccess(t, tt, "收藏")
	settle(tt)
	settle(tt)
	save("loop-4-shelf", tt.Image())
	clickAccess(t, tt, "首页")
	settle(tt)
	settle(tt)
	it.state, it.progress = packTaken, 1
	a.capture.told, a.capture.away = it, true
	tt.Frame()
	must(t, os.MkdirAll(filepath.Join(out, "taken"), 0o755))
	for i := range 10 {
		tt.Advance(40 * time.Millisecond)
		save(fmt.Sprintf("taken/%02d", i), crop(tt, tabArea))
	}
	settle(tt)
	save("loop-5-taken", crop(tt, tabArea))
	for range 40 {
		tt.Advance(100 * time.Millisecond)
	}
	save("loop-5-rest", crop(tt, tabArea))
	failing := add(a, "obys", packTaking, 0.3, obys, obys)
	failing.filed, a.capture.away = true, true
	settle(tt)
	failing.state, failing.failed = packFailed, packFailure(errors.New("explore: the page did not load"))
	settle(tt)
	settle(tt)
	save("loop-6-failed", crop(tt, ui.Rect{W: 640, H: 200}))

	// The button, idle, developing and lit, in the reader's bar.
	b, tt2, _ := openLobehub(t)
	tt2.SetScale(2)
	settle(tt2)
	acc, ok := tt2.AccessByRole(ui.RoleButton, "拓本")
	if !ok {
		t.Fatal("no button")
	}
	bar := ui.Rect{X: acc.Bounds.X - 40, Y: acc.Bounds.Y - 8, W: 160, H: 48}
	save("button-idle", crop(tt2, bar))
	tt2.Move(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	settle(tt2)
	save("button-hover", crop(tt2, bar))
	tt2.Move(600, 600)
	pk := &packItem{ID: "lobe", Name: "Innei", Page: lobehub, state: packTaking, progress: 0.4}
	b.capture.items = []*packItem{pk}
	settle(tt2)
	save("button-p40", crop(tt2, bar))
	save("reader-p40", tt2.Image())
	pk.state, pk.progress = packTaken, 1
	settle(tt2)
	tt2.Advance(time.Second)
	save("button-lit", crop(tt2, bar))
	save("reader-lit", tt2.Image())
	// Its card, opened from it and copied from: the card back in it, its dots a check.
	tt2.ClickAt(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	settle(tt2)
	if err := tt2.Click("复制"); err != nil {
		t.Fatal(err)
	}
	tt2.Move(600, 600)
	for range 70 {
		tt2.Advance(20 * time.Millisecond)
	}
	save("button-copied", crop(tt2, bar))
}

// Opened from the keys, the card's focus is on 复制, whose tip shows: one Esc
// still sends the card back.
func TestCaptureCardEscape(t *testing.T) {
	f := newFakePacker()
	a, tt, posts := captureTester(t, t.TempDir(), f)
	it := a.takePage("p1", "https://one.example/", "One", nil, ui.Rect{}, tt.Now())
	started(t, f)
	f.steps <- packStep{end: true}
	await(t, tt, posts, func() bool { return it.state == packTaken })
	settle(tt)
	for i := 0; i < 40 && !tt.Focused("One，已拓下"); i++ {
		tt.Key(0, ui.KeyTab)
		tt.Frame()
	}
	tt.Key(0, ui.KeyEnter)
	settle(tt)
	if a.capture.card != it.ID {
		t.Fatal("Enter on the thumbnail did not open its card")
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if a.capture.card != "" {
		t.Error("one Esc left the card open")
	}
}

// A pack goes on growing once taken: its card, opened again, pages through the
// screens added and counts what the folder holds now, which packs.json keeps.
func TestCaptureRecount(t *testing.T) {
	dir := t.TempDir()
	f := newFakePacker()
	a, tt, posts := captureTester(t, dir, f)
	it := a.takePage("p1", "https://one.example/", "One", nil, ui.Rect{}, tt.Now())
	started(t, f)
	f.steps <- packStep{end: true}
	await(t, tt, posts, func() bool { return it.state == packTaken })
	a.openPack(it, ui.Rect{}, homeIsland)
	await(t, tt, posts, func() bool { return it.pages != nil }) // its first screen read, the card reads its page
	if len(it.pages.tops) != 1 {
		t.Fatalf("the card read %v, want the one screen its index lists", it.pages)
	}
	// What the background adds: two screens, the index listing all five, a picture.
	folder := filepath.Join(dir, "packs", it.ID)
	var index strings.Builder
	for i := range 5 {
		name := fmt.Sprintf("screens/%02d.jpg", i)
		if i >= 3 {
			must(t, os.WriteFile(filepath.Join(folder, filepath.FromSlash(name)), []byte("x"), 0o644))
		}
		fmt.Fprintf(&index, "- `%s` at scroll %d px\n", name, i*900)
	}
	must(t, os.WriteFile(filepath.Join(folder, "README.md"), []byte(index.String()), 0o644))
	must(t, os.WriteFile(filepath.Join(folder, "files", "images", "5"), []byte("x"), 0o644))
	a.capture.card = ""
	settle(tt)
	a.openPack(it, ui.Rect{}, homeIsland)
	await(t, tt, posts, func() bool { return it.Screens == 5 })
	settle(tt)
	if it.Images != 6 || it.Fonts != 2 {
		t.Errorf("counted %d pictures and %d fonts, want 6 and 2", it.Images, it.Fonts)
	}
	if it.pages == nil || len(it.pages.tops) != 5 {
		t.Errorf("the card pages through %v, want the five screens", it.pages)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "packs.json")); err != nil || !strings.Contains(string(data), `"screens": 5`) {
		t.Errorf("packs.json keeps %s (%v), want five screens", data, err)
	}
}

func TestSlugOf(t *testing.T) {
	for in, want := range map[string]string{
		"What is a researcher’s worth?": "what-is-a-researchers-worth",
		"  Tegra — NYC  ":               "tegra-nyc",
		"中文 标题":                         "中文-标题",
		"???":                           "",
	} {
		if got := slugOf(in); got != want {
			t.Errorf("slugOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// proofHTML is an article's start, as the proofs in the tests set it.
const proofHTML = `<p>The dependencies of a package are of <a href="https://example.com/">different kinds</a>: some
ship with it, some only build it, and some are there for the tests alone.</p><p>Keeping them apart in the manifest
makes each easier to update, and tells a reader at a glance what the package needs to run.</p>
<h2>In practice</h2><pre><code>{
  "dependencies": {},
  "devDependencies": {}
}</code></pre><p>The rest of this note walks through each kind in turn.</p>`

// An article's proof sets its title from the title's row in the column and
// nothing above it or past the column; a long title wraps; Chinese is told apart.
func TestCaptureProof(t *testing.T) {
	p := proofPage{title: strings.Repeat("Accessibility Testing ", 5), source: "Smashing Magazine", date: "11月18日",
		blocks: []reader.Sketch{{Kind: reader.Paragraph, Runs: []reader.SketchRun{{Text: strings.Repeat("word ", 300)}}}}}
	img := proofOf(p, cardCols, cardRows, cardProof)
	ink := func(x, y int) bool {
		r, _, _, _ := img.At(x, y).RGBA()
		return r>>8 > uint32(theme.Window.R)+8
	}
	top := cardProof.titleTop * proofDot
	for y := range img.Rect.Dy() {
		for x := range img.Rect.Dx() {
			if ink(x, y) && (x < 204 || x >= 852 || y < top) {
				t.Fatalf("ink at %d, %d: outside the column or above the title", x, y)
			}
		}
	}
	if !ink(205, top+1) || !ink(205, top+cardProof.titleLine*proofDot+1) {
		t.Error("the title's first or second line is not set")
	}
	if !cjkText("我的 AI Native iOS 开发验证工作流", nil) || cjkText("Categorize Your Dependencies", nil) {
		t.Error("Chinese not told from English")
	}
	if pic := proofPic(p); len(pic.thumb.c) != thumbCols*thumbRows || len(pic.card.c) != cardCols*cardRows {
		t.Error("the proof's dots are not the thumbnail's and the card's")
	}
}
