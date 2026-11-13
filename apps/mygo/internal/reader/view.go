package reader

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/theme"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// Column is the reading column's width (theme.ReaderColumn, centred).
const Column = theme.ReaderColumn

// guide is how far below the top of the reader a reading position is
// measured.
const guide = 110

// cjkFace draws Chinese and Japanese text where no article's face says
// (Face.CJK): in code (see scripts).
const cjkFace = "PingFang SC"

// Reader shows an article in the reading column, in a scroll container
// that keeps its reading position: its header, its body and the end mark.
// Keep one per article shown; its fields are the app's state.
type Reader struct {
	Article *Article
	Content *Content
	Body    *Body
	// Size is the body text's size in DIPs: 15 to 23, 17 by default.
	Size float32
	// OnLink opens a link to another page; without it MyGo opens it.
	OnLink func(href string)
	// OnRetry is 重试 under a picture that did not load; without it the
	// button does not show.
	OnRetry func()

	// Width is the column's width, Column when zero.
	Width float32
	// Bar builds what the column starts with, held as the text crosses
	// to a new size or measure (the app's toolbar); Above and Below build
	// what the column holds above the body and after it, in place of the
	// reader's own header and end mark: the app's. Above returns the margin
	// under what it ends with, which the body's first margin collapses
	// with, as CSS's do: 36 under the header.
	Bar, Below func()
	Above      func() float32
	// Insert builds what the body holds before its first h2, or after its
	// last block when it has none (a work's facts),
	// and returns its top and bottom margins, which collapse with those of
	// the blocks around it.
	Insert func() (top, bottom float32)
	// Instant shows a new size or measure in the next frame rather than
	// crossing over to it, as the keyboard asks.
	Instant bool
	// Live lays a size or measure that changes from frame to frame (the app
	// gliding to a new one) out in place, every frame, the top of the block
	// being read held where it shows, instead of crossing over to it.
	Live bool
	// Flow takes a new measure by flowing the text over to it (flow.go):
	// its lines' pieces glide from where the old measure had them.
	Flow bool
	// Name is the body's accessible name: 「标题 · 正文」.
	Name string
	// OnPicture shows a picture of the body large, clicked or pressed with
	// Enter or Space; without it the pictures are not buttons.
	OnPicture func(Pic)
	// Hide is the ID of the picture the page leaves empty while it shows
	// large; 0 for none.
	Hide int
	// Button builds a button of the app's (重试); MyGo's when nil.
	Button func(c *ui.Context, label string) ui.Element
	// Missing builds the picture of a picture that did not load; the
	// pictogram's first drawing when nil.
	Missing func(c *ui.Context) ui.Element
	// Focus, set, gives the reader the keyboard focus in its next frame, as
	// on arrival: the keys that scroll it come to it, and to the app's
	// observers, wherever the pointer is. Tab passes over it (tabindex -1).
	Focus bool

	Scroll ui.ScrollState

	clips []*Clip
	// A move the reader was sent on (the saved place, a heading, a link's
	// target) rather than one it was scrolled on, and whether a build has
	// seen where it went.
	sending, landed bool
	marks           []*Clip // the library's clips, found by their words (SetMarks)
	markKey         string
	restore         *Position
	jump            string // the anchor a #link asked for
	copied          string // the code whose button shows 已复制
	copiedAt        time.Time
	gen             int // the body's generation: a new one lets go of the selection

	// What the last build saw: its paragraphs by element ID, the tops of
	// units and anchors in the body, and the boxes of the frame before.
	paras    map[uint64]*para
	units    []unitTop
	anchors  map[string]float32
	anchorH  map[string]float32 // and how tall the anchored elements are
	view     ui.Rect            // the scroll container
	body     ui.Rect
	shownY   float32 // the offset the frame before was laid out at
	top      float32 // where the body starts in the scrolled content
	endGap   float32 // between the body and what follows it
	selected []ui.SelectedText
	row      int
	// The top-level blocks' boxes and the hidden picture's box, as the frame
	// before laid them out.
	tops   []ui.Rect
	hidden ui.Rect
	sides  map[string]*ui.ScrollState // the app's sideways scrollers (Aside), by key

	// A new size or measure: what the last
	// layout was for, how many each has had (the keys of the text laid out
	// anew), and the crossing on its way.
	laidSize, laidWidth float32
	measures, layouts   int
	flow                reflow
	held                *held    // the block a live change holds in place
	fl                  *flowing // the text flowing over to a new measure (flow.go)
	heldTop             float32  // where the held block's top is in the window, as the frame before laid it out
	heldFound           bool
	picBox              map[*block]ui.Rect // where the pictures were, as the frame before laid them out
	exitInstant         bool               // what the text built last would go as
}

// reflow is the text going over to a new size or measure: a copy of the
// old lets go where it was while the new comes in, the scroll kept, so
// that the copy stays put; the new is laid out a frame later, then shows
// shift further up, as far as keeps the place being read where it was,
// until the copy is gone and the scroll takes the shift.
type reflow struct {
	on, instant bool
	at          time.Time // the frame it began in
	block       int       // the top-level block the guide was in, or -1
	frac        float32   // how far into it
	measured    bool
	shift       float32
	pad         float32 // room under the text, so that the scroll stays where the copy was
}

// held is the top-level block a live change of size or measure keeps where
// it showed, off below the top of the reader, and how many frames the size
// and measure have stood still since.
type held struct {
	block  *block
	off    float32
	still  int
	glide  *ui.Animation // a flow's scroll gliding there, asked once; nil to line it up at once, every frame
	called bool
}

// laid is what a frame's layout told of the body, kept over the frame the
// text crosses in, whose new text is not laid out yet.
type laid struct {
	units   []unitTop
	anchors map[string]float32
	tops    []ui.Rect
	body    ui.Rect
	top     float32
}

// para is a paragraph the last build made: the block, and what goes
// between it and the paragraph before in a copy.
type para struct {
	b   *block
	sep string // the line breaks innerText gives it: "\n\n" for a p, "\n" for other blocks
	row int    // a table cell's row, whose cells a tab separates; 0 for none
	// Where the frame before laid it out, and how its text is set, to find
	// the selection's place in it (SelectionBox).
	box   ui.Rect
	segs  []segment
	font  ui.Font
	color ui.Color
}

type unitTop struct {
	id  string
	top float32
}

// textCtx is what a container passes on to the text inside it.
type textCtx struct {
	size       float32
	color      ui.Color
	weight     int
	sep        string
	row        int
	lineHeight float32  // in place of the body's, when set (a table's)
	align      ui.Align // the lines' alignment (a table's figures to the right)
	numbers    bool     // figures in even widths
}

// NewReader makes the reader of an article whose body Prepare made.
func NewReader(a *Article, c *Content, b *Body) *Reader {
	return &Reader{Article: a, Content: c, Body: b, Size: theme.ReadingSize}
}

// face is how the article's text is set (Face); Chinese's for a body not
// made by Prepare.
func (r *Reader) face() Face {
	if r.Body == nil || r.Body.Face.Family == "" {
		return chineseFace
	}
	return r.Body.Face
}

// textSize is the size of the body's text: the reader's size (Size), as
// large as the article's face looks at it.
func (r *Reader) textSize() float32 { return r.Size * r.face().Scale }

func (r *Reader) em() float32 { return r.textSize() }

// View builds the reader, filling its parent.
func (r *Reader) View(c *ui.Context) {
	if r.Size == 0 {
		r.Size = theme.ReadingSize
	}
	// A move it was sent on ends once a build has seen where it went
	// (sendReader's data-sent, until scrollend): what moves after that, the
	// reader moved, though no frame came between.
	if r.sending && r.landed {
		r.sending = false
	}
	r.restoreNow(c)
	r.landed = r.sending && !r.Scroll.Scrolling()
	r.hidden = ui.Rect{}
	// A flow whose time is up ends before the body is built, so that this frame shows the words and the pictures
	// themselves where the last one painted them on their way (flow.go).
	if f := r.fl; f != nil {
		if f.now = c.Now(); f.done {
			r.fl = nil
		}
	}
	r.heldFound = false
	sc := ui.Scroll(c.Key(r.Article.ID)).TrackScroll(&r.Scroll).Grow(1).FillWidth().Scrollbars(false).TabStop(false).FocusRing(false)
	if r.Focus {
		sc.Focus()
		r.Focus = false
	}
	r.view = sc.LayoutBounds()
	var head, text ui.Element
	crossed := false // the text began crossing to a new size or measure in this frame
	var kept laid
	sc.Children(func() {
		// The app's own head starts with its bar, 52 high and 32 over the
		// title; the reader's own keeps that room empty.
		top := float32(84)
		if r.Above != nil || r.Bar != nil {
			top = 0
		}
		col := ui.Column(c).FillWidth().AlignSelf(ui.Center).Padding(top, 40, 180, 40).Font(theme.Sans)
		col.Children(func() {
			// The bar's reading options may change the size or the
			// measure: the text after it is laid out for what they say.
			if r.Bar != nil {
				r.Bar()
			}
			r.reflow(c)
			// The frame the text crosses in shows the old layout: its
			// records stay.
			crossed = r.flow.on && c.Now().Equal(r.flow.at)
			if crossed {
				kept = laid{slices.Clone(r.units), maps.Clone(r.anchors), slices.Clone(r.tops), r.body, r.top}
			}
			clear(r.paras)
			if r.paras == nil {
				r.paras, r.anchors, r.anchorH = map[uint64]*para{}, map[string]float32{}, map[string]float32{}
			}
			clear(r.anchors)
			clear(r.anchorH)
			if r.picBox == nil {
				r.picBox = map[*block]ui.Rect{}
			}
			clear(r.picBox)
			r.units, r.row, r.tops = r.units[:0], 0, r.tops[:0]
			// What a new measure lays out anew, and what a new size does too.
			t := r.reflowTransition(c)
			gap := float32(36)
			head = ui.Column(c.Key(fmt.Sprint("head:", r.measures))).Transition(t).Children(func() {
				if r.Above != nil {
					gap = r.Above()
				} else {
					r.header(c)
				}
			})
			text = ui.Column(c.Key(fmt.Sprint("text:", r.layouts))).Transition(t).Children(func() {
				bs, em := r.Body.blocks, r.em()
				first, last := float32(0), float32(0)
				if len(bs) > 0 {
					first, _ = outer(bs[0])
					_, last = outer(bs[len(bs)-1])
					if bs[0].kind == Heading {
						first = 0 // a body opening on a heading keeps the head's own room above it
					}
				}
				first, last = first*em, last*em
				body := ui.Column(c.Key(r.gen)).Selectable().SelectionColor(theme.AccentSoft).CopyData(r.copyData).
					Font(r.face().Family).FontSize(r.textSize()).LineHeight(r.face().LineHeight).TextColor(theme.Text)
				if r.fl != nil {
					body.DrawOver(r.paintFlow) // the words on their way (flow.go)
				}
				if r.Name != "" {
					body.Role(ui.RoleArticle).Label(r.Name)
				}
				r.body = body.LayoutBounds()
				r.top = r.body.Y - r.view.Y + r.shownY
				body.Children(func() {
					tc := textCtx{size: r.textSize(), color: theme.Text, sep: "\n\n"}
					build := func(_ int, b *block) ui.Element {
						e := r.block(c, b, tc)
						r.tops = append(r.tops, e.LayoutBounds())
						return e
					}
					if r.Insert == nil {
						r.children(c, bs, true, tc, build)
						return
					}
					// ponytail: the blocks flatten wrappers, so an h2 inside a div
					// counts here, not only one at the body's top level.
					// Galleries' bodies are one p.
					at := len(bs)
					for i, b := range bs {
						if b.kind == Heading && b.level == 2 {
							at = i
							break
						}
					}
					r.children(c, bs[:at], true, tc, build)
					// The insert is not the body's words: none of it is selected.
					in := ui.Column(c.Key("insert")).Unselectable()
					var top, bottom float32
					in.Children(func() { top, bottom = r.Insert() })
					above, below := float32(0), bottom
					if at > 0 {
						_, m := outer(bs[at-1])
						if at == len(bs) && bs[at-1].kind == Paragraph {
							// No longer the body's last child, a paragraph keeps its
							// 1.15em (p:last-child). ponytail: a body ending in loose
							// text or a div's last p keeps none in CSS; galleries'
							// bodies are one p.
							m = 1.15
						}
						above = max(m*em, top)
					} else {
						first = top
					}
					if at < len(bs) {
						m, _ := outer(bs[at])
						below = max(bottom, m*em)
					} else {
						last = bottom
					}
					in.Margin(above, 0, below, 0)
					r.children(c, bs[at:], true, tc, build)
				})
				body.Margin(max(gap, first), 0, 0, 0)
				r.selected = body.SelectedTexts()
				r.endGap = max(48, last)
				if r.Below != nil {
					r.Below()
				}
			})
			if r.flow.on {
				ui.Box(c.Key("reflow-room")).Height(r.flow.pad).AccessHidden(true)
			}
		})
		col.MaxWidth(r.width() + 80)
		if r.fl != nil {
			col.Translate(r.FlowShift(), 0) // flowing, the column slides from its old place to its new one
		}
	})
	for i := range r.units {
		r.units[i].top -= r.body.Y
	}
	for k, v := range r.anchors {
		r.anchors[k] = v - r.body.Y
	}
	for i := range r.tops {
		r.tops[i].Y -= r.body.Y
	}
	switch f := &r.flow; {
	case crossed:
		r.units, r.anchors, r.tops, r.body, r.top = kept.units, kept.anchors, kept.tops, kept.body, kept.top
	case f.on && !f.measured:
		r.measure()
	}
	if f := r.flow; f.on && f.measured && f.shift != 0 {
		head.Translate(0, -f.shift)
		text.Translate(0, -f.shift)
	}
	r.exitInstant = r.Instant
	if r.jump != "" {
		if top, ok := r.anchors[r.jump]; ok && r.body.H > 0 {
			r.Scroll.ScrollTo(0, max(0, r.bodyTop()+top-70), ui.Smooth)
			r.sending, r.landed = true, false
		}
		r.jump = ""
	}
	r.flowOn(c)
	r.shownY = r.Scroll.Y
}

// bodyTop is where the body starts in the scrolled content, as the frame
// before laid it out, and as a crossing to a new size or measure shows it.
func (r *Reader) bodyTop() float32 {
	if f := r.flow; f.on && f.measured {
		return r.top - f.shift
	}
	return r.top
}

// reflow notes a new size or measure, as the column begins after the bar:
// the place being read (the top-level block at the guide, and how far into
// it), and the text to lay out anew; and ends the crossing on its way once
// the old text is gone, the scroll taking its shift.
func (r *Reader) reflow(c *ui.Context) {
	now := c.Now()
	if f := &r.flow; f.on && f.measured {
		if end := f.at.Add(motion.ReflowOut); now.Before(end) {
			c.After(end.Sub(now))
		} else {
			if f.shift != 0 {
				r.Scroll.Y = max(0, r.Scroll.Y+f.shift)
				r.sending, r.landed = true, false
			}
			*f = reflow{}
		}
	}
	w := r.width()
	if r.laidSize == 0 {
		r.laidSize, r.laidWidth = r.Size, w
	}
	if r.Size == r.laidSize && w == r.laidWidth {
		// A live change holds its block a frame after it stands still, then lets go.
		if h := r.held; h != nil {
			if h.still++; h.still > 1 && r.fl == nil { // a flow holds it till it ends
				r.held = nil
			}
		}
		return
	}
	if r.Flow && w != r.laidWidth && r.Size == r.laidSize && !r.flow.on && r.fl == nil {
		r.startFlow(c, r.laidWidth)
		r.laidWidth = w
		return
	}
	if r.Live && !r.flow.on {
		r.laidSize, r.laidWidth = r.Size, w
		// The selection stays: letting it go would key the body anew, and its blocks would show unlaid for a frame.
		if r.held == nil {
			r.held = r.holdAt()
		}
		if r.held != nil {
			r.held.still = 0
		}
		return
	}
	r.held = nil
	measure := w != r.laidWidth
	r.laidSize, r.laidWidth = r.Size, w
	if r.body.H <= 0 || len(r.tops) == 0 {
		return // nothing laid out yet, nor a place to keep
	}
	relative := r.Scroll.Y + guide - r.bodyTop()
	block, frac := -1, float32(0)
	for i, t := range r.tops {
		if t.Y > relative {
			break
		}
		block = i
	}
	if block >= 0 {
		t := r.tops[block]
		frac = max(0, min(1, (relative-t.Y)/max(1, r.reach(block))))
	}
	if measure {
		r.measures++
	}
	r.layouts++
	r.flow = reflow{on: true, instant: r.exitInstant, at: now, block: block, frac: frac, pad: r.Scroll.MaxY + r.view.H}
	r.ClearSelection()
	c.Invalidate() // laid out, it is measured in the next frame
}

// Settling reports whether a live change of size or measure is under way: the text is laid out anew each frame,
// and where its blocks were in the frame before says little of where they are now.
func (r *Reader) Settling() bool { return r.held != nil || r.fl != nil }

// reach is how far top-level block i reaches down: to the next block's top, its margin below with it, which grows
// with the text as the block does, so that a place in the room between two blocks is kept as one in a block is.
func (r *Reader) reach(i int) float32 {
	if i+1 < len(r.tops) {
		return r.tops[i+1].Y - r.tops[i].Y
	}
	return r.tops[i].H
}

// holdAt is the block a live change holds: the top-level block at the guide,
// or the first below it when its top is above the reader's; nil when none
// shows, or at the top of the page.
func (r *Reader) holdAt() *held {
	// At the top of the page the page stays at its top, the text growing down from there.
	if r.body.H <= 0 || len(r.tops) == 0 || r.Scroll.Y <= 0 {
		return nil
	}
	relative := r.Scroll.Y + guide - r.bodyTop()
	at := -1
	for i, t := range r.tops {
		if t.Y > relative {
			break
		}
		at = i
	}
	for i := max(0, at); i < len(r.tops) && i < len(r.Body.blocks); i++ {
		if off := r.bodyTop() + r.tops[i].Y - r.Scroll.Y; off >= 0 {
			return &held{block: r.Body.blocks[i], off: off}
		}
	}
	return nil
}

// measure finds, in the frame after the text crossed, where its new layout
// puts the place being read, and how far that is from where it showed.
func (r *Reader) measure() {
	f := &r.flow
	f.measured = true
	if f.block >= 0 && f.block < len(r.tops) {
		t := r.tops[f.block]
		f.shift = max(0, r.top+t.Y+f.frac*r.reach(f.block)-guide) - r.Scroll.Y
	}
	if f.instant {
		// The old text showed for one frame: the scroll takes the shift now.
		if f.shift != 0 {
			r.Scroll.Y = max(0, r.Scroll.Y+f.shift)
			r.sending, r.landed = true, false
		}
		*f = reflow{}
	}
}

// reflowTransition is how the text a new size or measure lays out anew
// goes and comes (motion.Reflow): in the frame a crossing begins, the new
// comes in as it decided; otherwise nothing comes in, and what would go
// goes as the moment would have it.
func (r *Reader) reflowTransition(c *ui.Context) ui.ElementTransition {
	t := motion.Reflow(r.Instant)
	if f := r.flow; f.on && c.Now().Equal(f.at) {
		in := motion.Reflow(f.instant)
		t.Duration, t.Ease = in.Duration, in.Ease
	} else {
		t.Enter = nil
	}
	return t
}

func (r *Reader) width() float32 {
	if r.Width > 0 {
		return r.Width
	}
	return Column
}

// EndGap is the room between the body and what follows it: 48, or the last
// block's own margin when larger, as CSS collapses them.
func (r *Reader) EndGap() float32 { return r.endGap }

// AnchorY is where the element of id (a heading's) starts in the scrolled
// content, as the frame before laid it out.
func (r *Reader) AnchorY(id string) (float32, bool) {
	top, ok := r.anchors[id]
	return r.bodyTop() + top, ok
}

// AnchorBox is where the element of id (a heading's) starts in the scrolled
// content and how tall it is, as the frame before laid it out.
func (r *Reader) AnchorBox(id string) (y, h float32, ok bool) {
	top, ok := r.anchors[id]
	return r.bodyTop() + top, r.anchorH[id], ok
}

// HeadingView builds the heading anchored at id as the body sets it, its
// words spaced as there, for the app to show elsewhere; false when the body
// has none.
func (r *Reader) HeadingView(c *ui.Context, id string) (ui.Element, bool) {
	var find func(bs []*block) *block
	find = func(bs []*block) *block {
		for _, b := range bs {
			if b.kind == Heading && b.anchor == id {
				return b
			}
			if f := find(b.children); f != nil {
				return f
			}
		}
		return nil
	}
	b := find(r.Body.blocks)
	if b == nil {
		return ui.Element{}, false
	}
	f, color := r.HeadingFont(b.level), theme.Text
	if headingStyles[max(2, min(6, b.level))].quiet {
		color = theme.Text2
	}
	sp := spans(r.segments(b), f.Size)
	return ui.RichText(c, sp...).Font(f.Family).FontSize(f.Size).LineHeight(f.LineHeight).
		FontWeight(f.Weight).TextColor(color).DrawObjects(drawGlyphs(sp, color)), true
}

// HeadingFont is how the body sets a heading of level (2 to 6, as the body
// settles them), in the article's face: at 17, 24, 20, 17, 15 and 15.
func (r *Reader) HeadingFont(level int) ui.Font {
	s := headingStyles[max(2, min(6, level))]
	return ui.Font{Family: r.face().Family, Weight: 600, Size: s.size * r.textSize(), LineHeight: s.lineHeight}
}

// Jump scrolls to the element of id, 70 below the top, gliding unless told
// not to.
func (r *Reader) Jump(id string, smooth bool) {
	if y, ok := r.AnchorY(id); ok && r.body.H > 0 {
		if smooth {
			r.Scroll.ScrollTo(0, max(0, y-70), ui.Smooth)
		} else {
			r.Scroll.Y = max(0, min(r.Scroll.MaxY, y-70))
		}
		r.sending, r.landed = true, false
	}
}

// Sent reports whether the reader is on a move it was sent on, restoring
// the saved place or going to a heading or a link's target, rather than
// one it was scrolled on: reading on does not count it (sendReader's
// data-sent, until the scroll ends).
func (r *Reader) Sent() bool { return r.sending }

// Headline is the article's title as the reader sets it (DESIGN.md
// Headline), width wide, balanced, in the face of its own language: the
// page it flies in from knows the title, not the body.
func Headline(c *ui.Context, title string, width float32) ui.Element {
	// At 32 in any face: the title flies in at the ratio of 32 to the size
	// it left.
	return Lines(c, title, width, ui.Font{Family: titleFace(title).Family, Size: theme.Headline, Weight: 600, LineHeight: 1.3}, -0.012*theme.Headline, true)
}

type linesKey struct {
	text    string
	width   float32
	font    ui.Font
	spacing float32
	balance bool
}

// lines keeps how texts broke into lines, by text and width.
var lines = struct {
	sync.Mutex
	m map[linesKey][]ui.TextLine
}{m: map[linesKey][]ui.TextLine{}}

// Lines sets text in lines exactly f.LineHeight high, as Chrome sets a
// line-height: MyGo lets no line be lower than its fonts reach, and PingFang
// SC reaches 1.4 em, which would set a Chinese headline (1.3) 3.2 lower a
// line. Each line is its own text, as high as Chrome's line, its baseline
// where Chrome's is: the half-leading of the first font's own height. Before
// the width is known, the text shows whole.
func Lines(c *ui.Context, text string, width float32, f ui.Font, spacing float32, balance bool) ui.Element {
	lh := f.LineHeight * f.Size
	style := func(e ui.Element) ui.Element {
		e.FontSize(f.Size).FontWeight(f.Weight).LetterSpacing(spacing)
		if f.Family != "" {
			e.Font(f.Family)
		}
		return e
	}
	if width <= 0 {
		return style(ui.Text(c, text)).LineHeight(f.LineHeight)
	}
	key := linesKey{text, width, f, spacing, balance}
	lines.Lock()
	ls, ok := lines.m[key]
	lines.Unlock()
	if !ok {
		// Shaping is the costly part: done once a text and a width.
		l := ui.ShapeRichTextWith([]ui.Span{{Text: text, LetterSpacing: spacing}}, f, ui.TextShape{Width: width, Balance: balance})
		ls = l.Lines()
		lines.Lock()
		if len(lines.m) > 256 {
			clear(lines.m)
		}
		lines.m[key] = ls
		lines.Unlock()
	}
	primary := ui.Font{Family: "system-ui", Size: f.Size, Weight: f.Weight}.Metrics()
	baseline := (lh-primary.Ascent-primary.Descent)/2 + primary.Ascent
	units := utf16.Encode([]rune(text))
	box := ui.Column(c).Label(text)
	return box.Children(func() {
		for i, ln := range ls {
			part := string(utf16.Decode(units[min(ln.Start, len(units)):min(ln.End, len(units))]))
			part = strings.TrimRight(part, " ")
			shift := baseline - (ln.Baseline - ln.Rect.Y)
			t := style(ui.Text(c.Key(i), part)).LineHeight(f.LineHeight).NoWrap().Height(lh).AccessHidden(true)
			if shift != 0 {
				t.Translate(0, shift)
			}
		}
	})
}

func (r *Reader) header(c *ui.Context) {
	a := r.Article
	title := a.Title
	if title == "" {
		title = locales.ReaderNoTitle()
	}
	ui.Column(c).Children(func() {
		Headline(c, title, r.width())
		ui.Row(c).Margin(12, 0, 0, 0).Gap(8).Wrap().FontSize(theme.Body).TextColor(theme.Muted).FontFeatures(theme.Numbers).Children(func() {
			ui.Text(c, a.SourceName).TextColor(theme.Text2)
			ui.Text(c, "·")
			ui.Text(c, metaDate(a.PublishedAt, a.ModifiedAt))
			if r.Content.Status == "ready" && r.Body.Minutes > 0 {
				ui.Text(c, "·")
				ui.Text(c, locales.ReaderMinutes(r.Body.Minutes))
			}
		})
	})
	if notice := r.notice(); notice != "" {
		ui.Row(c).Margin(24, 0, 0, 0).Padding(12, 0).Gap(12).BorderWidth(1, 0, 1, 0).BorderColor(theme.LineSoft).
			FontSize(theme.Body).LineHeight(1.7).TextColor(theme.Text2).Children(func() {
			ui.Text(c, notice).Shrink(1)
			if r.Article.URL != "" {
				r.original(c)
			}
		})
	}
}

func (r *Reader) notice() string {
	switch s := r.Content.Status; {
	case s == "ready":
		return ""
	case r.Content.Message != "":
		return r.Content.Message
	case s == "summary":
		return locales.ReaderSummaryOnly()
	case s == "restricted":
		return locales.ReaderRestricted()
	}
	return locales.ReaderTextUnavailable()
}

// original is 阅读原文: the article on its site, which OnLink opens.
func (r *Reader) original(c *ui.Context) {
	url := r.Article.URL
	if r.OnLink != nil {
		url = ""
	}
	l := ui.Link(c, locales.ReaderReadOriginal(), url).TextColor(theme.Text2)
	if l.Clicked() && r.OnLink != nil {
		r.OnLink(r.Article.URL)
	}
}

// outer is a block's margins once those of its first and last children
// collapse through it, as CSS collapses them through a box without padding.
func outer(b *block) (top, bottom float32) {
	top, bottom = b.top, b.bottom
	if (b.kind == List || b.kind == Item || b.kind == Figure) && len(b.children) > 0 {
		t, _ := outer(b.children[0])
		_, bt := outer(b.children[len(b.children)-1])
		top, bottom = max(top, t), max(bottom, bt)
	}
	return
}

// children builds blocks one under another with the margins between them
// collapsed; a container that collapses passes its children's outer
// margins on, as outer reports them, instead of spacing them.
func (r *Reader) children(c *ui.Context, bs []*block, collapse bool, tc textCtx, build func(i int, b *block) ui.Element) {
	for i, b := range bs {
		top, bottom := outer(b)
		gap := top
		switch {
		case i > 0 && bs[i-1].kind == Heading && b.kind == Heading:
			// A heading right under another belongs with it: as near as the one above leaves room for.
			_, before := outer(bs[i-1])
			gap = max(before, min(top, .9))
		case i > 0:
			_, before := outer(bs[i-1])
			gap = max(before, top)
		case collapse:
			gap = 0
		}
		// Each top-level block has a mark before it, empty, always there: adding one would move every block
		// after it to another place among the body's children, and they would lose their layout for a frame.
		// The block a live change holds has its mark run from off above its top to its top, which the scroll
		// lines up with its own top once the frame is laid out, so the block stays where it showed whatever
		// came above it; the block's margin is the mark's.
		if build != nil {
			mark := ui.Box(c).AccessHidden(true)
			if h := r.held; h != nil && b == h.block {
				mark.Height(h.off).Margin(gap*r.em()-h.off, 0, 0, 0)
				switch {
				case h.glide == nil:
					mark.ScrollIntoView(ui.ScrollIntoViewOptions{Block: ui.ScrollStart})
				case !h.called:
					mark.ScrollIntoView(ui.ScrollIntoViewOptions{Block: ui.ScrollStart, Animation: h.glide})
					h.called = true
				}
				gap = 0
			}
		}
		var e ui.Element
		if build != nil {
			e = build(i, b)
		} else {
			e = r.block(c, b, tc)
		}
		after := float32(0)
		if i == len(bs)-1 && !collapse {
			after = bottom
		}
		e.Margin(gap*r.em(), 0, after*r.em(), 0)
		// Flowing, code and rules glide to their new places; the words and the pictures flow by themselves
		// (flow.go).
		if r.fl != nil && (b.kind == Code || b.kind == Rule) {
			e.Transition(ui.ElementTransition{Animation: &ui.Animation{Duration: flowTime, Ease: motion.TravelEase}, Position: true, Width: true})
		}
		if h := r.held; h != nil && b == h.block {
			r.heldTop, r.heldFound = e.LayoutBounds().Y, true
		}
		if b.unit != "" || b.anchor != "" {
			y := e.LayoutBounds().Y
			if b.unit != "" && (len(r.units) == 0 || r.units[len(r.units)-1].id != b.unit) {
				r.units = append(r.units, unitTop{b.unit, y})
			}
			if b.anchor != "" {
				r.anchors[b.anchor] = y
				r.anchorH[b.anchor] = e.LayoutBounds().H
			}
		}
	}
}

func (r *Reader) block(c *ui.Context, b *block, tc textCtx) ui.Element {
	switch b.kind {
	case List:
		return ui.Column(c).Children(func() {
			r.children(c, b.children, true, tc, func(i int, it *block) ui.Element { return r.item(c, b, i, it, tc) })
		})
	case Quote:
		return r.quote(c, b, tc)
	case Figure:
		return ui.Column(c).Children(func() { r.children(c, b.children, true, tc, nil) })
	case Code:
		return r.code(c, b)
	case Table:
		return r.table(c, b, tc)
	case Picture:
		return r.picture(c, b)
	case Rule:
		// A break between parts: a short rule in the middle, 64 wide.
		return ui.Box(c).Height(1).Width(64).AlignSelf(ui.Center).Background(theme.Faint)
	}
	return r.paragraph(c, b, tc)
}

func (r *Reader) item(c *ui.Context, l *block, i int, it *block, tc textCtx) ui.Element {
	marker := [3]string{"•", "◦", "▪"}[min(l.level, 2)]
	if l.ordered {
		marker = fmt.Sprintf("%d.", l.start+i)
	}
	tc.sep = "\n"
	return ui.Row(c).AlignItems(ui.Start).Children(func() {
		ui.Text(c, marker).Width(1.5 * tc.size).Shrink(0).TextAlign(ui.End).PaddingEnd(0.3 * tc.size).
			FontSize(tc.size).TextColor(tc.color).FontFeatures(theme.Numbers).Unselectable()
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Children(func() { r.children(c, it.children, true, tc, nil) })
	})
}

func (r *Reader) quote(c *ui.Context, b *block, tc textCtx) ui.Element {
	// A quote stands by a grey rule, in the secondary text colour; a callout by a rule in its kind's colour, under its
	// kind's name in that colour, its words in the text's own.
	q := ui.Column(c).BorderWidth(0, 0, 0, 2).BorderColor(theme.QuoteRule).Padding(2, 0, 2, 16)
	label, callout := calloutLabels[b.callout]
	color, ok := calloutColors[b.callout]
	if !ok {
		color = theme.Danger
	}
	tc.color = theme.Text2
	if callout {
		q.BorderColor(color)
		tc.color = theme.Text
	}
	return q.Children(func() {
		if callout {
			size := theme.Body * r.textSize() / theme.ReadingSize
			ui.Text(c, label()).Font(theme.Sans).FontSize(size).FontWeight(600).TextColor(color).Margin(0, 0, .3*size, 0)
		}
		r.children(c, b.children, false, tc, nil)
	})
}

// paragraph builds the text of a paragraph, heading or caption: one rich
// text, its links within it.
func (r *Reader) paragraph(c *ui.Context, b *block, tc textCtx) ui.Element {
	size, weight, lh, color := tc.size, tc.weight, r.face().LineHeight, tc.color
	if tc.lineHeight > 0 {
		lh = tc.lineHeight
	}
	switch b.kind {
	case Heading:
		f := r.HeadingFont(b.level)
		size, weight, lh, color = f.Size, f.Weight, f.LineHeight, theme.Text
		if headingStyles[max(2, min(6, b.level))].quiet {
			color = theme.Text2
		}
	case Caption:
		size, color = theme.Body*r.textSize()/theme.ReadingSize, theme.Muted
	}
	segs := r.segments(b)
	var e ui.Element
	switch {
	case r.fl != nil:
		// Flowing, its words are laid out unseen where the new measure puts them; the flow paints them on their way.
		e = ui.RichText(c, ghost(spans(segs, size))...)
	case !hasLink(segs):
		e = ui.RichText(c, spans(segs, size)...)
	default:
		links, li := linkSpans(segs), 0
		e = ui.RichText(c).Children(func() {
			for i := 0; i < len(segs); {
				j := i + 1
				for j < len(segs) && segs[j].href == segs[i].href {
					j++
				}
				if segs[i].href == "" {
					ui.RichText(c, spans(segs[i:j], size)...)
				} else {
					links[li].k = r.link(c, segs[i:j], size, color)
					li++
				}
				i = j
			}
		})
		sp := spans(segs, size)
		if hasGlyph(segs) {
			e.DrawObjects(drawGlyphs(sp, color))
		}
		// The links' light, under their words: the paragraph laid out as the body lays it out.
		font := ui.Font{Family: r.face().Family, Size: size, Weight: weight, LineHeight: lh}
		if tc.numbers {
			font.Features = theme.Numbers
		}
		shape := ui.TextShape{Align: tc.align, Pretty: b.kind == Paragraph && !hasWide(b.runs)}
		text := utf16Of(sp)
		e.Draw(func(pt *ui.Painter, box ui.Rect) {
			paintMarkers(pt, box.X, box.Y, b.markerLayout(box.W, size, sp, font, shape), text, size, links)
		})
	}
	e.FontSize(size).LineHeight(lh).TextColor(color)
	if weight > 0 {
		e.FontWeight(weight)
	}
	if tc.align != ui.Start {
		e.TextAlign(tc.align)
	}
	if tc.numbers {
		e.FontFeatures(theme.Numbers)
	}
	if b.kind == Paragraph && !hasWide(b.runs) {
		// MyGo's Pretty counts the words between spaces: a run of Chinese
		// is one word to it, and it moves whole clauses to the last line.
		e.Pretty()
	}
	sep := tc.sep
	if b.kind != Paragraph {
		sep = "\n"
	}
	if r.fl != nil {
		e.TextColor(ui.Transparent) // its words flow over it (flow.go)
	}
	r.paras[e.ID()] = &para{b: b, sep: sep, row: tc.row, box: e.LayoutBounds(), segs: segs, color: color,
		font: ui.Font{Family: r.face().Family, Size: size, Weight: weight, LineHeight: lh}}
	return e
}

// link builds a link of the body and returns how far it is lit, 0 to 1: pointed at or focused, the light under it
// rises over its words and deepens, and settles again as it is left (paintMarkers). From the keys and under less
// motion it changes at once.
func (r *Reader) link(c *ui.Context, segs []segment, size float32, color ui.Color) float32 {
	href := segs[0].href
	url := href
	if r.OnLink != nil || strings.HasPrefix(href, "#") {
		url = "" // the app decides what a click does
	}
	// Its light marks it (paintMarkers): the underline a link takes pointed at is drawn in no colour.
	l := ui.Link(c, "", url).Children(func() { ui.RichText(c, spans(segs, size)...) }).TextColor(color).DecorationColor(ui.Transparent)
	on := l.Hovered() || l.FocusVisible()
	target := map[bool]float32{true: 1, false: 0}[on]
	var k float32
	switch {
	case r.Instant || c.Preferences().ReduceMotion:
		k = l.Jump("sweep", target)
	case on:
		k = l.AnimateTo("sweep", target, ui.Animation{Duration: motion.State, Ease: motion.Ease})
	default:
		k = l.AnimateTo("sweep", target, ui.Animation{Duration: motion.Hover, Ease: motion.Ease})
	}
	if l.Clicked() {
		switch {
		case strings.HasPrefix(href, "#"):
			r.jump = href[1:]
			if s, err := urlUnescape(r.jump); err == nil {
				r.jump = s
			}
		case r.OnLink != nil:
			r.OnLink(href)
		}
	}
	return k
}

// segment is a run, or the part of one a clip covers.
type segment struct {
	run
	marked  bool
	cjkFont string // the face its Chinese and Japanese are drawn in (Face.CJK)
}

// segments cuts a paragraph's runs where the clips made in this session
// begin and end.
func (r *Reader) segments(b *block) []segment {
	var marks [][2]int
	for _, cl := range append(r.clips[:len(r.clips):len(r.clips)], r.marks...) {
		for _, p := range cl.parts {
			if p.b == b {
				marks = append(marks, [2]int{p.from, p.to})
			}
		}
	}
	out := make([]segment, 0, len(b.runs))
	at, cjkFont := 0, r.face().CJK
	for _, rn := range b.runs {
		n := utf8.RuneCountInString(rn.text)
		if len(marks) == 0 {
			out = append(out, segment{run: rn, cjkFont: cjkFont})
			at += n
			continue
		}
		cuts := []int{0, n}
		for _, m := range marks {
			for _, x := range m {
				if x-at > 0 && x-at < n {
					cuts = append(cuts, x-at)
				}
			}
		}
		sortInts(cuts)
		runes := []rune(rn.text)
		for i := 0; i+1 < len(cuts); i++ {
			if cuts[i] == cuts[i+1] {
				continue
			}
			piece := rn
			piece.text = string(runes[cuts[i]:cuts[i+1]])
			mid := at + cuts[i]
			marked := false
			for _, m := range marks {
				marked = marked || (mid >= m[0] && mid < m[1])
			}
			out = append(out, segment{run: piece, marked: marked, cjkFont: cjkFont})
		}
		at += n
	}
	return out
}

func sortInts(s []int) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func hasLink(segs []segment) bool {
	for _, s := range segs {
		if s.href != "" {
			return true
		}
	}
	return false
}

// spans styles segments by their tags, in a paragraph of the given size.
// hasGlyph reports whether segs hold a site's mark.
func hasGlyph(segs []segment) bool {
	for _, sg := range segs {
		if sg.style&glyph != 0 {
			return true
		}
	}
	return false
}

func spans(segs []segment, size float32) []ui.Span {
	out := make([]ui.Span, 0, len(segs))
	for _, sg := range segs {
		if sg.style&glyph != 0 {
			out = append(out, glyphSpan(sg.glyph, size))
			continue
		}
		s := ui.Span{Text: sg.text}
		st := sg.style
		if st&bold != 0 {
			s.Weight = 700
		}
		s.Italic = st&italic != 0
		s.Strikethrough = st&strike != 0
		at := size
		if st&mono != 0 {
			at = size * theme.Item / theme.ReadingSize
			s.Font, s.Size, s.Background, s.Radius, s.PaddingX, s.PaddingY = theme.Mono, at, theme.Selected, 6, 5, 2
		}
		if st&cjk != 0 {
			s.Font = cmp.Or(sg.cjkFont, cjkFace)
		}
		if st&autospace != 0 {
			s.LetterSpacing = size / 8
		}
		if st&halfwidth != 0 {
			s.Features = "halt"
		}
		// Superscripts and subscripts take the system face's own figures
		// and letters for them; text it has none for is set smaller in
		// place. A raising BaselineShift draws below the baseline in MyGo
		// 0.8.3 (a lowering one is right), so none is used.
		switch {
		case st&sup != 0 && asciiWord(sg.text):
			s.Features = "sups"
		case st&sub != 0 && asciiWord(sg.text):
			s.Features = "subs"
		case st&(sup|sub) != 0:
			s.Size = at * 0.83
		}
		if sg.marked {
			// A passage already clipped: the accent-soft ground with an
			// accent line under it (DESIGN.md Clip Card).
			s.Background, s.Underline, s.DecorationColor, s.DecorationThickness, s.UnderlineOffset = theme.AccentSoft, true, theme.Accent, 1.5, size*0.3
		}
		out = append(out, s)
	}
	return out
}

func (r *Reader) code(c *ui.Context, b *block) ui.Element {
	cv := b.code
	return ui.Column(c).Background(theme.CodeBackground).Radius(10).Border(1, theme.LineSoft).Children(func() {
		if cv.label != "" {
			ui.Row(c).Height(32).Padding(0, 4, 0, 16).BorderWidth(0, 0, 1, 0).BorderColor(theme.LineSoft).Unselectable().Children(func() {
				label := cv.label
				if label == plainText {
					label = locales.ReaderPlainText()
				}
				ui.Text(c, label).Font(theme.Mono).FontSize(theme.Label).LetterSpacing(0.24).TextColor(theme.Muted).Grow(1)
				r.copyButton(c, cv)
			})
		}
		ui.Column(c).Children(func() {
			side := ui.ScrollHorizontal(c).TrackScroll(&cv.side)
			side.Children(func() {
				size := theme.Body * r.Size / theme.ReadingSize
				e := ui.RichText(c, cv.spans...).Font(theme.Mono).FontSize(size).LineHeight(1.75).NoWrap().TabSize(2).
					TextColor(theme.CodeText).Padding(16)
				if len(cv.marks) > 0 {
					// A marked line's ground across the whole block, a 2-wide rule at its start.
					width := r.width()
					if r.body.W > 0 {
						width = r.body.W
					}
					e.Draw(func(p *ui.Painter, box ui.Rect) {
						lh := size * 1.75
						for i, m := range cv.marks {
							ground, rule := markColors(m)
							line := ui.Rect{X: box.X, Y: box.Y + 16 + float32(i)*lh, W: max(box.W, width-2), H: lh} // the card's inside, past short code
							p.Fill(line, ground, 0)
							p.Fill(ui.Rect{X: line.X, Y: line.Y, W: 2, H: lh}, rule, 0)
						}
					})
				}
				r.paras[e.ID()] = &para{b: b, sep: "\n", box: e.LayoutBounds()}
			})
			edgeFades(c, &cv.side, theme.CodeBackground)
		})
	})
}

func (r *Reader) copyButton(c *ui.Context, cv *codeView) {
	label, name, copied := locales.CommonCopy(), locales.ReaderCopyCode(), false
	if left := 1400*time.Millisecond - c.Now().Sub(r.copiedAt); r.copied == cv.id && left > 0 {
		label, name, copied = locales.CommonCopied(), locales.CommonCopied(), true
		c.After(left)
	}
	// The interface's face: the body around it may be set in New York.
	btn := ui.Button(c.Key("copy:"+cv.id), label).Label(name).Height(24).Padding(0, 8).Radius(6).Font(theme.Sans).FontSize(theme.Label).
		Background(ui.Color{}).Border(0, ui.Color{}).TextColor(theme.Muted)
	if btn.Hovered() || copied {
		btn.TextColor(theme.Text)
	}
	if btn.Hovered() {
		btn.Background(theme.Hover)
	}
	if btn.Clicked() {
		c.Services().WriteClipboard(cv.code)
		r.copied, r.copiedAt = cv.id, c.Now()
		c.After(1400 * time.Millisecond)
	}
}

// table sets a table as a book does: rules across only, a stronger one under its head, none round it; the head in
// the small grey of a label; each cell's words at its top, figures to the right in even widths. Its outer columns
// stand on the column's edges, as the text does. Wider than the column, it scrolls sideways, and the side it goes on
// fades out, less as it nears its end.
func (r *Reader) table(c *ui.Context, b *block, tc textCtx) ui.Element {
	cols := 0
	for _, row := range b.rows {
		n := 0
		for _, cl := range row {
			n += cl.colSpan
		}
		cols = max(cols, n)
	}
	width := r.width()
	if r.body.W > 0 {
		width = r.body.W
	}
	k := r.textSize() / theme.ReadingSize
	tc.size, tc.lineHeight, tc.numbers = theme.Item*k, 1.6, true
	head := headRows(b.rows)
	label := tc
	label.size, label.color, label.lineHeight = theme.Label*k, theme.Muted, 1.5
	// The room a cell keeps either side of its words: 12, none on the table's outer sides.
	pad := func(col, span int) (left, right float32) {
		left, right = 12, 12
		if col == 0 {
			left = 0
		}
		if col+span >= cols {
			right = 0
		}
		return
	}
	// Chrome's table (width: max-content, at least the column; cells at most
	// 360 with their room): each column as wide as its widest cell's words on
	// one line, up to 360, and the room left, when they fit, shared as their
	// widths are; wider, the table scrolls.
	want := make([]float32, cols)
	for i, row := range b.rows {
		col := 0
		for _, cl := range row {
			if cl.colSpan == 1 && col < cols {
				size, weight := tc.size, 400
				switch {
				case i < head:
					size = label.size
				case cl.header:
					weight = 600
				}
				left, right := pad(col, 1)
				want[col] = max(want[col], min(r.cellWidth(cl, size, weight), 336)+left+right)
			}
			col += cl.colSpan
		}
	}
	total := float32(0)
	for _, w := range want {
		total += w
	}
	if total < width && total > 0 {
		k := width / total
		for i := range want {
			want[i] *= k
		}
	}
	tracks := make([]ui.Track, cols)
	for i := range tracks {
		tracks[i] = ui.Fixed(want[i])
	}
	last := len(b.rows) - 1
	return ui.Column(c).Children(func() {
		side := ui.ScrollHorizontal(c).TrackScroll(&b.side).Label(locales.ReaderTableScroll())
		side.Children(func() {
			ui.Grid(c).ColumnTracks(tracks...).MinWidth(width).Children(func() {
				for i, row := range b.rows {
					r.row++
					col := 0
					for _, cl := range row {
						cc := tc
						if i < head {
							cc = label
						} else if cl.header {
							cc.weight = 600
						}
						cc.row, cc.sep = r.row, "\n"
						left, right := pad(col, cl.colSpan)
						e := ui.Column(c).Padding(10, right, 10, left)
						switch {
						case i < head:
							// The head's words sit on its rule.
							e.Padding(4, right, 8, left).Justify(ui.End)
						default:
							e.Justify(ui.Start)
						}
						if cl.colSpan == 1 && col < len(b.numeric) && b.numeric[col] {
							cc.align = ui.End
							e.AlignItems(ui.End)
						}
						// A rule under each row but the last, the head's stronger.
						switch end := i + cl.rowSpan - 1; {
						case end == head-1:
							e.BorderWidth(0, 0, 1, 0).BorderColor(theme.TableHeadRule)
						case end < last:
							e.BorderWidth(0, 0, 1, 0).BorderColor(theme.Line)
						}
						if cl.colSpan > 1 {
							e.ColumnSpan(cl.colSpan)
						}
						if cl.rowSpan > 1 {
							e.RowSpan(cl.rowSpan)
						}
						e.Children(func() { r.children(c, cl.blocks, false, cc, nil) })
						col += cl.colSpan
					}
				}
			})
		})
		edgeFades(c, &b.side, theme.Window)
	})
}

// edgeFades fades the sides a sideways scroll can still go to out into bg, 40 wide, less as it nears its end; call
// it in the box that holds the scroll, after it.
func edgeFades(c *ui.Context, s *ui.ScrollState, bg ui.Color) {
	if s.MaxX <= 1 {
		return
	}
	fade := func(key string, k, angle float32) {
		if k <= 0 {
			return
		}
		e := ui.Box(c.Key(key)).Absolute().Top(0).Bottom(0).Width(40).Opacity(k).IgnoreInput(true).AccessHidden(true).
			LinearGradient(ui.LinearGradient{Angle: angle, From: bg.Alpha(0), To: bg})
		if angle == 90 {
			e.Right(0)
		} else {
			e.Left(0)
		}
	}
	fade("fade-end", min(1, (s.MaxX-s.X)/40), 90)
	fade("fade-start", min(1, s.X/40), 270)
}

func (r *Reader) picture(c *ui.Context, b *block) ui.Element {
	p := b.pic
	if p.img == nil || p.w <= 0 || p.h <= 0 {
		return r.missing(c, p)
	}
	var e ui.Element
	switch {
	case p.player != nil:
		e = ui.Video(c, p.player)
	case r.fl != nil:
		e = ui.Box(c) // flowing, the flow paints it on its way (flow.go)
	default:
		e = ui.Image(c, p.img)
	}
	e.Width(p.w).MaxWidthPercent(100).AspectRatio(p.w / p.h).AlignSelf(ui.Start)
	r.picBox[b] = e.LayoutBounds()
	if p.alt != "" {
		e.Label(p.alt)
	}
	rad := pictureRadius(min(p.w, r.width()))
	if rad > 0 {
		e.Radius(rad)
	}
	e.DrawOver(func(pt *ui.Painter, box ui.Rect) {
		if len(p.texts) > 0 {
			drawSVGTexts(pt, box, p)
		}
		pictureEdge(pt, box, rad)
	})
	// A button that shows it large, whose place stays empty while it does.
	if r.OnPicture != nil {
		name := locales.ReaderViewLarger()
		if p.alt != "" {
			name = locales.ReaderViewLargerAlt(p.alt)
		}
		e.Focusable().Role(ui.RoleButton).Label(name).Cursor(ui.CursorZoomIn).FocusRingOffset(3)
		if e.Clicked() {
			r.OnPicture(r.pictureOf(p, e.LayoutBounds()))
		}
	}
	if r.Hide != 0 && p.id == r.Hide {
		e.Opacity(0)
		r.hidden = e.LayoutBounds()
	}
	return e
}

// pictureRadius is the corner of a picture of the body shown w wide: a card's, 10, where it shows as a picture;
// none for a small one, a badge or an icon, which a corner would cut into.
func pictureRadius(w float32) float32 {
	if w >= 160 {
		return 10
	}
	return 0
}

// pictureEdge draws a picture's edge, a line-soft rule inside its corners, so that a dark picture keeps its edge on
// the dark page; a small one goes without.
func pictureEdge(pt *ui.Painter, box ui.Rect, rad float32) {
	if rad > 0 {
		pt.Stroke(ui.Rect{X: box.X + .5, Y: box.Y + .5, W: box.W - 1, H: box.H - 1}, theme.LineSoft, rad-.5, 1)
	}
}

// Pic is a picture to show large (OnPicture): its words, its own size, and
// where the page showed it.
type Pic struct {
	ID      int
	Alt     string
	Caption string  // its figure's caption, else its alt
	W, H    float32 // its own size
	Box     ui.Rect // where the page showed it, in the window
	pic     *picture
	img     ui.ImageSource
}

// NewPic is a picture of the app's to show large, as a work's shot.
func NewPic(id int, img ui.ImageSource, alt, caption string) Pic {
	w, h := ui.ImageSize(img)
	return Pic{ID: id, Alt: alt, Caption: caption, W: w, H: h, img: img}
}

func (r *Reader) pictureOf(p *picture, box ui.Rect) Pic {
	pc := NewPic(p.id, p.img, p.alt, p.caption)
	if pc.Caption == "" {
		pc.Caption = p.alt
	}
	if pc.W <= 0 || pc.H <= 0 {
		pc.W, pc.H = p.w, p.h
	}
	pc.Box, pc.pic = box, p
	return pc
}

// View builds the picture as it shows large: a GIF plays, an SVG's words
// show over it.
func (p Pic) View(c *ui.Context) ui.Element {
	pc := p.pic
	if pc == nil {
		return ui.Image(c, p.img)
	}
	var e ui.Element
	if pc.player != nil {
		e = ui.Video(c, pc.player)
	} else {
		e = ui.Image(c, pc.img)
	}
	if len(pc.texts) > 0 {
		e.DrawOver(func(pt *ui.Painter, box ui.Rect) { drawSVGTexts(pt, box, pc) })
	}
	return e
}

// HiddenBox is where the page leaves the picture of Hide empty, as the
// frame before laid it out; zero when it does not show it.
func (r *Reader) HiddenBox() ui.Rect { return r.hidden }

// Aside builds a sideways scroller of the app's in Above or Below, as a
// work's strip of shots, under key: it keeps its scrolling while the
// reader lasts.
func (r *Reader) Aside(c *ui.Context, key string) ui.Element {
	s := r.sides[key]
	if s == nil {
		if r.sides == nil {
			r.sides = map[string]*ui.ScrollState{}
		}
		s = &ui.ScrollState{}
		r.sides[key] = s
	}
	return ui.ScrollHorizontal(c.Key(key)).TrackScroll(s)
}

// missing is a picture that did not load: its place in the column, at its
// own proportions when known, the pictogram over one button.
func (r *Reader) missing(c *ui.Context, p *picture) ui.Element {
	label := locales.ReaderImageFailed()
	if p.alt != "" {
		label = locales.ReaderImageFailedAlt(p.alt)
	}
	e := ui.Column(c).Label(label).MinHeight(260).Padding(24).Gap(16).Center()
	if p.w > 0 && p.h > 0 {
		e.AspectRatio(p.w / p.h)
	}
	if r.view.H > 0 {
		e.MaxHeight(r.view.H * 0.7)
	}
	return e.Children(func() {
		if r.Missing != nil {
			r.Missing(c)
		} else {
			ui.Image(c, missingArt).Width(168)
		}
		if r.OnRetry == nil {
			return
		}
		var b ui.Element
		if r.Button != nil {
			b = r.Button(c, locales.CommonTryAgain())
		} else {
			b = ui.Button(c, locales.CommonTryAgain())
		}
		if b.Clicked() {
			r.OnRetry()
		}
	})
}

// drawSVGTexts draws an SVG's text over it, where the SVG places it.
func drawSVGTexts(pt *ui.Painter, box ui.Rect, p *picture) {
	sx, sy := box.W/p.vb[2], box.H/p.vb[3]
	for _, t := range p.texts {
		s := ui.Span{Text: t.text, Size: t.size * sy, Color: t.color, Weight: t.weight, Font: t.family}
		w, _ := pt.MeasureText(0, s)
		x := box.X + (t.x-p.vb[0])*sx
		switch t.anchor {
		case "middle":
			x -= w / 2
		case "end":
			x -= w
		}
		ascent := ui.Font{Family: t.family, Size: s.Size, Weight: t.weight}.Metrics().Ascent
		pt.RichText(x, box.Y+(t.y-p.vb[1])*sy-ascent, 0, s)
	}
}

// metaDate is the reader's date: 11月4日 this year, 2025年11月4日 before,
// 更新于 one for a modified date.
func metaDate(published, modified string) string {
	updated, v := false, published
	if v == "" {
		updated, v = true, modified
	}
	if v == "" {
		return locales.ReaderNoDate()
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		if t, err = time.Parse("2006-01-02", v); err != nil {
			return v
		}
	} else {
		t = t.Local()
	}
	skeleton := "yMMMd"
	if t.Year() == time.Now().Year() {
		skeleton = "MMMd"
	}
	d := i18n.FormatDate(i18n.Locale(), t, i18n.DateOptions{Skeleton: skeleton})
	if updated {
		return locales.ReaderUpdated(d)
	}
	return d
}

// readingTime is the reader's: about 400 Han characters or 230 words a
// minute, code not counted.
func readingTime(bs []*block) (minutes int, size string) {
	var han, words int
	var walk func(bs []*block)
	walk = func(bs []*block) {
		for _, b := range bs {
			for _, rn := range b.runs {
				inWord := false
				for _, ch := range rn.text {
					switch {
					case unicode.Is(unicode.Han, ch):
						han++
						inWord = false
					case unicode.IsLetter(ch) || unicode.IsNumber(ch):
						if !inWord {
							words++
						}
						inWord = true
					default:
						inWord = false
					}
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
	walk(bs)
	if han+words == 0 {
		return 0, ""
	}
	minutes = max(1, int(float64(han)/400+float64(words)/230+0.5))
	if han >= words {
		return minutes, locales.ReaderCharacters(han + words)
	}
	return minutes, locales.ReaderWords(words)
}

func hasWide(runs []run) bool {
	for _, rn := range runs {
		if rn.style&cjk != 0 {
			return true
		}
	}
	return false
}

// asciiWord reports text of ASCII letters and digits only, which the
// system face has superior and inferior forms of.
func asciiWord(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return s != ""
}

// cellWidth is how wide a cell's words are on one line (their max-content),
// at size, kept on the cell.
func (r *Reader) cellWidth(cl *cell, size float32, weight int) float32 {
	if cl.measuredAt == size {
		return cl.measured
	}
	w := float32(0)
	var walk func(bs []*block)
	walk = func(bs []*block) {
		for _, b := range bs {
			if len(b.runs) > 0 {
				segs := make([]segment, len(b.runs))
				for i, rn := range b.runs {
					segs[i] = segment{run: rn, cjkFont: r.face().CJK}
				}
				lw, _ := ui.ShapeRichText(spans(segs, size), ui.Font{Family: r.face().Family, Size: size, Weight: weight}, 0).Size()
				w = max(w, lw)
			}
			walk(b.children)
		}
	}
	walk(cl.blocks)
	cl.measured, cl.measuredAt = w, size
	return w
}
