package main

import (
	"os"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/clock"
	"github.com/ryanlewis/things-cli/internal/output"
)

// testNow is the one instant these tests call today. Fixtures build "today"
// and "closed today" from it, and TestMain pins the clock at it, so the
// fixtures, the SQL and the --when checks agree on the day even when a run
// crosses midnight. Timeouts, creation windows and the list cache's age still
// read the wall clock.
var testNow = time.Now()

// TestMain pins a deterministic no-color baseline for the cmd/things tests.
// These tests drive the styled output path via ctx.Run without going through
// main() (which is what calls output.SetColorMode), so without this the
// package-global color profile would be whatever colorprofile.Detect(os.Stdout)
// returned at import time — TTY-dependent and non-deterministic across runners.
func TestMain(m *testing.M) {
	_ = output.SetColorMode("never")
	clock.Pin(testNow)
	os.Exit(m.Run())
}
