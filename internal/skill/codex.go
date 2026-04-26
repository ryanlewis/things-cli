package skill

import (
	"os"
	"path/filepath"
)

func init() { register(codexAgent{}) }

type codexAgent struct{}

func (codexAgent) Name() string { return "codex" }

func (codexAgent) DefaultDir() (string, error) {
	return filepath.Join(os.Getenv("HOME"), ".codex", "skills", "things-cli"), nil
}

func (codexAgent) Files() map[string][]byte { return sharedFiles }
