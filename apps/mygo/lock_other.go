//go:build !darwin && !linux

package main

import "os"

// lockLibrary takes nothing where flock is missing: the app ships for macOS.
func lockLibrary(dir string) (*os.File, error) { return nil, nil }
