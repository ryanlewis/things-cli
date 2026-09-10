package db

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
)

// seedTasks seeds one area, one project, a few tags, and tasks covering all
// views and statuses.
func seedTasks(t *testing.T, d *DB) {
	t.Helper()
	fx := &fixture{t: t, d: d}

	fx.area("area-work", "Work", 1)
	fx.project("proj-1", "Ship MVP", 1, inArea("area-work"))
	fx.tag("tg-urgent", "urgent", 1)
	fx.tag("tg-home", "home", 2)

	today := int64(model.ThingsDateFromTime(time.Now()))
	tomorrow := today + (1 << 7)
	done := model.TimeToUnix(time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC))

	fx.todo("t-today", "Today task", 10, anytimeOn(today), todayIndexRef(today), todayIndex(1))
	fx.todo("t-inbox", "Inbox task", 11, inbox(), notes("notes"), todayIndex(0))
	fx.todo("t-evening", "Evening task", 12, evening(today), todayIndex(0))
	fx.todo("t-upcoming", "Upcoming task", 22, somedayOn(tomorrow), todayIndex(0))
	fx.todo("t-anytime", "Anytime task", 13, anytime(), todayIndex(0))
	fx.todo("t-someday", "Someday task", 14, someday(), todayIndex(0))
	// A stopDate on the completed to-do so the logbook has something to order
	// by; the cancelled one deliberately has none, and is the only fixture
	// exercising the logbook filter's NULL guard.
	fx.todo("t-done", "Done task", 15, inbox(), completed(done), todayIndex(0))
	fx.todo("t-cancelled", "Cancelled", 16, inbox(), status(model.StatusCancelled), todayIndex(0))
	fx.todo("t-trashed", "Trashed task", 17, inbox(), trashed(), todayIndex(0))
	fx.todo("t-deadline", "Has deadline", 18, anytime(), deadline(tomorrow), todayIndex(0))
	fx.todo("t-in-proj", "Project task", 19, inbox(), inProject("proj-1"), inArea("area-work"), todayIndex(0))
	// Templates carry the recurrence rule; Things files them under Repeating
	// while their row otherwise looks exactly like a Someday to-do (issue #147).
	fx.todo("t-repeat", "Water plants", 20, someday(), inProject("proj-1"), inArea("area-work"), repeats())

	fx.tagged("t-today", "tg-urgent", "tg-home")
}

// CompletableView and CompletableViewNames used to read a map of their own and
// now read the view table, so pin both against the answer spelled out rather
// than against the table they are derived from (issue #240).
func TestCompletableViews(t *testing.T) {
	want := map[string]bool{"today": true, "anytime": true}
	for view := range views {
		if got := CompletableView(view); got != want[view] {
			t.Errorf("CompletableView(%q) = %v, want %v", view, got, want[view])
		}
	}
	if CompletableView("bogus") {
		t.Error("CompletableView(\"bogus\") = true, want false")
	}
	if got := CompletableViewNames(); !reflect.DeepEqual(got, []string{"anytime", "today"}) {
		t.Errorf("CompletableViewNames() = %v, want [anytime today]", got)
	}
}

// ProjectFilterableView reads the view table. Pin it against the answer
// spelled out here rather than derived from the table, so a spec that loses
// the flag fails rather than redefining the expectation. It is the one flag
// stated in the negative: everything is filterable but someday, an unknown
// name included.
func TestProjectFilterableView(t *testing.T) {
	denied := map[string]bool{"someday": true}
	for view := range views {
		if got := ProjectFilterableView(view); got != !denied[view] {
			t.Errorf("ProjectFilterableView(%q) = %v, want %v", view, got, !denied[view])
		}
	}
	if !ProjectFilterableView("bogus") {
		t.Error("ProjectFilterableView(\"bogus\") = false, want true")
	}
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
		// The Logbook carries cancelled beside completed (issue #210).
		{"logbook", []string{"t-done", "t-cancelled"}},
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
	// Now, not "a minute ago": the calendar day decides membership since issue
	// #230, and a minute before midnight falls on the previous day. 25 hours
	// back is safely not today whatever the hour.
	stopToday := model.TimeToUnix(time.Now())
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

	// IncludeCompleted (pre-log): the items closed today reappear. The one
	// closed yesterday does not — Things filed it into the Logbook when the
	// day rolled over, whatever manualLogDate says (issue #230).
	got, err = d.ListTasks("today", TaskFilter{IncludeCompleted: true})
	if err != nil {
		t.Fatalf("ListTasks today --include-completed: %v", err)
	}
	want := []string{"t-today", "t-evening", "t-just-done", "t-cancelled-today"}
	if !sameSet(want, uuidsOf(got)) {
		t.Fatalf("pre-log: expected %v, got %v", want, uuidsOf(got))
	}

	// And the Logbook is the complement: it holds the one closed yesterday and
	// neither of the two closed today. t-cancelled carries no stopDate at all,
	// so it also proves the NULL guard keeps such a row in the Logbook rather
	// than letting the negated clause drop it out of both lists.
	logged, err := d.ListTasks("logbook", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks logbook: %v", err)
	}
	wantLogged := []string{"t-done", "t-cancelled", "t-done-yesterday"}
	if !sameSet(wantLogged, uuidsOf(logged)) {
		t.Fatalf("pre-log logbook: expected %v, got %v", wantLogged, uuidsOf(logged))
	}

	// Simulate "Log Completed Now": bump manualLogDate past every stopDate.
	// That is the second half of the rule — it files the day's closed items
	// straight away instead of waiting for midnight.
	future := model.TimeToUnix(time.Now().Add(1 * time.Minute))
	mustExec(t, d, `INSERT INTO TMSettings (uuid, manualLogDate) VALUES ('s', ?)`, future)

	got, err = d.ListTasks("today", TaskFilter{IncludeCompleted: true})
	if err != nil {
		t.Fatalf("ListTasks today --include-completed: %v", err)
	}
	if !sameSet([]string{"t-today", "t-evening"}, uuidsOf(got)) {
		t.Fatalf("post-log: expected {t-today, t-evening}, got %v", uuidsOf(got))
	}

	// The two rows that left Today arrive in the Logbook in the same move.
	logged, err = d.ListTasks("logbook", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks logbook: %v", err)
	}
	wantLogged = []string{"t-done", "t-cancelled", "t-done-yesterday", "t-just-done", "t-cancelled-today"}
	if !sameSet(wantLogged, uuidsOf(logged)) {
		t.Fatalf("post-log logbook: expected %v, got %v", wantLogged, uuidsOf(logged))
	}
}

// The two lists partition the closed items: whatever the day and whatever
// manualLogDate says, a closed item is in exactly one of them, never both and
// never neither (issue #230). The today view only carries items scheduled for
// today, so the partition is asserted over that set.
func TestTodayAndLogbookPartitionClosedItems(t *testing.T) {
	today := int64(model.ThingsDateFromTime(time.Now()))

	cases := []struct {
		name         string
		stopDate     float64
		manualLogSet bool
		// wantUnderToday is spelled out per case rather than derived, so the
		// test states the rule instead of restating the implementation.
		wantUnderToday bool
	}{
		{"closed today, not logged", model.TimeToUnix(time.Now()), false, true},
		{"closed today, logged", model.TimeToUnix(time.Now()), true, false},
		{"closed yesterday, not logged", model.TimeToUnix(time.Now().Add(-25 * time.Hour)), false, false},
		{"closed yesterday, logged", model.TimeToUnix(time.Now().Add(-25 * time.Hour)), true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDB(t)
			mustExec(t, d, `INSERT INTO TMTask
				(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, "index")
				VALUES ('t-closed', 'Closed', 0, 3, 0, 1, 0, ?, ?, 1)`, today, tc.stopDate)
			if tc.manualLogSet {
				future := model.TimeToUnix(time.Now().Add(1 * time.Minute))
				mustExec(t, d, `INSERT INTO TMSettings (uuid, manualLogDate) VALUES ('s', ?)`, future)
			}

			inToday, err := d.ListTasks("today", TaskFilter{IncludeCompleted: true})
			if err != nil {
				t.Fatal(err)
			}
			inLogbook, err := d.ListTasks("logbook", TaskFilter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(inToday)+len(inLogbook) != 1 {
				t.Errorf("today=%v logbook=%v: want the row in exactly one list", uuidsOf(inToday), uuidsOf(inLogbook))
			}
			// It stays under Today only while both conditions hold.
			if got := len(inToday) == 1; got != tc.wantUnderToday {
				t.Errorf("under Today = %v, want %v", got, tc.wantUnderToday)
			}
		})
	}
}

// A closed item the today view never carries — completed out of the Inbox,
// out of Upcoming — belongs in the Logbook the moment it is closed, whatever
// calendar day that is, because no list is still showing it. Excluding "closed
// today" from the Logbook outright would leave those rows in no list at all
// (issue #230).
//
// Anytime is the case that is not like the other two. Since issue #238 the
// app's Anytime goes on showing a row closed out of it, so the Logbook
// withholds it for the rest of the day and `anytime --include-completed` has
// it instead. Each case therefore names the list that should be holding the
// row, and every case asserts it is in exactly one place.
func TestClosedTodayOutsideTodayLandsInOneList(t *testing.T) {
	future := int64(model.ThingsDateFromTime(time.Now().AddDate(0, 0, 3)))
	stopNow := model.TimeToUnix(time.Now())

	cases := []struct {
		uuid        string
		start       int
		startBucket int
		startDate   any
		heldBy      string // the view that should have it, "" for the logbook
	}{
		{"t-closed-inbox", 0, 0, nil, ""},          // closed straight out of the Inbox
		{"t-closed-anytime", 1, 0, nil, "anytime"}, // closed out of Anytime — no startDate
		{"t-closed-upcoming", 2, 0, future, ""},    // closed ahead of its Upcoming date
	}

	for _, tc := range cases {
		t.Run(tc.uuid, func(t *testing.T) {
			d := newTestDB(t)
			mustExec(t, d, `INSERT INTO TMTask
				(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, "index")
				VALUES (?, 'Closed', 0, 3, 0, ?, ?, ?, ?, 1)`,
				tc.uuid, tc.start, tc.startBucket, tc.startDate, stopNow)

			logged, err := d.ListTasks("logbook", TaskFilter{})
			if err != nil {
				t.Fatal(err)
			}
			wantLogged := tc.heldBy == ""
			if gotLogged := len(logged) == 1; gotLogged != wantLogged {
				t.Errorf("logbook = %v, want held there: %v", uuidsOf(logged), wantLogged)
			}

			// today never has it: none of these rows is scheduled for today.
			inToday, err := d.ListTasks("today", TaskFilter{IncludeCompleted: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(inToday) != 0 {
				t.Errorf("today = %v, want empty", uuidsOf(inToday))
			}

			inAnytime, err := d.ListTasks("anytime", TaskFilter{IncludeCompleted: true})
			if err != nil {
				t.Fatal(err)
			}
			wantAnytime := tc.heldBy == "anytime"
			if gotAnytime := len(inAnytime) == 1; gotAnytime != wantAnytime {
				t.Errorf("anytime --include-completed = %v, want held there: %v", uuidsOf(inAnytime), wantAnytime)
			}

			// Exactly one list, never both and never neither.
			if n := len(logged) + len(inAnytime); n != 1 {
				t.Errorf("row is in %d lists, want exactly 1", n)
			}
		})
	}
}

// Trashing a project leaves its children at trashed = 0, and every view but
// trash and logbook drops them, so the Logbook is the only list that can hold
// a to-do closed today under a trashed project (issue #230).
func TestClosedTodayUnderTrashedProjectIsReachable(t *testing.T) {
	d := newTestDB(t)
	today := int64(model.ThingsDateFromTime(time.Now()))
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, "index")
		VALUES ('proj-binned', 'Binned', 1, 0, 1, 1)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, project, "index")
		VALUES ('t-closed', 'Closed', 0, 3, 0, 1, 0, ?, ?, 'proj-binned', 2)`,
		today, model.TimeToUnix(time.Now()))

	inToday, err := d.ListTasks("today", TaskFilter{IncludeCompleted: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(inToday) != 0 {
		t.Errorf("today = %v, want empty — the parent is trashed", uuidsOf(inToday))
	}
	// It is not in the logbook either: issue #229 folds a trashed project's
	// to-dos into the project's Trash row. What issue #230 needs is that the
	// row has somewhere to be, and naming the project is where.
	logged, err := d.ListTasks("logbook", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logged) != 0 {
		t.Errorf("logbook = %v, want empty — the parent is trashed", uuidsOf(logged))
	}
	contents, err := d.ListTasks("project", TaskFilter{Project: "proj-binned"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet([]string{"t-closed"}, uuidsOf(contents)) {
		t.Errorf("--project proj-binned = %v, want {t-closed}", uuidsOf(contents))
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

	// t-in-proj inherits area-work via its project (pa.uuid join), and proj-1
	// is a row in its own right since issue #222.
	tasks, err := d.ListTasks("project", TaskFilter{Area: "area-work"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(tasks), []string{"proj-1", "t-in-proj"}) {
		t.Errorf("area filter: got %+v, want [proj-1 t-in-proj]", uuidsOf(tasks))
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

// DateFilterableView reads the view table. Pin it against the answer spelled
// out here rather than derived from the table, so a spec that loses the flag
// fails rather than redefining the expectation. Walking the table rather than
// a hand-written list of names also means a view added later is covered: set
// the flag on it and this fails until the answer here says so too.
func TestDateFilterableView(t *testing.T) {
	// someday is denied because its view predicate requires startDate IS NULL —
	// a startDate range filter could never match anything. inbox has no
	// startDate, trash is trashed-only, logbook has no meaningful startDate
	// filter, and repeating lists templates rather than dated rows.
	allowed := map[string]bool{
		"today": true, "upcoming": true, "anytime": true,
		"deadlines": true, "project": true,
	}
	for view := range views {
		if got := DateFilterableView(view); got != allowed[view] {
			t.Errorf("DateFilterableView(%q) = %v, want %v", view, got, allowed[view])
		}
	}
	if DateFilterableView("bogus") {
		t.Error("DateFilterableView(\"bogus\") = true, want false")
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

// A lookup matches its text literally. `_` used to stand for any character and
// `%` for any run of them, so a reference could resolve to a task the user did
// not name — and GetTask acts on a lookup that matches exactly one row, which
// put `complete` on the wrong task (issue #267).
func TestLookupMatchesTitlesLiterally(t *testing.T) {
	d := newTestDB(t)
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, "index") VALUES
		('t-colon', '20:30 review', 0, 0, 0, 1),
		('t-pct',   'Cut 50% of the scope', 0, 0, 0, 2),
		('t-fifty', 'Fifty50 something', 0, 0, 0, 3)`)

	// The dangerous shape: nothing is called '20_30 review', so the reference
	// has to miss. GetTask acts on a lookup that matches exactly one row, so
	// while `_` stood for any character this resolved silently to
	// '20:30 review' and a write landed on it.
	if got, err := d.GetTask("20_30 review"); err == nil {
		t.Errorf("GetTask(\"20_30 review\") resolved to %q; no task is called that", got.UUID)
	} else {
		var notFound *TaskNotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("GetTask(\"20_30 review\"): got %v, want a not-found error", err)
		}
	}

	// The underscore still finds its own task once one exists. The reference
	// is a substring, so the equality branch GetTask tries first misses and
	// the escaped LIKE is what resolves it — unescaped, the colon title
	// matched the same pattern and the lookup was ambiguous.
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, "index") VALUES
		('t-under', '20_30 review', 0, 0, 0, 4)`)
	got, err := d.GetTask("20_30 rev")
	if err != nil {
		t.Fatalf("GetTask(\"20_30 rev\"): %v", err)
	}
	if got.UUID != "t-under" {
		t.Errorf("got %q, want t-under", got.UUID)
	}

	// A percent in a lookup is a character to find, so a title that merely has
	// a 5 and a 0 in it is not a match.
	matches, err := d.FindTasksByTitle("50%")
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(matches), []string{"t-pct"}) {
		t.Errorf("FindTasksByTitle(\"50%%\"): got %v, want [t-pct]", uuidsOf(matches))
	}

	// Substring matching itself is unchanged: the wrapping wildcards are the
	// CLI's, not the caller's.
	matches, err = d.FindTasksByTitle("review")
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(matches), []string{"t-under", "t-colon"}) {
		t.Errorf("FindTasksByTitle(\"review\"): got %v, want both reviews", uuidsOf(matches))
	}

	// Still case-insensitive, as it has always been.
	matches, err = d.FindTasksByTitle("REVIEW")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Errorf("uppercase lookup: got %v, want both reviews", uuidsOf(matches))
	}
}

// `things search` matches its text literally too. `search '50%'` returned
// every title holding a 5 followed by a 0, because the value went in as a
// pattern (issue #267).
func TestSearchMatchesLiterally(t *testing.T) {
	d := newTestDB(t)
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, notes, type, status, trashed, "index") VALUES
		('s-pct',   'Cut 50% of the scope', '',            0, 0, 0, 1),
		('s-fifty', 'Fifty50 something',    '',            0, 0, 0, 2),
		('s-note',  'Plain title',          'about 50% done', 0, 0, 0, 3),
		('s-under', 'a_b',                  '',            0, 0, 0, 4),
		('s-axb',   'axb',                  '',            0, 0, 0, 5)`)

	pct, err := d.SearchTasks("50%")
	if err != nil {
		t.Fatal(err)
	}
	// The note match is wanted — search covers notes — but s-fifty is not.
	if !sameSet(uuidsOf(pct), []string{"s-pct", "s-note"}) {
		t.Errorf("search 50%%: got %v, want [s-pct s-note]", uuidsOf(pct))
	}

	under, err := d.SearchTasks("a_b")
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(under), []string{"s-under"}) {
		t.Errorf("search a_b: got %v, want [s-under]", uuidsOf(under))
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

// Tasks filed under a project heading have no t.project of their own; the
// project filter has to reach it through the heading row (issue #139).
func TestListTasksProjectFilterIncludesHeadingTasks(t *testing.T) {
	d, fx := newFixture(t)
	today := int64(model.ThingsDateFromTime(time.Now()))
	fx.project("proj-h", "Ship v2", 1)
	fx.heading("head-1", "Phase one", 2, inProject("proj-h"))
	fx.todo("t-direct", "Direct task", 3, anytimeOn(today), inProject("proj-h"))
	fx.todo("t-nested", "Nested task", 4, anytime(), underHeading("head-1"))
	// Outside the project, so the filter has to leave it out.
	fx.todo("t-loose", "Loose task", 5, anytime())

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
	d, fx := newFixture(t)
	today := int64(model.ThingsDateFromTime(time.Now()))
	fx.area("area-launch", "Launch", 1)
	fx.project("proj-h", "Ship v2", 1, inArea("area-launch"))
	fx.heading("head-1", "Phase one", 2, inProject("proj-h"))
	fx.todo("t-direct", "Direct task", 3, anytimeOn(today), inProject("proj-h"))
	fx.todo("t-nested", "Nested task", 4, anytime(), underHeading("head-1"))
	// Filed in no area at all, so the filter has to leave it out.
	fx.todo("t-loose", "Loose task", 5, anytime())

	got, err := d.ListTasks("project", TaskFilter{Area: "Launch"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	// proj-h itself is in the area too, and lists as a row (issue #222).
	if !sameSet(uuidsOf(got), []string{"proj-h", "t-direct", "t-nested"}) {
		t.Errorf("area filter: got %v, want [proj-h t-direct t-nested]", uuidsOf(got))
	}
}

// A heading-nested task reports its project in output, so it can't be mistaken
// for a standalone task (issue #139).
func TestHeadingTaskCarriesProject(t *testing.T) {
	d, fx := newFixture(t)
	fx.area("area-launch", "Launch", 1)
	fx.project("proj-h", "Ship v2", 1, inArea("area-launch"))
	fx.heading("head-1", "Phase one", 2, inProject("proj-h"))
	fx.todo("t-nested", "Nested task", 4, anytime(), underHeading("head-1"))

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
	d, fx := newFixture(t)
	todayDate := int64(model.ThingsDateFromTime(time.Now()))
	fx.project("proj-h", "Ship v2", 1)
	fx.heading("head-1", "Phase one", 2, inProject("proj-h"))
	// One of the two is scheduled for today; the today view sees only that one.
	fx.todo("t-direct", "Direct task", 3, anytimeOn(todayDate), inProject("proj-h"))
	fx.todo("t-nested", "Nested task", 4, anytime(), underHeading("head-1"))

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
	d, fx := newFixture(t)
	today := int64(model.ThingsDateFromTime(time.Now()))
	fx.tag("tg-ship", "ship", 1)
	fx.project("proj-h", "Ship v2", 1)
	fx.heading("head-1", "Phase one", 2, inProject("proj-h"))
	fx.todo("t-direct", "Direct task", 3, anytimeOn(today), inProject("proj-h"))
	fx.todo("t-nested", "Nested task", 4, anytime(), underHeading("head-1"))
	fx.todo("t-loose", "Loose task", 5, anytime())
	fx.tagged("t-nested", "tg-ship")

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

// A trashed project is one row in Trash, not a row plus its contents: the app
// folds its to-dos into the project's row and lists none of them separately,
// in trash or in logbook (issue #229). Naming the project is what returns
// them, so nothing here is swallowed — it is reached a different way.
func TestTrashAndLogbookFoldTrashedProjectChildren(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, "index") VALUES
		('proj-gone', 'Trashed project', 1, 0, 1, 1)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, project, "index") VALUES
		('t-binned', 'Trashed child',   0, 0, 1, 1, 0, 'proj-gone', 1),
		('t-logged', 'Completed child', 0, 3, 0, 1, 0, 'proj-gone', 2)`)

	// trash carries the trashed project row itself (issue #212); logbook
	// carries nothing here, because a trashed row is not logged and the
	// project's children are folded into its Trash row.
	for _, tc := range []struct {
		view string
		want []string
	}{
		{"trash", []string{"proj-gone"}},
		{"logbook", nil},
	} {
		got, err := d.ListTasks(tc.view, TaskFilter{})
		if err != nil {
			t.Fatalf("ListTasks(%q): %v", tc.view, err)
		}
		if !sameSet(uuidsOf(got), tc.want) {
			t.Errorf("view %q: got %v, want %v", tc.view, uuidsOf(got), tc.want)
		}
	}

	// The folded child is reachable by naming the project. The trashed child
	// is not, and matches the app: asking Things for a trashed project's
	// contents returns nothing for a row already in the Trash on its own
	// account.
	contents, err := d.ListTasks("project", TaskFilter{Project: "proj-gone"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(contents), []string{"t-logged"}) {
		t.Errorf("--project proj-gone: got %v, want [t-logged]", uuidsOf(contents))
	}
}

// The app folds a closed project's to-dos into the project's own Logbook row
// and lists none of them separately, so the CLI does too (issue #229). Trash
// is not the same case: a to-do thrown away out of a finished project is in
// Trash on its own account, and the project is not there to fold it into.
func TestLogbookFoldsClosedProjectChildren(t *testing.T) {
	d, fx := newFixture(t)

	// A closed project and a trashed one, each holding children in several
	// states, plus an open project as the control (issue #229).
	stop := model.TimeToUnix(time.Now().Add(-26 * time.Hour))
	fx.project("proj-done", "Finished", 1, completed(stop))
	fx.project("proj-binned", "Binned", 2, trashed())
	fx.project("proj-open", "Live", 3)

	fx.todo("done-completed", "Shipped", 4, anytime(), inProject("proj-done"), completed(stop))
	fx.todo("done-cancelled", "Dropped", 5, anytime(), inProject("proj-done"), cancelled(stop))
	fx.todo("done-trashed", "Binned", 6, anytime(), inProject("proj-done"), trashed())
	fx.todo("done-open", "Left over", 7, anytime(), inProject("proj-done"))
	fx.todo("binned-logged", "Logged", 8, anytime(), inProject("proj-binned"), completed(stop))
	fx.todo("open-todo", "To do", 9, anytime(), inProject("proj-open"))

	logged, err := d.ListTasks("logbook", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	// Only the closed project itself. Its completed and cancelled children are
	// folded into it, and binned-logged is folded into the trashed project.
	if !sameSet(uuidsOf(logged), []string{"proj-done"}) {
		t.Errorf("logbook: got %v, want [proj-done]", uuidsOf(logged))
	}

	binned, err := d.ListTasks("trash", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	// done-trashed keeps its Trash row even though its project is closed.
	if !sameSet(uuidsOf(binned), []string{"proj-binned", "done-trashed"}) {
		t.Errorf("trash: got %v, want [proj-binned done-trashed]", uuidsOf(binned))
	}
}

// Naming a closed or trashed project returns its contents whatever their
// status — otherwise the rows the Logbook and Trash now fold away would be
// reachable nowhere. It is what the app answers for `to dos of project id`:
// the closed and cancelled children, and not the trashed one, which is in the
// Trash on its own account (issue #229).
func TestProjectFilterReturnsClosedProjectContents(t *testing.T) {
	d, fx := newFixture(t)

	// A closed project and a trashed one, each holding children in several
	// states, plus an open project as the control (issue #229).
	stop := model.TimeToUnix(time.Now().Add(-26 * time.Hour))
	fx.project("proj-done", "Finished", 1, completed(stop))
	fx.project("proj-binned", "Binned", 2, trashed())
	fx.project("proj-open", "Live", 3)

	fx.todo("done-completed", "Shipped", 4, anytime(), inProject("proj-done"), completed(stop))
	fx.todo("done-cancelled", "Dropped", 5, anytime(), inProject("proj-done"), cancelled(stop))
	fx.todo("done-trashed", "Binned", 6, anytime(), inProject("proj-done"), trashed())
	fx.todo("done-open", "Left over", 7, anytime(), inProject("proj-done"))
	fx.todo("binned-logged", "Logged", 8, anytime(), inProject("proj-binned"), completed(stop))
	fx.todo("open-todo", "To do", 9, anytime(), inProject("proj-open"))

	cases := []struct {
		name    string
		project string
		want    []string
	}{
		{"closed project", "proj-done", []string{"done-completed", "done-cancelled", "done-open"}},
		{"closed project by title", "Finished", []string{"done-completed", "done-cancelled", "done-open"}},
		{"trashed project", "proj-binned", []string{"binned-logged"}},
		// The control: an open project is unchanged, still open rows only.
		{"open project", "proj-open", []string{"open-todo"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := d.ListTasks("project", TaskFilter{Project: tc.project})
			if err != nil {
				t.Fatal(err)
			}
			if !sameSet(uuidsOf(got), tc.want) {
				t.Errorf("--project %s: got %v, want %v", tc.project, uuidsOf(got), tc.want)
			}
		})
	}
}

// The widening is scoped to a named project. A bare --area sweep is still the
// open set: it must not start returning the closed contents of every closed
// project in the area.
func TestAreaFilterDoesNotWidenToClosedContents(t *testing.T) {
	d, fx := newFixture(t)

	// A closed project and an open one in the same area, each holding
	// children in several states (issue #229).
	stop := model.TimeToUnix(time.Now().Add(-26 * time.Hour))
	fx.area("ar", "Work", 1)
	fx.project("proj-done", "Finished", 1, inArea("ar"), completed(stop))
	fx.project("proj-open", "Live", 3, inArea("ar"))

	fx.todo("done-completed", "Shipped", 4, anytime(), inProject("proj-done"), completed(stop))
	fx.todo("done-cancelled", "Dropped", 5, anytime(), inProject("proj-done"), cancelled(stop))
	fx.todo("done-trashed", "Binned", 6, anytime(), inProject("proj-done"), trashed())
	fx.todo("done-open", "Left over", 7, anytime(), inProject("proj-done"))
	fx.todo("open-todo", "To do", 9, anytime(), inProject("proj-open"))

	got, err := d.ListTasks("project", TaskFilter{Area: "ar"})
	if err != nil {
		t.Fatal(err)
	}
	// The open project, its open to-do, and the closed project's one open
	// child. Nothing closed, and no row of the closed project's contents.
	want := []string{"proj-open", "open-todo", "done-open"}
	if !sameSet(uuidsOf(got), want) {
		t.Errorf("--area ar: got %v, want %v", uuidsOf(got), want)
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
	// The two project rows have no parent project of their own, so they sort
	// on COALESCE(p."index", 0) = 0, into the same key group as the area's
	// unparented rows and ahead of every project's to-dos; each project's
	// to-dos then follow in a contiguous block. Within that leading group the
	// next key is t.start, which these fixture projects leave NULL, so they
	// come first — a project row with a start of its own would order against
	// the area's loose to-dos by that key instead. Which primary key mixed
	// rows should take is issue #217.
	want := []string{"proj-a", "proj-b", "a1", "a2", "b1", "b2"}
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
//
// Two heading shapes matter to a lookup: "head-phase", with a title of its
// own, and "head-dupe", whose title collides exactly with an open to-do — a
// lookup that failed to filter headings out would either return the wrong row
// or report the pair as ambiguous. Each test below seeds the one it needs.

func TestGetTaskByUUIDExcludesHeading(t *testing.T) {
	d, fx := newFixture(t)
	seedTasks(t, d)
	fx.heading("head-phase", "Phase one", 20, notes("heading notes"), inProject("proj-1"))

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
	d, fx := newFixture(t)
	seedTasks(t, d)
	fx.heading("head-dupe", "Inbox task", 21, inProject("proj-1"))

	got, err := d.GetTask("Inbox task")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.UUID != "t-inbox" {
		t.Errorf("got %q, want t-inbox", got.UUID)
	}
}

func TestGetTaskLikeMatchSkipsHeading(t *testing.T) {
	d, fx := newFixture(t)
	seedTasks(t, d)
	fx.heading("head-phase", "Phase one", 20, notes("heading notes"), inProject("proj-1"))
	fx.heading("head-dupe", "Inbox task", 21, inProject("proj-1"))

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
	d, fx := newFixture(t)
	seedTasks(t, d)
	fx.heading("head-phase", "Phase one", 20, notes("heading notes"), inProject("proj-1"))
	fx.heading("head-dupe", "Inbox task", 21, inProject("proj-1"))

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
	d, fx := newFixture(t)
	seedTasks(t, d)
	fx.heading("head-phase", "Phase one", 20, notes("heading notes"), inProject("proj-1"))
	fx.heading("head-dupe", "Inbox task", 21, inProject("proj-1"))

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
//
// Things stores a repeating to-do as two rows sharing a title: the template
// carrying the recurrence rule, deferred with no start date, and the instance
// generated from it, scheduled and ruleless. The template takes the lower
// "index" in each test below, so index order alone would pick it — the
// ordering under test is what puts the instance first.

// `things complete "Water plants"` must land on the instance: the template
// refuses writes (issue #143), so resolving to it strands the user.
func TestGetTaskExactTitlePrefersInstance(t *testing.T) {
	d, fx := newFixture(t)
	today := int64(model.ThingsDateFromTime(time.Now()))
	fx.todo("tpl-water", "Water plants", 1, someday(), repeats())
	fx.todo("inst-water", "Water plants", 2, anytimeOn(today))

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
	d, fx := newFixture(t)
	today := int64(model.ThingsDateFromTime(time.Now()))
	fx.todo("tpl-water", "Water plants", 1, someday(), repeats())
	fx.todo("inst-water", "Water plants", 2, anytimeOn(today))

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
	d, fx := newFixture(t)
	today := int64(model.ThingsDateFromTime(time.Now()))
	fx.todo("tpl-water", "Water plants", 1, someday(), repeats())
	fx.todo("inst-water", "Water plants", 2, anytimeOn(today))

	_, err := d.GetTask("ater plant")
	var ambig *AmbiguousTaskError
	if !errors.As(err, &ambig) {
		t.Fatalf("wrong error type: %T: %v", err, err)
	}
	if len(ambig.Matches) != 2 || ambig.Matches[0].UUID != "inst-water" {
		t.Errorf("matches = %v, want inst-water first", uuidsOf(ambig.Matches))
	}
}

// --- exact titles shared by more than one row (issue #194) ---

// A project and a to-do can carry the same title. Neither is the obvious
// target, so the lookup reports both rather than handing back whichever sorted
// first: picking silently was what sent `things project edit <title>` to the
// to-do and then advised a retry that edited it.
func TestGetTaskExactTitleAmbiguousAcrossKinds(t *testing.T) {
	d, fx := newFixture(t)
	fx.project("proj-chores", "Chores", 1)
	fx.todo("todo-chores", "Chores", 2, anytime())

	_, err := d.GetTask("Chores")
	var ambig *AmbiguousTaskError
	if !errors.As(err, &ambig) {
		t.Fatalf("wrong error type: %T: %v", err, err)
	}
	if !sameSet(uuidsOf(ambig.Matches), []string{"proj-chores", "todo-chores"}) {
		t.Fatalf("matches = %v, want both rows", uuidsOf(ambig.Matches))
	}
	byUUID := map[string]model.TaskType{}
	for _, m := range ambig.Matches {
		byUUID[m.UUID] = m.Type
	}
	// The kind travels with each candidate: it is what tells the caller which
	// of `things edit` and `things project edit` to retry with.
	if byUUID["proj-chores"] != model.TypeProject {
		t.Errorf("proj-chores type = %v", byUUID["proj-chores"])
	}
	if byUUID["todo-chores"] != model.TypeTask {
		t.Errorf("todo-chores type = %v", byUUID["todo-chores"])
	}
}

// Two to-dos of the same kind are no more decidable than two of different
// kinds, and the index order that used to settle it is not a preference the
// user expressed.
func TestGetTaskExactTitleAmbiguousBetweenTodos(t *testing.T) {
	d, fx := newFixture(t)
	fx.todo("todo-a", "Call the vet", 1, anytime())
	fx.todo("todo-b", "Call the vet", 2, anytime())

	_, err := d.GetTask("Call the vet")
	var ambig *AmbiguousTaskError
	if !errors.As(err, &ambig) {
		t.Fatalf("wrong error type: %T: %v", err, err)
	}
	if !sameSet(uuidsOf(ambig.Matches), []string{"todo-a", "todo-b"}) {
		t.Errorf("matches = %v", uuidsOf(ambig.Matches))
	}
}

// The template drops out of a larger set the same way it drops out of a pair:
// the ambiguity check runs on what survives preferInstances, so the candidates
// reported are the instances alone rather than the template as well.
func TestGetTaskExactTitleDropsTemplateFromCandidates(t *testing.T) {
	d, fx := newFixture(t)
	today := int64(model.ThingsDateFromTime(time.Now()))
	fx.todo("tpl-water", "Water plants", 1, someday(), repeats())
	fx.todo("inst-water", "Water plants", 2, anytimeOn(today))
	fx.todo("inst-water-2", "Water plants", 3, anytimeOn(today))

	// Two instances behind one template: the template drops out, and the
	// remaining pair is reported.
	_, err := d.GetTask("Water plants")
	var ambig *AmbiguousTaskError
	if !errors.As(err, &ambig) {
		t.Fatalf("wrong error type: %T: %v", err, err)
	}
	if !sameSet(uuidsOf(ambig.Matches), []string{"inst-water", "inst-water-2"}) {
		t.Errorf("matches = %v, want the two instances", uuidsOf(ambig.Matches))
	}
}

// A template only stands in for an instance of its own kind. A repeating
// project and an ordinary to-do sharing a title are two candidates, not one:
// dropping the project template because the to-do is not repeating would send
// `things project edit <title>` to the to-do again (issue #194).
func TestGetTaskExactTitleKeepsTemplateOfAnotherKind(t *testing.T) {
	d, fx := newFixture(t)
	fx.project("tpl-chores", "Chores", 1, repeats())
	fx.todo("todo-chores", "Chores", 2, anytime())

	_, err := d.GetTask("Chores")
	var ambig *AmbiguousTaskError
	if !errors.As(err, &ambig) {
		t.Fatalf("wrong error type: %T: %v", err, err)
	}
	if !sameSet(uuidsOf(ambig.Matches), []string{"tpl-chores", "todo-chores"}) {
		t.Errorf("matches = %v, want both rows", uuidsOf(ambig.Matches))
	}
}

// A closed or trashed row carrying the title is not a candidate, so it cannot
// turn a single open match into an ambiguity.
func TestGetTaskExactTitleIgnoresClosedAndTrashed(t *testing.T) {
	d, fx := newFixture(t)
	fx.todo("open-one", "File taxes", 1, anytime())
	fx.todo("done-one", "File taxes", 2, completed(1_600_000_000))
	fx.todo("gone-one", "File taxes", 3, anytime(), trashed())

	got, err := d.GetTask("File taxes")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.UUID != "open-one" {
		t.Errorf("got %q, want open-one", got.UUID)
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
			('b', 'Water plants', 0, 0, 0, 2),
			('c', 'Water the tree', 0, 0, 0, 3)`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	d := &DB{db: sqlDB}

	got, err := d.GetTask("Water the tree")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.UUID != "c" {
		t.Errorf("got %q, want c", got.UUID)
	}

	// With no recurrence column no row reads as a template, so neither of the
	// two same-titled rows can be preferred over the other and both come back
	// as candidates (issue #194).
	var ambig *AmbiguousTaskError
	if _, err := d.GetTask("Water plants"); !errors.As(err, &ambig) {
		t.Fatalf("GetTask: %v, want AmbiguousTaskError", err)
	}
	if !sameSet(uuidsOf(ambig.Matches), []string{"a", "b"}) {
		t.Errorf("got %v", uuidsOf(ambig.Matches))
	}

	matches, err := d.FindTasksByTitle("Water plants")
	if err != nil {
		t.Fatalf("FindTasksByTitle: %v", err)
	}
	if !sameSet(uuidsOf(matches), []string{"a", "b"}) {
		t.Errorf("got %v", uuidsOf(matches))
	}
}

// --- batched uuid lookups (issue #167) ---

func TestGetTasksByUUIDs(t *testing.T) {
	d, fx := newFixture(t)
	seedTasks(t, d)
	fx.heading("head-phase", "Phase one", 20, notes("heading notes"), inProject("proj-1"))

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
	d, fx := newFixture(t)
	seedTasks(t, d)
	fx.heading("head-phase", "Phase one", 20, notes("heading notes"), inProject("proj-1"))

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

// Things shows a project scheduled for Today in its Today list, and the same
// for Upcoming and Anytime; the CLI's views used to be pinned to to-dos, so a
// scheduled project was invisible (issue #201).
func TestListTasksViewsIncludeProjects(t *testing.T) {
	d, fx := newFixture(t)

	today := int64(model.ThingsDateFromTime(time.Now()))
	tomorrow := today + (1 << 7)

	fx.area("area-work", "Work", 1)
	fx.project("proj-today", "Runbook audit", 5, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(2005))
	fx.project("proj-upcoming", "Q4 planning", 6, somedayOn(tomorrow), inArea("area-work"), todayIndex(0))
	fx.project("proj-anytime", "Backlog", 7, anytime(), inArea("area-work"), todayIndex(0))

	// To-dos for company: one inside the scheduled project, one loose in the
	// same area, one top-level.
	fx.todo("todo-in-proj", "Read logs", 12, anytimeOn(today), todayIndexRef(today), inProject("proj-today"), todayIndex(3000))
	fx.todo("todo-in-area", "Call bank", 13, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(1))
	fx.todo("todo-loose", "Buy milk", 14, anytimeOn(today), todayIndexRef(today), todayIndex(7))

	cases := []struct {
		view string
		want []string
	}{
		{"today", []string{"proj-today", "todo-in-proj", "todo-in-area", "todo-loose"}},
		{"upcoming", []string{"proj-upcoming"}},
		// anytime is the exception: every active project is trivially
		// "anytime", and the app shows them as group headers over their
		// to-dos rather than as rows (issue #217).
		{"anytime", []string{"todo-in-proj", "todo-in-area", "todo-loose"}},
	}

	for _, tc := range cases {
		t.Run(tc.view, func(t *testing.T) {
			got, err := d.ListTasks(tc.view, TaskFilter{})
			if err != nil {
				t.Fatalf("ListTasks(%q): %v", tc.view, err)
			}
			if !sameSet(uuidsOf(got), tc.want) {
				t.Errorf("view %q: got %v, want %v", tc.view, uuidsOf(got), tc.want)
			}
			for _, task := range got {
				if task.UUID == "proj-today" || task.UUID == "proj-upcoming" || task.UUID == "proj-anytime" {
					if task.Type != model.TypeProject {
						t.Errorf("%s: got type %d, want %d", task.UUID, task.Type, model.TypeProject)
					}
				}
			}
		})
	}
}

// Widening the views to projects must not widen them to everything project-
// shaped: repeating project templates, the to-dos inside them, trashed
// projects and headings all stay out.
func TestListTasksProjectsExcludedFromViews(t *testing.T) {
	d, fx := newFixture(t)

	today := int64(model.ThingsDateFromTime(time.Now()))

	fx.area("area-work", "Work", 1)
	fx.project("proj-today", "Runbook audit", 5, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(2005))
	fx.project("proj-trashed", "Abandoned", 8, anytimeOn(today), todayIndexRef(today), inArea("area-work"), trashed(), todayIndex(0))

	// A repeating project template is filed under Repeating, not under the
	// bucket its row carries, and its children come with it (issues #147, #171).
	fx.project("proj-template", "Monthly review", 9, anytimeOn(today), inArea("area-work"), repeats())
	fx.todo("todo-in-template", "Draft agenda", 10, anytimeOn(today), inProject("proj-template"))

	// A heading (type 2) is structure inside a project, never a list row.
	fx.heading("head-1", "Phase one", 11, anytimeOn(today), inProject("proj-today"))

	excluded := []string{"proj-template", "todo-in-template", "proj-trashed", "head-1"}
	for _, view := range []string{"today", "anytime"} {
		got, err := d.ListTasks(view, TaskFilter{})
		if err != nil {
			t.Fatalf("ListTasks(%q): %v", view, err)
		}
		for _, uuid := range excluded {
			for _, task := range got {
				if task.UUID == uuid {
					t.Errorf("view %q: %s should not be listed", view, uuid)
				}
			}
		}
	}

	// The template itself still belongs to Repeating, which already carried
	// project templates.
	got, err := d.ListTasks("repeating", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(repeating): %v", err)
	}
	if !sameSet(uuidsOf(got), []string{"proj-template"}) {
		t.Errorf("repeating: got %v, want [proj-template]", uuidsOf(got))
	}
}

// Anytime is the one scheduled view without project rows. Every active project
// is trivially "anytime", so the app groups its to-dos under the project name
// instead of listing the project among them — measured against the app on
// 10 Sep 2026, whose Anytime held none of the 23 active projects and all 107
// of their to-dos. The views where a project has actually been put somewhere
// keep their rows (issue #217).
func TestAnytimeHasNoProjectRows(t *testing.T) {
	d, fx := newFixture(t)

	today := int64(model.ThingsDateFromTime(time.Now()))
	tomorrow := today + (1 << 7)

	fx.area("area-work", "Work", 1)
	fx.project("proj-today", "Runbook audit", 5, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(2005))
	fx.project("proj-upcoming", "Q4 planning", 6, somedayOn(tomorrow), inArea("area-work"), todayIndex(0))
	fx.project("proj-anytime", "Backlog", 7, anytime(), inArea("area-work"), todayIndex(0))

	// To-dos for company: one inside the scheduled project, one loose in the
	// same area, one top-level.
	fx.todo("todo-in-proj", "Read logs", 12, anytimeOn(today), todayIndexRef(today), inProject("proj-today"), todayIndex(3000))
	fx.todo("todo-in-area", "Call bank", 13, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(1))
	fx.todo("todo-loose", "Buy milk", 14, anytimeOn(today), todayIndexRef(today), todayIndex(7))

	got, err := d.ListTasks("anytime", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range got {
		if task.Type == model.TypeProject {
			t.Errorf("anytime lists project %s as a row", task.UUID)
		}
	}
	// The projects' to-dos are still there — the rows were dropped, not the
	// contents.
	if !sameSet(uuidsOf(got), []string{"todo-in-proj", "todo-in-area", "todo-loose"}) {
		t.Errorf("anytime: got %v, want the three to-dos", uuidsOf(got))
	}

	// today and upcoming still carry theirs.
	for _, tc := range []struct{ view, project string }{
		{"today", "proj-today"},
		{"upcoming", "proj-upcoming"},
	} {
		rows, err := d.ListTasks(tc.view, TaskFilter{})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, task := range rows {
			if task.UUID == tc.project && task.Type == model.TypeProject {
				found = true
			}
		}
		if !found {
			t.Errorf("view %q dropped its project row %s: %v", tc.view, tc.project, uuidsOf(rows))
		}
	}
}

// The app arranges Anytime as unfiled items, then areas, and inside an area
// its own loose to-dos before those of its projects. Bare t."index" order
// interleaved all of it, so a project's to-dos were scattered and the rendered
// group header repeated (issue #217).
func TestAnytimeGroupsByAreaThenProject(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES
		('ar-first', 'Work', 1, -2005),
		('ar-second', 'Home', 1, -900)`)
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, start, startBucket, area, "index") VALUES
		('proj-a', 'Project A', 1, 0, 0, 1, 0, 'ar-first', -11481),
		('proj-b', 'Project B', 1, 0, 0, 1, 0, 'ar-first', -10958)`)
	// Indexes are deliberately interleaved: index order alone would mix every
	// group together. Area indexes are negative, as Things writes them, so an
	// unfiled row's COALESCE default of 0 would sort last without the CASE.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, project, area, "index") VALUES
		('a1',        'A one',   0, 0, 0, 1, 0, 'proj-a', NULL,        5),
		('b1',        'B one',   0, 0, 0, 1, 0, 'proj-b', NULL,        1),
		('loose-1',   'Loose',   0, 0, 0, 1, 0, NULL,     'ar-first',  9),
		('a2',        'A two',   0, 0, 0, 1, 0, 'proj-a', NULL,        7),
		('unfiled',   'Unfiled', 0, 0, 0, 1, 0, NULL,     NULL,        8),
		('home-todo', 'Home',    0, 0, 0, 1, 0, NULL,     'ar-second', 2)`)

	got, err := d.ListTasks("anytime", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"unfiled", "loose-1", "a1", "a2", "b1", "home-todo"}
	if got := uuidsOf(got); !slices.Equal(got, want) {
		t.Errorf("anytime order: got %v, want %v", got, want)
	}
}

// Upcoming is a diary: the app reads it by date, then by the within-day
// position it also keys Today on. The view listed in bare t."index" order,
// which interleaved the dates (issue #217).
func TestUpcomingOrdersByDateThenTodayIndex(t *testing.T) {
	d := newTestDB(t)

	today := int64(model.ThingsDateFromTime(time.Now()))
	tomorrow := today + (1 << 7)
	later := today + (2 << 7)

	// Indexes run against the wanted order so t."index" alone cannot produce it.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, todayIndex, "index") VALUES
		('late-b',  'Later two',    0, 0, 0, 2, 0, ?, -100, 1),
		('soon-b',  'Tomorrow two', 0, 0, 0, 2, 0, ?, -200, 2),
		('late-a',  'Later one',    0, 0, 0, 2, 0, ?, -900, 3),
		('soon-a',  'Tomorrow one', 0, 0, 0, 2, 0, ?, -800, 4)`,
		later, tomorrow, later, tomorrow)

	got, err := d.ListTasks("upcoming", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"soon-a", "soon-b", "late-a", "late-b"}
	if got := uuidsOf(got); !slices.Equal(got, want) {
		t.Errorf("upcoming order: got %v, want %v", got, want)
	}
}

// A project row has no parent project, so --project can never match it: the
// filter narrows to the project's contents, not the project itself. --area
// still finds it, because a project carries its own area.
func TestListTasksProjectRowsAndFilters(t *testing.T) {
	d, fx := newFixture(t)

	today := int64(model.ThingsDateFromTime(time.Now()))

	fx.area("area-work", "Work", 1)
	fx.area("area-home", "Home", 2)
	fx.project("proj-today", "Runbook audit", 5, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(2005))

	// To-dos for company: one inside the scheduled project, one loose in the
	// same area, one top-level.
	fx.todo("todo-in-proj", "Read logs", 12, anytimeOn(today), todayIndexRef(today), inProject("proj-today"), todayIndex(3000))
	fx.todo("todo-in-area", "Call bank", 13, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(1))
	fx.todo("todo-loose", "Buy milk", 14, anytimeOn(today), todayIndexRef(today), todayIndex(7))

	byUUID, err := d.ListTasks("today", TaskFilter{Project: "proj-today"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(byUUID), []string{"todo-in-proj"}) {
		t.Errorf("--project proj-today: got %v, want [todo-in-proj]", uuidsOf(byUUID))
	}

	byTitle, err := d.ListTasks("today", TaskFilter{Project: "Runbook audit"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(byTitle), []string{"todo-in-proj"}) {
		t.Errorf("--project 'Runbook audit': got %v, want [todo-in-proj]", uuidsOf(byTitle))
	}

	byArea, err := d.ListTasks("today", TaskFilter{Area: "area-work"})
	if err != nil {
		t.Fatal(err)
	}
	// todo-in-proj inherits area-work through proj-today (the pa join).
	want := []string{"proj-today", "todo-in-proj", "todo-in-area"}
	if !sameSet(uuidsOf(byArea), want) {
		t.Errorf("--area area-work: got %v, want %v", uuidsOf(byArea), want)
	}

	otherArea, err := d.ListTasks("today", TaskFilter{Area: "area-home"})
	if err != nil {
		t.Fatal(err)
	}
	if len(otherArea) != 0 {
		t.Errorf("--area area-home: got %v, want none", uuidsOf(otherArea))
	}
}

// The bare --project/--area/--tag filter routes to the internal catch-all
// view, which was pinned to to-dos while every named view except inbox had
// been widened to projects, so an agent sweeping an area that way got none of
// its projects (issue #222).
func TestListTasksCatchAllViewIncludesProjects(t *testing.T) {
	d, fx := newFixture(t)

	today := int64(model.ThingsDateFromTime(time.Now()))
	tomorrow := today + (1 << 7)

	fx.area("area-work", "Work", 1)
	fx.project("proj-today", "Runbook audit", 5, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(2005))
	fx.project("proj-upcoming", "Q4 planning", 6, somedayOn(tomorrow), inArea("area-work"), todayIndex(0))
	fx.project("proj-anytime", "Backlog", 7, anytime(), inArea("area-work"), todayIndex(0))

	// To-dos for company: one inside the scheduled project, one loose in the
	// same area, one top-level.
	fx.todo("todo-in-proj", "Read logs", 12, anytimeOn(today), todayIndexRef(today), inProject("proj-today"), todayIndex(3000))
	fx.todo("todo-in-area", "Call bank", 13, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(1))
	fx.todo("todo-loose", "Buy milk", 14, anytimeOn(today), todayIndexRef(today), todayIndex(7))

	fx.tag("tag-urgent", "urgent", 1)
	fx.tagged("proj-anytime", "tag-urgent")
	fx.tagged("todo-in-area", "tag-urgent")

	byArea, err := d.ListTasks("project", TaskFilter{Area: "area-work"})
	if err != nil {
		t.Fatal(err)
	}
	// The area's three open projects, plus the to-do in one of them (through
	// the pa join) and the loose to-do filed on the area itself.
	wantArea := []string{"proj-today", "proj-upcoming", "proj-anytime", "todo-in-proj", "todo-in-area"}
	if !sameSet(uuidsOf(byArea), wantArea) {
		t.Errorf("--area area-work: got %v, want %v", uuidsOf(byArea), wantArea)
	}

	byTag, err := d.ListTasks("project", TaskFilter{Tag: "urgent"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(byTag), []string{"proj-anytime", "todo-in-area"}) {
		t.Errorf("--tag urgent: got %v, want [proj-anytime todo-in-area]", uuidsOf(byTag))
	}
	for _, task := range byTag {
		if task.UUID == "proj-anytime" && task.Type != model.TypeProject {
			t.Errorf("proj-anytime: got type %d, want %d", task.Type, model.TypeProject)
		}
	}

	// A project has no parent project, so --project still narrows to contents.
	byProject, err := d.ListTasks("project", TaskFilter{Project: "proj-today"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(byProject), []string{"todo-in-proj"}) {
		t.Errorf("--project proj-today: got %v, want [todo-in-proj]", uuidsOf(byProject))
	}
}

// Widening the catch-all view to projects must not widen it past them:
// headings, trashed projects, repeating project templates and the to-dos
// inside a template all stay out, as they do in the named views.
func TestListTasksCatchAllViewExclusions(t *testing.T) {
	d, fx := newFixture(t)

	today := int64(model.ThingsDateFromTime(time.Now()))
	tomorrow := today + (1 << 7)

	fx.area("area-work", "Work", 1)
	fx.project("proj-today", "Runbook audit", 5, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(2005))
	fx.project("proj-upcoming", "Q4 planning", 6, somedayOn(tomorrow), inArea("area-work"), todayIndex(0))
	fx.project("proj-anytime", "Backlog", 7, anytime(), inArea("area-work"), todayIndex(0))
	fx.project("proj-trashed", "Abandoned", 8, anytimeOn(today), todayIndexRef(today), inArea("area-work"), trashed(), todayIndex(0))

	// A repeating project template is filed under Repeating, not under the
	// bucket its row carries, and its children come with it (issues #147, #171).
	fx.project("proj-template", "Monthly review", 9, anytimeOn(today), inArea("area-work"), repeats())
	fx.todo("todo-in-template", "Draft agenda", 10, anytimeOn(today), inProject("proj-template"))

	// A heading (type 2) is structure inside a project, never a list row.
	fx.heading("head-1", "Phase one", 11, anytimeOn(today), inProject("proj-today"))

	// To-dos for company: one inside the scheduled project, one loose in the
	// same area, one top-level.
	fx.todo("todo-in-proj", "Read logs", 12, anytimeOn(today), todayIndexRef(today), inProject("proj-today"), todayIndex(3000))
	fx.todo("todo-in-area", "Call bank", 13, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(1))
	fx.todo("todo-loose", "Buy milk", 14, anytimeOn(today), todayIndexRef(today), todayIndex(7))

	got, err := d.ListTasks("project", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	// Asserted as a whole set, not just as absences: a catch-all that returned
	// nothing at all would satisfy every exclusion on its own.
	want := []string{
		"proj-today", "proj-upcoming", "proj-anytime",
		"todo-in-proj", "todo-in-area", "todo-loose",
	}
	if !sameSet(uuidsOf(got), want) {
		t.Errorf("catch-all view: got %v, want %v", uuidsOf(got), want)
	}
}

// The today ordering keys on the parent project's index, which a project row
// leaves NULL. It therefore sits in its area's group next to that area's
// unparented to-dos, ordered against them by todayIndex, and ahead of the
// to-dos of any project with a non-zero index.
func TestListTasksTodayOrderWithProjects(t *testing.T) {
	d, fx := newFixture(t)

	today := int64(model.ThingsDateFromTime(time.Now()))

	fx.area("area-work", "Work", 1)
	fx.project("proj-today", "Runbook audit", 5, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(2005))

	// To-dos for company: one inside the scheduled project, one loose in the
	// same area, one top-level.
	fx.todo("todo-in-proj", "Read logs", 12, anytimeOn(today), todayIndexRef(today), inProject("proj-today"), todayIndex(3000))
	fx.todo("todo-in-area", "Call bank", 13, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(1))
	fx.todo("todo-loose", "Buy milk", 14, anytimeOn(today), todayIndexRef(today), todayIndex(7))

	got, err := d.ListTasks("today", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"todo-loose", "todo-in-area", "proj-today", "todo-in-proj"}
	if !reflect.DeepEqual(uuidsOf(got), want) {
		t.Errorf("today order: got %v, want %v", uuidsOf(got), want)
	}
}

// Today is arranged like Anytime and Someday — unfiled items, then areas, and
// inside an area its loose to-dos before its projects' — and ordered within a
// group by todayIndex alone. Measured against the app on 10 Sep 2026, where
// these keys reproduced a 27-row Today in every position (issue #237).
func TestTodayGroupsLooseTodosBeforeProjectTodos(t *testing.T) {
	d := newTestDB(t)

	today := int64(model.ThingsDateFromTime(time.Now()))
	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES
		('ar-first', 'Work', 1, -2005),
		('ar-second', 'Home', 1, -550)`)
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, area, "index") VALUES
		('proj-a', 'Project A', 1, 0, 0, 'ar-first', -11481)`)
	// todayIndex runs against t."index" so the within-group key is the one
	// under test, and against the group order so grouping cannot be faked.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate,
		 todayIndexReferenceDate, project, area, "index", todayIndex) VALUES
		('in-proj',  'In a project', 0, 0, 0, 1, 0, ?, ?, 'proj-a', NULL,        1, -900),
		('loose',    'Loose',        0, 0, 0, 1, 0, ?, ?, NULL,     'ar-first',  2, -100),
		('unfiled',  'Unfiled',      0, 0, 0, 1, 0, ?, ?, NULL,     NULL,        3, -50),
		('other-ar', 'Other area',   0, 0, 0, 1, 0, ?, ?, NULL,     'ar-second', 4, -999)`,
		today, today, today, today, today, today, today, today)

	got, err := d.ListTasks("today", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"unfiled", "loose", "in-proj", "other-ar"}
	if got := uuidsOf(got); !slices.Equal(got, want) {
		t.Errorf("today order: got %v, want %v", got, want)
	}
}

// The app leaves a closed item where it was, struck through, rather than
// pushing it to the end of its group, so today orders on todayIndex alone and
// not on status first. The app's Today interleaved six closed rows through
// three groups when this was measured (issue #237).
func TestTodayInterleavesClosedItemsByTodayIndex(t *testing.T) {
	d := newTestDB(t)

	today := int64(model.ThingsDateFromTime(time.Now()))
	stop := model.TimeToUnix(time.Now())
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate,
		 todayIndexReferenceDate, stopDate, "index", todayIndex) VALUES
		('open-first',  'One',   0, 0, 0, 1, 0, ?, ?, NULL, 1, -300),
		('closed-mid',  'Two',   0, 3, 0, 1, 0, ?, ?, ?,    2, -200),
		('open-last',   'Three', 0, 0, 0, 1, 0, ?, ?, NULL, 3, -100)`,
		today, today, today, today, stop, today, today)

	got, err := d.ListTasks("today", TaskFilter{IncludeCompleted: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"open-first", "closed-mid", "open-last"}
	if got := uuidsOf(got); !slices.Equal(got, want) {
		t.Errorf("today order: got %v, want %v — the closed row must stay in place", got, want)
	}
}

// The app keeps an item closed today visible, struck through, in whatever list
// it was in until the day rolls over — Anytime as much as Today. Without the
// flag anytime is open rows only, as before (issue #238).
func TestAnytimeIncludeCompleted(t *testing.T) {
	d := newTestDB(t)

	stopToday := model.TimeToUnix(time.Now())
	stopYesterday := model.TimeToUnix(time.Now().Add(-25 * time.Hour))
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, "index") VALUES
		('open-one',       'Open',           0, 0, 0, 1, 0, NULL, NULL, 1),
		('closed-today',   'Done today',     0, 3, 0, 1, 0, NULL, ?,    2),
		('cancelled-today','Dropped today',  0, 2, 0, 1, 0, NULL, ?,    3),
		('closed-earlier', 'Done yesterday', 0, 3, 0, 1, 0, NULL, ?,    4)`,
		stopToday, stopToday, stopYesterday)

	plain, err := d.ListTasks("anytime", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(plain), []string{"open-one"}) {
		t.Errorf("anytime: got %v, want [open-one]", uuidsOf(plain))
	}

	withClosed, err := d.ListTasks("anytime", TaskFilter{IncludeCompleted: true})
	if err != nil {
		t.Fatal(err)
	}
	// The one closed yesterday stays out: Things filed it when the day rolled
	// over. Cancelled counts as closed, the same as in today.
	want := []string{"open-one", "closed-today", "cancelled-today"}
	if !sameSet(uuidsOf(withClosed), want) {
		t.Errorf("anytime --include-completed: got %v, want %v", uuidsOf(withClosed), want)
	}

	// "Log Completed Now" files the day's closed items early, and the flag
	// respects it here exactly as it does in today.
	future := model.TimeToUnix(time.Now().Add(1 * time.Minute))
	mustExec(t, d, `INSERT INTO TMSettings (uuid, manualLogDate) VALUES ('s', ?)`, future)
	afterLog, err := d.ListTasks("anytime", TaskFilter{IncludeCompleted: true})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(afterLog), []string{"open-one"}) {
		t.Errorf("after Log Completed Now: got %v, want [open-one]", uuidsOf(afterLog))
	}
}

// today and anytime answer "is this row still shown in place?" from one
// helper, so a row scheduled for today that is closed today appears under both
// — which is what the app does, since a to-do scheduled for a day sits in the
// Anytime bucket too. All nine such rows in the measured data were in both.
func TestTodayAndAnytimeAgreeOnJustClosedRows(t *testing.T) {
	d := newTestDB(t)

	today := int64(model.ThingsDateFromTime(time.Now()))
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, "index")
		VALUES ('scheduled-and-done', 'Done', 0, 3, 0, 1, 0, ?, ?, 1)`,
		today, model.TimeToUnix(time.Now()))

	for _, view := range []string{"today", "anytime"} {
		got, err := d.ListTasks(view, TaskFilter{IncludeCompleted: true})
		if err != nil {
			t.Fatal(err)
		}
		if !sameSet(uuidsOf(got), []string{"scheduled-and-done"}) {
			t.Errorf("%s --include-completed: got %v, want the row", view, uuidsOf(got))
		}
	}

	// And it is in neither list without the flag, nor in the logbook, which
	// still holds nothing closed today.
	logged, err := d.ListTasks("logbook", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(logged) != 0 {
		t.Errorf("logbook: got %v, want empty — the row closed today", uuidsOf(logged))
	}
}

// today and anytime both drop repeating templates and the contents of a
// repeating project template; the Logbook keeps them. So the Logbook must not
// withhold such a row on the strength of the Anytime bucket it happens to sit
// in — nothing would be showing it, and it would list nowhere at all, which is
// the hazard issue #230 named (issues #171, #238).
func TestLogbookKeepsRepeatingTemplatesClosedToday(t *testing.T) {
	d := newTestDB(t)

	stopToday := model.TimeToUnix(time.Now())
	// The template row itself: it carries the recurrence rule.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, rt1_recurrenceRule, "index") VALUES
		('template', 'Repeats', 0, 3, 0, 1, 0, NULL, ?, x'00', 1)`, stopToday)
	// And a to-do inside a repeating project template, which carries no rule
	// of its own — only its project does.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, rt1_recurrenceRule, "index") VALUES
		('proj-template', 'Repeating project', 1, 0, 0, 1, 0, NULL, NULL, x'00', 2)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, project, "index") VALUES
		('inside-template', 'Step', 0, 3, 0, 1, 0, NULL, ?, 'proj-template', 3)`, stopToday)

	logged, err := d.ListTasks("logbook", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"template", "inside-template"}
	if !sameSet(uuidsOf(logged), want) {
		t.Errorf("logbook: got %v, want %v", uuidsOf(logged), want)
	}

	// Neither list is showing them, which is why the Logbook has to.
	for _, view := range []string{"today", "anytime"} {
		got, err := d.ListTasks(view, TaskFilter{IncludeCompleted: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("%s --include-completed: got %v, want empty", view, uuidsOf(got))
		}
	}
}

// A closed project is one row and its to-dos are not listed beside it. The
// Logbook and Trash have folded them since issue #229; the --include-completed
// variants of today and anytime folded nothing, so the same to-do was folded
// in one view and listed in another (issue #249).
func TestIncludeCompletedFoldsClosedProjectChildren(t *testing.T) {
	today := int64(model.ThingsDateFromTime(time.Now()))
	stopToday := model.TimeToUnix(time.Now())

	for _, view := range []string{"today", "anytime"} {
		t.Run(view, func(t *testing.T) {
			d := newTestDB(t)
			mustExec(t, d, `INSERT INTO TMTask
				(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, "index") VALUES
				('proj-done',  'Finished', 1, 3, 0, 1, 0, ?,    ?,    1),
				('proj-open',  'Live',     1, 0, 0, 1, 0, ?,    NULL, 2),
				('proj-binned','Binned',   1, 0, 1, 1, 0, ?,    NULL, 3)`,
				today, stopToday, today, today)
			mustExec(t, d, `INSERT INTO TMTask
				(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, project, "index") VALUES
				('under-done',   'Folded',    0, 3, 0, 1, 0, ?, ?, 'proj-done',   4),
				('under-binned', 'Also gone', 0, 3, 0, 1, 0, ?, ?, 'proj-binned', 5),
				('under-open',   'Listed',    0, 3, 0, 1, 0, ?, ?, 'proj-open',   6),
				('unparented',   'Listed',    0, 3, 0, 1, 0, ?, ?, NULL,          7)`,
				today, stopToday, today, stopToday, today, stopToday, today, stopToday)

			got, err := d.ListTasks(view, TaskFilter{IncludeCompleted: true})
			if err != nil {
				t.Fatal(err)
			}
			// The closed project itself is a row in today, which carries
			// project rows; anytime carries none (issue #217).
			want := []string{"under-open", "unparented", "proj-open"}
			if view == "today" {
				want = append(want, "proj-done")
			} else {
				want = want[:2]
			}
			if !sameSet(uuidsOf(got), want) {
				t.Errorf("%s --include-completed: got %v, want %v", view, uuidsOf(got), want)
			}
		})
	}
}

// The fold makes a closed project one row rather than a row plus its contents.
// Naming that project is asking for the contents, so the fold comes off — the
// answer `things --project <uuid>` and `show --agent` already give. Without
// this, `things today --project "Launch v2" --include-completed` on a finished
// project returned nothing at all (issue #253).
func TestNamedProjectLiftsTheFoldUnderIncludeCompleted(t *testing.T) {
	today := int64(model.ThingsDateFromTime(time.Now()))
	stopToday := model.TimeToUnix(time.Now())

	for _, view := range []string{"today", "anytime"} {
		t.Run(view, func(t *testing.T) {
			d := newTestDB(t)
			mustExec(t, d, `INSERT INTO TMTask
				(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, "index") VALUES
				('proj-done', 'Finished', 1, 3, 0, 1, 0, ?, ?,    1),
				('proj-open', 'Live',     1, 0, 0, 1, 0, ?, NULL, 2)`,
				today, stopToday, today)
			mustExec(t, d, `INSERT INTO TMTask
				(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, project, "index") VALUES
				('under-done', 'Folded', 0, 3, 0, 1, 0, ?, ?, 'proj-done', 3),
				('under-open', 'Listed', 0, 3, 0, 1, 0, ?, ?, 'proj-open', 4)`,
				today, stopToday, today, stopToday)

			// Unfiltered, the fold still applies: under-done is folded away.
			all, err := d.ListTasks(view, TaskFilter{IncludeCompleted: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range all {
				if task.UUID == "under-done" {
					t.Errorf("%s --include-completed: the fold should still hide under-done", view)
				}
			}

			// Naming the closed project returns its contents.
			named, err := d.ListTasks(view, TaskFilter{Project: "proj-done", IncludeCompleted: true})
			if err != nil {
				t.Fatal(err)
			}
			if !sameSet(uuidsOf(named), []string{"under-done"}) {
				t.Errorf("%s --project proj-done --include-completed: got %v, want [under-done]", view, uuidsOf(named))
			}

			// By title as well as by uuid, since --project takes either.
			byTitle, err := d.ListTasks(view, TaskFilter{Project: "Finished", IncludeCompleted: true})
			if err != nil {
				t.Fatal(err)
			}
			if !sameSet(uuidsOf(byTitle), []string{"under-done"}) {
				t.Errorf("%s --project Finished: got %v, want [under-done]", view, uuidsOf(byTitle))
			}

			// An open project is unaffected: the lifted clause was true for it
			// either way.
			openNamed, err := d.ListTasks(view, TaskFilter{Project: "proj-open", IncludeCompleted: true})
			if err != nil {
				t.Fatal(err)
			}
			if !sameSet(uuidsOf(openNamed), []string{"under-open"}) {
				t.Errorf("%s --project proj-open: got %v, want [under-open]", view, uuidsOf(openNamed))
			}

			// And without the flag nothing changes: the fold lives inside the
			// closed branch, so a closed child stays out whoever named it.
			noFlag, err := d.ListTasks(view, TaskFilter{Project: "proj-done"})
			if err != nil {
				t.Fatal(err)
			}
			if len(noFlag) != 0 {
				t.Errorf("%s --project proj-done (no flag): got %v, want none", view, uuidsOf(noFlag))
			}
		})
	}
}

// Filter values are matched literally, not as LIKE patterns. `--project '%'`
// used to match every project — and since issue #260 lift the closed-project
// fold for all of them at once — and a title holding a `%` or a `_` matched
// more than itself (issue #262).
func TestFilterValuesMatchLiterally(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES
		('ar-pct',   '100% Work', 1, 1),
		('ar-plain', 'Home',      1, 2)`)
	mustExec(t, d, `INSERT INTO TMTask (uuid, title, type, status, trashed, area, "index") VALUES
		('proj-pct',   '100% Done',  1, 0, 0, 'ar-pct',   1),
		('proj-under', 'A_B',        1, 0, 0, 'ar-plain', 2),
		('proj-plain', 'AxB',        1, 0, 0, 'ar-plain', 3)`)
	mustExec(t, d, `INSERT INTO TMTag (uuid, title, "index") VALUES
		('tg-pct',   '50%',  1),
		('tg-plain', '50ish', 2)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, project, area, "index") VALUES
		('in-pct',   'Under the percent project', 0, 0, 0, 1, 0, 'proj-pct',   NULL, 4),
		('in-under', 'Under A_B',                 0, 0, 0, 1, 0, 'proj-under', NULL, 5),
		('in-plain', 'Under AxB',                 0, 0, 0, 1, 0, 'proj-plain', NULL, 6),
		('in-home',  'Loose in Home',             0, 0, 0, 1, 0, NULL,         'ar-plain', 7)`)
	mustExec(t, d, `INSERT INTO TMTaskTag (tasks, tags) VALUES
		('in-pct', 'tg-pct'), ('in-plain', 'tg-plain')`)

	cases := []struct {
		name   string
		filter TaskFilter
		want   []string
	}{
		// A bare wildcard is a title now, and no project is called "%".
		{"project percent wildcard", TaskFilter{Project: "%"}, nil},
		{"area percent wildcard", TaskFilter{Area: "%"}, nil},
		{"tag percent wildcard", TaskFilter{Tag: "%"}, nil},
		// A literal % in the title still matches itself.
		{"project with a percent", TaskFilter{Project: "100% Done"}, []string{"in-pct"}},
		{"area with a percent", TaskFilter{Area: "100% Work"}, []string{"proj-pct", "in-pct"}},
		{"tag with a percent", TaskFilter{Tag: "50%"}, []string{"in-pct"}},
		// An underscore is a character, not "any character": A_B must not
		// match AxB.
		{"underscore is literal", TaskFilter{Project: "A_B"}, []string{"in-under"}},
		{"underscore does not match any char", TaskFilter{Project: "AxB"}, []string{"in-plain"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := d.ListTasks("project", tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if !sameSet(uuidsOf(got), tc.want) {
				t.Errorf("got %v, want %v", uuidsOf(got), tc.want)
			}
		})
	}

	// Matching stays case-insensitive, as it was before the escaping.
	got, err := d.ListTasks("project", TaskFilter{Project: "100% done"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(got), []string{"in-pct"}) {
		t.Errorf("lowercased title: got %v, want [in-pct]", uuidsOf(got))
	}

	// A uuid still matches exactly, through the equality arm rather than LIKE.
	byUUID, err := d.ListTasks("project", TaskFilter{Project: "proj-pct"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(byUUID), []string{"in-pct"}) {
		t.Errorf("by uuid: got %v, want [in-pct]", uuidsOf(byUUID))
	}
}

// The lift is scoped to a named project. A view filtered by area or tag alone
// is still the folded set, or naming an area would quietly reopen every closed
// project in it.
func TestAreaAndTagFiltersDoNotLiftTheFold(t *testing.T) {
	d := newTestDB(t)

	today := int64(model.ThingsDateFromTime(time.Now()))
	stopToday := model.TimeToUnix(time.Now())
	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES ('ar', 'Work', 1, 1)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, area, "index")
		VALUES ('proj-done', 'Finished', 1, 3, 0, 1, 0, ?, ?, 'ar', 1)`, today, stopToday)
	mustExec(t, d, `INSERT INTO TMTag (uuid, title, "index") VALUES ('tg', 'urgent', 1)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, project, "index")
		VALUES ('under-done', 'Folded', 0, 3, 0, 1, 0, ?, ?, 'proj-done', 2)`, today, stopToday)
	mustExec(t, d, `INSERT INTO TMTaskTag (tasks, tags) VALUES ('under-done', 'tg')`)

	for _, tc := range []struct {
		name   string
		filter TaskFilter
	}{
		{"area", TaskFilter{Area: "ar", IncludeCompleted: true}},
		{"tag", TaskFilter{Tag: "urgent", IncludeCompleted: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := d.ListTasks("today", tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range got {
				if task.UUID == "under-done" {
					t.Errorf("--%s lifted the fold: got %v", tc.name, uuidsOf(got))
				}
			}
		})
	}
}

// The fold sits inside the closed branch, so it can only remove a row the flag
// just added. An open to-do under a closed project is a different question and
// issue #249 does not ask it — dropping it would take real work out of Today.
func TestIncludeCompletedKeepsOpenTodosUnderClosedProject(t *testing.T) {
	d := newTestDB(t)

	today := int64(model.ThingsDateFromTime(time.Now()))
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, "index")
		VALUES ('proj-done', 'Finished', 1, 3, 0, 1, 0, ?, ?, 1)`,
		today, model.TimeToUnix(time.Now()))
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, project, "index")
		VALUES ('still-open', 'Left over', 0, 0, 0, 1, 0, ?, 'proj-done', 2)`, today)

	for _, tc := range []struct {
		view             string
		includeCompleted bool
	}{
		{"today", false}, {"today", true}, {"anytime", false}, {"anytime", true},
	} {
		got, err := d.ListTasks(tc.view, TaskFilter{IncludeCompleted: tc.includeCompleted})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, task := range got {
			if task.UUID == "still-open" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s (include-completed=%v) dropped the open to-do: %v", tc.view, tc.includeCompleted, uuidsOf(got))
		}
	}
}

// The folded row is not stranded: naming the project returns it, which is what
// issue #229 settled when it folded the same row out of the Logbook.
func TestFoldedJustClosedRowIsReachableByProject(t *testing.T) {
	d := newTestDB(t)

	today := int64(model.ThingsDateFromTime(time.Now()))
	stopToday := model.TimeToUnix(time.Now())
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, "index")
		VALUES ('proj-done', 'Finished', 1, 3, 0, 1, 0, ?, ?, 1)`, today, stopToday)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, project, "index")
		VALUES ('folded', 'Folded', 0, 3, 0, 1, 0, ?, ?, 'proj-done', 2)`, today, stopToday)

	for _, view := range []string{"today", "anytime", "logbook"} {
		got, err := d.ListTasks(view, TaskFilter{IncludeCompleted: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range got {
			if task.UUID == "folded" {
				t.Errorf("view %q lists the folded row", view)
			}
		}
	}

	contents, err := d.ListTasks("project", TaskFilter{Project: "proj-done"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(contents), []string{"folded"}) {
		t.Errorf("--project proj-done: got %v, want [folded]", uuidsOf(contents))
	}
}

// Someday takes the same arrangement. Its filter keeps only unparented rows,
// so what is left to check is that unfiled items lead and areas follow in area
// order — it matched the app in none of its six positions before (issue #237).
func TestSomedayGroupsUnfiledThenAreas(t *testing.T) {
	d := newTestDB(t)

	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES
		('ar-first', 'Work', 1, -2005),
		('ar-second', 'Home', 1, -550)`)
	// Indexes run against the wanted order, so t."index" alone cannot make it.
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, area, "index") VALUES
		('second-b', 'Home two',  0, 0, 0, 2, 0, NULL, 'ar-second', 1),
		('first-a',  'Work one',  0, 0, 0, 2, 0, NULL, 'ar-first',  2),
		('unfiled',  'Unfiled',   0, 0, 0, 2, 0, NULL, NULL,        3),
		('second-a', 'Home one',  0, 0, 0, 2, 0, NULL, 'ar-second', 0)`)

	got, err := d.ListTasks("someday", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"unfiled", "first-a", "second-a", "second-b"}
	if got := uuidsOf(got); !slices.Equal(got, want) {
		t.Errorf("someday order: got %v, want %v", got, want)
	}
}

// --include-completed carries project rows too: a project completed today and
// not yet logged is still on the app's Today list.
func TestListTasksTodayIncludeCompletedProject(t *testing.T) {
	d, fx := newFixture(t)

	today := int64(model.ThingsDateFromTime(time.Now()))
	// Now, not "a minute ago": the calendar day decides whether a closed row is
	// still under Today (issue #230), and a minute before midnight falls on the
	// previous day, which failed this test in the first minute of every day.
	stopToday := model.TimeToUnix(time.Now())

	fx.area("area-work", "Work", 1)
	// An open project for company, so the default list is not empty and the
	// exclusion below turns on the status rather than the row's type.
	fx.project("proj-today", "Runbook audit", 5, anytimeOn(today), todayIndexRef(today), inArea("area-work"), todayIndex(2005))
	fx.project("proj-done", "Shipped", 20, anytimeOn(today), todayIndexRef(today), inArea("area-work"), completed(stopToday), todayIndex(9000))

	got, err := d.ListTasks("today", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range got {
		if task.UUID == "proj-done" {
			t.Fatal("completed project should be excluded by default")
		}
	}

	got, err = d.ListTasks("today", TaskFilter{IncludeCompleted: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, task := range got {
		if task.UUID == "proj-done" {
			found = true
		}
	}
	if !found {
		t.Errorf("--include-completed: proj-done missing from %v", uuidsOf(got))
	}
}

// Things shows a project deferred to Someday in its Someday list, and a
// completed project in its Logbook. The CLI's two views were pinned to to-dos,
// so both project rows were invisible (issue #206).
func TestListTasksSomedayAndLogbookIncludeProjects(t *testing.T) {
	d, fx := newFixture(t)

	// The two ends of a project's life (issue #206): one deferred to Someday
	// and one completed into the Logbook, each with a to-do beside it. Both
	// stop dates fall on an earlier calendar day, because an item closed today
	// is still under Today rather than in the Logbook (issue #230).
	fx.area("area-work", "Work", 1)
	stopEarly := model.TimeToUnix(time.Now().Add(-48 * time.Hour))
	stopLate := model.TimeToUnix(time.Now().Add(-26 * time.Hour))

	fx.project("proj-someday", "Learn Welsh", 1, someday(), inArea("area-work"))
	fx.todo("todo-someday", "Read a book", 2, someday(), inArea("area-work"))
	fx.project("proj-logged", "Site rebuild", 3, anytime(), inArea("area-work"), completed(stopLate))
	fx.todo("todo-logged", "Ship the CSS", 4, anytime(), completed(stopEarly))

	cases := []struct {
		view    string
		want    []string
		project string
	}{
		{"someday", []string{"proj-someday", "todo-someday"}, "proj-someday"},
		{"logbook", []string{"proj-logged", "todo-logged"}, "proj-logged"},
	}

	for _, tc := range cases {
		t.Run(tc.view, func(t *testing.T) {
			got, err := d.ListTasks(tc.view, TaskFilter{})
			if err != nil {
				t.Fatalf("ListTasks(%q): %v", tc.view, err)
			}
			if !sameSet(uuidsOf(got), tc.want) {
				t.Errorf("view %q: got %v, want %v", tc.view, uuidsOf(got), tc.want)
			}
			for _, task := range got {
				if task.UUID != tc.project {
					continue
				}
				if task.Type != model.TypeProject {
					t.Errorf("%s: got type %d, want %d", task.UUID, task.Type, model.TypeProject)
				}
			}
		})
	}
}

// Widening the two views to projects must not widen them to everything
// project-shaped: a Someday-start repeating project template, the to-dos
// inside it, trashed projects and headings all stay out.
func TestListTasksSomedayLogbookProjectExclusions(t *testing.T) {
	d, fx := newFixture(t)

	stopEarly := model.TimeToUnix(time.Now().Add(-48 * time.Hour))
	stopLate := model.TimeToUnix(time.Now().Add(-26 * time.Hour))
	fx.project("proj-someday", "Learn Welsh", 1, someday())
	fx.project("proj-logged", "Site rebuild", 3, anytime(), completed(stopLate))

	// Trashed rows belong to the trash view, not to Someday or the Logbook.
	fx.project("proj-someday-trashed", "Dropped idea", 5, someday(), trashed())
	fx.project("proj-logged-trashed", "Dropped work", 6, anytime(), trashed(), completed(stopEarly))

	// A Someday-start repeating project template is filed under Repeating, not
	// under the bucket its row carries, and its children come with it
	// (issues #147, #171).
	fx.project("proj-template", "Annual review", 7, someday(), repeats())
	fx.todo("todo-in-template", "Draft agenda", 8, someday(), inProject("proj-template"))

	// A heading is structure inside a project, never a list row.
	fx.heading("head-someday", "Phase one", 9, someday(), inProject("proj-someday"))
	fx.heading("head-logged", "Phase two", 10, anytime(), inProject("proj-logged"), completed(stopEarly))

	excluded := map[string][]string{
		"someday": {"proj-template", "todo-in-template", "proj-someday-trashed", "head-someday"},
		"logbook": {"proj-logged-trashed", "head-logged"},
	}
	for view, uuids := range excluded {
		got, err := d.ListTasks(view, TaskFilter{})
		if err != nil {
			t.Fatalf("ListTasks(%q): %v", view, err)
		}
		for _, uuid := range uuids {
			for _, task := range got {
				if task.UUID == uuid {
					t.Errorf("view %q: %s should not be listed", view, uuid)
				}
			}
		}
	}

	// The template itself still belongs to Repeating, which already carried
	// project templates.
	got, err := d.ListTasks("repeating", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(repeating): %v", err)
	}
	if !sameSet(uuidsOf(got), []string{"proj-template"}) {
		t.Errorf("repeating: got %v, want [proj-template]", uuidsOf(got))
	}
}

// The Logbook orders by completion date, newest first, and a project row is
// ordered by its own stopDate like any other row.
func TestListTasksLogbookOrderWithProject(t *testing.T) {
	d, fx := newFixture(t)

	// stopLate is the later of the two, and the project carries it, so a
	// Logbook ordered newest-first has the project row ahead of the to-do.
	stopEarly := model.TimeToUnix(time.Now().Add(-48 * time.Hour))
	stopLate := model.TimeToUnix(time.Now().Add(-26 * time.Hour))
	fx.project("proj-logged", "Site rebuild", 3, anytime(), completed(stopLate))
	fx.todo("todo-logged", "Ship the CSS", 4, anytime(), completed(stopEarly))

	got, err := d.ListTasks("logbook", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"proj-logged", "todo-logged"}
	if !reflect.DeepEqual(uuidsOf(got), want) {
		t.Errorf("logbook order: got %v, want %v", uuidsOf(got), want)
	}
}

// A project row has no parent project, so --project can never match it in
// these two views either; --area still finds it, because a project carries its
// own area.
func TestListTasksSomedayProjectRowsAndFilters(t *testing.T) {
	d, fx := newFixture(t)

	fx.area("area-work", "Work", 1)
	fx.project("proj-someday", "Learn Welsh", 1, someday(), inArea("area-work"))
	fx.todo("todo-someday", "Read a book", 2, someday(), inArea("area-work"))

	byUUID, err := d.ListTasks("someday", TaskFilter{Project: "proj-someday"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byUUID) != 0 {
		t.Errorf("--project proj-someday: got %v, want none", uuidsOf(byUUID))
	}

	byArea, err := d.ListTasks("someday", TaskFilter{Area: "area-work"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"proj-someday", "todo-someday"}
	if !sameSet(uuidsOf(byArea), want) {
		t.Errorf("--area area-work: got %v, want %v", uuidsOf(byArea), want)
	}
}

// Things puts a trashed project in its Trash list, and `things projects`
// filters trashed rows, so pinning the view to to-dos left a trashed project
// visible nowhere (issue #212).
func TestListTasksTrashIncludesProjects(t *testing.T) {
	d, fx := newFixture(t)

	fx.area("area-home", "Home", 1)
	fx.project("trash-proj", "Dropped project", 1, anytime(), inArea("area-home"), trashed())
	fx.project("trash-proj-done", "Shelved rebuild", 2, anytime(), inArea("area-home"), trashed(), status(model.StatusCompleted))
	fx.todo("trash-todo", "Dropped to-do", 3, anytime(), inArea("area-home"), trashed())

	// Live rows never belong to the trash view, whichever kind they are, and a
	// heading is structure inside a project rather than a list row — not even
	// once its project has been trashed.
	fx.project("live-proj", "Still going", 4, anytime(), inArea("area-home"))
	fx.todo("live-todo", "Still to do", 5, anytime(), inArea("area-home"))
	fx.heading("trash-head", "Phase one", 6, inProject("trash-proj"), trashed())

	got, err := d.ListTasks("trash", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(trash): %v", err)
	}
	want := []string{"trash-proj", "trash-proj-done", "trash-todo"}
	if !sameSet(uuidsOf(got), want) {
		t.Errorf("trash: got %v, want %v", uuidsOf(got), want)
	}

	for _, task := range got {
		if task.UUID != "trash-proj" && task.UUID != "trash-proj-done" {
			continue
		}
		if task.Type != model.TypeProject {
			t.Errorf("%s: got type %v, want %v", task.UUID, task.Type, model.TypeProject)
		}
	}
}

// Widening trash to projects must not widen it to everything project-shaped:
// headings and live rows of either kind stay out.
func TestListTasksTrashProjectExclusions(t *testing.T) {
	d, fx := newFixture(t)

	fx.project("trash-proj", "Dropped project", 1, anytime(), trashed())
	fx.project("live-proj", "Still going", 4, anytime())
	fx.todo("live-todo", "Still to do", 5, anytime())
	fx.heading("trash-head", "Phase one", 6, inProject("trash-proj"), trashed())

	got, err := d.ListTasks("trash", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(trash): %v", err)
	}
	for _, uuid := range []string{"trash-head", "live-proj", "live-todo"} {
		for _, task := range got {
			if task.UUID == uuid {
				t.Errorf("trash: %s should not be listed", uuid)
			}
		}
	}
}

// A trashed project carries its own area, so --area finds it; it has no parent
// project of its own, so --project can never match it.
func TestListTasksTrashProjectRowsAndFilters(t *testing.T) {
	d, fx := newFixture(t)

	fx.area("area-home", "Home", 1)
	fx.project("trash-proj", "Dropped project", 1, anytime(), inArea("area-home"), trashed())
	fx.project("trash-proj-done", "Shelved rebuild", 2, anytime(), inArea("area-home"), trashed(), status(model.StatusCompleted))
	fx.todo("trash-todo", "Dropped to-do", 3, anytime(), inArea("area-home"), trashed())

	// Live rows in the same area, which the view keeps out however the filter
	// is written.
	fx.project("live-proj", "Still going", 4, anytime(), inArea("area-home"))
	fx.todo("live-todo", "Still to do", 5, anytime(), inArea("area-home"))

	byArea, err := d.ListTasks("trash", TaskFilter{Area: "area-home"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(byArea), []string{"trash-proj", "trash-proj-done", "trash-todo"}) {
		t.Errorf("--area area-home: got %v", uuidsOf(byArea))
	}

	byUUID, err := d.ListTasks("trash", TaskFilter{Project: "trash-proj"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byUUID) != 0 {
		t.Errorf("--project trash-proj: got %v, want none", uuidsOf(byUUID))
	}
}

// A project takes a deadline exactly as a to-do does and `things projects`
// reports it, but the deadlines view was pinned to to-dos, so an agent
// sweeping what is due never saw a project deadline (issue #213).
func TestListTasksDeadlinesIncludeProjects(t *testing.T) {
	d, fx := newFixture(t)

	jun1 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)))
	jun2 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 2, 0, 0, 0, 0, time.Local)))
	jun3 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 3, 0, 0, 0, 0, time.Local)))

	// Interleaved by date but not by kind, and with "index" running against
	// the deadline order, so an ordering that fell back to "index" or grouped
	// by type would put these in a different sequence.
	fx.todo("dl-todo-early", "Renew the cert", 3, anytime(), deadline(jun1))
	fx.project("dl-proj-mid", "Migrate hosts", 2, anytime(), deadline(jun2))
	fx.todo("dl-todo-late", "File the form", 1, anytime(), deadline(jun3))

	got, err := d.ListTasks("deadlines", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(deadlines): %v", err)
	}
	want := []string{"dl-todo-early", "dl-proj-mid", "dl-todo-late"}
	if !sameSet(uuidsOf(got), want) {
		t.Errorf("deadlines: got %v, want %v", uuidsOf(got), want)
	}

	for _, task := range got {
		if task.UUID != "dl-proj-mid" {
			continue
		}
		if task.Type != model.TypeProject {
			t.Errorf("dl-proj-mid: got type %v, want %v", task.Type, model.TypeProject)
		}
	}
}

// Project rows are ordered with the to-dos by deadline rather than forming a
// block of their own, so the project falls between the two to-dos.
func TestListTasksDeadlinesOrderWithProject(t *testing.T) {
	d, fx := newFixture(t)

	jun1 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)))
	jun2 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 2, 0, 0, 0, 0, time.Local)))
	jun3 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 3, 0, 0, 0, 0, time.Local)))

	// Interleaved by date but not by kind, and with "index" running against
	// the deadline order, so an ordering that fell back to "index" or grouped
	// by type would put these in a different sequence.
	fx.todo("dl-todo-early", "Renew the cert", 3, anytime(), deadline(jun1))
	fx.project("dl-proj-mid", "Migrate hosts", 2, anytime(), deadline(jun2))
	fx.todo("dl-todo-late", "File the form", 1, anytime(), deadline(jun3))

	got, err := d.ListTasks("deadlines", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dl-todo-early", "dl-proj-mid", "dl-todo-late"}
	if !reflect.DeepEqual(uuidsOf(got), want) {
		t.Errorf("deadlines order: got %v, want %v", uuidsOf(got), want)
	}
}

// Widening deadlines to projects must not widen it past a deadline: a project
// without one, a closed one, a trashed one, a heading and a repeating project
// template with its children all stay out.
func TestListTasksDeadlinesProjectExclusions(t *testing.T) {
	d, fx := newFixture(t)

	jun1 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)))
	jun2 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 2, 0, 0, 0, 0, time.Local)))

	// A project with no deadline is not a deadlines row, and neither a closed
	// nor a trashed project is, however its deadline reads.
	fx.project("dl-proj-none", "No date", 4, anytime())
	fx.project("dl-proj-done", "Shipped", 5, anytime(), status(model.StatusCompleted), deadline(jun1))
	fx.project("dl-proj-trashed", "Binned", 6, anytime(), trashed(), deadline(jun1))

	// A heading is structure inside a project, never a list row.
	fx.project("dl-proj-mid", "Migrate hosts", 2, anytime(), deadline(jun2))
	fx.heading("dl-head", "Phase one", 7, deadline(jun1), inProject("dl-proj-mid"))

	// A repeating project template belongs to Repeating, not to the view its
	// deadline would otherwise put it in, and the to-dos inside it come with
	// it — they carry no rule of their own, only the project does
	// (issues #147, #171).
	fx.project("dl-proj-template", "Quarterly audit", 8, anytime(), deadline(jun1), repeats())
	fx.todo("dl-todo-in-template", "Pull the figures", 9, anytime(), deadline(jun1), inProject("dl-proj-template"))

	got, err := d.ListTasks("deadlines", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(deadlines): %v", err)
	}
	for _, uuid := range []string{
		"dl-proj-none", "dl-proj-done", "dl-proj-trashed", "dl-head",
		"dl-proj-template", "dl-todo-in-template",
	} {
		for _, task := range got {
			if task.UUID == uuid {
				t.Errorf("deadlines: %s should not be listed", uuid)
			}
		}
	}
}

// --on/--from/--to filter t.deadline on this view, and a project row is
// filtered by its own deadline like any other row.
func TestListTasksDeadlinesDateFilterMatchesProject(t *testing.T) {
	d, fx := newFixture(t)

	jun1 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)))
	jun2 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 2, 0, 0, 0, 0, time.Local)))
	jun3 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 3, 0, 0, 0, 0, time.Local)))

	// Interleaved by date but not by kind, and with "index" running against
	// the deadline order, so an ordering that fell back to "index" or grouped
	// by type would put these in a different sequence.
	fx.todo("dl-todo-early", "Renew the cert", 3, anytime(), deadline(jun1))
	fx.project("dl-proj-mid", "Migrate hosts", 2, anytime(), deadline(jun2))
	fx.todo("dl-todo-late", "File the form", 1, anytime(), deadline(jun3))

	on := model.ThingsDate(model.ThingsDateFromTime(time.Date(2026, 6, 2, 0, 0, 0, 0, time.Local)))
	got, err := d.ListTasks("deadlines", TaskFilter{On: &on})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(got), []string{"dl-proj-mid"}) {
		t.Errorf("deadlines --on: got %v, want [dl-proj-mid]", uuidsOf(got))
	}
}

// A project carries its own area, so --area finds it; it has no parent
// project, so --project can never match it.
func TestListTasksDeadlinesProjectRowsAndFilters(t *testing.T) {
	d, fx := newFixture(t)

	fx.area("area-ops", "Ops", 1)
	jun1 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)))
	jun2 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 2, 0, 0, 0, 0, time.Local)))
	jun3 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 3, 0, 0, 0, 0, time.Local)))

	// Interleaved by date but not by kind, and with "index" running against
	// the deadline order, so an ordering that fell back to "index" or grouped
	// by type would put these in a different sequence.
	fx.todo("dl-todo-early", "Renew the cert", 3, anytime(), deadline(jun1), inArea("area-ops"))
	fx.project("dl-proj-mid", "Migrate hosts", 2, anytime(), deadline(jun2), inArea("area-ops"))
	fx.todo("dl-todo-late", "File the form", 1, anytime(), deadline(jun3), inArea("area-ops"))

	byArea, err := d.ListTasks("deadlines", TaskFilter{Area: "area-ops"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(byArea), []string{"dl-todo-early", "dl-proj-mid", "dl-todo-late"}) {
		t.Errorf("--area area-ops: got %v", uuidsOf(byArea))
	}

	byUUID, err := d.ListTasks("deadlines", TaskFilter{Project: "dl-proj-mid"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byUUID) != 0 {
		t.Errorf("--project dl-proj-mid: got %v, want none", uuidsOf(byUUID))
	}
}

// The Logbook is where Things files everything closed, not just everything
// finished: a cancelled to-do or project is logged beside the completed ones,
// but the view filtered status = 3 alone, so cancelled items never appeared
// (issue #210).
func TestListTasksLogbookIncludesCancelled(t *testing.T) {
	d, fx := newFixture(t)

	// Stop dates interleave the two statuses, and "index" runs against that
	// order, so a view that grouped by status or fell back to "index" would
	// return these in a different sequence.
	oldest := model.TimeToUnix(time.Now().Add(-96 * time.Hour))
	older := model.TimeToUnix(time.Now().Add(-72 * time.Hour))
	newer := model.TimeToUnix(time.Now().Add(-48 * time.Hour))
	newest := model.TimeToUnix(time.Now().Add(-24 * time.Hour))

	fx.todo("log-todo-done", "Wrote it up", 4, anytime(), completed(newest))
	fx.project("log-proj-cancel", "Dropped work", 3, anytime(), cancelled(newer))
	fx.todo("log-todo-cancel", "Dropped task", 2, anytime(), cancelled(older))
	fx.project("log-proj-done", "Shipped it", 1, anytime(), completed(oldest))

	got, err := d.ListTasks("logbook", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(logbook): %v", err)
	}
	want := []string{"log-todo-done", "log-proj-cancel", "log-todo-cancel", "log-proj-done"}
	if !sameSet(uuidsOf(got), want) {
		t.Errorf("logbook: got %v, want %v", uuidsOf(got), want)
	}

	// status tells the two kinds of closure apart, for both types.
	wantStatus := map[string]model.Status{
		"log-todo-done":   model.StatusCompleted,
		"log-proj-done":   model.StatusCompleted,
		"log-todo-cancel": model.StatusCancelled,
		"log-proj-cancel": model.StatusCancelled,
	}
	for _, task := range got {
		if want, ok := wantStatus[task.UUID]; ok && task.Status != want {
			t.Errorf("%s: got status %v, want %v", task.UUID, task.Status, want)
		}
	}
	for _, uuid := range []string{"log-proj-done", "log-proj-cancel"} {
		for _, task := range got {
			if task.UUID == uuid && task.Type != model.TypeProject {
				t.Errorf("%s: got type %v, want %v", uuid, task.Type, model.TypeProject)
			}
		}
	}
}

// Cancelled rows are ordered by stopDate with the completed ones rather than
// forming a block of their own, newest first like the rest of the Logbook.
func TestListTasksLogbookCancelledOrder(t *testing.T) {
	d, fx := newFixture(t)

	// Stop dates interleave the two statuses, and "index" runs against that
	// order, so a view that grouped by status or fell back to "index" would
	// return these in a different sequence.
	oldest := model.TimeToUnix(time.Now().Add(-96 * time.Hour))
	older := model.TimeToUnix(time.Now().Add(-72 * time.Hour))
	newer := model.TimeToUnix(time.Now().Add(-48 * time.Hour))
	newest := model.TimeToUnix(time.Now().Add(-24 * time.Hour))

	fx.todo("log-todo-done", "Wrote it up", 4, anytime(), completed(newest))
	fx.project("log-proj-cancel", "Dropped work", 3, anytime(), cancelled(newer))
	fx.todo("log-todo-cancel", "Dropped task", 2, anytime(), cancelled(older))
	fx.project("log-proj-done", "Shipped it", 1, anytime(), completed(oldest))

	got, err := d.ListTasks("logbook", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"log-todo-done", "log-proj-cancel", "log-todo-cancel", "log-proj-done"}
	if !reflect.DeepEqual(uuidsOf(got), want) {
		t.Errorf("logbook order: got %v, want %v", uuidsOf(got), want)
	}
}

// Widening the Logbook to cancelled must not widen it to everything closed:
// open rows, a trashed cancelled row and a cancelled heading stay out.
func TestListTasksLogbookCancelledExclusions(t *testing.T) {
	d, fx := newFixture(t)

	older := model.TimeToUnix(time.Now().Add(-72 * time.Hour))

	// Open rows are not logged, a trashed row belongs to trash however it was
	// closed, and a heading is structure inside a project, never a list row.
	fx.todo("log-open", "Still going", 5, anytime())
	fx.todo("log-cancel-trashed", "Binned", 6, anytime(), trashed(), cancelled(older))
	fx.project("log-proj-done", "Shipped it", 1, anytime(), completed(older))
	fx.heading("log-head", "Phase one", 7, cancelled(older), inProject("log-proj-done"))

	got, err := d.ListTasks("logbook", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(logbook): %v", err)
	}
	for _, uuid := range []string{"log-open", "log-cancel-trashed", "log-head"} {
		for _, task := range got {
			if task.UUID == uuid {
				t.Errorf("logbook: %s should not be listed", uuid)
			}
		}
	}
}

// A cancelled row is filtered like any other: --area finds it through the area
// it carries, and --project cannot match a project row.
func TestListTasksLogbookCancelledFilters(t *testing.T) {
	d, fx := newFixture(t)

	fx.area("area-lab", "Lab", 1)
	// Stop dates interleave the two statuses, and "index" runs against that
	// order, so a view that grouped by status or fell back to "index" would
	// return these in a different sequence.
	oldest := model.TimeToUnix(time.Now().Add(-96 * time.Hour))
	older := model.TimeToUnix(time.Now().Add(-72 * time.Hour))
	newer := model.TimeToUnix(time.Now().Add(-48 * time.Hour))
	newest := model.TimeToUnix(time.Now().Add(-24 * time.Hour))

	fx.todo("log-todo-done", "Wrote it up", 4, anytime(), completed(newest), inArea("area-lab"))
	fx.project("log-proj-cancel", "Dropped work", 3, anytime(), cancelled(newer), inArea("area-lab"))
	fx.todo("log-todo-cancel", "Dropped task", 2, anytime(), cancelled(older), inArea("area-lab"))
	fx.project("log-proj-done", "Shipped it", 1, anytime(), completed(oldest), inArea("area-lab"))

	byArea, err := d.ListTasks("logbook", TaskFilter{Area: "area-lab"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"log-todo-done", "log-proj-cancel", "log-todo-cancel", "log-proj-done"}
	if !sameSet(uuidsOf(byArea), want) {
		t.Errorf("--area area-lab: got %v, want %v", uuidsOf(byArea), want)
	}

	byUUID, err := d.ListTasks("logbook", TaskFilter{Project: "log-proj-cancel"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byUUID) != 0 {
		t.Errorf("--project log-proj-cancel: got %v, want none", uuidsOf(byUUID))
	}
}

// The cancelled widening must not reach the views that select open rows: a
// cancelled to-do is not in today, anytime, someday, upcoming or the catch-all.
func TestListTasksCancelledStaysOutOfOpenViews(t *testing.T) {
	d, fx := newFixture(t)

	// Stop dates interleave the two statuses, and "index" runs against that
	// order, so a view that grouped by status or fell back to "index" would
	// return these in a different sequence.
	oldest := model.TimeToUnix(time.Now().Add(-96 * time.Hour))
	older := model.TimeToUnix(time.Now().Add(-72 * time.Hour))
	newer := model.TimeToUnix(time.Now().Add(-48 * time.Hour))
	newest := model.TimeToUnix(time.Now().Add(-24 * time.Hour))

	fx.todo("log-todo-done", "Wrote it up", 4, anytime(), completed(newest))
	fx.project("log-proj-cancel", "Dropped work", 3, anytime(), cancelled(newer))
	fx.todo("log-todo-cancel", "Dropped task", 2, anytime(), cancelled(older))
	fx.project("log-proj-done", "Shipped it", 1, anytime(), completed(oldest))

	// The logbook seed rows are all start = 1 with no startDate, so on their
	// own they could never appear in today, upcoming or someday whatever their
	// status — the assertion below would hold even if those views stopped
	// filtering on status. Seed a cancelled row shaped to land in each of
	// them, so only `t.status = 0` keeps it out.
	today := int64(model.ThingsDateFromTime(time.Now()))
	tomorrow := today + (1 << 7)
	stopped := model.TimeToUnix(time.Now().Add(-24 * time.Hour))
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, startDate, stopDate, "index") VALUES
		('cancel-today',    'Was due today', 0, 2, 0, 1, 0, ?,    ?, 8),
		('cancel-upcoming', 'Was scheduled', 0, 2, 0, 2, 0, ?,    ?, 9),
		('cancel-someday',  'Was deferred',  0, 2, 0, 2, 0, NULL, ?, 10)`,
		today, stopped, tomorrow, stopped, stopped)

	for _, view := range []string{"today", "upcoming", "anytime", "someday", "project"} {
		got, err := d.ListTasks(view, TaskFilter{})
		if err != nil {
			t.Fatalf("ListTasks(%q): %v", view, err)
		}
		for _, task := range got {
			if task.Status == model.StatusCancelled {
				t.Errorf("view %q: cancelled row %s should not be listed", view, task.UUID)
			}
		}
	}
}

// Ties are the whole point of these seeds: every row shares its view's natural
// sort key with a sibling, and the "index" that breaks the tie runs against the
// uuid order the grouping happens to produce, so a view with no tiebreak lists
// them in the wrong order. cacheTaskUUIDs numbers a listing by position, so an
// order SQLite leaves undefined lets two identical listings number the same
// rows differently and `things complete 3` act on a row the user never read
// (issue #221).

func TestListTasksViewOrderIsTotalOnTiedKeys(t *testing.T) {
	jun1 := int64(model.ThingsDateFromTime(time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)))

	cases := []struct {
		view string
		seed func(fx *fixture)
		want []string
	}{
		// Three rows on one deadline, the shape issue #221 was filed on: two
		// of them tie on "index" as well and only the uuid separates those.
		{"deadlines", func(fx *fixture) {
			fx.todo("tie-dl-a", "Renew the cert", 5, anytime(), deadline(jun1))
			fx.todo("tie-dl-b", "File the form", 5, anytime(), deadline(jun1))
			fx.todo("tie-dl-c", "Pay the invoice", 1, anytime(), deadline(jun1))
		}, []string{"tie-dl-c", "tie-dl-a", "tie-dl-b"}},

		{"logbook", func(fx *fixture) {
			const stop = 780000000.0
			fx.todo("tie-lb-a", "Logged first", 5, completed(stop))
			fx.todo("tie-lb-b", "Logged second", 5, completed(stop))
			fx.todo("tie-lb-c", "Logged third", 1, completed(stop))
		}, []string{"tie-lb-c", "tie-lb-a", "tie-lb-b"}},

		{"today", func(fx *fixture) {
			fx.todo("tie-td-a", "Stand up", 5, anytimeOn(jun1), todayIndex(1), todayIndexRef(1))
			fx.todo("tie-td-b", "Sit down", 5, anytimeOn(jun1), todayIndex(1), todayIndexRef(1))
			fx.todo("tie-td-c", "Walk about", 1, anytimeOn(jun1), todayIndex(1), todayIndexRef(1))
		}, []string{"tie-td-c", "tie-td-a", "tie-td-b"}},

		// Rows filed nowhere, tied on "index": anytime groups before it orders
		// by index, and these rows share every grouping key, so they fall
		// through to the index — which they also tie on, leaving only the
		// uuid. someday takes the same grouping and reaches the same place;
		// inbox and trash reach it through the default index ordering.
		{"anytime", func(fx *fixture) {
			fx.todo("tie-any-b", "Second", 1, anytime())
			fx.todo("tie-any-a", "First", 1, anytime())
		}, []string{"tie-any-a", "tie-any-b"}},
	}

	for _, tc := range cases {
		t.Run(tc.view, func(t *testing.T) {
			d, fx := newFixture(t)
			tc.seed(fx)

			first, err := d.ListTasks(tc.view, TaskFilter{})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(uuidsOf(first), tc.want) {
				t.Errorf("%s order: got %v, want %v", tc.view, uuidsOf(first), tc.want)
			}

			// The same listing read twice has to number the same rows the
			// same way, which is the contract the write commands rely on.
			second, err := d.ListTasks(tc.view, TaskFilter{})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(uuidsOf(second), uuidsOf(first)) {
				t.Errorf("%s reread: got %v, want %v", tc.view, uuidsOf(second), uuidsOf(first))
			}
		})
	}
}

// A search listing is numbered out of the same cache, so it needs the same
// total order.
func TestSearchTasksOrderIsTotalOnTiedIndex(t *testing.T) {
	d := newTestDB(t)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, start, startBucket, "index") VALUES
		('tie-any-b', 'Rotate the keys again', 0, 0, 0, 1, 0, 1),
		('tie-any-a', 'Rotate the keys',       0, 0, 0, 1, 0, 1)`)

	got, err := d.SearchTasks("Rotate the keys")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tie-any-a", "tie-any-b"}
	if !reflect.DeepEqual(uuidsOf(got), want) {
		t.Errorf("search order: got %v, want %v", uuidsOf(got), want)
	}
}

// The seeded-tie tests above cannot catch a missing uuid tiebreak on their own:
// GROUP BY t.uuid already leaves SQLite returning tied rows in uuid order, so
// they pass either way. Assert the tiebreak on the SQL instead, where its
// absence is visible.
func TestEveryTaskOrderingEndsInTheUUIDTiebreak(t *testing.T) {
	d := newTestDB(t)

	// Spelled out rather than taken from uuidTiebreak: a test that compared
	// the orderings against the constant would still pass if the constant
	// itself were emptied.
	const wantSuffix = `, t.uuid ASC`
	if uuidTiebreak != wantSuffix {
		t.Errorf("uuidTiebreak = %q, want %q", uuidTiebreak, wantSuffix)
	}

	orderings := map[string]string{
		"default":            indexOrderBy,
		"templatesLastOrder": d.templatesLastOrder(),
	}
	for view, spec := range views {
		orderings["view "+view] = spec.orderBy
	}
	for name, orderBy := range orderings {
		if !strings.HasSuffix(orderBy, wantSuffix) {
			t.Errorf("%s ordering %q does not end in %q", name, orderBy, wantSuffix)
		}
	}
}

// Things' Someday list is the deferred things not filed under a project. A
// to-do inside a project stays inside it however it is deferred, so the app
// keeps it out of the global list while the CLI returned it (issue #211).
//
// The rule is the presence of a parent project, not the parent's bucket. That
// was measured, not assumed: a probe project created in Someday through the
// app, holding one Someday to-do, put the project in the app's Someday list
// and left the child out.
func TestListTasksSomedayExcludesProjectChildren(t *testing.T) {
	d, fx := newFixture(t)

	// Two parent projects in different buckets. The Someday one is the case
	// that separates "no parent project" from "no parent project outside
	// Someday": measured against Things, the app hides that child too.
	fx.area("area-den", "Den", 1)
	fx.project("sd-parent-anytime", "Anytime project", 1, anytime(), inArea("area-den"))
	fx.project("sd-parent-someday", "Someday project", 2, someday(), inArea("area-den"))

	// Someday to-dos: one under each parent, one under a heading of the
	// Anytime parent, and two with no parent project at all.
	fx.todo("sd-in-anytime", "Filed under anytime", 3, someday(), inProject("sd-parent-anytime"))
	fx.todo("sd-in-someday", "Filed under someday", 4, someday(), inProject("sd-parent-someday"))
	fx.heading("sd-head", "Phase one", 5, inProject("sd-parent-anytime"))
	fx.todo("sd-under-head", "Filed under a heading", 6, someday(), underHeading("sd-head"))
	fx.todo("sd-loose-area", "Deferred in an area", 7, someday(), inArea("area-den"))
	fx.todo("sd-loose", "Deferred on its own", 8, someday())

	got, err := d.ListTasks("someday", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(someday): %v", err)
	}
	want := []string{"sd-parent-someday", "sd-loose-area", "sd-loose"}
	if !sameSet(uuidsOf(got), want) {
		t.Errorf("someday: got %v, want %v", uuidsOf(got), want)
	}
}

// The discriminating case, called out on its own because it is the one the
// issue guessed wrong: a Someday to-do whose parent project is itself in
// Someday is still hidden, while the parent project lists.
func TestListTasksSomedayHidesChildOfSomedayProject(t *testing.T) {
	d, fx := newFixture(t)

	fx.project("sd-parent-someday", "Someday project", 2, someday())
	fx.todo("sd-in-someday", "Filed under someday", 4, someday(), inProject("sd-parent-someday"))

	got, err := d.ListTasks("someday", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	uuids := uuidsOf(got)

	var sawParent, sawChild bool
	for _, u := range uuids {
		switch u {
		case "sd-parent-someday":
			sawParent = true
		case "sd-in-someday":
			sawChild = true
		}
	}
	if !sawParent {
		t.Errorf("someday: parent project sd-parent-someday should list, got %v", uuids)
	}
	if sawChild {
		t.Errorf("someday: sd-in-someday sits inside a Someday project and should not list, got %v", uuids)
	}
}

// A to-do filed under a project heading carries a NULL project and reaches its
// project through the heading, so it must be excluded by the same rule rather
// than slipping through as unparented.
func TestListTasksSomedayExcludesHeadingNestedChildren(t *testing.T) {
	d, fx := newFixture(t)

	fx.project("sd-parent-anytime", "Anytime project", 1, anytime())
	fx.heading("sd-head", "Phase one", 5, inProject("sd-parent-anytime"))
	fx.todo("sd-under-head", "Filed under a heading", 6, someday(), underHeading("sd-head"))

	got, err := d.ListTasks("someday", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range got {
		if task.UUID == "sd-under-head" {
			t.Errorf("someday: sd-under-head reaches a project through its heading and should not list")
		}
	}
}

// Narrowing someday must not empty it: a project row and an unparented to-do
// both still list, and --area still finds them through the area they carry.
func TestListTasksSomedayKeepsUnparentedRows(t *testing.T) {
	d, fx := newFixture(t)

	fx.area("area-den", "Den", 1)
	fx.project("sd-parent-anytime", "Anytime project", 1, anytime(), inArea("area-den"))
	fx.project("sd-parent-someday", "Someday project", 2, someday(), inArea("area-den"))
	fx.todo("sd-in-anytime", "Filed under anytime", 3, someday(), inProject("sd-parent-anytime"))
	fx.todo("sd-loose-area", "Deferred in an area", 7, someday(), inArea("area-den"))
	fx.todo("sd-loose", "Deferred on its own", 8, someday())

	byArea, err := d.ListTasks("someday", TaskFilter{Area: "area-den"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(uuidsOf(byArea), []string{"sd-parent-someday", "sd-loose-area"}) {
		t.Errorf("--area area-den: got %v, want [sd-parent-someday sd-loose-area]", uuidsOf(byArea))
	}

	// --project can never match on this view now: every row that survives has
	// no parent project. The CLI rejects the combination outright rather than
	// print an empty list (see TestRunListSomedayRejectsProjectFilter); the
	// query layer stays literal and simply matches nothing.
	byProject, err := d.ListTasks("someday", TaskFilter{Project: "sd-parent-anytime"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byProject) != 0 {
		t.Errorf("--project sd-parent-anytime: got %v, want none", uuidsOf(byProject))
	}
}

// The narrowing is someday-only: the same parented to-do still lists in the
// other views, which show a project's contents.
func TestListTasksProjectChildrenStayInOtherViews(t *testing.T) {
	d, fx := newFixture(t)

	fx.project("sd-parent-anytime", "Anytime project", 1, anytime())
	fx.todo("sd-in-anytime", "Filed under anytime", 3, someday(), inProject("sd-parent-anytime"))

	// The seeded children are all someday-shaped (start=2, no startDate), so
	// none of them can appear in another view as they stand. Seed one more
	// child of the same parent in anytime shape to prove the guard is
	// someday-only rather than global.
	fx.todo("sd-anytime-child", "Inside, anytime", 9, anytime(), inProject("sd-parent-anytime"))

	got, err := d.ListTasks("anytime", TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, task := range got {
		if task.UUID == "sd-anytime-child" {
			found = true
		}
	}
	if !found {
		t.Errorf("anytime: sd-anytime-child should still list, got %v", uuidsOf(got))
	}
}
