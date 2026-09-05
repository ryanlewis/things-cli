package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alecthomas/kong"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
)

// seedProject returns a writable in-memory DB holding one open project, plus
// the raw handle so the exec stub can apply the status change to it.
func seedProject(t *testing.T) (*db.DB, *sql.DB) {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)
	stmts := []string{
		`INSERT INTO TMSettings (uuid, uriSchemeAuthenticationToken) VALUES ('s1', 'tok')`,
		`INSERT INTO TMTask (uuid, title, type, status, trashed)
		 VALUES ('proj-1', 'Chores', 1, 0, 0)`,
	}
	for _, s := range stmts {
		if _, err := sqlDB.Exec(s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	return db.NewFromSQL(sqlDB), sqlDB
}

// The point of issue #158: --json makes the run non-interactive, so a project
// complete could never succeed for a machine caller. --yes is the way through.
func TestProjectStatusChangeUnderJSONNeedsYes(t *testing.T) {
	cases := []struct {
		name   string
		verb   string
		status int
	}{
		{"complete", "complete", 3},
		{"cancel", "cancel", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("declines without --yes", func(t *testing.T) {
				fastVerify(t)
				database, sqlDB := seedProject(t)
				stubExecApplying(t, sqlDB, "proj-1", tc.status)

				err := runWith(t, database, "--json", tc.verb, "Chores")
				if err == nil {
					t.Fatal("want a cancelled error, got nil")
				}
				if !strings.Contains(err.Error(), "--yes") {
					t.Errorf("error %q should point at --yes", err)
				}
			})

			t.Run("proceeds with --yes", func(t *testing.T) {
				fastVerify(t)
				database, sqlDB := seedProject(t)
				stubExecApplying(t, sqlDB, "proj-1", tc.status)

				if err := runWith(t, database, "--json", tc.verb, "Chores", "--yes"); err != nil {
					t.Fatalf("%s --yes under --json: %v", tc.verb, err)
				}
				var got int
				if err := sqlDB.QueryRow(`SELECT status FROM TMTask WHERE uuid = 'proj-1'`).Scan(&got); err != nil {
					t.Fatalf("read back status: %v", err)
				}
				if got != tc.status {
					t.Errorf("status = %d, want %d", got, tc.status)
				}
			})
		})
	}
}

// A terminal run with --yes must not prompt either; stubTTY(true) with no
// stdin to read would otherwise decline.
func TestProjectStatusChangeYesSkipsPromptOnTerminal(t *testing.T) {
	stubTTY(t, true)
	fastVerify(t)
	database, sqlDB := seedProject(t)
	stubExecApplying(t, sqlDB, "proj-1", 3)

	if err := runWith(t, database, "complete", "Chores", "--yes"); err != nil {
		t.Fatalf("complete --yes: %v", err)
	}
}

func TestConfirmProjectStatusChangeAssumeYes(t *testing.T) {
	stubTTY(t, false)
	if err := confirmProjectStatusChange(&Deps{JSON: true}, true, "Complete", "Chores"); err != nil {
		t.Errorf("assumeYes under --json: %v", err)
	}
	if err := confirmProjectStatusChange(&Deps{}, true, "Cancel", "Chores"); err != nil {
		t.Errorf("assumeYes on a pipe: %v", err)
	}
}

// A to-do is not a project, so it never reaches the confirmation at all —
// --yes must be accepted there and change nothing.
func TestYesOnAPlainTaskIsHarmless(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecApplying(t, sqlDB, "one-1", 3)

	if err := runWith(t, database, "complete", "one-1", "--yes"); err != nil {
		t.Fatalf("complete a to-do with --yes: %v", err)
	}
}

func TestConfigAssumeYesSeedsTheFlag(t *testing.T) {
	cases := []struct {
		name string
		body string
		args []string
		want bool
	}{
		{"config on", "assume_yes = true\n", nil, true},
		{"config off", "assume_yes = false\n", nil, false},
		{"unset", "", nil, false},
		{"flag beats config off", "assume_yes = false\n", []string{"--yes"}, true},
		{"flag beats config on", "assume_yes = true\n", []string{"--yes=false"}, false},
		{"yes spelling accepted", "yes = true\n", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			path := writeConfig(t, tc.body)

			for _, verb := range []string{"complete", "cancel"} {
				args := append([]string{"--config", path, verb, "Chores"}, tc.args...)
				var cli CLI
				cfg, err := loadConfig(args)
				if err != nil {
					t.Fatalf("loadConfig: %v", err)
				}
				parser, err := kong.New(&cli, parserOptions(cfg)...)
				if err != nil {
					t.Fatalf("kong.New: %v", err)
				}
				if _, err := parser.Parse(args); err != nil {
					t.Fatalf("parse %v: %v", args, err)
				}
				got := cli.Complete.Yes
				if verb == "cancel" {
					got = cli.Cancel.Yes
				}
				if got != tc.want {
					t.Errorf("%s --yes = %v, want %v", verb, got, tc.want)
				}
			}
		})
	}
}

// The skill commands have a --yes of their own that means "overwrite" and
// "delete". assume_yes is about the project confirmation and must not reach
// them, or a config file written to automate `complete` would also make
// `skill uninstall` delete without asking.
func TestConfigAssumeYesDoesNotReachSkillCommands(t *testing.T) {
	cases := []struct {
		name string
		args []string
		got  func(*CLI) bool
	}{
		{"install", []string{"skill", "install", "claude"}, func(c *CLI) bool { return c.Skill.Install.Yes }},
		{"uninstall", []string{"skill", "uninstall", "claude"}, func(c *CLI) bool { return c.Skill.Uninstall.Yes }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			path := writeConfig(t, "assume_yes = true\n")
			args := append([]string{"--config", path}, tc.args...)
			args = append(args, "--path", filepath.Join(t.TempDir(), "skills"))

			var cli CLI
			cfg, err := loadConfig(args)
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			parser, err := kong.New(&cli, parserOptions(cfg)...)
			if err != nil {
				t.Fatalf("kong.New: %v", err)
			}
			if _, err := parser.Parse(args); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if tc.got(&cli) {
				t.Errorf("assume_yes = true reached skill %s --yes", tc.name)
			}
		})
	}

	// The flag itself still works there, which is what makes the config key's
	// silence the deliberate part.
	t.Run("flag still works", func(t *testing.T) {
		isolateHome(t)
		path := writeConfig(t, "")
		args := []string{"--config", path, "skill", "uninstall", "claude", "--yes"}
		var cli CLI
		cfg, err := loadConfig(args)
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
		parser, err := kong.New(&cli, parserOptions(cfg)...)
		if err != nil {
			t.Fatalf("kong.New: %v", err)
		}
		if _, err := parser.Parse(args); err != nil {
			t.Fatalf("parse: %v", err)
		}
		if !cli.Skill.Uninstall.Yes {
			t.Error("--yes on skill uninstall should still work")
		}
	})
}

// The template has to keep loading cleanly now that it carries assume_yes.
func TestConfigInitTemplateStillLoads(t *testing.T) {
	isolateHome(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := runOut(t, nil, "--config", path, "config", "init"); err != nil {
		t.Fatalf("config init: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("template not written: %v", err)
	}
	if _, err := loadConfig([]string{"--config", path}); err != nil {
		t.Fatalf("loadConfig(template): %v", err)
	}
}
