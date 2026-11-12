//go:build !darwin

package main

import (
	"os"

	"github.com/egoist/mygo"
)

// shoot writes win to file as it shows.
func shoot(win *mygo.Window, file string) error {
	png, err := win.CapturePage()
	if err != nil {
		return err
	}
	return os.WriteFile(file, png, 0o644)
}
