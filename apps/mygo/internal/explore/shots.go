package explore

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"trie/internal/fetch"
)

const (
	// shotFresh is FRESH: a capture is taken again after this long, so a site's redesign reaches Explore.
	shotFresh = 30 * 24 * time.Hour
	// atOnce is AT_ONCE: enough that a first visit fills in quickly, few enough to stay out of the way.
	atOnce = 3
)

var unsafeName = regexp.MustCompile(`[^\w.-]+`)

// ShotFile is where a site's captured first screen is kept, at a width the page draws it: 1280 for the stage, 400 for
// lists. Earlier versions name them so too, and read the same explore-shots folder.
func ShotFile(dir, site string, width int) string {
	return filepath.Join(dir, unsafeName.ReplaceAllString(site, "-")+"-"+strconv.Itoa(width)+".jpg")
}

// Shots is SiteShots: first screens of the sites Explore shows, taken in the background a few at a time, in hidden
// windows that share nothing with the app: a session of their own in memory (no cookies or storage kept), an untrusted
// page that calls no Go, no permissions, no pop-ups, no downloads, no sound, no requests to private addresses. What
// the page asks for now goes first; captures taken ahead of a visit wait behind it.
type Shots struct {
	dir string
	// take captures site's first screen from url and keeps it; the hidden window (window) unless a test sets another.
	take    func(site, url string) bool
	mu      sync.Mutex
	waiting []*shotJob
	running int
	pending map[string]*shotJob
	// stop is closed by Stop: captures under way give up, and none starts.
	stop    chan struct{}
	stopped bool
}

type shotJob struct {
	site  string
	urls  []string
	first bool
	done  chan struct{}
	taken bool
}

// NewShots keeps first screens in dir (explore-shots).
func NewShots(dir string) *Shots {
	s := &Shots{dir: dir, pending: map[string]*shotJob{}, stop: make(chan struct{})}
	s.take = s.window
	return s
}

// Stale tells whether a site has no capture, or one older than shotFresh.
func (s *Shots) Stale(site string) bool {
	info, err := os.Stat(ShotFile(s.dir, site, 1280))
	return err != nil || now().Sub(info.ModTime()) > shotFresh
}

// Capture takes a site's first screen from the first of urls that shows it (its home page, then a post when the home
// page is missing). first puts it ahead of everything waiting (a card just came into view); otherwise it waits its
// turn. wait tells, once the capture is over, whether it is on disk; false too when ctx ends first.
func (s *Shots) Capture(site string, urls []string, first bool) (wait func(ctx context.Context) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return func(context.Context) bool { return false }
	}
	j := s.pending[site]
	if j == nil {
		j = &shotJob{site: site, urls: urls, first: first, done: make(chan struct{})}
		if first {
			s.waiting = slices.Insert(s.waiting, 0, j)
		} else {
			s.waiting = append(s.waiting, j)
		}
		s.pending[site] = j
	} else if i := slices.Index(s.waiting, j); first && i >= 0 {
		s.waiting = slices.Insert(slices.Delete(s.waiting, i, i+1), 0, j)
		j.first = true
	}
	// Also for a capture moved up: the place kept free is its to take.
	s.next()
	return func(ctx context.Context) bool {
		select {
		case <-j.done:
			return j.taken
		case <-ctx.Done():
			return false
		}
	}
}

// Stop gives the captures up, for the app's window closing: those waiting are not taken, those under way close their
// windows at once, so the app does not wait for them to quit; none is taken after.
func (s *Shots) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	close(s.stop)
	for _, j := range s.waiting {
		delete(s.pending, j.site)
		close(j.done)
	}
	s.waiting = nil
}

// next starts what may start. Captures taken ahead of a visit leave one place free, so a card that comes into view
// never waits behind them. s.mu is held.
func (s *Shots) next() {
	for !s.stopped && s.running < atOnce && len(s.waiting) > 0 && (s.waiting[0].first || s.running < atOnce-1) {
		j := s.waiting[0]
		s.waiting = s.waiting[1:]
		s.running++
		go func() {
			taken := false
			for _, url := range j.urls {
				if taken = s.try(j.site, url); taken || s.ended() {
					break
				}
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			s.running--
			delete(s.pending, j.site)
			j.taken = taken
			close(j.done)
			s.next()
		}()
	}
}

// ended tells whether Stop was called.
func (s *Shots) ended() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

// try is take, failing on a panic rather than ending the app: the capture is not taken.
func (s *Shots) try(site, url string) (taken bool) {
	defer func() {
		if err := recover(); err != nil {
			log.Printf("explore: capturing %s: %v", url, err)
			taken = false
		}
	}()
	return s.take(site, url)
}

var (
	// A bot check standing in front of the site is not the site.
	botCheck = regexp.MustCompile(ci("just a moment") + `|` + ci("attention required") + `|` + ci("access denied") + `|` + ci("verify you are human") + `|` + ci("captcha"))
	// MyGo does not tell a page's HTTP status, which must be below 400; an error page tells it in its title
	// instead: "404 Not Found", "example.com | 522: Connection timed out", "404: NOT_FOUND".
	errorPage = regexp.MustCompile(`(?:^|\| *)[45][0-9][0-9]\b|` + ci("not found") + `|` + ci("forbidden") + `|` + ci("bad gateway") + `|` + ci("service unavailable") + `|` + ci("temporarily unavailable"))
)

// window takes site's first screen in a hidden window: a page that never finishes loading is taken as far as it got
// in ten seconds, one that fails to load at all is not taken.
func (s *Shots) window(site, homeURL string) bool {
	u, err := fetch.PublicURL(homeURL)
	if err != nil {
		return false
	}
	rules, err := privateRules()
	if err != nil {
		log.Printf("explore: no captures: %v", err)
		return false
	}
	win := openShot(rules, finest(mygo.Screen.Displays()))
	defer closeShot(win)
	page := win.Page()
	var mu sync.Mutex
	title := ""
	page.OnPageTitleUpdated(func(e *mygo.TitleEvent) {
		mu.Lock()
		title = e.Title
		mu.Unlock()
	})
	loaded := make(chan bool, 1)
	page.OnDidFinishLoad(func() {
		select {
		case loaded <- true:
		default:
		}
	})
	// A navigation the page replaced (a redirect by script) is no failure; MyGo does not report it.
	page.OnDidFailLoad(func(*mygo.LoadError) {
		select {
		case loaded <- false:
		default:
		}
	})
	if page.LoadURL(u.Href()) != nil {
		return false
	}
	select {
	case ok := <-loaded:
		if !ok {
			return false
		}
	case <-time.After(10 * time.Second):
	case <-s.stop:
		return false
	}
	// Fonts, pictures and entrance animations settle.
	select {
	case <-time.After(800 * time.Millisecond):
	case <-s.stop:
		return false
	}
	mu.Lock()
	t := title
	mu.Unlock()
	if win.IsDestroyed() || botCheck.MatchString(t) || errorPage.MatchString(t) {
		return false
	}
	img := s.firstScreen(win)
	return img != nil && s.keep(site, img) == nil
}

const (
	// plainTries and plainWait: a first screen of one colour is taken again this many times, this long apart.
	plainTries = 4
	plainWait  = 1500 * time.Millisecond
)

// firstScreen is the page's first screen as win shows it. One of one colour is a page still coming in, as a loader
// or an intro that slowly reveals it (unanim.studio): it is taken again a moment later, a few times, and nil if it
// stays so, as a capture that shows nothing of the site is no capture.
func (s *Shots) firstScreen(win *mygo.Window) image.Image {
	for try := 0; ; try++ {
		shot, err := win.CapturePage()
		if err != nil {
			return nil
		}
		img, err := png.Decode(bytes.NewReader(shot))
		if err != nil || img.Bounds().Empty() {
			return nil
		}
		if !oneColour(img) {
			return img
		}
		if try == plainTries {
			return nil
		}
		select {
		case <-time.After(plainWait):
		case <-s.stop:
			return nil
		}
	}
}

// oneColour tells whether img is of one colour, as it shows 400 wide: a line of small type still tells it from one.
func oneColour(img image.Image) bool {
	small := shrink(img, 400)
	b := small.Bounds()
	r0, g0, b0, _ := small.At(b.Min.X, b.Min.Y).RGBA()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := small.At(x, y).RGBA()
			if far(r, r0) || far(g, g0) || far(bl, b0) {
				return false
			}
		}
	}
	return true
}

// far tells two channels apart by more than 8 of 255.
func far(a, b uint32) bool { return a>>8 > b>>8+8 || b>>8 > a>>8+8 }

// keep writes a first screen at the widths the page draws it.
func (s *Shots) keep(site string, img image.Image) error {
	for _, size := range [][2]int{{1280, 80}, {400, 70}} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, shrink(img, size[0]), &jpeg.Options{Quality: size[1]}); err != nil {
			return err
		}
		if err := writeFile(ShotFile(s.dir, site, size[0]), buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

// place is where a capture's window goes: on the display that draws finest, at the zoom that captures the page's
// 1440 × 900 CSS pixels 2880 × 1800, as a Retina screen draws them, or as many as fit: the window
// stays within the display's work area, as AppKit keeps a window that answers as shown (seen), cutting it otherwise.
// WebKit captures a window at its size times its display's scale, and a window never shown is on the display it
// overlaps most.
type place struct {
	x, y int
	zoom float64
}

func finest(displays []mygo.Display) place {
	var best mygo.Display
	for _, d := range displays {
		if d.ScaleFactor > best.ScaleFactor {
			best = d
		}
	}
	best.ScaleFactor = max(best.ScaleFactor, 1)
	area := best.WorkArea
	if area.Width <= 0 || area.Height <= 0 {
		area = best.Bounds
	}
	zoom := max(1, 2/best.ScaleFactor)
	if area.Width > 2 && area.Height > 2 {
		zoom = min(zoom, float64(area.Width-2)/1440, float64(area.Height-2)/900)
	}
	// Not at 0, 0, which centres the window on the screen with the keyboard focus.
	return place{area.X + 1, area.Y + 1, zoom}
}

// shotOptions are a capture's window: never shown, so it neither takes the focus nor shows in the Dock, Mission
// Control or the Window menu; a page someone else's, in a session of its own that keeps nothing, kept running while
// hidden, its dialogs silenced and its requests to private addresses blocked.
func shotOptions(rules *mygo.ContentRules, at place) mygo.WindowOptions {
	return mygo.WindowOptions{
		// Without a title bar, whose height WebKit takes off the page once the window counts as shown (seen).
		Hidden: true, Frameless: true, UseContentSize: true, X: at.x, Y: at.y, Width: int(math.Ceil(1440 * at.zoom)), Height: int(math.Ceil(900 * at.zoom)), SkipTaskbar: true,
		Page: mygo.PageOptions{
			Untrusted: true, Session: mygo.NewEphemeralSession(), BackgroundThrottling: mygo.ThrottlingOff,
			ZoomFactor: at.zoom, DevTools: mygo.DevToolsDisabled, ContentRules: []*mygo.ContentRules{rules},
			PreloadScript: quiet, PreloadAllFrames: true,
		},
	}
}

// openShot is a capture's window, its page guarded before it loads anything, and drawn as on screen (seen).
func openShot(rules *mygo.ContentRules, at place) *mygo.Window {
	win := mygo.NewWindow(shotOptions(rules, at))
	p := win.Page()
	p.SetAudioMuted(true)
	p.SetPermissionHandler(deny)
	p.SetWindowOpenHandler(noWindow)
	p.OnWillDownload(noDownload)
	p.OnWillNavigate(webOnly)
	p.SetAuthHandler(noSignIn)
	seen(win)
	return win
}

// closeShot closes a capture's window, its own class given back first.
func closeShot(win *mygo.Window) {
	unseen(win)
	win.Destroy()
}

func deny(mygo.PermissionRequest) bool                    { return false }
func noWindow(mygo.WindowOpenRequest) *mygo.WindowOptions { return nil }
func noDownload(e *mygo.DownloadEvent)                    { e.PreventDefault() }
func noSignIn(mygo.AuthRequest) mygo.AuthResponse         { return mygo.AuthResponse{} }

// webOnly keeps the page on web pages: it may go to another site, as a redirect does, but not to another scheme.
func webOnly(e *mygo.NavigateEvent) {
	if !strings.HasPrefix(e.URL, "http:") && !strings.HasPrefix(e.URL, "https:") {
		e.PreventDefault()
	}
}

// quiet keeps the page's alert, confirm, prompt and print from showing anything, in every frame: MyGo shows a hidden
// window's dialogs as app-modal panels, which bring the app to the front. And it lets the page run as it would on
// screen: WebKit gives a hidden window's page no
// rendering updates and tells it it is hidden, so a first screen that animates in would be taken blank. Animation
// frames come from a timer, and the page is told it is visible. Its animation timeline stands still too, which
// leaves every CSS animation and transition at its first frame (rauno.me, catk.de, vendredi-society.fr: their
// words fade in from nothing): the clock moves them on, by as much as the timeline did not.
const quiet = `(() => {
  for (const [name, value] of [['alert', () => {}], ['confirm', () => false], ['prompt', () => null], ['print', () => {}]])
    Object.defineProperty(window, name, { value, writable: false, configurable: false })
  window.requestAnimationFrame = callback => setTimeout(() => callback(performance.now()), 16)
  window.cancelAnimationFrame = id => clearTimeout(id)
  Object.defineProperty(Document.prototype, 'visibilityState', { get: () => 'visible' })
  Object.defineProperty(Document.prototype, 'hidden', { get: () => false })
  let wall = performance.now(), line = null
  setInterval(() => {
    if (!document.getAnimations) return
    const now = performance.now(), at = document.timeline && document.timeline.currentTime
    const step = now - wall - (at != null && line != null ? at - line : 0)
    wall = now, line = at
    if (step > 0) for (const a of document.getAnimations())
      if (a.playState === 'running' && a.currentTime != null) a.currentTime += step
  }, 32)
})()`

// privatePatterns are what a captured page may not load (url-filter, WebKit's syntax): an address with credentials or
// a port, one naming localhost or a .local machine, a name of one label, an IP address. That is publicURL's refusal,
// asked of every request.
// ponytail: every IP address is refused, public ones too, which publicURL lets through; a public name that resolves to
// a private address is let through. A request hook would tell both apart; MyGo has none.
var privatePatterns = []string{
	`^[^:]+://[^/?#]*@`,
	`^[^:]+://[^/?#]*:[0-9]`,
	`^[^:]+://\[`,
	`^[^:]+://[0-9.]+[/?#:]`,
	`^[^:]+://([^/?#]*\.)?localhost\.?[/?#:]`,
	`^[^:]+://[^/?#]*\.local\.?[/?#:]`,
	`^[^:]+://[^/?#.:]+\.?[/?#:]`,
}

var privateRules = sync.OnceValues(func() (*mygo.ContentRules, error) {
	var rules []map[string]map[string]string
	for _, p := range privatePatterns {
		rules = append(rules, map[string]map[string]string{"trigger": {"url-filter": p}, "action": {"type": "block"}})
	}
	data, err := json.Marshal(rules)
	if err != nil {
		return nil, err
	}
	return mygo.CompileContentRules("trie-explore-shots", string(data))
})
