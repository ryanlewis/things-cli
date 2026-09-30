package output

import (
	"fmt"
	"io"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// columnGap separates two columns of a rendered listing.
const columnGap = "  "

// table lays cells out in aligned columns. Rows go in a cell at a time, every
// column is measured across the whole table, and the finished lines come back
// padded and joined — the work each list printer in this package used to do
// for itself, with its own row struct and its own width loop.
//
// Every column is padded to its widest cell except the last one kept, which
// goes out as it is: padding at the end of a row buys nothing, and on a narrow
// terminal it can carry the row past the edge and wrap it onto a blank line.
type table struct {
	// gap separates two columns.
	gap string
	// maxWidth is what a finished row has to fit in. Zero keeps every column
	// however wide the rows measure.
	maxWidth int
	// dropOrder names the columns that may be given up when a row does not
	// fit, in the order they go — the first named is the first to go.
	dropOrder []int
	// omitEmpty names the columns that take no space, gap included, when no
	// row puts anything in them.
	omitEmpty []int
	// shrink names the columns that may be cut short, with an ellipsis, when
	// a row still does not fit once every column in dropOrder has gone. They
	// give up width in the order named, none below its own minimum, so a row
	// whose other columns alone overrun maxWidth still prints, only wider.
	shrink []shrinkCol

	rows [][]string
}

// shrinkCol is a column a table may cut short, and the narrowest it may go.
type shrinkCol struct {
	col, min int
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
	cut := t.shrinkTo(widths, keep)
	last := -1
	for c := range widths {
		if keep[c] {
			last = c
		}
	}

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
			if cut[c] {
				cell = ansi.Truncate(cell, width, "…")
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
	for _, c := range t.omitEmpty {
		if c >= 0 && c < len(keep) && widths[c] == 0 {
			keep[c] = false
		}
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

// shrinkTo narrows the shrink columns, in place in widths, until the kept
// columns fit or every one of them is at its minimum. It reports which
// columns it narrowed, so their cells can be cut to the new width.
func (t *table) shrinkTo(widths []int, keep []bool) []bool {
	cut := make([]bool, len(widths))
	if t.maxWidth <= 0 {
		return cut
	}
	for _, s := range t.shrink {
		over := t.width(widths, keep) - t.maxWidth
		if over <= 0 {
			break
		}
		if s.col < 0 || s.col >= len(widths) || !keep[s.col] || widths[s.col] <= s.min {
			continue
		}
		widths[s.col] = max(widths[s.col]-over, s.min)
		cut[s.col] = true
	}
	return cut
}

// fits reports whether the kept columns, plus the gaps between them, are
// within maxWidth.
func (t *table) fits(widths []int, keep []bool) bool {
	return t.width(widths, keep) <= t.maxWidth
}

// width is how wide a row of the kept columns measures, gaps included.
func (t *table) width(widths []int, keep []bool) int {
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
	return total
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
