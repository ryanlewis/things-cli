package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
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
