package cache

import (
	"os"
	"path/filepath"
	"strings"
)

func dir() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "Caches", "things-cli")
}

func WriteLastList(uuids []string) error {
	d := dir()
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	data := strings.Join(uuids, "\n") + "\n"
	return os.WriteFile(filepath.Join(d, "last-list"), []byte(data), 0o644)
}

func ReadLastList() ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir(), "last-list"))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	return lines, nil
}
