package app

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/fetch"
	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/reading"
	"trie/internal/subscription"
)

// The explore catalog the app ships (resources/explore).
const exploreResources = "../../resources/explore"

// newExploreApp is newTestApp with Explore on the shipped catalog and its
// seed, offline: nothing reaches the network, so the page shows what the
// seed knows, every picture missing. subs, when set, is the subscription
// service (a fake feed for subscribing).
func newExploreApp(t testing.TB, subs func(store *library.Store) *subscription.Service) (*App, *library.Store) {
	t.Helper()
	a, store := newTestApp(t)
	dir := t.TempDir()
	read := reading.New(store, offline, reading.Worker)
	ex := explore.New(explore.Options{Resources: exploreResources, Cache: filepath.Join(dir, "explore-cache.json"),
		FetchFeed: func(context.Context, string) (library.ParsedFeed, error) { return library.ParsedFeed{}, errOffline },
		Sources: func() []library.Source {
			st, err := store.Library()
			if err != nil {
				return nil
			}
			return st.Sources
		},
		Preview: func(ctx context.Context, t reading.Target, source string) library.ArticleContent {
			return read.Preview(ctx, t, source, 0)
		},
		Upstreams: explore.Upstreams, Fetch: offline()})
	a.opt.Explore = ex
	if subs != nil {
		a.opt.Subscriptions = subs(store)
	}
	return a, store
}

// newExploreAppOn is newExploreApp on a copy of data's Explore files
// (explore-cache.json, explore-icons.json, explore-works, explore-shots), for
// pictures that were read before; still offline.
func newExploreAppOn(t testing.TB, data string) (*App, *library.Store) {
	t.Helper()
	a, store := newTestApp(t)
	dir := t.TempDir()
	for _, name := range []string{"explore-cache.json", "explore-icons.json", "explore-works", "explore-shots"} {
		if err := os.CopyFS(filepath.Join(dir, name), os.DirFS(filepath.Join(data, name))); err != nil && !os.IsNotExist(err) {
			if b, err := os.ReadFile(filepath.Join(data, name)); err == nil {
				os.WriteFile(filepath.Join(dir, name), b, 0o644)
			}
		}
	}
	read := reading.New(store, offline, reading.Worker)
	a.opt.Explore = explore.New(explore.Options{Resources: exploreResources, Cache: filepath.Join(dir, "explore-cache.json"),
		FetchFeed: func(context.Context, string) (library.ParsedFeed, error) { return library.ParsedFeed{}, errOffline },
		Sources: func() []library.Source {
			st, err := store.Library()
			if err != nil {
				return nil
			}
			return st.Sources
		},
		Preview: func(ctx context.Context, t reading.Target, source string) library.ArticleContent {
			return read.Preview(ctx, t, source, 0)
		},
		Upstreams: explore.Upstreams, Fetch: offline()})
	return a, store
}

// toExplore opens 探索 and lets the list and the crossing settle.
func toExplore(t testing.TB, a *App, tt *ui.Tester) {
	t.Helper()
	tt.Frame()
	if err := tt.Click("探索"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
}

// exploreTester is a Tester on the window at its default size, drawn at a
// quarter of its resolution: what the tests look at is laid out and hit in
// DIPs, and the CPU's painting of Explore's many frames, which dominates their
// time (twenty times over under the race detector), costs a sixteenth.
func exploreTester(a *App) *ui.Tester {
	tt := ui.NewTester(a.View, 1280, 850)
	tt.SetScale(0.25)
	return tt
}

// rest runs the clock on for what the page's motions take: the rooms' spring,
// the panel, a face's flight.
func rest(tt *ui.Tester) { tt.Advance(time.Second) }

// Two rooms: the design room first; the way across takes the rooms over on
// their spring, and back; from the keys, and under less motion, at once.
func TestExploreRooms(t *testing.T) {
	a, _ := newExploreApp(t, nil)
	tt := exploreTester(a)
	toExplore(t, a, tt)
	ex := a.ex
	if ex.room != design || len(ex.leads) == 0 || len(ex.sites) < 2*siteBatch || len(ex.index) < 2*coverBatch {
		t.Fatalf("room %d, %d leads, %d sites, %d writers", ex.room, len(ex.leads), len(ex.sites), len(ex.index))
	}
	if _, ok := tt.AccessByRole(ui.RoleRegion, "最新收录的作品"); !ok {
		t.Fatal("the lead shows")
	}
	g := a.geometryAt(850)
	must(t, tt.Click("去写作：作者的最新文章"))
	if ex.room != writing || ex.move == nil {
		t.Fatalf("the rooms go on their spring: room %d, move %v", ex.room, ex.move)
	}
	tt.Advance(120 * time.Millisecond)
	if x := ex.x; x >= 0 || x <= g.restX(writing) {
		t.Fatalf("on the way, the track stands between the rooms: %v of %v", x, g.restX(writing))
	}
	rest(tt)
	if ex.move != nil || ex.x != g.restX(writing) {
		t.Fatalf("settled at the writing room: %v, %v", ex.move, ex.x)
	}
	if _, ok := tt.Find("回到设计：设计网站和作品"); !ok {
		t.Fatal("the way back")
	}
	said := strings.Join(tt.Announcements(), "|")
	if !strings.Contains(said, "写作：作者的最新文章") {
		t.Fatalf("the crossing is read out: %q", said)
	}
	// From the keys: there at once.
	ex.focus = "door"
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	if ex.room != design || ex.move != nil || ex.x != 0 {
		t.Fatalf("Enter on the way across crosses at once: room %d, %v, %v", ex.room, ex.move, ex.x)
	}
	// Under less motion, at once too.
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Move(640, 400)
	rest(tt)
	must(t, tt.Click("去写作：作者的最新文章"))
	tt.Frame()
	if ex.room != writing || ex.move != nil || ex.x != g.restX(writing) {
		t.Fatalf("under less motion the rooms change at once: room %d, %v, %v", ex.room, ex.move, ex.x)
	}
}

// A sideways swipe drags the rooms with the fingers; letting go far enough
// crosses, carrying the fingers' speed; a short pull settles back; the tail
// of the swipe does not take them again while they move.
func TestExploreSwipe(t *testing.T) {
	a, _ := newExploreApp(t, nil)
	tt := exploreTester(a)
	toExplore(t, a, tt)
	ex := a.ex
	// A short, slow pull: the track follows the fingers, then settles back.
	for range 4 {
		tt.Scroll(700, 400, 10, 0)
		tt.Advance(30 * time.Millisecond)
	}
	if ex.x > -39 || ex.x < -41 {
		t.Fatalf("the track follows the fingers: %v", ex.x)
	}
	if ex.room != design {
		t.Fatal("a short pull does not cross yet")
	}
	tt.Advance(200 * time.Millisecond)
	rest(tt)
	if ex.room != design || ex.x != 0 {
		t.Fatalf("a short pull settles back: room %d at %v", ex.room, ex.x)
	}
	// Up and down scrolls the page, not the rooms.
	tt.Advance(time.Second)
	tt.Scroll(700, 400, 0, 120)
	tt.Frame()
	if ex.scroll.Y < 100 || ex.x != 0 {
		t.Fatalf("a vertical swipe scrolls: %v, track %v", ex.scroll.Y, ex.x)
	}
	// A long pull crosses as soon as it is a third of the way, on the spring.
	tt.Advance(time.Second)
	g := a.geometryAt(850)
	for range 12 {
		tt.Scroll(700, 400, 24, 0)
		tt.Advance(16 * time.Millisecond)
	}
	if ex.room != writing {
		t.Fatalf("a long pull crosses: room %d", ex.room)
	}
	// The rest of the swipe does not take the rooms again.
	x := ex.x
	tt.Scroll(700, 400, -40, 0)
	tt.Frame()
	if ex.room != writing || ex.sw.release != (time.Time{}) {
		t.Fatal("the tail of the swipe waits for the rooms to settle")
	}
	rest(tt)
	if ex.x != g.restX(writing) || x == g.restX(writing) {
		t.Fatalf("the rooms settle at the writing room from where the fingers left them: %v, %v", x, ex.x)
	}
	// The page keeps its depth across, easing up only as far as the room arriving reaches.
	if ex.scroll.Y < 100 {
		t.Fatalf("the page kept its depth: %v", ex.scroll.Y)
	}
}

// The streams: nine design sites at first, the next nine as the end comes
// within reach; twelve writers, then twelve more.
func TestExploreBatches(t *testing.T) {
	a, _ := newExploreApp(t, nil)
	tt := exploreTester(a)
	toExplore(t, a, tt)
	ex := a.ex
	if ex.shown[design] != siteBatch || ex.shown[writing] != coverBatch {
		t.Fatalf("the first batches: %v", ex.shown)
	}
	shows := func(e *explore.Entry) bool {
		_, ok := tt.AccessByRole(ui.RoleArticle, e.Name)
		return ok
	}
	if !shows(ex.sites[siteBatch-1]) || shows(ex.sites[siteBatch]) {
		t.Fatal("nine design sites are built")
	}
	for range 3 {
		ex.scroll.Y = ex.scroll.MaxY
		tt.Frame()
		rest(tt)
	}
	if ex.shown[design] < 2*siteBatch || !shows(ex.sites[siteBatch]) {
		t.Fatalf("the next batch came as the end neared: %d", ex.shown[design])
	}
	if ex.shown[writing] != coverBatch {
		t.Fatal("the room away keeps its batch")
	}
	ex.scroll.Y = 0
	ex.focus = "door"
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	rest(tt)
	for range 3 {
		ex.scroll.Y = ex.scroll.MaxY
		tt.Frame()
		rest(tt)
	}
	if ex.shown[writing] < 2*coverBatch {
		t.Fatalf("the writers' next batch: %d", ex.shown[writing])
	}
}

// A card opens its author's page, subscribed or not: it lists what Explore
// has of them and goes back to Explore; Esc goes back with the focus on the
// card; a byline on the front page opens the writer's page.
func TestExploreAuthorPage(t *testing.T) {
	a, _ := newExploreApp(t, nil)
	tt := exploreTester(a)
	toExplore(t, a, tt)
	ex := a.ex
	ex.scroll.Y = 700
	tt.Frame()
	rest(tt)
	// The first site not subscribed to: its page is what Explore has of them.
	e := ex.sites[slices.IndexFunc(ex.sites, func(e *explore.Entry) bool { return a.subscribedSource(e) == "" })]
	card := e.Name + "：" + e.Intro
	must(t, tt.Click(card))
	rest(tt)
	if want := a.entryPage(e); a.page != want {
		t.Fatalf("the card opens their page: %+v, want %+v", a.page, want)
	}
	if a.backTo() != (page{kind: "explore"}) || a.tabOf() != "explore" {
		t.Fatalf("their page goes back to Explore, under its tab: %+v %q", a.backTo(), a.tabOf())
	}
	if _, ok := tt.AccessByRole(ui.RoleHeading, e.Name); !ok {
		t.Fatal("their page names them")
	}
	posts := e.Posts
	if len(posts) == 0 {
		posts = e.Works
	}
	if len(posts) > 0 && !tt.HasText(posts[0].Title) {
		t.Fatalf("their page lists what Explore has of them: %q", posts[0].Title)
	}
	tt.Key(0, ui.KeyEscape)
	rest(tt)
	if a.page != (page{kind: "explore"}) {
		t.Fatalf("Esc goes back to Explore: %+v", a.page)
	}
	// From the keys there and back, the focus comes back to the card.
	ex.focus = "card:" + e.ID
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	rest(tt)
	if want := a.entryPage(e); a.page != want {
		t.Fatalf("Enter on the card opens their page: %+v", a.page)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !tt.Focused(card) {
		t.Fatal("the focus goes back to the card")
	}
	// A writer's byline on the front page opens their page.
	ex.scroll.Y = 0
	ex.focus = "door"
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	rest(tt)
	w := ex.fresh[0].entry
	tt.Move(640, 400)
	r, ok := tt.Find(w.Name)
	if !ok {
		t.Fatal("the byline shows")
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	rest(tt)
	if want := a.entryPage(w); a.page != want {
		t.Fatalf("the byline opens the writer's page: %+v, want %+v", a.page, want)
	}
}

// feedFor serves one RSS feed for every address: name's site with one post.
func feedFor(name, home string) fetch.Fetch {
	body := `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>` + name + `</title><link>` + home +
		`</link><description>d</description><item><title>A post</title><link>` + home + `/a-post</link><pubDate>Mon, 09 Nov 2026 08:00:00 GMT</pubDate>` +
		`<description>Some words.</description></item></channel></rss>`
	return func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
		return &fetch.Response{URL: url, Status: 200, Type: "application/rss+xml", Body: []byte(body)}, nil
	}
}

// Subscribing from an author's page: pressed, the pill becomes 已订阅 behind an
// amber tick and the author's row grows in the sidebar, their face flying into
// it; its name stays 订阅 <name>, its checked state says it is on. Pointed at
// again, it offers 取消订阅; pressed, they are let go, their row staying a beat
// for the face to leave before it folds. From the keys nothing flies.
func TestExploreSubscribe(t *testing.T) {
	var target *explore.Entry
	a, store := newExploreApp(t, func(store *library.Store) *subscription.Service {
		return subscription.New(store, func() fetch.Fetch { return feedFor(target.Name, target.HomeURL) }, nil, nil)
	})
	tt := exploreTester(a)
	toExplore(t, a, tt)
	ex := a.ex
	for _, e := range ex.index {
		if e.FeedURL != nil && a.subscribedSource(e) == "" && ex.pics["icon:"+e.ID] != nil && ex.pics["icon:"+e.ID].bmp != nil {
			target = e
			break
		}
	}
	if target == nil {
		t.Fatal("no writer to subscribe to")
	}
	ex.focus = "door"
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	rest(tt)
	tt.Move(640, 400)
	a.Press("card:" + target.ID)
	tt.Frame()
	rest(tt)
	pill := "订阅 " + target.Name
	must(t, tt.Click(pill))
	src := a.subscribedSource(target)
	if src == "" {
		t.Fatal("subscribed")
	}
	if len(a.flights2) != 1 || a.flights2[0].to != "chip:"+src {
		t.Fatalf("the face flies to the sidebar: %+v", a.flights2)
	}
	tt.Advance(100 * time.Millisecond)
	if !a.flyingTo("chip:" + src) {
		t.Fatal("still on its way")
	}
	rest(tt)
	if len(a.flights2) != 0 || a.writer(src) == nil {
		t.Fatal("landed, the row is there")
	}
	if acc, ok := tt.AccessByRole(ui.RoleToggleButton, pill); !ok || !acc.Checked || !tt.HasText("已订阅") {
		t.Fatal("the pill says 已订阅, and is on")
	}
	if a.ex.pills["head:"+target.ID].litAt.IsZero() {
		t.Error("no tick gathering")
	}
	if lib, _ := store.Library(); !slices.ContainsFunc(lib.Sources, func(s library.Source) bool { return s.ID == src && s.Subscribed }) {
		t.Fatal("kept in the library")
	}
	// Let go: the face flies back onto the pill, the row stays a beat, then folds.
	tt.Move(640, 400)
	tt.Advance(time.Second)
	if acc, ok := tt.AccessByRole(ui.RoleToggleButton, pill); ok {
		tt.Move(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	}
	rest(tt)
	if !tt.HasText("取消订阅") {
		t.Error("pointed at, it does not offer 取消订阅")
	}
	must(t, tt.Click(pill))
	if a.subscribedSource(target) != "" || a.parting[src] == nil {
		t.Fatalf("let go, the row stays a beat: %v", a.parting[src])
	}
	if len(a.flights2) != 1 || a.flights2[0].to != "pill:head:"+target.ID {
		t.Fatalf("the face flies back to the pill: %+v", a.flights2)
	}
	rest(tt)
	if a.parting[src] != nil || len(a.flights2) != 0 {
		t.Fatal("the row has folded, the face landed")
	}
	// From the keys nothing flies, and the row goes at once.
	must(t, tt.Click("订阅 "+target.Name))
	rest(tt)
	src = a.subscribedSource(target)
	for range 40 {
		if tt.Focused(pill) {
			break
		}
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused(pill) {
		t.Fatal("Tab reaches the pill")
	}
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if a.subscribedSource(target) != "" || len(a.flights2) != 0 || a.parting[src] != nil {
		t.Fatalf("from the keys: let go at once, nothing flies: %v %v", a.flights2, a.parting[src])
	}
}

// Subscribing that fails says of whom, why and what to do, in the service's
// own words; the pill stays as it was and nothing flies.
func TestExploreSubscribeFails(t *testing.T) {
	var target *explore.Entry
	a, _ := newExploreApp(t, func(store *library.Store) *subscription.Service {
		return subscription.New(store, func() fetch.Fetch {
			return func(_ context.Context, url string, _ func(string) bool, _ int) (*fetch.Response, error) {
				return &fetch.Response{URL: url, Status: 503, Type: "text/html", Body: []byte("down")}, nil
			}
		}, nil, nil)
	})
	tt := exploreTester(a)
	toExplore(t, a, tt)
	ex := a.ex
	for _, e := range ex.index {
		if e.FeedURL != nil && a.subscribedSource(e) == "" {
			target = e
			break
		}
	}
	ex.focus = "door"
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	rest(tt)
	a.Press("card:" + target.ID)
	tt.Frame()
	rest(tt)
	pill := "订阅 " + target.Name
	must(t, tt.Click(pill))
	rest(tt)
	if want := "没能订阅 " + target.Name + "。"; !strings.HasPrefix(a.errText, want) || len(a.errText) <= len(want) {
		t.Fatalf("the notice says %q: want it to begin %q and say why", a.errText, want)
	}
	if acc, ok := tt.AccessByRole(ui.RoleToggleButton, pill); !ok || acc.Checked || acc.Busy || len(a.flights2) != 0 {
		t.Fatal("failed, the pill is not as it was, or something flew")
	}
}

// A post opens in the reader, kept in the library as it opens, under its
// author's source, unsubscribed: 探索 is the way back; the favourite
// stands in the bar; the author does not join the sidebar. Esc goes back to
// Explore at the same depth, the focus on the post.
func TestExplorePreview(t *testing.T) {
	a, store := newExploreApp(t, nil)
	tt := exploreTester(a)
	toExplore(t, a, tt)
	ex := a.ex
	ex.focus = "door"
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	rest(tt)
	tt.Move(640, 400)
	rest(tt)
	f := ex.fresh[0]
	authors := len(a.authors)
	must(t, tt.Click(f.post.Title))
	id := a.postID(f.post.URL)
	if a.page != (page{"reader", id}) || len(a.flights) != 1 || a.lay == nil {
		t.Fatalf("the post opens in the reader, its title flying, its ground spreading: %v %d %v", a.page, len(a.flights), a.lay)
	}
	// From its card, where the writing room's track has slid it into view.
	if r, _ := tt.Find(f.post.Title); abs32(a.lay.From.X-r.X) > 1 || a.flights[0].w.From.X < r.X || a.flights[0].w.From.X > r.X+r.W {
		t.Fatalf("its title and ground set off from its card at %v: %v %v", r, a.flights[0].w.From, a.lay.From)
	}
	rest(tt)
	if _, ok := tt.Find("探索"); !ok {
		t.Fatal("the way back is 探索")
	}
	lib, _ := store.Library()
	at := slices.IndexFunc(lib.Articles, func(art library.Article) bool { return art.URL == f.post.URL })
	if at < 0 || lib.Articles[at].ID != id {
		t.Fatalf("kept in the library by its address: %v", at)
	}
	src := slices.IndexFunc(lib.Sources, func(s library.Source) bool { return s.ID == lib.Articles[at].SourceID })
	if src < 0 || lib.Sources[src].Subscribed || lib.Sources[src].DisplayName != f.entry.Name {
		t.Fatalf("under its author's source, unsubscribed: %+v", lib.Sources[src])
	}
	if len(a.authors) != authors {
		t.Fatal("the author does not join the sidebar")
	}
	if _, ok := tt.Find("收藏"); !ok {
		t.Fatal("the favourite stands in the bar")
	}
	tt.Key(0, ui.KeyS)
	rest(tt)
	if art := a.libArticle(id); art == nil || !art.Favorite {
		t.Fatal("S keeps it")
	}
	// Opened again, it is the same article.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.page.kind != "explore" || ex.room != writing {
		t.Fatalf("Esc goes back to Explore, in the room it left: %v", a.page)
	}
	tt.Frame()
	if !tt.Focused(f.post.Title) {
		t.Fatal("the focus goes back to the post")
	}
	tt.Key(0, ui.KeyEnter)
	rest(tt)
	if lib, _ := store.Library(); a.page != (page{"reader", id}) || len(lib.Articles) != len(a.lib.Articles) {
		t.Fatalf("opened again, the same article: %v", a.page)
	}
}

// Under less motion the lead holds its work, the cards neither drift closer
// nor give under the pointer, and the panel opens without its surface
// flying; the fades stay.
func TestExploreStill(t *testing.T) {
	a, _ := newExploreApp(t, nil)
	tt := exploreTester(a)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	toExplore(t, a, tt)
	ex := a.ex
	tt.Move(1000, 820)
	tt.Advance(10 * time.Second)
	if ex.lead != 0 {
		t.Fatalf("the lead holds under less motion: %d", ex.lead)
	}
	tt.SetPreferences(ui.Preferences{})
	tt.Frame()
	tt.Advance(leadHold + 200*time.Millisecond)
	if ex.lead != 1 {
		t.Fatalf("otherwise each work holds seven seconds: %d", ex.lead)
	}
	// Pointed at, the lead holds.
	tt.Move(400, 300)
	tt.Advance(leadHold + time.Second)
	if ex.lead != 1 {
		t.Fatalf("pointed at, the lead holds: %d", ex.lead)
	}
}

// A favicon.ico, which Explore's recommended writers often have, is drawn:
// the largest of its pictures, stored as PNG or as a 32-bit bitmap.
func TestDecodeICO(t *testing.T) {
	le := func(v ...int) []byte {
		var b []byte
		for _, x := range v {
			b = append(b, byte(x), byte(x>>8))
		}
		return b
	}
	ico := func(w int, entries ...[]byte) []byte {
		out := le(0, 1, len(entries))
		offset := 6 + 16*len(entries)
		for _, e := range entries {
			out = append(out, byte(w), byte(w), 0, 0)
			out = append(out, le(1, 32)...)
			out = append(out, le(len(e), 0, offset, 0)...)
			offset += len(e)
		}
		for _, e := range entries {
			out = append(out, e...)
		}
		return out
	}
	// A 2×2 bitmap, bottom row first: blue, green, red, alpha; then its mask.
	bmp := append(le(40, 0, 2, 0, 4, 0, 1, 32), make([]byte, 24)...)
	bmp = append(bmp, 0, 0, 255, 255, 0, 255, 0, 255, 255, 0, 0, 255, 10, 20, 30, 128)
	bmp = append(bmp, make([]byte, 8)...)
	img := decodeAny(ico(2, bmp))
	if img == nil || img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Fatalf("a 32-bit bitmap: %v", img)
	}
	if r, g, b, _ := img.At(0, 1).RGBA(); r>>8 != 255 || g != 0 || b != 0 {
		t.Fatalf("its bottom row comes first, red first: %d %d %d", r>>8, g>>8, b>>8)
	}
	if _, _, _, al := img.At(1, 0).RGBA(); al>>8 != 128 {
		t.Fatalf("its alpha is kept: %d", al>>8)
	}
	var png1 bytes.Buffer
	png.Encode(&png1, image.NewNRGBA(image.Rect(0, 0, 3, 3)))
	if img := decodeAny(ico(3, png1.Bytes())); img == nil || img.Bounds().Dx() != 3 {
		t.Fatalf("a PNG inside: %v", img)
	}
	if decodeAny([]byte{0, 0, 1, 0, 1, 0}) != nil {
		t.Fatal("a cut file is none")
	}
}

// TestExploreShots writes Explore's stills and motions to EXPLORE_SHOTS from
// the pictures a data folder keeps (EXPLORE_DATA, TestWarm's and
// TestLiveSites'), offline: both rooms' front pages and a stretch below, the
// author's panel opening and closing, subscribing, and a swipe across.
//
//	EXPLORE_DATA=/tmp/dir EXPLORE_SHOTS=out go test -run TestExploreShots -timeout 30m ./internal/app
func TestExploreShots(t *testing.T) {
	data, out := os.Getenv("EXPLORE_DATA"), os.Getenv("EXPLORE_SHOTS")
	if data == "" || out == "" {
		t.Skip("EXPLORE_DATA or EXPLORE_SHOTS not set")
	}
	os.MkdirAll(out, 0o755)
	var target *explore.Entry
	a, _ := newExploreAppOn(t, data)
	// As main.go wires it: a source subscribed from Explore takes the catalog's icon and name.
	svc := a.opt.Explore
	a.opt.Subscriptions = subscription.New(a.opt.Store, func() fetch.Fetch { return feedFor(target.Name, target.HomeURL) },
		func(home string) *string { return svc.IconFor(home) }, func(feedURL, home string) string { return svc.NameFor(&feedURL, home) })
	tt := ui.NewTester(a.View, 1280, 850)
	tt.SetScale(2)
	ex := a.ex
	settle(tt)
	must(t, tt.Click("探索"))
	// Pictures land as they are read from disk.
	for range 4 {
		settle(tt)
	}
	tt.Move(1000, 830)
	still := func(name string) {
		for range 2 {
			settle(tt)
		}
		savePNG(t, filepath.Join(out, name+".png"), tt.Image())
	}
	// The lead at its first work, a few seconds in, as the recorded capture was taken.
	ex.lead, ex.leadLeft = 0, 2*time.Second
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	savePNG(t, filepath.Join(out, "design-front.png"), tt.Image())
	ex.scroll.Y = 760
	tt.Frame()
	still("design-below")
	ex.scroll.Y = 0
	tt.Frame()
	settle(tt)
	must(t, tt.Click("去写作：作者的最新文章"))
	tt.Move(1000, 830)
	still("writing-front")
	ex.scroll.Y = 760
	tt.Frame()
	still("writing-below")
	// The motions, at half size, a frame every 33 ms.
	record := func(name string, span time.Duration, act func()) {
		tt.SetScale(1)
		settle(tt)
		act()
		var frames []*image.RGBA
		t0 := tt.Now()
		const step = 33 * time.Millisecond
		for d := time.Duration(0); d <= span; d += step {
			tt.Advance(d - tt.Now().Sub(t0))
			frames = append(frames, tt.Image())
		}
		writeGIF(t, filepath.Join(out, name+".gif"), frames, step)
		writeSheet(t, filepath.Join(out, name+"-frames.png"), everyOther(frames))
		tt.SetScale(2)
	}
	// A writer's cover opening their panel, and the panel closing back into it.
	ex.scroll.Y = 760
	tt.Frame()
	settle(tt)
	target = ex.index[1]
	card := target.Name + "：" + target.Intro
	r, ok := tt.Find(card)
	if !ok {
		t.Fatal("no cover for", target.Name)
	}
	// On the cover's surface, above its words (which let the pointer through to it).
	record("sheet-open", 700*time.Millisecond, func() { tt.ClickAt(r.X+r.W/2, r.Y+8) })
	still("sheet")
	record("sheet-close", 600*time.Millisecond, func() { tt.ClickAt(250, 830) })
	// Subscribing in the panel: the pill changes shape, the face flies into the sidebar's new row.
	tt.ClickAt(r.X+r.W/2, r.Y+8)
	settle(tt)
	record("subscribe", 1100*time.Millisecond, func() { must(t, tt.Click("订阅 "+target.Name)) })
	still("subscribed")
	tt.Move(1000, 830)
	settle(tt)
	record("unsubscribe", 1100*time.Millisecond, func() { must(t, tt.Click("订阅 "+target.Name)) })
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	// A swipe from the writing room back to design: the rooms follow the fingers, then the spring carries them over.
	ex.scroll.Y = 0
	tt.Frame()
	settle(tt)
	record("swipe", 1100*time.Millisecond, func() {
		for range 10 {
			tt.Scroll(700, 400, -26, 0)
			tt.Advance(16 * time.Millisecond)
		}
	})
	// And the way across pressed: the same spring.
	record("door", 900*time.Millisecond, func() { must(t, tt.Click("去写作：作者的最新文章")) })
}

// everyOther is every other frame, for a contact sheet.
func everyOther(frames []*image.RGBA) []*image.RGBA {
	var out []*image.RGBA
	for i := 0; i < len(frames); i += 2 {
		out = append(out, frames[i])
	}
	return out
}

// A list that cannot be read says so in words, with 重试; what went wrong
// goes to the log only.
func TestExploreUnreadable(t *testing.T) {
	a, _ := newTestApp(t)
	missing := filepath.Join(t.TempDir(), "no-explore")
	a.opt.Explore = explore.New(explore.Options{Resources: missing, Cache: filepath.Join(t.TempDir(), "explore-cache.json"),
		FetchFeed: func(context.Context, string) (library.ParsedFeed, error) { return library.ParsedFeed{}, errOffline },
		Fetch:     offline()})
	tt := exploreTester(a)
	toExplore(t, a, tt)
	if !tt.HasText("暂时读不到推荐") || !tt.HasText(locales.ExploreUnread()) {
		t.Error("the page does not say the list could not be read")
	}
	if tt.HasText(missing) || tt.HasText("no such file") {
		t.Error("the page shows the error itself")
	}
	if _, ok := tt.Find("重试"); !ok {
		t.Error("no 重试")
	}
}

// The window closed, the app shows in the next one as it was: Explore, its list read once, the rooms crossable.
func TestExploreNextWindow(t *testing.T) {
	a, _ := newExploreApp(t, nil)
	tt := exploreTester(a)
	toExplore(t, a, tt)
	if _, ok := tt.Find("去写作：作者的最新文章"); !ok {
		t.Fatal("Explore is not open")
	}
	next := exploreTester(a)
	settle(next)
	if _, ok := next.Find("去写作：作者的最新文章"); !ok {
		t.Fatal("the next window does not show Explore")
	}
	must(t, next.Click("去写作：作者的最新文章"))
	settle(next)
	if _, ok := next.Find("回到设计：设计网站和作品"); !ok {
		t.Error("the rooms do not cross in the next window")
	}
}

// Before any writer's posts were read (offline on a first start), the
// writing room's front page is the writers themselves, opening their
// pages; covers and pages say where they write, never that it cannot be
// read.
func TestExploreNothingRead(t *testing.T) {
	a, _ := newExploreApp(t, nil)
	tt := exploreTester(a)
	toExplore(t, a, tt)
	ex := a.ex
	for _, e := range ex.writers {
		e.Posts = nil
	}
	ex.fresh = nil
	must(t, tt.Click("去写作：作者的最新文章"))
	rest(tt)
	for _, s := range []string{"博主的新文章暂时读不到，联网后会自己更新。", "暂时读不到这个订阅源的文章", "没有订阅源"} {
		if tt.HasText(s) {
			t.Errorf("no word of what is missing: %q", s)
		}
	}
	if _, ok := tt.AccessByRole(ui.RoleRegion, "作者"); !ok {
		t.Fatal("the front page is the writers")
	}
	e := ex.index[0]
	r, _ := tt.AccessByRole(ui.RoleRegion, "作者")
	tt.ClickAt(r.Bounds.X+48+20, r.Bounds.Y+32+(r.Bounds.H-64)/8) // the first: its face, in the middle of the first of four rows
	rest(tt)
	if want := a.entryPage(e); a.page != want {
		t.Fatalf("their page opens: %+v, want %+v", a.page, want)
	}
	if tt.HasText("这位作者的新文章，会送到这里") {
		t.Error("their page ends at its head, saying nothing of what is missing")
	}
}
