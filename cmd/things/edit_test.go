package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
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
			database, _ := seedWritable(t)
			stubExecDropping(t)

			_, err := runOut(t, database, args...)
			if err == nil || !strings.Contains(err.Error(), "did not apply") {
				t.Fatalf("run %v = %v, want a did-not-apply error", args, err)
			}
		})
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
		// --when always waits: even `today` on an item already in Today may
		// touch its reminder, which the CLI does not read.
		{"whenTodayOnToday", []string{"edit", "one-1", "--when", "today"}, false},
		{"whenPhrase", []string{"edit", "one-1", "--when", "friday"}, false},
		{"allNoOp", []string{"edit", "one-1", "--title", "Post letter", "--notes", "second class", "--add-tags", "errand", "--deadline", "2026-10-15"}, true},
		{"noOpPlusWhen", []string{"edit", "one-1", "--title", "Post letter", "--when", "today"}, false},
		{"mixed", []string{"edit", "one-1", "--title", "Post letter", "--notes", "first class"}, false},
		{"mixedUncoveredFlag", []string{"edit", "one-1", "--title", "Post letter", "--append-notes", "x"}, false},
		{"projectSameTitle", []string{"project", "edit", "repproj-1", "--title", "Weekly review"}, true},
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
