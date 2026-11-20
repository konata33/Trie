package explore

import (
	"context"
	jsonv2 "encoding/json/v2"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
	if _, err := TakePack(context.Background(), "http://localhost/", dir+"/a", nil); err == nil {
		t.Error("a private address was taken")
	}
	// An existing folder is left alone.
	if _, err := TakePack(context.Background(), "https://example.com/", dir, nil); err == nil {
		t.Error("an existing folder was written over")
	}
	if _, err := os.Stat(dir + ".part"); err == nil {
		t.Error("a half pack was left")
	}
}

func TestPackReadme(t *testing.T) {
	p := &Pack{URL: "https://example.com/", Title: "Example", Cut: true, Markup: true, Images: 3, Fonts: 2, Styles: 1, Refused: 1,
		Screens: []PackScreen{{File: "screens/00.jpg", Headings: []string{"Work", "About"}}, {File: "screens/01.jpg", ScrollY: 900}}}
	got := p.readme()
	for _, want := range []string{"# Reference: Example", "## Screens (2)", "`screens/00.jpg` at scroll 0 px: Work · About", "`screens/01.jpg` at scroll 900 px\n",
		"- `page.html`", "the rest of it is in `page.html` and `text.md`.", "3 pictures, 2 fonts and 1 style sheets saved; 1 more the page loaded were not saved"} {
		if !strings.Contains(got, want) {
			t.Errorf("readme lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "still being completed") {
		t.Errorf("readme of a whole pack says it is being completed:\n%s", got)
	}
	p.adding = true
	if got := p.readme(); !strings.Contains(got, "still being completed") {
		t.Errorf("readme of a pack being added to does not say so:\n%s", got)
	}
	// No markup: the index sends nowhere it is not.
	p.Markup = false
	if got := p.readme(); strings.Contains(got, "page.html") {
		t.Errorf("readme names page.html it lacks:\n%s", got)
	}
}

// A pack being added to stops when asked for it, or for any, and nothing more is written once StopPack returns; a stop
// for another pack leaves it going.
func TestStopPack(t *testing.T) {
	var wrote atomic.Int32
	addTo(context.Background(), "/packs/a", func(ctx context.Context, dir string) {
		for ctx.Err() == nil {
			wrote.Add(1)
			time.Sleep(time.Millisecond)
		}
	})
	StopPack("/packs/b")
	n := wrote.Load()
	time.Sleep(20 * time.Millisecond)
	if wrote.Load() == n {
		t.Error("another pack's stop stopped it")
	}
	StopPack("/packs/a")
	n = wrote.Load()
	time.Sleep(20 * time.Millisecond)
	if wrote.Load() != n {
		t.Error("still writing once stopped")
	}
	StopPack("") // nothing going: it returns
}

// A pack's file is written whole in its folder; once the folder is deleted, a write fails and the folder is not made
// again.
func TestWriteIn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := writeIn(root, "files/images/a.png", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "files", "images", "a.png")); err != nil || string(data) != "x" {
		t.Errorf("written: %q %v", data, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "files", "images", ".*")); len(left) > 0 {
		t.Errorf("left behind: %v", left)
	}
	os.RemoveAll(dir)
	if err := writeIn(root, "screens/01.jpg", []byte("y")); err == nil {
		t.Error("written into a deleted folder")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a deleted pack's folder was made again")
	}
}

// What a file is goes by its type, then its address: a page or a script read again is no picture.
func TestFileKind(t *testing.T) {
	p := &Pack{}
	for _, c := range []struct{ typ, link, want string }{
		{"image/webp", "https://cdn.example.com/a", "images"},
		{"font/woff2", "https://use.example.net/af/1/l?fvd=n4", "fonts"},
		{"application/octet-stream", "https://example.com/Inter.woff2", "fonts"},
		{"text/css", "https://example.com/a.css", "styles"},
		{"text/html", "https://example.com/missing.png", ""},
		{"text/javascript", "https://example.com/a.js", ""},
	} {
		if got, _ := fileKind(c.typ, c.link, p); got != c.want {
			t.Errorf("fileKind(%q, %q) = %q, want %q", c.typ, c.link, got, c.want)
		}
	}
}

// A file named without an extension is given its type's; one with an extension keeps it.
func TestTypeExt(t *testing.T) {
	for _, c := range []struct{ name, typ, want string }{
		{"images/0a1b2c-image", "image/webp", ".webp"},
		{"images/0a1b2c-hero.png", "image/webp", ""},
		{"styles/0a1b2c-css2", "text/css", ".css"},
		{"fonts/0a1b2c-font", "application/octet-stream", ""},
	} {
		if got := typeExt(c.name, c.typ); got != c.want {
			t.Errorf("typeExt(%q, %q) = %q, want %q", c.name, c.typ, got, c.want)
		}
	}
	if !fontFile.MatchString("https://example.com/a/Inter.woff2?v=4") || fontFile.MatchString("https://example.com/woff2/page") {
		t.Error("fontFile tells fonts wrong")
	}
}

// Progress goes from a tenth, past the screens, through the files to the end, never back.
func TestPackProgress(t *testing.T) {
	if got := screensOf(0); got != 1 {
		t.Errorf("an empty page: %d screens", got)
	}
	if got := screensOf(4000); got != 5 {
		t.Errorf("4000 px: %d screens", got)
	}
	if got := screensOf(1e6); got != packScreens {
		t.Errorf("a very long page: %d screens", got)
	}
	for _, c := range []struct {
		screen, screens, file, files int
		want                         float64
	}{{0, 4, 0, 0, 0.1}, {2, 4, 0, 0, 0.475}, {6, 4, 0, 0, 0.85}, {-1, 0, 0, 0, 0.85}, {-1, 0, 5, 10, 0.925}} {
		if got := packShare(c.screen, c.screens, c.file, c.files); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("packShare(%d, %d, %d, %d) = %v, want %v", c.screen, c.screens, c.file, c.files, got, c.want)
		}
	}
	var heard []float64
	tell := &packTeller{fn: func(done float64, _ image.Image) { heard = append(heard, done) }}
	tell.at(0.5, nil)
	tell.at(0.3, nil) // a later guess of more screens: it waits rather than goes back
	tell.at(0.3, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	tell.at(1, nil)
	if len(heard) != 3 || heard[0] != 0.5 || heard[1] != 0.5 || heard[2] != 1 {
		t.Errorf("heard %v", heard)
	}
	(&packTeller{}).at(1, nil) // nil hears nothing
}

// What a page answers a step down it (stepScreen) decodes as MyGo decodes it, by exact name (encoding/json/v2):
// misnamed, every step would read as a page that cannot move.
func TestPackAnswers(t *testing.T) {
	var st packStep
	if err := jsonv2.Unmarshal([]byte(`{"to":900,"more":true,"tall":true,"held":false,"taken":true}`), &st); err != nil || st != (packStep{To: 900, More: true, Tall: true, Taken: true}) {
		t.Errorf("a step: %+v %v", st, err)
	}
	for _, name := range []string{"to:", "more:", "tall:", "held:", "taken"} {
		if !strings.Contains(stepScreen, name) {
			t.Errorf("stepScreen answers no %s", name)
		}
	}
	var d packDoc
	if err := jsonv2.Unmarshal([]byte(`{"href":"https://example.com/","ready":"interactive","words":true,"fonts":true,"pictures":false}`), &d); err != nil || d != (packDoc{Href: "https://example.com/", Ready: "interactive", Words: true, Fonts: true}) {
		t.Errorf("a document: %+v %v", d, err)
	}
	// What EvalContext answers (any) reads as the same step, by the same names.
	var raw any
	jsonv2.Unmarshal([]byte(`{"to":1800,"more":false,"tall":true,"held":true,"taken":false}`), &raw)
	if st, err := decodeAs[packStep](raw); err != nil || st != (packStep{To: 1800, Tall: true, Held: true}) {
		t.Errorf("a step through any: %+v %v", st, err)
	}
	if n, err := decodeAs[int](float64(3)); err != nil || n != 3 {
		t.Errorf("a count through any: %v %v", n, err)
	}
	for _, name := range []string{"href:", "ready:", "words:", "fonts:", "pictures:"} {
		if !strings.Contains(packRead, name) {
			t.Errorf("packRead answers no %s", name)
		}
	}
	// What step sets is what the script reads.
	if s := step(900, 300*time.Millisecond, 2500*time.Millisecond); !strings.HasPrefix(s, "const down = 900, least = 300, most = 2500\n") {
		t.Errorf("step: %.60q", s)
	}
	for _, name := range []string{"down", "least", "most"} {
		if !strings.Contains(stepScreen, name) {
			t.Errorf("stepScreen reads no %s", name)
		}
	}
	// A wait already past is no wait.
	if s := hold(-time.Second); !strings.HasPrefix(s, "const wait = 0\n") || !strings.Contains(packHold, "< wait") {
		t.Errorf("hold: %.40q", s)
	}
}
