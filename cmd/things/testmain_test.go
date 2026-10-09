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

// pinWallClock pins the clock to the wall time for the rest of t, for a test
// whose import gives a creation-date of about now. The refusal of a date in
// the future reads the pinned clock, and the read-back window reads the wall
// clock, so the date has to fit both. testNow falls further behind the wall
// clock the longer the package runs, and a slow run (-race in CI) leaves it
// more than the minute's slack behind.
func pinWallClock(t *testing.T) {
	t.Helper()
	t.Cleanup(clock.Pin(time.Now()))
}

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
