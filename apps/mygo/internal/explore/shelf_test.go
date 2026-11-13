package explore

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// A gallery's works and its sections' are one list, each work once, newest
// first, shelved under the kinds its sections and its titles give it.
func TestShelves(t *testing.T) {
	day := func(d string) *string { v := "2026-11-" + d + "T08:00:00.000Z"; return &v }
	main := "https://gallery.example/feed"
	s := &Service{stats: map[string]*stats{
		main:                                   {Works: []work{{Title: "Branding: Alpha", URL: "a", PublishedAt: day("03")}, {Title: "Web Design: Beta", URL: "b", PublishedAt: day("02")}}},
		"https://gallery.example/landing/feed": {Works: []work{{Title: "Gamma", URL: "c", PublishedAt: day("04")}, {Title: "Web Design: Beta", URL: "b", PublishedAt: day("02")}}},
	}}
	e := catalogEntry{FeedURL: &main, Kinds: []Shelf{{Kind: "brand", Match: "branding"}, {Kind: "landing", Feed: "https://gallery.example/landing/feed"}}}
	var got []string
	for _, w := range s.works(e) {
		got = append(got, w.URL+" "+strings.TrimPrefix(w.feed, "https://gallery.example/")+" "+strings.Join(w.kinds, ","))
	}
	if want := []string{"c landing/feed landing", "a feed brand", "b feed landing"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The catalog names each site once and shelves only under the five kinds.
func TestCatalogShelves(t *testing.T) {
	data, err := os.ReadFile("../../resources/explore/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var c []catalogEntry
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range c {
		if seen[e.ID] {
			t.Errorf("%s twice", e.ID)
		}
		seen[e.ID] = true
		for _, sh := range e.Kinds {
			if !slices.Contains([]string{"landing", "portfolio", "motion", "type", "brand"}, sh.Kind) || !e.gallery() {
				t.Errorf("%s shelves %q", e.ID, sh.Kind)
			}
		}
	}
}
