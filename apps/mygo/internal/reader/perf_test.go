package reader

import (
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"trie/internal/theme"

	"github.com/egoist/mygo/ui"
)

// TestPerformance logs how long the longest articles take to prepare, to
// show first and to scroll a notch at a time, without a window (the CPU
// renderer draws every frame in memory): PERF=1 go test -run Performance -v.
func TestPerformance(t *testing.T) {
	if os.Getenv("PERF") == "" {
		t.Skip("PERF not set")
	}
	for _, title := range []string{"雨のち晴れ", "Pluggable, Extensible, and Playful DevTools", "十个月，给 LobeHub", "UIKitCore 逆向分析报告"} {
		for _, a := range fixture(t) {
			if !strings.HasPrefix(a.Title, title) {
				continue
			}
			start := time.Now()
			c, _ := a.Content()
			decoded := time.Since(start)
			body := Prepare(c)
			prepared := time.Since(start) - decoded
			r := NewReader(a, c, body)
			th := theme.UI()
			start = time.Now()
			tt := ui.NewTester(func(ctx *ui.Context) { ctx.SetTheme(th); r.View(ctx) }, 1000, 820)
			tt.SetScale(2)
			first := time.Since(start)
			var steps []time.Duration
			for i := 0; i < 60; i++ {
				start := time.Now()
				tt.Scroll(500, 400, 0, 120)
				steps = append(steps, time.Since(start))
			}
			sort.Slice(steps, func(i, j int) bool { return steps[i] < steps[j] })
			t.Logf("%s: JSON %v, Prepare %v (%d pictures), first frame %v, scrolling 120 DIPs: median %v, slowest %v, at %.0f of %.0f",
				title, decoded.Round(time.Millisecond), prepared.Round(time.Millisecond), len(body.pics), first.Round(time.Millisecond),
				steps[len(steps)/2].Round(100*time.Microsecond), steps[len(steps)-1].Round(100*time.Microsecond), r.Scroll.Y, r.Scroll.MaxY)
		}
	}
}
