package extract

import (
	"encoding/json"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// whiteboard.test.ts: a scene draws into a picture that keeps its shapes, fills and words through the SVG sanitizer.
func TestWhiteboardDrawsScene(t *testing.T) {
	scene := func(elements string) string {
		return `{"type":"excalidraw","version":2,"elements":[` + elements + `],"appState":{"viewBackgroundColor":"#ffffff"}}`
	}
	base := `"angle":0,"strokeColor":"#1e1e1e","backgroundColor":"transparent","fillStyle":"solid","strokeWidth":2,"strokeStyle":"solid","roughness":1,"opacity":100,"seed":7`
	svg := RenderWhiteboard(scene(strings.Join([]string{
		`{` + base + `,"type":"rectangle","x":0,"y":0,"width":300,"height":80,"backgroundColor":"#a5d8ff","roundness":{"type":3}}`,
		`{` + base + `,"type":"rectangle","x":0,"y":160,"width":300,"height":80,"strokeStyle":"dashed","roundness":null}`,
		`{` + base + `,"type":"arrow","x":150,"y":80,"width":0,"height":80,"points":[[0,0],[0,80]],"roundness":{"type":2},"endArrowhead":"arrow"}`,
		`{` + base + `,"type":"text","x":20,"y":20,"width":260,"height":26,"fontSize":21,"fontFamily":2,"textAlign":"left","lineHeight":1.25,"text":"LodyKit (Swift) & <packages>\nsecond line"}`,
		`{` + base + `,"type":"text","x":0,"y":0,"width":10,"height":10,"text":"deleted","isDeleted":true}`,
	}, ",")))
	must(t, svg != "", "no picture")
	clean := SanitizeSVG(svg)
	must(t, clean != "", "passes the sanitizer whole")
	for _, want := range []string{`fill="#a5d8ff"`, `stroke-dasharray=`, `LodyKit (Swift) &amp; &lt;packages&gt;`, `second line`, `viewBox="-20 -20 340 280"`} {
		must(t, strings.Contains(clean, want), "%s missing", want)
	}
	must(t, !strings.Contains(clean, "deleted"), "a deleted element is drawn")
	must(t, RenderWhiteboard(svg) == "" && RenderWhiteboard(`{"type":"other","elements":[]}`) == "", "a picture or another kind read as a scene")
}

var svgNumber = regexp.MustCompile(`-?\d+(?:\.\d+)?(?:e[-+]?\d+)?`)

// TestWhiteboardMatchesTS draws the scenes ts-whiteboard.ts drew with the TS renderer (EXTRACT_PORT/whiteboard/scenes.json)
// and compares the SVG text; numbers that differ are counted with their largest difference.
func TestWhiteboardMatchesTS(t *testing.T) {
	dir := portDir(t)
	var scenes []struct{ Name, Source, Svg string }
	if err := readJSON(filepath.Join(dir, "whiteboard", "scenes.json"), &scenes); err != nil {
		t.Fatal(err)
	}
	same := 0
	for _, s := range scenes {
		want := s.Svg
		if want == "throws" {
			want = ""
		}
		got := RenderWhiteboard(s.Source)
		if s.Name == "seedless" { // seed 0 draws from Math.random on both sides
			must(t, got != "", "seedless scene not drawn")
			continue
		}
		if got == want {
			same++
			continue
		}
		a, b := svgNumber.FindAllString(want, -1), svgNumber.FindAllString(got, -1)
		structure := svgNumber.ReplaceAllString(want, "#") == svgNumber.ReplaceAllString(got, "#")
		differ, worst := 0, 0.0
		for i := 0; i < min(len(a), len(b)); i++ {
			if a[i] != b[i] {
				x, _ := strconv.ParseFloat(a[i], 64)
				y, _ := strconv.ParseFloat(b[i], 64)
				differ++
				worst = math.Max(worst, math.Abs(x-y))
			}
		}
		t.Errorf("%s: structure same %v, %d of %d numbers differ, by at most %g\n%s", s.Name, structure, differ, len(a), worst, firstDiff(want, got))
	}
	t.Logf("%d of %d scenes identical", same, len(scenes)-1)
}

// The values where Go's math.Sin and math.Cos differ from V8 and moved roughness-0 strokes by 0.1 (see jsmath.go).
func TestJSTrigExactValues(t *testing.T) {
	must(t, jsCos(2*jsPI) == 1 && jsSin(jsPI) == 1.2246467991473532e-16 && jsCos(jsPI/2) == 6.123233995736766e-17 && jsSin(-jsPI/2) == -1, "%v %v %v", jsCos(2*jsPI), jsSin(jsPI), jsCos(jsPI/2))
}

// truthyJS follows JavaScript: an element with "isDeleted": false is drawn, as in every real Excalidraw scene.
func TestTruthyJSMatchesJS(t *testing.T) {
	for _, c := range []struct {
		v    any
		want bool
	}{
		{nil, false}, {false, false}, {true, true}, {"", false}, {"0", true}, {"false", true},
		{json.Number("0"), false}, {json.Number("-0"), false}, {json.Number("0.0"), false}, {json.Number("2"), true},
		{[]any{}, true},
	} {
		if got := truthyJS(c.v); got != c.want {
			t.Errorf("truthyJS(%#v) = %v, want %v", c.v, got, c.want)
		}
	}
}
