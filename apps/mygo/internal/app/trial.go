package app

import (
	"errors"
	"strings"

	"github.com/egoist/mygo/ui"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/motion"
)

// A post from Explore read in place, before subscribing (main.tsx trial):
// not in the library, so nothing is saved, clipped or kept; the reader's bar
// offers the author's pill where the favourite would be, and the pill signs
// the article.

// trial is an Explore post as the reader shows it: the entry it came from,
// and the article and source it stands in for.
type trial struct {
	entry explore.Entry
	post  explore.Post
	art   library.Article
	src   library.Source
}

// trialID is the reader's ID of an Explore post (main.tsx trialArticle).
func trialID(url string) string { return "explore:" + url }

func isTrial(id string) bool { return strings.HasPrefix(id, "explore:") }

// openPost opens one of an entry's posts in the reader: its title travels to
// the headline while the ground spreads from its button (底色先动); a work,
// opened from its picture, just crosses. From the keys it is simply there.
func (a *App) openPost(c *ui.Context, e *explore.Entry, post explore.Post, from *trialFrom) {
	id := trialID(post.URL)
	t := &trial{entry: *e, post: post}
	t.art = library.Article{ArticleSnapshot: library.ArticleSnapshot{ID: id, URL: post.URL, SourceID: "explore:" + e.ID, Title: post.Title,
		Authors: []string{}, PublishedAt: post.PublishedAt, Aliases: []string{}, BodyStatus: "not_extracted", VerificationLabel: "推荐作者的最新文章"},
		SourceIDs: []string{}}
	t.src = library.Source{SourceSnapshot: library.SourceSnapshot{ID: "explore:" + e.ID, Title: e.Name, Host: e.Host, HomeURL: e.HomeURL, Icon: e.Icon},
		DisplayName: e.Name, FeedURL: e.FeedURL}
	a.trials[id] = t
	at := a.page
	m := a.goTo(c, page{"reader", id}, motion.Deep, []page{at})
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
		a.fly("title:"+id, titleOf(post.Title), at, m.to, from.style, a.headline, 32/from.size, motion.Deep)
	}
	if from.button.W > 0 {
		a.lay, a.layRow = &motion.Lay{Ground: motion.Spread, From: from.button, To: a.paper(), Run: a.run, Still: still}, ""
	}
}

// trialRead reads a preview's body from Explore (readExplorePost): source
// "page" or "feed" afresh from that source, a refresh afresh as the reader
// decides ("auto"), else reusing a recent preview.
func (a *App) trialRead(t *trial, refresh bool, source string) func() (library.ArticleContent, error) {
	svc := a.opt.Explore
	if source == "" && refresh {
		source = "auto"
	}
	entryID, url := t.entry.ID, t.post.URL
	return func() (library.ArticleContent, error) {
		if svc == nil {
			return library.ArticleContent{}, errNoExplore
		}
		return svc.Read(bg, entryID, url, source)
	}
}

var errNoExplore = errors.New("探索暂时不可用")
