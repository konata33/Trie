// Package feed is apps/desktop/src/main/subscription-feed.ts: it reads an
// RSS, RDF or Atom document into the ParsedFeed the library imports, and
// finds the feeds and icons a home page offers.
package feed

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/web"
)

// CanonicalFeedURL is canonicalFeedURL: a public address without www., a
// trailing slash or a fragment, the key one article goes by.
func CanonicalFeedURL(value string) (string, error) {
	u, err := fetch.PublicURL(value)
	if err != nil {
		return "", err
	}
	return web.URLKey(u.Href())
}

// address is a value resolved against base, if that is a public address.
func address(value, base string) *string {
	value = web.JSTrim(value)
	if value == "" {
		return nil
	}
	u, err := web.ResolveURL(value, base)
	if err != nil {
		return nil
	}
	if u, err = fetch.PublicURL(u.Href()); err != nil {
		return nil
	}
	href := u.Href()
	return &href
}

func or(p *string, fallback string) string {
	if p != nil {
		return *p
	}
	return fallback
}

// baseOf is the xml:base of the element and its ancestors, applied from the root down.
func baseOf(n *node, fallback string) string {
	var chain []*node
	for e := n; e != nil && e.kind == elementNode; e = e.parent {
		chain = append([]*node{e}, chain...)
	}
	for _, e := range chain {
		if v, ok := e.get("xml:base"); ok {
			fallback = or(address(v, fallback), fallback)
		}
	}
	return fallback
}

// alternate is an Atom element's first link without rel or with rel="alternate".
func alternate(n *node) *node {
	for _, l := range children(n, "link") {
		if rel, ok := l.get("rel"); !ok || rel == "" || rel == "alternate" {
			return l
		}
	}
	return nil
}

func atomLink(n *node, base string) *string {
	if link := alternate(n); link != nil {
		return address(link.attr("href"), baseOf(link, base))
	}
	return nil
}

var http = regexp.MustCompile(`^https?://`)

// identifierLink is a guid or id that is itself an http(s) address.
func identifierLink(value, base string) *string {
	if value != "" && http.MatchString(lower(web.JSTrim(value))) {
		return address(value, base)
	}
	return nil
}

// sectionLink is a link to one part of a page (tonsky.me/design/#2026-07-squawkbox is one logo of a portfolio):
// addresses drop the fragment, so it is kept as the body's base, where the reader sees the entry is that part,
// complete as the feed gives it.
func sectionLink(raw *string, base, url string) *string {
	if raw == nil {
		return nil
	}
	link, err := web.ResolveURL(web.JSTrim(*raw), base)
	if err != nil {
		return nil
	}
	href := link.Href()
	if i := strings.IndexByte(href, '#'); i < 0 || i == len(href)-1 {
		return nil
	}
	if a := address(href, base); a == nil || *a != url {
		return nil
	}
	return &href
}

var (
	boilerplate = regexp.MustCompile(`^(recent content|latest (posts|articles)|rss|atom|feed)\b|\b(rss|atom) feed\b|powered by|^(the )?(blog|posts) of\b`)
	ownBlog     = regexp.MustCompile(`^.{0,40}['’]s?[` + web.Space + `]*(personal[` + web.Space + `]+)?(blog|网络日志|博客)\.?$`)
	lastWord    = regexp.MustCompile(`[` + web.Space + `][^` + web.Space + `]*$`)
)

// SelfDescription is selfDescription: what a feed says about its author, nil
// for generator boilerplate ("Recent content on …", "RSS feed for …") and for
// a repeat of the title.
func SelfDescription(text, title string) *string {
	value := web.JSTrim(spaces.ReplaceAllString(text, " "))
	low := lower(value)
	if web.JSLen(value) < 4 || string(web.JSLower([]rune(value))) == string(web.JSLower([]rune(title))) || boilerplate.MatchString(low) || ownBlog.MatchString(units16(low)) {
		return nil
	}
	if web.JSLen(value) > 160 {
		value = lastWord.ReplaceAllString(units(value, 158), "") + "…"
	}
	return &value
}

// units16 spells each character outside the BMP as two private-use
// characters, so that . counts UTF-16 code units as in JavaScript.
func units16(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r > 0xffff {
			b.WriteString("\uE000\uE000")
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var gallery = regexp.MustCompile(`^(?:website|landing page|portfolio|template) inspiration:[` + web.Space + `]+`)

var (
	errTooBig   = errors.New("订阅源过大或包含不支持的 XML 声明")
	doctype     = regexp.MustCompile(`<!doctype|<!entity`)
	ansi        = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	controls    = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f]`)
	errNotFeed  = errors.New("这个地址不是 RSS 或 Atom 订阅源")
	errHuge     = errors.New("订阅源结构过大")
	errDeep     = errors.New("订阅源嵌套过深")
	errChannel  = errors.New("订阅源缺少频道信息")
	errTooMany  = errors.New("单次订阅返回超过 1000 条文章，请使用较小的订阅源")
	textEscaped = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
)

// Parse is parseFeed: the feed at feedURL, whose document is xml.
func Parse(xml, feedURL string) (library.ParsedFeed, error) {
	u, err := fetch.PublicURL(feedURL)
	if err != nil {
		return library.ParsedFeed{}, err
	}
	feedURL = u.Href()
	if len(xml) > 5*1024*1024 || doctype.MatchString(lower(xml)) {
		return library.ParsedFeed{}, errTooBig
	}
	cleaned := controls.ReplaceAllString(ansi.ReplaceAllString(xml, ""), "")
	warnings := []string{}
	if cleaned != xml {
		warnings = append(warnings, "订阅源包含无效字符，已清理后读取")
	}
	root, count, deepest, err := parseXML(cleaned)
	if err != nil {
		return library.ParsedFeed{}, err
	}
	kind := strings.ToLower(root.local)
	switch {
	case kind != "rss" && kind != "rdf" && kind != "feed":
		return library.ParsedFeed{}, errNotFeed
	case count > 50000:
		return library.ParsedFeed{}, errHuge
	case deepest > 80:
		return library.ParsedFeed{}, errDeep
	}
	atom := kind == "feed"
	channel := root
	if !atom {
		channel = child(root, "channel")
	}
	if channel == nil {
		return library.ParsedFeed{}, errChannel
	}
	channelBase := baseOf(channel, feedURL)
	var home *string
	if atom {
		home = atomLink(channel, feedURL)
	} else {
		home = address(child(channel, "link").textContent(), channelBase)
	}
	origin, _ := web.ParseURL(feedURL, nil)
	homeURL := or(home, origin.Scheme+"://"+origin.Hostname()+"/")
	homeParsed, _ := web.ParseURL(homeURL, nil)
	title := plain(child(channel, "title").textContent())
	if title == "" {
		title = homeParsed.Hostname()
	}
	about := "description"
	if atom {
		about = "subtitle"
	}
	description := SelfDescription(plain(child(channel, about).textContent()), title)
	var items []*node
	switch kind {
	case "feed":
		items = children(channel, "entry")
	case "rdf":
		items = children(root, "item")
	default:
		items = children(channel, "item")
	}
	if len(items) > 1000 {
		return library.ParsedFeed{}, errTooMany
	}
	articles := []library.FeedEntry{}
	seen := map[string]bool{}
	skipped := 0
	for _, item := range items {
		base := baseOf(item, feedURL)
		var url *string
		if atom {
			if url = atomLink(item, feedURL); url == nil {
				url = identifierLink(child(item, "id").textContent(), base)
			}
		} else if url = address(child(item, "link").textContent(), base); url == nil {
			url = identifierLink(child(item, "guid").textContent(), base)
		}
		if url == nil {
			skipped++
			continue
		}
		id, err := CanonicalFeedURL(*url)
		if err != nil {
			return library.ParsedFeed{}, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		var people []*node
		if atom {
			if people = children(item, "author"); len(people) == 0 {
				people = children(channel, "author")
			}
		} else {
			people = append(children(item, "creator"), children(item, "author")...)
		}
		authors := []string{}
		for _, a := range people {
			text := child(a, "name").textContent()
			if text == "" {
				text = a.textContent()
			}
			if name := plain(text); name != "" && !contains(authors, name) {
				authors = append(authors, name)
			}
		}
		body, summary := child(item, "encoded"), child(item, "description")
		if atom {
			body, summary = child(item, "content"), child(item, "summary")
		}
		content := body
		if content == nil {
			content = summary
		}
		html := content.textContent()
		// XHTML content, or markup some feeds (Land-book) write straight into <description> without escaping it.
		if content != nil && (content.attr("type") == "xhtml" || !atom && content.elements() > 0) {
			html = content.innerXML()
		}
		if t := content.attr("type"); atom && content != nil && t != "html" && t != "xhtml" {
			// p.textContent = html; html = p.outerHTML, for a <p> with no namespace in an XML document.
			if html == "" {
				html = "<p/>"
			} else {
				html = "<p>" + textEscaped.Replace(html) + "</p>"
			}
		}
		var rawLink *string
		if atom {
			if l := alternate(item); l != nil {
				if v, ok := l.get("href"); ok {
					rawLink = &v
				}
			}
		} else if l := child(item, "link"); l != nil {
			v := l.textContent()
			rawLink = &v
		}
		var bodyBase string
		if content != nil && hasBase(content) {
			bodyBase = baseOf(content, feedURL)
		} else {
			bodyBase = or(sectionLink(rawLink, base, *url), *url)
		}
		bodyKind := "body"
		if body == nil {
			bodyKind = "summary"
		}
		html = ShapeKnownBody(html, *url)
		var feedBody *library.FeedBody
		if web.JSTrim(html) != "" && len(html) <= 2_000_000 {
			sum := sha256.Sum256([]byte(bodyKind + bodyBase + html))
			feedBody = &library.FeedBody{HTML: html, Kind: bodyKind, BaseURL: &bodyBase, FeedURL: feedURL, Version: hex.EncodeToString(sum[:])}
		}
		// Design galleries prefix every entry with their own label ("Website Inspiration: Pete Sena"); the work's name is the title.
		name := plain(child(item, "title").textContent())
		if loc := gallery.FindStringIndex(lower(name)); loc != nil {
			name = name[loc[1]:]
		}
		if name == "" {
			parsed, _ := web.ParseURL(*url, nil)
			name = parsed.Pathname()
		}
		published, modified := dated(item, "pubdate"), (*string)(nil)
		if atom {
			published, modified = dated(item, "published"), feedDate(child(item, "updated").textContent())
		}
		articles = append(articles, library.FeedEntry{ID: id, URL: *url, Title: name, Authors: authors, PublishedAt: published, ModifiedAt: modified,
			Aliases: []string{}, BodyStatus: "not_extracted", VerificationLabel: "来自已订阅的 RSS/Atom", FeedBody: feedBody,
			Image: library.MaybeString{Set: true, Value: attached(item, base)}})
	}
	if skipped > 0 {
		warnings = append(warnings, fmt.Sprintf("%d 条内容没有可用的公开网页地址，未导入", skipped))
	}
	return library.ParsedFeed{FeedURL: feedURL, HomeURL: homeURL, Title: title, Articles: articles, FetchedAt: web.Now(), Warnings: warnings,
		Description: library.MaybeString{Set: true, Value: description}}, nil
}

// dated is date(child(item, name)?.textContent || child(item, 'date')?.textContent).
func dated(item *node, name string) *string {
	value := child(item, name).textContent()
	if value == "" {
		value = child(item, "date").textContent()
	}
	return feedDate(value)
}

func hasBase(n *node) bool {
	for ; n != nil && n.kind == elementNode; n = n.parent {
		if _, ok := n.get("xml:base"); ok {
			return true
		}
	}
	return false
}

// attached is a picture the feed attaches to the entry rather than placing in its body (Media RSS, image enclosures).
func attached(item *node, base string) *string {
	for _, n := range append(append(children(item, "content"), children(item, "thumbnail")...), children(item, "enclosure")...) {
		if n.space == mrssNS || n.local == "enclosure" && strings.HasPrefix(n.attr("type"), "image/") {
			return address(n.attr("url"), base)
		}
	}
	return nil
}

var (
	br        = `(?:<br[` + web.Space + `]*/?>[` + web.Space + `]*)`
	sp        = `[` + web.Space + `]`
	footer    = regexp.MustCompile(`(?i)` + sp + `*` + br + `*This post was originally published at <a[^>]*>Fonts In Use</a>` + sp + `*(?:<hr` + sp + `*/?>)?` + sp + `*$`)
	byline    = regexp.MustCompile(`(?i)^` + sp + `*Contributed by <a[^>]*>[^<]*</a>` + sp + `*` + br + `*`)
	credit    = regexp.MustCompile(`(?i)(?:<a[^>]*>` + sp + `*)?(<img\b[^>]*>)` + sp + `*(?:</a>)?` + sp + `*` + br + `*<i>([\s\S]*?)</i>` + sp + `*` + br + `*`)
	specimens = regexp.MustCompile(`(?i)(?:<a href="[^"]*/typefaces/[^"]*">` + sp + `*<img\b[^>]*>` + sp + `*</a>` + sp + `*` + br + `*)+`)
	breaks    = regexp.MustCompile(`(?i)(</figure>|</p>)` + sp + `*` + br + `+`)
)

// ShapeKnownBody is shapeKnownBody. Fonts In Use writes a use as plain lines:
// "Contributed by …", each picture followed by its credit in italics, the
// typefaces as linked specimen pictures, and a syndication footer. Keep the
// pictures and the words: each credit becomes the caption under its picture;
// the contributor line, the specimens and the footer are left out
// (2026-11-08). Other feeds pass through unchanged.
// ponytail: (?i) here also matches the Kelvin sign and long s (U+017F) for k
// and s, which JavaScript's /i does not; it only meets Fonts In Use's own markup.
func ShapeKnownBody(html, url string) string {
	u, err := web.ParseURL(url, nil)
	if err != nil || strings.TrimPrefix(u.Hostname(), "www.") != "fontsinuse.com" {
		return html
	}
	// Whitespace between the pieces is allowed for, but never removed elsewhere: it separates words in the text.
	html = footer.ReplaceAllString(html, "")
	html = byline.ReplaceAllString(html, "")
	html = credit.ReplaceAllString(html, "<figure>${1}<figcaption>${2}</figcaption></figure>")
	html = specimens.ReplaceAllString(html, "")
	return breaks.ReplaceAllString(html, "${1}")
}
