package explore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/png"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "golang.org/x/image/webp"

	"trie/internal/extract"
	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/reading"
	"trie/internal/subscription"
)

// TestReplay is the Go side of .context/mygo/explore-port's equivalence check (ts.mjs is the TS side): Service wired
// as Open wires it, from the tape the TS side recorded, the clock fixed at the recording's start, in an empty data
// folder over a copy of the library; the same flow as ts.mjs, written to out/go/. When out/ts holds the TS side's icons,
// out/icons.json compares them by their pixels. run.sh runs it:
//
//	EXPLORE_PORT=<absolute path of .context/mygo/explore-port> go test -run TestReplay -count=1 ./internal/explore
func TestReplay(t *testing.T) {
	dir := os.Getenv("EXPLORE_PORT")
	if dir == "" {
		t.Skip("EXPLORE_PORT is not set")
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
	data, err := os.ReadFile(filepath.Join(dir, "tape", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &tape); err != nil {
		t.Fatal(err)
	}
	stamp, err := os.ReadFile(filepath.Join(dir, "tape", "now.txt"))
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(stamp)))
	if err != nil {
		t.Fatal(err)
	}
	now = func() time.Time { return fixed }
	defer func() { now = time.Now }()
	work := t.TempDir()
	path := filepath.Join(work, "library.sqlite")
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(filepath.Join(dir, "..", "library", "library.sqlite"+suffix))
		if err != nil && suffix == "" {
			t.Fatal(err)
		}
		if err == nil {
			write(t, path+suffix, data)
		}
	}
	store, err := library.Open(path, library.Snapshot{Version: 1, AsOf: "2026-11-10", Sources: []library.SourceSnapshot{}, Articles: []library.ArticleSnapshot{}, ReviewItems: []library.ReviewItem{}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var mu sync.Mutex
	requests, misses := map[string]bool{}, map[string]bool{}
	client := func(limit int) func() fetch.Fetch {
		return func() fetch.Fetch {
			var used atomic.Int32
			return func(_ context.Context, url string, scope func(string) bool, maxBytes int) (*fetch.Response, error) {
				key := strconv.Itoa(maxBytes) + " " + url
				mu.Lock()
				requests[key] = true
				e, ok := tape[key]
				if !ok {
					misses[key] = true
				}
				mu.Unlock()
				if !ok {
					return nil, errors.New("not on the tape")
				}
				for _, hop := range e.Hops {
					if !scope(hop) {
						return nil, errors.New("重定向超出来源范围或形成循环")
					}
					if used.Add(1) > int32(limit) {
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
	}
	subs := subscription.New(store, client(8), nil, nil)
	reader := reading.New(store, client(160), reading.Worker)
	var ops atomic.Int32
	var kinds sync.Map
	count := func(kind string) {
		ops.Add(1)
		n, _ := kinds.LoadOrStore(kind, new(atomic.Int32))
		n.(*atomic.Int32).Add(1)
	}
	s := New(Options{Resources: filepath.Join("..", "..", "resources", "explore"), Cache: filepath.Join(work, "data", "explore-cache.json"),
		FetchFeed: func(ctx context.Context, url string) (library.ParsedFeed, error) {
			count("feed")
			return subs.FetchFeed(ctx, url)
		},
		Sources: func() []library.Source {
			st, err := store.Library()
			if err != nil {
				return nil
			}
			return st.Sources
		},
		Upstreams: Upstreams,
		FindIcon: func(ctx context.Context, home string) (*string, error) {
			count("icon")
			return findIcon(ctx, client(12)(), home)
		},
		// Open's readPage.
		ReadPage: func(ctx context.Context, url, homeURL string) string {
			count("page")
			c := reader.Preview(ctx, reading.Target{ID: "explore:" + url, URL: url, Aliases: []string{}, HomeURL: homeURL, TrustedArticle: true}, "auto", 0)
			if c.Status == "ready" && c.TextLength > thinText {
				return c.HTML
			}
			return ""
		}})
	ctx := context.Background()
	rounds := 0
	for before := int32(-1); before != ops.Load() && rounds < 20; rounds++ {
		before = ops.Load()
		if _, err := s.List(ctx); err != nil {
			t.Fatal(err)
		}
		s.bg.Wait()
		so := map[string]int32{}
		kinds.Range(func(k, v any) bool { so[k.(string)] = v.(*atomic.Int32).Load(); return true })
		t.Logf("round %d: %d reads (%v so far)", rounds+1, ops.Load()-before, so)
	}
	entries, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	type target struct {
		Entry  string `json:"entry"`
		URL    string `json:"url"`
		Target any    `json:"target"`
	}
	var targets []target
	for _, e := range entries {
		for _, p := range append(slices.Clone(e.Posts), e.Works...) {
			var out any
			if t, err := s.ReadTarget(ctx, e.ID, p.URL); err != nil {
				out = map[string]string{"error": err.Error()}
			} else {
				out = t
			}
			targets = append(targets, target{e.ID, p.URL, out})
		}
	}
	type site struct {
		HomeURL  string   `json:"homeURL"`
		Host     string   `json:"host"`
		Writer   bool     `json:"writer"`
		Pictures []string `json:"pictures"`
	}
	works, sites := map[string][]*string{}, map[string]*site{}
	orNil := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	for _, e := range entries {
		if found, _ := s.site(e.ID); found != nil {
			sites[e.ID] = &site{found.HomeURL, found.Host, found.Writer, found.Pictures}
		} else {
			sites[e.ID] = nil
		}
		if len(e.Works) > 0 {
			for i := range 8 {
				src, _ := s.workImage(e.ID, i)
				works[e.ID] = append(works[e.ID], orNil(src))
			}
		}
	}
	links := slices.Sorted(slices.Values(s.links()))
	links = slices.Compact(links)
	var design []map[string]string
	for _, d := range s.designSites() {
		design = append(design, map[string]string{"homeURL": d.HomeURL, "host": d.Host})
	}
	names := map[string]map[string]*string{}
	st, err := store.Library()
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range st.Sources {
		names[src.ID] = map[string]*string{"name": orNil(s.NameFor(src.FeedURL, src.HomeURL)), "icon": s.IconFor(src.HomeURL), "intro": orNil(s.Intro(src))}
	}
	s.bg.Wait()
	out := filepath.Join(dir, "out", "go")
	if err := os.RemoveAll(out); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Icon != nil && (strings.HasPrefix(e.ID, "pick:") || strings.HasPrefix(e.ID, "featured:")) {
			mime, raw := decodeDataURL(t, *e.Icon)
			write(t, filepath.Join(out, "icons", iconName(e.ID)+"."+strings.TrimPrefix(mime, "image/")), raw)
		}
	}
	var cache, icons any
	for file, into := range map[string]*any{"explore-cache.json": &cache, "explore-icons.json": &icons} {
		if data, err := os.ReadFile(filepath.Join(work, "data", file)); err == nil {
			if err := json.Unmarshal(data, into); err != nil {
				t.Fatal(err)
			}
		}
	}
	if icons == nil {
		icons = map[string]any{}
	}
	keys := func(m map[string]bool) []string { return slices.Sorted(maps.Keys(m)) }
	for name, value := range map[string]any{
		"list.json": entries, "targets.json": shortBodies(targets), "state.json": map[string]any{"rounds": rounds, "reads": ops.Load(), "cache": cache, "icons": icons,
			"requests": keys(requests), "misses": keys(misses)},
		"extra.json": map[string]any{"works": works, "sites": sites, "links": links, "designSites": design, "names": names},
	} {
		data, err := json.MarshalIndent(value, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(out, name), []byte(digestData(string(data))+"\n"))
	}
	t.Logf("%d rounds, %d reads, %d requests, %d not on the tape", rounds, ops.Load(), len(requests), len(misses))
	compareIcons(t, dir)
}

var dataURLs = regexp.MustCompile(`data:([\w/+.-]+);base64,([A-Za-z0-9+/=]+)`)

// digestData writes every data: URL as its digest, as ts.mjs does.
func digestData(text string) string {
	return dataURLs.ReplaceAllStringFunc(text, func(m string) string {
		parts := dataURLs.FindStringSubmatch(m)
		sum := sha256.Sum256([]byte(parts[2]))
		return "data:" + parts[1] + ";sha256=" + hex.EncodeToString(sum[:])[:16]
	})
}

func decodeDataURL(t *testing.T, url string) (string, []byte) {
	t.Helper()
	mime, data, ok := strings.Cut(strings.TrimPrefix(url, "data:"), ";base64,")
	raw, err := base64.StdEncoding.DecodeString(data)
	if !ok || err != nil {
		t.Fatalf("not a data URL: %.40s", url)
	}
	return mime, raw
}

var unsafeID = regexp.MustCompile(`[^\w.-]+`)

func iconName(id string) string { return unsafeID.ReplaceAllString(id, "_") }

// shortBodies keeps a target's feed body as its length and digest: the bodies are the feeds' own, compared whole.
func shortBodies(targets any) any {
	data, _ := json.Marshal(targets)
	var v []map[string]any
	json.Unmarshal(data, &v)
	for _, t := range v {
		if target, ok := t["target"].(map[string]any); ok {
			if body, ok := target["feedBody"].(map[string]any); ok {
				html, _ := body["html"].(string)
				sum := sha256.Sum256([]byte(html))
				body["html"] = map[string]any{"length": len(html), "sha256": hex.EncodeToString(sum[:])[:16]}
			}
		}
	}
	return v
}

// compareIcons compares the icons both sides drew, by their pixels: nativeImage and Go encode PNGs and shrink pictures
// differently. An icon that goes as it came is compared by its bytes.
func compareIcons(t *testing.T, dir string) {
	tsDir, goDir := filepath.Join(dir, "out", "ts", "icons"), filepath.Join(dir, "out", "go", "icons")
	names := map[string]bool{}
	for _, d := range []string{tsDir, goDir} {
		files, _ := os.ReadDir(d)
		for _, f := range files {
			names[f.Name()] = true
		}
	}
	if len(names) == 0 {
		return
	}
	type side struct {
		File          string `json:"file,omitempty"`
		Bytes         int    `json:"bytes,omitempty"`
		Width, Height int    `json:"-"`
		Size          string `json:"size,omitempty"`
	}
	type row struct {
		ID      string  `json:"id"`
		TS      *side   `json:"ts"`
		Go      *side   `json:"go"`
		Same    bool    `json:"sameBytes"`
		Mean    float64 `json:"meanDiff"`
		Max     int     `json:"maxDiff"`
		Verdict string  `json:"verdict"`
	}
	read := func(d, base string) (*side, []byte, image.Image) {
		files, _ := filepath.Glob(filepath.Join(d, base+".*"))
		if len(files) == 0 {
			return nil, nil, nil
		}
		data, _ := os.ReadFile(files[0])
		s := &side{File: filepath.Base(files[0]), Bytes: len(data)}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err == nil {
			s.Width, s.Height = img.Bounds().Dx(), img.Bounds().Dy()
			s.Size = strconv.Itoa(s.Width) + "x" + strconv.Itoa(s.Height)
		} else if extract.ImageType(data) == "image/png" {
			s.Size = "unreadable"
		}
		return s, data, img
	}
	bases := map[string]bool{}
	for n := range names {
		bases[strings.TrimSuffix(n, filepath.Ext(n))] = true
	}
	var rows []row
	for _, base := range slices.Sorted(maps.Keys(bases)) {
		ts, tsData, tsImg := read(tsDir, base)
		gs, goData, goImg := read(goDir, base)
		r := row{ID: base, TS: ts, Go: gs, Same: ts != nil && gs != nil && bytes.Equal(tsData, goData)}
		switch {
		case ts == nil || gs == nil:
			r.Verdict = "only one side has it"
		case r.Same:
			r.Verdict = "same bytes"
		case tsImg == nil || goImg == nil:
			r.Verdict = "different bytes, not decodable"
		case tsImg.Bounds().Size() != goImg.Bounds().Size():
			r.Verdict = "different sizes"
		default:
			r.Mean, r.Max = pixelDiff(tsImg, goImg)
			r.Verdict = "same size, pixels differ by mean " + strconv.FormatFloat(r.Mean, 'f', 2, 64)
		}
		rows = append(rows, r)
	}
	data, _ := json.MarshalIndent(rows, "", " ")
	write(t, filepath.Join(dir, "out", "icons.json"), data)
}

// pixelDiff is the mean and largest difference of two pictures' channels, premultiplied by their alpha (0 to 255), so
// that the colour of a pixel all but transparent counts as little as it shows.
func pixelDiff(a, b image.Image) (float64, int) {
	pa, pb := image.NewRGBA(a.Bounds()), image.NewRGBA(b.Bounds())
	draw.Draw(pa, pa.Bounds(), a, a.Bounds().Min, draw.Src)
	draw.Draw(pb, pb.Bounds(), b, b.Bounds().Min, draw.Src)
	total, most := 0, 0
	for i := range pa.Pix {
		d := int(pa.Pix[i]) - int(pb.Pix[i])
		if d < 0 {
			d = -d
		}
		total += d
		most = max(most, d)
	}
	return float64(total) / float64(len(pa.Pix)), most
}
