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
