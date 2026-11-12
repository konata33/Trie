// Package library is Trie's library database, a port of LibraryStore in
// apps/desktop/src/main/store.ts. It opens the SQLite file the Electron app
// writes, with the same tables, pragmas and checks, and keeps every field a
// row holds when it rewrites the row, including JSON keys it does not model.
package library

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"trie/internal/web"

	_ "modernc.org/sqlite" // pure Go; MyGo's plugin has no interactive transactions
)

// Store is an open library. Its methods may be called from any goroutine and
// run one at a time, as store.ts's do on Electron's main thread.
type Store struct {
	mu   sync.Mutex
	db   *sql.DB
	conn *sql.Conn // the one connection, so BEGIN and COMMIT reach the same session
}

var bg = context.Background()

const schema = `
        CREATE TABLE IF NOT EXISTS metadata (
          singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
          schemaVersion INTEGER NOT NULL, revision INTEGER NOT NULL, asOf TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS sources (
          id TEXT PRIMARY KEY, data TEXT NOT NULL, displayName TEXT NOT NULL,
          subscribed INTEGER NOT NULL DEFAULT 1 CHECK(subscribed IN (0, 1))
        );
        CREATE TABLE IF NOT EXISTS articles (
          id TEXT PRIMARY KEY, sourceID TEXT NOT NULL REFERENCES sources(id), data TEXT NOT NULL,
          read INTEGER NOT NULL DEFAULT 0 CHECK(read IN (0, 1)),
          favorite INTEGER NOT NULL DEFAULT 0 CHECK(favorite IN (0, 1))
        );
        CREATE TABLE IF NOT EXISTS reviews (
          id TEXT PRIMARY KEY, sourceID TEXT NOT NULL REFERENCES sources(id), data TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS article_content (
          articleID TEXT PRIMARY KEY REFERENCES articles(id), data TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS source_feeds (
          sourceID TEXT PRIMARY KEY REFERENCES sources(id), feedURL TEXT NOT NULL,
          fetchedAt TEXT NOT NULL, error TEXT, entryCount INTEGER NOT NULL
        );
        CREATE TABLE IF NOT EXISTS article_sources (
          articleID TEXT NOT NULL REFERENCES articles(id), sourceID TEXT NOT NULL REFERENCES sources(id),
          PRIMARY KEY(articleID, sourceID)
        );
        CREATE TABLE IF NOT EXISTS article_feed_content (
          articleID TEXT PRIMARY KEY REFERENCES articles(id), data TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS article_reading_positions (
          articleID TEXT PRIMARY KEY REFERENCES articles(id), data TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS clips (
          id TEXT PRIMARY KEY, articleID TEXT NOT NULL REFERENCES articles(id), data TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS article_previews (
          articleID TEXT PRIMARY KEY REFERENCES articles(id), data TEXT NOT NULL
        );
      `

// Open opens the library at path (":memory:" for a private one), creating
// it if missing, and writes seed's records into it: sources and articles a
// subscription has not taken over, and every review item. The app seeds an
// empty snapshot with today's date.
func Open(path string, seed Snapshot) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(bg)
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, conn: conn}
	if err := s.open(path, seed); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) open(path string, seed Snapshot) error {
	if err := s.exec("PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;"); err != nil {
		return err
	}
	if path != ":memory:" {
		if err := s.exec("PRAGMA journal_mode = WAL;"); err != nil {
			return err
		}
	}
	if err := s.exec(schema); err != nil {
		return err
	}
	// When an article was kept, so Favourites lists the latest keeps first; libraries from before it gain the column empty.
	if row, err := s.get("SELECT 1 FROM pragma_table_info('articles') WHERE name = 'favoritedAt'"); err != nil {
		return err
	} else if row == nil {
		if err := s.exec("ALTER TABLE articles ADD COLUMN favoritedAt TEXT"); err != nil {
			return err
		}
	}
	if err := s.tx(func() error { return s.seed(seed) }); err != nil {
		return err
	}
	return s.tx(s.refreshStale)
}

func (s *Store) seed(seed Snapshot) error {
	row, err := s.get("SELECT schemaVersion FROM metadata WHERE singleton = 1")
	if err != nil {
		return err
	}
	if row != nil {
		if v, ok := jsNumberValue(row[0]); !ok || v != 1 {
			return errors.New("Unsupported library schema")
		}
	}
	if err := s.exec(`INSERT INTO metadata VALUES (1, 1, 0, ?)
          ON CONFLICT(singleton) DO UPDATE SET asOf = excluded.asOf`, seed.AsOf); err != nil {
		return err
	}
	for _, item := range seed.Sources {
		data, err := s.rewrite("sources", "id", item.ID, item)
		if err == nil {
			err = s.exec(`INSERT INTO sources (id, data, displayName) VALUES (?, ?, ?)
          ON CONFLICT(id) DO UPDATE SET data = excluded.data
          WHERE NOT EXISTS(SELECT 1 FROM source_feeds WHERE sourceID = sources.id)`, item.ID, data, item.Title)
		}
		if err != nil {
			return err
		}
	}
	for _, item := range seed.Articles {
		data, err := s.rewrite("articles", "id", item.ID, item)
		if err == nil {
			err = s.exec(`INSERT INTO articles (id, sourceID, data) VALUES (?, ?, ?)
          ON CONFLICT(id) DO UPDATE SET sourceID = excluded.sourceID, data = excluded.data
          WHERE NOT EXISTS(SELECT 1 FROM article_sources r JOIN source_feeds f ON f.sourceID = r.sourceID WHERE r.articleID = articles.id)`, item.ID, item.SourceID, data)
		}
		if err != nil {
			return err
		}
	}
	for _, item := range seed.ReviewItems {
		data, err := s.rewrite("reviews", "id", item.ID, item)
		if err == nil {
			err = s.exec(`INSERT INTO reviews (id, sourceID, data) VALUES (?, ?, ?)
          ON CONFLICT(id) DO UPDATE SET sourceID = excluded.sourceID, data = excluded.data`, item.ID, item.SourceID, data)
		}
		if err != nil {
			return err
		}
	}
	return s.exec("INSERT OR IGNORE INTO article_sources(articleID, sourceID) SELECT id, sourceID FROM articles")
}

// refreshStale computes the previews a library lacks, or holds from older rules.
func (s *Store) refreshStale() error {
	rows, err := s.all(`SELECT a.id FROM articles a LEFT JOIN article_previews p ON p.articleID = a.id
          WHERE (p.articleID IS NULL OR json_extract(p.data, '$.version') IS NOT ?)
            AND (EXISTS(SELECT 1 FROM article_feed_content f WHERE f.articleID = a.id)
              OR EXISTS(SELECT 1 FROM article_content c WHERE c.articleID = a.id AND json_extract(c.data, '$.status') = 'ready'))`, previewVersion)
	if err != nil {
		return err
	}
	for _, row := range rows {
		var f fields
		id := f.str(row[0])
		if f.err != nil {
			return f.err
		}
		if err := s.refreshPreview(id); err != nil {
			return err
		}
	}
	return nil
}

// refreshPreview derives an article's preview from its best stored body once,
// so Library never parses article HTML.
func (s *Store) refreshPreview(id string) error {
	row, err := s.get(`SELECT json_extract(a.data, '$.title') AS title, json_extract(a.data, '$.url') AS url,
      CASE WHEN json_extract(c.data, '$.status') = 'ready' THEN json_extract(c.data, '$.html') END AS content,
      CASE WHEN json_extract(c.data, '$.status') = 'ready' THEN json_extract(c.data, '$.work') END AS work,
      json_extract(f.data, '$.html') AS feed, json_extract(f.data, '$.kind') AS kind, json_extract(f.data, '$.baseURL') AS baseURL
      FROM articles a LEFT JOIN article_content c ON c.articleID = a.id LEFT JOIN article_feed_content f ON f.articleID = a.id WHERE a.id = ?`, id)
	if err != nil {
		return err
	}
	drop := func() error { return s.exec("DELETE FROM article_previews WHERE articleID = ?", id) }
	if row == nil {
		return drop()
	}
	content, fromContent := row[2].(string)
	html, ok := content, fromContent
	if !ok {
		html, ok = row[4].(string)
	}
	if !ok {
		return drop()
	}
	base, ok := row[6].(string)
	if fromContent || !ok {
		var f fields
		if base = f.str(row[1]); f.err != nil {
			return f.err
		}
	}
	title, ok := row[0].(string)
	if !ok {
		return drop() // a list hint must never fail a save
	}
	var work *GalleryWork
	if text, isText := row[3].(string); isText {
		parsed, err := parseJSON(text)
		if err != nil {
			return drop()
		}
		if isGalleryWork(parsed) {
			work = new(GalleryWork)
			if json.Unmarshal([]byte(text), work) != nil {
				return drop()
			}
		}
	}
	p := articlePreview(html, title, base, !fromContent && row[5] == "summary", work)
	data, err := s.rewrite("article_previews", "articleID", id, struct {
		storedPreview
		Version int `json:"version"`
	}{p, previewVersion})
	if err != nil {
		return err
	}
	return s.exec(`INSERT INTO article_previews(articleID, data) VALUES (?, ?) ON CONFLICT(articleID) DO UPDATE SET data = excluded.data`, id, data)
}

// Picture is the nth picture of an article (index 0 to 3): an http(s)
// address, or the data URL read back from its cached page or work; "" when there is none.
func (s *Store) Picture(id string, index int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.get("SELECT json_extract(data, '$.sources["+strconv.Itoa(max(0, min(3, index)))+"]') AS src FROM article_previews WHERE articleID = ?", id)
	if err != nil || row == nil {
		return "", err
	}
	src, ok := row[0].(string)
	if !ok {
		return "", nil
	}
	if part, isWork := strings.CutPrefix(src, "work:"); isWork {
		c, err := s.content(id)
		if err != nil {
			return "", err
		}
		var w GalleryWork
		if c != nil && len(c.Work) > 0 {
			if err := json.Unmarshal(c.Work, &w); err != nil {
				return "", err
			}
		}
		var image, source *string
		if part == "poster" {
			image, source = w.Poster, w.PosterSource.Value
		} else if n := web.JSNumber(part); n >= 0 && n == math.Trunc(n) && n < float64(len(w.Sections)) {
			image, source = w.Sections[int(n)].Image, w.Sections[int(n)].Source.Value
		}
		switch {
		case truthy(image):
			return *image, nil
		case truthy(source):
			return *source, nil
		}
		return "", nil
	}
	if strings.HasPrefix(src, "inline:") {
		c, err := s.content(id)
		if err != nil || c == nil {
			return inlinePicture("", src), err
		}
		return inlinePicture(c.HTML, src), nil
	}
	return src, nil
}

// Library is the whole library.
func (s *Store) Library() (LibraryState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.library()
}

func (s *Store) library() (LibraryState, error) {
	var f fields
	meta, err := s.get("SELECT revision, asOf FROM metadata WHERE singleton = 1")
	if err != nil {
		return LibraryState{}, err
	}
	if meta == nil {
		return LibraryState{}, errors.New("Missing library metadata")
	}
	state := LibraryState{Version: 1, Sources: []Source{}, Articles: []Article{}, ReviewItems: []ReviewItem{}, Clips: []Clip{}}
	rows, err := s.all(`SELECT s.data, s.displayName, s.subscribed, f.feedURL, f.fetchedAt, f.error, f.entryCount
      FROM sources s LEFT JOIN source_feeds f ON f.sourceID = s.id ORDER BY s.rowid`)
	if err != nil {
		return LibraryState{}, err
	}
	for _, row := range rows {
		snapshot, err := sourceJSON(row[0])
		if err != nil {
			return LibraryState{}, err
		}
		source := Source{SourceSnapshot: snapshot, DisplayName: f.str(row[1])}
		if f.err != nil {
			return LibraryState{}, f.err
		}
		if source.Subscribed, err = storedBoolean(row[2]); err != nil {
			return LibraryState{}, err
		}
		source.FeedURL, source.LastFetchedAt, source.LastError = f.nstr(row[3]), f.nstr(row[4]), f.nstr(row[5])
		if row[6] != nil {
			n := f.count(row[6])
			source.FeedEntryCount = &n
		}
		if f.err != nil {
			return LibraryState{}, f.err
		}
		state.Sources = append(state.Sources, source)
	}
	memberships := map[string][]string{}
	if rows, err = s.all("SELECT articleID, sourceID FROM article_sources"); err != nil {
		return LibraryState{}, err
	}
	for _, row := range rows {
		id := f.str(row[0])
		memberships[id] = append(memberships[id], f.str(row[1]))
	}
	if f.err != nil {
		return LibraryState{}, f.err
	}
	positions := map[string]*ReadingPosition{}
	if rows, err = s.all("SELECT articleID, data FROM article_reading_positions"); err != nil {
		return LibraryState{}, err
	}
	for _, row := range rows {
		// A damaged position must not prevent the library from loading.
		if p := positionJSON(row[1], row[0]); p != nil {
			positions[p.ArticleID] = p
		}
	}
	rows, err = s.all(`SELECT a.data, a.read, a.favorite, a.favoritedAt, json_extract(c.data, '$.status') AS contentStatus,
      json_extract(f.data, '$.version') AS feedVersion, json_remove(p.data, '$.sources') AS preview FROM articles a
      LEFT JOIN article_content c ON c.articleID = a.id LEFT JOIN article_feed_content f ON f.articleID = a.id
      LEFT JOIN article_previews p ON p.articleID = a.id ORDER BY a.rowid`)
	if err != nil {
		return LibraryState{}, err
	}
	for _, row := range rows {
		snapshot, err := articleJSON(row[0])
		if err != nil {
			return LibraryState{}, err
		}
		status := "not_extracted"
		if jsTruthy(row[4]) {
			if status = f.str(row[4]); f.err != nil {
				return LibraryState{}, f.err
			}
		}
		var preview *ArticlePreview
		if row[6] != nil {
			preview = previewJSON(row[6])
		}
		if !slices.Contains([]string{"ready", "summary", "restricted", "unavailable", "not_extracted"}, status) {
			return LibraryState{}, errors.New("Invalid cached content status")
		}
		a := Article{ArticleSnapshot: snapshot, ReadingPosition: positions[snapshot.ID], Preview: preview}
		a.BodyStatus = status
		if a.Read, err = storedBoolean(row[1]); err == nil {
			a.Favorite, err = storedBoolean(row[2])
		}
		if err != nil {
			return LibraryState{}, err
		}
		a.FavoritedAt, a.FeedVersion = f.nstr(row[3]), f.nstr(row[5])
		if a.SourceIDs = memberships[snapshot.ID]; a.SourceIDs == nil {
			a.SourceIDs = []string{snapshot.SourceID}
		}
		if f.err != nil {
			return LibraryState{}, f.err
		}
		state.Articles = append(state.Articles, a)
	}
	if rows, err = s.all("SELECT data FROM reviews ORDER BY rowid"); err != nil {
		return LibraryState{}, err
	}
	for _, row := range rows {
		review, err := reviewJSON(row[0])
		if err != nil {
			return LibraryState{}, err
		}
		state.ReviewItems = append(state.ReviewItems, review)
	}
	if rows, err = s.all("SELECT articleID, data FROM clips"); err != nil {
		return LibraryState{}, err
	}
	for _, row := range rows {
		if clip := clipJSON(row[1], row[0]); clip != nil {
			state.Clips = append(state.Clips, *clip)
		}
	}
	slices.SortStableFunc(state.Clips, func(a, b Clip) int { return strings.Compare(b.CreatedAt, a.CreatedAt) })
	if state.AsOf = f.str(meta[1]); f.err == nil {
		state.Revision = f.count(meta[0])
	}
	if f.err != nil {
		return LibraryState{}, f.err
	}
	for _, source := range state.Sources {
		state.Summary.SampledCount += source.SampledCount
	}
	state.Summary.ArticleCount = len(state.Articles)
	for _, item := range state.ReviewItems {
		switch item.Status {
		case "uncertain", "unprocessed":
			state.Summary.PendingCount++
		case "non_article":
			state.Summary.ExcludedCount++
		case "failed":
			state.Summary.FailedCount++
		}
	}
	return state, nil
}

// Mutate applies an action as one transaction and returns the library after
// it. The action is checked first, as the Electron main process checks one
// from the renderer.
func (s *Store) Mutate(action LibraryAction) (LibraryState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkAction(action); err != nil {
		return LibraryState{}, err
	}
	err := s.tx(func() error {
		if err := s.apply(action); err != nil {
			return err
		}
		return s.exec("UPDATE metadata SET revision = revision + 1 WHERE singleton = 1")
	})
	if err != nil {
		return LibraryState{}, err
	}
	return s.library()
}

// checkAction is policy.ts's parseAction for a typed action.
func checkAction(action LibraryAction) error {
	id := func(id string) error {
		if id == "" || web.JSLen(id) > 4096 {
			return errors.New("无效的记录标识")
		}
		return nil
	}
	switch a := action.(type) {
	case nil:
		return errors.New("无效的操作")
	case ToggleFavorite:
		return id(a.ID)
	case SetFavorites:
		if len(a.Items) == 0 || len(a.Items) > 5000 {
			return errors.New("无效的批量收藏操作")
		}
		for _, item := range a.Items {
			if item.ID == "" || web.JSLen(item.ID) > 4096 || item.FavoritedAt != nil && (web.JSLen(*item.FavoritedAt) > 40 || !dateOK(*item.FavoritedAt)) {
				return errors.New("无效的批量收藏操作")
			}
		}
		return nil
	case MarkRead:
		return id(a.ID)
	case SetReadBatch:
		if len(a.IDs) > 5000 || slices.ContainsFunc(a.IDs, func(id string) bool { return id == "" || web.JSLen(id) > 4096 }) {
			return errors.New("无效的批量操作")
		}
		return nil
	case RenameSource:
		if name := web.JSTrim(a.Name); name == "" || web.JSLen(name) > 40 {
			return errors.New("订阅名称应为 1 至 40 个字")
		}
		return id(a.ID)
	case SetSubscription:
		return id(a.ID)
	case AddClip:
		return checkClip(a.Clip)
	case UpdateClipNote:
		if !clipID(a.ID) {
			return errors.New("无效的摘录标识")
		}
		if web.JSLen(a.Note) > 4000 {
			return errors.New("备注最多 4000 个字")
		}
		return nil
	case DeleteClip:
		if !clipID(a.ID) {
			return errors.New("无效的摘录标识")
		}
		return nil
	}
	return errors.New("不支持的操作")
}

func unique(ids []string) bool {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func (s *Store) apply(action LibraryAction) error {
	switch a := action.(type) {
	case ToggleFavorite:
		if err := s.articleExists(a.ID); err != nil {
			return err
		}
		// SET reads the old row, so the time is stamped only on the way in and cleared on the way out.
		return s.exec("UPDATE articles SET favorite = 1 - favorite, favoritedAt = CASE favorite WHEN 0 THEN ? ELSE NULL END WHERE id = ?", web.Now(), a.ID)
	case SetFavorites:
		ids := make([]string, len(a.Items))
		for i, item := range a.Items {
			ids[i] = item.ID
		}
		if len(ids) == 0 || len(ids) > 5000 || !unique(ids) {
			return errors.New("Favourites batch requires 1 to 5000 unique article IDs")
		}
		for _, item := range a.Items {
			if err := s.articleExists(item.ID); err != nil {
				return err
			}
			if item.FavoritedAt != nil && !dateOK(*item.FavoritedAt) {
				return errors.New("Invalid favourite time")
			}
		}
		for _, item := range a.Items {
			favorite, at := 0, (*string)(nil)
			if item.FavoritedAt != nil {
				ms, _ := web.JSDateParse(*item.FavoritedAt)
				favorite, at = 1, ptr(web.ISOTime(ms))
			}
			if err := s.exec("UPDATE articles SET favorite = ?, favoritedAt = ? WHERE id = ?", favorite, at, item.ID); err != nil {
				return err
			}
		}
		return nil
	case MarkRead:
		if err := s.articleExists(a.ID); err != nil {
			return err
		}
		return s.exec("UPDATE articles SET read = ? WHERE id = ?", a.Read, a.ID)
	case SetReadBatch:
		if len(a.IDs) > 5000 || !unique(a.IDs) {
			return errors.New("Batch requires at most 5000 unique article IDs")
		}
		for _, id := range a.IDs {
			if err := s.articleExists(id); err != nil {
				return err
			}
		}
		for _, id := range a.IDs {
			if err := s.exec("UPDATE articles SET read = ? WHERE id = ?", a.Read, id); err != nil {
				return err
			}
		}
		return nil
	case RenameSource:
		if err := s.sourceExists(a.ID); err != nil {
			return err
		}
		name := web.JSTrim(a.Name)
		if name == "" || web.JSLen(name) > 40 {
			return errors.New("Source name must contain 1 to 40 characters")
		}
		return s.exec("UPDATE sources SET displayName = ? WHERE id = ?", name, a.ID)
	case SetSubscription:
		if err := s.sourceExists(a.ID); err != nil {
			return err
		}
		return s.exec("UPDATE sources SET subscribed = ? WHERE id = ?", a.Subscribed, a.ID)
	case AddClip:
		input := a.Clip
		row, err := s.get("SELECT a.data, s.displayName FROM articles a JOIN sources s ON s.id = a.sourceID WHERE a.id = ?", input.ArticleID)
		if err != nil {
			return err
		}
		if row == nil {
			return errors.New("Unknown article ID")
		}
		exists, err := s.get("SELECT 1 FROM clips WHERE id = ?", input.ID)
		if err != nil {
			return err
		}
		if exists != nil {
			return errors.New("Clip already exists")
		}
		article, err := articleJSON(row[0])
		if err != nil {
			return err
		}
		var f fields
		at := web.Now()
		clip := Clip{ID: input.ID, ArticleID: input.ArticleID, Text: input.Text, Note: input.Note, UnitID: input.UnitID,
			ContentRevision: input.ContentRevision, CreatedAt: input.CreatedAt, UpdatedAt: at,
			Citation: Citation{Title: article.Title, URL: article.URL, SourceName: f.str(row[1]), Authors: article.Authors, PublishedAt: article.PublishedAt}}
		if clip.CreatedAt == "" {
			clip.CreatedAt = at
		}
		if f.err != nil {
			return f.err
		}
		data, err := stringify(clip)
		if err != nil {
			return err
		}
		return s.exec("INSERT INTO clips(id, articleID, data) VALUES (?, ?, ?)", clip.ID, clip.ArticleID, data)
	case UpdateClipNote:
		row, err := s.get("SELECT articleID, data FROM clips WHERE id = ?", a.ID)
		if err != nil {
			return err
		}
		var clip *Clip
		if row != nil {
			clip = clipJSON(row[1], row[0])
		}
		if clip == nil {
			return errors.New("Unknown clip ID")
		}
		if web.JSLen(a.Note) > 4000 {
			return errors.New("Clip note must contain at most 4000 characters")
		}
		// The stored clip with its note and time replaced: what store.ts writes,
		// with any field this package does not know kept.
		return s.exec("UPDATE clips SET data = json_set(?, '$.note', ?, '$.updatedAt', ?) WHERE id = ?", row[1], a.Note, web.Now(), clip.ID)
	case DeleteClip:
		result, err := s.conn.ExecContext(bg, "DELETE FROM clips WHERE id = ?", a.ID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return errors.New("Unknown clip ID")
		}
		return nil
	}
	return errors.New("Unsupported library action")
}

func (s *Store) articleExists(id string) error {
	row, err := s.get("SELECT 1 FROM articles WHERE id = ?", id)
	if err == nil && row == nil {
		err = errors.New("Unknown article ID")
	}
	return err
}

func (s *Store) sourceExists(id string) error {
	row, err := s.get("SELECT 1 FROM sources WHERE id = ?", id)
	if err == nil && row == nil {
		err = errors.New("Unknown source ID")
	}
	return err
}

// Clips are the named clips, in the order asked.
func (s *Store) Clips(ids []string) ([]Clip, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.library()
	if err != nil {
		return nil, err
	}
	out := make([]Clip, 0, len(ids))
	for _, id := range ids {
		i := slices.IndexFunc(state.Clips, func(c Clip) bool { return c.ID == id })
		if i < 0 {
			return nil, errors.New("Unknown clip ID")
		}
		out = append(out, state.Clips[i])
	}
	return out, nil
}

// SaveReadingPosition keeps where the reader is in an article. It does not
// change the library's revision.
func (s *Store) SaveReadingPosition(p ReadingPositionInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkPosition(p); err != nil {
		return err
	}
	if err := s.articleExists(p.ArticleID); err != nil {
		return err
	}
	data, err := s.rewrite("article_reading_positions", "articleID", p.ArticleID, ReadingPosition{p, web.Now()})
	if err != nil {
		return err
	}
	return s.exec(`INSERT INTO article_reading_positions(articleID, data) VALUES (?, ?)
      ON CONFLICT(articleID) DO UPDATE SET data = excluded.data`, p.ArticleID, data)
}

// Site is where a source lives, or nil when the source is unknown.
func (s *Store) Site(id string) (*Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.get("SELECT json_extract(data, '$.host') AS host, json_extract(data, '$.homeURL') AS homeURL FROM sources WHERE id = ?", id)
	if err != nil || row == nil {
		return nil, err
	}
	host, okHost := row[0].(string)
	home, okHome := row[1].(string)
	if !okHost || !okHome || host == "" || home == "" {
		return nil, nil
	}
	return &Site{Host: host, HomeURL: home}, nil
}

// Close closes the library.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.conn.Close(), s.db.Close())
}

// Content is an article's cached body, or nil when none is cached.
func (s *Store) Content(id string) (*ArticleContent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.content(id)
}

func (s *Store) content(id string) (*ArticleContent, error) {
	if err := s.articleExists(id); err != nil {
		return nil, err
	}
	row, err := s.get("SELECT data FROM article_content WHERE articleID = ?", id)
	if err != nil || row == nil {
		return nil, err
	}
	raw, err := rawObject(row[0])
	if err != nil {
		return nil, err
	}
	value := func(key string) any {
		if raw[key] == nil {
			return nil
		}
		v, _ := parseJSON(string(raw[key]))
		return v
	}
	var f fields
	status := f.str(value("status"))
	if f.err != nil {
		return nil, f.err
	}
	if !slices.Contains([]string{"ready", "summary", "restricted", "unavailable"}, status) {
		return nil, errors.New("Invalid cached content status")
	}
	c := &ArticleContent{ArticleID: id, Status: status, HTML: f.str(value("html")), TextLength: f.count(value("textLength")),
		Links: f.strs(value("links")), FetchedAt: f.str(value("fetchedAt")), SourceURL: f.str(value("sourceURL")),
		SourceKind: "page", Message: f.nstr(value("message")), FromCache: true}
	if value("sourceKind") == "feed" {
		c.SourceKind = "feed"
	}
	if f.err != nil {
		return nil, f.err
	}
	format := map[string]any{}
	for _, key := range []string{"formatVersion", "headings", "codeBlocks", "highlightStyles"} {
		format[key] = value(key)
	}
	if version, headings, blocks, styles, ok := formattedJSON(format); ok {
		c.FormatVersion, c.Headings, c.CodeBlocks, c.HighlightStyles = version, headings, blocks, styles
	}
	if raw["document"] != nil && IsReaderDocument(raw["document"]) {
		c.Document = raw["document"]
	}
	if raw["work"] != nil && isGalleryWork(value("work")) {
		c.Work = raw["work"]
	}
	c.SourceSnapshot = sourceSnapshotJSON(value("sourceSnapshot"))
	for key, to := range map[string]**int{"imageCacheVersion": &c.ImageCacheVersion, "missingImageCount": &c.MissingImageCount} {
		if v := value(key); v != nil {
			if _, isNumber := v.(json.Number); isNumber {
				n := f.count(v)
				*to = &n
			}
		}
	}
	if v, ok := value("feedVersion").(string); ok {
		c.FeedVersion = &v
	}
	return c, f.err
}

// rawObject is objectJSON with each value left as JSON text.
func rawObject(value any) (map[string]json.RawMessage, error) {
	text, ok := value.(string)
	if !ok {
		return nil, errors.New("Invalid stored JSON")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		var typeError *json.UnmarshalTypeError
		if errors.As(err, &typeError) {
			return nil, errors.New("Invalid stored object")
		}
		return nil, err
	}
	if raw == nil {
		return nil, errors.New("Invalid stored object")
	}
	return raw, nil
}

// SaveContent caches an article's body and refreshes its preview.
func (s *Store) SaveContent(content ArticleContent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.articleExists(content.ArticleID); err != nil {
		return err
	}
	if longer(content.HTML, 24*1024*1024) {
		return errors.New("Article cache size limit exceeded")
	}
	if len(content.Document) > 0 && string(content.Document) != "null" && !IsReaderDocument(content.Document) {
		return errors.New("Invalid structured article")
	}
	data, err := s.rewrite("article_content", "articleID", content.ArticleID, content)
	if err != nil {
		return err
	}
	if len(data) > 64*1024*1024 {
		return errors.New("Article cache size limit exceeded")
	}
	if err := s.exec(`INSERT INTO article_content(articleID, data) VALUES (?, ?)
      ON CONFLICT(articleID) DO UPDATE SET data = excluded.data`, content.ArticleID, data); err != nil {
		return err
	}
	return s.refreshPreview(content.ArticleID)
}

// ContentLinks are the links of every cached body.
func (s *Store) ContentLinks() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.all("SELECT data FROM article_content")
	if err != nil {
		return nil, err
	}
	links := []string{}
	for _, row := range rows {
		raw, err := rawObject(row[0])
		if err != nil {
			return nil, err
		}
		var v any
		if raw["links"] != nil {
			v, _ = parseJSON(string(raw["links"]))
		}
		var f fields
		if list := f.strs(v); f.err == nil {
			links = append(links, list...)
		} else {
			return nil, f.err
		}
	}
	return links, nil
}

// FeedContent is the body an article's feed carries, or nil.
func (s *Store) FeedContent(id string) (*FeedBody, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.feedContent(id)
}

func (s *Store) feedContent(id string) (*FeedBody, error) {
	row, err := s.get("SELECT data FROM article_feed_content WHERE articleID = ?", id)
	if err != nil || row == nil {
		return nil, err
	}
	v, err := objectJSON(row[0])
	if err != nil {
		return nil, err
	}
	kind, _ := v["kind"].(string)
	if kind != "body" && kind != "summary" {
		return nil, errors.New("Invalid feed content type")
	}
	var f fields
	body := &FeedBody{HTML: f.str(v["html"]), Kind: kind, Version: f.str(v["version"]), FeedURL: f.str(v["feedURL"])}
	if base, ok := v["baseURL"].(string); ok {
		body.BaseURL = &base
	}
	if f.err != nil {
		return nil, f.err
	}
	return body, nil
}

// ImportFeed saves a fetched feed as source sourceID, merging its entries
// with the articles already kept under any of their addresses. name is shown
// instead of the feed's title when the source is new ("" for the title); an
// existing source keeps its name. resubscribe subscribes the source again;
// icon is used when the source has none.
func (s *Store) ImportFeed(sourceID string, feed ParsedFeed, resubscribe bool, icon *string, name string) (SubscriptionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	imported, updated := map[string]bool{}, map[string]bool{}
	err := s.tx(func() error {
		before, err := s.library()
		if err != nil {
			return err
		}
		known := map[string]ArticleSnapshot{}
		remember := func(a ArticleSnapshot, urls ...string) error {
			for _, url := range urls {
				key, err := web.URLKey(url)
				if err != nil {
					return err
				}
				known[key] = a
			}
			return nil
		}
		for _, a := range before.Articles {
			if err := remember(a.ArticleSnapshot, append([]string{a.ID, a.URL}, a.Aliases...)...); err != nil {
				return err
			}
		}
		var previous *Source
		if i := slices.IndexFunc(before.Sources, func(s Source) bool { return s.ID == sourceID }); i >= 0 {
			previous = &before.Sources[i]
		}
		home, err := web.ParseURL(feed.HomeURL, nil)
		if err != nil {
			return err
		}
		source := SourceSnapshot{ID: sourceID, Title: feed.Title, Host: strings.TrimPrefix(home.Hostname(), "www."), HomeURL: feed.HomeURL,
			Icon: icon, CheckedAt: feed.FetchedAt, CatalogCandidateCount: len(feed.Articles),
			ScopeLabel: fmt.Sprintf("订阅源本次提供 %d 篇，已保存的旧文章会保留", len(feed.Articles))}
		if previous != nil && truthy(previous.Icon) {
			source.Icon = previous.Icon
		}
		if feed.Description.Set {
			source.Description = feed.Description
		} else if previous != nil && truthy(previous.Description.Value) {
			source.Description = previous.Description
		}
		data, err := s.rewrite("sources", "id", sourceID, source)
		if err != nil {
			return err
		}
		display := name
		if display == "" {
			display = feed.Title
		}
		if err := s.exec(`INSERT INTO sources(id,data,displayName,subscribed) VALUES(?,?,?,1)
        ON CONFLICT(id) DO UPDATE SET data=excluded.data`, sourceID, data, display); err != nil {
			return err
		}
		if resubscribe {
			if err := s.exec("UPDATE sources SET subscribed=1 WHERE id=?", sourceID); err != nil {
				return err
			}
		}
		if err := s.exec(`INSERT INTO source_feeds VALUES(?,?,?,NULL,?) ON CONFLICT(sourceID)
        DO UPDATE SET feedURL=excluded.feedURL,fetchedAt=excluded.fetchedAt,error=NULL,entryCount=excluded.entryCount`,
			sourceID, feed.FeedURL, feed.FetchedAt, len(feed.Articles)); err != nil {
			return err
		}
		fetched, fetchedOK := web.JSDateParse(feed.FetchedAt)
		for _, entry := range feed.Articles {
			entryKey, err := web.URLKey(entry.URL)
			if err != nil {
				return err
			}
			old, isOld := known[entryKey]
			id := entry.ID
			if isOld && old.ID != "" {
				id = old.ID
			}
			idKey, err := web.URLKey(id)
			if err != nil {
				return err
			}
			aliases := append(slices.Clone(old.Aliases), entry.Aliases...)
			if entryKey != idKey {
				aliases = append(aliases, entry.URL)
			}
			aliases = dedupe(aliases)
			owns := !isOld || old.SourceID == sourceID
			article := ArticleSnapshot{ID: id, URL: or(old.URL, entry.URL), SourceID: or(old.SourceID, sourceID), Title: entry.Title,
				Authors: entry.Authors, PublishedAt: orPtr(entry.PublishedAt, old.PublishedAt), ModifiedAt: orPtr(entry.ModifiedAt, old.ModifiedAt),
				Aliases: aliases, BodyStatus: "not_extracted", VerificationLabel: "来自 RSS/Atom 订阅"}
			if len(entry.Authors) == 0 {
				article.Authors = old.Authors
				if !isOld {
					article.Authors = []string{}
				}
			}
			if !owns {
				article.Title, article.Authors, article.PublishedAt, article.ModifiedAt, article.VerificationLabel = old.Title, old.Authors, old.PublishedAt, old.ModifiedAt, old.VerificationLabel
			}
			linked, err := s.get("SELECT 1 FROM article_sources WHERE articleID=? AND sourceID=?", id, sourceID)
			if err != nil {
				return err
			}
			if linked == nil {
				imported[id] = true
			} else if isOld && (old.Title != article.Title || !samePtr(old.ModifiedAt, article.ModifiedAt) || !samePtr(old.PublishedAt, article.PublishedAt) || !slices.Equal(old.Authors, article.Authors)) {
				updated[id] = true
			}
			// Only the last 30 days of a feed count as unread; older posts arrive already read.
			dated := orPtr(article.PublishedAt, article.ModifiedAt)
			stale := false
			if !isOld && dated != nil && fetchedOK {
				at, ok := web.JSDateParse(*dated)
				stale = ok && at < fetched-30*86_400_000
			}
			data, err := s.rewrite("articles", "id", id, article)
			if err == nil {
				err = s.exec("INSERT INTO articles(id,sourceID,data,read) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", id, article.SourceID, data, stale)
			}
			if err == nil {
				err = s.exec("INSERT OR IGNORE INTO article_sources VALUES(?,?)", id, sourceID)
			}
			if err != nil {
				return err
			}
			oldBody, err := s.feedContent(id)
			if err != nil {
				return err
			}
			if body := entry.FeedBody; owns && body != nil && !(oldBody != nil && oldBody.Kind == "body" && body.Kind == "summary") {
				changed := oldBody == nil || oldBody.Version != body.Version
				if linked != nil && changed {
					updated[id] = true
				}
				data, err := s.rewrite("article_feed_content", "articleID", id, body)
				if err == nil {
					err = s.exec("INSERT INTO article_feed_content VALUES(?,?) ON CONFLICT(articleID) DO UPDATE SET data=excluded.data", id, data)
				}
				if err == nil && changed {
					err = s.refreshPreview(id)
				}
				if err != nil {
					return err
				}
			}
			if err := remember(article, append([]string{id, article.URL}, aliases...)...); err != nil {
				return err
			}
		}
		for id := range imported {
			delete(updated, id)
		}
		day := web.Units(feed.FetchedAt)
		return s.exec("UPDATE metadata SET revision=revision+1, asOf=MAX(asOf,?) WHERE singleton=1", web.FromUnits(day[:min(10, len(day))]))
	})
	if err != nil {
		return SubscriptionResult{}, err
	}
	state, err := s.library()
	return SubscriptionResult{Library: state, SourceID: sourceID, ImportedCount: len(imported), UpdatedCount: len(updated)}, err
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func orPtr(a, b *string) *string {
	if truthy(a) {
		return a
	}
	if truthy(b) {
		return b
	}
	return nil
}

func samePtr(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func dedupe(list []string) []string {
	out := []string{}
	for _, s := range list {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// AdoptNames gives sources the reader never renamed the name nameFor finds
// for them ("" for none), for libraries subscribed before subscriptions took
// the catalog's name. A source still named by its feed's title was never
// renamed; once renamed here it no longer is, so running it again changes
// nothing. nameFor runs inside the store and must not call it.
func (s *Store) AdoptNames(nameFor func(feedURL *string, homeURL string) string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tx(func() error {
		rows, err := s.all(`SELECT s.id, s.displayName, json_extract(s.data, '$.homeURL') AS homeURL, f.feedURL FROM sources s
        LEFT JOIN source_feeds f ON f.sourceID = s.id WHERE s.displayName = json_extract(s.data, '$.title')`)
		if err != nil {
			return err
		}
		renamed := false
		for _, row := range rows {
			var f fields
			feedURL, home := f.nstr(row[3]), f.str(row[2])
			if f.err != nil {
				return f.err
			}
			if name := nameFor(feedURL, home); name != "" && name != row[1] {
				id := f.str(row[0])
				if f.err != nil {
					return f.err
				}
				if err := s.exec("UPDATE sources SET displayName=? WHERE id=?", name, id); err != nil {
					return err
				}
				renamed = true
			}
		}
		if renamed {
			return s.exec("UPDATE metadata SET revision=revision+1 WHERE singleton=1")
		}
		return nil
	})
}

// RecordFeedError notes why a source's feed could not be read.
func (s *Store) RecordFeedError(sourceID, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := web.Units(message)
	return s.tx(func() error {
		if err := s.exec("UPDATE source_feeds SET error=? WHERE sourceID=?", web.FromUnits(u[:min(1000, len(u))]), sourceID); err != nil {
			return err
		}
		return s.exec("UPDATE metadata SET revision=revision+1 WHERE singleton=1")
	})
}

// tx runs work under BEGIN IMMEDIATE, rolling back when it fails.
func (s *Store) tx(work func() error) (err error) {
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			s.exec("ROLLBACK")
			panic(p)
		}
		if err != nil {
			s.exec("ROLLBACK")
		}
	}()
	if err = work(); err != nil {
		return err
	}
	return s.exec("COMMIT")
}

func (s *Store) exec(query string, args ...any) error {
	_, err := s.conn.ExecContext(bg, query, args...)
	return err
}

// all reads every row before the next statement runs, as node:sqlite's all() does.
func (s *Store) all(query string, args ...any) ([][]any, error) {
	rows, err := s.conn.QueryContext(bg, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out [][]any
	for rows.Next() {
		values, targets := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		out = append(out, values)
	}
	return out, rows.Err()
}

// get is the first row, or nil.
func (s *Store) get(query string, args ...any) ([]any, error) {
	rows, err := s.all(query, args...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

// stringify is JSON.stringify: no HTML escaping.
func stringify(v any) (string, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// rewrite is v as the data of the row whose column is key, with every key of
// the stored row v's type does not have kept, so rewriting a row never drops
// what a newer version of either app stored in it.
func (s *Store) rewrite(table, column string, key any, v any) (string, error) {
	data, err := stringify(v)
	if err != nil {
		return "", err
	}
	row, err := s.get("SELECT CASE WHEN json_valid(data) AND json_type(data) = 'object' THEN json_remove(data, "+knownPaths(reflect.TypeOf(v))+") END FROM "+table+" WHERE "+column+" = ?", key)
	if err != nil || row == nil {
		return data, err
	}
	rest, _ := row[0].(string)
	if rest == "" || rest == "{}" {
		return data, nil
	}
	if data == "{}" {
		return rest, nil
	}
	return data[:len(data)-1] + "," + rest[1:], nil
}

var pathCache sync.Map

// knownPaths is the JSON paths of every field of a struct type, for json_remove.
func knownPaths(t reflect.Type) string {
	if paths, ok := pathCache.Load(t); ok {
		return paths.(string)
	}
	var names []string
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		for i := range t.NumField() {
			field := t.Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if field.Anonymous && name == "" {
				walk(field.Type)
			} else if name != "" && name != "-" {
				names = append(names, "'$."+name+"'")
			}
		}
	}
	walk(t)
	paths := strings.Join(names, ", ")
	pathCache.Store(t, paths)
	return paths
}
