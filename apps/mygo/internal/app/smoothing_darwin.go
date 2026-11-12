//go:build darwin

package app

import (
	"os"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// The Electron app draws its text with -webkit-font-smoothing: antialiased
// (styles.css body): glyphs without Core Text's font smoothing, which
// emboldens light text on a dark ground. MyGo smooths its text while the
// user leaves smoothing on, as AppKit does, and reads that from
// AppleFontSmoothing in the standard user defaults; registering 0 for this
// process, before any text is drawn, draws it as Chrome does. A user who set
// AppleFontSmoothing for every app keeps their own setting.
// TRIE_FONT_SMOOTHING=1 leaves MyGo's smoothing on, to compare.
func init() {
	if os.Getenv("TRIE_FONT_SMOOTHING") == "1" {
		return // as MyGo draws by default, to compare
	}
	if _, err := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL); err != nil {
		return
	}
	class := func(name string) objc.ID { return objc.ID(objc.GetClass(name)) }
	send := func(id objc.ID, sel string, args ...any) objc.ID { return id.Send(objc.RegisterName(sel), args...) }
	if class("NSUserDefaults") == 0 {
		return
	}
	pool := send(send(class("NSAutoreleasePool"), "alloc"), "init")
	defer send(pool, "drain")
	key := send(class("NSString"), "stringWithUTF8String:", "AppleFontSmoothing")
	zero := send(class("NSNumber"), "numberWithInt:", 0)
	send(send(class("NSUserDefaults"), "standardUserDefaults"), "registerDefaults:", send(class("NSDictionary"), "dictionaryWithObject:forKey:", zero, key))
}
