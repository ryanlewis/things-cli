package db

import (
	"testing"

	"github.com/ryanlewis/things-cli/internal/model"
)

func TestGetChecklistItemsOrderedAndDated(t *testing.T) {
	d := newTestDB(t)

	stopTS := model.TimeToUnix(mustTime("2026-04-10T10:00:00Z"))
	mustExec(t, d, `INSERT INTO TMChecklistItem (uuid, title, status, stopDate, "index", task) VALUES
		('c1', 'step B', 0, NULL,       2, 'task1'),
		('c2', 'step A', 3, ?,          1, 'task1'),
		('c3', 'other',  0, NULL,       1, 'other-task')`,
		stopTS)

	items, err := d.GetChecklistItems("task1")
	if err != nil {
		t.Fatalf("GetChecklistItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Title != "step A" || items[1].Title != "step B" {
		t.Errorf("unexpected order: %+v", items)
	}
	if items[0].StopDate == nil {
		t.Errorf("step A should have stopDate")
	}
	if items[1].StopDate != nil {
		t.Errorf("step B should not have stopDate")
	}
}

func TestGetChecklistItemsEmpty(t *testing.T) {
	d := newTestDB(t)
	items, err := d.GetChecklistItems("nope")
	if err != nil {
		t.Fatalf("GetChecklistItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty, got %d", len(items))
	}
}

// A listing carries each to-do's checklist counts, so a row can show progress
// without fetching the items. Cancelled items count as no longer open, like
// completed ones; a to-do without a checklist carries none. Tags must not be
// repeated by the count, which a join onto the items would do.
func TestListTasksCarriesChecklistProgress(t *testing.T) {
	d, f := newFixture(t)
	f.Tag("g1", "home", 0)
	f.Todo("with", "Pack for the trip", 0, anytime())
	f.Tagged("with", "g1")
	f.Todo("without", "Water the plants", 1, anytime())
	mustExec(t, d, `INSERT INTO TMChecklistItem (uuid, title, status, "index", task) VALUES
		('c1', 'passport', 3, 0, 'with'),
		('c2', 'charger',  0, 1, 'with'),
		('c3', 'socks',    2, 2, 'with'),
		('c4', 'tickets',  0, 3, 'with')`)

	tasks, err := d.ListTasks("anytime", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	byUUID := map[string]model.Task{}
	for _, task := range tasks {
		byUUID[task.UUID] = task
	}
	with := byUUID["with"]
	if with.ChecklistProgress == nil || *with.ChecklistProgress != (model.ChecklistProgress{Total: 4, Open: 2}) {
		t.Errorf("with: checklist progress %+v, want 4 total, 2 open", with.ChecklistProgress)
	}
	if len(with.Tags) != 1 {
		t.Errorf("with: tags %v, want one", with.Tags)
	}
	if p := byUUID["without"].ChecklistProgress; p != nil {
		t.Errorf("without: checklist progress %+v, want none", p)
	}

	got, err := d.GetTaskByUUID("with")
	if err != nil {
		t.Fatalf("GetTaskByUUID: %v", err)
	}
	if got.ChecklistProgress == nil || got.ChecklistProgress.Done() != 2 {
		t.Errorf("GetTaskByUUID: checklist progress %+v, want 2 done", got.ChecklistProgress)
	}
}
