package main

import (
	"strings"
	"testing"

	"github.com/alecthomas/kong"

	"github.com/ryanlewis/things-cli/internal/cache"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

func parse(t *testing.T, args ...string) (*CLI, *kong.Context) {
	t.Helper()
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("things"),
		kong.Vars{
			"builtin_lists": strings.Join(things.BuiltinLists, ", "),
			"skill_agents":  skillAgentNames(),
		},
	)
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

func TestKongSkillCommands(t *testing.T) {
	cases := []struct {
		args    []string
		command string
		check   func(*CLI) bool
	}{
		{[]string{"skill", "list"}, "skill list", func(*CLI) bool { return true }},
		{[]string{"skill", "show"}, "skill show", func(c *CLI) bool { return c.Skill.Show.Agent == "" }},
		{[]string{"skill", "show", "claude"}, "skill show <agent>", func(c *CLI) bool { return c.Skill.Show.Agent == "claude" }},
		{[]string{"skill", "install", "claude"}, "skill install <agent>", func(c *CLI) bool { return c.Skill.Install.Agent == "claude" && !c.Skill.Install.Yes }},
		{[]string{"skill", "install", "claude", "-y"}, "skill install <agent>", func(c *CLI) bool { return c.Skill.Install.Yes }},
		{[]string{"skill", "install", "claude", "--path", "/tmp/x"}, "skill install <agent>", func(c *CLI) bool { return c.Skill.Install.Path == "/tmp/x" }},
		{[]string{"skill", "uninstall", "claude", "-y"}, "skill uninstall <agent>", func(c *CLI) bool { return c.Skill.Uninstall.Yes }},
	}
	for _, tc := range cases {
		cli, ctx := parse(t, tc.args...)
		if ctx.Command() != tc.command {
			t.Errorf("%v: Command = %q, want %q", tc.args, ctx.Command(), tc.command)
		}
		if !tc.check(cli) {
			t.Errorf("%v: check failed, CLI = %+v", tc.args, cli.Skill)
		}
	}
}

func TestKongJSONFlag(t *testing.T) {
	cli, _ := parse(t, "--json", "list")
	if !cli.JSON {
		t.Error("expected JSON=true")
	}
}

func TestCacheTaskUUIDs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

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

func seedResolveTaskDB(t *testing.T) *db.DB {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed) VALUES ('abc-123', 'Cached task', 0, 0, 0)`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db.NewFromSQL(sqlDB)
}

func TestResolveTaskNumericFromCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := cache.WriteLastList([]string{"abc-123", "other"}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	database := seedResolveTaskDB(t)

	got, err := resolveTask("1", database)
	if err != nil {
		t.Fatalf("resolveTask: %v", err)
	}
	if got.UUID != "abc-123" || got.Title != "Cached task" {
		t.Errorf("got %+v", got)
	}
}

func TestResolveTaskStaleCacheIndex(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := cache.WriteLastList([]string{"missing-uuid"}); err != nil {
		t.Fatal(err)
	}
	database := seedResolveTaskDB(t)

	_, err := resolveTask("1", database)
	if err == nil {
		t.Fatal("expected stale cache error")
	}
}
