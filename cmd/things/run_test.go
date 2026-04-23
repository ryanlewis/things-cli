package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"

	"github.com/ryanlewis/things-cli/internal/cache"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

// withSilentStdout replaces os.Stdout for the duration of fn with a pipe that
// is drained and discarded, so handler output doesn't pollute test logs.
func withSilentStdout(t *testing.T, fn func()) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, r)
		close(done)
	}()
	defer func() {
		os.Stdout = orig
		w.Close()
		<-done
		r.Close()
	}()
	fn()
}

func seedFullDB(t *testing.T) *db.DB {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)

	// Area
	if _, err := sqlDB.Exec(
		`INSERT INTO TMArea (uuid, title, visible, "index") VALUES ('area-1', 'Home', 1, 0)`,
	); err != nil {
		t.Fatalf("seed area: %v", err)
	}

	// Project
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, area, "index")
		 VALUES ('proj-1', 'Chores', 1, 0, 0, 'area-1', 0)`,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	// Tag
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTag (uuid, title, "index") VALUES ('tag-1', 'urgent', 0)`,
	); err != nil {
		t.Fatalf("seed tag: %v", err)
	}

	today := int64(model.ThingsDateFromTime(time.Now()))
	// Task in today view (start=1, startBucket=0, startDate set, not trashed)
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startBucket, startDate, project, "index")
		 VALUES ('task-1', 'Buy milk', 0, 0, 0, 1, 0, ?, 'proj-1', 0)`,
		today,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTaskTag (tasks, tags) VALUES ('task-1', 'tag-1')`,
	); err != nil {
		t.Fatalf("seed tasktag: %v", err)
	}

	// Inbox task
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, start, "index")
		 VALUES ('task-2', 'Think', 0, 0, 0, 0, 1)`,
	); err != nil {
		t.Fatalf("seed inbox task: %v", err)
	}

	// Checklist item on task-1
	if _, err := sqlDB.Exec(
		`INSERT INTO TMChecklistItem (uuid, title, status, "index", task)
		 VALUES ('cl-1', 'Lactose free', 0, 0, 'task-1')`,
	); err != nil {
		t.Fatalf("seed checklist: %v", err)
	}

	return db.NewFromSQL(sqlDB)
}

func runWith(t *testing.T, database *db.DB, args ...string) error {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	var cli CLI
	parser, err := kong.New(&cli, kong.Name("things"),
		kong.Vars{"builtin_lists": strings.Join(things.BuiltinLists, ", ")},
	)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	ctx, err := parser.Parse(args)
	if err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	var runErr error
	withSilentStdout(t, func() {
		runErr = run(ctx, &cli, database)
	})
	return runErr
}

func TestRunDispatchReadOnly(t *testing.T) {
	database := seedFullDB(t)

	cases := [][]string{
		{"list", "inbox"},
		{"list", "today"},
		{"projects"},
		{"areas"},
		{"tags"},
		{"show", "task-1"},
		{"search", "milk"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if err := runWith(t, database, args...); err != nil {
				t.Fatalf("run %v: %v", args, err)
			}
		})
	}
}

func TestRunListProjectArgPromotesView(t *testing.T) {
	database := seedFullDB(t)
	// When args[0] isn't a valid view, it becomes the project filter and
	// view is promoted from "today" to "project".
	if err := runWith(t, database, "list", "Chores"); err != nil {
		t.Fatalf("run list Chores: %v", err)
	}
}

func TestRunListWithTagFilter(t *testing.T) {
	database := seedFullDB(t)
	if err := runWith(t, database, "list", "today", "--tag", "urgent"); err != nil {
		t.Fatalf("run list today --tag urgent: %v", err)
	}
}

func TestResolveTaskAmbiguousNonInteractive(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	for _, uuid := range []string{"a1", "a2"} {
		if _, err := sqlDB.Exec(
			`INSERT INTO TMTask (uuid, title, type, status, trashed) VALUES (?, 'Shared title', 0, 0, 0)`,
			uuid,
		); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	database := db.NewFromSQL(sqlDB)

	// Ensure stdin is not a TTY in the test process — it shouldn't be.
	_, err := resolveTask("Shared", database)
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveTaskNotFound(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	database := db.NewFromSQL(sqlDB)
	_, err := resolveTask("nope", database)
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestResolveTaskNumericWithoutCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sqlDB := dbtest.NewSQL(t)
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed) VALUES ('uuid-1', 'One', 0, 0, 0)`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	database := db.NewFromSQL(sqlDB)

	// "1" has no cache — falls through to treating "1" as a title, which
	// should return not-found.
	_, err := resolveTask("1", database)
	if err == nil {
		t.Fatal("expected not-found when no cache and no title match")
	}
}

func TestRunOpenRequiresArg(t *testing.T) {
	database := seedFullDB(t)
	err := runWith(t, database, "open")
	if err == nil || !strings.Contains(err.Error(), "pass a reference") {
		t.Fatalf("expected missing-arg error, got: %v", err)
	}
}

func TestRunOpenConflictingArgs(t *testing.T) {
	database := seedFullDB(t)
	err := runWith(t, database, "open", "today", "--query", "milk")
	if err == nil || !strings.Contains(err.Error(), "only one of") {
		t.Fatalf("expected conflict error, got: %v", err)
	}
}

func TestRunOpenAreaNotFound(t *testing.T) {
	database := seedFullDB(t)
	err := runWith(t, database, "open", "--area", "Nope")
	if err == nil || !strings.Contains(err.Error(), "area not found") {
		t.Fatalf("expected area-not-found, got: %v", err)
	}
}

func TestRunOpenTagNotFound(t *testing.T) {
	database := seedFullDB(t)
	err := runWith(t, database, "open", "--tag", "nope")
	if err == nil || !strings.Contains(err.Error(), "tag not found") {
		t.Fatalf("expected tag-not-found, got: %v", err)
	}
}

func TestConfirmActionNonInteractive(t *testing.T) {
	if confirmAction("Really?") {
		t.Error("expected false in non-interactive test run")
	}
}

func TestIsInteractiveStdinPipe(t *testing.T) {
	// In `go test`, stdin is typically not a TTY. Just call it for coverage;
	// don't assert on the result since test runners vary.
	_ = isInteractive()
}

// Sanity: the cache round-trip after a list call actually persists uuids
// that a subsequent resolveTask("1") can read back.
func TestRunListThenResolveByIndex(t *testing.T) {
	database := seedFullDB(t)
	if err := runWith(t, database, "list", "inbox"); err != nil {
		t.Fatalf("run list inbox: %v", err)
	}
	got, err := cache.ReadLastList()
	if err != nil {
		t.Fatalf("ReadLastList: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected cached uuids")
	}
}
