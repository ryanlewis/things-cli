package main

import (
	"encoding/json"
	"strings"
	"testing"
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

	stubExecEditing(t, sqlDB, "")
	if err := runWith(t, database, "edit", "one-1", "--title", "Post the letter"); err != nil {
		t.Fatalf("applied edit: %v", err)
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
			if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start) VALUES ('done-1', 'Filed', 0, 3, 0, 1)`); err != nil {
				t.Fatalf("seed: %v", err)
			}
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
