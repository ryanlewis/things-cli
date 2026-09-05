package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTagDeps(t *testing.T) (*Deps, *bytes.Buffer) {
	t.Helper()
	var stderr bytes.Buffer
	return &Deps{DB: seedFullDB(t), Stdout: io.Discard, Stderr: &stderr}, &stderr
}

func TestVerifyTagsAllKnown(t *testing.T) {
	deps, stderr := newTagDeps(t)
	// seedFullDB creates the tag "urgent".
	if err := verifyTags(deps, TagFlags{}, []string{"urgent"}); err != nil {
		t.Fatalf("verifyTags: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected no warning, got %q", stderr.String())
	}
}

func TestVerifyTagsWarnsOnUnknown(t *testing.T) {
	deps, stderr := newTagDeps(t)
	if err := verifyTags(deps, TagFlags{}, []string{"urgent", "cifas-auto-reject"}); err != nil {
		t.Fatalf("verifyTags: %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "cifas-auto-reject") {
		t.Errorf("warning does not name the unknown tag: %q", out)
	}
	if !strings.Contains(out, "do not exist in Things") {
		t.Errorf("warning does not explain the problem: %q", out)
	}
	if strings.Contains(out, "urgent") {
		t.Errorf("warning names a tag that does exist: %q", out)
	}
}

func TestVerifyTagsStrictFails(t *testing.T) {
	deps, stderr := newTagDeps(t)
	err := verifyTags(deps, TagFlags{StrictTags: true}, []string{"cifas-auto-reject"})
	if err == nil {
		t.Fatal("expected an error under --strict-tags")
	}
	if !strings.Contains(err.Error(), "cifas-auto-reject") {
		t.Errorf("error does not name the unknown tag: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("strict mode should not also warn: %q", stderr.String())
	}
}

func TestVerifyTagsNoNamesSkipsDatabase(t *testing.T) {
	// No DB handle and an unopenable path: verifyTags must not touch the
	// database when there are no tags to check, so `things add` keeps working
	// without a readable Things database.
	var stderr bytes.Buffer
	deps := &Deps{DBPath: filepath.Join(t.TempDir(), "missing.sqlite"), Stdout: io.Discard, Stderr: &stderr}
	if err := verifyTags(deps, TagFlags{StrictTags: true}, nil); err != nil {
		t.Fatalf("verifyTags with no tags: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected no output, got %q", stderr.String())
	}
}

func TestVerifyTagsDatabaseUnavailable(t *testing.T) {
	newDeps := func() (*Deps, *bytes.Buffer) {
		var stderr bytes.Buffer
		path := filepath.Join(t.TempDir(), "missing.sqlite")
		if err := os.WriteFile(path, []byte("not a database"), 0o600); err != nil {
			t.Fatalf("write stub db: %v", err)
		}
		return &Deps{DBPath: path, Stdout: io.Discard, Stderr: &stderr}, &stderr
	}

	// Default: the check is skipped with a warning and the write proceeds.
	deps, stderr := newDeps()
	if err := verifyTags(deps, TagFlags{}, []string{"anything"}); err != nil {
		t.Fatalf("verifyTags: %v", err)
	}
	if !strings.Contains(stderr.String(), "could not check tags") {
		t.Errorf("expected a skip warning, got %q", stderr.String())
	}

	// Strict: an unverifiable tag is a failure, not a warning.
	deps, _ = newDeps()
	err := verifyTags(deps, TagFlags{StrictTags: true}, []string{"anything"})
	if err == nil || !strings.Contains(err.Error(), "--strict-tags") {
		t.Fatalf("expected a strict failure, got %v", err)
	}
}

func TestVerifyTagStrings(t *testing.T) {
	deps, stderr := newTagDeps(t)
	tags := "urgent, nope"
	addTags := "also-nope"
	if err := verifyTagStrings(deps, TagFlags{}, &tags, nil, &addTags); err != nil {
		t.Fatalf("verifyTagStrings: %v", err)
	}
	out := stderr.String()
	for _, want := range []string{"nope", "also-nope"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning missing %q: %q", want, out)
		}
	}
}

func TestImportTags(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    []string
	}{
		{"none", `[{"type":"to-do","attributes":{"title":"x"}}]`, nil},
		{"array", `[{"type":"to-do","attributes":{"title":"x","tags":["work","urgent"]}}]`, []string{"work", "urgent"}},
		{"commaString", `[{"type":"to-do","attributes":{"title":"x","tags":"work, urgent"}}]`, []string{"work", "urgent"}},
		{
			"addTags",
			`[{"type":"to-do","operation":"update","id":"u1","attributes":{"add-tags":["work"]}}]`,
			[]string{"work"},
		},
		{
			// An array entry is one tag name, commas included.
			"arrayEntryKeepsComma",
			`[{"type":"to-do","attributes":{"title":"x","tags":["a, b"]}}]`,
			[]string{"a, b"},
		},
		{
			"nestedItems",
			`[{"type":"project","attributes":{"title":"p","tags":["outer"],
			  "items":[{"type":"to-do","attributes":{"title":"t","tags":["inner"]}}]}}]`,
			[]string{"outer", "inner"},
		},
		{"invalidJSON", `not json`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := importTags([]byte(c.payload))
			if len(got) != len(c.want) {
				t.Fatalf("importTags = %v, want %v", got, c.want)
			}
			// Map order makes the walk order of sibling keys unstable, so
			// compare as a set.
			seen := map[string]bool{}
			for _, g := range got {
				seen[g] = true
			}
			for _, w := range c.want {
				if !seen[w] {
					t.Errorf("importTags = %v, missing %q", got, w)
				}
			}
		})
	}
}

func TestRunAddWarnsOnUnknownTag(t *testing.T) {
	database := seedFullDB(t)
	captured := stubExec(t)

	stderr, err := runCapturingStderr(t, database, "add", "Buy milk", "--tags", "urgent,nope")
	if err != nil {
		t.Fatalf("run add: %v", err)
	}
	if !strings.Contains(stderr, "nope") {
		t.Errorf("expected a warning naming the unknown tag, got %q", stderr)
	}
	if len(*captured) == 0 {
		t.Error("expected the add to still be written")
	}
}

func TestRunAddStrictTagsRefusesToWrite(t *testing.T) {
	database := seedFullDB(t)
	captured := stubExec(t)

	stderr, err := runCapturingStderr(t, database, "add", "Buy milk", "--tags", "urgent,nope", "--strict-tags")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected a strict-tags failure, got %v", err)
	}
	if len(*captured) != 0 {
		t.Errorf("nothing should have been written, got %v", *captured)
	}
	if stderr != "" {
		t.Errorf("expected no warning alongside the error, got %q", stderr)
	}
}

func TestRunAddKnownTagIsSilent(t *testing.T) {
	database := seedFullDB(t)
	stubExec(t)

	stderr, err := runCapturingStderr(t, database, "add", "Buy milk", "--tags", "urgent", "--strict-tags")
	if err != nil {
		t.Fatalf("run add: %v", err)
	}
	if stderr != "" {
		t.Errorf("expected no warning for a known tag, got %q", stderr)
	}
}

func TestRunEditStrictTagsRefusesToWrite(t *testing.T) {
	database := seedFullDB(t)
	captured := stubExec(t)

	_, err := runCapturingStderr(t, database, "edit", "task-1", "--add-tags", "nope", "--strict-tags")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected a strict-tags failure, got %v", err)
	}
	if len(*captured) != 0 {
		t.Errorf("nothing should have been written, got %v", *captured)
	}
}

func TestRunProjectAddStrictTagsRefusesToWrite(t *testing.T) {
	database := seedFullDB(t)
	captured := stubExec(t)

	_, err := runCapturingStderr(t, database, "project", "add", "New project", "--tags", "nope", "--strict-tags")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected a strict-tags failure, got %v", err)
	}
	if len(*captured) != 0 {
		t.Errorf("nothing should have been written, got %v", *captured)
	}
}

func TestRunProjectEditWarnsOnUnknownTag(t *testing.T) {
	database := seedFullDB(t)
	stubExec(t)

	stderr, err := runCapturingStderr(t, database, "project", "edit", "Chores", "--tags", "nope")
	if err != nil {
		t.Fatalf("run project edit: %v", err)
	}
	if !strings.Contains(stderr, "nope") {
		t.Errorf("expected a warning naming the unknown tag, got %q", stderr)
	}
}

func TestRunImportStrictTagsRefusesToWrite(t *testing.T) {
	database := seedFullDB(t)
	captured := stubExec(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "payload.json")
	payload := `[{"type":"to-do","attributes":{"title":"Hi","tags":["nope"]}}]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	_, err := runCapturingStderr(t, database, "import", "--file", path, "--strict-tags")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected a strict-tags failure, got %v", err)
	}
	if len(*captured) != 0 {
		t.Errorf("nothing should have been imported, got %v", *captured)
	}
}

func TestRunImportWarnsOnUnknownTag(t *testing.T) {
	database := seedFullDB(t)
	captured := stubExec(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "payload.json")
	payload := `[{"type":"to-do","attributes":{"title":"Hi","tags":["urgent","nope"]}}]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	stderr, err := runCapturingStderr(t, database, "import", "--file", path)
	if err != nil {
		t.Fatalf("run import: %v", err)
	}
	if !strings.Contains(stderr, "nope") {
		t.Errorf("expected a warning naming the unknown tag, got %q", stderr)
	}
	if len(*captured) == 0 {
		t.Error("expected the import to still be dispatched")
	}
}

// The strict-tags error suggests a `things tag add` command. `tag add` takes
// names as separate positional arguments and does not split on commas, so a
// comma-joined suggestion would create a tag literally named "focus,".
func TestTagAddHint(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"one", []string{"focus"}, "things tag add focus"},
		{"many", []string{"focus", "cifas-auto-reject"}, "things tag add focus cifas-auto-reject"},
		{"spaces", []string{"deep work", "focus"}, `things tag add 'deep work' focus`},
		{"quote", []string{`ev"il`}, `things tag add 'ev"il'`},
		// Metacharacters bash would act on: & splits the command, $ and `
		// expand inside double quotes. Single quotes suppress all of it.
		{"ampersand", []string{"R&D"}, `things tag add 'R&D'`},
		{"dollar", []string{"a$HOME"}, `things tag add 'a$HOME'`},
		{"apostrophe", []string{"Ryan's"}, `things tag add 'Ryan'\''s'`},
		{"empty", []string{""}, `things tag add ''`},
		// Quoting stops the shell splitting a leading-dash name, but the CLI
		// would still read it as a flag, so the hint has to end flag parsing.
		{"leading dash", []string{"-p"}, "things tag add -- -p"},
		{"dash later", []string{"focus", "--json"}, "things tag add -- focus --json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tagAddHint(c.in); got != c.want {
				t.Errorf("tagAddHint(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestStrictTagsErrorSuggestsAUsableCommand(t *testing.T) {
	deps, _ := newTagDeps(t)
	err := verifyTags(deps, TagFlags{StrictTags: true}, []string{"cifas-auto-reject", "deep work"})
	if err == nil {
		t.Fatal("expected an error under --strict-tags")
	}
	if !strings.Contains(err.Error(), "`things tag add cifas-auto-reject 'deep work'`") {
		t.Errorf("error does not suggest a usable command: %v", err)
	}
}
