package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"trie/internal/fetch"
	"trie/internal/library"
)

// The service parts of subscription-service.test.ts; what the store does
// with an imported feed is in library/feed_test.go. Feeds are parsed by the
// real parser, as the TS tests parse them.

var empty = library.Snapshot{Version: 1, AsOf: "2026-11-01", Sources: []library.SourceSnapshot{}, Articles: []library.ArticleSnapshot{}, ReviewItems: []library.ReviewItem{}}

func feedXML(home, body string, slugs ...string) string {
	var items strings.Builder
	for _, s := range slugs {
		items.WriteString(`<item><title>` + s + `</title><link>https://writer.example/` + s + `/</link><content:encoded><![CDATA[<p>` + body + `</p>]]></content:encoded></item>`)
	}
	return `<rss xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Real Writer</title><link>` + home + `</link>` + items.String() + `</channel></rss>`
}

func serve(xml func(url string) string) func() fetch.Fetch {
	return func() fetch.Fetch {
		return func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
			return &fetch.Response{URL: url, Status: 200, Type: "application/rss+xml", Body: []byte(xml(url))}, nil
		}
	}
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

func state(t *testing.T, s *library.Store) library.LibraryState {
	t.Helper()
	return must[library.LibraryState](t)(s.Library())
}

func preview(t *testing.T, s *Service, input string) Preview {
	t.Helper()
	return must[Preview](t)(s.Preview(context.Background(), input))
}

func add(t *testing.T, s *Service, input string) library.SubscriptionResult {
	t.Helper()
	return must[library.SubscriptionResult](t)(s.Add(preview(t, s, input).Token, true))
}

// empty library -> typed feed URL -> preview -> persisted articles -> real source body, no sample whitelist
// (the read of that body through the reader waits for reader-service; here the feed body is checked in the store)
func TestPreviewAddPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	store := open(t, path, empty)
	s := New(store, serve(func(string) string { return feedXML("https://writer.example/", "Short real body", "one", "two") }), nil, nil)
	p := preview(t, s, "https://writer.example/feed")
	if p.ArticleCount != 2 || len(state(t, store).Articles) != 0 || !slices.Equal(p.SampleTitles, []string{"one", "two"}) || p.ExistingSourceID != nil {
		t.Fatalf("preview %+v", p)
	}
	added := must[library.SubscriptionResult](t)(s.Add(p.Token, true))
	if added.ImportedCount != 2 || len(added.Library.Sources) != 1 {
		t.Fatalf("added %+v", added)
	}
	id := added.Library.Articles[0].ID
	if body := must[*library.FeedBody](t)(store.FeedContent(id)); body == nil || !strings.Contains(body.HTML, "Short real body") {
		t.Fatal(body)
	}
	must[library.LibraryState](t)(store.Mutate(library.ToggleFavorite{ID: id}))
	must[library.LibraryState](t)(store.Mutate(library.RenameSource{ID: added.SourceID, Name: "My writer"}))
	store.Close()
	reopened := state(t, open(t, path, empty))
	if len(reopened.Articles) != 2 || !reopened.Articles[0].Favorite || reopened.Sources[0].DisplayName != "My writer" || reopened.Sources[0].FeedURL == nil {
		t.Fatalf("after reopening %+v", reopened)
	}
	if _, err := s.Add(p.Token, true); err == nil {
		t.Fatal("a used preview was accepted again")
	}
}

// refresh merges new entries without duplicating or removing aged-out entries, preserves user state
func TestRefreshMerges(t *testing.T) {
	store := open(t, ":memory:", empty)
	var mu sync.Mutex
	slugs := []string{"one", "two"}
	s := New(store, serve(func(string) string {
		mu.Lock()
		defer mu.Unlock()
		return feedXML("https://writer.example/", "Short real body", slugs...)
	}), nil, nil)
	added := add(t, s, "https://writer.example/feed")
	id := added.Library.Articles[0].ID
	must[library.LibraryState](t)(store.Mutate(library.MarkRead{ID: id, Read: true}))
	must[library.LibraryState](t)(store.Mutate(library.ToggleFavorite{ID: id}))
	same := must[RefreshResult](t)(s.Refresh(context.Background(), "", nil))
	if same.ImportedCount != 0 || same.UpdatedCount != 0 {
		t.Fatalf("unchanged refresh %+v", same)
	}
	mu.Lock()
	slugs = []string{"two", "three"}
	mu.Unlock()
	refreshed := must[RefreshResult](t)(s.Refresh(context.Background(), "", nil))
	if refreshed.ImportedCount != 1 || len(refreshed.Library.Articles) != 3 {
		t.Fatalf("refresh %+v", refreshed)
	}
	for _, a := range refreshed.Library.Articles {
		if a.ID == id && !a.Favorite {
			t.Fatal("refresh dropped the favourite")
		}
	}
	again := add(t, s, "https://writer.example/feed")
	if len(again.Library.Sources) != 1 || len(again.Library.Articles) != 3 || again.ImportedCount != 0 {
		t.Fatalf("added again %+v", again)
	}
}

// feed updates do not get overwritten by bundled sample metadata after restart
// (the subscription part: a feed on the home of a source without a feed is that source)
func TestSubscribeKnownSource(t *testing.T) {
	data := must[[]byte](t)(os.ReadFile("../library/testdata/verified-snapshot.json"))
	var fixture library.Snapshot
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "db.sqlite")
	store := open(t, path, fixture)
	s := New(store, serve(func(string) string { return feedXML("https://antfu.me/", "Short real body", "new") }), nil, nil)
	p := preview(t, s, "https://antfu.me/feed.xml")
	if p.ExistingSourceID == nil || *p.ExistingSourceID != "antfu" {
		t.Fatalf("existing %v", p.ExistingSourceID)
	}
	result := must[library.SubscriptionResult](t)(s.Add(p.Token, true))
	title := func(st library.LibraryState) (string, *string) {
		for _, src := range st.Sources {
			if src.ID == "antfu" {
				return src.Title, src.FeedURL
			}
		}
		return "", nil
	}
	if result.SourceID != "antfu" {
		t.Fatal(result.SourceID)
	}
	if got, _ := title(result.Library); got != "Real Writer" {
		t.Fatal(got)
	}
	store.Close()
	if got, feedURL := title(state(t, open(t, path, fixture))); got != "Real Writer" || feedURL == nil {
		t.Fatalf("after restart %q %v", got, feedURL)
	}
}

// same article in two subscriptions shares read/favorite state and retains both memberships
func TestTwoSubscriptionsOneArticle(t *testing.T) {
	store := open(t, ":memory:", empty)
	s := New(store, serve(func(string) string { return feedXML("https://writer.example/", "Short real body", "one") }), nil, nil)
	first := add(t, s, "https://writer.example/feed")
	second := add(t, s, "https://writer.example/other-feed")
	if len(second.Library.Sources) != 2 || len(second.Library.Articles) != 1 || first.SourceID == second.SourceID {
		t.Fatalf("%+v", second)
	}
	members := slices.Clone(second.Library.Articles[0].SourceIDs)
	slices.Sort(members)
	want := []string{first.SourceID, second.SourceID}
	slices.Sort(want)
	if !slices.Equal(members, want) {
		t.Fatal(members)
	}
	must[library.LibraryState](t)(store.Mutate(library.ToggleFavorite{ID: second.Library.Articles[0].ID}))
	st := must[library.LibraryState](t)(store.Mutate(library.SetSubscription{ID: first.SourceID, Subscribed: false}))
	for _, src := range st.Sources {
		if src.ID == second.SourceID && !src.Subscribed {
			t.Fatal("the other subscription was dropped")
		}
	}
	if !st.Articles[0].Favorite {
		t.Fatal("favourite lost")
	}
}

// failed previews do not write, private inputs are blocked, failed refresh retains articles and records error
func TestFailures(t *testing.T) {
	store := open(t, ":memory:", empty)
	var failure bool
	calls := 0
	s := New(store, func() fetch.Fetch {
		return func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
			calls++
			if failure {
				return nil, errors.New("offline")
			}
			return &fetch.Response{URL: url, Status: 200, Type: "application/rss+xml", Body: []byte(feedXML("https://writer.example/", "Short real body", "one", "two"))}, nil
		}
	}, nil, nil)
	for _, input := range []string{"http://127.0.0.1/feed", "localhost/feed", "", "   ", "file:///etc/passwd", "https://" + strings.Repeat("a", 4096)} {
		if _, err := s.Preview(context.Background(), input); err == nil {
			t.Fatalf("%q previewed", input)
		}
	}
	if calls != 0 || len(state(t, store).Sources) != 0 {
		t.Fatal("a refused preview read or wrote")
	}
	result := add(t, s, "https://writer.example/feed")
	failure = true
	var events []Progress
	refreshed := must[RefreshResult](t)(s.Refresh(context.Background(), "", func(p Progress) { events = append(events, p) }))
	if len(refreshed.Failures) != 1 || len(refreshed.Library.Articles) != 2 || refreshed.Library.Sources[0].LastError == nil {
		t.Fatalf("refresh %+v", refreshed)
	}
	if len(events) != 2 || events[0].State != "start" || events[1].State != "failed" || events[1].Finished != 1 || events[1].Total != 1 || events[1].Message == "" {
		t.Fatalf("events %+v", events)
	}
	if _, err := s.Add("not-a-preview", true); err == nil {
		t.Fatal("an unknown preview was accepted")
	}
	must[library.LibraryState](t)(store.Mutate(library.SetSubscription{ID: result.SourceID, Subscribed: false}))
	if _, err := s.Refresh(context.Background(), "", nil); err == nil {
		t.Fatal("refresh without subscriptions")
	}
	if state(t, store).Sources[0].Subscribed {
		t.Fatal("refresh resubscribed")
	}
}

// homepage feed discovery and empty valid feeds are usable
func TestHomepageDiscovery(t *testing.T) {
	store := open(t, ":memory:", empty)
	s := New(store, func() fetch.Fetch {
		return func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
			if strings.HasSuffix(url, "/feed") {
				return &fetch.Response{URL: url, Status: 200, Type: "application/rss+xml", Body: []byte(feedXML("https://writer.example/", ""))}, nil
			}
			return &fetch.Response{URL: url, Status: 200, Type: "text/html", Body: []byte(`<html><head><link rel="alternate" type="application/rss+xml" href="/feed"></head></html>`)}, nil
		}
	}, nil, nil)
	p := preview(t, s, "writer.example")
	if p.FeedURL != "https://writer.example/feed" || p.ArticleCount != 0 {
		t.Fatalf("%+v", p)
	}
	if len(must[library.SubscriptionResult](t)(s.Add(p.Token, true)).Library.Sources) != 1 {
		t.Fatal("not added")
	}
}

// a different feed cannot replace existing article metadata or body by claiming the same link
func TestOtherFeedSameLink(t *testing.T) {
	store := open(t, ":memory:", empty)
	s := New(store, serve(func(url string) string {
		if strings.Contains(url, "evil") {
			return feedXML("https://writer.example/", "Fake replacement", "one")
		}
		return feedXML("https://writer.example/", "Original body", "one")
	}), nil, nil)
	add(t, s, "https://writer.example/feed")
	previous := must[*library.FeedBody](t)(store.FeedContent("https://writer.example/one"))
	add(t, s, "https://evil.example/feed")
	st := state(t, store)
	if len(st.Articles) != 1 || len(st.Articles[0].SourceIDs) != 2 {
		t.Fatalf("%+v", st.Articles)
	}
	if now := must[*library.FeedBody](t)(store.FeedContent("https://writer.example/one")); now.HTML != previous.HTML || now.Version != previous.Version {
		t.Fatal("the body was replaced")
	}
}

// unsubscribing during a refresh never resurrects the source
func TestUnsubscribeDuringRefresh(t *testing.T) {
	store := open(t, ":memory:", empty)
	var delayed bool
	reading, release := make(chan struct{}), make(chan struct{})
	s := New(store, func() fetch.Fetch {
		return func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
			if delayed {
				close(reading)
				<-release
			}
			return &fetch.Response{URL: url, Status: 200, Type: "application/rss+xml", Body: []byte(feedXML("https://writer.example/", "b", "one", "two"))}, nil
		}
	}, nil, nil)
	added := add(t, s, "https://writer.example/feed")
	delayed = true
	done := make(chan error)
	go func() { _, err := s.Refresh(context.Background(), added.SourceID, nil); done <- err }()
	<-reading
	must[library.LibraryState](t)(store.Mutate(library.SetSubscription{ID: added.SourceID, Subscribed: false}))
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if state(t, store).Sources[0].Subscribed {
		t.Fatal("the refresh resubscribed the source")
	}
}

// a transient connection reset retries once without writing during preview
func TestTransientReset(t *testing.T) {
	store := open(t, ":memory:", empty)
	calls := 0
	reply := func(fail ...error) func() fetch.Fetch {
		return func() fetch.Fetch {
			return func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
				calls++
				if calls <= len(fail) {
					return nil, fail[calls-1]
				}
				return &fetch.Response{URL: url, Status: 200, Type: "application/rss+xml", Body: []byte(feedXML("https://writer.example/", "b", "one", "two"))}, nil
			}
		}
	}
	reset := &fetch.NetError{Err: errors.New("reset"), Transient: true}
	s := New(store, reply(reset), nil, nil)
	if p := preview(t, s, "https://writer.example/feed"); p.ArticleCount != 2 || calls != 2 || len(state(t, store).Articles) != 0 {
		t.Fatalf("%+v after %d reads", p, calls)
	}
	// Beyond the TS case: a second network failure reads as a dropped connection, and other failures are not retried.
	calls = 0
	s = New(store, reply(reset, &fetch.NetError{Err: errors.New("read: connection reset by peer")}), nil, nil)
	if _, err := s.Preview(context.Background(), "https://writer.example/feed"); err == nil || err.Error() != "连接被中断，请稍后重试" || calls != 2 {
		t.Fatalf("%v after %d reads", err, calls)
	}
	calls = 0
	s = New(store, reply(&fetch.NetError{Err: errors.New("x509: certificate expired")}), nil, nil)
	if _, err := s.Preview(context.Background(), "https://writer.example/feed"); err == nil || calls != 1 {
		t.Fatalf("%v after %d reads", err, calls)
	}
}

// a slow feed does not hold up the feeds after it
func TestSlowFeed(t *testing.T) {
	store := open(t, ":memory:", empty)
	var hold bool
	release := make(chan struct{})
	s := New(store, func() fetch.Fetch {
		return func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
			slow := strings.HasPrefix(url, "https://slow.example")
			if hold && slow {
				<-release
			}
			home, slug := "https://fast.example/", "fast"
			if slow {
				home, slug = "https://slow.example/", "slow"
			}
			return &fetch.Response{URL: url, Status: 200, Type: "application/rss+xml", Body: []byte(feedXML(home, "b", slug))}, nil
		}
	}, nil, nil)
	add(t, s, "https://slow.example/feed")
	fast := add(t, s, "https://fast.example/feed")
	hold = true
	var mu sync.Mutex
	var done []string
	finished := make(chan error)
	go func() {
		_, err := s.Refresh(context.Background(), "", func(p Progress) {
			if p.State == "done" {
				mu.Lock()
				done = append(done, p.SourceID)
				mu.Unlock()
			}
		})
		finished <- err
	}()
	for deadline := time.Now().Add(time.Second); ; time.Sleep(2 * time.Millisecond) {
		mu.Lock()
		n := len(done)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
	}
	mu.Lock()
	if !slices.Equal(done, []string{fast.SourceID}) {
		t.Fatalf("done %v", done)
	}
	mu.Unlock()
	close(release)
	if err := <-finished; err != nil || len(done) != 2 {
		t.Fatal(err, done)
	}
}

// Beyond the TS cases: a refresh of a source already being read waits for
// that read; a preview keeps at most five feeds.
func TestSharedRefreshAndPreviewLimit(t *testing.T) {
	store := open(t, ":memory:", empty)
	var reads, mu = 0, sync.Mutex{}
	gate := make(chan struct{})
	var gated bool
	s := New(store, func() fetch.Fetch {
		return func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
			mu.Lock()
			reads++
			wait := gated
			mu.Unlock()
			if wait {
				<-gate
			}
			return &fetch.Response{URL: url, Status: 200, Type: "application/rss+xml", Body: []byte(feedXML("https://writer.example/", "b", "one"))}, nil
		}
	}, nil, nil)
	added := add(t, s, "https://writer.example/feed")
	mu.Lock()
	gated, reads = true, 0
	mu.Unlock()
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := s.Refresh(context.Background(), added.SourceID, nil); results <- err }()
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if reads != 1 {
		t.Fatalf("%d reads for two refreshes of one source", reads)
	}
	mu.Lock()
	gated = false
	mu.Unlock()
	var tokens []string
	for range 6 {
		tokens = append(tokens, preview(t, s, "https://writer.example/feed").Token)
	}
	if _, err := s.Add(tokens[0], true); err == nil {
		t.Fatal("the oldest of six previews was kept")
	}
	must[library.SubscriptionResult](t)(s.Add(tokens[5], true))
}
