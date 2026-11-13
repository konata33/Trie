package app

import (
	"math"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/motion"
	"trie/internal/theme"
	"trie/internal/web"
)

// An author's panel over Explore (SourceSheet): who they are, then their
// works or their latest posts; the subscribe pill (Follow), and the face
// flying into the sidebar's new row.

// showSheet opens an author's panel: from a card ("site"), whose surface
// becomes the panel's thumbnail, or from a byline ("who"), which the panel
// grows out of. From the keys it is simply there.
func (a *App) showSheet(c *ui.Context, e *explore.Entry, from string, opener ui.Rect) {
	ex := a.ex
	ex.run++
	ex.open = &exOpen{id: e.ID, from: from, opener: opener, kbd: a.kbd, run: ex.run, focus: true}
	ex.sheet = ex.open
	ex.closing = ""
	c.Invalidate()
}

// closeSheet closes the panel: its picture flies back into its card, whose
// words follow; the focus goes back to what opened it. From the keys it is
// simply gone.
func (a *App) closeSheet(c *ui.Context) {
	ex := a.ex
	o := ex.open
	if o == nil {
		return
	}
	ex.open = nil
	o.kbd, o.closedAt = a.kbd, c.Now()
	if o.from == "site" {
		ex.closing = o.id
		ex.landed[o.id] = c.Now()
		ex.focus = "card:" + o.id
	} else {
		ex.focus = "who:" + o.id
	}
	if o.kbd {
		ex.sheet, ex.closing = nil, ""
	}
	c.Invalidate()
}

// presence is how far a part of the panel is there: it comes over in, delay
// after the panel opens, and goes over out as it closes, along ease; from
// the keys it is simply there or gone. Only fades and blurs ride it, which
// stay under less motion.
func (a *App) presence(e ui.Element, o *exOpen, in, delay, out time.Duration, ease ui.Easing) float32 {
	switch {
	case o.kbd && o.closing():
		return 0
	case o.kbd:
		return 1
	case o.closing():
		return e.Keyframes("out", []ui.Keyframe{{Value: 1}, {At: out, Value: 0, Ease: ease}}, ui.KeyframeOptions{Run: o.run, NonMotion: true})
	}
	return e.Keyframes("in", rise(delay, in, ease), ui.KeyframeOptions{Run: o.run, NonMotion: true})
}

func (o *exOpen) closing() bool { return !o.closedAt.IsZero() }

// exploreLayer is the panel's layer over the page: the shade, and the panel
// centred in the content area.
func (a *App) exploreLayer(c *ui.Context, g exGeometry, live bool) {
	ex := a.ex
	o := ex.sheet
	if o == nil {
		return
	}
	if o.closing() && (o.kbd || c.Now().Sub(o.closedAt) >= motion.DialogOut) {
		ex.sheet = nil
		ex.closing = ""
		return
	}
	if o.closing() {
		c.After(motion.DialogOut - c.Now().Sub(o.closedAt))
	}
	e := ex.find(o.id)
	if e == nil {
		ex.sheet, ex.open = nil, nil
		return
	}
	site := o.from == "site"
	ui.Box(c.Key("layer")).Absolute().Fill().ZIndex(5).IgnoreInput(o.closing()).Children(func() {
		shade := ui.Box(c.Key("shade-" + strconv.Itoa(int(o.run)))).Absolute().Fill().Background(theme.Shade)
		shade.Opacity(a.presence(shade, o, 200*time.Millisecond, 0, 200*time.Millisecond, motion.EaseOut))
		if shade.Clicked() && !o.closing() {
			a.closeSheet(c)
		}
		// The box centring the panel lets the pointer through to the shade around it.
		ui.Box(c.Key("sheet-box")).Absolute().Fill().Padding(48).Center().PassThrough().Children(func() {
			sheet := ui.Column(c.Key("sheet-" + strconv.Itoa(int(o.run)))).Width(min(880, a.area.W-96)).MaxHeightPercent(100).IgnoreInput(o.closing()).
				Role(ui.RoleDialog).Label(e.Name)
			// Opened from a byline, the panel grows out of it and goes back into it.
			if !site {
				k := a.presence(sheet, o, motion.DialogIn, 0, motion.DialogOut, motion.Ease)
				sheet.Opacity(k)
				if !a.still(c) && k < 1 {
					b := sheet.LayoutBounds()
					ox, oy := motion.Origin(b, o.opener)
					s := 0.97 + 0.03*k
					sheet.TransformOrigin(ox, oy).Scale(s, s)
				}
			}
			sheet.Children(func() {
				// The panel's ground: from a card it comes into focus around the picture travelling into it.
				ground := ui.Box(c.Key("ground")).Absolute().Fill().ZIndex(-1).Radius(12).Background(theme.Elevated).InsetShadow(0, 0, 0, 1, theme.Line).
					Shadow(0, 24, 64, 0, theme.ShadowDialog).IgnoreInput(true)
				if site {
					k := a.presence(ground, o, motion.DialogIn, 0, motion.DialogOut, motion.Ease)
					ground.Opacity(k)
					blur := 3 * (1 - k)
					if o.closing() {
						blur = 2 * (1 - k)
					}
					if blur > 0.05 {
						ground.Blur(blur)
					}
				}
				body := ui.Scroll(c.Key("body")).Shrink(1).MinHeight(0).Scrollbars(false).Radius(12)
				body.Children(func() {
					ui.Column(c).Padding(24).Gap(24).Children(func() {
						a.sheetHead(c, e, o, site)
						list := ui.Column(c.Key("list"))
						a.sheetWords(c, list, o, 180*time.Millisecond)
						list.Children(func() { a.sheetList(c, e) })
					})
				})
			})
		})
	})
	_ = g
	_ = live
}

// sheetWords has the panel's words follow it a beat later: up from 8 below,
// coming into view; they go by fading.
func (a *App) sheetWords(c *ui.Context, e ui.Element, o *exOpen, delay time.Duration) {
	in, out := motion.PopIn, motion.SwapOut
	k := a.presence(e, o, in, delay, out, motion.Ease)
	if k < 1 {
		e.Opacity(k)
		if !o.closing() && !a.still(c) {
			e.Translate(0, 8*(1-k))
		}
	}
}

// sheetHead is who they are: their site's picture (where the card's
// surface lands), face and name, their introduction, the pill and the way
// to their site; and the close button.
func (a *App) sheetHead(c *ui.Context, e *explore.Entry, o *exOpen, site bool) {
	ex := a.ex
	tall := e.Kind == "blog"
	picks := ex.picks[e.ID]
	ui.Row(c.Key("head")).Gap(24).AlignItems(ui.Start).Children(func() {
		switch {
		case site:
			w, h := float32(224), float32(140)
			if tall {
				w, h = 150, 200
			}
			shot := ui.Box(c.Key("shot")).Size(w, h).Radius(10).Shrink(0).Shadow(0, 0, 0, 1, theme.LineSoft).Shadow(0, 16, 32, -16, theme.ShadowShot).
				Role(ui.RoleImage).Label(e.Name + " 的页面")
			shot.Children(func() {
				if o.closing() {
					return // the surface is on its way back to its card
				}
				switch {
				case tall:
					a.plate(c, e, plateOpt{sharp: a.fullShot(e), writer: true, round: 10, w: w, h: h, sheet: true})
				case len(picks) > 0:
					a.plate(c, e, plateOpt{shot: a.workPic(picks[0]), has: true, bare: true, round: 10, w: w, h: h})
				default:
					a.plate(c, e, plateOpt{shot: a.shotOf(e), has: true, sharp: a.fullShot(e), round: 10, w: w, h: h})
				}
			})
		default:
			if p := a.shotOf(e); p != nil && p.bmp != nil {
				ui.Image(c.Key("shot"), p.bmp).Fit(ui.Cover).ObjectPosition(0.5, 0).Size(224, 140).Radius(10).Shrink(0).
					Shadow(0, 0, 0, 1, theme.LineSoft).Shadow(0, 16, 32, -16, theme.ShadowShot).Label(e.Name + " 的页面")
			}
		}
		who := ui.Column(c.Key("who")).Grow(1).Shrink(1).MinWidth(0).Gap(8).Padding(4, 0, 0, 0)
		a.sheetWords(c, who, o, 120*time.Millisecond)
		who.Children(func() {
			// The face over the name, each a block of its own.
			ui.Column(c.Key("name")).AlignItems(ui.Start).Children(func() {
				a.exFace(c, e, 40, false, 11)
				ui.Text(c, e.Name).FontSize(theme.Subtitle).LineHeight(28.0 / 20).FontWeight(650).LetterSpacing(-0.01 * 20).TextColor(theme.Text).
					Role(ui.RoleHeading).Level(2).Shrink(1)
			})
			ui.Text(c, e.Intro).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2)
			ui.Row(c.Key("acts")).Gap(8).AlignItems(ui.Center).Margin(8, 0, 0, 0).Children(func() {
				a.follow(c, e, "sheet")
				b := a.button(c, "去看看", btnOpt{inset: true, key: "visit", trail: "openArrow"})
				if b.Clicked() {
					a.openExternal(c, e.HomeURL)
				}
			})
			if err := ex.errors[e.ID]; err != "" {
				ui.Text(c.Key("err"), err).FontSize(theme.Label).TextColor(theme.Danger).Role(ui.RoleAlert)
			}
		})
		closeBox := ui.Box(c.Key("close-box")).Margin(-8, -8, 0, 0)
		a.sheetWords(c, closeBox, o, 120*time.Millisecond)
		closeBox.Children(func() {
			b := a.iconBtn(c, "close", iconOpt{icon: "close", label: "关闭", tip: "关闭 · Esc"})
			if o.focus && !o.closing() {
				o.focus = false
				b.Focus()
			}
			if b.Clicked() && !o.closing() {
				a.closeSheet(c)
			}
		})
	})
}

// shotOf is what a site looks like: its small capture, or for a
// gallery without one, its newest work.
func (a *App) shotOf(e *explore.Entry) *exPic {
	if p := a.ex.pics["thumb:"+e.ID]; p != nil && p.bmp != nil {
		return p
	}
	if len(e.Works) > 0 {
		return a.workPic(exWork{entry: e, index: 0, title: e.Works[0].Title, url: e.Works[0].URL, publishedAt: e.Works[0].PublishedAt})
	}
	return nil
}

// sheetList is their works or their latest posts; with nothing of theirs
// read yet, nothing: the panel ends at its head, whose link goes to their site.
func (a *App) sheetList(c *ui.Context, e *explore.Entry) {
	ex := a.ex
	var works []exWork
	for _, w := range ex.works {
		if w.entry.ID == e.ID {
			works = append(works, w)
		}
	}
	heading := func(s string) {
		ui.Text(c.Key("h"), s).FontSize(theme.Label).FontWeight(500).TextColor(theme.Muted).Margin(0, 0, 12, 0).Role(ui.RoleHeading).Level(3)
	}
	switch {
	case len(works) > 0:
		heading("最近收录")
		inner := min(880, a.area.W-96) - 48
		fw := (inner - 3*12) / 4
		ui.Column(c.Key("works")).Gap(12).Children(func() {
			for row := 0; row*4 < len(works); row++ {
				ui.Row(c.Key(row)).Gap(12).Children(func() {
					for col := range 4 {
						if i := row*4 + col; i < len(works) {
							a.frame(c, works[i], fw, fw*10/16)
						}
					}
				})
			}
		})
	case len(e.Posts) > 0:
		heading("最近的文章")
		// A hairline over each row but the first; gone over the row pointed at,
		// and over the one before it.
		hov := -1
		for i, p := range e.Posts {
			if p.URL == ex.hoverPost {
				hov = i
			}
		}
		ui.Column(c.Key("posts")).Role(ui.RoleList).Children(func() {
			for i, p := range e.Posts {
				row := ui.Box(c.Key(p.URL)).Role(ui.RoleListItem)
				row.Children(func() {
					b := ui.ButtonBase(c.Key("post")).Label(p.Title).Row().AlignItems(ui.End).Justify(ui.Start).Gap(16).Margin(0, -12).Padding(8, 12).Radius(6).FocusRing(false)
					a.reach(c, b, e.ID, p.URL)
					if b.FocusVisible() {
						b.FocusRing(true)
					}
					bg := ui.Transparent
					if b.Hovered() {
						bg = theme.Selected
					}
					b.Background(b.AnimateColor("bg", bg, a.ease(140*time.Millisecond)))
					if i > 0 && i != hov && i+1 != hov {
						b.Shadow(0, -1, 0, 0, theme.LineSoft)
					}
					style := func(c *ui.Context, s string) ui.Element {
						return ui.Text(c, s).FontSize(theme.Item).LineHeight(22.0 / 15).FontWeight(500).TextColor(theme.Text)
					}
					var title ui.Element
					b.Children(func() {
						title = style(c, p.Title).Grow(1).Shrink(1).MinWidth(0)
						a.end(title, page{kind: "explore"}, "title:"+trialID(p.URL))
						if p.PublishedAt != nil && *p.PublishedAt != "" {
							ui.Text(c, dateLabel(p.PublishedAt)).FontSize(theme.Label).TextColor(theme.Muted).Shrink(0).NoWrap()
						}
					})
					if b.Clicked() {
						a.openPost(c, e, p, &trialFrom{title: title.LayoutBounds(), button: b.LayoutBounds(), style: style, size: theme.Item})
					}
				})
			}
		})
	}
}

// --- Subscribing: the author's face and one word, a real button that becomes
// quiet once pressed (Follow). Their face is what flies to the sidebar.

// subscribedSource is the library source subscribed to an entry, if any:
// the library wins over the list Explore loaded with.
func (a *App) subscribedSource(e *explore.Entry) string {
	for i := range a.lib.Sources {
		s := &a.lib.Sources[i]
		if !s.Subscribed {
			continue
		}
		if e.SubscribedSourceID != nil && s.ID == *e.SubscribedSourceID || e.FeedURL != nil && *e.FeedURL != "" && s.FeedURL != nil && *s.FeedURL == *e.FeedURL ||
			exHost(s.HomeURL) == exHost(e.HomeURL) {
			return s.ID
		}
	}
	return ""
}

func exHost(u string) string {
	p, err := web.ParseURL(u, nil)
	if err != nil {
		return ""
	}
	h := p.Hostname()
	if len(h) > 4 && h[:4] == "www." {
		h = h[4:]
	}
	return h
}

// pillState is a pill's own: its width as it changes shape, and whether it
// was just pressed.
type pillState struct {
	on, working bool
	changedAt   time.Time
}

// follow is the subscribe pill of an entry, at where ("sheet", "bar" or
// "foot"): amber with the face and 订阅; once subscribed, quiet with a tick
// and 已订阅, offering 取消订阅 only when pointed at. A request answering
// within 150 ms shows no spinner.
func (a *App) follow(c *ui.Context, e *explore.Entry, where string) ui.Element {
	if e.FeedURL == nil || *e.FeedURL == "" {
		return ui.Element{}
	}
	ex := a.ex
	key := where + ":" + e.ID
	on := a.subscribedSource(e) != ""
	busy := ex.busy == e.ID
	now := c.Now()
	working := busy && now.Sub(ex.busyAt) >= 150*time.Millisecond
	if busy && !working {
		c.After(150*time.Millisecond - now.Sub(ex.busyAt))
	}
	st := ex.pills[key]
	if st == nil {
		st = &pillState{on: on, working: working}
		ex.pills[key] = st
	}
	if st.on != on || st.working != working {
		st.on, st.working, st.changedAt = on, working, now
	}
	label := "订阅 " + e.Name
	switch {
	case working && on:
		label = "正在取消订阅 " + e.Name
	case working:
		label = "正在订阅 " + e.Name
	case on:
		label = "已订阅 " + e.Name + "，按下取消订阅"
	}
	b := ui.ButtonBase(c.Key("follow-" + key)).Label(label).Role(ui.RoleToggleButton).Checked(on).Busy(working).Height(32).Radius(16).
		AlignItems(ui.Center).ClipX().FocusRing(false).Shrink(0)
	hover := b.Hovered() && !busy
	if !hover {
		delete(ex.fresh2, key)
	}
	alt := on && !busy && (hover && !ex.fresh2[key] || b.FocusVisible())
	// One pill changing shape: its width runs from the old words to the new
	// while the colours cross and the words come into focus.
	t := ui.ElementTransition{Duration: motion.State, Ease: motion.Ease, Width: true}
	if a.kbd || a.still(c) || now.Sub(st.changedAt) > motion.State+50*time.Millisecond || st.changedAt.IsZero() {
		t = motion.Instant(t)
	}
	b.Transition(t)
	ground, fg, weight := theme.Accent, theme.OnAccent, 600
	padL := float32(4)
	if on {
		ground, fg, weight, padL = theme.Selected, theme.Text2, 500, 12
		if hover {
			ground, fg = theme.FollowHover, theme.Text
		}
	} else if hover {
		ground = brighten(theme.Accent, 1.06)
	}
	if alt {
		fg = theme.Text
	}
	// The padding moves with the width; under less motion, or from the keys, it is simply there.
	var pad float32
	if a.kbd || a.still(c) {
		pad = b.Jump("padL", padL)
	} else {
		pad = b.AnimateTo("padL", padL, ui.Animation{Duration: motion.State, Ease: motion.Ease})
	}
	b.Padding(0, 16, 0, pad).Background(b.AnimateColor("bg", ground, a.ease(motion.State))).
		TextColor(b.AnimateColor("fg", fg, a.ease(motion.State))).FontSize(theme.Body).FontWeight(weight)
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	if !busy {
		a.press(b, motion.Squashed)
	}
	b.Children(func() {
		words := ui.Row(c.Key(map[bool]string{true: "on", false: "off"}[on])).Gap(8).AlignItems(ui.Center).Shrink(0)
		if !a.kbd {
			k := words.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: 200 * time.Millisecond, Value: 1, Ease: motion.Ease}}, ui.KeyframeOptions{NonMotion: true})
			if k < 1 {
				words.Opacity(k)
				if bl := 2 * (1 - k); bl > 0.05 {
					words.Blur(bl)
				}
			}
		}
		words.Children(func() {
			slot := ui.Box(c.Key("slot")).Size(12, 24).Center().Shrink(0)
			switch {
			case on && alt:
				slot.Children(func() { a.icon(c, "close", 12, false) })
				ui.Text(c, "取消订阅").NoWrap()
			case on:
				slot.Children(func() {
					if working {
						a.loading(c, 12)
					} else {
						a.icon(c, "check", 12, false)
					}
				})
				ui.Text(c, map[bool]string{true: "取消订阅", false: "已订阅"}[working]).NoWrap()
			default:
				slot.Width(24).Children(func() {
					if working {
						a.loading(c, 16)
						return
					}
					f := a.exFace(c, e, 24, false, 11).Shadow(0, 0, 0, 1, theme.OnAccent.Alpha(0.08))
					ex.faces[key] = f.LayoutBounds()
					if a.flyingTo("pill:" + key) {
						f.Opacity(0)
					}
				})
				ui.Text(c, "订阅").NoWrap()
			}
		})
	})
	if b.Clicked() && !busy {
		ex.fresh2[key] = true
		a.toggleFollow(c, e, key)
	}
	return b
}

// brighten is CSS's brightness(k) on a colour.
func brighten(c ui.Color, k float32) ui.Color {
	f := func(v uint8) uint8 { return uint8(min(255, float32(v)*k+0.5)) }
	return ui.Color{R: f(c.R), G: f(c.G), B: f(c.B), A: c.A}
}

// toggleFollow subscribes, or lets go when subscribed (toggleSubscription).
// The face travels with it: subscribing, it flies from the pill into the
// new sidebar row; letting go, it leaves the row and flies back onto the
// pill that subscribes again.
func (a *App) toggleFollow(c *ui.Context, e *explore.Entry, pill string) {
	ex := a.ex
	if ex.busy != "" {
		return
	}
	ex.busy, ex.busyAt = e.ID, c.Now()
	delete(ex.errors, e.ID)
	id, name := e.ID, e.Name
	var icon *exPic
	if p := ex.pics["icon:"+e.ID]; p != nil && p.bmp != nil {
		icon = p
	}
	moving := icon != nil && !a.still(c) && !a.kbd
	store, subs := a.opt.Store, a.opt.Subscriptions
	if cur := a.subscribedSource(e); cur != "" {
		from := a.chipAt[cur]
		a.do(func() func() {
			lib, err := store.Mutate(library.SetSubscription{ID: cur, Subscribed: false})
			return func() {
				ex.busy = ""
				if err != nil {
					a.followFailed(id, err)
					return
				}
				a.accept(lib)
				ex.live = "已取消订阅 " + name
				if moving && from.W > 0 {
					a.faceLeft[cur] = true
					a.flights2 = append(a.flights2, &faceFlight{img: icon.bmp, from: from, to: "pill:" + pill})
				}
			}
		})
		return
	}
	from := ex.faces[pill]
	feed := *e.FeedURL
	if subs == nil {
		ex.busy = ""
		return
	}
	a.do(func() func() {
		p, err := subs.Preview(bg, feed)
		var res library.SubscriptionResult
		if err == nil {
			res, err = subs.Add(p.Token, true)
		}
		return func() {
			ex.busy = ""
			if err != nil {
				a.followFailed(id, err)
				return
			}
			a.accept(res.Library)
			ex.live = "已订阅 " + name
			if moving && from.W > 0 {
				a.flights2 = append(a.flights2, &faceFlight{img: icon.bmp, from: from, to: "chip:" + res.SourceID, light: true})
			}
		}
	})
}

// followFailed says why a subscription did not go through: in the panel
// under the pill, or as a notice from the reader.
func (a *App) followFailed(id string, err error) {
	if a.page.kind == "reader" {
		a.fail(err.Error())
		return
	}
	a.ex.errors[id] = err.Error()
}

// faceFlight is an author's face travelling from one place to another
// along a gentle arc: the place it heads for is read
// every frame, so a sidebar row still growing, or a pill still changing
// shape, is met where it is; that place stays hidden until the face lands,
// then lights once. The farther it goes, the longer: 600 ms for a short
// hop up to 900 across the window.
type faceFlight struct {
	img    *ui.Bitmap
	from   ui.Rect
	to     string // "chip:<source ID>" or "pill:<pill>"
	light  bool
	start  time.Time
	length time.Duration
	last   ui.Rect
	tries  int
}

// flyingTo tells whether a face is on its way to target, which hides until
// it lands.
func (a *App) flyingTo(target string) bool {
	for _, f := range a.flights2 {
		if f.to == target {
			return true
		}
	}
	return false
}

// target is where a flight is heading, as the frame before laid it out.
func (a *App) target(to string) ui.Rect {
	if len(to) > 5 && to[:5] == "chip:" {
		return a.chipAt[to[5:]]
	}
	if len(to) > 5 && to[:5] == "pill:" {
		return a.ex.faces[to[5:]]
	}
	return ui.Rect{}
}

// faceFlights draws the faces on their way, over the window.
func (a *App) faceFlights(c *ui.Context) {
	now := c.Now()
	live := a.flights2[:0]
	for i, f := range a.flights2 {
		to := a.target(f.to)
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
			if f.light {
				a.lights[f.to] = now
			}
			continue
		}
		p := float64(motion.Flight(float32(t) / float32(f.length)))
		x0, y0 := float64(f.from.X+f.from.W/2), float64(f.from.Y+f.from.H/2)
		x1, y1 := float64(f.last.X+f.last.W/2), float64(f.last.Y+f.last.H/2)
		// The arc rises a little above the higher end, more for a longer way, never more than 120.
		cx, cy := (x0+x1)/2, math.Min(y0, y1)-math.Min(120, math.Hypot(x1-x0, y1-y0)*0.18)
		x := (1-p)*(1-p)*x0 + 2*(1-p)*p*cx + p*p*x1
		y := (1-p)*(1-p)*y0 + 2*(1-p)*p*cy + p*p*y1
		size := float64(f.from.W) + float64(f.last.W-f.from.W)*p
		ui.Image(c.Key("face-flight-"+strconv.Itoa(i)), f.img).Fit(ui.Cover).Absolute().Left(float32(x-size/2)).Top(float32(y-size/2)).
			Size(float32(size), float32(size)).Radius(float32(size/2)).Shadow(0, 10, 24, -8, theme.ShadowDialog).IgnoreInput(true).AccessHidden(true)
		live = append(live, f)
		c.AnimationFrame()
	}
	a.flights2 = live
}

// landed draws the light over a place a face just landed on: once, bright,
// fading over 360 ms. It is light, not movement.
func (a *App) landedLight(c *ui.Context, target string, radius float32) {
	at, ok := a.lights[target]
	if !ok {
		return
	}
	k := float32(c.Now().Sub(at)) / float32(360*time.Millisecond)
	if k >= 1 {
		delete(a.lights, target)
		return
	}
	ui.Box(c).Absolute().Fill().Radius(radius).Background(theme.Text).Opacity(0.35 * (1 - motion.Ease(k))).IgnoreInput(true)
	c.AnimationFrame()
}

// --- Opening a post: Explore's posts are read in place, outside the
// library (the reader's preview).

// trialFrom is where a post was opened from: its title, which travels to
// the reader's headline, drawn as style draws it at size, and its button,
// whose ground spreads into the page.
type trialFrom struct {
	title, button ui.Rect
	style         func(*ui.Context, string) ui.Element
	size          float32
}

// reach starts reading a post once the pointer rests on it, or the focus
// does, for 250 ms, so the reader usually opens with the text ready; a
// pointer sweeping across the page starts nothing.
func (a *App) reach(c *ui.Context, b ui.Element, entryID, url string) {
	ex := a.ex
	pf := &ex.prefetch
	if b.Hovered() {
		ex.hoverPost = url
	} else if ex.hoverPost == url {
		ex.hoverPost = ""
	}
	// Back from the reader by the keys, the focus goes back to the post.
	if ex.focus == "post:"+url {
		ex.focus = ""
		b.Focus()
	}
	if b.Hovered() || b.Focused() {
		if pf.url != url {
			pf.url, pf.entry, pf.at = url, entryID, c.Now()
		}
	} else if pf.url == url {
		pf.url = ""
	}
}

func (a *App) prefetchPost(c *ui.Context) {
	ex := a.ex
	pf := &ex.prefetch
	if pf.url == "" || pf.done[pf.url] || a.opt.Explore == nil {
		return
	}
	if wait := restBeforeRead - c.Now().Sub(pf.at); wait > 0 {
		c.After(wait)
		return
	}
	if pf.done == nil {
		pf.done = map[string]bool{}
	}
	url, entry, svc := pf.url, pf.entry, a.opt.Explore
	pf.done[url] = true
	a.do(func() func() {
		content, err := svc.Read(bg, entry, url, "")
		return func() {
			if err != nil || content.Status == "unavailable" {
				delete(pf.done, url)
			}
		}
	})
}

// exploreKeys are Explore's: Esc closes the open panel, at once.
func (a *App) exploreKeys(c *ui.Context) {
	if a.ex.open != nil && c.Shortcut(0, ui.KeyEscape) {
		a.kbd = true
		a.closeSheet(c)
	}
}
