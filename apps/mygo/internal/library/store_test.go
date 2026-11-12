package library

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Store cases. The comment over each test starts with the case's name.

// fixture is the bundled verified sample (testdata/verified-snapshot.json); each test gets its own copy.
func fixture(t *testing.T) Snapshot {
	t.Helper()
	data, err := os.ReadFile("testdata/verified-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func libraryPath(t *testing.T) string { return filepath.Join(t.TempDir(), "library.sqlite") }

func mustOpen(t testing.TB, path string, seed Snapshot) *Store {
	t.Helper()
	s, err := Open(path, seed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func lib(t *testing.T, s *Store) LibraryState {
	t.Helper()
	return must[LibraryState](t)(s.Library())
}

func mutate(t *testing.T, s *Store, a LibraryAction) LibraryState {
	t.Helper()
	return must[LibraryState](t)(s.Mutate(a))
}

func equal[T any](t *testing.T, got, want T, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", what, got, want)
	}
}

func fails(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %v, want one containing %q", err, want)
	}
}

// raw opens the file without the store.
func raw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// initial import reports the bounded verified sample and persists through reopen
func TestInitialImportPersistsThroughReopen(t *testing.T) {
	path, snapshot := libraryPath(t), fixture(t)
	first := mustOpen(t, path, snapshot)
	state := lib(t, first)
	equal(t, len(state.Sources), 6, "sources")
	equal(t, len(state.Articles), 5, "articles")
	equal(t, len(state.ReviewItems), 11, "review items")
	equal(t, state.Summary, snapshot.Summary, "summary")
	equal(t, state.Revision, 0, "revision")
	for _, s := range state.Sources {
		if !s.Subscribed || s.DisplayName != s.Title {
			t.Fatalf("source %s starts unsubscribed or renamed", s.ID)
		}
	}
	for _, a := range state.Articles {
		if a.Read || a.Favorite {
			t.Fatalf("article %s starts read or kept", a.ID)
		}
	}
	mutate(t, first, ToggleFavorite{ID: snapshot.Articles[0].ID})
	mutate(t, first, MarkRead{ID: snapshot.Articles[0].ID, Read: true})
	mutate(t, first, SetSubscription{ID: "innei", Subscribed: false})
	mutate(t, first, RenameSource{ID: "innei", Name: "  My Innei  "})
	first.Close()

	snapshot.AsOf = "2026-11-02"
	snapshot.Sources[0].Title = "New upstream title"
	snapshot.Sources[0].CatalogCandidateCount = 405
	snapshot.Articles[0].Title = "Updated article metadata"
	// New seeds can omit old records without deleting the local library.
	snapshot.Sources = snapshot.Sources[:len(snapshot.Sources)-1]
	snapshot.Articles = snapshot.Articles[:len(snapshot.Articles)-1]
	snapshot.ReviewItems = snapshot.ReviewItems[:len(snapshot.ReviewItems)-1]
	second := mustOpen(t, path, snapshot)
	state = lib(t, second)
	equal(t, state.AsOf, "2026-11-02", "asOf")
	equal(t, state.Revision, 4, "revision")
	equal(t, len(state.Sources), 6, "sources")
	equal(t, len(state.Articles), 5, "articles")
	equal(t, len(state.ReviewItems), 11, "review items")
	equal(t, state.Sources[0].Title, "New upstream title", "title")
	equal(t, state.Sources[0].DisplayName, "My Innei", "display name")
	equal(t, state.Sources[0].CatalogCandidateCount, 405, "candidates")
	equal(t, state.Sources[0].Subscribed, false, "subscribed")
	equal(t, state.Articles[0].Title, "Updated article metadata", "article title")
	equal(t, state.Articles[0].Favorite, true, "favorite")
	equal(t, state.Articles[0].Read, true, "read")
	changed := mutate(t, second, ToggleFavorite{ID: snapshot.Articles[0].ID})
	equal(t, changed.Articles[0].Favorite, false, "favorite after toggle")
	equal(t, changed.Sources[0].Subscribed, false, "subscribed after toggle")
}

// bulk read can be undone and all state changes survive independent connections
func TestBulkReadUndoAcrossConnections(t *testing.T) {
	path, snapshot := libraryPath(t), fixture(t)
	var ids []string
	for _, a := range snapshot.Articles {
		ids = append(ids, a.ID)
	}
	allRead := func(state LibraryState, read bool) bool {
		for _, a := range state.Articles {
			if a.Read != read {
				return false
			}
		}
		return true
	}
	first := mustOpen(t, path, snapshot)
	read := mutate(t, first, SetReadBatch{IDs: ids, Read: true})
	if !allRead(read, true) || read.Revision != 1 {
		t.Fatalf("batch read: revision %d", read.Revision)
	}
	first.Close()
	second := mustOpen(t, path, snapshot)
	if !allRead(lib(t, second), true) {
		t.Fatal("read state did not persist")
	}
	undone := mutate(t, second, SetReadBatch{IDs: ids, Read: false})
	if !allRead(undone, false) || undone.Revision != 2 {
		t.Fatalf("undo: revision %d", undone.Revision)
	}
	second.Close()
	if !allRead(lib(t, mustOpen(t, path, snapshot)), false) {
		t.Fatal("undo did not persist")
	}
}

type unsupported struct{}

func (unsupported) action() string { return "delete-source" }

// invalid runtime mutations preserve every row and revision. Values with the
// wrong JSON type (read: 1, name: 7, ids: 'not an array') cannot be written
// as Go actions; the rest are here.
func TestInvalidMutationsPreserveRows(t *testing.T) {
	snapshot := fixture(t)
	s := mustOpen(t, libraryPath(t), snapshot)
	id := snapshot.Articles[0].ID
	many := make([]string, 5001)
	for i := range many {
		many[i] = string(rune('a'+i%26)) + strings.Repeat("x", i/26)
	}
	invalid := []LibraryAction{
		nil, unsupported{},
		ToggleFavorite{ID: "missing"}, MarkRead{ID: "missing", Read: true},
		SetSubscription{ID: "missing"},
		RenameSource{ID: "missing", Name: "Name"},
		RenameSource{ID: "innei", Name: "  "},
		RenameSource{ID: "innei", Name: strings.Repeat("x", 41)},
		SetReadBatch{IDs: []string{id, "missing"}, Read: true},
		SetReadBatch{IDs: []string{id, id}, Read: true},
		SetReadBatch{IDs: []string{id, ""}, Read: true},
		SetReadBatch{IDs: many, Read: true},
		ToggleFavorite{ID: "' OR 1=1 --"},
		ToggleFavorite{ID: ""},
	}
	before := lib(t, s)
	for _, a := range invalid {
		if _, err := s.Mutate(a); err == nil {
			t.Fatalf("%#v was accepted", a)
		}
		equal(t, lib(t, s), before, "library after a refused action")
	}
	equal(t, mutate(t, s, ToggleFavorite{ID: id}).Revision, 1, "revision")
}

// source names are trimmed, length bounded and stored as literal SQL text
func TestSourceNamesAreLiteral(t *testing.T) {
	s := mustOpen(t, libraryPath(t), fixture(t))
	name := "Robert'); DROP TABLE articles;--"
	state := mutate(t, s, RenameSource{ID: "innei", Name: " " + name + " "})
	equal(t, state.Sources[0].DisplayName, name, "name")
	equal(t, len(state.Articles), 5, "articles")
	equal(t, len(mutate(t, s, RenameSource{ID: "innei", Name: strings.Repeat("x", 40)}).Sources[0].DisplayName), 40, "length")
	// JavaScript counts UTF-16 units: 20 astral characters are 40 and fit, 21 do not.
	mutate(t, s, RenameSource{ID: "innei", Name: strings.Repeat("😀", 20)})
	_, err := s.Mutate(RenameSource{ID: "innei", Name: strings.Repeat("😀", 21)})
	fails(t, err, "订阅名称")
}

// a failed seed rolls back metadata and inserted rows before closing
func TestFailedSeedRollsBack(t *testing.T) {
	path, snapshot := libraryPath(t), fixture(t)
	s := mustOpen(t, path, snapshot)
	before := lib(t, s)
	s.Close()
	broken := fixture(t)
	broken.AsOf = "2026-12-01"
	broken.Sources[0].Title = "Should roll back"
	bad := broken.Articles[0]
	bad.ID, bad.SourceID = "new-invalid-article", "missing-source"
	broken.Articles = append(broken.Articles, bad)
	_, err := Open(path, broken)
	fails(t, err, "FOREIGN KEY")
	db := raw(t, path)
	var asOf, data string
	if err := db.QueryRow("SELECT asOf FROM metadata").Scan(&asOf); err != nil {
		t.Fatal(err)
	}
	equal(t, asOf, before.AsOf, "asOf")
	if err := db.QueryRow("SELECT data FROM sources WHERE id = ?", "innei").Scan(&data); err != nil {
		t.Fatal(err)
	}
	equal(t, data, must[string](t)(stringify(snapshot.Sources[0])), "source data")
	if err := db.QueryRow("SELECT 1 FROM articles WHERE id = ?", "new-invalid-article").Scan(new(int)); err != sql.ErrNoRows {
		t.Fatalf("the invalid article was kept: %v", err)
	}
	db.Close()
	// Reopening also proves the failed open released its database lock.
	equal(t, lib(t, mustOpen(t, path, snapshot)), before, "library after reopening")
}

// A library from a later schema is refused, not rewritten.
func TestUnsupportedSchema(t *testing.T) {
	path := libraryPath(t)
	mustOpen(t, path, fixture(t)).Close()
	db := raw(t, path)
	if _, err := db.Exec("UPDATE metadata SET schemaVersion = 2"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err := Open(path, fixture(t))
	fails(t, err, "Unsupported library schema")
}

// The tables, pragmas and schema text are those of libraries written by earlier versions, so either version opens the
// other's file.
func TestSchemaAndPragmas(t *testing.T) {
	path := libraryPath(t)
	s := mustOpen(t, path, fixture(t))
	var mode string
	var keys, timeout int
	row := func(q string, v any) {
		if err := s.conn.QueryRowContext(bg, q).Scan(v); err != nil {
			t.Fatal(err)
		}
	}
	row("PRAGMA journal_mode", &mode)
	row("PRAGMA foreign_keys", &keys)
	row("PRAGMA busy_timeout", &timeout)
	equal(t, []any{mode, keys, timeout}, []any{"wal", 1, 5000}, "pragmas")
	var tables string
	row("SELECT group_concat(name, ',') FROM (SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name)", &tables)
	equal(t, tables, "article_content,article_feed_content,article_previews,article_reading_positions,article_sources,articles,clips,metadata,reviews,source_feeds,sources", "tables")
	var create string
	row("SELECT sql FROM sqlite_master WHERE name = 'articles'", &create)
	if !strings.HasSuffix(create, "favorite INTEGER NOT NULL DEFAULT 0 CHECK(favorite IN (0, 1))\n        , favoritedAt TEXT)") {
		t.Fatalf("articles table: %q", create)
	}
}
