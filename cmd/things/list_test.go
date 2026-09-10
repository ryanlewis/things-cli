package main

import (
	"strings"
	"testing"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
)

// seedRepeatingProjectDB seeds a repeating project template holding one to-do
// beside an ordinary project holding another, which is the pair the note has
// to tell apart.
func seedRepeatingProjectDB(t *testing.T) *db.DB {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, "index", rt1_recurrenceRule)
		 VALUES ('p-tmpl', 'Weekly review', 1, 0, 0, 1, x'0102')`,
	); err != nil {
		t.Fatalf("seed template project: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, "index")
		 VALUES ('p-real', 'Ship it', 1, 0, 0, 2)`,
	); err != nil {
		t.Fatalf("seed ordinary project: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, project, start, startBucket, "index")
		 VALUES ('t-child', 'Inside the template', 0, 0, 0, 'p-tmpl', 1, 0, 1),
		        ('t-plain', 'Inside a real one',   0, 0, 0, 'p-real', 1, 0, 2)`,
	); err != nil {
		t.Fatalf("seed tasks: %v", err)
	}
	return db.NewFromSQL(sqlDB)
}

const repeatingProjectNote = "is a repeating project template"

// Naming a repeating project template lists nothing, because its to-dos are
// held out of every open view. Say why rather than print an empty list and
// leave the reader guessing (issue #174).
func TestEmptyRepeatingProjectListingIsExplained(t *testing.T) {
	stderr, err := runCapturingStderr(t, seedRepeatingProjectDB(t), "--no-hints", "--project", "Weekly review")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stderr, repeatingProjectNote) {
		t.Errorf("stderr = %q, want the repeating-template note", stderr)
	}
}

// The note is an explanation, not an error, so it must not appear where the
// listing explains itself: a project with tasks, or a reference that names
// nothing at all.
func TestRepeatingProjectNoteStaysQuietOtherwise(t *testing.T) {
	cases := map[string][]string{
		"ordinary project":  {"--no-hints", "--project", "Ship it"},
		"unknown project":   {"--no-hints", "--project", "No such thing"},
		"no project filter": {"--no-hints", "anytime"},
		// logbook and trash report what the database holds, templates and
		// their contents included, so an empty listing there means nothing
		// has closed or been thrown away yet — not that the view hides them.
		"logbook": {"--no-hints", "logbook", "--project", "Weekly review"},
		"trash":   {"--no-hints", "trash", "--project", "Weekly review"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			stderr, err := runCapturingStderr(t, seedRepeatingProjectDB(t), args...)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if strings.Contains(stderr, repeatingProjectNote) {
				t.Errorf("stderr = %q, want no note", stderr)
			}
		})
	}
}

// Under --json the note goes to stderr, so stdout stays a well-formed empty
// array for whatever is parsing it.
func TestRepeatingProjectNoteKeepsJSONStdoutClean(t *testing.T) {
	stdout, stderr, err := runStreams(t, seedRepeatingProjectDB(t), "--json", "--project", "Weekly review")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.TrimSpace(stdout) != "[]" {
		t.Errorf("stdout = %q, want an empty JSON array", stdout)
	}
	if !strings.Contains(stderr, repeatingProjectNote) {
		t.Errorf("stderr = %q, want the repeating-template note", stderr)
	}
}

// The uuid is as good a reference as the title, and it is what an agent holds
// after reading `things repeating`.
func TestRepeatingProjectNoteMatchesByUUID(t *testing.T) {
	stderr, err := runCapturingStderr(t, seedRepeatingProjectDB(t), "--no-hints", "--project", "p-tmpl")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stderr, repeatingProjectNote) {
		t.Errorf("stderr = %q, want the repeating-template note", stderr)
	}
}
