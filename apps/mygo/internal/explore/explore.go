// Package explore is Explore, with the app's wiring of it (shell.go): Explore's catalog and the seed shipped with it,
// the writers upstream feeds recommend, the websites design galleries feature, their icons, the pictures that stand
// behind entries, and the first screens of design sites, captured in hidden windows (shots.go).
//
// What the window calls: List, Options.Changed, Work, Shot, Read, SourceShot; Intro is the line an author page shows.
// IconFor and NameFor go to subscription.New and library.Store.AdoptNames. The app builds it with Open:
//
//	var ex *explore.Service
//	subs := subscription.New(store, nil, func(home string) *string { return ex.IconFor(home) },
//		func(feedURL, home string) string { return ex.NameFor(&feedURL, home) })
//	resources, _ := mygo.App.Path(mygo.PathResources)
//	ex = explore.Open(filepath.Join(resources, "explore"), dir, store, subs, reader, func() { win.Update(reload) })
//	store.AdoptNames(ex.NameFor)
//	ex.Start() // captures the design room's first screens behind anything Explore asks for
//
// Captures need MyGo's event loop: a window closing while one is under way is not the last window closing until it
// ends (about 11 s at most), and capture windows count as no visible window for App.OnActivate.
package explore

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"trie/internal/feed"
	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/reading"
	"trie/internal/web"
)

const (
	// version is STATS: bumped when stats gain a field the page relies on (2: post excerpts, 3 to 8: cleaner, longer
	// excerpts, 9: post pictures, 10: galleries' sites, 12: feedKept items); older stats are shown at once and read again
	// behind them.
	version = 12
	fresh   = 6 * 60 * 60_000
	retry   = 10 * 60_000
	// picksKept is PICKS: posts kept from each upstream feed, newest first.
	picksKept = 10
	// feedKept is how many of a feed's latest items an entry keeps, as posts and as works.
	feedKept = 30
	// thinText is THIN: a recommended post whose feed gives less text than this (a member teaser, a digest's
	// one-paragraph summary) is shown only once its page reads; the same bar the page read is held to (Open).
	thinText = 400
)

// platforms are sites where many writers publish: their picks are told apart by author, and there is no one feed to
// subscribe to.
var platforms = map[string]bool{"uxdesign.cc": true, "mp.weixin.qq.com": true, "medium.com": true, "x.com": true, "twitter.com": true, "youtube.com": true, "github.com": true}

// now is the clock; the replay check fixes it.
var now = time.Now

func nowMS() int64 { return now().UnixMilli() }

func errNotListed() error { return errors.New(locales.ExploreNotListed()) }

// catalogEntry is a site of catalog.json.
type catalogEntry struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Kind    string  `json:"kind"` // blog or site
	HomeURL string  `json:"homeURL"`
	Host    string  `json:"host"`
	FeedURL *string `json:"feedURL"`
	Intro   string  `json:"intro"`
	Section string  `json:"section,omitempty"`
	Kinds   []Shelf `json:"kinds,omitempty"`
}

// Shelf is one kind of thing a gallery is good for (landing, portfolio,
// motion, type or brand) and where its works of that kind come from: the
// feed of the gallery's own section for it; else its works whose title
// Match finds; else all its works.
type Shelf struct {
	Kind  string `json:"kind"`
	Feed  string `json:"feed,omitempty"`
	Match string `json:"match,omitempty"`
}

// shelfFeeds are the feeds of a gallery's own sections.
func (e catalogEntry) shelfFeeds() []string {
	var out []string
	for _, sh := range e.Kinds {
		if sh.Feed != "" && !slices.Contains(out, sh.Feed) {
			out = append(out, sh.Feed)
		}
	}
	return out
}

// shelved is a gallery's work with the kinds it is shelved under, and the feed that lists it.
type shelved struct {
	work
	kinds []string
	feed  string
}

// works are a gallery's works and its sections', each once, newest first, with the kinds they are shelved under.
// Work's pictures go by their place here. s.mu is held.
func (s *Service) works(e catalogEntry) []shelved {
	var out []shelved
	at := map[string]int{}
	add := func(w work, feed, kind string) {
		i, ok := at[w.URL]
		if !ok {
			i = len(out)
			at[w.URL] = i
			out = append(out, shelved{work: w, feed: feed})
		}
		if kind != "" && !slices.Contains(out[i].kinds, kind) {
			out[i].kinds = append(out[i].kinds, kind)
		}
	}
	if st := s.stats[e.feed()]; e.feed() != "" && st != nil {
		for _, w := range st.Works {
			add(w, e.feed(), "")
			for _, sh := range e.Kinds {
				if sh.Holds(w.Title) {
					add(w, e.feed(), sh.Kind)
				}
			}
		}
	}
	for _, sh := range e.Kinds {
		if st := s.stats[sh.Feed]; sh.Feed != "" && st != nil {
			for _, w := range st.Works {
				add(w, sh.Feed, sh.Kind)
			}
		}
	}
	slices.SortStableFunc(out, func(x, y shelved) int { return cmp.Compare(parse(y.PublishedAt), parse(x.PublishedAt)) })
	return out
}

// Holds tells whether a work of the gallery's own feed titled title is on this shelf; a section's feed says so itself.
func (sh Shelf) Holds(title string) bool {
	return sh.Feed == "" && (sh.Match == "" || titled(sh.Match, title))
}

// titled tells whether title has what a shelf's Match looks for, in any case.
func titled(pattern, title string) bool {
	re, err := regexp.Compile("(?i)" + pattern)
	return err == nil && re.MatchString(title)
}

// gallery tells an inspiration gallery, which sells works; a showcase site's own page is its work.
func (e catalogEntry) gallery() bool {
	return e.Kind == "site" && strings.HasPrefix(e.Section, "设计灵感")
}

func (e catalogEntry) feed() string {
	if e.FeedURL == nil {
		return ""
	}
	return *e.FeedURL
}

// Entry is ExploreEntry: one site Explore shows.
type Entry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // blog or site
	// The catalog shelf it sits on, such as 中文独立博客 or 设计灵感画廊; for a recommendation, who recommended it.
	Section string `json:"section"`
	// Hand-written one-line introduction in Chinese.
	Intro   string  `json:"intro"`
	HomeURL string  `json:"homeURL"`
	Host    string  `json:"host"`
	FeedURL *string `json:"feedURL"`
	// The site's captured first screen, 400 wide, as a data URL; nil until captured, and for writers.
	Thumb *string `json:"thumb"`
	// The site's icon as a data URL, when there is one.
	Icon *string `json:"icon"`
	// Latest posts from the feed; empty when there is no feed or it could not be read.
	Posts []Post `json:"posts"`
	// Posts published in the last 90 / 30 days according to the feed; nil when unknown.
	Last90   *int `json:"last90"`
	Recent30 *int `json:"recent30"`
	// The upstream that recommended this writer or website; nil for the catalog's own.
	Upstream *string `json:"upstream"`
	// When a design gallery featured this website; such a site sorts by it, having no posts. Set only for those.
	FeaturedAt library.MaybeString `json:"featuredAt,omitzero"`
	// Why the latest read of the feed failed; the posts shown are then the last good ones.
	FetchError *string `json:"fetchError"`
	// The library source that already subscribes to this feed, if any.
	SubscribedSourceID *string `json:"subscribedSourceID"`
	// A design gallery's latest featured works (at most feedKept), whose pictures Work gives. Empty for others.
	Works []Post `json:"works"`
	// What a design gallery is good for, and where its works of each kind come from.
	Shelves []Shelf `json:"shelves,omitempty"`
}

// siteInfo is what stands behind an entry. A design site shows its own first screen, captured from
// HomeURL; a writer the pictures of their latest post, best first (possibly none), never their site.
type siteInfo struct {
	HomeURL, Host string
	Writer        bool
	Pictures      []string
}

// Options are what Explore runs on: what the service reads with, then what the app's handlers (shell.go) use.
type Options struct {
	// Resources holds catalog.json, seed.json and icons/<id>.png (resources/explore).
	Resources string
	// Cache is explore-cache.json; explore-icons.json, explore-works/ and explore-shots/ are kept beside it.
	Cache     string
	FetchFeed func(ctx context.Context, url string) (library.ParsedFeed, error)
	// Sources are the library's sources, for the entries already subscribed to.
	Sources func() []library.Source
	// Changed is told that Explore was read again in the background, so an open Explore page reads the list again.
	// It is called on a goroutine of its own; nil tells nobody.
	Changed func()
	// Upstreams are feeds recommending posts by writers outside the catalog.
	Upstreams []Upstream
	// FindIcon is a recommended writer's or featured site's icon as a data URL, nil when the site offers none it can
	// read; an error when the site could not be reached, so it is asked again next launch. Nil looks for none.
	FindIcon func(ctx context.Context, homeURL string) (*string, error)
	// ReadPage is a recommended post's page, read as the reader would show it, for the text its upstream did not give:
	// the article's HTML, "" when the page yields no full article. Nil reads none.
	ReadPage func(ctx context.Context, url, homeURL string) string

	// Preview reads one of an entry's posts for Read; nil reads none.
	Preview func(ctx context.Context, t reading.Target, source string) library.ArticleContent
	// Site is where a library source lives, for SourceShot.
	Site func(sourceID string) (*library.Site, error)
	// Shots takes sites' first screens; nil takes none, and only those on disk show.
	Shots *Shots
	// Fetch reads the pictures Work and Shot show; nil reads each with a fetch.New(6) of its own.
	Fetch fetch.Fetch
}

// Service is Explore, with the app's handlers (shell.go). Its methods may be called from any goroutine.
type Service struct {
	o                             Options
	iconsPath, shotsDir, worksDir string

	mu      sync.Mutex
	catalog []catalogEntry // nil until read
	stats   map[string]*stats
	// Recommended writers' and featured sites' icons by site, as data URLs; nil when a site offers none the shell can
	// read. Kept in explore-icons.json.
	icons    map[string]*string
	images   map[string]*string // catalog icons, by file
	captures map[string]capture // captured first screens, by file
	inflight map[string]chan struct{}
	// Feed entries behind the posts on screen, so a preview can start from the feed's own body. Memory only.
	shown map[string]library.FeedEntry
	// Feed bodies live in memory only, so the first visit after launch refreshes everything once in the background.
	warmed                        bool
	lookingForIcons, readingPages bool
	// Sites whose icon could not be reached this session; they are asked again next launch.
	unreached map[string]bool
	// Each recommended writer's site and latest pictures, and each website a gallery featured, by entry.
	pickSites, featuredSites map[string]siteInfo
	bg                       sync.WaitGroup // reads behind the list, for tests to wait for
	saving                   sync.Mutex     // one write of a file at a time, the latest state last

	shell
}

// capture is a first screen read from disk, as of the file's modification time.
type capture struct {
	mod  time.Time
	data string
}

// New makes a Service from o, the shell's own options included.
func New(o Options) *Service {
	dir := filepath.Dir(o.Cache)
	s := &Service{o: o, iconsPath: filepath.Join(dir, "explore-icons.json"), shotsDir: filepath.Join(dir, "explore-shots"), worksDir: filepath.Join(dir, "explore-works"),
		icons: map[string]*string{}, images: map[string]*string{}, captures: map[string]capture{}, inflight: map[string]chan struct{}{},
		shown: map[string]library.FeedEntry{}, unreached: map[string]bool{}, pickSites: map[string]siteInfo{}, featuredSites: map[string]siteInfo{}}
	s.shell.init()
	if data, err := os.ReadFile(s.iconsPath); err == nil {
		var icons map[string]*string
		if json.Unmarshal(data, &icons) == nil && icons != nil {
			s.icons = icons
		}
	}
	// A seed shipped with the catalog makes the first visit instant; it is refreshed in the background like any stale cache.
	s.stats = map[string]*stats{}
	for _, file := range []string{o.Cache, filepath.Join(o.Resources, "seed.json")} {
		data, err := os.ReadFile(file)
		var m map[string]*stats
		if err != nil || json.Unmarshal(data, &m) != nil || m == nil {
			continue
		}
		for url, st := range m {
			if st != nil {
				s.stats[url] = st
			}
		}
		break
	}
	return s
}

// load is the catalog, read once. s.mu is held.
func (s *Service) load() ([]catalogEntry, error) {
	if s.catalog != nil {
		return s.catalog, nil
	}
	data, err := os.ReadFile(filepath.Join(s.o.Resources, "catalog.json"))
	if err != nil {
		return nil, err
	}
	var c []catalogEntry
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	s.catalog = append([]catalogEntry{}, c...)
	return s.catalog, nil
}

func (s *Service) catalogEntries() ([]catalogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func find(catalog []catalogEntry, id string) *catalogEntry {
	for i := range catalog {
		if catalog[i].ID == id {
			return &catalog[i]
		}
	}
	return nil
}

func dataURL(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// icon is a catalog site's icon, shipped with the catalog. s.mu is held.
func (s *Service) icon(id string) *string {
	file := filepath.Join(s.o.Resources, "icons", id+".png")
	if v, ok := s.images[file]; ok {
		return v
	}
	var v *string
	if data, err := os.ReadFile(file); err == nil {
		d := dataURL("image/png", data)
		v = &d
	}
	s.images[file] = v
	return v
}

// captured is a site's captured first screen at one of the widths the page draws it, nil until it has been captured;
// a fresh capture replaces the one held in memory. s.mu is held.
func (s *Service) captured(site string, width int) *string {
	file := ShotFile(s.shotsDir, site, width)
	info, err := os.Stat(file)
	if err != nil {
		return nil
	}
	c, ok := s.captures[file]
	if !ok || !c.mod.Equal(info.ModTime()) {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil
		}
		c = capture{info.ModTime(), dataURL("image/jpeg", data)}
		s.captures[file] = c
	}
	return &c.data
}

// designSites is the design room's sites, whose first screens are captured before Explore is opened: the sites galleries
// featured (newest first, they fill the room's list), then the catalog's. Writers are shown by their posts' pictures,
// never by their sites. Reads only what is cached.
func (s *Service) designSites() []siteInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	catalog, err := s.load()
	if err != nil {
		return nil
	}
	var out []siteInfo
	for _, e := range s.featured(catalog) {
		out = append(out, siteInfo{HomeURL: e.HomeURL, Host: host(e.HomeURL)})
	}
	for _, e := range catalog {
		if e.Kind == "site" {
			out = append(out, siteInfo{HomeURL: e.HomeURL, Host: host(e.HomeURL)})
		}
	}
	return out
}

// IconFor is iconFor: the catalog icon for a site, so a source subscribed from Explore keeps its face in the sidebar.
func (s *Service) IconFor(homeURL string) *string {
	s.mu.Lock()
	defer s.mu.Unlock()
	catalog, err := s.load()
	if err != nil {
		return nil
	}
	for _, e := range catalog {
		if host(e.HomeURL) == host(homeURL) {
			return s.icon(e.ID)
		}
	}
	return nil
}

// NameFor is nameFor: the catalog's name for a feed (feed titles are often taglines, "NN/g latest articles and
// announcements"; the catalog names the site), or for a site it lists once; "" when it has none.
func (s *Service) NameFor(feedURL *string, homeURL string) string {
	catalog, err := s.catalogEntries()
	if err != nil {
		return ""
	}
	if feedURL != nil && *feedURL != "" {
		for _, e := range catalog {
			if e.feed() != "" && sameFeed(e.feed(), *feedURL) {
				return e.Name
			}
		}
	}
	var site []catalogEntry
	for _, e := range catalog {
		if host(e.HomeURL) == host(homeURL) {
			site = append(site, e)
		}
	}
	if len(site) == 1 {
		return site[0].Name
	}
	return ""
}

func sameFeed(a, b string) bool {
	x, err := feed.CanonicalFeedURL(a)
	if err != nil {
		return false
	}
	y, err := feed.CanonicalFeedURL(b)
	return err == nil && x == y
}

func (s *Service) changed() {
	if s.o.Changed != nil {
		s.o.Changed()
	}
}

func (s *Service) subscribed() []library.Source {
	var out []library.Source
	if s.o.Sources != nil {
		for _, src := range s.o.Sources() {
			if src.Subscribed {
				out = append(out, src)
			}
		}
	}
	return out
}

// batch is a refresh's feeds: those it reads, and those another read has in flight already.
type batch struct {
	queue  []string
	others []chan struct{}
}

// claim counts urls as in flight from now on, queued or being read, so a later request for one (and settled) waits for
// this read. s.mu is held.
func (s *Service) claim(urls []string) batch {
	var b batch
	mine := map[string]bool{}
	for _, url := range urls {
		if _, busy := s.inflight[url]; !busy && !mine[url] {
			mine[url] = true
			b.queue = append(b.queue, url)
		}
	}
	for _, url := range b.queue {
		s.inflight[url] = make(chan struct{})
	}
	for _, url := range urls {
		if ch, ok := s.inflight[url]; ok && !mine[url] {
			b.others = append(b.others, ch)
		}
	}
	return b
}

// refresh reads feeds six at a time. A failed read keeps the last good stats, records why, and is read again after
// retry; feeds another refresh is reading are waited for.
func (s *Service) refresh(urls []string) {
	s.mu.Lock()
	b := s.claim(urls)
	s.mu.Unlock()
	s.readAll(b)
}

func (s *Service) readAll(b batch) {
	next := make(chan string, len(b.queue))
	for _, url := range b.queue {
		next <- url
	}
	close(next)
	var wg sync.WaitGroup
	for range min(6, len(b.queue)) {
		wg.Go(func() {
			for url := range next {
				s.read(url)
			}
		})
	}
	wg.Wait()
	for _, ch := range b.others {
		<-ch
	}
	s.save(s.o.Cache, func() any { return s.stats })
}

func (s *Service) read(url string) {
	feed, err := s.o.FetchFeed(context.Background(), url)
	if err == nil {
		s.remember(url, feed)
	}
	s.mu.Lock()
	if err != nil {
		next := stats{Posts: []Post{}, Works: []work{}}
		if prev := s.stats[url]; prev != nil {
			next = *prev
		}
		next.At, next.Error, next.V = float64(nowMS()-fresh+retry), err.Error(), version
		s.stats[url] = &next
	}
	ch := s.inflight[url]
	delete(s.inflight, url)
	s.mu.Unlock()
	close(ch)
}

// remember keeps what a feed now says as its stats, and the entries behind the posts it lists.
func (s *Service) remember(url string, feed library.ParsedFeed) {
	var up *Upstream
	for i := range s.o.Upstreams {
		if s.o.Upstreams[i].FeedURL == url {
			up = &s.o.Upstreams[i]
			break
		}
	}
	if up != nil {
		picks := up.Picks(feed)
		picks = picks[:min(picksKept, len(picks))]
		made, thin := make([]*pickRecord, len(picks)), make([]bool, len(picks))
		for i, p := range picks {
			made[i] = &pickRecord{URL: p.URL, Title: p.Title, Author: p.Author, HomeURL: p.HomeURL, PublishedAt: p.PublishedAt, Excerpt: excerpt(p.Entry), Pictures: postPictures(p.Entry)}
			thin[i] = web.JSLen(plain(bodyOf(p.Entry))) < thinText
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		// A post already looked up on its page keeps what was found, so reading the upstream again does not hide it
		// until it is looked up anew.
		before := map[string]*pickRecord{}
		if prev := s.stats[url]; prev != nil && prev.V == version {
			for _, p := range prev.Picks {
				before[p.URL] = p
			}
		}
		for i, p := range made {
			was := before[p.URL]
			if p.Excerpt == "" && thin[i] && was != nil {
				p.Excerpt = was.Excerpt
			}
			p.Thin = thin[i] && (was == nil || was.Thin)
			p.Unreadable = thin[i] && was != nil && was.Unreadable
		}
		s.stats[url] = &stats{At: float64(nowMS()), Posts: []Post{}, V: version, Picks: made}
		for _, p := range picks {
			s.shown[p.URL] = p.Entry
		}
		return
	}
	catalog, _ := s.catalogEntries()
	gallery := false
	for _, e := range catalog {
		if e.feed() == url || slices.Contains(e.shelfFeeds(), url) {
			gallery = e.gallery()
			break
		}
	}
	st := feedStats(feed, nowMS(), gallery)
	st.At, st.V = float64(nowMS()), version
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats[url] = &st
	for _, p := range st.Posts {
		s.keepShown(feed, p.URL)
	}
	for _, w := range st.Works {
		s.keepShown(feed, w.URL)
	}
}

func (s *Service) keepShown(feed library.ParsedFeed, url string) {
	for _, a := range feed.Articles {
		if a.URL == url {
			s.shown[url] = a
			return
		}
	}
}

// ReadTarget is readTarget: what the reader needs to preview one of an entry's latest posts. After a restart the posts
// are known from the cache but not their feed's copy; the feed is read again for it, and when that read fails the
// post's page is still read on its own.
func (s *Service) ReadTarget(ctx context.Context, entryID, postURL string) (reading.Target, error) {
	if strings.HasPrefix(entryID, "pick:") {
		s.mu.Lock()
		var from *Upstream
		var found pickRecord
		for i := range s.o.Upstreams {
			if st := s.stats[s.o.Upstreams[i].FeedURL]; st != nil {
				if j := slices.IndexFunc(st.Picks, func(p *pickRecord) bool { return p.URL == postURL }); j >= 0 {
					from, found = &s.o.Upstreams[i], *st.Picks[j]
					break
				}
			}
		}
		_, isShown := s.shown[postURL]
		s.mu.Unlock()
		if from == nil {
			return reading.Target{}, errNotListed()
		}
		if !isShown {
			if f, err := s.o.FetchFeed(ctx, from.FeedURL); err == nil {
				s.remember(from.FeedURL, f)
			}
		}
		s.mu.Lock()
		entry := s.shown[postURL]
		s.mu.Unlock()
		return reading.Target{ID: "explore:" + postURL, URL: postURL, Aliases: []string{}, HomeURL: found.HomeURL, FeedBody: entry.FeedBody, TrustedArticle: true}, nil
	}
	s.mu.Lock()
	catalog, err := s.load()
	if err != nil {
		s.mu.Unlock()
		return reading.Target{}, err
	}
	entry := find(catalog, entryID)
	var st *stats
	var shelf *shelved // a gallery's work, from its own feed or a section's
	feedURL := ""
	if entry != nil && entry.feed() != "" {
		st, feedURL = s.stats[entry.feed()], entry.feed()
	}
	listed := st != nil && slices.ContainsFunc(st.Posts, func(p Post) bool { return p.URL == postURL })
	if entry != nil && entry.gallery() {
		for _, w := range s.works(*entry) {
			if w.URL == postURL {
				listed, feedURL, shelf = true, w.feed, &w
				break
			}
		}
	}
	_, isShown := s.shown[postURL]
	s.mu.Unlock()
	if !listed {
		return reading.Target{}, errNotListed()
	}
	if !isShown {
		if f, err := s.o.FetchFeed(ctx, feedURL); err == nil {
			s.remember(feedURL, f)
		}
	}
	s.mu.Lock()
	post, hasPost := s.shown[postURL]
	s.mu.Unlock()
	t := reading.Target{ID: "explore:" + postURL, URL: postURL, Aliases: post.Aliases, HomeURL: entry.HomeURL, FeedBody: post.FeedBody, TrustedArticle: true}
	if t.Aliases == nil {
		t.Aliases = []string{}
	}
	// A gallery's work carries what its feed knows, for the reader to stand it as a work page when the gallery's own
	// page gives none. Its work is looked up in the stats as they were before the feed was read again.
	if entry.gallery() {
		var w *work
		if shelf != nil {
			w = &shelf.work
		}
		card := reading.Card{}
		if hasPost && post.Title != "" {
			card.Name = post.Title
		} else if w != nil {
			card.Name = w.Title
		}
		if hasPost {
			card.Site = featuredSite(post, entry.HomeURL)
		}
		if w != nil {
			card.Picture = &w.Image
		} else if hasPost {
			card.Picture = firstImage(post)
		}
		t.Work = &card
	}
	return t, nil
}

// links are the addresses Explore shows, which the app may open in the browser.
func (s *Service) links() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	catalog, err := s.load()
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range catalog {
		out = append(out, e.HomeURL)
	}
	for _, st := range s.stats {
		for _, p := range st.Posts {
			out = append(out, p.URL)
		}
		for _, w := range st.Works {
			out = append(out, w.URL)
		}
		for _, p := range st.Picks {
			out = append(out, p.URL, p.HomeURL)
		}
		for _, site := range st.Sites {
			out = append(out, site.URL)
		}
	}
	return out
}

// workImage is where the picture of one of a gallery's works lives; the shell fetches and keeps it.
func (s *Service) workImage(entryID string, index int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	catalog, err := s.load()
	if err != nil {
		return "", err
	}
	if e := find(catalog, entryID); e != nil && e.gallery() {
		if ws := s.works(*e); index >= 0 && index < len(ws) {
			return ws[index].Image, nil
		}
	}
	return "", nil
}

// site is what stands behind an entry; nil for none.
func (s *Service) site(entryID string) (*siteInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.pickSites[entryID]; ok {
		return &p, nil
	}
	if f, ok := s.featuredSites[entryID]; ok {
		return &siteInfo{HomeURL: f.HomeURL, Host: f.Host, Pictures: []string{}}, nil
	}
	catalog, err := s.load()
	if err != nil {
		return nil, err
	}
	e := find(catalog, entryID)
	if e == nil {
		return nil, nil
	}
	out := &siteInfo{HomeURL: e.HomeURL, Host: host(e.HomeURL), Writer: e.Kind == "blog", Pictures: []string{}}
	if st := s.stats[e.feed()]; e.feed() != "" && st != nil && len(st.Posts) > 0 && st.Posts[0].URL != "" && len(st.Pictures[st.Posts[0].URL]) > 0 {
		out.Pictures = st.Pictures[st.Posts[0].URL]
	}
	return out, nil
}

// settled waits for a background read of the feeds behind an entry (its own, or the upstreams' for a recommended
// writer), so a first look sees what they now say.
func (s *Service) settled(entryID string) {
	s.mu.Lock()
	catalog, err := s.load()
	if err != nil {
		s.mu.Unlock()
		return
	}
	entry := find(catalog, entryID)
	var feeds []string
	if entry == nil {
		for _, up := range s.o.Upstreams {
			feeds = append(feeds, up.FeedURL)
		}
	} else if entry.feed() != "" {
		feeds = []string{entry.feed()}
	}
	var waits []chan struct{}
	for _, url := range feeds {
		if ch, ok := s.inflight[url]; ok {
			waits = append(waits, ch)
		}
	}
	s.mu.Unlock()
	for _, ch := range waits {
		<-ch
	}
	// Recommended writers are drawn from the upstreams' latest read.
	if entry == nil {
		sources := s.subscribed()
		s.mu.Lock()
		s.picked(catalog, sources)
		s.mu.Unlock()
	}
}

// lookForOpenings reads, four at a time, the pages of recommended posts whose feed gives less than an article, for the
// article; a page that cannot be read marks its post unreadable and it is never shown. A post is shown only once it is
// known to read, so nothing reaches the page that opens to a teaser. s.mu is held.
func (s *Service) lookForOpenings() {
	if s.readingPages || s.o.ReadPage == nil {
		return
	}
	var waiting []*pickRecord
	for _, up := range s.o.Upstreams {
		if st := s.stats[up.FeedURL]; st != nil {
			for _, p := range st.Picks {
				if p.Thin && !p.Unreadable {
					waiting = append(waiting, p)
				}
			}
		}
	}
	if len(waiting) == 0 {
		return
	}
	s.readingPages = true
	s.bg.Go(func() {
		next := make(chan *pickRecord, len(waiting))
		for _, p := range waiting {
			next <- p
		}
		close(next)
		var wg sync.WaitGroup
		for range min(4, len(waiting)) {
			wg.Go(func() {
				for p := range next {
					// A record's address, home and title never change; its excerpt and marks do, under s.mu.
					html := s.o.ReadPage(context.Background(), p.URL, p.HomeURL)
					opening := ""
					if html != "" {
						opening = excerpt(library.FeedEntry{Title: p.Title, URL: p.URL, FeedBody: &library.FeedBody{HTML: html}})
					}
					s.mu.Lock()
					if opening != "" {
						if p.Excerpt == "" {
							p.Excerpt = opening
						}
						p.Thin = false
					} else {
						p.Unreadable = true
					}
					s.mu.Unlock()
				}
			})
		}
		wg.Wait()
		s.save(s.o.Cache, func() any { return s.stats })
		s.mu.Lock()
		s.readingPages = false
		s.mu.Unlock()
		s.changed()
	})
}

// lookForIcons finds, four at a time, the icons of recommended writers' and featured sites not looked for yet, then
// tells an open Explore page to read the list again. homes are sites and their home pages. s.mu is held.
func (s *Service) lookForIcons(homes [][2]string) {
	if s.lookingForIcons || s.o.FindIcon == nil || len(homes) == 0 {
		return
	}
	s.lookingForIcons = true
	s.bg.Go(func() {
		next := make(chan [2]string, len(homes))
		for _, h := range homes {
			next <- h
		}
		close(next)
		var wg sync.WaitGroup
		for range min(4, len(homes)) {
			wg.Go(func() {
				// ponytail: a site that answered with no readable icon is remembered as nil and never asked again;
				// clear explore-icons.json to retry.
				for h := range next {
					icon, err := s.o.FindIcon(context.Background(), h[1])
					s.mu.Lock()
					if err != nil {
						s.unreached[h[0]] = true
					} else {
						s.icons[h[0]] = icon
					}
					s.mu.Unlock()
				}
			})
		}
		wg.Wait()
		s.save(s.iconsPath, func() any { return s.icons })
		s.mu.Lock()
		s.lookingForIcons = false
		s.mu.Unlock()
		s.changed()
	})
}

// save writes what value gives, taken under s.mu, to file; the cache only saves time, so a failure is let go.
func (s *Service) save(file string, value func() any) {
	s.saving.Lock()
	defer s.saving.Unlock()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	s.mu.Lock()
	err := enc.Encode(value())
	s.mu.Unlock()
	if err == nil {
		_ = writeFile(file, bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	}
}

// writeFile writes data to file whole: a reader sees the old file or the new one.
func writeFile(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if err = errors.Join(err, tmp.Chmod(0o644), tmp.Close()); err == nil {
		err = os.Rename(tmp.Name(), file)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// shot is a large capture of a design site's home page, for Explore's stage; the list itself only carries the small one.
func (s *Service) shot(entryID string) string {
	site, err := s.site(entryID)
	if err != nil || site == nil || site.Writer {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if data := s.captured(site.Host, 1280); data != nil {
		return *data
	}
	return ""
}

// List is loadExplore (list()): the catalog's entries, then the writers the upstreams recommend, then the websites the
// design galleries feature. Feeds never read yet are read first; the others are shown at once and read again behind
// the list when stale, or on the first visit after launch, and Changed is told once they have been.
func (s *Service) List(ctx context.Context) ([]Entry, error) {
	s.mu.Lock()
	catalog, err := s.load()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	t := float64(nowMS())
	var missing, stale, upstream []string
	for _, e := range catalog {
		for _, f := range e.shelfFeeds() {
			if st := s.stats[f]; st == nil || !s.warmed || t-st.At > fresh || st.V != version {
				stale = append(stale, f)
			}
		}
		url := e.feed()
		if url == "" {
			continue
		}
		// Stats saved before works were kept are read again at once for the galleries, whose page is built from them.
		st := s.stats[url]
		if st == nil || e.gallery() && st.Works == nil {
			missing = append(missing, url)
		}
		if st != nil && (!s.warmed || t-st.At > fresh || st.V != version) {
			stale = append(stale, url)
		}
	}
	s.warmed = true
	// Upstreams never hold the page, and are read at most every fresh: they are large and some refuse frequent readers.
	for _, up := range s.o.Upstreams {
		if st := s.stats[up.FeedURL]; st == nil || t-st.At > fresh || st.V != version {
			upstream = append(upstream, up.FeedURL)
		}
	}
	s.mu.Unlock()
	if len(missing) > 0 {
		done := make(chan struct{})
		go func() { s.refresh(missing); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	sources := s.subscribed()
	s.mu.Lock()
	// Stale feeds are read again behind the list, upstreams first: their writers lead the writing room. They are in
	// flight from here on, and land once the list is made from what was known before.
	var behind *batch
	if len(stale) > 0 || len(upstream) > 0 {
		b := s.claim(append(upstream, stale...))
		behind = &b
	}
	out := make([]Entry, 0, len(catalog))
	for _, e := range catalog {
		out = append(out, s.entry(e, sources))
	}
	out = append(out, s.picked(catalog, sources)...)
	out = append(out, s.featured(catalog)...)
	s.mu.Unlock()
	if behind != nil {
		s.bg.Go(func() {
			s.readAll(*behind)
			s.changed()
		})
	}
	return out, nil
}

// entry is a catalog site as List shows it. s.mu is held.
func (s *Service) entry(e catalogEntry, sources []library.Source) Entry {
	out := Entry{ID: e.ID, Name: e.Name, Kind: e.Kind, Section: e.Section, Intro: e.Intro, HomeURL: e.HomeURL, Host: e.Host, FeedURL: e.FeedURL,
		Icon: s.icon(e.ID), Posts: []Post{}, Works: []Post{}}
	if e.Kind == "site" {
		out.Thumb = s.captured(host(e.HomeURL), 400)
	}
	if st := s.stats[e.feed()]; e.feed() != "" && st != nil {
		if st.Posts != nil {
			out.Posts = slices.Clone(st.Posts)
		}
		out.Last90, out.Recent30 = st.Last90, st.Recent30
		if st.Error != "" {
			msg := st.Error
			out.FetchError = &msg
		}
	}
	if e.gallery() {
		out.Shelves = e.Kinds
		for _, w := range s.works(e) {
			out.Works = append(out.Works, Post{Title: w.Title, URL: w.URL, PublishedAt: w.PublishedAt, Kinds: w.kinds})
		}
	}
	var match *library.Source
	for i, src := range sources {
		if e.feed() != "" && src.FeedURL != nil && *src.FeedURL != "" && sameFeed(*src.FeedURL, e.feed()) {
			match = &sources[i]
			break
		}
	}
	for i, src := range sources {
		if match == nil && host(src.HomeURL) == host(e.HomeURL) {
			match = &sources[i]
		}
	}
	if match != nil && match.ID != "" {
		id := match.ID
		out.SubscribedSourceID = &id
	}
	return out
}

// published is a time a post sorts by: Date.parse(value) || 0.
func published(at *string) float64 {
	if t := parse(at); t == t {
		return t
	}
	return 0
}

// picked is the writers the upstreams recommend, one entry per site (per author on shared platforms); writers in the
// catalog keep only their own feed. Their icons and the pages of posts whose feed gives a teaser are looked for behind
// it. s.mu is held.
func (s *Service) picked(catalog []catalogEntry, sources []library.Source) []Entry {
	writers, pictures, unknown := s.writers(catalog, sources)
	s.pickSites = map[string]siteInfo{}
	for _, w := range writers {
		found := pictures[w.Posts[0].URL]
		if found == nil {
			found = []string{}
		}
		s.pickSites[w.ID] = siteInfo{HomeURL: w.HomeURL, Host: w.Host, Writer: true, Pictures: found}
	}
	s.lookForIcons(unknown)
	s.lookForOpenings()
	return writers
}

// writers is picked's entries, the pictures of their posts by address, and the sites whose icons have not been looked
// for. s.mu is held.
func (s *Service) writers(catalog []catalogEntry, sources []library.Source) ([]Entry, map[string][]string, [][2]string) {
	known := map[string]bool{}
	for _, e := range catalog {
		known[host(e.HomeURL)] = true
	}
	seen, at, unknownAt := map[string]bool{}, map[string]int{}, map[string]int{}
	var writers []Entry
	var unknown [][2]string
	pictures := map[string][]string{}
	for _, up := range s.o.Upstreams {
		st := s.stats[up.FeedURL]
		if st == nil {
			continue
		}
		for _, p := range st.Picks {
			site := host(p.HomeURL)
			post, _, _ := strings.Cut(p.URL, "?")
			post, _, _ = strings.Cut(post, "#")
			post = strings.TrimRight(post, "/")
			// A post with no opening yet waits for its page to be read; one whose page cannot be read is left out.
			if site == "" || known[site] || seen[post] || p.Excerpt == "" || p.Thin {
				continue
			}
			seen[post] = true
			onPlatform, key := platforms[site], site
			if onPlatform {
				key = site + ":" + p.Author
			}
			i, ok := at[key]
			if !ok {
				name := up.Name
				w := Entry{ID: "pick:" + key, Name: p.Author, Kind: "blog", Section: up.Name, Intro: locales.ExploreFeaturedOn(up.Name), HomeURL: p.HomeURL, Host: site,
					Icon: s.icons[site], Posts: []Post{}, Upstream: &name, Works: []Post{}}
				if !onPlatform {
					// Subscribing reads the writer's home page and finds the feed it declares.
					home := p.HomeURL
					w.FeedURL = &home
					if j := slices.IndexFunc(sources, func(src library.Source) bool { return host(src.HomeURL) == site }); j >= 0 && sources[j].ID != "" {
						id := sources[j].ID
						w.SubscribedSourceID = &id
					}
				}
				i, at[key] = len(writers), len(writers)
				writers = append(writers, w)
			}
			writers[i].Posts = append(writers[i].Posts, Post{Title: p.Title, URL: p.URL, PublishedAt: p.PublishedAt, Excerpt: p.Excerpt})
			if len(p.Pictures) > 0 {
				pictures[p.URL] = p.Pictures
			}
			if _, looked := s.icons[site]; !looked && !s.unreached[site] {
				if j, ok := unknownAt[site]; ok {
					unknown[j][1] = p.HomeURL
				} else {
					unknownAt[site] = len(unknown)
					unknown = append(unknown, [2]string{site, p.HomeURL})
				}
			}
		}
	}
	for _, w := range writers {
		sort.SliceStable(w.Posts, func(i, j int) bool { return published(w.Posts[i].PublishedAt) > published(w.Posts[j].PublishedAt) })
	}
	return writers, pictures, unknown
}

// featured is the websites the design galleries featured, one entry each (the newest feature wins), as design sites of
// their own; the catalog's own sites keep theirs. Each stands on its captured first screen and sorts by when it was
// featured. s.mu is held.
func (s *Service) featured(catalog []catalogEntry) []Entry {
	found, unknown := s.features(catalog)
	s.featuredSites = map[string]siteInfo{}
	for _, e := range found {
		s.featuredSites[e.ID] = siteInfo{HomeURL: e.HomeURL, Host: e.Host}
	}
	s.lookForIcons(unknown)
	return found
}

// features is featured's entries and the sites whose icons have not been looked for. s.mu is held.
func (s *Service) features(catalog []catalogEntry) ([]Entry, [][2]string) {
	known := map[string]bool{}
	for _, e := range catalog {
		known[host(e.HomeURL)] = true
	}
	type item struct {
		featuredRecord
		from string
	}
	var all []item
	for _, g := range catalog {
		if st := s.stats[g.feed()]; g.gallery() && g.feed() != "" && st != nil {
			for _, site := range st.Sites {
				all = append(all, item{site, g.Name})
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return published(all[i].PublishedAt) > published(all[j].PublishedAt) })
	var found []Entry
	var unknown [][2]string
	seen := map[string]bool{}
	for _, site := range all {
		name := host(site.URL)
		if name == "" || known[name] || seen[name] {
			continue
		}
		seen[name] = true
		// A gallery that titles its items with a sentence about the project (Mindsparkle) names the site by its address instead.
		title, from := site.Title, site.from
		if web.JSLen(title) > 32 {
			title = name
		}
		found = append(found, Entry{ID: "featured:" + name, Name: title, Kind: "site", Section: from, Intro: locales.ExploreFeaturedOn(from), HomeURL: site.URL, Host: name,
			Thumb: s.captured(name, 400), Icon: s.icons[name], Posts: []Post{}, Upstream: &from, FeaturedAt: library.MaybeString{Set: true, Value: site.PublishedAt},
			Works: []Post{}})
		if _, looked := s.icons[name]; !looked && !s.unreached[name] {
			o, _ := origin(site.URL)
			unknown = append(unknown, [2]string{name, o + "/"})
		}
	}
	return found, unknown
}

// Intro is the line Explore has about a source's site, for its author page when the source's feed says nothing about
// itself: that of the first entry, as List orders them, on the source's host; "" when there is none.
// It reads only what is known already.
func (s *Service) Intro(source library.Source) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	catalog, err := s.load()
	if err != nil {
		return ""
	}
	hostOf := func(value string) string {
		u, err := web.ParseURL(value, nil)
		if err != nil {
			return ""
		}
		return strings.TrimPrefix(u.Hostname(), "www.")
	}
	on := func(entryHost, homeURL string) bool {
		return entryHost == source.Host || hostOf(homeURL) == hostOf(source.HomeURL)
	}
	for _, e := range catalog {
		if on(e.Host, e.HomeURL) {
			return e.Intro
		}
	}
	writers, _, _ := s.writers(catalog, nil)
	features, _ := s.features(catalog)
	for _, e := range append(writers, features...) {
		if on(e.Host, e.HomeURL) {
			return e.Intro
		}
	}
	return ""
}
