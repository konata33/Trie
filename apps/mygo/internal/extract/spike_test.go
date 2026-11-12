package extract

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestSpike runs the candidates over the bench's page corpus and compares them with reference outputs recorded from
// the previous implementation (by .context/mygo/extract-spike/ts-run.ts):
//
//	EXTRACT_SPIKE=$PWD/../../.context/mygo/extract-spike go test ./internal/extract -run TestSpike -v
//
// Writes out/go-<candidate>/ (Readability layer), out/go-<candidate>-simple/ (plus SimpleClean) and report/.
func TestSpike(t *testing.T) {
	dir := os.Getenv("EXTRACT_SPIKE")
	if dir == "" {
		t.Skip("EXTRACT_SPIKE is not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "pages", "*.json"))
	var pages []page
	for _, f := range files {
		var p page
		if err := readJSON(f, &p); err != nil {
			t.Fatal(err)
		}
		if p.Status == 200 && p.HTML != "" {
			pages = append(pages, p)
		}
	}
	candidates := []struct {
		name string
		run  func(string, *url.URL) Article
	}{{"readeck", Readeck}}
	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("# 正文提取对比汇总\n\n%d 个 HTML 页面（pages/ 里状态 200 的）。生成：`go test ./internal/extract -run TestSpike`，%s。\n\n", len(pages), time.Now().Format("2006-01-02 15:04")))
	var layerRows, fullRows [][]string
	for _, c := range candidates {
		var layer, full []row
		for _, p := range pages {
			base, err := url.Parse(p.FinalURL)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			a := c.run(p.HTML, base)
			a.MS = float64(time.Since(start).Microseconds()) / 1000
			writeJSON(t, filepath.Join(dir, "out", "go-"+c.name, p.ID+".json"), struct {
				ID string `json:"id"`
				Article
			}{p.ID, a})
			start = time.Now()
			s := simple{ID: p.ID, Status: "unavailable"}
			if a.OK {
				s.HTML, s.Images = SimpleClean(a.Content, base)
				s.Status = "ready"
			}
			s.MS = a.MS + float64(time.Since(start).Microseconds())/1000
			writeJSON(t, filepath.Join(dir, "out", "go-"+c.name+"-simple", p.ID+".json"), s)

			var ts Article
			var tf tsFull
			if err := readJSON(filepath.Join(dir, "out", "ts-readability", p.ID+".json"), &ts); err != nil {
				t.Fatal(err)
			}
			if err := readJSON(filepath.Join(dir, "out", "ts-full", p.ID+".json"), &tf); err != nil {
				t.Fatal(err)
			}
			layer = append(layer, compare(p, ts.OK, ts.Content, ts.MS, a.OK, a.Content, a.MS, &ts, &a))
			r := compare(p, tf.Status == "ready" || tf.Status == "restricted", tf.HTML, tf.MS, a.OK, s.HTML, s.MS, nil, nil)
			r.TSStatus, r.Work = tf.Status, tf.Work != nil
			if r.Work {
				r.Note = "作品页（画廊适配器），不比"
			}
			full = append(full, r)
		}
		layerRows = append(layerRows, summarize(c.name, layer, false))
		fullRows = append(fullRows, summarize(c.name+" + 简单清理", full, true))
		writePages(t, filepath.Join(dir, "report", "readability-"+c.name+".md"), "Readability 层："+c.name+" 对 @mozilla/readability 0.6.0", layer)
		writePages(t, filepath.Join(dir, "report", "full-"+c.name+".md"), c.name+" + 简单清理 对 TS 完整提取（extractPage + 排版）", full)
		writeJSON(t, filepath.Join(dir, "report", "rows-"+c.name+".json"), map[string][]row{"readability": layer, "full": full})
	}
	summary.WriteString("## 一、Readability 层（同一份原文，只比 Readability 的直接输出）\n\n" + table(layerHeader, layerRows) + "\n")
	summary.WriteString("## 二、候选 + 简单清理 对 TS 完整输出\n\n不比作品页（画廊适配器产出，Go 侧还没有）。\"可读\" 指 TS 判为 ready/restricted 的页面。\n\n" + table(fullHeader, fullRows) + "\n")
	if err := os.WriteFile(filepath.Join(dir, "report", "summary.md"), []byte(summary.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + summary.String())
}

type page struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	FinalURL string `json:"finalURL"`
	Origin   string `json:"origin"`
	Kind     string `json:"kind"`
	Status   int    `json:"status"`
	HTML     string `json:"html"`
}

type simple struct {
	ID     string   `json:"id"`
	Status string   `json:"status"`
	HTML   string   `json:"html"`
	Images []string `json:"images"`
	MS     float64  `json:"ms"`
}

type tsFull struct {
	Status string    `json:"status"`
	HTML   string    `json:"html"`
	Work   *struct{} `json:"work"`
	MS     float64   `json:"ms"`
}

type row struct {
	ID, Host, Kind, Origin   string
	TSOK, GoOK               bool
	TSStatus                 string `json:",omitempty"`
	Work                     bool   `json:",omitempty"`
	Note                     string `json:",omitempty"`
	Sim, TextContentSim      float64
	SameBlocks               bool
	TSLen, GoLen             int
	TSParas, GoParas         int
	TSImgs, GoImgs           int
	ImgMissing, ImgExtra     []string
	TSPre, GoPre             int
	TSTables, GoTables       int
	TSHeads, GoHeads         int
	Title, Byline, Excerpt   bool
	SiteName, Lang           bool
	TSTitle, GoTitle         string `json:",omitempty"`
	Missing, Extra           []string
	MissingChars, ExtraChars int
	TSms, Goms               float64
}

func compare(p page, tsOK bool, tsHTML string, tsMS float64, goOK bool, goHTML string, goMS float64, ta, ga *Article) row {
	host := p.FinalURL
	if u, err := url.Parse(p.FinalURL); err == nil {
		host = strings.TrimPrefix(u.Hostname(), "www.")
	}
	r := row{ID: p.ID, Host: host, Kind: p.Kind, Origin: p.Origin, TSOK: tsOK, GoOK: goOK, TSms: tsMS, Goms: goMS}
	if !tsOK || !goOK {
		return r
	}
	a, b := Measured(tsHTML), Measured(goHTML)
	r.Sim, r.SameBlocks = Similarity(a, b), slices.Equal(a.Blocks, b.Blocks)
	r.TSLen, r.GoLen, r.TSParas, r.GoParas = a.Length, b.Length, a.Paragraphs, b.Paragraphs
	r.TSImgs, r.GoImgs = len(a.Images), len(b.Images)
	r.ImgMissing, r.ImgExtra = Missing(a.Images, b.Images), Missing(b.Images, a.Images)
	r.TSPre, r.GoPre, r.TSTables, r.GoTables, r.TSHeads, r.GoHeads = a.Pre, b.Pre, a.Tables, b.Tables, a.Headings, b.Headings
	r.Missing, r.Extra = Unmatched(a.Blocks, b.Blocks), Unmatched(b.Blocks, a.Blocks)
	for _, s := range r.Missing {
		r.MissingChars += utf8.RuneCountInString(s)
	}
	for _, s := range r.Extra {
		r.ExtraChars += utf8.RuneCountInString(s)
	}
	r.Missing, r.Extra = clip(r.Missing, 5), clip(r.Extra, 5)
	if ta != nil {
		same := func(x, y string) bool {
			return strings.Join(strings.Fields(x), " ") == strings.Join(strings.Fields(y), " ")
		}
		r.Title, r.Byline, r.Excerpt, r.SiteName, r.Lang = same(ta.Title, ga.Title), same(ta.Byline, ga.Byline), same(ta.Excerpt, ga.Excerpt), same(ta.SiteName, ga.SiteName), same(ta.Lang, ga.Lang)
		if !r.Title {
			r.TSTitle, r.GoTitle = ta.Title, ga.Title
		}
		tc, gc := Measure{Tokens: map[string]int{}}, Measure{Tokens: map[string]int{}}
		for _, x := range []struct {
			m *Measure
			s string
		}{{&tc, ta.TextContent}, {&gc, ga.TextContent}} {
			for _, tok := range tokens(x.s) {
				x.m.Tokens[tok]++
				x.m.Count++
			}
		}
		r.TextContentSim = Similarity(tc, gc)
	}
	return r
}

func clip(list []string, n int) []string {
	list = list[:min(len(list), n)]
	out := make([]string, len(list))
	for i, s := range list {
		if utf8.RuneCountInString(s) > 70 {
			s = string([]rune(s)[:70]) + "…"
		}
		out[i] = s
	}
	return out
}

var layerHeader = []string{"候选", "页数", "双方都有结果", "仅 TS 有", "仅 Go 有", "文字相似度 均值 / 中位", "≥0.999", "0.99–0.999", "0.95–0.99", "0.8–0.95", "<0.8", "块序列完全一致", "段落数一致", "图片地址一致", "代码块数一致", "表格数一致", "标题一致", "作者一致", "摘要一致", "站名一致", "Go 耗时 中位 / p90 / 最大 / 合计 ms", "TS 耗时 中位 / p90 / 最大 / 合计 ms"}
var fullHeader = []string{"候选", "可比页数", "TS 可读", "双方都有结果", "TS 可读但 Go 无", "TS 判不可读但 Go 有", "文字相似度 均值 / 中位", "≥0.999", "0.99–0.999", "0.95–0.99", "0.8–0.95", "<0.8", "块序列完全一致", "段落数一致", "图片地址一致", "代码块数一致", "表格数一致", "Go 耗时 中位 / p90 / 合计 ms", "TS 耗时 中位 / p90 / 合计 ms"}

func summarize(name string, rows []row, full bool) []string {
	var both, tsOnly, goOnly, considered, tsOK int
	var sims, goMS, tsMS []float64
	var buckets [5]int
	var same, paras, imgs, pre, tables, title, byline, excerpt, site int
	for _, r := range rows {
		if r.Work {
			continue
		}
		considered++
		goMS, tsMS = append(goMS, r.Goms), append(tsMS, r.TSms)
		if r.TSOK {
			tsOK++
		}
		switch {
		case r.TSOK && r.GoOK:
			both++
		case r.TSOK:
			tsOnly++
			continue
		case r.GoOK:
			goOnly++
			continue
		default:
			continue
		}
		sims = append(sims, r.Sim)
		switch {
		case r.Sim >= 0.999:
			buckets[0]++
		case r.Sim >= 0.99:
			buckets[1]++
		case r.Sim >= 0.95:
			buckets[2]++
		case r.Sim >= 0.8:
			buckets[3]++
		default:
			buckets[4]++
		}
		count := func(ok bool, n *int) {
			if ok {
				*n++
			}
		}
		count(r.SameBlocks, &same)
		count(r.TSParas == r.GoParas, &paras)
		count(len(r.ImgMissing) == 0 && len(r.ImgExtra) == 0 && r.TSImgs == r.GoImgs, &imgs)
		count(r.TSPre == r.GoPre, &pre)
		count(r.TSTables == r.GoTables, &tables)
		count(r.Title, &title)
		count(r.Byline, &byline)
		count(r.Excerpt, &excerpt)
		count(r.SiteName, &site)
	}
	of := func(n int) string { return fmt.Sprintf("%d (%.0f%%)", n, 100*float64(n)/float64(max(both, 1))) }
	mean, median := stats(sims)
	sim := fmt.Sprintf("%.4f / %.4f", mean, median)
	cells := []string{name, fmt.Sprint(considered)}
	if full {
		cells = append(cells, fmt.Sprint(tsOK), fmt.Sprint(both), fmt.Sprint(tsOnly), fmt.Sprint(goOnly), sim)
	} else {
		cells = append(cells, fmt.Sprint(both), fmt.Sprint(tsOnly), fmt.Sprint(goOnly), sim)
	}
	for _, b := range buckets {
		cells = append(cells, fmt.Sprint(b))
	}
	cells = append(cells, of(same), of(paras), of(imgs), of(pre), of(tables))
	if !full {
		cells = append(cells, of(title), of(byline), of(excerpt), of(site), timing(goMS, true), timing(tsMS, true))
	} else {
		cells = append(cells, timing(goMS, false), timing(tsMS, false))
	}
	return cells
}

func stats(v []float64) (mean, median float64) {
	if len(v) == 0 {
		return 0, 0
	}
	s := slices.Sorted(slices.Values(v))
	for _, x := range s {
		mean += x
	}
	return mean / float64(len(s)), s[len(s)/2]
}

func timing(v []float64, withMax bool) string {
	if len(v) == 0 {
		return "-"
	}
	s := slices.Sorted(slices.Values(v))
	total := 0.0
	for _, x := range s {
		total += x
	}
	p90 := s[min(len(s)-1, len(s)*9/10)]
	if withMax {
		return fmt.Sprintf("%.1f / %.1f / %.0f / %.0f", s[len(s)/2], p90, s[len(s)-1], total)
	}
	return fmt.Sprintf("%.1f / %.1f / %.0f", s[len(s)/2], p90, total)
}

func table(header []string, rows [][]string) string {
	// Transposed: one column per candidate, the measures down the side.
	var b strings.Builder
	b.WriteString("| 指标 |")
	for _, r := range rows {
		b.WriteString(" " + r[0] + " |")
	}
	b.WriteString("\n|---|" + strings.Repeat("---|", len(rows)) + "\n")
	for i := 1; i < len(header); i++ {
		b.WriteString("| " + header[i] + " |")
		for _, r := range rows {
			b.WriteString(" " + r[i] + " |")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func writePages(t *testing.T, path, title string, rows []row) {
	rows = slices.Clone(rows)
	slices.SortStableFunc(rows, func(x, y row) int {
		return cmp.Or(cmp.Compare(rank(x), rank(y)), cmp.Compare(x.Sim, y.Sim), strings.Compare(x.ID, y.ID))
	})
	var b strings.Builder
	b.WriteString("# " + title + "\n\n按差异从大到小。相似度：两边文字的词（中日韩按字）多重集的 Dice 系数。缺：TS 有而 Go 没有的文字块；多：Go 多出的。\n\n")
	b.WriteString("| 页面 | 类 | 相似度 | 字数 TS/Go | 段落 | 图片 TS/Go (缺/多) | 代码块 | 表格 | 标题 | 缺的块 | 多的块 |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		if r.Work || !r.TSOK || !r.GoOK {
			status := map[bool]string{true: "有", false: "无"}
			note := r.Note
			if note == "" {
				note = "TS " + status[r.TSOK] + cmp.Or(" ("+r.TSStatus+")", "") + "，Go " + status[r.GoOK]
			}
			b.WriteString(fmt.Sprintf("| %s | %s | - | | | | | | | %s | |\n", r.ID, r.Kind, note))
			continue
		}
		title := "="
		if !r.Title && r.TSTitle+r.GoTitle != "" {
			title = fmt.Sprintf("≠ %q / %q", clip([]string{r.TSTitle}, 1)[0], clip([]string{r.GoTitle}, 1)[0])
		} else if !r.Title && r.TSTitle == r.GoTitle {
			title = ""
		}
		cell := func(list []string, chars int) string {
			if len(list) == 0 {
				return ""
			}
			return fmt.Sprintf("%d 字：%s", chars, strings.ReplaceAll(strings.Join(clip(list, 2), " ／ "), "|", "\\|"))
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %.4f | %d/%d | %d/%d | %d/%d (%d/%d) | %d/%d | %d/%d | %s | %s | %s |\n", r.ID, r.Kind, r.Sim, r.TSLen, r.GoLen, r.TSParas, r.GoParas,
			r.TSImgs, r.GoImgs, len(r.ImgMissing), len(r.ImgExtra), r.TSPre, r.GoPre, r.TSTables, r.GoTables, strings.ReplaceAll(title, "|", "\\|"),
			cell(r.Missing, r.MissingChars), cell(r.Extra, r.ExtraChars)))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// rank puts pages where only one side has a result first, then compared pages, then the ones left out.
func rank(r row) int {
	switch {
	case r.Work:
		return 3
	case r.TSOK != r.GoOK:
		return 0
	case r.TSOK:
		return 1
	}
	return 2
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(t *testing.T, path string, v any) {
	b, err := json.Marshal(v)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o755)
	}
	if err == nil {
		err = os.WriteFile(path, b, 0o644)
	}
	if err != nil {
		t.Fatal(err)
	}
}
