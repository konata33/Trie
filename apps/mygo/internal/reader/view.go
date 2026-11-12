package reader

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"trie/internal/motion"
	"trie/internal/theme"

	"github.com/egoist/mygo/ui"
)

// Column is the reading column's width (theme.ReaderColumn, centred).
const Column = theme.ReaderColumn

// guide is how far below the top of the reader a reading position is
// measured, as useReadingSession.ts measures it.
const guide = 110

// sans is the reading face: the system's, with PingFang SC before the
// system's own choice for what it lacks, as -apple-system, "PingFang SC".
const sans = "system-ui, PingFang SC"

// cjkFace draws Chinese and Japanese text (see scripts).
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
	// button does not show, as in the Electron reader.
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
	// last block when it has none (ArticleBody's insert: a work's facts),
	// and returns its top and bottom margins, which collapse with those of
	// the blocks around it.
	Insert func() (top, bottom float32)
	// Instant shows a new size or measure in the next frame rather than
	// crossing over to it, as the keyboard asks.
	Instant bool
	// Name is the body's accessible name, as the Electron reader titled
	// the frame of its body: 「标题 · 正文」.
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
	view     ui.Rect // the scroll container
	body     ui.Rect
	shownY   float32 // the offset the frame before was laid out at
	top      float32 // where the body starts in the scrolled content
	endGap   float32 // between the body and what follows it
	selected []ui.SelectedText
	row      int
	// The top-level blocks' boxes, the sideways scrollers under the
	// pointer's reach, and the hidden picture's box, as the frame before
	// laid them out.
	tops   []ui.Rect
	asides []aside
	hidden ui.Rect
	sides  map[string]*ui.ScrollState // the app's sideways scrollers (Aside), by key

	// A new size or measure (ArticleReader.tsx settle): what the last
	// layout was for, how many each has had (the keys of the text laid out
	// anew), and the crossing on its way.
	laidSize, laidWidth float32
	measures, layouts   int
	flow                reflow
	exitInstant         bool // what the text built last would go as
}

// aside is something in the body that scrolls sideways: a code block, a
// wide table.
type aside struct {
	box  ui.Rect
	side *ui.ScrollState
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
	box  ui.Rect
	segs []segment
	font ui.Font
}

type unitTop struct {
	id  string
	top float32
}

// textCtx is what a container passes on to the text inside it.
type textCtx struct {
	size   float32
	color  ui.Color
	weight int
	sep    string
	row    int
}

// NewReader makes the reader of an article whose body Prepare made.
func NewReader(a *Article, c *Content, b *Body) *Reader {
	return &Reader{Article: a, Content: c, Body: b, Size: theme.ReadingSize}
}

func (r *Reader) em() float32 { return r.Size }

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
	r.asides, r.hidden = r.asides[:0], ui.Rect{}
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
		col := ui.Column(c).FillWidth().AlignSelf(ui.Center).Padding(top, 40, 180, 40).Font(sans)
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
				r.paras, r.anchors = map[uint64]*para{}, map[string]float32{}
			}
			clear(r.anchors)
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
				}
				first, last = first*em, last*em
				body := ui.Column(c.Key(r.gen)).Selectable().SelectionColor(theme.AccentSoft).CopyData(r.copyData).
					FontSize(r.Size).LineHeight(theme.ReadingLineHeight).TextColor(theme.Text)
				if r.Name != "" {
					body.Role(ui.RoleArticle).Label(r.Name)
				}
				r.body = body.LayoutBounds()
				r.top = r.body.Y - r.view.Y + r.shownY
				body.Children(func() {
					tc := textCtx{size: r.Size, color: theme.Text, sep: "\n\n"}
					build := func(_ int, b *block) ui.Element {
						e := r.block(c, b, tc)
						r.tops = append(r.tops, e.LayoutBounds())
						return e
					}
					if r.Insert == nil {
						r.children(c, bs, true, tc, build)
						return
					}
					// ponytail: ArticleBody looks only at the body's top level; the
					// blocks flatten wrappers, so an h2 inside a div counts here.
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
				} else {
					r.end(c, r.endGap)
				}
			})
			if r.flow.on {
				ui.Box(c.Key("reflow-room")).Height(r.flow.pad).AccessHidden(true)
			}
		})
		col.MaxWidth(r.width() + 80)
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
		return
	}
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
		frac = max(0, min(1, (relative-t.Y)/max(1, t.H)))
	}
	if measure {
		r.measures++
	}
	r.layouts++
	r.flow = reflow{on: true, instant: r.exitInstant, at: now, block: block, frac: frac, pad: r.Scroll.MaxY + r.view.H}
	r.ClearSelection()
	c.Invalidate() // laid out, it is measured in the next frame
}

// measure finds, in the frame after the text crossed, where its new layout
// puts the place being read, and how far that is from where it showed.
func (r *Reader) measure() {
	f := &r.flow
	f.measured = true
	if f.block >= 0 && f.block < len(r.tops) {
		t := r.tops[f.block]
		f.shift = max(0, r.top+t.Y+f.frac*t.H-guide) - r.Scroll.Y
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
// Headline), width wide, balanced.
func Headline(c *ui.Context, title string, width float32) ui.Element {
	return Lines(c, title, width, ui.Font{Family: sans, Size: 32, Weight: 600, LineHeight: 1.3}, -0.012*32, true)
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
		title = "未提取到标题"
	}
	ui.Column(c).Children(func() {
		Headline(c, title, r.width())
		ui.Row(c).Margin(12, 0, 0, 0).Gap(8).Wrap().FontSize(theme.Body).TextColor(theme.Muted).FontFeatures(theme.Numbers).Children(func() {
			ui.Text(c, a.SourceName).TextColor(theme.Text2)
			ui.Text(c, "·")
			ui.Text(c, metaDate(a.PublishedAt, a.ModifiedAt))
			if r.Content.Status == "ready" && r.Body.Minutes > 0 {
				ui.Text(c, "·")
				ui.Text(c, fmt.Sprintf("约 %d 分钟", r.Body.Minutes))
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
		return "当前来源仅提供摘要，可前往作者网站继续阅读。"
	case s == "restricted":
		return "来源内容受限，请前往作者网站查看访问方式。"
	}
	return "暂时无法获取正文，请稍后重试。"
}

// end is the end mark: a hairline either side of one quiet line.
func (r *Reader) end(c *ui.Context, top float32) {
	var text string
	switch r.Content.Status {
	case "summary":
		text = "摘要到此结束"
	case "restricted":
		text = "公开内容到此结束"
	case "ready":
		if r.Body.Minutes == 0 {
			return
		}
		text = fmt.Sprintf("全文约 %d 分钟 · %s", r.Body.Minutes, r.Body.Size)
		if r.Position().Progress >= 0.97 {
			text = "读完 · " + text
		}
	default:
		return
	}
	ui.Row(c).Margin(top, 0, 0, 0).Gap(12).AlignItems(ui.Center).FontSize(theme.Label).Children(func() {
		ui.Box(c).Height(1).Grow(1).Background(theme.Line)
		ui.Text(c, text).TextColor(theme.Muted).FontFeatures(theme.Numbers)
		if r.Content.Status != "ready" && r.Article.URL != "" {
			r.original(c)
		}
		ui.Box(c).Height(1).Grow(1).Background(theme.Line)
	})
}

// original is 阅读原文: the article on its site, which OnLink opens.
func (r *Reader) original(c *ui.Context) {
	url := r.Article.URL
	if r.OnLink != nil {
		url = ""
	}
	l := ui.Link(c, "阅读原文", url).TextColor(theme.Text2)
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
		case i > 0:
			_, before := outer(bs[i-1])
			gap = max(before, top)
		case collapse:
			gap = 0
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
		if b.unit != "" || b.anchor != "" {
			y := e.LayoutBounds().Y
			if b.unit != "" && (len(r.units) == 0 || r.units[len(r.units)-1].id != b.unit) {
				r.units = append(r.units, unitTop{b.unit, y})
			}
			if b.anchor != "" {
				r.anchors[b.anchor] = y
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
		return ui.Box(c).Height(1).Background(theme.Line)
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
	q := ui.Column(c)
	label, callout := calloutLabels[b.callout]
	if callout {
		q.Radius(10).Background(theme.Surface).Border(1, theme.Line).Padding(16)
		tc.color = theme.Text
	} else {
		q.BorderWidth(0, 0, 0, 2).BorderColor(theme.QuoteRule).Padding(2, 0, 2, 16)
		tc.color = theme.Text2
	}
	return q.Children(func() {
		if callout {
			color, ok := calloutColors[b.callout]
			if !ok {
				color = theme.Danger
			}
			size := theme.Body * r.Size / theme.ReadingSize
			ui.Text(c, label).FontSize(size).FontWeight(600).TextColor(color).Margin(0, 0, .4*size, 0)
		}
		r.children(c, b.children, false, tc, nil)
	})
}

// paragraph builds the text of a paragraph, heading or caption: one rich
// text, its links within it.
func (r *Reader) paragraph(c *ui.Context, b *block, tc textCtx) ui.Element {
	size, weight, lh, color := tc.size, tc.weight, float32(theme.ReadingLineHeight), tc.color
	switch b.kind {
	case Heading:
		size = [7]float32{0, theme.Title, theme.Subtitle, theme.ReadingSize, theme.ReadingSize, theme.ReadingSize, theme.ReadingSize}[b.level] * r.Size / theme.ReadingSize
		weight, lh, color = 600, [7]float32{0, 1.4, 1.4, 1.45, 1.5, 1.5, 1.5}[b.level], theme.Text
		if b.level == 6 {
			color = theme.Text2
		}
	case Caption:
		size, color = theme.Body*r.Size/theme.ReadingSize, theme.Muted
	}
	segs := r.segments(b)
	var e ui.Element
	if !hasLink(segs) {
		e = ui.RichText(c, spans(segs, size)...)
	} else {
		e = ui.RichText(c).Children(func() {
			for i := 0; i < len(segs); {
				j := i + 1
				for j < len(segs) && segs[j].href == segs[i].href {
					j++
				}
				if segs[i].href == "" {
					ui.RichText(c, spans(segs[i:j], size)...)
				} else {
					r.link(c, segs[i:j], size)
				}
				i = j
			}
		})
	}
	e.FontSize(size).LineHeight(lh).TextColor(color)
	if weight > 0 {
		e.FontWeight(weight)
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
	r.paras[e.ID()] = &para{b: b, sep: sep, row: tc.row, box: e.LayoutBounds(), segs: segs,
		font: ui.Font{Family: sans, Size: size, Weight: weight, LineHeight: lh}}
	return e
}

func (r *Reader) link(c *ui.Context, segs []segment, size float32) {
	href := segs[0].href
	url := href
	if r.OnLink != nil || strings.HasPrefix(href, "#") {
		url = "" // the app decides what a click does
	}
	l := ui.Link(c, "", url).Children(func() { ui.RichText(c, spans(segs, size)...) }).
		TextColor(theme.Text).Underline().DecorationThickness(1).UnderlineOffset(4).DecorationColor(theme.LinkUnderline)
	if l.Hovered() {
		l.DecorationColor(theme.Accent)
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
}

// segment is a run, or the part of one a clip covers.
type segment struct {
	run
	marked bool
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
	at := 0
	for _, rn := range b.runs {
		n := utf8.RuneCountInString(rn.text)
		if len(marks) == 0 {
			out = append(out, segment{run: rn})
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
			out = append(out, segment{run: piece, marked: marked})
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

// spans styles segments as reader-body.styles.ts styles their tags, in a
// paragraph of the given size.
func spans(segs []segment, size float32) []ui.Span {
	out := make([]ui.Span, 0, len(segs))
	for _, sg := range segs {
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
			s.Font = cjkFace
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
				ui.Text(c, cv.label).Font(theme.Mono).FontSize(theme.Label).LetterSpacing(0.24).TextColor(theme.Muted).Grow(1)
				r.copyButton(c, cv)
			})
		}
		side := ui.ScrollHorizontal(c).TrackScroll(&cv.side)
		r.asides = append(r.asides, aside{side.LayoutBounds(), &cv.side})
		side.Children(func() {
			e := ui.RichText(c, cv.spans...).Font(theme.Mono).FontSize(theme.Body * r.Size / theme.ReadingSize).LineHeight(1.75).NoWrap().TabSize(2).
				TextColor(theme.Text).Padding(16)
			r.paras[e.ID()] = &para{b: b, sep: "\n", box: e.LayoutBounds()}
		})
	})
}

func (r *Reader) copyButton(c *ui.Context, cv *codeView) {
	label, name := "复制", "复制代码"
	if left := 1400*time.Millisecond - c.Now().Sub(r.copiedAt); r.copied == cv.id && left > 0 {
		label, name = "已复制", "已复制"
		c.After(left)
	}
	btn := ui.Button(c.Key("copy:"+cv.id), label).Label(name).Height(24).Padding(0, 8).Radius(6).FontSize(theme.Label).
		Background(ui.Color{}).Border(0, ui.Color{}).TextColor(theme.Muted)
	if btn.Hovered() || label == "已复制" {
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
	tc.size = r.Size * theme.Item / theme.ReadingSize
	// Chrome's table (width: max-content, at least the column; cells at most
	// 360 with their 12 a side and their rule): each column as wide as its
	// widest cell's words on one line, up to 360, and the room left, when
	// they fit, shared as their widths are; wider, the table scrolls.
	want := make([]float32, cols)
	for _, row := range b.rows {
		col := 0
		for _, cl := range row {
			if cl.colSpan == 1 && col < cols {
				want[col] = max(want[col], min(r.cellWidth(cl, tc.size)+26, 360))
			}
			col += cl.colSpan
		}
	}
	total := float32(1)
	for _, w := range want {
		total += w
	}
	if total < width && total > 1 {
		k := (width - 1) / (total - 1)
		for i := range want {
			want[i] *= k
		}
	}
	tracks := make([]ui.Track, cols)
	for i := range tracks {
		tracks[i] = ui.Fixed(want[i])
	}
	side := ui.ScrollHorizontal(c).TrackScroll(&b.side).Label("表格，可横向滚动")
	r.asides = append(r.asides, aside{side.LayoutBounds(), &b.side})
	return side.Children(func() {
		ui.Grid(c).ColumnTracks(tracks...).MinWidth(width).BorderWidth(1, 0, 0, 1).BorderColor(theme.Line).Children(func() {
			for _, row := range b.rows {
				r.row++
				for _, cl := range row {
					cc := tc
					cc.row, cc.sep = r.row, "\n"
					if cl.header {
						cc.weight = 700
					}
					// A cell's words sit in its middle, as a td's vertical-align has them.
					e := ui.Column(c).Padding(8, 12).Justify(ui.Center).BorderWidth(0, 1, 1, 0).BorderColor(theme.Line)
					if cl.colSpan > 1 {
						e.ColumnSpan(cl.colSpan)
					}
					if cl.rowSpan > 1 {
						e.RowSpan(cl.rowSpan)
					}
					e.Children(func() { r.children(c, cl.blocks, false, cc, nil) })
				}
			}
		})
	})
}

func (r *Reader) picture(c *ui.Context, b *block) ui.Element {
	p := b.pic
	if p.img == nil || p.w <= 0 || p.h <= 0 {
		return r.missing(c, p)
	}
	var e ui.Element
	if p.player != nil {
		e = ui.Video(c, p.player)
	} else {
		e = ui.Image(c, p.img)
	}
	e.Width(p.w).MaxWidthPercent(100).AspectRatio(p.w / p.h).AlignSelf(ui.Start)
	if p.alt != "" {
		e.Label(p.alt)
	}
	if len(p.texts) > 0 {
		e.DrawOver(func(pt *ui.Painter, box ui.Rect) { drawSVGTexts(pt, box, p) })
	}
	// A button that shows it large (ArticleReader.tsx openImage), whose
	// place stays empty while it does.
	if r.OnPicture != nil {
		name := "放大查看图片"
		if p.alt != "" {
			name = "放大查看：" + p.alt
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

// ScrollsAside reports whether something under the point (x, y) in the
// window can still scroll sideways by dx, a code block, a wide table or a
// scroller of the app's (Aside) (ReaderBar.tsx scrollsAside): a swipe that
// starts there is its own.
func (r *Reader) ScrollsAside(x, y, dx float32) bool {
	for _, a := range r.asides {
		b, s := a.box, a.side
		if x < b.X || x >= b.X+b.W || y < b.Y || y >= b.Y+b.H || s.MaxX <= 0 {
			continue
		}
		if dx < 0 && s.X > 0 || dx > 0 && s.X < s.MaxX-1 {
			return true
		}
	}
	return false
}

// Aside builds a sideways scroller of the app's in Above or Below, as a
// work's strip of shots, under key: it keeps its scrolling while the
// reader lasts and, as the body's code blocks and wide tables do, keeps a
// swipe that starts on it while it can still scroll that way (ScrollsAside).
func (r *Reader) Aside(c *ui.Context, key string) ui.Element {
	s := r.sides[key]
	if s == nil {
		if r.sides == nil {
			r.sides = map[string]*ui.ScrollState{}
		}
		s = &ui.ScrollState{}
		r.sides[key] = s
	}
	e := ui.ScrollHorizontal(c.Key(key)).TrackScroll(s)
	r.asides = append(r.asides, aside{e.LayoutBounds(), s})
	return e
}

// missing is a picture that did not load: its place in the column, at its
// own proportions when known, the pictogram over one button.
func (r *Reader) missing(c *ui.Context, p *picture) ui.Element {
	label := "图片没能载入"
	if p.alt != "" {
		label += "：" + p.alt
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
			b = r.Button(c, "重试")
		} else {
			b = ui.Button(c, "重试")
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

// metaDate is the reader's date: 11月4日 this year, 2025年11月4日 before.
func metaDate(published, modified string) string {
	prefix, v := "", published
	if v == "" {
		prefix, v = "更新于 ", modified
	}
	if v == "" {
		return "未提供日期"
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		if t, err = time.Parse("2006-01-02", v); err != nil {
			return v
		}
	} else {
		t = t.Local()
	}
	if t.Year() == time.Now().Year() {
		return prefix + fmt.Sprintf("%d月%d日", t.Month(), t.Day())
	}
	return prefix + fmt.Sprintf("%d年%d月%d日", t.Year(), t.Month(), t.Day())
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
		return minutes, fmt.Sprintf("%s 字", thousands(han+words))
	}
	return minutes, fmt.Sprintf("%s 词", thousands(words))
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
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
func (r *Reader) cellWidth(cl *cell, size float32) float32 {
	if cl.measuredAt == size {
		return cl.measured
	}
	weight := 400
	if cl.header {
		weight = 700
	}
	w := float32(0)
	var walk func(bs []*block)
	walk = func(bs []*block) {
		for _, b := range bs {
			if len(b.runs) > 0 {
				segs := make([]segment, len(b.runs))
				for i, rn := range b.runs {
					segs[i] = segment{run: rn}
				}
				lw, _ := ui.ShapeRichText(spans(segs, size), ui.Font{Family: sans, Size: size, Weight: weight}, 0).Size()
				w = max(w, lw)
			}
			walk(b.children)
		}
	}
	walk(cl.blocks)
	cl.measured, cl.measuredAt = w, size
	return w
}
