package explore

import (
	"context"
	"os"
	"path/filepath"
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
		start := time.Now()
		p, err := TakePack(ctx, page, dir)
		cancel()
		if err != nil {
			t.Errorf("%d %s: %v", i, page, err)
			continue
		}
		t.Logf("%s: %d screens (cut %v), %d characters, %d pictures, %d fonts, %d refused, in %s",
			page, len(p.Screens), p.Cut, p.Text, p.Images, p.Fonts, p.Refused, time.Since(start).Round(time.Second))
		if len(p.Screens) == 0 || p.Text == 0 {
			t.Errorf("%s: an empty pack", page)
		}
	}
}
