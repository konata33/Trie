package app

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// openLobehub opens 十个月 LobeHub from Innei's page and lets it settle.
func openLobehub(t *testing.T) (*App, *ui.Tester, *readerPage) {
	t.Helper()
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("Innei"))
	settle(tt)
	must(t, tt.Click(lobeTitle))
	settle(tt)
	return a, tt, a.readers[lobehub]
}

// barTop is where the reader's bar shows: 0 at the top of the content
// area, -53 sent away.
func barTop(t *testing.T, tt *ui.Tester) float32 {
	t.Helper()
	acc, ok := tt.AccessByRole(ui.RoleToolbar, "阅读工具条")
	if !ok {
		t.Fatal("no reader bar")
	}
	return acc.Bounds.Y
}

// scrollBy scrolls the reader as a touchpad does, a frame at a time.
func scrollBy(tt *ui.Tester, dy float32) {
	step := dy / 10
	for range 10 {
		tt.Scroll(700, 500, 0, step)
		tt.Advance(16 * time.Millisecond)
	}
	tt.Advance(time.Second)
}

// Reading down past 200 sends the bar away; scrolling up brings it back.
// Past 4 it is lifted.
func TestBarAwayAndBack(t *testing.T) {
	_, tt, rp := openLobehub(t)
	if y := barTop(t, tt); y != 0 || rp.lifted {
		t.Fatalf("at the top the bar is at %v, lifted %v", y, rp.lifted)
	}
	scrollBy(tt, 150)
	if y := barTop(t, tt); y != 0 || !rp.lifted {
		t.Errorf("150 down (not past 200): the bar is at %v, lifted %v", y, rp.lifted)
	}
	scrollBy(tt, 300)
	if y := barTop(t, tt); y > -50 || !rp.away {
		t.Errorf("450 down: the bar is at %v, away %v", y, rp.away)
	}
	// A slow scroll up, 1.5 a frame, is still a scroll up of more than 6.
	for range 8 {
		tt.Scroll(700, 500, 0, -1.5)
		tt.Advance(16 * time.Millisecond)
	}
	tt.Advance(time.Second)
	if y := barTop(t, tt); y != 0 || rp.away {
		t.Errorf("scrolled up 12: the bar is at %v, away %v", y, rp.away)
	}
	// Moves of no more than 6 leave it as it is.
	tt.Scroll(700, 500, 0, 4)
	tt.Advance(time.Second)
	if rp.away {
		t.Error("a scroll of 4 down sent the bar away")
	}
}

// A move the reader was sent on is not reading down: the saved place on
// arrival, a heading of the outline.
func TestBarStaysForSentMoves(t *testing.T) {
	a, tt, rp := openLobehub(t)
	rp.r.Jump(a.readers[lobehub].content.Headings[3].ID, false)
	tt.Frame()
	tt.Advance(time.Second)
	if rp.r.Scroll.Y < 1000 {
		t.Fatalf("the heading's jump went to %v", rp.r.Scroll.Y)
	}
	if y := barTop(t, tt); y != 0 || rp.away {
		t.Errorf("after a jump to a heading the bar is at %v, away %v", y, rp.away)
	}
	// Gliding there, as a click on the outline does.
	rp.r.Jump(a.readers[lobehub].content.Headings[6].ID, true)
	for range 60 {
		tt.Advance(16 * time.Millisecond)
	}
	if rp.away {
		t.Error("a glide to a heading sent the bar away")
	}
	// Coming back to the article restores its place without sending the bar away.
	scrollBy(tt, -400)
	y := rp.r.Scroll.Y
	tt.ClickAt(460, 30)
	settle(tt)
	must(t, tt.Click(lobeTitle))
	settle(tt)
	if got := a.readers[lobehub].r.Scroll.Y; got < y-2 || got > y+2 {
		t.Fatalf("back in the article at %v, left at %v", got, y)
	}
	if y := barTop(t, tt); y != 0 || rp.away {
		t.Errorf("after the place was restored the bar is at %v, away %v", y, rp.away)
	}
	// Reading on from there does.
	scrollBy(tt, 300)
	if !rp.away {
		t.Error("reading down after the restore did not send the bar away")
	}
}

// Left while it was away, the bar is there again, at once, on coming back,
// and the saved place restored does not send it.
func TestBarStartsOver(t *testing.T) {
	a, tt, rp := openLobehub(t)
	scrollBy(tt, 600)
	if !rp.away {
		t.Fatal("600 down did not send the bar away")
	}
	a.Press("back")
	settle(tt)
	must(t, tt.Click(lobeTitle))
	tt.Advance(16 * time.Millisecond)
	if rp.barY != 0 || rp.away { // the page itself grows in (motion.Deep)
		t.Errorf("a frame into coming back the bar is at %v, away %v", rp.barY, rp.away)
	}
	settle(tt)
	if y := barTop(t, tt); y != 0 || rp.away || rp.r.Scroll.Y < 590 {
		t.Errorf("back at %v the bar is at %v, away %v", rp.r.Scroll.Y, y, rp.away)
	}
}

// What the reader scrolls after a move it was sent on counts, from the
// keyboard too, where nothing moves on its own to build frames between.
func TestBarCountsTheScrollAfterASend(t *testing.T) {
	a, tt, rp := openLobehub(t)
	a.kbd = true
	rp.r.Jump(rp.content.Headings[3].ID, false)
	tt.Frame()
	if rp.away || rp.r.Scroll.Y < 1000 {
		t.Fatalf("the jump to %v sent the bar away (%v)", rp.r.Scroll.Y, rp.away)
	}
	for range 4 {
		tt.Scroll(700, 500, 0, 5)
	}
	if !rp.away {
		t.Error("reading on by 20 after a jump did not send the bar away")
	}
}

// Scrolled from the keyboard, the article opened with the pointer, the bar
// goes and comes at once (DESIGN.md: 键盘触发的动作不加动画): the reader
// has the focus, so the keys are seen as the keyboard's.
func TestBarKeyboard(t *testing.T) {
	a, tt, rp := openLobehub(t)
	tt.Key(0, ui.KeyPageDown)
	tt.Advance(16 * time.Millisecond)
	if !a.kbd || !rp.away || rp.barY != -53 {
		t.Errorf("a frame after PageDown: keyboard %v, away %v, the bar at %v", a.kbd, rp.away, rp.barY)
	}
	tt.Advance(time.Second)
	tt.Key(0, ui.KeyUp)
	tt.Advance(16 * time.Millisecond)
	if rp.away || rp.barY != 0 {
		t.Errorf("a frame after ↑: away %v, the bar at %v", rp.away, rp.barY)
	}
}

// The bar shows how far the article was read, as the library keeps it:
// nothing at 0, the share while being read, 读到结尾 at the end.
func TestBarProgress(t *testing.T) {
	a, tt, _ := openLobehub(t)
	percent := func() []string { return barProgress(tt) }
	if got := percent(); len(got) != 0 {
		t.Errorf("unread, the bar shows %q", got)
	}
	scrollBy(tt, 150) // a sliver read: 0.2%, which rounds to 0
	if got := percent(); len(got) != 0 {
		t.Errorf("at %.4f read the bar shows %q", progressOf(a.article(lobehub)), got)
	}
	scrollBy(tt, 2000)
	scrollBy(tt, -10)
	if got := percent(); len(got) != 2 || got[0] != "读到 "+itoa(percentOf(a.article(lobehub)))+"%" {
		t.Errorf("being read, the bar shows %q", got)
	}
	scrollBy(tt, 20000)
	scrollBy(tt, -10)
	if got := percent(); len(got) != 1 || got[0] != "读到结尾" {
		t.Errorf("at the end (%.2f), the bar shows %q", progressOf(a.article(lobehub)), got)
	}
}

// The bar's progress follows the reading as it goes, not the place the library keeps once it stops; a jump to the
// end measures the end, though the body was not laid out there in the frame it jumped.
func TestBarProgressFollowsReading(t *testing.T) {
	a, tt, rp := openLobehub(t)
	rp.r.Scroll.Y = 1e6
	tt.Frame()
	settle(tt)
	if p := rp.measured.Progress; p != 1 {
		t.Fatalf("at the end the reader measures %v read", p)
	}
	// Back up a little, in less time than the place takes to be kept.
	for range 25 {
		tt.Scroll(700, 500, 0, -80)
		tt.Advance(16 * time.Millisecond)
	}
	live := share(float64(rp.measured.Progress))
	if live >= 95 {
		t.Fatalf("scrolled back only to %d%%", live)
	}
	if kept := progressOf(a.article(lobehub)); kept != 1 {
		t.Fatalf("the library already keeps %v", kept)
	}
	if got := barProgress(tt); len(got) == 0 || got[0] != "读到 "+itoa(live)+"%" {
		t.Errorf("scrolled back to %d%%, the bar says %q", live, got)
	}
}

// Under less motion the bar fades where it is, and the keyboard's moves are
// at once.
func TestBarStill(t *testing.T) {
	a, _ := newTestApp(t)
	tt := ui.NewTesterWith(a.View, 1280, 850, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	settle(tt)
	must(t, tt.Click("Innei"))
	settle(tt)
	must(t, tt.Click(lobeTitle))
	settle(tt)
	rp := a.readers[lobehub]
	scrollBy(tt, 450)
	if y := barTop(t, tt); !rp.away || y != 0 {
		t.Errorf("under less motion, sent away, the bar is at %v (away %v): it should fade where it is", y, rp.away)
	}
}

// barProgress is what the bar says of the progress: the moon's words and
// the share beside it.
func barProgress(tt *ui.Tester) []string {
	var out []string
	for _, s := range tt.Texts() {
		if strings.HasSuffix(s, "%") && len(s) <= 12 || s == "读到结尾" {
			out = append(out, s)
		}
	}
	return out
}

// TestBarRecord writes the bar beside the previous implementation's capture
// (.context/mygo/app-v1/electron/bar.mjs):
// LobeHub scrolled to 0, 150, 700 (the bar sent away) and back up to 600,
// and a GIF of reading down and scrolling up, in real time:
// BAR=dir go test -run BarRecord.
func TestBarRecord(t *testing.T) {
	dir := os.Getenv("BAR")
	if dir == "" {
		t.Skip("BAR not set")
	}
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	tt.SetScale(2)
	settle(tt)
	a.Press("author:innei")
	settle(tt)
	a.Press("open:" + lobehub)
	settle(tt)
	rp := a.readers[lobehub]
	var log strings.Builder
	fmt.Fprintf(&log, "Native (TestBarRecord), LobeHub, the scroll set then 2 s: the bar's top in the content area, away, lifted, the progress texts.\n")
	for _, s := range []struct {
		name string
		y    float32
	}{{"0", 0}, {"150", 150}, {"700-away", 700}, {"600-back", 600}} {
		rp.r.Scroll.Y = s.y
		tt.Frame()
		settle(tt)
		fmt.Fprintf(&log, "%-8s top %v bar %v away %v lifted %v progress %q\n", s.name, rp.r.Scroll.Y,
			barTop(t, tt)-a.area.Y, rp.away, rp.lifted, barProgress(tt))
		if s.name != "150" {
			savePNG(t, filepath.Join(dir, "lobehub-"+s.name+"-mygo.png"), tt.Image())
		}
	}
	os.WriteFile(filepath.Join(dir, "mygo-states.txt"), []byte(log.String()), 0o644)

	// Reading down at 500 a second, a pause, a short scroll up, at 50 wheel
	// events a second and a frame every 40 ms, the content area's top.
	a, _ = newTestApp(t)
	tt = ui.NewTester(a.View, 1280, 850)
	settle(tt)
	a.Press("author:innei")
	settle(tt)
	a.Press("open:" + lobehub)
	settle(tt)
	tt.Move(700, 500)
	tt.Advance(time.Second)
	const tick = 20 * time.Millisecond
	var frames []*image.RGBA
	n := 0
	run := func(d time.Duration, dy float32) {
		for range int(d / tick) {
			if dy != 0 {
				tt.Scroll(700, 500, 0, dy)
			}
			tt.Advance(tick)
			if n++; n%2 == 0 {
				m := tt.Image()
				r := image.Rect(232, 0, 1280, 520)
				f := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
				draw.Draw(f, f.Bounds(), m, r.Min, draw.Src)
				frames = append(frames, f)
			}
		}
	}
	run(400*time.Millisecond, 0)
	run(1600*time.Millisecond, 10)
	run(600*time.Millisecond, 0)
	run(300*time.Millisecond, -8)
	run(1200*time.Millisecond, 0)
	writeGIF(t, filepath.Join(dir, "read-down-scroll-up.gif"), frames, 2*tick)
}
