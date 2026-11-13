package reader

import (
	"unicode/utf16"

	"trie/internal/theme"

	"github.com/egoist/mygo/ui"
)

// A link of the body stands in a slanted light: under the lower half of its words, a warm glow rising from a little
// below the baseline and fading out upward, its ends leaning as a highlighter's stroke does. Pointed at or focused,
// the light rises over the whole of its words and deepens.

// The light, in ems of the text: how far it reaches below the baseline and above it, at rest and lit; how far its
// ends lean and reach past the words; how strong its top is lit, of its foot's.
const (
	glowFoot, glowTop, glowTopLit = .16, .58, .86
	glowSlant, glowReach          = .16, .08
	glowCrownLit                  = .4
)

// linkSpan is a link's stretch of its paragraph's text, in UTF-16: from its site's mark when it has one, to its last
// word. k is how far it is lit, 0 to 1. A piece of the flow cuts it open where the piece ends: the light ends there
// straight, without leaning or reaching past, so that the pieces join as they land.
type linkSpan struct {
	start, end   int
	k            float32
	openL, openR bool
}

// linkSpans are the links of segs, as the paragraph builds them: runs of the same href, the mark before one with it.
func linkSpans(segs []segment) []linkSpan {
	var out []linkSpan
	at := 0
	for i := 0; i < len(segs); {
		j := i + 1
		for j < len(segs) && segs[j].href == segs[i].href {
			j++
		}
		n := 0
		for _, sg := range segs[i:j] {
			n += utf16Len([]rune(sg.text))
		}
		if segs[i].href != "" {
			start := at
			if i > 0 && segs[i-1].style&glyph != 0 {
				start--
			}
			out = append(out, linkSpan{start: start, end: at + n})
		}
		at += n
		i = j
	}
	return out
}

// within is the links of a piece of the flow, its text from start to end of its paragraph's.
func within(links []linkSpan, start, end int) []linkSpan {
	var out []linkSpan
	for _, s := range links {
		if s.end <= start || s.start >= end {
			continue
		}
		out = append(out, linkSpan{start: max(s.start, start) - start, end: min(s.end, end) - start, k: s.k,
			openL: s.openL || s.start < start, openR: s.openR || s.end > end})
	}
	return out
}

// markerShape is a paragraph's layout as its links' light is drawn on: shaped again only at another width or size.
type markerShape struct {
	w, size float32
	layout  *ui.TextLayout
}

// markerLayout is b's layout at width w, for its links' light.
func (b *block) markerLayout(w, size float32, sp []ui.Span, font ui.Font, shape ui.TextShape) *ui.TextLayout {
	if m := b.marker; m != nil && m.w == w && m.size == size {
		return m.layout
	}
	shape.Width = w
	b.marker = &markerShape{w: w, size: size, layout: ui.ShapeRichTextWith(sp, font, shape)}
	return b.marker.layout
}

// paintMarkers paints the light of links on l, laid out at x, y, its text size size, its text text in UTF-16: on each
// line a link takes, under its words without the spaces the line ends in.
func paintMarkers(pt *ui.Painter, x, y float32, l *ui.TextLayout, text []uint16, size float32, links []linkSpan) {
	if len(links) == 0 {
		return
	}
	lines := l.Lines()
	for _, s := range links {
		foot := mix(theme.LinkGround, theme.LinkGroundLit, s.k)
		light := ui.LinearGradient{Angle: 180, From: foot.Alpha(glowCrownLit * s.k), To: foot, Oklab: true}
		top := glowTop + (glowTopLit-glowTop)*s.k
		var pieces [][2]int
		for _, ln := range lines {
			a, b := max(s.start, ln.Start), min(s.end, ln.End)
			for b > a && b-1 < len(text) && (text[b-1] == ' ' || text[b-1] == '\n') {
				b--
			}
			if b > a {
				pieces = append(pieces, [2]int{a, b})
			}
		}
		for i, pc := range pieces {
			rects := l.SelectionRects(ui.TextInputRange{Start: pc[0], End: pc[1]})
			if len(rects) == 0 {
				continue
			}
			q := rects[0]
			for _, o := range rects[1:] {
				q.W = max(q.X+q.W, o.X+o.W) - min(q.X, o.X)
				q.X = min(q.X, o.X)
			}
			base := q.Y + q.H*.8
			for _, ln := range lines {
				if mid := q.Y + q.H/2; mid >= ln.Rect.Y && mid < ln.Rect.Y+ln.Rect.H {
					base = ln.Baseline
				}
			}
			y0, y1 := y+base-top*size, y+base+glowFoot*size
			// The top of each end leans right of its foot; an end the flow cuts open stands straight.
			lean, reach := glowSlant*size/2, glowReach*size
			x0t, x0b, x1t, x1b := x+q.X, x+q.X, x+q.X+q.W, x+q.X+q.W
			if i > 0 || !s.openL {
				x0t, x0b = x0t-reach+lean, x0b-reach-lean
			}
			if i < len(pieces)-1 || !s.openR {
				x1t, x1b = x1t+reach+lean, x1b+reach-lean
			}
			pt.FillPathGradient((&ui.Path{}).MoveTo(x0t, y0).LineTo(x1t, y0).LineTo(x1b, y1).LineTo(x0b, y1).Close(), light)
		}
	}
}

func mix(a, b ui.Color, k float32) ui.Color {
	f := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*k + .5) }
	return ui.Color{R: f(a.R, b.R), G: f(a.G, b.G), B: f(a.B, b.B), A: f(a.A, b.A)}
}

// utf16Of is the text of sp in UTF-16, as a layout of them counts it.
func utf16Of(sp []ui.Span) []uint16 {
	var out []uint16
	for _, s := range sp {
		out = append(out, utf16.Encode([]rune(s.Text))...)
	}
	return out
}
