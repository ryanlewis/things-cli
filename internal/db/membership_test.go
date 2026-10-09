package db

import (
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/model"
)

// The membership checks below were each measured against the live app on
// 9 Oct 2026: which rows a view holds, not the order it holds them in.

// listSet runs a view and returns its uuids, failing the test on error.
func listSet(t *testing.T, d *DB, view string, opts TaskFilter) []string {
	t.Helper()
	got := mustList(t, d, view, opts)
	return uuidsOf(got)
}

// A project with a deadline and no start date is in Today once the deadline
// is today or past, and in Upcoming under the deadline's day while it is
// later, as a to-do of that shape is.
func TestDeadlineProjectInTodayAndUpcoming(t *testing.T) {
	d, fx := newFixture(t)
	today := int64(model.ThingsDateFromTime(testNow))
	earlier := int64(model.ThingsDateFromTime(testNow.AddDate(0, 0, -2)))
	later := int64(model.ThingsDateFromTime(testNow.AddDate(0, 0, 3)))

	fx.Project("p-due-today", "Due today", 1, anytime(), deadline(today))
	fx.Project("p-overdue", "Overdue", 2, anytime(), deadline(earlier))
	fx.Project("p-due-later", "Due later", 3, anytime(), deadline(later))
	fx.Project("p-suppressed", "Taken out of Today", 4, anytime(), deadline(earlier), suppressed(earlier))
	fx.Project("p-no-deadline", "No deadline", 5, anytime())

	if got, want := listSet(t, d, "today", TaskFilter{}), []string{"p-due-today", "p-overdue"}; !sameSet(got, want) {
		t.Errorf("today = %v, want %v", got, want)
	}
	if got, want := listSet(t, d, "upcoming", TaskFilter{}), []string{"p-due-later"}; !sameSet(got, want) {
		t.Errorf("upcoming = %v, want %v", got, want)
	}
	// Anytime and the Inbox carry to-dos only, so the project reaches Today
	// alone.
	for _, view := range []string{"anytime", "inbox"} {
		if got := listSet(t, d, view, TaskFilter{}); len(got) != 0 {
			t.Errorf("%s = %v, want empty", view, got)
		}
	}
}

// A Someday to-do or project with a later deadline is in Upcoming under the
// deadline's day, and one due today is in Today, as an Anytime one is, and
// out of Someday. A deadline suppressed for Today keeps it out, whatever the
// bucket.
func TestSomedayDeadlineInUpcomingAndToday(t *testing.T) {
	d, fx := newFixture(t)
	today := int64(model.ThingsDateFromTime(testNow))
	later := int64(model.ThingsDateFromTime(testNow.AddDate(0, 0, 3)))

	fx.Todo("t-someday-later", "Someday to-do due later", 1, someday(), deadline(later))
	fx.Project("p-someday-later", "Someday project due later", 2, someday(), deadline(later))
	fx.Todo("t-someday-today", "Someday to-do due today", 3, someday(), deadline(today))
	fx.Project("p-someday-today", "Someday project due today", 4, someday(), deadline(today))
	fx.Todo("t-someday-suppressed", "Someday to-do taken out of Today", 5, someday(), deadline(today), suppressed(today))
	// To-dos due today inside a Someday project and inside an Anytime one.
	fx.Project("p-someday", "Someday project", 6, someday())
	fx.Project("p-anytime", "Anytime project", 7, anytime())
	fx.Todo("t-in-someday-proj", "In a Someday project", 8, anytime(), deadline(today), inProject("p-someday"))
	fx.Todo("t-in-anytime-proj", "In an Anytime project", 9, anytime(), deadline(today), inProject("p-anytime"))

	if got, want := listSet(t, d, "upcoming", TaskFilter{}), []string{"t-someday-later", "p-someday-later"}; !sameSet(got, want) {
		t.Errorf("upcoming = %v, want %v", got, want)
	}
	if got, want := listSet(t, d, "today", TaskFilter{}), []string{"t-someday-today", "p-someday-today", "t-in-someday-proj", "t-in-anytime-proj"}; !sameSet(got, want) {
		t.Errorf("today = %v, want %v", got, want)
	}
	// --on matches the Upcoming rows on their deadline day.
	on := model.ThingsDate(later)
	if got, want := listSet(t, d, "upcoming", TaskFilter{On: &on}), []string{"t-someday-later", "p-someday-later"}; !sameSet(got, want) {
		t.Errorf("upcoming --on = %v, want %v", got, want)
	}
	// The deadline promotes a Someday to-do into Anytime as well, and out of
	// Someday, as it does an Inbox one; a suppressed one stays in Someday.
	// The Anytime project's to-do is in Anytime by its bucket.
	if got, want := listSet(t, d, "anytime", TaskFilter{}), []string{"t-someday-today", "t-in-anytime-proj"}; !sameSet(got, want) {
		t.Errorf("anytime = %v, want %v", got, want)
	}
	if got, want := listSet(t, d, "someday", TaskFilter{}), []string{"t-someday-later", "p-someday-later", "t-someday-suppressed", "p-someday"}; !sameSet(got, want) {
		t.Errorf("someday = %v, want %v", got, want)
	}
}

// A bare --tag lists the tagged rows closed today and not yet logged, as
// today, anytime and --project do, and --open-only drops them.
func TestTagListsClosedUnloggedRows(t *testing.T) {
	d, fx := newFixture(t)
	stopToday := model.TimeToUnix(testNow)
	stopYesterday := model.TimeToUnix(testNow.Add(-25 * time.Hour))

	fx.Tag("tg-urgent", "urgent", 1)
	fx.Todo("t-open", "Open", 1, anytime())
	fx.Todo("t-done-today", "Done today", 2, anytime(), completed(stopToday))
	fx.Todo("t-dropped-today", "Dropped today", 3, anytime(), cancelled(stopToday))
	fx.Todo("t-done-yesterday", "Done yesterday", 4, anytime(), completed(stopYesterday))
	fx.Todo("t-trashed-today", "Trashed", 5, anytime(), completed(stopToday), trashed())
	fx.Project("p-done-today", "Project done today", 6, anytime(), completed(stopToday))
	fx.Todo("t-untagged", "Untagged", 7, anytime(), completed(stopToday))
	for _, uuid := range []string{"t-open", "t-done-today", "t-dropped-today", "t-done-yesterday", "t-trashed-today", "p-done-today"} {
		fx.Tagged(uuid, "tg-urgent")
	}

	got := listSet(t, d, ViewProject, TaskFilter{Tag: "urgent"})
	if want := []string{"t-open", "t-done-today", "t-dropped-today", "p-done-today"}; !sameSet(got, want) {
		t.Errorf("--tag = %v, want %v", got, want)
	}
	got = listSet(t, d, ViewProject, TaskFilter{Tag: "urgent", OpenOnly: true})
	if want := []string{"t-open"}; !sameSet(got, want) {
		t.Errorf("--tag --open-only = %v, want %v", got, want)
	}
}

// Under a tag, a to-do of a project closed today stays in place, as the lists
// keep it, rather than folding into the project's row the way an area's
// page folds it.
func TestTagKeepsToDosOfProjectClosedToday(t *testing.T) {
	d, fx := newFixture(t)
	stopToday := model.TimeToUnix(testNow)

	fx.Area("area-work", "Work", 1)
	fx.Tag("tg-urgent", "urgent", 1)
	fx.Project("p-done", "Done today", 1, anytime(), inArea("area-work"), completed(stopToday))
	fx.Todo("t-child", "Child closed today", 2, anytime(), inProject("p-done"), completed(stopToday))
	fx.Tagged("t-child", "tg-urgent")

	if got, want := listSet(t, d, ViewProject, TaskFilter{Tag: "urgent"}), []string{"t-child"}; !sameSet(got, want) {
		t.Errorf("--tag = %v, want %v", got, want)
	}
	// With --area as well, the area's page folds the to-do into its
	// project's row, and the tag reaches that row through the to-do.
	if got, want := listSet(t, d, ViewProject, TaskFilter{Tag: "urgent", Area: "Work"}), []string{"p-done"}; !sameSet(got, want) {
		t.Errorf("--tag --area = %v, want %v", got, want)
	}
	if got := listSet(t, d, ViewProject, TaskFilter{Tag: "urgent", Area: "Work", OpenOnly: true}); len(got) != 0 {
		t.Errorf("--tag --area --open-only = %v, want empty", got)
	}
}

// Only a closed project takes its tagged to-do's place. A to-do closed today
// inside an open project is not folded, so it is its own row and the project
// is not matched; nor is a closed project whose tagged to-do was logged on an
// earlier day, or is still open.
func TestTagAreaMatchesOnlyFoldedProjects(t *testing.T) {
	d, fx := newFixture(t)
	stopToday := model.TimeToUnix(testNow)
	stopYesterday := model.TimeToUnix(testNow.Add(-25 * time.Hour))

	fx.Area("area-work", "Work", 1)
	fx.Tag("tg-urgent", "urgent", 1)
	fx.Project("p-open", "Open", 1, anytime(), inArea("area-work"))
	fx.Todo("t-open-proj-done", "Done in open project", 2, anytime(), inProject("p-open"), completed(stopToday))
	fx.Project("p-done-a", "Done, child logged", 3, anytime(), inArea("area-work"), completed(stopToday))
	fx.Todo("t-logged", "Logged yesterday", 4, anytime(), inProject("p-done-a"), completed(stopYesterday))
	fx.Project("p-done-b", "Done, child open", 5, anytime(), inArea("area-work"), completed(stopToday))
	fx.Todo("t-still-open", "Still open", 6, anytime(), inProject("p-done-b"))
	fx.Project("p-done-c", "Done, child under heading", 7, anytime(), inArea("area-work"), completed(stopToday))
	fx.Heading("h-c", "Heading", 8, inProject("p-done-c"))
	fx.Todo("t-under-heading", "Under heading", 9, anytime(), underHeading("h-c"), completed(stopToday))
	for _, uuid := range []string{"t-open-proj-done", "t-logged", "t-still-open", "t-under-heading"} {
		fx.Tagged(uuid, "tg-urgent")
	}

	got := listSet(t, d, ViewProject, TaskFilter{Tag: "urgent", Area: "Work"})
	if want := []string{"t-open-proj-done", "t-still-open", "p-done-c"}; !sameSet(got, want) {
		t.Errorf("--tag --area = %v, want %v", got, want)
	}
}

// search skips a to-do whose project is in the Trash, reached directly or
// through a heading, as the lists and title lookups do.
func TestSearchSkipsTrashedProjectChildren(t *testing.T) {
	d, fx := newFixture(t)

	fx.Project("p-binned", "Binned", 1, anytime(), trashed())
	fx.Heading("h-binned", "Heading", 2, inProject("p-binned"))
	fx.Project("p-live", "Live", 3, anytime())
	fx.Todo("t-direct", "needle direct", 4, anytime(), inProject("p-binned"))
	fx.Todo("t-heading", "needle under heading", 5, anytime(), underHeading("h-binned"))
	fx.Todo("t-live", "needle live", 6, anytime(), inProject("p-live"))
	fx.Todo("t-loose", "needle loose", 7, anytime())
	fx.Todo("t-trashed", "needle trashed", 8, anytime(), trashed())

	got, err := d.SearchTasks("needle")
	if err != nil {
		t.Fatal(err)
	}
	assertSet(t, got, []string{"t-live", "t-loose"}, "search")
}

// The other list views already leave a trashed project's to-dos out, through
// untrashedParent. Pinned here so search and the lists cannot drift apart.
func TestListViewsSkipTrashedProjectChildren(t *testing.T) {
	d, fx := newFixture(t)
	later := int64(model.ThingsDateFromTime(testNow.AddDate(0, 0, 3)))

	fx.Area("area-work", "Work", 1)
	fx.Tag("tg-urgent", "urgent", 1)
	fx.Project("p-binned", "Binned", 1, anytime(), inArea("area-work"), trashed())
	fx.Todo("t-anytime", "Anytime", 2, anytime(), inProject("p-binned"))
	fx.Todo("t-upcoming", "Upcoming", 3, somedayOn(later), inProject("p-binned"))
	fx.Todo("t-someday", "Someday", 4, someday(), inProject("p-binned"))
	for _, uuid := range []string{"t-anytime", "t-upcoming", "t-someday"} {
		fx.Tagged(uuid, "tg-urgent")
	}

	for _, tc := range []struct {
		view string
		opts TaskFilter
	}{
		{"anytime", TaskFilter{}},
		{"upcoming", TaskFilter{}},
		{"someday", TaskFilter{}},
		{ViewProject, TaskFilter{Area: "Work"}},
		{ViewProject, TaskFilter{Tag: "urgent"}},
	} {
		if got := listSet(t, d, tc.view, tc.opts); len(got) != 0 {
			t.Errorf("%s %+v = %v, want empty", tc.view, tc.opts, got)
		}
	}
}

// `things projects` lists a project closed today and not yet logged, as the
// app's project list does; --open-only drops it, and --completed still lists
// every closed project.
func TestListProjectsClosedUnlogged(t *testing.T) {
	d, fx := newFixture(t)
	stopToday := model.TimeToUnix(testNow)
	stopYesterday := model.TimeToUnix(testNow.Add(-25 * time.Hour))

	fx.Project("p-open", "Open", 1, anytime())
	fx.Project("p-done-today", "Done today", 2, anytime(), completed(stopToday))
	fx.Project("p-dropped-today", "Dropped today", 3, anytime(), cancelled(stopToday))
	fx.Project("p-done-yesterday", "Done yesterday", 4, anytime(), completed(stopYesterday))
	fx.Project("p-trashed", "Trashed today", 5, anytime(), completed(stopToday), trashed())

	uuids := func(includeCompleted, openOnly bool) []string {
		t.Helper()
		ps, err := d.ListProjects("", includeCompleted, openOnly)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(ps))
		for i, p := range ps {
			out[i] = p.UUID
		}
		return out
	}
	if got, want := uuids(false, false), []string{"p-open", "p-done-today", "p-dropped-today"}; !sameSet(got, want) {
		t.Errorf("projects = %v, want %v", got, want)
	}
	if got, want := uuids(false, true), []string{"p-open"}; !sameSet(got, want) {
		t.Errorf("projects --open-only = %v, want %v", got, want)
	}
	all := []string{"p-open", "p-done-today", "p-dropped-today", "p-done-yesterday"}
	for _, openOnly := range []bool{false, true} {
		if got := uuids(true, openOnly); !sameSet(got, all) {
			t.Errorf("projects --completed (open-only %v) = %v, want %v", openOnly, got, all)
		}
	}

	// "Log Completed Now" files them, and the listing drops them with it.
	fx.LogSettings(nil, model.TimeToUnix(testNow.Add(time.Minute)))
	if got, want := uuids(false, false), []string{"p-open"}; !sameSet(got, want) {
		t.Errorf("projects after log = %v, want %v", got, want)
	}
}
