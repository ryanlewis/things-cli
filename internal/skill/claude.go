package skill

import (
	"os"
	"path/filepath"
)

func init() { register(claudeAgent{}) }

const claudeFrontmatter = `---
name: things-cli
description: Use when the user mentions Things3, tasks, todos, inbox, today, upcoming, projects, areas, or to-do lists on macOS. Provides the ` + "`things`" + ` CLI for listing, creating, editing, completing, and searching tasks.
---

`

var claudeFiles = map[string][]byte{
	"SKILL.md": []byte(claudeFrontmatter + body),
}

type claudeAgent struct{}

func (claudeAgent) Name() string { return "claude" }

func (claudeAgent) DefaultDir() (string, error) {
	return filepath.Join(os.Getenv("HOME"), ".claude", "skills", "things-cli"), nil
}

func (claudeAgent) Files() map[string][]byte { return claudeFiles }
