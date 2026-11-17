package app

import (
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/theme"
)

// dotSet are the icons in the order of the approved sheet
// (.context/icon-redo/trial10/all-*.png).
var dotSet = []string{"home", "favorites", "explore", "clips", "refresh", "loading", "search", "add", "minus", "close", "back", "forward",
	"openOriginal", "openArrow", "copy", "check", "more", "trash", "play", "textSize", "time", "settings", "brokenImage"}

// TestDotSheet writes every icon at rest and lit, as the approved sheet
// lays them out, at 16 and at 12, at scale 1 and 2, and the kind marks at
// 28 and 16: DOTS=dir go test -run DotSheet.
func TestDotSheet(t *testing.T) {
	dir := os.Getenv("DOTS")
	if dir == "" {
		t.Skip("DOTS not set")
	}
	os.MkdirAll(dir, 0o755)
	a := &App{}
	pair := func(c *ui.Context, name string, size float32) {
		if name == "loading" {
			ui.Box(c.Key("rest")).TextColor(theme.Text2).Children(func() { a.loading(c, size).Rotate(0) })
			ui.Box(c.Key("lit")).TextColor(theme.Text).Children(func() { a.loading(c, size).Rotate(0) })
			return
		}
		a.icon(c, name, size, false).TextColor(theme.Text2)
		a.icon(c, name, size, true).TextColor(theme.Text)
	}
	grid := func(names []string, size float32) func(c *ui.Context) {
		return func(c *ui.Context) {
			ui.Column(c).Background(theme.Window).Size(576, 64*float32((len(names)+5)/6)).Children(func() {
				for row := 0; row*6 < len(names); row++ {
					ui.Row(c.Key(row)).Height(64).Children(func() {
						for col := range min(6, len(names)-row*6) {
							i := row*6 + col
							ui.Row(c.Key(i)).Width(96).Height(52).Justify(ui.Center).AlignItems(ui.Center).Gap(max(8, 46-2*size)).Children(func() {
								pair(c, names[i], size)
							})
						}
					})
				}
			})
		}
	}
	kinds := []string{"kind-landing", "kind-portfolio", "kind-motion", "kind-type", "kind-brand", "kind-articles"}
	for _, s := range []struct {
		name  string
		names []string
		size  float32
	}{{"icons-16", dotSet, 16}, {"icons-12", dotSet, 12}, {"kinds-28", kinds, 28}, {"kinds-16", kinds, 16}} {
		tt := ui.NewTester(grid(s.names, s.size), 576, int(64*((len(s.names)+5)/6)))
		for _, scale := range []float32{1, 2} {
			tt.SetScale(scale)
			settle(tt)
			savePNG(t, filepath.Join(dir, s.name+"-"+strconv.Itoa(int(scale))+"x.png"), tt.Image())
		}
	}
}

// TestDotFrames writes the dots' motion in the app frame by frame, 33 ms
// apart, at scale 1 and 2, cropped to where it happens, as contact sheets
// and GIFs, with a still of where each ends: the sidebar at rest, the
// pointer over the tabs, a press on an icon button, a change of tab, a
// refresh, a copy in the clip bar, the kind marks on 收藏, and the
// favourite: DOTS=dir go test -run DotFrames (ONLY=name for one).
func TestDotFrames(t *testing.T) {
	dir := os.Getenv("DOTS")
	if dir == "" {
		t.Skip("DOTS not set")
	}
	os.MkdirAll(dir, 0o755)
	const step = 33 * time.Millisecond
	type scene struct {
		name string
		open func(t *testing.T) (*App, *ui.Tester)
		// set brings the app to where the scene starts; it returns the area
		// to crop, in DIPs.
		set func(a *App, tt *ui.Tester) ui.Rect
		// act is what happens at the scene's start, and how long to film.
		act  func(a *App, tt *ui.Tester)
		span time.Duration
		walk bool // the pointer walks along the tabs instead
		// then happens thenAt into the film.
		then   func(a *App, tt *ui.Tester)
		thenAt time.Duration
	}
	plain := func(t *testing.T) (*App, *ui.Tester) {
		a, _ := newTestApp(t)
		return a, ui.NewTester(a.View, 1280, 850)
	}
	seeded := func(t *testing.T) (*App, *ui.Tester) {
		a, _ := newSeededApp(t)
		return a, ui.NewTester(a.View, 1280, 850)
	}
	find := func(tt *ui.Tester, name string) ui.Rect {
		if r, ok := tt.Find(name); ok {
			return r
		}
		acc, ok := tt.Access(name)
		if !ok {
			t.Fatalf("no %q", name)
		}
		return acc.Bounds
	}
	tabsArea := func(tt *ui.Tester) ui.Rect {
		home := find(tt, "首页")
		return ui.Rect{X: 0, Y: home.Y - 14, W: 232, H: home.H + 28}
	}
	scenes := []scene{
		{name: "sidebar", open: plain, set: func(a *App, tt *ui.Tester) ui.Rect { return ui.Rect{W: 232, H: 850} }},
		// The pointer goes along the tabs' icons, 3 px a frame.
		{name: "pointer", open: plain, set: func(a *App, tt *ui.Tester) ui.Rect { return tabsArea(tt) }, walk: true},
		{name: "press", open: plain, set: func(a *App, tt *ui.Tester) ui.Rect {
			r := find(tt, "订阅博客…")
			tt.Move(r.X+r.W/2, r.Y+r.H/2)
			tt.Advance(time.Second)
			return ui.Rect{X: r.X - 72, Y: r.Y - 16, W: r.W + 144, H: r.H + 32}
		}, act: func(a *App, tt *ui.Tester) {
			// Pressed off its centre, toward its left, and let go away from it 132 ms on.
			r := find(tt, "订阅博客…")
			tt.Press(r.X+r.W/2-4, r.Y+r.H/2+2)
		}, then: func(a *App, tt *ui.Tester) {
			r := find(tt, "订阅博客…")
			tt.Release(r.X+400, r.Y)
		}, thenAt: 132 * time.Millisecond, span: 900 * time.Millisecond},
		{name: "tabs", open: plain, set: func(a *App, tt *ui.Tester) ui.Rect { return tabsArea(tt) },
			act: func(a *App, tt *ui.Tester) { must(t, tt.Click("摘录")) }, span: 700 * time.Millisecond},
		{name: "tabs-back", open: plain, set: func(a *App, tt *ui.Tester) ui.Rect {
			must(t, tt.Click("摘录"))
			tt.Advance(2 * time.Second)
			return tabsArea(tt)
		}, act: func(a *App, tt *ui.Tester) { must(t, tt.Click("收藏")) }, span: 700 * time.Millisecond},
		{name: "refresh", open: plain, set: func(a *App, tt *ui.Tester) ui.Rect {
			r := find(tt, "刷新")
			return ui.Rect{X: r.X - 16, Y: r.Y - 16, W: r.W + 32, H: r.H + 32}
		}, act: func(a *App, tt *ui.Tester) {
			a.refresh.busy = true
			tt.Frame()
			tt.Advance(1300 * time.Millisecond)
			a.refresh.busy = false
		}, span: 1200 * time.Millisecond},
		{name: "copy", open: seeded, set: func(a *App, tt *ui.Tester) ui.Rect {
			must(t, tt.Click("摘录"))
			tt.Advance(2 * time.Second)
			r := find(tt, leadText)
			tt.Move(r.X+r.W/2, r.Y+r.H/2)
			tt.Advance(time.Second)
			return ui.Rect{X: r.X + r.W - 100, Y: r.Y - 40, W: 120, H: 56}
		}, act: func(a *App, tt *ui.Tester) {
			r := find(tt, leadText)
			tt.Move(r.X+r.W-44, r.Y-12)
			tt.Advance(200 * time.Millisecond)
			tt.ClickAt(r.X+r.W-44, r.Y-12)
		}, span: 3300 * time.Millisecond},
		{name: "kinds", open: func(t *testing.T) (*App, *ui.Tester) {
			a, store := newExploreApp(t, nil)
			tt := ui.NewTester(a.View, 1280, 850)
			toExplore(t, a, tt)
			for _, k := range [][3]string{{"a", "Alpha", "landing"}, {"b", "Beta", "type"}, {"c", "Gamma", "brand"}, {"d", "Delta", "motion"}, {"e", "Eps", "portfolio"}} {
				url := "https://gallery.example/" + k[0]
				lib, err := store.Mutate(library.Keep{Source: library.SourceSnapshot{ID: "feed:gallery", Title: "Gallery", Host: "gallery.example", HomeURL: "https://gallery.example/"},
					Name: "Gallery", FeedURL: "https://gallery.example/feed", Article: library.ArticleSnapshot{ID: url, URL: url, Title: k[1], Authors: []string{},
						Aliases: []string{}, BodyStatus: "not_extracted", Kinds: []string{k[2]}}})
				if err == nil {
					lib, err = store.Mutate(library.ToggleFavorite{ID: url})
				}
				if err != nil {
					t.Fatal(err)
				}
				a.accept(lib)
			}
			if lib, err := store.Mutate(library.ToggleFavorite{ID: a.lib.Articles[0].ID}); err == nil {
				a.accept(lib)
			}
			return a, tt
		}, set: func(a *App, tt *ui.Tester) ui.Rect {
			must(t, tt.Click("收藏"))
			tt.Move(1270, 845)
			tt.Advance(2 * time.Second)
			return ui.Rect{X: 232, W: 1048, H: 850}
		}, act: func(a *App, tt *ui.Tester) {
			r, ok := tt.Find("落地页，收藏 1 件")
			if !ok {
				t.Fatalf("no 落地页 block: %q", tt.Texts())
			}
			tt.Move(r.X+r.W/2, r.Y+r.H/2)
		}, span: 300 * time.Millisecond},
		{name: "orbit", open: func(t *testing.T) (*App, *ui.Tester) { a, tt, _ := openLobehub(t); return a, tt }, set: func(a *App, tt *ui.Tester) ui.Rect {
			acc, ok := tt.AccessByRole(ui.RoleToggleButton, "收藏")
			if !ok {
				acc, ok = tt.AccessByRole(ui.RoleToggleButton, "取消收藏")
			}
			if !ok {
				t.Fatal("no favourite button")
			}
			r := acc.Bounds
			tt.Move(r.X+r.W/2, r.Y+r.H/2)
			tt.Advance(time.Second)
			return ui.Rect{X: r.X - 8, Y: r.Y - 8, W: r.W + 16, H: r.H + 16}
		}, act: func(a *App, tt *ui.Tester) {
			acc, _ := tt.AccessByRole(ui.RoleToggleButton, "收藏")
			x, y := center(acc.Bounds)
			tt.ClickAt(x, y)
		}, span: 900 * time.Millisecond},
	}
	for _, s := range scenes {
		if only := os.Getenv("ONLY"); only != "" && only != s.name {
			continue
		}
		for _, scale := range []float32{1, 2} {
			a, tt := s.open(t)
			settle(tt)
			tt.SetScale(scale)
			settle(tt)
			area := s.set(a, tt)
			crop := func() *image.RGBA {
				m := tt.Image()
				r := image.Rect(int(area.X*scale), int(area.Y*scale), int((area.X+area.W)*scale), int((area.Y+area.H)*scale)).Intersect(m.Bounds())
				out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
				draw.Draw(out, out.Bounds(), m, r.Min, draw.Src)
				return out
			}
			name := s.name + "-" + strconv.Itoa(int(scale)) + "x"
			if s.act == nil && !s.walk {
				savePNG(t, filepath.Join(dir, name+".png"), crop())
				continue
			}
			frames := []*image.RGBA{crop()}
			if s.walk {
				home, clips := find(tt, "首页"), find(tt, "摘录")
				y := home.Y + home.H/2
				for x := home.X - 10; x < clips.X+clips.W+10; x += 3 {
					tt.Move(x, y)
					tt.Advance(step)
					frames = append(frames, crop())
				}
			} else {
				s.act(a, tt)
				for d := time.Duration(0); d <= s.span; d += step {
					if s.then != nil && d == s.thenAt {
						s.then(a, tt)
					}
					frames = append(frames, crop())
					tt.Advance(step)
				}
			}
			tt.Advance(2 * time.Second)
			savePNG(t, filepath.Join(dir, name+"-end.png"), crop())
			writeSheet(t, filepath.Join(dir, name+".png"), frames)
			writeGIF(t, filepath.Join(dir, name+".gif"), frames, step)
		}
	}
}
