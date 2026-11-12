package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"maps"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/motion"
	"trie/internal/reading"
)

// workTester opens an article kept as a work page whose first screen is grey
// and whose recording is clip.
func workTester(t *testing.T, opt ui.TesterOptions, clip []byte) (*App, *ui.Tester, *readerPage) {
	t.Helper()
	return workTesterWith(t, opt, func(*App, *library.Store) map[string]any {
		return map[string]any{"video": "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(clip)}
	})
}

// workTesterWith opens the work page with the fields media gives it.
func workTesterWith(t *testing.T, opt ui.TesterOptions, media func(*App, *library.Store) map[string]any) (*App, *ui.Tester, *readerPage) {
	t.Helper()
	a, store := newTestApp(t)
	first := image.NewRGBA(image.Rect(0, 0, 64, 40))
	for i := range first.Pix {
		first.Pix[i] = 100
	}
	var pic bytes.Buffer
	must(t, png.Encode(&pic, first))
	fields := map[string]any{"name": "Test Work", "designer": nil, "site": nil, "host": nil, "builtWith": nil,
		"facts": []any{}, "sections": []any{}, "poster": "data:image/png;base64," + base64.StdEncoding.EncodeToString(pic.Bytes())}
	for k, v := range media(a, store) {
		fields[k] = v
	}
	work, err := json.Marshal(fields)
	must(t, err)
	kept, err := store.Content(lobehub)
	must(t, err)
	kept.Work = work
	must(t, store.SaveContent(*kept))
	tt := ui.NewTesterWith(a.View, 1280, 850, opt)
	settle(tt)
	a.Press("author:innei")
	settle(tt)
	a.Press("open:" + lobehub)
	tt.Frame()
	return a, tt, a.readers[lobehub]
}

// heroAt is the colour of the picture a quarter of the way in, clear of the
// play mark in its middle.
func heroAt(t *testing.T, tt *ui.Tester) color.RGBA {
	t.Helper()
	r, ok := tt.Find("Test Work 的首屏")
	if !ok {
		t.Fatal("no first screen")
	}
	return tt.Image().RGBAAt(int(r.X+r.W/4), int(r.Y+r.H/4))
}

// until runs the clock on, giving the decoder real time between frames,
// until ok or a few seconds have passed.
func until(t *testing.T, tt *ui.Tester, what string, ok func() bool) {
	t.Helper()
	for range 100 {
		if ok() {
			return
		}
		tt.Advance(50 * time.Millisecond)
		time.Sleep(10 * time.Millisecond)
		if !tt.FramePending() {
			tt.Frame() // the jobs' results come as a frame builds
		}
	}
	t.Fatal(what)
}

// posterGrey tells the first screen's grey, as drawn, from the recording's
// colours.
func posterGrey(c color.RGBA) bool { return c.R == c.G && c.G == c.B && c.R > 90 && c.R < 140 }

// h264 is ../video/testdata/h264.mp4: six frames, red, green, blue, white,
// black and yellow, over 0.7 s.
func h264(t *testing.T) []byte {
	clip, err := os.ReadFile("../video/testdata/h264.mp4")
	must(t, err)
	return clip
}

// The first screen shows first; the recording takes over once it opens, and
// plays on; the picture pauses it, dimmed, and plays it on.
func TestWorkRecording(t *testing.T) {
	_, tt, rp := workTester(t, ui.TesterOptions{}, h264(t))
	if _, ok := tt.Find("Test Work 的首屏"); !ok || rp.rec.player != nil {
		t.Fatalf("the page does not open on its first screen: %v, %v", ok, rp.rec.player)
	}
	if _, ok := tt.Find("播放 Test Work 的页面录屏"); !ok {
		t.Error("the picture is not the recording's control before it plays")
	}
	until(t, tt, "the recording does not open", func() bool { return rp.rec.player != nil })
	tt.Advance(400 * time.Millisecond) // its fade
	seen := map[color.RGBA]bool{}
	until(t, tt, "the recording does not play on", func() bool {
		if c := heroAt(t, tt); !posterGrey(c) {
			seen[c] = true
		}
		return len(seen) >= 3
	})
	acc, ok := tt.AccessByRole(ui.RoleToggleButton, "暂停 Test Work 的页面录屏")
	if !ok || !acc.Checked {
		t.Fatalf("playing, the control is not pressed: %+v", acc)
	}
	must(t, tt.Click("暂停 Test Work 的页面录屏"))
	settle(tt)
	if rp.rec.player.Playing() {
		t.Fatal("a click does not pause it")
	}
	held := heroAt(t, tt)
	for range 5 {
		tt.Advance(100 * time.Millisecond)
		time.Sleep(10 * time.Millisecond)
	}
	if got := heroAt(t, tt); got != held {
		t.Errorf("paused, it moves on: %v then %v", held, got)
	}
	if _, ok := tt.Find("播放 Test Work 的页面录屏"); !ok {
		t.Error("paused, the control does not offer to play")
	}
	must(t, tt.Click("播放 Test Work 的页面录屏"))
	tt.Frame()
	if !rp.rec.player.Playing() {
		t.Fatal("a second click does not play it on")
	}
	until(t, tt, "played on, it stays", func() bool { return heroAt(t, tt) != held })
	if p := rp.rec.dec.Progress(); p <= 0 || p > 1 {
		t.Errorf("the trail is at %v", p)
	}
}

// From the keys: Tab reaches the picture, Space pauses the recording, with
// the play mark over it, and plays it on.
func TestWorkRecordingKeys(t *testing.T) {
	_, tt, rp := workTester(t, ui.TesterOptions{}, h264(t))
	until(t, tt, "the recording does not open", func() bool { return rp.rec.player != nil })
	settle(tt)
	focused := func() bool {
		acc, ok := tt.AccessByRole(ui.RoleToggleButton, "暂停 Test Work 的页面录屏")
		return ok && acc.Focused
	}
	for range 40 {
		if focused() {
			break
		}
		tt.Key(0, ui.KeyTab)
		tt.Frame()
	}
	if !focused() {
		t.Fatal("Tab does not reach the recording")
	}
	tt.Key(0, ui.KeySpace)
	tt.Advance(50 * time.Millisecond)
	if rp.rec.player.Playing() {
		t.Fatal("Space does not pause it")
	}
	r, _ := tt.Find("Test Work 的首屏")
	img := tt.Image()
	if mark := img.RGBAAt(int(r.X+r.W/2+16), int(r.Y+r.H/2)); mark.R > 40 {
		t.Errorf("paused and tabbed to, the picture shows no play mark: %v", mark)
	}
	// Dimmed at once, as the keys change things: brightness(0.7) of a frame's full colour (but black).
	if c := img.RGBAAt(int(r.X+r.W/4), int(r.Y+r.H/4)); max(c.R, c.G, c.B) > 20 && (max(c.R, c.G, c.B) > 190 || max(c.R, c.G, c.B) < 160) {
		t.Errorf("paused, the picture is not dimmed: %v", c)
	}
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if !rp.rec.player.Playing() {
		t.Fatal("Space does not play it on")
	}
}

// Left, the page's recording stops decoding where it stood; back on the page,
// the same player goes on from there.
func TestWorkRecordingLeftAndBack(t *testing.T) {
	a, tt, rp := workTester(t, ui.TesterOptions{}, h264(t))
	until(t, tt, "the recording does not open", func() bool { return rp.rec.player != nil })
	settle(tt)
	p, d := rp.rec.player, rp.rec.dec
	a.Press("back")
	settle(tt)
	time.Sleep(100 * time.Millisecond) // the frames decoded ahead
	at := d.Progress()
	for range 5 {
		tt.Advance(200 * time.Millisecond)
		time.Sleep(20 * time.Millisecond)
	}
	if a.page.kind != "author" || d.Progress() != at {
		t.Fatalf("away on %v, the recording decodes on: %v then %v", a.page, at, d.Progress())
	}
	a.Press("open:" + lobehub)
	tt.Frame()
	until(t, tt, "back on the page, the recording does not go on", func() bool { return d.Progress() != at })
	if rp.rec.player != p || !p.Playing() {
		t.Error("back on the page, the recording started over")
	}
}

// A recording the read did not keep within its wait is fetched once the
// page shows, through the reader's fetch, kept with the article, and plays.
func TestWorkRecordingLate(t *testing.T) {
	const src = "https://gallery.example/rec.mp4"
	var asked atomic.Int32
	_, tt, rp := workTesterWith(t, ui.TesterOptions{}, func(a *App, store *library.Store) map[string]any {
		clip := h264(t)
		a.opt.Reading = reading.New(store, func() fetch.Fetch {
			return func(ctx context.Context, u string, scope func(string) bool, n int) (*fetch.Response, error) {
				if u != src {
					return offline()(ctx, u, scope, n)
				}
				asked.Add(1)
				return &fetch.Response{URL: u, Status: 200, Type: "video/mp4", Body: clip}, nil
			}
		}, reading.Worker)
		return map[string]any{"video": nil, "videoSource": src}
	})
	until(t, tt, "the recording is not fetched and played", func() bool { return rp.rec.player != nil })
	if asked.Load() != 1 || rp.work.Video == nil {
		t.Errorf("fetched %d times, kept %v", asked.Load(), rp.work.Video != nil)
	}
}

// Under less motion the recording waits to be asked: the first screen and
// the play mark, until the picture is clicked; then it plays, as ui.Video
// does not, and pauses.
func TestWorkRecordingStill(t *testing.T) {
	_, tt, rp := workTester(t, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}}, h264(t))
	settle(tt)
	tt.Advance(time.Second)
	if rp.rec.player != nil || rp.rec.still != nil || rp.rec.opening || !posterGrey(heroAt(t, tt)) {
		t.Fatalf("under less motion the recording starts on its own: %v", heroAt(t, tt))
	}
	must(t, tt.Click("播放 Test Work 的页面录屏"))
	seen := map[color.RGBA]bool{}
	until(t, tt, "asked, the recording does not play", func() bool {
		if c := heroAt(t, tt); !posterGrey(c) {
			seen[c] = true
		}
		return len(seen) >= 3
	})
	must(t, tt.Click("暂停 Test Work 的页面录屏"))
	settle(tt)
	held := heroAt(t, tt)
	for range 5 {
		tt.Advance(100 * time.Millisecond)
		time.Sleep(10 * time.Millisecond)
	}
	if got := heroAt(t, tt); got != held || !rp.rec.still.paused {
		t.Errorf("paused, it moves on: %v then %v", held, got)
	}
}

// A recording that does not decode leaves the first screen alone, with no
// control over it.
func TestWorkRecordingBroken(t *testing.T) {
	_, tt, rp := workTester(t, ui.TesterOptions{}, []byte("\x00\x00\x00\x18ftypmp42 and no more"))
	until(t, tt, "the broken recording is not given up", func() bool { return rp.rec.failed })
	settle(tt)
	if _, ok := tt.Find("Test Work 的首屏"); !ok {
		t.Error("the first screen went with the recording")
	}
	if _, ok := tt.Find("播放 Test Work 的页面录屏"); ok {
		t.Error("a recording that does not play still offers to")
	}
}

// A recording is written to the data folder once, named by its content, and
// only the eight used last are kept.
func TestRecordingFile(t *testing.T) {
	dir := t.TempDir()
	clip := func(i int) string {
		return "data:video/mp4;base64," + base64.StdEncoding.EncodeToString([]byte{0, 0, 0, 8, 'f', 't', 'y', 'p', byte(i)})
	}
	first, err := recordingFile(dir, clip(0))
	must(t, err)
	if data, err := os.ReadFile(first); err != nil || len(data) != 9 || filepath.Ext(first) != ".mp4" {
		t.Fatalf("%s: %v %v", first, data, err)
	}
	old := time.Now().Add(-time.Hour)
	must(t, os.Chtimes(first, old, old))
	if again, err := recordingFile(dir, clip(0)); err != nil || again != first {
		t.Errorf("written again: %s, %v", again, err)
	}
	for i := 1; i <= 9; i++ {
		_, err := recordingFile(dir, clip(i))
		must(t, err)
		if i == 1 {
			// first is now older than every other but the one written next.
			must(t, os.Chtimes(first, old.Add(time.Minute), old.Add(time.Minute)))
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "recordings", "*"))
	if len(files) != 8 {
		t.Errorf("%d kept: %v", len(files), files)
	}
	if _, err := os.Stat(first); err == nil {
		t.Error("the recording used longest ago is kept")
	}
	if _, err := recordingFile(dir, "data:video/mp4,not base64"); err == nil {
		t.Error("a recording that is not base64 is written")
	}
}

// shotColors are the colours of the shots of shotsTester's work, Shot 1 to
// Shot 7: the second did not load.
var shotColors = []color.RGBA{{230, 40, 40, 255}, {}, {40, 40, 230, 255}, {40, 200, 40, 255}, {230, 200, 40, 255}, {200, 40, 200, 255}, {40, 200, 200, 255}}

// shotsTester opens the work page with seven shots, a strip wider than the
// column, 200 below the top of the window.
func shotsTester(t *testing.T) (*App, *ui.Tester, *readerPage) {
	t.Helper()
	var sections []any
	for i, col := range shotColors {
		var data any
		if col.A != 0 {
			m := image.NewRGBA(image.Rect(0, 0, 160, 100))
			for p := 0; p < len(m.Pix); p += 4 {
				m.Pix[p], m.Pix[p+1], m.Pix[p+2], m.Pix[p+3] = col.R, col.G, col.B, 255
			}
			var b bytes.Buffer
			must(t, png.Encode(&b, m))
			data = "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
		}
		sections = append(sections, map[string]any{"name": fmt.Sprintf("Shot %d", i+1), "image": data})
	}
	a, tt, rp := workTesterWith(t, ui.TesterOptions{}, func(*App, *library.Store) map[string]any {
		return map[string]any{"sections": sections, "video": nil}
	})
	settle(tt)
	for range 3 {
		b, ok := tt.Find("Test Work 的页面截图")
		if !ok {
			t.Fatal("no strip of shots")
		}
		if abs32(b.Y-200) < 1 {
			break
		}
		rp.r.Scroll.Y = max(0, rp.r.Scroll.Y+b.Y-200)
		tt.Frame()
		settle(tt)
	}
	return a, tt, rp
}

// A two-finger swipe that starts on the strip of shots while it can still
// scroll that way is the strip's (ReaderBar.tsx scrollsAside): it scrolls
// and the page stays; at the strip's start the swipe goes back.
func TestWorkShotsSwipe(t *testing.T) {
	a, tt, rp := shotsTester(t)
	strip, _ := tt.Find("Test Work 的页面截图")
	x, y := strip.X+strip.W/2, strip.Y+30
	tt.Move(x, y)
	tt.Advance(time.Second)
	for range 8 {
		tt.Scroll(x, y, 20, 0)
		tt.Advance(16 * time.Millisecond)
	}
	if !rp.r.ScrollsAside(x, y, -1) || rp.swipe.pulling {
		t.Fatalf("along the strip: it scrolls %v, the swipe pulls %v", rp.r.ScrollsAside(x, y, -1), rp.swipe.pulling)
	}
	tt.Advance(time.Second)
	for range 20 {
		tt.Scroll(x, y, -10, 0)
		tt.Advance(30 * time.Millisecond)
	}
	if rp.swipe.pulling || a.page.kind != "reader" {
		t.Fatalf("back along a strip that scrolls back: the swipe pulls %v, on %v", rp.swipe.pulling, a.page)
	}
	tt.Advance(time.Second)
	if rp.r.ScrollsAside(x, y, -1) {
		t.Fatal("the strip is not back at its start")
	}
	for range 11 {
		tt.Scroll(x, y, -10, 0)
		tt.Advance(30 * time.Millisecond)
	}
	if !rp.swipe.pulling {
		t.Fatal("at its start the strip leaves the swipe its pull")
	}
	tt.Advance(150 * time.Millisecond)
	if a.page != (page{"author", "innei"}) {
		t.Fatalf("armed at the strip's start, it goes back: on %v", a.page)
	}
}

// A shot clicked opens in the viewer (WorkShots, ImageViewer), out of its
// place in the strip, which stays empty while it shows: 截图, its name and
// its place in the set under it, 上一张 and 下一张 at the sides. ← and →
// step through the set and round it, the shot that did not load showing
// as a missing picture; by the pointer the picture shown lets go where it
// is while the next comes in, from the keys the next is simply there. Esc
// closes it, the focus back on the shot that opened it, as Base UI gives it
// back; closed by the pointer, the picture flies back into the place of the
// one shown, even past the strip's edge, as long as it is in the window.
func TestWorkShotsViewer(t *testing.T) {
	a, tt, rp := shotsTester(t)
	v := &a.viewer
	if acc, ok := tt.Access("Shot 2：截图没能载入"); !ok || !acc.Disabled {
		t.Errorf("a shot that did not load is a disabled button: %v %+v", ok, acc)
	}
	rp.lateWait = map[reading.Part]bool{1: true}
	tt.Frame()
	if acc, ok := tt.Access("Shot 2：截图载入中"); !ok || !acc.Disabled {
		t.Errorf("a shot on its way is a disabled button: %v %+v", ok, acc)
	}
	rp.lateWait = nil
	tt.Frame()
	first, ok := tt.Access("查看截图：Shot 1")
	if !ok {
		t.Fatal("the first shot is not a button")
	}
	at := func(x, y float32) color.RGBA { return tt.Image().RGBAAt(int(x), int(y)) }
	sx, sy := center(first.Bounds)
	sy = first.Bounds.Y + first.Bounds.W*10/16/2 // the middle of its picture
	tt.Move(sx, sy)
	tt.Advance(time.Second)
	tt.ClickAt(sx, sy)
	if !v.open || !v.travel {
		t.Fatalf("clicked: open %v, travels %v", v.open, v.travel)
	}
	if _, ok := tt.AccessByRole(ui.RoleDialog, "截图"); !ok || !tt.Focused("上一张") {
		t.Errorf("a dialog called 截图, the focus on 上一张: %v, %v", ok, tt.Focused("上一张"))
	}
	tt.Advance(time.Second)
	shows := func(s ...string) bool {
		for _, s := range s {
			if _, ok := tt.Find(s); !ok {
				return false
			}
		}
		return true
	}
	if !shows("Shot 1", "1 / 7", "上一张", "下一张", "关闭") {
		t.Fatalf("the shot's name, 1 / 7 and the arrows: %q", tt.Texts())
	}
	if c := at(sx, sy); c.R > 120 {
		t.Errorf("its place in the strip stays empty while it shows: %v", c)
	}
	big, ok := tt.AccessByRole(ui.RoleImage, "Shot 1")
	if !ok {
		t.Fatal("the shot does not show large")
	}
	px, py := center(big.Bounds)
	step := func(k ui.Key, want string) {
		t.Helper()
		tt.Key(0, k)
		tt.Advance(20 * time.Millisecond) // a step shows a frame later
		if !shows(want) {
			t.Fatalf("stepping to %s: %q", want, tt.Texts())
		}
	}
	step(ui.KeyRight, "2 / 7")
	if _, ok := tt.Access("图片没能载入：Shot 2"); !ok {
		t.Error("the shot that did not load shows as a missing picture")
	}
	step(ui.KeyLeft, "1 / 7")
	step(ui.KeyLeft, "7 / 7")
	step(ui.KeyRight, "1 / 7")
	step(ui.KeyRight, "2 / 7")
	step(ui.KeyRight, "3 / 7")
	if c := at(px, py); c.B < 200 || c.R > 60 {
		t.Errorf("from the keys the next is simply there: %v", c)
	}
	// By the pointer: Shot 3 lets go while Shot 4 comes in.
	next, _ := tt.Access("下一张")
	tt.Move(center(next.Bounds))
	tt.Advance(time.Second)
	tt.ClickAt(center(next.Bounds))
	tt.Advance(16 * time.Millisecond)
	tt.Advance(40 * time.Millisecond)
	if c := at(px, py); !shows("4 / 7") || c.B < 40 || c.G < 40 {
		t.Errorf("by the pointer the two cross: %v, %q", c, tt.Texts())
	}
	tt.Advance(400 * time.Millisecond)
	if c := at(px, py); c.B > 60 || c.G < 150 {
		t.Errorf("crossed, Shot 4 alone: %v", c)
	}
	// Esc: the focus goes back to the shot that opened it.
	tt.Key(0, ui.KeyEscape)
	tt.Advance(16 * time.Millisecond)
	if v.open || !tt.Focused("查看截图：Shot 1") {
		t.Fatalf("Esc: open %v, the focus back on Shot 1 %v", v.open, tt.Focused("查看截图：Shot 1"))
	}
	if c := at(sx, sy); c.R < 200 {
		t.Errorf("closed, Shot 1 is back in its place: %v", c)
	}
	// Enter opens it at once; ← goes round to Shot 7, past the strip's
	// edge; closed by the pointer it flies back there.
	tt.Key(0, ui.KeyEnter)
	tt.Advance(16 * time.Millisecond)
	if !v.open || v.travel || !v.instant {
		t.Fatalf("Enter: open %v, travels %v, at once %v", v.open, v.travel, v.instant)
	}
	step(ui.KeyLeft, "7 / 7")
	tt.ClickAt(640, 830)
	if v.open || v.until.Sub(tt.Now()) != motion.TravelOut {
		t.Errorf("closed by the pointer, it flies back to Shot 7's place: open %v, for %v", v.open, v.until.Sub(tt.Now()))
	}
	tt.Advance(time.Second)
	if !tt.Focused("查看截图：Shot 1") {
		t.Error("the focus is not back on Shot 1")
	}
}

// workWith opens a work page whose body is html and whose work has fields
// (over workTesterWith's, and no recording), settled.
func workWith(t *testing.T, html string, fields map[string]any) (*App, *ui.Tester, *readerPage) {
	t.Helper()
	a, tt, rp := workTesterWith(t, ui.TesterOptions{}, func(_ *App, store *library.Store) map[string]any {
		kept, err := store.Content(lobehub)
		must(t, err)
		kept.HTML, kept.Document, kept.Headings = html, nil, nil
		must(t, store.SaveContent(*kept))
		out := map[string]any{"video": nil}
		maps.Copy(out, fields)
		return out
	})
	settle(tt)
	return a, tt, rp
}

// find is the box of the text s, which must show.
func find(t *testing.T, tt *ui.Tester, s string) ui.Rect {
	t.Helper()
	r, ok := tt.Find(s)
	if !ok {
		t.Fatalf("no %q in %q", s, tt.Texts())
	}
	return r
}

// The body sits under the work's last part as work.css has it, their margins
// collapsing: 24 under who made it, nothing under the strip of shots (its
// 4px of padding) or the first screen, or the first block's own margin when
// larger; who made it sits 16 under the strip.
func TestWorkBodyGap(t *testing.T) {
	shot := []any{map[string]any{"name": "Shot 1", "image": nil}}
	by := map[string]any{"designer": "Someone", "site": "https://work.example/", "host": "work.example", "sections": shot}
	for _, k := range []struct {
		name, html string
		fields     map[string]any
		last       string // what the body sits under
		gap        float32
	}{
		{"who made it", "<p>Words of the work.</p>", by, "由 Someone 设计", 24},
		{"who made it, a heading first", "<h2>Heading</h2><p>Words of the work.</p>", by, "由 Someone 设计", 36}, // 1.8em of 20
		{"the strip", "<p>Words of the work.</p>", map[string]any{"sections": shot}, "Test Work 的页面截图", 0},
		{"the first screen", "<p>Words of the work.</p>", map[string]any{}, "Test Work 的首屏", 0},
	} {
		t.Run(k.name, func(t *testing.T) {
			_, tt, _ := workWith(t, k.html, k.fields)
			last := find(t, tt, k.last)
			body, ok := tt.AccessByRole(ui.RoleArticle, "")
			if !ok {
				t.Fatal("no body")
			}
			if d := body.Bounds.Y - (last.Y + last.H); abs32(d-k.gap) > 0.5 {
				t.Errorf("the body sits %v under %s, not %v", d, k.last, k.gap)
			}
			if k.last == "由 Someone 设计" {
				if d := last.Y - (find(t, tt, "Test Work 的页面截图").Y + find(t, tt, "Test Work 的页面截图").H); abs32(d-16) > 0.5 {
					t.Errorf("who made it sits %v under the strip, not 16", d)
				}
			}
		})
	}
}

// The facts go before the body's first h2 (ArticleBody insertAt), else after
// its last block, their margins (40 above, none under) collapsing with the
// blocks' around them: 40 from the paragraph before them, a heading's 1.8em
// after them; the sign-off 48 under them.
func TestWorkFactsPlace(t *testing.T) {
	facts := map[string]any{"facts": []any{map[string]any{"label": "技术", "values": []any{"WebGL"}}}}
	box := func(t *testing.T, tt *ui.Tester) (top, bottom float32) {
		g, ok := tt.AccessByRole(ui.RoleGroup, "技术")
		if !ok {
			t.Fatalf("no 技术 in %q", tt.Texts())
		}
		return g.Bounds.Y, g.Bounds.Y + g.Bounds.H
	}
	t.Run("before the first h2", func(t *testing.T) {
		_, tt, _ := workWith(t, "<p>Intro.</p><h2>Section</h2><p>More.</p>", facts)
		top, bottom := box(t, tt)
		intro, section := find(t, tt, "Intro."), find(t, tt, "Section")
		if d := top - (intro.Y + intro.H); abs32(d-40) > 0.5 {
			t.Errorf("the facts sit %v under the paragraph before them, not 40", d)
		}
		if d := section.Y - bottom; abs32(d-1.8*20) > 0.5 {
			t.Errorf("the h2 sits %v under the facts, not its 1.8em", d)
		}
	})
	t.Run("after the last block", func(t *testing.T) {
		_, tt, _ := workWith(t, "<p>Intro.</p><p>More.</p>", facts)
		top, bottom := box(t, tt)
		more := find(t, tt, "More.")
		if d := top - (more.Y + more.H); abs32(d-40) > 0.5 {
			t.Errorf("the facts sit %v under the last paragraph, not 40", d)
		}
		sign, ok := tt.AccessByRole(ui.RoleGroup, "文章信息")
		if d := sign.Bounds.Y - bottom; !ok || abs32(d-48) > 0.5 {
			t.Errorf("the sign-off sits %v under the facts, not 48", d)
		}
	})
	t.Run("categories and tags alone show nothing", func(t *testing.T) {
		_, tt, _ := workWith(t, "<p>Intro.</p>", map[string]any{"facts": []any{map[string]any{"label": "类别", "values": []any{"Portfolio"}},
			map[string]any{"label": "标签", "values": []any{"UI design"}}}})
		for _, s := range []string{"类别", "Portfolio", "标签", "UI design"} {
			if _, ok := tt.Find(s); ok {
				t.Errorf("%q shows", s)
			}
		}
	})
}

// The palette is a band as wide as the column, its colours in equal shares,
// each value written in the ink it takes; a click copies the value and the
// band says 已复制 in its place. Under it the credits: the radar of the
// jury's marks round the overall score at its centre, no award.
func TestWorkCredits(t *testing.T) {
	a, tt, _ := workWith(t, "<p>Words.</p>", map[string]any{"facts": []any{
		map[string]any{"label": "荣誉", "values": []any{"Site of the Day · 2026-11-11"}},
		map[string]any{"label": "评分", "values": []any{"总分 7.52", "设计 7.6", "可用性 7.26", "创意 7.78", "内容 7.44"}},
		map[string]any{"label": "配色", "values": []any{"#441363", "#F4F4F2"}}}})
	dark, pale := find(t, tt, "#441363"), find(t, tt, "#F4F4F2")
	band, ok := tt.AccessByRole(ui.RoleGroup, "配色")
	if !ok || abs32(band.Bounds.W-a.column()) > 0.5 || abs32(band.Bounds.H-72) > 0.5 {
		t.Fatalf("the band is not the column's width and 72 high: %v", band.Bounds)
	}
	if abs32(pale.X-dark.X-band.Bounds.W/2) > 0.5 {
		t.Errorf("the colours do not take equal shares: %v, %v in %v", dark, pale, band.Bounds)
	}
	img := tt.Image()
	ink := func(r ui.Rect) uint8 { // the brightest pixel along the value's middle
		var most uint8
		for x := r.X; x < r.X+r.W; x++ {
			most = max(most, img.RGBAAt(int(x), int(r.Y+r.H/2)).R)
		}
		return most
	}
	if ink(dark) < 160 {
		t.Errorf("the value on the dark colour is not written light")
	}
	if least := func() uint8 {
		least := uint8(255)
		for x := pale.X; x < pale.X+pale.W; x++ {
			least = min(least, img.RGBAAt(int(x), int(pale.Y+pale.H/2)).R)
		}
		return least
	}(); least > 100 {
		t.Errorf("the value on the pale colour is not written dark")
	}
	for _, s := range []string{"总分 7.52", "设计", "可用性", "创意", "7.78", "内容"} {
		find(t, tt, s)
	}
	for _, s := range []string{"总分", "荣誉", "Site of the Day"} {
		if _, ok := tt.Find(s); ok {
			t.Errorf("%q shows", s)
		}
	}
	// The radar: 设计 straight above its centre, 可用性 to its right, 创意
	// below, 内容 to its left.
	up, right, down, left := find(t, tt, "设计"), find(t, tt, "可用性"), find(t, tt, "创意"), find(t, tt, "内容")
	if !(up.Y < right.Y && right.Y < down.Y && left.X < up.X && up.X < right.X && abs32(right.Y-left.Y) < 1) {
		t.Errorf("the marks are not round the radar: %v %v %v %v", up, right, down, left)
	}
	acc, _ := tt.Access("总分 7.52")
	if core := acc.Bounds; !(core.Y > up.Y && core.Y < down.Y && core.X+core.W/2 > left.X && core.X+core.W/2 < right.X) {
		t.Errorf("the overall score is not at the radar's centre: %v (%v %v %v %v)", core, up, right, down, left)
	}
	must(t, tt.Click("复制色值 #441363"))
	tt.Frame()
	if got := tt.Clipboard(); got != "#441363" {
		t.Errorf("copied %q", got)
	}
	find(t, tt, "已复制")
	tt.Advance(motion.Copied + time.Second)
	settle(tt)
	find(t, tt, "#441363")
	// From the keys: Tab reaches a colour, Enter copies it.
	for range 60 {
		if tt.Focused("复制色值 #F4F4F2") {
			break
		}
		tt.Key(0, ui.KeyTab)
		tt.Frame()
	}
	if !tt.Focused("复制色值 #F4F4F2") {
		t.Fatal("Tab does not reach the second colour")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Advance(16 * time.Millisecond)
	if got := tt.Clipboard(); got != "#F4F4F2" {
		t.Errorf("Enter copied %q", got)
	}
}

// A shot that arrives late comes in to where it rests, 0.88 of its colour
// (work.css work-shot-in), never past it.
func TestWorkShotLateIn(t *testing.T) {
	_, tt, rp := shotsTester(t)
	first, ok := tt.Access("查看截图：Shot 1")
	if !ok {
		t.Fatal("the first shot is not a button")
	}
	x, y := int(first.Bounds.X+first.Bounds.W/2), int(first.Bounds.Y+first.Bounds.W*10/16/2)
	red := func() int { return int(tt.Image().RGBAAt(x, y).R) }
	rest := red()
	if rp.lateAt == nil {
		rp.lateAt = map[reading.Part]time.Time{}
	}
	rp.lateAt[0] = time.Time{} // it came late: it comes in from the next frame
	tt.Frame()
	most := 0
	for range 30 {
		tt.Advance(16 * time.Millisecond)
		most = max(most, red())
	}
	if most > rest+2 {
		t.Errorf("coming in, the shot shows at %d, past where it rests (%d)", most, rest)
	}
	if got := red(); got > rest+2 || got < rest-2 {
		t.Errorf("it came in to %d, not where it rests (%d)", got, rest)
	}
}
