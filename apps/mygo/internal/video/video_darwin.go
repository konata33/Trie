//go:build darwin

package video

import (
	"errors"
	"image"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo/ui"
)

// A Decoder reads a video file's first video track with AVAssetReader:
// each frame decoded by the system, in hardware where it can, scaled to the
// size Open gave and handed over as 32-bit BGRA, which Next copies into one
// RGBA picture it draws every frame into. Each frame shows until the next
// one's presentation time; Rewind starts a new reader. A player calls it
// from one goroutine at a time.
type Decoder struct {
	o        *objects
	w, h     int // the frames' size
	length   time.Duration
	img      *image.RGBA
	aheadAt  time.Duration // when o.ahead shows
	last     time.Duration // the last frame's duration, for one the next does not time
	ended    bool
	shown    atomic.Int64 // the presentation time of the frame Next returned last
	released bool
}

// objects are what a decoder holds of AVFoundation's, retained: the file,
// its track, the reader and its output, and the sample read ahead (the one
// after the frame Next returned, read for its time). Close releases them,
// or the decoder's cleanup once it is dropped.
type objects struct{ asset, track, reader, output, ahead uintptr }

// stop releases the reader, its output and the sample read ahead.
func (o *objects) stop() {
	if o.reader != 0 {
		objc.ID(o.reader).Send(selCancel)
	}
	for _, p := range []*uintptr{&o.ahead, &o.output, &o.reader} {
		if *p != 0 {
			cfRelease(*p)
			*p = 0
		}
	}
}

func (o *objects) release() {
	pooled(func() {
		o.stop()
		for _, p := range []*uintptr{&o.track, &o.asset} {
			if *p != 0 {
				cfRelease(*p)
				*p = 0
			}
		}
	})
}

// pooled runs fn on one thread, in an autorelease pool of its own, which
// takes what AVFoundation autoreleases meanwhile.
func pooled(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := poolPush()
	defer poolPop(pool)
	fn()
}

// Open opens the video file at path, its frames scaled down to cover
// boxW×boxH pixels (0 for their own size), for Next to read from the start.
func Open(path string, boxW, boxH int) (*Decoder, error) {
	if err := load(); err != nil {
		return nil, err
	}
	var d *Decoder
	var err error
	pooled(func() { d, err = open(path, boxW, boxH) })
	return d, err
}

func open(path string, boxW, boxH int) (*Decoder, error) {
	url := objc.ID(class("NSURL")).Send(sel("fileURLWithPath:"), nsString(path))
	asset := objc.ID(class("AVURLAsset")).Send(sel("URLAssetWithURL:options:"), url, objc.ID(0))
	if asset == 0 {
		return nil, errors.New("video: the file does not open")
	}
	tracks := asset.Send(sel("tracksWithMediaType:"), mediaVideo)
	if tracks == 0 || objc.Send[uint](tracks, sel("count")) == 0 {
		return nil, errors.New("video: the file has no video that decodes")
	}
	track := tracks.Send(sel("objectAtIndex:"), uint(0))
	descs := track.Send(sel("formatDescriptions"))
	if descs == 0 || objc.Send[uint](descs, sel("count")) == 0 {
		return nil, errors.New("video: the video has no format")
	}
	// CMVideoDimensions, two int32s, comes back in one register.
	dims := videoDimensions(uintptr(descs.Send(sel("objectAtIndex:"), uint(0))))
	w, h := fit(int(int32(dims)), int(int32(dims>>32)), boxW, boxH)
	if w <= 0 || h <= 0 {
		return nil, errors.New("video: the video has no size")
	}
	length := objc.Send[cmTime](asset, sel("duration"))
	asset.Send(selRetain)
	track.Send(selRetain)
	d := &Decoder{o: &objects{asset: uintptr(asset), track: uintptr(track)}, w: w, h: h, length: length.duration()}
	runtime.AddCleanup(d, (*objects).release, d.o)
	if err := d.start(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// start reads the track from its start, frames as 32-bit BGRA at the
// decoder's size.
func (d *Decoder) start() error {
	var nserr objc.ID
	reader := objc.ID(class("AVAssetReader")).Send(sel("assetReaderWithAsset:error:"), objc.ID(d.o.asset), unsafe.Pointer(&nserr))
	if reader == 0 {
		return describe(nserr, "the video does not read")
	}
	settings := objc.ID(class("NSMutableDictionary")).Send(sel("dictionary"))
	number := objc.ID(class("NSNumber"))
	settings.Send(selSetObject, number.Send(sel("numberWithUnsignedInt:"), uint32(0x42475241)), keyFormat) // 'BGRA'
	settings.Send(selSetObject, number.Send(sel("numberWithInt:"), int32(d.w)), keyWidth)
	settings.Send(selSetObject, number.Send(sel("numberWithInt:"), int32(d.h)), keyHeight)
	output := objc.ID(class("AVAssetReaderTrackOutput")).Send(sel("assetReaderTrackOutputWithTrack:outputSettings:"), objc.ID(d.o.track), settings)
	if output == 0 || !objc.Send[bool](reader, sel("canAddOutput:"), output) {
		return errors.New("video: the video does not decode")
	}
	output.Send(sel("setAlwaysCopiesSampleData:"), false)
	reader.Send(sel("addOutput:"), output)
	d.o.reader, d.o.output = uintptr(reader.Send(selRetain)), uintptr(output.Send(selRetain))
	if !objc.Send[bool](reader, sel("startReading")) {
		return describe(reader.Send(sel("error")), "the video does not decode")
	}
	d.ended = false
	return nil
}

// read returns the next sample with an image, retained, and its
// presentation time; io.EOF after the last.
func (d *Decoder) read() (uintptr, time.Duration, error) {
	for {
		s := uintptr(objc.ID(d.o.output).Send(selCopyNext))
		if s == 0 {
			reader := objc.ID(d.o.reader)
			if objc.Send[int](reader, selStatus) == 2 { // AVAssetReaderStatusCompleted
				return 0, 0, io.EOF
			}
			return 0, 0, describe(reader.Send(sel("error")), "the video stopped decoding")
		}
		if sampleImage(s) == 0 {
			cfRelease(s) // a sample with no picture, as a marker
			continue
		}
		var t sampleTiming
		sampleTimingInfo(s, 0, &t)
		return s, t.PTS.duration(), nil
	}
}

// Next returns the next frame, drawn into the picture of the one before,
// with how long it shows: until the next frame's presentation time, and the
// last until the video's end.
func (d *Decoder) Next() (ui.VideoFrame, error) {
	if d.released {
		return ui.VideoFrame{}, errors.New("video: closed")
	}
	if d.ended {
		return ui.VideoFrame{}, io.EOF
	}
	var f ui.VideoFrame
	var err error
	pooled(func() { f, err = d.next() })
	return f, err
}

func (d *Decoder) next() (ui.VideoFrame, error) {
	if d.o.ahead == 0 { // the first frame: the reader just started
		s, at, err := d.read()
		if err == io.EOF {
			err = errors.New("video: no frame decodes") // a file cut short, before its first
		}
		if err != nil {
			return ui.VideoFrame{}, err
		}
		d.o.ahead, d.aheadAt = s, at
	}
	s, at := d.o.ahead, d.aheadAt
	d.o.ahead = 0
	defer cfRelease(s)
	next, nextAt, err := d.read()
	switch {
	case err == nil:
		d.o.ahead, d.aheadAt = next, nextAt
		d.last = nextAt - at
	case err == io.EOF:
		d.ended = true
		if d.length > at {
			d.last = d.length - at
		}
	default:
		return ui.VideoFrame{}, err
	}
	d.draw(sampleImage(s))
	d.shown.Store(int64(at))
	return ui.VideoFrame{Image: d.img, Duration: d.last}, nil
}

// draw copies a decoded BGRA picture into the decoder's RGBA one.
func (d *Decoder) draw(pb uintptr) {
	lockBase(pb, 1) // kCVPixelBufferLock_ReadOnly
	defer unlockBase(pb, 1)
	w, h, stride := int(pixelWidth(pb)), int(pixelHeight(pb)), int(bytesPerRow(pb))
	base := baseAddress(pb)
	if d.img == nil || d.img.Rect.Dx() != w || d.img.Rect.Dy() != h {
		d.img = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	if base == nil || w <= 0 || h <= 0 {
		return
	}
	src := unsafe.Slice((*byte)(base), stride*(h-1)+4*w)
	for y := range h {
		swap(d.img.Pix[y*d.img.Stride:y*d.img.Stride+4*w], src[y*stride:y*stride+4*w])
	}
}

// Rewind goes back to before the first frame, with a new reader.
func (d *Decoder) Rewind() error {
	if d.released {
		return errors.New("video: closed")
	}
	var err error
	pooled(func() {
		d.o.stop()
		d.shown.Store(0)
		err = d.start()
	})
	return err
}

// Close releases the reader and the file; Next fails after it.
func (d *Decoder) Close() error {
	if d.released {
		return nil
	}
	d.released = true
	d.o.release()
	return nil
}

// Size is the frames' size in pixels.
func (d *Decoder) Size() (w, h int) { return d.w, d.h }

// Progress is how far through the video the frame Next returned last
// begins, from 0 to 1, as the frames decoded ahead of the one showing are
// a frame or two further on. Any goroutine may ask.
func (d *Decoder) Progress() float32 {
	if d.length <= 0 {
		return 0
	}
	return min(1, float32(d.shown.Load())/float32(d.length))
}

// cmTime is CoreMedia's CMTime.
type cmTime struct {
	Value     int64
	Timescale int32
	Flags     uint32
	Epoch     int64
}

func (t cmTime) duration() time.Duration {
	if t.Flags&1 == 0 { // kCMTimeFlags_Valid
		return 0
	}
	return ticks(t.Value, t.Timescale)
}

// sampleTiming is CMSampleTimingInfo.
type sampleTiming struct{ Duration, PTS, DTS cmTime }

var (
	loadOnce sync.Once
	loadErr  error

	cfRelease        func(uintptr)
	poolPush         func() uintptr
	poolPop          func(uintptr)
	sampleImage      func(sample uintptr) uintptr
	sampleTimingInfo func(sample uintptr, index int, out *sampleTiming) int32
	videoDimensions  func(desc uintptr) uint64
	lockBase         func(pb uintptr, flags uint64) int32
	unlockBase       func(pb uintptr, flags uint64) int32
	baseAddress      func(pb uintptr) unsafe.Pointer
	bytesPerRow      func(pb uintptr) uintptr
	pixelWidth       func(pb uintptr) uintptr
	pixelHeight      func(pb uintptr) uintptr

	mediaVideo, keyFormat, keyWidth, keyHeight objc.ID

	selRetain, selCancel, selCopyNext, selStatus, selSetObject objc.SEL
)

// load opens AVFoundation and what its frames come in, once.
func load() error {
	loadOnce.Do(func() {
		open := func(name string) uintptr {
			h, err := purego.Dlopen("/System/Library/Frameworks/"+name+".framework/"+name, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
			if err != nil && loadErr == nil {
				loadErr = err
			}
			return h
		}
		cf, cm, cv, av := open("CoreFoundation"), open("CoreMedia"), open("CoreVideo"), open("AVFoundation")
		libobjc, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil || loadErr != nil {
			loadErr = errors.Join(loadErr, err)
			return
		}
		constant := func(lib uintptr, name string) objc.ID {
			p, err := purego.Dlsym(lib, name)
			if err != nil || p == 0 {
				loadErr = errors.Join(loadErr, err, errors.New("video: no "+name))
				return 0
			}
			return **(**objc.ID)(unsafe.Pointer(&p))
		}
		mediaVideo = constant(av, "AVMediaTypeVideo")
		keyFormat = constant(cv, "kCVPixelBufferPixelFormatTypeKey")
		keyWidth = constant(cv, "kCVPixelBufferWidthKey")
		keyHeight = constant(cv, "kCVPixelBufferHeightKey")
		if loadErr != nil {
			return
		}
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&poolPush, libobjc, "objc_autoreleasePoolPush")
		purego.RegisterLibFunc(&poolPop, libobjc, "objc_autoreleasePoolPop")
		purego.RegisterLibFunc(&sampleImage, cm, "CMSampleBufferGetImageBuffer")
		purego.RegisterLibFunc(&sampleTimingInfo, cm, "CMSampleBufferGetSampleTimingInfo")
		purego.RegisterLibFunc(&videoDimensions, cm, "CMVideoFormatDescriptionGetDimensions")
		purego.RegisterLibFunc(&lockBase, cv, "CVPixelBufferLockBaseAddress")
		purego.RegisterLibFunc(&unlockBase, cv, "CVPixelBufferUnlockBaseAddress")
		purego.RegisterLibFunc(&baseAddress, cv, "CVPixelBufferGetBaseAddress")
		purego.RegisterLibFunc(&bytesPerRow, cv, "CVPixelBufferGetBytesPerRow")
		purego.RegisterLibFunc(&pixelWidth, cv, "CVPixelBufferGetWidth")
		purego.RegisterLibFunc(&pixelHeight, cv, "CVPixelBufferGetHeight")
		selRetain, selCancel, selCopyNext, selStatus = sel("retain"), sel("cancelReading"), sel("copyNextSampleBuffer"), sel("status")
		selSetObject = sel("setObject:forKey:")
	})
	return loadErr
}

func sel(name string) objc.SEL { return objc.RegisterName(name) }

func class(name string) objc.Class { return objc.GetClass(name) }

func nsString(s string) objc.ID {
	return objc.ID(class("NSString")).Send(sel("stringWithUTF8String:"), s)
}

// describe is an NSError's description as an error, or fallback's.
func describe(nserr objc.ID, fallback string) error {
	if nserr != 0 {
		if s := objc.Send[string](nserr.Send(sel("localizedDescription")), sel("UTF8String")); s != "" {
			return errors.New("video: " + s)
		}
	}
	return errors.New("video: " + fallback)
}
