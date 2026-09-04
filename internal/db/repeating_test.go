package db

import (
	"testing"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
)

func seedRepeatingPair(t *testing.T) *DB {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, start, rt1_recurrenceRule)
		 VALUES ('rep-1', 'Water plants', 0, 0, 0, 2, x'0102')`,
	); err != nil {
		t.Fatalf("seed repeating: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, start)
		 VALUES ('one-1', 'Post letter', 0, 0, 0, 2)`,
	); err != nil {
		t.Fatalf("seed one-off: %v", err)
	}
	return &DB{db: sqlDB}
}

func TestGetTaskByUUIDReportsRepeating(t *testing.T) {
	d := seedRepeatingPair(t)

	rep, err := d.GetTaskByUUID("rep-1")
	if err != nil {
		t.Fatalf("GetTaskByUUID(rep-1): %v", err)
	}
	if !rep.Repeating {
		t.Error("task with a recurrence rule: Repeating = false, want true")
	}

	one, err := d.GetTaskByUUID("one-1")
	if err != nil {
		t.Fatalf("GetTaskByUUID(one-1): %v", err)
	}
	if one.Repeating {
		t.Error("task without a recurrence rule: Repeating = true, want false")
	}
}

// Listing and search go through the same templated query, so the flag has to
// survive every path a caller can reach a task by.
func TestListAndSearchReportRepeating(t *testing.T) {
	d := seedRepeatingPair(t)

	tasks, err := d.ListTasks("someday", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(someday): %v", err)
	}
	got := map[string]bool{}
	for _, task := range tasks {
		got[task.UUID] = task.Repeating
	}
	if !got["rep-1"] || got["one-1"] {
		t.Errorf("someday repeating flags = %v, want rep-1 true and one-1 false", got)
	}

	found, err := d.SearchTasks("plants")
	if err != nil {
		t.Fatalf("SearchTasks: %v", err)
	}
	if len(found) != 1 || !found[0].Repeating {
		t.Errorf("SearchTasks(plants) = %+v, want one repeating task", found)
	}
}

// Older Things schemas name the column differently, and a future one could
// drop it. The probe must degrade to "nothing repeats" rather than making
// every task query fail.
func TestRepeatingColumnAbsentDegradesGracefully(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	if _, err := sqlDB.Exec(`ALTER TABLE TMTask DROP COLUMN rt1_recurrenceRule`); err != nil {
		t.Fatalf("drop column: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed) VALUES ('t1', 'Anything', 0, 0, 0)`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	d := &DB{db: sqlDB}

	if expr := d.repeatingExpr(); expr != "0" {
		t.Errorf("repeatingExpr() = %q, want %q", expr, "0")
	}
	task, err := d.GetTaskByUUID("t1")
	if err != nil {
		t.Fatalf("GetTaskByUUID: %v", err)
	}
	if task == nil || task.Repeating {
		t.Errorf("got %+v, want a task with Repeating false", task)
	}
}

func TestRepeatingExprIsProbedOnce(t *testing.T) {
	d := seedRepeatingPair(t)
	first := d.repeatingExpr()
	if first == "0" {
		t.Fatalf("repeatingExpr() = %q, want a recurrence-column expression", first)
	}
	if second := d.repeatingExpr(); second != first {
		t.Errorf("repeatingExpr() = %q on second call, want the cached %q", second, first)
	}
}

func TestTableColumnsUnknownTable(t *testing.T) {
	d := &DB{db: dbtest.NewSQL(t)}
	cols, err := d.tableColumns("NoSuchTable")
	if err != nil {
		t.Fatalf("tableColumns: %v", err)
	}
	if len(cols) != 0 {
		t.Errorf("tableColumns(NoSuchTable) = %v, want empty", cols)
	}
}
