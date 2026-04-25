package things

import (
	"strings"
	"testing"
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
