package db

import (
	"errors"
	"testing"
	"time"

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

	// Tag the today task with urgent + home
	mustExec(t, d, `INSERT INTO TMTaskTag (tasks, tags) VALUES
		('t-today', 'tg-urgent'),
		('t-today', 'tg-home')`)

	// stopDate on the done task so logbook has something to order by
	done := model.TimeToCoreData(time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC))
	mustExec(t, d, `UPDATE TMTask SET stopDate = ? WHERE uuid = 't-done'`, done)
}

func TestValidView(t *testing.T) {
	known := []string{"today", "inbox", "upcoming", "anytime", "someday", "logbook", "trash", "deadlines", "project"}
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
		{"someday", []string{"t-someday"}},
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

// Completed items remain in Today until "Log Completed Now" bumps
// TMSettings.manualLogDate past their stopDate. Items completed on previous
// days are still visible (matching the Things app, which keeps them on screen
// regardless of todayIndexReferenceDate until the user explicitly logs).
func TestListTasksTodayCompletedItemFiltering(t *testing.T) {
	d := newTestDB(t)
	seedTasks(t, d)

	today := int64(model.ThingsDateFromTime(time.Now()))
	yesterday := today - (1 << 7) // ThingsDate encodes the day in bits 7..11
	stopToday := model.TimeToCoreData(time.Now().Add(-1 * time.Minute))
	stopYesterday := model.TimeToCoreData(time.Now().Add(-25 * time.Hour))

	// Completed today, not yet logged → should appear.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate,
		 todayIndexReferenceDate, stopDate, "index")
		VALUES ('t-just-done', 'Just done', 0, 3, 0, 1, 0, ?, ?, ?, 20)`,
		today, today, stopToday)

	// Completed yesterday but not yet logged → still appears in Today, even
	// though todayIndexReferenceDate is stale. This matches the Things app.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate,
		 todayIndexReferenceDate, stopDate, "index")
		VALUES ('t-done-yesterday', 'Done yesterday', 0, 3, 0, 1, 0, ?, ?, ?, 21)`,
		today, yesterday, stopYesterday)

	got, err := d.ListTasks("today", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks today: %v", err)
	}
	want := []string{"t-today", "t-evening", "t-just-done", "t-done-yesterday"}
	if !sameSet(want, uuidsOf(got)) {
		t.Fatalf("pre-log: expected %v, got %v", want, uuidsOf(got))
	}

	// Simulate "Log Completed Now": bump manualLogDate past both stopDates.
	future := model.TimeToCoreData(time.Now().Add(1 * time.Minute))
	mustExec(t, d, `INSERT INTO TMSettings (uuid, manualLogDate) VALUES ('s', ?)`, future)

	got, err = d.ListTasks("today", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks today: %v", err)
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
