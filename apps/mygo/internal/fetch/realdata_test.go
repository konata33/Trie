package fetch

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"trie/internal/web"
)

// Real reads for the equivalence checks in .context/mygo/feed-port (run.sh).
// Polite: three requests at once and at least 1.1 s between two requests to
// one site, redirect hops included, since every hop resolves the host first.
// Failures are recorded, never retried.

type target struct {
	URL  string `json:"url"`
	Home string `json:"home,omitempty"` // the source's home, for the reader's redirect scope
}

type read struct {
	target
	File     string `json:"file"`
	FinalURL string `json:"finalURL,omitempty"`
	Status   int    `json:"status,omitempty"`
	Type     string `json:"type,omitempty"`
	Bytes    int    `json:"bytes"`
	Error    string `json:"error,omitempty"`
	At       string `json:"at"`
}

var unsafeName = regexp.MustCompile(`[^\w.-]`)

// file names a read by its host and the start of its URL's SHA-1.
func file(url string) string {
	sum := sha1.Sum([]byte(url))
	host := "x"
	if u, err := web.ParseURL(url, nil); err == nil {
		host = u.Hostname()
	}
	return unsafeName.ReplaceAllString(host, "-") + "_" + hex.EncodeToString(sum[:4])
}

func politely(t *testing.T, targets []target, limit, maxBytes int, scope func(target) func(string) bool, store func(read, *Response)) []read {
	var mu sync.Mutex
	last := map[string]time.Time{}
	pace := func(ctx context.Context, host string) ([]netip.Addr, error) {
		key := strings.TrimPrefix(host, "www.")
		for {
			mu.Lock()
			wait := time.Until(last[key].Add(1100 * time.Millisecond))
			if wait <= 0 {
				last[key] = time.Now()
				mu.Unlock()
				return systemResolve(ctx, host)
			}
			mu.Unlock()
			time.Sleep(wait)
		}
	}
	out := make([]read, len(targets))
	next := 0
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			for {
				mu.Lock()
				i := next
				next++
				mu.Unlock()
				if i >= len(targets) {
					return
				}
				tg := targets[i]
				r := read{target: tg, File: file(tg.URL), At: web.Now()}
				c := &Client{limit: limit, timeout: 15 * time.Second, resolve: pace, transport: shared}
				res, err := c.Fetch(context.Background(), tg.URL, scope(tg), maxBytes)
				if err != nil {
					r.Error = err.Error()
					t.Logf("ERR %s %s", tg.URL, err)
				} else {
					r.FinalURL, r.Status, r.Type, r.Bytes = res.URL, res.Status, res.Type, len(res.Body)
					store(r, res)
					t.Logf("%d %8d %s", res.Status, len(res.Body), tg.URL)
				}
				out[i] = r
			}
		})
	}
	wg.Wait()
	return out
}

func readJSON(t *testing.T, path string, v any) {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	data, _ := json.MarshalIndent(v, "", " ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// FEED_PORT_DIR/urls.json → FEED_PORT_DIR/feeds/: each feed read once, as
// subscriptions read it (8 requests, any public redirect, 5 MB), its raw
// body in <file>.body. Feeds already read are not read again.
func TestFetchFeeds(t *testing.T) {
	dir := os.Getenv("FEED_PORT_FETCH")
	if dir == "" {
		t.Skip("FEED_PORT_FETCH is not set (see .context/mygo/feed-port/run.sh)")
	}
	var targets []target
	readJSON(t, filepath.Join(dir, "urls.json"), &targets)
	index := filepath.Join(dir, "feeds", "index.json")
	var done []read
	if _, err := os.Stat(index); err == nil {
		readJSON(t, index, &done)
	}
	have := map[string]bool{}
	for _, r := range done {
		have[r.URL] = true
	}
	var todo []target
	for _, tg := range targets {
		if !have[tg.URL] {
			todo = append(todo, tg)
		}
	}
	reads := politely(t, todo, 8, MaxBytes, func(target) func(string) bool { return anywhere }, func(r read, res *Response) {
		if err := os.WriteFile(filepath.Join(dir, "feeds", r.File+".body"), res.Body, 0o644); err != nil {
			t.Error(err)
		}
	})
	writeJSON(t, index, append(done, reads...))
}

// FETCH_CORPUS=<dir>: the pages <dir>/fetch-targets.json picks from
// extract-spike/corpus.json, read again as the reader reads them (160
// requests, the post's or the home's publisher scope, 2 MB), with what the
// corpus recorded beside what Go read.
func TestCorpus(t *testing.T) {
	dir := os.Getenv("FETCH_CORPUS")
	if dir == "" {
		t.Skip("FETCH_CORPUS is not set (see .context/mygo/feed-port/run.sh)")
	}
	var targets []target
	readJSON(t, filepath.Join(dir, "fetch-targets.json"), &targets)
	decoded := map[string]int{}
	var mu sync.Mutex
	reads := politely(t, targets, 160, 2_000_000, func(tg target) func(string) bool {
		post, _ := PublisherScope(tg.URL)
		home, _ := PublisherScope(tg.Home)
		return func(u string) bool { return post != nil && post(u) || home != nil && home(u) }
	}, func(r read, res *Response) {
		// reader-service.ts decode(), for HTML only, as the corpus stored it; its length in UTF-16 code units.
		n := 0
		if strings.Contains(strings.ToLower(res.Type), "html") {
			n = web.JSLen(Decode(res.Body, Charset(res.Type)))
		}
		mu.Lock()
		decoded[r.URL] = n
		mu.Unlock()
	})
	type row struct {
		read
		Decoded int `json:"decoded"`
	}
	rows := []row{}
	for _, r := range reads {
		rows = append(rows, row{r, decoded[r.URL]})
	}
	writeJSON(t, filepath.Join(dir, "fetch-go.json"), rows)
}
