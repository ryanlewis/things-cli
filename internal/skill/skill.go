// Package skill bundles the things-cli agent skill and manages its
// installation into supported AI coding agents (e.g. Claude Code).
//
// The skill body is authored in a neutral Markdown source (body.md),
// embedded into the binary. Each Agent adapter renders that source into
// on-disk files appropriate for its target (e.g. Claude Code's SKILL.md with
// YAML frontmatter).
package skill

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed body.md
var body string

// Skill identity shared across agents. Individual agents may override these
// when their target requires it, but today every agent uses the defaults.
const (
	Name        = "things-cli"
	Description = "Use when the user mentions Things3, tasks, todos, inbox, today, upcoming, projects, areas, or to-do lists on macOS. Provides the `things` CLI for listing, creating, editing, completing, and searching tasks."
)

// Body returns the neutral skill source body (no frontmatter).
func Body() string { return body }

// SkillMD returns a self-contained SKILL.md: shared frontmatter + body.
func SkillMD() string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", Name, Description, body)
}

// sharedFiles is the SKILL.md payload every registered agent installs today.
// Agents that need extra files should compose this with their own additions.
var sharedFiles = map[string][]byte{
	"SKILL.md": []byte(SkillMD()),
}

// Agent renders and locates the skill for a particular AI coding agent.
type Agent interface {
	Name() string
	DefaultDir() (string, error)
	Files() map[string][]byte
}

var registry = map[string]Agent{}

func register(a Agent) { registry[a.Name()] = a }

// Lookup returns the agent adapter with the given name.
func Lookup(name string) (Agent, error) {
	a, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown agent %q (supported: %s)", name, AgentNames())
	}
	return a, nil
}

// Agents returns the registered agents, sorted by name.
func Agents() []Agent {
	out := make([]Agent, 0, len(registry))
	for _, a := range registry {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// AgentNames returns a comma-separated list of registered agent names.
func AgentNames() string {
	agents := Agents()
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name()
	}
	return strings.Join(names, ", ")
}

// Exists reports whether any of the agent's files are present in dir.
func Exists(a Agent, dir string) bool {
	for name := range a.Files() {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// InstalledFiles returns the agent's files present on disk under dir,
// sorted for stable output.
func InstalledFiles(a Agent, dir string) []string {
	var found []string
	for name := range a.Files() {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			found = append(found, name)
		}
	}
	sort.Strings(found)
	return found
}

// Install writes the agent's rendered files to dir, creating it if needed,
// and overwrites any existing files.
func Install(a Agent, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, content := range a.Files() {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall removes the agent's installed files from dir. If the directory is
// empty afterwards, it is also removed.
func Uninstall(a Agent, dir string) error {
	for name := range a.Files() {
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
	return nil
}
