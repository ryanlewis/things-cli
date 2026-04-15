package model

import (
	"testing"
	"time"
)

func TestThingsDateRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		y, m, d int
	}{
		{"typical", 2026, 4, 14},
		{"start of year", 2024, 1, 1},
		{"end of year", 2023, 12, 31},
		{"leap day", 2024, 2, 29},
		{"day 31", 2025, 7, 31},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := time.Date(tc.y, time.Month(tc.m), tc.d, 0, 0, 0, 0, time.Local)
			got := ThingsDateFromTime(in).ToTime()
			if got.Year() != tc.y || int(got.Month()) != tc.m || got.Day() != tc.d {
				t.Fatalf("roundtrip mismatch: in=%s got=%s", in, got)
			}
		})
	}
}

func TestThingsDateString(t *testing.T) {
	d := ThingsDateFromTime(time.Date(2026, 4, 14, 0, 0, 0, 0, time.Local))
	if got, want := d.String(), "2026-04-14"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestThingsDateEncoding(t *testing.T) {
	// Verify the bit layout: year<<16 | month<<12 | day<<7
	d := ThingsDateFromTime(time.Date(2026, 4, 14, 0, 0, 0, 0, time.Local))
	want := ThingsDate(2026<<16 | 4<<12 | 14<<7)
	if d != want {
		t.Fatalf("encoding mismatch: got %d, want %d", d, want)
	}
}

func TestCoreDataRoundTrip(t *testing.T) {
	cases := []time.Time{
		time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), // epoch
		time.Date(2026, 4, 14, 12, 34, 56, 0, time.UTC),
		time.Date(2000, 6, 15, 8, 0, 0, 0, time.UTC), // pre-epoch
	}
	for _, in := range cases {
		ts := TimeToCoreData(in)
		got := CoreDataToTime(ts)
		if !got.Equal(in) {
			t.Fatalf("roundtrip mismatch: in=%s got=%s (ts=%f)", in, got, ts)
		}
	}
}

func TestCoreDataEpochZero(t *testing.T) {
	epoch := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	if ts := TimeToCoreData(epoch); ts != 0 {
		t.Fatalf("epoch should be 0, got %f", ts)
	}
	if got := CoreDataToTime(0); !got.Equal(epoch) {
		t.Fatalf("CoreDataToTime(0) = %s, want %s", got, epoch)
	}
}
