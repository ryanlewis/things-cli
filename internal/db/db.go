package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"

	"github.com/ryanlewis/things-cli/internal/model"
)

type DB struct {
	db *sql.DB

	// repeatColumn caches the probed recurrence-column name ("" when the
	// schema carries none) and repeatQuery the task query built from it; see
	// (*DB).probeRepeating.
	repeatOnce   sync.Once
	repeatColumn string
	repeatQuery  string
}

// FindDBPath locates the Things3 SQLite database.
func FindDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	pattern := filepath.Join(home, "Library", "Group Containers",
		"JLMPQHK86H.com.culturedcode.ThingsMac", "ThingsData-*",
		"Things Database.thingsdatabase", "main.sqlite")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("globbing database path: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("Things3 database not found at %s", pattern)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple Things3 databases found: %v", matches)
	}
	return matches[0], nil
}

// Open opens a read-only connection to the Things3 database.
func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec("PRAGMA query_only = ON"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("setting query_only pragma: %w", err)
	}
	return &DB{db: sqlDB}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

// NewFromSQL wraps an existing *sql.DB. Test-only; production code uses Open.
func NewFromSQL(sqlDB *sql.DB) *DB {
	return &DB{db: sqlDB}
}

// thingsDate decodes a nullable Things date column (startDate, deadline) into
// the model's bit-encoded date. A NULL column — an unscheduled item — yields
// nil rather than the zero date, which would decode to a nonsense day.
// scanTask in tasks.go still inlines the same two decodes; folding it onto
// this helper is left to whoever next edits that file.
func thingsDate(v sql.NullFloat64) *model.ThingsDate {
	if !v.Valid {
		return nil
	}
	d := model.ThingsDate(int64(v.Float64))
	return &d
}
