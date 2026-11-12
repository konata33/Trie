package reader

import (
	"bytes"
	"encoding/base64"
	"image"
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
