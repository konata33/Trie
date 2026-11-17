package app

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/motion"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/theme"
)

var pictogramNames = []string{"loading", "unreachable", "broken", "picture", "clips", "favorites", "mailbox"}

// pictogramView is a window showing pictograms names in a row on the
// window's colour.
func pictogramView(names ...string) func(c *ui.Context) {
	a := &App{}
	return func(c *ui.Context) {
		ui.Row(c).Fill().Background(theme.Window).Padding(16).Gap(36).Wrap().Children(func() {
			for _, n := range names {
				a.pictogram(c, n)
			}
		})
	}
}

// A pictogram moves while it shows, and asks for no frame held still or
// with the window hidden.
func TestPictogramsMove(t *testing.T) {
	for _, n := range pictogramNames {
		tt := ui.NewTester(pictogramView(n), 200, 170)
		tt.Frame()
		before := slices.Clone(tt.Image().Pix)
		tt.Advance(700 * time.Millisecond)
		if bytes.Equal(before, tt.Image().Pix) || !tt.FramePending() {
			t.Errorf("%s does not move", n)
		}
		tt.SetWindowHidden(true)
		tt.Advance(100 * time.Millisecond)
		if tt.FramePending() {
			t.Errorf("%s asks for frames with the window hidden", n)
		}
		still := ui.NewTesterWith(pictogramView(n), 200, 170, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
		still.Frame()
		before = slices.Clone(still.Image().Pix)
		still.Advance(700 * time.Millisecond)
		if still.FramePending() || !bytes.Equal(before, still.Image().Pix) {
			t.Errorf("%s moves under less motion", n)
		}
	}
	// Scrolled out of view, it asks for none; back in, it moves again.
	a := &App{}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Scroll(c).Fill().Children(func() {
			a.pictogram(c, "loading")
			ui.Box(c).Height(2000)
		})
	}, 200, 170)
	tt.Frame()
	tt.Scroll(100, 80, 0, 600)
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Error("a pictogram out of view asks for frames")
	}
	tt.Scroll(100, 80, 0, -600)
	tt.Advance(time.Second)
	if !tt.FramePending() {
		t.Error("a pictogram back in view does not move")
	}
}

// TestPictogramShots writes the pictograms and the pages that show them:
// PICTOS=dir go test -run PictogramShots.
func TestPictogramShots(t *testing.T) {
	dir := os.Getenv("PICTOS")
	if dir == "" {
		t.Skip("PICTOS not set")
	}
	os.MkdirAll(dir, 0o755)
	// The stills, as the study's sheet, at 1x and 2x.
	for _, scale := range []float32{1, 2} {
		tt := ui.NewTesterWith(pictogramView(pictogramNames...), 4*204+16, 2*170+32, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
		tt.SetScale(scale)
		tt.Frame()
		savePNG(t, filepath.Join(dir, fmt.Sprintf("stills-%gx.png", scale)), tt.Image())
	}
	// Each moving, a frame every half second for six seconds.
	for _, n := range pictogramNames {
		for _, scale := range []float32{1, 2} {
			tt := ui.NewTester(pictogramView(n), 200, 170)
			tt.SetScale(scale)
			tt.Frame()
			var frames []*image.RGBA
			for range 13 {
				frames = append(frames, tt.Image())
				tt.Advance(500 * time.Millisecond)
			}
			writeSheet(t, filepath.Join(dir, fmt.Sprintf("frames-%s-%gx.png", n, scale)), frames)
		}
	}
	// What a frame costs: the scene alone, and a frame of the Tester's (the CPU's painting) at 2x.
	var times strings.Builder
	tester := func(view func(c *ui.Context)) *ui.Tester {
		tt := ui.NewTester(view, 200, 170)
		tt.SetScale(2)
		return tt
	}
	for _, n := range pictogramNames {
		var ds []sceneDot
		start := time.Now()
		for i := range 300 {
			ds = pictogramScenes[n](float64(i)/30, false, ds[:0])
			slices.SortStableFunc(ds, func(a, b sceneDot) int { return cmp.Compare(a.z, b.z) })
		}
		build := time.Since(start) / 300
		fmt.Fprintf(&times, "%-12s %5d dots  scene %6.3f ms  %s\n", n, len(ds), float64(build)/1e6, frameTimes(tester(pictogramView(n))))
	}
	// The same box, repainting nothing.
	empty := func(c *ui.Context) {
		ui.Row(c).Fill().Background(theme.Window).Padding(16).Children(func() {
			ui.Box(c).Width(168).AspectRatio(200.0 / 160).Draw(func(p *ui.Painter, _ ui.Rect) { p.AnimationFrameRate(pictogramFPS) })
		})
	}
	fmt.Fprintf(&times, "%-12s %5d dots  scene %6.3f ms  %s\n", "(nothing)", 0, 0.0, frameTimes(tester(empty)))
	os.WriteFile(filepath.Join(dir, "frame-cost.txt"), []byte(times.String()), 0o644)
	t.Log("\n" + times.String())
	// The pages, at 1x and 2x.
	for _, scale := range []float32{1, 2} {
		pictogramPages(t, dir, scale)
	}
}

// frameTimes is what tt's frames cost, a repaint at a time at the
// pictograms' rate: CPU painting, no GPU.
func frameTimes(tt *ui.Tester) string {
	tt.Frame()
	var took []time.Duration
	for range 150 {
		start := time.Now()
		tt.Advance(motion.Pictogram)
		took = append(took, time.Since(start))
	}
	slices.Sort(took)
	ms := func(d time.Duration) float64 { return float64(d) / 1e6 }
	return fmt.Sprintf("Tester frame median %6.2f ms  p90 %6.2f ms", ms(took[len(took)/2]), ms(took[len(took)*9/10]))
}

func pictogramPages(t *testing.T, dir string, scale float32) {
	shot := func(tt *ui.Tester, name string, around ui.Rect) {
		t.Helper()
		m := tt.Image()
		savePNG(t, filepath.Join(dir, fmt.Sprintf("page-%s-%gx.png", name, scale)), m)
		r := image.Rect(int((around.X-150)*scale), int((around.Y-260)*scale), int((around.X+around.W+150)*scale), int((around.Y+around.H+60)*scale))
		savePNG(t, filepath.Join(dir, fmt.Sprintf("crop-%s-%gx.png", name, scale)), m.SubImage(r.Intersect(m.Bounds())).(*image.RGBA))
	}
	find := func(tt *ui.Tester, s string) ui.Rect {
		t.Helper()
		r, ok := tt.Find(s)
		if !ok {
			t.Fatalf("no %q: %q", s, tt.Texts())
		}
		return r
	}
	// 收藏 and 摘录 with nothing kept or clipped.
	a, store := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	tt.SetScale(scale)
	settle(tt)
	lib, err := store.Library()
	must(t, err)
	off := library.SetFavorites{}
	for _, art := range lib.Articles {
		if art.Favorite {
			off.Items = append(off.Items, library.FavoriteTime{ID: art.ID})
		}
	}
	_, err = store.Mutate(off)
	must(t, err)
	for _, cl := range lib.Clips {
		_, err = store.Mutate(library.DeleteClip{ID: cl.ID})
		must(t, err)
	}
	lib, err = store.Library()
	must(t, err)
	a.accept(lib)
	tt.Key(ui.Cmd, ui.Key2)
	settle(tt)
	shot(tt, "favorites", find(tt, locales.FavoritesEmptyTitle()))
	t.Logf("the window on 收藏 with nothing kept, at %gx: %s", scale, frameTimes(tt))
	tt.Key(ui.Cmd, ui.Key4)
	settle(tt)
	shot(tt, "clips", find(tt, locales.ClipsEmptyTitle()))
	// An author with nothing yet.
	lib.Articles = slices.DeleteFunc(lib.Articles, func(art library.Article) bool { return slices.Contains(art.SourceIDs, "innei") })
	a.accept(lib)
	a.Press("author:innei")
	settle(tt)
	shot(tt, "author", find(tt, locales.AuthorEmptyTitle()))
	// The reader waiting for a body, and not reaching it.
	a, _ = newTestApp(t)
	tt = ui.NewTester(a.View, 1280, 850)
	tt.SetScale(scale)
	settle(tt)
	const lobe = "https://innei.in/posts/tech/lobehub-antd-to-base-ui-stylex-migration"
	a.Press("author:innei")
	settle(tt)
	a.Press("open:" + lobe)
	settle(tt)
	rp := a.readers[lobe]
	rp.content, rp.loading = nil, true
	tt.Frame()
	tt.Advance(time.Second)
	if r, ok := tt.AccessByRole(ui.RoleStatus, locales.ReaderLoading()); ok {
		shot(tt, "reader-loading", ui.Rect{X: r.Bounds.X + r.Bounds.W/2 - 84, Y: r.Bounds.Y + r.Bounds.H/2 + 120, W: 168})
	} else {
		t.Errorf("no reader loading: %q", tt.Texts())
	}
	rp.loading, rp.err = false, "offline"
	tt.Frame()
	tt.Advance(time.Second)
	shot(tt, "reader-unreachable", find(tt, locales.ReaderUnreachableTitle()))
	// A picture in the body that did not load (TestPictureRetry).
	a, store = newTestApp(t)
	const id = "https://antfu.me/posts/categorize-deps"
	kept, err := store.Content(id)
	must(t, err)
	kept.Document = nil
	kept.HTML = strings.Replace(kept.HTML, "<p><span>图片暂未载入：Screenshot of the extension PNPM Catalog Lens</span></p>",
		`<p><img src="https://antfu.me/images/pnpm-catalog-lens.png" alt="Screenshot" width="800" height="400"></p>`, 1)
	must(t, store.SaveContent(*kept))
	tt = ui.NewTester(a.View, 1280, 850)
	tt.SetScale(scale)
	settle(tt)
	a.Press("author:antfu")
	settle(tt)
	a.Press("open:" + id)
	settle(tt)
	r := find(tt, locales.CommonTryAgain())
	for r.Y > 700 || r.Y < 300 {
		tt.Scroll(700, 400, 0, r.Y-500)
		tt.Advance(time.Second)
		r = find(tt, locales.CommonTryAgain())
	}
	shot(tt, "picture", ui.Rect{X: r.X + r.W/2 - 200, Y: r.Y, W: 400, H: r.H})
	// Explore when its list cannot be read (TestExploreUnreadable).
	a, _ = newTestApp(t)
	a.opt.Explore = explore.New(explore.Options{Resources: filepath.Join(t.TempDir(), "no-explore"), Cache: filepath.Join(t.TempDir(), "explore-cache.json"),
		FetchFeed: func(context.Context, string) (library.ParsedFeed, error) { return library.ParsedFeed{}, errOffline },
		Fetch:     offline()})
	tt = ui.NewTester(a.View, 1280, 850)
	tt.SetScale(scale)
	toExplore(t, a, tt)
	shot(tt, "explore-unreachable", find(tt, locales.ExploreErrorTitle()))
}
