package skill

import (
	"os"
	"path/filepath"
)

func init() { register(piAgent{}) }

type piAgent struct{}

func (piAgent) Name() string { return "pi" }

func (piAgent) DefaultDir() (string, error) {
	return filepath.Join(os.Getenv("HOME"), ".pi", "agent", "skills", "things-cli"), nil
}

func (piAgent) Files() map[string][]byte { return sharedFiles }
