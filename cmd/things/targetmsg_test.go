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
			`"proj-1" has no heading "Later"; Things will move the to-do there without a heading`},
		{"editUnknownOwnHeading", []string{"edit", "tool-1", "--heading", "Later"},
			`"Tools" has no heading "Later"; Things will leave the to-do where it is`},
		{"editHeadingNoProject", []string{"edit", "one-1", "--heading", "Setup"},
			`--heading "Setup" needs --list: the to-do is not in a project, so Things will leave it where it is`},
		{"editUnknownHeadingID", []string{"edit", "tool-1", "--heading-id", "nope"},
			`Things has no heading with id "nope"; the to-do will stay where it is`},
		{"editUnknownHeadingIDWithList", []string{"edit", "one-1", "--list", "Tools", "--heading-id", "nope"},
			`Things has no heading with id "nope"; it will ignore --heading-id`},
		{"projectAddUnknownArea", []string{"project", "add", "Launch", "--area", "Nowhere"},
			`Things has no area called "Nowhere"; it will create the project with no area`},
		{"projectEditUnknownArea", []string{"project", "edit", "garden-1", "--area", "Nowhere"},
			`Things has no area called "Nowhere"; the project will stay where it is`},
		{"projectEditUnknownAreaID", []string{"project", "edit", "garden-1", "--area-id", "nope"},
			`Things has no area with id "nope"; the project will stay where it is`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Area("area-1", "Personal", 1)
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
