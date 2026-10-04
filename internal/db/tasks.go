package db

import (
	"database/sql"
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

	// IncludeCompleted keeps completed/cancelled items that Things has not yet
	// logged out of the list they are in (UI-parity). It reaches the views
	// CompletableView reports — inbox, today, anytime, upcoming, someday, and the catch-all when Project
	// names a project — and without it those return only open tasks. Ignored
	// by every other view.
	IncludeCompleted bool

	On   *model.ThingsDate
	From *model.ThingsDate
	To   *model.ThingsDate
}

// CompletableView reports whether --include-completed applies to the view.
// The answer comes off the view's own spec, and it is the same field that
// widens the status test when the flag is set, so the question the CLI asks
// and the SQL it then runs cannot disagree.
//
// projectNamed is whether --project names a project, and areaNamed whether
// --area names an area. The catch-all view takes the flag only then. Naming a
// project lists its contents (widensToProjectContents), and the app keeps a
// to-do closed today on the project's page (issue #295). Naming an area lists
// the area's page, which keeps one too (see completesWithArea). A bare --tag
// sweep through the same view still rejects it: a tag is a filter in the app,
// not a list with a page of its own, so there is no app answer to match.
func CompletableView(view string, projectNamed, areaNamed bool) bool {
	spec := views[view]
	return spec.supportsIncludeCompleted ||
		(projectNamed && spec.widensToProjectContents) ||
		(areaNamed && spec.completesWithArea)
}

// CompletableViewNames lists those views in a stable order, for error text.
func CompletableViewNames() []string {
	names := make([]string, 0, len(views))
	for name, spec := range views {
		if spec.supportsIncludeCompleted {
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
	t.deadline,
	t.stopDate,
	t.creationDate,
	COALESCE(t.trashed, 0),
	COALESCE(p.uuid, ''),
	COALESCE(p.title, ''),
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

func scanTask(row interface{ Scan(...any) error }) (model.Task, error) {
	var t model.Task
	var startDate, deadline, stopDate, creationDate, modificationDate sql.NullFloat64
	var tagsStr string
	var trashed, repeating, checklistTotal, checklistOpen int

	err := row.Scan(
		&t.UUID, &t.Title, &t.Notes,
		&t.Type, &t.Status, &t.Start, &t.StartBucket,
		&startDate, &deadline, &stopDate, &creationDate,
		&trashed,
		&t.ProjectUUID, &t.ProjectTitle,
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
	t.Repeating = repeating != 0
	t.StartDate = thingsDate(startDate)
	t.Deadline = thingsDate(deadline)
	t.StopDate = unixTime(stopDate)
	t.CreationDate = unixTime(creationDate)
	t.ModificationDate = unixTime(modificationDate)
	if tagsStr != "" {
		t.Tags = strings.Split(tagsStr, "\x1f")
	}
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
const closedTodayUnlogged = `CASE COALESCE((SELECT logInterval FROM TMSettings LIMIT 1), 1)` +
	` WHEN 0 THEN 0` +
	` WHEN 4 THEN ` + closedAfterManualLog +
	` ELSE ` + closedAfterManualLog + ` AND date(COALESCE(t.stopDate, 0), 'unixepoch', 'localtime') = date('now', 'localtime') END`

// closedAfterManualLog is true for a row closed after the last "Log Completed
// Now", the condition the daily and manual choices share.
const closedAfterManualLog = `COALESCE(t.stopDate, 0) > COALESCE((SELECT manualLogDate FROM TMSettings LIMIT 1), 0)`

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

// todayDue is the other way into Today: a to-do with no start date whose
// deadline has arrived, from the Inbox or from Anytime, and for as long as it
// is overdue. Measured on 3 Oct 2026 with test to-dos, the app's Today held
// all four of those shapes and the CLI none. It left out the two whose
// deadlineSuppressionDate equalled their deadline, which is how Things records
// "taken out of Today for this deadline"; the app cleared that column when a
// deadline was changed. No project, and no Someday-bucket to-do, of this shape
// was measured, so both are left out rather than guessed at (issue #294).
const todayDue = "t.start IN (0, 1) AND t.startDate IS NULL AND t.type = 0 AND t.deadline <= " + thingsToday +
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
// deadline. It reads the day the same way closedTodayUnlogged does.
const thingsToday = `((CAST(strftime('%Y', 'now', 'localtime') AS INTEGER) << 16) | ` +
	`(CAST(strftime('%m', 'now', 'localtime') AS INTEGER) << 12) | ` +
	`(CAST(strftime('%d', 'now', 'localtime') AS INTEGER) << 7))`

// heldInPlace is the set the Logbook withholds: every closed row Things has
// not yet logged, wherever it sits. It used to be the union of what the Inbox,
// Today, Anytime and Upcoming were still showing (issues #230, #238, #293),
// on the view that a row no list held went to the Logbook at once. The app
// does not do that. Measured on 4 Oct 2026 under the daily setting, its
// Logbook held none of eight rows closed that day, among them a closed
// project with no area that no list shows at all. The CLI reaches each such
// row under --include-completed: a view (someday included), its project, or
// its area. A closed Anytime project with no area is the one exception, and
// `things projects --completed` lists it.
//
// notATemplate is the other half of the test. today, anytime, upcoming and
// someday all drop repeating templates and the contents of repeating project
// templates, and the repeating view takes no --include-completed, so
// withholding such a row would take it out of every list. The Logbook keeps
// them (includesTemplates) at once. Both halves of notATemplate are "IS NULL"
// tests, which are never NULL themselves.
const heldInPlace = "(" + closedTodayUnlogged + " AND " + notATemplate + ")"

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
// The --include-completed views fold only once the project is logged; see
// parentCloseLogged.
//
// Trash is the deliberate exception rather than a third caller: it folds a
// trashed parent's children only, for the reason its own entry in the view
// table gives.
const parentNotClosed = "NOT (" + parentClosed + ")"

// parentCloseUnlogged is closedTodayUnlogged asked of the parent project.
var parentCloseUnlogged = strings.ReplaceAll(closedTodayUnlogged, "t.stopDate", "p.stopDate")

// parentNotClosedOrUnlogged is the fold the --include-completed views apply:
// a to-do of a closed project stays in place, struck through, until the
// project itself is logged, and only then folds into it (issue #249). Measured
// on 4 Oct 2026, the app's Anytime, Today and Upcoming each kept the to-dos
// of a project closed that day where they were.
var parentNotClosedOrUnlogged = "(" + parentNotClosed + " OR (" + parentCloseUnlogged + "))"

// openOrJustClosed is the status test for the views that --include-completed
// applies to. By default only open rows; with the flag, also the rows the app
// is still showing in place because they were closed and not yet logged, and
// not folded into a logged project's row. Shared so inbox, today, anytime,
// upcoming and someday cannot answer the question differently.
//
// The fold sits inside the closed branch rather than beside it, so it can only
// ever remove a row --include-completed just added. An open to-do under a
// closed project is a different question and a riskier one — dropping it would
// take real work out of Today — and issue #249 does not ask it. There was no
// such row in the data on 10 Sep 2026 to measure the app's answer against.
func openOrJustClosed(o whereOpts) string {
	if !o.includeCompleted {
		return openRows
	}
	justClosed := closedRows + " AND " + closedTodayUnlogged
	// The fold exists so a logged project is one row rather than a row plus
	// its contents. Naming that project is asking for the contents, so the
	// fold comes off — the same answer `things --project <uuid>` and
	// `show --agent` already give (issue #253). With p pinned to the named
	// project the clause is a constant, true for an open project and false
	// for a closed one, so dropping it can only ever affect the closed case.
	if !o.projectNamed {
		if o.foldsUnlogged {
			justClosed += " AND " + parentNotClosed
		} else {
			justClosed += " AND " + parentNotClosedOrUnlogged
		}
	}
	return "(" + openRows + " OR (" + justClosed + "))"
}

// The row-state and row-kind tests every view is built from. They were spelled
// out inside each view's WHERE string before, seven copies of the open set
// among them, so a change to one meant finding the rest by eye (issue #240).
const (
	// openRows is the default status test. Almost every view is the open set;
	// inbox, today, anytime, upcoming and someday widen past it under --include-completed, and only the
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
	// Inbox to-dos whose deadline has come. todayDue covers both buckets, so
	// joining it adds only the Inbox ones. Measured on 3 Oct 2026, the app's
	// Anytime held an Inbox to-do due that day and one overdue, the same two
	// its Today held, and no Inbox to-do due later or taken out of Today for
	// its deadline. A start = 2 to-do whose day has come is in Anytime too,
	// as it is in Today (scheduledArrived).
	anytimeScope = "(" + anytimeBucket + " OR (" + scheduledArrived + ") OR (" + todayDue + "))"
	// upcomingScheduled and somedayDeferred split the one Things code between
	// them. start = 2 is both lists: the app shows a deferred item in Upcoming
	// once it carries a date and in Someday while it does not. A dated one
	// whose day has come is Today's instead (scheduledArrived), so Upcoming
	// takes only the days after today.
	upcomingScheduled = "t.start = 2 AND t.startDate > " + thingsToday
	somedayDeferred   = "t.start = 2 AND t.startDate IS NULL"
	// upcomingDue is the other way into Upcoming: an Anytime to-do with no
	// start date but a deadline after today, which the app lists under the
	// deadline's day. Measured on 30 Sep 2026, the app's Upcoming held both
	// such to-dos in the data and the CLI neither. The measurement had no
	// project of this shape, and no to-do that also carried a start date, so
	// both are left out rather than guessed at.
	upcomingDue = "t.start = 1 AND t.startDate IS NULL AND t.type = 0 AND t.deadline > " + thingsToday
	// upcomingScope is Upcoming's whole scope: the two ways in, either of
	// which is enough.
	upcomingScope = "((" + upcomingScheduled + ") OR (" + upcomingDue + "))"
	// upcomingDate is the day Upcoming files a row under — its start date,
	// or for an upcomingDue row, which has none, its deadline. It is both the
	// view's sort key and the column --on/--from/--to compare.
	upcomingDate = "COALESCE(t.startDate, t.deadline)"
	hasDeadline  = "t.deadline IS NOT NULL"
	// isTemplate selects the rows that carry a recurrence rule — the templates
	// themselves, not the items they generate (issue #147).
	isTemplate = repeatingPlaceholder + " IS NOT NULL"
)

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
	// notHeldInPlace is the Logbook's complement of what Things has not yet
	// logged. COALESCE makes the negation null-safe. The Logbook's other extra is parentNotClosed, which it shares with the
	// --include-completed views since #252, so it is defined with its pair.
	notHeldInPlace = "COALESCE(" + heldInPlace + ", 0) = 0"
	// notTodayDue is the Inbox's half of todayDue: an undated Inbox to-do
	// whose deadline has come leaves the Inbox for Today, and goes back when
	// it is taken out of Today for that deadline. Measured on 3 Oct 2026, the
	// app's Inbox held neither a to-do due that day nor one overdue, and held
	// the overdue one again once its deadlineSuppressionDate was set (issue
	// #345). COALESCE keeps a to-do with no deadline, where todayDue is NULL.
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
	// Where supportsIncludeCompleted is set this must hold openRows: the flag
	// does not widen the field, it replaces it wholesale with
	// openOrJustClosed, which starts from the open set. Setting the flag
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

	// supportsIncludeCompleted marks the views --include-completed applies to:
	// the lists the app keeps a just-closed item visible in until the day
	// rolls over. inbox, today, anytime, upcoming and someday are all such
	// lists (issues #106, #238, #293). Measured on 4 Oct 2026, the app's
	// Someday kept a to-do and a project closed out of it, struck through.
	supportsIncludeCompleted bool

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
	// indistinguishable and no view's primary ordering changes. Views with no
	// arrangement of their own — inbox and trash — take indexOrderBy, which
	// they used to reach through a fallback in ListTasks; naming it here means
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

	// completesWithArea marks the view that takes --include-completed when
	// --area names an area, as it does when --project names a project. Only
	// the catch-all has it. The app's area page keeps an item closed today in
	// place until it is logged: measured on 3 Oct 2026, `to dos of area id X`
	// held loose to-dos closed out of Anytime, Upcoming and Someday, and a
	// project completed that day, but not that project's own to-dos, which
	// its row stands for, so the closed-parent fold stays on. The listing
	// also carries the area's projects' to-dos, and the project's page keeps
	// one closed today (issue #295), so the flag reaches those too.
	completesWithArea bool

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
	// includeCompleted widens the status test on the views that support it
	// and is ignored on the rest, which is what ListTasks did with it before.
	includeCompleted bool

	// projectNamed is set when --project names one project, which lifts the
	// closed-parent fold. See openOrJustClosed for why.
	projectNamed bool

	// areaNamed is set when --area names an area, which lets the flag reach
	// the view that completesWithArea.
	areaNamed bool

	// foldsUnlogged keeps the fold for a project closed and not yet logged.
	// An area's page does that: measured on 3 and 4 Oct 2026, it held a
	// project closed that day as one row, and none of its to-dos, which the
	// lists themselves went on showing in place.
	foldsUnlogged bool
}

// where composes the view's WHERE clause.
func (s viewSpec) where(o whereOpts) string {
	status := s.status
	if o.includeCompleted && (s.supportsIncludeCompleted || (o.areaNamed && s.completesWithArea)) {
		o.foldsUnlogged = !s.supportsIncludeCompleted
		status = openOrJustClosed(o)
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
		includesProjects: true, supportsIncludeCompleted: true, supportsDateFilter: true,
		dateColumn: todayDate,
		// Today takes the shared grouping and then todayIndex, which is the
		// one signal the app orders within a group by. Measured against the
		// app on 10 Sep 2026 over a 27-row Today, these keys reproduce its
		// order in every position (issue #237).
		//
		// Two keys came off to get there, and both were doing harm. t.status
		// put the closed items --include-completed keeps at the end of their
		// group, where the app leaves them in place among the open ones,
		// struck through: the app's Today interleaved six closed rows through
		// three groups. t.todayIndexReferenceDate DESC reordered whole groups
		// by the day their todayIndex was last rewritten, which the app does
		// not do either — it is the stamp that says which day a todayIndex
		// belongs to, not a sort key.
		//
		// A project row scheduled for Today (issue #201) has no parent project
		// of its own, so p.* is NULL and it lands in its area's group ahead of
		// that area's projects. Its own todayIndex then places it among the
		// area's loose to-dos, the same signal everything else here is ordered
		// by.
		orderBy: "ORDER BY " + listGrouping + ", t.todayIndex ASC, t.\"index\" ASC" + uuidTiebreak,
	},
	ViewInbox: {
		scope: inboxBucket, status: openRows, trashed: untrashedRows,
		extra: []string{notTodayDue},
		// --include-completed works here on the same rule as the other
		// lists: the app's Inbox keeps a to-do closed out of it that day in
		// place, struck through, until the day is logged.
		supportsIncludeCompleted: true,
		orderBy:                  indexOrderBy,
	},
	ViewUpcoming: {
		scope: upcomingScope, status: openRows, trashed: untrashedRows,
		includesProjects: true, supportsIncludeCompleted: true, supportsDateFilter: true,
		// Upcoming is a diary, so it reads by date and not by list position.
		// The app orders it by start date and then by todayIndex, which is the
		// within-day position it also keys Today on; the view listed in bare
		// t."index" order before, which interleaved the dates (issue #217).
		// A to-do there only by its deadline sorts by that day in the same way.
		//
		// --include-completed works here on the same rule as Today and
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
	// --include-completed works here on the same rule as Today: the app keeps
	// an item closed today visible in whatever list it was in until the day
	// rolls over, and Anytime is a list like Today (issue #238).
	ViewAnytime: {
		scope: anytimeScope, status: openRows, trashed: untrashedRows,
		extra:                    []string{parentNotDeferred},
		supportsIncludeCompleted: true, supportsDateFilter: true,
		// Anytime groups the way the app presents it: the project is the
		// header above its own to-dos, not a row among them. The view listed
		// in bare t."index" order before, so a project's to-dos interleaved
		// with everything else and the rendered header repeated (issue #217).
		orderBy: "ORDER BY " + listGrouping + ", t.\"index\" ASC" + uuidTiebreak,
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
		extra:                    []string{unparented},
		includesProjects:         true,
		rejectsProjectFilter:     true,
		supportsIncludeCompleted: true,
		// Someday is arranged like today and anytime. Its filter keeps only
		// rows with no parent project, so the two project keys are constant
		// across the listing and it reduces to unfiled items, then areas, then
		// t."index" — but it is written with the shared constant rather than a
		// trimmed copy, so the three views cannot drift apart. It listed in
		// bare t."index" order before and matched the app in none of its
		// positions (issue #237).
		//
		// One case here is unverified: a Someday project row and a loose
		// Someday to-do in the same area both fall through to t."index", which
		// compares a project's index with a to-do's — different spaces, the
		// same concern the repeating view's ordering calls out. It is no worse
		// than the bare index ordering this replaces, and there is no Someday
		// project in the data to measure the app's answer against.
		orderBy: "ORDER BY " + listGrouping + ", t.\"index\" ASC" + uuidTiebreak,
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
	// listed under --include-completed by its view, its project or its area,
	// and a closed Anytime project with no area by `things projects
	// --completed`. Those listings overlap each other, so a sweep across them
	// dedupes by uuid.
	//
	// A closed project is one row, not a row plus its contents: the app folds
	// the to-dos of a closed project into the project's own Logbook row and
	// lists none of them separately. Measured on 10 Sep 2026, the app's Logbook
	// held no to-do at all whose parent project was closed, against 328 such
	// rows in the CLI (issue #229). The trashed-parent half of the fold is the
	// clause buildListQuery appends for every view.
	ViewLogbook: {
		status: closedRows, trashed: untrashedRows,
		extra:             []string{notHeldInPlace, parentNotClosed},
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
		orderBy:                 indexOrderBy,
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
		widensToProjectContents: true, completesWithArea: true,
		// A filter that spans projects — `things --area X`, `things --tag y` —
		// groups by area then project so the rendered group headers stay
		// contiguous instead of repeating as rows interleave by index. Within a
		// project, and so the whole of a single-project listing, the rows
		// follow projectPageOrder. The uuid after each index keeps two areas,
		// or two projects, that share an index from interleaving.
		orderBy: "ORDER BY COALESCE(a.\"index\", pa.\"index\", 0), COALESCE(a.uuid, pa.uuid, ''), " +
			"COALESCE(p.\"index\", 0), COALESCE(p.uuid, ''), " + projectPageOrder + uuidTiebreak,
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
const projectPageOrder = `CASE WHEN t.heading IS NULL THEN 0 ELSE 1 END, COALESCE(h."index", 0), COALESCE(h.uuid, ''), ` +
	`CASE WHEN ` + upcomingScheduled + ` THEN 1 WHEN ` + somedayDeferred + ` THEN 2 ELSE 0 END, ` +
	`CASE WHEN ` + upcomingScheduled + ` THEN t.startDate END, ` +
	`CASE WHEN ` + upcomingScheduled + ` THEN t.todayIndex END, ` +
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
// the three lists are arranged identically (issues #217, #237). Four keys, and
// the two that ask "filed here at all?" are a CASE rather than a plain index
// because Things' own indexes are negative, so the COALESCE default of 0 that
// stands for "not filed here" would sort last where the app puts it first:
//
//  1. the items filed nowhere at all — no project and no area — lead the list;
//  2. then areas, in area order;
//  3. and inside an area its own loose to-dos come before those of its
//     projects, which then follow project by project.
//
// A to-do inside a project that carries no area is the case these keys do not
// separate: the area key takes its COALESCE default of 0 and so lands the row
// after every area. Sorting it where the app does would mean comparing a
// TMArea."index" with a TMTask."index", which are different spaces, so the
// app's sidebar order between a standalone project and an area cannot be
// reconstructed from either alone.
//
// The caller adds its own within-group key after these — t."index" for anytime
// and someday, today's todayIndex ordering for today — and the uuid tiebreak
// last. Together they put a project's to-dos in one contiguous block, so the
// rendered group header prints once above them, which is the app's own
// presentation of a project in these lists.
const listGrouping = `CASE WHEN p.uuid IS NULL AND t.area IS NULL THEN 0 ELSE 1 END, ` +
	`COALESCE(a."index", pa."index", 0), ` +
	`CASE WHEN p.uuid IS NULL THEN 0 ELSE 1 END, ` +
	`COALESCE(p."index", 0)`

// indexOrderBy is the ordering for the views with no arrangement of their own:
// inbox and trash name it in the view table and list in index order. There is
// no fallback behind them any more: a view whose spec leaves orderBy empty
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
// Under --include-completed an open project also lists the to-dos closed today
// and not yet logged, through openOrJustClosed, so it cannot answer that
// question differently from today and anytime. The app keeps such a to-do in
// its project whatever list it was closed out of: measured on 3 Oct 2026,
// `to dos of project id X` held all of them in three open projects, two closed
// ahead of their date out of Upcoming among them (issue #295). The project is
// named, so the closed-parent fold does not apply.
func closedProjectContents(includeCompleted bool) string {
	status := openOrJustClosed(whereOpts{includeCompleted: includeCompleted, projectNamed: true})
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

// projectNameMatch is the --project clause for the project row aliased alias,
// with its arguments: the uuid, or the title. An exact-case title wins, and
// only when no project carries one does the title match ignoring case and
// surrounding space — the order `open --tag/--area` take names in. Things lets
// two projects differ only by case, and without the preference naming one
// listed both (issue #290). A trashed project does not take the preference:
// the app no longer shows it, so it must not hide an open project that
// differs from it only by case. Named exactly, it still matches.
func projectNameMatch(alias, ref string) (string, []any) {
	clause := "(" + alias + ".uuid = ? OR nfc(" + alias + ".title) = ? OR (fold(" + alias + ".title) LIKE ?" + escapeClause +
		" AND NOT EXISTS (SELECT 1 FROM TMTask px WHERE px.type = ? AND px.trashed = 0 AND nfc(px.title) = ?)))"
	return clause, []any{ref, normName(ref), nameLike(ref), int(model.TypeProject), normName(ref)}
}

// areaNameMatch is the --area clause for an area's uuid and title columns,
// with its arguments, and tagNameMatch the --tag condition on a tag's title.
// Both take names the way projectNameMatch does: an exact-case title wins,
// and only when no area or tag carries one does the title match ignoring case
// and surrounding space. Areas and tags are never trashed, so every row takes
// the preference.
func areaNameMatch(uuidCol, titleCol, ref string) (string, []any) {
	clause := "(" + uuidCol + " = ? OR nfc(" + titleCol + ") = ? OR (fold(" + titleCol + ") LIKE ?" + escapeClause +
		" AND NOT EXISTS (SELECT 1 FROM TMArea ax WHERE nfc(ax.title) = ?)))"
	return clause, []any{ref, normName(ref), nameLike(ref), normName(ref)}
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
	// The spec answers --include-completed itself, on the views that support
	// it, so there is no per-view branch here to keep in step with the table.
	where := spec.where(whereOpts{
		includeCompleted: opts.IncludeCompleted,
		projectNamed:     opts.Project != "",
		areaNamed:        opts.Area != "",
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
		where = closedProjectContents(opts.IncludeCompleted)
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
		// The template row itself, which carries the recurrence rule.
		where += " AND " + repeatingPlaceholder + " IS NULL"
		// And the to-dos inside a repeating project template, which carry no
		// rule of their own — only the project does — so the clause above
		// cannot see them. Without this they list as ordinary tasks against
		// a project `things projects` does not report (issue #171). Built
		// here rather than through the placeholder because the placeholder
		// exists for the static predicates in the view table, and this one
		// needs an alias those never mention.
		where += " AND " + d.recurrenceColFor("p") + " IS NULL"
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
		where += " AND t.uuid IN (SELECT tt2.tasks FROM TMTaskTag tt2 JOIN TMTag tg2 ON tt2.tags = tg2.uuid WHERE " + clause + ")"
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
	// The parent-alias placeholder is substituted first; the two literals do
	// not overlap, so the order is belt and braces rather than load-bearing.
	where = strings.ReplaceAll(where, repeatingParentPlaceholder, d.recurrenceColFor("p"))
	where = strings.ReplaceAll(where, repeatingPlaceholder, d.recurrenceCol())

	query := d.taskQuery() + " WHERE " + where + " GROUP BY t.uuid " + spec.orderBy
	return query, args, nil
}

func (d *DB) GetTaskByUUID(uuid string) (*model.Task, error) {
	query := d.taskQuery() + " WHERE t.uuid = ? AND " + notHeading + " GROUP BY t.uuid"
	row := d.db.QueryRow(query, uuid)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying task by uuid: %w", err)
	}
	return &t, nil
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

// GetTask resolves a reference to one open task: a uuid, then an exact title,
// then a substring of a title. Both title paths report an AmbiguousTaskError
// when the reference leaves more than one candidate standing, so the caller
// picks by uuid rather than being handed a row this package chose for it.
func (d *DB) GetTask(uuidOrTitle string) (*model.Task, error) {
	t, err := d.GetTaskByUUID(uuidOrTitle)
	if err != nil {
		return nil, err
	}
	if t != nil {
		return t, nil
	}

	exact, err := d.findTasksByExactTitle(uuidOrTitle)
	if err != nil {
		return nil, err
	}
	if candidates := preferInstances(exact); len(candidates) > 0 {
		if len(candidates) == 1 {
			return &candidates[0], nil
		}
		return nil, &AmbiguousTaskError{Query: uuidOrTitle, Matches: candidates}
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

// findTasksByExactTitle returns every open task carrying exactly this title.
// It used to be a LIMIT 1 query, which made the ordering the whole decision
// and hid the rest of the matches: a project and a to-do can share a title,
// and whichever sorted first became the write target with no sign that a
// second candidate existed, so `things project edit <title>` could land on the
// to-do (issue #194). The ordering is still templatesLastOrder so callers that
// do take the first row read the rows in the order the rest of the package
// uses.
func (d *DB) findTasksByExactTitle(title string) ([]model.Task, error) {
	query := d.taskQuery() + " WHERE nfc(t.title) = ? AND t.trashed = 0 AND t.status = 0 AND " + notHeading +
		" GROUP BY t.uuid " + d.templatesLastOrder()
	return d.collectTasks(query, normName(title))
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

// FindTasksByTitle returns the open tasks whose title contains substr. The
// substring is matched literally: the wildcards are escaped and only the two
// the caller never typed, either side of the value, are left as wildcards
// (issue #267). Without that, `_` stood for any character and `%` for any run
// of them, so a lookup could resolve to a task the user did not name — and
// GetTask acts on a lookup that matches exactly one row, which put a status
// write on the wrong task. Matching ignores case, beyond ASCII too (see
// literalLike); nothing documented offered wildcards.
func (d *DB) FindTasksByTitle(substr string) ([]model.Task, error) {
	query := d.taskQuery() + " WHERE t.trashed = 0 AND t.status = 0 AND " + notHeading + " AND fold(t.title) LIKE ?" + escapeClause +
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
func (d *DB) SearchTasks(query string) ([]model.Task, error) {
	pattern := containsLike(query)
	q := d.taskQuery() + " WHERE t.trashed = 0 AND " + notHeading + " AND (fold(t.title) LIKE ?" + escapeClause + " OR fold(t.notes) LIKE ?" + escapeClause + ") GROUP BY t.uuid " + indexOrderBy
	return d.collectTasks(q, pattern, pattern)
}

func (d *DB) collectTasks(query string, args ...any) ([]model.Task, error) {
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying tasks: %w", err)
	}
	defer rows.Close()

	tasks := []model.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
