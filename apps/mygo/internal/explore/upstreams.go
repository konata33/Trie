package explore

import (
	"regexp"
	"strings"

	"trie/internal/library"
	"trie/internal/web"
)

// Pick is explore-upstreams.ts Pick: a post an upstream recommends, pointing at the writer's own site.
type Pick struct {
	URL, Title, Author, HomeURL string
	PublishedAt                 *string
	Entry                       library.FeedEntry
}

// Upstream is a feed that recommends posts by writers anywhere; Picks turns its items into posts on the writers' own
// sites, whose writers join Explore beside the catalog's.
type Upstream struct {
	Name, FeedURL string
	Picks         func(feed library.ParsedFeed) []Pick
}

// Upstreams are UPSTREAMS, the app's upstreams.
var Upstreams = []Upstream{
	{Name: "Hacker News", FeedURL: "https://hackernews.betacat.io/feed.xml", Picks: shared},
	{Name: "UX Collective", FeedURL: "https://uxdesign.cc/feed", Picks: publication},
	{Name: "Latent Space", FeedURL: "https://www.latent.space/feed", Picks: latentSpace},
	{Name: "Lobsters", FeedURL: "https://lobste.rs/t/ai,design,css,web,programming.rss", Picks: shared},
}

// origin is new URL(value).origin; ok is false where that throws.
func origin(value string) (string, bool) {
	u, err := web.ParseURL(value, nil)
	if err != nil {
		return "", false
	}
	switch u.Scheme {
	case "http", "https", "ws", "wss", "ftp":
		bare := *u
		bare.Username, bare.Password = "", ""
		root, err := web.ParseURL("/", &bare)
		if err != nil {
			return "", false
		}
		return strings.TrimSuffix(root.Href(), "/"), true
	}
	return "null", true
}

// pick makes a Pick of entry; ok is false for an address with no origin, where TS throws and the upstream's read fails.
func pick(entry library.FeedEntry, url, title, author string) (Pick, bool) {
	home, ok := origin(url)
	if !ok {
		return Pick{}, false
	}
	// Some upstreams date posts by day in a zone ahead of ours; a post is never newer than now.
	at := dated(entry)
	if t := parse(at); t > float64(nowMS()) {
		iso := web.ISOTime(nowMS())
		at = &iso
	}
	entry.URL, entry.Title = url, title
	return Pick{URL: url, Title: title, Author: author, HomeURL: home + "/", PublishedAt: at, Entry: entry}, true
}

// digestLinks are the links the Hacker News digest closes each summary with, to itself and the thread: "[summary] [comments]".
var digestLinks = regexp.MustCompile(`<` + ci("a") + `\b[^>]*>` + space + `*\[(?:` + ci("summary") + `|` + ci("comments") + `)\]` + space + `*</` + ci("a") + `>`)

// shared is a link aggregator's picks (Hacker News, Lobsters): each item links straight to the post, and the name on it
// is whoever shared it, so the site stands for the writer. A body that is only a link to the discussion ("Comments")
// says nothing about the post, so the reader goes to the page instead.
func shared(feed library.ParsedFeed) []Pick {
	var out []Pick
	for _, a := range feed.Articles {
		if !strings.HasPrefix(a.URL, "http:") && !strings.HasPrefix(a.URL, "https:") {
			continue
		}
		u, err := web.ParseURL(a.URL, nil)
		if err != nil {
			continue
		}
		site := strings.TrimPrefix(u.Hostname(), "www.")
		if site == "lobste.rs" {
			continue
		}
		html := digestLinks.ReplaceAllString(bodyOf(a), "")
		entry := a
		if web.JSLen(web.JSTrim(spaces.ReplaceAllString(anyTag.ReplaceAllString(html, " "), " "))) > 40 {
			body := *a.FeedBody
			body.HTML = html
			entry.FeedBody = &body
		} else {
			entry.FeedBody = nil
		}
		if p, ok := pick(entry, a.URL, a.Title, site); ok {
			out = append(out, p)
		}
	}
	return out
}

// publication is a publication of many writers' picks (UX Collective on Medium): the writer is the item's author;
// Medium tags its links with a tracking query.
func publication(feed library.ParsedFeed) []Pick {
	var out []Pick
	for _, a := range feed.Articles {
		url, _, _ := strings.Cut(a.URL, "?")
		author := feed.Title
		if len(a.Authors) > 0 && a.Authors[0] != "" {
			author = a.Authors[0]
		}
		if p, ok := pick(a, url, a.Title, author); ok {
			out = append(out, p)
		}
	}
	return out
}

// latentSpace is Latent Space's picks: its own essays and interviews; the daily [AINews] roundups are news, not reading.
func latentSpace(feed library.ParsedFeed) []Pick {
	var out []Pick
	for _, a := range feed.Articles {
		if strings.HasPrefix(a.Title, "[AINews]") {
			continue
		}
		if p, ok := pick(a, a.URL, a.Title, feed.Title); ok {
			out = append(out, p)
		}
	}
	return out
}
