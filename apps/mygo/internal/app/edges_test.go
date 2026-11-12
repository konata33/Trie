package app

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/fetch"
	"trie/internal/subscription"
)

// heard keeps what the tester announced, as Announcements forgets it.
type heard struct {
	tt   *ui.Tester
	said []string
}

func (h *heard) all() []string {
	h.said = append(h.said, h.tt.Announcements()...)
	return h.said
}

// feedAt answers every feed with one post on the asking URL's own site.
func feedAt() fetch.Fetch {
	return func(ctx context.Context, address string, ok func(string) bool, n int) (*fetch.Response, error) {
		u, _ := url.Parse(address)
		return feedFor("Edge", u.Scheme+"://"+u.Host)(ctx, address, ok, n)
	}
}

// A refresh says it starts and how it ended (main.tsx refreshFeeds).
func TestRefreshAnnouncements(t *testing.T) {
	a, store := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	h := &heard{tt: tt}
	settle(tt)
	if len(a.authors) < 2 {
		t.Fatal("two authors at least")
	}
	h.all()

	a.opt.Subscriptions = subscription.New(store, offline, nil, nil)
	a.refreshFeeds("")
	settle(tt)
	var names []string
	for _, au := range a.authors {
		names = append(names, au.src.DisplayName)
	}
	slices.Sort(names)
	// The names come in the order the feeds failed.
	head := fmt.Sprintf("0 / %d 已更新，", len(a.authors))
	got := h.all()
	i := slices.IndexFunc(got, func(s string) bool { return strings.HasPrefix(s, head) && strings.HasSuffix(s, " 未能更新") })
	if !slices.Contains(got, "正在从订阅源获取更新…") || i < 0 {
		t.Fatalf("failed refresh: %q", got)
	}
	said := strings.Split(strings.TrimSuffix(strings.TrimPrefix(got[i], head), " 未能更新"), "、")
	if slices.Sort(said); !slices.Equal(said, names) {
		t.Fatalf("failed names %q, want %q", said, names)
	}

	h.said = nil
	a.opt.Subscriptions = subscription.New(store, feedAt, nil, nil)
	a.refreshFeeds("")
	settle(tt)
	if got := h.all(); !slices.Contains(got, "全部已更新") {
		t.Fatalf("whole refresh: %q", got)
	}

	h.said = nil
	one := a.authors[0].src
	a.refreshFeeds(one.ID)
	settle(tt)
	if got := h.all(); !slices.Contains(got, one.DisplayName+" 已更新") {
		t.Fatalf("one source: %q", got)
	}
}

// Subscribing says it is reading the site, then what was saved.
func TestSubscribeAnnouncements(t *testing.T) {
	a, store := newTestApp(t)
	a.opt.Subscriptions = subscription.New(store, func() fetch.Fetch { return feedFor("Edge", "https://edge.example.com") }, nil, nil)
	tt := ui.NewTester(a.View, 1280, 850)
	h := &heard{tt: tt}
	settle(tt)
	a.openSubscribe(ui.Rect{})
	settle(tt)
	tt.Type("https://edge.example.com")
	tt.Frame()
	must(t, tt.Click("识别"))
	settle(tt)
	if got := h.all(); !slices.Contains(got, "正在读取网站提供的订阅信息…") {
		t.Fatalf("preview: %q", got)
	}
	must(t, tt.Click("确认订阅"))
	settle(tt)
	if got := h.all(); !slices.Contains(got, "订阅已保存 · 新收录 1 篇 · 更新 0 篇") {
		t.Fatalf("saved: %q", got)
	}
}

// The reader's page, its tools and the loading mark carry their names.
func TestReaderNames(t *testing.T) {
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	a.refresh.busy = true // Home's band turns the loading mark
	tt.Frame()
	if acc, ok := tt.Access("加载中"); !ok || acc.Role != ui.RoleProgress {
		t.Errorf("loading mark: %v %v", ok, acc.Role)
	}
	a.refresh.busy = false
	must(t, tt.Click("Innei"))
	settle(tt)
	must(t, tt.Click(lobeTitle))
	settle(tt)
	if _, ok := tt.AccessByRole(ui.RoleRegion, "文章阅读"); !ok {
		t.Error("no 文章阅读 region")
	}
	if _, ok := tt.AccessByRole(ui.RoleGroup, "阅读工具"); !ok {
		t.Error("no 阅读工具 group")
	}
	if _, ok := tt.AccessByRole(ui.RoleArticle, titleOf(a.article(lobehub).Title)+" · 正文"); !ok {
		t.Error("the body is not named 标题 · 正文")
	}
}

// Explore's preview of a post is 文章预览.
func TestTrialName(t *testing.T) {
	a, tt, _, _ := newHomeApp(t, ui.Preferences{})
	homeAt(t, a, tt, "流行")
	must(t, tt.Click(trendingOf(a.ex.entries)[0].post.Title))
	settle(tt)
	if _, ok := tt.AccessByRole(ui.RoleRegion, "文章预览"); !ok {
		t.Error("no 文章预览 region")
	}
}

// A clip the library could not keep says so, and the next one may go.
func TestClipNotSaved(t *testing.T) {
	a, store := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	settle(tt)
	must(t, tt.Click("Innei"))
	settle(tt)
	must(t, tt.Click(lobeTitle))
	settle(tt)
	tt.Press(440, 290)
	tt.Move(700, 290)
	tt.Release(700, 290)
	tt.Advance(400 * time.Millisecond)
	acc, ok := tt.AccessByRole(ui.RoleToolbar, "选中文字的操作")
	if !ok {
		t.Fatal("no clip bar")
	}
	store.Close() // the next write fails
	tt.ClickAt(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	tt.Frame()
	tt.Frame()
	rp := a.readers[a.page.id]
	if rp.feedback != "摘录未保存" || rp.clipBusy {
		t.Fatalf("feedback %q, busy %v", rp.feedback, rp.clipBusy)
	}
}
