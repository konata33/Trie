package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/motion"
)

// Back by the pointer from an article opened on 首页 (a search's line) or in
// 收藏 (an author's keep), the moon there goes from where it was as the
// article was opened to where the reader left it, under less motion too;
// from the keys it is simply there.
func TestMoonWanesOnHomeAndFavorites(t *testing.T) {
	for _, from := range []string{"home", "favorites"} {
		for _, k := range []struct {
			name       string
			kbd, still bool
		}{{"pointer", false, false}, {"keys", true, false}, {"still", false, true}} {
			t.Run(from+"/"+k.name, func(t *testing.T) {
				a, _ := newSeededApp(t)
				tt := ui.NewTester(a.View, 1280, 850)
				tt.SetScale(0.25) // a quarter of the pixels: the test looks at state, not at them
				settle(tt)
				back := page{kind: "home"}
				if from == "home" {
					a.homeSt().query = "LobeHub"
					tt.Frame()
				} else {
					must(t, tt.Click("收藏"))
					settle(tt)
					toArticles(t, a, tt)
					must(t, tt.Click(inneiShelf))
					back = page{"favorites", "innei"}
				}
				settle(tt)
				must(t, tt.Click(lobeTitle))
				settle(tt)
				rp := a.readers[lobehub]
				if a.page != (page{"reader", lobehub}) || rp == nil || rp.r == nil {
					t.Fatalf("the article did not open: on %v", a.page)
				}
				a.Reduce = k.still
				rp.r.Scroll.Y = rp.r.Scroll.MaxY * 0.4
				tt.Frame()
				settle(tt)
				if lit := litOf(a.article(lobehub)); lit > 0.8 || rp.lit != 1 {
					t.Fatalf("read on: the moon %v, %v as it opened", lit, rp.lit)
				}
				if k.kbd {
					tt.Key(0, ui.KeyEscape)
				} else {
					a.Press("back")
					tt.Frame()
				}
				tt.Advance(16 * time.Millisecond)
				tt.Advance(16 * time.Millisecond)
				if a.page != back {
					t.Fatalf("back: on %v", a.page)
				}
				m := a.moonBack
				if wanes := m != nil && m.started && m.from == 1; wanes == k.kbd {
					t.Fatalf("the moon wanes %v", wanes)
				}
				tt.Advance(motion.MoonWane)
				tt.Advance(16 * time.Millisecond)
				if a.moonBack != nil {
					t.Error("the moon rests where the reader left it")
				}
			})
		}
	}
}
