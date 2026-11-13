package app

import "github.com/egoist/mygo/i18n"

// The tests read the interface in Chinese, the language its texts were
// written in first; one test (TestEnglish) switches to English itself.
func init() { i18n.SetLocaleFunc(func() string { return "zh-Hans" }) }
