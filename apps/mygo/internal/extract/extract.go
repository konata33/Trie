package extract

import (
	"encoding/base64"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"trie/internal/locales"
	"trie/internal/web"

	readeck "codeberg.org/readeck/go-readability/v2"
	"golang.org/x/net/html"
)

// Extraction: a fetched page (or a feed's body) read into the reader's cleaned HTML.

// Extracted is the result of reading a page or a feed's body.
type Extracted struct {
	Status     string   `json:"status"` // ready, summary, restricted or unavailable
	HTML       string   `json:"html"`
	TextLength int      `json:"textLength"`
	Links      []string `json:"links"`
	Images     []string `json:"images"`
	FeedURLs   []string `json:"feedURLs"`
	Message    *string  `json:"message"`
	Work       *Work    `json:"work,omitempty"`
}

const (
	maxBytes      = 2_000_000
	maxElements   = 20_000
	embeddedBytes = 512 * 1024
)

var (
	trustedImage  = regexp.MustCompile(`^data:image/(?:png|jpeg|gif|webp|avif|svg\+xml);base64,[A-Za-z0-9+/]+={0,2}$`)
	embedded      = regexp.MustCompile(`(?is)^data:image/(png|jpeg|gif|webp|avif|svg\+xml)(?:;charset=[\w-]+)?(;base64)?,(.*)$`)
	srcsetSplit   = regexp.MustCompile(`,` + jsSpaces.String())
	srcsetPart    = regexp.MustCompile(`^([^\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+)` + jsSpaces.String() + `(\d+)w$`)
	reservedHosts = regexp.MustCompile(`\.(?:localhost|local|internal|invalid|test)$`)
)

func ptr[T any](v T) *T { return &v }

func empty(message string) Extracted {
	return Extracted{Status: "unavailable", HTML: "", Links: []string{}, Images: []string{}, FeedURLs: []string{}, Message: &message}
}

const unreadable = "未能提取可读正文"

// specialRanges are ipaddr.js's: an address in none of them is unicast.
var specialRanges = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range strings.Fields(`0.0.0.0/8 255.255.255.255/32 224.0.0.0/4 169.254.0.0/16 127.0.0.0/8 100.64.0.0/10 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16
		192.0.0.0/24 192.0.2.0/24 192.88.99.0/24 198.18.0.0/15 198.51.100.0/24 203.0.113.0/24 240.0.0.0/4 192.175.48.0/24 192.31.196.0/24 192.52.193.0/24
		::/128 fe80::/10 ff00::/8 ::1/128 fc00::/7 fec0::/10 100::/64 ::ffff:0:0:0/96 64:ff9b::/96 64:ff9b:1::/48 2002::/16 2001::/32 2001:2::/48
		2001:3::/32 2001:4:112::/48 2620:4f:8000::/48 2001:10::/28 2001:20::/28 2001:30::/28 5f00::/16 2001::/23 2001:db8::/32 3fff::/20`) {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// publicURL is an http(s) address without credentials on a public host, resolved against
// base ("" for none), or "". It validates addresses, not DNS.
func publicURL(value, base string) string {
	trimmed := web.JSTrim(value)
	if trimmed == "" || web.JSLen(value) > 8192 || strings.IndexFunc(trimmed, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0 {
		return ""
	}
	var u *web.URL
	var err error
	if base == "" {
		u, err = web.ParseURL(value, nil)
	} else {
		u, err = web.ResolveURL(value, base)
	}
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Username != "" || u.Password != "" {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		for _, r := range specialRanges {
			if r.Contains(ip) {
				return ""
			}
		}
	} else if !strings.Contains(host, ".") || host == "localhost" || reservedHosts.MatchString(host) {
		return ""
	}
	return u.Href()
}

// withoutHash is a URL's href with its fragment cleared (url.hash = ”): the fragment starts at the first # of a serialized URL.
func withoutHash(href string) string { h, _, _ := strings.Cut(href, "#"); return h }

// fromSrcset is the size a reader column needs: the smallest candidate at least 1600 wide, else the largest.
func fromSrcset(srcset, base string) string {
	type candidate struct {
		url   string
		width float64
	}
	var cs []candidate
	for _, part := range srcsetSplit.Split(srcset, -1) {
		m := srcsetPart.FindStringSubmatch(web.JSTrim(part))
		if m == nil {
			continue
		}
		w, _ := strconv.ParseFloat(m[2], 64)
		if u := publicURL(m[1], base); u != "" {
			cs = append(cs, candidate{u, w})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].width < cs[j].width })
	for _, c := range cs {
		if c.width >= 1600 {
			return c.url
		}
	}
	if len(cs) > 0 {
		return cs[len(cs)-1].url
	}
	return ""
}

// nodeBase64 is Buffer.from(s, 'base64'): both alphabets, other characters skipped, stopping at the first '='.
func nodeBase64(s string) []byte {
	var out []byte
	bits, n := 0, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		var v int
		switch {
		case c >= 'A' && c <= 'Z':
			v = int(c - 'A')
		case c >= 'a' && c <= 'z':
			v = int(c-'a') + 26
		case c >= '0' && c <= '9':
			v = int(c-'0') + 52
		case c == '+' || c == '-':
			v = 62
		case c == '/' || c == '_':
			v = 63
		case c == '=':
			return out
		default:
			continue
		}
		bits, n = bits<<6|v, n+6
		if n >= 8 {
			n -= 8
			out = append(out, byte(bits>>n))
			bits &= 1<<n - 1
		}
	}
	return out
}

// nodeUTF8 is Buffer.toString('utf8'): each byte that is not part of a character becomes U+FFFD.
func nodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var s strings.Builder
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		s.WriteRune(r)
		b = b[size:]
	}
	return s.String()
}

// embeddedImage is a picture the page carries inside itself as a data: URL, kept only once vetted: SVG through the same
// sanitizer as a downloaded one, any other picture only if its bytes are what its label says. "" when not kept.
func embeddedImage(value string) string {
	m := embedded.FindStringSubmatch(web.JSTrim(value))
	if m == nil || web.JSLen(m[3]) > embeddedBytes*2 {
		return ""
	}
	var body []byte
	if m[2] != "" {
		body = nodeBase64(strings.Join(strings.FieldsFunc(m[3], web.JSSpace), ""))
	} else {
		decoded, err := url.PathUnescape(m[3])
		if err != nil || !utf8.ValidString(decoded) {
			return ""
		}
		body = []byte(decoded)
	}
	if len(body) == 0 || len(body) > embeddedBytes {
		return ""
	}
	kind := strings.ToLower(m[1])
	if kind == "svg+xml" {
		if svg := SanitizeSVG(nodeUTF8(body)); svg != "" {
			return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
		}
		return ""
	}
	if ImageType(body) == "image/"+kind {
		return "data:image/" + kind + ";base64," + base64.StdEncoding.EncodeToString(body)
	}
	return ""
}

// sanitizer is the extracted HTML's sanitizer: before caching (imageData nil) pictures keep their addresses; once cached
// every picture is a data: URL from imageData or has no source.
func sanitizer(base string, imageData map[string]string) *policy {
	schemes, imgSchemes := []string{"http", "https"}, []string{"http", "https", "data"}
	if imageData != nil {
		schemes, imgSchemes = []string{"http", "https", "data"}, []string{"data"}
	}
	return &policy{
		tags: readerTags,
		attributes: map[string][]string{"*": {"id"}, "a": {"href", "title"}, "img": {"src", "alt", "title", "width", "height"}, "code": {"class"}, "pre": {"class"},
			"th": {"colspan", "rowspan", "scope"}, "td": {"colspan", "rowspan"}, "ol": {"start"}},
		classes:      map[string][]string{"code": nil, "pre": nil},
		classPattern: map[string]*regexp.Regexp{"code": languageClass, "pre": languageClass},
		schemes:      schemes, imgSchemes: imgSchemes,
		nonText: set("script style textarea option svg math iframe object embed template noscript form"),
		transform: map[string]func(attrs) (string, attrs, *string){
			"a": func(a attrs) (string, attrs, *string) {
				href := a.get("href")
				if !strings.HasPrefix(href, "#") {
					href = publicURL(href, base)
				}
				var out attrs
				if href != "" {
					out = append(out, html.Attribute{Key: "href", Val: href})
				}
				if t := a.get("title"); t != "" {
					out = append(out, html.Attribute{Key: "title", Val: t})
				}
				return "a", out, nil
			},
			"img": func(a attrs) (string, attrs, *string) {
				// A 1x1 picture is a view counter (Medium's feeds end with one), not part of the article.
				if imageData == nil && a.get("width") == "1" && a.get("height") == "1" {
					return "span", nil, ptr("")
				}
				src := embeddedImage(a.get("src"))
				if src == "" {
					source := fromSrcset(cmpOr(a.get("srcset"), a.get("data-srcset")), base)
					for _, key := range []string{"src", "data-src", "data-original"} {
						if source == "" {
							source = publicURL(a.get(key), base)
						}
					}
					if imageData == nil {
						src = source
					} else if data, ok := imageData[source]; ok && source != "" && trustedImage.MatchString(data) {
						src = data
					}
				}
				if src == "" && imageData == nil {
					return "span", nil, ptr(a.get("alt"))
				}
				// An image that could not be saved stays in place without a source; the reader shows it as failed there.
				var out attrs
				if src != "" {
					out = append(out, html.Attribute{Key: "src", Val: src})
				}
				for _, key := range []string{"alt", "title"} {
					if v := a.get(key); v != "" {
						out = append(out, html.Attribute{Key: key, Val: v})
					}
				}
				for _, key := range []string{"width", "height"} {
					if v := a.get(key); dimension.MatchString(v) {
						out = append(out, html.Attribute{Key: key, Val: v})
					}
				}
				return "img", out, nil
			},
		},
	}
}

func cmpOr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func domFor(page string) *html.Node {
	if page == "" || len(page) > maxBytes {
		return nil
	}
	doc := parseDocument(page)
	if doc == nil || len(elements(doc, nil)) > maxElements {
		return nil
	}
	return doc
}

// topLevelPrefix tells whether a script's code before its placement calls is only complete top-level statements:
// no open bracket, string or comment, and ending in a semicolon. Template literals and regex are refused.
func topLevelPrefix(source []uint16) bool {
	var stack []uint16
	var quote uint16
	comment := ""
	for i := 0; i < len(source); i++ {
		c := source[i]
		var next uint16
		if i+1 < len(source) {
			next = source[i+1]
		}
		switch {
		case comment == "//":
			if c == '\n' || c == '\r' {
				comment = ""
			}
			continue
		case comment == "/*":
			if c == '*' && next == '/' {
				comment = ""
				i++
			}
			continue
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
			continue
		case '`':
			return false
		case '/':
			if next == '/' || next == '*' {
				comment = string(rune(c)) + string(rune(next))
				i++
				continue
			}
			return false
		case '(', '{', '[':
			stack = append(stack, c)
		case ')', '}', ']':
			want := map[uint16]uint16{')': '(', '}': '{', ']': '['}[c]
			if len(stack) == 0 || stack[len(stack)-1] != want {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	rest := web.JSTrim(web.FromUnits(source))
	return quote == 0 && comment == "" && len(stack) == 0 && (rest == "" || strings.HasSuffix(rest, ";"))
}

var (
	placement     = `\$(RS|RC)\(` + sp + `*(["'])([SPB]:[A-Za-z0-9_-]{1,64})["']` + sp + `*,` + sp + `*(["'])([SPB]:[A-Za-z0-9_-]{1,64})["']` + sp + `*\)`
	placementCall = regexp.MustCompile(placement)
	placementTail = regexp.MustCompile(`(?:` + placement + sp + `*;?` + sp + `*)+$`)
)

// resolveReactSegments moves React's streamed segments into the places their direct $RS/$RC calls name. Runtime
// helpers, RSC and JavaScript strings are never evaluated.
func resolveReactSegments(doc *html.Node) {
	ids := map[string][]*html.Node{}
	for _, n := range queryAll(doc, "[id]") {
		ids[attr(n, "id")] = append(ids[attr(n, "id")], n)
	}
	calls := 0
	for _, script := range queryAll(doc, "script") {
		if t, ok := getAttr(script, "type"); hasAttr(script, "src") && attr(script, "src") != "" || ok && t != "" && t != "text/javascript" && t != "application/javascript" && t != "module" {
			continue
		}
		source := textContent(script)
		loc := lastTail(source)
		if loc < 0 || !topLevelPrefix(web.Units(source[:loc])) {
			continue
		}
		placements := placementCall.FindAllStringSubmatch(source[loc:], -1)
		if calls+len(placements) > 100 {
			break
		}
		calls += len(placements)
		for _, p := range placements {
			op, first, second := p[1], p[3], p[5]
			sourceID, targetID := second, first
			if op == "RS" {
				sourceID, targetID = first, second
			}
			want := "B:"
			if op == "RS" {
				want = "P:"
			}
			if !strings.HasPrefix(sourceID, "S:") || !strings.HasPrefix(targetID, want) || len(ids[sourceID]) != 1 || len(ids[targetID]) != 1 {
				continue
			}
			segment, target := ids[sourceID][0], ids[targetID][0]
			if !contains(doc, segment) || !contains(doc, target) || !htmlElement(segment, "div") || !hasAttr(segment, "hidden") || !htmlElement(target, "template") || contains(segment, target) {
				continue
			}
			// A completed boundary's content takes the place of its fallback, as $RC does: what follows the template up to the
			// boundary's <!--/$--> (a "Loading..." box) goes. Without the closing comment the fallback stays.
			if op == "RC" {
				for _, n := range fallback(target) {
					remove(n)
				}
			}
			replaceWith(target, children(segment)...)
			remove(segment)
		}
	}
}

// HasStandIn reports whether a body kept a streamed box's stand-in as words: a paragraph that only says it is loading
// ("CodeBlock Loading..."), as pages read before boundaries dropped their fallbacks did.
func HasStandIn(page string) bool { return loadingStandIn.MatchString(page) }

var loadingStandIn = regexp.MustCompile(`<p>[A-Za-z ]{0,24}Loading(?:\.\.\.|…)</p>`)

// fallback is what a Suspense boundary shows until its content comes: the siblings after its template up to the
// boundary's closing <!--/$--> comment, nested boundaries (<!--$-->, <!--$?-->, <!--$!-->) included; none when the
// comment is not there.
func fallback(template *html.Node) []*html.Node {
	var nodes []*html.Node
	depth := 0
	for n := template.NextSibling; n != nil; n = n.NextSibling {
		if n.Type == html.CommentNode {
			switch n.Data {
			case "$", "$?", "$!":
				depth++
			case "/$":
				if depth == 0 {
					return nodes
				}
				depth--
			}
		}
		nodes = append(nodes, n)
	}
	return nil
}

// headingsFromLooseLines gives back the headings a feed lost its tags for (Smashing Magazine's feed writes each h2 as
// bare text between the paragraphs): a lone line of text standing between blocks, short, and not ending as a sentence
// does. It becomes a heading a level above the body's own (h2 under h3s), at most h2.
func headingsFromLooseLines(root *html.Node) {
	loose, level := looseLines(root)
	tag := string([]byte{'h', byte('0' + max(2, level-1))})
	for _, c := range loose {
		h := &html.Node{Type: html.ElementNode, Data: tag}
		c.Parent.InsertBefore(h, c)
		c.Parent.RemoveChild(c)
		h.AppendChild(&html.Node{Type: html.TextNode, Data: web.JSTrim(c.Data)})
	}
}

// HasLooseLines reports whether a body has lines headingsFromLooseLines would make headings of: a feed's body kept
// before they were.
func HasLooseLines(page string) bool {
	doc := domFor(page)
	if doc == nil {
		return false
	}
	loose, _ := looseLines(body(doc))
	return len(loose) > 0
}

// looseLines are root's lone lines of text between blocks, and the highest level of its own headings (3 without).
func looseLines(root *html.Node) (loose []*html.Node, level int) {
	level = 3
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && len(c.Data) == 2 && c.Data[0] == 'h' && c.Data[1] >= '1' && c.Data[1] <= '6' {
			level = min(level, int(c.Data[1]-'0'))
		}
		if c.Type != html.TextNode {
			continue
		}
		line := web.JSTrim(c.Data)
		if line == "" || strings.Contains(line, "\n") || web.JSLen(line) > 100 || lineEnd.MatchString(line) ||
			!isElement(nearElement(c, -1), looseBlocks...) || !isElement(nearElement(c, 1), looseBlocks...) {
			continue
		}
		loose = append(loose, c)
	}
	return loose, level
}

var (
	lineEnd     = regexp.MustCompile(`[.,;:!?。，；：！？、…）)」』"”]$`)
	looseBlocks = []string{"p", "ul", "ol", "pre", "figure", "blockquote", "table", "div", "hr", "h1", "h2", "h3", "h4", "h5", "h6"}
)

// nearElement is the element before (dir -1) or after (dir 1) n among its siblings, past whitespace; nil past text.
func nearElement(n *html.Node, dir int) *html.Node {
	for s := sibling(n, dir); s != nil; s = sibling(s, dir) {
		switch {
		case s.Type == html.ElementNode:
			return s
		case s.Type == html.TextNode && web.JSTrim(s.Data) != "":
			return nil
		}
	}
	return nil
}

func sibling(n *html.Node, dir int) *html.Node {
	if dir < 0 {
		return n.PrevSibling
	}
	return n.NextSibling
}

// labelCodeLanguages takes a code block's language from the label its site's code box shows above it ("TSX" over a
// pre, innei.in's boxes): a lone line just before the pre, or before the box holding only it, whose words are one of
// Shiki's language names. The code takes the language and the label goes; a pre that names its own keeps it.
func labelCodeLanguages(doc *html.Node) {
	for _, pre := range queryAll(doc, "pre") {
		code := pre
		if c := query(pre, "code"); c != nil {
			code = c
		}
		if languageClass.MatchString(firstLanguage(attr(pre, "class"))) || languageClass.MatchString(firstLanguage(attr(code, "class"))) {
			continue
		}
		// The box: the pre, or a wrapper whose only element is the box below it, at most two levels up.
		box := pre
		for range 2 {
			if label := previousElement(box); label != nil {
				words := strings.ToLower(web.JSTrim(labelWords(label)))
				if len(words) <= 20 && (shikiLanguages[words] || plainLabels[words]) && query(label, "pre, img, a") == nil {
					if shikiLanguages[words] {
						setAttr(code, "class", web.JSTrim(attr(code, "class")+" language-"+words))
					}
					remove(label)
					break
				}
			}
			if box.Parent == nil || !htmlElement(box.Parent, "div") || len(elementsOf(box.Parent)) != 1 {
				break
			}
			box = box.Parent
		}
	}
}

// plainLabels name code without a language (Shiki shows it as plain text): the label goes, the code stays plain.
var plainLabels = set("text plaintext plain txt")

// labelWords is the words a code box's label shows, without its buttons (复制, Copy).
func labelWords(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch {
			case c.Type == html.TextNode:
				b.WriteString(c.Data)
			case c.Type == html.ElementNode && c.Data != "button":
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

// firstLanguage is the first language-* class of classes, or "".
func firstLanguage(classes string) string {
	for _, c := range strings.Fields(classes) {
		if strings.HasPrefix(c, "language-") {
			return c
		}
	}
	return ""
}

// elementsOf is n's element children.
func elementsOf(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

// lastTail is where the run of placement calls ending the script starts, or -1. RE2 has no back-references, so the
// pattern takes either quote and each call of a run is checked to pair its own; a run holding one that does not starts
// again after it, as a back-referencing pattern's leftmost match would.
func lastTail(source string) int {
	for offset := 0; ; {
		loc := placementTail.FindStringIndex(source[offset:])
		if loc == nil {
			return -1
		}
		start, bad := offset+loc[0], -1
		for _, m := range placementCall.FindAllStringIndex(source[start:], -1) {
			if !quotesPair(source[start+m[0] : start+m[1]]) {
				bad = start + m[1]
			}
		}
		if bad < 0 {
			return start
		}
		offset = bad
	}
}

func quotesPair(call string) bool {
	var q []byte
	for i := 0; i < len(call); i++ {
		if call[i] == '"' || call[i] == '\'' {
			q = append(q, call[i])
		}
	}
	return len(q) == 4 && q[0] == q[1] && q[2] == q[3]
}

var (
	hiddenStyle    = regexp.MustCompile(`(?i)display` + sp + `*:` + sp + `*none|visibility` + sp + `*:` + sp + `*hidden|content-visibility` + sp + `*:` + sp + `*hidden`)
	hiddenMarker   = regexp.MustCompile(`(?i)^(?:comments?(?:-(?:list|section|container|area))?|related-posts|share-buttons)$`)
	hiddenSelector = `script, template, noscript, iframe, object, embed, svg, math, nav, header, footer, aside, [hidden], [inert], [aria-hidden="true"], [role="navigation"], [role="banner"], [role="contentinfo"]`
	styledSelector = `article, main, section, div, p, span, figure, img, form, dialog`
)

// className is Element.className as a string; an SVG element's is an SVGAnimatedString.
func className(n *html.Node) string {
	if n.Namespace == "svg" {
		return "[object SVGAnimatedString]"
	}
	return attr(n, "class")
}

// removeHidden drops what is not shown: hydration payloads, previews, page chrome, and what inline or author CSS hides.
func removeHidden(doc *html.Node) {
	for _, n := range queryAll(doc, hiddenSelector) {
		remove(n)
	}
	for _, n := range queryAll(doc, "*") {
		marker := attr(n, "id") + " " + className(n)
		if hiddenStyle.MatchString(attr(n, "style")) || slices.ContainsFunc(jsSpaces.Split(marker, -1), hiddenMarker.MatchString) {
			remove(n)
		}
	}
	// Author CSS can hide content without inline styles; inspect it before discarding it.
	styles := newCascade(doc)
	for _, n := range queryAll(doc, styledSelector) {
		if !contains(doc, n) || rulesOff["hidden-css"] {
			continue
		}
		if styles.display(n) == "none" || styles.visibility(n) == "hidden" {
			remove(n)
		}
	}
	for _, n := range queryAll(doc, "style, link") {
		remove(n)
	}
}

var (
	paidType     = regexp.MustCompile(`(?:Article|BlogPosting|NewsArticle)$`)
	safeSelector = regexp.MustCompile(`^[.#a-zA-Z0-9_\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff},>+~*-]+$`)
)

// schemaRestricted reads JSON-LD for paid articles and paid regions, removing the regions it can locate; a region it
// cannot locate safely empties the body.
func schemaRestricted(doc *html.Node) (restricted, verifiedBoundary bool) {
	uncertain := false
	var paidSelectors []string
	for _, script := range queryAll(doc, `script[type="application/ld+json"]`) {
		root, err := parseJS(textContent(script))
		if err != nil {
			continue // malformed metadata is not an extraction source
		}
		type item struct {
			value   any
			hasPart bool
		}
		queue := []item{{root, false}}
		for i := 0; i < len(queue) && i < maxElements; i++ {
			it := queue[i]
			switch v := it.value.(type) {
			case []any:
				for _, x := range v {
					if len(queue) < maxElements {
						queue = append(queue, item{x, it.hasPart})
					}
				}
			case *jsObject:
				types := []any{v.get("@type")}
				if list, ok := v.get("@type").([]any); ok {
					types = list
				}
				paid := v.get("isAccessibleForFree") == false || v.get("isAccessibleForFree") == "false"
				if paid && slices.ContainsFunc(types, func(t any) bool { s, ok := t.(string); return ok && paidType.MatchString(s) }) {
					restricted = true
				}
				if paid && it.hasPart {
					restricted = true
					selector, ok := v.get("cssSelector").(string)
					if !ok || web.JSTrim(selector) == "" || web.JSLen(selector) > 512 || !safeSelector.MatchString(selector) || len(paidSelectors) >= 16 {
						uncertain = true
					} else {
						paidSelectors = append(paidSelectors, selector)
					}
				}
				for _, key := range v.entries() {
					switch x := v.get(key); x.(type) {
					case *jsObject, []any:
						if len(queue) < maxElements {
							queue = append(queue, item{x, it.hasPart || key == "hasPart"})
						}
					}
				}
			}
		}
	}
	matched := 0
	for _, selector := range paidSelectors {
		m := sel(selector)
		if m == nil {
			uncertain = true
			continue
		}
		regions := elements(doc, m.Match)
		if len(regions) > 0 {
			matched++
		}
		for _, n := range regions {
			if n == body(doc) || n == documentElement(doc) {
				clearChildren(n)
			} else {
				remove(n)
			}
		}
	}
	if b := body(doc); uncertain && b != nil {
		clearChildren(b)
	}
	return restricted, !uncertain && len(paidSelectors) > 0 && matched == len(paidSelectors)
}

func finish(page, base, status string, feeds []string, message *string, allowShort bool) Extracted {
	cleaned := sanitizer(base, nil).run(page)
	if feeds == nil {
		feeds = []string{}
	}
	if len(cleaned) > maxBytes {
		e := empty(locales.ReaderTextTooLarge())
		if status == "restricted" {
			e.Status = "restricted"
		}
		e.FeedURLs = feeds
		return e
	}
	fragment := parseInto(cleaned, "template")
	textLength := web.JSLen(collapse(textContent(fragment)))
	links := []string{}
	for _, a := range queryAll(fragment, "a[href]") {
		if href := attr(a, "href"); !strings.HasPrefix(href, "#") && !slices.Contains(links, href) {
			links = append(links, href)
		}
	}
	// Addresses still to fetch; an embedded picture is already here. Both count as the article's pictures.
	var pictures []string
	images := []string{}
	for _, img := range queryAll(fragment, "img[src]") {
		src := attr(img, "src")
		pictures = append(pictures, src)
		if !strings.HasPrefix(src, "data:") && !slices.Contains(images, src) {
			images = append(images, src)
		}
	}
	imageArticle := len(pictures) > 0 && query(fragment, "figcaption, h1, h2") != nil && textLength >= 10
	prose := cloneNode(fragment)
	for _, n := range queryAll(prose, "a, h1, h2, h3, h4, h5, h6") {
		remove(n)
	}
	proseLength := web.JSLen(collapse(textContent(prose)))
	short := !allowShort && (textLength < 120 || proseLength < 120) && !imageArticle && !rulesOff["short"]
	if status != "restricted" && (short || textLength == 0 && len(pictures) == 0) {
		e := empty(unreadable)
		e.FeedURLs = feeds
		return e
	}
	return Extracted{Status: status, HTML: cleaned, TextLength: textLength, Links: links, Images: images, FeedURLs: feeds, Message: message}
}

const (
	month = `(?:jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.?`
	sp    = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`
	date  = `\b\d{1,2}(?:st|nd|rd|th)?` + sp + `+` + month + `,?` + sp + `+\d{4}\b|\b` + month + sp + `+\d{1,2}(?:st|nd|rd|th)?,?` + sp + `+\d{4}\b|` +
		`\b\d{4}[-/.]\d{1,2}[-/.]\d{1,2}\b|\d{4}` + sp + `*年` + sp + `*\d{1,2}` + sp + `*月(?:` + sp + `*\d{1,2}` + sp + `*日)?|\d{1,2}` + sp + `*月` + sp + `*\d{1,2}` + sp + `*日`
)

var (
	dateLine     = regexp.MustCompile(`(?i)` + date)
	hashTag      = regexp.MustCompile(`#[\p{L}\p{N}_-]+`)
	separators   = regexp.MustCompile(`[·•|/,–—-]`)
	squashStart  = regexp.MustCompile(`^["'“”‘’「」『』]+`)
	squashEnd    = regexp.MustCompile(`["'“”‘’「」『』.:：!！?？]+$`)
	magazineLbl  = regexp.MustCompile(`(?i)^(?:info|share|share this(?: article| post)?|tags?|categories|category|author` + sp + `.+)$`)
	tagsLabel    = regexp.MustCompile(`(?i)^(?:tags?|categories|category)$`)
	sentenceEnd  = regexp.MustCompile(`[.!?。！？]`)
	headingTag   = regexp.MustCompile(`^h[1-6]$`)
	tocHeading   = regexp.MustCompile(`(?i)^(?:table of contents|contents|on this page|toc|目录|本文目录)$`)
	titleDivider = regexp.MustCompile(sp + `+[|–—-]` + sp + `+`)
)

func squash(text string) string {
	s := web.FromUnits(web.EncodeUnits(web.JSLower([]rune(collapse(text)))))
	return squashEnd.ReplaceAllString(squashStart.ReplaceAllString(s, ""), "")
}

func isHeading(n *html.Node) bool {
	return n.Type == html.ElementNode && headingTag.MatchString(n.Data)
}

// withoutFurniture removes, in place, page furniture that is not the article: a table of contents whose every entry is
// only a link to a heading of the same page, and an interactive figure flattened into labels (a <figure> holding
// section headings), which becomes one line leading to it on the original page.
func withoutFurniture(root *html.Node, pageURL string) {
	page := ""
	if u, err := web.ParseURL(pageURL, nil); err == nil {
		page = withoutHash(u.Href())
	}
	inPage := func(href string, ok bool) bool {
		if !ok {
			return false
		}
		if strings.HasPrefix(href, "#") {
			return len(href) > 1
		}
		u, err := web.ResolveURL(href, page)
		if err != nil {
			return false
		}
		full := u.Href()
		bare, hash, _ := strings.Cut(full, "#")
		return hash != "" && bare == page
	}
	for _, list := range queryAll(root, "ul, ol") {
		if !contains(root, list) || parentElement(list) != nil && closest(parentElement(list), "ul, ol") != nil {
			continue
		}
		links := queryAll(list, "a")
		words := strings.Join(strings.FieldsFunc(textContent(list), web.JSSpace), "")
		var joined strings.Builder
		every := true
		for _, a := range links {
			joined.WriteString(strings.Join(strings.FieldsFunc(textContent(a), web.JSSpace), ""))
			if !inPage(getAttr(a, "href")) {
				every = false
			}
		}
		if len(links) < 3 || !every || words != joined.String() {
			continue
		}
		if h := previousElement(list); h != nil && isHeading(h) && tocHeading.MatchString(web.JSTrim(textContent(h))) {
			remove(h)
		}
		remove(list)
	}
	for _, figure := range queryAll(root, "figure") {
		if !contains(root, figure) || query(figure, "h1, h2, h3, h4, h5, h6") == nil {
			continue
		}
		href := page
		if anchor := query(figure, "[id]"); anchor != nil {
			if id := attr(anchor, "id"); id != "" {
				href += "#" + id
			}
		}
		link := newElement("a", "href", href)
		link.AppendChild(textNode("互动示例，在原网页查看"))
		line := newElement("p")
		line.AppendChild(link)
		replaceWith(figure, line)
	}
}

func previousElement(n *html.Node) *html.Node {
	for p := n.PrevSibling; p != nil; p = p.PrevSibling {
		if p.Type == html.ElementNode {
			return p
		}
	}
	return nil
}

// withoutRepeatedHeader drops the page's own header where the extractor kept it at the top of the body: a block
// repeating the title the reader already shows, and a short line that is only the date (with tags, a byline or a reading
// time beside it), and a magazine's labels. Only a run at the very start goes.
func withoutRepeatedHeader(page string, titles []string, pageURL string) string {
	wanted := map[string]bool{}
	for _, t := range titles {
		if s := squash(t); web.JSLen(s) >= 2 {
			wanted[s] = true
		}
	}
	box := parseInto(page, "div")
	before := innerHTML(box)
	if !rulesOff["furniture"] {
		withoutFurniture(box, pageURL)
	}
	tidied := innerHTML(box) != before
	var blocks []*html.Node
	for _, n := range queryAll(box, "h1,h2,h3,h4,h5,h6,p,div,header,time") {
		if query(n, "h1,h2,h3,h4,h5,h6,p,div,header,ul,ol,pre,blockquote,figure,table,img,picture,video") == nil {
			blocks = append(blocks, n)
		}
	}
	removed, tags := 0, false
	for _, n := range blocks {
		text := collapse(textContent(n))
		if text == "" {
			continue
		}
		title := wanted[squash(text)]
		prose := !(len(n.Data) == 2 && n.Data[0] == 'h' && n.Data[1] >= '0' && n.Data[1] <= '9')
		length := web.JSLen(text)
		dated := prose && length <= 80 && dateLine.MatchString(text) &&
			web.JSLen(collapse(separators.ReplaceAllString(hashTag.ReplaceAllString(dateLine.ReplaceAllString(text, ""), ""), " "))) <= 24
		label := prose && length <= 48 && magazineLbl.MatchString(text)
		tag := prose && tags && length <= 24 && !sentenceEnd.MatchString(text)
		if removed >= 8 || removed >= 3 && !label && !tag || !title && !dated && !label && !tag {
			break
		}
		tags = tagsLabel.MatchString(text)
		gone := n
		for p := parentElement(gone); p != nil && p != box && web.JSTrim(strings.Replace(textContent(p), text, "", 1)) == "" && query(p, "img,picture,video") == nil; p = parentElement(gone) {
			gone = p
		}
		remove(gone)
		removed++
	}
	if removed > 0 || tidied {
		return innerHTML(box)
	}
	return page
}

var (
	excalidraw  = regexp.MustCompile(`"type":"excalidraw"`)
	blockID     = regexp.MustCompile(`"blockId":"([\w-]{4,64})"`)
	sceneFile   = regexp.MustCompile(`"snapshot":"(https://[^"\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]{1,500}?\.json)"`)
	jsonQuoteEs = strings.NewReplacer(`\"`, `"`)
)

// placeWhiteboards turns each empty "Whiteboard" block of Innei's blog into a picture whose source is the Excalidraw
// scene file its document data names; the reader draws it when it saves the article's pictures (whiteboard.go).
func placeWhiteboards(doc *html.Node, page string) {
	data := jsonQuoteEs.Replace(page)
	units := web.Units(data)
	for _, loc := range excalidraw.FindAllStringIndex(data, -1) {
		start := web.JSLen(data[:loc[0]])
		near := web.FromUnits(units[start:min(len(units), start+400)])
		id, scene := blockID.FindStringSubmatch(near), sceneFile.FindStringSubmatch(near)
		if id == nil || scene == nil {
			continue
		}
		block := query(doc, `[data-block-id="`+id[1]+`"]`)
		if block == nil || query(block, "img, picture, video, svg") != nil || web.JSLen(web.JSTrim(textContent(block))) > 40 {
			continue
		}
		picture := newElement("img", "src", scene[1], "alt", cmpOr(web.JSTrim(textContent(block)), "Whiteboard"))
		figure := newElement("figure")
		figure.AppendChild(picture)
		replaceWith(block, figure)
	}
}

var (
	feedType = regexp.MustCompile(`(?i)^application/(?:rss|atom)\+xml$`)
	paywall  = regexp.MustCompile(`(?i)(?:^|[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}_-])(?:paywall|login-wall|subscription-wall|subscribe-wall|metered-wall|registration-wall)(?:$|[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}_-])`)
	loginAsk = regexp.MustCompile(`(?i)(?:sign in|log in|subscribe|登录|订阅).{0,40}(?:read|continue|阅读|继续)`)
)

// ExtractPage reads the article of a fetched page. trustedArticle is a page a feed links
// to as an article, which may be read without semantic article markup.
func ExtractPage(page, pageURL string, trustedArticle bool) (result Extracted) {
	base := publicURL(pageURL, "")
	if base == "" {
		return empty("无效的原文地址")
	}
	defer func() {
		if recover() != nil {
			result = empty(unreadable)
		}
	}()
	doc := domFor(page)
	if doc == nil {
		return empty(unreadable)
	}
	if strings.Contains(page, "excalidraw") && !rulesOff["whiteboard"] {
		placeWhiteboards(doc, page)
	}
	feeds := []string{}
	if h := head(doc); h != nil {
		for _, n := range queryAll(h, `link[rel~="alternate"][href]`) {
			if feedType.MatchString(attr(n, "type")) {
				if u := publicURL(attr(n, "href"), base); u != "" && !slices.Contains(feeds, u) {
					feeds = append(feeds, u)
				}
			}
		}
	}
	description := ""
	if h := head(doc); h != nil {
		description = attr(query(h, `meta[property="og:description"][content]`), "content")
		if description == "" {
			description = attr(query(h, `meta[name="description"][content]`), "content")
		}
	}
	description = web.FromUnits(truncateUnits(web.Units(collapse(description)), 1000))
	if gallery := galleryPage(doc, base); gallery != nil && !rulesOff["gallery"] {
		p := finish(gallery.html, base, "ready", feeds, nil, true)
		if p.Status != "ready" {
			return p
		}
		w := gallery.work
		u := func(v *string) *string {
			if v == nil {
				return nil
			}
			if s := publicURL(*v, base); s != "" {
				return &s
			}
			return nil
		}
		w.Site, w.Poster, w.Video = u(w.Site), u(w.Poster), u(w.Video)
		for i := range w.Sections {
			w.Sections[i].Image = u(w.Sections[i].Image)
		}
		p.Work = &w
		return p
	}
	if !rulesOff["react"] {
		resolveReactSegments(doc)
	}
	labelCodeLanguages(doc)
	restricted, verified := false, false
	if !rulesOff["paywall"] {
		restricted, verified = schemaRestricted(doc)
	}
	if !rulesOff["hidden"] {
		removeHidden(doc)
	}
	var widgets []*html.Node
	for _, n := range queryAll(doc, `[id], [class], [role="dialog"]`) {
		if paywall.MatchString(attr(n, "id")+" "+className(n)) || attr(n, "role") == "dialog" && loginAsk.MatchString(textContent(n)) {
			widgets = append(widgets, n)
		}
	}
	restricted = restricted || len(widgets) > 0 && !rulesOff["paywall"]
	for _, n := range widgets {
		remove(n)
	}
	if !rulesOff["forms"] {
		for _, n := range queryAll(doc, "form") {
			remove(n)
		}
	}
	root := articleRoot(doc)
	if rulesOff["root"] {
		root = nil
	}
	if restricted {
		if verified {
			inner := ""
			if root != nil {
				inner = innerHTML(root)
			}
			return finish(inner, base, "restricted", feeds, ptr("原文需要登录或订阅；仅保存公开可见的内容"), false)
		}
		// A visible DOM can still contain blurred or clipped paid text. Only metadata is safe without a verified boundary.
		escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(description)
		if escaped != "" {
			escaped = "<p>" + escaped + "</p>"
		}
		return finish(escaped, base, "restricted", feeds, ptr("原文需要登录或订阅；仅显示发布者提供的公开简介"), false)
	}
	if root == nil && query(doc, `meta[property="og:type"][content="article"]`) == nil && !trustedArticle {
		e := empty(unreadable)
		e.FeedURLs = feeds
		return e
	}
	// Restrict fallback to one semantic article container, never the arbitrary page body.
	clone := cloneNode(doc)
	if root != nil {
		if b := body(clone); b != nil {
			clearChildren(b)
			b.AppendChild(cloneNode(root))
		}
	}
	content, title := parseArticle(clone, base)
	if content == "" && root != nil {
		content = innerHTML(root)
	}
	// The title as the page names it, with and without the site's name after a separator ("Person of Interest – Wilson
	// Tovar"); not the page's <h1>, which may say more than the feed's title the reader shows.
	var titles []string
	for _, t := range []string{title, attr(query(doc, `meta[property="og:title"]`), "content"), documentTitle(doc)} {
		if web.JSTrim(t) != "" {
			titles = append(titles, t, titleDivider.Split(t, 2)[0])
		}
	}
	if !rulesOff["header"] {
		content = withoutRepeatedHeader(content, titles, base)
	}
	return finish(content, base, "ready", feeds, nil, false)
}

// articleRoot is the page's one <article>, or with none its one <main>, when it holds the page's text.
//
// Intended difference from the bench's reference outputs (2026-11-09): a root with under a tenth of the body's text is a box in a
// longer page, not the article (lea.verou.me's <article class="note">: 158 of 24,088 characters), and the page is read
// whole instead. On the bench's 193 pages such roots hold 0 to 2.8% of the text and real ones from 22.9% up (short pages
// whose chrome outweighs the post), so a tenth leaves every other page as it was; half would change ten more.
func articleRoot(doc *html.Node) *html.Node {
	articles := queryAll(doc, "article")
	mains := queryAll(doc, `main, [role="main"]`)
	var root *html.Node
	switch {
	case len(articles) == 1:
		root = articles[0]
	case len(articles) == 0 && len(mains) == 1:
		root = mains[0]
	}
	if b := body(doc); root != nil && b != nil && !rulesOff["root-share"] {
		if page := web.JSLen(collapse(textContent(b))); web.JSLen(collapse(textContent(root)))*10 < page {
			return nil
		}
	}
	return root
}

func truncateUnits(u []uint16, n int) []uint16 { return u[:min(len(u), n)] }

// rulesOff switches rules off for the bench's progress table (the patches of .context/mygo/extract-spike/ablate.ts, by
// name); nil in the app.
var rulesOff map[string]bool

// parseArticle is the Readability layer; the bench swaps in Mozilla's own output to tell its differences from readeck's.
var parseArticle = readability

// readability is new Readability(clone, { maxElemsToParse, charThreshold: 120, keepClasses: true }).parse(): the
// article's HTML (Readability's content, its page <div> included) and title; "" when it finds none.
func readability(doc *html.Node, base string) (string, string) {
	pageURL, err := url.Parse(base)
	if err != nil {
		return "", ""
	}
	p := readeck.NewParser()
	p.MaxElemsToParse, p.CharThresholds, p.KeepClasses = maxElements, 120, true
	a, err := p.ParseAndMutate(doc, pageURL)
	if err != nil {
		panic(err) // Readability throws over maxElemsToParse; extractPage's catch makes it unavailable
	}
	if a.Node == nil {
		return "", ""
	}
	return outerHTML(a.Node), a.Title()
}

// CacheImages returns the extracted HTML with each picture's address replaced by its saved
// copy (a data: URL, SVG sanitized again), or left without a source.
func CacheImages(page string, imageData map[string]string) string {
	if len(page) > maxBytes {
		return ""
	}
	safe := map[string]string{}
	for u, data := range imageData {
		if !strings.HasPrefix(data, "data:image/svg+xml;") {
			safe[u] = data
			continue
		}
		m := svgData.FindStringSubmatch(data)
		if m == nil || len(m[1]) > 350_000 {
			continue
		}
		raw, _ := base64.StdEncoding.DecodeString(m[1])
		if raw == nil {
			raw = nodeBase64(m[1])
		}
		if svg := SanitizeSVG(nodeUTF8(raw)); svg != "" {
			safe[u] = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
		}
	}
	return sanitizer("https://invalid.invalid/", safe).run(page)
}

var svgData = regexp.MustCompile(`^data:image/svg\+xml;base64,([A-Za-z0-9+/]+={0,2})$`)

var (
	syndication = regexp.MustCompile(`(?i)^by` + sp + `+[^\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+` + sp + `+from` + sp + `+https?://[^\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+`)
)

// ExtractFeedBody reads a body a feed carries, read like a page's. kind is "body" or
// "summary".
func ExtractFeedBody(page, base, kind string) Extracted {
	u := publicURL(base, "")
	if u == "" {
		return empty("无效的正文来源地址")
	}
	doc := domFor(page)
	if doc == nil {
		return empty(unreadable)
	}
	if restricted, _ := schemaRestricted(doc); restricted {
		return ExtractPage(`<html><head><meta property="og:type" content="article"></head><body>`+page+`</body></html>`, u, false)
	}
	removeHidden(doc)
	b := body(doc)
	withoutFurniture(b, u)
	headingsFromLooseLines(b)
	// A syndication notice opening the post ("by zhangxinxu from <the post's address> / 本文可全文转载…"): the reader names
	// the author and links the original itself.
	for c := b.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && web.JSTrim(textContent(c)) != "" {
			if notice := collapse(textContent(c)); htmlElement(c, "p") && web.JSLen(notice) <= 240 && syndication.MatchString(notice) {
				remove(c)
			}
			break
		}
	}
	// The feed's own "Full Review" / "Read more" link at the end: the reader offers the original itself (阅读原文).
	if blocks := queryAll(b, "p, div"); len(blocks) > 0 {
		last := blocks[len(blocks)-1]
		if link := query(last, "a"); link != nil && web.JSTrim(textContent(last)) == web.JSTrim(textContent(link)) && moreLink.MatchString(web.JSTrim(textContent(link))) {
			remove(last)
		}
	}
	if kind == "summary" || IsTeaser(page) {
		return finish(innerHTML(b), u, "summary", nil, ptr("订阅源提供的是摘要，可打开原文继续阅读"), true)
	}
	return finish(innerHTML(b), u, "ready", nil, ptr("正文由订阅源提供"), true)
}

// articleKey is the address an article is matched by: public, without www., fragment or trailing slash.
func articleKey(value, base string) string {
	safe := publicURL(value, base)
	if safe == "" {
		return ""
	}
	key, err := web.URLKey(safe)
	if err != nil {
		return ""
	}
	return key
}

// ExtractFeed returns the body a publisher's feed carries for one of the article's addresses. ok is false when the
// feed is refused or unreadable, or carries no body for them.
func ExtractFeed(xml, feedURL string, articleURLs []string) (result Extracted, ok bool) {
	base := publicURL(feedURL, "")
	if base == "" || xml == "" || len(xml) > maxBytes || doctypeOrEntity.MatchString(xml) {
		return Extracted{}, false
	}
	defer func() {
		if recover() != nil {
			result, ok = Extracted{}, false
		}
	}()
	items, err := feedItems(xml)
	if err != nil {
		return Extracted{}, false
	}
	wanted := map[string]bool{}
	for _, u := range articleURLs {
		if k := articleKey(u, ""); k != "" {
			wanted[k] = true
		}
	}
	for _, item := range items {
		if !truthyJS(item.link) {
			continue
		}
		linkText, isText := item.link.(string)
		if !isText {
			return Extracted{}, false // publicURL(value) on an object throws
		}
		if !wanted[articleKey(linkText, base)] {
			continue
		}
		isBody := item.body != "" || item.bodyObject
		text := item.body
		if item.body == "" {
			if item.content == nil {
				return Extracted{}, false
			}
			text = *item.content
		}
		entryBase := publicURL(linkText, base)
		doc := domFor(text)
		if doc == nil {
			return Extracted{}, false
		}
		removeHidden(doc)
		if isBody {
			return finish(innerHTML(body(doc)), entryBase, "ready", nil, ptr("正文来自订阅源，完整程度以原文为准"), false), true
		}
		return finish(innerHTML(body(doc)), entryBase, "summary", nil, ptr("订阅源仅提供摘要"), false), true
	}
	return Extracted{}, false
}

var doctypeOrEntity = regexp.MustCompile(`(?i)<!DOCTYPE|<!ENTITY`)
