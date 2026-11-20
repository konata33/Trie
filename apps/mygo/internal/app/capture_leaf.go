package app

import (
	"image"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	xdraw "golang.org/x/image/draw"

	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/theme"
)

// The card's page (DESIGN.md Capture 卡片): what the pack holds, shown rather
// than counted. The card scrolls through the page as the reader's own window
// would, every screen at its place down it, at the card's width; past the
// page's foot the specimen: the page's face set large with its letters, and a
// contact strip of its own pictures. A thin line at the card's right edge tells
// where it is.

// packPages is a pack's page as its card shows it: each screen's top in the
// card's px and the screen itself (nil while it is read), and the specimen's
// pictures.
type packPages struct {
	tops  []float32
	shots []*ui.Bitmap
	pics  []specimenPic
	// came is when each screen read late first showed, for it to come in; read,
	// that all of it has been read.
	came map[int]time.Time
	read bool
}

type specimenPic struct {
	bmp    *ui.Bitmap
	aspect float32 // width over height
}

// screenLine is a screen as the pack's README lists it.
var screenLine = regexp.MustCompile("(?m)^- `(screens/[^`]+)` at scroll (\\d+) px")

// The specimen's measures: its top inset, its glyphs' size, the strip's
// height, and how many pictures it reads at most.
const (
	specimenTop   = 22
	specimenAa    = 64
	specimenStrip = 44
	specimenPics  = 6
)

// loadPages reads it's page for its card, off the window's thread: the screens
// in order, each shown as it comes, then the pictures. The other packs' pages
// are let go, so only the open card's are held.
func (a *App) loadPages(it *packItem) {
	h := &a.capture
	for _, o := range h.items {
		if o != it {
			o.pages = nil
		}
	}
	dir := filepath.Join(h.dir, it.ID)
	pg := &packPages{}
	it.pages = pg
	readme, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	var files []string
	for _, m := range screenLine.FindAllStringSubmatch(string(readme), -1) {
		y, _ := strconv.Atoi(m[2])
		files = append(files, m[1])
		pg.tops = append(pg.tops, float32(y)*cardW/1440)
	}
	if len(files) == 0 {
		files, pg.tops = []string{"screens/00.jpg"}, []float32{0}
	}
	pg.shots = make([]*ui.Bitmap, len(files))
	pg.shots[0], pg.came = it.shot, map[int]time.Time{}
	a.do(func() func() {
		// One after another, so a long page does not hold forty pictures at full size at once.
		for i, f := range files {
			if i == 0 && it.shot != nil {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
			if err != nil {
				continue
			}
			shot := shotOf(decodeAny(data), 2*cardW)
			// Read before the card shows it, it is simply there.
			a.post(func() { pg.shots[i] = shot; pg.came[i] = time.Time{} })
		}
		pics := readSpecimen(filepath.Join(dir, "files", "images"))
		return func() { pg.pics, pg.read = pics, true }
	})
}

// readSpecimen is the page's own pictures for the contact strip: the largest,
// as photographs and illustrations are, not icons, bars or strips.
func readSpecimen(dir string) []specimenPic {
	entries, _ := os.ReadDir(dir)
	type found struct {
		path string
		area int
	}
	var all []found
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		cfg, _, err := image.DecodeConfig(f)
		f.Close()
		if err != nil || cfg.Width < 240 || cfg.Height < 160 {
			continue
		}
		if r := float32(cfg.Width) / float32(cfg.Height); r > 2.4 || r < 0.5 {
			continue
		}
		all = append(all, found{path, cfg.Width * cfg.Height})
	}
	slices.SortFunc(all, func(x, y found) int { return y.area - x.area })
	var out []specimenPic
	for _, f := range all {
		if len(out) == specimenPics {
			break
		}
		data, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		img := decodeAny(data)
		if img == nil {
			continue
		}
		b := img.Bounds()
		h := 2 * specimenStrip
		w := max(1, b.Dx()*h/max(1, b.Dy()))
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
		out = append(out, specimenPic{ui.NewBitmap(dst), float32(b.Dx()) / float32(b.Dy())})
	}
	return out
}

// packHolds is what it's pack holds, as screen readers hear it: the card shows
// it rather than writes it.
func packHolds(it *packItem) string {
	counts := []string{locales.CaptureScreens(it.Screens)}
	if it.Images > 0 {
		counts = append(counts, locales.CaptureImages(it.Images))
	}
	if it.Fonts > 0 {
		counts = append(counts, locales.CaptureFonts(it.Fonts))
	}
	return strings.Join(counts, " · ")
}

// leafTops are the tops of it's screens down its page, in the card's px.
func leafTops(it *packItem) []float32 {
	if it.pages == nil {
		return []float32{0}
	}
	return it.pages.tops
}

// leafShot is it's screen i, nil while it is read.
func leafShot(it *packItem, i int) *ui.Bitmap {
	if it.pages == nil {
		return it.shot
	}
	return it.pages.shots[i]
}

// hasSpecimen tells whether it's page ends in a specimen: it has its own face
// for its words, or pictures of its own.
func hasSpecimen(it *packItem) bool {
	return it.font != "" || it.pages != nil && len(it.pages.pics) > 0
}

// leafHeight is how tall it's card's page is: its screens, then its specimen.
func leafHeight(it *packItem) float32 {
	tops := leafTops(it)
	h := tops[len(tops)-1] + cardH
	if hasSpecimen(it) {
		h += cardH
	}
	return h
}

// packLeaf is it's page in its card, scrolled as the reader scrolls it: the
// screens near what shows, the first rising out of its dots as the pack is
// taken (rise), and the specimen past the foot.
func (a *App) packLeaf(c *ui.Context, it *packItem, rise float32, still bool) {
	h := &a.capture
	if it.pages == nil && it.state == packTaken {
		a.loadPages(it)
	}
	sc := ui.Scroll(c.Key("leaf")).TrackScroll(&h.scroll).Absolute().Left(0).Top(0).Size(cardW, cardH).Scrollbars(false)
	if rise < 1 {
		sc.Opacity(rise)
		if !still {
			sc.Blur(2 * (1 - rise))
		}
	}
	tall := leafHeight(it)
	y := h.scroll.Y
	sc.Children(func() {
		ui.Box(c.Key("page")).Size(cardW, tall).Children(func() {
			for i, top := range leafTops(it) {
				// Only the screens near what shows are built.
				if top+cardH < y-cardH || top > y+2*cardH {
					continue
				}
				shot := leafShot(it, i)
				if shot == nil {
					ui.Box(c.Key(i)).Absolute().Left(0).Top(top).Size(cardW, cardH).Background(theme.Surface).AccessHidden(true)
					continue
				}
				img := ui.Image(c.Key(i), shot).Absolute().Left(0).Top(top).Size(cardW, cardH).Fit(ui.Cover).AccessHidden(true)
				// A screen read late comes in where it stands, out of a little blur.
				if pg := it.pages; pg != nil && i > 0 && !still {
					came, ok := pg.came[i]
					if !ok {
						came = c.Now()
						pg.came[i] = came
					}
					if e := motion.Ease(unit(float32(c.Now().Sub(came)) / float32(motion.SwapIn))); e < 1 && !c.Now().Before(came) {
						img.Opacity(e).Blur(2 * (1 - e))
						c.AnimationFrame()
					}
				}
			}
			if hasSpecimen(it) {
				tops := leafTops(it)
				a.specimen(c, it, tops[len(tops)-1]+cardH)
			}
		})
	})
}

// specimen is the page's foot in its card: its face at 64 with its letters and
// figures beside, then a strip of its own pictures, side by side at one height,
// all above the card's words.
func (a *App) specimen(c *ui.Context, it *packItem, top float32) {
	weight := it.TitleWeight
	if weight == 0 {
		weight = 400
	}
	ui.Column(c.Key("specimen")).Absolute().Left(0).Top(top).Size(cardW, cardH).Padding(specimenTop, 20, 0, 20).Gap(16).
		Background(theme.Window).AccessHidden(true).Children(func() {
		if it.font != "" {
			ui.Row(c.Key("glyphs")).Gap(18).AlignItems(ui.End).Children(func() {
				ui.Text(c, "Aa").Font(it.font).FontWeight(weight).FontSize(specimenAa).LineHeight(1).TextColor(theme.Text).NoWrap()
				ui.Column(c.Key("set")).Gap(1).Padding(0, 0, 6, 0).Shrink(1).MinWidth(0).ClipX().Children(func() {
					for _, l := range []string{"ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz", "0123456789 & ? ! @"} {
						ui.Text(c, l).Font(it.font).FontWeight(weight).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Text2).NoWrap()
					}
				})
			})
		}
		if it.pages == nil || len(it.pages.pics) == 0 {
			return
		}
		ui.Row(c.Key("strip")).Gap(6).Height(specimenStrip).ClipX().Children(func() {
			for i, p := range it.pages.pics {
				w := specimenStrip * min(max(p.aspect, 0.75), 1.8)
				ui.Image(c.Key(i), p.bmp).Size(w, specimenStrip).Fit(ui.Cover).Radius(4).Shrink(0).
					Shadow(0, 0, 0, 1, theme.CaptureEdge)
			}
		})
	})
}

// leafLine is the thin line at the card's right edge that tells where its page
// is scrolled: there while the card is pointed at (k), brighter while the page
// moves, and only when the page is longer than the card. It is dark under its
// light, so it shows on a light page and a dark one.
func (a *App) leafLine(c *ui.Context, it *packItem, k float32, now time.Time) {
	h := &a.capture
	tall := leafHeight(it)
	if tall <= cardH+1 {
		return
	}
	if h.scroll.Y != h.lastY {
		h.lastY, h.movedAt = h.scroll.Y, now
	}
	moving := now.Sub(h.movedAt) < motion.CaptureLineHold
	if moving {
		c.After(motion.CaptureLineHold - now.Sub(h.movedAt))
	}
	target := 0.55 * k
	if moving {
		target = 1
	}
	line := ui.Box(c.Key("line")).Absolute().Right(cardLineRight).Top(cardLineTop).Width(4).Height(cardLineSpan).IgnoreInput(true).AccessHidden(true)
	v := line.AnimateTo("v", target, a.ease(motion.Hover))
	if v < 0.01 {
		return
	}
	span := float32(cardLineSpan)
	length := max(cardLineMin, span*cardH/tall)
	at := float32(0)
	if most := tall - cardH; most > 0 {
		at = (span - length) * min(1, max(0, h.scroll.Y/most))
	}
	line.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(ui.Rect{X: r.X, Y: r.Y + at - 1, W: 4, H: length + 2}, theme.CaptureLineShade.Alpha(v), 2)
		p.Fill(ui.Rect{X: r.X + 1, Y: r.Y + at, W: 2, H: length}, theme.Text.Alpha(0.85*v), 1)
	})
}

// The line: how far in from the right and from the top, how long its span (it
// ends above the tools), and its least length.
const (
	cardLineRight = 4
	cardLineTop   = 12
	cardLineSpan  = cardH - 64
	cardLineMin   = 18
)
