package reading

import (
	"errors"
	"time"

	"trie/internal/extract"
	"trie/internal/library"
)

// Extractors is the extraction a read calls. Fragment, SVG, Whiteboard and Format may be nil: without Fragment a feed's
// body is never read, without Format a body stays as extracted.
type Extractors struct {
	Page func(html, url string, trustedArticle bool) (extract.Extracted, error)
	// Feed is the body a publisher's feed carries for one of the article's addresses; nil when it carries none.
	Feed   func(xml, url string, aliases []string) (*extract.Extracted, error)
	Images func(html string, images map[string]string) (string, error)
	// Fragment reads a body the feed carries; kind is "body" or "summary".
	Fragment func(html, base, kind string) (extract.Extracted, error)
	// SVG is the picture sanitized, "" when it is refused.
	SVG func(source string) (string, error)
	// Whiteboard is an Excalidraw scene file drawn into a sanitized SVG, or "" when it is not one.
	Whiteboard func(source string) (string, error)
	Format     func(html string, snapshot *library.ReaderSourceSnapshot) (extract.Formatted, error)
}

// Worker is the app's extraction, its jobs run one at a time, each on a goroutine of its own, given up after ten
// seconds. Go cannot stop a goroutine, so a job given up runs on to its end in the background, bounded by what a read
// hands it (pages of 2 MB, SVG of 256 KB, scenes of 2 MB) and the extract package's own limits (Readability's 20,000
// elements, the formatter's 24 MB and 50,000 nodes, the SVG sanitizer's 5,000 elements, a scene's 2,000). A panic is the worker's
// crash: that job fails.
var Worker = Extractors{
	Page: func(html, url string, trustedArticle bool) (extract.Extracted, error) {
		return run(func() (extract.Extracted, error) { return extract.ExtractPage(html, url, trustedArticle), nil })
	},
	Feed: func(xml, url string, aliases []string) (*extract.Extracted, error) {
		return run(func() (*extract.Extracted, error) {
			if found, ok := extract.ExtractFeed(xml, url, aliases); ok {
				return &found, nil
			}
			return nil, nil
		})
	},
	Images: func(html string, images map[string]string) (string, error) {
		return run(func() (string, error) { return extract.CacheImages(html, images), nil })
	},
	Fragment: func(html, base, kind string) (extract.Extracted, error) {
		return run(func() (extract.Extracted, error) { return extract.ExtractFeedBody(html, base, kind), nil })
	},
	SVG: func(source string) (string, error) {
		return run(func() (string, error) { return extract.SanitizeSVG(source), nil })
	},
	// The scene is drawn, then the drawing sanitized like any SVG.
	Whiteboard: func(source string) (string, error) {
		return run(func() (string, error) {
			if svg := extract.RenderWhiteboard(source); svg != "" {
				return extract.SanitizeSVG(svg), nil
			}
			return "", nil
		})
	},
	Format: func(html string, snapshot *library.ReaderSourceSnapshot) (extract.Formatted, error) {
		return run(func() (extract.Formatted, error) { return extract.FormatArticleHTML(html, snapshot) })
	},
}

var (
	// queue holds the running job: a job starts once the one before it has finished or been given up.
	queue      = make(chan struct{}, 1)
	jobTimeout = 10 * time.Second

	errJob     = errors.New("正文解析暂时失败")
	errTimeout = errors.New("正文解析超时，请尝试原文阅读")
)

func run[T any](job func() (T, error)) (T, error) {
	queue <- struct{}{}
	defer func() { <-queue }()
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if recover() != nil {
				done <- result{err: errJob}
			}
		}()
		v, err := job()
		if err != nil {
			err = errJob
		}
		done <- result{v, err}
	}()
	timer := time.NewTimer(jobTimeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.value, r.err
	case <-timer.C:
		var zero T
		return zero, errTimeout
	}
}
