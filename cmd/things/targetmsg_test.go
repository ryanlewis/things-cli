package main

import (
	"strings"
	"testing"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
)

// Agents read the warnings add, edit and project give when Things will not
// file an item where the flags say, so each is pinned here line for line.
func TestTargetWarningsExact(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"addUnknownList", []string{"add", "Buy oat milk", "--list", "Nowhere"},
			`Things finds no project or area called "Nowhere"; it will put the to-do in the Inbox`},
		{"addUnknownHeading", []string{"add", "Buy oat milk", "--list", "Tools", "--heading", "Later"},
			`"Tools" has no heading "Later"; Things will add the to-do there without a heading`},
		{"addUnknownHeadingInListUUID", []string{"add", "Buy oat milk", "--list", "proj-1", "--heading", "Later"},
			`"Tools" has no heading "Later"; Things will add the to-do there without a heading`},
		{"addHeadingWithoutList", []string{"add", "Buy oat milk", "--heading", "Setup"},
			`--heading "Setup" needs --list or --project; Things will ignore it and put the to-do in the Inbox`},
		{"editUnknownList", []string{"edit", "one-1", "--list", "Nowhere"},
			`Things finds no project or area called "Nowhere"; the to-do will stay where it is`},
		{"editUnknownListOwnHeading", []string{"edit", "tool-1", "--list", "Nowhere", "--heading", "Setup"},
			`Things finds no project or area called "Nowhere"; it will look for --heading "Setup" in the to-do's own project`},
		{"editUnknownListEmptyHeading", []string{"edit", "head-todo", "--list", "Nowhere", "--heading", ""},
			`Things finds no project or area called "Nowhere"; it will take the to-do out of its heading`},
		{"editUnknownListID", []string{"edit", "one-1", "--list-id", "nope"},
			`Things finds no project or area with id "nope"; the to-do will stay where it is`},
		{"editListIDIsTitle", []string{"edit", "one-1", "--list-id", "Tools"},
			`Things finds no project or area with id "Tools"; the to-do will stay where it is`},
		{"editUnknownHeading", []string{"edit", "one-1", "--list", "Tools", "--heading", "Later"},
			`"Tools" has no heading "Later"; Things will move the to-do there without a heading`},
		{"editUnknownHeadingInListID", []string{"edit", "one-1", "--list-id", "proj-1", "--heading", "Later"},
			`"Tools" has no heading "Later"; Things will move the to-do there without a heading`},
		{"editUnknownOwnHeading", []string{"edit", "tool-1", "--heading", "Later"},
			`"Tools" has no heading "Later"; Things will leave the to-do where it is`},
		{"editHeadingNoProject", []string{"edit", "one-1", "--heading", "Setup"},
			`--heading "Setup" needs --list or --list-id, as the to-do is not in a project; Things will ignore it and leave the to-do where it is`},
		{"editUnknownHeadingID", []string{"edit", "tool-1", "--heading-id", "nope"},
			`Things has no heading with id "nope"; the to-do will stay where it is`},
		{"editUnknownHeadingIDWithList", []string{"edit", "one-1", "--list", "Tools", "--heading-id", "nope"},
			`Things has no heading with id "nope"; it will ignore --heading-id`},
		{"projectAddUnknownArea", []string{"project", "add", "Launch", "--area", "Nowhere"},
			`Things finds no area called "Nowhere"; it will create the project with no area`},
		{"projectEditUnknownArea", []string{"project", "edit", "garden-1", "--area", "Nowhere"},
			`Things finds no area called "Nowhere"; the project will stay where it is`},
		{"projectEditUnknownAreaID", []string{"project", "edit", "garden-1", "--area-id", "nope"},
			`Things finds no area with id "nope"; the project will stay where it is`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Project("proj-1", "Tools", 5)
			fx.Project("garden-1", "Garden", 6)
			fx.Heading("head-1", "Setup", 1, dbtest.InProject("proj-1"))
			fx.Todo("tool-1", "Oil hinges", 8, dbtest.Anytime(), dbtest.InProject("proj-1"))
			fx.Todo("head-todo", "Buy oil", 9, dbtest.Anytime(), dbtest.UnderHeading("head-1"))
			stubExecDropping(t)

			// The read-back fails, as the write is dropped; only the
			// warning matters here.
			_, stderr, _ := runStreams(t, database, tc.args...)
			if !strings.Contains(stderr, "warning: "+tc.want+"\n") {
				t.Errorf("stderr = %q, want the line %q", stderr, "warning: "+tc.want)
			}
		})
	}
}

// import builds its list and area warnings with the same builders, under the
// item's path.
func TestImportTargetWarningsExact(t *testing.T) {
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("proj-1", "Tools", 5)
	stubExecDropping(t)

	payload := `[
	  {"type":"to-do","attributes":{"title":"a","list":"Nowhere"}},
	  {"type":"to-do","attributes":{"title":"b","list-id":"nope"}},
	  {"type":"to-do","attributes":{"title":"c","list":"proj-1"}},
	  {"type":"to-do","attributes":{"title":"d","list":"Tools","heading":"Setup"}},
	  {"type":"to-do","attributes":{"title":"e","list":"Tools","heading-id":"nope"}},
	  {"type":"to-do","attributes":{"title":"f","heading":"Setup"}},
	  {"type":"to-do","attributes":{"title":"g","list-id":"proj-1","heading":"Later"}},
	  {"type":"project","attributes":{"title":"Shed","area":"Nowhere"}},
	  {"type":"project","attributes":{"title":"Barn","area-id":"nope"}}
	]`
	_, stderr, err := runImportOut(t, database, payload, "--no-verify")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, want := range []string{
		`[0]: Things finds no project or area called "Nowhere"; it will put the to-do in the Inbox`,
		`[1]: Things finds no project or area with id "nope"; it will put the to-do in the Inbox`,
		`[2]: list "proj-1" is an id, and Things matches list by title only; it will put the to-do in the Inbox (use list-id)`,
		`[3]: "Tools" has no heading "Setup"; Things will add the to-do there without a heading`,
		`[4]: Things has no heading with id "nope"; it will ignore heading-id and heading`,
		`[5]: heading "Setup" needs list or list-id; Things will ignore it and put the to-do in the Inbox`,
		`[6]: "Tools" has no heading "Later"; Things will add the to-do there without a heading`,
		`[7]: Things finds no area called "Nowhere"; it will create the project in no area`,
		`[8]: Things finds no area with id "nope"; it will create the project in no area`,
	} {
		if !strings.Contains(stderr, "warning: "+want+"\n") {
			t.Errorf("stderr = %q, want the line %q", stderr, "warning: "+want)
		}
	}
}
