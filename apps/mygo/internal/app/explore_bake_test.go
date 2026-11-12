package app

import (
	"image"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"

	"trie/internal/theme"
)

// A picture with detail and colour across it, for the bakes to be compared on.
func bakeSample() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 300, 200))
	for y := range 200 {
		for x := range 300 {
			c := color.NRGBA{uint8(x * 255 / 300), uint8(y * 255 / 200), 90, 255}
			if (x/20+y/20)%2 == 0 {
				c = color.NRGBA{235, 225, 205, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// worst is the largest difference of a channel between a and b in r.
func worst(a, b *image.RGBA, r image.Rectangle) int {
	m := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			p, q := a.Pix[a.PixOffset(x, y):], b.Pix[b.PixOffset(x, y):]
			for i := range 3 {
				m = max(m, abs(int(p[i])-int(q[i])))
			}
		}
	}
	return m
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// The baked band, ground and halos draw what the masks and gradients they
// replace drew, within what their half pixel a DIP and the blur leave.
func TestBakesDrawAsMasksDid(t *testing.T) {
	pic := bakeSample()
	const w, h = 320, 220
	draw := func(view func(c *ui.Context)) *image.RGBA {
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Size(w, h).Background(theme.Surface).Children(func() { view(c) })
		}, w, h)
		tt.Frame()
		return tt.Image()
	}
	bmp := ui.NewBitmap(pic)
	// The band: the soft copy under the scrim, masked by the rising gradient.
	small := shrinkTo(pic, 40)
	soft := ui.NewBitmap(small)
	top, bh, rise := float32(100), float32(120), float32(72)
	old := draw(func(c *ui.Context) {
		k := rise / bh
		black := theme.Darken
		mask := ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{{Color: black.Alpha(0), At: 0}, {Color: black.Alpha(0.12), At: 0.3 * k},
			{Color: black.Alpha(0.45), At: 0.6 * k}, {Color: black.Alpha(0.85), At: 0.85 * k}, {Color: black, At: k}, {Color: black, At: 1}}}
		ui.Image(c, bmp).Fit(ui.Cover).Absolute().Fill()
		ui.Box(c).Absolute().Left(0).Right(0).Top(top).Height(bh).ClipX().ClipY().MaskLinearGradient(mask).Children(func() {
			ui.Image(c, soft).Fit(ui.Cover).ObjectPosition(0.5, 0).Absolute().Left(0).Top(-top).Width(w).Height(h)
			ui.Box(c).Absolute().Fill().Background(theme.Scrim)
		})
	})
	baked := draw(func(c *ui.Context) {
		ui.Image(c, bmp).Fit(ui.Cover).Absolute().Fill()
		// In a box of its own, as the words it lies under are.
		ui.Box(c).Absolute().Fill().Children(func() { band(c, top, bh, rise, &exPic{small: small}, 0, w, h, 0) })
	})
	if d := worst(old, baked, image.Rect(0, int(top), w, h)); d > 12 {
		t.Errorf("the band differs by %d", d)
	}
	// A cover's ground: the picture blurred, dimmed and fading out upward.
	old = draw(func(c *ui.Context) {
		ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(h).MaskLinearGradient(ui.LinearGradient{
			Angle: 0, Stops: []ui.GradientStop{{Color: theme.Darken, At: 0.3}, {Color: theme.Darken.Alpha(0), At: 1}}}).Children(func() {
			ui.Image(c, bmp).Fit(ui.Cover).ObjectPosition(0.5, 0).Fill().Blur(4)
			ui.Box(c).Absolute().Fill().Background(theme.Darken.Alpha(0.45))
		})
	})
	g := ui.NewBitmap(bakeGround(pic, groundCover, w, h))
	baked = draw(func(c *ui.Context) {
		ui.Image(c, g).Fit(ui.FillBox).Absolute().Fill()
	})
	if d := worst(old, baked, image.Rect(8, 8, w-8, h-8)); d > 16 {
		t.Errorf("the ground differs by %d", d)
	}
	// The halos.
	colors := [2]ui.Color{ui.RGB(200, 120, 60), ui.RGB(60, 120, 200)}
	old = draw(func(c *ui.Context) {
		for _, d := range halos(colors, inkCover, w, h) {
			ui.Box(c).Absolute().Left(d.cx-d.reach).Top(d.cy-d.reach).Size(2*d.reach, 2*d.reach).RadialGradient(ui.RadialGradient{Stops: d.stops, Radius: d.reach})
		}
	})
	ex := newExploreState()
	baked = draw(func(c *ui.Context) {
		ui.Image(c, ex.aura(colors, inkCover, w, h)).Fit(ui.FillBox).Absolute().Fill()
	})
	if d := worst(old, baked, image.Rect(0, 0, w, h)); d > 6 {
		t.Errorf("the halos differ by %d", d)
	}
}
