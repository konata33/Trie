package explore

// IconTone is shared/icon-tone.ts iconTone: how a site icon should sit on the dark interface. Favicons are drawn for
// white browser tabs, so painting all of them on a light disc turns every row into a white dot. Read from the icon's
// own pixels (RGBA, size × size):
//   - "ink": a dark, colourless mark on a transparent ground (Innei, Anthony Fu). It would vanish on the dark UI; draw it light.
//   - "paper": a flat logo on its own white square (NN/g, Fonts In Use). Inverted with its hues turned back, so it reads dark.
//   - "clear": a coloured mark on a transparent ground. As drawn.
//   - "full": anything else that fills its square (a dark tile, a photo). As drawn.
func IconTone(px []byte, size int) string {
	lum := func(i int) float64 {
		return (0.2126*float64(px[i]) + 0.7152*float64(px[i+1]) + 0.0722*float64(px[i+2])) / 255
	}
	sat := func(i int) float64 {
		return float64(max(px[i], px[i+1], px[i+2])-min(px[i], px[i+1], px[i+2])) / 255
	}
	// The outer ring tells the ground: a favicon either fills its square or stands on transparency.
	ring, solid, white := 0, 0, 0
	for y := range size {
		for x := range size {
			if x > 0 && y > 0 && x < size-1 && y < size-1 {
				continue
			}
			i := (y*size + x) * 4
			ring++
			if px[i+3] > 200 {
				solid++
				if lum(i) > 0.85 {
					white++
				}
			}
		}
	}
	if float64(solid)/float64(ring) < 0.5 {
		n, l, s := 0, 0.0, 0.0
		for i := 0; i < len(px); i += 4 {
			if px[i+3] > 40 {
				n++
				l += lum(i)
				s += sat(i)
			}
		}
		if n > 0 && l/float64(n) < 0.3 && s/float64(n) < 0.2 {
			return "ink"
		}
		return "clear"
	}
	if float64(white)/float64(ring) < 0.9 {
		return "full"
	}
	// On white: a logo is grey or has a few flat colours; a photo on white has many and is left alone.
	// ponytail: a greyscale photo on a white square would be inverted; no such favicon seen yet, add a texture check if one appears.
	bins := map[int]int{}
	n, s := 0, 0.0
	for i := 0; i < len(px); i += 4 {
		if px[i+3] <= 200 {
			continue
		}
		n++
		s += sat(i)
		bins[int(px[i]>>5)<<6|int(px[i+1]>>5)<<3|int(px[i+2]>>5)]++
	}
	colours := 0
	for _, count := range bins {
		if float64(count)/float64(n) > 0.02 {
			colours++
		}
	}
	if s/float64(n) < 0.1 || colours <= 4 {
		return "paper"
	}
	return "full"
}
