package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"trie/internal/feed"
	"trie/internal/fetch"
)

// FEED_PORT_DIR (.context/mygo/feed-port, run.sh): the Go side of the feed
// equivalence check, the counterpart of ts/replay.mjs. Every stored read goes
// through FetchFeed with a fetch that replays it, every synthetic case
// through Parse, DiscoverFeeds and DiscoverIcons; out/go.json is written in
// the shape of out/ts.json.
func TestReplayFeeds(t *testing.T) {
	dir := os.Getenv("FEED_PORT_DIR")
	if dir == "" {
		t.Skip("FEED_PORT_DIR is not set (see .context/mygo/feed-port/run.sh)")
	}
	read := func(name string, v any) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, v); err != nil {
			t.Fatal(err)
		}
	}
	var index []struct {
		URL, File, FinalURL, Type, Error string
		Status                           int
	}
	read("feeds/index.json", &index)
	stored := map[string]int{}
	for i, r := range index {
		if r.Error == "" {
			stored[r.URL] = i
		}
	}
	replay := func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		i, ok := stored[url]
		if !ok {
			return nil, errors.New("not stored: " + url)
		}
		body, err := os.ReadFile(filepath.Join(dir, "feeds", index[i].File+".body"))
		if err != nil {
			return nil, err
		}
		return &fetch.Response{URL: index[i].FinalURL, Status: index[i].Status, Type: index[i].Type, Body: body}, nil
	}
	s := New(nil, func() fetch.Fetch { return replay }, nil, nil)
	type settled struct {
		Value any    `json:"value,omitempty"`
		Error string `json:"error,omitempty"`
	}
	settle := func(v any, err error) settled {
		if err != nil {
			return settled{Error: err.Error()}
		}
		return settled{Value: v}
	}
	out := struct {
		Feeds map[string]settled `json:"feeds"`
		Cases map[string]any     `json:"cases"`
	}{map[string]settled{}, map[string]any{}}
	for _, r := range index {
		f, err := s.FetchFeed(context.Background(), r.URL)
		f.FetchedAt = ""
		out.Feeds[r.URL] = settle(f, err)
	}
	var cases []struct {
		Name, URL string
		XML, HTML *string
	}
	read("cases.json", &cases)
	for _, c := range cases {
		if c.HTML != nil {
			feeds, err := feed.DiscoverFeeds(*c.HTML, c.URL)
			out.Cases[c.Name] = map[string]settled{"feeds": settle(feeds, err), "icons": settle(feed.DiscoverIcons(*c.HTML, c.URL), nil)}
			continue
		}
		f, err := feed.Parse(*c.XML, c.URL)
		f.FetchedAt = ""
		out.Cases[c.Name] = settle(f, err)
	}
	data, _ := json.Marshal(out)
	if err := os.WriteFile(filepath.Join(dir, "out", "go.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
