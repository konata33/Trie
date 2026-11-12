package reader

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"image"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/webp"
)

// picture is an img of the body.
type picture struct {
	id       int // its place among the body's pictures, from 1
	src, alt string
	caption  string // its figure's caption
	// w and h are the size it shows at, in DIPs, unless the column is
	// narrower: the article's width and height, else the picture's own.
	w, h   float32
	img    ui.ImageSource
	player *ui.VideoPlayer // a GIF, which plays
	texts  []svgText       // an SVG's text, which MyGo's SVGs leave out
	vb     [4]float32      // the SVG's viewBox, which texts are placed in
	err    error           // why it shows as a picture that did not load
}

//go:embed picture.svg
var pictureSVG []byte

// missingArt is DESIGN.md's picture pictogram, its
// first frame: a picture that did not load shows it over 重试.
var missingArt = ui.MustParseSVG(pictureSVG)

// dataImage is what readerImage accepts.
var dataImage = regexp.MustCompile(`^(?i)data:image/(png|jpeg|gif|webp|avif|svg\+xml);base64,([a-z\d+/=]+)$`)

var errRemote = errors.New("the picture was not saved with the article")

func (b *builder) picture(n *node, unit string) *block {
	p := &picture{src: n.prop("src"), alt: n.prop("alt"), w: float32(n.num("width")), h: float32(n.num("height"))}
	if u := n.prop("dataReaderUnit"); u != "" {
		unit = u
	}
	b.pics = append(b.pics, p)
	p.id = len(b.pics)
	return &block{kind: Picture, unit: unit, pic: p}
}

// decodePictures decodes the pictures on as many goroutines as the machine
// has cores.
func decodePictures(pics []*picture) {
	var wg sync.WaitGroup
	slots := make(chan struct{}, runtime.NumCPU())
	for _, p := range pics {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wg.Done() }()
			p.err = p.decode()
		}()
	}
	wg.Wait()
}

// decode decodes a picture saved in the body as a data: URI. A remote one
// shows as not loaded: fetching it is the main process's (not this spike's).
func (p *picture) decode() error {
	m := dataImage.FindStringSubmatch(p.src)
	if m == nil {
		return errRemote
	}
	data, err := base64.StdEncoding.DecodeString(m[2])
	if err != nil {
		return err
	}
	format := strings.ToLower(m[1])
	var img ui.ImageSource
	if w := WebP(data); w != nil {
		img = ui.NewBitmap(w)
	} else if img, err = ui.DecodeImage(data); err != nil { // PNG, JPEG, GIF, BMP or SVG: not AVIF
		return err
	}
	p.img = img
	switch format {
	case "gif":
		if d, err := ui.NewGIFDecoder(data); err == nil {
			if v, err := ui.NewVideoPlayer(d); err == nil {
				p.player = v.SetLoop(true)
			}
		}
	case "svg+xml":
		p.texts, p.vb = svgTextsOf(data)
	}
	w, h := ui.ImageSize(img)
	switch {
	case w <= 0 || h <= 0:
	case p.w > 0 && p.h == 0:
		p.h = p.w * h / w
	case p.h > 0 && p.w == 0:
		p.w = p.h * w / h
	case p.w == 0:
		p.w, p.h = w, h
	}
	return nil
}

// WebP is the picture of data when it is a lossy WebP, in the colours
// libwebp, and so a browser, gives it; nil for anything else. A lossy WebP
// keeps BT.601 YCbCr in studio range (luma 16 to 235), which package
// x/image/webp hands over as an image.YCbCr, whose colour model takes it
// for JFIF's full range: white would show as 235 and black as 16. Its
// chroma is taken as Go takes it, the nearest sample for each pixel,
// where libwebp blends the nearest four.
func WebP(data []byte) image.Image {
	if len(data) < 16 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil
	}
	m, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	var y *image.YCbCr
	var alpha *image.NYCbCrA
	switch m := m.(type) {
	case *image.YCbCr:
		y = m
	case *image.NYCbCrA:
		y, alpha = &m.YCbCr, m
	default:
		return nil // lossless: RGB already
	}
	b := y.Rect
	out := image.NewNRGBA(b)
	for py := b.Min.Y; py < b.Max.Y; py++ {
		o := out.PixOffset(b.Min.X, py)
		for px := b.Min.X; px < b.Max.X; px++ {
			l, ci := mulHi(int(y.Y[y.YOffset(px, py)]), 19077), y.COffset(px, py)
			u, v := int(y.Cb[ci]), int(y.Cr[ci])
			// libwebp's VP8YUVToR, G and B (src/dsp/yuv.h).
			out.Pix[o] = clip8(l + mulHi(v, 26149) - 14234)
			out.Pix[o+1] = clip8(l - mulHi(u, 6419) - mulHi(v, 13320) + 8708)
			out.Pix[o+2] = clip8(l + mulHi(u, 33050) - 17685)
			out.Pix[o+3] = 255
			if alpha != nil {
				out.Pix[o+3] = alpha.A[alpha.AOffset(px, py)]
			}
			o += 4
		}
	}
	return out
}

func mulHi(v, c int) int { return v * c >> 8 }

// clip8 takes a colour with 6 bits of fraction to 0 to 255.
func clip8(v int) uint8 {
	switch {
	case v&^(256<<6-1) == 0:
		return uint8(v >> 6)
	case v < 0:
		return 0
	}
	return 255
}

// svgText is a line of an SVG's text, in the SVG's user units.
type svgText struct {
	text           string
	x, y           float32 // where its baseline starts, or its anchor
	size           float32
	color          ui.Color
	weight         int
	anchor, family string // text-anchor: start, middle or end
}

// svgFrame is what an SVG element passes on to the ones inside it.
type svgFrame struct {
	tx, ty, sx, sy float32 // its transform: translations and scales only
	size           float32
	color          ui.Color
	weight         int
	anchor, family string
	x, y           float32 // the text position, which tspans move on from
}

var svgTransform = regexp.MustCompile(`(\w+)\s*\(([^)]*)\)`)

// transform composes the translations and scales of a transform attribute
// after the frame's; rotations and skews are left out.
func (f *svgFrame) transform(tr string) {
	for _, part := range svgTransform.FindAllStringSubmatch(tr, -1) {
		v := numbers(part[2])
		var a, d, e, g float32 = 1, 1, 0, 0 // matrix(a b c d e f), with b = c = 0
		switch {
		case part[1] == "translate" && len(v) >= 1:
			e = v[0]
			if len(v) > 1 {
				g = v[1]
			}
		case part[1] == "scale" && len(v) >= 1:
			a, d = v[0], v[0]
			if len(v) > 1 {
				d = v[1]
			}
		case part[1] == "matrix" && len(v) == 6:
			a, d, e, g = v[0], v[3], v[4], v[5]
		}
		f.tx, f.ty = f.tx+e*f.sx, f.ty+g*f.sy
		f.sx, f.sy = f.sx*a, f.sy*d
	}
}

// svgTextsOf reads the text of an SVG, which MyGo draws without (docs/ui
// image.md): diagrams exported from Excalidraw, Mermaid or Figma label
// their boxes with it. It returns the SVG's viewBox too, which the text is
// placed in.
func svgTextsOf(data []byte) (out []svgText, vb [4]float32) {
	stack := []svgFrame{{sx: 1, sy: 1, size: 16, color: ui.Hex("#000000"), anchor: "start"}}
	inText := 0
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			f := stack[len(stack)-1]
			attrs := map[string]string{}
			for _, a := range t.Attr {
				attrs[a.Name.Local] = a.Value
			}
			for _, decl := range strings.Split(attrs["style"], ";") {
				if k, v, ok := strings.Cut(decl, ":"); ok {
					attrs[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
			if t.Name.Local == "svg" && len(stack) == 1 {
				if v := numbers(attrs["viewBox"]); len(v) == 4 {
					copy(vb[:], v)
				} else {
					vb = [4]float32{0, 0, num(attrs["width"]), num(attrs["height"])}
				}
			}
			if tr := attrs["transform"]; tr != "" {
				f.transform(tr)
			}
			if v, ok := attrs["font-size"]; ok {
				f.size = num(v)
			}
			if c, ok := svgColor(attrs["fill"]); ok {
				f.color = c
			}
			if v, ok := attrs["font-weight"]; ok {
				f.weight = map[string]int{"bold": 700, "bolder": 700, "normal": 400}[v]
				if n, err := strconv.Atoi(v); err == nil {
					f.weight = n
				}
			}
			if v, ok := attrs["text-anchor"]; ok {
				f.anchor = v
			}
			if v, ok := attrs["font-family"]; ok {
				f.family = v
			}
			if t.Name.Local == "text" || (t.Name.Local == "tspan" && inText > 0) {
				inText++
				if v, ok := attrs["x"]; ok {
					f.x = first(v)
				}
				if v, ok := attrs["y"]; ok {
					f.y = first(v)
				}
				f.x += first(attrs["dx"])
				f.y += first(attrs["dy"]) * emOf(attrs["dy"], f.size)
			}
			stack = append(stack, f)
		case xml.EndElement:
			if t.Name.Local == "text" || t.Name.Local == "tspan" {
				inText = max(0, inText-1)
			}
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			text := strings.Join(strings.Fields(string(t)), " ")
			if inText == 0 || text == "" {
				continue
			}
			f := stack[len(stack)-1]
			out = append(out, svgText{text: text, x: f.tx + f.x*f.sx, y: f.ty + f.y*f.sy,
				size: f.size * f.sy, color: f.color, weight: f.weight, anchor: f.anchor, family: f.family})
		}
	}
	if vb[2] <= 0 || vb[3] <= 0 {
		return nil, vb
	}
	return out, vb
}

func numbers(s string) []float32 {
	var out []float32
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		out = append(out, num(f))
	}
	return out
}

func num(s string) float32 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(s), "px"), "em"))
	v, _ := strconv.ParseFloat(s, 32)
	return float32(v)
}

func first(s string) float32 {
	if v := numbers(s); len(v) > 0 {
		return v[0]
	}
	return 0
}

// emOf turns a dy given in ems into user units.
func emOf(v string, size float32) float32 {
	if strings.HasSuffix(strings.TrimSpace(v), "em") {
		return size
	}
	return 1
}

func svgColor(v string) (ui.Color, bool) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" || v == "none" {
		return ui.Color{}, false
	}
	switch {
	case strings.HasPrefix(v, "#") && (len(v) == 4 || len(v) == 7):
		if len(v) == 4 {
			v = "#" + strings.Repeat(v[1:2], 2) + strings.Repeat(v[2:3], 2) + strings.Repeat(v[3:4], 2)
		}
		return ui.Hex(v), true
	case strings.HasPrefix(v, "rgb("):
		if n := numbers(strings.TrimSuffix(strings.TrimPrefix(v, "rgb("), ")")); len(n) == 3 {
			return ui.RGB(uint8(n[0]), uint8(n[1]), uint8(n[2])), true
		}
	case v == "black":
		return ui.Hex("#000000"), true
	case v == "white":
		return ui.Hex("#ffffff"), true
	}
	return ui.Color{}, false
}
