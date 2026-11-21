package reader

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// whiteWebP is a 16×16 white lossy WebP.
const whiteWebP = "UklGRiYAAABXRUJQVlA4IBoAAAAwAQCdASoQABAAAMASJaQAA3AA/v7deAAAAA=="

// TestWebPWhite: a lossy WebP keeps its colours in studio range, which
// image.Decode's colour model takes for full range (white as 235).
func TestWebPWhite(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(whiteWebP)
	if err != nil {
		t.Fatal(err)
	}
	if img, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	} else if r, _, _, _ := img.At(8, 8).RGBA(); r>>8 > 240 {
		t.Fatalf("image.Decode now gives white as %d: reader.WebP may no longer be needed", r>>8)
	}
	img := WebP(data)
	if img == nil {
		t.Fatal("WebP gave nothing for a lossy WebP")
	}
	if r, g, b, _ := img.At(8, 8).RGBA(); r>>8 < 254 || g>>8 < 254 || b>>8 < 254 {
		t.Fatalf("white comes out as %d,%d,%d", r>>8, g>>8, b>>8)
	}
}

// pngOf is a w×h PNG data URI whose pixel at (x, y) is opaque where solid says, see-through elsewhere.
func pngOf(t *testing.T, w, h int, solid func(x, y int) bool) string {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			if solid(x, y) {
				m.Set(x, y, color.NRGBA{R: 200, G: 40, B: 40, A: 255})
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

// A picture whose edges are see-through (a badge on no ground, a chart) shows without a corner and an edge; a photo,
// and a screenshot whose rounded corners alone are see-through, keep both.
func TestSeeThroughEdgesGoUnframed(t *testing.T) {
	const w, h = 400, 300
	badge := pngOf(t, w, h, func(x, y int) bool { dx, dy := x-w/2, y-h/2; return dx*dx+dy*dy < 120*120 })
	photo := pngOf(t, w, h, func(int, int) bool { return true })
	rounded := pngOf(t, w, h, func(x, y int) bool {
		cx, cy := min(max(x, 20), w-21), min(max(y, 20), h-21) // the nearest point of the inner rectangle
		return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= 20*20
	})
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	photoJPEG := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpg.Bytes())
	body := Prepare(&Content{Status: "ready", HTML: `<p><img src="` + badge + `"></p><p><img src="` + photo + `"></p><p><img src="` + rounded + `"></p><p><img src="` + photoJPEG + `"></p>`})
	if len(body.pics) != 4 {
		t.Fatalf("%d pictures", len(body.pics))
	}
	for i, want := range []float32{0, 10, 10, 10} {
		p := body.pics[i]
		if p.err != nil {
			t.Fatalf("picture %d: %v", i+1, p.err)
		}
		if got := pictureRadius(p, 640); got != want {
			t.Errorf("picture %d (see-through edges %v): corner %v, want %v", i+1, p.clear, got, want)
		}
	}
}
