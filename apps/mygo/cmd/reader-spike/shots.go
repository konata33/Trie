package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"trie/internal/reader"
	"trie/internal/theme"
)

// shotArticles are the representative articles, by the start of their
// titles, and the names their screenshots take.
var shotArticles = []struct{ name, title string }{
	{"lobehub", "十个月，给 LobeHub 换掉 Ant Design"},
	{"uikitcore", "UIKitCore 逆向分析报告"},
	{"rain", "雨のち晴れ"},
	{"async", "Async, Sync, in Between"},
	{"devtools", "Pluggable, Extensible, and Playful DevTools"},
	{"stylex", "从纯 CSS 到 StyleX"},
	{"rn-uikit", "Migrating from React Native to UIKit"},
	{"short", "Hello Tokyo!"},
	{"summary", "用 Claude Code、Codex 和 Remotion"},
}

// screenshots renders each representative article without a window, at
// twice its size as a Retina display shows it: its first screen and the
// screen halfway down.
func screenshots(articles []*reader.Article, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, s := range shotArticles {
		i := find(articles, s.title)
		if i < 0 {
			return fmt.Errorf("no article titled %q", s.title)
		}
		a := newApp(articles)
		tt := ui.NewTester(a.view, 1280, 820)
		tt.SetScale(2)
		a.choose(i)
		tt.Frame()
		if err := save(tt, filepath.Join(dir, s.name+"-1-top.png")); err != nil {
			return err
		}
		a.reader.Scroll.Y = a.reader.Scroll.MaxY / 2
		tt.Frame()
		if err := save(tt, filepath.Join(dir, s.name+"-2-middle.png")); err != nil {
			return err
		}
		fmt.Printf("%s: prepared in %v, %.0f DIPs to scroll\n", s.name, a.built.Round(1e5), a.reader.Scroll.MaxY)
		// The reading column alone, 724 wide, to set beside the Electron
		// reader's CSS drawn by Chrome (compare-*-chrome.png).
		r := a.make(i)
		th := theme.UI()
		col := ui.NewTester(func(c *ui.Context) { c.SetTheme(th); r.View(c) }, 724, 820)
		col.SetScale(2)
		if err := save(col, filepath.Join(dir, "compare-"+s.name+"-mygo.png")); err != nil {
			return err
		}
	}
	spec := reader.NewReader(&reader.Article{ID: "specimen", Title: "排版样张 Specimen", SourceName: "小样"}, &reader.Content{Status: "ready"},
		reader.Prepare(&reader.Content{Status: "ready", HTML: specimen}))
	th := theme.UI()
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(th); spec.View(c) }, 724, 1000)
	tt.SetScale(2)
	if err := save(tt, filepath.Join(dir, "specimen.png")); err != nil {
		return err
	}
	// What each kind of block looks like, scrolled to the first of it.
	for _, s := range featureShots {
		a := newApp(articles)
		tt := ui.NewTester(a.view, 1280, 820)
		tt.SetScale(2)
		a.choose(find(articles, s.title))
		tt.Frame()
		box, ok := locate(tt, s.text)
		if !ok {
			return fmt.Errorf("%s: nothing showing %q", s.name, s.text)
		}
		a.reader.Scroll.Y += box.Y - 160
		tt.Frame()
		if err := save(tt, filepath.Join(dir, "feature-"+s.name+".png")); err != nil {
			return err
		}
	}
	return nil
}

// specimen is every inline style and block of the body in one article,
// Chinese with English, for the type: what bold and italic do to Chinese,
// where lines break around punctuation and in a long URL.
const specimen = `<h2>二级标题 Heading 2</h2><h3>三级标题 Heading 3</h3>
<p>常规 Regular，<strong>粗体 Bold</strong>，<em>斜体 Italic</em>，<strong><em>粗斜体</em></strong>，<code>行内代码 code</code>，<del>删除线</del>，H<sub>2</sub>O，x<sup>2</sup>，<a href="https://example.com">链接 link</a>。</p>
<p>中英混排：用React写界面，用Go写后台，2026年11月9日。标点避头尾：这一行很长很长很长很长很长很长很长很长很长很长很长很长很长很长，「引号」（括号）《书名》……——最后。</p>
<p>长网址：https://github.com/lobehub/lobe-chat/pull/20317/files#diff-4c6b8e3f0a7c1d2e9b5f6a8d0c3e1f2a4b6c8d0e2f4a6b8c0d2e4f6a8b0c2d4e 结束。</p>
<blockquote><p>引用里的一段话 A quote.</p></blockquote>
<ul><li>列表一 Item one</li><li>列表二<ul><li>嵌套 Nested</li></ul></li></ul><ol start="3"><li>第三</li><li>第四</li></ol>
<hr><p>分隔线之后。</p>`

var featureShots = []struct{ name, title, text string }{
	{"table-lobehub", "十个月，给 LobeHub 换掉 Ant Design", "表格，可横向滚动"},
	{"table-uikitcore", "UIKitCore 逆向分析报告", "表格，可横向滚动"},
	{"code-async", "Async, Sync, in Between", "复制代码"},
	{"code-devtools", "Pluggable, Extensible, and Playful DevTools", "复制代码"},
	{"code-legacy", "Categorize Your Dependencies", "复制代码"},
	{"callout", "Ecosystem Sponsorship Forwarding", "Note"},
	{"missing-image", "从纯 CSS 到 StyleX", "图片没能载入"},
	{"lists-sup-hr", "手记1", "1."},
}

// locate finds the first text, or label, starting with prefix.
func locate(tt *ui.Tester, prefix string) (ui.Rect, bool) {
	for _, t := range tt.Texts() {
		if strings.HasPrefix(t, prefix) {
			return tt.Find(t)
		}
	}
	return ui.Rect{}, false
}

func find(articles []*reader.Article, title string) int {
	for i, a := range articles {
		if strings.HasPrefix(a.Title, title) {
			return i
		}
	}
	return -1
}

func save(tt *ui.Tester, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, tt.Image())
}
