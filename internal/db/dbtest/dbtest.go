// Package dbtest provides helpers for tests that need a Things3-shaped SQLite.
package dbtest

import (
	"database/sql"
	_ "embed"
	"path/filepath"
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

// NewFileSQL is a file-backed sibling of NewSQL: it creates a temp-file SQLite
// with the Things3 schema applied and returns the file path alongside an open
// handle (closed via t.Cleanup). Use it when a separate process must open the
// same database — e.g. spawning the built binary with `--db <path>`.
func NewFileSQL(t *testing.T) (path string, sqlDB *sql.DB) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "main.sqlite")
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.Exec(schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return path, sqlDB
}
