package things

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeWhen(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"  ", ""},
		{"today", "today"},
		{"Today", "today"},
		{"TOMORROW", "tomorrow"},
		{"evening", "evening"},
		{"anytime", "anytime"},
		{"someday", "someday"},
		{"2026-05-01", "2026-05-01"},
		{"2026-05-01@09:30", "2026-05-01@09:30"},
		{"9:30PM", "9:30PM"},
		{"21:30", "21:30"},
		{"next friday", "next friday"},
		{"friday", "friday"},
		{"monday", "monday"}, // dist 2 from "today" — must not be flagged as a typo
		{"Monday", "Monday"}, // case preserved for NL pass-through
		{"tuesday", "tuesday"},
		{"sunday", "sunday"},
		{"mon", "mon"},
		{"tonight", "tonight"},
		{"noon", "noon"},
		{"2026-03-10T14:30:00Z", "2026-03-10@14:30"},
		{"2026-03-10T14:30:00+02:00", "2026-03-10@14:30"},
	}
	for _, c := range cases {
		got, err := NormalizeWhen(c.in)
		if err != nil {
			t.Errorf("NormalizeWhen(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeWhen(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeWhenRejectsTypos(t *testing.T) {
	for _, in := range []string{"tommorrow", "tomorow", "evning", "anytim", "Somday"} {
		_, err := NormalizeWhen(in)
		if err == nil {
			t.Errorf("NormalizeWhen(%q) expected error, got nil", in)
			continue
		}
		if !strings.Contains(err.Error(), "did you mean") {
			t.Errorf("NormalizeWhen(%q) error = %v, want 'did you mean'", in, err)
		}
	}
}

func TestNormalizeDeadline(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"  ", ""},
		{"2026-05-01", "2026-05-01"},
		{"2026-03-10T14:30:00Z", "2026-03-10"},
		{"next friday", "next friday"},
	}
	for _, c := range cases {
		got, err := NormalizeDeadline(c.in)
		if err != nil {
			t.Errorf("NormalizeDeadline(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeDeadline(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseListDate(t *testing.T) {
	cases := []struct {
		in       string
		wantYear int
		wantMon  time.Month
		wantDay  int
	}{
		{"2026-05-09", 2026, time.May, 9},
		{"  2026-05-09  ", 2026, time.May, 9},
		{"2026-03-10T14:30:00Z", 2026, time.March, 10},
		{"2026-03-10T14:30:00+02:00", 2026, time.March, 10},
	}
	for _, c := range cases {
		got, err := ParseListDate("on", c.in)
		if err != nil {
			t.Errorf("ParseListDate(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got.Year() != c.wantYear || got.Month() != c.wantMon || got.Day() != c.wantDay {
			t.Errorf("ParseListDate(%q) = %v, want %d-%02d-%02d", c.in, got, c.wantYear, c.wantMon, c.wantDay)
		}
		// Always midnight local time.
		if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 {
			t.Errorf("ParseListDate(%q): expected midnight, got %v", c.in, got)
		}
	}
}

func TestParseListDateRejects(t *testing.T) {
	cases := []struct {
		in       string
		contains string
	}{
		{"", "is empty"},
		{"   ", "is empty"},
		{"today", "invalid date"},
		{"tomorrow", "invalid date"},
		{"next friday", "invalid date"},
		{"2026/05/09", "invalid date"},
		{"05-09-2026", "invalid date"},
		{"not-a-date", "invalid date"},
	}
	for _, c := range cases {
		_, err := ParseListDate("from", c.in)
		if err == nil {
			t.Errorf("ParseListDate(%q): expected error", c.in)
			continue
		}
		if !strings.Contains(err.Error(), c.contains) {
			t.Errorf("ParseListDate(%q): error = %v, want substring %q", c.in, err, c.contains)
		}
		if !strings.Contains(err.Error(), "--from") {
			t.Errorf("ParseListDate(%q): error %v should mention --from", c.in, err)
		}
	}
}

func TestNormalizeDeadlineRejects(t *testing.T) {
	cases := []struct {
		in       string
		contains string
	}{
		{"today", "does not accept keywords"},
		{"tomorrow", "does not accept keywords"},
	}
	for _, c := range cases {
		_, err := NormalizeDeadline(c.in)
		if err == nil {
			t.Errorf("NormalizeDeadline(%q) expected error, got nil", c.in)
			continue
		}
		if !strings.Contains(err.Error(), c.contains) {
			t.Errorf("NormalizeDeadline(%q) error = %v, want substring %q", c.in, err, c.contains)
		}
	}
}

// A value shaped like a date or a time of day that names none is refused,
// so add and edit give the same error before anything is sent. Phrases and
// valid dates and times still pass.
func TestNormalizeWhenRejectsImpossibleDatesAndTimes(t *testing.T) {
	for _, in := range []string{"2026-13-01", "2026-00-10", "2026-02-30", "2026-04-31", "2026-13-01@09:00", "2026-05-01@25:00", "2026-05-01@09:60", "25:00", "9:75", "13:30pm", "0:30am"} {
		if _, err := NormalizeWhen(in); err == nil || !strings.Contains(err.Error(), "invalid --when value") {
			t.Errorf("NormalizeWhen(%q) = %v, want it refused", in, err)
		}
	}
	for _, in := range []string{"2028-02-29", "2026-5-1", "2026-05-01@23:59", "00:00", "12:30pm", "9:30 PM", "blorp", "in 3 days", "next friday"} {
		if _, err := NormalizeWhen(in); err != nil {
			t.Errorf("NormalizeWhen(%q) = %v, want it to pass", in, err)
		}
	}
}
