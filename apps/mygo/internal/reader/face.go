package reader

import (
	"os"
	"unicode"

	"github.com/egoist/mygo/ui"

	"trie/internal/theme"
)

// A Face is how an article's text is set, by its language (DESIGN.md
// Typography): Family for its text, CJK for the runs of Han and kana (see
// scripts), Scale for the body, its headings and its title over the
// reader's size (Size), and the body's LineHeight.
type Face struct {
	Family     string
	CJK        string
	Scale      float32
	LineHeight float32
}

// The faces: each script in its own font. Chinese is set in SF Pro with
// PingFang SC, Japanese with Hiragino Sans, Korean with Apple SD Gothic Neo;
// English in New York, 20 where the others are 17.
var (
	chineseFace  = Face{Family: theme.Sans, CJK: "PingFang SC", Scale: 1, LineHeight: theme.ReadingLineHeight}
	japaneseFace = Face{Family: "system-ui, Hiragino Sans", CJK: "Hiragino Sans", Scale: 1, LineHeight: theme.ReadingLineHeight}
	koreanFace   = Face{Family: "system-ui, Apple SD Gothic Neo", CJK: "PingFang SC", Scale: 1, LineHeight: theme.ReadingLineHeight}
	englishFace  = Face{Family: theme.Serif + ", " + theme.Sans, CJK: "PingFang SC",
		Scale: float32(theme.EnglishReadingSize) / theme.ReadingSize, LineHeight: theme.EnglishReadingLineHeight}
)

// serif is whether New York is registered (RegisterSerif): without it,
// English is set in the system face, at the system face's size.
var serif bool

// RegisterSerif registers the system's New York, which macOS keeps hidden
// from apps by name, as theme.Serif. Call it before any text is laid out:
// registering a font drops every layout made.
func RegisterSerif() {
	for _, file := range []string{"/System/Library/Fonts/NewYork.ttf", "/System/Library/Fonts/NewYorkItalic.ttf"} {
		data, err := os.ReadFile(file)
		if err != nil || ui.RegisterFont(data, theme.Serif) != nil {
			return
		}
	}
	serif = true
}

// letters counts a text's characters by script, and its words of letters.
type letters struct{ han, kana, hangul, words int }

func (l *letters) add(text string) {
	inWord := false
	for _, ch := range text {
		switch {
		case unicode.Is(unicode.Han, ch):
			l.han++
			inWord = false
		case unicode.In(ch, unicode.Hiragana, unicode.Katakana):
			l.kana++
			inWord = false
		case unicode.Is(unicode.Hangul, ch):
			l.hangul++
			inWord = false
		case unicode.IsLetter(ch):
			if !inWord {
				l.words++
			}
			inWord = true
		default:
			inWord = false
		}
	}
}

// english is English's face: New York, or the system face at the system
// face's size without it (RegisterSerif).
func english() Face {
	if serif {
		return englishFace
	}
	f := englishFace
	f.Family, f.Scale = theme.Sans, 1
	return f
}

// faceOf tells an article's language from the text of its body, code and
// what never shows left out: kana in a tenth or more of its Han and kana is
// Japanese; otherwise more Han and kana than words of letters is Chinese,
// more Hangul than words is Korean, and the rest English.
func faceOf(nodes []*node) Face {
	var l letters
	var walk func(ns []*node)
	walk = func(ns []*node) {
		for _, n := range ns {
			switch {
			case n.Type == "text":
				l.add(n.Value)
			case n.Tag != "pre" && n.Tag != "code" && !skipped[n.Tag]:
				walk(n.Children)
			}
		}
	}
	walk(nodes)
	return l.face()
}

// face is the face of a text of these letters.
func (l letters) face() Face {
	switch cjk := l.han + l.kana; {
	case l.kana > 0 && l.kana*10 >= cjk:
		return japaneseFace
	case cjk > l.words:
		return chineseFace
	case l.hangul > l.words:
		return koreanFace
	}
	return english()
}

// titleFace tells a title's language from its own words, as the pages a
// title flies in from know no body: a Chinese title names English products
// often, so any kana is Japanese, any Han Chinese, any Hangul Korean.
func titleFace(title string) Face {
	var l letters
	l.add(title)
	switch {
	case l.kana > 0:
		return japaneseFace
	case l.han > 0:
		return chineseFace
	case l.hangul > 0:
		return koreanFace
	}
	return english()
}

// TitleFont is the family other pages set an article's title in, as the
// reader sets its headline (titleFace): New York for an English title.
func TitleFont(title string) string { return titleFace(title).Family }

// PassageFace is the family a passage of an article is set in out of the
// reader (摘录), told from its own text as an article's body is, and how
// much larger than Chinese it is set: 20/17 in New York.
func PassageFace(text string) (family string, scale float32) {
	var l letters
	l.add(text)
	f := l.face()
	return f.Family, f.Scale
}

// strut is the room under the baseline of a line of a body set in f, in
// ems: its half-leading and its font's descent.
func strut(f Face) float32 {
	const size = theme.ReadingSize
	m := ui.Font{Family: f.Family, Size: size}.Metrics()
	return ((f.LineHeight*size-m.Ascent-m.Descent)/2 + m.Descent) / size
}
