package app

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/motion"
	"trie/internal/reader"
)

const (
	lobePic   = "查看大图：style-equivalence 报告总览"
	lobeAlt   = "style-equivalence 报告总览"
	asyncPost = "https://antfu.me/posts/async-sync-in-between"
)

// showPicture scrolls the reader until the picture's button is in the
// middle of the window, and returns its box.
func showPicture(t *testing.T, tt *ui.Tester, rp *readerPage, name string) ui.Rect {
	t.Helper()
	for range 3 {
		b, ok := tt.Access(name)
		if !ok {
			t.Fatalf("no %q", name)
		}
		if b.Bounds.Y > 150 && b.Bounds.Y+b.Bounds.H < 800 {
			return b.Bounds
		}
		rp.r.Scroll.Y = max(0, rp.r.Scroll.Y+b.Bounds.Y-200)
		tt.Frame()
		settle(tt)
	}
	b, _ := tt.Access(name)
	return b.Bounds
}

func center(r ui.Rect) (float32, float32) { return r.X + r.W/2, r.Y + r.H/2 }

// The picture viewer: a picture of the body clicked flies out
// of its place, which stays empty, into the middle; clicked beside, it
// flies back and its place shows it again as it lands, the focus back on
// it. From the keys it is simply there and gone; under less motion it fades
// in where it shows.
func TestPictureViewer(t *testing.T) {
	a, tt, rp := openLobehub(t)
	box := showPicture(t, tt, rp, lobePic)
	shown := func() ui.Rect {
		acc, ok := tt.Access(lobeAlt)
		if !ok {
			return ui.Rect{}
		}
		return acc.Bounds
	}
	v := &a.viewer
	tt.ClickAt(center(box))
	if !v.open || !v.travel || rp.r.Hide == 0 {
		t.Fatalf("clicked: open %v, travels %v, its place empty %v", v.open, v.travel, rp.r.Hide != 0)
	}
	if p := shown(); abs32(p.X-box.X) > 1 || abs32(p.W-box.W) > 1 {
		t.Errorf("it starts where the page showed it: %v, %v", p, box)
	}
	if _, ok := tt.AccessByRole(ui.RoleDialog, "图片预览"); !ok || !tt.Focused("关闭") {
		t.Errorf("a dialog, the focus on 关闭: %v, %v", ok, tt.Focused("关闭"))
	}
	tt.Advance(120 * time.Millisecond)
	mid := shown()
	tt.Advance(time.Second)
	end := shown()
	if !(mid.W > box.W+50 && mid.W < end.W-20) || end.W > 1120.5 || end.X < 79.5 || end.Y < 63.5-5 {
		t.Errorf("it flies into the middle, within 80 a side and 64 above: %v, then %v", mid, end)
	}
	// Beside the picture: it flies back, its place empty until it lands.
	tt.ClickAt(40, 425)
	tt.Advance(100 * time.Millisecond)
	if v.open || rp.r.Hide == 0 {
		t.Errorf("flying back: open %v, its place empty %v", v.open, rp.r.Hide != 0)
	}
	tt.Advance(250 * time.Millisecond)
	if rp.r.Hide != 0 || !tt.Focused(lobePic) {
		t.Errorf("landed: its place shows it %v, the focus on it %v", rp.r.Hide == 0, tt.Focused(lobePic))
	}
	// From the keys: Enter shows it at once in the middle, Esc takes it away at once.
	tt.Key(0, ui.KeyEnter)
	tt.Advance(16 * time.Millisecond)
	if !v.open || v.travel || !v.instant {
		t.Fatalf("Enter: open %v, travels %v, at once %v", v.open, v.travel, v.instant)
	}
	if p := shown(); abs32(p.W-end.W) > 1 {
		t.Errorf("from the keys it is in the middle at once: %v", p)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Advance(16 * time.Millisecond)
	if v.open || rp.r.Hide != 0 || !tt.Focused(lobePic) {
		t.Errorf("Esc: open %v, its place shows it %v, the focus on it %v", v.open, rp.r.Hide == 0, tt.Focused(lobePic))
	}
	// Under less motion nothing travels: it comes into focus where it shows.
	a.Reduce = true
	tt.Move(center(box))
	tt.Advance(time.Second)
	tt.ClickAt(center(box))
	if !v.open || v.travel {
		t.Fatalf("less motion: open %v, travels %v", v.open, v.travel)
	}
	if p := shown(); abs32(p.W-end.W) > 1 {
		t.Errorf("less motion: it shows in the middle, fading in: %v", p)
	}
	tt.ClickAt(40, 425)
	if v.until.Sub(tt.Now()) != motion.DialogOut {
		t.Errorf("less motion: it fades out on the dialog's timing, %v", v.until.Sub(tt.Now()))
	}
}

// openOptions opens the reading options and reads from the middle of the
// article.
func openOptions(t *testing.T) (*App, *ui.Tester, *readerPage) {
	t.Helper()
	a, tt, rp := openLobehub(t)
	must(t, tt.Click("阅读选项"))
	settle(tt)
	rp.r.Scroll.Y = 4000
	tt.Frame()
	settle(tt)
	return a, tt, rp
}

// samePlace reports whether the reader reads the same unit at the guide,
// as far into it as its new size puts it.
func samePlace(t *testing.T, before, after reader.Position, k float32) {
	t.Helper()
	if before.UnitID == nil || after.UnitID == nil || *before.UnitID != *after.UnitID || abs32(after.Offset-before.Offset*k) > 4 {
		t.Errorf("the same paragraph at the same place: %v %v, then %v %v", before.UnitID, before.Offset, after.UnitID, after.Offset)
	}
}

// A larger type size glides there, the text laid out anew each frame, and the
// block being read stays where it showed the whole way, rather than the text
// fading over to its new layout.
// Flowing to a new measure, the words show in every frame, the one it ends in too: none is the bare page.
func TestFlowNeverBlank(t *testing.T) {
	_, tt, _ := openLobehub(t)
	must(t, tt.Click("阅读选项"))
	settle(tt)
	text := image.Rect(300, 260, 1200, 840)
	ink := func() float64 {
		m := tt.Image()
		var s float64
		n := 0
		for y := text.Min.Y; y < text.Max.Y; y += 2 {
			for x := text.Min.X; x < text.Max.X; x += 2 {
				s += float64(m.RGBAAt(x, y).G)
				n++
			}
		}
		return s / float64(n)
	}
	for _, b := range []string{"宽松", "舒适"} {
		before := ink()
		must(t, tt.Click(b))
		for i := range 40 {
			if v := ink(); v < 0.8*before {
				t.Fatalf("%s, frame %d: the text area is %.1f bright, against %.1f before: the words did not show", b, i, v, before)
			}
			tt.Advance(16 * time.Millisecond)
		}
		settle(tt)
	}
}

func TestTypeSizeGlidesInPlace(t *testing.T) {
	a, tt, rp := openLobehub(t)
	r := rp.r
	// The second heading at the guide: it is the block held.
	hd := rp.content.Headings[1]
	for range 40 {
		y, _, _ := r.AnchorBox(hd.ID)
		if y-r.Scroll.Y <= 100 {
			break
		}
		tt.Scroll(700, 500, 0, 40)
		tt.Advance(16 * time.Millisecond)
	}
	// A little back up brings the bar back, with 阅读选项 on it.
	for range 8 {
		tt.Scroll(700, 500, 0, -1.5)
		tt.Advance(16 * time.Millisecond)
	}
	tt.Advance(time.Second)
	place := func() float32 {
		box, ok := tt.Find(hd.Text)
		if !ok {
			t.Fatalf("%q does not show", hd.Text)
		}
		return box.Y
	}
	before := place()
	must(t, tt.Click("阅读选项"))
	settle(tt)
	must(t, tt.Click("放大字号"))
	from, between := r.Size, false
	for range 30 {
		tt.Advance(16 * time.Millisecond)
		if r.Size > from && r.Size < a.fontSize {
			between = true
		}
		if y := place(); y < before-1 || y > before+1 {
			t.Fatalf("the block being read moved from %v to %v at size %v", before, y, r.Size)
		}
	}
	settle(tt)
	if !between || r.Size != a.fontSize {
		t.Errorf("the size went %v → %v (target %v), through sizes between: %v", from, r.Size, a.fontSize, between)
	}
	if y := place(); y < before-1 || y > before+1 {
		t.Errorf("settled, the block being read is at %v, not %v", y, before)
	}
	// A wider measure is laid out at once, the words flowing over to it (reader flow.go), and the block being read
	// is where it was once they have.
	before = place()
	must(t, tt.Click("宽松"))
	tt.Advance(16 * time.Millisecond)
	tt.Advance(16 * time.Millisecond)
	if !r.Flowing() || r.Width != a.column() {
		t.Fatalf("wider: flowing %v, width %v (target %v)", r.Flowing(), r.Width, a.column())
	}
	settle(tt)
	if r.Flowing() {
		t.Error("the flow did not end")
	}
	if y := place(); y < before-1 || y > before+1 {
		t.Errorf("flowed, the block being read is at %v, not %v", y, before)
	}
}

// From the keys a new size shows in the next frame, at the same place;
// under less motion it still crosses over, as it only fades.
func TestReflowAtOnce(t *testing.T) {
	a, tt, rp := openOptions(t)
	plus, _ := tt.Access("放大字号")
	tt.ClickAt(center(plus.Bounds))
	tt.Advance(time.Second)
	tt.Key(0, ui.KeyTab)
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Advance(16 * time.Millisecond)
	if !tt.Focused("放大字号") {
		t.Fatal("the keys on 放大字号")
	}
	before, y0 := rp.r.Position(), rp.r.Scroll.Y
	tt.Key(0, ui.KeySpace)
	tt.Advance(16 * time.Millisecond)
	tt.Advance(16 * time.Millisecond)
	if a.fontSize != 19 || rp.r.Scroll.Y == y0 {
		t.Fatalf("from the keys: size %v, the scroll at once %v (%v)", a.fontSize, rp.r.Scroll.Y, y0)
	}
	samePlace(t, before, rp.r.Position(), 19.0/18)
	a.Reduce = true
	tt.Advance(time.Second)
	before, y0 = rp.r.Position(), rp.r.Scroll.Y
	minus, _ := tt.Access("缩小字号")
	tt.ClickAt(center(minus.Bounds))
	tt.Advance(50 * time.Millisecond)
	if a.fontSize != 18 || rp.r.Scroll.Y != y0 {
		t.Fatalf("less motion: size %v, the scroll waits for the old text at %v (%v)", a.fontSize, rp.r.Scroll.Y, y0)
	}
	tt.Advance(100 * time.Millisecond)
	if rp.r.Scroll.Y == y0 {
		t.Fatal("less motion: the old text faded, the scroll takes the shift")
	}
	samePlace(t, before, rp.r.Position(), 18.0/19)
}

// Back from an article by the pointer, its row's moon goes from where it was
// as the article was opened to where the reader left it, under
// less motion too; from the keys it is simply there.
func TestMoonWanes(t *testing.T) {
	for _, k := range []struct {
		name       string
		kbd, still bool
	}{{"pointer", false, false}, {"keys", true, false}, {"still", false, true}} {
		t.Run(k.name, func(t *testing.T) {
			a, tt, rp := openLobehub(t)
			a.Reduce = k.still
			rp.r.Scroll.Y = rp.r.Scroll.MaxY * 0.4
			tt.Frame()
			settle(tt)
			if lit := litOf(a.article(lobehub)); lit > 0.8 || rp.lit != 1 {
				t.Fatalf("read on: the moon %v, %v as it opened", lit, rp.lit)
			}
			if k.kbd {
				tt.Key(0, ui.KeyEscape)
			} else {
				a.Press("back")
				tt.Frame()
			}
			tt.Advance(16 * time.Millisecond)
			tt.Advance(16 * time.Millisecond)
			if a.page != (page{"author", "innei"}) {
				t.Fatalf("back: on %v", a.page)
			}
			m := a.moonBack
			if wanes := m != nil && m.started && m.from == 1; wanes == k.kbd {
				t.Fatalf("the row's moon wanes %v", wanes)
			}
			tt.Advance(motion.MoonWane)
			tt.Advance(16 * time.Millisecond)
			if a.moonBack != nil {
				t.Error("the moon rests where the reader left it")
			}
		})
	}
}

// TestLeftoverFrames writes the reader's leftovers frame by frame, as
// TestFrames does: LEFTOVERS=dir go test -run LeftoverFrames.
func TestLeftoverFrames(t *testing.T) {
	dir := os.Getenv("LEFTOVERS")
	if dir == "" {
		t.Skip("LEFTOVERS not set")
	}
	os.MkdirAll(dir, 0o755)
	const step = 40 * time.Millisecond
	// record plays act and the span after it a step at a time, keeping the
	// frames at keep whole, to set beside the previous implementation's
	// captures.
	record := func(name string, tt *ui.Tester, span time.Duration, act func(), keep ...int) {
		act()
		var frames []*image.RGBA
		t0 := tt.Now()
		for d := time.Duration(0); d <= span; d += step {
			tt.Advance(d - tt.Now().Sub(t0))
			m := tt.Image()
			if slices.Contains(keep, int(d/time.Millisecond)) {
				savePNG(t, filepath.Join(dir, fmt.Sprintf("%s-%d-mygo.png", name, d/time.Millisecond)), m)
			}
			frames = append(frames, halve(m))
		}
		writeSheet(t, filepath.Join(dir, name+".png"), frames)
		writeGIF(t, filepath.Join(dir, name+".gif"), frames, step)
	}
	// The picture: out of its place and back.
	{
		a, tt, rp := openLobehub(t)
		box := showPicture(t, tt, rp, lobePic)
		tt.Move(box.X+box.W/2, box.Y+box.H/2)
		settle(tt)
		record("viewer-open", tt, 560*time.Millisecond, func() { tt.ClickAt(box.X+box.W/2, box.Y+box.H/2) }, 120)
		tt.Move(40, 425)
		settle(tt)
		savePNG(t, filepath.Join(dir, "viewer-mygo.png"), tt.Image())
		record("viewer-close", tt, 400*time.Millisecond, func() { tt.ClickAt(40, 425) })
		_ = a
	}
	// A larger size, and the wider measure, read from the middle.
	for _, k := range []struct{ name, control string }{{"reflow-size", "放大字号"}, {"reflow-wide", "宽松"}} {
		_, tt, rp := openLobehub(t)
		must(t, tt.Click("阅读选项"))
		settle(tt)
		rp.r.Scroll.Y = 4000
		tt.Frame()
		settle(tt)
		b, ok := tt.Access(k.control)
		if !ok {
			b.Bounds, ok = tt.Find(k.control)
		}
		if !ok {
			t.Fatalf("no %q", k.control)
		}
		tt.Move(b.Bounds.X+b.Bounds.W/2, b.Bounds.Y+b.Bounds.H/2)
		settle(tt)
		record(k.name, tt, 320*time.Millisecond, func() { tt.ClickAt(b.Bounds.X+b.Bounds.W/2, b.Bounds.Y+b.Bounds.H/2) }, 0, 40, 200)
	}
	// Back from an article read to 40%: its row's moon wanes, shown four
	// times as large.
	{
		a, tt, rp := openLobehub(t)
		rp.r.Scroll.Y = rp.r.Scroll.MaxY * 0.4
		tt.Frame()
		settle(tt)
		tt.Move(1000, 700)
		var frames []*image.RGBA
		a.Press("back")
		tt.Frame()
		t0 := tt.Now()
		for d := time.Duration(0); d <= 560*time.Millisecond; d += 20 * time.Millisecond {
			tt.Advance(d - tt.Now().Sub(t0))
			r := a.list("innei").rows[lobehub]
			frames = append(frames, zoom(tt.Image(), image.Rect(int(r.X)+50, int(r.Y)+14, int(r.X)+80, int(r.Y)+40), 8))
		}
		writeSheet(t, filepath.Join(dir, "moon-wane.png"), frames)
		writeGIF(t, filepath.Join(dir, "moon-wane.gif"), frames, 20*time.Millisecond)
	}
}

// zoom crops m to r and scales it k times, each pixel a k×k block.
func zoom(m *image.RGBA, r image.Rectangle, k int) *image.RGBA {
	r = r.Intersect(m.Bounds())
	out := image.NewRGBA(image.Rect(0, 0, r.Dx()*k, r.Dy()*k))
	for y := 0; y < r.Dy()*k; y++ {
		for x := 0; x < r.Dx()*k; x++ {
			out.Set(x, y, m.At(r.Min.X+x/k, r.Min.Y+y/k))
		}
	}
	return out
}

// TestPictureTone writes the same picture of the body, on the page and
// large, to set beside the previous implementation's capture: TONE=label
// LEFTOVERS=dir go test -run PictureTone.
func TestPictureTone(t *testing.T) {
	dir, label := os.Getenv("LEFTOVERS"), os.Getenv("TONE")
	if dir == "" || label == "" {
		t.Skip("LEFTOVERS or TONE not set")
	}
	_, tt, rp := openLobehub(t)
	box := showPicture(t, tt, rp, lobePic)
	tt.Move(1000, 820)
	settle(tt)
	savePNG(t, filepath.Join(dir, "picture-page-mygo-"+label+".png"), tt.Image().SubImage(image.Rect(int(box.X), int(box.Y), int(box.X+box.W), int(box.Y+box.H))))
	tt.ClickAt(center(box))
	tt.Advance(time.Second)
	tt.Move(40, 425)
	tt.Advance(time.Second)
	savePNG(t, filepath.Join(dir, "picture-large-mygo-"+label+".png"), tt.Image())
}

// TestShotFrames writes a work's shots in the viewer, opened from its first
// screen and stepped by
// the pointer, to set beside the previous implementation's capture:
// SHOTWORK=work.json LEFTOVERS=dir go test -run ShotFrames, the work as the
// app keeps Extrafazant once it has fetched its shots.
func TestShotFrames(t *testing.T) {
	dir, path := os.Getenv("LEFTOVERS"), os.Getenv("SHOTWORK")
	if dir == "" || path == "" {
		t.Skip("LEFTOVERS or SHOTWORK not set")
	}
	const id = "https://awwwards.com/sites/extrafazant"
	work, err := os.ReadFile(path)
	must(t, err)
	a, store := newTestApp(t)
	kept, err := store.Content(id)
	must(t, err)
	kept.Work = work
	must(t, store.SaveContent(*kept))
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	a.Press("author:feed:8b485b2c47adf617")
	settle(tt)
	a.Press("open:" + id)
	settle(tt)
	rp := a.readers[id]
	for range 3 { // the strip 200 below the top of the window, as the recorded capture has it
		b, ok := tt.Find("Extrafazant 的截图")
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
	first, ok := tt.Access("查看大图：Loader and Hero Variable Font Interaction")
	if !ok {
		t.Fatal("the first screen does not show the first shot")
	}
	tt.ClickAt(center(first.Bounds))
	tt.Advance(900 * time.Millisecond)
	tt.Move(640, 830)
	tt.Advance(500 * time.Millisecond)
	savePNG(t, filepath.Join(dir, "shot-open-mygo.png"), tt.Image())
	next, _ := tt.Access("下一张")
	tt.ClickAt(center(next.Bounds))
	tt.Advance(17 * time.Millisecond) // the step shows a frame later
	tt.Advance(84 * time.Millisecond)
	savePNG(t, filepath.Join(dir, "shot-step-80-mygo.png"), tt.Image())
	tt.Advance(900 * time.Millisecond)
	tt.Move(640, 830)
	tt.Advance(500 * time.Millisecond)
	savePNG(t, filepath.Join(dir, "shot-stepped-mygo.png"), tt.Image())
}

// TestWorkRest writes Extrafazant's work page at rest, its strip 200 below
// the top of the window as the recorded capture has it, and a normal
// article at the top, each labelled to set the code's before beside its
// after: SHOTWORK=work.json REST=label LEFTOVERS=dir go test -run WorkRest -v.
func TestWorkRest(t *testing.T) {
	dir, path, label := os.Getenv("LEFTOVERS"), os.Getenv("SHOTWORK"), os.Getenv("REST")
	if dir == "" || path == "" || label == "" {
		t.Skip("LEFTOVERS, SHOTWORK or REST not set")
	}
	pinNow(t)
	const id = "https://awwwards.com/sites/extrafazant"
	work, err := os.ReadFile(path)
	must(t, err)
	a, store := newTestApp(t)
	kept, err := store.Content(id)
	must(t, err)
	kept.Work = work
	must(t, store.SaveContent(*kept))
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	a.Press("author:feed:8b485b2c47adf617")
	settle(tt)
	a.Press("open:" + id)
	settle(tt)
	rp := a.readers[id]
	for _, s := range []string{"11月8日", "Extrafazant 的首屏", "Extrafazant 的截图"} { // at the top: the head's date, then the work
		r, ok := tt.Find(s)
		t.Logf("at the top, %s: %v %v", s, r, ok)
	}
	for range 3 {
		b, ok := tt.Find("Extrafazant 的截图")
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
	tt.Move(1275, 845)
	settle(tt)
	savePNG(t, filepath.Join(dir, "work-rest-mygo-"+label+".png"), tt.Image())
	for _, s := range []string{"Extrafazant 的截图", "Loader and Hero Variable Font Interaction", "由 Dylan Brouwer 设计", "extrafazant.nl",
		"荣誉", "评分", "总分", "设计", "可用性", "创意", "内容", "7.22", "类别", "标签", "技术", "配色", "#101010"} {
		r, ok := tt.Find(s)
		t.Logf("%s: %v %v", s, r, ok)
	}
	for _, n := range []struct {
		role ui.Role
		name string
	}{{ui.RoleArticle, "Extrafazant，正文"}} {
		acc, ok := tt.AccessByRole(n.role, n.name)
		t.Logf("%s: %v %v", n.name, acc.Bounds, ok)
	}
	_, normal, _ := openLobehub(t)
	normal.Move(1275, 845)
	settle(normal)
	savePNG(t, filepath.Join(dir, "normal-rest-mygo-"+label+".png"), normal.Image())
}
