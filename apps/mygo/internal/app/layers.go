package app

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/theme"
	"trie/internal/web"
)

// layers are what shows over the pages: the subscription menu, the add
// dialog, the shortcut sheet and the notices.
type layers struct {
	menu     string // the open subscription menu: a source's ID for its sidebar row's, headMenu+ID for its page's
	renaming string // the source being renamed in its row
	name     string // the name being typed

	dialog *subscribeForm
	sheet  bool
	opener ui.Rect // the button the dialog or the sheet grew out of
	in     time.Duration
	out    time.Duration
}

// headMenu marks the subscription menu of an author's page, so that it and
// the one on their sidebar row open on their own.
const headMenu = "head:"

// closeAll closes the menus as a page changes (sourceMenu.close()).
func (l *layers) closeAll() { l.menu = "" }

// sourceMenu is an author's subscription menu, out of
// its ··· button, which it returns: 重命名 / 在浏览器打开 / 取消订阅. small is the
// sidebar's 24 square trigger.
func (a *App) sourceMenu(c *ui.Context, key any, src *library.Source, side bool) ui.Element {
	slot := src.ID
	if !side {
		slot = headMenu + src.ID
	}
	open := a.layers.menu == slot
	m := ui.MenuBase(c.Key(key), &open)
	t := m.Trigger.Label(locales.SidebarWriterActions(src.DisplayName)).Size(24, 24).Radius(6).Center().FocusRing(false)
	if t.FocusVisible() {
		t.FocusRing(true).FocusRingOffset(-2)
	}
	t.Children(func() { a.pressIcon(c, "more", 16, false, open) }) // a menu opens on the press
	m.Popup(func(panel ui.Element) {
		a.menuPanel(c, panel)
		if side {
			panel.AttachTo(m.Trigger, ui.AnchorTopRight, ui.AnchorTopLeft).Margin(-3, 0, 0, 8)
		} else {
			panel.AttachTo(m.Trigger, ui.AnchorBottomLeft, ui.AnchorTopLeft).Margin(8, 0, 0, 0)
		}
		if a.menuItem(c, m.Item(), locales.SidebarRename(), false, "").Clicked() {
			a.layers.renaming, a.layers.name = src.ID, src.DisplayName
		}
		if a.menuItem(c, m.Item(), locales.CommonOpenInBrowser(), false, "openArrow").Clicked() {
			a.openExternal(c, src.HomeURL)
		}
		ui.Box(c).Height(1).Margin(4, 8).Background(theme.Line).AccessHidden(true)
		if a.menuItem(c, m.Item(), locales.CommonUnfollow(), true, "").Clicked() {
			a.unsubscribe(src)
		}
	})
	a.layers.menu = keepOpen(a.layers.menu, slot, open)
	return t
}

// menuPanel is a menu's look: elevated, a line edge, 12 corners,
// the floating shadow; it grows out of its trigger and leaves by fading.
func (a *App) menuPanel(c *ui.Context, panel ui.Element) ui.Element {
	return panel.AttachedOrigin().Transition(a.tr(c, motion.Menu())).ObserveInput(a.observe).
		Width(176).Padding(4).Radius(12).Background(theme.Elevated).Border(1, theme.Line).
		Shadow(0, 0, 0, 1, theme.ShadowEdge).Shadow(0, 12, 32, 0, theme.ShadowFloat)
}

// menuItem is a menu's item: the pointer gets only the faint hover ground,
// the keyboard's highlight the selected ground and the focus ring.
func (a *App) menuItem(c *ui.Context, it ui.Element, label string, danger bool, trailing string) ui.Element {
	it.Label(label).Height(32).Padding(0, 8).Radius(6).Gap(8).AlignItems(ui.Center)
	switch {
	case it.Highlighted() && a.kbd:
		it.Background(theme.Selected).FocusRing(true).FocusRingOffset(-2)
	case it.Hovered():
		it.Background(theme.Hover)
	}
	color := theme.Text
	if danger {
		color = theme.Danger
	}
	return it.Children(func() {
		ui.Text(c, label).TextColor(color)
		if trailing != "" {
			a.icon(c, trailing, 12, false)
		}
	})
}

// openExternal opens an http(s) address in the browser.
func (a *App) openExternal(c *ui.Context, raw string) {
	u, err := web.ParseURL(raw, nil)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Username != "" || u.Password != "" {
		a.fail(locales.ErrorAddressNotOpened())
		return
	}
	c.Services().OpenURL(u.Href())
}

// unsubscribe lets an author go at once; the notice offers 撤销.
func (a *App) unsubscribe(src *library.Source) {
	id, name, face := src.ID, src.DisplayName, a.iconOf(src)
	a.mutate(library.SetSubscription{ID: id, Subscribed: false}, func() {
		if cur := a.current(); cur.kind == "author" && cur.id == id {
			a.page, a.from, a.came, a.left = page{kind: "home"}, nil, nil, nil
		}
		a.notify(locales.NoticeUnfollowed(name), face, library.SetSubscription{ID: id, Subscribed: true})
	})
}

// fail shows an error notice, until it is closed.
func (a *App) fail(text string) {
	a.errText = text
	a.toast.add(toastItem{text: text, err: true})
}

func (a *App) toggleSheet(from ui.Rect) {
	if a.layers.sheet {
		a.layers.sheet = false
		return
	}
	a.layers.sheet, a.layers.opener, a.layers.in, a.layers.out = true, from, motion.PopIn, motion.PopOut
	if a.kbd {
		a.layers.opener = ui.Rect{}
	}
}

func (a *App) openSubscribe(from ui.Rect) {
	a.layers.dialog = &subscribeForm{}
	a.layers.opener, a.layers.in, a.layers.out = from, motion.DialogIn, motion.DialogOut
	if a.kbd {
		a.layers.opener = ui.Rect{}
	}
}

// buildLayers builds the dialog, the sheet and the notices.
func (a *App) buildLayers(c *ui.Context) {
	if a.warmed > 0 && a.warmed <= warmFrames && !a.opt.Sync {
		a.warmDialog(c)
	}
	open := a.layers.dialog != nil
	a.centred(c, "dialog", &open, motion.Dialog(), func(panel ui.Element) {
		// DESIGN.md dialog: elevated, 12px corners, the dialog shadow, 24 inside.
		panel.Width(480).Padding(24).Shadow(0, 0, 0, 1, theme.Line).Shadow(0, 24, 64, 0, theme.ShadowDialog).Label(locales.SubscribeTitle())
	}, func() {
		ui.Text(c, locales.SubscribeTitle()).FontSize(theme.Subtitle).FontWeight(600).LineHeight(1.4).Margin(0, 0, 8, 0)
		if f := a.layers.dialog; f != nil {
			a.subscribeView(c, f)
		}
	})
	if !open && a.layers.dialog != nil {
		if a.layers.dialog.blocking() {
			open = true // a form that is saving waits until it is done
		} else {
			a.layers.dialog = nil
		}
	}
	a.centred(c, "sheet", &a.layers.sheet, motion.Sheet(), func(panel ui.Element) {
		// A floating panel, 16 inside: the settings, the interface's language.
		panel.Width(360).Padding(16).FontSize(theme.Body).LineHeight(1.5).InsetShadow(0, 0, 0, 1, theme.Line).
			Shadow(0, 0, 0, 1, theme.ShadowEdge).Shadow(0, 12, 32, 0, theme.ShadowFloat).Label(locales.Settings())
	}, func() {
		ui.Row(c.Key("language")).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.SettingsLanguage()).FontSize(theme.Item).FontWeight(600).LineHeight(1.45).Grow(1)
			// Each language in its own words. ponytail: no way back to 跟随系统 here once one is
			// chosen (System Settings' language of the app has it); add a third choice if readers miss it.
			lang := map[bool]int{true: 0, false: 1}[strings.HasPrefix(i18n.Locale(), "zh")]
			was := lang
			a.segmented(c, "language", locales.SettingsLanguage(), []string{"中文", "English"}, &lang)
			if lang != was && a.opt.SetLocale != nil {
				a.opt.SetLocale([]string{"zh-Hans", "en"}[lang])
			}
		})
	})
	a.viewerView(c)
	a.toasts(c)
}

// centred is a centred layer over the shade: it grows out of the button that opened it and shrinks back into it, the
// shade fading on its timing. Opened from the keyboard, it is simply there.
func (a *App) centred(c *ui.Context, key string, open *bool, t ui.ElementTransition, look func(ui.Element), content func()) {
	if *open {
		ui.Overlay(c, func() {
			ui.Box(c.Key("shade-" + key)).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(theme.Shade).IgnoreInput(true).
				Transition(a.tr(c, motion.Shade(a.layers.in, a.layers.out)))
		})
	}
	ui.DialogBase(c.Key(key), open, func(back, panel ui.Element) {
		w, h := c.Size()
		back.TransformOrigin(motion.Origin(ui.Rect{W: w, H: h}, a.layers.opener)).ObserveInput(a.observe).Transition(a.tr(c, t))
		panel.Radius(12).Background(theme.Elevated).TextColor(theme.Text)
		look(panel)
		content()
	})
}

// Notices: one at a time; a notice about someone carries their
// face; 4 s for news, 8 s while it offers 撤销, errors until closed. ⌘Z
// presses 撤销.
type toastItem struct {
	text   string
	face   *siteIcon
	undo   library.LibraryAction
	err    bool
	onUndo func() // as 撤销 is pressed, before what it saves is saved
}

type toastState struct {
	queue []toastItem
	id    string
	undo  func()
}

func (t *toastState) add(it toastItem) { t.queue = append(t.queue, it) }

// notify shows a notice; undo, when not nil, is what 撤销 saves.
func (a *App) notify(text string, face *siteIcon, undo library.LibraryAction) {
	a.toast.add(toastItem{text: text, face: face, undo: undo})
}

func (a *App) toasts(c *ui.Context) {
	for _, it := range a.toast.queue {
		if a.toast.id != "" {
			c.CloseToast(a.toast.id)
		}
		t := ui.Toast{Title: it.text, Timeout: motion.NoticeFor, Data: it}
		if it.err {
			t.Type, t.Timeout = "error", -1
		}
		a.toast.undo = nil
		if it.undo != nil {
			undo, onUndo := it.undo, it.onUndo
			t.Action, t.Timeout = locales.CommonUndo(), motion.UndoFor
			run := func() {
				a.toast.undo = nil
				if onUndo != nil {
					onUndo()
				}
				a.mutate(undo, nil)
			}
			t.OnAction, a.toast.undo = run, run
		}
		a.toast.id = c.AddToast(t)
	}
	a.toast.queue = nil
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(0, 0, 24, 0).Justify(ui.End).AlignItems(ui.Center).Gap(8).Label(locales.NoticesRegion())
		for _, t := range toasts {
			it, _ := t.Data.(toastItem)
			tb := ui.ToastBase(c, t)
			color := theme.Text
			if t.Type == "error" {
				color = theme.Danger
			}
			tb.Root.Row().Gap(8).Padding(8, 8, 8, 16).MaxWidth(560).Radius(12).Background(theme.Elevated).AlignItems(ui.Center).FontSize(theme.Body).
				Shadow(0, 0, 0, 1, theme.Line).Shadow(0, 0, 0, 2, theme.ShadowEdge).Shadow(0, 12, 32, 0, theme.ShadowFloat).
				Transition(a.tr(c, motion.Toast())).Children(func() {
				if it.face != nil {
					f := a.face(c, it.face, "", 20)
					a.noticeFace = f.LayoutBounds()
					if a.tripping("notice") {
						f.Opacity(0)
					}
				}
				ui.Text(c, t.Title).TextColor(color).SingleLine().Ellipsis("…").Shrink(1)
				if t.Action != "" {
					act := tb.ActionButton().Height(24).Padding(0, 8).Radius(6).AlignItems(ui.Center).FocusRing(false)
					if act.Hovered() {
						act.Background(theme.Hover)
					}
					if act.FocusVisible() {
						act.FocusRing(true).FocusRingOffset(-2)
					}
					act.Children(func() { ui.Text(c, t.Action).TextColor(theme.Accent) })
				}
				cl := tb.CloseButton().Label(locales.NoticeClose()).Size(24, 24).Radius(6).Center().FocusRing(false)
				if cl.Hovered() {
					cl.Background(theme.Hover)
				}
				if cl.FocusVisible() {
					cl.FocusRing(true).FocusRingOffset(-2)
				}
				cl.TextColor(theme.Text2).Children(func() { a.icon(c, "close", 16, false) })
				// How long the undo stays: a hairline along the foot that runs down.
				if left, total := tb.Left(), t.Timeout; left > 0 && total > 0 {
					ui.Box(c).Absolute().Left(16).Right(16).Bottom(0).Height(1).IgnoreInput(true).Children(func() {
						ui.Box(c).Height(1).WidthPercent(100 * float32(left) / float32(total)).Background(theme.Faint).Opacity(0.6)
					})
					c.AnimationFrame()
				}
			})
		}
	})
}

func trimSpace(s string) string { return strings.TrimSpace(s) }

// trip sends a face from (a window box) onto a place read every frame: the
// notice's face ("notice") or an author's in 收藏 ("shelf:<source ID>"), as
// what they stand for is let go of and put back (letGo). It flies
// as a face flies to the sidebar (faceFlight), and the place hides until
// it lands.
func (a *App) trip(img *ui.Bitmap, from ui.Rect, to string) {
	a.trips = append(a.trips, &faceFlight{img: img, from: from, to: to})
}

func (a *App) tripping(to string) bool {
	return slices.ContainsFunc(a.trips, func(f *faceFlight) bool { return f.to == to })
}

// tripTarget is where a trip heads, as the frame before laid it out.
func (a *App) tripTarget(to string) ui.Rect {
	if to == "notice" {
		return a.noticeFace
	}
	if id, ok := strings.CutPrefix(to, "shelf:"); ok && a.page == (page{kind: "favorites"}) {
		return a.shelfFaces[id]
	}
	return ui.Rect{}
}

// trips draws the faces on their way, over the window, along faceFlight's
// arc: 600 ms for a short hop up to 900 across the window.
func (a *App) tripsView(c *ui.Context) {
	now := c.Now()
	live := a.trips[:0]
	for i, f := range a.trips {
		to := a.tripTarget(f.to)
		if f.start.IsZero() {
			if to.W <= 0 {
				// The place is not laid out yet: wait for it, a few frames at most.
				if f.tries++; f.tries < 30 {
					live = append(live, f)
					c.AnimationFrame()
				}
				continue
			}
			f.start, f.last = now, to
			f.length = time.Duration(min(900, 600+math.Hypot(float64(to.X-f.from.X), float64(to.Y-f.from.Y))*0.35)) * time.Millisecond
		}
		if to.W > 0 {
			f.last = to
		}
		t := now.Sub(f.start)
		if t >= f.length {
			continue
		}
		p := float64(motion.Flight(float32(t) / float32(f.length)))
		x0, y0 := float64(f.from.X+f.from.W/2), float64(f.from.Y+f.from.H/2)
		x1, y1 := float64(f.last.X+f.last.W/2), float64(f.last.Y+f.last.H/2)
		cx, cy := (x0+x1)/2, math.Min(y0, y1)-math.Min(120, math.Hypot(x1-x0, y1-y0)*0.18)
		x := (1-p)*(1-p)*x0 + 2*(1-p)*p*cx + p*p*x1
		y := (1-p)*(1-p)*y0 + 2*(1-p)*p*cy + p*p*y1
		size := float64(f.from.W) + float64(f.last.W-f.from.W)*p
		ui.Image(c.Key("trip-"+strconv.Itoa(i)), f.img).Fit(ui.Cover).Absolute().Left(float32(x-size/2)).Top(float32(y-size/2)).
			Size(float32(size), float32(size)).Radius(float32(size/2)).Shadow(0, 10, 24, -8, theme.ShadowDialog).IgnoreInput(true).AccessHidden(true)
		live = append(live, f)
		c.AnimationFrame()
	}
	a.trips = live
}
