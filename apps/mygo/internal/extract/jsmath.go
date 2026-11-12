package extract

import "math"

// jsSin and jsCos are V8's Math.sin and Math.cos (fdlibm, src/base/ieee754.cc), so rough.js coordinates come out to the
// last bit; Go's math.Sin differs there and moved a few toFixed(1) roundings. Products are rounded on their own, as C
// does (no fused multiply-add). ponytail: arguments beyond 2^19·π/2, which drawings never reach, use Go's math.

func bits(u uint64) float64 { return math.Float64frombits(u) }

var (
	kC1, kC2, kC3 = bits(0x3FA555555555554C), bits(0xBF56C16C16C15177), bits(0x3EFA01A019CB1590)
	kC4, kC5, kC6 = bits(0xBE927E4F809C52AD), bits(0x3E21EE9EBDB4B1C4), bits(0xBDA8FAE9BE8838D4)
	kS1, kS2, kS3 = bits(0xBFC5555555555549), bits(0x3F8111111110F8A6), bits(0xBF2A01A019C161D5)
	kS4, kS5, kS6 = bits(0x3EC71DE357B1FE7D), bits(0xBE5AE5E68A2B9CEB), bits(0x3DE5D93A5ACFD57C)
	invpio2       = bits(0x3FE45F306DC9C883)
	pio21, pio21t = bits(0x3FF921FB54400000), bits(0x3DD0B4611A626331)
	pio22, pio22t = bits(0x3DD0B4611A600000), bits(0x3BA3198A2E037073)
	pio23, pio23t = bits(0x3BA3198A2E000000), bits(0x397B839A252049C1)
	npio2hw       = func() [32]int32 {
		var t [32]int32
		for n := range t {
			t[n] = int32(math.Float64bits(float64(n+1)*bits(0x3FF921FB54442D18)) >> 32)
		}
		return t
	}()
)

func high(x float64) int32 { return int32(math.Float64bits(x) >> 32) }

func mul(a, b float64) float64 { return float64(a * b) }

func kernelCos(x, y float64) float64 {
	ix := high(x) & 0x7fffffff
	if ix < 0x3e400000 && int32(x) == 0 {
		return 1
	}
	z := mul(x, x)
	r := mul(z, kC1+mul(z, kC2+mul(z, kC3+mul(z, kC4+mul(z, kC5+mul(z, kC6))))))
	if ix < 0x3FD33333 {
		return 1 - (mul(0.5, z) - (mul(z, r) - mul(x, y)))
	}
	qx := 0.28125
	if ix <= 0x3fe90000 {
		qx = math.Float64frombits(uint64(uint32(ix-0x00200000)) << 32)
	}
	hz := mul(0.5, z) - qx
	return (1 - qx) - (hz - (mul(z, r) - mul(x, y)))
}

func kernelSin(x, y float64, iy int) float64 {
	ix := high(x) & 0x7fffffff
	if ix < 0x3e400000 && int32(x) == 0 {
		return x
	}
	z := mul(x, x)
	v := mul(z, x)
	r := kS2 + mul(z, kS3+mul(z, kS4+mul(z, kS5+mul(z, kS6))))
	if iy == 0 {
		return x + mul(v, kS1+mul(z, r))
	}
	return x - ((mul(z, mul(0.5, y)-mul(v, r)) - y) - mul(v, kS1))
}

// remPio2 is __ieee754_rem_pio2 for |x| up to 2^19·π/2: x - n·π/2 as y0 + y1, and n; ok is false beyond.
func remPio2(x float64) (n int32, y0, y1 float64, ok bool) {
	hx := high(x)
	ix := hx & 0x7fffffff
	if ix <= 0x3fe921fb {
		return 0, x, 0, true
	}
	if ix < 0x4002d97c {
		if hx > 0 {
			z := x - pio21
			if ix != 0x3ff921fb {
				y0 = z - pio21t
				return 1, y0, (z - y0) - pio21t, true
			}
			z -= pio22
			y0 = z - pio22t
			return 1, y0, (z - y0) - pio22t, true
		}
		z := x + pio21
		if ix != 0x3ff921fb {
			y0 = z + pio21t
			return -1, y0, (z - y0) + pio21t, true
		}
		z += pio22
		y0 = z + pio22t
		return -1, y0, (z - y0) + pio22t, true
	}
	if ix > 0x413921fb {
		return 0, 0, 0, false
	}
	t := math.Abs(x)
	n = int32(mul(t, invpio2) + 0.5)
	fn := float64(n)
	r := t - mul(fn, pio21)
	w := mul(fn, pio21t)
	if n < 32 && ix != npio2hw[n-1] {
		y0 = r - w
	} else {
		j := ix >> 20
		y0 = r - w
		if i := j - ((high(y0) >> 20) & 0x7ff); i > 16 {
			t = r
			w = mul(fn, pio22)
			r = t - w
			w = mul(fn, pio22t) - ((t - r) - w)
			y0 = r - w
			if i = j - ((high(y0) >> 20) & 0x7ff); i > 49 {
				t = r
				w = mul(fn, pio23)
				r = t - w
				w = mul(fn, pio23t) - ((t - r) - w)
				y0 = r - w
			}
		}
	}
	y1 = (r - y0) - w
	if hx < 0 {
		return -n, -y0, -y1, true
	}
	return n, y0, y1, true
}

func jsSin(x float64) float64 {
	if high(x)&0x7fffffff <= 0x3fe921fb {
		return kernelSin(x, 0, 0)
	}
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return math.NaN()
	}
	n, y0, y1, ok := remPio2(x)
	if !ok {
		return math.Sin(x)
	}
	switch n & 3 {
	case 0:
		return kernelSin(y0, y1, 1)
	case 1:
		return kernelCos(y0, y1)
	case 2:
		return -kernelSin(y0, y1, 1)
	}
	return -kernelCos(y0, y1)
}

func jsCos(x float64) float64 {
	if high(x)&0x7fffffff <= 0x3fe921fb {
		return kernelCos(x, 0)
	}
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return math.NaN()
	}
	n, y0, y1, ok := remPio2(x)
	if !ok {
		return math.Cos(x)
	}
	switch n & 3 {
	case 0:
		return kernelCos(y0, y1)
	case 1:
		return -kernelSin(y0, y1, 1)
	case 2:
		return -kernelCos(y0, y1)
	}
	return kernelSin(y0, y1, 1)
}
