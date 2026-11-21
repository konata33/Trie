package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/fetch"
	"trie/internal/reading"
)

// An article whose page was kept by older rules is read again behind the
// reader when it is opened, not when a row is only rested on. A body mended
// in the background comes into the open reader at the same place while it is
// near its top; read down the page, once the reader is left.
func TestOpenedReadsBehind(t *testing.T) {
	a, store := newTestApp(t)
	var fetched atomic.Int32
	told := a.opt.Reading.Mended
	if told == nil {
		t.Fatal("the app is not told of a body mended behind the reader")
	}
	a.opt.Reading = reading.New(store, func() fetch.Fetch {
		return func(context.Context, string, func(string) bool, int) (*fetch.Response, error) {
			fetched.Add(1)
			return nil, errOffline
		}
	}, reading.Worker)
	a.opt.Reading.Mended = told
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("Anthony Fu"))
	settle(tt)
	var id, title string
	for _, art := range a.lib.Articles {
		if r, ok := tt.Find(art.Title); art.SourceID == "antfu" && art.BodyStatus != "not_extracted" && ok && r.Y >= 60 && r.Y <= 800 {
			id, title = art.ID, art.Title
			break
		}
	}
	if id == "" {
		t.Fatal("no kept article of Anthony Fu's on screen")
	}
	kept, err := store.Content(id)
	if err != nil || kept == nil {
		t.Fatalf("no kept body: %v", err)
	}
	older := 2
	kept.SourceKind, kept.Work, kept.ImageCacheVersion = "page", nil, &older
	must(t, store.SaveContent(*kept))
	r, _ := tt.Find(title)
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(400 * time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	if n := fetched.Load(); n != 0 {
		t.Fatalf("resting on the row read the page again (%d fetches)", n)
	}
	must(t, tt.Click(title))
	settle(tt)
	for i := 0; fetched.Load() == 0 && i < 300; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if fetched.Load() == 0 {
		t.Fatal("opened, the page kept by older rules was not read again")
	}
	rp := a.readers[id]
	if rp == nil || rp.r == nil {
		t.Fatal("the reader did not open")
	}
	rp.r.Scroll.Y = 120
	told(id)
	settle(tt)
	if rp.mended || rp.r == nil || rp.r.Scroll.Y != 120 {
		t.Fatalf("near its top, the mended body comes in at the same place: %v %+v", rp.mended, rp.r)
	}
	// Read down the page, it waits until the reader is left.
	shown := rp.r
	rp.r.Scroll.Y = 2000
	told(id)
	settle(tt)
	if !rp.mended || rp.r != shown {
		t.Fatal("read down the page, the words changed under the eye")
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if rp.mended || rp.r == shown {
		t.Fatal("left, the reader did not take in the mended body")
	}
}
