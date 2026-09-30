package db

import (
	"testing"

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
		('ar-k', 'κος',   1, 2)`)
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, area, "index") VALUES
		('proj-a', 'Ärger', 1, 0, 0, 'ar-a', 1),
		('proj-k', 'κος',   1, 0, 0, 'ar-k', 2)`)
	mustExec(t, d, `INSERT INTO TMTag (uuid, title, "index") VALUES
		('tg-a', 'Ärger', 1),
		('tg-k', 'κος',   2)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, notes, type, status, trashed, start, startBucket, project, "index") VALUES
		('in-a', 'Über alles', 'Notiz zu ÄRGER', 0, 0, 0, 1, 0, 'proj-a', 3),
		('in-k', 'Φως',        '',               0, 0, 0, 1, 0, 'proj-k', 4)`)
	mustExec(t, d, `INSERT INTO TMTaskTag (tasks, tags) VALUES ('in-a', 'tg-a'), ('in-k', 'tg-k')`)

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
	fx.project("p-tmpl", "Ärger review", 1, someday(), repeats())

	got, err := d.NamesRepeatingProject("ärger REVIEW")
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("NamesRepeatingProject(ärger REVIEW) = false, want true")
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
