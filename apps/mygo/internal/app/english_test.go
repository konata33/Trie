package app

import (
	"testing"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// In English the pages say the catalog's source messages: Home, the
// settings panel, an author's page and the Follow a Blog dialog.
func TestEnglish(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "en" })
	t.Cleanup(func() { i18n.SetLocaleFunc(func() string { return "zh-Hans" }) })
	pinNow(t)
	a, _ := newTestApp(t)
	tt := ui.NewTester(a.View, 1280, 850)
	tt.SetLocale("en")
	settle(tt)
	shows := func(where string, texts ...string) {
		t.Helper()
		for _, s := range texts {
			if !tt.HasText(s) {
				t.Errorf("%s: no %q in %q", where, s, tt.Texts())
			}
		}
	}
	named := func(where string, names ...string) {
		t.Helper()
		for _, s := range names {
			if _, ok := tt.Access(s); !ok {
				t.Errorf("%s: nothing named %q", where, s)
			}
		}
	}

	shows("home", "Home", "Favorites", "Explore", "Clips")
	named("home", "Settings", "Follow a Blog…", "Refresh", "Sidebar")

	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	shows("settings", "Language", "English")
	named("settings", "Settings")
	tt.Key(0, ui.KeyEscape)
	settle(tt)

	must(t, tt.Click("Innei"))
	settle(tt)
	shows("author page", "Nov 4")
	named("author page", "Articles by Innei", "Actions for Innei", "Search this writer's articles")

	must(t, tt.Click("Follow a Blog…"))
	settle(tt)
	shows("subscribe dialog", "Follow a Blog", "Blog or feed address", "Find Feed", "Cancel")
	if tt.HasText("订阅") {
		t.Errorf("subscribe dialog: Chinese left in %q", tt.Texts())
	}
}
