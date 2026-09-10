package db

import (
	"os"
	"sort"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	return &DB{db: dbtest.NewSQL(t)}
}

func mustExec(t *testing.T, d *DB, query string, args ...any) {
	t.Helper()
	if _, err := d.db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustCreate(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	_ = f.Close()
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// --- assertions ---

// uuidsOf reduces a listing to its uuids, in the order the listing returned
// them. Compare with sameSet for membership, or slices.Equal for order.
func uuidsOf(tasks []model.Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.UUID
	}
	return out
}

// sameSet reports whether a and b hold the same uuids whatever their order.
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

// keysOf sorts the ids a batch lookup returned, so a failure names them.
func keysOf(m map[string]*model.Task) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- fixtures ---

// fixture seeds the rows a test asserts on, one row per call, so a test reads
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
type fixture struct {
	t *testing.T
	d *DB
}

// newFixture returns an empty test database and the builder that fills it.
func newFixture(t *testing.T) (*DB, *fixture) {
	t.Helper()
	d := newTestDB(t)
	return d, &fixture{t: t, d: d}
}

func (f *fixture) area(uuid, title string, index int) {
	f.t.Helper()
	mustExec(f.t, f.d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES (?, ?, 1, ?)`,
		uuid, title, index)
}

func (f *fixture) tag(uuid, title string, index int) {
	f.t.Helper()
	mustExec(f.t, f.d, `INSERT INTO TMTag (uuid, title, "index") VALUES (?, ?, ?)`,
		uuid, title, index)
}

// tagged attaches tags to a task, as TMTaskTag does.
func (f *fixture) tagged(task string, tags ...string) {
	f.t.Helper()
	for _, tag := range tags {
		mustExec(f.t, f.d, `INSERT INTO TMTaskTag (tasks, tags) VALUES (?, ?)`, task, tag)
	}
}

func (f *fixture) todo(uuid, title string, index int, opts ...taskOpt) {
	f.t.Helper()
	f.insert(model.TypeTask, uuid, title, index, opts)
}

func (f *fixture) project(uuid, title string, index int, opts ...taskOpt) {
	f.t.Helper()
	f.insert(model.TypeProject, uuid, title, index, opts)
}

// heading is structure inside a project, never a list row, and it carries the
// project for the to-dos filed under it — they leave t.project NULL.
func (f *fixture) heading(uuid, title string, index int, opts ...taskOpt) {
	f.t.Helper()
	f.insert(model.TypeHeading, uuid, title, index, opts)
}

func (f *fixture) insert(kind model.TaskType, uuid, title string, index int, opts []taskOpt) {
	f.t.Helper()
	r := taskRow{kind: kind, status: model.StatusOpen, notes: ""}
	for _, opt := range opts {
		opt(&r)
	}
	mustExec(f.t, f.d, `INSERT INTO TMTask
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

type taskOpt func(*taskRow)

// Things files a row by its start bucket — 0 Inbox, 1 Anytime, 2 Someday —
// and, for the two scheduling buckets, by the day it is scheduled for. Today
// is the Anytime bucket carrying today's date; Upcoming is the Someday bucket
// carrying a later one. A row given none of these leaves both columns NULL, as
// a project or heading filed nowhere does.

func inbox() taskOpt { return bucket(0, 0, nil) }

func anytime() taskOpt { return bucket(1, 0, nil) }

func anytimeOn(date int64) taskOpt { return bucket(1, 0, date) }

// evening is the Anytime bucket's second half, which Today lists beneath its
// main list.
func evening(date int64) taskOpt { return bucket(1, 1, date) }

func someday() taskOpt { return bucket(2, 0, nil) }

func somedayOn(date int64) taskOpt { return bucket(2, 0, date) }

func bucket(start, startBucket int, date any) taskOpt {
	return func(r *taskRow) {
		r.start, r.startBucket, r.startDate = start, startBucket, date
	}
}

func notes(s string) taskOpt { return func(r *taskRow) { r.notes = s } }

func inArea(uuid string) taskOpt { return func(r *taskRow) { r.area = uuid } }

func inProject(uuid string) taskOpt { return func(r *taskRow) { r.project = uuid } }

func underHeading(uuid string) taskOpt { return func(r *taskRow) { r.heading = uuid } }

func trashed() taskOpt { return func(r *taskRow) { r.trashed = 1 } }

func completed(stop float64) taskOpt {
	return func(r *taskRow) { r.status, r.stopDate = model.StatusCompleted, stop }
}

func cancelled(stop float64) taskOpt {
	return func(r *taskRow) { r.status, r.stopDate = model.StatusCancelled, stop }
}

// status closes a row without giving it a stopDate. That is a fixture shape in
// its own right: the Logbook's NULL guard is what keeps such a row in a list
// rather than dropping it out of every one.
func status(s model.Status) taskOpt { return func(r *taskRow) { r.status = s } }

func deadline(date int64) taskOpt { return func(r *taskRow) { r.deadline = date } }

// todayIndex is the within-day position Today and Upcoming order on, and
// todayIndexRef the day that position was set for.
func todayIndex(v int) taskOpt { return func(r *taskRow) { r.todayIndex = v } }

func todayIndexRef(date int64) taskOpt { return func(r *taskRow) { r.todayIndexRef = date } }

// repeats makes the row a repeating template. Only whether the rule is present
// is ever read, never its content.
func repeats() taskOpt { return func(r *taskRow) { r.recurrence = []byte{0x01, 0x02} } }
