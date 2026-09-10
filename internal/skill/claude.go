package skill

import (
	"os"
	"path/filepath"
)

func init() { register(claudeAgent{}) }

type claudeAgent struct{}

func (claudeAgent) Name() string { return "claude" }

// DefaultDir resolves the skill directory inside Claude Code's config
// directory. Claude Code lets users relocate that directory via
// $CLAUDE_CONFIG_DIR, so honour it when set and fall back to ~/.claude.
func (claudeAgent) DefaultDir() (string, error) {
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		configDir = filepath.Join(os.Getenv("HOME"), ".claude")
	}
	return filepath.Join(configDir, "skills", "things-cli"), nil
}

func (claudeAgent) Files() map[string][]byte { return sharedFiles }
