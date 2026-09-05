package main

import (
	"database/sql"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/skill"
	"github.com/ryanlewis/things-cli/internal/things"
)

// fastVerify shrinks the read-back poll so failure cases don't spend ten
// seconds waiting for a status that will never change.
func fastVerify(t *testing.T) {
	t.Helper()
	timeout, interval, sleep := verifyTimeout, verifyInterval, verifySleep
	verifyTimeout = 20 * time.Millisecond
	verifyInterval = time.Millisecond
	verifySleep = func(time.Duration) {}
	t.Cleanup(func() {
		verifyTimeout, verifyInterval, verifySleep = timeout, interval, sleep
	})
}

// seedWritable returns an in-memory DB holding one repeating and one ordinary
// open to-do, plus the auth token `edit` needs, and the raw handle so a test
// can simulate Things applying a write.
func seedWritable(t *testing.T) (*db.DB, *sql.DB) {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)
	stmts := []string{
		`INSERT INTO TMSettings (uuid, uriSchemeAuthenticationToken) VALUES ('s1', 'tok')`,
		`INSERT INTO TMTask (uuid, title, type, status, trashed, start, rt1_recurrenceRule)
		 VALUES ('rep-1', 'Water plants', 0, 0, 0, 2, x'0102')`,
		`INSERT INTO TMTask (uuid, title, type, status, trashed, start)
		 VALUES ('one-1', 'Post letter', 0, 0, 0, 2)`,
		`INSERT INTO TMTask (uuid, title, type, status, trashed, rt1_recurrenceRule)
		 VALUES ('repproj-1', 'Weekly review', 1, 0, 0, x'0102')`,
	}
	for _, s := range stmts {
		if _, err := sqlDB.Exec(s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	return db.NewFromSQL(sqlDB), sqlDB
}

// stubExecApplying mocks the write command and, as Things would, moves the
// task to the given status so the read-back finds the change.
func stubExecApplying(t *testing.T, sqlDB *sql.DB, uuid string, status int) {
	t.Helper()
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		if _, err := sqlDB.Exec(`UPDATE TMTask SET status = ? WHERE uuid = ?`, status, uuid); err != nil {
			t.Errorf("simulating Things write: %v", err)
		}
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })
}

// stubExecDropping mocks a write that reports success but changes nothing —
// exactly what Things does when it drops a status update (issue #129).
func stubExecDropping(t *testing.T) *int {
	t.Helper()
	calls := 0
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		calls++
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })
	return &calls
}

func TestRepeatingWritesAreRefusedUpFront(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"complete", []string{"complete", "rep-1"}, "completed"},
		{"cancel", []string{"cancel", "rep-1"}, "canceled"},
		{"editComplete", []string{"edit", "rep-1", "--complete"}, "completed"},
		{"editCancel", []string{"edit", "rep-1", "--cancel"}, "canceled"},
		{"editWhen", []string{"edit", "rep-1", "--when", "today"}, "when"},
		{"editDeadline", []string{"edit", "rep-1", "--deadline", "2026-05-01"}, "deadline"},
		{"editDuplicate", []string{"edit", "rep-1", "--duplicate"}, "duplicate"},
		{"projectEditCancel", []string{"project", "edit", "repproj-1", "--cancel"}, "canceled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, _ := seedWritable(t)
			calls := stubExecDropping(t)

			err := runWith(t, database, tc.args...)
			if err == nil {
				t.Fatalf("run %v: expected a repeating-item error", tc.args)
			}
			if !strings.Contains(err.Error(), "repeating") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q on a repeating item", err, tc.want)
			}
			if *calls != 0 {
				t.Errorf("issued %d write(s); a refused command must not reach Things", *calls)
			}
		})
	}
}

// A repeating to-do can still be retitled or retagged — only the documented
// attributes are blocked.
func TestRepeatingAllowsUnrestrictedEdits(t *testing.T) {
	database, _ := seedWritable(t)
	stubExec(t)

	if err := runWith(t, database, "edit", "rep-1", "--title", "Water the plants"); err != nil {
		t.Fatalf("edit --title on a repeating to-do: %v", err)
	}
}

func TestCompleteVerifiesStatusLanded(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecApplying(t, sqlDB, "one-1", 3)

	if err := runWith(t, database, "complete", "one-1"); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

func TestCancelVerifiesStatusLanded(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecApplying(t, sqlDB, "one-1", 2)

	if err := runWith(t, database, "cancel", "one-1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

// The core of issue #129: a write Things accepts and then ignores must not be
// reported as success.
func TestSilentlyDroppedWriteFails(t *testing.T) {
	cases := [][]string{
		{"complete", "one-1"},
		{"cancel", "one-1"},
		{"edit", "one-1", "--cancel"},
		{"edit", "one-1", "--complete"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fastVerify(t)
			database, _ := seedWritable(t)
			stubExecDropping(t)

			err := runWith(t, database, args...)
			if err == nil {
				t.Fatalf("run %v: expected a verification failure", args)
			}
			if !strings.Contains(err.Error(), "did not apply") {
				t.Errorf("error = %v, want it to report the change was not applied", err)
			}
		})
	}
}

func TestNoVerifySkipsReadBack(t *testing.T) {
	fastVerify(t)
	database, _ := seedWritable(t)
	stubExecDropping(t)

	if err := runWith(t, database, "--no-verify", "complete", "one-1"); err != nil {
		t.Fatalf("complete --no-verify: %v", err)
	}
}

// --duplicate leaves the original untouched, so there is no status change to
// verify on it.
func TestEditDuplicateSkipsVerification(t *testing.T) {
	fastVerify(t)
	database, _ := seedWritable(t)
	stubExecDropping(t)

	if err := runWith(t, database, "edit", "one-1", "--complete", "--duplicate"); err != nil {
		t.Fatalf("edit --complete --duplicate: %v", err)
	}
}

func TestVerifyStatusTaskDisappeared(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	task, err := database.GetTaskByUUID("one-1")
	if err != nil {
		t.Fatalf("GetTaskByUUID: %v", err)
	}
	if _, err := sqlDB.Exec(`DELETE FROM TMTask WHERE uuid = 'one-1'`); err != nil {
		t.Fatalf("delete: %v", err)
	}

	err = verifyStatus(database, task, 3)
	if err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("verifyStatus after delete = %v, want a not-found error", err)
	}
}

// A read that keeps failing has to surface once the deadline passes rather
// than loop forever — the retry only covers transient errors.
func TestVerifyStatusPersistentReadErrorSurfaces(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	task, err := database.GetTaskByUUID("one-1")
	if err != nil {
		t.Fatalf("GetTaskByUUID: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	err = verifyStatus(database, task, 3)
	if err == nil || !strings.Contains(err.Error(), "verifying status change") {
		t.Fatalf("verifyStatus with an unreadable database = %v, want a read error", err)
	}
}

// --complete and --cancel ask for two different end states, so the read-back
// could not know which one to wait for. Kong rejects the pair at parse time.
func TestEditRejectsCompleteAndCancelTogether(t *testing.T) {
	cases := [][]string{
		{"edit", "one-1", "--complete", "--cancel"},
		{"project", "edit", "repproj-1", "--complete", "--cancel"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
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
			_, err = parser.Parse(args)
			if err == nil || !strings.Contains(err.Error(), "can't be used together") {
				t.Fatalf("parse %v = %v, want a mutual-exclusion error", args, err)
			}
		})
	}
}

// The batch read-back shares one budget across every item, so a payload of ten
// dropped status changes must not wait ten timeouts. Counting sleeps is the
// stable way to assert that: with a per-item deadline each item would burn its
// own full run of polls.
func TestVerifyStatusesShareOneDeadline(t *testing.T) {
	timeout, interval, sleep := verifyTimeout, verifyInterval, verifySleep
	t.Cleanup(func() { verifyTimeout, verifyInterval, verifySleep = timeout, interval, sleep })

	database, _ := seedWritable(t)
	// A budget of a few intervals, and a sleep that advances no real clock, so
	// the deadline is reached by elapsed wall time in a handful of rounds.
	verifyTimeout = 30 * time.Millisecond
	verifyInterval = 5 * time.Millisecond
	sleeps := 0
	verifySleep = func(d time.Duration) {
		sleeps++
		time.Sleep(d)
	}

	wants := make([]statusWant, 8)
	for i := range wants {
		wants[i] = statusWant{uuid: "one-1", title: "Post letter", want: model.StatusCompleted}
	}
	errs := verifyStatuses(database, wants, verifyTimeout)

	for i, res := range errs {
		if res.err == nil {
			t.Fatalf("item %d reported as landed, but nothing changed its status", i)
		}
		if !strings.Contains(res.err.Error(), "status change did not apply") {
			t.Errorf("item %d: unexpected error %v", i, res.err)
		}
		if !res.observed || res.got != model.StatusOpen {
			t.Errorf("item %d: want the observed status recorded as open, got (%v, observed=%v)", i, res.got, res.observed)
		}
	}
	// One sleep per round, and rounds stop at the shared deadline: at most
	// budget/interval of them however many items there are. A per-item
	// deadline would restart the count for each item and sleep roughly
	// len(wants) times as often. Load can only push the real count below the
	// bound, never above it, so this does not depend on how fast the machine
	// is.
	if maxRounds := int(verifyTimeout/verifyInterval) + 2; sleeps > maxRounds {
		t.Errorf("slept %d times for %d items, want at most %d rounds — the deadline is not shared", sleeps, len(wants), maxRounds)
	}
}

// A single write's read-back keeps the message it always had, batch refactor
// or not.
func TestVerifyStatusReportsTheItemThatDidNotChange(t *testing.T) {
	fastVerify(t)
	database, _ := seedWritable(t)

	task, err := database.GetTaskByUUID("one-1")
	if err != nil || task == nil {
		t.Fatalf("seed lookup: %v", err)
	}
	err = verifyStatus(database, task, model.StatusCompleted)
	if err == nil {
		t.Fatal("expected a failure, got nil")
	}
	for _, want := range []string{"status change did not apply", `"Post letter" (one-1)`, "is still open"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}
