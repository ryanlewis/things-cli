package output

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// A shrink column is cut with an ellipsis to fit maxWidth, and no further than
// its minimum: when the other columns alone overrun, the row still prints.
func TestTableShrink(t *testing.T) {
	tests := []struct {
		name     string
		maxWidth int
		want     []string
	}{
		{"fits", 0, []string{"1.  a long title here  x", "2.  short"}},
		{"cut", 16, []string{"1.  a long t…  x", "2.  short"}},
		{"floor", 8, []string{"1.  a lo…  x", "2.  short"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tbl := &table{
				gap:      columnGap,
				maxWidth: tt.maxWidth,
				shrink:   []shrinkCol{{col: 1, min: 5}},
			}
			tbl.row("1.", "a long title here", "x")
			tbl.row("2.", "short")
			got := tbl.lines()
			if len(got) != len(tt.want) {
				t.Fatalf("got %d lines, want %d: %q", len(got), len(tt.want), got)
			}
			for i, line := range got {
				if line != tt.want[i] {
					t.Errorf("line %d = %q, want %q", i, line, tt.want[i])
				}
			}
		})
	}
}

// A row ends at its last cell with something in it, so a dropped trailing
// column, or an empty one, leaves no padding behind.
func TestTableLastKeptColumnUnpadded(t *testing.T) {
	tbl := &table{gap: columnGap, maxWidth: 14, dropOrder: []int{2}}
	tbl.row("1.", "a", "2026-09-10")
	tbl.row("2.", "longer one", "")
	for i, line := range tbl.lines() {
		if w := lipgloss.Width(line); w > 14 {
			t.Errorf("line %d is %d wide, over 14: %q", i, w, line)
		}
		if line[len(line)-1] == ' ' {
			t.Errorf("line %d ends in padding: %q", i, line)
		}
	}
}

// A column that no row fills takes no space, not even its gap.
func TestTableEmptyColumnTakesNoSpace(t *testing.T) {
	for _, fill := range []string{"", "s"} {
		tbl := &table{gap: columnGap}
		tbl.row("a", fill, "b")
		want := "a  b"
		if fill != "" {
			want = "a  s  b"
		}
		if got := tbl.lines()[0]; got != want {
			t.Errorf("fill %q: got %q, want %q", fill, got, want)
		}
	}
}

// An empty column adds neither width nor gap to the fit, so it never costs
// another column its place. It is left out of dropOrder here so that nothing
// but its being empty can explain the tags surviving at the boundary.
func TestTableEmptyColumnFits(t *testing.T) {
	tests := []struct {
		maxWidth int
		want     string
	}{
		{23, "1.  title  [tag]  due:x"},
		{22, "1.  title  due:x"},
	}
	for _, tt := range tests {
		tbl := &table{
			gap:       columnGap,
			maxWidth:  tt.maxWidth,
			dropOrder: []int{2, 4},
		}
		tbl.row("1.", "title", "[tag]", "", "due:x")
		if got := tbl.lines()[0]; got != tt.want {
			t.Errorf("maxWidth %d: got %q, want %q", tt.maxWidth, got, tt.want)
		}
	}
}

// A tail survives a cut: the ellipsis goes into the text before it, and the
// column goes no narrower than the tail plus one character and the ellipsis.
func TestTableTail(t *testing.T) {
	tests := []struct {
		name     string
		maxWidth int
		want     string
	}{
		{"fits", 0, "1.  a long title (p)"},
		{"cut", 14, "1.  a lon… (p)"},
		{"floor", 4, "1.  a… (p)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tbl := &table{
				gap:      columnGap,
				maxWidth: tt.maxWidth,
				shrink:   []shrinkCol{{col: 1, min: 2}},
			}
			tbl.row("1.", "a long title")
			tbl.tail(1, " (p)")
			if got := tbl.lines()[0]; got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// A tail belongs to its own row's cell: rows without one around it are cut
// as usual, with no tail of their own, to the same column width.
func TestTableTailMixedRows(t *testing.T) {
	tbl := &table{
		gap:      columnGap,
		maxWidth: 14,
		shrink:   []shrinkCol{{col: 1, min: 2}},
	}
	tbl.row("1.", "a long to-do")
	tbl.row("2.", "a long project")
	tbl.tail(1, " (p)")
	tbl.row("3.", "another to-do")
	want := []string{"1.  a long to…", "2.  a lon… (p)", "3.  another t…"}
	got := tbl.lines()
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A shrink column gives way to its soft minimum before a column is dropped,
// takes back what a drop frees beyond the overrun, and goes on to its hard
// minimum only once nothing is left to drop.
func TestTableShrinkSoftFirst(t *testing.T) {
	tests := []struct {
		name     string
		maxWidth int
		want     string
	}{
		{"soft cut keeps every column", 20, "1.  abcdefg…  tag  d"},
		{"drop then regrow", 16, "1.  abcdefgh…  d"},
		{"drop all then hard cut", 10, "1.  abcde…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tbl := &table{
				gap:       columnGap,
				maxWidth:  tt.maxWidth,
				dropOrder: []int{2, 3},
				shrink:    []shrinkCol{{col: 1, min: 4, soft: 8}},
			}
			tbl.row("1.", "abcdefghijklmnop", "tag", "d")
			if got := tbl.lines()[0]; got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// A drop that frees more than the row was over by gives the rest back to the
// title the soft pass cut, rather than leaving it at its soft minimum.
func TestTableRegrowAfterDrop(t *testing.T) {
	title := strings.Repeat("t", 75)
	tbl := &table{
		gap:       columnGap,
		maxWidth:  70,
		dropOrder: []int{3},
		shrink:    []shrinkCol{{col: 2, min: 10, soft: 40}},
	}
	tbl.row("1.", "[ ]", title, "[work, waiting]", "due:2026-10-02")
	// 2+3+40+15+14 plus four gaps is 82: the tags go, freeing 17 where 12
	// was needed, and the title takes back the 5 left over.
	want := "1.  [ ]  " + strings.Repeat("t", 44) + "…  due:2026-10-02"
	if got := tbl.lines()[0]; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// Across every width, a row that can fit does fit, a column dropped at one
// width stays dropped at every narrower one, and regrow never undoes a drop.
func TestTableFitNeverOverruns(t *testing.T) {
	build := func(maxWidth int) *table {
		tbl := &table{
			gap:       columnGap,
			maxWidth:  maxWidth,
			dropOrder: []int{3, 4},
			shrink:    []shrinkCol{{col: 2, min: 10, soft: 40}},
		}
		tbl.row("1.", "[ ]", strings.Repeat("a", 75), "[work, waiting]", "due:2026-10-02")
		tbl.row("2.", "[ ]", "a long project title to cut", "", "2026-09-28")
		tbl.tail(2, " (project)")
		tbl.row("3.", "[ ]", "short", "[x]", "")
		return tbl
	}
	// The narrowest a row can go: number, status and the title at its floor
	// (the tail plus one character and the ellipsis), with two gaps.
	floor := 2 + 3 + len(" (project)") + 2 + 4
	prevKept := -1
	for w := 100; w >= 1; w-- {
		tbl := build(w)
		keep, _, _ := tbl.fit(tbl.widths())
		kept := 0
		for _, k := range keep {
			if k {
				kept++
			}
		}
		if prevKept >= 0 && kept > prevKept {
			t.Errorf("width %d keeps %d columns, more than %d at width %d", w, kept, prevKept, w+1)
		}
		prevKept = kept
		for i, line := range build(w).lines() {
			if lw := lipgloss.Width(line); lw > max(w, floor) {
				t.Errorf("width %d: line %d is %d wide: %q", w, i, lw, line)
			}
		}
	}
}

// A soft minimum below the hard one gives no width up before the drops, as
// shrinkCol says: the tags go first, and the title is cut only once they have.
func TestTableSoftBelowMinDropsFirst(t *testing.T) {
	tbl := &table{
		gap:       columnGap,
		maxWidth:  20,
		dropOrder: []int{2},
		shrink:    []shrinkCol{{col: 1, min: 8, soft: 4}},
	}
	tbl.row("1.", "abcdefghijklmnop", "tag")
	if got, want := tbl.lines()[0], "1.  abcdefghijklmnop"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A row whose last cells are empty, or hold only an empty styled span, ends
// at its last cell with something in it; an empty row is an empty line.
func TestTableRowEndsAtLastFilledCell(t *testing.T) {
	tbl := &table{gap: columnGap}
	tbl.row("1.", "title", "[tag]", "date")
	tbl.row("2.", "t", "", "\x1b[2m\x1b[m")
	tbl.row("3.", "", "[x]", "")
	tbl.row("", "", "", "")
	want := []string{"1.  title  [tag]  date", "2.  t", "3.         [x]", ""}
	for i, got := range tbl.lines() {
		if got != want[i] {
			t.Errorf("line %d = %q, want %q", i, got, want[i])
		}
	}
}

// A cut leaves no empty styled spans behind it, and never removes a reset
// that closes a style still open.
func TestDropEmptySpans(t *testing.T) {
	tests := []struct{ in, want string }{
		{"\x1b[2mW\x1b[m\x1b[2m…\x1b[m\x1b[2m\x1b[m\x1b[9m\x1b[m", "\x1b[2mW\x1b[m\x1b[2m…\x1b[m"},
		{"\x1b[1mA\x1b[2m\x1b[m", "\x1b[1mA\x1b[2m\x1b[m"},
		{"\x1b[1mA\x1b[2m\x1b[m\x1b[3m\x1b[m", "\x1b[1mA\x1b[2m\x1b[m"},
		{"plain…", "plain…"},
	}
	for _, tt := range tests {
		if got := dropEmptySpans(tt.in); got != tt.want {
			t.Errorf("dropEmptySpans(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A dropFirst column goes before a shrink column gives up anything, even its
// soft minimum's worth; dropOrder still waits until the soft cut is done.
func TestTableDropFirst(t *testing.T) {
	tests := []struct {
		name     string
		maxWidth int
		want     string
	}{
		{"all fit", 38, "1.  a title of twenty ch  ✓ 1/2  due:x"},
		{"drop first, title whole", 31, "1.  a title of twenty ch  due:x"},
		{"then soft cut", 27, "1.  a title of twen…  due:x"},
		{"then dropOrder", 16, "1.  a title of …"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tbl := &table{
				gap:       columnGap,
				maxWidth:  tt.maxWidth,
				dropFirst: []int{2},
				dropOrder: []int{3},
				shrink:    []shrinkCol{{col: 1, min: 4, soft: 12}},
			}
			tbl.row("1.", "a title of twenty ch", "✓ 1/2", "due:x")
			if got := tbl.lines()[0]; got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// A dropOrder column with compact forms switches the whole column to them
// before it is dropped, and is dropped only if the row still does not fit. A
// row without a compact form keeps its full text.
func TestTableCompactBeforeDrop(t *testing.T) {
	tests := []struct {
		name     string
		maxWidth int
		want     []string
	}{
		{"full fits", 21, []string{"1.  title  2026-10-02", "2.  other  2026-10-09", "3.  third  soon"}},
		{"compact", 16, []string{"1.  title  Fri", "2.  other  9 Oct", "3.  third  soon"}},
		{"still too wide", 15, []string{"1.  title", "2.  other", "3.  third"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tbl := &table{gap: columnGap, maxWidth: tt.maxWidth, dropOrder: []int{2}}
			tbl.row("1.", "title", "2026-10-02")
			tbl.alt(2, "Fri")
			tbl.row("2.", "other", "2026-10-09")
			tbl.alt(2, "9 Oct")
			tbl.row("3.", "third", "soon")
			got := tbl.lines()
			if len(got) != len(tt.want) {
				t.Fatalf("got %d lines, want %d", len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
