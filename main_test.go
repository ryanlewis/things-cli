package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/alecthomas/kong"
	_ "modernc.org/sqlite"

	"github.com/ryanlewis/things-cli/internal/cache"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
)

func parse(t *testing.T, args ...string) (*CLI, *kong.Context) {
	t.Helper()
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("things"))
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	ctx, err := parser.Parse(args)
	if err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return &cli, ctx
}

func TestKongListDefault(t *testing.T) {
	_, ctx := parse(t, "list")
	if ctx.Command() != "list" {
		t.Errorf("ctx.Command() = %q", ctx.Command())
	}
}

func TestKongListView(t *testing.T) {
	cli, ctx := parse(t, "list", "today")
	if ctx.Command() != "list <args>" {
		t.Errorf("ctx.Command() = %q", ctx.Command())
	}
	if len(cli.List.Args) != 1 || cli.List.Args[0] != "today" {
		t.Errorf("Args = %v", cli.List.Args)
	}
}

func TestKongAddFlags(t *testing.T) {
	cli, ctx := parse(t,
		"add", "Buy milk",
		"--notes", "2 liters",
		"--when", "today",
		"--deadline", "2026-05-01",
		"--tags", "shop",
		"--project", "Home",
	)
	if ctx.Command() != "add <title>" {
		t.Errorf("Command = %q", ctx.Command())
	}
	if cli.Add.Title != "Buy milk" || cli.Add.Notes != "2 liters" ||
		cli.Add.When != "today" || cli.Add.Deadline != "2026-05-01" ||
		cli.Add.Tags != "shop" || cli.Add.Project != "Home" {
		t.Errorf("add flags not parsed correctly: %+v", cli.Add)
	}
}

func TestKongShow(t *testing.T) {
	cli, ctx := parse(t, "show", "my task")
	if ctx.Command() != "show <task>" {
		t.Errorf("Command = %q", ctx.Command())
	}
	if cli.Show.Task != "my task" {
		t.Errorf("Task = %q", cli.Show.Task)
	}
}

func TestKongCompleteCancel(t *testing.T) {
	cli, ctx := parse(t, "complete", "abc-123")
	if ctx.Command() != "complete <task>" || cli.Complete.Task != "abc-123" {
		t.Errorf("complete parse: cmd=%q task=%q", ctx.Command(), cli.Complete.Task)
	}
	cli2, ctx2 := parse(t, "cancel", "xyz")
	if ctx2.Command() != "cancel <task>" || cli2.Cancel.Task != "xyz" {
		t.Errorf("cancel parse: cmd=%q task=%q", ctx2.Command(), cli2.Cancel.Task)
	}
}

func TestKongSearch(t *testing.T) {
	cli, ctx := parse(t, "search", "foo bar")
	if ctx.Command() != "search <query>" || cli.Search.Query != "foo bar" {
		t.Errorf("search parse: cmd=%q query=%q", ctx.Command(), cli.Search.Query)
	}
}

func TestKongJSONFlag(t *testing.T) {
	cli, _ := parse(t, "--json", "list")
	if !cli.JSON {
		t.Error("expected JSON=true")
	}
}

func TestCacheTaskUUIDs(t *testing.T) {
	orig := cache.Dir
	cache.Dir = t.TempDir()
	t.Cleanup(func() { cache.Dir = orig })

	tasks := []model.Task{
		{UUID: "u1"}, {UUID: "u2"}, {UUID: "u3"},
	}
	cacheTaskUUIDs(tasks)

	got, err := cache.ReadLastList()
	if err != nil {
		t.Fatalf("ReadLastList: %v", err)
	}
	if len(got) != 3 || got[0] != "u1" || got[2] != "u3" {
		t.Errorf("cached list = %v", got)
	}
}

// openTestDB creates a real sqlite file seeded with the internal/db testdata
// schema and one task, then opens it via db.Open. Used by the resolveTask
// test to exercise the cache-index branch that is unique to main.go.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.sqlite")

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	schema, err := os.ReadFile(filepath.Join("internal", "db", "testdata", "schema.sql"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if _, err := sqlDB.Exec(string(schema)); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed) VALUES ('abc-123', 'Cached task', 0, 0, 0)`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = sqlDB.Close()

	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestResolveTaskNumericFromCache(t *testing.T) {
	orig := cache.Dir
	cache.Dir = t.TempDir()
	t.Cleanup(func() { cache.Dir = orig })

	if err := cache.WriteLastList([]string{"abc-123", "other"}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	database := openTestDB(t)

	got, err := resolveTask("1", database)
	if err != nil {
		t.Fatalf("resolveTask: %v", err)
	}
	if got.UUID != "abc-123" || got.Title != "Cached task" {
		t.Errorf("got %+v", got)
	}
}

func TestResolveTaskStaleCacheIndex(t *testing.T) {
	orig := cache.Dir
	cache.Dir = t.TempDir()
	t.Cleanup(func() { cache.Dir = orig })

	if err := cache.WriteLastList([]string{"missing-uuid"}); err != nil {
		t.Fatal(err)
	}
	database := openTestDB(t)

	_, err := resolveTask("1", database)
	if err == nil {
		t.Fatal("expected stale cache error")
	}
}
