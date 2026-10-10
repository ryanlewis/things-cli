package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/model"
)

type TaskFilter struct {
	Project string
	Area    string
	Tag     string

	// OpenOnly drops the completed/cancelled items that Things has not yet
	// logged out of the list they are in. By default the views CompletableView
	// reports — inbox, today, anytime, upcoming, someday, and the catch-all
	// when Project, Area or Tag names one — list those items, as the app does
	// (UI-parity); with OpenOnly they return only open tasks. Every other view
	// lists the same rows either way.
	OpenOnly bool

	On   *model.ThingsDate
	From *model.ThingsDate
	To   *model.ThingsDate
}

// CompletableView reports whether the view lists closed rows Things has not
// yet logged, unless OpenOnly is set. The CLI asks it to keep rejecting the
// no-op --include-completed where it never applied. The answer comes off the
// view's own spec, and it is the same field that widens the status test, so
// the question the CLI asks and the SQL it then runs cannot disagree.
//
// projectNamed is whether --project names a project, areaNamed whether
// --area names an area, and tagNamed whether --tag names a tag. The catch-all
// view widens only then. Naming a project lists its contents
// (widensToProjectContents), and the app keeps a to-do closed today on the
// project's page (issue #295). Naming an area lists the area's page, which
// keeps one too, and so does a tag (see completesWithFilter).
func CompletableView(view string, projectNamed, areaNamed, tagNamed bool) bool {
	spec := views[view]
	return spec.widensStatus(areaNamed, tagNamed) ||
		(projectNamed && spec.widensToProjectContents)
}

// widensStatus reports whether the view's status test widens past the open
// rows to those closed and not yet logged, when completed rows are asked for.
// It does on the views that show unlogged rows, and on the one that
// completesWithFilter once --area or --tag names something.
func (s viewSpec) widensStatus(areaNamed, tagNamed bool) bool {
	return s.showsUnlogged || ((areaNamed || tagNamed) && s.completesWithFilter)
}

// CompletableViewNames lists those views in a stable order, for error text.
func CompletableViewNames() []string {
	names := make([]string, 0, len(views))
	for name, spec := range views {
		if spec.showsUnlogged {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// DateFilterableView reports whether --on/--from/--to apply to the view. An
// unknown name is not date-filterable: it has no spec, and a zero spec does
// not support the filter.
func DateFilterableView(view string) bool {
	return views[view].supportsDateFilter
}

// ProjectFilterableView reports whether --project applies to the view. The
// flag it reads is the negative one, so an unknown name stays filterable, as
// it was when this read a map of the views that refuse the filter.
func ProjectFilterableView(view string) bool {
	return !views[view].rejectsProjectFilter
}

// HidesTemplateContentsView reports whether the view withholds the to-dos
// inside a repeating project template (issue #171). trash, logbook and
// repeating keep templates, and with them their contents, so a closed or
// trashed to-do out of a template does list there — only the other views hide
// it. It reads the same flag buildListQuery does, so the answer and the SQL
// cannot drift apart.
func HidesTemplateContentsView(view string) bool {
	return !views[view].includesTemplates
}

// repeatingPlaceholder is substituted with the probed recurrence column
// reference by (*DB).taskQuery — the column name varies across Things schema
// versions, and a schema carrying none resolves it to NULL.
const repeatingPlaceholder = "{{repeating}}"

// repeatingParentPlaceholder is the same for the `p` alias — the row's
// resolved parent project — so a static filter can ask "is this the child of a
// repeating project template?". ListTasks substitutes it the same way, and
// baseTaskQuery's select list carries it too: such a child has no rule of its
// own, and is flagged repeating so the writes Things refuses on its project
// are refused on it as well (issue #174).
const repeatingParentPlaceholder = "{{repeating_parent}}"

// baseTaskQuery selects a task with its project, heading, area and tags, and
// how many checklist items it has and how many of those are open.
//
// The checklist counts are subqueries rather than a join: a join would repeat
// each row once per item and multiply the tags GROUP_CONCAT gathers. Things
// indexes TMChecklistItem.task, so each count is an index lookup on the rows
// the view kept, not a scan of every checklist.
//
// A task filed under a project heading carries t.heading and leaves t.project
// NULL — the heading row (a TMTask) holds the project. Resolving p through
// COALESCE(t.project, h.project) therefore folds heading-nested tasks into
// their project, so they carry a project title in output and match the
// --project and --area filters (issue #139).
const baseTaskQuery = `
SELECT
	t.uuid,
	COALESCE(t.title, ''),
	COALESCE(t.notes, ''),
	COALESCE(t.type, 0),
	COALESCE(t.status, 0),
	` + shownStart + `,
	COALESCE(t.startBucket, 0),
	t.startDate,
	t.reminderTime,
	t.deadline,
	t.stopDate,
	t.creationDate,
	COALESCE(t.trashed, 0),
	COALESCE(p.uuid, ''),
	COALESCE(p.title, ''),
	COALESCE(p.trashed, 0),
	COALESCE(t.heading, ''),
	COALESCE(h.title, ''),
	COALESCE(a.uuid, COALESCE(pa.uuid, '')),
	COALESCE(a.title, COALESCE(pa.title, '')),
	COALESCE(GROUP_CONCAT(tag.title, char(31)), ''),
	COALESCE(t."index", 0),
	COALESCE(t.todayIndex, 0),
	CASE WHEN {{repeating}} IS NOT NULL OR {{repeating_parent}} IS NOT NULL THEN 1 ELSE 0 END,
	t.userModificationDate,
	(SELECT COUNT(*) FROM TMChecklistItem ci WHERE ci.task = t.uuid),
	(SELECT COUNT(*) FROM TMChecklistItem ci WHERE ci.task = t.uuid AND COALESCE(ci.status, 0) = 0)
FROM TMTask t
LEFT JOIN TMTask h ON t.heading = h.uuid
LEFT JOIN TMTask p ON p.uuid = COALESCE(t.project, h.project)
LEFT JOIN TMArea a ON t.area = a.uuid
LEFT JOIN TMArea pa ON p.area = pa.uuid
LEFT JOIN TMTaskTag tt ON tt.tasks = t.uuid
LEFT JOIN TMTag tag ON tt.tags = tag.uuid
`

// splitTags splits the tag titles GROUP_CONCAT joined with char(31), and
// returns nil for a row with none.
func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x1f")
}

func scanTask(row rowScanner) (model.Task, error) {
	var t model.Task
	var startDate, deadline, stopDate, creationDate, modificationDate sql.NullFloat64
	var reminder sql.NullInt64
	var tagsStr string
	var trashed, projectTrashed, repeating, checklistTotal, checklistOpen int

	err := row.Scan(
		&t.UUID, &t.Title, &t.Notes,
		&t.Type, &t.Status, &t.Start, &t.StartBucket,
		&startDate, &reminder, &deadline, &stopDate, &creationDate,
		&trashed,
		&t.ProjectUUID, &t.ProjectTitle, &projectTrashed,
		&t.HeadingUUID, &t.HeadingTitle,
		&t.AreaUUID, &t.AreaTitle,
		&tagsStr,
		&t.Index, &t.TodayIndex,
		&repeating,
		&modificationDate,
		&checklistTotal, &checklistOpen,
	)
	if err != nil {
		return t, err
	}

	t.Trashed = trashed != 0
	t.ProjectTrashed = projectTrashed != 0
	t.Repeating = repeating != 0
	t.StartDate = thingsDate(startDate)
	if reminder.Valid {
		text := model.ReminderText(reminder.Int64)
		t.ReminderTime = &text
	}
	t.Deadline = thingsDate(deadline)
	t.StopDate = unixTime(stopDate)
	t.CreationDate = unixTime(creationDate)
	t.ModificationDate = unixTime(modificationDate)
	t.Tags = splitTags(tagsStr)
	if checklistTotal > 0 {
		t.ChecklistProgress = &model.ChecklistProgress{Total: checklistTotal, Open: checklistOpen}
	}
	return t, nil
}

// todoOrProject is the TMTask type set for the views that carry both kinds.
// The statement a reader is meant to find — which views carry projects, and
// why — is in internal/skill/SKILL.md under `things list`; each entry in the
// view table below carries the measurement behind its own clause (issues #201,
// #206, #210, #212, #213, #245). Headings (type 2) are structure inside a
// project, never rows in a list, so they stay out.
const todoOrProject = "t.type IN (0, 1)"

// closedTodayUnlogged is the app's rule for a closed item Things has not yet filed
// into the Logbook. Two conditions hold at once, and measuring against the app
// on 10 Sep 2026 is what established the pair (issue #230).
//
// The calendar day is the boundary Things actually keeps: the app's Today held
// six closed items, all closed that day, while its Logbook held forty items
// closed on the three previous days — every one of them with a stopDate after
// manualLogDate. So manualLogDate alone cannot be the rule, or those forty
// would still be under Today.
//
// manualLogDate is the second condition rather than a discarded one, because
// it is what "Log Completed Now" sets: it files the day's closed items
// immediately instead of waiting for midnight, and dropping it would leave the
// menu command with no effect. The user's manualLogDate was five days old
// throughout the measurement, so both conditions held for all six rows; they
// could only be told apart by closing something in the live app, which the
// measurement deliberately did not do.
//
// COALESCE guards a NULL stopDate. Without it the comparison is NULL, and the
// logbook's negation of this clause is NULL too, which would silently drop a
// closed row carrying no stopDate out of both lists.
//
// That pair is the "daily" choice of the app's "Move completed items to
// Logbook" setting, which the measurement ran under. Things keeps the setting
// in TMSettings.logInterval, as the tag its preferences menu gives each
// choice: 0 immediately, 1 daily, 4 manually. With "immediately" nothing is
// held in place; with "manually" a row is held until the next "Log Completed
// Now", whatever day it closed. Any other value, or no setting row, keeps the
// daily rule. The other two were read from the app's preferences, not measured
// in the live app, as that would mean changing the user's setting.
var closedTodayUnlogged = closedUnloggedOf("t")

// closedUnloggedOf is closedTodayUnlogged asked of the TMTask row aliased
// alias.
func closedUnloggedOf(alias string) string {
	stopDate := "COALESCE(" + alias + ".stopDate, 0)"
	return `CASE COALESCE((SELECT logInterval FROM TMSettings LIMIT 1), 1)` +
		` WHEN 0 THEN 0` +
		` WHEN 4 THEN ` + closedAfterManualLog(alias) +
		` ELSE ` + closedAfterManualLog(alias) + ` AND ` + stopDate + ` >= (SELECT start FROM ` + todayClock + `)` +
		` AND ` + stopDate + ` < (SELECT finish FROM ` + todayClock + `) END`
}

// closedAfterManualLog is true for a row closed after the last "Log Completed
// Now", the condition the daily and manual choices share.
func closedAfterManualLog(alias string) string {
	return `COALESCE(` + alias + `.stopDate, 0) > COALESCE((SELECT manualLogDate FROM TMSettings LIMIT 1), 0)`
}

// todayScheduled is the today view's scheduling test: the rows Things files
// under Today by their start date. todayDue is the other way in, and
// todayScope joins the two. A start = 2 row whose day has come counts too:
// see scheduledArrived.
const todayScheduled = "(t.start = 1 OR (" + scheduledArrived + ")) AND t.startBucket IN (0, 1) AND t.startDate IS NOT NULL"

// scheduledArrived is a row scheduled for a later day whose day has come but
// which Things has not yet moved. Just after midnight a to-do scheduled for
// the new day is still start = 2 until Things runs its day-change
// maintenance. Measured on 4 Oct 2026 at 00:31, one such to-do was in the
// app's Today and Anytime and not in its Upcoming (issue #363). No project
// was caught in this state; one is treated the same, as it shares the code
// path.
const scheduledArrived = "t.start = 2 AND t.startDate <= " + thingsToday

// shownStart is the start a row reports: the one the app shows. An open
// scheduledArrived row reports Anytime, the code Things gives it when it moves
// the row; without this, --json said "someday" for a row the listing filed
// under today. Things moves only open, untrashed rows: on 4 Oct 2026 the
// database held 22 closed and 17 trashed start = 2 rows dated days back, so
// those keep the code they have. Nor does it move a repeating template or a
// to-do inside one (notATemplate), which the views leave out of today too.
const shownStart = "CASE WHEN t.status = 0 AND t.trashed = 0 AND " + notATemplate + " AND " + scheduledArrived +
	" THEN 1 ELSE COALESCE(t.start, 0) END"

// todayDue is the other way into Today: a row with no start date whose
// deadline has arrived, from any bucket, and for as long as it is overdue. Measured on 3 Oct 2026 with test to-dos, the app's Today held
// all four of those shapes and the CLI none. It left out the two whose
// deadlineSuppressionDate equalled their deadline, which is how Things records
// "taken out of Today for this deadline"; the app cleared that column when a
// deadline was changed (issue #294).
//
// It takes projects as well as to-dos, and the Someday bucket as well as the
// other two. Measured on 9 Oct 2026, the app's Today held a loose Someday
// to-do due today, a Someday project and an Anytime project due today, and an
// Anytime to-do due today inside a Someday project and inside an Anytime one.
// An earlier measurement that day found a Someday to-do due today out of
// Today, and that was the suppression column at work, not the bucket: an item
// created through the URL scheme with a deadline of today or earlier gets
// deadlineSuppressionDate set to that deadline at creation, so it never
// enters Today, while one whose deadline is set afterwards has no suppression
// and does. A row with a start date as well is todayScheduled's question,
// and was not measured here.
//
// A row this test takes is promoted out of its bucket, not only into Today:
// it is in Anytime as well (anytimeScope), and out of the Inbox and Someday
// (notTodayDue). Anytime and the Inbox carry to-dos only (todoOnly).
//
// The test is IS NULL rather than "differs from the deadline" because no write
// measured leaves a stale suppression behind. On 4 Oct 2026, suppressed test
// to-dos had their deadline moved by the URL scheme (things edit --deadline)
// and by AppleScript (set due date), each to tomorrow and to an earlier,
// overdue day. Every move cleared the column, and the overdue ones came back
// into the app's Today. Setting the same deadline again left the column and
// kept the to-do out of Today. Sync from another device was not measured
// (issue #376).
const todayDue = "t.startDate IS NULL AND t.deadline <= " + thingsToday +
	" AND t.deadlineSuppressionDate IS NULL"

// todayScope is Today's whole scope: the two ways in, either of which is
// enough.
const todayScope = "((" + todayScheduled + ") OR (" + todayDue + "))"

// todayDate is the day --on/--from/--to match a Today row on: its start date,
// or for a todayDue row, which has none, today itself. That is the day the
// app shows it under; its deadline is in the past when it is overdue, so
// matching on that would drop it from `today --on <today>`.
const todayDate = "COALESCE(t.startDate, " + thingsToday + ")"

// thingsToday is today's local date in the ThingsDate encoding
// (year<<16 | month<<12 | day<<7), so it compares directly with startDate and
// deadline. It comes from today_clock, as closedTodayUnlogged's day does.
const thingsToday = `(SELECT day FROM ` + todayClock + `)`

// heldInPlace is the set the Logbook withholds: every closed row Things has
// not yet logged, wherever it sits. It used to be the union of what the Inbox,
// Today, Anytime and Upcoming were still showing (issues #230, #238, #293),
// on the view that a row no list held went to the Logbook at once. The app
// does not do that. Measured on 4 Oct 2026 under the daily setting, its
// Logbook held none of eight rows closed that day, among them a closed
// project with no area that no list shows at all. The CLI lists each such
// row by default in a view (someday included), its project, or its area,
// unless --open-only is passed. A closed Anytime project with no area is the
// one exception, and `things projects` lists it until it is logged.
//
// notATemplate is the other half of the test. today, anytime, upcoming and
// someday all drop repeating templates and the contents of repeating project
// templates, and the repeating view lists only open rows, so
// withholding such a row would take it out of every list. The Logbook keeps
// them (includesTemplates) at once. Both halves of notATemplate are "IS NULL"
// tests, which are never NULL themselves.
var heldInPlace = "(" + closedTodayUnlogged + " AND " + notATemplate + ")"

// notATemplate excludes the rows those views never carry: the template
// row itself, and a to-do inside a repeating project template, which carries
// no rule of its own — only its project does (issue #171).
const notATemplate = repeatingPlaceholder + " IS NULL AND " + repeatingParentPlaceholder + " IS NULL"

// parentClosed is true for a row whose parent project has been completed or
// cancelled. p is resolved through COALESCE(t.project, h.project), so a to-do
// filed under a project heading is judged by its heading's project. COALESCE
// makes an unparented row — p.uuid NULL — read false rather than NULL, which
// is what lets both the test and its negation stay boolean.
const parentClosed = "COALESCE(p.status, 0) IN (2, 3)"

// parentNotClosed is the fold issue #229 measured: a closed project is one row
// in the Logbook and its to-dos are not listed beside it, because the app
// folds them into the project's row. It keeps an unparented row in the view.
// The Logbook and the views that show unlogged rows fold only once the
// project is logged; see parentNotClosedOrUnlogged. An area's page folds as
// soon as the project closes, and is this constant's caller.
//
// Trash is the deliberate exception rather than another caller: it folds a
// trashed parent's children only, for the reason its own entry in the view
// table gives.
const parentNotClosed = "NOT (" + parentClosed + ")"

// parentCloseUnlogged is closedTodayUnlogged asked of the parent project.
var parentCloseUnlogged = closedUnloggedOf("p")

// parentNotClosedOrUnlogged is the fold the views that show unlogged rows apply:
// a to-do of a closed project stays in place, struck through, until the
// project itself is logged, and only then folds into it (issue #249). Measured
// on 4 Oct 2026, the app's Anytime, Today and Upcoming each kept the to-dos
// of a project closed that day where they were.
//
// The Logbook takes it too. A to-do closed and logged on an earlier day,
// inside a project completed today and not yet logged, has no logged row to
// fold into, and the app lists it in its Logbook. Measured on 8 Oct 2026:
// seven such to-dos were in the app's Logbook and missing from the CLI's, and
// both agreed once the day change logged the project.
//
// The unlogged half is the parent's heldInPlace, not bare parentCloseUnlogged:
// a closed repeating project template is logged at once (heldInPlace leaves
// templates out), so its Logbook row is there for its to-dos to fold into.
var parentNotClosedOrUnlogged = "(" + parentNotClosed + " OR (" + parentCloseUnlogged +
	" AND " + repeatingParentPlaceholder + " IS NULL))"

// openOrJustClosed is the status test for the views that show unlogged rows.
// Under OpenOnly only open rows; by default, also the rows the app
// is still showing in place because they were closed and not yet logged, and
// not folded into a logged project's row. Shared so inbox, today, anytime,
// upcoming and someday cannot answer the question differently.
//
// The fold sits inside the closed branch rather than beside it, so it can only
// ever remove a closed row the widening just added. An open to-do under a
// closed project is a different question and a riskier one — dropping it would
// take real work out of Today — and issue #249 does not ask it. There was no
// such row in the data on 10 Sep 2026 to measure the app's answer against.
//
// foldsUnlogged keeps the fold for a project closed and not yet logged.
// An area's page does that: measured on 3 and 4 Oct 2026, it held a
// project closed that day as one row, and none of its to-dos, which the
// lists themselves went on showing in place.
func openOrJustClosed(o whereOpts, foldsUnlogged bool) string {
	if !o.includeCompleted {
		return openRows
	}
	// The fold exists so a logged project is one row rather than a row plus
	// its contents. Naming that project is asking for the contents, so the
	// fold comes off — the same answer `things --project <uuid>` and
	// `show --agent` already give (issue #253). With p pinned to the named
	// project the clause is a constant, true for an open project and false
	// for a closed one, so dropping it can only ever affect the closed case.
	switch {
	case o.projectNamed:
		return openOrUnlogged("")
	case foldsUnlogged:
		return openOrUnlogged(parentNotClosed)
	default:
		return openOrUnlogged(parentNotClosedOrUnlogged)
	}
}

// openOrUnlogged is the open rows and the closed rows Things has not yet
// logged, the one spelling of "open or just closed" that the views, `things
// projects` and AddTarget all take. fold, when not empty, is a further test
// on the closed rows alone.
func openOrUnlogged(fold string) string {
	closed := closedRows + " AND " + closedTodayUnlogged
	if fold != "" {
		closed += " AND " + fold
	}
	return "(" + openRows + " OR (" + closed + "))"
}

// The row-state and row-kind tests every view is built from. They were spelled
// out inside each view's WHERE string before, seven copies of the open set
// among them, so a change to one meant finding the rest by eye (issue #240).
const (
	// openRows is the default status test. Almost every view is the open set;
	// inbox, today, anytime, upcoming and someday widen past it unless OpenOnly is set, and only the
	// logbook and trash are built on something else.
	openRows = "t.status = 0"
	// closedRows is the Logbook's status test: completed and cancelled both,
	// which is what "closed" means to the app (issue #210).
	closedRows = "t.status IN (2, 3)"
	// untrashedRows keeps thrown-away rows out of every view but trash.
	untrashedRows = "t.trashed = 0"
	// trashedRows is trash's own test, and the whole of its scope.
	trashedRows = "t.trashed = 1"
	// todoOnly is the row-kind test for the views that carry no project rows:
	// inbox and anytime. todoOrProject is the other half of the choice.
	todoOnly = "t.type = 0"
)

// The scope tests: what puts a row in one view rather than another. Each is
// the bucket, date or recurrence rule that names the app's own list.
const (
	inboxBucket   = "t.start = 0"
	anytimeBucket = "t.start = 1"
	// anytimeScope is Anytime's whole scope: its bucket, and the undated
	// Inbox and Someday to-dos whose deadline has come. todayDue covers every
	// bucket, so joining it adds only those two. Measured on 3 Oct 2026, the
	// app's Anytime held an Inbox to-do due that day and one overdue, the same
	// two its Today held, and no Inbox to-do due later or taken out of Today
	// for its deadline. Measured on 9 Oct 2026, it held a loose Someday to-do
	// due today, as Today did, and the app's Someday did not: a deadline
	// promotes a Someday row out of Someday the way it promotes an Inbox one
	// out of the Inbox. A start = 2 to-do whose day has come is in Anytime
	// too, as it is in Today (scheduledArrived).
	anytimeScope = "(" + anytimeBucket + " OR (" + scheduledArrived + ") OR (" + todayDue + "))"
	// upcomingDue is the other way into Upcoming: an Anytime or Someday row
	// with no start date but a deadline after today, which the app lists under
	// the deadline's day. Measured on 30 Sep 2026, the app's Upcoming held both
	// such Anytime to-dos in the data and the CLI neither. Measured on 9 Oct
	// 2026, it also held an Anytime project of that shape, and a Someday to-do
	// and a Someday project with a later deadline.
	//
	// The Inbox stays out: measured on 9 Oct 2026, an Inbox to-do with a
	// later deadline was in the app's Inbox and not its Upcoming. todayDue
	// takes one once its deadline comes. A row with a
	// start date as well is upcomingScheduled's, and was not measured here. A Someday
	// to-do inside a project with a later deadline is in Upcoming too:
	// measured on 9 Oct 2026, under an open parent project and under a
	// Someday one alike, while the app's Someday list left both out.
	upcomingDue = "t.start IN (1, 2) AND t.startDate IS NULL AND t.deadline > " + thingsToday
	// upcomingDate is the day Upcoming files a row under — its start date,
	// or for an upcomingDue row, which has none, its deadline. It is both the
	// view's sort key and the column --on/--from/--to compare.
	upcomingDate = "COALESCE(t.startDate, t.deadline)"
	hasDeadline  = "t.deadline IS NOT NULL"
	// isTemplate selects the rows that carry a recurrence rule — the templates
	// themselves, not the items they generate (issue #147).
	isTemplate = repeatingPlaceholder + " IS NOT NULL"
)

// upcomingScheduled and somedayDeferred split the one Things code between
// them. start = 2 is both lists: the app shows a deferred item in Upcoming
// once it carries a date and in Someday while it does not. A dated one
// whose day has come is Today's instead (scheduledArrived), so Upcoming
// takes only the days after today.
var (
	upcomingScheduled = scheduledLaterOf("t")
	somedayDeferred   = deferredUndatedOf("t")
	// upcomingScope is Upcoming's whole scope: the two ways in, either of
	// which is enough.
	upcomingScope = "((" + upcomingScheduled + ") OR (" + upcomingDue + "))"
)

// scheduledLaterOf and deferredUndatedOf are upcomingScheduled and
// somedayDeferred asked of the TMTask row aliased alias.
func scheduledLaterOf(alias string) string {
	return alias + ".start = 2 AND " + alias + ".startDate > " + thingsToday
}

func deferredUndatedOf(alias string) string {
	return alias + ".start = 2 AND " + alias + ".startDate IS NULL"
}

// notHeldInPlace is the Logbook's complement of what Things has not yet
// logged. COALESCE makes the negation null-safe. The Logbook's other extra is
// parentNotClosedOrUnlogged, which it shares with the views that show unlogged
// rows, so it is defined with its pair.
var notHeldInPlace = "COALESCE(" + heldInPlace + ", 0) = 0"

// The predicates only one view needs.
const (
	// unparented is someday's parity rule: a to-do inside a project stays
	// inside it however it is deferred (issue #211).
	unparented = "p.uuid IS NULL"
	// parentNotDeferred is anytime's parity rule: the app's Anytime leaves
	// out every to-do of a project in Someday or scheduled for a later date,
	// in the project or under one of its headings, and even one Today shows.
	// Measured on 3 Oct 2026 with test to-dos (issue #346). Both kinds of
	// project are start = 2. A start = 2 project whose day has come is not
	// deferred any more, only not yet moved (scheduledArrived), so its to-dos
	// stay (issue #363). COALESCE keeps an unparented to-do.
	parentNotDeferred = "NOT (COALESCE(p.start, 1) = 2 AND (p.startDate IS NULL OR p.startDate > " + thingsToday + "))"
	// notTodayDue is todayDue's other half, for the Inbox and Someday: an
	// undated row whose deadline has come leaves its bucket for Today and
	// Anytime, and goes back when it is taken out of Today for that deadline.
	// Measured on 3 Oct 2026, the app's Inbox held neither a to-do due that
	// day nor one overdue, and held the overdue one again once its
	// deadlineSuppressionDate was set (issue #345). Measured on 9 Oct 2026,
	// the app's Someday did not hold a loose Someday to-do due today. A
	// Someday project due today is in the app's Today; that it leaves Someday
	// too is inferred from the to-do, not measured. COALESCE keeps a row with
	// no deadline, where todayDue is NULL.
	notTodayDue = "COALESCE(" + todayDue + ", 0) = 0"
)

// viewSpec is one list view: what it selects, how it is arranged, and how it
// answers the flags that change either.
//
// All of that used to be spread across six maps — the WHERE in viewFilters,
// the ORDER BY in viewOrderBy with a fallback to indexOrderBy, and a flag each
// in viewsIncludingTemplates, completableViews, dateFilterableViews and
// viewsWithoutProjectFilter — so adding or changing a view meant finding all
// six, and every parity change in the week to 10 Sep 2026 edited more than one
// of them (issue #240).
//
// The last two of those gate flag validation at the CLI boundary rather than
// composing SQL, which is why #240 folded the four SQL-shaping maps first and
// these two after. Adding a view is now one entry here and nothing else.
//
// The WHERE is composed from the fields rather than written out per view, in a
// fixed slot order: scope, then status, then trashed, then the view's own
// extra predicates, then the row-kind test last. That is the order the strings
// were written in when each was spelled out by hand, so the SQL is unchanged —
// TestListQueryGoldenSQL holds it to that.
type viewSpec struct {
	// scope is what puts a row in this view at all: its start bucket, its
	// date, its recurrence rule. Empty for the views that take every row the
	// other fields allow — logbook, trash and the catch-all.
	scope string

	// status is the row-state test. Empty only for trash, which takes a row
	// whatever state it is in.
	//
	// Where showsUnlogged is set this must hold openRows: the widening
	// does not add to the field, it replaces it wholesale with
	// openOrJustClosed, which starts from the open set. Setting showsUnlogged
	// on a view built on any other status — the Logbook's closedRows, say —
	// would silently swap that view's status test for the open one rather than
	// add to it.
	status string

	// trashed is the thrown-away test, which every view carries: untrashedRows
	// everywhere but trash, which is built on its opposite.
	trashed string

	// extra are the predicates particular to this view, appended in order
	// after the shared three.
	extra []string

	// includesProjects picks the row-kind test that closes the WHERE: project
	// rows alongside to-dos (todoOrProject) or to-dos alone (todoOnly).
	includesProjects bool

	// showsUnlogged marks the views that list a closed row Things has not
	// yet logged, unless OpenOnly is set: the lists the app keeps a
	// just-closed item visible in until the day
	// rolls over. inbox, today, anytime, upcoming and someday are all such
	// lists (issues #106, #238, #293). Measured on 4 Oct 2026, the app's
	// Someday kept a to-do and a project closed out of it, struck through.
	showsUnlogged bool

	// supportsDateFilter marks the views --on/--from/--to make sense in.
	// Excluded: inbox tasks have no startDate; trash is trashed-only; logbook
	// items have a stopDate but no meaningful startDate filter; someday
	// requires startDate IS NULL, so a startDate range could never match
	// anything; and repeating lists templates rather than scheduled items, so
	// "what falls in this date range" is not a question that view answers —
	// the instances a template generates are ordinary rows in the dated views.
	// What a template's own startDate means was not measured: the database
	// this was checked against on 10 Sep 2026 held no repeating items at all.
	supportsDateFilter bool

	// rejectsProjectFilter marks the views a --project filter can never match
	// in. someday keeps only rows with no parent project (issue #211), so
	// pairing it with --project asks for the contents of a project the view
	// has already excluded: the two clauses contradict, and the listing is
	// empty whatever the project holds. Rejecting the combination beats
	// printing an empty list, the same call issue #124 made for date filters
	// on this view.
	//
	// It is the one flag here stated in the negative, because it has one
	// holder and the default is to allow the filter. That also keeps an
	// unknown view name filterable, which is what the map it replaced did.
	rejectsProjectFilter bool

	// includesTemplates marks the views that keep repeating templates in their
	// results. Everywhere else templates are filtered out: Things files a
	// template under Repeating, not under the start bucket its row happens to
	// carry, so a Someday-start template showing up in `things someday` is a
	// leak (issue #147). trash and logbook stay literal — they report what the
	// database actually holds, templates included.
	includesTemplates bool

	// orderBy is the view's whole ORDER BY clause, ending in uuidTiebreak. The
	// key before the tiebreak is t."index", the order Things keeps rows in
	// within a list, so the tiebreak only decides rows that were genuinely
	// indistinguishable and no view's primary ordering changes. The view with no
	// arrangement of its own, inbox, takes indexOrderBy, which it used to
	// reach through a fallback in ListTasks; naming it here means
	// no view's ordering is decided anywhere but in this table.
	orderBy string

	// dateColumn is what --on/--from/--to compare against. Empty means
	// t.startDate, which is right for every view but the three that are read
	// by another date.
	dateColumn string

	// widensToProjectContents marks the view that answers --project with the
	// project's whole contents (closedProjectContents) instead of composing
	// its usual WHERE with a project filter. Only the catch-all has it.
	widensToProjectContents bool

	// completesWithFilter marks the view that shows unlogged rows when --area
	// names an area or --tag names a tag, as it does when --project names a
	// project. Only the catch-all has it. The app's area page keeps an item
	// closed today in place until it is logged: measured on 3 Oct 2026, `to
	// dos of area id X` held loose to-dos closed out of Anytime, Upcoming and
	// Someday, and a project completed that day, but not that project's own
	// to-dos, which its row stands for, so the closed-parent fold stays on.
	// The listing also carries the area's projects' to-dos, and the project's
	// page keeps one closed today (issue #295), so the widening reaches those
	// too.
	//
	// A tag keeps them as well. Measured on 9 Oct 2026, the app's listing
	// for one tag held 70 rows where a bare --tag listed 65, and the five
	// were rows closed that day and not yet logged. A tag has no page that folds a
	// project's to-dos into its row, so a tag alone takes the lists' fold
	// (parentNotClosedOrUnlogged) and not the area's.
	completesWithFilter bool

	// keepsTrashedParentGuard marks the view that keeps untrashedParent even
	// when --project names a project, where every other view lifts it. Only
	// trash has it; see buildListQuery.
	keepsTrashedParentGuard bool
}

// rowKinds is the type test, which closes every view's WHERE.
func (s viewSpec) rowKinds() string {
	if s.includesProjects {
		return todoOrProject
	}
	return todoOnly
}

// whereOpts carries the parts of a TaskFilter that change how the WHERE is
// composed, as against the clauses buildListQuery appends after it. The
// fields reach only the status test today; they are a struct rather than
// parameters so a third does not turn every call site into a row of bare
// booleans.
type whereOpts struct {
	// includeCompleted is TaskFilter's !OpenOnly. It widens the status test on
	// the views showsUnlogged or completesWithFilter mark, and is ignored on the
	// rest.
	includeCompleted bool

	// projectNamed is set when --project names one project, which lifts the
	// closed-parent fold. See openOrJustClosed for why.
	projectNamed bool

	// areaNamed is set when --area names an area, and tagNamed when --tag
	// names a tag. Either lets the widening reach the view that
	// completesWithFilter.
	areaNamed bool
	tagNamed  bool
}

// where composes the view's WHERE clause.
func (s viewSpec) where(o whereOpts) string {
	status := s.status
	if o.includeCompleted && s.widensStatus(o.areaNamed, o.tagNamed) {
		// Only an area's page folds a project closed today into its row.
		status = openOrJustClosed(o, !s.showsUnlogged && o.areaNamed)
	}
	parts := make([]string, 0, 4+len(s.extra))
	for _, p := range []string{s.scope, status, s.trashed} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	parts = append(parts, s.extra...)
	parts = append(parts, s.rowKinds())
	return strings.Join(parts, " AND ")
}

// views is the table: one row per list view, and the only place a view is
// described.
var views = map[string]viewSpec{
	ViewToday: {
		scope: todayScope, status: openRows, trashed: untrashedRows,
		includesProjects: true, showsUnlogged: true, supportsDateFilter: true,
		dateColumn: todayDate,
		// Today takes the shared grouping, then todayIndexReferenceDate
		// newest first, then todayIndex. Measured against the app on 7 Oct
		// 2026 over a 26-row Today, these keys reproduce its order in every
		// position. The reference date is the day a row's todayIndex was last
		// written, so a row placed today sits above rows carried over from an
		// earlier day (and of two earlier days, the more recent comes first),
		// and todayIndex orders the rows that share a day.
		// Issue #237 dropped the reference date as not a sort key, which put
		// carried-over rows out of place among the day's own.
		//
		// t.status is not a key: the app leaves the closed items the view
		// keeps in place among the open ones, struck through, rather than at
		// the end of their group.
		//
		// A project row scheduled for Today (issue #201) has no parent project
		// of its own, so p.* is NULL and it lands in its area's group ahead of
		// that area's projects. Its own todayIndex then places it among the
		// area's loose to-dos, the same signal everything else here is ordered
		// by.
		//
		// startBucket leads: the app's This Evening section comes after every
		// day row, and inside it the rows group the same way the day rows do.
		// Measured on 9 Oct 2026, six evening rows (a project among them) were
		// the last six of the app's Today, in todayIndex order, where the CLI
		// had them inside the no-area group.
		orderBy: "ORDER BY " + eveningLast + ", " + listGrouping + ", t.todayIndexReferenceDate DESC, t.todayIndex ASC, t.\"index\" ASC" + uuidTiebreak,
	},
	ViewInbox: {
		scope: inboxBucket, status: openRows, trashed: untrashedRows,
		extra: []string{notTodayDue},
		// Closed rows are listed here on the same rule as the other
		// lists: the app's Inbox keeps a to-do closed out of it that day in
		// place, struck through, until the day is logged.
		showsUnlogged: true,
		orderBy:       indexOrderBy,
	},
	ViewUpcoming: {
		scope: upcomingScope, status: openRows, trashed: untrashedRows,
		includesProjects: true, showsUnlogged: true, supportsDateFilter: true,
		// Upcoming is a diary, so it reads by date and not by list position.
		// The app orders it by start date and then by todayIndex, which is the
		// within-day position it also keys Today on; the view listed in bare
		// t."index" order before, which interleaved the dates (issue #217).
		// A to-do there only by its deadline sorts by that day in the same way.
		//
		// Closed rows are listed here on the same rule as Today and
		// Anytime, over the same scope: a to-do closed today stays in Upcoming,
		// struck through, only if it was there while open (issue #293).
		dateColumn: upcomingDate,
		orderBy:    "ORDER BY " + upcomingDate + " ASC, t.todayIndex ASC, t.\"index\" ASC" + uuidTiebreak,
	},
	// Anytime is the one scheduled view that does not carry project rows, and
	// that is the app's own shape rather than an inconsistency. Every active
	// project is trivially "anytime", so listing them all as rows would bury
	// the to-dos; the app uses the project as the group header above its
	// to-dos instead. Measured on 10 Sep 2026: the app's Anytime held none of
	// the 23 active projects as a row, and held all 107 of their to-dos, which
	// the CLI already matched exactly. This revises the widening #205 and #216
	// applied to this view (issue #217). Today keeps its project rows — a
	// project scheduled for a day is a row in the app's Today — and so do
	// upcoming and someday, where a project has actually been put somewhere.
	//
	// Closed rows are listed here on the same rule as Today: the app keeps
	// an item closed today visible in whatever list it was in until the day
	// rolls over, and Anytime is a list like Today (issue #238).
	ViewAnytime: {
		scope: anytimeScope, status: openRows, trashed: untrashedRows,
		extra:         []string{parentNotDeferred},
		showsUnlogged: true, supportsDateFilter: true,
		// Anytime groups the way the app presents it: the project is the
		// header above its own to-dos, not a row among them. The view listed
		// in bare t."index" order before, so a project's to-dos interleaved
		// with everything else and the rendered header repeated (issue #217).
		// Inside a project group the rows follow projectPageOrder, so a
		// project's to-dos under no heading come before each heading's, as on
		// the project's own page. Measured on 9 Oct 2026, the app listed a
		// project's to-dos that way where the CLI ordered them by index alone.
		orderBy: "ORDER BY " + listGrouping + ", " + projectPageOrder + uuidTiebreak,
	},
	// Someday is the app's list of deferred things you have not filed under a
	// project. A to-do inside a project stays inside it however it is deferred:
	// the app shows it greyed within the project and keeps it out of the global
	// Someday list, so unparented is the parity rule (issue #211).
	// Measured against the app rather than assumed — the discriminating case is
	// a Someday to-do whose parent project is itself in Someday, and the app
	// hides that one too, so the test is the presence of a parent, not the
	// parent's own bucket. Project rows have no parent project, so they pass and
	// stay listed (issue #206). Resolving p through COALESCE(t.project,
	// h.project) means a to-do under a project heading is filed by its heading's
	// project, not left looking unparented.
	ViewSomeday: {
		scope: somedayDeferred, status: openRows, trashed: untrashedRows,
		extra:                []string{unparented, notTodayDue},
		includesProjects:     true,
		rejectsProjectFilter: true,
		showsUnlogged:        true,
		// Someday is arranged like today and anytime. Its filter keeps only
		// rows with no parent project, so the two project keys are constant
		// across the listing and it reduces to unfiled items, then areas, then
		// t."index" — but it is written with the shared constant rather than a
		// trimmed copy, so the three views cannot drift apart. It listed in
		// bare t."index" order before and matched the app in none of its
		// positions (issue #237).
		//
		// Inside a group it takes projectPageOrder, which puts the group's
		// project rows ahead of its loose to-dos. Measured on 9 Oct 2026, the
		// app's Someday opened with its three no-area projects, then the
		// no-area to-dos, where the CLI had the projects after the to-dos.
		orderBy: "ORDER BY " + listGrouping + ", " + projectPageOrder + uuidTiebreak,
	},
	// The Logbook is where Things files everything closed, not just everything
	// finished: cancelling a to-do or a project logs it under its stopDate
	// beside the completed ones, so the view carries status 2 as well as 3
	// (issue #210). Callers tell the two apart by `status`, which reads
	// "cancelled" or "completed" in JSON and prints [~] or [x] in plain output.
	// The Logbook is the complement of what Things has not yet logged
	// (heldInPlace), so a closed item is in the Logbook or still in place,
	// never both: Things moves an item out of its list and into the Logbook
	// at the same moment (issues #230, #238, #293). An item still in place is
	// listed by default by its view, its project or its area,
	// and a closed Anytime project with no area by `things projects` until
	// it is logged. Those listings overlap each other, so a sweep across them
	// dedupes by uuid.
	//
	// A closed project is one row, not a row plus its contents: the app folds
	// the to-dos of a closed project into the project's own Logbook row and
	// lists none of them separately. Measured on 10 Sep 2026, the app's Logbook
	// held no to-do at all whose parent project was closed, against 328 such
	// rows in the CLI (issue #229). The fold waits for the project itself to
	// be logged: until then there is no logged row to fold into, and the app
	// lists the project's already-logged to-dos on their own (see
	// parentNotClosedOrUnlogged). The trashed-parent half of the fold is the
	// clause buildListQuery appends for every view.
	ViewLogbook: {
		status: closedRows, trashed: untrashedRows,
		extra:             []string{notHeldInPlace, parentNotClosedOrUnlogged},
		includesProjects:  true,
		includesTemplates: true,
		orderBy:           "ORDER BY t.stopDate DESC, t.\"index\" ASC" + uuidTiebreak,
	},
	// Trash carries projects as well as to-dos: trashing a project in the
	// app puts the project row itself in Trash, and `things projects` filters
	// trashed rows, so pinning to-dos only here left a trashed project visible
	// nowhere (issue #212).
	//
	// Trash folds a trashed project's children into its row, the way the
	// Logbook folds a closed project's, and that fold is the trashed-parent
	// clause buildListQuery appends. It deliberately does not fold a *closed*
	// project's children: the app's Trash held 23 to-dos whose parent project
	// was closed but not trashed, and none whose parent was trashed. Throwing
	// away a to-do out of a finished project is an ordinary thing to do, and
	// the project is not in Trash to fold it into (issue #229).
	ViewTrash: {
		trashed: trashedRows,
		// No status test: a trashed row is in Trash whatever state it is in.
		includesProjects:        true,
		includesTemplates:       true,
		keepsTrashedParentGuard: true,
		// Most recently thrown away first. Measured on 9 Oct 2026, the app's
		// Trash was in userModificationDate order, newest first, over all
		// 1488 rows, where the CLI listed by index. The trashed-parent fold
		// above decides which rows are listed, not their order, so it stays.
		orderBy: "ORDER BY t.userModificationDate DESC, t.\"index\" ASC" + uuidTiebreak,
	},
	// Deadlines carries projects too: a project takes a deadline exactly as a
	// to-do does, `things projects` reports it, and agents.md advertises this
	// view as the way to sweep what is due, so pinning to-dos only hid every
	// project deadline from the sweep (issue #213). The view orders by
	// t.deadline, so project rows fall in among the to-dos by date rather than
	// forming a block of their own.
	ViewDeadlines: {
		scope: hasDeadline, status: openRows, trashed: untrashedRows,
		includesProjects: true, supportsDateFilter: true,
		dateColumn: "t.deadline",
		orderBy:    "ORDER BY t.deadline ASC, t.\"index\" ASC" + uuidTiebreak,
	},
	// Things' Repeating list: the templates that generate to-dos and
	// projects, not the items they generate. A template carries the
	// recurrence rule; each generated instance is an ordinary row with no
	// rule of its own, so isTemplate selects templates alone (issue #147). It
	// carries projects as well as to-dos: a project can repeat too, and the
	// app's Repeating list shows both kinds, so the view carries project
	// templates and `things projects` leaves them out (issue #165).
	ViewRepeating: {
		scope: isTemplate, status: openRows, trashed: untrashedRows,
		includesProjects:  true,
		includesTemplates: true,
		// Repeating holds both to-dos and projects. Ordering by type first
		// keeps the two kinds in contiguous blocks instead of interleaving
		// them by an index that is only meaningful within a kind.
		orderBy: "ORDER BY t.type ASC, t.\"index\" ASC" + uuidTiebreak,
	},
	// The catch-all open set: also the default view for a bare --project/
	// --area/--tag filter. `things --project X` on a closed or trashed project
	// widens past "open" — see closedProjectContents. It carries projects for
	// the same reason the named views do — `things --area Work` is a sweep of
	// that area, and the area's own projects are part of what the app shows
	// there (issue #222). A --project filter still returns no project rows: a
	// project has no parent project of its own, so p.uuid never matches.
	ViewProject: {
		status: openRows, trashed: untrashedRows,
		includesProjects: true, supportsDateFilter: true,
		widensToProjectContents: true, completesWithFilter: true,
		// A filter that spans projects — `things --area X`, `things --tag y` —
		// groups the way the lists do (listGrouping), so the rendered group
		// headers stay contiguous instead of repeating as rows interleave by
		// index: an area's projects and loose to-dos first, then its projects'
		// to-dos project by project. Within each, and so the whole of a
		// single-project listing, the rows follow projectPageOrder. Measured on
		// 9 Oct 2026 against `to dos of area` and `to dos of tag`, the app put
		// an area's open projects first, then its loose to-dos; the CLI had
		// the projects' to-dos first and the project rows among the loose
		// to-dos by index.
		orderBy: "ORDER BY " + listGrouping + ", " + projectPageOrder + uuidTiebreak,
	},
}

// projectPageOrder is how the app arranges the to-dos of one project on the
// project's page. The to-dos under no heading come first, then each heading's
// in heading order. Within each of those the Anytime to-dos come first by
// index, a to-do scheduled for today among them, even one Things has not yet
// moved out of start = 2; then the scheduled ones by start date and then todayIndex, the keys Upcoming orders by; then the
// Someday ones by index. The heading's uuid follows its index so that two
// headings sharing an index still keep their to-dos apart.
//
// Measured on 3 Oct 2026 against `to dos of project id X`, the CLI's old
// t.start, t."index" order differed from the app's in 7 of 20 open projects,
// mostly because it ordered the scheduled to-dos by index rather than date.
// No open project had a heading, so the heading keys come from a throwaway
// project built in Things for the purpose.
//
// The same order arranges an area's own rows, where project rows sit beside
// loose to-dos: in the Anytime and Someday parts a project row comes before
// the to-dos, and a scheduled one takes its place by date like a to-do.
// Measured on 9 Oct 2026 against `to dos of area` and `to dos of tag` (open
// projects first, then the loose to-dos, a project scheduled for a later day
// among the to-dos of its day) and the app's Someday (its projects first). A
// project page holds no project rows, so the key is constant there.
var projectPageOrder = `CASE WHEN t.heading IS NULL THEN 0 ELSE 1 END, COALESCE(h."index", 0), COALESCE(h.uuid, ''), ` +
	`CASE WHEN ` + upcomingScheduled + ` THEN 1 WHEN ` + somedayDeferred + ` THEN 2 ELSE 0 END, ` +
	`CASE WHEN ` + upcomingScheduled + ` THEN t.startDate END, ` +
	`CASE WHEN ` + upcomingScheduled + ` THEN t.todayIndex END, ` +
	fmt.Sprintf("CASE WHEN t.type = %d THEN 0 ELSE 1 END, ", int(model.TypeProject)) +
	`t."index" ASC`

// notHeading excludes project headings (TMTask type 2) from the lookup
// queries. The inbox view pins t.type = 0 outright, but a lookup has to keep
// returning projects as well as to-dos — show, edit, complete, cancel and
// open all resolve projects through GetTask/GetTaskByUUID — so it excludes the
// heading type rather than pinning the task type (issue #146).
// The int() conversion is a guard rather than a requirement. %d formats the
// integer whether or not the type is a fmt.Stringer — only %v, %s, %q and %x
// consult String() — so the conversion changes nothing today. It stays so that
// changing the verb later cannot quietly splice the word `heading` into the
// SQL in place of `2` (issue #224).
var notHeading = fmt.Sprintf("COALESCE(t.type, 0) != %d", int(model.TypeHeading))

// uuidTiebreak is the last key of every ordering in this package. It makes the
// order total: rows that tie on every other key still come back in one fixed
// sequence. Numbered listings are the reason — cacheTaskUUIDs numbers rows by
// position, so `things complete 3` acts on whatever row landed third, and an
// order SQLite leaves undefined let two identical listings number the same
// rows differently (issue #221).
//
// It is a named constant rather than inline text so a test can assert that
// every ordering carries it; the tiebreak itself is invisible in results on
// any data that has no ties, so nothing else would catch its removal.
const uuidTiebreak = `, t.uuid ASC`

// listGrouping reproduces how the app arranges a list of to-dos. Today,
// Anytime and Someday all take it: measured against the app on 10 Sep 2026,
// the three lists are arranged identically (issues #217, #237). The catch-all
// view behind `--area` and `--tag` takes it too, so those sweeps group the
// same way. The two keys that ask "filed here at all?" are a CASE rather than a plain index
// because Things' own indexes are negative, so the COALESCE default of 0 that
// stands for "not filed here" would sort last where the app puts it first:
//
//  1. the items with no area lead the list: loose to-dos filed nowhere, and
//     the to-dos of a project that carries no area;
//  2. then areas, in area order;
//  3. and inside a group its own loose to-dos come before those of its
//     projects, which then follow project by project.
//
// The first key reads the area off the row and off its project, so an
// area-less project's to-dos join the no-area group rather than taking the
// area key's COALESCE default of 0 and landing after every area, which real
// areas' negative indexes made them do. It reads the joined areas, as the
// rendered row does, so an area uuid that names no area counts as no area.
// Checked against the app on 8 Oct 2026: it listed an area-less project's
// to-dos at the top, beside the loose ones, where the CLI had them near the
// end.
//
// Project groups follow one another by the project's own schedule before its
// index: an Anytime project's group first, then a project scheduled for a
// later day (earliest first), then a Someday project's. Measured on 9 Oct
// 2026, the app's Today listed the to-dos of a project scheduled for 15 Oct
// (index 0) ahead of those of a Someday project (index -369), and those ahead
// of another Someday project's (index 0); the CLI's index order had the first
// two the other way round. That is one measured ordering, read as the
// buckets projectPageOrder already uses; it wants re-measuring against more
// projects. Anytime leaves out the to-dos of a deferred project and Someday
// those of every project, so the key only reorders Today and the catch-all.
// The uuid after each index keeps two areas, or two projects, that share an
// index from interleaving.
//
// The caller adds its own within-group key after these — projectPageOrder for
// anytime, someday and the catch-all, todayIndexReferenceDate then todayIndex
// for today — and the uuid tiebreak last. Together they put a project's to-dos
// in one contiguous block, so the rendered group header prints once above
// them, which is the app's own presentation of a project in these lists.
var listGrouping = `CASE WHEN a.uuid IS NULL AND pa.uuid IS NULL THEN 0 ELSE 1 END, ` +
	`COALESCE(a."index", pa."index", 0), COALESCE(a.uuid, pa.uuid, ''), ` +
	`CASE WHEN p.uuid IS NULL THEN 0 ELSE 1 END, ` +
	`CASE WHEN ` + projectScheduled + ` THEN 1 WHEN ` + projectDeferred + ` THEN 2 ELSE 0 END, ` +
	`CASE WHEN ` + projectScheduled + ` THEN p.startDate END, ` +
	`COALESCE(p."index", 0), COALESCE(p.uuid, '')`

// projectScheduled and projectDeferred are upcomingScheduled and
// somedayDeferred asked of the row's project, so a project group sorts by the
// same buckets the rows inside it do.
var (
	projectScheduled = scheduledLaterOf("p")
	projectDeferred  = deferredUndatedOf("p")
)

// eveningLast is Today's first key: the This Evening rows (startBucket 1)
// after every day row.
const eveningLast = "COALESCE(t.startBucket, 0) ASC"

// indexOrderBy is the ordering for the view with no arrangement of its own:
// inbox names it in the view table and lists in index order. There is
// no fallback behind it any more: a view whose spec leaves orderBy empty
// would run with no ORDER BY at all, which is what
// TestEveryTaskOrderingEndsInTheUUIDTiebreak is there to catch.
//
// SearchTasks takes it too: its results are
// numbered out of the same cache. uuidTiebreak closes the same gap here,
// because t."index" repeats across lists and two rows in one listing can share
// it.
const indexOrderBy = `ORDER BY t."index" ASC` + uuidTiebreak

// untrashedParent excludes to-dos whose project is in the trash, in every
// view. Trashing a project in Things leaves its child rows at trashed = 0, so
// without this they outlive the project and go on listing as ordinary open
// tasks (issue #155, the same class as #142/#143).
//
// trash and logbook used to be exempt, on the argument that they report what
// the database holds. The app disagrees: measured on 10 Sep 2026 it showed no
// such row in either list, folding those to-dos into the trashed project's own
// row instead — 77 rows in the CLI's logbook and 4 in its trash (issue #229).
// With the exemption gone the clause is unconditional.
const untrashedParent = "COALESCE(p.trashed, 0) = 0"

// The list view names, which key the views table. They are plain strings so
// the callers that carry a view as text — argv, the output layer — need no
// conversion.
const (
	ViewToday     = "today"
	ViewInbox     = "inbox"
	ViewUpcoming  = "upcoming"
	ViewAnytime   = "anytime"
	ViewSomeday   = "someday"
	ViewLogbook   = "logbook"
	ViewTrash     = "trash"
	ViewDeadlines = "deadlines"
	ViewRepeating = "repeating"
	// ViewProject is the catch-all open set, not a name a user can type. It is
	// what a bare --project/--area/--tag filter lists.
	ViewProject = "project"
)

// ValidView reports whether the name is one of the list views.
func ValidView(name string) bool {
	_, ok := views[name]
	return ok
}

// closedProjectContents is the catch-all view's WHERE clause when --project
// names a project that is itself closed or trashed. Asking for such a project's
// contents and getting nothing back is the wrong answer: since issue #229 the
// Logbook and Trash fold a closed or trashed project's to-dos into the project
// row, so naming the project is the only way left to reach them, and the
// catch-all view's "t.status = 0" would return none.
//
// It is what the app answers. `to dos of project id <closed project>` returned
// 78 for a project holding 65 completed, 13 cancelled and 3 trashed children,
// and 49 for one holding 42 and 7 — so the status pin drops and t.trashed = 0
// stays. A trashed child of a closed project is in Trash on its own account
// and is not part of the project's contents.
//
// The parent test is evaluated against the named project because --project
// constrains p to it. When that project is open the clause reduces to the
// ordinary open set, so `things --project <open project>` is unchanged.
//
// Unless OpenOnly is set, an open project also lists the to-dos closed today
// and not yet logged, through openOrJustClosed, so it cannot answer that
// question differently from today and anytime. The app keeps such a to-do in
// its project whatever list it was closed out of: measured on 3 Oct 2026,
// `to dos of project id X` held all of them in three open projects, two closed
// ahead of their date out of Upcoming among them (issue #295). The project is
// named, so the closed-parent fold does not apply.
func closedProjectContents(includeCompleted bool) string {
	status := openOrJustClosed(whereOpts{includeCompleted: includeCompleted, projectNamed: true}, false)
	return "(" + parentClosedOrTrashed + " OR " + status + ") AND t.trashed = 0 AND " + todoOrProject
}

// parentClosedOrTrashed is true for a row whose parent project has been closed
// or thrown away — parentClosed widened to take the trash in too, so the two
// cannot drift apart.
//
// Both halves COALESCE so an unparented row — p.uuid NULL — reads false rather
// than NULL. A bare "p.status IN (2, 3)" would be NULL there, and NULL OR
// false is NULL, which would drop every closed unparented row out of any
// caller that ORs this with a status test.
const parentClosedOrTrashed = "(" + parentClosed + " OR COALESCE(p.trashed, 0) = 1)"

func (d *DB) ListTasks(view string, opts TaskFilter) ([]model.Task, error) {
	query, args, err := d.buildListQuery(view, opts)
	if err != nil {
		return nil, err
	}
	return d.collectTasks(query, args...)
}

// likeEscapeChar is the escape character the filter LIKE patterns declare. A
// backslash is not special to SQLite's LIKE by default — the escape character
// only exists because ESCAPE names one — so it needs escaping in the value too
// once it has that meaning.
const likeEscapeChar = `\`

// escapeClause is appended to every LIKE that matches a filter value. Without
// it the value is a pattern: `things --project '%'` matched every project, and
// a title holding a `%` or a `_` matched more than itself (issue #262). It is
// built from likeEscapeChar so the clause and the escaping below cannot name
// different characters.
const escapeClause = ` ESCAPE '` + likeEscapeChar + `'`

// likeEscaper rewrites the LIKE wildcards, and the escape character itself,
// into their escaped forms. A Replacer is immutable and safe for concurrent
// use, so it is built once rather than per filter.
//
// It runs in one pass and does not rescan what it writes, so the escape
// character is handled in the same pass as the wildcards without the doubled
// backslash being escaped again.
var likeEscaper = strings.NewReplacer(
	likeEscapeChar, likeEscapeChar+likeEscapeChar,
	"%", likeEscapeChar+"%",
	"_", likeEscapeChar+"_",
)

// literalLike turns a filter value into a LIKE pattern matching it and nothing
// else. The value is case-folded first, and every LIKE it feeds folds its
// column with fold(), so matching ignores case beyond ASCII — LIKE's own case
// rule covers ASCII only, and missed "ärger" against "Ärger". Only the
// wildcards lose their meaning.
func literalLike(value string) string {
	return likeEscaper.Replace(FoldCase(value))
}

// nameLike is literalLike for the --tag, --area and --project filters: the
// value is trimmed first, the way FoldTag trims it for `open`, so the two
// paths agree on a value with a stray space (issue #289).
func nameLike(value string) string {
	return literalLike(strings.TrimSpace(value))
}

// listNameLike is nameLike for --area and --project, folded with FoldName to
// match a column folded with fold_name(). It escapes after folding, since a
// compatibility form such as fullwidth "％" folds to a wildcard.
func listNameLike(value string) string {
	return likeEscaper.Replace(FoldName(strings.TrimSpace(value)))
}

// foldsIntoTaggedProject reports whether a --tag listing has to match a
// project row through its tagged to-dos: when --area and --tag together list
// the area's page, which folds a to-do closed today into its project closed
// today. Measured on 9 Oct 2026: with a tagged to-do completed today inside
// an untagged project completed today, both in the area, the app's area page
// held the project row, struck through, and not the to-do. Matching the tag
// on rows alone dropped both, since the fold took the to-do and the tag the
// project.
func foldsIntoTaggedProject(spec viewSpec, opts TaskFilter) bool {
	return opts.Area != "" && opts.Project == "" && !opts.OpenOnly && spec.completesWithFilter
}

// projectOfFoldedTagged, followed by the tagged-rows subquery and "))" to
// close its two open subqueries, matches a closed project row holding a to-do the tag carries that
// the area's fold takes into it: closed, untrashed, and not yet logged. It is
// filed in the project directly or under one of its headings. The project's
// own row still has to pass the view's status test, so only a project closed
// and not yet logged is listed.
var projectOfFoldedTagged = "t.type = 1 AND " + closedRows + " AND t.uuid IN (SELECT COALESCE(c.project, ch.project) FROM TMTask c" +
	" LEFT JOIN TMTask ch ON c.heading = ch.uuid WHERE c.status IN (2, 3) AND c.trashed = 0 AND " +
	closedUnloggedOf("c") + " AND c.uuid IN ("

// projectNameMatch is the --project clause for the project row aliased alias,
// with its arguments: the uuid, or the title. An exact-case title wins, and
// only when no project carries one does the title match under FoldName,
// ignoring surrounding space — the order `open --tag/--area` take names in. Things lets
// two projects differ only by case, and without the preference naming one
// listed both (issue #290). A trashed project does not take the preference:
// the app no longer shows it, so it must not hide an open project that
// differs from it only by case. Named exactly, it still matches.
func projectNameMatch(alias, ref string) (string, []any) {
	clause := "(" + alias + ".uuid = ? OR nfc(" + alias + ".title) = ? OR (fold_name(" + alias + ".title) LIKE ?" + escapeClause +
		" AND NOT EXISTS (SELECT 1 FROM TMTask px WHERE px.type = ? AND px.trashed = 0 AND nfc(px.title) = ?)))"
	return clause, []any{ref, normName(ref), listNameLike(ref), int(model.TypeProject), normName(ref)}
}

// areaNameMatch is the --area clause for an area's uuid and title columns,
// with its arguments, and tagNameMatch the --tag condition on a tag's title.
// Both take names the way projectNameMatch does: an exact-case title wins,
// and only when no area or tag carries one does the title match ignoring case
// and surrounding space. An area also matches across compatibility forms
// (FoldName), as in Things; a tag does not. Areas and tags are never trashed,
// so every row takes the preference.
func areaNameMatch(uuidCol, titleCol, ref string) (string, []any) {
	clause := "(" + uuidCol + " = ? OR nfc(" + titleCol + ") = ? OR (fold_name(" + titleCol + ") LIKE ?" + escapeClause +
		" AND NOT EXISTS (SELECT 1 FROM TMArea ax WHERE nfc(ax.title) = ?)))"
	return clause, []any{ref, normName(ref), listNameLike(ref), normName(ref)}
}

func tagNameMatch(titleCol, ref string) (string, []any) {
	clause := "(nfc(" + titleCol + ") = ? OR (fold(" + titleCol + ") LIKE ?" + escapeClause +
		" AND NOT EXISTS (SELECT 1 FROM TMTag gx WHERE nfc(gx.title) = ?)))"
	return clause, []any{normName(ref), nameLike(ref), normName(ref)}
}

// containsLike is literalLike for the lookups that match a substring: the
// value is escaped so nothing inside it is a wildcard, then wrapped in the two
// the caller did not type. `%` and `_` in the value are characters to find,
// not pattern syntax (issue #267).
func containsLike(value string) string {
	return "%" + literalLike(value) + "%"
}

// buildListQuery composes a view's WHERE clause with the caller's filters and
// the view's ORDER BY, and returns the SQL ListTasks runs. Splitting it out of
// ListTasks gives the golden SQL test something to call: the text this returns
// is the contract every list view rests on, and TestListQueryGoldenSQL pins
// all of it (issue #240).
func (d *DB) buildListQuery(view string, opts TaskFilter) (string, []any, error) {
	spec, ok := views[view]
	if !ok {
		return "", nil, fmt.Errorf("unknown view: %s", view)
	}
	// The spec answers OpenOnly itself, on the views that show unlogged rows,
	// so there is no per-view branch here to keep in step with the table.
	where := spec.where(whereOpts{
		includeCompleted: !opts.OpenOnly,
		projectNamed:     opts.Project != "",
		areaNamed:        opts.Area != "",
		tagNamed:         opts.Tag != "",
	})
	// untrashedParent is what stops a trashed project's children outliving it
	// in the lists. Naming a project is asking for that project's contents, so
	// the guard comes off there — the same lift #260 made to the closed-parent
	// fold, and for the same reason: a trashed or closed project's children are
	// listed nowhere by default, and --project is how they are reached (issue
	// #263). With p pinned to the named project the clause is a constant, true
	// for an untrashed project and false for a trashed one, so lifting it can
	// only ever affect the trashed case.
	//
	// The catch-all view goes further, widening past the open set as well: it
	// answers with the project's whole contents rather than a slice of a list.
	// Trash goes the other way and keeps the guard — see its case below.
	switch {
	case opts.Project != "" && spec.widensToProjectContents:
		where = closedProjectContents(!opts.OpenOnly)
	case opts.Project == "" || spec.keepsTrashedParentGuard:
		// Trash keeps the guard even under --project. Its rows are the ones
		// thrown away on their own account, and a to-do thrown away out of a
		// project that is itself in the Trash is reachable nowhere, as in the
		// app — the line README, agents.md and SKILL.md all state. Lifting it
		// here would surface exactly that row, and contradict the catch-all
		// view's own answer for the same project: closedProjectContents pins
		// t.trashed = 0, so such a child is not part of the contents either.
		where += " AND " + untrashedParent
	}
	if !spec.includesTemplates {
		where += " AND " + notATemplate
	}

	var args []any
	if opts.Project != "" {
		clause, clauseArgs := projectNameMatch("p", opts.Project)
		where += " AND " + clause
		args = append(args, clauseArgs...)
	}
	if opts.Area != "" {
		clause, clauseArgs := areaNameMatch("COALESCE(a.uuid, pa.uuid)", "COALESCE(a.title, pa.title)", opts.Area)
		where += " AND " + clause
		args = append(args, clauseArgs...)
	}
	if opts.Tag != "" {
		clause, clauseArgs := tagNameMatch("tg2.title", opts.Tag)
		tagged := "SELECT tt2.tasks FROM TMTaskTag tt2 JOIN TMTag tg2 ON tt2.tags = tg2.uuid WHERE " + clause
		if foldsIntoTaggedProject(spec, opts) {
			// The area's page folds a to-do closed today into its project
			// closed today, so the tag reaches that project's row through the
			// to-do, or the listing loses both.
			where += " AND (t.uuid IN (" + tagged + ") OR (" + projectOfFoldedTagged + tagged + "))))"
			args = append(args, clauseArgs...)
		} else {
			where += " AND t.uuid IN (" + tagged + ")"
		}
		args = append(args, clauseArgs...)
	}

	if opts.On != nil || opts.From != nil || opts.To != nil {
		// ThingsDate is bit-encoded year<<16|month<<12|day<<7 — directly
		// comparable across (year, month, day), so no decode is needed.
		col := spec.dateColumn
		if col == "" {
			col = "t.startDate"
		}
		if opts.On != nil {
			where += " AND " + col + " = ?"
			args = append(args, int64(*opts.On))
		}
		if opts.From != nil {
			where += " AND " + col + " >= ?"
			args = append(args, int64(*opts.From))
		}
		if opts.To != nil {
			where += " AND " + col + " <= ?"
			args = append(args, int64(*opts.To))
		}
	}

	// The recurrence column varies across Things schema versions, so the
	// filters carry a placeholder that only a live DB can resolve. On a
	// schema with no such column it degrades to NULL: "IS NULL" makes the
	// exclusion a no-op and "IS NOT NULL" leaves the repeating view empty.
	where = fillRepeating(where, d.recurrenceCol(), d.recurrenceColFor("p"))

	query := d.taskQuery() + " WHERE " + where + " GROUP BY t.uuid " + spec.orderBy
	return query, args, nil
}

func (d *DB) GetTaskByUUID(uuid string) (*model.Task, error) {
	query := d.taskQuery() + " WHERE t.uuid = ? AND " + notHeading + " GROUP BY t.uuid"
	row := d.queryRow(query, uuid)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying task by uuid: %w", err)
	}
	return &t, nil
}

// StoredStart returns the start the item is stored with. The task queries
// report shownStart instead, which reads a row Things has not moved into
// today yet as Anytime; the edit checks need to tell the two apart. A missing
// item reads as the Inbox.
func (d *DB) StoredStart(uuid string) (model.Start, error) {
	var start int
	err := d.queryRow(`SELECT COALESCE(start, 0) FROM TMTask WHERE uuid = ?`, uuid).Scan(&start)
	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("reading start: %w", err)
	}
	return model.Start(start), nil
}

// ReminderTime returns the item's raw reminder time (model.ReminderClock
// decodes it), and whether it has one. Things sets it from a --when time and
// clears it when a --when for today arrives without one, so the edit no-op
// check needs it. Listings read it through the task query instead. A missing
// item has none.
func (d *DB) ReminderTime(uuid string) (int64, bool, error) {
	var raw sql.NullInt64
	err := d.queryRow(`SELECT reminderTime FROM TMTask WHERE uuid = ?`, uuid).Scan(&raw)
	switch {
	case err == sql.ErrNoRows:
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("reading reminder: %w", err)
	}
	return raw.Int64, raw.Valid, nil
}

// uuidChunkSize caps how many uuids go into one IN (...) clause, so a caller
// passing an arbitrarily long list can never trip SQLITE_MAX_VARIABLE_NUMBER —
// the bound parameter limit is a property of the SQLite build, not something
// this package can rely on, and hitting it would surface as an opaque query
// error. 500 sits well under every plausible limit — the modernc.org/sqlite
// build this module pins accepts 32766 (the modern SQLITE_MAX_VARIABLE_NUMBER)
// and refuses 40000 with "too many SQL variables" — and the per-chunk cost is
// flat, so nothing is lost by staying conservative. Import payloads are
// bounded by the macOS URL length limit anyway, so in practice one chunk
// covers them and the loop never runs twice.
const uuidChunkSize = 500

// TasksCreatedSince returns the items of typ created at or after since,
// oldest first, leaving out trashed rows, repeating templates and the
// instances Things generates from them. It is how a write that returns no
// uuid, such as things:///add, finds the item it created.
func (d *DB) TasksCreatedSince(typ model.TaskType, since time.Time) ([]model.Task, error) {
	query := d.taskQuery() + ` WHERE t.creationDate >= ? AND COALESCE(t.type, 0) = ? AND COALESCE(t.trashed, 0) = 0 AND ` + d.recurrenceCol() + ` IS NULL`
	if d.templateColumn != "" {
		query += ` AND ` + recurrenceRef("t", d.templateColumn) + ` IS NULL`
	}
	query += ` GROUP BY t.uuid ORDER BY t.creationDate` + uuidTiebreak
	return d.collectTasks(query, model.TimeToUnix(since), int(typ))
}

// GetTasksByUUIDs looks up many tasks in one query instead of one query per
// uuid, and returns them keyed by uuid. An id that matches nothing is simply
// absent from the map — the caller decides whether that is an error, the same
// way GetTaskByUUID returns a nil task rather than failing.
//
// It shares taskQuery and the notHeading filter with GetTaskByUUID so the two
// agree on what a lookup can see: headings stay excluded (issue #146) and the
// repeating column is resolved the same way. Duplicate and empty ids are
// dropped before querying, so callers can pass a raw list.
func (d *DB) GetTasksByUUIDs(uuids []string) (map[string]*model.Task, error) {
	found := make(map[string]*model.Task, len(uuids))

	unique := make([]string, 0, len(uuids))
	seen := make(map[string]struct{}, len(uuids))
	for _, u := range uuids {
		if u == "" {
			continue
		}
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}
		unique = append(unique, u)
	}

	for start := 0; start < len(unique); start += uuidChunkSize {
		end := min(start+uuidChunkSize, len(unique))
		chunk := unique[start:end]

		args := make([]any, len(chunk))
		for i, u := range chunk {
			args[i] = u
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		query := d.taskQuery() + " WHERE t.uuid IN (" + placeholders + ") AND " + notHeading + " GROUP BY t.uuid"

		tasks, err := d.collectTasks(query, args...)
		if err != nil {
			return nil, err
		}
		for i := range tasks {
			task := tasks[i]
			found[task.UUID] = &task
		}
	}
	return found, nil
}

// GetTask resolves a reference to one task: a uuid, then an exact title,
// then a substring of a title. Both title paths report an AmbiguousTaskError
// when the reference leaves more than one candidate standing, so the caller
// picks by uuid rather than being handed a row this package chose for it.
//
// The substring path only sees open rows outside the Trash, and it is only
// tried when no row of any status carries the exact title (see GetTaskExact):
// `complete "zz base"` with "zz base" completed must not close the open
// "zz base extra" instead.
func (d *DB) GetTask(uuidOrTitle string) (*model.Task, error) {
	t, err := d.GetTaskExact(uuidOrTitle)
	var notFound *TaskNotFoundError
	if !errors.As(err, &notFound) {
		return t, err
	}

	// Exact titles compare case and space as typed, while the substring
	// match below ignores case. So before it runs, the title is matched
	// once more with case and surrounding space set aside. An open row
	// matching that way is the task meant, as the substring match would
	// have found it; failing that, a closed or trashed row stops the lookup:
	// `complete "pay rent"` with "Pay rent" completed must not close the
	// open "Re: Pay rent deposit".
	folded, err := d.findTasksByFoldedTitle(uuidOrTitle)
	if err != nil {
		return nil, err
	}
	if t, found, err := pickTitleMatch(uuidOrTitle, folded); found {
		return t, err
	}

	// Try LIKE match — return all matches for disambiguation
	matches, err := d.FindTasksByTitle(uuidOrTitle)
	if err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, &TaskNotFoundError{Query: uuidOrTitle}
	case 1:
		return &matches[0], nil
	default:
		return nil, &AmbiguousTaskError{Query: uuidOrTitle, Matches: matches}
	}
}

// GetTaskExact is GetTask without the substring fallback: a UUID or a title
// that is exactly the reference, and a *TaskNotFoundError otherwise. A
// reference that must not be guessed at, such as an all-digit one that was not
// a list row, resolves through this (issue #375).
//
// An exact title is looked for among open rows outside the Trash first, and
// any such row wins, as `add` treats a title that also exists completed. Only
// when there is none do closed or trashed rows with the title count: they are
// reported as a *ClosedTitleError, so the caller can show or refuse them,
// rather than the lookup falling through to a substring match on some other,
// open, task.
func (d *DB) GetTaskExact(uuidOrTitle string) (*model.Task, error) {
	t, err := d.GetTaskByUUID(uuidOrTitle)
	if err != nil {
		return nil, err
	}
	if t != nil {
		return t, nil
	}

	all, err := d.findTasksByExactTitle(uuidOrTitle)
	if err != nil {
		return nil, err
	}
	if t, found, err := pickTitleMatch(uuidOrTitle, all); found {
		return t, err
	}
	return nil, &TaskNotFoundError{Query: uuidOrTitle}
}

// pickTitleMatch is the decision both title lookups make over the rows a
// title matched. A single open row outside the Trash is the task; several
// are an *AmbiguousTaskError. With none, closed or trashed rows are a
// *ClosedTitleError. A repeating template gives way to an instance of its
// kind first (preferInstances). found is false only when there are no rows
// to decide over, and the caller goes on to its next lookup.
func pickTitleMatch(query string, rows []model.Task) (t *model.Task, found bool, err error) {
	open, closed := splitOpen(rows)
	if candidates := preferInstances(open); len(candidates) > 0 {
		if len(candidates) == 1 {
			return &candidates[0], true, nil
		}
		return nil, true, &AmbiguousTaskError{Query: query, Matches: candidates}
	}
	if candidates := preferInstances(closed); len(candidates) > 0 {
		return nil, true, &ClosedTitleError{Query: query, Matches: candidates}
	}
	return nil, false, nil
}

// ClosedTitleError reports a reference whose exact title is carried only by
// closed or trashed rows (or to-dos in a trashed project), with no open row
// sharing it. GetTask also reports it for a title that matches such rows
// only once case and surrounding space are ignored. The lookup stops there rather than falling through to a
// substring match on some other, open, task. The caller decides what the
// rows are good for: a read can show one, a write refuses them.
type ClosedTitleError struct {
	Query   string
	Matches []model.Task
}

func (e *ClosedTitleError) Error() string {
	return fmt.Sprintf("no open task is titled %q; %d closed or trashed task(s) are", e.Query, len(e.Matches))
}

// findTasksByExactTitle returns every task carrying exactly this title, of
// any status and in the Trash or not; GetTaskExact splits the open rows
// outside the Trash from the rest, and only reads the rest when there are no
// open ones. A to-do whose project is in the Trash counts with the rest,
// since Things shows it only in the Trash.
// It used to be a LIMIT 1 query, which made the ordering the whole decision
// and hid the rest of the matches: a project and a to-do can share a title,
// and whichever sorted first became the write target with no sign that a
// second candidate existed, so `things project edit <title>` could land on the
// to-do (issue #194). The ordering is still templatesLastOrder so callers that
// do take the first row read the rows in the order the rest of the package
// uses.
func (d *DB) findTasksByExactTitle(title string) ([]model.Task, error) {
	query := d.taskQuery() + " WHERE nfc(t.title) = ? AND " + notHeading +
		" GROUP BY t.uuid " + d.templatesLastOrder()
	return d.collectTasks(query, normName(title))
}

// splitOpen separates the open rows outside the Trash from the rest: closed
// rows, trashed rows, and to-dos whose project is in the Trash, which Things
// shows only there.
func splitOpen(rows []model.Task) (open, closed []model.Task) {
	for _, m := range rows {
		if m.Status == model.StatusOpen && !m.Trashed && !m.ProjectTrashed {
			open = append(open, m)
		} else {
			closed = append(closed, m)
		}
	}
	return open, closed
}

// findTasksByFoldedTitle returns the tasks of any status, in the Trash or
// not, whose title equals title under FoldCase once surrounding space is
// trimmed from both. The SQL narrows the rows to those whose folded title
// contains the folded key; the comparison itself is made in Go, so space
// trims the same way on both sides.
func (d *DB) findTasksByFoldedTitle(title string) ([]model.Task, error) {
	key := FoldCase(strings.TrimSpace(title))
	if key == "" {
		return nil, nil
	}
	query := d.taskQuery() + " WHERE " + notHeading +
		" AND fold(t.title) LIKE ?" + escapeClause + " GROUP BY t.uuid " + d.templatesLastOrder()
	rows, err := d.collectTasks(query, containsLike(strings.TrimSpace(title)))
	if err != nil {
		return nil, err
	}
	var out []model.Task
	for _, r := range rows {
		if FoldCase(strings.TrimSpace(r.Title)) == key {
			out = append(out, r)
		}
	}
	return out, nil
}

// preferInstances drops a repeating template from a set of same-titled matches
// as long as an ordinary row of the same kind is there to take its place, and
// returns the set unchanged otherwise.
//
// This is the one preference the title lookup still makes, and it is the same
// one templatesLastOrder encodes: a repeating to-do exists twice, as the
// template carrying the recurrence rule and as the instance generated from it,
// sharing its title, and the template is never what the reference means
// because writes to it are refused outright (issues #143, #156). Treating that
// pair as ambiguous would put a disambiguation prompt in front of every
// repeating to-do.
//
// The kinds are kept apart because a template only stands in for an instance
// of its own kind: a repeating project and a to-do sharing a title are still
// two candidates, and dropping the project because the to-do is not repeating
// would resolve `things project edit <title>` to the to-do again — the very
// silence issue #194 is about.
func preferInstances(matches []model.Task) []model.Task {
	hasInstance := map[model.TaskType]bool{}
	for _, m := range matches {
		if !m.Repeating {
			hasInstance[m.Type] = true
		}
	}
	out := make([]model.Task, 0, len(matches))
	for _, m := range matches {
		if m.Repeating && hasInstance[m.Type] {
			continue
		}
		out = append(out, m)
	}
	return out
}

// FindTasksByTitle returns the open tasks whose title contains substr,
// leaving out the Trash: a trashed row, and a to-do whose project (directly
// or through its heading) is trashed, since Things shows it only in the
// Trash. A uuid still reaches either, and the writes refuse it. The
// substring is matched literally: the wildcards are escaped and only the two
// the caller never typed, either side of the value, are left as wildcards
// (issue #267). Without that, `_` stood for any character and `%` for any run
// of them, so a lookup could resolve to a task the user did not name — and
// GetTask acts on a lookup that matches exactly one row, which put a status
// write on the wrong task. Matching ignores case, beyond ASCII too (see
// literalLike); nothing documented offered wildcards.
func (d *DB) FindTasksByTitle(substr string) ([]model.Task, error) {
	query := d.taskQuery() + " WHERE " + untrashedRows + " AND " + untrashedParent + " AND " + openRows + " AND " + notHeading + " AND fold(t.title) LIKE ?" + escapeClause +
		" GROUP BY t.uuid " + d.templatesLastOrder()
	return d.collectTasks(query, containsLike(substr))
}

// templatesLastOrder orders a title lookup so repeating templates sort after
// ordinary to-dos, keeping t."index" as the tiebreak the lookups have always
// used, then t.uuid so the order is total. GetTask takes the first row of this
// order as the write target, so two same-titled rows tying on index must not
// resolve differently between two runs (issue #221).
//
// A repeating to-do exists twice: the template carrying the recurrence rule,
// and the instance Things generated from it, sharing its title. The template
// is never what "complete Water plants" means — writes to it are refused
// outright (issue #143) — so a lookup that picked it stranded the user on an
// error while an actionable instance sat next to it (issue #156). Ordering
// rather than filtering keeps the template reachable when it is the only match,
// which is what `things show` on a template-only title should still do.
//
// "<col> IS NOT NULL" yields 0 for instances and 1 for templates, so ASC puts
// instances first. On a schema with no recurrence column the expression is
// "NULL IS NOT NULL" — 0 for every row, leaving the index order untouched.
func (d *DB) templatesLastOrder() string {
	return `ORDER BY ` + d.recurrenceCol() + ` IS NOT NULL ASC, t."index" ASC` + uuidTiebreak
}

// TaskNotFoundError reports a reference that matched no task. It is typed so
// callers can render it as structured output instead of matching on the text.
type TaskNotFoundError struct {
	Query string
}

func (e *TaskNotFoundError) Error() string {
	return fmt.Sprintf("task not found: %s", e.Query)
}

type AmbiguousTaskError struct {
	Query   string
	Matches []model.Task
}

func (e *AmbiguousTaskError) Error() string {
	return fmt.Sprintf("ambiguous task: %q matches %d tasks", e.Query, len(e.Matches))
}

// SearchTasks returns the tasks whose title or notes contain query, matched
// literally for the same reason FindTasksByTitle is: `things search '50%'`
// used to match every title holding a 5 followed by a 0, because the value
// went in as a pattern (issue #267).
//
// It leaves out a to-do whose project, directly or through its heading, is in
// the Trash, as the lists do (untrashedParent): Things shows such a to-do only
// in the Trash, folded into its project. Measured on 9 Oct 2026, the app's
// search did not list one and the CLI's did, under the trashed project.
func (d *DB) SearchTasks(query string) ([]model.Task, error) {
	pattern := containsLike(query)
	q := d.taskQuery() + " WHERE t.trashed = 0 AND " + untrashedParent + " AND " + notHeading + " AND (fold(t.title) LIKE ?" + escapeClause + " OR fold(t.notes) LIKE ?" + escapeClause + ") GROUP BY t.uuid " + indexOrderBy
	return d.collectTasks(q, pattern, pattern)
}

func (d *DB) collectTasks(query string, args ...any) ([]model.Task, error) {
	return queryAll(d, "task", scanTask, query, args...)
}
