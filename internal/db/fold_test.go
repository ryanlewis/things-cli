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
			got := mustList(t, d, "project", tc.filter)
			if !sameSet(uuidsOf(got), tc.want) {
				t.Errorf("got %v, want %v", uuidsOf(got), tc.want)
			}
		})
	}

	t.Run("projects --area", func(t *testing.T) {
		got, err := d.ListProjects("ärger", false, false)
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
			assertSet(t, got, want, "SearchTasks(%q)", query)
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

// The filters, title lookups and search treat a name typed with precomposed
// letters (NFC, "é") and one typed with combining accents (NFD, "e" plus
// U+0301) as the same name, as Things does for tags and list titles. The
// "Café" rows are stored NFC and the "Noël" rows NFD, and each is asked for
// in the other form.
func TestFiltersIgnoreNormalisation(t *testing.T) {
	d := newTestDB(t)

	const (
		cafeNFC = "Café"
		cafeNFD = "Café"
		noelNFC = "Noël"
		noelNFD = "Noël"
	)
	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES
		('ar-c', ?, 1, 1), ('ar-n', ?, 1, 2)`, cafeNFC, noelNFD)
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, area, "index") VALUES
		('proj-c', ?, 1, 0, 0, 'ar-c', 1), ('proj-n', ?, 1, 0, 0, 'ar-n', 2)`, cafeNFC, noelNFD)
	mustExec(t, d, `INSERT INTO TMTag (uuid, title, "index") VALUES
		('tg-c', ?, 1), ('tg-n', ?, 2)`, cafeNFC, noelNFD)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, notes, type, status, trashed, start, startBucket, project, "index") VALUES
		('in-c', ?, '', 0, 0, 0, 1, 0, 'proj-c', 3),
		('in-n', ?, '', 0, 0, 0, 1, 0, 'proj-n', 4)`, "Crème brûlée", "Piña")
	mustExec(t, d, `INSERT INTO TMTaskTag (tasks, tags) VALUES ('in-c', 'tg-c'), ('in-n', 'tg-n')`)

	for _, tc := range []struct {
		name   string
		filter TaskFilter
		want   []string
	}{
		{"project NFD to NFC", TaskFilter{Project: cafeNFD}, []string{"in-c"}},
		{"project NFC to NFD", TaskFilter{Project: noelNFC}, []string{"in-n"}},
		{"area NFD to NFC", TaskFilter{Area: cafeNFD}, []string{"proj-c", "in-c"}},
		{"area NFC to NFD", TaskFilter{Area: noelNFC}, []string{"proj-n", "in-n"}},
		{"tag NFD to NFC", TaskFilter{Tag: cafeNFD}, []string{"in-c"}},
		{"tag NFC to NFD", TaskFilter{Tag: noelNFC}, []string{"in-n"}},
		{"tag NFD upper case", TaskFilter{Tag: "CAFÉ"}, []string{"in-c"}},
		{"project without the accent", TaskFilter{Project: "Cafe"}, nil},
	} {
		t.Run("list "+tc.name, func(t *testing.T) {
			got := mustList(t, d, "project", tc.filter)
			if !sameSet(uuidsOf(got), tc.want) {
				t.Errorf("got %v, want %v", uuidsOf(got), tc.want)
			}
		})
	}

	t.Run("title lookup", func(t *testing.T) {
		for ref, want := range map[string]string{
			"Crème brûlée": "in-c", "PIÑA": "in-n",
		} {
			got, err := d.FindTasksByTitle(ref)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].UUID != want {
				t.Errorf("FindTasksByTitle(%q) = %v, want [%s]", ref, uuidsOf(got), want)
			}
		}
	})

	t.Run("search", func(t *testing.T) {
		for query, want := range map[string][]string{
			"brûlée": {"in-c"}, "piñ": {"in-n"}, "NOËL": {"proj-n"},
		} {
			got, err := d.SearchTasks(query)
			if err != nil {
				t.Fatal(err)
			}
			assertSet(t, got, want, "SearchTasks(%q)", query)
		}
	})
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
		// Canonically equivalent forms fold together, case or not, but
		// compatibility forms (fullwidth "Ｃ") and a missing accent do not.
		{"Café", "Café", true}, {"CAFÉ", "café", true},
		{"Å", "å", true}, {"q̣̇", "Q̣̇", true},
		{"ǰ", "J̌", true}, {"ᾳ", "ᾼ", true},
		{"Cafe", "Café", false}, {"Ｃafé", "Café", false},
	} {
		if got := FoldCase(tc.a) == FoldCase(tc.b); got != tc.equal {
			t.Errorf("FoldCase(%q) == FoldCase(%q) is %v, want %v", tc.a, tc.b, got, tc.equal)
		}
		if strings.EqualFold(tc.a, tc.b) && !tc.equal {
			t.Errorf("EqualFold(%q, %q), but the case says they differ", tc.a, tc.b)
		}
	}
}

// FoldName equates what Things equated in project and area titles: every
// pair below matched there through list= or area=, except the accent pair.
// FoldCase, the tag rule, keeps the compatibility forms apart, as Things did
// for tags; the ligature folds under both.
func TestFoldName(t *testing.T) {
	for _, tc := range []struct {
		a, b        string
		equal, tags bool
	}{
		{"A", "Ａ", true, false}, {"A", "ａ", true, false}, {"2", "²", true, false},
		{"B", "Ⓑ", true, false}, {"x y", "x\u00a0y", true, false},
		{"IV", "Ⅳ", true, false}, {"kg", "㎏", true, false}, {"...", "…", true, false},
		{"1⁄2", "½", true, false}, {"k", "K", true, true}, {"fi", "ﬁ", true, true},
		{"Straße", "STRASSE", true, true}, {"Cafe\u0301", "Caf\u00e9", true, true},
		{"Cafe", "Café", false, false}, {"ı", "i", false, false},
	} {
		if got := FoldName(tc.a) == FoldName(tc.b); got != tc.equal {
			t.Errorf("FoldName(%q) == FoldName(%q) is %v, want %v", tc.a, tc.b, got, tc.equal)
		}
		if got := FoldCase(tc.a) == FoldCase(tc.b); got != tc.tags {
			t.Errorf("FoldCase(%q) == FoldCase(%q) is %v, want %v", tc.a, tc.b, got, tc.tags)
		}
	}
	for _, s := range []string{"Ｗork ² ﬁ", "MiXeD 50%_"} {
		var got string
		if err := dbtest.NewSQL(t).QueryRow(`SELECT fold_name(?)`, s).Scan(&got); err != nil {
			t.Fatalf("fold_name(%q): %v", s, err)
		}
		if got != FoldName(s) {
			t.Errorf("fold_name(%q) = %q, FoldName = %q", s, got, FoldName(s))
		}
	}
}

// The --project and --area filters and `open --area` match project and area
// titles across compatibility forms, as Things does; --tag does not, as
// Things does not. A fullwidth "％" folds to "%", which must still be matched
// as itself, not as a wildcard.
func TestListNamesMatchCompatibilityForms(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES
		('ar-1', 'Area 2', 1, 1), ('ar-pct', '50％', 1, 2)`)
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, area, "index") VALUES
		('proj-1', 'Ｗork', 1, 0, 0, 'ar-1', 1)`)
	mustExec(t, d, `INSERT INTO TMTag (uuid, title, "index") VALUES ('tg-1', 'Tag 2', 1)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, notes, type, status, trashed, start, startBucket, project, "index") VALUES
		('in-1', 'Do it', '', 0, 0, 0, 1, 0, 'proj-1', 2)`)
	mustExec(t, d, `INSERT INTO TMTaskTag (tasks, tags) VALUES ('in-1', 'tg-1')`)

	for _, tc := range []struct {
		name   string
		filter TaskFilter
		want   []string
	}{
		{"project", TaskFilter{Project: "work"}, []string{"in-1"}},
		{"area", TaskFilter{Area: "AREA ²"}, []string{"proj-1", "in-1"}},
		{"area no-break space", TaskFilter{Area: "Area\u00a02"}, []string{"proj-1", "in-1"}},
		{"tag", TaskFilter{Tag: "Tag ²"}, nil},
		{"tag exact", TaskFilter{Tag: "Tag 2"}, []string{"in-1"}},
		{"area wildcard", TaskFilter{Area: "5％"}, nil},
	} {
		t.Run("list "+tc.name, func(t *testing.T) {
			got := mustList(t, d, "project", tc.filter)
			if !sameSet(uuidsOf(got), tc.want) {
				t.Errorf("got %v, want %v", uuidsOf(got), tc.want)
			}
		})
	}

	for ref, want := range map[string]string{"area ²": "ar-1", "50%": "ar-pct", "5%": ""} {
		got, err := d.FindAreaUUID(ref)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("FindAreaUUID(%q) = %q, want %q", ref, got, want)
		}
	}
	if got, err := d.FindTagUUID("Tag ²"); err != nil || got != "" {
		t.Errorf("FindTagUUID(Tag ²) = %q, %v, want no match", got, err)
	}
}

// A folded name still contains every folded part of itself, so a substring
// search keeps working once FoldCase composes its result. Iota folds with the
// combining ypogegrammeni (U+0345), and composing that after an alpha would
// have turned "αι" into "ᾳ", hiding the "ι" a search asked for.
func TestFoldCaseKeepsSubstrings(t *testing.T) {
	for _, tc := range []struct{ s, sub string }{
		{"αι", "ι"}, {"ΑΙ", "ι"}, {"ηι", "Ι"}, {"Café", "caf"}, {"Straße", "SS"},
	} {
		if !strings.Contains(FoldCase(tc.s), FoldCase(tc.sub)) {
			t.Errorf("FoldCase(%q) = %q does not contain FoldCase(%q) = %q",
				tc.s, FoldCase(tc.s), tc.sub, FoldCase(tc.sub))
		}
	}
}

// fold() is registered for every connection, dbtest's included, keeps NULL as
// NULL, and gives the same answer as FoldCase.
func TestFoldSQLMatchesGo(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	for _, s := range []string{"Ärger", "ΚΟΣ", "κος", "Straße", "Café", "MiXeD 50%_"} {
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
