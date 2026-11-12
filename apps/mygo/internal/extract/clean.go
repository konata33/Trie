package extract

import (
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// SimpleClean (the selection bench's baseline, kept for TestSpike) is the allowlist half of extract-content.ts sanitize(): the tags and attributes the reader keeps, public links, and a
// picture's address picked from srcset like simpleSrcset(). None of the app's page rules (hidden parts, paywalls, repeated header,
// table of contents, gallery adapters, whiteboards, React segments) and no formatting: the bench measures how far this alone gets.
// It returns the cleaned HTML and the picture addresses still to fetch.
func SimpleClean(content string, base *url.URL) (string, []string) {
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(content), body)
	if err != nil {
		return "", nil
	}
	var out strings.Builder
	var images []string
	seen := map[string]bool{}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			out.WriteString(html.EscapeString(n.Data))
			return
		case html.ElementNode:
		default:
			return
		}
		if dropped[n.Data] {
			return
		}
		if !kept[n.Data] { // sanitize-html discards a tag it does not allow but keeps what is inside
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			return
		}
		attrs := cleanAttrs(n, base)
		if n.Data == "img" {
			if attrs == nil {
				if alt := attr(n, "alt"); alt != "" {
					out.WriteString(html.EscapeString(alt))
				}
				return
			}
			if src := attrs[0].Val; !strings.HasPrefix(src, "data:") && !seen[src] {
				seen[src] = true
				images = append(images, src)
			}
		}
		out.WriteString("<" + n.Data)
		for _, a := range attrs {
			out.WriteString(" " + a.Key + `="` + html.EscapeString(a.Val) + `"`)
		}
		out.WriteString(">")
		if simpleVoids[n.Data] {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		out.WriteString("</" + n.Data + ">")
	}
	for _, n := range nodes {
		walk(n)
	}
	return out.String(), images
}

var (
	kept        = set("p br h1 h2 h3 h4 h5 h6 ul ol li blockquote pre code table thead tbody tfoot tr th td caption figure figcaption a img hr strong em b i s del sup sub div span")
	dropped     = set("script style textarea option svg math iframe object embed template noscript form")
	simpleVoids = set("br hr img")
	allowed     = map[string][]string{"a": {"href", "title"}, "img": {"src", "alt", "title", "width", "height"}, "code": {"class"}, "pre": {"class"},
		"th": {"colspan", "rowspan", "scope"}, "td": {"colspan", "rowspan"}, "ol": {"start"}}
	languageClass = regexp.MustCompile(`^language-[a-zA-Z0-9_-]+$`)
	dimension     = regexp.MustCompile(`^\d{1,4}$`)
	inlineImage   = regexp.MustCompile(`^data:image/(?:png|jpeg|gif|webp|avif|svg\+xml);base64,[A-Za-z0-9+/]+={0,2}$`)
	reservedHost  = regexp.MustCompile(`\.(?:localhost|local|internal|invalid|test)$`)
	srcsetParts   = regexp.MustCompile(`,\s+`)
)

// cleanAttrs returns the attributes kept on n; for an img, src comes first, and nil means the picture has no usable source.
func cleanAttrs(n *html.Node, base *url.URL) []html.Attribute {
	var out []html.Attribute
	if n.Data == "img" {
		if attr(n, "width") == "1" && attr(n, "height") == "1" { // a view counter
			return nil
		}
		src := attr(n, "src")
		if !inlineImage.MatchString(src) {
			srcset := attr(n, "srcset")
			if srcset == "" {
				srcset = attr(n, "data-srcset")
			}
			src = simpleSrcset(srcset, base)
			for _, key := range []string{"src", "data-src", "data-original"} {
				if src == "" {
					src = simplePublicURL(attr(n, key), base)
				}
			}
		}
		if src == "" {
			return nil
		}
		out = append(out, html.Attribute{Key: "src", Val: src})
	}
	if id := attr(n, "id"); id != "" {
		out = append(out, html.Attribute{Key: "id", Val: id})
	}
	for _, key := range allowed[n.Data] {
		v := attr(n, key)
		switch {
		case v == "" || key == "src":
			continue
		case key == "href":
			if !strings.HasPrefix(v, "#") {
				v = simplePublicURL(v, base)
			}
		case key == "class":
			var langs []string
			for _, c := range strings.Fields(v) {
				if languageClass.MatchString(c) {
					langs = append(langs, c)
				}
			}
			v = strings.Join(langs, " ")
		case key == "width" || key == "height":
			if !dimension.MatchString(v) {
				v = ""
			}
		}
		if v != "" {
			out = append(out, html.Attribute{Key: key, Val: v})
		}
	}
	return out
}

// publicURL is extract-content.ts simplePublicURL(): an absolute http(s) address without credentials on a public host, or "".
func simplePublicURL(value string, base *url.URL) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 8192 || strings.IndexFunc(value, func(r rune) bool { return r <= ' ' || r == 0x7f }) >= 0 {
		return ""
	}
	u, err := base.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		if !ip.IsGlobalUnicast() || ip.IsPrivate() {
			return ""
		}
	} else if !strings.Contains(host, ".") || host == "localhost" || reservedHost.MatchString(host) {
		return ""
	}
	return u.String()
}

// simpleSrcset picks the smallest candidate at least 1600 wide, else the largest (extract-content.ts fromSrcset).
func simpleSrcset(srcset string, base *url.URL) string {
	type candidate struct {
		url   string
		width int
	}
	var cs []candidate
	for _, part := range srcsetParts.Split(srcset, -1) {
		f := strings.Fields(part)
		if len(f) != 2 || !strings.HasSuffix(f[1], "w") {
			continue
		}
		w, err := strconv.Atoi(strings.TrimSuffix(f[1], "w"))
		if u := simplePublicURL(f[0], base); err == nil && u != "" {
			cs = append(cs, candidate{u, w})
		}
	}
	if len(cs) == 0 {
		return ""
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].width < cs[j].width })
	for _, c := range cs {
		if c.width >= 1600 {
			return c.url
		}
	}
	return cs[len(cs)-1].url
}
