package explore

import (
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

// WebKit draws a page only while its window shows: a capture's window, never
// shown, got no rendering updates, so its page stood still at its first frame
// with its CSS animations and transitions not begun, its images never done and
// its load never over (catk.de, vendredi-society.fr: a first screen of the
// page's background alone). seen has the window answer AppKit's isVisible as
// shown, which is what WebKit asks of it: the page then runs as it would on
// screen, while the window stays off the screen, out of the window lists and
// without the focus. NSWindow's
// isVisible answers so for the capture windows (seeming) and as before for
// every other window.

var (
	seemMu  sync.Once
	seeming = map[uintptr]bool{} // read and changed on the main thread only
	// isVisible is NSWindow's own.
	isVisible uintptr
)

// seem puts the capture windows' isVisible in front of NSWindow's, once.
func seem() {
	seemMu.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			return
		}
		var method func(cls, sel uintptr) uintptr
		var setImplementation func(m, imp uintptr) uintptr
		purego.RegisterLibFunc(&method, lib, "class_getInstanceMethod")
		purego.RegisterLibFunc(&setImplementation, lib, "method_setImplementation")
		sel := uintptr(objc.RegisterName("isVisible"))
		m := method(uintptr(objc.GetClass("NSWindow")), sel)
		if m == 0 {
			return
		}
		isVisible = setImplementation(m, purego.NewCallback(func(self, cmd uintptr) uintptr {
			if seeming[self] {
				return 1
			}
			r, _, _ := purego.SyscallN(isVisible, self, cmd)
			return r & 0xff
		}))
	})
}

// seen has win, a capture's window, show to WebKit as shown. WebKit asks again as the window's occlusion changes,
// which it is told of: else it would keep the answer it had while the window was made, until something else asks.
func seen(win *mygo.Window) {
	mygo.RunOnMain(func() {
		seem()
		if w := win.NativeHandle(); w != 0 && isVisible != 0 {
			seeming[w] = true
			center := objc.ID(objc.GetClass("NSNotificationCenter")).Send(objc.RegisterName("defaultCenter"))
			cname := []byte("NSWindowDidChangeOcclusionStateNotification\x00")
			name := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), &cname[0])
			center.Send(objc.RegisterName("postNotificationName:object:"), name, w)
		}
	})
}

// unseen has win answer as it is again, before it closes.
func unseen(win *mygo.Window) {
	mygo.RunOnMain(func() { delete(seeming, win.NativeHandle()) })
}
