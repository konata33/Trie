package explore

import "github.com/egoist/mygo/i18n"

// The tests read the messages in Chinese, the language they were written in
// first.
func init() { i18n.SetLocaleFunc(func() string { return "zh-Hans" }) }
