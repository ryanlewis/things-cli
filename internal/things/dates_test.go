package things

import (
	"errors"
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
		{"9:30PM", "21:30"},
		{"6pm", "18:00"},
		{"6 PM", "18:00"},
		{"6:30pm", "18:30"},
		{"12am", "00:00"},
		{"12:30am", "00:30"},
		{"12pm", "12:00"},
		{"1am", "01:00"},
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
	for _, in := range []string{"2026-13-01", "2026-00-10", "2026-02-30", "2026-04-31", "2026-13-01@09:00", "2026-05-01@25:00", "2026-05-01@09:60", "25:00", "9:75", "13:30pm", "0:30am", "2026-10-09T25:00:00Z", "2026-10-09T10:00:00+200", "2026-10-09T25:00", "tomorrow@25:00", "today@18:60", "Evening@13pm", "friday@25:00", "Monday@noon", "today@noon", "2026-10-09@soon", "someday@18:00", "anytime@9am", "today@6  pm", "today@9:30  pm", "9:30  pm"} {
		if _, err := NormalizeWhen(in); err == nil || !strings.Contains(err.Error(), "invalid --when value") {
			t.Errorf("NormalizeWhen(%q) = %v, want it refused", in, err)
		}
	}
	for _, in := range []string{"2026-10-09T10:00", "2026-10-09T10:00:00", "tomorrow@18:00", "evening@6pm", "2026-10-09@9pm", "next friday@noon", "2028-02-29", "2026-5-1", "2026-05-01@23:59", "00:00", "12:30pm", "9:30 PM", "today@6 pm", "blorp", "in 3 days", "next friday"} {
		if _, err := NormalizeWhen(in); err != nil {
			t.Errorf("NormalizeWhen(%q) = %v, want it to pass", in, err)
		}
	}
}

// An impossible value carries why, so a caller can word its own message.
func TestNormalizeWhenImpossibleReason(t *testing.T) {
	for in, want := range map[string]WhenReason{
		"2026-02-30":       WhenBadDate,
		"2026-02-30@18:00": WhenBadDate,
		"2026-10-09T25:00": WhenBadDateTime,
		"25:00":            WhenBadTime,
		"someday@18:00":    WhenTimeIgnored,
		"today@noon":       WhenBadClock,
		"today@6  pm":      WhenBadClock,
	} {
		_, err := NormalizeWhen(in)
		var impossible *ImpossibleWhenError
		if !errors.As(err, &impossible) || impossible.Reason != want {
			t.Errorf("NormalizeWhen(%q) = %v, want an ImpossibleWhenError with reason %d", in, err, want)
		}
	}
}

// Every date and time form NormalizeWhen passes is told apart from a phrase,
// a single-digit hour and an am/pm time included.
func TestWhenDateOrTime(t *testing.T) {
	for _, in := range []string{"2026-05-01", "2026-5-1", "2026-05-01@09:30", "2026-05-01@9:30", "09:30", "9:30", "9:30pm", "9:30 PM", "2026-05-01@9:30pm", "2026-10-09@9pm", "tomorrow@18:00", "evening@6pm", "2026-10-09T10:00"} {
		v, err := NormalizeWhen(in)
		if err != nil || !WhenDateOrTime(v) {
			t.Errorf("WhenDateOrTime(%q) = false (err %v), want true", in, err)
		}
	}
	for _, in := range []string{"friday", "blorp", "in 3 days", "next friday", "2026-05-01 tonight", "today"} {
		v, _ := NormalizeWhen(in)
		if WhenDateOrTime(v) {
			t.Errorf("WhenDateOrTime(%q) = true, want false", in)
		}
	}
}

// A date and time with no offset is read as the wall-clock time it names,
// as an RFC3339 timestamp's offset is ignored.
func TestNormalizeWhenLocalDateTime(t *testing.T) {
	for in, want := range map[string]string{"2026-10-09T10:00": "2026-10-09@10:00", "2026-10-09T10:00:30": "2026-10-09@10:00"} {
		if got, err := NormalizeWhen(in); err != nil || got != want {
			t.Errorf("NormalizeWhen(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// KnownWhenWord covers the keywords and the weekday names, not phrases.
func TestKnownWhenWord(t *testing.T) {
	for _, in := range []string{"today", "Someday", "friday", "Mon"} {
		if !KnownWhenWord(in) {
			t.Errorf("KnownWhenWord(%q) = false, want true", in)
		}
	}
	for _, in := range []string{"next friday", "blorp", "tonight"} {
		if KnownWhenWord(in) {
			t.Errorf("KnownWhenWord(%q) = true, want false", in)
		}
	}
}

// A date, today, tomorrow or a weekday name with a time after the @ is sent
// as YYYY-MM-DD@HH:MM, and a weekday name alone as YYYY-MM-DD, read at the
// instant it is sent. evening with a time is sent as evening@HH:MM. Anything
// else goes as typed.
func TestResolveWhen(t *testing.T) {
	now := time.Date(2026, 10, 10, 14, 0, 0, 0, time.Local)
	beforeMidnight := time.Date(2026, 10, 10, 23, 59, 59, 0, time.Local)
	cases := []struct {
		in   string
		now  time.Time
		want string
	}{
		{"today@6pm", now, "2026-10-10@18:00"},
		{"Today@18:00", now, "2026-10-10@18:00"},
		{" TOMORROW@9:30 PM ", now, "2026-10-11@21:30"},
		{"tomorrow@12am", now, "2026-10-11@00:00"},
		{"today@9:05", now, "2026-10-10@09:05"},
		// tomorrow is the day after the one it was sent on, even when
		// Things reads it after midnight.
		{"tomorrow@6pm", beforeMidnight, "2026-10-11@18:00"},
		{"today@6pm", beforeMidnight, "2026-10-10@18:00"},
		{"2026-10-12@9pm", now, "2026-10-12@21:00"},
		{"2026-1-5@6:30am", now, "2026-01-05@06:30"},
		{"2026-10-12@09:30", now, "2026-10-12@09:30"},
		{"2026-10-09T10:00:00+05:00", now, "2026-10-09@10:00"},
		// evening keeps its word, so the item stays in the evening.
		{"evening@6pm", now, "evening@18:00"},
		{"Evening@9am", now, "evening@09:00"},
		// A weekday is the next such day strictly after today (a Saturday),
		// whether or not the time has passed.
		{"friday@9pm", now, "2026-10-16@21:00"},
		{"friday", now, "2026-10-16"},
		{"Monday@9pm", now, "2026-10-12@21:00"},
		{"sunday@9am", now, "2026-10-11@09:00"},
		{"saturday", now, "2026-10-17"},
		{"saturday@9am", now, "2026-10-17@09:00"},
		{"saturday@9pm", now, "2026-10-17@21:00"},
		{"sunday", beforeMidnight, "2026-10-11"},
		// Not resolved.
		{"fri@9pm", now, "fri@9pm"},
		{"fri", now, "fri"},
		{"friday@25:00", now, "friday@25:00"},
		{"next friday@noon", now, "next friday@noon"},
		{"today", now, "today"},
		{"6pm", now, "6pm"},
		{"2026-10-12", now, "2026-10-12"},
		{"today@25:00", now, "today@25:00"},
		{"someday@18:00", now, "someday@18:00"},
		{"", now, ""},
	}
	for _, tc := range cases {
		if got := ResolveWhen(tc.in, tc.now); got != tc.want {
			t.Errorf("ResolveWhen(%q, %s) = %q, want %q", tc.in, tc.now.Format(time.Kitchen), got, tc.want)
		}
	}
}
