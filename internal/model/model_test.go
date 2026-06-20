package model

import (
	"encoding/json"
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

func TestThingsDateMarshalJSON(t *testing.T) {
	d := ThingsDateFromTime(time.Date(2026, 5, 9, 0, 0, 0, 0, time.Local))
	got, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `"2026-05-09"`; string(got) != want {
		t.Fatalf("Marshal = %s, want %s", got, want)
	}
}

func TestThingsDateUnmarshalJSON(t *testing.T) {
	var d ThingsDate
	if err := json.Unmarshal([]byte(`"2026-05-09"`), &d); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got, want := d.String(), "2026-05-09"; got != want {
		t.Fatalf("Unmarshal -> String = %q, want %q", got, want)
	}
	want := ThingsDateFromTime(time.Date(2026, 5, 9, 0, 0, 0, 0, time.Local))
	if d != want {
		t.Fatalf("Unmarshal value = %d, want %d", d, want)
	}
}

func TestThingsDateUnmarshalInvalid(t *testing.T) {
	cases := []string{
		`"2026/05/09"`,
		`"not a date"`,
		`12345`,
	}
	for _, in := range cases {
		var d ThingsDate
		if err := json.Unmarshal([]byte(in), &d); err == nil {
			t.Errorf("Unmarshal(%s) succeeded, want error", in)
		}
	}
}

func TestThingsDateOmitemptyNil(t *testing.T) {
	type holder struct {
		StartDate *ThingsDate `json:"startDate,omitempty"`
	}
	got, err := json.Marshal(holder{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != `{}` {
		t.Fatalf("Marshal of nil = %s, want {}", got)
	}
}

func TestThingsDateRoundTripJSON(t *testing.T) {
	in := ThingsDateFromTime(time.Date(2024, 2, 29, 0, 0, 0, 0, time.Local))
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out ThingsDate
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if in != out {
		t.Fatalf("round-trip mismatch: in=%d out=%d", in, out)
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

func TestStatusMarshalJSON(t *testing.T) {
	cases := []struct {
		status Status
		want   string
	}{
		{StatusOpen, `"open"`},
		{StatusCancelled, `"cancelled"`},
		{StatusCompleted, `"completed"`},
		{Status(99), `"unknown"`},
	}
	for _, tc := range cases {
		got, err := json.Marshal(tc.status)
		if err != nil {
			t.Fatalf("Marshal(%d): %v", tc.status, err)
		}
		if string(got) != tc.want {
			t.Errorf("Marshal(%d) = %s, want %s", tc.status, got, tc.want)
		}
	}
}

func TestStatusUnmarshalJSON(t *testing.T) {
	cases := []struct {
		in   string
		want Status
	}{
		{`"open"`, StatusOpen},
		{`"cancelled"`, StatusCancelled},
		{`"completed"`, StatusCompleted},
		{`0`, StatusOpen},      // legacy integer input
		{`3`, StatusCompleted}, // legacy integer input
	}
	for _, tc := range cases {
		var s Status
		if err := json.Unmarshal([]byte(tc.in), &s); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.in, err)
		}
		if s != tc.want {
			t.Errorf("Unmarshal(%s) = %d, want %d", tc.in, s, tc.want)
		}
	}
	var s Status
	if err := json.Unmarshal([]byte(`"bogus"`), &s); err == nil {
		t.Error("Unmarshal of unknown string should error")
	}
}

func TestStatusRoundTripJSON(t *testing.T) {
	in := Task{Title: "t", Status: StatusCompleted}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out Task
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Status != StatusCompleted {
		t.Errorf("round-trip status = %d, want %d", out.Status, StatusCompleted)
	}
}
