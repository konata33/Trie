package explore

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestLivePack makes the reference pack of each of EXPLORE_LIVE_PACK (comma-separated pages) in EXPLORE_LIVE_OUT:
//
//	EXPLORE_LIVE=1 EXPLORE_LIVE_PACK=https://example.com/ EXPLORE_LIVE_OUT=/tmp/packs go test -count=1 -run TestLivePack ./internal/explore
func TestLivePack(t *testing.T) {
	live(t)
	out := os.Getenv("EXPLORE_LIVE_OUT")
	if os.Getenv("EXPLORE_LIVE_PACK") == "" || out == "" {
		t.Skip("EXPLORE_LIVE_PACK or EXPLORE_LIVE_OUT is not set")
	}
	for i, page := range strings.Split(os.Getenv("EXPLORE_LIVE_PACK"), ",") {
		dir := filepath.Join(out, unsafeName.ReplaceAllString(strings.TrimPrefix(strings.TrimPrefix(page, "https://"), "http://"), "-"))
		os.RemoveAll(dir)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		// The last page's adding gives way, as TakePack has it: how long that takes is what a pack waits behind it.
		stopAt := time.Now()
		StopPack("")
		if i > 0 && os.Getenv("EXPLORE_LIVE_MORE") == "" {
			t.Logf("%s: the last pack stopped adding in %s", page, time.Since(stopAt).Round(10*time.Millisecond))
		}
		start := time.Now()
		var heard []float64
		var came time.Time // the page in: the time after is the pack's own
		first := false
		p, err := TakePack(ctx, page, dir, func(done float64, img image.Image) {
			heard = append(heard, done)
			first = first || img != nil
			if done >= 0.05 && came.IsZero() {
				came = time.Now()
			}
		})
		cancel()
		if err != nil {
			t.Errorf("%d %s: %v", i, page, err)
			continue
		}
		t.Logf("%s: %d screens (cut %v), %d characters, markup %v, %d pictures, %d fonts, %d style sheets, %d not saved; page in %s, pack %s after",
			page, len(p.Screens), p.Cut, p.Text, p.Markup, p.Images, p.Fonts, p.Styles, p.Refused,
			came.Sub(start).Round(100*time.Millisecond), time.Since(came).Round(100*time.Millisecond))
		if len(p.Screens) == 0 || p.Text == 0 {
			t.Errorf("%s: an empty pack", page)
		}
		// With EXPLORE_LIVE_MORE set, what is added to the pack once taken is waited for and told.
		if os.Getenv("EXPLORE_LIVE_MORE") != "" {
			at := time.Now()
			adding.Lock()
			done := adding.done
			adding.Unlock()
			if done != nil {
				<-done
			}
			readme, _ := os.ReadFile(filepath.Join(dir, "README.md"))
			t.Logf("%s: added for %s: %d screens in all; %s", page, time.Since(at).Round(100*time.Millisecond),
				strings.Count(string(readme), "\n- `screens/")-1, regexp.MustCompile(`\d+ pictures, \d+ fonts and \d+ style sheets saved(; \d+ more)?`).Find(readme))
		}
		// Files not saved in time on a slow network are told above; none saved at all is a fault.
		if p.Refused > 0 && p.Images+p.Fonts+p.Styles == 0 {
			t.Errorf("%s: no file saved of %d", page, p.Refused)
		}
		if !first || len(heard) == 0 || heard[len(heard)-1] != 1 || !sort.Float64sAreSorted(heard) {
			t.Errorf("%s: progress %v, first screen heard %v", page, heard, first)
		}
	}
}
