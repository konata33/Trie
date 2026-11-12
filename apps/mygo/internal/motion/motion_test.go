package motion

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// sprung returns where a value MyGo animates with a, from 0 to 1, is at
// each time, as its spring solver puts it in a tester's frames.
func sprung(t *testing.T, a ui.Animation, at []time.Duration) []float32 {
	t.Helper()
	target, v := float32(0), float32(0)
	tt := ui.NewTester(func(c *ui.Context) {
		e := ui.Box(c).Size(10, 10)
		v = e.AnimateTo("v", target, a)
	}, 100, 100)
	target = 1000
	tt.Frame()
	start := tt.Now()
	out := make([]float32, len(at))
	for i, d := range at {
		tt.Advance(d - tt.Now().Sub(start))
		tt.Frame()
		out[i] = v / 1000
	}
	return out
}

// The travel spring is styles.css --travel-ease: MyGo's spring passes
// through the stops of its linear() within half a percent, the last stop
// (where linear() jumps the 0.6% still to go) included.
func TestTravelFollowsTravelEase(t *testing.T) {
	for _, d := range []time.Duration{600 * time.Millisecond, 460 * time.Millisecond} {
		n := len(travelStops) - 1
		at := make([]time.Duration, n+1)
		for k := range at {
			at[k] = d * time.Duration(k) / time.Duration(n)
		}
		got := sprung(t, Travel(d), at)
		worst := 0.0
		for k, want := range travelStops {
			diff := math.Abs(float64(got[k] - want))
			worst = max(worst, diff)
			if diff > 0.006 {
				t.Errorf("Travel(%v) at %v: %.4f, --travel-ease %.4f", d, at[k], got[k], want)
			}
			if e := TravelEase(float32(k) / float32(n)); math.Abs(float64(e-want)) > 1e-6 {
				t.Errorf("TravelEase(%d/%d) = %.4f, want %.4f", k, n, e, want)
			}
		}
		t.Logf("Travel(%v), a spring of %v: at most %.4f from --travel-ease's stops", d, Travel(d).Spring.Duration.Round(time.Millisecond/10), worst)
	}
}

// Follow is motion/react's {duration: 0.3, bounce: 0}: about halfway at a
// sixth of it, and within 0.001 of its target at 300 ms.
func TestFollowSettlesAs300msMotionSpring(t *testing.T) {
	got := sprung(t, Follow, []time.Duration{50 * time.Millisecond, 300 * time.Millisecond})
	if got[0] < 0.4 || got[0] > 0.6 {
		t.Errorf("Follow at 50 ms: %.3f, want about half", got[0])
	}
	if 1-got[1] > 0.0015 {
		t.Errorf("Follow at 300 ms: %.4f, want within 0.001 of 1", got[1])
	}
}

// Under less motion nothing moves or flies, and fades and blurs keep
// their time; after a key everything shows at once.
func TestStillAndInstant(t *testing.T) {
	tr := Deep.Crossing().Come()
	tr.Match, tr.Fly = struct{}{}, true
	s := Still(tr)
	if s.Match != nil || s.Fly || s.Enter.Scale != 0 || s.Enter.Blur != Defocus || s.Enter.Opacity != 0 {
		t.Errorf("Still: %+v, enter %+v", s, *s.Enter)
	}
	if s.Animation == nil || s.Animation.Duration != 420*time.Millisecond || s.Animation.Delay != 220*time.Millisecond {
		t.Errorf("Still kept no timing: %+v", s.Animation)
	}
	toast := Still(Toast())
	if toast.Enter.Y != 0 || toast.Exit.Y != 0 || toast.Enter.Blur != 2 {
		t.Errorf("Still(Toast()): enter %+v exit %+v", *toast.Enter, *toast.Exit)
	}
	if tr.Enter.Scale != 1.012 {
		t.Errorf("Still changed what it was given: %+v", *tr.Enter)
	}
	i := Instant(Dialog())
	if i.Duration >= 0 || i.ExitDuration >= 0 || i.Animation != nil {
		t.Errorf("Instant: %+v", i)
	}
}

// A layer grows out of the centre of the button that opened it.
func TestOrigin(t *testing.T) {
	x, y := Origin(ui.Rect{W: 1280, H: 820}, ui.Rect{X: 1180, Y: 12, W: 60, H: 28})
	if x != 1210.0/1280 || y != 26.0/820 {
		t.Errorf("Origin = %v, %v", x, y)
	}
	if x, y := Origin(ui.Rect{W: 1280, H: 820}, ui.Rect{}); x != 0.5 || y != 0.5 {
		t.Errorf("Origin without a button = %v, %v, want the centre", x, y)
	}
}

// The ground lights up, travels and melts as lay and spread time it.
func TestGroundTimes(t *testing.T) {
	for _, c := range []struct {
		g          Ground
		melt, gone time.Duration
	}{{Spread, 520 * time.Millisecond, 800 * time.Millisecond}, {Gather, 440 * time.Millisecond, 660 * time.Millisecond}} {
		k := c.g.Opacity()
		if k[2].At != c.melt || k[3].At != c.gone || k[3].Value != 0 {
			t.Errorf("%+v: %+v", c.g, k)
		}
	}
}
