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

// A --project value that is the uuid of a to-do, a heading or an area is
// refused as not a project rather than listing nothing with exit 0 (issue
// #419). A project's uuid and a title no project carries still list.
func TestListProjectRefusesNonProjectUUID(t *testing.T) {
	seed := func(t *testing.T) *db.DB {
		sqlDB := dbtest.NewSQL(t)
		fx := dbtest.NewFixture(t, sqlDB)
		fx.Area("area-1", "Home", 1)
		fx.Project("p-1", "Ship it", 1)
		fx.Heading("h-1", "Setup", 1, dbtest.InProject("p-1"))
		fx.Todo("t-1", "Child", 1, dbtest.Anytime(), dbtest.InProject("p-1"))
		return db.NewFromSQL(sqlDB)
	}
	cases := []struct {
		ref, kind, message string
	}{
		{"t-1", "task", `"Child" is a task; use things show`},
		{"h-1", "heading", `"Setup" is a heading; use things --project p-1`},
		{"area-1", "area", `"Home" is an area; use things --area`},
	}
	for _, tc := range cases {
		for _, view := range []string{"", "anytime"} {
			t.Run(tc.ref+" "+view, func(t *testing.T) {
				args := []string{"--json", "--project", tc.ref}
				if view != "" {
					args = append([]string{"--json", view}, args[1:]...)
				}
				stdout, _, err := runStreams(t, seed(t), args...)
				if err == nil {
					t.Fatalf("expected a refusal, got stdout %q", stdout)
				}
				if err.Error() != tc.message {
					t.Errorf("message = %q, want %q", err.Error(), tc.message)
				}
				payload, raw := decodePayload(t, err)
				if payload.Error != "not a project" || payload.Kind != tc.kind || payload.Query != tc.ref || payload.UUID != tc.ref {
					t.Errorf("payload = %s, want not a project naming %s as a %s", raw, tc.ref, tc.kind)
				}
			})
		}
	}
	for _, ref := range []string{"p-1", "Ship it", "No such thing"} {
		t.Run("lists "+ref, func(t *testing.T) {
			if _, _, err := runStreams(t, seed(t), "--json", "--project", ref); err != nil {
				t.Errorf("run: %v", err)
			}
		})
	}
}
