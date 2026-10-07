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
	"github.com/ryanlewis/things-cli/internal/config"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/skill"
	"github.com/ryanlewis/things-cli/internal/things"
)

// newParser builds the kong parser main does, bound to cli.
func newParser(t *testing.T, cli *CLI) *kong.Kong {
	t.Helper()
	parser, err := kong.New(cli, kong.Name("things"),
		kong.Vars{
			"builtin_lists": strings.Join(things.BuiltinLists, ", "),
			"skill_agents":  skill.AgentNames(),
		},
	)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	return parser
}

func parse(t *testing.T, args ...string) (*CLI, *kong.Context) {
	t.Helper()
	var cli CLI
	ctx, err := newParser(t, &cli).Parse(args)
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
	if cli.List.IncludeCompleted || cli.List.OpenOnly {
		t.Errorf("IncludeCompleted, OpenOnly defaulted to %v, %v, want false", cli.List.IncludeCompleted, cli.List.OpenOnly)
	}

	cli, _ = parse(t, "list", "today", "--open-only")
	if !cli.List.OpenOnly {
		t.Errorf("OpenOnly = %v, want true", cli.List.OpenOnly)
	}

	// The two contradict each other, so kong refuses the pair.
	var both CLI
	if _, err := newParser(t, &both).Parse([]string{"list", "today", "--open-only", "--include-completed"}); err == nil || !strings.Contains(err.Error(), "can't be used together") {
		t.Errorf("expected mutual-exclusion error, got: %v", err)
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
	dbtest.NewFixture(t, sqlDB).Todo("abc-123", "Cached task", 0)
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

	seedCache(t, 3*24*time.Hour, `things today --project 'Work'`, "abc-123", "other")
	database := seedResolveTaskDB(t)

	_, err := resolveTask(&Deps{}, "1", database)
	var stale *staleCacheError
	if !errors.As(err, &stale) {
		t.Fatalf("resolveTask = %v, want a stale cache error", err)
	}
	msg := err.Error()
	for _, want := range []string{"3 days ago", `things today --project 'Work'`, "uuid"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q", msg, want)
		}
	}
}

// Just past the bound the coarse rendering truncates to the bound itself, so
// the message has to say "over 4 hours" rather than claim four hours is older
// than four hours.
func TestResolveTaskNumericJustPastTheBound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	seedCache(t, cache.MaxAge+5*time.Minute, "things today", "abc-123")
	database := seedResolveTaskDB(t)

	_, err := resolveTask(&Deps{}, "1", database)
	var stale *staleCacheError
	if !errors.As(err, &stale) {
		t.Fatalf("resolveTask = %v, want a stale cache error", err)
	}
	if !strings.Contains(err.Error(), "over 4 hours ago") {
		t.Errorf("message = %q, want it to read as over the bound", err.Error())
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

// A number past the end of the cache was never a row, so it is not refused as
// stale whatever the cache's age. It is refused as not a row (issue #375).
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

// seedNumericTitleDB holds a task whose title only contains "12" and one whose
// title is exactly "2026".
func seedNumericTitleDB(t *testing.T) *db.DB {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)
	f := dbtest.NewFixture(t, sqlDB)
	f.Todo("abc-123", "Cached task", 0)
	f.Todo("ch-12", "Chapter 12 notes", 0)
	f.Todo("year-2026", "2026", 0)
	return db.NewFromSQL(sqlDB)
}

// An all-digit ref past the end of the last list is not a row, and must not
// fall through to a substring match: `complete 12` after a 1-row list would
// otherwise complete "Chapter 12 notes" (issue #375).
func TestResolveTaskNumericPastEndIsRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	seedCache(t, time.Minute, "things today", "abc-123")
	database := seedNumericTitleDB(t)

	got, err := resolveTask(&Deps{}, "12", database)
	var nf *notFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("resolveTask = %+v, %v, want a not-found error", got, err)
	}
	msg := err.Error()
	for _, want := range []string{`"12" is not a row`, "1 row", "things today", "uuid"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q", msg, want)
		}
	}
}

// With no listing at all there is no row to be, so the same refusal applies.
func TestResolveTaskNumericWithNoCacheIsRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	database := seedNumericTitleDB(t)

	got, err := resolveTask(&Deps{}, "12", database)
	var nf *notFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("resolveTask = %+v, %v, want a not-found error", got, err)
	}
	if !strings.Contains(err.Error(), "no last list") {
		t.Errorf("message = %q, want it to say there is no list", err.Error())
	}
}

// A title that is exactly the digits still resolves, past the end of the list
// and with no list at all.
func TestResolveTaskNumericExactTitleStillResolves(t *testing.T) {
	t.Run("past the end of the list", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		seedCache(t, time.Minute, "things today", "abc-123")
		got, err := resolveTask(&Deps{}, "2026", seedNumericTitleDB(t))
		if err != nil {
			t.Fatalf("resolveTask: %v", err)
		}
		if got.UUID != "year-2026" {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("no list", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		got, err := resolveTask(&Deps{}, "2026", seedNumericTitleDB(t))
		if err != nil {
			t.Fatalf("resolveTask: %v", err)
		}
		if got.UUID != "year-2026" {
			t.Errorf("got %+v", got)
		}
	})
}

// A row that is in the list is still a row, even when another task is titled
// with that number.
func TestResolveTaskNumericRowBeatsExactTitle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	seedCache(t, time.Minute, "things today", "abc-123", "ch-12", "year-2026")
	got, err := resolveTask(&Deps{}, "2", seedNumericTitleDB(t))
	if err != nil {
		t.Fatalf("resolveTask: %v", err)
	}
	if got.UUID != "ch-12" {
		t.Errorf("got %+v, want row 2", got)
	}
}

// Only an all-digit ref is held to this. A word that happens to be part of a
// title still resolves by substring.
func TestResolveTaskNonNumericSubstringStillResolves(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	got, err := resolveTask(&Deps{}, "Chapter", seedNumericTitleDB(t))
	if err != nil {
		t.Fatalf("resolveTask: %v", err)
	}
	if got.UUID != "ch-12" {
		t.Errorf("got %+v", got)
	}
}

// dbFiles creates two empty files to stand in for two databases. Only their
// paths matter here: the database a listing read is identified by its
// resolved path (issue #274).
func dbFiles(t *testing.T) (live, backup string) {
	t.Helper()
	dir := t.TempDir()
	live = filepath.Join(dir, "live.sqlite")
	backup = filepath.Join(dir, "backup.sqlite")
	for _, p := range []string{live, backup} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return live, backup
}

// A listing of one database does not back a row number used against another:
// `things --db <backup> today` then `things complete 2` is refused inside the
// freshness window too (issue #274).
func TestResolveTaskNumericRefusedFromOtherDatabase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	live, backup := dbFiles(t)
	database := seedResolveTaskDB(t)

	cacheTaskUUIDs(&Deps{DBPath: backup}, "things --db "+backup+" today", []model.Task{{UUID: "abc-123"}})

	_, err := resolveTask(&Deps{DBPath: live}, "1", database)
	var other *otherDBCacheError
	if !errors.As(err, &other) {
		t.Fatalf("resolveTask = %v, want a different-database cache error", err)
	}
	msg := err.Error()
	for _, want := range []string{"different database", "things --db " + backup + " today", "uuid"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q", msg, want)
		}
	}
	if payload := errorPayload(err); payload.Error != "stale list cache" || payload.Query != "1" {
		t.Errorf("JSON payload = %+v", payload)
	}
}

// The same database still resolves, however its path is spelled: through a
// symlink or a hard link, or relative to the working directory.
func TestResolveTaskNumericSameDatabaseResolves(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	live, _ := dbFiles(t)
	link := filepath.Join(t.TempDir(), "link.sqlite")
	if err := os.Symlink(live, link); err != nil {
		t.Fatal(err)
	}
	// A hard link stands in for a second spelling EvalSymlinks does not fold,
	// such as a different case on the default macOS volume.
	hard := filepath.Join(t.TempDir(), "hard.sqlite")
	if err := os.Link(live, hard); err != nil {
		t.Fatal(err)
	}
	database := seedResolveTaskDB(t)

	cacheTaskUUIDs(&Deps{DBPath: live}, "things today", []model.Task{{UUID: "abc-123"}})

	t.Chdir(filepath.Dir(live))
	for _, path := range []string{live, link, "live.sqlite", "./live.sqlite", hard} {
		got, err := resolveTask(&Deps{DBPath: path}, "1", database)
		if err != nil {
			t.Errorf("--db %s: resolveTask: %v", path, err)
			continue
		}
		if got.UUID != "abc-123" {
			t.Errorf("--db %s: got %+v", path, got)
		}
	}
}

// A cache file written before the database was recorded backs a row number
// only when this command uses the auto-discovered database, which is the one
// such a listing almost always read. Against a --db path it is refused.
func TestResolveTaskNumericCacheWithoutDatabase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, backup := dbFiles(t)
	database := seedResolveTaskDB(t)

	seedCache(t, time.Minute, "things today", "abc-123")

	if _, err := resolveTask(&Deps{}, "1", database); err != nil {
		t.Errorf("default database: resolveTask: %v", err)
	}

	_, err := resolveTask(&Deps{DBPath: backup}, "1", database)
	var other *otherDBCacheError
	if !errors.As(err, &other) {
		t.Fatalf("--db: resolveTask = %v, want a different-database cache error", err)
	}
	if !strings.Contains(err.Error(), "uuid") {
		t.Errorf("message = %q", err.Error())
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
		{"filter only", ListCmd{}, "project", "Some Project", `things --project 'Some Project'`},
		{"metacharacter in a name", ListCmd{Area: "R&D"}, "project", "", `things --area 'R&D'`},
		{"non-ASCII and percent stay bare", ListCmd{Area: "café"}, "project", "", `things --area café`},
		{"percent stays bare", ListCmd{Tag: "100%"}, "anytime", "", "things anytime --tag 100%"},
		{"view and filter", ListCmd{Area: "Home"}, "today", "", "things today --area Home"},
		{"tag", ListCmd{Tag: "errand"}, "anytime", "", "things anytime --tag errand"},
		{"dates", ListCmd{From: "2026-09-01", To: "2026-09-30"}, "upcoming", "", "things upcoming --from 2026-09-01 --to 2026-09-30"},
		{"open only", ListCmd{OpenOnly: true}, "today", "", "things today --open-only"},
		// A no-op since closed rows list by default, so the re-run needs no flag.
		{"include completed", ListCmd{IncludeCompleted: true}, "today", "", "things today"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cmd.commandLine(&Deps{}, tc.view, tc.project); got != tc.want {
				t.Errorf("commandLine = %q, want %q", got, tc.want)
			}
		})
	}
}

// A --open-only=false that overrode open_only in the config file has to be
// recorded, or the re-run would read the same file and list fewer rows.
func TestListCommandLineCarriesOpenOnlyOverride(t *testing.T) {
	isolateHome(t)
	cfg, err := config.Load(writeConfig(t, "open_only = true\n"))
	if err != nil {
		t.Fatal(err)
	}
	d := &Deps{Config: cfg}
	if got, want := (&ListCmd{}).commandLine(d, "today", ""), "things today --open-only=false"; got != want {
		t.Errorf("commandLine = %q, want %q", got, want)
	}
	if got, want := (&ListCmd{OpenOnly: true}).commandLine(d, "today", ""), "things today --open-only"; got != want {
		t.Errorf("commandLine = %q, want %q", got, want)
	}
	// logbook and trash ignore open_only, so there is nothing to override.
	if got, want := (&ListCmd{}).commandLine(d, "logbook", ""), "things logbook"; got != want {
		t.Errorf("commandLine = %q, want %q", got, want)
	}
}

// A --db the flag supplied has to survive into the recorded command: re-running
// without it would read the default database and renumber against other rows.
func TestListCommandLineCarriesDBFlag(t *testing.T) {
	d := &Deps{DBPath: "/tmp/things test.sqlite"}
	got := (&ListCmd{}).commandLine(d, "today", "")
	want := `things --db '/tmp/things test.sqlite' today`
	if got != want {
		t.Errorf("commandLine = %q, want %q", got, want)
	}
}

// A --config file can be what set the database, so the recorded command keeps
// it: without it a re-run reads the default config and maybe another database.
func TestListCommandLineCarriesConfigFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "my config.toml")
	if err := os.WriteFile(path, []byte("db = \"/tmp/other.sqlite\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Source = config.SourceFlag

	cases := []struct {
		name   string
		dbPath string
		want   string
	}{
		{"config sets the db", "/tmp/other.sqlite", "things --config '" + path + "' today"},
		{"--db overrides the config", "/tmp/mine.sqlite", "things --db /tmp/mine.sqlite --config '" + path + "' today"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &Deps{DBPath: tc.dbPath, Config: cfg}
			if got := (&ListCmd{}).commandLine(d, "today", ""); got != tc.want {
				t.Errorf("commandLine = %q, want %q", got, tc.want)
			}
		})
	}

	// A config found through the environment or the default location is
	// found again by a re-run, so it is not spelled out.
	envCfg := *cfg
	envCfg.Source = config.SourceEnv
	d := &Deps{DBPath: "/tmp/other.sqlite", Config: &envCfg}
	if got := (&ListCmd{}).commandLine(d, "today", ""); got != "things today" {
		t.Errorf("commandLine = %q, want %q", got, "things today")
	}
}

// A ref marked like a row number, `+N` or `#N`, is not one, even when row N
// exists, and it does not match a title fragment: `complete '#12'` must not
// close a task that mentions issue #12. Only an exact title or a uuid
// resolves it; otherwise the error says to pass the uuid, and to use the
// bare number when the last list has that row (it has 1 here).
func TestResolveTaskMarkedRowRefIsRefused(t *testing.T) {
	for ref, hint := range map[string]bool{"+12": false, "#12": false, " #12": false, "#12 ": false, "#0": false, "+1": true, " #1 ": true} {
		t.Run(ref, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			seedCache(t, time.Minute, "things today", "abc-123")
			sqlDB := dbtest.NewSQL(t)
			f := dbtest.NewFixture(t, sqlDB)
			f.Todo("abc-123", "Cached task", 0)
			f.Todo("issue-12", "Fix issue #12 and +12 more", 0)
			f.Todo("plus-1", "Lift +10 kg", 0)

			got, err := resolveTask(&Deps{}, ref, db.NewFromSQL(sqlDB))
			var nf *notFoundError
			if !errors.As(err, &nf) {
				t.Fatalf("resolveTask(%q) = %+v, %v, want a not-found error", ref, got, err)
			}
			for _, want := range []string{"not a row reference", "uuid"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q does not mention %q", err.Error(), want)
				}
			}
			if got := strings.Contains(err.Error(), "use 1,"); got != hint {
				t.Errorf("message %q: row hint %v, want %v", err.Error(), got, hint)
			}
		})
	}
}

// A task titled exactly `#7` still resolves by that title, and refs that are
// not marked that way, such as `2.`, keep matching title fragments.
func TestResolveTaskMarkedRowRefExactTitle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sqlDB := dbtest.NewSQL(t)
	f := dbtest.NewFixture(t, sqlDB)
	f.Todo("hash-7", "#7", 0)
	f.Todo("ver-2", "Upgrade to 2.4", 0)
	database := db.NewFromSQL(sqlDB)

	for ref, want := range map[string]string{"#7": "hash-7", " #7 ": "hash-7", "2.": "ver-2"} {
		got, err := resolveTask(&Deps{}, ref, database)
		if err != nil || got.UUID != want {
			t.Errorf("resolveTask(%q) = %+v, %v, want %s", ref, got, err, want)
		}
	}
}

// Space around a number does not make it a title fragment: ` 12 ` past the
// end of the list is refused as not a row, and ` 1 ` is row 1.
func TestResolveTaskNumericWithSpace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedCache(t, time.Minute, "things today", "abc-123")
	database := seedNumericTitleDB(t)

	got, err := resolveTask(&Deps{}, " 12 ", database)
	var nf *notFoundError
	if !errors.As(err, &nf) || !strings.Contains(err.Error(), "is not a row") {
		t.Fatalf("resolveTask(\" 12 \") = %+v, %v, want a not-a-row error", got, err)
	}
	got, err = resolveTask(&Deps{}, " 1 ", database)
	if err != nil || got.UUID != "abc-123" {
		t.Errorf("resolveTask(\" 1 \") = %+v, %v, want abc-123", got, err)
	}
}

// Space around a number is not part of a title: ` 2026 ` still finds the
// task titled "2026" when it is not a row.
func TestResolveTaskNumericExactTitleTrimmed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedCache(t, time.Minute, "things today", "abc-123")
	got, err := resolveTask(&Deps{}, " 2026 ", seedNumericTitleDB(t))
	if err != nil || got.UUID != "year-2026" {
		t.Errorf("resolveTask(\" 2026 \") = %+v, %v, want year-2026", got, err)
	}
}
