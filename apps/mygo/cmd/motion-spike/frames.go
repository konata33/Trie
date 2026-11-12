package main

import (
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// step is how far apart the frames of a contact sheet are.
const step = 40 * time.Millisecond

// A scenario is one crossing looked at frame by frame: set brings a new app
// to where it starts, without motion; act starts the crossing; frames are
// taken every step from then until span.
type scenario struct {
	name string
	set  func(a *app, tt *ui.Tester)
	act  func(a *app, tt *ui.Tester)
	span time.Duration
}

// pick chooses a Chinese title that wraps differently in its row and as
// the headline, and an author who writes in Chinese, where the fixture has
// them.
func pick(arts []*article) (first *article, au string) {
	first, au = arts[0], arts[0].author
	for _, art := range arts {
		if strings.HasPrefix(art.title, "十个月") {
			first = art
		}
		if art.author == "Innei" {
			au = art.author
		}
	}
	return first, au
}

func scenarios(arts []*article) []scenario {
	first, au := pick(arts)
	at := func(name string) func(*app, *ui.Tester) {
		return func(a *app, tt *ui.Tester) { a.auto = name; tt.Frame() }
	}
	click := func(s string) func(*app, *ui.Tester) {
		return func(a *app, tt *ui.Tester) {
			if err := tt.Click(s); err != nil {
				log.Fatalf("motion-spike: %v", err)
			}
		}
	}
	// settled runs set's actions without motion, as keys would.
	settled := func(names ...string) func(*app, *ui.Tester) {
		return func(a *app, tt *ui.Tester) {
			for _, n := range names {
				a.kbd, a.auto = true, n
				tt.Frame()
				tt.Advance(2 * time.Second)
			}
			a.kbd = false
			tt.Frame()
		}
	}
	none := func(*app, *ui.Tester) {}
	return []scenario{
		{"1-side-tab", none, click("探索"), 560 * time.Millisecond},
		{"2-deep-article", none, at("open:" + first.id), 960 * time.Millisecond},
		{"3-back-article", settled("open:" + first.id), at("back"), 760 * time.Millisecond},
		{"4-deep-author", none, at("author:" + au), 880 * time.Millisecond},
		{"5-back-author", settled("author:" + au), at("back"), 680 * time.Millisecond},
		{"6-dialog-open", none, click("对话框"), 480 * time.Millisecond},
		{"7-dialog-close", settled("dialog"), click("取消"), 320 * time.Millisecond},
		{"8-sheet-open", none, click("快捷键"), 320 * time.Millisecond},
		{"9-menu-open", none, click("···"), 200 * time.Millisecond},
		{"9-menu-close", func(a *app, tt *ui.Tester) { click("···")(a, tt); tt.Advance(time.Second) }, func(a *app, tt *ui.Tester) {
			a.menu = false
			tt.Frame()
		}, 120 * time.Millisecond},
		{"10-popover-open", none, click("Aa"), 320 * time.Millisecond},
		{"11-toast-in", none, click("通知"), 360 * time.Millisecond},
		{"12-reduce-deep-article", func(a *app, tt *ui.Tester) { a.reduce = true; tt.Frame(); tt.Advance(time.Second) }, at("open:" + first.id), 880 * time.Millisecond},
		{"13-reduce-back-article", func(a *app, tt *ui.Tester) {
			a.reduce = true
			settled("open:"+first.id)(a, tt)
		}, at("back"), 760 * time.Millisecond},
		{"14-keyboard-deep-article", none, func(a *app, tt *ui.Tester) {
			for i := 0; i < 40 && !tt.Focused(first.title); i++ {
				tt.Key(0, ui.KeyTab)
			}
			tt.Key(0, ui.KeyEnter)
		}, 120 * time.Millisecond},
	}
}

// writeFrames plays each scenario in a tester, whose clock moves only as
// it is told, and writes a contact sheet of its frames every 40 ms (each
// frame labelled with its time) and a GIF playing them in real time; then
// it plays each again a refresh at a time and writes how long the frames
// took to make in memory (frame-times.txt).
func writeFrames(arts []*article, dir, only string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var times strings.Builder
	fmt.Fprintf(&times, "Frames made in memory by ui.Tester at 1280×820, scale 1 (CPU renderer: build, layout and paint; no GPU, no present), one per 60 Hz refresh from the click.\n\n")
	for _, s := range scenarios(arts) {
		if !strings.HasPrefix(s.name, only) {
			continue
		}
		a, tt := start(arts, s)
		var frames []*image.RGBA
		t0 := tt.Now()
		for at := time.Duration(0); at <= s.span; at += step {
			tt.Advance(at - tt.Now().Sub(t0))
			a.label = fmt.Sprintf("%s  %d ms", s.name, at.Milliseconds())
			tt.Frame()
			frames = append(frames, half(tt.Image()))
		}
		if err := sheet(filepath.Join(dir, s.name+".png"), frames); err != nil {
			return err
		}
		if err := movie(filepath.Join(dir, s.name+".gif"), frames); err != nil {
			return err
		}
		// The same again, a refresh at a time, timed.
		a, tt = start(arts, s)
		var took []time.Duration
		for i := 0; i < int(s.span/(time.Second/60))+1; i++ {
			t := time.Now()
			tt.Advance(time.Second / 60)
			took = append(took, time.Since(t))
		}
		slices.Sort(took)
		fmt.Fprintf(&times, "%-26s %3d frames  median %5.2f ms  p90 %5.2f ms  max %5.2f ms\n", s.name, len(took),
			ms(took[len(took)/2]), ms(took[len(took)*9/10]), ms(took[len(took)-1]))
		fmt.Printf("%s: %d frames\n", s.name, len(frames))
	}
	return os.WriteFile(filepath.Join(dir, "frame-times.txt"), []byte(times.String()), 0o644)
}

// start makes an app in a tester, sets the scenario up and acts.
func start(arts []*article, s scenario) (*app, *ui.Tester) {
	a := newApp(arts)
	tt := ui.NewTester(a.view, 1280, 820)
	s.set(a, tt)
	s.act(a, tt)
	return a, tt
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// half scales m down by two, each pixel the mean of four.
func half(m *image.RGBA) *image.RGBA {
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

// sheet lays frames out four to a row, left to right, top to bottom.
func sheet(path string, frames []*image.RGBA) error {
	w, h := frames[0].Bounds().Dx(), frames[0].Bounds().Dy()
	const cols, gap = 4, 6
	rows := (len(frames) + cols - 1) / cols
	out := image.NewRGBA(image.Rect(0, 0, cols*w+(cols-1)*gap, rows*h+(rows-1)*gap))
	draw.Draw(out, out.Bounds(), image.NewUniform(color.RGBA{0x30, 0x30, 0x30, 0xff}), image.Point{}, draw.Src)
	for i, f := range frames {
		at := image.Pt((i%cols)*(w+gap), (i/cols)*(h+gap))
		draw.Draw(out, f.Bounds().Add(at), f, image.Point{}, draw.Src)
	}
	return save(path, func(f *os.File) error { return png.Encode(f, out) })
}

// movie writes frames as a GIF playing in real time, holding the last.
func movie(path string, frames []*image.RGBA) error {
	// Web-safe colours for the accent, and grays for the dark interface.
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
	return save(path, func(f *os.File) error { return gif.EncodeAll(f, g) })
}

func save(path string, write func(*os.File) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// play presses through every crossing once in a real window, so that
// MYGO_FRAME_STATS=all logs how long its frames take on the GPU; then the
// app quits.
func play(a *app, win *mygo.Window) {
	first, au := pick(a.arts)
	do := func(fn func()) { win.Update(fn) }
	press := func(name string) func() { return func() { a.auto = name } }
	steps := []struct {
		name string
		fn   func()
		wait time.Duration
	}{
		{"side", press("tab:explore"), 900}, {"side", press("tab:home"), 900},
		{"deep-article", press("open:" + first.id), 1300}, {"back-article", press("back"), 1100},
		{"deep-author", press("author:" + au), 1300}, {"back-author", press("back"), 1100},
		{"dialog-open", press("dialog"), 900}, {"dialog-close", press("dialog-close"), 700},
		{"sheet-open", press("sheet"), 700}, {"sheet-close", press("sheet-close"), 600},
		{"menu-open", press("menu"), 500}, {"menu-close", func() { a.menu = false }, 500},
		{"popover-open", press("pop"), 600}, {"popover-close", func() { a.pop = false }, 500},
		{"toast", press("toast"), 900},
	}
	time.Sleep(1500 * time.Millisecond) // the first frames
	for _, s := range steps {
		log.Printf("motion-spike: %s", s.name)
		do(s.fn)
		time.Sleep(s.wait * time.Millisecond)
	}
	log.Print("motion-spike: done")
	mygo.App.Quit()
}
