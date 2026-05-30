package output

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/ryanlewis/things-cli/internal/model"
)

// TestMain forces a deterministic no-color baseline for the output package
// tests. Lipgloss v2 renders full-fidelity ANSI unconditionally; stripping
// happens at write time according to the active color profile (see style.go).
// The default profile is auto-detected from os.Stdout, so without this the
// layout/content assertions would depend on whether the test process is
// attached to a TTY (interactive/PTY runner) or a pipe (CI). Color behavior is
// covered explicitly by TestColorMode_* which set their own mode.
func TestMain(m *testing.M) {
	_ = SetColorMode("never")
	os.Exit(m.Run())
}

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

// In v2, styles always render full-fidelity ANSI; stripping/downsampling happens
// at write time via the color profile. These tests therefore assert on the bytes
// that reach the writer (via Print), not on the raw output of style helpers.

func TestColorMode_Never_StripsANSI(t *testing.T) {
	t.Cleanup(func() { _ = SetColorMode("never") })
	if err := SetColorMode("never"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tasks := []model.Task{{UUID: "u1", Title: "Done", Status: model.StatusCompleted}}
	if err := Print(&buf, tasks, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("expected no ANSI escapes in never mode, got %q", out)
	}
	if !strings.Contains(out, "[x]") {
		t.Errorf("expected glyph [x] in output, got %q", out)
	}
}

func TestColorMode_Always_EmitsANSI(t *testing.T) {
	t.Cleanup(func() { _ = SetColorMode("never") })
	if err := SetColorMode("always"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	// The tag is styled with a pure foreground color (SGR 33), unlike the
	// completed title which carries only faint/strikethrough decoration. Asserting
	// the tag's color sequence proves always-mode emits *color*, not merely text
	// decoration that would survive an accidental downsample to ASCII.
	tasks := []model.Task{{UUID: "u1", Title: "Done", Status: model.StatusCompleted, Tags: []string{"tag"}}}
	if err := Print(&buf, tasks, false); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "\x1b[33m") {
		t.Errorf("expected the tag color (SGR 33) in always mode, got %q", out)
	}
}

func TestColorMode_Auto_StripsWhenNonTTY(t *testing.T) {
	// "auto" detects from os.Stdout. Point stdout at a pipe (a non-TTY) and clear
	// any color-forcing env so detection is deterministic, then confirm auto
	// strips ANSI — the `things ... | cat` / redirect-to-file path. This covers
	// the default real-CLI branch (colorprofile.Detect), which the never/always
	// tests bypass by pinning a static profile.
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("NO_COLOR", "")
	t.Setenv("TTY_FORCE", "")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = orig
		_ = w.Close()
		_ = r.Close()
		_ = SetColorMode("never")
	})

	if err := SetColorMode("auto"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tasks := []model.Task{{UUID: "u1", Title: "Done", Status: model.StatusCompleted}}
	if err := Print(&buf, tasks, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("auto mode on a non-TTY should strip ANSI, got %q", out)
	}
	if !strings.Contains(out, "[x]") {
		t.Errorf("expected glyph [x] in output, got %q", out)
	}
}

func TestStyledDate_Buckets(t *testing.T) {
	prevNow := nowFn
	t.Cleanup(func() { nowFn = prevNow })

	// Pin "now" to 2026-05-03 (Sunday).
	nowFn = func() time.Time {
		return time.Date(2026, 5, 3, 12, 0, 0, 0, time.Local)
	}

	cases := []struct {
		name string
		date *model.ThingsDate
		want lipgloss.Style
	}{
		{"overdue", mustDate(2026, 5, 1), dateOverdueStyle},
		{"today", mustDate(2026, 5, 3), dateTodayStyle},
		{"soon", mustDate(2026, 5, 5), dateSoonStyle},
		{"normal", mustDate(2026, 6, 1), dimStyle},
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
	if got := styledTags(nil); got != "" {
		t.Errorf("empty tags should render empty, got %q", got)
	}
	// In v2 styledTags always wraps the bracketed content in tag styling, so the
	// expected value is that same render. Comparing against tagStyle.Render keeps
	// v1's exact-equality scrutiny (catching a missing/extra bracket or stray
	// content) without depending on the color mode.
	got := styledTags([]string{"a", "b"})
	if want := tagStyle.Render("[a, b]"); got != want {
		t.Errorf("styledTags = %q, want %q", got, want)
	}
}
