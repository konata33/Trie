package reader

import (
	"testing"

	"trie/internal/theme"
)

// An article is set by its language: Chinese full of English terms and code
// stays Chinese, a lone katakana word does not make it Japanese, and English
// is New York at 20 on 1.6 lines, or the system face at 17 without it.
func TestFaceByLanguage(t *testing.T) {
	serif = true
	defer func() { serif = false }()
	for _, c := range []struct {
		name, html string
		want       Face
	}{
		{"chinese with terms", `<p>我入职 LobeHub 的第一个月做了一轮性能优化：react-layout-kit 这类 CSS-in-JS 的 Flexbox 组件一个页面上有几百个。</p>`, chineseFace},
		{"chinese with code", `<p>用法见<code>alpha beta gamma delta epsilon zeta eta theta</code>。</p><pre><code>const one = two(three, four, five, six, seven, eight, nine)</code></pre>`, chineseFace},
		{"chinese with katakana", `<p>我们去了一家叫カフェ的店，那里的咖啡很好喝，环境也很安静，适合一个人坐一下午看书写字。</p>`, chineseFace},
		{"japanese", `<p>東京で新しいプロジェクトを始めました。デザインとエンジニアリングの話です。</p>`, japaneseFace},
		{"korean", `<p>디자인 엔지니어링에 대한 글입니다. 새로운 프로젝트를 시작했습니다.</p>`, koreanFace},
		{"english with a little chinese", `<p>A product team at a large health insurer wants to add AI that writes the full determination, 中文.</p>`, englishFace},
	} {
		if got := Prepare(&Content{Status: "ready", HTML: c.html}).Face; got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	for title, want := range map[string]Face{
		"十个月，给 LobeHub 换掉 Ant Design：向 Base UI 与 StyleX 的渐进式迁移": chineseFace,
		"用 Claude Code 和 Codex 写 Go":          chineseFace,
		"デザインの話":                              japaneseFace,
		"Should You Actually Add AI To That?": englishFace,
	} {
		if got := titleFace(title); got != want {
			t.Errorf("title %q: %+v, want %+v", title, got, want)
		}
	}
	if englishFace.Scale*theme.ReadingSize != theme.EnglishReadingSize {
		t.Errorf("english scale %v sets 17 at %v, want %v", englishFace.Scale, englishFace.Scale*theme.ReadingSize, theme.EnglishReadingSize)
	}
	serif = false
	if got := Prepare(&Content{Status: "ready", HTML: `<p>Without New York, English is set in the system face.</p>`}).Face; got.Family != theme.Sans || got.Scale != 1 || got.LineHeight != theme.EnglishReadingLineHeight {
		t.Errorf("without serif: %+v", got)
	}
}
