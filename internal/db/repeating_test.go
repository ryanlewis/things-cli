package db

import (
	"fmt"
	"slices"
	"testing"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
)

func seedRepeatingPair(t *testing.T) *DB {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, start, rt1_recurrenceRule)
		 VALUES ('rep-1', 'Water plants', 0, 0, 0, 2, x'0102')`,
	); err != nil {
		t.Fatalf("seed repeating: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, start)
		 VALUES ('one-1', 'Post letter', 0, 0, 0, 2)`,
	); err != nil {
		t.Fatalf("seed one-off: %v", err)
	}
	return &DB{db: sqlDB}
}

func TestGetTaskByUUIDReportsRepeating(t *testing.T) {
	d := seedRepeatingPair(t)

	rep := mustGetByUUID(t, d, "rep-1")
	if !rep.Repeating {
		t.Error("task with a recurrence rule: Repeating = false, want true")
	}

	one := mustGetByUUID(t, d, "one-1")
	if one.Repeating {
		t.Error("task without a recurrence rule: Repeating = true, want false")
	}
}

// Listing and search go through the same templated query, so the flag has to
// survive every path a caller can reach a task by.
func TestListAndSearchReportRepeating(t *testing.T) {
	d := seedRepeatingPair(t)

	tasks := mustList(t, d, "repeating", TaskFilter{})
	if len(tasks) != 1 || tasks[0].UUID != "rep-1" || !tasks[0].Repeating {
		t.Errorf("ListTasks(repeating) = %+v, want just rep-1 flagged repeating", tasks)
	}

	found, err := d.SearchTasks("plants")
	if err != nil {
		t.Fatalf("SearchTasks: %v", err)
	}
	if len(found) != 1 || !found[0].Repeating {
		t.Errorf("SearchTasks(plants) = %+v, want one repeating task", found)
	}
}

// rep-1 and one-1 differ only by the recurrence rule, so someday keeping the
// one-off and dropping the template is the whole of issue #147. The catch-all
// "project" view backs `things --project/--area/--tag`, so it has to drop the
// template too or the leak just moves.
func TestTemplatesExcludedFromOpenViews(t *testing.T) {
	d := seedRepeatingPair(t)

	for _, view := range []string{"someday", "project"} {
		tasks := mustList(t, d, view, TaskFilter{})
		if len(tasks) != 1 || tasks[0].UUID != "one-1" {
			t.Errorf("ListTasks(%q) = %+v, want just one-1", view, uuidsOf(tasks))
		}
	}
}

// A template is a valid target for the same filters as any other view.
func TestRepeatingViewHonoursFilters(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	got := mustList(t, d, "repeating", TaskFilter{Area: "Work"})
	assertSet(t, got, []string{"t-repeat"}, "repeating --area Work")

	got = mustList(t, d, "repeating", TaskFilter{Project: "Nonexistent"})
	if len(got) != 0 {
		t.Errorf("repeating --project Nonexistent = %v, want none", uuidsOf(got))
	}
}

// Trashing a project leaves its rows trashed = 0, so a template inside one
// would outlive the project it lived in — the Repeating view needs the same
// guard the today and project views apply.
func TestRepeatingViewExcludesTrashedProject(t *testing.T) {
	d, fx := newFixture(t)

	fx.Project("proj-gone", "Trashed project", 1, trashed())
	fx.Todo("rep-orphan", "Water plants", 1, someday(), inProject("proj-gone"), repeats())

	got := mustList(t, d, "repeating", TaskFilter{})
	if len(got) != 0 {
		t.Errorf("ListTasks(repeating) = %v, want no templates from a trashed project", uuidsOf(got))
	}
}

// Older Things schemas name the column differently, and a future one could
// drop it. The probe must degrade to "nothing repeats" rather than making
// every task query fail.
func TestRepeatingColumnAbsentDegradesGracefully(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	if _, err := sqlDB.Exec(`ALTER TABLE TMTask DROP COLUMN rt1_recurrenceRule`); err != nil {
		t.Fatalf("drop column: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed) VALUES ('t1', 'Anything', 0, 0, 0)`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	d := &DB{db: sqlDB}

	if col := d.recurrenceCol(); col != "NULL" {
		t.Errorf("recurrenceCol() = %q, want %q", col, "NULL")
	}
	task := mustGetByUUID(t, d, "t1")
	if task == nil || task.Repeating {
		t.Errorf("got %+v, want a task with Repeating false", task)
	}

	// With nothing identifiable as a template, the repeating view is empty
	// and the exclusion the other views apply is a no-op rather than a
	// filter that hides everything.
	rep := mustList(t, d, "repeating", TaskFilter{})
	if len(rep) != 0 {
		t.Errorf("ListTasks(repeating) = %+v, want none", rep)
	}
	open := mustList(t, d, "project", TaskFilter{})
	if len(open) != 1 || open[0].UUID != "t1" {
		t.Errorf("ListTasks(project) = %v, want just t1", uuidsOf(open))
	}
}

func TestRecurrenceColIsProbedOnce(t *testing.T) {
	d := seedRepeatingPair(t)
	first := d.recurrenceCol()
	if first == "NULL" {
		t.Fatalf("recurrenceCol() = %q, want a recurrence-column reference", first)
	}
	if second := d.recurrenceCol(); second != first {
		t.Errorf("recurrenceCol() = %q on second call, want the cached %q", second, first)
	}
}

func TestTableColumnsUnknownTable(t *testing.T) {
	d := &DB{db: dbtest.NewSQL(t)}
	cols, err := d.tableColumns("NoSuchTable")
	if err != nil {
		t.Fatalf("tableColumns: %v", err)
	}
	if len(cols) != 0 {
		t.Errorf("tableColumns(NoSuchTable) = %v, want empty", cols)
	}
}

// A project can repeat too, and the app's Repeating list shows both kinds, so
// the view is the one place that is not pinned to t.type = 0 (issue #165).
// Ordering by type keeps to-dos and projects in contiguous blocks.
func TestRepeatingViewIncludesProjectTemplates(t *testing.T) {
	d, fx := newFixture(t)

	// The project carries the lower "index", so index order alone would put it
	// first: only the type-first ORDER BY yields the order asserted below.
	fx.Project("p-tmpl", "Weekly review", 1, someday(), repeats())
	fx.Todo("t-tmpl", "Water plants", 2, someday(), repeats())

	// Neither an ordinary project nor an ordinary to-do belongs here.
	fx.Project("p-plain", "Ship it", 3, anytime())
	fx.Todo("t-plain", "Post letter", 4, someday())

	got := mustList(t, d, "repeating", TaskFilter{})
	if want := []string{"t-tmpl", "p-tmpl"}; !slices.Equal(uuidsOf(got), want) {
		t.Fatalf("ListTasks(repeating) = %v, want %v (to-dos before projects)", uuidsOf(got), want)
	}
	if got[1].Type != model.TypeProject {
		t.Errorf("project template Type = %d, want %d", got[1].Type, model.TypeProject)
	}
	if !got[0].Repeating || !got[1].Repeating {
		t.Errorf("both rows should carry Repeating = true, got %v and %v", got[0].Repeating, got[1].Repeating)
	}
}

// Headings carry no recurrence rule, but the view no longer pins t.type = 0,
// so a heading must not be able to reach it.
func TestRepeatingViewExcludesHeadings(t *testing.T) {
	d, fx := newFixture(t)

	fx.Heading("h-odd", "A heading", 1, anytime(), repeats())

	got := mustList(t, d, "repeating", TaskFilter{})
	if len(got) != 0 {
		t.Errorf("ListTasks(repeating) = %v, want no headings", uuidsOf(got))
	}
}

// Someday keeps no to-do that sits inside a project, whether that project is a
// repeating template or an ordinary one (issues #171, #211), so the template
// guard cannot be told apart from the parent guard by presence alone. What
// still has to hold is that the view is not simply empty: a top-level Someday
// to-do, and a Someday project row, both list.
func TestTemplateChildExcludedFromSomeday(t *testing.T) {
	d, fx := newFixture(t)

	fx.Project("p-tmpl", "Weekly review", 1, someday(), repeats())
	fx.Project("p-real", "Ship it", 2, someday())
	fx.Todo("t-in-tmpl", "Inside the template", 1, someday(), inProject("p-tmpl"))
	fx.Todo("t-in-real", "Inside a real one", 2, someday(), inProject("p-real"))
	fx.Todo("t-toplevel", "Deferred on its own", 3, someday())

	got := mustList(t, d, "someday", TaskFilter{})
	// p-real is a Someday project and lists as a row of its own; p-tmpl is a
	// template and belongs to the repeating view.
	assertSet(t, got, []string{"t-toplevel", "p-real"}, "someday")
}

// A to-do inside a repeating project template must not list as an ordinary
// task: `things projects` does not report its project, so it would show
// against a project the user cannot see (issue #171). Each case seeds the row
// shape its view selects on, once inside a repeating project template and
// once inside an ordinary project — the sibling proves the guard discriminates
// rather than just hiding everything.
func TestTemplateProjectChildrenExcludedFromOpenViews(t *testing.T) {
	today := int64(model.ThingsDateFromTime(testNow))
	tomorrow := today + (1 << 7)

	cases := []struct {
		view    string
		columns string
		values  string
		// extra names the rows the view carries besides t-plain. The ordinary
		// project p-real is one of them wherever the view lists project rows
		// and p-real's own columns satisfy it — the catch-all view selects on
		// status alone, so it does (issue #222). The template project p-tmpl
		// is never among them.
		extra []string
	}{
		{"inbox", "start, startBucket", "0, 0", nil},
		{"today", "start, startBucket, startDate", fmt.Sprintf("1, 0, %d", today), nil},
		{"upcoming", "start, startBucket, startDate", fmt.Sprintf("2, 0, %d", tomorrow), nil},
		{"anytime", "start, startBucket", "1, 0", nil},
		// someday is absent deliberately: since issue #211 it carries no
		// to-do with a parent project at all, so the sibling this table
		// relies on cannot exist there. TestTemplateChildExcludedFromSomeday
		// covers that view instead.
		{"deadlines", "start, startBucket, deadline", fmt.Sprintf("1, 0, %d", tomorrow), nil},
		{"project", "start, startBucket", "1, 0", []string{"p-real"}},
	}

	for _, tc := range cases {
		t.Run(tc.view, func(t *testing.T) {
			d, fx := newFixture(t)
			// The child carries no recurrence rule — only its project does,
			// which is why the template exclusion cannot see it.
			fx.Project("p-tmpl", "Weekly review", 1, repeats())
			fx.Project("p-real", "Ship it", 2)
			mustExec(t, d, `INSERT INTO TMTask
				(uuid, title, type, status, trashed, project, "index", `+tc.columns+`) VALUES
				('t-child', 'Inside the template', 0, 0, 0, 'p-tmpl', 1, `+tc.values+`),
				('t-plain', 'Inside a real one',   0, 0, 0, 'p-real', 2, `+tc.values+`)`)

			got := mustList(t, d, tc.view, TaskFilter{})
			want := append([]string{"t-plain"}, tc.extra...)
			assertSet(t, got, want, "view %q (the template's child must not list)", tc.view)
		})
	}
}

// The guard resolves the project through COALESCE(t.project, h.project), so a
// to-do nested under a heading in a template project is caught too (#139).
func TestTemplateProjectChildrenExcludedThroughHeading(t *testing.T) {
	d, fx := newFixture(t)

	fx.Project("p-tmpl", "Weekly review", 1, repeats())
	fx.Heading("head-1", "A heading", 1, inProject("p-tmpl"))
	fx.Todo("t-child", "Under the heading", 1, anytime(), underHeading("head-1"))

	got := mustList(t, d, "anytime", TaskFilter{})
	if len(got) != 0 {
		t.Errorf("ListTasks(anytime) = %v, want no heading-nested children of a template project", uuidsOf(got))
	}
}

// A to-do inside a repeating project template carries no rule of its own, so
// the flag has to come from its project — directly or through a heading — or
// writes Things drops silently go out (issue #174). The project Things
// generates from the template points back at it through rt1_repeatingTemplate
// and carries no rule; its to-dos are ordinary work and stay unflagged, as do
// those of an ordinary project.
func TestTemplateProjectChildrenReportRepeating(t *testing.T) {
	d, fx := newFixture(t)
	mustExec(t, d, `ALTER TABLE TMTask ADD COLUMN rt1_repeatingTemplate TEXT`)

	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, "index", rt1_recurrenceRule, rt1_repeatingTemplate) VALUES
		('p-tmpl', 'Weekly review', 1, 0, 0, 1, x'0102', NULL),
		('p-inst', 'Weekly review', 1, 0, 0, 2, NULL, 'p-tmpl'),
		('p-real', 'Ship it',       1, 0, 0, 3, NULL, NULL)`)
	fx.Heading("head-1", "A heading", 1, inProject("p-tmpl"))
	fx.Todo("t-tmpl", "In the template", 1, anytime(), inProject("p-tmpl"))
	fx.Todo("t-head", "Under the heading", 2, anytime(), underHeading("head-1"))
	fx.Todo("t-inst", "In the instance", 3, anytime(), inProject("p-inst"))
	fx.Todo("t-plain", "In an ordinary one", 4, anytime(), inProject("p-real"))

	for uuid, want := range map[string]bool{
		"t-tmpl":  true,
		"t-head":  true,
		"t-inst":  false,
		"t-plain": false,
	} {
		got := mustGetByUUID(t, d, uuid)
		if got.Repeating != want {
			t.Errorf("%s: Repeating = %v, want %v", uuid, got.Repeating, want)
		}
	}
}

// trash and logbook report what the database holds, so a template's child
// that has been trashed or completed still belongs in them. Without this the
// guard would swallow rows those two views exist to show.
func TestTrashAndLogbookKeepTemplateProjectChildren(t *testing.T) {
	d, fx := newFixture(t)

	fx.Project("p-tmpl", "Weekly review", 1, repeats())
	fx.Todo("t-binned", "Trashed child", 1, anytime(), inProject("p-tmpl"), trashed())
	fx.Todo("t-logged", "Completed child", 2, anytime(), inProject("p-tmpl"), status(model.StatusCompleted))

	for _, tc := range []struct{ view, want string }{
		{"trash", "t-binned"},
		{"logbook", "t-logged"},
	} {
		got := mustList(t, d, tc.view, TaskFilter{})
		if !sameSet(uuidsOf(got), []string{tc.want}) {
			t.Errorf("view %q: got %v, want [%s]", tc.view, uuidsOf(got), tc.want)
		}
	}
}

// The guard reads the parent through the `p` alias, so the helper has to
// build a reference for an alias other than `t`, and still degrade to NULL on
// a schema carrying no recurrence column.
func TestRecurrenceColForAlias(t *testing.T) {
	d := seedRepeatingPair(t)
	if got, want := d.recurrenceColFor("p"), `p."rt1_recurrenceRule"`; got != want {
		t.Errorf("recurrenceColFor(p) = %q, want %q", got, want)
	}
	if got, want := d.recurrenceColFor("t"), d.recurrenceCol(); got != want {
		t.Errorf("recurrenceColFor(t) = %q, want recurrenceCol() = %q", got, want)
	}

	sqlDB := dbtest.NewSQL(t)
	if _, err := sqlDB.Exec(`ALTER TABLE TMTask DROP COLUMN rt1_recurrenceRule`); err != nil {
		t.Fatalf("drop column: %v", err)
	}
	bare := &DB{db: sqlDB}
	if got := bare.recurrenceColFor("p"); got != "NULL" {
		t.Errorf("recurrenceColFor(p) with no column = %q, want %q", got, "NULL")
	}
}

// NamesRepeatingProject is what turns an empty project listing into an
// explanation, so it has to recognise the template by uuid and by title, and
// say no to everything else — including an ordinary project and a repeating
// to-do, neither of which explains an empty listing (issue #174).
func TestNamesRepeatingProject(t *testing.T) {
	d, fx := newFixture(t)
	fx.Project("p-tmpl", "Weekly review", 1, someday(), repeats())
	fx.Project("p-real", "Ship it", 2, anytime())
	fx.Todo("t-tmpl", "Water plants", 3, someday(), repeats())

	cases := []struct {
		ref  string
		want bool
	}{
		{"p-tmpl", true},
		{"Weekly review", true},
		{"weekly review", true}, // titles match case-insensitively, as filters do
		{"p-real", false},
		{"Ship it", false},
		{"t-tmpl", false}, // a repeating to-do is not a repeating project
		{"Water plants", false},
		{"Weekly", false}, // the filter matches a title whole, not as a substring
		{"", false},
	}
	for _, tc := range cases {
		got, err := d.NamesRepeatingProject(tc.ref)
		if err != nil {
			t.Fatalf("NamesRepeatingProject(%q): %v", tc.ref, err)
		}
		if got != tc.want {
			t.Errorf("NamesRepeatingProject(%q) = %v, want %v", tc.ref, got, tc.want)
		}
	}
}

// A title carrying LIKE's wildcards is matched literally, the way the filters
// have since issue #266, so a reference cannot claim a template it does not
// name.
func TestNamesRepeatingProjectMatchesLiterally(t *testing.T) {
	d, fx := newFixture(t)
	fx.Project("p-tmpl", "100% review", 1, someday(), repeats())

	for ref, want := range map[string]bool{"100% review": true, "100_ review": false, "%": false} {
		got, err := d.NamesRepeatingProject(ref)
		if err != nil {
			t.Fatalf("NamesRepeatingProject(%q): %v", ref, err)
		}
		if got != want {
			t.Errorf("NamesRepeatingProject(%q) = %v, want %v", ref, got, want)
		}
	}
}

// On a schema carrying no recurrence column nothing repeats, so the check is
// false for every reference rather than an error.
func TestNamesRepeatingProjectWithoutRecurrenceColumn(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	if _, err := sqlDB.Exec(`ALTER TABLE TMTask DROP COLUMN rt1_recurrenceRule`); err != nil {
		t.Fatalf("drop column: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, "index") VALUES ('p-tmpl', 'Weekly review', 1, 0, 0, 1)`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	d := &DB{db: sqlDB}

	got, err := d.NamesRepeatingProject("Weekly review")
	if err != nil {
		t.Fatalf("NamesRepeatingProject: %v", err)
	}
	if got {
		t.Error("nothing repeats on a schema with no recurrence column")
	}
}
