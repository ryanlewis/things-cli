package output

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"

	"github.com/ryanlewis/things-cli/internal/model"
)

// colorProfile controls how the ANSI emitted by lipgloss renders is downsampled
// when written out. Lipgloss v2 styles always render full-fidelity ANSI; the
// stripping/downsampling that v1 did inside Render now happens at write time via
// a colorprofile.Writer (see newWriter). It is set by SetColorMode and defaults
// to auto-detection from stdout (honouring NO_COLOR, CLICOLOR_FORCE, etc.).
var colorProfile = detectProfile()

// detectProfile auto-detects the color profile from stdout and the environment
// (honouring NO_COLOR, CLICOLOR_FORCE, etc.). Used for both the package default
// and the "auto" mode so the two can't drift.
func detectProfile() colorprofile.Profile {
	return colorprofile.Detect(os.Stdout, os.Environ())
}

// SetColorMode reconfigures color output based on the user's --color flag.
// "auto" detects from stdout/env, "always" forces TrueColor, "never" strips all
// ANSI.
func SetColorMode(mode string) error {
	switch mode {
	case "", "auto":
		colorProfile = detectProfile()
	case "always":
		colorProfile = colorprofile.TrueColor
	case "never":
		colorProfile = colorprofile.NoTTY
	default:
		return fmt.Errorf("invalid --color mode %q (want auto|always|never)", mode)
	}
	return nil
}

// newWriter wraps w so that the ANSI emitted by lipgloss renders is downsampled
// (or stripped entirely) according to the active color profile on the way out.
func newWriter(w io.Writer) *colorprofile.Writer {
	return &colorprofile.Writer{Forward: w, Profile: colorProfile}
}

// Palette. Styles render full-fidelity ANSI unconditionally; the
// colorprofile.Writer applied in Print strips it for non-TTY / --color=never
// output, so substring-based tests over Print remain valid.
var (
	statusOpenStyle      = lipgloss.NewStyle()
	statusDoneStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Faint(true)
	statusCancelledStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Faint(true)

	titleDimStyle = lipgloss.NewStyle().Faint(true).Strikethrough(true)
	projectStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	areaStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Faint(true)
	tagStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	starStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)

	dateOverdueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	dateTodayStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	dateSoonStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	dimStyle         = lipgloss.NewStyle().Faint(true)

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	labelStyle  = lipgloss.NewStyle().Bold(true)
)

// nowFn is overridable in tests.
var nowFn = time.Now

func styledStatus(status int) string {
	icon := statusIcon(status)
	switch status {
	case model.StatusCompleted:
		return statusDoneStyle.Render(icon)
	case model.StatusCancelled:
		return statusCancelledStyle.Render(icon)
	default:
		return statusOpenStyle.Render(icon)
	}
}

func styledProjectIcon(p model.Project) string {
	icon := projectIcon(p)
	if p.Status == model.StatusCancelled {
		return statusCancelledStyle.Render(icon)
	}
	if p.Status == model.StatusCompleted {
		return statusDoneStyle.Render(icon)
	}
	return projectStyle.Render(icon)
}

// styledDate formats and colours a date based on its proximity to today.
// The "deadline" flag toggles the "due:" prefix.
func styledDate(d *model.ThingsDate, deadline bool) string {
	if d == nil {
		return ""
	}
	text := d.String()
	if deadline {
		text = "due:" + text
	}

	today := startOfDay(nowFn())
	target := startOfDay(d.ToTime())
	switch {
	case target.Before(today):
		return dateOverdueStyle.Render(text)
	case target.Equal(today):
		return dateTodayStyle.Render(text)
	case target.Sub(today) <= 3*24*time.Hour:
		return dateSoonStyle.Render(text)
	default:
		return dimStyle.Render(text)
	}
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func styledTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return tagStyle.Render("[" + strings.Join(tags, ", ") + "]")
}

// termWidth returns the terminal width, falling back to 120 for non-TTY
// (pipes, tests).
func termWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 120
}
