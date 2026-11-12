package extract

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"trie/internal/library"
	"trie/internal/web"

	"golang.org/x/net/html"
)

// Reader format: cleaned (and cached) article HTML laid out for the reader: stable heading and code ids, code blocks
// with their header, callouts, tables in a scroller, and the structured document. Decided 2026-11-09: no Shiki; a code
// block keeps its language and text, and the reader colours it with MyGo's own highlighting.

// FormatVersion is the reader format's version, stored with each formatted article.
const FormatVersion = 4

// Formatted is an article laid out for the reader.
type Formatted struct {
	HTML            string              `json:"html"`
	FormatVersion   int                 `json:"formatVersion"`
	Headings        []library.Heading   `json:"headings"`
	CodeBlocks      []library.CodeBlock `json:"codeBlocks"`
	HighlightStyles []string            `json:"highlightStyles"`
	Document        json.RawMessage     `json:"document"`
}

const maxHTML = 24 * 1024 * 1024

var (
	errFormatSize      = errors.New("正文超出排版处理限制")
	errFormatStructure = errors.New("文章结构超出排版处理限制")
	cachedPicture      = regexp.MustCompile(`^data:image/(png|jpeg|gif|webp|avif|svg\+xml);base64,([A-Za-z0-9+/]+={0,2})$`)
	imageToken         = regexp.MustCompile(`^reader-image:(\d+)$`)
	tocMarker          = regexp.MustCompile(`(?i)^(?:\[\[toc\]\]|\[toc\])$`)
	calloutMarker      = regexp.MustCompile(`(?i)^` + sp + `*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\](?:[ \t]*\r?\n|[ \t]*$)`)
	calloutTitles      = map[string]string{"NOTE": "Note", "TIP": "Tip", "IMPORTANT": "Important", "WARNING": "Warning", "CAUTION": "Caution"}
	formatterClasses   = []string{"reader-code-block", "reader-code-header", "reader-table-scroll"}
)

// cachedImage is a cached picture as the formatter keeps it: SVG sanitized again, other kinds only if their first bytes
// are what the label says; "" when not kept.
func cachedImage(value string) string {
	m := cachedPicture.FindStringSubmatch(value)
	if m == nil || len(m[2]) > int(math.Ceil(8*1024*1024/3.0))*4 {
		return ""
	}
	kind, encoded := m[1], m[2]
	if kind == "svg+xml" {
		if len(encoded) > 350_000 {
			return ""
		}
		if svg := SanitizeSVG(nodeUTF8(nodeBase64(encoded))); svg != "" {
			return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
		}
		return ""
	}
	prefix := nodeBase64(encoded[:min(64, len(encoded))])
	var valid bool
	switch kind {
	case "png":
		valid = ImageType(prefix) == "image/png"
	case "jpeg":
		valid = ImageType(prefix) == "image/jpeg"
	case "gif":
		valid = ImageType(prefix) == "image/gif"
	case "webp":
		valid = ImageType(prefix) == "image/webp"
	default:
		valid = len(prefix) >= 8 && string(prefix[4:8]) == "ftyp" && (strings.Contains(string(prefix[8:min(24, len(prefix))]), "avif") || strings.Contains(string(prefix[8:min(24, len(prefix))]), "avis"))
	}
	if valid {
		return value
	}
	return ""
}

// cleaner is the reader format's sanitizer: a cached picture becomes a reader-image:N token for images; with sourceOnly (the
// source snapshot) an http(s) picture keeps its address.
func cleaner(images *[]string, sourceOnly bool) *policy {
	imgSchemes := []string{"reader-image"}
	if sourceOnly {
		imgSchemes = []string{"reader-image", "http", "https"}
	}
	return &policy{
		tags: readerTags,
		attributes: map[string][]string{"*": {"id"}, "a": {"href", "title"}, "img": {"src", "alt", "title", "width", "height"}, "code": {"class"}, "pre": {"class"},
			"div": {"class"}, "p": {"class"}, "blockquote": {"class"}, "th": {"colspan", "rowspan", "scope"}, "td": {"colspan", "rowspan"}, "ol": {"start"}},
		classes: map[string][]string{"code": nil, "pre": nil, "div": formatterClasses, "p": {"reader-callout-title"},
			"blockquote": {"reader-callout", "reader-note", "reader-tip", "reader-important", "reader-warning", "reader-caution"}},
		classPattern: map[string]*regexp.Regexp{"code": languageClass, "pre": languageClass},
		schemes:      []string{"http", "https"}, imgSchemes: imgSchemes,
		nonText: set("script style textarea option svg math iframe object embed template noscript form button"),
		transform: map[string]func(attrs) (string, attrs, *string){
			"img": func(a attrs) (string, attrs, *string) {
				if sourceOnly {
					if u, err := web.ParseURL(a.get("src"), nil); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Username == "" && u.Password == "" {
						return "img", attrs{{Key: "src", Val: u.Href()}, {Key: "alt", Val: a.get("alt")}}, nil
					}
				}
				// Keep cached photo bytes out of the HTML parser's attribute buffers. An image without a cached copy keeps no source.
				var out attrs
				if src := cachedImage(a.get("src")); src != "" {
					out = append(out, html.Attribute{Key: "src", Val: "reader-image:" + strconv.Itoa(len(*images))})
					*images = append(*images, src)
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
			"a": func(a attrs) (string, attrs, *string) {
				href := a.get("href")
				if !strings.HasPrefix(href, "#") {
					if u, err := web.ParseURL(href, nil); err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Username != "" || u.Password != "" {
						href = ""
					}
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
		},
	}
}

// hnode is a hast node as hast-util-from-parse5 makes it: properties in attribute order, named and typed by
// property-information (class → className, a list; width, height, colspan, rowspan, start → numbers when they are).
type hnode struct {
	typ      string // root, element or text
	tag      string
	props    []hprop
	children []*hnode
	value    string
}

type hprop struct {
	name  string
	value any // string, float64 or []string
}

func (n *hnode) prop(name string) (any, bool) {
	for _, p := range n.props {
		if p.name == name {
			return p.value, true
		}
	}
	return nil, false
}

func (n *hnode) setProp(name string, value any) {
	for i, p := range n.props {
		if p.name == name {
			n.props[i].value = value
			return
		}
	}
	n.props = append(n.props, hprop{name, value})
}

func (n *hnode) delProp(name string) {
	n.props = slices.DeleteFunc(n.props, func(p hprop) bool { return p.name == name })
}

func (n *hnode) classes() []string {
	v, _ := n.prop("className")
	list, _ := v.([]string)
	return list
}

func (n *hnode) isElement(tag string) bool { return n.typ == "element" && n.tag == tag }

func el(tag string, props []hprop, children ...*hnode) *hnode {
	return &hnode{typ: "element", tag: tag, props: props, children: children}
}

func htext(s string) *hnode { return &hnode{typ: "text", value: s} }

var (
	attributeProperty = map[string]string{"class": "className", "colspan": "colSpan", "rowspan": "rowSpan", "tabindex": "tabIndex"}
	numberProperty    = set("width height colSpan rowSpan start tabIndex")
	propertyAttribute = map[string]string{"className": "class", "colSpan": "colspan", "rowSpan": "rowspan", "tabIndex": "tabindex", "ariaLabel": "aria-label", "dataReaderCode": "data-reader-code"}
)

// parseHast is rehype-parse with fragment: true (parse5 in a template context) through hast-util-from-parse5.
func parseHast(s string) *hnode {
	root := &hnode{typ: "root"}
	var convert func(n *html.Node) *hnode
	convert = func(n *html.Node) *hnode {
		switch n.Type {
		case html.TextNode:
			return htext(n.Data)
		case html.ElementNode:
		default:
			return nil
		}
		e := &hnode{typ: "element", tag: n.Data}
		for _, a := range n.Attr {
			name := qualified(a)
			prop := name
			if p, ok := attributeProperty[name]; ok {
				prop = p
			}
			if _, dup := e.prop(prop); dup {
				continue
			}
			switch {
			case prop == "className":
				e.props = append(e.props, hprop{prop, strings.FieldsFunc(a.Val, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' })})
			case numberProperty[prop] && a.Val != "" && !math.IsNaN(web.JSNumber(a.Val)):
				e.props = append(e.props, hprop{prop, web.JSNumber(a.Val)})
			default:
				e.props = append(e.props, hprop{prop, a.Val})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if k := convert(c); k != nil {
				e.children = append(e.children, k)
			}
		}
		return e
	}
	frag := parseInto(s, "template")
	for c := frag.FirstChild; c != nil; c = c.NextSibling {
		if k := convert(c); k != nil {
			root.children = append(root.children, k)
		}
	}
	return root
}

// plain is a node's text, a <br> a line break.
func plain(n *hnode) string {
	switch {
	case n.typ == "text":
		return n.value
	case n.isElement("br"):
		return "\n"
	}
	var b strings.Builder
	for _, c := range n.children {
		b.WriteString(plain(c))
	}
	return b.String()
}

// formatCodeText is the formatter's code text: line wrappers (div, p) end their line.
func formatCodeText(n *hnode) string {
	if n.isElement("br") {
		return "\n"
	}
	var b strings.Builder
	for i, c := range n.children {
		var v string
		if c.typ == "element" {
			v = formatCodeText(c)
			if (c.tag == "div" || c.tag == "p") && i < len(n.children)-1 && !strings.HasSuffix(v, "\n") {
				v += "\n"
			}
		} else {
			v = plain(c)
		}
		b.WriteString(v)
	}
	return b.String()
}

// validID is a usable element id: 1 to 256 characters, no white space or control characters.
func validID(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && s != "" && web.JSLen(s) <= 256 && strings.IndexFunc(s, func(r rune) bool { return web.JSSpace(r) || r <= 0x1f || r == 0x7f }) < 0
}

type formatter struct {
	images      []string
	usedIDs     map[string]bool
	suffixes    map[string]int
	headings    []library.Heading
	codeBlocks  []library.CodeBlock
	occurrences map[string]int
	codeBytes   int
}

func (f *formatter) allocate(base string) string {
	id := base
	prefix := web.FromUnits(truncateUnits(web.Units(base), 240))
	suffix := f.suffixes[prefix]
	if suffix == 0 {
		suffix = 1
	}
	for f.usedIDs[id] {
		id = prefix + "-" + strconv.Itoa(suffix)
		suffix++
	}
	f.suffixes[prefix] = suffix
	f.usedIDs[id] = true
	return id
}

func (f *formatter) transform(parent *hnode, inCode, inQuote bool) {
	for index := 0; index < len(parent.children); index++ {
		node := parent.children[index]
		if node.typ != "element" {
			continue
		}
		if node.tag == "img" {
			src, _ := node.prop("src")
			if m := imageToken.FindStringSubmatch(jsString(src)); m != nil {
				if i, err := strconv.Atoi(m[1]); err == nil && i < len(f.images) && f.images[i] != "" {
					node.setProp("src", f.images[i])
				} else {
					node.delProp("src")
				}
			} else {
				node.delProp("src")
			}
		}
		if node.tag == "div" && slices.Contains(node.classes(), "reader-code-block") {
			for _, c := range node.children {
				if c.isElement("pre") {
					parent.children[index], node = c, c
					break
				}
			}
		}
		if node.tag == "p" && !inCode && !inQuote && !slices.ContainsFunc(node.children, func(c *hnode) bool { return c.typ != "text" }) && tocMarker.MatchString(web.JSTrim(plain(node))) {
			parent.children = slices.Delete(parent.children, index, index+1)
			index--
			continue
		}
		if node.tag == "pre" && len(f.codeBlocks) < 128 {
			codeNode := node
			for _, c := range node.children {
				if c.isElement("code") {
					codeNode = c
					break
				}
			}
			code := formatCodeText(codeNode)
			if f.codeBytes+len(code) > 2*1024*1024 {
				continue
			}
			f.codeBytes += len(code)
			requested := "text"
			for _, name := range append(slices.Clone(codeNode.classes()), node.classes()...) {
				if strings.HasPrefix(name, "language-") {
					if requested = strings.ToLower(name[9:]); requested == "" {
						requested = "text"
					}
					break
				}
			}
			language := "text"
			if shikiLanguages[requested] {
				language = requested
			}
			var id string
			if v, ok := codeNode.prop("id"); ok && codeNode != node {
				id = jsString(v)
			} else {
				id = f.allocate("reader-code-" + strconv.Itoa(len(f.codeBlocks)+1))
			}
			f.codeBlocks = append(f.codeBlocks, library.CodeBlock{ID: id, Language: language, Code: code})
			// No highlighting here: the reader colours the code (MyGo's tokenizers stand in for Shiki).
			rendered := el("code", []hprop{{"id", id}, {"className", []string{"language-" + language}}}, htext(code))
			var preProps []hprop
			if v, ok := node.prop("id"); ok && jsTruthy(v) && jsString(v) != id {
				preProps = append(preProps, hprop{"id", v})
			}
			label := language
			if language == "text" {
				label = "纯文本"
			}
			parent.children[index] = el("div", []hprop{{"className", []string{"reader-code-block"}}},
				el("div", []hprop{{"className", []string{"reader-code-header"}}},
					el("span", []hprop{{"className", []string{"reader-code-language"}}}, htext(label)),
					el("button", []hprop{{"type", "button"}, {"className", []string{"reader-copy"}}, {"dataReaderCode", id}, {"ariaLabel", "复制代码"}}, htext("复制"))),
				el("pre", append(preProps, hprop{"tabIndex", float64(0)}), rendered))
			continue
		}
		if node.tag == "blockquote" {
			var first *hnode
			for _, c := range node.children {
				if c.typ == "element" {
					first = c
					break
				}
			}
			if first != nil && first.tag == "p" && len(first.children) > 0 && first.children[0].typ == "text" {
				if m := calloutMarker.FindStringSubmatch(first.children[0].value); m != nil {
					kind := strings.ToUpper(m[1])
					first.children[0].value = first.children[0].value[len(m[0]):]
					if first.children[0].value == "" {
						first.children = first.children[1:]
					}
					if len(first.children) > 0 && first.children[0].isElement("br") {
						first.children = first.children[1:]
					}
					if len(first.children) == 0 {
						node.children = slices.DeleteFunc(node.children, func(c *hnode) bool { return c == first })
					}
					node.setProp("className", []string{"reader-callout", "reader-" + strings.ToLower(kind)})
					node.children = append([]*hnode{el("p", []hprop{{"className", []string{"reader-callout-title"}}}, htext(calloutTitles[kind]))}, node.children...)
				}
			}
		}
		if headingTag.MatchString(node.tag) && !inCode && len(f.headings) < 512 {
			if title := web.FromUnits(truncateUnits(web.Units(web.JSTrim(plain(node))), 512)); title != "" {
				f.occurrences[title]++
				if v, ok := node.prop("id"); !ok || !jsTruthy(v) {
					sum := sha256.Sum256([]byte(title + "\x00" + strconv.Itoa(f.occurrences[title])))
					node.setProp("id", f.allocate("reader-heading-"+hex.EncodeToString(sum[:])[:12]))
				}
				v, _ := node.prop("id")
				f.headings = append(f.headings, library.Heading{ID: jsString(v), Level: int(node.tag[1] - '0'), Text: title})
			}
		}
		f.transform(node, inCode || node.tag == "code" || node.tag == "pre", inQuote || node.tag == "blockquote")
		scroll := []hprop{{"tabIndex", float64(0)}, {"role", "region"}, {"ariaLabel", "表格，可横向滚动"}}
		if node.tag == "div" && slices.Contains(node.classes(), "reader-table-scroll") {
			for _, p := range scroll {
				node.setProp(p.name, p.value)
			}
		}
		if node.tag == "table" && !(parent.typ == "element" && slices.Contains(parent.classes(), "reader-table-scroll")) {
			parent.children[index] = el("div", append([]hprop{{"className", []string{"reader-table-scroll"}}}, scroll...), node)
		}
	}
}

// jsString is String(value) for a hast property value.
func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return "undefined"
	case string:
		return x
	case float64:
		return jsNumber(x)
	case []string:
		return strings.Join(x, ",")
	}
	return ""
}

func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	}
	return true
}

// jsNumber is Number.prototype.toString().
func jsNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	e := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±xx
	mantissa, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	k, n := len(digits), x+1
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	exponent := "e+" + strconv.Itoa(n-1)
	if n-1 < 0 {
		exponent = "e-" + strconv.Itoa(1-n)
	}
	if k == 1 {
		return sign + digits + exponent
	}
	return sign + digits[:1] + "." + digits[1:] + exponent
}

// FormatArticleHTML lays an article out for the reader: html is the article's cleaned (and cached) HTML; snapshot,
// when given, is the HTML its source revision follows.
func FormatArticleHTML(page string, snapshot *library.ReaderSourceSnapshot) (Formatted, error) {
	if len(page) > maxHTML || strings.Count(page, "<") > 50_000 {
		return Formatted{}, errFormatSize
	}
	f := &formatter{usedIDs: map[string]bool{}, suffixes: map[string]int{}, occurrences: map[string]int{}, headings: []library.Heading{}, codeBlocks: []library.CodeBlock{}}
	root := parseHast(cleaner(&f.images, false).run(page))
	type frame struct {
		n     *hnode
		depth int
	}
	stack := []frame{{root, 0}}
	var elements []*hnode
	count := 0
	for len(stack) > 0 {
		fr := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if count++; count > 50_000 || fr.depth > 128 {
			return Formatted{}, errFormatStructure
		}
		if fr.n.typ == "element" {
			elements = append(elements, fr.n)
			if v, ok := fr.n.prop("id"); ok {
				if id, ok := validID(v); ok {
					f.usedIDs[id] = true
				}
			}
		}
		// Preserve first-occurrence IDs in document order.
		for i := len(fr.n.children) - 1; i >= 0; i-- {
			if fr.n.children[i].typ == "element" {
				stack = append(stack, frame{fr.n.children[i], fr.depth + 1})
			}
		}
	}
	// Reserve all original anchors before assigning replacements to duplicate IDs.
	seen := map[string]bool{}
	for _, n := range elements {
		v, ok := n.prop("id")
		id, valid := validID(v)
		switch {
		case ok && !valid:
			n.delProp("id")
		case !ok:
		case seen[id]:
			n.setProp("id", f.allocate(id))
		default:
			seen[id] = true
		}
	}
	f.transform(root, false, false)
	formatted := toHTML(root)
	if len(formatted) > maxHTML {
		return Formatted{}, errors.New("排版结果超出大小限制")
	}
	provenance := "legacy-cache"
	var sourceRoot *hnode
	if snapshot != nil {
		provenance = snapshot.Provenance
		if snapshot.Provenance == "source" {
			if len(snapshot.HTML) > maxHTML || strings.Count(snapshot.HTML, "<") > 50_000 {
				return Formatted{}, errors.New("正文来源超出排版处理限制")
			}
			sourceRoot = parseHast(cleaner(&[]string{}, true).run(snapshot.HTML))
		}
	}
	document, err := createReaderDocument(root, f.codeBlocks, provenance, sourceRoot)
	if err != nil {
		return Formatted{}, err
	}
	return Formatted{HTML: formatted, FormatVersion: FormatVersion, Headings: f.headings, CodeBlocks: f.codeBlocks, HighlightStyles: []string{}, Document: document}, nil
}

var (
	textReferences      = strings.NewReplacer("<", "&#x3C;", "&", "&#x26;")
	attributeReferences = strings.NewReplacer("\x00", "&#x0;", `"`, "&#x22;", "&", "&#x26;", "'", "&#x27;", "`", "&#x60;")
	hastVoids           = set("area base basefont bgsound br col command embed frame hr image img input keygen link meta param source track wbr")
	rawTextParents      = set("script style")
)

// toHTML is hast-util-to-html with its defaults: double quotes, numeric character references, void elements without a
// slash, every closing tag written.
func toHTML(root *hnode) string {
	var b strings.Builder
	var write func(n, parent *hnode)
	write = func(n, parent *hnode) {
		switch n.typ {
		case "text":
			if parent != nil && parent.typ == "element" && rawTextParents[parent.tag] {
				b.WriteString(n.value)
			} else {
				b.WriteString(textReferences.Replace(n.value))
			}
		case "element":
			b.WriteString("<" + n.tag)
			for _, p := range n.props {
				name := p.name
				if a, ok := propertyAttribute[name]; ok {
					name = a
				}
				var value string
				switch v := p.value.(type) {
				case []string:
					value = strings.Join(v, " ")
				case float64:
					if math.IsNaN(v) {
						continue
					}
					value = jsNumber(v)
				case string:
					value = v
				default:
					continue
				}
				b.WriteString(" " + name + `="` + attributeReferences.Replace(value) + `"`)
			}
			b.WriteString(">")
			for _, c := range n.children {
				write(c, n)
			}
			if !hastVoids[n.tag] || len(n.children) > 0 {
				b.WriteString("</" + n.tag + ">")
			}
		case "root":
			for _, c := range n.children {
				write(c, n)
			}
		}
	}
	write(root, nil)
	return b.String()
}
