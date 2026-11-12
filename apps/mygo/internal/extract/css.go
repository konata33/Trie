package extract

import (
	"strings"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

// getComputedStyle(element).display and .visibility, as jsdom 30 cascades them, reduced to what removeHidden reads:
// the user-agent rules that set display on the elements it checks, then the document's <style> sheets in order (rules at
// top level and in @media lists naming all or screen; other at-rules are skipped, external sheets never load), then the
// style attribute. A later rule wins at equal or higher specificity, !important wins over both, and visibility inherits.
// ponytail: selectors are cascadia's, not dom-selector's (no :is or :where, which drop their rule here), and values are
// checked against keyword lists rather than the CSS grammar.

type declaration struct {
	value     string
	important bool
}

type cssRule struct {
	match func(*html.Node) bool
	spec  cascadia.Specificity
	decls map[string]declaration // display and visibility only
}

type cascade struct {
	doc       *html.Node
	sheets    map[*html.Node][]cssRule // by <style> element
	order     []*html.Node
	connected int // sheets still in the document when memo was filled
	memo      map[memoKey]string
}

type memoKey struct {
	n    *html.Node
	prop string
}

var (
	displayKeywords   = set("none contents block inline run-in flow flow-root table flex grid ruby list-item table-row-group table-header-group table-footer-group table-row table-cell table-column-group table-column table-caption ruby-base ruby-text ruby-base-container ruby-text-container inline-block inline-table inline-flex inline-grid math -webkit-box -webkit-inline-box -webkit-flex -webkit-inline-flex -moz-box -moz-inline-box -moz-inline-stack -ms-flexbox -ms-inline-flexbox -ms-grid -ms-inline-grid")
	displayParts      = set("block inline run-in flow flow-root table flex grid ruby list-item")
	visibilityKeyword = set("visible hidden collapse")
	globalKeywords    = set("inherit initial unset revert revert-layer")
)

// userAgent are the rules of jsdom's default stylesheet that give display to the elements removeHidden checks, in order.
var userAgent = func() []cssRule {
	block := map[string]declaration{"display": {value: "block"}}
	none := map[string]declaration{"display": {value: "none"}}
	tags := func(names string) func(*html.Node) bool {
		s := set(names)
		return func(n *html.Node) bool { return n.Namespace == "" && s[n.Data] }
	}
	return []cssRule{
		{tags("address blockquote center dialog div figure figcaption footer form header hr legend listing main p plaintext pre search xmp"), cascadia.Specificity{0, 0, 1}, block},
		{func(n *html.Node) bool { return htmlElement(n, "dialog") && !hasAttr(n, "open") }, cascadia.Specificity{0, 1, 1}, none},
		// :popover-open never holds in a static document.
		{func(n *html.Node) bool {
			return hasAttr(n, "popover") && !(htmlElement(n, "dialog") && hasAttr(n, "open"))
		}, cascadia.Specificity{0, 3, 1}, none},
		{tags("article aside h1 h2 h3 h4 h5 h6 hgroup nav section"), cascadia.Specificity{0, 0, 1}, block},
	}
}()

func newCascade(doc *html.Node) *cascade {
	c := &cascade{doc: doc, sheets: map[*html.Node][]cssRule{}}
	for _, s := range queryAll(doc, "style") {
		if s.Namespace != "" {
			continue
		}
		if t := strings.ToLower(strings.TrimSpace(attr(s, "type"))); t != "" && t != "text/css" {
			continue
		}
		c.order = append(c.order, s)
		c.sheets[s] = parseSheet(textContent(s))
	}
	return c
}

func (c *cascade) display(n *html.Node) string    { return c.computed(n, "display") }
func (c *cascade) visibility(n *html.Node) string { return c.computed(n, "visibility") }

// computed resolves CSS-wide keywords and inheritance. The memo lasts while the sheets do: removing an element changes
// no other element's value unless it takes a <style> with it.
func (c *cascade) computed(n *html.Node, prop string) string {
	connected := 0
	for _, s := range c.order {
		if contains(c.doc, s) {
			connected++
		}
	}
	if c.memo == nil || connected != c.connected {
		c.memo, c.connected = map[memoKey]string{}, connected
	}
	return c.resolve(n, prop)
}

func (c *cascade) resolve(n *html.Node, prop string) string {
	if v, ok := c.memo[memoKey{n, prop}]; ok {
		return v
	}
	value := c.cascaded(n, prop)
	inherited := prop == "visibility"
	initial := map[string]string{"display": "inline", "visibility": "visible"}[prop]
	switch {
	case value == "inherit" || inherited && (value == "" || value == "unset"):
		if parent := parentElement(n); parent != nil {
			value = c.resolve(parent, prop)
		} else {
			value = initial
		}
	case value == "" || value == "initial" || value == "unset":
		value = initial
	}
	c.memo[memoKey{n, prop}] = value
	return value
}

// cascaded is the declared value that wins for n: rules in order, a later one at equal or higher specificity, any
// !important over the rest, then the style attribute over what is not !important.
func (c *cascade) cascaded(n *html.Node, prop string) string {
	var winner declaration
	var spec cascadia.Specificity
	found := false
	apply := func(d declaration, s cascadia.Specificity) {
		switch {
		case d.important:
			winner = d
		case winner.important:
		case !found || !s.Less(spec):
			winner, spec, found = d, s, true
		}
	}
	for _, r := range userAgent {
		if d, ok := r.decls[prop]; ok && r.match(n) {
			apply(d, r.spec)
		}
	}
	for _, s := range c.order {
		if !contains(c.doc, s) {
			continue
		}
		for _, r := range c.sheets[s] {
			if d, ok := r.decls[prop]; ok && r.match(n) {
				apply(d, r.spec)
			}
		}
	}
	for _, d := range parseDeclarations(attr(n, "style")) {
		if d.name == prop && (!winner.important || d.important) {
			winner = d.declaration
		}
	}
	return winner.value
}

type namedDeclaration struct {
	name string
	declaration
}

// parseSheet reads the style rules of a stylesheet that declare display or visibility.
func parseSheet(css string) []cssRule {
	var out []cssRule
	css = stripComments(css)
	for i := 0; i < len(css); {
		prelude, block, next, ok := nextRule(css, i)
		if !ok {
			break
		}
		i = next
		prelude = strings.TrimSpace(prelude)
		switch {
		case strings.HasPrefix(strings.ToLower(prelude), "@media"):
			if mediaMatches(prelude[len("@media"):]) {
				out = append(out, parseSheet(block)...)
			}
		case strings.HasPrefix(prelude, "@") || block == "" && prelude == "":
		default:
			decls := map[string]declaration{}
			for _, d := range parseDeclarations(block) {
				decls[d.name] = d.declaration
			}
			if len(decls) == 0 {
				continue
			}
			g, err := cascadia.ParseGroup(prelude)
			if err != nil {
				continue // a selector this matcher cannot read matches nothing here
			}
			var spec cascadia.Specificity
			for _, s := range g {
				if x := s.Specificity(); spec.Less(x) {
					spec = x
				}
			}
			out = append(out, cssRule{match: g.Match, spec: spec, decls: decls})
		}
	}
	return out
}

func mediaMatches(list string) bool {
	if strings.TrimSpace(list) == "" {
		return true
	}
	for _, q := range splitTop(list, ',') {
		if q := strings.ToLower(strings.TrimSpace(q)); q == "all" || q == "screen" {
			return true
		}
	}
	return false
}

// nextRule reads one rule from css at i: its prelude and, for a block, the text inside the braces.
func nextRule(css string, i int) (prelude, block string, next int, ok bool) {
	depth, quote := 0, byte(0)
	start := i
	for ; i < len(css); i++ {
		c := css[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '\\':
			i++
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case c == ';' && depth <= 0:
			return css[start:i], "", i + 1, true // an at-rule without a block
		case c == '{' && depth <= 0:
			end := matchBrace(css, i)
			return css[start:i], css[i+1 : min(end, len(css))], end + 1, true
		}
	}
	return "", "", len(css), false
}

func matchBrace(css string, open int) int {
	depth, quote := 0, byte(0)
	for i := open; i < len(css); i++ {
		c := css[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '\\':
			i++
		case c == '{':
			depth++
		case c == '}':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return len(css)
}

func stripComments(css string) string {
	var b strings.Builder
	quote := byte(0)
	for i := 0; i < len(css); i++ {
		c := css[i]
		switch {
		case quote != 0:
			b.WriteByte(c)
			if c == '\\' && i+1 < len(css) {
				i++
				b.WriteByte(css[i])
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
			b.WriteByte(c)
		case c == '/' && i+1 < len(css) && css[i+1] == '*':
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += end + 3
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// splitTop splits s at sep outside brackets, braces and strings.
func splitTop(s string, sep byte) []string {
	var out []string
	depth, quote, start := 0, byte(0), 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == sep && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// parseDeclarations reads display and visibility from a declaration block, dropping values the property does not take;
// nested rules are skipped.
func parseDeclarations(block string) []namedDeclaration {
	var out []namedDeclaration
	for _, part := range splitTop(block, ';') {
		if strings.Contains(part, "{") {
			continue
		}
		name, value, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "display" && name != "visibility" {
			continue
		}
		value = strings.TrimSpace(value)
		important := false
		if i := strings.LastIndex(value, "!"); i >= 0 && strings.EqualFold(strings.TrimSpace(value[i+1:]), "important") {
			important, value = true, strings.TrimSpace(value[:i])
		}
		if v, ok := validValue(name, value); ok {
			out = append(out, namedDeclaration{name, declaration{v, important}})
		}
	}
	return out
}

func validValue(name, value string) (string, bool) {
	v := strings.ToLower(value)
	switch {
	case v == "":
		return "", false
	case globalKeywords[v], strings.Contains(v, "var("):
		return v, true
	case name == "visibility":
		return v, visibilityKeyword[v]
	}
	words := strings.Fields(v)
	if len(words) == 1 {
		if v == "flow" {
			return "block", true
		}
		return v, displayKeywords[v]
	}
	for _, w := range words {
		if !displayParts[w] {
			return "", false
		}
	}
	return v, len(words) <= 3
}
