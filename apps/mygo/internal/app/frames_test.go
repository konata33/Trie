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
