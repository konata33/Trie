package library

import (
	"bytes"
	"encoding/json"
)

// The types the store reads and writes, with the JSON names libraries written
// by earlier versions use. A field that may be null is a pointer, one that may
// be absent is omitted when empty, and a string that may be absent or null is
// a MaybeString, which keeps all three apart.

// MaybeString is a string field that may be absent or null.
type MaybeString struct {
	Set   bool    // false when the field is absent
	Value *string // nil for null
}

func (m MaybeString) IsZero() bool                 { return !m.Set }
func (m MaybeString) MarshalJSON() ([]byte, error) { return json.Marshal(m.Value) }
func (m *MaybeString) UnmarshalJSON(b []byte) error {
	m.Set = true
	return json.Unmarshal(b, &m.Value)
}

// Some returns a MaybeString holding s.
func Some(s string) MaybeString { return MaybeString{Set: true, Value: &s} }

type SourceSnapshot struct {
	ID                    string  `json:"id"`
	Title                 string  `json:"title"`
	Host                  string  `json:"host"`
	HomeURL               string  `json:"homeURL"`
	Icon                  *string `json:"icon"`
	CheckedAt             string  `json:"checkedAt"`
	SampledCount          int     `json:"sampledCount"`
	CatalogCandidateCount int     `json:"catalogCandidateCount"`
	ScopeLabel            string  `json:"scopeLabel"`
	// The author's own words about the site, from the feed.
	Description MaybeString `json:"description,omitzero"`
}

type ArticleSnapshot struct {
	ID                string   `json:"id"`
	URL               string   `json:"url"`
	SourceID          string   `json:"sourceID"`
	Title             string   `json:"title"`
	Authors           []string `json:"authors"`
	PublishedAt       *string  `json:"publishedAt"`
	ModifiedAt        *string  `json:"modifiedAt"`
	Aliases           []string `json:"aliases"`
	BodyStatus        string   `json:"bodyStatus"`
	VerificationLabel string   `json:"verificationLabel"`
	// What a design work kept from Explore is shelved under there (landing, portfolio, motion, type, brand).
	Kinds []string `json:"kinds,omitempty"`
}

type ReviewItem struct {
	ID          string   `json:"id"`
	URL         string   `json:"url"`
	SourceID    string   `json:"sourceID"`
	Title       string   `json:"title"`
	Authors     []string `json:"authors"`
	PublishedAt *string  `json:"publishedAt"`
	Status      string   `json:"status"` // uncertain, non_article, failed or unprocessed
	Reason      string   `json:"reason"`
}

type Summary struct {
	SampledCount  int `json:"sampledCount"`
	ArticleCount  int `json:"articleCount"`
	PendingCount  int `json:"pendingCount"`
	ExcludedCount int `json:"excludedCount"`
	FailedCount   int `json:"failedCount"`
}

// Snapshot seeds a library: Open writes its records, never deleting any.
type Snapshot struct {
	Version     int               `json:"version"`
	AsOf        string            `json:"asOf"`
	Sources     []SourceSnapshot  `json:"sources"`
	Articles    []ArticleSnapshot `json:"articles"`
	ReviewItems []ReviewItem      `json:"reviewItems"`
	Summary     Summary           `json:"summary"`
}

type Source struct {
	SourceSnapshot
	Subscribed     bool    `json:"subscribed"`
	DisplayName    string  `json:"displayName"`
	FeedURL        *string `json:"feedURL"`
	LastFetchedAt  *string `json:"lastFetchedAt"`
	LastError      *string `json:"lastError"`
	FeedEntryCount *int    `json:"feedEntryCount"`
}

// Article is a library article; BodyStatus is not_extracted or the status of its cached content.
type Article struct {
	ArticleSnapshot
	Read     bool `json:"read"`
	Favorite bool `json:"favorite"`
	// When it was last kept; nil for favourites kept before this was recorded, and for articles not kept.
	FavoritedAt     *string          `json:"favoritedAt"`
	SourceIDs       []string         `json:"sourceIDs"`
	FeedVersion     *string          `json:"feedVersion"`
	ReadingPosition *ReadingPosition `json:"readingPosition,omitempty"`
	// What the list can say about the article before it is opened; nil until computed.
	Preview *ArticlePreview `json:"preview,omitempty"`
}

type ArticlePreview struct {
	// About the first 140 characters of real body text; nil when only a stub or link is known.
	Excerpt *string `json:"excerpt"`
	// Reading time with the reader's rule; nil when only a stub or summary is known.
	Minutes    *int `json:"minutes"`
	Images     int  `json:"images"`
	CodeBlocks int  `json:"codeBlocks"`
	// A lead image exists; Picture(id, 0) returns it.
	HasImage bool `json:"hasImage"`
	// Pictures available through Picture(id, index), at most 4.
	Gallery int `json:"gallery"`
}

type ReadingPositionInput struct {
	ArticleID      string  `json:"articleID"`
	ContentVersion string  `json:"contentVersion"`
	UnitID         *string `json:"unitID"`
	Offset         float64 `json:"offset"`
	ScrollTop      float64 `json:"scrollTop"`
	Progress       float64 `json:"progress"`
}

type ReadingPosition struct {
	ReadingPositionInput
	UpdatedAt string `json:"updatedAt"`
}

// ClipInput is a passage to keep; CreatedAt is empty for now, or the time of a clip being restored.
type ClipInput struct {
	ID              string        `json:"id"`
	ArticleID       string        `json:"articleID"`
	Text            string        `json:"text"`
	Note            string        `json:"note"`
	UnitID          *string       `json:"unitID"`
	ContentRevision *string       `json:"contentRevision"`
	CreatedAt       string        `json:"createdAt,omitempty"`
	Pictures        []ClipPicture `json:"pictures,omitempty"`
}

// ClipPicture is a picture of the body inside a clip's passage: the reader
// unit of its img, and where it stands in the clip's text.
type ClipPicture struct {
	Unit string `json:"unit"`
	At   int    `json:"at"` // in runes of Text: the picture stands before the words from here on
}

// Clip is a passage the reader kept. The citation is captured when the clip
// is made, so exports stay stable even if the feed later renames the article.
type Clip struct {
	ID              string        `json:"id"`
	ArticleID       string        `json:"articleID"`
	Text            string        `json:"text"`
	Note            string        `json:"note"`
	UnitID          *string       `json:"unitID"`
	ContentRevision *string       `json:"contentRevision"`
	CreatedAt       string        `json:"createdAt"`
	UpdatedAt       string        `json:"updatedAt"`
	Citation        Citation      `json:"citation"`
	Pictures        []ClipPicture `json:"pictures,omitempty"`
}

type Citation struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	SourceName  string   `json:"sourceName"`
	Authors     []string `json:"authors"`
	PublishedAt *string  `json:"publishedAt"`
}

// LibraryState is the whole library as the interface shows it.
type LibraryState struct {
	Version     int          `json:"version"`
	AsOf        string       `json:"asOf"`
	Revision    int          `json:"revision"`
	Sources     []Source     `json:"sources"`
	Articles    []Article    `json:"articles"`
	ReviewItems []ReviewItem `json:"reviewItems"`
	Clips       []Clip       `json:"clips"`
	Summary     Summary      `json:"summary"`
}

// LibraryAction is one of the action types below. Its JSON is a
// {"type": "toggle-favorite", ...} object.
type LibraryAction interface{ action() string }

type ToggleFavorite struct {
	ID string `json:"id"`
}

// SetFavorites keeps or lets go of several articles at once: a time keeps it
// as kept then (an undo puts it back in its place), nil lets it go.
type SetFavorites struct {
	Items []FavoriteTime `json:"items"`
}

type FavoriteTime struct {
	ID          string  `json:"id"`
	FavoritedAt *string `json:"favoritedAt"`
}

type MarkRead struct {
	ID   string `json:"id"`
	Read bool   `json:"read"`
}

type SetReadBatch struct {
	IDs  []string `json:"ids"`
	Read bool     `json:"read"`
}

type RenameSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SetSubscription struct {
	ID         string `json:"id"`
	Subscribed bool   `json:"subscribed"`
}

type AddClip struct {
	Clip ClipInput `json:"clip"`
}

type UpdateClipNote struct {
	ID   string `json:"id"`
	Note string `json:"note"`
}

type DeleteClip struct {
	ID string `json:"id"`
}

// Keep keeps an article met outside the subscriptions (a post read from
// Explore) under its author's source, which is added unsubscribed when the
// library has none. An article the library already has at the address
// stays as it is.
type Keep struct {
	Source  SourceSnapshot  `json:"source"`
	Name    string          `json:"name"`
	FeedURL string          `json:"feedURL"` // "" for an author without a feed
	Article ArticleSnapshot `json:"article"`
}

func (ToggleFavorite) action() string  { return "toggle-favorite" }
func (SetFavorites) action() string    { return "set-favorites" }
func (MarkRead) action() string        { return "mark-read" }
func (SetReadBatch) action() string    { return "set-read-batch" }
func (RenameSource) action() string    { return "rename-source" }
func (SetSubscription) action() string { return "set-subscription" }
func (Keep) action() string            { return "keep" }
func (AddClip) action() string         { return "add-clip" }
func (UpdateClipNote) action() string  { return "update-clip-note" }
func (DeleteClip) action() string      { return "delete-clip" }

func (a ToggleFavorite) MarshalJSON() ([]byte, error)  { type t ToggleFavorite; return typed(a, t(a)) }
func (a SetFavorites) MarshalJSON() ([]byte, error)    { type t SetFavorites; return typed(a, t(a)) }
func (a MarkRead) MarshalJSON() ([]byte, error)        { type t MarkRead; return typed(a, t(a)) }
func (a SetReadBatch) MarshalJSON() ([]byte, error)    { type t SetReadBatch; return typed(a, t(a)) }
func (a RenameSource) MarshalJSON() ([]byte, error)    { type t RenameSource; return typed(a, t(a)) }
func (a SetSubscription) MarshalJSON() ([]byte, error) { type t SetSubscription; return typed(a, t(a)) }
func (a AddClip) MarshalJSON() ([]byte, error)         { type t AddClip; return typed(a, t(a)) }
func (a UpdateClipNote) MarshalJSON() ([]byte, error)  { type t UpdateClipNote; return typed(a, t(a)) }
func (a DeleteClip) MarshalJSON() ([]byte, error)      { type t DeleteClip; return typed(a, t(a)) }

// typed writes fields with the action's type first.
func typed(a LibraryAction, fields any) ([]byte, error) {
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	kind, _ := json.Marshal(a.action())
	out := append([]byte(`{"type":`), kind...)
	if !bytes.Equal(body, []byte("{}")) {
		out = append(append(out, ','), body[1:len(body)-1]...)
	}
	return append(out, '}'), nil
}

type FeedBody struct {
	HTML    string  `json:"html"`
	Kind    string  `json:"kind"` // body or summary
	Version string  `json:"version"`
	FeedURL string  `json:"feedURL"`
	BaseURL *string `json:"baseURL,omitempty"`
}

type FeedEntry struct {
	ID                string    `json:"id"`
	URL               string    `json:"url"`
	Title             string    `json:"title"`
	Authors           []string  `json:"authors"`
	PublishedAt       *string   `json:"publishedAt"`
	ModifiedAt        *string   `json:"modifiedAt"`
	Aliases           []string  `json:"aliases"`
	BodyStatus        string    `json:"bodyStatus"`
	VerificationLabel string    `json:"verificationLabel"`
	FeedBody          *FeedBody `json:"feedBody"`
	// A picture the feed attaches to the entry outside its body, if any.
	Image MaybeString `json:"image,omitzero"`
}

type ParsedFeed struct {
	FeedURL   string      `json:"feedURL"`
	HomeURL   string      `json:"homeURL"`
	Title     string      `json:"title"`
	Articles  []FeedEntry `json:"articles"`
	FetchedAt string      `json:"fetchedAt"`
	Warnings  []string    `json:"warnings"`
	// The author's own one-line description of the site; absent keeps the one stored.
	Description MaybeString `json:"description,omitzero"`
}

type SubscriptionResult struct {
	Library       LibraryState `json:"library"`
	SourceID      string       `json:"sourceID"`
	ImportedCount int          `json:"importedCount"`
	UpdatedCount  int          `json:"updatedCount"`
}

// GalleryWork is a design-gallery entry read as a work page. Poster and video
// are page addresses straight after extraction and cached data URLs once
// read; nil when unavailable.
type GalleryWork struct {
	Name      string  `json:"name"`
	Designer  *string `json:"designer"`
	Site      *string `json:"site"`
	Host      *string `json:"host"`
	BuiltWith *string `json:"builtWith"`
	Facts     []struct {
		Label  string   `json:"label"`
		Values []string `json:"values"`
	} `json:"facts"`
	// Screenshots of the page's parts, each named as the gallery names it. A
	// shot not saved with the read keeps its source, to be fetched on its own later.
	Sections []struct {
		Name   string      `json:"name"`
		Image  *string     `json:"image"`
		Source MaybeString `json:"source,omitzero"`
	} `json:"sections"`
	Poster       *string     `json:"poster"`
	Video        *string     `json:"video"`
	PosterSource MaybeString `json:"posterSource,omitzero"`
	VideoSource  MaybeString `json:"videoSource,omitzero"`
}

type Heading struct {
	ID    string `json:"id"`
	Level int    `json:"level"`
	Text  string `json:"text"`
}

type CodeBlock struct {
	ID       string `json:"id"`
	Language string `json:"language"`
	Code     string `json:"code"`
}

type ReaderSourceSnapshot struct {
	HTML       string `json:"html"`
	Provenance string `json:"provenance"` // source or legacy-cache
}

// ArticleContent is an article's cached body. Work (a GalleryWork) and
// Document (the reader document) stay as stored JSON,
// so writing content back never drops what this package does not model.
type ArticleContent struct {
	Work              json.RawMessage       `json:"work,omitempty"`
	Document          json.RawMessage       `json:"document,omitempty"`
	SourceSnapshot    *ReaderSourceSnapshot `json:"sourceSnapshot,omitempty"`
	FormatVersion     int                   `json:"formatVersion,omitempty"`
	Headings          []Heading             `json:"headings,omitzero"`
	CodeBlocks        []CodeBlock           `json:"codeBlocks,omitzero"`
	HighlightStyles   []string              `json:"highlightStyles,omitzero"`
	ImageCacheVersion *int                  `json:"imageCacheVersion,omitempty"`
	MissingImageCount *int                  `json:"missingImageCount,omitempty"`
	ArticleID         string                `json:"articleID"`
	Status            string                `json:"status"` // ready, summary, restricted or unavailable
	HTML              string                `json:"html"`
	TextLength        int                   `json:"textLength"`
	Links             []string              `json:"links"`
	FetchedAt         string                `json:"fetchedAt"`
	SourceURL         string                `json:"sourceURL"`
	SourceKind        string                `json:"sourceKind"` // page or feed
	Message           *string               `json:"message"`
	FromCache         bool                  `json:"fromCache"`
	RefreshError      *string               `json:"refreshError,omitempty"`
	FeedVersion       *string               `json:"feedVersion,omitempty"`
}

// Site is where a source lives, for its first screen.
type Site struct {
	Host    string `json:"host"`
	HomeURL string `json:"homeURL"`
}

func ptr(s string) *string { return &s }
