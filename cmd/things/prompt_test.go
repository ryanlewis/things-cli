package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPromptLineReadsFromDepsStdin(t *testing.T) {
	var stderr bytes.Buffer
	d := &Deps{Stdin: strings.NewReader("  2 \n"), Stderr: &stderr}

	line, ok := promptLine(d, "Pick [1-3]: ")
	if !ok || line != "2" {
		t.Errorf("promptLine = %q, %v; want %q, true", line, ok, "2")
	}
	if stderr.String() != "Pick [1-3]: " {
		t.Errorf("question went to %q, want it on Deps.Stderr", stderr.String())
	}

	d = &Deps{Stdin: strings.NewReader(""), Stderr: &stderr}
	if line, ok := promptLine(d, "?"); ok || line != "" {
		t.Errorf("promptLine on empty stdin = %q, %v; want empty, false", line, ok)
	}
}

// The cache-write warning has to go through Deps.Stderr, not os.Stderr.
func TestCacheWarningGoesToDepsStderr(t *testing.T) {
	// HOME under /dev/null makes creating the cache directory fail.
	t.Setenv("HOME", "/dev/null")
	var stderr bytes.Buffer
	cacheTaskUUIDs(&Deps{Stderr: &stderr}, "today", nil)
	if !strings.Contains(stderr.String(), "warning: failed to cache task list") {
		t.Errorf("stderr = %q, want the cache warning", stderr.String())
	}
}
