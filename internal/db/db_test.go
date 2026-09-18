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

func TestFindDBPathReportsContainerPermissionDenied(t *testing.T) {
	home := t.TempDir()
	container := filepath.Join(home, "Library", "Group Containers", thingsGroupContainer)
	want := &os.PathError{Op: "readdir", Path: container, Err: fs.ErrPermission}

	diagnosis, err := findDBPath(home, func(path string) ([]os.DirEntry, error) {
		if path != container {
			t.Fatalf("ReadDir(%q), want %q", path, container)
		}
		return nil, want
	}, os.Stat)
	if err == nil {
		t.Fatal("expected permission error")
	}
	if diagnosis.Status != "permission_denied" {
		t.Errorf("status = %q, want permission_denied", diagnosis.Status)
	}
	var accessErr *PathAccessError
	if !errors.As(err, &accessErr) {
		t.Fatalf("error type = %T, want *PathAccessError", err)
	}
	if accessErr.Path != container {
		t.Errorf("error path = %q, want %q", accessErr.Path, container)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("error does not wrap fs.ErrPermission: %v", err)
	}
}

func TestFindDBPathReportsDatabasePermissionDenied(t *testing.T) {
	home := t.TempDir()
	container := filepath.Join(home, "Library", "Group Containers", thingsGroupContainer)
	dataDir := filepath.Join(container, "ThingsData-ABC")
	mustMkdirAll(t, dataDir)
	wantPath := filepath.Join(dataDir, "Things Database.thingsdatabase", "main.sqlite")

	diagnosis, err := findDBPath(home, os.ReadDir, func(path string) (os.FileInfo, error) {
		if path != wantPath {
			t.Fatalf("Stat(%q), want %q", path, wantPath)
		}
		return nil, &os.PathError{Op: "stat", Path: path, Err: fs.ErrPermission}
	})
	if err == nil {
		t.Fatal("expected permission error")
	}
	if diagnosis.Status != "permission_denied" {
		t.Errorf("status = %q, want permission_denied", diagnosis.Status)
	}
	var accessErr *PathAccessError
	if !errors.As(err, &accessErr) {
		t.Fatalf("error type = %T, want *PathAccessError", err)
	}
	if accessErr.Path != wantPath {
		t.Errorf("error path = %q, want %q", accessErr.Path, wantPath)
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
	_, err := Open("/nonexistent/dir/does/not/exist.sqlite")
	if err == nil {
		t.Fatal("expected error for bad path")
	}
}

func TestProbeDatabaseReadablePreservesPermissionError(t *testing.T) {
	path := "/protected/main.sqlite"
	err := probeDatabaseReadable(path, func(got string) (*os.File, error) {
		if got != path {
			t.Fatalf("open(%q), want %q", got, path)
		}
		return nil, &os.PathError{Op: "open", Path: got, Err: fs.ErrPermission}
	})
	if err == nil {
		t.Fatal("expected permission error")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("error does not wrap fs.ErrPermission: %v", err)
	}
}

func TestProbeDatabaseReadableAllowsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.sqlite")
	mustCreate(t, path)
	if err := probeDatabaseReadable(path, os.Open); err != nil {
		t.Fatalf("probeDatabaseReadable: %v", err)
	}
}

// Empty result sets must be non-nil slices: a nil slice JSON-encodes as
// `null`, which breaks documented `--json | jq '.[]'` pipelines.
func TestEmptyResultsAreNonNil(t *testing.T) {
	d := newTestDB(t)

	tasks, err := d.ListTasks("today", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
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

	projects, err := d.ListProjects("", false)
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
