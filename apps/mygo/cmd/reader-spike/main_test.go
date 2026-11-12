package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trie/internal/reader"

	"github.com/egoist/mygo/ui"
)

// Choosing a title shows its body; a selection brings the clip bar, and ⌘E
// keeps the clip in memory. Going to another article and back puts the
// reader where it was.
func TestSpike(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", ".context", "mygo", "fixtures", "articles.json")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no fixture at %s", path)
	}
	articles, err := reader.LoadFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(articles)
	tt := ui.NewTester(a.view, 1280, 820)
	if err := tt.Click("Async, Sync, in Between"); err != nil {
		t.Fatal(err)
	}
	var para string
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "In modern programming") {
			para = s
		}
	}
	p, ok := tt.Find(para)
	if !ok {
		t.Fatalf("the body did not show: %q", tt.Texts())
	}
	tt.Press(p.X+1, p.Y+8)
	tt.Move(p.X+200, p.Y+8)
	tt.Release(p.X+200, p.Y+8)
	if _, ok := tt.Find("摘录 ⌘E"); !ok {
		t.Fatal("no clip bar for the selection")
	}
	tt.Key(ui.Cmd, ui.KeyE)
	if clips := a.allClips(); len(clips) != 1 || !strings.HasPrefix(clips[0].Text, "In modern") {
		t.Fatalf("clips %+v", clips)
	}
	if !strings.HasPrefix(a.status, "已摘录") {
		t.Errorf("status %q", a.status)
	}
	a.reader.Scroll.Y = 1500
	tt.Frame()
	if err := tt.Click("Hello Tokyo!"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Async, Sync, in Between"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if y := a.reader.Scroll.Y; y < 1499 || y > 1501 {
		t.Errorf("back at %.0f, left at 1500", y)
	}
	if len(a.allClips()) != 1 {
		t.Errorf("%d clips after going back, want the one kept", len(a.allClips()))
	}
}
