package web

import (
	"reflect"
	"testing"
)

func equal[T any](t *testing.T, got, want T, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", what, got, want)
	}
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

// Results of `new URL` in Node 26 (Ada 3.4.4).
func TestWebURL(t *testing.T) {
	for _, c := range [][3]string{
		{"https://a.com/a^b", "", "https://a.com/a%5Eb"},
		{"https://a.com/a|b{c}`d", "", "https://a.com/a|b%7Bc%7D%60d"},
		{`https://a.com/?q=a b'c"d<e>`, "", "https://a.com/?q=a%20b%27c%22d%3Ce%3E"},
		{"data:a b ?x", "", "data:a b%20?x"},
		{"data:a b ", "", "data:a b"},
		{"foo:/path", "", "foo:/path"},
		{"https://EXAMPLE.com:443", "", "https://example.com/"},
		{"http://0x7f.1/", "", "http://127.0.0.1/"},
		{"https://a.com/%2e%2E/x", "", "https://a.com/x"},
		{" \thttps://a.com/a\nb ", "", "https://a.com/ab"},
		{`..\x`, "https://a.com/b/c", "https://a.com/x"},
		{"//cdn.example/x.jpg", "https://a.com/", "https://cdn.example/x.jpg"},
		{"https://a.com/中文?中=文#片", "", "https://a.com/%E4%B8%AD%E6%96%87?%E4%B8%AD=%E6%96%87#%E7%89%87"},
		{"https://xn--n3h.com/", "", "https://xn--n3h.com/"},
		{"https://☃.com/", "", "https://xn--n3h.com/"},
		{"https://a..b/", "", "https://a..b/"},
		{"https://a.b./", "", "https://a.b./"},
		{"https://[::1]:80/", "", "https://[::1]:80/"},
		{"http://[1:0:0:2::3:0]/", "", "http://[1::2:0:0:3:0]/"},
		{"https://www./x", "", "https://www./x"},
		{"file:///C:/x/../y", "", "file:///C:/y"},
		{"https://user:pa:ss@a.com/", "", "https://user:pa%3Ass@a.com/"},
		{"https://a.com/#a b`c", "", "https://a.com/#a%20b%60c"},
		{"https://%41.com/", "", "https://a.com/"},
		{"https://a b.com/", "", ""},
		{"https://a.com:99999/", "", ""},
		{"https://1.2.3.4.5/", "", ""},
		{"https://a.1/", "", ""},
		{"https://a.0x/", "", ""},
		{"relative", "", ""},
	} {
		var got string
		var u *URL
		var err error
		if c[1] == "" {
			u, err = ParseURL(c[0], nil)
		} else {
			u, err = ResolveURL(c[0], c[1])
		}
		if err == nil {
			got = u.Href()
		}
		equal(t, got, c[2], c[0])
	}
	for _, c := range [][2]string{
		{"foo:/path", "foo:/path"},
		{"foo://www.x/a/", "foo://x/a"},
		{"mailto:a@b", "mailto:a@b"},
		{"https://www./x", "https://www./x"},
		{"tag:blog.example,2024:post-1", "tag:blog.example,2024:post-1"},
		{"https://www.a.com//#x", "https://a.com/"},
		{"https://www.a.com/a//?q#x", "https://a.com/a?q"},
		{"foo://u@www./", "foo://u@www./"},
	} {
		equal(t, must[string](t)(URLKey(c[0])), c[1], "key of "+c[0])
	}
}

// Date.parse as V8 reads the ISO format (checked in Node 26).
func TestDateParse(t *testing.T) {
	for _, c := range [][2]string{
		{"2026-02-30T00:00:00.000Z", "2026-03-02T00:00:00.000Z"},
		{"2026-01-01T24:00:00.000Z", "2026-01-02T00:00:00.000Z"},
		{"0000-01-01T00:00:00.000Z", "0000-01-01T00:00:00.000Z"},
		{"2026", "2026-01-01T00:00:00.000Z"},
		{"2026-11", "2026-11-01T00:00:00.000Z"},
		{"2026-11-02T10:00:00.1234Z", "2026-11-02T10:00:00.123Z"},
		{"2026-11-02T10:00:00+0800", "2026-11-02T02:00:00.000Z"},
		{"+002026-11-02T00:00:00.000Z", "2026-11-02T00:00:00.000Z"},
		{"2026-11-02t10:00:00z", "2026-11-02T10:00:00.000Z"},
		{"+275760-09-13T00:00:00.000Z", "+275760-09-13T00:00:00.000Z"},
		{"2026-13-01T00:00:00.000Z", ""}, {"2026-01-01T24:00:01.000Z", ""}, {"2026-01-01T23:60:00.000Z", ""},
		{"-000000-01-01T00:00:00Z", ""}, {"yesterday", ""}, {"", ""}, {"20261102", ""}, {"+275760-09-13T00:00:00.001Z", ""},
	} {
		got := ""
		if ms, ok := JSDateParse(c[0]); ok {
			got = ISOTime(ms)
		}
		equal(t, got, c[1], c[0])
	}
}

// The entity decoder joins numeric surrogate pairs and keeps unknown entities.
func TestDecode(t *testing.T) {
	equal(t, Decode("&#xD83D;&#xDE00; &AMP; &nbsp; &bogus; &#0; &#12345678; &#x41;&#66;"), "😀 &   &bogus; &#0; &#12345678; AB", "decoded")
}
