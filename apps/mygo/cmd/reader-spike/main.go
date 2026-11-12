// Command reader-spike shows the fixture's articles in MyGo's native UI:
// their titles on the left, the chosen one's body on the right, as the
// Electron reader draws it. Selecting text and pressing ⌘E keeps a clip in
// memory; nothing is written to the library.
//
//	go run ./cmd/reader-spike -fixture ../../.context/mygo/fixtures/articles.json
//	go run ./cmd/reader-spike -fixture … -shots ../../.context/mygo/reader-spike
package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"trie/internal/reader"
	"trie/internal/theme"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type app struct {
	articles  []*reader.Article
	chosen    int
	reader    *reader.Reader // the chosen article's, once prepared
	preparing int            // the article being prepared, or -1
	positions map[string]reader.Position
	clips     []reader.Clip
	status    string
	list      ui.ScrollState
	theme     *ui.Theme
	// prepare makes an article's reader, at once in tests and screenshots,
	// on a goroutine in the window.
	prepare func(i int)
	built   time.Duration // how long the last Prepare took
}

func newApp(articles []*reader.Article) *app {
	a := &app{articles: articles, chosen: -1, preparing: -1, positions: map[string]reader.Position{}, theme: theme.UI()}
	a.prepare = func(i int) { a.show(i, a.make(i)) }
	return a
}

// make prepares article i's reader: decoding its content and pictures.
func (a *app) make(i int) *reader.Reader {
	start := time.Now()
	art := a.articles[i]
	c, err := art.Content()
	if err != nil {
		log.Print(err)
		c = &reader.Content{Status: "unavailable"}
	}
	r := reader.NewReader(art, c, reader.Prepare(c))
	a.built = time.Since(start)
	r.OnLink = func(href string) { a.status = "链接（小样不打开）：" + href }
	r.OnRetry = func() { a.status = "这次不联网：远程图片之后由后台移植取回" }
	return r
}

func (a *app) show(i int, r *reader.Reader) {
	if i != a.preparing {
		return // another article was chosen meanwhile
	}
	if p, ok := a.positions[r.Article.ID]; ok {
		r.RestorePosition(p)
	}
	a.reader, a.preparing = r, -1
}

func (a *app) choose(i int) {
	if i == a.chosen {
		return
	}
	if r := a.reader; r != nil {
		a.positions[r.Article.ID] = r.Position()
		a.clips = append(a.clips, r.Clips()...)
	}
	a.chosen, a.reader, a.preparing = i, nil, i
	a.prepare(i)
}

func (a *app) view(c *ui.Context) {
	c.SetTheme(a.theme)
	c.OnShortcut(ui.Cmd, ui.KeyE, a.keep)
	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(theme.Window).Children(func() {
		a.sidebar(c)
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Children(func() {
			switch {
			case a.reader != nil:
				a.reader.View(c)
				a.clipBar(c)
			case a.preparing >= 0:
				ui.Text(c, "正在排版…").FontSize(13).TextColor(theme.Muted).Margin(120, ui.Auto)
			default:
				ui.Text(c, "从左边选一篇").FontSize(13).TextColor(theme.Muted).Margin(120, ui.Auto)
			}
		})
	})
}

func (a *app) sidebar(c *ui.Context) {
	ui.Column(c).Width(280).Shrink(0).Background(theme.Sidebar).BorderWidth(0, 1, 0, 0).BorderColor(theme.Line).Children(func() {
		ui.Scroll(c).TrackScroll(&a.list).Grow(1).Padding(46, 8, 16, 8).Children(func() {
			for i, art := range a.articles {
				row := ui.Button(c.Key(art.ID), "").Label(art.Title).Padding(8, 12).Radius(6).Background(ui.Color{}).Border(0, ui.Color{}).
					Justify(ui.Start).Children(func() {
					ui.Text(c, art.Title).FontSize(13).LineHeight(1.45).MaxLines(2).TextColor(theme.Text2).Grow(1)
				})
				if i == a.chosen {
					row.Background(theme.Selected)
				} else if row.Hovered() {
					row.Background(theme.Hover)
				}
				if row.Clicked() {
					a.choose(i)
				}
			}
		})
		ui.Column(c).Padding(12, 16).Gap(4).BorderWidth(1, 0, 0, 0).BorderColor(theme.Line).Children(func() {
			clips := a.allClips()
			ui.Textf(c, "本次摘录 %d 条（只在内存里）", len(clips)).FontSize(12).TextColor(theme.Muted)
			for i := max(0, len(clips)-3); i < len(clips); i++ {
				ui.Text(c, clips[i].Text).FontSize(12).MaxLines(1).TextColor(theme.Text2)
			}
		})
	})
}

func (a *app) allClips() []reader.Clip {
	clips := a.clips
	if a.reader != nil {
		clips = append(clips[:len(clips):len(clips)], a.reader.Clips()...)
	}
	return clips
}

// clipBar shows under the reader while text is selected, as the Electron
// reader's bar shows over it.
func (a *app) clipBar(c *ui.Context) {
	sel, ok := a.reader.Selection()
	if !ok && a.status == "" {
		return
	}
	ui.Row(c).Height(48).Padding(0, 16).Gap(8).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(theme.Line).
		Background(theme.Elevated).FontSize(13).Children(func() {
		if ok {
			unit := "无段落"
			if sel.UnitID != nil {
				unit = *sel.UnitID
			}
			ui.Textf(c, "已选 %d 字 · 起于 %s", utf8.RuneCountInString(sel.Text), unit).TextColor(theme.Text2).Grow(1).SingleLine()
			if ui.PrimaryButton(c, "摘录 ⌘E").Clicked() {
				a.keep()
			}
			if ui.Button(c, "复制").Clicked() {
				c.Services().WriteClipboard(sel.Text)
				a.status = "已复制"
			}
			return
		}
		ui.Text(c, a.status).TextColor(theme.Text2).Grow(1).SingleLine()
		if ui.Button(c, "好").Clicked() {
			a.status = ""
		}
	})
}

func (a *app) keep() {
	if a.reader == nil {
		return
	}
	if cl, ok := a.reader.Keep(time.Now()); ok {
		unit := "null"
		if cl.UnitID != nil {
			unit = *cl.UnitID
		}
		a.status = fmt.Sprintf("已摘录 · %s · %q", unit, ellipsis(cl.Text, 24))
	}
}

func ellipsis(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func main() {
	fixture := flag.String("fixture", "", "the articles.json the fixture exported")
	shots := flag.String("shots", "", "write screenshots of the representative articles here and quit, without a window")
	bench := flag.String("bench", "", "open the article whose title starts with this, scroll it to its end in a window and quit; MYGO_FRAME_STATS=all logs each frame")
	flag.Parse()
	if *fixture == "" {
		log.Fatal("reader-spike: -fixture is required")
	}
	articles, err := reader.LoadFixture(*fixture)
	if err != nil {
		log.Fatal(err)
	}
	if *shots != "" {
		if err := screenshots(articles, *shots); err != nil {
			log.Fatal(err)
		}
		return
	}
	a := newApp(articles)
	mygo.App.WhenReady(func() {
		var win *mygo.Window
		a.prepare = func(i int) {
			go func() {
				r := a.make(i)
				win.Update(func() { a.show(i, r) })
			}()
		}
		win = mygo.NewWindow(mygo.WindowOptions{
			Title: "Trie 正文小样", Width: 1280, Height: 820, MinWidth: 900, MinHeight: 480,
			Content: ui.View(a.view),
		})
		if *bench != "" {
			go scrollThrough(a, win, find(articles, *bench))
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// scrollThrough opens article i and scrolls it 30 DIPs every 8 ms, about
// as fast as a flick on a touchpad, to its end; then the app quits.
func scrollThrough(a *app, win *mygo.Window, i int) {
	done := make(chan bool)
	step := func(fn func() bool) bool {
		win.Update(func() { done <- fn() })
		return <-done
	}
	step(func() bool { a.choose(i); return true })
	for !step(func() bool { return a.reader != nil }) {
		time.Sleep(50 * time.Millisecond)
	}
	start := time.Now()
	time.Sleep(500 * time.Millisecond) // the first frames
	for step(func() bool {
		r := a.reader
		r.Scroll.Y = min(r.Scroll.Y+30, r.Scroll.MaxY)
		return r.Scroll.Y < r.Scroll.MaxY
	}) {
		time.Sleep(8 * time.Millisecond)
	}
	log.Printf("reader-spike: scrolled %q to its end in %v", a.articles[i].Title, time.Since(start).Round(time.Millisecond))
	time.Sleep(300 * time.Millisecond)
	mygo.App.Quit()
}
