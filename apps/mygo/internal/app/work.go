package app

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/reading"
	"trie/internal/theme"
	"trie/internal/video"
)

// A design gallery's work: its recording or first
// screen on the screenshot ground, its parts as a contact sheet, who made it
// with the way to the real site, and the labelled facts after the words.

// workImage decodes a data: picture of a work, once.
func (a *App) workImage(key string, data *string) *ui.Bitmap {
	if data == nil || !strings.HasPrefix(*data, "data:image/") {
		return nil
	}
	if t, ok := a.thumbs["work:"+key]; ok {
		return t.img
	}
	t := &thumb{done: true}
	if _, b64, ok := strings.Cut(*data, ";base64,"); ok {
		if b, err := base64.StdEncoding.DecodeString(b64); err == nil {
			if w := reader.WebP(b); w != nil {
				t.img = ui.NewBitmap(w)
			} else {
				t.img, _ = ui.DecodeBitmap(b)
			}
		}
	}
	a.thumbs["work:"+key] = t
	return t.img
}

// A work's pictures, as its strip lists them and its first screen shows them: s is 0 for its own first screen and i+1 for
// shot i.

// recorded says whether rp's work has a recording that plays.
func recorded(rp *readerPage) bool {
	w := rp.work
	return w.Video != nil && *w.Video != "" && !rp.rec.failed
}

// stageSet is the pictures of rp's work from first to n: from 0 when the work has a first screen of its own (its picture,
// that picture on its way, or its recording), else from 1; n is how many shots it has.
func (a *App) stageSet(rp *readerPage) (first, n int) {
	w := rp.work
	if a.workImage(rp.id+":poster", w.Poster) == nil && !rp.lateWait[reading.Poster] && !recorded(rp) {
		first = 1
	}
	return first, len(w.Sections)
}

// stageImage is picture s, nil until it comes, and the part that fetches it late.
func (a *App) stageImage(rp *readerPage, s int) (*ui.Bitmap, reading.Part) {
	if s == 0 {
		return a.workImage(rp.id+":poster", rp.work.Poster), reading.Poster
	}
	return a.workImage(rp.id+":"+strconv.Itoa(s-1), rp.work.Sections[s-1].Image), reading.Part(s - 1)
}

// stageName is what picture s is called: 录屏 or 首屏 for the work's own, a shot as the gallery names it.
func stageName(rp *readerPage, s int) string {
	switch {
	case s > 0:
		return rp.work.Sections[s-1].Name
	case recorded(rp):
		return locales.WorkRecording()
	}
	return locales.WorkHomepageShot()
}

// stagePic is picture s as the viewer shows it; without a picture if it did not load.
func (a *App) stagePic(rp *readerPage, s int) reader.Pic {
	name := stageName(rp, s)
	if img, _ := a.stageImage(rp, s); img != nil {
		return reader.NewPic(s+1, img, name, name)
	}
	return reader.Pic{ID: s + 1, Alt: name, Caption: name}
}

// staged is the picture the first screen shows: the one picked in the strip, or else the work's own, or else the first
// shot that came or is on its way; -1 for none.
func (a *App) staged(rp *readerPage) int {
	first, n := a.stageSet(rp)
	if rp.picked && rp.show >= first && rp.show <= n {
		return rp.show
	}
	for s := first; s <= n; s++ {
		if img, part := a.stageImage(rp, s); img != nil || rp.lateWait[part] || s == 0 {
			return s
		}
	}
	return -1
}

// workLate fetches the pictures of a work the read did not get to within its
// wait: the first screen, the recording and the shots,
// two at a time; each takes its place as it lands.
func (a *App) workLate(rp *readerPage) {
	if rp.lateAsked || rp.work == nil || a.warming {
		return
	}
	rp.lateAsked = true
	w, id, svc := rp.work, rp.id, a.opt.Reading
	var parts []reading.Part
	if w.Poster == nil && w.PosterSource.Value != nil && *w.PosterSource.Value != "" {
		parts = append(parts, reading.Poster)
	}
	if w.Video == nil && w.VideoSource.Value != nil && *w.VideoSource.Value != "" {
		parts = append(parts, reading.Video)
	}
	for i, s := range w.Sections {
		if s.Image == nil && s.Source.Value != nil && *s.Source.Value != "" {
			parts = append(parts, reading.Part(i))
		}
	}
	if len(parts) == 0 || svc == nil {
		return
	}
	rp.lateWait, rp.lateAt = map[reading.Part]bool{}, map[reading.Part]time.Time{}
	two := make(chan struct{}, 2)
	for _, part := range parts {
		rp.lateWait[part] = true
		a.do(func() func() {
			if !a.opt.Sync {
				two <- struct{}{}
				defer func() { <-two }()
			}
			data, _ := svc.WorkMedia(bg, id, part)
			return func() {
				delete(rp.lateWait, part)
				if data == "" || rp.work != w {
					return
				}
				if part == reading.Video {
					w.Video = &data // played as the page next builds
					return
				}
				if part == reading.Poster {
					w.Poster = &data
					delete(a.thumbs, "work:"+id+":poster")
				} else {
					w.Sections[part].Image = &data
					delete(a.thumbs, "work:"+id+":"+strconv.Itoa(int(part)))
				}
				rp.lateAt[part] = time.Time{} // timed from the frame it shows
			}
		})
	}
}

// lateIn is a late picture coming in, from 0 and 4 out of focus to rest,
// the opacity it rests at, over d from the frame it first shows; untouched
// for one that came with the read.
func (a *App) lateIn(c *ui.Context, rp *readerPage, part reading.Part, e ui.Element, rest float32, d time.Duration) {
	at, ok := rp.lateAt[part]
	if !ok || a.still(c) {
		return
	}
	if at.IsZero() {
		at = c.Now()
		rp.lateAt[part] = at
	}
	k := float32(c.Now().Sub(at)) / float32(d)
	if k >= 1 {
		delete(rp.lateAt, part)
		return
	}
	k = motion.Ease(k)
	e.Opacity(rest * k)
	if b := 4 * (1 - k); b > 0.05 {
		e.Blur(b)
	}
	c.AnimationFrame()
}

// waitingGround is a picture's place breathing softly while it is on its
// way, between the tile and the floating ground; still under less motion.
func (a *App) waitingGround(c *ui.Context, e ui.Element) {
	if a.still(c) {
		e.Background(theme.Surface)
		return
	}
	t := e.Loop("wait", 3200*time.Millisecond, ui.Linear)
	e.Background(theme.Surface.Mix(theme.Elevated, breathe(t)))
}

// workHero builds the work's first screen, its strip of shots and who made it, one under another. Each part's top margin
// collapses with what is above it, as CSS margins do: with the head's 36, a notice's 24 (afterNotice), or the part before's
// none. It returns the margin under the last part, for the body's to collapse with: 36, the head's, when there is none.
func (a *App) workHero(c *ui.Context, rp *readerPage, art *library.Article, afterNotice bool) float32 {
	w := rp.work
	a.workLate(rp)
	gap, above := float32(36), map[bool]float32{false: 36, true: 24}[afterNotice]
	top := func(margin, under float32) float32 {
		margin, above, gap = max(0, margin-above), 0, under
		return margin
	}
	ui.Column(c.Key("work")).Children(func() {
		first, n := a.stageSet(rp)
		v := &a.viewer
		if v.rp == rp && v.shot > 0 {
			v.land(c.Now(), n-first+1)
			if v.open {
				rp.show, rp.picked, rp.pickAt = v.shot-1+first, true, time.Time{} // the first screen follows the viewer
			}
		}
		s := a.staged(rp)
		// The recording plays over the work's own first screen alone: another picked, it lets go, and starts over once
		// the work's own is back.
		playing := s == 0 && recorded(rp)
		if !playing && (rp.rec.player != nil || rp.rec.still != nil || rp.rec.opening) {
			rp.rec.close()
		}
		if s >= 0 {
			a.workStage(c, rp, s, first, playing, top(32, 0))
		}
		if n > first {
			a.workStrip(c, rp, s, first, n, top(24, 0))
		}
		var by []string
		if w.Designer != nil && *w.Designer != "" {
			by = append(by, locales.WorkDesignedBy(*w.Designer))
		}
		if w.BuiltWith != nil && *w.BuiltWith != "" {
			by = append(by, locales.WorkBuiltWith(*w.BuiltWith))
		}
		site := w.Site != nil && *w.Site != ""
		if len(by) == 0 && !site {
			return
		}
		ui.Row(c.Key("by")).Margin(top(16, 24), 0, 0, 0).AlignItems(ui.Center).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Muted).Children(func() {
			ui.Text(c, strings.Join(by, " · "))
			if site {
				if len(by) > 0 {
					ui.Text(c, "·").Margin(0, 8).Opacity(0.6).AccessHidden(true)
				}
				host := ""
				if w.Host != nil {
					host = *w.Host
				}
				// The way to the real site: its underline a hairline, then the arrow.
				b := ui.ButtonBase(c.Key("site")).Label(host).Gap(4).AlignItems(ui.Center).Radius(6).FocusRing(false)
				color, line := theme.Text2, theme.Line
				if b.Hovered() {
					color, line = theme.Text, theme.Faint
				}
				if b.FocusVisible() {
					b.FocusRing(true)
				}
				if b.Clicked() {
					a.openExternal(c, *w.Site)
				}
				b.TextColor(b.AnimateColor("fg", color, a.ease(motion.LinkHover))).Children(func() {
					ui.Text(c, host).Underline().UnderlineOffset(3).DecorationColor(b.AnimateColor("line", line, a.ease(motion.LinkHover)))
					a.icon(c, "openArrow", 16, false)
				})
				a.tip(c, b, locales.WorkOpenInBrowserTip(), false)
			}
		})
	})
	return gap
}

// workStage is the work's first screen: picture s on the screenshot ground and shadow, or its place breathing while it
// is on its way, with the recording over the work's own as it plays (playing). The frame keeps the shape of the work's
// first picture, its own first screen or else its first shot, and the pictures picked fill it from the top. A picture
// picked by the pointer grows up out of the strip into it, over the strip, while the one shown goes out of focus
// beneath; from the keys
// it is simply there, and under less motion it comes into focus where it shows. A click on it shows it large in the
// viewer, which walks through all the pictures and leaves its place empty meanwhile.
func (a *App) workStage(c *ui.Context, rp *readerPage, s, first int, playing bool, margin float32) {
	w, v, now := rp.work, &a.viewer, c.Now()
	img, part := a.stageImage(rp, s)
	waiting := img == nil && rp.lateWait[part]
	frame, _ := a.stageImage(rp, first)
	if frame == nil {
		frame = img
	}
	ratio := float32(16.0 / 10)
	if frame != nil {
		pw, ph := frame.Size()
		ratio = max(1, float32(pw)/float32(max(1, ph))) // no taller than it is wide: a long page shows its top
	}
	name := stageName(rp, s)
	alt, coming := name, locales.WorkShotLoading(name)
	if s == 0 {
		alt, coming = locales.WorkHomepageShotOf(w.Name), locales.WorkHomepageShotLoading(w.Name)
	}
	// Over the strip, which a picture picked grows up out of.
	hero := ui.Box(c.Key("poster")).FillWidth().AspectRatio(ratio).Radius(10).Margin(margin, 0, 0, 0).Background(theme.Surface).
		Shadow(0, 0, 0, 1, theme.LineSoft).Shadow(0, 16, 32, -16, theme.ShadowShot).ZIndex(1)
	if waiting {
		hero.Role(ui.RoleStatus).Label(coming)
		a.waitingGround(c, hero)
	}
	box := hero.LayoutBounds()
	rp.stageBox = box
	hero.Children(func() {
		if img != nil {
			pic := ui.Image(c.Key(s), img).Absolute().Fit(ui.Cover).ObjectPosition(0.5, 0).IgnoreInput(true).Label(alt)
			picked := s == rp.show && rp.picked && now.Sub(rp.pickAt) < time.Second
			k := float32(now.Sub(rp.pickAt)) / float32(motion.TravelIn)
			switch {
			case a.still(c):
				pic.Fill().Radius(10).Transition(a.tr(c, motion.StageStill(picked)))
			case picked && rp.flies && k < 1 && box.W > 0:
				// It grows up out of its place in the strip, from the first screen's corner, so that it scrolls
				// with the page: from the strip's 16:10 and 6 corners to the first screen's frame and 10.
				e, from := motion.TravelEase(k), rp.pickFrom
				at := func(a, b float32) float32 { return a + (b-a)*e }
				pic.Left(at(from.X, 0)).Top(at(from.Y, 0)).Size(at(from.W, box.W), at(from.H, box.H)).Radius(at(6, 10)).
					Transition(a.tr(c, motion.Stage()))
				c.AnimationFrame()
			default:
				pic.Fill().Radius(10).Transition(a.tr(c, motion.Stage()))
			}
			a.lateIn(c, rp, part, pic, 1, 420*time.Millisecond)
			if v.rp == rp && v.shot > 0 && (v.open || now.Before(v.until)) {
				pic.Opacity(0)
				v.place = pic.LayoutBounds()
			}
		}
		if playing {
			a.workRecording(c, rp, hero, ratio)
			return
		}
		b := ui.ButtonBase(c.Key("zoom")).Label(locales.ReaderViewLargerAlt(name)).Absolute().Fill().Radius(10).Center().TextColor(theme.Faint).
			Cursor(ui.CursorZoomIn).FocusRing(false)
		if b.FocusVisible() {
			b.FocusRing(true).FocusRingOffset(-2)
		}
		if b.Clicked() {
			*v = viewer{open: true, rp: rp, shot: s - first + 1, label: locales.ViewerScreenshots(), run: v.run + 1, since: now, instant: a.kbd}
		}
		if img == nil && !waiting {
			b.Children(func() { a.icon(c, "brokenImage", 20, false) })
		}
	})
}

// workStrip is the strip of the work's pictures, first to n, under its first screen: a contact sheet, with an amber
// line under s, the one the first screen shows, that slides along to the one picked. It rests with a picture at its
// start. A picture that came is picked by a click,
// Enter or Space; one on its way breathes in its place, and one that did not load cannot be picked.
func (a *App) workStrip(c *ui.Context, rp *readerPage, s, first, n int, margin float32) {
	tw := (a.column() - 32) / 5
	rp.r.Aside(c, "shots").ScrollSnap(ui.SnapProximity).Margin(margin, 0, 0, 0).Scrollbars(false).Label(locales.WorkScreenshotsOf(rp.work.Name)).Children(func() {
		ui.Row(c).Gap(8).Padding(0, 0, 4, 0).Children(func() {
			for i := first; i <= n; i++ {
				img, part := a.stageImage(rp, i)
				name := stageName(rp, i)
				waiting := img == nil && rp.lateWait[part]
				ok := img != nil || i == 0 && recorded(rp)
				label := name
				switch {
				case waiting:
					label = locales.WorkShotLoading(name)
				case !ok:
					label = locales.WorkShotFailed(name)
				}
				cur := i == s
				b := ui.ButtonBase(c.Key(i)).Label(label).Role(ui.RoleRadio).Checked(cur).Column().AlignItems(ui.Stretch).
					Justify(ui.Start).Gap(8).Width(tw).SnapAlign(ui.Start).FocusRing(false).Disabled(!ok)
				if b.Clicked() && !cur {
					rp.show, rp.picked, rp.pickAt, rp.flies = i, true, c.Now(), !a.kbd
					// Where its picture shows in the strip, from the first screen's corner.
					at, hero := b.LayoutBounds(), rp.stageBox
					rp.pickFrom = ui.Rect{X: at.X - hero.X, Y: at.Y - hero.Y, W: tw, H: tw * 10 / 16}
				}
				lit := b.Hovered() || cur
				b.Children(func() {
					f := ui.Box(c).FillWidth().AspectRatio(16.0 / 10).Radius(6).ClipX().ClipY().Background(theme.Surface).
						Center().TextColor(theme.Faint)
					if b.FocusVisible() {
						f.Shadow(0, 0, 0, 2, theme.Focus)
					} else {
						f.InsetShadow(0, 0, 0, 1, theme.LineSoft)
					}
					press := ui.Pose{}
					if b.Pressed() && !a.kbd && !cur {
						press.Scale = motion.Pressed
					}
					f.AnimatePose(press, ui.Animation{Duration: motion.WorkShot, Ease: motion.Ease})
					if waiting {
						a.waitingGround(c, f)
					}
					f.Children(func() {
						switch {
						case img != nil:
							pic := ui.Image(c, img).Fit(ui.Cover).ObjectPosition(0.5, 0).Fill()
							o := pic.AnimateTo("lit", map[bool]float32{true: 1, false: 0.88}[lit], a.ease(motion.WorkShot))
							pic.Opacity(o)
							a.lateIn(c, rp, part, pic, o, 360*time.Millisecond)
						case ok:
							a.icon(c, "play", 16, false) // a recording without its first screen's picture
						case !waiting:
							a.icon(c, "brokenImage", 16, false)
						}
					})
					ink := map[bool]ui.Color{true: theme.Text2, false: theme.Muted}[lit]
					if cur {
						ink = theme.Text
					}
					t := ui.Text(c, name).FontSize(theme.Label).LineHeight(16.0/12).SingleLine().Ellipsis("…").Margin(2, 0, 0, 0)
					t.TextColor(t.AnimateColor("ink", ink, a.ease(motion.WorkShot)))
				})
			}
			// The amber line under the picture shown, on the window's ground, where any picture's colours leave it clear: one
			// line, as wide as a shot, that slides along the strip to the picture picked on Carry, in the strip's own terms,
			// so that it scrolls with it (DESIGN.md 选中的光是一份).
			if s >= first {
				line := ui.Box(c.Key("mark")).Absolute().Top(tw*10/16 + 4).Height(2).Radius(1).Background(theme.Accent).
					IgnoreInput(true).AccessHidden(true)
				x := float32(s-first) * (tw + 8)
				var l float32
				if a.kbd || a.still(c) {
					l = line.Jump("l", x)
				} else {
					l = line.AnimateTo("l", x, motion.Carry)
				}
				line.Left(l).Width(tw)
			}
		})
	})
}

// recording is a work's recording as its page plays it: the player of the file it is kept in, or why
// there is none yet.
type recording struct {
	player  *ui.VideoPlayer
	still   *stillPlay // the player while the desktop asks for less motion
	dec     *video.Decoder
	run     int     // counts openings and lettings go: an opening let go meanwhile is dropped
	opening bool    // its file and its first frame are on their way
	failed  bool    // it does not decode: the first screen stands alone
	asked   bool    // the picture was clicked: under less motion it plays only once asked
	scale   float32 // the display's pixels to a DIP, as the picture last painted
}

// close lets the player go; shown again, the recording opens again from its start.
func (r *recording) close() {
	r.run++
	if r.player != nil {
		r.player.Close()
	}
	if r.still != nil {
		close(r.still.quit)
	}
	r.player, r.still, r.dec, r.opening = nil, nil, nil, false
}

// workRecording plays the recording over the first screen, muted and looping, on its own unless the desktop asks for less
// motion, when it waits to be asked. The whole picture is the control: it pauses the recording, which dims, and plays it on;
// the play mark shows before the first frame and on a paused picture pointed at or tabbed to. A trail of light just under the
// picture shows how far through the loop it is, brightest at its head.
func (a *App) workRecording(c *ui.Context, rp *readerPage, hero ui.Element, ratio float32) {
	rec, w := &rp.rec, rp.work
	still := a.still(c)
	hero.Draw(func(p *ui.Painter, _ ui.Rect) { rec.scale = p.Scale() })
	fade := func(d time.Duration) ui.Animation {
		if still {
			return ui.Animation{Duration: time.Nanosecond} // no transitions under less motion
		}
		return a.ease(d)
	}
	p, sp := rec.player, rec.still
	started := p != nil || sp != nil
	paused := p != nil && !p.Playing() || sp != nil && sp.paused
	if started {
		var v ui.Element
		if sp != nil {
			v = ui.Image(c, sp.show(c))
		} else {
			v = ui.Video(c, p)
			if !still && !a.kbd {
				v.Opacity(v.Keyframes("in", rise(0, motion.WorkIn, motion.Ease), ui.KeyframeOptions{NonMotion: true}))
			}
		}
		v.Absolute().Fill().Fit(ui.Cover).Radius(10).AccessHidden(true)
		// Paused, it dims: brightness(0.7), black at 0.3 over it (ShadowFloat is black at 0.5).
		dim := ui.Box(c).Absolute().Fill().Radius(10).Background(theme.ShadowFloat.Alpha(0.6)).IgnoreInput(true).AccessHidden(true)
		dim.Opacity(dim.AnimateTo("dim", map[bool]float32{true: 1, false: 0}[paused], fade(motion.WorkDim)))
	}
	label := locales.WorkPlayRecording(w.Name)
	if started && !paused {
		label = locales.WorkPauseRecording(w.Name)
	}
	b := ui.ButtonBase(c.Key("recording")).Label(label).Role(ui.RoleToggleButton).Checked(started && !paused).Absolute().Fill().
		Radius(10).Center().FocusRing(false)
	if b.FocusVisible() {
		b.FocusRing(true).FocusRingOffset(-2)
	}
	if b.Clicked() {
		switch {
		case sp != nil:
			sp.paused = !sp.paused
		case p == nil:
			rec.asked = true
		case p.Playing():
			p.Pause()
		default:
			p.Play()
		}
	}
	// Only the page showing opens its recording: the page left, built on for its exit, does not take it back.
	if !started && !rec.opening && (rec.asked || !still) && !a.warming && a.page == (page{"reader", rp.id}) {
		if rec.scale == 0 {
			c.AnimationFrame() // its size in pixels is known once the picture has painted
		} else {
			px := a.column() * rec.scale
			a.openRecording(rp, *w.Video, int(px), int(px/ratio), c.Preferences().ReduceMotion)
		}
	}
	mark := !started || paused && (b.Hovered() || b.FocusVisible())
	shown := b.AnimateTo("mark", map[bool]float32{true: 1, false: 0}[mark], fade(motion.WorkMark))
	size := map[bool]float32{true: 1, false: 0.92}[mark]
	if b.Pressed() && !a.kbd {
		size = motion.Pressed
	}
	size = b.AnimateTo("markSize", size, fade(motion.WorkMarkGrow))
	b.Children(func() {
		if shown < 0.001 {
			return
		}
		ui.Box(c).Size(48, 48).Radius(24).Center().TextColor(theme.Text).Background(theme.Scrim).InsetShadow(0, 0, 0, 1, theme.Line).
			Shadow(0, 0, 0, 1, theme.ShadowEdge).Shadow(0, 12, 32, 0, theme.ShadowFloat).Opacity(shown).TransformOrigin(0.5, 0.5).
			Scale(size, size).Children(func() { a.icon(c, "play", 20, false) })
	})
	// The trail, on the dark just under the picture: faint where the loop began.
	var progress float32
	if rec.dec != nil {
		progress = rec.dec.Progress()
	}
	lit := map[bool]float32{true: 1, false: 0}[started]
	if paused {
		lit = 0.45
	}
	thick := map[bool]float32{true: 3, false: 2}[started && !paused && b.Hovered()]
	h := b.AnimateTo("trailHeight", thick, fade(motion.WorkTrailGrow))
	trail := ui.Box(c).Absolute().Left(0).Bottom(-8).WidthPercent(100 * progress).Height(h).IgnoreInput(true).AccessHidden(true).
		Opacity(b.AnimateTo("trail", lit, fade(motion.WorkTrail))).LinearGradient(ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{
		{Color: theme.Text.Alpha(0), At: 0}, {Color: theme.Text.Alpha(0.16), At: 0.45}, {Color: theme.Text.Alpha(0.7), At: 1}}})
	trail.Children(func() {
		head := map[bool]float32{true: 0.4, false: 1}[paused]
		ui.Box(c).Absolute().Right(-2).Top(h/2-2).Size(4, 4).Radius(2).Background(theme.Text).Shadow(0, 0, 4, 1, theme.Text.Alpha(0.4)).
			Opacity(b.AnimateTo("trailHead", head, fade(motion.WorkTrail)))
	})
}

// openRecording opens the recording off the window's thread: the file it is kept in, a decoder of frames covering w×h
// pixels and its player, which decodes the first frame at once; still plays it as stillPlay does. One page's recording
// stays open: another's lets go.
func (a *App) openRecording(rp *readerPage, data string, w, h int, still bool) {
	for _, other := range a.readers {
		if other != rp {
			other.rec.close()
		}
	}
	rec, dir := &rp.rec, a.opt.DataDir
	rec.opening = true
	run := rec.run
	a.do(func() func() {
		var p *ui.VideoPlayer
		var sp *stillPlay
		path, err := recordingFile(dir, data)
		var d *video.Decoder
		if err == nil {
			d, err = video.Open(path, w, h)
		}
		switch {
		case err != nil:
		case still:
			sp = playStill(d)
		default:
			if p, err = ui.NewVideoPlayer(d); err != nil {
				d.Close()
			}
		}
		return func() {
			if rec.run != run {
				if p != nil {
					p.Close()
				}
				if sp != nil {
					close(sp.quit)
				}
				return
			}
			rec.opening = false
			if err != nil {
				// A recording that does not play: the first screen stays, and nothing is said.
				log.Printf("trie: the recording of %s does not play: %v", rp.id, err)
				rec.failed = true
				return
			}
			rec.dec, rec.still = d, sp
			if p != nil {
				rec.player = p.SetLoop(true)
			}
		}
	})
}

// stillPlay plays a recording asked for, its picture clicked, while the desktop asks for less motion, where ui.Video
// holds a video at its first frame: a goroutine decodes it a frame ahead, and the view shows each
// frame as it comes due, on time while it shows, and stops as it goes from view or out of sight.
type stillPlay struct {
	bmp    *ui.Bitmap
	next   chan ui.VideoFrame
	quit   chan struct{}
	due    time.Time
	paused bool
}

func playStill(d *video.Decoder) *stillPlay {
	sp := &stillPlay{next: make(chan ui.VideoFrame), quit: make(chan struct{})}
	go func() {
		defer d.Close()
		var bufs [3]*image.RGBA // the frame showing, the one waiting and the one decoding, each drawn into again two frames on
		for i := 0; ; i = (i + 1) % 3 {
			f, err := d.Next()
			if err == io.EOF {
				if err = d.Rewind(); err == nil {
					f, err = d.Next()
				}
			}
			if err != nil {
				return // it shows the frame it was at, as a player does
			}
			img := f.Image.(*image.RGBA)
			if bufs[i] == nil || bufs[i].Rect != img.Rect {
				bufs[i] = image.NewRGBA(img.Rect)
			}
			copy(bufs[i].Pix, img.Pix)
			select {
			case sp.next <- ui.VideoFrame{Image: bufs[i], Duration: f.Duration}:
			case <-sp.quit:
				return
			}
		}
	}()
	return sp
}

// show takes the frame due, once decoded, and asks for the frame the next one is due in; it returns the bitmap to show.
func (sp *stillPlay) show(c *ui.Context) *ui.Bitmap {
	now, hidden := c.Now(), c.WindowHidden()
	if sp.bmp == nil || !sp.paused && !hidden && !now.Before(sp.due) {
		select {
		case f := <-sp.next:
			if sp.bmp == nil {
				sp.bmp = ui.NewBitmap(f.Image)
			} else {
				sp.bmp.Update(f.Image)
			}
			sp.due = now.Add(f.Duration)
		default:
			sp.due = now.Add(time.Second / 60) // still decoding: looked for again a frame on
		}
	}
	if !sp.paused && !hidden {
		c.After(max(0, sp.due.Sub(now)))
	}
	return sp.bmp
}

// recordingFile is the file a recording kept as a data URL plays from, as AVFoundation reads files and tells an mp4 by its
// name: in the data folder's recordings, named by its content and written once.
func recordingFile(dataDir, data string) (string, error) {
	sum := sha1.Sum([]byte(data))
	dir := filepath.Join(dataDir, "recordings")
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+".mp4")
	now := time.Now()
	if os.Chtimes(path, now, now) == nil {
		return path, nil // kept before, and now used last
	}
	_, b64, ok := strings.Cut(data, ";base64,")
	body, err := base64.StdEncoding.DecodeString(b64)
	if !ok || err != nil {
		return "", errors.New("the recording is not a data URL")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "*.part")
	if err != nil {
		return "", err
	}
	_, err = tmp.Write(body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	// ponytail: the eight used last stay, the rest go as another comes; the library keeps every recording anyway.
	if files, err := filepath.Glob(filepath.Join(dir, "*.mp4")); err == nil && len(files) > 8 {
		at := func(f string) time.Time {
			info, err := os.Stat(f)
			if err != nil {
				return time.Time{}
			}
			return info.ModTime()
		}
		slices.SortFunc(files, func(x, y string) int { return at(y).Compare(at(x)) })
		for _, f := range files[8:] {
			if f != path {
				os.Remove(f)
			}
		}
	}
	return path, nil
}

// techMark is the brand mark of a technology as a gallery names it ("Nuxt.js", "GSAP", "three.js"), nil when there is none.
func techMark(name string) *ui.SVG {
	key := techAliases[strings.NewReplacer(" ", "", ".", "", "-", "", "_", "").Replace(strings.ToLower(name))]
	if key == "" {
		return nil
	}
	if m, ok := techMarks[key]; ok {
		return m
	}
	m, err := ui.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="currentColor" d="` + techPaths[key] + `"/></svg>`))
	if err != nil {
		m = nil
	}
	techMarks[key] = m
	return m
}

var techMarks = map[string]*ui.SVG{}

var (
	score    = regexp.MustCompile(`^(.+?)\s+(\d+(?:\.\d+)?)$`)
	hexColor = regexp.MustCompile(`^#[\da-fA-F]{3}(?:[\da-fA-F]{3})?$`)
)

// creditOrder is the facts a work page shows under its palette, in their order; a gallery's awards (a gallery of the
// day's every entry is one), categories, tags, styles and features are left out.
var creditOrder = []string{"评分", "技术", "字体"}

type workFact struct {
	label  string
	values []string
}

// workCredits splits a work's facts into its colours and the credits shown under them.
func workCredits(w *library.GalleryWork) (palette []string, credits []workFact) {
	for _, label := range creditOrder {
		for _, f := range w.Facts {
			if f.Label == label && len(f.Values) > 0 {
				credits = append(credits, workFact{label, f.Values})
				break
			}
		}
	}
	for _, f := range w.Facts {
		if f.Label == "配色" {
			for _, v := range f.Values {
				if _, ok := swatchOf(v); ok {
					palette = append(palette, v)
				}
			}
			break
		}
	}
	return palette, credits
}

// hasWorkFacts reports whether a work has anything for workFacts to show.
func hasWorkFacts(w *library.GalleryWork) bool {
	palette, credits := workCredits(w)
	return len(palette) > 0 || len(credits) > 0
}

// swatchOf is the colour a palette value names: a hex as it is, or a colour word; gradient is reported as found
// with no colour of its own.
func swatchOf(v string) (ui.Color, bool) {
	if hexColor.MatchString(v) {
		return ui.Hex(v), true
	}
	if strings.EqualFold(v, "gradient") {
		return ui.Color{}, true
	}
	sw, ok := theme.Swatches[strings.ToLower(v)]
	return sw, ok
}

// light reports whether text on c should be dark: its relative luminance (sRGB, WCAG) above the point where dark
// and light text contrast with it equally.
func light(c ui.Color) bool {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R)+0.7152*lin(c.G)+0.0722*lin(c.B) > 0.179
}

// workFacts is the work's palette as a band of its own colours, each one copied by a click, and under it its
// credits set as a colophon: the jury's score, what it is built with, its typefaces. It returns its top and bottom
// margins, which collapse with the body's: 40 clear of the words before it.
func (a *App) workFacts(c *ui.Context, w *library.GalleryWork) (top, bottom float32) {
	palette, credits := workCredits(w)
	ui.Column(c.Key("facts")).Gap(28).Children(func() {
		if len(palette) > 0 {
			a.workPalette(c, palette)
		}
		if len(credits) == 0 {
			return
		}
		tracks := make([]ui.Track, min(3, len(credits)))
		for i := range tracks {
			tracks[i] = ui.Fr(1)
		}
		if credits[0].label == "评分" {
			tracks[0] = ui.Fixed(scoreWidth)
		}
		ui.Grid(c.Key("credits")).ColumnTracks(tracks...).GapX(32).GapY(28).Children(func() {
			for _, f := range credits {
				ui.Column(c.Key(f.label)).Gap(10).MinWidth(0).Role(ui.RoleGroup).Label(storedText(f.label)).Children(func() {
					ui.Text(c, storedText(f.label)).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted)
					a.workCredit(c, f)
				})
			}
		})
	})
	return 40, 0
}

// workPalette is the band of a work's colours, as wide as the column: each colour an equal share, its value written
// in it in the ink it takes, the one pointed at widening; a click copies the value and says so for a while.
func (a *App) workPalette(c *ui.Context, palette []string) {
	still := a.still(c)
	ui.Row(c.Key("palette")).FillWidth().Height(72).AlignItems(ui.Stretch).Radius(10).ClipX().ClipY().Shadow(0, 0, 0, 1, theme.LineSoft).
		Role(ui.RoleGroup).Label(locales.WorkPalette()).Children(func() {
		for _, v := range palette {
			sw, _ := swatchOf(v)
			gradient := strings.EqualFold(v, "gradient")
			name := v
			if !hexColor.MatchString(v) {
				name = strings.ToUpper(v[:1]) + v[1:]
			}
			b := ui.ButtonBase(c.Key(v)).Label(locales.WorkCopyColor(name)).Basis(0).MinWidth(0).FocusRing(false).Cursor(ui.CursorPointer).
				Column().Justify(ui.End).AlignItems(ui.Start).Padding(0, 12, 10, 12)
			copied := ui.Local(b, "copied", func() time.Time { return time.Time{} })
			if b.Clicked() {
				c.Services().WriteClipboard(name)
				*copied = c.Now()
			}
			held := false
			if left := motion.Copied - c.Now().Sub(*copied); left > 0 {
				held = true
				c.After(left)
			}
			lit := b.Hovered() || b.FocusVisible() || held
			grow := float32(1)
			if lit && !still && len(palette) > 1 {
				grow = 1.6
			}
			b.Grow(b.AnimateTo("grow", grow, a.ease(motion.State)))
			if gradient {
				b.LinearGradient(ui.LinearGradient{Angle: 135, Stops: []ui.GradientStop{{Color: theme.Swatches["purple"], At: 0},
					{Color: theme.Swatches["pink"], At: 0.5}, {Color: theme.Swatches["orange"], At: 1}}})
			} else {
				b.Background(sw)
			}
			if b.FocusVisible() {
				b.InsetShadow(0, 0, 0, 2, theme.Focus)
			}
			ink := theme.Text
			if !gradient && light(sw) {
				ink = theme.Window
			}
			label := name
			if held {
				label = locales.CommonCopied()
			}
			b.Children(func() {
				t := ui.Text(c, label).FontSize(theme.Label).LineHeight(16.0 / 12).FontFeatures(theme.Numbers).TextColor(ink).NoWrap()
				t.Opacity(t.AnimateTo("lit", map[bool]float32{true: 1, false: 0.72}[lit], a.ease(motion.Hover)))
			})
			a.tip(c, b, locales.WorkCopyColorTip(), false)
		}
	})
}

// workCredit is one credit's value: the score, technologies with their marks, typefaces by name.
func (a *App) workCredit(c *ui.Context, f workFact) {
	switch f.label {
	case "评分":
		workScore(c, f.values)
	default:
		for _, v := range f.values {
			ui.Row(c.Key(v)).Gap(8).AlignItems(ui.Center).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2).Children(func() {
				if f.label == "技术" {
					// A brand mark for a technology galleries name, or its initial.
					if mark := techMark(v); mark != nil {
						ui.Icon(c, mark).Size(16, 16).TextColor(theme.Text).Opacity(0.85).Shrink(0)
					} else {
						ui.Text(c, firstRune(v)).Size(16, 16).Radius(6).Background(theme.Selected).FontSize(theme.Label).FontWeight(600).
							LineHeight(16.0 / 12).TextAlign(ui.Center).TextColor(theme.Text).Shrink(0)
					}
				}
				ui.Text(c, v).SingleLine().Ellipsis("…").Shrink(1)
			})
		}
	}
}

// The score's cell: the radar of the jury's marks with the overall score at its heart, its names around it.
const (
	scoreWidth  = 300
	radarHeight = 172
	radarRadius = 62
	radarClear  = 22 // the axes start this far out, clear of the score at the centre
	// The radar reads 6 at its centre and 9 at its edge, where a gallery's picks score, so the tenths between their
	// marks show; a mark outside them rests at the centre or the edge. Its rings are the wholes.
	radarFloor = 6.0
	radarTop   = 9.0
)

// workScore is the jury's score as a radar: its marks on as many axes, the first straight up, going clockwise, each
// with its name and number at the axis's end. The overall score sits at the centre, and a dashed amber ring at its
// value is what the marks are read against: a corner past it is where the work does better than its own score, and
// those names are the brighter. Fewer than three marks are written as a list beside the score instead.
func workScore(c *ui.Context, values []string) {
	type mark struct {
		name, n string
		v       float64
	}
	var total string
	overall := math.NaN()
	var marks []mark
	for _, v := range values {
		m := score.FindStringSubmatch(v)
		if m == nil {
			continue
		}
		n, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		if m[1] == "总分" {
			total, overall = m[2], n
		} else {
			marks = append(marks, mark{m[1], m[2], n})
		}
	}
	above := func(m mark) bool { return m.v > overall }
	name := func(m mark) {
		ink := map[bool]ui.Color{true: theme.Text, false: theme.Text2}[above(m)]
		ui.Row(c.Key(m.name)).Gap(6).FontSize(theme.Label).LineHeight(16.0 / 12).Children(func() {
			ui.Text(c, storedText(m.name)).TextColor(theme.Muted)
			ui.Text(c, m.n).FontFeatures(theme.Numbers).TextColor(ink)
		})
	}
	if len(marks) < 3 {
		ui.Row(c.Key("score")).Gap(20).AlignItems(ui.Center).Children(func() {
			if total != "" {
				ui.Text(c, total).FontSize(theme.Title).LineHeight(28.0 / 24).FontWeight(500).FontFeatures(theme.Numbers).TextColor(theme.Text)
			}
			ui.Column(c.Key("marks")).Gap(4).Children(func() {
				for _, m := range marks {
					name(m)
				}
			})
		})
		return
	}
	cx, cy := float32(scoreWidth)/2, float32(radarHeight)/2
	angle := func(i int) float64 { return -math.Pi/2 + 2*math.Pi*float64(i)/float64(len(marks)) }
	point := func(i int, v float64) (float32, float32) {
		r := float32((min(radarTop, max(radarFloor, v))-radarFloor)/(radarTop-radarFloor)) * radarRadius
		return cx + r*float32(math.Cos(angle(i))), cy + r*float32(math.Sin(angle(i)))
	}
	ring := func(r ui.Rect, v func(i int) float64) *ui.Path {
		var g ui.Path
		for i := range marks {
			x, y := point(i, v(i))
			if i == 0 {
				g.MoveTo(r.X+x, r.Y+y)
			} else {
				g.LineTo(r.X+x, r.Y+y)
			}
		}
		return g.Close()
	}
	ui.Box(c.Key("radar")).Size(scoreWidth, radarHeight).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		for whole := radarFloor + 1; whole <= radarTop; whole++ {
			p.StrokePath(ring(r, func(int) float64 { return whole }), 1, map[bool]ui.Color{true: theme.Line, false: theme.LineSoft}[whole == radarTop])
		}
		for i := range marks {
			x, y := point(i, radarTop)
			a := angle(i)
			p.Line(r.X+cx+radarClear*float32(math.Cos(a)), r.Y+cy+radarClear*float32(math.Sin(a)), r.X+x, r.Y+y, 1, theme.LineSoft)
		}
		shape := ring(r, func(i int) float64 { return marks[i].v })
		p.FillPath(shape, theme.Selected)
		p.StrokePath(shape, 1.5, theme.Text2)
		if !math.IsNaN(overall) {
			p.StrokePath(ring(r, func(int) float64 { return overall }).Dash(0, 3, 3), 1, theme.Accent)
		}
		for i, m := range marks {
			x, y := point(i, m.v)
			p.Fill(ui.Rect{X: r.X + x - 2, Y: r.Y + y - 2, W: 4, H: 4}, map[bool]ui.Color{true: theme.Text, false: theme.Text2}[above(m)], 2)
		}
	}).Children(func() {
		if total != "" {
			ui.Text(c, total).Label(locales.WorkOverallScore(total)).Absolute().Left(0).Right(0).Top(cy - 10).Height(20).TextAlign(ui.Center).FontSize(theme.Item).
				LineHeight(20.0 / 15).FontWeight(600).FontFeatures(theme.Numbers).TextColor(theme.Text)
		}
		// Each name at its axis's end: above the top, below the bottom, beside the sides.
		for i, m := range marks {
			a := angle(i)
			ex, ey := cx+(radarRadius+8)*float32(math.Cos(a)), cy+(radarRadius+8)*float32(math.Sin(a))
			wn, _ := c.MeasureText(0, ui.Span{Text: storedText(m.name), Size: theme.Label})
			wv, _ := c.MeasureText(0, ui.Span{Text: m.n, Size: theme.Label})
			w, h := wn+6+wv, float32(16)
			x, y := ex-w/2, ey-h/2
			switch cos, sin := math.Cos(a), math.Sin(a); {
			case cos > 0.3:
				x = ex
			case cos < -0.3:
				x = ex - w
			case sin < 0:
				y = ey - h
			default:
				y = ey
			}
			ui.Box(c.Key(i)).Absolute().Left(max(0, min(x, scoreWidth-w))).Top(y).Children(func() { name(m) })
		}
	})
}
