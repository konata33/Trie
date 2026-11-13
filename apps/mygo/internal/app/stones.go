package app

import (
	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/theme"
)

// The article's headings as stepping stones across it. Read past, a heading goes on up with the text and stays at
// the top edge, at its own size, the section under foot, the text going on
// under it into the fog, until the next heading comes up under it and
// pushes it on and out. Everything follows the scroll, nothing runs on its
// own: scrolled back, it all goes back the same way.

// The stones' places, in the page from its top edge, or from under the bar
// while it shows.
const (
	stoneTop float32 = 28 // where the heading under foot stays
	stoneGap float32 = 16 // between a heading pushed on and the one pushing it
	fogPad   float32 = 10 // the fog is solid this far under the heading under foot
	fogFade  float32 = 28 // and thins out over this
)

// place is where a heading is in the page, as the text has it now.
type place struct {
	y, h float32
	ok   bool
}

// stoneView builds the stones of rp over its page.
func (a *App) stoneView(c *ui.Context, rp *readerPage) {
	r := rp.r
	if rp.work != nil || rp.content == nil {
		return
	}
	hs := rp.content.Headings
	col := min(a.area.W-80, r.Width)                    // as the column shows now, gliding to a new measure
	left, h := (a.area.W-col)/2+r.FlowShift(), a.area.H // flowing to a new measure, the column slides there
	// While the text reflows to a new size or measure its headings' places lag a frame behind it: the stones stay
	// where they were until it settles, the text going on under them.
	// So do they where the text has headings not laid out yet (no height: a body just built, or built anew), which
	// would all seem to be at its top; before any were laid out, there are none.
	ps := rp.stonePlaces
	if !r.Settling() || len(ps) != len(hs) {
		fresh, laid := make([]place, len(hs)), true
		for i, hd := range hs {
			y, hh, ok := r.AnchorBox(hd.ID)
			fresh[i] = place{y - r.Scroll.Y, hh, ok}
			laid = laid && (!ok || hh > 0)
		}
		switch {
		case laid:
			ps = fresh
		case len(ps) != len(hs):
			ps = make([]place, len(hs))
		}
		rp.stonePlaces = ps
	}
	// The bar, coming down, pushes the top edge down under it.
	shown := max(0, min(1, (53+rp.barY)/53)) * rp.barOp
	stick := stoneTop + 52*shown
	fogTop := 52 * shown // the bar's own ground covers what is above

	drawn := func(c *ui.Context, hd library.Heading) ui.Element {
		if e, ok := r.HeadingView(c, hd.ID); ok {
			return e
		}
		f := r.HeadingFont(hd.Level)
		return ui.Text(c, hd.Text).Font(f.Family).FontSize(f.Size).LineHeight(f.LineHeight).FontWeight(f.Weight).TextColor(theme.Text)
	}
	// fading is how much of a heading pushed on still shows: all of it at
	// its place, none once its foot has gone under the top edge.
	fading := func(y, hh float32) float32 { return max(0, min(1, (y+hh-fogTop)/(stick+hh-fogTop))) }

	// Under foot: the last heading whose top has come up to its place.
	foot := -1
	for i, p := range ps {
		if p.ok && p.y <= stick {
			foot = i
		}
	}
	rp.foot = foot
	next := foot + 1
	nextOK := next < len(hs) && ps[next].ok

	// The top edge: the heading under foot, pushed on by the next as it comes
	// up under it; the one before, going on and out ahead of it.
	var topDraw []func()
	fogBottom, fogK := stick, float32(0)
	if foot >= 0 {
		fh := ps[foot].h
		y := stick
		if nextOK {
			y = min(stick, ps[next].y-fh-stoneGap)
		}
		fogBottom, fogK = y+fh+fogPad, 1
		hd := hs[foot]
		topDraw = append(topDraw, func() {
			drawn(c.Key("stone-foot:"+hd.ID), hd).Absolute().Left(left).Top(y - fogTop).Width(col).Opacity(fading(y, fh)).IgnoreInput(true).AccessHidden(true)
		})
		if b := foot - 1; b >= 0 && ps[b].ok {
			py, ph := ps[foot].y-ps[b].h-stoneGap, ps[b].h
			if py+ph > 0 {
				hd := hs[b]
				topDraw = append(topDraw, func() {
					drawn(c.Key("stone-foot:"+hd.ID), hd).Absolute().Left(left).Top(py - fogTop).Width(col).Opacity(fading(py, ph)).IgnoreInput(true).AccessHidden(true)
				})
			}
		}
	} else if nextOK {
		// Before the first: the fog comes in as the first heading nears its place.
		fogBottom = stick + ps[next].h + fogPad
		fogK = max(0, min(1, (stick+40-ps[next].y)/40))
	}
	// The next heading coming up into the fog shows over it, where the text
	// has it, so that it never dims on its way to its place.
	if nextOK && fogK > 0 && ps[next].y < fogBottom+fogFade {
		hd, y := hs[next], ps[next].y
		topDraw = append(topDraw, func() {
			drawn(c.Key("stone-foot:"+hd.ID), hd).Absolute().Left(left).Top(y - fogTop).Width(col).IgnoreInput(true).AccessHidden(true)
		})
	}
	if fogK > 0 {
		fh := fogBottom - fogTop + fogFade
		ui.Box(c.Key("stone-fog-top")).Absolute().Left(left - 40).Width(col + 80).Top(fogTop).Height(fh).Opacity(fogK).IgnoreInput(true).AccessHidden(true).
			LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{{Color: theme.Window, At: 0},
				{Color: theme.Window, At: (fogBottom - fogTop) / fh}, {Color: theme.Window.Alpha(0), At: 1}}})
	}
	// Under the bar while it shows: what goes on and out goes under it.
	ui.Box(c.Key("stone-top")).Absolute().Left(0).Top(fogTop).Width(a.area.W).Height(max(0, h-fogTop)).ClipX().ClipY().
		IgnoreInput(true).AccessHidden(true).Children(func() {
		for _, d := range topDraw {
			d()
		}
	})
}
