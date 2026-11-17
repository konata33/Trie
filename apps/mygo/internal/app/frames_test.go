package app

import (
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/locales"
)

// TestFrames writes each crossing frame by frame: a contact sheet of its frames every 40 ms and a GIF playing them in real
// time, and how long the frames took to make in memory:
// FRAMES=dir go test -run Frames.
func TestFrames(t *testing.T) {
	dir := os.Getenv("FRAMES")
	if dir == "" {
		t.Skip("FRAMES not set")
	}
	os.MkdirAll(dir, 0o755)
	const step = 40 * time.Millisecond
	click := func(s string) func(*App, *ui.Tester) {
		return func(a *App, tt *ui.Tester) {
			if err := tt.Click(s); err != nil {
				t.Fatal(err)
			}
		}
	}
	at := func(x, y float32) func(*App, *ui.Tester) { return func(a *App, tt *ui.Tester) { tt.ClickAt(x, y) } }
	// set brings the window there without motion, as keys would.
	set := func(steps ...func(*App, *ui.Tester)) func(*App, *ui.Tester) {
		return func(a *App, tt *ui.Tester) {
			for _, s := range steps {
				s(a, tt)
				tt.Advance(2 * time.Second)
			}
			tt.Move(640, 800) // the pointer rests away from what it pressed
			tt.Advance(time.Second)
		}
	}
	scenarios := []struct {
		name     string
		set, act func(*App, *ui.Tester)
		span     time.Duration
	}{
		{"1-open-author", set(), click("Innei"), 880 * time.Millisecond},
		{"2-open-article", set(click("Innei")), click(lobeTitle), 960 * time.Millisecond},
		{"3-back-article", set(click("Innei"), click(lobeTitle)), at(460, 30), 760 * time.Millisecond},
		{"4-side-tab", set(click("Innei")), click("收藏"), 560 * time.Millisecond},
		{"5-dialog-open", set(click("Innei")), click("订阅博客…"), 480 * time.Millisecond},
	}
	var times strings.Builder
	fmt.Fprintf(&times, "Frames made in memory by ui.Tester at 1280×850, scale 1 (CPU renderer: build, layout and paint; no GPU), one per 60 Hz refresh from the click.\n\n")
	for _, s := range scenarios {
		a, _ := newTestApp(t)
		tt := ui.NewTester(a.View, 1280, 850)
		settle(tt)
		s.set(a, tt)
		s.act(a, tt)
		var frames []*image.RGBA
		t0 := tt.Now()
		for d := time.Duration(0); d <= s.span; d += step {
			tt.Advance(d - tt.Now().Sub(t0))
			frames = append(frames, halve(tt.Image()))
		}
		writeSheet(t, filepath.Join(dir, s.name+".png"), frames)
		writeGIF(t, filepath.Join(dir, s.name+".gif"), frames, step)
		// The same again, a refresh at a time, timed.
		a, _ = newTestApp(t)
		tt = ui.NewTester(a.View, 1280, 850)
		settle(tt)
		s.set(a, tt)
		s.act(a, tt)
		var took []time.Duration
		for range int(s.span/(time.Second/60)) + 1 {
			start := time.Now()
			tt.Advance(time.Second / 60)
			took = append(took, time.Since(start))
		}
		slices.Sort(took)
		ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
		fmt.Fprintf(&times, "%-18s %3d frames  median %5.2f ms  p90 %5.2f ms  max %5.2f ms\n", s.name, len(took),
			ms(took[len(took)/2]), ms(took[len(took)*9/10]), ms(took[len(took)-1]))
	}
	os.WriteFile(filepath.Join(dir, "frame-times.txt"), []byte(times.String()), 0o644)
}

// TestLayerFrames writes the layers' motion frame by frame, as TestFrames
// does: the dialog and the settings growing out of what opened them and
// back, a passage taking the stage and going back, the pointer's ground
// gliding in a menu and down the sidebar, a notice changing into another:
// FRAMES=dir go test -run LayerFrames.
func TestLayerFrames(t *testing.T) {
	dir := os.Getenv("FRAMES")
	if dir == "" {
		t.Skip("FRAMES not set")
	}
	os.MkdirAll(dir, 0o755)
	const step = 20 * time.Millisecond
	type step_ = func(*App, *ui.Tester)
	click := func(s string) step_ {
		return func(_ *App, tt *ui.Tester) { must(t, tt.Click(s)) }
	}
	over := func(s string) step_ {
		return func(_ *App, tt *ui.Tester) {
			r, ok := tt.Find(s)
			if !ok {
				t.Fatalf("no %q", s)
			}
			tt.Move(r.X+r.W/2, r.Y+r.H/2)
		}
	}
	at := func(x, y float32) step_ { return func(_ *App, tt *ui.Tester) { tt.ClickAt(x, y) } }
	notice := func(text string, undo bool) step_ {
		return func(a *App, tt *ui.Tester) {
			var u library.LibraryAction
			if undo {
				u = library.SetSubscription{ID: "x", Subscribed: true}
			}
			a.notify(text, nil, u)
			tt.Frame()
		}
	}
	menu := locales.SidebarWriterActions("Innei")
	scenarios := []struct {
		name   string
		seeded bool
		set    []step_
		act    step_
		span   time.Duration
	}{
		{"l1-dialog-open", false, nil, click(locales.SidebarFollowBlog()), 480 * time.Millisecond},
		{"l2-dialog-close", false, []step_{click(locales.SidebarFollowBlog())}, click(locales.CommonCancel()), 700 * time.Millisecond},
		{"l3-sheet-open", false, nil, click(locales.Settings()), 320 * time.Millisecond},
		{"l4-sheet-close", false, []step_{click(locales.Settings())}, at(200, 120), 500 * time.Millisecond},
		{"l5-menu-glide", false, []step_{click(menu), over(locales.SidebarRename())}, over(locales.CommonUnfollow()), 260 * time.Millisecond},
		{"l6-side-glide", false, []step_{over("Innei")}, over("Nielsen Norman Group"), 260 * time.Millisecond},
		{"l7-clip-open", true, []step_{click("摘录")}, click(leadText), 480 * time.Millisecond},
		{"l8-clip-close", true, []step_{click("摘录"), click(leadText)}, at(1250, 830), 700 * time.Millisecond},
		{"l9-clip-article", true, []step_{click("摘录"), click(leadText)}, func(_ *App, tt *ui.Tester) { tt.ClickAt(cardCite(t, tt)) }, 640 * time.Millisecond},
		{"l10-notice-change", false, []step_{notice("已复制", false)}, notice(locales.NoticeUnfollowed("Nielsen Norman Group"), true), 400 * time.Millisecond},
	}
	for _, s := range scenarios {
		a := (*App)(nil)
		if s.seeded {
			a, _ = newSeededApp(t)
		} else {
			a, _ = newTestApp(t)
		}
		tt := ui.NewTester(a.View, 1280, 850)
		settle(tt)
		for _, f := range s.set {
			f(a, tt)
			tt.Advance(2 * time.Second)
		}
		s.act(a, tt)
		var frames []*image.RGBA
		t0 := tt.Now()
		for d := time.Duration(0); d <= s.span; d += step {
			tt.Advance(d - tt.Now().Sub(t0))
			frames = append(frames, halve(tt.Image()))
		}
		writeSheet(t, filepath.Join(dir, s.name+".png"), frames)
		writeGIF(t, filepath.Join(dir, s.name+".gif"), frames, step)
	}
}

// TestGrowSurface checks the surface of a layer the pointer opens: it comes
// out of the button and hides under the layer once there, and is gone as the
// layer closes, which goes back as one piece; the keys and less motion open
// the layer without one.
func TestGrowSurface(t *testing.T) {
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	if err := tt.Click(locales.SidebarFollowBlog()); err != nil {
		t.Fatal(err)
	}
	tt.Advance(100 * time.Millisecond)
	g := a.layers.grows["dialog"]
	if g == nil || g.home || g.at.W <= g.from.W || g.at.W >= g.to.W {
		t.Fatalf("100 ms in, the surface should be on its way: %+v", g)
	}
	tt.Advance(time.Second)
	if !g.home {
		t.Fatal("the surface should hide under the dialog once it is there")
	}
	if err := tt.Click(locales.CommonCancel()); err != nil {
		t.Fatal(err)
	}
	tt.Advance(20 * time.Millisecond)
	if a.layers.grows["dialog"] != nil {
		t.Fatal("closing, the dialog should go back as one piece, without its surface")
	}
	tt.Advance(time.Second)

	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Advance(100 * time.Millisecond)
	if !a.layers.sheet || a.layers.grows["sheet"] != nil {
		t.Fatalf("⌘, should open the settings in place: open %v, surface %v", a.layers.sheet, a.layers.grows["sheet"])
	}
	tt.Key(0, ui.KeyEscape)
	tt.Advance(time.Second)

	a.Reduce = true
	tt.Move(640, 400)
	if err := tt.Click(locales.Settings()); err != nil {
		t.Fatal(err)
	}
	tt.Advance(50 * time.Millisecond)
	if !a.layers.sheet || a.layers.grows["sheet"] != nil {
		t.Fatal("under less motion the settings should come into focus in place")
	}

	// A passage on the stage: pressing its article's title readies the stage to let go with its page
	// rather than go back into the passage.
	a, _ = newSeededApp(t)
	tt = ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("摘录"))
	settle(tt)
	must(t, tt.Click(leadText))
	tt.Advance(time.Second)
	s := a.clips.stage
	if s == nil || !s.open || !s.built {
		t.Fatalf("the passage should be on the stage: %+v", s)
	}
	x, y := cardCite(t, tt)
	tt.Press(x, y)
	tt.Advance(50 * time.Millisecond)
	if !s.leave {
		t.Fatal("pressed, the article's title should ready the stage to leave with its page")
	}
	tt.Release(x, y)
	tt.Advance(50 * time.Millisecond)
	if a.page.kind != "reader" || a.clips.stage != nil {
		t.Fatalf("the article should open, the stage gone: page %v", a.page)
	}
}

// halve scales m down by two, each pixel the mean of four.
func halve(m *image.RGBA) *image.RGBA {
	b := m.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx()/2, b.Dy()/2))
	for y := 0; y < b.Dy()/2; y++ {
		for x := 0; x < b.Dx()/2; x++ {
			var sum [4]int
			for _, d := range [4][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
				i := m.PixOffset(b.Min.X+2*x+d[0], b.Min.Y+2*y+d[1])
				for k := range sum {
					sum[k] += int(m.Pix[i+k])
				}
			}
			o := out.PixOffset(x, y)
			for k := range sum {
				out.Pix[o+k] = uint8(sum[k] / 4)
			}
		}
	}
	return out
}

// writeSheet lays frames out four to a row.
func writeSheet(t *testing.T, path string, frames []*image.RGBA) {
	w, h := frames[0].Bounds().Dx(), frames[0].Bounds().Dy()
	const cols, gap = 4, 6
	rows := (len(frames) + cols - 1) / cols
	out := image.NewRGBA(image.Rect(0, 0, cols*w+(cols-1)*gap, rows*h+(rows-1)*gap))
	draw.Draw(out, out.Bounds(), image.NewUniform(color.RGBA{0x30, 0x30, 0x30, 0xff}), image.Point{}, draw.Src)
	for i, f := range frames {
		at := image.Pt((i%cols)*(w+gap), (i/cols)*(h+gap))
		draw.Draw(out, f.Bounds().Add(at), f, image.Point{}, draw.Src)
	}
	savePNG(t, path, out)
}

// writeGIF plays frames in real time, holding the first and the last.
func writeGIF(t *testing.T, path string, frames []*image.RGBA, step time.Duration) {
	pal := append(color.Palette{}, palette.WebSafe...)
	for i := 0; len(pal) < 256; i++ {
		v := uint8(0x0c + i*2)
		pal = append(pal, color.RGBA{v, v, v, 0xff})
	}
	g := &gif.GIF{}
	for i, f := range frames {
		p := image.NewPaletted(f.Bounds(), pal)
		draw.FloydSteinberg.Draw(p, f.Bounds(), f, image.Point{})
		delay := int(step / (10 * time.Millisecond))
		if i == 0 || i == len(frames)-1 {
			delay = 80
		}
		g.Image, g.Delay = append(g.Image, p), append(g.Delay, delay)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := gif.EncodeAll(f, g); err != nil {
		t.Fatal(err)
	}
}
