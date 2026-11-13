package extract

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Extraction and gallery-page cases, one Go test each.

const (
	tsBase  = "https://example.com/posts/story"
	tsProse = "This is a useful public article about reading and engineering. It contains enough actual prose to be read independently, with clear explanations and practical examples. "
)

var prose3 = strings.Repeat(tsProse, 3)

func tsPageHTML(content string, head ...string) string {
	return `<html><head><title>A useful story</title>` + strings.Join(head, "") + `</head><body><article><h1>A useful story</h1><p>` + prose3 + `</p>` + content + `</article></body></html>`
}

func must(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Errorf(format, args...)
	}
}

func match(pattern, s string) bool { return regexp.MustCompile(pattern).MatchString(s) }

func TestLegacyArticleNeedsTrust(t *testing.T) {
	page := "<html><body><h1>Old essay</h1><p>" + strings.Repeat("A real public essay with substantial original prose and useful explanations. ", 20) + "</p></body></html>"
	must(t, ExtractPage(page, "https://writer.example/essay.html", false).Status == "unavailable", "untrusted legacy page read")
	r := ExtractPage(page, "https://writer.example/essay.html", true)
	must(t, r.Status == "ready" && r.TextLength > 500, "%+v", r)
}

func TestKeepsStructureCodeTablesImages(t *testing.T) {
	r := ExtractPage(tsPageHTML(`<pre><code class="language-ts noise">const a = 1</code></pre><table><caption>Results</caption><tr><th>Category</th><th>Value</th></tr><tr><td>Reading</td><td>42</td></tr></table><figure><img src="../picture.png"><figcaption>A picture</figcaption></figure><a href="/more">More</a>`), tsBase, false)
	must(t, r.Status == "ready", "status %s", r.Status)
	must(t, strings.Contains(r.HTML, `<code class="language-ts">`) && strings.Contains(r.HTML, "<table>"), "%s", r.HTML)
	must(t, slices.Equal(r.Images, []string{"https://example.com/picture.png"}) && slices.Equal(r.Links, []string{"https://example.com/more"}), "%v %v", r.Images, r.Links)
}

func TestStripsUnsafeContent(t *testing.T) {
	// Extraction runs no scripts, so the page's script cannot have run.
	r := ExtractPage(tsPageHTML(`<script>globalThis.extractExecuted = true</script><p onclick="alert(1)" style="color:red" class="noise">Visible words</p><svg><text>SVG secret</text></svg><iframe src="https://evil.example"></iframe><a href="javascript&#58;alert(1)">Bad</a><img src="data:image/png;base64,AAAA"><a href="http://0x7f000001/private">Loopback</a><img src="https://[::ffff:127.0.0.1]/a"><img src="https://user:pass@example.com/a"><a href="http://internal.local/a">Local</a><a href="https://%6cocalhost/a">Encoded host</a>`), tsBase, false)
	must(t, r.Status == "ready", "status %s", r.Status)
	must(t, !match(`(?i)script|onclick|style=|noise|svg|iframe|javascript:|data:|127\.0\.0\.1|0x7f|localhost|internal\.local|user:pass|SVG secret`, r.HTML), "%s", r.HTML)
	must(t, len(r.Images) == 0 && len(r.Links) == 0, "%v %v", r.Images, r.Links)
}

func TestHiddenContentExcluded(t *testing.T) {
	r := ExtractPage(tsPageHTML(`<p hidden>Hidden secret</p><div class="private">CSS secret</div><div style="display:none">Inline secret</div><script src="https://never-fetch.invalid/code.js"></script><script type="application/json">{"body":"Hydration secret"}</script><section class="comments">Comment secret</section>`, `<style>.private{display:none}</style>`), tsBase, false)
	must(t, r.Status == "ready" && !match(`Hidden secret|CSS secret|Inline secret|Hydration secret|Comment secret|never-fetch`, r.HTML), "%s %s", r.Status, r.HTML)
}

func TestReactPlacementsRestoreSegments(t *testing.T) {
	page := `<html><body><template id="B:0"></template><div hidden id="S:0"><main><template id="P:1"></template></main></div><div hidden id="S:1"><div class="contents [&_[data-comment-gutter-host]]:lg:!pr-0"><article><h1>Streamed story</h1><p>` + prose3 + `</p><div hidden>Ordinary hidden secret</div></article></div></div><script>$RS=function(a,b){return a+b;};$RS("S:1","P:1")</script><script>$RC=function(a,b){return a+b;};$RC("B:0","S:0")</script></body></html>`
	r := ExtractPage(page, tsBase, false)
	must(t, r.Status == "ready" && strings.Contains(r.HTML, "useful public article") && !match(`Ordinary hidden secret|\$RS|\$RC|return a`, r.HTML), "%s %s", r.Status, r.HTML)
}

// A completed boundary's content replaces its fallback (innei.in's code blocks streamed in after "CodeBlock
// Loading..."); a nested boundary's markers do not end it early, and without the closing marker the fallback stays.
func TestReactBoundaryDropsFallback(t *testing.T) {
	page := `<html><body><article><p>` + prose3 + `</p><div><!--$?--><template id="B:3"></template><div class="h-[500px]">CodeBlock Loading...</div><!--$--><p>Nested fallback</p><!--/$--><!--/$--></div><p>After the code.</p></article>` +
		`<div hidden id="S:3"><pre><code>const styles = stylex.create({})</code></pre></div><script>$RC("B:3","S:3")</script></body></html>`
	r := ExtractPage(page, tsBase, false)
	must(t, r.Status == "ready" && strings.Contains(r.HTML, "stylex.create") && strings.Contains(r.HTML, "After the code.") && !match(`Loading|Nested fallback`, r.HTML), "%s %s", r.Status, r.HTML)
	open := strings.Replace(page, `<!--/$--><!--/$-->`, ``, 1)
	r = ExtractPage(open, tsBase, false)
	must(t, strings.Contains(r.HTML, "stylex.create") && strings.Contains(r.HTML, "CodeBlock Loading"), "an unclosed boundary lost its fallback: %s", r.HTML)
}

// A feed that wrote its h2s as bare lines between paragraphs (Smashing Magazine) gets them back, a level above its own
// h3s; a line ending as a sentence, or text running on in a paragraph, stays as it is.
func TestFeedLooseLinesAreHeadings(t *testing.T) {
	body := `<p>` + prose3 + `</p>
Finding The Break-even Line
<p>Every interface change costs the user something.</p>
<h3>Question 1: Does This Reduce The Effort?</h3>
<p>Count everything the task asks.</p>
This line ends as a sentence.
<p>More words.</p>`
	r := ExtractFeedBody(body, tsBase, "body")
	must(t, strings.Contains(r.HTML, "<h2>Finding The Break-even Line</h2>") && strings.Contains(r.HTML, "<h3>Question 1") &&
		!strings.Contains(r.HTML, "<h2>This line"), "%s", r.HTML)
}

// A code box's language label above its pre becomes the code's language; a pre naming its own keeps it and its label.
func TestCodeBoxLabelIsLanguage(t *testing.T) {
	page := `<html><body><article><p>` + prose3 + `</p><div><div><div><span>TSX</span><button type="button">复制</button></div><div><pre><code>const a = 1</code></pre></div></div></div>` +
		`<div><p>CSS</p><pre><code class="language-scss">.a{}</code></pre></div></article></body></html>`
	r := ExtractPage(page, tsBase, false)
	must(t, strings.Contains(r.HTML, `class="language-tsx"`) && !match(`TSX|复制`, r.HTML) && strings.Contains(r.HTML, "language-scss") && strings.Contains(r.HTML, ">CSS<"), "%s", r.HTML)
}

func TestReactPlacementsIgnoredUnlessDirect(t *testing.T) {
	for _, script := range []string{``, `if(false)$RS("S:1","P:1");`, `$RS=function(){};if(false)$RS("S:1","P:1");`, `if(false){noop();$RS("S:1","P:1");}`, `setTimeout(()=>{$RS("S:1","P:1")},0)`, `const text='$RS("S:1","P:1");';`, `function later(){noop();$RS("S:1","P:1");}`, strings.Repeat(`$RS("S:1","P:1");`, 101)} {
		page := `<template id="P:1"></template><div hidden id="S:1"><article><p>` + prose3 + `</p></article></div><script>` + script + `</script>`
		must(t, ExtractPage(page, tsBase, false).Status == "unavailable", "placed by %q", script)
	}
	duplicates := `<template id="P:1"></template><template id="P:1"></template><div hidden id="S:1"><article><p>` + prose3 + `</p></article></div><script>$RS("S:1","P:1")</script>`
	must(t, ExtractPage(duplicates, tsBase, false).Status == "unavailable", "placed into a duplicate target")
	for _, pair := range [][2]string{{"span", "template"}, {"div", "div"}} {
		invalid := fmt.Sprintf(`<%[2]s id="P:1"></%[2]s><%[1]s hidden id="S:1"><article><p>%[3]s</p></article></%[1]s><script>$RS("S:1","P:1")</script>`, pair[0], pair[1], prose3)
		must(t, ExtractPage(invalid, tsBase, false).Status == "unavailable", "placed %v", pair)
	}
}

func TestStreamedRestrictedStaysRestricted(t *testing.T) {
	page := `<head><meta name="description" content="A public introduction."><script type="application/ld+json">{"@type":"Article","isAccessibleForFree":false}</script></head><body><template id="B:0"></template><div hidden id="S:0"><article><p>` + prose3 + `</p><p>Paid streamed chapter.</p></article></div><script>$RC("B:0","S:0")</script></body>`
	r := ExtractPage(page, tsBase, false)
	must(t, r.Status == "restricted" && strings.Contains(r.HTML, "A public introduction") && !match(`useful public article|Paid streamed`, r.HTML), "%s %s", r.Status, r.HTML)
}

func TestSemanticEvidenceAndTinyPages(t *testing.T) {
	must(t, ExtractPage(`<div>`+prose3+`</div>`, tsBase, false).Status == "unavailable", "a bare div")
	must(t, ExtractPage(`<main><article><p>`+prose3+`</p></article><article><p>`+prose3+`</p></article></main>`, tsBase, false).Status == "unavailable", "two articles")
	must(t, ExtractPage(`<article><h1>Short</h1><p>No body.</p></article>`, tsBase, false).Status == "unavailable", "a tiny article")
	must(t, ExtractPage(`<article><h1>A photo essay</h1><figure><img src="/picture.jpg"><figcaption>A distant mountain</figcaption></figure></article>`, tsBase, false).Status == "ready", "a photo essay")
}

func TestRestrictedSchemaKeepsExcerpt(t *testing.T) {
	r := ExtractPage(tsPageHTML(`<div hidden>Paid private text</div>`, `<meta name="description" content="Public publisher introduction."><script type="application/ld+json">{"@type":"BlogPosting","isAccessibleForFree":false,"articleBody":"Private schema body"}</script>`), tsBase, false)
	must(t, r.Status == "restricted" && strings.Contains(r.HTML, "Public publisher introduction") && !match(`Paid private|Private schema`, r.HTML), "%s %s", r.Status, r.HTML)
	wall := ExtractPage(tsPageHTML(`<div class="paywall"><p>Subscribe to continue reading</p><button>Subscribe</button></div>`), tsBase, false)
	must(t, wall.Status == "restricted" && !strings.Contains(wall.HTML, "Subscribe"), "%s %s", wall.Status, wall.HTML)
	must(t, ExtractPage(tsPageHTML(`<form class="paywall"><input type="password">Log in to read</form>`), tsBase, false).Status == "restricted", "a login form")
	must(t, ExtractPage(tsPageHTML(`<p>The article explains how to subscribe and log in to a service.</p>`), tsBase, false).Status == "ready", "prose about subscribing")
}

func TestPaidRegionsRemoved(t *testing.T) {
	head := `<script type="application/ld+json">{"@type":"NewsArticle","isAccessibleForFree":false,"articleBody":"Schema private body","hasPart":{"@type":"WebPageElement","isAccessibleForFree":false,"cssSelector":".protected-section"}}</script>`
	r := ExtractPage(tsPageHTML(`<section class="protected-section"><p>Paid chapter never shown.</p></section>`, head), tsBase, false)
	must(t, r.Status == "restricted" && strings.Contains(r.HTML, "useful public article") && !match(`Paid chapter|Schema private body`, r.HTML), "%s %s", r.Status, r.HTML)
	for _, selector := range []string{"[broken", strings.Repeat("x", 513), ":has(.private)"} {
		schema, _ := json.Marshal(map[string]any{"@type": "Article", "isAccessibleForFree": false, "hasPart": map[string]any{"@type": "WebPageElement", "isAccessibleForFree": false, "cssSelector": selector}})
		invalid := ExtractPage(tsPageHTML(`<p>Paid chapter never shown.</p>`, `<script type="application/ld+json">`+string(schema)+`</script>`), tsBase, false)
		must(t, invalid.Status == "restricted" && invalid.HTML == "", "%q: %s %s", selector[:min(10, len(selector))], invalid.Status, invalid.HTML)
	}
}

func TestRestrictedWithoutBoundaryShowsNoDOM(t *testing.T) {
	schema := `<script type="application/ld+json">{"@type":"Article","isAccessibleForFree":false}</script>`
	blurred := `<div style="filter:blur(8px)"><p>Premium blurred body never shown.</p></div>`
	none := ExtractPage(tsPageHTML(blurred, schema), tsBase, false)
	must(t, none.Status == "restricted" && none.HTML == "", "%s %s", none.Status, none.HTML)
	description := "Publisher &lt;intro&gt; &amp; " + strings.Repeat("public ", 200)
	with := ExtractPage(tsPageHTML(blurred, schema+`<meta property="og:description" content="`+description+`">`), tsBase, false)
	must(t, with.Status == "restricted" && strings.Contains(with.HTML, "Publisher &lt;intro&gt; &amp;") && with.TextLength <= 1000 && !match(`Premium blurred|useful public article|<intro>`, with.HTML), "%s %d %s", with.Status, with.TextLength, with.HTML[:min(200, len(with.HTML))])
	unmatched := `<script type="application/ld+json">{"@type":"Article","isAccessibleForFree":false,"hasPart":{"@type":"WebPageElement","isAccessibleForFree":false,"cssSelector":".missing-protected-section"}}</script>`
	must(t, ExtractPage(tsPageHTML(blurred, unmatched), tsBase, false).HTML == "", "an unmatched boundary shows the DOM")
}

func TestArchiveLinksUnavailable(t *testing.T) {
	var links strings.Builder
	for i := range 20 {
		fmt.Fprintf(&links, `<a href="/post/%d">Some archived article title number %d</a>`, i, i)
	}
	must(t, ExtractPage(`<main><h1>Archive</h1>`+links.String()+`</main>`, tsBase, false).Status == "unavailable", "an archive read as an article")
}

func tsRSS(entries string) string {
	return `<?xml version="1.0"?><rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Stories</title><link>https://example.com</link><description>Stories</description>` + entries + `</channel></rss>`
}

func tsEntry(url, body string, tag ...string) string {
	t := "content:encoded"
	if len(tag) > 0 {
		t = tag[0]
	}
	return `<item><title>Story</title><link>` + url + `</link><` + t + `><![CDATA[<p>` + body + `</p>]]></` + t + `></item>`
}

func TestSanitizedExpansionBounded(t *testing.T) {
	encoded := strings.Repeat("&", 410_000)
	xml := tsRSS(tsEntry(tsBase, encoded))
	must(t, len(xml) < 2_000_000, "feed too big")
	r, ok := ExtractFeed(xml, "https://example.com/feed", []string{tsBase})
	must(t, ok && r.Status == "unavailable" && r.HTML == "", "%v %s", ok, r.Status)
	restricted := ExtractPage(tsPageHTML(`<p>`+encoded+`</p><div class="paid">Private text.</div>`, `<script type="application/ld+json">{"@type":"Article","isAccessibleForFree":false,"hasPart":{"@type":"WebPageElement","isAccessibleForFree":false,"cssSelector":".paid"}}</script>`), tsBase, false)
	must(t, restricted.Status == "restricted" && restricted.HTML == "", "%s %d", restricted.Status, len(restricted.HTML))
}

func TestFeedCandidatesFromHead(t *testing.T) {
	r := ExtractPage(tsPageHTML(`<a href="/fake.xml">RSS</a>`, `<link rel="alternate" type="application/rss+xml" href="/feed.xml"><link rel="alternate" type="application/atom+xml" href="http://192.168.1.1/feed"><link rel="alternate" type="text/html" href="/other">`), tsBase, false)
	must(t, slices.Equal(r.FeedURLs, []string{"https://example.com/feed.xml"}), "%v", r.FeedURLs)
}

func TestFeedMatchesAliases(t *testing.T) {
	xml := tsRSS(tsEntry("https://example.com/other", strings.Repeat("Wrong article ", 30)) + tsEntry("https://www.example.com/posts/story/", prose3))
	r, ok := ExtractFeed(xml, "https://example.com/feed.xml", []string{tsBase})
	must(t, ok && r.Status == "ready" && strings.Contains(r.HTML, "useful public article") && !strings.Contains(r.HTML, "Wrong article"), "%v %s", ok, r.Status)
	_, ok = ExtractFeed(xml, "https://example.com/feed.xml", []string{"http://example.com/posts/story"})
	must(t, !ok, "http matched https")
	_, ok = ExtractFeed(xml, "https://example.com/feed.xml", []string{tsBase + "?other=1"})
	must(t, !ok, "a query matched")
}

func TestFeedBodiesAndSummaries(t *testing.T) {
	r, ok := ExtractFeed(tsRSS(tsEntry(tsBase, prose3, "description")), "https://example.com/feed", []string{tsBase})
	must(t, ok && r.Status == "summary", "description: %v %s", ok, r.Status)
	atom := `<feed xmlns="http://www.w3.org/2005/Atom"><title>Stories</title><entry><title>Story</title><id>` + tsBase + `</id><link href="` + tsBase + `"/><content type="html">&lt;p&gt;` + prose3 + `&lt;/p&gt;</content></entry></feed>`
	r, ok = ExtractFeed(atom, "https://example.com/feed", []string{tsBase})
	must(t, ok && r.Status == "ready" && strings.Contains(r.HTML, "useful public article"), "atom: %v %s", ok, r.Status)
}

func TestFeedRejectsBadXML(t *testing.T) {
	for _, xml := range []string{`<rss><broken>`, `<!DOCTYPE rss [<!ENTITY x SYSTEM "file:///etc/passwd">]><rss>&x;</rss>`, strings.Repeat("x", 2_000_001), `<rss>` + strings.Repeat("<a>", 100) + strings.Repeat("</a>", 100) + `</rss>`} {
		_, ok := ExtractFeed(xml, "https://example.com/feed", []string{tsBase})
		must(t, !ok, "accepted %q", xml[:min(30, len(xml))])
	}
	must(t, ExtractPage(strings.Repeat("x", 2_000_001), tsBase, false).Status == "unavailable", "an oversized page")
}

func TestLazyImagesAndCaching(t *testing.T) {
	x := ExtractPage(tsPageHTML(`<img src="data:image/gif;base64,AAAA" data-src="/lazy.jpg" alt="Lazy picture"><img src="/missing.jpg" alt="Missing &lt;picture&gt;"><a href="/more">More</a>`), tsBase, false)
	must(t, slices.Equal(x.Images, []string{"https://example.com/lazy.jpg", "https://example.com/missing.jpg"}), "%v", x.Images)
	cached := CacheImages(x.HTML, map[string]string{"https://example.com/lazy.jpg": "data:image/jpeg;base64,AAAA"})
	must(t, strings.Contains(cached, `src="data:image/jpeg;base64,AAAA"`) && !match(`src="https?:|missing\.jpg`, cached), "%s", cached)
	must(t, strings.Contains(cached, "Missing &lt;picture&gt;") && strings.Contains(cached, `href="https://example.com/more"`), "%s", cached)
	got := CacheImages(`<img src="https://example.com/x" alt="fallback"><script>bad()</script>`, map[string]string{"https://example.com/x": "data:image/svg+xml;base64,AAAA"})
	must(t, got == `<img alt="fallback" />`, "an image that could not be kept stays as a sourceless placeholder: %s", got)
}

func TestRepeatedHeaderDropped(t *testing.T) {
	body := func(content string, title ...string) Extracted {
		name := "Goodbye Claude, Hello Pi"
		if len(title) > 0 {
			name = title[0]
		}
		return ExtractPage(`<html><head><title>`+name+` – Caleb</title></head><body><main><div class="post">`+content+`<p>`+prose3+`</p></div></main></body></html>`, tsBase, true)
	}
	repeated := body(`<p>Goodbye Claude, Hello Pi</p><p>21 September 2026 <a href="/tags/pi">#pi</a> <a href="/tags/claude">#claude</a></p>`)
	must(t, repeated.Status == "ready" && !match(`Goodbye Claude|21 September|#pi`, repeated.HTML) && strings.Contains(repeated.HTML, "useful public article"), "the title and the date line are already in the reader header: %s", repeated.HTML)
	must(t, !strings.Contains(body(`<p>07 Nov, 2026</p>`, "Person of Interest").HTML, "07 Nov"), "a date line alone")
	must(t, !strings.Contains(body(`<p>2026年11月4日 · 阅读 5 分钟</p>`, "十个月").HTML, "2026年11月4日"), "a Chinese date line")
	must(t, strings.Contains(body(`<h2>Logos I designed</h2>`, "Logos").HTML, "Logos I designed"), "a heading that says more")
	must(t, strings.Contains(body(`<p>On 21 September 2026 I finally moved my whole setup to a new tool, after a year of small frustrations.</p>`).HTML, "On 21 September 2026"), "a paragraph opening with a date")
	must(t, strings.Contains(body(`<h2>2026年11月4日</h2>`, "日记").HTML, "2026年11月4日"), "a diary's date heading")
	labelled := body(`<div><p>INFO</p><div><p><span>Date</span> <span>November 9, 2026</span></p><p><span>Author</span> <span>ibby</span></p><div><p>Share</p></div><div><p>Tags</p></div></div></div><div><p> BLOG </p></div>`, "Scentury")
	must(t, !match(`INFO|November 9|ibby|Share|Tags|BLOG`, labelled.HTML) && strings.Contains(labelled.HTML, "useful public article"), "a magazine's labels: %s", labelled.HTML)
	must(t, !strings.Contains(body(`<div><p>Share</p></div><div><p>Share</p></div>`, "Starting out").HTML, "Share"), "two share labels")
	must(t, strings.Contains(body(`<p>Share what you learn.</p>`, "Notes").HTML, "Share what you learn"), "a sentence that starts like a label stays")
}

func TestEmbeddedPictureVetted(t *testing.T) {
	diagram := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10" style="--_s:#f3f3f3"><rect width="10" height="10" fill="var(--_s)"/><text>Label</text><script>alert(1)</script></svg>`))
	r := ExtractPage(tsPageHTML(`<p><img alt="Mermaid diagram" width="865" height="287" src="data:image/svg+xml;base64,`+diagram+`"></p><p><img alt="Not a picture" src="data:image/png;base64,PGh0bWw+"></p>`), tsBase, false)
	must(t, r.Status == "ready", "status %s", r.Status)
	kept := regexp.MustCompile(`<img[^>]*alt="Mermaid diagram"[^>]*>`).FindString(r.HTML)
	m := regexp.MustCompile(`src="data:image/svg\+xml;base64,([^"]+)"`).FindStringSubmatch(kept)
	must(t, m != nil, "no picture: %s", kept)
	if m != nil {
		svg, _ := base64.StdEncoding.DecodeString(m[1])
		must(t, strings.Contains(string(svg), `fill="#f3f3f3"`) && strings.Contains(string(svg), "Label") && !match(`script|var\(`, string(svg)), "%s", svg)
	}
	must(t, !strings.Contains(r.HTML, "data:image/png"), "a data: URL whose bytes are not the picture it claims is dropped (its alt stays as words)")
	must(t, len(r.Images) == 0, "nothing to download: %v", r.Images)
	cached := regexp.MustCompile(`<img[^>]*alt="Mermaid diagram"[^>]*>`).FindString(CacheImages(r.HTML, map[string]string{}))
	must(t, strings.Contains(cached, `src="data:image/svg+xml;base64,`), "the caching pass keeps it too: %s", cached)
}

func TestFeedTeaserLosesMoreLink(t *testing.T) {
	r := ExtractFeedBody(`<p><img src="https://gallery.example/shot.png"></p><p>A portfolio that looks like its owner did at ten, with doodles and a drawing wall.</p><p><a href="https://gallery.example/tobi">Full Review</a></p>`, "https://gallery.example/tobi", "body")
	must(t, r.Status == "summary" && !strings.Contains(r.HTML, "Full Review"), "%s %s", r.Status, r.HTML)
	must(t, strings.Contains(ExtractFeedBody(`<p>Read more about it <a href="https://x.example/a">here</a>.</p>`, "https://x.example/a", "body").HTML, "here"), "an ordinary link in the text stays")
}

func TestWhiteboardPlaceholders(t *testing.T) {
	scene := "https://object.example/versions/3178f182.json"
	page := `<html><head><title>Migrating</title></head><body><article><p>` + prose3 + `</p><div class="rich-block-anchor" data-block-id="8e2bcf85"><div class="aspect"><span>Whiteboard</span></div></div><p>` + prose3 + `</p></article>` +
		`<script>self.__next_f.push([1,"{\"type\":\"excalidraw\",\"version\":1,\"$\":{\"blockId\":\"8e2bcf85\"},\"snapshot\":\"` + scene + `\"}"])</script></body></html>`
	r := ExtractPage(page, "https://innei.example/posts/tech/migrating", true)
	must(t, r.Status == "ready" && slices.Equal(r.Images, []string{scene}), "the scene file is fetched like a picture and drawn there: %s %v", r.Status, r.Images)
	must(t, match(`<img[^>]*alt="Whiteboard"`, r.HTML) && !strings.Contains(r.HTML, "<span>Whiteboard</span>"), "%s", r.HTML)
}

func TestPageFurniture(t *testing.T) {
	toc := `<div><ul><li><a href="#one">One</a><ul><li><a href="#one-a">One A</a></li></ul></li><li><a href="#two">Two</a></li><li><a href="#three">Three</a></li></ul></div>`
	widget := `<figure><div><p>Hover on a framework to preview how modules compose.</p></div><div><h3 id="kit-general">General</h3><div><p><span>A11y</span></p><p><img src="https://example.com/vitest.svg"><span>Vitest</span></p></div></div><p>This is for demonstration.</p></figure>`
	r := ExtractPage(`<html><head><title>DevTools</title></head><body><article>`+toc+`<h2 id="one">One</h2><p>`+prose3+`</p>`+widget+`<p>`+prose3+`</p><ul><li><a href="#one">See one</a> and the rest of this real list item</li><li>Second</li><li>Third</li></ul></article></body></html>`, tsBase, true)
	must(t, r.Status == "ready", "status %s", r.Status)
	must(t, !match(`href="#one-a"|>Three<`, r.HTML), "the table of contents is gone (the reader has its own)")
	must(t, !match(`Hover on a framework|A11y|vitest\.svg`, r.HTML), "the flattened widget is gone")
	must(t, strings.Contains(r.HTML, `<a href="https://example.com/posts/story#kit-general">互动示例，在原网页查看</a>`), "%s", r.HTML)
	must(t, strings.Contains(r.HTML, "See one</a> and the rest of this real list item"), "a list with words of its own stays")
}

func TestSrcsetSizeAndViewCounters(t *testing.T) {
	srcset := "/up/a.png 4000w, /up/a-300x200.png 300w, /up/a-1024x683.png 1024w, /up/a-2048x1365.png 2048w, /up/a-1600x1067.png 1600w"
	small := "/up/b.jpg 1024w, /up/b-768x512.jpg 768w"
	r := ExtractPage(tsPageHTML(`<figure><img src="/up/a.png" srcset="`+srcset+`"></figure><img src="/up/b.jpg" srcset="`+small+`"><img src="https://medium.example/_/stat?event=viewed" width="1" height="1" alt="">`), tsBase, false)
	must(t, slices.Equal(r.Images, []string{"https://example.com/up/a-1600x1067.png", "https://example.com/up/b.jpg"}) && !strings.Contains(r.HTML, "_/stat"), "%v", r.Images)
}

func TestSyndicationNotice(t *testing.T) {
	notice := `<p>by <a href="https://writer.example/">writer</a> from <a href="https://writer.example/?p=1">https://writer.example/?p=1</a><br />本文可全文转载，但需要保留原作者、出处以及文中链接，商用请联系授权。</p>`
	r := ExtractFeedBody(notice+`<h3>一、还在 mousedown</h3><p>`+prose3+`</p>`, "https://writer.example/?p=1", "body")
	must(t, !match(`from|转载`, r.HTML) && strings.Contains(r.HTML, "还在 mousedown"), "%s", r.HTML)
	must(t, strings.Contains(ExtractFeedBody(`<p>By the river from dawn I watched the boats, as https://writer.example/boats describes.</p><p>`+prose3+`</p>`, "https://writer.example/?p=2", "body").HTML, "By the river"), "a first paragraph that mentions a source stays")
}

// Gallery pages

func cdn(trim string, w int) string {
	return fmt.Sprintf("https://assets.example/cdn-cgi/image/trim=%s,width=%d,height=351,fit=cover,format=jpg/shot.jpg", trim, w)
}

var galleryEntry = `<html><head><meta property="og:description" content="A brand strategist's site with cinematic transitions."></head><body>
  <span class="review-name"><h1>Pete Sena</h1></span><span class="review-credits"><a class="credit-text" href="/authors/ds">Digital Surgeons</a></span>
  <div class="review-platform__title"><span>Built with</span><a href="https://onepagelove.example/go/webflow">Webflow <span aria-hidden="true">↗</span></a></div>
  <div class="review-screenshot"><video src="https://onepagelove.example/rec/full.mp4" poster="https://onepagelove.example/rec/poster.jpg" muted loop></video></div>
  <div class="review-cta-button"><a href="#screenshot" title="View the website screenshot">Screenshot</a></div>
  <div class="review-cta-button"><a href="https://site.example?ref=onepagelove" title="Visit Pete Sena Website">Visit Website</a></div>
  <div class="inspo-links"><span class="link-type">Tech</span> <span class="link-list"><a>GSAP</a><i>,</i> <a>Three.js</a></span></div>
  <div class="inspo-links inspo-links--typefaces"><span class="link-type">Typefaces</span> <span class="link-list"><a>Inter</a></span></div>
  <div class="review-tags__row inspo-features"><a>Hero Banner</a><a>Client Logo Row</a></div>
  <div class="opl-sections"><figure class="opl-sections__item"><a class="opl-sections__link" aria-label="Hero Banner on Pete Sena">
    <img srcset="` + cdn("21;0;23339;0", 640) + ` 640w, ` + cdn("21;0;23339;0", 1280) + ` 1280w" src="` + cdn("21;0;23339;0", 640) + `"></a>
    <figcaption><a>Hero Banner</a></figcaption></figure>
    <figure class="opl-sections__item"><a class="opl-sections__link" aria-label="Sliver on Pete Sena"><img src="https://assets.example/sliver.jpg" width="640" height="36"></a></figure></div>
</body></html>`

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestGalleryEntryWork(t *testing.T) {
	g := galleryPage(parseDocument(galleryEntry), "")
	must(t, g != nil, "no work")
	if g == nil {
		return
	}
	must(t, match(`^<p>A brand strategist(?:&#39;|')s site with cinematic transitions\.</p>$`, g.html), "the body is the prose alone: %s", g.html)
	must(t, jsonOf(g.work.Sections) == jsonOf([]Shot{{Name: "Hero Banner", Image: ptr(cdn("21;0;23339;0", 1280))}}), "the widest srcset candidate, commas in the address intact; the sliver is left out: %s", jsonOf(g.work.Sections))
	w := g.work
	w.Facts, w.Sections = nil, nil
	must(t, jsonOf(w) == jsonOf(Work{Name: "Pete Sena", Designer: ptr("Digital Surgeons"), Site: ptr("https://site.example"), Host: ptr("site.example"), BuiltWith: ptr("Webflow"),
		Poster: ptr("https://onepagelove.example/rec/poster.jpg"), Video: ptr("https://onepagelove.example/rec/full.mp4")}), "the visit link, not the screenshot button, without the referral tag: %s", jsonOf(w))
	must(t, jsonOf(g.work.Facts) == jsonOf([]Fact{{"技术", []string{"GSAP", "Three.js"}}, {"字体", []string{"Inter"}}, {"功能", []string{"Hero Banner", "Client Logo Row"}}}), "%s", jsonOf(g.work.Facts))
	must(t, !strings.Contains(g.html, "Sliver"), "a section too thin to show anything is left out")
	must(t, galleryPage(parseDocument(`<article><p>An ordinary post.</p></article>`), "") == nil, "an ordinary post read as a work")
	p := ExtractPage(galleryEntry, "https://onepagelove.example/pete-sena", true)
	must(t, p.Status == "ready" && p.Work != nil && len(p.Work.Sections) == 1 && *p.Work.Video == "https://onepagelove.example/rec/full.mp4", "%+v", p)
}

func TestTeaser(t *testing.T) {
	teaser := `<p><a href="https://g.example/x"><img src="https://g.example/x.jpg"></a></p><p>A brand strategist's site.</p><p><a href="https://g.example/x">Full Review</a></p>`
	must(t, IsTeaser(teaser), "a lead-in")
	must(t, !IsTeaser(`<p>`+strings.Repeat("A long real post. ", 60)+`</p><p><a href="/x">Read more</a></p>`), "long bodies stay bodies")
	must(t, !IsTeaser(`<p>A short note with a <a href="/y">link</a> in it.</p>`), "short posts without a read-more link stay bodies")
	must(t, IsTeaser(`<p>今天的短讯。<a href="/z">阅读全文</a></p>`), "阅读全文")
	must(t, ExtractFeedBody(teaser, "https://g.example/x", "body").Status == "summary", "a teaser body")
}

// TestFeedLeadInPageIsWork is the extraction half of "a cached feed lead-in is read again from the page behind it": the
// lead-in reads as a summary and the entry page as a work. Re-reading a cached body and saving the media belong to
// internal/reading, tested there.
func TestFeedLeadInPageIsWork(t *testing.T) {
	teaser := `<p>A brand strategist's site.</p><p><a href="https://onepagelove.example/pete-sena">Full Review</a></p>`
	must(t, IsTeaser(teaser) && ExtractFeedBody(teaser, "https://onepagelove.example/pete-sena", "body").Status == "summary", "the lead-in")
	p := ExtractPage(galleryEntry, "https://onepagelove.example/pete-sena", false)
	must(t, p.Status == "ready" && p.Work != nil && p.Work.Name == "Pete Sena" && *p.Work.Poster == "https://onepagelove.example/rec/poster.jpg" && *p.Work.Video == "https://onepagelove.example/rec/full.mp4", "%+v", p.Work)
}

func shot(id string) string { return "https://assets.awwwards.example/awards/element/2026/09/" + id }

const blankPNG = "data:image/png;base64,iVBORw0KGgo="

var awardPage = `<html><head><meta property="og:description" content="An immersive web journey through Agrumea Farm."><meta property="og:image" content="https://assets.awwwards.example/og.jpg"></head><body>
<div class="anchor-section" id="creator"><div class="head-site">
  <div class="box-score"><div class="box-score__top"><strong>SOTD</strong></div><div class="box-score__note"><strong>7.33</strong><sub>/10</sub></div></div>
  <div class="c-heading"><div class="c-heading__top"><h2 class="text-default">Site of the Day - Nov 5, 2026</h2></div>
  <div class="c-heading__middle"><h1 class="heading-1"><a href="https://agrumea.example/en">Agrumea Farm</a></h1></div>
  <div class="c-heading__bottom"><div class="head-site__credits"><div class="users-credits"><ul class="users-credits__details">
    <li><div class="users-credits__item"><figure class="avatar-name"><a class="avatar-name__link" href="/studio_k95/" aria-label="Studio K95"><figcaption class="avatar-name__name"><strong>Studio K95</strong></figcaption></a></figure></div></li>
    <li><div class="users-credits__item"><figure class="avatar-name"><a class="avatar-name__link" href="/ada/" aria-label="Ada"><figcaption class="avatar-name__name"><strong>Ada</strong></figcaption></a></figure></div></li>
  </ul></div></div></div></div>
  <ul class="gallery-site"><li><div class="box-figure"><img class="gallery-site__img lazy" src="` + blankPNG + `" data-src="https://assets.awwwards.example/awards/submissions/2026/09/agrumea.jpg" alt="Agrumea Farm"></div></li></ul>
</div>
<ul class="gallery-site gallery-site--two-cols"><li><div class="card-slide"><div class="box-figure"><figure class="figure-rollover">
  <a aria-label="Hero - Home" href="/inspiration/hero-home-agrumea-farm" class="figure-rollover__link"><video class="figure-rollover__file lazy" poster="` + blankPNG + `" data-poster="` + shot("hero_static.jpeg") + `" muted loop preload="none">
    <source src="https://assets.awwwards.example/assets/videos/blank.mp4" data-src="` + shot("hero.mp4") + `" type="video/mp4"></video></a></figure></div>
  <div class="card-slide__info"><h3 class="card-slide__title"><a href="/inspiration/hero-home-agrumea-farm">Hero - Home</a></h3><div class="card-slide__data"><small>from</small><a href="/elements/scroll/">Scroll</a></div></div></div></li></ul>
</div>
<div class="anchor-section" id="typography"><ul class="list-palette"><li><div class="list-palette__item" style="background: #762530;"><div class="list-palette__name"><strong>HEX</strong> #762530</div></div></li></ul></div>
<div class="anchor-section" id="details">
  <a target="_blank" href="https://agrumea.example/en?utm_source=awwwards&amp;lang=it" class="button" data-controller="visit-count">Visit Site</a>
  <ul class="list-tags">
      <li><strong><a href="/websites/food-drink/" class="button button--tag">Food &amp; Drink</a></strong></li>
          <li><strong><a href="/websites/animation/" class="button button--tag">Animation</a></strong></li>
      <li><strong><a href="/websites/3d/" class="button button--tag">3D</a></strong></li>
          <li><strong><a href="/websites/gsap/" class="button button--tag">GSAP</a></strong></li>
      <li><strong><a href="/websites/three-js/" class="button button--tag">Three.js</a></strong></li>
  </ul>
  <ul class="gallery-site gallery-site--two-cols">
    <li><div class="card-slide"><a aria-label="" href="/inspiration/mobile-agrumea-farm" class="figure-rollover__link"><img class="figure-rollover__file lazy" src="` + blankPNG + `" data-src="` + shot("mobile.jpg") + `" alt=""></a>
      <div class="card-slide__info"><h3 class="card-slide__title"><a href="/inspiration/mobile-agrumea-farm">Great element</a></h3><div class="card-slide__data"><small>from</small><a href="/elements/mobile_thumbnail/">Mobile thumbnail</a></div></div></div></li>
    <li><div class="card-slide"><a aria-label="" href="/inspiration/desktop-agrumea-farm" class="figure-rollover__link"><img class="figure-rollover__file lazy" src="` + blankPNG + `" data-src="" alt=""></a></div></li>
  </ul>
</div>
<div class="anchor-section" id="score"><div class="layout-overall">
  <div class="layout-overall__type">Design<strong>40%</strong></div><div class="layout-overall__type">Usability<strong>30%</strong></div><div class="layout-overall__type">Creativity<strong>20%</strong></div><div class="layout-overall__type">Content<strong>10%</strong></div>
  <div class="layout-overall__score"><strong>7.4 / 10</strong></div><div class="layout-overall__score"><strong>7.29 / 10</strong></div><div class="layout-overall__score"><strong>7.23 / 10</strong></div><div class="layout-overall__score"><strong>7.4 / 10</strong></div>
</div><div class="layout-overall"><div class="layout-overall__type">Accessibility</div><div class="layout-overall__score"><strong>7.60 / 10</strong></div></div></div>
</body></html>`

const awardURL = "https://www.awwwards.com/sites/agrumea-farm"

func TestAwwwardsAdapter(t *testing.T) {
	g := galleryPage(parseDocument(awardPage), awardURL)
	must(t, g != nil, "no work")
	if g == nil {
		return
	}
	w := g.work
	facts, sections := w.Facts, w.Sections
	w.Facts, w.Sections = nil, nil
	must(t, jsonOf(w) == jsonOf(Work{Name: "Agrumea Farm", Designer: ptr("Studio K95、Ada"), Site: ptr("https://agrumea.example/en?lang=it"), Host: ptr("agrumea.example"),
		Poster: ptr("https://assets.awwwards.example/awards/submissions/2026/09/agrumea.jpg")}), "no recording of its own: the lazy screenshot, not the placeholder or og:image: %s", jsonOf(w))
	want := []Fact{{"荣誉", []string{"Site of the Day · 2026-11-05"}}, {"评分", []string{"总分 7.33", "设计 7.4", "可用性 7.29", "创意 7.23", "内容 7.4"}},
		{"类别", []string{"Food & Drink"}}, {"标签", []string{"Animation", "3D"}}, {"技术", []string{"GSAP", "Three.js"}}, {"配色", []string{"#762530"}}}
	must(t, jsonOf(facts) == jsonOf(want), "%s", jsonOf(facts))
	must(t, g.html == "<p>An immersive web journey through Agrumea Farm.</p>", "%s", g.html)
	must(t, jsonOf(sections) == jsonOf([]Shot{{"Hero - Home", ptr(shot("hero_static.jpeg"))}, {"Mobile thumbnail", ptr(shot("mobile.jpg"))}}), "element frames, then screenshots; empty lazy slots are skipped: %s", jsonOf(sections))
	must(t, galleryPage(parseDocument(`<html><body><h1>Winning websites</h1><ul class="list-tags"><li>GSAP</li></ul></body></html>`), "https://www.awwwards.com/websites/") == nil, "a list page read as a work")
	other := galleryPage(parseDocument(awardPage), "")
	must(t, other == nil || other.work.Name == "", "other hosts keep the One Page Love reading")
}

// TestAwwwardsEntryPage is the extraction half of "an Awwwards feed item, one sentence under a picture, is read from
// its entry page"; choosing the page over the feed item and caching its media belong to internal/reading.
func TestAwwwardsEntryPage(t *testing.T) {
	p := ExtractPage(awardPage, awardURL, true)
	must(t, p.Status == "ready" && p.Work != nil && p.Work.Facts[0].Values[0] == "Site of the Day · 2026-11-05" && *p.Work.Poster == "https://assets.awwwards.example/awards/submissions/2026/09/agrumea.jpg" && p.Work.Video == nil, "%+v", p.Work)
}

// The intended difference: a single <article> holding little of the page (a note box) is not the root, and the page is
// read whole; an <article> holding the post still is.
func TestRootHoldsPageText(t *testing.T) {
	long := strings.Repeat("<p>"+prose3+"</p>", 6)
	note := `<html><head><title>Dark mode toggles</title></head><body><div class="post">` + long + `<article class="note"><p>A note beside the post, short.</p></article>` + long + `</div></body></html>`
	r := ExtractPage(note, tsBase, true)
	must(t, r.Status == "ready" && r.TextLength > 5000, "the post was cut to its note: %s %d", r.Status, r.TextLength)
	post := `<html><head><title>A useful story</title></head><body><nav>` + strings.Repeat("<a href='/x'>Menu item</a>", 3) + `</nav><div class="aside-ish">Sidebar words that are not the article.</div><article><h1>A useful story</h1>` + long + `</article></body></html>`
	r = ExtractPage(post, tsBase, true)
	must(t, r.Status == "ready" && strings.Contains(r.HTML, "useful public article") && !strings.Contains(r.HTML, "Sidebar words"), "the article root was not used: %s", r.HTML[:min(300, len(r.HTML))])
}
