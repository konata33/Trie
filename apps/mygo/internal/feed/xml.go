package feed

import (
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"

	"trie/internal/web"
)

// Parse reads a feed into a DOM as jsdom's text/xml parser does (saxes, XML
// 1.0, namespaces on), refusing any document that is not well-formed.
// encoding/xml tokenizes, and the checks it leaves out are made on top:
// one root, no text outside it, matching tags, bound prefixes, no duplicate
// attributes. innerXML is innerHTML in an XML document (w3c-xmlserializer).

type nodeKind int

const (
	documentNode nodeKind = iota
	elementNode
	textNode
	cdataNode
	commentNode
	piNode
)

type node struct {
	kind     nodeKind
	parent   *node
	children []*node
	// An element's prefix ("" for none), local name and namespace ("" for null).
	prefix, local, space string
	attrs                []attr
	data, target         string
}

type attr struct{ prefix, local, space, value string }

const (
	xmlNS   = "http://www.w3.org/XML/1998/namespace"
	xmlnsNS = "http://www.w3.org/2000/xmlns/"
	htmlNS  = "http://www.w3.org/1999/xhtml"
	mrssNS  = "http://search.yahoo.com/mrss/"
)

var errNotXML = errors.New("这个地址没有返回有效的 RSS 或 Atom 订阅源")

// The XML declaration saxes accepts: version 1.x first, then encoding and standalone, in that order.
var declaration = regexp.MustCompile(`^<\?xml[ \t\r\n]+version[ \t\r\n]*=[ \t\r\n]*(?:"1\.[0-9]+"|'1\.[0-9]+')` +
	`(?:[ \t\r\n]+encoding[ \t\r\n]*=[ \t\r\n]*(?:"[A-Za-z][A-Za-z0-9._-]*"|'[A-Za-z][A-Za-z0-9._-]*'))?` +
	`(?:[ \t\r\n]+standalone[ \t\r\n]*=[ \t\r\n]*(?:"(?:yes|no)"|'(?:yes|no)'))?[ \t\r\n]*\?>`)

func space(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// parseXML returns the document element, the number of elements and the
// most ancestors any element has.
func parseXML(text string) (*node, int, int, error) {
	// saxes skips a byte order mark, and reads a declaration only at the very start.
	text = strings.TrimPrefix(text, "\uFEFF")
	if len(text) > 5 && strings.HasPrefix(text, "<?xml") && (space(text[5]) || text[5] == '?') {
		decl := declaration.FindString(text)
		if decl == "" {
			return nil, 0, 0, errNotXML
		}
		text = text[len(decl):]
	}
	d := xml.NewDecoder(strings.NewReader(text))
	d.Strict = true
	document := &node{kind: documentNode}
	stack := []*node{document}
	scopes := []map[string]string{{"xml": xmlNS, "xmlns": xmlnsNS}}
	var root *node
	count, deepest := 0, 0
	for {
		start := d.InputOffset()
		tok, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, 0, errNotXML
		}
		parent := stack[len(stack)-1]
		outside := parent == document
		switch t := tok.(type) {
		case xml.StartElement:
			if outside && root != nil {
				return nil, 0, 0, errNotXML // documents may contain only one root
			}
			el, scope, ok := element(t, text[start:d.InputOffset()], scopes[len(scopes)-1])
			if !ok {
				return nil, 0, 0, errNotXML
			}
			el.parent = parent
			parent.children = append(parent.children, el)
			if outside {
				root = el
			}
			count++
			deepest = max(deepest, len(stack)-1)
			stack, scopes = append(stack, el), append(scopes, scope)
		case xml.EndElement:
			if outside || t.Name.Space != parent.prefix || t.Name.Local != parent.local {
				return nil, 0, 0, errNotXML
			}
			stack, scopes = stack[:len(stack)-1], scopes[:len(scopes)-1]
		case xml.CharData:
			cdata := strings.HasPrefix(text[start:], "<![CDATA[")
			if outside {
				if cdata || strings.Trim(string(t), " \t\r\n") != "" {
					return nil, 0, 0, errNotXML // text data outside of root node
				}
				continue
			}
			kind := textNode
			if cdata {
				kind = cdataNode
			}
			parent.children = append(parent.children, &node{kind: kind, parent: parent, data: string(t)})
		case xml.Comment:
			if !outside {
				parent.children = append(parent.children, &node{kind: commentNode, parent: parent, data: string(t)})
			}
		case xml.ProcInst:
			if strings.EqualFold(t.Target, "xml") {
				return nil, 0, 0, errNotXML // a declaration away from the start
			}
			if !outside {
				parent.children = append(parent.children, &node{kind: piNode, parent: parent, target: t.Target, data: string(t.Inst)})
			}
		default: // a DOCTYPE (refused before parsing) or another <! declaration
			return nil, 0, 0, errNotXML
		}
	}
	if root == nil || len(stack) != 1 {
		return nil, 0, 0, errNotXML
	}
	return root, count, deepest, nil
}

var lineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ")

// element builds an element from its start tag (raw is the tag as written),
// with namespaces resolved as saxes resolves them.
func element(t xml.StartElement, raw string, outer map[string]string) (*node, map[string]string, bool) {
	el := &node{kind: elementNode, prefix: t.Name.Space, local: t.Name.Local}
	values := make([]string, len(t.Attr))
	again := false
	for i, a := range t.Attr {
		values[i] = a.Value
		again = again || strings.ContainsAny(a.Value, "\t\n")
	}
	if again {
		// Attribute-value normalization: a tab or line break written in a value is a space, one written as a
		// character reference stays. encoding/xml has decoded both alike, so the values are read again.
		for i, v := range quotedValues(raw) {
			values[i] = attribute(lineBreaks.Replace(v[1:len(v)-1]), v[0])
		}
	}
	scope, copied := outer, false
	for i, a := range t.Attr {
		prefix, declares := "", false
		if a.Name.Space == "xmlns" {
			prefix, declares = a.Name.Local, true
		} else if a.Name.Space == "" && a.Name.Local == "xmlns" {
			declares = true
		}
		if !declares {
			continue
		}
		uri := web.JSTrim(values[i])
		if prefix != "" && uri == "" || prefix == "xml" && uri != xmlNS || prefix == "xmlns" || uri == xmlnsNS || uri == xmlNS && prefix != "xml" {
			return nil, nil, false
		}
		if !copied {
			scope, copied = map[string]string{}, true
			for k, v := range outer {
				scope[k] = v
			}
		}
		scope[prefix] = uri
	}
	resolve := func(prefix string) (string, bool) {
		uri, ok := scope[prefix]
		return uri, ok && (prefix == "" || uri != "")
	}
	if strings.Contains(el.local, ":") || el.prefix == "xmlns" {
		return nil, nil, false
	}
	if el.prefix != "" {
		uri, ok := resolve(el.prefix)
		if !ok {
			return nil, nil, false // unbound namespace prefix
		}
		el.space = uri
	} else {
		el.space = scope[""]
	}
	seen := map[string]bool{}
	for i, a := range t.Attr {
		at := attr{prefix: a.Name.Space, local: a.Name.Local, value: values[i]}
		if strings.Contains(at.local, ":") {
			return nil, nil, false
		}
		name := at.local
		if at.prefix == "" {
			if at.local == "xmlns" {
				at.space = xmlnsNS
			}
		} else {
			uri, ok := resolve(at.prefix)
			if !ok {
				return nil, nil, false
			}
			at.space, name = uri, "{"+uri+"}"+at.local
		}
		if seen[name] {
			return nil, nil, false // duplicate attribute
		}
		seen[name] = true
		el.attrs = append(el.attrs, at)
	}
	return el, scope, true
}

// quotedValues are the attribute values of a well-formed start tag as
// written, with their quotes.
func quotedValues(tag string) []string {
	var out []string
	for i := 0; i < len(tag); i++ {
		if tag[i] != '=' {
			continue
		}
		j := i + 1
		for tag[j] != '"' && tag[j] != '\'' {
			j++
		}
		end := j + 1 + strings.IndexByte(tag[j+1:], tag[j])
		out = append(out, tag[j:end+1])
		i = end
	}
	return out
}

// attribute decodes the references in a raw attribute value.
func attribute(raw string, quote byte) string {
	d := xml.NewDecoder(strings.NewReader("<a v=" + string(quote) + raw + string(quote) + "/>"))
	d.Strict = true
	if t, err := d.RawToken(); err == nil {
		return t.(xml.StartElement).Attr[0].Value
	}
	return raw
}

// children are the child elements whose local name is name, ignoring case,
// prefix and namespace; child is the first of them, nil for none.
func children(n *node, name string) []*node {
	var out []*node
	for _, c := range n.children {
		if c.kind == elementNode && strings.ToLower(c.local) == strings.ToLower(name) {
			out = append(out, c)
		}
	}
	return out
}

func child(n *node, name string) *node {
	if list := children(n, name); len(list) > 0 {
		return list[0]
	}
	return nil
}

// get is getAttribute(qualifiedName) in an XML document.
func (n *node) get(name string) (string, bool) {
	if n == nil {
		return "", false
	}
	for _, a := range n.attrs {
		if a.local == name && a.prefix == "" || a.prefix != "" && a.prefix+":"+a.local == name {
			return a.value, true
		}
	}
	return "", false
}

func (n *node) attr(name string) string { v, _ := n.get(name); return v }

// textContent of an element: its text and CDATA, in order; "" for none.
func (n *node) textContent() string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*node)
	walk = func(n *node) {
		for _, c := range n.children {
			switch c.kind {
			case textNode, cdataNode:
				b.WriteString(c.data)
			case elementNode:
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

func (n *node) elements() int {
	count := 0
	for _, c := range n.children {
		if c.kind == elementNode {
			count++
		}
	}
	return count
}

// innerXML is innerHTML of an element in an XML document: each child
// serialized on its own, with no namespace inherited.
func (n *node) innerXML() string {
	var b strings.Builder
	for _, c := range n.children {
		serialize(&b, c, "", map[string][]string{xmlNS: {"xml"}}, new(int))
	}
	return b.String()
}

var (
	textEscape      = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	attributeEscape = strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;", "\t", "&#x9;", "\n", "&#xA;", "\r", "&#xD;")
	void            = map[string]bool{"area": true, "base": true, "basefont": true, "bgsound": true, "br": true, "col": true, "embed": true, "frame": true, "hr": true,
		"img": true, "input": true, "keygen": true, "link": true, "menuitem": true, "meta": true, "param": true, "source": true, "track": true, "wbr": true}
)

// serialize is w3c-xmlserializer's xmlSerialization; namespaces are "" for
// null, and the prefix map is keyed by namespace.
func serialize(b *strings.Builder, n *node, inherited string, prefixes map[string][]string, index *int) {
	switch n.kind {
	case textNode:
		b.WriteString(textEscape.Replace(n.data))
		return
	case cdataNode:
		b.WriteString("<![CDATA[" + n.data + "]]>")
		return
	case commentNode:
		b.WriteString("<!--" + n.data + "-->")
		return
	case piNode:
		b.WriteString("<?" + n.target + " " + n.data + "?>")
		return
	}
	m := map[string][]string{}
	for ns, list := range prefixes {
		m[ns] = append([]string(nil), list...)
	}
	// recordNamespaceInformation
	local := map[string]string{}
	var localDefault *string
	for _, a := range n.attrs {
		if a.space != xmlnsNS {
			continue
		}
		if a.prefix == "" {
			localDefault = &a.value
			continue
		}
		if a.value == xmlNS || contains(m[a.value], a.local) {
			continue
		}
		m[a.value] = append(m[a.value], a.local)
		local[a.local] = a.value
	}
	// localDefaultNamespace !== ns, where an empty default is the empty string, never null.
	defaultDiffers := func(ns string) bool { return localDefault == nil || *localDefault == "" || *localDefault != ns }
	ns := n.space
	ignoreDefault := false
	qualified := n.local
	b.WriteByte('<')
	if inherited == ns {
		ignoreDefault = localDefault != nil
		if ns == xmlNS {
			qualified = "xml:" + n.local
		}
		b.WriteString(qualified)
	} else if candidate, ok := preferred(m, ns, n.prefix); ok {
		qualified = candidate + ":" + n.local
		if localDefault != nil && *localDefault != xmlNS {
			inherited = *localDefault
		}
		b.WriteString(qualified)
	} else if prefix := n.prefix; prefix != "" {
		if _, ok := local[prefix]; ok {
			prefix = generate(m, ns, index)
		}
		m[ns] = append(m[ns], prefix)
		qualified = prefix + ":" + n.local
		b.WriteString(qualified + " xmlns:" + prefix + `="` + attributeEscape.Replace(ns) + `"`)
		if localDefault != nil {
			inherited = *localDefault
		}
	} else if defaultDiffers(ns) {
		ignoreDefault, inherited = true, ns
		b.WriteString(qualified + ` xmlns="` + attributeEscape.Replace(ns) + `"`)
	} else {
		inherited = ns
		b.WriteString(qualified)
	}
	// serializeAttributes
	for _, a := range n.attrs {
		candidate, has := "", false
		if a.space != "" {
			candidate, has = preferred(m, a.space, a.prefix)
			if a.space == xmlnsNS {
				if a.value == xmlNS || a.prefix == "" && ignoreDefault || a.prefix != "" && local[a.local] != a.value && contains(m[a.value], a.local) {
					continue
				}
				if a.prefix == "xmlns" {
					candidate, has = "xmlns", true
				}
			} else if !has {
				candidate, has = generate(m, a.space, index), true
				b.WriteString(" xmlns:" + candidate + `="` + attributeEscape.Replace(a.space) + `"`)
			}
		}
		b.WriteByte(' ')
		if has {
			b.WriteString(candidate + ":")
		}
		b.WriteString(a.local + `="` + attributeEscape.Replace(a.value) + `"`)
	}
	switch {
	case ns == htmlNS && len(n.children) == 0 && void[n.local]:
		b.WriteString(" />")
		return
	case ns != htmlNS && len(n.children) == 0:
		b.WriteString("/>")
		return
	}
	b.WriteByte('>')
	for _, c := range n.children {
		serialize(b, c, inherited, m, index)
	}
	b.WriteString("</" + qualified + ">")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// preferred is preferredPrefixString; false for null.
func preferred(m map[string][]string, ns, prefix string) (string, bool) {
	list, ok := m[ns]
	if !ok || len(list) == 0 {
		return "", false
	}
	if prefix != "" && contains(list, prefix) {
		return prefix, true
	}
	return list[len(list)-1], true
}

// generate is generatePrefix: it replaces the namespace's prefixes.
func generate(m map[string][]string, ns string, index *int) string {
	*index++
	prefix := "ns" + strconv.Itoa(*index)
	m[ns] = []string{prefix}
	return prefix
}
