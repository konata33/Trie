package app

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/reading"
	"trie/internal/theme"
	"trie/internal/web"
)

// The reader: the bar at the top, the headline and the byline, the body
// internal/reader shows, its headings as stepping stones (stones.go), and the
// bar that clips a selection.

// readerPage is an article's reader, kept while the session lasts.
type readerPage struct {
	id      string
	content *library.ArticleContent
	work    *library.GalleryWork
	r       *reader.Reader
	loading bool
	err     string
	req     int
	waited  time.Time // the body kept the reader waiting since
	arrived time.Time // and came then

	away, lifted bool // the bar: sent up and away, lifted off the page
	left         bool // not shown since it was left, or yet: the bar starts over
	focus        bool // the reader is to take the keyboard focus
	lastY        float32
	run          float32   // how far this scroll has gone one way
	runAt        time.Time // when it last moved
	barY         float32   // where the bar is, sent away or not

	// The screenshot the strip's amber line went to last, and whether it
	// went back along the strip to it (workStrip).
	markAt   int
	markBack bool

	measured reader.Position
	saveAt   time.Time
	restored bool

	aa, more              bool    // the reading options open; the open button's menu open
	optW                  float32 // the reading options' width as laid out, which their row opens to
	progW, shareW, digitW float32 // the progress's, its share's and a digit's widths as laid out, which they open to
	feedback              string
	feedbackAt            time.Time

	// A work's pictures fetched after the read (work.go): those on their way, and those just landed.
	lateAsked bool
	lateWait  map[reading.Part]bool
	lateAt    map[reading.Part]time.Time
	rec       recording // and its recording, as it plays
	// The picture its first screen shows once one is picked (stagePic's s), and when: picked by the pointer, it grows up
	// out of the strip (flies) from pickFrom, its place there from the first screen's corner; followed from the viewer's
	// steps, the zero time. stageBox is the first screen in the window, as the frame before laid it out.
	show     int
	picked   bool
	pickAt   time.Time
	flies    bool
	pickFrom ui.Rect
	stageBox ui.Rect

	lit         float32 // the moon as the article was opened, for its row's to wane from
	foot        int     // the heading being read, under foot at the top edge (stones.go); -1 for none
	stonePlaces []place // where its headings were, kept while the text settles (stones.go)
	barOp       float32 // how opaque the bar is, sent away or not

	selKey   string    // the selection, to see it settle
	selAt    time.Time // when it last changed
	selHide  string    // a selection whose bar Esc closed
	clipBusy bool
}

// reading is article id's reader, its body asked for if it is not there.
func (a *App) reading(id string) *readerPage {
	rp := a.readers[id]
	if rp == nil {
		rp = &readerPage{id: id, left: true, foot: -1}
		a.readers[id] = rp
	}
	if rp.content == nil && !rp.loading && rp.err == "" {
		a.loadReader(rp, false, "")
	}
	return rp
}

// loadReader reads the article (internal/reading: the library while it is
// current, else the page), and lays out its body off the window's thread.
func (a *App) loadReader(rp *readerPage, refresh bool, source string) {
	rp.req++
	req := rp.req
	rp.loading, rp.err = true, ""
	if rp.content == nil && rp.waited.IsZero() {
		rp.waited = time.Now()
	}
	svc, id := a.opt.Reading, rp.id
	art := a.article(id)
	if art == nil {
		rp.loading, rp.err = false, locales.ReaderNotInLibrary()
		return
	}
	src := nameOf(a.sourceOf(art))
	meta := &reader.Article{ID: art.ID, Title: art.Title, URL: art.URL, Authors: art.Authors, SourceName: src}
	if art.PublishedAt != nil {
		meta.PublishedAt = *art.PublishedAt
	}
	if art.ModifiedAt != nil {
		meta.ModifiedAt = *art.ModifiedAt
	}
	read := func() (library.ArticleContent, error) { return svc.Read(bg, id, refresh, source) }
	if t := a.trials[id]; t != nil {
		read = a.trialRead(t, refresh, source)
	}
	a.do(func() func() {
		content, err := read()
		var rc *reader.Content
		var body *reader.Body
		var work *library.GalleryWork
		if err == nil {
			data, _ := json.Marshal(content)
			if rc, err = reader.FromJSON(data); err == nil {
				rc.Message = storedText(rc.Message) // the reader's own header shows it
				body = reader.Prepare(rc)
			}
			if len(content.Work) > 0 && string(content.Work) != "null" {
				work = &library.GalleryWork{}
				if json.Unmarshal(content.Work, work) != nil {
					work = nil
				}
			}
		}
		return func() {
			if req != rp.req {
				return // another read was asked for meanwhile
			}
			rp.loading = false
			if err != nil {
				rp.err = err.Error()
				return
			}
			rp.content, rp.work, rp.lateAsked = &content, work, false
			old := rp.r
			rp.r = reader.NewReader(meta, rc, body)
			rp.r.Size = a.fontSize
			if old != nil {
				rp.r.Scroll = old.Scroll // an updated body keeps the reader's place
			} else {
				a.restore(rp)
			}
			if !rp.waited.IsZero() && time.Since(rp.waited) > motion.Arrived {
				rp.arrived = time.Now()
			}
			rp.waited = time.Time{}
		}
	})
}

// restore puts the reader back where it was left: in this session, else
// as the library kept it, for this version of the body.
func (a *App) restore(rp *readerPage) {
	pos, ok := a.positions[rp.id]
	if !ok {
		art := a.article(rp.id)
		if art == nil || art.ReadingPosition == nil {
			rp.restored = true
			return
		}
		p := art.ReadingPosition
		pos = reader.Position{ArticleID: p.ArticleID, ContentVersion: p.ContentVersion, UnitID: p.UnitID, Offset: float32(p.Offset),
			ScrollTop: float32(p.ScrollTop), Progress: float32(p.Progress)}
	}
	if pos.ContentVersion == rp.r.ContentVersion() {
		rp.r.RestorePosition(pos)
	}
}

// keepPosition measures where the reader is and saves it half a second
// after it stops changing.
func (a *App) keepPosition(c *ui.Context, rp *readerPage) {
	r := rp.r
	if r == nil {
		return
	}
	if !rp.restored {
		rp.restored = !r.Restoring() && r.Scroll.MaxY > 0
		return
	}
	pos := r.Position()
	// The progress too: a frame whose body is not laid out yet (a jump far down) measures none, and the next, at
	// the same scroll, must not keep it.
	if m := rp.measured; pos.ScrollTop != m.ScrollTop || pos.ContentVersion != m.ContentVersion || pos.Progress != m.Progress {
		rp.measured = pos
		a.positions[rp.id] = pos
		rp.saveAt = c.Now().Add(motion.PositionSave)
		c.After(motion.PositionSave)
	}
	if !rp.saveAt.IsZero() && !c.Now().Before(rp.saveAt) {
		a.savePosition(rp)
	}
}

// savePosition writes the reader's place to the library at once.
func (a *App) savePosition(rp *readerPage) {
	if rp.saveAt.IsZero() || a.libArticle(rp.id) == nil {
		rp.saveAt = time.Time{}
		return
	}
	rp.saveAt = time.Time{}
	pos := rp.measured
	in := library.ReadingPositionInput{ArticleID: pos.ArticleID, ContentVersion: pos.ContentVersion, UnitID: pos.UnitID,
		Offset: float64(pos.Offset), ScrollTop: float64(pos.ScrollTop), Progress: float64(pos.Progress)}
	store := a.opt.Store
	a.do(func() func() {
		err := store.SaveReadingPosition(in)
		return func() {
			if err != nil {
				a.say(rp, locales.ReaderPositionNotSaved())
				return
			}
			// The lists see it at once.
			if art := a.article(in.ArticleID); art != nil {
				art.ReadingPosition = &library.ReadingPosition{ReadingPositionInput: in, UpdatedAt: web.Now()}
			}
		}
	})
}

// leaveReader keeps the place of a reader being left.
func (a *App) leaveReader(left, to page) {
	if left.kind != "reader" || left == to {
		return
	}
	if rp := a.readers[left.id]; rp != nil {
		if rp.r != nil && rp.restored {
			a.savePosition(rp)
		}
		rp.aa, rp.more, rp.left = false, false, true
		// Left by the pointer, the row's moon goes from where it was to
		// where the reader left it.
		if !a.kbd {
			a.moonBack = &moonBack{id: left.id, from: rp.lit}
		}
	}
}

func (a *App) say(rp *readerPage, text string) { rp.feedback, rp.feedbackAt = text, time.Time{} }

// readerPage builds article id's page, whose back button goes to the last of
// from; live is false for the page left, built on for its exit.
func (a *App) readerPage(c *ui.Context, id string, from []page, live bool) {
	art := a.article(id)
	if art == nil {
		return
	}
	rp := a.readers[id]
	if rp == nil {
		rp = a.reading(id)
	}
	// Back on the page, the bar starts over, there at once; while the page
	// is left it stays as it was.
	fresh := live && rp.left
	if fresh {
		rp.left, rp.away, rp.run, rp.focus = false, false, 0, true
		rp.lit = litOf(art)
	}
	// Arrived, the reader takes the focus, so that the keys scrolling it
	// are the keyboard's wherever the pointer is.
	if live && rp.focus && rp.r != nil {
		rp.r.Focus, rp.focus = true, false
	}
	p := page{"reader", id}
	src := a.sourceOf(art)
	col := a.column()
	sw := ui.Column(c.Key("page")).Grow(1).FillWidth().MinHeight(0).Role(ui.RoleRegion).Label(locales.ReaderRegion())
	sw.Children(func() {
		if r := rp.r; r != nil {
			r.Width, r.Size, r.Live = a.measure(c, sw, live)
			r.Flow = r.Live
			r.Bar = func() {
				a.readerBar(c, rp, art, from, fresh)
				r.Width, r.Size, r.Live = a.measure(c, sw, live) // as its reading options left them
				r.Flow = r.Live
			}
			r.Above = func() float32 { return a.readerHead(c, rp, art, src, p) }
			// A work's facts go before the body's first h2, or after it.
			r.Insert = nil
			if w := rp.work; w != nil && hasWorkFacts(w) {
				r.Insert = func() (float32, float32) { return a.workFacts(c, w) }
			}
			r.OnLink = func(href string) { a.openExternal(c, href) }
			r.OnRetry = func() { a.loadReader(rp, true, "") }
			r.Button = func(c *ui.Context, label string) ui.Element { return a.button(c, label, btnOpt{}) }
			r.Missing = func(c *ui.Context) ui.Element { return a.pictogram(c, "picture") }
			r.Instant = a.kbd
			r.Name = locales.ReaderTextName(titleOf(art.Title))
			a.pictures(c, rp, live)
			r.SetMarks(a.marksOf(id))
			r.View(c)
		} else {
			// No body yet: the same column, the bar, the head and the waiting or
			// failed state.
			ui.Scroll(c.Key(id)).Grow(1).FillWidth().Scrollbars(false).Children(func() {
				ui.Column(c).FillWidth().MaxWidth(col+80).AlignSelf(ui.Center).Padding(0, 40, 180, 40).Children(func() {
					a.readerBar(c, rp, art, from, fresh)
					a.readerHead(c, rp, art, src, p)
				})
			})
		}
		if live && rp.r != nil {
			a.stoneView(c, rp)
		}
	})
	if live {
		if rp.r != nil {
			a.scrolling = &rp.r.Scroll
			a.keepPosition(c, rp)
			a.trackBar(c, rp)
		} else {
			a.scrolling = nil
		}
	}
	if live && rp.r != nil && a.libArticle(id) != nil {
		a.clipBar(c, rp, art)
	}
	if rp.feedback != "" && live {
		a.feedbackView(c, rp)
	}
}

// measure is the reader's column width and type size as the page shows them, on el: a new size glides there
// on the following spring, the text laid out anew each frame (live); a new width is there at once, the text
// flowing over to it (the reader's Flow), so that it moves rather than flashes over. From the keyboard, under
// less motion, or for a page laid out unseen, both are there at once, and the reader crosses over to them as
// it does (a fade, under less motion).
func (a *App) measure(c *ui.Context, el ui.Element, live bool) (width, size float32, glide bool) {
	width, size = a.column(), a.fontSize
	if !live || a.kbd || a.still(c) {
		return width, el.Jump("size", size), false
	}
	return width, el.AnimateTo("size", size, motion.Follow), true
}

// marksOf are the library's clips of an article, for the body to mark.
func (a *App) marksOf(id string) []reader.Mark {
	var out []reader.Mark
	for _, cl := range a.lib.Clips {
		if cl.ArticleID == id {
			out = append(out, reader.Mark{ID: cl.ID, Text: cl.Text, UnitID: cl.UnitID})
		}
	}
	return out
}

// trackBar follows the scroll for the bar: reading down past 200
// sends it up and away, scrolling up brings it back, by more than 6 a
// scroll either way; a move the reader was sent on (the saved place, a
// heading) is not reading down. Past 4 it lifts off the page.
func (a *App) trackBar(c *ui.Context, rp *readerPage) {
	r := rp.r
	y := r.Scroll.Y
	delta := y - rp.lastY
	rp.lastY = y
	rp.lifted = y > 4
	if r.Sent() {
		rp.run = 0
		return
	}
	if delta == 0 {
		return
	}
	// One scroll's way: the frames of a gesture going one way add up, as a
	// scroll event's delta does in the browser, whatever the frame rate.
	now := c.Now()
	if delta*rp.run < 0 || now.Sub(rp.runAt) > scrollGap {
		rp.run = 0
	}
	rp.run, rp.runAt = rp.run+delta, now
	if rp.run > 6 || rp.run < -6 {
		rp.away = rp.run > 0 && y > 200
	}
}

// scrollGap is how long a scroll may pause and stay one scroll.
const scrollGap = 160 * time.Millisecond

// readerHead is the column above the body: the headline, the byline, and
// what the reader has to say of the body. It returns the margin under what
// it ends with, for the body's to collapse with.
func (a *App) readerHead(c *ui.Context, rp *readerPage, art *library.Article, src *library.Source, p page) float32 {
	title := titleOf(art.Title)
	if rp.work != nil && rp.work.Name != "" {
		title = rp.work.Name
	}
	ui.Column(c.Key("head")).Children(func() {
		h := a.headline(c, title).Role(ui.RoleHeading).Level(1)
		a.end(h, p, "title:"+art.ID)
		ui.Row(c).Margin(12, 0, 0, 0).Wrap().Gap(8).AlignItems(ui.Center).FontSize(theme.Body).LineHeight(1.5).TextColor(theme.Muted).FontFeatures(theme.Numbers).Children(func() {
			a.sourceByline(c, src)
			if d := articleDate(art, false); d != "" {
				ui.Text(c, "·").AccessHidden(true)
				ui.Text(c, d)
			}
			// The reading time is the library's, not counted again.
			if m := art.Preview; m != nil && m.Minutes != nil && rp.work == nil && rp.hasBody() {
				ui.Text(c, "·").AccessHidden(true)
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					a.icon(c, "time", 12, false)
					ui.Text(c, locales.ReaderMinutes(*m.Minutes))
				})
			}
		})
	})
	// What follows the head sits 36 under it, as the body does; the body's
	// margin collapses with what is above it, as CSS's do.
	switch {
	case !rp.hasBody() && (rp.loading || rp.content == nil && rp.err == ""):
		a.pending(c, func() { a.pictogram(c, "loading") }, nil)
	case !rp.hasBody():
		reason := rp.err
		if reason == "" && rp.content != nil {
			if rp.content.RefreshError != nil {
				reason = storedText(*rp.content.RefreshError)
			} else if n := notice(rp.content); n != "" {
				reason = n
			}
		}
		if reason == "" {
			reason = locales.ReaderTextUnavailableNow()
		}
		a.pending(c, func() {
			a.emptyState(c, "unreachable", locales.ReaderUnreachableTitle(), reason, func() {
				ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
					if a.button(c, locales.CommonTryAgain(), btnOpt{}).Clicked() {
						a.loadReader(rp, true, "")
					}
					a.originalButton(c, art)
				})
			})
		}, &reason)
	default:
		failed, n := rp.err != "" || rp.content.RefreshError != nil, notice(rp.content)
		if !failed && n == "" && rp.work == nil {
			return 36
		}
		gap := float32(36)
		ui.Column(c.Key("before")).Margin(36, 0, 0, 0).Gap(24).Children(func() {
			if failed {
				a.readerNotice(c, rp, art, locales.ReaderUpdateFailed(), false)
			}
			if n != "" {
				a.readerNotice(c, rp, art, n, true)
			}
			if rp.work != nil {
				gap = a.workHero(c, rp, art, failed || n != "")
			}
		})
		return gap
	}
	return 36
}

func (rp *readerPage) hasBody() bool {
	return rp.content != nil && (strings.TrimSpace(rp.content.HTML) != "" || rp.work != nil) && rp.r != nil
}

// notice is what the reader says of a body that is not the whole article.
func notice(c *library.ArticleContent) string {
	switch {
	case c == nil || c.Status == "ready":
		return ""
	case c.Message != nil && *c.Message != "":
		return storedText(*c.Message)
	case c.Status == "summary":
		return locales.ReaderSummaryOnly()
	case c.Status == "restricted":
		return locales.ReaderRestricted()
	}
	return locales.ReaderTextUnavailable()
}

// pending is the space below the title holding the waiting or failed state,
// centred.
func (a *App) pending(c *ui.Context, fn func(), reason *string) {
	_, h := c.Size()
	p := ui.Column(c.Key("pending")).FillWidth().Margin(36, 0, 0, 0).MinHeight(max(260, h-420)).Padding(0, 0, 40, 0).AlignItems(ui.Center).Justify(ui.Center)
	if reason != nil {
		p.Role(ui.RoleAlert)
	} else {
		p.Role(ui.RoleStatus).Label(locales.ReaderLoading())
	}
	p.Children(fn)
}

// readerNotice is a ruled strip above the body (仅摘要, 更新失败) with its
// ways on: 重试, and 阅读原文 for a body that is not the whole article.
func (a *App) readerNotice(c *ui.Context, rp *readerPage, art *library.Article, text string, original bool) {
	ui.Row(c.Key("notice:"+text)).Wrap().AlignItems(ui.Center).Padding(12, 0).BorderWidth(1, 0, 1, 0).BorderColor(theme.LineSoft).
		FontSize(theme.Body).LineHeight(1.7).TextColor(theme.Text2).Role(ui.RoleAlert).Children(func() {
		ui.Text(c, text)
		if a.linkButton(c, "retry", locales.CommonTryAgain(), true, rp.loading).Margin(0, 0, 0, 12).Clicked() && !rp.loading {
			a.loadReader(rp, true, "")
		}
		if original {
			b := a.linkButton(c, "original", locales.ReaderReadOriginal(), true, false).Margin(0, 0, 0, 12)
			if b.Clicked() {
				a.openExternal(c, art.URL)
			}
		}
	})
}

// originalButton is 在浏览器打开 with its arrow: the page opens outside the app.
func (a *App) originalButton(c *ui.Context, art *library.Article) {
	b := a.button(c, locales.CommonOpenInBrowser(), btnOpt{kind: ghost})
	if b.Clicked() {
		a.openExternal(c, art.URL)
	}
}

// sourceChip is the author's chip in the byline (SourceChip): the site's own
// icon on a light disc, or the initial.
func (a *App) sourceChip(c *ui.Context, src *library.Source, size, img float32) {
	ic := a.iconOf(src)
	box := ui.Box(c).Size(size, size).Radius(size / 2).Center().ClipX().ClipY().Shrink(0).AccessHidden(true)
	if ic == nil {
		box.Background(theme.Selected).Children(func() {
			ui.Text(c, initial(nameOf(src))).FontSize(theme.Label).FontWeight(600).TextColor(theme.Text2).LineHeight(1)
		})
		return
	}
	box.Background(theme.ChipLight).Children(func() {
		ui.Image(c, ic.raw).Fit(ui.Cover).Size(img, img).Radius(img / 2)
	})
}

// readerBar is the bar at the top of the reader: back, how far
// the article has been read, and the tools. Reading down sends it away.
func (a *App) readerBar(c *ui.Context, rp *readerPage, art *library.Article, from []page, fresh bool) {
	// The bar is the column's first child, 8 past it either side, held at
	// the top of the scrolled page, 32 over the title.
	bar := ui.Row(c.Key("bar")).Sticky().Top(0).ZIndex(4).Height(52).Margin(0, -8, 32, -8).Padding(14, 0, 6, 0).Gap(4).
		AlignItems(ui.Center).Background(theme.Window).Role(ui.RoleToolbar).Label(locales.ReaderToolbar())
	// Sent away: up by its height and a pixel, faded, out of the
	// pointer's way; it stays while a control in it has the focus or its
	// panel is open. Under less motion it fades where it is; from the
	// keyboard it is simply there or gone.
	away := rp.away && !rp.aa && !rp.more && !bar.FocusWithin()
	ty, op := float32(0), float32(1)
	if away {
		ty, op = -53, 0
	}
	if a.still(c) {
		ty = 0
	}
	var o float32
	if a.kbd || fresh {
		rp.barY, o = bar.Jump("y", ty), bar.Jump("op", op)
	} else {
		rp.barY = bar.AnimateTo("y", ty, ui.Animation{Duration: motion.BarAway, Ease: motion.Ease})
		o = bar.AnimateTo("op", op, a.ease(motion.BarFade))
	}
	rp.barOp = o
	if rp.barY != 0 {
		bar.Translate(0, rp.barY)
	}
	if o < 1 {
		bar.Opacity(o)
	}
	if away {
		bar.IgnoreInput(true)
	}
	// Lifted: a hairline under it once the page has moved.
	lift := theme.LineSoft
	if !rp.lifted {
		lift = ui.Transparent
	}
	bar.Shadow(0, 1, 0, 0, bar.AnimateColor("lift", lift, a.ease(motion.BarFade)))
	bar.Children(func() {
		label := locales.CommonBack()
		if len(from) > 0 {
			label = backLabel(a, from[len(from)-1])
		}
		b := ui.ButtonBase(c.Key("back")).Label(label).Height(28).Padding(0, 10, 0, 6).Gap(4).Radius(6).AlignItems(ui.Center).MaxWidth(220).FocusRing(false)
		if b.Clicked() {
			a.back(c)
		}
		color, bg := theme.Text2, ui.Transparent
		if b.Hovered() {
			color, bg = theme.Text, theme.Hover
		}
		b.Background(b.AnimateColor("bg", bg, a.ease(motion.Hover))).TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover)))
		if b.FocusVisible() {
			b.FocusRing(true)
		}
		a.press(b, motion.Squashed)
		a.control(b)
		b.Children(func() {
			a.icon(c, "back", 16, false)
			ui.Text(c, label).SingleLine().Ellipsis("…").Shrink(1)
		})
		a.readProgress(c, rp, art, fresh)
		ui.Box(c).Grow(1)
		open := a.iconBtn(c, "original", iconOpt{icon: "openOriginal", label: locales.ReaderOpenOriginal(), tip: locales.ReaderOpenOriginalTip()})
		a.control(open)
		if open.Clicked() {
			a.openExternal(c, art.URL)
		}
		// Right-clicked, it offers the link itself.
		m := ui.ContextMenuBase(c, open, &rp.more)
		m.Popup(func(panel ui.Element) {
			a.menuPanel(c, panel)
			if a.menuItem(c, m.Item(), locales.CommonCopyLink(), false, "").Clicked() {
				a.copyLink(c, rp, art)
			}
		})
		// The favourite, once the library has the article: an Explore post's takes a moment.
		if kept := a.libArticle(art.ID); kept != nil {
			fav := a.favButton(c, kept, 32, 18)
			a.control(fav)
		}
		ui.Row(c.Key("tools")).Gap(4).AlignItems(ui.Center).Role(ui.RoleGroup).Label(locales.ReaderTools()).Children(func() { a.readerTools(c, rp, art) })
	})
	// A new width flows the text over to it (the reader's Flow); the bar widens along with the column, from the
	// frame it is chosen: as narrow as the old column on the right, the column's slide taking its left.
	if r := rp.r; r != nil {
		if inset := r.FlowInset(a.column()); inset != 0 {
			bar.Margin(0, -8+inset, 32, -8)
		}
	}
}

// copyLink puts the article's address on the clipboard (⇧⌘C, or the open button's right-click menu).
func (a *App) copyLink(c *ui.Context, rp *readerPage, art *library.Article) {
	c.Services().WriteClipboard(art.URL)
	a.say(rp, locales.CommonLinkCopied())
}

// readProgress is how far the article is read, beside the back button: the moon and the share. They follow the
// reading as it goes, not the place the library keeps once it stops: one value glides to the whole percent read
// (the following spring), the share's digits turning on it as a counter's wheels do, the ones going round and the
// tens turning on with them past 9, and the moon waning with it. Unread it is not there, opening out as the first
// percent is read; past 94 the share folds away and the moon empties: 读到结尾. From the keys, under less motion,
// as the page opens and while the bar is away, it is there at once.
func (a *App) readProgress(c *ui.Context, rp *readerPage, art *library.Article, fresh bool) {
	p := progressOf(art)
	if rp.r != nil && rp.restored && rp.measured.ContentVersion != "" {
		p = float64(rp.measured.Progress)
	}
	outer := ui.Row(c.Key("progress")).ClipX().Shrink(0).AlignItems(ui.Center)
	target := float32(math.Round(p * 100))
	var v float32
	// Out of sight, the bar sent away, it keeps up with the reading at once, so that it shows where the reading is as
	// the bar comes back, not turning there from where it last showed.
	if fresh || a.kbd || a.still(c) || rp.barOp < 0.05 {
		v = outer.Jump("v", target)
	} else {
		v = outer.AnimateTo("v", target, motion.Follow)
	}
	show, num := unit(v), unit(95-v)
	label := locales.ReaderProgress(share(p))
	if p >= 0.95 {
		label = locales.ReaderFinished()
	}
	if show < 1 {
		outer.Width(show * rp.progW).Opacity(show)
	}
	if show <= 0 {
		return // the row stays, holding the value
	}
	outer.Margin(0, 0, 0, 6*show).Children(func() {
		in := ui.Row(c.Key("in")).Gap(6 * num).AlignItems(ui.Center).Shrink(0).FontSize(theme.Label).TextColor(theme.Muted)
		in.Children(func() {
			a.moon(c, 10, max(0.06, 1-v/100)*num, label)
			if num <= 0 {
				return
			}
			share := ui.Row(c.Key("share")).ClipX().Shrink(0).AlignItems(ui.Center).AccessHidden(true)
			if num < 1 {
				share.Width(num * rp.shareW).Opacity(num)
			}
			share.Children(func() {
				wheels := ui.Row(c.Key("wheels")).Shrink(0).AlignItems(ui.Center).FontFeatures(theme.Numbers)
				wheels.Children(func() { a.wheels(c, rp, min(v, 99)) })
				// Its own width, held off at none by the share's: what the share opens to.
				if w := wheels.LayoutBounds().W; w > 0 {
					rp.shareW = w
				}
			})
		})
		if w := in.LayoutBounds().W; w > 0 {
			rp.progW = w
		}
	})
}

// wheelH is the height of a line of the share's wheels.
const wheelH = 18

// wheels are the share's digits for v, as a counter's wheels show it: the ones turning with v's fraction, the tens
// turning on with them from 9 to the next ten and showing nothing below ten, and the %.
func (a *App) wheels(c *ui.Context, rp *readerPage, v float32) {
	ones := float32(math.Mod(float64(v), 10))
	tens := float32(math.Floor(float64(v)/10)) + max(0, ones-9)
	wheel := func(key string, at float32, faces []string, width float32) {
		slot := ui.Box(c.Key(key)).Height(wheelH).ClipX().ClipY().Shrink(0)
		if width >= 0 {
			slot.Width(width)
		}
		slot.Children(func() {
			col := ui.Column(c).Translate(0, -at*wheelH)
			col.Children(func() {
				for _, f := range faces {
					ui.Text(c, f).Height(wheelH).FixedLineHeight(wheelH)
				}
			})
			if w := col.LayoutBounds().W; w > 0 {
				rp.digitW = w
			}
		})
	}
	// Below ten the tens wheel shows nothing and takes no room; it opens as the 1 turns in.
	wheel("tens", tens, []string{"", "1", "2", "3", "4", "5", "6", "7", "8", "9"}, rp.digitW*min(1, tens))
	wheel("ones", ones, []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "0"}, -1)
	ui.Text(c, "%")
}

// unit is v held between 0 and 1.
func unit(v float32) float32 { return max(0, min(1, v)) }

// backLabel names the page the reader goes back to.
func backLabel(a *App, p page) string {
	switch p.kind {
	case "author":
		return nameOf(a.source(p.id))
	case "favorites":
		return locales.TabFavorites()
	case "clips":
		return locales.TabClips()
	case "explore":
		return locales.TabExplore()
	case "kind":
		return kindName(p.id)
	case "reader":
		if art := a.article(p.id); art != nil {
			return titleOf(art.Title)
		}
	}
	return locales.TabHome()
}

// readerTools are Aa: how the text looks. Pressed, its options open out of it along the bar, to its left, so that
// nothing lies over the text they change while it changes; pressed again, or Esc, they fold back into it. What opens
// and folds is the row's width, the controls held against Aa and cut off at the row's far edge, so that the buttons
// beside it slide along with it. The controls say what they are to a screen reader; on the bar they need no words.
func (a *App) readerTools(c *ui.Context, rp *readerPage, art *library.Article) {
	row := ui.Row(c.Key("options")).ClipX().AlignItems(ui.Center).Justify(ui.End).Shrink(0)
	target := float32(0)
	if rp.aa {
		target = rp.optW
	}
	var w float32
	if a.kbd || a.still(c) {
		w = row.Jump("w", target)
	} else {
		w = row.AnimateTo("w", target, ui.Animation{Duration: map[bool]time.Duration{true: motion.PopIn, false: motion.PopOut}[rp.aa], Ease: motion.Ease})
	}
	row.Width(w)
	if rp.aa || w > 0.5 {
		if rp.optW > 0 {
			row.Opacity(min(1, w/rp.optW))
		}
		row.Role(ui.RoleGroup).Label(locales.ReaderTypography()).Children(func() {
			controls := ui.Row(c.Key("controls")).Gap(16).Padding(0, 8, 0, 0).AlignItems(ui.Center).Shrink(0).FontSize(theme.Body).TextColor(theme.Text2)
			controls.Children(func() {
				a.control(a.stepper(c, "size", locales.ReaderTextSize(), &a.fontSize, theme.ReadingMin, theme.ReadingMax, locales.ReaderTextSmaller(), locales.ReaderTextLarger()))
				ww := map[bool]int{true: 1, false: 0}[a.wide]
				a.control(a.segmented(c, "width", locales.ReaderTextWidth(), []string{locales.ReaderWidthComfortable(), locales.ReaderWidthWide()}, &ww))
				a.wide = ww == 1
			})
			// Its width as laid out, which the row opens to; measured the frame after it first shows.
			if cw := controls.LayoutBounds().W; cw > 0 {
				rp.optW = cw
			}
		})
	}
	aa := a.iconBtn(c, "aa", iconOpt{icon: "textSize", label: locales.ReaderOptions(), tip: locales.ReaderOptionsTip(), active: rp.aa})
	a.control(aa)
	if aa.Clicked() {
		rp.aa = !rp.aa
	}
	aa.Expanded(rp.aa)
}

// clipBar floats over the selection: 摘录 ⌘E, or 取消摘录
// for a passage already clipped. It shows once the selection settles.
func (a *App) clipBar(c *ui.Context, rp *readerPage, art *library.Article) {
	r := rp.r
	sel, ok := r.Selection()
	key := ""
	if ok {
		key = fmt.Sprint(sel.Start, sel.End, len(sel.Text))
	}
	now := c.Now()
	if key != rp.selKey {
		rp.selKey, rp.selAt = key, now
	}
	if c.Shortcut(ui.Cmd, ui.KeyE) {
		a.clip(c, rp, art)
		return
	}
	if !ok || key == rp.selHide {
		return
	}
	if wait := motion.SelectionSettle - now.Sub(rp.selAt); wait > 0 && !a.kbd {
		c.After(wait)
		return
	}
	box, found := r.SelectionBox()
	if !found {
		return
	}
	existing, inMark := r.SelectedMark()
	col := min(a.area.W-80, a.column())
	left := a.area.X + (a.area.W-col)/2
	x := min(max(box.X+box.W/2, left+60), left+col-60) - a.area.X
	below := box.Y-max(a.area.Y, a.area.Y+52) < 56
	bar := ui.Row(c.Key("clipbar")).Absolute().ZIndex(5).Padding(4).Gap(4).Radius(10).Background(theme.Elevated).AlignItems(ui.Center).
		InsetShadow(0, 0, 0, 1, theme.Line).Shadow(0, 0, 0, 1, theme.ShadowEdge).Shadow(0, 12, 32, 0, theme.ShadowFloat).
		Role(ui.RoleToolbar).Label(map[bool]string{true: locales.ClipsActions(), false: locales.ReaderSelectionActions()}[inMark])
	w := bar.LayoutBounds().W
	bar.Left(x - w/2)
	if below {
		bar.Top(box.Y + box.H + 8 - a.area.Y)
	} else {
		bar.Top(box.Y - 8 - bar.LayoutBounds().H - a.area.Y)
	}
	if bar.OverlayShortcut(0, ui.KeyEscape) {
		rp.selHide = key
	}
	enter := ui.ElementTransition{Duration: motion.ClipBarIn, Ease: motion.Ease, Enter: &ui.Motion{Y: 4, Scale: 0.97}}
	bar.Transition(a.tr(c, enter)).Children(func() {
		label := locales.ReaderClip()
		if inMark {
			label = locales.ReaderUnclip()
		}
		b := ui.ButtonBase(c.Key("clip")).Label(label).KeyShortcuts(ui.Cmd, ui.KeyE).Height(32).Padding(0, 12).Gap(8).Radius(6).AlignItems(ui.Center).FocusRing(false)
		if b.Hovered() {
			b.Background(theme.Selected)
		}
		if b.FocusVisible() {
			b.FocusRing(true)
		}
		a.press(b, motion.Squashed)
		if b.Clicked() {
			if inMark {
				a.unclip(rp, existing)
			} else {
				a.clip(c, rp, art)
			}
		}
		b.Children(func() {
			t := ui.Text(c, label).FontSize(theme.Body)
			if !inMark {
				t.TextColor(theme.Accent).FontWeight(500)
			}
			ui.Row(c).Gap(2).AccessHidden(true).Children(func() { kbd(c, "⌘E") })
		})
	})
}

// clip keeps the selection as a clip in the library (⌘E), or takes back the
// clip the selection lies within.
func (a *App) clip(c *ui.Context, rp *readerPage, art *library.Article) {
	r := rp.r
	if id, ok := r.SelectedMark(); ok {
		a.unclip(rp, id)
		return
	}
	sel, ok := r.Selection()
	if !ok || rp.clipBusy {
		return
	}
	if len([]rune(sel.Text)) > 20000 {
		a.say(rp, locales.ReaderSelectionTooLong())
		return
	}
	rp.clipBusy = true
	in := library.ClipInput{ID: sel.ID, ArticleID: art.ID, Text: sel.Text, Note: "", UnitID: sel.UnitID, ContentRevision: sel.ContentRevision}
	a.mutateOr(library.AddClip{Clip: in}, func() {
		rp.clipBusy = false
		r.SetMarks(a.marksOf(art.ID))
		r.ClearSelection()
		a.say(rp, locales.ReaderClipped())
	}, func() {
		rp.clipBusy = false
		a.say(rp, locales.ReaderClipNotSaved())
	})
}

// unclip takes a clip back; the notice offers 撤销.
func (a *App) unclip(rp *readerPage, id string) {
	var cl *library.Clip
	for i := range a.lib.Clips {
		if a.lib.Clips[i].ID == id {
			cl = &a.lib.Clips[i]
		}
	}
	if cl == nil {
		rp.r.Unmark(id)
		return
	}
	restore := library.AddClip{Clip: library.ClipInput{ID: cl.ID, ArticleID: cl.ArticleID, Text: cl.Text, Note: cl.Note, UnitID: cl.UnitID,
		ContentRevision: cl.ContentRevision, CreatedAt: cl.CreatedAt}}
	a.mutate(library.DeleteClip{ID: id}, func() {
		rp.r.SetMarks(a.marksOf(rp.id))
		rp.r.ClearSelection()
		a.notify(locales.ClipsDeleted(), nil, restore)
	})
}

// feedbackView is the reader's quiet status line at the window's corner,
// for three seconds.
func (a *App) feedbackView(c *ui.Context, rp *readerPage) {
	now := c.Now()
	if rp.feedbackAt.IsZero() {
		rp.feedbackAt = now
	}
	left := motion.FeedbackFor - now.Sub(rp.feedbackAt)
	if left <= 0 {
		rp.feedback = ""
		return
	}
	c.After(left)
	enter := ui.ElementTransition{Duration: motion.FeedbackIn, Ease: motion.Ease, Enter: &ui.Motion{Y: 4, Scale: 0.97}}
	ui.Text(c.Key("feedback:"+rp.feedback), rp.feedback).Absolute().Right(24).Bottom(24).ZIndex(20).Padding(8, 12).Radius(12).
		Background(theme.Elevated).Border(1, theme.Line).FontSize(theme.Label).TextColor(theme.Text).Role(ui.RoleStatus).
		Shadow(0, 0, 0, 1, theme.ShadowEdge).Shadow(0, 12, 32, 0, theme.ShadowFloat).Transition(a.tr(c, enter))
}
