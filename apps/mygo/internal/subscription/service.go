// Package subscription reads a typed address into a feed to preview, adds it
// to the library, and refreshes subscribed feeds a few at a time.
package subscription

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"trie/internal/feed"
	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/web"
)

// feedsAtOnce is how many feeds a refresh reads at once.
const feedsAtOnce = 4

// Preview is SubscriptionPreview.
type Preview struct {
	Token            string   `json:"token"`
	Title            string   `json:"title"`
	HomeURL          string   `json:"homeURL"`
	FeedURL          string   `json:"feedURL"`
	ArticleCount     int      `json:"articleCount"`
	ExistingSourceID *string  `json:"existingSourceID"`
	SampleTitles     []string `json:"sampleTitles"`
	Warnings         []string `json:"warnings"`
}

// Progress is RefreshProgress: State is start, done or failed.
type Progress struct {
	SourceID      string `json:"sourceID"`
	State         string `json:"state"`
	ImportedCount *int   `json:"importedCount,omitempty"`
	Message       string `json:"message,omitempty"`
	Finished      int    `json:"finished"`
	Total         int    `json:"total"`
}

// RefreshResult is RefreshResult.
type RefreshResult struct {
	Library       library.LibraryState `json:"library"`
	ImportedCount int                  `json:"importedCount"`
	UpdatedCount  int                  `json:"updatedCount"`
	Failures      []Failure            `json:"failures"`
}

type Failure struct {
	SourceID string `json:"sourceID"`
	Message  string `json:"message"`
}

type pending struct {
	token string
	feed  library.ParsedFeed
	at    time.Time
}

type call struct {
	done   chan struct{}
	result library.SubscriptionResult
	err    error
}

// Service previews, adds and refreshes subscriptions.
type Service struct {
	store   *library.Store
	fetcher func() fetch.Fetch
	iconFor func(homeURL string) *string
	nameFor func(feedURL, homeURL string) string

	mu         sync.Mutex
	previews   []pending // oldest first
	refreshing map[string]*call
}

// New makes a Service on store. A nil fetcher reads with fetch.New(8), a
// fresh budget for every feed; nil iconFor and nameFor find nothing.
func New(store *library.Store, fetcher func() fetch.Fetch, iconFor func(homeURL string) *string, nameFor func(feedURL, homeURL string) string) *Service {
	if fetcher == nil {
		fetcher = func() fetch.Fetch { return fetch.New(8).Fetch }
	}
	if iconFor == nil {
		iconFor = func(string) *string { return nil }
	}
	if nameFor == nil {
		nameFor = func(string, string) string { return "" }
	}
	return &Service{store: store, fetcher: fetcher, iconFor: iconFor, nameFor: nameFor, refreshing: map[string]*call{}}
}

func anywhere(string) bool { return true }

var (
	feedRoot  = regexp.MustCompile(`^[` + web.Space + `]*(?:<\?xml[^>]*>[` + web.Space + `]*)?<(?:[\w-]+:)?(?:rss|feed|rdf)\b`)
	htmlStart = regexp.MustCompile(`^[` + web.Space + `]*<!doctype[` + web.Space + `]+html|^[` + web.Space + `]*<html[` + web.Space + `>]`)
	xmlCode   = regexp.MustCompile(`<\?xml[^>]*encoding=["']([\w-]+)`)
)

// lower is toLowerCase for ASCII only, so a lowercase pattern matches as JavaScript's /i does.
func lower(s string) string { return strings.Map(web.LowerASCII, s) }

// decode reads body with the charset the Content-Type names, else the XML
// declaration's encoding, else UTF-8.
func decode(body []byte, contentType string) string {
	label := fetch.Charset(contentType)
	if label == "" {
		// body.subarray(0, 200).toString(): the cut may split a character, which the pattern never needs.
		if m := xmlCode.FindStringSubmatch(lower(string(body[:min(len(body), 200)]))); m != nil {
			label = m[1]
		}
	}
	return fetch.Decode(body, label)
}

// FetchFeed is fetchFeed: the feed at input, or the feed a home page there declares.
func (s *Service) FetchFeed(ctx context.Context, input string) (library.ParsedFeed, error) {
	get := s.fetcher()
	request := func(url string) (*fetch.Response, error) {
		res, err := get(ctx, url, anywhere, fetch.MaxBytes)
		var ne *fetch.NetError
		if err == nil || !errors.As(err, &ne) || !ne.Transient {
			return res, err
		}
		select {
		case <-time.After(400 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// Raw socket errors are English and technical; the reader only needs to know it was the connection.
		res, err = get(ctx, url, anywhere, fetch.MaxBytes)
		if errors.As(err, &ne) {
			return nil, errors.New("连接被中断，请稍后重试")
		}
		return res, err
	}
	first, err := request(input)
	if err != nil {
		return library.ParsedFeed{}, err
	}
	if first.Status < 200 || first.Status >= 300 {
		return library.ParsedFeed{}, fmt.Errorf("订阅地址暂时无法读取（HTTP %d），已有文章未受影响", first.Status)
	}
	text := decode(first.Body, first.Type)
	if low := lower(text); feedRoot.MatchString(low) || !strings.Contains(lower(first.Type), "html") && !htmlStart.MatchString(low) {
		return feed.Parse(text, first.URL)
	}
	candidates, err := feed.DiscoverFeeds(text, first.URL)
	if err != nil {
		return library.ParsedFeed{}, err
	}
	if len(candidates) == 0 {
		return library.ParsedFeed{}, errors.New("这个网页没有提供可识别的订阅入口，请填写完整的 RSS/Atom 地址")
	}
	for _, url := range candidates {
		// A failure tries the next explicitly declared feed.
		if res, err := request(url); err == nil && res.Status == 200 {
			if parsed, err := feed.Parse(decode(res.Body, res.Type), res.URL); err == nil {
				return parsed, nil
			}
		}
	}
	return library.ParsedFeed{}, errors.New("发现了订阅入口，但暂时无法读取，请稍后重试或直接填写订阅源地址")
}

// key is the feed address as the library matches it.
func key(value string) (string, error) { return feed.CanonicalFeedURL(value) }

func homeKey(value string) (string, error) {
	k, err := key(value)
	if err != nil {
		return "", err
	}
	u, err := web.ParseURL(k, nil)
	if err != nil {
		return "", err
	}
	return u.Hostname(), nil
}

// existing is the source the feed already is: the same feed address, or a
// source without a feed on the feed's own host. The feed's own addresses
// are public, so only a stored address can fail here.
func (s *Service) existing(f library.ParsedFeed) (*string, error) {
	state, err := s.store.Library()
	if err != nil {
		return nil, err
	}
	feedKey, _ := key(f.FeedURL)
	feedHome, _ := homeKey(f.FeedURL)
	home, _ := homeKey(f.HomeURL)
	for _, src := range state.Sources {
		if src.FeedURL != nil && *src.FeedURL != "" {
			k, err := key(*src.FeedURL)
			if err != nil {
				return nil, err
			}
			if k == feedKey {
				return &src.ID, nil
			}
		}
	}
	for _, src := range state.Sources {
		if src.FeedURL == nil || *src.FeedURL == "" {
			k, err := homeKey(src.HomeURL)
			if err != nil {
				return nil, err
			}
			if k == home && feedHome == home {
				return &src.ID, nil
			}
		}
	}
	return nil, nil
}

var scheme = regexp.MustCompile(`^[a-z][a-z\d+.-]*:`)

// Preview is preview: the feed input names, kept for ten minutes for Add.
func (s *Service) Preview(ctx context.Context, input string) (Preview, error) {
	input = web.JSTrim(input)
	if input == "" || web.JSLen(input) > 4096 {
		return Preview{}, errors.New("请填写有效的 RSS/Atom 或博客首页地址")
	}
	if !scheme.MatchString(lower(input)) {
		input = "https://" + input
	}
	u, err := fetch.PublicURL(input)
	if err != nil {
		return Preview{}, err
	}
	f, err := s.FetchFeed(ctx, u.Href())
	if err != nil {
		return Preview{}, err
	}
	token := rand.Text()
	s.mu.Lock()
	kept := s.previews[:0]
	for _, p := range s.previews {
		if time.Since(p.at) <= 10*time.Minute {
			kept = append(kept, p)
		}
	}
	s.previews = append(kept[max(0, len(kept)-4):], pending{token, f, time.Now()})
	s.mu.Unlock()
	id, err := s.existing(f)
	if err != nil {
		return Preview{}, err
	}
	titles := []string{}
	for _, a := range f.Articles[:min(3, len(f.Articles))] {
		titles = append(titles, a.Title)
	}
	return Preview{Token: token, Title: f.Title, HomeURL: f.HomeURL, FeedURL: f.FeedURL, ArticleCount: len(f.Articles), ExistingSourceID: id, SampleTitles: titles, Warnings: f.Warnings}, nil
}

// Add is add: the previewed feed saved, as the source it already is or a new one.
func (s *Service) Add(token string, resubscribe bool) (library.SubscriptionResult, error) {
	s.mu.Lock()
	var p *pending
	for i := range s.previews {
		if s.previews[i].token == token {
			p = &s.previews[i]
		}
	}
	s.mu.Unlock()
	if p == nil || time.Since(p.at) > 10*time.Minute {
		return library.SubscriptionResult{}, errors.New("订阅预览已过期，请重新识别地址")
	}
	f := p.feed
	id, err := s.existing(f)
	if err != nil {
		return library.SubscriptionResult{}, err
	}
	if id == nil {
		k, err := key(f.FeedURL)
		if err != nil {
			return library.SubscriptionResult{}, err
		}
		sum := sha256.Sum256([]byte(k))
		made := "feed:" + hex.EncodeToString(sum[:])[:16]
		id = &made
	}
	result, err := s.store.ImportFeed(*id, f, resubscribe, s.iconFor(f.HomeURL), s.nameFor(f.FeedURL, f.HomeURL))
	if err != nil {
		return library.SubscriptionResult{}, err
	}
	s.mu.Lock()
	for i := range s.previews {
		if s.previews[i].token == token {
			s.previews = append(s.previews[:i], s.previews[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	return result, nil
}

// Refresh is refresh: every subscribed feed (or only sourceID's), a few at a
// time, so one slow or timing-out feed does not hold up the feeds after it.
// A failed feed keeps its articles and records why. onProgress may be nil;
// it is called one event at a time.
func (s *Service) Refresh(ctx context.Context, sourceID string, onProgress func(Progress)) (RefreshResult, error) {
	state, err := s.store.Library()
	if err != nil {
		return RefreshResult{}, err
	}
	var sources []library.Source
	for _, src := range state.Sources {
		if src.Subscribed && src.FeedURL != nil && *src.FeedURL != "" && (sourceID == "" || src.ID == sourceID) {
			sources = append(sources, src)
		}
	}
	if len(sources) == 0 {
		return RefreshResult{}, errors.New("还没有可刷新的订阅，请先添加 RSS/Atom 地址")
	}
	if onProgress == nil {
		onProgress = func(Progress) {}
	}
	var mu sync.Mutex
	result := RefreshResult{Failures: []Failure{}}
	finished, next, total := 0, 0, len(sources)
	var stored error
	var wg sync.WaitGroup
	for range min(feedsAtOnce, total) {
		wg.Go(func() {
			for {
				mu.Lock()
				if next >= total {
					mu.Unlock()
					return
				}
				src := sources[next]
				next++
				onProgress(Progress{SourceID: src.ID, State: "start", Finished: finished, Total: total})
				mu.Unlock()
				r, err := s.refreshOne(ctx, src)
				mu.Lock()
				if err == nil {
					result.ImportedCount += r.ImportedCount
					result.UpdatedCount += r.UpdatedCount
					finished++
					onProgress(Progress{SourceID: src.ID, State: "done", ImportedCount: &r.ImportedCount, Finished: finished, Total: total})
				} else {
					const message = "刷新失败，已保留已有文章。请检查网络或订阅地址后重试"
					if err := s.store.RecordFeedError(src.ID, message); err != nil && stored == nil {
						stored = err
					}
					result.Failures = append(result.Failures, Failure{src.ID, message})
					finished++
					onProgress(Progress{SourceID: src.ID, State: "failed", Message: message, Finished: finished, Total: total})
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if stored != nil {
		return RefreshResult{}, stored
	}
	if result.Library, err = s.store.Library(); err != nil {
		return RefreshResult{}, err
	}
	return result, nil
}

// refreshOne reads and imports one source's feed; a refresh of a source
// already being read waits for that read instead of starting another.
func (s *Service) refreshOne(ctx context.Context, src library.Source) (library.SubscriptionResult, error) {
	s.mu.Lock()
	if c, ok := s.refreshing[src.ID]; ok {
		s.mu.Unlock()
		<-c.done
		return c.result, c.err
	}
	c := &call{done: make(chan struct{})}
	s.refreshing[src.ID] = c
	s.mu.Unlock()
	f, err := s.FetchFeed(ctx, *src.FeedURL)
	if err == nil {
		c.result, err = s.store.ImportFeed(src.ID, f, false, s.iconFor(f.HomeURL), "")
	}
	c.err = err
	s.mu.Lock()
	delete(s.refreshing, src.ID)
	s.mu.Unlock()
	close(c.done)
	return c.result, c.err
}
