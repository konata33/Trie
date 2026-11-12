package extract

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"

	"trie/internal/web"
)

// SVG pictures: a picture's SVG reduced to static geometry, local references and text. The source is parsed as XML the
// way jsdom's parser (saxes) takes it, namespaces bound or refused, and the copy written as XMLSerializer writes it.

const (
	svgNS   = "http://www.w3.org/2000/svg"
	xlinkNS = "http://www.w3.org/1999/xlink"
	xmlNS   = "http://www.w3.org/XML/1998/namespace"
	xmlnsNS = "http://www.w3.org/2000/xmlns/"
)

var (
	reusableGeometry = set("path rect circle ellipse line polyline polygon")
	svgElements      = set("svg g path rect circle ellipse line polyline polygon text tspan defs linearGradient radialGradient stop clipPath mask pattern use title desc")
	svgAttributes    = set(`id viewBox preserveAspectRatio x y x1 y1 x2 y2 dx dy width height rx ry r cx cy d points pathLength transform
		fill fill-opacity fill-rule stroke stroke-width stroke-opacity stroke-linecap stroke-linejoin stroke-miterlimit stroke-dasharray stroke-dashoffset opacity color
		clip-path clip-rule mask filter vector-effect paint-order display visibility shape-rendering text-rendering
		font-family font-size font-weight font-style font-stretch text-anchor dominant-baseline alignment-baseline letter-spacing word-spacing textLength lengthAdjust rotate
		gradientUnits gradientTransform spreadMethod offset stop-color stop-opacity fx fy fr clipPathUnits maskUnits maskContentUnits patternUnits patternContentUnits patternTransform`)
	fragmentRef  = regexp.MustCompile(`^#[A-Za-z_][A-Za-z0-9_.:-]*$`)
	localPaint   = regexp.MustCompile(`^url\(` + sp + `*(?:'#[A-Za-z_][A-Za-z0-9_.:-]*'|"#[A-Za-z_][A-Za-z0-9_.:-]*"|#[A-Za-z_][A-Za-z0-9_.:-]*)` + sp + `*\)$`)
	colorValue   = regexp.MustCompile(`(?i)^(?:[A-Za-z]+|#[\da-fA-F]{3,4}|#[\da-fA-F]{6}|#[\da-fA-F]{8}|(?:rgb|rgba|hsl|hsla)\([\d\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}.,%+\-/]+\))$`)
	unsafeChars  = regexp.MustCompile(`[\\\x00-\x1f\x7f]`)
	urlOrTag     = regexp.MustCompile(`(?i)url` + sp + `*\(|[<>]`)
	declaration_ = regexp.MustCompile(`^` + sp + `*(--[A-Za-z0-9_-]+)` + sp + `*:` + sp + `*([^\n\r\x{2028}\x{2029}]+?)` + sp + `*$`)
	varCall      = regexp.MustCompile(`^var\(` + sp + `*(--[A-Za-z0-9_-]+)` + sp + `*(?:,` + sp + `*([^\n\r\x{2028}\x{2029}]+))?\)$`)
	styleDecl    = regexp.MustCompile(`(?i)^` + sp + `*([a-z-]+)` + sp + `*:` + sp + `*([^\n\r\x{2028}\x{2029}]+?)` + sp + `*$`)
	fontShort    = regexp.MustCompile(`^(?:(?:normal|italic|bold|\d{3})` + sp + `+)*(\d+(?:\.\d+)?px)` + sp + `+([^\n\r\x{2028}\x{2029}]+)$`)
	xmlDecl      = regexp.MustCompile(`^\x{feff}?<\?xml` + sp + `[^?]*\?>`)
	refused      = regexp.MustCompile(`(?i)<!DOCTYPE|<!ENTITY|<\?`)
)

type xmlAttr struct {
	space, prefix, local, value string
}

func (a xmlAttr) name() string {
	if a.prefix != "" {
		return a.prefix + ":" + a.local
	}
	return a.local
}

type xmlNode struct {
	kind     byte // 'e' element, 't' text or CDATA, 'c' comment
	space    string
	prefix   string // as written
	local    string
	attrs    []xmlAttr
	children []*xmlNode
	text     string
}

func (n *xmlNode) get(name string) (string, bool) {
	for _, a := range n.attrs {
		if a.name() == name {
			return a.value, true
		}
	}
	return "", false
}

func (n *xmlNode) getNS(space, local string) (string, bool) {
	for _, a := range n.attrs {
		if a.space == space && a.local == local {
			return a.value, true
		}
	}
	return "", false
}

var errXML = errors.New("invalid XML")

// parseXML reads a well-formed namespaced XML document as saxes does: one root, prefixes bound, attributes unique and
// their white space normalized.
func parseXML(source string) (*xmlNode, error) {
	d := xml.NewDecoder(strings.NewReader(strings.TrimPrefix(source, "\ufeff")))
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	type scope struct {
		node   *xmlNode
		raw    xml.Name
		prefix map[string]string
	}
	doc := &xmlNode{kind: 'd'}
	stack := []scope{{node: doc, prefix: map[string]string{"xml": xmlNS}}}
	var root *xmlNode
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) == 1 && root != nil {
				return nil, errXML
			}
			bindings := map[string]string{}
			for k, v := range top.prefix {
				bindings[k] = v
			}
			for _, a := range t.Attr {
				switch {
				case a.Name.Space == "" && a.Name.Local == "xmlns":
					bindings[""] = a.Value
				case a.Name.Space == "xmlns":
					if a.Value == "" || a.Name.Local == "xmlns" {
						return nil, errXML
					}
					bindings[a.Name.Local] = a.Value
				}
			}
			n := &xmlNode{kind: 'e', prefix: t.Name.Space, local: t.Name.Local}
			ns, ok := bindings[t.Name.Space]
			if !ok && t.Name.Space != "" {
				return nil, errXML // unbound prefix
			}
			n.space = ns
			seen := map[string]bool{}
			for _, a := range t.Attr {
				x := xmlAttr{prefix: a.Name.Space, local: a.Name.Local, value: normalizeAttr(a.Value)}
				switch {
				case a.Name.Space == "" && a.Name.Local == "xmlns", a.Name.Space == "xmlns":
					x.space = xmlnsNS
				case a.Name.Space != "":
					if x.space, ok = bindings[a.Name.Space]; !ok {
						return nil, errXML
					}
				}
				if key := x.space + " " + x.local; seen[x.name()] || seen[key] {
					return nil, errXML
				} else {
					seen[x.name()], seen[key] = true, true
				}
				n.attrs = append(n.attrs, x)
			}
			top.node.children = append(top.node.children, n)
			if len(stack) == 1 {
				root = n
			}
			stack = append(stack, scope{node: n, raw: t.Name, prefix: bindings})
		case xml.EndElement:
			if len(stack) == 1 || t.Name != top.raw {
				return nil, errXML
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 1 {
				if strings.Trim(string(t), " \t\r\n") != "" {
					return nil, errXML
				}
				continue
			}
			top.node.children = append(top.node.children, &xmlNode{kind: 't', text: string(t)})
		case xml.Comment:
			top.node.children = append(top.node.children, &xmlNode{kind: 'c', text: string(t)})
		case xml.Directive:
			return nil, errXML
		}
	}
	if len(stack) != 1 || root == nil {
		return nil, errXML
	}
	return root, nil
}

// normalizeAttr is XML attribute-value normalization for a parser without a DTD: tab, line feed and carriage return
// become spaces.
func normalizeAttr(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
}

// SanitizeSVG returns the SVG rebuilt from the elements and attributes it allows, or "" when the
// source is refused.
func SanitizeSVG(source string) (out string) {
	if source == "" || len(source) > 256*1024 || refused.MatchString(xmlDecl.ReplaceAllString(source, "")) {
		return ""
	}
	root, err := parseXML(source)
	if err != nil || root.local != "svg" || root.space != svgNS {
		return ""
	}
	type item struct {
		n     *xmlNode
		depth int
	}
	pending := []item{{root, 1}}
	count := 0
	targets := map[string]*xmlNode{}
	duplicate := map[string]bool{}
	for len(pending) > 0 {
		it := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if count++; count > 5000 || it.depth > 64 {
			return ""
		}
		if id, _ := it.n.get("id"); id != "" {
			if _, seen := targets[id]; seen {
				duplicate[id] = true
			}
			targets[id] = it.n
		}
		for _, c := range it.n.children {
			if c.kind == 'e' {
				pending = append(pending, item{c, it.depth + 1})
			}
		}
	}
	table := customProperties(root)
	var copyNode func(*xmlNode) *xmlNode
	copyNode = func(n *xmlNode) *xmlNode {
		if n.space != svgNS || !svgElements[n.local] {
			return nil
		}
		href, ok := n.get("href")
		if !ok {
			href, _ = n.getNS(xlinkNS, "href")
		}
		href = web.JSTrim(href)
		if n.local == "use" {
			var target *xmlNode
			if fragmentRef.MatchString(href) && !duplicate[href[1:]] {
				target = targets[href[1:]]
			}
			if target == nil || target.space != svgNS || !reusableGeometry[target.local] || len(target.children) > 0 {
				return nil
			}
		}
		clean := &xmlNode{kind: 'e', space: svgNS, local: n.local}
		if fragmentRef.MatchString(href) {
			clean.attrs = append(clean.attrs, xmlAttr{local: "href", value: href})
		}
		for _, p := range presentation(n, table) {
			name, value := p[0], p[1]
			check := web.JSTrim(value)
			if name == "id" {
				check = value
			}
			if name == "href" || !svgAttributes[name] || !safeAttribute(name, check) {
				continue
			}
			setXMLAttr(clean, name, web.JSTrim(value))
		}
		for _, c := range n.children {
			switch c.kind {
			case 'e':
				if copied := copyNode(c); copied != nil {
					clean.children = append(clean.children, copied)
				}
			case 't':
				clean.children = append(clean.children, &xmlNode{kind: 't', text: c.text})
			}
		}
		return clean
	}
	var b strings.Builder
	serializeXML(&b, copyNode(root), "")
	if b.Len() > 256*1024 {
		return ""
	}
	return b.String()
}

func setXMLAttr(n *xmlNode, name, value string) {
	for i, a := range n.attrs {
		if a.name() == name {
			n.attrs[i].value = value
			return
		}
	}
	n.attrs = append(n.attrs, xmlAttr{local: name, value: value})
}

// customProperties is one document-wide table of the custom properties set in style attributes; the first definition of
// a name wins. ponytail: not CSS inheritance; enough for generated diagrams (Mermaid's).
func customProperties(root *xmlNode) map[string]string {
	table := map[string]string{}
	var walk func(*xmlNode)
	walk = func(n *xmlNode) {
		if n.kind != 'e' {
			return
		}
		if style, ok := n.getNS("", "style"); ok {
			for _, d := range strings.Split(style, ";") {
				if m := declaration_.FindStringSubmatch(d); m != nil {
					if _, seen := table[m[1]]; !seen {
						table[m[1]] = m[2]
					}
				}
			}
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(root)
	return table
}

func resolveVar(value string, table map[string]string, depth int) string {
	m := varCall.FindStringSubmatch(web.JSTrim(value))
	if m != nil {
		next, ok := table[m[1]]
		if !ok {
			next = m[2]
		}
		if next != "" && depth < 8 {
			return resolveVar(next, table, depth+1)
		}
	}
	return web.JSTrim(value)
}

// presentation is an element's own attributes, then the declarations of its style attribute for attributes this
// sanitizer knows (style wins, as in CSS), each var() resolved.
func presentation(n *xmlNode, table map[string]string) [][2]string {
	var out [][2]string
	put := func(name, value string) {
		for i := range out {
			if out[i][0] == name {
				out[i][1] = value
				return
			}
		}
		out = append(out, [2]string{name, value})
	}
	for _, a := range n.attrs {
		if a.name() != "style" && a.space == "" {
			put(a.name(), a.value)
		}
	}
	style, _ := n.get("style")
	for _, d := range strings.Split(style, ";") {
		m := styleDecl.FindStringSubmatch(d)
		if m == nil {
			continue
		}
		if f := fontShort.FindStringSubmatch(m[2]); m[1] == "font" && f != nil {
			put("font-size", f[1])
			put("font-family", f[2])
		} else if svgAttributes[m[1]] && m[1] != "id" {
			put(m[1], m[2])
		}
	}
	for i := range out {
		if out[i][0] != "id" {
			out[i][1] = resolveVar(out[i][1], table, 0)
		}
	}
	return out
}

func safeAttribute(name, value string) bool {
	if value == "" || unsafeChars.MatchString(value) {
		return false
	}
	switch name {
	case "id":
		return fragmentRef.MatchString("#" + value)
	case "href":
		return fragmentRef.MatchString(value)
	case "fill", "stroke":
		return localPaint.MatchString(value) || colorValue.MatchString(value)
	case "clip-path", "mask", "filter":
		return value == "none" || localPaint.MatchString(value)
	case "color", "stop-color":
		return colorValue.MatchString(value)
	}
	return !urlOrTag.MatchString(value)
}

var (
	xmlText      = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	xmlAttrValue = strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;", "\t", "&#x9;", "\n", "&#xA;", "\r", "&#xD;")
)

// serializeXML writes SVG-namespace elements without prefixes as XMLSerializer does: the namespace declared on the
// root, empty elements closed with "/>".
func serializeXML(b *strings.Builder, n *xmlNode, inherited string) {
	if n.kind == 't' {
		b.WriteString(xmlText.Replace(n.text))
		return
	}
	b.WriteString("<" + n.local)
	if n.space != inherited {
		b.WriteString(` xmlns="` + xmlAttrValue.Replace(n.space) + `"`)
	}
	for _, a := range n.attrs {
		b.WriteString(" " + a.local + `="` + xmlAttrValue.Replace(a.value) + `"`)
	}
	if len(n.children) == 0 {
		b.WriteString("/>")
		return
	}
	b.WriteString(">")
	for _, c := range n.children {
		serializeXML(b, c, n.space)
	}
	b.WriteString("</" + n.local + ">")
}

// ImageType is what a picture really is from its first bytes, or "" for a kind the reader
// does not keep.
func ImageType(body []byte) string {
	switch {
	case bytes.HasPrefix(body, []byte{137, 80, 78, 71, 13, 10, 26, 10}):
		return "image/png"
	case len(body) >= 3 && body[0] == 255 && body[1] == 216 && body[2] == 255:
		return "image/jpeg"
	case len(body) >= 6 && (string(body[:6]) == "GIF87a" || string(body[:6]) == "GIF89a"):
		return "image/gif"
	case len(body) >= 12 && string(body[:4]) == "RIFF" && string(body[8:12]) == "WEBP":
		return "image/webp"
	case len(body) >= 8 && string(body[4:8]) == "ftyp" && (bytes.Contains(body[8:min(24, len(body))], []byte("avif")) || bytes.Contains(body[8:min(24, len(body))], []byte("avis"))):
		return "image/avif"
	}
	return ""
}
