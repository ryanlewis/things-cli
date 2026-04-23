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

// Body returns the neutral, agent-independent skill source.
func Body() string { return body }

// Agent renders and locates the skill for a particular AI coding agent.
type Agent interface {
	// Name is the user-facing identifier passed to `things skill install <name>`.
	Name() string
	// DefaultDir returns the default destination directory for this agent.
	DefaultDir() (string, error)
	// Files returns relative filename -> file contents for this agent.
	Files() map[string][]byte
}

var registry = map[string]Agent{}

func register(a Agent) {
	registry[a.Name()] = a
}

// Lookup returns the agent adapter with the given name.
func Lookup(name string) (Agent, error) {
	a, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown agent %q (supported: %s)", name, joinNames(Agents()))
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

func joinNames(agents []Agent) string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name()
	}
	return strings.Join(names, ", ")
}

// Exists reports whether the skill already appears to be installed in dir.
// It returns true if any of the agent's files are present.
func Exists(a Agent, dir string) bool {
	return len(InstalledFiles(a, dir)) > 0
}

// InstalledFiles returns the relative paths of this agent's files that
// currently exist on disk under dir, sorted for stable output.
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

// Install writes the agent's rendered files to dir, creating it if needed.
// Existing files are overwritten.
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
	// Best-effort: remove the directory if it's empty.
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
	return nil
}
