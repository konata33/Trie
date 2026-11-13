package app

import (
	"image"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/motion"
	"trie/internal/subscription"
	"trie/internal/theme"
)

// A person, wherever they show (the card of their site, a byline, the
// reader's byline), goes to their author page, subscribed or not. Someone
// not subscribed to has the page of what Explore has of them, its ID their
// source's under Explore, "explore:<entry ID>"; its head offers the subscribe
// pill where the menu would be.

// entryPage is the author page of entry e: the subscribed source's, else
// Explore's own.
func (a *App) entryPage(e *explore.Entry) page {
	if id := a.subscribedSource(e); id != "" {
		return page{"author", id}
	}
	return page{"author", "explore:" + e.ID}
}

// openPerson goes deep into p, an author's page, remembering where it came
// from; from an article opened from p, it goes back to p.
func (a *App) openPerson(c *ui.Context, p page) {
	if a.page == p {
		return
	}
	if n := len(a.from); a.page.kind == "reader" && n > 0 && a.from[n-1] == p {
		a.back(c)
		return
	}
	a.goTo(c, p, motion.Deep, append(a.from[:len(a.from):len(a.from)], a.page))
}

// openFromExplore goes to e's page from Explore; what opened it (a card, a
// byline) takes the focus back when the keys come back.
func (a *App) openFromExplore(c *ui.Context, e *explore.Entry, opener string) {
	a.ex.opener = opener
	a.openPerson(c, a.entryPage(e))
}

// bylinePage is the page of src, the author an article shows under, when
// there is one: a subscribed author's, or Explore's.
func (a *App) bylinePage(src *library.Source) (page, bool) {
	if src == nil {
		return page{}, false
	}
	if id, ok := strings.CutPrefix(src.ID, "explore:"); ok {
		if e := a.ex.find(id); e != nil {
			return a.entryPage(e), true
		}
		return page{}, false
	}
	if a.writer(src.ID) != nil {
		return page{"author", src.ID}, true
	}
	// Kept unsubscribed, they are the one Explore has at their feed, or by who they are.
	for i := range a.ex.entries {
		e := &a.ex.entries[i]
		if src.FeedURL != nil && *src.FeedURL != "" && e.FeedURL != nil && *e.FeedURL == *src.FeedURL || src.ID == subscription.PersonID(e.ID) {
			return a.entryPage(e), true
		}
	}
	return page{}, false
}

// exPerson is someone not subscribed to as their page shows them: Explore's
// entry, and their posts as articles, newest first: the library's for those
// it keeps, else their trials; a gallery's works keep the index Explore
// gives their pictures by.
type exPerson struct {
	entry *explore.Entry
	sig   string
	au    *author
	works map[string]int
	posts map[string]*trial // by the library ID a post is kept by
}

// exPersonOf is the person behind page ID id, when it is Explore's; built
// again once Explore's copy of them, or the library, changes.
func (a *App) exPersonOf(id string) *exPerson {
	eid, ok := strings.CutPrefix(id, "explore:")
	if !ok {
		return nil
	}
	e := a.ex.find(eid)
	if e == nil {
		return nil
	}
	sig := strconv.Itoa(len(e.Posts)) + "/" + strconv.Itoa(len(e.Works)) + "/" + strconv.Itoa(a.lib.Revision)
	if len(e.Posts) > 0 {
		sig += e.Posts[0].URL
	}
	if len(e.Works) > 0 {
		sig += e.Works[0].URL
	}
	if a.ex.people == nil {
		a.ex.people = map[string]*exPerson{}
	}
	if p := a.ex.people[eid]; p != nil && p.entry == e && p.sig == sig {
		return p
	}
	src := exploreSource(e)
	p := &exPerson{entry: e, sig: sig, au: &author{src: &src, days: -1}, works: map[string]int{}, posts: map[string]*trial{}}
	work := map[string]int{}
	for i, w := range e.Works {
		work[w.URL] = i
	}
	posts := e.Posts
	if len(posts) == 0 {
		posts = e.Works
	}
	for _, post := range posts {
		t := a.newTrial(e, post)
		art := t.art
		if kept := a.libArticle(art.ID); kept != nil {
			art = *kept
		}
		// What the feed opens with, until the library has read the article itself.
		if art.Preview == nil || art.Preview.Excerpt == nil {
			pv := library.ArticlePreview{}
			if art.Preview != nil {
				pv = *art.Preview
			}
			if post.Excerpt != "" {
				pv.Excerpt = &post.Excerpt
			}
			art.Preview = &pv
		}
		if i, ok := work[post.URL]; ok {
			p.works[art.ID] = i
		}
		p.posts[art.ID] = t
		p.au.arts = append(p.au.arts, &art)
	}
	a.ex.people[eid] = p
	return p
}

// listedPost is the post an unsubscribed person's page lists as article id.
func (a *App) listedPost(id string) *trial {
	for _, p := range a.ex.people {
		if t := p.posts[id]; t != nil {
			return t
		}
	}
	return nil
}

// entryPic is the picture of art on page p: a gallery's work as Explore has
// it, else the article's own; nil when it has none.
func (a *App) entryPic(p page, art *library.Article) (pic *favPic, wants bool) {
	if who := a.exPersonOf(p.id); who != nil {
		if i, ok := who.works[art.ID]; ok && a.opt.Explore != nil {
			svc, eid := a.opt.Explore, who.entry.ID
			return a.favPicture("work:"+eid+"#"+strconv.Itoa(i), func() image.Image {
				data, _ := svc.Work(bg, eid, i)
				return dataImage(data)
			}), true
		}
	}
	if art.Preview == nil || !art.Preview.HasImage {
		return nil, false
	}
	return a.keepPic(art.ID), true
}

// sourceByline is the author an article shows under, at its head: their chip and
// name, which go to their page when they have one, the name lighting up and
// underlined under the pointer.
func (a *App) sourceByline(c *ui.Context, src *library.Source) {
	p, ok := a.bylinePage(src)
	if !ok {
		a.sourceChip(c, src, 18, 14)
		ui.Text(c, nameOf(src)).TextColor(theme.Text2)
		return
	}
	b := ui.ButtonBase(c.Key("byline")).Label(nameOf(src)).Row().Gap(8).AlignItems(ui.Center).Margin(0, -4).Padding(0, 4).Radius(6).FocusRing(false)
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	if b.Clicked() {
		a.openPerson(c, p)
	}
	color := theme.Text2
	if b.Hovered() {
		color = theme.Text
	}
	a.control(b)
	b.Children(func() {
		a.sourceChip(c, src, 18, 14)
		name := ui.Text(c, nameOf(src)).TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover)))
		a.underline(name, b, b.Hovered(), theme.UnderlineHover, 3)
	})
}
