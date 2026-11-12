//go:build darwin

package main

import (
	"fmt"
	"os/exec"

	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

// shoot writes win to file as the screen shows it, drawn by the GPU: the
// window server's picture of the window, whatever lies over it, taken by
// another process. CapturePage draws the window again on the CPU, on the
// main thread, for half a second at scale 2 between the frames being timed.
func shoot(win *mygo.Window, file string) error {
	var n uintptr
	mygo.RunOnMain(func() { n = uintptr(objc.ID(win.NativeHandle()).Send(objc.RegisterName("windowNumber"))) })
	if out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", n), file).CombinedOutput(); err != nil {
		return fmt.Errorf("%v %s", err, out)
	}
	return nil
}
