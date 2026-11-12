// Package extract is Trie's reading of a fetched page or feed body into the reader's article, ported from
// apps/desktop/src/main: extract-content.ts (extract.go, with sanitize.go, css.go, feed.go), gallery-page.ts (gallery.go),
// svg-image.ts and image-type.ts (svg.go), whiteboard.ts (whiteboard.go on rough.go), reader-format.ts (format.go) and
// reader-document.ts (document.go). The Readability layer is codeberg.org/readeck/go-readability/v2 standing in for
// @mozilla/readability 0.6.0 (this file, chosen in .context/mygo/extract-spike); the bench in bench_test.go compares the
// whole output with the TS one (.context/mygo/extract-port).
package extract

import (
	"net/url"
	"strings"

	readeck "codeberg.org/readeck/go-readability/v2"
	"golang.org/x/net/html"
)

// Article is what a Readability implementation returns for one page, in the shape the TS bench writes.
type Article struct {
	OK          bool    `json:"ok"`
	Error       string  `json:"error,omitempty"`
	Title       string  `json:"title"`
	Byline      string  `json:"byline"`
	Excerpt     string  `json:"excerpt"`
	SiteName    string  `json:"siteName"`
	Lang        string  `json:"lang"`
	Content     string  `json:"content"`
	TextContent string  `json:"textContent"`
	MS          float64 `json:"ms"`
}

// The options extract-content.ts passes to Readability.
const (
	maxElems      = 20_000
	charThreshold = 120
)

// Readeck runs codeberg.org/readeck/go-readability/v2 on an already decoded page. The page is parsed here rather than by the
// library's own reader, which guesses a charset (chardet) and NFC-normalises: the TS side decodes by Content-Type and does neither.
func Readeck(page string, base *url.URL) Article {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return Article{Error: err.Error()}
	}
	p := readeck.NewParser()
	p.MaxElemsToParse, p.CharThresholds, p.KeepClasses = maxElems, charThreshold, true
	a, err := p.ParseAndMutate(doc, base)
	if err != nil {
		return Article{Error: err.Error()}
	}
	if a.Node == nil {
		return Article{Error: "no article"}
	}
	var content, text strings.Builder
	if err := a.RenderHTML(&content); err != nil {
		return Article{Error: err.Error()}
	}
	_ = a.RenderText(&text)
	return Article{OK: true, Title: a.Title(), Byline: a.Byline(), Excerpt: a.Excerpt(), SiteName: a.SiteName(), Lang: a.Language(),
		Content: content.String(), TextContent: text.String()}
}
