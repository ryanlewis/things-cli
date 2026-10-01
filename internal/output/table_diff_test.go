package output

import (
	"math/rand"
	"slices"
	"strings"
	"testing"
)

// tableSpec is one randomly built table: its configuration and its cells,
// built into both a table and a legacyTable.
type tableSpec struct {
	dropFirst, dropOrder []int
	shrink               []shrinkCol
	rows                 [][]cell
}

func (s tableSpec) build(maxWidth int) (*table, *legacyTable) {
	tbl := &table{gap: columnGap, maxWidth: maxWidth, dropFirst: s.dropFirst, dropOrder: s.dropOrder, shrink: s.shrink}
	old := &legacyTable{gap: columnGap, maxWidth: maxWidth, dropFirst: s.dropFirst, dropOrder: s.dropOrder}
	for _, sc := range s.shrink {
		old.shrink = append(old.shrink, legacyShrinkCol(sc))
	}
	for _, row := range s.rows {
		tbl.add(slices.Clone(row)...)
		texts := make([]string, len(row))
		for c, cl := range row {
			texts[c] = cl.text
		}
		old.row(texts...)
		for c, cl := range row {
			old.tail(c, cl.tail)
			old.alt(c, cl.alt)
		}
	}
	return tbl, old
}

// randText is a cell's text: empty a third of the time, otherwise words of
// ASCII or wide runes, sometimes styled whole and sometimes a character at a
// time, the way a dim title is, and now and then holding a tab or a newline.
func randText(r *rand.Rand, maxWords int) string {
	if r.Intn(3) == 0 {
		return ""
	}
	words := []string{"a", "word", "longerword", "日本", "東京都", "x", "due:2026-10-02", "[tag, other]", "é"}
	var b strings.Builder
	for i := range 1 + r.Intn(maxWords) {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(words[r.Intn(len(words))])
	}
	s := b.String()
	switch r.Intn(12) {
	case 0:
		s = titleDimStyle.Render(s)
	case 1, 2:
		s = tagStyle.Render(s)
	case 3:
		s = strings.Replace(s, " ", "\t", 1)
	case 4:
		s = strings.Replace(s, " ", "\n", 1)
	}
	return s
}

// randSpec builds a table under one of the printers' real configurations or
// a random one. A random one draws dropFirst and dropOrder from disjoint
// columns, may name a column in shrink that is also in dropOrder (the project
// listing's area is), and may name columns the rows do not reach.
func randSpec(r *rand.Rand) tableSpec {
	var s tableSpec
	ncols := 2 + r.Intn(6)
	switch r.Intn(5) {
	case 0: // a task listing on a terminal
		ncols = 7
		s.dropFirst, s.dropOrder = []int{colChecklist, colStart}, []int{colTags, colDate}
		s.shrink = []shrinkCol{{col: colTitle, min: 10, soft: 40}}
	case 1: // a task listing piped: no shrink and no compact forms
		ncols = 7
		s.dropFirst, s.dropOrder = []int{colChecklist, colStart}, []int{colTags, colDate}
	case 2: // a project listing
		ncols = 4
		s.dropOrder = []int{colProjectTags, colProjectArea}
		s.shrink = []shrinkCol{{col: colProjectTitle, min: 10, soft: 30}, {col: colProjectArea, min: 20, soft: 20}}
	default:
		perm := r.Perm(ncols + 2)
		nFirst, nOrder := r.Intn(3), r.Intn(3)
		s.dropFirst = perm[:nFirst]
		s.dropOrder = perm[nFirst : nFirst+nOrder]
		for range r.Intn(3) {
			s.shrink = append(s.shrink, shrinkCol{col: r.Intn(ncols + 1), min: r.Intn(25), soft: r.Intn(50) - 5})
		}
	}
	for range 1 + r.Intn(6) {
		n := ncols
		if r.Intn(6) == 0 {
			n = r.Intn(ncols + 1)
		}
		row := make([]cell, n)
		for c := range row {
			row[c].text = randText(r, 8)
			if r.Intn(5) == 0 {
				row[c].tail = " " + dimStyle.Render("(project)")
			}
			if r.Intn(3) == 0 {
				row[c].alt = randText(r, 3)
			}
		}
		s.rows = append(s.rows, row)
	}
	return s
}

// checkMatchesLegacy renders spec with both tables at every width from 0 to
// 130, or to one past the widest the rows measure whole if that is less,
// since nothing changes beyond it, and fails on the first line that differs.
func checkMatchesLegacy(t *testing.T, spec tableSpec) {
	t.Helper()
	whole, _ := spec.build(0)
	for w := 0; w <= min(130, whole.width(whole.measure())+1); w++ {
		tbl, old := spec.build(w)
		got, want := tbl.lines(), old.lines()
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("width %d: lines differ\nspec: %+v\ngot:  %q\nwant: %q", w, spec, got, want)
		}
	}
}

// The rewritten table renders every table the way legacyTable did, byte for
// byte, under the printers' own configurations and random ones.
func TestTableMatchesLegacy(t *testing.T) {
	r := rand.New(rand.NewSource(298))
	n := 150
	if testing.Short() {
		n = 30
	}
	for range n {
		checkMatchesLegacy(t, randSpec(r))
	}
}

// FuzzTableMatchesLegacy runs TestTableMatchesLegacy's check from fuzzed
// seeds, for a longer run than the test's: go test -fuzz FuzzTableMatchesLegacy.
func FuzzTableMatchesLegacy(f *testing.F) {
	for _, seed := range []int64{1, 2, 3, 298, 313} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		checkMatchesLegacy(t, randSpec(rand.New(rand.NewSource(seed))))
	})
}
