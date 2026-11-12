package extract

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"trie/internal/library"
)

// The cases of svg-image.test.ts, reader-format.test.ts and reader-document.test.ts. Documents are checked by saving them
// through internal/library, whose SaveContent runs reader-document.ts's isReaderDocument (check.go).

func svgDoc(body string, attrs ...string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" ` + strings.Join(attrs, "") + `>` + body + `</svg>`
}

func TestSVGKeepsGeometry(t *testing.T) {
	r := SanitizeSVG(`<?xml version="1.0"?>` + svgDoc(`<defs><linearGradient id="paint" gradientUnits="userSpaceOnUse"><stop offset="0%" stop-color="#abc"/><stop offset="100%" stop-color="rgb(1, 2, 3)"/></linearGradient><path id="mark" d="M0 0L20 20"/></defs><use xmlns:xlink="http://www.w3.org/1999/xlink" xlink:href="#mark" fill="url(#paint)"/><text x="0" y="20" font-family="Arial" font-size="12">A &amp; B<tspan dx="2">Logo</tspan></text>`, `viewBox="0 0 24 24"`))
	must(t, r != "", "refused")
	for _, want := range []string{`viewBox="0 0 24 24"`, `gradientUnits="userSpaceOnUse"`, `href="#mark"`, `fill="url(#paint)"`, `A &amp; B<tspan dx="2">Logo</tspan>`} {
		must(t, strings.Contains(r, want), "%s missing in %s", want, r)
	}
	must(t, !match(`xlink|<\?xml`, r) && SanitizeSVG(r) == r, "%s", r)
}

func TestSVGDropsActiveContent(t *testing.T) {
	r := SanitizeSVG(svgDoc(`<script>alert(1)</script><style>path{fill:url(https://evil.example)}</style><foreignObject><div xmlns="http://www.w3.org/1999/xhtml">Secret</div></foreignObject><animate attributeName="href" to="https://evil.example"/><g onclick="evil()" onload="evil()" style="fill:red" unknown="value"><path d="M0 0"/></g><html:g xmlns:html="http://www.w3.org/1999/xhtml"><path>Foreign secret</path></html:g>`, `onload="evil()"`))
	// A style declaration for a known attribute becomes that attribute, checked like any other; the style attribute itself never survives.
	must(t, strings.Contains(r, `<g fill="red"><path d="M0 0"/></g>`), "%s", r)
	must(t, !match(`script|style|foreignObject|Secret|animate|evil|onclick|onload|unknown|Foreign|html`, r), "%s", r)
}

func TestSVGResolvesVariables(t *testing.T) {
	// The shape of server-rendered Mermaid diagrams (Innei's blog): colours set as var() and defined in the root's style.
	r := SanitizeSVG(svgDoc(`<rect width="10" height="10" fill="var(--_s)" style="rx:var(--_r)"/><path d="M0 0L9 9" fill="none" stroke="var(--_l)"/><text fill="var(--_fg)">Label</text><rect fill="var(--leak)"/><rect fill="var(--missing)"/>`,
		`style="--pele-surface:#f3f3f3;--_s:var(--pele-surface,#eee);--_l:var(--pele-line,#1f1f1f);--_fg:#262626;--_r:4px;--leak:url(https://evil.example/x);color:var(--_fg);font:16px sans-serif"`))
	must(t, match(`<svg[^>]*color="#262626"[^>]*>`, r) && strings.Contains(r, `font-size="16px"`) && strings.Contains(r, `font-family="sans-serif"`), "%s", r)
	must(t, strings.Contains(r, `<rect width="10" height="10" fill="#f3f3f3" rx="4px"/>`), "a variable resolves through its own fallback chain: %s", r)
	must(t, strings.Contains(r, `stroke="#1f1f1f"`), "an undefined variable takes its fallback")
	must(t, strings.Contains(r, `<text fill="#262626">Label</text>`) && !match(`evil|url\(http|var\(|style=`, r), "%s", r)
}

func TestSVGLocalReferencesOnly(t *testing.T) {
	for _, value := range []string{`https://evil.example/a`, `//evil.example/a`, `data:image/svg+xml,bad`, `javascript:alert(1)`, `url(https://evil.example/a)`, `url(//evil.example/a)`, `url(\68 ttps://evil.example/a)`, `u\72l(#paint)`, `url(%23paint)`, `url(#paint) red`, `url(#paint);color:red`} {
		escaped := strings.NewReplacer("&", "&amp;", `"`, "&quot;").Replace(value)
		r := SanitizeSVG(svgDoc(fmt.Sprintf(`<use href="%[1]s"/><path fill="%[1]s" stroke="%[1]s" clip-path="%[1]s" mask="%[1]s" filter="%[1]s"/>`, escaped)))
		must(t, r != "" && !match(`href=|fill=|stroke=|clip-path=|mask=|filter=`, r), "%s: %s", value, r)
	}
	decoded := SanitizeSVG(svgDoc(`<use href="&#104;ttps://evil.example"/><path fill="u&#114;l(https://evil.example)"/>`))
	must(t, !match(`href=|fill=`, decoded), "%s", decoded)
	must(t, strings.Contains(SanitizeSVG(svgDoc(`<path fill="currentColor" stroke="#12ab34" clip-path="url('#clip')"/>`)), `clip-path="url('#clip')"`), "a quoted local paint URL")
}

func TestSVGRejectsDeclarations(t *testing.T) {
	for _, source := range []string{
		`<!DOCTYPE svg SYSTEM "https://evil.example/a">` + svgDoc(""),
		`<!DOCTYPE svg [<!ENTITY x "expanded">]>` + svgDoc(`<text>&x;</text>`),
		`<?xml-stylesheet href="https://evil.example/a"?>` + svgDoc(""),
		svgDoc(`<?other data?>`),
		`<svg xmlns="http://www.w3.org/2000/svg"><path></svg>`,
		`<svg/>`, `<svg xmlns="http://www.w3.org/1999/xhtml"/>`, `<g xmlns="http://www.w3.org/2000/svg"/>`,
	} {
		must(t, SanitizeSVG(source) == "", "accepted %s", source)
	}
}

func TestSVGLimits(t *testing.T) {
	must(t, SanitizeSVG(svgDoc(`<text>`+strings.Repeat("猫", 90_000)+`</text>`)) == "", "oversized UTF-8")
	must(t, SanitizeSVG(svgDoc(`<text><![CDATA[`+strings.Repeat("<", 70_000)+`]]></text>`)) == "", "output over the limit")
	must(t, SanitizeSVG(svgDoc(strings.Repeat(`<path/>`, 5000))) == "", "5000 children")
	must(t, SanitizeSVG(svgDoc(strings.Repeat(`<g>`, 64)+strings.Repeat(`</g>`, 64))) == "", "depth 65")
	must(t, SanitizeSVG(svgDoc(strings.Repeat(`<path/>`, 4999))) != "", "4999 children")
	must(t, SanitizeSVG(svgDoc(strings.Repeat(`<g>`, 63)+strings.Repeat(`</g>`, 63))) != "", "depth 64")
}

func TestSVGNamespaces(t *testing.T) {
	r := SanitizeSVG(`<s:svg xmlns:s="http://www.w3.org/2000/svg" xmlns:evil="https://evil.example" evil:href="https://evil.example" xml:base="https://evil.example"><s:path evil:fill="red" d="M0 0"/></s:svg>`)
	must(t, r == strings.Replace(svgDoc(`<path d="M0 0"/>`), " >", ">", 1) && !match(`evil|xml:base|s:`, r), "%s", r)
}

func TestSVGUseReferences(t *testing.T) {
	for _, body := range []string{
		`<use id="self" href="#self"/>`,
		`<g id="tree"><use href="#tree"/><use href="#tree"/></g><use href="#tree"/>`,
		`<g id="a"><path d="M0 0"/></g><g id="b"><use href="#a"/><use href="#a"/></g><use href="#b"/>`,
		`<path id="duplicate"/><path id="duplicate"/><use href="#duplicate"/>`,
		`<use href="#missing"/>`,
		`<text id="words">Logo</text><use href="#words"/>`,
		`<path id="nested"><title>Geometry child</title></path><use href="#nested"/>`,
		`<foreignObject><path xmlns="http://www.w3.org/1999/xhtml" id="foreign"/></foreignObject><use href="#foreign"/>`,
	} {
		r := SanitizeSVG(svgDoc(body))
		must(t, r != "" && !match(`<use\b`, r), "%s: %s", body, r)
	}
	valid := SanitizeSVG(svgDoc(`<defs><path id="mark" d="M0 0L10 10"/></defs><use href="#mark" x="5"/>`))
	must(t, strings.Contains(valid, `<use href="#mark" x="5"/>`) && SanitizeSVG(valid) == valid, "%s", valid)
	conflicting := SanitizeSVG(svgDoc(`<defs><path id="mark"/></defs><g id="recursive"><use href="#recursive"/></g><use xmlns:xlink="http://www.w3.org/1999/xlink" href="#mark" xlink:href="#recursive"/>`))
	must(t, strings.Contains(conflicting, `<use href="#mark"/>`) && !strings.Contains(conflicting, `href="#recursive"`), "%s", conflicting)
	spaced := SanitizeSVG(svgDoc(`<g id=" mark "><use href="#mark"/></g><path id="mark"/>`))
	must(t, strings.Count(spaced, `id="mark"`) == 1, "%s", spaced)
}

// reader-format.test.ts

const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j5T8AAAAASUVORK5CYII="

func format(t *testing.T, page string, snapshot ...*library.ReaderSourceSnapshot) Formatted {
	t.Helper()
	var s *library.ReaderSourceSnapshot
	if len(snapshot) > 0 {
		s = snapshot[0]
	}
	f, err := FormatArticleHTML(page, s)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// readerDoc is the stored document as the reader reads it.
type readerDoc struct {
	Version         int    `json:"version"`
	ContentRevision string `json:"contentRevision"`
	UnitsRevision   string `json:"unitsRevision"`
	Provenance      string `json:"provenance"`
	Root            struct {
		Children []docNode `json:"children"`
	} `json:"root"`
	Units []struct {
		ID, Kind, Text, Anchor string
	} `json:"units"`
	CodeTokens map[string][]struct{ Text, ClassName string } `json:"codeTokens"`
}

type docNode struct {
	Type       string         `json:"type"`
	Tag        string         `json:"tagName"`
	Value      string         `json:"value"`
	Properties map[string]any `json:"properties"`
	Children   []docNode      `json:"children"`
}

func decode(t *testing.T, f Formatted) (readerDoc, string) {
	t.Helper()
	var d readerDoc
	if err := json.Unmarshal(f.Document, &d); err != nil {
		t.Fatal(err)
	}
	var root struct {
		Root json.RawMessage `json:"root"`
	}
	json.Unmarshal(f.Document, &root)
	return d, string(root.Root)
}

var formatSnapshot = library.Snapshot{Version: 1, AsOf: "2026-11-02",
	Sources: []library.SourceSnapshot{{ID: "writer", Title: "Writer", HomeURL: "https://writer.example/", Host: "writer.example", SampledCount: 1, CatalogCandidateCount: 1, ScopeLabel: "test"}},
	Articles: []library.ArticleSnapshot{{ID: "article", SourceID: "writer", Title: "Article", URL: "https://writer.example/article", Authors: []string{"Writer"}, Aliases: []string{},
		BodyStatus: "not_extracted", VerificationLabel: "test"}},
	ReviewItems: []library.ReviewItem{}, Summary: library.Summary{SampledCount: 1, ArticleCount: 1}}

func cachedContent(f Formatted) library.ArticleContent {
	return library.ArticleContent{ArticleID: "article", Status: "ready", HTML: f.HTML, TextLength: 123, Links: []string{"https://writer.example/reference"},
		FetchedAt: "2026-11-01T00:00:00Z", SourceURL: "https://writer.example/article", SourceKind: "page", ImageCacheVersion: ptr(2), MissingImageCount: ptr(0),
		Document: f.Document, FormatVersion: f.FormatVersion, Headings: f.Headings, CodeBlocks: f.CodeBlocks, HighlightStyles: f.HighlightStyles}
}

// storable is whether the library accepts f's document (isReaderDocument).
func storable(t *testing.T, f Formatted) bool {
	t.Helper()
	s, err := library.Open(filepath.Join(t.TempDir(), "library.sqlite"), formatSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	return s.SaveContent(cachedContent(f)) == nil
}

func TestHeadingIDsAndTOC(t *testing.T) {
	page := `<p>[[toc]]</p><p>[TOC]</p><h2 id="keep">保留原锚点</h2><h2>重复标题</h2><h3>重复标题</h3><h2 id="keep">重复 ID</h2><h2 id="keep-1">Unique anchor</h2><a href="#keep-1">Go to unique heading</a><p>Explain [[toc]] here.</p><p><code>[[toc]]</code></p><blockquote><p>[[toc]]</p></blockquote>`
	first, again := format(t, page), format(t, page)
	must(t, slices.Equal(first.Headings, again.Headings) && first.Headings[0].ID == "keep", "%v", first.Headings)
	ids := map[string]bool{}
	for _, h := range first.Headings {
		ids[h.ID] = true
	}
	must(t, len(ids) == 5, "%v", first.Headings)
	must(t, strings.Contains(first.HTML, `id="keep-1">Unique anchor</h2>`), "%s", first.HTML)
	must(t, !match(`<p>\[\[toc\]\]</p>|<p>\[TOC\]</p>`, strings.Split(first.HTML, "<h2")[0]), "standalone placeholders: %s", first.HTML)
	must(t, strings.Contains(first.HTML, "Explain [[toc]] here") && strings.Contains(first.HTML, "<code>[[toc]]</code>") && strings.Contains(first.HTML, "<blockquote><p>[[toc]]</p></blockquote>"), "%s", first.HTML)
}

func TestCallouts(t *testing.T) {
	kinds := []string{"NOTE", "TIP", "IMPORTANT", "WARNING", "CAUTION"}
	var page strings.Builder
	for _, k := range kinds {
		fmt.Fprintf(&page, `<blockquote><p>[!%s]<br>Keep this content.</p></blockquote>`, k)
	}
	page.WriteString(`<blockquote><p>[!CUSTOM]<br>Custom content</p></blockquote><blockquote><p><code>[!NOTE]</code> is literal</p></blockquote><p>[!NOTE] is ordinary prose.</p><pre><code>[!NOTE]` + "\n" + `[[toc]]` + "\n" + `</code></pre>`)
	r := format(t, page.String())
	for _, k := range kinds {
		must(t, strings.Contains(r.HTML, `<blockquote class="reader-callout reader-`+strings.ToLower(k)+`"><p class="reader-callout-title">`), "%s callout", k)
	}
	must(t, strings.Count(r.HTML, `class="reader-callout `) == 5, "%s", r.HTML)
	must(t, strings.Contains(r.HTML, "[!CUSTOM]") && strings.Contains(r.HTML, "[!NOTE] is ordinary prose") && r.CodeBlocks[0].Code == "[!NOTE]\n[[toc]]\n", "%s", r.HTML)
}

// TestCodeTextAndLanguage is "highlights real code while preserving its exact copy text and plain-language fallback"
// without Shiki (decided 2026-11-09): the copy text and languages are the same, the code carries no token spans.
func TestCodeTextAndLanguage(t *testing.T) {
	code := "  const value = 42;\n\n"
	r := format(t, `<pre class="language-ts"><code>`+code+`</code></pre><pre><code class="language-not-a-real-language">  raw`+"\n"+`</code></pre>`)
	must(t, r.CodeBlocks[0].Code == code && r.CodeBlocks[0].Language == "ts" && r.CodeBlocks[1].Code == "  raw\n" && r.CodeBlocks[1].Language == "text", "%+v", r.CodeBlocks)
	must(t, len(r.HighlightStyles) == 0 && strings.Count(r.HTML, "data-reader-code=") == 2, "%s", r.HTML)
	must(t, strings.Contains(r.HTML, `<code id="reader-code-1" class="language-ts">`+code+`</code>`) && !strings.Contains(r.HTML, "reader-token-"), "%s", r.HTML)
}

// TestCodeLeftForReaderToColour is "code tokens are labelled by role so the reader owns the palette" as it stands now:
// the formatter labels nothing and stores no tokens for its blocks, so internal/reader colours them with MyGo's
// tokenizers in the same roles (reader/code.go kindRole).
func TestCodeLeftForReaderToColour(t *testing.T) {
	r := format(t, `<pre><code class="language-json">{ "name": "trie", "count": 3 }`+"\n"+`</code></pre><pre><code class="language-js">// note`+"\n"+`console.log('hi')`+"\n"+`</code></pre>`)
	d, _ := decode(t, r)
	must(t, r.CodeBlocks[0].Language == "json" && r.CodeBlocks[1].Language == "js" && len(d.CodeTokens) == 0 && !strings.Contains(r.HTML, "reader-token-"), "%v %s", d.CodeTokens, r.HTML)
}

func TestCodeLineBreaks(t *testing.T) {
	r := format(t, `<pre><code>first<br>  second`+"\n"+`</code></pre><pre><code><div>one</div><div>two</div></code></pre>`)
	must(t, r.CodeBlocks[0].Code == "first\n  second\n" && r.CodeBlocks[1].Code == "one\ntwo", "%+v", r.CodeBlocks)
}

func TestCachedImagesSurvive(t *testing.T) {
	svg := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10" onload="bad()"><script>bad()</script><rect width="10" height="10" fill="red"/></svg>`))
	r := format(t, `<script>bad()</script><style>body{display:none}</style><p onclick="bad()" style="color:red">Public</p><img src="`+tinyPNG+`" alt="Local"><img src="data:image/svg+xml;base64,`+svg+`"><img src="https://example.com/track"><img src="data:text/html;base64,AAAA"><button data-reader-code="fake">Fake copy</button><a href="javascript:bad()">Bad</a><a href="https://user:secret@example.com/">Credentials</a>`)
	shown := regexp.MustCompile(`<img src="([^"]+)"`).FindAllStringSubmatch(r.HTML, -1)
	must(t, len(shown) == 2 && shown[0][1] == tinyPNG, "the remote and non-image sources keep their place without a source: %s", r.HTML)
	if len(shown) == 2 {
		clean, _ := base64.StdEncoding.DecodeString(strings.SplitN(shown[1][1], ",", 2)[1])
		must(t, !match(`script|onload|bad`, string(clean)), "%s", clean)
	}
	must(t, !match(`<script|<style|onclick|style=|data:text|javascript:|secret|Fake copy`, r.HTML) && strings.Count(r.HTML, "<img") == 4, "%s", r.HTML)
}

func TestPhotoHeavyBodies(t *testing.T) {
	photo := make([]byte, 700_000)
	copy(photo, []byte{255, 216, 255})
	data := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(photo)
	var page strings.Builder
	page.WriteString("<h2>Photo diary</h2>")
	for i := range 17 {
		fmt.Fprintf(&page, `<figure><img src="%s" alt="Photo %d"><figcaption>Caption %d</figcaption></figure>`, data, i, i)
	}
	r := format(t, page.String())
	must(t, strings.Count(r.HTML, "<img ") == 17 && strings.Count(r.HTML, "data:image/jpeg;base64,") == 17 && !strings.Contains(r.HTML, "reader-image:"), "images")
	must(t, strings.Contains(r.HTML, "Photo 16") && strings.Contains(r.HTML, "Caption 16"), "the last photo")
}

func TestFormatIdempotent(t *testing.T) {
	first := format(t, `<p>[[toc]]</p><h2>Heading</h2><blockquote><p>[!NOTE]`+"\n"+`A note.</p></blockquote><pre><code class="language-json">{ "value": 1 }`+"\n"+`</code></pre><table><tr><th>A</th><td>B</td></tr></table><img src="`+tinyPNG+`">`)
	second := format(t, first.HTML)
	must(t, second.HTML == first.HTML, "%s\n%s", first.HTML, second.HTML)
	must(t, slices.Equal(second.Headings, first.Headings) && slices.Equal(second.CodeBlocks, first.CodeBlocks) && slices.Equal(second.HighlightStyles, first.HighlightStyles), "metadata changed")
}

func TestFormatLimits(t *testing.T) {
	code := strings.Repeat("const value = 42;\n", 1500)
	r := format(t, `<pre><code class="language-js">`+code+`</code></pre>`)
	must(t, r.CodeBlocks[0].Code == code && len(r.HighlightStyles) == 0, "large code")
	_, err := FormatArticleHTML(strings.Repeat("x", 24*1024*1024+1), nil)
	must(t, err != nil, "an oversized body formatted")
	_, err = FormatArticleHTML(strings.Repeat("<div>", 150)+"Deep"+strings.Repeat("</div>", 150), nil)
	must(t, err != nil, "a body nested 150 deep formatted")
}

// TestFormatUpgradePersists is the formatter's half of "offline cache format upgrade persists metadata and images
// without changing user state": a cached body formatted again saves and reloads whole, the favourite kept. Deciding when
// to reformat is reader-service's, ported with it.
func TestFormatUpgradePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.sqlite")
	s, err := library.Open(path, formatSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mutate(library.ToggleFavorite{ID: "article"}); err != nil {
		t.Fatal(err)
	}
	f := format(t, `<p>[[toc]]</p><h2>Offline heading</h2><blockquote><p>[!NOTE]<br>Cached note</p></blockquote><img src="`+tinyPNG+`">`)
	must(t, f.FormatVersion == FormatVersion && len(f.Headings) == 1 && strings.Contains(f.HTML, "reader-callout reader-note") && strings.Contains(f.HTML, tinyPNG), "%s", f.HTML)
	if err := s.SaveContent(cachedContent(f)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = library.Open(path, formatSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := s.Content("article")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	must(t, slices.Equal(c.Headings, f.Headings) && c.HTML == f.HTML && len(c.Document) > 0, "content changed on reopening")
	state, _ := s.Library()
	must(t, state.Articles[0].Favorite, "the favourite was lost")
}

// reader-document.test.ts

func TestDocumentSemanticNodes(t *testing.T) {
	r := format(t, `<script>bad()</script><div><h2 id="section">Section</h2><blockquote><p>[!NOTE]<br>Useful.</p></blockquote><table><caption>Rates</caption><tr><th scope="col">Name</th><td><strong>Value</strong></td></tr></table><p onclick="bad()">Text <a href="javascript:bad()">unsafe</a> <a href="https://example.com/ok" title="safe">safe</a></p><img src="`+tinyPNG+`" alt="Picture"><button>Fake</button></div>`)
	d, root := decode(t, r)
	must(t, storable(t, r) && d.Provenance == "legacy-cache", "invalid document")
	tampered := r
	tampered.Document = json.RawMessage(strings.Replace(string(r.Document), `"dataCallout":"NOTE"`, `"dataCallout":"BAD"`, 1))
	must(t, !storable(t, tampered), "the library's check let a damaged document through")
	must(t, !match(`reader-table-scroll|reader-callout-title|button|script|onclick|javascript:|reader-image:`, root) && strings.Contains(root, `"dataCallout":"NOTE"`) && strings.Contains(root, `"tagName":"table"`) && strings.Contains(root, `"src":"data:image/png;base64,`), "%s", root)
	var kinds []string
	for _, u := range d.Units {
		kinds = append(kinds, u.Kind)
	}
	must(t, slices.Equal(kinds, []string{"heading", "paragraph", "caption", "cell", "cell", "paragraph", "image"}) && d.Units[0].Anchor == "section", "%v", kinds)
}

func TestStableUnitIDs(t *testing.T) {
	original, _ := decode(t, format(t, `<p>Same</p><p>Different</p><p>Same</p>`))
	inserted, _ := decode(t, format(t, `<p>Inserted</p><p>Same</p><p>Different</p><p>Same</p>`))
	ids := func(d readerDoc) (out []string) {
		for _, u := range d.Units {
			out = append(out, u.ID)
		}
		return out
	}
	must(t, slices.Equal(ids(inserted)[1:], ids(original)) && original.Units[0].ID != original.Units[2].ID && original.UnitsRevision != inserted.UnitsRevision, "%v %v", ids(original), ids(inserted))
}

func unitTexts(d readerDoc) (out [][2]string) {
	for _, u := range d.Units {
		out = append(out, [2]string{u.Kind, u.Text})
	}
	return out
}

func TestNestedListsUnits(t *testing.T) {
	f := format(t, `<ul><li>Parent <code>x()</code><ul><li>Child</li></ul></li></ul><p>After <code>y()</code></p>`)
	d, root := decode(t, f)
	must(t, storable(t, f) && slices.Equal(unitTexts(d), [][2]string{{"list-item", "Parent x()"}, {"list-item", "Child"}, {"paragraph", "After y()"}}) && strings.Contains(root, `"tagName":"code"`), "%v", unitTexts(d))
}

// TestCodeUnitsRawText is "code units preserve exact raw text while presentation tokens remain separate": with no Shiki
// there are no presentation tokens for a formatted block (the reader highlights it), and the unit and the code element
// keep the exact raw text.
func TestCodeUnitsRawText(t *testing.T) {
	code := "  const value = 42;\n\n"
	f := format(t, `<pre><code class="language-ts">`+code+`</code></pre>`)
	d, root := decode(t, f)
	var unit string
	for _, u := range d.Units {
		if u.Kind == "code" {
			unit = u.Text
		}
	}
	pre := d.Root.Children[0]
	must(t, unit == code && pre.Tag == "pre" && pre.Children[0].Children[0].Value == code, "%q %+v", unit, pre)
	must(t, d.CodeTokens[fmt.Sprint(pre.Children[0].Properties["id"])] == nil && !match(`reader-token-|reader-copy|reader-code-header`, root), "%v", d.CodeTokens)
}

func TestSourceRevision(t *testing.T) {
	source := &library.ReaderSourceSnapshot{HTML: `<h2>Heading</h2><p>Story</p><img src="https://example.com/photo.jpg" alt="Photo">`, Provenance: "source"}
	with, _ := decode(t, format(t, `<h2>Heading</h2><p>Story</p><img src="`+tinyPNG+`" alt="Photo">`, source))
	without, _ := decode(t, format(t, `<h2>Heading</h2><p>Story</p><img src="https://example.com/photo.jpg" alt="Photo">`, source))
	changed, _ := decode(t, format(t, `<h2>Heading</h2><p>Story changed</p>`, &library.ReaderSourceSnapshot{HTML: `<h2>Heading</h2><p>Story changed</p>`, Provenance: "source"}))
	must(t, with.ContentRevision == without.ContentRevision && with.Provenance == "source", "the revision follows the source")
	must(t, with.UnitsRevision == without.UnitsRevision, "a photo that failed keeps its place, so reading positions stay put")
	must(t, with.ContentRevision != changed.ContentRevision, "a changed story keeps its revision")
}

func TestLegacyMarkupNormalizes(t *testing.T) {
	first := format(t, `<blockquote><p>[!NOTE]<br>Note body</p></blockquote><pre><code class="language-js">const n = 1;`+"\n"+`</code></pre><table><tr><td>Cell</td></tr></table>`)
	upgraded := format(t, first.HTML, &library.ReaderSourceSnapshot{HTML: first.HTML, Provenance: "legacy-cache"})
	a, _ := decode(t, first)
	b, _ := decode(t, upgraded)
	must(t, a.ContentRevision == b.ContentRevision && slices.Equal(unitTexts(a), unitTexts(b)) && storable(t, upgraded), "%v %v", unitTexts(a), unitTexts(b))
}

func TestCodeBeyond128(t *testing.T) {
	var page strings.Builder
	for i := range 129 {
		fmt.Fprintf(&page, `<pre><code>line %d</code></pre>`, i)
	}
	f := format(t, page.String())
	d, _ := decode(t, f)
	code := 0
	for _, u := range d.Units {
		if u.Kind == "code" {
			code++
		}
	}
	must(t, len(f.CodeBlocks) == 128 && code == 129 && d.Units[len(d.Units)-1].Text == "line 128" && storable(t, f), "%d %d", len(f.CodeBlocks), code)
}

func TestLooseTextUnits(t *testing.T) {
	f := format(t, `Loose <em>inline</em><div>Container <strong>text</strong><p>Paragraph</p></div><table><tr><td><h2>Heading in cell</h2>Cell tail</td></tr></table>`)
	d, _ := decode(t, f)
	var texts []string
	for _, u := range d.Units {
		texts = append(texts, u.Text)
	}
	must(t, storable(t, f) && slices.Equal(texts, []string{"Loose inline", "Container text", "Paragraph", "Heading in cellCell tail"}), "%q", texts)
}

func TestSourceImageIdentity(t *testing.T) {
	page := `<p>Same text</p><img src="` + tinyPNG + `" alt="Same alt">`
	first, root := decode(t, format(t, page, &library.ReaderSourceSnapshot{HTML: `<p>Same text</p><img src="https://example.com/one.png" alt="Same alt">`, Provenance: "source"}))
	second, _ := decode(t, format(t, page, &library.ReaderSourceSnapshot{HTML: `<p>Same text</p><img src="https://example.com/two.png" alt="Same alt">`, Provenance: "source"}))
	must(t, first.ContentRevision != second.ContentRevision && !strings.Contains(root, "https://example.com"), "%s", root)
}

func TestInvalidSizeAttributes(t *testing.T) {
	f := format(t, `<p>Keep this text</p><img src="`+tinyPNG+`" width="0"><table><tr><td rowspan="0" colspan="oops">Span text</td></tr></table>`)
	_, root := decode(t, f)
	must(t, storable(t, f) && strings.Contains(root, "Keep this text") && strings.Contains(root, `"rowSpan":0`) && !match(`"width":0|"colSpan":"oops"`, root), "%s", root)
}
