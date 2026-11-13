package app

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"trie/internal/explore"
	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/reader"
	"trie/internal/web"
)

// author is a subscribed source as the sidebar shows it: newest writer
// first.
type author struct {
	src    *library.Source
	latest string // the newest article's title
	days   int    // since the newest article; -1 when there is none
	time   int64
	arts   []*library.Article // newest first
}

// derive works out what the pages show from the library.
func (a *App) derive() {
	// Every address an article is known by, for Explore's posts to find theirs (postID).
	a.byURL = map[string]string{}
	for i := range a.lib.Articles {
		art := &a.lib.Articles[i]
		for _, u := range append([]string{art.ID, art.URL}, art.Aliases...) {
			if k, err := web.URLKey(u); err == nil {
				a.byURL[k] = art.ID
			}
		}
	}
	subscribed := map[string]bool{}
	for i := range a.lib.Sources {
		s := &a.lib.Sources[i]
		if s.Subscribed && s.FeedURL != nil && *s.FeedURL != "" {
			subscribed[s.ID] = true
		}
	}
	by := map[string]*author{}
	var out []*author
	for i := range a.lib.Sources {
		s := &a.lib.Sources[i]
		if !subscribed[s.ID] {
			continue
		}
		au := &author{src: s, days: -1, time: math.MinInt64}
		by[s.ID] = au
		out = append(out, au)
	}
	for i := range a.lib.Articles {
		art := &a.lib.Articles[i]
		for _, id := range memberships(art) {
			if au := by[id]; au != nil {
				au.arts = append(au.arts, art)
			}
		}
	}
	now := time.Now().UnixMilli()
	for _, au := range out {
		slices.SortStableFunc(au.arts, byDate)
		if len(au.arts) > 0 {
			au.latest = au.arts[0].Title
			if t, ok := articleTime(au.arts[0]); ok {
				au.time = t
				au.days = max(0, int(math.Round(float64(now-t)/864e5)))
			}
		}
	}
	slices.SortStableFunc(out, func(x, y *author) int {
		switch {
		case x.time > y.time:
			return -1
		case x.time < y.time:
			return 1
		}
		return 0
	})
	// Rows of authors let go stay a beat, as their faces lift off them.
	for i, au := range a.authors {
		if !slices.ContainsFunc(out, func(x *author) bool { return x.src.ID == au.src.ID }) && a.booted {
			a.parting[au.src.ID] = &partingRow{au: au, index: i}
		}
	}
	a.authors = out
}

func memberships(art *library.Article) []string {
	if len(art.SourceIDs) > 0 {
		return art.SourceIDs
	}
	return []string{art.SourceID}
}

// articleTime is Date.parse(publishedAt || modifiedAt).
func articleTime(art *library.Article) (int64, bool) {
	v := art.PublishedAt
	if v == nil || *v == "" {
		v = art.ModifiedAt
	}
	if v == nil {
		return 0, false
	}
	return web.JSDateParse(*v)
}

// byDate orders articles newest first, undated last, then by title.
func byDate(x, y *library.Article) int {
	tx, okx := articleTime(x)
	ty, oky := articleTime(y)
	switch {
	case okx && !oky:
		return -1
	case oky && !okx:
		return 1
	case okx && tx != ty:
		if tx > ty {
			return -1
		}
		return 1
	}
	return strings.Compare(x.Title, y.Title)
}

// writer is the author of page ID id: a subscribed one, or someone Explore
// has who is not subscribed to.
func (a *App) writer(id string) *author {
	if p := a.exPersonOf(id); p != nil {
		return p.au
	}
	for _, au := range a.authors {
		if au.src.ID == id {
			return au
		}
	}
	return nil
}

func (a *App) source(id string) *library.Source {
	if p := a.exPersonOf(id); p != nil {
		return p.au.src
	}
	for i := range a.lib.Sources {
		if a.lib.Sources[i].ID == id {
			return &a.lib.Sources[i]
		}
	}
	return nil
}

// article is article id: the library's, else the Explore post standing for
// it until the library has it.
func (a *App) article(id string) *library.Article {
	if art := a.libArticle(id); art != nil {
		return art
	}
	if t := a.trials[id]; t != nil {
		return &t.art
	}
	return nil
}

// sourceOf is the source an article shows under: the author page's it was
// opened from, else its first subscribed one.
func (a *App) sourceOf(art *library.Article) *library.Source {
	if t := a.trials[art.ID]; t != nil && a.libArticle(art.ID) == nil {
		return &t.src
	}
	ids := memberships(art)
	for _, p := range append(a.from[:len(a.from):len(a.from)], a.page) {
		if p.kind == "author" && slices.Contains(ids, p.id) {
			return a.source(p.id)
		}
	}
	for _, id := range ids {
		if a.writer(id) != nil {
			return a.source(id)
		}
	}
	return a.source(art.SourceID)
}

func nameOf(s *library.Source) string {
	if s == nil {
		return locales.CommonUnknownSource()
	}
	return s.DisplayName
}

// magnitude is how bright an author's name is:
// the more recently they wrote, the brighter.
func magnitude(days int, fresh bool) int {
	switch {
	case fresh || days >= 0 && days <= 7:
		return 1
	case days >= 0 && days <= 31:
		return 2
	case days >= 0 && days <= 92:
		return 3
	}
	return 4
}

// Reading progress: only what was started and not finished
// shows the moon.
func progressOf(art *library.Article) float64 {
	if art.ReadingPosition == nil {
		return 0
	}
	return art.ReadingPosition.Progress
}

func inProgress(art *library.Article) bool { p := progressOf(art); return p >= 0.02 && p < 0.95 }

// litOf is what is left to read: a sliver stays until the last few percent.
func litOf(art *library.Article) float32 {
	p := progressOf(art)
	if p >= 0.95 {
		return 0
	}
	return float32(max(0.06, 1-p))
}

func percentOf(art *library.Article) int { return share(progressOf(art)) }

// share is a progress as the whole percent read, 99 at most: 100 only says the end, which the pages say in words.
func share(p float64) int { return min(99, int(math.Round(p*100))) }

// dateOnly tells a date written alone, 2026-11-04.
var dateOnly = func(s string) bool {
	return len(s) == 10 && s[4] == '-' && s[7] == '-'
}

func initial(s string) string {
	s = strings.TrimSpace(s)
	r, _ := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return "?"
	}
	return strings.ToUpper(string(r))
}

// siteIcon is a source's icon as the dark interface shows it:
// toned once, with a dimmed copy for the sidebar's rows at rest.
type siteIcon struct {
	raw   *ui.Bitmap // as drawn: the reader's byline puts it on a light disc
	toned *ui.Bitmap
	dim   *ui.Bitmap // saturate(0.55) brightness(0.92)
	inset bool       // ink and clear marks keep 12% around them
}

// bundled are the icons of the seeded sources.
var bundled = map[string]string{"innei": "site-innei.png", "antfu": "site-antfu.svg", "velas": "site-velas.png",
	"innei.in": "site-innei.png", "antfu.me": "site-antfu.svg", "velasx.com": "site-velas.png"}

// iconOf is the source's icon, or nil for the initial instead.
func (a *App) iconOf(s *library.Source) *siteIcon {
	if s == nil {
		return nil
	}
	if ic, ok := a.icons[s.ID]; ok {
		return ic
	}
	var img image.Image
	if s.Icon != nil && strings.HasPrefix(*s.Icon, "data:image/") {
		if _, data, ok := strings.Cut(*s.Icon, ";base64,"); ok {
			if b, err := base64.StdEncoding.DecodeString(data); err == nil {
				img = decodeAny(b)
			}
		}
	} else if f := bundled[s.ID]; f != "" || bundled[s.Host] != "" {
		if f == "" {
			f = bundled[s.Host]
		}
		if b, err := artFiles.ReadFile("art/" + f); err == nil {
			img = decodeAny(b)
		}
	}
	var ic *siteIcon
	if img != nil {
		ic = toneIcon(img)
	}
	a.icons[s.ID] = ic
	return ic
}

// decodeAny decodes a PNG, JPEG, GIF, WebP, ICO or SVG picture; a lossy
// WebP in the colours a browser gives it (reader.WebP).
func decodeAny(b []byte) image.Image {
	if img := reader.WebP(b); img != nil {
		return img
	}
	if img, _, err := image.Decode(bytes.NewReader(b)); err == nil {
		return img
	}
	if img := decodeICO(b); img != nil {
		return img
	}
	if svg, err := ui.ParseSVG(b); err == nil {
		w, h := svg.Size()
		if w > 0 && h > 0 {
			return svg.Image(96, int(96*h/w), color.Black)
		}
	}
	return nil
}

// decodeICO is the largest picture of a favicon.ico: one stored as PNG, or
// as a 32-bit bitmap with its alpha; nil for older bitmaps.
func decodeICO(b []byte) image.Image {
	in := b
	le16 := func(i int) int { return int(in[i]) | int(in[i+1])<<8 }
	le32 := func(i int) int { return le16(i) | le16(i+2)<<16 }
	if len(b) < 6 || le16(0) != 0 || le16(2) != 1 {
		return nil
	}
	n, best, size := le16(4), -1, -1
	for i := range n {
		at := 6 + 16*i
		if at+16 > len(b) {
			return nil
		}
		w := int(b[at])
		if w == 0 {
			w = 256
		}
		if w > size {
			best, size = at, w
		}
	}
	if best < 0 {
		return nil
	}
	length, offset := le32(best+8), le32(best+12)
	if offset < 0 || length <= 0 || offset+length > len(b) {
		return nil
	}
	data := b[offset : offset+length]
	if bytes.HasPrefix(data, []byte("\x89PNG")) {
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil
		}
		return img
	}
	// A BITMAPINFOHEADER, its rows bottom up, blue, green, red and alpha; its
	// height counts the AND mask after it too.
	in = data
	if len(data) < 40 || le32(0) != 40 || le16(14) != 32 {
		return nil
	}
	w, h := le32(4), le32(8)/2
	if w <= 0 || h <= 0 || w > 512 || h > 512 || 40+4*w*h > len(data) {
		return nil
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		row := data[40+4*w*(h-1-y):]
		for x := range w {
			p := row[4*x:]
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = p[2], p[1], p[0], p[3]
		}
	}
	return img
}

// toneIcon reads the icon at 32×32 and tones it.
func toneIcon(img image.Image) *siteIcon {
	const size = 32
	small := image.NewNRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(small, small.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	tone := explore.IconTone(small.Pix, size)
	src := image.NewNRGBA(img.Bounds())
	xdraw.Draw(src, src.Bounds(), img, img.Bounds().Min, xdraw.Src)
	toned := image.NewNRGBA(src.Bounds())
	dim := image.NewNRGBA(src.Bounds())
	for i := 0; i < len(src.Pix); i += 4 {
		r, g, b := float64(src.Pix[i])/255, float64(src.Pix[i+1])/255, float64(src.Pix[i+2])/255
		switch tone {
		case "ink":
			r, g, b = invert9(r), invert9(g), invert9(b)
		case "paper":
			r, g, b = invert9(r), invert9(g), invert9(b)
			r, g, b = -0.574*r+1.43*g+0.144*b, 0.426*r+0.43*g+0.144*b, 0.426*r+1.43*g-0.856*b
		}
		r, g, b = clamp01(r), clamp01(g), clamp01(b)
		toned.Pix[i], toned.Pix[i+1], toned.Pix[i+2], toned.Pix[i+3] = byte(r*255+0.5), byte(g*255+0.5), byte(b*255+0.5), src.Pix[i+3]
		// saturate(0.55) brightness(0.92), CSS's matrices.
		s := 0.55
		dr := (0.213+0.787*s)*r + (0.715-0.715*s)*g + (0.072-0.072*s)*b
		dg := (0.213-0.213*s)*r + (0.715+0.285*s)*g + (0.072-0.072*s)*b
		db := (0.213-0.213*s)*r + (0.715-0.715*s)*g + (0.072+0.928*s)*b
		dim.Pix[i], dim.Pix[i+1], dim.Pix[i+2], dim.Pix[i+3] = byte(clamp01(dr*0.92)*255+0.5), byte(clamp01(dg*0.92)*255+0.5), byte(clamp01(db*0.92)*255+0.5), src.Pix[i+3]
	}
	return &siteIcon{raw: ui.NewBitmap(src), toned: ui.NewBitmap(toned), dim: ui.NewBitmap(dim), inset: tone == "ink" || tone == "clear"}
}

func invert9(v float64) float64 { return 0.9 - 0.8*v }
func clamp01(v float64) float64 { return max(0, min(1, v)) }
