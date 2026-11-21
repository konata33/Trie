package app

import (
	"image"
	"image/color"
	"image/draw"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
	xdraw "golang.org/x/image/draw"

	"trie/internal/reader"
	"trie/internal/theme"
)

// The proof (DESIGN.md Capture 显影): an article as the reader sets it, which
// its thumbnail and card develop from until the page's first screen comes. Its
// title, byline, picture and first blocks are set in bars of ink a word long,
// on the reader's dark ground, in the reader's measure. It is set once for each
// size the picture shows at, on a sheet whose every 12 px is one of that
// size's dots, so that each line of text falls on a row of them.
const proofDot = 12

// proofMeasure is how the proof is set at one size: in rows of dots, where the
// title starts, how high its bars stand and how far apart its lines are; the
// gaps under the title and under the byline; the body's lines apart and the
// more between blocks; the tallest a picture stands.
type proofMeasure struct {
	titleTop, titleBar, titleLine int
	underTitle, underByline       int
	line, between, picture        int
}

var (
	// The card's: the reader's own rhythm, the headline's bars two rows high.
	cardProof = proofMeasure{titleTop: 7, titleBar: 2, titleLine: 4, underTitle: 2, underByline: 4, line: 3, between: 1, picture: 24}
	// The thumbnail's: a row a line, every other row.
	thumbProof = proofMeasure{titleTop: 3, titleBar: 1, titleLine: 2, underTitle: 1, underByline: 2, line: 2, between: 1, picture: 10}
)

// The proof's inks: the title as the reader's text; the body dimmer, as a
// page of small type reads from afar; code in the code block's own ink.
var (
	proofTitle  = theme.Text
	proofBody   = theme.ProofBody
	proofQuiet  = theme.Faint
	proofRule   = theme.ProofRule
	proofCode   = theme.ProofCode
	proofCodeBg = theme.ProofCodeBg
	proofPicBg  = theme.ProofPicBg
)

// proofPage is what the proof of an article is set from.
type proofPage struct {
	title, source, date string
	mark                ui.Color // the site's icon, as one colour; zero for none
	blocks              []reader.Sketch
	picture             image.Image // the article's picture, standing under the byline; nil for none
}

// proofPic is p's proof at the thumbnail's dots and at the card's.
func proofPic(p proofPage) *dotPic {
	at := func(cols, rows int, m proofMeasure) *dotGrid {
		img := proofOf(p, cols, rows, m)
		return sampleGrid(img, img.Bounds(), cols, rows)
	}
	return &dotPic{thumb: at(thumbCols, thumbRows, thumbProof), card: at(cardCols, cardRows, cardProof)}
}

// proofOf sets p's proof for cols × rows dots as m measures it. Across, it is
// the reader's area, 1056 px for the card's 88 dots, the column 648 of them in
// the middle.
func proofOf(p proofPage, cols, rows int, m proofMeasure) *image.RGBA {
	w, h := cols*proofDot, rows*proofDot
	s := float32(w) / 1056
	ps := &proofSheet{img: image.NewRGBA(image.Rect(0, 0, w, h)), s: s, left: int(204 * s), right: int(852 * s)}
	ps.fill(0, 0, w, h, theme.Window)
	row := func(n int) int { return n * proofDot }
	// The headline, 32 px.
	y := ps.runs([]reader.SketchRun{{Text: p.title}}, ps.left, row(m.titleTop), row(m.titleBar), row(m.titleLine), 34, proofTitle)
	// The byline: the site's mark, its name, the date.
	y += row(m.underTitle)
	if p.mark.A > 0 {
		ps.dot(ps.left+proofDot/2, y+proofDot/2, proofDot/2, p.mark)
	}
	x := ps.left + 2*proofDot
	for i, t := range []string{p.source, p.date} {
		if t == "" {
			continue
		}
		if i > 0 {
			x += int(10 * s)
		}
		tw := ps.width(t, 13)
		ps.fill(x, y, x+tw, y+proofDot, proofQuiet)
		x += tw + int(10*s)
	}
	y += proofDot + row(m.underByline)
	if p.picture != nil {
		y = ps.picture(p.picture, y, row(m.picture)) + row(m.line)
	}
	// The body, 20 px (17 for Chinese and Japanese); each block's top is a line
	// and m.between under the last bar before it.
	em := float32(20)
	if cjkText(p.title, p.blocks) {
		em = 17
	}
	next := row(m.line-1) + row(m.between)
	for _, b := range p.blocks {
		if y >= h {
			break
		}
		indent := ps.left + int(24*s)*b.Depth
		switch b.Kind {
		case reader.Heading:
			y += row(m.between)
			y = ps.runs(b.Runs, ps.left, y, proofDot, row(m.line), em*1.2, proofTitle) + row(m.line-1)
		case reader.Quote:
			end := ps.runs(b.Runs, indent, y, proofDot, row(m.line), em, proofBody.Mix(theme.Window, 0.25))
			ps.fill(indent-int(20*s), y, indent-int(20*s)+max(1, int(3*s)), end, proofRule)
			y = end + next
		case reader.Item:
			ps.dot(indent-int(12*s), y+proofDot/2, max(1, int(3*s)), proofBody)
			y = ps.runs(b.Runs, indent, y, proofDot, row(m.line), em, proofBody) + row(m.line-1)
		case reader.Code:
			y = ps.code(b, y, m) + next
		case reader.Picture, reader.Table:
			ps.fill(ps.left, y, ps.right, min(h, y+row(m.picture/2)), proofPicBg)
			y += row(m.picture/2) + next
		case reader.Rule:
			mid := (ps.left + ps.right) / 2
			ps.fill(mid-int(32*s), y+proofDot/2-1, mid+int(32*s), y+proofDot/2+1, proofQuiet)
			y += row(m.line) + next
		case reader.Caption:
			y = ps.runs(b.Runs, ps.left, y, proofDot, row(m.line), 13, proofQuiet) + next
		default:
			y = ps.runs(b.Runs, indent, y, proofDot, row(m.line), em, proofBody) + next
		}
	}
	return ps.img
}

// proofSheet is a proof being set: its scale across from the reader's px,
// and its column.
type proofSheet struct {
	img         *image.RGBA
	s           float32
	left, right int
}

func (ps *proofSheet) fill(x0, y0, x1, y1 int, c ui.Color) {
	draw.Draw(ps.img, image.Rect(x0, y0, x1, y1), image.NewUniform(color.RGBA{c.R, c.G, c.B, 255}), image.Point{}, draw.Src)
}

// dot is a round dot of radius r at (cx, cy).
func (ps *proofSheet) dot(cx, cy, r int, c ui.Color) {
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			if x*x+y*y <= r*r {
				ps.img.Set(cx+x, cy+y, color.RGBA{c.R, c.G, c.B, 255})
			}
		}
	}
}

// width is how wide t sets at em reader px, on the sheet.
func (ps *proofSheet) width(t string, em float32) int {
	w := float32(0)
	for _, r := range t {
		w += advance(r, em)
	}
	return int(w*ps.s + 0.5)
}

// advance is about how wide r sets at em px: a Chinese, Japanese or Korean
// character a whole em, a Latin letter by its kind.
func advance(r rune, em float32) float32 {
	switch {
	case isWide(r):
		return em
	case r == ' ':
		return 0.26 * em
	case strings.ContainsRune("iljtfrI.,:;'’!|()[]", r):
		return 0.3 * em
	case strings.ContainsRune("mwMW@%", r):
		return 0.86 * em
	case unicode.IsUpper(r):
		return 0.66 * em
	case unicode.IsDigit(r):
		return 0.56 * em
	}
	return 0.5 * em
}

func isWide(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) ||
		r >= 0x3000 && r <= 0x303F || r >= 0xFF00 && r <= 0xFFEF
}

// cjkText tells whether an article's words are mostly Chinese, Japanese or
// Korean, which the reader sets at 17 px.
func cjkText(title string, blocks []reader.Sketch) bool {
	wide, narrow := 0, 0
	count := func(t string) {
		for _, r := range t {
			switch {
			case isWide(r):
				wide++
			case unicode.IsLetter(r):
				narrow++
			}
		}
	}
	count(title)
	for _, b := range blocks {
		for _, r := range b.Runs {
			count(r.Text)
		}
	}
	return wide > narrow/3
}

// runs sets runs from left to the column's right edge in bars bar high, a line
// every pitch from top, at em reader px, a word a bar, a link's words warmed
// by the amber under them. Chinese and Japanese run on as one bar, broken
// where their punctuation stops them. Lines break between words, as the
// reader's do. It returns the foot of the last line's bar.
func (ps *proofSheet) runs(runs []reader.SketchRun, left, top, bar, pitch int, em float32, ink ui.Color) int {
	x, y := float32(left), top
	space := advance(' ', em) * ps.s
	for _, rn := range runs {
		c := ink
		if rn.Link {
			c = ink.Mix(theme.Accent, 0.55)
		}
		for _, word := range splitWords(rn.Text) {
			if word == " " {
				x += space
				continue
			}
			w := float32(ps.width(word, em))
			if x > float32(left) && x+w > float32(ps.right) {
				x, y = float32(left), y+pitch
				if y >= ps.img.Rect.Dy() {
					return y
				}
			}
			if !strings.ContainsAny(word, "，。、；：！？") {
				ps.fill(int(x), y, int(x+w-0.5*ps.s), y+bar, c)
			}
			x += w
		}
	}
	return y + bar
}

// splitWords is t as words, spaces and each Chinese or Japanese character apart.
func splitWords(t string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range t {
		switch {
		case unicode.IsSpace(r):
			flush()
			out = append(out, " ")
		case isWide(r):
			flush()
			out = append(out, string(r))
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}

// picture sets pic across the column from top, as the reader shows a picture,
// no taller than most and cut about its middle to fill; it returns its foot.
func (ps *proofSheet) picture(pic image.Image, top, most int) int {
	b := pic.Bounds()
	if b.Empty() {
		return top
	}
	w := ps.right - ps.left
	h := min(most, w*b.Dy()/b.Dx())
	h -= h % proofDot
	if h <= 0 {
		return top
	}
	src := b
	if b.Dx()*h > w*b.Dy() { // wider than the box
		cw := b.Dy() * w / h
		src = image.Rect(b.Min.X+(b.Dx()-cw)/2, b.Min.Y, b.Min.X+(b.Dx()+cw)/2, b.Max.Y)
	} else {
		ch := b.Dx() * h / w
		src = image.Rect(b.Min.X, b.Min.Y+(b.Dy()-ch)/2, b.Max.X, b.Min.Y+(b.Dy()+ch)/2)
	}
	xdraw.CatmullRom.Scale(ps.img, image.Rect(ps.left, top, ps.right, top+h), pic, src, xdraw.Src, nil)
	return top + h
}

// code sets a code block: its ground, and its first lines a bar each, a line
// apart, a row in from its edges. It returns its foot.
func (ps *proofSheet) code(b reader.Sketch, top int, m proofMeasure) int {
	var lines []string
	if len(b.Runs) > 0 {
		lines = strings.Split(strings.TrimRight(b.Runs[0].Text, "\n"), "\n")
	}
	lines = lines[:min(len(lines), 8)]
	pitch := 2 * proofDot
	h := len(lines)*pitch + proofDot
	ps.fill(ps.left, top, ps.right, min(ps.img.Rect.Dy(), top+h), proofCodeBg)
	y := top + proofDot
	for _, l := range lines {
		indent := len(l) - len(strings.TrimLeft(l, " \t"))
		x := ps.left + int(float32(24+8*indent)*ps.s)
		if w := ps.width(strings.TrimSpace(l), 15); w > 0 {
			ps.fill(x, y, min(ps.right-int(24*ps.s), x+w), y+proofDot, proofCode)
		}
		y += pitch
	}
	return top + h
}
