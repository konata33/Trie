package explore

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"

	"trie/internal/extract"
	"trie/internal/feed"
	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/reading"
	"trie/internal/subscription"
)

// shell is what the app's Explore handlers keep: the pictures fetched for Explore, recent previews, and the links
// those previews showed.
type shell struct {
	pmu          sync.Mutex
	pictures     map[string]*future // by file
	previews     []*preview         // in the order they were first kept
	previewLinks map[string]bool
	tmu          sync.Mutex
	telling      *time.Timer
}

type future struct {
	done  chan struct{}
	value string
}

type preview struct {
	key     string
	done    chan struct{}
	content library.ArticleContent
	err     error
}

func (sh *shell) init() {
	sh.pictures = map[string]*future{}
	sh.previewLinks = map[string]bool{}
}

// Open is Explore as the app wires it: the catalog in resources (resources/explore), the cache and the
// pictures in the library's folder dir, feeds read as subscriptions read them, recommended posts' pages read as the
// reader previews them, sites' first screens taken in hidden windows. changed is Options.Changed.
func Open(resources, dir string, store *library.Store, subscriptions *subscription.Service, reader *reading.Service, changed func()) *Service {
	return New(Options{
		Resources: resources, Cache: filepath.Join(dir, "explore-cache.json"), FetchFeed: subscriptions.FetchFeed,
		Sources: func() []library.Source {
			st, err := store.Library()
			if err != nil {
				return nil
			}
			return st.Sources
		},
		Changed: changed, Upstreams: Upstreams, FindIcon: FindIcon,
		// A recommended post's page, read as the reader would show it; a page that yields no full article counts as unreadable.
		ReadPage: func(ctx context.Context, url, homeURL string) string {
			c := reader.Preview(ctx, reading.Target{ID: "explore:" + url, URL: url, Aliases: []string{}, HomeURL: homeURL, TrustedArticle: true}, "auto", 0)
			if c.Status == "ready" && c.TextLength > thinText {
				return c.HTML
			}
			return ""
		},
		Preview: func(ctx context.Context, t reading.Target, source string) library.ArticleContent {
			return reader.Preview(ctx, t, source, reading.PreviewWait)
		},
		Site:  store.Site,
		Shots: NewShots(filepath.Join(dir, "explore-shots")),
	})
}

// Start captures, five seconds on, the first screens of the design room's sites that have none or an old one, behind
// anything Explore asks for, so a first visit already has them.
func (s *Service) Start() {
	if s.o.Shots == nil {
		return
	}
	time.AfterFunc(5*time.Second, func() {
		for _, site := range s.designSites() {
			if s.o.Shots.Stale(site.Host) {
				wait := s.o.Shots.Capture(site.Host, []string{site.HomeURL}, false)
				go func() {
					if wait(context.Background()) {
						s.tell()
					}
				}()
			}
		}
	})
}

// Stop gives up the captures under way and those waiting, as the app's window closes, so the app quits at once
// rather than after them (Shots.Stop).
func (s *Service) Stop() {
	if s.o.Shots != nil {
		s.o.Shots.Stop()
	}
}

// tell is Changed for captures: several can land within a moment, and the page reads the list once for them.
func (s *Service) tell() {
	s.tmu.Lock()
	defer s.tmu.Unlock()
	if s.telling != nil {
		s.telling.Stop()
	}
	s.telling = time.AfterFunc(800*time.Millisecond, s.changed)
}

// Links are the addresses Explore shows or showed in a preview, which the app may open in the browser.
func (s *Service) Links() []string {
	out := s.links()
	s.pmu.Lock()
	defer s.pmu.Unlock()
	for link := range s.previewLinks {
		out = append(out, link)
	}
	return out
}

// Work is loadExploreWork: the picture of one of a gallery's works (its place in Entry.Works) as a data URL, "" when it cannot be had.
func (s *Service) Work(ctx context.Context, entryID string, index int) (string, error) {
	if index < 0 {
		return "", nil
	}
	src, err := s.workImage(entryID, index)
	if err != nil || src == "" {
		return "", err
	}
	return s.picture(ctx, src, 0), nil
}

// Shot is loadExploreShot: what stands behind an entry, as a data URL. A writer stands on their latest post's own
// picture, the first wide enough to fill a card ("" with none: the page sets the post's words instead); a design site
// on its own first screen, captured, or taken now when it has none. "" for an entry Explore does not know.
func (s *Service) Shot(ctx context.Context, entryID string) (string, error) {
	site, err := s.site(entryID)
	if err != nil || site == nil {
		return "", err
	}
	// Pictures not known yet may be on their way in a background read of the feed.
	if site.Writer && len(site.Pictures) == 0 {
		s.settled(entryID)
		if again, err := s.site(entryID); err == nil && again != nil {
			site = again
		}
	}
	if site.Writer {
		for _, src := range site.Pictures {
			if found := s.picture(ctx, src, 600); found != "" {
				return found, nil
			}
		}
		return "", nil
	}
	if s.o.Shots == nil {
		return s.shot(entryID), nil
	}
	if s.o.Shots.Stale(site.Host) {
		wait := s.o.Shots.Capture(site.Host, []string{site.HomeURL}, true)
		go func() {
			if wait(context.Background()) {
				s.tell()
			}
		}()
	}
	if shot := s.shot(entryID); shot != "" {
		return shot, nil
	}
	if s.o.Shots.Capture(site.Host, []string{site.HomeURL}, true)(ctx) {
		return s.shot(entryID), nil
	}
	return "", nil
}

// SourceShot is loadSourceShot: the first screen of a subscribed source's own site as a data URL, writers included
// (Explore stands writers on a post's picture instead). One on disk is shown at once and renewed in the background once
// stale; one not taken yet is taken now. "" when there is none.
func (s *Service) SourceShot(ctx context.Context, sourceID string) (string, error) {
	if s.o.Site == nil {
		return "", nil
	}
	site, err := s.o.Site(sourceID)
	if err != nil || site == nil {
		return "", err
	}
	h := strings.ToLower(strings.TrimPrefix(site.Host, "www."))
	read := func() string {
		data, err := os.ReadFile(ShotFile(s.shotsDir, h, 1280))
		if err != nil {
			return ""
		}
		return dataURL("image/jpeg", data)
	}
	if s.o.Shots == nil || !s.o.Shots.Stale(h) {
		return read(), nil
	}
	taking := s.o.Shots.Capture(h, []string{site.HomeURL}, true)
	if shot := read(); shot != "" {
		return shot, nil
	}
	if taking(ctx) {
		return read(), nil
	}
	return "", nil
}

// Read is readExplorePost: one of an entry's latest posts read without adding it to the library. source "page" or
// "feed" reads it afresh from the source chosen in the reading options, "auto" afresh as the reader decides (a retry);
// "" reuses a recent preview, so going back and forth does not refetch the article and its pictures. A summary or an
// unavailable body is not kept, so opening it again tries the page.
func (s *Service) Read(ctx context.Context, entryID, url, source string) (library.ArticleContent, error) {
	key := url
	if source != "" && source != "auto" {
		key = source + ":" + url
	}
	s.pmu.Lock()
	var p *preview
	if source == "" {
		if i := slices.IndexFunc(s.previews, func(p *preview) bool { return p.key == key }); i >= 0 {
			p = s.previews[i]
		}
	}
	if p == nil {
		p = &preview{key: key, done: make(chan struct{})}
		if i := slices.IndexFunc(s.previews, func(p *preview) bool { return p.key == key }); i >= 0 {
			s.previews[i] = p
		} else if s.previews = append(s.previews, p); len(s.previews) > 8 {
			s.previews = s.previews[len(s.previews)-8:]
		}
		go func() {
			t, err := s.ReadTarget(context.Background(), entryID, url)
			if err == nil && s.o.Preview == nil {
				err = errors.New("explore: Options.Preview is not set")
			} else if err == nil {
				p.content = s.o.Preview(context.Background(), t, or(source, "auto"))
			}
			p.err = err
			s.pmu.Lock()
			if err != nil || p.content.Status == "unavailable" || p.content.Status == "summary" {
				s.previews = slices.DeleteFunc(s.previews, func(p *preview) bool { return p.key == key })
			}
			if err == nil {
				for _, link := range append(slices.Clone(p.content.Links), p.content.SourceURL) {
					s.previewLinks[link] = true
				}
			}
			s.pmu.Unlock()
			close(p.done)
		}()
	}
	s.pmu.Unlock()
	select {
	case <-p.done:
		return p.content, p.err
	case <-ctx.Done():
		return library.ArticleContent{}, ctx.Err()
	}
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// picture is a picture Explore shows from another site (a gallery's work, a recommended writer's latest post), fetched
// on first sight, shrunk to wall size and kept on disk, as a data URL. One narrower than minWidth (a logo, an avatar)
// is not kept; "" says there is none worth showing, and stays so for the session.
func (s *Service) picture(ctx context.Context, src string, minWidth int) string {
	sum := sha1.Sum([]byte(src))
	file := filepath.Join(s.worksDir, hex.EncodeToString(sum[:])+"-1200.jpg")
	s.pmu.Lock()
	f := s.pictures[file]
	if f == nil {
		f = &future{done: make(chan struct{})}
		s.pictures[file] = f
		go func() {
			f.value = s.fetchPicture(file, src, minWidth)
			close(f.done)
		}()
	}
	s.pmu.Unlock()
	select {
	case <-f.done:
		return f.value
	case <-ctx.Done():
		return ""
	}
}

func (s *Service) fetchPicture(file, src string, minWidth int) string {
	if cached, err := os.ReadFile(file); err == nil {
		// One kept as it came before AVIF was converted is converted now, and kept so.
		if extract.ImageType(cached) == "image/avif" {
			if data, mime := reading.FromAVIF(context.Background(), cached); mime != "" && writeFile(file, data) == nil {
				return dataURL(mime, data)
			}
			return ""
		}
		return dataURL(or(extract.ImageType(cached), "image/jpeg"), cached)
	}
	get := s.o.Fetch
	if get == nil {
		get = fetch.New(6).Fetch
	}
	res, err := get(context.Background(), src, func(string) bool { return true }, 12*1024*1024)
	if err != nil {
		// A connection dropped on the way is tried once more: a picture that fails is not asked for again this session.
		res, err = get(context.Background(), src, func(string) bool { return true }, 12*1024*1024)
	}
	if err != nil || res.Status < 200 || res.Status >= 300 {
		return ""
	}
	// What decodeNative reads is PNG and JPEG; a WebP, GIF or AVIF picture (I/O 3000) goes to the page as it came.
	// The file keeps its name; its bytes say what it is. The native window cannot decode AVIF: such
	// a picture goes as macOS's sips converts it, as the reader's do (PNG with transparency, else JPEG).
	data, mime := res.Body, extract.ImageType(res.Body)
	if mime == "image/avif" {
		if data, mime = reading.FromAVIF(context.Background(), data); mime == "" {
			return ""
		}
	}
	if img := decodeNative(res.Body); img != nil {
		if img.Bounds().Dx() < minWidth {
			return ""
		}
		// Wide enough for Explore's stage on a Retina screen; the file name carries the width so older, smaller copies are not reused.
		var buf bytes.Buffer
		if jpeg.Encode(&buf, shrink(img, 1200), &jpeg.Options{Quality: 82}) != nil {
			return ""
		}
		data, mime = buf.Bytes(), "image/jpeg"
	} else if mime == "" {
		return ""
	}
	if writeFile(file, data) != nil {
		return ""
	}
	return dataURL(mime, data)
}

// decodeNative is a picture decoded for shrinking: PNG and JPEG only; nil for anything else.
func decodeNative(body []byte) image.Image {
	var img image.Image
	var err error
	switch extract.ImageType(body) {
	case "image/png":
		img, err = png.Decode(bytes.NewReader(body))
	case "image/jpeg":
		img, err = jpeg.Decode(bytes.NewReader(body))
	default:
		return nil
	}
	if err != nil || img.Bounds().Empty() {
		return nil
	}
	return img
}

// shrink scales a picture wider than width down to that width; others stay as they are.
func shrink(img image.Image, width int) image.Image {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w <= width {
		return img
	}
	out := image.NewNRGBA(image.Rect(0, 0, width, max(1, int(math.Round(float64(h)*float64(width)/float64(w))))))
	xdraw.CatmullRom.Scale(out, out.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	return out
}

var htmlType = regexp.MustCompile(`(?i)html`)

// FindIcon is a site's icon, read from its home page and drawn at most 96px wide like the catalog's
// icons, as a data URL. Nil when the site answered but offers no icon the page can draw; an error when a connection
// failed, so the site is asked again next launch.
func FindIcon(ctx context.Context, homeURL string) (*string, error) {
	return findIcon(ctx, fetch.New(12).Fetch, homeURL)
}

func findIcon(ctx context.Context, get fetch.Fetch, homeURL string) (*string, error) {
	var unreachable error
	read := func(url string, maxBytes int) *fetch.Response {
		res, err := get(ctx, url, func(string) bool { return true }, maxBytes)
		if err != nil {
			unreachable = err
			return nil
		}
		return res
	}
	home := read(homeURL, 2_000_000)
	page, at := "", homeURL
	if home != nil {
		if home.URL != "" {
			at = home.URL
		}
		if home.Status == 200 && htmlType.MatchString(home.Type) {
			page = fetch.Decode(home.Body, "")
		}
	}
	for _, src := range feed.DiscoverIcons(page, at) {
		res := read(src, 1_000_000)
		if res == nil || res.Status != 200 {
			continue
		}
		if img := decodeNative(res.Body); img != nil {
			var buf bytes.Buffer
			if png.Encode(&buf, shrink(img, 96)) == nil {
				icon := dataURL("image/png", buf.Bytes())
				return &icon, nil
			}
		}
		// What decodeNative cannot read, WebP and the ICO most favicons are, goes to the page as it came.
		mime := extract.ImageType(res.Body)
		if mime == "" && len(res.Body) > 4 && binary.BigEndian.Uint32(res.Body) == 0x00000100 {
			mime = "image/x-icon"
		}
		if mime != "" {
			icon := dataURL(mime, res.Body)
			return &icon, nil
		}
	}
	return nil, unreachable
}
