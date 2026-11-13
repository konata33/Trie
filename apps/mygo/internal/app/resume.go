package app

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/theme"
	"trie/internal/web"
)

// 接着读 on 首页: the articles left half-read, wherever they were opened,
// each hanging its moon in the margin and opening with the words the
// reader stopped at, the way back into them. No heading: the moons say it.

// resumable are the articles left half-read in the last thirty days, the
// last one left first, three at most.
func (a *App) resumable(now time.Time) []*library.Article {
	var out []*library.Article
	for i := range a.lib.Articles {
		art := &a.lib.Articles[i]
		if !inProgress(art) || art.ReadingPosition == nil {
			continue
		}
		if ms, ok := web.JSDateParse(art.ReadingPosition.UpdatedAt); ok && now.Sub(local(ms)) < 30*24*time.Hour {
			out = append(out, art)
		}
	}
	slices.SortStableFunc(out, func(x, y *library.Article) int {
		return strings.Compare(y.ReadingPosition.UpdatedAt, x.ReadingPosition.UpdatedAt)
	})
	return out[:min(3, len(out))]
}

// stopped is the words art was left at (stoppedAt), read from its stored
// body off the window's thread once; "" until then, and when there are none.
func (a *App) stopped(art *library.Article) string {
	pos := art.ReadingPosition
	if pos == nil || pos.UnitID == nil {
		return ""
	}
	hs := a.homeSt()
	key := art.ID + "#" + *pos.UnitID
	if s, ok := hs.stopped[key]; ok || a.warming || a.opt.Store == nil {
		return s
	}
	hs.stopped[key] = ""
	store, id, unit := a.opt.Store, art.ID, *pos.UnitID
	a.do(func() func() {
		var text string
		if c, err := store.Content(id); err == nil && c != nil {
			text = stoppedAt(c.Document, unit)
		}
		return func() { hs.stopped[key] = text }
	})
	return ""
}

// stoppedAt is the text of the unit a reading position names in a stored
// document, or, when that unit has no words of its own (a picture, its
// caption, code), of the first one after it that has.
func stoppedAt(doc json.RawMessage, unit string) string {
	var d struct {
		Units []struct{ ID, Kind, Text string } `json:"units"`
	}
	if json.Unmarshal(doc, &d) != nil {
		return ""
	}
	at := slices.IndexFunc(d.Units, func(u struct{ ID, Kind, Text string }) bool { return u.ID == unit })
	if at < 0 {
		return ""
	}
	for _, u := range d.Units[at:] {
		text := strings.Join(strings.Fields(u.Text), " ")
		if u.Kind != "image" && u.Kind != "caption" && u.Kind != "code" && text != "" {
			return text
		}
	}
	return ""
}

// resumeView is 接着读: up to three columns, each under a hairline as the
// people followed are, its moon hanging in the margin beside the words the
// reader stopped at, an ellipsis before them; under them, faint, whose and
// which. Without those words, how the article opens.
func (a *App) resumeView(c *ui.Context, arts []*library.Article, live bool) {
	hs := a.homeSt()
	p := page{kind: "home"}
	ui.Grid(c.Key("resume")).ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1)).GapX(48).Margin(0, 0, 64, 0).Role(ui.RoleRegion).Label(locales.HomeResumeRegion()).
		Children(func() {
			for _, art := range arts {
				cell := ui.Box(c.Key(art.ID)).MinWidth(0).BorderWidth(1, 0, 0, 0).BorderColor(theme.Line)
				cell.Children(func() {
					b := ui.ButtonBase(c.Key("open")).Label(locales.HomeResume(titleOf(art.Title), percentOf(art))).Column().Gap(12).AlignItems(ui.Start).
						Padding(24, 0, 0, 0).Radius(6).FocusRing(false).FillWidth()
					r := b.LayoutBounds()
					a.layInto(p, art.ID, r)
					if b.Clicked() && live {
						hs.resumed, hs.shown = art.ID, ""
						a.openAs(c, art.ID, r, resumeTitle, theme.Label)
					}
					if live && hs.focus == art.ID {
						hs.focus = ""
						b.Focus().ScrollIntoView()
					}
					if b.FocusVisible() {
						b.FocusRing(true).FocusRingOffset(4)
					}
					a.press(b, motion.Squashed)
					hover := b.Hovered()
					// The moon hangs in the margin, level with the first line of the words.
					a.moonOf(c, art, "").Absolute().Left(-26).Top(24 + 9)
					b.Children(func() {
						words := a.stopped(art)
						if words != "" {
							words = "…" + words
						} else if pv := art.Preview; pv != nil && pv.Excerpt != nil {
							words = *pv.Excerpt
						}
						if words != "" {
							face, scale := reader.PassageFace(words)
							color := theme.Text2
							if hover {
								color = theme.Text
							}
							ui.Text(c.Key("words"), words).Font(face).FontSize(18 * scale).LineHeight(1.65).MaxLines(3).Ellipsis("…").Pretty().
								TextColor(b.AnimateColor("words", color, a.ease(motion.Hover)))
						}
						src := a.sourceOf(art)
						ui.Row(c).Gap(8).AlignItems(ui.Center).MinWidth(0).MaxWidthPercent(100).TextColor(theme.Muted).Children(func() {
							a.avatar(c, nameOf(src), a.iconOf(src), 16)
							ui.Text(c, nameOf(src)).FontSize(theme.Label).LineHeight(16.0 / 12).FontWeight(500).TextColor(theme.Text2).SingleLine().Shrink(0)
							t := resumeTitle(c, titleOf(art.Title)).Shrink(1).MinWidth(0)
							a.homeTitle(t, art.ID, titleLook{resumeTitle, theme.Label}, "resume")
						})
					})
				})
			}
		})
}

func resumeTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(theme.Label).LineHeight(16.0 / 12).SingleLine().Ellipsis("…")
}
