package reader

import (
	"maps"
	"strings"
	"testing"
	"time"

	"trie/internal/theme"

	"github.com/egoist/mygo/ui"
)

// showTable shows a body of html in a reader w wide.
func showTable(t *testing.T, html string, w int) (*Body, *ui.Tester) {
	t.Helper()
	c := &Content{Status: "ready", HTML: html}
	body := Prepare(c)
	r := NewReader(&Article{ID: "table", Title: "Table"}, c, body)
	th := theme.UI()
	tt := ui.NewTester(func(ctx *ui.Context) { ctx.SetTheme(th); r.View(ctx) }, w, 900)
	tt.Frame()
	tt.Frame()
	return body, tt
}

// cellAt is the rectangle of the cell whose words are s.
func cellAt(t *testing.T, tt *ui.Tester, s string) ui.Rect {
	t.Helper()
	r, ok := tt.Find(s)
	if !ok {
		t.Fatalf("no cell %q in %q", s, tt.Texts())
	}
	return r
}

// Cells go where a browser's table puts them: after those reaching down into their row from above (rowspan), a cell
// across columns (colspan) taking them all; the next row starts again at the first column.
func TestTableCellsPlaced(t *testing.T) {
	body, tt := showTable(t, `<table>
		<tr><td>Singapore</td><td>9 AM - 12:30 PM</td></tr>
		<tr><td colspan="2"><p><em>If you live in a different region, your meeting time would be:</em></p></td></tr>
		<tr><td rowspan="2">Seoul</td><td>10 AM - 1:30 PM</td></tr>
		<tr><td>11 AM - 2:30 PM</td></tr>
		<tr><td>Sydney</td><td>12 PM - 3:30 PM</td></tr>
		<tr><td>Chicago</td><td>7 PM - 10:30 PM</td><td>†</td></tr>
	</table>`, 724)
	got := map[string]int{}
	walkBlocks(body.blocks, func(b *block) {
		for _, row := range b.rows {
			for _, cl := range row {
				got[strings.TrimSpace(cellText(cl))] = cl.col
			}
		}
	})
	want := map[string]int{"Singapore": 0, "9 AM - 12:30 PM": 1, "If you live in a different region, your meeting time would be:": 0,
		"Seoul": 0, "10 AM - 1:30 PM": 1, "11 AM - 2:30 PM": 1, "Sydney": 0, "12 PM - 3:30 PM": 1, "Chicago": 0, "7 PM - 10:30 PM": 1, "†": 2}
	if !maps.Equal(got, want) {
		t.Errorf("columns %v, want %v", got, want)
	}
	left, right, third := cellAt(t, tt, "Singapore"), cellAt(t, tt, "9 AM - 12:30 PM"), cellAt(t, tt, "†")
	for s, col := range want {
		r := cellAt(t, tt, s)
		if x := []float32{left.X, right.X, third.X}[col]; r.X != x {
			t.Errorf("%q at x %.1f, want %.1f (column %d)", s, r.X, x, col)
		}
	}
	if seoul, ten, eleven := cellAt(t, tt, "Seoul"), cellAt(t, tt, "10 AM - 1:30 PM"), cellAt(t, tt, "11 AM - 2:30 PM"); eleven.Y <= ten.Y || seoul.Y != ten.Y {
		t.Errorf("Seoul %v, 10 AM %v, 11 AM %v: the rows under Seoul's", seoul, ten, eleven)
	}
	if wide := cellAt(t, tt, "If you live in a different region, your meeting time would be:"); wide.H > left.H*1.5 {
		t.Errorf("the cell across both columns is %v, more than a line", wide)
	}
}

// A column is never narrower than its longest word, even past the 360 a column otherwise keeps to, nor are those a cell
// across columns covers: no word breaks, none is set a letter to a line; the table scrolls instead.
func TestTableColumnsHoldTheirWords(t *testing.T) {
	long := "PneumonoultramicroscopicsilicovolcanoconiosesAntidisestablishmentarianism"
	prose := strings.Repeat("words that wrap in a column of their own ", 4)
	_, tt := showTable(t, `<table>
		<tr><th>A</th><th>B</th><th>Term</th><th>Meaning</th></tr>
		<tr><td>x</td><td>y</td><td>`+long+`</td><td>`+prose+`</td></tr>
		<tr><td colspan="2">Internationalization</td><td>Lung</td><td>`+prose+`</td></tr>
	</table>`, 724)
	line := cellAt(t, tt, "Lung").H
	for _, s := range []string{long, "Internationalization"} {
		if r := cellAt(t, tt, s); r.H == 0 || r.H > line*1.5 {
			t.Errorf("%q is %v, not one line of %.1f", s, r, line)
		}
	}
}

// A table spanning absurdly wide and deep is placed in time and in no more than maxColumns columns.
func TestTableSpansBounded(t *testing.T) {
	var rows [][]*cell
	for range 150 {
		rows = append(rows, []*cell{{colSpan: 1000, rowSpan: 150}, {colSpan: 1, rowSpan: 1}})
	}
	start := time.Now()
	place(rows)
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Errorf("placing took %v", took)
	}
	if n := columns(rows); n > maxColumns {
		t.Errorf("%d columns, more than %d", n, maxColumns)
	}
	// Spanned from above, the next row's cells start after the span.
	ok := [][]*cell{{{colSpan: 1, rowSpan: 2}, {colSpan: 1, rowSpan: 1}}, {{colSpan: 1, rowSpan: 1}}}
	place(ok)
	if ok[1][0].col != 1 {
		t.Errorf("a cell under a rowspan starts in column %d, not 1", ok[1][0].col)
	}
}
