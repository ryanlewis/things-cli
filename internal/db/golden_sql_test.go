package db

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
)

// -update-golden rewrites testdata/list_query_golden.txt from the current
// code. Regenerating is only ever right when the SQL was *meant* to change:
// the point of the file is that a refactor which is supposed to preserve the
// queries has to leave it untouched.
var updateGolden = flag.Bool("update-golden", false, "rewrite the golden list-query SQL")

const goldenSQLPath = "testdata/list_query_golden.txt"

// goldenFilterCases are the filter shapes a view is composed with. Every
// filter appears alone, so a change to one clause cannot hide behind another,
// and then in combination, which is what pins the order the clauses are
// appended in.
func goldenFilterCases() []struct {
	name string
	opts TaskFilter
} {
	on := model.ThingsDate(132813696)   // 2026-09-07
	from := model.ThingsDate(132813696) // 2026-09-07
	to := model.ThingsDate(132814464)   // 2026-09-13
	return []struct {
		name string
		opts TaskFilter
	}{
		{"none", TaskFilter{}},
		{"project", TaskFilter{Project: "Launch v2"}},
		{"area", TaskFilter{Area: "Work"}},
		{"tag", TaskFilter{Tag: "urgent"}},
		{"on", TaskFilter{On: &on}},
		{"from", TaskFilter{From: &from}},
		{"to", TaskFilter{To: &to}},
		{"from+to", TaskFilter{From: &from, To: &to}},
		{"project+area+tag", TaskFilter{Project: "Launch v2", Area: "Work", Tag: "urgent"}},
		{"all", TaskFilter{Project: "Launch v2", Area: "Work", Tag: "urgent", From: &from, To: &to}},
	}
}

// renderGoldenSQL builds every view against every filter shape, with and
// without --include-completed, and renders the lot as one text document.
//
// The views come from viewFilters rather than a list written out here, so a
// view added later cannot quietly escape the golden file: it shows up as an
// unreviewed block the moment it exists.
func renderGoldenSQL(t *testing.T, d *DB) string {
	t.Helper()
	names := make([]string, 0, len(viewFilters))
	for v := range viewFilters {
		names = append(names, v)
	}
	sort.Strings(names)
	filterCases := goldenFilterCases()

	var b strings.Builder
	for _, view := range names {
		for _, includeCompleted := range []bool{false, true} {
			for _, fc := range filterCases {
				opts := fc.opts
				opts.IncludeCompleted = includeCompleted
				query, args, err := d.buildListQuery(view, opts)
				if err != nil {
					t.Fatalf("buildListQuery(%s, %+v): %v", view, opts, err)
				}
				fmt.Fprintf(&b, "### view=%s includeCompleted=%v filters=%s\n", view, includeCompleted, fc.name)
				fmt.Fprintf(&b, "ARGS: %#v\n", args)
				fmt.Fprintf(&b, "%s\n\n", query)
			}
		}
	}
	return b.String()
}

// The SQL every list view runs is the contract the whole `things list` surface
// rests on, and it is assembled from constants and maps that several changes a
// week touch. This pins the finished text — WHERE, GROUP BY and ORDER BY, plus
// the bound arguments — for every view against every filter shape, so a
// refactor that is meant to preserve the queries has to prove it did (issue
// #240).
//
// A deliberate SQL change regenerates the file with -update-golden, and the
// diff on that file is then the reviewable record of what moved.
func TestListQueryGoldenSQL(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	got := renderGoldenSQL(t, &DB{db: sqlDB})

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenSQLPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenSQLPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %s", goldenSQLPath)
		return
	}

	wantBytes, err := os.ReadFile(goldenSQLPath)
	if err != nil {
		t.Fatalf("read golden (regenerate with: go test ./internal/db -run TestListQueryGoldenSQL -update-golden): %v", err)
	}
	want := string(wantBytes)
	if got == want {
		return
	}

	// Report the differing cases rather than the whole document — the file is
	// a few thousand lines, and a wall of it says nothing about which view
	// moved.
	gotCases, wantCases := splitGoldenCases(got), splitGoldenCases(want)
	var reported int
	for _, header := range goldenCaseOrder(want, got) {
		g, inGot := gotCases[header]
		w, inWant := wantCases[header]
		switch {
		case inGot && inWant && g == w:
			continue
		case !inWant:
			t.Errorf("%s: new case, not in the golden file", header)
		case !inGot:
			t.Errorf("%s: case gone from the generated SQL", header)
		default:
			t.Errorf("%s: SQL changed\n  golden: %s\n     got: %s", header, w, g)
		}
		reported++
		if reported == 5 {
			t.Errorf("(further differences not listed)")
			return
		}
	}
}

// splitGoldenCases indexes the document by its "### view=..." headers.
func splitGoldenCases(doc string) map[string]string {
	cases := map[string]string{}
	var header string
	var body strings.Builder
	flush := func() {
		if header != "" {
			cases[header] = strings.TrimSpace(body.String())
		}
		body.Reset()
	}
	for line := range strings.SplitSeq(doc, "\n") {
		if strings.HasPrefix(line, "### ") {
			flush()
			header = line
			continue
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	flush()
	return cases
}

// goldenCaseOrder lists every header in either document, golden order first so
// the report reads in the same order as the file.
func goldenCaseOrder(want, got string) []string {
	seen := map[string]bool{}
	var order []string
	for _, doc := range []string{want, got} {
		for line := range strings.SplitSeq(doc, "\n") {
			if strings.HasPrefix(line, "### ") && !seen[line] {
				seen[line] = true
				order = append(order, line)
			}
		}
	}
	return order
}
