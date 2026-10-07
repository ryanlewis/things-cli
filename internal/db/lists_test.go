package db

import (
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
)

// AddTarget matches the way Things filed throwaway to-dos in a real run: an
// open project or an area by title, ignoring case but not surrounding space;
// by title, a project closed today and not yet logged, but never a logged or
// trashed one, which only its uuid reaches; a heading only inside the
// project it names, matched the same way.
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
		// By uuid Things files into any project, closed, logged or
		// trashed (measured with list-id).
		{"proj-done", "", true, false},
		{"proj-trash", "", true, false},
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
		listOK := target.UUID != ""
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
			if target.UUID != "B-lower" || !headOK {
				t.Errorf("insert order %v: AddTarget(%q, Setup) = %q, %v, want B-lower, true", order, list, target.UUID, headOK)
			}
			area, _, err := d.AddTarget(list+" area", "")
			if err != nil {
				t.Fatalf("AddTarget(%q area): %v", list, err)
			}
			if area.UUID != "B-lower-area" {
				t.Errorf("insert order %v: AddTarget(%q area) = %q, want B-lower-area", order, list, area.UUID)
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
		if target.UUID != want {
			t.Errorf("AddTarget(%q) = %q, want %q", list, target.UUID, want)
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

// AddTarget reports what the CLI notes about the row it picked: how many
// other rows the title matched, and a project's status and trash state.
func TestAddTargetReportsTarget(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("A-work", "Work", 1, dbtest.Completed(model.TimeToUnix(time.Now())))
	fx.Project("B-work", "WORK", 2)
	fx.Project("C-work", "work", 3)
	fx.Project("D-bin", "Bin", 4, dbtest.Trashed())
	fx.Area("A-home", "Home", 1)
	fx.Area("B-home", "HOME", 2)
	fx.Project("Z-errands", "errands", 5)
	fx.Area("0-errands", "Errands", 3)
	d := &DB{db: sqlDB}

	for _, tc := range []struct {
		list string
		want Target
	}{
		{"work", Target{UUID: "A-work", Title: "Work", Status: model.StatusCompleted, Others: 2}},
		{"B-work", Target{UUID: "B-work", Title: "WORK", ByUUID: true}},
		{"D-bin", Target{UUID: "D-bin", Title: "Bin", Trashed: true, ByUUID: true}},
		{" D-bin ", Target{UUID: "D-bin", Title: "Bin", Trashed: true, ByUUID: true}},
		{"home", Target{UUID: "A-home", Title: "Home", Area: true, Others: 1, OtherAreas: 1}},
		{"B-home", Target{UUID: "B-home", Title: "HOME", Area: true, ByUUID: true}},
		// Measured in Things 3: a project wins over an area of the same
		// title, even when the area's uuid sorts first.
		{"ERRANDS", Target{UUID: "Z-errands", Title: "errands", Others: 1, OtherAreas: 1}},
	} {
		got, _, err := d.AddTarget(tc.list, "")
		if err != nil {
			t.Fatalf("AddTarget(%q): %v", tc.list, err)
		}
		if got != tc.want {
			t.Errorf("AddTarget(%q) = %+v, want %+v", tc.list, got, tc.want)
		}
	}
}

// A repeating project's template is no title match, but its uuid still
// names it, as list-id does in Things.
func TestAddTargetTemplateByUUIDOnly(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("tmpl", "Weekly", 1, dbtest.Repeats())
	fx.Heading("head-t", "Setup", 1, dbtest.InProject("tmpl"))
	d := &DB{db: sqlDB}

	if got, _, err := d.AddTarget("Weekly", ""); err != nil || got.UUID != "" {
		t.Errorf("AddTarget(Weekly) = %+v, %v, want no match", got, err)
	}
	got, headOK, err := d.AddTarget("tmpl", "Setup")
	if err != nil || got.UUID != "tmpl" || !headOK {
		t.Errorf("AddTarget(tmpl, Setup) = %+v, %v, %v, want tmpl with its heading", got, headOK, err)
	}
}

// The other "Move completed items to Logbook" choices decide which closed
// projects a title still matches, as they decide the lists: "immediately"
// (0) holds none back, "manually" (4) every one closed since the last "Log
// Completed Now". Only "daily" was measured in Things.
func TestAddTargetLogInterval(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		interval      int
		today, before bool
	}{
		{0, false, false},
		{4, true, true},
	} {
		sqlDB := dbtest.NewSQL(t)
		fx := dbtest.NewFixture(t, sqlDB)
		fx.Project("p-today", "Today", 1, dbtest.Completed(model.TimeToUnix(now)))
		fx.Project("p-before", "Before", 2, dbtest.Completed(model.TimeToUnix(now.Add(-48*time.Hour))))
		if _, err := sqlDB.Exec(`INSERT INTO TMSettings (uuid, logInterval, manualLogDate) VALUES ('s', ?, ?)`,
			tc.interval, model.TimeToUnix(now.Add(-72*time.Hour))); err != nil {
			t.Fatal(err)
		}
		d := &DB{db: sqlDB}
		for list, want := range map[string]bool{"Today": tc.today, "Before": tc.before} {
			got, _, err := d.AddTarget(list, "")
			if err != nil {
				t.Fatal(err)
			}
			if (got.UUID != "") != want {
				t.Errorf("logInterval %d: AddTarget(%q) = %+v, want match %v", tc.interval, list, got, want)
			}
		}
	}
}
