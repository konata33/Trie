package library

import (
	"slices"
	"testing"
)

// A post read from Explore is kept under its author's source, added
// unsubscribed; kept again, or kept at an address the library has, nothing
// is added; subscribing later takes the same source and the same article.
func TestKeepThenSubscribe(t *testing.T) {
	store := mustOpen(t, libraryPath(t), fixture(t))
	feed := "https://kept.example/feed.xml"
	keep := Keep{Source: SourceSnapshot{ID: "feed:kept", Title: "Kept", Host: "kept.example", HomeURL: "https://kept.example/"}, Name: "Kept",
		FeedURL: feed, Article: ArticleSnapshot{ID: "https://kept.example/a", URL: "https://kept.example/a", Title: "A", Authors: []string{},
			Aliases: []string{}, BodyStatus: "not_extracted"}}
	before := lib(t, store)
	state, err := store.Mutate(keep)
	if err != nil {
		t.Fatal(err)
	}
	src := slices.IndexFunc(state.Sources, func(s Source) bool { return s.ID == "feed:kept" })
	if src < 0 || state.Sources[src].Subscribed || state.Sources[src].DisplayName != "Kept" || state.Sources[src].FeedURL == nil || *state.Sources[src].FeedURL != feed {
		t.Fatalf("the source, unsubscribed, with its feed: %+v", state.Sources)
	}
	if len(state.Articles) != len(before.Articles)+1 {
		t.Fatalf("one article more: %d", len(state.Articles))
	}
	if state, err = store.Mutate(keep); err != nil || len(state.Articles) != len(before.Articles)+1 {
		t.Fatalf("kept again, nothing more: %v %d", err, len(state.Articles))
	}
	have := before.Articles[0]
	again := keep
	again.Article.ID, again.Article.URL = "https://elsewhere.example/x", have.URL
	if state, err = store.Mutate(again); err != nil || len(state.Articles) != len(before.Articles)+1 {
		t.Fatalf("an address the library has stays as it is: %v %d", err, len(state.Articles))
	}
	res, err := store.ImportFeed("feed:kept", ParsedFeed{FeedURL: feed, HomeURL: "https://kept.example/", Title: "Kept", FetchedAt: "2026-11-13T00:00:00.000Z",
		Articles: []FeedEntry{{ID: "urn:a", URL: "https://kept.example/a", Title: "A", Authors: []string{}, Aliases: []string{}, BodyStatus: "not_extracted"}}}, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Library.Articles) != len(before.Articles)+1 || !slices.ContainsFunc(res.Library.Articles, func(a Article) bool { return a.ID == "https://kept.example/a" }) {
		t.Fatal("subscribing keeps the article as it was kept")
	}
	if !slices.ContainsFunc(res.Library.Sources, func(s Source) bool { return s.ID == "feed:kept" && s.Subscribed }) {
		t.Fatal("subscribing takes the kept source")
	}
}
