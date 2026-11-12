//go:build darwin

package video

import (
	"image/color"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testdata/h264.mp4 and hevc.mp4 are six 64×48 frames of solid colours, made
// with ffmpeg: red, green, blue at 0, 0.1 and 0.2 s, white at 0.3 s shown
// until 0.5 s, then black and yellow, the video ending at 0.7 s; the H.264
// one has B-frames, decoded out of order.
var clip = []struct {
	c color.RGBA
	d time.Duration
}{
	{color.RGBA{255, 0, 0, 255}, 100 * time.Millisecond},
	{color.RGBA{0, 255, 0, 255}, 100 * time.Millisecond},
	{color.RGBA{0, 0, 255, 255}, 100 * time.Millisecond},
	{color.RGBA{255, 255, 255, 255}, 200 * time.Millisecond},
	{color.RGBA{0, 0, 0, 255}, 100 * time.Millisecond},
	{color.RGBA{255, 255, 0, 255}, 100 * time.Millisecond},
}

func near(a, b color.RGBA) bool {
	d := func(x, y uint8) bool { return int(x)-int(y) < 8 && int(y)-int(x) < 8 }
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B) && a.A == b.A
}

// The frames come in presentation order, each timed until the next, and
// again after Rewind.
func TestFrames(t *testing.T) {
	for _, name := range []string{"h264.mp4", "hevc.mp4"} {
		d, err := Open(filepath.Join("testdata", name), 0, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if w, h := d.Size(); w != 64 || h != 48 {
			t.Errorf("%s is %d×%d", name, w, h)
		}
		for round := range 2 {
			for i, want := range clip {
				f, err := d.Next()
				if err != nil {
					t.Fatalf("%s, round %d, frame %d: %v", name, round, i, err)
				}
				px := f.Image.At(32, 24).(color.RGBA)
				if !near(px, want.c) || f.Duration != want.d {
					t.Errorf("%s, round %d, frame %d: %v for %v, want %v for %v", name, round, i, px, f.Duration, want.c, want.d)
				}
			}
			if _, err := d.Next(); err != io.EOF {
				t.Fatalf("%s, round %d: after the last frame, %v", name, round, err)
			}
			if p := d.Progress(); p < 0.85 || p > 0.86 {
				t.Errorf("%s: the last frame begins %.3f of the way", name, p)
			}
			if err := d.Rewind(); err != nil {
				t.Fatalf("%s: rewind: %v", name, err)
			}
		}
		d.Close()
		if _, err := d.Next(); err == nil {
			t.Errorf("%s decodes after Close", name)
		}
	}
}

// Frames come scaled down by the system to cover the box they show in, and
// never larger than the video.
func TestScaled(t *testing.T) {
	for _, k := range []struct{ boxW, boxH, w, h int }{{32, 24, 32, 24}, {40, 10, 40, 30}, {128, 96, 64, 48}} {
		d, err := Open("testdata/hevc.mp4", k.boxW, k.boxH)
		if err != nil {
			t.Fatal(err)
		}
		f, err := d.Next()
		if err != nil {
			t.Fatal(err)
		}
		if b := f.Image.Bounds(); b.Dx() != k.w || b.Dy() != k.h {
			t.Errorf("for %d×%d: %v, want %d×%d", k.boxW, k.boxH, b, k.w, k.h)
		}
		if px := f.Image.At(k.w/2, k.h/2).(color.RGBA); !near(px, clip[0].c) {
			t.Errorf("for %d×%d: %v", k.boxW, k.boxH, px)
		}
		d.Close()
	}
}

// A file cut short or not a video at all fails, at Open or at its first
// frame, rather than playing nothing.
func TestBroken(t *testing.T) {
	good, err := os.ReadFile("testdata/h264.mp4")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{"cut.mp4": good[:1000], "noise.mp4": good[len(good)-900:], "empty.mp4": nil} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		d, err := Open(path, 0, 0)
		if err == nil {
			_, err = d.Next()
			d.Close()
		}
		if err == nil || err == io.EOF {
			t.Errorf("%s: %v", name, err)
		}
	}
}
