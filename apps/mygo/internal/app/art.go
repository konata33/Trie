package app

import (
	"embed"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/motion"
	"trie/internal/theme"
)

// The app's drawings in art/, recorded from the previous implementation by
// .context/mygo/app-v1/tools/svgs.py.
//
//go:embed art
var artFiles embed.FS

func artSVG(name string) *ui.SVG {
	data, err := artFiles.ReadFile("art/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	return ui.MustParseSVG(data)
}

// glyph is one of the app's icons (DESIGN.md Icons): an ink layer in the
// text's colour and at most one accent shape, faint at rest and amber lit.
type glyph struct{ ink, accent *ui.SVG }

var glyphs = sync.OnceValue(func() map[string]glyph {
	m := map[string]glyph{}
	for _, name := range []string{"home", "explore", "clips", "favorites", "add", "minus", "refresh", "search", "close", "back", "forward",
		"openOriginal", "openArrow", "more", "check", "brokenImage", "play", "trash", "copy", "textSize", "time"} {
		g := glyph{ink: artSVG("icon-" + name)}
		if _, err := artFiles.Open("art/icon-" + name + "-accent.svg"); err == nil {
			g.accent = artSVG("icon-" + name + "-accent")
		}
		m[name] = g
	}
	return m
})

// icon builds icon name at size, its accent lit amber when lit: the accent
// eases over 250 ms, at once after a key.
func (a *App) icon(c *ui.Context, name string, size float32, lit bool) ui.Element {
	g := glyphs()[name]
	box := ui.Box(c).Size(size, size).Shrink(0)
	target := theme.IconRest
	if lit {
		target = theme.Accent
	}
	accent := target
	if g.accent != nil {
		accent = box.AnimateColor("accent", target, a.ease(motion.State))
	}
	return box.Children(func() {
		if g.accent != nil {
			ui.Icon(c, g.accent).Absolute().Left(0).Top(0).Size(size, size).TextColor(accent)
		}
		ui.Icon(c, g.ink).Absolute().Left(0).Top(0).Size(size, size)
	})
}

// The pictograms (DESIGN.md Illustration): 168 wide, grey ink and one amber
// part, boiling at 12 fps through three outlines, each the drawing displaced
// by the noise of an SVG filter (feTurbulence fractalNoise,
// baseFrequency 0.035, seeds 11, 29 and 47; feDisplacementMap scale 2.8),
// applied to the outline's points rather than its pixels. Loading plays its
// four drawings over a second. Less motion shows the first drawing still.
type pictogram struct {
	still []*ui.SVG    // the drawings as drawn
	boil  [][3]*ui.SVG // each drawing in the three outlines
}

var pictograms = sync.OnceValue(func() map[string]*pictogram {
	m := map[string]*pictogram{}
	for _, name := range []string{"favorites", "clips", "mailbox", "unreachable", "broken", "picture", "loading"} {
		p := &pictogram{}
		files := []string{"pictogram-" + name}
		if name == "loading" {
			files = []string{"pictogram-loading-1", "pictogram-loading-2", "pictogram-loading-3", "pictogram-loading-4"}
		}
		for _, f := range files {
			data, err := artFiles.ReadFile("art/" + f + ".svg")
			if err != nil {
				panic(err)
			}
			p.still = append(p.still, ui.MustParseSVG(data))
			var frames [3]*ui.SVG
			for i, seed := range [3]int{11, 29, 47} {
				frames[i] = ui.MustParseSVG([]byte(boiled(string(data), seed)))
			}
			p.boil = append(p.boil, frames)
		}
		m[name] = p
	}
	return m
})

// boilTick is the line boil's frame, 12 fps.
const boilTick = motion.Boil

// pictogram builds pictogram name, 168 wide, boiling while it shows.
func (a *App) pictogram(c *ui.Context, name string) ui.Element {
	p := pictograms()[name]
	box := ui.Box(c).Width(168).AspectRatio(200.0 / 160).Shrink(0).AccessHidden(true)
	if a.still(c) {
		return box.Children(func() { ui.Image(c, p.still[0]).Fill() })
	}
	now := c.Now()
	tick := int(now.UnixNano() / int64(boilTick))
	drawing := 0
	if len(p.boil) > 1 {
		drawing = (tick / 3) % len(p.boil)
	}
	svg := p.boil[drawing][tick%3]
	if box.InView() {
		c.After(boilTick - time.Duration(now.UnixNano()%int64(boilTick)))
	}
	return box.Children(func() { ui.Image(c, svg).Fill() })
}

// The loading mark: one stroke going round, 1.1 s a
// turn; under less motion it holds still and breathes.
var loadingMark = sync.OnceValue(func() *ui.SVG { return artSVG("loading-mark") })

func (a *App) loading(c *ui.Context, size float32) ui.Element {
	ic := ui.Icon(c, loadingMark()).Size(size, size).Shrink(0).Role(ui.RoleProgress).Label("加载中")
	if a.still(c) {
		return ic.Opacity(0.35 + 0.65*breathe(ic.Loop("breathe", 2*motion.LoadingBreath, ui.Linear)))
	}
	return ic.Rotate(ic.Loop("turn", motion.LoadingTurn, ui.Linear) * 360)
}

// breathe is a slow rise and fall over a loop: 0 → 1 → 0, eased.
func breathe(t float32) float32 { return float32(0.5 - 0.5*math.Cos(2*math.Pi*float64(t))) }

// The button loops (一笔圈出): short, middle and long, in the
// colour of the text, stretched to the button.
var inkLoops = sync.OnceValue(func() [3]*ui.SVG {
	var out [3]*ui.SVG
	for i := range out {
		data, err := artFiles.ReadFile(fmt.Sprintf("art/ink-loop-%d.svg", i+1))
		if err != nil {
			panic(err)
		}
		out[i] = ui.MustParseSVG([]byte(strings.Replace(string(data), "<g ", `<g fill="currentColor" `, 1)))
	}
	return out
})

// boiled is svg, a pictogram of potrace paths under two transforms each,
// with every point moved by the displacement noise of seed, in the
// viewBox's units.
func boiled(svg string, seed int) string {
	t := newTurbulence(seed)
	var out strings.Builder
	out.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 160" width="168" height="134.4">`)
	for _, g := range groupPattern.FindAllStringSubmatch(svg, -1) {
		at, fill, flip, body := parseMatrix(g[1]), g[2], parseFlip(g[3]), g[4]
		place := func(x, y float64) (float64, float64) {
			x, y = flip[0]+flip[2]*x, flip[1]+flip[3]*y
			x, y = at[0]*x+at[2]*y+at[4], at[1]*x+at[3]*y+at[5]
			dx, dy := t.at(x, y)
			// feDisplacementMap shows each pixel from where the map points:
			// the outline moves the other way, by scale·(channel − ½).
			return x - 2.8*(dx-0.5), y - 2.8*(dy-0.5)
		}
		fmt.Fprintf(&out, `<g fill="%s">`, fill)
		for _, d := range pathPattern.FindAllStringSubmatch(body, -1) {
			out.WriteString(`<path d="`)
			out.WriteString(placePath(d[1], place))
			out.WriteString(`"/>`)
		}
		out.WriteString(`</g>`)
	}
	out.WriteString(`</svg>`)
	return out.String()
}

var (
	groupPattern = regexp.MustCompile(`<g transform="([^"]*)" fill="([^"]*)"><g transform="([^"]*)">(.*?)</g></g>`)
	pathPattern  = regexp.MustCompile(`<path d="([^"]*)"/>`)
	numPattern   = regexp.MustCompile(`-?[\d.]+(?:e-?\d+)?`)
	tokPattern   = regexp.MustCompile(`[MmCcLlZz]|-?[\d.]+(?:e-?\d+)?`)
)

func parseMatrix(s string) [6]float64 {
	var m [6]float64
	for i, n := range numPattern.FindAllString(s, 6) {
		m[i], _ = strconv.ParseFloat(n, 64)
	}
	return m
}

// parseFlip reads potrace's "translate(tx,ty) scale(sx,sy)" as tx, ty, sx, sy.
func parseFlip(s string) [4]float64 {
	var f [4]float64
	for i, n := range numPattern.FindAllString(s, 4) {
		f[i], _ = strconv.ParseFloat(n, 64)
	}
	return f
}

// placePath rewrites path data of M, C, L and Z, absolute or relative, as
// absolute M, C and L through place.
func placePath(d string, place func(x, y float64) (float64, float64)) string {
	toks := tokPattern.FindAllString(d, -1)
	var b strings.Builder
	var cmd byte
	var x, y, sx, sy float64
	num := func(i *int) float64 {
		v, _ := strconv.ParseFloat(toks[*i], 64)
		*i++
		return v
	}
	put := func(px, py float64) {
		qx, qy := place(px, py)
		fmt.Fprintf(&b, " %.2f %.2f", qx, qy)
	}
	for i := 0; i < len(toks); {
		if c := toks[i][0]; strings.IndexByte("MmCcLlZz", c) >= 0 && len(toks[i]) == 1 {
			cmd = c
			i++
			if cmd == 'Z' || cmd == 'z' {
				b.WriteString(" Z")
				x, y = sx, sy
				continue
			}
		}
		switch cmd {
		case 'M', 'm':
			nx, ny := num(&i), num(&i)
			if cmd == 'm' {
				nx, ny = x+nx, y+ny
			}
			x, y, sx, sy = nx, ny, nx, ny
			b.WriteString(" M")
			put(x, y)
			cmd = map[byte]byte{'M': 'L', 'm': 'l'}[cmd] // pairs after a move are lines
		case 'C', 'c':
			var p [6]float64
			for k := range p {
				p[k] = num(&i)
			}
			if cmd == 'c' {
				for k := 0; k < 6; k += 2 {
					p[k], p[k+1] = x+p[k], y+p[k+1]
				}
			}
			b.WriteString(" C")
			put(p[0], p[1])
			put(p[2], p[3])
			put(p[4], p[5])
			x, y = p[4], p[5]
		case 'L', 'l':
			nx, ny := num(&i), num(&i)
			if cmd == 'l' {
				nx, ny = x+nx, y+ny
			}
			x, y = nx, ny
			b.WriteString(" L")
			put(x, y)
		default:
			i++ // not a path this writes
		}
	}
	return strings.TrimSpace(b.String())
}

// turbulence is SVG's feTurbulence fractalNoise with one octave, as the
// filter spec's reference code computes it, for the red and green channels.
type turbulence struct {
	lattice [bSize + bSize + 2]int
	grad    [4][bSize + bSize + 2][2]float64
}

const (
	bSize   = 0x100
	bMask   = 0xff
	perlinN = 0x1000
	freq    = 0.035
)

func newTurbulence(seed int) *turbulence {
	t := &turbulence{}
	const (
		ra = 16807
		rm = 2147483647
		rq = 127773
		rr = 2836
	)
	s := int64(seed)
	if s <= 0 {
		s = -(s % (rm - 1)) + 1
	}
	if s > rm-1 {
		s = rm - 1
	}
	random := func() int64 {
		s = ra*(s%rq) - rr*(s/rq)
		if s <= 0 {
			s += rm
		}
		return s
	}
	for k := 0; k < 4; k++ {
		for i := 0; i < bSize; i++ {
			t.lattice[i] = i
			for j := 0; j < 2; j++ {
				t.grad[k][i][j] = float64((random()%(bSize+bSize))-bSize) / bSize
			}
			g := &t.grad[k][i]
			n := math.Sqrt(g[0]*g[0] + g[1]*g[1])
			if n != 0 {
				g[0], g[1] = g[0]/n, g[1]/n
			}
		}
	}
	for i := bSize - 1; i > 0; i-- {
		k := t.lattice[i]
		j := int(random() % bSize)
		t.lattice[i], t.lattice[j] = t.lattice[j], k
	}
	for i := 0; i < bSize+2; i++ {
		t.lattice[bSize+i] = t.lattice[i]
		for k := 0; k < 4; k++ {
			t.grad[k][bSize+i] = t.grad[k][i]
		}
	}
	return t
}

func sCurve(t float64) float64     { return t * t * (3 - 2*t) }
func lerp(t, a, b float64) float64 { return a + t*(b-a) }
func (t *turbulence) noise(k int, x, y float64) float64 {
	tx := x + perlinN
	bx0 := int(tx) & bMask
	bx1 := (bx0 + 1) & bMask
	rx0 := tx - float64(int(tx))
	rx1 := rx0 - 1
	ty := y + perlinN
	by0 := int(ty) & bMask
	by1 := (by0 + 1) & bMask
	ry0 := ty - float64(int(ty))
	ry1 := ry0 - 1
	i, j := t.lattice[bx0], t.lattice[bx1]
	b00, b10, b01, b11 := t.lattice[i+by0], t.lattice[j+by0], t.lattice[i+by1], t.lattice[j+by1]
	sx, sy := sCurve(rx0), sCurve(ry0)
	q := t.grad[k][b00]
	u := rx0*q[0] + ry0*q[1]
	q = t.grad[k][b10]
	v := rx1*q[0] + ry0*q[1]
	a := lerp(sx, u, v)
	q = t.grad[k][b01]
	u = rx0*q[0] + ry1*q[1]
	q = t.grad[k][b11]
	v = rx1*q[0] + ry1*q[1]
	return lerp(sy, a, lerp(sx, u, v))
}

// at returns the red and green channels of the noise at (x, y), 0 to 1.
func (t *turbulence) at(x, y float64) (r, g float64) {
	ch := func(k int) float64 { return (t.noise(k, x*freq, y*freq)*255 + 255) / 2 / 255 }
	return ch(0), ch(1)
}
