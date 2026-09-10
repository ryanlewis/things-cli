package output

import (
	"fmt"
	"io"

	"charm.land/lipgloss/v2"
)

// columnGap separates two columns of a rendered listing.
const columnGap = "  "

// table lays cells out in aligned columns. Rows go in a cell at a time, every
// column is measured across the whole table, and the finished lines come back
// padded and joined — the work each list printer in this package used to do
// for itself, with its own row struct and its own width loop.
//
// Every column is padded to its widest cell except the last one declared,
// which goes out as it is. Dropping a column does not move that rule: the
// column left unpadded is the last one declared, whether or not it survived.
type table struct {
	// gap separates two columns.
	gap string
	// maxWidth is what a finished row has to fit in. Zero keeps every column
	// however wide the rows measure.
	maxWidth int
	// dropOrder names the columns that may be given up when a row does not
	// fit, in the order they go — the first named is the first to go.
	dropOrder []int

	rows [][]string
}

// row appends one row. Rows are expected to carry the same number of cells;
// a short row leaves the columns past its end empty.
func (t *table) row(cells ...string) {
	t.rows = append(t.rows, cells)
}

// lines renders one line per row, in the order the rows went in, so a caller
// that has to write something between two rows — a group header — still can.
func (t *table) lines() []string {
	widths := t.widths()
	keep := t.keep(widths)
	last := len(widths) - 1

	lines := make([]string, len(t.rows))
	for i, cells := range t.rows {
		cols := make([]string, 0, len(widths))
		for c, width := range widths {
			if !keep[c] {
				continue
			}
			cell := ""
			if c < len(cells) {
				cell = cells[c]
			}
			if c == last {
				cols = append(cols, cell)
				continue
			}
			cols = append(cols, padCol(width, cell))
		}
		lines[i] = joinWithGap(cols, t.gap)
	}
	return lines
}

// render writes every line of the table.
func (t *table) render(w io.Writer) error {
	for _, line := range t.lines() {
		fmt.Fprintln(w, line)
	}
	return nil
}

// widths measures each column across every row. lipgloss.Width, not len: a
// cell carries ANSI and may carry a wide rune, and neither is a column of
// terminal.
func (t *table) widths() []int {
	n := 0
	for _, cells := range t.rows {
		if len(cells) > n {
			n = len(cells)
		}
	}
	widths := make([]int, n)
	for _, cells := range t.rows {
		for c, cell := range cells {
			if w := lipgloss.Width(cell); w > widths[c] {
				widths[c] = w
			}
		}
	}
	return widths
}

// keep decides which columns survive the terminal width. It gives up one
// column at a time, in dropOrder, and stops as soon as the row fits — so a
// wide terminal keeps everything and a narrow one loses as little as it can.
func (t *table) keep(widths []int) []bool {
	keep := make([]bool, len(widths))
	for i := range keep {
		keep[i] = true
	}
	if t.maxWidth <= 0 {
		return keep
	}
	for _, c := range t.dropOrder {
		if t.fits(widths, keep) {
			break
		}
		if c >= 0 && c < len(keep) {
			keep[c] = false
		}
	}
	return keep
}

// fits reports whether the kept columns, plus the gaps between them, are
// within maxWidth.
func (t *table) fits(widths []int, keep []bool) bool {
	total, n := 0, 0
	for c, w := range widths {
		if !keep[c] {
			continue
		}
		total += w
		n++
	}
	if n > 1 {
		total += (n - 1) * len(t.gap)
	}
	return total <= t.maxWidth
}

func padCol(width int, s string) string {
	return lipgloss.NewStyle().Width(width).Render(s)
}

func joinWithGap(cols []string, gap string) string {
	parts := make([]string, 0, len(cols)*2-1)
	for i, c := range cols {
		if i > 0 {
			parts = append(parts, gap)
		}
		parts = append(parts, c)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}
