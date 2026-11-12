package extract

import (
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
)

// sanitize-html 2.18.1 in its discard mode, as extract-content.ts and reader-format.ts configure it. It does not build a
// tree: it echoes what htmlparser2 12 reports, tag by tag, and htmlparser2 closes and opens elements by rules of its own
// (an <h2> closes an open <p>, "</p>" alone opens one), which the scanner below keeps. Tokens come from x/net/html's
// tokenizer, which reads serialized HTML (the only input here) as htmlparser2's does.

type attrs []html.Attribute

func (a attrs) get(key string) string {
	for _, x := range a {
		if x.Key == key {
			return x.Val
		}
	}
	return ""
}

type policy struct {
	tags         map[string]bool
	attributes   map[string][]string // by tag, and "*"
	classes      map[string][]string // allowed class names by tag; a tag listed here may keep class
	classPattern map[string]*regexp.Regexp
	schemes      []string
	imgSchemes   []string
	nonText      map[string]bool
	// transform replaces a tag's name and attributes before they are checked; text, when set, is the element's text.
	transform map[string]func(attrs) (string, attrs, *string)
}

var (
	readerTags    = set("p br h1 h2 h3 h4 h5 h6 ul ol li blockquote pre code table thead tbody tfoot tr th td caption figure figcaption a img hr strong em b i s del sup sub div span")
	validAttr     = regexp.MustCompile(`^[^\x00\t\n\f\r /<=>]+$`)
	schemeOf      = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9.\-+]*):`)
	selfClosing   = set("img br hr area base basefont input link meta col")
	nonBoolean    = set("abbr accept accept-charset accesskey action allow alt as autocapitalize autocomplete blocking charset cite class color cols colspan content contenteditable coords crossorigin data datetime decoding dir dirname download draggable enctype enterkeyhint fetchpriority for form formaction formenctype formmethod formtarget headers height hidden high href hreflang http-equiv id imagesizes imagesrcset inputmode integrity is itemid itemprop itemref itemtype kind label lang list loading low max maxlength media method min minlength name nonce optimum pattern ping placeholder popover popovertarget popovertargetaction poster preload referrerpolicy rel rows rowspan sandbox scope shape size sizes slot span spellcheck src srcdoc srclang srcset start step style tabindex target title translate type usemap value width wrap onauxclick onafterprint onbeforematch onbeforeprint onbeforeunload onbeforetoggle onblur oncancel oncanplay oncanplaythrough onchange onclick onclose oncontextlost oncontextmenu oncontextrestored oncopy oncuechange oncut ondblclick ondrag ondragend ondragenter ondragleave ondragover ondragstart ondrop ondurationchange onemptied onended onerror onfocus onformdata onhashchange oninput oninvalid onkeydown onkeypress onkeyup onlanguagechange onload onloadeddata onloadedmetadata onloadstart onmessage onmessageerror onmousedown onmouseenter onmouseleave onmousemove onmouseout onmouseover onmouseup onoffline ononline onpagehide onpageshow onpaste onpause onplay onplaying onpopstate onprogress onratechange onreset onresize onrejectionhandled onscroll onscrollend onsecuritypolicyviolation onseeked onseeking onselect onslotchange onstalled onstorage onsubmit onsuspend ontimeupdate ontoggle onunhandledrejection onunload onvolumechange onwaiting onwheel")
	schemeChecked = set("href src cite action formaction data xlink:href poster background ping longdesc usemap codebase classid archive profile manifest itemid dynsrc lowsrc")
	controlRun    = regexp.MustCompile(`[\x00-\x20]+`)
	// jsSpaces is JavaScript's /\s+/.
	jsSpaces    = regexp.MustCompile(`[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+`)
	textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
)

type frame struct {
	tag, name string // as parsed, and as transformed
	innerText *string
}

func (p *policy) run(input string) string {
	var out strings.Builder
	var stack []*frame
	skip := map[int]bool{}
	renamed := map[int]string{}
	depth, skipText, skipTextDepth, addedText := 0, false, 0, false
	open := func(name string, a attrs) {
		if skipText {
			skipTextDepth++
			return
		}
		f := &frame{tag: name, name: name}
		stack = append(stack, f)
		if t := p.transform[name]; t != nil {
			var text *string
			var tag string
			tag, a, text = t(a)
			f.innerText = text
			if tag != name {
				f.name, name = tag, tag
				renamed[depth] = tag
			}
		}
		skipped := false
		if !p.tags[name] {
			skipped = true
			skip[depth] = true
			if p.nonText[name] {
				skipText, skipTextDepth = true, 1
			}
		}
		depth++
		if skipped {
			if f.innerText != nil && *f.innerText != "" {
				out.WriteString(escapeHTML(*f.innerText, false))
				addedText = true
			}
			return
		}
		out.WriteString("<" + name)
		allowed := append(slices.Clone(p.attributes[name]), p.attributes["*"]...)
		if _, ok := p.classes[name]; ok {
			allowed = append(allowed, "class")
		}
		for _, x := range a {
			key, value := x.Key, x.Val
			if !validAttr.MatchString(key) || value == "" && key != "alt" && nonBoolean[key] || !slices.Contains(allowed, key) {
				continue
			}
			if schemeChecked[key] && p.naughty(name, value) {
				continue
			}
			if key == "class" {
				var kept []string
				for _, c := range jsSpaces.Split(value, -1) {
					if slices.Contains(p.classes[name], c) || p.classPattern[name] != nil && p.classPattern[name].MatchString(c) {
						kept = append(kept, c)
					}
				}
				if value = strings.Join(kept, " "); value == "" {
					continue
				}
			}
			out.WriteString(" " + key)
			if value != "" {
				out.WriteString(`="` + escapeHTML(value, true) + `"`)
			} else if key == "alt" {
				out.WriteString(`=""`)
			}
		}
		if selfClosing[name] {
			out.WriteString(" />")
		} else {
			out.WriteString(">")
			if f.innerText != nil && *f.innerText != "" {
				out.WriteString(escapeHTML(*f.innerText, false))
				addedText = true
			}
		}
	}
	text := func(s string) {
		if skipText {
			return
		}
		if n := len(stack); n > 0 && stack[n-1].innerText != nil {
			s = *stack[n-1].innerText
		}
		if !addedText {
			out.WriteString(escapeHTML(s, false))
		}
	}
	closeTag := func(name string) {
		if skipText {
			if skipTextDepth--; skipTextDepth > 0 {
				return
			}
			skipText = false
		}
		if len(stack) == 0 {
			return
		}
		f := stack[len(stack)-1]
		if f.tag != name {
			return
		}
		stack = stack[:len(stack)-1]
		depth--
		if skip[depth] {
			delete(skip, depth)
			return
		}
		if r, ok := renamed[depth]; ok {
			name = r
			delete(renamed, depth)
		}
		if selfClosing[name] {
			return
		}
		out.WriteString("</" + name + ">")
		addedText = false
	}
	scan(input, open, text, closeTag)
	return out.String()
}

// naughty is launder's naughtyHref with the tag's schemes; protocol-relative addresses are refused.
func (p *policy) naughty(tag, href string) bool {
	href = controlRun.ReplaceAllString(href, "")
	for {
		i := strings.Index(href, "<!--")
		if i < 0 {
			break
		}
		j := strings.Index(href[i+4:], "-->")
		if j < 0 {
			break
		}
		href = href[:i] + href[i+4+j+3:]
	}
	m := schemeOf.FindStringSubmatch(href)
	if m == nil {
		return strings.HasPrefix(href, "//") || strings.HasPrefix(href, `\\`) || strings.HasPrefix(href, `/\`) || strings.HasPrefix(href, `\/`)
	}
	schemes := p.schemes
	if tag == "img" {
		schemes = p.imgSchemes
	}
	return !slices.Contains(schemes, strings.ToLower(m[1]))
}

// escapeHTML is sanitize-html's escapeHtml with entities decoded: & < > and, in attributes, ".
func escapeHTML(s string, quote bool) string {
	if quote {
		return attrEscaper.Replace(s)
	}
	return textEscaper.Replace(s)
}

// htmlparser2's rules for elements it opens and closes on its own.
var (
	headingCloses    = set("h1 h2 h3 h4 h5 h6 p")
	formCloses       = set("input option optgroup select button datalist textarea")
	pCloses          = set("p")
	openImpliesClose = map[string]map[string]bool{
		"tr": set("tr th td"), "th": set("th"), "td": set("thead th td"), "body": set("head link script"), "a": set("a"), "li": set("li"), "p": pCloses,
		"h1": headingCloses, "h2": headingCloses, "h3": headingCloses, "h4": headingCloses, "h5": headingCloses, "h6": headingCloses,
		"select": formCloses, "input": formCloses, "output": formCloses, "button": formCloses, "datalist": formCloses, "textarea": formCloses,
		"option": set("option"), "optgroup": set("optgroup option"), "dd": set("dd dt"), "dt": set("dd dt"),
		"address": pCloses, "article": pCloses, "aside": pCloses, "blockquote": pCloses, "details": pCloses, "div": pCloses, "dl": pCloses,
		"fieldset": pCloses, "figcaption": pCloses, "figure": pCloses, "footer": pCloses, "form": pCloses, "header": pCloses, "hr": pCloses,
		"main": pCloses, "nav": pCloses, "ol": pCloses, "pre": pCloses, "section": pCloses, "table": pCloses, "ul": pCloses,
		"rt": set("rt rp"), "rp": set("rt rp"), "tbody": set("thead tbody"), "tfoot": set("thead tbody"),
	}
	parserVoids = set("area base basefont br col command embed frame hr img input isindex keygen link meta param source track wbr")
	integration = set("mi mo mn ms mtext annotation-xml foreignobject desc title")
)

// scan reports the elements of s as htmlparser2's Parser does: opens (after the closes they imply), text, and closes,
// implied ones included; at the end every element still open is closed.
func scan(s string, open func(string, attrs), text func(string), closeTag func(string)) {
	z := html.NewTokenizer(strings.NewReader(s))
	var stack []string // innermost last
	foreign := []bool{false}
	pop := func() {
		name := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if name == "svg" || name == "math" || integration[name] {
			foreign = foreign[:len(foreign)-1]
		}
		closeTag(name)
	}
	start := func(name string, a attrs) bool {
		if name == "form" && slices.Contains(stack, "form") {
			return false // a second form is ignored, end tag and all
		}
		if implied := openImpliesClose[name]; implied != nil {
			for len(stack) > 0 && implied[stack[len(stack)-1]] {
				pop()
			}
		}
		if !parserVoids[name] {
			stack = append(stack, name)
			switch {
			case name == "svg" || name == "math":
				foreign = append(foreign, true)
			case integration[name]:
				foreign = append(foreign, false)
			}
		}
		open(name, a)
		if parserVoids[name] {
			closeTag(name)
		}
		return true
	}
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			for i := len(stack) - 1; i >= 0; i-- {
				closeTag(stack[i])
			}
			return
		case html.TextToken:
			text(string(z.Text()))
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			name := tok.Data
			inForeign := foreign[len(foreign)-1]
			if name == "image" && !inForeign {
				name = "img"
			}
			var a attrs
			for _, x := range tok.Attr {
				if !slices.ContainsFunc(a, func(y html.Attribute) bool { return y.Key == x.Key }) {
					a = append(a, html.Attribute{Key: x.Key, Val: x.Val})
				}
			}
			// A self-closing tag closes in foreign content, judged once the tag is open (so <svg/> closes, <foreignObject/> does not).
			if start(name, a) && tt == html.SelfClosingTagToken && foreign[len(foreign)-1] && !parserVoids[name] && len(stack) > 0 && stack[len(stack)-1] == name {
				pop()
			}
		case html.EndTagToken:
			name := z.Token().Data
			if name == "image" && !foreign[len(foreign)-1] {
				name = "img"
			}
			switch {
			case parserVoids[name]:
				if name == "br" {
					open("br", nil)
					closeTag("br")
				}
			case slices.Contains(stack, name):
				for stack[len(stack)-1] != name {
					pop()
				}
				pop()
			case name == "p":
				start("p", nil)
				pop()
			}
		}
	}
}
