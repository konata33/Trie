package explore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/reading"
	"trie/internal/subscription"
	"trie/internal/web"
)

// Explore's cases that are not the store's (those are in library/feed_test.go), and the app's handlers.

var empty = library.Snapshot{Version: 1, AsOf: "2026-11-01", Sources: []library.SourceSnapshot{}, Articles: []library.ArticleSnapshot{}, ReviewItems: []library.ReviewItem{}}

func ptr[T any](v T) *T { return &v }

func write(t *testing.T, file string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCatalog(t *testing.T, dir string, entries ...catalogEntry) {
	t.Helper()
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "catalog.json"), data)
}

func openStore(t *testing.T) *library.Store {
	t.Helper()
	s, err := library.Open(":memory:", empty)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// service is New, waiting for what it reads behind the list before the test's folders go.
func service(t *testing.T, o Options) *Service {
	s := New(o)
	t.Cleanup(s.bg.Wait)
	return s
}

// feedsOf reads each feed as parsed xml.
func feedsOf(t *testing.T, xml func(url string) string) func(context.Context, string) (library.ParsedFeed, error) {
	return func(_ context.Context, url string) (library.ParsedFeed, error) { return parsed(t, xml(url), url), nil }
}

// hanging is a read that never answers while the test runs.
func hanging(t *testing.T) func(context.Context, string) (library.ParsedFeed, error) {
	return func(context.Context, string) (library.ParsedFeed, error) {
		<-t.Context().Done()
		return library.ParsedFeed{}, errors.New("gone")
	}
}

func list(t *testing.T, s *Service) map[string]Entry {
	t.Helper()
	entries, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Entry{}
	for _, e := range entries {
		out[e.ID] = e
	}
	return out
}

func is(p *string, want string) bool { return p != nil && *p == want }

func check(t *testing.T, ok bool, why string, got ...any) {
	t.Helper()
	if !ok {
		t.Errorf("%s: %v", why, got)
	}
}

// explore list reads the catalog, thumbnails and feeds, matches subscriptions and caches feed stats
func TestListCatalog(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "explore-shots", "site.example-400.jpg"), []byte{0xff, 0xd8, 0xff})
	write(t, filepath.Join(dir, "explore-shots", "writer.example-400.jpg"), []byte{0xff, 0xd8, 0xff})
	write(t, filepath.Join(dir, "icons", "writer.png"), []byte{0x89, 0x50, 0x4e, 0x47})
	writeCatalog(t, dir,
		catalogEntry{ID: "writer", Name: "Writer", Kind: "blog", HomeURL: "https://writer.example", Host: "writer.example", FeedURL: ptr("https://writer.example/feed"), Intro: "写作"},
		catalogEntry{ID: "site", Name: "Site", Kind: "site", HomeURL: "https://www.site.example/", Host: "site.example", Intro: "站点"},
		catalogEntry{ID: "dead", Name: "Dead", Kind: "blog", HomeURL: "https://dead.example", Host: "dead.example", FeedURL: ptr("https://dead.example/feed"), Intro: "失效"})
	var calls atomic.Int32
	fetchFeed := func(_ context.Context, url string) (library.ParsedFeed, error) {
		calls.Add(1)
		if strings.Contains(url, "dead") {
			return library.ParsedFeed{}, errors.New("down")
		}
		return parsed(t, rss([2]string{"p", daysAgo(5)}), url), nil
	}
	sources := []library.Source{{SourceSnapshot: library.SourceSnapshot{ID: "feed:1", HomeURL: "https://writer.example/"}, Subscribed: true, FeedURL: ptr("https://www.writer.example/feed/")},
		{SourceSnapshot: library.SourceSnapshot{ID: "feed:2", HomeURL: "https://site.example/"}, Subscribed: true}}
	cache := filepath.Join(dir, "cache.json")
	byID := list(t, service(t, Options{Resources: dir, Cache: cache, FetchFeed: fetchFeed, Sources: func() []library.Source { return sources }}))
	check(t, is(byID["writer"].SubscribedSourceID, "feed:1") && is(byID["site"].SubscribedSourceID, "feed:2") && byID["dead"].SubscribedSourceID == nil, "subscriptions",
		byID["writer"].SubscribedSourceID, byID["site"].SubscribedSourceID, byID["dead"].SubscribedSourceID)
	check(t, byID["site"].Thumb != nil && strings.HasPrefix(*byID["site"].Thumb, "data:image/jpeg;base64,"), "a design site shows its captured first screen")
	check(t, byID["writer"].Thumb == nil, "a writer is shown by their posts, not their site")
	check(t, byID["writer"].Icon != nil && strings.HasPrefix(*byID["writer"].Icon, "data:image/png;base64,"), "the catalog icon")
	s := service(t, Options{Resources: dir, Cache: cache, FetchFeed: fetchFeed, Sources: func() []library.Source { return sources }})
	check(t, is(s.IconFor("https://www.writer.example/about"), *byID["writer"].Icon) && s.IconFor("https://elsewhere.example/") == nil, "iconFor")
	check(t, slices.Contains(s.Links(), "https://writer.example") && slices.Contains(s.Links(), "https://writer.example/p/"), "links", s.Links())
	w, dead := byID["writer"], byID["dead"]
	check(t, len(w.Posts) == 1 && intIs(w.Last90, 1) && intIs(w.Recent30, 1), "the writer's stats", w.Posts, w.Last90, w.Recent30)
	check(t, len(dead.Posts) == 0 && dead.Last90 == nil, "a dead feed has none")
	check(t, is(dead.FetchError, "down") && w.FetchError == nil, "fetchError", dead.FetchError, w.FetchError)
	check(t, calls.Load() == 2, "each feed is read once", calls.Load())
	hung := list(t, service(t, Options{Resources: dir, Cache: cache, FetchFeed: hanging(t)}))
	check(t, len(hung["writer"].Posts) == 1, "a fresh disk cache is shown at once; the warm-up refresh runs behind it")
	// A feed that fails after a good read keeps its posts.
	flaky := filepath.Join(dir, "flaky.json")
	var fail atomic.Bool
	flakyFetch := func(_ context.Context, url string) (library.ParsedFeed, error) {
		if fail.Load() {
			return library.ParsedFeed{}, errors.New("down")
		}
		return parsed(t, rss([2]string{"p", daysAgo(5)}), url), nil
	}
	first := service(t, Options{Resources: dir, Cache: flaky, FetchFeed: flakyFetch})
	list(t, first)
	first.bg.Wait()
	fail.Store(true)
	var saved map[string]map[string]any
	data, _ := os.ReadFile(flaky)
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	for _, st := range saved {
		st["at"] = 0
	}
	data, _ = json.Marshal(saved)
	write(t, flaky, data)
	again := service(t, Options{Resources: dir, Cache: flaky, FetchFeed: flakyFetch})
	list(t, again)
	again.bg.Wait()
	kept := list(t, again)["writer"]
	check(t, len(kept.Posts) == 1 && is(kept.FetchError, "down"), "old posts stay, and the failure is told rather than hidden", kept.Posts, kept.FetchError)
	// The shipped seed is used when there is no cache yet.
	data, _ = os.ReadFile(flaky)
	write(t, filepath.Join(dir, "seed.json"), data)
	seeded := list(t, service(t, Options{Resources: dir, Cache: filepath.Join(dir, "none.json"), FetchFeed: hanging(t)}))
	check(t, len(seeded["writer"].Posts) == 1, "seed content shows at once while the refresh runs in the background")
}

// an Explore post previews from its feed body without touching the library
func TestPreviewFromFeedBody(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t)
	writeCatalog(t, dir, catalogEntry{ID: "writer", Name: "Writer", Kind: "blog", HomeURL: "https://writer.example", Host: "writer.example", FeedURL: ptr("https://writer.example/feed")})
	// Long enough to be a whole article (internal/reading's shortFeedText); a shorter body is checked against the page.
	body := "<p>" + strings.Repeat("A real paragraph from the feed body. ", 50) + "</p>"
	xml := `<rss xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Writer</title><link>https://writer.example/</link><item><title>Post</title><link>https://writer.example/post/</link><pubDate>` +
		daysAgo(2) + `</pubDate><content:encoded><![CDATA[` + body + `]]></content:encoded></item></channel></rss>`
	cache := filepath.Join(dir, "cache.json")
	explore := service(t, Options{Resources: dir, Cache: cache, FetchFeed: feedsOf(t, func(string) string { return xml })})
	list(t, explore)
	ctx := context.Background()
	if _, err := explore.ReadTarget(ctx, "writer", "https://writer.example/other/"); err == nil || err.Error() != "推荐里没有这篇文章" {
		t.Fatalf("a post Explore does not list: %v", err)
	}
	target, err := explore.ReadTarget(ctx, "writer", "https://writer.example/post/")
	if err != nil || target.FeedBody == nil || target.FeedBody.Kind != "body" {
		t.Fatalf("%+v %v", target, err)
	}
	fetched := 0
	reader := reading.New(store, func() fetch.Fetch {
		return func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
			fetched++
			return &fetch.Response{URL: url, Status: 404, Type: "text/html", Body: []byte{}}, nil
		}
	}, reading.Worker)
	content := reader.Preview(ctx, target, "auto", reading.PreviewWait)
	check(t, content.Status == "ready" && content.SourceKind == "feed" && strings.Contains(content.HTML, "A real paragraph from the feed body"), "the feed body", content.Status, content.SourceKind)
	check(t, fetched == 0, "a full feed body needs no page fetch", fetched)
	st, _ := store.Library()
	check(t, len(st.Articles) == 0, "the library is not touched", len(st.Articles))
	// After a restart only the cache is left; the feed is read again to find the body.
	restarted := service(t, Options{Resources: dir, Cache: cache, FetchFeed: feedsOf(t, func(string) string { return xml })})
	if target, err := restarted.ReadTarget(ctx, "writer", "https://writer.example/post/"); err != nil || target.FeedBody == nil || target.FeedBody.Kind != "body" {
		t.Fatalf("after a restart: %+v %v", target, err)
	}
	// When that read fails (a reset connection), the post is still opened, from its page.
	offline := service(t, Options{Resources: dir, Cache: cache, FetchFeed: func(context.Context, string) (library.ParsedFeed, error) {
		return library.ParsedFeed{}, errors.New("连接被中断，请稍后重试")
	}})
	if target, err := offline.ReadTarget(ctx, "writer", "https://writer.example/post/"); err != nil || target.URL != "https://writer.example/post/" || target.FeedBody != nil {
		t.Fatalf("offline: %+v %v", target, err)
	}
}

// a gallery entry carries its latest works and where their pictures are; writers and showcase sites carry none
func TestGalleryWorks(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir,
		catalogEntry{ID: "gallery", Name: "Gallery", Kind: "site", Section: "设计灵感画廊（有可用 RSS）", HomeURL: "https://g.example", Host: "g.example", FeedURL: ptr("https://g.example/feed"), Intro: "画廊"},
		catalogEntry{ID: "showcase", Name: "Showcase", Kind: "site", Section: "设计范例站点（design-site）", HomeURL: "https://s.example", Host: "s.example", FeedURL: ptr("https://s.example/feed"), Intro: "站点"})
	xml := func(url string) string {
		h := host(url)
		return `<rss xmlns:media="http://search.yahoo.com/mrss/" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>G</title><link>https://` + h + `/</link>
      <item><title>Website Inspiration: Body</title><link>https://` + h + `/body</link><pubDate>` + daysAgo(1) + `</pubDate><content:encoded><![CDATA[<p><img src="/shots/body.jpg"></p>]]></content:encoded></item>
      <item><title>Attached</title><link>https://` + h + `/attached</link><pubDate>` + daysAgo(2) + `</pubDate><media:content url="https://cdn.example/a.jpg?w=1600&amp;h=1000" medium="image" /></item>
      <item><title>Bare</title><link>https://` + h + `/bare</link><pubDate>` + daysAgo(3) + `</pubDate></item></channel></rss>`
	}
	s := service(t, Options{Resources: dir, Cache: filepath.Join(dir, "cache.json"), FetchFeed: feedsOf(t, xml)})
	byID := list(t, s)
	var titles []string
	for _, w := range byID["gallery"].Works {
		titles = append(titles, w.Title)
	}
	check(t, slices.Equal(titles, []string{"Body", "Attached"}), "only entries with a picture, newest first, without the gallery label", titles)
	img := func(i int) string { src, _ := s.workImage("gallery", i); return src }
	check(t, img(0) == "https://g.example/shots/body.jpg", "a body picture resolved against the entry", img(0))
	check(t, img(1) == "https://cdn.example/a.jpg?w=1600&h=1000", "a Media RSS picture outside the body", img(1))
	check(t, img(2) == "", "no third", img(2))
	check(t, byID["showcase"].Works != nil && len(byID["showcase"].Works) == 0, "a showcase site is shown by its own page")
	check(t, slices.Contains(s.Links(), "https://g.example/attached"), "links")
}

// upstream picks become writers on their own sites, beside the catalog writers
func TestUpstreamPicks(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir, catalogEntry{ID: "writer", Name: "Writer", Kind: "blog", HomeURL: "https://writer.example", Host: "writer.example", FeedURL: ptr("https://writer.example/feed"), Intro: "写作"})
	item := func(title, link, author, date string) string {
		return `<item><title>` + title + `</title><link>` + strings.ReplaceAll(link, "&", "&amp;") + `</link><author>` + author + `</author><pubDate>` + date +
			`</pubDate><description>The opening of ` + title + `, long enough to stand as an excerpt on the page.</description></item>`
	}
	channel := func(items string) string {
		return `<rss><channel><title>Up</title><link>https://up.example/</link>` + items + `</channel></rss>`
	}
	ahead := time.Now().Add(24 * time.Hour).UTC().Format(http.TimeFormat)
	var chosen []Upstream
	for _, u := range Upstreams {
		if u.Name == "Hacker News" || u.Name == "UX Collective" || u.Name == "Lobsters" {
			chosen = append(chosen, u)
		}
	}
	feedURL := func(name string) string {
		return chosen[slices.IndexFunc(chosen, func(u Upstream) bool { return u.Name == name })].FeedURL
	}
	feeds := map[string]string{
		feedURL("Hacker News"): channel(strings.Replace(item("Theirs", "https://other.example/theirs/", "submitter", daysAgo(2)), "</description>",
			`&lt;img src="/cover.jpg"&gt; &lt;a href="https://hn.example/#t"&gt;[summary]&lt;/a&gt; &lt;a href="https://hn.example/c"&gt;[comments]&lt;/a&gt;</description>`, 1) +
			item("Mine", "https://writer.example/mine/", "submitter", daysAgo(1)) + item("Ahead", "https://third.example/ahead/", "submitter", ahead)),
		feedURL("UX Collective"): channel(item("An essay", "https://uxdesign.cc/an-essay-1a2b?source=rss----1", "Jane Doe", daysAgo(1)) +
			item("A teaser", "https://uxdesign.cc/a-teaser-9z?source=rss----1", "Nil Doe", daysAgo(1))),
		feedURL("Lobsters"): channel(item("Theirs", "https://other.example/theirs/", "other.example by someone", daysAgo(2)) + item("Ask", "https://lobste.rs/s/abc/ask", "by someone", daysAgo(1)) +
			`<item><title>Bare</title><link>https://bare.example/post/</link><pubDate>` + daysAgo(1) + `</pubDate><description>&lt;a href="https://lobste.rs/s/x"&gt;Comments&lt;/a&gt;</description></item>` +
			`<item><title>Walled</title><link>https://walled.example/post/</link><pubDate>` + daysAgo(1) + `</pubDate><description>&lt;a href="https://lobste.rs/s/y"&gt;Comments&lt;/a&gt;</description></item>` +
			`<item><title>Whole</title><link>https://whole.example/post/</link><pubDate>` + daysAgo(1) + `</pubDate><description>` + strings.Repeat("The whole post is in the feed, a few paragraphs long. ", 9) + `</description></item>`),
	}
	fetchFeed := feedsOf(t, func(url string) string {
		if xml, ok := feeds[url]; ok {
			return xml
		}
		return rss([2]string{"p", daysAgo(5)})
	})
	refreshed := make(chan struct{}, 1)
	changed := func() {
		select {
		case refreshed <- struct{}{}:
		default:
		}
	}
	var asked atomic.Int32
	findIcon := func(_ context.Context, home string) (*string, error) {
		asked.Add(1)
		if home == "https://uxdesign.cc/" {
			return nil, errors.New("reset")
		}
		if home == "https://other.example/" {
			return ptr("data:image/png;base64,AA"), nil
		}
		return nil, nil
	}
	// Pages read for posts whose feed gives less than an article: most read; one is behind a wall, one is a member-only teaser.
	var mu sync.Mutex
	var pages []string
	readPage := func(_ context.Context, url, _ string) string {
		mu.Lock()
		pages = append(pages, url)
		mu.Unlock()
		switch {
		case strings.HasPrefix(url, "https://walled.example/") || strings.Contains(url, "a-teaser"):
			return ""
		case strings.HasPrefix(url, "https://bare.example/"):
			return "<p>The page of Bare, read in full because its upstream gave only a link to the discussion.</p>"
		}
		return "<p>The whole article, read from its page.</p>"
	}
	cache := filepath.Join(dir, "cache.json")
	explore := service(t, Options{Resources: dir, Cache: cache, FetchFeed: fetchFeed, Changed: changed, Upstreams: chosen, FindIcon: findIcon, ReadPage: readPage})
	for id := range list(t, explore) {
		// The upstreams are read behind the list, which is made from what was known before.
		check(t, !strings.HasPrefix(id, "pick:"), "the first list shows no recommendation", id)
	}
	<-refreshed
	_, shown := list(t, explore)["pick:other.example"]
	check(t, !shown, "a post whose feed gives a teaser waits for its page")
	explore.bg.Wait()
	byID := list(t, explore)
	var ids []string
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	check(t, slices.Equal(ids, []string{"pick:bare.example", "pick:other.example", "pick:third.example", "pick:uxdesign.cc:Jane Doe", "pick:whole.example", "writer"}),
		"a catalog writer keeps only its own feed, a discussion on the aggregator itself is not a post, and a teaser whose page does not read is never shown", ids)
	check(t, !slices.ContainsFunc(pages, func(url string) bool { return strings.HasPrefix(url, "https://whole.example/") }), "a post the feed gives whole is shown without reading its page", pages)
	other := byID["pick:other.example"]
	check(t, other.Name == "other.example" && len(other.Posts) == 1 && is(other.FeedURL, "https://other.example/") && other.Intro == "Hacker News 推荐",
		"the site stands for the writer and a repeat is merged", other.Name, len(other.Posts), other.FeedURL, other.Intro)
	check(t, regexp.MustCompile(`^The opening of .*page\.$`).MatchString(other.Posts[0].Excerpt), "the digest's own [summary] [comments] links are not part of the opening", other.Posts[0].Excerpt)
	essay := byID["pick:uxdesign.cc:Jane Doe"]
	check(t, essay.Name == "Jane Doe" && essay.Posts[0].URL == "https://uxdesign.cc/an-essay-1a2b" && essay.FeedURL == nil, "writers on a shared publication are told apart by name", essay)
	at, _ := web.JSDateParse(*byID["pick:third.example"].Posts[0].PublishedAt)
	check(t, at <= time.Now().UnixMilli(), "a post dated ahead is never newer than now", *byID["pick:third.example"].Posts[0].PublishedAt)
	target, err := explore.ReadTarget(context.Background(), "pick:other.example", "https://other.example/theirs/")
	check(t, err == nil && target.URL == "https://other.example/theirs/" && target.HomeURL == "https://other.example/" && target.FeedBody != nil, "the post's target", target, err)
	check(t, slices.Contains(explore.Links(), "https://other.example/theirs/"), "links")
	site, _ := explore.site("pick:other.example")
	check(t, site != nil && site.HomeURL == "https://other.example/" && site.Host == "other.example" && site.Writer && slices.Equal(site.Pictures, []string{"https://other.example/cover.jpg"}),
		"a recommended writer stands on their latest post's pictures", site)
	third, _ := explore.site("pick:third.example")
	check(t, third != nil && third.Pictures != nil && len(third.Pictures) == 0, "no pictures", third)
	check(t, len(explore.designSites()) == 0, "writers are never captured; only the design room's sites are")
	// A writer is never shown by their site, even when a capture of it is on disk.
	write(t, filepath.Join(dir, "explore-shots", "other.example-400.jpg"), []byte("small"))
	write(t, filepath.Join(dir, "explore-shots", "other.example-1280.jpg"), []byte("large"))
	check(t, list(t, explore)["pick:other.example"].Thumb == nil, "no thumb for a writer")
	check(t, explore.shot("pick:other.example") == "", "no shot for a writer")
	// Icons are looked for behind the list; the next read carries them, and they are kept for the next launch.
	explore.bg.Wait()
	again := list(t, explore)
	check(t, is(again["pick:other.example"].Icon, "data:image/png;base64,AA") && again["pick:third.example"].Icon == nil, "icons", again["pick:other.example"].Icon, again["pick:third.example"].Icon)
	check(t, strings.HasPrefix(again["pick:bare.example"].Posts[0].Excerpt, "The page of Bare"), "a post with no text of its own shows once its page is read", again["pick:bare.example"].Posts[0].Excerpt)
	_, walled := again["pick:walled.example"]
	check(t, !walled, "a post whose page cannot be read is never shown")
	explore.bg.Wait()
	check(t, asked.Load() == 5, "each site is asked once a session, an unreachable one included", asked.Load())
	var icons map[string]*string
	data, _ := os.ReadFile(filepath.Join(dir, "explore-icons.json"))
	if err := json.Unmarshal(data, &icons); err != nil {
		t.Fatal(err)
	}
	_, kept := icons["uxdesign.cc"]
	check(t, !kept, "an unreachable site is not remembered as having no icon")
	later := service(t, Options{Resources: dir, Cache: cache, FetchFeed: fetchFeed, Upstreams: chosen, FindIcon: func(context.Context, string) (*string, error) {
		return nil, errors.New("asked again")
	}})
	check(t, is(list(t, later)["pick:other.example"].Icon, "data:image/png;base64,AA"), "the icon kept for the next launch")
	// After a restart, opening a recommended post reads its upstream again; what the pages showed is kept, so no writer
	// drops out until looked up anew.
	restarted := service(t, Options{Resources: dir, Cache: cache, FetchFeed: fetchFeed, Upstreams: chosen})
	list(t, restarted)
	if _, err := restarted.ReadTarget(context.Background(), "pick:other.example", "https://other.example/theirs/"); err != nil {
		t.Fatal(err)
	}
	after := list(t, restarted)
	for _, id := range []string{"pick:third.example", "pick:other.example", "pick:bare.example"} {
		_, ok := after[id]
		check(t, ok, "a post whose page read stays shown", id)
	}
	for _, id := range []string{"pick:walled.example", "pick:uxdesign.cc:Nil Doe"} {
		_, ok := after[id]
		check(t, !ok, "a post whose page did not read stays hidden", id)
	}
}

// subscriptions take the catalog name; unrenamed sources adopt it once, renamed ones keep theirs
// (with NameFor; the store's part is library/feed_test.go TestAdoptNames)
func TestNameFor(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir,
		catalogEntry{ID: "nng", Name: "Nielsen Norman Group", Kind: "blog", HomeURL: "https://www.nngroup.com", Host: "nngroup.com", FeedURL: ptr("https://www.nngroup.com/feed/rss/")},
		catalogEntry{ID: "fonts", Name: "Fonts In Use", Kind: "blog", HomeURL: "https://fontsinuse.com", Host: "fontsinuse.com", FeedURL: ptr("https://fontsinuse.com/main.rss")},
		catalogEntry{ID: "a", Name: "A", Kind: "blog", HomeURL: "https://shared.example/a", Host: "shared.example", FeedURL: ptr("https://shared.example/a.xml")},
		catalogEntry{ID: "b", Name: "B", Kind: "blog", HomeURL: "https://shared.example/b", Host: "shared.example", FeedURL: ptr("https://shared.example/b.xml")})
	explore := service(t, Options{Resources: dir, Cache: filepath.Join(dir, "cache.json"), FetchFeed: func(context.Context, string) (library.ParsedFeed, error) {
		return library.ParsedFeed{}, errors.New("offline")
	}})
	xml := func(title, home string) string {
		return `<rss><channel><title>` + title + `</title><link>` + home + `</link><item><title>p</title><link>` + home + `p/</link></item></channel></rss>`
	}
	urls := []string{"https://nngroup.com/feed/rss", "https://fontsinuse.com/other.rss", "https://shared.example/c.xml", "https://writer.example/feed"}
	feeds := map[string]string{
		// The catalog's feed, written without www. and the trailing slash.
		urls[0]: xml("NN/g latest articles and announcements", "https://www.nngroup.com/"),
		// Another feed of a site the catalog lists once: matched by the site.
		urls[1]: xml("Fonts In Use – Type in use, indexed and archived", "https://www.fontsinuse.com/"),
		// A site the catalog lists twice is not guessed at.
		urls[2]: xml("Shared C", "https://shared.example/"),
		urls[3]: xml("Real Writer", "https://writer.example/"),
	}
	get := func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		return &fetch.Response{URL: url, Status: 200, Type: "application/rss+xml", Body: []byte(feeds[url])}, nil
	}
	stores := []*library.Store{openStore(t), openStore(t)}
	subs := subscription.New(stores[0], func() fetch.Fetch { return get }, func(string) *string { return nil }, func(feedURL, home string) string { return explore.NameFor(&feedURL, home) })
	for _, url := range urls {
		p, err := subs.Preview(context.Background(), url)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := subs.Add(p.Token, true); err != nil {
			t.Fatal(err)
		}
	}
	names := func(s *library.Store) map[string]string {
		st, err := s.Library()
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, src := range st.Sources {
			out[src.ID] = src.DisplayName
		}
		return out
	}
	var added []string
	for _, n := range names(stores[0]) {
		added = append(added, n)
	}
	sort.Strings(added)
	check(t, slices.Equal(added, []string{"Fonts In Use", "Nielsen Norman Group", "Real Writer", "Shared C"}), "subscriptions take the catalog name", added)
	// A library subscribed before: every source is named by its feed's title, and the reader renamed one.
	old := stores[1]
	imp := func(id string, f library.ParsedFeed) {
		if _, err := old.ImportFeed(id, f, true, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, url := range urls {
		imp(url, parsed(t, feeds[url], url))
	}
	imp("nng-renamed", parsed(t, feeds[urls[0]], "https://www.nngroup.com/feed/rss/"))
	if _, err := old.Mutate(library.RenameSource{ID: "nng-renamed", Name: "UX research"}); err != nil {
		t.Fatal(err)
	}
	if err := old.AdoptNames(explore.NameFor); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{urls[0]: "Nielsen Norman Group", urls[1]: "Fonts In Use", urls[2]: "Shared C", urls[3]: "Real Writer", "nng-renamed": "UX research"}
	got := names(old)
	check(t, len(got) == len(want), "adopted names", got)
	for id, name := range want {
		check(t, got[id] == name, "adopted names", id, got[id])
	}
	before, _ := old.Library()
	if err := old.AdoptNames(explore.NameFor); err != nil {
		t.Fatal(err)
	}
	after, _ := old.Library()
	check(t, after.Revision == before.Revision, "a second run changes nothing", before.Revision, after.Revision)
}

// websites a design gallery features become design sites of their own, once each, sorted by when they were featured
func TestFeaturedSites(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir,
		catalogEntry{ID: "gallery", Name: "Gallery", Kind: "site", HomeURL: "https://g.example", Host: "g.example", FeedURL: ptr("https://g.example/feed"), Intro: "画廊", Section: "设计灵感画廊"},
		catalogEntry{ID: "known", Name: "Known", Kind: "site", HomeURL: "https://known.example", Host: "known.example", Intro: "范例", Section: "设计范例站点"})
	item := func(title string, links []string, date string) string {
		var a []string
		for _, href := range links {
			a = append(a, `<a href="`+href+`">`+href+`</a>`)
		}
		return `<item><title>` + title + `</title><link>https://g.example/` + title + `</link><pubDate>` + date + `</pubDate>` +
			`<description><![CDATA[<p><img src="https://g.example/` + title + `.jpg">` + strings.Join(a, " ") + `</p>]]></description></item>`
	}
	xml := `<rss><channel><title>Gallery</title><link>https://g.example/</link>` +
		item("Studio", []string{"https://twitter.com/gallery", "https://studio.example/"}, daysAgo(1)) +
		item("Again", []string{"https://studio.example/work"}, daysAgo(3)) +
		item("Catalogued", []string{"https://known.example/"}, daysAgo(2)) +
		item("Inside", []string{"https://g.example/about"}, daysAgo(2)) +
		item("Older", []string{"https://older.example/"}, daysAgo(6)) + `</channel></rss>`
	explore := service(t, Options{Resources: dir, Cache: filepath.Join(dir, "cache.json"), FetchFeed: feedsOf(t, func(string) string { return xml })})
	entries, err := explore.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var featured []Entry
	for _, e := range entries {
		if strings.HasPrefix(e.ID, "featured:") {
			featured = append(featured, e)
		}
	}
	var got [][5]string
	for _, e := range featured {
		got = append(got, [5]string{e.ID, e.Name, e.HomeURL, e.Intro, e.Kind})
	}
	check(t, slices.Equal(got, [][5]string{{"featured:studio.example", "Studio", "https://studio.example/", "Gallery 推荐", "site"}, {"featured:older.example", "Older", "https://older.example/", "Gallery 推荐", "site"}}),
		"accounts, the gallery's own pages and sites already in the catalog are not features; a site featured twice shows once, at its newest feature", got)
	t0, _ := web.JSDateParse(*featured[0].FeaturedAt.Value)
	t1, _ := web.JSDateParse(*featured[1].FeaturedAt.Value)
	check(t, t0 > t1, "newest first", t0, t1)
	site, _ := explore.site("featured:studio.example")
	check(t, site != nil && site.HomeURL == "https://studio.example/" && site.Host == "studio.example" && !site.Writer && len(site.Pictures) == 0, "a featured site stands on its first screen", site)
	var hosts []string
	for _, s := range explore.designSites() {
		hosts = append(hosts, s.Host)
	}
	check(t, slices.Equal(hosts, []string{"studio.example", "older.example", "g.example", "known.example"}), "featured sites are captured first", hosts)
	check(t, slices.Contains(explore.Links(), "https://studio.example/"), "links")
	check(t, explore.Intro(library.Source{SourceSnapshot: library.SourceSnapshot{Host: "studio.example", HomeURL: "https://studio.example/"}}) == "Gallery 推荐" &&
		explore.Intro(library.Source{SourceSnapshot: library.SourceSnapshot{Host: "x", HomeURL: "https://www.known.example/about"}}) == "范例" &&
		explore.Intro(library.Source{SourceSnapshot: library.SourceSnapshot{Host: "nowhere.example", HomeURL: "https://nowhere.example/"}}) == "",
		"an author page's intro is the entry's on its site")
}

// Explore's cases that belong to subscriptions: subscription.Service with Explore's IconFor.

// subscribing counts only the last 30 days as unread; undated posts stay unread (the subscription part)
func TestSubscribeLast30DaysUnread(t *testing.T) {
	store := openStore(t)
	get := func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		return &fetch.Response{URL: url, Status: 200, Type: "application/rss+xml", Body: []byte(rss([2]string{"new", daysAgo(3)}, [2]string{"old", daysAgo(45)}, [2]string{"nodate", ""}))}, nil
	}
	s := subscription.New(store, func() fetch.Fetch { return get }, func(home string) *string {
		if strings.Contains(home, "writer.example") {
			return ptr("data:image/png;base64,AA")
		}
		return nil
	}, nil)
	p, err := s.Preview(context.Background(), "https://writer.example/feed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(p.Token, true); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Library()
	read := map[string]bool{}
	for _, a := range st.Articles {
		read[a.Title] = a.Read
	}
	check(t, len(read) == 3 && !read["new"] && read["old"] && !read["nodate"], "read states", read)
	check(t, is(st.Sources[0].Icon, "data:image/png;base64,AA"), "a source subscribed from Explore keeps the catalog icon")
}

// refresh reports start, then done or failed, for every source
func TestRefreshProgress(t *testing.T) {
	store := openStore(t)
	if _, err := store.ImportFeed("good", parsed(t, rss([2]string{"a", daysAgo(1)}), "https://writer.example/feed"), true, nil, ""); err != nil {
		t.Fatal(err)
	}
	bad := parsed(t, rss(), "https://broken.example/feed")
	bad.HomeURL = "https://broken.example/"
	if _, err := store.ImportFeed("bad", bad, true, nil, ""); err != nil {
		t.Fatal(err)
	}
	get := func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		if strings.Contains(url, "broken") {
			return &fetch.Response{URL: url, Status: 500, Type: "text/plain", Body: []byte{}}, nil
		}
		return &fetch.Response{URL: url, Status: 200, Type: "application/rss+xml", Body: []byte(rss([2]string{"a", daysAgo(1)}, [2]string{"b", daysAgo(1)}))}, nil
	}
	var events []subscription.Progress
	result, err := subscription.New(store, func() fetch.Fetch { return get }, nil, nil).Refresh(context.Background(), "", func(p subscription.Progress) { events = append(events, p) })
	if err != nil {
		t.Fatal(err)
	}
	check(t, len(result.Failures) == 1, "one failure", result.Failures)
	// Feeds are read a few at a time, so events of different sources interleave; each source still starts before it ends.
	states := func(id string) (out []string) {
		for _, e := range events {
			if e.SourceID == id {
				out = append(out, e.State)
			}
		}
		return out
	}
	check(t, slices.Equal(states("good"), []string{"start", "done"}) && slices.Equal(states("bad"), []string{"start", "failed"}), "states", events)
	var finished []int
	for _, e := range events {
		check(t, e.Total == 2, "total", e)
		if e.State != "start" {
			finished = append(finished, e.Finished)
		}
		if e.State == "done" {
			check(t, e.ImportedCount != nil && *e.ImportedCount == 1, "imported", e)
		}
		if e.State == "failed" {
			check(t, e.Message != "", "a failure says why", e)
		}
	}
	sort.Ints(finished)
	check(t, slices.Equal(finished, []int{1, 2}), "finished", finished)
}

// a connection reset twice in a row reaches the reader as a plain Chinese message
func TestResetTwice(t *testing.T) {
	get := func(context.Context, string, func(string) bool, int) (*fetch.Response, error) {
		return nil, &fetch.NetError{Err: errors.New("Client network socket disconnected before secure TLS connection was established"), Transient: true}
	}
	_, err := subscription.New(openStore(t), func() fetch.Fetch { return get }, nil, nil).Preview(context.Background(), "https://writer.example/feed")
	check(t, err != nil && strings.Contains(err.Error(), "连接被中断，请稍后重试"), "the message", err)
}
