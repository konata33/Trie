package explore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"image/png"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
)

// The capture windows as they are, in MyGo's event loop (EXPLORE_LIVE=1). TestLiveIsolation needs no network: its page
// is loaded from a string, and a server on this machine counts what reaches it. TestLiveSites captures the first screens
// of EXPLORE_LIVE_SITES (comma-separated home pages, one after another) into EXPLORE_LIVE_OUT:
//
//	EXPLORE_LIVE=1 go test -count=1 -run 'TestLive' ./internal/explore
//
// The test app is an accessory: no Dock icon, and it never comes to the front, so it leaves the user's screen alone.

var probeHits atomic.Int32

// ShotProbe is a bound service no capture page may reach.
type ShotProbe struct{}

func (ShotProbe) Hit() { probeHits.Add(1) }

func TestMain(m *testing.M) {
	if os.Getenv("EXPLORE_LIVE") == "" {
		os.Exit(m.Run())
	}
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	// A capture's window closing is not the app quitting.
	mygo.App.OnWindowAllClosed(func() {})
	mygo.Bind(ShotProbe{})
	code := 1
	mygo.App.WhenReady(func() {
		go func() {
			code = m.Run()
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	os.Exit(code)
}

func live(t *testing.T) {
	if os.Getenv("EXPLORE_LIVE") == "" {
		t.Skip("EXPLORE_LIVE is not set")
	}
}

// ownWindows counts this process's windows as the window server lists them: those on screen (what Mission Control, the
// Dock's window list and ⌘` show) and all it has.
func ownWindows(t *testing.T) (shown, all int) {
	t.Helper()
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatal(err)
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatal(err)
	}
	var list func(option, relativeTo uint32) uintptr
	var count func(array uintptr) int
	var at func(array uintptr, i int) uintptr
	var value func(dict, key uintptr) uintptr
	var number func(num uintptr, kind int, out *int32) uint8
	var str func(alloc uintptr, s string, encoding uint32) uintptr
	var release func(uintptr)
	purego.RegisterLibFunc(&list, cg, "CGWindowListCopyWindowInfo")
	purego.RegisterLibFunc(&count, cf, "CFArrayGetCount")
	purego.RegisterLibFunc(&at, cf, "CFArrayGetValueAtIndex")
	purego.RegisterLibFunc(&value, cf, "CFDictionaryGetValue")
	purego.RegisterLibFunc(&number, cf, "CFNumberGetValue")
	purego.RegisterLibFunc(&str, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&release, cf, "CFRelease")
	key := str(0, "kCGWindowOwnerPID", 0x08000100) // kCFStringEncodingUTF8
	defer release(key)
	own := func(option uint32) (mine, any int) {
		windows := list(option, 0)
		if windows == 0 {
			t.Fatal("the window server lists nothing")
		}
		defer release(windows)
		for i := range count(windows) {
			var pid int32
			if v := value(at(windows, i), key); v != 0 && number(v, 3, &pid) != 0 { // kCFNumberSInt32Type
				any++
				if int(pid) == os.Getpid() {
					mine++
				}
			}
		}
		return mine, any
	}
	shown, onScreen := own(1) // kCGWindowListOptionOnScreenOnly
	all, _ = own(0)           // kCGWindowListOptionAll
	if onScreen == 0 {
		t.Fatal("no window on screen at all: the list is not read")
	}
	return shown, all
}

func eval(t *testing.T, p *mygo.Page, code string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	v, err := p.EvalContext(ctx, code)
	if err != nil {
		t.Fatalf("%s: %v", code, err)
	}
	s, _ := v.(string)
	return s
}

func loadHTML(t *testing.T, p *mygo.Page, html, base string) {
	t.Helper()
	done := make(chan struct{}, 1)
	off := p.OnDidFinishLoad(func() {
		select {
		case done <- struct{}{}:
		default:
		}
	})
	defer off()
	p.LoadHTML(html, base)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the page did not load")
	}
}

const probePage = `<!doctype html><html><head><title>Isolation probe</title></head><body style="margin:0;background:#fff">
<div style="position:absolute;left:0;top:0;width:720px;height:450px;background:rgb(255,0,0)"></div>
<img src="http://127.0.0.1:PORT/by-address.png"><img src="http://localhost:PORT/by-name.png">
<script>
window.__probe = {};
const note = (k, v) => { window.__probe[k] = v };
fetch('http://127.0.0.1:PORT/fetched').then(() => note('fetch', 'answered'), () => note('fetch', 'refused'));
document.cookie = 'probe=1; path=/';
localStorage.setItem('probe', '1');
note('cookie', document.cookie);
note('alert', String(window.alert).includes('[native code]') ? 'native' : 'silenced');
const frame = document.createElement('iframe');
document.body.append(frame);
note('frameAlert', String(frame.contentWindow.alert).includes('[native code]') ? 'native' : 'silenced');
(window.mygo ? window.mygo.call('ShotProbe.Hit') : Promise.reject(new Error('no bridge'))).then(() => note('call', 'answered'), e => note('call', String(e && e.message || e)));
(navigator.mediaDevices ? navigator.mediaDevices.getUserMedia({ audio: true }) : Promise.reject(new Error('no mediaDevices'))).then(() => note('microphone', 'granted'), e => note('microphone', String(e && e.name || e)));
note('size', [innerWidth, innerHeight, devicePixelRatio].join('x'));
note('visibility', document.visibilityState);
</script></body></html>`

// Every isolation site-shots.ts asks of a capture's window, in a real one: see TestShotWindowIsolated for the list.
func TestLiveIsolation(t *testing.T) {
	live(t)
	var hits atomic.Int32
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte("reached")) })}
	go srv.Serve(ln)
	defer srv.Close()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	rules, err := privateRules()
	if err != nil {
		t.Fatal("WebKit compiles the rules:", err)
	}
	active, windows := mygo.App.IsActive(), len(mygo.Windows())
	at := finest(mygo.Screen.Displays())
	win := openShot(rules, at)
	defer closeShot(win)
	p := win.Page()
	loadHTML(t, p, strings.ReplaceAll(probePage, "PORT", port), "http://isolation.example/")
	// Asked with a gesture, as Eval runs code: a pop-up and a download.
	download := fmt.Sprintf("trie-explore-probe-%d.txt", time.Now().UnixNano())
	popup := eval(t, p, `return String(window.open('https://example.org/'))`)
	eval(t, p, `const a = document.createElement('a'); a.href = 'data:text/plain,probe'; a.download = '`+download+`'; document.body.append(a); a.click(); return 'clicked'`)
	time.Sleep(2 * time.Second)
	var probe map[string]string
	if err := json.Unmarshal([]byte(eval(t, p, `return JSON.stringify(window.__probe)`)), &probe); err != nil {
		t.Fatal(err)
	}
	t.Logf("page: %v; window.open: %s", probe, popup)
	shown, all := ownWindows(t)
	t.Logf("window server: %d of this app's %d windows on screen; app active %v (before %v), window visible %v, focused %v",
		shown, all, mygo.App.IsActive(), active, win.IsVisible(), win.IsFocused())
	// Never shown: no focus, nothing in the Dock or a window list. (IsVisible is true: the window answers as shown,
	// for WebKit to draw its page; the window server tells what shows.)
	if shown != 0 || all == 0 || win.IsFocused() || mygo.App.IsActive() != active {
		t.Error("the capture's window shows or took the focus")
	}
	// The page calls no Go method.
	if !strings.Contains(probe["call"], "untrusted") || probeHits.Load() != 0 {
		t.Errorf("a bound method: %q, %d calls", probe["call"], probeHits.Load())
	}
	// No permission, no window, no download, no sound.
	if probe["microphone"] == "granted" {
		t.Error("the microphone is granted")
	}
	if popup != "null" || len(mygo.Windows()) != windows+1 {
		t.Errorf("window.open gave %s; %d windows", popup, len(mygo.Windows())-windows)
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, "Downloads", download)); err == nil {
		os.Remove(filepath.Join(home, "Downloads", download))
		t.Error("a download was saved")
	}
	if !p.IsAudioMuted() {
		t.Error("the page is not muted")
	}
	// Dialogs are silenced in the page (an iframe the page makes may still have its own; see the report).
	if probe["alert"] != "silenced" {
		t.Error("alert is the page's own")
	}
	// A session of its own that keeps nothing: the next capture's page starts with no cookie or storage, and the
	// app's own session never saw them.
	if probe["cookie"] != "probe=1" {
		t.Fatalf("the probe's cookie was not set: %q", probe["cookie"])
	}
	next := openShot(rules, finest(mygo.Screen.Displays()))
	defer closeShot(next)
	loadHTML(t, next.Page(), `<script>window.__seen = JSON.stringify({ cookie: document.cookie, stored: localStorage.getItem('probe') })</script>`, "http://isolation.example/")
	if seen := eval(t, next.Page(), `return window.__seen`); seen != `{"cookie":"","stored":null}` {
		t.Errorf("the next capture sees %s", seen)
	}
	if cookies, err := mygo.Cookies.List(mygo.CookieFilter{URL: "http://isolation.example/"}); err != nil || len(cookies) != 0 {
		t.Errorf("the app's session: %v %v", cookies, err)
	}
	// 1440 × 900 CSS pixels, captured at the display's pixels of a window as large as fits (finest).
	if !strings.HasPrefix(probe["size"], "1440x900x") {
		t.Errorf("the page's size: %s", probe["size"])
	}
	shot, err := win.CapturePage()
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(shot))
	if err != nil {
		t.Fatal(err)
	}
	scale := 1.0
	for _, d := range mygo.Screen.Displays() {
		scale = max(scale, d.ScaleFactor)
	}
	if b, o := img.Bounds(), shotOptions(nil, at); b.Dx() != int(scale)*o.Width || b.Dy() != int(scale)*o.Height {
		t.Errorf("captured %v", b)
	}
	if r, g, b, _ := img.At(100, 100).RGBA(); r>>8 < 200 || g>>8 > 60 || b>>8 > 60 {
		t.Errorf("the page is not drawn: %d %d %d", r>>8, g>>8, b>>8)
	}
	// Requests to this machine never leave the page, nor a navigation to it.
	eval(t, p, `location.href = 'http://127.0.0.1:`+port+`/navigated'; return 'went'`)
	time.Sleep(time.Second)
	if hits.Load() != 0 || probe["fetch"] != "refused" {
		t.Errorf("%d requests reached this machine; fetch %s", hits.Load(), probe["fetch"])
	}
	t.Logf("an iframe the page makes: alert %s", probe["frameAlert"])
}

// The first screens of real sites, through Shots as Explore takes them, one site at a time.
func TestLiveSites(t *testing.T) {
	live(t)
	sites, out := os.Getenv("EXPLORE_LIVE_SITES"), os.Getenv("EXPLORE_LIVE_OUT")
	if sites == "" || out == "" {
		t.Skip("EXPLORE_LIVE_SITES or EXPLORE_LIVE_OUT is not set")
	}
	type result struct {
		Home      string         `json:"home"`
		Taken     bool           `json:"taken"`
		Seconds   float64        `json:"seconds"`
		Files     map[string]any `json:"files"`
		MaxShown  int            `json:"maxShownWindows"`
		Active    [2]bool        `json:"appActiveBeforeAfter"`
		WindowsAt [2]int         `json:"windowsBeforeAfter"`
	}
	var results []result
	shots := NewShots(out)
	for _, home := range strings.Split(sites, ",") {
		r := result{Home: home, Files: map[string]any{}, Active: [2]bool{mygo.App.IsActive()}, WindowsAt: [2]int{len(mygo.Windows())}}
		site := host(home)
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		wait := shots.Capture(site, []string{home}, true)
		done := make(chan bool)
		go func() { done <- wait(ctx) }()
	watch:
		for {
			select {
			case r.Taken = <-done:
				break watch
			case <-time.After(150 * time.Millisecond):
				shown, _ := ownWindows(t)
				r.MaxShown = max(r.MaxShown, shown)
			}
		}
		cancel()
		r.Seconds = time.Since(start).Seconds()
		time.Sleep(300 * time.Millisecond) // the window closes on the main thread
		r.Active[1], r.WindowsAt[1] = mygo.App.IsActive(), len(mygo.Windows())
		for _, width := range []int{1280, 400} {
			file := ShotFile(out, site, width)
			data, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
			r.Files[filepath.Base(file)] = map[string]any{"bytes": len(data), "width": cfg.Width, "height": cfg.Height, "jpeg": err == nil}
		}
		t.Logf("%+v", r)
		if r.MaxShown != 0 || r.Active[0] != r.Active[1] || r.WindowsAt[0] != r.WindowsAt[1] {
			t.Errorf("%s: the window showed, took the focus or stayed open", home)
		}
		results = append(results, r)
	}
	data, _ := json.MarshalIndent(results, "", " ")
	if err := os.WriteFile(filepath.Join(out, "report.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Stop, as the app's window closes, ends a capture under way at once: its window closes without waiting for the page
// (EXPLORE_LIVE_STOP: a home page that takes a while, as one that never finishes loading).
func TestLiveStop(t *testing.T) {
	live(t)
	home := os.Getenv("EXPLORE_LIVE_STOP")
	if home == "" {
		t.Skip("EXPLORE_LIVE_STOP is not set")
	}
	shots := NewShots(t.TempDir())
	wait := shots.Capture(host(home), []string{home}, true)
	time.Sleep(time.Second)
	if _, all := ownWindows(t); all == 0 {
		t.Fatal("the capture's window is not open")
	}
	start := time.Now()
	shots.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	taken := wait(ctx)
	took := time.Since(start)
	time.Sleep(300 * time.Millisecond) // the window closes on the main thread
	// The app's windows: the text input system keeps a hidden one of its own (TUINSWindow) once a page has shown.
	left := len(mygo.Windows())
	t.Logf("stopped in %s, taken %v, windows left %d", took, taken, left)
	if taken || took > 2*time.Second || left != 0 {
		t.Errorf("the capture under way ends at once and closes its window: %s, %v, %d", took, taken, left)
	}
}

// firstPage comes in as sites made of motion do: one colour while a loader runs, then words that fade in with a CSS
// animation, beside a WebGL canvas, which a capture's window drew only once WebKit counted it as shown (seen).
const firstPage = `<!doctype html><html><head><style>
body { margin: 0; background: #111 }
@keyframes in { from { opacity: 0 } to { opacity: 1 } }
.words { position: absolute; left: 0; top: 0; width: 720px; height: 450px; background: rgb(230, 30, 30); animation: in .4s both }
canvas { position: absolute; left: 720px; top: 0; width: 720px; height: 450px }
</style></head><body><script>
setTimeout(() => {
  document.body.insertAdjacentHTML('beforeend', '<div class=words></div><canvas width=720 height=450></canvas>')
  const gl = document.querySelector('canvas').getContext('webgl')
  const frame = () => { gl.clearColor(1, 0.5, 0, 1); gl.clear(gl.COLOR_BUFFER_BIT); requestAnimationFrame(frame) }
  frame()
}, 2000)
</script></body></html>`

// A first screen that comes in slowly is taken once it has (firstScreen), its CSS animation run and its WebGL drawn;
// one that stays one colour is not taken.
func TestLiveFirstScreen(t *testing.T) {
	live(t)
	rules, err := privateRules()
	if err != nil {
		t.Fatal(err)
	}
	shots := NewShots(t.TempDir())
	win := openShot(rules, finest(mygo.Screen.Displays()))
	defer closeShot(win)
	loadHTML(t, win.Page(), firstPage, "http://first.example/")
	img := shots.firstScreen(win)
	if img == nil {
		t.Fatal("the first screen was not taken")
	}
	b := img.Bounds()
	at := func(fx, fy float64) (uint32, uint32, uint32) {
		r, g, bl, _ := img.At(b.Min.X+int(fx*float64(b.Dx())), b.Min.Y+int(fy*float64(b.Dy()))).RGBA()
		return r >> 8, g >> 8, bl >> 8
	}
	if r, g, bl := at(0.25, 0.25); r < 200 || g > 60 || bl > 60 {
		t.Errorf("the words did not fade in: %d %d %d", r, g, bl)
	}
	if r, g, bl := at(0.75, 0.25); r < 200 || g < 100 || g > 160 || bl > 40 {
		t.Errorf("the WebGL canvas is not drawn: %d %d %d", r, g, bl)
	}
	blank := openShot(rules, finest(mygo.Screen.Displays()))
	defer closeShot(blank)
	loadHTML(t, blank.Page(), `<!doctype html><body style="background:#fff"></body>`, "http://blank.example/")
	if shots.firstScreen(blank) != nil {
		t.Error("a page of one colour was taken")
	}
}
