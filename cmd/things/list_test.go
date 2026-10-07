package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
)

// seedRepeatingProjectDB seeds a repeating project template holding one to-do
// beside an ordinary project holding another, which is the pair the note has
// to tell apart.
func seedRepeatingProjectDB(t *testing.T) *db.DB {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("p-tmpl", "Weekly review", 1, dbtest.Repeats())
	fx.Project("p-real", "Ship it", 2)
	fx.Todo("t-child", "Inside the template", 1, dbtest.Anytime(), dbtest.InProject("p-tmpl"))
	fx.Todo("t-plain", "Inside a real one", 2, dbtest.Anytime(), dbtest.InProject("p-real"))
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

// seedStuckRowDB seeds a to-do scheduled for today that Things has not yet
// moved out of start = 2 (issue #363), beside an ordinary Someday to-do.
func seedStuckRowDB(t *testing.T) *db.DB {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	today := int64(model.ThingsDateFromTime(testNow))
	fx.Todo("t-stuck", "Scheduled today, not yet moved", 1, dbtest.SomedayOn(today))
	fx.Todo("t-someday", "Some day", 2, dbtest.Someday())
	return db.NewFromSQL(sqlDB)
}

// --json reports the start the listing and the app go by: a stuck row is
// today's, so it says "anytime", while a Someday row still says "someday".
func TestStuckRowJSONStartIsAnytime(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "today"},
		{"--json", "search", "Scheduled today"},
	} {
		stdout, _, err := runStreams(t, seedStuckRowDB(t), args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var tasks []model.Task
		if err := json.Unmarshal([]byte(stdout), &tasks); err != nil {
			t.Fatalf("%v: unmarshal %q: %v", args, stdout, err)
		}
		if len(tasks) != 1 || tasks[0].UUID != "t-stuck" || tasks[0].Start != model.StartAnytime {
			t.Errorf("%v: got %+v, want t-stuck with start anytime", args, tasks)
		}
	}

	stdout, _, err := runStreams(t, seedStuckRowDB(t), "--json", "show", "t-stuck")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"start": "anytime"`) {
		t.Errorf("show t-stuck --json = %q, want start anytime", stdout)
	}
	stdout, _, err = runStreams(t, seedStuckRowDB(t), "--json", "show", "t-someday")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"start": "someday"`) {
		t.Errorf("show t-someday --json = %q, want start someday", stdout)
	}
}

// The plain-text listing marks the stuck row with the star every to-do
// scheduled for today carries.
func TestStuckRowPlainTextIsToday(t *testing.T) {
	stdout, _, err := runStreams(t, seedStuckRowDB(t), "--no-hints", "today")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "★ Scheduled today, not yet moved") {
		t.Errorf("today = %q, want the stuck row starred", stdout)
	}
	if strings.Contains(stdout, "Some day") {
		t.Errorf("today = %q, want no Someday row", stdout)
	}
}
