package app

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/locales"
)

// tabTo presses Tab until the element labelled s has the focus.
func tabTo(t *testing.T, tt *ui.Tester, s string) {
	t.Helper()
	for range 40 {
		if tt.Focused(s) {
			return
		}
		tt.Key(0, ui.KeyTab)
		tt.Frame()
	}
	t.Fatalf("Tab never reached %q", s)
}

func hasClip(a *App, id string) bool {
	return slices.ContainsFunc(a.lib.Clips, func(cl library.Clip) bool { return cl.ID == id })
}

// Deleting on the stage from the keys gives the focus to the next passage
// (which becomes the lead, built anew); put back by 撤销, the passage
// rises into place.
func TestClipStageKeyboardDelete(t *testing.T) {
	a, _ := newSeededApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("摘录"))
	settle(tt)
	cs := a.clips
	next := cs.order[1]
	a.kbd = true // as the window's input observer marks it
	a.showClip(leadClip)
	settle(tt)
	tabTo(t, tt, "删除")
	tt.Key(0, ui.KeySpace)
	settle(tt)
	if hasClip(a, leadClip) || cs.stage != nil {
		t.Fatalf("delete on the stage: clip still there %v, stage %v", hasClip(a, leadClip), cs.stage != nil)
	}
	if !tt.Focused(next.Text) {
		t.Errorf("the next passage %q should have the focus", next.Text)
	}
	if cs.seen[leadClip] {
		t.Error("a deleted passage is still remembered as here from the start: put back, it would not rise")
	}
	// 撤销, with the pointer: it rises from below into place.
	a.kbd = false
	must(t, tt.Click("撤销"))
	var ys []float32
	for range 40 {
		tt.Advance(16 * time.Millisecond)
		if r, ok := tt.Find(leadText); ok && hasClip(a, leadClip) {
			ys = append(ys, r.Y)
		}
	}
	if len(ys) < 2 || ys[0] <= ys[len(ys)-1] {
		t.Errorf("put back, the passage should rise into place: y %v", ys)
	}
}

// What the stage copied stays with that passage: turning to the next one
// shows neither 已复制 nor the wash. A copy the clipboard refuses says
// 复制失败.
func TestClipStageCopyStaysWithPassage(t *testing.T) {
	a, _ := newSeededApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("摘录"))
	settle(tt)
	must(t, tt.Click(leadText))
	settle(tt)
	cs := a.clips
	tt.ClickAt(cardCopy(a))
	tt.Frame()
	if !cs.copy.says(leadClip, tt.Now()) || cs.copy.failed {
		t.Fatalf("copy: %+v", cs.copy)
	}
	if _, ok := cs.washes["card"]; !ok {
		t.Fatal("the copy should light up what it took")
	}
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if cs.open == leadClip {
		t.Fatal("→ did not turn")
	}
	if cs.copy.says(cs.open, tt.Now()) {
		t.Error("the next passage says 已复制")
	}
	if _, ok := cs.washes["card"]; ok {
		t.Error("the wash went on over the next passage")
	}
	// Refused.
	a.kbd = false
	tt.FailClipboard(errors.New("held by another app"))
	tt.ClickAt(cardCopy(a))
	tt.Frame()
	if !cs.copy.failed || !tt.HasText("复制失败") {
		t.Errorf("a refused copy should say 复制失败: %+v", cs.copy)
	}
	if _, ok := cs.washes["card"]; ok {
		t.Error("a refused copy lit up what it did not take")
	}
}

// Deleting the only person in a column leaves the others in theirs; put
// back, they return to their own.
func TestArrangeKeepsColumns(t *testing.T) {
	ps := func(names ...string) []*person {
		var out []*person
		for _, n := range names {
			out = append(out, &person{name: n, clips: []*library.Clip{{}}})
		}
		return out
	}
	height := func(*person) (float32, bool) { return 300, true }
	l := arrange(ps("hero", "a", "b", "c"), 3, height, nil)
	kept := l.placed
	gone := arrange(ps("hero", "a", "c"), 3, height, kept)
	if gone.placed["a"] != kept["a"] || gone.placed["c"] != kept["c"] || len(gone.rest) != 3 {
		t.Fatalf("deleting b moved the others: before %v, after %v", kept, gone.placed)
	}
	back := arrange(ps("hero", "a", "b", "c"), 3, height, kept)
	if back.placed["b"] != kept["b"] {
		t.Errorf("put back, b went to column %d, not %d", back.placed["b"], kept["b"])
	}
	if lone := arrange(ps("hero", "c"), 3, height, kept); len(lone.rest) != 1 {
		t.Errorf("a lone other person should take the whole width: %d columns", len(lone.rest))
	}
}

// The copied Markdown names a Chinese title in 《》, and leaves out a
// missing link.
func TestClipMarkdownTitleAndLink(t *testing.T) {
	cl := &library.Clip{Text: "一段话"}
	cl.Citation.Title, cl.Citation.SourceName = "我的工作流", "Innei"
	got := clipMarkdown(cl, nil)
	if !strings.Contains(got, "《我的工作流》") || strings.Contains(got, "\n\n\n") || strings.Contains(got, "工作流》\n\n") {
		t.Errorf("markdown: %q", got)
	}
	cl.Citation.URL = "https://innei.in/a"
	if got := clipMarkdown(cl, nil); !strings.HasSuffix(got, "《我的工作流》\nhttps://innei.in/a\n") {
		t.Errorf("markdown with a link: %q", got)
	}
}

// The card's note is not there until the pen in its foot opens it: written
// and kept with ⌘↩, the focus goes back to the pen, whose tip does not take
// the Esc that closes the card. Pressed while the note is written, the pen
// puts it away (kept) rather than opening it anew; an empty note left with
// Esc folds away.
func TestClipNotePen(t *testing.T) {
	pinNow(t)
	a, _ := newSeededApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	tt.SetWindowActive(true)
	settle(tt)
	must(t, tt.Click("摘录"))
	settle(tt)
	must(t, tt.Click(leadText))
	settle(tt)
	cs := a.clips
	noteOf := func(id string) string {
		for _, cl := range a.lib.Clips {
			if cl.ID == id {
				return cl.Note
			}
		}
		return "?"
	}
	var bare string
	for _, cl := range cs.order {
		if cl.Note == "" {
			bare = cl.ID
			break
		}
	}
	a.kbd = true
	a.showClip(bare)
	tt.Frame()
	a.kbd = false
	settle(tt)
	if _, ok := tt.Find(locales.ClipsNote()); ok {
		t.Fatal("without a note, the card holds no place for one")
	}
	pen, ok := tt.Find("写想法")
	if !ok {
		t.Fatal("no pen")
	}
	at := func(r ui.Rect) (float32, float32) { return r.X + r.W/2, r.Y + r.H/2 }
	tt.ClickAt(at(pen))
	settle(tt)
	if !cs.note.editing {
		t.Fatal("the pen opens the note")
	}
	// Left empty, Esc folds it away; the card stays.
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if cs.note.editing || cs.stage == nil {
		t.Fatalf("Esc in an empty note: editing %v, card %v", cs.note.editing, cs.stage != nil)
	}
	if _, ok := tt.Find(locales.ClipsNote()); ok {
		t.Fatal("an empty note left folds away")
	}
	tt.ClickAt(at(pen))
	settle(tt)
	tt.Type("排版的节奏")
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyEnter)
	settle(tt)
	if cs.note.editing || noteOf(bare) != "排版的节奏" || !tt.Focused("改想法") {
		t.Fatalf("⌘↩ keeps it, the focus on the pen: editing %v, note %q", cs.note.editing, noteOf(bare))
	}
	// Written again, the pen pressed puts it away, kept.
	pen, _ = tt.Find("改想法")
	tt.ClickAt(at(pen))
	settle(tt)
	tt.Type("，再加一句")
	tt.Frame()
	x, y := at(pen)
	tt.Move(x, y)
	tt.Frame()
	tt.Press(x, y)
	tt.Frame()
	tt.Release(x, y)
	settle(tt)
	if cs.note.editing || noteOf(bare) != "排版的节奏，再加一句" {
		t.Fatalf("the pen pressed while writing: editing %v, note %q", cs.note.editing, noteOf(bare))
	}
	// From the pen, its tip showing, one Esc closes the card.
	a.kbd = true
	tabTo(t, tt, "改想法")
	tt.Advance(1200 * time.Millisecond)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if cs.open != "" {
		t.Fatal("one Esc from the pen closes the card")
	}
	tt.Advance(600 * time.Millisecond) // opened from the pointer, it travels back
	if cs.stage != nil {
		t.Fatal("the card is back in the page")
	}
}

// firstPicture is the unit and alt of the first picture with a unit of its
// own in an article's stored body.
func firstPicture(t *testing.T, store *library.Store, id string) (unit, alt string) {
	t.Helper()
	content, err := store.Content(id)
	must(t, err)
	var walk func(n map[string]any) bool
	walk = func(n map[string]any) bool {
		if props, _ := n["properties"].(map[string]any); n["tagName"] == "img" && props != nil {
			if u, _ := props["dataReaderUnit"].(string); u != "" {
				unit, _ = props["dataReaderUnit"].(string)
				alt, _ = props["alt"].(string)
				return true
			}
		}
		kids, _ := n["children"].([]any)
		for _, k := range kids {
			if m, ok := k.(map[string]any); ok && walk(m) {
				return true
			}
		}
		return false
	}
	var doc struct{ Root map[string]any }
	must(t, json.Unmarshal(content.Document, &doc))
	if !walk(doc.Root) {
		t.Fatalf("%s keeps no picture with a unit", id)
	}
	return unit, alt
}

// A passage that ran across a picture shows it on the card where it stood
// among the words; copied, the Markdown names it there and the HTML holds it.
func TestClipCardPictures(t *testing.T) {
	a, store := newSeededApp(t)
	const art = "https://innei.in/notes/2026/10/24/2026-10-recap"
	unit, alt := firstPicture(t, store, art)
	const id = "5eed0000-0000-4000-8000-000000000013"
	before, after := "The words before the picture.", "The words after it."
	lib, err := store.Mutate(library.AddClip{Clip: library.ClipInput{ID: id, ArticleID: art, Text: before + "\n\n" + after,
		CreatedAt: "2026-11-12T00:00:00.000Z", Pictures: []library.ClipPicture{{Unit: unit, At: len([]rune(before + "\n\n"))}}}})
	must(t, err)
	a.accept(lib)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("摘录"))
	settle(tt)
	a.kbd = true
	a.showClip(id)
	tt.Frame()
	a.kbd = false
	settle(tt)
	got := a.clips.pics[id][unit]
	if got.Img == nil || got.W <= 0 {
		t.Fatalf("the card did not read its picture: %+v", got)
	}
	var pic ui.Rect
	for _, n := range tt.AccessAll() {
		if n.Name == alt && n.Bounds.W > 0 && n.Bounds.X > a.clips.cardBox.X-1 {
			pic = n.Bounds
		}
	}
	b, _ := tt.Find(before)
	f, _ := tt.Find(after)
	if pic.W <= 0 || pic.W > a.clips.cardBox.W+1 || pic.Y < b.Y+b.H || pic.Y+pic.H > f.Y {
		t.Fatalf("the picture %v does not stand between %v and %v in the card %v", pic, b, f, a.clips.cardBox)
	}
	tt.ClickAt(cardCopy(a))
	settle(tt)
	data := tt.ClipboardData()
	text, _ := data.Read(transfer.Text)
	html, _ := data.Read(transfer.HTML)
	// The Markdown links to the picture saved in the passage's folder, where it stood.
	link := regexp.MustCompile(`\n>\n> !\[[^\]]*\]\(<([^>]+)>\)\n>\n> ` + regexp.QuoteMeta(after)).FindSubmatch(text)
	if link == nil || !strings.HasPrefix(string(link[1]), a.clipFolder(id)) || !strings.Contains(string(html), `<img src="data:image/`) || a.clips.copy.failed {
		t.Fatalf("copied:\n%s\n%.300s", text, html)
	}
	if fi, err := os.Stat(string(link[1])); err != nil || fi.Size() == 0 {
		t.Fatalf("the picture's file: %v", err)
	}
}

// A passage's opening keeps a line to each paragraph: a heading does not run
// into the words under it.
func TestExcerptKeepsParagraphs(t *testing.T) {
	if got := excerpt("  Better Designs \n\n  Across both studies, the teams…\n", nil); got != "Better Designs\nAcross both studies, the teams…" {
		t.Errorf("excerpt = %q", got)
	}
}
