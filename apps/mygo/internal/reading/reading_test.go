package reading

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trie/internal/extract"
	"trie/internal/feed"
	"trie/internal/fetch"
	"trie/internal/library"
)

// Service's cases, then the reading halves of the gallery-page, reader-format, subscription and reader-document
// validation cases. The comment over each test is its name. Extraction is the extract package called in place, not
// the worker.

var (
	long    = strings.Repeat("This is a public paragraph with useful article content and code explanations. ", 8)
	page    = `<html><article><h1>Reader</h1><p>` + long + `</p><pre><code>const answer = 42;</code></pre><a href="https://example.com/reference">Reference</a></article></html>`
	png     = []byte{137, 80, 78, 71, 13, 10, 26, 10, 0}
	jpegish = []byte{255, 216, 255}
	empty   = library.Snapshot{Version: 1, AsOf: "2026-11-01", Sources: []library.SourceSnapshot{}, Articles: []library.ArticleSnapshot{}, ReviewItems: []library.ReviewItem{}}
)

// direct is the cases' extraction: page without trustedArticle, the feed, cached images and SVG; no fragment, whiteboard
// or format.
var direct = Extractors{
	Page: func(html, url string, _ bool) (extract.Extracted, error) {
		return extract.ExtractPage(html, url, false), nil
	},
	Feed: func(xml, url string, aliases []string) (*extract.Extracted, error) {
		if found, ok := extract.ExtractFeed(xml, url, aliases); ok {
			return &found, nil
		}
		return nil, nil
	},
	Images: func(html string, images map[string]string) (string, error) {
		return extract.CacheImages(html, images), nil
	},
	SVG: func(source string) (string, error) { return extract.SanitizeSVG(source), nil },
}

func withFragment(x Extractors) Extractors {
	x.Fragment = func(html, base, kind string) (extract.Extracted, error) {
		return extract.ExtractFeedBody(html, base, kind), nil
	}
	return x
}

// bare is the reader-format cases' extraction: nothing but the formatter, which the test gives.
func bare(format func(string, *library.ReaderSourceSnapshot) (extract.Formatted, error)) Extractors {
	return Extractors{
		Page: func(string, string, bool) (extract.Extracted, error) {
			return extract.Extracted{}, errors.New("No extraction")
		},
		Feed:   func(string, string, []string) (*extract.Extracted, error) { return nil, nil },
		Images: func(html string, _ map[string]string) (string, error) { return html, nil },
		Format: format,
	}
}

type handler func(url string, scope func(string) bool, maxBytes int) (*fetch.Response, error)

func serve(h handler) func() fetch.Fetch {
	return func() fetch.Fetch {
		return func(_ context.Context, url string, scope func(string) bool, maxBytes int) (*fetch.Response, error) {
			return h(url, scope, maxBytes)
		}
	}
}

func offline(message string) func() fetch.Fetch {
	return serve(func(string, func(string) bool, int) (*fetch.Response, error) { return nil, errors.New(message) })
}

func reply(url string, status int, kind string, body []byte) *fetch.Response {
	return &fetch.Response{URL: url, Status: status, Type: kind, Body: body}
}

func htmlPage(body string) handler {
	return func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		return reply(url, 200, "text/html", []byte(body)), nil
	}
}

var reset = &fetch.NetError{Err: errors.New("Client network socket disconnected before secure TLS connection was established"), Transient: true}

func snapshot(t *testing.T) library.Snapshot {
	t.Helper()
	data, err := os.ReadFile("../library/testdata/verified-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	var s library.Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func open(t *testing.T, path string, seed library.Snapshot) *library.Store {
	t.Helper()
	s, err := library.Open(path, seed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func check(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}

func first(t *testing.T, s *library.Store) library.Article {
	t.Helper()
	return must[library.LibraryState](t)(s.Library()).Articles[0]
}

func read(t *testing.T, r *Service, id string, refresh bool) library.ArticleContent {
	t.Helper()
	return must[library.ArticleContent](t)(r.Read(context.Background(), id, refresh, ""))
}

func content(t *testing.T, s *library.Store, id string) *library.ArticleContent {
	t.Helper()
	return must[*library.ArticleContent](t)(s.Content(id))
}

func preview(r *Service, t Target, source string) library.ArticleContent {
	return r.Preview(context.Background(), t, source, PreviewWait)
}

func feedBody(html, kind, base, feedURL string) *library.FeedBody {
	return &library.FeedBody{HTML: html, Kind: kind, BaseURL: &base, FeedURL: feedURL, Version: "v"}
}

func workOf(t *testing.T, c library.ArticleContent) *library.GalleryWork {
	t.Helper()
	if !hasWork(c.Work) {
		return nil
	}
	var w library.GalleryWork
	if err := json.Unmarshal(c.Work, &w); err != nil {
		t.Fatal(err)
	}
	return &w
}

func value(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func count(n *int) int {
	if n == nil {
		return -1
	}
	return *n
}

func dataURL(kind string, body []byte) string {
	return "data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(body)
}

// body cache survives process-style reopen and reseeding without changing favorites
func TestCacheSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.sqlite")
	store := open(t, path, snapshot(t))
	id := first(t, store).ID
	var requests atomic.Int32
	must[library.LibraryState](t)(store.Mutate(library.ToggleFavorite{ID: id}))
	service := New(store, serve(func(url string, s func(string) bool, n int) (*fetch.Response, error) {
		requests.Add(1)
		return htmlPage(page)(url, s, n)
	}), direct)
	read1 := read(t, service, id, false)
	check(t, read1.Status == "ready" && !read1.FromCache && strings.Contains(read1.HTML, "const answer"), "%+v", read1)
	check(t, read(t, service, id, false).FromCache, "the second read was not from the cache")
	check(t, first(t, store).BodyStatus == "ready" && requests.Load() == 1, "%s, %d requests", first(t, store).BodyStatus, requests.Load())
	store.Close()
	store = open(t, path, snapshot(t))
	reader := New(store, offline("offline"), direct)
	cached := read(t, reader, id, false)
	check(t, cached.HTML == read1.HTML && cached.FromCache && first(t, store).Favorite, "after reopening: %+v", cached)
	check(t, slices.Contains(must[[]string](t)(store.ContentLinks()), "https://example.com/reference"), "the body's links were not kept")
	failed := read(t, reader, id, true)
	check(t, failed.HTML == read1.HTML && failed.RefreshError != nil, "failed refresh %+v", failed)
}

// unknown IDs do not cause requests; cold failures are retryable without false content
func TestColdFailures(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	var calls atomic.Int32
	service := New(store, serve(func(string, func(string) bool, int) (*fetch.Response, error) {
		calls.Add(1)
		return nil, errors.New("network")
	}), direct)
	_, err := service.Read(context.Background(), "https://unknown.example/", false, "")
	check(t, err != nil && calls.Load() == 0, "unknown article: %v, %d calls", err, calls.Load())
	id := first(t, store).ID
	check(t, read(t, service, id, false).Status == "unavailable", "a failed read was not unavailable")
	check(t, content(t, store, id) == nil, "a failed read was saved")
	read(t, service, id, false)
	check(t, calls.Load() == 2, "%d calls", calls.Load())
}

// a transient page connection failure retries once without falling back to a stale partial cache
func TestTransientRetriedOnce(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	var calls atomic.Int32
	service := New(store, serve(func(url string, s func(string) bool, n int) (*fetch.Response, error) {
		if calls.Add(1) == 1 {
			return nil, &fetch.NetError{Err: errors.New("reset"), Transient: true}
		}
		return htmlPage(page)(url, s, n)
	}), direct)
	check(t, read(t, service, first(t, store).ID, false).Status == "ready" && calls.Load() == 2, "%d calls", calls.Load())
}

// connections reset twice in a row (a proxy dropping a burst) are tried a third time after a pause
func TestResetTwiceTriedThird(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	var calls atomic.Int32
	service := New(store, serve(func(url string, s func(string) bool, n int) (*fetch.Response, error) {
		if calls.Add(1) <= 2 {
			return nil, reset
		}
		return htmlPage(page)(url, s, n)
	}), direct)
	check(t, read(t, service, first(t, store).ID, false).Status == "ready" && calls.Load() == 3, "%d calls", calls.Load())
}

// explicit restricted page keeps public preview and never tries feed fallback
func TestRestrictedPageNoFeedFallback(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	var calls []string
	var mu sync.Mutex
	html := `<html><head><link rel="alternate" type="application/rss+xml" href="/feed"><script type="application/ld+json">{"@type":"Article","isAccessibleForFree":false}</script></head><article><p>` + long + `</p><div hidden>Secret content</div></article></html>`
	service := New(store, serve(func(url string, s func(string) bool, n int) (*fetch.Response, error) {
		mu.Lock()
		calls = append(calls, url)
		mu.Unlock()
		return htmlPage(html)(url, s, n)
	}), direct)
	result := read(t, service, first(t, store).ID, false)
	check(t, result.Status == "restricted" && !strings.Contains(result.HTML, "Secret") && len(calls) == 1, "%s %v", result.Status, calls)
}

// access denial and payment-required responses never trigger alternate feed requests
func TestDeniedNoFeedFallback(t *testing.T) {
	for _, status := range []int{401, 402, 403} {
		store := open(t, ":memory:", snapshot(t))
		var calls atomic.Int32
		service := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
			calls.Add(1)
			return reply(url, status, "text/html", []byte(page)), nil
		}), direct)
		id := first(t, store).ID
		result := read(t, service, id, false)
		want := "restricted"
		if status == 403 {
			want = "unavailable"
		}
		check(t, calls.Load() == 1 && result.HTML == "" && result.Status == want && content(t, store, id) == nil, "%d: %d calls, %+v", status, calls.Load(), result)
	}
}

// publisher-declared feed fallback matches article and labels descriptions as summaries
func TestPublisherFeedFallback(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	state := must[library.LibraryState](t)(store.Library())
	item := state.Articles[0]
	home := state.Sources[slices.IndexFunc(state.Sources, func(s library.Source) bool { return s.ID == item.SourceID })].HomeURL
	feedURL := strings.TrimSuffix(home, "/") + "/feed"
	service := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		if url == feedURL {
			return reply(url, 200, "application/rss+xml", []byte(`<rss version="2.0"><channel><title>Feed</title><item><title>Article</title><link>`+item.URL+`</link><description><![CDATA[<p>`+long+`</p>]]></description></item></channel></rss>`)), nil
		}
		return reply(url, 200, "text/html", []byte(`<html><head><link rel="alternate" type="application/rss+xml" href="`+feedURL+`"></head><body>No readable content</body></html>`)), nil
	}), direct)
	result := read(t, service, item.ID, false)
	check(t, result.Status == "summary" && result.SourceKind == "feed", "%s %s", result.Status, result.SourceKind)
}

// images are cached as data URLs and unavailable images do not erase readable text
func TestImagesCachedAsData(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	photo := "https://images.example.com/photo.png"
	service := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		if url == photo {
			return reply(url, 200, "image/png", png), nil
		}
		return reply(url, 200, "text/html", []byte(strings.Replace(page, "</article>", `<figure><img src="`+photo+`" alt="Photo"></figure></article>`, 1))), nil
	}), direct)
	result := read(t, service, first(t, store).ID, false)
	check(t, strings.Contains(result.HTML, "data:image/png;base64,") && !strings.Contains(result.HTML, `src="https:`), "%s", result.HTML)
}

// SVG images are sanitized before caching and invalid XML remains a visible missing image
func TestSVGSanitized(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	service := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		switch {
		case strings.HasSuffix(url, "/logo.svg"):
			return reply(url, 200, "image/svg+xml; charset=utf-8", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" onload="alert(1)"><script>alert(1)</script><rect width="20" height="20" fill="#f00"/><use href="https://private.example/track"/></svg>`)), nil
		case strings.HasSuffix(url, "/invalid.svg"):
			return reply(url, 200, "image/svg+xml", []byte(`<html>error</html>`)), nil
		}
		return reply(url, 200, "text/html", []byte(strings.Replace(page, "</article>", `<img src="/logo.svg" alt="Logo"><img src="/invalid.svg" alt="Invalid"></article>`, 1))), nil
	}), direct)
	result := read(t, service, first(t, store).ID, false)
	m := regexp.MustCompile(`src="data:image/svg\+xml;base64,([^"]+)"`).FindStringSubmatch(result.HTML)
	check(t, m != nil, "no cached SVG: %s", result.HTML)
	svg, _ := base64.StdEncoding.DecodeString(m[1])
	check(t, strings.Contains(string(svg), "<rect") && !regexp.MustCompile(`script|onload|private\.example|alert`).Match(svg), "%s", svg)
	check(t, count(result.MissingImageCount) == 1 && count(content(t, store, result.ArticleID).MissingImageCount) == 1, "missing %d", count(result.MissingImageCount))
}

// photo-heavy posts cache images beyond the old count and size limits with bounded concurrency
func TestPhotoHeavyPosts(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	article := first(t, store)
	var urls []string
	var tagsHTML strings.Builder
	for i := range 17 {
		urls = append(urls, fmt.Sprintf("https://images.example.com/%d.jpg", i))
		fmt.Fprintf(&tagsHTML, `<img src="%s" alt="Photo %d">`, urls[i], i)
	}
	html := strings.Replace(page, "</article>", tagsHTML.String()+"</article>", 1)
	var active, peak, calls, arrivals atomic.Int32
	// The first six pictures wait for each other, so the six readers are seen at once; the code keeps it to six.
	six := make(chan struct{})
	images := func(url string, _ func(string) bool, maxBytes int) (*fetch.Response, error) {
		calls.Add(1)
		if !slices.Contains(urls, url) {
			return reply(url, 200, "text/html", []byte(html)), nil
		}
		if maxBytes != 8*1024*1024 {
			t.Errorf("an image read with a %d byte limit", maxBytes)
		}
		n := active.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		if arrivals.Add(1) == 6 {
			close(six)
		}
		select {
		case <-six:
		case <-time.After(2 * time.Second):
		}
		active.Add(-1)
		size := 600_000
		if url == urls[0] {
			size = 3 * 1024 * 1024
		}
		body := make([]byte, size)
		copy(body, jpegish)
		return reply(url, 200, "image/jpeg", body), nil
	}
	must[library.LibraryState](t)(store.Mutate(library.ToggleFavorite{ID: article.ID}))
	save(t, store, library.ArticleContent{ArticleID: article.ID, Status: "ready", HTML: "<p>14657.JPG</p>", TextLength: 10, Links: []string{},
		SourceURL: article.URL, SourceKind: "page", Message: str("部分图片暂未保存，可前往原文查看")})
	reader := New(store, serve(images), direct)
	result := read(t, reader, article.ID, false)
	check(t, strings.Count(result.HTML, "data:image/jpeg") == 17 && count(result.MissingImageCount) == 0 && peak.Load() == 6, "%d pictures, %d missing, %d at once",
		strings.Count(result.HTML, "data:image/jpeg"), count(result.MissingImageCount), peak.Load())
	check(t, len(result.HTML) < 24*1024*1024 && first(t, store).Favorite, "too large, or the favourite lost")
	check(t, read(t, reader, article.ID, false).FromCache && calls.Load() == 18, "%d calls", calls.Load())
	failing := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		if url == urls[0] {
			return nil, errors.New("offline")
		}
		return images(url, anywhere, 8*1024*1024)
	}), direct)
	refreshed := read(t, failing, article.ID, true)
	check(t, refreshed.HTML == result.HTML, "Failed image refresh must preserve the previous complete body")
	check(t, refreshed.RefreshError != nil && strings.Contains(*refreshed.RefreshError, "图片") && content(t, store, article.ID).HTML == result.HTML, "%s", value(refreshed.RefreshError))
}

// image failures report a count, retry transient errors once and keep a meaningful placeholder
func TestImageFailures(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	var mu sync.Mutex
	attempts := map[string]int{}
	service := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		mu.Lock()
		attempts[url]++
		attempt := attempts[url]
		mu.Unlock()
		switch {
		case strings.HasSuffix(url, "/retry.jpg") && attempt == 1:
			return nil, &fetch.NetError{Err: errors.New("reset"), Transient: true}
		case strings.HasSuffix(url, "/missing.jpg"):
			return reply(url, 404, "text/html", []byte("not found")), nil
		case strings.HasSuffix(url, "/retry.jpg"):
			return reply(url, 200, "image/jpeg", jpegish), nil
		}
		return reply(url, 200, "text/html", []byte(strings.Replace(page, "</article>", `<img src="/retry.jpg"><img src="/missing.jpg" alt="14657.JPG"></article>`, 1))), nil
	}), direct)
	result := read(t, service, first(t, store).ID, false)
	check(t, result.Status == "ready" && count(result.MissingImageCount) == 1, "%s, %d missing", result.Status, count(result.MissingImageCount))
	check(t, result.Message == nil || !strings.Contains(*result.Message, "图片"), "a failed image is shown where it was, not reported in a banner")
	check(t, strings.Contains(result.HTML, `<img alt="14657.JPG" />`), "%s", result.HTML)
	for url, n := range attempts {
		if strings.HasSuffix(url, "/retry.jpg") && n != 2 || strings.HasSuffix(url, "/missing.jpg") && n != 1 {
			t.Fatalf("%s read %d times", url, n)
		}
	}
}

// image caching remains bounded by per-image, total-byte and count limits
func TestImageLimits(t *testing.T) {
	const mb = 1024 * 1024
	for _, sizes := range [][]int{{9 * mb, 6 * mb, 6 * mb, 6 * mb}, slices.Repeat([]int{10}, 70)} {
		store := open(t, ":memory:", snapshot(t))
		var requested atomic.Int32
		number := regexp.MustCompile(`/(\d+)\.jpg$`)
		service := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
			m := number.FindStringSubmatch(url)
			if m == nil {
				var tags strings.Builder
				for i := range sizes {
					fmt.Fprintf(&tags, `<img src="https://images.example.com/%d.jpg">`, i)
				}
				return reply(url, 200, "text/html", []byte(strings.Replace(page, "</article>", tags.String()+"</article>", 1))), nil
			}
			requested.Add(1)
			var i int
			fmt.Sscan(m[1], &i)
			body := make([]byte, sizes[i])
			copy(body, jpegish)
			return reply(url, 200, "image/jpeg", body), nil
		}), direct)
		result := read(t, service, first(t, store).ID, false)
		want := 6
		if len(sizes) == 4 {
			want = 2
		}
		check(t, len(result.HTML) < 24*mb && count(result.MissingImageCount) == want && requested.Load() <= 64, "%d sizes: %d missing, %d requested", len(sizes), count(result.MissingImageCount), requested.Load())
	}
}

// a feed description holding the whole article with pictures the page extraction loses is read as the article
func TestFeedDescriptionHoldsArticle(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	words := `<p>The exhibition is a meditation on contemporary sculpture as a complex and powerful practice that plays a special role in the current context.</p>`
	photo := "https://images.example.com/use.png"
	service := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		if url == photo {
			return reply(url, 200, "image/png", png), nil
		}
		// The page keeps its pictures in a gallery outside the article the extractor reads.
		return reply(url, 200, "text/html", []byte(`<html><body><div class="gallery"><img src="`+photo+`"></div><article><h1>Use</h1>`+strings.Repeat(words, 3)+`</article></body></html>`)), nil
	}), withFragment(direct))
	result := preview(service, Target{ID: "explore:https://uses.example/use", URL: "https://uses.example/use", Aliases: []string{}, HomeURL: "https://uses.example/", TrustedArticle: true,
		FeedBody: feedBody(`Contributed by someone<br><img src="`+photo+`"><br>`+strings.Repeat(words, 3), "summary", "https://uses.example/use", "https://uses.example/rss")}, "")
	check(t, result.Status == "ready" && result.SourceKind == "feed" && strings.Contains(result.HTML, "data:image/png;base64,"), "the picture from the description is kept: %+v", result)
}

// a short feed body is checked against the page, and whichever holds more of the article is read
func TestShortFeedBodyAgainstPage(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	article := strings.Repeat(`<p>Every agent tool stores memory in its own shape, and every week someone builds another one that none of the others can read.</p>`, 20)
	// A recommender's entry: a picture and a one-paragraph summary in the feed's content field (Hacker News digests).
	digest := `<img src="https://digest.example/cover.png"><br>The author proposes a shared format for agent memory. <a href="https://digest.example/#post">[summary]</a>`
	try := func(feedHTML, pageHTML string) (library.ArticleContent, int) {
		var pages atomic.Int32
		service := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
			if url == "https://blog.example/post" {
				pages.Add(1)
			}
			if strings.HasSuffix(url, ".png") {
				return reply(url, 404, "text/plain", nil), nil
			}
			return reply(url, 200, "text/html", []byte(pageHTML)), nil
		}), withFragment(direct))
		result := preview(service, Target{ID: "explore:https://blog.example/post", URL: "https://blog.example/post", Aliases: []string{}, HomeURL: "https://blog.example/", TrustedArticle: true,
			FeedBody: feedBody(feedHTML, "body", "https://blog.example/post", "https://digest.example/feed.xml")}, "")
		return result, int(pages.Load())
	}
	full, _ := try(digest, `<html><body><nav>Docs Sign in</nav><article><h1>Agent memory</h1>`+article+`</article></body></html>`)
	check(t, full.SourceKind == "page" && strings.Contains(full.HTML, "none of the others can read"), "the page holds the article, not the digest: %s", full.SourceKind)
	note, pages := try(`<p>A short note that is the whole post, kept in full by the feed, with a thought or two worth reading again.</p>`, `<html><body><nav>Home Archive About</nav><main><p>Subscribe</p></main></body></html>`)
	check(t, note.SourceKind == "feed" && pages == 1, "a page with less of the article does not replace the feed: %s, %d pages", note.SourceKind, pages)
	whole, pages := try(article, `<html><body><article><p>Page</p></article></body></html>`)
	check(t, whole.SourceKind == "feed" && pages == 0, "a long feed body is read without fetching the page: %s, %d pages", whole.SourceKind, pages)
}

// a source chosen in the reading options is read as chosen
func TestChosenSource(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	pageHTML := `<html><body><article><h1>Post</h1>` + strings.Repeat(`<p>The page holds the whole article with all of its paragraphs and explanations.</p>`, 20) + `</article></body></html>`
	target := Target{ID: "explore:https://blog.example/chosen", URL: "https://blog.example/chosen", Aliases: []string{}, HomeURL: "https://blog.example/", TrustedArticle: true,
		FeedBody: feedBody(`<p>The version the feed carries, which the reader would set aside for the fuller page.</p>`, "body", "https://blog.example/chosen", "https://blog.example/feed")}
	reader := New(store, serve(htmlPage(pageHTML)), withFragment(direct))
	check(t, preview(reader, target, "").SourceKind == "page", "left to itself the reader takes the fuller page")
	fromFeed := preview(reader, target, "feed")
	check(t, fromFeed.SourceKind == "feed" && strings.Contains(fromFeed.HTML, "The version the feed carries"), "%+v", fromFeed)
	whole := target
	whole.FeedBody = feedBody(`<p>`+strings.Repeat("A whole article in the feed. ", 80)+`</p>`, "body", "https://blog.example/chosen", "https://blog.example/feed")
	check(t, preview(reader, whole, "page").SourceKind == "page", "the page is read even when the feed body is whole")
}

// a gallery entry reads as its work page even when the feed teaser has a picture and the same words
func TestGalleryEntryReadsAsWork(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	words := `<p>Oluwatobi Salawu is a full-stack engineer who built a portfolio to look and feel like his ten year old self when he first started coding.</p>`
	shot := "https://gallery.example/shot.png"
	work := &extract.Work{Name: "Oluwatobi Salawu", Site: str("https://tobi.example/"), Host: str("tobi.example"), Facts: []extract.Fact{}, Sections: []extract.Shot{}, Video: str("https://gallery.example/rec.mp4")}
	x := withFragment(direct)
	x.Page = func(string, string, bool) (extract.Extracted, error) {
		return extract.Extracted{Status: "ready", HTML: words, TextLength: 140, Links: []string{}, Images: []string{}, FeedURLs: []string{}, Work: work}, nil
	}
	reader := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		switch {
		case strings.HasSuffix(url, ".png"):
			return reply(url, 200, "image/png", png), nil
		case strings.HasSuffix(url, ".mp4"):
			return reply(url, 404, "text/html", []byte("<html></html>")), nil
		}
		return reply(url, 200, "text/html", []byte("<html></html>")), nil
	}), x)
	result := preview(reader, Target{ID: "explore:https://gallery.example/tobi", URL: "https://gallery.example/tobi", Aliases: []string{}, HomeURL: "https://gallery.example/", TrustedArticle: true,
		FeedBody: feedBody(`<p><img src="`+shot+`"></p>`+words+`<p><a href="https://gallery.example/tobi">Full Review</a></p>`, "body", "https://gallery.example/tobi", "https://gallery.example/feed")}, "")
	check(t, result.SourceKind == "page" && workOf(t, result) != nil, "the work page, with its recording, not the feed teaser: %+v", result)
}

// a feed body that is only a picture (a gallery shot) stays as it is, without reading the gallery page as an article
func TestPictureFeedBodyStays(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	shot := "https://minimal.example/shot.png"
	// A gallery page general extraction would read: navigation, labels, related sites and an advertisement.
	galleryHTML := `<html><body><main><a href="/">Back</a><h2>Type</h2><ul><li>Agency</li></ul><h2>Similar Websites</h2>` + strings.Repeat(`<p><a href="/x">Another site</a> 9 years ago, featured in the gallery with a screenshot.</p>`, 6) + `<p>Sponsored. Where designers get their inspiration.</p></main></body></html>`
	var pages atomic.Int32
	fetcher := serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		if url == shot {
			return reply(url, 200, "image/png", png), nil
		}
		pages.Add(1)
		return reply(url, 200, "text/html", []byte(galleryHTML)), nil
	})
	target := Target{ID: "explore:https://minimal.example/onlines", URL: "https://minimal.example/onlines", Aliases: []string{}, HomeURL: "https://minimal.example/", TrustedArticle: true,
		FeedBody: feedBody(`<p><img src="`+shot+`"></p>`, "body", "https://minimal.example/onlines", "https://minimal.example/feed")}
	result := preview(New(store, fetcher, withFragment(direct)), target, "")
	check(t, result.SourceKind == "feed" && !regexp.MustCompile(`Similar Websites|Sponsored`).MatchString(result.HTML) && pages.Load() == 1, "the gallery page is not read as an article: %s, %d pages", result.HTML, pages.Load())
	// One section of a bigger page (a portfolio's single logo) is complete as the feed gives it too.
	section := target
	section.ID, section.URL = "explore:https://minimal.example/design/", "https://minimal.example/design/"
	// Addresses drop the fragment; the feed parser keeps the link to the section as the body's base (subscription-feed sectionLink).
	section.FeedBody = feedBody(`<p><img src="`+shot+`"><br><a href="https://squawk.example/">Squawkbox</a> is a boiler fleet monitor.</p>`, "body", "https://minimal.example/design/#logo", "https://minimal.example/feed")
	result = preview(New(store, fetcher, withFragment(direct)), section, "")
	check(t, result.SourceKind == "feed" && pages.Load() == 1, "a section is not looked up on its page: %s, %d pages", result.SourceKind, pages.Load())
}

// a feed body that is only a lead picture gives way to the article on the page (a magazine)
func TestLeadPictureGivesWay(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	lead := "https://mag.example/lead.png"
	article := strings.Repeat(`<p>As a longtime fragrance lover, the designer built a lamp that keeps the history of a room in the scent it leaves behind.</p>`, 8)
	reader := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		if strings.HasSuffix(url, ".png") {
			return reply(url, 200, "image/png", png), nil
		}
		return reply(url, 200, "text/html", []byte(`<html><body><article><h1>Lamp</h1><p><img src="`+lead+`"></p>`+article+`</article></body></html>`)), nil
	}), withFragment(direct))
	result := preview(reader, Target{ID: "explore:https://mag.example/lamp", URL: "https://mag.example/lamp", Aliases: []string{}, HomeURL: "https://mag.example/", TrustedArticle: true,
		FeedBody: feedBody(`<p><img src="`+lead+`" alt="Lamp"></p>`, "body", "https://mag.example/lamp", "https://mag.example/feed")}, "")
	check(t, result.SourceKind == "page" && strings.Contains(result.HTML, "keeps the history of a room"), "%s", result.SourceKind)
}

// a page judged members-only that shows less than the feed leaves the feed version
func TestMembersOnlyLeavesFeed(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	words := strings.Repeat(`<p>A studio site with a quiet grid, slow transitions and type set large enough to read across the room.</p>`, 3)
	x := withFragment(direct)
	x.Page = func(string, string, bool) (extract.Extracted, error) {
		return extract.Extracted{Status: "restricted", Links: []string{}, Images: []string{}, FeedURLs: []string{}, Message: str("原文需要登录或订阅；仅保存公开可见的内容")}, nil
	}
	reader := New(store, serve(htmlPage("<html></html>")), x)
	result := preview(reader, Target{ID: "explore:https://mind.example/site", URL: "https://mind.example/site", Aliases: []string{}, HomeURL: "https://mind.example/", TrustedArticle: true,
		FeedBody: feedBody(words, "summary", "https://mind.example/site", "https://mind.example/feed")}, "")
	check(t, result.SourceKind == "feed" && strings.Contains(result.HTML, "quiet grid"), "%+v", result)
}

// a gallery item whose page gives no work page reads as a work built from its feed: the shot, the name and the site
func TestGalleryCardWork(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	shot := "https://gallery.example/atlas.webp"
	var pageReads atomic.Int32
	reader := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		if url == shot {
			return reply(url, 200, "image/png", png), nil
		}
		pageReads.Add(1)
		return reply(url, 200, "text/html", []byte(`<html><body><nav>Gallery Archive Tags</nav><div id="app"></div></body></html>`)), nil
	}), withFragment(direct))
	target := func(html string) Target {
		return Target{ID: "explore:https://gallery.example/atlas", URL: "https://gallery.example/atlas", Aliases: []string{}, HomeURL: "https://gallery.example/", TrustedArticle: true,
			FeedBody: feedBody(html, "summary", "https://gallery.example/atlas", "https://gallery.example/feed"),
			Work:     &Card{Name: "Tokyo Atlas", Site: str("https://www.atlas.example/"), Picture: str(shot)}}
	}
	// The feed gives only the shot: a work with no words at all.
	alone := preview(reader, target(`<figure><a href="https://www.atlas.example/"><img src="`+shot+`"></a></figure>`), "")
	w := workOf(t, alone)
	check(t, alone.Status == "ready" && w != nil, "a work page, not \"nothing to read\": %+v", alone)
	check(t, w.Name == "Tokyo Atlas" && value(w.Host) == "atlas.example" && strings.HasPrefix(value(w.Poster), "data:image/png;base64,"), "%+v", w)
	check(t, !strings.Contains(alone.HTML, "<img"), "the shot is the poster, not repeated in the body")
	// With a line about the site, the line is the body.
	told := preview(reader, target(`<p><img src="`+shot+`"></p><p>An art fair site set in one typeface.</p>`), "")
	check(t, workOf(t, told) != nil && strings.Contains(told.HTML, "one typeface") && pageReads.Load() == 2, "%s, %d pages", told.HTML, pageReads.Load())
}

// a post the feed links on an old domain is read where it moved on its publisher's own site
func TestMovedPost(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	// The page answers from where it moved; the reader's fetch refuses addresses outside the scope it is given.
	reader := New(store, serve(func(_ string, scope func(string) bool, _ int) (*fetch.Response, error) {
		if !scope("https://writer.example/blog/post") {
			return nil, errors.New("页面跳转到了作者网站之外")
		}
		if scope("https://elsewhere.example/blog/post") {
			t.Error("the scope allows another site")
		}
		return reply("https://writer.example/blog/post", 200, "text/html", []byte(page)), nil
	}), withFragment(direct))
	result := preview(reader, Target{ID: "explore:https://old.example/blog/post", URL: "https://old.example/blog/post", Aliases: []string{}, HomeURL: "https://writer.example", TrustedArticle: true,
		FeedBody: feedBody(`<p>The opening of the post, as the feed gives it.</p>`, "summary", "https://old.example/blog/post", "https://writer.example/feed.xml")}, "")
	check(t, result.SourceKind == "page" && result.SourceURL == "https://writer.example/blog/post", "%s %s", result.SourceKind, result.SourceURL)
}

// --- The reading halves of the gallery-page, reader-format, subscription and reader-document validation cases.

// The gallery-page cases' empty snapshot and extract (with the feed's body).
var gallerySnapshot = library.Snapshot{Version: 1, AsOf: "2026-11-05", Sources: []library.SourceSnapshot{}, Articles: []library.ArticleSnapshot{}, ReviewItems: []library.ReviewItem{}}

var (
	mp4  = append(append([]byte{0, 0, 0, 24}, "ftypisom"...), make([]byte, 16)...)
	jpeg = []byte{255, 216, 255, 224, 0, 16}
)

func importXML(t *testing.T, s *library.Store, source, xml, feedURL string) string {
	t.Helper()
	parsed := must[library.ParsedFeed](t)(feed.Parse(xml, feedURL))
	return must[library.SubscriptionResult](t)(s.ImportFeed(source, parsed, true, nil, "")).Library.Articles[0].ID
}

// A screenshot the read did not save in time is fetched on its own later and kept
func TestScreenshotFetchedLater(t *testing.T) {
	store := open(t, ":memory:", gallerySnapshot)
	id := importXML(t, store, "g", `<rss><channel><title>G</title><link>https://g.example/</link><item><title>W</title><link>https://g.example/w</link></item></channel></rss>`, "https://g.example/feed")
	work := `{"name":"W","designer":null,"site":null,"host":null,"builtWith":null,"facts":[],"poster":null,"video":null,"sections":[{"name":"Hero","image":null,"source":"https://cdn.g.example/hero.jpg"},{"name":"Gone","image":null,"source":null}]}`
	save(t, store, library.ArticleContent{ArticleID: id, Status: "ready", HTML: "<p>W</p>", TextLength: 1, Links: []string{}, SourceURL: "https://g.example/w", SourceKind: "page", Work: json.RawMessage(work)})
	var fetched atomic.Int32
	service := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		fetched.Add(1)
		return reply(url, 200, "image/jpeg", jpeg), nil
	}), direct)
	picture := dataURL("image/jpeg", jpeg)
	check(t, must[string](t)(service.WorkMedia(context.Background(), id, 0)) == picture, "the screenshot was not fetched")
	shot := workOf(t, *content(t, store, id)).Sections[0]
	check(t, shot.Name == "Hero" && value(shot.Image) == picture && shot.Source.Set && shot.Source.Value == nil, "kept with the article, so it is not fetched again: %+v", shot)
	check(t, must[string](t)(service.WorkMedia(context.Background(), id, 0)) == picture && fetched.Load() == 1, "%d fetches", fetched.Load())
	check(t, must[string](t)(service.WorkMedia(context.Background(), id, 1)) == "", "a shot with no source stays not loaded")
}

// A cached feed lead-in is read again from the page behind it
func TestCachedLeadInReadAgain(t *testing.T) {
	store := open(t, ":memory:", gallerySnapshot)
	teaser := `<p>A brand strategist's site.</p><p><a href="https://onepagelove.example/pete-sena">Full Review</a></p>`
	id := importXML(t, store, "g", `<rss xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>G</title><link>https://onepagelove.example/</link><item><title>Pete Sena</title><link>https://onepagelove.example/pete-sena</link><content:encoded><![CDATA[`+teaser+`]]></content:encoded></item></channel></rss>`, "https://onepagelove.example/feed")
	// What an older build saved: the lead-in, stored as a finished feed body.
	version := must[*library.FeedBody](t)(store.FeedContent(id)).Version
	save(t, store, library.ArticleContent{ArticleID: id, Status: "ready", HTML: teaser, TextLength: 40, Links: []string{}, SourceURL: "https://onepagelove.example/feed", SourceKind: "feed", FeedVersion: &version})
	var pages atomic.Int32
	jpg := regexp.MustCompile(`\.jpe?g$|format=jpg`)
	service := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		if strings.HasSuffix(url, "pete-sena") {
			pages.Add(1)
		}
		body := []byte(galleryEntry)
		if strings.HasSuffix(url, ".mp4") {
			body = mp4
		} else if jpg.MatchString(url) {
			body = jpeg
		}
		return reply(url, 200, "text/html", body), nil
	}), withFragment(direct))
	result := read(t, service, id, false)
	w := workOf(t, result)
	check(t, pages.Load() == 1 && result.SourceKind == "page" && w != nil && w.Name == "Pete Sena", "%d pages, %s, %+v", pages.Load(), result.SourceKind, w)
	check(t, value(w.Poster) == dataURL("image/jpeg", jpeg) && value(w.Video) == dataURL("video/mp4", mp4), "poster %.40s, video %.40s", value(w.Poster), value(w.Video))
	check(t, value(workOf(t, *content(t, store, id)).Video) == value(w.Video), "the work survives the cache")
}

// An Awwwards feed item, one sentence under a picture, is read from its entry page
func TestAwwwardsItemReadFromEntry(t *testing.T) {
	store := open(t, ":memory:", gallerySnapshot)
	id := importXML(t, store, "aw", `<rss><channel><title>Awwwards - Sites of the day</title><link>https://www.awwwards.com</link><item><title>Agrumea Farm</title><link>`+awardURL+`</link>
    <description><![CDATA[<div><img src='https://www.awwwards.com/awards/submissions/2026/09/agrumea.jpg' alt='Agrumea Farm'/></div>An immersive web journey through Agrumea Farm.]]></description></item></channel></rss>`, "https://feeds.feedburner.com/awwwards-sites-of-the-day")
	var pages atomic.Int32
	service := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		body := jpeg
		if url == awardURL {
			pages.Add(1)
			body = []byte(awardPage)
		}
		return reply(url, 200, "text/html", body), nil
	}), withFragment(direct))
	result := read(t, service, id, false)
	w := workOf(t, result)
	check(t, pages.Load() == 1 && result.SourceKind == "page" && w != nil && w.Facts[0].Values[0] == "Site of the Day · 2026-11-05", "%d pages, %s, %+v", pages.Load(), result.SourceKind, w)
	check(t, value(w.Poster) == dataURL("image/jpeg", jpeg) && w.Video == nil, "poster %.40s, video %s", value(w.Poster), value(w.Video))
}

// The reader-format cases' library: one article on one source.
var formatSnapshot = library.Snapshot{Version: 1, AsOf: "2026-11-02",
	Sources: []library.SourceSnapshot{{ID: "writer", Title: "Writer", HomeURL: "https://writer.example/", Host: "writer.example", SampledCount: 1, CatalogCandidateCount: 1, ScopeLabel: "test"}},
	Articles: []library.ArticleSnapshot{{ID: "article", SourceID: "writer", Title: "Article", URL: "https://writer.example/article", Authors: []string{"Writer"}, Aliases: []string{},
		BodyStatus: "not_extracted", VerificationLabel: "test"}},
	ReviewItems: []library.ReviewItem{}, Summary: library.Summary{SampledCount: 1, ArticleCount: 1}}

const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j5T8AAAAASUVORK5CYII="

func cached(html string) library.ArticleContent {
	version, missing := 2, 0
	return library.ArticleContent{ArticleID: "article", Status: "ready", HTML: html, TextLength: 123, Links: []string{"https://writer.example/reference"},
		FetchedAt: "2026-11-01T00:00:00Z", SourceURL: "https://writer.example/article", SourceKind: "page", ImageCacheVersion: &version, MissingImageCount: &missing}
}

func save(t *testing.T, s *library.Store, c library.ArticleContent) {
	t.Helper()
	noErr(t, s.SaveContent(c))
}

// gate is a formatter held until it is opened; entered says it was reached.
func gate() (format func(string, *library.ReaderSourceSnapshot) (extract.Formatted, error), entered chan struct{}, release func()) {
	opened := make(chan struct{})
	entered = make(chan struct{}, 4)
	return func(html string, snapshot *library.ReaderSourceSnapshot) (extract.Formatted, error) {
		entered <- struct{}{}
		<-opened
		return extract.FormatArticleHTML(html, snapshot)
	}, entered, sync.OnceFunc(func() { close(opened) })
}

func later(r *Service, id string) chan library.ArticleContent {
	done := make(chan library.ArticleContent, 1)
	go func() {
		c, _ := r.Read(context.Background(), id, false, "")
		done <- c
	}()
	return done
}

func libraryJSON(t *testing.T, s *library.Store) string {
	t.Helper()
	b, _ := json.Marshal(must[library.LibraryState](t)(s.Library()))
	return string(b)
}

// Offline cache format upgrade persists metadata and images without changing user state
func TestOfflineFormatUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.sqlite")
	store := open(t, path, formatSnapshot)
	must[library.LibraryState](t)(store.Mutate(library.ToggleFavorite{ID: "article"}))
	save(t, store, cached(`<p>[[toc]]</p><h2>Offline heading</h2><blockquote><p>[!NOTE]<br>Cached note</p></blockquote><img src="`+tinyPNG+`">`))
	before := libraryJSON(t, store)
	var formats atomic.Int32
	reader := New(store, offline("Network must not run"), bare(func(html string, _ *library.ReaderSourceSnapshot) (extract.Formatted, error) {
		formats.Add(1)
		return extract.FormatArticleHTML(html, nil)
	}))
	result := read(t, reader, "article", false)
	check(t, result.FromCache && result.FormatVersion == extract.FormatVersion && len(result.Headings) == 1, "%+v", result)
	check(t, strings.Contains(result.HTML, "reader-callout reader-note") && strings.Contains(result.HTML, tinyPNG), "%s", result.HTML)
	check(t, libraryJSON(t, store) == before, "the library changed")
	check(t, read(t, reader, "article", false).FormatVersion == extract.FormatVersion && formats.Load() == 1, "%d formats", formats.Load())
	store.Close()
	store = open(t, path, formatSnapshot)
	saved := content(t, store, "article")
	check(t, slices.Equal(saved.Headings, result.Headings) && saved.HTML == result.HTML && first(t, store).Favorite, "after reopening %+v", saved)
}

// Failed local reformat retains the cached readable article
func TestFailedReformatKeepsCache(t *testing.T) {
	store := open(t, ":memory:", formatSnapshot)
	save(t, store, cached("<p>Existing cached body.</p>"))
	reader := New(store, offline("No network"), bare(func(string, *library.ReaderSourceSnapshot) (extract.Formatted, error) {
		return extract.Formatted{}, errors.New("Parser failure")
	}))
	result := read(t, reader, "article", false)
	check(t, result.HTML == "<p>Existing cached body.</p>" && result.RefreshError != nil && content(t, store, "article").HTML == result.HTML, "%+v", result)
}

// Offline refresh fallback upgrades old layout even when a newer feed body is pending
func TestOfflineFallbackUpgrades(t *testing.T) {
	seed := formatSnapshot
	article := seed.Articles[0]
	article.ID = article.URL
	seed.Articles = []library.ArticleSnapshot{article}
	store := open(t, ":memory:", seed)
	must[library.SubscriptionResult](t)(store.ImportFeed("writer", library.ParsedFeed{FeedURL: "https://writer.example/feed", HomeURL: "https://writer.example/", Title: "Writer", Warnings: []string{},
		Articles: []library.FeedEntry{{ID: article.ID, URL: article.URL, Title: article.Title, Authors: article.Authors, Aliases: []string{}, BodyStatus: article.BodyStatus, VerificationLabel: article.VerificationLabel,
			FeedBody: &library.FeedBody{HTML: "<p>Newer feed body</p>", Kind: "body", Version: "newer", FeedURL: "https://writer.example/feed"}}}}, true, nil, ""))
	c := cached("<p>[[toc]]</p><h2>Cached heading</h2>")
	c.ArticleID = article.ID
	save(t, store, c)
	reader := New(store, offline("Offline"), bare(extract.FormatArticleHTML))
	result := read(t, reader, article.ID, false)
	check(t, result.FormatVersion == extract.FormatVersion && len(result.Headings) > 0 && result.Headings[0].Text == "Cached heading" && result.RefreshError != nil, "%+v", result)
	saved := content(t, store, article.ID)
	check(t, saved.FormatVersion == extract.FormatVersion && saved.FeedVersion == nil && must[*library.FeedBody](t)(store.FeedContent(article.ID)).Version == "newer", "%+v", saved)
}

// Late reformat cannot overwrite a body refreshed while its worker was running
func TestLateReformatKeepsNewBody(t *testing.T) {
	store := open(t, ":memory:", formatSnapshot)
	save(t, store, cached("<h2>Old body</h2>"))
	format, entered, release := gate()
	defer release()
	pending := later(New(store, offline("No network"), bare(format)), "article")
	<-entered
	newer := cached("<h2>New body</h2>")
	newer.FetchedAt = "2026-11-02T00:00:00Z"
	save(t, store, newer)
	release()
	<-pending
	check(t, content(t, store, "article").HTML == "<h2>New body</h2>", "%s", content(t, store, "article").HTML)
}

// Late reformat preserves newer fetch metadata when refreshed HTML is identical
func TestLateReformatKeepsMetadata(t *testing.T) {
	store := open(t, ":memory:", formatSnapshot)
	original := cached("<h2>Same body</h2>")
	save(t, store, original)
	format, entered, release := gate()
	defer release()
	pending := later(New(store, offline("No network"), bare(format)), "article")
	<-entered
	newer, missing := original, 1
	newer.FetchedAt, newer.SourceKind, newer.SourceURL, newer.FeedVersion, newer.MissingImageCount = "2026-11-02T00:00:00Z", "feed", "https://writer.example/feed", str("newer"), &missing
	save(t, store, newer)
	release()
	result := <-pending
	check(t, result.FormatVersion == extract.FormatVersion && result.FetchedAt == "2026-11-02T00:00:00Z" && result.SourceKind == "feed" && value(result.FeedVersion) == "newer", "%+v", result)
	saved := content(t, store, "article")
	check(t, saved.SourceURL == "https://writer.example/feed" && count(saved.MissingImageCount) == 1, "%+v", saved)
}

// The subscription cases' feed of one writer.
func writerFeed(body string, slugs ...string) string {
	var items strings.Builder
	for _, s := range slugs {
		items.WriteString(`<item><title>` + s + `</title><link>https://writer.example/` + s + `/</link><content:encoded><![CDATA[<p>` + body + `</p>]]></content:encoded></item>`)
	}
	return `<rss xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Real Writer</title><link>https://writer.example/</link>` + items.String() + `</channel></rss>`
}

// Empty library -> typed feed URL -> preview -> persisted articles -> real source body, no
// sample whitelist (its reading half: the subscription half is in internal/subscription)
func TestFeedBodyRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	store := open(t, path, empty)
	id := importXML(t, store, "writer", writerFeed("Short real body", "one", "two"), "https://writer.example/feed")
	reader := New(store, offline("must use source-provided body"), withFragment(direct))
	result := read(t, reader, id, false)
	check(t, result.Status == "ready" && result.SourceKind == "feed" && strings.Contains(result.HTML, "Short real body"), "%+v", result)
	store.Close()
	check(t, content(t, open(t, path, empty), id).Status == "ready", "the body was not kept")
}

// Repairing a partial page cache keeps using the page instead of a feed body with fewer images
func TestRepairPartialPageCache(t *testing.T) {
	store := open(t, ":memory:", empty)
	id := importXML(t, store, "writer", writerFeed("Short real body", "one"), "https://writer.example/feed")
	version := must[*library.FeedBody](t)(store.FeedContent(id)).Version
	save(t, store, library.ArticleContent{ArticleID: id, Status: "ready", HTML: "<p>Old page</p>", TextLength: 8, Links: []string{}, SourceURL: first(t, store).URL, SourceKind: "page",
		Message: str("部分图片暂未保存，可前往原文查看"), FeedVersion: &version})
	reader := New(store, serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		if strings.HasSuffix(url, "/photo.png") {
			return reply(url, 200, "image/png", png[:8]), nil
		}
		return reply(url, 200, "text/html", []byte(`<article><p>`+strings.Repeat("Original page content. ", 20)+`</p><img src="/photo.png"></article>`)), nil
	}), withFragment(direct))
	result := read(t, reader, id, false)
	check(t, result.SourceKind == "page" && count(result.MissingImageCount) == 0 && strings.Contains(result.HTML, "Original page content") && strings.Contains(result.HTML, "data:image/png"), "%+v", result)
}

// Late body extraction cannot overwrite a newer feed version in the cache
func TestLateExtractionKeepsNewerFeed(t *testing.T) {
	store := open(t, ":memory:", empty)
	id := importXML(t, store, "writer", writerFeed("Old body", "one"), "https://writer.example/feed")
	entered, release := make(chan struct{}), make(chan struct{})
	x := withFragment(direct)
	x.Fragment = func(html, base, kind string) (extract.Extracted, error) {
		if strings.Contains(html, "Old body") {
			close(entered)
			<-release
		}
		return extract.ExtractFeedBody(html, base, kind), nil
	}
	reader := New(store, offline("no network"), x)
	pending := later(reader, id)
	<-entered
	importXML(t, store, "writer", writerFeed("New body", "one"), "https://writer.example/feed")
	check(t, strings.Contains(read(t, reader, id, false).HTML, "New body"), "the newer feed body was not read")
	close(release)
	<-pending
	saved := content(t, store, id).HTML
	check(t, strings.Contains(saved, "New body") && !strings.Contains(saved, "Old body"), "%s", saved)
}

// Format failure on first retrieval preserves readable content instead of returning unavailable
func TestFormatFailureKeepsBody(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	article := first(t, store)
	html := `<h2>Readable source</h2><p>Keep the safely extracted body even when the optional structure conversion fails.</p>`
	x := bare(func(string, *library.ReaderSourceSnapshot) (extract.Formatted, error) {
		return extract.Formatted{}, errors.New("Conversion failed")
	})
	x.Page = func(string, string, bool) (extract.Extracted, error) {
		return extract.Extracted{Status: "ready", HTML: html, TextLength: 150, Links: []string{}, Images: []string{}, FeedURLs: []string{}}, nil
	}
	result := read(t, New(store, serve(htmlPage(html)), x), article.ID, false)
	check(t, result.Status == "ready" && result.HTML == html && !hasWork(result.Document) && result.RefreshError != nil && strings.Contains(*result.RefreshError, "已保留可阅读"), "%+v", result)
	check(t, content(t, store, article.ID).HTML == html, "the body was not kept")
}

// Identical display HTML cannot coalesce different source revisions during cache upgrade
func TestSourceRevisionsNotCoalesced(t *testing.T) {
	store := open(t, ":memory:", snapshot(t))
	article := first(t, store)
	source := func(name string) *library.ReaderSourceSnapshot {
		return &library.ReaderSourceSnapshot{HTML: `<p>Rendered body</p><img src="https://example.com/` + name + `.png" alt="Picture">`, Provenance: "source"}
	}
	version, missing := 2, 1
	old := library.ArticleContent{ArticleID: article.ID, Status: "ready", HTML: `<p>Rendered body</p><span>图片暂未载入：Picture</span>`, TextLength: 150, Links: []string{},
		FetchedAt: "2026-11-02T00:00:00Z", SourceURL: article.URL, SourceKind: "page", FromCache: true, ImageCacheVersion: &version, MissingImageCount: &missing, SourceSnapshot: source("one")}
	save(t, store, old)
	format, entered, release := gate()
	defer release()
	reader := New(store, offline("No network"), bare(format))
	firstRead := later(reader, article.ID)
	<-entered
	newer := old
	newer.SourceSnapshot = source("two")
	save(t, store, newer)
	secondRead := later(reader, article.ID)
	<-entered
	release()
	oldResult, newResult := <-firstRead, <-secondRead
	revision := func(doc json.RawMessage) string {
		var d struct {
			ContentRevision string `json:"contentRevision"`
		}
		json.Unmarshal(doc, &d)
		return d.ContentRevision
	}
	saved := content(t, store, article.ID)
	check(t, revision(oldResult.Document) != revision(newResult.Document) && revision(saved.Document) == revision(newResult.Document), "revisions %s %s %s",
		revision(oldResult.Document), revision(newResult.Document), revision(saved.Document))
	check(t, *saved.SourceSnapshot == *source("two"), "%+v", saved.SourceSnapshot)
}

// --- Picture conversion and the worker.

// An AVIF picture, which the native reader cannot decode, is kept as PNG when it has transparency and as JPEG when not,
// converted by sips; one sips cannot read counts as not loaded. The files are real AVIF (ffmpeg's SVT-AV1, Pillow).
func TestAVIFConverted(t *testing.T) {
	if _, err := exec.LookPath("sips"); err != nil {
		t.Skip("sips is macOS's")
	}
	opaque := must[[]byte](t)(os.ReadFile("testdata/opaque.avif"))
	alpha := must[[]byte](t)(os.ReadFile("testdata/alpha.avif"))
	broken := opaque[:32] // its file type box alone
	check(t, extract.ImageType(opaque) == "image/avif" && extract.ImageType(alpha) == "image/avif" && extract.ImageType(broken) == "image/avif", "not AVIF")
	files := map[string][]byte{"/opaque.avif": opaque, "/alpha.avif": alpha, "/broken.avif": broken}
	fetcher := serve(func(url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		for name, body := range files {
			if strings.HasSuffix(url, name) {
				return reply(url, 200, "image/avif", body), nil
			}
		}
		return reply(url, 200, "text/html", []byte(strings.Replace(page, "</article>", `<img src="/opaque.avif" alt="Opaque"><img src="/alpha.avif" alt="Alpha"><img src="/broken.avif" alt="Broken"></article>`, 1))), nil
	})
	store := open(t, ":memory:", snapshot(t))
	result := read(t, New(store, fetcher, direct), first(t, store).ID, false)
	check(t, count(result.MissingImageCount) == 1 && !strings.Contains(result.HTML, "image/avif"), "%d missing: %s", count(result.MissingImageCount), result.HTML)
	decoded := func(kind string) image.Config {
		m := regexp.MustCompile(`src="data:` + kind + `;base64,([^"]+)"`).FindStringSubmatch(result.HTML)
		check(t, m != nil, "no %s", kind)
		body, _ := base64.StdEncoding.DecodeString(m[1])
		config, format, err := image.DecodeConfig(strings.NewReader(string(body)))
		check(t, err == nil && "image/"+format == kind, "%s: %v %s", kind, err, format)
		return config
	}
	photo, drawing := decoded("image/jpeg"), decoded("image/png")
	check(t, photo.Width == 64 && photo.Height == 48 && drawing.Width == 40 && drawing.Height == 30, "%+v %+v", photo, drawing)
	// A work page's pictures are converted too.
	x := withFragment(direct)
	x.Page = func(string, string, bool) (extract.Extracted, error) {
		return extract.Extracted{Status: "ready", HTML: "<p>A site.</p>", TextLength: 7, Links: []string{}, Images: []string{}, FeedURLs: []string{},
			Work: &extract.Work{Name: "Site", Facts: []extract.Fact{}, Sections: []extract.Shot{{Name: "Hero", Image: str("https://g.example/alpha.avif")}, {Name: "Gone", Image: str("https://g.example/broken.avif")}}, Poster: str("https://g.example/opaque.avif")}}, nil
	}
	w := workOf(t, preview(New(store, fetcher, x), Target{ID: "explore:https://g.example/site", URL: "https://g.example/site", Aliases: []string{}, HomeURL: "https://g.example/", TrustedArticle: true}, ""))
	check(t, strings.HasPrefix(value(w.Poster), "data:image/jpeg;base64,") && strings.HasPrefix(value(w.Sections[0].Image), "data:image/png;base64,"), "poster %.30s, shot %.30s", value(w.Poster), value(w.Sections[0].Image))
	check(t, w.Sections[1].Image == nil && value(w.Sections[1].Source.Value) == "https://g.example/broken.avif", "the broken shot keeps its source: %+v", w.Sections[1])
}

// FromAVIF, which Explore's pictures take too: JPEG for an opaque picture, PNG for one with transparency, nothing for
// what sips cannot read or once the context has ended.
func TestFromAVIF(t *testing.T) {
	if _, err := exec.LookPath("sips"); err != nil {
		t.Skip("sips is macOS's")
	}
	opaque := must[[]byte](t)(os.ReadFile("testdata/opaque.avif"))
	alpha := must[[]byte](t)(os.ReadFile("testdata/alpha.avif"))
	ctx := context.Background()
	for _, c := range []struct {
		body []byte
		kind string
	}{{opaque, "image/jpeg"}, {alpha, "image/png"}, {opaque[:32], ""}, {[]byte("not a picture"), ""}} {
		data, kind := FromAVIF(ctx, c.body)
		check(t, kind == c.kind && (kind == "") == (data == nil), "%d bytes: %q, %d bytes", len(c.body), kind, len(data))
		if kind != "" {
			check(t, extract.ImageType(data) == kind, "%s is %s", kind, extract.ImageType(data))
		}
	}
	gone, cancel := context.WithCancel(ctx)
	cancel()
	data, kind := FromAVIF(gone, opaque)
	check(t, kind == "" && data == nil, "a context that ended: %q", kind)
}

// Worker's jobs run one at a time; one that runs over is given up and the next starts; a panic fails only its job.
func TestWorkerJobs(t *testing.T) {
	defer func(d time.Duration) { jobTimeout = d }(jobTimeout)
	jobTimeout = 50 * time.Millisecond
	var active, peak atomic.Int32
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			run(func() (int, error) {
				n := active.Add(1)
				for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
				}
				time.Sleep(5 * time.Millisecond)
				active.Add(-1)
				return 0, nil
			})
		})
	}
	wg.Wait()
	check(t, peak.Load() == 1, "%d jobs at once", peak.Load())
	stuck := make(chan struct{})
	defer close(stuck)
	_, err := run(func() (int, error) { <-stuck; return 1, nil })
	check(t, err == errTimeout, "a job running over: %v", err)
	v, err := run(func() (int, error) { return 2, nil })
	check(t, v == 2 && err == nil, "the next job: %d %v", v, err)
	_, err = run(func() (int, error) { panic("boom") })
	check(t, err == errJob, "a panic: %v", err)
	_, err = run(func() (int, error) { return 0, errors.New("parse") })
	check(t, err == errJob, "an error: %v", err)
	page := must[extract.Extracted](t)(Worker.Page(`<html><article><h1>Reader</h1><p>`+long+`</p></article></html>`, "https://example.com/post", true))
	check(t, page.Status == "ready", "the worker's page: %s", page.Status)
}

// The gallery-page cases' entry pages, reduced to the parts the adapters read.
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

func shot(id string) string { return "https://assets.awwwards.example/awards/element/2026/09/" + id }

const (
	blankPNG = "data:image/png;base64,iVBORw0KGgo="
	awardURL = "https://www.awwwards.com/sites/agrumea-farm"
)

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
