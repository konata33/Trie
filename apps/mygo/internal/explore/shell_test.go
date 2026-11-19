package explore

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/reading"
)

// The app's Explore handlers: FindIcon, the pictures Explore keeps, the stage's first screens, previews.

func pngOf(w, h int) []byte {
	var b bytes.Buffer
	png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, w, h)))
	return b.Bytes()
}

func jpegOf(w, h int) []byte {
	var b bytes.Buffer
	jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)), nil)
	return b.Bytes()
}

var webpBytes = append([]byte("RIFF\x10\x00\x00\x00WEBPVP8 "), make([]byte, 16)...)

// served is a fetch of the addresses in pages; others are unreachable.
func served(t *testing.T, pages map[string]*fetch.Response) (fetch.Fetch, *atomic.Int32) {
	var calls atomic.Int32
	return func(_ context.Context, url string, scope func(string) bool, _ int) (*fetch.Response, error) {
		calls.Add(1)
		if !scope("https://anywhere.example/") {
			t.Error("a redirect is refused")
		}
		if r, ok := pages[url]; ok {
			if r.URL == "" {
				r.URL = url
			}
			return r, nil
		}
		return nil, &fetch.NetError{Err: errors.New("unreachable " + url)}
	}, &calls
}

func decoded(t *testing.T, url string) (string, image.Config) {
	t.Helper()
	mime, data, ok := strings.Cut(strings.TrimPrefix(url, "data:"), ";base64,")
	if !ok {
		t.Fatalf("not a data URL: %.40s", url)
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(raw))
	return mime, cfg
}

func html(body string) *fetch.Response {
	return &fetch.Response{Status: 200, Type: "text/html; charset=utf-8", Body: []byte(body)}
}

func picture(mime string, data []byte) *fetch.Response {
	return &fetch.Response{Status: 200, Type: mime, Body: data}
}

// findIcon: the best icon a home page declares, drawn at most 96 wide; one decodeNative cannot read goes as it came;
// a site that answered with none is nil, one that could not be reached an error.
func TestFindIcon(t *testing.T) {
	ico := []byte{0, 0, 1, 0, 1, 0, 16, 16, 0, 0}
	get, _ := served(t, map[string]*fetch.Response{
		"https://big.example/":                html(`<link rel="icon" href="/small.png"><link rel="apple-touch-icon" href="/apple.png">`),
		"https://big.example/apple.png":       picture("image/png", pngOf(200, 100)),
		"https://ico.example/":                html(`<title>No links</title>`),
		"https://ico.example/favicon.ico":     picture("image/x-icon", ico),
		"https://webp.example/":               html(`<link rel="icon" href="/i.webp">`),
		"https://webp.example/i.webp":         picture("image/webp", webpBytes),
		"https://none.example/":               html(``),
		"https://none.example/favicon.ico":    {Status: 404, Type: "text/html", Body: []byte("missing")},
		"https://text.example/":               html(``),
		"https://text.example/favicon.ico":    picture("text/plain", []byte("not a picture")),
		"https://halfway.example/favicon.ico": picture("image/png", pngOf(32, 32)),
		"https://moved.example/":              {URL: "https://www.moved.example/", Status: 200, Type: "text/html", Body: []byte(`<link rel="icon" href="/m.png">`)},
		"https://www.moved.example/m.png":     picture("image/png", pngOf(64, 64)),
	})
	ctx := context.Background()
	icon := func(home string) (*string, error) { return findIcon(ctx, get, home) }
	if got, err := icon("https://big.example/"); err != nil || got == nil {
		t.Fatal(got, err)
	} else if mime, cfg := decoded(t, *got); mime != "image/png" || cfg.Width != 96 || cfg.Height != 48 {
		t.Fatalf("the apple-touch-icon, drawn 96 wide: %s %+v", mime, cfg)
	}
	if got, err := icon("https://ico.example/"); err != nil || !is(got, "data:image/x-icon;base64,"+base64.StdEncoding.EncodeToString(ico)) {
		t.Fatal("an ICO goes as it came", got, err)
	}
	if got, err := icon("https://webp.example/"); err != nil || got == nil || !strings.HasPrefix(*got, "data:image/webp;base64,") {
		t.Fatal("a WebP goes as it came", got, err)
	}
	for _, home := range []string{"https://none.example/", "https://text.example/"} {
		if got, err := icon(home); got != nil || err != nil {
			t.Fatalf("%s answered with no icon: %v %v", home, got, err)
		}
	}
	if got, err := icon("https://down.example/"); got != nil || err == nil {
		t.Fatal("an unreachable site is an error", got, err)
	}
	if got, err := icon("https://halfway.example/"); err != nil || got == nil {
		t.Fatal("a home page that could not be read still has its favicon", got, err)
	} else if _, cfg := decoded(t, *got); cfg.Width != 32 {
		t.Fatalf("a small icon stays as it is: %+v", cfg)
	}
	if got, err := icon("https://moved.example/"); err != nil || got == nil {
		t.Fatal("icons are found where the home page moved to", got, err)
	}
}

func galleryOf(t *testing.T, images ...string) (string, func(context.Context, string) (library.ParsedFeed, error)) {
	dir := t.TempDir()
	writeCatalog(t, dir, catalogEntry{ID: "gallery", Name: "Gallery", Kind: "site", Section: "设计灵感画廊", HomeURL: "https://g.example", Host: "g.example", FeedURL: ptr("https://g.example/feed")},
		catalogEntry{ID: "writer", Name: "Writer", Kind: "blog", HomeURL: "https://writer.example", Host: "writer.example", FeedURL: ptr("https://writer.example/feed")},
		catalogEntry{ID: "site", Name: "Site", Kind: "site", Section: "设计范例站点", HomeURL: "https://www.site.example/", Host: "site.example"})
	xml := func(url string) string {
		var items strings.Builder
		for i, src := range images {
			items.WriteString(`<item><title>W` + string(rune('a'+i)) + `</title><link>https://g.example/w` + string(rune('a'+i)) + `</link><pubDate>` + daysAgo(i+1) +
				`</pubDate><description><![CDATA[<p><img src="` + src + `"></p>]]></description></item>`)
		}
		if strings.Contains(url, "writer") {
			return `<rss><channel><title>Writer</title><link>https://writer.example/</link><item><title>Post</title><link>https://writer.example/post/</link><pubDate>` + daysAgo(1) +
				`</pubDate><description><![CDATA[<p>Words.</p><img src="https://cdn.example/logo.png"><img src="https://cdn.example/wide.jpg">]]></description></item></channel></rss>`
		}
		return `<rss><channel><title>G</title><link>https://g.example/</link>` + items.String() + `</channel></rss>`
	}
	return dir, feedsOf(t, xml)
}

// Work: a work's picture, fetched once, shrunk to 1200 wide and kept on disk; one decodeNative cannot read goes as it
// came; one that cannot be had is "" for the session.
func TestWorkPictures(t *testing.T) {
	dir, fetchFeed := galleryOf(t, "https://cdn.example/big.jpg", "https://cdn.example/w.webp", "https://cdn.example/gone.jpg")
	get, calls := served(t, map[string]*fetch.Response{
		"https://cdn.example/big.jpg":  picture("image/jpeg", jpegOf(1600, 1000)),
		"https://cdn.example/w.webp":   picture("image/webp", webpBytes),
		"https://cdn.example/gone.jpg": {Status: 404, Type: "text/html", Body: []byte("gone")},
	})
	o := Options{Resources: dir, Cache: filepath.Join(dir, "cache.json"), FetchFeed: fetchFeed, Fetch: get}
	s := service(t, o)
	list(t, s)
	ctx := context.Background()
	big, err := s.Work(ctx, "gallery", 0)
	if err != nil {
		t.Fatal(err)
	}
	if mime, cfg := decoded(t, big); mime != "image/jpeg" || cfg.Width != 1200 || cfg.Height != 750 {
		t.Fatalf("shrunk to 1200: %s %+v", mime, cfg)
	}
	sum := sha1.Sum([]byte("https://cdn.example/big.jpg"))
	if _, err := os.Stat(filepath.Join(dir, "explore-works", hex.EncodeToString(sum[:])+"-1200.jpg")); err != nil {
		t.Fatal("kept on disk:", err)
	}
	if webp, _ := s.Work(ctx, "gallery", 1); webp != "data:image/webp;base64,"+base64.StdEncoding.EncodeToString(webpBytes) {
		t.Fatalf("a WebP goes as it came: %.40s", webp)
	}
	gone, _ := s.Work(ctx, "gallery", 2)
	again, _ := s.Work(ctx, "gallery", 0)
	n := calls.Load()
	s.Work(ctx, "gallery", 2)
	if gone != "" || again != big || calls.Load() != n || n != 3 {
		t.Fatalf("each picture is fetched once a session: %d fetches", n)
	}
	for _, i := range []int{-1, 3, 8} {
		if w, err := s.Work(ctx, "gallery", i); w != "" || err != nil {
			t.Fatalf("work %d: %.20s %v", i, w, err)
		}
	}
	// The next launch reads what is kept.
	o.Fetch = func(context.Context, string, func(string) bool, int) (*fetch.Response, error) {
		return nil, errors.New("offline")
	}
	later := service(t, o)
	list(t, later)
	if kept, _ := later.Work(ctx, "gallery", 0); kept != big {
		t.Fatal("the kept copy is read")
	}
}

// A picture too narrow to stand behind a writer still shows as a work, whichever is asked for first.
func TestNarrowPictureStillAWork(t *testing.T) {
	dir, fetchFeed := galleryOf(t)
	get, _ := served(t, map[string]*fetch.Response{"https://cdn.example/narrow.jpg": picture("image/jpeg", jpegOf(400, 300))})
	s := service(t, Options{Resources: dir, Cache: filepath.Join(dir, "cache.json"), FetchFeed: fetchFeed, Fetch: get})
	ctx := context.Background()
	if behind := s.picture(ctx, "https://cdn.example/narrow.jpg", 600); behind != "" {
		t.Fatal("a narrow picture stood behind a writer")
	}
	if work := s.Picture(ctx, "https://cdn.example/narrow.jpg"); work == "" {
		t.Fatal("the narrow picture was refused as a work too")
	}
}

// A read behind the list holds what a feed adds until its pictures are in, so a new work comes onto the page with its
// picture; the page's first fill fetches none ahead.
func TestNewWorksComeWithPictures(t *testing.T) {
	dir, _ := galleryOf(t)
	works := []string{"https://cdn.example/old.jpg"}
	fetchFeed := feedsOf(t, func(url string) string {
		if strings.Contains(url, "writer") {
			return `<rss><channel><title>Writer</title><link>https://writer.example/</link></channel></rss>`
		}
		var items strings.Builder
		for i, src := range works {
			items.WriteString(`<item><title>W` + src + `</title><link>` + src + `.html</link><pubDate>` + daysAgo(i+1) +
				`</pubDate><description><![CDATA[<p><img src="` + src + `"></p>]]></description></item>`)
		}
		return `<rss><channel><title>G</title><link>https://g.example/</link>` + items.String() + `</channel></rss>`
	})
	base, calls := served(t, map[string]*fetch.Response{
		"https://cdn.example/old.jpg": picture("image/jpeg", jpegOf(1600, 1000)),
		"https://cdn.example/new.jpg": picture("image/jpeg", jpegOf(1600, 1000)),
	})
	var s *Service
	var shownEarly atomic.Bool
	get := func(ctx context.Context, url string, scope func(string) bool, max int) (*fetch.Response, error) {
		if s != nil && url == "https://cdn.example/new.jpg" {
			if src, _ := s.workImage("gallery", 0); src == url {
				shownEarly.Store(true)
			}
		}
		return base(ctx, url, scope, max)
	}
	o := Options{Resources: dir, Cache: filepath.Join(dir, "cache.json"), FetchFeed: fetchFeed, Fetch: get}
	first := service(t, o)
	list(t, first)
	first.bg.Wait()
	if calls.Load() != 0 {
		t.Fatalf("the first fill fetched %d pictures ahead", calls.Load())
	}
	// The next launch shows the kept list and reads the feed behind it; the feed now has a new work.
	works = []string{"https://cdn.example/new.jpg", "https://cdn.example/old.jpg"}
	s = service(t, o)
	if src, _ := s.workImage("gallery", 0); src != "https://cdn.example/old.jpg" {
		t.Fatal("the kept list first:", src)
	}
	list(t, s)
	s.bg.Wait()
	if src, _ := s.workImage("gallery", 0); src != "https://cdn.example/new.jpg" {
		t.Fatal("the new work is shown once read:", src)
	}
	if shownEarly.Load() || calls.Load() != 1 {
		t.Fatalf("the new work's picture comes before the work: shown early %v, %d fetches", shownEarly.Load(), calls.Load())
	}
	if pic, _ := s.Work(context.Background(), "gallery", 0); pic == "" || calls.Load() != 1 {
		t.Fatal("the page finds the picture already in")
	}
}

// An AVIF picture, which the native window cannot decode, goes as macOS's sips converts it: JPEG, PNG when it has
// transparency; one sips cannot read is none. One kept from before is converted as it is read.
func TestWorkAVIF(t *testing.T) {
	if _, err := exec.LookPath("sips"); err != nil {
		t.Skip("sips is macOS's")
	}
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join("..", "reading", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	opaque, alpha := read("opaque.avif"), read("alpha.avif")
	dir, fetchFeed := galleryOf(t, "https://cdn.example/o.avif", "https://cdn.example/a.avif", "https://cdn.example/broken.avif")
	get, _ := served(t, map[string]*fetch.Response{
		"https://cdn.example/o.avif":      picture("image/avif", opaque),
		"https://cdn.example/a.avif":      picture("image/avif", alpha),
		"https://cdn.example/broken.avif": picture("image/avif", opaque[:32]),
	})
	s := service(t, Options{Resources: dir, Cache: filepath.Join(dir, "cache.json"), FetchFeed: fetchFeed, Fetch: get})
	list(t, s)
	ctx := context.Background()
	photo, _ := s.Work(ctx, "gallery", 0)
	drawing, _ := s.Work(ctx, "gallery", 1)
	broken, _ := s.Work(ctx, "gallery", 2)
	if mime, cfg := decoded(t, photo); mime != "image/jpeg" || cfg.Width != 64 || cfg.Height != 48 {
		t.Fatalf("opaque: %s %+v", mime, cfg)
	}
	if mime, cfg := decoded(t, drawing); mime != "image/png" || cfg.Width != 40 || cfg.Height != 30 {
		t.Fatalf("transparent: %s %+v", mime, cfg)
	}
	if broken != "" {
		t.Fatalf("one sips cannot read is none: %.40s", broken)
	}
	// Kept as it came by an older version: converted as it is read, and kept so.
	sum := sha1.Sum([]byte("https://cdn.example/old.avif"))
	file := filepath.Join(dir, "explore-works", hex.EncodeToString(sum[:])+"-1200.jpg")
	if err := os.WriteFile(file, opaque, 0o644); err != nil {
		t.Fatal(err)
	}
	if mime, _ := decoded(t, s.fetchPicture(file, "https://cdn.example/old.avif", 0)); mime != "image/jpeg" {
		t.Fatalf("a kept AVIF: %s", mime)
	}
	if kept, _ := os.ReadFile(file); !bytes.HasPrefix(kept, []byte{0xff, 0xd8}) {
		t.Fatal("the converted copy is kept")
	}
}

// Shot: a writer stands on the first of their latest post's pictures wide enough to fill a card; a design site on its
// first screen, taken now when there is none; SourceShot is a source's own site's.
func TestShotStage(t *testing.T) {
	dir, fetchFeed := galleryOf(t)
	get, _ := served(t, map[string]*fetch.Response{
		"https://cdn.example/logo.png": picture("image/png", pngOf(200, 200)),
		"https://cdn.example/wide.jpg": picture("image/jpeg", jpegOf(900, 600)),
	})
	g := newGated(t)
	g.dir = filepath.Join(dir, "explore-shots")
	take := g.take
	g.take = func(site, url string) bool {
		if !take(site, url) {
			return false
		}
		write(t, ShotFile(g.dir, site, 1280), jpegOf(1280, 800))
		write(t, ShotFile(g.dir, site, 400), jpegOf(400, 250))
		return true
	}
	changed := make(chan struct{}, 8)
	s := service(t, Options{Resources: dir, Cache: filepath.Join(dir, "cache.json"), FetchFeed: fetchFeed, Fetch: get, Shots: g.Shots, Changed: func() { changed <- struct{}{} },
		Site: func(id string) (*library.Site, error) {
			if id == "feed:1" {
				return &library.Site{Host: "www.Source.example", HomeURL: "https://www.source.example/"}, nil
			}
			return nil, nil
		}})
	list(t, s)
	ctx := context.Background()
	writer, err := s.Shot(ctx, "writer")
	if err != nil {
		t.Fatal(err)
	}
	if mime, cfg := decoded(t, writer); mime != "image/jpeg" || cfg.Width != 900 {
		t.Fatalf("the first picture at least 600 wide: %s %+v", mime, cfg)
	}
	// taken waits for one capture of site, lets it land, and gives what got returns.
	taken := func(site string, got func() (string, error)) string {
		t.Helper()
		done := make(chan string, 1)
		go func() {
			shot, err := got()
			if err != nil {
				t.Error(err)
			}
			done <- shot
		}()
		g.expect(t, []string{site}, 1)
		g.gate(site) <- true
		return <-done
	}
	site := taken("site.example", func() (string, error) { return s.Shot(ctx, "site") })
	if _, cfg := decoded(t, site); cfg.Width != 1280 {
		t.Fatalf("taken now: %+v", cfg)
	}
	if thumb := list(t, s)["site"].Thumb; thumb == nil || !strings.HasPrefix(*thumb, "data:image/jpeg;base64,") {
		t.Fatal("the list carries the small one")
	}
	if again, _ := s.Shot(ctx, "site"); again != site {
		t.Fatal("a fresh capture is shown as it is")
	}
	g.expect(t, nil, 0)
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("a capture that lands tells an open Explore page")
	}
	if none, err := s.Shot(ctx, "nowhere"); none != "" || err != nil {
		t.Fatal("an entry Explore does not know")
	}
	source := taken("source.example", func() (string, error) { return s.SourceShot(ctx, "feed:1") })
	if _, cfg := decoded(t, source); cfg.Width != 1280 {
		t.Fatalf("a source's site, by its host without www.: %+v", cfg)
	}
	if again, err := s.SourceShot(ctx, "feed:1"); again != source || err != nil {
		t.Fatal("one on disk is shown at once")
	}
	if none, err := s.SourceShot(ctx, "feed:2"); none != "" || err != nil {
		t.Fatal("a source with no site")
	}
}

// A design site whose first screen could not be taken stays off the walls, and is not tried again, for a day; one not
// tried yet shows meanwhile. Galleries and writers are not touched.
func TestFailedSiteOffTheWall(t *testing.T) {
	dir, fetchFeed := galleryOf(t)
	g := newGated(t)
	g.dir = filepath.Join(dir, "explore-shots")
	s := service(t, Options{Resources: dir, Cache: filepath.Join(dir, "cache.json"), FetchFeed: fetchFeed, Shots: g.Shots})
	if _, ok := list(t, s)["site"]; !ok {
		t.Fatal("a site not tried yet shows")
	}
	done := make(chan string, 1)
	go func() {
		shot, _ := s.Shot(context.Background(), "site")
		done <- shot
	}()
	g.expect(t, []string{"site.example"}, 1)
	g.gate("site.example") <- false
	if shot := <-done; shot != "" {
		t.Fatal("no capture was taken")
	}
	g.expect(t, nil, 0)
	shown := list(t, s)
	if _, ok := shown["site"]; ok {
		t.Fatal("a site that could not be taken is on the wall")
	}
	if _, ok := shown["gallery"]; !ok || shown["writer"].ID == "" {
		t.Fatal("galleries and writers stay")
	}
	if g.Stale("site.example") {
		t.Fatal("tried again before a day is out")
	}
	defer func(was func() time.Time) { now = was }(now)
	now = func() time.Time { return time.Now().Add(shotRetry + time.Minute) }
	if !g.Stale("site.example") {
		t.Fatal("tried again after a day")
	}
	if _, ok := list(t, s)["site"]; !ok {
		t.Fatal("back on the wall, to be tried again")
	}
}

// Read: a preview is kept for the next look unless it is a summary or unavailable; a source chosen reads afresh; at
// most eight are kept; the links a preview showed may be opened.
func TestReadPreviews(t *testing.T) {
	dir, fetchFeed := galleryOf(t, "https://cdn.example/a.jpg", "https://cdn.example/b.jpg")
	var mu sync.Mutex
	var reads []string
	status := map[string]string{"https://g.example/wb": "summary"}
	s := service(t, Options{Resources: dir, Cache: filepath.Join(dir, "cache.json"), FetchFeed: fetchFeed,
		Preview: func(_ context.Context, target reading.Target, source string) library.ArticleContent {
			mu.Lock()
			reads = append(reads, source+" "+target.URL)
			mu.Unlock()
			return library.ArticleContent{Status: or(status[target.URL], "ready"), Links: []string{target.URL + "#linked"}, SourceURL: target.URL + "?source"}
		}})
	list(t, s)
	ctx := context.Background()
	read := func(url, source string) library.ArticleContent {
		t.Helper()
		c, err := s.Read(ctx, "gallery", url, source)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	read("https://g.example/wa", "")
	read("https://g.example/wa", "")
	read("https://g.example/wb", "")
	read("https://g.example/wb", "")
	read("https://g.example/wa", "page")
	read("https://g.example/wa", "auto")
	mu.Lock()
	got := slices.Clone(reads)
	mu.Unlock()
	if !slices.Equal(got, []string{"auto https://g.example/wa", "auto https://g.example/wb", "auto https://g.example/wb", "page https://g.example/wa", "auto https://g.example/wa"}) {
		t.Fatal(got)
	}
	if _, err := s.Read(ctx, "gallery", "https://g.example/elsewhere", ""); err == nil || err.Error() != "探索里已经没有这篇文章。" {
		t.Fatal("a post Explore does not list:", err)
	}
	links := s.Links()
	if !slices.Contains(links, "https://g.example/wa#linked") || !slices.Contains(links, "https://g.example/wb?source") {
		t.Fatal("the preview's links", links)
	}
	s.pmu.Lock()
	for i := range 9 {
		s.previews = append(s.previews, &preview{key: string(rune('0' + i)), done: make(chan struct{})})
	}
	s.pmu.Unlock()
	read("https://g.example/wa", "feed")
	s.pmu.Lock()
	n, first := len(s.previews), s.previews[0].key
	s.pmu.Unlock()
	if n != 8 || first != "2" {
		t.Fatalf("at most eight are kept, the oldest go: %d from %q", n, first)
	}
}
