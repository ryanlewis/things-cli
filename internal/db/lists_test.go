package db

import (
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
)

// AddTarget matches the way Things filed throwaway to-dos in a real run: an
// open project or an area by title, ignoring case but not surrounding space;
// a project closed today and not yet logged, but never a logged or trashed
// one; a heading only inside the project it names, matched the same way.
func TestAddTarget(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Area("area-1", "Personal", 1)
	fx.Area("area-pad", "Errands ", 2)
	fx.Project("proj-1", "Tools", 1)
	fx.Project("proj-done", "Old", 2, dbtest.Status(model.StatusCompleted))
	fx.Project("proj-trash", "Binned", 3, dbtest.Trashed())
	fx.Project("proj-today", "Shelved", 4, dbtest.Cancelled(model.TimeToUnix(time.Now())))
	fx.Project("proj-logged", "Shipped", 5, dbtest.Completed(model.TimeToUnix(time.Now().Add(-48*time.Hour))))
	fx.Heading("head-1", "Ärger", 1, dbtest.InProject("proj-1"))
	fx.Heading("head-trash", "Gone", 2, dbtest.InProject("proj-1"), dbtest.Trashed())
	fx.Heading("head-sharp", "Straße", 3, dbtest.InProject("proj-1"))
	fx.Heading("head-nfd", "Noe\u0308l", 4, dbtest.InProject("proj-1"))
	fx.Heading("head-wide", "\uff26ull", 5, dbtest.InProject("proj-1"))
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
		{"Shelved", "", true, false},
		{"proj-today", "", true, false},
		{"Shipped", "", false, false},
		{"Tools", "ärger", true, true},
		{"Tools", "Nope", true, false},
		{"Tools", "Gone", true, false},
		{"Personal", "Ärger", true, false},
		{"Nowhere", "Ärger", false, false},
		{" Tools ", "", false, false},
		{" Personal ", "", false, false},
		{" proj-1 ", "", true, false}, // a uuid is sent trimmed, as list-id
		{"Tools", " Ärger ", true, false},
		{"Errands ", "", true, false},
		{"Errands", "", false, false},
		{"Ｔools", "", true, false}, // compatibility forms match, as in Things
		{"Personal\u00a0", "", false, false},
		{"Errands\u00a0", "", true, false},
		// Checked in Things 3: headings fold fully and across NFC and NFD,
		// but a compatibility form such as fullwidth does not match.
		{"Tools", "Ärgeｒ", true, false},
		{"Tools", "STRASSE", true, true},
		{"Tools", "No\u00ebl", true, true},
		{"Tools", "Full", true, false},
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

// Projects, or areas, whose titles fold together: Things files into the one
// whose uuid sorts first byte by byte, whichever title list matches exactly
// and whatever the insert order and index. "B…" sorts before "a…" byte by
// byte but not ignoring case, so the uuids tell the two orders apart.
func TestAddTargetPicksSmallestUUID(t *testing.T) {
	for _, order := range [][2]string{{"work", "Work"}, {"Work", "work"}} {
		sqlDB := dbtest.NewSQL(t)
		fx := dbtest.NewFixture(t, sqlDB)
		uuids := map[string]string{"work": "B-lower", "Work": "a-upper"}
		for i, title := range order {
			fx.Project(uuids[title], title, i+1)
			fx.Area(uuids[title]+"-area", title+" area", i+1)
		}
		fx.Heading("head-1", "Setup", 1, dbtest.InProject("B-lower"))
		d := &DB{db: sqlDB}

		for _, list := range []string{"Work", "work", "WORK"} {
			target, headOK, err := d.AddTarget(list, "Setup")
			if err != nil {
				t.Fatalf("AddTarget(%q): %v", list, err)
			}
			if target != "B-lower" || !headOK {
				t.Errorf("insert order %v: AddTarget(%q, Setup) = %q, %v, want B-lower, true", order, list, target, headOK)
			}
			area, _, err := d.AddTarget(list+" area", "")
			if err != nil {
				t.Fatalf("AddTarget(%q area): %v", list, err)
			}
			if area != "B-lower-area" {
				t.Errorf("insert order %v: AddTarget(%q area) = %q, want B-lower-area", order, list, area)
			}
		}
	}
}

// A project closed today still holds its place in Things, so it is a
// candidate with the smallest uuid; a logged or trashed one, or a repeating
// project's template, is not.
func TestAddTargetSkipsLoggedForSmallestUUID(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("A-today", "Work", 1, dbtest.Completed(model.TimeToUnix(time.Now())))
	fx.Project("B-open", "work", 2)
	fx.Project("1-trash", "WORK", 3, dbtest.Trashed())
	fx.Project("2-logged", "Work", 4, dbtest.Completed(model.TimeToUnix(time.Now().Add(-48*time.Hour))))
	fx.Project("x-open", "Plan", 5)
	fx.Project("1-template", "Plan", 7, dbtest.Repeats())
	fx.Project("Y-logged", "Plan", 6, dbtest.Cancelled(model.TimeToUnix(time.Now().Add(-48*time.Hour))))
	d := &DB{db: sqlDB}
	for list, want := range map[string]string{"work": "A-today", "plan": "x-open"} {
		target, _, err := d.AddTarget(list, "")
		if err != nil {
			t.Fatalf("AddTarget(%q): %v", list, err)
		}
		if target != want {
			t.Errorf("AddTarget(%q) = %q, want %q", list, target, want)
		}
	}
}

// A heading-id counts only for an untrashed heading, never another item.
func TestHeadingExists(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("proj-1", "Tools", 1)
	fx.Heading("head-1", "Setup", 1, dbtest.InProject("proj-1"))
	fx.Heading("head-trash", "Gone", 2, dbtest.InProject("proj-1"), dbtest.Trashed())
	d := &DB{db: sqlDB}
	for uuid, want := range map[string]bool{"head-1": true, "head-trash": false, "proj-1": false, "nope": false} {
		got, err := d.HeadingExists(uuid)
		if err != nil {
			t.Fatalf("HeadingExists(%q): %v", uuid, err)
		}
		if got != want {
			t.Errorf("HeadingExists(%q) = %v, want %v", uuid, got, want)
		}
	}
}
