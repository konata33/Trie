//go:build darwin || linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// One app at a time: a second lock on a library is refused until the first
// lets go, and trie.lock holds the process id for the Electron app.
func TestLockLibrary(t *testing.T) {
	dir := t.TempDir()
	first, err := lockLibrary(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "trie.lock")); string(b) != strconv.Itoa(os.Getpid()) {
		t.Errorf("trie.lock says %q", b)
	}
	if _, err := lockLibrary(dir); !errors.Is(err, errHeld) {
		t.Errorf("a second lock: %v", err)
	}
	first.Close()
	again, err := lockLibrary(dir)
	if err != nil {
		t.Fatalf("after the first let go: %v", err)
	}
	again.Close()
}

// The Electron app holds a library while its SingletonLock names a live
// Electron process; a lock left by a crash, or naming another program, does
// not hold it.
func TestElectronHolds(t *testing.T) {
	dir := t.TempDir()
	electron := func(int) string { return "/Applications/Electron.app/Contents/MacOS/Electron" }
	if electronHolds(dir, electron) {
		t.Error("held without a SingletonLock")
	}
	link := filepath.Join(dir, "SingletonLock")
	point := func(pid int) {
		os.Remove(link)
		if err := os.Symlink("host-"+strconv.Itoa(pid), link); err != nil {
			t.Fatal(err)
		}
	}
	point(os.Getppid()) // go test, alive while this runs
	if !electronHolds(dir, electron) {
		t.Error("a live Electron process does not hold it")
	}
	if electronHolds(dir, func(int) string { return "/bin/zsh" }) {
		t.Error("another program holds it")
	}
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	point(gone.Process.Pid)
	if electronHolds(dir, electron) {
		t.Error("a lock left by a process that is gone holds it")
	}
}
