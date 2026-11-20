package explore

import (
	"errors"
	"image"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

// snapshot is win's page as it shows now, at its window's scale, read from the pixels WebKit draws it in, off the
// main thread. Window.CapturePage encodes the picture as PNG on the main thread, a few hundred milliseconds for a
// capture's 2880 × 1800, which the app's own frames wait for, and the PNG is then decoded again; this does neither,
// and falls back to it only when the pixels come in a layout it does not read.
func snapshot(win *mygo.Window) (image.Image, error) {
	c, err := quartz()
	if err != nil {
		return capturePNG(win)
	}
	type answer struct {
		pic uintptr // a CGImage, retained, or 0
		why string
	}
	var mu sync.Mutex
	gaveUp := false
	got := make(chan answer, 1)
	mygo.RunOnMain(func() {
		web := webViewIn(objc.ID(win.NativeHandle()).Send(objc.RegisterName("contentView")))
		if web == 0 {
			got <- answer{why: "no web view"}
			return
		}
		var blk objc.Block
		blk = objc.NewBlock(func(_ objc.Block, img, nserr objc.ID) {
			defer blk.Release()
			a := answer{why: "no picture"}
			if img != 0 {
				a.pic = uintptr(img.Send(objc.RegisterName("CGImageForProposedRect:context:hints:"), uintptr(0), uintptr(0), uintptr(0)))
			} else if nserr != 0 {
				a.why = objc.Send[string](nserr.Send(objc.RegisterName("localizedDescription")), objc.RegisterName("UTF8String"))
			}
			mu.Lock()
			defer mu.Unlock()
			if a.pic != 0 && !gaveUp {
				c.retain(a.pic)
			} else {
				a.pic = 0
			}
			got <- a
		})
		web.Send(objc.RegisterName("takeSnapshotWithConfiguration:completionHandler:"), uintptr(0), blk)
	})
	var a answer
	select {
	case a = <-got:
	case <-time.After(10 * time.Second):
		mu.Lock()
		gaveUp = true
		select {
		case a = <-got: // it came as the wait ran out, retained
		default:
			a.why = "no answer in 10 s"
		}
		mu.Unlock()
	}
	if a.pic == 0 {
		// WebKit's own capture, once more: what failed may have been a moment's.
		if img, err := capturePNG(win); err == nil {
			return img, nil
		}
		return nil, errors.New("explore: WebKit took no picture of the page: " + a.why)
	}
	defer c.release(a.pic)
	if img := c.pixels(a.pic); img != nil {
		return img, nil
	}
	return capturePNG(win)
}

// quartzCalls are the CoreGraphics and CoreFoundation functions a snapshot's pixels are read with.
type quartzCalls struct {
	width, height, bytesPerRow, bitsPerPixel, bitsPerComponent func(uintptr) uintptr
	bitmapInfo                                                 func(uintptr) uint32
	provider, copyData, retain                                 func(uintptr) uintptr
	bytes                                                      func(uintptr) unsafe.Pointer
	length                                                     func(uintptr) int
	release                                                    func(uintptr)
}

var quartz = sync.OnceValues(func() (*quartzCalls, error) {
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return nil, err
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return nil, err
	}
	c := &quartzCalls{}
	purego.RegisterLibFunc(&c.width, cg, "CGImageGetWidth")
	purego.RegisterLibFunc(&c.height, cg, "CGImageGetHeight")
	purego.RegisterLibFunc(&c.bytesPerRow, cg, "CGImageGetBytesPerRow")
	purego.RegisterLibFunc(&c.bitsPerPixel, cg, "CGImageGetBitsPerPixel")
	purego.RegisterLibFunc(&c.bitsPerComponent, cg, "CGImageGetBitsPerComponent")
	purego.RegisterLibFunc(&c.bitmapInfo, cg, "CGImageGetBitmapInfo")
	purego.RegisterLibFunc(&c.provider, cg, "CGImageGetDataProvider")
	purego.RegisterLibFunc(&c.copyData, cg, "CGDataProviderCopyData")
	purego.RegisterLibFunc(&c.bytes, cf, "CFDataGetBytePtr")
	purego.RegisterLibFunc(&c.length, cf, "CFDataGetLength")
	purego.RegisterLibFunc(&c.retain, cf, "CFRetain")
	purego.RegisterLibFunc(&c.release, cf, "CFRelease")
	return c, nil
})

// pixels copies a CGImage of 8-bit RGB, with alpha or a padding byte, in either byte order, into an image; nil for any
// other layout: 10-bit or float components, a packed pixel format (info&0xF0000), or four colours and no alpha (CMYK).
func (c *quartzCalls) pixels(pic uintptr) image.Image {
	w, h, stride := int(c.width(pic)), int(c.height(pic)), int(c.bytesPerRow(pic))
	info := c.bitmapInfo(pic)
	alpha, order := info&0x1f, info&0x7000
	if c.bitsPerPixel(pic) != 32 || c.bitsPerComponent(pic) != 8 || info&0xF0100 != 0 || w == 0 || h == 0 || stride < 4*w ||
		alpha < 1 || alpha > 6 || order != 0 && order != 0x2000 && order != 0x4000 {
		return nil
	}
	data := c.copyData(c.provider(pic))
	if data == 0 {
		return nil
	}
	defer c.release(data)
	if c.length(data) < stride*(h-1)+4*w {
		return nil
	}
	src := unsafe.Slice((*byte)(c.bytes(data)), c.length(data))
	// Where red, green, blue and alpha are in each pixel's four bytes: alpha (or padding) first or last in the 32-bit
	// word (CGImageAlphaInfo 2, 4, 6 first), the word little-endian (kCGBitmapByteOrder32Little) or not.
	first := alpha == 2 || alpha == 4 || alpha == 6
	var r, g, b, a int
	switch little := order == 0x2000; {
	case little && first:
		b, g, r, a = 0, 1, 2, 3
	case little:
		a, b, g, r = 0, 1, 2, 3
	case first:
		a, r, g, b = 0, 1, 2, 3
	default:
		r, g, b, a = 0, 1, 2, 3
	}
	opaque := alpha == 5 || alpha == 6
	rect := image.Rect(0, 0, w, h)
	var pix []byte
	var img image.Image
	if alpha == 3 || alpha == 4 { // not premultiplied
		n := image.NewNRGBA(rect)
		pix, img = n.Pix, n
	} else {
		n := image.NewRGBA(rect)
		pix, img = n.Pix, n
	}
	for y := range h {
		row, out := src[y*stride:y*stride+4*w], pix[y*4*w:(y+1)*4*w]
		for x := 0; x < 4*w; x += 4 {
			out[x], out[x+1], out[x+2], out[x+3] = row[x+r], row[x+g], row[x+b], row[x+a]
			if opaque {
				out[x+3] = 255
			}
		}
	}
	return img
}

// keptFiles is what WebKit holds of win's page, as it saves a page as a web archive: the files it loaded and still
// uses (style sheets, pictures, fonts, scripts), each with its address and type, read from memory and not asked for
// again. A file a script loaded for itself, as a WebGL texture, is not among them. The archive is read off the main
// thread, as one of a few hundred files takes tens of milliseconds the app's frames would wait for.
func keptFiles(win *mygo.Window) ([]keptFile, error) {
	var mu sync.Mutex
	gaveUp := false
	got := make(chan objc.ID, 1) // the archive's data, retained, or 0
	mygo.RunOnMain(func() {
		web := webViewIn(objc.ID(win.NativeHandle()).Send(objc.RegisterName("contentView")))
		if web == 0 {
			got <- 0
			return
		}
		var blk objc.Block
		blk = objc.NewBlock(func(_ objc.Block, data, _ objc.ID) {
			defer blk.Release()
			mu.Lock()
			defer mu.Unlock()
			if data == 0 || gaveUp {
				got <- 0
				return
			}
			got <- data.Send(objc.RegisterName("retain"))
		})
		web.Send(objc.RegisterName("createWebArchiveDataWithCompletionHandler:"), blk)
	})
	var data objc.ID
	late := false
	select {
	case data = <-got:
	case <-time.After(5 * time.Second):
		mu.Lock()
		gaveUp = true
		select {
		case data = <-got: // it came as the wait ran out, retained
		default:
			late = true
		}
		mu.Unlock()
	}
	if late {
		return nil, errors.New("explore: WebKit did not give the page's files")
	}
	if data == 0 {
		return nil, errors.New("explore: WebKit gave none of the page's files")
	}
	// Foundation's autoreleased objects go into a pool of this thread's, which the goroutine keeps until it is done.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	defer data.Send(objc.RegisterName("release"))
	return archived(data), nil
}

// webViewIn is the first WKWebView in view's tree, or 0.
func webViewIn(view objc.ID) objc.ID {
	if view == 0 {
		return 0
	}
	if objc.Send[bool](view, objc.RegisterName("isKindOfClass:"), objc.ID(objc.GetClass("WKWebView"))) {
		return view
	}
	subs := view.Send(objc.RegisterName("subviews"))
	for i := range objc.Send[uint](subs, objc.RegisterName("count")) {
		if web := webViewIn(subs.Send(objc.RegisterName("objectAtIndex:"), i)); web != 0 {
			return web
		}
	}
	return 0
}

// archived is the files a web archive holds besides the page itself (its WebSubresources), read while the archive's
// data is held and an autorelease pool is in place.
func archived(data objc.ID) []keptFile {
	if data == 0 {
		return nil
	}
	list := objc.ID(objc.GetClass("NSPropertyListSerialization")).Send(objc.RegisterName("propertyListWithData:options:format:error:"), data, uint(0), uintptr(0), uintptr(0))
	key := func(dict objc.ID, k string) objc.ID {
		return dict.Send(objc.RegisterName("objectForKey:"), objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), k))
	}
	text := func(s objc.ID) string {
		if s == 0 {
			return ""
		}
		return objc.Send[string](s, objc.RegisterName("UTF8String"))
	}
	subs := key(list, "WebSubresources")
	var out []keptFile
	for i := range objc.Send[uint](subs, objc.RegisterName("count")) {
		r := subs.Send(objc.RegisterName("objectAtIndex:"), i)
		body := key(r, "WebResourceData")
		n := objc.Send[uint](body, objc.RegisterName("length"))
		if n == 0 || n > packFileBytes {
			continue
		}
		f := keptFile{URL: text(key(r, "WebResourceURL")), Type: text(key(r, "WebResourceMIMEType")), Data: make([]byte, n)}
		body.Send(objc.RegisterName("getBytes:length:"), unsafe.Pointer(&f.Data[0]), n)
		out = append(out, f)
	}
	return out
}
