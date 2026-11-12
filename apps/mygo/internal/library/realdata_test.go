package library

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
	"trie/internal/web"
)

// TestRealLibrary compares the Go store with the Electron one on a copy of a
// real library: TRIE_LIBRARY_COPY names the library (only ever copied),
// TRIE_TS_DUMP what .context/mygo/data-layer/dump.ts made of it.
// .context/mygo/data-layer/run.sh sets both.
func TestRealLibrary(t *testing.T) {
	source, dumpPath := os.Getenv("TRIE_LIBRARY_COPY"), os.Getenv("TRIE_TS_DUMP")
	if source == "" || dumpPath == "" {
		t.Skip("set TRIE_LIBRARY_COPY and TRIE_TS_DUMP (see .context/mygo/data-layer/run.sh)")
	}
	ts := readDump(t, dumpPath)
	seed := Snapshot{Version: 1, AsOf: "2026-11-09", Sources: []SourceSnapshot{}, Articles: []ArticleSnapshot{}, ReviewItems: []ReviewItem{}}
	s := mustOpen(t, copyLibrary(t, source), seed)

	started := time.Now()
	state := lib(t, s)
	t.Logf("Library() took %v", time.Since(started).Round(time.Millisecond))
	goDump := map[string]any{"library": state}
	content, feeds, shown, all := map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}
	for _, a := range state.Articles {
		content[a.ID] = attempt(s.Content(a.ID))
		feeds[a.ID] = attempt(s.FeedContent(a.ID))
		var list []any
		for i := range 4 {
			list = append(list, nullable(attempt(s.Picture(a.ID, i))))
		}
		shown[a.ID] = list
	}
	goDump["content"], goDump["feedContent"], goDump["pictures"] = content, feeds, shown
	sites := map[string]any{}
	for _, id := range append(sourceIDs(state), "missing") {
		sites[id] = attempt(s.Site(id))
	}
	goDump["sites"] = sites
	goDump["contentLinks"] = attempt(s.ContentLinks())
	var clipIDs []string
	for _, c := range state.Clips {
		clipIDs = append(clipIDs, c.ID)
	}
	goDump["clips"] = attempt(s.Clips(clipIDs))

	rows, err := s.all(`SELECT a.id, json_extract(a.data, '$.url') AS url,
      CASE WHEN json_extract(c.data, '$.status') = 'ready' THEN json_extract(c.data, '$.html') END AS content,
      json_extract(f.data, '$.html') AS feed, json_extract(f.data, '$.baseURL') AS baseURL
      FROM articles a LEFT JOIN article_content c ON c.articleID = a.id LEFT JOIN article_feed_content f ON f.articleID = a.id`)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		content, fromContent := row[2].(string)
		html, ok := content, fromContent
		if !ok {
			html, ok = row[3].(string)
		}
		if !ok {
			continue
		}
		base, ok := row[4].(string)
		if fromContent || !ok {
			base = fmt.Sprint(row[1])
		}
		all[row[0].(string)] = pictures(shorten(html), base, 1000)
	}
	goDump["allPictures"] = all
	var images [][3]string
	for _, item := range ts["images"].([]any) {
		pair := item.([]any)
		href := "ERR"
		if u, err := web.ResolveURL(pair[0].(string), pair[1].(string)); err == nil {
			href = u.Href()
		}
		images = append(images, [3]string{pair[0].(string), pair[1].(string), href})
	}
	goDump["images"] = images
	keys := map[string]any{}
	for url := range ts["keys"].(map[string]any) {
		if key, err := web.URLKey(url); err == nil {
			keys[url] = key
		} else {
			keys[url] = "ERR"
		}
	}
	goDump["keys"] = keys

	s.mu.Lock()
	for _, a := range state.Articles {
		if err := s.refreshPreview(a.ID); err != nil {
			t.Fatal(err)
		}
	}
	previews := map[string]any{}
	if rows, err = s.all("SELECT articleID, data FROM article_previews"); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		previews[row[0].(string)] = json.RawMessage(row[1].(string))
	}
	s.mu.Unlock()
	goDump["previews"] = previews

	failed := false
	for _, section := range []string{"library", "content", "feedContent", "pictures", "sites", "contentLinks", "clips", "allPictures", "images", "keys", "previews"} {
		var diffs []string
		compare(section, ts[section], reparse(t, goDump[section]), &diffs)
		t.Logf("%-12s %s", section, summary(diffs))
		failed = failed || len(diffs) > 0
	}
	if failed {
		t.Error("the Go store differs from the Electron store")
	}
	replay(t, source, seed, ts)
	roundTrip(t, source, seed)
}

// replay applies dump.ts's scripted changes to a fresh copy and compares the
// library and the rows they leave with what the Electron store left.
func replay(t *testing.T, source string, seed Snapshot, ts map[string]any) {
	s := mustOpen(t, copyLibrary(t, source), seed)
	started := time.Now()
	var results [][3]any
	for _, item := range ts["steps"].([]any) {
		step := item.(map[string]any)
		decode := func(key string, to any) {
			b, err := json.Marshal(step[key])
			if err == nil {
				err = json.Unmarshal(b, to)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		var err error
		switch {
		case step["mutate"] != nil:
			kinds := map[string]LibraryAction{"toggle-favorite": &ToggleFavorite{}, "set-favorites": &SetFavorites{}, "mark-read": &MarkRead{},
				"set-read-batch": &SetReadBatch{}, "rename-source": &RenameSource{}, "set-subscription": &SetSubscription{},
				"add-clip": &AddClip{}, "update-clip-note": &UpdateClipNote{}, "delete-clip": &DeleteClip{}}
			action := kinds[step["mutate"].(map[string]any)["type"].(string)]
			decode("mutate", action)
			_, err = s.Mutate(reflect.ValueOf(action).Elem().Interface().(LibraryAction))
		case step["position"] != nil:
			var p ReadingPositionInput
			decode("position", &p)
			err = s.SaveReadingPosition(p)
		case step["content"] != nil:
			var c ArticleContent
			decode("content", &c)
			err = s.SaveContent(c)
		case step["import"] != nil:
			var i struct {
				SourceID    string     `json:"sourceID"`
				Feed        ParsedFeed `json:"feed"`
				Resubscribe bool       `json:"resubscribe"`
				Icon, Name  *string
			}
			decode("import", &i)
			name := ""
			if i.Name != nil {
				name = *i.Name
			}
			var r SubscriptionResult
			if r, err = s.ImportFeed(i.SourceID, i.Feed, i.Resubscribe, i.Icon, name); err == nil {
				results = append(results, [3]any{r.SourceID, r.ImportedCount, r.UpdatedCount})
			}
		case step["feedError"] != nil:
			var e struct{ SourceID, Message string }
			decode("feedError", &e)
			err = s.RecordFeedError(e.SourceID, e.Message)
		case step["adopt"] != nil:
			var a struct{ Names map[string]string }
			decode("adopt", &a)
			err = s.AdoptNames(func(_ *string, home string) string { return a.Names[home] })
		}
		if err != nil {
			t.Fatalf("step %s: %v", short(step), err)
		}
	}
	tsAt, _ := web.JSDateParse(ts["dumpedAt"].(string))
	from, to := started.UnixMilli()-60_000, time.Now().UnixMilli()+60_000
	// A time each store stamped as it ran (toggle-favorite, a clip's
	// updatedAt, a reading position) matches the other's stamp.
	sameMoment = func(a, b any) bool {
		x, okX := a.(string)
		y, okY := b.(string)
		if !okX || !okY || !isoMillis.MatchString(x) || !isoMillis.MatchString(y) {
			return false
		}
		mx, _ := web.JSDateParse(x)
		my, _ := web.JSDateParse(y)
		return mx > tsAt-600_000 && mx <= tsAt+60_000 && my >= from && my <= to
	}
	defer func() { sameMoment = nil }()
	after := ts["after"].(map[string]any)
	state := lib(t, s)
	got := map[string]any{"library": state, "results": results, "content": attempt(s.Content(after["content"].(map[string]any)["articleID"].(string)))}
	feeds := map[string]any{}
	for _, a := range state.Articles {
		feeds[a.ID] = attempt(s.FeedContent(a.ID))
	}
	got["feedContent"] = feeds
	tables := map[string]any{}
	s.mu.Lock()
	for table := range after["rows"].(map[string]any) {
		column := "articleID"
		if table == "sources" || table == "articles" || table == "clips" {
			column = "id"
		}
		rows, err := s.all("SELECT " + column + ", data FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		data := map[string]any{}
		for _, row := range rows {
			data[row[0].(string)] = json.RawMessage(row[1].(string))
		}
		tables[table] = data
	}
	previews, err := s.all("SELECT articleID, data FROM article_previews")
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]any{}
	for _, row := range previews {
		stored[row[0].(string)] = json.RawMessage(row[1].(string))
	}
	got["rows"], got["previews"] = tables, stored
	failed := false
	for _, section := range []string{"library", "results", "content", "feedContent", "previews", "rows"} {
		var diffs []string
		compare("after."+section, after[section], reparse(t, got[section]), &diffs)
		t.Logf("replay %-12s %s", section, summary(diffs))
		failed = failed || len(diffs) > 0
	}
	if failed {
		t.Error("the same changes left a different library")
	}
}

// TestSharedFile writes to a library another process may hold, to see how
// the two apps share one file: TRIE_SHARED_LIBRARY names a temporary
// copy (.context/mygo/data-layer/run.sh holds it from Node meanwhile).
func TestSharedFile(t *testing.T) {
	path := os.Getenv("TRIE_SHARED_LIBRARY")
	if path == "" {
		t.Skip("set TRIE_SHARED_LIBRARY to a temporary copy")
	}
	started := time.Now()
	s, err := Open(path, Snapshot{Version: 1, AsOf: "2026-11-09"})
	if err != nil {
		t.Logf("go: open failed after %v: %v", time.Since(started).Round(100*time.Millisecond), err)
		return
	}
	defer s.Close()
	t.Logf("go: opened after %v", time.Since(started).Round(100*time.Millisecond))
	state, err := s.Mutate(RenameSource{ID: "innei", Name: "Innei (go)"})
	t.Logf("go: wrote after %v: %v; first source now %q", time.Since(started).Round(100*time.Millisecond), err, firstName(state))
}

func firstName(state LibraryState) string {
	if len(state.Sources) == 0 {
		return ""
	}
	return state.Sources[0].DisplayName
}

// sameMoment, when set, says two strings are the same stamp of "now".
var sameMoment func(a, b any) bool

// The comparison itself must see a difference wherever there is one.
func TestCompareFindsDifferences(t *testing.T) {
	base := `{"a":[1,{"b":"x","c":null}],"n":1.5,"e":{"error":"boom"}}`
	for _, changed := range []string{
		`{"a":[1,{"b":"y","c":null}],"n":1.5,"e":{"error":"boom"}}`,
		`{"a":[1,{"b":"x"}],"n":1.5,"e":{"error":"boom"}}`,
		`{"a":[1,{"b":"x","c":null},2],"n":1.5,"e":{"error":"boom"}}`,
		`{"a":[1,{"b":"x","c":null}],"n":1.25,"e":{"error":"boom"}}`,
		`{"a":[1,{"b":"x","c":null}],"n":1.5,"e":{"ok":true}}`,
		`{"a":[1,{"b":"x","c":false}],"n":1.5,"e":{"error":"boom"},"extra":0}`,
	} {
		var diffs []string
		compare("v", reparse(t, json.RawMessage(base)), reparse(t, json.RawMessage(changed)), &diffs)
		if len(diffs) == 0 {
			t.Fatalf("no difference found in %s", changed)
		}
	}
	var diffs []string
	compare("v", reparse(t, json.RawMessage(base)), reparse(t, json.RawMessage(`{"n":1.50,"e":{"error":"other"},"a":[1.0,{"c":null,"b":"x"}]}`)), &diffs)
	equal(t, diffs, []string(nil), "equal values")
}

// roundTrip reads every row of a second copy, writes it back through the Go
// types and Store.rewrite, and checks that its JSON means the same and the
// library reads the same.
func roundTrip(t *testing.T, source string, seed Snapshot) {
	s := mustOpen(t, copyLibrary(t, source), seed)
	before := lib(t, s)
	tables := []struct {
		table, column string
		value         func() any
	}{
		{"sources", "id", func() any { return new(SourceSnapshot) }},
		{"articles", "id", func() any { return new(ArticleSnapshot) }},
		{"reviews", "id", func() any { return new(ReviewItem) }},
		{"article_content", "articleID", func() any { return new(ArticleContent) }},
		{"article_feed_content", "articleID", func() any { return new(FeedBody) }},
		{"article_reading_positions", "articleID", func() any { return new(ReadingPosition) }},
		{"clips", "id", func() any { return new(Clip) }},
		{"article_previews", "articleID", func() any {
			return new(struct {
				storedPreview
				Version int `json:"version"`
			})
		}},
	}
	s.mu.Lock()
	failed := false
	for _, table := range tables {
		rows, err := s.all("SELECT " + table.column + ", data FROM " + table.table)
		if err != nil {
			t.Fatal(err)
		}
		var diffs []string
		for _, row := range rows {
			key, data := row[0].(string), row[1].(string)
			value := table.value()
			if err := json.Unmarshal([]byte(data), value); err != nil {
				diffs = append(diffs, key+": "+err.Error())
				continue
			}
			written, err := s.rewrite(table.table, table.column, key, reflect.ValueOf(value).Elem().Interface())
			if err != nil {
				t.Fatal(err)
			}
			compare(table.table+"["+key+"]", reparse(t, json.RawMessage(data)), reparse(t, json.RawMessage(written)), &diffs)
			if err := s.exec("UPDATE "+table.table+" SET data = ? WHERE "+table.column+" = ?", written, key); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("round trip %-26s %d rows, %s", table.table, len(rows), summary(diffs))
		failed = failed || len(diffs) > 0
	}
	s.mu.Unlock()
	var diffs []string
	compare("library", reparse(t, before), reparse(t, lib(t, s)), &diffs)
	t.Logf("round trip library after   %s", summary(diffs))
	if failed || len(diffs) > 0 {
		t.Error("a row changed on its way through Go")
	}
}

func copyLibrary(t testing.TB, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "library.sqlite")
	for _, suffix := range []string{"", "-wal"} {
		in, err := os.Open(source + suffix)
		if os.IsNotExist(err) && suffix != "" {
			continue
		} else if err != nil {
			t.Fatal(err)
		}
		out, err := os.Create(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(out, in); err != nil {
			t.Fatal(err)
		}
		in.Close()
		out.Close()
	}
	return path
}

func readDump(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseJSON(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return v.(map[string]any)
}

func sourceIDs(state LibraryState) []string {
	var ids []string
	for _, s := range state.Sources {
		ids = append(ids, s.ID)
	}
	return ids
}

// attempt is dump.ts's attempt(): the value, or {error} when the call failed.
func attempt[T any](v T, err error) any {
	if err != nil {
		return map[string]string{"error": err.Error()}
	}
	return v
}

func nullable(v any) any {
	if v == "" {
		return nil
	}
	return v
}

func reparse(t *testing.T, v any) any {
	t.Helper()
	text, err := stringify(v)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseJSON(text)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// compare reports where two JSON values differ; numbers compare by value,
// and two errors match whatever their messages.
func compare(path string, a, b any, out *[]string) {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: ts %s, go %s", path, short(a), short(b)))
			return
		}
		if _, isError := x["error"]; isError && len(x) == 1 {
			if _, ok := y["error"]; !ok || len(y) != 1 {
				*out = append(*out, fmt.Sprintf("%s: ts failed (%s), go %s", path, short(x["error"]), short(b)))
			}
			return
		}
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			xv, inX := x[k]
			yv, inY := y[k]
			switch {
			case !inX:
				*out = append(*out, fmt.Sprintf("%s.%s: only in go: %s", path, k, short(yv)))
			case !inY:
				*out = append(*out, fmt.Sprintf("%s.%s: only in ts: %s", path, k, short(xv)))
			default:
				compare(path+"."+k, xv, yv, out)
			}
		}
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			*out = append(*out, fmt.Sprintf("%s: ts %s, go %s", path, short(a), short(b)))
			return
		}
		for i := range x {
			compare(fmt.Sprintf("%s[%d]", path, i), x[i], y[i], out)
		}
	case json.Number:
		y, ok := b.(json.Number)
		fx, _ := web.NumberValue(x)
		fy, _ := web.NumberValue(y)
		if !ok || fx != fy {
			*out = append(*out, fmt.Sprintf("%s: ts %s, go %s", path, short(a), short(b)))
		}
	default:
		if a != b && (sameMoment == nil || !sameMoment(a, b)) {
			*out = append(*out, fmt.Sprintf("%s: ts %s, go %s", path, short(a), short(b)))
		}
	}
}

func short(v any) string {
	b, _ := json.Marshal(v)
	if s := string(b); len(s) > 160 {
		return s[:160] + "…"
	} else {
		return s
	}
}

func summary(diffs []string) string {
	if len(diffs) == 0 {
		return "identical"
	}
	shown := diffs[:min(12, len(diffs))]
	return fmt.Sprintf("%d differences:\n  %s", len(diffs), strings.Join(shown, "\n  "))
}
