package reader

import (
	"slices"
	"testing"
)

// A body's sketch opens lists, quotes and figures up into what they hold, keeps
// which words link and a code block's code, and stops at its limit.
func TestSketch(t *testing.T) {
	body := Prepare(&Content{Status: "ready", HTML: `<p>Read <a href="https://example.com/">the docs</a> first.</p>
<h2>Install</h2><ul><li>One</li><li>Two</li></ul><blockquote><p>Said once.</p></blockquote>
<pre><code>a := 1
  b := 2</code></pre><hr><p>Last.</p>`})
	var kinds []Kind
	sk := body.Sketch(20)
	for _, s := range sk {
		kinds = append(kinds, s.Kind)
	}
	if want := []Kind{Paragraph, Heading, Item, Item, Quote, Code, Rule, Paragraph}; !slices.Equal(kinds, want) {
		t.Fatalf("kinds %v, want %v", kinds, want)
	}
	if r := sk[0].Runs; len(r) != 3 || r[0].Link || !r[1].Link || r[1].Text != "the docs" || r[2].Link {
		t.Errorf("the paragraph's runs: %+v", r)
	}
	if sk[2].Depth != 1 || sk[4].Depth != 1 {
		t.Errorf("an item at depth %d, a quote at %d: want 1", sk[2].Depth, sk[4].Depth)
	}
	if len(sk[5].Runs) != 1 || sk[5].Runs[0].Text != "a := 1\n  b := 2" {
		t.Errorf("the code: %+v", sk[5].Runs)
	}
	if n := len(body.Sketch(3)); n != 3 {
		t.Errorf("limited to 3, got %d", n)
	}
	if (*Body)(nil).Sketch(3) != nil {
		t.Error("no body sketches nothing")
	}
}
