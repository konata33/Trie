package library

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"trie/internal/web"
	"unicode"
	"unicode/utf8"
)

// Article previews, computed as earlier versions computed them. Their regexps
// are JavaScript's: \s is Unicode whitespace, /i folds ASCII only, lengths and
// slices count UTF-16 code units. The patterns below spell that out for RE2.

// previewVersion is the preview rules' version: stored rows of another
// version are recomputed when the library opens.
const previewVersion = 3

// storedPreview is an ArticlePreview with the picture sources the list does
// not see; inline:<n> is the nth <img> of a cached page, work:poster and
// work:<n> a work page's pictures.
type storedPreview struct {
	ArticlePreview
	Sources []string `json:"sources"`
}

// ci spells each ASCII letter of a pattern as a two-case class: JavaScript's
// /i, which unlike (?i) does not match ſ for s or K (Kelvin) for k.
func ci(pattern string) string {
	var b strings.Builder
	for _, r := range pattern {
		if web.IsAlpha(r) {
			b.WriteString("[" + strings.ToLower(string(r)) + strings.ToUpper(string(r)) + "]")
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// paired is /<(a|b)\b[\s\S]*?<\/\1\s*>/gi, which RE2 has no backreference for.
func paired(tags ...string) *regexp.Regexp {
	parts := make([]string, len(tags))
	for i, tag := range tags {
		parts[i] = "<" + ci(tag) + `\b[\s\S]*?</` + ci(tag) + "[" + web.Space + "]*>"
	}
	return regexp.MustCompile(strings.Join(parts, "|"))
}

var (
	blockTag    = regexp.MustCompile(`</?` + ci(`(?:p|div|br|hr|li|ul|ol|dl|dt|dd|h[1-6]|blockquote|table|tr|td|th|section|article|header|footer|aside|nav|figure|figcaption|details|summary)`) + `\b[^>]*>`)
	hiddenText  = paired("script", "style", "pre", "button", "template", "noscript")
	uncounted   = paired("script", "style", "pre")
	asides      = paired("blockquote", "sup")
	codeHeader  = regexp.MustCompile(`<div class="reader-code-header">[\s\S]*?</div>`)
	comment     = regexp.MustCompile(`<!--[\s\S]*?-->`)
	anyTag      = regexp.MustCompile(`<[^>]*>`)
	spaces      = regexp.MustCompile(`[` + web.Space + `]+`)
	word        = regexp.MustCompile(`[\p{L}\p{N}]+`)
	imgTag      = regexp.MustCompile(`<` + ci("img") + `\b[^>]*>`)
	preTag      = regexp.MustCompile(`<` + ci("pre") + `\b`)
	imgStart    = regexp.MustCompile(`<` + ci("img") + `\b`)
	paragraph   = regexp.MustCompile(`<` + ci("p") + `\b[^>]*>([\s\S]*?)</` + ci("p") + `[` + web.Space + `]*>`)
	link        = regexp.MustCompile(`<` + ci("a") + `\b[^>]*>([\s\S]*?)</` + ci("a") + `[` + web.Space + `]*>`)
	toc         = regexp.MustCompile(`^\[\[?` + ci("toc") + `\]\]?$`)
	afterStop   = regexp.MustCompile(`([。！？，、；：」』）]) `)
	bareURL     = regexp.MustCompile(`https?://[^` + web.Space + `]+`)
	tinyClass   = regexp.MustCompile(`\b` + ci(`(?:emoji|wp-smiley|smiley|avatar|gravatar|icon|badge)`))
	trackerSrc  = regexp.MustCompile(ci(`(?:^|[/._?=-])(?:pixel|tracker|tracking|beacon|spacer|1x1|blank\.gif)(?:[/._?&=-]|$)|feeds\.feedburner\.com|stats\.wordpress\.com|gravatar\.com|/emoji/`))
	inlineImage = regexp.MustCompile(`^` + ci(`data:image/(?:png|jpe?g|gif|webp);base64,`))
	svgPath     = regexp.MustCompile(ci(`\.svg`) + `$`)
	attributes  = map[string]*regexp.Regexp{}
)

func init() {
	for _, name := range []string{"width", "height", "class", "data-src", "src"} {
		attributes[name] = regexp.MustCompile(`[` + web.Space + `]` + ci(name) + `[` + web.Space + `]*=[` + web.Space + `]*(?:"([^"]*)"|'([^']*)'|([^` + web.Space + `>]+))`)
	}
}

// page is a cached page with each data URL longer than 256 code units cut to "data:", so no pass reads megabytes
// of base64. A cut URL holds no quote, space or '>', so the tags and attributes found in html are the source's;
// at and cut map a position in html back to source: after the ith cut, its position in html and the bytes cut so far.
type page struct {
	source, html string
	at, cut      []int
	hid          bool // a cut took a '<' with it, which may have started a tag
}

// shorten is source.replace(/data:[^"'\s>]+/g, url => url.length > 256 ? 'data:' : url), keeping where it cut.
func shorten(source string) page {
	p := page{source: source, html: source}
	var b strings.Builder
	last := 0
	for i := 0; ; {
		k := strings.Index(source[i:], "data:")
		if k < 0 {
			break
		}
		start := i + k
		i = urlEnd(source, start+5)
		if !longer(source[start:i], 256) {
			continue
		}
		b.WriteString(source[last : start+5])
		p.at, p.cut = append(p.at, b.Len()), append(p.cut, i-b.Len())
		p.hid = p.hid || strings.IndexByte(source[start:i], '<') >= 0
		last = i
	}
	if len(p.at) > 0 {
		b.WriteString(source[last:])
		p.html = b.String()
	}
	return p
}

// urlStop marks the bytes that can end a run of /[^"'\s>]/: the ASCII ones and the first bytes of the other spaces.
var urlStop = func() (t [256]bool) {
	for _, r := range "\"'>\t\n\v\f\r \u00a0\u1680\u2000\u2028\u2029\u202f\u205f\u3000\ufeff" {
		t[string(r)[0]] = true
	}
	return t
}()

// urlEnd is where the run of /[^"'\s>]/ from i ends. A stop byte never continues a code point, so it is where the
// regexp would read a code point too.
func urlEnd(s string, i int) int {
	for i < len(s) {
		if !urlStop[s[i]] {
			i++
			continue
		}
		r, w := utf8.DecodeRuneInString(s[i:])
		if r < utf8.RuneSelf || web.JSSpace(r) {
			return i
		}
		i += w
	}
	return i
}

// orig is the position in source of position i in html.
func (p page) orig(i int) int {
	if k, _ := slices.BinarySearch(p.at, i+1); k > 0 {
		return i + p.cut[k-1]
	}
	return i
}

// imgTags are the page's <img> tags, as positions in the html of the page returned.
func (p page) imgTags() (page, [][]int) {
	if p.hid {
		p = page{source: p.source, html: p.source}
	}
	return p, imgTag.FindAllStringIndex(p.html, -1)
}

// plainText is the page's text without code, scripts or markup.
func plainText(html string) string {
	html = hiddenText.ReplaceAllString(html, " ")
	html = codeHeader.ReplaceAllString(html, " ")
	html = comment.ReplaceAllString(html, "")
	html = blockTag.ReplaceAllString(html, " ")
	html = anyTag.ReplaceAllString(html, "")
	return web.JSTrim(spaces.ReplaceAllString(web.Decode(html), " "))
}

// readingMinutes is the reader's rule: 400 Han characters or 230 words a minute, code excluded.
func readingMinutes(html string) int {
	html = uncounted.ReplaceAllString(html, " ")
	html = comment.ReplaceAllString(html, "")
	text := web.Decode(anyTag.ReplaceAllString(html, ""))
	han, words := 0, 0
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			han++
		}
	}
	for _, w := range word.FindAllString(text, -1) {
		if strings.IndexFunc(w, func(r rune) bool { return unicode.Is(unicode.Han, r) }) < 0 {
			words++
		}
	}
	return max(1, jsRound(float64(han)/400+float64(words)/230))
}

// jsRound is Math.round.
func jsRound(f float64) int {
	r := math.Floor(f)
	if f-r >= 0.5 {
		r++
	}
	return int(r)
}

func punctuation(r rune) bool { return web.JSSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) }

// fold is toLowerCase() with every /[\s\p{P}\p{S}]/u code point removed, in UTF-16.
func fold(u []uint16) []uint16 {
	rs := web.JSLower(web.CodePoints(u))
	out := rs[:0]
	for _, r := range rs {
		if !punctuation(r) {
			out = append(out, r)
		}
	}
	return web.EncodeUnits(out)
}

// withoutTitle drops a title the text starts with, however it is spaced or punctuated.
func withoutTitle(text, title string) string {
	target := fold(web.Units(title))
	if len(target) == 0 {
		return text
	}
	u := web.Units(text)
	for end := 1; end <= min(len(u), web.JSLen(title)*2+8); end++ {
		seen := fold(u[:end])
		if slices.Equal(seen, target) && !(end < len(u) && !isSurrogate(u[end]) && (unicode.IsLetter(rune(u[end])) || unicode.IsNumber(rune(u[end])))) {
			return strings.TrimLeftFunc(web.FromUnits(u[end:]), punctuation)
		}
		if len(seen) > len(target) || !slices.Equal(target[:len(seen)], seen) {
			break
		}
	}
	return text
}

func isSurrogate(c uint16) bool { return c >= 0xd800 && c < 0xe000 }

// excerpt is about the first limit code units of text, cut at a sentence end or a space.
func excerpt(text string, limit int) string {
	u := web.Units(text)
	if len(u) <= limit {
		return text
	}
	head := u[:limit+1]
	cut := -1
	for i, c := range head {
		stop := strings.ContainsRune("。！？!?…", rune(c)) || c == '.' && i+1 < len(head) && web.JSSpace(rune(head[i+1]))
		if stop && float64(i) >= float64(limit)*0.4 && i < limit {
			cut = i + 1
		}
	}
	if cut > 0 {
		for cut < len(head) && strings.ContainsRune("…」』”’\"')）", rune(head[cut])) {
			cut++
		}
		return web.FromUnits(head[:cut])
	}
	end := limit
	for i := len(head) - 1; i >= 0; i-- {
		if head[i] == ' ' {
			if float64(i) >= float64(limit)*0.6 {
				end = i
			}
			break
		}
	}
	rs := web.CodePoints(head[:end])
	for len(rs) > 0 && (web.JSSpace(rs[len(rs)-1]) || strings.ContainsRune(",，、;；:：-–—(（「『“‘/|·", rs[len(rs)-1])) {
		rs = rs[:len(rs)-1]
	}
	return web.FromUnits(web.EncodeUnits(rs)) + "…"
}

// attribute is the first value of the named attribute in the tag at html[tag[0]:tag[1]], read from source, decoded and trimmed.
func (p page) attribute(tag []int, name string) (string, bool) {
	m := attributes[name].FindStringSubmatchIndex(p.html[tag[0]:tag[1]])
	if m == nil {
		return "", false
	}
	for g := 1; g <= 3; g++ {
		if m[2*g] >= 0 {
			return web.JSTrim(web.Decode(p.source[p.orig(tag[0]+m[2*g]):p.orig(tag[0]+m[2*g+1])])), true
		}
	}
	return "", false
}

// pictures is up to limit picture sources in the page, skipping icons, trackers and repeats.
func pictures(p page, base string, limit int) []string {
	found := []string{}
	p, tags := p.imgTags()
	for n, tag := range tags {
		if len(found) >= limit {
			break
		}
		small := false
		for _, name := range []string{"width", "height"} {
			value, _ := p.attribute(tag, name)
			if size, ok := web.JSParseInt(value); ok && size > 0 && size <= 48 {
				small = true
			}
		}
		if small {
			continue
		}
		if class, _ := p.attribute(tag, "class"); tinyClass.MatchString(class) {
			continue
		}
		raw, _ := p.attribute(tag, "data-src")
		if raw == "" {
			raw, _ = p.attribute(tag, "src")
		}
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, "data:") {
			if inlineImage.MatchString(raw) && longer(raw, 400) {
				found = append(found, "inline:"+strconv.Itoa(n))
			}
			continue
		}
		u, err := web.ResolveURL(raw, base)
		if err != nil || u.Scheme != "http" && u.Scheme != "https" {
			continue
		}
		href := u.Href()
		if trackerSrc.MatchString(href) || svgPath.MatchString(u.Pathname()) || slices.Contains(found, href) {
			continue
		}
		found = append(found, href)
	}
	return found
}

// inlinePicture is the data URL behind an inline:<n> source, read back from the cached page.
func inlinePicture(html, source string) string {
	n := web.JSNumber(strings.TrimPrefix(source, "inline:"))
	p, tags := shorten(html).imgTags()
	if math.IsNaN(n) || n != math.Trunc(n) || n < 0 || n >= float64(len(tags)) {
		return ""
	}
	raw, _ := p.attribute(tags[int(n)], "data-src")
	if raw == "" {
		raw, _ = p.attribute(tags[int(n)], "src")
	}
	if strings.HasPrefix(raw, "data:") {
		return raw
	}
	return ""
}

func nonSpace(text string) int {
	n := 0
	for _, r := range text {
		if !web.JSSpace(r) {
			n++
			if r >= 0x10000 {
				n++
			}
		}
	}
	return n
}

func linkText(html string) int {
	n := 0
	for _, m := range link.FindAllStringSubmatch(html, -1) {
		n += nonSpace(plainText(m[1]))
	}
	return n
}

// leadText is the author's own paragraphs, so a table of contents, headings,
// callouts and language-switch links do not lead the excerpt.
func leadText(html string) (string, bool) {
	var kept []string
	for _, m := range paragraph.FindAllStringSubmatch(asides.ReplaceAllString(html, " "), -1) {
		if float64(linkText(m[1])) <= float64(nonSpace(plainText(m[1])))*0.6 {
			if text := plainText(m[1]); !toc.MatchString(text) {
				kept = append(kept, text)
			}
		}
	}
	text := web.JSTrim(afterStop.ReplaceAllString(strings.Join(kept, " "), "$1"))
	return text, nonSpace(text) >= 40
}

// workPictures is a work page's first screen then screenshots, the ones saved or still at their address.
func workPictures(w *GalleryWork) []string {
	shots := []string{}
	if truthy(w.Poster) || truthy(w.PosterSource.Value) {
		shots = append(shots, "work:poster")
	}
	for i, shot := range w.Sections {
		if truthy(shot.Image) || truthy(shot.Source.Value) {
			shots = append(shots, "work:"+strconv.Itoa(i))
		}
	}
	return shots
}

func truthy(s *string) bool { return s != nil && *s != "" }

// articlePreview is what the list can say about an article before it is opened.
func articlePreview(source, title, base string, summary bool, work *GalleryWork) storedPreview {
	// Cached pages inline their images; the text passes do not need megabytes of base64.
	short := shorten(source)
	html := short.html
	text := plainText(html)
	body := withoutTitle(text, title)
	visible := nonSpace(body)
	// A link to the original ("read the full post", a bare URL) is not body text.
	stub := nonSpace(bareURL.ReplaceAllString(body, "")) < 40 || visible < 200 && float64(linkText(html)) > float64(visible)*0.6
	var p storedPreview
	if work != nil {
		shots := workPictures(work)
		p.Sources, p.Images = shots[:min(4, len(shots))], len(shots)
	} else {
		p.Sources, p.Images = pictures(short, base, 4), len(imgStart.FindAllStringIndex(html, -1))
	}
	if !stub {
		lead, ok := leadText(html)
		if !ok {
			lead = body
		}
		e := excerpt(withoutTitle(lead, title), 140)
		p.Excerpt = &e
		if !summary {
			m := readingMinutes(html)
			p.Minutes = &m
		}
	}
	p.CodeBlocks = len(preTag.FindAllStringIndex(html, -1))
	p.HasImage, p.Gallery = len(p.Sources) > 0, len(p.Sources)
	return p
}
