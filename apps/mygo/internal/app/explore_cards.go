package app

import (
	"image"
	"math"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	xdraw "golang.org/x/image/draw"

	"trie/internal/explore"
	"trie/internal/motion"
	"trie/internal/theme"
)

// The pieces of Explore's pages (Explore.tsx Lead, Frame, Paper, Story,
// SiteCard, Cover, Plate, Ink, Face; explore.css).

// shrinkTo is img at most w wide, smoothly.
func shrinkTo(img image.Image, w int) image.Image {
	b := img.Bounds()
	if b.Dx() <= w {
		return img
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, max(1, int(math.Round(float64(b.Dy())*float64(w)/float64(b.Dx()))))))
	xdraw.CatmullRom.Scale(out, out.Bounds(), img, b, xdraw.Src, nil)
	return out
}

// exFace is an author's face as Explore shows it (Face): the site's icon on
// a light disc, or the initial on a quiet one; a publication's is square.
func (a *App) exFace(c *ui.Context, e *explore.Entry, size float32, square bool, initial float32) ui.Element {
	r := size / 2
	if square {
		r = 6
	}
	// No clip: the picture rounds itself, so a face near a card's rounded corner needs no mask of the clips.
	box := ui.Box(c).Size(size, size).Radius(r).Shrink(0).AccessHidden(true).Center()
	pic := a.ex.pics["icon:"+e.ID]
	if pic == nil || pic.bmp == nil {
		name := e.Name
		if name == "" {
			name = e.Host
		}
		return box.Background(theme.Selected).Children(func() {
			ui.Text(c, firstRune(name)).FontSize(initial).FontWeight(600).LineHeight(1).TextColor(theme.Text2)
		})
	}
	return box.Background(theme.ChipLight).InsetShadow(0, 0, 0, 1, theme.LineSoft).Children(func() {
		ui.Image(c, pic.bmp).Fit(ui.Cover).Fill().Radius(r)
	})
}

func firstRune(s string) string {
	for _, r := range s {
		return initial(string(r))
	}
	return "?"
}

// band is the words-over-pictures band (DESIGN.md Words Over Pictures) of a
// box h high at top in its parent: clear at its top, rising over rise to
// the scrim, which runs under the words to the foot; under it the picture,
// blurred (pic's soft copy, drawn at the picture's place: y down, pw by ph).
// It is one picture baked for its size (bakeBand), or the scrim's gradient
// where there is no picture: no clip and no mask, which a scroll would make
// the CPU draw again in every frame.
func band(c *ui.Context, top, h, rise float32, pic *exPic, y, pw, ph float32, radius float32) {
	if h <= 0 {
		return
	}
	var box ui.Element
	if bmp := pic.band(bandKey{w: pw, h: h, top: top, rise: rise, y: y, ph: ph}); bmp != nil {
		box = ui.Image(c, bmp).Fit(ui.FillBox)
	} else {
		box = ui.Box(c).LinearGradient(bandScrim(min(1, rise/h)))
	}
	// Under the words, whether they are laid out in the flow or placed (absolute boxes paint above the flow).
	box.Absolute().Left(0).Right(0).Top(top).Height(h).ZIndex(-1).IgnoreInput(true).AccessHidden(true)
	if radius > 0 {
		box.Radius(0, 0, radius, radius)
	}
}

// intoView is how far a card has come into focus as it scrolls in (DESIGN.md
// 移焦): from 0.2 and 3 out of focus as it shows, clear once seven tenths of
// it is in; it follows the scroll.
func (a *App) intoView(e ui.Element, dy float32) {
	b := e.LayoutBounds()
	if b.H <= 0 {
		return
	}
	// dy is how far the card is drawn below where it is laid out (the middle column's half card).
	bottom := a.area.Y + a.area.H
	k := min(1, max(0, (bottom-b.Y-dy)/(0.7*b.H)))
	if k >= 1 {
		return
	}
	e.Opacity(0.2 + 0.8*k)
	if blur := 3 * (1 - k); blur > 0.05 {
		e.Blur(blur)
	}
}

// --- The design room's front page: the newest works in turn, and the
// galleries' frames stacked beside them.

func (a *App) designFront(c *ui.Context, g exGeometry, on bool) {
	ex := a.ex
	leadW := g.w - 320 - 16
	ui.Row(c.Key("front")).Height(g.front).Gap(16).Children(func() {
		a.enterIn(c, ui.Box(c.Key("lead-slot")).Width(leadW).Height(g.front), 0, on).Children(func() { a.lead(c, leadW, g.front, !on) })
		fw, fh := float32(320-12)/2, (g.front-3*12)/4
		a.enterIn(c, ui.Column(c.Key("newin")).Width(320).Height(g.front).Gap(12).Role(ui.RoleGroup).Label("设计画廊的最新作品"), 1, on).Children(func() {
			for row := range 4 {
				ui.Row(c.Key(row)).Gap(12).Children(func() {
					for col := range 2 {
						if i := row*2 + col; i < len(ex.frames) {
							a.frame(c, ex.frames[i], fw, fh)
						}
					}
				})
			}
		})
	})
}

// enterIn plays a front page's part coming into focus on Explore's first
// arrival, the n-th 40 ms after the one before (.issue.enter); the room away
// comes in as a whole.
func (a *App) enterIn(c *ui.Context, e ui.Element, n int, on bool) ui.Element {
	ex := a.ex
	if !ex.enter || !on || a.still(c) {
		return e
	}
	k := e.Keyframes("enter", rise(time.Duration(n)*40*time.Millisecond, 300*time.Millisecond, motion.Ease), ui.KeyframeOptions{Run: ex.run})
	if k < 1 {
		e.Opacity(k).Translate(0, 8*(1-k))
		if blur := 8 * (1 - k); blur > 0.05 {
			e.Blur(blur)
		}
	}
	return e
}

// rise is a value going from 0 to 1 over d, after delay, along ease.
func rise(delay, d time.Duration, ease ui.Easing) []ui.Keyframe {
	if delay <= 0 {
		return []ui.Keyframe{{Value: 0}, {At: d, Value: 1, Ease: ease}}
	}
	return []ui.Keyframe{{Value: 0}, {At: delay, Value: 0}, {At: delay + d, Value: 1, Ease: ease}}
}

// leadClock turns the lead: each work holds seven seconds while the lead is
// neither pointed at, in focus, nor out at the edge; under less motion it
// holds.
func (a *App) leadClock(c *ui.Context) {
	ex := a.ex
	now := c.Now()
	last := ex.leadAt
	if len(ex.leads) < 2 || ex.leadHeld || a.still(c) || ex.open != nil {
		ex.leadAt = time.Time{} // held: the time it waits is not the work's
		return
	}
	ex.leadAt = now
	switch gap := now.Sub(last); {
	case last.IsZero():
	case gap > leadHold:
		ex.leadLeft = 0 // back on the page, the work on show starts over (Lead mounting)
	default:
		ex.leadLeft += gap
	}
	if ex.leadLeft >= leadHold {
		ex.leadPrev, ex.leadPrevAt = ex.lead, now
		ex.lead, ex.leadLeft = (ex.lead+1)%len(ex.leads), 0
		ex.leadRun++
	}
	// The pip fills 24 across seven seconds: a frame every 140 ms moves it half a
	// pixel. Out of view, the lead only needs waking for its next work.
	if ex.leadSeen {
		c.After(140 * time.Millisecond)
	} else {
		c.After(leadHold - ex.leadLeft)
	}
}

// lead is the newest works in turn (Lead): each fades in over the last, its
// title and source on the band at its foot; the pips are its clock.
func (a *App) lead(c *ui.Context, w, h float32, away bool) {
	ex := a.ex
	box := ui.Box(c.Key("lead")).Size(w, h).Radius(10).ClipX().ClipY().Background(theme.Surface).InsetShadow(0, 0, 0, 1, theme.LineSoft).
		Role(ui.RoleRegion).Label("最新收录的作品")
	works := ex.leads
	if len(works) == 0 {
		return
	}
	ex.leadHeld = box.Hovered() || box.FocusWithin() || away
	ex.leadSeen = box.InView()
	at := min(ex.lead, len(works)-1)
	work := works[at]
	pics := make([]*exPic, len(works))
	for i, wk := range works {
		pics[i] = a.workPic(wk)
	}
	box.Children(func() {
		// The next work fades in over the last, which stays whole beneath it
		// until it is covered; the others are not drawn.
		covering := c.Now().Sub(ex.leadPrevAt) < 300*time.Millisecond
		if covering {
			c.After(300*time.Millisecond - c.Now().Sub(ex.leadPrevAt))
		}
		for i, p := range pics {
			if p == nil || p.bmp == nil || i != at && !(covering && i == ex.leadPrev) {
				continue
			}
			img := ui.Image(c.Key("slide-"+strconv.Itoa(i)), p.bmp).Fit(ui.Cover).ObjectPosition(0.5, 0).Absolute().Fill()
			if i == at {
				img.ZIndex(1)
				if ex.leadRun > 0 {
					img.Opacity(img.Keyframes("in", rise(0, 300*time.Millisecond, motion.Ease), ui.KeyframeOptions{Run: ex.leadRun, NonMotion: true}))
				}
			}
		}
		// The top shade keeps the pips readable over light works; the lower one leads the eye down into the words' band.
		ui.Box(c).Absolute().Fill().ZIndex(2).IgnoreInput(true).LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
			{Color: theme.ScrimLight, At: 0}, {Color: theme.ScrimLight.Alpha(0), At: 0.16}, {Color: theme.ScrimLight.Alpha(0), At: 0.4}, {Color: theme.ScrimLight, At: 1}}})
		says := ui.ButtonBase(c.Key("says")).Label(work.title).Absolute().Fill().ZIndex(2).Radius(10).FocusRing(false)
		a.reach(c, says, work.entry.ID, work.url)
		if says.Clicked() {
			a.openPost(c, work.entry, work.post(), nil)
		}
		if says.FocusVisible() {
			says.FocusRing(true).FocusRingOffset(-2)
		}
		hover := says.Hovered()
		says.Children(func() {
			// The words and their band at the foot: 32 in, 24 up; the band is
			// as tall as the words were the frame before.
			fromH := float32(20)
			titleW := min(760, w-64)
			wordsH := ex.leadWords
			if wordsH <= 0 {
				wordsH = 40 + 12 + fromH
			}
			band(c, h-24-wordsH-96, wordsH+24+96, 96, pics[at], 0, w, h, 10)
			words := ui.Column(c.Key("words")).Absolute().Left(32).Bottom(24).Gap(12)
			if b := words.LayoutBounds(); b.H > 0 {
				ex.leadWords = b.H
			}
			words.Children(func() {
				t := ui.Text(c.Key("title-"+strconv.Itoa(int(ex.leadRun))), work.title).FontSize(32).LineHeight(40.0 / 32).FontWeight(650).
					LetterSpacing(-0.015 * 32).Balance().MaxLines(2).TextColor(theme.Text).Width(titleW)
				if hover {
					t.Underline().UnderlineOffset(8).DecorationColor(theme.Faint)
				}
				// A new title replaces the last at once and comes up from 4 below.
				if !a.kbd && !a.still(c) {
					k := t.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: 180 * time.Millisecond, Value: 1, Ease: motion.Ease}}, ui.KeyframeOptions{Run: ex.leadRun})
					if k < 1 {
						t.Opacity(k).Translate(0, 4*(1-k))
						if b := 2 * (1 - k); b > 0.05 {
							t.Blur(b)
						}
					}
				}
				ui.Row(c.Key("from")).Gap(8).AlignItems(ui.Center).Height(fromH).FontSize(theme.Body).TextColor(theme.Text2).Children(func() {
					if p := ex.pics["icon:"+work.entry.ID]; p != nil && p.bmp != nil {
						ui.Box(c).Size(20, 20).Radius(10).Background(theme.Text).Children(func() {
							ui.Image(c, p.bmp).Fit(ui.Cover).Fill().Radius(10)
						})
					}
					line := work.entry.Name
					if work.publishedAt != nil && *work.publishedAt != "" {
						line += " · " + dateLabel(work.publishedAt)
					}
					ui.Text(c, line)
					if p := pics[at]; p != nil && len(p.dots) > 0 {
						ui.Row(c.Key("dots")).Gap(4).Margin(0, 0, 0, 8).AccessHidden(true).Children(func() {
							for _, d := range p.dots {
								ui.Box(c).Size(8, 8).Radius(4).Background(d).Shadow(0, 0, 0, 1, theme.Faint)
							}
						})
					}
				})
			})
		})
		if len(works) > 1 {
			a.pips(c, works, at)
		}
	})
}

// pips are the lead's clock: the one filling is the work on show; pressing
// one shows it.
func (a *App) pips(c *ui.Context, works []exWork, at int) {
	ex := a.ex
	ui.Row(c.Key("pips")).Absolute().Right(24).Top(16).ZIndex(3).Children(func() {
		for i, w := range works {
			b := ui.ButtonBase(c.Key(i)).Label("第 "+strconv.Itoa(i+1)+" 件："+w.title).Size(32, 20).Padding(0, 4).AlignItems(ui.Center).FocusRing(false)
			if i == at {
				b.Current(ui.CurrentItem)
			}
			if b.Clicked() && i != ex.lead {
				ex.leadPrev, ex.leadPrevAt = ex.lead, c.Now()
				ex.lead, ex.leadLeft = i, 0
				ex.leadRun++
			}
			if b.FocusVisible() {
				b.FocusRing(true)
			}
			track := theme.Faint
			if b.Hovered() {
				track = theme.Muted
			}
			b.Children(func() {
				ui.Box(c).Grow(1).Height(2).Radius(1).Background(b.AnimateColor("track", track, a.ease(160*time.Millisecond))).Children(func() {
					fill, color := float32(0), theme.Accent
					switch {
					case i < at:
						fill, color = 1, theme.Text2
					case i == at:
						fill = float32(ex.leadLeft) / float32(leadHold)
						if a.still(c) {
							fill = 1
						}
					}
					if fill > 0 {
						ui.Box(c).Height(2).Radius(1).WidthPercent(100 * min(1, fill)).Background(color)
					}
				})
			})
		}
	})
}

// frame is one of the galleries' new works (Frame): its picture, the
// gallery's badge, and its title on a band that rises when pointed at.
func (a *App) frame(c *ui.Context, w exWork, width, height float32) {
	pic := a.workPic(w)
	b := ui.ButtonBase(c.Key(w.key())).Label(w.title+"，"+w.entry.Name).Size(width, height).Radius(10).ClipX().ClipY().
		Background(theme.Surface).InsetShadow(0, 0, 0, 1, theme.LineSoft).Center().FocusRing(false)
	a.reach(c, b, w.entry.ID, w.url)
	if b.Clicked() {
		a.openPost(c, w.entry, w.post(), nil)
	}
	focus := b.FocusVisible()
	if focus {
		b.FocusRing(true).FocusRingOffset(-2)
	}
	hover := b.Hovered()
	icon := a.ex.pics["icon:"+w.entry.ID]
	b.Children(func() {
		switch {
		case pic != nil && pic.bmp != nil:
			img := ui.Image(c, pic.bmp).Fit(ui.Cover).ObjectPosition(0.5, 0).Absolute().Opacity(a.arrived(c, pic, 200*time.Millisecond))
			zoom := float32(1)
			if hover && !a.still(c) {
				zoom = 1.04
			}
			grown(img, width, height, img.AnimateTo("zoom", zoom, ui.Animation{Duration: 200 * time.Millisecond, Ease: motion.Ease}))
		case icon != nil && icon.bmp != nil:
			ui.Image(c, icon.bmp).Fit(ui.Cover).Size(32, 32).Radius(6).Opacity(0.45)
		}
		if pic == nil || !pic.done {
			a.breathing(c, theme.Hover)
		}
		if icon != nil && icon.bmp != nil {
			ui.Box(c).Absolute().Left(8).Bottom(8).Size(20, 20).Radius(10).Background(theme.Text).ZIndex(1).
				Shadow(0, 0, 0, 2, theme.ScrimLight).Children(func() { ui.Image(c, icon.bmp).Fit(ui.Cover).Fill().Radius(10) })
		}
		// The caption follows the words-over-a-picture rule; a frame is small, so its band rises over 24.
		show := float32(0)
		if hover || focus {
			show = 1
		}
		cap := ui.Box(c.Key("cap")).Absolute().Left(0).Right(0).Bottom(0).Height(48).IgnoreInput(true).ZIndex(2)
		cap.Opacity(cap.AnimateTo("show", show, a.ease(200*time.Millisecond))).Children(func() {
			band(c, 0, 48, 24, pic, -(height - 48), width, height, 0)
			ui.Text(c, w.title).Absolute().Left(32).Right(8).Bottom(8).FontSize(theme.Label).LineHeight(16.0 / 12).FontWeight(600).
				TextColor(theme.Text).SingleLine()
		})
	})
}

// breathing is a placeholder breathing while its picture is on its way.
func (a *App) breathing(c *ui.Context, color ui.Color) {
	o := float32(1)
	if !a.still(c) {
		box := ui.Box(c.Key("wait")).Absolute().Fill().Radius(10).Background(color).IgnoreInput(true)
		t := box.Loop("breathe", 2400*time.Millisecond, ui.Linear)
		o = 0.35 + 0.65*breathe(t)
		box.Opacity(o)
		return
	}
	ui.Box(c.Key("wait")).Absolute().Fill().Radius(10).Background(color).IgnoreInput(true).Opacity(o)
}

// --- The writing room's front page: a lead story, two seconds and a column
// of briefs, all in type, each with the opening of its post (Paper).

func (a *App) frontPaper(c *ui.Context, g exGeometry, on bool) {
	ex := a.ex
	items := ex.fresh
	if len(items) == 0 {
		ui.Box(c.Key("paper")).Height(g.front).Radius(10).Background(theme.Surface).InsetShadow(0, 0, 0, 1, theme.LineSoft).Center().
			Role(ui.RoleRegion).Label("博主的最新文章").Children(func() {
			ui.Text(c, "博主的新文章暂时读不到，联网后会自己更新。").MaxWidth(320).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Muted).TextAlign(ui.Center)
		})
		return
	}
	unit := (g.w - 32) / 3.3
	widths := [3]float32{unit * 1.35, unit, unit * 0.95}
	ui.Row(c.Key("paper")).Height(g.front).Gap(16).Role(ui.RoleRegion).Label("博主的最新文章").Children(func() {
		cols := [3][]exFresh{items[:1], items[min(1, len(items)):min(3, len(items))], items[min(3, len(items)):min(8, len(items))]}
		sizes := [3]string{"top", "second", "brief"}
		for i, col := range cols {
			a.enterIn(c, ui.Column(c.Key(i)).Width(widths[i]).Height(g.front).Gap(16), i, on).Children(func() {
				n := float32(max(1, len(col)))
				h := (g.front - 16*(n-1)) / n
				for _, it := range col {
					a.story(c, it, sizes[i], widths[i], h, g)
				}
			})
		}
	})
}

// story is one post on the front page, standing on its writer's ground:
// their latest picture rising from the foot, or their face's colours.
func (a *App) story(c *ui.Context, it exFresh, size string, w, h float32, g exGeometry) {
	e, post := it.entry, it.post
	pad := float32(24)
	if size == "brief" {
		pad = 16
	}
	var full *exPic
	if size != "brief" {
		full = a.fullShot(e)
	}
	card := ui.Column(c.Key(e.ID+"|"+post.URL)).Size(w, h).Padding(pad).Radius(10).ClipX().ClipY().Background(theme.Surface).
		InsetShadow(0, 0, 0, 1, theme.LineSoft).Role(ui.RoleArticle)
	hover := card.Hovered()
	gap := float32(12)
	if size == "brief" {
		gap = 8
	}
	card.Gap(gap)
	if size == "brief" {
		card.Justify(ui.SpaceBetween)
	}
	card.Children(func() {
		a.ink(c, e, full, inkStory, w, h, hover && size != "brief")
		var ts, tl float32
		var lines int
		var ex, el float32
		switch size {
		case "top":
			ts, tl, lines, ex, el = 32, 1.25, 4, 17, 1.85
			if g.cw >= 1180 {
				ts, tl = 40, 1.15
			}
		case "second":
			ts, tl, lines, ex, el = 24, 1.3, 3, 15, 1.6
		default:
			ts, tl, lines, ex, el = 15, 1.45, 2, 13, 1.5
		}
		weight := 650
		if size == "brief" {
			weight = 600
		}
		b := ui.ButtonBase(c.Key("post")).Label(post.Title).Radius(6).FocusRing(false).AlignItems(ui.Stretch).Justify(ui.Start)
		if size != "brief" {
			b.Grow(1).Shrink(1).MinHeight(0)
		}
		b.Column().Gap(map[bool]float32{true: 4, false: 12}[size == "brief"])
		a.reach(c, b, e.ID, post.URL)
		if b.FocusVisible() {
			b.FocusRing(true)
		}
		titleStyle := func(c *ui.Context, s string) ui.Element {
			return ui.Text(c, s).FontSize(ts).LineHeight(tl).FontWeight(weight).LetterSpacing(-0.015 * ts).Balance().MaxLines(lines).TextColor(theme.Text)
		}
		var title ui.Element
		b.Children(func() {
			title = titleStyle(c, post.Title)
			a.underline(title, b, b.Hovered(), theme.Faint, 0.2*ts)
			a.end(title, page{kind: "explore"}, "title:"+trialID(post.URL))
			if post.Excerpt == "" {
				return
			}
			if size == "brief" {
				ui.Text(c, post.Excerpt).FontSize(ex).LineHeight(el).TextColor(theme.Muted).MaxLines(2).Pretty()
				return
			}
			// The opening runs to the foot of its space and fades there, on a
			// scrim of the card's own colour that fades with it.
			scrim := ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{{Color: theme.Surface.Alpha(0.9), At: 0.5}, {Color: theme.Surface.Alpha(0), At: 0.9}}}
			ui.Box(c.Key("ex")).Grow(1).Shrink(1).MinHeight(0).Children(func() {
				ui.Box(c).Absolute().Top(0).Bottom(0).Left(-pad + 1).Right(-pad + 1).LinearGradient(scrim).IgnoreInput(true)
				ui.Box(c).Absolute().Fill().ClipY().Effect(storyFade).Children(func() {
					ui.Text(c, post.Excerpt).FontSize(ex).LineHeight(el).TextColor(theme.Text2).Pretty().FillWidth()
				})
			})
		})
		if b.Clicked() {
			a.openPost(c, e, post, &trialFrom{title: title.LayoutBounds(), button: b.LayoutBounds(), style: titleStyle, size: ts})
		}
		a.byline(c, e, post, size)
	})
}

// byline is a story's writer, which opens their panel, growing out of it.
func (a *App) byline(c *ui.Context, e *explore.Entry, post explore.Post, size string) {
	face, font, top := float32(20), float32(theme.Label), float32(0)
	switch size {
	case "top":
		face, font, top = 24, theme.Body, 16
	case "second":
		top = 12
	case "brief":
		face = 16
	}
	b := ui.ButtonBase(c.Key("byline")).Label(e.Name).HasPopup(ui.PopupDialog).Margin(top, 0, 0, 0).AlignSelf(ui.Start).Gap(8).
		AlignItems(ui.Center).Radius(6).FocusRing(false).MaxWidthPercent(100).FontSize(font).TextColor(theme.Muted)
	if size != "brief" {
		b.Margin(max(top, 0), 0, 0, 0)
	}
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	if a.ex.focus == "who:"+e.ID {
		a.ex.focus = ""
		b.Focus()
	}
	name := theme.Text2
	if b.Hovered() {
		name = theme.Text
	}
	b.Children(func() {
		a.exFace(c, e, face, false, map[bool]float32{true: 9, false: 11}[size == "brief"])
		ui.Text(c, e.Name).FontWeight(500).TextColor(b.AnimateColor("name", name, a.ease(140*time.Millisecond))).SingleLine().Shrink(1)
		if post.PublishedAt != nil && *post.PublishedAt != "" {
			ui.Text(c, "·").Shrink(0).AccessHidden(true)
			ui.Text(c, dateLabel(post.PublishedAt)).Shrink(0)
		}
	})
	if b.Clicked() {
		a.showSheet(c, e, "who", b.LayoutBounds())
	}
}

// The grounds an author stands on (Ink): where it shows.
type inkKind uint8

const (
	inkStory inkKind = iota // a story on the front page
	inkCover                // a writer's cover
	inkSheet                // a writer's thumbnail in their panel
)

// ink is an author's ground: their latest picture (full) rising in the
// lower half, or with none, a still halo of their face's two strongest
// colours.
func (a *App) ink(c *ui.Context, e *explore.Entry, full *exPic, kind inkKind, w, h float32, hover bool) {
	// Not clipped: the card or plate it fills clips it, and a clip inside its rounded one would be masked on the CPU.
	ui.Box(c.Key("ink")).Absolute().Fill().ZIndex(-1).IgnoreInput(true).AccessHidden(true).Children(func() {
		var ground *exPic
		if kind == inkStory && full != nil {
			ground = a.ground(e, groundStory, w, h/2)
		}
		if ground != nil && ground.bmp != nil {
			// The writer's picture rises in the lower half, blurred, under a
			// scrim of the card's colour that carries the byline.
			o := float32(0.3)
			if hover {
				o = 0.36
			}
			img := ui.Image(c.Key("site"), ground.bmp).Fit(ui.FillBox).Absolute().Left(0).Right(0).Bottom(0).Height(h / 2)
			img.Opacity(img.AnimateTo("o", o, a.ease(200*time.Millisecond)) * a.arrived(c, ground, 300*time.Millisecond))
			ui.Box(c).Absolute().Fill().LinearGradient(ui.LinearGradient{Angle: 0, Stops: []ui.GradientStop{
				{Color: theme.Scrim, At: 48 / h}, {Color: theme.Scrim.Alpha(0.85), At: 58 / h}, {Color: theme.Scrim.Alpha(0.45), At: 74 / h},
				{Color: theme.Scrim.Alpha(0.12), At: 92 / h}, {Color: theme.Scrim.Alpha(0), At: 112 / h}}})
			return
		}
		colors := grey
		if p := a.ex.pics["icon:"+e.ID]; p != nil && p.bmp != nil {
			colors = p.hues
		}
		// The two halos, baked into one picture for the box (bakeHalos): two
		// gradients each larger than the card cost the GPU more than the rest
		// of it in every frame of a scroll.
		ui.Image(c, a.ex.aura(colors, kind, w, h)).Fit(ui.FillBox).Absolute().Left(0).Top(0).Width(w).Height(h)
	})
}

// halos are an aura's two discs in a box w×h, as kind places them.
func halos(colors [2]ui.Color, kind inkKind, w, h float32) [2]haloDisc {
	switch kind {
	case inkCover:
		return [2]haloDisc{halo(colors[0], 0.32, w, h, -0.3, -0.12, 1.2, 40, false), halo(colors[1], 0.18, w, h, 0, 0.1, 1, 40, true)}
	case inkSheet:
		return [2]haloDisc{halo(colors[0], 0.3, w, h, -0.3, -0.12, 1.2, 34, false), halo(colors[1], 0.18, w, h, 0.22, -0.7, 0.62, 34, true)}
	}
	return [2]haloDisc{halo(colors[0], 0.3, w, h, -0.18, -0.55, 0.78, 34, false), halo(colors[1], 0.18, w, h, 0.22, -0.7, 0.62, 34, true)}
}

// haloDisc is a radial gradient around (cx, cy), stops out to reach.
type haloDisc struct {
	cx, cy, reach float32
	stops         []ui.GradientStop
}

// auraKey is the halos a box is baked with.
type auraKey struct {
	colors [2]ui.Color
	kind   inkKind
	w, h   float32
}

// aura is the halos of a box w×h, baked (bakeHalos) once for its colours and
// size.
func (ex *exploreState) aura(colors [2]ui.Color, kind inkKind, w, h float32) *ui.Bitmap {
	k := auraKey{colors, kind, w, h}
	if b := ex.auras[k]; b != nil {
		return b
	}
	// ponytail: the auras of every card shown are kept, a few kilobytes each,
	// until there are 256; then they are baked again as they show.
	if len(ex.auras) >= 256 || ex.auras == nil {
		ex.auras = map[auraKey]*ui.Bitmap{}
	}
	b := ui.NewBitmap(bakeHalos(halos(colors, kind, w, h), w, h))
	ex.auras[k] = b
	return b
}

// halo is one of an aura's blurred discs: right and top (or bottom) are
// fractions of the box, as CSS's percentages, size its diameter as a
// fraction of the box's width, blurred by blur.
func halo(color ui.Color, opacity, w, h, right, edge, size, blur float32, fromBottom bool) haloDisc {
	d := size * w
	r := d / 2
	cx := w - right*w - r
	cy := edge*h + r
	if fromBottom {
		cy = h - edge*h - r
	}
	reach := r + 2*blur
	// A disc blurred as a Gaussian is: its edge at half, ±1σ at 84% and 16%, ±2σ nearly all and nothing.
	stops := []ui.GradientStop{}
	for _, s := range [][2]float32{{-2, 0.977}, {-1, 0.841}, {0, 0.5}, {1, 0.159}, {2, 0}} {
		at := (r + s[0]*blur) / reach
		if at < 0 {
			continue
		}
		stops = append(stops, ui.GradientStop{Color: color.Alpha(opacity * s[1]), At: at})
	}
	if stops[0].At > 0 {
		stops = append([]ui.GradientStop{{Color: color.Alpha(opacity * float32(phi(float64(r/blur)))), At: 0}}, stops...)
	}
	return haloDisc{cx, cy, reach, stops}
}

// phi is the standard normal distribution.
func phi(x float64) float64 { return 0.5 * (1 + math.Erf(x/math.Sqrt2)) }

// --- The streams below the front pages.

// siteStream is the design room's sites, freshest first, three across, the
// middle column half a card lower; the next nine come as the end nears.
func (a *App) siteStream(c *ui.Context, g exGeometry, live bool) {
	ex := a.ex
	list := ex.sites
	n := min(ex.shown[design], len(list))
	cw := (g.cw - 32) / 3
	ch := cw * 10 / 16
	ui.Column(c.Key("sites")).Gap(16).Padding(0, 0, ch/2, 0).Role(ui.RoleRegion).Label("设计网站").Children(func() {
		for row := 0; row*3 < n; row++ {
			ui.Row(c.Key(row)).Gap(16).AlignItems(ui.Start).Children(func() {
				for col := range 3 {
					if i := row*3 + col; i < n {
						a.siteCard(c, list[i], cw, ch, col == 1)
					}
				}
			})
		}
		a.streamEnd(c, design, n < len(list), live, siteBatch)
	})
}

// coverStream is the writing room's writers, freshest first, each once:
// four across, three when the page is narrower than 1000.
func (a *App) coverStream(c *ui.Context, g exGeometry, live bool) {
	ex := a.ex
	list := ex.index
	n := min(ex.shown[writing], len(list))
	cols := 4
	if g.cw <= 1000 {
		cols = 3
	}
	if g.cw <= 760 {
		cols = 2
	}
	cw := (g.cw - float32(cols-1)*16) / float32(cols)
	ui.Column(c.Key("covers")).Gap(24).Role(ui.RoleRegion).Label("博主").Children(func() {
		for row := 0; row*cols < n; row++ {
			ui.Row(c.Key(row)).Gap(16).Children(func() {
				for col := range cols {
					if i := row*cols + col; i < n {
						a.cover(c, list[i], cw, cw*4/3)
					}
				}
			})
		}
		a.streamEnd(c, writing, n < len(list), live, coverBatch)
	})
}

// streamEnd brings the next batch as the end of a stream comes within 600 of
// showing; while it is still near, the next follows at once.
func (a *App) streamEnd(c *ui.Context, room int, more, live bool, step int) {
	if !more {
		return
	}
	ex := a.ex
	end := ui.Box(c.Key("end")).Height(1).FillWidth().AccessHidden(true)
	if !live || a.warming {
		return
	}
	y := end.LayoutBounds().Y
	if end.InViewMargin(0, 0, 600, 0) && y != ex.endY[room] {
		ex.endY[room] = y
		ex.shown[room] += step
		c.Invalidate()
	}
}

// cardState is how a card's surface stands: out in its open panel (away), or
// landing back from it (lift).
func (a *App) cardState(e *explore.Entry) (away, lift bool) {
	ex := a.ex
	return ex.open != nil && ex.open.id == e.ID && ex.open.from == "site", ex.closing == e.ID
}

// siteCard is a design site in the stream: the site itself fills the card (a
// gallery shows its own picks, turning while pointed at), and at its foot,
// on the band, its face, name and address, then the newest thing it has.
func (a *App) siteCard(c *ui.Context, e *explore.Entry, w, h float32, middle bool) {
	ex := a.ex
	picks := ex.picks[e.ID]
	gallery := len(picks) > 0
	var full *exPic
	if !gallery {
		full = a.fullShot(e)
	}
	thumb := ex.pics["thumb:"+e.ID]
	away, lift := a.cardState(e)
	card := ui.Box(c.Key(e.ID)).Size(w, h).Radius(10).Role(ui.RoleArticle).Label(e.Name)
	dy := float32(0)
	if middle {
		dy = h / 2
		card.Translate(0, dy)
	}
	if lift {
		card.ZIndex(6)
	}
	a.intoView(card, dy)
	hover := card.Hovered()
	turn := a.turning(c, e.ID, picks, hover)
	card.Children(func() {
		// Not clipped: the plate inside clips itself, and a rounded clip in another reaching into its corners is masked on the CPU.
		win := ui.ButtonBase(c.Key("window")).Label(e.Name + "：" + e.Intro).HasPopup(ui.PopupDialog).Absolute().Fill().Radius(10).FocusRing(false)
		if ex.focus == "card:"+e.ID {
			ex.focus = ""
			win.Focus()
		}
		if win.FocusVisible() {
			win.FocusRing(true)
		}
		if away {
			win.InsetShadow(0, 0, 0, 1, theme.LineSoft)
		}
		// Pressed, a card gives a little at once.
		if !a.still(c) {
			s := float32(1)
			if win.Pressed() && !a.kbd {
				s = 0.99
			}
			k := card.AnimateTo("press", s, ui.Animation{Duration: motion.Press, Ease: motion.Ease})
			card.TransformOrigin(0.5, 0.5).Scale(k, k)
		}
		zoom := a.zoom(c, card, hover)
		var shown *exPic
		win.Children(func() {
			if away {
				return
			}
			if gallery {
				var first *exPic
				if turn != nil {
					first = turn.pics[0]
				}
				a.plate(c, e, plateOpt{shot: first, has: true, bare: true, round: 10, zoom: zoom, w: w, h: h})
				shown = first
				if turn != nil {
					// The picks crossfade as they turn (300 ms); at rest the plate shows the first.
					fading := c.Now().Sub(turn.changedAt) < 300*time.Millisecond
					if fading {
						c.After(300*time.Millisecond - c.Now().Sub(turn.changedAt))
					}
					for i, p := range turn.pics {
						if p == nil || p.bmp == nil {
							continue
						}
						if i == turn.at {
							shown = p
						}
						on, off := i == turn.at && (i != 0 || fading), fading && i == turn.prev
						if !on && !off {
							continue
						}
						img := ui.Image(c.Key("pick-"+strconv.Itoa(i)), p.bmp).Fit(ui.Cover).ObjectPosition(0.5, 0).Absolute().Fill().Radius(10)
						k := img.Keyframes("swap", rise(0, 300*time.Millisecond, motion.Ease), ui.KeyframeOptions{Run: turn.run, NonMotion: true})
						if off {
							k = 1 - k
						}
						img.Opacity(k)
					}
					if turn.at < len(turn.pics) && (turn.pics[turn.at] == nil || !turn.pics[turn.at].done) {
						a.breathing(c, theme.Hover)
					}
				}
				return
			}
			a.plate(c, e, plateOpt{shot: thumb, has: true, sharp: full, round: 10, zoom: zoom, w: w, h: h})
			shown = thumb
			if full != nil && full.bmp != nil {
				shown = full
			}
		})
		if win.Clicked() {
			a.showSheet(c, e, "site", win.LayoutBounds())
		}
		// The caption: face, name, address and palette, then the newest thing.
		var post *explore.Post
		var work *exWork
		if gallery && turn != nil && turn.at < len(picks) {
			work = &picks[turn.at]
		} else if !gallery && len(e.Posts) > 0 {
			post = &e.Posts[0]
		}
		capH := float32(20)
		if post != nil || work != nil {
			capH += 22
		}
		// The caption takes the pointer only on its link; elsewhere it reaches the card.
		words := ui.Box(c.Key("cap")).Absolute().Fill().PassThrough().IgnoreInput(away)
		if away {
			words.Invisible()
		} else if lift || ex.closing == "" && ex.open == nil {
			a.backIn(c, words, e.ID)
		}
		words.Children(func() {
			band(c, h-14-capH-72, capH+14+72, 72, shown, 0, w, h, 10)
			ui.Column(c.Key("words")).Absolute().Left(16).Right(16).Bottom(14).Gap(2).Children(func() {
				ui.Row(c.Key("name")).Gap(8).AlignItems(ui.Center).Height(20).Children(func() {
					a.exFace(c, e, 20, false, 11)
					ui.Text(c, e.Name).FontSize(theme.Item).LineHeight(20.0 / 15).FontWeight(600).TextColor(theme.Text).SingleLine().Shrink(1)
					ui.Text(c, e.Host).FontSize(theme.Label).TextColor(theme.Text2).Shrink(0).NoWrap()
					if !gallery && shown != nil && len(shown.dots) > 0 {
						ui.Box(c).Grow(1)
						ui.Row(c.Key("dots")).Gap(4).Padding(0, 0, 0, 8).Children(func() {
							for _, d := range shown.dots {
								dot := ui.Box(c.Key(hexOf(d))).Size(8, 8).Radius(4).Background(d).Shadow(0, 0, 0, 1, theme.DotRing)
								a.tip(c, dot, hexOf(d), false)
							}
						})
					}
				})
				switch {
				case work != nil:
					a.capPost(c, e, work.post(), "pick-"+strconv.Itoa(turn.at))
				case post != nil:
					a.capPost(c, e, *post, "post")
				}
			})
		})
	})
}

// capPost is a card's newest thing, under its name: 13, its words a step
// brighter when pointed at.
func (a *App) capPost(c *ui.Context, e *explore.Entry, post explore.Post, key string) {
	b := ui.ButtonBase(c.Key("site-post")).Label(post.Title).AlignSelf(ui.Start).Justify(ui.Start).MaxWidthPercent(100).Padding(0, 0, 0, 28).Radius(6).FocusRing(false)
	a.reach(c, b, e.ID, post.URL)
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	color := theme.Text2
	if b.Hovered() {
		color = theme.Text
	}
	style := func(c *ui.Context, s string) ui.Element {
		return ui.Text(c, s).FontSize(theme.Body).LineHeight(20.0 / 13).SingleLine()
	}
	var title ui.Element
	b.Children(func() {
		title = style(c.Key(key), post.Title).TextColor(b.AnimateColor("fg", color, a.ease(140*time.Millisecond)))
		a.underline(title, b, b.Hovered(), theme.UnderlineHover, 4)
		if !a.kbd {
			k := title.Keyframes("swap", []ui.Keyframe{{Value: 0}, {At: 160 * time.Millisecond, Value: 1, Ease: motion.Ease}})
			if k < 1 {
				title.Opacity(k)
			}
		}
		a.end(title, page{kind: "explore"}, "title:"+trialID(post.URL))
	})
	if b.Clicked() {
		a.openPost(c, e, post, &trialFrom{title: title.LayoutBounds(), button: b.LayoutBounds(), style: func(c *ui.Context, s string) ui.Element {
			return style(c, s).TextColor(theme.Text2)
		}, size: theme.Body})
	}
}

// pickTurns is a gallery's picks, loaded on first sight and turned on the
// lead's clock while pointed at (usePicks).
type pickTurns struct {
	at, prev  int
	pics      []*exPic
	asked     int // how many are asked for: the first at once, the rest once pointed at
	since     time.Time
	hover     bool
	changedAt time.Time // when the pick on show last changed, for the crossfade
	run       uint64
}

func (a *App) turning(c *ui.Context, id string, picks []exWork, hover bool) *pickTurns {
	if len(picks) == 0 {
		return nil
	}
	t := a.ex.turns[id]
	if t == nil {
		t = &pickTurns{asked: 1}
		a.ex.turns[id] = t
	}
	if hover && !t.hover {
		t.asked, t.since = len(picks), c.Now()
	}
	was := t.at
	if !hover && t.hover {
		t.at = 0
	}
	t.hover = hover
	if len(t.pics) != len(picks) {
		t.pics = make([]*exPic, len(picks))
	}
	for i := range min(t.asked, len(picks)) {
		t.pics[i] = a.workPic(picks[i])
	}
	if hover && !a.still(c) && len(picks) > 1 {
		since := c.Now().Sub(t.since)
		t.at = int(since/pickTurn) % len(picks)
		c.After(pickTurn - since%pickTurn)
	}
	t.at = min(t.at, len(picks)-1)
	if t.at != was {
		t.prev, t.changedAt = was, c.Now()
		t.run++
	}
	return t
}

// zoom is a card's picture drifting a little closer while pointed at, slowly,
// the way a lens settles; not under less motion.
func (a *App) zoom(c *ui.Context, card ui.Element, hover bool) float32 {
	z := float32(1)
	if hover && !a.still(c) {
		z = 1.03
	}
	if a.still(c) {
		return card.Jump("zoom", 1)
	}
	return card.AnimateTo("zoom", z, ui.Animation{Duration: 400 * time.Millisecond, Ease: motion.Ease})
}

// backIn fades a card's words back in once its picture has landed (a 220 ms
// wait, then 240 ms); at once from the keys.
func (a *App) backIn(c *ui.Context, e ui.Element, id string) {
	ex := a.ex
	if ex.landed[id].IsZero() || a.kbd {
		return
	}
	t := c.Now().Sub(ex.landed[id])
	k := min(1, max(0, float32(t-220*time.Millisecond)/float32(240*time.Millisecond)))
	if k >= 1 {
		delete(ex.landed, id)
		return
	}
	e.Opacity(k)
	c.AnimationFrame()
}

// plateOpt is what a card's surface shows (Plate): its small capture (shot,
// when has: nil while it is on its way), the sharp one landing over it, or
// a writer's ground.
type plateOpt struct {
	shot, sharp *exPic
	has         bool // a shot is given (undefined in Plate: the writer's ground)
	bare        bool // a gallery's: no mark while its picture comes
	writer      bool
	round       float32
	zoom        float32
	w, h        float32
	sheet       bool
}

// plateMatch names the surface a card and its author's panel share.
type plateMatch struct{ id string }

// plate is the surface a card stands on; it travels into the author's panel
// and back (a spring that does not bounce; at once from the keys).
func (a *App) plate(c *ui.Context, e *explore.Entry, o plateOpt) ui.Element {
	p := ui.Box(c.Key("plate")).Absolute().Fill().Radius(o.round).ClipX().ClipY().Background(theme.Surface).
		InsetShadow(0, 0, 0, 1, theme.LineSoft).AccessHidden(true).Center()
	t := ui.ElementTransition{Animation: &motion.Card, Match: plateMatch{e.ID}, Fly: true, ScaleContent: true}
	p.Transition(a.tr(c, t))
	if o.zoom == 0 {
		o.zoom = 1
	}
	return p.Children(func() {
		if !o.has {
			a.ink(c, e, nil, map[bool]inkKind{true: inkSheet, false: inkCover}[o.sheet], o.w, o.h, false)
		} else if o.shot != nil && o.shot.bmp != nil && !(o.sharp != nil && !o.writer && o.sharp.bmp != nil && a.arrived(c, o.sharp, 300*time.Millisecond) >= 1) {
			// (Once the sharp one has fully covered it, the small capture is not drawn.)
			grown(ui.Image(c, o.shot.bmp).Fit(ui.Cover).ObjectPosition(0.5, 0).Absolute(), o.w, o.h, o.zoom)
		} else if !o.bare && (o.sharp == nil || o.sharp.bmp == nil) {
			if ic := a.ex.pics["icon:"+e.ID]; ic != nil && ic.bmp != nil {
				ui.Image(c, ic.bmp).Fit(ui.Cover).Size(34, 34).Radius(34 * 0.22).Opacity(0.5)
			}
		}
		// The full-size picture fades in over the small one once it has
		// loaded, so the card sharpens instead of popping.
		if o.writer {
			// A writer's latest picture sunk into the foot, dimmed and blurred,
			// baked for the plate's size (bakeGround).
			if g := a.ground(e, groundCover, o.w, o.h*0.52); g != nil && g.bmp != nil {
				gh := o.h * 0.52
				img := ui.Image(c.Key("sharp"), g.bmp).Fit(ui.FillBox).Absolute().Opacity(a.arrived(c, g, 300*time.Millisecond))
				img.Left(-o.w * (o.zoom - 1) / 2).Bottom(-gh * (o.zoom - 1) / 2).Width(o.w * o.zoom).Height(gh * o.zoom)
			}
			return
		}
		if s := o.sharp; s != nil && s.bmp != nil && s != o.shot {
			k := a.arrived(c, s, 300*time.Millisecond)
			grown(ui.Image(c.Key("sharp"), s.bmp).Fit(ui.Cover).ObjectPosition(0.5, 0).Absolute().Opacity(k), o.w, o.h, o.zoom)
		}
	})
}

// grown places a picture filling a w×h box, z times as large around its
// middle: a zoom laid out rather than a transform, which in a rounded clip
// would have the clip masked on the CPU in every frame it moves.
func grown(img ui.Element, w, h, z float32) {
	if z == 0 {
		z = 1
	}
	img.Left(-w * (z - 1) / 2).Top(-h * (z - 1) / 2).Width(w * z).Height(h * z)
}

// cover is a writer in the room's list, read like the front page: their
// name, their latest post's title and opening, then their next two. The
// card stands on their latest picture sunk into its foot, or on their
// colours; opening it lifts that ground into their page.
func (a *App) cover(c *ui.Context, e *explore.Entry, w, h float32) {
	ex := a.ex
	full := a.fullShot(e)
	away, lift := a.cardState(e)
	card := ui.Box(c.Key(e.ID)).Size(w, h).Radius(10).Role(ui.RoleArticle).Label(e.Name)
	if lift {
		card.ZIndex(6)
	}
	a.intoView(card, 0)
	hover := card.Hovered()
	var posts []explore.Post
	for _, p := range e.Posts {
		dup := false
		for _, q := range posts {
			dup = dup || q.Title == p.Title
		}
		if !dup && len(posts) < 3 {
			posts = append(posts, p)
		}
	}
	card.Children(func() {
		open := ui.ButtonBase(c.Key("open")).Label(e.Name + "：" + e.Intro).HasPopup(ui.PopupDialog).Absolute().Fill().Radius(10).FocusRing(false)
		if ex.focus == "card:"+e.ID {
			ex.focus = ""
			open.Focus()
		}
		if open.FocusVisible() {
			open.FocusRing(true)
		}
		if away {
			open.InsetShadow(0, 0, 0, 1, theme.LineSoft)
		}
		if !a.still(c) {
			s := float32(1)
			if open.Pressed() && !a.kbd {
				s = 0.99
			}
			k := card.AnimateTo("press", s, ui.Animation{Duration: motion.Press, Ease: motion.Ease})
			card.TransformOrigin(0.5, 0.5).Scale(k, k)
		}
		zoom := a.zoom(c, card, hover)
		open.Children(func() {
			if !away {
				a.plate(c, e, plateOpt{sharp: full, writer: true, round: 10, zoom: zoom, w: w, h: h})
			}
		})
		if open.Clicked() {
			a.showSheet(c, e, "site", open.LayoutBounds())
		}
		words := ui.Column(c.Key("read")).Absolute().Fill().Padding(18, 18, 16, 18).Gap(14).PassThrough().IgnoreInput(away)
		if away {
			words.Invisible()
		} else {
			a.backIn(c, words, e.ID)
		}
		words.Children(func() {
			ui.Row(c.Key("name")).Gap(8).AlignItems(ui.Center).IgnoreInput(true).Children(func() {
				a.exFace(c, e, 20, isPress(e), 11)
				ui.Text(c, e.Name).FontSize(theme.Item).LineHeight(20.0 / 15).FontWeight(600).TextColor(glow(e)).SingleLine().Shrink(1)
			})
			if len(posts) == 0 {
				text := "没有订阅源"
				if e.FeedURL != nil {
					text = "暂时读不到这个订阅源的文章"
				}
				ui.Text(c, text).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Muted)
				return
			}
			latest := posts[0]
			lb := ui.ButtonBase(c.Key("lead")).Label(latest.Title).Grow(1).Shrink(1).MinHeight(0).Column().AlignItems(ui.Stretch).Justify(ui.Start).Gap(10).
				Radius(6).FocusRing(false)
			a.reach(c, lb, e.ID, latest.URL)
			if lb.FocusVisible() {
				lb.FocusRing(true)
			}
			style := func(c *ui.Context, s string) ui.Element {
				return ui.Text(c, s).FontSize(18).LineHeight(1.35).FontWeight(650).LetterSpacing(-0.01 * 18).Balance().MaxLines(3).TextColor(theme.Text)
			}
			var title ui.Element
			lb.Children(func() {
				title = style(c, latest.Title).Shrink(0)
				a.underline(title, lb, lb.Hovered(), theme.Faint, 0.2*18)
				a.end(title, page{kind: "explore"}, "title:"+trialID(latest.URL))
				if latest.Excerpt != "" {
					ui.Box(c.Key("ex")).Grow(1).Shrink(1).MinHeight(0).ClipY().Effect(coverFade).Children(func() {
						ui.Text(c, latest.Excerpt).FontSize(14).LineHeight(1.65).TextColor(theme.Text2).Pretty().FillWidth()
					})
				} else {
					ui.Box(c).Grow(1)
				}
				if latest.PublishedAt != nil && *latest.PublishedAt != "" {
					ui.Text(c, dateLabel(latest.PublishedAt)).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).Shrink(0)
				}
			})
			if lb.Clicked() {
				a.openPost(c, e, latest, &trialFrom{title: title.LayoutBounds(), button: lb.LayoutBounds(), style: style, size: 18})
			}
			if len(posts) > 1 {
				a.toc(c, e, posts[1:], w, h)
			}
		})
	})
}

// toc is a cover's next posts at its foot, like a contents list, on the band.
func (a *App) toc(c *ui.Context, e *explore.Entry, posts []explore.Post, w, h float32) {
	full := a.ex.pics["shot:"+e.ID]
	n := float32(len(posts))
	listH := 20*n + 2*(n-1)
	ui.Column(c.Key("foot")).Margin(0, -18, -16, -18).Padding(0, 16, 16, 16).Children(func() {
		band(c, -88, listH+16+88, 88, full, -(h - listH - 16 - 88), w, h, 10)
		ui.Column(c.Key("toc")).Gap(2).Children(func() {
			for _, p := range posts {
				b := ui.ButtonBase(c.Key(p.URL)).Label(p.Title).Row().AlignItems(ui.End).Justify(ui.Start).Gap(12).FillWidth().Radius(6).FocusRing(false)
				a.reach(c, b, e.ID, p.URL)
				if b.FocusVisible() {
					b.FocusRing(true)
				}
				color := theme.Text2
				if b.Hovered() {
					color = theme.Text
				}
				style := func(c *ui.Context, s string) ui.Element {
					return ui.Text(c, s).FontSize(theme.Body).LineHeight(20.0 / 13).FontWeight(500).SingleLine()
				}
				var title ui.Element
				b.Children(func() {
					title = style(c, p.Title).Grow(1).Shrink(1).MinWidth(0).TextColor(b.AnimateColor("fg", color, a.ease(140*time.Millisecond)))
					a.underline(title, b, b.Hovered(), theme.UnderlineHover, 4)
					a.end(title, page{kind: "explore"}, "title:"+trialID(p.URL))
					if p.PublishedAt != nil && *p.PublishedAt != "" {
						ui.Text(c, dateLabel(p.PublishedAt)).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).Shrink(0).NoWrap()
					}
				})
				if b.Clicked() {
					a.openPost(c, e, p, &trialFrom{title: title.LayoutBounds(), button: b.LayoutBounds(), style: func(c *ui.Context, s string) ui.Element {
						return style(c, s).TextColor(theme.Text2)
					}, size: theme.Body})
				}
			}
		})
	})
}

// underline draws a link's line under t, in color, fading in over 140 ms
// while on (text-decoration-color from transparent); none at rest.
func (a *App) underline(t, owner ui.Element, on bool, color ui.Color, offset float32) {
	k := owner.AnimateTo("underline", map[bool]float32{true: 1, false: 0}[on], a.ease(140*time.Millisecond))
	if k > 0.01 {
		t.Underline().UnderlineOffset(offset).DecorationColor(color.Alpha(k))
	}
}

// hexOf is a colour as #RRGGBB.
func hexOf(c ui.Color) string {
	const digits = "0123456789ABCDEF"
	b := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range []uint8{c.R, c.G, c.B} {
		b[1+2*i], b[2+2*i] = digits[v>>4], digits[v&15]
	}
	return string(b)
}
