package output

import (
	"testing"
	"time"
)

// pinLayout makes the rest of the test see stdout as a terminal width columns
// wide (tty) or as a pipe that termWidth reports as width, and puts both back
// when the test ends. Calling it again, say once per pass of a loop, re-pins.
func pinLayout(t *testing.T, width int, tty bool) {
	t.Helper()
	prevWidth, prevTerm := termWidth, stdoutIsTerminal
	t.Cleanup(func() { termWidth, stdoutIsTerminal = prevWidth, prevTerm })
	termWidth = func() int { return width }
	stdoutIsTerminal = func() bool { return tty }
}

// pinClock makes the rest of the test see now as the time and loc as the
// local zone, and puts both back when the test ends. A zero now leaves the
// clock alone and a nil loc leaves the zone alone, for a test that needs only
// one of them.
func pinClock(t *testing.T, now time.Time, loc *time.Location) {
	t.Helper()
	prevNow, prevLocal := nowFn, time.Local
	t.Cleanup(func() { nowFn, time.Local = prevNow, prevLocal })
	if !now.IsZero() {
		nowFn = func() time.Time { return now }
	}
	if loc != nil {
		time.Local = loc
	}
}
