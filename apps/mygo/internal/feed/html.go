package feed

import (
	"errors"
	"math"
	"regexp"
	"sort"
	"strings"

	"trie/internal/locales"
	"trie/internal/web"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// HTML is read as jsdom reads it: a page with scripting disabled, so
// <noscript> holds markup, but JSDOM.fragment with scripting enabled, so there
// <noscript> holds text. querySelectorAll and textContent never enter a
// <template>'s contents, which x/net/html keeps as the template's children.

var spaces = regexp.MustCompile(`[` + web.Space + `]+`)

// units slices a string as JavaScript does, by UTF-16 code units.
func units(s string, n int) string {
	u := web.Units(s)
	if len(u) <= n {
		return s
	}
	return web.FromUnits(u[:n])
}

// plain is the text of an HTML fragment (the
// first 4000 characters), scripts and styles left out, white space collapsed,
// at most 300 characters.
func plain(value string) string {
	context := &html.Node{Type: html.ElementNode, Data: "template", DataAtom: atom.Template}
	nodes, err := html.ParseFragment(strings.NewReader(units(value, 4000)), context)
	if err != nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			b.WriteString(n.Data)
			return
		case n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "template" && n.Namespace == ""):
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return units(web.JSTrim(spaces.ReplaceAllString(b.String(), " ")), 300)
}

func document(page string) *html.Node {
	doc, _ := html.ParseWithOptions(strings.NewReader(page), html.ParseOptionEnableScripting(false))
	return doc
}

// elements are the elements named name under n in document order, as
// querySelectorAll finds them: any namespace, not inside a template.
func elements(n *html.Node, name string) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if c.Data == name {
				out = append(out, c)
			}
			if c.Data != "template" || c.Namespace != "" {
				walk(c)
			}
		}
	}
	walk(n)
	return out
}

// get is getAttribute: an attribute with no namespace of its own (x/net/html
// files xlink:href under the xlink namespace).
func get(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func value(n *html.Node, key string) string { v, _ := get(n, key); return v }

func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				b.WriteString(c.Data)
			} else if c.Type == html.ElementNode && (c.Data != "template" || c.Namespace != "") {
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

// words is the [attr~="word"] test for rel, whose value HTML matches without regard to ASCII case.
func words(v, word string) bool {
	for _, w := range strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' }) {
		if strings.EqualFold(w, word) && len(w) == len(word) {
			return true
		}
	}
	return false
}

var (
	feedType  = regexp.MustCompile(`(?:rss|atom)\+xml`)
	feedLabel = regexp.MustCompile(`^(?:rss|atom|feed|rss[` + web.Space + `]*feed|atom[` + web.Space + `]*feed|订阅[` + web.Space + `]*(?:rss|atom)|(?:rss|atom)[` + web.Space + `]*订阅)$`)
)

func errPageTooBig() error { return errors.New(locales.SubscribePageTooLarge()) }

// lower is toLowerCase for ASCII only: with it a case-sensitive pattern of
// ASCII letters matches as JavaScript's /i does, which never folds other
// letters onto ASCII ones.
func lower(s string) string { return strings.Map(web.LowerASCII, s) }

// DiscoverFeeds is up to three feeds a
// home page declares in its head or links to as "RSS", "Atom", "订阅 RSS"…
func DiscoverFeeds(page, pageURL string) ([]string, error) {
	if len(page) > 5*1024*1024 {
		return nil, errPageTooBig()
	}
	doc := document(page)
	var values []string
	if head := head(doc); head != nil {
		for _, l := range elements(head, "link") {
			if rel, ok := get(l, "rel"); ok && words(rel, "alternate") {
				if href, ok := get(l, "href"); ok && feedType.MatchString(lower(value(l, "type"))) {
					values = append(values, href)
				}
			}
		}
	}
	for _, a := range elements(doc, "a") {
		href, ok := get(a, "href")
		if !ok || within(a, "script", "style", "template") {
			continue
		}
		labels := []string{text(a), value(a, "aria-label"), value(a, "title")}
		if img := elements(a, "img"); len(img) > 0 {
			labels = append(labels, value(img[0], "alt"))
		}
		for _, label := range labels {
			if feedLabel.MatchString(lower(web.JSTrim(label))) {
				values = append(values, href)
				break
			}
		}
	}
	return first(values, pageURL, 3), nil
}

// head is document.head: the first head child of the html element.
func head(doc *html.Node) *html.Node {
	for h := doc.FirstChild; h != nil; h = h.NextSibling {
		if h.Type == html.ElementNode && h.DataAtom == atom.Html && h.Namespace == "" {
			for c := h.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && c.DataAtom == atom.Head && c.Namespace == "" {
					return c
				}
			}
		}
	}
	return nil
}

// within is closest(names): n or an ancestor has one of the names.
func within(n *html.Node, names ...string) bool {
	for ; n != nil; n = n.Parent {
		if n.Type == html.ElementNode {
			for _, name := range names {
				if n.Data == name {
					return true
				}
			}
		}
	}
	return false
}

// first resolves values against pageURL, keeping public addresses once each, at most limit.
func first(values []string, pageURL string, limit int) []string {
	out := []string{}
	for _, v := range values {
		if u := address(v, pageURL); u != nil && !contains(out, *u) {
			out = append(out, *u)
		}
	}
	return out[:min(len(out), limit)]
}

var (
	svgType  = regexp.MustCompile(`svg`)
	svgFile  = regexp.MustCompile(`\.svg(?:[?#]|$)`)
	iconWord = regexp.MustCompile(`(?:^|[` + web.Space + `])icon(?:[` + web.Space + `]|$)`)
)

// DiscoverIcons is a site's own icons,
// sharpest first (touch icons, then declared icons by size), then
// /favicon.ico; SVG is left out, the shell cannot draw it.
func DiscoverIcons(page, pageURL string) []string {
	if len(page) > 5*1024*1024 {
		return first([]string{"/favicon.ico"}, pageURL, 4)
	}
	type icon struct {
		href string
		rank float64
	}
	var icons []icon
	for _, l := range elements(document(page), "link") {
		rel, hasRel := get(l, "rel")
		href, hasHref := get(l, "href")
		if !hasRel || !hasHref || svgType.MatchString(lower(value(l, "type"))) || svgFile.MatchString(lower(href)) {
			continue
		}
		rel = strings.ToLower(rel)
		rank := -1.0
		if strings.Contains(rel, "apple-touch-icon") {
			rank = 2000
		} else if iconWord.MatchString(rel) {
			rank = 1000
		}
		if size := parseInt(value(l, "sizes")); !math.IsNaN(size) && size != 0 {
			rank += size
		}
		if rank >= 0 {
			icons = append(icons, icon{href, rank})
		}
	}
	sort.SliceStable(icons, func(i, j int) bool { return icons[i].rank > icons[j].rank })
	values := []string{}
	for _, i := range icons {
		values = append(values, i.href)
	}
	return first(append(values, "/favicon.ico"), pageURL, 4)
}

// parseInt is parseInt(s) with no radix: decimal, or hexadecimal after 0x; NaN for no digits.
func parseInt(s string) float64 {
	s = strings.TrimLeftFunc(s, web.JSSpace)
	sign := 1.0
	if s != "" && (s[0] == '+' || s[0] == '-') {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	base := 10.0
	if len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		base, s = 16, s[2:]
	}
	n, digits := 0.0, 0
	for _, c := range s {
		d := strings.IndexRune("0123456789abcdef", web.LowerASCII(c))
		if d < 0 || float64(d) >= base {
			break
		}
		n, digits = n*base+float64(d), digits+1
	}
	if digits == 0 {
		return math.NaN()
	}
	return sign * n
}
