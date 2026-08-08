package db

import (
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
