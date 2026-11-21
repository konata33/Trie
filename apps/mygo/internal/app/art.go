package app

import (
	"embed"
	"math"

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
