package output

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/ryanlewis/things-cli/internal/model"
)

func TestSetColorMode(t *testing.T) {
	t.Cleanup(func() { _ = SetColorMode("never") })

	cases := []struct {
		mode    string
		wantErr bool
	}{
		{"", false},
		{"auto", false},
		{"always", false},
		{"never", false},
		{"sparkle", true},
	}
	for _, tc := range cases {
		err := SetColorMode(tc.mode)
		if tc.wantErr && err == nil {
			t.Errorf("SetColorMode(%q) = nil, want error", tc.mode)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("SetColorMode(%q) = %v, want nil", tc.mode, err)
		}
	}
}

func TestStyledStatus_NeverMode(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	if err := SetColorMode("never"); err != nil {
		t.Fatal(err)
	}
	got := styledStatus(model.StatusCompleted)
	if strings.Contains(got, "\x1b[") {
		t.Errorf("expected no ANSI escapes in never mode, got %q", got)
	}
	if !strings.Contains(got, "[x]") {
		t.Errorf("expected glyph [x] in output, got %q", got)
	}
}

func TestStyledStatus_AlwaysMode(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prev)
	})

	if err := SetColorMode("always"); err != nil {
		t.Fatal(err)
	}
	got := styledStatus(model.StatusCompleted)
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("expected ANSI escapes in always mode, got %q", got)
	}
}

func TestStyledDate_Buckets(t *testing.T) {
	prevNow := nowFn
	prevProfile := lipgloss.ColorProfile()
	t.Cleanup(func() {
		nowFn = prevNow
		lipgloss.SetColorProfile(prevProfile)
	})

	// Pin "now" to 2026-05-03 (Sunday).
	nowFn = func() time.Time {
		return time.Date(2026, 5, 3, 12, 0, 0, 0, time.Local)
	}
	lipgloss.SetColorProfile(termenv.TrueColor)

	cases := []struct {
		name string
		date *model.ThingsDate
		want lipgloss.Style
	}{
		{"overdue", mustDate(2026, 5, 1), dateOverdueStyle},
		{"today", mustDate(2026, 5, 3), dateTodayStyle},
		{"soon", mustDate(2026, 5, 5), dateSoonStyle},
		{"normal", mustDate(2026, 6, 1), dateNormalStyle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := styledDate(tc.date, false)
			want := tc.want.Render(tc.date.String())
			if got != want {
				t.Errorf("styledDate = %q, want %q", got, want)
			}
		})
	}

	// nil date → empty string.
	if got := styledDate(nil, false); got != "" {
		t.Errorf("styledDate(nil) = %q, want empty", got)
	}

	// deadline=true prepends "due:".
	got := styledDate(mustDate(2026, 6, 1), true)
	if !strings.Contains(got, "due:2026-06-01") {
		t.Errorf("expected 'due:' prefix in %q", got)
	}
}

func TestStyledTags(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	_ = SetColorMode("never")

	if got := styledTags(nil); got != "" {
		t.Errorf("empty tags should render empty, got %q", got)
	}
	got := styledTags([]string{"a", "b"})
	if got != "[a, b]" {
		t.Errorf("styledTags = %q, want %q", got, "[a, b]")
	}
}
