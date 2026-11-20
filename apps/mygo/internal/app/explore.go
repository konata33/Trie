package app

import (
	"encoding/base64"
	"image"
	"log"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	xdraw "golang.org/x/image/draw"

	"trie/internal/explore"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/theme"
	"trie/internal/web"
)

// Explore: two rooms side by side, design on the left and writing on the
// right. Each room's front page fills the window and chooses: the newest
// works in turn with the galleries' frames, the writers' front page. Below it every source of the room runs on in one
// stream, freshest first, the next few coming in as the end nears. A card
// opens its author's panel, its picture flying into the panel's thumbnail.
// A sideways swipe drags the rooms; letting go far enough crosses on a
// spring that carries the fingers' speed.

const (
	exPeek, exGap          = 96, 32 // the other room's edge, and the gutter between the rooms
	exShift                = exPeek + exGap
	exPad                  = 32 // the page's sides
	exTop, exFoot          = 46, 64
	exPart                 = 64 // a stream below its front page
	siteBatch              = 9  // design sites a batch
	coverBatch             = 12 // writers a batch
	leadHold               = 7 * time.Second
	pickTurn               = 1800 * time.Millisecond
	restBeforeRead         = 250 * time.Millisecond                         // a post the pointer rests on starts reading
	loaderWait, loaderStay = 400 * time.Millisecond, 600 * time.Millisecond // the loading plane
)

const (
	design  = 0
	writing = 1
)

// exWork is something a gallery featured: the work, its gallery, and which
// of the gallery's pictures it is.
type exWork struct {
	entry       *explore.Entry
	index       int
	title, url  string
	publishedAt *string
}

func (w exWork) key() string { return w.entry.ID + "#" + strconv.Itoa(w.index) }

func (w exWork) post() explore.Post {
	p := explore.Post{Title: w.title, URL: w.url, PublishedAt: w.publishedAt}
	if w.index < len(w.entry.Works) {
		p.Kinds = w.entry.Works[w.index].Kinds
	}
	return p
}

// exFresh is a writer's latest post, for the writing room's front page.
type exFresh struct {
	entry *explore.Entry
	post  explore.Post
}

// exPic is a picture Explore shows: on its way, there, or none to be had.
type exPic struct {
	done  bool
	bmp   *ui.Bitmap
	soft  *ui.Bitmap  // a blurred copy, for the words' band over it
	small image.Image // soft's pixels, which bands are baked from
	bands map[bandKey]*ui.Bitmap
	at    time.Time  // when it came, for its fade (set on the first frame that shows it)
	dots  []ui.Color // its three main colours (palette)
	// How bright it rests and comes up to, by how light it is (toneOf); a door's.
	rest, lit float32
	// held: a place that shows it wherever it is asked for it (workPic), so it
	// keeps its turn when a card of it is scrolled away (nearPic).
	held bool
}

// roomMove is the rooms on their spring, from where they were toward room
// to, at the fingers' speed.
type roomMove struct {
	from, to   int
	x0, x1     float32
	o0         [2]float32
	top0, top1 float32
	velocity   float32
	started    bool
}

// swipe is a two-finger sideways swipe.
type swipe struct {
	x       float32
	last    time.Time
	axis    byte
	done    bool
	moves   []swipeMove
	speed   float32 // the track's speed over the last 100 ms, px/s
	release time.Time
}

type swipeMove struct {
	at time.Time
	dx float32
}

// exploreState is what Explore keeps while the app runs: its cached list
// and kept place, and its page's state.
type exploreState struct {
	entries []explore.Entry
	loaded  bool // a list came at least once
	loading bool
	failed  string
	req     int
	askedAt time.Time
	shownAt time.Time // the loader, since it showed
	// The page's first arrival: the front pages come into focus, the far
	// room last.
	enter   bool
	enterAt time.Time
	run     uint64

	// What the page shows, from entries.
	sites, writers, index []*explore.Entry
	works, leads, frames  []exWork
	picks                 map[string][]exWork
	fresh                 []exFresh

	room   int
	scroll ui.ScrollState
	shown  [2]int
	endY   [2]float32 // where each stream's end was when its last batch came
	move   *roomMove
	pull   float32 // how far the fingers pull the rooms
	sw     swipe
	x      float32 // where the track is (the frame before)

	lead     int
	leadRun  uint64
	leadLeft time.Duration // the lead's work: how long it has held
	leadAt   time.Time
	turns    map[string]*pickTurns // a gallery's picks turning while pointed at

	pics     map[string]*exPic
	auras    map[auraKey]*ui.Bitmap
	px       float32              // the screen's pixels a DIP, as the last frame drew the rooms
	loads    chan struct{}        // work pictures come a few at a time
	away     sync.Map             // work pictures waiting their turn whose card was scrolled away (nearPic)
	busy     map[string]time.Time // the sources being subscribed or let go, and since when
	fresh2   map[string]bool      // a pill just pressed: no 取消订阅 hint until the pointer leaves
	live     string
	focus    string // "card:<id>", "who:<id>", "post:<url>", "door"
	opener   string // what opened the author's page last opened from here: the focus going back by the keys
	prefetch struct {
		url, entry string
		at         time.Time
		done       map[string]bool
	}
	// Where the faces on the pills were, the frame before, for the faces
	// flying back to them.
	faces map[string]ui.Rect

	people     map[string]*exPerson // the people not subscribed to, as their pages show them
	kinds      map[string][]exWork  // the galleries' works by what they are shelved under, newest first
	kindOf     map[string][]string  // and what each work is shelved under, by its address
	walls      map[string]*kindWall // the kinds' pages
	pills      map[string]*pillState
	hoverPost  string    // the post under the pointer
	leadHeld   bool      // the lead is pointed at, in focus or out at the edge
	leadWords  float32   // how tall the lead's words were, for their band
	leadSeen   bool      // the lead showed in the frame before
	leadPrev   int       // the work the one on show covers as it fades in
	leadPrevAt time.Time // since when

	ask     *roomAsk   // a crossing a swipe asked for, made in the next build
	jump    bool       // the rooms change at once in the next build
	jumping bool       // ... in this one
	still   bool       // less motion, as the last build saw it (for the swipe)
	peekHot bool       // the pointer is on the way across
	focusIn [2]bool    // the focus is inside a room
	roomEnd [2]float32 // where each room's content ends, in the page
}

func newExploreState() *exploreState {
	return &exploreState{shown: [2]int{siteBatch, coverBatch}, pics: map[string]*exPic{},
		fresh2: map[string]bool{}, turns: map[string]*pickTurns{}, faces: map[string]ui.Rect{}, loads: make(chan struct{}, 8),
		pills: map[string]*pillState{}}
}

// ExploreChanged takes Explore's background read in (onExploreChanged): an
// open page reads the list again without a reload. Call it on the window's
// thread.
func (a *App) ExploreChanged() {
	if a.ex.loaded || a.ex.loading {
		a.loadExplore()
	}
}

// loadExplore reads the list (loadExplore), its icons and small captures
// decoded off the window's thread.
func (a *App) loadExplore() {
	ex, svc := a.ex, a.opt.Explore
	if svc == nil {
		ex.failed = locales.ExploreUnavailable()
		return
	}
	ex.req++
	req := ex.req
	if !ex.loaded {
		ex.loading, ex.failed = true, ""
	}
	a.do(func() func() {
		list, err := svc.List(bg)
		pics := map[string]*exPic{}
		if err != nil {
			// What went wrong goes to the log; the page says it in words.
			log.Printf("explore: %v", err)
		} else {
			for _, e := range list {
				if e.Icon != nil {
					pics["icon:"+e.ID] = decodePic(*e.Icon, false)
				}
				if e.Thumb != nil {
					pics["thumb:"+e.ID] = decodePic(*e.Thumb, true)
				}
			}
		}
		return func() {
			if req != ex.req {
				return
			}
			ex.loading = false
			if err != nil {
				if !ex.loaded {
					ex.failed = locales.ExploreUnread()
				}
				return
			}
			first := !ex.loaded
			ex.loaded, ex.failed = true, ""
			ex.entries = list
			for k, p := range pics {
				ex.pics[k] = p
			}
			ex.derive()
			if first {
				ex.enter, ex.enterAt = true, time.Time{}
				ex.run++
			}
		}
	})
}

// exTime is Date.parse(value) || 0.
func exTime(v *string) float64 {
	if v == nil || *v == "" {
		return 0
	}
	if ms, ok := web.JSDateParse(*v); ok {
		return float64(ms)
	}
	return 0
}

// newest is what an entry sorts by: its newest post, or when a gallery
// featured it; -1 with neither.
func newest(e *explore.Entry) float64 {
	var at *string
	if len(e.Posts) > 0 && e.Posts[0].PublishedAt != nil && *e.Posts[0].PublishedAt != "" {
		at = e.Posts[0].PublishedAt
	} else if e.FeaturedAt.Value != nil && *e.FeaturedAt.Value != "" {
		at = e.FeaturedAt.Value
	}
	if at == nil {
		return -1
	}
	return exTime(at)
}

// derive works out what the page shows.
func (ex *exploreState) derive() {
	ex.sites, ex.writers, ex.index = nil, nil, nil
	for i := range ex.entries {
		e := &ex.entries[i]
		switch e.Kind {
		case "site":
			ex.sites = append(ex.sites, e)
		case "blog":
			ex.writers = append(ex.writers, e)
		}
	}
	byNewest := func(x, y *explore.Entry) int {
		switch nx, ny := newest(x), newest(y); {
		case nx > ny:
			return -1
		case nx < ny:
			return 1
		}
		return 0
	}
	slices.SortStableFunc(ex.sites, byNewest)
	ex.works = nil
	for _, e := range ex.sites {
		for i, w := range e.Works {
			ex.works = append(ex.works, exWork{entry: e, index: i, title: w.Title, url: w.URL, publishedAt: w.PublishedAt})
		}
	}
	slices.SortStableFunc(ex.works, func(x, y exWork) int { return cmpDesc(exTime(x.publishedAt), exTime(y.publishedAt)) })
	ex.kinds, ex.kindOf = map[string][]exWork{}, map[string][]string{}
	for _, w := range ex.works {
		for _, k := range w.entry.Works[w.index].Kinds {
			ex.kinds[k] = append(ex.kinds[k], w)
		}
		if ks := w.entry.Works[w.index].Kinds; len(ks) > 0 {
			ex.kindOf[w.url] = ks
		}
	}
	// The lead and the frames take works from as many galleries as possible, newest first.
	var queues [][]exWork
	for _, e := range ex.sites {
		var q []exWork
		for _, w := range ex.works {
			if w.entry == e {
				q = append(q, w)
			}
		}
		if len(q) > 0 {
			queues = append(queues, q)
		}
	}
	var spread []exWork
	for round := 0; ; round++ {
		var ready [][]exWork
		for _, q := range queues {
			if round < len(q) {
				ready = append(ready, q)
			}
		}
		if len(ready) == 0 {
			break
		}
		slices.SortStableFunc(ready, func(x, y []exWork) int {
			return cmpDesc(exTime(x[round].publishedAt), exTime(y[round].publishedAt))
		})
		for _, q := range ready {
			spread = append(spread, q[round])
		}
	}
	ex.leads, ex.frames = spread[:min(5, len(spread))], spread[min(5, len(spread)):min(13, len(spread))]
	onFront := map[string]bool{}
	for _, w := range append(slices.Clone(ex.leads), ex.frames...) {
		onFront[w.key()] = true
	}
	ex.picks = map[string][]exWork{}
	for _, e := range ex.sites {
		var unseen, seen []exWork
		for _, w := range ex.works {
			if w.entry != e {
				continue
			}
			if onFront[w.key()] {
				seen = append(seen, w)
			} else {
				unseen = append(unseen, w)
			}
		}
		own := append(unseen, seen...)
		ex.picks[e.ID] = own[:min(5, len(own))]
	}
	for _, e := range ex.writers {
		if e.Upstream == nil {
			ex.index = append(ex.index, e)
		}
	}
	slices.SortStableFunc(ex.index, byNewest)
	var fresh []exFresh
	for _, e := range ex.writers {
		if len(e.Posts) > 0 {
			fresh = append(fresh, exFresh{entry: e, post: e.Posts[0]})
		}
	}
	slices.SortStableFunc(fresh, func(x, y exFresh) int { return cmpDesc(exTime(x.post.PublishedAt), exTime(y.post.PublishedAt)) })
	// The three large slots go to the newest posts with an opening, the
	// longest leading so the top story fills its column; the briefs take the
	// rest, newest first.
	var told []exFresh
	for _, f := range fresh {
		if f.post.Excerpt != "" && len(told) < 3 {
			told = append(told, f)
		}
	}
	slices.SortStableFunc(told, func(x, y exFresh) int {
		return cmpDesc(float64(utf16Count(x.post.Excerpt)), float64(utf16Count(y.post.Excerpt)))
	})
	ex.fresh = slices.Clone(told)
	for _, f := range fresh {
		if !slices.ContainsFunc(told, func(t exFresh) bool { return t.entry == f.entry && t.post.URL == f.post.URL }) {
			ex.fresh = append(ex.fresh, f)
		}
	}
	ex.lead = min(ex.lead, max(0, len(ex.leads)-1))
}

func cmpDesc(x, y float64) int {
	switch {
	case x > y:
		return -1
	case x < y:
		return 1
	}
	return 0
}

// utf16Count is a string's length as JavaScript counts it.
func utf16Count(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

func (ex *exploreState) find(id string) *explore.Entry {
	for i := range ex.entries {
		if ex.entries[i].ID == id {
			return &ex.entries[i]
		}
	}
	return nil
}

// decodePic decodes a data: picture; soft also makes the blurred copy the
// words' band shows under them.
func decodePic(data string, soft bool) *exPic { return picOf(decodeData(data), soft) }

// topOf is the top of a picture taller than twice its width, as tall as
// that: a page captured whole (1200×12000) shows no more of itself in a box
// holding it by its top, none of which is taller than that, and costs a
// fifth to draw.
func topOf(img image.Image) image.Image {
	b := img.Bounds()
	sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if h := 2 * b.Dx(); ok && b.Dy() > h {
		return sub.SubImage(image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Min.Y+h))
	}
	return img
}

// picOf is img as Explore draws it (decodePic), or a picture of none.
func picOf(img image.Image, soft bool) *exPic {
	p := &exPic{done: true}
	if img == nil {
		return p
	}
	p.bmp = ui.NewBitmap(img)
	p.dots = mainColours(img)
	if soft {
		p.small = shrinkTo(img, 40)
		p.soft = ui.NewBitmap(p.small)
		p.rest, p.lit = toneOf(p.small)
	}
	return p
}

// band is the words' band baked for g (bakeBand), from the bands baked
// lately; nil for a picture with no soft copy, whose band is the scrim alone.
func (p *exPic) band(g bandKey) *ui.Bitmap {
	if p == nil || p.small == nil {
		return nil
	}
	if b := p.bands[g]; b != nil {
		return b
	}
	// ponytail: a picture keeps at most four sizes of band; a window resized
	// back and forth bakes them again, at a fraction of a millisecond each.
	if len(p.bands) >= 4 {
		clear(p.bands)
	}
	if p.bands == nil {
		p.bands = map[bandKey]*ui.Bitmap{}
	}
	b := ui.NewBitmap(bakeBand(p.small, g))
	p.bands[g] = b
	return b
}

// decodeData decodes a data: picture; nil for none.
func decodeData(data string) image.Image {
	_, b64, ok := strings.Cut(data, ";base64,")
	if !ok || !strings.HasPrefix(data, "data:image/") {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil
	}
	img := decodeAny(raw)
	if img == nil || img.Bounds().Empty() {
		return nil
	}
	return img
}

// picture is a picture Explore loads on first sight (a gallery's work, a
// site's capture, a writer's latest picture): load gives its data URL, ""
// for none. Works come eight at a time, most from sites of their own, as
// setting up each connection takes longer than the picture; one whose card
// was scrolled away before its turn (nearPic) gives its place to the next
// and is asked for again once the card comes near. top keeps only the top
// of a tall one (topOf), for a picture every box holds by its top.
func (a *App) picture(key string, queued, top bool, load func() string) *exPic {
	ex := a.ex
	if p := ex.pics[key]; p != nil || a.warming {
		return p
	}
	p := &exPic{}
	ex.pics[key] = p
	a.do(func() func() {
		if queued && !a.opt.Sync {
			ex.loads <- struct{}{}
			defer func() { <-ex.loads }()
			if _, gone := ex.away.LoadAndDelete(key); gone {
				return func() {
					if ex.pics[key] == p {
						delete(ex.pics, key)
					}
				}
			}
		}
		got := &exPic{done: true}
		if data := load(); data != "" {
			img := decodeData(data)
			if top && img != nil {
				img = topOf(img)
			}
			got = picOf(img, true)
		}
		return func() { *p = *got }
	})
	return p
}

// workKey is the key of a work's picture: its address, so that a gallery
// read again, its works in new places, never shows one work's picture on
// another.
func workKey(w exWork) string {
	if w.index < len(w.entry.Works) && w.entry.Works[w.index].Image != "" {
		return "work:" + w.entry.Works[w.index].Image
	}
	return "work:" + w.key()
}

// workPic is a work's picture for a place that shows it wherever it is (a
// door, a wall, the lead): held, it keeps its turn while a card of the same
// work is scrolled away.
func (a *App) workPic(w exWork) *exPic {
	p := a.loadWork(w)
	if p != nil {
		p.held = true
	}
	a.ex.away.Delete(workKey(w))
	return p
}

// loadWork asks for a work's picture, by its address (workKey).
func (a *App) loadWork(w exWork) *exPic {
	svc, src := a.opt.Explore, ""
	if w.index < len(w.entry.Works) {
		src = w.entry.Works[w.index].Image
	}
	// Every box holds a work by its top, but a wall holding its kind's by their middle (wallLooks).
	top := !slices.ContainsFunc(a.ex.kindOf[w.url], func(k string) bool { return wallLooks[k].py != 0 })
	return a.picture(workKey(w), true, top, func() string {
		if src == "" {
			return ""
		}
		return svc.Picture(bg, src)
	})
}

// nearPic is the picture of the work card b shows, asked for once the card
// is within 600 DIPs of the window; one scrolled away while its picture
// waits its turn lets the pictures on screen go first.
func (a *App) nearPic(b ui.Element, w exWork) *exPic {
	key := workKey(w)
	if b.InViewMargin(600) {
		a.ex.away.Delete(key)
		return a.loadWork(w)
	}
	p := a.ex.pics[key]
	if p != nil && !p.done && !p.held {
		a.ex.away.Store(key, true)
	}
	return p
}

// shotKey is the key of what stands behind an entry: a writer stands on
// their latest post, so a newer post brings its own picture.
func shotKey(e *explore.Entry) string {
	if e.Kind == "blog" && len(e.Posts) > 0 {
		return "shot:" + e.ID + "|" + e.Posts[0].URL
	}
	return "shot:" + e.ID
}

// fullShot is the site at full size (loadExploreShot): a design site's large
// capture, a writer's latest post's picture.
func (a *App) fullShot(e *explore.Entry) *exPic {
	svc, id := a.opt.Explore, e.ID
	return a.picture(shotKey(e), false, false, func() string {
		data, _ := svc.Shot(bg, id)
		return data
	})
}

// ground is a writer's latest picture sunk into a box w×h (bakeGround), baked
// off the window's thread for its size, in steps of 16 so that a window
// being resized does not bake one at every step; nil until it is.
func (a *App) ground(e *explore.Entry, kind groundKind, w, h float32) *exPic {
	ex, svc, id := a.ex, a.opt.Explore, e.ID
	w, h = float32(math.Ceil(float64(w)/16)*16), float32(math.Ceil(float64(h)/16)*16)
	key := "ground:" + strconv.Itoa(int(kind)) + ":" + shotKey(e) + ":" + strconv.Itoa(int(w)) + "x" + strconv.Itoa(int(h))
	if p := ex.pics[key]; p != nil || a.warming {
		return p
	}
	p := &exPic{}
	ex.pics[key] = p
	a.do(func() func() {
		got := &exPic{done: true}
		if data, _ := svc.Shot(bg, id); data != "" {
			if img := decodeData(data); img != nil {
				got.bmp = ui.NewBitmap(bakeGround(img, kind, w, h))
			}
		}
		return func() { *p = *got }
	})
	return p
}

// arrived reports how far p has faded in, over d from the frame it first
// showed: at once from the keys, and for a picture there before the page.
func (a *App) arrived(c *ui.Context, p *exPic, d time.Duration) float32 {
	if p == nil || p.bmp == nil {
		return 0
	}
	now := c.Now()
	if p.at.IsZero() {
		p.at = now
		if a.kbd {
			p.at = now.Add(-d)
		}
	}
	k := float32(now.Sub(p.at)) / float32(d)
	if k >= 1 {
		return 1
	}
	c.AnimationFrame()
	return motion.Ease(max(0, k))
}

// mainColours is three colours of a picture, so its palette reads before it is
// opened: the most common of a 24-square sampling.
func mainColours(img image.Image) []ui.Color {
	small := squash(img, 24)
	b := small.Bounds()
	count := map[int]int{}
	var order []int
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := small.At(x, y).RGBA()
			k := int(r>>13)<<6 | int(g>>13)<<3 | int(bl>>13)
			if count[k] == 0 {
				order = append(order, k)
			}
			count[k]++
		}
	}
	slices.SortStableFunc(order, func(x, y int) int { return count[y] - count[x] })
	var out []ui.Color
	for _, k := range order[:min(3, len(order))] {
		out = append(out, ui.RGB(uint8(k>>6&7)*32+16, uint8(k>>3&7)*32+16, uint8(k&7)*32+16))
	}
	return out
}

// squash is img drawn into an n square, as a canvas's drawImage(img, 0, 0, n, n) does.
func squash(img image.Image, n int) image.Image {
	out := image.NewNRGBA(image.Rect(0, 0, n, n))
	xdraw.ApproxBiLinear.Scale(out, out.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	return out
}

// The halo every writer and person glows, whoever they are.
var grey = [2]ui.Color{theme.HaloGrey, theme.HaloGreyDeep}

// glow is how bright a writer's name is: as recent as their latest post, in
// the sidebar's four steps.
func glow(e *explore.Entry) ui.Color {
	t := newest(e)
	days := math.Inf(1)
	if t > 0 {
		days = (float64(time.Now().UnixMilli()) - t) / 864e5
	}
	switch {
	case days <= 7:
		return theme.Mag1
	case days <= 30:
		return theme.Mag2
	case days <= 90:
		return theme.Mag3
	}
	return theme.Mag4
}

// isPress tells a publication, which shows a square mark among the writers.
func isPress(e *explore.Entry) bool { return strings.HasPrefix(e.Section, "设计写作") }

// --- The page.

// exGeometry is the page's measures at the content area's width: the
// page's content box (cw), a room (w), the front pages' height.
type exGeometry struct {
	cw, w, front float32
}

func (a *App) exGeometry(c *ui.Context) exGeometry {
	_, winH := c.Size()
	return a.geometryAt(winH)
}

// geometryAt is the page's measures in a window winH high.
func (a *App) geometryAt(winH float32) exGeometry {
	cw := a.area.W - 2*exPad
	return exGeometry{cw: cw, w: cw - exShift, front: max(600, min(winH-130, 920))}
}

// restX is where the track stands for a room.
func (g exGeometry) restX(room int) float32 {
	if room == design {
		return 0
	}
	return exShift - (g.w + exGap)
}

// explorePage builds Explore; live is false for the page left, built on for
// its exit.
func (a *App) explorePage(c *ui.Context, live bool) {
	ex := a.ex
	if !ex.loaded && !ex.loading && ex.failed == "" && a.opt.Explore != nil {
		ex.askedAt = c.Now()
		a.loadExplore()
	}
	if live {
		a.scrolling = nil
	}
	if !ex.loaded {
		a.exploreWaiting(c)
		return
	}
	g := a.exGeometry(c)
	if live && ex.enter {
		if ex.enterAt.IsZero() {
			ex.enterAt = c.Now()
		}
		if left := 1200*time.Millisecond - c.Now().Sub(ex.enterAt); left > 0 {
			c.After(left)
		} else {
			ex.enter = false
		}
	}
	root := ui.Box(c.Key("explore")).Fill().Label(locales.TabExplore())
	root.Children(func() {
		sc := ui.Scroll(c.Key("scroll")).TrackScroll(&ex.scroll).Fill().Scrollbars(false)
		sc.Children(func() {
			ui.Column(c.Key("issue")).FillWidth().Padding(exTop, exPad, exFoot, exPad).Children(func() {
				a.rooms(c, g, live)
			})
		})
		if live {
			a.scrolling = &ex.scroll
		}
	})

	if live {
		a.leadClock(c)
		a.prefetchPost(c)
	}
}

// exploreWaiting is the page while the list is on its way, or could not be
// read: the plane in flight after 400 ms, or landed, with 重试.
func (a *App) exploreWaiting(c *ui.Context) {
	ex := a.ex
	ui.Column(c.Key("issue-waiting")).Fill().Padding(exTop, exPad, exFoot, exPad).Children(func() {
		if ex.failed != "" {
			a.emptyStateAlert(c, "unreachable", locales.ExploreErrorTitle(), ex.failed, func() {
				if a.linkButton(c, "retry", locales.CommonTryAgain(), true, false).Clicked() {
					ex.failed = ""
					a.loadExplore()
				}
			})
			return
		}
		now := c.Now()
		if wait := loaderWait - now.Sub(ex.askedAt); wait > 0 {
			c.After(wait)
			return
		}
		if ex.shownAt.IsZero() {
			ex.shownAt = now
		}
		ui.Column(c.Key("loading")).AlignItems(ui.Center).MaxWidth(360).AlignSelf(ui.Center).Margin(a.area.H*0.2, 0, 0, 0).
			Role(ui.RoleStatus).Label(locales.ExploreLoading()).Children(func() { a.pictogram(c, "loading") })
	})
}

// emptyStateAlert is emptyState read out at once (role="alert").
func (a *App) emptyStateAlert(c *ui.Context, art, title, text string, action func()) {
	ui.Column(c.Key("alert")).Role(ui.RoleAlert).FillWidth().Children(func() { a.emptyState(c, art, title, text, action) })
}

// rooms builds the two rooms side by side on their track, the way across,
// and takes the swipes.
func (a *App) rooms(c *ui.Context, g exGeometry, live bool) {
	ex := a.ex
	now := c.Now()
	ex.still = a.still(c)
	if ask := ex.ask; ask != nil && live {
		ex.ask = nil
		a.enterRoom(c, ask.to, ask.kbd, ask.velocity)
	}
	// A change of room from the keys, or under less motion, shows at once in the build after it.
	ex.jumping, ex.jump = ex.jump, false
	view := ui.Box(c.Key("rooms")).Margin(0, -exPad).Padding(0, exPad).ClipX().ClipY()
	view.Draw(func(p *ui.Painter, _ ui.Rect) { ex.px = p.Scale() })
	if live {
		view.HandleInput(func(ev ui.InputEvent) bool { return a.exploreWheel(ev, g) })
	}
	// Let go: a long enough pull or a flick toward the other room crosses;
	// anything else settles back, from where it is.
	if r := ex.sw.release; !r.IsZero() && live {
		if now.Before(r) {
			c.After(r.Sub(now))
		} else {
			ex.sw.release = time.Time{}
			toward := ex.sw.x
			if ex.room == writing {
				toward = -toward
			}
			flick := abs32(ex.sw.speed) > 600 && toward > 24
			if toward > 72 || flick {
				a.enterRoom(c, 1-ex.room, false, ex.sw.speed)
			} else {
				a.glideRooms(c, ex.room, ex.room, 0)
			}
		}
	}
	pulling := ex.sw.release != (time.Time{}) && ex.move == nil
	moving := ex.move != nil
	both := pulling || moving
	view.Children(func() {
		track := ui.Row(c.Key("track")).AlignItems(ui.Start).Gap(exGap)
		x := g.restX(ex.room)
		var p float32 = 1
		switch m := ex.move; {
		case m != nil:
			spring := ui.Animation{Spring: ui.SpringOptions{Stiffness: 170, Damping: 28, Precision: 0.5}}
			if !m.started {
				m.started = true
				track.Jump("x", m.x0)
				spring.Spring.Velocity = m.velocity
			}
			raw := track.AnimateTo("x", m.x1, spring)
			// Never past the room it settles on: momentum may carry it there faster, not further.
			if m.x1 < m.x0 {
				x = max(m.x1, raw)
			} else {
				x = min(m.x1, raw)
			}
			if m.x1 != m.x0 {
				p = (x - m.x0) / (m.x1 - m.x0)
			}
			if m.top1 != m.top0 {
				ex.scroll.Y = m.top0 + (m.top1-m.top0)*p
			}
			if track.Settled("x") || x == m.x1 && abs32(track.Velocity("x")) < 5 {
				ex.move = nil
				track.Jump("x", m.x1)
				c.Invalidate()
			}
		case pulling:
			toward := ex.sw.x
			if ex.room == writing {
				toward = -toward
			}
			pull := toward
			if toward <= 0 {
				pull = max(-24, toward*0.25)
			}
			ex.pull = pull
			if ex.room == design {
				x -= pull
			} else {
				x += pull
			}
			x = track.Jump("x", x)
		default:
			x = track.Jump("x", x)
		}
		ex.x = x
		// Pointing at the other room's edge draws the page toward it.
		lean := float32(0)
		peekHot := !both && ex.peekHot
		if peekHot && !a.still(c) {
			lean = map[int]float32{design: -12, writing: 12}[ex.room]
		}
		lean = track.AnimateTo("lean", lean, ui.Animation{Duration: 200 * time.Millisecond, Ease: motion.Ease})
		// On whole pixels of the screen, so that the words keep the place
		// within a pixel their glyphs were drawn for: moving by fractions of a
		// pixel, every frame of a crossing drew glyphs anew, and the GPU waited
		// for each frame before it took them.
		track.Translate(ex.snap(x+lean), 0)
		track.Children(func() {
			for room := range 2 {
				a.room(c, g, room, live, both, p, peekHot)
			}
		})
		a.door(c, g)
	})
}

// room builds one room: its front page, then its stream.
func (a *App) room(c *ui.Context, g exGeometry, room int, live, both bool, p float32, peekHot bool) {
	ex := a.ex
	on := room == ex.room
	r := ui.Column(c.Key(room)).Width(g.w).Shrink(0).Role(ui.RoleRegion).Label(map[int]string{design: locales.ExploreDesign(), writing: locales.ExploreWriting()}[room])
	// The room away is cut back to its front page, a step dimmer.
	if !on && !both {
		r.MaxHeight(g.front).ClipY()
	}
	if !on {
		r.IgnoreInput(true).AccessHidden(!on)
	}
	target := float32(0.6)
	if on {
		target = 1
	} else if peekHot {
		target = 1
	}
	var o float32
	switch m := ex.move; {
	case m != nil:
		o = r.Jump("o", m.o0[room]+(map[bool]float32{true: 1, false: 0.6}[room == m.to]-m.o0[room])*p)
	case !on && ex.sw.release != (time.Time{}):
		o = r.Jump("o", 0.6+0.4*min(1, max(0, ex.pull)/(abs32(g.restX(writing))*0.3)))
	case a.kbd || ex.jumping || a.still(c) && !peekHot:
		o = r.Jump("o", target)
	default:
		d := 320 * time.Millisecond
		if peekHot {
			d = 200 * time.Millisecond
		}
		o = r.AnimateTo("o", target, ui.Animation{Duration: d, Ease: motion.Ease, NonMotion: true})
	}
	ex.focusIn[room] = r.FocusWithin()
	// On the way into Explore the far room comes last, from just past the
	// window's edge, once the front page has come into focus; under less
	// motion it only fades in.
	if !on && ex.enter {
		in := 460 * time.Millisecond
		if a.still(c) {
			in = 360 * time.Millisecond
		}
		k := r.Keyframes("edge", []ui.Keyframe{{Value: 0}, {At: 160 * time.Millisecond, Value: 0}, {At: in, Value: 1, Ease: motion.Ease}},
			ui.KeyframeOptions{Run: ex.run, NonMotion: true})
		o *= k
		if !a.still(c) {
			r.Translate(ex.snap(map[int]float32{design: -16, writing: 16}[room]*(1-k)), 0)
		}
	}
	// The room dims under the window's own colour rather than fading as one
	// layer: the same pixels over the window's plain ground, without drawing
	// the room again into a bitmap in every frame (dim).
	r.Children(func() {
		ui.Box(c.Key("front")).Children(func() {
			if room == design {
				a.designFront(c, g, on)
			} else {
				a.frontPaper(c, g, on)
			}
			dim(c, o)
		})
		// Below the front pages each room's stream reaches across the other's
		// edge; while the rooms move, the room away keeps it pushed out of the
		// way, and a crossing moves it in and out as it goes.
		part := ui.Column(c.Key("part")).Margin(exPart, 0, 0, 0)
		if room == design {
			part.Margin(exPart, -exShift, 0, 0)
		} else {
			part.Margin(exPart, 0, 0, -exShift)
		}
		reach := float32(2 * exShift)
		away := map[int]float32{design: -reach, writing: reach}[room]
		switch m := ex.move; {
		case m != nil && m.to != m.from && room == m.to:
			part.Translate(ex.snap(away*(1-p)), 0)
		case m != nil && m.to != m.from && room == m.from:
			part.Translate(ex.snap(away*p), 0)
		case both && !on:
			part.Translate(away, 0)
		}
		part.Children(func() {
			if room == design {
				a.kindShelf(c, g, live)
				a.siteStream(c, g, live && on)
			} else {
				a.coverStream(c, g, live && on)
			}
			dim(c, o)
		})
		if b := part.LayoutBounds(); b.H > 0 {
			ex.roomEnd[room] = b.Y + b.H - a.area.Y + ex.scroll.Y
		}
	})
}

// snap is v on the screen's pixels, as the last frame drew them (px).
func (ex *exploreState) snap(v float32) float32 {
	if ex.px <= 0 {
		return v
	}
	return float32(math.Round(float64(v*ex.px))) / ex.px
}

// dim covers its parent with the window's colour at 1-o: a room at o over the
// window's plain ground, as Opacity(o) shows it.
func dim(c *ui.Context, o float32) {
	if o < 1 {
		ui.Box(c.Key("dim")).Absolute().Fill().ZIndex(20).Background(theme.Window.Alpha(1 - o)).IgnoreInput(true).AccessHidden(true)
	}
}

// door is the way across: the whole visible edge of the other room, as tall
// as the front page.
func (a *App) door(c *ui.Context, g exGeometry) {
	ex := a.ex
	label := map[int]string{design: locales.ExploreGoToWriting(), writing: locales.ExploreBackToDesign()}[ex.room]
	b := ui.ButtonBase(c.Key("door")).Label(label).Absolute().Top(0).Width(exPeek + exPad).Height(g.front).FocusRing(false).ZIndex(2)
	if ex.room == design {
		b.Right(0)
	} else {
		b.Left(0)
	}
	if ex.focus == "door" {
		ex.focus = ""
		b.Focus()
	}
	ex.peekHot = b.Hovered()
	if b.Clicked() {
		a.enterRoom(c, 1-ex.room, a.kbd, 0)
	}
	if b.FocusVisible() {
		b.Children(func() {
			ring := ui.Box(c).Absolute().Top(0).Bottom(0).Radius(10).Border(2, theme.Focus).IgnoreInput(true)
			if ex.room == design {
				ring.Left(0).Right(exPad)
			} else {
				ring.Left(exPad).Right(0)
			}
		})
	}
}

// exploreWheel takes a swipe over the rooms: sideways it drags them with the
// fingers at any depth of the page; up and down it scrolls the page.
func (a *App) exploreWheel(ev ui.InputEvent, g exGeometry) bool {
	if ev.Kind != ui.InputScroll {
		return false
	}
	ex := a.ex
	s := &ex.sw
	now := ev.Time
	if now.Sub(s.last) > 160*time.Millisecond {
		s.x, s.axis, s.done, s.moves = 0, 0, false, s.moves[:0]
	}
	s.last = now
	if s.axis == 0 {
		s.axis = 'y'
		if abs32(ev.DX) > abs32(ev.DY) {
			s.axis = 'x'
		}
	}
	if s.axis != 'x' {
		return false
	}
	// While the rooms are still on their spring, the tail of the same swipe
	// (or a new one) does not take them again.
	if s.done || ex.move != nil {
		s.done = true
		return true
	}
	a.kbd = false
	s.x += ev.DX
	s.moves = append(s.moves, swipeMove{now, ev.DX})
	s.moves = slices.DeleteFunc(s.moves, func(m swipeMove) bool { return now.Sub(m.at) >= 100*time.Millisecond })
	span := float32(16 * time.Millisecond)
	if len(s.moves) > 0 {
		span = max(span, float32(now.Sub(s.moves[0].at)))
	}
	var sum float32
	for _, m := range s.moves {
		sum += m.dx
	}
	s.speed = -sum / span * float32(time.Second)
	toward := s.x
	if ex.room == writing {
		toward = -toward
	}
	if ex.still {
		if toward > 90 {
			s.done, s.release = true, time.Time{}
			ex.ask = &roomAsk{to: 1 - ex.room, kbd: true}
		}
		return true
	}
	if toward > abs32(g.restX(writing))*0.3 {
		s.done, s.release = true, time.Time{}
		ex.ask = &roomAsk{to: 1 - ex.room, velocity: s.speed}
		return true
	}
	s.release = now.Add(120 * time.Millisecond)
	return true
}

// roomAsk is a crossing the swipe asked for, made in the next build.
type roomAsk struct {
	to       int
	kbd      bool
	velocity float32
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// enterRoom goes to the other room: on the spring, or at once from the keys
// and under less motion.
func (a *App) enterRoom(c *ui.Context, next int, kbd bool, velocity float32) {
	ex := a.ex
	if next == ex.room {
		return
	}
	ex.enter = false
	ex.live = map[int]string{writing: locales.ExploreInWriting(), design: locales.ExploreInDesign()}[next]
	// Focus inside the room going away would be lost once it turns inert; it moves to the way back instead.
	if ex.focusIn[ex.room] {
		ex.focus = "door"
	}
	if kbd || a.still(c) {
		ex.move, ex.sw.release = nil, time.Time{}
		ex.room, ex.jump = next, true
		c.Invalidate()
		return
	}
	a.glideRooms(c, ex.room, next, velocity)
}

// glideRooms puts the rooms on their spring toward to, from wherever they
// are; the page keeps its depth, easing up only as far as the room arriving
// reaches.
func (a *App) glideRooms(c *ui.Context, from, to int, velocity float32) {
	ex := a.ex
	g := a.exGeometry(c)
	m := &roomMove{from: from, to: to, x0: ex.x, x1: g.restX(to), velocity: velocity, top0: ex.scroll.Y}
	for room := range 2 {
		m.o0[room] = map[bool]float32{true: 1, false: 0.6}[room == from]
	}
	m.top1 = m.top0
	if end := ex.roomEnd[to]; end > 0 {
		m.top1 = max(0, min(m.top0, end+exFoot-a.area.H))
	}
	ex.move, ex.sw.release = m, time.Time{}
	ex.room = to
	c.Invalidate()
}
