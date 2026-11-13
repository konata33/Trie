package app

import "trie/internal/locales"

// storedTexts are the texts the library keeps in Chinese and the app shows: an
// article's notice, a writer's refresh failure, a link standing in for an
// interactive example, a gallery work's fact labels and score names. Code
// matches some of them, so they stay as they are stored.
var storedTexts = map[string]func() string{
	"订阅源提供的是摘要，可打开原文继续阅读":        locales.ReaderFeedSummaryReadOn,
	"订阅源仅提供摘要":                   locales.ReaderFeedSummary,
	"原文需要登录或订阅；仅保存公开可见的内容":       locales.ReaderSignInPublicKept,
	"原文需要登录或订阅；仅显示发布者提供的公开简介":    locales.ReaderSignInSummaryOnly,
	"互动示例，在原网页查看":                locales.ReaderInteractiveExample,
	"刷新失败，已保留已有文章。请检查网络或订阅地址后重试": locales.RefreshFailedKept,
	"评分":  locales.WorkFactScore,
	"技术":  locales.WorkFactTechnology,
	"字体":  locales.WorkFactTypefaces,
	"设计":  locales.WorkScoreDesign,
	"可用性": locales.WorkScoreUsability,
	"创意":  locales.WorkScoreCreativity,
	"内容":  locales.WorkScoreContent,
}

// storedText is s, a text the library keeps, in the language the app shows;
// a text it does not know is s.
func storedText(s string) string {
	if say, ok := storedTexts[s]; ok {
		return say()
	}
	return s
}
