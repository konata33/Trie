package feed

import (
	"encoding/json"
	"os"
	"testing"
	"time"
	_ "time/tzdata"
)

// testdata/dates.json is reference output recorded from the previous
// implementation (Date.parse and feed dates, in Node) for about two thousand
// spellings, fixed and generated (.context/mygo/feed-port/ts/dates.mjs), in a
// zone with daylight saving time.
func TestDatesMatchV8(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	local := time.Local
	time.Local = zone
	defer func() { time.Local = local }()
	data, err := os.ReadFile("testdata/dates.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input string
		MS    *int64
		Date  *string
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	bad := 0
	for _, c := range cases {
		ms, ok := jsDate(c.Input)
		got := &ms
		if !ok {
			got = nil
		}
		date := feedDate(c.Input)
		if (got == nil) != (c.MS == nil) || got != nil && *got != *c.MS || (date == nil) != (c.Date == nil) || date != nil && *date != *c.Date {
			if bad++; bad <= 20 {
				t.Errorf("%q: Date.parse %v, Go %v; date() %v, Go %v", c.Input, deref(c.MS), deref(got), deref(c.Date), deref(date))
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d differ", bad, len(cases))
	}
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
