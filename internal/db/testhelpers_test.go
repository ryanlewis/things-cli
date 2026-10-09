package db

import (
	"fmt"
	"os"
	"slices"
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

// mustList runs a view, failing the test on error.
func mustList(t *testing.T, d *DB, view string, opts TaskFilter) []model.Task {
	t.Helper()
	got, err := d.ListTasks(view, opts)
	if err != nil {
		t.Fatalf("ListTasks(%q, %+v): %v", view, opts, err)
	}
	return got
}

// mustGet looks a task up by uuid or title, failing the test on error.
func mustGet(t *testing.T, d *DB, ref string) *model.Task {
	t.Helper()
	got, err := d.GetTask(ref)
	if err != nil {
		t.Fatalf("GetTask(%q): %v", ref, err)
	}
	return got
}

// mustGetByUUID looks a task up by uuid, failing the test on error.
func mustGetByUUID(t *testing.T, d *DB, uuid string) *model.Task {
	t.Helper()
	got, err := d.GetTaskByUUID(uuid)
	if err != nil {
		t.Fatalf("GetTaskByUUID(%q): %v", uuid, err)
	}
	return got
}

// mustFindByUUID is mustGetByUUID for a row the test seeded: a miss fails the
// test rather than handing the caller a nil to dereference.
func mustFindByUUID(t *testing.T, d *DB, uuid string) *model.Task {
	t.Helper()
	got := mustGetByUUID(t, d, uuid)
	if got == nil {
		t.Fatalf("GetTaskByUUID(%q): not found", uuid)
	}
	return got
}

// --- assertions ---

// assertSet reports an error unless tasks hold exactly the want uuids, in any
// order. label, formatted with args, names the listing in the failure.
func assertSet(t *testing.T, tasks []model.Task, want []string, label string, args ...any) {
	t.Helper()
	if got := uuidsOf(tasks); !sameSet(got, want) {
		t.Errorf("%s: got %v, want %v", fmt.Sprintf(label, args...), got, want)
	}
}

// assertOrder reports an error unless tasks hold exactly the want uuids, in
// that order.
func assertOrder(t *testing.T, tasks []model.Task, want []string, label string, args ...any) {
	t.Helper()
	if got := uuidsOf(tasks); !slices.Equal(got, want) {
		t.Errorf("%s: got %v, want %v", fmt.Sprintf(label, args...), got, want)
	}
}

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

// The builder lives in dbtest so other packages' tests can use it. These
// shorthands keep the call sites in this package short.
var (
	inbox         = dbtest.Inbox
	anytime       = dbtest.Anytime
	anytimeOn     = dbtest.AnytimeOn
	evening       = dbtest.Evening
	someday       = dbtest.Someday
	somedayOn     = dbtest.SomedayOn
	notes         = dbtest.Notes
	inArea        = dbtest.InArea
	inProject     = dbtest.InProject
	underHeading  = dbtest.UnderHeading
	trashed       = dbtest.Trashed
	completed     = dbtest.Completed
	cancelled     = dbtest.Cancelled
	status        = dbtest.Status
	deadline      = dbtest.Deadline
	suppressed    = dbtest.DeadlineSuppressed
	todayIndex    = dbtest.TodayIndex
	todayIndexRef = dbtest.TodayIndexRef
	repeats       = dbtest.Repeats
	recurrence    = dbtest.Recurrence
)

// newFixture returns an empty test database and the builder that fills it.
func newFixture(t *testing.T) (*DB, *dbtest.Fixture) {
	t.Helper()
	d := newTestDB(t)
	return d, dbtest.NewFixture(t, d.db)
}
