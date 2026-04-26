package skill

import (
	"os"
	"path/filepath"
)

func init() { register(claudeAgent{}) }

type claudeAgent struct{}

func (claudeAgent) Name() string { return "claude" }

func (claudeAgent) DefaultDir() (string, error) {
	return filepath.Join(os.Getenv("HOME"), ".claude", "skills", "things-cli"), nil
}

func (claudeAgent) Files() map[string][]byte { return sharedFiles }
