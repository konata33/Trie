package explore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/reading"
	"trie/internal/subscription"
	"trie/internal/web"
)

// polite is a fetch that keeps to three requests at once, one at a time to
// a site, and a second between two to the same site.
type polite struct {
	sem   chan struct{}
	mu    sync.Mutex
	sites map[string]*politeSite
}

type politeSite struct {
	mu   sync.Mutex
	last time.Time
}

func newPolite() *polite {
	return &polite{sem: make(chan struct{}, 3), sites: map[string]*politeSite{}}
}

func (p *polite) wrap(get fetch.Fetch) fetch.Fetch {
	return func(ctx context.Context, url string, scope func(string) bool, maxBytes int) (*fetch.Response, error) {
		host := url
		if u, err := web.ParseURL(url, nil); err == nil {
			host = u.Hostname()
		}
		p.mu.Lock()
		site := p.sites[host]
		if site == nil {
			site = &politeSite{}
			p.sites[host] = site
		}
		p.mu.Unlock()
		site.mu.Lock()
		defer site.mu.Unlock()
		time.Sleep(time.Until(site.last.Add(1100 * time.Millisecond)))
		p.sem <- struct{}{}
		defer func() { <-p.sem }()
		site.last = time.Now()
		res, err := get(ctx, url, scope, maxBytes)
		site.last = time.Now()
		return res, err
	}
}

var errOffline = errors.New("offline")

// TestWarm fills a data folder with what Explore shows, read politely from
// the network, for screenshots that need real pictures: the feeds, the
// recommended writers' icons, the galleries' works and the writers' latest
// pictures (no first screens: TestLiveSites takes those).
//
//	EXPLORE_WARM=/tmp/dir go test -run TestWarm -count=1 -timeout 30m ./internal/explore
func TestWarm(t *testing.T) {
	dir := os.Getenv("EXPLORE_WARM")
	if dir == "" {
		t.Skip("EXPLORE_WARM is not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(dir, "library.sqlite")
	if _, err := os.Stat(lib); err != nil {
		src, err := os.Open(filepath.Join("..", "..", "..", "..", ".context", "mygo", "library", "library.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		dst, err := os.Create(lib)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(dst, src); err != nil {
			t.Fatal(err)
		}
		src.Close()
		dst.Close()
	}
	store, err := library.Open(lib, library.Snapshot{Version: 1, AsOf: time.Now().Format("2006-01-02")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p := newPolite()
	// Each picture, each feed and each icon has a budget of its own, as in the app.
	fresh := func(n int) fetch.Fetch {
		return func(ctx context.Context, url string, scope func(string) bool, maxBytes int) (*fetch.Response, error) {
			return p.wrap(fetch.New(n).Fetch)(ctx, url, scope, maxBytes)
		}
	}
	subs := subscription.New(store, func() fetch.Fetch { return p.wrap(fetch.New(8).Fetch) }, nil, nil)
	reader := reading.New(store, func() fetch.Fetch { return p.wrap(fetch.New(160).Fetch) }, reading.Worker)
	s := New(Options{Resources: filepath.Join("..", "..", "resources", "explore"), Cache: filepath.Join(dir, "explore-cache.json"),
		FetchFeed: subs.FetchFeed,
		Sources: func() []library.Source {
			st, err := store.Library()
			if err != nil {
				return nil
			}
			return st.Sources
		},
		Upstreams: Upstreams,
		FindIcon:  func(ctx context.Context, home string) (*string, error) { return findIcon(ctx, fresh(12), home) },
		ReadPage: func(ctx context.Context, url, homeURL string) string {
			c := reader.Preview(ctx, reading.Target{ID: "explore:" + url, URL: url, Aliases: []string{}, HomeURL: homeURL, TrustedArticle: true}, "auto", 0)
			if c.Status == "ready" && c.TextLength > thinText {
				return c.HTML
			}
			return ""
		},
		Fetch: fresh(6)})
	// Pictures only (EXPLORE_WARM_PICTURES): the feeds are read already, and not again.
	if os.Getenv("EXPLORE_WARM_PICTURES") != "" {
		s.o.FetchFeed = func(context.Context, string) (library.ParsedFeed, error) { return library.ParsedFeed{}, errOffline }
		s.o.FindIcon, s.o.ReadPage, s.o.Upstreams = nil, nil, nil
	}
	ctx := context.Background()
	start := time.Now()
	entries, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.bg.Wait()
	entries, err = s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.bg.Wait()
	t.Logf("list: %d entries in %s", len(entries), time.Since(start).Round(time.Second))
	var wg sync.WaitGroup
	works, shots := 0, 0
	for _, e := range entries {
		switch {
		case len(e.Works) > 0:
			for i := range min(8, len(e.Works)) {
				works++
				wg.Go(func() { s.Work(ctx, e.ID, i) })
			}
		case e.Kind == "blog":
			shots++
			wg.Go(func() { s.Shot(ctx, e.ID) })
		}
	}
	wg.Wait()
	s.bg.Wait()
	t.Logf("%d works, %d writers' pictures, in %s", works, shots, time.Since(start).Round(time.Second))
}
