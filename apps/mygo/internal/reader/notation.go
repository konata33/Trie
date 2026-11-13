package reader

import (
	"regexp"
	"strconv"
	"strings"

	"trie/internal/theme"

	"github.com/egoist/mygo/ui"
)

// lineMark is how a line of code is set apart, as Shiki's notation comments mark it.
type lineMark uint8

const (
	lineHighlight lineMark = iota + 1 // [!code hl], [!code highlight]
	lineInserted                      // [!code ++]
	lineDeleted                       // [!code --]
)

var (
	// notation is one of Shiki's notation comments' marks: // [!code hl], # [!code ++], <!-- [!code word:Hello:1] -->.
	notation = regexp.MustCompile(`\s*\[!code ([^\]\n]+)\]`)
	// commentOpen and commentClose are a comment's ends around a notation, which go with it when it is all the
	// comment holds.
	commentOpen  = regexp.MustCompile(`\s*(//|/\*|#|<!--|--|;)\s*$`)
	commentClose = regexp.MustCompile(`^\s*(\*/|-->)`)
)

// notations takes Shiki's notation comments out of code whose spans spell it, as the sites that write them show it:
// it gives the spans and the code without them, the marks of the lines that remain, and the words they set apart
// shown on a ground. A notation alone on its line marks the line after it; a word's holds from the line after its own.
func notations(sp []ui.Span, code string) ([]ui.Span, string, map[int]lineMark) {
	if !strings.Contains(code, "[!code ") {
		return sp, code, nil
	}
	type span struct{ from, to int }
	type wordRule struct {
		word     string
		from, to int // the lines it holds for, kept lines' indexes; to -1 for the rest of the code
	}
	var cuts []span
	var words []wordRule
	marks := map[int]lineMark{}
	kept, at := 0, 0
	var pending []string // the notations of lines gone, for the line after them
	for _, line := range strings.SplitAfter(code, "\n") {
		body := strings.TrimSuffix(line, "\n")
		ms := notation.FindAllStringSubmatchIndex(body, -1)
		kinds := pending
		pending = nil
		var lineCuts []span
		for _, m := range ms {
			from, to := m[0], m[1]
			if o := commentOpen.FindStringIndex(body[:from]); o != nil {
				if c := commentClose.FindStringIndex(body[to:]); c != nil || !strings.HasPrefix(strings.TrimSpace(body[o[0]:from]), "/*") {
					from = o[0]
					if c != nil {
						to += c[1]
					}
				}
			}
			lineCuts = append(lineCuts, span{at + from, at + to})
			kinds = append(kinds, body[m[2]:m[3]])
		}
		rest := body
		for i := len(lineCuts) - 1; i >= 0; i-- {
			rest = rest[:lineCuts[i].from-at] + rest[lineCuts[i].to-at:]
		}
		gone := len(ms) > 0 && strings.TrimSpace(rest) == ""
		if gone {
			cuts = append(cuts, span{at, at + len(line)})
			pending = kinds
		} else {
			cuts = append(cuts, lineCuts...)
			for _, k := range kinds {
				switch {
				case k == "hl" || k == "highlight":
					marks[kept] = lineHighlight
				case k == "++":
					marks[kept] = lineInserted
				case k == "--":
					marks[kept] = lineDeleted
				}
			}
		}
		for _, k := range kinds {
			w, ok := strings.CutPrefix(k, "word:")
			if !ok || w == "" {
				continue
			}
			// It holds from the line after its own, for as many lines as it says, else to the end.
			r := wordRule{word: w, from: kept, to: -1}
			if !gone {
				r.from++
			}
			if i := strings.LastIndexByte(w, ':'); i > 0 {
				if n, err := strconv.Atoi(w[i+1:]); err == nil {
					r.word, r.to = w[:i], r.from+n
				}
			}
			words = append(words, r)
		}
		if !gone {
			kept++
		}
		at += len(line)
	}
	// The code without the notations, and its spans.
	var clean strings.Builder
	prev := 0
	for _, c := range cuts {
		clean.WriteString(code[prev:c.from])
		prev = c.to
	}
	clean.WriteString(code[prev:])
	out := cutSpans(sp, func(i int) bool {
		for _, c := range cuts {
			if i >= c.from && i < c.to {
				return true
			}
		}
		return false
	})
	// The words set apart, where the rules hold.
	text := clean.String()
	var grounds []span
	lineStart, line := 0, 0
	for _, l := range strings.SplitAfter(text, "\n") {
		for _, w := range words {
			if line < w.from || w.to >= 0 && line >= w.to {
				continue
			}
			for i := 0; ; {
				j := strings.Index(l[i:], w.word)
				if j < 0 {
					break
				}
				grounds = append(grounds, span{lineStart + i + j, lineStart + i + j + len(w.word)})
				i += j + len(w.word)
			}
		}
		lineStart += len(l)
		line++
	}
	if len(grounds) > 0 {
		out = groundSpans(out, func(i int) bool {
			for _, g := range grounds {
				if i >= g.from && i < g.to {
					return true
				}
			}
			return false
		})
	}
	return out, text, marks
}

// cutSpans drops the bytes of the spans' text that drop says, by their place in the text the spans spell.
func cutSpans(sp []ui.Span, drop func(int) bool) []ui.Span {
	out := make([]ui.Span, 0, len(sp))
	at := 0
	for _, s := range sp {
		var b strings.Builder
		for i := 0; i < len(s.Text); i++ {
			if !drop(at + i) {
				b.WriteByte(s.Text[i])
			}
		}
		at += len(s.Text)
		if b.Len() > 0 {
			s.Text = b.String()
			out = append(out, s)
		}
	}
	return out
}

// groundSpans sets the bytes that on says on the ground of a word set apart, cutting the spans where it begins and
// ends.
func groundSpans(sp []ui.Span, on func(int) bool) []ui.Span {
	out := make([]ui.Span, 0, len(sp))
	at := 0
	for _, s := range sp {
		start := 0
		for i := 1; i <= len(s.Text); i++ {
			if i < len(s.Text) && on(at+i) == on(at+start) {
				continue
			}
			piece := s
			piece.Text = s.Text[start:i]
			if on(at + start) {
				piece.Background, piece.Radius = theme.CodeWord, 4
			}
			out = append(out, piece)
			start = i
		}
		at += len(s.Text)
	}
	return out
}

// markColors are the ground of a marked line and the rule at its start.
func markColors(m lineMark) (ground, rule ui.Color) {
	switch m {
	case lineInserted:
		return theme.CodeInserted, theme.CodeInsertedRule
	case lineDeleted:
		return theme.CodeDeleted, theme.CodeDeletedRule
	}
	return theme.CodeLine, theme.Accent
}
