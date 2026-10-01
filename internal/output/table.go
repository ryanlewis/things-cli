package output

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

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
// Every column is padded to its widest cell, and each row ends at its last
// cell with something in it, which goes out as it is: padding, gaps or an
// empty styled span at the end of a row buy nothing, show up when the row is
// copied, and on a narrow terminal can carry it past the edge and wrap it
// onto a blank line.
type table struct {
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
	// compact form (a cell's alt) switches to it before any of them is
	// dropped; a dropFirst column with one switches to it just before it goes.
	dropOrder []int
	// shrink names the columns that may be cut short, with an ellipsis, when
	// a row does not fit. They give up width in the order named: first, once
	// every column in dropFirst has gone, down to their soft minimum, before
	// any column in dropOrder goes, and then, once every column in dropOrder
	// has gone, down to their hard minimum. A row whose other columns alone
	// overrun maxWidth still prints, only wider.
	shrink []shrinkCol

	rows [][]cell
}

// shrinkCol is a column a table may cut short. soft is how far it gives way
// before a dropOrder column is dropped for it, and min the narrowest it may go
// once nothing is left to drop. A soft of zero, or one below min, gives no
// width up before the drops.
type shrinkCol struct {
	col, min, soft int
}

// cell is one cell of a row: its text, an ending a cut leaves whole (tail),
// and a compact form (alt) its column may switch to, keeping the rows
// aligned. Its widths are measured once, as the row goes in — lipgloss.Width,
// not len: a cell carries ANSI and may carry a wide rune.
type cell struct {
	text, tail, alt string
	// width is text and tail measured together, so a cell measures what it
	// prints; altWidth is alt and tail, and tailWidth the tail alone.
	width, altWidth, tailWidth int
}

// form is the cell as it prints, tail included — its compact form when short
// is set and it has one — and how wide that measures.
func (c cell) form(short bool) (string, int) {
	if short && c.alt != "" {
		return c.alt + c.tail, c.altWidth
	}
	return c.text + c.tail, c.width
}

// cut shortens s, one of the cell's forms, to width with an ellipsis,
// keeping its tail whole.
func (c cell) cut(s string, width int) string {
	head := s[:len(s)-len(c.tail)]
	return dropEmptySpans(ansi.Truncate(head, width-c.tailWidth, "…")) + c.tail
}

// add appends one row of cells. Rows are expected to carry the same number
// of cells; a short row leaves the columns past its end empty.
func (t *table) add(cells ...cell) {
	for i := range cells {
		c := &cells[i]
		c.width = lipgloss.Width(c.text + c.tail)
		c.altWidth = lipgloss.Width(c.alt + c.tail)
		c.tailWidth = lipgloss.Width(c.tail)
	}
	t.rows = append(t.rows, cells)
}

// row appends one row of plain text cells.
func (t *table) row(texts ...string) {
	cells := make([]cell, len(texts))
	for i, s := range texts {
		cells[i].text = s
	}
	t.add(cells...)
}

// emptySpans matches a run of styles each opened and reset with nothing
// between, at the end of a string.
var emptySpans = regexp.MustCompile(`(?:\x1b\[[0-9;]+m\x1b\[m)+$`)

// dropEmptySpans removes the empty styled spans ansi.Truncate leaves behind
// for the characters it cut from text styled one character at a time. It only
// does so after a reset, so what it removes can never leave a style open: when
// the run follows text still styled, its first span keeps the reset that
// closes that style and only the spans after it go.
func dropEmptySpans(s string) string {
	loc := emptySpans.FindStringIndex(s)
	if loc == nil {
		return s
	}
	const reset = "\x1b[m"
	start := loc[0]
	if !strings.HasSuffix(s[:start], reset) {
		start += strings.Index(s[start:], reset) + len(reset)
	}
	return s[:start]
}

// lines renders one line per row, in the order the rows went in, so a caller
// that has to write something between two rows — a group header — still can.
func (t *table) lines() []string {
	cols := t.fit()

	lines := make([]string, len(t.rows))
	for i, row := range t.rows {
		cells := make([]string, len(cols))
		end := -1
		for c, k := range cols {
			if !k.keep || c >= len(row) {
				continue
			}
			s, w := row[c].form(k.compact)
			if k.cut && w > k.width {
				s = row[c].cut(s, k.width)
			}
			cells[c] = s
			if w > 0 {
				end = c
			}
		}
		parts := make([]string, 0, end+1)
		for c, k := range cols[:end+1] {
			if !k.keep {
				continue
			}
			if c == end {
				parts = append(parts, cells[c])
				continue
			}
			parts = append(parts, padCol(k.width, cells[c]))
		}
		lines[i] = joinWithGap(parts, t.gap)
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

// col is what fit knows and decides about one column.
type col struct {
	// width is what the column takes now: full to begin with, then less as
	// fit compacts or cuts it. short is its width with every cell in its
	// compact form, or in full where it has none, so a column with no compact
	// forms measures no narrower in them. tail is its widest tail.
	width, full, short, tail int
	// keep is whether the column prints, cut whether its cells are cut to
	// width, and compact whether they print in their compact forms.
	keep, cut, compact bool
}

// measure sizes every column across the whole table. A column no row puts
// anything in is not kept: it takes no space, gap included. That covers a
// column the drop and shrink lists name but no row reaches, which is
// measured, empty, so that fit can look any of them up. The lists hold a
// printer's column constants; a negative index is a bug, and panics.
func (t *table) measure() []col {
	n := 0
	for _, row := range t.rows {
		n = max(n, len(row))
	}
	for _, c := range slices.Concat(t.dropFirst, t.dropOrder) {
		n = max(n, c+1)
	}
	for _, s := range t.shrink {
		n = max(n, s.col+1)
	}
	cols := make([]col, n)
	for _, row := range t.rows {
		for c, cl := range row {
			k := &cols[c]
			k.full = max(k.full, cl.width)
			_, short := cl.form(true)
			k.short = max(k.short, short)
			k.tail = max(k.tail, cl.tailWidth)
		}
	}
	for c := range cols {
		cols[c].width = cols[c].full
		cols[c].keep = cols[c].full > 0
	}
	return cols
}

// fit decides which columns survive maxWidth, which are cut short and which
// switch to their compact form. Each step stops as soon as the row fits:
//
//  1. dropFirst gives up one column at a time;
//  2. shrink columns give way to their soft minimum;
//  3. dropOrder columns switch to their compact forms, one at a time;
//  4. dropOrder gives up one column at a time;
//  5. room steps 3 and 4 freed beyond the overrun goes back (see restore);
//  6. shrink columns give way to their hard minimum.
//
// So a wide terminal keeps everything and a narrow one loses as little as it
// can, the end of an over-long cell before a column on every row, and a
// column worth less than that before either.
func (t *table) fit() []col {
	cols := t.measure()
	if t.maxWidth <= 0 {
		return cols
	}
	t.untilFits(cols, t.dropFirst, t.drop)
	t.shrinkTo(cols, softFloor)
	t.untilFits(cols, t.dropOrder, compact)
	t.untilFits(cols, t.dropOrder, t.drop)
	t.restore(cols)
	t.shrinkTo(cols, hardFloor)
	return cols
}

// untilFits applies step to each of the columns in order, until the row fits.
func (t *table) untilFits(cols []col, order []int, step func([]col, int)) {
	for _, c := range order {
		if t.fits(cols) {
			return
		}
		step(cols, c)
	}
}

// compact switches column c to its compact forms, where it is kept, not yet
// compact, and narrower in them.
func compact(cols []col, c int) {
	k := &cols[c]
	if k.keep && !k.compact && k.short < k.width {
		k.width, k.compact = k.short, true
	}
}

// drop gives up column c, unless switching it to its compact form is enough
// for the row to fit.
func (t *table) drop(cols []col, c int) {
	compact(cols, c)
	if !t.fits(cols) {
		cols[c].keep = false
	}
}

// softFloor is how far s gives way before any dropOrder column is dropped
// for it, or false when it gives none up then.
func softFloor(s shrinkCol, tail int) (int, bool) {
	if s.soft <= 0 || s.soft < s.min {
		return 0, false
	}
	return max(s.soft, tail+2), true
}

// hardFloor is the narrowest s may go once nothing is left to drop. A column
// with tails goes no narrower than its widest tail plus one character and the
// ellipsis, whatever its own minimum.
func hardFloor(s shrinkCol, tail int) (int, bool) {
	return max(s.min, tail+2), true
}

// shrinkTo narrows the shrink columns, in order, until the kept columns fit
// or every one of them is down to its floor, and marks the ones it narrowed
// as cut.
func (t *table) shrinkTo(cols []col, floorOf func(shrinkCol, int) (int, bool)) {
	for _, s := range t.shrink {
		over := t.width(cols) - t.maxWidth
		if over <= 0 {
			return
		}
		k := &cols[s.col]
		if !k.keep {
			continue
		}
		floor, ok := floorOf(s, k.tail)
		if !ok || k.width <= floor {
			continue
		}
		k.width = max(k.width-over, floor)
		k.cut = true
	}
}

// restore hands back the room a row has left under maxWidth once a compact
// form or a drop freed more than it was over by. First the columns step 3 of
// fit shortened get their full forms back, where the row still fits with
// them, from the end of dropOrder: the column that would have been dropped
// last is the one most worth having whole. Then what is left goes to the
// columns the soft pass cut, each up to its full width, in shrink order — the
// order they gave it up.
func (t *table) restore(cols []col) {
	for _, c := range slices.Backward(t.dropOrder) {
		k := &cols[c]
		if !k.keep || !k.compact {
			continue
		}
		short := k.width
		k.width = k.full
		if t.fits(cols) {
			k.compact = false
			continue
		}
		k.width = short
	}
	for _, s := range t.shrink {
		slack := t.maxWidth - t.width(cols)
		if slack <= 0 {
			return
		}
		k := &cols[s.col]
		if !k.keep || !k.cut {
			continue
		}
		k.width = min(k.width+slack, k.full)
		k.cut = k.width < k.full
	}
}

// fits reports whether the kept columns, plus the gaps between them, are
// within maxWidth.
func (t *table) fits(cols []col) bool {
	return t.width(cols) <= t.maxWidth
}

// width is how wide a row of the kept columns measures, gaps included.
func (t *table) width(cols []col) int {
	total, n := 0, 0
	for _, k := range cols {
		if !k.keep {
			continue
		}
		total += k.width
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
	if len(cols) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cols)*2-1)
	for i, c := range cols {
		if i > 0 {
			parts = append(parts, gap)
		}
		parts = append(parts, c)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}
