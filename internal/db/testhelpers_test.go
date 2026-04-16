package db

import (
	"os"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	return &DB{db: dbtest.NewSQL(t)}
}

func mustExec(t *testing.T, d *DB, query string, args ...any) {
	t.Helper()
	if _, err := d.db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustCreate(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	_ = f.Close()
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
