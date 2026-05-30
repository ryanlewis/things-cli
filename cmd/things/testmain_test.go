package main

import (
	"os"
	"testing"

	"github.com/ryanlewis/things-cli/internal/output"
)

// TestMain pins a deterministic no-color baseline for the cmd/things tests.
// These tests drive the styled output path via ctx.Run without going through
// main() (which is what calls output.SetColorMode), so without this the
// package-global color profile would be whatever colorprofile.Detect(os.Stdout)
// returned at import time — TTY-dependent and non-deterministic across runners.
func TestMain(m *testing.M) {
	_ = output.SetColorMode("never")
	os.Exit(m.Run())
}
