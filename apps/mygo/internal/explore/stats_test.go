package explore

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"trie/internal/feed"
	"trie/internal/library"
	"trie/internal/web"
)

// The feedStats and postPictures cases of explore.test.ts. Feeds are parsed by the real parser, as the TS tests parse
// them with parseFeed.

func daysAgo(days int) string {
	return time.Now().Add(-time.Duration(days) * 24 * time.Hour).UTC().Format(http.TimeFormat)
}

// rss is a writer's feed of items, each a slug and a date ("" for none).
func rss(items ...[2]string) string {
	var b strings.Builder
	b.WriteString(`<rss><channel><title>Writer</title><link>https://writer.example/</link>`)
	for _, it := range items {
		b.WriteString(`<item><title>` + it[0] + `</title><link>https://writer.example/` + it[0] + `/</link>`)
		if it[1] != "" {
			b.WriteString(`<pubDate>` + it[1] + `</pubDate>`)
		}
		b.WriteString(`</item>`)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func parsed(t *testing.T, xml, url string) library.ParsedFeed {
	t.Helper()
	f, err := feed.Parse(xml, url)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func intIs(p *int, want int) bool { return p != nil && *p == want }

// feedStats takes the newest posts, undated last, and counts 90 / 30 day windows
func TestFeedStats(t *testing.T) {
	st := feedStats(parsed(t, rss([2]string{"d10", daysAgo(10)}, [2]string{"nodate", ""}, [2]string{"d40", daysAgo(40)}, [2]string{"d1", daysAgo(1)}, [2]string{"d100", daysAgo(100)}), "https://writer.example/feed"), nowMS(), false)
	var titles []string
	for _, p := range st.Posts {
		titles = append(titles, p.Title)
	}
	if !slices.Equal(titles, []string{"d1", "d10", "d40", "d100", "nodate"}) || !intIs(st.Last90, 3) || !intIs(st.Recent30, 2) {
		t.Fatalf("posts %v, last90 %v, recent30 %v", titles, st.Last90, st.Recent30)
	}
	atom := `<feed xmlns="http://www.w3.org/2005/Atom"><title>A</title><link href="https://writer.example/"/><entry><title>u</title><link href="https://writer.example/u/"/><updated>` +
		web.ISOTime(time.Now().Add(-5*24*time.Hour).UnixMilli()) + `</updated></entry></feed>`
	if st := feedStats(parsed(t, atom, "https://writer.example/feed"), nowMS(), false); !intIs(st.Last90, 1) || !intIs(st.Recent30, 1) {
		t.Fatalf("an Atom feed's <updated> is its date: %v %v", st.Last90, st.Recent30)
	}
	bare := feedStats(parsed(t, rss([2]string{"x", ""}), "https://writer.example/feed"), nowMS(), false)
	if !slices.Equal(bare.Posts, []Post{{Title: "x", URL: "https://writer.example/x/"}}) || bare.Last90 != nil || bare.Recent30 != nil ||
		bare.Works == nil || len(bare.Works) != 0 || bare.Pictures == nil || len(bare.Pictures) != 0 || bare.Sites != nil || bare.Picks != nil {
		t.Fatalf("%+v", bare)
	}
	told := `<rss><channel><title>W</title><link>https://writer.example/</link><item><title>t</title><link>https://writer.example/t/</link><description>&lt;p&gt;Hello &amp;amp; &lt;b&gt;welcome&lt;/b&gt;&lt;/p&gt;&lt;script&gt;x()&lt;/script&gt;&lt;p&gt;to the  post&lt;/p&gt;</description></item></channel></rss>`
	opening := func(xml string) string {
		return feedStats(parsed(t, xml, "https://writer.example/feed"), nowMS(), false).Posts[0].Excerpt
	}
	for _, c := range []struct{ xml, want, why string }{
		{told, "Hello & welcome to the post", "the excerpt is the feed text with tags, scripts and spacing removed"},
		{strings.Replace(told, "to the  post", "to the post. The post t appeared first on Writer.", 1), "Hello & welcome to the post.", "a syndication footer is left out"},
		{strings.Replace(told, "Hello", "本文可全文转载，商用请联系授权。 Hello", 1), "Hello & welcome to the post", "a reprint notice is left out"},
		{strings.Replace(told, "Hello", "by someone from https://writer.example/t/?p=1 Source: example.com © Studio . License: All Rights Reserved . **Hello**", 1), "Hello & welcome to the post",
			"bylines, links, reprint notices and Markdown emphasis are left out"},
	} {
		if got := opening(c.xml); got != c.want {
			t.Errorf("%s: %q", c.why, got)
		}
	}
}

// a post's own pictures are its candidates; drawings, advertisements, avatars and small pictures are left out
func TestPostPictures(t *testing.T) {
	body := `<p><img src="/emoji.png" class="emoji"><img src="https://cdn.example/ads/sponsor.png"><img src="/diagram.svg"><img src="/thumb.jpg" width="120">
    <img src="/photo.jpg" width="1200"><img src="https://cdn.example/second.webp"><img src="https://secure.gravatar.com/avatar/1"><img src="/third.png"><img src="/fourth.png"></p>`
	base := "https://writer.example/post/"
	entry := library.FeedEntry{URL: base, Image: library.Some("https://cdn.example/attached.jpg"), FeedBody: &library.FeedBody{HTML: body, BaseURL: &base}}
	if got := postPictures(entry); !slices.Equal(got, []string{"https://cdn.example/attached.jpg", "https://writer.example/photo.jpg", "https://cdn.example/second.webp"}) {
		t.Fatal(got)
	}
}

// Beyond the TS cases: the parts of excerpt RE2 cannot say as JavaScript does (the credit's lookahead, a notice near
// the start counted in UTF-16 units, the cut at 1200 units), and an entity no code point answers.
func TestExcerptEdges(t *testing.T) {
	e := func(html string) string {
		return excerpt(library.FeedEntry{Title: "T", FeedBody: &library.FeedBody{HTML: html}})
	}
	for _, c := range []struct{ html, want string }{
		{"Photo © Studio. Then the text.", "Photo Then the text."},
		{"© Studio.no space. Then", "Then"},
		{"© " + strings.Repeat("x", 81) + ". Rest", "Rest"}, // too long for a credit; the notice takes it
		{"Source: a。b. Rest", "Source: a。b. Rest"},
		{"License: one。 two", "two"},
		{"😀 Source: x.", "😀"},
		{"&#9731; &#99999999; &amp;lt;", "☃ � &lt;"}, // TS throws on the second
		{"<SCRIPT>x()</script>Kept <pre>code</PRE>text", "Kept text"},
		{"T", ""},
	} {
		if got := e(c.html); got != c.want {
			t.Errorf("%q: %q, want %q", c.html, got, c.want)
		}
	}
	long := strings.Repeat("字", 1199) + "😀" + "tail"
	if got := e(long); got != strings.Repeat("字", 1199)+"�…" {
		t.Errorf("cut at 1200 units: …%q", got[len(got)-12:])
	}
	if got := e(strings.Repeat("a", 150) + " 本文转载自某处。 Rest"); got != "Rest" {
		t.Errorf("a notice within 160 units: %q", got)
	}
	if got := e(strings.Repeat("a", 159) + " 本文转载自某处。 Rest"); got != strings.Repeat("a", 159)+" 本文转载自某处。 Rest" {
		t.Errorf("a notice past 160 units is kept: %q", got)
	}
}
