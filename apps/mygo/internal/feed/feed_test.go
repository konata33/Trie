package feed

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"trie/internal/library"
)

// Feed parsing and discovery cases.

func rss(items string) string {
	return `<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel><title>Writer</title><link>https://writer.example/</link>` + items + `</channel></rss>`
}

func item(link string) string {
	return `<item><title><![CDATA[A <b>real</b> post]]></title><link>` + link + `</link><dc:creator>Writer One</dc:creator><pubDate>Tue, 28 Apr 2025 14:00:00 GMT</pubDate><content:encoded><![CDATA[<p>A real body</p>]]></content:encoded></item>`
}

func parse(t *testing.T, xml, url string) library.ParsedFeed {
	t.Helper()
	f, err := Parse(xml, url)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func equal(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", what, got, want)
	}
}

func str(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// RSS imports real metadata, raw body and original trailing slash with canonical identity
func TestRSSEntry(t *testing.T) {
	f := parse(t, rss(item("https://writer.example/one/")), "https://writer.example/feed")
	equal(t, len(f.Articles), 1, "articles")
	a := f.Articles[0]
	equal(t, []any{a.ID, a.URL, a.Title, a.Authors, str(a.PublishedAt), a.FeedBody.Kind}, []any{"https://writer.example/one", "https://writer.example/one/", "A real post", []string{"Writer One"}, "2025-04-28T14:00:00.000Z", "body"}, "entry")
	if !strings.Contains(a.FeedBody.HTML, "A real body") {
		t.Fatal(a.FeedBody.HTML)
	}
}

// Atom uses published and updated separately, author override and xml:base once
func TestAtomEntry(t *testing.T) {
	xml := `<feed xmlns="http://www.w3.org/2005/Atom" xml:base="blog/"><title>Atom</title><link href="https://writer.example/"/><author><name>Feed Author</name></author><entry xml:base="posts/"><title>Entry</title><link href="one/"/><author><name>Entry Author</name></author><updated>2026-10-31T10:00:00Z</updated><content type="html">&lt;p&gt;Body&lt;/p&gt;</content></entry></feed>`
	e := parse(t, xml, "https://writer.example/feed.xml").Articles[0]
	equal(t, []any{e.URL, str(e.PublishedAt), str(e.ModifiedAt), e.Authors, str(e.FeedBody.BaseURL)}, []any{"https://writer.example/blog/posts/one/", nil, "2026-10-31T10:00:00.000Z", []string{"Entry Author"}, "https://writer.example/blog/posts/"}, "entry")
}

// invalid entries and dates are not fabricated, descriptions are labelled summaries
func TestInvalidEntries(t *testing.T) {
	f := parse(t, rss(`<item><title>Private</title><link>http://127.0.0.1/</link></item><item><guid>not-a-url</guid></item><item><title>Summary</title><link>https://writer.example/post</link><pubDate>2025-02-31</pubDate><description>Short summary</description></item>`), "https://writer.example/feed")
	equal(t, []any{len(f.Articles), len(f.Warnings) > 0, str(f.Articles[0].PublishedAt), f.Articles[0].FeedBody.Kind}, []any{1, true, nil, "summary"}, "feed")
}

// malformed, entity, non-feed and oversized input is rejected; empty valid feed is allowed
func TestRejectedInput(t *testing.T) {
	equal(t, len(parse(t, rss(""), "https://writer.example/feed").Articles), 0, "empty feed")
	for _, xml := range []string{"<rss>", "<html><body>not feed</body></html>", `<!DOCTYPE rss [<!ENTITY secret SYSTEM "file:///etc/passwd">]><rss/>`, strings.Repeat("x", 5*1024*1024+1)} {
		if _, err := Parse(xml, "https://writer.example/feed"); err == nil {
			t.Errorf("%.40q accepted", xml)
		}
	}
}

// XML-forbidden controls are cleaned explicitly and duplicate links collapse
func TestControlsAndDuplicates(t *testing.T) {
	xml := strings.Replace(rss(item("https://writer.example/one/")+item("https://www.writer.example/one#fragment")), "Writer</title>", "Writer\x1b[31m</title>", 1)
	f := parse(t, xml, "https://writer.example/feed")
	equal(t, []any{len(f.Articles), len(f.Warnings) > 0}, []any{1, true}, "feed")
}

// homepage discovery follows declared RSS links and ignores arbitrary external links
func TestDiscoverFeeds(t *testing.T) {
	got, err := DiscoverFeeds(`<head><link rel="alternate" type="application/rss+xml" href="/feed.xml"></head><a href="https://feeds.example/writer">RSS</a><a href="https://other.example/">My friend</a><script>"<a href=\"/fake\">RSS</a>"</script>`, "https://writer.example/")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, got, []string{"https://writer.example/feed.xml", "https://feeds.example/writer"}, "feeds")
	key, _ := CanonicalFeedURL("https://www.Writer.example/Case/?a=A#x")
	equal(t, key, "https://writer.example/Case?a=A", "canonical")
}

// a feed description is kept only when it says something about the author
func TestSelfDescription(t *testing.T) {
	for _, c := range [][2]string{{"Anthony Fu' Blog", "Anthony Fu"}, {"Ruan YiFeng's Blog", "阮一峰的网络日志"}, {"Recent content on Example", "Example"}, {"Tw93 Blog", "Tw93 Blog"}} {
		if d := SelfDescription(c[0], c[1]); d != nil {
			t.Errorf("%q kept as %q", c[0], *d)
		}
	}
	equal(t, str(SelfDescription("非正常信号发射与搜寻装置", "Velas电波站")), "非正常信号发射与搜寻装置", "kept")
	if d := SelfDescription("Innei's personal blog on frontend and full-stack development — TypeScript, React.", "Innei"); d == nil || !strings.HasPrefix(*d, "Innei's personal blog on frontend") {
		t.Fatal(d)
	}
}

// a design gallery label in front of each entry is not part of its title
func TestGalleryLabel(t *testing.T) {
	xml := `<rss><channel><title>G</title><link>https://g.example/</link><item><title>Website Inspiration: Pete Sena</title><link>https://g.example/pete-sena</link></item><item><title>Inspiration: a post about it</title><link>https://g.example/post</link></item></channel></rss>`
	f := parse(t, xml, "https://g.example/feed")
	equal(t, []string{f.Articles[0].Title, f.Articles[1].Title}, []string{"Pete Sena", "Inspiration: a post about it"}, "titles")
}

// markup written straight into an RSS description is kept as markup
func TestDescriptionMarkup(t *testing.T) {
	f := parse(t, `<rss><channel><title>Gallery</title><link>https://gallery.example/</link><item><title>Town</title><link>https://gallery.example/town</link>
    <description><a href="https://gallery.example/town"><img src="https://cdn.gallery.example/town.jpg" alt="Town" /></a></description></item></channel></rss>`, "https://gallery.example/rss.xml")
	if !regexp.MustCompile(`<img src="https://cdn\.gallery\.example/town\.jpg"`).MatchString(f.Articles[0].FeedBody.HTML) {
		t.Fatal(f.Articles[0].FeedBody.HTML)
	}
}

// a site offers its sharpest readable icon first and favicon.ico last
func TestDiscoverIcons(t *testing.T) {
	page := `<head><link rel="icon" href="/favicon.svg" type="image/svg+xml"><link rel="icon" sizes="32x32" href="/i32.png"><link rel="shortcut icon" href="/s.png">
    <link rel="icon" sizes="192x192" href="/i192.png"><link rel="apple-touch-icon" href="/touch.png"><link rel="stylesheet" href="/a.css"></head>`
	equal(t, DiscoverIcons(page, "https://writer.example/blog/"), []string{"https://writer.example/touch.png", "https://writer.example/i192.png", "https://writer.example/i32.png", "https://writer.example/s.png"}, "icons")
	equal(t, DiscoverIcons("<p>none</p>", "https://writer.example/"), []string{"https://writer.example/favicon.ico"}, "none")
}

// a Fonts In Use use keeps its pictures with their credits as captions and its words; contributor, specimens and footer are left out
func TestFontsInUse(t *testing.T) {
	body := `Contributed by <a href="/contributors/1/bomba">Bomba</a><br/><a href="https://fontsinuse.com/uses/1/use"><img src="https://assets.fontsinuse.com/photo.jpeg"/></a><br/><br/><i><span>Atlas Studio. </span><span>License: All Rights Reserved. </span></i><br/>` +
		`<a href="https://fontsinuse.com/typefaces/2/haetti-antiqua"><img src="https://assets.fontsinuse.com/haetti.png"/></a><br/><a href="https://fontsinuse.com/typefaces/52/times"><img src="https://assets.fontsinuse.com/times.png"/></a><br/><br/>` +
		`<p>The exhibition <cite>Tempesta</cite> is a meditation.</p><br/>This post was originally published at <a href="https://fontsinuse.com/uses/1/use">Fonts In Use</a><hr/>`
	html := parse(t, rss(`<item><title>Use</title><link>https://fontsinuse.com/uses/1/use</link><description><![CDATA[`+body+`]]></description></item>`), "https://fontsinuse.com/main.rss").Articles[0].FeedBody.HTML
	equal(t, html, `<figure><img src="https://assets.fontsinuse.com/photo.jpeg"/><figcaption><span>Atlas Studio. </span><span>License: All Rights Reserved. </span></figcaption></figure>`+
		`<p>The exhibition <cite>Tempesta</cite> is a meditation.</p>`, "use")
	equal(t, ShapeKnownBody(`<p>A <b>b</b> <i>c</i></p>`, "https://writer.example/"), `<p>A <b>b</b> <i>c</i></p>`, "other feeds pass through unchanged")
}

// an entry linking to one part of a page keeps that link as its body's base; its address is the page
func TestSectionLink(t *testing.T) {
	atom := `<feed xmlns="http://www.w3.org/2005/Atom"><title>Design</title><link href="https://writer.example/"/>
    <entry><title>Logo: Squawkbox</title><link rel="alternate" type="text/html" href="https://writer.example/design/#2026-07-squawkbox"/><id>https://writer.example/design/#2026-07-squawkbox</id>
    <published>2026-07-01T00:00:00Z</published><content type="html">&lt;img src="/design/squawkbox.svg"&gt;&lt;br&gt;Squawkbox is a boiler fleet monitor.</content></entry>
    <entry><title>A post</title><link href="https://writer.example/blog/post/"/><id>https://writer.example/blog/post/</id><published>2026-06-01T00:00:00Z</published><content type="html">&lt;p&gt;Words.&lt;/p&gt;</content></entry></feed>`
	f := parse(t, atom, "https://writer.example/atom.xml")
	logo, post := f.Articles[0], f.Articles[1]
	equal(t, []any{logo.URL, str(logo.FeedBody.BaseURL), str(post.FeedBody.BaseURL)}, []any{"https://writer.example/design/", "https://writer.example/design/#2026-07-squawkbox", "https://writer.example/blog/post/"}, "bases")
}
