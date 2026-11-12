package extract

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Measure is what the bench compares in an article's HTML, whichever side produced it. Text is read the same way for both:
// text nodes, a line break at every block, whitespace collapsed; the formatter's code-block header ("纯文本 / 复制") is not article text.
type Measure struct {
	Blocks     []string // one per block of text, in order
	Tokens     map[string]int
	Count      int // tokens
	Length     int // characters of the joined blocks
	Paragraphs int // <p> with text
	Images     []string
	Pre        int
	Tables     int
	Headings   int
}

var blockTags = set("p div li ul ol h1 h2 h3 h4 h5 h6 pre blockquote figure figcaption table thead tbody tfoot tr td th caption section article header footer aside nav main dl dt dd hr br details summary")

func Measured(fragment string) Measure {
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, _ := html.ParseFragment(strings.NewReader(fragment), body)
	m := Measure{Tokens: map[string]int{}}
	var line strings.Builder
	flush := func() {
		if s := strings.Join(strings.Fields(line.String()), " "); s != "" {
			m.Blocks = append(m.Blocks, s)
		}
		line.Reset()
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			line.WriteString(n.Data)
			return
		}
		if n.Type != html.ElementNode {
			return
		}
		switch n.Data {
		case "script", "style", "template":
			return
		case "img":
			if src := attr(n, "src"); src != "" {
				m.Images = append(m.Images, src)
			}
		case "pre":
			m.Pre++
		case "table":
			m.Tables++
		case "h1", "h2", "h3", "h4", "h5", "h6":
			m.Headings++
		case "p":
			if strings.TrimSpace(textOf(n)) != "" {
				m.Paragraphs++
			}
		}
		if slices.Contains(strings.Fields(attr(n, "class")), "reader-code-header") {
			return
		}
		block := blockTags[n.Data]
		if block {
			flush()
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if block {
			flush()
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	flush()
	for i, b := range m.Blocks {
		m.Length += utf8.RuneCountInString(b)
		if i > 0 {
			m.Length++
		}
		for _, t := range tokens(b) {
			m.Tokens[t]++
			m.Count++
		}
	}
	return m
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// tokens splits text into lower-cased words of letters and digits, with every CJK character a token of its own; punctuation is dropped.
func tokens(s string) []string {
	var out []string
	var word strings.Builder
	end := func() {
		if word.Len() > 0 {
			out = append(out, word.String())
			word.Reset()
		}
	}
	for _, r := range s {
		switch {
		case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
			end()
			out = append(out, string(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteRune(unicode.ToLower(r))
		default:
			end()
		}
	}
	end()
	return out
}

// Similarity is the Dice coefficient of the two token multisets: 1 when both say the same words as often, 0 when nothing is shared.
// Order is ignored; extraction differs by what it keeps or drops, which this sees.
func Similarity(a, b Measure) float64 {
	if a.Count+b.Count == 0 {
		return 1
	}
	shared := 0
	for t, n := range a.Tokens {
		shared += min(n, b.Tokens[t])
	}
	return 2 * float64(shared) / float64(a.Count+b.Count)
}

// Unmatched returns the blocks of a that b does not have (as a multiset), longest first.
func Unmatched(a, b []string) []string {
	have := map[string]int{}
	for _, s := range b {
		have[s]++
	}
	var out []string
	for _, s := range a {
		if have[s] > 0 {
			have[s]--
		} else {
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(x, y string) int { return utf8.RuneCountInString(y) - utf8.RuneCountInString(x) })
	return out
}

// Missing returns the strings of a not in b, in a's order.
func Missing(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
