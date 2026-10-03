package db

import (
	"strings"
	"testing"
	"unicode"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
)

// The name and title filters ignore case beyond ASCII, as Things does. SQLite's
// LIKE folds ASCII only, so without fold() on both sides `--tag ärger` missed a
// tag named "Ärger". "ΚΟΣ" against "κος" is the final-sigma case: ToLower alone
// turns Σ into σ, not ς, and misses it.
func TestFiltersIgnoreNonASCIICase(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES
		('ar-a', 'Ärger', 1, 1),
		('ar-k', 'κος',   1, 2),
		('ar-s', 'Straße', 1, 3)`)
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, area, "index") VALUES
		('proj-a', 'Ärger', 1, 0, 0, 'ar-a', 1),
		('proj-k', 'κος',   1, 0, 0, 'ar-k', 2),
		('proj-s', 'Straße', 1, 0, 0, 'ar-s', 5)`)
	mustExec(t, d, `INSERT INTO TMTag (uuid, title, "index") VALUES
		('tg-a', 'Ärger', 1),
		('tg-k', 'κος',   2),
		('tg-s', 'Straße', 3)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, notes, type, status, trashed, start, startBucket, project, "index") VALUES
		('in-a', 'Über alles', 'Notiz zu ÄRGER', 0, 0, 0, 1, 0, 'proj-a', 3),
		('in-k', 'Φως',        '',               0, 0, 0, 1, 0, 'proj-k', 4),
		('in-s', 'Fahrt',      '',               0, 0, 0, 1, 0, 'proj-s', 6)`)
	mustExec(t, d, `INSERT INTO TMTaskTag (tasks, tags) VALUES ('in-a', 'tg-a'), ('in-k', 'tg-k'), ('in-s', 'tg-s')`)

	for _, tc := range []struct {
		name   string
		filter TaskFilter
		want   []string
	}{
		{"project", TaskFilter{Project: "ärger"}, []string{"in-a"}},
		{"project final sigma", TaskFilter{Project: "ΚΟΣ"}, []string{"in-k"}},
		{"area", TaskFilter{Area: "äRGER"}, []string{"proj-a", "in-a"}},
		{"area final sigma", TaskFilter{Area: "ΚΟΣ"}, []string{"proj-k", "in-k"}},
		{"tag", TaskFilter{Tag: "ärger"}, []string{"in-a"}},
		{"tag final sigma", TaskFilter{Tag: "ΚΟΣ"}, []string{"in-k"}},
		{"tag surrounding space", TaskFilter{Tag: " ärger "}, []string{"in-a"}},
		{"area surrounding space", TaskFilter{Area: " ärger "}, []string{"proj-a", "in-a"}},
		{"project surrounding space", TaskFilter{Project: " ärger "}, []string{"in-a"}},
		{"project full fold", TaskFilter{Project: "STRASSE"}, []string{"in-s"}},
		{"area full fold", TaskFilter{Area: "STRASSE"}, []string{"proj-s", "in-s"}},
		{"tag full fold", TaskFilter{Tag: "STRASSE"}, []string{"in-s"}},
	} {
		t.Run("list "+tc.name, func(t *testing.T) {
			got, err := d.ListTasks("project", tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if !sameSet(uuidsOf(got), tc.want) {
				t.Errorf("got %v, want %v", uuidsOf(got), tc.want)
			}
		})
	}

	t.Run("projects --area", func(t *testing.T) {
		got, err := d.ListProjects("ärger", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].UUID != "proj-a" {
			t.Errorf("got %+v, want just proj-a", got)
		}
	})

	t.Run("title lookup", func(t *testing.T) {
		for ref, want := range map[string]string{"üBER": "in-a", "φΩς": "in-k"} {
			got, err := d.FindTasksByTitle(ref)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].UUID != want {
				t.Errorf("FindTasksByTitle(%q) = %v, want [%s]", ref, uuidsOf(got), want)
			}
		}
	})

	t.Run("search title and notes", func(t *testing.T) {
		// "ärger" is in in-a's notes and is proj-a's title.
		for query, want := range map[string][]string{
			"über": {"in-a"}, "ärger": {"proj-a", "in-a"}, "ΦΩΣ": {"in-k"},
			"strasse": {"proj-s"},
		} {
			got, err := d.SearchTasks(query)
			if err != nil {
				t.Fatal(err)
			}
			if !sameSet(uuidsOf(got), want) {
				t.Errorf("SearchTasks(%q) = %v, want %v", query, uuidsOf(got), want)
			}
		}
	})
}

func TestNamesRepeatingProjectIgnoresNonASCIICase(t *testing.T) {
	d, fx := newFixture(t)
	fx.Project("p-tmpl", "Ärger review", 1, someday(), repeats())

	got, err := d.NamesRepeatingProject("ärger REVIEW")
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("NamesRepeatingProject(ärger REVIEW) = false, want true")
	}
}

// FoldCase does full case folding, as Things does: a tag "Straße" is found as
// "STRASSE", and "ﬁx" as "FIX". Everything strings.EqualFold equates still
// folds together, and the Turkish dotless "ı" and dotted "İ" stay apart from
// "i": ToLower(ToUpper(s)) sent both to "i", so a title lookup for "kirmizi"
// could resolve to a task titled "kırmızı".
func TestFoldCase(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		equal bool
	}{
		{"Work", "wORK", true}, {"Ärger", "äRGER", true}, {"ΚΟΣ", "κος", true},
		{"σ", "ς", true}, {"ß", "ẞ", true}, {"K", "K", true}, {"s", "ſ", true},
		{"µ", "μ", true},
		{"ß", "ss", true}, {"ß", "SS", true}, {"ẞ", "SS", true}, {"Straße", "STRASSE", true},
		{"STRAẞE", "strasse", true}, {"ﬁx", "FIX", true}, {"ﬀ", "Ff", true}, {"ŉ", "ʼN", true},
		{"ı", "i", false}, {"ı", "I", false}, {"İ", "i", false}, {"İ", "I", false},
		{"ß", "s", false}, {"a", "b", false},
	} {
		if got := FoldCase(tc.a) == FoldCase(tc.b); got != tc.equal {
			t.Errorf("FoldCase(%q) == FoldCase(%q) is %v, want %v", tc.a, tc.b, got, tc.equal)
		}
		if strings.EqualFold(tc.a, tc.b) && !tc.equal {
			t.Errorf("EqualFold(%q, %q), but the case says they differ", tc.a, tc.b)
		}
	}
}

// fold() is registered for every connection, dbtest's included, keeps NULL as
// NULL, and gives the same answer as FoldCase.
func TestFoldSQLMatchesGo(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	for _, s := range []string{"Ärger", "ΚΟΣ", "κος", "Straße", "MiXeD 50%_"} {
		var got string
		if err := sqlDB.QueryRow(`SELECT fold(?)`, s).Scan(&got); err != nil {
			t.Fatalf("fold(%q): %v", s, err)
		}
		if got != FoldCase(s) {
			t.Errorf("fold(%q) = %q, FoldCase = %q", s, got, FoldCase(s))
		}
	}
	var isNull bool
	if err := sqlDB.QueryRow(`SELECT fold(NULL) IS NULL`).Scan(&isNull); err != nil {
		t.Fatal(err)
	}
	if !isNull {
		t.Error("fold(NULL) is not NULL")
	}
}

// Every rune that folds to fullFolds' entry for r under simple folding has
// the same entry, so "ẞ" and "ß" both reach "ss" rather than one of them
// stopping at the other.
func TestFullFoldsCoverWholeOrbits(t *testing.T) {
	for r, want := range fullFolds {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if got, ok := fullFolds[f]; !ok || got != want {
				t.Errorf("fullFolds[%U] = %q, want %q like %U", f, got, want, r)
			}
		}
	}
}
