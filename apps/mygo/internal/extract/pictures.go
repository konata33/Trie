package extract

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"trie/internal/web"
)

// Readability scores text, so a post that sets its pictures in boxes beside its words (It's Nice That, Minimalissimo,
// a portfolio's case study) comes out as the words alone. The pictures are put back: the article's area is widened to
// the largest box around what Readability kept that adds pictures but hardly any words, and each picture of that box
// goes in after the block that comes before it on the page.

const markKey = "data-trie-at"

var (
	notPicture  = regexp.MustCompile(`(?i)logo|avatar|icon|emoji|sprite|badge|spinner|gravatar|placeholder|button|anchor|permalink`)
	srcsetWidth = regexp.MustCompile(`\s(\d+)w(?:\s*,|\s*$)`)
	furnitureOf = `nav, header, footer, aside, [role="navigation"], [role="banner"], [role="contentinfo"], [role="complementary"]`
	// Blocks a picture can be set after: not inside a paragraph, a list, a table, a figure, a quotation or a heading.
	settable = set("p div section article figure ul ol blockquote pre table h1 h2 h3 h4 h5 h6 hr")
	wrappers = `p, li, table, figure, blockquote, pre, h1, h2, h3, h4, h5, h6, a`
)

// markElements numbers the elements under n in document order, as markKey, so what Readability keeps can be found on
// the page again; the numbering is the elements by number.
func markElements(n *html.Node) []*html.Node {
	var marked []*html.Node
	var walk func(*html.Node)
	walk = func(p *html.Node) {
		for c := p.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				setAttr(c, markKey, strconv.Itoa(len(marked)))
				marked = append(marked, c)
				walk(c)
			}
		}
	}
	walk(n)
	return marked
}

func markOf(n *html.Node) int {
	v, ok := getAttr(n, markKey)
	if !ok {
		return -1
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return -1
	}
	return i
}

// picture reports whether img is a picture of the post: not a drawing, a logo, an avatar or an icon, nor marked small.
func picture(img *html.Node) bool {
	src := cmpOr(attr(img, "src"), attr(img, "data-src"), attr(img, "data-original"))
	srcset := cmpOr(attr(img, "srcset"), attr(img, "data-srcset"))
	if src == "" && srcset == "" || strings.HasPrefix(src, "data:") && srcset == "" {
		return false
	}
	if strings.HasSuffix(strings.ToLower(strings.SplitN(src, "?", 2)[0]), ".svg") {
		return false
	}
	if notPicture.MatchString(src + " " + attr(img, "alt") + " " + className(img) + " " + attr(img, "id")) {
		return false
	}
	width, _ := strconv.Atoi(attr(img, "width"))
	height, _ := strconv.Atoi(attr(img, "height"))
	for _, m := range srcsetWidth.FindAllStringSubmatch(srcset+" ", -1) {
		if w, _ := strconv.Atoi(m[1]); w > width {
			width = w
		}
	}
	return !(width > 0 && width < 300 || height > 0 && height < 200)
}

// pictures are the pictures under n not in page furniture.
func pictures(n *html.Node) []*html.Node {
	var out []*html.Node
	for _, img := range queryAll(n, "img") {
		if picture(img) && closest(img, furnitureOf) == nil {
			out = append(out, img)
		}
	}
	return out
}

// withPictures is content, Readability's article from a page whose elements markElements numbered (marked), with the
// pictures of the article's area put back; limit is the box it may not widen past.
func withPictures(content string, marked []*html.Node, limit *html.Node) string {
	box := parseInto(content, "div")
	kept := map[int]*html.Node{}
	var area *html.Node
	for _, n := range queryAll(box, "["+markKey+"]") {
		i := markOf(n)
		if i < 0 || i >= len(marked) {
			continue
		}
		kept[i] = n
		if area == nil {
			area = marked[i]
		} else {
			for !contains(area, marked[i]) {
				area = parentElement(area)
			}
		}
	}
	if area == nil {
		return content
	}
	has := func(img *html.Node) bool { _, ok := kept[markOf(img)]; return ok }
	// The first box with more pictures may add a quarter more words (a caption, a credit); a box beyond it must add
	// next to none, so the next posts' cards around an article stay out.
	words := web.JSLen(collapse(textContent(box)))
	most, wider, widerWords := 0, (*html.Node)(nil), 0
	for p := parentElement(area); p != nil && p.Data != "body" && p.Data != "html" && contains(limit, p); p = parentElement(p) {
		n := web.JSLen(collapse(textContent(p)))
		if n > words*5/4+100 || wider != nil && n > widerWords+40 {
			break
		}
		missing := 0
		for _, img := range pictures(p) {
			if !has(img) {
				missing++
			}
		}
		if missing > most {
			most, wider, widerWords = missing, p, n
		}
	}
	if wider == nil {
		return content
	}
	seen := map[string]bool{}
	for _, img := range queryAll(box, "img") {
		seen[cmpOr(attr(img, "src"), attr(img, "data-src"))] = true
	}
	// Where a picture goes: after the last block before it that it is not inside.
	var blocks []*html.Node
	for i, n := range kept {
		if settable[n.Data] && (parentElement(n) == nil || closest(parentElement(n), wrappers) == nil) {
			blocks = append(blocks, marked[i])
		}
	}
	slices.SortFunc(blocks, func(a, b *html.Node) int { return markOf(a) - markOf(b) })
	if len(blocks) == 0 {
		return content
	}
	type placed struct{ piece, before *html.Node }
	var places []placed
	leading := 0
	for _, img := range pictures(wider) {
		src := cmpOr(attr(img, "src"), attr(img, "data-src"))
		if has(img) || seen[src] {
			continue
		}
		seen[src] = true
		unit := img
		if f := closest(img, "figure"); f != nil && contains(wider, f) && len(queryAll(f, "img")) == 1 {
			unit = f
		}
		var before *html.Node
		for _, b := range blocks {
			if markOf(b) < markOf(unit) && !contains(b, unit) {
				before = b
			}
		}
		if before == nil {
			leading++
		}
		piece := cloneNode(unit)
		if piece.Data != "figure" {
			figure := newElement("figure")
			figure.AppendChild(piece)
			piece = figure
		}
		places = append(places, placed{piece, before})
	}
	after := map[*html.Node]*html.Node{} // the last picture set after each block
	last := blocks[len(blocks)-1]
	for _, p := range places {
		if p.before == nil {
			// A cover leads the words. More than three pictures ahead of them stand beside the words on the page (a
			// photo column next to a short text, a talk's slides that change as the words scroll by): set ahead one after
			// another they would bury the text, so they follow it.
			if leading <= 3 {
				first := kept[markOf(blocks[0])]
				first.Parent.InsertBefore(p.piece, first)
				continue
			}
			p.before = last
		}
		at := kept[markOf(p.before)]
		if last := after[p.before]; last != nil {
			at = last
		}
		at.Parent.InsertBefore(p.piece, at.NextSibling)
		after[p.before] = p.piece
	}
	return innerHTML(box)
}
