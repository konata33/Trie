//go:build !darwin

package explore

import (
	"errors"
	"image"

	"github.com/egoist/mygo"
)

// keptFiles is pack_darwin.go's: elsewhere every file of a pack is read again.
func keptFiles(*mygo.Window) ([]keptFile, error) {
	return nil, errors.New("explore: no files kept here")
}

// snapshot is pack_darwin.go's: elsewhere the page is captured as PNG and decoded.
func snapshot(win *mygo.Window) (image.Image, error) { return capturePNG(win) }
