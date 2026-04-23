package skill

import (
	"fmt"
	"os"
	"path/filepath"
)

func init() { register(claudeAgent{}) }

type claudeAgent struct{}

func (claudeAgent) Name() string { return "claude" }

func (claudeAgent) DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills", "things-cli"), nil
}

func (claudeAgent) Files() map[string][]byte {
	return map[string][]byte{"SKILL.md": []byte(claudeSkillMD())}
}

// claudeSkillMD prepends Claude Code's required YAML frontmatter to the
// neutral skill body.
func claudeSkillMD() string {
	const frontmatter = `---
name: things-cli
description: Use when the user mentions Things3, tasks, todos, inbox, today, upcoming, projects, areas, or to-do lists on macOS. Provides the ` + "`things`" + ` CLI for reading the local Things3 SQLite database and writing via the things:/// URL scheme.
---

`
	return fmt.Sprintf("%s%s", frontmatter, body)
}
