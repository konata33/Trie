package extract

import (
	"bytes"
	"cmp"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"trie/internal/library"

	"golang.org/x/net/html"
)

// TestPortBench runs the port over the selection bench's corpus and compares it with the TS output:
//
//	EXTRACT_PORT=$PWD/../../.context/mygo/extract-port go test ./internal/extract -run TestPortBench -v
//
// The corpus is ../extract-spike (pages/, out/ts-full/ from ts-run.ts); out/ts-doc/ (ts-doc.ts) adds the TS documents
// and code. Where a page differs, Mozilla Readability's own output for the same document (out/readability-ts/, from
// readability-ts.ts over what EXTRACT_PORT_DUMP=1 writes to out/readability-in/) is fed to the Go pipeline in place of
// readeck's: if the page then matches, the difference is the Readability layer's. Writes out/go/ and report/.
// EXTRACT_PORT must be an absolute path to an existing directory.
func TestPortBench(t *testing.T) {
	dir := portDir(t)
	corpus := filepath.Join(dir, "..", "extract-spike")
	files, _ := filepath.Glob(filepath.Join(corpus, "pages", "*.json"))
	if len(files) == 0 {
		t.Fatalf("no pages in %s", corpus)
	}
	only := os.Getenv("EXTRACT_PORT_ONLY")
	defer func() { parseArticle = readability }()
	var rows []benchRow
	for _, f := range files {
		var p page
		if err := readJSON(f, &p); err != nil {
			t.Fatal(err)
		}
		if p.Status != 200 || p.HTML == "" || only != "" && !strings.Contains(p.ID, only) {
			continue
		}
		var ts tsPage
		var doc tsDoc
		if err := readJSON(filepath.Join(corpus, "out", "ts-full", p.ID+".json"), &ts); err != nil {
			t.Fatal(err)
		}
		if err := readJSON(filepath.Join(dir, "out", "ts-doc", p.ID+".json"), &doc); err != nil {
			t.Fatalf("%v (run ts-doc.ts first)", err)
		}
		dump := filepath.Join(dir, "out", "readability-in", p.ID+".json")
		parseArticle = func(d *html.Node, base string) (string, string) {
			if os.Getenv("EXTRACT_PORT_DUMP") != "" {
				writeJSON(t, dump, map[string]string{"url": base, "html": outerHTML(d)})
			}
			return readability(d, base)
		}
		start := time.Now()
		out := runPort(p)
		r := compareRun(p, ts, doc, out)
		r.Goms = float64(time.Since(start).Microseconds()) / 1000
		r.TSms = ts.MS
		parseArticle = readability
		// The same page with Mozilla's Readability output for the same document.
		var mozilla struct {
			Content, Title string
			None           bool
		}
		if !r.Same() && readJSON(filepath.Join(dir, "out", "readability-ts", p.ID+".json"), &mozilla) == nil {
			parseArticle = func(*html.Node, string) (string, string) { return mozilla.Content, mozilla.Title }
			m := compareRun(p, ts, doc, runPort(p))
			r.Layer = ptr(m.Same())
			parseArticle = readability
		}
		// The intended difference (articleRoot's share of the page): with it off, the page is as the TS has it.
		if !r.Same() {
			rulesOff = map[string]bool{"root-share": true}
			m := compareRun(p, ts, doc, runPort(p))
			rulesOff = nil
			if m.Same() {
				r.Intended = true
			}
		}
		writeJSON(t, filepath.Join(dir, "out", "go", p.ID+".json"), out)
		rows = append(rows, r)
	}
	slices.SortFunc(rows, func(a, b benchRow) int { return strings.Compare(a.ID, b.ID) })
	report := benchReport(rows)
	if err := os.MkdirAll(filepath.Join(dir, "report"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report", "bench.md"), []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(dir, "report", "rows.json"), rows)
	t.Log("\n" + report[:strings.Index(report, "\n## ")])
}

// portDir is EXTRACT_PORT, which must be an absolute path to an existing directory (anything else fails the test rather
// than write into the package); the test is skipped while it, or any of the switches named, is unset.
func portDir(t *testing.T, switches ...string) string {
	t.Helper()
	dir := os.Getenv("EXTRACT_PORT")
	for _, s := range append([]string{"EXTRACT_PORT"}, switches...) {
		if os.Getenv(s) == "" {
			t.Skip(s + " is not set")
		}
	}
	if info, err := os.Stat(dir); !filepath.IsAbs(dir) || err != nil || !info.IsDir() {
		t.Fatalf("EXTRACT_PORT=%q is not an absolute path to a directory", dir)
	}
	return dir
}

type tsPage struct {
	Status     string   `json:"status"`
	Message    *string  `json:"message"`
	TextLength int      `json:"textLength"`
	Extracted  string   `json:"extracted"`
	HTML       string   `json:"html"`
	Images     []string `json:"images"`
	Headings   []string `json:"headings"`
	CodeBlocks []struct {
		Language string `json:"language"`
		Length   int    `json:"length"`
	} `json:"codeBlocks"`
	Work *struct {
		Sections int  `json:"sections"`
		Poster   bool `json:"poster"`
		Video    bool `json:"video"`
	} `json:"work"`
	MS float64 `json:"ms"`
}

type tsDoc struct {
	Status     string              `json:"status"`
	CodeBlocks []library.CodeBlock `json:"codeBlocks"`
	Document   *struct {
		ContentRevision string          `json:"contentRevision"`
		UnitsRevision   string          `json:"unitsRevision"`
		Provenance      string          `json:"provenance"`
		Root            json.RawMessage `json:"root"`
		Units           json.RawMessage `json:"units"`
		CodeTokenIDs    []string        `json:"codeTokenIDs"`
	} `json:"document"`
}

// portRun is the whole Go pipeline for one page, in reader-service.ts order, with ts-run.ts's stand-in pictures.
type portRun struct {
	Extracted Extracted       `json:"extracted"`
	Formatted *Formatted      `json:"formatted,omitempty"`
	HTML      string          `json:"html"` // formatted, pictures mapped back as ts-run.ts writes them
	Root      json.RawMessage `json:"root,omitempty"`
	Error     string          `json:"error,omitempty"`
}

var standIn = regexp.MustCompile(`src="(data:[^"]+)"`)

func runPort(p page) portRun {
	x := ExtractPage(p.HTML, p.FinalURL, true)
	out := portRun{Extracted: x}
	if x.Status == "unavailable" {
		return out
	}
	stand, back := map[string]string{}, map[string]string{}
	png := []byte{137, 80, 78, 71, 13, 10, 26, 10}
	for i, u := range x.Images {
		sum := sha1.Sum([]byte(u))
		data := "data:image/png;base64," + base64.StdEncoding.EncodeToString(slices.Concat(png, sum[:], []byte{byte(i & 255), byte(i >> 8)}))
		stand[u], back[data] = data, u
	}
	f, err := FormatArticleHTML(CacheImages(x.HTML, stand), &library.ReaderSourceSnapshot{HTML: x.HTML, Provenance: "source"})
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Formatted = &f
	label := func(src string) string {
		if u, ok := back[src]; ok {
			return u
		}
		sum := sha1.Sum([]byte(src))
		return src[:strings.Index(src, ",")] + ";sha1=" + hex.EncodeToString(sum[:])[:10]
	}
	out.HTML = standIn.ReplaceAllStringFunc(f.HTML, func(m string) string {
		return `src="` + strings.ReplaceAll(label(m[5:len(m)-1]), "&", "&amp;") + `"`
	})
	var document struct {
		Root json.RawMessage `json:"root"`
	}
	json.Unmarshal(f.Document, &document)
	out.Root = document.Root
	for data, u := range back {
		out.Root = bytes.ReplaceAll(out.Root, []byte(data), []byte(u))
	}
	return out
}

type benchRow struct {
	ID, Host           string
	TSStatus, GoStatus string
	SameExtracted      bool
	SameHTML           bool
	Sim                float64
	TSLen, GoLen       int
	SameImages         bool
	SameHeadings       bool
	SameCode           bool
	SameTables         bool
	SameDocument       bool // root, units and both revisions
	SameRevision       bool
	SameWork           bool
	Missing, Extra     []string `json:",omitempty"`
	FirstDiff          string   `json:",omitempty"`
	Layer              *bool    `json:",omitempty"` // with Mozilla's Readability output the page matches
	Intended           bool     `json:",omitempty"` // differs only by the intended root rule
	Error              string   `json:",omitempty"`
	TSms, Goms         float64
}

func (r benchRow) Same() bool {
	return r.TSStatus == r.GoStatus && r.SameExtracted && r.SameHTML && r.SameImages && r.SameHeadings && r.SameCode && r.SameDocument && r.SameWork
}

var tokenSpan = regexp.MustCompile(`<span class="reader-token-\d+">([^<]*)</span>`)

func compareRun(p page, ts tsPage, doc tsDoc, out portRun) benchRow {
	x := out.Extracted
	r := benchRow{ID: p.ID, Host: hostKey(p.FinalURL), TSStatus: ts.Status, GoStatus: x.Status, Error: out.Error}
	r.SameExtracted = x.HTML == ts.Extracted && x.TextLength == ts.TextLength
	r.SameWork = (ts.Work == nil) == (x.Work == nil)
	if ts.Work != nil && x.Work != nil {
		r.SameWork = ts.Work.Sections == len(x.Work.Sections) && ts.Work.Poster == (x.Work.Poster != nil) && ts.Work.Video == (x.Work.Video != nil)
	}
	if ts.Status == "unavailable" || x.Status == "unavailable" {
		r.SameHTML, r.SameImages, r.SameHeadings, r.SameCode, r.SameDocument, r.SameRevision, r.SameTables = ts.Status == x.Status, true, true, true, true, true, true
		return r
	}
	tsHTML := tokenSpan.ReplaceAllString(ts.HTML, "$1")
	r.SameHTML = tsHTML == out.HTML
	a, b := Measured(tsHTML), Measured(out.HTML)
	r.Sim, r.TSLen, r.GoLen = Similarity(a, b), a.Length, b.Length
	r.SameTables = a.Tables == b.Tables
	if !r.SameHTML {
		r.Missing, r.Extra = clip(Unmatched(a.Blocks, b.Blocks), 3), clip(Unmatched(b.Blocks, a.Blocks), 3)
		r.FirstDiff = firstDiff(tsHTML, out.HTML)
	}
	r.SameImages = slices.Equal(ts.Images, x.Images)
	if f := out.Formatted; f != nil {
		var headings []string
		for _, h := range f.Headings {
			headings = append(headings, fmt.Sprint(h.Level, " ", h.Text))
		}
		r.SameHeadings = slices.Equal(ts.Headings, headings)
		r.SameCode = reflect.DeepEqual(doc.CodeBlocks, f.CodeBlocks) || len(doc.CodeBlocks) == 0 && len(f.CodeBlocks) == 0
		var d struct {
			ContentRevision, UnitsRevision, Provenance string
			Units                                      json.RawMessage
		}
		json.Unmarshal(f.Document, &d)
		if doc.Document != nil {
			r.SameRevision = d.ContentRevision == doc.Document.ContentRevision && d.UnitsRevision == doc.Document.UnitsRevision
			r.SameDocument = r.SameRevision && jsonEqual(d.Units, doc.Document.Units) && jsonEqual(out.Root, doc.Document.Root)
		}
	}
	return r
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func hostKey(u string) string {
	host := u
	if i := strings.Index(u, "//"); i >= 0 {
		host = u[i+2:]
	}
	host, _, _ = strings.Cut(host, "/")
	return strings.TrimPrefix(host, "www.")
}

func esc(s string) string { return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s) }

// firstDiff shows where two strings part, a little of each.
func firstDiff(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	from := max(0, i-40)
	cut := func(s string) string { return strings.ToValidUTF8(s[min(from, len(s)):min(len(s), i+60)], "") }
	return fmt.Sprintf("@%d TS «%s» Go «%s»", i, cut(a), cut(b))
}

func benchReport(rows []benchRow) string {
	count := func(ok func(benchRow) bool) string {
		n := 0
		for _, r := range rows {
			if ok(r) {
				n++
			}
		}
		return fmt.Sprintf("%d / %d", n, len(rows))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# 对比台：Go 完整输出 对 TS 完整输出\n\n%d 页（extract-spike/pages 里状态 200 的 HTML）。生成：`EXTRACT_PORT=… go test ./internal/extract -run TestPortBench`，%s。\n\n", len(rows), time.Now().Format("2006-01-02 15:04"))
	b.WriteString("TS 的排版 HTML 去掉了 Shiki 的 `<span class=\"reader-token-N\">` 再比（已定不用 Shiki）；文档比 root、units、两个 revision，codeTokens 不比。\n\n| 指标 | 一致 |\n|---|---|\n")
	for _, m := range []struct {
		name string
		ok   func(benchRow) bool
	}{
		{"状态", func(r benchRow) bool { return r.TSStatus == r.GoStatus }},
		{"抽取 HTML 与字数（逐字）", func(r benchRow) bool { return r.SameExtracted }},
		{"排版 HTML（逐字）", func(r benchRow) bool { return r.SameHTML }},
		{"文字相似度 ≥ 0.999", func(r benchRow) bool {
			return r.Sim >= 0.999 || r.TSStatus == "unavailable" && r.GoStatus == "unavailable"
		}},
		{"图片地址（顺序一致）", func(r benchRow) bool { return r.SameImages }},
		{"标题", func(r benchRow) bool { return r.SameHeadings }},
		{"代码块（语言与全文）", func(r benchRow) bool { return r.SameCode }},
		{"表格数", func(r benchRow) bool { return r.SameTables }},
		{"正文结构（root、units、revision）", func(r benchRow) bool { return r.SameDocument }},
		{"作品页（画廊）", func(r benchRow) bool { return r.SameWork }},
		{"全部一致", benchRow.Same},
		{"不一致：有意的差异（根要包含页面正文，关掉这条就和 TS 一致）", func(r benchRow) bool { return !r.Same() && r.Intended }},
		{"不一致：Readability 层（换成 Mozilla Readability 的输出后全部一致）", func(r benchRow) bool { return !r.Same() && !r.Intended && r.Layer != nil && *r.Layer }},
		{"不一致：其他（待查）", func(r benchRow) bool { return !r.Same() && !r.Intended && (r.Layer == nil || !*r.Layer) }},
	} {
		fmt.Fprintf(&b, "| %s | %s |\n", m.name, count(m.ok))
	}
	var goMS, tsMS []float64
	for _, r := range rows {
		goMS, tsMS = append(goMS, r.Goms), append(tsMS, r.TSms)
	}
	fmt.Fprintf(&b, "| 耗时 中位 / p90 / 合计 ms（Go，含排版） | %s |\n| 耗时 中位 / p90 / 合计 ms（TS，ts-run） | %s |\n", timing(goMS, false), timing(tsMS, false))
	b.WriteString("\n## 不一致的页\n\n| 页面 | 换成 Mozilla 后一致 | 状态 TS/Go | 抽取 | 排版 | 相似度 | 字数 TS/Go | 图片 | 标题 | 代码 | 结构 | revision | 缺 | 多 | 第一处不同 |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	mark := func(ok bool) string { return map[bool]string{true: "=", false: "≠"}[ok] }
	for _, r := range slices.SortedFunc(slices.Values(rows), func(a, b benchRow) int { return cmp.Compare(a.Sim, b.Sim) }) {
		if r.Same() {
			continue
		}
		layer := "?"
		if r.Layer != nil {
			layer = map[bool]string{true: "是", false: "否"}[*r.Layer]
		}
		if r.Intended {
			layer = "有意的差异"
		}
		fmt.Fprintf(&b, "| %s | %s | %s/%s | %s | %s | %.4f | %d/%d | %s | %s | %s | %s | %s | %s | %s | %s |\n", r.ID, layer, r.TSStatus, r.GoStatus, mark(r.SameExtracted), mark(r.SameHTML),
			r.Sim, r.TSLen, r.GoLen, mark(r.SameImages), mark(r.SameHeadings), mark(r.SameCode), mark(r.SameDocument), mark(r.SameRevision),
			esc(strings.Join(r.Missing, " ／ ")), esc(strings.Join(r.Extra, " ／ ")), esc(cmpOr(r.Error, r.FirstDiff)))
	}
	return b.String()
}

// TestPortProgress replays the port rule by rule, in the order they were ported (ablation.md's), and runs the bench at
// each step: EXTRACT_PORT=… EXTRACT_PORT_STEPS=1 go test ./internal/extract -run TestPortProgress. Writes report/progress.md.
func TestPortProgress(t *testing.T) {
	dir := portDir(t, "EXTRACT_PORT_STEPS")
	defer func() { rulesOff = nil }()
	corpus := filepath.Join(dir, "..", "extract-spike")
	files, _ := filepath.Glob(filepath.Join(corpus, "pages", "*.json"))
	type input struct {
		p   page
		ts  tsPage
		doc tsDoc
	}
	var inputs []input
	for _, f := range files {
		var in input
		if err := readJSON(f, &in.p); err != nil {
			t.Fatal(err)
		}
		if in.p.Status != 200 || in.p.HTML == "" {
			continue
		}
		if err := readJSON(filepath.Join(corpus, "out", "ts-full", in.p.ID+".json"), &in.ts); err != nil {
			t.Fatal(err)
		}
		if err := readJSON(filepath.Join(dir, "out", "ts-doc", in.p.ID+".json"), &in.doc); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, in)
	}
	steps := []struct {
		name string
		on   []string
	}{
		{"规则全关：readeck + 清理 + 排版 + 正文结构", nil},
		{"+ React 流式片段归位", []string{"react"}},
		{"+ 隐藏内容（hidden 等、行内样式、作者 CSS）", []string{"hidden", "hidden-css"}},
		{"+ 唯一 article/main 作根", []string{"root"}},
		{"+ 重复标题、日期行、杂志标签（含目录与互动示例）", []string{"header", "furniture"}},
		{"+ 120 字门槛", []string{"short"}},
		{"+ 付费墙判定", []string{"paywall"}},
		{"+ 删除表单", []string{"forms"}},
		{"+ 画廊作品页（gallery-page）", []string{"gallery"}},
		{"+ Excalidraw 白板占位（至此与 TS 原样一致）", []string{"whiteboard"}},
		{"+ 有意的差异：根要包含页面正文", []string{"root-share"}},
	}
	off := set("react hidden hidden-css root header furniture short paywall forms gallery whiteboard root-share")
	var b strings.Builder
	fmt.Fprintf(&b, "# 逐条规则的进展\n\n按 extract-spike/report/ablation.md 的顺序把规则一条条打开，每步跑一遍对比台（%d 页）。没有打开的规则按 ablate.ts 的改法关掉。\n\n| 步骤 | 状态一致 | 抽取逐字一致 | 全部一致 | 文字相似度 ≥ 0.999 |\n|---|---|---|---|---|\n", len(inputs))
	for _, step := range steps {
		for _, r := range step.on {
			delete(off, r)
		}
		rulesOff = off
		status, extracted, same, close := 0, 0, 0, 0
		for _, in := range inputs {
			r := compareRun(in.p, in.ts, in.doc, runPort(in.p))
			if r.TSStatus == r.GoStatus {
				status++
			}
			if r.SameExtracted {
				extracted++
			}
			if r.Same() {
				same++
			}
			if r.TSStatus == r.GoStatus && (r.Sim >= 0.999 || r.TSStatus == "unavailable") {
				close++
			}
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n", step.name, status, extracted, same, close)
	}
	if err := os.WriteFile(filepath.Join(dir, "report", "progress.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + b.String())
}
