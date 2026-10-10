package db

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
)

func TestFindDBPathNoMatch(t *testing.T) {
	// Override HOME to an empty tempdir — the glob should find nothing.
	t.Setenv("HOME", t.TempDir())
	_, err := FindDBPath()
	if err == nil {
		t.Fatal("expected error when DB not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFindDBPathSingleMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Library", "Group Containers",
		"JLMPQHK86H.com.culturedcode.ThingsMac", "ThingsData-ABC",
		"Things Database.thingsdatabase")
	mustMkdirAll(t, dir)
	mustCreate(t, filepath.Join(dir, "main.sqlite"))

	got, err := FindDBPath()
	if err != nil {
		t.Fatalf("FindDBPath: %v", err)
	}
	if got != filepath.Join(dir, "main.sqlite") {
		t.Errorf("got %q, want %q", got, filepath.Join(dir, "main.sqlite"))
	}
}

func TestFindDBPathMultipleMatches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, id := range []string{"A", "B"} {
		dir := filepath.Join(home, "Library", "Group Containers",
			"JLMPQHK86H.com.culturedcode.ThingsMac", "ThingsData-"+id,
			"Things Database.thingsdatabase")
		mustMkdirAll(t, dir)
		mustCreate(t, filepath.Join(dir, "main.sqlite"))
	}
	_, err := FindDBPath()
	if err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("expected multiple error, got %v", err)
	}
}

// denyStat wraps os.Stat, refusing the database path under each named
// ThingsData folder the way macOS refuses an app-group container.
func denyStat(t *testing.T, container string, folders ...string) statFunc {
	t.Helper()
	denied := map[string]bool{}
	for _, f := range folders {
		denied[filepath.Join(container, f, "Things Database.thingsdatabase", "main.sqlite")] = true
	}
	return func(path string) (os.FileInfo, error) {
		if denied[path] {
			return nil, &os.PathError{Op: "stat", Path: path, Err: fs.ErrPermission}
		}
		return os.Stat(path)
	}
}

func makeThingsData(t *testing.T, container, folder string) string {
	t.Helper()
	dir := filepath.Join(container, folder, "Things Database.thingsdatabase")
	mustMkdirAll(t, dir)
	path := filepath.Join(dir, "main.sqlite")
	mustCreate(t, path)
	return path
}

func thingsContainer(home string) string {
	return filepath.Join(home, "Library", "Group Containers", thingsGroupContainer)
}

func TestFindDBPathContainerPermissionDenied(t *testing.T) {
	home := t.TempDir()
	container := thingsContainer(home)
	diagnosis, err := findDBPath(home, func(path string) ([]os.DirEntry, error) {
		if path != container {
			t.Fatalf("ReadDir(%q), want %q", path, container)
		}
		return nil, &os.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	}, os.Stat)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("err = %v, want one wrapping fs.ErrPermission", err)
	}
	if errors.Is(err, fs.ErrNotExist) || strings.Contains(err.Error(), "not found") {
		t.Errorf("a refusal reads as a missing database: %v", err)
	}
	if diagnosis.Container != container || diagnosis.Pattern == "" {
		t.Errorf("diagnosis = %+v, want the container and pattern filled in", diagnosis)
	}
}

func TestFindDBPathDatabasePermissionDenied(t *testing.T) {
	home := t.TempDir()
	container := thingsContainer(home)
	makeThingsData(t, container, "ThingsData-ABC")
	_, err := findDBPath(home, os.ReadDir, denyStat(t, container, "ThingsData-ABC"))
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("err = %v, want one wrapping fs.ErrPermission", err)
	}
}

// An unreadable ThingsData folder must not hide a readable one holding the
// database: Glob skipped the unreadable one, and so does the walk.
func TestFindDBPathPermissionDeniedBesideMatch(t *testing.T) {
	home := t.TempDir()
	container := thingsContainer(home)
	mustMkdirAll(t, filepath.Join(container, "ThingsData-OLD"))
	want := makeThingsData(t, container, "ThingsData-NEW")
	diagnosis, err := findDBPath(home, os.ReadDir, denyStat(t, container, "ThingsData-OLD"))
	if err != nil {
		t.Fatalf("findDBPath: %v", err)
	}
	if diagnosis.Database != want {
		t.Errorf("Database = %q, want %q", diagnosis.Database, want)
	}
}

// A file named like a ThingsData folder fails stat with ENOTDIR. Glob skipped
// it, so the walk must too.
func TestFindDBPathSkipsStrayFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	container := thingsContainer(home)
	mustMkdirAll(t, container)
	mustCreate(t, filepath.Join(container, "ThingsData-stray"))
	want := makeThingsData(t, container, "ThingsData-ABC")
	got, err := FindDBPath()
	if err != nil {
		t.Fatalf("FindDBPath: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindDBPathStrayFileOnlyIsNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	container := thingsContainer(home)
	mustMkdirAll(t, container)
	mustCreate(t, filepath.Join(container, "ThingsData-stray"))
	_, err := FindDBPath()
	var notFound *NotFoundError
	if !errors.As(err, &notFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want a NotFoundError", err)
	}
}

func TestFindDBPathMultipleIsTyped(t *testing.T) {
	home := t.TempDir()
	container := thingsContainer(home)
	makeThingsData(t, container, "ThingsData-A")
	makeThingsData(t, container, "ThingsData-B")
	diagnosis, err := findDBPath(home, os.ReadDir, os.Stat)
	var multiple *MultipleError
	if !errors.As(err, &multiple) || len(multiple.Matches) != 2 {
		t.Fatalf("err = %v, want a MultipleError with two matches", err)
	}
	if diagnosis.Database != "" || len(diagnosis.Matches) != 2 {
		t.Errorf("diagnosis = %+v, want two matches and no database", diagnosis)
	}
}

func TestProbeReadablePreservesPermissionError(t *testing.T) {
	err := probeReadable("/protected/main.sqlite", func(path string) (*os.File, error) {
		return nil, &os.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	})
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("err = %v, want one wrapping fs.ErrPermission", err)
	}
}

func TestProbeReadableAllowsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.sqlite")
	mustCreate(t, path)
	if err := probeReadable(path, os.Open); err != nil {
		t.Fatalf("probeReadable: %v", err)
	}
}

func TestOpenReportsPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	path := filepath.Join(t.TempDir(), "locked.sqlite")
	mustCreate(t, path)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Open err = %v, want one wrapping fs.ErrPermission", err)
	}
}

func TestOpenReadOnly(t *testing.T) {
	// Create an empty DB file, open it, confirm close works.
	path := filepath.Join(t.TempDir(), "t.sqlite")
	mustCreate(t, path)
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestNewFromSQL(t *testing.T) {
	sqlDB := dbtest.NewSQL(t)
	d := NewFromSQL(sqlDB)
	if d == nil || d.db == nil {
		t.Fatal("NewFromSQL returned empty DB")
	}
	if _, err := d.db.Exec(`INSERT INTO TMArea (uuid, title, visible) VALUES ('x', 'y', 1)`); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

func TestOpenBadPath(t *testing.T) {
	// modernc.org/sqlite only errors at Exec time for bad paths on some
	// platforms — but the PRAGMA query_only pragma will execute, so a
	// non-existent path through a non-existent directory should fail.
	_, err := Open("/nonexistent/dir/does/not/exist.sqlite")
	if err == nil {
		t.Fatal("expected error for bad path")
	}
}

// Empty result sets must be non-nil slices: a nil slice JSON-encodes as
// `null`, which breaks documented `--json | jq '.[]'` pipelines.
func TestEmptyResultsAreNonNil(t *testing.T) {
	d := newTestDB(t)

	tasks := mustList(t, d, "today", TaskFilter{})
	if tasks == nil || len(tasks) != 0 {
		t.Errorf("ListTasks: want non-nil empty slice, got %#v", tasks)
	}

	found, err := d.SearchTasks("no-such-task")
	if err != nil {
		t.Fatalf("SearchTasks: %v", err)
	}
	if found == nil || len(found) != 0 {
		t.Errorf("SearchTasks: want non-nil empty slice, got %#v", found)
	}

	projects, err := d.ListProjects("", false, false)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if projects == nil || len(projects) != 0 {
		t.Errorf("ListProjects: want non-nil empty slice, got %#v", projects)
	}

	areas, err := d.ListAreas()
	if err != nil {
		t.Fatalf("ListAreas: %v", err)
	}
	if areas == nil || len(areas) != 0 {
		t.Errorf("ListAreas: want non-nil empty slice, got %#v", areas)
	}

	tags, err := d.ListTags()
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if tags == nil || len(tags) != 0 {
		t.Errorf("ListTags: want non-nil empty slice, got %#v", tags)
	}
}
