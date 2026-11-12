package extract

import (
	"encoding/json"
	"net/mail"
	"regexp"
	"strings"

	"trie/internal/web"
)

// extractFeed's reading of a feed: the XML checked as jsdom checks it (well-formed, at most 20,000 elements nested at
// most 80 deep), turned into the objects xml2js 0.5 makes (every child an array, text under "_", attributes under "$",
// whitespace-only text dropped) and read as rss-parser 3.13 reads items, including where it throws.

// feedItem is what extractFeed reads of an item: its link, the body (content:encoded, or Atom content as text) and
// the summary rss-parser calls content.
type feedItem struct {
	link       any // string or, for a link element holding markup, an object (which fails the feed's read)
	body       string
	bodyObject bool // a body element holding markup: truthy without being text
	content    *string
}

type feedError string

func (e feedError) Error() string { return string(e) }

// xml2js turns an element into xml2js's value: a string for text alone, else an object.
func xml2js(n *xmlNode) any {
	obj := &jsObject{values: map[string]any{}}
	var text strings.Builder
	var kids []*xmlNode
	for _, c := range n.children {
		switch c.kind {
		case 't':
			text.WriteString(c.text)
		case 'e':
			kids = append(kids, c)
		}
	}
	s := text.String()
	whitespace := strings.TrimFunc(s, web.JSSpace) == ""
	if !whitespace {
		obj.keys, obj.values["_"] = append(obj.keys, "_"), s
	}
	if len(n.attrs) > 0 {
		attrs := &jsObject{values: map[string]any{}}
		for _, a := range n.attrs {
			attrs.keys, attrs.values[a.name()] = append(attrs.keys, a.name()), a.value
		}
		obj.keys, obj.values["$"] = append(obj.keys, "$"), attrs
	}
	for _, c := range kids {
		name := c.local
		if c.prefix != "" {
			name = c.prefix + ":" + c.local
		}
		v := xml2js(c)
		if list, ok := obj.values[name].([]any); ok {
			obj.values[name] = append(list, v)
		} else {
			obj.keys, obj.values[name] = append(obj.keys, name), []any{v}
		}
	}
	switch {
	case len(obj.keys) == 1 && obj.keys[0] == "_":
		return s
	case len(obj.keys) == 0:
		return s // whitespace or nothing: xml2js's emptyTag is the text itself
	}
	return obj
}

func field(v any, key string) any {
	if o, ok := v.(*jsObject); ok {
		return o.get(key)
	}
	return nil
}

func first(v any) any {
	if list, ok := v.([]any); ok && len(list) > 0 {
		return list[0]
	}
	return nil
}

// fromXML is copyFromXML for one field: the first value, as text when it is an object holding only text "_".
func fromXML(v any, key string) any {
	x := first(field(v, key))
	if s, ok := field(x, "_").(string); ok {
		return s
	}
	return x
}

// truthyJS is JavaScript truthiness for a parseJS value: null, false, "" and 0 are false.
// (Real Excalidraw scenes mark every element "isDeleted": false; reading false as true drew no whiteboard.)
func truthyJS(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, _ := x.Float64()
		return f != 0
	}
	return true
}

// getContent is rss-parser's getContent: text, or an element holding markup rebuilt by xml2js's Builder in a <div>.
func getContent(v any) string {
	if s, ok := field(v, "_").(string); ok {
		return s
	}
	if o, ok := v.(*jsObject); ok {
		var b strings.Builder
		buildXML(&b, "div", o)
		return b.String()
	}
	s, _ := v.(string)
	return s
}

var (
	builderText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#xD;")
	builderAttr = strings.NewReplacer("&", "&amp;", "<", "&lt;", `"`, "&quot;", "\t", "&#x9;", "\n", "&#xA;", "\r", "&#xD;")
)

// buildXML is xml2js's Builder (headless, not pretty) for one element: attributes, then text and children in key order.
func buildXML(b *strings.Builder, name string, v any) {
	b.WriteString("<" + name)
	var inner strings.Builder
	if o, ok := v.(*jsObject); ok {
		for _, key := range o.entries() {
			switch child := o.get(key); {
			case key == "$":
				if attrs, ok := child.(*jsObject); ok {
					for _, a := range attrs.entries() {
						s, _ := attrs.get(a).(string)
						b.WriteString(" " + a + `="` + builderAttr.Replace(s) + `"`)
					}
				}
			case key == "_":
				s, _ := child.(string)
				inner.WriteString(builderText.Replace(s))
			default:
				list, _ := child.([]any)
				for _, entry := range list {
					buildXML(&inner, key, entry)
				}
			}
		}
	} else if s, ok := v.(string); ok {
		inner.WriteString(builderText.Replace(s))
	}
	if inner.Len() == 0 {
		b.WriteString("/>")
		return
	}
	b.WriteString(">" + inner.String() + "</" + name + ">")
}

// link is rss-parser's getLink(links, 'alternate', 0): it throws on a link without attributes it has to look at.
func link(links any) (any, error) {
	list, _ := links.([]any)
	for _, l := range list {
		attrs, ok := field(l, "$").(*jsObject)
		if !ok {
			return nil, feedError("Cannot read properties of undefined (reading 'rel')")
		}
		if attrs.get("rel") == "alternate" {
			return attrs.get("href"), nil
		}
	}
	if len(list) > 0 && truthyJS(list[0]) {
		attrs, ok := field(list[0], "$").(*jsObject)
		if !ok {
			return nil, feedError("Cannot read properties of undefined (reading 'href')")
		}
		return attrs.get("href"), nil
	}
	return nil, nil
}

// validDate is whether new Date(text).toISOString() does not throw. ponytail: V8's lenient fallback parser is stood in
// for by RFC 5322 dates; other text it would read is refused here.
func validDate(text string) bool {
	if _, ok := web.JSDateParse(text); ok {
		return true
	}
	_, err := mail.ParseDate(text)
	return err == nil
}

var rss2 = regexp.MustCompile(`^2`)
var rss09 = regexp.MustCompile(`0\.9`)

func feedItems(xmlText string) ([]feedItem, error) {
	root, err := parseXML(xmlText)
	if err != nil {
		return nil, err
	}
	count := 0
	var walk func(n *xmlNode, depth int) bool
	walk = func(n *xmlNode, depth int) bool {
		if count++; count > maxElements || depth > 80 {
			return false
		}
		for _, c := range n.children {
			if c.kind == 'e' && !walk(c, depth+1) {
				return false
			}
		}
		return true
	}
	if !walk(root, 0) {
		return nil, feedError("too large")
	}
	name := root.local
	if root.prefix != "" {
		name = root.prefix + ":" + root.local
	}
	top := xml2js(root)
	version, _ := field(field(top, "$"), "version").(string)
	var items []feedItem
	readItem := func(x any, atom bool) error {
		item := feedItem{body: "", content: nil}
		encoded := fromXML(x, "content:encoded")
		atomBody := fromXML(x, "content")
		if atom {
			if links := field(x, "link"); links != nil {
				if list, _ := links.([]any); len(list) > 0 {
					if item.link, err = link(links); err != nil {
						return err
					}
				}
			}
			for _, key := range []string{"published", "updated"} {
				if s, ok := first(field(x, key)).(string); ok && s != "" {
					if !validDate(s) {
						return feedError("Invalid time value")
					}
					break
				}
			}
			if c := first(field(x, "content")); c != nil {
				item.content = ptr(getContent(c))
			}
		} else {
			item.link = fromXML(x, "link")
			if d := field(x, "description"); d != nil {
				item.content = ptr(getContent(first(d)))
			}
		}
		switch {
		case truthyJS(encoded):
			if s, ok := encoded.(string); ok {
				item.body = s
			} else {
				item.bodyObject = true
			}
		default:
			if s, ok := atomBody.(string); ok {
				item.body = s
			}
		}
		items = append(items, item)
		return nil
	}
	switch {
	case name == "feed" && truthyJS(top):
		if links := field(top, "link"); links != nil {
			if _, err := link(links); err != nil {
				return nil, err
			}
		}
		for _, e := range asList(field(top, "entry")) {
			if err := readItem(e, true); err != nil {
				return nil, err
			}
		}
	case name == "rss" && rss2.MatchString(version), name == "rdf:RDF", name == "rss" && rss09.MatchString(version):
		channel := field(top, "channel")
		if name == "rdf:RDF" {
			if first(channel) == nil {
				return nil, feedError("Cannot read properties of undefined (reading '0')")
			}
			channel = top
		} else {
			if channel == nil {
				return nil, feedError("Cannot read properties of undefined (reading '0')")
			}
			channel = first(channel)
		}
		for _, x := range asList(field(channel, "item")) {
			if err := readItem(x, false); err != nil {
				return nil, err
			}
		}
	default:
		return nil, feedError("Feed not recognized as RSS 1 or 2.")
	}
	return items, nil
}

func asList(v any) []any { l, _ := v.([]any); return l }
