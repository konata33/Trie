package fetch

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/htmlindex"
)

// testdata/decode.json is reader-service.ts decode() in Node, TextDecoder or
// Buffer.toString, for 47 byte strings under 30 labels (.context/mygo/feed-port/ts/decode.mjs).
func TestDecodeMatchesTextDecoder(t *testing.T) {
	data, err := os.ReadFile("testdata/decode.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Label, Hex, Text string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	bad, skipped := map[string]int{}, 0
	for _, c := range cases {
		body, _ := hex.DecodeString(c.Hex)
		// Node's ICU decoders read invalid and user-defined multi-byte sequences their own way (see Decode); only clean decodes count.
		if e, err := htmlindex.Get(c.Label); err == nil && multiByte[must(htmlindex.Name(e))] && strings.ContainsFunc(c.Text, func(r rune) bool {
			return r == utf8.RuneError || r >= 0x80 && r <= 0x9f || r >= 0xe000 && r <= 0xf8ff
		}) {
			skipped++
			continue
		}
		if got := Decode(body, c.Label); got != c.Text {
			if bad[c.Label]++; bad[c.Label] == 1 {
				t.Errorf("%s %s: got %q, want %q", c.Label, c.Hex, got, c.Text)
			}
		}
	}
	if len(bad) > 0 {
		t.Error(fmt.Sprint(bad))
	}
	t.Logf("%d of %d cases compared; %d multi-byte cases with invalid or user-defined sequences left out", len(cases)-skipped, len(cases), skipped)
}

var multiByte = map[string]bool{"gbk": true, "gb18030": true, "big5": true, "shift_jis": true, "euc-jp": true, "iso-2022-jp": true, "euc-kr": true}

func must(s string, _ error) string { return s }

func TestCharset(t *testing.T) {
	for typ, want := range map[string]string{"text/html": "", "text/html; charset=UTF-8": "utf-8", `text/html;charset="gbk"`: "gbk", "text/xml; CHARSET = 'Shift_JIS'": "shift_jis",
		"text/html; charset=": "", "application/rss+xml; charset=iso-8859-1; x=y": "iso-8859-1", "text/html; charset = big5": "big5"} {
		if got := Charset(typ); got != want {
			t.Errorf("%q: %q", typ, got)
		}
	}
}
