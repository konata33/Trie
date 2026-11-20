package app

import (
	"cmp"
	"fmt"
	"image"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/theme"
)

// 收藏's first page is a map of what is kept, one screen tall: a block for
// each kind the designs kept are shelved under, for each gallery whose
// designs are shelved under none, and one for the articles, edge to edge,
// each as large as what it holds is many. A block is tiled with what it
// holds, its designs' pictures or its writers' sheets, and says what it is
// and how many in a tab cut into its corner. Choosing one zooms into it
// where it stands: its pictures grow out of their tiles into rows at their
// own proportions, and the rest of the map folds into a strip at the left,
// the same map smaller, where another block is chosen or the map opened
// again.

const (
	// A block's tiles all but touch and the blocks stand well apart, so that each kind reads as one piece at a
	// glance: the gap between blocks is six times the gap within one.
	mapGap     = 12  // between blocks
	mapTileGap = 2   // between the tiles of a block
	mapRailGap = 6   // between blocks in the strip
	mapRail    = 80  // the strip the map folds into
	mapRow     = 280 // a zoomed block's rows, about
	mapRowGap  = 8
	wallGap    = 16  // between columns of writers' sheets
	wallColumn = 240 // the narrowest a column of sheets is
	paperRest  = 3   // titles under the newest on a writer's sheet
	articles   = "articles"
	captures   = "captures" // the block of pages captured (PAGES.md 拓本)
)

// mapBlock is one block of the map: a kind's designs or a gallery's (g),
// the articles, by writer, or the pages captured (packs).
type mapBlock struct {
	key     string
	g       *favGroup
	writers []*favGroup
	packs   []*packItem
	n       int
}

// mapMatch is a block's identity across the map and its strip; tileMatch a
// keep's in a block, across its tile and its place zoomed in.
type (
	mapMatch  struct{ key string }
	tileMatch struct{ block, key string }
)

// mapBlocks are the map's blocks in an order that holds as keeps come and
// go: the kinds in theirs, the galleries shelved under none, the articles.
func mapBlocks(groups []*favGroup) []mapBlock {
	var out []mapBlock
	for _, k := range kindOrder {
		if i := slices.IndexFunc(groups, func(g *favGroup) bool { return g.kind == k }); i >= 0 {
			out = append(out, mapBlock{key: groups[i].src.ID, g: groups[i], n: len(groups[i].list)})
		}
	}
	words := mapBlock{key: articles}
	for _, g := range groups {
		switch {
		case g.kind != "":
		case g.design:
			out = append(out, mapBlock{key: g.src.ID, g: g, n: len(g.list)})
		default:
			words.writers = append(words.writers, g)
			words.n += len(g.list)
		}
	}
	if words.n > 0 {
		out = append(out, words)
	}
	return out
}

// blocks are the map's blocks as query finds them: mapBlocks, then the
// pages captured.
func (a *App) blocks(groups []*favGroup, query string) []mapBlock {
	out := mapBlocks(groups)
	if ps := a.shelved(query); len(ps) > 0 {
		out = append(out, mapBlock{key: captures, packs: ps, n: len(ps)})
	}
	return out
}

// shelved are the pages captured that have come into 收藏, newest first,
// those whose name or address hold what is typed.
func (a *App) shelved(query string) []*packItem {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []*packItem
	for _, it := range a.capture.items {
		if it.state != packTaken || !it.filed || it.gone {
			continue
		}
		if q == "" || strings.Contains(strings.ToLower(it.Name), q) || strings.Contains(strings.ToLower(it.URL), q) {
			out = append(out, it)
		}
	}
	return out
}

// blockName is what a block is: its kind, its gallery, the articles or the
// pages captured.
func blockName(b mapBlock) string {
	switch {
	case b.packs != nil:
		return locales.FavoritesCaptures()
	case b.g == nil:
		return locales.FavoritesArticles()
	case b.g.kind != "":
		return kindName(b.g.kind)
	}
	return nameOf(b.g.src)
}

// kindGlyph is the icon of a kind, or of the articles.
func kindGlyph(k string) string { return "kind-" + k }

// mapRects lays the blocks over r, gap apart, each as large as what it
// holds is many, though never under half an even share.
func mapRects(blocks []mapBlock, r ui.Rect, gap float32) []ui.Rect {
	total := 0
	for _, b := range blocks {
		total += b.n
	}
	ws := make([]float32, len(blocks))
	for i, b := range blocks {
		ws[i] = max(float32(b.n), float32(total)/float32(2*len(blocks)))
	}
	out := make([]ui.Rect, len(blocks))
	cut(ws, r, gap, 1, out)
	return out
}

// cut lays weights ws over r in their order, gap apart: r is cut where the
// run's weight halves, across its width if it is wide times as wide as
// tall (1, its longer side; more, to keep parts wide, as lines of words
// want), and each part laid over its share.
func cut(ws []float32, r ui.Rect, gap, wide float32, out []ui.Rect) {
	switch len(ws) {
	case 0:
		return
	case 1:
		out[0] = r
		return
	}
	var total float32
	for _, w := range ws {
		total += w
	}
	k, head, best := 1, float32(0), math.Inf(1)
	var acc float32
	for i := 1; i < len(ws); i++ {
		acc += ws[i-1]
		if d := math.Abs(float64(2*acc - total)); d < best {
			k, head, best = i, acc, d
		}
	}
	f := head / total
	a, b := r, r
	if r.W >= wide*r.H {
		a.W = (r.W - gap) * f
		b.X, b.W = r.X+a.W+gap, r.W-a.W-gap
	} else {
		a.H = (r.H - gap) * f
		b.Y, b.H = r.Y+a.H+gap, r.H-a.H-gap
	}
	cut(ws[:k], a, gap, wide, out[:k])
	cut(ws[k:], b, gap, wide, out[k:])
}

// mosaic is how n tiles cover a block w×h, gap apart: in the columns that
// bring them nearest 3:2, shared out over the rows as evenly as they go,
// each row's widened to fill it.
func mosaic(n int, w, h, gap float32) []ui.Rect {
	// ponytail: past what fits 20 wide and 14 tall, the rest go unshown (the tab still counts them); a block that
	// many deep wants its own zoom.
	n = min(n, max(1, int((w+gap)/(20+gap))*int((h+gap)/(14+gap))))
	cols, best := 1, math.Inf(1)
	for c := 1; c <= n; c++ {
		rows := (n + c - 1) / c
		cw, ch := (w-gap*float32(c-1))/float32(c), (h-gap*float32(rows-1))/float32(rows)
		if d := math.Abs(math.Log(float64(cw / ch / 1.5))); d < best {
			cols, best = c, d
		}
	}
	rows := (n + cols - 1) / cols
	ch := (h - gap*float32(rows-1)) / float32(rows)
	out := make([]ui.Rect, 0, n)
	for row := range rows {
		in := n / rows
		if row < n%rows {
			in++
		}
		cw := (w - gap*float32(in-1)) / float32(in)
		for col := range in {
			out = append(out, ui.Rect{X: float32(col) * (cw + gap), Y: float32(row) * (ch + gap), W: cw, H: ch})
		}
	}
	return out
}

// mapRows lays pictures of proportions rs in rows w wide, gap apart, each
// row as tall as fills it from about h; a last row short of filling stays h.
func mapRows(rs []float32, w, h, gap float32) []ui.Rect {
	var out []ui.Rect
	var y float32
	for i := 0; i < len(rs); {
		j, sum := i, float32(0)
		for j < len(rs) {
			sum += rs[j]
			j++
			if sum*h+gap*float32(j-i-1) >= w {
				break
			}
		}
		rh := min(h, (w-gap*float32(j-i-1))/sum)
		x := float32(0)
		for k := i; k < j; k++ {
			out = append(out, ui.Rect{X: x, Y: y, W: rs[k] * rh, H: rh})
			x += rs[k]*rh + gap
		}
		y += rh + gap
		i = j
	}
	return out
}

// zoomTo zooms the map into block k, or ("") out to the whole of it. From
// the keys, the focus goes to the first keep in, or back to the block.
func (a *App) zoomTo(c *ui.Context, k string) {
	fs := a.favs()
	if fs.kind == k {
		return
	}
	if a.kbd {
		fs.focus = favFirst
		if k == "" {
			fs.focus = "m:" + fs.kind
		}
	}
	fs.kind, fs.turned = k, c.Now()
	fs.shelves.ScrollTo(0, 0, ui.Animation{})
}

// mapTr is how a block or a keep moves: zooming, it flies from where the
// other side showed it (a tile growing into its place, a block folding into
// the strip); otherwise as keeps come and go (listMove), still matched so
// that the next zoom finds it.
func (a *App) mapTr(c *ui.Context, match any, quiet bool) ui.ElementTransition {
	fs := a.favs()
	if match != nil && c.Now().Sub(fs.turned) < crossingTime {
		d := motion.Deep
		if fs.kind == "" {
			d = motion.Back
		}
		return a.tr(c, d.Crossing().Shared(match))
	}
	t := a.listMove(c, quiet)
	t.Match = match
	return t
}

// favMap is 收藏's first page: the map, h tall, or the block zoomed into
// beside the strip.
func (a *App) favMap(c *ui.Context, p page, groups []*favGroup, h float32, quiet bool, stop string, live bool) {
	fs := a.favs()
	blocks := a.blocks(groups, fs.query)
	w := min(a.area.W, 1200) - 64
	if fs.kind == "" {
		rects := mapRects(blocks, ui.Rect{W: w, H: h}, mapGap)
		ui.Box(c.Key("map")).Width(w).Height(h).Margin(0, 0, 32, 0).Role(ui.RoleList).Label(locales.TabFavorites()).Children(func() {
			for i, b := range blocks {
				a.mapBlock(c, b, rects[i], false, quiet, stop, live)
			}
		})
		return
	}
	in := blocks[slices.IndexFunc(blocks, func(b mapBlock) bool { return b.key == fs.kind })]
	ui.Row(c.Key("zoom")).FillWidth().Gap(24).AlignItems(ui.Start).Padding(0, 0, 64, 0).Children(func() {
		rects := mapRects(blocks, ui.Rect{W: mapRail, H: h}, mapRailGap)
		ui.Box(c.Key("rail")).Width(mapRail).Height(h).Shrink(0).Sticky().Top(24).Role(ui.RoleList).Label(locales.TabFavorites()).Children(func() {
			for i, b := range blocks {
				a.mapBlock(c, b, rects[i], true, quiet, stop, live)
			}
		})
		mw := w - mapRail - 24
		ui.Column(c.Key("in:" + in.key)).Width(mw).Shrink(0).Children(func() {
			switch {
			case in.packs != nil:
				a.mapPacks(c, in, mw, h, quiet, stop, live)
			case in.g == nil:
				a.mapPapers(c, p, in, mw, quiet, stop, live)
			default:
				a.mapPictures(c, p, in, mw, h, quiet, stop, live)
			}
		})
	})
}

// mapBlock is a block of the map (or, mini, of the strip): tiled with what
// it holds, its tab in its corner. Choosing it zooms in; in the strip,
// choosing the block zoomed into opens the map again.
func (a *App) mapBlock(c *ui.Context, b mapBlock, r ui.Rect, mini, quiet bool, stop string, live bool) {
	fs := a.favs()
	key := "m:" + b.key
	in := fs.kind == b.key
	label := locales.FavoritesWorkCount(blockName(b), b.n)
	switch {
	case b.packs != nil:
		label = locales.FavoritesCaptureCount(blockName(b), b.n)
	case b.g == nil:
		label = locales.FavoritesWriterCount(blockName(b), b.n)
	}
	radius := float32(12)
	if mini {
		radius = 6
	}
	// Its top-left corner is square: the tab covers it, and a round one would show its picture's rim around it.
	btn := ui.ButtonBase(c.Key(key)).Label(label).Absolute().Left(r.X).Top(r.Y).Size(r.W, r.H).Radius(0, radius, radius, radius).ClipX().ClipY().
		FocusRing(false).KeyShortcuts(0, ui.KeyEnter)
	var match any = mapMatch{b.key}
	if mini && in {
		// Its tiles grew into the page: the strip's own comes in as it does.
		match = nil
	}
	btn.Transition(a.mapTr(c, match, quiet))
	if mini {
		btn.TabStop(false)
	} else {
		a.card(btn, key, stop, live)
	}
	if btn.Clicked() && live {
		if mini && in {
			a.zoomTo(c, "")
		} else {
			a.zoomTo(c, b.key)
		}
	}
	focus := btn.FocusVisible()
	if focus {
		btn.FocusRing(true).FocusRingOffset(2)
	}
	lit := btn.Hovered() || focus || mini && in
	if mini {
		rest := float32(0.4)
		if lit {
			rest = 1
		}
		btn.Opacity(btn.AnimateTo("rest", rest, a.ease(motion.Hover)))
	} else {
		a.give(btn, 0.99)
	}
	if b.g != nil && !mini {
		ctx := fs.ctx == key
		menu := ui.ContextMenuBase(c, btn, &ctx)
		a.shelfMenu(c, menu, b.g, true)
		fs.ctx = keepOpen(fs.ctx, key, ctx)
	}
	a.tip(c, btn, blockName(b), mini)
	btn.Children(func() {
		switch {
		case b.packs != nil:
			for i, t := range mosaic(len(b.packs), r.W, r.H, mapTileGap) {
				it := b.packs[i]
				a.mapTile(c, b.key, "p:"+it.ID, it.Name, a.packPic(it), t, lit, mini, quiet)
			}
		case b.g == nil:
			a.mapWriters(c, b, r, mini, quiet)
		default:
			for i, t := range mosaic(len(b.g.list), r.W, r.H, mapTileGap) {
				art := b.g.list[i]
				a.mapTile(c, b.key, "k:"+art.ID, titleOf(art.Title), a.wallPic(art.ID), t, lit, mini, quiet)
			}
		}
		a.mapTab(c, b, mini, lit)
		if mini && in {
			ui.Box(c.Key("in")).Absolute().Fill().Radius(radius).IgnoreInput(true).InsetShadow(0, 0, 0, 1, theme.EdgeHover)
		}
	})
}

// mapTab is a block's tab, cut into its top-left corner in the window's
// ground, as the gap between blocks turning in: its kind's glyph, or its
// gallery's face, and how many it holds. The block's edges turn round into
// it at either side as its own corner turns out, so that its outline runs
// on around the tab rather than stopping at a patch laid over it.
func (a *App) mapTab(c *ui.Context, b mapBlock, mini, lit bool) {
	r, size := float32(12), float32(28)
	tab := ui.Row(c.Key("tab")).Absolute().Left(0).Top(0).Height(44).Gap(10).Padding(0, 16, 0, 12).AlignItems(ui.Center)
	if mini {
		r, size = 6, 16
		tab.Height(24).Gap(0).Padding(0, 4, 0, 4)
	}
	tab.Background(theme.Window).Radius(0, 0, r, 0).ZIndex(2).IgnoreInput(true).AccessHidden(true)
	color := theme.Text2
	if lit {
		color = theme.Text
	}
	tab.TextColor(tab.AnimateColor("fg", color, a.ease(motion.Hover))).Children(func() {
		switch {
		case b.packs != nil:
			a.icon(c, "capture", size, lit)
		case b.g == nil:
			a.icon(c, kindGlyph(articles), size, lit)
		case b.g.kind != "":
			a.icon(c, kindGlyph(b.g.kind), size, lit)
		default:
			a.face(c, a.iconOf(b.g.src), "face", size)
		}
		if !mini {
			ui.Text(c, fmt.Sprint(b.n)).FontSize(theme.Item).LineHeight(20.0 / 15).FontWeight(500).FontFeatures(theme.Numbers)
		}
		// The ground outside a quarter circle, r across, at the foot of either side.
		fillet := func(key string) ui.Element {
			return ui.Box(c.Key(key)).Absolute().Size(r, r).RadialGradient(ui.RadialGradient{Center: ui.Point{X: 1, Y: 1}, Radius: r,
				Stops: []ui.GradientStop{{Color: theme.Window.Alpha(0), At: 0}, {Color: theme.Window.Alpha(0), At: (r - 0.75) / r}, {Color: theme.Window, At: 1}}})
		}
		fillet("along").Right(-r).Top(0)
		fillet("down").Left(0).Bottom(-r)
	})
}

// mapTile is a design or a page captured on a block, key: its picture pic
// filling its tile, dimmed as pictures are on the wall of a page, whole while
// its block is pointed at. One with no picture to be had shows its title.
func (a *App) mapTile(c *ui.Context, block, key, title string, pic *favPic, r ui.Rect, lit, mini, quiet bool) {
	t := ui.Box(c.Key(key)).Absolute().Left(r.X).Top(r.Y).Size(r.W, r.H).ClipX().ClipY().Background(theme.Surface).IgnoreInput(true)
	if !mini {
		t.Transition(a.mapTr(c, tileMatch{block, key}, quiet))
	}
	t.Children(func() {
		switch {
		case pic == nil:
			a.breathing(c, theme.Hover)
		case pic.bmp != nil:
			img := ui.Image(c, pic.bmp).Fit(ui.Cover).ObjectPosition(0.5, 0).Absolute().Fill()
			if fade := a.picFade(c, pic); fade < 1 {
				img.Opacity(fade)
			}
			bright := 1 - (1-pic.rest)*0.4
			if lit {
				bright = 1
			}
			if v := 1 - t.AnimateTo("bright", bright, a.ease(motion.Lean)); v > 0.001 {
				ui.Box(c).Absolute().Fill().Background(theme.Darken).Opacity(v)
			}
		case !pic.done:
			a.breathing(c, theme.Hover)
		case !mini && r.W >= 72:
			ui.Text(c, title).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).MaxLines(3).Ellipsis("…").
				Absolute().Left(10).Right(10).Bottom(8)
		}
	})
}

// mapWriters are the articles' block: each writer a sheet, as large as
// they are kept from often, their face and name over their titles in the
// articles' own face; in the strip, their face.
func (a *App) mapWriters(c *ui.Context, b mapBlock, r ui.Rect, mini, quiet bool) {
	ws := make([]float32, len(b.writers))
	for i, g := range b.writers {
		ws[i] = float32(len(g.list))
	}
	rs := make([]ui.Rect, len(ws))
	cut(ws, ui.Rect{W: r.W, H: r.H}, mapTileGap, 2.5, rs)
	for i, g := range b.writers {
		s := rs[i]
		sheet := ui.Column(c.Key(g.src.ID)).Absolute().Left(s.X).Top(s.Y).Size(s.W, s.H).Background(theme.Surface).ClipX().ClipY().IgnoreInput(true)
		if mini {
			// Clear of the tab: under it, or beside it if the sheet is too short to be read under it.
			top, left := float32(6), float32(6)
			if s.X == 0 && s.Y == 0 {
				if s.H >= 80 {
					top = 28
				} else {
					left = 30
				}
			}
			sheet.Padding(top, 6, 6, left).Gap(3).AlignItems(ui.Start).Children(func() { a.mapGreek(c, g, s.W-6-left, s.H-top-6) })
			continue
		}
		sheet.Transition(a.mapTr(c, tileMatch{articles, "a:" + g.src.ID}, quiet))
		// The sheet under the tab sets its face and name beside the tab, centred on it (44 tall, 12 + 28 + 10 +
		// its count + 16 wide), and its titles from under it.
		top, under, beside := float32(16), float32(2), float32(0)
		if s.X == 0 && s.Y == 0 {
			top, under, beside = 14, 14, 62+9*float32(len(fmt.Sprint(b.n)))
		}
		// A narrow sheet sets its newest title a size down, as the rest are, and too narrow for words to wrap
		// whole, on one line.
		lead, lines := float32(theme.Subtitle), 2
		if s.W < 240 {
			lead = theme.Item
		}
		if s.W < 160 {
			lines = 1
		}
		sheet.Padding(top, 16, 16, 16).Gap(10).Children(func() {
			ui.Row(c.Key("by")).Gap(8).AlignItems(ui.Center).Margin(0, 0, under, beside).Children(func() {
				a.face(c, a.iconOf(g.src), "face", 16)
				ui.Text(c, nameOf(g.src)).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Text2).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			})
			for j, art := range g.list {
				t := titleOf(art.Title)
				if j > 0 {
					ui.Text(c.Key(art.ID), t).Font(reader.TitleFont(t)).FontSize(theme.Item).LineHeight(22.0 / 15).FontWeight(500).TextColor(theme.Text2).
						SingleLine().Ellipsis("…")
					continue
				}
				ui.Text(c.Key(art.ID), t).Font(reader.TitleFont(t)).FontSize(lead).LineHeight(1.4).FontWeight(600).TextColor(theme.Text).
					MaxLines(lines).Ellipsis("…")
				// Under a writer kept from once or twice, how their newest opens, as many lines as the sheet
				// holds over the rest: their face's line, the title's two and the other title's.
				if paras := a.sheetOpening(art); len(g.list) <= 2 && s.W >= 240 && len(paras) > 0 {
					if lines := int((s.H - top - 16 - 24 - under - 2*lead*1.4 - 10 - float32(len(g.list)-1)*32) / 24); lines > 0 {
						t := strings.Join(paras, "\n")
						family, scale := reader.PassageFace(t)
						ui.Text(c.Key("opening"), t).Font(family).FontSize(14*scale).LineHeight(24/(14*scale)).TextColor(theme.Text2).
							MaxLines(lines).Ellipsis("…").Margin(0, 0, 6, 0)
					}
				}
			}
			// What the sheet cannot hold fades into it rather than being cut.
			ui.Box(c.Key("fade")).Absolute().Left(0).Right(0).Bottom(0).Height(32).LinearGradient(ui.LinearGradient{Angle: 180,
				Stops: []ui.GradientStop{{Color: theme.Surface.Alpha(0), At: 0}, {Color: theme.Surface, At: 1}}})
		})
	}
}

// mapGreek is a writer's sheet in the strip, too small for words: their
// face and a bar for their name, then each title a bar as long as it runs,
// the newest heavier and over two, as an editor's minimap draws lines of
// code; under a writer kept from once or twice, how their newest opens,
// set as paragraphs of finer bars, as many as the sheet holds. w and h are
// the room the bars have.
func (a *App) mapGreek(c *ui.Context, g *favGroup, w, h float32) {
	// How long a line runs, in the widths of a Latin letter: a Chinese character is two.
	run := func(s string) float32 {
		var n float32
		for _, r := range s {
			n++
			if r >= 0x2E80 {
				n++
			}
		}
		return n
	}
	bar := func(key string, long, h float32, color ui.Color) {
		ui.Box(c.Key(key)).Width(max(6, long)).Height(h).Radius(h / 2).Background(color).Shrink(0)
	}
	ui.Row(c.Key("by")).Gap(4).AlignItems(ui.Center).Margin(0, 0, 2, 0).Children(func() {
		a.face(c, a.iconOf(g.src), "face", 10)
		bar("name", (w-14)*clampf(run(nameOf(g.src))/16, 0.3, 1), 2, theme.Muted)
	})
	for i, art := range g.list {
		n := run(titleOf(art.Title))
		if i > 0 {
			bar(art.ID, w*clampf(n/40, 0.3, 1), 2, theme.Line)
			continue
		}
		bar(art.ID, w*clampf(n/24, 0.3, 1), 3, theme.Text2)
		if n > 24 {
			bar(art.ID+"+", w*clampf((n-24)/24, 0.25, 0.8), 3, theme.Text2)
		}
		if len(g.list) > 2 {
			continue
		}
		// Lines of 40, broken after a space or any Chinese character, as the words would wrap, a paragraph's
		// last short; as many as the room under the face's line (15), the title's two (12) and the other
		// title's (5 each) holds, 5 a line.
		most := int((h - 15 - 12 - 5*float32(len(g.list)-1)) / 5)
		k := 0
		emit := func(n float32) {
			bar(fmt.Sprint("p", k), w*n/40, 2, theme.Line)
			k++
		}
		for pi, para := range a.sheetOpening(art) {
			var line, at float32
			for _, r := range para {
				if k >= most {
					break
				}
				u := float32(1)
				if r >= 0x2E80 {
					u = 2
				}
				if r == ' ' {
					at = line
				}
				line += u
				if r >= 0x2E80 {
					at = line
				}
				if line >= 40 {
					if at == 0 {
						at = line
					}
					emit(at)
					line, at = line-at, 0
				}
			}
			if line > 0 && k < most {
				emit(line)
			}
			if k >= most {
				break
			}
			// A paragraph's end: a line's room, less the gap.
			ui.Box(c.Key(fmt.Sprint("pp", pi))).Height(2)
			k++
		}
	}
}

// sheetOpening is how a writer's newest keep opens, as far as is known: its
// first paragraphs once read, else its feed's excerpt.
func (a *App) sheetOpening(art *library.Article) []string {
	if o := a.opening(art.ID, true); o != nil && len(o.paras) > 0 {
		return o.paras
	}
	if pv := art.Preview; pv != nil && pv.Excerpt != nil && *pv.Excerpt != "" {
		return []string{*pv.Excerpt}
	}
	return nil
}

// mapPictures is a block zoomed into: its designs in rows, each at its own
// proportions, grown out of its tile; so few that one row of mapRow would
// not fill the width, they fill it, as tall as the map at most.
func (a *App) mapPictures(c *ui.Context, p page, b mapBlock, w, most float32, quiet bool, stop string, live bool) {
	rs := make([]float32, len(b.g.list))
	for i, art := range b.g.list {
		rs[i] = a.wallRatio(art.ID)
	}
	rects, h := wallRects(rs, w, most)
	ui.Box(c.Key("pictures")).Width(w).Height(h).Role(ui.RoleList).Label(blockName(b)).Children(func() {
		for i, art := range b.g.list {
			a.wallPicture(c, p, b.key, art, rects[i], quiet, stop, live)
		}
	})
}

// wallRects lays a zoomed block's pictures of proportions rs in rows w wide,
// each row about mapRow tall; so few that one row would not fill the width,
// they fill it, as tall as most at most. h is how tall the rows stand.
func wallRects(rs []float32, w, most float32) (rects []ui.Rect, h float32) {
	var sum float32
	for _, r := range rs {
		sum += r
	}
	row := clampf((w-mapRowGap*float32(len(rs)-1))/sum, mapRow, max(mapRow, most))
	rects = mapRows(rs, w, row, mapRowGap)
	if n := len(rects); n > 0 {
		h = rects[n-1].Y + rects[n-1].H
	}
	return rects, h
}

// mapPacks is the pages captured zoomed into: each its first screen at
// 16:10, in rows as the designs are, grown out of its tile.
func (a *App) mapPacks(c *ui.Context, b mapBlock, w, most float32, quiet bool, stop string, live bool) {
	rs := make([]float32, len(b.packs))
	for i := range rs {
		rs[i] = 1.6
	}
	rects, h := wallRects(rs, w, most)
	ui.Box(c.Key("packs")).Width(w).Height(h).Role(ui.RoleList).Label(blockName(b)).Children(func() {
		for i, it := range b.packs {
			a.wallPack(c, it, rects[i], quiet, stop, live)
		}
	})
}

// mapPapers is the articles zoomed into: each writer's sheet down the
// shortest column, grown out of their part of the block.
func (a *App) mapPapers(c *ui.Context, p page, b mapBlock, w float32, quiet bool, stop string, live bool) {
	fs := a.favs()
	n := int(clampf(float32(int((w+wallGap)/(wallColumn+wallGap))), 1, 3))
	cw := (w - wallGap*float32(n-1)) / float32(n)
	cols := make([][]*favGroup, n)
	heights := make([]float32, n)
	for _, g := range b.writers {
		k := 0
		for i := range heights {
			if heights[i] < heights[k]-1 {
				k = i
			}
		}
		cols[k] = append(cols[k], g)
		heights[k] += paperWeight(g, cw) + wallGap
	}
	from := len(fs.cards)
	ui.Row(c.Key("papers")).FillWidth().Gap(wallGap).AlignItems(ui.Start).Role(ui.RoleList).Label(locales.FavoritesArticles()).Children(func() {
		for i, col := range cols {
			ui.Column(c.Key(i)).Width(cw).Shrink(0).Gap(wallGap).Children(func() {
				for _, g := range col {
					a.wallPaper(c, p, g, quiet, stop, live)
				}
			})
		}
	})
	if live && from <= len(fs.cards) {
		// J and K go writer by writer, as the block has them, rather than down one column and then the next.
		order := map[string]int{}
		for i, g := range b.writers {
			order["a:"+g.src.ID] = i
			for _, art := range g.list {
				order["k:"+art.ID] = i
			}
		}
		slices.SortStableFunc(fs.cards[from:], func(x, y favCard) int { return cmp.Compare(order[x.key], order[y.key]) })
	}
}

// paperWeight guesses how tall a writer's sheet stands, cw wide.
func paperWeight(g *favGroup, cw float32) float32 {
	perLine := max(1, (cw-40)/9) // the characters a 15 px line holds, about
	lines := func(s string, most float32) float32 { return min(most, float32(utf8.RuneCountInString(s))/perLine+1) }
	lead := g.list[0]
	h := 20 + 40 + 28*lines(titleOf(lead.Title), 3)*15/20 + 14
	if pv := lead.Preview; pv != nil && pv.Excerpt != nil && *pv.Excerpt != "" {
		h += 8 + 24*lines(*pv.Excerpt, 3)
	}
	for _, art := range g.list[1:min(1+paperRest, len(g.list))] {
		h += 21 + 22*lines(titleOf(art.Title), 2)
	}
	if len(g.list) > 1+paperRest {
		h += 32
	}
	return h
}

// wallPaper is a writer zoomed into: a sheet of paper, their face and name
// at its head (which opens their page), the title kept from them last with
// how it opens, the next three under hairlines, and the way to all.
func (a *App) wallPaper(c *ui.Context, p page, g *favGroup, quiet bool, stop string, live bool) {
	fs := a.favs()
	id := g.src.ID
	key := "a:" + id
	cell := ui.Box(c.Key(key)).FillWidth().Transition(a.mapTr(c, tileMatch{articles, key}, quiet))
	cell.Children(func() {
		paper := ui.Column(c.Key("paper")).FillWidth().Padding(20, 20, 10, 20).Radius(10)
		hover := paper.Hovered()
		edge := theme.LineSoft
		if hover {
			edge = theme.Line
		}
		paper.Background(theme.Surface).InsetShadow(0, 0, 0, 1, paper.AnimateColor("edge", edge, a.ease(motion.Hover)))
		ctx := fs.ctx == key
		menu := ui.ContextMenuBase(c, paper, &ctx)
		a.shelfMenu(c, menu, g, true)
		fs.ctx = keepOpen(fs.ctx, key, ctx)
		paper.Children(func() {
			ui.Row(c.Key("head")).FillWidth().Height(28).Gap(4).AlignItems(ui.Center).Margin(0, 0, 12, 0).Children(func() {
				b := ui.ButtonBase(c.Key("enter")).Label(locales.FavoritesWriterCount(nameOf(g.src), len(g.list))).Row().Height(28).Padding(0, 6).Gap(8).
					Margin(0, 0, 0, -6).Radius(6).AlignItems(ui.Center).Shrink(1).MinWidth(0).FocusRing(false).
					KeyShortcuts(0, ui.KeyEnter).KeyShortcuts(ui.Cmd, ui.KeyBackspace)
				a.card(b, key, stop, live)
				if b.Clicked() && live {
					fs.cursor = key
					a.enterShelf(c, id)
				}
				if b.FocusVisible() {
					b.FocusRing(true)
				}
				color := theme.Text2
				if b.Hovered() {
					color = theme.Text
				}
				b.Children(func() {
					f := a.face(c, a.iconOf(g.src), "face", 20)
					if live {
						a.shelfFaces[id] = f.LayoutBounds()
					}
					if a.tripping("shelf:" + id) {
						f.Opacity(0)
					}
					t := ui.Text(c.Key("name"), nameOf(g.src)).FontSize(theme.Body).LineHeight(20.0 / 13).FontWeight(600).
						TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover))).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
					a.share(c, t, p, favNameMatch{id}, live)
				})
				ui.Box(c).Grow(1)
				a.moreMenu(c, "shelf:"+id, locales.FavoritesWriterActions(nameOf(g.src)), hover || paper.FocusWithin(), func(m ui.MenuParts) { a.shelfMenu(c, m, g, false) }).
					Margin(-2, -8, -2, 0)
			})
			ui.Column(c.Key("titles")).FillWidth().Role(ui.RoleList).Label(locales.FavoritesFrom(nameOf(g.src))).Children(func() {
				for i, art := range g.list[:min(1+paperRest, len(g.list))] {
					a.paperTitle(c, p, art, i == 0, quiet, stop, live)
				}
			})
			if len(g.list) > 1+paperRest {
				all := ui.ButtonBase(c.Key("all")).Label(locales.FavoritesAllFrom(len(g.list))).Height(28).Margin(2, 0, 0, 0).AlignSelf(ui.Start).TabStop(false).FocusRing(false)
				if all.Clicked() && live {
					fs.cursor = key
					a.enterShelf(c, id)
				}
				color := theme.Muted
				if all.Hovered() {
					color = theme.Text
				}
				all.Children(func() {
					ui.Text(c, locales.FavoritesAllFrom(len(g.list))).FontSize(theme.Label).LineHeight(16.0 / 12).FontFeatures(theme.Numbers).
						TextColor(all.AnimateColor("fg", color, a.ease(motion.Hover)))
				})
			}
		})
	})
}

// paperTitle is one title on a writer's sheet, in the article's face: the
// newest at 20 with the first lines of how it opens, the rest at 15 under a
// hairline. The pointer lays no ground: the words brighten.
func (a *App) paperTitle(c *ui.Context, p page, art *library.Article, lead, quiet bool, stop string, live bool) {
	fs := a.favs()
	key := "k:" + art.ID
	style, size := paperLine, float32(theme.Item)
	if lead {
		style, size = paperLead, theme.Subtitle
	}
	row := ui.Box(c.Key(key)).FillWidth().Transition(a.listMove(c, quiet))
	if !lead {
		row.BorderWidth(1, 0, 0, 0).BorderColor(theme.Line)
	}
	row.Children(func() {
		b := ui.ButtonBase(c.Key("open")).Label(titleOf(art.Title)).FillWidth().Column().Gap(8).Radius(6).FocusRing(false).
			KeyShortcuts(0, ui.KeyEnter).KeyShortcuts(0, ui.KeyS).KeyShortcuts(0, ui.KeyDelete)
		if lead {
			b.Padding(0, 0, 14, 0)
		} else {
			b.Padding(10, 0)
		}
		a.card(b, key, stop, live)
		r := b.LayoutBounds()
		if b.Clicked() && live {
			fs.cursor = key
			a.openAs(c, art.ID, r, style, size)
		}
		a.layInto(p, art.ID, r)
		focus := b.FocusVisible()
		if focus {
			b.FocusRing(true).FocusRingOffset(4)
		}
		color := theme.Text2
		if lead || b.Hovered() || focus {
			color = theme.Text
		}
		b.Children(func() {
			ui.Row(c.Key("line")).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
				t := style(c, titleOf(art.Title)).TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover))).Shrink(1).MinWidth(0)
				a.end(t, p, "title:"+art.ID)
				a.looks[art.ID] = titleLook{style, size}
				if inProgress(art) {
					top := float32(6)
					if lead {
						top = 9
					}
					ui.Box(c.Key("moon")).Padding(top, 0, 0, 0).Shrink(0).Children(func() {
						a.moonOf(c, art, locales.ReaderProgress(percentOf(art)))
					})
				}
			})
			if pv := art.Preview; lead && pv != nil && pv.Excerpt != nil && *pv.Excerpt != "" {
				family, scale := reader.PassageFace(*pv.Excerpt)
				ui.Text(c.Key("opening"), *pv.Excerpt).Font(family).FontSize(14 * scale).LineHeight(24 / (14 * scale)).TextColor(theme.Text2).
					MaxLines(3).Ellipsis("…")
			}
		})
		ctx := fs.ctx == key
		menu := ui.ContextMenuBase(c, b, &ctx)
		a.keepMenu(c, menu, p, art, true)
		fs.ctx = keepOpen(fs.ctx, key, ctx)
	})
}

func paperLead(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).Font(reader.TitleFont(s)).FontSize(theme.Subtitle).LineHeight(1.4).FontWeight(600).TextColor(theme.Text).
		MaxLines(3).Ellipsis("…").Balance()
}

func paperLine(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).Font(reader.TitleFont(s)).FontSize(theme.Item).LineHeight(22.0 / 15).FontWeight(500).TextColor(theme.Text).
		MaxLines(2).Ellipsis("…")
}

// wallPicture is a design zoomed into, in block: its picture at its own
// proportions in r, a little dimmer the lighter it is and whole when
// pointed at, drifting closer; its title and where it was found rising at
// its foot. One with no picture to be had shows them at once.
func (a *App) wallPicture(c *ui.Context, p page, block string, art *library.Article, r ui.Rect, quiet bool, stop string, live bool) {
	fs := a.favs()
	key := "k:" + art.ID
	w, h := r.W, r.H
	cell := ui.Box(c.Key(key)).Absolute().Left(r.X).Top(r.Y).Size(w, h).Role(ui.RoleListItem).Transition(a.mapTr(c, tileMatch{block, key}, quiet))
	hover := cell.Hovered()
	var pic *favPic
	if cell.InViewMargin(800, 0) || fs.pics["wall:"+art.ID] != nil {
		pic = a.wallPic(art.ID)
	}
	cell.Children(func() {
		b := ui.ButtonBase(c.Key("open")).Label(titleOf(art.Title)).Absolute().Fill().Radius(10).ClipX().ClipY().Background(theme.Surface).
			FocusRing(false).KeyShortcuts(0, ui.KeyEnter).KeyShortcuts(0, ui.KeyS).KeyShortcuts(0, ui.KeyDelete)
		if a.crossing(c, live) {
			b.Cache()
		}
		a.card(b, key, stop, live)
		box := b.LayoutBounds()
		if b.Clicked() && live {
			fs.cursor = key
			a.openAs(c, art.ID, box, wallCaption, theme.Body)
		}
		a.layInto(p, art.ID, box)
		focus := b.FocusVisible()
		if focus {
			b.FocusRing(true).FocusRingOffset(2)
		}
		a.give(b, 0.99)
		zoom := a.zoom(c, cell, hover)
		bare := pic != nil && pic.done && pic.bmp == nil
		b.Children(func() {
			switch {
			case pic != nil && pic.bmp != nil:
				img := ui.Image(c, pic.bmp).Fit(ui.Cover).ObjectPosition(0.5, 0).Absolute()
				if fade := a.picFade(c, pic); fade < 1 {
					img.Opacity(fade)
				}
				grown(img, w, h, zoom)
				// Smaller than the page, a picture is dimmed less than one filling it: a white one rests at 0.83.
				bright := 1 - (1-pic.rest)*0.4
				if hover || focus {
					bright = 1
				}
				if v := 1 - b.AnimateTo("bright", bright, a.ease(motion.Lean)); v > 0.001 {
					ui.Box(c).Absolute().Fill().Background(theme.Darken).Opacity(v)
				}
			case !bare:
				a.breathing(c, theme.Hover)
			}
			show := float32(0)
			if hover || focus || bare {
				show = 1
			}
			capH := min(h, 72)
			cap := ui.Box(c.Key("cap")).Absolute().Left(0).Right(0).Bottom(0).Height(capH).IgnoreInput(true)
			cap.Opacity(cap.AnimateTo("show", show, a.ease(motion.Hover))).Children(func() {
				if !bare {
					ui.Box(c).Absolute().Fill().LinearGradient(bandScrim(min(1, 40/capH)))
				}
				ui.Column(c).Absolute().Left(14).Right(14).Bottom(12).Gap(2).Children(func() {
					t := wallCaption(c, titleOf(art.Title))
					a.end(t, p, "title:"+art.ID)
					a.looks[art.ID] = titleLook{wallCaption, theme.Body}
					ui.Text(c, nameOf(a.keeperOf(art))).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Text2).SingleLine().Ellipsis("…")
				})
			})
		})
		edge := theme.LineSoft
		if hover {
			edge = theme.EdgeHover
		}
		ui.Box(c.Key("edge")).Absolute().Fill().Radius(10).IgnoreInput(true).AccessHidden(true).InsetShadow(0, 0, 0, 1, b.AnimateColor("hairline", edge, a.ease(motion.Hover)))
		ctx := fs.ctx == key
		menu := ui.ContextMenuBase(c, b, &ctx)
		a.keepMenu(c, menu, p, art, true)
		fs.ctx = keepOpen(fs.ctx, key, ctx)
		a.moreMenu(c, key, locales.FavoritesArticleActions(titleOf(art.Title)), hover || cell.FocusWithin(), func(m ui.MenuParts) { a.keepMenu(c, m, p, art, false) }).
			Absolute().Top(10).Right(10).ZIndex(1)
	})
}

// wallPack is a page captured, zoomed into: its first screen in r, a little
// dimmer the lighter it is and whole when pointed at, drifting closer; its
// name and address rising at its foot. Its card grows out of it and goes
// back into it; copied there, it says 已复制 at its foot for a while.
func (a *App) wallPack(c *ui.Context, it *packItem, r ui.Rect, quiet bool, stop string, live bool) {
	fs, h := a.favs(), &a.capture
	key := "p:" + it.ID
	w, ht := r.W, r.H
	cell := ui.Box(c.Key(key)).Absolute().Left(r.X).Top(r.Y).Size(w, ht).Role(ui.RoleListItem).Transition(a.mapTr(c, tileMatch{captures, key}, quiet))
	hover := cell.Hovered()
	pic := a.packPic(it)
	now := c.Now()
	said := false
	if h.copiedHome == homeShelf && h.copiedID == it.ID {
		from, to := h.copiedNote()
		said = !now.Before(from) && now.Before(to)
		switch {
		case now.Before(from):
			c.After(from.Sub(now))
		case said:
			c.After(to.Sub(now))
		}
	}
	cell.Children(func() {
		b := ui.ButtonBase(c.Key("open")).Label(it.Name).Absolute().Fill().Radius(10).ClipX().ClipY().Background(theme.Surface).
			FocusRing(false).KeyShortcuts(0, ui.KeyEnter)
		a.card(b, key, stop, live)
		if b.Clicked() && live {
			fs.cursor = key
			a.openPack(it, b.LayoutBounds(), homeShelf)
		}
		focus := b.FocusVisible()
		if focus {
			b.FocusRing(true).FocusRingOffset(2)
		}
		a.give(b, 0.99)
		zoom := a.zoom(c, cell, hover)
		b.Children(func() {
			if pic != nil && pic.bmp != nil {
				img := ui.Image(c, pic.bmp).Fit(ui.Cover).ObjectPosition(0.5, 0).Absolute()
				if fade := a.picFade(c, pic); fade < 1 {
					img.Opacity(fade)
				}
				grown(img, w, ht, zoom)
				bright := 1 - (1-pic.rest)*0.4
				if hover || focus {
					bright = 1
				}
				if v := 1 - b.AnimateTo("bright", bright, a.ease(motion.Lean)); v > 0.001 {
					ui.Box(c).Absolute().Fill().Background(theme.Darken).Opacity(v)
				}
			} else if pic == nil || !pic.done {
				a.breathing(c, theme.Hover)
			}
			// The foot's shade comes with the words, or with 已复制.
			capH := min(ht, 72)
			ease := a.ease(motion.Hover)
			if a.kbd {
				ease = ui.Animation{Duration: time.Nanosecond}
			}
			cap := ui.Box(c.Key("cap")).Absolute().Left(0).Right(0).Bottom(0).Height(capH).IgnoreInput(true)
			cap.Opacity(cap.AnimateTo("show", map[bool]float32{true: 1}[hover || focus || said], ease)).Children(func() {
				ui.Box(c).Absolute().Fill().LinearGradient(bandScrim(min(1, 40/capH)))
				note := locales.CommonCopied()
				right := float32(14)
				if said {
					nw, _ := c.MeasureText(0, ui.Span{Text: note, Size: theme.Body})
					right += nw + 12
				}
				words := ui.Column(c.Key("words")).Absolute().Left(14).Right(right).Bottom(12).Gap(2)
				words.Opacity(words.AnimateTo("show", map[bool]float32{true: 1}[hover || focus], ease)).Children(func() {
					wallCaption(c, it.Name)
					ui.Text(c, hostOf(it.URL)).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Text2).SingleLine().Ellipsis("…")
				})
				if o := cap.AnimateTo("said", map[bool]float32{true: 1}[said], ease); o > 0.01 {
					ui.Text(c.Key("said"), note).FontSize(theme.Body).LineHeight(18.0 / 13).TextColor(theme.Text).NoWrap().
						Absolute().Right(12).Bottom(12).Opacity(o)
				}
			})
		})
		edge := theme.LineSoft
		if hover {
			edge = theme.EdgeHover
		}
		ui.Box(c.Key("edge")).Absolute().Fill().Radius(10).IgnoreInput(true).AccessHidden(true).InsetShadow(0, 0, 0, 1, b.AnimateColor("hairline", edge, a.ease(motion.Hover)))
	})
}

// packPic is a page captured's first screen for 收藏, 720 wide.
func (a *App) packPic(it *packItem) *favPic {
	file := filepath.Join(a.capture.dir, it.ID, "screens", "00.jpg")
	return a.favPicture("pack:"+packKey(it), func() image.Image {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil
		}
		return screenTop(decodeAny(data), 720)
	})
}

// hostOf is the site a page is on, as one reads it: its host without www.
func hostOf(page string) string {
	u, err := url.Parse(page)
	if err != nil || u.Hostname() == "" {
		return page
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

func wallCaption(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(theme.Body).LineHeight(18.0 / 13).FontWeight(600).TextColor(theme.Text).SingleLine().Ellipsis("…")
}

// wallPic is a design's picture for the map, 720 wide.
func (a *App) wallPic(id string) *favPic {
	thumbs := a.opt.Thumbs
	return a.favPicture("wall:"+id, func() image.Image {
		if thumbs != nil {
			return nil // the tests' thumbnails are bitmaps, without the picture to measure
		}
		img, _ := a.thumbImage(id, 0, 720, true)
		return img
	})
}

// wallRatio is a design's proportions as its picture has them, held between
// 2:3 and 2:1; 3:2 until it has come, 2:1 with none to be had.
func (a *App) wallRatio(id string) float32 {
	p := a.favs().pics["wall:"+id]
	switch {
	case p == nil || !p.done:
		return 1.5
	case p.bmp == nil:
		return 2
	}
	w, h := p.bmp.Size()
	return clampf(float32(w)/float32(max(1, h)), 2.0/3, 2)
}
