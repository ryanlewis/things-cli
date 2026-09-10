package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"

	"github.com/ryanlewis/things-cli/internal/cache"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/skill"
	"github.com/ryanlewis/things-cli/internal/things"
)

func parse(t *testing.T, args ...string) (*CLI, *kong.Context) {
	t.Helper()
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("things"),
		kong.Vars{
			"builtin_lists": strings.Join(things.BuiltinLists, ", "),
			"skill_agents":  skill.AgentNames(),
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

func TestKongListIncludeCompleted(t *testing.T) {
	cli, _ := parse(t, "list", "today", "--include-completed")
	if !cli.List.IncludeCompleted {
		t.Errorf("IncludeCompleted = %v, want true", cli.List.IncludeCompleted)
	}

	cli, _ = parse(t, "list", "today")
	if cli.List.IncludeCompleted {
		t.Errorf("IncludeCompleted defaulted to %v, want false", cli.List.IncludeCompleted)
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

// Ensures Run methods write to the injected Deps.Stdout rather than os.Stdout —
// without this, output capture and JSON streaming silently fall back to global
// stdout.
func TestVersionCmdWritesToDepsStdout(t *testing.T) {
	var buf bytes.Buffer
	deps := &Deps{Stdout: &buf}
	if err := (&VersionCmd{}).Run(deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "things ") {
		t.Errorf("expected version banner on Deps.Stdout, got %q", buf.String())
	}
}

// Close on a Deps that never opened a DB must not panic, and must remain safe
// to call twice (defer in main pairs with potential explicit close).
func TestDepsCloseSafeWhenNoDB(t *testing.T) {
	deps := &Deps{}
	deps.Close()
	deps.Close()
}

func TestCacheTaskUUIDs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tasks := []model.Task{
		{UUID: "u1"}, {UUID: "u2"}, {UUID: "u3"},
	}
	cacheTaskUUIDs(&Deps{}, "things today", tasks)

	got, err := cache.ReadLastList()
	if err != nil {
		t.Fatalf("ReadLastList: %v", err)
	}
	if len(got.UUIDs) != 3 || got.UUIDs[0] != "u1" || got.UUIDs[2] != "u3" {
		t.Errorf("cached list = %v", got.UUIDs)
	}
	if got.Command != "things today" {
		t.Errorf("cached command = %q", got.Command)
	}
	if got.Stale(time.Now()) {
		t.Error("a listing just written reads back as stale")
	}
}

// seedCache writes a last-list cache aged by age, so a test can put a numeric
// reference either side of cache.MaxAge.
func seedCache(t *testing.T, age time.Duration, command string, uuids ...string) {
	t.Helper()
	entry := cache.LastList{WrittenAt: time.Now().Add(-age), Command: command, UUIDs: uuids}
	if err := cache.WriteLastList(entry); err != nil {
		t.Fatalf("seed cache: %v", err)
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

	seedCache(t, time.Minute, "things today", "abc-123", "other")
	database := seedResolveTaskDB(t)

	got, err := resolveTask(&Deps{}, "1", database)
	if err != nil {
		t.Fatalf("resolveTask: %v", err)
	}
	if got.UUID != "abc-123" || got.Title != "Cached task" {
		t.Errorf("got %+v", got)
	}
}

func TestResolveTaskStaleCacheIndex(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	seedCache(t, time.Minute, "things today", "missing-uuid")
	database := seedResolveTaskDB(t)

	_, err := resolveTask(&Deps{}, "1", database)
	if err == nil {
		t.Fatal("expected stale cache error")
	}
}

// A numeric ref against a listing older than cache.MaxAge is refused rather
// than resolved: the UUID is still there, so acting on it would silently hit
// whatever has drifted into that row (issue #265).
func TestResolveTaskNumericRefusedWhenCacheIsOld(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	seedCache(t, 3*24*time.Hour, `things today --project "Work"`, "abc-123", "other")
	database := seedResolveTaskDB(t)

	_, err := resolveTask(&Deps{}, "1", database)
	var stale *staleCacheError
	if !errors.As(err, &stale) {
		t.Fatalf("resolveTask = %v, want a stale cache error", err)
	}
	msg := err.Error()
	for _, want := range []string{"3 days ago", `things today --project "Work"`, "uuid"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q", msg, want)
		}
	}
}

// The bound is a bound: a listing from within it still resolves.
func TestResolveTaskNumericInsideTheBound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	seedCache(t, cache.MaxAge-time.Minute, "things today", "abc-123")
	database := seedResolveTaskDB(t)

	got, err := resolveTask(&Deps{}, "1", database)
	if err != nil {
		t.Fatalf("resolveTask: %v", err)
	}
	if got.UUID != "abc-123" {
		t.Errorf("got %+v", got)
	}
}

// A cache file left by a things-cli older than 0.8.0 carries no timestamp, so
// there is nothing to age it by and no listing to name. It is refused with the
// same shape of message.
func TestResolveTaskNumericRefusedWhenCacheHasNoTimestamp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Library", "Caches", "things-cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "last-list"), []byte("abc-123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	database := seedResolveTaskDB(t)

	_, err := resolveTask(&Deps{}, "1", database)
	var stale *staleCacheError
	if !errors.As(err, &stale) {
		t.Fatalf("resolveTask = %v, want a stale cache error", err)
	}
	if !strings.Contains(err.Error(), "older things-cli") {
		t.Errorf("message = %q", err.Error())
	}
}

// A number past the end of the cache was never a row, so it keeps falling
// through to the title lookup whatever the cache's age.
func TestResolveTaskNumericPastEndOfOldCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	seedCache(t, 3*24*time.Hour, "things today", "abc-123")
	database := seedResolveTaskDB(t)

	_, err := resolveTask(&Deps{}, "9", database)
	var stale *staleCacheError
	if errors.As(err, &stale) {
		t.Fatalf("row 9 of a 1-row cache should not be refused as stale: %v", err)
	}
	if err == nil {
		t.Fatal("expected a not-found error for a title of \"9\"")
	}
}

func TestListCommandLine(t *testing.T) {
	cases := []struct {
		name    string
		cmd     ListCmd
		view    string
		project string
		want    string
	}{
		{"bare view", ListCmd{}, "today", "", "things today"},
		{"filter only", ListCmd{}, "project", "Some Project", `things --project "Some Project"`},
		{"view and filter", ListCmd{Area: "Home"}, "today", "", "things today --area Home"},
		{"tag", ListCmd{Tag: "errand"}, "anytime", "", "things anytime --tag errand"},
		{"dates", ListCmd{From: "2026-09-01", To: "2026-09-30"}, "upcoming", "", "things upcoming --from 2026-09-01 --to 2026-09-30"},
		{"include completed", ListCmd{IncludeCompleted: true}, "today", "", "things today --include-completed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cmd.commandLine(tc.view, tc.project); got != tc.want {
				t.Errorf("commandLine = %q, want %q", got, tc.want)
			}
		})
	}
}
