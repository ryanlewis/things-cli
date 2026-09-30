package output

import (
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
		{"fits", 0, []string{"1.  a long title here  x", "2.  short              "}},
		{"cut", 16, []string{"1.  a long t…  x", "2.  short      "}},
		{"floor", 8, []string{"1.  a lo…  x", "2.  short  "}},
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

// The last kept column goes out unpadded, so a dropped trailing column leaves
// no padding behind to carry a row past the terminal edge.
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

// An omitEmpty column that no row fills takes no space, not even its gap.
func TestTableOmitEmpty(t *testing.T) {
	for _, fill := range []string{"", "s"} {
		tbl := &table{gap: columnGap, omitEmpty: []int{1}}
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

// An omitted empty column adds neither width nor gap to the fit, so it never
// costs another column its place. It is left out of dropOrder here so that
// nothing but omitEmpty can explain the tags surviving at the boundary.
func TestTableOmitEmptyFits(t *testing.T) {
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
			omitEmpty: []int{3},
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
