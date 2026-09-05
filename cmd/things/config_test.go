package main

import (
	"encoding/json"
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
	if !cli.Add.StrictTags.StrictTags {
		t.Error("strict_tags = true in the config file did not reach add --strict-tags")
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
