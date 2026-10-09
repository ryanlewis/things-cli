package db

import (
	"testing"

	"github.com/ryanlewis/things-cli/internal/model"
)

// The app's This Evening section comes after every day row of Today, and its
// rows group the way the day rows do. Measured on 9 Oct 2026: six evening
// rows, an evening project among them, were the last six of the app's Today,
// where the CLI had them inside the no-area group.
func TestTodayListsEveningRowsLast(t *testing.T) {
	d, fx := newFixture(t)
	today := int64(model.ThingsDateFromTime(testNow))
	fx.Area("ar", "Work", -10)

	fx.Todo("eve-loose", "Evening loose", 1, evening(today), todayIndexRef(today), todayIndex(-500))
	fx.Project("eve-proj", "Evening project", 2, evening(today), todayIndexRef(today), todayIndex(-400))
	fx.Todo("eve-area", "Evening in area", 3, evening(today), todayIndexRef(today), todayIndex(-900), inArea("ar"))
	fx.Todo("day-loose", "Day loose", 4, anytimeOn(today), todayIndexRef(today), todayIndex(100))
	fx.Todo("day-area", "Day in area", 5, anytimeOn(today), todayIndexRef(today), todayIndex(200), inArea("ar"))

	got := mustList(t, d, ViewToday, TaskFilter{})
	want := []string{"day-loose", "day-area", "eve-loose", "eve-proj", "eve-area"}
	assertOrder(t, got, want, "today")
}

// Project groups in Today follow the project's schedule before its index: an
// Anytime project's to-dos, then a later-scheduled project's, then a Someday
// project's. Measured on 9 Oct 2026, the app listed the to-dos of a project
// scheduled for a later day (index 0) ahead of a Someday project's (index
// -369), and those ahead of another Someday project's (index 0), where the
// CLI ordered the groups by index. Inferred from that one ordering.
func TestTodayOrdersProjectGroupsBySchedule(t *testing.T) {
	d, fx := newFixture(t)
	today := int64(model.ThingsDateFromTime(testNow))
	soon := int64(model.ThingsDateFromTime(testNow.AddDate(0, 0, 3)))
	later := int64(model.ThingsDateFromTime(testNow.AddDate(0, 0, 6)))

	fx.Project("p-someday-b", "Someday B", 0, someday())
	fx.Project("p-later", "Later", 0, somedayOn(later))
	fx.Project("p-someday-a", "Someday A", -369, someday())
	fx.Project("p-soon", "Soon", 50, somedayOn(soon))
	fx.Project("p-anytime", "Anytime", 99, anytime())
	// todayIndex runs against the expected group order, so a group order
	// read off the children would fail.
	fx.Todo("c-someday-b", "SB", 0, anytimeOn(today), todayIndexRef(today), todayIndex(1), inProject("p-someday-b"))
	fx.Todo("c-later", "L", 0, anytimeOn(today), todayIndexRef(today), todayIndex(2), inProject("p-later"))
	fx.Todo("c-someday-a", "SA", 0, anytimeOn(today), todayIndexRef(today), todayIndex(3), inProject("p-someday-a"))
	fx.Todo("c-soon", "S", 0, anytimeOn(today), todayIndexRef(today), todayIndex(4), inProject("p-soon"))
	fx.Todo("c-anytime", "A", 0, anytimeOn(today), todayIndexRef(today), todayIndex(5), inProject("p-anytime"))

	got := mustList(t, d, ViewToday, TaskFilter{})
	want := []string{"c-anytime", "c-soon", "c-later", "c-someday-a", "c-someday-b"}
	assertOrder(t, got, want, "today")
}

// Anytime orders a project's to-dos the way the project's page does: those
// under no heading first, then each heading's in heading order. Measured on 9
// Oct 2026, the app listed them so where the CLI ordered by index alone.
func TestAnytimeOrdersProjectTodosByHeading(t *testing.T) {
	d, fx := newFixture(t)
	fx.Project("proj", "Ship", 1, anytime())
	fx.Heading("h1", "Phase one", 1, inProject("proj"))
	fx.Heading("h2", "Phase two", 2, inProject("proj"))
	fx.Todo("h1-a", "H1a", -411, anytime(), underHeading("h1"))
	fx.Todo("h2-a", "H2a", -300, anytime(), underHeading("h2"))
	fx.Todo("h1-b", "H1b", 0, anytime(), underHeading("h1"))
	fx.Todo("plain-a", "Plain a", 0, anytime(), inProject("proj"))
	fx.Todo("plain-b", "Plain b", 398, anytime(), inProject("proj"))

	got := mustList(t, d, ViewAnytime, TaskFilter{})
	want := []string{"plain-a", "plain-b", "h1-a", "h1-b", "h2-a"}
	assertOrder(t, got, want, "anytime")
}

// Someday opens each group with its project rows, then the loose to-dos.
// Measured on 9 Oct 2026, the app's Someday opened with its three no-area
// projects, where the CLI listed them after the to-dos by index.
func TestSomedayListsProjectRowsFirst(t *testing.T) {
	d, fx := newFixture(t)
	fx.Area("ar", "Personal", -3070)
	fx.Todo("t1", "T1", -32250, someday())
	fx.Todo("t2", "T2", -28307, someday())
	fx.Project("p1", "P1", -964, someday())
	fx.Project("p2", "P2", -369, someday())
	fx.Todo("ta", "TA", -37852, someday(), inArea("ar"))
	fx.Project("pa", "PA", 0, someday(), inArea("ar"))

	got := mustList(t, d, ViewSomeday, TaskFilter{})
	want := []string{"p1", "p2", "t1", "t2", "pa", "ta"}
	assertOrder(t, got, want, "someday")
}

// An area's listing puts its open projects first, then its loose to-dos in
// the order of the area's page — Anytime by index, scheduled by date, Someday
// — and only then its projects' to-dos, project by project. Measured on 9
// Oct 2026 against `to dos of area`: the app listed the area's three open
// projects first, its loose to-dos next and a Someday project last, where the
// CLI printed the projects' to-dos first and the project rows among the
// loose to-dos by index. The app's area page lists no project's to-dos; the
// CLI keeps them after the loose ones, the order Anytime uses.
func TestAreaListingOrdersProjectsThenLooseThenChildren(t *testing.T) {
	d, fx := newFixture(t)
	soon := int64(model.ThingsDateFromTime(testNow.AddDate(0, 0, 3)))
	later := int64(model.ThingsDateFromTime(testNow.AddDate(0, 0, 6)))
	fx.Area("ar", "Personal", -3070)

	fx.Project("p1", "P1", -4767, anytime(), inArea("ar"))
	fx.Project("p2", "P2", -4154, anytime(), inArea("ar"))
	fx.Project("p-someday", "Someday project", 0, someday(), inArea("ar"))
	fx.Project("p-sched", "Scheduled project", 0, somedayOn(soon), todayIndex(-20), inArea("ar"))
	fx.Todo("loose-1", "Loose 1", -5387, anytime(), inArea("ar"))
	fx.Todo("loose-2", "Loose 2", -159, anytime(), inArea("ar"))
	fx.Todo("loose-sched", "Loose scheduled", -478, somedayOn(soon), todayIndex(-10), inArea("ar"))
	fx.Todo("loose-later", "Loose later", 0, somedayOn(later), inArea("ar"))
	fx.Todo("loose-someday", "Loose someday", -900, someday(), inArea("ar"))
	fx.Todo("c2", "Child of P2", -999, anytime(), inProject("p2"))
	fx.Todo("c1", "Child of P1", 0, anytime(), inProject("p1"))

	got := mustList(t, d, ViewProject, TaskFilter{Area: "Personal"})
	want := []string{
		"p1", "p2", "loose-1", "loose-2",
		"p-sched", "loose-sched", "loose-later",
		"p-someday", "loose-someday",
		"c1", "c2",
	}
	assertOrder(t, got, want, "--area")
}

// A --tag sweep groups like the lists: rows filed in no area first, then each
// area in area order, and inside an area its projects, then its loose to-dos,
// then its projects' to-dos. Measured on 9 Oct 2026 against `to dos of tag`
// for the area order and the order inside an area; the app's tag list held
// no area-less row, so leading with those follows the lists rather than a
// measurement.
func TestTagListingGroupsLikeTheLists(t *testing.T) {
	d, fx := newFixture(t)
	fx.Area("ar-p", "Personal", -3070)
	fx.Area("ar-w", "Work", -2005)
	fx.Tag("tg", "Work", 0)

	fx.Todo("w-loose", "Work loose", -31191, anytime(), inArea("ar-w"))
	fx.Project("w-proj", "Work project", -13007, anytime(), inArea("ar-w"))
	fx.Todo("w-child", "Work child", 0, anytime(), inProject("w-proj"))
	fx.Todo("p-loose", "Personal loose", 0, anytime(), inArea("ar-p"))
	fx.Todo("unfiled", "Unfiled", 5, anytime())
	fx.Tagged("w-loose", "tg")
	fx.Tagged("w-proj", "tg")
	fx.Tagged("w-child", "tg")
	fx.Tagged("p-loose", "tg")
	fx.Tagged("unfiled", "tg")

	got := mustList(t, d, ViewProject, TaskFilter{Tag: "Work"})
	want := []string{"unfiled", "p-loose", "w-proj", "w-loose", "w-child"}
	assertOrder(t, got, want, "--tag")
}

// Trash lists what was thrown away most recently first. Measured on 9 Oct
// 2026, the app's Trash was in userModificationDate order, newest first, over
// all 1488 rows; the CLI listed by index.
func TestTrashOrdersByModificationDateNewestFirst(t *testing.T) {
	d, fx := newFixture(t)
	fx.Todo("old", "Old", 1, anytime(), trashed())
	fx.Project("mid", "Mid", 2, anytime(), trashed())
	fx.Todo("new", "New", 3, anytime(), trashed())
	mustExec(t, d, `UPDATE TMTask SET userModificationDate = ? WHERE uuid = 'old'`, 1791000000.0)
	mustExec(t, d, `UPDATE TMTask SET userModificationDate = ? WHERE uuid = 'mid'`, 1791100000.0)
	mustExec(t, d, `UPDATE TMTask SET userModificationDate = ? WHERE uuid = 'new'`, 1791200000.0)

	got := mustList(t, d, ViewTrash, TaskFilter{})
	want := []string{"new", "mid", "old"}
	assertOrder(t, got, want, "trash")
}

// A project in Today only by its deadline takes its place by todayIndex, like
// any other row, not a place of its own at the top. Measured on 9 Oct 2026:
// an undated project due that day was third in the app's Today, after two
// loose to-dos with a lower todayIndex, and another due project was
// sixteenth. Things gives such a row a todayIndex and reference date.
func TestTodayPlacesDeadlineDueProjectByTodayIndex(t *testing.T) {
	d, fx := newFixture(t)
	today := int64(model.ThingsDateFromTime(testNow))

	fx.Todo("first", "First", -9741, anytimeOn(today), todayIndexRef(today), todayIndex(-6199))
	fx.Todo("second", "Second", -9224, anytimeOn(today), todayIndexRef(today), todayIndex(-5554))
	fx.Project("due", "Due today", -4192, anytime(), deadline(today), todayIndexRef(today), todayIndex(-4544))
	fx.Todo("after", "After", 5847, anytimeOn(today), todayIndexRef(today), todayIndex(5334))

	got := mustList(t, d, ViewToday, TaskFilter{})
	want := []string{"first", "second", "due", "after"}
	assertOrder(t, got, want, "today")
}
