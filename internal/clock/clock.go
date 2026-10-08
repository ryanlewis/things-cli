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

// DayStart is the first instant of t's local day, in t's location.
//
// Midnight is not always there once. Where daylight saving begins at midnight,
// as in America/Santiago, it is skipped and time.Date gives 23:00 the day
// before, so the day starts at the transition instead. Where it ends at 01:00
// and the clocks go back to 00:00, as in Asia/Amman in 2021, midnight comes
// twice and time.Date may give the second, so the day starts at the first.
//
// For the start of the next day, pass a time well inside it, such as noon: a
// date one day on from 00:30 can land on a skipped hour, which time.Date
// normalises back into the day before.
func DayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	loc := t.Location()
	start := time.Date(y, m, d, 0, 0, 0, 0, loc)
	if start.Day() != d {
		_, start = start.ZoneBounds()
		return start
	}
	zoneStart, _ := start.ZoneBounds()
	if zoneStart.IsZero() {
		return start
	}
	_, offset := start.Zone()
	_, before := zoneStart.Add(-time.Nanosecond).Zone()
	if before <= offset {
		return start
	}
	first := start.Add(-time.Duration(before-offset) * time.Second)
	if fy, fm, fd := first.Date(); first.Before(zoneStart) && fy == y && fm == m && fd == d &&
		first.Hour() == 0 && first.Minute() == 0 && first.Second() == 0 {
		return first
	}
	return start
}

// NextDayStart is the first instant of the local day after t's.
func NextDayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return DayStart(time.Date(y, m, d+1, 12, 0, 0, 0, t.Location()))
}
