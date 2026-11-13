package reader

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"regexp"
	"strings"
	"testing"
	"time"

	"trie/internal/theme"

	"github.com/egoist/mygo/ui"
)

// open shows the fixture's article whose title starts with title in a
// window w×h DIPs, the reading column's width at 724.
func open(t *testing.T, title string, w, h int) (*Reader, *ui.Tester) {
	t.Helper()
	for _, a := range fixture(t) {
		if !strings.HasPrefix(a.Title, title) {
			continue
		}
		c, err := a.Content()
		if err != nil {
			t.Fatal(err)
		}
		r := NewReader(a, c, Prepare(c))
		th := theme.UI()
		return r, ui.NewTester(func(ctx *ui.Context) { ctx.SetTheme(th); r.View(ctx) }, w, h)
	}
	t.Fatalf("no article titled %q", title)
	return nil, nil
}

// text returns the first text of the frame holding s.
func text(tt *ui.Tester, s string) (string, bool) {
	for _, x := range tt.Texts() {
		if strings.Contains(x, s) {
			return x, true
		}
	}
	return "", false
}

func box(t *testing.T, tt *ui.Tester, s string) ui.Rect {
	t.Helper()
	x, ok := text(tt, s)
	if !ok {
		t.Fatalf("no text holding %q", s)
	}
	r, _ := tt.Find(x)
	return r
}

// The real articles show their title, paragraphs, table cells and code.
func TestArticlesShowTheirText(t *testing.T) {
	for _, tc := range []struct {
		title string
		holds []string
	}{
		{"十个月，给 LobeHub 换掉 Ant Design", []string{"十个月，给 LobeHub 换掉 Ant Design：向 Base UI 与 StyleX 的渐进式迁移",
			"我入职 LobeHub 的第一个月做了一轮性能优化", "antd Form", "当前挂载的 Form.Item 的值", "起点是一轮性能优化"}},
		// Stored without a document: made from its HTML, nine tables.
		{"UIKitCore 逆向分析报告", []string{"UIKitCore 逆向分析报告", "最近为了给自己的 iOS 界面实现找一份可靠的参照", "项目", "Runtime", "研究基线"}},
		{"Async, Sync, in Between", []string{"In modern programming, the function coloring problem", "export function markdownToHtml(markdown) {\n  const ast = parse(markdown)"}},
		{"Pluggable, Extensible, and Playful DevTools", []string{"// my-tool.ts\nimport { defineDevframe } from 'devframe'"}},
		{"Categorize Your Dependencies", []string{"pnpm-workspace.yaml"}},
	} {
		t.Run(tc.title, func(t *testing.T) {
			_, tt := open(t, tc.title, 724, 900)
			for _, s := range tc.holds {
				if _, ok := text(tt, s); !ok {
					t.Errorf("no text holds %q", s)
				}
			}
		})
	}
}

func TestPicturesOfTheFixtureDecode(t *testing.T) {
	for title, want := range map[string]int{"雨のち晴れ": 17, "Pluggable, Extensible, and Playful DevTools": 12, "Migrating from React Native to UIKit": 14, "十个月，给 LobeHub": 2} {
		for _, a := range fixture(t) {
			if !strings.HasPrefix(a.Title, title) {
				continue
			}
			c, _ := a.Content()
			b := Prepare(c)
			shown := 0
			for _, p := range b.pics {
				if p.img != nil {
					shown++
				} else {
					t.Errorf("%s: %s did not decode: %v", title, p.src[:min(40, len(p.src))], p.err)
				}
			}
			if shown != want {
				t.Errorf("%s: %d pictures shown, want %d", title, shown, want)
			}
		}
	}
}

// A drag selects across paragraphs, a heading and a list; Copy and a clip
// take the same text, broken as the browser breaks it.
func TestSelectionCrossesBlocks(t *testing.T) {
	r, tt := open(t, "Async, Sync, in Between", 724, 2400)
	from := box(t, tt, "The Coloring Problem")
	to := box(t, tt, "This restriction forces developers")
	tt.Press(from.X+1, from.Y+from.H/2)
	tt.Move(to.X+60, to.Y+to.H-8)
	tt.Release(to.X+60, to.Y+to.H-8)
	cl, ok := r.Selection()
	if !ok {
		t.Fatal("nothing selected")
	}
	for _, s := range []string{
		"The Coloring Problem\n\nIn modern programming, the function coloring problem isn't new.",
		"freely.\n\nFor instance, in JavaScript:\n\nAn async function can call both sync and other async functions.\nA sync function, however,",
		"to async.\n\nThis restriction",
	} {
		if !strings.Contains(cl.Text, s) {
			t.Errorf("selection lacks %q:\n%s", s, cl.Text)
		}
	}
	if cl.UnitID == nil || *cl.UnitID != "reader-unit-63920ee94b29b239966abc90" {
		t.Errorf("unit %v, want the heading's", cl.UnitID)
	}
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); strings.TrimSpace(got) != cl.Text {
		t.Errorf("Copy wrote %q, the clip has %q", got, cl.Text)
	}
}

// pick finds three top-level blocks in a row of the kinds given: the
// paragraphs on either side of a table, a picture or code.
func pick(t *testing.T, r *Reader, kinds ...Kind) []*block {
	t.Helper()
	bs := r.Body.blocks
	for i := 0; i+len(kinds) <= len(bs); i++ {
		ok := true
		for j, k := range kinds {
			b := bs[i+j]
			if b.kind == Figure && len(b.children) > 0 {
				b = b.children[0]
			}
			ok = ok && b.kind == k
		}
		if ok {
			return bs[i : i+len(kinds)]
		}
	}
	t.Fatalf("no blocks of kinds %v in a row", kinds)
	return nil
}

func runsText(b *block) string {
	var sb strings.Builder
	for _, r := range b.runs {
		sb.WriteString(r.text)
	}
	return sb.String()
}

// A selection crosses tables, pictures and code; the cells come
// tab-separated, a picture gives no text, the code all of its own.
func TestSelectionCrossesTablesPicturesAndCode(t *testing.T) {
	for _, tc := range []struct {
		title string
		kind  Kind
		want  func(mid *block) string
	}{
		{"十个月，给 LobeHub", Table, func(*block) string {
			return "antd Form\tbase-ui Form\n提交时返回\t当前挂载的 Form.Item 的值\t整个表单 store\n"
		}},
		{"雨のち晴れ", Picture, nil},
		{"Async, Sync, in Between", Code, func(mid *block) string { return "\n" + strings.TrimSuffix(mid.code.code, "\n") + "\n" }},
	} {
		t.Run(tc.title, func(t *testing.T) {
			r, tt := open(t, tc.title, 724, 900)
			bs := pick(t, r, Paragraph, tc.kind, Paragraph)
			before, after := runsText(bs[0]), runsText(bs[2])
			r.Scroll.Y += box(t, tt, before).Y - 40
			tt.Frame()
			from, to := box(t, tt, before), box(t, tt, after)
			if to.Y+to.H > 900 {
				t.Skipf("%s: the blocks do not fit one screen", tc.title)
			}
			tt.Press(from.X+1, from.Y+8)
			tt.Move(to.X+40, to.Y+8)
			tt.Release(to.X+40, to.Y+8)
			cl, ok := r.Selection()
			if !ok {
				t.Fatal("nothing selected")
			}
			if !strings.HasPrefix(cl.Text, strings.TrimSpace(before)) {
				t.Errorf("the selection does not start with the paragraph before:\n%s", cl.Text)
			}
			if tc.want != nil && !strings.Contains(cl.Text, tc.want(bs[1])) {
				t.Errorf("the selection lacks %q:\n%s", tc.want(bs[1]), cl.Text)
			}
			if tc.kind == Picture && !strings.Contains(cl.Text, strings.TrimSpace(before)+"\n\n") {
				t.Errorf("the picture put text in the selection:\n%s", cl.Text)
			}
			if n := len([]rune(after)); !strings.Contains(cl.Text, string([]rune(after)[:min(2, n)])) {
				t.Errorf("the selection does not reach the paragraph after:\n%s", cl.Text)
			}
		})
	}
}

// ⌘E's clip is what the store's add-clip takes, marked in the body; the
// selection lets go.
func TestKeepMakesAClip(t *testing.T) {
	r, tt := open(t, "十个月，给 LobeHub", 724, 900)
	p := box(t, tt, "我入职 LobeHub 的第一个月做了一轮性能优化")
	tt.Press(p.X+1, p.Y+8)
	tt.Move(p.X+300, p.Y+40)
	tt.Release(p.X+300, p.Y+40)
	cl, ok := r.Keep(time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))
	if !ok {
		t.Fatal("no clip")
	}
	data, _ := json.Marshal(cl)
	var input map[string]any
	json.Unmarshal(data, &input)
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuid.MatchString(cl.ID) || input["articleID"] != r.Article.ID || input["note"] != "" ||
		input["createdAt"] != "2026-11-09T12:00:00.000Z" || !strings.HasPrefix(cl.Text, "我入职 LobeHub") {
		t.Errorf("clip %s", data)
	}
	if rev, _ := input["contentRevision"].(string); !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(rev) || rev != r.Body.Revision {
		t.Errorf("contentRevision %v, want the document's %s", input["contentRevision"], r.Body.Revision)
	}
	if unit, _ := input["unitID"].(string); !strings.HasPrefix(unit, "reader-unit-") || cl.Start.UnitID != unit || cl.Start.Offset != 0 {
		t.Errorf("unitID %v, start %+v", input["unitID"], cl.Start)
	}
	tt.Frame()
	if _, ok := r.Selection(); ok {
		t.Error("the selection stayed after the clip was kept")
	}
	marked := 0
	for _, s := range r.segments(cl.parts[0].b) {
		if s.marked {
			marked += len([]rune(s.text))
		}
	}
	if marked != cl.parts[0].to-cl.parts[0].from {
		t.Errorf("%d runes marked, the clip holds %d", marked, cl.parts[0].to-cl.parts[0].from)
	}
}

// A reading position measured in one reader puts a new reader of the same
// content where the old one was.
func TestReadingPositionRestores(t *testing.T) {
	r, tt := open(t, "雨のち晴れ", 724, 820)
	r.Scroll.Y = 6000
	tt.Frame()
	tt.Frame()
	pos := r.Position()
	if pos.UnitID == nil || pos.ContentVersion != r.Body.Revision || pos.Progress <= 0 || pos.Progress >= 1 || pos.ScrollTop != 6000 {
		t.Fatalf("position %+v", pos)
	}
	again, tt2 := open(t, "雨のち晴れ", 724, 820)
	again.RestorePosition(pos)
	tt2.Frame()
	if d := again.Scroll.Y - 6000; d < -1 || d > 1 {
		t.Errorf("restored to %.1f, was at 6000", again.Scroll.Y)
	}
	if p := again.Position(); p.UnitID == nil || *p.UnitID != *pos.UnitID {
		t.Errorf("restored position %+v, saved %+v", p, pos)
	}
}

func TestLineBreaking(t *testing.T) {
	lines := func(text string, width float32) []string {
		runs := scripts([]run{{text: text}})
		segs := make([]segment, len(runs))
		for i, r := range runs {
			segs[i] = segment{run: r}
		}
		l := ui.ShapeRichTextWith(spans(segs, 17), ui.Font{Family: theme.Sans, Size: 17}, ui.TextShape{Width: width})
		var out []string
		rs := []rune(text)
		for _, ln := range l.Lines() {
			if ln.Rect.W > width+0.5 && !strings.HasSuffix(string(rs[ln.Start:ln.End]), " ") {
				t.Errorf("a line %.1f wide in %.0f: %q", ln.Rect.W, width, string(rs[ln.Start:ln.End]))
			}
			out = append(out, string(rs[ln.Start:ln.End]))
		}
		return out
	}
	// No line starts with closing punctuation or ends with opening, at any
	// width the reader's sizes give.
	text := "这些问题单独看都不大，但只凭截图很容易得到一个“看起来差不多”的实现。真正进入 UIKitCore 后会发现，屏幕上的结果往往由几套互相独立的系统叠加而成：字体来自 Dynamic Type style，标题自身使用 gaussian blur，滚动边缘使用 variable blur（Liquid Glass 的抬升又由另一组 trait、spring 和 lens 状态驱动）。《雨のち晴れ》「引用」『书名』——破折号……省略号。"
	for w := float32(200); w <= 660; w += 7 {
		for _, ln := range lines(text, w) {
			if r := []rune(strings.TrimSpace(ln)); len(r) > 0 && strings.ContainsRune("，。、；：？！）」』》〉】”’…", r[0]) {
				t.Errorf("at %.0f a line starts with %q: %q", w, r[0], ln)
			}
			if r := []rune(strings.TrimSpace(ln)); len(r) > 0 && strings.ContainsRune("（「『《〈【“‘", r[len(r)-1]) {
				t.Errorf("at %.0f a line ends with %q: %q", w, r[len(r)-1], ln)
			}
		}
	}
	// A long URL breaks within the column rather than overflowing it.
	url := lines("见 https://github.com/lobehub/lobe-chat/pull/20317/files#diff-4c6b8e3f0a7c1d2e9b5f6a8d0c3e1f2a4b6c8d0e2f4a6b8c0d2e4f6a8b0c2d4e 的讨论", 300)
	if len(url) < 3 {
		t.Errorf("the URL took %d lines at 300: %q", len(url), url)
	}
	// Han meeting Latin with no space between takes 1/8 em after it.
	var spaced []string
	for _, r := range scripts([]run{{text: "用React写，"}, {text: "v2"}}) {
		if r.style&autospace != 0 {
			spaced = append(spaced, r.text)
		}
	}
	if strings.Join(spaced, "|") != "用|t" {
		t.Errorf("1/8 em after %q, want after 用 and t (not after the comma before v2)", spaced)
	}
}

// Chinese is drawn in PingFang SC at the size given: pixel for pixel the
// same as naming it.
func TestChineseIsPingFangSC(t *testing.T) {
	const s = "中文正文，标点。引号「」"
	render := func(build func(c *ui.Context)) *image.RGBA {
		return ui.Render(func(c *ui.Context) {
			ui.Column(c).Fill().Background(theme.Window).Padding(8).Font(theme.Sans).FontSize(17).TextColor(theme.Text).Children(func() { build(c) })
		}, 240, 40, 2)
	}
	ours := render(func(c *ui.Context) {
		runs := scripts([]run{{text: s}})
		segs := make([]segment, len(runs))
		for i, r := range runs {
			segs[i] = segment{run: r}
		}
		ui.RichText(c, spans(segs, 17)...)
	})
	pingfang := render(func(c *ui.Context) { ui.Text(c, s).Font("PingFang SC") })
	cascade := render(func(c *ui.Context) { ui.Text(c, s) })
	if !bytes.Equal(ours.Pix, pingfang.Pix) {
		t.Error("the reader's Chinese is not drawn as PingFang SC is")
	}
	if bytes.Equal(cascade.Pix, pingfang.Pix) {
		t.Log("MyGo's own fallback draws PingFang SC as it is")
	} else {
		t.Log("MyGo's own fallback (system-ui) draws Chinese otherwise than PingFang SC at 17: the reader names it")
	}
}

// avif is a 32×24 AVIF picture (SVT-AV1), which MyGo does not decode.
const avif = "AAAAIGZ0eXBhdmlmAAAAAGF2aWZtaWYxbWlhZk1BMUIAAAD5bWV0YQAAAAAAAAAvaGRscgAAAAAAAAAAcGljdAAAAAAAAAAAAAAAAFBpY3R1cmVIYW5kbGVyAAAAAA5waXRtAAAAAAABAAAAHmlsb2MAAAAARAAAAQABAAAAAQAAASEAAAAjAAAAKGlpbmYAAAAAAAEAAAAaaW5mZQIAAAAAAQAAYXYwMUNvbG9yAAAAAGppcHJwAAAAS2lwY28AAAAUaXNwZQAAAAAAAAAgAAAAGAAAABBwaXhpAAAAAAMICAgAAAAMYXYxQ4EADAAAAAATY29scm5jbHgAAgACAAIAAAAAF2lwbWEAAAAAAAAAAQABBAECgwQAAAArbWRhdAoKAgAABVE/cavkATIVEACXgBBAggAAEAAoJXand8Bwt29w"

// Saved pictures of each format the cleaner keeps: PNG, JPEG, WebP, SVG and
// GIF show, a GIF of several frames plays; AVIF and remote pictures show as
// not loaded, with 重试.
func TestPictureFormats(t *testing.T) {
	plain := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for i := range plain.Pix {
		plain.Pix[i] = 0xC0
	}
	var pngData, jpegData, gifData bytes.Buffer
	png.Encode(&pngData, plain)
	jpeg.Encode(&jpegData, plain, nil)
	frames := &gif.GIF{}
	for _, c := range []color.Color{color.White, color.Black} {
		f := image.NewPaletted(image.Rect(0, 0, 40, 20), color.Palette{color.White, color.Black})
		for i := range f.Pix {
			f.Pix[i] = uint8(f.Palette.Index(c))
		}
		frames.Image, frames.Delay = append(frames.Image, f), append(frames.Delay, 20)
	}
	gif.EncodeAll(&gifData, frames)
	webp := ""
	for _, a := range fixture(t) {
		if c, _ := a.Content(); strings.Contains(c.HTML, "data:image/webp;base64,") {
			webp = regexp.MustCompile(`data:image/webp;base64,[A-Za-z0-9+/=]+`).FindString(c.HTML)
			break
		}
	}
	svg := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 50" width="200" height="50"><rect width="200" height="50" fill="#fff"/><g transform="translate(10 0)"><text x="90" y="30" font-size="20" text-anchor="middle" fill="#123456">标签 Label</text></g></svg>`))
	uri := func(kind string, data []byte) string {
		return "data:image/" + kind + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	html := `<p>pictures</p>` +
		`<img src="` + uri("png", pngData.Bytes()) + `" alt="png">` +
		`<img src="` + uri("jpeg", jpegData.Bytes()) + `" alt="jpeg">` +
		`<img src="` + webp + `" alt="webp">` +
		`<img src="data:image/svg+xml;base64,` + svg + `" alt="svg">` +
		`<img src="` + uri("gif", gifData.Bytes()) + `" alt="gif">` +
		`<img src="data:image/avif;base64,` + avif + `" alt="avif" width="320" height="240">` +
		`<img src="https://example.com/a.png" alt="remote">`
	body := Prepare(&Content{Status: "ready", HTML: html})
	for _, p := range body.pics {
		switch p.alt {
		case "avif", "remote":
			if p.img != nil || p.err == nil {
				t.Errorf("%s decoded", p.alt)
			}
		default:
			if p.img == nil {
				t.Errorf("%s did not decode: %v", p.alt, p.err)
			}
		}
		if (p.alt == "gif") != (p.player != nil) {
			t.Errorf("%s: player %v", p.alt, p.player != nil)
		}
		if p.alt == "svg" && (len(p.texts) != 1 || p.texts[0].text != "标签 Label" || p.texts[0].x != 100 || p.texts[0].anchor != "middle") {
			t.Errorf("svg texts %+v", p.texts)
		}
	}
	r := NewReader(&Article{ID: "formats", Title: "formats"}, &Content{Status: "ready"}, body)
	r.OnRetry = func() {}
	th := theme.UI()
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(th); r.View(c) }, 724, 2400)
	for _, name := range []string{"图片没能载入：avif", "图片没能载入：remote"} {
		if _, ok := tt.Find(name); !ok {
			t.Errorf("no %s", name)
		}
	}
	if n := strings.Count(strings.Join(tt.Texts(), "\n"), "重试"); n != 2 {
		t.Errorf("%d 重试 buttons, want 2", n)
	}
	// The GIF moves on as the clock does.
	g, _ := tt.Find("gif")
	at := func() color.RGBA { return tt.Image().RGBAAt(int(g.X+g.W/2), int(g.Y+g.H/2)) }
	first := at()
	tt.Advance(250 * time.Millisecond)
	if at() == first {
		t.Errorf("the GIF shows %v after 250ms, as at first", first)
	}
}
