package extract

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"trie/internal/web"
)

// tidy is what the format does to an article's blocks before laying them out, the same for a page's body and a feed's,
// and again for a body kept before it did: the furniture at the start (a date line, "Translations: …", "Read on
// <site>") and at the end (an advertisement's label, a newsletter sign-up, a reprint notice, "The post … appeared first
// on …") goes; a first paragraph labelled "Summary:" becomes the dek, its label gone; and a caption repeated under each
// picture of a run stays only under the last. Tables lose their empty rows, and those that only lay things out become
// their blocks (tables); headings and table cells lose what shows as nothing at their ends, and a heading repeating the
// one just before goes (headings). Each step finds nothing to do in a body it has already tidied.
func tidy(root *hnode) {
	tables(root)
	headings(root)
	blocks := leafBlocks(root)
	removed := map[*hnode]bool{}
	drop := func(b placed) {
		b.parent.children = slices.DeleteFunc(b.parent.children, func(c *hnode) bool { return c == b.n })
		removed[b.n] = true
	}
	// The start: at most two lines before the first that is the article's.
	for i, gone := 0, 0; i < len(blocks) && gone < 2; i++ {
		b := blocks[i]
		text := collapse(plain(b.n))
		if text == "" || !(startFurniture.MatchString(text) && web.JSLen(text) <= 120 || dateOnly(b.n, text)) {
			break
		}
		drop(b)
		gone++
	}
	// The end: at most three lines after the article's last, passing over pictures and rules on the way.
	for i, gone, passed := len(blocks)-1, 0, 0; i >= 0 && gone < 3 && passed <= 2; i-- {
		b := blocks[i]
		if removed[b.n] {
			continue
		}
		text := collapse(plain(b.n))
		if text == "" {
			passed++
			continue
		}
		if web.JSLen(text) > 300 || !(endFurniture.MatchString(text) || newsletter.MatchString(text) || reprintNotice.MatchString(text)) {
			break
		}
		drop(b)
		gone++
	}
	for _, b := range blocks {
		if !removed[b.n] && collapse(plain(b.n)) != "" {
			dek(b.n)
			break
		}
	}
	dropEmpty(root)
	repeatedCaptions(root)
}

var (
	startFurniture = regexp.MustCompile(`(?i)^(?:translations?\s*[:：]|(?:read|view)\s+(?:this\s+)?on\s+[\w.-]+\.[a-z]{2,}\.?$)`)
	endFurniture   = regexp.MustCompile(`(?i)^(?:ads?|advertisement|sponsored|广告)$|^the post\s.{1,300}\sappeared first on\s.{1,120}$|^(?:read|view|continue reading|read more|read the full (?:post|article|story))\s+on\s+[\w.-]+\.[a-z]{2,}\.?$`)
	newsletter     = regexp.MustCompile(`(?i)\b(?:sign up|subscribe)\b.{0,60}\b(?:newsletters?|inbox|mailing list)\b`)
	reprintNotice  = regexp.MustCompile(`转载.{0,30}(?:注明|授权|联系|出处)|著作权归|版权所有`)
	dekLabel       = regexp.MustCompile(`(?i)^[\s\x{a0}]*(?:summary|tl;?\s?dr|abstract|in short|in brief|摘要|导语|导读|太长不看)[\s\x{a0}]*[:：][\s\x{a0}]*`)
	tidyContainers = set("div section article main header footer aside")
	tidyBlocks     = set("p h1 h2 h3 h4 h5 h6 div section article main header footer aside figure ul ol pre table blockquote hr")
)

// placed is a block and the element holding it.
type placed struct{ n, parent *hnode }

// leafBlocks are the blocks of a body in reading order that hold no block of their own: its paragraphs, headings,
// figures, lists, tables, quotes, code and rules, and a picture standing on its own between them.
func leafBlocks(root *hnode) []placed {
	var out []placed
	var walk func(parent *hnode)
	walk = func(parent *hnode) {
		for _, c := range parent.children {
			if c.typ != "element" {
				continue
			}
			switch {
			case tidyContainers[c.tag] && slices.ContainsFunc(c.children, func(k *hnode) bool { return k.typ == "element" && tidyBlocks[k.tag] }):
				walk(c)
			case tidyBlocks[c.tag] || c.tag == "img":
				out = append(out, placed{c, parent})
			}
		}
	}
	walk(root)
	return out
}

// dateOnly reports whether a paragraph is only a date, with at most tags, a byline or a reading time beside it, as
// withoutRepeatedHeader finds one at the top of a page.
func dateOnly(n *hnode, text string) bool {
	if n.tag != "p" && n.tag != "div" || web.JSLen(text) > 80 || !dateLine.MatchString(text) {
		return false
	}
	rest := collapse(separators.ReplaceAllString(hashTag.ReplaceAllString(dateLine.ReplaceAllString(text, ""), ""), " "))
	return web.JSLen(rest) <= 24
}

// dek makes a first paragraph labelled as the article's summary ("Summary:", "TL;DR:", "摘要：") its dek: the label goes
// and the paragraph is marked reader-dek. One with too few words after the label stays as it is.
func dek(n *hnode) {
	if !n.isElement("p") {
		return
	}
	text := plain(n)
	m := dekLabel.FindString(text)
	if m == "" || web.JSLen(collapse(text[len(m):])) < 20 {
		return
	}
	// The label's bytes come off the paragraph's first text, through the inline elements it starts in.
	left := len(m)
	var cut func(p *hnode) bool
	cut = func(p *hnode) bool {
		for _, c := range p.children {
			if left == 0 {
				return true
			}
			if c.typ == "text" {
				k := min(left, len(c.value))
				c.value, left = c.value[k:], left-k
				continue
			}
			if c.isElement("br") {
				left = max(0, left-1)
				continue
			}
			if cut(c) {
				return true
			}
		}
		return left == 0
	}
	cut(n)
	n.setProp("className", append(slices.Clone(n.classes()), "reader-dek"))
}

// dropEmpty removes the spans and containers the steps above left with nothing in them but white space; one an anchor
// points at (an id) stays.
func dropEmpty(n *hnode) {
	n.children = slices.DeleteFunc(n.children, func(c *hnode) bool {
		if c.typ != "element" {
			return false
		}
		dropEmpty(c)
		return blank(c)
	})
}

func blank(n *hnode) bool {
	if n.typ == "text" {
		return strings.TrimSpace(n.value) == ""
	}
	if _, anchored := n.prop("id"); anchored || !tidyContainers[n.tag] && n.tag != "span" {
		return false
	}
	return !slices.ContainsFunc(n.children, func(c *hnode) bool { return !blank(c) })
}

// repeatedCaptions keeps a caption shared by a run of figures, one after another with nothing between them, only under
// the last of them (Fonts In Use credits every photo of a post alike).
func repeatedCaptions(n *hnode) {
	var prev *hnode // the figure just before, while the run lasts
	for _, c := range n.children {
		switch {
		case c.typ == "text" && strings.TrimSpace(c.value) == "":
			continue
		case c.isElement("figure"):
			if prev != nil {
				if a, b := figcaption(prev), figcaption(c); a != nil && b != nil && collapse(plain(a)) != "" && collapse(plain(a)) == collapse(plain(b)) {
					prev.children = slices.DeleteFunc(prev.children, func(k *hnode) bool { return k == a })
				}
			}
			prev = c
			continue
		}
		prev = nil
		if c.typ == "element" {
			repeatedCaptions(c)
		}
	}
}

func figcaption(figure *hnode) *hnode {
	for _, c := range figure.children {
		if c.isElement("figcaption") {
			return c
		}
	}
	return nil
}

// tables tidies the tables of a body: a row with neither words nor a picture in any cell goes, the cells reaching into
// it from the rows above (rowspan) reaching one row less; a table that only lays things out (layoutTable) becomes the
// blocks of its cells, in reading order, and the scrolling box the format put round it goes with it.
func tables(n *hnode) {
	for i := 0; i < len(n.children); i++ {
		c := n.children[i]
		if c.typ != "element" {
			continue
		}
		nested := c.isElement("table") && slices.ContainsFunc(c.children, holdsTable)
		tables(c)
		var out []*hnode
		switch {
		case c.isElement("table"):
			dropBlankRows(c)
			if !layoutTable(c, nested) {
				continue
			}
			out = unwrapTable(c)
		case c.isElement("div") && slices.Contains(c.classes(), "reader-table-scroll") && !slices.ContainsFunc(c.children, func(k *hnode) bool { return k.isElement("table") }):
			out = c.children
		default:
			continue
		}
		n.children = slices.Replace(n.children, i, i+1, out...)
		i += len(out) - 1
	}
}

func holdsTable(n *hnode) bool {
	return n.isElement("table") || slices.ContainsFunc(n.children, holdsTable)
}

// tableRows are a table's rows in order, those of its head, bodies and foot, with the element holding each.
func tableRows(t *hnode) (rows, parents []*hnode) {
	for _, c := range t.children {
		switch {
		case c.isElement("tr"):
			rows, parents = append(rows, c), append(parents, t)
		case c.isElement("thead") || c.isElement("tbody") || c.isElement("tfoot"):
			for _, r := range c.children {
				if r.isElement("tr") {
					rows, parents = append(rows, r), append(parents, c)
				}
			}
		}
	}
	return rows, parents
}

func tableCells(row *hnode) []*hnode {
	var out []*hnode
	for _, c := range row.children {
		if c.isElement("td") || c.isElement("th") {
			out = append(out, c)
		}
	}
	return out
}

// span is a cell's colspan or rowspan, at least 1.
func span(cell *hnode, name string) int {
	v, _ := cell.prop(name)
	if f, ok := v.(float64); ok && f >= 1 {
		return int(min(f, 1000))
	}
	return 1
}

// shows reports whether n holds words or a picture.
func shows(n *hnode) bool {
	return collapse(plain(n)) != "" || holdsPicture(n)
}

func holdsPicture(n *hnode) bool {
	return n.isElement("img") || slices.ContainsFunc(n.children, holdsPicture)
}

func dropBlankRows(t *hnode) {
	rows, parents := tableRows(t)
	for i := len(rows) - 1; i >= 0; i-- {
		if slices.ContainsFunc(tableCells(rows[i]), shows) {
			continue
		}
		for j := range i {
			for _, cell := range tableCells(rows[j]) {
				if s := span(cell, "rowSpan"); j+s > i {
					cell.delProp("rowSpan")
					if s > 2 {
						cell.setProp("rowSpan", float64(s-1))
					}
				}
			}
		}
		parents[i].children = slices.DeleteFunc(parents[i].children, func(c *hnode) bool { return c == rows[i] })
	}
}

// layoutTable reports whether a table lays things out rather than setting data in rows and columns: without a head
// (th, thead, caption), it holds another table, or has one row or one column, or pictures and no words.
func layoutTable(t *hnode, nested bool) bool {
	var head func(n *hnode) bool
	head = func(n *hnode) bool {
		return n.isElement("th") || n.isElement("thead") || n.isElement("caption") || !n.isElement("table") && slices.ContainsFunc(n.children, head)
	}
	if slices.ContainsFunc(t.children, head) {
		return false
	}
	rows, _ := tableRows(t)
	cols := 0
	for _, r := range rows {
		n := 0
		for _, cell := range tableCells(r) {
			n += span(cell, "colSpan")
		}
		cols = max(cols, n)
	}
	return nested || len(rows) <= 1 || cols <= 1 || collapse(plain(t)) == ""
}

// unwrapTable is the blocks of a layout table's cells, one after another: a cell's blocks as they are, its loose words
// and pictures each run made a paragraph. A row's first cell of line numbers beside code (a gutter) goes.
func unwrapTable(t *hnode) []*hnode {
	var out []*hnode
	rows, _ := tableRows(t)
	for _, r := range rows {
		cells := tableCells(r)
		for i, cell := range cells {
			// Only a row's first cell is a gutter, and only beside code that is more than numbers itself.
			if i == 0 && len(cells) > 1 && gutter(cell) && !gutter(cells[1]) && hasCode(cells[1]) {
				continue
			}
			var loose []*hnode
			end := func() {
				if slices.ContainsFunc(loose, shows) {
					out = append(out, el("p", nil, loose...))
				}
				loose = nil
			}
			for _, c := range cell.children {
				if c.typ == "element" && (tidyBlocks[c.tag] || c.tag == "dl") {
					end()
					out = append(out, c)
				} else {
					loose = append(loose, c)
				}
			}
			end()
		}
	}
	return out
}

// hasCode reports whether n is or holds a code block.
func hasCode(n *hnode) bool { return n.isElement("pre") || slices.ContainsFunc(n.children, hasCode) }

// gutter reports whether a cell holds code that is only its lines' numbers, 1, 2, 3 …, one to a line.
func gutter(cell *hnode) bool {
	if !hasCode(cell) {
		return false
	}
	lines := strings.Fields(plain(cell))
	for i, l := range lines {
		if l != strconv.Itoa(i+1) {
			return false
		}
	}
	return len(lines) > 0
}

// headings trims what shows as nothing at the ends of each heading and table cell (trimEdges), where it would set the
// words in from the column, and drops a heading that repeats the one just before it at its level, nothing but empty
// blocks between them: a page's heading and its copy for narrow screens.
func headings(root *hnode) {
	var trim func(n *hnode)
	trim = func(n *hnode) {
		for _, c := range n.children {
			if headingTag.MatchString(c.tag) || c.tag == "td" || c.tag == "th" {
				trimEdges(c)
			} else if c.typ == "element" {
				trim(c)
			}
		}
	}
	trim(root)
	prev := ""
	for _, b := range leafBlocks(root) {
		switch text := b.n.tag + " " + collapse(plain(b.n)); {
		case headingTag.MatchString(b.n.tag):
			if strings.EqualFold(text, prev) {
				b.parent.children = slices.DeleteFunc(b.parent.children, func(c *hnode) bool { return c == b.n })
				continue
			}
			prev = text
		case !shows(b.n):
		default:
			prev = ""
		}
	}
}

// trimEdges takes off what shows as nothing at the start and the end of n: white space of any kind (the no-break space
// an icon left), line breaks, and inline elements with nothing in them, but for those an anchor points at (an id).
func trimEdges(n *hnode) {
	trimEdge(n, false)
	trimEdge(n, true)
}

// trimEdge trims one end of n and reports whether it found something shown there.
func trimEdge(n *hnode, end bool) bool {
	for k := 0; k < len(n.children); {
		i := k
		if end {
			i = len(n.children) - 1 - k
		}
		c := n.children[i]
		switch {
		case c.typ == "text":
			if end {
				c.value = strings.TrimRightFunc(c.value, invisible)
			} else {
				c.value = strings.TrimLeftFunc(c.value, invisible)
			}
			if c.value != "" {
				return true
			}
		case c.isElement("img"):
			return true
		case c.typ == "element" && !c.isElement("br"):
			if trimEdge(c, end) {
				return true
			}
			if _, anchored := c.prop("id"); anchored {
				k++
				continue
			}
		}
		n.children = slices.Delete(n.children, i, i+1)
	}
	return false
}

func invisible(r rune) bool {
	return unicode.IsSpace(r) || r == 0x200b || r == 0x2060 || r == 0xfeff
}
