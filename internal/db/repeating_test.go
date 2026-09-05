package db

import (
	"fmt"
	"slices"
	"testing"
	"time"

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

	rep, err := d.GetTaskByUUID("rep-1")
	if err != nil {
		t.Fatalf("GetTaskByUUID(rep-1): %v", err)
	}
	if !rep.Repeating {
		t.Error("task with a recurrence rule: Repeating = false, want true")
	}

	one, err := d.GetTaskByUUID("one-1")
	if err != nil {
		t.Fatalf("GetTaskByUUID(one-1): %v", err)
	}
	if one.Repeating {
		t.Error("task without a recurrence rule: Repeating = true, want false")
	}
}

// Listing and search go through the same templated query, so the flag has to
// survive every path a caller can reach a task by.
func TestListAndSearchReportRepeating(t *testing.T) {
	d := seedRepeatingPair(t)

	tasks, err := d.ListTasks("repeating", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(repeating): %v", err)
	}
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
		tasks, err := d.ListTasks(view, TaskFilter{})
		if err != nil {
			t.Fatalf("ListTasks(%q): %v", view, err)
		}
		if len(tasks) != 1 || tasks[0].UUID != "one-1" {
			t.Errorf("ListTasks(%q) = %+v, want just one-1", view, uuidsOf(tasks))
		}
	}
}

// A template is a valid target for the same filters as any other view.
func TestRepeatingViewHonoursFilters(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	got, err := d.ListTasks("repeating", TaskFilter{Area: "Work"})
	if err != nil {
		t.Fatalf("ListTasks(repeating, area=Work): %v", err)
	}
	if !sameSet(uuidsOf(got), []string{"t-repeat"}) {
		t.Errorf("repeating --area Work = %v, want [t-repeat]", uuidsOf(got))
	}

	got, err = d.ListTasks("repeating", TaskFilter{Project: "Nonexistent"})
	if err != nil {
		t.Fatalf("ListTasks(repeating, project=Nonexistent): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("repeating --project Nonexistent = %v, want none", uuidsOf(got))
	}
}

// Trashing a project leaves its rows trashed = 0, so a template inside one
// would outlive the project it lived in — the Repeating view needs the same
// guard the today and project views apply.
func TestRepeatingViewExcludesTrashedProject(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, "index") VALUES
		('proj-gone', 'Trashed project', 1, 0, 1, 1)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, project, "index", rt1_recurrenceRule) VALUES
		('rep-orphan', 'Water plants', 0, 0, 0, 2, 0, 'proj-gone', 1, x'0102')`)

	got, err := d.ListTasks("repeating", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(repeating): %v", err)
	}
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
	task, err := d.GetTaskByUUID("t1")
	if err != nil {
		t.Fatalf("GetTaskByUUID: %v", err)
	}
	if task == nil || task.Repeating {
		t.Errorf("got %+v, want a task with Repeating false", task)
	}

	// With nothing identifiable as a template, the repeating view is empty
	// and the exclusion the other views apply is a no-op rather than a
	// filter that hides everything.
	rep, err := d.ListTasks("repeating", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(repeating): %v", err)
	}
	if len(rep) != 0 {
		t.Errorf("ListTasks(repeating) = %+v, want none", rep)
	}
	open, err := d.ListTasks("project", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(project): %v", err)
	}
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
	d := newTestDB(t)

	// The project carries the lower "index", so index order alone would put it
	// first: only the type-first ORDER BY yields the order asserted below.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, "index", rt1_recurrenceRule) VALUES
		('p-tmpl',   'Weekly review', 1, 0, 0, 2, 0, 1, x'0102'),
		('t-tmpl',   'Water plants',  0, 0, 0, 2, 0, 2, x'0102')`)

	// Neither an ordinary project nor an ordinary to-do belongs here.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, "index") VALUES
		('p-plain', 'Ship it',    1, 0, 0, 1, 0, 3),
		('t-plain', 'Post letter', 0, 0, 0, 2, 0, 4)`)

	got, err := d.ListTasks("repeating", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(repeating): %v", err)
	}
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
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, "index", rt1_recurrenceRule) VALUES
		('h-odd', 'A heading', 2, 0, 0, 1, 0, 1, x'0102')`)

	got, err := d.ListTasks("repeating", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(repeating): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListTasks(repeating) = %v, want no headings", uuidsOf(got))
	}
}

// A to-do inside a repeating project template must not list as an ordinary
// task: `things projects` does not report its project, so it would show
// against a project the user cannot see (issue #171). Each case seeds the row
// shape its view selects on, once inside a repeating project template and
// once inside an ordinary project — the sibling proves the guard discriminates
// rather than just hiding everything.
func TestTemplateProjectChildrenExcludedFromOpenViews(t *testing.T) {
	today := int64(model.ThingsDateFromTime(time.Now()))
	tomorrow := today + (1 << 7)

	cases := []struct {
		view    string
		columns string
		values  string
	}{
		{"inbox", "start, startBucket", "0, 0"},
		{"today", "start, startBucket, startDate", fmt.Sprintf("1, 0, %d", today)},
		{"upcoming", "start, startBucket, startDate", fmt.Sprintf("2, 0, %d", tomorrow)},
		{"anytime", "start, startBucket", "1, 0"},
		{"someday", "start, startBucket", "2, 0"},
		{"deadlines", "start, startBucket, deadline", fmt.Sprintf("1, 0, %d", tomorrow)},
		{"project", "start, startBucket", "1, 0"},
	}

	for _, tc := range cases {
		t.Run(tc.view, func(t *testing.T) {
			d := newTestDB(t)
			// The child carries no recurrence rule — only its project does,
			// which is why the template exclusion cannot see it.
			mustExec(t, d, `INSERT INTO TMTask
				(uuid, title, type, status, trashed, "index", rt1_recurrenceRule) VALUES
				('p-tmpl', 'Weekly review', 1, 0, 0, 1, x'0102')`)
			mustExec(t, d, `INSERT INTO TMTask
				(uuid, title, type, status, trashed, "index") VALUES
				('p-real', 'Ship it', 1, 0, 0, 2)`)
			mustExec(t, d, `INSERT INTO TMTask
				(uuid, title, type, status, trashed, project, "index", `+tc.columns+`) VALUES
				('t-child', 'Inside the template', 0, 0, 0, 'p-tmpl', 1, `+tc.values+`),
				('t-plain', 'Inside a real one',   0, 0, 0, 'p-real', 2, `+tc.values+`)`)

			got, err := d.ListTasks(tc.view, TaskFilter{})
			if err != nil {
				t.Fatalf("ListTasks(%q): %v", tc.view, err)
			}
			if !sameSet(uuidsOf(got), []string{"t-plain"}) {
				t.Errorf("view %q: got %v, want just t-plain — the template's child must not list", tc.view, uuidsOf(got))
			}
		})
	}
}

// The guard resolves the project through COALESCE(t.project, h.project), so a
// to-do nested under a heading in a template project is caught too (#139).
func TestTemplateProjectChildrenExcludedThroughHeading(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, "index", rt1_recurrenceRule) VALUES
		('p-tmpl', 'Weekly review', 1, 0, 0, 1, x'0102')`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, project, "index") VALUES
		('head-1', 'A heading', 2, 0, 0, 'p-tmpl', 1)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, heading, "index") VALUES
		('t-child', 'Under the heading', 0, 0, 0, 1, 0, 'head-1', 1)`)

	got, err := d.ListTasks("anytime", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(anytime): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListTasks(anytime) = %v, want no heading-nested children of a template project", uuidsOf(got))
	}
}

// trash and logbook report what the database holds, so a template's child
// that has been trashed or completed still belongs in them. Without this the
// guard would swallow rows those two views exist to show.
func TestTrashAndLogbookKeepTemplateProjectChildren(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, "index", rt1_recurrenceRule) VALUES
		('p-tmpl', 'Weekly review', 1, 0, 0, 1, x'0102')`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, project, "index") VALUES
		('t-binned', 'Trashed child',   0, 0, 1, 1, 0, 'p-tmpl', 1),
		('t-logged', 'Completed child', 0, 3, 0, 1, 0, 'p-tmpl', 2)`)

	for _, tc := range []struct{ view, want string }{
		{"trash", "t-binned"},
		{"logbook", "t-logged"},
	} {
		got, err := d.ListTasks(tc.view, TaskFilter{})
		if err != nil {
			t.Fatalf("ListTasks(%q): %v", tc.view, err)
		}
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
