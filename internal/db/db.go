package db

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

const thingsGroupContainer = "JLMPQHK86H.com.culturedcode.ThingsMac"

// PathDiagnosis describes how automatic Things database discovery ended. It
// deliberately contains paths and status only, never database contents.
type PathDiagnosis struct {
	Home      string
	Container string
	Pattern   string
	Database  string
	Matches   []string
	Status    string
}

// PathAccessError means macOS or the filesystem refused to enumerate or
// inspect part of the Things app-group container. filepath.Glob silently
// turns that case into an empty match set, which looks exactly like a missing
// database; keeping the underlying error lets callers distinguish the two.
type PathAccessError struct {
	Path string
	Err  error
}

func (e *PathAccessError) Error() string {
	return fmt.Sprintf("cannot access the Things3 database path %s: %v", e.Path, e.Err)
}

func (e *PathAccessError) Unwrap() error { return e.Err }

// PathNotFoundError means automatic discovery could enumerate the container
// but found no Things database. Pattern is retained for the existing error
// text and for diagnostics.
type PathNotFoundError struct {
	Pattern string
}

func (e *PathNotFoundError) Error() string {
	return fmt.Sprintf("Things3 database not found at %s", e.Pattern)
}

// MultiplePathsError means more than one Things data directory contains a
// database, so choosing one automatically would be ambiguous.
type MultiplePathsError struct {
	Matches []string
}

func (e *MultiplePathsError) Error() string {
	return fmt.Sprintf("multiple Things3 databases found: %v", e.Matches)
}

// FindDBPath locates the Things3 SQLite database.
func FindDBPath() (string, error) {
	diagnosis, err := DiagnoseDBPath()
	return diagnosis.Database, err
}

// DiagnoseDBPath runs automatic database discovery and returns the paths it
// inspected even when discovery fails. It is safe for diagnostic commands:
// it never opens the database or reads task data.
func DiagnoseDBPath() (PathDiagnosis, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return PathDiagnosis{Status: "error"}, fmt.Errorf("finding home directory: %w", err)
	}
	diagnosis, err := findDBPath(home, os.ReadDir, os.Stat)
	return diagnosis, err
}

type readDirFunc func(string) ([]os.DirEntry, error)
type statFunc func(string) (os.FileInfo, error)
type openFileFunc func(string) (*os.File, error)

func findDBPath(home string, readDir readDirFunc, stat statFunc) (PathDiagnosis, error) {
	container := filepath.Join(home, "Library", "Group Containers", thingsGroupContainer)
	pattern := filepath.Join(container, "ThingsData-*",
		"Things Database.thingsdatabase", "main.sqlite")
	diagnosis := PathDiagnosis{Home: home, Container: container, Pattern: pattern}

	entries, err := readDir(container)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			diagnosis.Status = "permission_denied"
			return diagnosis, &PathAccessError{Path: container, Err: err}
		}
		if errors.Is(err, fs.ErrNotExist) {
			diagnosis.Status = "not_found"
			return diagnosis, &PathNotFoundError{Pattern: pattern}
		}
		diagnosis.Status = "error"
		return diagnosis, fmt.Errorf("reading Things3 data directory %s: %w", container, err)
	}

	var matches []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "ThingsData-") {
			continue
		}
		path := filepath.Join(container, entry.Name(),
			"Things Database.thingsdatabase", "main.sqlite")
		info, statErr := stat(path)
		switch {
		case statErr == nil && !info.IsDir():
			matches = append(matches, path)
		case statErr == nil:
			// A directory named main.sqlite is not a database match.
		case errors.Is(statErr, fs.ErrNotExist):
			// This ThingsData directory has no live database.
		case errors.Is(statErr, fs.ErrPermission):
			diagnosis.Status = "permission_denied"
			return diagnosis, &PathAccessError{Path: path, Err: statErr}
		default:
			diagnosis.Status = "error"
			return diagnosis, fmt.Errorf("inspecting Things3 database path %s: %w", path, statErr)
		}
	}

	diagnosis.Matches = matches
	if len(matches) == 0 {
		diagnosis.Status = "not_found"
		return diagnosis, &PathNotFoundError{Pattern: pattern}
	}
	if len(matches) > 1 {
		diagnosis.Status = "multiple"
		return diagnosis, &MultiplePathsError{Matches: matches}
	}
	diagnosis.Status = "ok"
	diagnosis.Database = matches[0]
	return diagnosis, nil
}

// Open opens a read-only connection to the Things3 database.
func Open(path string) (*DB, error) {
	// SQLite reduces several filesystem failures to SQLITE_CANTOPEN (14),
	// losing the underlying EPERM/EACCES that identifies macOS privacy
	// controls. Probe one byte first so callers can still use errors.Is with
	// fs.ErrPermission. This does not modify the database or inspect task data.
	if err := probeDatabaseReadable(path, os.Open); err != nil {
		return nil, err
	}

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

func probeDatabaseReadable(path string, openFile openFileFunc) error {
	file, err := openFile(path)
	if err != nil {
		return fmt.Errorf("opening database file for read probe: %w", err)
	}

	var header [1]byte
	_, readErr := file.Read(header[:])
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("reading database file: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing database file after read probe: %w", closeErr)
	}
	return nil
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
func thingsDate(v sql.NullFloat64) *model.ThingsDate {
	if !v.Valid {
		return nil
	}
	d := model.ThingsDate(int64(v.Float64))
	return &d
}

// unixTime is the same for the absolute-timestamp columns (stopDate,
// creationDate): fractional seconds since the Unix epoch, as model.UnixToTime
// reads them. A NULL column yields nil rather than the epoch itself, which
// would read as a real instant in 1970 — the difference between "never
// closed" and "closed on 1 January 1970" (issue #224).
func unixTime(v sql.NullFloat64) *time.Time {
	if !v.Valid {
		return nil
	}
	ts := model.UnixToTime(v.Float64)
	return &ts
}
