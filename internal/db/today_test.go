package db

import (
	"slices"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/clock"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
)

// The queries read today from the clock once, so at the last millisecond of
// a month a row scheduled for that day has arrived in Today and one scheduled
// for the next has not, and one closed that evening is held there while one
// closed at the next midnight is not. Read from SQLite's 'now' part by part,
// the same moment could have taken the year and month before midnight and the
// day after it. The zone is eight hours behind UTC, where it is already the
// next day, so a query that read UTC would fail too.
func TestTodayReadsOneDayFromTheClock(t *testing.T) {
	zone := time.FixedZone("UTC-8", -8*60*60)
	last := time.Date(2030, 1, 31, 23, 59, 59, int(999*time.Millisecond), zone)
	t.Cleanup(clock.Pin(last))
	d := newTestDB(t)
	fx := dbtest.NewFixture(t, d.db)

	day := int64(model.ThingsDateFromTime(last))
	midnight := time.Date(2030, 2, 1, 0, 0, 0, 0, zone)
	next := int64(model.ThingsDateFromTime(midnight))
	fx.Todo("open", "Open", 0, anytimeOn(day), todayIndexRef(day), todayIndex(1))
	fx.Todo("closed-evening", "Closed this evening", 1, anytimeOn(day), todayIndexRef(day), todayIndex(2),
		completed(model.TimeToUnix(last.Add(-time.Hour))))
	fx.Todo("closed-midnight", "Closed at midnight", 2, anytimeOn(day), todayIndexRef(day), todayIndex(3),
		completed(model.TimeToUnix(midnight)))
	fx.Todo("arrived", "Scheduled for today", 3, somedayOn(day), todayIndexRef(day), todayIndex(4))
	fx.Todo("not-yet", "Scheduled for tomorrow", 4, somedayOn(next), todayIndexRef(day), todayIndex(5))

	got, err := d.ListTasks(ViewToday, TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"open", "closed-evening", "arrived"}; !slices.Equal(uuidsOf(got), want) {
		t.Errorf("today at %s = %v, want %v", last, uuidsOf(got), want)
	}
}

// A query that names today_clock without going through withToday fails
// rather than reading some other day.
func TestTodayClockMustBeSupplied(t *testing.T) {
	d := newTestDB(t)
	if _, err := d.db.Query(`SELECT day FROM ` + todayClock); err == nil {
		t.Error("a bare query of today_clock succeeded, want no such table")
	}
}

// Where daylight saving begins at midnight, today starts at the transition,
// not at 23:00 the day before: America/Santiago skipped from 00:00 to 01:00 on
// 6 Sep 2026.
func TestDayStartAcrossAMissingMidnight(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("no zone data: %v", err)
	}
	cases := []struct {
		now, want time.Time
	}{
		{time.Date(2026, 9, 6, 12, 0, 0, 0, loc), time.Date(2026, 9, 6, 1, 0, 0, 0, loc)},
		{time.Date(2026, 9, 5, 12, 0, 0, 0, loc), time.Date(2026, 9, 5, 0, 0, 0, 0, loc)},
		{time.Date(2026, 9, 7, 12, 0, 0, 0, loc), time.Date(2026, 9, 7, 0, 0, 0, 0, loc)},
	}
	for _, tc := range cases {
		if got := dayStart(tc.now); !got.Equal(tc.want) {
			t.Errorf("dayStart(%s) = %s, want %s", tc.now, got, tc.want)
		}
	}
	// The day before ends where the missing midnight's day begins.
	if got, want := dayStart(time.Date(2026, 9, 5, 12, 0, 0, 0, loc).AddDate(0, 0, 1)), dayStart(time.Date(2026, 9, 6, 12, 0, 0, 0, loc)); !got.Equal(want) {
		t.Errorf("finish of 5 Sep = %s, want %s", got, want)
	}
}
