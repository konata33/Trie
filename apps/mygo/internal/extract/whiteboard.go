package extract

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"

	"trie/internal/web"
)

// Whiteboards: an Excalidraw scene (the JSON file a whiteboard is saved as) drawn into an SVG picture the way Excalidraw
// draws it, the same rough.js strokes from each element's own seed (rough.go). Text uses system fonts. The result still
// goes through SanitizeSVG before it is kept. Rectangles, ellipses, diamonds, lines and arrows (with arrowheads),
// freehand strokes and text are drawn; embedded images and frames are left out.

const maxWhiteboardElements = 2000

var (
	whiteboardColor = regexp.MustCompile(`(?i)^(?:#[\da-f]{3,8}|[a-z]+)$`)
	whiteboardText  = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
)

type element struct{ o *jsObject }

func (e element) num(key string) (float64, bool) {
	n, ok := e.o.get(key).(json.Number)
	if !ok {
		return 0, false
	}
	v, ok := web.NumberValue(n)
	return v, ok
}

func (e element) numOr(key string, fallback float64) float64 {
	if v, ok := e.num(key); ok {
		return v
	}
	return fallback
}

func (e element) str(key string) string { s, _ := e.o.get(key).(string); return s }

func (e element) points() [][2]float64 {
	list, _ := e.o.get("points").([]any)
	out := make([][2]float64, 0, len(list))
	for _, p := range list {
		pair, _ := p.([]any)
		var q [2]float64
		for i := 0; i < 2 && i < len(pair); i++ {
			if n, ok := pair[i].(json.Number); ok {
				q[i], _ = web.NumberValue(n)
			} else {
				q[i] = math.NaN()
			}
		}
		if len(pair) < 2 {
			q[1] = math.NaN()
		}
		out = append(out, q)
	}
	return out
}

func wbColor(value, fallback string) string {
	if value != "" && whiteboardColor.MatchString(value) {
		return value
	}
	return fallback
}

// wbNumber is n(): a value to one decimal, as JavaScript writes it.
func wbNumber(v float64) string { return jsNumber(jsRound(float64(v*10)) / 10) }

// Excalidraw's font numbers: 1 Virgil, 5 Excalifont and 8 Comic Shanns are handwriting; 3 Cascadia is code; the rest sans.
func wbFont(family float64, ok bool) string {
	switch {
	case ok && (family == 1 || family == 5 || family == 8):
		return "'Comic Sans MS', 'Chalkboard SE', cursive"
	case ok && family == 3:
		return "'SF Mono', Menlo, monospace"
	}
	return "Helvetica, Arial, sans-serif"
}

// wbOptions are a shape's drawing options: as Excalidraw's generateRoughOptions, dashes widen the stroke a little, small
// shapes are drawn less roughly, fills are as heavy as half a stroke.
type wbOptions struct {
	rough roughOptions // the generator's options, merged over its defaults
	dash  []float64
}

func wbOptionsFor(e element, closed bool) wbOptions {
	width := e.numOr("strokeWidth", 2)
	style := e.str("strokeStyle")
	if _, set := e.o.values["strokeStyle"]; !set || e.o.get("strokeStyle") == nil {
		style = "solid"
	}
	w, h := e.numOr("width", 0), e.numOr("height", 0)
	big, small := math.Max(w, h), math.Min(w, h)
	roughness := e.numOr("roughness", 1)
	o := defaultRough()
	o.seed = e.numOr("seed", 1)
	o.stroke = wbColor(e.str("strokeColor"), "#1e1e1e")
	o.strokeWidth = width
	var dash []float64
	switch style {
	case "solid":
	case "dashed":
		o.strokeWidth, dash = width+0.5, []float64{8, 8 + width}
	case "dotted":
		o.strokeWidth, dash = width+0.5, []float64{1.5, 6 + width}
	default:
		o.strokeWidth = width + 0.5
	}
	o.disableMultiStroke = style != "solid"
	o.roughness = roughness
	if closed && !(small >= 20 && big >= 50 || small >= 15) {
		divisor := 2.0
		if big < 10 {
			divisor = 3
		}
		o.roughness = math.Min(roughness/divisor, 2.5)
	}
	o.fillWeight, o.hachureGap, o.preserveVx = width/2, float64(width*4), roughness < 2
	if bg := e.str("backgroundColor"); closed && bg != "" && bg != "transparent" {
		o.fill = wbColor(bg, "none")
		o.fillStyle = cmpOr(e.str("fillStyle"), "hachure")
	}
	return wbOptions{rough: o, dash: dash}
}

// draw runs one generator call: a fresh copy of the options, so each shape starts from its seed.
func draw(o wbOptions, shape func(*roughOptions) []opSet) string {
	r := o.rough
	r.random = nil
	return wbPaths(shape(&r), o)
}

func wbPaths(sets []opSet, o wbOptions) string {
	var b strings.Builder
	dash := ""
	if o.dash != nil {
		dash = ` stroke-dasharray="` + jsNumber(o.dash[0]) + " " + jsNumber(o.dash[1]) + `"`
	}
	for _, set := range sets {
		d := opsToPath(set)
		switch set.typ {
		case "path":
			b.WriteString(`<path d="` + d + `" fill="none" stroke="` + o.rough.stroke + `" stroke-width="` + jsNumber(o.rough.strokeWidth) + `" stroke-linecap="round" stroke-linejoin="round"` + dash + `/>`)
		case "fillPath":
			b.WriteString(`<path d="` + d + `" fill="` + o.rough.fill + `" stroke="none"/>`)
		default:
			b.WriteString(`<path d="` + d + `" fill="none" stroke="` + o.rough.fill + `" stroke-width="` + jsNumber(o.rough.fillWeight) + `"/>`)
		}
	}
	return b.String()
}

// corner is Excalidraw's corner radius: a quarter of the shorter side, or for adaptive corners a fixed 32 once the shape
// is large enough.
func corner(size float64, e element) float64 {
	r, ok := e.o.get("roundness").(*jsObject)
	if !ok {
		return 0
	}
	kind, _ := element{r}.num("type")
	if kind == 3 {
		value := element{r}.numOr("value", 32)
		if size <= value/0.25 {
			return float64(size * 0.25)
		}
		return value
	}
	return float64(size * 0.25)
}

func arrowhead(points [][2]float64, end bool, kind string, o wbOptions, length float64) string {
	tip, from := points[0], points[1]
	if end {
		tip, from = points[len(points)-1], points[len(points)-2]
	}
	distance := jsHypot(from[0]-tip[0], from[1]-tip[1])
	if distance == 0 || math.IsNaN(distance) {
		distance = 1
	}
	limit := 15.0
	if kind == "arrow" {
		limit = 25
	}
	size := math.Min(limit, float64(length*0.5))
	back := [2]float64{tip[0] + float64((from[0]-tip[0])/distance*size), tip[1] + float64((from[1]-tip[1])/distance*size)}
	turn := func(angle float64) [2]float64 {
		cos, sin := jsCos(angle), jsSin(angle)
		dx, dy := back[0]-tip[0], back[1]-tip[1]
		return [2]float64{tip[0] + float64(dx*cos) - float64(dy*sin), tip[1] + float64(dx*sin) + float64(dy*cos)}
	}
	degrees := 25.0
	switch kind {
	case "bar":
		degrees = 90
	case "arrow":
		degrees = 20
	}
	spread := float64(degrees*jsPI) / 180
	left, right := turn(-spread), turn(spread)
	solid := o
	solid.rough.fill, solid.rough.fillStyle = o.rough.stroke, "solid"
	switch kind {
	case "triangle":
		return draw(solid, func(r *roughOptions) []opSet { return roughPolygon([]point{tip, left, right}, r) })
	case "dot", "circle":
		return draw(solid, func(r *roughOptions) []opSet { return roughEllipse(tip[0], tip[1], size, size, r) })
	}
	return draw(o, func(r *roughOptions) []opSet { return []opSet{linearPath([]point{left, tip, right}, false, r)} })
}

func wbShape(e element) (string, bool) {
	w, h := e.numOr("width", 0), e.numOr("height", 0)
	switch e.str("type") {
	case "text":
		size := e.numOr("fontSize", 20)
		line := float64(size * e.numOr("lineHeight", 1.25))
		align := map[string]string{"center": "middle", "right": "end"}[e.str("textAlign")]
		if align == "" {
			align = "start"
		}
		x := map[string]float64{"middle": w / 2, "end": w}[align]
		text, isText := e.o.get("text").(string)
		if !isText && e.o.get("text") != nil {
			return "", false // (element.text ?? '').split throws
		}
		var b strings.Builder
		for i, t := range strings.Split(text, "\n") {
			y := float64(float64(i)*line) + (line-size)/2 + float64(size*0.8)
			b.WriteString(`<tspan x="` + wbNumber(x) + `" y="` + wbNumber(y) + `">` + whiteboardText.Replace(t) + `</tspan>`)
		}
		family, ok := e.num("fontFamily")
		return `<text font-family="` + wbFont(family, ok) + `" font-size="` + jsNumber(size) + `" text-anchor="` + align + `" fill="` + wbColor(e.str("strokeColor"), "#1e1e1e") + `">` + b.String() + `</text>`, true
	case "rectangle":
		o := wbOptionsFor(e, true)
		if r := corner(math.Min(w, h), e); r != 0 && !math.IsNaN(r) {
			n := jsNumber
			d := "M " + n(r) + " 0 L " + n(w-r) + " 0 Q " + n(w) + " 0, " + n(w) + " " + n(r) + " L " + n(w) + " " + n(h-r) + " Q " + n(w) + " " + n(h) + ", " + n(w-r) + " " + n(h) +
				" L " + n(r) + " " + n(h) + " Q 0 " + n(h) + ", 0 " + n(h-r) + " L 0 " + n(r) + " Q 0 0, " + n(r) + " 0"
			return draw(o, func(ro *roughOptions) []opSet { return roughPath(d, ro) }), true
		}
		return draw(o, func(ro *roughOptions) []opSet { return roughRectangle(0, 0, w, h, ro) }), true
	case "ellipse":
		o := wbOptionsFor(e, true)
		o.rough.curveFitting = 1
		return draw(o, func(ro *roughOptions) []opSet { return roughEllipse(w/2, h/2, w, h, ro) }), true
	case "diamond":
		o := wbOptionsFor(e, true)
		return draw(o, func(ro *roughOptions) []opSet {
			return roughPolygon([]point{{w / 2, 0}, {w, h / 2}, {w / 2, h}, {0, h / 2}}, ro)
		}), true
	case "freedraw":
		if points := e.points(); len(points) > 0 {
			o := wbOptionsFor(e, false)
			o.rough.roughness = 0
			return draw(o, func(ro *roughOptions) []opSet { return []opSet{roughCurve(points, ro)} }), true
		}
	case "line", "arrow":
		points := e.points()
		if len(points) < 2 {
			break
		}
		o := wbOptionsFor(e, false)
		length := 0.0
		for i := 1; i < len(points); i++ {
			length += jsHypot(points[i][0]-points[i-1][0], points[i][1]-points[i-1][1])
		}
		var out string
		if truthyJS(e.o.get("roundness")) && len(points) > 2 {
			out = draw(o, func(ro *roughOptions) []opSet { return []opSet{roughCurve(points, ro)} })
		} else {
			out = draw(o, func(ro *roughOptions) []opSet { return []opSet{linearPath(points, false, ro)} })
		}
		if e.str("type") == "arrow" {
			single := o
			single.rough.disableMultiStroke = true
			if k := e.str("startArrowhead"); k != "" {
				out += arrowhead(points, false, k, single, length)
			}
			if k := e.str("endArrowhead"); k != "" {
				out += arrowhead(points, true, k, single, length)
			}
		}
		return out, true
	}
	return "", true
}

func wbBounds(e element) [4]float64 {
	x, y := e.numOr("x", 0), e.numOr("y", 0)
	if points := e.points(); len(points) > 0 {
		b := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
		for _, p := range points {
			px, py := x+p[0], y+p[1]
			b[0], b[1], b[2], b[3] = jsMin(b[0], px), jsMin(b[1], py), jsMax(b[2], px), jsMax(b[3], py)
		}
		return b
	}
	return [4]float64{x, y, x + e.numOr("width", 0), y + e.numOr("height", 0)}
}

func jsMin(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Min(a, b)
}

func jsMax(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Max(a, b)
}

// RenderWhiteboard returns the scene as an SVG picture, or "" for anything that is not one.
func RenderWhiteboard(source string) string {
	v, err := parseJS(source)
	if err != nil {
		return ""
	}
	scene, ok := v.(*jsObject)
	list, isList := field(v, "elements").([]any)
	if !ok || scene.get("type") != "excalidraw" || !isList || len(list) > maxWhiteboardElements {
		return ""
	}
	var elements []element
	for _, item := range list {
		o, ok := item.(*jsObject)
		if !ok || truthyJS(o.get("isDeleted")) {
			continue
		}
		e := element{o}
		finite := true
		for _, key := range []string{"x", "y", "width", "height"} {
			if v, ok := e.num(key); !ok || math.IsInf(v, 0) || math.IsNaN(v) {
				finite = false
			}
		}
		if finite {
			elements = append(elements, e)
		}
	}
	var drawn []string
	for _, e := range elements {
		body, ok := wbShape(e)
		if !ok {
			return ""
		}
		if body == "" {
			continue
		}
		rotate := ""
		if angle, ok := e.num("angle"); ok && angle != 0 {
			rotate = " rotate(" + wbNumber(float64(angle*180)/jsPI) + " " + wbNumber(e.numOr("width", 0)/2) + " " + wbNumber(e.numOr("height", 0)/2) + ")"
		}
		opacity := ""
		if o, ok := e.num("opacity"); ok && o < 100 {
			opacity = ` opacity="` + jsNumber(math.Max(0, o)/100) + `"`
		} else if v, present := e.o.values["opacity"]; present && v == nil {
			opacity = ` opacity="0"` // null < 100, and Math.max(0, null) is 0
		}
		drawn = append(drawn, `<g transform="translate(`+wbNumber(e.numOr("x", 0))+" "+wbNumber(e.numOr("y", 0))+")"+rotate+`"`+opacity+`>`+body+`</g>`)
	}
	if len(drawn) == 0 {
		return ""
	}
	// Room around the drawing for strokes, arrowheads and the roughness of the lines.
	const pad = 20
	box := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, e := range elements {
		b := wbBounds(e)
		box[0], box[1], box[2], box[3] = jsMin(box[0], b[0]), jsMin(box[1], b[1]), jsMax(box[2], b[2]), jsMax(box[3], b[3])
	}
	left, top := box[0]-pad, box[1]-pad
	width, height := box[2]+pad-left, box[3]+pad-top
	background := "#ffffff"
	if s, ok := field(field(v, "appState"), "viewBackgroundColor").(string); ok {
		background = wbColor(s, "#ffffff")
	}
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="` + wbNumber(left) + " " + wbNumber(top) + " " + wbNumber(width) + " " + wbNumber(height) + `" width="` + wbNumber(width) + `" height="` + wbNumber(height) + `">` +
		`<rect x="` + wbNumber(left) + `" y="` + wbNumber(top) + `" width="` + wbNumber(width) + `" height="` + wbNumber(height) + `" fill="` + background + `"/>` + strings.Join(drawn, "") + `</svg>`
}
