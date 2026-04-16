// Package dbtest provides helpers for tests that need a Things3-shaped SQLite.
package dbtest

import (
	"database/sql"
	_ "embed"
	"testing"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// NewSQL returns an in-memory *sql.DB with the Things3 schema applied.
// Closed via t.Cleanup.
func NewSQL(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open :memory: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.Exec(schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return sqlDB
}
