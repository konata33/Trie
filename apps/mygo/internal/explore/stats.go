package explore

import (
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"trie/internal/library"
	"trie/internal/web"
)

// Post is ExplorePost: one of an entry's latest posts, or a gallery's work.
type Post struct {
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	PublishedAt *string `json:"publishedAt"`
	// The opening of the post's text from its feed, plain and short; "" when the feed carries no text.
	Excerpt string `json:"excerpt,omitempty"`
	// What a gallery's work is shelved under (Shelf.Kind): landing, portfolio, motion, type, brand.
	Kinds []string `json:"kinds,omitempty"`
}

// work is a design gallery's latest featured site and the picture its feed shows of it.
type work struct {
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	PublishedAt *string `json:"publishedAt"`
	Image       string  `json:"image"`
}

// featuredRecord is one of FeedStats' sites: a website a gallery's item features.
type featuredRecord struct {
	URL         string  `json:"url"`
	Title       string  `json:"title"`
	PublishedAt *string `json:"publishedAt"`
}

// pickRecord is one of FeedStats' picks: a Pick without its feed entry. Thin is set while the feed gives less than an
// article and the page has not been read yet, Unreadable when the post's page gave no readable text either; neither is
// shown. lookForOpenings changes Excerpt, Thin and Unreadable in place, under Service.mu.
type pickRecord struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Author      string   `json:"author"`
	HomeURL     string   `json:"homeURL"`
	PublishedAt *string  `json:"publishedAt"`
	Excerpt     string   `json:"excerpt"`
	Pictures    []string `json:"pictures"`
	Thin        bool     `json:"thin,omitempty"`
	Unreadable  bool     `json:"unreadable,omitempty"`
}

// stats is a feed's stats, as explore-cache.json and seed.json keep them. A nil slice or map is a field the stored
// object does not have (works of stats saved before works were kept), an empty one a field it has empty. Error is set
// while the latest read failed; the posts are then the last good ones.
type stats struct {
	At       float64             `json:"at"`
	Posts    []Post              `json:"posts"`
	Last90   *int                `json:"last90"`
	Recent30 *int                `json:"recent30"`
	Works    []work              `json:"works,omitzero"`
	V        int                 `json:"v,omitempty"`
	Error    string              `json:"error,omitempty"`
	Pictures map[string][]string `json:"pictures,omitzero"`
	Picks    []*pickRecord       `json:"picks,omitzero"`
	Sites    []featuredRecord    `json:"sites,omitzero"`
}

const day = 86_400_000

var (
	anyTag   = regexp.MustCompile(`<[^>]+>`)
	spaces   = regexp.MustCompile(`[` + web.Space + `]+`)
	nonSpace = `[^` + web.Space + `]`
	space    = `[` + web.Space + `]`

	firstImg   = regexp.MustCompile(`<` + ci("img") + `\b[^>]*?` + space + ci("src") + `=["']([^"']+)["']`)
	imgTag     = regexp.MustCompile(`<` + ci("img") + `\b[^>]*>`)
	srcAttr    = regexp.MustCompile(space + ci("src") + `=["']([^"']+)["']`)
	widthAttr  = regexp.MustCompile(space + ci("width") + `=["']?([0-9]+)`)
	smallClass = regexp.MustCompile(space + ci("class") + `=["'][^"']*\b(?:` + ci("emoji") + `|` + ci("avatar") + `|` + ci("icon") + `|` + ci("logo") + `)\b`)
	notPicture = regexp.MustCompile(`\.` + ci("svg") + `(?:$|[?#])|/` + ci("ads") + `?/|` + ci("avatar") + `|` + ci("gravatar") + `|` + ci("emoji") + `|` + ci("badge") + `|` + ci("sprite") + `|` + ci("spacer"))

	anchorHref = regexp.MustCompile(`<` + ci("a") + `\b[^>]*?` + space + ci("href") + `=["']([^"']+)["']`)
	// Links that lead to a gallery's social accounts or services rather than to the site it features.
	elsewhere = regexp.MustCompile(`(?:^|\.)(?:twitter|x|instagram|facebook|linkedin|pinterest|dribbble|behance|youtube|vimeo|tiktok|threads|bsky|medium|patreon|feedburner)\.(?:com|net|app|social)$|^wp\.me$`)
	fileLink  = regexp.MustCompile(`\.(?:` + ci("jpe") + `?` + ci("g") + `|` + ci("png") + `|` + ci("gif") + `|` + ci("webp") + `|` + ci("svg") + `|` + ci("pdf") + `)$`)

	// /<(script|style|figure|pre)\b[\s\S]*?<\/\1>/gi, which RE2 has no backreference for.
	hiddenBlocks  = paired("script", "style", "figure", "pre")
	numericEntity = regexp.MustCompile(`&#([0-9]+);`)
	byline        = regexp.MustCompile(`^` + space + `*` + ci("by") + space + `+` + nonSpace + `+` + space + `+` + ci("from") + space + `+` + nonSpace + `+`)
	bareLink      = regexp.MustCompile(`https?://` + nonSpace + `+`)
	emphasis      = regexp.MustCompile(`\*\*|__`)
	credit        = regexp.MustCompile(`©|` + ci("license:") + `|` + ci("source:"))
	// ponytail: .{1,200}? counts code points where JavaScript counts UTF-16 units, so a title of over 100 emoji differs.
	footer      = regexp.MustCompile(space + `*(?:` + ci("this post was originally published ") + `(?:` + ci("at") + `|` + ci("on") + `)|` + ci("the post ") + `.{1,200}?` + ci(" appeared first on") + `) [^.]*\.?$`)
	notice      = regexp.MustCompile(`转载|版权所有|商用请|©|` + ci("all rights reserved") + `|` + ci("license:"))
	sentenceEnd = regexp.MustCompile(`[。！？!?]|\.` + space)
)

// ci spells each ASCII letter of a pattern as a two-case class: JavaScript's /i, which unlike (?i) does not match ſ for
// s or K (Kelvin) for k (as library/preview.go spells it).
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

// paired is /<(a|b)\b[\s\S]*?<\/\1>/gi as one alternative per tag.
func paired(tags ...string) *regexp.Regexp {
	parts := make([]string, len(tags))
	for i, tag := range tags {
		parts[i] = "<" + ci(tag) + `\b[\s\S]*?</` + ci(tag) + ">"
	}
	return regexp.MustCompile(strings.Join(parts, "|"))
}

// host is an address's host without www., lowercase; "" when it is no address.
func host(value string) string {
	u, err := web.ParseURL(value, nil)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
}

func httpURL(u *web.URL) bool { return u.Scheme == "http" || u.Scheme == "https" }

// base is where an entry's body resolves: entry.feedBody?.baseURL || entry.url.
func base(e library.FeedEntry) string {
	if e.FeedBody != nil && e.FeedBody.BaseURL != nil && *e.FeedBody.BaseURL != "" {
		return *e.FeedBody.BaseURL
	}
	return e.URL
}

func bodyOf(e library.FeedEntry) string {
	if e.FeedBody == nil {
		return ""
	}
	return e.FeedBody.HTML
}

// attached is entry.image when it is a non-empty string.
func attached(e library.FeedEntry) string {
	if e.Image.Value == nil {
		return ""
	}
	return *e.Image.Value
}

// firstImage is the first picture of a feed entry: the one its feed attaches, else the first in its body, as an
// absolute http(s) address.
func firstImage(e library.FeedEntry) *string {
	if img := attached(e); img != "" {
		return &img
	}
	m := firstImg.FindStringSubmatch(bodyOf(e))
	if m == nil {
		return nil
	}
	u, err := web.ResolveURL(strings.ReplaceAll(m[1], "&amp;", "&"), base(e))
	if err != nil || !httpURL(u) {
		return nil
	}
	href := u.Href()
	return &href
}

// postPictures is a post's own pictures, best first, to stand behind it in Explore: the picture its feed attaches,
// then those in its body. Drawings (SVG), advertisements, avatars, emoji and pictures marked small are left out; the
// shell also drops any narrower than 600px when it fetches them.
func postPictures(e library.FeedEntry) []string {
	srcs := []string{attached(e)}
	for _, tag := range imgTag.FindAllString(bodyOf(e), -1) {
		m := srcAttr.FindStringSubmatch(tag)
		if m == nil {
			continue
		}
		width := 0.0
		if w := widthAttr.FindStringSubmatch(tag); w != nil {
			width, _ = strconv.ParseFloat(w[1], 64)
		}
		if width != 0 && width < 300 || smallClass.MatchString(tag) {
			continue
		}
		srcs = append(srcs, strings.ReplaceAll(m[1], "&amp;", "&"))
	}
	out := []string{}
	for _, src := range srcs {
		if src == "" {
			continue
		}
		u, err := web.ResolveURL(src, base(e))
		if err != nil || !httpURL(u) || notPicture.MatchString(u.Href()) || slices.Contains(out, u.Href()) {
			continue
		}
		if out = append(out, u.Href()); len(out) == 3 {
			break
		}
	}
	return out
}

// featuredSite is the website a gallery's item features, when the item names it: the first link in its body that
// leaves the gallery and is not one of its accounts.
func featuredSite(e library.FeedEntry, galleryHome string) *string {
	own := map[string]bool{host(galleryHome): true, host(e.URL): true}
	for _, m := range anchorHref.FindAllStringSubmatch(bodyOf(e), -1) {
		u, err := web.ResolveURL(strings.ReplaceAll(m[1], "&amp;", "&"), base(e))
		if err != nil {
			continue
		}
		site := host(u.Href())
		if httpURL(u) && site != "" && !own[site] && !elsewhere.MatchString(site) && !fileLink.MatchString(u.Pathname()) {
			href := u.Href()
			return &href
		}
	}
	return nil
}

// excerpt is the opening of an entry's text from its feed, as plain text short enough for a front page.
func excerpt(e library.FeedEntry) string {
	html := bodyOf(e)
	if html == "" {
		return ""
	}
	text := anyTag.ReplaceAllString(hiddenBlocks.ReplaceAllString(html, " "), " ")
	for _, r := range [][2]string{{"&nbsp;", " "}, {"&lt;", "<"}, {"&gt;", ">"}, {"&quot;", `"`}, {"&#39;", "'"}, {"&apos;", "'"}} {
		text = strings.ReplaceAll(text, r[0], r[1])
	}
	text = numericEntity.ReplaceAllStringFunc(text, func(m string) string {
		n, err := strconv.ParseUint(m[2:len(m)-1], 10, 32)
		if err != nil || n > utf8.MaxRune {
			// ponytail: a code point past U+10FFFF is no character; one replacement character stands for it
			// rather than failing the feed's read.
			return "�"
		}
		return string(rune(n))
	})
	text = strings.ReplaceAll(text, "&amp;", "&")
	// Feed boilerplate a reader would skip: a byline with the post's own address, bare links, Markdown emphasis.
	text = emphasis.ReplaceAllString(bareLink.ReplaceAllString(byline.ReplaceAllString(text, " "), " "), "")
	// Picture credits a feed repeats through the text: "© Studio . License: All Rights Reserved . Source: site.com".
	text = web.JSTrim(spaces.ReplaceAllString(dropCredits(text), " "))
	// A syndication footer closing a short post: "This post was originally published at X", "The post X appeared first on Y."
	if loc := footer.FindStringIndex(text); loc != nil {
		text = text[:loc[0]] + text[loc[1]:]
	}
	// Credits and reprint or copyright notices opening the post: drop everything up to the end of the last such sentence near the start.
	if all := notice.FindAllStringIndex(text[:unitsPrefix(text, 160)], -1); len(all) > 0 {
		rest := text[all[len(all)-1][1]:]
		if loc := sentenceEnd.FindStringIndex(rest); loc != nil {
			_, n := utf8.DecodeRuneInString(rest[loc[0]:])
			text = web.JSTrim(rest[loc[0]+n:])
		}
	}
	if text == "" || text == e.Title {
		return ""
	}
	if web.JSLen(text) > 1200 {
		return web.FromUnits(web.Units(text)[:1200]) + "…"
	}
	return text
}

// dropCredits is .replace(/(?:©|license:|source:)[^。]{0,80}?[.。](?=\s|$)/gi, ' '), whose lookahead RE2 does not
// have: a credit runs to the first full stop followed by a space or the end, within 80 UTF-16 units and no 。 before it.
func dropCredits(text string) string {
	var b strings.Builder
	last := 0
	for pos := 0; pos < len(text); {
		loc := credit.FindStringIndex(text[pos:])
		if loc == nil {
			break
		}
		start := pos + loc[0]
		if stop, ok := creditEnd(text, pos+loc[1]); ok {
			b.WriteString(text[last:start] + " ")
			last, pos = stop, stop
			continue
		}
		_, n := utf8.DecodeRuneInString(text[start:])
		pos = start + n
	}
	return b.String() + text[last:]
}

func creditEnd(text string, i int) (int, bool) {
	units := 0
	for j := i; j < len(text); {
		r, n := utf8.DecodeRuneInString(text[j:])
		if r == '.' || r == '。' {
			if j+n == len(text) {
				return j + n, true
			}
			if next, _ := utf8.DecodeRuneInString(text[j+n:]); web.JSSpace(next) {
				return j + n, true
			}
		}
		if r == '。' {
			return 0, false
		}
		if units += utf16Len(r); units > 80 {
			return 0, false
		}
		j += n
	}
	return 0, false
}

func utf16Len(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// unitsPrefix is how many bytes of text its first n UTF-16 units take (text.slice(0, n)), without half a pair.
func unitsPrefix(text string, n int) int {
	units := 0
	for i, r := range text {
		if units += utf16Len(r); units > n {
			return i
		}
	}
	return len(text)
}

// plain is html's text with its tags and spacing gone.
func plain(html string) string {
	return web.JSTrim(spaces.ReplaceAllString(strings.ReplaceAll(anyTag.ReplaceAllString(html, " "), "&nbsp;", " "), " "))
}

// dated is when a feed entry was published: its publishedAt, else modifiedAt (many Atom feeds carry only <updated>).
func dated(e library.FeedEntry) *string {
	if e.PublishedAt != nil && *e.PublishedAt != "" {
		return e.PublishedAt
	}
	return e.ModifiedAt
}

// parse is Date.parse(at) for a value that may be absent: NaN for none.
func parse(at *string) float64 {
	if at == nil || *at == "" {
		return math.NaN()
	}
	if ms, ok := web.JSDateParse(*at); ok {
		return float64(ms)
	}
	return math.NaN()
}

// feedStats is the latest ten posts (an author's page in Explore lists them), how many appeared
// in the last 90 / 30 days, and the newest entries that carry a picture. Counts are nil when no entry is dated.
func feedStats(feed library.ParsedFeed, now int64, gallery bool) stats {
	type item struct {
		a  library.FeedEntry
		at *string
		t  float64
	}
	sorted := make([]item, len(feed.Articles))
	var known []float64
	for i, a := range feed.Articles {
		sorted[i] = item{a, dated(a), parse(dated(a))}
		if t := sorted[i].t; !math.IsNaN(t) && t <= float64(now+day) {
			known = append(known, t)
		}
	}
	key := func(t float64) float64 {
		if math.IsNaN(t) {
			return math.Inf(-1)
		}
		return t
	}
	sort.SliceStable(sorted, func(i, j int) bool { return key(sorted[i].t) > key(sorted[j].t) })
	within := func(days int64) *int {
		if len(known) == 0 {
			return nil
		}
		n := 0
		for _, t := range known {
			if t >= float64(now-days*day) {
				n++
			}
		}
		return &n
	}
	out := stats{Posts: []Post{}, Last90: within(90), Recent30: within(30), Works: []work{}, Pictures: map[string][]string{}}
	// An author's page lists as many as the feed gives, up to feedKept; the rooms show the newest few.
	for _, d := range sorted[:min(feedKept, len(sorted))] {
		if img := firstImage(d.a); img != nil {
			out.Works = append(out.Works, work{Title: d.a.Title, URL: d.a.URL, PublishedAt: d.at, Image: *img})
		}
	}
	for _, d := range sorted[:min(feedKept, len(sorted))] {
		out.Posts = append(out.Posts, Post{Title: d.a.Title, URL: d.a.URL, PublishedAt: d.at, Excerpt: excerpt(d.a)})
		if found := postPictures(d.a); len(found) > 0 {
			out.Pictures[d.a.URL] = found
		}
	}
	// A gallery's featured websites, each once, from its latest twenty items.
	if gallery {
		out.Sites = []featuredRecord{}
		seen := map[string]bool{}
		for _, d := range sorted[:min(20, len(sorted))] {
			if url := featuredSite(d.a, feed.HomeURL); url != nil && !seen[host(*url)] {
				seen[host(*url)] = true
				out.Sites = append(out.Sites, featuredRecord{URL: *url, Title: d.a.Title, PublishedAt: d.at})
			}
		}
	}
	return out
}
