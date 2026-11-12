// Package reading reads an article whole: its page fetched, its body extracted and formatted, its pictures downloaded
// and kept inside it, and the result kept in the library. Extraction runs one job at a time under a time limit
// (worker.go).
package reading

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"trie/internal/extract"
	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/web"

	"golang.org/x/text/encoding/unicode"
)

const (
	imageCacheVersion = 2
	imageBytes        = 8 * 1024 * 1024
	articleImageBytes = 15 * 1024 * 1024
	videoBytes        = 16 * 1024 * 1024
	pageBytes         = 2_000_000
	// A feed body shorter than this may be the whole article (a note) or only a lead-in: a publisher's opening paragraph, or a recommender's
	// picture and one-paragraph summary (Hacker News digests). The page is read too, and whichever gives more of the article is kept.
	shortFeedText = 1500
	// Below this a body is a picture with at most a caption (a gallery's screenshot, a photo post), not a lead-in to an article: it stays as it
	// is, because general extraction on a gallery page reads its navigation, related sites and advertising as text (minimal.gallery).
	pictureText = 40
	// A page shorter than this is a caption or a card, not an article; a feed's lone picture, or a gallery's work, stands instead.
	articleText = 600
	// A preview is a first look: it waits a few seconds for images, not the full 45 (none when only its text is wanted).
	PreviewWait = 6 * time.Second
	readWait    = 45 * time.Second
)

// Short feed bodies saved before they were checked against the page are read once more (fetchedAt older than this).
var shortFeedChecked = time.Date(2026, 11, 8, 12, 30, 0, 0, time.UTC).UnixMilli()

// Target is ReadTarget: everything retrieval needs to know about an article, whether or not it is in the library.
type Target struct {
	ID             string            `json:"id"`
	URL            string            `json:"url"`
	Aliases        []string          `json:"aliases"`
	HomeURL        string            `json:"homeURL"`
	FeedBody       *library.FeedBody `json:"feedBody"`
	TrustedArticle bool              `json:"trustedArticle"`
	// A design gallery's item as its feed knows it. When the item's page gives no work page of its own (only some galleries
	// have an adapter) and no fuller article, the reader shows this as one: the featured picture, the name and the site,
	// with whatever words were read.
	Work *Card `json:"work,omitempty"`
}

// Card is ReadTarget's work.
type Card struct {
	Name    string  `json:"name"`
	Site    *string `json:"site"`
	Picture *string `json:"picture"`
}

// Part is which of a work page's pictures WorkMedia fetches: a section's screenshot by its index, Poster or Video.
type Part int

const (
	Poster Part = -1
	Video  Part = -2
)

// Service reads articles and keeps them in the library.
type Service struct {
	store   *library.Store
	fetcher func() fetch.Fetch
	x       Extractors

	mu      sync.Mutex
	pending map[string]*call
	// Explore previews are not saved, so their work pages keep their sources here for the pictures fetched afterwards.
	previews map[string]library.ArticleContent
	order    []string // the previews' keys, oldest first
	// saving makes each look at the library and the write that follows it one step.
	saving sync.Mutex
}

type call struct {
	done    chan struct{}
	content library.ArticleContent
	err     error
}

// New makes a Service on store. A nil fetcher reads with fetch.New(160), a fresh budget for
// every read; the app's extraction is Worker.
func New(store *library.Store, fetcher func() fetch.Fetch, x Extractors) *Service {
	if fetcher == nil {
		fetcher = func() fetch.Fetch { return fetch.New(160).Fetch }
	}
	return &Service{store: store, fetcher: fetcher, x: x, pending: map[string]*call{}, previews: map[string]library.ArticleContent{}}
}

// once runs work for key, or waits for the run already under way and returns its result.
func (s *Service) once(key string, work func() (library.ArticleContent, error)) (library.ArticleContent, error) {
	s.mu.Lock()
	if c, ok := s.pending[key]; ok {
		s.mu.Unlock()
		<-c.done
		return c.content, c.err
	}
	c := &call{done: make(chan struct{})}
	s.pending[key] = c
	s.mu.Unlock()
	c.content, c.err = work()
	s.mu.Lock()
	delete(s.pending, key)
	s.mu.Unlock()
	close(c.done)
	return c.content, c.err
}

var (
	galleryBlocks = regexp.MustCompile(`<h2\b[^>]*>页面区块</h2>`)
	fragmentPart  = regexp.MustCompile(`#.`)
	pictureMarkup = regexp.MustCompile(`(?i)<figure\b[\s\S]*?</figure>|<img\b[^>]*>`)
	paragraphs    = regexp.MustCompile(`(?i)<p\b[^>]*>([\s\S]*?)</p>`)
	tags          = regexp.MustCompile(`<[^>]+>`)
	spaces        = regexp.MustCompile(`[` + web.Space + `]+`)
	svgType       = regexp.MustCompile(`\bimage/svg\+xml\b`)
)

// lower is toLowerCase for ASCII only, so a lowercase pattern matches as JavaScript's /i does.
func lower(s string) string { return strings.Map(web.LowerASCII, s) }

func decode(body []byte, contentType string) string {
	return fetch.Decode(body, fetch.Charset(contentType))
}

// utf8Text is body.toString('utf8'): ill-formed sequences read as U+FFFD, a byte order mark kept.
func utf8Text(body []byte) string {
	out, _ := unicode.UTF8.NewDecoder().Bytes(body)
	return string(out)
}

func plain(html string) string {
	return web.JSTrim(spaces.ReplaceAllString(strings.ReplaceAll(tags.ReplaceAllString(html, " "), "&nbsp;", " "), " "))
}

// holdsWords is whether outer contains the longest paragraph of inner, word for word: a feed description that holds
// the article the page gives.
func holdsWords(outer, inner string) bool {
	longest := ""
	for _, m := range paragraphs.FindAllStringSubmatch(inner, -1) {
		if p := plain(m[1]); web.JSLen(p) > web.JSLen(longest) {
			longest = p
		}
	}
	return web.JSLen(longest) >= 80 && strings.Contains(plain(outer), longest)
}

func hasWork(work json.RawMessage) bool { return len(work) > 0 && string(work) != "null" }

func str(s string) *string { return &s }

func withError(c library.ArticleContent, message string) library.ArticleContent {
	c.RefreshError = &message
	return c
}

func unavailable(id, url string) library.ArticleContent {
	return library.ArticleContent{ArticleID: id, Status: "unavailable", Links: []string{}, SourceURL: url, SourceKind: "page",
		Message: str("暂时无法读取正文，请重试或打开原文查看")}
}

// Read is read: the article's body, from the library while what is kept is current, else read and kept. refresh reads
// the page again; source is "page" or "feed" for the source chosen in the reading options, "" to decide. A read that
// fails gives what is kept with a RefreshError, or an unavailable body; the error is for an article not in the library,
// or the library failing.
func (s *Service) Read(ctx context.Context, id string, refresh bool, source string) (library.ArticleContent, error) {
	state, err := s.store.Library()
	if err != nil {
		return library.ArticleContent{}, err
	}
	at := slices.IndexFunc(state.Articles, func(a library.Article) bool { return a.ID == id })
	if at < 0 {
		return library.ArticleContent{}, errors.New("文章不在当前阅读库中")
	}
	articleURL := state.Articles[at].URL
	cached, err := s.store.Content(id)
	if err != nil {
		return library.ArticleContent{}, err
	}
	feedBody, err := s.store.FeedContent(id)
	if err != nil {
		return library.ArticleContent{}, err
	}
	var oldImages, staleTeaser, staleGallery, staleShort bool
	if c := cached; c != nil {
		oldImages = (c.ImageCacheVersion == nil || *c.ImageCacheVersion != imageCacheVersion) &&
			(c.Message != nil && strings.Contains(*c.Message, "部分图片暂未保存") || c.MissingImageCount != nil && *c.MissingImageCount != 0)
		// A cached feed lead-in from before teasers were recognised is read again, so the page behind it can replace it.
		snapshot := c.HTML
		if c.SourceSnapshot != nil {
			snapshot = c.SourceSnapshot.HTML
		}
		staleTeaser = c.SourceKind == "feed" && c.Status == "ready" && extract.IsTeaser(snapshot)
		// So is a gallery entry saved as plain text before work pages existed.
		staleGallery = !hasWork(c.Work) && (c.Message != nil && strings.HasPrefix(*c.Message, "这是设计画廊的作品页") || galleryBlocks.MatchString(c.HTML))
		// And so is a short feed body kept before the page was compared with it.
		fetched, ok := web.JSDateParse(c.FetchedAt)
		staleShort = c.SourceKind == "feed" && c.Status == "ready" && c.TextLength < shortFeedText && c.TextLength >= pictureText && !(ok && fetched >= shortFeedChecked)
		if !refresh && source == "" && !oldImages && !staleTeaser && !staleGallery && !staleShort &&
			(feedBody == nil || c.FeedVersion != nil && *c.FeedVersion == feedBody.Version) {
			return s.formatCached(*c), nil
		}
	}
	version := "page"
	if feedBody != nil && feedBody.Version != "" {
		version = feedBody.Version
	}
	return s.once(id+":"+version+":"+strconv.FormatBool(refresh)+":"+source, func() (library.ArticleContent, error) {
		// Updating the body reads the page again, as before; a chosen source is read as chosen.
		from := source
		if from == "" {
			from = "auto"
			if refresh || oldImages && cached.SourceKind == "page" {
				from = "page"
			}
		}
		content, err := s.retrieve(ctx, id, from)
		if err == nil {
			content, err = s.settle(id, cached, content)
		}
		if err != nil {
			log.Print("Article read failed: ", err)
			if cached != nil {
				return withError(s.formatCached(*cached), "暂时无法获取新版内容，继续显示本机保存的内容"), nil
			}
			return unavailable(id, articleURL), nil
		}
		return content, nil
	})
}

// settle is what read does with a body it has read: keeps what was saved when the new one is worse, formats and saves it.
func (s *Service) settle(id string, cached *library.ArticleContent, content library.ArticleContent) (library.ArticleContent, error) {
	if cached != nil && cached.Status == "ready" && cached.HTML != "" && content.Status != "ready" {
		message := "来源暂未提供新版正文，继续显示已保存的内容"
		if content.Message != nil && *content.Message != "" {
			message = *content.Message
		}
		return withError(s.formatCached(*cached), message), nil
	}
	if cached != nil && cached.Status == "ready" && cached.ImageCacheVersion != nil && *cached.ImageCacheVersion == imageCacheVersion &&
		cached.MissingImageCount != nil && *cached.MissingImageCount == 0 && content.MissingImageCount != nil && *content.MissingImageCount != 0 {
		return withError(s.formatCached(*cached), "新版图片暂未保存完整，继续显示已保存的内容"), nil
	}
	if formatted, err := s.formatContent(content); err == nil {
		content = formatted
	} else if cached != nil && cached.HTML != "" {
		return withError(s.formatCached(*cached), "新版排版暂时不可用，继续显示已保存的内容"), nil
	} else {
		content = withError(content, "增强排版暂时不可用，已保留可阅读的正文")
	}
	s.saving.Lock()
	defer s.saving.Unlock()
	current, err := s.store.FeedContent(id)
	if err != nil {
		return content, err
	}
	same := current == nil && content.FeedVersion == nil || current != nil && content.FeedVersion != nil && current.Version == *content.FeedVersion
	if web.JSTrim(content.HTML) != "" && same {
		err = s.store.SaveContent(content)
	}
	return content, err
}

func (s *Service) formatContent(c library.ArticleContent) (library.ArticleContent, error) {
	if c.HTML == "" || s.x.Format == nil || c.FormatVersion == extract.FormatVersion && library.IsReaderDocument(c.Document) {
		return c, nil
	}
	// Legacy caches have no independent source; do not duplicate their cached photo bytes.
	var snapshot *library.ReaderSourceSnapshot
	if c.SourceSnapshot != nil && c.SourceSnapshot.Provenance == "source" {
		snapshot = c.SourceSnapshot
	}
	f, err := s.x.Format(c.HTML, snapshot)
	if err != nil {
		return c, err
	}
	if web.JSTrim(f.HTML) == "" && c.Status != "restricted" {
		return c, errors.New("未取得可显示的正文")
	}
	c.HTML, c.FormatVersion, c.Headings, c.CodeBlocks, c.HighlightStyles, c.Document = f.HTML, f.FormatVersion, f.Headings, f.CodeBlocks, f.HighlightStyles, f.Document
	c.SourceSnapshot = snapshot
	return c, nil
}

func (s *Service) formatCached(cached library.ArticleContent) library.ArticleContent {
	if s.x.Format == nil || cached.FormatVersion == extract.FormatVersion && library.IsReaderDocument(cached.Document) {
		return cached
	}
	snapshot, provenance := "", "legacy-cache"
	if cached.SourceSnapshot != nil {
		snapshot = cached.SourceSnapshot.HTML
		if cached.SourceSnapshot.Provenance != "" {
			provenance = cached.SourceSnapshot.Provenance
		}
	}
	sum := sha256.Sum256([]byte(cached.HTML + "\x00" + snapshot + "\x00" + provenance))
	content, _ := s.once("format:"+cached.ArticleID+":"+hex.EncodeToString(sum[:]), func() (library.ArticleContent, error) {
		formatted, err := s.formatContent(cached)
		if err == nil {
			s.saving.Lock()
			defer s.saving.Unlock()
			var current *library.ArticleContent
			if current, err = s.store.Content(cached.ArticleID); err == nil {
				if current == nil || current.HTML != cached.HTML || !sameSnapshot(current.SourceSnapshot, cached.SourceSnapshot) {
					return formatted, nil
				}
				upgraded := *current
				upgraded.HTML, upgraded.FormatVersion, upgraded.Headings, upgraded.CodeBlocks, upgraded.HighlightStyles, upgraded.Document, upgraded.SourceSnapshot =
					formatted.HTML, formatted.FormatVersion, formatted.Headings, formatted.CodeBlocks, formatted.HighlightStyles, formatted.Document, formatted.SourceSnapshot
				if err = s.store.SaveContent(upgraded); err == nil {
					return upgraded, nil
				}
			}
		}
		return withError(cached, "阅读排版暂时无法更新，继续显示已保存的内容"), nil
	})
	return content
}

func sameSnapshot(a, b *library.ReaderSourceSnapshot) bool {
	return a == nil && b == nil || a != nil && b != nil && a.HTML == b.HTML && a.Provenance == b.Provenance
}

// WorkMedia is workMedia: a work-page picture the read did not save in time (a screenshot by index, the first screen
// or the recording), fetched now, kept with the article and returned as a data URL; "" when it cannot be had.
func (s *Service) WorkMedia(ctx context.Context, id string, part Part) (string, error) {
	c, err := s.stored(id)
	if err != nil || c == nil || !hasWork(c.Work) {
		return "", err
	}
	var w library.GalleryWork
	if err := json.Unmarshal(c.Work, &w); err != nil {
		return "", err
	}
	var saved, source *string
	kind := "image"
	switch {
	case part == Poster:
		saved, source, kind = w.Poster, w.PosterSource.Value, "poster"
	case part == Video:
		saved, source, kind = w.Video, w.VideoSource.Value, "video"
	case part >= 0 && int(part) < len(w.Sections):
		saved, source = w.Sections[part].Image, w.Sections[part].Source.Value
	}
	if saved != nil && *saved != "" || source == nil || *source == "" {
		if saved == nil {
			return "", nil
		}
		return *saved, nil
	}
	data := s.media(ctx, s.fetcher(), source, kind, time.Now().Add(60*time.Second))
	if data == nil {
		return "", nil
	}
	s.saving.Lock()
	defer s.saving.Unlock()
	s.mu.Lock()
	if p, ok := s.previews[id]; ok {
		s.previews[id] = withPart(p, part, data)
		s.mu.Unlock()
		return *data, nil
	}
	s.mu.Unlock()
	current, err := s.store.Content(id)
	if err == nil && current != nil && hasWork(current.Work) {
		err = s.store.SaveContent(withPart(*current, part, data))
	}
	if err != nil {
		return "", err
	}
	return *data, nil
}

func (s *Service) stored(id string) (*library.ArticleContent, error) {
	s.mu.Lock()
	p, ok := s.previews[id]
	s.mu.Unlock()
	if ok {
		return &p, nil
	}
	return s.store.Content(id)
}

// withPart is c with one picture of its work saved, its source cleared.
func withPart(c library.ArticleContent, part Part, data *string) library.ArticleContent {
	var w library.GalleryWork
	if !hasWork(c.Work) || json.Unmarshal(c.Work, &w) != nil {
		return c
	}
	cleared := library.MaybeString{Set: true}
	switch {
	case part == Poster:
		w.Poster, w.PosterSource = data, cleared
	case part == Video:
		w.Video, w.VideoSource = data, cleared
	case part >= 0 && int(part) < len(w.Sections):
		w.Sections[part].Image, w.Sections[part].Source = data, cleared
	}
	c.Work, _ = json.Marshal(w)
	return c
}

// Preview is preview: an article not in the library, such as an Explore post, read without saving anything. imageWait
// is how long its pictures are waited for (PreviewWait; none when only its text is wanted).
func (s *Service) Preview(ctx context.Context, t Target, source string, imageWait time.Duration) library.ArticleContent {
	if source == "" {
		source = "auto"
	}
	content, err := s.fetchContent(ctx, t, source, imageWait)
	if err != nil {
		content = unavailable(t.ID, t.URL)
	}
	if hasWork(content.Work) {
		s.mu.Lock()
		if _, ok := s.previews[t.ID]; !ok {
			s.order = append(s.order, t.ID)
		}
		s.previews[t.ID] = content
		for len(s.order) > 8 {
			delete(s.previews, s.order[0])
			s.order = s.order[1:]
		}
		s.mu.Unlock()
	}
	formatted, err := s.formatContent(content)
	if err != nil {
		if content.HTML != "" {
			return withError(content, "增强排版暂时不可用，已保留可阅读的正文")
		}
		return content
	}
	return formatted
}

func (s *Service) retrieve(ctx context.Context, id, from string) (library.ArticleContent, error) {
	state, err := s.store.Library()
	if err != nil {
		return library.ArticleContent{}, err
	}
	at := slices.IndexFunc(state.Articles, func(a library.Article) bool { return a.ID == id })
	if at < 0 {
		return library.ArticleContent{}, errors.New("文章不在当前阅读库中")
	}
	article := state.Articles[at]
	source := func(id string) *library.Source {
		if i := slices.IndexFunc(state.Sources, func(s library.Source) bool { return s.ID == id }); i >= 0 {
			return &state.Sources[i]
		}
		return nil
	}
	home := source(article.SourceID)
	if home == nil {
		return library.ArticleContent{}, errors.New("文章的来源不在当前阅读库中")
	}
	feedBody, err := s.store.FeedContent(id)
	if err != nil {
		return library.ArticleContent{}, err
	}
	trusted := slices.ContainsFunc(article.SourceIDs, func(id string) bool {
		src := source(id)
		return src != nil && src.FeedURL != nil && *src.FeedURL != ""
	})
	return s.fetchContent(ctx, Target{ID: id, URL: article.URL, Aliases: article.Aliases, HomeURL: home.HomeURL, FeedBody: feedBody, TrustedArticle: trusted}, from, readWait)
}

func anywhere(string) bool { return true }

// retrying is a read's fetch: a reset connection is tried again after a pause, twice. While the app starts it reads
// every Explore source and captures sites at once, and a proxy then drops new connections in bursts that an immediate
// retry falls into too.
func retrying(once fetch.Fetch) fetch.Fetch {
	return func(ctx context.Context, url string, scope func(string) bool, maxBytes int) (*fetch.Response, error) {
		for _, pause := range []time.Duration{400 * time.Millisecond, 1200 * time.Millisecond} {
			res, err := once(ctx, url, scope, maxBytes)
			var ne *fetch.NetError
			if !errors.As(err, &ne) || !ne.Transient {
				return res, err
			}
			select {
			case <-time.After(pause):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return once(ctx, url, scope, maxBytes)
	}
}

func (s *Service) fetchContent(ctx context.Context, t Target, from string, imageWait time.Duration) (library.ArticleContent, error) {
	get := retrying(s.fetcher())
	var result, summary *extract.Extracted
	picture := false
	payload := t.FeedBody
	sourceURL, sourceKind := t.URL, "page"
	fromFeed := func(x *extract.Extracted) { result, sourceKind, sourceURL = x, "feed", payload.FeedURL }
	if payload != nil && s.x.Fragment != nil && from != "page" {
		base := t.URL
		if payload.BaseURL != nil && *payload.BaseURL != "" {
			base = *payload.BaseURL
		}
		embedded, err := s.x.Fragment(payload.HTML, base, payload.Kind)
		if err != nil {
			return library.ArticleContent{}, err
		}
		// Complete as the feed gives it: a picture with at most a caption, or one section of a bigger page (tonsky.me/design/#2026-07-squawkbox
		// is one logo; the page is the whole portfolio).
		// A body that is only a picture may be the whole post (a photo, a logo) or a magazine's lead picture for an article on the page
		// (It's Nice That, Abduzeedo): the page is read, and kept only when it is an article (articleText) rather than the picture's page.
		picture = embedded.Status == "ready" && len(embedded.Images) > 0 && embedded.TextLength < pictureText
		section := fragmentPart.MatchString(t.URL) || payload.BaseURL != nil && fragmentPart.MatchString(*payload.BaseURL)
		if embedded.Status == "restricted" || embedded.Status == "ready" && (embedded.TextLength >= shortFeedText || section || from == "feed") {
			fromFeed(&embedded)
		} else if embedded.Status == "ready" || embedded.Status == "summary" {
			// A short body waits for the page, and stands in for it when the page cannot be read.
			summary = &embedded
		}
	}
	if result == nil {
		// A post may also move to its publisher's own site: an old domain the feed still links to (paco.sh → paco.me).
		post, err := fetch.PublisherScope(t.URL)
		var home func(string) bool
		if err == nil {
			home, err = fetch.PublisherScope(t.HomeURL)
		}
		var page *fetch.Response
		if err == nil {
			page, err = get(ctx, t.URL, func(u string) bool { return post(u) || home(u) }, pageBytes)
		}
		if err == nil {
			sourceURL = page.URL
			switch {
			case page.Status == 401 || page.Status == 402 || page.Status == 403:
				if summary == nil {
					status, message := "restricted", "原站要求登录或付费，请前往作者网站完成授权后阅读"
					if page.Status == 403 {
						status, message = "unavailable", "原站拒绝了本次读取，请打开原文确认访问方式"
					}
					return library.ArticleContent{ArticleID: t.ID, Status: status, Links: []string{}, FetchedAt: web.Now(), SourceURL: page.URL, SourceKind: "page", Message: &message}, nil
				}
			case page.Status >= 200 && page.Status < 300 && strings.Contains(lower(page.Type), "html"):
				var x extract.Extracted
				if x, err = s.x.Page(decode(page.Body, page.Type), page.URL, t.TrustedArticle); err == nil {
					result = &x
				}
			}
		}
		if err != nil && summary == nil {
			return library.ArticleContent{}, err
		}
		switch {
		// A page that cannot be read, or one judged members-only that shows less than the feed gave (Mindsparkle), leaves the feed's version.
		case summary != nil && (result == nil || result.Status == "unavailable" || result.Status == "restricted" && result.TextLength < summary.TextLength):
			fromFeed(summary)
		case picture && summary != nil && result != nil && result.Status == "ready" && result.Work == nil && result.TextLength < articleText:
			fromFeed(summary)
		// A feed's description that already holds the page's words (its longest paragraph, word for word) and pictures the page's extraction left behind
		// is the whole article and the fuller body (Fonts In Use keeps a post's pictures outside the text the extractor keeps, so the page gives words only).
		// Never for a work page: a gallery entry's recording, first screen and screenshots live outside its text, so the feed's one picture
		// would always look like more (One Page Love lost its recordings to this).
		case summary != nil && result != nil && result.Status == "ready" && result.Work == nil && len(summary.Images) > len(result.Images) && holdsWords(summary.HTML, result.HTML):
			whole := *summary
			whole.Status, whole.Message = "ready", str("正文由订阅源提供")
			fromFeed(&whole)
		// The page replaces a short feed body only when it clearly holds more (half as much again): the same post with a date line or a
		// caption around it is no reason to leave the feed's version, which keeps its pictures where the author placed them.
		case summary != nil && summary.Status == "ready" && result != nil && result.Status == "ready" && result.Work == nil && float64(summary.TextLength)*1.5 >= float64(result.TextLength):
			fromFeed(summary)
		}
	}
	// A gallery's item with no work page and no fuller article stands as a work built from its feed; the words read stay as its body.
	if card := t.Work; card != nil && (result == nil || result.Work == nil) &&
		(result == nil || result.Status != "ready" || result.TextLength < articleText || len(result.Images) == 0 && card.Picture != nil && *card.Picture != "") {
		words := ""
		if result != nil && result.Status != "unavailable" {
			words = result.HTML
		} else if summary != nil {
			words = summary.HTML
		}
		words = pictureMarkup.ReplaceAllString(words, "")
		var site, host *string
		if card.Site != nil && *card.Site != "" {
			if u, err := web.ParseURL(*card.Site, nil); err == nil {
				host = str(strings.TrimPrefix(u.Hostname(), "www."))
				if *host != "" {
					site = card.Site
				}
			}
		}
		length := web.JSLen(web.JSTrim(spaces.ReplaceAllString(tags.ReplaceAllString(words, " "), " ")))
		// Markup left with no words (an empty link around the shot) is no body; the work page shows its picture alone.
		if length == 0 {
			words = ""
		}
		links := []string{}
		if result != nil && result.Links != nil {
			links = result.Links
		}
		result = &extract.Extracted{Status: "ready", HTML: words, TextLength: length, Links: links, Images: []string{}, FeedURLs: []string{},
			Work: &extract.Work{Name: card.Name, Site: site, Host: host, Facts: []extract.Fact{}, Sections: []extract.Shot{}, Poster: card.Picture}}
	}
	if result == nil || result.Status == "unavailable" {
		var feeds []string
		if result != nil {
			feeds = slices.Clone(result.FeedURLs)
		}
		if len(feeds) == 0 {
			scope, err := fetch.PublisherScope(t.HomeURL)
			if err != nil {
				return library.ArticleContent{}, err
			}
			home, err := get(ctx, t.HomeURL, scope, pageBytes)
			if err != nil {
				return library.ArticleContent{}, err
			}
			if home.Status == 200 && strings.Contains(lower(home.Type), "html") {
				x, err := s.x.Page(decode(home.Body, home.Type), home.URL, false)
				if err != nil {
					return library.ArticleContent{}, err
				}
				feeds = x.FeedURLs
			}
		}
		feeds = unique(feeds)
		for _, feed := range feeds[:min(2, len(feeds))] {
			// Another publisher-declared source may still be readable when one fails.
			if _, err := fetch.PublicURL(feed); err != nil {
				continue
			}
			scope, err := fetch.PublisherScope(feed)
			if err != nil {
				continue
			}
			res, err := get(ctx, feed, scope, pageBytes)
			if err != nil || res.Status != 200 {
				continue
			}
			found, err := s.x.Feed(decode(res.Body, res.Type), res.URL, append([]string{t.ID, t.URL}, t.Aliases...))
			if err == nil && found != nil && found.Status != "unavailable" {
				result, sourceURL, sourceKind = found, res.URL, "feed"
				break
			}
		}
	}
	if result == nil || result.Status == "unavailable" {
		return library.ArticleContent{}, errors.New("未找到可读正文")
	}
	urls := unique(result.Images)
	images := map[string]string{}
	var mu sync.Mutex
	kept, next := 0, 0
	deadline := time.Now().Add(imageWait)
	// Bound work and cached bytes while allowing photo-heavy posts to load in parallel: six at a time, as a browser opens to one site,
	// since a slow picture CDN (Medium's, one to five seconds a picture through a proxy) otherwise fills a preview's few seconds with half the pictures.
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			for {
				mu.Lock()
				if next >= min(len(urls), 64) || kept >= articleImageBytes || !time.Now().Before(deadline) {
					mu.Unlock()
					return
				}
				url := urls[next]
				next++
				mu.Unlock()
				// A failed image must not erase the article text.
				body, kind := s.image(ctx, get, url)
				mu.Lock()
				if kind != "" && kept+len(body) <= articleImageBytes {
					images[url] = "data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(body)
					kept += len(body)
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	html, err := s.x.Images(result.HTML, images)
	if err != nil {
		return library.ArticleContent{}, err
	}
	// A work page's recording, first screen and screenshots are kept like its images, so the reader never reaches the network.
	// They share one short wait so a slow gallery CDN does not hold the page back; whatever misses it keeps its
	// source and is fetched on its own once the page is showing (WorkMedia).
	var work json.RawMessage
	if found := result.Work; found != nil {
		until := deadline
		if soon := time.Now().Add(8 * time.Second); soon.Before(until) {
			until = soon
		}
		var w library.GalleryWork
		raw, _ := json.Marshal(found)
		if err := json.Unmarshal(raw, &w); err != nil {
			return library.ArticleContent{}, err
		}
		for i := range w.Sections {
			w.Sections[i].Source = library.MaybeString{Set: true, Value: w.Sections[i].Image}
		}
		var wg sync.WaitGroup
		wg.Go(func() { w.Poster = s.media(ctx, get, found.Poster, "poster", until) })
		wg.Go(func() { w.Video = s.media(ctx, get, found.Video, "video", until) })
		var mu sync.Mutex
		nextShot := 0
		// Three at a time, like the body's images: a CDN answering a dozen at once lets some run past the wait.
		for range 3 {
			wg.Go(func() {
				for {
					mu.Lock()
					if nextShot >= len(w.Sections) {
						mu.Unlock()
						return
					}
					shot := &w.Sections[nextShot]
					nextShot++
					mu.Unlock()
					if shot.Image = s.media(ctx, get, shot.Source.Value, "image", until); shot.Image != nil {
						shot.Source.Value = nil
					}
				}
			})
		}
		wg.Wait()
		w.PosterSource, w.VideoSource = library.MaybeString{Set: true}, library.MaybeString{Set: true}
		if w.Poster == nil {
			w.PosterSource.Value = found.Poster
		}
		if w.Video == nil {
			w.VideoSource.Value = found.Video
		}
		work, _ = json.Marshal(w)
	}
	// A work page is its pictures and facts; it may have no words at all (I/O 3000 gives only the shot).
	if web.JSTrim(html) == "" && result.Status != "restricted" && result.Work == nil {
		return library.ArticleContent{}, errors.New("未取得可显示的正文")
	}
	links := result.Links
	if links == nil {
		links = []string{}
	}
	version, missing := imageCacheVersion, len(urls)-len(images)
	content := library.ArticleContent{ArticleID: t.ID, Status: result.Status, HTML: html, TextLength: result.TextLength,
		SourceSnapshot: &library.ReaderSourceSnapshot{HTML: result.HTML, Provenance: "source"},
		Links:          links, FetchedAt: web.Now(), SourceURL: sourceURL, SourceKind: sourceKind, Message: result.Message,
		ImageCacheVersion: &version, MissingImageCount: &missing, Work: work}
	if payload != nil {
		content.FeedVersion = str(payload.Version)
	}
	return content, nil
}

func unique(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// image is one of a body's pictures as it is kept, its kind from its first bytes; no kind when it cannot be had.
func (s *Service) image(ctx context.Context, get fetch.Fetch, url string) ([]byte, string) {
	res, err := get(ctx, url, anywhere, imageBytes)
	if err != nil || res.Status != 200 || len(res.Body) > imageBytes {
		return nil, ""
	}
	body, kind := res.Body, extract.ImageType(res.Body)
	// A whiteboard's scene file (extract-content placeWhiteboards) is drawn into a picture here.
	if kind == "" && s.x.Whiteboard != nil && strings.Contains(lower(res.Type), "json") && len(body) <= 2*1024*1024 {
		svg, err := s.x.Whiteboard(utf8Text(body))
		if err != nil {
			return nil, ""
		}
		if svg != "" {
			body, kind = []byte(svg), "image/svg+xml"
		}
	}
	if kind == "" && svgType.MatchString(lower(res.Type)) && len(body) <= 256*1024 && s.x.SVG != nil {
		svg, err := s.x.SVG(utf8Text(body))
		if err != nil {
			return nil, ""
		}
		if svg != "" {
			body, kind = []byte(svg), "image/svg+xml"
		}
	}
	if kind == "image/avif" {
		return FromAVIF(ctx, body)
	}
	return body, kind
}

// media is one of a work page's pictures as a data URL, or nil when it cannot be had by until.
func (s *Service) media(ctx context.Context, get fetch.Fetch, url *string, kind string, until time.Time) *string {
	if url == nil || *url == "" || !time.Now().Before(until) {
		return nil
	}
	limit := imageBytes
	if kind == "video" {
		limit = videoBytes
	}
	ctx, cancel := context.WithDeadline(ctx, until)
	defer cancel()
	type got struct {
		res *fetch.Response
		err error
	}
	done := make(chan got, 1)
	go func() {
		res, err := get(ctx, *url, anywhere, limit)
		done <- got{res, err}
	}()
	var r got
	select {
	case r = <-done:
	case <-ctx.Done():
		select {
		case r = <-done:
		default:
			return nil
		}
	}
	if r.err != nil || r.res.Status != 200 || len(r.res.Body) > limit {
		return nil
	}
	body, mime := r.res.Body, ""
	if kind == "video" {
		if len(body) >= 8 && string(body[4:8]) == "ftyp" {
			mime = "video/mp4"
		}
	} else if mime = extract.ImageType(body); mime == "image/avif" {
		// The picture is in; converting it is not held to the wait.
		body, mime = FromAVIF(context.WithoutCancel(ctx), body)
	}
	if mime == "" {
		return nil
	}
	return str("data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(body))
}

// FromAVIF is an AVIF picture converted by macOS's own sips, since the native interface cannot decode AVIF: PNG when
// it has transparency, else JPEG. No kind when sips is missing or cannot read it, and the picture counts as not loaded.
// Explore's pictures take the same way.
func FromAVIF(ctx context.Context, body []byte) ([]byte, string) {
	dir, err := os.MkdirTemp("", "trie-avif-")
	if err != nil {
		return nil, ""
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in.avif")
	if os.WriteFile(in, body, 0o600) != nil {
		return nil, ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	probe, err := exec.CommandContext(ctx, "sips", "-g", "hasAlpha", in).Output()
	if err != nil {
		return nil, ""
	}
	format, kind := "jpeg", "image/jpeg"
	if bytes.Contains(probe, []byte("hasAlpha: yes")) {
		format, kind = "png", "image/png"
	}
	out := filepath.Join(dir, "out."+format)
	if exec.CommandContext(ctx, "sips", "-s", "format", format, in, "--out", out).Run() != nil {
		return nil, ""
	}
	converted, err := os.ReadFile(out)
	if err != nil || extract.ImageType(converted) != kind {
		return nil, ""
	}
	return converted, kind
}
