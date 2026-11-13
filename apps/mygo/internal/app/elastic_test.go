package app

import (
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/theme"
)

// TestElasticFrames writes the press and the sliding ground frame by frame,
// cropped and at scale 2: ELASTIC=dir go test -run ElasticFrames.
func TestElasticFrames(t *testing.T) {
	dir := os.Getenv("ELASTIC")
	if dir == "" {
		t.Skip("ELASTIC not set")
	}
	os.MkdirAll(dir, 0o755)
	const step = time.Second / 60
	crop := func(tt *ui.Tester, r ui.Rect) *image.RGBA {
		m := tt.Image()
		s := float32(2)
		rect := image.Rect(int(r.X*s), int(r.Y*s), int((r.X+r.W)*s), int((r.Y+r.H)*s))
		out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
		draw.Draw(out, out.Bounds(), m, rect.Min, draw.Src)
		return out
	}
	grow := func(r ui.Rect, by float32) ui.Rect {
		return ui.Rect{X: r.X - by, Y: r.Y - by, W: r.W + 2*by, H: r.H + 2*by}
	}
	newTT := func() (*App, *ui.Tester) {
		a, _ := newTestApp(t)
		tt := ui.NewTester(a.View, 1280, 850)
		tt.SetScale(2)
		settle(tt)
		return a, tt
	}
	film := func(tt *ui.Tester, r ui.Rect, span time.Duration) []*image.RGBA {
		var frames []*image.RGBA
		for d := time.Duration(0); d <= span; d += step {
			frames = append(frames, crop(tt, r))
			tt.Advance(step)
		}
		return frames
	}
	write := func(name string, frames []*image.RGBA) {
		writeSheet(t, filepath.Join(dir, name+".png"), frames)
		writeGIF(t, filepath.Join(dir, name+".gif"), frames, step)
	}

	// A press held 300 ms and let go, on the add button and on a tab.
	for _, target := range []string{"订阅博客…", "收藏"} {
		_, tt := newTT()
		r, ok := tt.Find(target)
		if !ok {
			t.Fatalf("no %s", target)
		}
		x, y := r.X+r.W/2, r.Y+r.H/2
		tt.Move(x, y)
		tt.Advance(time.Second)
		area := grow(r, 10)
		tt.Press(x, y)
		frames := film(tt, area, 300*time.Millisecond)
		tt.Release(x+400, y) // away from it: let go without clicking
		frames = append(frames, film(tt, area, 600*time.Millisecond)...)
		write("press-"+target, frames)
	}

	// The tabs' ground sliding from 首页 to 摘录.
	{
		_, tt := newTT()
		home, _ := tt.Find("首页")
		area := ui.Rect{X: home.X - 12, Y: home.Y - 10, W: 220, H: home.H + 20}
		if err := tt.Click("摘录"); err != nil {
			t.Fatal(err)
		}
		tt.Move(640, 800)
		write("slide-tabs", film(tt, area, 500*time.Millisecond))
	}

	// The authors' ground sliding down the sidebar, and back up.
	{
		_, tt := newTT()
		names := []string{}
		for _, au := range []string{"Innei", "Anthony Fu", "Tw93", "Paco Coursey", "Rauno Freiberg"} {
			if _, ok := tt.Find(au); ok {
				names = append(names, au)
			}
		}
		if len(names) < 2 {
			t.Fatalf("authors found: %v (texts %v)", names, tt.Texts())
		}
		first, _ := tt.Find(names[0])
		last, _ := tt.Find(names[len(names)-1])
		if err := tt.Click(names[0]); err != nil {
			t.Fatal(err)
		}
		tt.Move(640, 800)
		tt.Advance(2 * time.Second)
		top, bottom := min(first.Y, last.Y), max(first.Y+first.H, last.Y+last.H)
		area := ui.Rect{X: 0, Y: top - 16, W: 232, H: bottom - top + 32}
		if err := tt.Click(names[len(names)-1]); err != nil {
			t.Fatal(err)
		}
		tt.Move(640, 800)
		write("slide-authors", film(tt, area, 500*time.Millisecond))
	}
}

// TestElasticQuiet: from the keys and under less motion the tabs' ground
// does not slide: it is in place within a frame or two.
func TestElasticQuiet(t *testing.T) {
	for _, mode := range []string{"keys", "reduce"} {
		a, _ := newTestApp(t)
		tt := ui.NewTester(a.View, 1280, 850)
		settle(tt)
		home, _ := tt.Find("首页")
		area := image.Rect(int(home.X)-12, int(home.Y)-10, int(home.X)+208, int(home.Y+home.H)+10)
		if mode == "reduce" {
			tt.SetPreferences(ui.Preferences{ReduceMotion: true})
			tt.Advance(time.Second)
			if err := tt.Click("摘录"); err != nil {
				t.Fatal(err)
			}
			tt.Move(640, 800)
		} else {
			a.kbd = true // as the window's input observer marks it; the tester's shortcut skips it
			tt.Key(ui.Cmd, ui.Key4)
		}
		// The ground reads where the frame before laid the tab out: one
		// frame behind, in place from the second.
		tt.Advance(3 * time.Second / 60)
		early := tt.Image().SubImage(area).(*image.RGBA)
		early = cloneRGBA(early)
		tt.Advance(2 * time.Second)
		late := tt.Image().SubImage(area).(*image.RGBA)
		// The label opens over its own 220 ms; the ground's left edge is
		// already where it ends: compare the column just left of 摘录's icon.
		clips, _ := tt.Find("摘录")
		x := int(clips.X) - 30
		for y := area.Min.Y; y < area.Max.Y; y++ {
			if early.RGBAAt(x, y) != late.RGBAAt(x, y) {
				t.Errorf("%s: the ground moved after the first frame at (%d, %d)", mode, x, y)
				break
			}
		}
	}
}

func cloneRGBA(m *image.RGBA) *image.RGBA {
	out := image.NewRGBA(m.Bounds())
	draw.Draw(out, out.Bounds(), m, m.Bounds().Min, draw.Src)
	return out
}

// TestElasticMore writes the second round frame by frame, cropped and at
// scale 2 (the whole window, halved, for the list and its notice):
// ELASTIC=dir go test -run ElasticMore.
func TestElasticMore(t *testing.T) {
	dir := os.Getenv("ELASTIC")
	if dir == "" {
		t.Skip("ELASTIC not set")
	}
	os.MkdirAll(dir, 0o755)
	const step = time.Second / 60
	crop := func(tt *ui.Tester, r ui.Rect, s float32) *image.RGBA {
		m := tt.Image()
		rect := image.Rect(int(r.X*s), int(r.Y*s), int((r.X+r.W)*s), int((r.Y+r.H)*s))
		out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
		draw.Draw(out, out.Bounds(), m, rect.Min, draw.Src)
		return out
	}
	film := func(tt *ui.Tester, r ui.Rect, s float32, span time.Duration) []*image.RGBA {
		var frames []*image.RGBA
		for d := time.Duration(0); d <= span; d += step {
			if s == 0 {
				frames = append(frames, halve(tt.Image()))
			} else {
				frames = append(frames, crop(tt, r, s))
			}
			tt.Advance(step)
		}
		return frames
	}
	write := func(name string, frames []*image.RGBA) {
		writeSheet(t, filepath.Join(dir, name+".png"), frames)
		writeGIF(t, filepath.Join(dir, name+".gif"), frames, step)
	}
	around := func(rs ...ui.Rect) ui.Rect {
		r := rs[0]
		for _, o := range rs[1:] {
			x0, y0 := min(r.X, o.X), min(r.Y, o.Y)
			r = ui.Rect{X: x0, Y: y0, W: max(r.X+r.W, o.X+o.W) - x0, H: max(r.Y+r.H, o.Y+o.H) - y0}
		}
		return ui.Rect{X: r.X - 8, Y: r.Y - 8, W: r.W + 16, H: r.H + 16}
	}

	// 字号 up a step, and the reader's favourite kept.
	{
		_, tt, _ := openOptions(t)
		tt.SetScale(2)
		settle(tt)
		plus, _ := tt.Access("放大字号")
		minus, _ := tt.Access("缩小字号")
		tt.ClickAt(center(plus.Bounds))
		write("count", film(tt, around(minus.Bounds, plus.Bounds), 2, 500*time.Millisecond))
	}
	{
		_, tt, _ := openLobehub(t)
		tt.SetScale(2)
		settle(tt)
		fav, ok := tt.AccessByRole(ui.RoleToggleButton, "收藏")
		if !ok {
			fav, ok = tt.AccessByRole(ui.RoleToggleButton, "取消收藏")
		}
		if !ok {
			t.Fatal("no favourite button")
		}
		tt.ClickAt(center(fav.Bounds))
		tt.Move(640, 800)
		write("orbit", film(tt, around(fav.Bounds), 2, 800*time.Millisecond))
	}
	// The viewer's round 关闭 pressed and let go away from it.
	{
		_, tt, rp := openLobehub(t)
		box := showPicture(t, tt, rp, lobePic)
		tt.ClickAt(center(box))
		settle(tt)
		tt.SetScale(2)
		settle(tt)
		cl, ok := tt.Access("关闭")
		if !ok {
			t.Fatal("no 关闭")
		}
		x, y := center(cl.Bounds)
		tt.Move(x, y)
		tt.Advance(time.Second)
		tt.Press(x, y)
		frames := film(tt, around(cl.Bounds), 2, 250*time.Millisecond)
		tt.Release(x+300, y)
		write("round", append(frames, film(tt, around(cl.Bounds), 2, 550*time.Millisecond)...))
	}
	// The ··· on a favourite's card pressed and let go away from it; the
	// card let go, the rest closing up and the notice rising; then put back.
	{
		a, _ := newSeededApp(t)
		tt := ui.NewTester(a.View, 1280, 850)
		settle(tt)
		must(t, tt.Click("收藏"))
		settle(tt)
		must(t, tt.Click(antfuShelf))
		settle(tt)
		card, _ := tt.Find(ecoTitle)
		tt.Move(center(card))
		tt.Advance(time.Second)
		more, ok := tt.Access("「" + ecoTitle + "」的收藏操作")
		if !ok {
			t.Fatal("no ···")
		}
		tt.SetScale(2)
		settle(tt)
		x, y := center(more.Bounds)
		tt.Move(x, y)
		tt.Advance(time.Second)
		tt.ClickAt(x, y) // its menu opens: the ··· squares up
		write("more", film(tt, around(more.Bounds), 2, 600*time.Millisecond))
		tt.SetScale(1)
		settle(tt)
		must(t, tt.Click("取消收藏"))
		tt.Move(640, 10)
		write("letgo", film(tt, ui.Rect{}, 0, 900*time.Millisecond))
		tt.Key(ui.Cmd, ui.KeyZ)
		write("putback", film(tt, ui.Rect{}, 0, 700*time.Millisecond))
	}
}

// An icon's accent moves while its button is pressed, but not from the
// keys, nor under less motion.
func TestIconNudgeQuiet(t *testing.T) {
	for _, mode := range []string{"pointer", "keys", "reduce"} {
		a := &App{kbd: mode == "keys"}
		pressed := false
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Size(64, 64).Background(ui.RGB(0, 0, 0)).Children(func() {
				a.pressIcon(c, "play", 48, true, pressed)
			})
		}, 64, 64)
		if mode == "reduce" {
			tt.SetPreferences(ui.Preferences{ReduceMotion: true})
		}
		tt.Frame()
		tt.Advance(time.Second)
		rest := cloneRGBA(tt.Image())
		pressed = true
		tt.Frame()
		tt.Advance(400 * time.Millisecond)
		moved := false
		for i := range rest.Pix {
			if rest.Pix[i] != tt.Image().Pix[i] {
				moved = true
				break
			}
		}
		if moved != (mode == "pointer") {
			t.Errorf("%s: the accent moved %v", mode, moved)
		}
	}
}

// TestElasticIcons writes each icon's press and let-go frame by frame, at
// scale 3: ELASTIC=dir go test -run ElasticIcons.
func TestElasticIcons(t *testing.T) {
	dir := os.Getenv("ELASTIC")
	if dir == "" {
		t.Skip("ELASTIC not set")
	}
	os.MkdirAll(dir, 0o755)
	const step, s = time.Second / 60, 3
	for name := range nudges {
		a := &App{}
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Row(c).Padding(20).Background(theme.Window).Children(func() {
				a.iconBtn(c, name, iconOpt{icon: name, label: name})
			})
		}, 72, 72)
		tt.SetScale(s)
		settle(tt)
		r, ok := tt.Access(name)
		if !ok {
			t.Fatalf("no %s", name)
		}
		x, y := center(r.Bounds)
		tt.Move(x, y)
		tt.Advance(time.Second)
		var frames []*image.RGBA
		film := func(span time.Duration) {
			for d := time.Duration(0); d <= span; d += step {
				frames = append(frames, cloneRGBA(tt.Image()))
				tt.Advance(step)
			}
		}
		tt.Press(x, y)
		film(300 * time.Millisecond)
		tt.Release(x+200, y) // away from it: let go without clicking
		film(800 * time.Millisecond)
		writeSheet(t, filepath.Join(dir, name+".png"), frames)
		writeGIF(t, filepath.Join(dir, name+".gif"), frames, step)
	}
}
