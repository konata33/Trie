package app

import (
	"math"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/theme"
	"trie/internal/web"
)

// Subscribing from Explore, the reader and an author's page (Follow): the
// pill, and the face flying into the sidebar's new row; and reading
// Explore's posts in place.

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

// pillState is a pill's own: what it showed last (on, working, offering
// 取消订阅) and since when, as its width runs from the old words to the new;
// and when it was last subscribed to here, for the tick to gather.
type pillState struct {
	on, working, alt bool
	changedAt, litAt time.Time
}

// follow is the subscribe pill of an entry on their page's head (DESIGN.md
// Buttons 订阅胶囊). Amber is what is the reader's own, so it comes only
// once they are: not yet subscribed, a quiet pill with their face and 订阅;
// while the request runs, past 150 ms, the words give way to the reading
// mark; subscribed, the face flies into their new sidebar row, and the dots
// it leaves behind gather into an amber tick before 已订阅, on no ground;
// pointed at, or with the keys on it, it is a pill again, offering 取消订阅.
// Its name stays 订阅 <name>; whether it is on is its checked state.
func (a *App) follow(c *ui.Context, e *explore.Entry, where string) ui.Element {
	if e.FeedURL == nil || *e.FeedURL == "" {
		return ui.Element{}
	}
	ex := a.ex
	key := where + ":" + e.ID
	on := a.subscribedSource(e) != ""
	busyAt, busy := ex.busy[e.ID]
	now := c.Now()
	working := busy && now.Sub(busyAt) >= 150*time.Millisecond
	if busy && !working {
		c.After(150*time.Millisecond - now.Sub(busyAt))
	}
	b := ui.ButtonBase(c.Key("follow-" + key)).Label(locales.ExploreFollowWriter(e.Name)).Role(ui.RoleToggleButton).Checked(on).Busy(working).
		Height(32).Radius(16).AlignItems(ui.Center).ClipX().FocusRing(false).Shrink(0)
	pointed := b.Hovered() && !a.kbd
	if !pointed {
		delete(ex.fresh2, key) // pressed, it says 已订阅 until the pointer leaves it once
	}
	alt := on && (busy || pointed && !ex.fresh2[key] || b.FocusVisible())
	st := ex.pills[key]
	if st == nil {
		st = &pillState{on: on, working: working, alt: alt}
		ex.pills[key] = st
	}
	if st.on != on || st.working != working || st.alt != alt {
		if on && !st.on {
			st.litAt = now
		}
		st.on, st.working, st.alt, st.changedAt = on, working, alt, now
	}
	// One pill changing shape: its width runs from the old words to the new
	// while the colours cross and the words come into focus.
	t := ui.ElementTransition{Duration: motion.State, Ease: motion.Ease, Width: true}
	if a.kbd || a.still(c) || now.Sub(st.changedAt) > motion.State+50*time.Millisecond || st.changedAt.IsZero() {
		t = motion.Instant(t)
	}
	b.Transition(t)
	ground, fg := theme.Selected, theme.Text
	padL := float32(4)
	switch {
	case alt && a.pressing(b):
		ground, padL = theme.Pressed, 12
	case alt:
		ground, padL = theme.FollowHover, 12
	case on:
		ground, fg, padL = ui.Transparent, theme.Text2, 12
	case busy:
	case a.pressing(b):
		ground = theme.Pressed
	case pointed:
		ground = theme.FollowHover
	}
	// The padding moves with the width; under less motion, or from the keys, it is simply there.
	var pad float32
	if a.kbd || a.still(c) {
		pad = b.Jump("padL", padL)
	} else {
		pad = b.AnimateTo("padL", padL, ui.Animation{Duration: motion.State, Ease: motion.Ease})
	}
	b.Padding(0, 16, 0, pad).Background(b.AnimateColor("bg", ground, a.ease(motion.Hover))).
		TextColor(b.AnimateColor("fg", fg, a.ease(motion.Hover))).FontSize(theme.Body).FontWeight(500)
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	look := "off"
	switch {
	case alt:
		look = "alt"
	case on:
		look = "on"
	}
	b.Children(func() {
		words := ui.Row(c.Key(look)).Gap(8).AlignItems(ui.Center).Shrink(0)
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
			switch look {
			case "alt":
				ui.Box(c.Key("slot")).Size(14, 24).Center().Shrink(0).Children(func() { a.icon(c, "close", 12, false) })
				a.pillWords(c, locales.CommonUnfollow(), working)
			case "on":
				// The dots the face left gather into the tick as it flies.
				k := float32(1)
				if since := now.Sub(st.litAt); !st.litAt.IsZero() && !a.kbd && !a.still(c) && since < motion.Flow {
					k = float32(since) / float32(motion.Flow)
					c.AnimationFrame()
				}
				dots := flowed(leftDots(), k)
				ui.Box(c.Key("tick")).Size(14, 24).Shrink(0).AccessHidden(true).
					Draw(func(p *ui.Painter, r ui.Rect) {
						paintDots(p, ui.Rect{X: r.X, Y: r.Y + 5, W: 14, H: 14}, dots, dotLight{}, 0)
					})
				ui.Text(c, locales.ExploreFollowed()).NoWrap()
			default:
				slot := ui.Box(c.Key("slot")).Size(24, 24).Center().Shrink(0)
				slot.Children(func() {
					f := a.exFace(c, e, 24, false, 11)
					ex.faces[key] = f.LayoutBounds()
					if a.flyingTo("pill:" + key) {
						f.Opacity(0) // on its way back from the sidebar
					}
				})
				a.pillWords(c, locales.CommonFollow(), working)
			}
		})
	})
	if b.Clicked() && !busy {
		ex.fresh2[key] = true
		a.toggleFollow(c, e, key)
	}
	return b
}

// pillWords are a pill's words, which give way to the reading mark in their
// place while it works, the pill keeping its width (DESIGN.md Buttons).
func (a *App) pillWords(c *ui.Context, text string, working bool) {
	ui.Box(c.Key("words")).Shrink(0).Children(func() {
		t := ui.Text(c, text).NoWrap()
		o := t.AnimateTo("o", map[bool]float32{false: 1}[working], a.ease(motion.SwapOut))
		if o < 1 {
			t.Opacity(o)
		}
		if working {
			ui.Box(c.Key("mark")).Absolute().Fill().Center().Opacity(1 - o).Children(func() { a.loading(c, 12) })
		}
	})
}

// leftDots are the dots a face leaves in its place in the subscribe pill as
// it flies to the sidebar: a faint ring, as wide as the tick they gather into
// (flowed).
func leftDots() []inkDot {
	const n = 7
	out := make([]inkDot, n)
	for i := range out {
		s, c := math.Sincos(math.Pi * (0.75 + 2*float64(i)/n))
		out[i] = inkDot{x: 0.5 + 0.38*float32(c), y: 0.5 + 0.38*float32(s), r: checkDot, a: 0.5, c: theme.Text2}
	}
	return out
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
	if _, ok := ex.busy[e.ID]; ok {
		return
	}
	if ex.busy == nil {
		ex.busy = map[string]time.Time{}
	}
	ex.busy[e.ID] = c.Now()
	name := e.Name
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
				delete(ex.busy, e.ID)
				if err != nil {
					a.fail(locales.ExploreUnfollowFailed(name, err.Error()))
					return
				}
				a.accept(lib)
				ex.live = locales.NoticeUnfollowed(name)
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
		delete(ex.busy, e.ID)
		return
	}
	a.do(func() func() {
		p, err := subs.Preview(bg, feed)
		var res library.SubscriptionResult
		if err == nil {
			res, err = subs.Add(p.Token, true)
		}
		return func() {
			delete(ex.busy, e.ID)
			if err != nil {
				// The service's own sentence says why and what to do; the notice says of whom.
				a.fail(locales.ExploreFollowFailed(name, said(err, locales.SubscribeCantRead())))
				return
			}
			a.accept(res.Library)
			ex.live = locales.NoticeFollowing(name)
			if moving && from.W > 0 {
				a.flights2 = append(a.flights2, &faceFlight{img: icon.bmp, from: from, to: "chip:" + res.SourceID, light: true})
			}
		}
	})
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
	// Where they show (Bounds): Explore's rooms slide on a track (Translate), which a layout's box leaves out.
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
