package dbtest

import "testing"

func TestNewSQL(t *testing.T) {
	sqlDB := NewSQL(t)
	if sqlDB == nil {
		t.Fatal("NewSQL returned nil")
	}
	// Schema is applied: the Things3 tables should exist and accept inserts.
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed) VALUES ('u', 't', 0, 0, 0)`,
	); err != nil {
		t.Fatalf("insert into TMTask: %v", err)
	}
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM TMTask`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
}
