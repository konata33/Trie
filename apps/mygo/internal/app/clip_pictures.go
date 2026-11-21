package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/reader"
	"trie/internal/theme"
)

// A passage that ran across pictures keeps them: the clip names each by its
// unit in the article's stored body and where it stands among the words.
// The card reads them from the body the first time they are wanted.

// cardTall is the most of the window's height a picture on the card takes,
// so that one is seen whole with the words around it.
const cardTall = 0.6

// passagePart is a paragraph of a passage, or one of its pictures (unit).
type passagePart struct{ words, unit string }

// passageParts are a passage's paragraphs with its pictures among them, in
// order.
func passageParts(text string, pics []library.ClipPicture) []passagePart {
	var out []passagePart
	add := func(s string) {
		for _, l := range paragraphBreaks.Split(s, -1) {
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, passagePart{words: l})
			}
		}
	}
	runes, from := []rune(text), 0
	for _, p := range pics {
		at := min(max(p.At, from), len(runes))
		add(string(runes[from:at]))
		out = append(out, passagePart{unit: p.Unit})
		from = at
	}
	add(string(runes[from:]))
	return out
}

// clipPicsKept is how many passages' pictures stay decoded.
const clipPicsKept = 16

// clipPictures are the pictures in a passage, by unit: read from its article's
// stored body off the window's thread the first time they are wanted, nil
// until then. then, if given, gets them once they are there.
func (a *App) clipPictures(cl *library.Clip, then func(map[string]reader.KeptPicture)) map[string]reader.KeptPicture {
	cs := a.clipsSt()
	got, asked := cs.pics[cl.ID]
	switch {
	case len(cl.Pictures) == 0 || a.opt.Store == nil:
		if then != nil {
			then(nil)
		}
		return nil
	case got != nil:
		if then != nil {
			then(got)
		}
		return got
	}
	if then != nil {
		cs.waiting[cl.ID] = append(cs.waiting[cl.ID], then)
	}
	if asked {
		return nil // on its way
	}
	// ponytail: past clipPicsKept passages, all the others' pictures go at once; keep the latest few if flipping
	// back and forth shows them coming in again.
	if len(cs.pics) >= clipPicsKept {
		for k, v := range cs.pics {
			if v != nil {
				delete(cs.pics, k)
			}
		}
	}
	cs.pics[cl.ID] = nil
	store, id, key := a.opt.Store, cl.ArticleID, cl.ID
	units := make([]string, len(cl.Pictures))
	for i, p := range cl.Pictures {
		units[i] = p.Unit
	}
	a.do(func() func() {
		got := map[string]reader.KeptPicture{}
		if content, err := store.Content(id); err == nil && content != nil {
			if data, err := json.Marshal(content); err == nil {
				if rc, err := reader.FromJSON(data); err == nil {
					got = reader.ClipPictures(rc, units)
				}
			}
		}
		return func() {
			cs.pics[key] = got
			for _, f := range cs.waiting[key] {
				f(got)
			}
			delete(cs.waiting, key)
		}
	})
	return nil
}

// cardPicture is a picture of the passage on the card, as the reader sets
// one: its own width up to the column's, left; one that would stand taller
// than cardTall of the window narrowed to it, in the middle; 10px corners
// and the soft line inside them from 160px wide, square and bare when
// smaller or on no ground of its own (a chart, a logo). A GIF plays.
func cardPicture(c *ui.Context, key string, p reader.KeptPicture, column, tall float32) ui.Element {
	w := min(p.W, column)
	h := w * p.H / p.W
	narrowed := h > tall
	if narrowed {
		w, h = w*tall/h, tall
	}
	var e ui.Element
	if p.Player != nil {
		e = ui.Video(c.Key(key), p.Player)
	} else {
		e = ui.Image(c.Key(key), p.Img)
	}
	e.Size(w, h).Shrink(0).AlignSelf(ui.Start)
	if narrowed {
		e.AlignSelf(ui.Center)
	}
	if p.Alt != "" {
		e.Label(p.Alt)
	}
	edge := w >= 160 && !p.Clear
	if edge {
		e.Radius(10)
	}
	e.DrawOver(func(pt *ui.Painter, box ui.Rect) {
		p.DrawTexts(pt, box)
		if edge {
			pt.Stroke(ui.Rect{X: box.X + .5, Y: box.Y + .5, W: box.W - 1, H: box.H - 1}, theme.LineSoft, 9.5, 1)
		}
	})
	return e
}

// copyClip puts a passage on the clipboard as Markdown and, with pictures,
// as HTML too: the pictures are saved as files in the passage's folder
// (clipFolder), which the Markdown links to where they stood, so that an
// agent can open them; the HTML holds them, for a note to paste whole.
// done hears how it went.
func (a *App) copyClip(c *ui.Context, cl *library.Clip, done func(error)) {
	if len(cl.Pictures) == 0 {
		c.WriteClipboardThen(clipMarkdown(cl, nil), done)
		return
	}
	svc, dir, order := c.Services(), a.clipFolder(cl.ID), cl.Pictures
	a.clipPictures(cl, func(pics map[string]reader.KeptPicture) {
		a.do(func() func() {
			saved := savePictures(dir, order, pics)
			return func() {
				md := clipMarkdown(cl, saved)
				svc.WriteClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.Text, []byte(md)),
					transfer.Bytes(transfer.Markdown, []byte(md)), transfer.Bytes(transfer.HTML, []byte(clipHTML(cl, saved))))))
				// The write tells nothing of how it went; the words read back do.
				if got, err := svc.ReadClipboardData(transfer.Text).Read(transfer.Text); err != nil || string(got) != md {
					done(errors.New(locales.ClipsCopyFailed()))
					return
				}
				done(nil)
			}
		})
	})
}

// pasted is a picture as a copy carries it: the file it was saved to (""
// when it could not be), its data: URL, its words and its size.
type pasted struct {
	path, url, alt string
	w, h           int
}

// clipFolder is the folder a passage's pictures are saved in for a copy to
// link to: under clips in the library's folder, else the system's temporary
// one.
func (a *App) clipFolder(id string) string {
	root := filepath.Join(os.TempDir(), "trie-clips")
	if a.opt.DataDir != "" {
		root = filepath.Join(a.opt.DataDir, "clips")
	}
	return filepath.Join(root, id)
}

// savePictures writes a passage's pictures to dir as 1.png, 2.jpg and on,
// in their order in the passage, and gives each as a copy carries it. Off
// the window's thread. ponytail: the folder stays when its passage is
// deleted (撤销 brings the passage back); remove it with the passage if the
// clips folder grows.
func savePictures(dir string, order []library.ClipPicture, pics map[string]reader.KeptPicture) map[string]pasted {
	out := map[string]pasted{}
	for i, cp := range order {
		p, ok := pics[cp.Unit]
		if !ok {
			continue
		}
		data, mime, url := pictureBytes(p)
		if data == nil {
			continue
		}
		alt := strings.Join(strings.Fields(p.Alt), " ")
		if alt == "" {
			alt = strings.Join(strings.Fields(p.Caption), " ")
		}
		s := pasted{url: url, alt: alt, w: int(p.W), h: int(p.H)}
		file := filepath.Join(dir, strconv.Itoa(i+1)+"."+pictureExt(mime))
		if os.MkdirAll(dir, 0o755) == nil && os.WriteFile(file, data, 0o644) == nil {
			s.path = file
		}
		out[cp.Unit] = s
	}
	return out
}

var dataURL = regexp.MustCompile(`^data:(image/[a-zA-Z0-9.+-]+);base64,(.+)$`)

// pictureBytes is a picture as it is saved and pasted: its bytes, their
// type and their data: URL. An SVG is drawn as a PNG at twice its size, its
// longer side at most pasteSide, as agents and notes open few SVGs; one with
// words of its own stays an SVG, which a raster would leave them out of.
func pictureBytes(p reader.KeptPicture) (data []byte, mime, url string) {
	m := dataURL.FindStringSubmatch(p.Src)
	if m == nil {
		return nil, "", ""
	}
	if svg, ok := p.Img.(*ui.SVG); ok && !p.HasTexts() && p.W > 0 && p.H > 0 {
		k := min(2, pasteSide/max(p.W, p.H))
		var b bytes.Buffer
		if png.Encode(&b, svg.Image(max(1, int(k*p.W)), max(1, int(k*p.H)), nil)) == nil {
			return b.Bytes(), "image/png", "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
		}
	}
	data, err := base64.StdEncoding.DecodeString(m[2])
	if err != nil {
		return nil, "", ""
	}
	return data, strings.ToLower(m[1]), p.Src
}

// pictureExt is the file extension of a picture's type.
func pictureExt(mime string) string {
	switch sub := strings.TrimPrefix(mime, "image/"); sub {
	case "jpeg":
		return "jpg"
	case "svg+xml":
		return "svg"
	default:
		return sub
	}
}

// pasteSide is the longest side, in pixels, of an SVG copied as a PNG: the
// clip card's column at twice the points.
const pasteSide = 1200

// mdImage is a picture in the Markdown a passage is copied as: a link to
// its file, its alt text escaped.
func mdImage(s pasted) string {
	return "![" + strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`).Replace(s.alt) + "](<" + s.path + ">)"
}

// clipHTML is clipMarkdown as HTML, the pictures in it.
func clipHTML(cl *library.Clip, saved map[string]pasted) string {
	var b strings.Builder
	b.WriteString("<blockquote>")
	for _, part := range passageParts(cl.Text, cl.Pictures) {
		if part.unit == "" {
			b.WriteString("<p>" + html.EscapeString(part.words) + "</p>")
			continue
		}
		if s, ok := saved[part.unit]; ok {
			b.WriteString(`<p><img src="` + html.EscapeString(s.url) + `" alt="` + html.EscapeString(s.alt) + `"`)
			if s.w > 0 && s.h > 0 {
				b.WriteString(` width="` + strconv.Itoa(s.w) + `" height="` + strconv.Itoa(s.h) + `"`)
			}
			b.WriteString("></p>")
		}
	}
	b.WriteString("</blockquote>")
	// The source, link and note as the Markdown has them, after its quote.
	md := clipMarkdown(cl, saved)
	if i := strings.Index(md, "\n\n— "); i >= 0 {
		for _, l := range strings.Split(strings.TrimSpace(md[i:]), "\n") {
			if l = strings.TrimSpace(l); l == "" {
				continue
			}
			if l == cl.Citation.URL {
				b.WriteString(`<p><a href="` + html.EscapeString(l) + `">` + html.EscapeString(l) + "</a></p>")
				continue
			}
			b.WriteString("<p>" + html.EscapeString(l) + "</p>")
		}
	}
	return b.String()
}
