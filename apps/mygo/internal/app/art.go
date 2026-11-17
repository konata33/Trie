package app

import (
	"embed"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"trie/internal/locales"
	"trie/internal/motion"
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

// glyph is a drawing in an ink and one fill, each in the colour its
// element's text is given.
type glyph struct{ ink, accent *ui.SVG }

// doorArt are Explore's doors, a poster for each kind
// (art/door-<kind>.svg, 1024 by 1280): an ink and one fill, as an icon's.
var doorArt = sync.OnceValue(func() map[string]glyph {
	m := map[string]glyph{}
	for _, k := range kindOrder {
		m[k] = glyph{ink: artSVG("door-" + k), accent: artSVG("door-" + k + "-accent")}
	}
	return m
})

// loading is the loading mark (DESIGN.md 读取记号): refresh's trail without
// its arrowhead, in the colour of the text around it, turning clockwise
// 1.1 s a turn; under less motion it holds still and breathes.
func (a *App) loading(c *ui.Context, size float32) ui.Element {
	cut, _ := dotCut("loading", size)
	ic := ui.Icon(c, dotArt()[dotKey{"loading", cut}].ink).Size(size, size).Shrink(0).Role(ui.RoleProgress).Label(locales.CommonLoading())
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
