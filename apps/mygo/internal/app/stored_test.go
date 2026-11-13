package app

import (
	"testing"

	"github.com/egoist/mygo/i18n"
)

// TestStoredText checks that the map covers every text the library stores and
// code matches, that each shows as a message of its own in Chinese, and that
// English translates it. The Chinese shown may be worded differently from
// what is stored.
func TestStoredText(t *testing.T) {
	stored := []string{
		"订阅源提供的是摘要，可打开原文继续阅读",
		"订阅源仅提供摘要",
		"原文需要登录或订阅；仅保存公开可见的内容",
		"原文需要登录或订阅；仅显示发布者提供的公开简介",
		"互动示例，在原网页查看",
		"刷新失败，已保留已有文章。请检查网络或订阅地址后重试",
		"评分", "技术", "字体", "设计", "可用性", "创意", "内容",
	}
	for _, s := range stored {
		if _, ok := storedTexts[s]; !ok {
			t.Errorf("no message for the stored %q", s)
		}
	}
	for s := range storedTexts {
		if got := storedText(s); got == "" {
			t.Errorf("zh-Hans: storedText(%q) is empty", s)
		}
	}
	if got := storedText("Josh Comeau"); got != "Josh Comeau" {
		t.Errorf("an unknown text: %q", got)
	}
	i18n.SetLocaleFunc(func() string { return "en" })
	t.Cleanup(func() { i18n.SetLocaleFunc(func() string { return "zh-Hans" }) })
	for s := range storedTexts {
		if got := storedText(s); got == s || got == "" {
			t.Errorf("en: storedText(%q) = %q", s, got)
		}
	}
	if got := storedText("订阅源仅提供摘要"); got != "The feed only has a summary." {
		t.Errorf("en: %q", got)
	}
}
