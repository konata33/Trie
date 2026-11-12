package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
)

// bigPage is the biggest cached page of the real library: 16 MB, 17 pictures inlined as data URLs.
const bigPage = "https://innei.in/notes/220"

var perfSeed = Snapshot{Version: 1, AsOf: "2026-11-09", Sources: []SourceSnapshot{}, Articles: []ArticleSnapshot{}, ReviewItems: []ReviewItem{}}

// previewCheck is what TestPreviewsUnchanged keeps of an article: the preview row, and the text passes over the whole page.
type previewCheck struct {
	Stored   string   `json:"stored"`
	Minutes  int      `json:"minutes"`
	Text     string   `json:"text"`
	Lead     string   `json:"lead"`
	Links    int      `json:"links"`
	Pictures []string `json:"pictures"`
	Shown    []string `json:"shown"`
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestPreviewsUnchanged compares the preview of every article of a library copy with what an earlier build made of it:
// TRIE_LIBRARY_COPY names the library (only ever copied), TRIE_PREVIEW_GOLDEN the earlier build's output,
// written instead of read when TRIE_PREVIEW_UPDATE=1. .context/mygo/library-perf/run.sh sets them.
func TestPreviewsUnchanged(t *testing.T) {
	source, golden := os.Getenv("TRIE_LIBRARY_COPY"), os.Getenv("TRIE_PREVIEW_GOLDEN")
	if source == "" || golden == "" {
		t.Skip("set TRIE_LIBRARY_COPY and TRIE_PREVIEW_GOLDEN (see .context/mygo/library-perf/run.sh)")
	}
	s := mustOpen(t, copyLibrary(t, source), perfSeed)
	ids, err := s.all("SELECT id FROM articles")
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	for _, id := range ids {
		if err := s.refreshPreview(id[0].(string)); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Unlock()
	rows, err := s.all(`SELECT a.id, json_extract(a.data, '$.url') AS url,
      CASE WHEN json_extract(c.data, '$.status') = 'ready' THEN json_extract(c.data, '$.html') END AS content,
      json_extract(f.data, '$.html') AS feed, json_extract(f.data, '$.baseURL') AS baseURL, p.data
      FROM articles a LEFT JOIN article_content c ON c.articleID = a.id LEFT JOIN article_feed_content f ON f.articleID = a.id
      LEFT JOIN article_previews p ON p.articleID = a.id`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*previewCheck{}
	for _, row := range rows {
		c := &previewCheck{}
		got[row[0].(string)] = c
		for i := range 4 {
			shown, err := s.Picture(row[0].(string), i)
			if err != nil {
				t.Fatal(err)
			}
			c.Shown = append(c.Shown, digest(shown))
		}
		c.Stored, _ = row[5].(string)
		html, fromContent := row[2].(string)
		if !fromContent {
			html, _ = row[3].(string)
		}
		base, ok := row[4].(string)
		if fromContent || !ok {
			base = fmt.Sprint(row[1])
		}
		c.Minutes, c.Text, c.Links, c.Pictures = readingMinutes(html), digest(plainText(html)), linkText(html), pictures(shorten(html), base, 1000)
		if lead, ok := leadText(html); ok {
			c.Lead = digest(lead)
		}
	}
	if os.Getenv("TRIE_PREVIEW_UPDATE") == "1" {
		data, err := json.MarshalIndent(got, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, data, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d articles to %s", len(got), golden)
		return
	}
	data, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]*previewCheck
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	var differ []string
	for id := range want {
		if !reflect.DeepEqual(got[id], want[id]) {
			differ = append(differ, id)
		}
	}
	for id := range got {
		if want[id] == nil {
			differ = append(differ, id)
		}
	}
	sort.Strings(differ)
	for _, id := range differ[:min(5, len(differ))] {
		t.Errorf("%s\n got  %+v\n want %+v", id, got[id], want[id])
	}
	if len(differ) > 0 {
		t.Fatalf("%d of %d articles differ", len(differ), len(want))
	}
	// Saving a cached body again leaves its preview as it was.
	saved, err := s.all("SELECT articleID FROM article_content")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range saved {
		id := row[0].(string)
		c, err := s.Content(id)
		if err != nil || c == nil {
			t.Fatal(id, err)
		}
		if err := s.SaveContent(*c); err != nil {
			t.Fatal(id, err)
		}
		if p, err := s.get("SELECT data FROM article_previews WHERE articleID = ?", id); err != nil || p == nil || p[0] != want[id].Stored {
			t.Fatalf("%s saved again: %v %v, want %s", id, p, err, want[id].Stored)
		}
	}
	t.Logf("%d articles, all the same; %d bodies saved again, the same", len(want), len(saved))
}

// bigContent is a store on a copy of TRIE_LIBRARY_COPY and bigPage's cached content and title.
func bigContent(b *testing.B) (*Store, ArticleContent, string) {
	source := os.Getenv("TRIE_LIBRARY_COPY")
	if source == "" {
		b.Skip("set TRIE_LIBRARY_COPY (see .context/mygo/library-perf/run.sh)")
	}
	s := mustOpen(b, copyLibrary(b, source), perfSeed)
	c, err := s.Content(bigPage)
	if err != nil || c == nil {
		b.Fatal("no cached content for ", bigPage, err)
	}
	state, err := s.Library()
	if err != nil {
		b.Fatal(err)
	}
	for _, a := range state.Articles {
		if a.ID == bigPage {
			return s, *c, a.Title
		}
	}
	b.Fatal("no article ", bigPage)
	return nil, ArticleContent{}, ""
}

func BenchmarkPreview(b *testing.B) {
	_, c, title := bigContent(b)
	for b.Loop() {
		articlePreview(c.HTML, title, bigPage, false, nil)
	}
}

func BenchmarkContent(b *testing.B) {
	s, _, _ := bigContent(b)
	for b.Loop() {
		if _, err := s.Content(bigPage); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSaveContent(b *testing.B) {
	s, c, _ := bigContent(b)
	for b.Loop() {
		if err := s.SaveContent(c); err != nil {
			b.Fatal(err)
		}
	}
}
