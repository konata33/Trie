package app

import (
	"errors"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/subscription"
	"trie/internal/web"
)

// A post from Explore, opened, is kept in the library (Keep) under its
// author's source, added unsubscribed when the library has none: it reads,
// keeps, clips and remembers its place as any article does. Until the
// library has it, which takes a moment, its trial stands for it. Its body is
// read through Explore (readExplorePost), which knows its feed's copy, and
// is kept with it.

// trial is an Explore post as the reader shows it until the library has it:
// the entry it came from, the article it stands for, under the library's ID
// it is kept by, and the entry as a source; kept closes once the library has it.
type trial struct {
	entry explore.Entry
	post  explore.Post
	art   library.Article
	src   library.Source
	kept  chan struct{}
}

// postID is the library's ID of the post at url: the article it has at that
// address, else the address, which a post is kept by.
func (a *App) postID(url string) string {
	if k, err := web.URLKey(url); err == nil {
		if id := a.byURL[k]; id != "" {
			return id
		}
	}
	return url
}

// libArticle is article id as the library has it; nil until it does.
func (a *App) libArticle(id string) *library.Article {
	for i := range a.lib.Articles {
		if a.lib.Articles[i].ID == id {
			return &a.lib.Articles[i]
		}
	}
	return nil
}

// newTrial is post of e as the reader shows it, under e's own source.
func (a *App) newTrial(e *explore.Entry, post explore.Post) *trial {
	t := &trial{entry: *e, post: post, src: exploreSource(e), kept: make(chan struct{})}
	t.art = library.Article{ArticleSnapshot: library.ArticleSnapshot{ID: a.postID(post.URL), URL: post.URL, SourceID: t.src.ID, Title: post.Title,
		Authors: []string{}, PublishedAt: post.PublishedAt, Aliases: []string{}, BodyStatus: "not_extracted", VerificationLabel: "推荐作者的最新文章"},
		SourceIDs: []string{}}
	return t
}

// exploreSource is e as a source: "explore:<entry ID>", the ID of its page too.
func exploreSource(e *explore.Entry) library.Source {
	return library.Source{SourceSnapshot: library.SourceSnapshot{ID: "explore:" + e.ID, Title: e.Name, Host: e.Host, HomeURL: e.HomeURL, Icon: e.Icon},
		DisplayName: e.Name, FeedURL: e.FeedURL}
}

// keep puts post of e in the library, once, unless it has it already; its
// trial stands for it in the meantime and reads its body through Explore.
func (a *App) keep(e *explore.Entry, post explore.Post) {
	id := a.postID(post.URL)
	if a.trials[id] != nil || a.libArticle(id) != nil {
		return
	}
	t := a.newTrial(e, post)
	a.trials[id] = t
	feed, src := "", subscription.PersonID(e.ID)
	if e.FeedURL != nil && *e.FeedURL != "" {
		feed = *e.FeedURL
		var err error
		if src, _, err = subscription.SourceID(a.lib.Sources, feed, e.HomeURL); err != nil {
			src = ""
		}
	}
	store := a.opt.Store
	if src == "" || store == nil {
		close(t.kept)
		return
	}
	art := t.art.ArticleSnapshot
	art.SourceID, art.Kinds = src, post.Kinds
	action := library.Keep{Source: library.SourceSnapshot{ID: src, Title: e.Name, Host: e.Host, HomeURL: e.HomeURL, Icon: e.Icon},
		Name: e.Name, FeedURL: feed, Article: art}
	a.do(func() func() {
		lib, err := store.Mutate(action)
		close(t.kept)
		if err != nil {
			return nil // the trial goes on standing for it: it reads, and keeps nothing
		}
		return func() { a.accept(lib) }
	})
}

// openPost opens one of an entry's posts in the reader, kept in the library
// as it opens: its title travels to the headline while the ground spreads
// from its button (底色先动); a work, opened from its picture, just
// crosses. From the keys it is simply there.
func (a *App) openPost(c *ui.Context, e *explore.Entry, post explore.Post, from *trialFrom) {
	a.keep(e, post)
	id := a.postID(post.URL)
	at := a.page
	m := a.goTo(c, page{"reader", id}, motion.Deep, append(a.from[:len(a.from):len(a.from)], at))
	if m == nil {
		return
	}
	// A read before this one may have failed or come back partial: this one asks again.
	if rp := a.readers[id]; rp != nil && rp.err != "" {
		rp.err = ""
	}
	a.reading(id)
	if m.kbd || from == nil {
		return
	}
	still := a.still(c)
	if !still && from.title.W > 0 {
		// From where its title shows: built before its click came, it was not there yet to give its box (end).
		a.fly("title:"+id, titleOf(post.Title), at, m.to, from.style, a.headline, 32/from.size, motion.Deep).w.From = from.title
	}
	if from.button.W > 0 {
		a.lay, a.layRow = &motion.Lay{Ground: motion.Spread, From: from.button, To: a.paper(), Run: a.run, Still: still}, ""
	}
}

// trialRead reads a post's body through Explore (readExplorePost): source
// "page" or "feed" afresh from that source, a refresh afresh as the reader
// decides ("auto"), else reusing a recent preview. A body read whole is kept
// with the article once the library has it.
func (a *App) trialRead(t *trial, refresh bool, source string) func() (library.ArticleContent, error) {
	svc, store := a.opt.Explore, a.opt.Store
	if source == "" && refresh {
		source = "auto"
	}
	entryID, url, id := t.entry.ID, t.post.URL, t.art.ID
	return func() (library.ArticleContent, error) {
		if svc == nil {
			return library.ArticleContent{}, errors.New(locales.ExploreUnavailable())
		}
		content, err := svc.Read(bg, entryID, url, source)
		if err != nil {
			return content, err
		}
		content.ArticleID = id
		if content.Status == "ready" && store != nil {
			select {
			case <-t.kept:
				store.SaveContent(content) // ponytail: a body not kept is read again through the library next time
			case <-time.After(10 * time.Second):
			}
		}
		return content, nil
	}
}
