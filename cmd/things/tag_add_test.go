package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alecthomas/kong"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/skill"
	"github.com/ryanlewis/things-cli/internal/things"
)

// seedTagDB returns an in-memory DB holding the tag "Work", a to-do to hang
// writes off, and the auth token, plus the raw handle so a test can simulate
// Things creating a tag.
func seedTagDB(t *testing.T) (*db.DB, *sql.DB) {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)
	stmts := []string{
		`INSERT INTO TMSettings (uuid, uriSchemeAuthenticationToken) VALUES ('s1', 'tok')`,
		`INSERT INTO TMTag (uuid, title, "index") VALUES ('tag-1', 'Work', 0)`,
		`INSERT INTO TMTask (uuid, title, type, status, trashed, start)
		 VALUES ('one-1', 'Post letter', 0, 0, 0, 2)`,
	}
	for _, s := range stmts {
		if _, err := sqlDB.Exec(s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	return db.NewFromSQL(sqlDB), sqlDB
}

// stubExecCreatingTags records every exec call. When sqlDB is non-nil it also
// inserts the tag named by a tag-creating AppleScript, as Things would, so the
// read-back finds it.
func stubExecCreatingTags(t *testing.T, sqlDB *sql.DB) *[][]string {
	t.Helper()
	var calls [][]string
	prev := things.SetExecCommandForTest(func(name string, args ...string) *exec.Cmd {
		calls = append(calls, append([]string{name}, args...))
		if sqlDB == nil || len(args) != 2 || args[0] != "-e" {
			return exec.Command("true")
		}
		title := tagNameFromScript(args[1])
		if title == "" {
			return exec.Command("true")
		}
		if _, err := sqlDB.Exec(
			`INSERT INTO TMTag (uuid, title, "index") VALUES (?, ?, ?)`,
			fmt.Sprintf("new-tag-%d", len(calls)), title, len(calls),
		); err != nil {
			t.Errorf("simulating Things tag write: %v", err)
		}
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })
	return &calls
}

// tagNameFromScript pulls the tag name back out of a creation script, undoing
// the escaping appleScriptString applied. Returns "" for any other script.
func tagNameFromScript(script string) string {
	const prefix = `make new tag with properties {name:"`
	i := strings.Index(script, prefix)
	if i < 0 {
		return ""
	}
	rest := script[i+len(prefix):]
	j := strings.Index(rest, `"}`)
	if j < 0 {
		return ""
	}
	return strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(rest[:j])
}

// osascriptTagNames lists the tags the recorded calls asked Things to create.
func osascriptTagNames(calls [][]string) []string {
	var names []string
	for _, c := range calls {
		if len(c) == 3 && c[0] == "osascript" {
			if name := tagNameFromScript(c[2]); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

// writeTempFile writes content to a throwaway file and returns its path.
func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// parseCLI parses args the way main does, returning the parse error instead of
// failing the test — for asserting on flag validation.
func parseCLI(t *testing.T, args ...string) error {
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
	_, err = parser.Parse(args)
	return err
}

func TestTagAddCreatesOnlyMissingTags(t *testing.T) {
	database, sqlDB := seedTagDB(t)
	calls := stubExecCreatingTags(t, sqlDB)

	out, err := runOut(t, database, "tag", "add", "focus", "Work")
	if err != nil {
		t.Fatalf("tag add: %v", err)
	}
	if got := osascriptTagNames(*calls); len(got) != 1 || got[0] != "focus" {
		t.Errorf("created %v, want [focus]", got)
	}
	if !strings.Contains(out, "created: focus") {
		t.Errorf("output does not report the creation: %q", out)
	}
	if !strings.Contains(out, "already exists: Work") {
		t.Errorf("output does not report the skip: %q", out)
	}
}

// Things treats names differing only in case as the same tag, so "work"
// alongside an existing "Work" must not create a near-duplicate.
func TestTagAddSkipsCaseInsensitiveMatch(t *testing.T) {
	database, sqlDB := seedTagDB(t)
	calls := stubExecCreatingTags(t, sqlDB)

	out, err := runOut(t, database, "tag", "add", "work")
	if err != nil {
		t.Fatalf("tag add: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("expected no writes, got %v", *calls)
	}
	if !strings.Contains(out, "already exists: work") {
		t.Errorf("output does not report the skip: %q", out)
	}
}

func TestTagAddDedupesArguments(t *testing.T) {
	database, sqlDB := seedTagDB(t)
	calls := stubExecCreatingTags(t, sqlDB)

	if _, err := runOut(t, database, "tag", "add", "focus", "FOCUS", " focus "); err != nil {
		t.Fatalf("tag add: %v", err)
	}
	if got := osascriptTagNames(*calls); len(got) != 1 || got[0] != "focus" {
		t.Errorf("created %v, want [focus] once", got)
	}
}

func TestTagAddRejectsBlankNames(t *testing.T) {
	database, sqlDB := seedTagDB(t)
	calls := stubExecCreatingTags(t, sqlDB)

	_, err := runOut(t, database, "tag", "add", "  ")
	if err == nil || !strings.Contains(err.Error(), "no tag names") {
		t.Fatalf("expected a no-names error, got %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("expected no writes, got %v", *calls)
	}
}

func TestTagAddJSON(t *testing.T) {
	database, sqlDB := seedTagDB(t)
	stubExecCreatingTags(t, sqlDB)

	out, err := runOut(t, database, "--json", "tag", "add", "focus", "Work")
	if err != nil {
		t.Fatalf("tag add: %v", err)
	}
	var got tagAddResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(got.Created) != 1 || got.Created[0] != "focus" {
		t.Errorf("created = %v, want [focus]", got.Created)
	}
	if len(got.Skipped) != 1 || got.Skipped[0] != "Work" {
		t.Errorf("skipped = %v, want [Work]", got.Skipped)
	}
}

// A creation Things accepted and then dropped must not report success.
func TestTagAddVerifiesCreationLanded(t *testing.T) {
	fastVerify(t)
	database, _ := seedTagDB(t)
	stubExecCreatingTags(t, nil) // records the call, writes nothing

	_, err := runOut(t, database, "tag", "add", "focus")
	if err == nil || !strings.Contains(err.Error(), "tag creation did not apply") {
		t.Fatalf("expected a verification failure, got %v", err)
	}
}

func TestTagAddNoVerifySkipsReadBack(t *testing.T) {
	fastVerify(t)
	database, _ := seedTagDB(t)
	stubExecCreatingTags(t, nil)

	if _, err := runOut(t, database, "--no-verify", "tag", "add", "focus"); err != nil {
		t.Fatalf("tag add --no-verify: %v", err)
	}
}

func TestTagAddReportsCreationFailure(t *testing.T) {
	database, _ := seedTagDB(t)
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		return exec.Command("false")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })

	_, err := runOut(t, database, "tag", "add", "focus")
	if err == nil || !strings.Contains(err.Error(), `creating tag "focus"`) {
		t.Fatalf("expected a creation failure naming the tag, got %v", err)
	}
}

// --create-tags creates each missing tag over AppleScript before the URL
// scheme write, so Things has the tag by the time it applies it.
func TestCreateTagsRunsBeforeTheWrite(t *testing.T) {
	database, sqlDB := seedTagDB(t)
	calls := stubExecCreatingTags(t, sqlDB)

	stderr, err := runCapturingStderr(t, database, "add", "Buy milk", "--tags", "focus,Work", "--create-tags")
	if err != nil {
		t.Fatalf("add --create-tags: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("expected a create then a write, got %v", *calls)
	}
	if got := osascriptTagNames(*calls); len(got) != 1 || got[0] != "focus" {
		t.Errorf("created %v, want [focus] only", got)
	}
	write := (*calls)[1]
	if write[0] != "open" || !strings.Contains(strings.Join(write, " "), "things:///add") {
		t.Errorf("second call is not the URL-scheme write: %v", write)
	}
	if !strings.Contains(stderr, "created in Things: focus") {
		t.Errorf("stderr does not report the creation: %q", stderr)
	}
	if strings.Contains(stderr, "will be ignored") {
		t.Errorf("stderr still warns about a tag it created: %q", stderr)
	}
}

// Every command that accepts tags accepts --create-tags.
func TestCreateTagsOnEveryTagWrite(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"add", []string{"add", "Buy milk", "--tags", "focus", "--create-tags"}},
		{"projectAdd", []string{"project", "add", "Launch", "--tags", "focus", "--create-tags"}},
		{"edit", []string{"edit", "one-1", "--add-tags", "focus", "--create-tags"}},
		{"projectEdit", []string{"project", "edit", "proj-1", "--add-tags", "focus", "--create-tags"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, sqlDB := seedTagDB(t)
			if _, err := sqlDB.Exec(
				`INSERT INTO TMTask (uuid, title, type, status, trashed) VALUES ('proj-1', 'Chores', 1, 0, 0)`,
			); err != nil {
				t.Fatalf("seed project: %v", err)
			}
			calls := stubExecCreatingTags(t, sqlDB)

			if _, err := runOut(t, database, tc.args...); err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			if got := osascriptTagNames(*calls); len(got) != 1 || got[0] != "focus" {
				t.Errorf("created %v, want [focus]", got)
			}
		})
	}
}

func TestImportCreateTags(t *testing.T) {
	database, sqlDB := seedTagDB(t)
	calls := stubExecCreatingTags(t, sqlDB)

	path := writeTempFile(t, `[{"type":"to-do","attributes":{"title":"x","tags":["focus","Work"]}}]`)
	if _, err := runOut(t, database, "import", "--file", path, "--create-tags"); err != nil {
		t.Fatalf("import --create-tags: %v", err)
	}
	if got := osascriptTagNames(*calls); len(got) != 1 || got[0] != "focus" {
		t.Errorf("created %v, want [focus]", got)
	}
}

// A tag that could not be created aborts the write rather than letting Things
// silently drop it.
func TestCreateTagsFailureBlocksTheWrite(t *testing.T) {
	database, _ := seedTagDB(t)
	var calls int
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		calls++
		return exec.Command("false")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })

	_, err := runOut(t, database, "add", "Buy milk", "--tags", "focus", "--create-tags")
	if err == nil || !strings.Contains(err.Error(), `creating tag "focus"`) {
		t.Fatalf("expected a creation failure, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected the write to be skipped, got %d exec calls", calls)
	}
}

// --create-tags and --strict-tags ask for opposite things, so they are
// rejected together on every command that carries them.
func TestCreateTagsConflictsWithStrictTags(t *testing.T) {
	cases := [][]string{
		{"add", "Buy milk", "--tags", "focus", "--create-tags", "--strict-tags"},
		{"project", "add", "Launch", "--tags", "focus", "--create-tags", "--strict-tags"},
		{"edit", "one-1", "--add-tags", "focus", "--create-tags", "--strict-tags"},
		{"project", "edit", "proj-1", "--add-tags", "focus", "--create-tags", "--strict-tags"},
		{"import", "--create-tags", "--strict-tags"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			err := parseCLI(t, args...)
			if err == nil {
				t.Fatal("expected the two flags to be rejected together")
			}
			if !strings.Contains(err.Error(), "create-tags") || !strings.Contains(err.Error(), "strict-tags") {
				t.Errorf("error does not name both flags: %v", err)
			}
		})
	}
}

// --create-tags cannot fall back to a warning: without the database it cannot
// tell which tags are missing, so it refuses rather than writing a task whose
// tags Things would drop.
func TestCreateTagsRefusesWithoutTheDatabase(t *testing.T) {
	deps, _ := newTagDeps(t)
	deps.DB = nil
	deps.DBPath = writeTempFile(t, "not a database")

	err := verifyTags(deps, TagFlags{CreateTags: true}, []string{"anything"})
	if err == nil || !strings.Contains(err.Error(), "--create-tags") {
		t.Fatalf("expected a --create-tags failure, got %v", err)
	}
}
