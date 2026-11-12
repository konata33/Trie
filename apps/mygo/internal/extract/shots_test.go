package extract

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trie/internal/library"
	"trie/internal/reader"
	"trie/internal/theme"

	"github.com/egoist/mygo/ui"
)

// TestReaderShots shows three corpus articles, extracted and formatted here, in internal/reader without a window:
// EXTRACT_PORT=… EXTRACT_PORT_SHOTS=1 go test ./internal/extract -run TestReaderShots.
// Pictures are not fetched offline, so only those the page carries itself show; the others show as not loaded.
func TestReaderShots(t *testing.T) {
	dir := portDir(t, "EXTRACT_PORT_SHOTS")
	out := filepath.Join(dir, "shots")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct {
		id, name string
		features []string // texts to scroll to, each a screenshot
	}{
		{"george.mand.is_075e7c8d", "george-mand", []string{"复制代码", "表格，可横向滚动"}},
		{"innei.in_01fe1377", "innei-uikitcore", []string{"表格，可横向滚动", "复制代码"}},
		{"ishadeed.com_4be1d6c7", "ishadeed", []string{"复制代码"}},
	} {
		var p page
		if err := readJSON(filepath.Join(dir, "..", "extract-spike", "pages", s.id+".json"), &p); err != nil {
			t.Fatal(err)
		}
		x := ExtractPage(p.HTML, p.FinalURL, true)
		f, err := FormatArticleHTML(CacheImages(x.HTML, map[string]string{}), &library.ReaderSourceSnapshot{HTML: x.HTML, Provenance: "source"})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"status": x.Status, "html": f.HTML, "formatVersion": f.FormatVersion, "codeBlocks": f.CodeBlocks, "highlightStyles": f.HighlightStyles, "document": f.Document})
		var c reader.Content
		if err := json.Unmarshal(raw, &c); err != nil || c.Document == nil {
			t.Fatalf("%s: the reader does not take the document: %v", s.id, err)
		}
		r := reader.NewReader(&reader.Article{ID: s.id, Title: documentTitle(parseDocument(p.HTML)), URL: p.FinalURL, SourceName: hostKey(p.FinalURL)}, &c, reader.Prepare(&c))
		th := theme.UI()
		tt := ui.NewTester(func(ctx *ui.Context) { ctx.SetTheme(th); r.View(ctx) }, 724, 900)
		tt.SetScale(2)
		tt.Frame()
		save := func(name string) {
			file, err := os.Create(filepath.Join(out, s.name+"-"+name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := png.Encode(file, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		save("1-top")
		for i, text := range s.features {
			found := false
			for _, shown := range tt.Texts() {
				if strings.HasPrefix(shown, text) {
					if box, ok := tt.Find(shown); ok {
						r.Scroll.Y += box.Y - 160
						tt.Frame()
						found = true
						break
					}
				}
			}
			if !found {
				t.Errorf("%s: nothing showing %q", s.id, text)
				continue
			}
			save(string(rune('2'+i)) + "-" + map[string]string{"复制代码": "code", "表格，可横向滚动": "table"}[text])
		}
		t.Logf("%s: %d blocks, %d code blocks, scroll height %.0f", s.id, len(c.Document.Units), len(f.CodeBlocks), r.Scroll.MaxY)
	}
}
