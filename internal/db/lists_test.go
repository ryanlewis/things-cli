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
		{"proj-1", "", true, false},
		{"area-1", "", true, false},
		{"proj-1", "Ärger", true, true},
		{"proj-done", "", false, false},
		{"proj-trash", "", false, false},
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
		target, headOK, err := d.AddTarget(tc.list, tc.heading)
		listOK := target != ""
		if err != nil {
			t.Fatalf("AddTarget(%q, %q): %v", tc.list, tc.heading, err)
		}
		if listOK != tc.listOK || headOK != tc.headOK {
			t.Errorf("AddTarget(%q, %q) = %v, %v, want %v, %v", tc.list, tc.heading, listOK, headOK, tc.listOK, tc.headOK)
		}
	}
}

// Open projects whose titles differ only in case: the exact-case title is the
// one checked for the heading, whichever row the database returns first.
func TestAddTargetPrefersExactCase(t *testing.T) {
	for _, order := range [][2]string{{"work", "Work"}, {"Work", "work"}} {
		sqlDB := dbtest.NewSQL(t)
		fx := dbtest.NewFixture(t, sqlDB)
		uuids := map[string]string{"work": "proj-lower", "Work": "proj-upper"}
		for i, title := range order {
			fx.Project(uuids[title], title, i+1)
		}
		fx.Heading("head-1", "Setup", 1, dbtest.InProject("proj-upper"))
		d := &DB{db: sqlDB}

		for _, tc := range []struct {
			list   string
			headOK bool
		}{{"Work", true}, {"work", false}} {
			_, headOK, err := d.AddTarget(tc.list, "Setup")
			if err != nil {
				t.Fatalf("AddTarget(%q): %v", tc.list, err)
			}
			if headOK != tc.headOK {
				t.Errorf("insert order %v: AddTarget(%q, Setup) heading = %v, want %v", order, tc.list, headOK, tc.headOK)
			}
		}
	}
}
