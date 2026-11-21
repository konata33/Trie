package explore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"trie/internal/extract"
	"trie/internal/feed"
	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/reading"
)

// TestHealth reads the latest posts of every catalogue entry live, as Explore opens them, and records for each what the
// publisher's page answered and what the reader made of it, into HEALTH_OUT. HEALTH_N posts per entry (5).
func TestHealth(t *testing.T) {
	out := os.Getenv("HEALTH_OUT")
	if out == "" {
		t.Skip("HEALTH_OUT is not set")
	}
	n := 5
	if v, err := strconv.Atoi(os.Getenv("HEALTH_N")); err == nil {
		n = v
	}
	only := os.Getenv("HEALTH_ONLY")
	data, err := os.ReadFile("../../resources/explore/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog []catalogEntry
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(out, "html"), 0o755)
	reader := reading.New(nil, nil, reading.Worker)
	type work struct {
		Poster, Video bool
		Shots, Facts  int
		Site          string
	}
	type row struct {
		Entry, Section, Kind, URL, Title, Error string
		FeedKind                                string
		FeedText, FeedImages                    int
		PageStatus                              int
		PageType, PageError, PageExtract        string
		PageText, PageImages                    int
		PageLead                                bool
		Status, Source, Message, RefreshError   string
		Text, Images, Missing                   int
		Work                                    *work
		MS                                      int64
		File                                    string
	}
	var mu sync.Mutex
	var rows []row
	tags := regexp.MustCompile(`<[^>]+>`)
	imgs := regexp.MustCompile(`<img\b`)
	dataURL := regexp.MustCompile(`data:[^"' )]+`)
	keptPicture := regexp.MustCompile(`data:image/[a-z+]+;base64,[A-Za-z0-9+/=]+`)
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, e := range catalog {
		if only != "" && !slices.Contains(strings.Split(only, ","), e.ID) {
			continue
		}
		if e.feed() == "" {
			rows = append(rows, row{Entry: e.ID, Section: e.Section, Kind: e.Kind, Error: "no feed"})
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			res, err := tries(e.feed(), 4_000_000)
			var parsed library.ParsedFeed
			if err == nil && res.Status != 200 {
				err = fmt.Errorf("HTTP %d", res.Status)
			}
			if err == nil {
				parsed, err = feed.Parse(string(res.Body), res.URL)
			}
			if err != nil {
				mu.Lock()
				rows = append(rows, row{Entry: e.ID, Section: e.Section, Kind: e.Kind, Error: "feed: " + err.Error()})
				mu.Unlock()
				return
			}
			for i, p := range parsed.Articles[:min(n, len(parsed.Articles))] {
				r := row{Entry: e.ID, Section: e.Section, Kind: e.Kind, URL: p.URL, Title: p.Title}
				if p.FeedBody != nil {
					r.FeedKind = p.FeedBody.Kind
					r.FeedText = len([]rune(strings.Join(strings.Fields(tags.ReplaceAllString(p.FeedBody.HTML, " ")), " ")))
					r.FeedImages = len(imgs.FindAllString(p.FeedBody.HTML, -1))
				}
				// What the page itself answers, read once on its own.
				page, err := tries(p.URL, 2_000_000)
				if err != nil {
					r.PageError = err.Error()
				} else {
					r.PageStatus, r.PageType = page.Status, page.Type
					if page.Status == 200 && strings.Contains(strings.ToLower(page.Type), "html") {
						x := extract.ExtractPage(string(page.Body), page.URL, true)
						r.PageExtract, r.PageText, r.PageImages, r.PageLead = x.Status, x.TextLength, len(x.Images), x.Lead != ""
					}
				}
				target := reading.Target{ID: "explore:" + p.URL, URL: p.URL, Aliases: p.Aliases, HomeURL: e.HomeURL, FeedBody: p.FeedBody, TrustedArticle: true}
				if target.Aliases == nil {
					target.Aliases = []string{}
				}
				if e.gallery() {
					target.Work = &reading.Card{Name: p.Title, Site: featuredSite(p, e.HomeURL), Picture: firstImage(p)}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
				start := time.Now()
				c := reader.Preview(ctx, target, "", 30*time.Second)
				cancel()
				r.MS = time.Since(start).Milliseconds()
				r.Status, r.Source, r.Text, r.Images = c.Status, c.SourceKind, c.TextLength, len(imgs.FindAllString(c.HTML, -1))
				if c.MissingImageCount != nil {
					r.Missing = *c.MissingImageCount
				}
				if c.Message != nil {
					r.Message = *c.Message
				}
				if c.RefreshError != nil {
					r.RefreshError = *c.RefreshError
				}
				if len(c.Work) > 0 && string(c.Work) != "null" {
					var w library.GalleryWork
					_ = json.Unmarshal(c.Work, &w)
					r.Work = &work{Poster: w.Poster != nil || w.PosterSource.Value != nil, Video: w.Video != nil || w.VideoSource.Value != nil, Shots: len(w.Sections), Facts: len(w.Facts)}
					if w.Host != nil {
						r.Work.Site = *w.Host
					}
				}
				r.File = fmt.Sprintf("%s-%d", e.ID, i)
				_ = os.WriteFile(filepath.Join(out, "html", r.File+".html"), []byte("<!-- "+p.URL+" -->\n"+dataURL.ReplaceAllString(c.HTML, "data:")), 0o644)
				// The body as kept, its pictures replaced by one small picture, to lay out again offline.
				_ = os.WriteFile(filepath.Join(out, "html", r.File+".kept.html"), []byte(keptPicture.ReplaceAllString(c.HTML, tinyPNG)), 0o644)
				t.Logf("%s | %.40s | page %d %s %d chars %d img | read %s/%s %d chars %d img %d missing work=%v %dms", e.ID, p.Title, r.PageStatus, r.PageExtract, r.PageText, r.PageImages, r.Status, r.Source, r.Text, r.Images, r.Missing, r.Work != nil, r.MS)
				mu.Lock()
				rows = append(rows, r)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	data, _ = json.MarshalIndent(rows, "", " ")
	if err := os.WriteFile(filepath.Join(out, "health.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// tries reads url as a read would, up to four times a few seconds apart: a proxy drops new connections in bursts.
func tries(url string, limit int) (*fetch.Response, error) {
	var res *fetch.Response
	var err error
	for i := range 4 {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		res, err = fetch.New(20).Fetch(ctx, url, func(string) bool { return true }, limit)
		cancel()
		if err == nil {
			return res, nil
		}
		time.Sleep(time.Duration(2+3*i) * time.Second)
	}
	return res, err
}

// tinyPNG is a 1×1 picture standing in for a kept one.
const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j5T8AAAAASUVORK5CYII="
