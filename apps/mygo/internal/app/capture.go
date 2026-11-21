package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/theme"
)

// 拓本 (PAGES.md 拓本, DESIGN.md Capture): the reader's button takes the
// page as a folder for the reader's own AI, one page at a time, the others
// waiting. What is being taken and what is taken and not yet used sit in the
// island at the top of the window, each as its picture developing in dots;
// pressed, a thumbnail grows into its card, which copies the folder's place.

type packState uint8

const (
	packWaiting packState = iota
	packTaking
	packTaken
	packFailed
)

// packer takes the pack of a page into dir (explore.TakePack); tests stand in
// their own.
type packer func(ctx context.Context, pageURL, dir string, progress explore.PackProgress) (*explore.Pack, error)

// packItem is one page captured. The fields with tags are kept in
// packs.json, for the packs taken; the rest live as long as the session.
type packItem struct {
	ID      string    `json:"id"`   // its folder's name in packs
	URL     string    `json:"url"`  // the page taken
	Page    string    `json:"page"` // the article it was taken from, whose button it lights
	Name    string    `json:"name"` // the site's name
	Created time.Time `json:"created"`
	Used    bool      `json:"used"` // its folder was copied
	Screens int       `json:"screens"`
	Images  int       `json:"images"`
	Fonts   int       `json:"fonts"`
	Cut     bool      `json:"cut,omitempty"` // the page went on past the screens taken
	// TitleFont is the font file in the pack that sets the page's largest words,
	// at TitleWeight; font is the family it was registered as, for the card's name.
	TitleFont   string `json:"titleFont,omitempty"`
	TitleWeight int    `json:"titleWeight,omitempty"`
	font        string

	state    packState
	failed   string // what happened, as its card says
	progress float32
	// base is the picture the app had of the page, first its first screen
	// once taken, and shot that screen to show.
	base, first *dotPic
	shot        *ui.Bitmap
	// When the app's picture and its first screen came, it started, it was taken
	// and it failed, as the frames saw them.
	baseAt, firstAt, startedAt, takenAt, failedAt time.Time
	// pct is how far it has come as its figures show it, since pctAt (percent).
	pct    int
	pctAt  time.Time
	cancel context.CancelFunc
	pages  *packPages // its card's page, read while the card is open
	gone   bool       // deleted, or abandoned as the app quits
	// filed: it has left the island for good, into 收藏 or sent away with it.
	filed bool
	// The first screen and the face were asked for (packShot, packFont); the
	// app has a picture of the page, or it is on its way (takePage), so its
	// dots wait on the window's ground rather than show grey first.
	shotAsked, fontAsked, basing bool
}

// Where a pack's card grew out of, and goes back into: its thumbnail in the
// island, the reader's 拓本 button, or its place in 收藏.
const (
	homeIsland = "island"
	homeButton = "button"
	homeShelf  = "shelf"
)

// favLight is the light on 收藏's tab as a capture lands in it (landedLight).
const favLight = "tab:favorites"

// captureState is the capture's: the packs, newest first, and the island.
type captureState struct {
	items  []*packItem
	dir    string // the packs' folder
	keep   bool   // packs.json is kept (there is a library folder)
	take   packer
	reveal func(path string)
	card   string // the pack whose card is open
	home   string // where that card grew out of (homeIsland…)
	back   string // the pack whose card went back into its thumbnail last
	// copied is when the pack copiedID was copied, from the keys or under less
	// motion (copyKbd), out of a card from copiedHome: the card goes back there,
	// which says 已复制 (copiedNote).
	copied     time.Time
	copiedID   string
	copiedHome string
	copyKbd    bool
	// heldAt is when the island was last pointed at, or its card open: what
	// was just taken stays CaptureLinger past it. away: 收进收藏 sent the island off,
	// until nothing is being taken; focused: the keys were in it.
	heldAt  time.Time
	away    bool
	focused bool
	// wide, tall and markAt are the island's size and its mark's place as it last
	// held something: going back into its point it keeps them, one whole, rather
	// than closing round its mark first.
	wide, tall, markAt float32
	files              []*captureFile // thumbnails on their way into 收藏
	pct                int            // the figures the island showed last, kept as they cross into the mark
	// islandRect is the island as the frame before laid it out, in the window;
	// homing the island on its way home into 收藏's tab, which then tells of
	// what it took in (capture_post.go): of told, taken while it was away, since
	// toldAt; tabPct and tabFill are the figures and the orbit it showed last.
	// post is the note of a page that could not be taken while it was away.
	islandRect ui.Rect
	homing     *captureHoming
	told       *packItem
	toldAt     time.Time
	tabPct     int
	tabFill    float32
	post       *capturePost
	// scroll is the open card's page; lastY where it was in the frame before, and
	// movedAt when it last moved, for the line that tells where it is.
	scroll  ui.ScrollState
	lastY   float32
	movedAt time.Time
	flights []*captureFlight
	up      bool                // the island showed in the frame before
	last    []*packItem         // what it showed then, kept while it goes back into its point
	slots   map[string]ui.Rect  // the thumbnails, as the frame before laid them out, in the window
	placed  map[string]bool     // the thumbnails built in the frame before
	fresh   map[string]struct{} // thumbnails just come, which fade in under less motion
}

// captureFlight is a thumbnail on its way from the button to its place in the
// island.
type captureFlight struct {
	id    string
	from  ui.Rect
	start time.Time
	tries int
}

// captureFile is a thumbnail on its way from the island into 收藏's tab; under
// less motion (still), fading where it was.
type captureFile struct {
	it     *packItem
	from   ui.Rect
	start  time.Time
	length time.Duration
	still  bool
}

// Island geometry (DESIGN.md Capture 岛).
const (
	thumbW, thumbH = 72, 45
	islandTop      = 8
	islandPad      = 6
	islandGap      = 4
	islandShown    = 5  // thumbnails side by side; earlier ones stack behind the last
	islandEdges    = 3  // the edges of the stacked ones that show
	packsWarm      = 12 // the packs of earlier sessions whose first screens load at once
	// The island's right end: its status, how far the page being taken has come
	// or the capture mark, 4 from the thumbnails and from the edge, wide enough
	// for 100%; and 收进收藏, the width of an icon button, after it while the
	// island is pointed at.
	islandStatusW = 36
	islandMarkGap = 4
	islandHideW   = 28
	// 已复制 after the mark: its gap from the mark's icon, and its room at the right.
	islandNoteGap   = 8
	islandNoteRight = 12
	cardW, cardH    = 440, 275
)

// loadPacks reads the packs kept from earlier sessions, their first screens
// off the window's thread, and clears what a quit left half taken.
func (a *App) loadPacks() {
	h := &a.capture
	h.take, h.reveal = explore.TakePack, mygo.Shell.ShowItemInFolder
	h.slots, h.placed, h.fresh = map[string]ui.Rect{}, map[string]bool{}, map[string]struct{}{}
	if a.opt.DataDir == "" {
		h.dir = filepath.Join(os.TempDir(), "trie-packs")
		return
	}
	h.dir, h.keep = filepath.Join(a.opt.DataDir, "packs"), true
	parts, _ := filepath.Glob(filepath.Join(h.dir, "*.part"))
	for _, p := range parts {
		os.RemoveAll(p)
	}
	data, err := os.ReadFile(filepath.Join(a.opt.DataDir, "packs.json"))
	if err != nil {
		return
	}
	var kept []*packItem
	if json.Unmarshal(data, &kept) != nil {
		return
	}
	for _, it := range kept {
		if st, err := os.Stat(filepath.Join(h.dir, it.ID)); err != nil || !st.IsDir() || it.ID == "" {
			continue
		}
		// Taken in another session: no picture develops, it is there, in 收藏.
		it.state, it.progress, it.takenAt, it.filed = packTaken, 1, time.Unix(1, 0), true
		h.items = append(h.items, it)
	}
	for _, it := range h.items[:min(len(h.items), packsWarm)] {
		a.packFont(it)
		a.packShot(it)
	}
}

// packShot reads the first screen of a pack taken in an earlier session, off
// the window's thread, once: for its place in 收藏 and its card.
func (a *App) packShot(it *packItem) {
	if it.shot != nil || it.shotAsked || it.state != packTaken {
		return
	}
	it.shotAsked = true
	file := filepath.Join(a.capture.dir, it.ID, "screens", "00.jpg")
	a.do(func() func() {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil
		}
		img := decodeAny(data)
		g, shot := gridOf(img), shotOf(img, 2*cardW)
		return func() { it.first, it.shot, it.firstAt = g, shot, time.Unix(1, 0) }
	})
}

// packFont registers the face that sets the page's largest words, off the
// window's thread, once, for its card to set the name in; a file the system
// cannot read leaves the name in the app's own face. ponytail: each
// registration lays out the app's text afresh once; it is done once a pack,
// for the newest dozen at start and for the rest as their cards open.
func (a *App) packFont(it *packItem) {
	if it.TitleFont == "" || it.fontAsked {
		return
	}
	it.fontAsked = true
	file, family := filepath.Join(a.capture.dir, it.ID, filepath.FromSlash(it.TitleFont)), "trie-pack-"+packKey(it)
	a.do(func() func() {
		data, err := os.ReadFile(file)
		// Core Text takes a WOFF (1.0) file and sets nothing in it, so the name would come
		// out in the system's fallback face: it keeps the app's own.
		if err != nil || bytes.HasPrefix(data, []byte("wOFF")) || ui.RegisterFont(data, family) != nil {
			return nil
		}
		return func() { it.font = family }
	})
}

// savePacks keeps the packs taken in packs.json. ponytail: written on the
// window's thread, a few KB; move it to a job if the list grows long.
func (a *App) savePacks() {
	h := &a.capture
	if !h.keep {
		return
	}
	var kept []*packItem
	for _, it := range h.items {
		if it.state == packTaken && !it.gone {
			kept = append(kept, it)
		}
	}
	data, err := json.MarshalIndent(kept, "", "  ")
	file := filepath.Join(a.opt.DataDir, "packs.json")
	if err == nil {
		err = os.WriteFile(file+".tmp", data, 0o644)
	}
	if err == nil {
		err = os.Rename(file+".tmp", file) // whole: a quit while it is written keeps the list before
	}
	if err != nil {
		a.fail(locales.ErrorNotSaved(err.Error()))
	}
}

// StopPacks abandons the packs being taken and those waiting, as the app
// quits: each leaves no folder (TakePack takes it beside its place).
func (a *App) StopPacks() {
	h := &a.capture
	h.items = slices.DeleteFunc(h.items, func(it *packItem) bool {
		if it.state != packWaiting && it.state != packTaking {
			return false
		}
		it.gone = true
		if it.cancel != nil {
			it.cancel()
		}
		return true
	})
}

// packOf is the newest pack of article id, nil for none.
func (a *App) packOf(id string) *packItem {
	for _, it := range a.capture.items {
		if it.Page == id {
			return it
		}
	}
	return nil
}

// copiedBack is how long after a copy the card goes back into its thumbnail:
// once the light has passed over it, at once from the keys.
func (h *captureState) copiedBack() time.Duration {
	if h.copyKbd {
		return 0
	}
	return motion.CaptureSweep
}

// copiedNote is when the island says 已复制 for the pack just copied: from when
// its card is back in its thumbnail, for motion.Copied. Zero when nothing was
// copied.
func (h *captureState) copiedNote() (from, to time.Time) {
	if h.copied.IsZero() {
		return
	}
	from = h.copied.Add(h.copiedBack())
	if !h.copyKbd {
		from = from.Add(motion.DialogOut)
	}
	return from, from.Add(motion.Copied)
}

// packOpened is the share of the way a pack has come once its page has opened
// (explore.TakePack's).
const packOpened = 0.1

// shown is how far it has come as it shows: its pack's share, which stands
// still while the page opens, the first tenth of the way and up to 15 s, so
// until then it creeps toward that tenth by the time gone (CaptureOpening);
// it never goes back. A pack that failed keeps where it got to.
func (it *packItem) shown(now time.Time) float32 {
	switch it.state {
	case packTaken:
		return 1
	case packWaiting:
		return 0
	}
	p := it.progress
	if p < packOpened && !it.startedAt.IsZero() {
		end := now
		if it.state == packFailed && !it.failedAt.IsZero() {
			end = it.failedAt
		}
		t := end.Sub(it.startedAt).Seconds() / motion.CaptureOpening.Seconds()
		p = max(p, packOpened*float32(1-math.Exp(-max(0, t))))
	}
	return p
}

// percent is how far it has come in hundredths, as its figures show it: they
// move on at most every CapturePercent, so they read as steps rather than as a
// counter running, and reach 100 only once it is taken.
func (it *packItem) percent(c *ui.Context, now time.Time) int {
	n := min(99, int(it.shown(now)*100))
	if it.state == packTaken {
		n = 100
	}
	switch wait := motion.CapturePercent - now.Sub(it.pctAt); {
	case n <= it.pct:
	case n == 100 || it.pctAt.IsZero() || wait <= 0:
		it.pct, it.pctAt = n, now
	default:
		c.After(wait)
	}
	if it.state == packTaking {
		c.After(motion.CapturePercent) // it creeps on with nothing told
	}
	return it.pct
}

// percentText is n hundredths as figures: 37%.
func percentText(n int) string { return strconv.Itoa(n) + "%" }

// packKey names what is kept of a pack apart from its folder, its picture in
// 收藏 and its face: its folder and when it was taken, as a page captured
// once one is deleted can take the same folder.
func packKey(it *packItem) string {
	return it.ID + "-" + strconv.FormatInt(it.Created.UnixNano(), 36)
}

func (h *captureState) find(id string) *packItem {
	for _, it := range h.items {
		if it.ID == id {
			return it
		}
	}
	return nil
}

// folder is a new pack's folder name: its title in lower case, words joined
// by hyphens; the page's host when the title has no letters.
func (h *captureState) folder(title, page string) string {
	base := slugOf(title)
	if base == "" {
		base = "page"
		if u, err := url.Parse(page); err == nil && u.Hostname() != "" {
			base = strings.TrimPrefix(u.Hostname(), "www.")
		}
	}
	name := base
	for n := 2; ; n++ {
		_, err := os.Stat(filepath.Join(h.dir, name))
		if h.find(name) == nil && errors.Is(err, os.ErrNotExist) {
			return name
		}
		name = base + "-" + strconv.Itoa(n)
	}
}

// slugOf is title as a folder name: letters and figures of any script, lower
// case, the rest one hyphen; apostrophes dropped, so "researcher’s" stays a word.
func slugOf(title string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r == '\'' || r == '’':
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if gap && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			gap = false
		default:
			gap = true
		}
		if utf8.RuneCountInString(b.String()) >= 60 {
			break
		}
	}
	return b.String()
}

// packTarget is what the reader's page captures: a work's own site, else
// its gallery page; an article's original. name is the work's, or the
// article's title, so that two pieces by one writer tell apart; else the site's.
func (a *App) packTarget(rp *readerPage, art *library.Article) (link, name string) {
	link = art.URL
	if w := rp.work; w != nil {
		if w.Site != nil && strings.TrimSpace(*w.Site) != "" {
			link = *w.Site
		}
		if w.Name != "" {
			return link, w.Name
		}
	} else if t := strings.TrimSpace(art.Title); t != "" {
		return link, t
	} else if src := a.sourceOf(art); src != nil && nameOf(src) != "" {
		return link, nameOf(src)
	}
	if u, err := url.Parse(link); err == nil && u.Hostname() != "" {
		return link, strings.TrimPrefix(u.Hostname(), "www.")
	}
	return link, link
}

// packPicture is the picture the app already has of the page, as the dots'
// first colours: a work's first screen; for an article, its proof, set from what
// the reader has of it now (proofPic). It makes it when called, off the window's
// thread.
func (a *App) packPicture(rp *readerPage, art *library.Article) func() *dotPic {
	if w := rp.work; w != nil {
		data := w.Poster
		if data == nil && len(w.Sections) > 0 {
			data = w.Sections[0].Image
		}
		if data == nil || !strings.HasPrefix(*data, "data:image/") {
			return nil
		}
		raw := *data
		return func() *dotPic {
			return gridOf(decodeData(raw))
		}
	}
	p := proofPage{title: titleOf(art.Title), date: articleDate(art, false)}
	if rp.r != nil {
		p.blocks = rp.r.Body.Sketch(32)
	}
	var icon string
	if src := a.sourceOf(art); src != nil {
		p.source = nameOf(src)
		if src.Icon != nil {
			icon = *src.Icon
		}
	}
	// The library knows whether it has a picture: one without asks nothing of it.
	id, pictures := art.ID, art.Preview != nil && art.Preview.HasImage && a.opt.Thumbs == nil // the tests' stand-in has no pixels
	return func() *dotPic {
		if pictures {
			p.picture, _ = a.thumbImage(id, 0, 240, false)
		}
		p.mark = markOf(decodeData(icon))
		return proofPic(p)
	}
}

// markOf is a site's icon as the one colour it reads as from afar: the mean of
// its opaque pixels; zero for none.
func markOf(img image.Image) ui.Color {
	if img == nil {
		return ui.Color{}
	}
	b := img.Bounds()
	var r, g, bl, n float64
	for y := b.Min.Y; y < b.Max.Y; y += max(1, b.Dy()/24) {
		for x := b.Min.X; x < b.Max.X; x += max(1, b.Dx()/24) {
			cr, cg, cb, ca := img.At(x, y).RGBA()
			if ca < 0x8000 {
				continue
			}
			r, g, bl, n = r+float64(cr)/float64(ca), g+float64(cg)/float64(ca), bl+float64(cb)/float64(ca), n+1
		}
	}
	if n == 0 {
		return ui.Color{}
	}
	return ui.Color{R: uint8(255 * r / n), G: uint8(255 * g / n), B: uint8(255 * bl / n), A: 255}
}

// takePage captures page (an article's ID) at link, named name, its dots
// first coloured by pic (nil for grey): it waits for the packs before it, and
// when from is set, its thumbnail flies there from the button.
func (a *App) takePage(page, link, name string, pic func() *dotPic, from ui.Rect, now time.Time) *packItem {
	h := &a.capture
	it := &packItem{ID: h.folder(name, link), URL: link, Page: page, Name: name, Created: now}
	h.items = append([]*packItem{it}, h.items...)
	// Pressed again, the island comes back, one island, the one on its way home
	// gone; and the note goes into its tab.
	h.away, h.homing = false, nil
	h.foldPost(now)
	if pic != nil {
		it.basing = true
		a.do(func() func() {
			g := pic()
			return func() { it.base, it.basing = g, g != nil }
		})
	}
	if from.W > 0 {
		h.flights = append(h.flights, &captureFlight{id: it.ID, from: from})
	}
	a.nextPack()
	return it
}

// nextPack starts the oldest pack waiting, while none is being taken.
func (a *App) nextPack() {
	h := &a.capture
	var it *packItem
	for _, o := range h.items {
		switch o.state {
		case packTaking:
			return
		case packWaiting:
			it = o
		}
	}
	if it == nil {
		return
	}
	it.state = packTaking
	ctx, cancel := context.WithCancel(bg)
	it.cancel = cancel
	take, link, dir, post := h.take, it.URL, filepath.Join(h.dir, it.ID), a.post
	progress := func(done float64, first image.Image) {
		g, shot := gridOf(first), shotOf(first, 2*cardW)
		post(func() {
			if it.state != packTaking || it.gone {
				return
			}
			it.progress = max(it.progress, float32(done))
			if g != nil {
				it.first, it.shot = g, shot
			}
		})
	}
	a.do(func() func() {
		p, err := take(ctx, link, dir, progress)
		return func() { a.packTaken(it, p, err, dir) }
	})
}

// packTaken takes what taking it came to, and starts the next.
func (a *App) packTaken(it *packItem, p *explore.Pack, err error, dir string) {
	it.cancel = nil
	switch {
	case it.gone:
		if err == nil {
			a.do(func() func() { explore.StopPack(dir); os.RemoveAll(dir); return nil })
		}
	case err != nil:
		it.state, it.failed = packFailed, packFailure(err)
		if !a.capture.away {
			a.said = append(a.said, locales.CaptureDidFail(it.Name)) // out of sight, a notice says it
		}
	default:
		it.state, it.progress = packTaken, 1
		it.Screens, it.Images, it.Fonts, it.Cut = len(p.Screens), p.Images, p.Fonts, p.Cut
		it.TitleFont, it.TitleWeight = p.TitleFont, p.TitleWeight
		a.packFont(it)
		a.savePacks()
		a.said = append(a.said, locales.CaptureDone(it.Name))
		if a.capture.away {
			a.capture.told, a.capture.toldAt = it, time.Time{} // 收藏's tab says so (tabNews)
		}
	}
	a.nextPack()
}

// packFailure says what happened, as the card tells it.
func packFailure(err error) string {
	switch s := err.Error(); {
	case strings.Contains(s, "did not load"):
		return locales.CaptureDidNotLoad()
	case strings.Contains(s, "answered"):
		return locales.CaptureRefused()
	}
	return locales.CaptureFailed()
}

// dropPack deletes it, its folder too, and stops taking it or adding to it.
func (a *App) dropPack(it *packItem) {
	h := &a.capture
	it.gone = true
	if it.cancel != nil {
		it.cancel()
	}
	h.items = slices.DeleteFunc(h.items, func(o *packItem) bool { return o == it })
	if it.state == packTaken {
		dir := filepath.Join(h.dir, it.ID)
		a.do(func() func() { explore.StopPack(dir); os.RemoveAll(dir); return nil })
		a.savePacks()
	}
	if h.card == it.ID {
		h.card = ""
	}
	a.nextPack()
}

// retryPack takes a pack that failed again, as a new one in its place.
func (a *App) retryPack(it *packItem, now time.Time) {
	h := &a.capture
	h.card = "" // its card goes back into the thumbnail, which waits to develop again
	it.state, it.failed, it.progress, it.failedAt = packWaiting, "", 0, time.Time{}
	it.startedAt, it.pct, it.pctAt = time.Time{}, 0, time.Time{}
	it.ID = "" // its name is free again: it left no folder
	it.ID, it.Created = h.folder(it.Name, it.URL), now
	it.filed, h.away, h.homing = false, false, nil // taken again, it shows in the island again, the only one
	h.back = it.ID
	a.nextPack()
}

// openPack opens it's card out of from, its home (homeIsland…), which it goes
// back into; at once from the keys. A pack goes on growing for a minute once
// taken, so the card reads its screens and counts afresh each time it opens.
func (a *App) openPack(it *packItem, from ui.Rect, home string) {
	h := &a.capture
	h.card, h.home, h.copied = it.ID, home, time.Time{}
	h.scroll.Y = 0
	it.pages = nil
	a.recount(it)
	a.packFont(it)
	a.packShot(it)
	a.layers.opener, a.layers.in, a.layers.out = from, motion.DialogIn, motion.DialogOut
	if a.kbd {
		a.layers.opener = ui.Rect{}
	}
	a.growFrom("pack", motion.DialogGrow, 24, theme.ShadowDialog)
}

// recount counts, off the window's thread, the screens, pictures and fonts it's
// folder holds now, those added once it was taken too, and keeps them when they
// changed.
func (a *App) recount(it *packItem) {
	if it.state != packTaken {
		return
	}
	dir := filepath.Join(a.capture.dir, it.ID)
	a.do(func() func() {
		screens, images, fonts := filesIn(dir, "screens"), filesIn(dir, "files", "images"), filesIn(dir, "files", "fonts")
		return func() {
			if it.gone || screens == 0 || screens == it.Screens && images == it.Images && fonts == it.Fonts {
				return // a folder gone from under it keeps what it had
			}
			it.Screens, it.Images, it.Fonts = screens, images, fonts
			a.savePacks()
		}
	})
}

// filesIn counts the files in a folder of dir, those being written (hidden)
// left out.
func filesIn(dir string, folder ...string) int {
	entries, _ := os.ReadDir(filepath.Join(append([]string{dir}, folder...)...))
	n := 0
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			n++
		}
	}
	return n
}

// hideIsland is 收进收藏: the island itself goes home into 收藏's tab (putAway),
// which tells from then on how what is still being taken goes on there.
func (a *App) hideIsland(c *ui.Context, now time.Time) {
	h := &a.capture
	h.away, h.focused = true, false
	for _, it := range h.last {
		if it.state == packTaken || it.state == packFailed {
			it.filed = true
		}
	}
	a.putAway(c, slices.Clone(h.last), now)
	// Its way home takes over from where the island was, in this frame: the island is gone.
	h.up = false
	clear(h.slots)
	clear(h.placed)
}

// onShelf tells whether the captures in 收藏 are what the window shows.
func (a *App) onShelf() bool {
	return a.page == (page{kind: "favorites"}) && a.fav != nil && a.fav.kind == captures
}

// showShelf goes to the captures in 收藏: the 拓本 block zoomed into, everything
// shown.
func (a *App) showShelf(c *ui.Context) {
	fs := a.favs()
	fs.query = ""
	if a.page == (page{kind: "favorites"}) {
		a.zoomTo(c, captures)
		return
	}
	fs.kind, fs.place = captures, ""
	a.goTo(c, page{kind: "favorites"}, motion.Side, nil)
}

// captureButton is 拓本 in the reader's bar, mirroring the page's pack: its
// dots develop as the island's thumbnail does, and light once it is taken;
// pressed then, it opens the pack's card rather than take the page again. A
// card it opened and copied from comes back into it, and its dots flow into a
// check while it says so.
func (a *App) captureButton(c *ui.Context, rp *readerPage, art *library.Article) {
	h := &a.capture
	it := a.packOf(art.ID)
	b := ui.ButtonBase(c.Key("capture")).Label(locales.Capture()).Size(32, 32).Radius(6).Center().Shrink(0).FocusRing(false)
	color, ground := theme.Icon, ui.Transparent
	if b.Hovered() {
		color, ground = theme.Text, theme.Hover
	}
	fg := b.AnimateColor("fg", color, a.ease(motion.Hover))
	b.Background(b.AnimateColor("bg", ground, a.ease(motion.Hover))).TextColor(fg)
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	tip, dev, lit := locales.CaptureTip(), float32(-1), false
	if it != nil {
		switch it.state {
		case packWaiting:
			tip, dev = locales.CaptureTakingTip(), 0
		case packTaking:
			// Its tip says how far it has come, as the island's figures do.
			tip, dev = locales.CaptureTakingTip()+" "+percentText(it.percent(c, c.Now())), b.AnimateTo("dev", it.shown(c.Now()), motion.Develop)
		case packTaken:
			tip, lit = locales.CaptureReadyTip(), true
		case packFailed:
			tip = locales.CaptureFailedTip() // pressed, its card says what happened and offers 重试
		}
	}
	copied := false
	if it != nil && it.ID == h.copiedID && h.copiedHome == homeButton {
		now := c.Now()
		from, to := h.copiedNote()
		copied = !now.Before(from) && now.Before(to)
		switch {
		case now.Before(from):
			c.After(from.Sub(now))
		case copied:
			c.After(to.Sub(now))
			tip = locales.CommonCopied()
		}
	}
	b.Children(func() {
		box := ui.Box(c).Size(16, 16).Shrink(0)
		light, _ := a.lightOf(c, b, box)
		dots := captureDots(dev, lit, fg)
		if flow := a.flow(c, box, copied); flow > 0 {
			dots = flowed(dots, flow)
		}
		box.Draw(func(p *ui.Painter, r ui.Rect) { paintDots(p, r, dots, light, 0) })
	})
	a.tip(c, b, tip, false)
	a.control(b)
	if !b.Clicked() {
		return
	}
	if it != nil {
		a.openPack(it, b.LayoutBounds(), homeButton)
		return
	}
	var from ui.Rect
	if r := b.LayoutBounds(); !a.kbd && !a.still(c) {
		from = ui.Rect{X: r.X + r.W/2 - 8, Y: r.Y + r.H/2 - 5, W: 16, H: 10}
	}
	link, name := a.packTarget(rp, art)
	a.takePage(art.ID, link, name, a.packPicture(rp, art), from, c.Now())
}

// islandItems are what the island shows, newest first: the packs being taken
// and waiting, those that failed until they leave, and those just taken, for
// CaptureLinger, or that long after the island was last held, unless copied. A
// pack whose card grew out of it stays while that card is open or going back
// into it, and after a copy while the island says so. Sent into 收藏
// (收进收藏), it shows only that.
func (a *App) islandItems(c *ui.Context) []*packItem {
	h := &a.capture
	now := c.Now()
	ours := h.home == homeIsland
	going := false
	if h.back != "" && ours {
		going, _ = a.landing(c, "pack")
	}
	_, said := h.copiedNote()
	saying := h.copiedHome == homeIsland && now.Before(said)
	if saying {
		c.After(said.Sub(now))
	}
	var out []*packItem
	for _, it := range h.items {
		on := false
		switch {
		case ours && it.ID == h.card, going && it.ID == h.back, saying && it.ID == h.copiedID:
			on = true
		case h.away || it.filed:
		case it.state == packTaken:
			until := it.takenAt.Add(motion.CaptureLinger)
			if h.heldAt.After(it.takenAt) {
				until = h.heldAt.Add(motion.CaptureLinger)
			}
			if on = !it.Used && now.Before(until); on {
				c.After(until.Sub(now))
			}
		default:
			on = true
		}
		if on {
			out = append(out, it)
		}
	}
	// From the left: those taken or failed, then those waiting, then the page
	// being taken, next to its figures (islandRank); newest first within each.
	slices.SortStableFunc(out, func(x, y *packItem) int { return islandRank(x) - islandRank(y) })
	return out
}

// islandFront is which of items (islandItems, more than islandShown) show side
// by side; the others stack behind. Those to wait for least go behind: the
// earliest taken or failed first, then the latest to be taken of those
// waiting; the one being taken, and the one just taken, always show.
func islandFront(items []*packItem) []*packItem {
	out := slices.Clone(items)
	done := slices.IndexFunc(out, func(it *packItem) bool { return islandRank(it) > 0 }) // past the taken and failed, newest first
	if done < 0 {
		done = len(out)
	}
	for over := len(out) - islandShown; over > 0; over-- {
		if done > 1 {
			done--
			out = slices.Delete(out, done, done+1) // the earliest taken
			continue
		}
		at := done // the newest waiting
		if at >= len(out) || out[at].state != packWaiting {
			at = 0
		}
		out = slices.Delete(out, at, at+1)
	}
	return out
}

// islandRank is where a pack stands in the island, from the left.
func islandRank(it *packItem) int {
	switch it.state {
	case packWaiting:
		return 1
	case packTaking:
		return 2
	}
	return 0
}

// captureLayers are the island, the thumbnails flying into it and out of it into
// 收藏, and the card, over the pages.
func (a *App) captureLayers(c *ui.Context) {
	h := &a.capture
	now := c.Now()
	busy := false
	// The frames see when each pack's moments came. Out of sight, 收藏's tab says
	// what came of each, one that failed in a note under it; a pack that failed
	// leaves the island after a while and stays in 收藏 until it is deleted or
	// taken again.
	for _, it := range h.items {
		if it.base != nil && it.baseAt.IsZero() {
			it.baseAt = now
		}
		if it.first != nil && it.firstAt.IsZero() {
			it.firstAt = now
		}
		switch {
		case it.state == packWaiting:
			busy = true
		case it.state == packTaking:
			busy = true
			if it.startedAt.IsZero() {
				it.startedAt = now
			}
		case it.state == packTaken && it.takenAt.IsZero():
			it.takenAt = now
			if h.away {
				// Taken out of sight: straight into 收藏, whose tab says so.
				it.filed = true
				a.lights[favLight] = now
				c.AnimationFrame() // the sidebar, built before, lights in the next
			}
		case it.state == packFailed && it.failedAt.IsZero():
			it.failedAt = now
			if h.away {
				it.filed = true
				a.postFailed(it, now)
			}
		case it.state == packFailed && !it.filed && it.ID != h.card:
			if left := motion.CaptureFailedFor - now.Sub(it.failedAt); left > 0 {
				c.After(left)
			} else {
				it.filed = true
			}
		}
	}
	// Sent away, the island stays away until it is down and nothing is being taken.
	if h.away && !h.up && !busy {
		h.away = false
	}
	items := a.islandItems(c)
	on := len(items) > 0
	if !h.away {
		// What the island showed and lets go of now, taken, flies into 收藏 from its
		// thumbnail; one stacked out of sight, or from the keys, lights the tab at once.
		for _, it := range h.last {
			if it.gone || it.filed || it.state != packTaken || slices.Contains(items, it) {
				continue
			}
			it.filed = true
			if from, ok := h.slots[it.ID]; ok && !a.kbd {
				h.files = append(h.files, &captureFile{it: it, from: from, still: a.still(c)})
			} else {
				a.lights[favLight] = now
				c.AnimationFrame()
			}
		}
	}
	if !on {
		h.focused = false // the keys cannot be in an island going away: Esc is the page's again
	}
	switch {
	case on:
		h.last = items
	case !h.away:
		// Going back into its point, it holds what had nowhere else to go.
		h.last = slices.DeleteFunc(h.last, func(it *packItem) bool { return it.filed || it.gone })
	}
	ui.Overlay(c, func() {
		if on || h.up {
			a.island(c, h.last, on)
			a.captureFlights(c)
		} else {
			clear(h.slots)
			clear(h.placed)
		}
		a.captureFiles(c)
		a.captureHoming(c)
		a.capturePost(c)
	})
	a.packCard(c)
}

// captureFiles draw the thumbnails on their way from the island into 收藏's tab:
// along a low arc, on Flight, as long as the way is (FlightFor), from 72
// across to the tab's icon, each as it stands; landing, they light the tab.
// Under less motion each fades where it was, and the tab lights as it is gone.
func (a *App) captureFiles(c *ui.Context) {
	h := &a.capture
	now := c.Now()
	to := a.favTab
	live := h.files[:0]
	for _, f := range h.files {
		x0, y0 := float64(f.from.X+f.from.W/2), float64(f.from.Y+f.from.H/2)
		x1, y1 := float64(to.X+to.W/2), float64(to.Y+to.H/2)
		if f.start.IsZero() {
			f.start, f.length = now, motion.FlightFor(math.Hypot(x1-x0, y1-y0))
			if f.still {
				f.length = motion.PopIn
			}
		}
		t := now.Sub(f.start)
		if t >= f.length || to.W <= 0 || f.it.gone {
			a.lights[favLight] = now
			c.AnimationFrame()
			continue
		}
		k := float64(motion.Flight(float32(t) / float32(f.length)))
		mx, my := (x0+x1)/2, math.Max(y0, y1)+math.Min(56, math.Hypot(x1-x0, y1-y0)*0.12)
		x := (1-k)*(1-k)*x0 + 2*(1-k)*k*mx + k*k*x1
		y := (1-k)*(1-k)*y0 + 2*(1-k)*k*my + k*k*y1
		w := float64(f.from.W) + (float64(to.W)-float64(f.from.W))*k
		o := float32(1)
		if f.still {
			x, y, w, o = x0, y0, float64(f.from.W), 1-float32(t)/float32(f.length)
		}
		ht := w * thumbH / thumbW
		box := ui.Box(c.Key("file-"+f.it.ID)).Absolute().Left(float32(x-w/2)).Top(float32(y-ht/2)).Size(float32(w), float32(ht)).
			Radius(float32(math.Max(2, 6*w/thumbW))).Background(theme.Window).ClipX().ClipY().Shadow(0, 8, 20, -8, theme.ShadowDialog).
			Opacity(o).IgnoreInput(true).AccessHidden(true)
		it, still := f.it, f.still
		box.Children(func() { thumbFace(c, it, now, still) })
		live = append(live, f)
		c.AnimationFrame()
	}
	h.files = live
}

// island is the island holding items, growing out of its point at the top
// edge the first time and, once on is false, going back into it whole.
func (a *App) island(c *ui.Context, items []*packItem, on bool) {
	h := &a.capture
	now := c.Now()
	still := a.still(c)
	isl := ui.Box(c.Key("island")).Absolute().Role(ui.RoleGroup).Label(locales.CaptureIsland()).ObserveInput(a.observe)
	target := map[bool]float32{true: 1}[on]
	var k float32
	switch {
	case a.kbd:
		k = isl.Jump("grown", target)
	case !h.up:
		k = isl.Jump("grown", 0) // from its point, in the next frame
		c.Invalidate()
	case still:
		k = isl.AnimateTo("grown", target, motion.Fade(motion.PopIn))
	default:
		k = isl.AnimateTo("grown", target, motion.Island)
	}
	if !on && k < 0.002 && h.up && isl.Settled("grown") {
		h.up, h.last = false, nil
		clear(h.slots)
		clear(h.placed)
		return
	}
	h.up = true

	hovered := isl.Hovered() && !a.kbd
	// Pointed at, or its card open, it holds what was just taken; the keys in it, Esc sends it away (shortcuts).
	if hovered || h.card != "" && h.home == homeIsland {
		h.heldAt = now
	}
	h.focused = on && isl.FocusWithin()
	var s float32 = 1
	if !still {
		s = 1 + 0.12*isl.AnimateTo("hover", map[bool]float32{true: 1}[hovered], motion.Follow)
	}
	all, n := items, len(items)
	cols, edges := min(n, islandShown), 0
	if n > islandShown {
		// The rest stack behind the first shown, their edges showing at its left.
		edges, items = min(n-islandShown, islandEdges), islandFront(items)
	}
	tw, th := thumbW*s, thumbH*s
	thumbsW := islandPad + float32(cols)*tw + float32(max(0, cols-1))*islandGap + 2*float32(edges)
	markX := thumbsW + islandMarkGap
	w := markX + islandStatusW + islandMarkGap
	ht := 2*islandPad + th
	// Just copied, once its card is back in its thumbnail, the island grows by a
	// word after the mark to say so, and gives it back.
	note := locales.CommonCopied()
	from, to := h.copiedNote()
	noting := h.copiedHome == homeIsland && !now.Before(from) && now.Before(to) &&
		slices.ContainsFunc(items, func(it *packItem) bool { return it.ID == h.copiedID })
	if now.Before(from) {
		c.After(from.Sub(now))
	}
	noteX := markX + islandStatusW/2 + 8 + islandNoteGap
	// 收进收藏 comes after the status while it is pointed at or the keys are in it.
	open := on && (hovered || h.focused) && !noting
	if noting {
		nw, _ := c.MeasureText(0, ui.Span{Text: note, Size: theme.Body})
		w = noteX + nw + islandNoteRight
	} else if open {
		w += islandHideW
	}
	var shown float32
	if a.kbd || still {
		shown = isl.Jump("hide", map[bool]float32{true: 1}[open])
	} else {
		shown = isl.AnimateTo("hide", map[bool]float32{true: 1}[open], a.ease(motion.Hover))
	}
	var said float32
	if a.kbd {
		said = isl.Jump("note", map[bool]float32{true: 1}[noting])
	} else {
		said = isl.AnimateTo("note", map[bool]float32{true: 1}[noting], motion.Fade(motion.PopIn))
	}
	size := func(key string, v float32) float32 {
		if a.kbd || k < 0.01 || still {
			return isl.Jump(key, v)
		}
		return isl.AnimateTo(key, v, motion.Follow)
	}
	if on {
		h.wide, h.tall, h.markAt = w, ht, markX
	} else if h.wide > 0 {
		w, ht, markX = h.wide, h.tall, h.markAt
	}
	w, ht = size("w", w), size("h", ht)
	area := a.area
	if area.W <= 0 {
		vw, _ := c.Size()
		area = ui.Rect{W: vw}
	}
	left := float32(math.Round(float64(area.X + area.W/2 - w/2)))
	h.islandRect = ui.Rect{X: left, Y: islandTop, W: w, H: ht}
	isl.Left(left).Top(islandTop).Width(w).Height(ht).Radius(12).Background(theme.Elevated).
		InsetShadow(0, 0, 0, 1, theme.Line).Shadow(0, 0, 0, 1, theme.ShadowEdge).Shadow(0, 12, 32, 0, theme.ShadowFloat)
	if still {
		isl.Opacity(k)
	} else if k < 0.999 {
		// One whole, from a point on the window's top edge.
		isl.TransformOrigin(0.5, -islandTop/ht).Scale(k, k).Opacity(min(1, 4*k))
	}
	a.control(isl)
	built := map[string]bool{}
	isl.Children(func() {
		// The earliest stack behind the first shown, their edges showing.
		first := islandPad + 2*float32(edges) + tw/2
		for e := edges; e >= 1; e-- {
			ui.Box(c.Key(e)).Absolute().Left(first-thumbW/2-2*float32(e)).Top(islandPad+th/2-thumbH/2).Size(thumbW, thumbH).
				TransformOrigin(0.5, 0.5).Scale(s, s).Radius(6).Background(theme.Window.Mix(theme.Elevated, 0.25*float32(e-1))).
				AccessHidden(true).IgnoreInput(true)
		}
		for i, it := range items {
			cx := first + float32(i)*(tw+islandGap)
			cy := islandPad + th/2
			h.slots[it.ID] = ui.Rect{X: left + cx - tw/2, Y: islandTop + cy - th/2, W: tw, H: th}
			built[it.ID] = true
			a.packThumb(c, it, cx, cy, s, hovered, now)
		}
		a.islandStatus(c, all, markX, ht, noting, now)
		if on {
			a.islandHide(c, markX+islandStatusW, ht, shown, now)
		}
		if said > 0.01 {
			ui.Row(c.Key("note")).Absolute().Left(noteX).Top(0).Height(ht).AlignItems(ui.Center).
				Opacity(said * said).IgnoreInput(true).AccessHidden(true).Children(func() {
				ui.Text(c, note).FontSize(theme.Body).TextColor(theme.Text).NoWrap()
			})
		}
	})
	for id := range h.slots {
		if !built[id] {
			delete(h.slots, id)
		}
	}
	h.placed = built
	// Esc with the keys in it sends it away (收进收藏), at once. The island asks
	// for it, around the focus: the tip of the thumbnail the keys are on, the
	// layer on top, would take the first Esc from the page.
	if on && isl.Shortcut(0, ui.KeyEscape) {
		a.hideIsland(c, now)
	}
}

// islandStatus is the island's right end, at x: how far the page being taken
// has come, in hundredths (percent), and, a moment after one is taken, its
// 100%; else the capture mark, lit once all the island holds are taken. Just
// copied, the mark's dots flow into a check while the island says so. The
// figures and the mark cross where they are.
func (a *App) islandStatus(c *ui.Context, items []*packItem, x, ht float32, copied bool, now time.Time) {
	h := &a.capture
	var at *packItem
	lit := len(items) > 0
	for _, it := range items {
		switch {
		case it.state == packTaking && at == nil,
			it.state == packTaken && now.Sub(it.takenAt) < motion.CaptureRise+motion.CapturePercent:
			at = it
		}
		lit = lit && it.state == packTaken
	}
	if at != nil && !copied {
		h.pct = at.percent(c, now)
		if at.state == packTaken {
			c.After(motion.CaptureRise + motion.CapturePercent - now.Sub(at.takenAt))
		}
	}
	slot := ui.Box(c.Key("status")).Absolute().Left(x).Top(0).Size(islandStatusW, ht).IgnoreInput(true).AccessHidden(true)
	figures := slot.AnimateTo("figures", map[bool]float32{true: 1}[at != nil && !copied], a.ease(motion.SwapIn))
	slot.Children(func() {
		if figures > 0.01 {
			ui.Row(c.Key("figures")).Absolute().Fill().AlignItems(ui.Center).Justify(ui.Center).Opacity(figures).Children(func() {
				ui.Text(c, percentText(h.pct)).FontSize(theme.Body).FontFeatures(theme.Numbers).TextColor(theme.Text).NoWrap()
			})
		}
		if figures < 0.99 {
			box := ui.Box(c.Key("mark")).Absolute().Left((islandStatusW-16)/2).Top((ht-16)/2).Size(16, 16).Opacity(1 - figures)
			dots := captureDots(-1, lit, theme.Icon)
			if flow := a.flow(c, box, copied); flow > 0 {
				dots = flowed(dots, flow)
			}
			box.Draw(func(p *ui.Painter, r ui.Rect) { paintDots(p, r, dots, dotLight{}, 0) })
		}
	})
}

// islandHide is 收进收藏 at x, shown as far as shown goes: 收藏's own icon, for
// that is where it sends what the island holds (hideIsland).
func (a *App) islandHide(c *ui.Context, x, ht, shown float32, now time.Time) {
	b := ui.ButtonBase(c.Key("hide")).Label(locales.CaptureHide()).Absolute().Left(x).Top(islandPad).
		Size(islandHideW, ht-2*islandPad).Radius(6).Center().FocusRing(false)
	if shown < 0.5 && !b.Focused() {
		b.IgnoreInput(true) // not there yet: the pointer passes to what is under it
	}
	if shown < 0.999 {
		b.Opacity(shown)
	}
	color := theme.Icon
	if b.Hovered() {
		color = theme.Text
	}
	fg := b.AnimateColor("fg", color, a.ease(motion.Hover))
	b.TextColor(fg)
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	b.Children(func() { a.dotIcon(c, b, "favorites", 16, iconLook{ink: fg}) })
	a.tip(c, b, locales.CaptureHide(), false)
	a.control(b)
	if b.Clicked() {
		a.hideIsland(c, now)
	}
}

// packThumb is it's thumbnail at (cx, cy) in the island, scaled by s: its
// picture developing, the first screen rising out of the dots once taken, a
// light passing over it. Its tip says whose it is and how it stands.
func (a *App) packThumb(c *ui.Context, it *packItem, cx, cy, s float32, hovered bool, now time.Time) {
	h := &a.capture
	still := a.still(c)
	label := locales.CaptureTaking(it.Name)
	switch {
	case it.state == packWaiting:
		label = locales.CaptureWaiting(it.Name)
	case it.state == packFailed:
		label = locales.CaptureNotTaken(it.Name)
	case it.state == packTaken && it.Used:
		label = locales.CaptureUsed(it.Name)
	case it.state == packTaken:
		label = locales.CaptureReady(it.Name)
	}
	b := ui.ButtonBase(c.Key(it.ID)).Label(label).Absolute().Size(thumbW, thumbH).Radius(6).ClipX().ClipY().
		Background(theme.Window).FocusRing(false).TransformOrigin(0.5, 0.5)
	fresh := !h.placed[it.ID]
	place := func(key string, v float32) float32 {
		if fresh || a.kbd || still {
			return b.Jump(key, v)
		}
		return b.AnimateTo(key, v, motion.Follow)
	}
	b.Left(place("x", cx-thumbW/2)).Top(place("y", cy-thumbH/2))
	if s != 1 {
		b.Scale(s, s)
	}
	// Pointed at, the one pointed at is bright and the others a step back; one
	// waiting its turn stands a step back until it starts.
	o := float32(1)
	switch {
	case hovered && b.Hovered():
	case it.state == packWaiting:
		o = 0.6
	case hovered:
		o = 0.8
	}
	o = b.AnimateTo("o", o, a.ease(motion.Hover))
	// Under less motion it fades in where it comes.
	if fresh && still {
		h.fresh[it.ID] = struct{}{}
		b.Jump("in", 0)
		c.Invalidate()
	} else if _, ok := h.fresh[it.ID]; ok {
		in := b.AnimateTo("in", 1, motion.Fade(motion.PopIn))
		o *= in
		if in > 0.999 {
			delete(h.fresh, it.ID)
		}
	}
	if slices.ContainsFunc(h.flights, func(f *captureFlight) bool { return f.id == it.ID }) ||
		slices.ContainsFunc(h.files, func(f *captureFile) bool { return f.it == it }) {
		o = 0 // it is on its way here, or away into 收藏
	}
	if o < 1 {
		b.Opacity(o)
	}
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	a.tip(c, b, label, false)
	d := a.developingOf(c, b, it, now, still)
	rise, sweep := a.risen(c, it, now, still)
	b.Children(func() {
		if rise < 1 || it.shot == nil {
			ui.Box(c).Absolute().Left(0).Top(0).Size(thumbW, thumbH).IgnoreInput(true).
				Draw(func(p *ui.Painter, r ui.Rect) { paintDeveloping(p, r, d) })
		}
		if rise > 0 && it.shot != nil {
			img := ui.Image(c, it.shot).Absolute().Left(0).Top(0).Size(thumbW, thumbH).Fit(ui.Cover).IgnoreInput(true)
			if rise < 1 {
				img.Opacity(rise)
				if !still {
					img.Blur(1.5 * (1 - rise))
				}
			}
		}
		if sweep > 0 && sweep < 1 {
			ui.Box(c).Absolute().Left(0).Top(0).Size(thumbW, thumbH).IgnoreInput(true).
				Draw(func(p *ui.Painter, r ui.Rect) { paintSweep(p, r, sweep, 6, 125) })
		}
	})
	if b.Clicked() {
		a.openPack(it, h.slots[it.ID], homeIsland)
	}
}

// developingOf is how it's picture stands in e this frame: how far it has come
// (shown), followed on the follow spring (devAt).
func (a *App) developingOf(c *ui.Context, e ui.Element, it *packItem, now time.Time, still bool) developing {
	d := devAt(it, e.AnimateTo("p", it.shown(now), motion.Develop), now, still)
	if it.base != nil && d.baseIn < 1 || it.first != nil && d.recolour < 1 {
		c.AnimationFrame()
	}
	return d
}

// devAt is it's picture developed as far as p: its dots going from grey (the
// ground, while the app's picture is on its way) to that picture as it comes,
// and over to its first screen's
// colours once that comes, each over CaptureRecolour; falling once it failed.
func devAt(it *packItem, p float32, now time.Time, still bool) developing {
	d := developing{from: it.base, to: it.first, ground: theme.CaptureDot, p: p, shown: 1, still: still}
	if it.basing {
		d.ground = theme.Window // its picture is a moment away: it waits unseen rather than flash grey
	}
	if it.base != nil {
		d.baseIn = 1
		if !it.baseAt.IsZero() {
			d.baseIn = motion.Ease(unit(float32(now.Sub(it.baseAt)) / float32(motion.CaptureRecolour)))
		}
	}
	if it.first != nil {
		d.recolour = motion.Ease(unit(float32(now.Sub(it.firstAt)) / float32(motion.CaptureRecolour)))
	}
	if it.state == packFailed {
		d.failed = it.failedAt
	}
	return d
}

// risen is how far it's first screen has risen out of its dots, and how far
// the light has passed over it, from when it was taken.
func (a *App) risen(c *ui.Context, it *packItem, now time.Time, still bool) (rise, sweep float32) {
	if it.state != packTaken {
		return 0, 0
	}
	t := now.Sub(it.takenAt)
	rise = motion.Ease(unit(float32(t) / float32(motion.CaptureRise)))
	if !still {
		sweep = unit(float32(t-motion.CaptureRise) / float32(motion.CaptureSweep))
	}
	if t < motion.CaptureRise+motion.CaptureSweep {
		c.AnimationFrame()
	}
	return rise, sweep
}

// captureFlights draw the thumbnails on their way from the button into the
// island: along a low arc, over CaptureFly along Flight, from 16 across to 72.
func (a *App) captureFlights(c *ui.Context) {
	h := &a.capture
	now := c.Now()
	live := h.flights[:0]
	for _, f := range h.flights {
		it, to := h.find(f.id), h.slots[f.id]
		if it == nil || to.W <= 0 {
			// Its place is not laid out yet: wait for it, a few frames at most.
			if f.tries++; it != nil && f.tries < 30 {
				live = append(live, f)
				c.AnimationFrame()
			}
			continue
		}
		if f.start.IsZero() {
			f.start = now
		}
		t := now.Sub(f.start)
		if t >= motion.CaptureFly {
			continue
		}
		k := float64(motion.Flight(float32(t) / float32(motion.CaptureFly)))
		x0, y0 := float64(f.from.X+f.from.W/2), float64(f.from.Y+f.from.H/2)
		x1, y1 := float64(to.X+to.W/2), float64(to.Y+to.H/2)
		// Low: it dips under the straight way by an eighth of it, at most 56.
		mx, my := (x0+x1)/2, math.Max(y0, y1)+math.Min(56, math.Hypot(x1-x0, y1-y0)*0.12)
		x := (1-k)*(1-k)*x0 + 2*(1-k)*k*mx + k*k*x1
		y := (1-k)*(1-k)*y0 + 2*(1-k)*k*my + k*k*y1
		w := float64(f.from.W) + float64(to.W-f.from.W)*k
		ht := w * thumbH / thumbW
		d := devAt(it, it.shown(now), now, false)
		ui.Box(c.Key("fly-"+f.id)).Absolute().Left(float32(x-w/2)).Top(float32(y-ht/2)).Size(float32(w), float32(ht)).
			Radius(float32(math.Max(2, 6*w/thumbW))).Background(theme.Window).ClipX().ClipY().Shadow(0, 8, 20, -8, theme.ShadowDialog).
			IgnoreInput(true).AccessHidden(true).Children(func() {
			ui.Box(c).Absolute().Fill().Draw(func(p *ui.Painter, r ui.Rect) { paintDeveloping(p, r, d) })
		})
		live = append(live, f)
		c.AnimationFrame()
	}
	h.flights = live
}

// packCard is the open pack's card, grown out of its thumbnail as the
// dialogs grow out of their buttons; Esc or a press outside sends it back in.
func (a *App) packCard(c *ui.Context) {
	h := &a.capture
	it := h.find(h.card)
	open := it != nil
	// Copied, the card goes back into its thumbnail once the light has passed;
	// at once from the keys or under less motion.
	if open && !h.copied.IsZero() {
		if left := h.copiedBack() - c.Now().Sub(h.copied); left > 0 {
			c.After(left)
		} else {
			open = false
		}
	}
	a.centred(c, "pack", &open, motion.Dialog(), func(panel ui.Element) {
		panel.Width(cardW).Shadow(0, 0, 0, 1, theme.Line).Shadow(0, 24, 64, 0, theme.ShadowDialog).ClipX().ClipY()
		if it != nil {
			panel.Label(it.Name)
		}
		// One Esc sends it back. Opened from the keys, the card's focus is on 复制,
		// whose tip, the layer on top, would take the first Esc; the card is around
		// the focus, so it takes it first.
		if panel.Shortcut(0, ui.KeyEscape) {
			open = false
		}
	}, func() {
		if it != nil {
			a.packCardView(c, it)
		}
	})
	if !open && h.card != "" {
		h.back, h.card = h.card, ""
		// Going back into its thumbnail, the page rolls back to its first screen, which the thumbnail shows.
		h.scroll.ScrollTo(0, 0, motion.Follow)
	}
}

// packCardView is the card: the page itself across the whole of it (its dots
// while it is not taken), scrolled through as a page is, its specimen past its
// foot (packLeaf); its name on a fall of dark at the foot, and its tools as
// ink-dot icons at the foot's right: 复制 always, lit; 在访达中显示 and delete
// while the card is pointed at or the keys are in it. One that failed says
// what happened, with 重试 (DESIGN.md Capture 卡片).
func (a *App) packCardView(c *ui.Context, it *packItem) {
	h := &a.capture
	now := c.Now()
	still := a.still(c)
	dir := filepath.Join(h.dir, it.ID)
	card := ui.Box(c.Key("picture")).Width(cardW).Height(cardH).Background(theme.Window).ClipX().ClipY()
	if it.state == packTaken {
		card.Label(packHolds(it))
	}
	// The keys in it count, not the focus the card opens with.
	near := card.Hovered() && !a.kbd || a.kbd && card.FocusWithin()
	var k float32
	if a.kbd || still {
		k = card.Jump("near", map[bool]float32{true: 1}[near])
	} else {
		k = card.AnimateTo("near", map[bool]float32{true: 1}[near], motion.Follow)
	}
	d := a.developingOf(c, card, it, now, still)
	rise, sweep := a.risen(c, it, now, still)
	// Copied, a light passes over the picture: what is given to the AI.
	var passed float32
	if t := now.Sub(h.copied); !h.copied.IsZero() && t < motion.CaptureSweep && !still && !h.copyKbd {
		passed = float32(t) / float32(motion.CaptureSweep)
		c.AnimationFrame()
	}
	type tool struct {
		key, icon, label, tip string
		lit, done, danger     bool
		always                bool
		press                 func()
	}
	var tools []tool
	switch it.state {
	case packTaken:
		said := !h.copied.IsZero() && now.Sub(h.copied) < motion.Copied
		if said {
			c.After(motion.Copied - now.Sub(h.copied))
		}
		label, tip := locales.CommonCopy(), locales.CaptureCopyTip()
		if said {
			label, tip = locales.CommonCopied(), locales.CommonCopied()
		}
		tools = append(tools,
			tool{key: "copy", icon: "copy", label: label, tip: tip, lit: true, done: said, always: true, press: func() {
				c.Services().WriteClipboard(locales.CaptureBrief(it.Name, it.URL, dir))
				h.copied, h.copiedID, h.copiedHome, h.copyKbd = now, it.ID, h.home, a.kbd || still
				a.said = append(a.said, locales.CommonCopied())
				if !it.Used {
					it.Used = true
					a.savePacks()
				}
			}},
			tool{key: "reveal", icon: "folder", label: locales.CaptureReveal(), tip: locales.CaptureReveal(), press: func() { h.reveal(dir) }})
	case packFailed:
		tools = append(tools, tool{key: "retry", icon: "refresh", label: locales.CommonTryAgain(), tip: locales.CommonTryAgain(), lit: true, always: true,
			press: func() { a.retryPack(it, now) }})
	}
	tools = append(tools, tool{key: "delete", icon: "trash", label: locales.CommonDelete(), tip: locales.CommonDelete(), danger: true,
		press: func() { a.dropPack(it) }})
	// While it is being taken, how far it has come stands where 复制 will be (排队中
	// while it waits), the tools to its left; it gives way to 复制 as the first
	// screen rises.
	share, shareW := "", float32(0)
	switch {
	case it.state == packTaking:
		share = percentText(it.percent(c, now))
	case it.state == packWaiting:
		share = locales.CaptureQueued()
	case it.state == packTaken && rise < 1 && !still:
		share = percentText(100)
	}
	toolsRight := float32(cardToolsRight)
	if share != "" {
		shareW, _ = c.MeasureText(0, ui.Span{Text: share, Size: theme.Body})
		shareW = max(32, shareW)
		if it.state != packTaken {
			toolsRight += shareW + 2
		}
	}

	card.Children(func() {
		if rise < 1 || it.shot == nil {
			ui.Box(c.Key("dots")).Absolute().Left(0).Top(0).Size(cardW, cardH).IgnoreInput(true).AccessHidden(true).
				Draw(func(p *ui.Painter, r ui.Rect) { paintDeveloping(p, r, d) })
		}
		if rise > 0 && it.shot != nil {
			a.packLeaf(c, it, rise, still)
		}
		for i, s := range []float32{sweep, passed} {
			if s > 0 && s < 1 {
				angle := float32(125)
				if i == 1 {
					angle = 305 // from the copy button, at the bottom right
				}
				ui.Box(c.Key(i)).Absolute().Left(0).Top(0).Size(cardW, cardH).IgnoreInput(true).AccessHidden(true).
					Draw(func(p *ui.Painter, r ui.Rect) { paintSweep(p, r, s, 12, angle) })
			}
		}
		// Under the words the picture frosts over, more toward the foot, so what the
		// page has there does not show through them as a second line of type.
		if rise > 0 && it.shot != nil {
			frost := ui.Box(c.Key("frost")).Absolute().Left(0).Right(0).Bottom(0).Height(cardFall).ClipX().ClipY().IgnoreInput(true).AccessHidden(true).
				MaskLinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{{Color: ui.Transparent, At: 0.15}, {Color: theme.Text, At: 0.7}}})
			if rise < 1 {
				frost.Opacity(rise)
			}
			frost.Children(func() {
				// The screens under the foot as the page is scrolled, each larger by twice the
				// blur all round, so it does not thin out at the card's edges.
				const over = 2 * cardFrost
				at := h.scroll.Y + cardH - cardFall
				for i, top := range leafTops(it) {
					if top+cardH < at-over || top > at+cardFall+over {
						continue
					}
					if shot := leafShot(it, i); shot != nil {
						ui.Image(c.Key(i), shot).Absolute().Left(-over).Top(top-at-over).Size(cardW+2*over, cardH+2*over).Fit(ui.Cover).Blur(cardFrost)
					}
				}
			})
		}
		// The fall of dark the words stand on, eased so it has no edge.
		ui.Box(c.Key("fall")).Absolute().Left(0).Right(0).Bottom(0).Height(cardFall).IgnoreInput(true).AccessHidden(true).
			LinearGradient(ui.LinearGradient{Angle: 180, Stops: fallStops()})
		// A hairline inside the edge, so a light page keeps its corners on the shade.
		ui.Box(c.Key("edge")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Radius(12).InsetShadow(0, 0, 0, 1, theme.CaptureEdge).
			IgnoreInput(true).AccessHidden(true)

		// The words: the name, in the page's own face when it has one for it, level
		// with the tools.
		words := ui.Column(c.Key("words")).Absolute().Left(20).Bottom(cardWordsBottom).Right(toolsRight + float32(len(tools))*cardToolStep + 12).
			Gap(cardWordsGap).IgnoreInput(true)
		words.Children(func() {
			name := ui.Text(c, it.Name).FontSize(theme.Subtitle).LineHeight(26.0 / 20).TextColor(theme.Text).SingleLine().Ellipsis("…")
			if it.font != "" && latinName(it.Name) {
				name.Font(it.font).FontWeight(it.TitleWeight)
			} else {
				name.FontWeight(600).LetterSpacing(-0.012 * theme.Subtitle)
			}
			if it.state == packFailed {
				ui.Text(c, it.failed).FontSize(theme.Body).LineHeight(18.0 / 13).TextColor(theme.Text2).MaxLines(2).Ellipsis("…")
			}
		})
		if it.state == packTaken {
			a.leafLine(c, it, k, now)
		}

		if share != "" {
			o := float32(1)
			if it.state == packTaken {
				o = 1 - rise
			}
			ui.Row(c.Key("share")).Absolute().Right(cardToolsRight).Bottom(10).Height(32).Width(shareW).Justify(ui.Center).AlignItems(ui.Center).
				Opacity(o).IgnoreInput(true).AccessHidden(true).Children(func() {
				ui.Text(c, share).FontSize(theme.Body).FontFeatures(theme.Numbers).TextColor(theme.Text).NoWrap()
			})
		}
		// The tools, the first at the right; the others come in one after another.
		for i, t := range tools {
			b := a.cardTool(c, t.key, t.icon, t.label, t.tip, t.lit, t.done, t.danger)
			b.Absolute().Right(toolsRight + float32(i)*cardToolStep).Bottom(10)
			if t.always && share != "" && it.state == packTaken {
				b.Opacity(rise) // 复制 comes in as the figures go
			}
			if !t.always {
				o := unit((k - 0.15*float32(i-1)) / 0.85)
				if b.FocusVisible() {
					o = 1
				}
				if o < 0.999 {
					b.Opacity(o)
				}
			}
			if b.Clicked() {
				t.press()
			}
		}
	})
}

// cardTool is one of the card's tools, on the picture: an ink-dot icon with no
// ground, brighter under the pointer; lit, its accent amber; done, copy's dots
// flowed into a check; danger, delete, going danger under the pointer.
func (a *App) cardTool(c *ui.Context, key, icon, label, tip string, lit, done, danger bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Label(label).Size(32, 32).Radius(8).Center().Shrink(0).FocusRing(false)
	color := theme.Icon
	if b.Hovered() {
		color = theme.Text
		if danger {
			color = theme.Danger
		}
	}
	fg := b.AnimateColor("fg", color, a.ease(motion.Hover))
	b.TextColor(fg)
	if b.FocusVisible() {
		b.FocusRing(true).FocusRingOffset(-2)
	}
	b.Children(func() { a.dotIcon(c, b, icon, 16, iconLook{lit: lit, ink: fg, copied: done}) })
	a.tip(c, b, tip, false)
	return b
}

// latinName tells whether name is all Latin letters, figures and punctuation, as
// a page's own face sets them: one in another script keeps the app's face whole,
// rather than mixing two.
func latinName(name string) bool {
	for _, r := range name {
		if r > 0x24F && !(r >= 0x2000 && r <= 0x206F) {
			return false
		}
	}
	return name != ""
}

// The card's foot (DESIGN.md Capture 卡片): how high the fall of dark rises,
// where the tools stand from the right edge and how far apart.
const (
	cardFall       = 132
	cardFrost      = 14
	cardToolsRight = 12
	cardToolStep   = 34
	// The words: how high their foot stands, so the name is level with the tools.
	cardWordsBottom = 13
	cardWordsGap    = 6
)

// fallStops are the fall of dark (DESIGN.md The Words Over Pictures Rule):
// scrim from the words' first line down, eased away above it so no band shows
// where it begins.
func fallStops() []ui.GradientStop {
	var stops []ui.GradientStop
	for _, s := range [][2]float32{{0, 0}, {0.22, 0.06}, {0.4, 0.3}, {0.55, 0.66}, {0.66, 0.9}, {0.72, 1}} {
		stops = append(stops, ui.GradientStop{Color: theme.Scrim.Alpha(float32(theme.Scrim.A) / 255 * s[1]), At: s[0]})
	}
	return stops
}
