package db

import (
	"testing"

	"github.com/ryanlewis/things-cli/internal/model"
)

func TestGetChecklistItemsOrderedAndDated(t *testing.T) {
	d := newTestDB(t)

	stopTS := model.TimeToCoreData(mustTime("2026-04-10T10:00:00Z"))
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
