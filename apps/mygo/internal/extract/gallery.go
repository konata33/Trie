package extract

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"trie/internal/web"

	"golang.org/x/net/html"
)

// Gallery pages: design-gallery entries (One Page Love and the like) are cards, not articles: a screen recording, the
// real site, a few labelled facts and section screenshots. The card is read into a work the reader lays out itself.

// Work is a gallery work as extraction leaves it: poster, video and section images are page addresses.
type Work struct {
	Name      string  `json:"name"`
	Designer  *string `json:"designer"`
	Site      *string `json:"site"`
	Host      *string `json:"host"`
	BuiltWith *string `json:"builtWith"`
	Facts     []Fact  `json:"facts"`
	Sections  []Shot  `json:"sections"`
	Poster    *string `json:"poster"`
	Video     *string `json:"video"`
}

type Fact struct {
	Label  string   `json:"label"`
	Values []string `json:"values"`
}

type Shot struct {
	Name  string  `json:"name"`
	Image *string `json:"image"`
}

type gallery struct {
	html string
	work Work
}

var (
	factLabels   = map[string]string{"tech": "技术", "typefaces": "字体", "typeface": "字体", "category": "类别", "style": "风格", "color": "配色", "colors": "配色", "features": "功能", "platform": "平台"}
	galleryQuote = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	srcsetEntry  = regexp.MustCompile(`([^\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+)` + sp + `+(\d+)w`)
	onSuffix     = regexp.MustCompile(sp + `+on` + sp + `+[^\n\r\x{2028}\x{2029}]+$`)
	referral     = regexp.MustCompile(`[?&]ref=onepagelove\b`)
	awardLine    = regexp.MustCompile(`^([^\n\r\x{2028}\x{2029}]+?)` + sp + `+-` + sp + `+(\w+ \d{1,2}, \d{4})$`)
	scoreNumber  = regexp.MustCompile(`\d+(?:\.\d+)?`)
	swatch       = regexp.MustCompile(`(?i)background:` + sp + `*(#[0-9a-f]{3,8})\b`)
	hexPrefix    = regexp.MustCompile(`(?i)^HEX` + sp + `*`)
	trackingKey  = regexp.MustCompile(`^(?:utm_|ref$)`)
	httpPrefix   = regexp.MustCompile(`^https?:`)
	blankVideo   = regexp.MustCompile(`/blank\.mp4$`)
	scoreKinds   = map[string]string{"design": "设计", "usability": "可用性", "creativity": "创意", "content": "内容"}
)

func textOfNode(n *html.Node) string {
	if n == nil {
		return ""
	}
	return textContent(n)
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// bestSource is the largest srcset candidate up to 1600 wide, else the first, else src or data-src; ok is false for none.
// Image CDN addresses contain commas, so candidates are read as "url width".
func bestSource(img *html.Node) (string, bool) {
	type candidate struct {
		url   string
		width float64
	}
	var cs []candidate
	for _, m := range srcsetEntry.FindAllStringSubmatch(attr(img, "srcset"), -1) {
		w, _ := strconv.ParseFloat(m[2], 64)
		cs = append(cs, candidate{strings.TrimPrefix(m[1], ","), w})
	}
	var fitting []candidate
	for _, c := range cs {
		if c.width <= 1600 {
			fitting = append(fitting, c)
		}
	}
	slices.SortStableFunc(fitting, func(a, b candidate) int { return int(b.width - a.width) })
	switch {
	case len(fitting) > 0:
		return fitting[0].url, true
	case len(cs) > 0:
		return cs[0].url, true
	}
	if v, ok := getAttr(img, "src"); ok {
		return v, true
	}
	return getAttr(img, "data-src")
}

// hostOf is a URL's hostname without www., or nil.
func hostOf(raw *string) *string {
	if raw == nil || *raw == "" {
		return nil
	}
	u, err := web.ParseURL(*raw, nil)
	if err != nil {
		return nil
	}
	return optional(strings.TrimPrefix(u.Hostname(), "www."))
}

func metaDescription(doc *html.Node) string {
	if v, ok := getAttr(query(doc, `meta[property="og:description"]`), "content"); ok {
		return collapse(v)
	}
	return collapse(attr(query(doc, `meta[name="description"]`), "content"))
}

func workBody(description, name string) string {
	return "<p>" + galleryQuote.Replace(cmpOr(description, name)) + "</p>"
}

func unique(values []string) []string {
	var out []string
	for _, v := range values {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func texts(nodes []*html.Node) []string {
	var out []string
	for _, n := range nodes {
		if s := collapse(textContent(n)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func onePageLove(doc *html.Node) *gallery {
	facts := []Fact{}
	for _, group := range queryAll(doc, ".inspo-links") {
		f := Fact{Label: collapse(textOfNode(query(group, ".link-type"))), Values: texts(queryAll(group, ".link-list a"))}
		if f.Label != "" && len(f.Values) > 0 {
			facts = append(facts, f)
		}
	}
	if len(facts) == 0 {
		return nil
	}
	// Features sit in their own tag rows: page sections first, then subject tags.
	if features := texts(queryAll(doc, ".inspo-features a")); len(features) > 0 {
		facts = append(facts, Fact{Label: "features", Values: unique(features)})
	}
	var site *string
	if href, ok := getAttr(query(doc, `a[title^="Visit "][href^="http"]`), "href"); ok {
		site = ptr(replaceFirst(referral, href, ""))
	}
	host := hostOf(site)
	// The entry's own recording; the related entries further down carry thumbnail videos of their own.
	recording := query(doc, ".review-screenshot video")
	sections := []Shot{}
	for _, item := range queryAll(doc, ".opl-sections__item") {
		img := query(item, "img")
		w, h := web.JSNumber(attr(img, "width")), web.JSNumber(attr(img, "height"))
		// A section cut from a short source screenshot comes back as a sliver (640×36) that shows nothing; leave it out.
		if truthy(w) && truthy(h) && h/w < 0.2 {
			continue
		}
		var name string
		if label, ok := getAttr(query(item, "a[aria-label]"), "aria-label"); ok {
			name = replaceFirst(onSuffix, label, "")
		} else if caption := query(item, "figcaption"); caption != nil {
			name = textContent(caption)
		} else {
			name = attr(img, "alt")
		}
		if img == nil {
			continue
		}
		if src, ok := bestSource(img); ok && src != "" && len(sections) < 12 {
			sections = append(sections, Shot{Name: collapse(name), Image: ptr(src)})
		}
	}
	designers := unique(texts(queryAll(doc, ".review-credits .credit-text")))
	name := cmpOr(collapse(textOfNode(query(doc, ".review-name h1"))), collapse(textOfNode(query(doc, "h1"))))
	for i := range facts {
		if l, ok := factLabels[web.FromUnits(web.EncodeUnits(web.JSLower([]rune(facts[i].Label))))]; ok {
			facts[i].Label = l
		}
	}
	w := Work{Name: name, Designer: optional(strings.Join(designers, "、")), Host: host, Facts: facts, Sections: sections,
		BuiltWith: optional(collapse(strings.Replace(textOfNode(query(doc, ".review-platform__title a")), "↗", "", 1)))}
	if site != nil && *site != "" && host != nil {
		w.Site = site
	}
	if v := attr(recording, "poster"); v != "" {
		w.Poster = &v
	} else {
		w.Poster = optional(attr(query(doc, `meta[property="og:image"]`), "content"))
	}
	if v := attr(recording, "src"); v != "" {
		w.Video = &v
	} else if recording != nil {
		w.Video = optional(attr(query(recording, "source[src]"), "src"))
	}
	return &gallery{html: workBody(metaDescription(doc), name), work: w}
}

// replaceFirst is String.prototype.replace with a regex that is not global.
func replaceFirst(re *regexp.Regexp, s, repl string) string {
	if loc := re.FindStringIndex(s); loc != nil {
		return s[:loc[0]] + repl + s[loc[1]:]
	}
	return s
}

func truthy(f float64) bool { return f != 0 && !math.IsNaN(f) }

// real is the first of the attributes holding a real address: Awwwards media are lazy, with the address in data-* and a
// placeholder (a data: image or blank.mp4) in src.
func real(n *html.Node, attributes ...string) *string {
	for _, a := range attributes {
		if v := attr(n, a); v != "" && httpPrefix.MatchString(v) && !blankVideo.MatchString(v) {
			return &v
		}
	}
	return nil
}

func awwwards(doc *html.Node) *gallery {
	head := query(doc, ".head-site")
	var name string
	if head != nil {
		name = collapse(textOfNode(query(head, "h1")))
	}
	if head == nil || name == "" {
		return nil
	}
	facts := []Fact{}
	if award := awardLine.FindStringSubmatch(collapse(textOfNode(query(head, ".c-heading__top")))); award != nil {
		facts = append(facts, Fact{Label: "荣誉", Values: []string{award[1] + " · " + awardDate(award[2])}})
	}
	// The first chart is the jury's; a second one holds the developer review.
	var kinds, marks []string
	if chart := query(doc, ".layout-overall"); chart != nil {
		for _, t := range queryAll(chart, ".layout-overall__type") {
			first := ""
			if t.FirstChild != nil {
				first = textContent(t.FirstChild)
			}
			kinds = append(kinds, scoreKinds[web.FromUnits(web.EncodeUnits(web.JSLower([]rune(collapse(first)))))])
		}
		for _, s := range queryAll(chart, ".layout-overall__score") {
			marks = append(marks, scoreNumber.FindString(textContent(s)))
		}
	}
	var score []string
	if overall := collapse(textOfNode(query(head, ".box-score__note strong"))); overall != "" {
		score = append(score, "总分 "+overall)
	}
	for i, kind := range kinds {
		if kind != "" && i < len(marks) && marks[i] != "" {
			score = append(score, kind+" "+marks[i])
		}
	}
	if len(score) > 0 {
		facts = append(facts, Fact{Label: "评分", Values: score})
	}
	// Categories, tags and technologies are separate template loops into one list (nominees lead with colour swatches);
	// only the indentation each loop leaves before its items tells them apart.
	color := func(item *html.Node) string {
		if m := swatch.FindStringSubmatch(attr(query(item, `a[class*="tag--color"]`), "style")); m != nil {
			return m[1]
		}
		return ""
	}
	var swatches []string
	var items []*html.Node
	for _, li := range queryAll(doc, ".list-tags > li") {
		if c := color(li); c != "" {
			swatches = append(swatches, c)
		} else {
			items = append(items, li)
		}
	}
	gaps := make([]string, len(items))
	for i, item := range items {
		if p := item.PrevSibling; p != nil && p.Type == html.TextNode {
			gaps[i] = p.Data
		}
	}
	count := func(g string) int {
		n := 0
		for _, x := range gaps {
			if x == g {
				n++
			}
		}
		return n
	}
	loop := ""
	if len(gaps) > 0 {
		loop = gaps[0]
		for _, g := range gaps {
			if count(g) > count(loop) {
				loop = g
			}
		}
	}
	var groups [][]string
	for i, item := range items {
		if i == 0 || gaps[i] != loop {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], collapse(textContent(item)))
	}
	// ponytail: any other grouping (minified markup, an empty loop) is shown as plain tags rather than guessed at.
	if len(groups) == 3 {
		for i, values := range groups {
			facts = append(facts, Fact{Label: []string{"类别", "标签", "技术"}[i], Values: values})
		}
	} else if len(items) > 0 {
		facts = append(facts, Fact{Label: "标签", Values: slices.Concat(groups...)})
	}
	var palette []string
	for _, n := range queryAll(doc, ".list-palette__name") {
		if v := replaceFirst(hexPrefix, collapse(textContent(n)), ""); v != "" {
			palette = append(palette, v)
		}
	}
	if len(palette) > 0 {
		facts = append(facts, Fact{Label: "配色", Values: palette})
	} else if len(swatches) > 0 {
		facts = append(facts, Fact{Label: "配色", Values: swatches})
	}
	visit := real(query(doc, `a[data-controller~="visit-count"]`), "href")
	if visit == nil {
		visit = real(query(head, "h1 a"), "href")
	}
	var site *string
	if visit != nil {
		site = withoutTracking(*visit)
	}
	// Elements are the site's own clips (their static frames are kept); Inside look holds full-page screenshots.
	sections := []Shot{}
	for _, card := range queryAll(doc, ".gallery-site--two-cols .card-slide") {
		media := query(card, "video, img")
		var src *string
		if htmlElement(media, "video") {
			src = real(media, "data-poster", "poster")
		} else {
			src = real(media, "data-src", "src")
		}
		label := collapse(attr(query(card, "a[aria-label]"), "aria-label"))
		if label == "" {
			label = collapse(textOfNode(query(card, ".card-slide__data a")))
		}
		if src != nil && len(sections) < 12 {
			sections = append(sections, Shot{Name: label, Image: src})
		}
	}
	recording := query(head, ".gallery-site video")
	poster := real(recording, "data-poster", "poster")
	if poster == nil {
		poster = real(query(head, ".gallery-site img"), "data-src", "src")
	}
	if poster == nil {
		poster = real(query(doc, `meta[property="og:image"]`), "content")
	}
	var video *string
	if recording != nil {
		video = real(query(recording, "source"), "data-src", "src")
	}
	if video == nil {
		video = real(recording, "data-src", "src")
	}
	credits := queryAll(head, ".head-site__credits .avatar-name__name strong")
	return &gallery{html: workBody(cmpOr(metaDescription(doc), collapse(textOfNode(query(doc, "#details .c-heading__middle")))), name), work: Work{
		Name: name, Sections: sections, Designer: optional(strings.Join(unique(texts(credits)), "、")), Site: site, Host: hostOf(site),
		Facts: facts, Poster: poster, Video: video,
	}}
}

// awardDate is the award's date as new Date(text) reads "Nov 5, 2026" (local time, the month by its first three
// letters), written year-month-day; text as it is when that is no date.
func awardDate(text string) string {
	var monthName string
	var day, year int
	if _, err := fmt.Sscanf(text, "%s %d, %d", &monthName, &day, &year); err != nil || day < 1 || day > 31 {
		return text
	}
	months := []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
	m := slices.Index(months, strings.ToLower(monthName[:min(3, len(monthName))]))
	if m < 0 || len(monthName) < 3 {
		return text
	}
	t := time.Date(year, time.Month(m+1), day, 0, 0, 0, 0, time.Local)
	return fmt.Sprintf("%d-%02d-%02d", t.Year(), int(t.Month()), t.Day())
}

// withoutTracking is the address without utm_* and ref parameters, as url.searchParams.delete leaves it.
func withoutTracking(raw string) *string {
	u, err := web.ParseURL(raw, nil)
	if err != nil {
		return nil
	}
	href := u.Href()
	rest, fragment, hasFragment := strings.Cut(href, "#")
	address, query, hasQuery := strings.Cut(rest, "?")
	if !hasQuery {
		return &href
	}
	type pair struct{ name, value string }
	var pairs []pair
	changed := false
	for _, part := range strings.Split(query, "&") {
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		p := pair{formDecode(name), formDecode(value)}
		pairs = append(pairs, p)
	}
	var kept []pair
	for _, p := range pairs {
		if trackingKey.MatchString(p.name) {
			changed = true
		} else {
			kept = append(kept, p)
		}
	}
	if !changed {
		return &href
	}
	var b strings.Builder
	for i, p := range kept {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(formEncode(p.name) + "=" + formEncode(p.value))
	}
	out := address
	if b.Len() > 0 {
		out += "?" + b.String()
	}
	if hasFragment {
		out += "#" + fragment
	}
	return &out
}

func formDecode(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			v, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
			b = append(b, byte(v))
			i += 2
		} else {
			b = append(b, s[i])
		}
	}
	return nodeUTF8(b)
}

func isHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

// formEncode is the application/x-www-form-urlencoded serializer.
func formEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c == ' ':
			b.WriteByte('+')
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '*' || c == '-' || c == '.' || c == '_':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// galleryPage is the gallery's own adapter by host; any other page is read the One Page Love way, which finds nothing on
// ordinary posts.
func galleryPage(doc *html.Node, pageURL string) *gallery {
	if h := hostOf(&pageURL); h != nil && *h == "awwwards.com" {
		return awwwards(doc)
	}
	return onePageLove(doc)
}

var (
	moreLink    = regexp.MustCompile(`(?i)^(?:full review|read (?:the )?(?:full|more)|continue reading|keep reading|阅读全文|继续阅读|查看全文|阅读原文)`)
	scriptBlock = regexp.MustCompile(`(?i)<script\b[\s\S]*?</script` + sp + `*>|<style\b[\s\S]*?</style` + sp + `*>`)
	anyTag      = regexp.MustCompile(`<[^>]+>`)
	anyEntity   = regexp.MustCompile(`(?i)&[a-z#0-9]+;`)
	anchor      = regexp.MustCompile(`(?i)<a\b[^>]*>([\s\S]*?)</a` + sp + `*>`)
)

// IsTeaser is whether page is a feed body that is only a lead-in, a short text ending in a link to the full
// piece ("Full Review", "Read more", 阅读全文). Design galleries publish entries this way.
func IsTeaser(page string) bool {
	text := collapse(anyEntity.ReplaceAllString(anyTag.ReplaceAllString(scriptBlock.ReplaceAllString(page, " "), " "), " "))
	if web.JSLen(text) >= 600 {
		return false
	}
	for _, m := range anchor.FindAllStringSubmatch(page, -1) {
		if moreLink.MatchString(web.JSTrim(anyTag.ReplaceAllString(m[1], ""))) {
			return true
		}
	}
	return false
}
