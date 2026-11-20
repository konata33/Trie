package reader

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"trie/internal/theme"

	"github.com/egoist/mygo/ui"
)

// A body's highest headings are its sections, set at 24 over 17 text, whatever level its author began them at; the
// levels under them follow at 20, 17, then 15.
func TestHeadingsSettle(t *testing.T) {
	body := Prepare(&Content{Status: "ready", HTML: `<h3>A</h3><p>a</p><h4>B</h4><p>b</p><h5>C</h5><p>c</p><h6>D</h6>`})
	var levels []int
	walkBlocks(body.blocks, func(b *block) {
		if b.kind == Heading {
			levels = append(levels, b.level)
		}
	})
	if !slices.Equal(levels, []int{2, 3, 4, 5}) {
		t.Errorf("h3 to h6 settle at %v, want 2 to 5", levels)
	}
	r := NewReader(&Article{ID: "h", Title: "h"}, &Content{Status: "ready"}, body)
	r.Size = 17
	for level, want := range map[int]float32{2: 24, 3: 20, 4: 17, 5: 15, 6: 15} {
		if got := r.HeadingFont(level).Size; got != want {
			t.Errorf("level %d at %v, want %v", level, got, want)
		}
	}
}

// A table's columns of figures are set to the right; a column with words in it, or only its head, is not.
func TestNumericColumns(t *testing.T) {
	body := Prepare(&Content{Status: "ready", HTML: `<table>
		<tr><th>Name</th><th>Size</th><th>Time</th><th>Note</th></tr>
		<tr><td>a</td><td>1,024 KB</td><td>56ms</td><td>12</td></tr>
		<tr><td>b</td><td>−3.5</td><td>—</td><td>fast</td></tr>
	</table>`})
	var got []bool
	walkBlocks(body.blocks, func(b *block) {
		if b.kind == Table {
			got = b.numeric
		}
	})
	if want := []bool{false, true, true, false}; !slices.Equal(got, want) {
		t.Errorf("figure columns %v, want %v", got, want)
	}
}

// Magic links a feed left as written become links: a user's to their GitHub page, under their name or handle; in
// code they stay as they are.
func TestMagicLinks(t *testing.T) {
	body := Prepare(&Content{Status: "ready", HTML: `<p>Thanks {@serkodev|SerKo}, {@wooorm} and {Vite|https://opencollective.com/vite}.</p><p><code>{@keep}</code></p>`})
	var got []string
	walkBlocks(body.blocks, func(b *block) {
		for _, rn := range b.runs {
			if rn.style&glyph == 0 {
				got = append(got, strings.TrimPrefix(rn.text, "\u2060")+"→"+rn.href)
			}
		}
	})
	want := []string{"Thanks →", "SerKo→https://github.com/serkodev", ", →", "wooorm→https://github.com/wooorm", " and →",
		"Vite→https://opencollective.com/vite", ".→", "{@keep}→"}
	if !slices.Equal(got, want) {
		t.Errorf("runs %q, want %q", got, want)
	}
}

// White space beside an inline element shows as one space, also after a paragraph's first piece; none is added
// where the source has none.
func TestInlineSpaces(t *testing.T) {
	for html, want := range map[string]string{
		`<p><a href="https://devfra.me/"><strong>Devframe</strong></a> is neutral.</p>`: "Devframe is neutral.",
		`<p><em>a</em>  <code>b</code> c</p>`:                                           "a b c",
		`<p>see <a href="https://antfu.me/">it</a>.</p>`:                                "see it.",
		`<p><strong>x </strong> y</p>`:                                                  "x y",
		`<p>用<a href="https://antfu.me/">链接</a>写</p>`:                                   "用链接写",
	} {
		var sb strings.Builder
		walkBlocks(Prepare(&Content{Status: "ready", HTML: html}).blocks, func(b *block) {
			for _, rn := range b.runs {
				sb.WriteString(rn.text)
			}
		})
		if got := sb.String(); got != want {
			t.Errorf("%s reads %q, want %q", html, got, want)
		}
	}
}

// A link to a site with a mark shows the mark before its words, outside the link; other links, and links that hold
// only a picture, show none.
func TestLinkMarks(t *testing.T) {
	for href, want := range map[string]string{
		"https://github.com/antfu": "github", "https://docs.github.com/x": "github", "https://en.wikipedia.org/wiki/A": "wikipedia",
		"https://x.com/a": "x", "https://www.youtube.com/watch?v=1": "youtube", "https://antfu.me/posts": "", "https://notgithub.com/": "",
	} {
		if got := glyphOf(href); got != want {
			t.Errorf("%s: mark %q, want %q", href, got, want)
		}
	}
	body := Prepare(&Content{Status: "ready", HTML: `<p>See <a href="https://github.com/vitejs/vite">Vite</a>, <a href="https://antfu.me">antfu.me</a>` +
		` and <a href="https://github.com/x"><img src="https://example.com/a.png"></a>, thanks {@wooorm}.</p>`})
	var got []string
	walkBlocks(body.blocks, func(b *block) {
		for _, rn := range b.runs {
			if rn.style&glyph != 0 {
				got = append(got, "["+rn.glyph+"]"+rn.href)
			} else if rn.href != "" {
				got = append(got, strings.TrimPrefix(rn.text, "\u2060"))
			}
		}
	})
	if want := []string{"[github]", "Vite", "antfu.me", "[github]", "wooorm"}; !slices.Equal(got, want) {
		t.Errorf("links and marks %q, want %q", got, want)
	}
	// The words after a mark are joined to it, so a line does not break between them.
	walkBlocks(body.blocks, func(b *block) {
		for i, rn := range b.runs {
			if rn.style&glyph != 0 && (i+1 >= len(b.runs) || !strings.HasPrefix(b.runs[i+1].text, "\u2060")) {
				t.Errorf("the words after the %s mark are not joined to it", rn.glyph)
			}
		}
	})
}

// Shiki's notation comments leave the code, as the sites that write them show it: a highlighted line keeps its mark,
// a notation alone on its line marks the line after it, and a word notation sets the word apart where it holds.
func TestCodeNotations(t *testing.T) {
	code := "// [!code word:Promise]\nconst a = Promise.resolve()\nmarkdown = pre(markdown) // [!code hl]\n# [!code ++]\nadd()\nb = Promise.all([]) /* [!code --] */\n// keep [!code word:x:1]\nx + x\ny = x"
	sp, clean, marks := notations([]ui.Span{{Text: code}}, code)
	want := "const a = Promise.resolve()\nmarkdown = pre(markdown)\nadd()\nb = Promise.all([])\n// keep\nx + x\ny = x"
	if clean != want {
		t.Errorf("code %q,\nwant %q", clean, want)
	}
	if wantMarks := map[int]lineMark{1: lineHighlight, 2: lineInserted, 3: lineDeleted}; !maps.Equal(marks, wantMarks) {
		t.Errorf("marks %v, want %v", marks, wantMarks)
	}
	var text strings.Builder
	var grounds []string
	for _, s := range sp {
		text.WriteString(s.Text)
		if s.Background == theme.CodeWord {
			grounds = append(grounds, s.Text)
		}
	}
	if text.String() != want {
		t.Errorf("the spans spell %q", text.String())
	}
	if wantGrounds := []string{"Promise", "Promise", "x", "x"}; !slices.Equal(grounds, wantGrounds) {
		t.Errorf("words set apart %q, want %q", grounds, wantGrounds)
	}
}

// A link's light takes in its site's mark; a piece of the flow cuts it open where the piece ends.
func TestLinkGrounds(t *testing.T) {
	body := Prepare(&Content{Status: "ready", HTML: `<p>See <a href="https://github.com/a/b">Vite</a> and <a href="https://antfu.me">antfu</a>.</p>`})
	r := NewReader(&Article{ID: "g", Title: "g"}, &Content{Status: "ready"}, body)
	segs := r.segments(body.blocks[0])
	links := linkSpans(segs)
	text := utf16Of(spans(segs, 17))
	if len(links) != 2 {
		t.Fatalf("%d links, want 2", len(links))
	}
	if got := string(utf16.Decode(text[links[0].start:links[0].end])); got != "￼⁠Vite" {
		t.Errorf("the first ground covers %q, want the mark and Vite", got)
	}
	if got := string(utf16.Decode(text[links[1].start:links[1].end])); got != "antfu" {
		t.Errorf("the second ground covers %q", got)
	}
	cut := within(links, links[0].start+2, len(text))
	if len(cut) != 2 || !cut[0].openL || cut[0].openR || cut[0].start != 0 || cut[1].openL {
		t.Errorf("cut into a piece: %+v", cut)
	}
}
