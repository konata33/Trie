package app

import "testing"

// Reading down, each heading in turn comes up to the top edge and stays
// there until the next takes its place; scrolled back to the top, none is.
func TestStonesStepOnHeadings(t *testing.T) {
	_, tt, rp := openLobehub(t)
	if n := len(rp.content.Headings); n < 3 {
		t.Fatalf("the article has %d headings", n)
	}
	if rp.foot != -1 {
		t.Fatalf("at the top heading %d is under foot", rp.foot)
	}
	var seen []int
	for range 60 {
		scrollBy(tt, 400)
		if len(seen) > 0 && rp.foot < seen[len(seen)-1] {
			t.Fatalf("reading down went back from heading %d to %d", seen[len(seen)-1], rp.foot)
		}
		if rp.foot >= 0 && (len(seen) == 0 || seen[len(seen)-1] != rp.foot) {
			seen = append(seen, rp.foot)
		}
	}
	if len(seen) < 3 {
		t.Fatalf("reading down came onto %d headings: %v", len(seen), seen)
	}
	scrollBy(tt, -1e6)
	settle(tt)
	if rp.foot != -1 {
		t.Errorf("back at the top heading %d is under foot", rp.foot)
	}
}
