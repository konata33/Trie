package explore

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestPackName(t *testing.T) {
	a := packName("https://cdn.example.com/a/image.png?w=800")
	b := packName("https://cdn.example.com/b/image.png?w=800")
	if a == b || !strings.HasSuffix(a, "-image.png") || !strings.HasSuffix(b, "-image.png") {
		t.Errorf("two files called image.png: %q, %q", a, b)
	}
	if got := packName("https://example.com/fonts/Geist%20Mono.woff2"); strings.ContainsAny(got, " /%") {
		t.Errorf("unsafe name %q", got)
	}
	if got := packName("https://example.com/"); !strings.HasSuffix(got, "-file") {
		t.Errorf("no base name: %q", got)
	}
}

func TestTakePackRefuses(t *testing.T) {
	dir := t.TempDir()
	if _, err := TakePack(context.Background(), "http://localhost/", dir+"/a"); err == nil {
		t.Error("a private address was taken")
	}
	// An existing folder is left alone.
	if _, err := TakePack(context.Background(), "https://example.com/", dir); err == nil {
		t.Error("an existing folder was written over")
	}
	if _, err := os.Stat(dir + ".part"); err == nil {
		t.Error("a half pack was left")
	}
}

func TestPackReadme(t *testing.T) {
	p := &Pack{URL: "https://example.com/", Title: "Example", Cut: true, Images: 3, Fonts: 2, Refused: 1,
		Screens: []PackScreen{{File: "screens/00.jpg", Headings: []string{"Work", "About"}}, {File: "screens/01.jpg", ScrollY: 900}}}
	got := p.readme()
	for _, want := range []string{"# Reference: Example", "## Screens (2)", "`screens/00.jpg` at scroll 0 px: Work · About", "`screens/01.jpg` at scroll 900 px\n", "only the first 40", "3 pictures and 2 fonts saved; 1 the site would not give."} {
		if !strings.Contains(got, want) {
			t.Errorf("readme lacks %q:\n%s", want, got)
		}
	}
}
