package output

// legacyTable is table as it stood before the consolidation of the fitting
// code (#298–#313), kept verbatim apart from its names, less render, so that
// TestTableMatchesLegacy can check the rewrite renders every table the same,
// byte for byte. It goes once the refactor has landed. padCol, joinWithGap
// and dropEmptySpans are shared with table, which keeps them unchanged.

import (
	"slices"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// table lays cells out in aligned columns. Rows go in a cell at a time, every
// column is measured across the whole table, and the finished lines come back
// padded and joined — the work each list printer in this package used to do
// for itself, with its own row struct and its own width loop.
//
// Every column is padded to its widest cell, and each row ends at its last
// cell with something in it, which goes out as it is: padding, gaps or an
// empty styled span at the end of a row buy nothing, show up when the row is
// copied, and on a narrow terminal can carry it past the edge and wrap it
// onto a blank line.
type legacyTable struct {
	// gap separates two columns.
	gap string
	// maxWidth is what a finished row has to fit in. Zero keeps every column
	// however wide the rows measure.
	maxWidth int
	// dropFirst names the columns that go, in order, before any shrink column
	// is cut: ones worth less than the end of an over-long title.
	dropFirst []int
	// dropOrder names the columns that may be given up when a row does not
	// fit once the shrink columns are at their soft minimum, in the order
	// they go — the first named is the first to go. Every column here with a
	// compact form (see alt) switches to it before any of them is dropped; a
	// dropFirst column with one switches to it just before it goes.
	dropOrder []int
	// shrink names the columns that may be cut short, with an ellipsis, when
	// a row does not fit. They give up width in the order named: first, once
	// every column in dropFirst has gone, down to their soft minimum, before
	// any column in dropOrder goes, and then, once every column in dropOrder
	// has gone, down to their hard minimum. A row whose other columns alone
	// overrun maxWidth still prints, only wider.
	shrink []legacyShrinkCol

	rows [][]string
	// tails holds the endings set with tail, by row and column.
	tails map[[2]int]string
	// alts holds the compact forms set with alt, by row and column.
	alts map[[2]int]string
}

// shrinkCol is a column a table may cut short. soft is how far it gives way
// before a dropOrder column is dropped for it, and min the narrowest it may go
// once nothing is left to drop. A soft of zero, or one below min, gives no
// width up before the drops.
type legacyShrinkCol struct {
	col, min, soft int
}

// row appends one row. Rows are expected to carry the same number of cells;
// a short row leaves the columns past its end empty.
func (t *legacyTable) row(cells ...string) {
	t.rows = append(t.rows, cells)
}

// tail gives one cell, column c of the row just added, an ending that a cut
// leaves whole; other rows are untouched. The cell measures and prints as its
// text followed by s, and when the column is cut short the ellipsis goes into
// the text before s.
func (t *legacyTable) tail(c int, s string) {
	if len(t.rows) == 0 || s == "" {
		return
	}
	if t.tails == nil {
		t.tails = make(map[[2]int]string)
	}
	t.tails[[2]int{len(t.rows) - 1, c}] = s
}

// alt gives one cell, column c of the row just added, a compact form. When
// its column is too wide to keep whole, the whole column switches to its
// compact forms, so the rows stay aligned; a row without one keeps its text.
func (t *legacyTable) alt(c int, s string) {
	if len(t.rows) == 0 || s == "" {
		return
	}
	if t.alts == nil {
		t.alts = make(map[[2]int]string)
	}
	t.alts[[2]int{len(t.rows) - 1, c}] = s
}

// cell is the full text of row i's cell in column c, tail included.
func (t *legacyTable) cell(i, c int) string {
	return t.cellAs(i, c, false)
}

// cellAs is row i's cell in column c, in its compact form when compact is set
// and it has one, tail included.
func (t *legacyTable) cellAs(i, c int, compact bool) string {
	cell := ""
	if c < len(t.rows[i]) {
		cell = t.rows[i][c]
	}
	if alt, ok := t.alts[[2]int{i, c}]; compact && ok {
		cell = alt
	}
	return cell + t.tails[[2]int{i, c}]
}

// compactWidth is how wide column c measures in its compact form, and
// whether any of its cells has one.
func (t *legacyTable) compactWidth(c int) (width int, has bool) {
	for i := range t.rows {
		_, ok := t.alts[[2]int{i, c}]
		has = has || ok
		width = max(width, lipgloss.Width(t.cellAs(i, c, true)))
	}
	return width, has
}

// cut shortens cell, row i's in column c as cell returns it, to width,
// keeping its tail whole.
func (t *legacyTable) cut(i, c int, cell string, width int) string {
	if lipgloss.Width(cell) <= width {
		return cell
	}
	tail := t.tails[[2]int{i, c}]
	head := cell[:len(cell)-len(tail)]
	return dropEmptySpans(ansi.Truncate(head, width-lipgloss.Width(tail), "…")) + tail
}

// lines renders one line per row, in the order the rows went in, so a caller
// that has to write something between two rows — a group header — still can.
func (t *legacyTable) lines() []string {
	widths := t.widths()
	keep, cut, compact := t.fit(widths)

	lines := make([]string, len(t.rows))
	for i := range t.rows {
		cells := make([]string, len(widths))
		end := -1
		for c, width := range widths {
			if !keep[c] {
				continue
			}
			cell := t.cellAs(i, c, compact[c])
			if cut[c] {
				cell = t.cut(i, c, cell, width)
			}
			cells[c] = cell
			if lipgloss.Width(cell) > 0 {
				end = c
			}
		}
		cols := make([]string, 0, len(widths))
		for c, width := range widths {
			if !keep[c] || c > end {
				continue
			}
			if c == end {
				cols = append(cols, cells[c])
				continue
			}
			cols = append(cols, padCol(width, cells[c]))
		}
		lines[i] = joinWithGap(cols, t.gap)
	}
	return lines
}

// widths measures each column across every row. lipgloss.Width, not len: a
// cell carries ANSI and may carry a wide rune, and neither is a column of
// terminal.
func (t *legacyTable) widths() []int {
	n := 0
	for _, cells := range t.rows {
		if len(cells) > n {
			n = len(cells)
		}
	}
	widths := make([]int, n)
	for i, cells := range t.rows {
		for c := range cells {
			if w := lipgloss.Width(t.cell(i, c)); w > widths[c] {
				widths[c] = w
			}
		}
	}
	return widths
}

// fit decides which columns survive the terminal width, which are cut short
// and which switch to their compact form, narrowing widths in place. A column
// no row puts anything in takes no space, gap included. Then, stopping as
// soon as the row fits:
//
//  1. dropFirst gives up one column at a time;
//  2. shrink columns give way to their soft minimum;
//  3. dropOrder columns switch to their compact forms, one at a time;
//  4. dropOrder gives up one column at a time;
//  5. a column step 3 shortened goes back to its full form if a later
//     compact form or a drop made room for it;
//  6. any width left beyond the overrun goes back to the columns the soft
//     pass cut;
//  7. shrink columns give way to their hard minimum.
//
// So a wide terminal keeps everything and a narrow one loses as little as it
// can, the end of an over-long cell before a column on every row, and a
// column worth less than that before either.
func (t *legacyTable) fit(widths []int) (keep, cut, compact []bool) {
	keep = make([]bool, len(widths))
	cut = make([]bool, len(widths))
	compact = make([]bool, len(widths))
	for c, w := range widths {
		keep[c] = w > 0
	}
	if t.maxWidth <= 0 {
		return keep, cut, compact
	}
	full := slices.Clone(widths)
	t.drop(widths, keep, compact, t.dropFirst)
	t.shrinkTo(widths, keep, cut, true)
	compacted := t.compactTier(widths, keep, compact, t.dropOrder)
	dropped := t.drop(widths, keep, compact, t.dropOrder)
	if compacted || dropped {
		// A later compact form or a drop can free enough for a column the
		// compact tier shortened to go back to its full form, and more than
		// the row was over by: hand the rest back to the columns the soft
		// pass cut, in the order they gave it up.
		t.expand(widths, full, keep, compact, t.dropOrder)
		t.regrow(widths, full, keep, cut)
	}
	t.shrinkTo(widths, keep, cut, false)
	return keep, cut, compact
}

// compactTier switches the columns in order to their compact form, where
// they have a narrower one, until the kept columns fit, so that every column
// in dropOrder gives up width before any of them is dropped. It reports
// whether it freed any width.
func (t *legacyTable) compactTier(widths []int, keep, compact []bool, order []int) bool {
	freed := false
	for _, c := range order {
		if t.fits(widths, keep) {
			break
		}
		if c < 0 || c >= len(keep) || !keep[c] || compact[c] {
			continue
		}
		if w, ok := t.compactWidth(c); ok && w < widths[c] {
			widths[c], compact[c] = w, true
			freed = true
		}
	}
	return freed
}

// expand gives the kept columns the compact tier shortened their full form
// back, where the row still fits with it, going through order from its end:
// the column that would have been dropped last is the one most worth having
// whole.
func (t *legacyTable) expand(widths, full []int, keep, compact []bool, order []int) {
	for i := len(order) - 1; i >= 0; i-- {
		c := order[i]
		if c < 0 || c >= len(keep) || !keep[c] || !compact[c] {
			continue
		}
		short := widths[c]
		widths[c] = full[c]
		if t.fits(widths, keep) {
			compact[c] = false
			continue
		}
		widths[c] = short
	}
}

// drop goes through the columns in order until the kept columns fit,
// switching each to its compact form if it has a narrower one and dropping it
// only if the row still does not fit. It reports whether it freed any width.
func (t *legacyTable) drop(widths []int, keep, compact []bool, order []int) bool {
	freed := false
	for _, c := range order {
		if t.fits(widths, keep) {
			break
		}
		if c < 0 || c >= len(keep) || !keep[c] {
			continue
		}
		freed = true
		if t.compactTier(widths, keep, compact, []int{c}) && t.fits(widths, keep) {
			break
		}
		keep[c] = false
	}
	return freed
}

// shrinkTo narrows the shrink columns, in place in widths, until the kept
// columns fit or every one of them is at its soft minimum (soft) or its hard
// one. It marks in cut the columns it narrowed, so their cells can be cut to
// the new width. A column with tails goes no narrower than its widest tail
// plus one character and the ellipsis, whatever its own minimum.
func (t *legacyTable) shrinkTo(widths []int, keep, cut []bool, soft bool) {
	for _, s := range t.shrink {
		over := t.width(widths, keep) - t.maxWidth
		if over <= 0 {
			break
		}
		if s.col < 0 || s.col >= len(widths) || !keep[s.col] {
			continue
		}
		floor := max(s.min, t.widestTail(s.col)+2)
		if soft {
			if s.soft <= 0 || s.soft < s.min {
				continue
			}
			floor = max(floor, s.soft)
		}
		if widths[s.col] <= floor {
			continue
		}
		widths[s.col] = max(widths[s.col]-over, floor)
		cut[s.col] = true
	}
}

// regrow gives the room a row has left under maxWidth back to the shrink
// columns that were cut, each up to its full width, in shrink order.
func (t *legacyTable) regrow(widths, full []int, keep, cut []bool) {
	for _, s := range t.shrink {
		slack := t.maxWidth - t.width(widths, keep)
		if slack <= 0 {
			return
		}
		if s.col < 0 || s.col >= len(widths) || !keep[s.col] || !cut[s.col] {
			continue
		}
		widths[s.col] = min(widths[s.col]+slack, full[s.col])
		cut[s.col] = widths[s.col] < full[s.col]
	}
}

// widestTail is the widest tail set in column c, or zero.
func (t *legacyTable) widestTail(c int) int {
	widest := 0
	for k, tail := range t.tails {
		if k[1] == c {
			widest = max(widest, lipgloss.Width(tail))
		}
	}
	return widest
}

// fits reports whether the kept columns, plus the gaps between them, are
// within maxWidth.
func (t *legacyTable) fits(widths []int, keep []bool) bool {
	return t.width(widths, keep) <= t.maxWidth
}

// width is how wide a row of the kept columns measures, gaps included.
func (t *legacyTable) width(widths []int, keep []bool) int {
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
