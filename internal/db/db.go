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
	"syscall"
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
	// templateColumn is the probed column linking a generated instance to
	// its repeating template ("" when the schema carries none).
	templateColumn string
}

const thingsGroupContainer = "JLMPQHK86H.com.culturedcode.ThingsMac"

// PathDiagnosis is what automatic discovery looked at: the container it
// listed, the pattern it matched against, and the databases it found. It
// holds paths only, never database contents.
type PathDiagnosis struct {
	Home      string
	Container string
	Pattern   string
	Matches   []string
	Database  string
}

// NotFoundError means discovery could list the Things container and found no
// database in it. It matches fs.ErrNotExist.
type NotFoundError struct {
	Pattern string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("Things3 database not found at %s", e.Pattern)
}

func (e *NotFoundError) Unwrap() error { return fs.ErrNotExist }

// MultipleError means more than one ThingsData folder holds a database, so
// picking one would be a guess.
type MultipleError struct {
	Matches []string
}

func (e *MultipleError) Error() string {
	return fmt.Sprintf("multiple Things3 databases found: %v", e.Matches)
}

// FindDBPath locates the Things3 SQLite database.
func FindDBPath() (string, error) {
	diagnosis, err := DiagnoseDBPath()
	return diagnosis.Database, err
}

// DiagnoseDBPath runs automatic discovery and returns what it inspected, even
// when discovery fails. It never opens the database.
func DiagnoseDBPath() (PathDiagnosis, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return PathDiagnosis{}, fmt.Errorf("finding home directory: %w", err)
	}
	return findDBPath(home, os.ReadDir, os.Stat)
}

type (
	readDirFunc func(string) ([]os.DirEntry, error)
	statFunc    func(string) (os.FileInfo, error)
)

// findDBPath walks the Things container by hand rather than with
// filepath.Glob. Glob drops every I/O error, so a container macOS refuses to
// list looks exactly like one with no database in it; the walk keeps the
// permission error so it can be reported as such. It is still only returned
// when no database turned up: an unreadable ThingsData folder beside a
// readable one with the database in it does not stop discovery.
func findDBPath(home string, readDir readDirFunc, stat statFunc) (PathDiagnosis, error) {
	container := filepath.Join(home, "Library", "Group Containers", thingsGroupContainer)
	diagnosis := PathDiagnosis{
		Home:      home,
		Container: container,
		Pattern:   filepath.Join(container, "ThingsData-*", "Things Database.thingsdatabase", "main.sqlite"),
	}

	entries, err := readDir(container)
	if err != nil {
		if missing(err) {
			return diagnosis, &NotFoundError{Pattern: diagnosis.Pattern}
		}
		return diagnosis, fmt.Errorf("reading the Things3 container: %w", err)
	}

	var unreadable error
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "ThingsData-") {
			continue
		}
		path := filepath.Join(container, entry.Name(), "Things Database.thingsdatabase", "main.sqlite")
		info, err := stat(path)
		switch {
		case err == nil:
			if !info.IsDir() {
				diagnosis.Matches = append(diagnosis.Matches, path)
			}
		case missing(err):
			// No live database in this folder, or a stray file with the
			// prefix: Glob skipped both.
		case unreadable == nil:
			unreadable = fmt.Errorf("inspecting the Things3 database path: %w", err)
		}
	}

	switch {
	case len(diagnosis.Matches) == 1:
		diagnosis.Database = diagnosis.Matches[0]
		return diagnosis, nil
	case len(diagnosis.Matches) > 1:
		return diagnosis, &MultipleError{Matches: diagnosis.Matches}
	case unreadable != nil:
		return diagnosis, unreadable
	default:
		return diagnosis, &NotFoundError{Pattern: diagnosis.Pattern}
	}
}

// missing reports whether err means the path is not there. ENOTDIR counts: a
// file where a directory was expected has nothing under it.
func missing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// Open opens a read-only connection to the Things3 database.
func Open(path string) (*DB, error) {
	// SQLite reports a file it may not read as SQLITE_CANTOPEN (14) and drops
	// the EPERM or EACCES behind it, which is what identifies a macOS privacy
	// refusal. Reading one byte first keeps that error for the caller.
	if err := probeReadable(path, os.Open); err != nil {
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

type openFileFunc func(string) (*os.File, error)

// probeReadable opens path read-only and reads at most one byte. An empty
// file passes: SQLite treats it as an empty database.
func probeReadable(path string, open openFileFunc) error {
	f, err := open(path)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer func() { _ = f.Close() }()
	var b [1]byte
	if _, err := f.Read(b[:]); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("reading database: %w", err)
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
