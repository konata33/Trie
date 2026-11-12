package extract

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"trie/internal/library"
	"trie/internal/web"
)

// Reader document: the formatted body as the reader's structured document (a hast tree of the reader's tags whose
// blocks carry unit IDs), with digests over exactly what JSON.stringify writes, so unit IDs and revisions agree with the
// documents in libraries written by earlier versions. Stored documents are checked by internal/library when saved
// (IsReaderDocument).

var (
	errUnits         = errors.New("阅读单元超出处理限制")
	errImages        = errors.New("阅读图片超出处理限制")
	errCode          = errors.New("阅读代码超出处理限制")
	languageName     = regexp.MustCompile(`(?i)^language-[a-z0-9_-]{1,64}$`)
	languageSemantic = regexp.MustCompile(`(?i)^language-[a-z0-9_-]+$`)
	calloutClass     = regexp.MustCompile(`^reader-(note|tip|important|warning|caution)$`)
	httpAddress      = regexp.MustCompile(`^https?://`)
	documentBlocks   = set("p h1 h2 h3 h4 h5 h6 ul ol li blockquote pre table figure figcaption caption hr div")
	directSkipped    = set("ul ol pre table img figcaption caption")
)

type unit struct {
	id, kind, text, anchor string
}

type codeToken struct{ text, className string }

type docBuilder struct {
	codeByID     map[string]library.CodeBlock
	tokenIDs     []string
	tokens       map[string][]codeToken
	units        []unit
	occurrences  map[string]int
	usedAnchors  map[string]bool
	codeSeen     map[string]int
	nodes        int
	imageSize    int
	codeSize     int
	unitElements map[string]*hnode
}

func wrapper(n *hnode) bool {
	return n.typ == "element" && slices.ContainsFunc(n.classes(), func(c string) bool { return c == "reader-code-block" || c == "reader-table-scroll" })
}

// digest is sha256 over JSON.stringify(value), in hex.
func digest(v any) string {
	var b strings.Builder
	stringify(&b, v)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// stringify is JSON.stringify for the values here: strings, numbers, lists, ordered objects, hast nodes and units.
func stringify(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case string:
		jsonString(b, x)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			b.WriteString("null")
		} else {
			b.WriteString(jsNumber(x))
		}
	case int:
		b.WriteString(strconv.Itoa(x))
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case []string:
		b.WriteByte('[')
		for i, s := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			jsonString(b, s)
		}
		b.WriteByte(']')
	case []any:
		b.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			stringify(b, item)
		}
		b.WriteByte(']')
	case *jsObject:
		b.WriteByte('{')
		for i, k := range x.entries() {
			if i > 0 {
				b.WriteByte(',')
			}
			jsonString(b, k)
			b.WriteByte(':')
			stringify(b, x.get(k))
		}
		b.WriteByte('}')
	case *hnode:
		switch x.typ {
		case "text":
			b.WriteString(`{"type":"text","value":`)
			jsonString(b, x.value)
			b.WriteByte('}')
		case "root":
			b.WriteString(`{"type":"root","children":`)
			stringify(b, nodesOf(x.children))
			b.WriteByte('}')
		default:
			b.WriteString(`{"type":"element","tagName":`)
			jsonString(b, x.tag)
			b.WriteString(`,"properties":{`)
			for i, p := range x.props {
				if i > 0 {
					b.WriteByte(',')
				}
				jsonString(b, p.name)
				b.WriteByte(':')
				stringify(b, p.value)
			}
			b.WriteString(`},"children":`)
			stringify(b, nodesOf(x.children))
			b.WriteByte('}')
		}
	}
}

func nodesOf(list []*hnode) []any {
	out := make([]any, len(list))
	for i, n := range list {
		out[i] = n
	}
	return out
}

// jsonString writes s as JSON.stringify does: only quotes, backslashes and control characters are escaped.
func jsonString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				b.WriteString(strconv.FormatInt(int64(r)>>4, 16) + strconv.FormatInt(int64(r)&15, 16))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

func object(pairs ...any) *jsObject {
	o := &jsObject{values: map[string]any{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		k := pairs[i].(string)
		o.keys, o.values[k] = append(o.keys, k), pairs[i+1]
	}
	return o
}

func (o *jsObject) set(k string, v any) {
	if _, ok := o.values[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.values[k] = v
}

// directText is the text a block owns: not that of nested lists, code, tables, pictures, captions or child units.
func directText(n *hnode) string {
	var b strings.Builder
	var visit func(*hnode)
	visit = func(c *hnode) {
		switch {
		case c.typ == "text":
			b.WriteString(c.value)
		case c.typ != "element":
		case hasUnit(c):
		case c.tag == "br":
			b.WriteByte('\n')
		case directSkipped[c.tag]:
		default:
			for _, k := range c.children {
				visit(k)
			}
		}
	}
	for _, c := range n.children {
		visit(c)
	}
	return b.String()
}

func hasUnit(n *hnode) bool { v, ok := n.prop("dataReaderUnit"); _, s := v.(string); return ok && s }

// documentCodeText is a code block's text: text, a <br> a line break, line wrappers ending their line.
func documentCodeText(n *hnode) string {
	var b strings.Builder
	for i, c := range n.children {
		switch c.typ {
		case "text":
			b.WriteString(c.value)
		case "element":
			v := documentCodeText(c)
			if c.tag == "br" {
				v = "\n"
			}
			if (c.tag == "div" || c.tag == "p") && i < len(n.children)-1 && !strings.HasSuffix(v, "\n") {
				v += "\n"
			}
			b.WriteString(v)
		}
	}
	return b.String()
}

type semanticCounter struct{ nodes int }

// semantic is what a unit's identity and a revision are computed over: tags, the attributes that change meaning, text.
func semantic(n *hnode, depth int, c *semanticCounter) (any, error) {
	if c.nodes++; c.nodes > 50_000 || depth > 128 {
		return nil, errFormatStructure
	}
	if n.typ == "text" {
		return n.value, nil
	}
	if n.typ != "element" && n.typ != "root" {
		return nil, nil
	}
	children := []any{}
	for _, child := range n.children {
		v, err := semantic(child, depth+1, c)
		if err != nil {
			return nil, err
		}
		if v == nil {
			continue
		}
		if wrapper(child) {
			children = append(children, v.([]any)...)
		} else {
			children = append(children, v)
		}
	}
	if n.typ == "root" {
		return children, nil
	}
	if !readerTags[n.tag] {
		return nil, nil
	}
	names := n.classes()
	if slices.Contains(names, "reader-code-header") || slices.Contains(names, "reader-callout-title") {
		return nil, nil
	}
	if wrapper(n) {
		return children, nil
	}
	attributes := object()
	for _, key := range []string{"href", "title", "alt", "width", "height", "colSpan", "rowSpan", "scope", "start"} {
		if v, ok := n.prop(key); ok {
			attributes.set(key, v)
		}
	}
	if v, ok := n.prop("src"); ok && n.tag == "img" {
		if s, ok := v.(string); ok && httpAddress.MatchString(s) {
			attributes.set("src", s)
		}
	}
	if n.tag == "code" || n.tag == "pre" {
		for _, name := range names {
			if languageSemantic.MatchString(name) {
				attributes.set("language", name)
				break
			}
		}
		if n.tag == "code" {
			return []any{n.tag, attributes, documentCodeText(n)}, nil
		}
	}
	if n.tag == "blockquote" {
		kind := ""
		if v, ok := n.prop("dataCallout"); ok && jsTruthy(v) {
			kind = jsString(v)
		} else {
			for _, name := range names {
				if m := calloutClass.FindStringSubmatch(name); m != nil {
					kind = strings.ToUpper(m[1])
					break
				}
			}
		}
		if kind != "" {
			attributes.set("callout", kind)
		}
	}
	return []any{n.tag, attributes, children}, nil
}

func (d *docBuilder) addUnit(n *hnode, kind, value string) error {
	if len(d.units) >= 10_000 || web.JSLen(value) > 2*1024*1024 {
		return errUnits
	}
	s, err := semantic(n, 0, &semanticCounter{})
	if err != nil {
		return err
	}
	fingerprint := digest([]any{kind, value, s})
	d.occurrences[fingerprint]++
	id := "reader-unit-" + digest([]any{fingerprint, float64(d.occurrences[fingerprint])})[:24]
	n.setProp("dataReaderUnit", id)
	u := unit{id: id, kind: kind, text: value}
	if v, ok := n.prop("id"); ok {
		if anchor, ok := validID(v); ok {
			u.anchor = anchor
		}
	}
	d.units = append(d.units, u)
	return nil
}

// readerLink is a link the reader keeps: an http(s) address without credentials, or a #fragment.
func readerLink(v any) string {
	s, ok := v.(string)
	if !ok || web.JSLen(s) > 8192 {
		return ""
	}
	if strings.HasPrefix(s, "#") {
		return s
	}
	u, err := web.ParseURL(s, nil)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Username != "" || u.Password != "" {
		return ""
	}
	return u.Href()
}

func jsNumberOf(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		return web.JSNumber(x)
	case []string:
		return web.JSNumber(strings.Join(x, ","))
	}
	return math.NaN()
}

func (d *docBuilder) convert(n *hnode, depth int, inUnit bool) (*hnode, error) {
	if d.nodes++; d.nodes > 50_000 || depth > 128 {
		return nil, errFormatStructure
	}
	if n.typ == "text" {
		return htext(n.value), nil
	}
	if n.typ != "element" || !readerTags[n.tag] {
		return nil, nil
	}
	names := n.classes()
	if slices.Contains(names, "reader-code-header") || slices.Contains(names, "reader-callout-title") {
		return nil, nil
	}
	if n.tag == "img" {
		if src, ok := n.prop("src"); ok {
			s, isText := src.(string)
			if !isText || !strings.HasPrefix(s, "data:image/") {
				return nil, errImages
			}
			if d.imageSize += web.JSLen(s); d.imageSize > 24*1024*1024 {
				return nil, errImages
			}
		}
	}
	result := &hnode{typ: "element", tag: n.tag}
	for _, key := range []string{"id", "href", "title", "src", "alt", "width", "height", "colSpan", "rowSpan", "scope", "start"} {
		value, ok := n.prop(key)
		if !ok {
			continue
		}
		switch key {
		case "id":
			if id, ok := validID(value); ok && !d.usedAnchors[id] {
				result.setProp("id", id)
				d.usedAnchors[id] = true
			}
		case "href":
			if href := readerLink(value); href != "" {
				result.setProp("href", href)
			}
		case "src":
			if s, ok := value.(string); ok {
				result.setProp("src", s)
			}
		case "width", "height", "colSpan", "rowSpan", "start":
			number := jsNumberOf(value)
			least := 1.0
			if key == "start" {
				least = -100000
			} else if key == "rowSpan" {
				least = 0
			}
			if number == math.Trunc(number) && math.Abs(number) <= 1<<53-1 && number >= least && number <= 100000 {
				result.setProp(key, number)
			}
		default:
			result.setProp(key, value)
		}
	}
	var language string
	for _, name := range names {
		if languageName.MatchString(name) {
			language = name
			break
		}
	}
	if language != "" && (n.tag == "pre" || n.tag == "code") {
		result.setProp("className", []string{language})
	}
	if n.tag == "blockquote" {
		for _, name := range names {
			if m := calloutClass.FindStringSubmatch(name); m != nil {
				result.setProp("dataCallout", strings.ToUpper(m[1]))
				break
			}
		}
	}
	if n.tag == "pre" {
		return d.pre(n, result, names)
	}
	kind := ""
	switch {
	case headingTag.MatchString(n.tag) && !inUnit:
		kind = "heading"
	case n.tag == "li":
		kind = "list-item"
	case n.tag == "p" && !inUnit:
		kind = "paragraph"
	case n.tag == "figcaption" || n.tag == "caption":
		kind = "caption"
	case n.tag == "th" || n.tag == "td":
		kind = "cell"
	case n.tag == "img":
		kind = "image"
	}
	owns := kind != "" && kind != "image"
	for _, child := range n.children {
		if wrapper(child) {
			list, err := d.convertChildren(child.children, depth+1)
			if err != nil {
				return nil, err
			}
			result.children = append(result.children, list...)
			continue
		}
		converted, err := d.convert(child, depth+1, inUnit || owns)
		if err != nil {
			return nil, err
		}
		if converted != nil {
			result.children = append(result.children, converted)
		}
	}
	switch {
	case kind != "":
		value := directText(result)
		if kind == "image" {
			alt, _ := result.prop("alt")
			value = ""
			if jsTruthy(alt) {
				value = jsString(alt)
			}
		}
		if web.JSTrim(value) != "" || kind == "image" {
			if err := d.addUnit(result, kind, value); err != nil {
				return nil, err
			}
		}
	case result.tag == "div" && !inUnit:
		if value := directText(result); web.JSTrim(value) != "" {
			if err := d.addUnit(result, "paragraph", value); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

// pre turns a pre into one code element holding its raw text, paired with the formatter's code block when there is one.
func (d *docBuilder) pre(n, result *hnode, names []string) (*hnode, error) {
	var original *hnode
	for _, c := range n.children {
		if c.isElement("code") {
			original = c
			break
		}
	}
	var codeID any
	var block library.CodeBlock
	paired := false
	if original != nil {
		codeID, _ = original.prop("id")
		if s, ok := codeID.(string); ok {
			block, paired = d.codeByID[s]
		}
	}
	source := n
	if original != nil {
		source = original
	}
	raw := block.Code
	if !paired || raw == "" {
		raw = documentCodeText(source)
	}
	if d.codeSize += web.JSLen(raw); d.codeSize > 2*1024*1024 {
		return nil, errCode
	}
	fingerprint := digest(raw)
	d.codeSeen[fingerprint]++
	id := block.ID
	if !paired || id == "" {
		if s, ok := validID(codeID); ok && !d.usedAnchors[s] {
			id = s
		} else {
			id = "reader-code-" + digest([]any{fingerprint, float64(d.codeSeen[fingerprint])})[:24]
		}
	}
	var requested string
	for _, name := range append(slices.Clone(source.classes()), names...) {
		if languageName.MatchString(name) {
			requested = name
			break
		}
	}
	code := &hnode{typ: "element", tag: "code", props: []hprop{{"id", id}}, children: []*hnode{htext(raw)}}
	switch {
	case paired:
		code.props = append(code.props, hprop{"className", []string{"language-" + block.Language}})
	case requested != "":
		code.props = append(code.props, hprop{"className", []string{requested}})
	}
	if d.usedAnchors[id] {
		code.delProp("id")
	} else {
		d.usedAnchors[id] = true
	}
	result.children = []*hnode{code}
	// A block the formatter made is coloured by the reader (MyGo's highlighting), so it stores no presentation tokens;
	// any other pre keeps its text as one plain token.
	if !(paired && original != nil) {
		if _, seen := d.tokens[id]; !seen {
			d.tokenIDs = append(d.tokenIDs, id)
		}
		d.tokens[id] = []codeToken{{text: raw}}
	}
	if err := d.addUnit(code, "code", raw); err != nil {
		return nil, err
	}
	return result, nil
}

func (d *docBuilder) convertChildren(children []*hnode, depth int) ([]*hnode, error) {
	var out []*hnode
	for _, child := range children {
		if wrapper(child) {
			list, err := d.convertChildren(child.children, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, list...)
			continue
		}
		converted, err := d.convert(child, depth, false)
		if err != nil {
			return nil, err
		}
		if converted != nil {
			out = append(out, converted)
		}
	}
	return out, nil
}

// createReaderDocument builds the document and returns it as the JSON the library stores.
func createReaderDocument(root *hnode, codeBlocks []library.CodeBlock, provenance string, sourceRoot *hnode) ([]byte, error) {
	d := &docBuilder{codeByID: map[string]library.CodeBlock{}, tokens: map[string][]codeToken{}, occurrences: map[string]int{}, usedAnchors: map[string]bool{}, codeSeen: map[string]int{}}
	for _, b := range codeBlocks {
		d.codeByID[b.ID] = b
	}
	top, err := d.convertChildren(root.children, 1)
	if err != nil {
		return nil, err
	}
	var normalized, inline []*hnode
	flush := func() error {
		if len(inline) == 0 {
			return nil
		}
		paragraph := &hnode{typ: "element", tag: "p", children: inline}
		value := directText(paragraph)
		if web.JSTrim(value) != "" || slices.ContainsFunc(inline, func(n *hnode) bool { return n.typ == "element" }) {
			if web.JSTrim(value) != "" {
				if err := d.addUnit(paragraph, "paragraph", value); err != nil {
					return err
				}
			}
			normalized = append(normalized, paragraph)
		}
		inline = nil
		return nil
	}
	for _, n := range top {
		switch {
		case n.typ == "element" && documentBlocks[n.tag]:
			if err := flush(); err != nil {
				return nil, err
			}
			normalized = append(normalized, n)
		case n.typ == "element" || n.typ == "text":
			inline = append(inline, n)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	canonical := &hnode{typ: "root", children: normalized}
	positions := map[string]int{}
	var walk func(n *hnode)
	walk = func(n *hnode) {
		if n.typ != "element" {
			return
		}
		if v, ok := n.prop("dataReaderUnit"); ok {
			if s, ok := v.(string); ok {
				positions[s] = len(positions)
			}
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	for _, n := range canonical.children {
		walk(n)
	}
	sort.SliceStable(d.units, func(i, j int) bool { return positions[d.units[i].id] < positions[d.units[j].id] })
	revisionRoot := canonical
	if provenance == "source" && sourceRoot != nil {
		revisionRoot = sourceRoot
	}
	s, err := semantic(revisionRoot, 0, &semanticCounter{})
	if err != nil {
		return nil, err
	}
	contentRevision := digest(s)
	unitList := make([]any, len(d.units))
	for i, u := range d.units {
		unitList[i] = []any{u.kind, u.id, u.text}
	}
	unitsRevision := digest(unitList)
	var b strings.Builder
	b.WriteString(`{"version":1,"contentRevision":"` + contentRevision + `","unitsRevision":"` + unitsRevision + `","provenance":`)
	jsonString(&b, provenance)
	b.WriteString(`,"root":`)
	stringify(&b, canonical)
	b.WriteString(`,"units":[`)
	for i, u := range d.units {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"id":`)
		jsonString(&b, u.id)
		b.WriteString(`,"kind":`)
		jsonString(&b, u.kind)
		b.WriteString(`,"text":`)
		jsonString(&b, u.text)
		if u.anchor != "" {
			b.WriteString(`,"anchor":`)
			jsonString(&b, u.anchor)
		}
		b.WriteByte('}')
	}
	b.WriteString(`],"codeTokens":{`)
	ids := slices.Clone(d.tokenIDs)
	tokenKeys := &jsObject{keys: ids, values: map[string]any{}}
	for i, id := range tokenKeys.entries() {
		if i > 0 {
			b.WriteByte(',')
		}
		jsonString(&b, id)
		b.WriteString(":[")
		for j, t := range d.tokens[id] {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"text":`)
			jsonString(&b, t.text)
			if t.className != "" {
				b.WriteString(`,"className":`)
				jsonString(&b, t.className)
			}
			b.WriteByte('}')
		}
		b.WriteByte(']')
	}
	b.WriteString("}}")
	doc := []byte(b.String())
	// The document is refused when it fails the reader's check, before anything is saved.
	if !library.IsReaderDocument(doc) {
		return nil, errors.New("阅读文档未通过结构校验")
	}
	return doc, nil
}
