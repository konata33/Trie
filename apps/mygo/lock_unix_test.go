//go:build darwin || linux

package main

import (
	"errors"
	"testing"
)

// One app at a time: a second lock on a library is refused until the first
// lets go.
func TestLockLibrary(t *testing.T) {
	dir := t.TempDir()
	first, err := lockLibrary(dir)
	if err != nil {
		t.Fatal(err)
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
