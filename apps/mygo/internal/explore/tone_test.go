package explore

import "testing"

// IconTone cases.

const toneSize = 16

type rgba [4]byte

// toneIcon is an icon of ground with a centred square mark of mark (its right half of right, when set).
func toneIcon(ground, mark rgba, right *rgba) []byte {
	px := make([]byte, toneSize*toneSize*4)
	for y := range toneSize {
		for x := range toneSize {
			c := ground
			if x >= 4 && x < 12 && y >= 4 && y < 12 {
				c = mark
				if right != nil && x >= 8 {
					c = *right
				}
			}
			copy(px[(y*toneSize+x)*4:], c[:])
		}
	}
	return px
}

var clearPx, whitePx = rgba{0, 0, 0, 0}, rgba{255, 255, 255, 255}

func tone(t *testing.T, px []byte, want string) {
	t.Helper()
	if got := IconTone(px, toneSize); got != want {
		t.Fatalf("tone %q, want %q", got, want)
	}
}

// a dark mark on transparency is ink
func TestToneInk(t *testing.T) { tone(t, toneIcon(clearPx, rgba{51, 51, 51, 255}, nil), "ink") }

// a coloured mark on transparency stays as drawn
func TestToneClear(t *testing.T) { tone(t, toneIcon(clearPx, rgba{220, 60, 40, 255}, nil), "clear") }

// a flat logo on a white square is paper
func TestTonePaper(t *testing.T) {
	tone(t, toneIcon(whitePx, rgba{0, 0, 0, 255}, nil), "paper")
	tone(t, toneIcon(whitePx, rgba{255, 90, 30, 255}, &rgba{20, 20, 20, 255}), "paper")
}

// a dark tile stays as drawn
func TestToneDarkTile(t *testing.T) { tone(t, toneIcon(rgba{10, 10, 10, 255}, whitePx, nil), "full") }

// a colourful picture on white is left alone
func TestToneColourfulPicture(t *testing.T) {
	px := toneIcon(whitePx, rgba{0, 0, 0, 255}, nil)
	// Eight saturated colours, eight pixels each, inside the white frame: a picture, not a logo.
	colours := []rgba{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 0, 255}, {255, 0, 255, 255}, {0, 255, 255, 255}, {255, 128, 0, 255}, {128, 0, 255, 255}}
	for k := range 64 {
		copy(px[((4+k/8)*toneSize+4+k%8)*4:], colours[k/8][:])
	}
	tone(t, px, "full")
}
