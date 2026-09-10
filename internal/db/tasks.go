package db

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/ryanlewis/things-cli/internal/model"
)

type TaskFilter struct {
	Project string
	Area    string
	Tag     string

	// IncludeCompleted keeps completed/cancelled items that Things has not yet
	// logged out of the list they are in (UI-parity). It reaches the views
	// CompletableView reports — today and anytime — and without it those views
	// return only open tasks. Ignored by every other view.
	IncludeCompleted bool

	On   *model.ThingsDate
	From *model.ThingsDate
	To   *model.ThingsDate
}

// CompletableView reports whether --include-completed applies to the view.
// The answer comes off the view's own spec, and it is the same field that
// widens the status test when the flag is set, so the question the CLI asks
// and the SQL it then runs cannot disagree.
func CompletableView(view string) bool {
	return views[view].supportsIncludeCompleted
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

// repeatingPlaceholder is substituted with the probed recurrence column
// reference by (*DB).taskQuery — the column name varies across Things schema
// versions, and a schema carrying none resolves it to NULL.
const repeatingPlaceholder = "{{repeating}}"

// repeatingParentPlaceholder is the same for the `p` alias — the row's
// resolved parent project — so a static filter can ask "is this the child of a
// repeating project template?". ListTasks substitutes it the same way; unlike
// repeatingPlaceholder it never reaches baseTaskQuery, which has no `p`
// recurrence column in its select list.
const repeatingParentPlaceholder = "{{repeating_parent}}"

// baseTaskQuery selects a task with its project, heading, area and tags.
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
	COALESCE(t.start, 0),
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
	CASE WHEN {{repeating}} IS NOT NULL THEN 1 ELSE 0 END
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
	var startDate, deadline, stopDate, creationDate sql.NullFloat64
	var tagsStr string
	var trashed, repeating int

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
	if tagsStr != "" {
		t.Tags = strings.Split(tagsStr, "\x1f")
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
const closedTodayUnlogged = `COALESCE(t.stopDate, 0) > COALESCE((SELECT manualLogDate FROM TMSettings LIMIT 1), 0) AND date(COALESCE(t.stopDate, 0), 'unixepoch', 'localtime') = date('now', 'localtime')`

// todayScheduled is the today view's scheduling test: the rows Things files
// under Today at all. It is named because the Logbook needs it too — the
// Logbook only withholds a closed item while Today is still holding it, and
// Today never holds a row this test rejects.
const todayScheduled = "t.start = 1 AND t.startBucket IN (0, 1) AND t.startDate IS NOT NULL"

// heldInPlace is the set the Logbook withholds: the closed rows some other
// view is still showing where the app leaves them. Today is one such view and
// Anytime is the other, so the test is the union of the two (issues #230,
// #238), and the Logbook takes anything neither of them holds.
//
// The scheduling half matters as much as the day: a to-do closed straight out
// of the Inbox, or ahead of its date out of Upcoming, is under neither list,
// so the Logbook takes it the moment it closes. Withholding it on the day
// alone would leave it listed nowhere at all (issue #230).
//
// The fold issue #249 adds to those two views needs no matching guard here,
// unlike the template one. The test is left claiming today and anytime still
// hold a closed project's to-dos, which since #249 they do not, and that
// over-claim is harmless: the Logbook rejects exactly those rows on its own
// parentNotClosed clause, so the withhold can never be what strands one. They
// are folded into the project row and reached by naming the project, exactly
// as issue #229 settled.
//
// Anytime's half is not redundant with Today's. A to-do can sit in the Anytime
// bucket with no start date, which Today rejects, and Anytime is where the app
// goes on showing it for the rest of the day. Today's half is not redundant
// either: it carries project rows, which Anytime does not.
//
// notATemplate is the third half of the test and the reason for it is the same
// "listed nowhere at all" hazard: today and anytime both drop repeating
// templates and the contents of repeating project templates, while the Logbook
// keeps them (includesTemplates), so withholding such a row would take
// it out of every list. Both halves are "IS NULL" tests, which are never NULL
// themselves, so they cannot turn a true held-test into NULL.
const heldInPlace = "(((" + heldByToday + ") OR (" + heldByAnytime + ")) AND " + notATemplate + ")"

// notATemplate excludes the rows today and anytime never carry: the template
// row itself, and a to-do inside a repeating project template, which carries
// no rule of its own — only its project does (issue #171).
const notATemplate = repeatingPlaceholder + " IS NULL AND " + repeatingParentPlaceholder + " IS NULL"

// heldByAnytime is the Anytime half — the bucket test the view itself uses.
const heldByAnytime = "t.start = 1 AND t.type = 0 AND " + closedTodayUnlogged

// heldByToday is the Today half.
//
// It used to carry a trashed-parent clause of its own, back when trash and
// logbook were exempt from untrashedParent and the Logbook had to keep a
// to-do under a trashed project. Issue #229 made untrashedParent
// unconditional, so the Logbook — the only caller — already sees none of
// those rows and the clause could never change the answer.
//
// COALESCE at the call site makes the negation null-safe. start and
// startBucket are nullable columns, and a NULL there would leave the AND chain
// NULL, which "NOT" leaves NULL too — dropping the row out of the Logbook by
// accident.
const heldByToday = todayScheduled + " AND " + closedTodayUnlogged

// parentClosed is true for a row whose parent project has been completed or
// cancelled. p is resolved through COALESCE(t.project, h.project), so a to-do
// filed under a project heading is judged by its heading's project. COALESCE
// makes an unparented row — p.uuid NULL — read false rather than NULL, which
// is what lets both the test and its negation stay boolean.
const parentClosed = "COALESCE(p.status, 0) IN (2, 3)"

// parentNotClosed is the fold issue #229 measured: a closed project is one row
// and its to-dos are not listed beside it, because the app folds them into the
// project's row. The Logbook applies it, and so must the two views that can
// show a closed row, or the same to-do is folded in one place and listed in
// another (issue #249). It keeps an unparented row in the view.
//
// Trash is the deliberate exception rather than a third caller: it folds a
// trashed parent's children only, for the reason its own entry in the view
// table gives.
const parentNotClosed = "NOT (" + parentClosed + ")"

// openOrJustClosed is the status test for the views that --include-completed
// applies to. By default only open rows; with the flag, also the rows the app
// is still showing in place because they were closed today, not yet logged,
// and not folded into a closed project's row. Shared so today and anytime
// cannot answer the question differently.
//
// The fold sits inside the closed branch rather than beside it, so it can only
// ever remove a row --include-completed just added. An open to-do under a
// closed project is a different question and a riskier one — dropping it would
// take real work out of Today — and issue #249 does not ask it. There was no
// such row in the data on 10 Sep 2026 to measure the app's answer against.
func openOrJustClosed(includeCompleted bool) string {
	if !includeCompleted {
		return "t.status = 0"
	}
	return "(t.status = 0 OR (t.status IN (2, 3) AND " + closedTodayUnlogged + " AND " + parentNotClosed + "))"
}

// The row-state and row-kind tests every view is built from. They were spelled
// out inside each view's WHERE string before, seven copies of the open set
// among them, so a change to one meant finding the rest by eye (issue #240).
const (
	// openRows is the default status test. Almost every view is the open set;
	// today and anytime widen past it under --include-completed, and only the
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
	// upcomingScheduled and somedayDeferred split the one Things code between
	// them. start = 2 is both lists: the app shows a deferred item in Upcoming
	// once it carries a date and in Someday while it does not.
	upcomingScheduled = "t.start = 2 AND t.startDate IS NOT NULL"
	somedayDeferred   = "t.start = 2 AND t.startDate IS NULL"
	hasDeadline       = "t.deadline IS NOT NULL"
	// isTemplate selects the rows that carry a recurrence rule — the templates
	// themselves, not the items they generate (issue #147).
	isTemplate = repeatingPlaceholder + " IS NOT NULL"
)

// The predicates only one view needs.
const (
	// unparented is someday's parity rule: a to-do inside a project stays
	// inside it however it is deferred (issue #211).
	unparented = "p.uuid IS NULL"
	// notHeldInPlace is the Logbook's complement of what today and anytime
	// still show. COALESCE makes the negation null-safe — see heldInPlace.
	// The Logbook's other extra is parentNotClosed, which it shares with the
	// --include-completed views since #252, so it is defined with its pair.
	notHeldInPlace = "COALESCE(" + heldInPlace + ", 0) = 0"
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
	// openOrJustClosed(true), which starts from the open set. Setting the flag
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
	// rolls over. today and anytime are both such lists (issues #106, #238).
	// someday would be too if it behaved the same way, but there was no item
	// closed out of Someday in the data on 10 Sep 2026 to measure the app's
	// answer against, and its list matched the CLI exactly, so it is left out
	// rather than guessed at.
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
}

// rowKinds is the type test, which closes every view's WHERE.
func (s viewSpec) rowKinds() string {
	if s.includesProjects {
		return todoOrProject
	}
	return todoOnly
}

// where composes the view's WHERE clause. includeCompleted widens the status
// test on the views that support it and is ignored on the rest, which is what
// ListTasks did with it before.
func (s viewSpec) where(includeCompleted bool) string {
	status := s.status
	if includeCompleted && s.supportsIncludeCompleted {
		status = openOrJustClosed(true)
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
	"today": {
		scope: todayScheduled, status: openRows, trashed: untrashedRows,
		includesProjects: true, supportsIncludeCompleted: true, supportsDateFilter: true,
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
	"inbox": {
		scope: inboxBucket, status: openRows, trashed: untrashedRows,
		orderBy: indexOrderBy,
	},
	"upcoming": {
		scope: upcomingScheduled, status: openRows, trashed: untrashedRows,
		includesProjects: true, supportsDateFilter: true,
		// Upcoming is a diary, so it reads by date and not by list position.
		// The app orders it by start date and then by todayIndex, which is the
		// within-day position it also keys Today on; the view listed in bare
		// t."index" order before, which interleaved the dates (issue #217).
		orderBy: "ORDER BY t.startDate ASC, t.todayIndex ASC, t.\"index\" ASC" + uuidTiebreak,
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
	"anytime": {
		scope: anytimeBucket, status: openRows, trashed: untrashedRows,
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
	"someday": {
		scope: somedayDeferred, status: openRows, trashed: untrashedRows,
		extra:                []string{unparented},
		includesProjects:     true,
		rejectsProjectFilter: true,
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
	// Over the rows these views can carry, Logbook is the exact complement of
	// what today and anytime keep under --include-completed, so such a closed
	// item is in the Logbook or in one of those lists and never in both:
	// Things moves an item out of its list and into the Logbook at the same
	// moment (issues #230, #238). The complement is taken over heldInPlace,
	// not over the day alone — a closed item no list held is logged straight
	// away, whatever day it closed on. today and anytime overlap each other,
	// though: a to-do scheduled for today sits in the Anytime bucket too, so a
	// sweep across both dedupes by uuid. parentNotClosed narrows what "these
	// views can carry" means: a closed to-do inside a closed or trashed
	// project is in neither list, and is reached by naming the project.
	//
	// A closed project is one row, not a row plus its contents: the app folds
	// the to-dos of a closed project into the project's own Logbook row and
	// lists none of them separately. Measured on 10 Sep 2026, the app's Logbook
	// held no to-do at all whose parent project was closed, against 328 such
	// rows in the CLI (issue #229). The trashed-parent half of the fold is the
	// clause buildListQuery appends for every view.
	"logbook": {
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
	"trash": {
		trashed: trashedRows,
		// No status test: a trashed row is in Trash whatever state it is in.
		includesProjects:  true,
		includesTemplates: true,
		orderBy:           indexOrderBy,
	},
	// Deadlines carries projects too: a project takes a deadline exactly as a
	// to-do does, `things projects` reports it, and agents.md advertises this
	// view as the way to sweep what is due, so pinning to-dos only hid every
	// project deadline from the sweep (issue #213). The view orders by
	// t.deadline, so project rows fall in among the to-dos by date rather than
	// forming a block of their own.
	"deadlines": {
		scope: hasDeadline, status: openRows, trashed: untrashedRows,
		includesProjects: true, supportsDateFilter: true,
		orderBy: "ORDER BY t.deadline ASC, t.\"index\" ASC" + uuidTiebreak,
	},
	// Things' Repeating list: the templates that generate to-dos and
	// projects, not the items they generate. A template carries the
	// recurrence rule; each generated instance is an ordinary row with no
	// rule of its own, so isTemplate selects templates alone (issue #147). It
	// carries projects as well as to-dos: a project can repeat too, and the
	// app's Repeating list shows both kinds, so the view carries project
	// templates and `things projects` leaves them out (issue #165).
	"repeating": {
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
	"project": {
		status: openRows, trashed: untrashedRows,
		includesProjects: true, supportsDateFilter: true,
		// A single-project listing keeps its start/index order (the area and
		// project keys are constant across it), while a filter that spans
		// projects — `things --area X`, `things --tag y` — groups by area then
		// project so the rendered group headers stay contiguous instead of
		// repeating as rows interleave by index.
		orderBy: "ORDER BY COALESCE(a.\"index\", pa.\"index\", 0), COALESCE(p.\"index\", 0), t.start ASC, t.\"index\" ASC" + uuidTiebreak,
	},
}

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
const closedProjectContents = "(" + parentClosedOrTrashed + " OR t.status = 0) AND t.trashed = 0 AND " + todoOrProject

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
	where := spec.where(opts.IncludeCompleted)
	// Naming a closed or trashed project asks for its contents, so the
	// catch-all view widens past the open set and past the trashed-parent
	// guard, which would otherwise strip exactly the rows being asked for.
	contentsOfClosedProject := view == "project" && opts.Project != ""
	if contentsOfClosedProject {
		where = closedProjectContents
	} else {
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
		where += " AND (p.uuid = ? OR p.title LIKE ?)"
		args = append(args, opts.Project, opts.Project)
	}
	if opts.Area != "" {
		where += " AND (COALESCE(a.uuid, pa.uuid) = ? OR COALESCE(a.title, pa.title) LIKE ?)"
		args = append(args, opts.Area, opts.Area)
	}
	if opts.Tag != "" {
		where += " AND t.uuid IN (SELECT tt2.tasks FROM TMTaskTag tt2 JOIN TMTag tg2 ON tt2.tags = tg2.uuid WHERE tg2.title LIKE ?)"
		args = append(args, opts.Tag)
	}

	if opts.On != nil || opts.From != nil || opts.To != nil {
		// ThingsDate is bit-encoded year<<16|month<<12|day<<7 — directly
		// comparable across (year, month, day), so no decode is needed.
		col := "t.startDate"
		if view == "deadlines" {
			col = "t.deadline"
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

func (d *DB) GetTask(uuidOrTitle string) (*model.Task, error) {
	t, err := d.GetTaskByUUID(uuidOrTitle)
	if err != nil {
		return nil, err
	}
	if t != nil {
		return t, nil
	}

	// Try exact title match. Only one row comes back, so the ordering is the
	// whole decision: templates last (issue #156).
	query := d.taskQuery() + " WHERE t.title = ? AND t.trashed = 0 AND t.status = 0 AND " + notHeading +
		" GROUP BY t.uuid " + d.templatesLastOrder() + " LIMIT 1"
	row := d.db.QueryRow(query, uuidOrTitle)
	task, err := scanTask(row)
	if err == nil {
		return &task, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("querying task by title: %w", err)
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

func (d *DB) FindTasksByTitle(substr string) ([]model.Task, error) {
	query := d.taskQuery() + " WHERE t.title LIKE ? AND t.trashed = 0 AND t.status = 0 AND " + notHeading +
		" GROUP BY t.uuid " + d.templatesLastOrder()
	return d.collectTasks(query, "%"+substr+"%")
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

func (d *DB) SearchTasks(query string) ([]model.Task, error) {
	pattern := "%" + query + "%"
	q := d.taskQuery() + " WHERE (t.title LIKE ? OR t.notes LIKE ?) AND t.trashed = 0 AND " + notHeading + " GROUP BY t.uuid " + indexOrderBy
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
