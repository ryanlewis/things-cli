package output

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readPage reads a docs page with its whitespace runs folded to one space, so
// a list wrapped across lines matches as one line.
func readPage(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Join(strings.Fields(string(body)), " ")
}

// codeList renders names as the pages write them: `a`, `b`, `c`, with last
// joining the final two ("" for a plain comma).
func codeList(names []string, last string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "`" + n + "`"
	}
	if last == "" || len(quoted) < 2 {
		return strings.Join(quoted, ", ")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " " + last + " " + quoted[len(quoted)-1]
}

// The command reference and the skill spell out the keys --fields takes and
// the default sets, which drift silently from the struct tags and the
// variables above unless something checks them.
func TestDocsListFieldNames(t *testing.T) {
	for _, c := range []struct {
		path, want string
	}{
		{"../../docs/content/commands.md", "A task listing takes the task keys: " + codeList(TaskFields, "") + "."},
		{"../../docs/content/commands.md", "`things projects` takes the project keys: " + codeList(ProjectFields, "") + "."},
		{"../../docs/content/commands.md", "a task row carries these keys: " + codeList(TaskDefaultFields, "") + "."},
		{"../../docs/content/commands.md", "A project row from `things projects` carries these keys: " + codeList(ProjectDefaultFields, "") + "."},
		{"../../internal/skill/SKILL.md", "Task rows carry " + codeList(TaskDefaultFields, "and") + ";"},
		{"../../internal/skill/SKILL.md", "`things projects` rows carry " + codeList(ProjectDefaultFields, "and") + " "},
	} {
		if !strings.Contains(readPage(t, c.path), c.want) {
			t.Errorf("%s does not say %q", c.path, c.want)
		}
	}
}
