package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

// fastVerify shrinks the read-back poll so failure cases don't spend seconds
// waiting for a status that will never change. runStreams hands the shrunk
// timeout to any run that leaves --verify-timeout at its built-in default.
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
	if _, err := sqlDB.Exec(`INSERT INTO TMSettings (uuid, uriSchemeAuthenticationToken) VALUES ('s1', 'tok')`); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Todo("rep-1", "Water plants", 0, dbtest.Someday(), dbtest.Repeats())
	fx.Todo("one-1", "Post letter", 1, dbtest.Someday())
	fx.Project("repproj-1", "Weekly review", 2, dbtest.Repeats())
	fx.Todo("repchild-1", "Clear desk", 3, dbtest.Anytime(), dbtest.InProject("repproj-1"))
	return db.NewFromSQL(sqlDB), sqlDB
}

// stubExecApplying mocks the write command and, as Things would, moves the
// task to the given status and bumps its modification date so the read-back
// finds the change.
func stubExecApplying(t *testing.T, sqlDB *sql.DB, uuid string, status int) {
	t.Helper()
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		if _, err := sqlDB.Exec(`UPDATE TMTask SET status = ?, userModificationDate = COALESCE(userModificationDate, 0) + 1 WHERE uuid = ?`, status, uuid); err != nil {
			t.Errorf("simulating Things write: %v", err)
		}
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })
}

// bumpModificationDates moves every item's modification date forward, which
// is the trace Things leaves of any write it applies.
func bumpModificationDates(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	if _, err := sqlDB.Exec(`UPDATE TMTask SET userModificationDate = COALESCE(userModificationDate, 0) + 1`); err != nil {
		t.Errorf("simulating Things write: %v", err)
	}
}

// stubExecEditing mocks an edit Things applies: it runs apply against the
// database, as the edit would change it, and bumps the modification date.
func stubExecEditing(t *testing.T, sqlDB *sql.DB, apply string) *int {
	t.Helper()
	calls := 0
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		calls++
		if apply != "" {
			if _, err := sqlDB.Exec(apply); err != nil {
				t.Errorf("simulating Things write: %v", err)
			}
		}
		bumpModificationDates(t, sqlDB)
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })
	return &calls
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
		// A to-do inside a repeating project template carries no rule of
		// its own; the refusal has to come from its project (issue #174).
		{"childComplete", []string{"complete", "repchild-1"}, "completed"},
		{"childEditWhen", []string{"edit", "repchild-1", "--when", "today"}, "when"},
		{"childEditDeadline", []string{"edit", "repchild-1", "--deadline", "2026-05-01"}, "deadline"},
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
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecEditing(t, sqlDB, `UPDATE TMTask SET title = 'Water the plants' WHERE uuid = 'rep-1'`)

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

// A listing numbers the items closed today alongside the open ones, so a ref
// can land on a closed item. Closing it the same way again exits 0 with a
// note; switching it to the other closed status is refused. Neither sends
// anything to Things.
func TestCompleteCancelOnClosedItemSendsNothing(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		args    []string
		wantErr string
		note    string
	}{
		{"complete completed", 3, []string{"complete", "one-1"}, "", "already completed"},
		{"cancel cancelled", 2, []string{"cancel", "one-1"}, "", "already cancelled"},
		{"complete cancelled", 2, []string{"complete", "one-1"}, "is already cancelled, so it was not completed", ""},
		{"cancel completed", 3, []string{"cancel", "one-1"}, "is already completed, so it was not cancelled", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			if _, err := sqlDB.Exec(`UPDATE TMTask SET status = ? WHERE uuid = 'one-1'`, tc.status); err != nil {
				t.Fatal(err)
			}
			calls := stubExecDropping(t)

			stderr, err := runCapturingStderr(t, database, tc.args...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want it to say %q", err, tc.wantErr)
				}
				// --json names the refusal rather than the generic token.
				if p := errorPayload(err); p.Error != "already closed" || p.UUID != "one-1" || p.Kind != "task" {
					t.Errorf("payload = %+v, want error \"already closed\" on task one-1", p)
				}
			} else {
				if err != nil {
					t.Errorf("run %v: %v", tc.args, err)
				}
				if !strings.Contains(stderr, tc.note) {
					t.Errorf("stderr = %q, want a note saying %q", stderr, tc.note)
				}
				// Plain output stays empty; --json prints the item as
				// `edit --complete` does on the same item.
				if out, err := runOut(t, database, tc.args...); err != nil || out != "" {
					t.Errorf("plain stdout = %q, %v; want nothing", out, err)
				}
				got, err := runOut(t, database, append([]string{"--json"}, tc.args...)...)
				if err != nil {
					t.Fatalf("--json %v: %v", tc.args, err)
				}
				want, err := runOut(t, database, "--json", "show", "one-1")
				if err != nil {
					t.Fatalf("show --json: %v", err)
				}
				if got != want {
					t.Errorf("--json stdout = %q, want the show --json object %q", got, want)
				}
			}
			if *calls != 0 {
				t.Errorf("issued %d write(s); a closed item must not reach Things", *calls)
			}
		})
	}
}

// edit --complete and --cancel go through the same guard: on an item already
// in that state they exit 0 with a note and send nothing, and a switch between
// completed and cancelled is refused whole, other edits included. The same
// holds for project edit.
func TestEditStatusOnClosedItemSendsNothing(t *testing.T) {
	cases := []struct {
		name    string
		uuid    string
		status  model.Status
		args    []string
		wantErr string
	}{
		{"complete completed", "one-1", model.StatusCompleted, []string{"edit", "one-1", "--complete"}, ""},
		{"cancel cancelled", "one-1", model.StatusCancelled, []string{"edit", "one-1", "--cancel"}, ""},
		{"complete cancelled", "one-1", model.StatusCancelled, []string{"edit", "one-1", "--complete"}, "is already cancelled, so it was not completed"},
		{"cancel completed", "one-1", model.StatusCompleted, []string{"edit", "one-1", "--cancel", "--title", "New"}, "is already completed, so it was not cancelled"},
		{"project complete completed", "proj-1", model.StatusCompleted, []string{"project", "edit", "proj-1", "--complete"}, ""},
		{"project cancel completed", "proj-1", model.StatusCompleted, []string{"project", "edit", "proj-1", "--cancel"}, "is already completed, so it was not cancelled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			dbtest.NewFixture(t, sqlDB).Project("proj-1", "Move house", 4)
			if _, err := sqlDB.Exec(`UPDATE TMTask SET status = ? WHERE uuid = ?`, int(tc.status), tc.uuid); err != nil {
				t.Fatal(err)
			}
			calls := stubExecDropping(t)

			stderr, err := runCapturingStderr(t, database, tc.args...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want it to say %q", err, tc.wantErr)
				}
				wantKind := "task"
				if tc.uuid == "proj-1" {
					wantKind = "project"
				}
				if p := errorPayload(err); p.Error != "already closed" || p.UUID != tc.uuid || p.Kind != wantKind {
					t.Errorf("payload = %+v, want error \"already closed\" on %s %s", p, wantKind, tc.uuid)
				}
			} else {
				if err != nil {
					t.Errorf("run %v: %v", tc.args, err)
				}
				if !strings.Contains(stderr, "nothing sent") {
					t.Errorf("stderr = %q, want a note saying nothing was sent", stderr)
				}
			}
			if *calls != 0 {
				t.Errorf("issued %d write(s); a closed item must not reach Things", *calls)
			}
		})
	}
}

// On an item already completed, edit --complete with other flags still sends
// those, without the status Things already has, and waits for them to land.
func TestEditCompleteOnCompletedSendsOtherEdits(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	if _, err := sqlDB.Exec(`UPDATE TMTask SET status = ? WHERE uuid = 'one-1'`, int(model.StatusCompleted)); err != nil {
		t.Fatal(err)
	}
	var sent []string
	prev := things.SetExecCommandForTest(func(name string, args ...string) *exec.Cmd {
		sent = append(sent, strings.Join(args, " "))
		if _, err := sqlDB.Exec(`UPDATE TMTask SET title = 'Post parcel' WHERE uuid = 'one-1'`); err != nil {
			t.Errorf("simulating Things write: %v", err)
		}
		bumpModificationDates(t, sqlDB)
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })

	stderr, err := runCapturingStderr(t, database, "edit", "one-1", "--complete", "--title", "Post parcel")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "title=Post%20parcel") || strings.Contains(sent[0], "completed") {
		t.Errorf("sent %q, want one write with the title and no status", sent)
	}
	if !strings.Contains(stderr, "already completed") {
		t.Errorf("stderr = %q, want a note that the status was already set", stderr)
	}
}

// --reveal alongside a status the item already has still goes, without the
// status, and nothing waits for a change.
func TestEditCompleteOnCompletedStillReveals(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	if _, err := sqlDB.Exec(`UPDATE TMTask SET status = ? WHERE uuid = 'one-1'`, int(model.StatusCompleted)); err != nil {
		t.Fatal(err)
	}
	var sent []string
	prev := things.SetExecCommandForTest(func(name string, args ...string) *exec.Cmd {
		sent = append(sent, strings.Join(args, " "))
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })

	if _, err := runCapturingStderr(t, database, "edit", "one-1", "--complete", "--reveal"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "reveal=true") || strings.Contains(sent[0], "completed") {
		t.Errorf("sent %q, want one write with reveal and no status", sent)
	}
}

// On an item already completed, --complete with a title it already has
// changes nothing, so nothing is sent and the item is printed once: one
// object under --json.
func TestEditCompleteOnCompletedWithSameTitleSendsNothing(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", asJSON), func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			if _, err := sqlDB.Exec(`UPDATE TMTask SET status = ? WHERE uuid = 'one-1'`, int(model.StatusCompleted)); err != nil {
				t.Fatal(err)
			}
			calls := stubExecDropping(t)

			args := []string{"edit", "one-1", "--complete", "--title", "Post letter"}
			if asJSON {
				args = append([]string{"--json"}, args...)
			}
			stdout, stderr, err := runStreams(t, database, args...)
			if err != nil {
				t.Fatalf("edit: %v", err)
			}
			if *calls != 0 {
				t.Errorf("issued %d write(s), want none", *calls)
			}
			if !strings.Contains(stderr, "nothing sent") {
				t.Errorf("stderr = %q, want a note saying nothing was sent", stderr)
			}
			if !asJSON {
				if n := strings.Count(stdout, "Post letter"); n != 1 {
					t.Errorf("item printed %d times, want once:\n%s", n, stdout)
				}
				return
			}
			dec := json.NewDecoder(strings.NewReader(stdout))
			var task model.Task
			if err := dec.Decode(&task); err != nil || task.UUID != "one-1" {
				t.Fatalf("decode %q: %v (uuid %q)", stdout, err, task.UUID)
			}
			if dec.More() {
				t.Errorf("more than one JSON value in %q", stdout)
			}
		})
	}
}

// --duplicate leaves the item as it is and edits a copy Things makes with the
// item's status, so the guard does not apply: --cancel on a completed item
// sends the status, for a cancelled copy.
func TestEditDuplicateSkipsClosedGuard(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	if _, err := sqlDB.Exec(`UPDATE TMTask SET status = ? WHERE uuid = 'one-1'`, int(model.StatusCompleted)); err != nil {
		t.Fatal(err)
	}
	sent := stubExec(t)

	if err := runWith(t, database, "edit", "one-1", "--duplicate", "--cancel"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	got := strings.Join(*sent, " ")
	if !strings.Contains(got, "duplicate=true") || !strings.Contains(got, "canceled=true") {
		t.Errorf("sent %q, want duplicate and canceled", got)
	}
}

// A status the CLI does not know is not treated as closed: the edit goes
// ahead as it did before the guard.
func TestEditUnknownStatusPassesClosedGuard(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	if _, err := sqlDB.Exec(`UPDATE TMTask SET status = 1 WHERE uuid = 'one-1'`); err != nil {
		t.Fatal(err)
	}
	stubExecApplying(t, sqlDB, "one-1", int(model.StatusCompleted))

	if err := runWith(t, database, "edit", "one-1", "--complete"); err != nil {
		t.Fatalf("edit: %v", err)
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

	err = verifyStatuses(database, []statusWant{{uuid: task.UUID, title: task.Title, want: 3}}, verifyTimeout)[0].err
	if err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("verifyStatuses after delete = %v, want a not-found error", err)
	}
}

// Things writes the database while we read it, so a row can be missing for a
// moment. A row gone on round one must be retried, not reported as deleted.
func TestVerifyStatusRowMissingThenReturns(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	task, err := database.GetTaskByUUID("one-1")
	if err != nil {
		t.Fatalf("GetTaskByUUID: %v", err)
	}
	if _, err := sqlDB.Exec(`DELETE FROM TMTask WHERE uuid = 'one-1'`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	sleeps := 0
	verifySleep = func(time.Duration) {
		sleeps++
		if sleeps == 1 {
			if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start) VALUES ('one-1', 'Post letter', 0, 3, 0, 0)`); err != nil {
				t.Errorf("simulating the row coming back: %v", err)
			}
		}
	}

	// A generous budget: success returns as soon as the row is back, so it
	// only matters if the clock runs ahead of the test under -race.
	if err := verifyStatuses(database, []statusWant{{uuid: task.UUID, title: task.Title, want: 3}}, 5*time.Second)[0].err; err != nil {
		t.Fatalf("verifyStatuses with the row back after one pause = %v, want nil", err)
	}
	if sleeps != 1 {
		t.Errorf("verifySleep called %d times, want 1", sleeps)
	}
}

// A row that never comes back is retried until the deadline, so the error
// follows at least one pause rather than the first read.
func TestVerifyStatusRowNeverReturnsPausesBeforeError(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	task, err := database.GetTaskByUUID("one-1")
	if err != nil {
		t.Fatalf("GetTaskByUUID: %v", err)
	}
	if _, err := sqlDB.Exec(`DELETE FROM TMTask WHERE uuid = 'one-1'`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	sleeps := 0
	verifySleep = func(time.Duration) {
		sleeps++
		time.Sleep(time.Millisecond)
	}

	err = verifyStatuses(database, []statusWant{{uuid: task.UUID, title: task.Title, want: 3}}, 200*time.Millisecond)[0].err
	if err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("verifyStatuses for a row that never returns = %v, want a not-found error", err)
	}
	if sleeps == 0 {
		t.Error("verifySleep never called, want a retry before the not-found error")
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

	err = verifyStatuses(database, []statusWant{{uuid: task.UUID, title: task.Title, want: 3}}, verifyTimeout)[0].err
	if err == nil || !strings.Contains(err.Error(), "verifying status change") {
		t.Fatalf("verifyStatuses with an unreadable database = %v, want a read error", err)
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
			parser, err := kong.New(&cli, parserOptions(nil)...)
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
	err = verifyStatuses(database, []statusWant{{uuid: task.UUID, title: task.Title, want: model.StatusCompleted}}, verifyTimeout)[0].err
	if err == nil {
		t.Fatal("expected a failure, got nil")
	}
	for _, want := range []string{"status change did not apply", `"Post letter" (one-1)`, "is still open"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}
