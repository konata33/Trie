// Package app is Trie's window: the shell (the sidebar and the content
// area's crossings), an author's page and the reader, drawn by MyGo, on the
// library, reading and subscription packages.
package app

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/reading"
	"trie/internal/subscription"
	"trie/internal/theme"
)

// Options are what the window runs on.
type Options struct {
	DataDir string // the library's folder, for the thumbnails' cache
	Store   *library.Store
	Reading *reading.Service
	// Subscriptions may be nil: then nothing is refreshed or added.
	Subscriptions *subscription.Service
	// Explore is the catalog and its recommendations; nil leaves 探索 saying
	// it is not available.
	Explore *explore.Service
	// Post runs fn on the window's thread and builds a frame after it
	// (Window.Update). Nil runs the jobs' results as the next frame is
	// built, the way a tester wants them.
	Post func(fn func())
	// Sync runs the jobs at once, on the calling goroutine, for tests and
	// screenshots; otherwise each runs on a goroutine of its own.
	Sync bool
	// Refresh reads the subscriptions once as the window opens.
	Refresh bool
	// Thumbs is the tests' stand-in for articles' pictures: set, the cards
	// load none (keepPic); nil uses the library and the network (thumb.go).
	Thumbs func(id string, index int) (*ui.Bitmap, error)
	// SetLocale chooses the interface's language (mygo.App.SetLocale), from
	// the settings panel. Nil, as in the tests, keeps dates in Chinese.
	SetLocale func(tag string)
}

// page is what the content area shows: a sidebar tab (home, favorites,
// explore, clips), an author's page or an article.
type page struct{ kind, id string }

var tabs = []struct {
	kind  string
	label func() string
	icon  string
}{{"home", locales.TabHome, "home"}, {"favorites", locales.TabFavorites, "favorites"}, {"explore", locales.TabExplore, "explore"}, {"clips", locales.TabClips, "clips"}}

// App is the window's state; its view builds the interface from it.
type App struct {
	opt   Options
	theme *ui.Theme

	// warming is set while a page is laid out unseen (warm.go): it asks for nothing.
	warming bool

	lib     library.LibraryState
	authors []*author
	icons   map[string]*siteIcon // by source ID

	// Navigation: the page showing, where going
	// back goes, the change asked for in this build and shown in the next,
	// how the page showing came, and the page left, built on for its exit.
	page page
	from []page
	next *move
	came *move
	left *leaving

	kbd    bool // the last input was a key: what it does shows at once
	Reduce bool // the app's own switch for less motion (tests)

	lay     *motion.Lay
	layRow  string // the article whose row the ground goes back to
	flights []*flight
	run     uint64
	chips   map[string]int  // a sidebar chip is built anew to take its face back
	chipFly map[string]bool // while the face flies back into it
	area    ui.Rect         // the content area, as the frame before laid it out

	moonBack *moonBack // a list's moon waning as the article's reader is left
	viewer   viewer    // a picture shown large

	lists   map[string]*listState // an author's page by source ID
	readers map[string]*readerPage
	thumbs  map[string]*thumb
	// Where each article was read to in this session, kept as each reader
	// is left.
	positions map[string]reader.Position
	fontSize  float32 // the body's size, for the session
	wide      bool    // 宽松

	refresh refreshState
	fresh   map[string]bool // authors with new articles from the last refresh
	slides  map[any]*slide  // where each list's selected ground was last (slideGround)

	home     *homeState  // 首页
	fav      *favState   // 收藏
	clips    *clipsState // 摘录
	ex       *exploreState
	trials   map[string]*trial      // Explore's posts opened, by the library ID they are kept by
	byURL    map[string]string      // the library's articles by every address they are known by (postID)
	chipAt   map[string]ui.Rect     // the sidebar's faces, as the frame before laid them out
	flights2 []*faceFlight          // faces flying to and from the sidebar
	lights   map[string]time.Time   // places a face just landed on
	parting  map[string]*partingRow // sidebar rows of authors just let go, kept a beat
	faceLeft map[string]bool        // rows whose face flew off as they were let go

	layers layers
	toast  toastState
	// Faces on their way onto the notice and back (trip), the notice's face
	// and the authors' faces in 收藏, as the frame before laid them out.
	trips      []*faceFlight
	noticeFace ui.Rect
	shelfFaces map[string]ui.Rect
	// How the rows of the page left for an article draw its title, for the
	// title to travel back to (openAs).
	looks     map[string]titleLook
	scrolling *ui.ScrollState // the live page's, for the scrollbar
	ruler     rulerState
	noDrag    []ui.Rect // controls under the window's drag strip, from the frame before

	waiting []func() // results waiting for the next build (Options.Post nil)
	warmed  int      // frames of the warm-up drawn (warm.go)
	hover   string   // the article whose row the pointer rests on
	pinned  bool     // hover was pressed (Press), not pointed at
	// The author whose sidebar row the pointer rests on, and since when.
	hoverAuthor   string
	hoverAuthorAt time.Time
	hoverAt       time.Time
	movedAt       time.Time // when the page last changed
	pressed       []string  // Press's, for the next build
	busy          bool      // a library change is being saved
	said          []string  // for screen readers to read out at the next build
	errText       string
	booted        bool
}

// New makes the window's state from the library.
func New(opt Options) (*App, error) {
	lib, err := opt.Store.Library()
	if err != nil {
		return nil, err
	}
	t := theme.UI()
	t.TooltipDelay = motion.TipWait
	a := &App{opt: opt, theme: t, page: page{kind: "home"}, chips: map[string]int{}, chipFly: map[string]bool{},
		lists: map[string]*listState{}, readers: map[string]*readerPage{}, thumbs: map[string]*thumb{},
		positions: map[string]reader.Position{}, fontSize: theme.ReadingSize, icons: map[string]*siteIcon{}, fresh: map[string]bool{},
		ex: newExploreState(), trials: map[string]*trial{}, chipAt: map[string]ui.Rect{}, lights: map[string]time.Time{},
		parting: map[string]*partingRow{}, faceLeft: map[string]bool{}, shelfFaces: map[string]ui.Rect{},
		looks: map[string]titleLook{}}
	a.accept(lib)
	return a, nil
}

// accept takes a library the store gave back, unless an older one.
func (a *App) accept(lib library.LibraryState) {
	if a.lib.Revision > lib.Revision && len(a.lib.Sources) > 0 {
		return
	}
	a.lib = lib
	a.derive()
}

// do runs job off the window's thread and its result on it.
func (a *App) do(job func() func()) {
	if a.opt.Sync {
		a.post(job())
		return
	}
	go func() { a.post(job()) }()
}

func (a *App) post(fn func()) {
	if fn == nil {
		return
	}
	if a.opt.Post != nil {
		a.opt.Post(fn)
		return
	}
	a.waiting = append(a.waiting, fn)
}

var bg = context.Background()

// still reports whether motion should move nothing: the desktop asks for
// less motion, or the app's switch does.
func (a *App) still(c *ui.Context) bool { return a.Reduce || c.Preferences().ReduceMotion }

// tr returns t as the moment asks: at once after a key, without moving
// under less motion.
func (a *App) tr(c *ui.Context, t ui.ElementTransition) ui.ElementTransition {
	switch {
	case a.kbd:
		return motion.Instant(t)
	case a.still(c):
		return motion.Still(t)
	}
	return t
}

// ease is a change of colour or opacity over d along the one curve, at once
// after a key.
func (a *App) ease(d time.Duration) ui.Animation {
	if a.kbd {
		return ui.Animation{Duration: time.Nanosecond}
	}
	return ui.Animation{Duration: d, Ease: motion.Ease, NonMotion: true}
}

// observe keeps what the last input was: a key marks
// the keyboard, the pointer moving clears it.
func (a *App) observe(ev ui.InputEvent) {
	switch ev.Kind {
	case ui.InputKeyDown:
		if ev.Key != ui.KeyUnknown {
			a.kbd = true
		}
	case ui.InputPointerDown, ui.InputPointerMove:
		a.kbd, a.pinned = false, false
	}
}

// View builds the window.
func (a *App) View(c *ui.Context) {
	hourCycle = c.Region().HourCycle
	c.SetTheme(a.theme)
	for len(a.waiting) > 0 {
		fns := a.waiting
		a.waiting = nil
		for _, fn := range fns {
			fn()
		}
	}
	if !a.booted {
		a.booted = true
		a.boot(c)
	}
	if len(a.pressed) > 0 {
		a.presses(c)
	}
	if l := a.left; l != nil && c.Now().After(l.at) && (l.move.door == "" || c.Now().Sub(l.at) >= motion.DoorLeave) {
		a.left = nil // a frame later: its copy goes as its exit says; through a door, once it has gone (doorLeave)
	}
	if m := a.next; m != nil {
		a.movedAt = c.Now()
		a.left = &leaving{page: a.page, from: a.from, move: m, at: c.Now()}
		a.leaveReader(a.page, m.to)
		a.next, a.page, a.from, a.came = nil, m.to, m.from, m
		if m.kbd {
			a.left = nil // gone at once, without a copy
			c.DisableAnimations()
		}
		if m.chip != "" {
			a.chips[m.chip]++ // built anew, it appears, and the face flies into it
			a.chipFly[m.chip] = true
		}
	}
	a.shortcuts(c)
	holes := a.noDrag
	a.noDrag = nil
	root := ui.Row(c).Fill().AlignItems(ui.Stretch).Background(theme.Window).Font(theme.Sans).FontSize(theme.FontSize).
		LineHeight(theme.LineHeight).FontFeatures(theme.Numbers).TextColor(theme.Text).ObserveInput(a.observe)
	// The page behind a dialog goes out of focus while it shows.
	target, d := float32(0), a.layers.out
	if a.layers.dialog != nil {
		target, d = motion.Behind, a.layers.in
	}
	var behind float32
	if a.kbd || d == 0 {
		behind = root.Jump("behind", target)
	} else {
		behind = root.AnimateTo("behind", target, motion.Fade(d))
	}
	// Behind a passage on the stage the whole window, sidebar too, goes out
	// of focus as it goes dark; the stage itself is a layer above it.
	if s := a.clipsSt().stage; s != nil && a.page.kind == "clips" {
		behind = max(behind, motion.Defocus*s.dark(s.values(c.Now())))
	}
	if behind > 0.01 {
		root.Blur(behind)
	} else if a.warmed < warmFrames && !a.opt.Sync {
		a.warmed++
		root.Blur(warmBlur)
		c.Invalidate()
	}
	root.Children(func() {
		a.sidebar(c)
		a.stage(c)
	})
	if a.moonBack != nil && !a.moonBack.started {
		a.moonBack = nil // the page gone back to does not show its row
	}
	a.dragStrip(c, holes)
	a.buildLayers(c)
	// What Explore says as it changes (a room crossed, a subscription), for screen readers.
	if a.ex.live != "" {
		c.Announce(a.ex.live)
		a.ex.live = ""
	}
	for _, text := range a.said {
		c.Announce(text)
	}
	a.said = nil
	ui.Overlay(c, func() {
		live := a.flights[:0]
		for _, f := range a.flights {
			if f.w.From.W <= 0 {
				continue // its first end was not built: it goes with its page
			}
			oldStyle, newStyle, text := f.old, f.new, f.text
			if f.w.View(c, f.key, func() ui.Element { return oldStyle(c, text) }, func() ui.Element { return newStyle(c, text) }) {
				live = append(live, f)
			}
		}
		a.flights = live
		a.faceFlights(c)
		a.tripsView(c)
	})
}

// dragStrip is the window's drag strip: the top 38
// DIPs move the window, but for the controls there.
func (a *App) dragStrip(c *ui.Context, holes []ui.Rect) {
	ui.Box(c.Key("titlebar")).Absolute().Left(0).Top(0).Right(0).Height(38).DragWindow().AccessHidden(true).
		HitTest(func(x, y float32, r ui.Rect) bool {
			for _, h := range holes {
				if x >= h.X && x < h.X+h.W && y >= h.Y && y < h.Y+h.H {
					return false
				}
			}
			return true
		})
}

// control notes e, a control that may lie under the drag strip, where the
// frame before showed it (a sticky bar's, a scrolled band's).
func (a *App) control(e ui.Element) ui.Element {
	if r := e.Bounds(); r.Y < 38 && r.W > 0 {
		a.noDrag = append(a.noDrag, r)
	}
	return e
}

// boot runs once, with the first frame: the launch's refresh, which reads
// the subscriptions again once per launch.
func (a *App) boot(c *ui.Context) {
	if !a.opt.Sync {
		go warmFonts()
	}
	if a.opt.Refresh && a.opt.Subscriptions != nil && len(a.authors) > 0 {
		a.refreshFeeds("")
	}
}

// mutate saves a change to the library and takes the library it gives
// back; after runs once it is saved.
func (a *App) mutate(action library.LibraryAction, after func()) { a.mutateOr(action, after, nil) }

// mutateOr is mutate, with failed run when the change is not saved.
func (a *App) mutateOr(action library.LibraryAction, after, failed func()) {
	if a.busy {
		if failed != nil {
			failed()
		}
		return
	}
	a.busy = true
	store := a.opt.Store
	a.do(func() func() {
		lib, err := store.Mutate(action)
		return func() {
			a.busy = false
			if err != nil {
				a.fail(locales.ErrorNotSaved(err.Error()))
				if failed != nil {
					failed()
				}
				return
			}
			a.accept(lib)
			if after != nil {
				after()
			}
		}
	})
}
