package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alecthomas/kong"

	"github.com/ryanlewis/things-cli/internal/config"
)

// writeConfig puts a config file in a temp directory and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestConfigKeysMatchFlags is the guard against the config registry drifting
// away from the CLI: every key must name a flag that exists, and carry the
// same built-in default kong does.
func TestConfigKeysMatchFlags(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, parserOptions(nil)...)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}

	defaults := map[string]string{}
	err = kong.Visit(parser.Model, func(node kong.Visitable, next kong.Next) error {
		if flag, ok := node.(*kong.Flag); ok {
			if _, seen := defaults[flag.Name]; !seen {
				defaults[flag.Name] = flag.Default
			}
		}
		return next(nil)
	})
	if err != nil {
		t.Fatalf("visit model: %v", err)
	}

	for _, k := range config.Keys {
		got, ok := defaults[k.Flag]
		if !ok {
			t.Errorf("config key %q names --%s, which is not a flag on the CLI", k.Name, k.Flag)
			continue
		}
		if got == "" {
			// No default tag means the field's zero value applies.
			got = fmt.Sprintf("%v", reflect.Zero(reflect.TypeOf(k.Default)).Interface())
		}
		if want := fmt.Sprintf("%v", k.Default); got != want {
			t.Errorf("config key %q default = %q, but --%s defaults to %q", k.Name, want, k.Flag, got)
		}
	}
}

func TestConfigPathFromArgs(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"today"}, ""},
		{[]string{"--config", "a.toml"}, "a.toml"},
		{[]string{"--config=a.toml"}, "a.toml"},
		{[]string{"add", "buy milk", "--config", "a.toml"}, "a.toml"},
		{[]string{"--config"}, ""},
		{[]string{"--", "--config", "a.toml"}, ""},
	}
	for _, tc := range cases {
		if got := configPathFromArgs(tc.args); got != tc.want {
			t.Errorf("configPathFromArgs(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestConfigSeedsGlobalFlagDefaults(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "json = true\nno_verify = true\ncolor = \"never\"\n")

	var cli CLI
	cfg, err := loadConfig([]string{"--config", path, "today"})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	parser, err := kong.New(&cli, parserOptions(cfg)...)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse([]string{"--config", path, "today"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cli.JSON {
		t.Error("json = true in the config file did not reach --json")
	}
	if !cli.NoVerify {
		t.Error("no_verify = true in the config file did not reach --no-verify")
	}
	if cli.Color != "never" {
		t.Errorf("color = %q, want never", cli.Color)
	}
}

func TestFlagBeatsConfig(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "json = true\ncolor = \"never\"\n")

	var cli CLI
	cfg, err := loadConfig([]string{"--config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	parser, err := kong.New(&cli, parserOptions(cfg)...)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	args := []string{"--config", path, "--json=false", "--color", "always", "today"}
	if _, err := parser.Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cli.JSON {
		t.Error("--json=false did not override json = true from the config file")
	}
	if cli.Color != "always" {
		t.Errorf("color = %q, want always (the flag should beat the config file)", cli.Color)
	}
}

func TestConfigSeedsPerCommandFlag(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "strict_tags = true\n")

	var cli CLI
	cfg, err := loadConfig([]string{"--config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	parser, err := kong.New(&cli, parserOptions(cfg)...)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse([]string{"--config", path, "add", "buy milk"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cli.Add.StrictTags {
		t.Error("strict_tags = true in the config file did not reach add --strict-tags")
	}
}

// A db path in the config that no longer exists must not stop a --db flag
// from overriding it — the file always loses to the command line — and it
// must not be reported until something actually opens the database.
func TestFlagBeatsStaleConfigDB(t *testing.T) {
	isolateHome(t)
	real := filepath.Join(t.TempDir(), "main.sqlite")
	if err := os.WriteFile(real, nil, 0o600); err != nil {
		t.Fatalf("write db: %v", err)
	}
	path := writeConfig(t, `db = "/nonexistent/things.sqlite"`+"\n")

	var cli CLI
	cfg, err := loadConfig([]string{"--config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	parser, err := kong.New(&cli, parserOptions(cfg)...)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse([]string{"--config", path, "--db", real, "today"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cli.DB != real {
		t.Errorf("db = %q, want %q (the flag should beat the config file)", cli.DB, real)
	}
	if _, err := (&Deps{DBPath: cli.DB, Config: cfg}).Database(); err != nil {
		t.Errorf("opening the database named by --db: %v", err)
	}

	// Without the flag, parsing still succeeds — the stale entry only bites
	// when the database is opened, and then it names the file.
	var bare CLI
	parser, err = kong.New(&bare, parserOptions(cfg)...)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse([]string{"--config", path, "today"}); err != nil {
		t.Fatalf("parse with a stale config db must not fail: %v", err)
	}
	_, err = (&Deps{DBPath: bare.DB, Config: cfg}).Database()
	if err == nil {
		t.Fatal("opening a stale config db: want error, got nil")
	}
	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error %q (%T) does not unwrap to *config.Error", err, err)
	}
	if cfgErr.Path != path {
		t.Errorf("cfgErr.Path = %q, want %q", cfgErr.Path, path)
	}
	for _, want := range []string{path, "db:", "/nonexistent/things.sqlite"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

// A path that no config file supplied is reported on its own terms — blaming
// a file that never mentioned it would send the user to the wrong place.
func TestMissingDBWithoutConfigAttribution(t *testing.T) {
	isolateHome(t)
	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	_, err = (&Deps{DBPath: "/nonexistent/things.sqlite", Config: cfg}).Database()
	if err == nil {
		t.Fatal("opening a missing database: want error, got nil")
	}
	if strings.Contains(err.Error(), "config file") {
		t.Errorf("error %q blames a config file that never set this path", err)
	}
	if !strings.Contains(err.Error(), "/nonexistent/things.sqlite") {
		t.Errorf("error %q does not name the path", err)
	}
}

// An empty db in the config means "unset", not a path of its own.
func TestEmptyConfigDBIsUnset(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "db = \"\"\n")

	var cli CLI
	cfg, err := loadConfig([]string{"--config", path})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	parser, err := kong.New(&cli, parserOptions(cfg)...)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse([]string{"--config", path, "today"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cli.DB != "" {
		t.Errorf("db = %q, want empty", cli.DB)
	}
}

func TestConfigUsesEnvVar(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "json = true\n")
	t.Setenv(config.EnvVar, path)

	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Path != path || cfg.Source != config.SourceEnv {
		t.Errorf("loadConfig = (%q, %q), want (%q, %q)", cfg.Path, cfg.Source, path, config.SourceEnv)
	}
}

func TestConfigLoadReportsBadFile(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "nope = 1\n")
	_, err := loadConfig([]string{"--config", path})
	if err == nil {
		t.Fatal("loadConfig: want error, got nil")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("error = %q, want it to name the file and the unknown key", err)
	}
}

func TestConfigPathCmd(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "json = false\n")

	out, err := runOut(t, nil, "--config", path, "config", "path")
	if err != nil {
		t.Fatalf("config path: %v", err)
	}
	if !strings.Contains(out, path) || !strings.Contains(out, "exists") {
		t.Errorf("config path = %q, want the path and its existence", out)
	}

	absent := filepath.Join(t.TempDir(), "absent.toml")
	out, err = runOut(t, nil, "--config", absent, "config", "path")
	if err != nil {
		t.Fatalf("config path (absent): %v", err)
	}
	if !strings.Contains(out, absent) || !strings.Contains(out, "not found") {
		t.Errorf("config path = %q, want the path and \"not found\"", out)
	}
}

func TestConfigPathCmdJSON(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "")

	out, err := runOut(t, nil, "--config", path, "--json", "config", "path")
	if err != nil {
		t.Fatalf("config path --json: %v", err)
	}
	var got struct {
		Path   string `json:"path"`
		Exists bool   `json:"exists"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if got.Path != path || !got.Exists || got.Source != config.SourceFlag {
		t.Errorf("config path --json = %+v, want path %q, exists, source flag", got, path)
	}
}

func TestConfigShowCmd(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "color = \"never\"\n")

	out, err := runOut(t, nil, "--config", path, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	for _, want := range []string{path, "color", "never", "config", "json", "false", "default", "(unset)"} {
		if !strings.Contains(out, want) {
			t.Errorf("config show = %q, missing %q", out, want)
		}
	}
}

func TestConfigShowCmdJSON(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "strict_tags = true\n")

	out, err := runOut(t, nil, "--config", path, "--json", "config", "show")
	if err != nil {
		t.Fatalf("config show --json: %v", err)
	}
	var got struct {
		Path     string           `json:"path"`
		Exists   bool             `json:"exists"`
		Settings []config.Setting `json:"settings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if got.Path != path || !got.Exists {
		t.Errorf("path = %q exists = %v, want %q true", got.Path, got.Exists, path)
	}
	if len(got.Settings) != len(config.Keys) {
		t.Fatalf("got %d settings, want %d", len(got.Settings), len(config.Keys))
	}
	for _, s := range got.Settings {
		if s.Key != "strict_tags" {
			continue
		}
		if s.Value != true || s.Source != "config" {
			t.Errorf("strict_tags = %v (%s), want true (config)", s.Value, s.Source)
		}
	}
}

func TestConfigInitCmd(t *testing.T) {
	isolateHome(t)
	path := filepath.Join(t.TempDir(), "nested", "config.toml")

	out, err := runOut(t, nil, "--config", path, "config", "init")
	if err != nil {
		t.Fatalf("config init: %v", err)
	}
	if !strings.Contains(out, path) {
		t.Errorf("config init = %q, want it to name the file written", out)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if string(body) != config.Template() {
		t.Error("config init did not write the template")
	}
}

func TestConfigInitRefusesToOverwrite(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "json = true\n")

	_, err := runOut(t, nil, "--config", path, "config", "init")
	if err == nil {
		t.Fatal("config init over an existing file: want error, got nil")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %q, want it to name the file", err)
	}
	body, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if string(body) != "json = true\n" {
		t.Errorf("config init overwrote the file: %q", body)
	}
}

func TestConfigInitForceOverwrites(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "json = true\n")

	if _, err := runOut(t, nil, "--config", path, "config", "init", "--force"); err != nil {
		t.Fatalf("config init --force: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(body) != config.Template() {
		t.Error("config init --force did not rewrite the template")
	}
}

func TestConfigInitDefaultPathStaysUnderHome(t *testing.T) {
	home := isolateHome(t)

	if _, err := runOut(t, nil, "config", "init"); err != nil {
		t.Fatalf("config init: %v", err)
	}
	want := filepath.Join(home, ".config", "things-cli", "config.toml")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("config init did not write %s: %v", want, err)
	}
}

// TestConfigTagPolicyExclusivity covers the interaction between the config
// file and the --strict-tags / --create-tags exclusive pair. Kong counts a
// resolved value as set, so a file that turns one on must not make the other
// unusable on the command line.
func TestConfigTagPolicyExclusivity(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		args       []string
		wantStrict bool
		wantCreate bool
	}{
		{"config strict", "strict_tags = true\n", nil, true, false},
		{"config create", "create_tags = true\n", nil, false, true},
		{"flag strict beats config create", "create_tags = true\n", []string{"--strict-tags"}, true, false},
		{"flag create beats config strict", "strict_tags = true\n", []string{"--create-tags"}, false, true},
		{"both false", "strict_tags = false\ncreate_tags = false\n", nil, false, false},
		{"one true one false", "strict_tags = true\ncreate_tags = false\n", nil, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			path := writeConfig(t, tc.body)
			args := append([]string{"--config", path, "add", "buy milk"}, tc.args...)

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
			if got := cli.Add.StrictTags; got != tc.wantStrict {
				t.Errorf("strict-tags = %v, want %v", got, tc.wantStrict)
			}
			if got := cli.Add.CreateTags; got != tc.wantCreate {
				t.Errorf("create-tags = %v, want %v", got, tc.wantCreate)
			}
		})
	}
}

func TestConfigRejectsBothTagPolicies(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "strict_tags = true\ncreate_tags = true\n")
	_, err := loadConfig([]string{"--config", path})
	if err == nil {
		t.Fatal("both tag policies true: want error, got nil")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "cannot both be true") {
		t.Errorf("error = %q, want it to name the file and the conflict", err)
	}
}

// runDiag is runStreams for the diagnostic commands: no database, and the
// returned error is whatever the command reported.
func runDiag(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runStreams(t, nil, args...)
}

// The point of issue #179: a config file the CLI cannot use must not disable
// the commands that exist to tell you so.
func TestDiagnosticsSurviveABrokenConfig(t *testing.T) {
	broken := map[string]struct {
		body string
		want string
	}{
		"stale db":       {`db = "/nonexistent/things.sqlite"` + "\n", "no such file"},
		"unknown key":    {"verbose = true\n", `unknown key "verbose"`},
		"malformed toml": {"color = \"always\n", "invalid TOML"},
		"wrong type":     {`json = "yes"` + "\n", "must be a boolean"},
		"both spellings": {"no_verify = true\nno-verify = false\n", "set twice"},
	}

	for name, tc := range broken {
		t.Run(name, func(t *testing.T) {
			isolateHome(t)
			path := writeConfig(t, tc.body)

			t.Run("config path", func(t *testing.T) {
				stdout, stderr, err := runDiag(t, "--config", path, "config", "path")
				if err != nil {
					t.Fatalf("config path must still report the file: %v", err)
				}
				if !strings.Contains(stdout, path) {
					t.Errorf("stdout %q does not name the file", stdout)
				}
				// A stale db is a usable file; the others are not, and say so.
				if name != "stale db" && !strings.Contains(stderr, tc.want) {
					t.Errorf("stderr %q does not explain the problem (%q)", stderr, tc.want)
				}
			})

			t.Run("config show", func(t *testing.T) {
				stdout, _, err := runDiag(t, "--config", path, "config", "show")
				if !strings.Contains(stdout, path) {
					t.Errorf("stdout %q does not name the file", stdout)
				}
				if name == "stale db" {
					// The file is fine; only the path it points at is gone.
					if err != nil {
						t.Fatalf("config show with a stale db: %v", err)
					}
					if !strings.Contains(stdout, "/nonexistent/things.sqlite") {
						t.Errorf("stdout %q does not show the configured db", stdout)
					}
					return
				}
				if err == nil {
					t.Fatal("config show on an unreadable file: want an error, got nil")
				}
				if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), path) {
					t.Errorf("error %q should name the file and the problem (%q)", err, tc.want)
				}
			})

			t.Run("config init refuses but explains", func(t *testing.T) {
				_, _, err := runDiag(t, "--config", path, "config", "init")
				if err == nil {
					t.Fatal("config init over an existing file: want an error, got nil")
				}
				if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "--force") {
					t.Errorf("error %q should name the file and the way out", err)
				}
				body, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatalf("read config: %v", readErr)
				}
				if string(body) != tc.body {
					t.Errorf("config init changed the file: %q", body)
				}
			})

			t.Run("config init --force repairs", func(t *testing.T) {
				if _, _, err := runDiag(t, "--config", path, "config", "init", "--force"); err != nil {
					t.Fatalf("config init --force: %v", err)
				}
				cfg, err := loadConfig([]string{"--config", path})
				if err != nil {
					t.Fatalf("the repaired file still does not load: %v", err)
				}
				if cfg.Err != nil {
					t.Errorf("repaired file carries an error: %v", cfg.Err)
				}
			})
		})
	}
}

// --help has to survive a broken file too: kong answers it during Parse, so
// the gate that stops other commands must sit after parsing, not before.
func TestBrokenConfigStillParses(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, "verbose = true\n")

	cfg, cfgErr := loadConfig([]string{"--config", path})
	if cfgErr == nil {
		t.Fatal("loadConfig: want an error for an unknown key")
	}
	if cfg == nil || cfg.Path != path {
		t.Fatalf("loadConfig dropped the file it failed on: %+v", cfg)
	}

	var cli CLI
	parser, err := kong.New(&cli, parserOptions(cfg)...)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	ctx, err := parser.Parse([]string{"--config", path, "today"})
	if err != nil {
		t.Fatalf("parsing must not fail on a broken config: %v", err)
	}
	if cli.JSON || cli.Color != "auto" {
		t.Error("a file that failed to load seeded a default anyway")
	}
	if diagnosesConfig(ctx) {
		t.Error("`today` is not a config diagnostic")
	}
}

func TestDiagnosesConfigMarksOnlyTheConfigCommands(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"config", "path"}, true},
		{[]string{"config", "show"}, true},
		{[]string{"config", "init"}, true},
		{[]string{"today"}, false},
		{[]string{"projects"}, false},
		{[]string{"skill", "list"}, false},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			isolateHome(t)
			var cli CLI
			parser, err := kong.New(&cli, parserOptions(nil)...)
			if err != nil {
				t.Fatalf("kong.New: %v", err)
			}
			ctx, err := parser.Parse(tc.args)
			if err != nil {
				t.Fatalf("parse %v: %v", tc.args, err)
			}
			if got := diagnosesConfig(ctx); got != tc.want {
				t.Errorf("diagnosesConfig(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
