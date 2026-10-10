package main

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/things"
)

// A list, area or heading name goes to Things as given when Things matches
// it as given, trimmed when only that form matches, and as given when
// neither does. A title that really has surrounding space keeps it.
func TestTargetNamesSentTrimmedWhenOnlyThatMatches(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"addList", []string{"add", "Buy oat milk", "--list", " Tools "}, []string{"list=Tools"}},
		{"addProject", []string{"add", "Buy oat milk", "--project", " Tools ", "--heading", " Setup "}, []string{"list=Tools", "heading=Setup"}},
		{"addArea", []string{"add", "Buy oat milk", "--list", " Personal "}, []string{"list=Personal"}},
		{"addUnknown", []string{"add", "Buy oat milk", "--list", " Nowhere "}, []string{"list=%20Nowhere%20"}},
		{"addUnknownHeading", []string{"add", "Buy oat milk", "--list", "Tools", "--heading", " Later "}, []string{"list=Tools", "heading=%20Later%20"}},
		{"addPaddedTitle", []string{"add", "Buy oat milk", "--list", "Errands "}, []string{"list=Errands%20"}},
		{"projectAdd", []string{"project", "add", "Launch", "--area", " Personal "}, []string{"area=Personal"}},
		{"projectAddPaddedTitle", []string{"project", "add", "Launch", "--area", "Errands "}, []string{"area=Errands%20"}},
		{"edit", []string{"edit", "one-1", "--list", " Tools ", "--heading", " Setup "}, []string{"list=Tools", "heading=Setup"}},
		{"editOwnHeading", []string{"edit", "tool-1", "--heading", " Setup "}, []string{"heading=Setup"}},
		{"editUnknown", []string{"edit", "one-1", "--list", " Nowhere "}, []string{"list=%20Nowhere%20"}},
		{"projectEdit", []string{"project", "edit", "garden-1", "--area", " Personal "}, []string{"area=Personal"}},
		{"projectEditPaddedTitle", []string{"project", "edit", "garden-1", "--area", "Errands "}, []string{"area=Errands%20"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Area("area-1", "Personal", 1)
			fx.Area("area-2", "Errands ", 2)
			fx.Project("proj-1", "Tools", 5)
			fx.Project("garden-1", "Garden", 6)
			fx.Heading("head-1", "Setup", 1, dbtest.InProject("proj-1"))
			fx.Todo("tool-1", "Oil hinges", 8, dbtest.Anytime(), dbtest.InProject("proj-1"))
			var url string
			prev := things.SetExecCommandForTest(func(_ string, args ...string) *exec.Cmd {
				url = args[len(args)-1]
				return exec.Command("true")
			})
			t.Cleanup(func() { things.SetExecCommandForTest(prev) })

			_, stderr, _ := runStreams(t, database, append([]string{"--no-verify"}, tc.args...)...)
			params := strings.Split(url[strings.Index(url, "?")+1:], "&")
			for _, want := range tc.want {
				if !slices.Contains(params, want) {
					t.Errorf("url = %q, want %s", url, want)
				}
			}
			if strings.Contains(tc.name, "Unknown") != strings.Contains(stderr, "warning: ") {
				t.Errorf("stderr = %q, want a warning only for a name neither form matches", stderr)
			}
		})
	}
}
