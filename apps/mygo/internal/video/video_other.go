//go:build !darwin

package video

import "github.com/egoist/mygo/ui"

// A Decoder decodes nothing here: Open returns ErrUnsupported.
type Decoder struct{}

// Open returns ErrUnsupported: only macOS has a decoder yet.
func Open(path string, boxW, boxH int) (*Decoder, error) { return nil, ErrUnsupported }

func (d *Decoder) Next() (ui.VideoFrame, error) { return ui.VideoFrame{}, ErrUnsupported }
func (d *Decoder) Rewind() error                { return ErrUnsupported }
func (d *Decoder) Close() error                 { return nil }
func (d *Decoder) Size() (w, h int)             { return 0, 0 }
func (d *Decoder) Progress() float32            { return 0 }
