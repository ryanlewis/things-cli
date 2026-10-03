package output

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ryanlewis/things-cli/internal/model"
)

// TestMain forces a deterministic no-color baseline for the output package
// tests. Lipgloss v2 renders full-fidelity ANSI unconditionally; stripping
// happens at write time according to the active color profile (see style.go).
// The default profile is auto-detected from os.Stdout, so without this the
// layout/content assertions would depend on whether the test process is
// attached to a TTY (interactive/PTY runner) or a pipe (CI). Color behavior is
// covered explicitly by TestColorMode_* which set their own mode.
//
// The terminal check is pinned for the same reason: left to read the real
// stdout, a run attached to a narrow terminal fits and wraps output the
// assertions expect whole. Every test sees what CI sees, a pipe with
// termWidth's 120-column fallback; a test that needs a terminal pins its own.
func TestMain(m *testing.M) {
	_ = SetColorMode("never")
	stdoutIsTerminal = func() bool { return false }
	termWidth = func() int { return 120 }
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
	if err := PrintTaskList(&buf, tasks, false, ""); err != nil {
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
	if err := PrintTaskList(&buf, tasks, false, ""); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "\x1b[33m") {
		t.Errorf("expected the tag color (SGR 33) in always mode, got %q", out)
	}
}

func TestColorMode_Always_TaskDetail(t *testing.T) {
	t.Cleanup(func() { _ = SetColorMode("never") })
	if err := SetColorMode("always"); err != nil {
		t.Fatal(err)
	}
	// The detail view styles tags with a pure foreground color (SGR 33); assert
	// it survives the always-mode (TrueColor) writer on the PrintTaskWithChecklist
	// path, which is distinct from the printTasks path.
	task := &model.Task{UUID: "u1", Title: "T1", Tags: []string{"tag"}}
	var buf bytes.Buffer
	if err := PrintTaskWithChecklist(&buf, task, nil, false); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "\x1b[33m") {
		t.Errorf("expected the tag color (SGR 33) in always-mode detail, got %q", out)
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
	if err := PrintTaskList(&buf, tasks, false, ""); err != nil {
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
	// Pin "now" to 2026-05-03 (Sunday).
	pinClock(t, time.Date(2026, 5, 3, 12, 0, 0, 0, time.Local), nil)

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

// A date three calendar days out is "soon" even when the clocks go back in
// between, which puts its midnight 73 hours away.
func TestStyledDate_SoonAcrossClocksBack(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skipf("no tz data: %v", err)
	}
	pinClock(t, time.Date(2026, 10, 23, 12, 0, 0, 0, london), london)

	d := mustDate(2026, 10, 26)
	if got, want := styledDate(d, false), dateSoonStyle.Render(d.String()); got != want {
		t.Errorf("styledDate = %q, want %q", got, want)
	}
}

// The compact form reads relative to today, keeps the deadline's "due:" and
// takes the colour of the full form.
func TestStyledCompactDate(t *testing.T) {
	// Pin "now" to 2026-05-03 (Sunday).
	pinClock(t, time.Date(2026, 5, 3, 12, 0, 0, 0, time.Local), nil)

	cases := []struct {
		date *model.ThingsDate
		want string
	}{
		{mustDate(2026, 5, 3), "today"},
		{mustDate(2026, 5, 4), "tomorrow"},
		{mustDate(2026, 5, 2), "yesterday"},
		{mustDate(2026, 5, 5), "Tue"},
		{mustDate(2026, 5, 9), "Sat"},
		{mustDate(2026, 5, 10), "10 May"},
		{mustDate(2026, 4, 30), "3d ago"},
		{mustDate(2026, 4, 27), "6d ago"},
		{mustDate(2026, 4, 26), "26 Apr"},
		{mustDate(2027, 1, 2), "2 Jan 27"},
		{mustDate(2025, 12, 31), "31 Dec 25"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			full := styledDate(tc.date, false)
			got := styledCompactDate(tc.date, false)
			if !strings.Contains(got, tc.want) || lipgloss.Width(got) != len(tc.want) {
				t.Errorf("styledCompactDate = %q, want %q", got, tc.want)
			}
			// Same style as the full form: the escapes around the text match.
			if strings.Replace(full, tc.date.String(), tc.want, 1) != got {
				t.Errorf("compact %q is not styled like full %q", got, full)
			}
		})
	}

	if got := styledCompactDate(nil, true); got != "" {
		t.Errorf("styledCompactDate(nil) = %q, want empty", got)
	}
	if got := styledCompactDate(mustDate(2026, 5, 8), true); !strings.Contains(got, "due:Fri") {
		t.Errorf("expected 'due:Fri' in %q", got)
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

// The compact form counts calendar days in local time, the same way
// styledDate buckets them: a minute before midnight tomorrow is still
// "tomorrow", and a daylight-saving change, which makes two midnights 47 or
// 49 hours apart, does not shift a day.
func TestStyledCompactDate_Boundaries(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skipf("no tz data: %v", err)
	}
	pinClock(t, time.Time{}, london)

	cases := []struct {
		name string
		now  time.Time
		date *model.ThingsDate
		want string
	}{
		{"just before midnight, today", time.Date(2026, 5, 3, 23, 59, 0, 0, london), mustDate(2026, 5, 3), "today"},
		{"just before midnight, tomorrow", time.Date(2026, 5, 3, 23, 59, 0, 0, london), mustDate(2026, 5, 4), "tomorrow"},
		{"just after midnight, yesterday", time.Date(2026, 5, 4, 0, 1, 0, 0, london), mustDate(2026, 5, 3), "yesterday"},
		{"clocks forward", time.Date(2026, 3, 28, 12, 0, 0, 0, london), mustDate(2026, 3, 30), "Mon"},
		{"clocks back", time.Date(2026, 10, 24, 12, 0, 0, 0, london), mustDate(2026, 10, 26), "Mon"},
		{"clocks back, past", time.Date(2026, 10, 27, 12, 0, 0, 0, london), mustDate(2026, 10, 24), "3d ago"},
		{"new year", time.Date(2026, 12, 31, 23, 0, 0, 0, london), mustDate(2027, 1, 1), "tomorrow"},
		{"new year, a week out", time.Date(2026, 12, 31, 12, 0, 0, 0, london), mustDate(2027, 1, 8), "8 Jan 27"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pinClock(t, tc.now, nil)
			if got := styledCompactDate(tc.date, false); !strings.Contains(got, tc.want) {
				t.Errorf("styledCompactDate = %q, want %q", got, tc.want)
			}
		})
	}
}

// The short form of a tag list keeps the first tag, cut to a fixed width, and
// counts the rest. A lone tag is only cut, and one short enough already, or
// none, has no short form.
func TestStyledCompactTags(t *testing.T) {
	cases := []struct {
		tags []string
		want string
	}{
		{nil, ""},
		{[]string{"errands"}, ""},
		{[]string{"exactly-fifteen"}, ""},
		{[]string{"waiting-on-post-office"}, "[waiting-on-pos…]"},
		{[]string{"write", "ops"}, "[write +1]"},
		{[]string{"waiting-on-post-office", "errands", "needs-photo-booth"}, "[waiting-on-pos… +2]"},
		{[]string{"exactly-fifteen", "x"}, "[exactly-fifteen +1]"},
	}
	for _, tc := range cases {
		got := styledCompactTags(tc.tags)
		if tc.want == "" {
			if got != "" {
				t.Errorf("styledCompactTags(%q) = %q, want empty", tc.tags, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) || lipgloss.Width(got) != lipgloss.Width(tc.want) {
			t.Errorf("styledCompactTags(%q) = %q, want %q", tc.tags, got, tc.want)
		}
		if tagStyle.Render(tc.want) != got {
			t.Errorf("styledCompactTags(%q) = %q, not styled like styledTags", tc.tags, got)
		}
	}
}

// The first tag is cut by display width, never mid-character: a tag of wide
// runes or emoji stops at a whole character within 15 columns, and the style
// wraps the cut text whole, so none is left open.
func TestStyledCompactTags_WideRunes(t *testing.T) {
	if err := SetColorMode("always"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetColorMode("never") })

	for _, first := range []string{
		"東京出張の準備と予約確認",                // 2 columns each: 7 fit beside the ellipsis
		"🏷️🏷️🏷️🏷️🏷️🏷️🏷️🏷️🏷️🏷️",        // emoji with a variation selector
		"👩‍💻👩‍💻👩‍💻👩‍💻👩‍💻👩‍💻👩‍💻👩‍💻👩‍💻", // ZWJ sequences, one grapheme each
		"abc東京出張の準備と予約",               // an odd column left over before a wide rune
	} {
		got := styledCompactTags([]string{first, "b"})
		plain := ansi.Strip(got)
		if !utf8.ValidString(got) {
			t.Errorf("%q: invalid UTF-8 in %q", first, got)
		}
		cut := strings.TrimSuffix(strings.TrimPrefix(plain, "["), " +1]")
		if w := lipgloss.Width(cut); w > compactTagWidth {
			t.Errorf("%q: first tag %q is %d wide, over %d", first, cut, w, compactTagWidth)
		}
		if !strings.HasPrefix(first, strings.TrimSuffix(cut, "…")) {
			t.Errorf("%q: cut %q is not a whole-character prefix", first, cut)
		}
		if got != tagStyle.Render(plain) {
			t.Errorf("%q: %q is not the plain text styled whole", first, got)
		}
		if !strings.HasSuffix(got, "\x1b[m") {
			t.Errorf("%q: %q does not end with a reset", first, got)
		}
	}
}
