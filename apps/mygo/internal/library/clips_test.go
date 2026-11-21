package library

import (
	"math"
	"strings"
	"testing"
	"time"
)

// Clip, favourite and reading-position cases.

const clipUUID = "6f1c2d3e-4a5b-4c6d-8e7f-90a1b2c3d4e5"

func clipInput(articleID string) ClipInput {
	return ClipInput{ID: clipUUID, ArticleID: articleID, Text: "  first line\n\nsecond line ", UnitID: ptr("paragraph-3"), ContentRevision: ptr(strings.Repeat("a", 64))}
}

// a clip keeps its citation and note across reopen and leaves article state alone
func TestClipKeepsCitationAndNote(t *testing.T) {
	path, snapshot := libraryPath(t), fixture(t)
	article := snapshot.Articles[0]
	first := mustOpen(t, path, snapshot)
	before := lib(t, first)
	mutate(t, first, AddClip{Clip: clipInput(article.ID)})
	mutate(t, first, UpdateClipNote{ID: clipUUID, Note: "use in Trie"})
	first.Close()
	second := mustOpen(t, path, snapshot)
	after := lib(t, second)
	equal(t, len(after.Clips), 1, "clips")
	c := after.Clips[0]
	equal(t, c.Note, "use in Trie", "note")
	equal(t, c.Citation.Title, article.Title, "citation title")
	equal(t, c.Citation.URL, article.URL, "citation url")
	equal(t, c.Citation.Authors, article.Authors, "citation authors")
	if c.Citation.SourceName == "" || *c.UnitID != "paragraph-3" || c.UpdatedAt < c.CreatedAt {
		t.Fatalf("clip %#v", c)
	}
	equal(t, after.Revision, before.Revision+2, "revision")
	for i := range after.Articles {
		equal(t, [2]bool{after.Articles[i].Read, after.Articles[i].Favorite}, [2]bool{before.Articles[i].Read, before.Articles[i].Favorite}, "article state")
	}
	equal(t, must[[]Clip](t)(second.Clips([]string{clipUUID})), []Clip{c}, "clips by ID")
}

// deleting a clip can be undone with its original creation time
func TestDeletedClipRestoresItsTime(t *testing.T) {
	snapshot := fixture(t)
	s := mustOpen(t, libraryPath(t), snapshot)
	articleID := snapshot.Articles[0].ID
	mutate(t, s, AddClip{Clip: clipInput(articleID)})
	original := lib(t, s).Clips[0]
	mutate(t, s, DeleteClip{ID: clipUUID})
	equal(t, len(lib(t, s).Clips), 0, "clips after delete")
	restored := clipInput(articleID)
	restored.CreatedAt = original.CreatedAt
	mutate(t, s, AddClip{Clip: restored})
	equal(t, lib(t, s).Clips[0].CreatedAt, original.CreatedAt, "restored time")
	_, err := s.Mutate(AddClip{Clip: clipInput(articleID)})
	fails(t, err, "already exists")
	_, err = s.Mutate(DeleteClip{ID: "00000000-0000-4000-8000-000000000000"})
	fails(t, err, "Unknown clip")
	missing := clipInput("missing-article")
	missing.ID = "00000000-0000-4000-8000-000000000001"
	_, err = s.Mutate(AddClip{Clip: missing})
	fails(t, err, "Unknown article")
	equal(t, len(lib(t, s).Clips), 1, "clips")
}

// a clip keeps its pictures across a note edit and reopen; a clip without them
// still loads, and badly placed pictures are refused
func TestClipKeepsPictures(t *testing.T) {
	path, snapshot := libraryPath(t), fixture(t)
	s := mustOpen(t, path, snapshot)
	in := clipInput(snapshot.Articles[0].ID)
	pictures := []ClipPicture{{Unit: "reader-unit-a", At: 0}, {Unit: "reader-unit-b", At: 14}}
	in.Pictures = pictures
	mutate(t, s, AddClip{Clip: in})
	mutate(t, s, UpdateClipNote{ID: clipUUID, Note: "chart"})
	plain := clipInput(snapshot.Articles[0].ID)
	plain.ID = "00000000-0000-4000-8000-000000000002"
	mutate(t, s, AddClip{Clip: plain})
	s.Close()
	if _, err := raw(t, path).Exec("INSERT INTO clips VALUES (?, ?, json_set((SELECT data FROM clips WHERE id = ?), '$.id', ?, '$.pictures', json('[{\"unit\":\"u\",\"at\":1.5}]')))",
		"00000000-0000-4000-8000-000000000003", snapshot.Articles[0].ID, clipUUID, "00000000-0000-4000-8000-000000000003"); err != nil {
		t.Fatal(err)
	}
	clips := lib(t, mustOpen(t, path, snapshot)).Clips
	equal(t, len(clips), 2, "clips (the one with a fractional At is damaged)")
	for _, c := range clips {
		if c.ID == clipUUID {
			equal(t, c.Pictures, pictures, "pictures")
			equal(t, c.Note, "chart", "note")
		} else if c.Pictures != nil {
			t.Fatalf("plain clip gained pictures %#v", c.Pictures)
		}
	}
	for _, bad := range [][]ClipPicture{
		{{Unit: "u", At: 27}},
		{{Unit: "u", At: 5}, {Unit: "v", At: 4}},
		{{Unit: "", At: 0}},
		{{Unit: "u", At: 0}, {Unit: "u", At: 3}},
		{{Unit: "u", At: -1}},
	} {
		c := clipInput("a")
		c.Pictures = bad
		if checkAction(AddClip{Clip: c}) == nil {
			t.Fatalf("%#v was accepted", bad)
		}
	}
}

// a damaged clip row is skipped instead of breaking the library
func TestDamagedClipIsSkipped(t *testing.T) {
	path, snapshot := libraryPath(t), fixture(t)
	s := mustOpen(t, path, snapshot)
	mutate(t, s, AddClip{Clip: clipInput(snapshot.Articles[0].ID)})
	s.Close()
	if _, err := raw(t, path).Exec("INSERT INTO clips VALUES (?, ?, ?)", "broken", snapshot.Articles[0].ID, `{"text":`); err != nil {
		t.Fatal(err)
	}
	clips := lib(t, mustOpen(t, path, snapshot)).Clips
	equal(t, len(clips), 1, "clips")
	equal(t, clips[0].ID, clipUUID, "clip")
}

// runtime clip actions are validated before reaching the store (a note of
// the wrong JSON type cannot be written in Go)
func TestClipActionsAreValidated(t *testing.T) {
	if err := checkAction(AddClip{Clip: clipInput("article")}); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*ClipInput){
		func(c *ClipInput) { c.ID = "not-a-uuid" },
		func(c *ClipInput) { c.Text = "   " },
		func(c *ClipInput) { c.Text = strings.Repeat("x", 20001) },
		func(c *ClipInput) { c.ContentRevision = ptr("short") },
		func(c *ClipInput) { c.UnitID = ptr("has space") },
		func(c *ClipInput) { c.UnitID = ptr("") },
		func(c *ClipInput) { c.CreatedAt = "yesterday" },
		func(c *ClipInput) { c.CreatedAt = "2026-13-01T00:00:00.000Z" },
		func(c *ClipInput) { c.Note = strings.Repeat("x", 4001) },
	} {
		c := clipInput("a")
		edit(&c)
		if checkAction(AddClip{Clip: c}) == nil {
			t.Fatalf("%#v was accepted", c)
		}
	}
	if checkAction(UpdateClipNote{ID: clipUUID, Note: strings.Repeat("x", 4001)}) == nil {
		t.Fatal("a long note was accepted")
	}
}

// keeping stamps favoritedAt, letting go clears it, and an old library gains the column with its favourites intact
func TestFavoritedAtAndMigration(t *testing.T) {
	path, snapshot := libraryPath(t), fixture(t)
	id := snapshot.Articles[0].ID
	s := mustOpen(t, path, snapshot)
	find := func(state LibraryState) Article {
		for _, a := range state.Articles {
			if a.ID == id {
				return a
			}
		}
		t.Fatal("article missing")
		return Article{}
	}
	before := time.Now().Add(-time.Second)
	kept := find(mutate(t, s, ToggleFavorite{ID: id}))
	at, err := time.Parse(time.RFC3339, *kept.FavoritedAt)
	if !kept.Favorite || err != nil || at.Before(before) {
		t.Fatalf("kept %v at %v", kept.Favorite, kept.FavoritedAt)
	}
	released := find(mutate(t, s, ToggleFavorite{ID: id}))
	if released.Favorite || released.FavoritedAt != nil {
		t.Fatalf("released %v at %v", released.Favorite, released.FavoritedAt)
	}
	mutate(t, s, ToggleFavorite{ID: id})
	s.Close()
	if _, err := raw(t, path).Exec("ALTER TABLE articles DROP COLUMN favoritedAt"); err != nil {
		t.Fatal(err)
	}
	upgraded := find(lib(t, mustOpen(t, path, snapshot)))
	if !upgraded.Favorite || upgraded.FavoritedAt != nil {
		t.Fatalf("upgraded %v at %v", upgraded.Favorite, upgraded.FavoritedAt)
	}
}

// letting go of several keeps at once, and undoing it, puts each back with the time it was kept
func TestSetFavoritesUndo(t *testing.T) {
	snapshot := fixture(t)
	s := mustOpen(t, libraryPath(t), snapshot)
	a, b := snapshot.Articles[0].ID, snapshot.Articles[1].ID
	mutate(t, s, ToggleFavorite{ID: a})
	mutate(t, s, ToggleFavorite{ID: b})
	var kept, gone []FavoriteTime
	for _, article := range lib(t, s).Articles {
		if article.ID == a || article.ID == b {
			kept = append(kept, FavoriteTime{ID: article.ID, FavoritedAt: article.FavoritedAt})
			gone = append(gone, FavoriteTime{ID: article.ID})
		}
	}
	for _, article := range mutate(t, s, SetFavorites{Items: gone}).Articles {
		if (article.ID == a || article.ID == b) && (article.Favorite || article.FavoritedAt != nil) {
			t.Fatalf("%s is still kept", article.ID)
		}
	}
	back := mutate(t, s, SetFavorites{Items: kept}).Articles
	for _, item := range kept {
		for _, article := range back {
			if article.ID == item.ID && (!article.Favorite || *article.FavoritedAt != *item.FavoritedAt) {
				t.Fatalf("%s came back as %v at %v", article.ID, article.Favorite, article.FavoritedAt)
			}
		}
	}
	for _, items := range [][]FavoriteTime{{{ID: a}, {ID: a}}, {{ID: "missing"}}, {{ID: a, FavoritedAt: ptr("yesterday")}}, {}} {
		if _, err := s.Mutate(SetFavorites{Items: items}); err == nil {
			t.Fatalf("%v was accepted", items)
		}
	}
	// A time is stored as new Date(time).toISOString() would write it.
	mutate(t, s, SetFavorites{Items: []FavoriteTime{{ID: a, FavoritedAt: ptr("2026-11-02T10:00:00+08:00")}}})
	equal(t, *lib(t, s).Articles[0].FavoritedAt, "2026-11-02T02:00:00.000Z", "normalized time")
}

// reading position survives reopen and overwrite without touching article state or content
func TestReadingPositionSurvivesReopen(t *testing.T) {
	path, snapshot := libraryPath(t), fixture(t)
	id := snapshot.Articles[0].ID
	first := mustOpen(t, path, snapshot)
	content := ArticleContent{ArticleID: id, Status: "ready", HTML: "<p>Body</p>", TextLength: 4, Links: []string{},
		FetchedAt: "2026-11-02T00:00:00.000Z", SourceURL: snapshot.Articles[0].URL, SourceKind: "page"}
	mutate(t, first, MarkRead{ID: id, Read: true})
	mutate(t, first, ToggleFavorite{ID: id})
	if err := first.SaveContent(content); err != nil {
		t.Fatal(err)
	}
	before := lib(t, first)
	if err := first.SaveReadingPosition(ReadingPositionInput{ArticleID: id, ContentVersion: "v1", UnitID: ptr("paragraph-4"), Offset: 12, ScrollTop: 340, Progress: 0.4}); err != nil {
		t.Fatal(err)
	}
	equal(t, lib(t, first).Revision, before.Revision, "revision")
	first.Close()

	second := mustOpen(t, path, snapshot)
	p := lib(t, second).Articles[0].ReadingPosition
	equal(t, p.ReadingPositionInput, ReadingPositionInput{ArticleID: id, ContentVersion: "v1", UnitID: ptr("paragraph-4"), Offset: 12, ScrollTop: 340, Progress: 0.4}, "position")
	if !dateOK(p.UpdatedAt) {
		t.Fatalf("updatedAt %q", p.UpdatedAt)
	}
	if err := second.SaveReadingPosition(ReadingPositionInput{ArticleID: id, ContentVersion: "v2", Offset: -7, Progress: 1}); err != nil {
		t.Fatal(err)
	}
	after := lib(t, second)
	q := after.Articles[0].ReadingPosition
	if q.ContentVersion != "v2" || q.UnitID != nil || q.Offset != -7 {
		t.Fatalf("overwritten position %#v", q)
	}
	equal(t, []any{after.Revision, after.AsOf, after.Articles[0].Read, after.Articles[0].Favorite}, []any{before.Revision, before.AsOf, true, true}, "library state")
	content.FromCache = true
	equal(t, *must[*ArticleContent](t)(second.Content(id)), content, "content")
}

// invalid runtime positions and unknown articles never write a row (values
// of the wrong JSON type, and a missing unitID, cannot be written in Go)
func TestInvalidPositionsNeverWrite(t *testing.T) {
	snapshot := fixture(t)
	s := mustOpen(t, libraryPath(t), snapshot)
	valid := ReadingPositionInput{ArticleID: snapshot.Articles[0].ID, ContentVersion: "v1"}
	before := lib(t, s)
	for _, edit := range []func(*ReadingPositionInput){
		func(p *ReadingPositionInput) { p.ArticleID = "" },
		func(p *ReadingPositionInput) { p.ArticleID = "missing" },
		func(p *ReadingPositionInput) { p.ArticleID = strings.Repeat("x", 4097) },
		func(p *ReadingPositionInput) { p.ContentVersion = "" },
		func(p *ReadingPositionInput) { p.ContentVersion = strings.Repeat("x", 257) },
		func(p *ReadingPositionInput) { p.UnitID = ptr("") },
		func(p *ReadingPositionInput) { p.UnitID = ptr("a\n") },
		func(p *ReadingPositionInput) { p.UnitID = ptr(strings.Repeat("x", 257)) },
		func(p *ReadingPositionInput) { p.Offset = math.NaN() },
		func(p *ReadingPositionInput) { p.Offset = math.Inf(1) },
		func(p *ReadingPositionInput) { p.Offset = 1e7 + 1 },
		func(p *ReadingPositionInput) { p.ScrollTop = -1 },
		func(p *ReadingPositionInput) { p.ScrollTop = math.Inf(1) },
		func(p *ReadingPositionInput) { p.Progress = -0.1 },
		func(p *ReadingPositionInput) { p.Progress = 1.1 },
		func(p *ReadingPositionInput) { p.Progress = math.NaN() },
	} {
		p := valid
		edit(&p)
		if s.SaveReadingPosition(p) == nil {
			t.Fatalf("%#v was saved", p)
		}
		if p.ArticleID != "missing" && checkPosition(p) == nil {
			t.Fatalf("%#v passed the check", p)
		}
		equal(t, lib(t, s), before, "library")
	}
}

// damaged stored position is ignored while the rest of the library loads
func TestDamagedPositionIsIgnored(t *testing.T) {
	path, snapshot := libraryPath(t), fixture(t)
	mustOpen(t, path, snapshot).Close()
	db := raw(t, path)
	for _, row := range [][2]string{
		{snapshot.Articles[0].ID, "{bad json"},
		{snapshot.Articles[1].ID, `{"articleID":"` + snapshot.Articles[1].ID + `","contentVersion":"v1","unitID":null,"offset":0,"scrollTop":0,"progress":2,"updatedAt":"2026-11-02T00:00:00.000Z"}`},
		{snapshot.Articles[2].ID, `{"articleID":"` + snapshot.Articles[2].ID + `","contentVersion":"v1","offset":0,"scrollTop":0,"progress":0,"updatedAt":"2026-11-02T00:00:00.000Z"}`},
		{snapshot.Articles[3].ID, `{"articleID":"` + snapshot.Articles[3].ID + `","contentVersion":"v1","unitID":null,"offset":0,"scrollTop":0,"progress":0,"updatedAt":"2026-11-02T00:00:00Z"}`},
		{snapshot.Articles[4].ID, `{"articleID":"` + snapshot.Articles[4].ID + `","contentVersion":"v1","unitID":null,"offset":1.5,"scrollTop":0,"progress":0.25,"updatedAt":"2026-11-02T00:00:00.000Z"}`},
	} {
		if _, err := db.Exec("INSERT INTO article_reading_positions(articleID, data) VALUES (?, ?)", row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	state := lib(t, mustOpen(t, path, snapshot))
	equal(t, len(state.Articles), len(snapshot.Articles), "articles")
	for i := 0; i < 4; i++ {
		if state.Articles[i].ReadingPosition != nil {
			t.Fatalf("damaged position %d was read", i)
		}
	}
	equal(t, state.Articles[4].ReadingPosition.Offset, 1.5, "valid position")
}
