package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

// isolate points the default-path lookup at a temp directory so a test never
// reads or writes the developer's real config file.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv(EnvVar, "")
	return home
}

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestDefaultPathUsesHome(t *testing.T) {
	home := isolate(t)
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(home, ".config", "things-cli", "config.toml")
	if got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
}

func TestDefaultPathPrefersXDG(t *testing.T) {
	isolate(t)
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(xdg, "things-cli", "config.toml")
	if got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
}

func TestDefaultPathWithoutHome(t *testing.T) {
	isolate(t)
	t.Setenv("HOME", "")
	if _, err := DefaultPath(); err == nil {
		t.Fatal("DefaultPath with no $HOME: want error, got nil")
	}
}

func TestResolvePathPrecedence(t *testing.T) {
	home := isolate(t)
	envPath := filepath.Join(t.TempDir(), "env.toml")
	flagPath := filepath.Join(t.TempDir(), "flag.toml")

	path, source, err := ResolvePath("")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if want := filepath.Join(home, ".config", "things-cli", "config.toml"); path != want || source != SourceDefault {
		t.Errorf("no override = (%q, %q), want (%q, %q)", path, source, want, SourceDefault)
	}

	t.Setenv(EnvVar, envPath)
	path, source, err = ResolvePath("")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if path != envPath || source != SourceEnv {
		t.Errorf("env = (%q, %q), want (%q, %q)", path, source, envPath, SourceEnv)
	}

	path, source, err = ResolvePath(flagPath)
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if path != flagPath || source != SourceFlag {
		t.Errorf("flag = (%q, %q), want (%q, %q)", path, source, flagPath, SourceFlag)
	}
}

func TestResolvePathMakesRelativeAbsolute(t *testing.T) {
	isolate(t)
	path, _, err := ResolvePath("relative.toml")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("ResolvePath(%q) = %q, want an absolute path", "relative.toml", path)
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.toml")
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Exists {
		t.Error("Exists = true for a file that is not there")
	}
	for _, s := range f.Settings() {
		if s.Source != "default" {
			t.Errorf("%s: source = %q, want default", s.Key, s.Source)
		}
	}
}

func TestLoadReadsEveryKey(t *testing.T) {
	path := write(t, `
json = true
color = "never"
hints = false
no_verify = true
strict_tags = true
`)
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !f.Exists {
		t.Error("Exists = false for a file that is there")
	}
	want := map[string]any{"json": true, "color": "never", "hints": false, "no_verify": true, "strict_tags": true, "db": "", "create_tags": false, "assume_yes": false}
	source := map[string]string{"json": "config", "color": "config", "hints": "config", "no_verify": "config", "strict_tags": "config", "db": "default", "create_tags": "default", "assume_yes": "default"}
	for _, s := range f.Settings() {
		if s.Value != want[s.Key] {
			t.Errorf("%s = %v, want %v", s.Key, s.Value, want[s.Key])
		}
		if s.Source != source[s.Key] {
			t.Errorf("%s: source = %q, want %q", s.Key, s.Source, source[s.Key])
		}
	}
}

func TestLoadAcceptsHyphenatedSpelling(t *testing.T) {
	path := write(t, "no-verify = true\nstrict-tags = true\n")
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, s := range f.Settings() {
		if s.Key == "no_verify" || s.Key == "strict_tags" {
			if s.Value != true {
				t.Errorf("%s = %v, want true", s.Key, s.Value)
			}
		}
	}
}

func TestLoadRejectsBadFiles(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"unknown key", "nope = 1\n", []string{"unknown key", `"nope"`, "json, color, hints, db, no_verify, strict_tags, create_tags, assume_yes"}},
		{"malformed", "json = \n", []string{"invalid TOML", "line 1"}},
		{"wrong type", `json = "yes"` + "\n", []string{`key "json" must be a boolean`, "got string"}},
		{"table value", "[json]\na = 1\n", []string{`key "json" must be a boolean`, "got table"}},
		{"bad enum", `color = "pink"` + "\n", []string{`key "color" must be one of`, "auto, always, never"}},
		{"duplicate spelling", "no_verify = true\nno-verify = false\n", []string{`key "no_verify" is set twice`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := write(t, tc.body)
			_, err := Load(path)
			if err == nil {
				t.Fatal("Load: want error, got nil")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not name the file %q", err, path)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
			if strings.Count(err.Error(), "\n") != 0 {
				t.Errorf("error spans multiple lines: %q", err)
			}
		})
	}
}

// resolveFlag runs a file's resolver for one flag name, the way kong does.
func resolveFlag(t *testing.T, f *File, name string) (any, error) {
	t.Helper()
	return f.Resolver().Resolve(nil, nil, &kong.Flag{Value: &kong.Value{Name: name}})
}

// A db path that has gone missing is not the resolver's problem: failing here
// would take out every command, including the ones that exist to tell the user
// the path is stale. The caller checks it when it opens the database.
func TestResolverPassesMissingDBThrough(t *testing.T) {
	stale := "/nonexistent/things.sqlite"
	f, err := Load(write(t, `db = "`+stale+`"`+"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	v, err := resolveFlag(t, f, "db")
	if err != nil {
		t.Fatalf("resolving db: %v", err)
	}
	if v != stale {
		t.Errorf("db = %v, want %q", v, stale)
	}
}

// SetsDB is what lets the caller blame the file for a path the file supplied.
func TestSetsDB(t *testing.T) {
	stale := "/nonexistent/things.sqlite"
	f, err := Load(write(t, `db = "`+stale+`"`+"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !f.SetsDB(stale) {
		t.Errorf("SetsDB(%q) = false, want true", stale)
	}
	if f.SetsDB("/somewhere/else.sqlite") {
		t.Error("SetsDB reported a path the file never mentioned")
	}

	unset, err := Load(write(t, "json = true\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if unset.SetsDB(stale) {
		t.Error("a file with no db key should not claim a path")
	}
	if (&File{}).SetsDB("") {
		t.Error("an empty db should never be claimed")
	}
	var nilFile *File
	if nilFile.SetsDB(stale) {
		t.Error("a nil File should claim nothing")
	}
}

// A file that would not load supplies nothing at all — otherwise a half-read
// file would seed some defaults and not others.
func TestBrokenFileResolvesNothingButKeepsItsPath(t *testing.T) {
	path := write(t, "json = true\nnope = 1\n")
	f, err := Load(path)
	if err == nil {
		t.Fatal("Load: want error, got nil")
	}
	if f == nil {
		t.Fatal("Load returned no File; the diagnostic commands need its path")
	}
	if f.Path != path || !f.Exists {
		t.Errorf("File = {Path: %q, Exists: %v}, want {%q, true}", f.Path, f.Exists, path)
	}
	if f.Err == nil {
		t.Error("File.Err is nil on a file that failed to load")
	}
	v, resolveErr := resolveFlag(t, f, "json")
	if resolveErr != nil {
		t.Fatalf("resolving json: %v", resolveErr)
	}
	if v != nil {
		t.Errorf("json resolved to %v from a file that failed to load", v)
	}
	for _, s := range f.Settings() {
		if s.Source != "default" {
			t.Errorf("%s came from a file that failed to load", s.Key)
		}
	}
}

// An empty db is how a file says "unset", so it must not reach the flag as a
// path of its own.
func TestResolverSkipsEmptyDB(t *testing.T) {
	f, err := Load(write(t, "db = \"\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	v, err := resolveFlag(t, f, "db")
	if err != nil {
		t.Fatalf("resolving db: %v", err)
	}
	if v != nil {
		t.Errorf("db resolved to %#v, want nil so kong keeps its own default", v)
	}
}

func TestLoadAcceptsExistingDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "main.sqlite")
	if err := os.WriteFile(dbPath, nil, 0o600); err != nil {
		t.Fatalf("write db: %v", err)
	}
	f, err := Load(write(t, "db = "+quote(dbPath)+"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, s := range f.Settings() {
		if s.Key == "db" && s.Value != dbPath {
			t.Errorf("db = %v, want %v", s.Value, dbPath)
		}
	}
}

func quote(s string) string { return `"` + s + `"` }

func TestTemplateCoversEveryKeyAndReloads(t *testing.T) {
	tpl := Template()
	for _, k := range Keys {
		if !strings.Contains(tpl, k.Example) {
			t.Errorf("template is missing the example for %q", k.Name)
		}
	}
	// Everything is commented out, so the template must load as an empty file.
	f, err := Load(write(t, tpl))
	if err != nil {
		t.Fatalf("Load(template): %v", err)
	}
	for _, s := range f.Settings() {
		if s.Source != "default" {
			t.Errorf("%s: template set a value; every line should be commented out", s.Key)
		}
	}
	for _, line := range strings.Split(tpl, "\n") {
		if line != "" && !strings.HasPrefix(line, "# ") && line != "#" {
			t.Errorf("uncommented template line: %q", line)
		}
	}
}

func TestKeyNamesAreUniqueAndSnakeCase(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range Keys {
		spellings := []string{k.Name}
		if k.Flag != k.Name {
			spellings = append(spellings, k.Flag)
		}
		for _, name := range spellings {
			if seen[name] {
				t.Errorf("duplicate config key spelling %q", name)
			}
			seen[name] = true
		}
		if strings.Contains(k.Name, "-") {
			t.Errorf("key %q should be snake_case", k.Name)
		}
	}
}
