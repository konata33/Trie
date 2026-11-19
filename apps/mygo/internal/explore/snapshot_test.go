package explore

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"trie/internal/fetch"
	"trie/internal/library"
)

// The snapshot: what the app pulls before reading the feeds' own sites, and what Publish writes for it.

const hostedBase = "https://snapshot.example/explore/v12/"

var jpegBytes = []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3}

func hostedJSON(t *testing.T, v any) *fetch.Response {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return &fetch.Response{Status: 200, Body: data}
}

// countedFeeds reads each feed as one fresh post, counting the reads by feed.
func countedFeeds(t *testing.T) (func(context.Context, string) (library.ParsedFeed, error), func(string) int) {
	var mu sync.Mutex
	reads := map[string]int{}
	return func(_ context.Context, url string) (library.ParsedFeed, error) {
			mu.Lock()
			reads[url]++
			mu.Unlock()
			return parsed(t, rss([2]string{"read", daysAgo(1)}), url), nil
		}, func(url string) int {
			mu.Lock()
			defer mu.Unlock()
			return reads[url]
		}
}

func TestPullStandsInForTheFeeds(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir,
		catalogEntry{ID: "fresh", Name: "Fresh", Kind: "blog", HomeURL: "https://fresh.example", Host: "fresh.example", FeedURL: ptr("https://fresh.example/feed")},
		catalogEntry{ID: "old", Name: "Old", Kind: "blog", HomeURL: "https://old.example", Host: "old.example", FeedURL: ptr("https://old.example/feed")})
	at := float64(nowMS())
	snapshot := map[string]*stats{
		"https://fresh.example/feed": {At: at, V: version, Posts: []Post{{Title: "From the snapshot", URL: "https://fresh.example/p"}}},
		// The run that made the snapshot could not read this one lately: the app reads it itself.
		"https://old.example/feed": {At: at - fresh - 60_000, V: version, Posts: []Post{{Title: "Old", URL: "https://old.example/p"}}},
	}
	get, _ := served(t, map[string]*fetch.Response{
		hostedBase + "explore-cache.json": hostedJSON(t, snapshot),
		hostedBase + "explore-icons.json": hostedJSON(t, map[string]*string{"writer.example": ptr("data:image/png;base64,AA==")}),
	})
	feeds, reads := countedFeeds(t)
	s := service(t, Options{Resources: dir, Cache: filepath.Join(dir, "cache.json"), FetchFeed: feeds, Fetch: get, Snapshot: hostedBase})
	byID := list(t, s)
	s.bg.Wait()
	check(t, len(byID["fresh"].Posts) == 1 && byID["fresh"].Posts[0].Title == "From the snapshot", "a new library shows the snapshot", byID["fresh"].Posts)
	check(t, reads("https://fresh.example/feed") == 0, "a feed the snapshot holds fresh is not read from its site", reads("https://fresh.example/feed"))
	check(t, reads("https://old.example/feed") == 1, "a feed the snapshot holds stale is", reads("https://old.example/feed"))
	check(t, s.icons["writer.example"] != nil, "the snapshot's icons are taken")
	// Asked again within pullEvery, nothing is pulled or read again.
	list(t, s)
	s.bg.Wait()
	check(t, reads("https://fresh.example/feed") == 0 && reads("https://old.example/feed") == 1, "no second pull or read")

	// With the snapshot out of reach, the launch reads every feed, as with none.
	down, _ := served(t, nil)
	feeds2, reads2 := countedFeeds(t)
	again := service(t, Options{Resources: dir, Cache: filepath.Join(dir, "cache.json"), FetchFeed: feeds2, Fetch: down, Snapshot: hostedBase})
	list(t, again)
	again.bg.Wait()
	check(t, reads2("https://fresh.example/feed") == 1 && reads2("https://old.example/feed") == 1, "an unreachable snapshot leaves it to the sites",
		reads2("https://fresh.example/feed"), reads2("https://old.example/feed"))
}

func TestPulledNewsWaitsForItsPictures(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir, catalogEntry{ID: "gallery", Name: "Gallery", Kind: "site", Section: "设计灵感画廊", HomeURL: "https://gallery.example",
		Host: "gallery.example", FeedURL: ptr("https://gallery.example/feed")})
	cache := filepath.Join(dir, "cache.json")
	old := map[string]*stats{"https://gallery.example/feed": {At: float64(nowMS()) - 2*fresh, V: version, Posts: []Post{}, Works: []work{{URL: "https://gallery.example/w1", Image: "https://img.example/1.jpg"}}}}
	data, _ := json.Marshal(old)
	write(t, cache, data)
	news := map[string]*stats{"https://gallery.example/feed": {At: float64(nowMS()), V: version, Posts: []Post{},
		Works: []work{{URL: "https://gallery.example/w2", Image: "https://img.example/2.jpg"}, {URL: "https://gallery.example/w1", Image: "https://img.example/1.jpg"}}}}
	sum := sha1.Sum([]byte("https://img.example/2.jpg"))
	picture := hostedBase + "explore-works/" + hex.EncodeToString(sum[:]) + "-1200.jpg"
	var gotPicture bool
	var mu sync.Mutex
	pages := map[string]*fetch.Response{hostedBase + "explore-cache.json": hostedJSON(t, news), picture: {Status: 200, Body: jpegBytes}}
	base, _ := served(t, pages)
	get := func(ctx context.Context, url string, scope func(string) bool, max int) (*fetch.Response, error) {
		if url == picture {
			mu.Lock()
			gotPicture = true
			mu.Unlock()
		}
		return base(ctx, url, scope, max)
	}
	s := service(t, Options{Resources: dir, Cache: cache, FetchFeed: hanging(t), Fetch: get, Snapshot: hostedBase})
	list(t, s)
	s.mu.Lock()
	pulling := s.pulling
	s.mu.Unlock()
	if pulling != nil {
		<-pulling
	}
	// The new work lands once its picture is in.
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		works := len(s.stats["https://gallery.example/feed"].Works)
		s.mu.Unlock()
		if works == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	check(t, len(s.stats["https://gallery.example/feed"].Works) == 2 && gotPicture, "the pulled work came with its picture", gotPicture)
	_, err := os.Stat(filepath.Join(dir, "explore-works", hex.EncodeToString(sum[:])+"-1200.jpg"))
	check(t, err == nil, "the snapshot's picture is kept as one fetched from its site", err)
}

func TestPicturesAndFirstScreensComeFromTheSnapshot(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir)
	sum := sha1.Sum([]byte("https://img.example/far.jpg"))
	name := hex.EncodeToString(sum[:]) + "-1200.jpg"
	get, calls := served(t, map[string]*fetch.Response{
		hostedBase + "explore-works/" + name:               {Status: 200, Body: jpegBytes},
		hostedBase + "explore-shots/site.example-400.jpg":  {Status: 200, Body: jpegBytes},
		hostedBase + "explore-shots/site.example-1280.jpg": {Status: 200, Body: jpegBytes},
	})
	g := newGated(t)
	s := service(t, Options{Resources: dir, Cache: filepath.Join(dir, "cache.json"), Fetch: get, Snapshot: hostedBase, Shots: g.Shots})
	check(t, strings.HasPrefix(s.Picture(context.Background(), "https://img.example/far.jpg"), "data:image/jpeg;base64,"), "a picture from the snapshot")
	check(t, calls.Load() == 1, "not from its own site", calls.Load())
	check(t, g.Capture("site.example", []string{"https://site.example/"}, true)(context.Background()), "a first screen from the snapshot")
	check(t, len(g.urls) == 0, "taken with no window", g.urls)
	check(t, !g.Stale("site.example"), "and kept as a capture", g.Stale("site.example"))
	// One the snapshot does not hold is taken in a window.
	go func() { g.gate("other.example") <- true }()
	check(t, g.Capture("other.example", []string{"https://other.example/"}, true)(context.Background()) && len(g.urls) == 1, "a window for the rest", g.urls)
}

func TestPublishWritesTheSnapshot(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir,
		catalogEntry{ID: "writer", Name: "Writer", Kind: "blog", HomeURL: "https://writer.example", Host: "writer.example", FeedURL: ptr("https://writer.example/feed")},
		catalogEntry{ID: "site", Name: "Site", Kind: "site", HomeURL: "https://site.example/", Host: "site.example"})
	feeds, _ := countedFeeds(t)
	g := newGated(t)
	g.take = func(site, _ string) bool {
		write(t, ShotFile(g.dir, site, 400), jpegBytes)
		write(t, ShotFile(g.dir, site, 1280), jpegBytes)
		return true
	}
	cache := filepath.Join(dir, "lib", "explore-cache.json")
	s := service(t, Options{Resources: dir, Cache: cache, FetchFeed: feeds, Shots: g.Shots, Snapshot: hostedBase})
	out := t.TempDir()
	if err := s.Publish(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	at := filepath.Join(out, "explore", "v"+strconv.Itoa(version))
	var got map[string]*stats
	data, err := os.ReadFile(filepath.Join(at, "explore-cache.json"))
	if err == nil {
		err = json.Unmarshal(data, &got)
	}
	check(t, err == nil && got["https://writer.example/feed"] != nil && got["https://writer.example/feed"].V == version, "the stats", err)
	_, err = os.Stat(filepath.Join(at, "explore-shots", "site.example-1280.jpg"))
	check(t, err == nil, "a design site's first screen", err)
	_, err = os.Stat(filepath.Join(at, "explore-icons.json"))
	check(t, err == nil, "the icons", err)

	// A run that reads too few feeds writes nothing.
	failing := service(t, Options{Resources: dir, Cache: filepath.Join(t.TempDir(), "explore-cache.json"), FetchFeed: func(context.Context, string) (library.ParsedFeed, error) {
		return library.ParsedFeed{}, context.DeadlineExceeded
	}})
	empty := t.TempDir()
	check(t, failing.Publish(context.Background(), empty) != nil, "a bad run fails")
	_, err = os.Stat(filepath.Join(empty, "explore"))
	check(t, os.IsNotExist(err), "and writes nothing", err)
}
