package library

import (
	"strings"
	"testing"
	"time"
	"trie/internal/web"
)

// The store parts of the explore, subscription, reading and reader-format
// cases. Their feeds are written as the parsed feed the store receives.

var empty = Snapshot{Version: 1, AsOf: "2026-11-01", Sources: []SourceSnapshot{}, Articles: []ArticleSnapshot{}, ReviewItems: []ReviewItem{}}

func daysAgo(days int) *string {
	return ptr(web.ISOTime(time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()))
}

// post is one parsed RSS item: a link, a title, an optional date and body.
type post struct {
	slug string
	date *string
	body string
}

func writerFeed(home string, posts ...post) ParsedFeed {
	feed := ParsedFeed{FeedURL: "https://writer.example/feed", HomeURL: home, Title: "Real Writer", FetchedAt: web.Now(), Warnings: []string{}, Description: MaybeString{Set: true}}
	for _, p := range posts {
		url := "https://writer.example/" + p.slug + "/"
		entry := FeedEntry{ID: url, URL: url, Title: p.slug, Authors: []string{}, PublishedAt: p.date, Aliases: []string{}, BodyStatus: "not_extracted", VerificationLabel: "来自 RSS/Atom 订阅"}
		if p.body != "" {
			entry.FeedBody = &FeedBody{HTML: "<p>" + p.body + "</p>", Kind: "body", Version: "v-" + p.body, FeedURL: feed.FeedURL}
		}
		feed.Articles = append(feed.Articles, entry)
	}
	return feed
}

func readByTitle(state LibraryState) map[string]bool {
	out := map[string]bool{}
	for _, a := range state.Articles {
		out[a.Title] = a.Read
	}
	return out
}

// subscribing counts only the last 30 days as unread; undated posts stay unread
func TestLast30DaysUnread(t *testing.T) {
	s := mustOpen(t, ":memory:", empty)
	icon := "data:image/png;base64,AA"
	must[SubscriptionResult](t)(s.ImportFeed("writer", writerFeed("https://writer.example/", post{"new", daysAgo(3), ""}, post{"old", daysAgo(45), ""}, post{"nodate", nil, ""}), true, &icon, ""))
	state := lib(t, s)
	equal(t, readByTitle(state), map[string]bool{"new": false, "old": true, "nodate": false}, "read")
	equal(t, *state.Sources[0].Icon, icon, "a source subscribed from Explore keeps the catalog icon")
}

// the 30-day rule never overrides read state the reader already set
func TestThirtyDayRuleKeepsReadState(t *testing.T) {
	s := mustOpen(t, ":memory:", empty)
	importFeed(t, s, "writer", writerFeed("https://writer.example/", post{"kept", daysAgo(50), ""}))
	kept := lib(t, s).Articles[0]
	mutate(t, s, MarkRead{ID: kept.ID, Read: false})
	must[SubscriptionResult](t)(s.ImportFeed("writer", writerFeed("https://writer.example/", post{"kept", daysAgo(50), ""}, post{"older", daysAgo(60), ""}), false, nil, ""))
	equal(t, readByTitle(lib(t, s)), map[string]bool{"kept": false, "older": true}, "read")
}

// subscriptions take the catalog name; unrenamed sources adopt it once, renamed ones keep theirs
func TestAdoptNames(t *testing.T) {
	s := mustOpen(t, ":memory:", empty)
	feed := func(feedURL, title, home string) ParsedFeed {
		f := writerFeed(home, post{"p", nil, ""})
		f.FeedURL, f.Title = feedURL, title
		f.Articles[0].ID, f.Articles[0].URL = home+"p/", home+"p/"
		return f
	}
	feeds := []ParsedFeed{
		feed("https://nngroup.com/feed/rss", "NN/g latest articles and announcements", "https://www.nngroup.com/"),
		feed("https://fontsinuse.com/other.rss", "Fonts In Use – Type in use, indexed and archived", "https://www.fontsinuse.com/"),
		feed("https://shared.example/c.xml", "Shared C", "https://shared.example/"),
		feed("https://writer.example/feed", "Real Writer", "https://writer.example/"),
	}
	for _, f := range feeds {
		importFeed(t, s, f.FeedURL, f)
	}
	renamed := feeds[0]
	renamed.FeedURL = "https://www.nngroup.com/feed/rss/"
	importFeed(t, s, "nng-renamed", renamed)
	mutate(t, s, RenameSource{ID: "nng-renamed", Name: "UX research"})
	// Explore's NameFor: the catalog's name for a feed, or for a site it lists once.
	nameFor := func(feedURL *string, homeURL string) string {
		switch {
		case strings.Contains(homeURL, "nngroup.com"):
			return "Nielsen Norman Group"
		case strings.Contains(homeURL, "fontsinuse.com"):
			return "Fonts In Use"
		}
		return ""
	}
	if err := s.AdoptNames(nameFor); err != nil {
		t.Fatal(err)
	}
	names := func() map[string]string {
		out := map[string]string{}
		for _, source := range lib(t, s).Sources {
			out[source.ID] = source.DisplayName
		}
		return out
	}
	equal(t, names(), map[string]string{"https://nngroup.com/feed/rss": "Nielsen Norman Group", "https://fontsinuse.com/other.rss": "Fonts In Use",
		"https://shared.example/c.xml": "Shared C", "https://writer.example/feed": "Real Writer", "nng-renamed": "UX research"}, "names")
	revision := lib(t, s).Revision
	if err := s.AdoptNames(nameFor); err != nil {
		t.Fatal(err)
	}
	equal(t, lib(t, s).Revision, revision, "a second run changes nothing")
}

// refresh merges new entries without duplicating or removing aged-out entries, preserves user state
func TestRefreshMergesEntries(t *testing.T) {
	s := mustOpen(t, ":memory:", empty)
	added := importFeed(t, s, "writer", writerFeed("https://writer.example/", post{"one", nil, "Short real body"}, post{"two", nil, "Short real body"}))
	id := added.Library.Articles[0].ID
	mutate(t, s, MarkRead{ID: id, Read: true})
	mutate(t, s, ToggleFavorite{ID: id})
	same := must[SubscriptionResult](t)(s.ImportFeed("writer", writerFeed("https://writer.example/", post{"one", nil, "Short real body"}, post{"two", nil, "Short real body"}), false, nil, ""))
	equal(t, [2]int{same.ImportedCount, same.UpdatedCount}, [2]int{0, 0}, "unchanged refresh")
	refreshed := must[SubscriptionResult](t)(s.ImportFeed("writer", writerFeed("https://writer.example/", post{"two", nil, "Short real body"}, post{"three", nil, "Short real body"}), false, nil, ""))
	equal(t, [2]int{refreshed.ImportedCount, len(refreshed.Library.Articles)}, [2]int{1, 3}, "refresh")
	for _, a := range refreshed.Library.Articles {
		if a.ID == id && (!a.Favorite || !a.Read) {
			t.Fatal("refresh changed the reader's state")
		}
	}
	again := importFeed(t, s, "writer", writerFeed("https://writer.example/", post{"two", nil, "Short real body"}, post{"three", nil, "Short real body"}))
	equal(t, []int{len(again.Library.Sources), len(again.Library.Articles), again.ImportedCount}, []int{1, 3, 0}, "added again")
	// A changed title or body counts as an update, not an import.
	changed := writerFeed("https://writer.example/", post{"two", nil, "Edited body"}, post{"three", nil, "Short real body"})
	changed.Articles[1].Title = "Three, renamed"
	result := importFeed(t, s, "writer", changed)
	equal(t, [2]int{result.ImportedCount, result.UpdatedCount}, [2]int{0, 2}, "updates")
}

// feed updates do not get overwritten by bundled sample metadata after restart
func TestFeedUpdatesSurviveReseeding(t *testing.T) {
	path, seed := libraryPath(t), fixture(t)
	s := mustOpen(t, path, seed)
	result := importFeed(t, s, "antfu", writerFeed("https://antfu.me/", post{"new", nil, "Short real body"}))
	title := func(state LibraryState) (string, *string) {
		for _, source := range state.Sources {
			if source.ID == "antfu" {
				return source.Title, source.FeedURL
			}
		}
		return "", nil
	}
	if got, _ := title(result.Library); got != "Real Writer" {
		t.Fatalf("title %q", got)
	}
	s.Close()
	got, feedURL := title(lib(t, mustOpen(t, path, seed)))
	if got != "Real Writer" || feedURL == nil {
		t.Fatalf("after reseeding: %q %v", got, feedURL)
	}
}

// same article in two subscriptions shares read/favorite state and retains both memberships
func TestArticleInTwoSubscriptions(t *testing.T) {
	s := mustOpen(t, ":memory:", empty)
	importFeed(t, s, "first", writerFeed("https://writer.example/", post{"one", nil, "Short real body"}))
	second := importFeed(t, s, "second", writerFeed("https://writer.example/", post{"one", nil, "Short real body"}))
	equal(t, [2]int{len(second.Library.Sources), len(second.Library.Articles)}, [2]int{2, 1}, "counts")
	equal(t, second.Library.Articles[0].SourceIDs, []string{"first", "second"}, "memberships")
	mutate(t, s, ToggleFavorite{ID: second.Library.Articles[0].ID})
	state := mutate(t, s, SetSubscription{ID: "first", Subscribed: false})
	if !state.Articles[0].Favorite || !state.Sources[1].Subscribed {
		t.Fatal("state not shared")
	}
}

// a different feed cannot replace existing article metadata or body by claiming the same link
func TestOtherFeedCannotReplaceArticle(t *testing.T) {
	s := mustOpen(t, ":memory:", empty)
	importFeed(t, s, "writer", writerFeed("https://writer.example/", post{"one", nil, "Original body"}))
	previous := must[*FeedBody](t)(s.FeedContent("https://writer.example/one/"))
	evil := writerFeed("https://writer.example/", post{"one", nil, "Fake replacement"})
	evil.Articles[0].Title = "Fake title"
	importFeed(t, s, "evil", evil)
	state := lib(t, s)
	equal(t, [2]int{len(state.Articles), len(state.Articles[0].SourceIDs)}, [2]int{1, 2}, "one article, two memberships")
	equal(t, state.Articles[0].Title, "one", "title")
	equal(t, must[*FeedBody](t)(s.FeedContent("https://writer.example/one/")), previous, "feed body")
}

// unsubscribing during a refresh never resurrects the source (a refresh imports without resubscribing)
func TestRefreshKeepsUnsubscribed(t *testing.T) {
	s := mustOpen(t, ":memory:", empty)
	importFeed(t, s, "writer", writerFeed("https://writer.example/", post{"one", nil, ""}))
	mutate(t, s, SetSubscription{ID: "writer", Subscribed: false})
	must[SubscriptionResult](t)(s.ImportFeed("writer", writerFeed("https://writer.example/", post{"one", nil, ""}), false, nil, ""))
	equal(t, lib(t, s).Sources[0].Subscribed, false, "subscribed")
}

// failed refresh retains articles and records error
func TestRecordFeedError(t *testing.T) {
	s := mustOpen(t, ":memory:", empty)
	importFeed(t, s, "writer", writerFeed("https://writer.example/", post{"one", nil, ""}, post{"two", nil, ""}))
	before := lib(t, s).Revision
	if err := s.RecordFeedError("writer", strings.Repeat("😀", 600)); err != nil {
		t.Fatal(err)
	}
	state := lib(t, s)
	// 1000 UTF-16 units: 500 emoji, as message.slice(0, 1000) keeps.
	equal(t, []any{len(state.Articles), *state.Sources[0].LastError, state.Revision}, []any{2, strings.Repeat("😀", 500), before + 1}, "after the error")
	equal(t, importFeed(t, s, "writer", writerFeed("https://writer.example/", post{"one", nil, ""})).Library.Sources[0].LastError, (*string)(nil), "error cleared by a read")
}

// An entry under another spelling of a kept link (www., case, a fragment, a
// trailing slash) is the same article. Its link has the article's key, so it
// is not added as an alias, as in earlier versions.
func TestImportMatchesSpellings(t *testing.T) {
	s := mustOpen(t, ":memory:", empty)
	importFeed(t, s, "writer", writerFeed("https://writer.example/", post{"one", nil, ""}))
	other := writerFeed("https://writer.example/", post{"one", nil, ""})
	other.Articles[0].ID, other.Articles[0].URL = "https://WWW.writer.example/one#top", "https://WWW.writer.example/one#top"
	result := importFeed(t, s, "mirror", other)
	equal(t, len(result.Library.Articles), 1, "articles")
	equal(t, result.Library.Articles[0].Aliases, []string{}, "aliases")
	equal(t, result.Library.Articles[0].SourceIDs, []string{"writer", "mirror"}, "memberships")
	_, err := s.ImportFeed("writer", ParsedFeed{FeedURL: "x", HomeURL: "not a url", Title: "T", FetchedAt: web.Now()}, true, nil, "")
	fails(t, err, "Invalid URL")
	equal(t, lib(t, s).Revision, result.Library.Revision, "a failed import changes nothing")
}

// offline refresh fallback upgrades old layout even when a newer feed body is pending (store part)
func TestImportWithoutFetchTime(t *testing.T) {
	seed := previewSnapshot()
	s := mustOpen(t, ":memory:", seed)
	entry := entryOf(seed.Articles[0], &FeedBody{HTML: "<p>Newer feed body</p>", Kind: "body", Version: "newer", FeedURL: "https://writer.example/feed"})
	importFeed(t, s, "writer", ParsedFeed{FeedURL: "https://writer.example/feed", HomeURL: "https://writer.example/", Title: "Writer", Warnings: []string{}, Articles: []FeedEntry{entry}})
	c := pageContent("<p>[[toc]]</p><h2>Cached heading</h2>")
	c.FormatVersion = 4
	c.Headings, c.CodeBlocks, c.HighlightStyles = []Heading{{ID: "h", Level: 2, Text: "Cached heading"}}, []CodeBlock{}, []string{}
	if err := s.SaveContent(c); err != nil {
		t.Fatal(err)
	}
	got := must[*ArticleContent](t)(s.Content(previewBase))
	equal(t, []any{got.FormatVersion, got.FeedVersion, must[*FeedBody](t)(s.FeedContent(previewBase)).Version}, []any{4, (*string)(nil), "newer"}, "content")
	equal(t, lib(t, s).AsOf, "2026-11-02", "an empty fetch time leaves asOf")
}

// body cache survives process-style reopen and reseeding without changing favorites (store part)
func TestContentSurvivesReopen(t *testing.T) {
	path, seed := libraryPath(t), fixture(t)
	s := mustOpen(t, path, seed)
	id := seed.Articles[0].ID
	mutate(t, s, ToggleFavorite{ID: id})
	c := ArticleContent{ArticleID: id, Status: "ready", HTML: "<pre><code>const answer = 42</code></pre>", TextLength: 16,
		Links: []string{"https://example.com/reference"}, FetchedAt: web.Now(), SourceURL: seed.Articles[0].URL, SourceKind: "page"}
	if err := s.SaveContent(c); err != nil {
		t.Fatal(err)
	}
	equal(t, lib(t, s).Articles[0].BodyStatus, "ready", "body status")
	s.Close()
	s = mustOpen(t, path, seed)
	cached := must[*ArticleContent](t)(s.Content(id))
	equal(t, []any{cached.HTML, cached.FromCache, lib(t, s).Articles[0].Favorite}, []any{c.HTML, true, true}, "after reopening")
	equal(t, must[[]string](t)(s.ContentLinks()), []string{"https://example.com/reference"}, "links")
	_, err := s.Content("missing")
	fails(t, err, "Unknown article ID")
	equal(t, must[*Site](t)(s.Site("antfu")).HomeURL, seed.Sources[1].HomeURL, "site")
	equal(t, must[*Site](t)(s.Site("missing")), (*Site)(nil), "unknown site")
}
