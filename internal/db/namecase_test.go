package db

import (
	"errors"
	"testing"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
)

// Things lets two projects share a title that differs only by case. A
// --project filter that names one of them exactly lists that project alone,
// the way `open --tag/--area` takes an exact-case name before a folded one
// (issue #290). Only when no title matches exactly does the filter fall back
// to every title equal to it ignoring case.
func TestProjectFilterPrefersExactCase(t *testing.T) {
	d, fx := newFixture(t)
	fx.Project("p-upper", "Garden", 1, anytime())
	fx.Project("p-lower", "garden", 2, anytime())
	fx.Todo("c-upper", "Weed beds", 3, anytime(), dbtest.InProject("p-upper"))
	fx.Todo("c-lower", "Mow lawn", 4, anytime(), dbtest.InProject("p-lower"))

	cases := []struct {
		ref  string
		want []string
	}{
		{"Garden", []string{"c-upper"}},
		{"garden", []string{"c-lower"}},
		{"GARDEN", []string{"c-upper", "c-lower"}}, // no exact title: both fold onto it
		{" Garden ", []string{"c-upper", "c-lower"}},
		{"p-lower", []string{"c-lower"}},
	}
	for _, tc := range cases {
		for _, view := range []string{ViewProject, ViewAnytime} {
			got, err := d.ListTasks(view, TaskFilter{Project: tc.ref})
			if err != nil {
				t.Fatalf("ListTasks(%s, --project %q): %v", view, tc.ref, err)
			}
			if !sameSet(uuidsOf(got), tc.want) {
				t.Errorf("ListTasks(%s, --project %q) = %v, want %v", view, tc.ref, uuidsOf(got), tc.want)
			}
		}
	}
}

// The note on an empty project listing has to name the same project the
// filter listed, or `--project Garden` on an empty ordinary project would be
// explained by a repeating template called "garden".
func TestNamesRepeatingProjectPrefersExactCase(t *testing.T) {
	d, fx := newFixture(t)
	fx.Project("p-real", "Garden", 1, anytime())
	fx.Project("p-tmpl", "garden", 2, someday(), repeats())

	for ref, want := range map[string]bool{"Garden": false, "garden": true, "GARDEN": true} {
		got, err := d.NamesRepeatingProject(ref)
		if err != nil {
			t.Fatalf("NamesRepeatingProject(%q): %v", ref, err)
		}
		if got != want {
			t.Errorf("NamesRepeatingProject(%q) = %v, want %v", ref, got, want)
		}
	}
}

// The bare-title lookup already takes an exact-case title first, for to-dos
// and projects alike. A reference that matches neither exactly stays
// ambiguous rather than picking one: the lookup feeds writes, and it never
// chooses between candidates on the caller's behalf (issue #194).
func TestGetTaskPrefersExactCase(t *testing.T) {
	d, fx := newFixture(t)
	fx.Todo("t-upper", "Water", 1, anytime())
	fx.Todo("t-lower", "water", 2, anytime())
	fx.Project("p-upper", "Garden", 3, anytime())
	fx.Project("p-lower", "garden", 4, anytime())

	for ref, want := range map[string]string{"Water": "t-upper", "water": "t-lower", "Garden": "p-upper", "garden": "p-lower"} {
		got, err := d.GetTask(ref)
		if err != nil {
			t.Fatalf("GetTask(%q): %v", ref, err)
		}
		if got.UUID != want {
			t.Errorf("GetTask(%q) = %q, want %q", ref, got.UUID, want)
		}
	}

	for _, ref := range []string{"WATER", "GARDEN"} {
		_, err := d.GetTask(ref)
		var ambig *AmbiguousTaskError
		if !errors.As(err, &ambig) || len(ambig.Matches) != 2 {
			t.Errorf("GetTask(%q): got %v, want ambiguity between both case variants", ref, err)
		}
	}
}
