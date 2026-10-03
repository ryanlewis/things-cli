package dbtest

import (
	"database/sql"
	"testing"

	"github.com/ryanlewis/things-cli/internal/model"
)

// Fixture seeds the rows a test asserts on, one row per call, so a test reads
// as the list it expects back.
//
// Nothing an assertion can turn on is defaulted. "index" is a required
// argument on every row and every date is passed in, because several ordering
// tests seed those values deliberately against the order they expect — that is
// what leaves the key under test as the only thing that can produce it. The
// arrangement tests still seed their rows in SQL of their own for that reason:
// their area and project indexes are negative, as Things writes them, and the
// ordering CASE keys exist precisely because an unfiled row's COALESCE default
// of 0 would otherwise sort it last instead of first (issues #217, #237). A
// fixture that numbered rows 1, 2, 3 for itself would leave those tests green
// and meaningless, so "index" is a parameter here rather than a counter.
//
// Columns left unset stay NULL, which is what an omitted column gave before —
// the one exception is notes, which defaults to the empty string the old
// inline INSERTs wrote. The read path COALESCEs that column to the empty
// string, so the two are indistinguishable in a result.
type Fixture struct {
	t  *testing.T
	db *sql.DB
}

// NewFixture returns the builder that fills sqlDB, as made by NewSQL.
func NewFixture(t *testing.T, sqlDB *sql.DB) *Fixture {
	t.Helper()
	return &Fixture{t: t, db: sqlDB}
}

func (f *Fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

// Area seeds an area.
func (f *Fixture) Area(uuid, title string, index int) {
	f.t.Helper()
	f.exec(`INSERT INTO TMArea (uuid, title, visible, "index") VALUES (?, ?, 1, ?)`,
		uuid, title, index)
}

// Tag seeds a tag.
func (f *Fixture) Tag(uuid, title string, index int) {
	f.t.Helper()
	f.exec(`INSERT INTO TMTag (uuid, title, "index") VALUES (?, ?, ?)`,
		uuid, title, index)
}

// Tagged attaches tags to a task, as TMTaskTag does.
func (f *Fixture) Tagged(task string, tags ...string) {
	f.t.Helper()
	for _, tag := range tags {
		f.exec(`INSERT INTO TMTaskTag (tasks, tags) VALUES (?, ?)`, task, tag)
	}
}

// Todo seeds a to-do.
func (f *Fixture) Todo(uuid, title string, index int, opts ...Opt) {
	f.t.Helper()
	f.insert(model.TypeTask, uuid, title, index, opts)
}

// Project seeds a project.
func (f *Fixture) Project(uuid, title string, index int, opts ...Opt) {
	f.t.Helper()
	f.insert(model.TypeProject, uuid, title, index, opts)
}

// Heading is structure inside a project, never a list row, and it carries the
// project for the to-dos filed under it — they leave t.project NULL.
func (f *Fixture) Heading(uuid, title string, index int, opts ...Opt) {
	f.t.Helper()
	f.insert(model.TypeHeading, uuid, title, index, opts)
}

func (f *Fixture) insert(kind model.TaskType, uuid, title string, index int, opts []Opt) {
	f.t.Helper()
	r := taskRow{kind: kind, status: model.StatusOpen, notes: ""}
	for _, opt := range opts {
		opt(&r)
	}
	f.exec(`INSERT INTO TMTask
		(uuid, title, notes, type, status, trashed, start, startBucket, startDate,
		 todayIndexReferenceDate, todayIndex, deadline, stopDate, project, area,
		 heading, "index", rt1_recurrenceRule)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid, title, r.notes, int(r.kind), int(r.status), r.trashed,
		r.start, r.startBucket, r.startDate, r.todayIndexRef, r.todayIndex,
		r.deadline, r.stopDate, r.project, r.area, r.heading, index, r.recurrence)
}

// taskRow holds the columns a TMTask row is built from. A nil field is written
// as NULL.
type taskRow struct {
	kind    model.TaskType
	status  model.Status
	trashed int

	notes         any
	start         any
	startBucket   any
	startDate     any
	todayIndex    any
	todayIndexRef any
	deadline      any
	stopDate      any
	project       any
	area          any
	heading       any
	recurrence    any
}

// Opt sets a column on the row Todo, Project or Heading writes.
type Opt func(*taskRow)

// Things files a row by its start bucket — 0 Inbox, 1 Anytime, 2 Someday —
// and, for the two scheduling buckets, by the day it is scheduled for. Today
// is the Anytime bucket carrying today's date; Upcoming is the Someday bucket
// carrying a later one. A row given none of these leaves both columns NULL, as
// a project or heading filed nowhere does.

func Inbox() Opt { return bucket(0, 0, nil) }

func Anytime() Opt { return bucket(1, 0, nil) }

func AnytimeOn(date int64) Opt { return bucket(1, 0, date) }

// Evening is the Anytime bucket's second half, which Today lists beneath its
// main list.
func Evening(date int64) Opt { return bucket(1, 1, date) }

func Someday() Opt { return bucket(2, 0, nil) }

func SomedayOn(date int64) Opt { return bucket(2, 0, date) }

func bucket(start, startBucket int, date any) Opt {
	return func(r *taskRow) {
		r.start, r.startBucket, r.startDate = start, startBucket, date
	}
}

func Notes(s string) Opt { return func(r *taskRow) { r.notes = s } }

func InArea(uuid string) Opt { return func(r *taskRow) { r.area = uuid } }

func InProject(uuid string) Opt { return func(r *taskRow) { r.project = uuid } }

func UnderHeading(uuid string) Opt { return func(r *taskRow) { r.heading = uuid } }

func Trashed() Opt { return func(r *taskRow) { r.trashed = 1 } }

func Completed(stop float64) Opt {
	return func(r *taskRow) { r.status, r.stopDate = model.StatusCompleted, stop }
}

func Cancelled(stop float64) Opt {
	return func(r *taskRow) { r.status, r.stopDate = model.StatusCancelled, stop }
}

// Status closes a row without giving it a stopDate. That is a fixture shape in
// its own right: the Logbook's NULL guard is what keeps such a row in a list
// rather than dropping it out of every one.
func Status(s model.Status) Opt { return func(r *taskRow) { r.status = s } }

func Deadline(date int64) Opt { return func(r *taskRow) { r.deadline = date } }

// TodayIndex is the within-day position Today and Upcoming order on, and
// TodayIndexRef the day that position was set for.
func TodayIndex(v int) Opt { return func(r *taskRow) { r.todayIndex = v } }

func TodayIndexRef(date int64) Opt { return func(r *taskRow) { r.todayIndexRef = date } }

// Repeats makes the row a repeating template. Only whether the rule is present
// is ever read, never its content.
func Repeats() Opt { return func(r *taskRow) { r.recurrence = []byte{0x01, 0x02} } }
