package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/motion"
	"trie/internal/subscription"
	"trie/internal/theme"
)

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

	exploreRun int
	exploreWas bool
}

func (r *refreshState) ask(id string) {
	if r.asked == nil {
		r.asked = map[string]bool{}
	}
	r.asked[id] = true
}

// failure is why an author's feed could not be read, if it could not.
func (a *App) failure(au *author) string {
	id := au.src.ID
	if a.refresh.pending[id] {
		return ""
	}
	if f, ok := a.refresh.failed[id]; ok {
		if f == "" {
			return "订阅源无响应"
		}
		return f
	}
	if !a.refresh.busy && au.src.LastError != nil && *au.src.LastError != "" {
		return *au.src.LastError
	}
	return ""
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
	a.said = append(a.said, "正在从订阅源获取更新…")
	one := "订阅"
	if src := a.source(sourceID); src != nil && src.DisplayName != "" {
		one = src.DisplayName
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
					name := "订阅源"
					if i := slices.IndexFunc(res.Library.Sources, func(s library.Source) bool { return s.ID == f.SourceID }); i >= 0 && res.Library.Sources[i].DisplayName != "" {
						name = res.Library.Sources[i].DisplayName
					}
					names = append(names, name)
				}
				switch {
				case len(names) > 0:
					a.said = append(a.said, fmt.Sprintf("%d / %d 已更新，%s 未能更新", len(ids)-len(names), len(ids), strings.Join(names, "、")))
				case sourceID != "":
					a.said = append(a.said, one+" 已更新")
				default:
					a.said = append(a.said, "全部已更新")
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
