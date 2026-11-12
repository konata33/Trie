//go:build darwin || linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// lockLibrary takes the library in dir for this app alone: one window of
// the app opens a library at a time. It holds trie.lock with flock for as
// long as the returned file stays open.
func lockLibrary(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, "trie.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errHeld
		}
		return nil, err
	}
	return f, nil
}
