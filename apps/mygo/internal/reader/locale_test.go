package reader

import "github.com/egoist/mygo/i18n"

// The tests read the interface in Chinese, the language its texts were
// written in first.
func init() { i18n.SetLocaleFunc(func() string { return "zh-Hans" }) }
