package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

// A field edit that lands prints the item as `things show` would, in both
// output modes, so the caller has its confirmation without a second command.
func TestEditPrintsItemOnceItLands(t *testing.T) {
	cases := []struct {
		name string
		args []string
		uuid string
	}{
		{"edit", []string{"edit", "one-1", "--title", "Post the letter"}, "one-1"},
		{"projectEdit", []string{"project", "edit", "repproj-1", "--title", "Post the letter"}, "repproj-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			stubExecEditing(t, sqlDB, `UPDATE TMTask SET title = 'Post the letter' WHERE uuid = '`+tc.uuid+`'`)

			plain, err := runOut(t, database, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			wantPlain, err := runOut(t, database, "show", tc.uuid)
			if err != nil {
				t.Fatalf("show: %v", err)
			}
			if plain != wantPlain {
				t.Errorf("plain output = %q, want the show output %q", plain, wantPlain)
			}

			got, err := runOut(t, database, append([]string{"--json"}, tc.args...)...)
			if err != nil {
				t.Fatalf("--json %v: %v", tc.args, err)
			}
			want, err := runOut(t, database, "--json", "show", tc.uuid)
			if err != nil {
				t.Fatalf("show --json: %v", err)
			}
			if got != want {
				t.Errorf("--json output = %s, want the show --json output %s", got, want)
			}
			var item map[string]any
			if err := json.Unmarshal([]byte(got), &item); err != nil {
				t.Fatalf("decode %s: %v", got, err)
			}
			if item["title"] != "Post the letter" {
				t.Errorf("title = %v, want the edited title", item["title"])
			}
		})
	}
}

// The modification date has to move past the value it had before the write,
// not merely exist: an item Things has touched before is the common case.
func TestEditWaitsForModificationDateToMove(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	if _, err := sqlDB.Exec(`UPDATE TMTask SET userModificationDate = 1790000000.5`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	stubExecDropping(t)
	err := runWith(t, database, "edit", "one-1", "--title", "Post the letter")
	if err == nil || !strings.Contains(err.Error(), "edit did not apply") {
		t.Fatalf("dropped edit = %v, want an edit-did-not-apply error", err)
	}
	// The error has to say what to do next: an unchanged edit and a dropped
	// one look the same, and only a read tells them apart.
	if !strings.Contains(err.Error(), "things show one-1") {
		t.Errorf("error = %v, want it to point at `things show one-1`", err)
	}

	stubExecEditing(t, sqlDB, "")
	if err := runWith(t, database, "edit", "one-1", "--title", "Post the letter"); err != nil {
		t.Fatalf("applied edit: %v", err)
	}
}

// A write that leaves an earlier modification date still landed: the item's
// last change may have come from a device whose clock runs ahead of this one.
func TestEditAcceptsEarlierModificationDate(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	if _, err := sqlDB.Exec(`UPDATE TMTask SET userModificationDate = 1790000000.5`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	stubExecEditing(t, sqlDB, `UPDATE TMTask SET userModificationDate = 1789999990 WHERE uuid = 'one-1'`)
	// stubExecEditing bumps by one after apply, so the date ends up earlier
	// than the seeded value but different from it.
	if err := runWith(t, database, "edit", "one-1", "--title", "Post the letter"); err != nil {
		t.Fatalf("edit with an earlier modification date: %v", err)
	}
}

// A field edit Things drops fails the same way a dropped status change does,
// for every kind of field — the modification date is the one signal for all.
func TestDroppedFieldEditFails(t *testing.T) {
	cases := [][]string{
		{"edit", "one-1", "--title", "Post the letter"},
		{"edit", "one-1", "--append-notes", "second class"},
		{"edit", "one-1", "--when", "today"},
		{"edit", "one-1", "--append-checklist", "stamp"},
		{"edit", "one-1", "--title", "Post the letter", "--complete"},
		{"project", "edit", "repproj-1", "--area", "Home"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			dbtest.NewFixture(t, sqlDB).Area("area-1", "Home", 1)
			stubExecDropping(t)

			_, err := runOut(t, database, args...)
			if err == nil || !strings.Contains(err.Error(), "did not apply") {
				t.Fatalf("run %v = %v, want a did-not-apply error", args, err)
			}
		})
	}
}

// stubExecChecklist mocks an edit that only changes the checklist. Things
// writes the TMChecklistItem rows and leaves the to-do's own modification
// date alone, so apply runs with no bump. An empty apply changes nothing.
func stubExecChecklist(t *testing.T, sqlDB *sql.DB, apply string) {
	t.Helper()
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		if apply != "" {
			if _, err := sqlDB.Exec(apply); err != nil {
				t.Errorf("simulating Things write: %v", err)
			}
		}
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })
}

// A checklist-only edit is confirmed by the checklist itself: Things does not
// move the to-do's modification date when only its checklist rows change, so
// waiting for the date alone reported a false "did not apply" for an edit
// that landed, and a retry would add the items twice.
func TestEditChecklistOnlyConfirmsByChecklist(t *testing.T) {
	cases := []struct {
		name  string
		seed  string
		flag  string
		apply string
	}{
		{"append", "", "--append-checklist",
			`INSERT INTO TMChecklistItem (uuid, title, status, "index", task) VALUES ('cl-a', 'stamp', 0, 0, 'one-1'), ('cl-b', 'envelope', 0, 1, 'one-1')`},
		{"prepend", `INSERT INTO TMChecklistItem (uuid, title, status, "index", task) VALUES ('cl-1', 'envelope', 0, 0, 'one-1')`, "--prepend-checklist",
			`INSERT INTO TMChecklistItem (uuid, title, status, "index", task) VALUES ('cl-a', 'stamp', 0, -1, 'one-1')`},
		// A replace with the same titles still makes new rows.
		{"replace", `INSERT INTO TMChecklistItem (uuid, title, status, "index", task) VALUES ('cl-1', 'stamp', 0, 0, 'one-1')`, "--checklist",
			`DELETE FROM TMChecklistItem WHERE uuid = 'cl-1'; INSERT INTO TMChecklistItem (uuid, title, status, "index", task) VALUES ('cl-a', 'stamp', 0, 0, 'one-1')`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			if _, err := sqlDB.Exec(`UPDATE TMTask SET userModificationDate = 1790000000.5`); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if tc.seed != "" {
				if _, err := sqlDB.Exec(tc.seed); err != nil {
					t.Fatalf("seed checklist: %v", err)
				}
			}
			stubExecChecklist(t, sqlDB, tc.apply)

			got, err := runOut(t, database, "edit", "one-1", tc.flag, `stamp\nenvelope`)
			if err != nil {
				t.Fatalf("checklist-only edit: %v", err)
			}
			want, err := runOut(t, database, "show", "one-1")
			if err != nil {
				t.Fatalf("show: %v", err)
			}
			if got != want {
				t.Errorf("output = %q, want the show output %q", got, want)
			}
		})
	}
}

// A checklist-only edit Things drops still fails: neither the date nor the
// checklist moved. A change to another item's checklist is not this one's.
func TestEditChecklistOnlyDroppedFails(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecChecklist(t, sqlDB, `INSERT INTO TMChecklistItem (uuid, title, status, "index", task) VALUES ('cl-x', 'stamp', 0, 0, 'rep-1')`)

	err := runWith(t, database, "edit", "one-1", "--append-checklist", "stamp")
	if err == nil || !strings.Contains(err.Error(), "edit did not apply") {
		t.Fatalf("dropped checklist edit = %v, want an edit-did-not-apply error", err)
	}
}

// A mixed edit still confirms through the modification date, which Things
// moves for the title change.
func TestEditMixedChecklistConfirmsByDate(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecEditing(t, sqlDB, `UPDATE TMTask SET title = 'Post the letter' WHERE uuid = 'one-1'`)

	got, err := runOut(t, database, "edit", "one-1", "--title", "Post the letter", "--append-checklist", "stamp")
	if err != nil {
		t.Fatalf("mixed edit: %v", err)
	}
	if !strings.Contains(got, "Post the letter") {
		t.Errorf("output = %q, want the edited item", got)
	}
}

// A status transition still has to show up as the status, not just as a
// modification date: an edit that bumped the date but left the item open
// did not do what --complete asked.
func TestEditStatusTransitionNeedsTheStatus(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecEditing(t, sqlDB, "")

	err := runWith(t, database, "edit", "one-1", "--complete")
	if err == nil || !strings.Contains(err.Error(), "status change did not apply") {
		t.Fatalf("edit --complete with status unchanged = %v, want a status error", err)
	}
}

// --no-verify and --duplicate skip the read-back, and say so: exit 0 with
// nothing printed must not stand for an edit nobody checked.
func TestEditUnconfirmedOutput(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		reason string
		plain  string
	}{
		{"noVerify", []string{"--no-verify", "edit", "one-1", "--title", "Post the letter"}, "no-verify", "not confirmed (--no-verify)"},
		{"duplicate", []string{"edit", "one-1", "--complete", "--duplicate"}, "duplicate", "copy is not read back"},
		{"projectNoVerify", []string{"--no-verify", "project", "edit", "repproj-1", "--title", "Review"}, "no-verify", "not confirmed (--no-verify)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, _ := seedWritable(t)
			calls := stubExecDropping(t)

			plain, err := runOut(t, database, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			if !strings.Contains(plain, tc.plain) {
				t.Errorf("plain output = %q, want it to contain %q", plain, tc.plain)
			}

			got, err := runOut(t, database, append([]string{"--json"}, tc.args...)...)
			if err != nil {
				t.Fatalf("--json %v: %v", tc.args, err)
			}
			var out unconfirmedEdit
			if err := json.Unmarshal([]byte(got), &out); err != nil {
				t.Fatalf("decode %s: %v", got, err)
			}
			if out.Confirmed || out.Reason != tc.reason || out.UUID == "" {
				t.Errorf("--json output = %+v, want confirmed=false reason=%q and the uuid", out, tc.reason)
			}
			if !strings.Contains(got, `"confirmed": false`) {
				t.Errorf("--json output %s must carry confirmed: false explicitly", got)
			}
			if *calls != 2 {
				t.Errorf("issued %d writes, want one per run", *calls)
			}
		})
	}
}

// An edit that asks for nothing new — no field flags, and a status the item
// already has — has no modification to wait for, so it must not fail after
// the budget; it prints the item as it stands.
func TestEditWithNothingToChangeDoesNotWait(t *testing.T) {
	cases := [][]string{
		{"edit", "one-1"},
		{"edit", "one-1", "--reveal"},
		{"edit", "done-1", "--complete"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			dbtest.NewFixture(t, sqlDB).Todo("done-1", "Filed", 1, dbtest.Status(model.StatusCompleted), dbtest.Anytime())
			stubExecDropping(t)

			out, err := runOut(t, database, args...)
			if err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			if !strings.Contains(out, args[1]) {
				t.Errorf("output = %q, want the item printed", out)
			}
		})
	}
}

// An edit made only of values the item already has is recognised before the
// write: Things records no change for it, so waiting would end in a false
// "did not apply". The URL still goes out, and the item is printed at once.
// Anything that is a real change, or whose outcome depends on how Things reads
// it, still waits — a dropping stub turns those into a failure.
func TestEditCertainNoOpSkipsTheWait(t *testing.T) {
	now := time.Now()
	today := int(model.ThingsDateFromTime(now))
	deadline := int(model.ThingsDateFromTime(time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)))
	day := func(offset int) string { return now.AddDate(0, 0, offset).Format("2006-01-02") }
	dayInt := func(offset int) string {
		return strconv.Itoa(int(model.ThingsDateFromTime(now.AddDate(0, 0, offset))))
	}

	cases := []struct {
		name string
		args []string
		noOp bool
	}{
		{"sameTitle", []string{"edit", "one-1", "--title", "Post letter"}, true},
		{"newTitle", []string{"edit", "one-1", "--title", "Post the letter"}, false},
		{"titleCaseOnly", []string{"edit", "one-1", "--title", "post letter"}, false},
		{"sameNotes", []string{"edit", "one-1", "--notes", "second class"}, true},
		{"newNotes", []string{"edit", "one-1", "--notes", "first class"}, false},
		{"sameTagsOtherCase", []string{"edit", "one-1", "--tags", " errand ,URGENT"}, true},
		{"moreTags", []string{"edit", "one-1", "--tags", "Errand,Urgent,Home"}, false},
		{"clearTags", []string{"edit", "one-1", "--tags", ""}, false},
		{"addTagsPresent", []string{"edit", "one-1", "--add-tags", "ERRAND"}, true},
		{"addTagsNew", []string{"edit", "one-1", "--add-tags", "Errand,Home"}, false},
		// Things drops a tag it does not have, so an unknown name is no
		// change unless --create-tags makes it first (see below).
		{"sameTagsPlusUnknown", []string{"edit", "one-1", "--tags", "Errand,Urgent,Ghost"}, true},
		{"addTagsUnknown", []string{"edit", "one-1", "--add-tags", "errand,Ghost"}, true},
		{"onlyUnknownTags", []string{"edit", "one-1", "--tags", "Ghost"}, false},
		{"projectUnknownTag", []string{"project", "edit", "repproj-1", "--add-tags", "Ghost"}, true},
		{"sameDeadline", []string{"edit", "one-1", "--deadline", "2026-10-15"}, true},
		{"newDeadline", []string{"edit", "one-1", "--deadline", "2026-10-16"}, false},
		{"clearDeadline", []string{"edit", "one-1", "--deadline", ""}, false},
		{"clearAbsentDeadline", []string{"edit", "two-1", "--deadline", ""}, true},
		{"emptyAppendNotes", []string{"edit", "one-1", "--append-notes", ""}, true},
		{"emptyPrependNotes", []string{"edit", "one-1", "--prepend-notes", ""}, true},
		{"prependNotes", []string{"edit", "one-1", "--prepend-notes", "x"}, false},
		{"whenTodayOnToday", []string{"edit", "one-1", "--when", "Today"}, true},
		{"whenTodayDateOnToday", []string{"edit", "one-1", "--when", day(0)}, true},
		{"whenEveningOnToday", []string{"edit", "one-1", "--when", "evening"}, false},
		{"whenEveningOnEvening", []string{"edit", "eve-1", "--when", "evening"}, true},
		// Just after midnight a row scheduled for the new day is still
		// start = 2 until Things moves it. Measured on 8 Oct 2026 at
		// 00:00: today and today's date left such a row as it was, start
		// and modification date unchanged; evening moved it.
		{"whenTodayOnUnmoved", []string{"edit", "unmoved-1", "--when", "today"}, true},
		{"whenTodayDateOnUnmoved", []string{"edit", "unmoved-1", "--when", day(0)}, true},
		{"whenEveningOnUnmoved", []string{"edit", "unmoved-1", "--when", "evening"}, false},
		// Unmeasured, chosen so a correct write never reports failure (see
		// unmovedKeeps): a reminder is kept, an older date stays, a project
		// behaves as a to-do, and the evening part stays.
		{"whenTodayOnUnmovedReminder", []string{"edit", "unmoved-rem", "--when", "today"}, true},
		{"whenTodayDateOnUnmovedReminder", []string{"edit", "unmoved-rem", "--when", day(0)}, true},
		{"whenTodayOnUnmovedOld", []string{"edit", "unmoved-old", "--when", "today"}, true},
		{"whenPastDateOnUnmovedOld", []string{"edit", "unmoved-old", "--when", day(-1)}, true},
		{"whenEveningOnUnmovedOld", []string{"edit", "unmoved-old", "--when", "evening"}, false},
		{"whenTodayOnUnmovedEvening", []string{"edit", "unmoved-eve", "--when", "today"}, true},
		{"whenEveningOnUnmovedEvening", []string{"edit", "unmoved-eve", "--when", "evening"}, true},
		{"whenTimeOnUnmoved", []string{"edit", "unmoved-1", "--when", day(0) + "@08:00"}, false},
		{"projectWhenTodayOnUnmoved", []string{"project", "edit", "unmoved-proj", "--when", "today"}, true},
		// A moved row with an older date still moves to today.
		{"whenTodayOnMovedOld", []string{"edit", "past-1", "--when", "today"}, false},
		{"whenTodayDateOnEvening", []string{"edit", "eve-1", "--when", day(0)}, true},
		{"whenTodayOnEvening", []string{"edit", "eve-1", "--when", "today"}, false},
		// Things clears a reminder on a --when for today without a time.
		{"whenTodayClearsReminder", []string{"edit", "rem-1", "--when", "today"}, false},
		{"whenTodayDateClearsReminder", []string{"edit", "rem-1", "--when", day(0)}, false},
		// 18:30 is never rem-1's reminder of 18:00, today or tomorrow.
		{"whenTimeOtherClock", []string{"edit", "rem-1", "--when", "18:30"}, false},
		// Things records no change for the reminder an item already has.
		{"whenSameDateTime", []string{"edit", "tom-1", "--when", day(1) + "@08:00"}, true},
		{"whenDateTimeOtherClock", []string{"edit", "tom-1", "--when", day(1) + "@09:00"}, false},
		{"whenPastDateTimeOnReminder", []string{"edit", "rem-1", "--when", day(-2) + "@18:00"}, true},
		{"whenTomorrowKeepsReminder", []string{"edit", "tom-1", "--when", "tomorrow"}, true},
		{"whenTomorrowDate", []string{"edit", "tom-1", "--when", day(1)}, true},
		{"whenOtherDate", []string{"edit", "tom-1", "--when", day(2)}, false},
		{"whenPastDate", []string{"edit", "past-1", "--when", day(-1)}, false},
		// A past date files the item under today, not the evening.
		{"whenPastDateOnToday", []string{"edit", "one-1", "--when", day(-3)}, true},
		{"whenPastDateOnEvening", []string{"edit", "eve-1", "--when", day(-3)}, false},
		{"whenPastDateClearsReminder", []string{"edit", "rem-1", "--when", day(-3)}, false},
		{"whenSomedayOnSomeday", []string{"edit", "some-1", "--when", "someday"}, true},
		{"whenSomedayOnScheduled", []string{"edit", "tom-1", "--when", "someday"}, false},
		{"whenAnytimeOnAnytime", []string{"edit", "two-1", "--when", "anytime"}, true},
		{"whenClearOnAnytime", []string{"edit", "two-1", "--when", ""}, true},
		{"whenClearOnSomeday", []string{"edit", "some-1", "--when", ""}, false},
		{"whenClearOnInbox", []string{"edit", "inbox-1", "--when", ""}, false},
		{"whenAnytimeOnToday", []string{"edit", "one-1", "--when", "anytime"}, false},
		{"whenPhrase", []string{"edit", "one-1", "--when", "friday"}, false},
		{"allNoOp", []string{"edit", "one-1", "--title", "Post letter", "--notes", "second class", "--add-tags", "errand", "--deadline", "2026-10-15", "--when", "today", "--append-notes", ""}, true},
		{"noOpPlusNewWhen", []string{"edit", "one-1", "--title", "Post letter", "--when", "someday"}, false},
		{"sameWhenPlusNewTitle", []string{"edit", "one-1", "--title", "Post the letter", "--when", "today"}, false},
		{"mixed", []string{"edit", "one-1", "--title", "Post letter", "--notes", "first class"}, false},
		{"mixedUncoveredFlag", []string{"edit", "one-1", "--title", "Post letter", "--append-notes", "x"}, false},
		{"projectSameTitle", []string{"project", "edit", "repproj-1", "--title", "Weekly review"}, true},
		{"projectWhenSomeday", []string{"project", "edit", "proj-1", "--when", "someday", "--append-notes", ""}, true},
		{"projectWhenToday", []string{"project", "edit", "proj-1", "--when", "today"}, false},
		{"projectMoveArea", []string{"project", "edit", "repproj-1", "--title", "Weekly review", "--area", "Home"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			stmts := []string{
				`UPDATE TMTask SET notes = 'second class', start = 1, startBucket = 0, startDate = ` + strconv.Itoa(today) +
					`, deadline = ` + strconv.Itoa(deadline) + ` WHERE uuid = 'one-1'`,
				`INSERT INTO TMTag (uuid, title) VALUES ('tag-1', 'Errand'), ('tag-2', 'Urgent'), ('tag-3', 'Home')`,
				`INSERT INTO TMTaskTag (tasks, tags) VALUES ('one-1', 'tag-1'), ('one-1', 'tag-2')`,
				`INSERT INTO TMTask (uuid, title, type, status, trashed, start) VALUES ('two-1', 'Undated', 0, 0, 0, 1)`,
				`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startDate, startBucket) VALUES ('eve-1', 'Evening', 0, 0, 0, 1, ` + strconv.Itoa(today) + `, 1)`,
				`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startDate, startBucket, reminderTime) VALUES ('rem-1', 'Reminded', 0, 0, 0, 1, ` + strconv.Itoa(today) + `, 0, 1207959552)`,
				`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startDate, startBucket, reminderTime) VALUES ('tom-1', 'Tomorrow', 0, 0, 0, 2, ` + dayInt(1) + `, 0, 536870912)`,
				`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startDate, startBucket) VALUES ('past-1', 'Overdue start', 0, 0, 0, 1, ` + dayInt(-1) + `, 0)`,
				`INSERT INTO TMTask (uuid, title, type, status, trashed, start) VALUES ('some-1', 'Someday', 0, 0, 0, 2)`,
				`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startDate, startBucket) VALUES ('unmoved-1', 'Not moved yet', 0, 0, 0, 2, ` + strconv.Itoa(today) + `, 0)`,
				`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startDate, startBucket, reminderTime) VALUES ('unmoved-rem', 'Not moved, reminded', 0, 0, 0, 2, ` + strconv.Itoa(today) + `, 0, 1207959552)`,
				`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startDate, startBucket) VALUES ('unmoved-old', 'Not moved for days', 0, 0, 0, 2, ` + dayInt(-3) + `, 0)`,
				`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startDate, startBucket) VALUES ('unmoved-eve', 'Not moved, evening', 0, 0, 0, 2, ` + strconv.Itoa(today) + `, 1)`,
				`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startDate, startBucket) VALUES ('unmoved-proj', 'Project not moved yet', 1, 0, 0, 2, ` + strconv.Itoa(today) + `, 0)`,
				`INSERT INTO TMTask (uuid, title, type, status, trashed, start) VALUES ('inbox-1', 'Inbox', 0, 0, 0, 0)`,
				`INSERT INTO TMTask (uuid, title, type, status, trashed, start) VALUES ('proj-1', 'Someday project', 1, 0, 0, 2)`,
				`INSERT INTO TMArea (uuid, title, "index") VALUES ('area-1', 'Home', 1)`,
			}
			for _, s := range stmts {
				if _, err := sqlDB.Exec(s); err != nil {
					t.Fatalf("seed %q: %v", s, err)
				}
			}
			calls := stubExecDropping(t)

			_, err := runOut(t, database, tc.args...)
			if *calls != 1 {
				t.Errorf("issued %d writes, want the URL sent once either way", *calls)
			}
			if tc.noOp {
				if err != nil {
					t.Fatalf("%v: %v — a certain no-op must not wait for a change", tc.args, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "did not apply") {
				t.Fatalf("%v = %v, want the read-back to wait and fail", tc.args, err)
			}
		})
	}
}

// --create-tags makes a missing tag before the write, so Things applies it
// and the edit is a real change that waits for its read-back.
func TestEditCreatedTagIsAChange(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stmts := []string{
		`INSERT INTO TMTag (uuid, title) VALUES ('tag-1', 'Errand')`,
		`INSERT INTO TMTaskTag (tasks, tags) VALUES ('one-1', 'tag-1')`,
	}
	for _, s := range stmts {
		if _, err := sqlDB.Exec(s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	calls := stubExecDropping(t)

	_, err := runOut(t, database, "edit", "one-1", "--tags", "Errand,Ghost", "--create-tags")
	if *calls != 2 {
		t.Errorf("issued %d commands, want the tag created and the URL sent", *calls)
	}
	if err == nil || !strings.Contains(err.Error(), "did not apply") {
		t.Fatalf("err = %v, want the read-back to wait and fail", err)
	}
}

// A database error reading the auth token must reach the user as a warning on
// stderr, not vanish into an empty token that later reads as "auth token is
// required".
func TestEditWarnsWhenAuthTokenReadFails(t *testing.T) {
	for _, args := range [][]string{
		{"edit", "one-1", "--title", "Post the letter"},
		{"project", "edit", "repproj-1", "--title", "Post the letter"},
	} {
		t.Run(args[0], func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			if _, err := sqlDB.Exec(`DROP TABLE TMSettings`); err != nil {
				t.Fatalf("drop settings: %v", err)
			}
			stubExec(t)

			stderr, _ := runCapturingStderr(t, database, args...)
			if !strings.Contains(stderr, "warning: could not read Things auth token") {
				t.Errorf("stderr = %q, want the auth token read warning", stderr)
			}
		})
	}
}

// countingConn counts the queries that read the tag list (ListTags), so a
// test can check how often a command reads it.
type countingConn struct {
	driver.Conn
	tagReads *int
}

func (c countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "FROM TMTag\n") {
		*c.tagReads++
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

type countingConnector struct {
	drv      driver.Driver
	tagReads *int
}

func (c countingConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.drv.Open(":memory:")
	if err != nil {
		return nil, err
	}
	return countingConn{conn, c.tagReads}, nil
}

func (c countingConnector) Driver() driver.Driver { return c.drv }

// An edit with tags reads the tag list once: the tag check and the no-op
// check share the one read.
func TestEditReadsTagListOnce(t *testing.T) {
	fastVerify(t)
	src := dbtest.NewSQL(t)
	tagReads := 0
	sqlDB := sql.OpenDB(countingConnector{drv: src.Driver(), tagReads: &tagReads})
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	rows, err := src.Query(`SELECT sql FROM sqlite_master WHERE sql IS NOT NULL`)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	for rows.Next() {
		var stmt string
		if err := rows.Scan(&stmt); err != nil {
			t.Fatalf("scan schema: %v", err)
		}
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("apply %q: %v", stmt, err)
		}
	}
	_ = rows.Close()
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Todo("one-1", "Post letter", 1)
	fx.Tag("tag-1", "Errand", 1)
	if _, err := sqlDB.Exec(`INSERT INTO TMSettings (uuid, uriSchemeAuthenticationToken) VALUES ('s1', 'tok')`); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	stubExecDropping(t)

	_, _ = runOut(t, db.NewFromSQL(sqlDB), "edit", "one-1", "--add-tags", "Errand,Ghost")
	if tagReads != 1 {
		t.Errorf("read the tag list %d times, want once", tagReads)
	}
}

// Things matches an update's list, heading and area titles ignoring case but
// not surrounding space. When nothing matches it leaves the item where it is,
// and drops a heading it cannot find, all without a word; an unknown id is
// dropped the same way, and a move to where the item already is records no
// change. The edit warns on
// stderr when that will happen and still sends the write. A move Things will
// drop is no change, so an edit made only of one prints the item at once
// rather than waiting for a change that never comes; anything else still
// waits, which the dropping stub turns into a failure.
func TestEditWarnsOnUnresolvedMove(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		want     string // "" for no warning
		noChange bool
	}{
		{"knownList", []string{"edit", "one-1", "--list", "tools"}, "", false},
		{"knownArea", []string{"edit", "one-1", "--list", "Personal"}, "", false},
		{"listUUID", []string{"edit", "one-1", "--list", "proj-1"}, "", false},
		{"knownHeading", []string{"edit", "one-1", "--list", "Tools", "--heading", "setup"}, "", false},
		{"unknownList", []string{"edit", "one-1", "--list", "Nowhere"}, `no project or area called "Nowhere"; the to-do will stay where it is`, true},
		{"paddedList", []string{"edit", "one-1", "--list", " Tools "}, `no project or area called " Tools "`, true},
		{"completedProject", []string{"edit", "one-1", "--list", "Old"}, `no project or area called "Old"`, true},
		{"unknownListAndTitle", []string{"edit", "one-1", "--list", "Nowhere", "--title", "Post the letter"}, `no project or area called "Nowhere"`, false},
		{"unknownListAndHeading", []string{"edit", "one-1", "--list", "Nowhere", "--heading", "Setup"}, `no project or area called "Nowhere"; the to-do will stay where it is`, true},
		{"unknownListAndHeadingInProject", []string{"edit", "tool-1", "--list", "Nowhere", "--heading", "Setup"}, `no project or area called "Nowhere"; it will look for --heading "Setup" in the to-do's own project`, false},
		{"unknownListAndOwnHeading", []string{"edit", "head-todo", "--list", "Nowhere", "--heading", "setup"}, `look for --heading "setup"`, true},
		{"unknownListID", []string{"edit", "one-1", "--list-id", "nope"}, `no project or area with id "nope"; the to-do will stay where it is`, true},
		{"listIDIsTitle", []string{"edit", "one-1", "--list-id", "Tools"}, `no project or area with id "Tools"`, true},
		{"knownListID", []string{"edit", "one-1", "--list-id", "proj-1"}, "", false},
		{"sameList", []string{"edit", "tool-1", "--list", "tools"}, "", true},
		{"sameListFullwidth", []string{"edit", "tool-1", "--list", "\uff34ools"}, "", true},
		{"sameAreaFullwidth", []string{"edit", "area-todo", "--list", "\uff30ersonal"}, "", true},
		{"sameListUUID", []string{"edit", "tool-1", "--list", "proj-1"}, "", true},
		{"sameListID", []string{"edit", "tool-1", "--list-id", "proj-1"}, "", true},
		{"sameArea", []string{"edit", "area-todo", "--list", "personal"}, "", true},
		{"sameListAndTitle", []string{"edit", "tool-1", "--list", "Tools", "--title", "Oil the hinges"}, "", false},
		{"sameListFromHeading", []string{"edit", "head-todo", "--list", "Tools"}, "", false},
		{"sameHeading", []string{"edit", "head-todo", "--heading", "SETUP"}, "", true},
		{"sameListAndHeading", []string{"edit", "head-todo", "--list", "Tools", "--heading", "Setup"}, "", true},
		{"sameHeadingID", []string{"edit", "head-todo", "--heading-id", "head-1"}, "", true},
		{"sameHeadingIDOtherList", []string{"edit", "head-todo", "--list", "Personal", "--heading-id", "head-1"}, "", true},
		{"headingIDElsewhere", []string{"edit", "one-1", "--heading-id", "head-1"}, "", false},
		{"unknownHeadingID", []string{"edit", "tool-1", "--heading-id", "nope"}, `no heading with id "nope"; the to-do will stay where it is`, true},
		{"unknownHeadingIDWithList", []string{"edit", "one-1", "--list", "Tools", "--heading-id", "nope"}, `no heading with id "nope"; it will ignore --heading-id`, false},
		{"unknownHeading", []string{"edit", "one-1", "--list", "Tools", "--heading", "Later"}, `"Tools" has no heading "Later"; Things will move the to-do there without a heading`, false},
		{"paddedHeading", []string{"edit", "one-1", "--list", "Tools", "--heading", " Setup "}, `"Tools" has no heading " Setup "`, false},
		{"unknownHeadingInListID", []string{"edit", "one-1", "--list-id", "proj-1", "--heading", "Later"}, `"proj-1" has no heading "Later"`, false},
		{"knownHeadingInListID", []string{"edit", "one-1", "--list-id", "proj-1", "--heading", "Setup"}, "", false},
		{"unknownHeadingInListUUID", []string{"edit", "one-1", "--list", "proj-1", "--heading", "Later"}, `"proj-1" has no heading "Later"`, false},
		{"headingInAreaUUID", []string{"edit", "one-1", "--list", "area-1", "--heading", "Setup"}, `"area-1" has no heading "Setup"`, false},
		{"headingOnlyKnown", []string{"edit", "tool-1", "--heading", "SETUP"}, "", false},
		// Of two headings that differ only in case, Things files the to-do
		// under the one with the lower uuid, whichever case was sent.
		{"caseTwinHeading", []string{"edit", "twin-todo", "--heading", "SETUP"}, "", false},
		{"caseTwinListAndHeading", []string{"edit", "twin-todo", "--list", "Tools", "--heading", "SETUP"}, "", false},
		{"headingOnlyUnknown", []string{"edit", "tool-1", "--heading", "Later"}, `"Tools" has no heading "Later"; Things will leave the to-do where it is`, true},
		{"headingOnlyNoProject", []string{"edit", "one-1", "--heading", "Setup"}, `--heading "Setup" needs --list: the to-do is not in a project, so Things will leave it where it is`, true},
		{"headingID", []string{"edit", "one-1", "--list", "Tools", "--heading-id", "head-1"}, "", false},
		{"projectKnownArea", []string{"project", "edit", "repproj-1", "--area", "personal"}, "", false},
		{"projectAreaUUID", []string{"project", "edit", "repproj-1", "--area", "area-1"}, "", false},
		{"projectUnknownArea", []string{"project", "edit", "repproj-1", "--area", "Nowhere"}, `no area called "Nowhere"; the project will stay where it is`, true},
		{"projectPaddedArea", []string{"project", "edit", "repproj-1", "--area", " Personal "}, `no area called " Personal "`, true},
		{"projectSameArea", []string{"project", "edit", "areaproj-1", "--area", "PERSONAL"}, "", true},
		{"projectSameAreaFullwidth", []string{"project", "edit", "areaproj-1", "--area", "\uff30ERSONAL"}, "", true},
		{"projectSameAreaUUID", []string{"project", "edit", "areaproj-1", "--area", "area-1"}, "", true},
		{"projectSameAreaID", []string{"project", "edit", "areaproj-1", "--area-id", "area-1"}, "", true},
		// Two areas are called Personal, and Things picks area-1, whose
		// uuid sorts first, so a project or to-do in area-3 moves.
		{"projectSharedAreaOther", []string{"project", "edit", "areaproj-3", "--area", "personal"}, "", false},
		{"sharedAreaOther", []string{"edit", "area3-todo", "--list", "Personal"}, "", false},
		// A repeating project's template is no title match, but its own
		// heading is still found by the project's uuid.
		{"sameHeadingInTemplate", []string{"edit", "rephead-todo", "--heading", "setup"}, "", true},
		// Measured in Things 3: a heading alone moves a to-do under it in its
		// own project when that project is logged or trashed, too.
		{"headingInLoggedProject", []string{"edit", "logged-todo", "--heading", "setup"}, "", false},
		{"headingInTrashedProject", []string{"edit", "trashed-todo", "--heading", "setup"}, "", false},
		// What Things does with an empty area was not checked.
		{"projectEmptyArea", []string{"project", "edit", "repproj-1", "--area", ""}, "", false},
		{"projectKnownAreaID", []string{"project", "edit", "repproj-1", "--area-id", "area-1"}, "", false},
		{"projectUnknownAreaID", []string{"project", "edit", "repproj-1", "--area-id", "nope"}, `no area with id "nope"; the project will stay where it is`, true},
		{"projectUnknownAreaAndTitle", []string{"project", "edit", "repproj-1", "--area", "Nowhere", "--title", "Monthly review"}, `no area called "Nowhere"`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Area("area-1", "Personal", 1)
			fx.Project("proj-1", "Tools", 5)
			fx.Project("proj-done", "Old", 6, dbtest.Status(model.StatusCompleted))
			fx.Heading("head-1", "Setup", 1, dbtest.InProject("proj-1"))
			fx.Todo("tool-1", "Oil hinges", 7, dbtest.Anytime(), dbtest.InProject("proj-1"))
			fx.Todo("head-todo", "Buy oil", 8, dbtest.Anytime(), dbtest.UnderHeading("head-1"))
			fx.Heading("head-2", "SETUP", 2, dbtest.InProject("proj-1"))
			fx.Todo("twin-todo", "Buy rags", 11, dbtest.Anytime(), dbtest.UnderHeading("head-2"))
			fx.Todo("area-todo", "Sweep", 9, dbtest.Anytime(), dbtest.InArea("area-1"))
			fx.Project("areaproj-1", "Garden", 10, dbtest.InArea("area-1"))
			fx.Area("area-3", "personal", 3)
			fx.Project("areaproj-3", "Shed", 11, dbtest.InArea("area-3"))
			fx.Todo("area3-todo", "Mop", 12, dbtest.Anytime(), dbtest.InArea("area-3"))
			fx.Heading("head-rep", "Setup", 13, dbtest.InProject("repproj-1"))
			fx.Todo("rephead-todo", "Plan week", 14, dbtest.Anytime(), dbtest.UnderHeading("head-rep"))
			fx.Project("proj-logged", "Shipped", 15, dbtest.Completed(model.TimeToUnix(time.Now().Add(-48*time.Hour))))
			fx.Heading("head-logged", "Setup", 16, dbtest.InProject("proj-logged"))
			fx.Todo("logged-todo", "Write notes", 17, dbtest.Anytime(), dbtest.InProject("proj-logged"))
			fx.Project("proj-trashed", "Binned", 18, dbtest.Trashed())
			fx.Heading("head-trashed", "Setup", 19, dbtest.InProject("proj-trashed"))
			fx.Todo("trashed-todo", "Bin notes", 20, dbtest.Anytime(), dbtest.InProject("proj-trashed"))
			calls := stubExecDropping(t)

			_, stderr, err := runStreams(t, database, tc.args...)
			if *calls != 1 {
				t.Errorf("issued %d writes, want the URL sent once either way", *calls)
			}
			if tc.noChange {
				if err != nil {
					t.Fatalf("%v: %v — a move Things will drop must not wait for a change", tc.args, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "did not apply") {
				t.Fatalf("%v = %v, want the read-back to wait and fail", tc.args, err)
			}
			if tc.want == "" {
				if strings.Contains(stderr, "warning: ") {
					t.Errorf("stderr = %q, want no warning", stderr)
				}
				return
			}
			if !strings.Contains(stderr, "warning: ") || !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want a warning containing %q", stderr, tc.want)
			}
		})
	}
}

// Things matches an update's list and area by title only, so a uuid left the
// item where it was. A uuid of a project or area now goes to Things as
// list-id or area-id; a title still goes as list or area.
func TestEditSendsUUIDAsListID(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"projectUUID", []string{"edit", "one-1", "--list", "proj-1"}, "list-id=proj-1"},
		{"paddedUUID", []string{"edit", "one-1", "--list", " proj-1 "}, "list-id=proj-1"},
		{"areaUUID", []string{"edit", "one-1", "--list", "area-1"}, "list-id=area-1"},
		{"title", []string{"edit", "one-1", "--list", "Tools"}, "list=Tools"},
		{"unknown", []string{"edit", "one-1", "--list", "Nowhere"}, "list=Nowhere"},
		{"projectAreaUUID", []string{"project", "edit", "repproj-1", "--area", "area-1"}, "area-id=area-1"},
		{"projectPaddedAreaUUID", []string{"project", "edit", "repproj-1", "--area", " area-1 "}, "area-id=area-1"},
		{"projectAreaTitle", []string{"project", "edit", "repproj-1", "--area", "Personal"}, "area=Personal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Area("area-1", "Personal", 1)
			fx.Project("proj-1", "Tools", 5)
			var url string
			prev := things.SetExecCommandForTest(func(_ string, args ...string) *exec.Cmd {
				url = args[len(args)-1]
				return exec.Command("true")
			})
			t.Cleanup(func() { things.SetExecCommandForTest(prev) })

			_, _, _ = runStreams(t, database, append([]string{"--no-verify"}, tc.args...)...)
			params := strings.Split(url[strings.Index(url, "?")+1:], "&")
			if !slices.Contains(params, tc.want) {
				t.Errorf("url = %q, want %s", url, tc.want)
			}
			if key, _, _ := strings.Cut(tc.want, "-id="); key != tc.want {
				for _, p := range params {
					if strings.HasPrefix(p, key+"=") {
						t.Errorf("url = %q, want no %s title beside %s-id", url, key, key)
					}
				}
			}
		})
	}
}
