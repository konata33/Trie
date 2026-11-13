// Command trie is Trie, the native window on the library, shown with
// MyGo's own UI: run from source, a copy at TRIE_DATA_DIR; built as
// Trie.app, the real library (dataDir).
//
//	TRIE_DATA_DIR=/tmp/trie-copy go run .
package main

import (
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"trie/internal/app"
	"trie/internal/explore"
	"trie/internal/library"
	// The interface in English and Chinese: English, the app's own, for any
	// reader whose system is not in Chinese (PRODUCT.md).
	"trie/internal/locales"
	"trie/internal/reader"
	"trie/internal/reading"
	"trie/internal/subscription"
)

// icon is the app icon. A plain executable (go run) has no bundle to take
// it from, so the Dock shows it only when the app sets it.
//
//go:embed resources/icon.png
var icon []byte

// dataDir is the library's folder. A release build (mygo build, Trie.app)
// opens the real library, ~/Library/Application Support/Trie, unless
// TRIE_DATA_DIR names another. Run from
// source, the app opens only TRIE_DATA_DIR, which must be absolute and
// must not be that library: development works on copies.
func dataDir(env string, home string, release bool) (string, error) {
	real := filepath.Join(home, "Library", "Application Support", "Trie")
	if env == "" {
		if release {
			return real, nil
		}
		return "", errors.New("TRIE_DATA_DIR is not set: point it at a copy of the library, as an absolute path")
	}
	if !filepath.IsAbs(env) {
		return "", fmt.Errorf("TRIE_DATA_DIR must be absolute: %q", env)
	}
	dir := filepath.Clean(env)
	if within(dir, real) {
		return "", fmt.Errorf("TRIE_DATA_DIR is the real library (%s): run from source, the app opens only a copy of it", real)
	}
	return dir, nil
}

// errHeld is why a library stays closed: another window of this app has it
// open (lockLibrary).
var errHeld = errors.New("another window of this app has the library open")

// refuse says in a dialog why the library stays closed, and quits.
func refuse(err error) {
	title, detail := locales.ErrorLibraryCantOpen(), err.Error()
	if errors.Is(err, errHeld) {
		title, detail = locales.ErrorLibraryInUse(), locales.ErrorLibraryInUseDetail()
	}
	mygo.App.SetName("Trie")
	mygo.App.WhenReady(func() {
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{Type: mygo.MessageWarning, Message: title, Detail: detail})
		mygo.App.Quit()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// within reports whether dir is real or inside it, as the file system sees
// them: through symbolic links, and without telling case apart, as APFS does
// by default.
func within(dir, real string) bool {
	d, r := strings.ToLower(resolve(dir)), strings.ToLower(resolve(real))
	return d == r || strings.HasPrefix(d, r+string(filepath.Separator))
}

// resolve follows p's symbolic links; a folder not made yet is its nearest
// existing parent, resolved, and the rest.
func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if parent := filepath.Dir(p); parent != p {
		return filepath.Join(resolve(parent), filepath.Base(p))
	}
	return p
}

func main() {
	home, _ := os.UserHomeDir()
	dir, err := dataDir(os.Getenv("TRIE_DATA_DIR"), home, !mygo.IsDev())
	if err != nil {
		log.Fatal("trie: ", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal("trie: ", err)
	}
	held, err := lockLibrary(dir)
	if err != nil {
		refuse(err)
		return
	}
	defer held.Close()
	seed := library.Snapshot{Version: 1, AsOf: time.Now().Format("2006-01-02")}
	store, err := library.Open(filepath.Join(dir, "library.sqlite"), seed)
	if err != nil {
		refuse(err)
		return
	}
	defer store.Close()
	// English articles are set in New York, which macOS hides from apps by
	// name (DESIGN.md Typography); before any text is laid out.
	reader.RegisterSerif()
	// win is the window while it is open: closed, the app stays in the Dock (keepOpen).
	var win atomic.Pointer[mygo.Window]
	// post runs fn on the main thread, where the window builds, and redraws the window if it is open.
	post := func(fn func()) {
		if w := win.Load(); w != nil {
			w.Update(fn)
		} else {
			go mygo.RunOnMain(fn)
		}
	}
	var a *app.App
	// Explore names and draws the sources subscribed from it: the catalog's
	// name for a feed, the catalog's icon for a site.
	var ex *explore.Service
	reader := reading.New(store, nil, reading.Worker)
	subs := subscription.New(store, nil, func(home string) *string { return ex.IconFor(home) },
		func(feedURL, home string) string { return ex.NameFor(&feedURL, home) })
	resources, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		log.Fatal("trie: ", err)
	}
	ex = explore.Open(filepath.Join(resources, "explore"), dir, store, subs, reader, func() {
		post(func() { a.ExploreChanged() })
	})
	// Sources subscribed before subscriptions took the catalog's name take it now.
	if err := store.AdoptNames(ex.NameFor); err != nil {
		log.Print("trie: ", err)
	}
	a, err = app.New(app.Options{
		DataDir:       dir,
		Store:         store,
		Reading:       reader,
		Subscriptions: subs,
		Explore:       ex,
		Post:          post,
		Refresh:       os.Getenv("TRIE_NO_REFRESH") == "",
		SetLocale:     mygo.App.SetLocale,
	})
	if err != nil {
		log.Fatal("trie: ", err)
	}
	steps := os.Getenv("TRIE_PLAY")
	mygo.App.SetName("Trie")
	mygo.App.WhenReady(func() {
		if !mygo.App.IsPackaged() {
			if err := mygo.App.Dock.SetIcon(icon); err != nil {
				log.Print("trie: ", err)
			}
		}
		open := func() {
			w := mygo.NewWindow(mygo.WindowOptions{
				Title: "Trie", Width: 1280, Height: 850, MinWidth: 1100, MinHeight: 700,
				BackgroundColor: "#141414",
				// The page fills the window under the traffic lights.
				TitleBarStyle:        mygo.TitleBarHiddenInset,
				TrafficLightPosition: &mygo.Point{X: 16, Y: 13},
				Content:              ui.View(a.View),
				// Played through, the window shows without taking the focus.
				Hidden: steps != "",
			})
			win.Store(w)
			w.OnClosed(func() { win.CompareAndSwap(w, nil) })
		}
		open()
		keepOpen(func() {
			if w := win.Load(); w == nil {
				open()
			} else if w.IsMinimized() {
				w.Restore()
			}
		}, ex.Stop)
		if steps != "" {
			// On the display that draws finest, so that runs time the same pixels wherever the pointer is.
			d := mygo.Display{ScaleFactor: 0}
			for _, o := range mygo.Screen.Displays() {
				if o.ScaleFactor > d.ScaleFactor {
					d = o
				}
			}
			w := win.Load()
			w.SetPosition(d.WorkArea.X+40, d.WorkArea.Y+40)
			log.Printf("trie: play at scale %g", d.ScaleFactor)
			w.ShowInactive()
			go play(w, a, strings.Split(steps, ","))
		}
		// The design room's first screens are captured behind anything Explore asks for; not in a play, whose frames
		// are timed: the captures load sites in web views of their own, in hidden windows.
		if steps == "" {
			ex.Start()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// keepOpen keeps the app running with its window closed, in the Dock, as Mac apps do: a click on its icon
// brings the window back (activate: reopen), whatever the hidden windows
// that capture sites for Explore are doing. Quitting, as ⌘Q does, stops the captures under way first (stop), which
// close their windows at once rather than keep the app until they are done.
func keepOpen(reopen func(), stop func()) {
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnActivate(func(bool) { reopen() })
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { stop() })
}

// play presses through steps once the window shows, 1.2 s apart, so that
// MYGO_FRAME_STATS=all logs how long their frames take on the GPU; then the
// app quits. A step is a name App.Press takes, shot:<file> or screen:<file>.
func play(win *mygo.Window, a *app.App, steps []string) {
	time.Sleep(2 * time.Second) // the first frames
	for _, s := range steps {
		log.Printf("trie: %s", s)
		// screen:<file> writes the window's frame as the screen shows it, drawn by the GPU, to file: the window must be
		// in front there.
		if file, ok := strings.CutPrefix(s, "screen:"); ok {
			b := win.Bounds()
			rect := fmt.Sprintf("-R%d,%d,%d,%d", b.X, b.Y, b.Width, b.Height)
			if out, err := exec.Command("screencapture", "-x", "-o", rect, file).CombinedOutput(); err != nil {
				log.Printf("trie: %v %s", err, out)
			}
			continue
		}
		// shot:<file> writes the window to file as the screen shows it, whatever lies over it (shoot).
		if file, ok := strings.CutPrefix(s, "shot:"); ok {
			if err := shoot(win, file); err != nil {
				log.Print("trie: ", err)
			}
			continue
		}
		win.Update(func() { a.Press(s) })
		time.Sleep(1200 * time.Millisecond)
	}
	log.Print("trie: done")
	mygo.App.Quit()
}
