package reader

import (
	"embed"
	"net/url"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
)

// The marks of the sites a link of the body may go to, from Simple Icons (CC0), each one path on a 24 grid filled
// with the text's colour.
//
//go:embed glyphs
var glyphFiles embed.FS

// glyphHosts are the sites whose links show their mark before their words: a host, or the domain it ends in.
var glyphHosts = map[string]string{
	"github.com": "github", "x.com": "x", "twitter.com": "x", "youtube.com": "youtube", "youtu.be": "youtube",
	"wikipedia.org": "wikipedia", "npmjs.com": "npm", "npmx.dev": "npm", "developer.mozilla.org": "mdnwebdocs",
	"stackoverflow.com": "stackoverflow", "news.ycombinator.com": "ycombinator", "reddit.com": "reddit",
	"bsky.app": "bluesky", "mastodon.social": "mastodon", "t.me": "telegram", "discord.com": "discord",
	"discord.gg": "discord", "figma.com": "figma", "dribbble.com": "dribbble", "bilibili.com": "bilibili",
	"b23.tv": "bilibili", "zhihu.com": "zhihu", "xiaohongshu.com": "xiaohongshu", "opencollective.com": "opencollective",
}

// glyphOf is the mark of the site href goes to, empty for a site without one.
func glyphOf(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	for host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www."); host != ""; {
		if g, ok := glyphHosts[host]; ok {
			return g
		}
		_, rest, _ := strings.Cut(host, ".")
		if !strings.Contains(rest, ".") {
			return ""
		}
		host = rest
	}
	return ""
}

var glyphSVGs sync.Map // name → *ui.SVG

func glyphSVG(name string) *ui.SVG {
	if s, ok := glyphSVGs.Load(name); ok {
		return s.(*ui.SVG)
	}
	data, err := glyphFiles.ReadFile("glyphs/" + name + ".svg")
	if err != nil {
		return nil
	}
	s := ui.MustParseSVG(data)
	glyphSVGs.Store(name, s)
	return s
}

// A mark stands in its link's text as an inline object (glyphSpan): a little room before it, the mark 0.8em square,
// standing a little below the baseline as capitals' middles sit, then room before the words.
const (
	glyphLead, glyphSize, glyphGap, glyphDescent = 0.04, 0.8, 0.26, 0.1
	// glyphLang marks an object span as a mark, its name after it: the span's language, which an object does not
	// use, goes with it wherever its text is cut (the flow's pieces).
	glyphLang = "x-glyph-"
)

func glyphSpan(name string, size float32) ui.Span {
	return ui.Span{Text: "￼", ObjectWidth: (glyphLead + glyphSize + glyphGap) * size, ObjectHeight: (glyphSize - glyphDescent) * size,
		ObjectDescent: glyphDescent * size, Lang: glyphLang + name}
}

// drawGlyph draws the mark whose object is r in c.
func drawGlyph(p *ui.Painter, name string, r ui.Rect, c ui.Color) {
	s := glyphSVG(name)
	if s == nil {
		return
	}
	size := r.H
	lead := r.W * glyphLead / (glyphLead + glyphSize + glyphGap)
	p.Icon(s, ui.Rect{X: r.X + lead, Y: r.Y, W: size, H: size}, c)
}

// glyphsOf gives the marks of spans by the rune they stand at in their text, for DrawObjects.
func glyphsOf(sp []ui.Span) map[int]string {
	var out map[int]string
	at := 0
	for _, s := range sp {
		if name, ok := strings.CutPrefix(s.Lang, glyphLang); ok && s.ObjectWidth > 0 {
			if out == nil {
				out = map[int]string{}
			}
			out[at] = name
		}
		at += len([]rune(s.Text))
	}
	return out
}

// drawGlyphs is a DrawObjects that draws the marks of sp in c.
func drawGlyphs(sp []ui.Span, c ui.Color) func(p *ui.Painter, at int, r ui.Rect) {
	marks := glyphsOf(sp)
	return func(p *ui.Painter, at int, r ui.Rect) {
		if name, ok := marks[at]; ok {
			drawGlyph(p, name, r, c)
		}
	}
}

// glyphNames are the marks of sp's objects, in order.
func glyphNames(sp []ui.Span) []string {
	var out []string
	for _, s := range sp {
		if name, ok := strings.CutPrefix(s.Lang, glyphLang); ok && s.ObjectWidth > 0 {
			out = append(out, name)
		}
	}
	return out
}
