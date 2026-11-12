package app

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	xdraw "golang.org/x/image/draw"

	"trie/internal/fetch"
	"trie/internal/motion"
	"trie/internal/theme"
)

// A list row's picture: the article's picture shrunk to 240 wide, kept on disk by article and source,
// fetched once it is near the view; one that never comes gives its room back.
type thumb struct {
	img     *ui.Bitmap
	done    bool // loaded, or known to be missing
	missing bool
	shown   time.Time // when it came, to come into focus
}

func thumbKey(id string, index int) string { return id + "\x00" + strconv.Itoa(index) }

// thumbShows reports whether a row should keep room for its picture: until
// it is known missing.
func (a *App) thumbShows(id string, index int) bool {
	t := a.thumbs[thumbKey(id, index)]
	return t == nil || !t.missing
}

// thumbView is the picture's frame, asking for the picture when near.
func (a *App) thumbView(c *ui.Context, id string, index int, near bool) ui.Element {
	key := thumbKey(id, index)
	t := a.thumbs[key]
	// A page laid out unseen (warm.go) asks for nothing: its pictures are
	// read, or fetched, once it shows.
	if t == nil && near && !a.warming {
		t = &thumb{}
		a.thumbs[key] = t
		load := a.opt.Thumbs
		if load == nil {
			load = a.loadThumb
		}
		a.do(func() func() {
			img, err := load(id, index)
			return func() {
				t.done, t.img, t.missing = true, img, err != nil || img == nil
			}
		})
	}
	frame := ui.Box(c.Key("thumb:"+strconv.Itoa(index))).Radius(6).ClipX().ClipY().Background(theme.Surface).
		InsetShadow(0, 0, 0, 1, theme.LineSoft).Shrink(0).AccessHidden(true)
	return frame.Children(func() {
		if t == nil || t.img == nil {
			return
		}
		img := ui.Image(c, t.img).Fit(ui.Cover).Fill()
		// It comes into focus: its fade over 280 ms, its blur and scale over
		// 560; under less motion only the fade.
		if t.shown.IsZero() {
			t.shown = c.Now()
		}
		since := c.Now().Sub(t.shown)
		if since >= motion.ThumbFocus {
			return
		}
		c.AnimationFrame()
		img.Opacity(min(1, float32(since)/float32(motion.ThumbFade)))
		if a.still(c) {
			return
		}
		e := motion.Ease(float32(since) / float32(motion.ThumbFocus))
		if blur := 6 * (1 - e); blur > 0.05 {
			img.Blur(blur)
		}
		img.TransformOrigin(0.5, 0.5).Scale(1.04-0.04*e, 1.04-0.04*e)
	})
}

// loadThumb is the main process's library:thumb at 240 wide.
func (a *App) loadThumb(id string, index int) (*ui.Bitmap, error) {
	return a.thumbFrom(id, index, true)
}

// thumbFrom is a row's picture from the thumbnails kept on disk, else, when
// fetching, read and kept.
func (a *App) thumbFrom(id string, index int, fetching bool) (*ui.Bitmap, error) {
	img, err := a.thumbImage(id, index, 240, fetching)
	if img == nil {
		return nil, err
	}
	return ui.NewBitmap(img), nil
}

// thumbImage is an article's picture at most size wide: 240 for list rows,
// 1200 for 收藏's pictures, whose file names carry their width.
func (a *App) thumbImage(id string, index, size int, fetching bool) (image.Image, error) {
	src, err := a.opt.Store.Picture(id, index)
	if err != nil || src == "" {
		return nil, err
	}
	sum := sha1.Sum([]byte(id + src[:min(len(src), 4096)]))
	name, quality := hex.EncodeToString(sum[:]), 78
	if size != 240 {
		name, quality = name+"-"+strconv.Itoa(size), 82
	}
	file := filepath.Join(a.opt.DataDir, "thumbs", name+".jpg")
	if data, err := os.ReadFile(file); err == nil {
		return decodeAny(data), nil
	}
	if !fetching && !strings.HasPrefix(src, "data:") {
		return nil, os.ErrNotExist
	}
	var body []byte
	if strings.HasPrefix(src, "data:") {
		_, data, _ := strings.Cut(src, ",")
		if body, err = base64.StdEncoding.DecodeString(data); err != nil {
			return nil, err
		}
	} else {
		res, err := fetch.New(6).Fetch(bg, src, func(string) bool { return true }, 8*1024*1024)
		if err != nil {
			return nil, err
		}
		if res.Status < 200 || res.Status >= 300 {
			return nil, nil
		}
		body = res.Body
	}
	img := decodeAny(body)
	if img == nil {
		return nil, nil
	}
	if w := img.Bounds().Dx(); w > size {
		h := img.Bounds().Dy() * size / w
		small := image.NewRGBA(image.Rect(0, 0, size, max(1, h)))
		xdraw.CatmullRom.Scale(small, small.Bounds(), img, img.Bounds(), xdraw.Src, nil)
		img = small
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	if os.MkdirAll(filepath.Dir(file), 0o755) == nil {
		os.WriteFile(file, buf.Bytes(), 0o644)
	}
	return decodeAny(buf.Bytes()), nil
}
