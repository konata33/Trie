package reader

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"trie/internal/locales"
	"trie/internal/theme"

	"github.com/egoist/mygo/ui"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Kind is what a block of the body is.
type Kind uint8

const (
	Paragraph Kind = iota // runs: p, and loose text in a container
	Heading               // runs: h1–h6
	Caption               // runs: figcaption, a table's caption
	List                  // children are Items: ul, ol
	Item                  // children: li
	Quote                 // children: blockquote, a callout when callout is set
	Figure                // children: figure
	Code                  // code: pre
	Table                 // rows: table
	Picture               // pic: img
	Rule                  // hr
)

type style uint16

const (
	bold style = 1 << iota
	italic
	strike
	mono // code within text
	sup
	sub
	cjk       // Han, kana and their punctuation: drawn in PingFang SC, as the browser falls back to it
	autospace // an ideograph meeting a Latin letter or digit: 1/8 em after it, as text-autospace: normal
	halfwidth // closing punctuation before more punctuation, set half wide, as text-spacing-trim: normal
	glyph     // the mark of the site the link after it goes to (glyph.go): one U+FFFC, an inline object
)

// run is a stretch of a paragraph's text in one style.
type run struct {
	text  string
	style style
	href  string // an http(s) URL or a #fragment
	glyph string // a glyph run's mark
}

// block is a block of the body. Margins are in ems of the body's text; the
// view collapses them as CSS does.
type block struct {
	kind        Kind
	level       int // a heading's 1–6; a list's depth, from 0
	ordered     bool
	start       int
	unit        string // data-reader-unit: what clips and reading positions point at
	anchor      string // id, which #links jump to
	runs        []run
	children    []*block
	callout     string // NOTE, TIP, IMPORTANT, WARNING, CAUTION
	code        *codeView
	pic         *picture
	rows        [][]*cell
	numeric     []bool         // a table's columns of figures, set to the right
	side        ui.ScrollState // a table's sideways scrolling
	marker      *markerShape   // a paragraph's layout its links' grounds are drawn on
	top, bottom float32
}

type cell struct {
	blocks           []*block
	header           bool
	colSpan, rowSpan int
	// The width of its words on one line, at the size measuredAt.
	measured, measuredAt float32
}

// Body is an article's body made into blocks: what Prepare makes once, off
// the UI thread, for View to show.
type Body struct {
	blocks []*block
	// Revision is the document's contentRevision, which clips keep; empty
	// for a body made from HTML stored without a document.
	Revision string
	// Minutes and Size are the reading time and length the header and the
	// end mark show: 约 3 分钟, 1,234 字.
	Minutes int
	Size    string
	// Face is how its text is set, by its language.
	Face Face
	pics []*picture
}

// interactiveExample is the link the extractor puts where a page's
// interactive example was (extract.go), as the library keeps it: shown in
// the interface's language.
const interactiveExample = "互动示例，在原网页查看"

// skipped are the tags whose content never shows: the HTML of articles
// stored without a document may still hold them, and the copy buttons an
// earlier formatter put in code headers, which the view draws itself.
var skipped = map[string]bool{
	"script": true, "style": true, "template": true, "noscript": true, "head": true, "title": true,
	"iframe": true, "form": true, "input": true, "select": true, "textarea": true, "button": true,
	"object": true, "embed": true, "video": true, "audio": true, "svg": true, "math": true, "canvas": true,
}

// inlineTags continue the paragraph around them; any other element starts
// a block of its own (div, section and the like hold blocks as a div does).
var inlineTags = map[string]bool{
	"span": true, "a": true, "strong": true, "b": true, "em": true, "i": true, "s": true, "del": true,
	"strike": true, "sup": true, "sub": true, "code": true, "kbd": true, "samp": true, "br": true,
	"img": true, "u": true, "ins": true, "mark": true, "small": true, "abbr": true, "cite": true,
	"q": true, "time": true, "var": true, "dfn": true, "bdi": true, "bdo": true, "wbr": true, "label": true,
	"font": true, "big": true, "tt": true, "picture": true, "source": true,
}

type builder struct {
	codes  map[string]CodeBlock
	tokens map[string][]codeToken
	roles  bool // the tokens name roles (format 4); older ones name a light theme's colors
	pics   []*picture
	depth  int
	face   Face // how the body is set (faceOf)
}

// Prepare makes the blocks of c: from its document, or else from its HTML. It decodes the pictures, so call it off the
// UI thread.
func Prepare(c *Content) *Body {
	b := &builder{codes: map[string]CodeBlock{}}
	for _, cb := range c.CodeBlocks {
		b.codes[cb.ID] = cb
	}
	body := &Body{}
	var nodes []*node
	if d := c.Document; d != nil {
		nodes, b.tokens, b.roles, body.Revision = d.Root.Children, d.CodeTokens, c.FormatVersion >= 4, d.ContentRevision
	} else {
		nodes = parseHTML(c.HTML)
	}
	b.face = faceOf(nodes)
	body.Face = b.face
	f := &flow{b: b}
	f.nodes(nodes, 0, "")
	body.blocks = f.done()
	settleHeadings(body.blocks)
	body.pics = b.pics
	body.Minutes, body.Size = readingTime(body.blocks)
	decodePictures(b.pics)
	return body
}

// headingStyle is how the body sets a heading of a level: its size over the body's text, its line height, and its
// margins above and below, in ems of the body's text. A heading sits further from what comes before it than from
// what it heads. Quiet headings take the secondary text colour.
type headingStyle struct {
	size, lineHeight, top, bottom float32
	quiet                         bool
}

// headingStyles are the levels the body sets, 2 to 6 (settleHeadings): at 17, 24, 20, 17, 15 and 15.
var headingStyles = [7]headingStyle{
	2: {float32(theme.Title) / theme.ReadingSize, 1.35, 2.8, .7, false},
	3: {float32(theme.Subtitle) / theme.ReadingSize, 1.4, 1.9, .5, false},
	4: {1, 1.5, 1.6, .4, false},
	5: {float32(theme.Item) / theme.ReadingSize, 1.5, 1.4, .3, true},
	6: {float32(theme.Item) / theme.ReadingSize, 1.5, 1.4, .3, true},
}

// settleHeadings sets a body's headings by their place among its own: its highest level is set as level 2, the
// article's sections, whether its author began them at h1 or at h3; the levels under it follow, down to 6.
func settleHeadings(bs []*block) {
	top := 7
	walkBlocks(bs, func(b *block) {
		if b.kind == Heading {
			top = min(top, b.level)
		}
	})
	walkBlocks(bs, func(b *block) {
		if b.kind != Heading {
			return
		}
		b.level = max(2, min(6, b.level-top+2))
		s := headingStyles[b.level]
		b.top, b.bottom = s.top, s.bottom
	})
}

// walkBlocks calls fn on every block of bs, those in lists, quotes, figures and table cells too.
func walkBlocks(bs []*block, fn func(*block)) {
	for _, b := range bs {
		fn(b)
		walkBlocks(b.children, fn)
		for _, row := range b.rows {
			for _, cl := range row {
				walkBlocks(cl.blocks, fn)
			}
		}
	}
}

// flow gathers the blocks of a container; loose inline content becomes
// paragraphs made from tmpl, which gives their kind, unit and margins.
type flow struct {
	b     *builder
	tmpl  block
	out   []*block
	para  *block
	space bool // the open paragraph is empty or ends in white space
	glue  bool // a site's mark was just added: the words after it are joined to it
}

func (f *flow) open() *block {
	if f.para == nil {
		p := f.tmpl
		f.para = &p
		f.tmpl.anchor = "" // the anchor goes with the first piece
	}
	return f.para
}

func (f *flow) flush() {
	p := f.para
	f.para = nil
	if p == nil {
		return
	}
	// White space at the end of a block does not show, nor does a line
	// break: <p>a<br></p> is one line, <p><br></p> an empty one.
	broken := false
	for len(p.runs) > 0 {
		last := &p.runs[len(p.runs)-1]
		last.text = strings.TrimRight(last.text, " ")
		if !broken && strings.HasSuffix(last.text, "\n") {
			last.text, broken = last.text[:len(last.text)-1], true
		}
		if last.text != "" {
			break
		}
		p.runs = p.runs[:len(p.runs)-1]
	}
	if len(p.runs) == 0 && broken {
		p.runs = []run{{}}
	}
	if len(p.runs) > 0 {
		p.runs = trimPunctuation(scripts(p.runs))
		f.out = append(f.out, p)
	}
}

// done ends the flow of a container: its last paragraph leaves the
// container's own padding to close it.
func (f *flow) done() []*block {
	f.flush()
	if n := len(f.out); n > 0 && f.out[n-1].kind == Paragraph {
		f.out[n-1].bottom = 0
	}
	return f.out
}

// text adds text to the open paragraph, collapsing white space as HTML
// does outside pre; no-break and ideographic spaces stay.
func (f *flow) text(s string, st style, href string) {
	if f.para == nil {
		f.space = true // white space at the start of a block does not show
	}
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '\n', '\t', '\r', '\f':
			if !f.space {
				sb.WriteByte(' ')
				f.space = true
			}
		default:
			sb.WriteRune(r)
			f.space = false
		}
	}
	if sb.Len() > 0 {
		text := sb.String()
		if f.glue {
			// A word joiner keeps the mark on the line of the link's first word.
			text, f.glue = "\u2060"+text, false
		}
		f.add(run{text: text, style: st, href: href})
	}
}

// magicLink is a link written as markdown-it-magic-link writes it, which some feeds leave as it is: {@user} or
// {@user|Name}, a GitHub user; {Name|https://…}, a page.
var magicLink = regexp.MustCompile(`\{@([A-Za-z0-9][A-Za-z0-9-]{0,38})(?:\|([^{}|\n]{1,80}))?\}|\{([^{}|@\n]{1,80})\|(https?://[^{}\s|]+)\}`)

// magic adds text whose magic links become links: a user's to their GitHub page, under their name or else their
// handle.
func (f *flow) magic(s string, st style) {
	at := 0
	for _, m := range magicLink.FindAllStringSubmatchIndex(s, -1) {
		var name, href string
		if m[2] >= 0 {
			name, href = s[m[2]:m[3]], "https://github.com/"+s[m[2]:m[3]]
			if m[4] >= 0 {
				name = s[m[4]:m[5]]
			}
		} else {
			name, href = s[m[6]:m[7]], link(s[m[8]:m[9]])
		}
		if name = strings.TrimSpace(name); name == "" || href == "" {
			continue
		}
		f.text(s[at:m[0]], st, "")
		f.mark(href, st)
		f.text(name, st, href)
		at = m[1]
	}
	f.text(s[at:], st, "")
}

// mark adds the mark of the site href goes to, if it has one, before the link's words: outside the link, which does
// not underline it.
func (f *flow) mark(href string, st style) {
	if g := glyphOf(href); g != "" {
		f.add(run{text: "\uFFFC", style: st&^(mono|sup|sub) | glyph, glyph: g})
		f.space, f.glue = true, true // the mark keeps its own room before the words
	}
}

// hasWords reports whether n holds words, rather than only a picture.
func hasWords(n *node) bool {
	if n.Type == "text" {
		return strings.TrimSpace(n.Value) != ""
	}
	for _, ch := range n.Children {
		if hasWords(ch) {
			return true
		}
	}
	return false
}

func (f *flow) add(r run) {
	p := f.open()
	if n := len(p.runs); n > 0 && p.runs[n-1].style == r.style && p.runs[n-1].href == r.href {
		p.runs[n-1].text += r.text
		return
	}
	p.runs = append(p.runs, r)
}

func (f *flow) nodes(ns []*node, st style, href string) {
	for _, n := range ns {
		f.node(n, st, href)
	}
}

func (f *flow) node(n *node, st style, href string) {
	switch n.Type {
	case "text":
		if f.para != nil || strings.TrimSpace(n.Value) != "" {
			v := n.Value
			if v == interactiveExample {
				v = locales.ReaderInteractiveExample()
			}
			if st&mono == 0 && href == "" {
				f.magic(v, st)
			} else {
				f.text(v, st, href)
			}
		}
		return
	case "element":
	default:
		return
	}
	tag := n.Tag
	if skipped[tag] || hasClass(n, "reader-code-header") {
		return
	}
	if inlineTags[tag] {
		f.inline(n, st, href)
		return
	}
	f.flush()
	unit := n.prop("dataReaderUnit")
	if unit == "" {
		unit = f.tmpl.unit
	}
	id := n.prop("id")
	sub := func(t block) {
		t.unit, t.anchor = unit, id
		g := &flow{b: f.b, tmpl: t}
		g.nodes(n.Children, 0, "")
		g.flush()
		f.out = append(f.out, g.out...)
	}
	switch tag {
	case "p":
		sub(block{kind: Paragraph, bottom: 1.15})
	case "h1", "h2", "h3", "h4", "h5", "h6":
		sub(block{kind: Heading, level: int(tag[1] - '0')}) // its level and margins as the body sets them: settleHeadings
	case "figcaption", "caption":
		sub(block{kind: Caption})
	case "ul", "ol":
		f.list(n, unit, id)
	case "li": // outside a list
		sub(block{kind: Paragraph, top: .4, bottom: .4})
	case "blockquote":
		q := &block{kind: Quote, unit: unit, anchor: id, callout: n.prop("dataCallout"), bottom: 1.15}
		q.children = f.child(n, unit)
		f.out = append(f.out, q)
	case "figure":
		fig := &block{kind: Figure, unit: unit, anchor: id, bottom: 1.3}
		fig.children = f.child(n, unit)
		captionPictures(fig.children, captionOf(fig.children))
		f.out = append(f.out, fig)
	case "pre":
		f.out = append(f.out, f.b.pre(n, unit, id))
	case "table":
		f.table(n, unit, id)
	case "hr":
		f.out = append(f.out, &block{kind: Rule, top: 2, bottom: 2})
	default: // div and the like hold what they hold
		start := len(f.out)
		sub(f.tmpl)
		// A paragraph that is its container's last child leaves no margin
		// under it, nor does the container, which has none of its own.
		if last := lastElement(n); last != nil && last.Tag == "p" && len(f.out) > start && f.out[len(f.out)-1].kind == Paragraph {
			f.out[len(f.out)-1].bottom = 0
		}
	}
}

// lastElement is n's last child that is an element.
func lastElement(n *node) *node {
	for i := len(n.Children) - 1; i >= 0; i-- {
		if ch := n.Children[i]; ch.Type == "element" {
			return ch
		}
	}
	return nil
}

// captionOf is the words of a figure's caption (its figcaption's
// textContent), and captionPictures gives them to the pictures in it, which
// show them large.
func captionOf(bs []*block) string {
	var sb strings.Builder
	for _, b := range bs {
		if b.kind == Caption {
			for _, r := range b.runs {
				sb.WriteString(r.text)
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

func captionPictures(bs []*block, caption string) {
	for _, b := range bs {
		if b.pic != nil {
			b.pic.caption = caption
		}
		captionPictures(b.children, caption)
	}
}

// child flows n's children as a container of their own.
func (f *flow) child(n *node, unit string) []*block {
	g := &flow{b: f.b, tmpl: block{kind: Paragraph, unit: unit}}
	g.nodes(n.Children, 0, "")
	return g.done()
}

func (f *flow) inline(n *node, st style, href string) {
	switch n.Tag {
	case "br":
		f.add(run{text: "\n", style: st, href: href})
		f.space = true
		return
	case "wbr":
		return
	case "img":
		f.flush()
		pic := f.b.picture(n, f.tmpl.unit)
		// The picture stands on a line of the paragraph around it, as an
		// inline image does: the paragraph's margins, and under the picture
		// the line's room below its baseline.
		if f.tmpl.kind == Paragraph {
			pic.top, pic.bottom = f.tmpl.top, f.tmpl.bottom+strut(f.b.face)
		}
		f.out = append(f.out, pic)
		return
	case "strong", "b":
		st |= bold
	case "em", "i", "cite", "var", "dfn":
		st |= italic
	case "s", "del", "strike":
		st |= strike
	case "sup":
		st |= sup
	case "sub":
		st |= sub
	case "code", "kbd", "samp", "tt":
		st |= mono
	case "a":
		if h := link(n.prop("href")); h != "" {
			href = h
			if hasWords(n) {
				f.mark(h, st)
			}
		}
	}
	f.nodes(n.Children, st, href)
}

func (f *flow) list(n *node, unit, id string) {
	l := &block{kind: List, ordered: n.Tag == "ol", start: 1, level: f.b.depth, unit: unit, anchor: id, bottom: 1.15}
	if s := n.prop("start"); s != "" {
		l.start = n.num("start")
	}
	f.b.depth++
	for _, ch := range n.Children {
		if ch.Type == "text" && strings.TrimSpace(ch.Value) == "" {
			continue
		}
		u := ch.prop("dataReaderUnit")
		if u == "" {
			u = unit
		}
		it := &block{kind: Item, unit: u, anchor: ch.prop("id"), top: .4, bottom: .4}
		if ch.Type == "element" && ch.Tag == "li" {
			it.children = f.child(ch, u)
		} else { // text or a list right inside a list: an item without its own marker is the browser's way, this is close
			g := &flow{b: f.b, tmpl: block{kind: Paragraph, unit: u}}
			g.node(ch, 0, "")
			it.children = g.done()
		}
		if len(it.children) > 0 {
			l.children = append(l.children, it)
		}
	}
	f.b.depth--
	if len(l.children) > 0 {
		f.out = append(f.out, l)
	}
}

func (f *flow) table(n *node, unit, id string) {
	t := &block{kind: Table, unit: unit, anchor: id, top: 1.5, bottom: 1.5}
	var visit func(n *node)
	visit = func(n *node) {
		for _, ch := range n.Children {
			if ch.Type != "element" {
				continue
			}
			switch ch.Tag {
			case "caption":
				g := &flow{b: f.b, tmpl: block{kind: Caption, unit: ch.prop("dataReaderUnit")}}
				g.nodes(ch.Children, 0, "")
				f.out = append(f.out, g.done()...)
			case "thead", "tbody", "tfoot":
				visit(ch)
			case "tr":
				var row []*cell
				for _, td := range ch.Children {
					if td.Type != "element" || (td.Tag != "td" && td.Tag != "th") {
						continue
					}
					c := &cell{header: td.Tag == "th", colSpan: max(1, td.num("colSpan")), rowSpan: max(1, td.num("rowSpan"))}
					c.blocks = f.child(td, td.prop("dataReaderUnit"))
					row = append(row, c)
				}
				if len(row) > 0 {
					t.rows = append(t.rows, row)
				}
			}
		}
	}
	visit(n)
	if len(t.rows) > 0 {
		t.numeric = numericColumns(t.rows)
		f.out = append(f.out, t)
	}
}

// numericColumns are the columns of rows whose cells under the head hold figures alone (1,024, 56ms, 9%, −3.5),
// at least one of them, the rest empty or a dash.
func numericColumns(rows [][]*cell) []bool {
	cols := 0
	for _, row := range rows {
		n := 0
		for _, cl := range row {
			n += cl.colSpan
		}
		cols = max(cols, n)
	}
	figures, other := make([]int, cols), make([]bool, cols)
	for _, row := range rows[headRows(rows):] {
		col := 0
		for _, cl := range row {
			if col >= cols {
				break
			}
			switch text := strings.TrimSpace(cellText(cl)); {
			case cl.colSpan > 1 || cl.header:
				other[col] = true
			case text == "" || text == "-" || text == "–" || text == "—":
			case figure.MatchString(text):
				figures[col]++
			default:
				other[col] = true
			}
			col += cl.colSpan
		}
	}
	out := make([]bool, cols)
	for i := range out {
		out[i] = figures[i] > 0 && !other[i]
	}
	return out
}

// figure is a cell's text that is a figure: a sign, a currency, digits with their separators, a unit.
var figure = regexp.MustCompile(`^[+\-−±~≈]?[$¥€£]?\d[\d,.\s]*(%|‰|x|×|[kKMGT]?B|ms|μs|ns|s|px|k|K|M)?$`)

// headRows is how many rows open the table holding headers alone, its head; none when every row does.
func headRows(rows [][]*cell) int {
	n := 0
	for _, row := range rows {
		for _, cl := range row {
			if !cl.header {
				if n == len(rows) {
					return 0
				}
				return n
			}
		}
		n++
	}
	return 0
}

// cellText is the words of a cell.
func cellText(cl *cell) string {
	var b strings.Builder
	walkBlocks(cl.blocks, func(bl *block) {
		for _, rn := range bl.runs {
			b.WriteString(rn.text)
		}
	})
	return b.String()
}

func hasClass(n *node, name string) bool {
	switch v := n.Properties["className"].(type) {
	case []any:
		for _, c := range v {
			if c == name {
				return true
			}
		}
	case string:
		return strings.Contains(" "+v+" ", " "+name+" ")
	}
	return false
}

// link keeps the links readerLink keeps: http(s) without credentials, and
// #fragments.
func link(v string) string {
	if strings.HasPrefix(v, "#") {
		return v
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return ""
	}
	return u.String()
}

// parseHTML makes nodes of the HTML of an article stored without a
// document, with the properties a document would hold.
func parseHTML(s string) []*node {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return nil
	}
	names := map[string]string{"id": "id", "href": "href", "src": "src", "alt": "alt", "title": "title",
		"width": "width", "height": "height", "colspan": "colSpan", "rowspan": "rowSpan", "start": "start",
		"data-reader-unit": "dataReaderUnit", "data-callout": "dataCallout", "class": "className"}
	var conv func(h *html.Node) *node
	conv = func(h *html.Node) *node {
		n := &node{}
		switch h.Type {
		case html.TextNode:
			n.Type, n.Value = "text", h.Data
			return n
		case html.ElementNode:
			n.Type, n.Tag, n.Properties = "element", h.Data, map[string]any{}
			for _, a := range h.Attr {
				if name, ok := names[a.Key]; ok {
					n.Properties[name] = a.Val
				}
			}
		default:
			n.Type = "root"
		}
		for ch := h.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type == html.TextNode || ch.Type == html.ElementNode || ch.Type == html.DocumentNode {
				n.Children = append(n.Children, conv(ch))
			}
		}
		return n
	}
	root := conv(doc)
	// The document's html and body hold the article.
	var find func(n *node) *node
	find = func(n *node) *node {
		if n.Tag == atom.Body.String() {
			return n
		}
		for _, ch := range n.Children {
			if b := find(ch); b != nil {
				return b
			}
		}
		return nil
	}
	if body := find(root); body != nil {
		return body.Children
	}
	return root.Children
}

// scripts cuts runs where Chinese and Japanese text meets other text. A
// font list of -apple-system then PingFang SC draws what
// the system face lacks in PingFang SC at the size given; MyGo's cascade
// draws it a little larger and its punctuation half wide (Core Text sizes
// the system face's fallbacks to it), so those runs name PingFang SC
// themselves. Where an ideograph meets a Latin letter or a digit with no
// space between, text-autospace: normal puts 1/8 em between them: the run
// ending there is marked for it.
func scripts(runs []run) []run {
	out := make([]run, 0, len(runs))
	var prev rune
	for _, rn := range runs {
		if rn.style&mono != 0 {
			// Code within text keeps one background: the system's
			// monospaced face falls back for its Chinese.
			out = append(out, rn)
			prev, _ = utf8.DecodeLastRuneInString(rn.text)
			continue
		}
		start, cur := 0, false // cur: the piece being cut is wide
		for i, ch := range rn.text {
			c := wide(ch)
			spaced := rn.style&mono == 0 && prev != 0 && (ideograph(prev) && narrow(ch) || narrow(prev) && ideograph(ch))
			if i == 0 {
				cur = c
				if n := len(out); spaced && n > 0 && out[n-1].style&(mono|autospace) == 0 { // the run before ends there
					last := out[n-1]
					out = appendPiece(out[:n-1], last, last.text, last.style&cjk != 0, true)
				}
			} else if c != cur || spaced {
				out = appendPiece(out, rn, rn.text[start:i], cur, spaced)
				start, cur = i, c
			}
			prev = ch
		}
		out = appendPiece(out, rn, rn.text[start:], cur, false)
	}
	return out
}

func appendPiece(out []run, rn run, text string, cjkText, spaced bool) []run {
	if text == "" {
		return out
	}
	rn.text = text
	if cjkText {
		rn.style |= cjk
	}
	if !spaced {
		return append(out, rn)
	}
	// The last character takes the space after it.
	_, n := utf8.DecodeLastRuneInString(text)
	if head := text[:len(text)-n]; head != "" {
		h := rn
		h.text = head
		out = append(out, h)
	}
	rn.text, rn.style = text[len(text)-n:], rn.style|autospace
	return append(out, rn)
}

// wide reports the characters the system face lacks and PingFang SC draws.
func wide(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Bopomofo) ||
		r >= 0x3000 && r <= 0x303F || r >= 0xFF00 && r <= 0xFFEF || r >= 0xFE30 && r <= 0xFE4F || r >= 0x31F0 && r <= 0x31FF
}

func ideograph(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana)
}

func narrow(r rune) bool {
	return (unicode.IsLetter(r) || unicode.IsDigit(r)) && !wide(r) && r < 0x2E80
}

// closing is the punctuation whose space is after it: before more
// punctuation it is set half wide (PingFang SC's halt feature), as CSS's
// text-spacing-trim: normal collapses the space between
// them. A closing mark squeezed half wide at the end of a line, which
// normal also allows, is not done: the line breaks are Core Text's.
const closing = "，。、．：；！？）」』》〉】〕〗〙〛｝］"

func trimPunctuation(runs []run) []run {
	out := make([]run, 0, len(runs))
	for i, rn := range runs {
		if rn.style&cjk == 0 {
			out = append(out, rn)
			continue
		}
		start := 0
		for at, ch := range rn.text {
			n := at + utf8.RuneLen(ch)
			next, _ := utf8.DecodeRuneInString(rn.text[n:])
			if n == len(rn.text) && i+1 < len(runs) && runs[i+1].style&cjk != 0 {
				next, _ = utf8.DecodeRuneInString(runs[i+1].text)
			}
			if !strings.ContainsRune(closing, ch) || !(strings.ContainsRune(closing, next) || strings.ContainsRune("（「『《〈【〔〖〘〚｛［", next)) {
				continue
			}
			if at > start {
				h := rn
				h.text = rn.text[start:at]
				out = append(out, h)
			}
			h := rn
			h.text, h.style = rn.text[at:n], rn.style|halfwidth
			out = append(out, h)
			start = n
		}
		if start < len(rn.text) {
			rn.text = rn.text[start:]
			out = append(out, rn)
		}
	}
	return out
}
