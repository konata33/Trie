package reader

import (
	"crypto/rand"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

// Clip is a passage kept from the body: library.ClipInput, which the
// store's add-clip takes.
type Clip struct {
	ID              string  `json:"id"`
	ArticleID       string  `json:"articleID"`
	Text            string  `json:"text"`
	Note            string  `json:"note"`
	UnitID          *string `json:"unitID"`          // the unit the selection began in
	ContentRevision *string `json:"contentRevision"` // the document's; null for a body from HTML
	CreatedAt       string  `json:"createdAt"`
	// Where the selection began and ended, to the rune: the store keeps only
	// UnitID, but the native reader can mark a clip exactly with these.
	Start, End ClipPoint `json:"-"`
	parts      []clipPart
}

// ClipPoint is a place in a unit's text.
type ClipPoint struct {
	UnitID string
	Offset int // in runes of the paragraph the unit's text is shown in
}

type clipPart struct {
	b        *block
	from, to int
}

// Selection returns the clip the selection makes, without keeping it: false
// while nothing, or only white space, is selected.
func (r *Reader) Selection() (Clip, bool) {
	text, parts := r.join(r.selected)
	text = strings.TrimSpace(text)
	if text == "" || len(parts) == 0 {
		return Clip{}, false
	}
	cl := Clip{ID: uuid(), ArticleID: r.Article.ID, Text: text, parts: parts}
	if u := parts[0].b.unit; u != "" {
		cl.UnitID = &u
	}
	if rev := r.Body.Revision; rev != "" {
		cl.ContentRevision = &rev
	}
	first, last := parts[0], parts[len(parts)-1]
	cl.Start, cl.End = ClipPoint{first.b.unit, first.from}, ClipPoint{last.b.unit, last.to}
	return cl, true
}

// Keep makes a clip of the selection and keeps it in memory, marked in the
// body, as ⌘E does; it lets go of the selection.
// Nothing is written to the library.
func (r *Reader) Keep(now time.Time) (Clip, bool) {
	cl, ok := r.Selection()
	if !ok {
		return Clip{}, false
	}
	if len(cl.Text) > 20000 {
		return Clip{}, false // 选中的文字太长, as the store refuses it
	}
	cl.CreatedAt = now.UTC().Format("2006-01-02T15:04:05.000Z")
	r.clips = append(r.clips, &cl)
	r.gen++
	return cl, true
}

// Clips are the clips kept in this session, oldest first.
func (r *Reader) Clips() []Clip {
	out := make([]Clip, len(r.clips))
	for i, c := range r.clips {
		out[i] = *c
	}
	return out
}

// join gives the selected parts' text as Copy writes it, broken between
// blocks as the browser's innerText breaks them (a blank line around a
// paragraph, a line break around other blocks, a tab between the cells of
// a row), and the parts themselves.
func (r *Reader) join(sel []ui.SelectedText) (string, []clipPart) {
	var sb strings.Builder
	var parts []clipPart
	var prev *para
	for _, s := range sel {
		p := r.paras[s.ID]
		runes := []rune(s.Text)
		from, to := max(0, min(s.Start, len(runes))), max(0, min(s.End, len(runes)))
		if p == nil || from >= to {
			continue
		}
		if prev != nil {
			switch {
			case p.row != 0 && p.row == prev.row:
				sb.WriteByte('\t')
			case len(p.sep) > len(prev.sep):
				sb.WriteString(p.sep)
			default:
				sb.WriteString(prev.sep)
			}
		}
		sb.WriteString(string(runes[from:to]))
		parts = append(parts, clipPart{p.b, from, to})
		prev = p
	}
	return sb.String(), parts
}

func (r *Reader) copyData(sel []ui.SelectedText) transfer.Data {
	text, _ := r.join(sel)
	return transfer.TextData(text)
}

func uuid() string {
	var b [16]byte
	rand.Read(b[:])
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Position is a reading position: library.ReadingPositionInput, which
// SaveReadingPosition stores.
type Position struct {
	ArticleID      string  `json:"articleID"`
	ContentVersion string  `json:"contentVersion"`
	UnitID         *string `json:"unitID"`
	Offset         float32 `json:"offset"`
	ScrollTop      float32 `json:"scrollTop"`
	Progress       float32 `json:"progress"`
}

// ContentVersion is what a reading position is kept for: the document's
// revision, or the legacy version of a body from HTML.
func (r *Reader) ContentVersion() string {
	v := r.Body.Revision
	if v == "" {
		v = r.Content.FeedVersion
		if v == "" {
			v = r.Content.FetchedAt
		}
		v = "legacy:" + v
	}
	return v[:min(len(v), 256)]
}

// Position measures where the reader is: the
// unit at a guide 110 DIPs below the top, how far into it, and how far
// through the body, as of the last frame.
func (r *Reader) Position() Position {
	pos := Position{ArticleID: r.Article.ID, ContentVersion: r.ContentVersion(), ScrollTop: r.Scroll.Y}
	if r.body.H <= 0 {
		return pos
	}
	relative := r.Scroll.Y + guide - r.bodyTop()
	var unit *unitTop
	for i := range r.units { // in document order, and so by top
		if r.units[i].top > relative {
			break
		}
		unit = &r.units[i]
	}
	if unit != nil {
		id := unit.id
		pos.UnitID, pos.Offset = &id, relative-unit.top
	} else {
		pos.Offset = relative
	}
	pos.Offset = max(-1e7, min(1e7, pos.Offset))
	pos.Progress = max(0, min(1, relative/max(1, r.body.H-r.view.H+guide)))
	return pos
}

// RestorePosition puts the reader back at p, a position of this content
// version, once the body has been laid out: the unit's place, else the
// offset it was scrolled to.
func (r *Reader) RestorePosition(p Position) {
	if p.ContentVersion == r.ContentVersion() {
		r.restore = &p
	}
}

func (r *Reader) restoreNow(c *ui.Context) {
	p := r.restore
	if p == nil {
		return
	}
	if r.body.H <= 0 || len(r.units) == 0 && p.UnitID != nil {
		c.Invalidate() // laid out in the next frame
		return
	}
	y := p.ScrollTop
	if p.UnitID != nil {
		for _, u := range r.units {
			if u.id == *p.UnitID {
				y = r.bodyTop() + u.top - guide + p.Offset
				break
			}
		}
	}
	if y := float32(math.Max(0, float64(y))); math.Abs(float64(y-r.Scroll.Y)) >= 1 {
		r.Scroll.Y, r.sending = y, true
	}
	r.restore = nil
}

func urlUnescape(s string) (string, error) { return url.PathUnescape(s) }

// Mark is a clip the library keeps (library.Clip): its words and
// the unit its selection began in.
type Mark struct {
	ID     string
	Text   string
	UnitID *string
}

// SetMarks draws the library's clips of the article where their words are
// still found: white space ignored, at the unit
// they began in when the words occur more than once, else at the first;
// one whose words are gone is left out. Clips kept in this session that
// the library now holds give way to it.
func (r *Reader) SetMarks(marks []Mark) {
	var key strings.Builder
	for _, m := range marks {
		key.WriteString(m.ID + "\x00" + m.Text + "\x00")
	}
	if key.String() == r.markKey {
		return
	}
	r.markKey = key.String()
	held := map[string]bool{}
	for _, m := range marks {
		held[m.ID] = true
	}
	kept := r.clips[:0]
	for _, cl := range r.clips {
		if !held[cl.ID] {
			kept = append(kept, cl)
		}
	}
	r.clips = kept
	// The body's words without white space, and for each kept rune the
	// block and the rune of the block's text it came from.
	type at struct {
		b *block
		i int
	}
	var flat []rune
	var from []at
	var walk func(bs []*block)
	walk = func(bs []*block) {
		for _, b := range bs {
			i := 0
			for _, rn := range b.runs {
				for _, ch := range rn.text {
					if !unicode.IsSpace(ch) {
						flat = append(flat, ch)
						from = append(from, at{b, i})
					}
					i++
				}
			}
			walk(b.children)
			for _, row := range b.rows {
				for _, cl := range row {
					walk(cl.blocks)
				}
			}
		}
	}
	walk(r.Body.blocks)
	r.marks = r.marks[:0]
	for _, m := range marks {
		words := []rune(strings.Join(strings.Fields(m.Text), ""))
		if len(words) == 0 {
			continue
		}
		first, chosen := -1, -1
		for i := 0; i+len(words) <= len(flat); i++ {
			if flat[i] != words[0] || !slices.Equal(flat[i:i+len(words)], words) {
				continue
			}
			if first < 0 {
				first = i
			}
			if m.UnitID != nil && from[i].b.unit == *m.UnitID {
				chosen = i
				break
			}
		}
		if chosen < 0 {
			chosen = first
		}
		if chosen < 0 {
			continue
		}
		cl := &Clip{ID: m.ID, Text: m.Text}
		for i := chosen; i < chosen+len(words); i++ {
			f := from[i]
			if n := len(cl.parts); n > 0 && cl.parts[n-1].b == f.b {
				cl.parts[n-1].to = f.i + 1
				continue
			}
			cl.parts = append(cl.parts, clipPart{f.b, f.i, f.i + 1})
		}
		r.marks = append(r.marks, cl)
	}
	r.gen++
}

// SelectedMark is the clip the selection lies within, when it lies within
// one: the clip bar then offers to take it back.
func (r *Reader) SelectedMark() (string, bool) {
	_, parts := r.join(r.selected)
	if len(parts) == 0 {
		return "", false
	}
	for _, cl := range append(r.marks[:len(r.marks):len(r.marks)], r.clips...) {
		inside := true
		for _, p := range parts {
			hit := false
			for _, q := range cl.parts {
				if q.b == p.b && p.from >= q.from && p.to <= q.to {
					hit = true
					break
				}
			}
			if !hit {
				inside = false
				break
			}
		}
		if inside {
			return cl.ID, true
		}
	}
	return "", false
}

// Unmark lets go of a clip kept in this session.
func (r *Reader) Unmark(id string) {
	kept := r.clips[:0]
	for _, cl := range r.clips {
		if cl.ID != id {
			kept = append(kept, cl)
		}
	}
	r.clips = kept
	r.gen++
}

// SelectionBox is the box the selection covers in the window, as the frame
// before laid it out: the bar that clips it floats over it.
func (r *Reader) SelectionBox() (ui.Rect, bool) {
	var box ui.Rect
	found := false
	add := func(q ui.Rect) {
		if !found {
			box, found = q, true
			return
		}
		x0, y0 := min(box.X, q.X), min(box.Y, q.Y)
		x1, y1 := max(box.X+box.W, q.X+q.W), max(box.Y+box.H, q.Y+q.H)
		box = ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
	}
	for _, s := range r.selected {
		p := r.paras[s.ID]
		if p == nil || p.box.W <= 0 || s.Start >= s.End {
			continue
		}
		if len(p.segs) == 0 {
			add(p.box)
			continue
		}
		l := ui.ShapeRichText(spans(p.segs, p.font.Size), p.font, p.box.W)
		runes := []rune(s.Text)
		from, to := utf16Len(runes[:min(s.Start, len(runes))]), utf16Len(runes[:min(s.End, len(runes))])
		for _, q := range l.SelectionRects(ui.TextInputRange{Start: from, End: to}) {
			add(ui.Rect{X: p.box.X + q.X, Y: p.box.Y + q.Y, W: q.W, H: q.H})
		}
	}
	return box, found
}

func utf16Len(rs []rune) int {
	n := 0
	for _, r := range rs {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// Restoring reports whether a reading position waits to be restored, as it
// does until the body is laid out.
func (r *Reader) Restoring() bool { return r.restore != nil }

// ClearSelection lets go of the selection.
func (r *Reader) ClearSelection() { r.gen++ }

// UnitY is where the block of a unit starts in the scrolled content, as the
// frame before laid it out.
func (r *Reader) UnitY(id string) (float32, bool) {
	for _, u := range r.units {
		if u.id == id {
			return r.bodyTop() + u.top, true
		}
	}
	return 0, false
}

// Units are the units of the body in order.
func (r *Reader) Units() []string {
	out := make([]string, len(r.units))
	for i, u := range r.units {
		out[i] = u.id
	}
	return out
}
