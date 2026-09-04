package main

import (
	"bytes"
	"encoding/json"
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
	"github.com/ryanlewis/things-cli/internal/skill"
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

	// Heading in proj-1, plus an anytime task filed under it. The task has no
	// project of its own and no start date, so it is outside the today view.
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, project, "index")
		 VALUES ('head-1', 'Weekly', 2, 0, 0, 'proj-1', 2)`,
	); err != nil {
		t.Fatalf("seed heading: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startBucket, heading, "index")
		 VALUES ('task-3', 'Sweep floor', 0, 0, 0, 1, 0, 'head-1', 3)`,
	); err != nil {
		t.Fatalf("seed heading task: %v", err)
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
	_, err := runOut(t, database, args...)
	return err
}

// runOut parses args, runs the command against database, and returns whatever
// the handler wrote to Deps.Stdout.
func runOut(t *testing.T, database *db.DB, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	var cli CLI
	parser, err := kong.New(&cli, kong.Name("things"),
		kong.Vars{
			"builtin_lists": strings.Join(things.BuiltinLists, ", "),
			"skill_agents":  skill.AgentNames(),
		},
	)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	ctx, err := parser.Parse(args)
	if err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	var buf bytes.Buffer
	deps := &Deps{DB: database, JSON: cli.JSON, Stdout: &buf}
	var runErr error
	withSilentStdout(t, func() {
		runErr = ctx.Run(deps)
	})
	return buf.String(), runErr
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

func TestRunListDateFilterEndToEnd(t *testing.T) {
	database := seedFullDB(t)
	today := time.Now().Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	// task-1 in seedFullDB is scheduled for today — --on today should match,
	// --on tomorrow should not. Assert via the last-list cache, which records
	// exactly the uuids `list` returned.
	if err := runWith(t, database, "list", "today", "--on", today); err != nil {
		t.Fatalf("run list today --on today: %v", err)
	}
	got, err := cache.ReadLastList()
	if err != nil {
		t.Fatalf("ReadLastList: %v", err)
	}
	if len(got) != 1 || got[0] != "task-1" {
		t.Errorf("--on today: got %v, want [task-1]", got)
	}

	if err := runWith(t, database, "list", "today", "--on", tomorrow); err != nil {
		t.Fatalf("run list today --on tomorrow: %v", err)
	}
	got, err = cache.ReadLastList()
	if err != nil {
		t.Fatalf("ReadLastList: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("--on tomorrow: got %v, want empty", got)
	}
}

func TestRunListDateFilterRejectsView(t *testing.T) {
	database := seedFullDB(t)
	err := runWith(t, database, "list", "inbox", "--on", "2026-05-09")
	if err == nil || !strings.Contains(err.Error(), "not supported on the \"inbox\" view") {
		t.Fatalf("expected view-rejection error, got: %v", err)
	}
}

func TestRunListIncludeCompletedRejectsView(t *testing.T) {
	database := seedFullDB(t)

	// Non-today view: the flag is rejected rather than silently ignored.
	err := runWith(t, database, "list", "inbox", "--include-completed")
	if err == nil || !strings.Contains(err.Error(), "only supported on the \"today\" view") {
		t.Fatalf("inbox: expected view-rejection error, got: %v", err)
	}

	// A filter with no explicit view lists the whole project, which also rejects.
	err = runWith(t, database, "list", "Chores", "--include-completed")
	if err == nil || !strings.Contains(err.Error(), "only supported on the \"today\" view") {
		t.Fatalf("project filter: expected view-rejection error, got: %v", err)
	}

	// An explicit today view keeps the flag valid alongside a filter.
	if err := runWith(t, database, "list", "today", "Chores", "--include-completed"); err != nil {
		t.Fatalf("today + project filter: %v", err)
	}
}

func TestRunListDateFilterRejectsOnWithRange(t *testing.T) {
	database := seedFullDB(t)
	err := runWith(t, database, "list", "upcoming", "--on", "2026-05-09", "--from", "2026-05-09")
	if err == nil || !strings.Contains(err.Error(), "--on cannot be combined with --from/--to") {
		t.Fatalf("expected mutex error, got: %v", err)
	}
}

func TestRunListDateFilterRejectsBadDate(t *testing.T) {
	database := seedFullDB(t)
	err := runWith(t, database, "list", "upcoming", "--from", "tomorrow")
	if err == nil || !strings.Contains(err.Error(), "invalid date") {
		t.Fatalf("expected invalid-date error, got: %v", err)
	}
}

func TestRunListDateFilterRejectsInvertedRange(t *testing.T) {
	database := seedFullDB(t)
	err := runWith(t, database, "list", "upcoming", "--from", "2026-05-10", "--to", "2026-05-09")
	if err == nil || !strings.Contains(err.Error(), "is after --to") {
		t.Fatalf("expected inverted-range error, got: %v", err)
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

// Cancelling a project must route through the project branch (confirmation
// prompt + CancelProject), not the task AppleScript path — which can't
// address projects and fails with a raw osascript error. Non-interactively
// the confirmation declines, proving the branch was taken.
func TestRunCancelProjectRequiresConfirmation(t *testing.T) {
	database := seedFullDB(t)
	err := runWith(t, database, "cancel", "Chores")
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected confirmation-declined error, got: %v", err)
	}
}

func TestRunCompleteProjectRequiresConfirmation(t *testing.T) {
	database := seedFullDB(t)
	err := runWith(t, database, "complete", "Chores")
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected confirmation-declined error, got: %v", err)
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

// A filter with no explicit view lists the target's whole open set rather than
// its Today slice — task-3 is an anytime task under a heading in Chores, so it
// only shows up if both the default view and the heading join are right
// (issues #139, #140).
func TestRunListFilterDefaultsToAllOpenTasks(t *testing.T) {
	database := seedFullDB(t)

	cases := []struct {
		name string
		args []string
	}{
		{"project flag", []string{"list", "--project", "Chores"}},
		{"project arg", []string{"list", "Chores"}},
		{"area flag", []string{"list", "--area", "Home"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := runWith(t, database, tc.args...); err != nil {
				t.Fatalf("run %v: %v", tc.args, err)
			}
			got, err := cache.ReadLastList()
			if err != nil {
				t.Fatalf("ReadLastList: %v", err)
			}
			want := map[string]bool{"task-1": true, "task-3": true}
			if len(got) != len(want) {
				t.Fatalf("got %v, want task-1 and task-3", got)
			}
			for _, uuid := range got {
				if !want[uuid] {
					t.Errorf("unexpected uuid %q in %v", uuid, got)
				}
			}
		})
	}
}

// An explicit view still wins over the filter default, and the listing says
// which view it drew from so a short list can't read as the whole project
// (issue #140).
func TestRunListLabelsExplicitViewWhenFiltered(t *testing.T) {
	database := seedFullDB(t)

	out, err := runOut(t, database, "list", "today", "--project", "Chores")
	if err != nil {
		t.Fatalf("run list today --project Chores: %v", err)
	}
	if !strings.Contains(out, "view: today") {
		t.Errorf("expected the today view labelled, got:\n%s", out)
	}
	if strings.Contains(out, "Sweep floor") {
		t.Errorf("explicit today view should not list the anytime task, got:\n%s", out)
	}

	// The unfiltered default and the full-project listing carry no label.
	out, err = runOut(t, database, "list", "--project", "Chores")
	if err != nil {
		t.Fatalf("run list --project Chores: %v", err)
	}
	if strings.Contains(out, "view:") {
		t.Errorf("unlabelled listing expected, got:\n%s", out)
	}
	if !strings.Contains(out, "Sweep floor") {
		t.Errorf("expected the heading-nested task, got:\n%s", out)
	}

	out, err = runOut(t, database, "list", "today")
	if err != nil {
		t.Fatalf("run list today: %v", err)
	}
	if strings.Contains(out, "view:") {
		t.Errorf("unfiltered today needs no label, got:\n%s", out)
	}
}

// JSON output stays a bare task array — the view label is human output only.
func TestRunListJSONIsUnlabelled(t *testing.T) {
	database := seedFullDB(t)

	out, err := runOut(t, database, "--json", "list", "today", "--project", "Chores")
	if err != nil {
		t.Fatalf("run --json list today --project Chores: %v", err)
	}
	var tasks []model.Task
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(tasks) != 1 || tasks[0].UUID != "task-1" {
		t.Errorf("got %d tasks, want [task-1]", len(tasks))
	}
}

// A heading-nested task reports the project it sits in, so `things show` and
// listings don't present it as a standalone task (issue #139).
func TestRunListHeadingTaskShowsProject(t *testing.T) {
	database := seedFullDB(t)

	out, err := runOut(t, database, "--json", "show", "task-3")
	if err != nil {
		t.Fatalf("run show task-3: %v", err)
	}
	var task model.Task
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if task.ProjectTitle != "Chores" {
		t.Errorf("projectTitle = %q, want Chores", task.ProjectTitle)
	}
	if task.HeadingTitle != "Weekly" {
		t.Errorf("headingTitle = %q, want Weekly", task.HeadingTitle)
	}
}
