package extract

import (
	"strings"
	"sync"

	"trie/internal/web"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The DOM the TS code works on is jsdom's; here it is golang.org/x/net/html's tree, read the way jsdom reads its own:
// parsed with scripting off (jsdom's default, so <noscript> holds markup), serialized as parse5 does for innerHTML, and
// with a <template>'s content outside the tree (x/net/html keeps it as children; jsdom in a separate fragment).

func parseDocument(s string) *html.Node {
	doc, err := html.ParseWithOptions(strings.NewReader(s), html.ParseOptionEnableScripting(false))
	if err != nil {
		return nil
	}
	return doc
}

// parseInto parses s as the children of an element named context: "template" for JSDOM.fragment and rehype-parse's
// fragments (parse5 without a context element), "div" for innerHTML on a <div>. A template's nodes are returned in a
// fragment (a DocumentNode, as no element is their parent); a div's in a detached <div>.
func parseInto(s, context string) *html.Node {
	ctx := &html.Node{Type: html.ElementNode, Data: context, DataAtom: atom.Lookup([]byte(context))}
	nodes, _ := html.ParseFragmentWithOptions(strings.NewReader(s), ctx, html.ParseOptionEnableScripting(false))
	box := ctx
	if context == "template" {
		box = &html.Node{Type: html.DocumentNode}
	}
	for _, n := range nodes {
		box.AppendChild(n)
	}
	return box
}

func isElement(n *html.Node, tags ...string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	for _, t := range tags {
		if n.Data == t {
			return true
		}
	}
	return len(tags) == 0
}

func htmlElement(n *html.Node, tag string) bool { return isElement(n, tag) && n.Namespace == "" }

// content is false for a template, whose children jsdom keeps out of the tree.
func content(n *html.Node) bool { return !htmlElement(n, "template") }

// elements lists n's element descendants in tree order, as a static querySelectorAll list.
func elements(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(p *html.Node) {
		for c := p.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if match == nil || match(c) {
				out = append(out, c)
			}
			if content(c) {
				walk(c)
			}
		}
	}
	if content(n) {
		walk(n)
	}
	return out
}

var selectors sync.Map

// sel compiles a CSS selector once; an invalid one is nil, which is what querySelectorAll throws for.
func sel(s string) cascadia.Matcher {
	if m, ok := selectors.Load(s); ok {
		if m == nil {
			return nil
		}
		return m.(cascadia.Matcher)
	}
	g, err := cascadia.ParseGroup(s)
	if err != nil {
		selectors.Store(s, nil)
		return nil
	}
	selectors.Store(s, cascadia.Matcher(g))
	return g
}

func queryAll(n *html.Node, selector string) []*html.Node {
	m := sel(selector)
	if m == nil {
		panic("invalid selector " + selector) // querySelectorAll throws
	}
	return elements(n, m.Match)
}

func query(n *html.Node, selector string) *html.Node {
	if list := queryAll(n, selector); len(list) > 0 {
		return list[0]
	}
	return nil
}

func matches(n *html.Node, selector string) bool { return sel(selector).Match(n) }

// closest is Element.closest: n or its nearest ancestor element matching selector.
func closest(n *html.Node, selector string) *html.Node {
	for ; n != nil && n.Type == html.ElementNode; n = n.Parent {
		if matches(n, selector) {
			return n
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	v, _ := getAttr(n, key)
	return v
}

// getAttr is getAttribute: the first attribute with that qualified name.
func getAttr(n *html.Node, key string) (string, bool) {
	if n == nil {
		return "", false
	}
	for _, a := range n.Attr {
		if qualified(a) == key {
			return a.Val, true
		}
	}
	return "", false
}

func hasAttr(n *html.Node, key string) bool { _, ok := getAttr(n, key); return ok }

func qualified(a html.Attribute) string {
	if a.Namespace != "" {
		return a.Namespace + ":" + a.Key
	}
	return a.Key
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if qualified(a) == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func newElement(tag string, attrs ...string) *html.Node {
	n := &html.Node{Type: html.ElementNode, Data: tag, DataAtom: atom.Lookup([]byte(tag))}
	for i := 0; i+1 < len(attrs); i += 2 {
		n.Attr = append(n.Attr, html.Attribute{Key: attrs[i], Val: attrs[i+1]})
	}
	return n
}

func textNode(s string) *html.Node { return &html.Node{Type: html.TextNode, Data: s} }

// textContent is Node.textContent.
func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(p *html.Node) {
		for c := p.FirstChild; c != nil; c = c.NextSibling {
			switch {
			case c.Type == html.TextNode:
				b.WriteString(c.Data)
			case c.Type == html.ElementNode && content(c):
				walk(c)
			}
		}
	}
	if n.Type == html.ElementNode && !content(n) {
		return ""
	}
	walk(n)
	return b.String()
}

func remove(n *html.Node) {
	if n.Parent != nil {
		n.Parent.RemoveChild(n)
	}
}

// replaceWith is ChildNode.replaceWith(...nodes); the nodes move from wherever they are.
func replaceWith(n *html.Node, nodes ...*html.Node) {
	parent := n.Parent
	if parent == nil {
		return
	}
	for _, c := range nodes {
		remove(c)
		parent.InsertBefore(c, n)
	}
	parent.RemoveChild(n)
}

func children(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, c)
	}
	return out
}

func clearChildren(n *html.Node) {
	for n.FirstChild != nil {
		n.RemoveChild(n.FirstChild)
	}
}

func cloneNode(n *html.Node) *html.Node {
	c := &html.Node{Type: n.Type, DataAtom: n.DataAtom, Data: n.Data, Namespace: n.Namespace, Attr: append([]html.Attribute(nil), n.Attr...)}
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		c.AppendChild(cloneNode(k))
	}
	return c
}

// contains is Node.contains: n is other or one of its ancestors.
func contains(n, other *html.Node) bool {
	for ; other != nil; other = other.Parent {
		if other == n {
			return true
		}
	}
	return false
}

func parentElement(n *html.Node) *html.Node {
	if n.Parent != nil && n.Parent.Type == html.ElementNode {
		return n.Parent
	}
	return nil
}

func firstElementChild(n *html.Node, tag string) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if htmlElement(c, tag) {
			return c
		}
	}
	return nil
}

func documentElement(doc *html.Node) *html.Node { return firstElementChild(doc, "html") }

func head(doc *html.Node) *html.Node {
	if root := documentElement(doc); root != nil {
		return firstElementChild(root, "head")
	}
	return nil
}

func body(doc *html.Node) *html.Node {
	if root := documentElement(doc); root != nil {
		for c := root.FirstChild; c != nil; c = c.NextSibling {
			if htmlElement(c, "body") || htmlElement(c, "frameset") {
				return c
			}
		}
	}
	return nil
}

// documentTitle is document.title: the first <title>'s own text, ASCII white space stripped and collapsed.
func documentTitle(doc *html.Node) string {
	for _, t := range elements(doc, func(n *html.Node) bool { return htmlElement(n, "title") }) {
		var b strings.Builder
		for c := t.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				b.WriteString(c.Data)
			}
		}
		return strings.Join(strings.FieldsFunc(b.String(), func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\f' || r == '\r' }), " ")
	}
	return ""
}

var voidElements = set("area base basefont bgsound br col embed frame hr img input keygen link meta param source track wbr")

// rawText are the elements whose text parse5 serializes unescaped (noscript is not one with scripting off).
var rawText = set("style script xmp iframe noembed noframes plaintext")

func innerHTML(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		serialize(&b, c)
	}
	return b.String()
}

func outerHTML(n *html.Node) string {
	var b strings.Builder
	serialize(&b, n)
	return b.String()
}

func serialize(b *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.ElementNode:
		b.WriteString("<" + n.Data)
		for _, a := range n.Attr {
			b.WriteString(" " + qualified(a) + `="`)
			escapeInto(b, a.Val, true)
			b.WriteString(`"`)
		}
		b.WriteString(">")
		if n.Namespace == "" && voidElements[n.Data] {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			serialize(b, c)
		}
		b.WriteString("</" + n.Data + ">")
	case html.TextNode:
		if p := n.Parent; p != nil && p.Type == html.ElementNode && p.Namespace == "" && rawText[p.Data] {
			b.WriteString(n.Data)
		} else {
			escapeInto(b, n.Data, false)
		}
	case html.CommentNode:
		b.WriteString("<!--" + n.Data + "-->")
	case html.DoctypeNode:
		b.WriteString("<!DOCTYPE " + n.Data + ">")
	case html.DocumentNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			serialize(b, c)
		}
	}
}

// escapeInto is entities' escapeText (& < > nbsp) or escapeAttribute (& " nbsp), as parse5 serializes.
func escapeInto(b *strings.Builder, s string, attribute bool) {
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == ' ':
			b.WriteString("&nbsp;")
		case r == '"' && attribute:
			b.WriteString("&quot;")
		case r == '<' && !attribute:
			b.WriteString("&lt;")
		case r == '>' && !attribute:
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
}

// collapse is JavaScript's text.replace(/\s+/g, ' ').trim().
func collapse(s string) string {
	return strings.Join(strings.FieldsFunc(s, web.JSSpace), " ")
}

func set(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}
