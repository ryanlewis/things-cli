package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/ryanlewis/things-cli/internal/model"
)

type TaskFilter struct {
	Project string
	Area    string
	Tag     string

	// IncludeCompleted keeps completed/cancelled items in the today view that
	// Things has not yet logged out of Today (UI-parity). Without it, today
	// returns only open tasks. Ignored by every other view.
	IncludeCompleted bool

	On   *model.ThingsDate
	From *model.ThingsDate
	To   *model.ThingsDate
}

// dateFilterableViews lists the views where --on/--from/--to make sense.
// Excluded: inbox tasks have no startDate; trash is trashed-only; logbook
// items have a stopDate but no meaningful startDate filter; someday requires
// startDate IS NULL, so a startDate range could never match anything.
var dateFilterableViews = map[string]bool{
	"today":     true,
	"upcoming":  true,
	"anytime":   true,
	"deadlines": true,
	"project":   true,
}

// DateFilterableView reports whether --on/--from/--to apply to the view.
func DateFilterableView(view string) bool {
	return dateFilterableViews[view]
}

// viewsWithoutProjectFilter lists the views a --project filter can never match
// in. someday keeps only rows with no parent project (issue #211), so pairing
// it with --project asks for the contents of a project the view has already
// excluded: the two clauses contradict, and the listing is empty whatever the
// project holds. Rejecting the combination beats printing an empty list, the
// same call issue #124 made for date filters on this view.
var viewsWithoutProjectFilter = map[string]bool{
	"someday": true,
}

// ProjectFilterableView reports whether --project applies to the view.
func ProjectFilterableView(view string) bool {
	return !viewsWithoutProjectFilter[view]
}

// repeatingPlaceholder is substituted with the probed recurrence column
// reference by (*DB).taskQuery — the column name varies across Things schema
// versions, and a schema carrying none resolves it to NULL.
const repeatingPlaceholder = "{{repeating}}"

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
	if startDate.Valid {
		d := model.ThingsDate(int64(startDate.Float64))
		t.StartDate = &d
	}
	if deadline.Valid {
		d := model.ThingsDate(int64(deadline.Float64))
		t.Deadline = &d
	}
	if stopDate.Valid {
		ts := model.UnixToTime(stopDate.Float64)
		t.StopDate = &ts
	}
	if creationDate.Valid {
		ts := model.UnixToTime(creationDate.Float64)
		t.CreationDate = &ts
	}
	if tagsStr != "" {
		t.Tags = strings.Split(tagsStr, "\x1f")
	}
	return t, nil
}

// todoOrProject is the TMTask type set for the views that carry both kinds.
// Things schedules a project exactly as it schedules a to-do — start,
// startBucket, startDate and todayIndex all live on the project row — and
// lists the project itself in Today, Upcoming and Anytime, so those views
// carry both kinds (issue #201, the same UI-parity argument as #106). Someday
// and Logbook are the same story at the other two ends of a project's life: a
// project deferred to Someday is a row in Someday, and a closed project —
// completed or cancelled — is a row in the Logbook under its stopDate
// (issues #206, #210). A trashed project is a row in the app's Trash (issue
// #212). Deadlines is the same argument about a different column: a project
// carries a deadline the way a to-do does (issue #213).
//
// Repeating carries both for its own reason (issue #165). Headings (type 2)
// are structure inside a project, never rows in a list, so they stay out.
const todoOrProject = "t.type IN (0, 1)"

// stillUnderToday is the app's rule for a closed item Things has not yet filed
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
const stillUnderToday = `COALESCE(t.stopDate, 0) > COALESCE((SELECT manualLogDate FROM TMSettings LIMIT 1), 0) AND date(COALESCE(t.stopDate, 0), 'unixepoch', 'localtime') = date('now', 'localtime')`

// todayScheduled is the today view's scheduling test: the rows Things files
// under Today at all. It is named because the Logbook needs it too — the
// Logbook only withholds a closed item while Today is still holding it, and
// Today never holds a row this test rejects.
const todayScheduled = "t.start = 1 AND t.startBucket IN (0, 1) AND t.startDate IS NOT NULL"

// heldByToday is the full test for a closed row Today is still showing, and so
// the exact set the Logbook withholds. The scheduling test is half of it: a
// to-do closed straight out of the Inbox, out of Anytime or ahead of its date
// out of Upcoming is never under Today, so the Logbook takes it the moment it
// closes. Without that half those rows list nowhere at all — Today rejects them
// on start/startBucket/startDate and the Logbook rejected them on the day
// (issue #230).
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
const heldByToday = todayScheduled + " AND " + stillUnderToday

// todayWhere builds the today view's WHERE clause. By default only open tasks
// are returned. With includeCompleted, completed/cancelled items are kept while
// Things still shows them in Today — see stillUnderToday for the rule (issues
// #106, #230).
func todayWhere(includeCompleted bool) string {
	status := "t.status = 0"
	if includeCompleted {
		status = "(t.status = 0 OR (t.status IN (2, 3) AND " + stillUnderToday + "))"
	}
	return todayScheduled + " AND " + status + " AND t.trashed = 0 AND " + todoOrProject
}

var viewFilters = map[string]string{
	"today":    todayWhere(false),
	"inbox":    "t.start = 0 AND t.status = 0 AND t.trashed = 0 AND t.type = 0",
	"upcoming": "t.start = 2 AND t.startDate IS NOT NULL AND t.status = 0 AND t.trashed = 0 AND " + todoOrProject,
	"anytime":  "t.start = 1 AND t.status = 0 AND t.trashed = 0 AND " + todoOrProject,
	// Someday is the app's list of deferred things you have not filed under a
	// project. A to-do inside a project stays inside it however it is deferred:
	// the app shows it greyed within the project and keeps it out of the global
	// Someday list, so "p.uuid IS NULL" is the parity rule (issue #211).
	// Measured against the app rather than assumed — the discriminating case is
	// a Someday to-do whose parent project is itself in Someday, and the app
	// hides that one too, so the test is the presence of a parent, not the
	// parent's own bucket. Project rows have no parent project, so they pass and
	// stay listed (issue #206). Resolving p through COALESCE(t.project,
	// h.project) means a to-do under a project heading is filed by its heading's
	// project, not left looking unparented.
	"someday": "t.start = 2 AND t.startDate IS NULL AND t.status = 0 AND t.trashed = 0 AND p.uuid IS NULL AND " + todoOrProject,
	// The Logbook is where Things files everything closed, not just everything
	// finished: cancelling a to-do or a project logs it under its stopDate
	// beside the completed ones, so the view carries status 2 as well as 3
	// (issue #210). Callers tell the two apart by `status`, which reads
	// "cancelled" or "completed" in JSON and prints [~] or [x] in plain output.
	// Over the rows both views can carry, Logbook is the exact complement of
	// what Today keeps under --include-completed, so such a closed item is in
	// one list or the other and never in both: Things moves an item out of
	// Today and into the Logbook at the same moment (issue #230). The
	// complement is taken over heldByToday, not over the day alone — a closed
	// item Today never held is logged straight away, whatever day it closed
	// on. The fold below narrows what "both views can carry" means: a closed
	// to-do inside a closed or trashed project is in neither list, and is
	// reached by naming the project.
	//
	// A closed project is one row, not a row plus its contents: the app folds
	// the to-dos of a closed project into the project's own Logbook row and
	// lists none of them separately. Measured on 10 Sep 2026, the app's Logbook
	// held no to-do at all whose parent project was closed, against 328 such
	// rows in the CLI (issue #229). COALESCE keeps an unparented row — p.uuid
	// NULL — in the view. The trashed-parent half of the fold is the clause
	// ListTasks appends for every view.
	"logbook": "t.status IN (2, 3) AND t.trashed = 0 AND COALESCE(" + heldByToday + ", 0) = 0 AND COALESCE(p.status, 0) NOT IN (2, 3) AND " + todoOrProject,
	// Trash carries projects as well as to-dos: trashing a project in the
	// app puts the project row itself in Trash, and `things projects` filters
	// trashed rows, so pinning t.type = 0 here left a trashed project visible
	// nowhere (issue #212).
	//
	// Trash folds a trashed project's children into its row, the way the
	// Logbook folds a closed project's, and that fold is the trashed-parent
	// clause ListTasks appends. It deliberately does not fold a *closed*
	// project's children: the app's Trash held 23 to-dos whose parent project
	// was closed but not trashed, and none whose parent was trashed. Throwing
	// away a to-do out of a finished project is an ordinary thing to do, and
	// the project is not in Trash to fold it into (issue #229).
	"trash": "t.trashed = 1 AND " + todoOrProject,
	// Deadlines carries projects too: a project takes a deadline exactly as a
	// to-do does, `things projects` reports it, and agents.md advertises this
	// view as the way to sweep what is due, so pinning t.type = 0 hid every
	// project deadline from the sweep (issue #213). The view orders by
	// t.deadline, so project rows fall in among the to-dos by date rather than
	// forming a block of their own.
	"deadlines": "t.deadline IS NOT NULL AND t.status = 0 AND t.trashed = 0 AND " + todoOrProject,
	// Things' Repeating list: the templates that generate to-dos and
	// projects, not the items they generate. A template carries the
	// recurrence rule; each generated instance is an ordinary row with no
	// rule of its own, so "{{repeating}} IS NOT NULL" selects templates
	// alone (issue #147). It carries projects as well as to-dos: a project
	// can repeat too, and the app's Repeating list shows both kinds, so the
	// view carries project templates and `things projects` leaves them out
	// (issue #165).
	"repeating": repeatingPlaceholder + " IS NOT NULL AND t.status = 0 AND t.trashed = 0 AND " + todoOrProject,
	// The catch-all open set: also the default view for a bare --project/
	// --area/--tag filter. `things --project X` on a closed or trashed project
	// widens past "open" — see closedProjectContents. It carries projects for
	// the same reason the named views do — `things --area Work` is a sweep of
	// that area, and the area's own projects are part of what the app shows
	// there (issue #222). A --project filter still returns no project rows: a
	// project has no parent project of its own, so p.uuid never matches.
	"project": "t.status = 0 AND t.trashed = 0 AND " + todoOrProject,
}

// notHeading excludes project headings (TMTask type 2) from the lookup
// queries. The inbox view pins t.type = 0 outright, but a lookup has to keep
// returning projects as well as to-dos — show, edit, complete, cancel and
// open all resolve projects through GetTask/GetTaskByUUID — so it excludes the
// heading type rather than pinning the task type (issue #146).
// The int() conversion is load-bearing: model.TypeHeading is a fmt.Stringer,
// so %v or %s would splice the word `heading` into the SQL instead of `2`.
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

// viewOrderBy holds the per-view ordering. Every entry ends in uuidTiebreak.
// The key before it is t."index", the order Things keeps rows in within a
// list, so the tiebreak only decides rows that were genuinely
// indistinguishable — no view's primary ordering changes.
var viewOrderBy = map[string]string{
	"logbook":   "ORDER BY t.stopDate DESC, t.\"index\" ASC" + uuidTiebreak,
	"deadlines": "ORDER BY t.deadline ASC, t.\"index\" ASC" + uuidTiebreak,
	// Repeating holds both to-dos and projects. Ordering by type first keeps
	// the two kinds in contiguous blocks instead of interleaving them by an
	// index that is only meaningful within a kind.
	"repeating": "ORDER BY t.type ASC, t.\"index\" ASC" + uuidTiebreak,
	// Today view: top-level items (no project, no area) come first, then
	// everything else sorted by area then project index. Project tasks and
	// area-only tasks interleave by area.index — so projects in a low-index
	// area come before area-only items in higher-index areas, matching the
	// Things app. Within each group, sort by status (open before completed),
	// then todayIndexReferenceDate DESC, then todayIndex ASC.
	//
	// A project row scheduled for Today (issue #201) has no parent project of
	// its own, so p.* is NULL and it lands in its area's group keyed on
	// COALESCE(p."index", 0) = 0, alongside that area's unparented to-dos and
	// ahead of the to-dos of any project with a non-zero index. Its own
	// todayIndex then places it, the same signal the app orders Today by.
	"today": "ORDER BY CASE WHEN p.uuid IS NULL AND t.area IS NULL THEN 0 ELSE 1 END, COALESCE(a.\"index\", pa.\"index\", 0), COALESCE(p.\"index\", 0), t.status ASC, t.todayIndexReferenceDate DESC, t.todayIndex ASC, t.\"index\" ASC" + uuidTiebreak,
	// Project view: a single-project listing keeps its start/index order (the
	// area and project keys are constant across it), while a filter that spans
	// projects — `things --area X`, `things --tag y` — groups by area then
	// project so the rendered group headers stay contiguous instead of
	// repeating as rows interleave by index.
	"project": "ORDER BY COALESCE(a.\"index\", pa.\"index\", 0), COALESCE(p.\"index\", 0), t.start ASC, t.\"index\" ASC" + uuidTiebreak,
}

// indexOrderBy is the ordering for the views with no entry in viewOrderBy —
// inbox, upcoming, anytime, someday and trash all list in index order, as does
// the bare --project/--area/--tag filter. SearchTasks takes it too: its results
// are numbered out of the same cache. uuidTiebreak closes the same gap here,
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

// viewsIncludingTemplates lists the views that keep repeating templates in
// their results. Everywhere else templates are filtered out: Things files a
// template under Repeating, not under the start bucket its row happens to
// carry, so a Someday-start template showing up in `things someday` is a leak
// (issue #147). trash and logbook stay literal — they report what the database
// actually holds, templates included.
var viewsIncludingTemplates = map[string]bool{
	"repeating": true,
	"trash":     true,
	"logbook":   true,
}

func ValidView(name string) bool {
	_, ok := viewFilters[name]
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
// or thrown away. p is resolved through COALESCE(t.project, h.project), so a
// to-do filed under a project heading is judged by its heading's project.
//
// Both halves COALESCE so an unparented row — p.uuid NULL — reads false rather
// than NULL. A bare "p.status IN (2, 3)" would be NULL there, and NULL OR
// false is NULL, which would drop every closed unparented row out of any
// caller that ORs this with a status test.
const parentClosedOrTrashed = "(COALESCE(p.status, 0) IN (2, 3) OR COALESCE(p.trashed, 0) = 1)"

func (d *DB) ListTasks(view string, opts TaskFilter) ([]model.Task, error) {
	where, ok := viewFilters[view]
	if !ok {
		return nil, fmt.Errorf("unknown view: %s", view)
	}
	if view == "today" && opts.IncludeCompleted {
		where = todayWhere(true)
	}
	// Naming a closed or trashed project asks for its contents, so the
	// catch-all view widens past the open set and past the trashed-parent
	// guard, which would otherwise strip exactly the rows being asked for.
	contentsOfClosedProject := view == "project" && opts.Project != ""
	if contentsOfClosedProject {
		where = closedProjectContents
	} else {
		where += " AND " + untrashedParent
	}
	if !viewsIncludingTemplates[view] {
		// The template row itself, which carries the recurrence rule.
		where += " AND " + repeatingPlaceholder + " IS NULL"
		// And the to-dos inside a repeating project template, which carry no
		// rule of their own — only the project does — so the clause above
		// cannot see them. Without this they list as ordinary tasks against
		// a project `things projects` does not report (issue #171). Built
		// here rather than through the placeholder because the placeholder
		// exists for the static strings in viewFilters, and this one needs
		// an alias those strings never mention.
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

	orderBy := viewOrderBy[view]
	if orderBy == "" {
		orderBy = indexOrderBy
	}

	// The recurrence column varies across Things schema versions, so the
	// filters carry a placeholder that only a live DB can resolve. On a
	// schema with no such column it degrades to NULL: "IS NULL" makes the
	// exclusion a no-op and "IS NOT NULL" leaves the repeating view empty.
	where = strings.ReplaceAll(where, repeatingPlaceholder, d.recurrenceCol())

	query := d.taskQuery() + " WHERE " + where + " GROUP BY t.uuid " + orderBy
	return d.collectTasks(query, args...)
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
