package reading

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trie/internal/fetch"
	"trie/internal/library"
)

// TestReplay is the Go side of .context/mygo/reading-port's equivalence check: every case of cases.json read through
// Service with Worker's extraction, from the tape the TS side recorded, on a copy of the library with its bodies
// removed; out/go/<case>.json is what compare.py compares with ts.ts's out/ts/. run.sh runs it:
//
//	READING_PORT=<absolute path of .context/mygo/reading-port> go test -run TestReplay -count=1 ./internal/reading
//
// The tape is replayed as ts.ts replays it: each recorded redirect hop asked of the reader's scope and counted against
// the read's budget of 160, a recorded failure returned as the error ReaderHTTP gave (ECONNRESET and EAI_AGAIN as
// transient), a request not on the tape failed and listed.
func TestReplay(t *testing.T) {
	dir := os.Getenv("READING_PORT")
	if dir == "" {
		t.Skip("READING_PORT is not set")
	}
	type entry struct {
		Hops     []string `json:"hops"`
		Status   int      `json:"status"`
		Type     string   `json:"type"`
		FinalURL string   `json:"finalURL"`
		Body     string   `json:"body"`
		Error    string   `json:"error"`
		Code     string   `json:"code"`
	}
	var tape map[string]entry
	var cases struct {
		Cases []struct {
			Name   string `json:"name"`
			Kind   string `json:"kind"`
			ID     string `json:"id"`
			Target Target `json:"target"`
		} `json:"cases"`
	}
	for file, into := range map[string]any{"tape/index.json": &tape, "cases.json": &cases} {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, into); err != nil {
			t.Fatal(err)
		}
	}
	// The library copy, bodies removed.
	path := filepath.Join(t.TempDir(), "library.sqlite")
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(filepath.Join(dir, "..", "library", "library.sqlite"+suffix))
		if err != nil && suffix == "" {
			t.Fatal(err)
		}
		if err == nil {
			noErr(t, os.WriteFile(path+suffix, data, 0o600))
		}
	}
	raw := must[*sql.DB](t)(sql.Open("sqlite", path))
	defer raw.Close()
	must[sql.Result](t)(raw.Exec("DELETE FROM article_content"))
	store := open(t, path, library.Snapshot{Version: 1, AsOf: "2026-11-09", Sources: []library.SourceSnapshot{}, Articles: []library.ArticleSnapshot{}, ReviewItems: []library.ReviewItem{}})
	stored := func(id string) json.RawMessage {
		var data string
		if err := raw.QueryRow("SELECT data FROM article_content WHERE articleID = ?", id).Scan(&data); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatal(err)
			}
			return json.RawMessage("null")
		}
		return json.RawMessage(data)
	}
	noErr(t, os.MkdirAll(filepath.Join(dir, "out", "go"), 0o755))
	for _, c := range cases.Cases {
		var mu sync.Mutex
		requests, misses := [][2]any{}, []string{}
		fetcher := func() fetch.Fetch {
			var used atomic.Int32
			return func(_ context.Context, url string, scope func(string) bool, maxBytes int) (*fetch.Response, error) {
				key := strconv.Itoa(maxBytes) + " " + url
				mu.Lock()
				requests = append(requests, [2]any{maxBytes, url})
				e, ok := tape[key]
				if !ok {
					misses = append(misses, key)
				}
				mu.Unlock()
				if !ok {
					return nil, errors.New("not on the tape")
				}
				for _, hop := range e.Hops {
					if !scope(hop) {
						return nil, errors.New("重定向超出来源范围或形成循环")
					}
					if used.Add(1) > 160 {
						return nil, errors.New("本篇文章的读取额度已用完")
					}
				}
				if e.Error != "" {
					err := errors.New(e.Error)
					switch e.Code {
					case "":
						return nil, err
					case "ECONNRESET", "EAI_AGAIN":
						return nil, &fetch.NetError{Err: err, Transient: true}
					default:
						return nil, &fetch.NetError{Err: err}
					}
				}
				body, err := os.ReadFile(filepath.Join(dir, "tape", "bodies", e.Body))
				if err != nil {
					return nil, err
				}
				return &fetch.Response{URL: e.FinalURL, Status: e.Status, Type: e.Type, Body: body}, nil
			}
		}
		reader := New(store, fetcher, Worker)
		type step struct {
			MS       float64         `json:"ms"`
			Returned json.RawMessage `json:"returned"`
			Stored   json.RawMessage `json:"stored"`
		}
		var steps []step
		run := func(work func() (library.ArticleContent, error)) {
			start := time.Now()
			got, err := work()
			var returned any = got
			if err != nil {
				returned = map[string]string{"error": err.Error()}
			}
			s := step{MS: float64(time.Since(start).Microseconds()) / 1000, Returned: plainJSON(t, returned), Stored: json.RawMessage("null")}
			if c.Kind == "read" {
				s.Stored = stored(c.ID)
			}
			steps = append(steps, s)
		}
		ctx := context.Background()
		if c.Kind == "read" {
			run(func() (library.ArticleContent, error) { return reader.Read(ctx, c.ID, false, "") })
			run(func() (library.ArticleContent, error) { return reader.Read(ctx, c.ID, true, "") })
		} else {
			run(func() (library.ArticleContent, error) { return reader.Preview(ctx, c.Target, "", PreviewWait), nil })
			run(func() (library.ArticleContent, error) { return reader.Preview(ctx, c.Target, "page", PreviewWait), nil })
		}
		for i := range steps {
			steps[i].Returned, steps[i].Stored = digestData(steps[i].Returned), digestData(steps[i].Stored)
		}
		out := plainJSON(t, map[string]any{"name": c.Name, "requests": requests, "misses": misses, "steps": steps})
		noErr(t, os.WriteFile(filepath.Join(dir, "out", "go", c.Name+".json"), out, 0o644))
		t.Logf("%s: %d requests, %d not on the tape, %.0f+%.0f ms", c.Name, len(requests), len(misses), steps[0].MS, steps[1].MS)
	}
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// plainJSON is JSON.stringify's text: no HTML escaping.
func plainJSON(t *testing.T, v any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSpace(b.Bytes())
}

var dataURLs = regexp.MustCompile(`data:([\w/+.-]+);base64,([A-Za-z0-9+/=]+)`)

// digestData is ts.ts's digestData: each data: URL as its type and a digest of its text.
func digestData(text json.RawMessage) json.RawMessage {
	return dataURLs.ReplaceAllFunc(text, func(m []byte) []byte {
		parts := dataURLs.FindSubmatch(m)
		sum := sha256.Sum256(parts[2])
		return []byte("data:" + string(parts[1]) + ";sha256=" + hex.EncodeToString(sum[:])[:16])
	})
}
