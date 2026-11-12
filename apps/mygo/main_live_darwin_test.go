package main

import (
	"log"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The app's window as main keeps it, in MyGo's event loop (TRIE_LIVE=1):
//
//	TRIE_LIVE=1 CGO_ENABLED=0 go test -count=1 -run TestLiveWindow .
//
// The test app is an accessory: no Dock icon, and its windows never show, so it leaves the user's screen alone.

var (
	// the window open, as main's win
	testWin atomic.Pointer[mygo.Window]
	// stopped tells that quitting stopped the captures (keepOpen's stop).
	stopped atomic.Bool
)

func openTest() {
	w := mygo.NewWindow(mygo.WindowOptions{Width: 400, Height: 300, Hidden: true, Content: ui.View(func(c *ui.Context) { ui.Text(c, "Trie") })})
	testWin.Store(w)
	w.OnClosed(func() { testWin.CompareAndSwap(w, nil) })
}

func TestMain(m *testing.M) {
	if os.Getenv("TRIE_LIVE") == "" {
		os.Exit(m.Run())
	}
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	keepOpen(func() {
		if testWin.Load() == nil {
			openTest()
		}
	}, func() { stopped.Store(true) })
	code := 1
	mygo.App.WhenReady(func() {
		go func() {
			code = m.Run()
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	// ⌘Q goes the way App.Quit does: the captures under way were stopped first.
	if code == 0 && !stopped.Load() {
		log.Print("quitting did not stop the captures")
		code = 1
	}
	os.Exit(code)
}

// Closing the window leaves the app running; the Dock icon brings a window back.
func TestLiveWindow(t *testing.T) {
	if os.Getenv("TRIE_LIVE") == "" {
		t.Skip("TRIE_LIVE is not set")
	}
	mygo.RunOnMain(openTest)
	first := testWin.Load()
	first.Close()
	deadline := time.Now().Add(5 * time.Second)
	for testWin.Load() != nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if testWin.Load() != nil {
		t.Fatal("the window did not close")
	}
	time.Sleep(500 * time.Millisecond)
	alive := make(chan struct{})
	go mygo.RunOnMain(func() { close(alive) })
	select {
	case <-alive:
	case <-time.After(2 * time.Second):
		t.Fatal("the app stopped with its window")
	}
	if stopped.Load() {
		t.Fatal("closing the window quit the app")
	}
	// A click on the Dock icon (activate) opens the window again.
	mygo.RunOnMain(func() {
		if testWin.Load() == nil {
			openTest()
		}
	})
	if w := testWin.Load(); w == nil || w == first || w.IsDestroyed() {
		t.Fatal("no window again")
	}
}
