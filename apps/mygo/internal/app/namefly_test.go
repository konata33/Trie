package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// Back from an author's page by the pointer, the name flies from the page's head back into its sidebar row, shrinking
// as it goes, and lands.
func TestAuthorNameFliesBack(t *testing.T) {
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("Innei"))
	settle(tt)
	a.Press("back")
	tt.Frame()
	if len(a.flights) == 0 {
		t.Fatalf("back: page %v, no flight", a.page)
	}
	if r := a.flights[0].w.Ratio; r <= 0.3 || r >= 0.5 {
		t.Errorf("the name shrinks by %v, not 13/32", r)
	}
	for range 60 {
		tt.Advance(16 * time.Millisecond)
	}
	if len(a.flights) != 0 {
		t.Errorf("%d flights still going", len(a.flights))
	}
}
