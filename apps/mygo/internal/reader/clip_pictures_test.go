package reader

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"

	"trie/internal/theme"

	"github.com/egoist/mygo/ui"
)

// A clip keeps the pictures of its passage by unit, where they stand in its
// text; one that begins in a caption takes the caption's picture along; and
// ClipPictures decodes them.
func TestClipKeepsItsPictures(t *testing.T) {
	var pngData bytes.Buffer
	png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 8, 4)))
	src := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData.Bytes())
	el := func(tag, unit string, props map[string]any, children ...map[string]any) map[string]any {
		if props == nil {
			props = map[string]any{}
		}
		props["dataReaderUnit"] = unit
		return map[string]any{"type": "element", "tagName": tag, "properties": props, "children": children}
	}
	txt := func(s string) map[string]any { return map[string]any{"type": "text", "value": s} }
	root := map[string]any{"type": "root", "children": []map[string]any{
		el("p", "reader-unit-p1", nil, txt("The words before the chart run on.")),
		el("figure", "reader-unit-fig", nil,
			el("img", "reader-unit-img", map[string]any{"src": src, "alt": "A chart", "width": 200.0, "height": 100.0}),
			el("figcaption", "reader-unit-cap", nil, txt("Error bars reflect 95% confidence intervals."))),
		el("p", "reader-unit-p2", nil, txt("The words after the chart run on.")),
	}}
	data, _ := json.Marshal(map[string]any{"formatVersion": 4, "document": map[string]any{"version": 1, "contentRevision": strings.Repeat("a", 64), "root": root}})
	var c Content
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	r := NewReader(&Article{ID: "a", Title: "t"}, &c, Prepare(&c))
	th := theme.UI()
	tt := ui.NewTester(func(ctx *ui.Context) { ctx.SetTheme(th); r.View(ctx) }, 724, 900)
	drag := func(from, to string, dx float32) Clip {
		t.Helper()
		a, b := box(t, tt, from), box(t, tt, to)
		tt.Press(a.X+1, a.Y+a.H/2)
		tt.Move(b.X+dx, b.Y+b.H/2)
		tt.Release(b.X+dx, b.Y+b.H/2)
		cl, ok := r.Selection()
		if !ok {
			t.Fatal("nothing selected")
		}
		return cl
	}

	cl := drag("The words before", "The words after", 60)
	at := strings.Index(cl.Text, "Error bars") // the picture stands before its caption, the next words selected
	if len(cl.Pictures) != 1 || cl.Pictures[0].Unit != "reader-unit-img" || at < 0 || cl.Pictures[0].At != len([]rune(cl.Text[:at])) {
		t.Fatalf("pictures %+v in %q", cl.Pictures, cl.Text)
	}

	cl = drag("Error bars", "Error bars", 60)
	if !strings.HasPrefix(cl.Text, "Error") || len(cl.Pictures) != 1 || cl.Pictures[0] != (ClipPicture{"reader-unit-img", 0}) {
		t.Fatalf("pictures %+v in %q", cl.Pictures, cl.Text)
	}

	pics := ClipPictures(&c, []string{"reader-unit-img", "reader-unit-gone"})
	p, ok := pics["reader-unit-img"]
	if len(pics) != 1 || !ok || p.Err != nil || p.Img == nil || p.W != 200 || p.H != 100 || p.Alt != "A chart" || p.Src != src ||
		p.Caption != "Error bars reflect 95% confidence intervals." {
		t.Fatalf("pictures %+v", pics)
	}
}
