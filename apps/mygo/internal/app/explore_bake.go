package app

import (
	"image"
	"image/draw"
	"math"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"

	"trie/internal/theme"
)

// Explore's pictures that fade by a gradient are baked once into a bitmap of
// their own, at half a pixel a DIP, rather than masked as they draw
// (MaskLinearGradient): a mask is drawn on the CPU and handed to the GPU
// again each time it moves, after the GPU has finished every frame in
// flight, which made each frame of a scroll wait for the last. Drawn as a
// picture, they move for free; they look as the masks did, the soft copy
// (40 wide) and the blur leaving nothing finer than half a pixel a DIP.
const bakeScale = 0.5

// bandKey is the geometry a band is baked for (band): a band w wide, h high
// at top, rising over rise, over a picture whose box has its top at y and is
// ph high. The box is the band's width and holds the picture top-aligned,
// unless pw is set: then it is pw wide from x, the picture held at px, py
// (a door's, kindDoor).
type bandKey struct{ w, h, top, rise, y, ph, x, pw, px, py float32 }

// bandRise is the words' band rising from clear:
// how much of it shows, at fractions of its rise.
var bandRise = [][2]float32{{0, 0}, {0.3, 0.12}, {0.6, 0.45}, {0.85, 0.85}, {1, 1}}

// along is the piecewise linear ramp stops at t, held at its ends.
func along(stops [][2]float32, t float32) float32 {
	if t <= stops[0][0] {
		return stops[0][1]
	}
	for i := 1; i < len(stops); i++ {
		if a, b := stops[i-1], stops[i]; t <= b[0] {
			return a[1] + (b[1]-a[1])*(t-a[0])/(b[0]-a[0])
		}
	}
	return stops[len(stops)-1][1]
}

// bandScrim is the band with no picture under it: the scrim, rising.
func bandScrim(k float32) ui.LinearGradient {
	stops := make([]ui.GradientStop, 0, len(bandRise)+1)
	for _, s := range bandRise {
		stops = append(stops, ui.GradientStop{Color: theme.Scrim.Alpha(s[1]), At: s[0] * k})
	}
	return ui.LinearGradient{Angle: 180, Stops: append(stops, ui.GradientStop{Color: theme.Scrim, At: 1})}
}

// bakeBand is a band w×h DIPs as band drew it: soft, the picture's small
// copy, covering its box (bandKey), under
// the scrim, the two rising from clear over rise. A mask fades each thing
// drawn on its own (MaskLinearGradient), so the scrim over the picture is
// the scrim faded over the picture faded, not the two faded together.
func bakeBand(soft image.Image, g bandKey) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, max(1, int(math.Ceil(float64(g.w*bakeScale)))), max(1, int(math.Ceil(float64(g.h*bakeScale))))))
	src := premultiplied(soft)
	sw, sh := float32(src.Bounds().Dx()), float32(src.Bounds().Dy())
	pw, px, py := g.w, float32(0.5), float32(0)
	if g.pw > 0 {
		pw, px, py = g.pw, g.px, g.py
	}
	scale := max(pw/sw, g.ph/sh) // Fit(Cover), ObjectPosition(px, py)
	ox, oy := g.x+(pw-sw*scale)*px, (g.ph-sh*scale)*py
	sr, sg, sb, sa := theme.Scrim.R, theme.Scrim.G, theme.Scrim.B, float32(theme.Scrim.A)/255
	r := min(g.rise, g.h)
	bw, bh := out.Bounds().Dx(), out.Bounds().Dy()
	for by := range bh {
		yd := (float32(by) + 0.5) * g.h / float32(bh)
		m := float32(1)
		if r > 0 {
			m = along(bandRise, yd/r)
		}
		yp := yd + g.top - g.y // in the picture's box
		for bx := range bw {
			xd := (float32(bx) + 0.5) * g.w / float32(bw)
			var p [4]float32
			if yp >= 0 && yp < g.ph && xd >= g.x && xd < g.x+pw {
				p = sample(src, (xd-ox)/scale, (yp-oy)/scale)
			}
			// The picture faded by m, then the scrim faded by m over it.
			s := m * sa
			px := out.Pix[out.PixOffset(bx, by):]
			px[0] = byte(s*float32(sr) + m*p[0]*(1-s) + 0.5)
			px[1] = byte(s*float32(sg) + m*p[1]*(1-s) + 0.5)
			px[2] = byte(s*float32(sb) + m*p[2]*(1-s) + 0.5)
			px[3] = byte(255*(1-(1-m*p[3]/255)*(1-s)) + 0.5)
		}
	}
	return out
}

// fade draws an element fading out downward, whole down to From of its
// height and gone at To, as a mask of a linear gradient did, but on the GPU
// from the element's own bitmap: words that fade, which no picture can
// stand for.
var fade = shader.MustCompile(`package main

var From float
var To float

func ColorEffect(pos vec2, color vec4) vec4 {
	h := rectSize().y
	return color * clamp((To*h-pos.y)/((To-From)*h), 0, 1)
}
`)

// The openings of a front-page story and of a cover running to the foot of
// their space.
var (
	storyFade = fade.Effect(shader.Options{Uniforms: map[string]any{"From": 0.5, "To": 0.9}})
	coverFade = fade.Effect(shader.Options{Uniforms: map[string]any{"From": 0.4, "To": 0.95}})
)

// groundKind is where a writer's picture is sunk (ground).
type groundKind uint8

const (
	groundCover groundKind = iota // the foot of a cover or of their panel's thumbnail: dimmed, then clear above 30%
	groundStory                   // the lower half of a front-page story: clear above 40%
)

// bakeGround is a writer's latest picture sunk into a box w×h DIPs as Plate
// and Ink drew it: covering the box, top-aligned, blurred by 4 (transparent
// beyond the box, as the GPU's blur is), dimmed on a cover, and fading out
// upward from 30% (a cover) or 40% (a story) of the box up, the dimming
// faded on its own over the picture faded, as a mask fades each thing.
func bakeGround(img image.Image, kind groundKind, w, h float32) *image.RGBA {
	bw, bh := max(1, int(math.Ceil(float64(w*bakeScale)))), max(1, int(math.Ceil(float64(h*bakeScale))))
	// The picture as it covered the box, at the bake's pixels.
	fit := image.NewRGBA(image.Rect(0, 0, bw, bh))
	src := premultiplied(img)
	sw, sh := float32(src.Bounds().Dx()), float32(src.Bounds().Dy())
	scale := max(w/sw, h/sh)
	ox := (w - sw*scale) / 2
	for by := range bh {
		yd := (float32(by) + 0.5) * h / float32(bh)
		for bx := range bw {
			xd := (float32(bx) + 0.5) * w / float32(bw)
			p := sample(src, (xd-ox)/scale, yd/scale)
			px := fit.Pix[fit.PixOffset(bx, by):]
			px[0], px[1], px[2], px[3] = byte(p[0]+0.5), byte(p[1]+0.5), byte(p[2]+0.5), byte(p[3]+0.5)
		}
	}
	out := blur(fit, 4*float32(bw)/w)
	dim, clear := float32(0.45), float32(0.3)
	if kind == groundStory {
		dim, clear = 0, 0.4
	}
	for by := range bh {
		fromFoot := 1 - (float32(by)+0.5)/float32(bh)
		m := float32(1)
		if fromFoot > clear {
			m = max(0, 1-(fromFoot-clear)/(1-clear))
		}
		d := m * dim
		for bx := range bw {
			px := out.Pix[out.PixOffset(bx, by):]
			// The picture faded by m, then the darkening faded by m over it.
			px[0] = byte(m*float32(px[0])*(1-d) + 0.5)
			px[1] = byte(m*float32(px[1])*(1-d) + 0.5)
			px[2] = byte(m*float32(px[2])*(1-d) + 0.5)
			px[3] = byte(255*(1-(1-m*float32(px[3])/255)*(1-d)) + 0.5)
		}
	}
	return out
}

// premultiplied is img as premultiplied RGBA from its origin.
func premultiplied(img image.Image) *image.RGBA {
	if m, ok := img.(*image.RGBA); ok && m.Bounds().Min == (image.Point{}) {
		return m
	}
	b := img.Bounds()
	m := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(m, m.Bounds(), img, b.Min, draw.Src)
	return m
}

// sample is img at (u, v) in its pixels, blended between the four nearest
// as a GPU's linear sampler does, held at its edges.
func sample(img *image.RGBA, u, v float32) (p [4]float32) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	u, v = min(max(u-0.5, 0), float32(w-1)), min(max(v-0.5, 0), float32(h-1))
	x0, y0 := int(u), int(v)
	x1, y1 := min(x0+1, w-1), min(y0+1, h-1)
	fx, fy := u-float32(x0), v-float32(y0)
	for i := range 4 {
		at := func(x, y int) float32 { return float32(img.Pix[img.PixOffset(x, y)+i]) }
		top := at(x0, y0) + (at(x1, y0)-at(x0, y0))*fx
		bottom := at(x0, y1) + (at(x1, y1)-at(x0, y1))*fx
		p[i] = top + (bottom-top)*fy
	}
	return p
}

// blur is img blurred by a Gaussian of sigma pixels, transparent beyond it.
func blur(img *image.RGBA, sigma float32) *image.RGBA {
	r := int(math.Ceil(float64(3 * sigma)))
	if r < 1 {
		return img
	}
	k := make([]float32, 2*r+1)
	var sum float32
	for i := range k {
		d := float32(i - r)
		k[i] = float32(math.Exp(float64(-d * d / (2 * sigma * sigma))))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	pass := func(src *image.RGBA, dx, dy int) *image.RGBA {
		dst := image.NewRGBA(src.Bounds())
		for y := range h {
			for x := range w {
				var acc [4]float32
				for i, kv := range k {
					sx, sy := x+(i-r)*dx, y+(i-r)*dy
					if sx < 0 || sy < 0 || sx >= w || sy >= h {
						continue
					}
					s := src.Pix[src.PixOffset(sx, sy):]
					acc[0] += kv * float32(s[0])
					acc[1] += kv * float32(s[1])
					acc[2] += kv * float32(s[2])
					acc[3] += kv * float32(s[3])
				}
				d := dst.Pix[dst.PixOffset(x, y):]
				d[0], d[1], d[2], d[3] = byte(acc[0]+0.5), byte(acc[1]+0.5), byte(acc[2]+0.5), byte(acc[3]+0.5)
			}
		}
		return dst
	}
	return pass(pass(img, 1, 0), 0, 1)
}

// haloScale is the pixels a DIP the halos are baked at: blurred by 34 and
// more, they have nothing finer than four DIPs.
const haloScale = 0.25

// bakeHalos is the discs in a box w×h, the second over the first, each a
// radial gradient as the GPU draws one.
func bakeHalos(discs [2]haloDisc, w, h float32) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, max(1, int(math.Ceil(float64(w*haloScale)))), max(1, int(math.Ceil(float64(h*haloScale))))))
	bw, bh := out.Bounds().Dx(), out.Bounds().Dy()
	for by := range bh {
		y := (float32(by) + 0.5) * h / float32(bh)
		for bx := range bw {
			x := (float32(bx) + 0.5) * w / float32(bw)
			var acc [4]float32 // premultiplied
			for _, d := range discs {
				t := float32(math.Hypot(float64(x-d.cx), float64(y-d.cy))) / d.reach
				c := gradientAt(d.stops, t)
				a := c[3]
				for i := range 3 {
					acc[i] = c[i]*a + acc[i]*(1-a)
				}
				acc[3] = a + acc[3]*(1-a)
			}
			px := out.Pix[out.PixOffset(bx, by):]
			px[0], px[1], px[2], px[3] = byte(acc[0]+0.5), byte(acc[1]+0.5), byte(acc[2]+0.5), byte(acc[3]*255+0.5)
		}
	}
	return out
}

// gradientAt is stops at t: straight colour, 0 to 255, and alpha, 0 to 1.
func gradientAt(stops []ui.GradientStop, t float32) [4]float32 {
	at := func(s ui.GradientStop) [4]float32 {
		return [4]float32{float32(s.Color.R), float32(s.Color.G), float32(s.Color.B), float32(s.Color.A) / 255}
	}
	if t <= stops[0].At {
		return at(stops[0])
	}
	for i := 1; i < len(stops); i++ {
		if t <= stops[i].At {
			a, b := at(stops[i-1]), at(stops[i])
			f := (t - stops[i-1].At) / max(stops[i].At-stops[i-1].At, 1e-6)
			for j := range a {
				a[j] += (b[j] - a[j]) * f
			}
			return a
		}
	}
	return at(stops[len(stops)-1])
}
