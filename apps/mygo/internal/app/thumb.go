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

	"github.com/egoist/mygo/ui"
	xdraw "golang.org/x/image/draw"

	"trie/internal/fetch"
)

// thumb is a picture decoded once: a work's (workImage).
type thumb struct {
	img  *ui.Bitmap
	done bool
}

// thumbImage is an article's picture at most size wide, kept on disk by
// article and source; 1200 for the cards of 收藏 and the author pages, whose
// file names carry their width (240 has none).
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
