package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/reading"
	"trie/internal/theme"
	"trie/internal/web"
)

// The reader: the bar
// at the top, the headline and the byline, the body internal/reader shows,
// the author's sign-off and the end mark; the outline in the left margin,
// and the bar that clips a selection.

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
	titled       bool // it carries the title
	lastY        float32
	run          float32   // how far this scroll has gone one way
	runAt        time.Time // when it last moved
	barY         float32   // where the bar is, sent away or not
	titleBottom  float32   // the headline's foot in the window, the frame before

	// The screenshot the strip's amber line went to last, and whether it
	// went back along the strip to it (workStrip).
	markAt   int
	markBack bool

	measured reader.Position
	saveAt   time.Time
	restored bool

	aa, more   bool
	feedback   string
	feedbackAt time.Time

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

	lit   float32   // the moon as the article was opened, for its row's to wane from
	swipe backSwipe // the two-finger swipe back

	selKey   string    // the selection, to see it settle
	selAt    time.Time // when it last changed
	selHide  string    // a selection whose bar Esc closed
	clipBusy bool
}

// reading is article id's reader, its body asked for if it is not there.
func (a *App) reading(id string) *readerPage {
	rp := a.readers[id]
	if rp == nil {
		rp = &readerPage{id: id, left: true}
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
		rp.loading, rp.err = false, "文章不在当前阅读库中"
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
	if pos.ScrollTop != rp.measured.ScrollTop || pos.ContentVersion != rp.measured.ContentVersion {
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
	if rp.saveAt.IsZero() || isTrial(rp.id) {
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
				a.say(rp, "阅读位置保存失败；本次会话仍可继续阅读")
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
		if !a.kbd && !isTrial(left.id) {
			a.moonBack = &moonBack{id: left.id, from: rp.lit}
		}
	}
	a.focused = false
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
		rp.lit, rp.swipe = litOf(art), backSwipe{}
	}
	// Arrived, the reader takes the focus, so that the keys scrolling it
	// are the keyboard's wherever the pointer is.
	if live && rp.focus && rp.r != nil {
		rp.r.Focus, rp.focus = true, false
	}
	p := page{"reader", id}
	src := a.sourceOf(art)
	col := a.column()
	// The page, in the box the two-finger swipe holds as it pulls; the
	// page left keeps the pull it went with, and laid out unseen it has none.
	if !live && (a.left == nil || a.left.page != p) {
		rp.swipe = backSwipe{}
	}
	sw := ui.Column(c.Key("swipe")).Grow(1).FillWidth().MinHeight(0).Role(ui.RoleRegion).Label(map[bool]string{true: "文章预览", false: "文章阅读"}[isTrial(id)])
	a.swipeView(c, sw, rp, live)
	sw.Children(func() {
		if r := rp.r; r != nil {
			r.Width, r.Size = col, a.fontSize
			r.Bar = func() {
				a.readerBar(c, rp, art, from, fresh)
				r.Width, r.Size = a.column(), a.fontSize // as its reading options left them
			}
			r.Above = func() float32 { return a.readerHead(c, rp, art, src, p) }
			r.Below = func() { a.readerFoot(c, rp, art, src) }
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
			r.Name = titleOf(art.Title) + " · 正文"
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
			a.outline(c, rp)
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
	if live && rp.r != nil && !isTrial(id) {
		a.clipBar(c, rp, art)
	}
	a.swipeMark(c, rp)
	if rp.feedback != "" && live {
		a.feedbackView(c, rp)
	}
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
// heading) is not reading down. Past 4 it lifts off the page; once the
// headline has passed under it, it carries the title.
func (a *App) trackBar(c *ui.Context, rp *readerPage) {
	r := rp.r
	y := r.Scroll.Y
	delta := y - rp.lastY
	rp.lastY = y
	rp.lifted = y > 4
	rp.titled = rp.titleBottom < a.area.Y+52+rp.barY
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

// scrollGap is how long a scroll may pause and stay one scroll (the swipe
// back reads its gestures so).
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
		rp.titleBottom = h.LayoutBounds().Y + h.LayoutBounds().H
		ui.Row(c).Margin(12, 0, 0, 0).Wrap().Gap(8).AlignItems(ui.Center).FontSize(theme.Body).LineHeight(1.5).TextColor(theme.Muted).Children(func() {
			a.sourceChip(c, src, 18, 14)
			ui.Text(c, nameOf(src)).TextColor(theme.Text2)
			ui.Text(c, "·").AccessHidden(true)
			switch {
			case art.PublishedAt != nil && *art.PublishedAt != "":
				ui.Text(c, metaDate(*art.PublishedAt))
			case art.ModifiedAt != nil && *art.ModifiedAt != "":
				ui.Text(c, "更新于 "+metaDate(*art.ModifiedAt))
			default:
				ui.Text(c, "未提供日期")
			}
			// The reading time is the library's, not counted again.
			if m := art.Preview; m != nil && m.Minutes != nil && rp.work == nil && rp.hasBody() {
				ui.Text(c, "·").AccessHidden(true)
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					a.icon(c, "time", 12, false)
					ui.Text(c, fmt.Sprintf("约 %d 分钟", *m.Minutes))
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
				reason = *rp.content.RefreshError
			} else if n := notice(rp.content); n != "" {
				reason = n
			}
		}
		if reason == "" {
			reason = "暂时无法获取正文。"
		}
		a.pending(c, func() {
			a.emptyState(c, "unreachable", "正文暂时读不出来", reason, func() {
				ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
					if a.button(c, "重试", btnOpt{}).Clicked() {
						a.loadReader(rp, true, "")
					}
					a.originalButton(c, art)
				})
			})
		}, &reason)
	default:
		failed, n := rp.err != "" || rp.content.RefreshError != nil, notice(rp.content)
		if !rp.loading && !failed && n == "" && rp.work == nil {
			return 36
		}
		gap := float32(36)
		ui.Column(c.Key("before")).Margin(36, 0, 0, 0).Gap(24).Children(func() {
			if rp.loading {
				ui.Text(c, "正在更新正文…").FontSize(theme.Label).TextColor(theme.Muted).Role(ui.RoleStatus)
			}
			if failed {
				a.readerNotice(c, rp, art, "更新失败，继续显示本机保存的版本。", false)
			}
			if n != "" {
				a.readerNotice(c, rp, art, n, true)
			}
			if rp.work != nil {
				gap = a.workHero(c, rp, art, rp.loading || failed || n != "")
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
		return *c.Message
	case c.Status == "summary":
		return "当前来源仅提供摘要，可前往作者网站继续阅读。"
	case c.Status == "restricted":
		return "来源内容受限，请前往作者网站查看访问方式。"
	}
	return "暂时无法获取正文，请稍后重试。"
}

// pending is the space below the title holding the waiting or failed state,
// centred.
func (a *App) pending(c *ui.Context, fn func(), reason *string) {
	_, h := c.Size()
	p := ui.Column(c.Key("pending")).FillWidth().Margin(36, 0, 0, 0).MinHeight(max(260, h-420)).Padding(0, 0, 40, 0).AlignItems(ui.Center).Justify(ui.Center)
	if reason != nil {
		p.Role(ui.RoleAlert)
	} else {
		p.Role(ui.RoleStatus).Label("正在读取正文…")
	}
	p.Children(fn)
}

// readerNotice is a ruled strip above the body (仅摘要, 更新失败) with its
// ways on: 重试, and 阅读原文 for a body that is not the whole article.
func (a *App) readerNotice(c *ui.Context, rp *readerPage, art *library.Article, text string, original bool) {
	ui.Row(c.Key("notice:"+text)).Wrap().AlignItems(ui.Center).Padding(12, 0).BorderWidth(1, 0, 1, 0).BorderColor(theme.LineSoft).
		FontSize(theme.Body).LineHeight(1.7).TextColor(theme.Text2).Role(ui.RoleAlert).Children(func() {
		ui.Text(c, text)
		if a.linkButton(c, "retry", "重试", true, rp.loading).Margin(0, 0, 0, 12).Clicked() && !rp.loading {
			a.loadReader(rp, true, "")
		}
		if original {
			b := a.linkButton(c, "original", "阅读原文", true, false).Margin(0, 0, 0, 12)
			if b.Clicked() {
				a.openExternal(c, art.URL)
			}
		}
	})
}

// originalButton is 在浏览器打开 with its arrow: the page opens outside the app.
func (a *App) originalButton(c *ui.Context, art *library.Article) {
	b := a.button(c, "在浏览器打开", btnOpt{kind: ghost})
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

// readerFoot is what follows the body: the author signing off, set to the
// right as an essay or a letter ends, and the end mark.
func (a *App) readerFoot(c *ui.Context, rp *readerPage, art *library.Article, src *library.Source) {
	if !rp.hasBody() {
		return
	}
	name := nameOf(src)
	var byline []string
	for _, n := range art.Authors {
		if strings.TrimSpace(strings.ToLower(n)) != strings.TrimSpace(strings.ToLower(name)) {
			byline = append(byline, n)
		}
	}
	signer := name
	if len(byline) > 0 {
		signer = strings.Join(byline, "、") + " · " + name
	}
	sign := ui.Column(c.Key("signoff")).Margin(rp.r.EndGap(), 0, 0, 0).AlignItems(ui.End).Gap(4).Role(ui.RoleGroup).Label("文章信息")
	// It comes into focus as it scrolls in, from 0.2 and a 3px blur to clear
	// about 180 up (animation-timeline: view(), cover 0% to 20%).
	box, vh := sign.LayoutBounds(), a.area.H
	if box.H > 0 && vh > 0 {
		k := min(1, max(0, (vh-(box.Y-a.area.Y))/(0.2*(vh+box.H))))
		if k < 1 {
			sign.Opacity(0.2 + 0.8*k)
			if blur := 3 * (1 - k); blur > 0.05 {
				sign.Blur(blur)
			}
		}
	}
	sign.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).MinHeight(32).MaxWidthPercent(100).Children(func() {
			// In a preview the pill (face and 订阅) is the signature: the face
			// that signs is the one that flies to the sidebar.
			if t := a.trials[art.ID]; t != nil && t.entry.FeedURL != nil && *t.entry.FeedURL != "" {
				a.follow(c, &t.entry, "foot")
				return
			}
			a.sourceChip(c, src, 24, 18)
			ui.Text(c, signer).FontSize(theme.Item).LineHeight(1.45).FontWeight(500).TextColor(theme.Text2).SingleLine().Ellipsis("…").Shrink(1)
		})
		ui.Row(c).Wrap().Justify(ui.End).AlignItems(ui.Center).Gap(8).FontSize(theme.Label).LineHeight(20.0 / 12).TextColor(theme.Muted).Children(func() {
			dated, revised := dayOf(art.PublishedAt), dayOf(art.ModifiedAt)
			if dated != "" {
				d := ui.Text(c, dated)
				a.tip(c, d, fullDate(art.PublishedAt), false)
			}
			if revised != "" && revised != dated {
				ui.Text(c, "·").AccessHidden(true)
				d := ui.Text(c, metaDate(*art.ModifiedAt)+"更新")
				a.tip(c, d, fullDate(art.ModifiedAt), false)
			}
			if dated != "" {
				ui.Text(c, "·").AccessHidden(true)
			}
			l := ui.ButtonBase(c.Key("site")).Label("在浏览器打开原文：" + art.URL).Gap(4).AlignItems(ui.Center).Radius(6).FocusRing(false)
			color, line := theme.Muted, ui.Transparent
			if l.Hovered() {
				color, line = theme.Text, theme.Accent
			}
			if l.FocusVisible() {
				l.FocusRing(true)
			}
			if l.Clicked() {
				a.openExternal(c, art.URL)
			}
			l.TextColor(l.AnimateColor("fg", color, a.ease(motion.SignHover))).Children(func() {
				ui.Text(c, hostOf(art.URL)).Underline().UnderlineOffset(3).DecorationColor(l.AnimateColor("line", line, a.ease(motion.SignHover)))
				a.pressIcon(c, "openArrow", 12, false, l.Pressed())
			})
			a.tip(c, l, art.URL, false)
		})
	})
	// The end: a hairline either side of one quiet line, only when it has
	// something to say.
	status := rp.content.Status
	minutes := art.Preview != nil && art.Preview.Minutes != nil && rp.work == nil
	if status != "summary" && status != "restricted" && !minutes {
		return
	}
	ui.Row(c.Key("end")).Margin(48, 0, 0, 0).Gap(12).AlignItems(ui.Center).FontSize(theme.Label).TextColor(theme.Muted).Children(func() {
		ui.Box(c).Height(1).Grow(1).Background(theme.Line)
		switch status {
		case "summary", "restricted":
			ui.Text(c, map[bool]string{true: "摘要到此结束", false: "公开内容到此结束"}[status == "summary"])
			if a.linkButton(c, "original", "阅读原文", false, false).Clicked() {
				a.openExternal(c, art.URL)
			}
		default:
			text := fmt.Sprintf("全文约 %d 分钟 · %s", *art.Preview.Minutes, rp.r.Body.Size)
			if rp.reached(rp.r) {
				text = "读完 · " + text
			}
			ui.Text(c, text)
		}
		ui.Box(c).Height(1).Grow(1).Background(theme.Line)
	})
}

// reached reports whether the reader has been near the end (97%).
func (rp *readerPage) reached(r *reader.Reader) bool {
	s := r.Scroll
	return s.MaxY <= 0 || s.Y/s.MaxY >= 0.97
}

// readerBar is the bar at the top of the reader: back, how far
// the article has been read, the title once it has scrolled under, and the
// tools. Reading down sends it away.
func (a *App) readerBar(c *ui.Context, rp *readerPage, art *library.Article, from []page, fresh bool) {
	// The bar is the column's first child, 8 past it either side, held at
	// the top of the scrolled page, 32 over the title.
	bar := ui.Row(c.Key("bar")).Sticky().Top(0).ZIndex(4).Height(52).Margin(0, -8, 32, -8).Padding(14, 0, 6, 0).Gap(4).
		AlignItems(ui.Center).Background(theme.Window).Role(ui.RoleToolbar).Label("阅读操作")
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
		label := "返回"
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
		// How far it was read, as the library keeps it: nothing before it was
		// started, the moon and the share while being read, at the end the
		// empty ring and no number.
		if p := progressOf(art); p >= 0.95 || percentOf(art) > 0 {
			ui.Row(c).Gap(6).Margin(0, 0, 0, 6).AlignItems(ui.Center).FontSize(theme.Label).TextColor(theme.Muted).Children(func() {
				label := fmt.Sprintf("读到 %d%%", percentOf(art))
				if p >= 0.95 {
					label = "读到结尾"
				}
				a.moon(c, 10, litOf(art), label)
				if p < 0.95 {
					ui.Text(c, fmt.Sprintf("%d%%", percentOf(art))).AccessHidden(true)
				}
			})
		}
		// The title, once the headline has passed under the bar.
		on := rp.titled && rp.r != nil
		k := b.AnimateTo("title", map[bool]float32{true: 1, false: 0}[on], a.ease(motion.BarTitle))
		if k > 0.001 {
			ty := (1 - b.AnimateTo("titleY", map[bool]float32{true: 1, false: 0}[on], ui.Animation{Duration: motion.BarTitleRise, Ease: motion.Ease})) * 4
			if a.still(c) || a.kbd {
				ty = 0
			}
			ui.Text(c, titleOf(art.Title)).Margin(0, 0, 0, 10).Padding(0, 0, 0, 12).BorderWidth(0, 0, 0, 1).BorderColor(theme.Line).
				FontSize(theme.Body).LineHeight(16.0/13).TextColor(theme.Text2).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).
				Opacity(k).Translate(0, ty).AccessHidden(true)
		}
		ui.Box(c).Grow(1)
		// A post outside the library (an Explore preview) offers its author's
		// pill instead of the favourite, 8 clear of the bar's other buttons.
		t := a.trials[art.ID]
		if t != nil {
			if pill := a.follow(c, &t.entry, "bar"); pill.Valid() {
				pill.Margin(0, 8)
				a.control(pill)
			}
		}
		open := a.iconBtn(c, "original", iconOpt{icon: "openOriginal", label: "在浏览器打开原文", tip: "在浏览器打开原文 · O"})
		a.control(open)
		if open.Clicked() {
			a.openExternal(c, art.URL)
		}
		if t == nil {
			fav := a.favButton(c, art, 32, 18)
			a.control(fav)
		}
		ui.Row(c.Key("tools")).Gap(4).AlignItems(ui.Center).Role(ui.RoleGroup).Label("阅读工具").Children(func() { a.readerTools(c, rp, art) })
	})
}

// backLabel names the page the reader goes back to.
func backLabel(a *App, p page) string {
	switch p.kind {
	case "author":
		return nameOf(a.source(p.id))
	case "favorites":
		return "收藏"
	case "clips":
		return "摘录"
	case "explore":
		return "探索"
	}
	return "首页"
}

// readerTools are Aa, how the text looks, and ⋯, what one does with the
// article.
func (a *App) readerTools(c *ui.Context, rp *readerPage, art *library.Article) {
	aa := a.iconBtn(c, "aa", iconOpt{icon: "textSize", label: "阅读选项", tip: "字号、宽度与专注阅读", active: rp.aa})
	a.control(aa)
	if aa.Clicked() {
		rp.aa = !rp.aa
	}
	aa.HasPopup(ui.PopupDialog).Expanded(rp.aa)
	ui.PopoverBase(c, aa, &rp.aa, func(panel ui.Element) {
		panel.AttachTo(aa, ui.AnchorBottomRight, ui.AnchorTopRight).Margin(8, 0, 0, 0).AttachedOrigin().Transition(a.tr(c, motion.Popover())).
			ObserveInput(a.observe).Width(280).Padding(8, 0).Radius(12).Background(theme.Elevated).Border(1, theme.Line).TextColor(theme.Text2).
			Shadow(0, 0, 0, 1, theme.ShadowEdge).Shadow(0, 12, 32, 0, theme.ShadowFloat).Label("阅读选项").FontSize(theme.Body).LineHeight(1.5)
		setting := func(label string, control func()) {
			ui.Row(c.Key(label)).MinHeight(48).Padding(0, 16).Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, label)
				control()
			})
		}
		setting("正文字号", func() {
			a.stepper(c, "size", "正文字号", &a.fontSize, theme.ReadingMin, theme.ReadingMax, "缩小正文字号", "放大正文字号")
		})
		setting("正文宽度", func() {
			w := map[bool]int{true: 1, false: 0}[a.wide]
			a.segmented(c, "width", "正文宽度", []string{"舒适", "宽松"}, &w)
			a.wide = w == 1
		})
		setting("专注阅读", func() {
			a.switchControl(c, "focus", "专注阅读", &a.focused)
		})
		if a.fontSize != theme.ReadingSize || a.wide {
			ui.Row(c).Justify(ui.End).Padding(0, 16, 8, 16).FontSize(theme.Label).Children(func() {
				if a.linkButton(c, "reset", "恢复默认", false, false).Clicked() {
					a.fontSize, a.wide = theme.ReadingSize, false
				}
			})
		}
	})
	open := rp.more
	m := ui.MenuBase(c.Key("more"), &open)
	t := m.Trigger.Label("更多").Size(32, 32).Radius(6).Center().FocusRing(false)
	color, bg := theme.Text2, ui.Transparent
	switch {
	case open:
		color, bg = theme.Text, theme.Selected
	case t.Hovered():
		color, bg = theme.Text, theme.Hover
	}
	t.Background(t.AnimateColor("bg", bg, a.ease(motion.Hover))).TextColor(t.AnimateColor("fg", color, a.ease(motion.Hover)))
	if t.FocusVisible() {
		t.FocusRing(true)
	}
	a.control(t)
	t.Children(func() { a.pressIcon(c, "more", 16, false, open) }) // a menu opens on the press
	a.tip(c, t, "更多", false)
	m.Popup(func(panel ui.Element) {
		a.menuPanel(c, panel).AttachTo(m.Trigger, ui.AnchorBottomRight, ui.AnchorTopRight).Margin(8, 0, 0, 0)
		if a.menuItem(c, m.Item(), "复制原文链接", false, "").Clicked() {
			c.Services().WriteClipboard(art.URL)
			a.say(rp, "已复制到剪贴板")
		}
		up := m.Item().Disabled(rp.loading)
		if a.menuItem(c, up, "更新正文", false, "").Clicked() && !rp.loading {
			a.loadReader(rp, true, "")
		}
		if other := otherSource(rp); other != "" {
			ui.Box(c).Height(1).Margin(4, 8).Background(theme.Line).AccessHidden(true)
			label := map[bool]string{true: "改读订阅源的版本", false: "改读原网页的版本"}[other == "feed"]
			if a.menuItem(c, m.Item().Disabled(rp.loading), label, false, "").Clicked() && !rp.loading {
				a.loadReader(rp, true, other)
			}
		}
	})
	rp.more = open
}

// otherSource is the body's other source the ⋯ menu offers: the feed's
// version of a page read, or the page of a feed's.
func otherSource(rp *readerPage) string {
	c := rp.content
	if c == nil || rp.work != nil || (c.FeedVersion == nil && c.SourceKind != "feed") {
		return ""
	}
	if c.SourceKind == "feed" {
		return "page"
	}
	return "feed"
}

// outline is the outline in the left margin (目录): one tick per heading,
// the section being read brighter; pointed at, the ticks unfold into the
// headings. Only where the margin has room.
func (a *App) outline(c *ui.Context, rp *readerPage) {
	hs := rp.content.Headings
	if len(hs) < 2 {
		return
	}
	col := min(a.area.W-80, a.column())
	left := (a.area.W - col) / 2 // the column's left edge, in the content area
	room := left - 32 - 24
	if room < 48 {
		return
	}
	width := min(220, room)
	lo := hs[0].Level
	for _, h := range hs {
		lo = min(lo, h.Level)
	}
	// The section being read: the last heading that has passed 110 below the top.
	r := rp.r
	here := ""
	for _, h := range hs {
		if y, ok := r.AnchorY(h.ID); ok && y-r.Scroll.Y <= 110 {
			here = h.ID
		}
	}
	if r.Scroll.MaxY > 0 && r.Scroll.Y >= r.Scroll.MaxY-2 {
		here = hs[len(hs)-1].ID
	}
	_, winH := c.Size()
	nav := ui.Scroll(c.Key("outline")).Absolute().Top(120).Left(left - 32 - width).Width(width).MaxHeight(winH - 200).Scrollbars(false).
		Role(ui.RoleNavigation).Label("文章目录").ZIndex(3)
	open := nav.Hovered() || nav.FocusWithin()
	k := nav.AnimateTo("open", map[bool]float32{true: 1, false: 0}[open], a.ease(motion.Hover))
	dx := float32(0)
	if !a.still(c) {
		dx = 4 * (1 - nav.AnimateTo("openX", map[bool]float32{true: 1, false: 0}[open], ui.Animation{Duration: motion.Hover, Ease: motion.Ease}))
	}
	nav.Children(func() {
		ui.Column(c).Children(func() {
			for _, h := range hs {
				on := h.ID == here
				b := ui.ButtonBase(c.Key(h.ID)).Label(h.Text).MinHeight(24).Gap(12).Radius(6).AlignItems(ui.Center).Justify(ui.End).FocusRing(false)
				if on {
					b.Current(ui.CurrentLocation)
				}
				if b.FocusVisible() {
					b.FocusRing(true).FocusRingOffset(-2)
				}
				if b.Clicked() {
					r.Jump(h.ID, !a.kbd && !a.still(c))
				}
				depth := min(2, max(0, h.Level-lo))
				tick := [3]float32{16, 10, 6}[depth]
				tc, lc := theme.Faint, theme.Text2
				if on {
					tick, tc, lc = 24, theme.Text, theme.Text
				}
				b.Children(func() {
					ui.Text(c, h.Text).FontSize(theme.Body).LineHeight(20.0/13).TextColor(b.AnimateColor("fg", lc, a.ease(motion.Hover))).
						SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).Opacity(k).Translate(dx, 0).TextAlign(ui.End)
					ui.Row(c).Width(24).Justify(ui.End).Shrink(0).Children(func() {
						w := b.AnimateTo("tick", tick, ui.Animation{Duration: motion.Hover, Ease: motion.Ease})
						if a.kbd || a.still(c) {
							w = b.Jump("tick", tick)
						}
						ui.Box(c).Width(w).Height(2).Radius(1).Background(b.AnimateColor("tickColor", tc, a.ease(motion.Hover)))
					})
				})
			}
		})
	})
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
		Role(ui.RoleToolbar).Label(map[bool]string{true: "这段摘录的操作", false: "选中文字的操作"}[inMark])
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
		label := "摘录"
		if inMark {
			label = "取消摘录"
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
		a.say(rp, "选中的文字太长")
		return
	}
	rp.clipBusy = true
	in := library.ClipInput{ID: sel.ID, ArticleID: art.ID, Text: sel.Text, Note: "", UnitID: sel.UnitID, ContentRevision: sel.ContentRevision}
	a.mutateOr(library.AddClip{Clip: in}, func() {
		rp.clipBusy = false
		r.SetMarks(a.marksOf(art.ID))
		r.ClearSelection()
		a.say(rp, "已摘录")
	}, func() {
		rp.clipBusy = false
		a.say(rp, "摘录未保存")
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
		a.notify("摘录已删除", nil, restore)
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
