package library

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
)

// Every path that rewrites a row keeps the JSON keys this package does not
// model, at the top level and (for a clip, whose note is patched in place)
// inside nested objects; keys it does model follow store.ts, so a body read
// again without a work page loses its old one.
func TestRewritesKeepUnknownKeys(t *testing.T) {
	path := libraryPath(t)
	s := mustOpen(t, path, previewSnapshot())
	importFeed(t, s, "writer", previewFeed("<p>"+strings.Repeat("Feed words here. ", 20)+"</p>", "body", "v1"))
	withWork := pageContent("<p>" + strings.Repeat("Page words here. ", 20) + "</p>")
	withWork.Work = json.RawMessage(`{"name":"W","designer":null,"site":null,"host":null,"builtWith":null,"facts":[],"sections":[],"poster":null,"video":null}`)
	if err := s.SaveContent(withWork); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveReadingPosition(ReadingPositionInput{ArticleID: previewBase, ContentVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	mutate(t, s, AddClip{Clip: clipInput(previewBase)})
	before := lib(t, s)
	db := raw(t, path)
	tables := []string{"sources", "articles", "article_feed_content", "article_content", "article_reading_positions", "clips", "article_previews"}
	for _, table := range tables {
		if _, err := db.Exec(`UPDATE ` + table + ` SET data = json_set(data, '$.future', json('{"kept":[1,"two",null]}'))`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE clips SET data = json_set(data, '$.citation.page', 12)`); err != nil {
		t.Fatal(err)
	}
	equal(t, lib(t, s), before, "unknown keys are not shown")

	importFeed(t, s, "writer", previewFeed("<p>"+strings.Repeat("New feed words. ", 20)+"</p>", "body", "v2"))
	if err := s.SaveContent(pageContent("<p>" + strings.Repeat("Read again. ", 20) + "</p>")); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveReadingPosition(ReadingPositionInput{ArticleID: previewBase, ContentVersion: "v2", Progress: 0.5}); err != nil {
		t.Fatal(err)
	}
	mutate(t, s, UpdateClipNote{ID: clipUUID, Note: "noted"})
	for _, table := range tables {
		var kept string
		if err := db.QueryRow(`SELECT json_extract(data, '$.future') FROM ` + table).Scan(&kept); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		equal(t, kept, `{"kept":[1,"two",null]}`, table)
	}
	var page int
	var note, work, html string
	if err := db.QueryRow(`SELECT json_extract(data, '$.citation.page'), json_extract(data, '$.note') FROM clips`).Scan(&page, &note); err != nil {
		t.Fatal(err)
	}
	equal(t, []any{page, note}, []any{12, "noted"}, "patched clip")
	if err := db.QueryRow(`SELECT coalesce(json_extract(data, '$.work'), 'none'), json_extract(data, '$.html') FROM article_content`).Scan(&work, &html); err != nil {
		t.Fatal(err)
	}
	equal(t, work, "none", "a modelled key the new body lacks is gone")
	if !strings.Contains(html, "Read again.") {
		t.Fatal("the new body was not written")
	}
	db.Close()
	// A seed rewriting a source without a feed keeps its unknown keys too.
	s.Close()
	path = libraryPath(t)
	mustOpen(t, path, fixture(t)).Close()
	db = raw(t, path)
	if _, err := db.Exec(`UPDATE sources SET data = json_set(data, '$.future', 1); UPDATE reviews SET data = json_set(data, '$.future', 1)`); err != nil {
		t.Fatal(err)
	}
	mustOpen(t, path, fixture(t)).Close()
	var missing int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM sources WHERE json_extract(data, '$.future') IS NOT 1) + (SELECT count(*) FROM reviews WHERE json_extract(data, '$.future') IS NOT 1)`).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	equal(t, missing, 0, "rows that lost a key")
}

// A damaged row is overwritten as store.ts overwrites it: there is nothing readable to keep.
func TestDamagedRowIsOverwritten(t *testing.T) {
	path, seed := libraryPath(t), fixture(t)
	mustOpen(t, path, seed).Close()
	if _, err := raw(t, path).Exec("INSERT INTO article_reading_positions VALUES (?, '{bad json')", seed.Articles[0].ID); err != nil {
		t.Fatal(err)
	}
	s := mustOpen(t, path, seed)
	if err := s.SaveReadingPosition(ReadingPositionInput{ArticleID: seed.Articles[0].ID, ContentVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	if lib(t, s).Articles[0].ReadingPosition == nil {
		t.Fatal("the new position was not read")
	}
}

func documents(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile("testdata/documents.json")
	if err != nil {
		t.Fatal(err)
	}
	var docs map[string]json.RawMessage
	if err := json.Unmarshal(data, &docs); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range docs {
		out[k] = string(v)
	}
	return out
}

func doc(t *testing.T, text string) object {
	t.Helper()
	v, err := parseJSON(text)
	if err != nil {
		t.Fatal(err)
	}
	return v.(object)
}

// rejects executable nodes, source props, unsafe links and remote media in cached structure
func TestDocumentRejectsUnsafeNodes(t *testing.T) {
	source := documents(t)[`<p id="root">Read <a href="https://example.com/">link</a></p>`]
	if !isReaderDocument(doc(t, source)) {
		t.Fatal("the formatter's document was refused")
	}
	for _, bad := range []string{
		`{"type":"element","tagName":"script","properties":{},"children":[]}`,
		`{"type":"element","tagName":"p","properties":{"onClick":"bad()"},"children":[]}`,
		`{"type":"element","tagName":"div","properties":{"style":"position:fixed"},"children":[]}`,
		`{"type":"element","tagName":"a","properties":{"href":"javascript:bad()"},"children":[]}`,
		`{"type":"element","tagName":"img","properties":{"src":"https://example.com/track.png"},"children":[]}`,
	} {
		d := doc(t, source)
		root := d["root"].(object)
		root["children"] = append(root["children"].([]any), doc(t, bad))
		if isReaderDocument(d) {
			t.Fatalf("accepted %s", bad)
		}
	}
}

// rejects mismatched or duplicate unit bindings and missing source anchors
func TestDocumentRejectsBadBindings(t *testing.T) {
	source := documents(t)[`<p id="first">One</p><p>Two</p>`]
	for name, edit := range map[string]func(object){
		"mismatch":  func(d object) { d["units"].([]any)[0].(object)["id"] = "not-in-tree" },
		"duplicate": func(d object) { d["units"] = append(d["units"].([]any), d["units"].([]any)[0]) },
		"missing":   func(d object) { d["units"].([]any)[0].(object)["anchor"] = "missing" },
	} {
		d := doc(t, source)
		edit(d)
		if isReaderDocument(d) {
			t.Fatalf("%s accepted", name)
		}
	}
}

// rejects oversized or deeply nested cached trees before rendering
func TestDocumentRejectsLargeTrees(t *testing.T) {
	source := documents(t)[`<p>Readable</p>`]
	wide := doc(t, source)
	children := make([]any, 50001)
	for i := range children {
		children[i] = object{"type": "text", "value": "x"}
	}
	wide["root"].(object)["children"] = children
	if isReaderDocument(wide) {
		t.Fatal("wide tree accepted")
	}
	deep := doc(t, source)
	var branch any = object{"type": "text", "value": "end"}
	for range 130 {
		branch = object{"type": "element", "tagName": "div", "properties": object{}, "children": []any{branch}}
	}
	root := deep["root"].(object)
	root["children"] = append(root["children"].([]any), branch)
	if isReaderDocument(deep) {
		t.Fatal("deep tree accepted")
	}
}

// persists source snapshot and structured versions without changing library state
func TestContentKeepsStructure(t *testing.T) {
	seed := fixture(t)
	s := mustOpen(t, ":memory:", seed)
	id := seed.Articles[0].ID
	before := lib(t, s)
	var formatted struct {
		HTML            string          `json:"html"`
		FormatVersion   int             `json:"formatVersion"`
		Headings        []Heading       `json:"headings"`
		CodeBlocks      []CodeBlock     `json:"codeBlocks"`
		HighlightStyles []string        `json:"highlightStyles"`
		Document        json.RawMessage `json:"document"`
	}
	if err := json.Unmarshal([]byte(documents(t)["formatted"]), &formatted); err != nil {
		t.Fatal(err)
	}
	snapshot := &ReaderSourceSnapshot{HTML: "<h2>Title</h2><p>Body.</p>", Provenance: "source"}
	value := ArticleContent{ArticleID: id, Status: "ready", HTML: formatted.HTML, FormatVersion: formatted.FormatVersion, Headings: formatted.Headings,
		CodeBlocks: formatted.CodeBlocks, HighlightStyles: formatted.HighlightStyles, Document: formatted.Document, SourceSnapshot: snapshot,
		TextLength: 10, Links: []string{}, FetchedAt: "2026-11-02T00:00:00Z", SourceURL: seed.Articles[0].URL, SourceKind: "page"}
	if err := s.SaveContent(value); err != nil {
		t.Fatal(err)
	}
	saved := must[*ArticleContent](t)(s.Content(id))
	if !isReaderDocument(doc(t, string(saved.Document))) || doc(t, string(saved.Document))["contentRevision"] != doc(t, string(formatted.Document))["contentRevision"] {
		t.Fatal("the document did not survive")
	}
	equal(t, saved.SourceSnapshot, snapshot, "source snapshot")
	equal(t, []any{saved.FormatVersion, saved.Headings}, []any{formatted.FormatVersion, formatted.Headings}, "format")
	after := lib(t, s)
	equal(t, after.Sources, before.Sources, "sources")
	for i := range after.Articles {
		equal(t, [2]bool{after.Articles[i].Read, after.Articles[i].Favorite}, [2]bool{before.Articles[i].Read, before.Articles[i].Favorite}, "article state")
	}
	bad := doc(t, string(formatted.Document))
	root := bad["root"].(object)
	root["children"] = append(root["children"].([]any), object{"type": "element", "tagName": "iframe", "properties": object{}, "children": []any{}})
	value.Document = must[json.RawMessage](t)(json.Marshal(bad))
	fails(t, s.SaveContent(value), "Invalid structured")
	equal(t, must[*ArticleContent](t)(s.Content(id)).HTML, formatted.HTML, "html after a refused save")
}

// Calls from many goroutines run one at a time; none is lost.
func TestConcurrentCalls(t *testing.T) {
	seed := fixture(t)
	s := mustOpen(t, libraryPath(t), seed)
	var wg sync.WaitGroup
	errs := make(chan error, 8*25*4)
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := seed.Articles[g%len(seed.Articles)].ID
			for range 25 {
				_, err := s.Mutate(ToggleFavorite{ID: id})
				errs <- err
				errs <- s.SaveReadingPosition(ReadingPositionInput{ArticleID: id, ContentVersion: "v", Progress: 0.5})
				_, err = s.Library()
				errs <- err
				_, err = s.Content(id)
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	equal(t, lib(t, s).Revision, 200, "revision")
}

func TestActionJSON(t *testing.T) {
	for _, c := range []struct {
		action LibraryAction
		want   string
	}{
		{MarkRead{ID: "a", Read: false}, `{"type":"mark-read","id":"a","read":false}`},
		{SetFavorites{Items: []FavoriteTime{{ID: "a"}}}, `{"type":"set-favorites","items":[{"id":"a","favoritedAt":null}]}`},
		{AddClip{Clip: ClipInput{ID: "c", ArticleID: "a", Text: "t"}}, `{"type":"add-clip","clip":{"id":"c","articleID":"a","text":"t","note":"","unitID":null,"contentRevision":null}}`},
		{DeleteClip{ID: "c"}, `{"type":"delete-clip","id":"c"}`},
	} {
		equal(t, string(must[[]byte](t)(json.Marshal(c.action))), c.want, "action JSON")
	}
}

// A value that encodes to {} still leaves a valid row with the stored keys.
func TestRewriteOfAnEmptyObjectStaysValid(t *testing.T) {
	path := libraryPath(t)
	s := mustOpen(t, path, fixture(t))
	defer s.Close()
	db := raw(t, path)
	defer db.Close()
	var id string
	if err := db.QueryRow(`SELECT id FROM sources LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sources SET data = json_set(data, '$.future', 1) WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	data, err := s.rewrite("sources", "id", id, struct {
		A string `json:"a,omitempty"`
	}{})
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(data)) || !strings.Contains(data, `"future":1`) {
		t.Fatalf("rewrite gave %s", data)
	}
}

// A document is checked from its JSON text before it is saved, and broken text never passes.
func TestIsReaderDocumentText(t *testing.T) {
	for _, doc := range []string{``, `not json`, `{}`, `[]`, `{"version":1}`, `{"version":1,"root":{}} trailing`} {
		if IsReaderDocument([]byte(doc)) {
			t.Fatalf("%q passed", doc)
		}
	}
}
