package app

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/subscription"
	"trie/internal/theme"
	"trie/internal/web"
)

// aheadWait is how long a refresh holds the articles it brought while their
// pictures come (picturesAhead).
const aheadWait = 60 * time.Second

// refreshState is the subscriptions' refresh as the sidebar shows it: rows
// dim while their feed is read and brighten
// one by one as each settles; a failed feed keeps its retry.
type refreshState struct {
	busy    bool
	pending map[string]bool
	failed  map[string]string
	asked   map[string]bool // retries pressed: their mark turns
	retries []string        // retries waiting for the refresh under way
	arrived map[string]time.Time

	// The refresh icon's turn.
	spinAt, stopAt time.Time
	from, rest     float64
}

func (r *refreshState) ask(id string) {
	if r.asked == nil {
		r.asked = map[string]bool{}
	}
	r.asked[id] = true
}

// failure is why an author's feed has not been read for over a day
// (staleAfter), if so. A refresh failing now and then, the network gone for
// a while, marks nothing: only a feed that keeps failing does (as Folo
// flags a feed after a day of errors).
func (a *App) failure(au *author) string {
	id := au.src.ID
	if a.refresh.pending[id] || !stale(au.src, time.Now()) {
		return ""
	}
	if f, ok := a.refresh.failed[id]; ok {
		if f == "" {
			return locales.RefreshNoResponse()
		}
		return storedText(f)
	}
	if !a.refresh.busy && au.src.LastError != nil && *au.src.LastError != "" {
		return storedText(*au.src.LastError)
	}
	return ""
}

// staleAfter is how long a feed fails before its author is marked.
const staleAfter = 24 * time.Hour

// stale reports whether src's feed was last read well over staleAfter
// before now, or never.
func stale(src *library.Source, now time.Time) bool {
	if src.LastFetchedAt == nil {
		return true
	}
	at, ok := web.JSDateParse(*src.LastFetchedAt)
	return !ok || now.UnixMilli()-at > staleAfter.Milliseconds()
}

// failing counts the authors whose feeds have not been read for over a day.
func (a *App) failing() int {
	n := 0
	for _, au := range a.authors {
		if a.failure(au) != "" {
			n++
		}
	}
	return n
}

// refreshFeeds reads every subscribed feed again, or only sourceID's; a
// retry pressed while a refresh runs waits its turn, turning meanwhile.
func (a *App) refreshFeeds(sourceID string) {
	r := &a.refresh
	svc := a.opt.Subscriptions
	if svc == nil {
		return
	}
	if r.pending == nil {
		r.pending, r.failed = map[string]bool{}, map[string]string{}
	}
	if r.busy {
		if sourceID == "" || slices.Contains(r.retries, sourceID) {
			return
		}
		r.retries = append(r.retries, sourceID)
		r.pending[sourceID] = true
		delete(r.failed, sourceID)
		return
	}
	var ids []string
	for _, au := range a.authors {
		if sourceID == "" || au.src.ID == sourceID {
			ids = append(ids, au.src.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	before := map[string]bool{}
	for i := range a.lib.Articles {
		before[a.lib.Articles[i].ID] = true
	}
	r.busy = true
	a.said = append(a.said, locales.RefreshChecking())
	one := locales.RefreshFeedUpdated()
	if src := a.source(sourceID); src != nil && src.DisplayName != "" {
		one = locales.RefreshWriterUpdated(src.DisplayName)
	}
	if sourceID == "" {
		clear(a.fresh)
	}
	for _, id := range append(ids, r.retries...) {
		r.pending[id] = true
		delete(r.failed, id)
	}
	settled := map[string]bool{}
	post := a.post
	a.do(func() func() {
		res, err := svc.Refresh(bg, sourceID, func(p subscription.Progress) {
			if p.State == "start" {
				return
			}
			post(func() {
				if settled[p.SourceID] || !slices.Contains(ids, p.SourceID) {
					return
				}
				settled[p.SourceID] = true
				delete(r.pending, p.SourceID)
				if p.State == "failed" {
					r.failed[p.SourceID] = p.Message
				}
				if p.State == "done" {
					a.arrive(p.SourceID)
					if p.ImportedCount != nil && *p.ImportedCount > 0 {
						a.fresh[p.SourceID] = true
					}
				}
			})
		})
		if err == nil {
			a.picturesAhead(res.Library.Articles, before)
		}
		return func() {
			r.busy = false
			if err != nil {
				a.fail(err.Error())
			} else {
				a.accept(res.Library)
				for _, art := range res.Library.Articles {
					if !before[art.ID] {
						for _, id := range memberships(&art) {
							if slices.Contains(ids, id) {
								a.fresh[id] = true
							}
						}
					}
				}
				var names []string
				for _, f := range res.Failures {
					r.failed[f.SourceID] = f.Message
					name := locales.RefreshUnnamedFeed()
					if i := slices.IndexFunc(res.Library.Sources, func(s library.Source) bool { return s.ID == f.SourceID }); i >= 0 && res.Library.Sources[i].DisplayName != "" {
						name = res.Library.Sources[i].DisplayName
					}
					names = append(names, name)
				}
				switch {
				case len(names) > 0:
					a.said = append(a.said, locales.RefreshSomeFailed(len(ids)-len(names), len(ids), strings.Join(names, locales.CommonListSeparator())))
				case sourceID != "":
					a.said = append(a.said, one)
				default:
					a.said = append(a.said, locales.RefreshAllUpdated())
				}
				for _, id := range ids {
					if _, failed := r.failed[id]; !settled[id] && !failed {
						a.arrive(id)
					}
				}
			}
			clear(r.pending)
			for _, id := range r.retries {
				r.pending[id] = true
			}
			if len(r.retries) > 0 {
				next := r.retries[0]
				r.retries = r.retries[1:]
				a.refreshFeeds(next)
			}
		}
	})
}

// picturesAhead fetches the pictures of the articles a refresh brought, the
// ones not in before, four at a time, before the refresh shows them: a new
// article comes onto the pages with its picture, and the refresh's turn ends
// once they are in. It waits at most aheadWait; a picture still on its way
// then comes as pictures do.
func (a *App) picturesAhead(arts []library.Article, before map[string]bool) {
	if a.opt.Thumbs != nil || a.opt.Store == nil {
		return // the tests' thumbnails are bitmaps, without the picture to fetch
	}
	next := make(chan string, len(arts))
	for i := range arts {
		if pv := arts[i].Preview; !before[arts[i].ID] && pv != nil && pv.HasImage {
			next <- arts[i].ID
		}
	}
	close(next)
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for range min(4, len(next)) {
			wg.Go(func() {
				for id := range next {
					a.thumbImage(id, 0, 1200, true)
				}
			})
		}
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(aheadWait):
	}
}

// arrive flares a refreshed author's face once: light, not movement, so it
// stays under less motion (brightness 1.7 over 480 ms).
func (a *App) arrive(id string) {
	if a.refresh.arrived == nil {
		a.refresh.arrived = map[string]time.Time{}
	}
	a.refresh.arrived[id] = time.Time{} // timed from the next frame
}

// flare draws the flare over a chip, if one is under way.
func (a *App) flare(c *ui.Context, id string) {
	at, ok := a.refresh.arrived[id]
	if !ok {
		return
	}
	now := c.Now()
	if at.IsZero() {
		at = now
		a.refresh.arrived[id] = at
	}
	k := float32(now.Sub(at)) / float32(motion.Flare)
	if k >= 1 {
		delete(a.refresh.arrived, id)
		return
	}
	ease := 1 - (1-k)*(1-k)
	ui.Box(c).Absolute().Fill().Radius(9).Background(theme.Text).Opacity(0.45 * (1 - ease)).IgnoreInput(true)
	c.AnimationFrame()
}

func plural(n int, unit string) string { return fmt.Sprintf("%d %s", n, unit) }
