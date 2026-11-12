package main

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// sample is a library of two authors without the fixture.
func sample() []*article {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 9, 0, 0, 0, time.UTC) }
	body := []para{{text: "迁移从一张表开始：哪些组件还在用，哪些已经可以换掉。"}, {text: "第二段话，长一点，好让正文有几行可读。"}}
	return []*article{
		{id: "a1", title: "十个月，给 LobeHub 换掉 Ant Design：向 Base UI 与 StyleX 的渐进式迁移", author: "Innei", at: day(4), paras: body},
		{id: "a2", title: "Pluggable, Extensible, and Playful DevTools", author: "Anthony Fu", at: day(3), paras: body},
		{id: "a3", title: "手记2", author: "Innei", at: day(2), paras: body},
	}
}

const title = "十个月，给 LobeHub 换掉 Ant Design：向 Base UI 与 StyleX 的渐进式迁移"

// headlineBox is where the reader's headline shows: the first text
// showing the title once the reader is the page.
func headlineBox(t *testing.T, tt *ui.Tester) ui.Rect {
	t.Helper()
	r, ok := tt.Find(title)
	if !ok {
		t.Fatal("no headline")
	}
	return r
}

// Opening an article with the pointer: the title flies, the ground
// spreads, and the reader settles from a little closer, so the headline is
// not yet where it rests.
func TestPointerOpensWithMotion(t *testing.T) {
	a := newApp(sample())
	tt := ui.NewTester(a.view, 1280, 820)
	if err := tt.Click(title); err != nil {
		t.Fatal(err)
	}
	if a.page.kind != "reader" || len(a.flights) != 1 || a.lay == nil {
		t.Fatalf("page %v, %d flights, ground %v", a.page, len(a.flights), a.lay != nil)
	}
	tt.Advance(60 * time.Millisecond)
	moving := headlineBox(t, tt)
	tt.Advance(2 * time.Second)
	rest := headlineBox(t, tt)
	if moving == rest {
		t.Errorf("the headline rested at once without less motion: %v", rest)
	}
	if len(a.flights) != 0 || a.lay != nil {
		t.Errorf("still flying after 2 s: %d flights, ground %v", len(a.flights), a.lay != nil)
	}
}

// Under less motion, the desktop's or the spike's switch, nothing moves:
// the title does not fly, the reader shows at its place and size at once,
// and the ground is laid where it goes; fades keep their time.
func TestReduceMotionPlacesAtOnce(t *testing.T) {
	for _, desktop := range []bool{true, false} {
		a := newApp(sample())
		var tt *ui.Tester
		if desktop {
			tt = ui.NewTesterWith(a.view, 1280, 820, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
		} else {
			tt = ui.NewTester(a.view, 1280, 820)
			a.reduce = true
			tt.Frame()
			tt.Advance(time.Second)
		}
		if err := tt.Click(title); err != nil {
			t.Fatal(err)
		}
		if len(a.flights) != 0 || a.lay == nil || !a.lay.Still || a.lay.To != a.paper() {
			t.Errorf("desktop %v: %d flights, ground %+v", desktop, len(a.flights), a.lay)
		}
		tt.Advance(60 * time.Millisecond)
		early := headlineBox(t, tt)
		if !tt.FramePending() {
			t.Errorf("desktop %v: nothing fades in", desktop)
		}
		tt.Advance(2 * time.Second)
		if rest := headlineBox(t, tt); early != rest {
			t.Errorf("desktop %v: the headline moved, %v then %v", desktop, early, rest)
		}
	}
}

// What the keyboard does shows at once: opening the focused row with
// Enter, going back with Escape, a tab with ⌘2 and the shortcut sheet
// with ?.
func TestKeyboardDoesNotAnimate(t *testing.T) {
	a := newApp(sample())
	tt := ui.NewTester(a.view, 1280, 820)
	for i := 0; i < 40 && !tt.Focused(title); i++ {
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused(title) {
		t.Fatal("Tab never reached the row")
	}
	steps := []struct {
		name string
		do   func()
		page string
	}{
		{"Enter", func() { tt.Key(0, ui.KeyEnter) }, "reader"},
		{"Escape", func() { tt.Key(0, ui.KeyEscape) }, "home"},
		{"⌘2", func() { tt.Key(ui.Cmd, ui.Key2) }, "explore"},
		{"?", func() { tt.Key(ui.Shift, ui.KeySlash) }, "explore"},
	}
	for _, s := range steps {
		s.do()
		if tt.FramePending() || len(a.flights) != 0 || a.lay != nil {
			t.Errorf("%s animates: frames pending %v, %d flights, ground %v", s.name, tt.FramePending(), len(a.flights), a.lay != nil)
		}
		if a.page.kind != s.page {
			t.Errorf("after %s the page is %v, want %s", s.name, a.page, s.page)
		}
	}
	if !a.sheet || !tt.HasText("这张表") {
		t.Error("? did not open the shortcut sheet")
	}
}

// A page left by a click lets go out of focus as the next comes in: MyGo
// builds a frame again after a click, and the page must still carry its
// exit in that frame's last pass. 40 ms after the tab is clicked, the home
// page's text still shows where the explore page, at rest, has none.
func TestPageLeftFadesOut(t *testing.T) {
	a := newApp(sample())
	tt := ui.NewTester(a.view, 1280, 820)
	if err := tt.Click("探索"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(40 * time.Millisecond)
	mid := tt.Image().SubImage(image.Rect(240, 0, 1280, 820)).(*image.RGBA)
	mid = &image.RGBA{Pix: append([]uint8(nil), mid.Pix...), Stride: mid.Stride, Rect: mid.Rect}
	tt.Advance(time.Second)
	end := tt.Image()
	n := 0
	for y := mid.Rect.Min.Y; y < mid.Rect.Max.Y; y++ {
		for x := mid.Rect.Min.X; x < mid.Rect.Max.X; x++ {
			if luma(mid, x, y) > luma(end, x, y)+20 {
				n++
			}
		}
	}
	if n < 300 {
		t.Errorf("40 ms after the click, %d pixels of the home page still show; it vanished at once", n)
	}
}

func luma(m *image.RGBA, x, y int) int {
	i := m.PixOffset(x, y)
	return (int(m.Pix[i]) + int(m.Pix[i+1]) + int(m.Pix[i+2])) / 3
}
