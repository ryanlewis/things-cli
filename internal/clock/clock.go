// Package clock is where things-cli reads the current time to decide what day
// it is: the database's "today", the --when checks and relative dates in the
// output. Timeouts and ages read the wall clock directly instead.
package clock

import "time"

var now = time.Now

// Now returns the current time, or the instant Pin fixed.
func Now() time.Time { return now() }

// Pin fixes Now at t and returns a func that undoes it. It is for tests, so
// that fixtures, queries and checks all read one day even when a run crosses
// midnight. Call it before the code under test runs, not alongside it.
func Pin(t time.Time) (restore func()) {
	prev := now
	now = func() time.Time { return t }
	return func() { now = prev }
}
