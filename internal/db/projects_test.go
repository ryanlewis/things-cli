package db

import (
	"testing"

	"github.com/ryanlewis/things-cli/internal/model"
)

func seedProjects(t *testing.T, d *DB) {
	t.Helper()
	mustExec(t, d, `INSERT INTO TMArea (uuid, title, visible, "index") VALUES
		('area-work', 'Work', 1, 1),
		('area-home', 'Home', 1, 2)`)
	mustExec(t, d, `INSERT INTO TMTask
		(uuid, title, type, status, trashed, area, "index",
		 untrashedLeafActionsCount, openUntrashedLeafActionsCount) VALUES
		('p1', 'Ship feature',    1, 0, 0, 'area-work', 1, 5, 2),
		('p2', 'Plan vacation',   1, 0, 0, 'area-home', 1, 3, 3),
		('p3', 'Old done',        1, 3, 0, 'area-work', 2, 4, 0),
		('p4', 'Orphan',          1, 0, 0, NULL,        1, 1, 1),
		('p5', 'Trashed',         1, 0, 1, 'area-work', 9, 0, 0)`)
	mustExec(t, d, `INSERT INTO TMTag (uuid, title, "index") VALUES ('tg1', 'urgent', 1)`)
	mustExec(t, d, `INSERT INTO TMTaskTag (tasks, tags) VALUES ('p1', 'tg1')`)
}

func TestListProjectsOpenOnly(t *testing.T) {
	d := newTestDB(t)
	seedProjects(t, d)

	projects, err := d.ListProjects("", false)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 3 {
		t.Fatalf("got %d, want 3 (open, non-trashed, non-completed): %+v", len(projects), projects)
	}
	// Order: projects with an area come first (p1 then p2), orphan last.
	if projects[0].UUID != "p1" {
		t.Errorf("first project: got %s, want p1", projects[0].UUID)
	}
	if projects[len(projects)-1].UUID != "p4" {
		t.Errorf("last project should be orphan p4, got %s", projects[len(projects)-1].UUID)
	}
}

func TestListProjectsIncludeCompleted(t *testing.T) {
	d := newTestDB(t)
	seedProjects(t, d)

	projects, err := d.ListProjects("", true)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 4 {
		t.Fatalf("want 4 (including completed), got %d", len(projects))
	}
	found := false
	for _, p := range projects {
		if p.UUID == "p3" && p.Status == model.StatusCompleted {
			found = true
		}
	}
	if !found {
		t.Errorf("completed project p3 not returned: %+v", projects)
	}
}

func TestListProjectsAreaFilter(t *testing.T) {
	d := newTestDB(t)
	seedProjects(t, d)

	byUUID, err := d.ListProjects("area-work", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(byUUID) != 1 || byUUID[0].UUID != "p1" {
		t.Errorf("uuid filter: got %+v", byUUID)
	}

	byTitle, err := d.ListProjects("Home", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(byTitle) != 1 || byTitle[0].UUID != "p2" {
		t.Errorf("title filter: got %+v", byTitle)
	}
}

func TestListProjectsCarriesTagsAndCounts(t *testing.T) {
	d := newTestDB(t)
	seedProjects(t, d)

	projects, err := d.ListProjects("area-work", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects", len(projects))
	}
	p := projects[0]
	if p.TaskCount != 5 || p.OpenCount != 2 {
		t.Errorf("counts: got %+v", p)
	}
	if len(p.Tags) != 1 || p.Tags[0] != "urgent" {
		t.Errorf("tags: got %+v", p.Tags)
	}
	if p.AreaTitle != "Work" {
		t.Errorf("area title: got %q", p.AreaTitle)
	}
}
