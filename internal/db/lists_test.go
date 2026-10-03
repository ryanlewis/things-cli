package db

import (
	"testing"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
)

// AddTarget matches the way Things filed throwaway to-dos in a real run: an
// open project or an area by title, ignoring case; never a completed or
// trashed project; a heading only inside the project it names.
func TestAddTarget(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Area("area-1", "Personal", 1)
	fx.Project("proj-1", "Tools", 1)
	fx.Project("proj-done", "Old", 2, dbtest.Status(model.StatusCompleted))
	fx.Project("proj-trash", "Binned", 3, dbtest.Trashed())
	fx.Heading("head-1", "Ärger", 1, dbtest.InProject("proj-1"))
	fx.Heading("head-trash", "Gone", 2, dbtest.InProject("proj-1"), dbtest.Trashed())
	d := &DB{db: sqlDB}

	cases := []struct {
		list, heading  string
		listOK, headOK bool
	}{
		{"Tools", "", true, false},
		{"tools", "", true, false},
		{"personal", "", true, false},
		{"Nowhere", "", false, false},
		{"Old", "", false, false},
		{"Binned", "", false, false},
		{"Tools", "ärger", true, true},
		{"Tools", "Nope", true, false},
		{"Tools", "Gone", true, false},
		{"Personal", "Ärger", true, false},
		{"Nowhere", "Ärger", false, false},
	}
	for _, tc := range cases {
		listOK, headOK, err := d.AddTarget(tc.list, tc.heading)
		if err != nil {
			t.Fatalf("AddTarget(%q, %q): %v", tc.list, tc.heading, err)
		}
		if listOK != tc.listOK || headOK != tc.headOK {
			t.Errorf("AddTarget(%q, %q) = %v, %v, want %v, %v", tc.list, tc.heading, listOK, headOK, tc.listOK, tc.headOK)
		}
	}
}
