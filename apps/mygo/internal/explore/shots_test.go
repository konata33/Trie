package explore

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"trie/internal/fetch"
)

// site-shots.ts has no tests of its own; these hold its queue rules, its file names and sizes, and the isolation of
// the hidden windows (live_test.go sees the same in real windows).

// gated is a Shots whose captures wait for the test to release them, site by site.
type gated struct {
	*Shots
	mu            sync.Mutex
	gates         map[string]chan bool
	urls          []string
	running, most int
	started       chan string
}

func newGated(t *testing.T) *gated {
	g := &gated{Shots: &Shots{dir: t.TempDir(), pending: map[string]*shotJob{}}, gates: map[string]chan bool{}, started: make(chan string, 64)}
	g.take = func(site, url string) bool {
		gate := g.gate(site)
		g.mu.Lock()
		g.urls = append(g.urls, url)
		g.running++
		g.most = max(g.most, g.running)
		g.mu.Unlock()
		g.started <- site
		taken := <-gate
		g.mu.Lock()
		g.running--
		g.mu.Unlock()
		return taken
	}
	return g
}

func (g *gated) gate(site string) chan bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.gates[site] == nil {
		g.gates[site] = make(chan bool, 4)
	}
	return g.gates[site]
}

// expect waits for the captures of sites to start (together, in any order), and for the queue to settle as running
// and waiting.
func (g *gated) expect(t *testing.T, sites []string, running int, waiting ...string) {
	t.Helper()
	var started []string
	for range sites {
		select {
		case site := <-g.started:
			started = append(started, site)
		case <-time.After(2 * time.Second):
			t.Fatalf("started %v, want %v", started, sites)
		}
	}
	slices.Sort(started)
	if want := slices.Sorted(slices.Values(sites)); !slices.Equal(started, want) {
		t.Fatalf("started %v, want %v", started, want)
	}
	g.Shots.mu.Lock()
	var queue []string
	for _, j := range g.waiting {
		queue = append(queue, j.site)
	}
	r := g.Shots.running
	g.Shots.mu.Unlock()
	if r != running || !slices.Equal(queue, waiting) {
		t.Fatalf("running %d, waiting %v; want %d, %v", r, queue, running, waiting)
	}
	select {
	case site := <-g.started:
		t.Fatalf("%s started too", site)
	default:
	}
}

func waited(t *testing.T, wait func(context.Context) bool) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return wait(ctx)
}

// Stop, as the app's window closes: what waits is given up at once, nothing starts after, and a capture asked for
// later is not taken; one under way ends as it can (the hidden window's waits end on stop).
func TestShotStop(t *testing.T) {
	g := newGated(t)
	g.stop = make(chan struct{})
	wa := g.Capture("a", []string{"a1"}, false)
	g.Capture("b", []string{"b1"}, false)
	wc := g.Capture("c", []string{"c1"}, false)
	g.expect(t, []string{"a", "b"}, 2, "c")
	g.Stop()
	g.Stop() // twice is once
	if waited(t, wc) {
		t.Fatal("the capture waiting is given up")
	}
	if waited(t, g.Capture("d", []string{"d1"}, true)) {
		t.Fatal("a capture asked for after Stop is not taken")
	}
	if !g.ended() {
		t.Fatal("the captures under way see the stop")
	}
	g.gate("a") <- true
	g.gate("b") <- false
	if !waited(t, wa) {
		t.Fatal("one that ended taken is taken")
	}
	select {
	case site := <-g.started:
		t.Fatalf("%s started after Stop", site)
	case <-time.After(100 * time.Millisecond):
	}
}

// At most three at once; what the page asks for now goes first, and captures taken ahead of a visit leave one place
// free; a capture asked for again is the same capture, moved up when it is now wanted; a site's urls are tried in turn.
func TestShotQueue(t *testing.T) {
	g := newGated(t)
	wa := g.Capture("a", []string{"a1"}, false)
	g.Capture("b", []string{"b1"}, false)
	wc := g.Capture("c", []string{"c1"}, false)
	g.expect(t, []string{"a", "b"}, 2, "c")
	wd := g.Capture("d", []string{"d1"}, true)
	g.expect(t, []string{"d"}, 3, "c")
	we := g.Capture("e", []string{"e1"}, true)
	g.expect(t, nil, 3, "e", "c")
	wc2 := g.Capture("c", []string{"c1"}, true)
	g.expect(t, nil, 3, "c", "e")
	g.gate("a") <- true
	g.expect(t, []string{"c"}, 3, "e")
	if !waited(t, wa) {
		t.Fatal("a is taken")
	}
	g.gate("b") <- false
	g.expect(t, []string{"e"}, 3)
	wd2 := g.Capture("d", []string{"d1"}, true)
	g.gate("d") <- true
	g.gate("c") <- true
	g.gate("e") <- false
	if !waited(t, wd) || !waited(t, wd2) || !waited(t, wc) || !waited(t, wc2) || waited(t, we) {
		t.Fatal("results")
	}
	// Once over, a capture is forgotten: asked again, it is taken again.
	wa = g.Capture("a", []string{"a1"}, false)
	g.expect(t, []string{"a"}, 1)
	g.gate("a") <- true
	waited(t, wa)
	// A site's addresses are tried until one is taken.
	wf := g.Capture("f", []string{"f1", "f2", "f3"}, true)
	g.expect(t, []string{"f"}, 1)
	g.gate("f") <- false
	g.expect(t, []string{"f"}, 1)
	g.gate("f") <- true
	if !waited(t, wf) {
		t.Fatal("f is taken from its second address")
	}
	g.mu.Lock()
	urls, most := slices.Clone(g.urls), g.most
	g.mu.Unlock()
	if !slices.Equal(urls[len(urls)-2:], []string{"f1", "f2"}) || most > atOnce {
		t.Fatalf("urls %v, at most %d at once", urls, most)
	}
	// A capture moved up takes the place kept free at once (site-shots.ts waited for a running one to end).
	g.Capture("x", []string{"x1"}, false)
	g.Capture("y", []string{"y1"}, false)
	g.Capture("z", []string{"z1"}, false)
	g.expect(t, []string{"x", "y"}, 2, "z")
	g.Capture("z", []string{"z1"}, true)
	g.expect(t, []string{"z"}, 3)
	for _, site := range []string{"x", "y", "z"} {
		g.gate(site) <- true
	}
	// A wait given up is false.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if g.Capture("w", []string{"w1"}, true)(ctx) {
		t.Fatal("a wait given up")
	}
	g.gate("w") <- true
}

// A capture is taken again after 30 days; the files are named as the Electron app names them.
func TestShotStale(t *testing.T) {
	dir := t.TempDir()
	s := NewShots(dir)
	if !s.Stale("site.example") {
		t.Fatal("no capture is stale")
	}
	file := ShotFile(dir, "site.example", 1280)
	if file != filepath.Join(dir, "site.example-1280.jpg") || ShotFile(dir, "a b:c", 400) != filepath.Join(dir, "a-b-c-400.jpg") {
		t.Fatal(file)
	}
	write(t, file, []byte{0xff, 0xd8, 0xff})
	if s.Stale("site.example") {
		t.Fatal("a fresh capture is not stale")
	}
	old := time.Now().Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	if !s.Stale("site.example") {
		t.Fatal("a capture of 31 days ago is stale")
	}
}

// A first screen is kept 1280 and 400 wide, as JPEGs of quality 80 and 70.
func TestShotKeep(t *testing.T) {
	s := NewShots(t.TempDir())
	if err := s.keep("site.example", image.NewRGBA(image.Rect(0, 0, 2880, 1800))); err != nil {
		t.Fatal(err)
	}
	for width, height := range map[int]int{1280: 800, 400: 250} {
		data, err := os.ReadFile(ShotFile(s.dir, "site.example", width))
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != width || cfg.Height != height {
			t.Fatalf("%d: %+v %v", width, cfg, err)
		}
	}
}

// The isolation site-shots.ts asks of its windows, as the window's options and handlers say it:
//   - a session of its own, in memory, for each capture: no cookies or storage kept;
//   - an untrusted page: it calls no Go method (and trusts no origin);
//   - no permission, no window, no download, no sound, no sign-in prompt, no dialog; no scheme but the web's;
//   - never shown: no focus taken, nothing in the Dock or a window list.
func TestShotWindowIsolated(t *testing.T) {
	o := shotOptions(nil, place{1, 1, 1})
	p := o.Page
	if !o.Hidden || o.Panel || o.AlwaysOnTop || !o.SkipTaskbar {
		t.Fatalf("the window shows: %+v", o)
	}
	if !p.Untrusted || len(p.TrustedOrigins) != 0 || p.DevTools != mygo.DevToolsDisabled {
		t.Fatalf("the page is trusted: %+v", p)
	}
	if p.Session == nil || !p.Session.IsEphemeral() || shotOptions(nil, place{1, 1, 1}).Page.Session == p.Session {
		t.Fatal("captures share a session, or keep one on disk")
	}
	if p.BackgroundThrottling != mygo.ThrottlingOff || p.Autoplay == mygo.AutoplayAllow {
		t.Fatal("throttling or autoplay")
	}
	if !o.UseContentSize || o.Width != 1440 || o.Height != 900 || p.ZoomFactor != 1 || o.X != 1 || o.Y != 1 {
		t.Fatalf("1440 × 900 CSS pixels: %+v", o)
	}
	// On the display that draws finest, within its work area; at twice the size where none draws finer than its
	// pixels, and smaller where the work area is: 1440 × 900 CSS pixels all the same.
	retina := mygo.Display{ScaleFactor: 2, Bounds: mygo.Rectangle{X: 1920, Width: 1800, Height: 1169}, WorkArea: mygo.Rectangle{X: 1920, Y: 37, Width: 1800, Height: 1132}}
	plain := mygo.Display{ScaleFactor: 1, Bounds: mygo.Rectangle{Width: 2560, Height: 1440}, WorkArea: mygo.Rectangle{Y: 25, Width: 2560, Height: 1415}}
	if at := finest([]mygo.Display{plain, retina}); at != (place{1921, 38, 1}) {
		t.Fatalf("on the Retina display: %+v", at)
	}
	if at := finest([]mygo.Display{plain}); at.x != 1 || at.y != 26 || at.zoom != float64(1415-2)/900 || shotOptions(nil, at).Page.ZoomFactor != at.zoom {
		t.Fatalf("on plain displays: %+v", at)
	}
	small := mygo.Display{ScaleFactor: 2, Bounds: mygo.Rectangle{Width: 1440, Height: 900}, WorkArea: mygo.Rectangle{Y: 25, Width: 1440, Height: 875}}
	if at := finest([]mygo.Display{small}); at.zoom != float64(875-2)/900 || shotOptions(nil, at).Height > 874 || shotOptions(nil, at).Width > 1439 {
		t.Fatalf("on a display smaller than the page: %+v", at)
	}
	for _, name := range []string{"'alert'", "'confirm'", "'prompt'", "'print'", "requestAnimationFrame", "'visibilityState'"} {
		if !p.PreloadAllFrames || !strings.Contains(p.PreloadScript, name) {
			t.Fatalf("%s is not set in every frame", name)
		}
	}
	if deny(mygo.PermissionRequest{Permissions: []mygo.Permission{mygo.PermissionCamera, mygo.PermissionMicrophone, mygo.PermissionGeolocation, mygo.PermissionNotifications}, Origin: "https://site.example"}) {
		t.Fatal("a permission is granted")
	}
	if noWindow(mygo.WindowOpenRequest{URL: "https://site.example/", UserInitiated: true}) != nil || noWindow(mygo.WindowOpenRequest{URL: "https://ad.example/"}) != nil {
		t.Fatal("a window opens")
	}
	if e := (&mygo.DownloadEvent{URL: "https://site.example/a.zip"}); func() bool { noDownload(e); return !e.DefaultPrevented() }() {
		t.Fatal("a download goes ahead")
	}
	if noSignIn(mygo.AuthRequest{Host: "site.example", Scheme: "basic"}) != (mygo.AuthResponse{}) {
		t.Fatal("a sign-in is answered")
	}
	for url, allowed := range map[string]bool{"https://www.site.example/": true, "http://other.example/a": true, "mailto:a@site.example": false, "trie://x": false,
		"file:///etc/passwd": false, "javascript:alert(1)": false, "data:text/html,x": false} {
		e := &mygo.NavigateEvent{URL: url}
		if webOnly(e); e.DefaultPrevented() == allowed {
			t.Errorf("navigation to %s: prevented %v", url, e.DefaultPrevented())
		}
	}
	// The window is guarded before its page loads anything, and is never shown, focused or brought forward.
	src, err := os.ReadFile("shots.go")
	if err != nil {
		t.Fatal(err)
	}
	if call := regexp.MustCompile(`\.(Show|ShowInactive|Focus|Restore|Maximize|SetAlwaysOnTop)\(|NewWindow\(mygo\.WindowOptions\{[^}]*URL:`).Find(src); call != nil {
		t.Fatalf("shots.go calls %s", call)
	}
}

// The requests a captured page may not make are publicURL's refusals (and every IP address), as WebKit matches them.
func TestPrivateAddresses(t *testing.T) {
	var rules []*regexp.Regexp
	for _, p := range privatePatterns {
		// WebKit's url-filter is case-insensitive unless told otherwise.
		rules = append(rules, regexp.MustCompile("(?i)"+p))
	}
	blocked := func(url string) bool {
		return slices.ContainsFunc(rules, func(re *regexp.Regexp) bool { return re.MatchString(url) })
	}
	for _, url := range []string{"https://example.com/", "https://www.example.com/a?b=c#d", "http://sub.example.co.uk/x:y", "https://xn--fsq.example/", "https://localhost.example.com/",
		"https://example.local.com/", "data:image/png;base64,AAAA", "blob:https://example.com/1", "about:blank"} {
		if blocked(url) {
			t.Errorf("%s is blocked", url)
		}
	}
	for _, url := range []string{"http://localhost/", "http://localhost:5177/", "http://LOCALHOST./x", "http://app.localhost/", "https://printer.local/", "http://intranet/", "http://example./",
		"http://127.0.0.1/", "http://10.0.0.1/", "http://192.168.1.1/x", "http://172.16.0.1/", "http://100.64.0.1/", "http://169.254.169.254/latest", "http://0.0.0.0/", "http://[::1]/",
		"http://[fd00::1]:8080/", "https://user:pass@example.com/", "https://user@example.com/", "https://example.com:8443/", "ws://localhost/", "wss://127.0.0.1/", "http://8.8.8.8/"} {
		if !blocked(url) {
			t.Errorf("%s is let through", url)
		}
		if _, err := fetch.PublicURL(url); err == nil && url != "http://8.8.8.8/" {
			t.Errorf("publicURL takes %s", url)
		}
	}
}

// A first screen of one colour (a loader, a page not drawn yet) tells from one with anything on it, a line of small
// type too.
func TestOneColour(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2880, 1800))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{14, 14, 14, 255}), image.Point{}, draw.Src)
	if !oneColour(img) {
		t.Fatal("a plain screen has something on it")
	}
	// A line of 12px type, two pixels a CSS pixel.
	for x := 1200; x < 1500; x += 6 {
		draw.Draw(img, image.Rect(x, 880, x+3, 904), image.NewUniform(color.RGBA{220, 220, 220, 255}), image.Point{}, draw.Src)
	}
	if oneColour(img) {
		t.Fatal("a line of type is not seen")
	}
}
