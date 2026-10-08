package clock

import (
	"testing"
	"time"
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no zone data for %s: %v", name, err)
	}
	return loc
}

// America/Santiago skipped from 00:00 to 01:00 on 6 Sep 2026, so that day
// starts at 01:00, and the day before ends there, including when it is read
// in its own first hour.
func TestDayStartAcrossAMissingMidnight(t *testing.T) {
	loc := zone(t, "America/Santiago")
	gap := time.Date(2026, 9, 6, 1, 0, 0, 0, loc)
	cases := []struct {
		now, start, finish time.Time
	}{
		{time.Date(2026, 9, 6, 12, 0, 0, 0, loc), gap, time.Date(2026, 9, 7, 0, 0, 0, 0, loc)},
		{time.Date(2026, 9, 5, 12, 0, 0, 0, loc), time.Date(2026, 9, 5, 0, 0, 0, 0, loc), gap},
		{time.Date(2026, 9, 5, 0, 30, 0, 0, loc), time.Date(2026, 9, 5, 0, 0, 0, 0, loc), gap},
		{time.Date(2026, 9, 7, 0, 30, 0, 0, loc), time.Date(2026, 9, 7, 0, 0, 0, 0, loc), time.Date(2026, 9, 8, 0, 0, 0, 0, loc)},
	}
	for _, tc := range cases {
		if got := DayStart(tc.now); !got.Equal(tc.start) {
			t.Errorf("DayStart(%s) = %s, want %s", tc.now, got, tc.start)
		}
		if got := NextDayStart(tc.now); !got.Equal(tc.finish) {
			t.Errorf("NextDayStart(%s) = %s, want %s", tc.now, got, tc.finish)
		}
	}
}

// Asia/Amman went back from 01:00 to 00:00 on 29 Oct 2021, so midnight came
// twice: at 00:00 +03 (21:00 UTC) and again at 00:00 +02 (22:00 UTC). The day
// starts at the first.
func TestDayStartAcrossARepeatedMidnight(t *testing.T) {
	loc := zone(t, "Asia/Amman")
	noon := time.Date(2021, 10, 29, 12, 0, 0, 0, loc)
	first := time.Date(2021, 10, 28, 21, 0, 0, 0, time.UTC)
	if got := DayStart(noon); !got.Equal(first) {
		t.Errorf("DayStart(%s) = %s (%s UTC), want %s UTC", noon, got, got.UTC(), first)
	}
	if got := NextDayStart(time.Date(2021, 10, 28, 12, 0, 0, 0, loc)); !got.Equal(first) {
		t.Errorf("NextDayStart(28 Oct) = %s UTC, want %s UTC", got.UTC(), first)
	}
	// On 29 Sep 2000 the same change came at the same hour, and there
	// time.Date gives the second midnight, so DayStart has to step back.
	noon2000 := time.Date(2000, 9, 29, 12, 0, 0, 0, loc)
	if got, want := DayStart(noon2000), time.Date(2000, 9, 28, 21, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("DayStart(%s) = %s UTC, want %s UTC", noon2000, got.UTC(), want)
	}
	// An ordinary day is untouched.
	plain := time.Date(2021, 10, 20, 12, 0, 0, 0, loc)
	if got, want := DayStart(plain), time.Date(2021, 10, 20, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Errorf("DayStart(%s) = %s, want %s", plain, got, want)
	}
}
