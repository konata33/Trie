package extract

import (
	"net/url"
	"strings"
	"testing"
)

const sample = `<!doctype html><html><head><title>A small post – Site</title><meta property="og:site_name" content="Site"></head><body>
<nav><a href="/">Home</a></nav>
<article><h1>A small post</h1>
<p>` + "Readability keeps the paragraphs of an article and drops what is around them. " + `It needs enough text to be sure, so this paragraph says the same thing twice. Readability keeps the paragraphs of an article and drops what is around them.</p>
<p>A second paragraph with a <a href="/next">link</a> and a picture: <img src="small.png" srcset="a-800.png 800w, a-1600.png 1600w, a-3200.png 3200w" alt="a"></p>
<pre><code class="language-go hl">fmt.Println("hi")</code></pre>
</article><footer>© Site</footer></body></html>`

func TestReadeck(t *testing.T) {
	base, _ := url.Parse("https://example.com/posts/small")
	a := Readeck(sample, base)
	if !a.OK || a.Title != "A small post – Site" || a.SiteName != "Site" || !strings.Contains(a.Content, "A second paragraph") || strings.Contains(a.Content, "Home") {
		t.Errorf("%+v", a)
	}
}

func TestSimpleClean(t *testing.T) {
	base, _ := url.Parse("https://example.com/posts/small")
	got, images := SimpleClean(`<div class="x" style="color:red"><script>alert(1)</script><p>Hi <a href="javascript:alert(1)">x</a> <a href="#n">n</a>`+
		`<img src="s.png" srcset="a-800.png 800w, a-1600.png 1600w, a-3200.png 3200w"><img src="t.gif" width="1" height="1"><img alt="gone"></p>`+
		`<pre class="language-js other"><code>1</code></pre><section>kept text</section></div>`, base)
	want := `<div><p>Hi <a>x</a> <a href="#n">n</a><img src="https://example.com/posts/a-1600.png">gone</p><pre class="language-js"><code>1</code></pre>kept text</div>`
	if got != want || len(images) != 1 || images[0] != "https://example.com/posts/a-1600.png" {
		t.Errorf("got %s %v", got, images)
	}
}

func TestMeasure(t *testing.T) {
	a := Measured(`<p>Hello, world.</p><div class="reader-code-block"><div class="reader-code-header"><span>纯文本</span><button>复制</button></div><pre><code>x := 1</code></pre></div><p>中文<b>段落</b></p><table><tr><td>1</td></tr></table>`)
	b := Measured(`<p>hello world</p><pre>x := 1</pre><p>中文段落</p><table><tr><td>1</td></tr></table>`)
	if Similarity(a, b) != 1 || a.Paragraphs != 2 || a.Pre != 1 || a.Tables != 1 || strings.Contains(strings.Join(a.Blocks, " "), "复制") {
		t.Errorf("a=%+v b=%+v sim=%v", a, b, Similarity(a, b))
	}
	if c := Measured(`<p>hello there</p>`); Similarity(b, c) >= 0.5 || len(Unmatched(b.Blocks, c.Blocks)) != 4 {
		t.Errorf("sim=%v unmatched=%v", Similarity(b, c), Unmatched(b.Blocks, c.Blocks))
	}
}
