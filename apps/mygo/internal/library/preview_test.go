package library

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"trie/internal/web"
)

// Article preview cases.

const previewBase = "https://writer.example/posts/hello"

var photo = "data:image/jpeg;base64," + strings.Repeat("A", 600)

func preview(html string, summary bool, title string) storedPreview {
	return articlePreview(html, title, previewBase, summary, nil)
}

func deref(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// a link back to the original is a stub, not an excerpt
func TestLinkBackIsStub(t *testing.T) {
	innei := preview(`<p>往原址览之：<a href="https://innei.in/notes/221">https://innei.in/notes/221</a></p>`, true, "Hello")
	if innei.Excerpt != nil || innei.Minutes != nil {
		t.Fatalf("innei: %v %v", deref(innei.Excerpt), innei.Minutes)
	}
	long := preview(`<p>往原址览之：<a href="https://innei.in/posts/tinkering/steins-gate-mv-with-claude-code">https://innei.in/posts/tinkering/steins-gate-mv-with-claude-code</a></p>`, true, "Hello")
	if long.Excerpt != nil {
		t.Fatalf("long: %v", deref(long.Excerpt))
	}
	announcement := preview(`<p><a href="https://nuxt.com/blog/nuxt-icon-v1-0">Go to nuxt.com and read the full announcement of the brand new version</a>.</p>`, false, "Hello")
	if announcement.Excerpt != nil || announcement.Minutes != nil {
		t.Fatalf("announcement: %v", deref(announcement.Excerpt))
	}
}

// a summary gives only the excerpt; a body also gives reading time
func TestSummaryHasNoReadingTime(t *testing.T) {
	html := "<p>This feed only carries the opening paragraph of a much longer essay about local-first software.</p>"
	summary := preview(html, true, "Hello")
	equal(t, []any{deref(summary.Excerpt), summary.Minutes == nil}, []any{html[3 : len(html)-4], true}, "summary")
	equal(t, *preview(html, false, "Hello").Minutes, 1, "minutes")
}

// excerpt drops a repeated title, decodes text and cuts cleanly
func TestExcerptDropsTitleAndCuts(t *testing.T) {
	body := `<h1>Hello, World!</h1><script>alert(1)</script><style>p{}</style><p>Tom &amp; Jerry&#39;s guide &mdash; the first part, on cheese and mice.</p><pre>const hidden = 1</pre>`
	equal(t, *preview(body, false, "Hello World").Excerpt, "Tom & Jerry's guide — the first part, on cheese and mice.", "decoded")
	equal(t, *preview(`<h1>十月未央</h1><p>从上次总结也已经过去了半个多月之久，是时候又来说说最近发生的一些事情和想法了，就从旅行说起。</p>`, false, "十月未央").Excerpt,
		"从上次总结也已经过去了半个多月之久，是时候又来说说最近发生的一些事情和想法了，就从旅行说起。", "chinese")
	// A title that is only a prefix of the first word is kept.
	if e := *preview("<p>Hellos from the other side of the world, written while waiting for a train.</p>", false, "Hello").Excerpt; !strings.HasPrefix(e, "Hellos") {
		t.Fatalf("prefix title: %q", e)
	}
	sentences := "The first sentence is long enough to stand on its own here. " + "Then a second one keeps going well past the limit without any stop at all and on and on"
	equal(t, excerpt(sentences, 140), "The first sentence is long enough to stand on its own here.", "sentence cut")
	words := strings.TrimSpace(strings.Repeat("word ", 40)) + ", and more"
	if cut := excerpt(words, 140); web.JSLen(cut) > 141 || !strings.HasSuffix(cut, "word…") || strings.Contains(cut, ",…") {
		t.Fatalf("word cut %q", cut)
	}
	han := strings.Repeat("没有标点的长句", 30)
	equal(t, excerpt(han, 140), string([]rune(han)[:140])+"…", "han cut")
	// Lengths are UTF-16: an emoji counts twice, as in JavaScript.
	emoji := strings.Repeat("😀", 100)
	equal(t, excerpt(emoji, 140), strings.Repeat("😀", 70)+"…", "emoji cut")
}

// reading time uses the reader rule and skips code
func TestReadingTimeSkipsCode(t *testing.T) {
	chinese := preview("<p>"+strings.Repeat("阅读时间", 300)+"</p><pre>"+strings.Repeat("代码", 4000)+"</pre>", false, "Hello")
	equal(t, *chinese.Minutes, 3, "1200 Han / 400")
	english := preview("<p>"+strings.Repeat("word ", 690)+"</p><pre>"+strings.Repeat("code ", 5000)+"</pre>", false, "Hello")
	equal(t, *english.Minutes, 3, "690 words / 230")
	equal(t, *preview("<p>"+strings.Repeat("短", 50)+"</p>", false, "Hello").Minutes, 1, "short")
	equal(t, [2]int{english.Images, english.CodeBlocks}, [2]int{0, 1}, "counts")
}

// lead image skips trackers and decoration and resolves relative sources
func TestLeadImageSkipsDecoration(t *testing.T) {
	lead := func(html string) string {
		if p := pictures(shorten(html), previewBase, 1); len(p) > 0 {
			return p[0]
		}
		return ""
	}
	html := `<img src="https://stats.example/pixel.gif" width="1" height="1"><img class="emoji" src="/smile.png"><img src="/avatar.png" width="40">` +
		`<img src="/logo.svg"><img src="../images/cover.jpg?w=800" alt="cover"><img src="/second.jpg">`
	equal(t, lead(html), "https://writer.example/images/cover.jpg?w=800", "lead")
	equal(t, lead(`<img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=">`), "", "spacer")
	equal(t, lead(`<img src="`+photo+`">`), "inline:0", "an inlined photo is kept by position, not copied")
	equal(t, inlinePicture(`<img src="x.png"><img src="`+photo+`">`, "inline:1"), photo, "inline picture")
	equal(t, pictures(shorten(`<img src="/a.jpg"><img width="16" src="/icon.png"><img src="/a.jpg"><img src="/b.jpg"><img src="/c.jpg"><img src="/d.jpg"><img src="/e.jpg">`), previewBase, 4),
		[]string{"https://writer.example/a.jpg", "https://writer.example/b.jpg", "https://writer.example/c.jpg", "https://writer.example/d.jpg"}, "up to four, skipping icons and repeats")
	equal(t, lead(`<img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" data-src="/lazy.jpg">`), "https://writer.example/lazy.jpg", "lazy")
	counted := preview("<p>"+strings.Repeat("Some text to read here. ", 5)+"</p>"+html+"<pre>a</pre><pre>b</pre>", false, "Hello")
	equal(t, []any{counted.Images, counted.CodeBlocks, counted.HasImage}, []any{6, 2, true}, "counts")
}

// a page with its long data URLs cut reads as the page itself
func TestShortenedPageReadsAsSource(t *testing.T) {
	dataURL := regexp.MustCompile(`data:[^"'` + web.Space + `>]+`)
	long := strings.Repeat("QUJD", 100)
	pages := []string{
		`<img src="data:image/png;base64,` + long + `" width="12"><img class='data:image/png;base64,` + long + `/icon' src="data:image/gif;base64,` + long + `">`,
		`<img src=data:image/webp;base64,` + long + ` data-src="/lazy.jpg"><img src="https://cdn.example/x?u=data:image/png;base64,` + long + `">`,
		`<p>data:text/html,<img/src=` + long + `></p><img src="` + photo + `">`, // the cut takes a tag with it
		"<img src=\"data:image/jpeg;base64," + long + "\u3000x\" alt=\"a\u00a0b\"><img src='data:image/png;base64," + long + "\xff\xe2\x80" + long + "'>",
		`<img src="data:image/png;base64,` + long + `&amp;` + long + `"><img SRC = "data:image/png;base64,` + long + `" HEIGHT=20>`,
		`<img src="data:` + strings.Repeat("中", 251) + `"><img src="data:` + strings.Repeat("中", 252) + `"><img src="data:` + strings.Repeat("😀", 126) + `">`,
		`<p>data:` + long + `</p><img src="data:` + long,
	}
	for i, html := range pages {
		want := dataURL.ReplaceAllStringFunc(html, func(url string) string {
			if web.JSLen(url) > 256 {
				return "data:"
			}
			return url
		})
		short, whole := shorten(html), page{source: html, html: html}
		equal(t, short.html, want, fmt.Sprint("html ", i))
		sp, st := short.imgTags()
		wp, wt := whole.imgTags()
		equal(t, len(st), len(wt), fmt.Sprint("tags ", i))
		for n := range wt {
			for _, name := range []string{"width", "height", "class", "data-src", "src"} {
				a, okA := sp.attribute(st[n], name)
				b, okB := wp.attribute(wt[n], name)
				equal(t, []any{a, okA}, []any{b, okB}, fmt.Sprint("page ", i, " tag ", n, " ", name))
			}
		}
		equal(t, pictures(short, previewBase, 10), pictures(whole, previewBase, 10), fmt.Sprint("pictures ", i))
	}
}

func previewSnapshot() Snapshot {
	return Snapshot{Version: 1, AsOf: "2026-11-02",
		Sources:  []SourceSnapshot{{ID: "writer", Title: "Writer", HomeURL: "https://writer.example/", Host: "writer.example", SampledCount: 1, CatalogCandidateCount: 1, ScopeLabel: "test"}},
		Articles: []ArticleSnapshot{{ID: previewBase, SourceID: "writer", Title: "Hello", URL: previewBase, Authors: []string{}, Aliases: []string{}, BodyStatus: "not_extracted", VerificationLabel: "test"}},
		Summary:  Summary{SampledCount: 1, ArticleCount: 1}}
}

func entryOf(a ArticleSnapshot, body *FeedBody) FeedEntry {
	return FeedEntry{ID: a.ID, URL: a.URL, Title: a.Title, Authors: a.Authors, PublishedAt: a.PublishedAt, ModifiedAt: a.ModifiedAt,
		Aliases: a.Aliases, BodyStatus: a.BodyStatus, VerificationLabel: a.VerificationLabel, FeedBody: body}
}

func previewFeed(html, kind, version string) ParsedFeed {
	return ParsedFeed{FeedURL: "https://writer.example/feed", HomeURL: "https://writer.example/", Title: "Writer", FetchedAt: "2026-11-02T00:00:00Z", Warnings: []string{},
		Articles: []FeedEntry{entryOf(previewSnapshot().Articles[0], &FeedBody{HTML: html, Kind: kind, Version: version, FeedURL: "https://writer.example/feed"})}}
}

func pageContent(html string) ArticleContent {
	return ArticleContent{ArticleID: previewBase, Status: "ready", HTML: html, TextLength: 1, Links: []string{}, FetchedAt: "2026-11-02T00:00:00Z", SourceURL: previewBase, SourceKind: "page"}
}

func importFeed(t *testing.T, s *Store, sourceID string, feed ParsedFeed) SubscriptionResult {
	t.Helper()
	return must[SubscriptionResult](t)(s.ImportFeed(sourceID, feed, true, nil, ""))
}

// library exposes the preview of the best stored body without the lead image source
func TestLibraryPreviewFromBestBody(t *testing.T) {
	path := libraryPath(t)
	s := mustOpen(t, path, previewSnapshot())
	if lib(t, s).Articles[0].Preview != nil {
		t.Fatal("a preview before any body")
	}
	importFeed(t, s, "writer", previewFeed(`<p>往原址览之：<a href="https://innei.in/notes/221">https://innei.in/notes/221</a></p>`, "summary", "v1"))
	equal(t, *lib(t, s).Articles[0].Preview, ArticlePreview{}, "stub preview")
	importFeed(t, s, "writer", previewFeed("<p>"+strings.Repeat("A real feed body with words. ", 10)+`</p><img src="/cover.jpg">`, "body", "v2"))
	fromFeed := *lib(t, s).Articles[0].Preview
	equal(t, []any{*fromFeed.Minutes, fromFeed.Images, fromFeed.HasImage}, []any{1, 1, true}, "feed preview")
	if !strings.HasPrefix(*fromFeed.Excerpt, "A real feed body") {
		t.Fatalf("excerpt %q", *fromFeed.Excerpt)
	}
	if b, _ := json.Marshal(lib(t, s).Articles[0]); strings.Contains(string(b), `"sources"`) {
		t.Fatal("the snapshot carries picture sources")
	}
	equal(t, must[string](t)(s.Picture(previewBase, 0)), "https://writer.example/cover.jpg", "picture")
	if err := s.SaveContent(pageContent("<h1>Hello</h1><p>" + strings.Repeat("阅读", 400) + `</p><img src="` + photo + `"><pre>x</pre>`)); err != nil {
		t.Fatal(err)
	}
	fromContent := *lib(t, s).Articles[0].Preview
	equal(t, []any{*fromContent.Minutes, fromContent.CodeBlocks, string([]rune(*fromContent.Excerpt)[:2])}, []any{2, 1, "阅读"}, "content preview")
	equal(t, must[string](t)(s.Picture(previewBase, 0)), photo, "inline picture")
	// Rows missing from an older library are rebuilt when it is opened.
	s.Close()
	if _, err := raw(t, path).Exec("DELETE FROM article_previews"); err != nil {
		t.Fatal(err)
	}
	equal(t, *lib(t, mustOpen(t, path, previewSnapshot())).Articles[0].Preview, fromContent, "rebuilt preview")
}

// a read work page keeps its first screen and screenshots as its pictures
func TestWorkPagePictures(t *testing.T) {
	s := mustOpen(t, libraryPath(t), previewSnapshot())
	shot := "https://gallery.example/pricing.jpg"
	work := `{"name":"Hello","designer":null,"site":null,"host":null,"builtWith":null,"facts":[],"poster":"` + photo + `","video":null,` +
		`"sections":[{"name":"Hero","image":null,"source":"` + shot + `"},{"name":"Lost","image":null,"source":null},{"name":"Footer","image":"` + photo + `"}]}`
	c := pageContent("<p>A landing page for a design studio, picked by the gallery.</p>")
	c.Work = json.RawMessage(work)
	if err := s.SaveContent(c); err != nil {
		t.Fatal(err)
	}
	p := lib(t, s).Articles[0].Preview
	equal(t, []any{p.Images, p.HasImage, p.Gallery}, []any{3, true, 3}, "work preview")
	var got []string
	for i := range 4 {
		got = append(got, must[string](t)(s.Picture(previewBase, i)))
	}
	equal(t, got, []string{photo, shot, photo, ""}, "pictures")
	// The work survives the cache as stored, including a section source left null.
	equal(t, string(must[*ArticleContent](t)(s.Content(previewBase)).Work), work, "cached work")
}

func TestPlainText(t *testing.T) {
	equal(t, plainText("<p>a</p><p>b&lt;c</p><!-- x --><div class=\"reader-code-header\">copy</div>"), "a b<c", "plain text")
}
