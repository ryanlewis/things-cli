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

// A trashed project is gone from the app, so its exact-case title must not
// hide the open project that differs from it only by case. Both still match.
func TestProjectFilterTrashedExactCaseDoesNotHide(t *testing.T) {
	d, fx := newFixture(t)
	fx.Project("p-gone", "Garden", 1, anytime(), dbtest.Trashed())
	fx.Project("p-open", "garden", 2, anytime())
	fx.Todo("c-gone", "Weed beds", 3, anytime(), dbtest.InProject("p-gone"))
	fx.Todo("c-open", "Mow lawn", 4, anytime(), dbtest.InProject("p-open"))

	got, err := d.ListTasks(ViewProject, TaskFilter{Project: "Garden"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(got), []string{"c-gone", "c-open"}) {
		t.Errorf("--project Garden = %v, want [c-gone c-open]", uuidsOf(got))
	}
}

// --area and --tag take names the same way --project does: an exact-case
// title wins, and only with none does the filter match every title equal to
// it ignoring case and surrounding space. Things lets two areas or two tags
// differ only by case, and without the preference naming one listed both.
func TestAreaAndTagFiltersPreferExactCase(t *testing.T) {
	d, fx := newFixture(t)
	fx.Area("a-upper", "Home", 1)
	fx.Area("a-lower", "home", 2)
	fx.Tag("g-upper", "Errand", 1)
	fx.Tag("g-lower", "errand", 2)
	fx.Todo("t-upper", "Fix tap", 1, anytime(), dbtest.InArea("a-upper"))
	fx.Todo("t-lower", "Water plants", 2, anytime(), dbtest.InArea("a-lower"))
	fx.Tagged("t-upper", "g-upper")
	fx.Tagged("t-lower", "g-lower")

	cases := []struct {
		ref  string
		want []string
	}{
		{"Home", []string{"t-upper"}},
		{"home", []string{"t-lower"}},
		{"HOME", []string{"t-upper", "t-lower"}}, // no exact title: both fold onto it
		{" Home ", []string{"t-upper", "t-lower"}},
	}
	for _, tc := range cases {
		got, err := d.ListTasks(ViewAnytime, TaskFilter{Area: tc.ref})
		if err != nil {
			t.Fatalf("--area %q: %v", tc.ref, err)
		}
		if !sameSet(uuidsOf(got), tc.want) {
			t.Errorf("--area %q = %v, want %v", tc.ref, uuidsOf(got), tc.want)
		}
	}
	if got, err := d.ListTasks(ViewAnytime, TaskFilter{Area: "a-lower"}); err != nil || !sameSet(uuidsOf(got), []string{"t-lower"}) {
		t.Errorf("--area a-lower = %v, %v, want [t-lower]", uuidsOf(got), err)
	}

	tagCases := []struct {
		ref  string
		want []string
	}{
		{"Errand", []string{"t-upper"}},
		{"errand", []string{"t-lower"}},
		{"ERRAND", []string{"t-upper", "t-lower"}},
		{" Errand ", []string{"t-upper", "t-lower"}},
	}
	for _, tc := range tagCases {
		got, err := d.ListTasks(ViewAnytime, TaskFilter{Tag: tc.ref})
		if err != nil {
			t.Fatalf("--tag %q: %v", tc.ref, err)
		}
		if !sameSet(uuidsOf(got), tc.want) {
			t.Errorf("--tag %q = %v, want %v", tc.ref, uuidsOf(got), tc.want)
		}
	}
}

// `things projects --area` shares the rule.
func TestProjectsAreaFilterPrefersExactCase(t *testing.T) {
	d, fx := newFixture(t)
	fx.Area("a-upper", "Home", 1)
	fx.Area("a-lower", "home", 2)
	fx.Project("p-upper", "Kitchen", 1, anytime(), dbtest.InArea("a-upper"))
	fx.Project("p-lower", "Garden", 2, anytime(), dbtest.InArea("a-lower"))

	cases := []struct {
		ref  string
		want []string
	}{
		{"Home", []string{"p-upper"}},
		{"home", []string{"p-lower"}},
		{"HOME", []string{"p-upper", "p-lower"}},
		{" home ", []string{"p-upper", "p-lower"}},
		{"a-upper", []string{"p-upper"}},
	}
	for _, tc := range cases {
		got, err := d.ListProjects(tc.ref, false)
		if err != nil {
			t.Fatalf("projects --area %q: %v", tc.ref, err)
		}
		var uuids []string
		for _, p := range got {
			uuids = append(uuids, p.UUID)
		}
		if !sameSet(uuids, tc.want) {
			t.Errorf("projects --area %q = %v, want %v", tc.ref, uuids, tc.want)
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
