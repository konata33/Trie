package reader

import (
	"strings"

	"trie/internal/locales"
	"trie/internal/theme"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Content colors: they mark what a piece of the article is, not the
// interface (internal/theme, DESIGN.md Reading Content).
var (
	calloutColors = map[string]ui.Color{
		"NOTE": theme.CalloutNote, "TIP": theme.CalloutTip, "IMPORTANT": theme.CalloutImportant, "WARNING": theme.CalloutWarning,
	}
	// roleColors are those of the roles whose classes, reader-token-1 to
	// reader-token-8, the formatter gives code tokens.
	roleColors = [...]ui.Color{
		roleString: theme.CodeString, roleNumber: theme.CodeNumber, roleKeyword: theme.CodeKeyword,
		roleFunction: theme.CodeFunction, roleKey: theme.CodeKey, roleType: theme.CodeType,
		rolePunctuation: theme.CodePunctuation, roleComment: theme.CodeComment,
	}
)

type role uint8

// The roles in the formatter's order: reader-token-N is role N.
const (
	roleText role = iota
	roleString
	roleNumber
	roleKeyword
	roleFunction
	roleKey
	roleType
	rolePunctuation
	roleComment
)

// plainText is the label of a pre whose code names no language, which the header says in the interface's language.
const plainText = "text"

var calloutLabels = map[string]func() string{"NOTE": locales.ReaderCalloutNote, "TIP": locales.ReaderCalloutTip, "IMPORTANT": locales.ReaderCalloutImportant,
	"WARNING": locales.ReaderCalloutWarning, "CAUTION": locales.ReaderCalloutCaution}

// codeView is a pre: its code, highlighted.
type codeView struct {
	id    string // the code's id, which the copy button's state follows
	label string // the header's: the language, or plainText (纯文本); empty for a pre without one
	code  string // what the copy button copies
	spans []ui.Span
	marks map[int]lineMark // the lines its notations set apart, by index
	side  ui.ScrollState   // its sideways scrolling
}

func (b *builder) pre(n *node, unit, id string) *block {
	blk := &block{kind: Code, unit: unit, anchor: id, top: 1.6, bottom: 1.8}
	for _, ch := range n.Children {
		if ch.Type != "element" || ch.Tag != "code" {
			continue
		}
		if cb, ok := b.codes[ch.prop("id")]; ok {
			if u := ch.prop("dataReaderUnit"); u != "" {
				blk.unit = u
			}
			label := cb.Language
			if label == "" {
				label = plainText
			}
			sp, code, marks := notations(b.highlight(cb), cb.Code)
			blk.code = &codeView{id: cb.ID, label: label, code: code, spans: cjkSpans(sp), marks: marks}
			return blk
		}
		break
	}
	// A pre the formatter did not pair with a code block shows its text as
	// it is, without a header.
	var sb strings.Builder
	var walk func(n *node)
	walk = func(n *node) {
		switch {
		case n.Type == "text":
			sb.WriteString(n.Value)
		case n.Tag == "br":
			sb.WriteByte('\n')
		case n.Type == "element" && !skipped[n.Tag]:
			for _, ch := range n.Children {
				walk(ch)
			}
		}
	}
	walk(n)
	text := strings.TrimRight(strings.TrimPrefix(sb.String(), "\n"), "\n")
	blk.code = &codeView{code: text, spans: cjkSpans([]ui.Span{{Text: text}})}
	return blk
}

// highlight colors a code block by the roles its stored tokens name, when
// they spell the code exactly. Tokens of older formats name a light theme's
// colors instead: MyGo's own tokenizers color those blocks, their kinds
// given the same roles and colors.
func (b *builder) highlight(cb CodeBlock) []ui.Span {
	if tokens := b.tokens[cb.ID]; b.roles && spells(tokens, cb.Code) {
		var spans []ui.Span
		for _, t := range tokens {
			r := roleText
			if n, ok := strings.CutPrefix(t.ClassName, "reader-token-"); ok && len(n) == 1 && n[0] >= '1' && n[0] <= '8' {
				r = role(n[0] - '0')
			}
			spans = appendSpan(spans, t.Text, r)
		}
		return spans
	}
	lang := editor.LanguageNamed(cb.Language)
	if lang == nil || lang.Tokenizer == nil {
		return []ui.Span{{Text: cb.Code}}
	}
	var spans []ui.Span
	var state uint64
	var tokens []editor.Token
	for i, line := range strings.Split(cb.Code, "\n") {
		if i > 0 {
			spans = appendSpan(spans, "\n", roleText)
		}
		tokens, state = lang.Tokenizer.Tokenize(line, state, tokens[:0])
		at := 0
		for _, t := range tokens {
			end := min(max(t.End, at), len(line))
			spans = appendSpan(spans, line[at:end], kindRole(t.Kind))
			at = end
		}
		if at < len(line) {
			spans = appendSpan(spans, line[at:], roleText)
		}
	}
	return spans
}

func spells(tokens []codeToken, code string) bool {
	if len(tokens) == 0 {
		return false
	}
	n := 0
	for _, t := range tokens {
		if !strings.HasPrefix(code[n:], t.Text) {
			return false
		}
		n += len(t.Text)
	}
	return n == len(code)
}

// kindRole gives a token kind the role that stored tokens of the same
// scope carry.
func kindRole(k editor.TokenKind) role {
	switch k {
	case editor.TokenKeyword:
		return roleKeyword
	case editor.TokenType:
		return roleType
	case editor.TokenFunction:
		return roleFunction
	case editor.TokenProperty, editor.TokenTag, editor.TokenAttribute:
		return roleKey
	case editor.TokenConstant, editor.TokenNumber:
		return roleNumber
	case editor.TokenString, editor.TokenEscape, editor.TokenRegexp:
		return roleString
	case editor.TokenComment:
		return roleComment
	case editor.TokenOperator, editor.TokenPunctuation:
		return rolePunctuation
	}
	return roleText
}

// appendSpan adds text in r's color, joined to the span before when it
// looks the same: white space takes any color.
func appendSpan(spans []ui.Span, text string, r role) []ui.Span {
	if text == "" {
		return spans
	}
	s := ui.Span{Text: text}
	if r != roleText {
		s.Color = roleColors[r]
		s.Italic = r == roleComment
	}
	if n := len(spans); n > 0 {
		last := &spans[n-1]
		if (last.Color == s.Color && last.Italic == s.Italic) || strings.TrimSpace(text) == "" {
			last.Text += text
			return spans
		}
	}
	return append(spans, s)
}

// cjkSpans draws the Chinese and Japanese of code in PingFang SC, as the
// browser falls back to it from the monospaced faces (see scripts), and
// drops the line break ending the code, which shows no empty line in a pre.
func cjkSpans(spans []ui.Span) []ui.Span {
	if n := len(spans); n > 0 {
		spans[n-1].Text = strings.TrimSuffix(spans[n-1].Text, "\n")
	}
	out := make([]ui.Span, 0, len(spans))
	for _, s := range spans {
		start, cur := 0, false
		for i, ch := range s.Text {
			if c := wide(ch); i == 0 {
				cur = c
			} else if c != cur {
				out = appendFace(out, s, s.Text[start:i], cur)
				start, cur = i, c
			}
		}
		out = appendFace(out, s, s.Text[start:], cur)
	}
	return out
}

func appendFace(out []ui.Span, s ui.Span, text string, cjkText bool) []ui.Span {
	if text == "" {
		return out
	}
	s.Text = text
	if cjkText {
		s.Font = cjkFace
	}
	return append(out, s)
}
