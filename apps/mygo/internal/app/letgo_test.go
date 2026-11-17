package app

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
)

// letGoWriter is the writer the tests let go: the longest name, which the
// sidebar cuts short, as the study's.
func letGoWriter(a *App) *author {
	w := a.authors[0]
	for _, au := range a.authors {
		if textWidth(au.src.DisplayName, 13) > textWidth(w.src.DisplayName, 13) {
			w = au
		}
	}
	return w
}

// sideRowAt is where the sidebar shows name's row, by its ···: none while
// the row turns into dots or is gone.
func sideRowAt(tt *ui.Tester, name string) (ui.Rect, bool) {
	n, ok := tt.AccessByRole(ui.RoleMenuButton, locales.SidebarWriterActions(name))
	return n.Bounds, ok
}

// unfollowFromMenu lets name go from its sidebar row's ···, with the pointer.
func unfollowFromMenu(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	must(t, tt.Click(locales.SidebarWriterActions(name)))
	tt.Advance(time.Second)
	must(t, tt.Click(locales.CommonUnfollow()))
	tt.Frame()
}

// TestLetGo: a writer let go from the sidebar's menu turns into dots for a
// while (DESIGN.md 取消订阅), the row keeping its place, and is then gone;
// 撤销 brings the row back. From the keys, or under less motion after a
// short fade, the row goes at once.
func TestLetGo(t *testing.T) {
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	au := letGoWriter(a)
	id, name := au.src.ID, au.src.DisplayName
	was, _ := sideRowAt(tt, name)
	unfollowFromMenu(t, tt, name)
	if a.writer(id) != nil {
		t.Fatal("the writer is still followed")
	}
	tt.Advance(time.Second)
	p := a.parting[id]
	if p == nil || !p.dots || p.strips == nil {
		t.Fatalf("a second in, the row should be turning into dots: %+v", p)
	}
	d := motion.LetGo
	if total := p.last + d.Wait + d.Closing(); total < 1500*time.Millisecond || total > 2100*time.Millisecond {
		t.Errorf("the row should take about 1.8 s, takes %v", total)
	}
	// The row below, once the rows have closed up, and once the empty row
	// kept for 撤销 has gone too: it does not move.
	below := a.authors[p.index]
	tt.Advance(1200 * time.Millisecond)
	if _, ok := sideRowAt(tt, name); ok {
		t.Fatal("the row should be gone")
	}
	closed, _ := sideRowAt(tt, below.src.DisplayName)
	if closed.Y != was.Y {
		t.Errorf("the row below should have closed up to %v, is at %v", was.Y, closed.Y)
	}
	tt.Advance(motion.UndoFor)
	if a.parting[id] != nil {
		t.Fatal("the row should be let go once 撤销 is no longer offered")
	}
	if at, _ := sideRowAt(tt, below.src.DisplayName); at.Y != closed.Y {
		t.Errorf("the row below moved from %v to %v as the empty row went", closed.Y, at.Y)
	}

	a.mutate(library.SetSubscription{ID: id, Subscribed: true}, nil)
	settle(tt)

	// 撤销 once the row has closed up: it comes back where it was.
	unfollowFromMenu(t, tt, name)
	tt.Advance(2500 * time.Millisecond)
	must(t, tt.Click(locales.CommonUndo()))
	settle(tt)
	if at, ok := sideRowAt(tt, name); a.writer(id) == nil || !ok || at.Y != was.Y {
		t.Fatalf("撤销 should bring the row back where it was, %v: at %v", was.Y, at.Y)
	}

	// Brought back while still dots: the row is whole again.
	unfollowFromMenu(t, tt, name)
	tt.Advance(600 * time.Millisecond)
	must(t, tt.Click(locales.CommonUndo()))
	settle(tt)
	if a.writer(id) == nil || a.parting[id] != nil {
		t.Fatal("撤销 halfway should bring the row back")
	}

	// Under less motion the row only fades, over 150 ms.
	a.Reduce = true
	unfollowFromMenu(t, tt, name)
	tt.Advance(100 * time.Millisecond)
	if p := a.parting[id]; p == nil || p.strips != nil {
		t.Fatalf("under less motion the row should fade, without dots: %+v", p)
	}
	tt.Advance(200 * time.Millisecond)
	if _, ok := sideRowAt(tt, name); ok || a.parting[id] == nil {
		t.Fatal("under less motion the row should be gone once faded, kept for 撤销")
	}
	must(t, tt.Click(locales.CommonUndo()))
	settle(tt)
	a.Reduce = false

	// From the keys it goes at once.
	must(t, tt.Click(locales.SidebarWriterActions(name)))
	settle(tt)
	tt.Key(0, ui.KeyUp) // to 取消订阅, the last item
	tt.Key(0, ui.KeyEnter)
	tt.Advance(50 * time.Millisecond)
	if a.writer(id) != nil || a.parting[id] != nil {
		t.Fatalf("from the keys the row should go at once: followed %v, parting %+v", a.writer(id) != nil, a.parting[id])
	}
}

// TestLetGoFrames writes the sidebar every 33 ms as a writer is let go from
// its menu, at 2x and 1x, as a sheet, a GIF and its frames; then 撤销, the
// row's coming back, and the same under less motion:
// FRAMES=dir go test -run LetGoFrames.
func TestLetGoFrames(t *testing.T) {
	dir := os.Getenv("FRAMES")
	if dir == "" {
		t.Skip("FRAMES not set")
	}
	const step = 33 * time.Millisecond
	for _, scale := range []float32{2, 1} {
		for _, s := range []struct {
			name   string
			reduce bool
			undo   time.Duration // 撤销 this long after letting go, before the frames start
			span   time.Duration
		}{
			{"leave", false, -1, 2300 * time.Millisecond},
			{"undo", false, 2500 * time.Millisecond, 600 * time.Millisecond},
			{"undo-halfway", false, 700 * time.Millisecond, 600 * time.Millisecond},
			{"reduced", true, -1, 400 * time.Millisecond},
			{"reduced-undo", true, 600 * time.Millisecond, 400 * time.Millisecond},
		} {
			a, _ := newTestApp(t)
			a.Reduce = s.reduce
			tt := ui.NewTester(a.View, 1280, 850)
			tt.SetScale(scale)
			settle(tt)
			name := letGoWriter(a).src.DisplayName
			if _, ok := sideRowAt(tt, "Wilson Tovar (krthr)"); ok {
				name = "Wilson Tovar (krthr)" // the study's
			}
			row, _ := sideRowAt(tt, name)
			// The rows around it, the sidebar's width.
			box := image.Rect(0, int((row.Y-78)*scale), int(232*scale), int((row.Y+107)*scale))
			unfollowFromMenu(t, tt, name)
			if s.undo >= 0 {
				tt.Advance(s.undo)
				must(t, tt.Click(locales.CommonUndo()))
			}
			var frames []*image.RGBA
			t0 := tt.Now()
			for d := time.Duration(0); d <= s.span; d += step {
				tt.Advance(d - tt.Now().Sub(t0))
				frames = append(frames, cropTo(tt.Image(), box))
			}
			out := filepath.Join(dir, fmt.Sprintf("%s-%gx", s.name, scale))
			os.MkdirAll(out, 0o755)
			for i, f := range frames {
				savePNG(t, filepath.Join(out, fmt.Sprintf("%04d.png", i*int(step/time.Millisecond))), f)
			}
			writeSheet(t, out+".png", frames)
			writeGIF(t, out+".gif", frames, step)
		}
	}
	// How long each 60 Hz frame of the leaving takes to make at 1x.
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	unfollowFromMenu(t, tt, letGoWriter(a).src.DisplayName)
	var took []time.Duration
	for range 2300 * 60 / 1000 {
		start := time.Now()
		tt.Advance(time.Second / 60)
		took = append(took, time.Since(start))
	}
	slices.Sort(took)
	os.WriteFile(filepath.Join(dir, "frame-times.txt"), []byte(fmt.Sprintf("leave, 1x, %d frames: median %v p90 %v max %v\n",
		len(took), took[len(took)/2], took[len(took)*9/10], took[len(took)-1])), 0o644)
}

// cropTo copies r of m into an image of its own.
func cropTo(m *image.RGBA, r image.Rectangle) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), m, r.Min, draw.Src)
	return out
}
