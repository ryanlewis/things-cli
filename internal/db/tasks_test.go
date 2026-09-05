package db

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
)

// seedTasks seeds one area, one project, a few tags, and tasks covering all
// views and statuses.
func seedTasks(t *testing.T, d *DB) {
	t.Helper()

	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES
		('area-work', 'Work', 1, 1)`)

	// Project (type=1) to host a few tasks.
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, area, "index") VALUES
		('proj-1', 'Ship MVP', 1, 0, 0, 'area-work', 1)`)

	// Tags
	mustExec(t, d, `INSERT INTO TMTag (uuid, title, "index") VALUES
		('tg-urgent', 'urgent', 1),
		('tg-home',   'home',   2)`)

	today := int64(model.ThingsDateFromTime(time.Now()))

	// Tasks covering views:
	//   t-today       → today view (start=1, startBucket=0)
	//   t-inbox       → inbox (start=0)
	//   t-evening     → today/evening (start=1, startBucket=1)
	//   t-upcoming    → upcoming, scheduled for the future (start=2, startDate set)
	//   t-anytime     → anytime (start=1, no startDate)
	//   t-someday     → someday (start=2, no startDate)
	//   t-done        → status=completed (logbook)
	//   t-cancelled   → status=cancelled (logbook)
	//   t-trashed     → trashed
	//   t-deadline    → has deadline
	//   t-in-proj     → open task inside proj-1
	//   t-repeat      → repeating template; shaped like t-someday but belongs
	//                   to the repeating view, not someday (issue #147)
	tomorrow := today + (1 << 7)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, notes, type, status, trashed, start, startBucket,
		 startDate, todayIndexReferenceDate, deadline, project, area, "index", todayIndex) VALUES
		('t-today',     'Today task',    '',       0, 0, 0, 1, 0, ?, ?, NULL, NULL,     NULL,        10, 1),
		('t-inbox',     'Inbox task',    'notes',  0, 0, 0, 0, 0, NULL, NULL, NULL, NULL,     NULL,        11, 0),
		('t-evening',   'Evening task',  '',       0, 0, 0, 1, 1, ?, NULL, NULL, NULL,     NULL,        12, 0),
		('t-upcoming',  'Upcoming task', '',       0, 0, 0, 2, 0, ?, NULL, NULL, NULL,     NULL,        22, 0),
		('t-anytime',   'Anytime task',  '',       0, 0, 0, 1, 0, NULL, NULL, NULL, NULL,     NULL,        13, 0),
		('t-someday',   'Someday task',  '',       0, 0, 0, 2, 0, NULL, NULL, NULL, NULL,     NULL,        14, 0),
		('t-done',      'Done task',     '',       0, 3, 0, 0, 0, NULL, NULL, NULL, NULL,     NULL,        15, 0),
		('t-cancelled', 'Cancelled',     '',       0, 2, 0, 0, 0, NULL, NULL, NULL, NULL,     NULL,        16, 0),
		('t-trashed',   'Trashed task',  '',       0, 0, 1, 0, 0, NULL, NULL, NULL, NULL,     NULL,        17, 0),
		('t-deadline',  'Has deadline',  '',       0, 0, 0, 1, 0, NULL, NULL, ?,    NULL,     NULL,        18, 0),
		('t-in-proj',   'Project task',  '',       0, 0, 0, 0, 0, NULL, NULL, NULL, 'proj-1', 'area-work', 19, 0)`,
		today, today, today, tomorrow, tomorrow) // last arg is the deadline

	// Templates carry the recurrence rule; Things files them under Repeating
	// while their row otherwise looks exactly like a Someday to-do.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, project, area, "index", rt1_recurrenceRule) VALUES
		('t-repeat', 'Water plants', 0, 0, 0, 2, 0, 'proj-1', 'area-work', 20, x'0102')`)

	// Tag the today task with urgent + home
	mustExec(t, d, `INSERT INTO TMTaskTag (tasks, tags) VALUES
		('t-today', 'tg-urgent'),
		('t-today', 'tg-home')`)

	// stopDate on the done task so logbook has something to order by
	done := model.TimeToUnix(time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC))
	mustExec(t, d, `UPDATE TMTask SET stopDate = ? WHERE uuid = 't-done'`, done)
}

func TestValidView(t *testing.T) {
	known := []string{"today", "inbox", "upcoming", "anytime", "someday", "repeating", "logbook", "trash", "deadlines", "project"}
	for _, v := range known {
		if !ValidView(v) {
			t.Errorf("%q should be valid", v)
		}
	}
	if ValidView("bogus") {
		t.Errorf("%q should not be valid", "bogus")
	}
}

func TestListTasksUnknownView(t *testing.T) {
	d := newTestDB(t)
	_, err := d.ListTasks("bogus", TaskFilter{})
	if err == nil {
		t.Fatal("expected error for unknown view")
	}
}

func TestListTasksViews(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	cases := []struct {
		view string
		want []string
	}{
		// Today view includes both the Today bucket (startBucket=0, here t-today)
		// and the Evening bucket (startBucket=1, here t-evening). This mirrors
		// the Things app, which lists Evening items beneath Today's main list.
		{"today", []string{"t-today", "t-evening"}},
		{"inbox", []string{"t-inbox", "t-in-proj"}},
		{"upcoming", []string{"t-upcoming"}},
		// Anytime is everything with start=1 — Today, Evening, and undated.
		{"anytime", []string{"t-today", "t-evening", "t-anytime", "t-deadline"}},
		// t-repeat has the same start/startDate shape as t-someday but is a
		// template, so it belongs to repeating and nowhere else (issue #147).
		{"someday", []string{"t-someday"}},
		{"repeating", []string{"t-repeat"}},
		{"logbook", []string{"t-done"}},
		{"trash", []string{"t-trashed"}},
		{"deadlines", []string{"t-deadline"}},
	}

	for _, tc := range cases {
		t.Run(tc.view, func(t *testing.T) {
			got, err := d.ListTasks(tc.view, TaskFilter{})
			if err != nil {
				t.Fatalf("ListTasks(%q): %v", tc.view, err)
			}
			gotUUIDs := uuidsOf(got)
			if !sameSet(gotUUIDs, tc.want) {
				t.Errorf("view %q: got %v, want %v", tc.view, gotUUIDs, tc.want)
			}
		})
	}
}

// By default the today view returns only open tasks (issue #106) — completed
// and cancelled items never appear, even before Things logs them out of Today.
// With IncludeCompleted, those items remain visible until "Log Completed Now"
// bumps TMSettings.manualLogDate past their stopDate, matching the Things app
// (which keeps them on screen regardless of todayIndexReferenceDate until the
// user explicitly logs).
func TestListTasksTodayCompletedItemFiltering(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	// AddDate keeps the ThingsDate valid across month boundaries; raw bit
	// subtraction would underflow the day field to 0 on the 1st.
	today := int64(model.ThingsDateFromTime(time.Now()))
	yesterday := int64(model.ThingsDateFromTime(time.Now().AddDate(0, 0, -1)))
	stopToday := model.TimeToUnix(time.Now().Add(-1 * time.Minute))
	stopYesterday := model.TimeToUnix(time.Now().Add(-25 * time.Hour))

	// Completed today, not yet logged.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate,
		 todayIndexReferenceDate, stopDate, "index")
		VALUES ('t-just-done', 'Just done', 0, 3, 0, 1, 0, ?, ?, ?, 20)`,
		today, today, stopToday)

	// Completed yesterday but not yet logged.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate,
		 todayIndexReferenceDate, stopDate, "index")
		VALUES ('t-done-yesterday', 'Done yesterday', 0, 3, 0, 1, 0, ?, ?, ?, 21)`,
		today, yesterday, stopYesterday)

	// Cancelled today, not yet logged — exercises the status=2 branch of
	// `status IN (2, 3)`, which the completed (status=3) fixtures don't cover.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate,
		 todayIndexReferenceDate, stopDate, "index")
		VALUES ('t-cancelled-today', 'Cancelled today', 0, 2, 0, 1, 0, ?, ?, ?, 22)`,
		today, today, stopToday)

	// Default: completed/cancelled items are excluded outright.
	got, err := d.ListTasks("today", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks today: %v", err)
	}
	if !sameSet([]string{"t-today", "t-evening"}, uuidsOf(got)) {
		t.Fatalf("default: expected {t-today, t-evening}, got %v", uuidsOf(got))
	}

	// IncludeCompleted (pre-log): unlogged completed and cancelled items reappear.
	got, err = d.ListTasks("today", TaskFilter{IncludeCompleted: true})
	if err != nil {
		t.Fatalf("ListTasks today --include-completed: %v", err)
	}
	want := []string{"t-today", "t-evening", "t-just-done", "t-done-yesterday", "t-cancelled-today"}
	if !sameSet(want, uuidsOf(got)) {
		t.Fatalf("pre-log: expected %v, got %v", want, uuidsOf(got))
	}

	// Simulate "Log Completed Now": bump manualLogDate past both stopDates.
	future := model.TimeToUnix(time.Now().Add(1 * time.Minute))
	mustExec(t, d, `INSERT INTO TMSettings (uuid, manualLogDate) VALUES ('s', ?)`, future)

	got, err = d.ListTasks("today", TaskFilter{IncludeCompleted: true})
	if err != nil {
		t.Fatalf("ListTasks today --include-completed: %v", err)
	}
	if !sameSet([]string{"t-today", "t-evening"}, uuidsOf(got)) {
		t.Fatalf("post-log: expected {t-today, t-evening}, got %v", uuidsOf(got))
	}
}

func TestListTasksProjectFilter(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	byUUID, err := d.ListTasks("project", TaskFilter{Project: "proj-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byUUID) != 1 || byUUID[0].UUID != "t-in-proj" {
		t.Errorf("project uuid filter: got %+v", uuidsOf(byUUID))
	}

	byTitle, err := d.ListTasks("project", TaskFilter{Project: "Ship MVP"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byTitle) != 1 || byTitle[0].UUID != "t-in-proj" {
		t.Errorf("project title filter: got %+v", uuidsOf(byTitle))
	}
}

func TestListTasksAreaFilter(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	// t-in-proj inherits area-work via its project (pa.uuid join).
	tasks, err := d.ListTasks("project", TaskFilter{Area: "area-work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].UUID != "t-in-proj" {
		t.Errorf("area filter: got %+v", uuidsOf(tasks))
	}
}

func TestListTasksTagFilter(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	tasks, err := d.ListTasks("today", TaskFilter{Tag: "urgent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].UUID != "t-today" {
		t.Errorf("tag filter: got %+v", uuidsOf(tasks))
	}

	// Non-matching tag
	none, err := d.ListTasks("today", TaskFilter{Tag: "does-not-exist"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("expected empty, got %+v", uuidsOf(none))
	}
}

func TestListTasksDateFilters(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES ('a', 'Work', 1, 0)`)

	d1 := int64(model.ThingsDateFromTime(time.Date(2026, 5, 9, 0, 0, 0, 0, time.Local)))
	d2 := int64(model.ThingsDateFromTime(time.Date(2026, 5, 10, 0, 0, 0, 0, time.Local)))
	d3 := int64(model.ThingsDateFromTime(time.Date(2026, 5, 11, 0, 0, 0, 0, time.Local)))

	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, start, startBucket, startDate, area, "index") VALUES
		('u-09', 'Sat', 0, 0, 0, 2, 0, ?, 'a', 1),
		('u-10', 'Sun', 0, 0, 0, 2, 0, ?, 'a', 2),
		('u-11', 'Mon', 0, 0, 0, 2, 0, ?, 'a', 3)`,
		d1, d2, d3)

	on09 := model.ThingsDate(d1)
	on10 := model.ThingsDate(d2)

	cases := []struct {
		name   string
		filter TaskFilter
		want   []string
	}{
		{"on exact", TaskFilter{On: &on09}, []string{"u-09"}},
		{"from inclusive", TaskFilter{From: &on10}, []string{"u-10", "u-11"}},
		{"to inclusive", TaskFilter{To: &on10}, []string{"u-09", "u-10"}},
		{"range weekend", TaskFilter{From: &on09, To: &on10}, []string{"u-09", "u-10"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := d.ListTasks("upcoming", tc.filter)
			if err != nil {
				t.Fatalf("ListTasks: %v", err)
			}
			if !sameSet(uuidsOf(got), tc.want) {
				t.Errorf("got %v, want %v", uuidsOf(got), tc.want)
			}
		})
	}
}

// On the deadlines view, --on/--from/--to filter against t.deadline rather
// than t.startDate; verify we hit the right column.
func TestListTasksDeadlinesDateFilters(t *testing.T) {
	d := newTestDB(t)

	d1 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)))
	d2 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 2, 0, 0, 0, 0, time.Local)))

	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, deadline, "index") VALUES
		('dl-1', 'A', 0, 0, 0, ?, 1),
		('dl-2', 'B', 0, 0, 0, ?, 2)`,
		d1, d2)

	on := model.ThingsDate(d1)
	got, err := d.ListTasks("deadlines", TaskFilter{On: &on})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if !sameSet(uuidsOf(got), []string{"dl-1"}) {
		t.Errorf("deadlines --on: got %v", uuidsOf(got))
	}
}

func TestDateFilterableView(t *testing.T) {
	allowed := []string{"today", "upcoming", "anytime", "deadlines", "project"}
	// someday is denied because its view predicate requires startDate IS NULL —
	// a startDate range filter could never match anything.
	denied := []string{"inbox", "trash", "logbook", "someday", "bogus"}
	for _, v := range allowed {
		if !DateFilterableView(v) {
			t.Errorf("%q: expected filterable", v)
		}
	}
	for _, v := range denied {
		if DateFilterableView(v) {
			t.Errorf("%q: expected NOT filterable", v)
		}
	}
}

func TestTagGroupConcatDelimiter(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	// Filter to t-today specifically; today now also includes the Evening
	// bucket (t-evening), so don't assert the row count of the whole view.
	tasks, err := d.ListTasks("today", TaskFilter{Tag: "urgent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d, want 1", len(tasks))
	}
	tags := tasks[0].Tags
	if len(tags) != 2 {
		t.Fatalf("tags: got %v, want 2 entries", tags)
	}
	// Confirm no tag string contains the unit separator (split succeeded).
	for _, tg := range tags {
		if tg == "" {
			t.Errorf("empty tag in %v", tags)
		}
	}
}

func TestGetTaskByUUID(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	got, err := d.GetTaskByUUID("t-today")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Title != "Today task" {
		t.Errorf("got %+v", got)
	}

	missing, err := d.GetTaskByUUID("nope")
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Errorf("expected nil, got %+v", missing)
	}
}

func TestGetTaskExactTitle(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	got, err := d.GetTask("Inbox task")
	if err != nil {
		t.Fatal(err)
	}
	if got.UUID != "t-inbox" {
		t.Errorf("got %q, want t-inbox", got.UUID)
	}
}

func TestGetTaskUUIDFirst(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	// UUID match should take precedence over title fallback.
	got, err := d.GetTask("t-today")
	if err != nil {
		t.Fatal(err)
	}
	if got.UUID != "t-today" {
		t.Errorf("got %q, want t-today", got.UUID)
	}
}

func TestGetTaskLikeMatchSingle(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	got, err := d.GetTask("Someday")
	if err != nil {
		t.Fatal(err)
	}
	if got.UUID != "t-someday" {
		t.Errorf("got %q", got.UUID)
	}
}

func TestGetTaskAmbiguous(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	// "task" appears in several open titles — ensure we get an AmbiguousTaskError.
	_, err := d.GetTask("task")
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
	var ambig *AmbiguousTaskError
	if !errors.As(err, &ambig) {
		t.Fatalf("wrong error type: %T: %v", err, err)
	}
	if len(ambig.Matches) < 2 {
		t.Errorf("expected ≥2 matches, got %d", len(ambig.Matches))
	}
	if ambig.Query != "task" {
		t.Errorf("Query = %q", ambig.Query)
	}
	if ambig.Error() == "" {
		t.Errorf("Error() should produce a message")
	}
}

func TestGetTaskNotFound(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	_, err := d.GetTask("zzz-does-not-exist-xyz")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	var ambig *AmbiguousTaskError
	if errors.As(err, &ambig) {
		t.Errorf("should not be ambiguous: %v", err)
	}
	// Typed so callers can render it as structured output (issue #152).
	var notFound *TaskNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("wrong error type: %T: %v", err, err)
	}
	if notFound.Query != "zzz-does-not-exist-xyz" {
		t.Errorf("Query = %q", notFound.Query)
	}
	if notFound.Error() != "task not found: zzz-does-not-exist-xyz" {
		t.Errorf("Error() = %q", notFound.Error())
	}
}

func TestSearchTasksTitleAndNotes(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	byTitle, err := d.SearchTasks("Inbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(byTitle) != 1 || byTitle[0].UUID != "t-inbox" {
		t.Errorf("title search: got %+v", uuidsOf(byTitle))
	}

	// "notes" appears in the notes field of t-inbox only.
	byNotes, err := d.SearchTasks("notes")
	if err != nil {
		t.Fatal(err)
	}
	if len(byNotes) != 1 || byNotes[0].UUID != "t-inbox" {
		t.Errorf("notes search: got %+v", uuidsOf(byNotes))
	}

	// Trashed tasks still match search (SearchTasks only filters trashed=0)
	// — verify trashed excluded.
	trashed, err := d.SearchTasks("Trashed")
	if err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 0 {
		t.Errorf("trashed task should not match: %+v", uuidsOf(trashed))
	}
}

func TestFindTasksByTitleLike(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	got, err := d.FindTasksByTitle("Upcoming")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].UUID != "t-upcoming" {
		t.Errorf("got %+v", uuidsOf(got))
	}
}

func TestScanTaskFieldsPopulated(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	got, err := d.GetTaskByUUID("t-in-proj")
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectUUID != "proj-1" || got.ProjectTitle != "Ship MVP" {
		t.Errorf("project link: %+v", got)
	}
	if got.AreaUUID != "area-work" || got.AreaTitle != "Work" {
		t.Errorf("area link: uuid=%q title=%q", got.AreaUUID, got.AreaTitle)
	}
}

// --- helpers ---

func uuidsOf(tasks []model.Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.UUID
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]int, len(a))
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
		if m[x] < 0 {
			return false
		}
	}
	return true
}

// seedHeadingTasks builds a project with a heading, holding one task directly
// on the project and one under the heading. The heading-nested task leaves
// t.project NULL, as Things does — the heading row carries the project.
func seedHeadingTasks(t *testing.T, d *DB) {
	t.Helper()

	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES
		('area-launch', 'Launch', 1, 1)`)
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, area, "index") VALUES
		('proj-h', 'Ship v2', 1, 0, 0, 'area-launch', 1)`)
	// Heading (type=2) belonging to proj-h.
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, project, "index") VALUES
		('head-1', 'Phase one', 2, 0, 0, 'proj-h', 2)`)
	mustExec(t, d, `INSERT INTO TMTag (uuid, title, "index") VALUES ('tg-ship', 'ship', 1)`)

	today := int64(model.ThingsDateFromTime(time.Now()))
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, project, heading, "index") VALUES
		('t-direct',  'Direct task',  0, 0, 0, 1, 0, ?,    'proj-h', NULL,     3),
		('t-nested',  'Nested task',  0, 0, 0, 1, 0, NULL, NULL,     'head-1', 4),
		('t-loose',   'Loose task',   0, 0, 0, 1, 0, NULL, NULL,     NULL,     5)`,
		today)
	mustExec(t, d, `INSERT INTO TMTaskTag (tasks, tags) VALUES ('t-nested', 'tg-ship')`)
}

// Tasks filed under a project heading have no t.project of their own; the
// project filter has to reach it through the heading row (issue #139).
func TestListTasksProjectFilterIncludesHeadingTasks(t *testing.T) {
	d := newTestDB(t)
	seedHeadingTasks(t, d)

	for _, filter := range []string{"proj-h", "Ship v2"} {
		got, err := d.ListTasks("project", TaskFilter{Project: filter})
		if err != nil {
			t.Fatalf("ListTasks(project, %q): %v", filter, err)
		}
		if !sameSet(uuidsOf(got), []string{"t-direct", "t-nested"}) {
			t.Errorf("project filter %q: got %v, want [t-direct t-nested]", filter, uuidsOf(got))
		}
	}
}

// The area comes from the project, so a heading-nested task has to inherit it
// through the heading too.
func TestListTasksAreaFilterIncludesHeadingTasks(t *testing.T) {
	d := newTestDB(t)
	seedHeadingTasks(t, d)

	got, err := d.ListTasks("project", TaskFilter{Area: "Launch"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if !sameSet(uuidsOf(got), []string{"t-direct", "t-nested"}) {
		t.Errorf("area filter: got %v, want [t-direct t-nested]", uuidsOf(got))
	}
}

// A heading-nested task reports its project in output, so it can't be mistaken
// for a standalone task (issue #139).
func TestHeadingTaskCarriesProject(t *testing.T) {
	d := newTestDB(t)
	seedHeadingTasks(t, d)

	task, err := d.GetTaskByUUID("t-nested")
	if err != nil {
		t.Fatalf("GetTaskByUUID: %v", err)
	}
	if task == nil {
		t.Fatal("t-nested not found")
	}
	if task.ProjectUUID != "proj-h" || task.ProjectTitle != "Ship v2" {
		t.Errorf("project = %q/%q, want proj-h/Ship v2", task.ProjectUUID, task.ProjectTitle)
	}
	if task.HeadingUUID != "head-1" || task.HeadingTitle != "Phase one" {
		t.Errorf("heading = %q/%q, want head-1/Phase one", task.HeadingUUID, task.HeadingTitle)
	}
	if task.AreaUUID != "area-launch" || task.AreaTitle != "Launch" {
		t.Errorf("area = %q/%q, want area-launch/Launch", task.AreaUUID, task.AreaTitle)
	}
}

// The project view is the whole open set, so a project filter run against it
// returns tasks the today view would have hidden (issue #140).
func TestListTasksProjectViewIsNotATodaySlice(t *testing.T) {
	d := newTestDB(t)
	seedHeadingTasks(t, d)

	today, err := d.ListTasks("today", TaskFilter{Project: "Ship v2"})
	if err != nil {
		t.Fatalf("ListTasks(today): %v", err)
	}
	if !sameSet(uuidsOf(today), []string{"t-direct"}) {
		t.Fatalf("today slice: got %v, want [t-direct]", uuidsOf(today))
	}

	all, err := d.ListTasks("project", TaskFilter{Project: "Ship v2"})
	if err != nil {
		t.Fatalf("ListTasks(project): %v", err)
	}
	if !sameSet(uuidsOf(all), []string{"t-direct", "t-nested"}) {
		t.Errorf("project view: got %v, want [t-direct t-nested]", uuidsOf(all))
	}
}

// Tags live on the task itself, but the tag filter still has to work on a
// heading-nested task now that the project join reaches through the heading.
func TestListTasksTagFilterIncludesHeadingTasks(t *testing.T) {
	d := newTestDB(t)
	seedHeadingTasks(t, d)

	got, err := d.ListTasks("project", TaskFilter{Tag: "ship"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if !sameSet(uuidsOf(got), []string{"t-nested"}) {
		t.Errorf("tag filter: got %v, want [t-nested]", uuidsOf(got))
	}
}

// The project view is the default for a bare --project/--area/--tag filter, so
// it must hide tasks living in a trashed project the way the today view does.
func TestListTasksProjectViewExcludesTrashedProject(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMTag (uuid, title, "index") VALUES ('tg-u', 'urgent', 1)`)
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, "index") VALUES
		('proj-gone', 'Trashed project', 1, 0, 1, 1)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, project, "index") VALUES
		('t-orphan', 'Child of trashed', 0, 0, 0, 1, 0, 'proj-gone', 1)`)
	mustExec(t, d, `INSERT INTO TMTaskTag (tasks, tags) VALUES ('t-orphan', 'tg-u')`)

	got, err := d.ListTasks("project", TaskFilter{Tag: "urgent"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want no tasks from a trashed project", uuidsOf(got))
	}
}

// Trashing a project in Things leaves its child rows at trashed = 0, so every
// open view has to check the project as well as the task or those children
// outlive the project they lived in (issue #155). One case per view, each
// seeding the row shape that view selects on, all of them inside a trashed
// project.
func TestListTasksViewsExcludeTrashedProject(t *testing.T) {
	today := int64(model.ThingsDateFromTime(time.Now()))
	tomorrow := today + (1 << 7)

	// view → the columns beyond the shared ones that put a row in that view.
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
			mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, "index") VALUES
				('proj-gone', 'Trashed project', 1, 0, 1, 1)`)
			mustExec(t, d, `INSERT INTO TMTask
				(uuid, title, type, status, trashed, project, "index", `+tc.columns+`) VALUES
				('t-orphan', 'Child of trashed', 0, 0, 0, 'proj-gone', 1, `+tc.values+`)`)

			got, err := d.ListTasks(tc.view, TaskFilter{})
			if err != nil {
				t.Fatalf("ListTasks(%q): %v", tc.view, err)
			}
			if len(got) != 0 {
				t.Errorf("view %q: got %v, want no tasks from a trashed project", tc.view, uuidsOf(got))
			}
		})
	}
}

// A task under a heading carries t.heading and leaves t.project NULL, so the
// guard has to reach the project through the heading the way --project does
// (issue #139), or heading-nested children of a trashed project slip past it.
func TestListTasksExcludesTrashedProjectThroughHeading(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, "index") VALUES
		('proj-gone', 'Trashed project', 1, 0, 1, 1),
		('head-1',    'A heading',       2, 0, 0, 2)`)
	mustExec(t, d, `UPDATE TMTask SET project = 'proj-gone' WHERE uuid = 'head-1'`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, heading, "index") VALUES
		('t-orphan', 'Under the heading', 0, 0, 0, 1, 0, 'head-1', 1)`)

	got, err := d.ListTasks("anytime", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(anytime): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want no heading-nested tasks from a trashed project", uuidsOf(got))
	}
}

// trash and logbook report what the database holds rather than what Things
// would show as actionable, so they keep the children of a trashed project.
// Without this the guard would silently swallow them.
func TestTrashAndLogbookKeepTrashedProjectChildren(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, "index") VALUES
		('proj-gone', 'Trashed project', 1, 0, 1, 1)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, project, "index") VALUES
		('t-binned', 'Trashed child',   0, 0, 1, 1, 0, 'proj-gone', 1),
		('t-logged', 'Completed child', 0, 3, 0, 1, 0, 'proj-gone', 2)`)

	for _, tc := range []struct {
		view string
		want string
	}{
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

// A filter spanning several projects must keep each project's tasks contiguous,
// otherwise the rendered group headers repeat as rows interleave by index.
func TestListTasksProjectViewGroupsByProject(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES ('ar-h', 'Home', 1, 1)`)
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, area, "index") VALUES
		('proj-a', 'Project A', 1, 0, 0, 'ar-h', 1),
		('proj-b', 'Project B', 1, 0, 0, 'ar-h', 2)`)
	// Interleaved task indexes: index order alone would alternate A, B, A, B.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, project, "index") VALUES
		('a1', 'A one', 0, 0, 0, 1, 0, 'proj-a', 1),
		('b1', 'B one', 0, 0, 0, 1, 0, 'proj-b', 2),
		('a2', 'A two', 0, 0, 0, 1, 0, 'proj-a', 3),
		('b2', 'B two', 0, 0, 0, 1, 0, 'proj-b', 4)`)

	got, err := d.ListTasks("project", TaskFilter{Area: "Home"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	var order []string
	for _, task := range got {
		order = append(order, task.UUID)
	}
	want := []string{"a1", "a2", "b1", "b2"}
	if len(order) != len(want) {
		t.Fatalf("got %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("got %v, want %v", order, want)
		}
	}
}

// --- heading exclusion from lookups (issue #146) ---

// seedLookupHeadings adds two headings on top of the seedTasks fixture: one
// with a title of its own, and one whose title collides exactly with an open
// to-do so a lookup that failed to filter would either return the wrong row or
// report the pair as ambiguous.
func seedLookupHeadings(t *testing.T, d *DB) {
	t.Helper()

	mustExec(t, d, `INSERT INTO TMTask (uuid, title, notes, type, status, trashed, project, "index") VALUES
		('head-phase', 'Phase one',  'heading notes', 2, 0, 0, 'proj-1', 20),
		('head-dupe',  'Inbox task', '',              2, 0, 0, 'proj-1', 21)`)
}

func TestGetTaskByUUIDExcludesHeading(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)
	seedLookupHeadings(t, d)

	got, err := d.GetTaskByUUID("head-phase")
	if err != nil {
		t.Fatalf("GetTaskByUUID: %v", err)
	}
	if got != nil {
		t.Errorf("heading returned as a task: %+v", got)
	}
}

// Projects still resolve — show, edit, complete, cancel and open all rely on
// GetTaskByUUID returning a project row.
func TestGetTaskByUUIDKeepsProject(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)
	seedLookupHeadings(t, d)

	got, err := d.GetTaskByUUID("proj-1")
	if err != nil {
		t.Fatalf("GetTaskByUUID: %v", err)
	}
	if got == nil || got.Type != model.TypeProject {
		t.Fatalf("project lookup: got %+v", got)
	}
}

// A heading sharing a to-do's exact title must neither win the exact-title
// match nor make it ambiguous.
func TestGetTaskExactTitleSkipsHeading(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)
	seedLookupHeadings(t, d)

	got, err := d.GetTask("Inbox task")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.UUID != "t-inbox" {
		t.Errorf("got %q, want t-inbox", got.UUID)
	}
}

func TestGetTaskLikeMatchSkipsHeading(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)
	seedLookupHeadings(t, d)

	// "Phase" matches only the heading, so the lookup should not find a task.
	if _, err := d.GetTask("Phase"); err == nil {
		t.Fatal("expected not-found error for a heading-only title")
	}

	// A single to-do plus a same-named heading resolves to the to-do rather
	// than raising AmbiguousTaskError.
	got, err := d.GetTask("nbox tas")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.UUID != "t-inbox" {
		t.Errorf("got %q, want t-inbox", got.UUID)
	}
}

func TestFindTasksByTitleExcludesHeadings(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)
	seedLookupHeadings(t, d)

	got, err := d.FindTasksByTitle("Phase")
	if err != nil {
		t.Fatalf("FindTasksByTitle: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want no matches", uuidsOf(got))
	}

	dupes, err := d.FindTasksByTitle("Inbox task")
	if err != nil {
		t.Fatalf("FindTasksByTitle: %v", err)
	}
	if !sameSet(uuidsOf(dupes), []string{"t-inbox"}) {
		t.Errorf("got %v, want [t-inbox]", uuidsOf(dupes))
	}
}

func TestSearchTasksExcludesHeadings(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)
	seedLookupHeadings(t, d)

	byTitle, err := d.SearchTasks("Phase")
	if err != nil {
		t.Fatalf("SearchTasks: %v", err)
	}
	if len(byTitle) != 0 {
		t.Errorf("title search: got %v, want no matches", uuidsOf(byTitle))
	}

	byNotes, err := d.SearchTasks("heading notes")
	if err != nil {
		t.Fatalf("SearchTasks: %v", err)
	}
	if len(byNotes) != 0 {
		t.Errorf("notes search: got %v, want no matches", uuidsOf(byNotes))
	}

	// The to-do sharing the heading's title still comes back, alone.
	shared, err := d.SearchTasks("Inbox task")
	if err != nil {
		t.Fatalf("SearchTasks: %v", err)
	}
	if !sameSet(uuidsOf(shared), []string{"t-inbox"}) {
		t.Errorf("got %v, want [t-inbox]", uuidsOf(shared))
	}

	// Projects remain searchable.
	proj, err := d.SearchTasks("Ship MVP")
	if err != nil {
		t.Fatalf("SearchTasks: %v", err)
	}
	if !sameSet(uuidsOf(proj), []string{"proj-1"}) {
		t.Errorf("project search: got %v, want [proj-1]", uuidsOf(proj))
	}
}

// --- repeating template vs. its instance in title lookups (issue #156) ---

// seedTemplateAndInstance creates a repeating to-do the way Things stores one:
// a template carrying the recurrence rule (start=2/someday-ish, no startDate)
// and the instance generated from it (start=1, scheduled, no rule), sharing a
// title. The template is given the lower "index" so index order alone would
// pick it — the ordering under test is what puts the instance first.
func seedTemplateAndInstance(t *testing.T, d *DB) {
	t.Helper()

	today := int64(model.ThingsDateFromTime(time.Now()))
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, "index", rt1_recurrenceRule) VALUES
		('tpl-water',  'Water plants', 0, 0, 0, 2, 0, NULL, 1, x'0102'),
		('inst-water', 'Water plants', 0, 0, 0, 1, 0, ?,    2, NULL)`,
		today)
}

// `things complete "Water plants"` must land on the instance: the template
// refuses writes (issue #143), so resolving to it strands the user.
func TestGetTaskExactTitlePrefersInstance(t *testing.T) {
	d := newTestDB(t)
	seedTemplateAndInstance(t, d)

	got, err := d.GetTask("Water plants")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.UUID != "inst-water" {
		t.Errorf("got %q (repeating=%v), want inst-water", got.UUID, got.Repeating)
	}
	if got.Repeating {
		t.Error("resolved row should not be the template")
	}
}

// The template is still reachable when nothing else matches — ordering, not
// filtering, so `things show` on a template-only title keeps working.
func TestGetTaskExactTitleTemplateOnly(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, "index", rt1_recurrenceRule) VALUES
		('tpl-only', 'Pay rent', 0, 0, 0, 2, 1, x'0102')`)

	got, err := d.GetTask("Pay rent")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.UUID != "tpl-only" || !got.Repeating {
		t.Errorf("got %+v, want the template", got)
	}
}

// The LIKE path feeds the disambiguation picker, so the instance has to be the
// first thing offered there too.
func TestFindTasksByTitleOrdersTemplatesLast(t *testing.T) {
	d := newTestDB(t)
	seedTemplateAndInstance(t, d)

	got, err := d.FindTasksByTitle("Water")
	if err != nil {
		t.Fatalf("FindTasksByTitle: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want both rows", uuidsOf(got))
	}
	if got[0].UUID != "inst-water" || got[1].UUID != "tpl-water" {
		t.Errorf("order = %v, want [inst-water tpl-water]", uuidsOf(got))
	}
}

// A partial title matching only the pair resolves through the LIKE path's
// ambiguity branch; the candidates it reports are ordered the same way.
func TestGetTaskAmbiguousListsInstanceFirst(t *testing.T) {
	d := newTestDB(t)
	seedTemplateAndInstance(t, d)

	_, err := d.GetTask("ater plant")
	var ambig *AmbiguousTaskError
	if !errors.As(err, &ambig) {
		t.Fatalf("wrong error type: %T: %v", err, err)
	}
	if len(ambig.Matches) != 2 || ambig.Matches[0].UUID != "inst-water" {
		t.Errorf("matches = %v, want inst-water first", uuidsOf(ambig.Matches))
	}
}

// With no recurrence column the ordering expression is a constant, so title
// lookups fall back to plain index order rather than failing.
func TestTitleLookupsWithoutRecurrenceColumn(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	if _, err := sqlDB.Exec(`ALTER TABLE TMTask DROP COLUMN rt1_recurrenceRule`); err != nil {
		t.Fatalf("drop column: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, "index") VALUES
			('a', 'Water plants', 0, 0, 0, 1),
			('b', 'Water plants', 0, 0, 0, 2)`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	d := &DB{db: sqlDB}

	got, err := d.GetTask("Water plants")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.UUID != "a" {
		t.Errorf("got %q, want the lowest index", got.UUID)
	}

	matches, err := d.FindTasksByTitle("Water")
	if err != nil {
		t.Fatalf("FindTasksByTitle: %v", err)
	}
	if !sameSet(uuidsOf(matches), []string{"a", "b"}) {
		t.Errorf("got %v", uuidsOf(matches))
	}
}

// --- batched uuid lookups (issue #167) ---

func TestGetTasksByUUIDs(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)
	seedLookupHeadings(t, d)

	// A duplicate, an empty string, an unknown id and a heading all go in
	// alongside the real ones: callers pass a raw list built from a payload.
	got, err := d.GetTasksByUUIDs([]string{
		"t-today", "proj-1", "t-today", "", "nope", "head-phase",
	})
	if err != nil {
		t.Fatalf("GetTasksByUUIDs: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 (to-do + project): %v", len(got), keysOf(got))
	}
	if task := got["t-today"]; task == nil || task.Title != "Today task" {
		t.Errorf("t-today = %+v", task)
	}
	if task := got["proj-1"]; task == nil {
		t.Error("a project should resolve, as it does through GetTaskByUUID")
	}
	// Absent, not a nil entry: the caller distinguishes the two.
	if _, present := got["nope"]; present {
		t.Error("an unknown id should be absent from the map")
	}
	if _, present := got["head-phase"]; present {
		t.Error("a heading should be excluded, as it is from GetTaskByUUID (#146)")
	}
	if _, present := got[""]; present {
		t.Error("an empty id should never be queried")
	}
}

// The batch must agree with the single lookup field for field, or the import
// checks would see different tasks depending on which path ran.
func TestGetTasksByUUIDsMatchesGetTaskByUUID(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)
	seedLookupHeadings(t, d)

	ids := []string{"t-today", "t-inbox", "proj-1", "head-phase", "nope"}
	batch, err := d.GetTasksByUUIDs(ids)
	if err != nil {
		t.Fatalf("GetTasksByUUIDs: %v", err)
	}
	for _, id := range ids {
		single, err := d.GetTaskByUUID(id)
		if err != nil {
			t.Fatalf("GetTaskByUUID(%s): %v", id, err)
		}
		one, ok := batch[id]
		if single == nil {
			if ok {
				t.Errorf("%s: batch returned %+v where the single lookup found nothing", id, one)
			}
			continue
		}
		if !ok {
			t.Errorf("%s: batch found nothing where the single lookup found %+v", id, single)
			continue
		}
		if !reflect.DeepEqual(*one, *single) {
			t.Errorf("%s: batch = %+v, single = %+v", id, *one, *single)
		}
	}
}

// More ids than fit in one chunk, and not a whole multiple of one either, so
// the loop has to reassemble three chunks — two full and a short tail —
// without dropping a row at a boundary or double-counting one.
func TestGetTasksByUUIDsChunksLargeInput(t *testing.T) {
	d := newTestDB(t)

	const n = uuidChunkSize*2 + 7
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		uuid := fmt.Sprintf("bulk-%04d", i)
		ids = append(ids, uuid)
		mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed) VALUES (?, ?, 0, 0, 0)`,
			uuid, fmt.Sprintf("Bulk %d", i))
	}

	got, err := d.GetTasksByUUIDs(ids)
	if err != nil {
		t.Fatalf("GetTasksByUUIDs across %d ids: %v", n, err)
	}
	if len(got) != n {
		t.Fatalf("got %d rows, want %d — a chunk went missing", len(got), n)
	}
	for _, id := range ids {
		if got[id] == nil {
			t.Fatalf("%s missing from the batch result", id)
		}
	}
}

func TestGetTasksByUUIDsEmptyInput(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	for _, ids := range [][]string{nil, {}, {"", ""}} {
		got, err := d.GetTasksByUUIDs(ids)
		if err != nil {
			t.Fatalf("GetTasksByUUIDs(%v): %v", ids, err)
		}
		if len(got) != 0 {
			t.Errorf("GetTasksByUUIDs(%v) = %v, want empty", ids, keysOf(got))
		}
	}
}

func keysOf(m map[string]*model.Task) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
