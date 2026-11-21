package reader

import (
	"time"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"

	"trie/internal/motion"
)

// The text flowing over to a new measure (Flow). The body is laid out at the new width at once, its words drawn
// unseen and its pictures left as empty boxes of their size; over them the body paints each paragraph's lines in
// pieces, a piece being the words an old line and a new line share, and every piece, and every picture, glides from
// where the old measure showed it to where the new one puts it once the scroll has come to rest. The scroll glides
// there along the same curve, as far as keeps the block being read where it showed, and the column slides from its
// old place to its new one, the app's bar widening along with it. The new places are known the frame after it sets
// off, from the layout (where every paragraph is, on the screen or not); until then everything shows where it was.
// Then the words and the pictures show again where the pieces came to rest.

// flowTime is how long the text flows, along the travel spring.
const flowTime = motion.TravelIn

// flowing is a flow under way.
type flowing struct {
	at    time.Time          // the frame it set off in, the body laid out at the new measure
	now   time.Time          // the frame being built and painted
	shift float32            // how far the column moves left, half the width it gains
	old   map[uint64]para    // the paragraphs as the old measure showed them, by element
	pics  map[*block]ui.Rect // the pictures as it showed them
	set   bool               // the pieces' places at rest are known
	done  bool               // its time is up: the next frame is built without it
	words []piece
	shots []shot
}

// piece is some words of a paragraph, from where the old measure showed them to where the new one puts them, both in
// the window, the scroll at rest.
type piece struct {
	layout   *ui.TextLayout
	glyphs   []string   // the marks of its objects, in order
	links    []linkSpan // its links' grounds
	text     []uint16   // its text in UTF-16, which the grounds go by
	size     float32    // its text size, which the grounds go by
	color    ui.Color
	from, to ui.Point
}

// shot is a picture going from its old box to its new one.
type shot struct {
	pic      *picture
	from, to ui.Rect
}

// Flowing reports whether the text is flowing over to a new measure.
func (r *Reader) Flowing() bool { return r.fl != nil }

// k is how far along the flow is, along the curve, at t.
func (f *flowing) k(t time.Time) float32 {
	return motion.TravelEase(min(1, max(0, float32(t.Sub(f.at))/float32(flowTime))))
}

// FlowShift is how far right of its new place the column shows, flowing: half the width it gains, going to nothing.
func (r *Reader) FlowShift() float32 {
	if f := r.fl; f != nil {
		return f.shift * (1 - f.k(f.now))
	}
	return 0
}

// FlowInset is how much narrower than the column the app's bar shows on its right, so that it widens along with
// the flow: twice the column's shift. next is the width the reading options chose in this frame, before the flow
// has set off: the bar starts as narrow as the old column.
func (r *Reader) FlowInset(next float32) float32 {
	switch {
	case r.fl != nil:
		return 2 * r.FlowShift()
	case r.Flow && next != r.width():
		return 2 * r.columnShift(r.width(), next)
	}
	return 0
}

// columnShift is how far left the column moves going from width w0 to w1, centred in the reader.
func (r *Reader) columnShift(w0, w1 float32) float32 {
	col := func(w float32) float32 {
		if r.view.W > 0 {
			return min(r.view.W, w+80)
		}
		return w + 80
	}
	return (col(w1) - col(w0)) / 2
}

// startFlow sets a flow off from the old width: the paragraphs and pictures as they show now, and the block to
// hold, which the scroll glides to keep in place.
func (r *Reader) startFlow(c *ui.Context, from float32) {
	f := &flowing{at: c.Now(), now: c.Now(), shift: r.columnShift(from, r.width()), old: map[uint64]para{}, pics: map[*block]ui.Rect{}}
	for id, p := range r.paras {
		if p.segs != nil && r.near(p.box) {
			f.old[id] = *p
			sp := spans(p.segs, p.font.Size)
			f.words = append(f.words, piece{layout: ui.ShapeRichText(sp, p.font, p.box.W), glyphs: glyphNames(sp), links: linkSpans(p.segs), text: utf16Of(sp),
				size: p.font.Size, color: p.color,
				from: ui.Point{X: p.box.X, Y: p.box.Y}, to: ui.Point{X: p.box.X, Y: p.box.Y}})
		}
	}
	for b, box := range r.picBox {
		if r.near(box) {
			f.pics[b] = box
			f.shots = append(f.shots, shot{pic: b.pic, from: box, to: box})
		}
	}
	r.fl = f
	r.held = r.holdAt()
	if r.held != nil {
		r.held.glide = &ui.Animation{Duration: flowTime, Ease: motion.TravelEase}
	}
}

// near reports whether box, in the window, shows in the reader or within a screen of it: what may come into view
// on the way.
func (r *Reader) near(box ui.Rect) bool {
	return box.H > 0 && box.Y+box.H > r.view.Y-r.view.H && box.Y < r.view.Y+2*r.view.H
}

// flowOn moves the flow along once the body is built: the frame after it set off, the layout tells where everything
// goes, and the pieces are cut; when its time is up, it ends.
func (r *Reader) flowOn(c *ui.Context) {
	f := r.fl
	if f == nil {
		return
	}
	if !f.set && c.Now().After(f.at) {
		// Where the scroll comes to rest: as far as puts the held block where it showed.
		var drop float32
		if h := r.held; h != nil && r.heldFound {
			drop = r.heldTop - (r.view.Y + h.off)
		}
		f.set, f.words, f.shots = true, f.words[:0], f.shots[:0]
		for id, o := range f.old {
			if p, ok := r.paras[id]; ok {
				f.words = append(f.words, cut(o, p.box.W, ui.Point{X: p.box.X, Y: p.box.Y - drop})...)
			}
		}
		for b, from := range f.pics {
			if to, ok := r.picBox[b]; ok {
				to.Y -= drop
				f.shots = append(f.shots, shot{pic: b.pic, from: from, to: to})
			}
		}
	}
	if c.Now().Sub(f.at) >= flowTime {
		// This frame's body is built for the flow, its words unseen: it paints them at rest, and the next frame,
		// built without the flow, shows them themselves (View).
		f.done = true
		c.Invalidate()
		return
	}
	c.AnimationFrame()
}

// ghost is a paragraph's spans drawn unseen, as a flow lays it out: the same words at the same sizes, without the
// grounds and lines that would show.
func ghost(sp []ui.Span) []ui.Span {
	out := make([]ui.Span, len(sp))
	for i, s := range sp {
		s.Background, s.Underline, s.Strikethrough = ui.Transparent, false, false
		out[i] = s
	}
	return out
}

// paintFlow paints the pictures and the pieces over the body (its DrawOver), as far along as the curve has gone.
// The body is in the column, which slides: the painter has its slide, which comes off.
func (r *Reader) paintFlow(p *ui.Painter, _ ui.Rect) {
	f := r.fl
	if f == nil {
		return
	}
	k := f.k(f.now)
	tx := f.shift * (1 - k)
	lerp := func(a, b float32) float32 { return a + (b-a)*k }
	for _, s := range f.shots {
		box := ui.Rect{X: lerp(s.from.X, s.to.X) - tx, Y: lerp(s.from.Y, s.to.Y), W: lerp(s.from.W, s.to.W), H: lerp(s.from.H, s.to.H)}
		// In the corner it takes where it lands, as the body shows it (pictureRadius).
		rad := pictureRadius(s.pic, s.to.W)
		p.Clip(box, rad, func() {
			p.Image(s.pic.img, box, ui.FillBox)
			if len(s.pic.texts) > 0 {
				drawSVGTexts(p, box, s.pic)
			}
		})
		pictureEdge(p, box, rad)
	}
	for _, w := range f.words {
		y := lerp(w.from.Y, w.to.Y)
		if y > r.view.Y+r.view.H || y < r.view.Y-300 {
			continue
		}
		x := lerp(w.from.X, w.to.X) - tx
		paintMarkers(p, x, y, w.layout, w.text, w.size, w.links)
		p.TextLayout(w.layout, x, y, w.color)
		for i, o := range w.layout.Objects() {
			if i < len(w.glyphs) {
				drawGlyph(p, w.glyphs[i], ui.Rect{X: x + o.Rect.X, Y: y + o.Rect.Y, W: o.Rect.W, H: o.Rect.H}, w.color)
			}
		}
	}
}

// cut cuts a paragraph into the pieces its old lines and its lines at width share; at is the paragraph's corner
// where it comes to rest.
func cut(old para, width float32, at ui.Point) []piece {
	sp := spans(old.segs, old.font.Size)
	links := linkSpans(old.segs)
	l0, l1 := ui.ShapeRichText(sp, old.font, old.box.W), ui.ShapeRichText(sp, old.font, width)
	a, b := l0.Lines(), l1.Lines()
	var out []piece
	for i, j := 0, 0; i < len(a) && j < len(b); {
		start, end := max(a[i].Start, b[j].Start), min(a[i].End, b[j].End)
		if end > start {
			if words := slice(sp, start, end); len(words) > 0 {
				c0 := l0.CaretAt(ui.TextCaretPosition{Index: start, Affinity: ui.TextDownstream})
				c1 := l1.CaretAt(ui.TextCaretPosition{Index: start, Affinity: ui.TextDownstream})
				out = append(out, piece{layout: ui.ShapeRichText(words, old.font, 0), glyphs: glyphNames(words),
					links: within(links, start, end), text: utf16Of(words), size: old.font.Size, color: old.color,
					from: ui.Point{X: old.box.X + c0.X, Y: old.box.Y + a[i].Rect.Y},
					to:   ui.Point{X: at.X + c1.X, Y: at.Y + b[j].Rect.Y}})
			}
		}
		switch {
		case a[i].End == b[j].End:
			i, j = i+1, j+1
		case a[i].End < b[j].End:
			i++
		default:
			j++
		}
	}
	return out
}

// slice is the part of spans from UTF-16 offset start to end; nil when it holds only spaces.
func slice(sp []ui.Span, start, end int) []ui.Span {
	var out []ui.Span
	words := false
	at := 0
	for _, s := range sp {
		u := utf16.Encode([]rune(s.Text))
		from, to := max(start-at, 0), min(end-at, len(u))
		at += len(u)
		if from >= to {
			continue
		}
		part := s
		part.Text = string(utf16.Decode(u[from:to]))
		for _, r := range part.Text {
			if r != ' ' && r != '\n' && r != '\t' {
				words = true
			}
		}
		out = append(out, part)
	}
	if !words {
		return nil
	}
	return out
}
