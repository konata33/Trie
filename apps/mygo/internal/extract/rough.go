package extract

import (
	"math"
	"math/big"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The part of rough.js 4.6.6 (with hachure-fill 0.5.2, path-data-parser 0.1.0, points-on-curve 0.2.0 and points-on-path
// 0.2.1) that whiteboard.ts draws with: lines, linear paths, polygons, rectangles, ellipses, curves and paths, solid and
// hachure, cross-hatch and zigzag fills, each from its seed as Excalidraw draws them. Every product that feeds a sum is
// rounded on its own (float64(a*b)): Go may fuse a*b+c into one instruction on arm64, which V8 never does.
// ponytail: the dots, dashed and zigzag-line fills (which Excalidraw never writes) draw as hachure, and paths read only
// M, L, Q, C and Z (whiteboard.go's own rounded rectangles).

// jsPI is Math.PI as a value: expressions with it round step by step, as in JavaScript, not as exact Go constants.
var jsPI = math.Pi

type roughOptions struct {
	maxRandomnessOffset, roughness, bowing                 float64
	stroke, fill, fillStyle                                string // fill "" is no fill
	strokeWidth, curveTightness, curveFitting, stepCount   float64
	fillWeight, hachureAngle, hachureGap, fillShapeGain    float64
	seed                                                   float64
	disableMultiStroke, disableMultiStrokeFill, preserveVx bool
	random                                                 *roughRandom
}

func defaultRough() roughOptions {
	return roughOptions{maxRandomnessOffset: 2, roughness: 1, bowing: 1, stroke: "#000", strokeWidth: 1, curveFitting: 0.95, stepCount: 9,
		fillStyle: "hachure", fillWeight: -1, hachureAngle: -41, hachureGap: -1, fillShapeGain: 0.8}
}

type roughRandom struct{ seed float64 }

// next is rough.js's Random: a Park–Miller generator on 32-bit integers; seed 0 is Math.random.
func (r *roughRandom) next() float64 {
	if r.seed == 0 || math.IsNaN(r.seed) {
		return rand.Float64()
	}
	s := int32(uint32(48271) * uint32(toInt32(r.seed)))
	r.seed = float64(s)
	return float64(int32(0x7fffffff)&s) / (1 << 31)
}

func toInt32(f float64) int32 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return int32(uint32(int64(math.Mod(math.Trunc(f), 4294967296))))
}

func (o *roughOptions) next() float64 {
	if o.random == nil {
		o.random = &roughRandom{seed: o.seed}
	}
	return o.random.next()
}

func (o *roughOptions) offset(lo, hi, gain float64) float64 {
	return float64(o.roughness*gain) * (float64(o.next()*(hi-lo)) + lo)
}

func (o *roughOptions) offsetOpt(x, gain float64) float64 { return o.offset(-x, x, gain) }

type roughOp struct {
	op   string // move, bcurveTo, lineTo
	data []float64
}

type opSet struct {
	typ string // path, fillPath, fillSketch
	ops []roughOp
}

type point = [2]float64

func roughLine(x1, y1, x2, y2 float64, o *roughOptions, move, overlay bool) []roughOp {
	lengthSq := float64(math.Pow(x1-x2, 2)) + math.Pow(y1-y2, 2)
	length := math.Sqrt(lengthSq)
	gain := 1.0
	switch {
	case length < 200:
	case length > 500:
		gain = 0.4
	default:
		gain = float64(-0.0016668*length) + 1.233334
	}
	offset := o.maxRandomnessOffset
	if float64(offset*offset)*100 > lengthSq {
		offset = length / 10
	}
	half := offset / 2
	diverge := 0.2 + float64(o.next()*0.2)
	midX := float64(o.bowing*o.maxRandomnessOffset) * (y2 - y1) / 200
	midY := float64(o.bowing*o.maxRandomnessOffset) * (x1 - x2) / 200
	midX = o.offsetOpt(midX, gain)
	midY = o.offsetOpt(midY, gain)
	randomHalf := func() float64 { return o.offsetOpt(half, gain) }
	randomFull := func() float64 { return o.offsetOpt(offset, gain) }
	keep := func(f func() float64) float64 {
		if o.preserveVx {
			return 0
		}
		return f()
	}
	var ops []roughOp
	if move {
		if overlay {
			a := x1 + keep(randomHalf)
			ops = append(ops, roughOp{"move", []float64{a, y1 + keep(randomHalf)}})
		} else {
			a := x1 + keep(randomFull)
			ops = append(ops, roughOp{"move", []float64{a, y1 + keep(randomFull)}})
		}
	}
	r := randomFull
	if overlay {
		r = randomHalf
	}
	d := make([]float64, 6)
	d[0] = midX + x1 + float64((x2-x1)*diverge) + r()
	d[1] = midY + y1 + float64((y2-y1)*diverge) + r()
	d[2] = midX + x1 + float64(2*(x2-x1)*diverge) + r()
	d[3] = midY + y1 + float64(2*(y2-y1)*diverge) + r()
	d[4] = x2 + keep(r)
	d[5] = y2 + keep(r)
	return append(ops, roughOp{"bcurveTo", d})
}

func doubleLine(x1, y1, x2, y2 float64, o *roughOptions, filling bool) []roughOp {
	single := o.disableMultiStroke
	if filling {
		single = o.disableMultiStrokeFill
	}
	ops := roughLine(x1, y1, x2, y2, o, true, false)
	if single {
		return ops
	}
	return append(ops, roughLine(x1, y1, x2, y2, o, true, true)...)
}

func linearPath(points []point, close bool, o *roughOptions) opSet {
	n := len(points)
	switch {
	case n > 2:
		var ops []roughOp
		for i := 0; i < n-1; i++ {
			ops = append(ops, doubleLine(points[i][0], points[i][1], points[i+1][0], points[i+1][1], o, false)...)
		}
		if close {
			ops = append(ops, doubleLine(points[n-1][0], points[n-1][1], points[0][0], points[0][1], o, false)...)
		}
		return opSet{"path", ops}
	case n == 2:
		return opSet{"path", doubleLine(points[0][0], points[0][1], points[1][0], points[1][1], o, false)}
	}
	return opSet{"path", nil}
}

func cloneAlterSeed(o *roughOptions) *roughOptions {
	c := *o
	c.random = nil
	if o.seed != 0 && !math.IsNaN(o.seed) {
		c.seed = o.seed + 1
	}
	return &c
}

func roughCurve(points []point, o *roughOptions) opSet {
	if len(points) == 0 {
		return opSet{"path", nil}
	}
	o1 := curveWithOffset(points, float64(1*(1+float64(o.roughness*0.2))), o)
	var o2 []roughOp
	if !o.disableMultiStroke {
		o2 = curveWithOffset(points, float64(1.5*(1+float64(o.roughness*0.22))), cloneAlterSeed(o))
	}
	return opSet{"path", append(o1, o2...)}
}

func curveWithOffset(points []point, offset float64, o *roughOptions) []roughOp {
	var ps []point
	p := func(q point) point {
		x := q[0] + o.offsetOpt(offset, 1)
		return point{x, q[1] + o.offsetOpt(offset, 1)}
	}
	ps = append(ps, p(points[0]), p(points[0]))
	for i := 1; i < len(points); i++ {
		ps = append(ps, p(points[i]))
		if i == len(points)-1 {
			ps = append(ps, p(points[i]))
		}
	}
	return curveOps(ps, o)
}

func curveOps(points []point, o *roughOptions) []roughOp {
	n := len(points)
	switch {
	case n > 3:
		s := 1 - o.curveTightness
		ops := []roughOp{{"move", []float64{points[1][0], points[1][1]}}}
		for i := 1; i+2 < n; i++ {
			v := points[i]
			b1 := point{v[0] + (float64(s*points[i+1][0])-float64(s*points[i-1][0]))/6, v[1] + (float64(s*points[i+1][1])-float64(s*points[i-1][1]))/6}
			b2 := point{points[i+1][0] + (float64(s*points[i][0])-float64(s*points[i+2][0]))/6, points[i+1][1] + (float64(s*points[i][1])-float64(s*points[i+2][1]))/6}
			ops = append(ops, roughOp{"bcurveTo", []float64{b1[0], b1[1], b2[0], b2[1], points[i+1][0], points[i+1][1]}})
		}
		return ops
	case n == 3:
		return []roughOp{{"move", []float64{points[1][0], points[1][1]}}, {"bcurveTo", []float64{points[1][0], points[1][1], points[2][0], points[2][1], points[2][0], points[2][1]}}}
	case n == 2:
		return roughLine(points[0][0], points[0][1], points[1][0], points[1][1], o, true, true)
	}
	return nil
}

type ellipseParams struct{ increment, rx, ry float64 }

func generateEllipseParams(width, height float64, o *roughOptions) ellipseParams {
	psq := math.Sqrt(float64(jsPI*2) * math.Sqrt((float64(math.Pow(width/2, 2))+math.Pow(height/2, 2))/2))
	steps := math.Ceil(math.Max(o.stepCount, float64((o.stepCount/math.Sqrt(200))*psq)))
	rx, ry := math.Abs(width/2), math.Abs(height/2)
	fit := 1 - o.curveFitting
	rx += o.offsetOpt(float64(rx*fit), 1)
	ry += o.offsetOpt(float64(ry*fit), 1)
	return ellipseParams{float64(jsPI*2) / steps, rx, ry}
}

func ellipseWithParams(x, y float64, o *roughOptions, p ellipseParams) ([]point, opSet) {
	inner := o.offset(0.4, 1, 1)
	ap1, cp1 := ellipsePoints(p.increment, x, y, p.rx, p.ry, 1, float64(p.increment*o.offset(0.1, inner, 1)), o)
	ops := curveOps(ap1, o)
	if !o.disableMultiStroke && o.roughness != 0 {
		ap2, _ := ellipsePoints(p.increment, x, y, p.rx, p.ry, 1.5, 0, o)
		ops = append(ops, curveOps(ap2, o)...)
	}
	return cp1, opSet{"path", ops}
}

func ellipsePoints(increment, cx, cy, rx, ry, offset, overlap float64, o *roughOptions) (all, core []point) {
	at := func(dx, dy float64, scale, angle float64) point {
		x := dx + cx + float64(float64(scale*rx)*jsCos(angle))
		return point{x, dy + cy + float64(float64(scale*ry)*jsSin(angle))}
	}
	if o.roughness == 0 {
		increment /= 4
		all = append(all, at(0, 0, 1, -increment))
		for angle := 0.0; angle <= jsPI*2; angle += increment {
			p := at(0, 0, 1, angle)
			core, all = append(core, p), append(all, p)
		}
		all = append(all, at(0, 0, 1, 0), at(0, 0, 1, increment))
		return all, core
	}
	jitter := func(scale, angle float64) point {
		dx := o.offsetOpt(offset, 1)
		x := dx + cx + float64(float64(scale*rx)*jsCos(angle))
		dy := o.offsetOpt(offset, 1)
		return point{x, dy + cy + float64(float64(scale*ry)*jsSin(angle))}
	}
	rad := o.offsetOpt(0.5, 1) - jsPI/2
	all = append(all, jitter(0.9, rad-increment))
	end := float64(jsPI*2) + rad - 0.01
	for angle := rad; angle < end; angle += increment {
		p := jitter(1, angle)
		core, all = append(core, p), append(all, p)
	}
	all = append(all, jitter(1, rad+float64(jsPI*2)+float64(overlap*0.5)), jitter(0.98, rad+overlap), jitter(0.9, rad+float64(overlap*0.5)))
	return all, core
}

func bezierTo(x1, y1, x2, y2, x, y float64, current point, o *roughOptions) []roughOp {
	ro := o.maxRandomnessOffset
	if ro == 0 {
		ro = 1
	}
	ros := []float64{ro, ro + 0.3}
	iterations := 2
	if o.disableMultiStroke {
		iterations = 1
	}
	var ops []roughOp
	for i := 0; i < iterations; i++ {
		if i == 0 {
			ops = append(ops, roughOp{"move", []float64{current[0], current[1]}})
		} else {
			a, b := current[0], current[1]
			if !o.preserveVx {
				a += o.offsetOpt(ros[0], 1)
				b += o.offsetOpt(ros[0], 1)
			}
			ops = append(ops, roughOp{"move", []float64{a, b}})
		}
		f := point{x, y}
		if !o.preserveVx {
			fx := x + o.offsetOpt(ros[i], 1)
			f = point{fx, y + o.offsetOpt(ros[i], 1)}
		}
		d := make([]float64, 4)
		for k, v := range []float64{x1, y1, x2, y2} {
			d[k] = v + o.offsetOpt(ros[i], 1)
		}
		ops = append(ops, roughOp{"bcurveTo", append(d, f[0], f[1])})
	}
	return ops
}

type pathSegment struct {
	key  byte
	data []float64
}

var (
	pathSpace   = regexp.MustCompile(`^[ \t\r\n,]+`)
	pathCommand = regexp.MustCompile(`^[aAcChHlLmMqQsStTvVzZ]`)
	pathNumber  = regexp.MustCompile(`^(?:[-+]?[0-9]+(?:\.[0-9]*)?|[-+]?\.[0-9]+)(?:[eE][-+]?[0-9]+)?`)
	minusSpace  = regexp.MustCompile(`-` + sp)
	pathParams  = map[byte]int{'A': 7, 'a': 7, 'C': 6, 'c': 6, 'H': 1, 'h': 1, 'L': 2, 'l': 2, 'M': 2, 'm': 2, 'Q': 4, 'q': 4, 'S': 4, 's': 4, 'T': 2, 't': 2, 'V': 1, 'v': 1, 'Z': 0, 'z': 0}
)

// parsePath is path-data-parser's parsePath, then absolutize and normalize for M, L, C, Q and Z.
func parsePath(d string) []pathSegment {
	type token struct {
		command byte
		number  float64
		isNum   bool
	}
	var tokens []token
	for rest := d; rest != ""; {
		if m := pathSpace.FindString(rest); m != "" {
			rest = rest[len(m):]
		} else if m := pathCommand.FindString(rest); m != "" {
			tokens, rest = append(tokens, token{command: m[0]}), rest[1:]
		} else if m := pathNumber.FindString(rest); m != "" {
			v, _ := strconv.ParseFloat(m, 64)
			tokens, rest = append(tokens, token{number: v, isNum: true}), rest[len(m):]
		} else {
			return nil
		}
	}
	var segments []pathSegment
	mode := byte(0)
	for i := 0; i < len(tokens); {
		count := 0
		switch {
		case mode == 0:
			if tokens[i].isNum || tokens[i].command != 'M' && tokens[i].command != 'm' {
				return parsePath("M0,0" + d)
			}
			mode, count = tokens[i].command, pathParams[tokens[i].command]
			i++
		case tokens[i].isNum:
			count = pathParams[mode]
		default:
			mode, count = tokens[i].command, pathParams[tokens[i].command]
			i++
		}
		if i+count > len(tokens) {
			return segments // "Path data ended short" throws in the TS; whiteboard.go writes complete paths
		}
		data := make([]float64, count)
		for k := range count {
			data[k] = tokens[i+k].number
		}
		segments = append(segments, pathSegment{mode, data})
		i += count
		if mode == 'M' {
			mode = 'L'
		}
	}
	var out []pathSegment
	cx, cy, subx, suby := 0.0, 0.0, 0.0, 0.0
	for _, s := range segments {
		switch s.key {
		case 'M':
			out = append(out, s)
			cx, cy, subx, suby = s.data[0], s.data[1], s.data[0], s.data[1]
		case 'L':
			out = append(out, s)
			cx, cy = s.data[0], s.data[1]
		case 'C':
			out = append(out, s)
			cx, cy = s.data[4], s.data[5]
		case 'Q':
			x1, y1, x, y := s.data[0], s.data[1], s.data[2], s.data[3]
			out = append(out, pathSegment{'C', []float64{cx + float64(2*(x1-cx))/3, cy + float64(2*(y1-cy))/3, x + float64(2*(x1-x))/3, y + float64(2*(y1-y))/3, x, y}})
			cx, cy = x, y
		case 'Z', 'z':
			out = append(out, pathSegment{'Z', nil})
			cx, cy = subx, suby
		}
	}
	return out
}

func svgPath(segments []pathSegment, o *roughOptions) opSet {
	var ops []roughOp
	first, current := point{}, point{}
	for _, s := range segments {
		switch s.key {
		case 'M':
			current, first = point{s.data[0], s.data[1]}, point{s.data[0], s.data[1]}
		case 'L':
			ops = append(ops, doubleLine(current[0], current[1], s.data[0], s.data[1], o, false)...)
			current = point{s.data[0], s.data[1]}
		case 'C':
			ops = append(ops, bezierTo(s.data[0], s.data[1], s.data[2], s.data[3], s.data[4], s.data[5], current, o)...)
			current = point{s.data[4], s.data[5]}
		case 'Z':
			ops = append(ops, doubleLine(current[0], current[1], first[0], first[1], o, false)...)
			current = first
		}
	}
	return opSet{"path", ops}
}

// pointsOnPath is points-on-path's pointsOnPath(d, 1, distance).
func pointsOnPath(segments []pathSegment, tolerance, distance float64) [][]point {
	var sets [][]point
	var current, pending []point
	start := point{}
	appendCurve := func() {
		if len(pending) >= 4 {
			current = append(current, pointsOnBezierCurves(pending, tolerance)...)
		}
		pending = nil
	}
	appendPoints := func() {
		appendCurve()
		if len(current) > 0 {
			sets, current = append(sets, current), nil
		}
	}
	for _, s := range segments {
		switch s.key {
		case 'M':
			appendPoints()
			start = point{s.data[0], s.data[1]}
			current = append(current, start)
		case 'L':
			appendCurve()
			current = append(current, point{s.data[0], s.data[1]})
		case 'C':
			if len(pending) == 0 {
				last := start
				if len(current) > 0 {
					last = current[len(current)-1]
				}
				pending = append(pending, last)
			}
			pending = append(pending, point{s.data[0], s.data[1]}, point{s.data[2], s.data[3]}, point{s.data[4], s.data[5]})
		case 'Z':
			appendCurve()
			current = append(current, start)
		}
	}
	appendPoints()
	if distance == 0 {
		return sets
	}
	var out [][]point
	for _, set := range sets {
		if s := simplifyPoints(set, 0, len(set), distance, nil); len(s) > 0 {
			out = append(out, s)
		}
	}
	return out
}

func distanceSq(a, b point) float64 { return float64(math.Pow(a[0]-b[0], 2)) + math.Pow(a[1]-b[1], 2) }

func lerp(a, b point, t float64) point {
	return point{a[0] + float64((b[0]-a[0])*t), a[1] + float64((b[1]-a[1])*t)}
}

func flatness(p []point, i int) float64 {
	sq := func(v float64) float64 { return v * v }
	ux := sq(float64(3*p[i+1][0]) - float64(2*p[i][0]) - p[i+3][0])
	uy := sq(float64(3*p[i+1][1]) - float64(2*p[i][1]) - p[i+3][1])
	vx := sq(float64(3*p[i+2][0]) - float64(2*p[i+3][0]) - p[i][0])
	vy := sq(float64(3*p[i+2][1]) - float64(2*p[i+3][1]) - p[i][1])
	return math.Max(ux, vx) + math.Max(uy, vy)
}

func bezierSplit(p []point, i int, tolerance float64, out []point) []point {
	if flatness(p, i) < tolerance {
		if len(out) == 0 || math.Sqrt(distanceSq(out[len(out)-1], p[i])) > 1 {
			out = append(out, p[i])
		}
		return append(out, p[i+3])
	}
	q1, q2, q3 := lerp(p[i], p[i+1], .5), lerp(p[i+1], p[i+2], .5), lerp(p[i+2], p[i+3], .5)
	r1, r2 := lerp(q1, q2, .5), lerp(q2, q3, .5)
	red := lerp(r1, r2, .5)
	out = bezierSplit([]point{p[i], q1, r1, red}, 0, tolerance, out)
	return bezierSplit([]point{red, r2, q3, p[i+3]}, 0, tolerance, out)
}

func pointsOnBezierCurves(p []point, tolerance float64) []point {
	var out []point
	for i := 0; i < (len(p)-1)/3; i++ {
		out = bezierSplit(p, i*3, tolerance, out)
	}
	return out
}

func distanceToSegmentSq(p, v, w point) float64 {
	l2 := distanceSq(v, w)
	if l2 == 0 {
		return distanceSq(p, v)
	}
	t := (float64((p[0]-v[0])*(w[0]-v[0])) + float64((p[1]-v[1])*(w[1]-v[1]))) / l2
	return distanceSq(p, lerp(v, w, math.Max(0, math.Min(1, t))))
}

func simplifyPoints(points []point, start, end int, epsilon float64, out []point) []point {
	s, e := points[start], points[end-1]
	maxSq, maxIndex := 0.0, 1
	for i := start + 1; i < end-1; i++ {
		if d := distanceToSegmentSq(points[i], s, e); d > maxSq {
			maxSq, maxIndex = d, i
		}
	}
	if math.Sqrt(maxSq) > epsilon {
		out = simplifyPoints(points, start, maxIndex+1, epsilon, out)
		return simplifyPoints(points, maxIndex, end, epsilon, out)
	}
	if len(out) == 0 {
		out = append(out, s)
	}
	return append(out, e)
}

func solidFillPolygon(polygons [][]point, o *roughOptions) opSet {
	var ops []roughOp
	for _, points := range polygons {
		if len(points) <= 2 {
			continue
		}
		offset := o.maxRandomnessOffset
		for i, p := range points {
			x := p[0] + o.offsetOpt(offset, 1)
			op := roughOp{"lineTo", []float64{x, p[1] + o.offsetOpt(offset, 1)}}
			if i == 0 {
				op.op = "move"
			}
			ops = append(ops, op)
		}
	}
	return opSet{"fillPath", ops}
}

// patternFillPolygons is the hachure filler (and cross-hatch and zigzag, built on it).
func patternFillPolygons(polygons [][]point, o *roughOptions) opSet {
	switch o.fillStyle {
	case "cross-hatch":
		ops := renderHachure(polygonHachureLines(polygons, o), o)
		o2 := *o
		o2.hachureAngle += 90
		return opSet{"fillSketch", append(ops, renderHachure(polygonHachureLines(polygons, &o2), &o2)...)}
	case "zigzag":
		gap := o.hachureGap
		if gap < 0 {
			gap = float64(o.strokeWidth * 4)
		}
		gap = math.Max(gap, 0.1)
		o2 := *o
		o2.hachureGap = gap
		lines := polygonHachureLines(polygons, &o2)
		angle := (jsPI / 180) * o.hachureAngle
		dgx, dgy := float64(float64(gap*0.5)*jsCos(angle)), float64(float64(gap*0.5)*jsSin(angle))
		var zig [][2]point
		for _, l := range lines {
			if math.Sqrt(distanceSq(l[0], l[1])) != 0 {
				zig = append(zig, [2]point{{l[0][0] - dgx, l[0][1] + dgy}, l[1]}, [2]point{{l[0][0] + dgx, l[0][1] - dgy}, l[1]})
			}
		}
		return opSet{"fillSketch", renderHachure(zig, o)}
	}
	return opSet{"fillSketch", renderHachure(polygonHachureLines(polygons, o), o)}
}

func renderHachure(lines [][2]point, o *roughOptions) []roughOp {
	var ops []roughOp
	for _, l := range lines {
		ops = append(ops, doubleLine(l[0][0], l[0][1], l[1][0], l[1][1], o, true)...)
	}
	return ops
}

func polygonHachureLines(polygons [][]point, o *roughOptions) [][2]point {
	gap := o.hachureGap
	if gap < 0 {
		gap = float64(o.strokeWidth * 4)
	}
	gap = jsRound(math.Max(gap, 0.1))
	skip := 1.0
	if o.roughness >= 1 {
		r := 0.0
		if o.random != nil {
			r = o.random.next()
		}
		if r == 0 {
			r = rand.Float64()
		}
		if r > 0.7 {
			skip = gap
		}
	}
	if skip == 0 {
		skip = 1
	}
	return hachureLines(polygons, gap, o.hachureAngle+90, skip)
}

func rotatePoints(points []point, degrees float64) {
	angle := (jsPI / 180) * degrees
	cos, sin := jsCos(angle), jsSin(angle)
	for i := range points {
		x, y := points[i][0], points[i][1]
		points[i][0] = float64(x*cos) - float64(y*sin)
		points[i][1] = float64(x*sin) + float64(y*cos)
	}
}

// hachureLines rotates the polygons in place and back, as hachure-fill does, so a second fill sees the same points.
func hachureLines(polygons [][]point, gap, angle, step float64) [][2]point {
	gap = math.Max(gap, 0.1)
	if angle != 0 {
		for _, p := range polygons {
			rotatePoints(p, angle)
		}
	}
	lines := straightHachureLines(polygons, gap, step)
	if angle != 0 {
		for _, p := range polygons {
			rotatePoints(p, -angle)
		}
		for i := range lines {
			pts := lines[i][:]
			rotatePoints(pts, -angle)
		}
	}
	return lines
}

func straightHachureLines(polygons [][]point, gap, step float64) [][2]point {
	type edge struct{ ymin, ymax, x, islope float64 }
	var edges []*edge
	for _, polygon := range polygons {
		vertices := slices.Clone(polygon)
		if len(vertices) == 0 {
			continue
		}
		if vertices[0] != vertices[len(vertices)-1] {
			vertices = append(vertices, vertices[0])
		}
		if len(vertices) <= 2 {
			continue
		}
		for i := 0; i < len(vertices)-1; i++ {
			p1, p2 := vertices[i], vertices[i+1]
			if p1[1] != p2[1] {
				ymin := math.Min(p1[1], p2[1])
				x := p2[0]
				if ymin == p1[1] {
					x = p1[0]
				}
				edges = append(edges, &edge{ymin, math.Max(p1[1], p2[1]), x, (p2[0] - p1[0]) / (p2[1] - p1[1])})
			}
		}
	}
	slices.SortStableFunc(edges, func(a, b *edge) int {
		switch {
		case a.ymin < b.ymin:
			return -1
		case a.ymin > b.ymin:
			return 1
		case a.x < b.x:
			return -1
		case a.x > b.x:
			return 1
		case a.ymax == b.ymax:
			return 0
		}
		return int((a.ymax - b.ymax) / math.Abs(a.ymax-b.ymax))
	})
	var lines [][2]point
	if len(edges) == 0 {
		return lines
	}
	type active struct{ e *edge }
	var actives []active
	y := edges[0].ymin
	for iteration := 0; len(actives) > 0 || len(edges) > 0; iteration++ {
		if len(edges) > 0 {
			ix := -1
			for i, e := range edges {
				if e.ymin > y {
					break
				}
				ix = i
			}
			for _, e := range edges[:ix+1] {
				actives = append(actives, active{e})
			}
			edges = edges[ix+1:]
		}
		actives = slices.DeleteFunc(actives, func(a active) bool { return a.e.ymax <= y })
		slices.SortStableFunc(actives, func(a, b active) int {
			if a.e.x == b.e.x {
				return 0
			}
			return int((a.e.x - b.e.x) / math.Abs(a.e.x-b.e.x))
		})
		if step != 1 || math.Mod(float64(iteration), gap) == 0 {
			for i := 0; i+1 < len(actives); i += 2 {
				lines = append(lines, [2]point{{jsRound(actives[i].e.x), y}, {jsRound(actives[i+1].e.x), y}})
			}
		}
		y += step
		for _, a := range actives {
			a.e.x += float64(step * a.e.islope)
		}
	}
	return lines
}

// jsRound is Math.round: halves round up.
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	if r == 0 && math.Signbit(x) {
		return math.Copysign(0, -1)
	}
	return r
}

// jsToFixed is +x.toFixed(digits): the exact value rounded half up.
func jsToFixed(x float64, digits int) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) >= 1e21 {
		return x
	}
	exact := new(big.Float).SetFloat64(math.Abs(x)).Text('f', 1100)
	whole, frac, _ := strings.Cut(exact, ".")
	frac += strings.Repeat("0", digits+1)
	keep := whole + frac[:digits]
	v, _ := new(big.Int).SetString(keep, 10)
	if frac[digits] >= '5' {
		v.Add(v, big.NewInt(1))
	}
	s := v.String()
	if digits > 0 {
		s = strings.Repeat("0", max(0, digits+1-len(s))) + s
		s = s[:len(s)-digits] + "." + s[len(s)-digits:]
	}
	f, _ := strconv.ParseFloat(s, 64)
	if x < 0 {
		f = -f
	}
	return f
}

// jsHypot is V8's Math.hypot: scaled by the largest value, Kahan-summed.
func jsHypot(values ...float64) float64 {
	maxAbs := 0.0
	for _, v := range values {
		if a := math.Abs(v); a > maxAbs {
			maxAbs = a
		}
	}
	if maxAbs == 0 {
		return 0
	}
	sum, compensation := 0.0, 0.0
	for _, v := range values {
		n := math.Abs(v) / maxAbs
		summand := float64(n*n) - compensation
		preliminary := sum + summand
		compensation = (preliminary - sum) - summand
		sum = preliminary
	}
	return float64(math.Sqrt(sum) * maxAbs)
}

// opsToPath is the generator's opsToPath(set, 1).
func opsToPath(set opSet) string {
	var b strings.Builder
	for _, op := range set.ops {
		d := make([]string, len(op.data))
		for i, v := range op.data {
			d[i] = jsNumber(jsToFixed(v, 1))
		}
		switch op.op {
		case "move":
			b.WriteString("M" + d[0] + " " + d[1] + " ")
		case "bcurveTo":
			b.WriteString("C" + d[0] + " " + d[1] + ", " + d[2] + " " + d[3] + ", " + d[4] + " " + d[5] + " ")
		case "lineTo":
			b.WriteString("L" + d[0] + " " + d[1] + " ")
		}
	}
	return strings.TrimSpace(b.String())
}

// The generator's shapes: each returns its sets, fill first.

func roughRectangle(x, y, w, h float64, o *roughOptions) []opSet {
	points := []point{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}}
	outline := linearPath(points, true, o)
	var sets []opSet
	if o.fill != "" {
		fill := []point{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}}
		if o.fillStyle == "solid" {
			sets = append(sets, solidFillPolygon([][]point{fill}, o))
		} else {
			sets = append(sets, patternFillPolygons([][]point{fill}, o))
		}
	}
	if o.stroke != "none" {
		sets = append(sets, outline)
	}
	return sets
}

func roughEllipse(x, y, w, h float64, o *roughOptions) []opSet {
	params := generateEllipseParams(w, h, o)
	estimated, outline := ellipseWithParams(x, y, o, params)
	var sets []opSet
	if o.fill != "" {
		if o.fillStyle == "solid" {
			_, shape := ellipseWithParams(x, y, o, params)
			shape.typ = "fillPath"
			sets = append(sets, shape)
		} else {
			sets = append(sets, patternFillPolygons([][]point{estimated}, o))
		}
	}
	if o.stroke != "none" {
		sets = append(sets, outline)
	}
	return sets
}

func roughPolygon(points []point, o *roughOptions) []opSet {
	outline := linearPath(points, true, o)
	var sets []opSet
	if o.fill != "" {
		if o.fillStyle == "solid" {
			sets = append(sets, solidFillPolygon([][]point{points}, o))
		} else {
			sets = append(sets, patternFillPolygons([][]point{points}, o))
		}
	}
	if o.stroke != "none" {
		sets = append(sets, outline)
	}
	return sets
}

func roughPath(d string, o *roughOptions) []opSet {
	if d == "" {
		return nil
	}
	d = strings.ReplaceAll(d, "\n", " ")
	d = minusSpace.ReplaceAllString(d, "-")
	segments := parsePath(d)
	hasFill := o.fill != "" && o.fill != "transparent" && o.fill != "none"
	sets := pointsOnPath(segments, 1, (1+o.roughness)/2)
	shape := svgPath(segments, o)
	var out []opSet
	if hasFill {
		switch {
		case o.fillStyle == "solid" && len(sets) == 1:
			f := *o
			f.disableMultiStroke = true
			if o.roughness != 0 {
				f.roughness = o.roughness + o.fillShapeGain
			}
			fill := svgPath(segments, &f)
			var ops []roughOp
			for i, op := range fill.ops {
				if i == 0 || op.op != "move" {
					ops = append(ops, op)
				}
			}
			out = append(out, opSet{"fillPath", ops})
		case o.fillStyle == "solid":
			out = append(out, solidFillPolygon(sets, o))
		default:
			out = append(out, patternFillPolygons(sets, o))
		}
	}
	if o.stroke != "none" {
		out = append(out, shape)
	}
	return out
}
