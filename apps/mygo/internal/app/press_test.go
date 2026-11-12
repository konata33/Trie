package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The presses TRIE_PLAY times: an article of the people followed opens
// from 首页 (read), and page scrolls an author's keeps on their page of 收藏,
// not the authors behind it.
func TestPressReadAndKeeps(t *testing.T) {
	a, _ := newSeededApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	tt.SetScale(0.25) // a quarter of the pixels: the test looks at state, not at them
	settle(tt)
	id := ""
	for k := range a.homeSt().columns {
		id = k
	}
	if id == "" {
		t.Fatal("首页 laid out none of the people followed")
	}
	a.Press("read:" + id)
	settle(tt)
	if a.page != (page{"reader", id}) {
		t.Fatalf("read:%s went to %v", id, a.page)
	}
	a.Press("tab:favorites")
	settle(tt)
	a.Press("shelf:antfu")
	settle(tt)
	a.Press("page:400")
	settle(tt)
	if fs := a.favs(); a.page != (page{"favorites", "antfu"}) || fs.keeps.Y <= 0 || fs.shelves.Y != 0 {
		t.Fatalf("page:400 on %v scrolled the keeps to %g and the authors to %g", a.page, fs.keeps.Y, fs.shelves.Y)
	}
}
