package library

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"trie/internal/locales"
	"trie/internal/web"
	"unicode/utf8"
)

// The checks on stored rows, actions and reader documents. Stored JSON is
// decoded to `any` (objects, []any, string, json.Number, bool, nil) and read
// field by field as earlier versions read it, so a row they accept is accepted
// here and a damaged one is refused or skipped the same way.

type object = map[string]any

// parseJSON is JSON.parse.
func parseJSON(text string) (any, error) {
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("Unexpected non-whitespace character after JSON")
	}
	return v, nil
}

// objectJSON is stored text holding a JSON object.
func objectJSON(value any) (object, error) {
	text, ok := value.(string)
	if !ok {
		return nil, errors.New("Invalid stored JSON")
	}
	v, err := parseJSON(text)
	if err != nil {
		return nil, err
	}
	o, ok := v.(object)
	if !ok {
		return nil, errors.New("Invalid stored object")
	}
	return o, nil
}

// fields reads strings, nullable strings, counts and string arrays, keeping
// the first error.
type fields struct {
	err error
}

func (f *fields) fail(message string) {
	if f.err == nil {
		f.err = errors.New(message)
	}
}

func (f *fields) str(v any) string {
	s, ok := v.(string)
	if !ok {
		f.fail("Expected string")
	}
	return s
}

func (f *fields) nstr(v any) *string {
	if v == nil {
		return nil
	}
	s := f.str(v)
	return &s
}

func (f *fields) count(v any) int {
	n, ok := safeCount(v)
	if !ok {
		f.fail("Expected nonnegative integer")
	}
	return n
}

func (f *fields) strs(v any) []string {
	list, ok := v.([]any)
	if !ok {
		f.fail("Expected string array")
		return nil
	}
	out := make([]string, len(list))
	for i, item := range list {
		out[i] = f.str(item)
	}
	return out
}

// safeCount is a nonnegative safe integer, from JSON or from SQLite.
func safeCount(v any) (int, bool) {
	f, ok := jsNumberValue(v)
	if !ok || f != math.Trunc(f) || math.Abs(f) > 1<<53-1 || f < 0 {
		return 0, false
	}
	return int(f), true
}

// jsNumberValue is v when typeof v === 'number'.
func jsNumberValue(v any) (float64, bool) {
	switch v := v.(type) {
	case json.Number:
		return web.NumberValue(v)
	case int64:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

// storedBoolean is a 0 or 1 column.
func storedBoolean(v any) (bool, error) {
	if f, ok := jsNumberValue(v); ok && (f == 0 || f == 1) {
		return f == 1, nil
	}
	return false, errors.New("Invalid stored boolean")
}

// jsTruthy is JavaScript truthiness of a SQLite value.
func jsTruthy(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case int64:
		return v != 0
	case float64:
		return v != 0 && !math.IsNaN(v)
	}
	return true
}

func sourceJSON(value any) (SourceSnapshot, error) {
	v, err := objectJSON(value)
	if err != nil {
		return SourceSnapshot{}, err
	}
	var f fields
	s := SourceSnapshot{ID: f.str(v["id"]), Title: f.str(v["title"]), Host: f.str(v["host"]), HomeURL: f.str(v["homeURL"]),
		Icon: f.nstr(v["icon"]), CheckedAt: f.str(v["checkedAt"]), SampledCount: f.count(v["sampledCount"]),
		CatalogCandidateCount: f.count(v["catalogCandidateCount"]), ScopeLabel: f.str(v["scopeLabel"])}
	if d, ok := v["description"].(string); ok {
		s.Description = Some(d)
	}
	return s, f.err
}

func articleJSON(value any) (ArticleSnapshot, error) {
	v, err := objectJSON(value)
	if err != nil {
		return ArticleSnapshot{}, err
	}
	if v["bodyStatus"] != "not_extracted" {
		return ArticleSnapshot{}, errors.New("Invalid article body status")
	}
	var f fields
	a := ArticleSnapshot{ID: f.str(v["id"]), URL: f.str(v["url"]), SourceID: f.str(v["sourceID"]), Title: f.str(v["title"]),
		Authors: f.strs(v["authors"]), PublishedAt: f.nstr(v["publishedAt"]), ModifiedAt: f.nstr(v["modifiedAt"]),
		Aliases: f.strs(v["aliases"]), BodyStatus: "not_extracted", VerificationLabel: f.str(v["verificationLabel"])}
	if k, ok := v["kinds"]; ok {
		a.Kinds = f.strs(k)
	}
	return a, f.err
}

func reviewJSON(value any) (ReviewItem, error) {
	v, err := objectJSON(value)
	if err != nil {
		return ReviewItem{}, err
	}
	status, _ := v["status"].(string)
	if status != "uncertain" && status != "non_article" && status != "failed" && status != "unprocessed" {
		return ReviewItem{}, errors.New("Invalid review status")
	}
	var f fields
	r := ReviewItem{ID: f.str(v["id"]), URL: f.str(v["url"]), SourceID: f.str(v["sourceID"]), Title: f.str(v["title"]),
		Authors: f.strs(v["authors"]), PublishedAt: f.nstr(v["publishedAt"]), Status: status, Reason: f.str(v["reason"])}
	return r, f.err
}

var (
	isoMillis = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`)
	uuid      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	sha256Hex = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func clipID(s string) bool { return uuid.MatchString(s) }

// dateOK is Number.isFinite(Date.parse(s)).
func dateOK(s string) bool { _, ok := web.JSDateParse(s); return ok }

// checkClip checks a typed clip.
func checkClip(c ClipInput) error {
	switch {
	case !clipID(c.ID):
		return errors.New(locales.ErrorInvalidClipID())
	case c.ArticleID == "" || web.JSLen(c.ArticleID) > 4096:
		return errors.New(locales.ErrorInvalidArticleID())
	case web.JSTrim(c.Text) == "" || web.JSLen(c.Text) > 20000:
		return errors.New(locales.ErrorClipLength())
	case c.UnitID != nil && !unitOK(*c.UnitID), !clipPicturesOK(c.Pictures, utf8.RuneCountInString(c.Text)):
		return errors.New(locales.ErrorInvalidClipParagraph())
	case c.ContentRevision != nil && !sha256Hex.MatchString(*c.ContentRevision):
		return errors.New(locales.ErrorInvalidTextVersion())
	case c.CreatedAt != "" && (!isoMillis.MatchString(c.CreatedAt) || !dateOK(c.CreatedAt)):
		return errors.New(locales.ErrorInvalidClipDate())
	case web.JSLen(c.Note) > 4000:
		return errors.New(locales.ErrorNoteTooLong())
	}
	return nil
}

// unitOK is a reader unit a clip may name.
func unitOK(s string) bool {
	return s != "" && web.JSLen(s) <= 256 && strings.IndexFunc(s, controlOrSpace) < 0
}

// clipPicturesOK checks a clip's pictures against its text of n runes: at
// most 64, each unit named once, standing in order within the text.
func clipPicturesOK(pictures []ClipPicture, n int) bool {
	if len(pictures) > 64 { // reader.MaxClipPictures
		return false
	}
	at := 0
	for i, p := range pictures {
		if !unitOK(p.Unit) || p.At < at || p.At > n ||
			slices.ContainsFunc(pictures[:i], func(q ClipPicture) bool { return q.Unit == p.Unit }) {
			return false
		}
		at = p.At
	}
	return true
}

func control(r rune) bool        { return r <= 0x1f || r == 0x7f }
func controlOrSpace(r rune) bool { return control(r) || web.JSSpace(r) }

// clipInputJSON reads a stored clip's fields with the types parseClipInput requires.
func clipInputJSON(v object) (ClipInput, bool) {
	var c ClipInput
	var ok bool
	if c.ID, ok = v["id"].(string); !ok {
		return c, false
	}
	if c.ArticleID, ok = v["articleID"].(string); !ok {
		return c, false
	}
	if c.Text, ok = v["text"].(string); !ok {
		return c, false
	}
	if c.Note, ok = v["note"].(string); !ok {
		return c, false
	}
	for _, field := range []struct {
		key string
		to  **string
	}{{"unitID", &c.UnitID}, {"contentRevision", &c.ContentRevision}} {
		switch s := v[field.key].(type) {
		case nil:
			if _, present := v[field.key]; !present {
				return c, false
			}
		case string:
			*field.to = &s
		default:
			return c, false
		}
	}
	if created, present := v["createdAt"]; present {
		if c.CreatedAt, ok = created.(string); !ok || c.CreatedAt == "" {
			return c, false
		}
	}
	if pictures, present := v["pictures"]; present && pictures != nil { // null, as no pictures
		list, ok := pictures.([]any)
		if !ok {
			return c, false
		}
		for _, item := range list {
			p, ok := item.(object)
			unit, isString := p["unit"].(string)
			at, isCount := safeCount(p["at"])
			if !ok || !isString || !isCount {
				return c, false
			}
			c.Pictures = append(c.Pictures, ClipPicture{Unit: unit, At: at})
		}
	}
	return c, checkClip(c) == nil
}

// clipJSON is a stored clip, or nil when damaged: a damaged clip must not
// prevent the library from loading.
func clipJSON(value, articleID any) *Clip {
	v, err := objectJSON(value)
	if err != nil {
		return nil
	}
	input, ok := clipInputJSON(v)
	updated, isString := v["updatedAt"].(string)
	c, isObject := v["citation"].(object)
	if !ok || input.ArticleID != articleID || input.CreatedAt == "" || !isString || !dateOK(updated) || !isObject {
		return nil
	}
	var f fields
	clip := &Clip{ID: input.ID, ArticleID: input.ArticleID, Text: input.Text, Note: input.Note, UnitID: input.UnitID,
		ContentRevision: input.ContentRevision, CreatedAt: input.CreatedAt, UpdatedAt: updated, Pictures: input.Pictures,
		Citation: Citation{Title: f.str(c["title"]), URL: f.str(c["url"]), SourceName: f.str(c["sourceName"]),
			Authors: f.strs(c["authors"]), PublishedAt: f.nstr(c["publishedAt"])}}
	if f.err != nil {
		return nil
	}
	return clip
}

// checkPosition checks a typed reading position.
func checkPosition(p ReadingPositionInput) error {
	bounded := func(s string, max int) bool {
		return s != "" && web.JSLen(s) <= max && strings.IndexFunc(s, control) < 0
	}
	finite := func(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
	if !bounded(p.ArticleID, 4096) || !bounded(p.ContentVersion, 256) || p.UnitID != nil && !bounded(*p.UnitID, 256) ||
		!finite(p.Offset) || math.Abs(p.Offset) > 1e7 || !finite(p.ScrollTop) || p.ScrollTop < 0 || p.ScrollTop > 1e7 ||
		!finite(p.Progress) || p.Progress < 0 || p.Progress > 1 {
		return errors.New(locales.ErrorInvalidPosition())
	}
	return nil
}

// positionJSON is a stored reading position, or nil when damaged.
func positionJSON(data, articleID any) *ReadingPosition {
	text, ok := data.(string)
	if !ok {
		return nil
	}
	parsed, err := parseJSON(text)
	v, isObject := parsed.(object)
	if err != nil || !isObject {
		return nil
	}
	var p ReadingPosition
	p.ArticleID, _ = v["articleID"].(string)
	p.ContentVersion, _ = v["contentVersion"].(string)
	switch u := v["unitID"].(type) {
	case string:
		p.UnitID = &u
	case nil:
		if _, present := v["unitID"]; !present {
			return nil
		}
	default:
		return nil
	}
	var okOffset, okTop, okProgress bool
	p.Offset, okOffset = number(v["offset"])
	p.ScrollTop, okTop = number(v["scrollTop"])
	p.Progress, okProgress = number(v["progress"])
	p.UpdatedAt, ok = v["updatedAt"].(string)
	if !okOffset || !okTop || !okProgress || checkPosition(p.ReadingPositionInput) != nil || p.ArticleID != articleID ||
		!ok || !isoMillis.MatchString(p.UpdatedAt) || !dateOK(p.UpdatedAt) {
		return nil
	}
	return &p
}

// number is a JSON number; one too large for float64 reads as infinite, as in JavaScript.
func number(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	return web.NumberValue(n)
}

// previewJSON is a stored preview without its sources, or nil when damaged:
// a damaged preview only hides the list hint.
func previewJSON(value any) *ArticlePreview {
	v, err := objectJSON(value)
	if err != nil {
		return nil
	}
	hasImage, isBool := v["hasImage"].(bool)
	excerpt, isString := v["excerpt"].(string)
	if !isBool || v["excerpt"] != nil && !isString {
		return nil
	}
	if _, present := v["excerpt"]; !present {
		return nil
	}
	var f fields
	p := &ArticlePreview{Images: f.count(v["images"]), CodeBlocks: f.count(v["codeBlocks"]), HasImage: hasImage}
	if isString {
		p.Excerpt = &excerpt
	}
	if m, present := v["minutes"]; !present || m != nil {
		n := f.count(m)
		p.Minutes = &n
	}
	if g, ok := number(v["gallery"]); ok {
		p.Gallery = int(g) // ponytail: a fractional count (never written) is truncated
	} else if hasImage {
		p.Gallery = 1
	}
	if f.err != nil {
		return nil
	}
	return p
}

// formattedJSON is the stored format of a cached body, or ok false when any part is invalid.
func formattedJSON(v map[string]any) (version int, headings []Heading, blocks []CodeBlock, styles []string, ok bool) {
	version, okVersion := safeCount(v["formatVersion"])
	if _, isNumber := v["formatVersion"].(json.Number); !isNumber || !okVersion || version < 1 {
		return 0, nil, nil, nil, false
	}
	hs, okH := v["headings"].([]any)
	cs, okC := v["codeBlocks"].([]any)
	ss, okS := v["highlightStyles"].([]any)
	if !okH || len(hs) > 512 || !okC || len(cs) > 128 || !okS || len(ss) > 256 {
		return 0, nil, nil, nil, false
	}
	headings, blocks, styles = []Heading{}, []CodeBlock{}, []string{}
	for _, item := range hs {
		h, _ := item.(object)
		id, okID := h["id"].(string)
		text, okText := h["text"].(string)
		level, okLevel := number(h["level"])
		if h == nil || !okID || web.JSLen(id) > 256 || !okText || web.JSLen(text) > 512 || !okLevel || level != math.Trunc(level) || level < 1 || level > 6 {
			return 0, nil, nil, nil, false
		}
		headings = append(headings, Heading{ID: id, Level: int(level), Text: text})
	}
	bytes := 0
	for _, item := range cs {
		c, _ := item.(object)
		id, okID := c["id"].(string)
		language, okLanguage := c["language"].(string)
		code, okCode := c["code"].(string)
		if c == nil || !okID || web.JSLen(id) > 256 || !okLanguage || web.JSLen(language) > 64 || !okCode {
			return 0, nil, nil, nil, false
		}
		if bytes += len(code); bytes > 2*1024*1024 {
			return 0, nil, nil, nil, false
		}
		blocks = append(blocks, CodeBlock{ID: id, Language: language, Code: code})
	}
	for _, item := range ss {
		rule, isString := item.(string)
		if !isString || web.JSLen(rule) > 256 {
			return 0, nil, nil, nil, false
		}
		styles = append(styles, rule)
	}
	return version, headings, blocks, styles, true
}

// sourceSnapshotJSON is a cached body's source snapshot, or nil when invalid.
// ponytail: a provenance of ['source'] passes the check (String() matches)
// and is read back as the string, not the array.
func sourceSnapshotJSON(value any) *ReaderSourceSnapshot {
	v, ok := value.(object)
	if !ok {
		return nil
	}
	html, ok := v["html"].(string)
	if !ok || web.JSLen(html) > 24*1024*1024 || !provenance(v["provenance"]) {
		return nil
	}
	return &ReaderSourceSnapshot{HTML: html, Provenance: web.JSString(v["provenance"])}
}

func provenance(v any) bool { p := web.JSString(v); return p == "source" || p == "legacy-cache" }

func identifier(v any) bool {
	s, ok := v.(string)
	return ok && s != "" && web.JSLen(s) <= 256 && strings.IndexFunc(s, controlOrSpace) < 0
}

func hash(v any) bool { s, ok := v.(string); return ok && sha256Hex.MatchString(s) }

// readerLink is an http(s) address without credentials, or a #fragment.
func readerLink(v any) (string, bool) {
	s, ok := v.(string)
	if !ok || web.JSLen(s) > 8192 {
		return "", false
	}
	if strings.HasPrefix(s, "#") {
		return s, true
	}
	u, err := web.ParseURL(s, nil)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Username != "" || u.Password != "" {
		return "", false
	}
	return u.Href(), true
}

func externalLink(v any) bool {
	_, ok := readerLink(v)
	return ok && !strings.HasPrefix(v.(string), "#")
}

// base64Data is /^data:<kind>;base64,[a-z\d+/=]+$/i, its prefix one of kinds. Such a string is ASCII.
func base64Data(v any, max int, kinds ...string) bool {
	s, ok := v.(string)
	if !ok || longer(s, max) {
		return false
	}
	for _, kind := range kinds {
		prefix := "data:" + kind + ";base64,"
		if len(s) > len(prefix) && asciiFold(s[:len(prefix)], prefix) {
			for i := len(prefix); i < len(s); i++ {
				if !base64Byte[s[i]] {
					return false
				}
			}
			return true
		}
	}
	return false
}

var base64Byte = func() (t [256]bool) {
	for _, c := range "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=" {
		t[c] = true
	}
	return t
}()

// longer is web.JSLen(s) > n without counting a long s: a byte is at most one code unit, three bytes at least one.
func longer(s string, n int) bool { return len(s) > 3*n || len(s) > n && web.JSLen(s) > n }

// asciiFold compares as /i does for ASCII text.
func asciiFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if web.LowerASCII(rune(a[i])) != web.LowerASCII(rune(b[i])) {
			return false
		}
	}
	return true
}

func readerImage(v any) bool {
	return base64Data(v, 12*1024*1024, "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/svg+xml")
}

func text(v any, max int) bool { s, ok := v.(string); return ok && web.JSLen(s) <= max }

// isGalleryWork is the check of a cached work page.
func isGalleryWork(value any) bool {
	v, ok := value.(object)
	optional := func(key string) bool { return v[key] == nil && has(v, key) || text(v[key], 512) }
	source := func(s any, present bool) bool { return !present || s == nil || externalLink(s) }
	if !ok || !text(v["name"], 512) || !optional("designer") || !optional("builtWith") || !optional("host") ||
		!(v["site"] == nil && has(v, "site") || externalLink(v["site"])) ||
		!(v["poster"] == nil && has(v, "poster") || readerImage(v["poster"])) ||
		!(v["video"] == nil && has(v, "video") || base64Data(v["video"], 24*1024*1024, "video/mp4")) {
		return false
	}
	facts, ok := v["facts"].([]any)
	if !ok || len(facts) > 16 {
		return false
	}
	for _, item := range facts {
		fact, ok := item.(object)
		values, isList := fact["values"].([]any)
		if !ok || !text(fact["label"], 64) || !isList || len(values) > 64 {
			return false
		}
		for _, value := range values {
			if !text(value, 128) {
				return false
			}
		}
	}
	posterSource, hasPoster := v["posterSource"]
	videoSource, hasVideo := v["videoSource"]
	if !source(posterSource, hasPoster) || !source(videoSource, hasVideo) {
		return false
	}
	sections, ok := v["sections"].([]any)
	if !ok || len(sections) > 12 {
		return false
	}
	for _, item := range sections {
		shot, ok := item.(object)
		shotSource, present := shot["source"]
		if !ok || !text(shot["name"], 128) || !(shot["image"] == nil && has(shot, "image") || readerImage(shot["image"])) || !source(shotSource, present) {
			return false
		}
	}
	return true
}

func has(v object, key string) bool { _, ok := v[key]; return ok }

var (
	readerTags = set("p", "br", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "blockquote", "pre", "code", "table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption", "figure", "figcaption", "img", "a", "hr", "strong", "em", "b", "i", "s", "del", "sup", "sub", "div", "span")
	properties = set("id", "className", "href", "title", "src", "alt", "width", "height", "colSpan", "rowSpan", "scope", "start", "dataReaderUnit", "dataCallout", "dataReaderRole")
	unitKinds  = set("heading", "paragraph", "list-item", "caption", "code", "image", "cell")
	callouts   = set("NOTE", "TIP", "IMPORTANT", "WARNING", "CAUTION")
	language   = regexp.MustCompile(`^` + ci("language-") + `[a-zA-Z0-9_-]{1,64}$`)
	tokenClass = regexp.MustCompile(`^reader-token-\d{1,3}$`)
)

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[item] = true
	}
	return m
}

// IsReaderDocument is isReaderDocument for a document still in its JSON text,
// such as one just built.
func IsReaderDocument(doc []byte) bool {
	passed.Lock()
	same := len(doc) > 0 && passed.doc == string(doc)
	passed.Unlock()
	if same {
		return true
	}
	v, err := parseJSON(string(doc))
	if err != nil || !isReaderDocument(v) {
		return false
	}
	passed.Lock()
	passed.doc = string(doc)
	passed.Unlock()
	return true
}

// passed is the last document IsReaderDocument passed. A read checks its document as it is built, saved and read
// back; comparing megabytes of inlined pictures is much quicker than parsing them again.
var passed struct {
	sync.Mutex
	doc string
}

// isReaderDocument is the check of a stored document before it reaches the reader.
func isReaderDocument(value any) bool {
	v, ok := value.(object)
	if !ok {
		return false
	}
	version, _ := number(v["version"])
	root, okRoot := v["root"].(object)
	children, okChildren := root["children"].([]any)
	units, okUnits := v["units"].([]any)
	tokens, okTokens := v["codeTokens"].(object)
	if version != 1 || !hash(v["contentRevision"]) || !hash(v["unitsRevision"]) || !provenance(v["provenance"]) ||
		!okRoot || root["type"] != "root" || !okChildren || len(children) > 50000 || !okUnits || len(units) > 10000 || !okTokens {
		return false
	}
	unitIDs, bindings, anchors := map[string]bool{}, map[string]bool{}, map[string]bool{}
	textSize, imageSize, nodeCount := 0, 0, 0
	for _, item := range units {
		unit, ok := item.(object)
		id, _ := unit["id"].(string)
		kind, _ := unit["kind"].(string)
		text, isText := unit["text"].(string)
		anchor, hasAnchor := unit["anchor"]
		if !ok || !identifier(unit["id"]) || unitIDs[id] || !unitKinds[kind] || !isText || web.JSLen(text) > 2*1024*1024 || hasAnchor && !identifier(anchor) {
			return false
		}
		unitIDs[id] = true
	}
	type frame struct {
		node  any
		depth int
	}
	stack := make([]frame, 0, len(children))
	for _, child := range children {
		stack = append(stack, frame{child, 1})
	}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nodeCount++
		node, ok := f.node.(object)
		if nodeCount > 50000 || f.depth > 128 || !ok {
			return false
		}
		if node["type"] == "text" {
			value, ok := node["value"].(string)
			if textSize += web.JSLen(value); !ok || textSize > 24*1024*1024 {
				return false
			}
			continue
		}
		tag, _ := node["tagName"].(string)
		props, okProps := node["properties"].(object)
		kids, okKids := node["children"].([]any)
		if node["type"] != "element" || !readerTags[tag] || !okProps || !okKids {
			return false
		}
		for key, property := range props {
			if !properties[key] {
				return false
			}
			switch key {
			case "className":
				names, ok := property.([]any)
				if !ok || len(names) > 2 {
					return false
				}
				for _, name := range names {
					s, ok := name.(string)
					if !ok || !language.MatchString(s) {
						return false
					}
				}
			case "src":
				if tag != "img" || !readerImage(property) {
					return false
				}
				if imageSize += len(property.(string)); imageSize > 24*1024*1024 { // a reader image is ASCII
					return false
				}
			case "href":
				if _, ok := readerLink(property); tag != "a" || !ok {
					return false
				}
			case "id":
				if !identifier(property) || anchors[property.(string)] {
					return false
				}
				anchors[property.(string)] = true
			case "dataReaderUnit":
				if !identifier(property) || !unitIDs[property.(string)] || bindings[property.(string)] {
					return false
				}
				bindings[property.(string)] = true
			case "dataCallout":
				if tag != "blockquote" || !callouts[web.JSString(property)] {
					return false
				}
			case "dataReaderRole":
				if tag != "p" || web.JSString(property) != "dek" {
					return false
				}
			case "width", "height", "colSpan", "rowSpan", "start":
				var n float64
				switch p := property.(type) {
				case json.Number:
					n, _ = web.NumberValue(p)
				case string:
					n = web.JSNumber(p)
				default:
					return false
				}
				least := 1.0
				if key == "start" {
					least = -100000
				} else if key == "rowSpan" {
					least = 0
				}
				if math.IsNaN(n) || n != math.Trunc(n) || math.Abs(n) > 1<<53-1 || n < least || n > 100000 {
					return false
				}
			default:
				if !text(property, 8192) {
					return false
				}
			}
		}
		if len(stack)+nodeCount+len(kids) > 50000 {
			return false
		}
		for _, kid := range kids {
			stack = append(stack, frame{kid, f.depth + 1})
		}
	}
	if len(bindings) != len(unitIDs) {
		return false
	}
	for _, item := range units {
		if anchor, _ := item.(object)["anchor"].(string); anchor != "" && !anchors[anchor] {
			return false
		}
	}
	tokenCount, tokenText := 0, 0
	for id, list := range tokens {
		items, ok := list.([]any)
		if !identifier(id) || !ok {
			return false
		}
		for _, item := range items {
			tokenCount++
			token, ok := item.(object)
			text, isText := token["text"].(string)
			if tokenText += web.JSLen(text); tokenCount > 50000 || !ok || !isText || tokenText > 2*1024*1024 {
				return false
			}
			if class, present := token["className"]; present {
				if s, ok := class.(string); !ok || !tokenClass.MatchString(s) {
					return false
				}
			}
		}
	}
	return true
}
