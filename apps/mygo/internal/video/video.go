// Package video decodes a work page's recording, kept as a file, for
// ui.VideoPlayer: on macOS through AVFoundation, which decodes H.264 and
// HEVC in hardware, its frames scaled there to the size they show at.
// Other systems have no decoder yet: Open says so.
package video

import (
	"encoding/binary"
	"errors"
	"math"
	"time"
)

// ErrUnsupported is Open's error on a system with no decoder.
var ErrUnsupported = errors.New("video: no decoder on this system")

// fit is the size frames of w×h pixels are scaled to so that they still
// cover boxW×boxH, as ui.Cover shows them, without growing: their own size
// when the box is no smaller, or unknown (0).
func fit(w, h, boxW, boxH int) (int, int) {
	if boxW <= 0 || boxH <= 0 || w <= 0 || h <= 0 {
		return w, h
	}
	k := max(float64(boxW)/float64(w), float64(boxH)/float64(h))
	if k >= 1 {
		return w, h
	}
	return max(1, int(math.Ceil(float64(w)*k))), max(1, int(math.Ceil(float64(h)*k)))
}

// ticks is a media time of value/scale seconds, without overflowing.
func ticks(value int64, scale int32) time.Duration {
	if scale <= 0 {
		return 0
	}
	s := int64(scale)
	return time.Duration(value/s)*time.Second + time.Duration(value%s)*time.Second/time.Duration(s)
}

// swap copies src's BGRA pixels into dst as RGBA, two at a time.
func swap(dst, src []byte) {
	n := len(src) &^ 7
	for i := 0; i < n; i += 8 {
		v := binary.LittleEndian.Uint64(src[i:])
		binary.LittleEndian.PutUint64(dst[i:], v&0xff00ff00ff00ff00|v>>16&0x000000ff000000ff|v&0x000000ff000000ff<<16)
	}
	for i := n; i+3 < len(src); i += 4 {
		dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i+2], src[i+1], src[i], src[i+3]
	}
}
